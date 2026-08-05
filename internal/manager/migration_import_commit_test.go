package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMigrationImportCommitPublishesProfilesAndEnvironmentsOnce(t *testing.T) {
	service, provider, verified := migrationVerifiedDestinationFixture(t)
	profileStore := profile.Store{Root: service.Store.Root}
	if records, err := service.Environments.List(); err != nil || len(records) != 0 {
		t.Fatalf("environments visible before commit: records=%+v err=%v", records, err)
	}
	for _, name := range []string{"dev-clone", "dev-exact"} {
		if _, err := os.Lstat(profileStore.ProfileDir(name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("profile %q visible before commit: %v", name, err)
		}
	}

	completed, publication, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != MigrationPhaseComplete || completed.Decision == nil ||
		completed.Decision.Value != MigrationDecisionCommit || publication.Validate() != nil ||
		provider.activateCalls != 1 || completed.Recovery.Action != MigrationRecoveryNone ||
		completed.Recovery.Code != completed.Result.Code {
		t.Fatalf("completed=%+v publication=%+v activateCalls=%d", completed, publication, provider.activateCalls)
	}
	activation, err := migrationOperationEffect(completed, MigrationEffectActivate)
	if err != nil || activation.Status != MigrationEffectSucceeded ||
		len(activation.Evidence) != 1 ||
		activation.Evidence[0].Code != migrationDestinationActivationEvidenceCode {
		t.Fatalf("activation=%+v err=%v", activation, err)
	}
	for _, claim := range completed.Claims {
		if claim.State != MigrationClaimReleased {
			t.Fatalf("completed import retained claim: %+v", claim)
		}
	}
	records, err := service.Environments.List()
	if err != nil || len(records) != 2 {
		t.Fatalf("published records=%+v err=%v", records, err)
	}
	for _, record := range records {
		if record.Mode != environment.ModeDedicatedPortal || record.HostWorkspace() != "" ||
			record.Status != environment.StatusStopped {
			t.Fatalf("published record authority=%+v", record)
		}
		loaded, err := profileStore.Load(record.Profile)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Metadata["createdFrom"] != verified.ID ||
			loaded.Metadata["lineageMode"] != "migration" {
			t.Fatalf("published profile metadata=%+v", loaded.Metadata)
		}
		history, err := os.ReadFile(filepath.Join(
			profileStore.ProfileDir(record.Profile), "home", ".claude", "history.jsonl",
		))
		if err != nil || string(history) != "claude-session-survives-full-migration\n" {
			t.Fatalf("published profile state=%q error=%v", history, err)
		}
		gitConfig, err := os.ReadFile(filepath.Join(
			profileStore.ProfileDir(record.Profile), "home", ".gitconfig",
		))
		if err != nil || strings.Contains(string(gitConfig), "SOURCE-IDENTITY-MUST-NOT-SURVIVE") {
			t.Fatalf("source-generated identity survived=%q error=%v", gitConfig, err)
		}
		configuration, err := RuntimeConfigurationForProfile(
			loaded, record.Backend, record.Mode,
		)
		if err != nil {
			t.Fatal(err)
		}
		if record.MachineIdentityID != configuration.Layers.MachineID ||
			record.BootConfigurationID != configuration.Layers.BootID {
			t.Fatalf("published record is not runnable without drift: %+v", record)
		}
	}

	replayed, replayPublication, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if err != nil || replayed.Revision != completed.Revision ||
		!reflect.DeepEqual(replayPublication, publication) || provider.activateCalls != 1 {
		t.Fatalf("replay=%+v publication=%+v calls=%d err=%v", replayed, replayPublication, provider.activateCalls, err)
	}
}

func TestMigrationImportCommitRejectsProviderSubstitutionBeforeVisibility(t *testing.T) {
	service, provider, verified := migrationVerifiedDestinationFixture(t)
	provider.activateResponse = func(
		request backend.DestinationActivationRequest,
		_ int,
	) (backend.DestinationActivation, error) {
		response := backend.DestinationActivation{
			Binding: request.Binding, StageHandle: request.Proof.StageHandle,
			ProofDigest:   request.Proof.ProofDigest,
			ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
			Stopped:       true, Promoted: true,
		}
		response.StageHandle = "stage_substitute1234"
		return response, nil
	}

	failed, _, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if !errors.Is(err, backend.ErrMigrationProviderResponse) ||
		failed.Phase != MigrationPhaseRecoverableFailure || failed.Decision == nil ||
		failed.Recovery.Action != MigrationRecoveryFinish {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	if records, listErr := service.Environments.List(); listErr != nil || len(records) != 0 {
		t.Fatalf("provider substitution published environments: %+v err=%v", records, listErr)
	}
	for _, name := range []string{"dev-clone", "dev-exact"} {
		if _, statErr := os.Lstat(filepath.Join(service.Store.Root, "profiles", name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("provider substitution published profile %q: %v", name, statErr)
		}
	}
}

func TestMigrationImportCommitPreflightsProfileConflictBeforeDecision(t *testing.T) {
	service, provider, verified := migrationVerifiedDestinationFixture(t)
	profileStore := profile.Store{Root: service.Store.Root}
	if err := profileStore.Save(profile.Default("dev-clone")); err != nil {
		t.Fatal(err)
	}

	unchanged, _, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if !errors.Is(err, profile.ErrBatchConflict) ||
		unchanged.Phase != MigrationPhaseVerifying || unchanged.Decision != nil ||
		provider.activateCalls != 0 {
		t.Fatalf("preflight operation=%+v calls=%d error=%v", unchanged, provider.activateCalls, err)
	}
	if records, listErr := service.Environments.List(); listErr != nil || len(records) != 0 {
		t.Fatalf("preflight conflict published environments: %+v error=%v", records, listErr)
	}
}

func TestMigrationImportCommitRaceAfterDecisionRequiresFinish(t *testing.T) {
	service, provider, verified := migrationVerifiedDestinationFixture(t)
	profileStore := profile.Store{Root: service.Store.Root}
	provider.activateResponse = func(
		request backend.DestinationActivationRequest,
		_ int,
	) (backend.DestinationActivation, error) {
		if err := profileStore.Save(profile.Default("dev-clone")); err != nil {
			return backend.DestinationActivation{}, err
		}
		return backend.DestinationActivation{
			Binding: request.Binding, StageHandle: request.Proof.StageHandle,
			ProofDigest:   request.Proof.ProofDigest,
			ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
			Stopped:       true, Promoted: true,
		}, nil
	}

	failed, _, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if !errors.Is(err, profile.ErrBatchConflict) ||
		failed.Phase != MigrationPhaseRecoverableFailure || failed.Decision == nil ||
		failed.Decision.Value != MigrationDecisionCommit ||
		failed.Recovery.Action != MigrationRecoveryFinish || provider.activateCalls != 1 {
		t.Fatalf("raced commit=%+v calls=%d error=%v", failed, provider.activateCalls, err)
	}
	if records, listErr := service.Environments.List(); listErr != nil || len(records) != 0 {
		t.Fatalf("raced commit published environments: %+v error=%v", records, listErr)
	}
}

func TestMigrationImportCommitResumesSameOneWayDecision(t *testing.T) {
	service, provider, verified := migrationVerifiedDestinationFixture(t)
	provider.activateResponse = func(
		request backend.DestinationActivationRequest,
		call int,
	) (backend.DestinationActivation, error) {
		if call == 1 {
			return backend.DestinationActivation{}, errors.New("injected activation interruption")
		}
		return backend.DestinationActivation{
			Binding: request.Binding, StageHandle: request.Proof.StageHandle,
			ProofDigest:   request.Proof.ProofDigest,
			ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
			Stopped:       true, Promoted: true,
		}, nil
	}
	failed, _, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if err == nil || failed.Phase != MigrationPhaseRecoverableFailure ||
		failed.Decision == nil || failed.Decision.Value != MigrationDecisionCommit ||
		failed.Recovery.Action != MigrationRecoveryFinish {
		t.Fatalf("first commit=%+v err=%v", failed, err)
	}
	completed, _, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if err != nil || completed.Phase != MigrationPhaseComplete || provider.activateCalls != 2 {
		t.Fatalf("resumed commit=%+v calls=%d err=%v", completed, provider.activateCalls, err)
	}
}

func TestMigrationImportCommitReplayRejectsForgedTerminalEvidence(t *testing.T) {
	service, provider, verified := migrationVerifiedDestinationFixture(t)
	completed, _, err := service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	forged := completed.Clone()
	forgedDigest := migration.Digest("sha256:" + strings.Repeat("0", 64))
	for index := range forged.Effects {
		if forged.Effects[index].Kind == MigrationEffectActivate {
			forged.Effects[index].Evidence[0].Digest = forgedDigest
		}
	}
	forged.Result.ReceiptDigest = forgedDigest
	if err := forged.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.withLock(func() error {
		return service.Store.writeOperationUnlocked(forged)
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = service.CommitImportDestination(
		context.Background(), MigrationImportCommitRequest{OperationID: verified.ID},
	)
	if !errors.Is(err, ErrMigrationOperationMismatch) || provider.activateCalls != 1 {
		t.Fatalf("forged replay calls=%d error=%v", provider.activateCalls, err)
	}
}

func migrationVerifiedDestinationFixture(
	t *testing.T,
) (MigrationImportService, *managerMigrationProviderFixture, MigrationOperation) {
	t.Helper()
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
	return service, provider, verified
}
