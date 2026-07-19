package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

type observedEnvironmentBackend struct {
	observations []backend.LifecycleObservation
	stopErr      error
	stopCalls    int
}

func (b *observedEnvironmentBackend) ObserveLifecycle(_ context.Context, instanceName string) backend.LifecycleObservation {
	if len(b.observations) == 0 {
		return backend.LifecycleObservation{State: backend.LifecycleUnknown, InstanceName: instanceName, ObservedAt: time.Now().UTC(), ReasonCode: "fixture-exhausted"}
	}
	observation := b.observations[0]
	if len(b.observations) > 1 {
		b.observations = b.observations[1:]
	}
	return observation
}

func (b *observedEnvironmentBackend) StopInstance(context.Context, string) error {
	b.stopCalls++
	return b.stopErr
}

func (*observedEnvironmentBackend) Cleanup(context.Context, *backend.Session) error { return nil }

func TestStopEnvironmentIncarnationRequiresIndependentTerminalObservation(t *testing.T) {
	core, record := observedStopFixture(t)
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	provider := &observedEnvironmentBackend{observations: []backend.LifecycleObservation{
		lifecycleObservation(backend.LifecycleRunning, record.InstanceName, bootID, ""),
		lifecycleObservation(backend.LifecycleStopped, record.InstanceName, "", ""),
	}}
	result, err := core.StopEnvironmentIncarnation(context.Background(), lifecycle.StopRequest{
		AttemptID: "stop-test", Mode: "automatic",
		Incarnation: lifecycle.EnvironmentRef{EnvironmentID: record.ID, StartGeneration: 1, InstanceName: record.InstanceName, BootID: bootID},
	}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider.stopCalls != 1 || result.Observation.State != backend.LifecycleStopped {
		t.Fatalf("stop transaction mismatch: calls=%d result=%+v", provider.stopCalls, result)
	}
	loaded, err := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "stopped" {
		t.Fatalf("terminal observation was not committed: %+v", loaded)
	}
}

func TestStopEnvironmentIncarnationDoesNotTrustCommandSuccess(t *testing.T) {
	core, record := observedStopFixture(t)
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	provider := &observedEnvironmentBackend{observations: []backend.LifecycleObservation{
		lifecycleObservation(backend.LifecycleRunning, record.InstanceName, bootID, ""),
		lifecycleObservation(backend.LifecycleUnknown, record.InstanceName, "", "inventory-timeout"),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result, err := core.StopEnvironmentIncarnation(ctx, lifecycle.StopRequest{
		AttemptID: "stop-test", Mode: "automatic",
		Incarnation: lifecycle.EnvironmentRef{EnvironmentID: record.ID, StartGeneration: 1, InstanceName: record.InstanceName, BootID: bootID},
	}, provider)
	if err == nil || result.Observation.State != backend.LifecycleUnknown {
		t.Fatalf("unknown observation became success: result=%+v err=%v", result, err)
	}
	loaded, loadErr := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Status != "running" {
		t.Fatalf("command success falsely wrote stopped: %+v", loaded)
	}
}

func TestStopEnvironmentIncarnationRejectsMalformedOrCrossInstanceObservation(t *testing.T) {
	tests := map[string]backend.LifecycleObservation{
		"malformed terminal": {
			State: backend.LifecycleStopped, InstanceName: "hideout-lifecycle-test",
			BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
		},
		"cross instance": lifecycleObservation(backend.LifecycleStopped, "hideout-other", "", ""),
	}
	for name, terminal := range tests {
		t.Run(name, func(t *testing.T) {
			core, record := observedStopFixture(t)
			bootID := "01234567-89ab-cdef-0123-456789abcdef"
			provider := &observedEnvironmentBackend{observations: []backend.LifecycleObservation{
				lifecycleObservation(backend.LifecycleRunning, record.InstanceName, bootID, ""), terminal,
			}}
			result, err := core.StopEnvironmentIncarnation(context.Background(), lifecycle.StopRequest{
				AttemptID: "stop-test", Mode: "automatic",
				Incarnation: lifecycle.EnvironmentRef{EnvironmentID: record.ID, StartGeneration: 1, InstanceName: record.InstanceName, BootID: bootID},
			}, provider)
			if err == nil || result.Observation.State != backend.LifecycleUnknown || result.ReasonCode != "backend-observation-invalid" {
				t.Fatalf("invalid observation became stop proof: result=%+v err=%v", result, err)
			}
			loaded, loadErr := (environment.Store{Root: core.Store.Root}).Load(record.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if loaded.Status != "running" {
				t.Fatalf("invalid observation wrote stopped: %+v", loaded)
			}
		})
	}
}

func TestStopEnvironmentIncarnationRefusesAnyOwnerRecord(t *testing.T) {
	core, record := observedStopFixture(t)
	store := environment.Store{Root: core.Store.Root}
	owner, err := session.AcquireOwner(store.OwnerRoot(record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_20260716T120000Z_0123456789abcdef", EnvironmentID: record.ID,
		Profile: "default", Backend: "lima", WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(), State: session.OwnerStateRunning,
		TerminalMode: session.TerminalNone, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CommandClass: "bash",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	provider := &observedEnvironmentBackend{}
	_, err = core.StopEnvironmentIncarnation(context.Background(), lifecycle.StopRequest{
		AttemptID: "stop-test", Mode: "automatic",
		Incarnation: lifecycle.EnvironmentRef{EnvironmentID: record.ID, StartGeneration: 1, InstanceName: record.InstanceName, BootID: "01234567-89ab-cdef-0123-456789abcdef"},
	}, provider)
	if err == nil || provider.stopCalls != 0 {
		t.Fatalf("owner record did not block stop: calls=%d err=%v", provider.stopCalls, err)
	}
}

func observedStopFixture(t *testing.T) (Core, environment.Record) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	environmentStore := environment.Store{Root: store.Root}
	record, err := environmentStore.Create(environment.Spec{
		Name: "lifecycle", ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "lima",
		Mode: environment.ModeWorkspaceBound, MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(), BoundWorkspace: t.TempDir(), BoundGuestRoot: "/workspace", InstanceName: "hideout-lifecycle-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = "running"
	if err := environmentStore.Save(record); err != nil {
		t.Fatal(err)
	}
	return New(store), record
}

func lifecycleObservation(state backend.LifecycleState, instanceName, bootID, reason string) backend.LifecycleObservation {
	return backend.LifecycleObservation{State: state, InstanceName: instanceName, BootID: bootID, ObservedAt: time.Now().UTC(), ReasonCode: reason}
}

var _ EnvironmentLifecycleBackend = (*observedEnvironmentBackend)(nil)
