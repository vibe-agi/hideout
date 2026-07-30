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
const stopTransactionTimeout = 35 * time.Second
const automaticStopAttachWaitLimit = stopTransactionTimeout + time.Second

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
	mu               sync.Mutex
	store            JournalStore
	daemonID         string
	idleGrace        time.Duration
	now              func() time.Time
	after            afterFunc
	stop             StopFunc
	publish          func(Event)
	enabled          bool
	environments     map[string]*registryEnvironment
	mutationClaims   map[string]map[uint64]MutationOwner
	mutationSequence uint64
	stopWG           sync.WaitGroup
	closing          bool
	closed           bool
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
		mutationClaims: map[string]map[uint64]MutationOwner{},
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
	if len(state.establishing) != 0 {
		return c.persistLocked(state)
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
	if !ok || c.closing || c.closed || state.mutation || len(state.establishing) != 0 || state.journal.IdleDeadline == nil || state.journal.IdleDeadline.Generation != sequence ||
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
	attemptID := fmt.Sprintf("stop-%d-%d", incarnation.StartGeneration, sequence)
	mutationRequest, requestErr := c.normalizeMutationRequest(MutationRequest{
		Keys: []string{EnvironmentMutationKey(environmentID)},
		Owner: MutationOwner{
			Kind: MutationOwnerStop, ID: attemptID,
			Phase:     MutationPhaseStopping,
			Recovery:  "wait for stable stop evidence, inspect lifecycle status, then retry",
			StartedAt: c.nowUTC(),
		},
	})
	if requestErr != nil {
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(
			c.daemonID,
			"lifecycle-mutation-key-invalid",
			c.nowUTC(),
		)
		_ = c.persistLocked(state)
		c.mu.Unlock()
		return
	}
	lease, claimErr := c.claimMutationKeysLocked(mutationRequest)
	if claimErr != nil {
		state.deadlineSeq++
		retrySequence := state.deadlineSeq
		now := c.nowUTC()
		state.journal.IdleDeadline = &IdleDeadline{
			Incarnation: incarnation, DaemonInstanceID: c.daemonID,
			ScheduledAt: now, Deadline: now.Add(c.idleGrace),
			Generation: retrySequence,
		}
		state.timer = c.after(c.idleGrace, func() {
			c.expire(environmentID, incarnation, retrySequence)
		})
		c.checkpointLaterLocked(environmentID, state)
		c.emitLocked(Event{
			EnvironmentID: environmentID,
			Generation:    incarnation.StartGeneration,
			Kind:          "stop-deferred",
			ReasonCode:    "mutation-in-progress",
			At:            now,
		})
		c.mu.Unlock()
		return
	}
	state.journal.IdleDeadline = nil
	state.timer = nil
	state.journal.StopAttempt = &StopAttempt{
		ID: attemptID, Incarnation: incarnation, DaemonInstanceID: c.daemonID,
		Mode: "automatic", State: "planned", StartedAt: c.nowUTC(),
	}
	if err := c.persistLocked(state); err != nil {
		state.blocked = true
		lease.releaseLocked()
		c.mu.Unlock()
		return
	}
	c.emitLocked(Event{EnvironmentID: environmentID, Generation: incarnation.StartGeneration, Kind: "idle-grace-expired", At: c.nowUTC()})
	if !c.enabled || c.stop == nil {
		state.journal.StopAttempt = nil
		_ = c.persistLocked(state)
		lease.releaseLocked()
		c.mu.Unlock()
		return
	}
	state.journal.StopAttempt.State = "invoked"
	_ = c.persistLocked(state)
	ctx, cancel := context.WithTimeout(context.Background(), stopTransactionTimeout)
	state.stopCancel = cancel
	state.stopDone = make(chan struct{})
	c.stopWG.Add(1)
	c.mu.Unlock()

	result, stopErr := c.stop(ctx, StopRequest{AttemptID: attemptID, Mode: "automatic", Incarnation: incarnation})
	cancel()
	c.commitStopResult(environmentID, attemptID, incarnation, result, stopErr)
	lease.Release()
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
	c.finishStopLocked(state)
}

func (c *Coordinator) finishStopLocked(state *registryEnvironment) {
	if state.stopDone == nil {
		return
	}
	close(state.stopDone)
	state.stopDone = nil
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
	startedAt := c.nowUTC()
	attemptID := fmt.Sprintf(
		"explicit-%d-%d",
		state.journal.StartGeneration,
		startedAt.UnixNano(),
	)
	mutationRequest, err := c.normalizeMutationRequest(MutationRequest{
		Keys: []string{EnvironmentMutationKey(environmentID)},
		Owner: MutationOwner{
			Kind: MutationOwnerStop, ID: attemptID,
			Phase:     MutationPhaseStopping,
			Recovery:  "wait for stable stop evidence, inspect lifecycle status, then retry",
			StartedAt: startedAt,
		},
	})
	if err != nil {
		c.mu.Unlock()
		return Status{}, err
	}
	lease, err := c.claimMutationKeysLocked(mutationRequest)
	if err != nil {
		status := c.statusLocked(environmentID, state)
		c.mu.Unlock()
		return status, err
	}
	defer lease.Release()
	if state.reconciling {
		status := c.statusLocked(environmentID, state)
		lease.releaseLocked()
		conflictErr := c.environmentConflictLocked(
			environmentID,
			state,
			mutationRequest.Owner,
			ErrReconciliationInFlight,
		)
		c.mu.Unlock()
		return status, conflictErr
	}
	if len(state.handles) != 0 || len(state.establishing) != 0 {
		status := c.statusLocked(environmentID, state)
		lease.releaseLocked()
		conflictErr := c.environmentConflictLocked(
			environmentID,
			state,
			mutationRequest.Owner,
			ErrMutationBlockedByActivity,
		)
		c.mu.Unlock()
		return status, conflictErr
	}
	if state.mutation {
		status := c.statusLocked(environmentID, state)
		lease.releaseLocked()
		conflictErr := c.environmentConflictLocked(
			environmentID,
			state,
			mutationRequest.Owner,
			ErrStopInFlight,
		)
		c.mu.Unlock()
		return status, conflictErr
	}
	if attemptBlocksAttach(state.journal.StopAttempt) {
		status := c.statusLocked(environmentID, state)
		lease.releaseLocked()
		conflictErr := c.environmentConflictLocked(
			environmentID,
			state,
			mutationRequest.Owner,
			ErrStopInFlight,
		)
		c.mu.Unlock()
		return status, conflictErr
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
		return status, errors.New("lifecycle explicit stop is blocked by unclassified provider state; restart the local control plane (hideout daemon stop, then retry) so it re-reconciles the environment")
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
	state.journal.StopAttempt = &StopAttempt{
		ID: attemptID, Incarnation: incarnation, DaemonInstanceID: c.daemonID,
		Mode: "explicit-recovery", State: "invoked", StartedAt: startedAt,
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
	stopCtx, cancel := context.WithTimeout(ctx, stopTransactionTimeout)
	state.stopCancel = cancel
	state.stopDone = make(chan struct{})
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
	request, err := c.normalizeMutationRequest(MutationRequest{
		Keys: []string{EnvironmentMutationKey(environmentID)},
		Owner: MutationOwner{
			Kind: MutationOwnerCleanup, ID: c.daemonID,
			Phase:     MutationPhaseApplying,
			Recovery:  "wait for lifecycle activity to finish, then retry metadata cleanup",
			StartedAt: c.nowUTC(),
		},
	})
	if err != nil {
		return err
	}
	lease, err := c.claimMutationKeysLocked(request)
	if err != nil {
		return err
	}
	defer lease.releaseLocked()
	if state.reconciling {
		lease.releaseLocked()
		return c.environmentConflictLocked(
			environmentID,
			state,
			request.Owner,
			ErrReconciliationInFlight,
		)
	}
	if len(state.handles) != 0 || len(state.establishing) != 0 || state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		lease.releaseLocked()
		return c.environmentConflictLocked(
			environmentID,
			state,
			request.Owner,
			ErrMutationBlockedByActivity,
		)
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

// BeginDisposal serializes one exact removal identity with attach and stop,
// then persists authorization before Manager may perform backend cleanup.
// Repeating it after a daemon restart resumes the last durable forward phase.
func (c *Coordinator) BeginDisposal(ctx context.Context, request DisposalRequest) (DisposalIntent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.validate(); err != nil {
		return DisposalIntent{}, err
	}
	if err := ctx.Err(); err != nil {
		return DisposalIntent{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return DisposalIntent{}, ErrCoordinatorClosed
	}
	state, err := c.loadEnvironmentLocked(request.EnvironmentID)
	if err != nil {
		return DisposalIntent{}, err
	}
	claimRequest, err := c.normalizeMutationRequest(MutationRequest{
		Keys: []string{EnvironmentMutationKey(request.EnvironmentID)},
		Owner: MutationOwner{
			Kind: MutationOwnerDisposal, ID: request.authority(),
			Phase:     MutationPhaseApplying,
			Recovery:  "inspect the owning disposal operation and retry after it reaches a terminal or blocked phase",
			StartedAt: c.nowUTC(),
		},
	})
	if err != nil {
		return DisposalIntent{}, err
	}
	lease, err := c.claimMutationKeysLocked(claimRequest)
	if err != nil {
		return DisposalIntent{}, err
	}
	retainLease := false
	defer func() {
		if !retainLease {
			lease.releaseLocked()
		}
	}()
	if state.reconciling {
		lease.releaseLocked()
		return DisposalIntent{}, c.environmentConflictLocked(
			request.EnvironmentID,
			state,
			claimRequest.Owner,
			ErrReconciliationInFlight,
		)
	}
	if len(state.handles) != 0 || len(state.establishing) != 0 || state.mutation ||
		attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		lease.releaseLocked()
		return DisposalIntent{}, c.environmentConflictLocked(
			request.EnvironmentID,
			state,
			claimRequest.Owner,
			ErrMutationBlockedByActivity,
		)
	}
	if state.journal.StartGeneration == 0 {
		generation := request.Generation
		if generation == 0 {
			generation = 1
		}
		state.journal = newJournal(request.EnvironmentID, c.daemonID, generation, c.nowUTC())
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "disposal-in-progress", c.nowUTC())
	} else if request.Generation != 0 && request.Generation != state.journal.StartGeneration {
		return DisposalIntent{}, errors.New("lifecycle disposal request generation mismatch")
	}

	now := c.nowUTC()
	authority := request.authority()
	if state.journal.Disposal == nil {
		state.journal.Disposal = &DisposalIntent{
			Schema: DisposalIntentSchema, Authority: authority,
			Backend: request.Backend, InstanceName: request.InstanceName,
			RecordDigest: request.RecordDigest, ActivitySessionID: request.ActivitySessionID,
			Generation: state.journal.StartGeneration,
			State:      DisposalStatePlanned, RequestedAt: now, UpdatedAt: now,
		}
	} else {
		intent := state.journal.Disposal
		if intent.Authority != authority || intent.Backend != request.Backend ||
			intent.InstanceName != request.InstanceName ||
			intent.RecordDigest != request.RecordDigest ||
			intent.ActivitySessionID != request.ActivitySessionID ||
			intent.Generation != state.journal.StartGeneration {
			return DisposalIntent{}, errors.New("lifecycle disposal intent identity mismatch")
		}
		if intent.State == DisposalStateBlocked {
			if err := ValidateDisposalTransition(intent.State, DisposalStatePlanned); err != nil {
				return DisposalIntent{}, err
			}
			intent.State = DisposalStatePlanned
			intent.ReasonCode = ""
			intent.UpdatedAt = now
		}
	}
	if err := c.cancelDeadlineLocked(state); err != nil {
		lease.releaseLocked()
		return DisposalIntent{}, err
	}
	state.mutation = true
	ownerCopy := claimRequest.Owner
	state.mutationOwner = &ownerCopy
	state.mutationLease = lease
	state.blocked = true
	state.journal.Reconciliation = blockedReconciliation(c.daemonID, "disposal-in-progress", now)
	if err := c.persistLocked(state); err != nil {
		state.mutation = false
		state.mutationOwner = nil
		state.mutationLease = nil
		lease.releaseLocked()
		return DisposalIntent{}, err
	}
	c.emitLocked(Event{
		EnvironmentID: request.EnvironmentID, Generation: state.journal.StartGeneration,
		Kind: "disposal-started", At: now,
	})
	retainLease = true
	return *state.journal.Disposal, nil
}

// AdvanceDisposal persists one forward proof boundary. It never invokes or
// assumes a backend operation.
func (c *Coordinator) AdvanceDisposal(ctx context.Context, environmentID, recordDigest, nextState string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, intent, err := c.activeDisposalLocked(environmentID, recordDigest)
	if err != nil {
		return err
	}
	if nextState == DisposalStateBlocked {
		return errors.New("blocked disposal requires a reason")
	}
	if err := ValidateDisposalTransition(intent.State, nextState); err != nil {
		return err
	}
	intent.State = nextState
	intent.ReasonCode = ""
	intent.UpdatedAt = c.nowUTC()
	state.journal.Reconciliation = blockedReconciliation(c.daemonID, "disposal-in-progress", c.nowUTC())
	if err := c.persistLocked(state); err != nil {
		return err
	}
	c.emitLocked(Event{
		EnvironmentID: environmentID, Generation: state.journal.StartGeneration,
		Kind: "disposal-progressed", ReasonCode: nextState, At: c.nowUTC(),
	})
	return nil
}

// BlockDisposal records a bounded fail-closed outcome and releases the
// daemon-local mutation slot so a later revalidated retry can resume.
func (c *Coordinator) BlockDisposal(ctx context.Context, environmentID, recordDigest, reasonCode string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validDisposalReasonCode(reasonCode) {
		return errors.New("lifecycle disposal block reason is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, intent, err := c.disposalLocked(environmentID, recordDigest)
	if err != nil {
		return err
	}
	if err := ValidateDisposalTransition(intent.State, DisposalStateBlocked); err != nil {
		return err
	}
	intent.State = DisposalStateBlocked
	intent.ReasonCode = reasonCode
	intent.UpdatedAt = c.nowUTC()
	state.mutation = false
	state.mutationOwner = nil
	if state.mutationLease != nil {
		state.mutationLease.releaseLocked()
		state.mutationLease = nil
	}
	state.blocked = true
	state.journal.Reconciliation = blockedReconciliation(c.daemonID, reasonCode, c.nowUTC())
	persistErr := c.persistLocked(state)
	c.emitLocked(Event{
		EnvironmentID: environmentID, Generation: state.journal.StartGeneration,
		Kind: "disposal-blocked", ReasonCode: reasonCode, At: c.nowUTC(),
	})
	return persistErr
}

// CompleteDisposalMetadata removes only the journal/coordinator identity after
// Manager has persisted the metadata-cleaning boundary. Manager still owns the
// record-last removal that follows.
func (c *Coordinator) CompleteDisposalMetadata(ctx context.Context, environmentID, recordDigest string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, intent, err := c.activeDisposalLocked(environmentID, recordDigest)
	if err != nil {
		return err
	}
	if intent.State != DisposalStateMetadataCleaning {
		return errors.New("lifecycle disposal metadata is not ready for completion")
	}
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	if err := c.store.Remove(environmentID); err != nil {
		state.mutation = false
		state.mutationOwner = nil
		if state.mutationLease != nil {
			state.mutationLease.releaseLocked()
			state.mutationLease = nil
		}
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "journal-removal-failed", c.nowUTC())
		return errors.Join(err, c.persistLocked(state))
	}
	if state.mutationLease != nil {
		state.mutationLease.releaseLocked()
		state.mutationLease = nil
	}
	state.mutationOwner = nil
	delete(c.environments, environmentID)
	c.emitLocked(Event{
		EnvironmentID: environmentID, Generation: intent.Generation,
		Kind: "disposal-metadata-removed", At: c.nowUTC(),
	})
	return nil
}

func (c *Coordinator) activeDisposalLocked(environmentID, recordDigest string) (*registryEnvironment, *DisposalIntent, error) {
	state, intent, err := c.disposalLocked(environmentID, recordDigest)
	if err != nil {
		return nil, nil, err
	}
	if !state.mutation {
		return nil, nil, errors.New("lifecycle disposal mutation is not active")
	}
	return state, intent, nil
}

func (c *Coordinator) disposalLocked(environmentID, recordDigest string) (*registryEnvironment, *DisposalIntent, error) {
	if c.closing || c.closed {
		return nil, nil, ErrCoordinatorClosed
	}
	if !idPattern.MatchString(environmentID) || !lowerHex(recordDigest, 64) {
		return nil, nil, errors.New("lifecycle disposal lease identity is invalid")
	}
	state, err := c.loadEnvironmentLocked(environmentID)
	if err != nil {
		return nil, nil, err
	}
	if state.journal.Disposal == nil || state.journal.Disposal.RecordDigest != recordDigest {
		return nil, nil, errors.New("lifecycle disposal lease identity mismatch")
	}
	return state, state.journal.Disposal, nil
}

// RunDestructiveMutation serializes an explicit Manager-owned destructive
// environment operation with attach, idle stop, and explicit stop. Lifecycle
// never performs the mutation itself. It records blocked discovery truth before
// invoking the owner so a daemon crash cannot turn an interrupted cleanup into
// an apparently idle environment. Success removes the obsolete lifecycle
// journal; failure keeps it blocked for explicit recovery.
func (c *Coordinator) RunDestructiveMutation(ctx context.Context, environmentID string, mutate func(context.Context) error) error {
	return c.RunDestructiveMutationWithOwner(
		ctx,
		environmentID,
		MutationOwner{
			Kind:     MutationOwnerCleanup,
			ID:       c.daemonID,
			Phase:    MutationPhaseApplying,
			Recovery: "inspect lifecycle status and retry the cleanup after the blocker finishes",
		},
		mutate,
	)
}

// RunDestructiveMutationWithOwner is the typed form used by configuration and
// lifecycle operation ledgers. The owner is visible to every rejected
// concurrent caller and does not broaden the callback's authority.
func (c *Coordinator) RunDestructiveMutationWithOwner(
	ctx context.Context,
	environmentID string,
	owner MutationOwner,
	mutate func(context.Context) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !idPattern.MatchString(environmentID) || mutate == nil {
		return errors.New("lifecycle destructive mutation is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := c.normalizeMutationRequest(MutationRequest{
		Keys:  []string{EnvironmentMutationKey(environmentID)},
		Owner: owner,
	})
	if err != nil {
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
	lease, err := c.claimMutationKeysLocked(request)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	defer lease.Release()
	if state.reconciling {
		lease.releaseLocked()
		conflictErr := c.environmentConflictLocked(
			environmentID,
			state,
			request.Owner,
			ErrReconciliationInFlight,
		)
		c.mu.Unlock()
		return conflictErr
	}
	if len(state.handles) != 0 || len(state.establishing) != 0 || state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		lease.releaseLocked()
		conflictErr := c.environmentConflictLocked(
			environmentID,
			state,
			request.Owner,
			ErrMutationBlockedByActivity,
		)
		c.mu.Unlock()
		return conflictErr
	}
	if err := c.cancelDeadlineLocked(state); err != nil {
		c.mu.Unlock()
		return err
	}
	state.mutation = true
	ownerCopy := request.Owner
	state.mutationOwner = &ownerCopy
	state.blocked = true
	// A never-attached environment (or one whose journal a prior mutation
	// removed) has no lifecycle journal: its zero start generation would fail
	// journal identity validation, and there is nothing durable to block. The
	// in-memory mutation flag alone guards re-entry.
	journalless := state.journal.StartGeneration == 0
	if !journalless {
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "destructive-mutation-in-progress", c.nowUTC())
		if err := c.persistLocked(state); err != nil {
			state.mutation = false
			state.mutationOwner = nil
			c.mu.Unlock()
			return err
		}
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
	state.mutationOwner = nil
	journalless = state.journal.StartGeneration == 0
	if mutationErr != nil {
		if journalless {
			// Nothing durable exists for a journal-less environment: the
			// mutation error itself is the truth and a retry starts clean.
			delete(c.environments, environmentID)
			c.emitLocked(Event{
				EnvironmentID: environmentID,
				Kind:          "destructive-mutation-failed",
				ReasonCode:    "cleanup-unproved",
				At:            c.nowUTC(),
			})
			c.mu.Unlock()
			return mutationErr
		}
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
	if journalless {
		delete(c.environments, environmentID)
		c.mu.Unlock()
		return nil
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
	status := BuildStatus(environmentID, state.journal.StartGeneration, observation, state.journal.Resources, state.journal.Facts, deadline, state.journal.Reconciliation, stopState)
	if state.journal.Disposal != nil {
		status.DisposalPhase = state.journal.Disposal.State
		status.DisposalReasonCode = state.journal.Disposal.ReasonCode
	}
	status.EstablishingSessions = len(state.establishing)
	if status.EstablishingSessions != 0 {
		status.Activity = ActivityEstablishing
		status.ReasonCode = "attach-establishment-in-progress"
	}
	return status
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
		state.establishing = map[string]*establishment{}
		deferred := state.journal.Incarnation != nil && state.journal.Incarnation.BootID != ""
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		if state.stopCancel != nil {
			cancels = append(cancels, state.stopCancel)
			state.stopCancel = nil
		}
		c.finishStopLocked(state)
		state.journal.IdleDeadline = nil
		if state.reconciling {
			state.blocked = true
			state.journal.Reconciliation = blockedReconciliation(c.daemonID, "daemon-shutdown", c.nowUTC())
			c.finishReconciliationLocked(state)
		}
		if state.journal.StartGeneration == 0 {
			delete(c.environments, environmentID)
			continue
		}
		result = errors.Join(result, c.persistNowLocked(state))
		if deferred {
			c.emitLocked(Event{
				EnvironmentID: environmentID, Generation: generation,
				Kind: "stop-deferred", ReasonCode: "daemon-shutdown", At: c.nowUTC(),
			})
		}
	}
	c.mutationClaims = map[string]map[uint64]MutationOwner{}
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
