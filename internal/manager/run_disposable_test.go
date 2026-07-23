package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
)

func (b *applyRunFakeBackend) ObserveLifecycle(_ context.Context, instanceName string) backend.LifecycleObservation {
	b.observeCalls++
	if len(b.observations) == 0 {
		return backend.LifecycleObservation{State: backend.LifecycleAbsent, InstanceName: instanceName, ObservedAt: time.Now().UTC()}
	}
	observation := b.observations[0]
	if len(b.observations) > 1 {
		b.observations = b.observations[1:]
	}
	observation.InstanceName = instanceName
	observation.ObservedAt = time.Now().UTC()
	return observation
}

func (b *applyRunFakeBackend) StopInstance(context.Context, string) error { return nil }

func applyDisposableRun(t *testing.T, store profile.Store, fake *applyRunFakeBackend, backendName string) (RunResult, error) {
	t.Helper()
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     backendName,
		Workspace:   t.TempDir(),
		Command:     []string{"tool"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	return core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   backendName,
		AllowWeakIsolation: backendName == "native",
		Environment:        RunEnvironmentOptions{RemoveAfterRun: true, Create: true},
	})
}

func disposableRecords(t *testing.T, store profile.Store) []environment.Record {
	t.Helper()
	records, err := (environment.Store{Root: store.Root}).List()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestApplyRunDisposableRemovesEnvironmentAfterProvedTeardown(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	fake := &applyRunFakeBackend{}
	result, err := applyDisposableRun(t, store, fake, "native")
	if err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}
	if result.EnvironmentDisposition != "removed" {
		t.Fatalf("disposition=%q want removed: %+v", result.EnvironmentDisposition, result)
	}
	if result.EnvironmentID == "" || !strings.HasPrefix(result.EnvironmentName, "rm-") {
		t.Fatalf("result must keep the disposed environment identity for audit: %+v", result)
	}
	if records := disposableRecords(t, store); len(records) != 0 {
		t.Fatalf("disposable record must be removed after a proved teardown: %+v", records)
	}
	auditData, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl"))
	if err != nil {
		t.Fatalf("read environment audit: %v", err)
	}
	if !strings.Contains(string(auditData), `"env.dispose"`) || !strings.Contains(string(auditData), `"removed"`) {
		t.Fatalf("environment audit must record the disposal: %s", auditData)
	}
}

func TestApplyRunDisposableTargetFailureStillDisposes(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	fake := &applyRunFakeBackend{runErr: errors.New("target exploded")}
	result, err := applyDisposableRun(t, store, fake, "native")
	if err == nil || !strings.Contains(err.Error(), "target exploded") {
		t.Fatalf("target failure must surface, got %v", err)
	}
	if result.EnvironmentDisposition != "removed" {
		t.Fatalf("target failure still disposes the environment, got %q", result.EnvironmentDisposition)
	}
	if records := disposableRecords(t, store); len(records) != 0 {
		t.Fatalf("target failure is not a teardown failure; record must be removed: %+v", records)
	}
}

func TestApplyRunDisposableRetainsRecordWhenCleanupFails(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	fake := &applyRunFakeBackend{cleanupErr: errors.New("delete exploded")}
	result, err := applyDisposableRun(t, store, fake, "native")
	if err == nil || !strings.Contains(err.Error(), "delete exploded") {
		t.Fatalf("cleanup failure must surface, got %v", err)
	}
	if result.EnvironmentDisposition != "cleanup-required" {
		t.Fatalf("unproved teardown must report cleanup-required, got %q", result.EnvironmentDisposition)
	}
	records := disposableRecords(t, store)
	if len(records) != 1 || !records[0].Disposable || records[0].Status != environment.StatusError {
		t.Fatalf("unproved teardown must retain the disposable record in error state: %+v", records)
	}
}

func TestDisposeFinishedEnvironmentGradedFailure(t *testing.T) {
	newDisposableRecord := func(t *testing.T) (Core, environment.Store, environment.Record) {
		t.Helper()
		store := profile.Store{Root: t.TempDir()}
		core := New(store)
		envStore := environment.Store{Root: store.Root}
		spec := dedicatedRunEnvironmentSpec(runtimeConfigurationTestProfile("default"), "lima", t.TempDir(), "/workspace", "rm-graded123456")
		spec.Disposable = true
		rec, err := envStore.Create(spec)
		if err != nil {
			t.Fatal(err)
		}
		return core, envStore, rec
	}

	t.Run("proved disposal with a session cleanup error still removes and returns the error", func(t *testing.T) {
		core, envStore, rec := newDisposableRecord(t)
		sessionErr := errors.New("session cleanup exploded")
		disposition, err := core.disposeFinishedEnvironment(envStore, rec, true, sessionErr)
		if disposition != "removed" || !errors.Is(err, sessionErr) {
			t.Fatalf("disposition=%q err=%v; want removed with the cleanup error preserved", disposition, err)
		}
		if _, loadErr := envStore.Load(rec.ID); loadErr == nil {
			t.Fatal("proved disposal must remove the record even when session cleanup errored")
		}
	})

	t.Run("unproved disposal retains the record in error state", func(t *testing.T) {
		core, envStore, rec := newDisposableRecord(t)
		disposition, err := core.disposeFinishedEnvironment(envStore, rec, false, nil)
		if disposition != "cleanup-required" || err != nil {
			t.Fatalf("disposition=%q err=%v; want cleanup-required without new errors", disposition, err)
		}
		retained, loadErr := envStore.Load(rec.ID)
		if loadErr != nil || retained.Status != environment.StatusError || !retained.Disposable {
			t.Fatalf("unproved disposal must retain the disposable record in error state: %+v err=%v", retained, loadErr)
		}
	})
}

// noObservationBackend satisfies backend.Backend but not the manager's
// EnvironmentLifecycleBackend, modeling a backend that cannot observe VM
// inventory. Only the type assertion in disposableCleanupProved touches it.
type noObservationBackend struct{ backend.Backend }

func TestDisposableCleanupProvedRequiresLimaObservation(t *testing.T) {
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	limaRecord := environment.Record{Backend: "lima", InstanceName: "hideout-default-env-rm"}
	nativeRecord := environment.Record{Backend: "native"}

	proved := &applyRunFakeBackend{name: "lima", observations: []backend.LifecycleObservation{
		{State: backend.LifecycleAbsent},
		{State: backend.LifecycleAbsent},
	}}
	if !disposableCleanupProved(proved, limaRecord) || proved.observeCalls != 2 {
		t.Fatalf("stable absence must prove lima disposal (observations=%d)", proved.observeCalls)
	}

	transient := &applyRunFakeBackend{name: "lima", observations: []backend.LifecycleObservation{
		{State: backend.LifecycleAbsent},
		{State: backend.LifecycleRunning, BootID: bootID},
	}}
	if disposableCleanupProved(transient, limaRecord) {
		t.Fatal("a transient absence must not prove lima disposal")
	}

	if disposableCleanupProved(noObservationBackend{}, limaRecord) {
		t.Fatal("a lima record without lifecycle observation must fail closed")
	}
	if !disposableCleanupProved(noObservationBackend{}, nativeRecord) {
		t.Fatal("a non-VM backend proves by its clean cleanup return")
	}
}

type disposableLifecycleApplyBackend struct {
	*lifecycleApplyBackend
	running bool
}

type observingDisposalCoordinator struct {
	lifecycle.DisposalCoordinator
	environmentStore               environment.Store
	journalStore                   lifecycle.JournalStore
	failComplete                   bool
	recordPresentAtJournalRemoval  bool
	journalGoneBeforeRecordRemoval bool
}

func (coordinator *observingDisposalCoordinator) CompleteDisposalMetadata(ctx context.Context, environmentID, digest string) error {
	if _, err := coordinator.environmentStore.Load(environmentID); err == nil {
		coordinator.recordPresentAtJournalRemoval = true
	}
	if coordinator.failComplete {
		return errors.New("injected journal removal failure")
	}
	if err := coordinator.DisposalCoordinator.CompleteDisposalMetadata(ctx, environmentID, digest); err != nil {
		return err
	}
	_, journalErr := coordinator.journalStore.Load(environmentID)
	_, recordErr := coordinator.environmentStore.Load(environmentID)
	coordinator.journalGoneBeforeRecordRemoval = errors.Is(journalErr, os.ErrNotExist) && recordErr == nil
	return nil
}

func (provider *disposableLifecycleApplyBackend) ObserveLifecycle(_ context.Context, instanceName string) backend.LifecycleObservation {
	state := backend.LifecycleAbsent
	bootID := ""
	if provider.running {
		state = backend.LifecycleRunning
		bootID = provider.bootID
	}
	return backend.LifecycleObservation{
		State: state, InstanceName: instanceName, BootID: bootID, ObservedAt: time.Now().UTC(),
	}
}

func (provider *disposableLifecycleApplyBackend) Activate(ctx context.Context, session *backend.Session, env []string) error {
	if err := provider.lifecycleApplyBackend.Activate(ctx, session, env); err != nil {
		return err
	}
	provider.running = true
	return nil
}

func (provider *disposableLifecycleApplyBackend) Cleanup(ctx context.Context, session *backend.Session) error {
	err := provider.applyRunFakeBackend.Cleanup(ctx, session)
	if err == nil {
		provider.running = false
	}
	return err
}

func TestApplyRunDisposableLifecycleProtocolRemovesJournalBeforeRecord(t *testing.T) {
	setFakeLinuxShim(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "lima", Workspace: t.TempDir(), Command: []string{"tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-disposable-finalizer", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	observingCoordinator := &observingDisposalCoordinator{
		DisposalCoordinator: coordinator,
		environmentStore:    environment.Store{Root: root},
		journalStore:        journalStore,
	}
	core.LifecycleDisposals = observingCoordinator
	provider := &disposableLifecycleApplyBackend{lifecycleApplyBackend: &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"},
		journal:             journalStore, bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: provider, RequestedBackend: "lima",
		Environment: RunEnvironmentOptions{RemoveAfterRun: true, Create: true},
		Lifecycle:   coordinator,
	})
	if err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}
	if result.EnvironmentDisposition != DisposableRecoveryRemoved {
		t.Fatalf("result=%+v", result)
	}
	if !slices.Contains(provider.calls, "cleanup") || provider.running {
		t.Fatalf("provider calls=%v running=%v", provider.calls, provider.running)
	}
	if _, err := (environment.Store{Root: root}).Load(result.EnvironmentID); err == nil {
		t.Fatal("ordinary disposable finalizer retained environment record")
	}
	if _, err := journalStore.Load(result.EnvironmentID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary disposable finalizer retained lifecycle journal: %v", err)
	}
	if !observingCoordinator.recordPresentAtJournalRemoval ||
		!observingCoordinator.journalGoneBeforeRecordRemoval {
		t.Fatalf("record-last ordering was not observed: %+v", observingCoordinator)
	}
}

func TestApplyRunDisposableLifecycleProtocolRetainsClassifiableStateWhenJournalRemovalFails(t *testing.T) {
	setFakeLinuxShim(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "lima", Workspace: t.TempDir(), Command: []string{"tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-disposable-failure", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	observingCoordinator := &observingDisposalCoordinator{
		DisposalCoordinator: coordinator,
		environmentStore:    environment.Store{Root: root},
		journalStore:        journalStore,
		failComplete:        true,
	}
	core.LifecycleDisposals = observingCoordinator
	provider := &disposableLifecycleApplyBackend{lifecycleApplyBackend: &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"},
		journal:             journalStore, bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: provider, RequestedBackend: "lima",
		Environment: RunEnvironmentOptions{RemoveAfterRun: true, Create: true},
		Lifecycle:   coordinator,
	})
	if err == nil || !strings.Contains(err.Error(), "injected journal removal failure") {
		t.Fatalf("error=%v result=%+v", err, result)
	}
	if result.EnvironmentDisposition != DisposableRecoveryCleanupRequired ||
		!observingCoordinator.recordPresentAtJournalRemoval {
		t.Fatalf("result=%+v observer=%+v", result, observingCoordinator)
	}
	retained, err := (environment.Store{Root: root}).Load(result.EnvironmentID)
	if err != nil || !retained.Disposable || retained.Status != environment.StatusError {
		t.Fatalf("retained record=%+v err=%v", retained, err)
	}
	journal, err := journalStore.Load(result.EnvironmentID)
	if err != nil || journal.Disposal == nil ||
		journal.Disposal.State != lifecycle.DisposalStateBlocked ||
		journal.Disposal.ReasonCode != lifecycle.DisposalReasonJournalRemovalFailed {
		t.Fatalf("retained journal=%+v err=%v", journal, err)
	}
}

func TestApplyRunDisposableLifecycleProtocolPreservesTargetAndCleanupResults(t *testing.T) {
	for _, test := range []struct {
		name            string
		targetErr       error
		cleanupErr      error
		wantDisposition string
		wantRecord      bool
	}{
		{
			name:            "target failure still removes",
			targetErr:       errors.New("managed target exploded"),
			wantDisposition: DisposableRecoveryRemoved,
		},
		{
			name:            "target failure remains primary when cleanup is required",
			targetErr:       errors.New("managed target exploded"),
			cleanupErr:      errors.New("managed cleanup exploded"),
			wantDisposition: DisposableRecoveryCleanupRequired,
			wantRecord:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			setFakeLinuxShim(t)
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			store := profile.Store{Root: root}
			core := New(store)
			plan, err := core.PlanRun(RunPlanOptions{
				ProfileName: "default", Backend: "lima", Workspace: t.TempDir(), Command: []string{"tool"},
			})
			if err != nil {
				t.Fatal(err)
			}
			journalStore := lifecycle.JournalStore{Root: root}
			coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
				Store: journalStore, DaemonID: "daemon-disposable-result", IdleGrace: time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			core.LifecycleDisposals = coordinator
			provider := &disposableLifecycleApplyBackend{lifecycleApplyBackend: &lifecycleApplyBackend{
				applyRunFakeBackend: &applyRunFakeBackend{
					name: "lima", runErr: test.targetErr, cleanupErr: test.cleanupErr,
				},
				journal: journalStore, bootID: "01234567-89ab-cdef-0123-456789abcdef",
			}}
			result, runErr := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend: provider, RequestedBackend: "lima",
				Environment: RunEnvironmentOptions{RemoveAfterRun: true, Create: true},
				Lifecycle:   coordinator,
			})
			if !errors.Is(runErr, test.targetErr) || result.Error != test.targetErr.Error() {
				t.Fatalf("target result changed: result=%+v err=%v", result, runErr)
			}
			if result.EnvironmentDisposition != test.wantDisposition {
				t.Fatalf("result=%+v", result)
			}
			if test.cleanupErr == nil && result.CleanupError != "" {
				t.Fatalf("clean disposal reported cleanup error: %+v", result)
			}
			if test.cleanupErr != nil && !strings.Contains(result.CleanupError, test.cleanupErr.Error()) {
				t.Fatalf("cleanup result was lost: %+v", result)
			}
			_, recordErr := (environment.Store{Root: root}).Load(result.EnvironmentID)
			if test.wantRecord && recordErr != nil {
				t.Fatalf("cleanup-required record missing: %v", recordErr)
			}
			if !test.wantRecord && recordErr == nil {
				t.Fatalf("removed disposal retained record: %v", recordErr)
			}
		})
	}
}
