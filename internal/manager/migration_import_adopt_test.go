package manager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profilestate"
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
	materializeMigrationOperationProfileStates(t, root, &operation)
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
	stage := migrationDestinationStageStateFixtureForOperation(
		operation, filepath.Join(root, "profiles"),
	)
	stage.StageHandle = migrationImportStageHandle(operation.ID)
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

func materializeMigrationOperationProfileStates(
	t *testing.T,
	root string,
	operation *MigrationOperation,
) {
	t.Helper()
	profilesRoot := filepath.Join(root, "profiles")
	for index := range operation.EnvironmentActions {
		snapshot, _ := managerMaterializationProfileState(t)
		action := &operation.EnvironmentActions[index]
		action.ProfileStateContentDigest = migration.Digest(snapshot.Digest())
		action.ProfileStateLogicalBytes = snapshot.LogicalBytes()
		owner := profilestate.Owner{
			OperationID: operation.ID, ProfileName: action.DestinationProfileName,
			ComponentID:   string(action.ProfileStateComponentID),
			ContentDigest: string(action.ProfileStateContentDigest),
			LogicalBytes:  action.ProfileStateLogicalBytes,
		}
		materializer, err := profilestate.NewMaterializer(profilesRoot, owner)
		if err != nil {
			t.Fatal(err)
		}
		if err := snapshot.Write(context.Background(), 73, materializer.Consume); err != nil {
			_ = materializer.Abort()
			t.Fatal(err)
		}
		if err := materializer.Finish(); err != nil {
			_ = materializer.Abort()
			t.Fatal(err)
		}
	}
}

func migrationDestinationAdoptionFixture(
	t *testing.T,
	fixed backend.DestinationAdoptionRequest,
	call int,
) backend.DestinationAdoption {
	t.Helper()
	actions := []string{migration.AdoptionActionPreserveIdentity}
	post := fixed.SourceIdentity
	if fixed.Policy == migration.GuestIdentitySafeClone {
		actions = []string{
			migration.AdoptionActionResetMachineID,
			migration.AdoptionActionResetSSHHostKeys,
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
	if len(fixed.MountBindings) != 0 {
		actions = append(actions, migration.AdoptionActionRebindDiskMounts)
	}
	actions = append(actions, migration.AdoptionActionInstallSSHKeys)
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
		MountBindings: append(
			[]migration.DiskMountBinding(nil), fixed.MountBindings...,
		),
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
		Policy: guestRequest.Policy, Helper: guestRequest.Helper,
		MountBindings: append(
			[]migration.DiskMountBinding(nil), guestRequest.MountBindings...,
		),
		ActionResults: results,
		PostIdentity:  &post, Status: migration.AdoptionReceiptStatusCompleted,
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
