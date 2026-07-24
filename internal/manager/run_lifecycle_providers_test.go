package manager

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/portbridge"
	"github.com/vibe-agi/hideout/internal/profile"
)

type recordingLifecycleRegistration struct {
	mu               sync.Mutex
	events           []string
	root             lifecycle.ResourceRef
	session          lifecycle.ResourceRef
	finishCalls      int
	finishCleanupErr error
}

func newRecordingLifecycleRegistration() *recordingLifecycleRegistration {
	return &recordingLifecycleRegistration{
		root:    lifecycle.ResourceRef{Kind: lifecycle.KindBackendIncarnation, ID: "env-order", Generation: 1},
		session: lifecycle.ResourceRef{Kind: lifecycle.KindRunSession, ID: "ses-order", Generation: 1},
	}
}

func (r *recordingLifecycleRegistration) Incarnation() lifecycle.EnvironmentRef {
	return lifecycle.EnvironmentRef{EnvironmentID: "env-order", StartGeneration: 1, InstanceName: "hideout-order", BootID: "01234567-89ab-cdef-0123-456789abcdef"}
}
func (r *recordingLifecycleRegistration) Root() lifecycle.ResourceRef            { return r.root }
func (r *recordingLifecycleRegistration) Session() lifecycle.ResourceRef         { return r.session }
func (r *recordingLifecycleRegistration) Commit(context.Context) error           { return nil }
func (r *recordingLifecycleRegistration) BindBoot(context.Context, string) error { return nil }
func (r *recordingLifecycleRegistration) Register(_ context.Context, spec lifecycle.RegistrationSpec) (lifecycle.ResourceRef, error) {
	ref := lifecycle.ResourceRef{Kind: spec.Kind, ID: spec.ID, Generation: 1}
	r.add("register:" + string(spec.Kind) + ":" + string(spec.State))
	return ref, nil
}
func (r *recordingLifecycleRegistration) Transition(_ context.Context, ref lifecycle.ResourceRef, state lifecycle.ResourceState) error {
	r.add("transition:" + string(ref.Kind) + ":" + string(state))
	return nil
}
func (r *recordingLifecycleRegistration) Release(_ context.Context, ref lifecycle.ResourceRef, _ error) error {
	r.add("release:" + string(ref.Kind))
	return nil
}
func (r *recordingLifecycleRegistration) RecordFact(_ context.Context, spec lifecycle.FactSpec) error {
	r.add("fact:" + string(spec.Kind))
	return nil
}
func (r *recordingLifecycleRegistration) Finish(_ context.Context, cleanupErr error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishCalls++
	r.finishCleanupErr = cleanupErr
	return nil
}
func (r *recordingLifecycleRegistration) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}
func (r *recordingLifecycleRegistration) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type lifecycleBridgeBackend struct {
	*applyRunFakeBackend
	recording *recordingLifecycleRegistration
}

func (b *lifecycleBridgeBackend) StartHostToGuestBridge(ctx context.Context, _ string, _ string, _ []string, spec portbridge.Spec) (*portbridge.Bridge, error) {
	b.recording.add("authority:" + string(lifecycle.KindRunBridge))
	return portbridge.Start(ctx, spec)
}

func TestRunLifecycleEffectCallbacksRegisterBeforeSuccessAndPreserveOnlyRealEffects(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: root}, DaemonID: "daemon-effects", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: "env-effects", InstanceName: "hideout-effects", SessionID: "ses-effects",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-effects",
			BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), "01234567-89ab-cdef-0123-456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), lifecycle.StateActive); err != nil {
		t.Fatal(err)
	}
	effects := newRunLifecycleEffects(registration, "ses-effects")
	complete, err := effects.beginHandoff("editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := complete(true); err != nil {
		t.Fatal(err)
	}
	failed, err := effects.beginHostFSStage()
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(false); err != nil {
		t.Fatal(err)
	}
	staged, err := effects.beginHostFSStage()
	if err != nil {
		t.Fatal(err)
	}
	if err := staged(true); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status := coordinator.Snapshot()[0]
	if len(status.Handoffs) != 1 || len(status.Retained) != 2 || len(status.Pins) != 0 || len(status.Drains) != 0 {
		t.Fatalf("effect classification=%+v", status)
	}
}

func TestBrokerLifecycleIsPlannedBeforeListenAndRolledBackOnFailure(t *testing.T) {
	core, runSession, runNetwork := lifecycleDataPlaneFixture(t, "native")
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	runSession.Layout.BrokerSock = filepath.Join(blockedParent, "broker.sock")
	recording := newRecordingLifecycleRegistration()
	_, err := core.StartRunDataPlane(context.Background(), runSession, runNetwork, RunDataPlaneOptions{
		Backend: &applyRunFakeBackend{name: "native"}, Opener: broker.NoopOpener{}, Lifecycle: recording,
	})
	if err == nil {
		t.Fatal("invalid broker placement unexpectedly listened")
	}
	events := recording.snapshot()
	want := []string{
		"register:broker.listener:planned",
		"release:broker.listener",
	}
	for _, event := range want {
		if !containsLifecycleEvent(events, event) {
			t.Fatalf("missing %q in lifecycle order: %v", event, events)
		}
	}
	if containsLifecycleEvent(events, "transition:broker.listener:active") {
		t.Fatalf("failed listener became active: %v", events)
	}
}

func TestRunBridgeLifecycleIsPlannedBeforeProviderAuthority(t *testing.T) {
	core, runSession, runNetwork := lifecycleDataPlaneFixture(t, "lima")
	target, closeTarget := startManagerEchoServer(t)
	defer closeTarget()
	recording := newRecordingLifecycleRegistration()
	backendValue := &lifecycleBridgeBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, recording: recording,
	}
	dataPlane, err := core.StartRunDataPlane(context.Background(), runSession, runNetwork, RunDataPlaneOptions{
		Backend: backendValue, Opener: broker.NoopOpener{}, Lifecycle: recording,
		PortBridges: []RunPortBridgeRequest{{ID: "bridge-order", Owner: "test", TargetAddress: target}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := core.CloseRunDataPlane(dataPlane); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	events := recording.snapshot()
	planned := lifecycleEventIndex(events, "register:endpoint.run-bridge:planned")
	authority := lifecycleEventIndex(events, "authority:endpoint.run-bridge")
	active := lifecycleEventIndex(events, "transition:endpoint.run-bridge:active")
	if planned < 0 || authority < 0 || active < 0 || !(planned < authority && authority < active) {
		t.Fatalf("bridge ordering is not planned -> authority -> active: %v", events)
	}
}

func lifecycleDataPlaneFixture(t *testing.T, backendName string) (Core, RunSession, RunNetwork) {
	t.Helper()
	if backendName == "lima" {
		// Pin every guest helper to test-owned fakes; without them helper
		// resolution falls through to the operator's real store or PATH and
		// the fixture only works on a machine with a hideout installation.
		setFakeLinuxShim(t)
		setFakeLinuxWorkspacePortal(t)
		setFakeLinuxSessionSupervisor(t)
	}
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: backendName, Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runNetwork, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return core, runSession, runNetwork
}

func containsLifecycleEvent(events []string, target string) bool {
	return lifecycleEventIndex(events, target) >= 0
}

func lifecycleEventIndex(events []string, target string) int {
	for index, event := range events {
		if event == target {
			return index
		}
	}
	return -1
}

var _ lifecycle.Registration = (*recordingLifecycleRegistration)(nil)
var _ hostToGuestBridgeProvider = (*lifecycleBridgeBackend)(nil)
