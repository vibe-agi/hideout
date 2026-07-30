package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestDaemonLifecycleKeepsSiblingSessionPinnedUntilFinalRelease(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName, BootID: bootID,
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Stop(context.Background()); err != nil {
			t.Errorf("stop daemon: %v", err)
		}
	}()

	attach := func(sessionID string) lifecycle.Registration {
		t.Helper()
		registration, attachErr := d.lifecycle.BeginAttach(context.Background(), lifecycle.AttachRequest{
			EnvironmentID: record.ID, InstanceName: record.InstanceName, SessionID: sessionID,
			Observation: backend.LifecycleObservation{
				State: backend.LifecycleRunning, InstanceName: record.InstanceName,
				BootID: bootID, ObservedAt: time.Now().UTC(),
			},
		})
		if attachErr != nil {
			t.Fatal(attachErr)
		}
		if bindErr := registration.BindBoot(context.Background(), bootID); bindErr != nil {
			t.Fatal(bindErr)
		}
		if transitionErr := registration.Transition(context.Background(), registration.Session(), lifecycle.StateActive); transitionErr != nil {
			t.Fatal(transitionErr)
		}
		return registration
	}

	first := attach("ses-first")
	second := attach("ses-second")
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status := d.Status().Lifecycle
	if len(status) != 1 || status[0].Activity != lifecycle.ActivityPinned || status[0].IdleDeadline != nil {
		t.Fatalf("first sibling release made environment idle: %+v", status)
	}
	if err := second.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status = d.Status().Lifecycle
	if len(status) != 1 || status[0].Activity != lifecycle.ActivityIdleGrace || status[0].IdleDeadline == nil {
		t.Fatalf("final sibling release did not start grace: %+v", status)
	}
}
