package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const DefaultIdleGrace = 15 * time.Second

const coordinatorCloseWait = 3 * time.Second

// The durable planned graph is the crash-recovery envelope. Routine state
// transitions are coalesced outside the latency-sensitive one-shot command
// path, while still checkpointing well before the 15-second idle-stop grace.
const lifecycleCheckpointDelay = 500 * time.Millisecond

type timerHandle interface {
	Stop() bool
}

type afterFunc func(time.Duration, func()) timerHandle

type StopRequest struct {
	AttemptID   string
	Mode        string
	Incarnation EnvironmentRef
}

type StopResult struct {
	Observation     backend.LifecycleObservation
	ReasonCode      string
	CleanupUnproved bool
}

type StopFunc func(context.Context, StopRequest) (StopResult, error)

type Event struct {
	EnvironmentID string
	Generation    uint64
	Kind          string
	ReasonCode    string
	At            time.Time
	Status        Status
}

type CoordinatorOptions struct {
	Store     JournalStore
	DaemonID  string
	IdleGrace time.Duration
	Now       func() time.Time
	AfterFunc func(time.Duration, func()) timerHandle
	Stop      StopFunc
	Publish   func(Event)
	Enabled   bool
}

// Coordinator is the daemon-owned single writer for lifecycle metadata and
// stop-attempt serialization. It owns no provider or backend authority.
type Coordinator struct {
	mu           sync.Mutex
	store        JournalStore
	daemonID     string
	idleGrace    time.Duration
	now          func() time.Time
	after        afterFunc
	stop         StopFunc
	publish      func(Event)
	enabled      bool
	environments map[string]*registryEnvironment
	stopWG       sync.WaitGroup
	closing      bool
	closed       bool
}

func NewCoordinator(options CoordinatorOptions) (*Coordinator, error) {
	if options.Store.Root == "" || !idPattern.MatchString(options.DaemonID) {
		return nil, errors.New("lifecycle coordinator identity is invalid")
	}
	grace := options.IdleGrace
	if grace == 0 {
		grace = DefaultIdleGrace
	}
	if grace < time.Millisecond || grace > time.Hour {
		return nil, errors.New("lifecycle idle grace is out of bounds")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	after := options.AfterFunc
	if after == nil {
		after = func(delay time.Duration, fn func()) timerHandle { return time.AfterFunc(delay, fn) }
	}
	return &Coordinator{
		store: options.Store, daemonID: options.DaemonID, idleGrace: grace,
		now: now, after: after, stop: options.Stop, publish: options.Publish,
		enabled: options.Enabled, environments: map[string]*registryEnvironment{},
	}, nil
}

func (c *Coordinator) nowUTC() time.Time { return c.now().UTC() }

func (c *Coordinator) finishRegistration(environmentID, handleID string, cleanupErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] {
		return nil
	}
	delete(state.handles, handleID)
	delete(state.committed, handleID)
	delete(state.closing, handleID)
	delete(state.resourceOrder, handleID)
	if cleanupErr != nil {
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "cleanup-unproved", c.nowUTC())
		if err := c.persistLocked(state); err != nil {
			return err
		}
		c.emitLocked(Event{EnvironmentID: environmentID, Generation: state.journal.StartGeneration, Kind: "reconciliation-blocked", ReasonCode: "cleanup-unproved", At: c.nowUTC()})
		return nil
	}
	if len(state.handles) != 0 {
		c.checkpointLaterLocked(environmentID, state)
		return nil
	}
	return c.scheduleIfIdleLocked(environmentID, state)
}

func (c *Coordinator) scheduleIfIdleLocked(environmentID string, state *registryEnvironment) error {
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	if state.blocked || state.journal.Incarnation == nil || state.journal.Incarnation.BootID == "" {
		return c.persistLocked(state)
	}
	observation, err := currentIncarnationObservation(state)
	if err != nil {
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "backend-observation-unproved", c.nowUTC())
		return c.persistLocked(state)
	}
	evaluation, err := EvaluateAutoStop(EvaluationInput{
		Incarnation: *state.journal.Incarnation, Resources: state.journal.Resources,
		Observation: observation, GraceExpired: false, ReconciliationComplete: state.journal.Reconciliation.State == "complete",
		CurrentDaemonOwnsAttempt: true,
	})
	if err != nil {
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "lifecycle-graph-invalid", c.nowUTC())
		_ = c.persistLocked(state)
		return err
	}
	if len(evaluation.Pins) != 0 || len(evaluation.Drains) != 0 {
		return c.persistLocked(state)
	}
	state.deadlineSeq++
	if state.deadlineSeq == 0 {
		state.deadlineSeq = 1
	}
	now := c.nowUTC()
	deadline := now.Add(c.idleGrace)
	incarnation := *state.journal.Incarnation
	sequence := state.deadlineSeq
	state.journal.IdleDeadline = &IdleDeadline{
		Incarnation: incarnation, DaemonInstanceID: c.daemonID,
		ScheduledAt: now, Deadline: deadline, Generation: sequence,
	}
	c.checkpointLaterLocked(environmentID, state)
	state.timer = c.after(c.idleGrace, func() { c.expire(environmentID, incarnation, sequence) })
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: incarnation.StartGeneration, Kind: "idle-grace-started", At: now})
	return nil
}

func (c *Coordinator) cancelDeadlineLocked(state *registryEnvironment) error {
	if !c.cancelDeadlineForAttachLocked(state) {
		return nil
	}
	return c.persistNowLocked(state)
}

func (c *Coordinator) cancelDeadlineForAttachLocked(state *registryEnvironment) bool {
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	if state.journal.IdleDeadline == nil {
		return false
	}
	incarnation := state.journal.IdleDeadline.Incarnation
	state.deadlineSeq++
	state.journal.IdleDeadline = nil
	c.emitLocked(Event{EnvironmentID: incarnation.EnvironmentID, Generation: incarnation.StartGeneration, Kind: "idle-grace-cancelled", At: c.nowUTC()})
	state.dirty = true
	return true
}

func (c *Coordinator) expire(environmentID string, incarnation EnvironmentRef, sequence uint64) {
	c.mu.Lock()
	state, ok := c.environments[environmentID]
	if !ok || c.closing || c.closed || state.mutation || state.journal.IdleDeadline == nil || state.journal.IdleDeadline.Generation != sequence ||
		!state.journal.IdleDeadline.Incarnation.Equal(incarnation) || len(state.handles) != 0 {
		c.mu.Unlock()
		return
	}
	observation, observationErr := currentIncarnationObservation(state)
	if observationErr != nil {
		state.journal.IdleDeadline = nil
		state.timer = nil
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "backend-observation-unproved", c.nowUTC())
		_ = c.persistLocked(state)
		c.emitLocked(Event{
			EnvironmentID: environmentID,
			Generation:    incarnation.StartGeneration,
			Kind:          "reconciliation-blocked",
			ReasonCode:    "backend-observation-unproved",
			At:            c.nowUTC(),
		})
		c.mu.Unlock()
		return
	}
	evaluation, err := EvaluateAutoStop(EvaluationInput{
		Incarnation: incarnation, Resources: state.journal.Resources, Observation: observation,
		GraceExpired: true, ReconciliationComplete: state.journal.Reconciliation.State == "complete",
		CurrentDaemonOwnsAttempt: true,
	})
	if err != nil || !evaluation.Allowed || len(evaluation.Drains) != 0 {
		state.blocked = err != nil
		if err != nil {
			state.journal.Reconciliation = blockedReconciliation(c.daemonID, "lifecycle-graph-invalid", c.nowUTC())
		}
		_ = c.persistLocked(state)
		c.mu.Unlock()
		return
	}
	state.journal.IdleDeadline = nil
	state.timer = nil
	attemptID := fmt.Sprintf("stop-%d-%d", incarnation.StartGeneration, sequence)
	state.journal.StopAttempt = &StopAttempt{
		ID: attemptID, Incarnation: incarnation, DaemonInstanceID: c.daemonID,
		Mode: "automatic", State: "planned", StartedAt: c.nowUTC(),
	}
	if err := c.persistLocked(state); err != nil {
		state.blocked = true
		c.mu.Unlock()
		return
	}
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: incarnation.StartGeneration, Kind: "idle-grace-expired", At: c.nowUTC()})
	if !c.enabled || c.stop == nil {
		state.journal.StopAttempt = nil
		_ = c.persistLocked(state)
		c.mu.Unlock()
		return
	}
	state.journal.StopAttempt.State = "invoked"
	_ = c.persistLocked(state)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	state.stopCancel = cancel
	c.stopWG.Add(1)
	c.mu.Unlock()

	result, stopErr := c.stop(ctx, StopRequest{AttemptID: attemptID, Mode: "automatic", Incarnation: incarnation})
	cancel()
	c.commitStopResult(environmentID, attemptID, incarnation, result, stopErr)
	c.stopWG.Done()
}

func (c *Coordinator) commitStopResult(environmentID, attemptID string, incarnation EnvironmentRef, result StopResult, stopErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	state, ok := c.environments[environmentID]
	if !ok || state.journal.StopAttempt == nil || state.journal.StopAttempt.ID != attemptID || !state.journal.StopAttempt.Incarnation.Equal(incarnation) {
		return
	}
	now := c.nowUTC()
	if state.stopCancel != nil {
		state.stopCancel()
		state.stopCancel = nil
	}
	observation := result.Observation
	if observation.ObservedAt.IsZero() || observation.Validate() != nil || observation.InstanceName != incarnation.InstanceName {
		observation = backend.LifecycleObservation{State: backend.LifecycleUnknown, InstanceName: incarnation.InstanceName, ObservedAt: now, ReasonCode: "stop-result-unavailable"}
	}
	state.journal.StopAttempt.Observation = &backendObservationSnapshot{State: string(observation.State), ObservedAt: observation.ObservedAt, ReasonCode: boundedReason(observation.ReasonCode)}
	if observation.State == backend.LifecycleStopped || observation.State == backend.LifecycleAbsent {
		state.observation = observation
		state.journal.StopAttempt.State = "committed"
		state.journal.Resources = resourcesAfterRootStop(state.journal.Resources, result.CleanupUnproved, now)
		state.journal.Incarnation = nil
		state.resourceUsers = map[string]map[string]bool{}
		state.terminal = map[string]ResourceState{}
		state.blocked = result.CleanupUnproved
		if result.CleanupUnproved {
			state.journal.Reconciliation = blockedReconciliation(c.daemonID, "cleanup-unproved", now)
		} else {
			state.journal.Reconciliation = Reconciliation{DaemonInstanceID: c.daemonID, State: "complete", ObservedAt: now}
		}
		c.emitLocked(Event{EnvironmentID: environmentID, Generation: incarnation.StartGeneration, Kind: "environment-stopped", ReasonCode: boundedReason(result.ReasonCode), At: now})
	} else {
		state.observation = observation
		state.journal.StopAttempt.State = "unknown"
		state.blocked = true
		reason := result.ReasonCode
		if reason == "" {
			reason = observation.ReasonCode
		}
		c.emitLocked(Event{EnvironmentID: environmentID, Generation: incarnation.StartGeneration, Kind: "stop-unknown", ReasonCode: boundedReason(reason), At: now})
	}
	_ = c.persistLocked(state)
}

func resourcesAfterRootStop(resources []Resource, cleanupUnproved bool, now time.Time) []Resource {
	out := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		if resource.Ref.Kind == KindBackendIncarnation {
			continue
		}
		keep := false
		if cleanupUnproved && resource.IsPossiblyLive() {
			resource.State = StateOrphaned
			resource.PossibleVMDependency = true
			resource.UpdatedAt = now
			keep = true
		}
		if keep {
			resource.Dependencies = nil
			out = append(out, resource)
		}
	}
	return out
}

// StopExplicit serializes an operator-requested non-destructive stop with
// attach and automatic stop. The callback remains the sole backend authority.
func (c *Coordinator) StopExplicit(ctx context.Context, environmentID string) (Status, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !idPattern.MatchString(environmentID) {
		return Status{}, errors.New("lifecycle explicit stop identity is invalid")
	}
	c.mu.Lock()
	if c.closing || c.closed {
		c.mu.Unlock()
		return Status{}, ErrCoordinatorClosed
	}
	state, err := c.loadEnvironmentLocked(environmentID)
	if err != nil {
		c.mu.Unlock()
		return Status{}, err
	}
	if state.reconciling {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, ErrReconciliationInFlight
	}
	if len(state.handles) != 0 {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, errors.New("lifecycle explicit stop is blocked by active sessions")
	}
	if state.mutation {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, ErrStopInFlight
	}
	if attemptBlocksAttach(state.journal.StopAttempt) {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, ErrStopInFlight
	}
	if state.journal.Incarnation == nil || state.journal.Incarnation.BootID == "" {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, errors.New("lifecycle explicit stop lacks an observed backend incarnation")
	}
	recoveryReason := state.journal.Reconciliation.ReasonCode
	recoveryBlocked := state.blocked || state.journal.Reconciliation.State != "complete"
	if recoveryBlocked && recoveryReason != "cleanup-unproved" && recoveryReason != "owner-requires-explicit-recovery" {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, errors.New("lifecycle explicit stop is blocked by unclassified provider state")
	}
	recoverableOrphans := 0
	for _, resource := range state.journal.Resources {
		if resource.Ref.Kind == KindBackendIncarnation || !resource.IsPossiblyLive() {
			continue
		}
		// Explicit recovery may resolve only resources already classified as
		// orphaned. A known-active pin or provider drain still has a current owner
		// and must complete its ordinary cleanup path first.
		if resource.State != StateOrphaned {
			status := c.statusLocked(environmentID, state)
			c.mu.Unlock()
			return status, errors.New("lifecycle explicit stop is blocked by a live managed resource")
		}
		descriptor, ok := Lookup(resource.Ref.Kind)
		if !ok || resource.ClosePolicy != CloseCoTerminateWithRoot || !slices.Contains(descriptor.ClosePolicies, CloseCoTerminateWithRoot) {
			status := c.statusLocked(environmentID, state)
			c.mu.Unlock()
			return status, errors.New("lifecycle explicit stop is blocked by an orphan that requires provider cleanup")
		}
		recoverableOrphans++
	}
	if recoveryBlocked && recoverableOrphans == 0 {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, errors.New("lifecycle explicit stop has no catalog-approved orphan recovery")
	}
	if err := c.cancelDeadlineLocked(state); err != nil {
		c.mu.Unlock()
		return Status{}, err
	}
	incarnation := *state.journal.Incarnation
	attemptID := fmt.Sprintf("explicit-%d-%d", incarnation.StartGeneration, c.nowUTC().UnixNano())
	state.journal.StopAttempt = &StopAttempt{
		ID: attemptID, Incarnation: incarnation, DaemonInstanceID: c.daemonID,
		Mode: "explicit-recovery", State: "invoked", StartedAt: c.nowUTC(),
	}
	if err := c.persistLocked(state); err != nil {
		c.mu.Unlock()
		return Status{}, err
	}
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: incarnation.StartGeneration, Kind: "stop-attempt-started", At: c.nowUTC()})
	if c.stop == nil {
		state.journal.StopAttempt = nil
		_ = c.persistLocked(state)
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, errors.New("lifecycle stop authority is unavailable")
	}
	stopCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	state.stopCancel = cancel
	c.stopWG.Add(1)
	c.mu.Unlock()

	result, stopErr := c.stop(stopCtx, StopRequest{AttemptID: attemptID, Mode: "explicit-recovery", Incarnation: incarnation})
	cancel()
	c.commitStopResult(environmentID, attemptID, incarnation, result, stopErr)
	c.stopWG.Done()

	c.mu.Lock()
	state = c.environments[environmentID]
	status := c.statusLocked(environmentID, state)
	c.mu.Unlock()
	if stopErr == nil && status.Activity != ActivityStopped {
		stopErr = errors.New("lifecycle explicit stop did not reach a proved terminal backend state")
	}
	return status, stopErr
}

func (c *Coordinator) persistLocked(state *registryEnvironment) error {
	state.journal.UpdatedAt = c.nowUTC()
	state.journal.Resources = sortedResources(state.journal.Resources)
	return c.store.Write(state.journal)
}

func (c *Coordinator) persistNowLocked(state *registryEnvironment) error {
	if state.checkpoint != nil {
		state.checkpoint.Stop()
		state.checkpoint = nil
		state.checkpointSeq++
	}
	err := c.persistLocked(state)
	if err == nil {
		state.dirty = false
	}
	return err
}

func (c *Coordinator) checkpointLaterLocked(environmentID string, state *registryEnvironment) {
	state.dirty = true
	if state.checkpoint != nil {
		return
	}
	state.checkpointSeq++
	sequence := state.checkpointSeq
	state.checkpoint = time.AfterFunc(lifecycleCheckpointDelay, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		current, ok := c.environments[environmentID]
		if !ok || current != state || current.checkpointSeq != sequence || c.closed {
			return
		}
		current.checkpoint = nil
		if !current.dirty {
			return
		}
		if err := c.persistLocked(current); err != nil {
			current.blocked = true
			current.journal.Reconciliation = blockedReconciliation(c.daemonID, "journal-checkpoint-failed", c.nowUTC())
			c.emitLocked(Event{
				EnvironmentID: environmentID,
				Generation:    current.journal.StartGeneration,
				Kind:          "reconciliation-blocked",
				ReasonCode:    "journal-checkpoint-failed",
				At:            c.nowUTC(),
			})
			return
		}
		current.dirty = false
	})
}

func (c *Coordinator) emitLocked(event Event) {
	if event.Status.Schema == "" {
		if state, ok := c.environments[event.EnvironmentID]; ok {
			event.Status = c.statusLocked(event.EnvironmentID, state)
		}
	}
	if c.publish != nil {
		c.publish(event)
	}
}

// Snapshot returns redacted derived status and never exposes journal internals.
func (c *Coordinator) Snapshot() []Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]Status, 0, len(c.environments))
	for environmentID, state := range c.environments {
		result = append(result, c.statusLocked(environmentID, state))
	}
	sortStatuses(result)
	return result
}

// ForgetEnvironment removes lifecycle metadata only after an explicit
// environment-clean authority has already removed the environment. It never
// invokes backend cleanup itself and refuses to hide a live handle or stop
// transaction.
func (c *Coordinator) ForgetEnvironment(environmentID string) error {
	if !idPattern.MatchString(environmentID) {
		return errors.New("lifecycle forget identity is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok {
		var err error
		state, err = c.loadEnvironmentLocked(environmentID)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if state.reconciling {
		return ErrReconciliationInFlight
	}
	if len(state.handles) != 0 || state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		return errors.New("lifecycle metadata cannot be forgotten while environment activity is in flight")
	}
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	if err := c.store.Remove(environmentID); err != nil {
		return err
	}
	delete(c.environments, environmentID)
	return nil
}

// RunDestructiveMutation serializes an explicit Manager-owned destructive
// environment operation with attach, idle stop, and explicit stop. Lifecycle
// never performs the mutation itself. It records blocked discovery truth before
// invoking the owner so a daemon crash cannot turn an interrupted cleanup into
// an apparently idle environment. Success removes the obsolete lifecycle
// journal; failure keeps it blocked for explicit recovery.
func (c *Coordinator) RunDestructiveMutation(ctx context.Context, environmentID string, mutate func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !idPattern.MatchString(environmentID) || mutate == nil {
		return errors.New("lifecycle destructive mutation is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.closing || c.closed {
		c.mu.Unlock()
		return ErrCoordinatorClosed
	}
	state, err := c.loadEnvironmentLocked(environmentID)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if state.reconciling {
		c.mu.Unlock()
		return ErrReconciliationInFlight
	}
	if len(state.handles) != 0 || state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		c.mu.Unlock()
		return errors.New("lifecycle destructive mutation is blocked by environment activity")
	}
	if err := c.cancelDeadlineLocked(state); err != nil {
		c.mu.Unlock()
		return err
	}
	state.mutation = true
	state.blocked = true
	state.journal.Reconciliation = blockedReconciliation(c.daemonID, "destructive-mutation-in-progress", c.nowUTC())
	if err := c.persistLocked(state); err != nil {
		state.mutation = false
		c.mu.Unlock()
		return err
	}
	c.emitLocked(Event{
		EnvironmentID: environmentID,
		Generation:    state.journal.StartGeneration,
		Kind:          "destructive-mutation-started",
		ReasonCode:    "operator-requested",
		At:            c.nowUTC(),
	})
	c.mu.Unlock()

	mutationErr := mutate(ctx)

	c.mu.Lock()
	if c.closing || c.closed {
		c.mu.Unlock()
		return errors.Join(mutationErr, ErrCoordinatorClosed)
	}
	state, ok := c.environments[environmentID]
	if !ok {
		c.mu.Unlock()
		return errors.Join(mutationErr, errors.New("lifecycle destructive mutation lost coordinator state"))
	}
	state.mutation = false
	if mutationErr != nil {
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "cleanup-unproved", c.nowUTC())
		persistErr := c.persistLocked(state)
		c.emitLocked(Event{
			EnvironmentID: environmentID,
			Generation:    state.journal.StartGeneration,
			Kind:          "destructive-mutation-failed",
			ReasonCode:    "cleanup-unproved",
			At:            c.nowUTC(),
		})
		c.mu.Unlock()
		return errors.Join(mutationErr, persistErr)
	}
	if err := c.store.Remove(environmentID); err != nil {
		persistErr := c.persistLocked(state)
		c.mu.Unlock()
		return errors.Join(err, persistErr)
	}
	delete(c.environments, environmentID)
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) statusLocked(environmentID string, state *registryEnvironment) Status {
	observation := state.observation
	if observation.ObservedAt.IsZero() || observation.Validate() != nil {
		observation = backend.LifecycleObservation{State: backend.LifecycleUnknown, InstanceName: environmentID, ObservedAt: c.nowUTC(), ReasonCode: "not-observed"}
	}
	var deadline *time.Time
	if state.journal.IdleDeadline != nil {
		value := state.journal.IdleDeadline.Deadline
		deadline = &value
	}
	stopState := ""
	if state.journal.StopAttempt != nil {
		stopState = state.journal.StopAttempt.State
	}
	return BuildStatus(environmentID, state.journal.StartGeneration, observation, state.journal.Resources, state.journal.Facts, deadline, state.journal.Reconciliation, stopState)
}

func currentIncarnationObservation(state *registryEnvironment) (backend.LifecycleObservation, error) {
	if state == nil || state.journal.Incarnation == nil {
		return backend.LifecycleObservation{}, errors.New("lifecycle incarnation is unavailable")
	}
	observation := state.observation
	if err := observation.Validate(); err != nil {
		return backend.LifecycleObservation{}, err
	}
	incarnation := state.journal.Incarnation
	if observation.State != backend.LifecycleRunning || observation.InstanceName != incarnation.InstanceName || observation.BootID != incarnation.BootID {
		return backend.LifecycleObservation{}, errors.New("lifecycle observation does not prove the current incarnation running")
	}
	return observation, nil
}

// Close cancels local deadlines. It never stops a backend during daemon
// shutdown because the complete lifecycle proof may no longer fit the bound.
func (c *Coordinator) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	if c.closing {
		c.mu.Unlock()
		return nil
	}
	c.closing = true
	var result error
	var cancels []context.CancelFunc
	for _, state := range c.environments {
		environmentID := state.journal.EnvironmentID
		generation := state.journal.StartGeneration
		deferred := state.journal.Incarnation != nil && state.journal.Incarnation.BootID != ""
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		if state.stopCancel != nil {
			cancels = append(cancels, state.stopCancel)
			state.stopCancel = nil
		}
		state.journal.IdleDeadline = nil
		if state.reconciling {
			state.blocked = true
			state.journal.Reconciliation = blockedReconciliation(c.daemonID, "daemon-shutdown", c.nowUTC())
			c.finishReconciliationLocked(state)
		}
		result = errors.Join(result, c.persistNowLocked(state))
		if deferred {
			c.emitLocked(Event{
				EnvironmentID: environmentID, Generation: generation,
				Kind: "stop-deferred", ReasonCode: "daemon-shutdown", At: c.nowUTC(),
			})
		}
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		c.stopWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(coordinatorCloseWait):
		result = errors.Join(result, errors.New("lifecycle stop transaction did not terminate during bounded shutdown"))
	}
	c.mu.Lock()
	c.closed = true
	c.closing = false
	c.mu.Unlock()
	return result
}
