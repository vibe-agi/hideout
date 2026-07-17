package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

type ReconcileInput struct {
	EnvironmentID      string
	InstanceName       string
	Observation        backend.LifecycleObservation
	OwnerSessionIDs    []string
	AdditionalUnproved bool
}

// Reconcile invalidates prior-daemon scheduling ownership and classifies only
// from current backend and provider facts. It never re-adopts old authority.
func (c *Coordinator) Reconcile(ctx context.Context, input ReconcileInput) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !idPattern.MatchString(input.EnvironmentID) || !idPattern.MatchString(input.InstanceName) {
		return errors.New("lifecycle reconciliation identity is invalid")
	}
	if err := input.Observation.Validate(); err != nil {
		return err
	}
	if input.Observation.InstanceName != input.InstanceName {
		return errors.New("lifecycle reconciliation observation belongs to another instance")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, err := c.loadEnvironmentLocked(input.EnvironmentID)
	if err != nil {
		return err
	}
	if !state.reconciling {
		if len(state.handles) != 0 || state.mutation ||
			attemptBlocksReconciliation(state.journal.StopAttempt, c.daemonID) || state.stopCancel != nil {
			return errors.New("lifecycle reconciliation is blocked by environment activity")
		}
		state.reconciling = true
		state.reconcileDone = make(chan struct{})
	}
	defer c.finishReconciliationLocked(state)
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	state.journal.IdleDeadline = nil
	state.journal.StopAttempt = nil
	state.observation = input.Observation
	state.handles = map[string]bool{}
	state.committed = map[string]bool{}
	state.closing = map[string]bool{}
	state.resourceUsers = map[string]map[string]bool{}
	state.resourceOrder = map[string][]ResourceRef{}
	state.terminal = map[string]ResourceState{}
	now := c.nowUTC()
	// Even an unknown first observation needs a schema-valid, durable blocked
	// journal. Generation 1 is discovery identity only; it grants no incarnation
	// or attach authority without a proved backend observation.
	if state.journal.StartGeneration == 0 {
		state.journal.StartGeneration = 1
	}

	switch input.Observation.State {
	case backend.LifecycleStopped, backend.LifecycleAbsent:
		state.journal.Incarnation = nil
		state.journal.Resources = nil
		state.blocked = false
		state.journal.Reconciliation = Reconciliation{DaemonInstanceID: c.daemonID, State: "complete", ObservedAt: now}
		if err := c.persistLocked(state); err != nil {
			return err
		}
		c.emitLocked(Event{EnvironmentID: input.EnvironmentID, Generation: state.journal.StartGeneration, Kind: "reconciliation-completed", At: now})
		return nil
	case backend.LifecycleUnknown:
		state.blocked = true
		markDependentJournalResourcesOrphaned(state, now)
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, input.Observation.ReasonCode, now)
		if err := c.persistLocked(state); err != nil {
			return err
		}
		c.emitLocked(Event{EnvironmentID: input.EnvironmentID, Generation: state.journal.StartGeneration, Kind: "reconciliation-blocked", ReasonCode: input.Observation.ReasonCode, At: now})
		return nil
	case backend.LifecycleRunning:
	}

	exact := state.journal.Incarnation != nil &&
		state.journal.Incarnation.InstanceName == input.InstanceName &&
		state.journal.Incarnation.BootID == input.Observation.BootID
	if !exact {
		state.journal.StartGeneration++
		if state.journal.StartGeneration == 0 {
			state.journal.StartGeneration = 1
		}
		incarnation := EnvironmentRef{
			EnvironmentID: input.EnvironmentID, StartGeneration: state.journal.StartGeneration,
			InstanceName: input.InstanceName, BootID: input.Observation.BootID,
		}
		state.journal.Incarnation = &incarnation
		state.journal.Resources = []Resource{newRootResource(incarnation, c.daemonID, now)}
		appendOwnerOrphans(state, input.OwnerSessionIDs, incarnation, c.daemonID, now)
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "backend-incarnation-changed", now)
		if err := c.persistLocked(state); err != nil {
			return err
		}
		c.emitLocked(Event{EnvironmentID: input.EnvironmentID, Generation: state.journal.StartGeneration, Kind: "backend-incarnation-superseded", ReasonCode: "backend-incarnation-changed", At: now})
		return nil
	}

	incarnation := *state.journal.Incarnation
	if !input.AdditionalUnproved && len(input.OwnerSessionIDs) == 0 {
		// Every provider fact supplied by the restart reconciler is absent. Old
		// daemon-owned live-graph rows are discovery metadata, not authority, and
		// must not become permanent orphans after their real providers are proved
		// gone.
		state.journal.Resources = []Resource{newRootResource(incarnation, c.daemonID, now)}
		state.blocked = false
		state.journal.Reconciliation = Reconciliation{DaemonInstanceID: c.daemonID, State: "complete", ObservedAt: now}
		c.emitLocked(Event{EnvironmentID: input.EnvironmentID, Generation: state.journal.StartGeneration, Kind: "reconciliation-completed", At: now})
		if err := c.scheduleIfIdleLocked(input.EnvironmentID, state); err != nil {
			return err
		}
		return c.persistNowLocked(state)
	}
	rootFound := false
	possibleOldDependency := input.AdditionalUnproved || len(input.OwnerSessionIDs) != 0
	for index, resource := range state.journal.Resources {
		if resource.Ref.Kind == KindBackendIncarnation {
			state.journal.Resources[index] = newRootResource(incarnation, c.daemonID, now)
			rootFound = true
			continue
		}
		possibleOldDependency = true
		if resource.State != StateOrphaned {
			resource.State = StateOrphaned
			resource.PossibleVMDependency = true
			resource.UpdatedAt = now
			state.journal.Resources[index] = resource
		}
	}
	if !rootFound {
		state.journal.Resources = append(state.journal.Resources, newRootResource(incarnation, c.daemonID, now))
	}
	appendOwnerOrphans(state, input.OwnerSessionIDs, incarnation, c.daemonID, now)
	state.blocked = possibleOldDependency
	reconciliationState := "complete"
	if state.blocked {
		reconciliationState = "blocked"
	}
	state.journal.Reconciliation = Reconciliation{DaemonInstanceID: c.daemonID, State: reconciliationState, ObservedAt: now}
	if state.blocked {
		reasonCode := "provider-state-unproved"
		if !input.AdditionalUnproved && len(input.OwnerSessionIDs) != 0 {
			reasonCode = "owner-requires-explicit-recovery"
		}
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, reasonCode, now)
	}
	if err := c.persistLocked(state); err != nil {
		return err
	}
	if !state.blocked {
		c.emitLocked(Event{EnvironmentID: input.EnvironmentID, Generation: state.journal.StartGeneration, Kind: "reconciliation-completed", At: now})
		if err := c.scheduleIfIdleLocked(input.EnvironmentID, state); err != nil {
			return err
		}
		return c.persistNowLocked(state)
	}
	c.emitLocked(Event{EnvironmentID: input.EnvironmentID, Generation: state.journal.StartGeneration, Kind: "reconciliation-blocked", ReasonCode: state.journal.Reconciliation.ReasonCode, At: now})
	return nil
}

func appendOwnerOrphans(state *registryEnvironment, ownerSessionIDs []string, incarnation EnvironmentRef, daemonID string, now time.Time) {
	for _, sessionID := range ownerSessionIDs {
		if !idPattern.MatchString(sessionID) || hasSessionResource(state.journal.Resources, sessionID) {
			continue
		}
		root := ResourceRef{Kind: KindBackendIncarnation, ID: incarnation.EnvironmentID, Generation: incarnation.StartGeneration}
		state.journal.Resources = append(state.journal.Resources, Resource{
			Ref:   ResourceRef{Kind: KindRunSession, ID: sessionID, Generation: incarnation.StartGeneration},
			Owner: OwnerRef{Kind: "daemon", ID: daemonID, Generation: incarnation.StartGeneration},
			State: StateOrphaned, Dependencies: []DependencySpec{{Ref: root, StopMode: StopModePin}},
			Persistence: PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot,
			PossibleVMDependency: true, UpdatedAt: now,
		})
	}
}

func newRootResource(incarnation EnvironmentRef, daemonID string, now time.Time) Resource {
	return Resource{
		Ref:   ResourceRef{Kind: KindBackendIncarnation, ID: incarnation.EnvironmentID, Generation: incarnation.StartGeneration},
		Owner: OwnerRef{Kind: "daemon", ID: daemonID, Generation: incarnation.StartGeneration},
		State: StateActive, Persistence: PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot,
		Incarnation: &incarnation, UpdatedAt: now,
	}
}

func markDependentJournalResourcesOrphaned(state *registryEnvironment, now time.Time) {
	for index, resource := range state.journal.Resources {
		if resource.Ref.Kind == KindBackendIncarnation {
			continue
		}
		resource.State = StateOrphaned
		resource.PossibleVMDependency = true
		resource.UpdatedAt = now
		state.journal.Resources[index] = resource
	}
}

func hasSessionResource(resources []Resource, sessionID string) bool {
	for _, resource := range resources {
		if resource.Ref.Kind == KindRunSession && resource.Ref.ID == sessionID {
			return true
		}
	}
	return false
}
