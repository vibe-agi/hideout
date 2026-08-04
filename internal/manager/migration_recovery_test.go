package manager

import "testing"

func TestMigrationImportRecoverableFailureCanResumeOrCancelRollback(t *testing.T) {
	operation := MigrationOperation{
		Kind: MigrationOperationImport, Phase: MigrationPhaseRecoverableFailure,
	}
	if recovery := migrationRecoveryForState(operation); recovery.Action != MigrationRecoveryResume {
		t.Fatalf("uncommitted import recovery=%+v", recovery)
	}
	operation.Cancellation = &MigrationCancellationDecision{}
	if recovery := migrationRecoveryForState(operation); recovery.Action != MigrationRecoveryRollback {
		t.Fatalf("cancelled import recovery=%+v", recovery)
	}
	operation.Cancellation = nil
	operation.Decision = &MigrationCommitDecision{Value: MigrationDecisionCommit}
	if recovery := migrationRecoveryForState(operation); recovery.Action != MigrationRecoveryFinish {
		t.Fatalf("committed import recovery=%+v", recovery)
	}
}
