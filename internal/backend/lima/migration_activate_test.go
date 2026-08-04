package lima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestActivateMigrationDestinationPromotesStoppedObjectsAndReplays(t *testing.T) {
	fixture, request := migrationDestinationActivationFixture(t, "activate")

	first, err := fixture.provider.ActivateMigrationDestination(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	assertMigrationActivationLayout(t, fixture)

	second, err := fixture.provider.ActivateMigrationDestination(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("activation replay changed: first=%+v second=%+v", first, second)
	}
}

func TestActivateMigrationDestinationResumesAfterCommittedPartialPromotion(t *testing.T) {
	fixture, request := migrationDestinationActivationFixture(t, "resume")
	ownerDigest, err := migrationJSONDigest(fixture.owner)
	if err != nil {
		t.Fatal(err)
	}
	intent := migrationActivationIntent{
		Schema: migrationActivationIntentSchema, Binding: request.Binding,
		Proof: request.Proof, OwnerDigest: ownerDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
	}
	if err := writeMigrationJSONExclusive(
		filepath.Join(fixture.stageDir, "activation-intent.json"), intent,
	); err != nil {
		t.Fatal(err)
	}
	if err := syncMigrationDirectory(fixture.stageDir); err != nil {
		t.Fatal(err)
	}

	var attached migrationStageEntry
	for _, entry := range fixture.owner.Entries {
		if entry.Role == migration.DiskRoleAttached {
			attached = entry
			break
		}
	}
	if attached.ComponentID == "" {
		t.Fatal("activation fixture lacks an attached disk")
	}
	disksRoot, err := ensurePrivateMigrationDirectory(fixture.home, "_disks")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(fixture.stageDir, "disks", string(attached.ObjectHandle))
	destination := filepath.Join(disksRoot, string(attached.ObjectHandle))
	if err := promoteMigrationDirectoryNoReplace(source, destination, func(path string) error {
		return verifyMigrationActivationDiskDirectory(
			fixture.home, fixture.stageDir, fixture.owner, attached, path,
		)
	}); err != nil {
		t.Fatal(err)
	}

	activation, err := fixture.provider.ActivateMigrationDestination(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := activation.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	assertMigrationActivationLayout(t, fixture)
}

func TestActivateMigrationDestinationRejectsChangedProofBeforeCommitIntent(t *testing.T) {
	fixture, request := migrationDestinationActivationFixture(t, "proof")
	request.Proof.ProofDigest = migration.Digest("sha256:" + strings.Repeat("0", 64))

	_, err := fixture.provider.ActivateMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.activation_proof_changed" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("changed activation proof error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		fixture.stageDir, "activation-intent.json",
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed proof wrote an activation intent: %v", err)
	}
	for _, configuration := range fixture.owner.Configurations {
		if _, err := os.Lstat(filepath.Join(
			fixture.home, string(configuration.BackendIdentity),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("changed proof promoted %s: %v", configuration.BackendIdentity, err)
		}
	}
}

func TestActivateMigrationDestinationCompletedReplayRejectsMetadataLoss(t *testing.T) {
	fixture, request := migrationDestinationActivationFixture(t, "metadata")
	if _, err := fixture.provider.ActivateMigrationDestination(
		context.Background(), request,
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(
		fixture.home, string(fixture.owner.Configurations[0].BackendIdentity), "lima-version",
	)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.provider.ActivateMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.activation_completion_invalid" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("lost activation metadata error=%v", err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("completed replay silently repaired missing evidence: %v", statErr)
	}
}

func migrationDestinationActivationFixture(
	t *testing.T,
	suffix string,
) (*migrationDestinationVerificationFixture, backend.DestinationActivationRequest) {
	t.Helper()
	fixture := newMigrationDestinationVerificationFixture(t, suffix, "4", "5")
	proof, err := fixture.provider.VerifyMigrationDestination(
		context.Background(), fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := backend.DestinationActivationRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID: fixture.request.Binding.OperationID,
			EffectID: migration.OpaqueID(
				"effect_activate_" + suffix + "1",
			),
			CapabilityRevision: fixture.request.Binding.CapabilityRevision,
		},
		Proof: proof,
		ObjectHandles: append(
			[]migration.OpaqueID(nil), fixture.owner.ObjectHandles...,
		),
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return fixture, request
}

func assertMigrationActivationLayout(
	t *testing.T,
	fixture *migrationDestinationVerificationFixture,
) {
	t.Helper()
	for _, entry := range fixture.owner.Entries {
		if entry.Role != migration.DiskRoleAttached {
			continue
		}
		if _, err := os.Lstat(filepath.Join(
			fixture.stageDir, "disks", string(entry.ObjectHandle),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("activated attached disk remains staged: %v", err)
		}
		if err := verifyMigrationActivationDiskDirectory(
			fixture.home, fixture.stageDir, fixture.owner, entry,
			filepath.Join(fixture.home, "_disks", string(entry.ObjectHandle)),
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, configuration := range fixture.owner.Configurations {
		if _, err := os.Lstat(filepath.Join(
			fixture.stageDir, "instances", string(configuration.BackendIdentity),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("activated instance remains staged: %v", err)
		}
		if err := verifyMigrationActivationInstanceDirectory(
			fixture.home, fixture.stageDir, fixture.owner, configuration,
			filepath.Join(fixture.home, string(configuration.BackendIdentity)), "2.2.0",
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(
		fixture.stageDir, "activation-complete.json",
	)); err != nil {
		t.Fatalf("activation completion evidence is absent: %v", err)
	}
}
