package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

type cancellingEstablishmentRegistrar struct {
	inner *lifecycle.Coordinator
	stage string
}

type signallingEstablishmentRegistrar struct {
	inner   lifecycle.Registrar
	entered chan struct{}
	once    sync.Once
}

func (r *signallingEstablishmentRegistrar) ReserveAttach(ctx context.Context, request lifecycle.EstablishmentRequest) (lifecycle.EstablishmentReservation, error) {
	r.once.Do(func() { close(r.entered) })
	return r.inner.ReserveAttach(ctx, request)
}

func (r *signallingEstablishmentRegistrar) BeginAttach(ctx context.Context, request lifecycle.AttachRequest) (lifecycle.Registration, error) {
	return r.inner.BeginAttach(ctx, request)
}

func (r *cancellingEstablishmentRegistrar) ReserveAttach(ctx context.Context, request lifecycle.EstablishmentRequest) (lifecycle.EstablishmentReservation, error) {
	if r.stage == "reserve" {
		return nil, context.Canceled
	}
	reservation, err := r.inner.ReserveAttach(ctx, request)
	if err != nil {
		return nil, err
	}
	return &cancellingEstablishmentReservation{inner: reservation, stage: r.stage}, nil
}

func (*cancellingEstablishmentRegistrar) BeginAttach(context.Context, lifecycle.AttachRequest) (lifecycle.Registration, error) {
	return nil, errors.New("legacy BeginAttach invoked")
}

type cancellingEstablishmentReservation struct {
	inner lifecycle.EstablishmentReservation
	stage string
}

func (r *cancellingEstablishmentReservation) Prepare(ctx context.Context, request lifecycle.AttachRequest) (lifecycle.EnvironmentRef, error) {
	if r.stage == "prepare" {
		return lifecycle.EnvironmentRef{}, context.Canceled
	}
	return r.inner.Prepare(ctx, request)
}

func (r *cancellingEstablishmentReservation) Promote(ctx context.Context) (lifecycle.Registration, error) {
	if r.stage == "promote" {
		return nil, context.Canceled
	}
	registration, err := r.inner.Promote(ctx)
	if err != nil {
		return nil, err
	}
	if r.stage == "post-promote" {
		return &cancelAfterPromotionRegistration{Registration: registration}, nil
	}
	return registration, nil
}

func (r *cancellingEstablishmentReservation) Abort(ctx context.Context, cause error) error {
	return r.inner.Abort(ctx, cause)
}

type cancelAfterPromotionRegistration struct {
	lifecycle.Registration
	failed bool
}

func (r *cancelAfterPromotionRegistration) Register(ctx context.Context, spec lifecycle.RegistrationSpec) (lifecycle.ResourceRef, error) {
	if !r.failed {
		r.failed = true
		return lifecycle.ResourceRef{}, context.Canceled
	}
	return r.Registration.Register(ctx, spec)
}

func TestApplyRunCancellationCleansEveryEstablishmentBoundary(t *testing.T) {
	for _, stage := range []string{"reserve", "prepare", "promote", "post-promote"} {
		t.Run(stage, func(t *testing.T) {
			setFakeLinuxShim(t)
			setFakeLinuxWorkspacePortal(t)
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			store := profile.Store{Root: root}
			if err := store.Save(profile.Default("cancel-" + stage)); err != nil {
				t.Fatal(err)
			}
			core := New(store)
			plan, err := core.PlanRun(RunPlanOptions{
				ProfileName: "cancel-" + stage, Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
			})
			if err != nil {
				t.Fatal(err)
			}
			journalStore := lifecycle.JournalStore{Root: root}
			coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
				Store: journalStore, DaemonID: "daemon-cancel-" + stage, IdleGrace: time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			fake := &lifecycleApplyBackend{
				applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, journal: journalStore,
				bootID: "01234567-89ab-cdef-0123-456789abcdef",
			}
			result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true},
				Lifecycle:                   &cancellingEstablishmentRegistrar{inner: coordinator, stage: stage},
				PrepareWorkspaceAttachment:  func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
				ActivateWorkspaceAttachment: func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
				ReleaseWorkspaceAttachment:  func(context.Context) error { return nil },
				Streams:                     &backend.RunStreams{Ready: func(backend.SessionReadyProof) error { return nil }},
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("ApplyRun error=%v", err)
			}
			assertCancelledEstablishmentClean(t, root, result.SessionID, coordinator)
			if fake.targetRuns != 0 {
				t.Fatalf("cancelled establishment launched %d targets", fake.targetRuns)
			}
		})
	}
}

func TestApplyRunCancelledReconciliationWaitPublishesNoRuntime(t *testing.T) {
	setFakeLinuxShim(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("cancel-wait")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "cancel-wait", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runEnv, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: root}, DaemonID: "daemon-cancel-wait", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := coordinator.BeginReconciliation(context.Background(), runEnv.Record.ID)
	if err != nil || !started {
		t.Fatalf("BeginReconciliation started=%t err=%v", started, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result RunResult
		err    error
	}
	done := make(chan outcome, 1)
	reserveEntered := make(chan struct{})
	registrar := &signallingEstablishmentRegistrar{inner: coordinator, entered: reserveEntered}
	go func() {
		result, runErr := core.ApplyRun(ctx, plan, ApplyRunOptions{
			Backend:          &lifecycleApplyBackend{applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}},
			RequestedBackend: "lima", Lifecycle: registrar,
		})
		done <- outcome{result: result, err: runErr}
	}()
	select {
	case <-reserveEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("ApplyRun did not enter the reconciliation wait")
	}
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("ApplyRun error=%v", got.err)
		}
		assertCancelledEstablishmentClean(t, root, got.result.SessionID, coordinator)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled reconciliation wait did not return")
	}
}

func TestApplyRunWaitsForAutomaticStopBeforeFreshObservationAndTarget(t *testing.T) {
	setFakeLinuxShim(t)
	setFakeLinuxWorkspacePortal(t)
	setFakeLinuxSessionSupervisor(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("wait-automatic-stop")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "wait-automatic-stop", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runEnv, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	stopEntered := make(chan lifecycle.StopRequest, 4)
	releaseStop := make(chan struct{})
	waitingEntered := make(chan struct{}, 1)
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-wait-automatic-stop", IdleGrace: time.Millisecond,
		Enabled: true,
		Stop: func(_ context.Context, request lifecycle.StopRequest) (lifecycle.StopResult, error) {
			stopEntered <- request
			<-releaseStop
			return lifecycle.StopResult{Observation: backend.LifecycleObservation{
				State: backend.LifecycleStopped, InstanceName: request.Incarnation.InstanceName,
				ObservedAt: time.Now().UTC(),
			}}, nil
		},
		Publish: func(event lifecycle.Event) {
			if event.Kind == "attach-establishment-waiting" && event.ReasonCode == "automatic-stop-pending" {
				select {
				case waitingEntered <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: runEnv.Record.ID, InstanceName: runEnv.Record.InstanceName, SessionID: "ses-prior",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: runEnv.Record.InstanceName,
			BootID: "11111111-2222-3333-4444-555555555555", ObservedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	priorGeneration := prior.Incarnation().StartGeneration
	if err := prior.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var stopping lifecycle.StopRequest
	select {
	case stopping = <-stopEntered:
	case <-time.After(time.Second):
		t.Fatal("automatic stop did not enter backend callback")
	}
	if stopping.Incarnation.StartGeneration != priorGeneration {
		t.Fatalf("automatic stop generation=%d want %d", stopping.Incarnation.StartGeneration, priorGeneration)
	}

	targetReached := make(chan struct{}, 1)
	fake := &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, journal: journalStore,
		bootID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		beforeReady: func(*backend.Session) error {
			targetReached <- struct{}{}
			return nil
		},
	}
	type outcome struct {
		result RunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
			Backend: fake, RequestedBackend: "lima", Lifecycle: coordinator,
			PrepareWorkspaceAttachment:  func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
			ActivateWorkspaceAttachment: func(runSession *RunSession) error { return runSession.WorkspaceAttachment.Validate() },
			ReleaseWorkspaceAttachment:  func(context.Context) error { return nil },
			Streams:                     &backend.RunStreams{Ready: func(backend.SessionReadyProof) error { return nil }},
		})
		done <- outcome{result: result, err: runErr}
	}()
	select {
	case <-waitingEntered:
	case got := <-done:
		t.Fatalf("run returned before entering automatic stop wait: err=%v result=%+v", got.err, got.result)
	case <-time.After(time.Second):
		t.Fatal("run did not enter automatic stop wait")
	}
	select {
	case <-targetReached:
		t.Fatal("target launched before automatic stop completed")
	case got := <-done:
		t.Fatalf("run returned before automatic stop completed: err=%v result=%+v", got.err, got.result)
	default:
	}

	close(releaseStop)
	var got outcome
	select {
	case got = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not resume after automatic stop")
	}
	if got.err != nil {
		t.Fatalf("ApplyRun after automatic stop: %v", got.err)
	}
	if fake.targetRuns != 1 {
		t.Fatalf("target runs=%d want 1", fake.targetRuns)
	}
	statuses := coordinator.Snapshot()
	if len(statuses) != 1 || statuses[0].StartGeneration <= priorGeneration {
		t.Fatalf("fresh run reused stopped generation: prior=%d statuses=%+v", priorGeneration, statuses)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertCancelledEstablishmentClean(t *testing.T, root, sessionID string, coordinator *lifecycle.Coordinator) {
	t.Helper()
	if sessionID == "" {
		t.Fatal("cancelled establishment omitted allocated session identity")
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", sessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled establishment retained global session runtime: %v", err)
	}
	environmentStore := environment.Store{Root: root}
	records, err := environmentStore.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if _, err := os.Stat(environmentStore.RuntimeSessionDir(record.ID, sessionID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled establishment retained environment runtime for %s: %v", record.ID, err)
		}
		owners, err := runsession.ListOwners(environmentStore.OwnerRoot(record.ID))
		if err != nil {
			t.Fatal(err)
		}
		for _, owner := range owners {
			if owner.SessionID == sessionID {
				t.Fatalf("cancelled establishment retained owner: %+v", owner)
			}
		}
	}
	for _, status := range coordinator.Snapshot() {
		if status.EstablishingSessions != 0 {
			t.Fatalf("cancelled establishment retained lifecycle authority: %+v", status)
		}
		for _, resources := range [][]lifecycle.ResourceSummary{status.Pins, status.Drains, status.Retained, status.Handoffs, status.Orphans} {
			for _, resource := range resources {
				if resource.ID == sessionID {
					t.Fatalf("cancelled establishment retained lifecycle session resource: %+v", status)
				}
			}
		}
	}
}
