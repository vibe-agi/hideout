package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationImportWorkerCrashCutRouting(t *testing.T) {
	crashErr := errors.New("injected response-loss crash cut")
	tests := []struct {
		name            string
		failStep        string
		wantFirst       []string
		wantRestartHead string
	}{
		{
			name: "stage receipt persisted before response loss", failStep: "materialize",
			wantFirst: []string{"load", "materialize"}, wantRestartHead: "adopt",
		},
		{
			name: "adoption receipt persisted before response loss", failStep: "adopt",
			wantFirst: []string{"load", "materialize", "adopt"}, wantRestartHead: "adopt",
		},
		{
			name: "verification receipt persisted before response loss", failStep: "verify",
			wantFirst:       []string{"load", "materialize", "adopt", "verify"},
			wantRestartHead: "adopt",
		},
		{
			name: "one way commit decision persisted before response loss", failStep: "commit",
			wantFirst:       []string{"load", "materialize", "adopt", "verify", "commit"},
			wantRestartHead: "commit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := manager.MigrationOperation{
				ID: "op_workercrashcut1", Kind: manager.MigrationOperationImport,
				Phase: manager.MigrationPhaseClaiming,
			}
			calls := make([]string, 0, 12)
			failed := false
			step := func(name string) error {
				calls = append(calls, name)
				if name == test.failStep && !failed {
					failed = true
					return crashErr
				}
				return nil
			}
			steps := migrationImportWorkerSteps{
				load: func(string) (manager.MigrationOperation, error) {
					calls = append(calls, "load")
					return operation, nil
				},
				materialize: func(
					context.Context,
					manager.MigrationImportMaterializeRequest,
				) (manager.MigrationOperation, error) {
					// FinishDestinationStage is durable before the response is
					// observable. A restart must therefore skip bundle replay.
					operation.Phase = manager.MigrationPhasePreparingSecrets
					operation.DestinationStage = &manager.MigrationDestinationStageState{}
					return operation, step("materialize")
				},
				adopt: func(
					context.Context,
					manager.MigrationImportAdoptRequest,
				) (manager.MigrationOperation, error) {
					operation.Phase = manager.MigrationPhaseVerifying
					return operation, step("adopt")
				},
				verify: func(
					context.Context,
					manager.MigrationImportVerifyRequest,
				) (manager.MigrationOperation, error) {
					operation.Phase = manager.MigrationPhaseVerifying
					return operation, step("verify")
				},
				commit: func(
					context.Context,
					manager.MigrationImportCommitRequest,
				) (manager.MigrationOperation, error) {
					decision := manager.MigrationCommitDecision{
						Value: manager.MigrationDecisionCommit,
					}
					operation.Decision = &decision
					if err := step("commit"); err != nil {
						operation.Phase = manager.MigrationPhaseRecoverableFailure
						return operation, err
					}
					operation.Phase = manager.MigrationPhaseComplete
					return operation, nil
				},
				rollback: func(
					context.Context,
					manager.MigrationImportRollbackRequest,
				) (manager.MigrationOperation, error) {
					calls = append(calls, "rollback")
					operation.Phase = manager.MigrationPhaseRolledBack
					return operation, nil
				},
			}
			workers := &migrationWorkerSet{steps: steps}
			request := manager.MigrationImportWorkerRequest{
				OperationID: operation.ID,
			}
			if err := workers.runImport(context.Background(), request); !errors.Is(err, crashErr) {
				t.Fatalf("first crash-cut result=%v calls=%v", err, calls)
			}
			if !slices.Equal(calls, test.wantFirst) {
				t.Fatalf("first crash-cut calls=%v want=%v", calls, test.wantFirst)
			}
			firstCount := len(calls)
			if err := workers.runImport(context.Background(), request); err != nil {
				t.Fatalf("restart result=%v calls=%v", err, calls)
			}
			restartCalls := calls[firstCount:]
			if len(restartCalls) < 2 || restartCalls[0] != "load" ||
				restartCalls[1] != test.wantRestartHead {
				t.Fatalf("restart routing=%v want load,%s", restartCalls, test.wantRestartHead)
			}
			if operation.Phase != manager.MigrationPhaseComplete {
				t.Fatalf("restart did not finish exact operation: %+v", operation)
			}
			if test.failStep == "materialize" && slices.Contains(restartCalls, "materialize") {
				t.Fatalf("restart replayed an authenticated completed stage: %v", restartCalls)
			}
			if test.failStep == "commit" && !slices.Equal(restartCalls, []string{"load", "commit"}) {
				t.Fatalf("committed restart crossed another effect: %v", restartCalls)
			}
		})
	}

	t.Run("rollback decision bypasses forward effects", func(t *testing.T) {
		decision := manager.MigrationCommitDecision{Value: manager.MigrationDecisionRollback}
		operation := manager.MigrationOperation{
			ID: "op_workerrollback1", Kind: manager.MigrationOperationImport,
			Phase: manager.MigrationPhaseRollingBack, Decision: &decision,
		}
		calls := []string{}
		workers := &migrationWorkerSet{steps: migrationImportWorkerSteps{
			load: func(string) (manager.MigrationOperation, error) {
				calls = append(calls, "load")
				return operation, nil
			},
			rollback: func(
				context.Context,
				manager.MigrationImportRollbackRequest,
			) (manager.MigrationOperation, error) {
				calls = append(calls, "rollback")
				operation.Phase = manager.MigrationPhaseRolledBack
				return operation, nil
			},
		}}
		if err := workers.runImport(context.Background(), manager.MigrationImportWorkerRequest{
			OperationID: operation.ID,
		}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(calls, []string{"load", "rollback"}) {
			t.Fatalf("rollback restart entered forward effects: %v", calls)
		}
	})
}

func TestMigrationStartupDispositionWaitsForSecretAfterStagedSelectedValue(t *testing.T) {
	operation := manager.MigrationOperation{
		Kind: manager.MigrationOperationImport, Phase: manager.MigrationPhasePreparingSecrets,
		DestinationStage: &manager.MigrationDestinationStageState{},
		SecretActions: []migration.SecretAction{{
			SourceRef: "secret_source01",
			Transfer:  migration.SecretSelectedValue, Decision: "import-value",
			ValueComponentID: "component_secret01",
		}},
	}
	if got := migrationStartupDispositionForOperation(operation); got != migrationStartupNeedsSecret {
		t.Fatalf("staged selected-secret disposition=%s", got)
	}
	operation.PreparedSecrets = []manager.MigrationPreparedSecret{{
		SourceRef: "secret_source01", DestinationRef: "local-proxy",
	}}
	if got := migrationStartupDispositionForOperation(operation); got != migrationStartupResumeImport {
		t.Fatalf("prepared selected-secret disposition=%s", got)
	}
}

func TestMigrationStartupDispositionIsDeterministicAcrossTerminalAndRecoveryStates(t *testing.T) {
	tests := []struct {
		name      string
		operation manager.MigrationOperation
		want      migrationStartupDisposition
	}{
		{
			name: "terminal evidence",
			operation: manager.MigrationOperation{
				Phase: manager.MigrationPhaseComplete,
			},
			want: migrationStartupPublishTerminal,
		},
		{
			name: "explicit recovery remains operator owned",
			operation: manager.MigrationOperation{
				Phase: manager.MigrationPhaseRecoverableFailure,
			},
			want: migrationStartupWaitForOperator,
		},
		{
			name: "cancel continues cleanup",
			operation: manager.MigrationOperation{
				Cancellation: &manager.MigrationCancellationDecision{},
			},
			want: migrationStartupResumeCancellation,
		},
		{
			name: "unstaged work needs new secret",
			operation: manager.MigrationOperation{
				Kind: manager.MigrationOperationImport,
			},
			want: migrationStartupNeedsSecret,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := migrationStartupDispositionForOperation(test.operation); got != test.want {
				t.Fatalf("disposition=%s want=%s", got, test.want)
			}
		})
	}
}
