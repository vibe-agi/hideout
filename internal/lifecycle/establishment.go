package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

type establishmentPhase uint8

const (
	establishmentReserved establishmentPhase = iota + 1
	establishmentPrepared
	establishmentPromoted
	establishmentAborted
)

type establishment struct {
	coordinator *Coordinator
	environment string
	session     string

	mu          sync.Mutex
	phase       establishmentPhase
	incarnation EnvironmentRef
	root        ResourceRef
}

func (c *Coordinator) ReserveAttach(ctx context.Context, request EstablishmentRequest) (EstablishmentReservation, error) {
	return c.reserveAttach(ctx, request, true)
}

func (c *Coordinator) reserveAttach(ctx context.Context, request EstablishmentRequest, shouldWaitForAutomaticStop bool) (EstablishmentReservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !idPattern.MatchString(request.EnvironmentID) || !idPattern.MatchString(request.SessionID) {
		return nil, errors.New("lifecycle establishment identity is invalid")
	}
	waitingReason := ""
	for {
		c.mu.Lock()
		if c.closing || c.closed {
			c.mu.Unlock()
			return nil, ErrCoordinatorClosed
		}
		state, err := c.loadEnvironmentLocked(request.EnvironmentID)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		if state.reconciling {
			if waitingReason != "reconciliation-pending" {
				c.emitLocked(Event{
					EnvironmentID: request.EnvironmentID,
					Generation:    state.journal.StartGeneration,
					Kind:          "attach-establishment-waiting",
					ReasonCode:    "reconciliation-pending",
					At:            c.nowUTC(),
				})
				waitingReason = "reconciliation-pending"
			}
			done := state.reconcileDone
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
			}
			continue
		}
		if state.blocked {
			reasonCode := state.journal.Reconciliation.ReasonCode
			c.mu.Unlock()
			return nil, &AttachBlockedError{ReasonCode: reasonCode}
		}
		if state.stopDone != nil && state.journal.StopAttempt != nil &&
			state.journal.StopAttempt.Mode == "automatic" {
			if !shouldWaitForAutomaticStop {
				c.mu.Unlock()
				return nil, ErrStopInFlight
			}
			if waitingReason != "automatic-stop-pending" {
				c.emitLocked(Event{
					EnvironmentID: request.EnvironmentID,
					Generation:    state.journal.StartGeneration,
					Kind:          "attach-establishment-waiting",
					ReasonCode:    "automatic-stop-pending",
					At:            c.nowUTC(),
				})
				waitingReason = "automatic-stop-pending"
			}
			done := state.stopDone
			c.mu.Unlock()
			if err := waitForAutomaticStop(ctx, done); err != nil {
				return nil, err
			}
			continue
		}
		if state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
			c.mu.Unlock()
			return nil, ErrStopInFlight
		}
		if state.handles[request.SessionID] || state.establishing[request.SessionID] != nil {
			c.mu.Unlock()
			return nil, errors.New("lifecycle session is already registered or establishing")
		}
		// Reservation removes timer authority synchronously in memory. The next
		// registration commit persists the updated journal together with the
		// conservative planned graph; forcing a separate fsync here would add a
		// full journal write to every idle-to-warm attach without adding safety.
		c.cancelDeadlineForAttachLocked(state)
		reservation := &establishment{
			coordinator: c,
			environment: request.EnvironmentID,
			session:     request.SessionID,
			phase:       establishmentReserved,
		}
		state.establishing[request.SessionID] = reservation
		c.emitLocked(Event{
			EnvironmentID: request.EnvironmentID,
			Generation:    state.journal.StartGeneration,
			Kind:          "attach-establishment-reserved",
			ReasonCode:    "attach-establishment-in-progress",
			At:            c.nowUTC(),
		})
		c.mu.Unlock()
		return reservation, nil
	}
}

func waitForAutomaticStop(ctx context.Context, done <-chan struct{}) error {
	timer := time.NewTimer(automaticStopAttachWaitLimit)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	case <-timer.C:
		return ErrStopInFlight
	}
}

func (e *establishment) Prepare(ctx context.Context, request AttachRequest) (EnvironmentRef, error) {
	if err := contextError(ctx); err != nil {
		return EnvironmentRef{}, err
	}
	if err := validateAttachRequest(request); err != nil {
		return EnvironmentRef{}, err
	}
	if request.EnvironmentID != e.environment || request.SessionID != e.session {
		return EnvironmentRef{}, errors.New("lifecycle attach request does not match establishment reservation")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase != establishmentReserved {
		return EnvironmentRef{}, errors.New("lifecycle establishment is not reserving")
	}
	e.coordinator.mu.Lock()
	defer e.coordinator.mu.Unlock()
	if e.coordinator.closing || e.coordinator.closed {
		return EnvironmentRef{}, ErrCoordinatorClosed
	}
	state, ok := e.coordinator.environments[e.environment]
	if !ok || state.establishing[e.session] != e {
		return EnvironmentRef{}, errors.New("lifecycle establishment reservation is not active")
	}
	if state.reconciling || state.blocked {
		return EnvironmentRef{}, ErrAttachBlocked
	}
	if state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		return EnvironmentRef{}, ErrStopInFlight
	}
	if len(state.handles) != 0 && !observationMatchesIncarnation(state.journal.Incarnation, request.Observation) {
		return EnvironmentRef{}, fmt.Errorf("%w: backend incarnation changed while sessions are active", ErrAttachBlocked)
	}
	state.observation = request.Observation
	incarnation, root, err := e.coordinator.selectIncarnationLocked(state, request)
	if err != nil {
		return EnvironmentRef{}, err
	}
	e.incarnation = incarnation
	e.root = root
	e.phase = establishmentPrepared
	e.coordinator.emitLocked(Event{
		EnvironmentID: e.environment,
		Generation:    incarnation.StartGeneration,
		Kind:          "attach-establishment-prepared",
		ReasonCode:    "attach-establishment-in-progress",
		At:            e.coordinator.nowUTC(),
	})
	return incarnation, nil
}

func (e *establishment) Promote(ctx context.Context) (Registration, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase != establishmentPrepared {
		return nil, errors.New("lifecycle establishment is not prepared")
	}
	e.coordinator.mu.Lock()
	defer e.coordinator.mu.Unlock()
	if e.coordinator.closing || e.coordinator.closed {
		return nil, ErrCoordinatorClosed
	}
	state, ok := e.coordinator.environments[e.environment]
	if !ok || state.establishing[e.session] != e {
		return nil, errors.New("lifecycle establishment reservation is not active")
	}
	if state.reconciling || state.blocked {
		return nil, ErrAttachBlocked
	}
	if state.mutation || attemptBlocksAttach(state.journal.StopAttempt) || state.stopCancel != nil {
		return nil, ErrStopInFlight
	}
	if state.handles[e.session] {
		return nil, errors.New("lifecycle session is already registered")
	}

	state.handles[e.session] = true
	delete(state.closing, e.session)
	sessionRef := ResourceRef{Kind: KindRunSession, ID: e.session, Generation: e.incarnation.StartGeneration}
	session := Resource{
		Ref:                  sessionRef,
		Owner:                OwnerRef{Kind: "daemon", ID: e.coordinator.daemonID, Generation: e.incarnation.StartGeneration},
		State:                StatePlanned,
		Dependencies:         []DependencySpec{{Ref: e.root, StopMode: StopModePin}},
		Persistence:          PersistenceEphemeral,
		ClosePolicy:          CloseCoTerminateWithRoot,
		PossibleVMDependency: true,
		UpdatedAt:            e.coordinator.nowUTC(),
	}
	if err := addOrJoinResource(state, e.session, session); err != nil {
		delete(state.handles, e.session)
		return nil, err
	}
	state.journal.Reconciliation = Reconciliation{DaemonInstanceID: e.coordinator.daemonID, State: "complete", ObservedAt: e.coordinator.nowUTC()}
	state.committed[e.session] = false
	delete(state.establishing, e.session)
	e.phase = establishmentPromoted
	registration := &registration{
		coordinator: e.coordinator,
		environment: e.environment,
		id:          e.session,
		incarnation: e.incarnation,
		root:        e.root,
		session:     sessionRef,
		refs:        []ResourceRef{sessionRef},
	}
	e.coordinator.emitLocked(Event{EnvironmentID: e.environment, Generation: e.incarnation.StartGeneration, Kind: "resource-registered", At: e.coordinator.nowUTC()})
	e.coordinator.emitLocked(Event{EnvironmentID: e.environment, Generation: e.incarnation.StartGeneration, Kind: "attach-establishment-promoted", ReasonCode: "owner-established", At: e.coordinator.nowUTC()})
	return registration, nil
}

func (e *establishment) Abort(ctx context.Context, cause error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase == establishmentPromoted || e.phase == establishmentAborted {
		return nil
	}
	e.coordinator.mu.Lock()
	defer e.coordinator.mu.Unlock()
	state, ok := e.coordinator.environments[e.environment]
	if ok && state.establishing[e.session] == e {
		delete(state.establishing, e.session)
		e.coordinator.emitLocked(Event{
			EnvironmentID: e.environment,
			Generation:    state.journal.StartGeneration,
			Kind:          "attach-establishment-aborted",
			ReasonCode:    establishmentAbortReason(cause),
			At:            e.coordinator.nowUTC(),
		})
		if len(state.establishing) == 0 && len(state.handles) == 0 && !e.coordinator.closing && !e.coordinator.closed {
			if state.journal.StartGeneration == 0 && state.journal.Incarnation == nil {
				delete(e.coordinator.environments, e.environment)
			} else if err := e.coordinator.scheduleIfIdleLocked(e.environment, state); err != nil {
				e.phase = establishmentAborted
				return err
			}
		}
	}
	e.phase = establishmentAborted
	return nil
}

func validateAttachRequest(request AttachRequest) error {
	if !idPattern.MatchString(request.EnvironmentID) || !idPattern.MatchString(request.InstanceName) || !idPattern.MatchString(request.SessionID) {
		return errors.New("lifecycle attach identity is invalid")
	}
	if err := request.Observation.Validate(); err != nil {
		return err
	}
	if request.Observation.InstanceName != request.InstanceName {
		return fmt.Errorf("%w: backend observation belongs to another instance", ErrAttachBlocked)
	}
	if request.Observation.State == backend.LifecycleUnknown {
		return fmt.Errorf("%w: backend observation is unknown", ErrAttachBlocked)
	}
	return nil
}

func establishmentAbortReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "attach-establishment-cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "attach-establishment-timeout"
	default:
		return "attach-establishment-aborted"
	}
}
