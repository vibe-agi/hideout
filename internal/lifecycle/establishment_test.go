package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestEstablishmentWaitsForEarlierReconciliationWithoutBlockingIt(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	started, err := coordinator.BeginReconciliation(context.Background(), "env-test")
	if err != nil || !started {
		t.Fatalf("BeginReconciliation started=%t err=%v", started, err)
	}
	type result struct {
		reservation EstablishmentReservation
		err         error
	}
	done := make(chan result, 1)
	go func() {
		reservation, reserveErr := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
			EnvironmentID: "env-test",
			SessionID:     "ses-one",
		})
		done <- result{reservation: reservation, err: reserveErr}
	}()
	select {
	case got := <-done:
		t.Fatalf("reservation bypassed reconciliation: %+v", got)
	case <-time.After(25 * time.Millisecond):
	}
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test",
		InstanceName:  "hideout-test",
		Observation: backend.LifecycleObservation{
			State:        backend.LifecycleAbsent,
			InstanceName: "hideout-test",
			ObservedAt:   time.Now().UTC(),
		},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("ReserveAttach after reconcile: %v", got.err)
		}
		if err := got.reservation.Abort(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reservation did not resume after reconciliation")
	}
}

func TestEstablishmentWaitEmitsOneRedactedStableEvent(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	var (
		eventsMu sync.Mutex
		events   []Event
	)
	coordinator.publish = func(event Event) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}
	started, err := coordinator.BeginReconciliation(context.Background(), "env-test")
	if err != nil || !started {
		t.Fatalf("BeginReconciliation started=%t err=%v", started, err)
	}
	done := make(chan EstablishmentReservation, 1)
	go func() {
		reservation, _ := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
			EnvironmentID: "env-test", SessionID: "ses-private-waiter",
		})
		done <- reservation
	}()
	time.Sleep(25 * time.Millisecond)
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleAbsent, InstanceName: "hideout-test", ObservedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	reservation := <-done
	if reservation == nil {
		t.Fatal("reservation did not resume")
	}
	if err := reservation.Abort(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	body, err := json.Marshal(events)
	waiting := 0
	for _, event := range events {
		if event.Kind == "attach-establishment-waiting" && event.ReasonCode == "reconciliation-pending" {
			waiting++
		}
	}
	eventsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if waiting != 1 {
		t.Fatalf("waiting events=%d events=%s", waiting, body)
	}
	if strings.Contains(string(body), "ses-private-waiter") {
		t.Fatalf("waiting event leaked reservation identity: %s", body)
	}
}

func TestEstablishmentWaitHonorsCancellation(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	started, err := coordinator.BeginReconciliation(context.Background(), "env-test")
	if err != nil || !started {
		t.Fatalf("BeginReconciliation started=%t err=%v", started, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, reserveErr := coordinator.ReserveAttach(ctx, EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
		done <- reserveErr
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReserveAttach error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled reservation wait did not return")
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	if len(state.establishing) != 0 {
		coordinator.mu.Unlock()
		t.Fatalf("cancelled wait published %d reservations", len(state.establishing))
	}
	coordinator.mu.Unlock()
}

func TestEstablishmentWaitsForAutomaticStopBeforeReserving(t *testing.T) {
	stopEntered := make(chan StopRequest, 1)
	releaseStop := make(chan struct{})
	coordinator, clock := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		stopEntered <- request
		<-releaseStop
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(),
		}}, nil
	})
	var (
		eventsMu sync.Mutex
		events   []Event
	)
	waitingEntered := make(chan struct{}, 1)
	coordinator.publish = func(event Event) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
		if event.Kind == "attach-establishment-waiting" && event.ReasonCode == "automatic-stop-pending" {
			select {
			case waitingEntered <- struct{}{}:
			default:
			}
		}
	}
	registration := prepareIdleRegistration(t, coordinator)
	stoppingIncarnation := registration.Incarnation()
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	stopFinished := make(chan struct{})
	go func() {
		clock.advance(DefaultIdleGrace)
		close(stopFinished)
	}()
	request := <-stopEntered
	if !request.Incarnation.Equal(stoppingIncarnation) {
		t.Fatalf("automatic stop targeted %+v, want %+v", request.Incarnation, stoppingIncarnation)
	}

	type result struct {
		reservation EstablishmentReservation
		err         error
	}
	reserved := make(chan result, 1)
	go func() {
		reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
			EnvironmentID: "env-test", SessionID: "ses-after-stop",
		})
		reserved <- result{reservation: reservation, err: err}
	}()
	select {
	case <-waitingEntered:
	case <-time.After(time.Second):
		t.Fatal("reservation did not enter automatic stop wait")
	}
	select {
	case got := <-reserved:
		t.Fatalf("reservation crossed automatic stop: %+v", got)
	default:
	}

	close(releaseStop)
	<-stopFinished
	var got result
	select {
	case got = <-reserved:
	case <-time.After(time.Second):
		t.Fatal("reservation did not resume after automatic stop")
	}
	if got.err != nil || got.reservation == nil {
		t.Fatalf("reservation after automatic stop=%T err=%v", got.reservation, got.err)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	stopCommitted := state.journal.StopAttempt != nil && state.journal.StopAttempt.State == "committed"
	incarnationCleared := state.journal.Incarnation == nil
	reservationActive := state.establishing["ses-after-stop"] != nil
	coordinator.mu.Unlock()
	if !stopCommitted || !incarnationCleared || !reservationActive {
		t.Fatalf("post-stop reservation state committed=%t incarnationCleared=%t active=%t", stopCommitted, incarnationCleared, reservationActive)
	}
	if err := got.reservation.Abort(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	eventsMu.Lock()
	waiting := 0
	for _, event := range events {
		if event.Kind == "attach-establishment-waiting" && event.ReasonCode == "automatic-stop-pending" {
			waiting++
		}
	}
	eventsMu.Unlock()
	if waiting != 1 {
		t.Fatalf("automatic stop waiting events=%d", waiting)
	}
}

func TestEstablishmentAutomaticStopWaitHonorsCancellation(t *testing.T) {
	stopEntered := make(chan struct{}, 1)
	releaseStop := make(chan struct{})
	waiting := make(chan struct{}, 1)
	coordinator, clock := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		stopEntered <- struct{}{}
		<-releaseStop
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(),
		}}, nil
	})
	coordinator.publish = func(event Event) {
		if event.Kind == "attach-establishment-waiting" && event.ReasonCode == "automatic-stop-pending" {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
	}
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	stopFinished := make(chan struct{})
	go func() {
		clock.advance(DefaultIdleGrace)
		close(stopFinished)
	}()
	<-stopEntered

	ctx, cancel := context.WithCancel(context.Background())
	reserved := make(chan error, 1)
	go func() {
		_, err := coordinator.ReserveAttach(ctx, EstablishmentRequest{
			EnvironmentID: "env-test", SessionID: "ses-cancelled",
		})
		reserved <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("reservation did not enter automatic stop wait")
	}
	cancel()
	select {
	case err := <-reserved:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled automatic stop wait error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled automatic stop wait did not return")
	}
	coordinator.mu.Lock()
	establishing := len(coordinator.environments["env-test"].establishing)
	coordinator.mu.Unlock()
	if establishing != 0 {
		t.Fatalf("cancelled automatic stop wait published %d reservations", establishing)
	}
	close(releaseStop)
	<-stopFinished
}

func TestEstablishmentAutomaticStopWaitFailsClosedOnUnknownResult(t *testing.T) {
	stopEntered := make(chan struct{}, 1)
	releaseStop := make(chan struct{})
	waiting := make(chan struct{}, 1)
	coordinator, clock := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		stopEntered <- struct{}{}
		<-releaseStop
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(), ReasonCode: "inventory-timeout",
		}}, errors.New("inventory timed out")
	})
	coordinator.publish = func(event Event) {
		if event.Kind == "attach-establishment-waiting" && event.ReasonCode == "automatic-stop-pending" {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
	}
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	stopFinished := make(chan struct{})
	go func() {
		clock.advance(DefaultIdleGrace)
		close(stopFinished)
	}()
	<-stopEntered
	reserved := make(chan error, 1)
	go func() {
		_, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
			EnvironmentID: "env-test", SessionID: "ses-after-unknown",
		})
		reserved <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("reservation did not enter automatic stop wait")
	}
	close(releaseStop)
	<-stopFinished
	select {
	case err := <-reserved:
		if !errors.Is(err, ErrAttachBlocked) || errors.Is(err, ErrStopInFlight) {
			t.Fatalf("unknown automatic stop result error=%T %v", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("reservation did not classify unknown automatic stop result")
	}
}

func TestEstablishmentBlocksNewReconciliationUntilAbort(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	if err != nil {
		t.Fatal(err)
	}
	if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err == nil || started {
		t.Fatalf("reconciliation crossed reservation started=%t err=%v", started, err)
	}
	if err := reservation.Abort(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err != nil || !started {
		t.Fatalf("reconciliation after abort started=%t err=%v", started, err)
	}
}

func TestEstablishmentPreservesBoundedBlockedReason(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	coordinator.mu.Lock()
	state, err := coordinator.loadEnvironmentLocked("env-test")
	if err != nil {
		coordinator.mu.Unlock()
		t.Fatal(err)
	}
	state.blocked = true
	state.journal.Reconciliation = blockedReconciliation(coordinator.daemonID, "owner-requires-explicit-recovery", coordinator.nowUTC())
	coordinator.mu.Unlock()

	_, err = coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	var blocked *AttachBlockedError
	if !errors.Is(err, ErrAttachBlocked) || !errors.As(err, &blocked) || blocked.ReasonCode != "owner-requires-explicit-recovery" {
		t.Fatalf("blocked reservation error=%T %v", err, err)
	}
	if !strings.Contains(err.Error(), "explicit recovery") {
		t.Fatalf("blocked reservation omitted recovery guidance: %v", err)
	}
}

func TestEstablishmentPrepareRejectsUnknownObservationWithoutRegistration(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	if err != nil {
		t.Fatal(err)
	}
	request := testAttachRequest(backend.LifecycleUnknown, "")
	request.Observation.ReasonCode = "backend-observation-unproved"
	if _, err := reservation.Prepare(context.Background(), request); !errors.Is(err, ErrAttachBlocked) {
		t.Fatalf("Prepare unknown error=%v", err)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	if len(state.handles) != 0 || len(state.establishing) != 1 {
		coordinator.mu.Unlock()
		t.Fatalf("unknown observation changed authority: handles=%d reservations=%d", len(state.handles), len(state.establishing))
	}
	coordinator.mu.Unlock()
}

func TestEstablishmentBlocksEveryConflictingLifecycleOperation(t *testing.T) {
	newPrepared := func(t *testing.T, enabled bool, stop StopFunc) (*Coordinator, EstablishmentReservation, EnvironmentRef) {
		t.Helper()
		coordinator, _ := newTestCoordinator(t, enabled, stop)
		reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
			EnvironmentID: "env-test", SessionID: "ses-one",
		})
		if err != nil {
			t.Fatal(err)
		}
		incarnation, err := reservation.Prepare(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
		if err != nil {
			t.Fatal(err)
		}
		return coordinator, reservation, incarnation
	}
	observation := backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: "hideout-test", BootID: testBootID,
		ObservedAt: time.Date(2026, 7, 16, 5, 1, 0, 0, time.UTC),
	}

	t.Run("direct reconciliation", func(t *testing.T) {
		coordinator, _, _ := newPrepared(t, false, nil)
		err := coordinator.Reconcile(context.Background(), ReconcileInput{
			EnvironmentID: "env-test", InstanceName: "hideout-test", Observation: observation,
		})
		if err == nil {
			t.Fatal("direct reconciliation crossed an active establishment reservation")
		}
	})

	t.Run("explicit stop", func(t *testing.T) {
		stopCalls := 0
		coordinator, _, _ := newPrepared(t, true, func(context.Context, StopRequest) (StopResult, error) {
			stopCalls++
			return StopResult{Observation: backend.LifecycleObservation{
				State: backend.LifecycleStopped, InstanceName: "hideout-test", ObservedAt: time.Now().UTC(),
			}}, nil
		})
		if _, err := coordinator.StopExplicit(context.Background(), "env-test"); err == nil {
			t.Fatal("explicit stop crossed an active establishment reservation")
		}
		if stopCalls != 0 {
			t.Fatalf("explicit stop invoked backend %d times", stopCalls)
		}
	})

	t.Run("forget", func(t *testing.T) {
		coordinator, _, _ := newPrepared(t, false, nil)
		if err := coordinator.ForgetEnvironment("env-test"); err == nil {
			t.Fatal("forget removed lifecycle state with an active establishment reservation")
		}
		coordinator.mu.Lock()
		_, retained := coordinator.environments["env-test"]
		coordinator.mu.Unlock()
		if !retained {
			t.Fatal("forget discarded the reservation owner state")
		}
	})

	t.Run("destructive mutation", func(t *testing.T) {
		coordinator, _, _ := newPrepared(t, false, nil)
		mutated := false
		err := coordinator.RunDestructiveMutation(context.Background(), "env-test", func(context.Context) error {
			mutated = true
			return nil
		})
		if !errors.Is(err, ErrMutationBlockedByActivity) {
			t.Fatalf("destructive mutation error=%v", err)
		}
		if mutated {
			t.Fatal("destructive mutation callback crossed an active establishment reservation")
		}
	})

	t.Run("idle expiry", func(t *testing.T) {
		stopCalls := 0
		coordinator, _, incarnation := newPrepared(t, true, func(context.Context, StopRequest) (StopResult, error) {
			stopCalls++
			return StopResult{Observation: backend.LifecycleObservation{
				State: backend.LifecycleStopped, InstanceName: "hideout-test", ObservedAt: time.Now().UTC(),
			}}, nil
		})
		coordinator.mu.Lock()
		state := coordinator.environments["env-test"]
		state.deadlineSeq++
		sequence := state.deadlineSeq
		state.journal.IdleDeadline = &IdleDeadline{
			Incarnation: incarnation, DaemonInstanceID: coordinator.daemonID,
			ScheduledAt: coordinator.nowUTC(), Deadline: coordinator.nowUTC(), Generation: sequence,
		}
		coordinator.mu.Unlock()
		coordinator.expire("env-test", incarnation, sequence)
		if stopCalls != 0 {
			t.Fatalf("idle expiry invoked backend %d times", stopCalls)
		}
	})
}

func TestEstablishmentStatusIsDerivedAndOmitsReservationIdentity(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-two"})
	if err != nil {
		t.Fatal(err)
	}
	statuses := coordinator.Snapshot()
	if len(statuses) != 1 || statuses[0].EstablishingSessions != 1 || statuses[0].Activity != ActivityEstablishing ||
		statuses[0].ReasonCode != "attach-establishment-in-progress" {
		t.Fatalf("unexpected establishing status: %+v", statuses)
	}
	encoded, err := json.Marshal(statuses[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ses-two") {
		t.Fatalf("reservation identity leaked through status: %s", encoded)
	}
	if err := reservation.Abort(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestEstablishmentStatusAndEventsRedactControlMaterial(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	var events []Event
	coordinator.publish = func(event Event) { events = append(events, event) }
	reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
		EnvironmentID: "env-test", SessionID: "ses-token-supersecret",
	})
	if err != nil {
		t.Fatal(err)
	}
	statusBody, err := json.Marshal(coordinator.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("path=/Users/alice/project lock=owner.lock credential=sk-secret pid=4242 args=rm-all")
	if err := reservation.Abort(context.Background(), cause); err != nil {
		t.Fatal(err)
	}
	eventBody, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(statusBody) + string(eventBody)
	for _, forbidden := range []string{
		"ses-token-supersecret", "/Users/alice/project", "owner.lock", "sk-secret", "4242", "rm-all",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("establishment observability leaked %q: %s", forbidden, combined)
		}
	}
	if !strings.Contains(string(eventBody), `"Kind":"attach-establishment-aborted"`) ||
		!strings.Contains(string(eventBody), `"ReasonCode":"attach-establishment-aborted"`) {
		t.Fatalf("establishment abort event was not stable and bounded: %s", eventBody)
	}
}

func TestEstablishmentContractCarriesOnlyOpaqueIdentities(t *testing.T) {
	typeOfRequest := reflect.TypeOf(EstablishmentRequest{})
	if typeOfRequest.NumField() != 3 ||
		typeOfRequest.Field(0).Name != "EnvironmentID" ||
		typeOfRequest.Field(1).Name != "SessionID" ||
		typeOfRequest.Field(2).Name != "MutationKeys" ||
		typeOfRequest.Field(2).Type != reflect.TypeOf([]string{}) {
		t.Fatalf("establishment request authority drifted: %v", typeOfRequest)
	}
}

func TestEstablishmentPrepareAndPromoteAreSingleUse(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{
		EnvironmentID: "env-test",
		SessionID:     "ses-one",
	})
	if err != nil {
		t.Fatalf("ReserveAttach: %v", err)
	}

	incarnation, err := reservation.Prepare(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if incarnation.EnvironmentID != "env-test" || incarnation.InstanceName != "hideout-test" || incarnation.BootID != testBootID {
		t.Fatalf("unexpected prepared incarnation: %+v", incarnation)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	if len(state.establishing) != 1 || len(state.handles) != 0 {
		coordinator.mu.Unlock()
		t.Fatalf("prepare published normal registration: reservations=%d handles=%d", len(state.establishing), len(state.handles))
	}
	coordinator.mu.Unlock()

	registration, err := reservation.Promote(context.Background())
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if registration.Incarnation() != incarnation {
		t.Fatalf("promotion incarnation=%+v want %+v", registration.Incarnation(), incarnation)
	}
	coordinator.mu.Lock()
	if len(state.establishing) != 0 || !state.handles["ses-one"] {
		coordinator.mu.Unlock()
		t.Fatalf("promotion was not atomic: reservations=%d handle=%t", len(state.establishing), state.handles["ses-one"])
	}
	coordinator.mu.Unlock()
	if _, err := reservation.Promote(context.Background()); err == nil {
		t.Fatal("second promotion should fail closed")
	}
	if _, err := reservation.Prepare(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID)); err == nil {
		t.Fatal("prepare after promotion should fail closed")
	}
	if err := reservation.Abort(context.Background(), nil); err != nil {
		t.Fatalf("Abort after promotion: %v", err)
	}
	if !state.handles["ses-one"] {
		t.Fatal("abort after promotion removed the normal registration")
	}
}

func TestEstablishmentAbortIsScopedAndIdempotent(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-two"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Abort(context.Background(), context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err := first.Abort(context.Background(), context.Canceled); err != nil {
		t.Fatalf("second Abort: %v", err)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	_, firstHeld := state.establishing["ses-one"]
	_, secondHeld := state.establishing["ses-two"]
	coordinator.mu.Unlock()
	if firstHeld || !secondHeld {
		t.Fatalf("abort crossed reservation ownership: first=%t second=%t", firstHeld, secondHeld)
	}
	if err := second.Abort(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestEstablishmentConcurrentReservationsPromoteAndAbortIndependently(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-two"})
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := testAttachRequest(backend.LifecycleRunning, testBootID)
	firstIncarnation, err := first.Prepare(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := firstRequest
	secondRequest.SessionID = "ses-two"
	secondIncarnation, err := second.Prepare(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if firstIncarnation != secondIncarnation {
		t.Fatalf("compatible siblings selected different incarnations: first=%+v second=%+v", firstIncarnation, secondIncarnation)
	}
	firstRegistration, err := first.Promote(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondRegistration, err := second.Promote(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	firstHeld := state.handles["ses-one"]
	secondHeld := state.handles["ses-two"]
	reservationCount := len(state.establishing)
	coordinator.mu.Unlock()
	if !firstHeld || !secondHeld || reservationCount != 0 {
		t.Fatalf("sibling promotion state: first=%t second=%t reservations=%d", firstHeld, secondHeld, reservationCount)
	}
	if err := firstRegistration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	coordinator.mu.Lock()
	firstHeld = state.handles["ses-one"]
	secondHeld = state.handles["ses-two"]
	coordinator.mu.Unlock()
	if firstHeld || !secondHeld {
		t.Fatalf("first cleanup crossed sibling ownership: first=%t second=%t", firstHeld, secondHeld)
	}
	if err := secondRegistration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCloseDropsAllInMemoryEstablishments(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-two"})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	coordinator.mu.Lock()
	remaining := 0
	if state := coordinator.environments["env-test"]; state != nil {
		remaining = len(state.establishing)
	}
	coordinator.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("coordinator close retained %d provisional reservations", remaining)
	}
	if err := first.Abort(context.Background(), context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err := second.Abort(context.Background(), context.Canceled); err != nil {
		t.Fatal(err)
	}
}

func TestEstablishmentRejectsInvalidAndMismatchedIdentity(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	for _, request := range []EstablishmentRequest{
		{},
		{EnvironmentID: "bad/path", SessionID: "ses-one"},
		{EnvironmentID: "env-test", SessionID: "bad session"},
	} {
		if _, err := coordinator.ReserveAttach(context.Background(), request); err == nil {
			t.Fatalf("invalid reservation accepted: %+v", request)
		}
	}
	reservation, err := coordinator.ReserveAttach(context.Background(), EstablishmentRequest{EnvironmentID: "env-test", SessionID: "ses-one"})
	if err != nil {
		t.Fatal(err)
	}
	request := testAttachRequest(backend.LifecycleRunning, testBootID)
	request.SessionID = "ses-other"
	if _, err := reservation.Prepare(context.Background(), request); err == nil {
		t.Fatal("mismatched attach request was accepted")
	}
}
