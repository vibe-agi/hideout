package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationImportAdoptionPersistsImportTimePoliciesAndReplays(t *testing.T) {
	service, provider, operation := migrationImportAdoptionFixture(t)
	provider.adoptResponse = func(
		request backend.DestinationAdoptionRequest,
		call int,
	) (backend.DestinationAdoption, error) {
		return migrationDestinationAdoptionFixture(t, request, call), nil
	}

	adopted, responses, err := service.AdoptImportDestination(
		context.Background(), MigrationImportAdoptRequest{OperationID: operation.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	adoptEffect, err := migrationOperationEffect(adopted, MigrationEffectAdopt)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Phase != MigrationPhaseVerifying ||
		adoptEffect.Status != MigrationEffectSucceeded ||
		adopted.DestinationAdoption == nil || len(adopted.DestinationAdoption.Records) != 2 ||
		len(responses) != 2 || provider.adoptCalls != 2 {
		t.Fatalf("adopted operation=%+v responses=%d calls=%d", adopted, len(responses), provider.adoptCalls)
	}
	for index, record := range adopted.DestinationAdoption.Records {
		if record.EnvironmentRef != adopted.IdentityActions[index].SourceRef ||
			record.Request.Policy != adopted.IdentityActions[index].GuestPolicy ||
			record.Request.OperationID != migration.OpaqueID(adopted.ID) ||
			!record.Request.SourceIdentity.Equal(adopted.SourceGuestIdentities[index].Evidence) ||
			record.Request.Helper != *adopted.AdoptionHelper ||
			record.Receipt.MatchesRequest(record.Request) != nil ||
			!record.Stopped || !record.TemporaryAuthorityRemoved {
			t.Fatalf("adoption record[%d]=%+v", index, record)
		}
	}
	if adopted.DestinationAdoption.Records[0].Receipt.PostIdentity.Equal(
		adopted.SourceGuestIdentities[0].Evidence,
	) {
		t.Fatal("Safe Clone preserved the source guest identity")
	}
	if !adopted.DestinationAdoption.Records[1].Receipt.PostIdentity.Equal(
		adopted.SourceGuestIdentities[1].Evidence,
	) {
		t.Fatal("Exact Guest Restore changed the source guest identity")
	}

	replayed, replayResponses, err := service.AdoptImportDestination(
		context.Background(), MigrationImportAdoptRequest{OperationID: operation.ID},
	)
	if err != nil || replayed.Revision != adopted.Revision || len(replayResponses) != 0 ||
		provider.adoptCalls != 2 {
		t.Fatalf("adoption replay operation=%+v responses=%d calls=%d err=%v",
			replayed, len(replayResponses), provider.adoptCalls, err)
	}
}

func TestMigrationImportAdoptionRejectsProviderPolicySubstitution(t *testing.T) {
	service, provider, operation := migrationImportAdoptionFixture(t)
	provider.adoptResponse = func(
		request backend.DestinationAdoptionRequest,
		call int,
	) (backend.DestinationAdoption, error) {
		if call == 1 {
			request.Policy = migration.GuestIdentityExactRestore
		}
		return migrationDestinationAdoptionFixture(t, request, call), nil
	}

	failed, _, err := service.AdoptImportDestination(
		context.Background(), MigrationImportAdoptRequest{OperationID: operation.ID},
	)
	if !errors.Is(err, backend.ErrMigrationProviderResponse) ||
		failed.Phase != MigrationPhaseRecoverableFailure ||
		failed.DestinationAdoption != nil || provider.adoptCalls != 1 {
		t.Fatalf("substituted adoption operation=%+v calls=%d err=%v",
			failed, provider.adoptCalls, err)
	}
	adoptEffect, effectErr := migrationOperationEffect(failed, MigrationEffectAdopt)
	if effectErr != nil || adoptEffect.Status != MigrationEffectRunning {
		t.Fatalf("substituted adoption effect=%+v err=%v", adoptEffect, effectErr)
	}
}

func migrationImportAdoptionFixture(
	t *testing.T,
) (MigrationImportService, *managerMigrationProviderFixture, MigrationOperation) {
	t.Helper()
	root := t.TempDir()
	store := MigrationStore{Root: root}
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AcquireClaims(operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(operation.ID, MigrationPhaseMaterializing, nil); err != nil {
		t.Fatal(err)
	}
	stageEffect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginEffect(operation.ID, stageEffect.ID, stageEffect.Provider); err != nil {
		t.Fatal(err)
	}
	stage := MigrationDestinationStageState{
		StageHandle: migrationImportStageHandle(operation.ID),
		ObjectHandles: []migration.OpaqueID{
			"backend_clone1234", "backend_clone5678",
		},
		Checkpoints: []MigrationDestinationStageCheckpoint{
			{
				ComponentID: "component_disk0001", NextOffset: 4096,
				ContentDigest: migration.Digest("sha256:" + strings.Repeat("a", 64)),
			},
			{
				ComponentID: "component_disk0002", NextOffset: 8192,
				ContentDigest: migration.Digest("sha256:" + strings.Repeat("b", 64)),
			},
		},
		Profiles:       migrationMaterializedProfilesFixture(),
		EvidenceDigest: migration.Digest("sha256:" + strings.Repeat("c", 64)),
	}
	if _, err := store.FinishDestinationStage(
		operation.ID, stageEffect.ID, stageEffect.Provider, stage,
		[]MigrationEffectEvidence{{
			Code: "migration.import.stage_verified", OpaqueRef: stage.StageHandle,
			Digest:     stage.EvidenceDigest,
			Count:      uint64(len(stage.Checkpoints) + len(stage.Profiles)),
			ObservedAt: time.Now().UTC(),
		}},
	); err != nil {
		t.Fatal(err)
	}
	operation, err = store.TransitionPhase(
		operation.ID, MigrationPhasePreparingSecrets, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := newManagerMigrationProviderFixture()
	provider.capability.Revision = operation.CapabilityRevision
	provider.capability.AdoptionHelper = &backend.MigrationHelperCapability{
		PackageID: operation.AdoptionHelper.PackageID,
		Version:   operation.AdoptionHelper.Version, GuestArchitecture: "linux/arm64",
		Digest: operation.AdoptionHelper.SHA256,
	}
	service := MigrationImportService{MigrationService: MigrationService{
		Store: store, Environments: environment.Store{Root: root}, Import: provider,
	}}
	return service, provider, operation
}

func migrationDestinationAdoptionFixture(
	t *testing.T,
	fixed backend.DestinationAdoptionRequest,
	call int,
) backend.DestinationAdoption {
	t.Helper()
	actions := []string{
		migration.AdoptionActionPreserveIdentity,
		migration.AdoptionActionInstallSSHKeys,
	}
	post := fixed.SourceIdentity
	if fixed.Policy == migration.GuestIdentitySafeClone {
		actions = []string{
			migration.AdoptionActionResetMachineID,
			migration.AdoptionActionResetSSHHostKeys,
			migration.AdoptionActionInstallSSHKeys,
		}
		machineDigit := fmt.Sprintf("%x", call+10)
		sshDigit := fmt.Sprintf("%x", call+12)
		post = migration.GuestIdentityEvidence{
			MachineIDDigest: migration.Digest("sha256:" + strings.Repeat(machineDigit, 64)),
			SSHHostKeyDigests: []migration.Digest{
				migration.Digest("sha256:" + strings.Repeat(sshDigit, 64)),
			},
		}
	}
	guestRequest := migration.AdoptionRequest{
		Schema: migration.AdoptionRequestSchema, OperationID: fixed.Binding.OperationID,
		EnvironmentRef: fixed.EnvironmentRef,
		RequestNonce:   migration.OpaqueID(fmt.Sprintf("nonce_request_%04d", call)),
		ReceiptNonce:   migration.OpaqueID(fmt.Sprintf("nonce_receipt_%04d", call)),
		Policy:         fixed.Policy, SourceIdentity: fixed.SourceIdentity,
		DestinationSSHUser: "developer",
		DestinationSSHKeys: []string{
			fmt.Sprintf("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture%d", call),
		},
		PermittedActions: actions, Helper: fixed.Helper,
	}
	results := make([]migration.AdoptionActionResult, len(actions))
	for index, action := range actions {
		results[index] = migration.AdoptionActionResult{
			Action: action, Status: migration.AdoptionActionStatusCompleted,
		}
	}
	receipt := migration.AdoptionReceipt{
		Schema: migration.AdoptionReceiptSchema, OperationID: guestRequest.OperationID,
		EnvironmentRef: guestRequest.EnvironmentRef,
		RequestNonce:   guestRequest.RequestNonce, ReceiptNonce: guestRequest.ReceiptNonce,
		Policy: guestRequest.Policy, Helper: guestRequest.Helper, ActionResults: results,
		PostIdentity: &post, Status: migration.AdoptionReceiptStatusCompleted,
		CompletionMarker: true,
	}
	adoption := backend.DestinationAdoption{
		Binding: fixed.Binding, StageHandle: fixed.StageHandle,
		Request: guestRequest, Receipt: receipt,
		Stopped: true, TemporaryAuthorityRemoved: true,
	}
	if err := adoption.Validate(); err != nil {
		t.Fatal(err)
	}
	return adoption
}
