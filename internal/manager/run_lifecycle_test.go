package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

type lifecycleApplyBackend struct {
	*applyRunFakeBackend
	journal        lifecycle.JournalStore
	bootID         string
	planned        bool
	networkPlanned bool
	beforeReady    func(*backend.Session) error
	targetRuns     int
}

func (b *lifecycleApplyBackend) ObserveLifecycle(_ context.Context, instanceName string) backend.LifecycleObservation {
	return backend.LifecycleObservation{
		State: backend.LifecycleAbsent, InstanceName: instanceName, ObservedAt: time.Now().UTC(),
	}
}

func (b *lifecycleApplyBackend) Prepare(ctx context.Context, spec backend.RunSpec) (*backend.Session, error) {
	journal, err := b.journal.Load(spec.Machine.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("planned lifecycle journal missing before backend prepare: %w", err)
	}
	required := map[lifecycle.ResourceKind]lifecycle.ResourceState{
		lifecycle.KindRunSession:      lifecycle.StatePlanned,
		lifecycle.KindGuestSupervisor: lifecycle.StatePlanned,
		lifecycle.KindGuestTarget:     lifecycle.StatePlanned,
		lifecycle.KindBrokerListener:  lifecycle.StatePlanned,
	}
	for _, resource := range journal.Resources {
		if resource.Ref.ID != spec.SessionID {
			continue
		}
		if want, ok := required[resource.Ref.Kind]; ok && resource.State == want {
			delete(required, resource.Ref.Kind)
		}
	}
	if len(required) != 0 {
		return nil, fmt.Errorf("provider lifecycle dependencies missing before backend prepare: %v", required)
	}
	b.planned = true
	return b.applyRunFakeBackend.Prepare(ctx, spec)
}

func (b *lifecycleApplyBackend) Activate(_ context.Context, session *backend.Session, _ []string) error {
	session.ExpectedBootID = b.bootID
	return nil
}

func (b *lifecycleApplyBackend) RunWithStreams(ctx context.Context, session *backend.Session, command []string, env []string, streams backend.RunStreams) error {
	if expectation := session.ProjectionReadiness; expectation != nil {
		session.ProjectionReadinessObservation = &backend.ProjectionReadinessObservation{
			Status: backend.ProjectionReadinessReady, CatalogDigest: expectation.Manifest.CatalogDigest,
			ExpectedEntries: len(expectation.Manifest.Entries), ObservedEntries: len(expectation.Manifest.Entries),
			TargetProjected: expectation.TargetProjected,
		}
	}
	proof, err := backend.ReadyProofForSession(session, backend.SessionReadyAuthenticatedSupervisor)
	if err != nil {
		return err
	}
	if b.beforeReady != nil {
		if err := b.beforeReady(session); err != nil {
			return err
		}
	}
	if streams.Ready == nil {
		return errors.New("test stream is missing ready barrier")
	}
	if err := streams.Ready(proof); err != nil {
		return err
	}
	b.targetRuns++
	return b.applyRunFakeBackend.Run(ctx, session, command, env)
}

func (b *lifecycleApplyBackend) StartEnvironmentNetwork(_ context.Context, session *backend.Session, _ string, _ string, _ []string) error {
	journal, err := b.journal.Load(session.EnvironmentID)
	if err != nil {
		return err
	}
	for _, resource := range journal.Resources {
		if resource.Ref.Kind == lifecycle.KindNetworkService && resource.Ref.ID == session.EnvironmentID && resource.State == lifecycle.StatePlanned {
			b.networkPlanned = true
			return nil
		}
	}
	return errors.New("network service dependency was not planned before provider start")
}

func (*lifecycleApplyBackend) VerifyEnvironmentNetwork(context.Context, *backend.Session, string, []string) error {
	return nil
}

func (*lifecycleApplyBackend) VerifyDirectEnvironmentNetwork(context.Context, *backend.Session, string, []string) error {
	return nil
}

func (*lifecycleApplyBackend) StopEnvironmentNetwork(context.Context, *backend.Session, string, string, []string) error {
	return nil
}

type establishmentOrderingRegistrar struct {
	inner  *lifecycle.Coordinator
	store  environment.Store
	events []string
}

type rejectingEstablishmentRegistrar struct {
	err error
}

func (r rejectingEstablishmentRegistrar) ReserveAttach(context.Context, lifecycle.EstablishmentRequest) (lifecycle.EstablishmentReservation, error) {
	return nil, r.err
}

func (rejectingEstablishmentRegistrar) BeginAttach(context.Context, lifecycle.AttachRequest) (lifecycle.Registration, error) {
	return nil, errors.New("legacy BeginAttach invoked")
}

func (r *establishmentOrderingRegistrar) ReserveAttach(ctx context.Context, request lifecycle.EstablishmentRequest) (lifecycle.EstablishmentReservation, error) {
	if _, err := os.Stat(r.store.RuntimeSessionDir(request.EnvironmentID, request.SessionID)); !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("session runtime existed before reservation: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	lock, err := r.store.LockContext(lockCtx, request.EnvironmentID)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("transition lock was held before reservation: %w", err)
	}
	if err := lock.Unlock(); err != nil {
		return nil, err
	}
	r.events = append(r.events, "reserve")
	reservation, err := r.inner.ReserveAttach(ctx, request)
	if err != nil {
		return nil, err
	}
	return &establishmentOrderingReservation{inner: reservation, registrar: r, request: request}, nil
}

func (*establishmentOrderingRegistrar) BeginAttach(context.Context, lifecycle.AttachRequest) (lifecycle.Registration, error) {
	return nil, errors.New("legacy BeginAttach invoked")
}

type establishmentOrderingReservation struct {
	inner     lifecycle.EstablishmentReservation
	registrar *establishmentOrderingRegistrar
	request   lifecycle.EstablishmentRequest
}

func (r *establishmentOrderingReservation) Prepare(ctx context.Context, request lifecycle.AttachRequest) (lifecycle.EnvironmentRef, error) {
	if _, err := os.Stat(r.registrar.store.RuntimeSessionDir(request.EnvironmentID, request.SessionID)); !errors.Is(err, os.ErrNotExist) {
		return lifecycle.EnvironmentRef{}, fmt.Errorf("session runtime existed before establishment prepare: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	lock, err := r.registrar.store.LockContext(lockCtx, request.EnvironmentID)
	cancel()
	if err == nil {
		_ = lock.Unlock()
		return lifecycle.EnvironmentRef{}, errors.New("transition lock was not held during establishment prepare")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return lifecycle.EnvironmentRef{}, fmt.Errorf("probe transition lock during establishment prepare: %w", err)
	}
	r.registrar.events = append(r.registrar.events, "prepare")
	return r.inner.Prepare(ctx, request)
}

func (r *establishmentOrderingReservation) Promote(ctx context.Context) (lifecycle.Registration, error) {
	if _, err := os.Stat(r.registrar.store.RuntimeSessionDir(r.request.EnvironmentID, r.request.SessionID)); err != nil {
		return nil, fmt.Errorf("session runtime missing before establishment promotion: %w", err)
	}
	owners, err := runsession.ListOwners(r.registrar.store.OwnerRoot(r.request.EnvironmentID))
	if err != nil {
		return nil, err
	}
	ownerLive := false
	for _, owner := range owners {
		if owner.SessionID == r.request.SessionID && owner.Status == runsession.OwnerLive {
			ownerLive = true
			break
		}
	}
	if !ownerLive {
		return nil, errors.New("durable live session owner missing before establishment promotion")
	}
	r.registrar.events = append(r.registrar.events, "promote")
	return r.inner.Promote(ctx)
}

func (r *establishmentOrderingReservation) Abort(ctx context.Context, cause error) error {
	r.registrar.events = append(r.registrar.events, "abort")
	return r.inner.Abort(ctx, cause)
}

func TestApplyRunEstablishmentOrdersReservationLockRuntimeOwnerAndPromotion(t *testing.T) {
	setFakeLinuxShim(t)
	setFakeLinuxWorkspacePortal(t)
	setFakeLinuxSessionSupervisor(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("establishment-order")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "establishment-order", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-establishment-order", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	registrar := &establishmentOrderingRegistrar{inner: coordinator, store: environment.Store{Root: root}}
	fake := &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, journal: journalStore,
		bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	fake.beforeReady = func(*backend.Session) error {
		registrar.events = append(registrar.events, "ready")
		return nil
	}
	_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Lifecycle: registrar,
		PrepareWorkspaceAttachment:  func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ActivateWorkspaceAttachment: func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ReleaseWorkspaceAttachment:  func(context.Context) error { return nil },
		Streams:                     &backend.RunStreams{Ready: func(backend.SessionReadyProof) error { return nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"reserve", "prepare", "promote", "ready"}; !slices.Equal(registrar.events, want) {
		t.Fatalf("establishment order=%v want=%v", registrar.events, want)
	}
}

func TestApplyRunReservationFailurePublishesNoRuntimeOrTarget(t *testing.T) {
	setFakeLinuxShim(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("establishment-rejected")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "establishment-rejected", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("reservation rejected")
	fake := &lifecycleApplyBackend{applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true},
		Lifecycle: rejectingEstablishmentRegistrar{err: sentinel},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ApplyRun error=%v", err)
	}
	if fake.targetRuns != 0 {
		t.Fatalf("reservation failure launched %d targets", fake.targetRuns)
	}
	if result.SessionID == "" {
		t.Fatal("reservation attempt did not allocate an opaque session identity")
	}
	if _, statErr := os.Stat(filepath.Join(root, "sessions", result.SessionID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("reservation failure published global session runtime: %v", statErr)
	}
}

func TestApplyRunMapsOwnerBlockedReservationToExistingRecoveryCode(t *testing.T) {
	setFakeLinuxShim(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("owner-recovery")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "owner-recovery", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleApplyBackend{applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true},
		Lifecycle: rejectingEstablishmentRegistrar{err: &lifecycle.AttachBlockedError{ReasonCode: "owner-requires-explicit-recovery"}},
	})
	if EnvironmentRecoveryCode(err) != "session.cleanup.failed" || !strings.Contains(err.Error(), "explicit recovery") {
		t.Fatalf("owner recovery error=%T %v", err, err)
	}
	if result.SessionID == "" || fake.targetRuns != 0 {
		t.Fatalf("owner recovery result=%+v targetRuns=%d", result, fake.targetRuns)
	}
	if _, statErr := os.Stat(filepath.Join(root, "sessions", result.SessionID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("owner recovery rejection published global runtime: %v", statErr)
	}
}

func TestApplyRunRegistersLifecycleBeforeBackendAuthority(t *testing.T) {
	setFakeLinuxShim(t)
	setFakeLinuxWorkspacePortal(t)
	setFakeLinuxSessionSupervisor(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	profileValue := profile.Default("lifecycle")
	if err := store.Save(profileValue); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "lifecycle", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-manager-test", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"},
		journal:             journalStore, bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Lifecycle: coordinator,
		PrepareWorkspaceAttachment:  func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ActivateWorkspaceAttachment: func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ReleaseWorkspaceAttachment:  func(context.Context) error { return nil },
		Streams:                     &backend.RunStreams{Ready: func(backend.SessionReadyProof) error { return nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fake.planned {
		t.Fatal("backend authority preceded durable lifecycle planning")
	}
	journal, err := journalStore.Load(result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for (journal.IdleDeadline == nil || len(journal.Resources) != 1) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		journal, err = journalStore.Load(result.EnvironmentID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if journal.IdleDeadline == nil || journal.Incarnation == nil || journal.Incarnation.BootID != fake.bootID {
		t.Fatalf("final lifecycle state mismatch: %+v", journal)
	}
	for _, resource := range journal.Resources {
		if resource.Ref.Kind != lifecycle.KindBackendIncarnation {
			t.Fatalf("proved cleanup left a session resource: %+v", resource)
		}
	}
}

func TestApplyRunSharedReadyBarrierActivatesBeforeCommitAndPreventsTargetOnRejection(t *testing.T) {
	setFakeLinuxShim(t)
	setFakeLinuxWorkspacePortal(t)
	setFakeLinuxSessionSupervisor(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("ready-barrier")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "ready-barrier", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-ready-barrier-test", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	stateFor := func(kind lifecycle.ResourceKind) (lifecycle.ResourceState, error) {
		for _, status := range coordinator.Snapshot() {
			for _, resources := range [][]lifecycle.ResourceSummary{status.Pins, status.Drains, status.Orphans} {
				for _, resource := range resources {
					if resource.Kind == kind {
						return resource.State, nil
					}
				}
			}
		}
		return "", fmt.Errorf("resource %s is missing from lifecycle snapshot", kind)
	}
	beforeReadyPlanned := false
	downstreamSawActive := false
	fake := &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, journal: journalStore,
		bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	fake.beforeReady = func(session *backend.Session) error {
		for _, kind := range []lifecycle.ResourceKind{lifecycle.KindGuestSupervisor, lifecycle.KindGuestTarget} {
			state, stateErr := stateFor(kind)
			if stateErr != nil {
				return stateErr
			}
			if state != lifecycle.StatePlanned {
				return fmt.Errorf("%s state before authenticated ready=%s", kind, state)
			}
		}
		beforeReadyPlanned = true
		return nil
	}
	rejected := errors.New("daemon refused ready publication")
	_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Lifecycle: coordinator,
		PrepareWorkspaceAttachment:  func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ActivateWorkspaceAttachment: func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ReleaseWorkspaceAttachment:  func(context.Context) error { return nil },
		Streams: &backend.RunStreams{Ready: func(proof backend.SessionReadyProof) error {
			for _, kind := range []lifecycle.ResourceKind{lifecycle.KindGuestSupervisor, lifecycle.KindGuestTarget} {
				state, stateErr := stateFor(kind)
				if stateErr != nil {
					return stateErr
				}
				if state != lifecycle.StateActive {
					return fmt.Errorf("%s state at daemon commit=%s", kind, state)
				}
			}
			downstreamSawActive = true
			return rejected
		}},
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("ApplyRun error=%v", err)
	}
	if !beforeReadyPlanned || !downstreamSawActive {
		t.Fatalf("ready ordering planned=%v active=%v", beforeReadyPlanned, downstreamSawActive)
	}
	if fake.targetRuns != 0 {
		t.Fatalf("target ran %d time(s) after ready publication was rejected", fake.targetRuns)
	}
}

func TestApplyRunPlansEnvironmentNetworkBeforeProviderStart(t *testing.T) {
	setFakeLinuxShim(t)
	setFakeLinuxWorkspacePortal(t)
	setFakeLinuxSessionSupervisor(t)
	dnsStub := filepath.Join(t.TempDir(), "hideout-dns-stub-linux")
	if err := os.WriteFile(dnsStub, []byte("dns-stub"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_DNS_STUB_PATH", dnsStub)
	tun2socks := filepath.Join(t.TempDir(), "tun2socks-linux")
	if err := os.WriteFile(tun2socks, []byte("tun2socks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteTun2SocksManifest(tun2socks, runtime.GOARCH, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_TUN2SOCKS_PATH", tun2socks)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	profileValue := profile.Default("lifecycle-network")
	profileValue.Network.Mode = network.ModeTun2Socks
	profileValue.Network.ProxySecretRef = "shared-proxy"
	profileValue.Network.MediatedResolver = "1.1.1.1"
	if err := store.Save(profileValue); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "lifecycle-network", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-network-test", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, journal: journalStore,
		bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Lifecycle: coordinator,
		PrepareWorkspaceAttachment:  func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ActivateWorkspaceAttachment: func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
		ReleaseWorkspaceAttachment:  func(context.Context) error { return nil },
		Streams:                     &backend.RunStreams{Ready: func(backend.SessionReadyProof) error { return nil }},
		Network: RunNetworkOptions{
			Resolver: network.EnvSecretResolver{Env: []string{network.SecretEnvName("shared-proxy") + "=socks5://user:pass@127.0.0.1:1080"}},
			Verified: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fake.networkPlanned {
		t.Fatal("network provider started before its lifecycle dependency was planned")
	}
}
