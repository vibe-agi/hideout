package lifecycle

import (
	"errors"
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/backend"
)

func (c *Coordinator) selectIncarnationLocked(state *registryEnvironment, request AttachRequest) (EnvironmentRef, ResourceRef, error) {
	journal := &state.journal
	observation := request.Observation
	var incarnation EnvironmentRef
	reuse := false
	if journal.Incarnation != nil && journal.Incarnation.InstanceName == request.InstanceName {
		switch observation.State {
		case backend.LifecycleRunning:
			reuse = journal.Incarnation.BootID == observation.BootID
		case backend.LifecycleStopped, backend.LifecycleAbsent:
			reuse = journal.Incarnation.BootID == ""
		}
	}
	if reuse {
		incarnation = *journal.Incarnation
	} else {
		journal.StartGeneration++
		if journal.StartGeneration == 0 {
			journal.StartGeneration = 1
		}
		incarnation = EnvironmentRef{
			EnvironmentID:   request.EnvironmentID,
			StartGeneration: journal.StartGeneration,
			InstanceName:    request.InstanceName,
		}
		if observation.State == backend.LifecycleRunning {
			incarnation.BootID = observation.BootID
		}
		journal.Incarnation = &incarnation
		// Provider stores and append-only audit own retained state. A new backend
		// incarnation never carries old live-resource discovery metadata forward.
		journal.Resources = nil
		journal.StopAttempt = nil
		state.resourceUsers = map[string]map[string]bool{}
		state.terminal = map[string]ResourceState{}
	}
	if journal.StartGeneration == 0 {
		journal.StartGeneration = incarnation.StartGeneration
	}
	rootRef := ResourceRef{Kind: KindBackendIncarnation, ID: request.EnvironmentID, Generation: incarnation.StartGeneration}
	rootState := StatePlanned
	if incarnation.BootID != "" {
		rootState = StateActive
	}
	root := Resource{
		Ref:         rootRef,
		Owner:       OwnerRef{Kind: "daemon", ID: c.daemonID, Generation: incarnation.StartGeneration},
		State:       rootState,
		Persistence: PersistenceEphemeral,
		ClosePolicy: CloseCoTerminateWithRoot,
		Incarnation: &incarnation,
		UpdatedAt:   c.nowUTC(),
	}
	found := false
	for index, resource := range journal.Resources {
		if resource.Ref.Key() == rootRef.Key() {
			journal.Resources[index] = root
			found = true
			break
		}
	}
	if !found {
		journal.Resources = append(journal.Resources, root)
	}
	return incarnation, rootRef, nil
}

func (c *Coordinator) bindBoot(environmentID, handleID, bootID string) (EnvironmentRef, error) {
	if !bootIDPattern.MatchString(bootID) {
		return EnvironmentRef{}, errors.New("lifecycle boot identity is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return EnvironmentRef{}, ErrCoordinatorClosed
	}
	state, ok := c.environments[environmentID]
	if !ok || !state.handles[handleID] || state.journal.Incarnation == nil {
		return EnvironmentRef{}, errors.New("lifecycle attach is not active")
	}
	incarnation := *state.journal.Incarnation
	if incarnation.BootID != "" && incarnation.BootID != bootID {
		return EnvironmentRef{}, fmt.Errorf("backend boot identity changed during attach: %w", ErrAttachBlocked)
	}
	newBootBinding := incarnation.BootID == ""
	incarnation.BootID = bootID
	state.journal.Incarnation = &incarnation
	now := c.nowUTC()
	// BindBoot receives the boot identity observed by Core after the backend is
	// usable. Keep that observation in memory; the durable journal remains a
	// restart discovery index and must never become current liveness proof.
	state.observation = backend.LifecycleObservation{
		State:        backend.LifecycleRunning,
		InstanceName: incarnation.InstanceName,
		BootID:       bootID,
		ObservedAt:   now,
	}
	for index, resource := range state.journal.Resources {
		switch resource.Ref.Kind {
		case KindBackendIncarnation:
			resource.Incarnation = &incarnation
			resource.State = StateActive
			resource.UpdatedAt = now
			state.journal.Resources[index] = resource
		case KindRunSession:
			if resource.Ref.ID == handleID && resource.State == StatePlanned {
				resource.State = StateStarting
				resource.UpdatedAt = now
				state.journal.Resources[index] = resource
			}
		}
	}
	if newBootBinding {
		if err := c.persistNowLocked(state); err != nil {
			return EnvironmentRef{}, err
		}
	} else {
		c.checkpointLaterLocked(environmentID, state)
	}
	return incarnation, nil
}

func (c *Coordinator) loadEnvironmentLocked(environmentID string) (*registryEnvironment, error) {
	if state, ok := c.environments[environmentID]; ok {
		return state, nil
	}
	now := c.nowUTC()
	journal, err := c.store.Load(environmentID)
	if errors.Is(err, os.ErrNotExist) {
		journal = newJournal(environmentID, c.daemonID, 1, now)
		journal.StartGeneration = 0
	} else if err != nil {
		return nil, journalError("load", err)
	}
	state := &registryEnvironment{
		journal: journal, handles: map[string]bool{}, committed: map[string]bool{}, closing: map[string]bool{},
		resourceUsers: map[string]map[string]bool{}, resourceOrder: map[string][]ResourceRef{},
		terminal: map[string]ResourceState{}, loaded: true,
	}
	if journal.Reconciliation.DaemonInstanceID != c.daemonID && len(journal.Resources) != 0 {
		state.blocked = true
		state.journal.Reconciliation = blockedReconciliation(c.daemonID, "prior-daemon-state-unproved", now)
	}
	c.environments[environmentID] = state
	return state, nil
}
