package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestLifecycleConflictReportsAttachOwnerAndPreservesReservation(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	reservation, err := coordinator.ReserveAttach(
		context.Background(),
		EstablishmentRequest{
			EnvironmentID: "env-test",
			SessionID:     "ses-attach-owner",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	mutated := false
	err = coordinator.RunDestructiveMutationWithOwner(
		context.Background(),
		"env-test",
		testMutationOwner(
			MutationOwnerConfiguration,
			"op_config_owner",
			MutationPhaseApplying,
		),
		func(context.Context) error {
			mutated = true
			return nil
		},
	)
	assertLifecycleConflict(
		t,
		err,
		EnvironmentMutationKey("env-test"),
		MutationOwnerAttach,
		"ses-attach-owner",
		MutationPhaseEstablishing,
		ErrMutationBlockedByActivity,
	)
	if mutated {
		t.Fatal("configuration callback crossed attach establishment")
	}
	request := testAttachRequest(backend.LifecycleRunning, testBootID)
	request.SessionID = "ses-attach-owner"
	if _, err := reservation.Prepare(context.Background(), request); err != nil {
		t.Fatalf("conflict damaged attach reservation: %v", err)
	}
	registration, err := reservation.Promote(context.Background())
	if err != nil {
		t.Fatalf("conflict damaged attach promotion: %v", err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleConflictReportsReconcileOwnerAndDoesNotInvokeStop(t *testing.T) {
	stopCalls := 0
	coordinator, _ := newTestCoordinator(
		t,
		true,
		func(context.Context, StopRequest) (StopResult, error) {
			stopCalls++
			return StopResult{}, errors.New("stop must not run")
		},
	)
	started, err := coordinator.BeginReconciliation(
		context.Background(),
		"env-test",
	)
	if err != nil || !started {
		t.Fatalf("begin reconciliation started=%t err=%v", started, err)
	}
	_, err = coordinator.StopExplicit(context.Background(), "env-test")
	assertLifecycleConflict(
		t,
		err,
		EnvironmentMutationKey("env-test"),
		MutationOwnerReconcile,
		"daemon-test",
		MutationPhaseReconciling,
		ErrReconciliationInFlight,
	)
	if stopCalls != 0 {
		t.Fatalf("blocked stop invoked provider %d times", stopCalls)
	}
	if err := coordinator.BlockReconciliation(
		"env-test",
		"inventory-unavailable",
	); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleConfigurationAndAttachShareProfileMutationKey(t *testing.T) {
	t.Run("configuration owns key", func(t *testing.T) {
		coordinator, _ := newTestCoordinator(t, false, nil)
		key := ProfileMutationKey("default", "profile.network")
		lease, err := coordinator.AcquireMutation(
			context.Background(),
			MutationRequest{
				Keys: []string{key},
				Owner: testMutationOwner(
					MutationOwnerConfiguration,
					"op_profile_config",
					MutationPhaseApplying,
				),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release()
		_, err = coordinator.ReserveAttach(
			context.Background(),
			EstablishmentRequest{
				EnvironmentID: "env-test",
				SessionID:     "ses-config-blocked",
				MutationKeys:  []string{key},
			},
		)
		assertLifecycleConflict(
			t,
			err,
			key,
			MutationOwnerConfiguration,
			"op_profile_config",
			MutationPhaseApplying,
			ErrStopInFlight,
		)
		coordinator.mu.Lock()
		state := coordinator.environments["env-test"]
		reservations := 0
		if state != nil {
			reservations = len(state.establishing)
		}
		coordinator.mu.Unlock()
		if reservations != 0 {
			t.Fatalf("blocked attach published %d reservations", reservations)
		}
	})

	t.Run("attach owns key", func(t *testing.T) {
		coordinator, _ := newTestCoordinator(t, false, nil)
		key := ProfileMutationKey("default", "profile.environment")
		reservation, err := coordinator.ReserveAttach(
			context.Background(),
			EstablishmentRequest{
				EnvironmentID: "env-test",
				SessionID:     "ses-profile-owner",
				MutationKeys:  []string{key},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = coordinator.AcquireMutation(
			context.Background(),
			MutationRequest{
				Keys: []string{key},
				Owner: testMutationOwner(
					MutationOwnerConfiguration,
					"op_profile_waiter",
					MutationPhaseApplying,
				),
			},
		)
		assertLifecycleConflict(
			t,
			err,
			key,
			MutationOwnerAttach,
			"ses-profile-owner",
			MutationPhaseEstablishing,
			ErrMutationBlockedByActivity,
		)
		if err := reservation.Abort(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLifecycleConflictReportsStopOwnerAndPreservesStop(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	stopCalls := 0
	coordinator, _ := newTestCoordinator(
		t,
		true,
		func(context.Context, StopRequest) (StopResult, error) {
			stopCalls++
			close(entered)
			<-release
			return StopResult{
				Observation: backend.LifecycleObservation{
					State:        backend.LifecycleStopped,
					InstanceName: "hideout-test",
					ObservedAt:   time.Now().UTC(),
				},
			}, nil
		},
	)
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	stopDone := make(chan error, 1)
	go func() {
		_, err := coordinator.StopExplicit(context.Background(), "env-test")
		stopDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not enter provider")
	}
	cleanupCalls := 0
	err := coordinator.RunDestructiveMutationWithOwner(
		context.Background(),
		"env-test",
		testMutationOwner(
			MutationOwnerCleanup,
			"op_cleanup_waiter",
			MutationPhaseApplying,
		),
		func(context.Context) error {
			cleanupCalls++
			return nil
		},
	)
	var conflict *MutationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("cleanup conflict=%v", err)
	}
	if conflict.Key != EnvironmentMutationKey("env-test") ||
		conflict.Owner.Kind != MutationOwnerStop ||
		conflict.Owner.Phase != MutationPhaseStopping ||
		conflict.Owner.ID == "" ||
		conflict.Owner.Recovery == "" ||
		!errors.Is(err, ErrStopInFlight) {
		t.Fatalf("stop blocker=%+v err=%v", conflict, err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("blocked cleanup ran %d times", cleanupCalls)
	}
	close(release)
	if err := <-stopDone; err != nil {
		t.Fatalf("conflict damaged owning stop: %v", err)
	}
	if stopCalls != 1 {
		t.Fatalf("stop provider calls=%d", stopCalls)
	}
}

func TestLifecycleConflictReportsCleanupOwnerAndDoesNotDamageWaiters(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	cleanupDone := make(chan error, 1)
	go func() {
		cleanupDone <- coordinator.RunDestructiveMutationWithOwner(
			context.Background(),
			"env-test",
			testMutationOwner(
				MutationOwnerCleanup,
				"op_cleanup_owner",
				MutationPhaseApplying,
			),
			func(context.Context) error {
				close(entered)
				<-release
				return nil
			},
		)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not acquire mutation key")
	}

	_, attachErr := coordinator.ReserveAttach(
		context.Background(),
		EstablishmentRequest{
			EnvironmentID: "env-test",
			SessionID:     "ses-cleanup-blocked",
		},
	)
	assertLifecycleConflict(
		t,
		attachErr,
		EnvironmentMutationKey("env-test"),
		MutationOwnerCleanup,
		"op_cleanup_owner",
		MutationPhaseApplying,
		ErrAttachBlocked,
	)
	reconcileErr := coordinator.Reconcile(
		context.Background(),
		ReconcileInput{
			EnvironmentID: "env-test",
			InstanceName:  "hideout-test",
			Observation: backend.LifecycleObservation{
				State:        backend.LifecycleAbsent,
				InstanceName: "hideout-test",
				ObservedAt:   time.Now().UTC(),
			},
		},
	)
	assertLifecycleConflict(
		t,
		reconcileErr,
		EnvironmentMutationKey("env-test"),
		MutationOwnerCleanup,
		"op_cleanup_owner",
		MutationPhaseApplying,
		ErrMutationBlockedByActivity,
	)
	close(release)
	if err := <-cleanupDone; err != nil {
		t.Fatalf("waiters damaged owning cleanup: %v", err)
	}
}

func assertLifecycleConflict(
	t *testing.T,
	err error,
	key, ownerKind, ownerID, ownerPhase string,
	cause error,
) {
	t.Helper()
	var conflict *MutationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error is not a typed lifecycle conflict: %v", err)
	}
	if conflict.Key != key ||
		conflict.Owner.Kind != ownerKind ||
		conflict.Owner.ID != ownerID ||
		conflict.Owner.Phase != ownerPhase ||
		conflict.Owner.Recovery == "" ||
		conflict.Requested.Kind == "" ||
		conflict.Requested.ID == "" ||
		!errors.Is(err, cause) {
		t.Fatalf("conflict=%+v cause=%v", conflict, err)
	}
}

func testMutationOwner(kind, id, phase string) MutationOwner {
	return MutationOwner{
		Kind: kind, ID: id, Phase: phase,
		Recovery:  "wait for the owning operation to finish, then retry",
		StartedAt: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC),
	}
}
