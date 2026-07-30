package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
)

type stopResponseLossBackend struct {
	*daemonLifecycleBackend
	stopInvocations atomic.Int32
}

type cleanupResponseLossBackend struct {
	*daemonLifecycleBackend
}

type reconciliationSequenceBackend struct {
	observations []backend.LifecycleObservation
}

func (provider *reconciliationSequenceBackend) ObserveLifecycle(
	_ context.Context,
	instanceName string,
) backend.LifecycleObservation {
	observation := backend.LifecycleObservation{
		State: backend.LifecycleUnknown, InstanceName: instanceName,
		ReasonCode: "sequence-exhausted",
	}
	if len(provider.observations) != 0 {
		observation = provider.observations[0]
		provider.observations = provider.observations[1:]
		observation.InstanceName = instanceName
	}
	observation.ObservedAt = time.Now().UTC()
	return observation
}

func (*reconciliationSequenceBackend) StopInstance(context.Context, string) error {
	return nil
}

func (*reconciliationSequenceBackend) Cleanup(
	context.Context,
	*backend.Session,
) error {
	return nil
}

func TestLifecycleReconciliationRejectsTransientTerminalObservation(t *testing.T) {
	provider := &reconciliationSequenceBackend{
		observations: []backend.LifecycleObservation{
			{State: backend.LifecycleStopped},
			{
				State:  backend.LifecycleRunning,
				BootID: "01234567-89ab-cdef-0123-456789abcdef",
			},
		},
	}
	observation := observeLifecycleForReconciliation(
		context.Background(), provider, "hideout-transient-terminal",
	)
	if observation.State != backend.LifecycleUnknown ||
		observation.ReasonCode != "backend-terminal-observation-unstable" ||
		len(provider.observations) != 0 {
		t.Fatalf("transient terminal observation=%+v", observation)
	}
}

func (provider *stopResponseLossBackend) StopInstance(
	_ context.Context,
	instanceName string,
) error {
	provider.mu.Lock()
	provider.observation = backend.LifecycleObservation{
		State: backend.LifecycleStopped, InstanceName: instanceName,
	}
	provider.mu.Unlock()
	provider.stopInvocations.Add(1)
	return errors.New(
		"injected stop response loss with socks5://user:password@127.0.0.1",
	)
}

func (provider *cleanupResponseLossBackend) Cleanup(
	ctx context.Context,
	session *backend.Session,
) error {
	if err := provider.daemonLifecycleBackend.Cleanup(
		ctx,
		session,
	); err != nil {
		return err
	}
	return errors.New(
		"injected cleanup response loss with socks5://user:password@127.0.0.1",
	)
}

func TestDaemonStartupReconcilesLostStopResponseFromStableObservationWithoutReplay(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &stopResponseLossBackend{
		daemonLifecycleBackend: &daemonLifecycleBackend{
			observation: backend.LifecycleObservation{
				State:        backend.LifecycleRunning,
				InstanceName: record.InstanceName,
				BootID:       "01234567-89ab-cdef-0123-456789abcdef",
			},
		},
	}
	start := func() *Daemon {
		d, err := Start(Options{
			Store: store, LifecycleIdleGrace: time.Hour,
			LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
				return provider, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	before := start()
	waitForLifecycleReconciliation(t, before, record.ID)
	if _, err := before.lifecycle.StopExplicit(
		context.Background(), record.ID,
	); err == nil {
		t.Fatal("lost stop response was accepted without recovery")
	}
	if provider.stopInvocations.Load() != 1 {
		t.Fatalf("stop invocations=%d want 1", provider.stopInvocations.Load())
	}
	if err := before.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	journal, err := (lifecycle.JournalStore{Root: store.Root}).Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "user:password") {
		t.Fatalf("raw stop provider error persisted: %s", encoded)
	}

	after := start()
	defer func() {
		if err := after.Stop(context.Background()); err != nil {
			t.Errorf("stop restarted daemon: %v", err)
		}
	}()
	waitForLifecycleReconciliation(t, after, record.ID)
	statuses := after.lifecycle.Snapshot()
	if len(statuses) != 1 ||
		statuses[0].EnvironmentID != record.ID ||
		statuses[0].Activity != lifecycle.ActivityStopped {
		t.Fatalf("reconciled stop status=%+v", statuses)
	}
	if provider.stopInvocations.Load() != 1 {
		t.Fatalf(
			"startup reconciliation replayed stop: invocations=%d",
			provider.stopInvocations.Load(),
		)
	}
}

func TestDaemonEnvironmentOperationResponseLossRecoversWithoutProviderReplay(
	t *testing.T,
) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &stopResponseLossBackend{
		daemonLifecycleBackend: &daemonLifecycleBackend{
			observation: backend.LifecycleObservation{
				State:        backend.LifecycleRunning,
				InstanceName: record.InstanceName,
				BootID:       "01234567-89ab-cdef-0123-456789abcdef",
			},
		},
	}
	start := func() *Daemon {
		daemon, err := Start(Options{
			Store: store, LifecycleIdleGrace: time.Hour,
			LifecycleBackend: func(
				environment.Record,
			) (manager.EnvironmentLifecycleBackend, error) {
				return provider, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return daemon
	}

	before := start()
	waitForLifecycleReconciliation(t, before, record.ID)
	code, body := daemonPost(
		t,
		before,
		"/api/v1/environment/stop/plan",
		`{"ids":["`+record.ID+`"]}`,
		before.Token(),
	)
	if code != 200 {
		t.Fatalf("plan code=%d body=%s", code, body)
	}
	var planned struct {
		Data   manager.EnvironmentActionPlan `json:"data"`
		Errors []string                      `json:"errors"`
	}
	if err := json.Unmarshal(body, &planned); err != nil {
		t.Fatal(err)
	}
	if len(planned.Errors) != 0 ||
		planned.Data.OperationID == "" ||
		planned.Data.PlanDigest == "" {
		t.Fatalf("planned response=%+v", planned)
	}
	applyBody, err := json.Marshal(manager.EnvironmentActionAPIRequest{
		IDs:         []string{record.ID},
		OperationID: planned.Data.OperationID,
		PlanDigest:  planned.Data.PlanDigest,
		Confirmed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, body = daemonPost(
		t,
		before,
		"/api/v1/environment/stop/apply",
		string(applyBody),
		before.Token(),
	)
	if code != 200 {
		t.Fatalf("apply code=%d body=%s", code, body)
	}
	var applied struct {
		Data   manager.EnvironmentActionResult `json:"data"`
		Errors []string                        `json:"errors"`
	}
	if err := json.Unmarshal(body, &applied); err != nil {
		t.Fatal(err)
	}
	if len(applied.Errors) != 1 ||
		applied.Data.Operation == nil ||
		applied.Data.Operation.Phase != manager.OperationRecoveryRequired ||
		applied.Data.Operation.Recovery.Code !=
			"lifecycle-terminal-unproved" ||
		provider.stopInvocations.Load() != 1 {
		t.Fatalf(
			"applied=%+v stopInvocations=%d",
			applied,
			provider.stopInvocations.Load(),
		)
	}
	if strings.Contains(string(body), "user:password") {
		t.Fatalf("API leaked provider response: %s", body)
	}
	if err := before.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	after := start()
	defer func() {
		if err := after.Stop(context.Background()); err != nil {
			t.Errorf("stop restarted daemon: %v", err)
		}
	}()
	operationStore := manager.OperationStore{Root: store.Root}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		operation, loadErr := operationStore.Load(
			planned.Data.OperationID,
		)
		environmentRecord, recordErr := (environment.Store{
			Root: store.Root,
		}).Load(record.ID)
		if loadErr == nil &&
			recordErr == nil &&
			operation.Phase == manager.OperationSucceeded &&
			environmentRecord.Status == environment.StatusStopped {
			if provider.stopInvocations.Load() != 1 {
				t.Fatalf(
					"provider replayed after restart: %d",
					provider.stopInvocations.Load(),
				)
			}
			if len(operation.Effects) != 1 ||
				operation.Effects[0].Status != manager.EffectSucceeded ||
				len(operation.Effects[0].Evidence) < 3 {
				t.Fatalf("terminal operation lacks proof: %+v", operation)
			}
			data, err := os.ReadFile(
				operationStore.OperationPath(operation.ID),
			)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "user:password") {
				t.Fatalf("operation ledger leaked provider response: %s", data)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	operation, _ := operationStore.Load(planned.Data.OperationID)
	t.Fatalf(
		"operation did not converge: %+v stopInvocations=%d",
		operation,
		provider.stopInvocations.Load(),
	)
}

func TestDaemonEnvironmentDeleteAPIRequiresReviewedForceOperation(
	t *testing.T,
) {
	store, record := daemonLifecycleEnvironment(t)
	record.Status = environment.StatusRunning
	if err := (environment.Store{Root: store.Root}).Save(record); err != nil {
		t.Fatal(err)
	}
	provider := &daemonLifecycleBackend{
		observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: record.InstanceName,
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
	}
	daemon, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(
			environment.Record,
		) (manager.EnvironmentLifecycleBackend, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := daemon.Stop(context.Background()); err != nil {
			t.Errorf("stop daemon: %v", err)
		}
	}()
	waitForLifecycleReconciliation(t, daemon, record.ID)

	code, body := daemonPost(
		t,
		daemon,
		"/api/v1/environment/delete/plan",
		`{"ids":["`+record.ID+`"],"force":true}`,
		daemon.Token(),
	)
	if code != 200 {
		t.Fatalf("delete plan code=%d body=%s", code, body)
	}
	var planned struct {
		Data   manager.EnvironmentActionPlan `json:"data"`
		Errors []string                      `json:"errors"`
	}
	if err := json.Unmarshal(body, &planned); err != nil {
		t.Fatal(err)
	}
	if len(planned.Errors) != 0 ||
		planned.Data.Action != manager.EnvironmentActionDelete ||
		!planned.Data.Force ||
		planned.Data.OperationID == "" ||
		planned.Data.PlanDigest == "" {
		t.Fatalf("delete plan=%+v", planned)
	}

	mismatched, err := json.Marshal(manager.EnvironmentActionAPIRequest{
		IDs:         []string{record.ID},
		OperationID: planned.Data.OperationID,
		PlanDigest:  planned.Data.PlanDigest,
		Confirmed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body = daemonPost(
		t,
		daemon,
		"/api/v1/environment/delete/apply",
		string(mismatched),
		daemon.Token(),
	)
	if !strings.Contains(
		string(body),
		manager.ErrOperationMismatch.Error(),
	) ||
		provider.stopCalls.Load() != 0 ||
		provider.cleanupCalls.Load() != 0 {
		t.Fatalf(
			"force mismatch body=%s stop=%d cleanup=%d",
			body,
			provider.stopCalls.Load(),
			provider.cleanupCalls.Load(),
		)
	}

	confirmed, err := json.Marshal(manager.EnvironmentActionAPIRequest{
		IDs: []string{record.ID}, Force: true,
		OperationID: planned.Data.OperationID,
		PlanDigest:  planned.Data.PlanDigest,
		Confirmed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, body = daemonPost(
		t,
		daemon,
		"/api/v1/environment/delete/apply",
		string(confirmed),
		daemon.Token(),
	)
	if code != 200 {
		t.Fatalf("delete apply code=%d body=%s", code, body)
	}
	var applied struct {
		Data   manager.EnvironmentActionResult `json:"data"`
		Errors []string                        `json:"errors"`
	}
	if err := json.Unmarshal(body, &applied); err != nil {
		t.Fatal(err)
	}
	if len(applied.Errors) != 0 ||
		applied.Data.Operation == nil ||
		applied.Data.Operation.Phase != manager.OperationSucceeded ||
		applied.Data.Operation.Kind != "environment.delete" ||
		provider.stopCalls.Load() != 1 ||
		provider.cleanupCalls.Load() != 1 {
		t.Fatalf(
			"delete result=%+v stop=%d cleanup=%d",
			applied,
			provider.stopCalls.Load(),
			provider.cleanupCalls.Load(),
		)
	}
	if _, err := (environment.Store{Root: store.Root}).Load(
		record.ID,
	); err == nil {
		t.Fatalf("deleted environment record remains: %v", err)
	}
}

func TestDaemonDeleteOperationRecoversCleanupResponseLossWithoutReplay(
	t *testing.T,
) {
	store, record := daemonLifecycleEnvironment(t)
	provider := &cleanupResponseLossBackend{
		daemonLifecycleBackend: &daemonLifecycleBackend{
			observation: backend.LifecycleObservation{
				State: backend.LifecycleRunning, InstanceName: record.InstanceName,
				BootID: "01234567-89ab-cdef-0123-456789abcdef",
			},
		},
	}
	start := func() *Daemon {
		daemon, err := Start(Options{
			Store: store, LifecycleIdleGrace: time.Hour,
			LifecycleBackend: func(
				environment.Record,
			) (manager.EnvironmentLifecycleBackend, error) {
				return provider, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return daemon
	}
	before := start()
	waitForLifecycleReconciliation(t, before, record.ID)
	_, body := daemonPost(
		t,
		before,
		"/api/v1/environment/delete/plan",
		`{"ids":["`+record.ID+`"]}`,
		before.Token(),
	)
	var planned struct {
		Data   manager.EnvironmentActionPlan `json:"data"`
		Errors []string                      `json:"errors"`
	}
	if err := json.Unmarshal(body, &planned); err != nil {
		t.Fatal(err)
	}
	if len(planned.Errors) != 0 ||
		planned.Data.OperationID == "" {
		t.Fatalf("delete plan=%+v body=%s", planned, body)
	}
	payload, err := json.Marshal(manager.EnvironmentActionAPIRequest{
		IDs:         []string{record.ID},
		OperationID: planned.Data.OperationID,
		PlanDigest:  planned.Data.PlanDigest,
		Confirmed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body = daemonPost(
		t,
		before,
		"/api/v1/environment/delete/apply",
		string(payload),
		before.Token(),
	)
	var applied struct {
		Data   manager.EnvironmentActionResult `json:"data"`
		Errors []string                        `json:"errors"`
	}
	if err := json.Unmarshal(body, &applied); err != nil {
		t.Fatal(err)
	}
	if len(applied.Errors) != 1 ||
		applied.Data.Operation == nil ||
		applied.Data.Operation.Phase != manager.OperationRecoveryRequired ||
		provider.cleanupCalls.Load() != 1 ||
		strings.Contains(string(body), "user:password") {
		t.Fatalf(
			"delete response-loss result=%+v cleanup=%d body=%s",
			applied,
			provider.cleanupCalls.Load(),
			body,
		)
	}
	if err := before.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	after := start()
	defer func() {
		if err := after.Stop(context.Background()); err != nil {
			t.Errorf("stop restarted daemon: %v", err)
		}
	}()
	operationStore := manager.OperationStore{Root: store.Root}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		operation, operationErr := operationStore.Load(
			planned.Data.OperationID,
		)
		_, recordErr := (environment.Store{Root: store.Root}).Load(
			record.ID,
		)
		if operationErr == nil &&
			recordErr != nil &&
			operation.Phase == manager.OperationSucceeded {
			if provider.cleanupCalls.Load() != 1 {
				t.Fatalf(
					"cleanup replayed after restart: %d",
					provider.cleanupCalls.Load(),
				)
			}
			if operation.Result == nil ||
				operation.Result.Code != "environment-deleted" ||
				len(operation.Effects) != 1 ||
				len(operation.Effects[0].Evidence) < 4 {
				t.Fatalf("delete proof is incomplete: %+v", operation)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	operation, _ := operationStore.Load(planned.Data.OperationID)
	t.Fatalf(
		"delete operation did not converge: %+v cleanup=%d",
		operation,
		provider.cleanupCalls.Load(),
	)
}

func TestDaemonStartupResumesExplicitRemovalAfterActivityFailureWithoutDuplicateBackendCleanup(t *testing.T) {
	for _, test := range []struct {
		name      string
		authority string
		source    string
	}{
		{
			name:      "clean",
			authority: lifecycle.DisposalAuthorityEnvironmentClean,
			source:    manager.EnvironmentCleanRecoverySourceExplicit,
		},
		{
			name:      "delete",
			authority: lifecycle.DisposalAuthorityEnvironmentDelete,
			source:    manager.EnvironmentDeleteRecoverySourceExplicit,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, record := daemonLifecycleEnvironment(t)
			journalStore := lifecycle.JournalStore{Root: store.Root}
			coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
				Store: journalStore, DaemonID: "daemon-removal-before-crash",
				IdleGrace: time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			core := manager.New(store)
			core.LifecycleDisposals = coordinator
			identity, err := environment.NewRemovalIdentity(record)
			if err != nil {
				t.Fatal(err)
			}
			provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
				State: backend.LifecycleRunning, InstanceName: record.InstanceName,
				BootID: "01234567-89ab-cdef-0123-456789abcdef",
			}}
			outcome, err := core.RecoverEnvironmentClean(
				context.Background(),
				manager.EnvironmentCleanRecoveryRequest{
					Identity: identity, Authority: test.authority,
					Source: test.source, Provider: provider,
					ActivityCleanup: func(context.Context, environment.Record) error {
						return errors.New("injected activity cleanup response loss")
					},
				},
			)
			if err == nil ||
				outcome.ReasonCode != lifecycle.DisposalReasonActivityCleanupFailed ||
				provider.cleanupCalls.Load() != 1 {
				t.Fatalf(
					"pre-crash outcome=%+v cleanupCalls=%d err=%v",
					outcome, provider.cleanupCalls.Load(), err,
				)
			}
			journal, err := journalStore.Load(record.ID)
			if err != nil || journal.Disposal == nil ||
				journal.Disposal.Authority != test.authority {
				t.Fatalf("durable removal intent=%+v err=%v", journal.Disposal, err)
			}
			if err := coordinator.Close(); err != nil {
				t.Fatal(err)
			}

			d, err := Start(Options{
				Store: store, LifecycleIdleGrace: time.Hour,
				LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
					return provider, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := d.Stop(context.Background()); err != nil {
					t.Errorf("stop daemon: %v", err)
				}
			}()
			waitForDisposableRecordsRemoved(t, store, []environment.Record{record})
			if provider.cleanupCalls.Load() != 1 {
				t.Fatalf(
					"startup recovery repeated backend cleanup: calls=%d",
					provider.cleanupCalls.Load(),
				)
			}
		})
	}
}

type failingJournalCompletion struct {
	lifecycle.DisposalCoordinator
}

func (failingJournalCompletion) CompleteDisposalMetadata(
	context.Context,
	string,
	string,
) error {
	return errors.New("injected journal completion response loss")
}

func TestDaemonStartupCompletesCleanJournalAfterRecordRemovalWithoutBackendReplay(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	journalStore := lifecycle.JournalStore{Root: store.Root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-clean-record-removed",
		IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	core := manager.New(store)
	core.LifecycleDisposals = failingJournalCompletion{
		DisposalCoordinator: coordinator,
	}
	identity, err := environment.NewRemovalIdentity(record)
	if err != nil {
		t.Fatal(err)
	}
	provider := &daemonLifecycleBackend{observation: backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	outcome, err := core.RecoverEnvironmentClean(
		context.Background(),
		manager.EnvironmentCleanRecoveryRequest{
			Identity: identity,
			Source:   manager.EnvironmentCleanRecoverySourceExplicit,
			Provider: provider,
			ActivityCleanup: func(context.Context, environment.Record) error {
				return nil
			},
		},
	)
	if err == nil ||
		outcome.ReasonCode != lifecycle.DisposalReasonJournalRemovalFailed ||
		!outcome.RecordRemoved || outcome.JournalRemoved ||
		provider.cleanupCalls.Load() != 1 {
		t.Fatalf(
			"response-loss outcome=%+v cleanupCalls=%d err=%v",
			outcome, provider.cleanupCalls.Load(), err,
		)
	}
	if _, err := (environment.Store{Root: store.Root}).Load(record.ID); err == nil {
		t.Fatal("record survived metadata-cleaning checkpoint")
	}
	journal, err := journalStore.Load(record.ID)
	if err != nil || journal.Disposal == nil ||
		journal.Disposal.State != lifecycle.DisposalStateMetadataCleaning ||
		journal.Disposal.Authority != lifecycle.DisposalAuthorityEnvironmentClean {
		t.Fatalf("retained recovery journal=%+v err=%v", journal, err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Start(Options{
		Store: store, LifecycleIdleGrace: time.Hour,
		LifecycleBackend: func(environment.Record) (manager.EnvironmentLifecycleBackend, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Stop(context.Background()); err != nil {
			t.Errorf("stop daemon: %v", err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, loadErr := journalStore.Load(record.ID)
		if errors.Is(loadErr, os.ErrNotExist) {
			if provider.cleanupCalls.Load() != 1 {
				t.Fatalf(
					"missing-record recovery repeated backend cleanup: calls=%d",
					provider.cleanupCalls.Load(),
				)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("missing-record clean journal did not converge")
}
