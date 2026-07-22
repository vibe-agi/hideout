package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

const daemonTestBootConfigurationID = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

type daemonLifecycleBackend struct {
	mu           sync.Mutex
	observation  backend.LifecycleObservation
	stopCalls    atomic.Int32
	cleanupCalls atomic.Int32
	absenceErr   error
	proved       []string
}

type blockingLifecycleBackend struct {
	*daemonLifecycleBackend
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type concurrencyLifecycleBackend struct {
	active  atomic.Int32
	maximum atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (b *concurrencyLifecycleBackend) ObserveLifecycle(ctx context.Context, instanceName string) backend.LifecycleObservation {
	active := b.active.Add(1)
	defer b.active.Add(-1)
	for {
		maximum := b.maximum.Load()
		if active <= maximum || b.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
		return backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: instanceName,
			BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
		}
	case <-ctx.Done():
		return backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: instanceName,
			ObservedAt: time.Now().UTC(), ReasonCode: "reconciliation-cancelled",
		}
	}
}

func (*concurrencyLifecycleBackend) StopInstance(context.Context, string) error { return nil }
func (*concurrencyLifecycleBackend) Cleanup(context.Context, *backend.Session) error {
	return nil
}

func (b *blockingLifecycleBackend) ObserveLifecycle(ctx context.Context, instanceName string) backend.LifecycleObservation {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return b.daemonLifecycleBackend.ObserveLifecycle(ctx, instanceName)
	case <-ctx.Done():
		return backend.LifecycleObservation{
			State: backend.LifecycleUnknown, InstanceName: instanceName,
			ObservedAt: time.Now().UTC(), ReasonCode: "reconciliation-cancelled",
		}
	}
}

func (b *daemonLifecycleBackend) ObserveLifecycle(context.Context, string) backend.LifecycleObservation {
	b.mu.Lock()
	defer b.mu.Unlock()
	value := b.observation
	value.ObservedAt = time.Now().UTC()
	return value
}

func (b *daemonLifecycleBackend) StopInstance(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopCalls.Add(1)
	b.observation = backend.LifecycleObservation{State: backend.LifecycleStopped, InstanceName: b.observation.InstanceName}
	return nil
}

func (b *daemonLifecycleBackend) Cleanup(context.Context, *backend.Session) error {
	b.cleanupCalls.Add(1)
	return nil
}

func (b *daemonLifecycleBackend) ProveSessionAbsent(_ context.Context, _ string, sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.proved = append(b.proved, sessionID)
	return b.absenceErr
}

func TestDaemonServesStatusWhileLifecycleReconciliationIsPending(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &blockingLifecycleBackend{
		daemonLifecycleBackend: &daemonLifecycleBackend{observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: record.InstanceName,
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		}},
		started: make(chan struct{}), release: make(chan struct{}),
	}
	startedAt := time.Now()
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("daemon start waited for backend reconciliation: %s", elapsed)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("background reconciliation did not start")
	}
	code, body := daemonDo(t, d, http.MethodGet, statusPath, d.Token())
	if code != http.StatusOK {
		t.Fatalf("status unavailable during reconciliation: code=%d body=%s", code, body)
	}
	var status Status
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Lifecycle) != 1 || status.Lifecycle[0].Reconciliation != "pending" || status.Lifecycle[0].ReasonCode != "reconciliation-pending" {
		t.Fatalf("pending reconciliation is not operator-visible: %+v", status.Lifecycle)
	}
	if code, body := daemonPost(t, d, lifecycleStopPath, `{"environmentId":"`+record.ID+`"}`, d.Token()); code != http.StatusConflict || !strings.Contains(string(body), lifecycle.ErrReconciliationInFlight.Error()) {
		t.Fatalf("stop crossed reconciliation fence: code=%d body=%s", code, body)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(provider.release)
	}()
	mutationStarted := time.Now()
	if code, body := daemonPost(t, d, lifecycleMutatePath, `{"environmentId":"`+record.ID+`","operation":"remove","force":true}`, d.Token()); code != http.StatusOK {
		t.Fatalf("mutation did not wait for reconciliation: code=%d body=%s", code, body)
	}
	if elapsed := time.Since(mutationStarted); elapsed < 40*time.Millisecond {
		t.Fatalf("mutation crossed the reconciliation fence after %s", elapsed)
	}
	if loaded, err := (environment.Store{Root: store.Root}).Load(record.ID); err == nil {
		t.Fatalf("waited mutation did not remove environment: %+v", loaded)
	}
}

func TestDaemonRetriesBlockedLifecycleReconciliationInSameEpoch(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	var factoryCalls atomic.Int32
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			if factoryCalls.Add(1) == 1 {
				return nil, errors.New("transient provider failure")
			}
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleReason(t, d, record.ID, "backend-provider-unavailable")
	code, body := daemonPost(t, d, lifecycleReconcilePath, `{"environmentId":"`+record.ID+`"}`, d.Token())
	if code != http.StatusOK {
		t.Fatalf("retry reconciliation code=%d body=%s", code, body)
	}
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityIdleGrace)
	if got := factoryCalls.Load(); got != 2 {
		t.Fatalf("reconciliation retry factory calls=%d want 2", got)
	}
}

func TestDaemonBoundsParallelLifecycleReconciliation(t *testing.T) {
	store, _ := daemonLifecycleEnvironment(t)
	environmentStore := environment.Store{Root: store.Root}
	for index := 1; index < 7; index++ {
		if _, err := environmentStore.Create(environment.Spec{
			Name: fmt.Sprintf("parallel-%d", index), ImageRef: environment.BuiltinBaseImage,
			Profile: "default", Backend: "lima", Mode: environment.ModeWorkspaceBound,
			MachineIdentityID:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			BootConfigurationID: daemonTestBootConfigurationID,
			BoundWorkspace:      t.TempDir(), BoundGuestRoot: "/workspace",
			InstanceName: fmt.Sprintf("hideout-parallel-%d", index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	provider := &concurrencyLifecycleBackend{
		started: make(chan struct{}, 16), release: make(chan struct{}),
	}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	deadline := time.After(2 * time.Second)
	for provider.maximum.Load() < 4 {
		select {
		case <-provider.started:
		case <-deadline:
			t.Fatalf("bounded worker pool did not reach four concurrent probes: max=%d", provider.maximum.Load())
		}
	}
	time.Sleep(50 * time.Millisecond)
	if maximum := provider.maximum.Load(); maximum != 4 {
		t.Fatalf("lifecycle reconciliation concurrency=%d want exactly 4", maximum)
	}
	if code, body := daemonDo(t, d, http.MethodGet, statusPath, d.Token()); code != http.StatusOK || !strings.Contains(string(body), `"reconciliation":"pending"`) {
		t.Fatalf("status unavailable while worker pool is saturated: code=%d body=%s", code, body)
	}
	close(provider.release)
}

func TestDaemonClassifiesLifecycleJournalWithoutEnvironmentRecord(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: store.Root}, DaemonID: "daemon-previous",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reconcile(context.Background(), lifecycle.ReconcileInput{
		EnvironmentID: record.ID, InstanceName: record.InstanceName,
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: record.InstanceName,
			BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (environment.Store{Root: store.Root}).Remove(record.ID); err != nil {
		t.Fatal(err)
	}

	d, err := Start(Options{
		Store: store,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			return nil, errors.New("must not probe a missing environment record")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	status := waitForLifecycleReason(t, d, record.ID, "environment-record-unavailable")
	if status.Reconciliation != "blocked" || status.Activity != lifecycle.ActivityBlocked {
		t.Fatalf("missing environment journal was not failed closed: %+v", status)
	}
}

func waitForLifecycleActivity(t *testing.T, d *Daemon, environmentID string, activity lifecycle.Activity) lifecycle.Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, status := range d.Status().Lifecycle {
			if status.EnvironmentID == environmentID && status.Activity == activity {
				return status
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("environment %s did not reach activity %s: %+v", environmentID, activity, d.Status().Lifecycle)
	return lifecycle.Status{}
}

func waitForLifecycleReason(t *testing.T, d *Daemon, environmentID, reason string) lifecycle.Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, status := range d.Status().Lifecycle {
			if status.EnvironmentID == environmentID && status.ReasonCode == reason {
				return status
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("environment %s did not report reason %s: %+v", environmentID, reason, d.Status().Lifecycle)
	return lifecycle.Status{}
}

func waitForLifecycleReconciliation(t *testing.T, d *Daemon, environmentID string) lifecycle.Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, status := range d.Status().Lifecycle {
			if status.EnvironmentID == environmentID && status.Reconciliation == "complete" {
				return status
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("environment %s did not complete lifecycle reconciliation: %+v", environmentID, d.Status().Lifecycle)
	return lifecycle.Status{}
}

func waitForLifecycleOrphans(t *testing.T, d *Daemon, environmentID string) lifecycle.Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, status := range d.Status().Lifecycle {
			if status.EnvironmentID == environmentID && status.Reconciliation == "blocked" && len(status.Orphans) != 0 {
				return status
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("environment %s did not report lifecycle orphans: %+v", environmentID, d.Status().Lifecycle)
	return lifecycle.Status{}
}

func TestDaemonBlocksRunningEnvironmentWithoutLifecycleJournal(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	if err := (lifecycle.JournalStore{Root: store.Root}).Remove(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := (environment.Store{Root: store.Root}).PrepareRuntimeRoot(record.ID); err != nil {
		t.Fatal(err)
	}
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	status := waitForLifecycleReason(t, d, record.ID, "backend-incarnation-changed")
	if status.Reconciliation != "blocked" || status.Activity != lifecycle.ActivityBlocked || status.IdleDeadline != nil {
		t.Fatalf("running environment without lifecycle proof was not blocked: %+v", status)
	}
}

func TestDaemonRestartRetainsStaleRunningOwnerForExplicitRecovery(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	if err := (lifecycle.JournalStore{Root: store.Root}).Remove(record.ID); err != nil {
		t.Fatal(err)
	}
	environmentStore := environment.Store{Root: store.Root}
	sessionID := "ses_20260716T120000Z_0123456789abcdef"
	if _, err := environmentStore.PrepareSessionRuntime(record.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	ownerRecord := runsession.OwnerRecord{
		Schema: runsession.ActiveSessionSchema, SessionID: sessionID,
		EnvironmentID: record.ID, Profile: "default", Backend: "lima", WorkspaceID: "wrk_" + strings.Repeat("a", 64),
		SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
		State:             runsession.OwnerStateRunning, TerminalMode: runsession.TerminalNone,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CommandClass: "bash",
	}
	ownerData, err := json.Marshal(ownerRecord)
	if err != nil {
		t.Fatal(err)
	}
	ownerDir := filepath.Join(environmentStore.OwnerRoot(record.ID), sessionID)
	if err := os.MkdirAll(ownerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "session.json"), append(ownerData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	oldCoordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: store.Root}, DaemonID: "daemon_previous",
		IdleGrace: time.Hour, Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldRegistration, err := oldCoordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, SessionID: sessionID,
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: record.InstanceName,
			BootID: bootID, ObservedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := oldRegistration.BindBoot(context.Background(), bootID); err != nil {
		t.Fatal(err)
	}
	if err := oldRegistration.Transition(context.Background(), oldRegistration.Session(), lifecycle.StateActive); err != nil {
		t.Fatal(err)
	}
	brokerRef, err := oldRegistration.Register(context.Background(), lifecycle.RegistrationSpec{
		Kind: lifecycle.KindBrokerListener, ID: sessionID,
		OwnerKind: "session", OwnerID: sessionID, State: lifecycle.StateActive,
		Dependencies: []lifecycle.DependencySpec{{Ref: oldRegistration.Session(), StopMode: lifecycle.StopModeDrain}},
		Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain,
		PossibleVMDependency: true,
	})
	if err != nil || brokerRef.Kind == "" {
		t.Fatalf("seed previous live graph: ref=%+v err=%v", brokerRef, err)
	}
	if err := backend.WriteActivationReceipt(environmentStore.RuntimeDir(record.ID), backend.ActivationReceipt{
		Schema: backend.ActivationReceiptSchema, EnvironmentID: record.ID, InstanceName: record.InstanceName,
		ConfigSHA256: strings.Repeat("a", 64), RuntimeIdentitySHA256: strings.Repeat("b", 64),
		BootID: bootID, NamespaceProbe: true, OwnerSessionID: sessionID, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
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
	defer d.Stop(context.Background())
	status := waitForLifecycleReason(t, d, record.ID, "owner-requires-explicit-recovery")
	if status.Reconciliation != "blocked" || status.Activity != lifecycle.ActivityBlocked || status.IdleDeadline != nil {
		t.Fatalf("stale running owner did not keep restart fail closed: %+v", status)
	}
	if len(status.Orphans) != 1 || status.Orphans[0].Kind != lifecycle.KindRunSession || status.Orphans[0].ID != sessionID {
		t.Fatalf("proved-absent provider rows survived stale-owner classification: %+v", status.Orphans)
	}
	owners, err := runsession.ListOwners(environmentStore.OwnerRoot(record.ID))
	if err != nil || len(owners) != 1 || owners[0].SessionID != sessionID || owners[0].Status != runsession.OwnerStale {
		t.Fatalf("owners=%+v err=%v", owners, err)
	}
	if _, err := os.Stat(environmentStore.RuntimeSessionDir(record.ID, sessionID)); err != nil {
		t.Fatalf("stale session runtime was removed before explicit recovery: %v", err)
	}
}

func TestDaemonRestartLeavesStaleOwnerOrphaned(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	environmentStore := environment.Store{Root: store.Root}
	owner, err := runsession.AcquireOwner(environmentStore.OwnerRoot(record.ID), runsession.OwnerRecord{
		Schema: runsession.ActiveSessionSchema, SessionID: "ses_20260716T120000Z_0123456789abcdef",
		EnvironmentID: record.ID, Profile: "default", Backend: "lima", WorkspaceID: "wrk_" + strings.Repeat("a", 64),
		SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
		State:             runsession.OwnerStateFailed, TerminalMode: runsession.TerminalNone,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CommandClass: "bash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	status := waitForLifecycleOrphans(t, d, record.ID)
	if len(status.Orphans) == 0 {
		t.Fatalf("stale owner was re-adopted or hidden: %+v", status)
	}
}

func TestDaemonShutdownCancelsDeferredAutomaticStop(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: 50 * time.Millisecond,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityIdleGrace)
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if provider.stopCalls.Load() != 0 {
		t.Fatal("daemon shutdown allowed a deferred timer to stop the VM")
	}
	auditData, err := os.ReadFile(filepath.Join(d.RuntimeDir(), auditName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditData), `"action":"lifecycle.stop-deferred"`) ||
		!strings.Contains(string(auditData), `"decision":"deny"`) ||
		!strings.Contains(string(auditData), `"reasonCode":"daemon-shutdown"`) {
		t.Fatalf("bounded shutdown omitted truthful deferred-stop evidence: %s", auditData)
	}
}

func TestLifecycleBackendFactoryAloneDoesNotEnableAutomaticStop(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: 20 * time.Millisecond,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityIdleEligible)
	time.Sleep(80 * time.Millisecond)
	if calls := provider.stopCalls.Load(); calls != 0 {
		t.Fatalf("backend factory silently enabled automatic stop: calls=%d", calls)
	}
}

func TestLifecycleAutomaticStopRequiresExplicitReadinessEnable(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: 20 * time.Millisecond, LifecycleAutomaticStop: true,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityStopped)
	if calls := provider.stopCalls.Load(); calls != 1 {
		t.Fatalf("explicit readiness enable stop calls=%d want 1", calls)
	}
}

func TestLifecycleAuditDecisionRejectsBlockedAndUnknownOutcomes(t *testing.T) {
	for _, kind := range []string{
		"backend-incarnation-superseded", "destructive-mutation-failed",
		"reconciliation-blocked", "resource-orphaned", "stop-deferred", "stop-unknown",
	} {
		if decision := lifecycleAuditDecision(kind); decision != "deny" {
			t.Fatalf("kind %q decision=%q want deny", kind, decision)
		}
	}
	for _, kind := range []string{"environment-stopped", "reconciliation-completed", "resource-registered"} {
		if decision := lifecycleAuditDecision(kind); decision != "allow" {
			t.Fatalf("kind %q decision=%q want allow", kind, decision)
		}
	}
}

func TestLifecycleResidualClassificationKeepsNonOwnerFailuresUnproved(t *testing.T) {
	if lifecycleResidualRequiresProviderProof(nil) {
		t.Fatal("empty residual set required provider proof")
	}
	if lifecycleResidualRequiresProviderProof([]string{"owner-requires-explicit-recovery"}) {
		t.Fatal("catalog-typed stale owner was treated as an unclassified provider residual")
	}
	for _, reasons := range [][]string{
		{"owner-live"},
		{"network-runtime-unproved"},
		{"owner-requires-explicit-recovery", "session-runtime-unproved"},
	} {
		if !lifecycleResidualRequiresProviderProof(reasons) {
			t.Fatalf("unclassified provider residual was accepted: %v", reasons)
		}
	}
}

func TestDaemonLifecycleStopEndpointUsesCoordinator(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityIdleGrace)
	code, body := daemonPost(t, d, lifecycleStopPath, `{"environmentId":"`+record.ID+`"}`, d.Token())
	if code != http.StatusOK {
		t.Fatalf("explicit lifecycle stop code=%d body=%s", code, body)
	}
	var response struct {
		Lifecycle lifecycle.Status `json:"lifecycle"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Lifecycle.Activity != lifecycle.ActivityStopped || provider.stopCalls.Load() != 1 {
		t.Fatalf("response=%+v stopCalls=%d", response, provider.stopCalls.Load())
	}
	loaded, err := (environment.Store{Root: store.Root}).Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "stopped" {
		t.Fatalf("environment status=%q", loaded.Status)
	}
}

func TestDaemonManagerStopRouteUsesLifecycleCoordinator(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityIdleGrace)
	code, body := daemonPost(t, d, "/api/v1/environment/stop/apply", `{"ids":["`+record.ID+`"]}`, d.Token())
	if code != http.StatusOK || !strings.Contains(string(body), `"errors":[]`) {
		t.Fatalf("manager stop code=%d body=%s", code, body)
	}
	if provider.stopCalls.Load() != 1 {
		t.Fatalf("manager route bypassed lifecycle stop: calls=%d", provider.stopCalls.Load())
	}
}

func TestDaemonBackgroundStopUsesLifecycleCoordinator(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleActivity(t, d, record.ID, lifecycle.ActivityIdleGrace)
	run, err := d.backgroundRun("environment-stop", []string{record.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.stopCalls.Load() != 1 {
		t.Fatalf("background stop bypassed lifecycle stop: calls=%d", provider.stopCalls.Load())
	}
}

func TestDaemonManagerCleanRouteRemovesLifecycleMetadata(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleReconciliation(t, d, record.ID)
	code, body := daemonPost(t, d, "/api/v1/environment/clean/apply", `{"ids":["`+record.ID+`"]}`, d.Token())
	if code != http.StatusOK || !strings.Contains(string(body), `"errors":[]`) {
		t.Fatalf("manager clean code=%d body=%s", code, body)
	}
	if provider.cleanupCalls.Load() != 1 {
		t.Fatalf("manager clean bypassed lifecycle backend factory: calls=%d", provider.cleanupCalls.Load())
	}
	if _, err := (environment.Store{Root: store.Root}).Load(record.ID); err == nil {
		t.Fatal("cleaned environment record remains")
	}
	if _, err := (lifecycle.JournalStore{Root: store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleaned environment lifecycle journal remains: %v", err)
	}
	if statuses := d.Status().Lifecycle; len(statuses) != 0 {
		t.Fatalf("cleaned environment remains in daemon status: %+v", statuses)
	}
}

func TestDaemonBackgroundCleanRemovesLifecycleMetadata(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleStopped, InstanceName: record.InstanceName,
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleReconciliation(t, d, record.ID)
	run, err := d.backgroundRun("environment-clean", []string{record.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := (lifecycle.JournalStore{Root: store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("background clean lifecycle journal remains: %v", err)
	}
}

func TestDaemonLifecycleMutationSerializesForceRemoveAndRecreate(t *testing.T) {
	for _, operation := range []string{"remove", "recreate"} {
		t.Run(operation, func(t *testing.T) {
			store, record := daemonLifecycleEnvironment(t)
			environmentStore := environment.Store{Root: store.Root}
			record.Status = "running"
			if err := environmentStore.Save(record); err != nil {
				t.Fatal(err)
			}
			provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
				State: backend.LifecycleRunning, InstanceName: record.InstanceName,
				BootID: "01234567-89ab-cdef-0123-456789abcdef",
			}}
			d, err := Start(Options{
				Store: store, LifecycleIdleGrace: time.Hour,
				LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Stop(context.Background())
			waitCtx, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelWait()
			if err := d.lifecycle.WaitReconciliation(waitCtx, record.ID); err != nil {
				t.Fatalf("wait for lifecycle reconciliation: %v", err)
			}
			body := `{"environmentId":"` + record.ID + `","operation":"` + operation + `","force":true}`
			code, response := daemonPost(t, d, lifecycleMutatePath, body, d.Token())
			if code != http.StatusOK {
				t.Fatalf("mutation code=%d body=%s", code, response)
			}
			if provider.stopCalls.Load() != 1 || provider.cleanupCalls.Load() != 1 {
				t.Fatalf("mutation bypassed daemon lifecycle provider: stop=%d cleanup=%d", provider.stopCalls.Load(), provider.cleanupCalls.Load())
			}
			loaded, loadErr := environmentStore.Load(record.ID)
			if operation == "remove" {
				if loadErr == nil {
					t.Fatalf("removed record load err=%v record=%+v", loadErr, loaded)
				}
			} else if loadErr != nil || loaded.ID != record.ID || loaded.Status != "ready" {
				t.Fatalf("recreated record=%+v err=%v", loaded, loadErr)
			}
			if _, err := (lifecycle.JournalStore{Root: store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("mutation left old lifecycle journal: %v", err)
			}
			if statuses := d.Status().Lifecycle; len(statuses) != 0 {
				t.Fatalf("mutation left old lifecycle status: %+v", statuses)
			}
		})
	}
}

func TestDaemonLifecycleForceMutationWaitsOutLiveSessionActivity(t *testing.T) {
	// A live run session holds a lifecycle registration handle. recreate
	// --force cancels the environment's sessions and waits for their handles
	// to finish instead of failing closed on first contact — the gate2
	// named-environment lane hit exactly that 409.
	store, record := daemonLifecycleEnvironment(t)
	environmentStore := environment.Store{Root: store.Root}
	record.Status = "running"
	if err := environmentStore.Save(record); err != nil {
		t.Fatal(err)
	}
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: bootID,
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWait()
	if err := d.lifecycle.WaitReconciliation(waitCtx, record.ID); err != nil {
		t.Fatalf("wait for lifecycle reconciliation: %v", err)
	}
	registration, err := d.lifecycle.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, SessionID: "ses_force_guard",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: record.InstanceName,
			BootID: bootID, ObservedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("begin guard attach: %v", err)
	}
	go func() {
		// The cancelled session finishes its cleanup shortly after the force
		// mutation starts waiting for the environment to quiesce.
		time.Sleep(400 * time.Millisecond)
		_ = registration.Finish(context.Background(), nil)
	}()
	body := `{"environmentId":"` + record.ID + `","operation":"recreate","force":true}`
	code, response := daemonPost(t, d, lifecycleMutatePath, body, d.Token())
	if code != http.StatusOK {
		t.Fatalf("force mutation with live-session handle code=%d body=%s", code, response)
	}
	loaded, loadErr := environmentStore.Load(record.ID)
	if loadErr != nil || loaded.ID != record.ID || loaded.Status != "ready" {
		t.Fatalf("recreated record=%+v err=%v", loaded, loadErr)
	}
}

func TestDaemonLifecycleMutationWithoutForceDoesNotPoisonRunningState(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	environmentStore := environment.Store{Root: store.Root}
	record.Status = "running"
	if err := environmentStore.Save(record); err != nil {
		t.Fatal(err)
	}
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) { return provider, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())
	waitForLifecycleReconciliation(t, d, record.ID)
	code, _ := daemonPost(t, d, lifecycleMutatePath, `{"environmentId":"`+record.ID+`","operation":"remove"}`, d.Token())
	if code != http.StatusConflict {
		t.Fatalf("running mutation without force code=%d", code)
	}
	statuses := d.Status().Lifecycle
	if len(statuses) != 1 || statuses[0].Activity != lifecycle.ActivityIdleGrace {
		t.Fatalf("preflight refusal poisoned lifecycle state: %+v", statuses)
	}
	if provider.stopCalls.Load() != 0 || provider.cleanupCalls.Load() != 0 {
		t.Fatalf("preflight refusal invoked provider: stop=%d cleanup=%d", provider.stopCalls.Load(), provider.cleanupCalls.Load())
	}
}

func daemonLifecycleEnvironment(t *testing.T) (profile.Store, environment.Record) {
	t.Helper()
	store := testStore(t)
	record, err := (environment.Store{Root: store.Root}).Create(environment.Spec{
		Name: "lifecycle", ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "lima",
		Mode:                environment.ModeWorkspaceBound,
		MachineIdentityID:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		BootConfigurationID: daemonTestBootConfigurationID,
		BoundWorkspace:      t.TempDir(), BoundGuestRoot: "/workspace", InstanceName: "hideout-lifecycle-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = "ready"
	if err := (environment.Store{Root: store.Root}).Save(record); err != nil {
		t.Fatal(err)
	}
	seedDaemonLifecycleJournal(t, store, record)
	return store, record
}

func seedDaemonLifecycleJournal(t *testing.T, store profile.Store, record environment.Record) {
	t.Helper()
	const bootID = "01234567-89ab-cdef-0123-456789abcdef"
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: store.Root}, DaemonID: "daemon-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, SessionID: "ses-seed",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: record.InstanceName,
			BootID: bootID, ObservedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), bootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), lifecycle.StateActive); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
}

var _ manager.EnvironmentLifecycleBackend = (*daemonLifecycleBackend)(nil)
