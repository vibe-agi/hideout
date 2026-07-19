package workspaceattach

import (
	"context"
	"errors"
	"testing"
)

func TestAdmissionControllerEnforcesSessionProviderAndEnvironmentScopes(t *testing.T) {
	limits := SelectedLimits()
	limits.ViewsPerEnvironment = 2
	limits.HandlesPerSession = 2
	limits.HandlesPerProvider = 3
	limits.InFlightPerSession = 1
	limits.InFlightGlobal = 2
	limits.QueuedBytesPerSession = 4096
	limits.QueuedBytesGlobal = 8192
	limits.FrameBytes = 4096
	limits.TeardownInFlightPerSession = 1
	controller, err := NewAdmissionController(limits)
	if err != nil {
		t.Fatal(err)
	}
	acquire := func(session string, request AdmissionRequest) AdmissionLease {
		t.Helper()
		request.EnvironmentID = "env-a"
		request.ProviderID = "provider-a"
		request.SessionID = session
		if request.Class == "" {
			request.Class = AdmissionOrdinary
		}
		lease, err := controller.Acquire(context.Background(), request)
		if err != nil {
			t.Fatalf("acquire %s %+v: %v", session, request, err)
		}
		return lease
	}

	viewA := acquire("session-a", AdmissionRequest{Views: 1})
	viewB := acquire("session-b", AdmissionRequest{Views: 1})
	if _, err := controller.Acquire(context.Background(), AdmissionRequest{
		EnvironmentID: "env-a", ProviderID: "provider-b", SessionID: "session-c", Class: AdmissionOrdinary, Views: 1,
	}); !errors.Is(err, ErrProviderOverloaded) {
		t.Fatalf("environment view limit error=%v", err)
	}
	handlesA := acquire("session-a", AdmissionRequest{Handles: 2})
	handleB := acquire("session-b", AdmissionRequest{Handles: 1})
	if _, err := controller.Acquire(context.Background(), AdmissionRequest{
		EnvironmentID: "env-a", ProviderID: "provider-a", SessionID: "session-b", Class: AdmissionOrdinary, Handles: 1,
	}); !errors.Is(err, ErrProviderOverloaded) {
		t.Fatalf("provider handle limit error=%v", err)
	}

	for _, lease := range []AdmissionLease{viewA, viewB, handlesA, handleB} {
		lease.Release()
		lease.Release()
	}
	if got := controller.Snapshot(); !emptyAdmission(got) {
		t.Fatalf("admission usage leaked after release: %+v", got)
	}
}

func TestAdmissionControllerKeepsSiblingAndTeardownCapacityIndependent(t *testing.T) {
	limits := SelectedLimits()
	limits.InFlightPerSession = 1
	limits.InFlightGlobal = 2
	limits.TeardownInFlightPerSession = 1
	controller, err := NewAdmissionController(limits)
	if err != nil {
		t.Fatal(err)
	}
	request := func(session string, class AdmissionClass) AdmissionRequest {
		return AdmissionRequest{
			EnvironmentID: "env-a", ProviderID: "provider-a", SessionID: session,
			Class: class, InFlight: 1,
		}
	}
	noisy, err := controller.Acquire(context.Background(), request("session-noisy", AdmissionOrdinary))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Acquire(context.Background(), request("session-noisy", AdmissionOrdinary)); !errors.Is(err, ErrProviderOverloaded) {
		t.Fatalf("same-session saturation error=%v", err)
	}
	sibling, err := controller.Acquire(context.Background(), request("session-sibling", AdmissionOrdinary))
	if err != nil {
		t.Fatalf("noisy session starved sibling: %v", err)
	}
	teardown, err := controller.Acquire(context.Background(), request("session-noisy", AdmissionTeardown))
	if err != nil {
		t.Fatalf("ordinary saturation starved teardown: %v", err)
	}
	if _, err := controller.Acquire(context.Background(), request("session-noisy", AdmissionTeardown)); !errors.Is(err, ErrProviderOverloaded) {
		t.Fatalf("teardown bound error=%v", err)
	}
	teardown.Release()
	sibling.Release()
	noisy.Release()
}
