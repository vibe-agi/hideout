package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

var (
	ErrAttachBlocked          = errors.New("lifecycle attach is blocked pending reconciliation; restart the local control plane (hideout daemon stop, then retry) so it re-reconciles the environment")
	ErrReconciliationInFlight = errors.New("lifecycle reconciliation is in flight")
	ErrStopInFlight           = errors.New("lifecycle stop is in flight")
	ErrCoordinatorClosed      = errors.New("lifecycle coordinator is closed")
	// ErrMutationBlockedByActivity reports live environment activity (attached
	// sessions, an in-flight stop, or another mutation) refusing a destructive
	// mutation. A forced mutation may cancel the environment's sessions and
	// retry within a bounded window; everything else treats it as terminal.
	ErrMutationBlockedByActivity = errors.New("lifecycle destructive mutation is blocked by environment activity")
)

// AttachBlockedError preserves the bounded reconciliation reason without
// exposing provider identities or control material. Callers may translate a
// known reason into an existing product recovery code while errors.Is still
// classifies it as ErrAttachBlocked.
type AttachBlockedError struct {
	ReasonCode string
}

func (e *AttachBlockedError) Error() string {
	if e != nil && e.ReasonCode == "owner-requires-explicit-recovery" {
		return ErrAttachBlocked.Error() + ": explicit recovery is required"
	}
	return ErrAttachBlocked.Error()
}

func (*AttachBlockedError) Unwrap() error { return ErrAttachBlocked }

// AttachRequest contains only stable identities and independently observed
// backend state. It deliberately excludes backend handles and credentials.
type AttachRequest struct {
	EnvironmentID string
	InstanceName  string
	SessionID     string
	Observation   backend.LifecycleObservation
}

// EstablishmentRequest contains the opaque identities needed to reserve an
// attach boundary before Manager publishes session runtime state.
type EstablishmentRequest struct {
	EnvironmentID string
	SessionID     string
}

// RegistrationSpec describes one resource before its effect becomes usable.
// IDs must be opaque and must not contain paths or control material.
type RegistrationSpec struct {
	Kind                 ResourceKind
	ID                   string
	OwnerKind            string
	OwnerID              string
	State                ResourceState
	Dependencies         []DependencySpec
	Persistence          PersistenceClass
	ClosePolicy          ClosePolicy
	PossibleVMDependency bool
}

type FactSpec struct {
	Kind  ResourceKind
	ID    string
	Class FactClass
}

// Registrar is the narrow interface injected into Manager. The daemon owns
// the implementation; Manager remains the authority that creates and closes
// the represented effects. ReserveAttach may wait for an already-invoked
// automatic stop to reach a proved result; callers must collect their backend
// observation only after the reservation returns.
type Registrar interface {
	ReserveAttach(context.Context, EstablishmentRequest) (EstablishmentReservation, error)
	BeginAttach(context.Context, AttachRequest) (Registration, error)
}

// EstablishmentReservation is daemon-local coordination, not durable session
// authority. Prepare proves the backend incarnation, Promote replaces the
// reservation with a normal registration, and Abort releases only this claim.
type EstablishmentReservation interface {
	Prepare(context.Context, AttachRequest) (EnvironmentRef, error)
	Promote(context.Context) (Registration, error)
	Abort(context.Context, error) error
}

// ActiveAttachObservationProvider is an optional warm-sibling fast path. It
// returns only current-daemon in-memory proof while another registered handle
// pins the same incarnation; persisted journal data is never returned as
// liveness proof.
type ActiveAttachObservationProvider interface {
	ActiveAttachObservation(context.Context, string, string) (backend.LifecycleObservation, bool)
}

// SessionResourceRegistrar is the daemon-owned dynamic registration surface
// used by Manager API operations that complete outside the original run call
// stack. It records lifecycle metadata only and grants no provider authority.
type SessionResourceRegistrar interface {
	RegisterSessionResource(context.Context, string, RegistrationSpec) (ResourceRef, error)
	TransitionSessionResource(context.Context, string, ResourceRef, ResourceState) error
	ReleaseSessionResource(context.Context, string, ResourceRef, error) error
	RecordSessionFact(context.Context, string, FactSpec) error
}

// Registration is scoped to one run session and one backend incarnation.
type Registration interface {
	Incarnation() EnvironmentRef
	Root() ResourceRef
	Session() ResourceRef
	Commit(context.Context) error
	BindBoot(context.Context, string) error
	Register(context.Context, RegistrationSpec) (ResourceRef, error)
	Transition(context.Context, ResourceRef, ResourceState) error
	Release(context.Context, ResourceRef, error) error
	RecordFact(context.Context, FactSpec) error
	Finish(context.Context, error) error
}

type registration struct {
	coordinator *Coordinator
	environment string
	id          string
	incarnation EnvironmentRef
	root        ResourceRef
	session     ResourceRef

	mu        sync.Mutex
	refs      []ResourceRef
	committed bool
	finished  bool
}

func (r *registration) Incarnation() EnvironmentRef { return r.incarnation }
func (r *registration) Root() ResourceRef           { return r.root }
func (r *registration) Session() ResourceRef        { return r.session }

// Commit makes the currently planned provider subgraph durable. A provider
// must plan its complete dependency shape before this barrier and must not
// make that provider's effect usable before the barrier succeeds. A later
// startup phase may add and synchronously persist another provider subgraph.
func (r *registration) Commit(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return errors.New("lifecycle registration is finished")
	}
	if r.committed {
		return nil
	}
	if err := r.coordinator.commitRegistration(r.environment, r.id); err != nil {
		return err
	}
	r.committed = true
	return nil
}

func (r *registration) BindBoot(ctx context.Context, bootID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return errors.New("lifecycle registration is finished")
	}
	if !r.committed {
		if err := r.coordinator.commitRegistration(r.environment, r.id); err != nil {
			return err
		}
		r.committed = true
	}
	incarnation, err := r.coordinator.bindBoot(r.environment, r.id, bootID)
	if err == nil {
		r.incarnation = incarnation
	}
	return err
}

func (r *registration) Register(ctx context.Context, spec RegistrationSpec) (ResourceRef, error) {
	if err := contextError(ctx); err != nil {
		return ResourceRef{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return ResourceRef{}, errors.New("lifecycle registration is finished")
	}
	ref, err := r.coordinator.register(r.environment, r.id, spec)
	if err != nil {
		return ResourceRef{}, err
	}
	if !containsRef(r.refs, ref) {
		r.refs = append(r.refs, ref)
	}
	return ref, nil
}

func (r *registration) Transition(ctx context.Context, ref ResourceRef, state ResourceState) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || !containsRef(r.refs, ref) {
		return errors.New("lifecycle resource is not owned by this registration")
	}
	if !r.committed {
		if err := r.coordinator.commitRegistration(r.environment, r.id); err != nil {
			return err
		}
		r.committed = true
	}
	return r.coordinator.transition(r.environment, r.id, ref, state)
}

func (r *registration) Release(ctx context.Context, ref ResourceRef, cleanupErr error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || !r.committed || !containsRef(r.refs, ref) {
		return errors.New("lifecycle resource is not owned by this registration")
	}
	return r.coordinator.release(r.environment, r.id, ref, cleanupErr)
}

func (r *registration) RecordFact(ctx context.Context, spec FactSpec) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return errors.New("lifecycle registration is finished")
	}
	if !r.committed {
		if err := r.coordinator.commitRegistration(r.environment, r.id); err != nil {
			return err
		}
		r.committed = true
	}
	return r.coordinator.recordFact(r.environment, r.id, spec)
}

func (r *registration) Finish(ctx context.Context, cleanupErr error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return nil
	}
	r.finished = true
	if !r.committed {
		r.mu.Unlock()
		return r.coordinator.abortRegistration(r.environment, r.id)
	}
	refs, err := r.coordinator.beginFinishRegistration(r.environment, r.id)
	r.mu.Unlock()
	if err != nil {
		return err
	}

	var result error
	for _, ref := range refs {
		result = errors.Join(result, r.coordinator.release(r.environment, r.id, ref, cleanupErr))
	}
	result = errors.Join(result, r.coordinator.finishRegistration(r.environment, r.id, errors.Join(cleanupErr, result)))
	return result
}

// beginFinishRegistration seals the registration surface before taking the
// release snapshot. This prevents a dynamic Manager operation from appearing
// after the snapshot and escaping final lifecycle release.
func (c *Coordinator) beginFinishRegistration(environmentID, handleID string) ([]ResourceRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return nil, ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] {
		return nil, errors.New("lifecycle registration is not active")
	}
	state.closing[handleID] = true
	return releaseOrderForHandleLocked(state, handleID), nil
}

func (c *Coordinator) releaseOrderForHandle(environmentID, handleID string) []ResourceRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] {
		return nil
	}
	return releaseOrderForHandleLocked(state, handleID)
}

func releaseOrderForHandleLocked(state *registryEnvironment, handleID string) []ResourceRef {
	owned := map[string]ResourceRef{}
	for _, ref := range state.resourceOrder[handleID] {
		// A provider may release its authority before the enclosing run finishes.
		// Keep the historical order entry for idempotence, but do not release the
		// same shared resource a second time from Finish.
		if !state.resourceUsers[ref.Key()][handleID] {
			continue
		}
		owned[ref.Key()] = ref
	}
	resources := map[string]Resource{}
	for _, resource := range state.journal.Resources {
		resources[resource.Ref.Key()] = resource
	}
	visited := map[string]bool{}
	postorder := make([]ResourceRef, 0, len(owned))
	var visit func(ResourceRef)
	visit = func(ref ResourceRef) {
		if visited[ref.Key()] {
			return
		}
		visited[ref.Key()] = true
		for _, dependency := range resources[ref.Key()].Dependencies {
			if ownedRef, ok := owned[dependency.Ref.Key()]; ok {
				visit(ownedRef)
			}
		}
		postorder = append(postorder, ref)
	}
	for _, ref := range state.resourceOrder[handleID] {
		if ownedRef, ok := owned[ref.Key()]; ok {
			visit(ownedRef)
		}
	}
	for left, right := 0, len(postorder)-1; left < right; left, right = left+1, right-1 {
		postorder[left], postorder[right] = postorder[right], postorder[left]
	}
	return postorder
}

func (c *Coordinator) sessionEnvironment(sessionID string, allowClosing bool) (string, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return "", 0, ErrCoordinatorClosed
	}
	match := ""
	var generation uint64
	for environmentID, state := range c.environments {
		if !state.handles[sessionID] || !state.committed[sessionID] || (!allowClosing && state.closing[sessionID]) {
			continue
		}
		if match != "" {
			return "", 0, errors.New("lifecycle session identity is ambiguous")
		}
		match = environmentID
		generation = state.journal.StartGeneration
	}
	if match == "" || generation == 0 {
		return "", 0, errors.New("lifecycle session registration is not active")
	}
	return match, generation, nil
}

func (c *Coordinator) RegisterSessionResource(ctx context.Context, sessionID string, spec RegistrationSpec) (ResourceRef, error) {
	if err := contextError(ctx); err != nil {
		return ResourceRef{}, err
	}
	environmentID, generation, err := c.sessionEnvironment(sessionID, false)
	if err != nil {
		return ResourceRef{}, err
	}
	for index := range spec.Dependencies {
		if spec.Dependencies[index].Ref.Generation == 0 {
			spec.Dependencies[index].Ref.Generation = generation
		}
	}
	return c.register(environmentID, sessionID, spec)
}

func (c *Coordinator) TransitionSessionResource(ctx context.Context, sessionID string, ref ResourceRef, state ResourceState) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	environmentID, _, err := c.sessionEnvironment(sessionID, true)
	if err != nil {
		return err
	}
	return c.transition(environmentID, sessionID, ref, state)
}

func (c *Coordinator) ReleaseSessionResource(ctx context.Context, sessionID string, ref ResourceRef, cleanupErr error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	environmentID, _, err := c.sessionEnvironment(sessionID, true)
	if err != nil {
		return err
	}
	return c.release(environmentID, sessionID, ref, cleanupErr)
}

func (c *Coordinator) RecordSessionFact(ctx context.Context, sessionID string, spec FactSpec) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	environmentID, _, err := c.sessionEnvironment(sessionID, false)
	if err != nil {
		return err
	}
	return c.recordFact(environmentID, sessionID, spec)
}

type registryEnvironment struct {
	journal       Journal
	handles       map[string]bool
	establishing  map[string]*establishment
	committed     map[string]bool
	closing       map[string]bool
	mutation      bool
	resourceUsers map[string]map[string]bool
	resourceOrder map[string][]ResourceRef
	terminal      map[string]ResourceState
	timer         timerHandle
	stopCancel    context.CancelFunc
	stopDone      chan struct{}
	deadlineSeq   uint64
	checkpoint    timerHandle
	checkpointSeq uint64
	dirty         bool
	loaded        bool
	blocked       bool
	reconciling   bool
	reconcileDone chan struct{}
	observation   backend.LifecycleObservation
}

func (c *Coordinator) ActiveAttachObservation(ctx context.Context, environmentID, instanceName string) (backend.LifecycleObservation, bool) {
	if contextError(ctx) != nil || !idPattern.MatchString(environmentID) || !idPattern.MatchString(instanceName) {
		return backend.LifecycleObservation{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return backend.LifecycleObservation{}, false
	}
	state, err := c.loadEnvironmentLocked(environmentID)
	if err != nil || state.reconciling || state.blocked || committedHandleCount(state) == 0 ||
		state.journal.Reconciliation.DaemonInstanceID != c.daemonID ||
		state.journal.Reconciliation.State != "complete" {
		return backend.LifecycleObservation{}, false
	}
	observation := state.observation
	if observation.State != backend.LifecycleRunning || observation.InstanceName != instanceName || observation.Validate() != nil {
		return backend.LifecycleObservation{}, false
	}
	return observation, true
}

func committedHandleCount(state *registryEnvironment) int {
	count := 0
	for handleID := range state.handles {
		if state.committed[handleID] {
			count++
		}
	}
	return count
}

func (c *Coordinator) BeginAttach(ctx context.Context, request AttachRequest) (Registration, error) {
	// The one-shot compatibility API receives its backend observation before
	// serialization. It must fail fast rather than wait across a stop and then
	// consume stale liveness evidence. Two-phase callers reserve first and
	// observe only after the reservation has fenced automatic stop.
	reservation, err := c.reserveAttach(ctx, EstablishmentRequest{
		EnvironmentID: request.EnvironmentID,
		SessionID:     request.SessionID,
	}, false)
	if err != nil {
		return nil, err
	}
	if _, err := reservation.Prepare(ctx, request); err != nil {
		_ = reservation.Abort(context.Background(), err)
		return nil, err
	}
	registration, err := reservation.Promote(ctx)
	if err != nil {
		_ = reservation.Abort(context.Background(), err)
		return nil, err
	}
	return registration, nil
}

func observationMatchesIncarnation(incarnation *EnvironmentRef, observation backend.LifecycleObservation) bool {
	if incarnation == nil || incarnation.InstanceName != observation.InstanceName {
		return false
	}
	switch observation.State {
	case backend.LifecycleRunning:
		return incarnation.BootID != "" && incarnation.BootID == observation.BootID
	case backend.LifecycleStopped, backend.LifecycleAbsent:
		return incarnation.BootID == ""
	default:
		return false
	}
}

func (c *Coordinator) register(environmentID, handleID string, spec RegistrationSpec) (ResourceRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ResourceRef{}, ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] || state.closing[handleID] || state.journal.Incarnation == nil {
		return ResourceRef{}, errors.New("lifecycle registration is not active")
	}
	if spec.State == "" {
		spec.State = StatePlanned
	}
	if spec.OwnerID == "" {
		spec.OwnerID = handleID
	}
	ref := ResourceRef{Kind: spec.Kind, ID: spec.ID, Generation: state.journal.StartGeneration}
	resource := Resource{
		Ref:                  ref,
		Owner:                OwnerRef{Kind: spec.OwnerKind, ID: spec.OwnerID, Generation: state.journal.StartGeneration},
		State:                spec.State,
		Dependencies:         append([]DependencySpec(nil), spec.Dependencies...),
		Persistence:          spec.Persistence,
		ClosePolicy:          spec.ClosePolicy,
		PossibleVMDependency: spec.PossibleVMDependency,
		UpdatedAt:            c.nowUTC(),
	}
	if err := addOrJoinResource(state, handleID, resource); err != nil {
		return ResourceRef{}, err
	}
	if state.committed[handleID] {
		if err := c.persistNowLocked(state); err != nil {
			removeResourceUser(state, handleID, ref)
			removeResourceOrder(state, handleID, ref)
			return ResourceRef{}, err
		}
	}
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "resource-registered", At: c.nowUTC()})
	return ref, nil
}

func (c *Coordinator) commitRegistration(environmentID, handleID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] || state.closing[handleID] {
		return errors.New("lifecycle registration is not active")
	}
	if state.committed[handleID] {
		return nil
	}
	if err := c.persistNowLocked(state); err != nil {
		return err
	}
	state.committed[handleID] = true
	return nil
}

func (c *Coordinator) abortRegistration(environmentID, handleID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] {
		return nil
	}
	for _, ref := range releaseOrderForHandleLocked(state, handleID) {
		removeResourceUser(state, handleID, ref)
	}
	delete(state.handles, handleID)
	delete(state.committed, handleID)
	delete(state.closing, handleID)
	delete(state.resourceOrder, handleID)
	if len(state.handles) == 0 {
		return c.scheduleIfIdleLocked(environmentID, state)
	}
	return nil
}

func (c *Coordinator) recordFact(environmentID, handleID string, spec FactSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] || state.closing[handleID] || state.journal.Incarnation == nil {
		return errors.New("lifecycle fact registration is not active")
	}
	fact := Fact{
		Kind: spec.Kind, ID: spec.ID, Class: spec.Class,
		Generation: state.journal.StartGeneration, RecordedAt: c.nowUTC(),
	}
	if err := validateFact(fact); err != nil {
		return err
	}
	previousFacts := append([]Fact(nil), state.journal.Facts...)
	state.journal.Facts = append(state.journal.Facts, fact)
	if overflow := len(state.journal.Facts) - maxJournalFacts; overflow > 0 {
		state.journal.Facts = append([]Fact(nil), state.journal.Facts[overflow:]...)
	}
	if err := c.persistNowLocked(state); err != nil {
		state.journal.Facts = previousFacts
		return err
	}
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "fact-recorded", At: c.nowUTC()})
	return nil
}

func (c *Coordinator) transition(environmentID, handleID string, ref ResourceRef, target ResourceState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, resource, index, err := c.resourceLocked(environmentID, handleID, ref)
	if err != nil {
		if terminal, ok := c.terminalStateLocked(environmentID, ref); ok && terminal == target {
			return nil
		}
		return err
	}
	if err := ValidateTransition(resource.State, target); err != nil {
		return err
	}
	resource.State = target
	resource.UpdatedAt = c.nowUTC()
	if target == StateReleased || target == StateFailed {
		state.terminal[ref.Key()] = target
		state.journal.Resources = append(state.journal.Resources[:index], state.journal.Resources[index+1:]...)
		delete(state.resourceUsers, ref.Key())
	} else {
		state.journal.Resources[index] = resource
	}
	if target == StateOrphaned {
		if err := c.persistNowLocked(state); err != nil {
			return err
		}
	} else {
		c.checkpointLaterLocked(environmentID, state)
	}
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "resource-transitioned", At: c.nowUTC()})
	return nil
}

func (c *Coordinator) release(environmentID, handleID string, ref ResourceRef, cleanupErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if ok && state.handles[handleID] && containsRef(state.resourceOrder[handleID], ref) &&
		!state.resourceUsers[ref.Key()][handleID] {
		// Release is per-handle idempotent. The resource may still be active for a
		// sibling handle, so the global terminal map alone cannot prove this case.
		return nil
	}
	state, resource, index, err := c.resourceLocked(environmentID, handleID, ref)
	if err != nil {
		if _, terminal := c.terminalStateLocked(environmentID, ref); terminal {
			return nil
		}
		return err
	}
	users := state.resourceUsers[ref.Key()]
	delete(users, handleID)
	if len(users) != 0 {
		c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "resource-released", At: c.nowUTC()})
		return nil
	}
	delete(state.resourceUsers, ref.Key())
	resource.UpdatedAt = c.nowUTC()
	if cleanupErr != nil {
		if resource.State != StateOrphaned {
			if transitionErr := ValidateTransition(resource.State, StateOrphaned); transitionErr != nil {
				return transitionErr
			}
		}
		resource.State = StateOrphaned
		resource.PossibleVMDependency = true
		state.journal.Resources[index] = resource
		if err := c.persistNowLocked(state); err != nil {
			return err
		}
		c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "resource-orphaned", ReasonCode: "cleanup-unproved", At: c.nowUTC()})
		return nil
	}
	if resource.State == StatePlanned || resource.State == StateStarting {
		state.terminal[ref.Key()] = StateFailed
		state.journal.Resources = append(state.journal.Resources[:index], state.journal.Resources[index+1:]...)
		c.checkpointLaterLocked(environmentID, state)
		c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "resource-released", At: c.nowUTC()})
		return nil
	}
	if resource.State != StateDraining {
		if err := ValidateTransition(resource.State, StateDraining); err != nil {
			return err
		}
		resource.State = StateDraining
	}
	state.terminal[ref.Key()] = StateReleased
	state.journal.Resources = append(state.journal.Resources[:index], state.journal.Resources[index+1:]...)
	c.checkpointLaterLocked(environmentID, state)
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "resource-released", At: c.nowUTC()})
	return nil
}

func (c *Coordinator) resourceLocked(environmentID, handleID string, ref ResourceRef) (*registryEnvironment, Resource, int, error) {
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] || !state.resourceUsers[ref.Key()][handleID] {
		return nil, Resource{}, -1, errors.New("lifecycle resource registration is not active")
	}
	for index, resource := range state.journal.Resources {
		if resource.Ref.Key() == ref.Key() {
			return state, resource, index, nil
		}
	}
	return nil, Resource{}, -1, errors.New("lifecycle resource is absent")
}

func (c *Coordinator) terminalStateLocked(environmentID string, ref ResourceRef) (ResourceState, bool) {
	state, ok := c.environments[environmentID]
	if !ok {
		return "", false
	}
	value, ok := state.terminal[ref.Key()]
	return value, ok
}

func addOrJoinResource(state *registryEnvironment, handleID string, resource Resource) error {
	for _, existing := range state.journal.Resources {
		if existing.Ref.Key() != resource.Ref.Key() {
			continue
		}
		if !resourcesJoinCompatible(existing, resource) {
			return errors.New("conflicting lifecycle resource registration")
		}
		if state.resourceUsers[resource.Ref.Key()] == nil {
			state.resourceUsers[resource.Ref.Key()] = map[string]bool{}
		}
		state.resourceUsers[resource.Ref.Key()][handleID] = true
		if !containsRef(state.resourceOrder[handleID], resource.Ref) {
			state.resourceOrder[handleID] = append(state.resourceOrder[handleID], resource.Ref)
		}
		return nil
	}
	state.journal.Resources = append(state.journal.Resources, resource)
	if err := ValidateGraph(state.journal.Resources, true); err != nil {
		state.journal.Resources = state.journal.Resources[:len(state.journal.Resources)-1]
		return err
	}
	state.resourceUsers[resource.Ref.Key()] = map[string]bool{handleID: true}
	state.resourceOrder[handleID] = append(state.resourceOrder[handleID], resource.Ref)
	return nil
}

func resourcesJoinCompatible(existing, joining Resource) bool {
	if reflect.DeepEqual(resourceWithoutTime(existing), resourceWithoutTime(joining)) {
		return true
	}
	// A new consumer must durably declare its dependency before it verifies or
	// starts a shared provider. Joining an already-live provider with a planned
	// declaration is safe only when every part of the resource contract except
	// state is identical. The existing state is never downgraded.
	if joining.State != StatePlanned || (existing.State != StatePlanned && existing.State != StateStarting && existing.State != StateActive) {
		return false
	}
	existing.State = StatePlanned
	joining.State = StatePlanned
	return reflect.DeepEqual(resourceWithoutTime(existing), resourceWithoutTime(joining))
}

func removeResourceUser(state *registryEnvironment, handleID string, ref ResourceRef) {
	users := state.resourceUsers[ref.Key()]
	delete(users, handleID)
	if len(users) != 0 {
		return
	}
	delete(state.resourceUsers, ref.Key())
	for index, resource := range state.journal.Resources {
		if resource.Ref.Key() == ref.Key() {
			state.journal.Resources = append(state.journal.Resources[:index], state.journal.Resources[index+1:]...)
			return
		}
	}
}

func removeResourceOrder(state *registryEnvironment, handleID string, ref ResourceRef) {
	refs := state.resourceOrder[handleID]
	for index, candidate := range refs {
		if candidate.Key() != ref.Key() {
			continue
		}
		state.resourceOrder[handleID] = append(refs[:index], refs[index+1:]...)
		return
	}
}

func resourceWithoutTime(resource Resource) Resource {
	resource.UpdatedAt = time.Time{}
	return resource
}

func containsRef(refs []ResourceRef, target ResourceRef) bool {
	for _, ref := range refs {
		if ref.Key() == target.Key() {
			return true
		}
	}
	return false
}

func sortedResources(resources []Resource) []Resource {
	out := append([]Resource(nil), resources...)
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.Key() < out[j].Ref.Key() })
	return out
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func attemptBlocksAttach(attempt *StopAttempt) bool {
	if attempt == nil {
		return false
	}
	switch attempt.State {
	case "planned", "draining", "invoked", "observing", "unknown":
		return true
	default:
		return false
	}
}
