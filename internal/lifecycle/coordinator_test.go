package lifecycle

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

type testClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*testTimer
}

type testTimer struct {
	clock   *testClock
	due     time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *testTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := !t.stopped && !t.fired
	t.stopped = true
	return wasActive
}

func (c *testClock) after(delay time.Duration, fn func()) timerHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &testTimer{clock: c, due: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *testClock) advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	var callbacks []func()
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fired && !timer.due.After(c.now) {
			timer.fired = true
			callbacks = append(callbacks, timer.fn)
		}
	}
	c.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func newTestCoordinator(t *testing.T, enabled bool, stop StopFunc) (*Coordinator, *testClock) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: JournalStore{Root: root}, DaemonID: "daemon-test", IdleGrace: DefaultIdleGrace,
		Now:       func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now },
		AfterFunc: clock.after, Stop: stop, Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator, clock
}

func testAttachRequest(state backend.LifecycleState, bootID string) AttachRequest {
	return AttachRequest{
		EnvironmentID: "env-test", InstanceName: "hideout-test", SessionID: "ses-one",
		Observation: backend.LifecycleObservation{State: state, InstanceName: "hideout-test", BootID: bootID, ObservedAt: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)},
	}
}

func waitForJournal(t *testing.T, store JournalStore, environmentID string, check func(Journal) bool) Journal {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		journal, err := store.Load(environmentID)
		if err == nil && check(journal) {
			return journal
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("journal did not converge: %v", err)
			}
			t.Fatalf("journal did not converge: %+v", journal)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func prepareIdleRegistration(t *testing.T, coordinator *Coordinator) Registration {
	t.Helper()
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	return registration
}

func TestCoordinatorAttachCancelsGraceAndStaleTimerHasNoAuthority(t *testing.T) {
	stops := 0
	coordinator, clock := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		stops++
		return StopResult{}, errors.New("must not run")
	})
	first := prepareIdleRegistration(t, coordinator)
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	secondRequest := testAttachRequest(backend.LifecycleRunning, testBootID)
	secondRequest.SessionID = "ses-two"
	second, err := coordinator.BeginAttach(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(DefaultIdleGrace)
	if stops != 0 {
		t.Fatalf("cancelled deadline invoked stop %d times", stops)
	}
	if err := second.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
}

func TestBindBootRecordsCurrentObservationRatherThanRelyingOnJournal(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleStopped, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := coordinator.Snapshot()[0].BackendState; got != string(backend.LifecycleStopped) {
		t.Fatalf("pre-start observation=%q want stopped", got)
	}
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	status := coordinator.Snapshot()[0]
	if status.BackendState != string(backend.LifecycleRunning) || status.Activity != ActivityPinned {
		t.Fatalf("boot binding did not establish current running observation: %+v", status)
	}
}

func TestLoadedJournalCannotBecomeRunningProofOrScheduleGrace(t *testing.T) {
	first, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, first)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	timerCreated := false
	second, err := NewCoordinator(CoordinatorOptions{
		Store:     first.store,
		DaemonID:  "daemon-test",
		IdleGrace: DefaultIdleGrace,
		AfterFunc: func(time.Duration, func()) timerHandle {
			timerCreated = true
			return &testTimer{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second.mu.Lock()
	state, err := second.loadEnvironmentLocked("env-test")
	if err != nil {
		second.mu.Unlock()
		t.Fatal(err)
	}
	status := second.statusLocked("env-test", state)
	if err := second.scheduleIfIdleLocked("env-test", state); err != nil {
		second.mu.Unlock()
		t.Fatal(err)
	}
	blocked := state.blocked
	deadline := state.journal.IdleDeadline
	second.mu.Unlock()

	if status.BackendState != string(backend.LifecycleUnknown) || status.Activity != ActivityBlocked {
		t.Fatalf("loaded discovery journal became current backend proof: %+v", status)
	}
	if !blocked || deadline != nil || timerCreated {
		t.Fatalf("loaded discovery journal scheduled grace: blocked=%t deadline=%+v timer=%t", blocked, deadline, timerCreated)
	}
}

func TestForgetEnvironmentRejectsLiveHandleAndRemovesTerminalJournal(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	if err := coordinator.ForgetEnvironment("env-test"); err == nil {
		t.Fatal("environment lifecycle metadata was forgotten with a live handle")
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ForgetEnvironment("env-test"); err != nil {
		t.Fatal(err)
	}
	if statuses := coordinator.Snapshot(); len(statuses) != 0 {
		t.Fatalf("forgotten environment remains in status: %+v", statuses)
	}
	if _, err := coordinator.store.Load("env-test"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forgotten environment journal remains: %v", err)
	}
	ids, err := coordinator.store.ListEnvironmentIDs()
	if err != nil || len(ids) != 0 {
		t.Fatalf("forgotten environment directory remains: ids=%v err=%v", ids, err)
	}
}

func TestDestructiveMutationCancelsGraceBlocksAttachAndForgetsOnSuccess(t *testing.T) {
	stops := 0
	coordinator, clock := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		stops++
		return StopResult{}, errors.New("stop must not race destructive mutation")
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	proceed := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- coordinator.RunDestructiveMutation(context.Background(), "env-test", func(context.Context) error {
			close(entered)
			<-proceed
			return nil
		})
	}()
	<-entered
	request := testAttachRequest(backend.LifecycleRunning, testBootID)
	request.SessionID = "ses-two"
	if _, err := coordinator.BeginAttach(context.Background(), request); err == nil {
		t.Fatal("attach entered an explicit destructive mutation")
	}
	clock.advance(DefaultIdleGrace)
	if stops != 0 {
		t.Fatalf("cancelled grace raced destructive mutation: stops=%d", stops)
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if statuses := coordinator.Snapshot(); len(statuses) != 0 {
		t.Fatalf("successful mutation left lifecycle status: %+v", statuses)
	}
	if _, err := coordinator.store.Load("env-test"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful mutation left lifecycle journal: %v", err)
	}
}

func TestDestructiveMutationFailureKeepsBlockedDiscoveryTruth(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	want := errors.New("cleanup failed")
	if err := coordinator.RunDestructiveMutation(context.Background(), "env-test", func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("mutation error=%v want=%v", err, want)
	}
	statuses := coordinator.Snapshot()
	if len(statuses) != 1 || statuses[0].Activity != ActivityBlocked || statuses[0].Reconciliation != "blocked" || statuses[0].ReasonCode != "cleanup-unproved" || statuses[0].IdleDeadline != nil {
		t.Fatalf("failed mutation was not retained as blocked discovery truth: %+v", statuses)
	}
	if _, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID)); !errors.Is(err, ErrAttachBlocked) {
		t.Fatalf("failed destructive mutation did not block attach: %v", err)
	}
}

func TestCoordinatorStopsOnlyExactIncarnationAfterFullGrace(t *testing.T) {
	var request StopRequest
	var clock *testClock
	coordinator, createdClock := newTestCoordinator(t, true, func(ctx context.Context, value StopRequest) (StopResult, error) {
		request = value
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining > 35*time.Second || remaining < 34*time.Second {
			t.Fatalf("stop transaction is not bounded to 35 seconds: %v", remaining)
		}
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: value.Incarnation.InstanceName,
			ObservedAt: clock.now,
		}}, nil
	})
	clock = createdClock
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	clock.advance(DefaultIdleGrace - time.Millisecond)
	if request.AttemptID != "" {
		t.Fatal("stop ran before full grace")
	}
	clock.advance(time.Millisecond)
	if request.Incarnation.BootID != testBootID || request.Mode != "automatic" {
		t.Fatalf("stop was not bound to exact incarnation: %+v", request)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.StopAttempt == nil || journal.StopAttempt.State != "committed" || journal.Incarnation != nil || len(journal.Resources) != 0 {
		t.Fatalf("observed stop was not committed: %+v", journal)
	}
}

func TestCoordinatorUnknownStopBlocksAttach(t *testing.T) {
	coordinator, clock := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: "hideout-test", ObservedAt: clockTime(), ReasonCode: "inventory-timeout",
		}}, errors.New("observation timed out")
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	clock.advance(DefaultIdleGrace)
	next := testAttachRequest(backend.LifecycleRunning, testBootID)
	next.SessionID = "ses-two"
	if _, err := coordinator.BeginAttach(context.Background(), next); !errors.Is(err, ErrAttachBlocked) && !errors.Is(err, ErrStopInFlight) {
		t.Fatalf("unknown stop allowed attach: %v", err)
	}
	status := coordinator.Snapshot()
	if len(status) != 1 || status[0].Activity != ActivityStoppingUnknown {
		t.Fatalf("unknown stop not surfaced honestly: %+v", status)
	}
}

func TestCoordinatorMalformedTerminalStopResultFailsClosed(t *testing.T) {
	coordinator, clock := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: "hideout-other", ObservedAt: time.Now().UTC(),
		}}, nil
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	clock.advance(DefaultIdleGrace)
	status := coordinator.Snapshot()
	if len(status) != 1 || status[0].Activity != ActivityStoppingUnknown || status[0].BackendState != string(backend.LifecycleUnknown) {
		t.Fatalf("cross-instance terminal result became stop proof: %+v", status)
	}
}

func TestCoordinatorShadowModeNeverInvokesBackend(t *testing.T) {
	called := false
	coordinator, clock := newTestCoordinator(t, false, func(context.Context, StopRequest) (StopResult, error) {
		called = true
		return StopResult{}, nil
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	clock.advance(DefaultIdleGrace)
	if called {
		t.Fatal("shadow evaluation invoked backend stop")
	}
}

func TestCoordinatorCloseCancelsAndWaitsForInFlightStop(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	coordinator, clock := newTestCoordinator(t, true, func(ctx context.Context, request StopRequest) (StopResult, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(), ReasonCode: "shutdown-cancelled",
		}}, ctx.Err()
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	advanceDone := make(chan struct{})
	go func() {
		clock.advance(DefaultIdleGrace)
		close(advanceDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("automatic stop did not enter")
	}
	started := time.Now()
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= coordinatorCloseWait {
		t.Fatal("shutdown waited for the full bound after cancellation")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("stop callback remained live after coordinator close")
	}
	select {
	case <-advanceDone:
	case <-time.After(time.Second):
		t.Fatal("timer callback did not finish after shutdown")
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.StopAttempt == nil || journal.StopAttempt.State != "unknown" || journal.Incarnation == nil {
		t.Fatalf("cancelled stop was reported as terminal: %+v", journal)
	}
}

func TestCoordinatorCloseFencesLateSessionCleanupWrites(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	before, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); !errors.Is(err, ErrCoordinatorClosed) {
		t.Fatalf("late cleanup was not fenced after coordinator close: %v", err)
	}
	after, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Resources) != len(before.Resources) || after.Incarnation == nil || before.Incarnation == nil || !after.Incarnation.Equal(*before.Incarnation) {
		t.Fatalf("late cleanup rewrote restart discovery after close: before=%+v after=%+v", before, after)
	}
	if _, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID)); !errors.Is(err, ErrCoordinatorClosed) {
		t.Fatalf("late attach was not fenced after coordinator close: %v", err)
	}
}

func TestCoordinatorExplicitStopSharesAttachSerialization(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: "hideout-test", ObservedAt: time.Now().UTC(),
		}}, nil
	})
	registration := prepareIdleRegistration(t, coordinator)
	if _, err := coordinator.StopExplicit(context.Background(), "env-test"); err == nil {
		t.Fatal("explicit stop did not refuse an active attach")
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.StopExplicit(context.Background(), "env-test")
	if err != nil {
		t.Fatal(err)
	}
	if status.Activity != ActivityStopped || status.BackendState != string(backend.LifecycleStopped) {
		t.Fatalf("explicit stop status=%+v", status)
	}
	request := testAttachRequest(backend.LifecycleRunning, testBootID)
	request.SessionID = "ses-two"
	next, err := coordinator.BeginAttach(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if next.Incarnation().StartGeneration <= status.StartGeneration {
		t.Fatalf("stopped incarnation authority was reused: old=%d new=%d", status.StartGeneration, next.Incarnation().StartGeneration)
	}
}

func TestCoordinatorExplicitStopRejectsOrphanThatRequiresProviderCleanup(t *testing.T) {
	stops := 0
	coordinator, _ := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		stops++
		return StopResult{}, errors.New("must not run")
	})
	registration := prepareIdleRegistration(t, coordinator)
	if _, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindNetworkService, ID: "env-test",
		OwnerKind: "manager", OwnerID: "env-test", State: StateActive,
		Dependencies: []DependencySpec{{Ref: registration.Root(), StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), os.ErrPermission); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.StopExplicit(context.Background(), "env-test"); err == nil {
		t.Fatal("explicit stop accepted an orphan that requires provider cleanup")
	}
	if stops != 0 {
		t.Fatalf("backend stop ran %d times", stops)
	}
}

func TestCoordinatorExplicitStopRejectsUnclassifiedProviderStateWithoutOrphanRow(t *testing.T) {
	stops := 0
	coordinator, _ := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		stops++
		return StopResult{}, errors.New("must not run")
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test",
			BootID: testBootID, ObservedAt: time.Now().UTC(),
		},
		AdditionalUnproved: true,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.StopExplicit(context.Background(), "env-test")
	if err == nil || status.Activity != ActivityBlocked {
		t.Fatalf("explicit stop accepted unclassified provider state: status=%+v err=%v", status, err)
	}
	if stops != 0 {
		t.Fatalf("backend stop ran %d times", stops)
	}
}

func TestCoordinatorExplicitStopRejectsLiveOrUnprovedReconciledOwner(t *testing.T) {
	stops := 0
	coordinator, _ := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		stops++
		return StopResult{}, errors.New("must not run")
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test",
			BootID: testBootID, ObservedAt: time.Now().UTC(),
		},
		OwnerSessionIDs:    []string{"ses-live"},
		AdditionalUnproved: true,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.StopExplicit(context.Background(), "env-test")
	if err == nil || status.Activity != ActivityBlocked || len(status.Orphans) != 1 {
		t.Fatalf("explicit stop accepted live/unproved owner: status=%+v err=%v", status, err)
	}
	if stops != 0 {
		t.Fatalf("backend stop ran %d times", stops)
	}
}

func TestCoordinatorExplicitStopCoTerminatesApprovedGuestOrphans(t *testing.T) {
	stops := 0
	coordinator, _ := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		stops++
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(),
		}}, nil
	})
	registration := prepareIdleRegistration(t, coordinator)
	supervisor, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindGuestSupervisor, ID: "ses-one",
		OwnerKind: "session", OwnerID: "ses-one", State: StateActive,
		Dependencies: []DependencySpec{
			{Ref: registration.Root(), StopMode: StopModeDrain},
			{Ref: registration.Session(), StopMode: StopModeDrain},
		},
		Persistence: PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindGuestTarget, ID: "ses-one",
		OwnerKind: "session", OwnerID: "ses-one", State: StateActive,
		Dependencies: []DependencySpec{{Ref: supervisor, StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), os.ErrPermission); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.StopExplicit(context.Background(), "env-test")
	if err != nil {
		t.Fatal(err)
	}
	if stops != 1 || status.Activity != ActivityStopped {
		t.Fatalf("stops=%d status=%+v", stops, status)
	}
}

func TestCoordinatorExplicitStopCoTerminatesReconciledStaleSessionOwner(t *testing.T) {
	stops := 0
	coordinator, _ := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		stops++
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(),
		}}, nil
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test",
			BootID: testBootID, ObservedAt: time.Now().UTC(),
		},
		OwnerSessionIDs: []string{"ses-stale"},
	}); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.StopExplicit(context.Background(), "env-test")
	if err != nil {
		t.Fatal(err)
	}
	if stops != 1 || status.Activity != ActivityStopped {
		t.Fatalf("stops=%d status=%+v", stops, status)
	}
}

func TestCoordinatorExplicitStopDoesNotReportUnknownObservationAsSuccess(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: request.Incarnation.InstanceName,
			ObservedAt: time.Now().UTC(), ReasonCode: "observation-unavailable",
		}}, nil
	})
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.StopExplicit(context.Background(), "env-test")
	if err == nil || status.Activity != ActivityStoppingUnknown {
		t.Fatalf("unknown stop was reported successful: status=%+v err=%v", status, err)
	}
}

func TestCoordinatorAttachNeverEntersIncarnationWhileStopIsInFlight(t *testing.T) {
	const iterations = 100
	for iteration := 0; iteration < iterations; iteration++ {
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
		registration := prepareIdleRegistration(t, coordinator)
		if err := registration.Finish(context.Background(), nil); err != nil {
			t.Fatal(err)
		}

		timerDone := make(chan struct{})
		go func() {
			clock.advance(DefaultIdleGrace)
			close(timerDone)
		}()
		request := <-stopEntered
		if request.Incarnation.BootID != testBootID {
			t.Fatalf("iteration %d stopped wrong incarnation: %+v", iteration, request)
		}

		attach := testAttachRequest(backend.LifecycleRunning, testBootID)
		attach.SessionID = "ses-racing"
		if _, err := coordinator.BeginAttach(context.Background(), attach); !errors.Is(err, ErrStopInFlight) {
			t.Fatalf("iteration %d attach entered stopping incarnation: %v", iteration, err)
		}
		close(releaseStop)
		<-timerDone

		next, err := coordinator.BeginAttach(context.Background(), attach)
		if err != nil {
			t.Fatalf("iteration %d attach after observed stop: %v", iteration, err)
		}
		if next.Incarnation().StartGeneration <= request.Incarnation.StartGeneration {
			t.Fatalf("iteration %d reused stopped generation: old=%d new=%d", iteration, request.Incarnation.StartGeneration, next.Incarnation().StartGeneration)
		}
		if err := next.Finish(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if err := coordinator.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func clockTime() time.Time { return time.Date(2026, 7, 16, 5, 0, 15, 0, time.UTC) }
