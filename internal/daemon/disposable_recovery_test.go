package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

type daemonDisposableRecoveryBackend struct {
	mu             sync.Mutex
	absent         map[string]bool
	cleanupStarted chan string
	release        chan struct{}
	cleanupCalls   atomic.Int32
	active         atomic.Int32
	maximum        atomic.Int32
}

func (provider *daemonDisposableRecoveryBackend) ObserveLifecycle(_ context.Context, instanceName string) backend.LifecycleObservation {
	provider.mu.Lock()
	absent := provider.absent[instanceName]
	provider.mu.Unlock()
	state := backend.LifecycleRunning
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	if absent {
		state = backend.LifecycleAbsent
		bootID = ""
	}
	return backend.LifecycleObservation{
		State: state, InstanceName: instanceName, BootID: bootID, ObservedAt: time.Now().UTC(),
	}
}

func (*daemonDisposableRecoveryBackend) StopInstance(context.Context, string) error { return nil }

func (provider *daemonDisposableRecoveryBackend) Cleanup(ctx context.Context, session *backend.Session) error {
	provider.cleanupCalls.Add(1)
	active := provider.active.Add(1)
	defer provider.active.Add(-1)
	for {
		maximum := provider.maximum.Load()
		if active <= maximum || provider.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case provider.cleanupStarted <- session.InstanceName:
	default:
	}
	select {
	case <-provider.release:
		provider.mu.Lock()
		provider.absent[session.InstanceName] = true
		provider.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDaemonRecoversDisposableCrashResidueWithoutBlockingStatus(t *testing.T) {
	store, records := daemonDisposableRecoveryRecords(t, 1)
	provider := newDaemonDisposableRecoveryBackend()
	startedAt := time.Now()
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("daemon start waited for disposable recovery: %s", elapsed)
	}
	select {
	case <-provider.cleanupStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("disposable recovery did not reach backend cleanup")
	}
	if status := d.Status(); status.State != "serving" || status.InstanceID == "" {
		t.Fatalf("daemon status unavailable during disposal: %+v", status)
	}
	close(provider.release)
	waitForDisposableRecordsRemoved(t, store, records)
	if provider.cleanupCalls.Load() != 1 {
		t.Fatalf("cleanup calls=%d want 1", provider.cleanupCalls.Load())
	}
}

func TestDaemonBoundsConcurrentDisposableRecoveryWorkers(t *testing.T) {
	store, records := daemonDisposableRecoveryRecords(t, 7)
	provider := newDaemonDisposableRecoveryBackend()
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	deadline := time.After(3 * time.Second)
	for provider.maximum.Load() < 4 {
		select {
		case <-provider.cleanupStarted:
		case <-deadline:
			t.Fatalf("disposable worker pool max=%d want 4", provider.maximum.Load())
		}
	}
	time.Sleep(50 * time.Millisecond)
	if maximum := provider.maximum.Load(); maximum != 4 {
		t.Fatalf("disposable recovery concurrency=%d want 4", maximum)
	}
	if status := d.Status(); status.State != "serving" {
		t.Fatalf("status unavailable with saturated recovery workers: %+v", status)
	}
	close(provider.release)
	waitForDisposableRecordsRemoved(t, store, records)
}

func TestDaemonShutdownInterruptsDisposableRecoveryAndRetainsIntent(t *testing.T) {
	store, records := daemonDisposableRecoveryRecords(t, 1)
	record := records[0]
	provider := newDaemonDisposableRecoveryBackend()
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.cleanupStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("disposable recovery did not start")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	retained, err := (environment.Store{Root: store.Root}).Load(record.ID)
	if err != nil || !retained.Disposable || retained.Status != environment.StatusError {
		t.Fatalf("interrupted record=%+v err=%v", retained, err)
	}
	journal, err := (lifecycle.JournalStore{Root: store.Root}).Load(record.ID)
	if err != nil || journal.Disposal == nil || journal.Disposal.State != lifecycle.DisposalStateBlocked {
		t.Fatalf("interrupted journal=%+v err=%v", journal, err)
	}
	if provider.cleanupCalls.Load() != 1 {
		t.Fatalf("cleanup calls=%d want 1", provider.cleanupCalls.Load())
	}
}

func TestDaemonConvergesValidIntentOnlyResidueOnlyWhenExactInstanceIsAbsent(t *testing.T) {
	store, records := daemonDisposableRecoveryRecords(t, 1)
	record := records[0]
	identity, err := environment.NewDisposableIdentity(record)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: store.Root}, DaemonID: "daemon-intent-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.BeginDisposal(context.Background(), lifecycle.DisposalRequest{
		EnvironmentID: identity.EnvironmentID, Backend: identity.Backend,
		InstanceName: identity.InstanceName, RecordDigest: identity.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (environment.Store{Root: store.Root}).Remove(record.ID); err != nil {
		t.Fatal(err)
	}

	provider := newDaemonDisposableRecoveryBackend()
	provider.absent[record.InstanceName] = true
	d, err := Start(Options{
		Store: store,
		LifecycleBackend: func(probe environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			if probe.ID != record.ID || probe.Backend != record.Backend || probe.InstanceName != record.InstanceName {
				return nil, errors.New("missing-record recovery changed exact identity")
			}
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, loadErr := (lifecycle.JournalStore{Root: store.Root}).Load(record.ID)
		if errors.Is(loadErr, os.ErrNotExist) {
			if provider.cleanupCalls.Load() != 0 {
				t.Fatalf("intent-only convergence invoked backend cleanup %d time(s)", provider.cleanupCalls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("valid intent-only absent residue did not converge")
}

func newDaemonDisposableRecoveryBackend() *daemonDisposableRecoveryBackend {
	return &daemonDisposableRecoveryBackend{
		absent: map[string]bool{}, cleanupStarted: make(chan string, 16), release: make(chan struct{}),
	}
}

func daemonDisposableRecoveryRecords(t *testing.T, count int) (profile.Store, []environment.Record) {
	t.Helper()
	store := testStore(t)
	envStore := environment.Store{Root: store.Root}
	records := make([]environment.Record, 0, count)
	for index := 0; index < count; index++ {
		record, err := envStore.Create(environment.Spec{
			Name: fmt.Sprintf("disposable-%d", index), ImageRef: environment.BuiltinBaseImage,
			Profile: "default", Backend: "lima", Mode: environment.ModeDedicated,
			MachineIdentityID:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			BootConfigurationID: daemonTestBootConfigurationID,
			DedicatedWorkspace:  t.TempDir(), DedicatedGuestRoot: "/workspace",
			InstanceName: fmt.Sprintf("hideout-disposable-%d", index), Disposable: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		record.Status = environment.StatusError
		if err := envStore.Save(record); err != nil {
			t.Fatal(err)
		}
		seedDaemonLifecycleJournal(t, store, record)
		records = append(records, record)
	}
	return store, records
}

func waitForDisposableRecordsRemoved(t *testing.T, store profile.Store, records []environment.Record) {
	t.Helper()
	envStore := environment.Store{Root: store.Root}
	journalStore := lifecycle.JournalStore{Root: store.Root}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allRemoved := true
		for _, record := range records {
			if _, err := envStore.Load(record.ID); err == nil {
				allRemoved = false
				break
			}
			if _, err := journalStore.Load(record.ID); !errors.Is(err, os.ErrNotExist) {
				allRemoved = false
				break
			}
		}
		if allRemoved {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("disposable residue did not converge: records=%+v", records)
}
