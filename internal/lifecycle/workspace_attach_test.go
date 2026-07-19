package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestWorkspaceAttachCancelsFinalGraceWithoutStoppingSharedIncarnation(t *testing.T) {
	stops := 0
	coordinator, clock := newTestCoordinator(t, true, func(context.Context, StopRequest) (StopResult, error) {
		stops++
		return StopResult{}, errors.New("cancelled workspace grace must not stop")
	})
	first := beginWorkspaceLifecycle(t, coordinator, "ses-one", "provider-shared", "view-one", testBootID)
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status := coordinator.Snapshot()[0]
	if status.Activity != ActivityIdleGrace || status.IdleDeadline == nil {
		t.Fatalf("final workspace release did not start grace: %+v", status)
	}

	second := beginWorkspaceLifecycle(t, coordinator, "ses-two", "provider-shared", "view-two", testBootID)
	status = coordinator.Snapshot()[0]
	if status.Activity != ActivityPinned || status.IdleDeadline != nil {
		t.Fatalf("workspace attach did not synchronously cancel grace: %+v", status)
	}
	clock.advance(DefaultIdleGrace)
	if stops != 0 {
		t.Fatalf("stale final-release timer stopped a new workspace attachment %d time(s)", stops)
	}
	if second.Incarnation().StartGeneration != first.Incarnation().StartGeneration {
		t.Fatalf("grace cancellation changed live incarnation: first=%d second=%d", first.Incarnation().StartGeneration, second.Incarnation().StartGeneration)
	}
}

func TestWorkspaceAttachCannotRaceStopOrReuseStoppedIncarnation(t *testing.T) {
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
	first := beginWorkspaceLifecycle(t, coordinator, "ses-one", "provider-shared", "view-one", testBootID)
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	stopDone := make(chan struct{})
	go func() {
		clock.advance(DefaultIdleGrace)
		close(stopDone)
	}()
	request := <-stopEntered
	if request.Incarnation.StartGeneration != first.Incarnation().StartGeneration || request.Incarnation.BootID != testBootID {
		t.Fatalf("stop was not bound to released workspace incarnation: %+v", request)
	}
	racing := testAttachRequest(backend.LifecycleRunning, testBootID)
	racing.SessionID = "ses-racing"
	if _, err := coordinator.BeginAttach(context.Background(), racing); !errors.Is(err, ErrStopInFlight) {
		t.Fatalf("workspace attach entered stop-in-flight incarnation: %v", err)
	}
	close(releaseStop)
	<-stopDone

	next := beginWorkspaceLifecycle(t, coordinator, "ses-racing", "provider-next", "view-next", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if next.Incarnation().StartGeneration <= request.Incarnation.StartGeneration {
		t.Fatalf("workspace attach reused stopped incarnation: old=%d new=%d", request.Incarnation.StartGeneration, next.Incarnation().StartGeneration)
	}
}

func TestWorkspaceAttachRejectsDifferentBootWhileSiblingOwnsProvider(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first := beginWorkspaceLifecycle(t, coordinator, "ses-one", "provider-shared", "view-one", testBootID)
	request := testAttachRequest(backend.LifecycleRunning, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	request.SessionID = "ses-other-boot"
	if _, err := coordinator.BeginAttach(context.Background(), request); !errors.Is(err, ErrAttachBlocked) {
		t.Fatalf("different boot joined live workspace provider: %v", err)
	}
	status := coordinator.Snapshot()[0]
	if status.StartGeneration != first.Incarnation().StartGeneration || status.BackendState != string(backend.LifecycleRunning) {
		t.Fatalf("rejected attach mutated live workspace incarnation: %+v", status)
	}
}

func beginWorkspaceLifecycle(t *testing.T, coordinator *Coordinator, sessionID, providerID, viewID, bootID string) Registration {
	t.Helper()
	request := testAttachRequest(backend.LifecycleRunning, bootID)
	request.SessionID = sessionID
	registration, err := coordinator.BeginAttach(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindWorkspaceHostProvider, ID: providerID, OwnerKind: "manager", OwnerID: providerID,
		State: StatePlanned, Dependencies: []DependencySpec{{Ref: registration.Root(), StopMode: StopModeDrain}},
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindWorkspaceGuestView, ID: viewID, OwnerKind: "session", OwnerID: sessionID,
		State: StatePlanned, Dependencies: []DependencySpec{
			{Ref: registration.Root(), StopMode: StopModeDrain},
			{Ref: registration.Session(), StopMode: StopModeDrain},
			{Ref: provider, StopMode: StopModeDrain},
		},
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ResourceRef{registration.Session(), provider, view} {
		if err := registration.Transition(context.Background(), ref, StateStarting); err != nil {
			t.Fatal(err)
		}
		if err := registration.Transition(context.Background(), ref, StateActive); err != nil {
			t.Fatal(err)
		}
	}
	return registration
}
