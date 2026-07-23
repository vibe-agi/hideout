package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

type disposableRecoveryBackend struct {
	cleanupCalls  int
	cleanupErr    error
	observations  []backend.LifecycleObservation
	wrongInstance string
}

func (provider *disposableRecoveryBackend) StopInstance(context.Context, string) error {
	return nil
}

func (provider *disposableRecoveryBackend) Cleanup(_ context.Context, session *backend.Session) error {
	provider.cleanupCalls++
	if session == nil || session.InstanceName == "" {
		return errors.New("cleanup received no exact instance")
	}
	return provider.cleanupErr
}

func (provider *disposableRecoveryBackend) ObserveLifecycle(_ context.Context, instanceName string) backend.LifecycleObservation {
	observation := backend.LifecycleObservation{
		State: backend.LifecycleAbsent, InstanceName: instanceName, ObservedAt: time.Now().UTC(),
	}
	if len(provider.observations) != 0 {
		observation = provider.observations[0]
		provider.observations = provider.observations[1:]
		if provider.wrongInstance != "" {
			observation.InstanceName = provider.wrongInstance
		} else {
			observation.InstanceName = instanceName
		}
		observation.ObservedAt = time.Now().UTC()
	}
	return observation
}

func TestRecoverDisposableEnvironmentRemovesExactBackendAndMetadataRecordLast(t *testing.T) {
	core, envStore, record, journalStore := disposableRecoveryFixture(t)
	gatewayCalls := 0
	core.disposableGatewayCloser = func(environmentID string) error {
		gatewayCalls++
		if environmentID != record.ID {
			return errors.New("gateway identity mismatch")
		}
		return nil
	}
	if err := envStore.PrepareRuntime(record.ID); err != nil {
		t.Fatal(err)
	}
	runtimeMarker := filepath.Join(envStore.RuntimeDir(record.ID), "tmp", "residue")
	if err := os.WriteFile(runtimeMarker, []byte("residue"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &disposableRecoveryBackend{observations: []backend.LifecycleObservation{
		{State: backend.LifecycleRunning, BootID: "01234567-89ab-cdef-0123-456789abcdef"},
		{State: backend.LifecycleAbsent},
		{State: backend.LifecycleAbsent},
	}}

	outcome, err := core.RecoverDisposableEnvironment(context.Background(), DisposableRecoveryRequest{
		EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: provider,
	})
	if err != nil {
		t.Fatalf("RecoverDisposableEnvironment: %v", err)
	}
	if outcome.Status != DisposableRecoveryRemoved || !outcome.RecordRemoved ||
		!outcome.JournalRemoved || !outcome.RuntimeRemoved ||
		!outcome.BackendCleanupInvoked || outcome.AbsenceObservations != 2 {
		t.Fatalf("outcome=%+v", outcome)
	}
	if provider.cleanupCalls != 1 || gatewayCalls != 1 {
		t.Fatalf("cleanupCalls=%d gatewayCalls=%d", provider.cleanupCalls, gatewayCalls)
	}
	if _, err := envStore.Load(record.ID); err == nil {
		t.Fatal("environment record survived successful recovery")
	}
	if _, err := journalStore.Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lifecycle journal survived successful recovery: %v", err)
	}
	if _, err := os.Stat(runtimeMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime residue survived successful recovery: %v", err)
	}
}

func TestRecoverDisposableEnvironmentSkipsDeleteWhenAlreadyAbsent(t *testing.T) {
	core, _, record, _ := disposableRecoveryFixture(t)
	provider := &disposableRecoveryBackend{observations: []backend.LifecycleObservation{
		{State: backend.LifecycleAbsent},
		{State: backend.LifecycleAbsent},
		{State: backend.LifecycleAbsent},
	}}
	outcome, err := core.RecoverDisposableEnvironment(context.Background(), DisposableRecoveryRequest{
		EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != DisposableRecoveryRemoved || outcome.BackendCleanupInvoked || provider.cleanupCalls != 0 {
		t.Fatalf("already-absent recovery=%+v cleanupCalls=%d", outcome, provider.cleanupCalls)
	}
}

func TestRecoverDisposableEnvironmentResumesDurableForwardPhases(t *testing.T) {
	for _, phase := range []string{
		lifecycle.DisposalStateBackendAbsent,
		lifecycle.DisposalStateMetadataCleaning,
	} {
		t.Run(phase, func(t *testing.T) {
			core, envStore, record, journalStore := disposableRecoveryFixture(t)
			seedDisposableRecoveryPhase(t, &core, record, journalStore, phase)
			provider := &disposableRecoveryBackend{observations: []backend.LifecycleObservation{
				{State: backend.LifecycleAbsent},
				{State: backend.LifecycleAbsent},
				{State: backend.LifecycleAbsent},
			}}
			outcome, err := core.RecoverDisposableEnvironment(context.Background(), DisposableRecoveryRequest{
				EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: provider,
			})
			if err != nil {
				t.Fatalf("resume %s: %v", phase, err)
			}
			if outcome.Status != DisposableRecoveryRemoved || provider.cleanupCalls != 0 ||
				outcome.BackendCleanupInvoked || outcome.AbsenceObservations != 2 {
				t.Fatalf("resume %s outcome=%+v cleanupCalls=%d", phase, outcome, provider.cleanupCalls)
			}
			if _, err := envStore.Load(record.ID); err == nil {
				t.Fatalf("resume %s retained record: %v", phase, err)
			}
			if _, err := journalStore.Load(record.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("resume %s retained journal: %v", phase, err)
			}
		})
	}
}

func TestRecoverDisposableEnvironmentDoesNotDeleteInstanceReappearingAfterAbsenceProof(t *testing.T) {
	for _, phase := range []string{
		lifecycle.DisposalStateBackendAbsent,
		lifecycle.DisposalStateMetadataCleaning,
	} {
		t.Run(phase, func(t *testing.T) {
			core, envStore, record, journalStore := disposableRecoveryFixture(t)
			seedDisposableRecoveryPhase(t, &core, record, journalStore, phase)
			provider := &disposableRecoveryBackend{observations: []backend.LifecycleObservation{{
				State: backend.LifecycleRunning, BootID: "01234567-89ab-cdef-0123-456789abcdef",
			}}}
			outcome, err := core.RecoverDisposableEnvironment(context.Background(), DisposableRecoveryRequest{
				EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: provider,
			})
			if err == nil || outcome.ReasonCode != lifecycle.DisposalReasonBackendAbsenceUnproved {
				t.Fatalf("reappeared %s instance outcome=%+v err=%v", phase, outcome, err)
			}
			if provider.cleanupCalls != 0 || outcome.BackendCleanupInvoked || outcome.RecordRemoved {
				t.Fatalf("reappeared %s instance was destructively handled: outcome=%+v calls=%d", phase, outcome, provider.cleanupCalls)
			}
			if _, err := envStore.Load(record.ID); err != nil {
				t.Fatalf("reappeared %s instance lost record: %v", phase, err)
			}
		})
	}
}

func TestRecoverDisposableEnvironmentRetainsBlockedIntentOnCleanupOrProofFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		provider     *disposableRecoveryBackend
		wantReason   string
		wantCleanups int
	}{
		{
			name: "cleanup failure",
			provider: &disposableRecoveryBackend{
				cleanupErr: errors.New("delete exploded"),
				observations: []backend.LifecycleObservation{{
					State: backend.LifecycleRunning, BootID: "01234567-89ab-cdef-0123-456789abcdef",
				}},
			},
			wantReason: "backend-cleanup-failed", wantCleanups: 1,
		},
		{
			name: "transient absence",
			provider: &disposableRecoveryBackend{observations: []backend.LifecycleObservation{
				{State: backend.LifecycleRunning, BootID: "01234567-89ab-cdef-0123-456789abcdef"},
				{State: backend.LifecycleAbsent},
				{State: backend.LifecycleRunning, BootID: "01234567-89ab-cdef-0123-456789abcdef"},
			}},
			wantReason: "backend-absence-unproved", wantCleanups: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, envStore, record, journalStore := disposableRecoveryFixture(t)
			outcome, err := core.RecoverDisposableEnvironment(context.Background(), DisposableRecoveryRequest{
				EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: tc.provider,
			})
			if err == nil {
				t.Fatal("unproved recovery returned success")
			}
			if outcome.Status != DisposableRecoveryCleanupRequired || outcome.ReasonCode != tc.wantReason ||
				outcome.RecordRemoved || outcome.JournalRemoved || tc.provider.cleanupCalls != tc.wantCleanups {
				t.Fatalf("outcome=%+v cleanupCalls=%d err=%v", outcome, tc.provider.cleanupCalls, err)
			}
			retained, loadErr := envStore.Load(record.ID)
			if loadErr != nil || !retained.Disposable || retained.Status != environment.StatusError {
				t.Fatalf("retained record=%+v err=%v", retained, loadErr)
			}
			journal, loadErr := journalStore.Load(record.ID)
			if loadErr != nil || journal.Disposal == nil ||
				journal.Disposal.State != lifecycle.DisposalStateBlocked ||
				journal.Disposal.ReasonCode != tc.wantReason {
				t.Fatalf("retained journal=%+v err=%v", journal, loadErr)
			}
		})
	}
}

func TestRecoverDisposableEnvironmentCancellationMakesNoDestructiveCall(t *testing.T) {
	core, _, record, _ := disposableRecoveryFixture(t)
	provider := &disposableRecoveryBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, err := core.RecoverDisposableEnvironment(ctx, DisposableRecoveryRequest{
		EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: provider,
	})
	if !errors.Is(err, context.Canceled) || provider.cleanupCalls != 0 ||
		outcome.BackendCleanupInvoked || outcome.RecordRemoved {
		t.Fatalf("outcome=%+v cleanupCalls=%d err=%v", outcome, provider.cleanupCalls, err)
	}
}

func TestRecoverDisposableEnvironmentRefusesUnauthorizedOrUnprovedMatrix(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*testing.T, *Core, environment.Store, *environment.Record)
		provider *disposableRecoveryBackend
	}{
		{
			name: "non-disposable",
			setup: func(t *testing.T, _ *Core, store environment.Store, record *environment.Record) {
				record.Disposable = false
				if err := store.Save(*record); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "name-only",
			setup: func(t *testing.T, _ *Core, store environment.Store, record *environment.Record) {
				record.Disposable = false
				record.Name = "rm-name-only"
				if err := store.Save(*record); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "status-only",
			setup: func(t *testing.T, _ *Core, store environment.Store, record *environment.Record) {
				record.Disposable = false
				record.Status = environment.StatusError
				if err := store.Save(*record); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "live-owner",
			setup: func(t *testing.T, _ *Core, store environment.Store, record *environment.Record) {
				now := time.Now().UTC()
				owner, err := runsession.AcquireOwner(store.OwnerRoot(record.ID), runsession.OwnerRecord{
					Schema: runsession.ActiveSessionSchema, SessionID: "ses_20260723T030000Z_0123456789abcdef",
					EnvironmentID: record.ID, Profile: record.Profile, Backend: record.Backend,
					WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
					State: runsession.OwnerStateRunning, TerminalMode: runsession.TerminalNone,
					StartedAt: now, UpdatedAt: now,
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = owner.Close() })
			},
		},
		{
			name: "unprovable-owner",
			setup: func(t *testing.T, _ *Core, store environment.Store, record *environment.Record) {
				if err := store.PrepareRuntimeRoot(record.ID); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store.OwnerRoot(record.ID), "ses_20260723T030001Z_0123456789abcdef"), []byte("not-a-directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unknown-observation",
			provider: &disposableRecoveryBackend{observations: []backend.LifecycleObservation{{
				State: backend.LifecycleUnknown, ReasonCode: "inventory-unavailable",
			}}},
		},
		{
			name: "mismatched-observation",
			provider: &disposableRecoveryBackend{
				wrongInstance: "hideout-foreign-instance",
				observations:  []backend.LifecycleObservation{{State: backend.LifecycleAbsent}},
			},
		},
		{
			name: "durable-identity-mismatch",
			setup: func(t *testing.T, core *Core, store environment.Store, record *environment.Record) {
				identity, err := environment.NewDisposableIdentity(*record)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := core.LifecycleDisposals.BeginDisposal(context.Background(), lifecycle.DisposalRequest{
					EnvironmentID: identity.EnvironmentID, Backend: identity.Backend,
					InstanceName: identity.InstanceName, RecordDigest: identity.Digest,
				}); err != nil {
					t.Fatal(err)
				}
				if err := core.LifecycleDisposals.BlockDisposal(context.Background(), identity.EnvironmentID, identity.Digest, lifecycle.DisposalReasonBackendObservationUnproved); err != nil {
					t.Fatal(err)
				}
				record.InstanceName += "-changed"
				if err := store.Save(*record); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, envStore, record, _ := disposableRecoveryFixture(t)
			if tc.setup != nil {
				tc.setup(t, &core, envStore, &record)
			}
			provider := tc.provider
			if provider == nil {
				provider = &disposableRecoveryBackend{}
			}
			outcome, err := core.RecoverDisposableEnvironment(context.Background(), DisposableRecoveryRequest{
				EnvironmentID: record.ID, Source: DisposableRecoverySourceRestart, Provider: provider,
			})
			if err == nil {
				t.Fatalf("unauthorized recovery succeeded: %+v", outcome)
			}
			if provider.cleanupCalls != 0 || outcome.BackendCleanupInvoked || outcome.RecordRemoved {
				t.Fatalf("destructive call escaped refusal: outcome=%+v calls=%d err=%v", outcome, provider.cleanupCalls, err)
			}
			if _, loadErr := envStore.Load(record.ID); loadErr != nil {
				t.Fatalf("refused record was removed: %v", loadErr)
			}
		})
	}
}

func disposableRecoveryFixture(t *testing.T) (Core, environment.Store, environment.Record, lifecycle.JournalStore) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	if err := os.Chmod(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	envStore := environment.Store{Root: store.Root}
	spec := dedicatedRunEnvironmentSpec(runtimeConfigurationTestProfile("default"), "lima", t.TempDir(), "/workspace", "disposable-recovery")
	spec.Disposable = true
	record, err := envStore.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	record.InstanceName = "hideout-default-" + strings.TrimPrefix(record.ID, "env_")
	if err := envStore.Save(record); err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: store.Root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-disposable-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	core.LifecycleDisposals = coordinator
	return core, envStore, record, journalStore
}

func seedDisposableRecoveryPhase(
	t *testing.T,
	core *Core,
	record environment.Record,
	journalStore lifecycle.JournalStore,
	phase string,
) {
	t.Helper()
	identity, err := environment.NewDisposableIdentity(record)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, ok := core.LifecycleDisposals.(*lifecycle.Coordinator)
	if !ok {
		t.Fatal("fixture lifecycle coordinator has unexpected type")
	}
	if _, err := coordinator.BeginDisposal(context.Background(), lifecycle.DisposalRequest{
		EnvironmentID: identity.EnvironmentID, Backend: identity.Backend,
		InstanceName: identity.InstanceName, RecordDigest: identity.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.AdvanceDisposal(
		context.Background(), identity.EnvironmentID, identity.Digest,
		lifecycle.DisposalStateBackendAbsent,
	); err != nil {
		t.Fatal(err)
	}
	if phase == lifecycle.DisposalStateMetadataCleaning {
		if err := coordinator.AdvanceDisposal(
			context.Background(), identity.EnvironmentID, identity.Digest,
			lifecycle.DisposalStateMetadataCleaning,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-disposable-resumed-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	core.LifecycleDisposals = resumed
}
