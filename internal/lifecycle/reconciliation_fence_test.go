package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestReconciliationFenceBlocksAttachStopAndMutationUntilCurrentProof(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	state.journal.Reconciliation.DaemonInstanceID = "daemon-prior"
	state.blocked = true
	if err := coordinator.persistNowLocked(state); err != nil {
		coordinator.mu.Unlock()
		t.Fatal(err)
	}
	coordinator.mu.Unlock()
	started, err := coordinator.BeginReconciliation(context.Background(), "env-test")
	if err != nil || !started {
		t.Fatalf("begin reconciliation started=%t err=%v", started, err)
	}
	if second, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err != nil || second {
		t.Fatalf("concurrent reconciliation did not coalesce: started=%t err=%v", second, err)
	}

	attachDone := make(chan error, 1)
	go func() {
		request := testAttachRequest(backend.LifecycleRunning, testBootID)
		request.SessionID = "ses-two"
		reg, attachErr := coordinator.BeginAttach(context.Background(), request)
		if attachErr == nil {
			attachErr = reg.Finish(context.Background(), nil)
		}
		attachDone <- attachErr
	}()
	select {
	case err := <-attachDone:
		t.Fatalf("attach escaped in-flight reconciliation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := coordinator.StopExplicit(context.Background(), "env-test"); !errors.Is(err, ErrReconciliationInFlight) {
		t.Fatalf("explicit stop entered reconciliation: %v", err)
	}
	if err := coordinator.RunDestructiveMutation(context.Background(), "env-test", func(context.Context) error { return nil }); !errors.Is(err, ErrReconciliationInFlight) {
		t.Fatalf("destructive mutation entered reconciliation: %v", err)
	}

	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test",
			BootID: testBootID, ObservedAt: time.Date(2026, 7, 16, 5, 1, 0, 0, time.UTC),
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-attachDone:
		if err != nil {
			t.Fatalf("attach did not resume after reconciliation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("attach remained blocked after reconciliation")
	}
}

func TestTransientUnknownReconciliationRetriesInSameDaemonEpoch(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	coordinator.mu.Lock()
	state := coordinator.environments["env-test"]
	state.journal.Reconciliation.DaemonInstanceID = "daemon-prior"
	state.blocked = true
	if err := coordinator.persistNowLocked(state); err != nil {
		coordinator.mu.Unlock()
		t.Fatal(err)
	}
	coordinator.mu.Unlock()
	if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err != nil || !started {
		t.Fatalf("begin first reconciliation started=%t err=%v", started, err)
	}
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: "hideout-test",
			ObservedAt: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC), ReasonCode: "inventory-unavailable",
		},
	}); err != nil {
		t.Fatal(err)
	}
	first := coordinator.Snapshot()
	if len(first) != 1 || first[0].Reconciliation != "blocked" || first[0].ReasonCode != "inventory-unavailable" || first[0].IdleDeadline != nil {
		t.Fatalf("unknown reconciliation was not blocked: %+v", first)
	}

	if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err != nil || !started {
		t.Fatalf("begin retry started=%t err=%v", started, err)
	}
	if err := coordinator.Reconcile(context.Background(), ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test",
			BootID: testBootID, ObservedAt: time.Date(2026, 7, 16, 5, 0, 1, 0, time.UTC),
		},
	}); err != nil {
		t.Fatal(err)
	}
	second := coordinator.Snapshot()
	if len(second) != 1 || second[0].Reconciliation != "complete" || second[0].ReasonCode != "" || second[0].IdleDeadline == nil {
		t.Fatalf("same-epoch retry did not establish one fresh grace: %+v", second)
	}
	deadline := *second[0].IdleDeadline
	if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err != nil || started {
		t.Fatalf("complete same-epoch reconciliation restarted: started=%t err=%v", started, err)
	}
	after := coordinator.Snapshot()
	if len(after) != 1 || after[0].IdleDeadline == nil || !after[0].IdleDeadline.Equal(deadline) {
		t.Fatalf("complete retry changed fresh grace: before=%s after=%+v", deadline, after)
	}
}
