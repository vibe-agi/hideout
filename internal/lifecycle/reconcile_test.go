package lifecycle

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestReconcileReplacementDaemonStartsFreshFullGrace(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	firstClock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	first := coordinatorForSharedRoot(t, root, "daemon-first", firstClock)
	registration, err := first.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondClock := &testClock{now: firstClock.now.Add(7 * time.Second)}
	second := coordinatorForSharedRoot(t, root, "daemon-second", secondClock)
	if err := second.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: "hideout-test", BootID: testBootID, ObservedAt: secondClock.now},
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := second.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.Reconciliation.DaemonInstanceID != "daemon-second" || journal.Reconciliation.State != "complete" || journal.IdleDeadline == nil {
		t.Fatalf("replacement daemon did not complete reconciliation: %+v", journal)
	}
	if journal.IdleDeadline.ScheduledAt != secondClock.now || journal.IdleDeadline.Deadline.Sub(secondClock.now) != DefaultIdleGrace {
		t.Fatalf("old deadline was resumed instead of granting fresh grace: %+v", journal.IdleDeadline)
	}
}

func TestRestartTreatsPreCheckpointPlannedGraphConservatively(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	first := coordinatorForSharedRoot(t, root, "daemon-first", clock)
	registration, err := first.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindGuestSupervisor, ID: "ses-one", OwnerKind: "session", OwnerID: "ses-one",
		Dependencies: []DependencySpec{
			{Ref: registration.Root(), StopMode: StopModeDrain},
			{Ref: registration.Session(), StopMode: StopModeDrain},
		},
		Persistence: PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindGuestTarget, ID: "ses-one", OwnerKind: "session", OwnerID: "ses-one",
		Dependencies: []DependencySpec{{Ref: supervisor, StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), supervisor, StateStarting); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), supervisor, StateActive); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), target, StateStarting); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), target, StateActive); err != nil {
		t.Fatal(err)
	}

	// Crash before the coalesced state checkpoint. The durable planned graph is
	// intentionally the conservative recovery envelope.
	first.mu.Lock()
	state := first.environments["env-test"]
	if state.checkpoint != nil {
		state.checkpoint.Stop()
		state.checkpoint = nil
	}
	first.closed = true
	first.mu.Unlock()

	secondClock := &testClock{now: clock.now.Add(time.Second)}
	second := coordinatorForSharedRoot(t, root, "daemon-second", secondClock)
	if started, err := second.BeginReconciliation(context.Background(), "env-test"); err != nil || !started {
		t.Fatalf("replacement reconciliation started=%t err=%v", started, err)
	}
	if err := second.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test", OwnerSessionIDs: []string{"ses-one"},
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test", BootID: testBootID, ObservedAt: secondClock.now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := second.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ResourceRef{registration.Session(), supervisor, target} {
		if resource := findResource(t, journal.Resources, ref); resource.State != StateOrphaned {
			t.Fatalf("pre-checkpoint provider was silently treated as absent: %+v", resource)
		}
	}
}

func TestReplacementDaemonReconcilesPriorUnknownStopAttempt(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	firstClock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	first := coordinatorForSharedRoot(t, root, "daemon-first", firstClock)
	registration := prepareRegistrationForCoordinator(t, first)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	journal, err := first.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	journal.StopAttempt = &StopAttempt{
		ID: "stop-1-1", Incarnation: *journal.Incarnation,
		DaemonInstanceID: "daemon-first", Mode: "automatic", State: "unknown",
		StartedAt: firstClock.now,
		Observation: &backendObservationSnapshot{
			State: string(backend.LifecycleUnknown), ObservedAt: firstClock.now,
			ReasonCode: "backend-stop-observation-timeout",
		},
	}
	if err := first.store.Write(journal); err != nil {
		t.Fatal(err)
	}

	secondClock := &testClock{now: firstClock.now.Add(time.Second)}
	second := coordinatorForSharedRoot(t, root, "daemon-second", secondClock)
	started, err := second.BeginReconciliation(context.Background(), "env-test")
	if err != nil || !started {
		t.Fatalf("replacement reconciliation started=%t err=%v", started, err)
	}
	if err := second.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: "hideout-test", ObservedAt: secondClock.now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	after, err := second.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if after.StopAttempt != nil || after.Incarnation != nil || after.Reconciliation.State != "complete" {
		t.Fatalf("replacement daemon retained prior stop authority: %+v", after)
	}
}

func TestReconcileOldSessionBecomesOrphanAndNeverSchedulesStop(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	firstClock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	first := coordinatorForSharedRoot(t, root, "daemon-first", firstClock)
	registration, err := first.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondClock := &testClock{now: firstClock.now.Add(time.Second)}
	second := coordinatorForSharedRoot(t, root, "daemon-second", secondClock)
	if err := second.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test", OwnerSessionIDs: []string{"ses-one"},
		Observation: backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: "hideout-test", BootID: testBootID, ObservedAt: secondClock.now},
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := second.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.Reconciliation.State != "blocked" || journal.IdleDeadline != nil {
		t.Fatalf("old authority looked idle after restart: %+v", journal)
	}
	if resource := findResource(t, journal.Resources, registration.Session()); resource.State != StateOrphaned {
		t.Fatalf("old session was silently re-adopted: %+v", resource)
	}
}

func TestReconcileExternalBootChangeAllocatesBlockedGeneration(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	firstClock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	first := coordinatorForSharedRoot(t, root, "daemon-first", firstClock)
	registration := prepareRegistrationForCoordinator(t, first)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	secondClock := &testClock{now: firstClock.now.Add(time.Second)}
	second := coordinatorForSharedRoot(t, root, "daemon-second", secondClock)
	newBoot := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if err := second.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: "hideout-test", BootID: newBoot, ObservedAt: secondClock.now},
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := second.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.StartGeneration != 2 || journal.Incarnation == nil || journal.Incarnation.BootID != newBoot || journal.Reconciliation.State != "blocked" || journal.Reconciliation.ReasonCode != "backend-incarnation-changed" || journal.IdleDeadline != nil {
		t.Fatalf("external boot was adopted as old generation: %+v", journal)
	}
}

func TestReconcileRejectsCrossInstanceObservationWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	coordinator := coordinatorForSharedRoot(t, root, "daemon-test", clock)
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-other", BootID: testBootID, ObservedAt: clock.now,
		},
	}); err == nil {
		t.Fatal("cross-instance backend observation was accepted")
	}
	if _, err := coordinator.store.Load("env-test"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed reconciliation wrote lifecycle authority: %v", err)
	}
}

func TestReconcileUnknownFirstObservationPersistsBlockedState(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	coordinator := coordinatorForSharedRoot(t, root, "daemon-test", clock)
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: "hideout-test",
			ObservedAt: clock.now, ReasonCode: "inventory-unavailable",
		},
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.StartGeneration != 1 || journal.Reconciliation.State != "blocked" || journal.Reconciliation.ReasonCode != "inventory-unavailable" || journal.IdleDeadline != nil {
		t.Fatalf("unknown first observation was not persisted fail closed: %+v", journal)
	}
	status := coordinator.Snapshot()
	if len(status) != 1 || status[0].Activity != ActivityBlocked || status[0].BackendState != string(backend.LifecycleUnknown) || status[0].ReasonCode != "inventory-unavailable" {
		t.Fatalf("unknown first observation status=%+v", status)
	}
}

func coordinatorForSharedRoot(t *testing.T, root, daemonID string, clock *testClock) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: JournalStore{Root: root}, DaemonID: daemonID, Now: func() time.Time { return clock.now }, AfterFunc: clock.after,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func prepareRegistrationForCoordinator(t *testing.T, coordinator *Coordinator) Registration {
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
