package manager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationImportVerificationUsesDurableFactsAndReplays(t *testing.T) {
	service, provider, adopted := migrationImportVerificationFixture(t)
	provider.verifyResponse = func(
		request backend.DestinationVerifyRequest,
		_ int,
	) (backend.DestinationProof, error) {
		return migrationDestinationProofFixture(request), nil
	}

	verified, proof, err := service.VerifyImportDestination(
		context.Background(), MigrationImportVerifyRequest{OperationID: adopted.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	verifyEffect, err := migrationOperationEffect(verified, MigrationEffectVerify)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Phase != MigrationPhaseVerifying ||
		verifyEffect.Status != MigrationEffectSucceeded || provider.verifyCalls != 1 ||
		proof.ProofDigest == "" || len(provider.verifyRequests) != 1 {
		t.Fatalf("verified=%+v proof=%+v calls=%d", verified, proof, provider.verifyCalls)
	}
	request := provider.verifyRequests[0]
	wantPolicies := []migration.IdentitySelection{
		{SourceRef: adopted.IdentityActions[0].SourceRef, Policy: adopted.IdentityActions[0].GuestPolicy},
		{SourceRef: adopted.IdentityActions[1].SourceRef, Policy: adopted.IdentityActions[1].GuestPolicy},
	}
	if !reflect.DeepEqual(request.ExpectedDisks, adopted.ExpectedDisks) {
		t.Fatalf("verification disks differ: got=%#v want=%#v",
			request.ExpectedDisks, adopted.ExpectedDisks)
	}
	if !reflect.DeepEqual(request.IdentityPolicies, wantPolicies) {
		t.Fatalf("verification policies differ: got=%#v want=%#v",
			request.IdentityPolicies, wantPolicies)
	}
	if len(request.AdoptionReceipts) != len(adopted.DestinationAdoption.Records) {
		t.Fatalf("verification receipt count=%d want=%d",
			len(request.AdoptionReceipts), len(adopted.DestinationAdoption.Records))
	}
	if len(request.AdoptionRequests) != len(adopted.DestinationAdoption.Records) {
		t.Fatalf("verification request count=%d want=%d",
			len(request.AdoptionRequests), len(adopted.DestinationAdoption.Records))
	}
	for index, receipt := range request.AdoptionReceipts {
		if !reflect.DeepEqual(
			request.AdoptionRequests[index],
			adopted.DestinationAdoption.Records[index].Request,
		) {
			t.Fatalf("adoption request[%d] differs", index)
		}
		if !reflect.DeepEqual(receipt, adopted.DestinationAdoption.Records[index].Receipt) {
			t.Fatalf("receipt[%d] differs", index)
		}
	}

	replayed, replayProof, err := service.VerifyImportDestination(
		context.Background(), MigrationImportVerifyRequest{OperationID: adopted.ID},
	)
	if err != nil || replayed.Revision != verified.Revision ||
		replayProof != proof || provider.verifyCalls != 1 {
		t.Fatalf("verification replay operation=%+v proof=%+v calls=%d err=%v",
			replayed, replayProof, provider.verifyCalls, err)
	}
}

func TestMigrationImportVerificationRejectsProviderBindingSubstitution(t *testing.T) {
	service, provider, adopted := migrationImportVerificationFixture(t)
	provider.verifyResponse = func(
		request backend.DestinationVerifyRequest,
		_ int,
	) (backend.DestinationProof, error) {
		proof := migrationDestinationProofFixture(request)
		proof.StageHandle = "stage_substitute1234"
		return proof, nil
	}

	failed, _, err := service.VerifyImportDestination(
		context.Background(), MigrationImportVerifyRequest{OperationID: adopted.ID},
	)
	if !errors.Is(err, backend.ErrMigrationProviderResponse) ||
		failed.Phase != MigrationPhaseRecoverableFailure || provider.verifyCalls != 1 {
		t.Fatalf("substituted verification operation=%+v calls=%d err=%v",
			failed, provider.verifyCalls, err)
	}
	verifyEffect, effectErr := migrationOperationEffect(failed, MigrationEffectVerify)
	if effectErr != nil || verifyEffect.Status != MigrationEffectRunning {
		t.Fatalf("substituted verification effect=%+v err=%v", verifyEffect, effectErr)
	}
}

func TestMigrationImportVerificationRejectsCapabilityDriftBeforeProvider(t *testing.T) {
	service, provider, adopted := migrationImportVerificationFixture(t)
	provider.capability.Revision = migration.Digest("sha256:" + strings.Repeat("d", 64))

	failed, _, err := service.VerifyImportDestination(
		context.Background(), MigrationImportVerifyRequest{OperationID: adopted.ID},
	)
	if !errors.Is(err, ErrMigrationPlanStale) ||
		failed.Phase != MigrationPhaseRecoverableFailure || provider.verifyCalls != 0 {
		t.Fatalf("capability drift operation=%+v calls=%d err=%v",
			failed, provider.verifyCalls, err)
	}
}

func TestMigrationImportRollbackCompensatesVerificationAdoptionAndStageInReverse(t *testing.T) {
	service, provider, adopted := migrationImportVerificationFixture(t)
	provider.verifyResponse = func(
		request backend.DestinationVerifyRequest,
		_ int,
	) (backend.DestinationProof, error) {
		return migrationDestinationProofFixture(request), nil
	}
	verified, _, err := service.VerifyImportDestination(
		context.Background(), MigrationImportVerifyRequest{OperationID: adopted.ID},
	)
	if err != nil {
		t.Fatal(err)
	}

	rolledBack, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: verified.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []MigrationEffectKind{
		MigrationEffectVerify, MigrationEffectAdopt, MigrationEffectStage,
	} {
		effect, effectErr := migrationOperationEffect(rolledBack, kind)
		if effectErr != nil || effect.Status != MigrationEffectCompensated {
			t.Fatalf("rollback effect kind=%s effect=%+v err=%v", kind, effect, effectErr)
		}
	}
	stageEffect, _ := migrationOperationEffect(rolledBack, MigrationEffectStage)
	if rolledBack.Phase != MigrationPhaseRolledBack || provider.rollbackCalls != 1 ||
		provider.rollbackRequest.Binding.EffectID != stageEffect.ID ||
		provider.rollbackRequest.StageHandle != rolledBack.DestinationStage.StageHandle {
		t.Fatalf("rolled back=%+v request=%+v calls=%d",
			rolledBack, provider.rollbackRequest, provider.rollbackCalls)
	}
	replayed, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: verified.ID},
	)
	if err != nil || replayed.Revision != rolledBack.Revision || provider.rollbackCalls != 1 {
		t.Fatalf("rollback replay operation=%+v calls=%d err=%v",
			replayed, provider.rollbackCalls, err)
	}
}

func TestMigrationImportRollbackResumesFromProviderFailureWithoutRepeatingLocalWork(t *testing.T) {
	service, provider, adopted := migrationImportVerificationFixture(t)
	provider.verifyResponse = func(
		request backend.DestinationVerifyRequest,
		_ int,
	) (backend.DestinationProof, error) {
		return migrationDestinationProofFixture(request), nil
	}
	verified, _, err := service.VerifyImportDestination(
		context.Background(), MigrationImportVerifyRequest{OperationID: adopted.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider.rollbackResponse = func(
		_ backend.DestinationRollbackRequest,
		call int,
	) error {
		if call == 1 {
			return errors.New("injected rollback interruption")
		}
		return nil
	}

	failed, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: verified.ID},
	)
	if err == nil || failed.Phase != MigrationPhaseRecoverableFailure {
		t.Fatalf("first rollback operation=%+v err=%v", failed, err)
	}
	verifyEffect, _ := migrationOperationEffect(failed, MigrationEffectVerify)
	adoptEffect, _ := migrationOperationEffect(failed, MigrationEffectAdopt)
	stageEffect, _ := migrationOperationEffect(failed, MigrationEffectStage)
	if verifyEffect.Status != MigrationEffectCompensated ||
		adoptEffect.Status != MigrationEffectCompensating ||
		stageEffect.Status != MigrationEffectCompensating {
		t.Fatalf("progressive rollback statuses verify=%s adopt=%s stage=%s",
			verifyEffect.Status, adoptEffect.Status, stageEffect.Status)
	}

	rolledBack, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: verified.ID},
	)
	if err != nil || rolledBack.Phase != MigrationPhaseRolledBack ||
		provider.rollbackCalls != 2 {
		t.Fatalf("resumed rollback operation=%+v calls=%d err=%v",
			rolledBack, provider.rollbackCalls, err)
	}
	verifyAfter, _ := migrationOperationEffect(rolledBack, MigrationEffectVerify)
	if verifyAfter.Status != MigrationEffectCompensated {
		t.Fatalf("resumed rollback repeated local verification compensation: %+v", verifyAfter)
	}
}

func migrationImportVerificationFixture(
	t *testing.T,
) (MigrationImportService, *managerMigrationProviderFixture, MigrationOperation) {
	t.Helper()
	service, provider, operation := migrationImportAdoptionFixture(t)
	provider.adoptResponse = func(
		request backend.DestinationAdoptionRequest,
		call int,
	) (backend.DestinationAdoption, error) {
		return migrationDestinationAdoptionFixture(t, request, call), nil
	}
	adopted, _, err := service.AdoptImportDestination(
		context.Background(), MigrationImportAdoptRequest{OperationID: operation.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, provider, adopted
}

func migrationDestinationProofFixture(
	request backend.DestinationVerifyRequest,
) backend.DestinationProof {
	return backend.DestinationProof{
		Binding: request.Binding, StageHandle: request.StageHandle,
		ProofDigest: migration.Digest("sha256:" + strings.Repeat("6", 64)),
		Stopped:     true, DigestsMatch: true, IdentityPolicySatisfied: true,
		TemporaryAuthorityRemoved: true, ImportedAuthorityAbsent: true,
	}
}
