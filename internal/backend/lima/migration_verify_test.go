package lima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestVerifyMigrationDestinationBindsStageAdoptionAndImportPolicy(t *testing.T) {
	fixture := newMigrationDestinationVerificationFixture(t, "verify", "4", "5")

	first, err := fixture.provider.VerifyMigrationDestination(
		context.Background(), fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.provider.VerifyMigrationDestination(
		context.Background(), fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.ProofDigest.Validate() != nil || !first.Stopped ||
		!first.DigestsMatch || !first.IdentityPolicySatisfied ||
		!first.TemporaryAuthorityRemoved || !first.ImportedAuthorityAbsent {
		t.Fatalf("verification proof is incomplete or unstable: first=%+v second=%+v", first, second)
	}
	if _, err := os.Lstat(filepath.Join(
		fixture.home, string(fixture.owner.Objects[0].BackendIdentity),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verification made the destination instance visible: %v", err)
	}
}

func TestVerifyMigrationDestinationSamePayloadKeepsPerImportIdentityDecision(t *testing.T) {
	first := newMigrationDestinationVerificationFixture(t, "multi_a", "4", "5")
	second := newMigrationDestinationVerificationFixture(t, "multi_b", "6", "7")

	firstProof, err := first.provider.VerifyMigrationDestination(
		context.Background(), first.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondProof, err := second.provider.VerifyMigrationDestination(
		context.Background(), second.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstSafe := first.request.AdoptionReceipts[0].PostIdentity
	secondSafe := second.request.AdoptionReceipts[0].PostIdentity
	if firstSafe == nil || secondSafe == nil ||
		firstSafe.MachineIDDigest == secondSafe.MachineIDDigest ||
		first.owner.Objects[0].BackendIdentity == second.owner.Objects[0].BackendIdentity ||
		firstProof.ProofDigest == secondProof.ProofDigest {
		t.Fatalf("separate imports reused destination identity: first=%+v second=%+v",
			firstProof, secondProof)
	}
	for _, candidate := range []*migrationDestinationVerificationFixture{first, second} {
		exactRequest := candidate.request.AdoptionRequests[1]
		exactReceipt := candidate.request.AdoptionReceipts[1]
		if exactRequest.Policy != migration.GuestIdentityExactRestore ||
			exactReceipt.PostIdentity == nil ||
			!exactReceipt.PostIdentity.Equal(exactRequest.SourceIdentity) {
			t.Fatalf("Exact Guest Restore did not preserve identity: request=%+v receipt=%+v",
				exactRequest, exactReceipt)
		}
	}
}

func TestVerifyMigrationDestinationRejectsChangedEvidenceAndAuthority(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(*testing.T, *migrationDestinationVerificationFixture)
		code   string
	}{
		"root changed after stopped proof": {
			mutate: func(t *testing.T, fixture *migrationDestinationVerificationFixture) {
				t.Helper()
				path := fixture.rootPath(fixture.request.AdoptionRequests[0].EnvironmentRef)
				writeMigrationVerificationByte(t, path, 96, 0x7f)
			},
			code: "migration.provider.verify_disk_changed",
		},
		"attached disk changed": {
			mutate: func(t *testing.T, fixture *migrationDestinationVerificationFixture) {
				t.Helper()
				for _, entry := range fixture.owner.Entries {
					if entry.Role == migration.DiskRoleAttached {
						writeMigrationVerificationByte(
							t, filepath.Join(fixture.stageDir, entry.RelativePath), 64, 0x7e,
						)
						return
					}
				}
				t.Fatal("attached disk fixture missing")
			},
			code: "migration.provider.verify_disk_changed",
		},
		"normalized config changed": {
			mutate: func(t *testing.T, fixture *migrationDestinationVerificationFixture) {
				t.Helper()
				path := filepath.Join(
					fixture.stageDir, fixture.owner.Configurations[0].NormalizedRelativePath,
				)
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			code: "migration.provider.verify_config_changed",
		},
		"temporary channel reappeared": {
			mutate: func(t *testing.T, fixture *migrationDestinationVerificationFixture) {
				t.Helper()
				path := filepath.Join(
					fixture.stageDir, "adoption",
					string(fixture.request.AdoptionRequests[0].EnvironmentRef), "channel",
				)
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			code: "migration.provider.verify_temporary_authority_present",
		},
		"top-level instance appeared": {
			mutate: func(t *testing.T, fixture *migrationDestinationVerificationFixture) {
				t.Helper()
				if err := os.Mkdir(
					filepath.Join(fixture.home, string(fixture.owner.Objects[0].BackendIdentity)),
					0o700,
				); err != nil {
					t.Fatal(err)
				}
			},
			code: "migration.provider.verify_stopped_unproved",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newMigrationDestinationVerificationFixture(t, "tamper", "4", "5")
			testCase.mutate(t, fixture)
			_, err := fixture.provider.VerifyMigrationDestination(
				context.Background(), fixture.request,
			)
			var providerErr *backend.MigrationProviderError
			if !errors.As(err, &providerErr) || providerErr.Code != testCase.code ||
				!providerErr.RecoveryRequired {
				if providerErr != nil {
					t.Fatalf("verification error=%v cause=%v", err, providerErr.Cause)
				}
				t.Fatalf("verification error=%v", err)
			}
		})
	}
}

func TestVerifyMigrationDestinationRejectsAdoptionRequestSubstitution(t *testing.T) {
	fixture := newMigrationDestinationVerificationFixture(t, "substitute", "4", "5")
	fixture.request.AdoptionRequests[0].SourceIdentity.MachineIDDigest =
		migration.Digest("sha256:" + strings.Repeat("8", 64))

	_, err := fixture.provider.VerifyMigrationDestination(
		context.Background(), fixture.request,
	)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.verify_adoption_invalid" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("substituted adoption request error=%v", err)
	}
}

func TestRollbackMigrationDestinationRemovesVerifiedAdoptionEvidence(t *testing.T) {
	fixture := newMigrationDestinationVerificationFixture(t, "cleanup", "4", "5")
	if _, err := fixture.provider.VerifyMigrationDestination(
		context.Background(), fixture.request,
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.provider.RollbackMigrationDestination(
		context.Background(), backend.DestinationRollbackRequest{
			Binding: fixture.owner.Binding, StageHandle: fixture.owner.StageHandle,
			ObjectHandles: append([]migration.OpaqueID(nil), fixture.owner.ObjectHandles...),
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(fixture.stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified stage survived rollback: %v", err)
	}
}

type migrationDestinationVerificationFixture struct {
	home     string
	provider Backend
	stageDir string
	owner    migrationStageOwner
	request  backend.DestinationVerifyRequest
}

func newMigrationDestinationVerificationFixture(
	t *testing.T,
	suffix,
	safeMachineDigit,
	safeSSHDigit string,
) *migrationDestinationVerificationFixture {
	t.Helper()
	source := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, source, suffix)
	stageRequest.Binding.OperationID = migration.OpaqueID("op_verify_" + suffix + "1")
	stageRequest.Binding.EffectID = migration.OpaqueID("effect_stage_" + suffix + "1")
	if err := stageRequest.Validate(); err != nil {
		t.Fatal(err)
	}
	stage, err := source.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(
		source.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	var owner migrationStageOwner
	if err := readMigrationJSONStrict(filepath.Join(stageDir, "owner.json"), &owner); err != nil {
		t.Fatal(err)
	}
	ownerDigest, err := migrationJSONDigest(owner)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := source.provider.MigrationCapabilities(context.Background())
	if err != nil || capability.AdoptionHelper == nil {
		t.Fatalf("capability=%+v err=%v", capability, err)
	}
	helper := migration.HelperBinding{
		PackageID: capability.AdoptionHelper.PackageID,
		Version:   capability.AdoptionHelper.Version,
		SHA256:    capability.AdoptionHelper.Digest,
	}
	verify := backend.DestinationVerifyRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID:        stageRequest.Binding.OperationID,
			EffectID:           migration.OpaqueID("effect_verify_" + suffix + "1"),
			CapabilityRevision: stageRequest.Binding.CapabilityRevision,
		},
		StageHandle:   stage.StageHandle,
		ExpectedDisks: append([]migration.DiskObject(nil), stageRequest.Disks...),
	}
	for index, configuration := range owner.Configurations {
		policy := migration.GuestIdentitySafeClone
		sourceMachineDigit, sourceSSHDigit := "1", "2"
		postMachineDigit, postSSHDigit := safeMachineDigit, safeSSHDigit
		if index == 1 {
			policy = migration.GuestIdentityExactRestore
			sourceMachineDigit, sourceSSHDigit = "a", "b"
			postMachineDigit, postSSHDigit = sourceMachineDigit, sourceSSHDigit
		}
		sourceIdentity := migration.GuestIdentityEvidence{
			MachineIDDigest: migration.Digest(
				"sha256:" + strings.Repeat(sourceMachineDigit, 64),
			),
			SSHHostKeyDigests: []migration.Digest{migration.Digest(
				"sha256:" + strings.Repeat(sourceSSHDigit, 64),
			)},
		}
		postIdentity := migration.GuestIdentityEvidence{
			MachineIDDigest: migration.Digest(
				"sha256:" + strings.Repeat(postMachineDigit, 64),
			),
			SSHHostKeyDigests: []migration.Digest{migration.Digest(
				"sha256:" + strings.Repeat(postSSHDigit, 64),
			)},
		}
		actions := []string{migration.AdoptionActionPreserveIdentity}
		if policy == migration.GuestIdentitySafeClone {
			actions = []string{
				migration.AdoptionActionResetMachineID,
				migration.AdoptionActionResetSSHHostKeys,
			}
		}
		guestRequest := migration.AdoptionRequest{
			Schema:         migration.AdoptionRequestSchema,
			OperationID:    stageRequest.Binding.OperationID,
			EnvironmentRef: configuration.EnvironmentRef,
			RequestNonce: migration.OpaqueID(
				"nonce_request_" + suffix + "_" + string(configuration.EnvironmentRef),
			),
			ReceiptNonce: migration.OpaqueID(
				"nonce_receipt_" + suffix + "_" + string(configuration.EnvironmentRef),
			),
			Policy: policy, SourceIdentity: sourceIdentity,
			DestinationSSHKeys: []string{
				"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture" + string(rune('A'+index)),
			},
			PermittedActions: actions, Helper: helper,
		}
		results := make([]migration.AdoptionActionResult, len(actions))
		for actionIndex, action := range actions {
			results[actionIndex] = migration.AdoptionActionResult{
				Action: action, Status: migration.AdoptionActionStatusCompleted,
			}
		}
		receipt := migration.AdoptionReceipt{
			Schema:         migration.AdoptionReceiptSchema,
			OperationID:    guestRequest.OperationID,
			EnvironmentRef: guestRequest.EnvironmentRef,
			RequestNonce:   guestRequest.RequestNonce, ReceiptNonce: guestRequest.ReceiptNonce,
			Policy: policy, Helper: helper, ActionResults: results,
			PostIdentity: &postIdentity, Status: migration.AdoptionReceiptStatusCompleted,
			CompletionMarker: true,
		}
		if err := receipt.MatchesRequest(guestRequest); err != nil {
			t.Fatal(err)
		}
		var rootEntry migrationStageEntry
		for _, entry := range owner.Entries {
			if entry.DiskID == configuration.RootDiskID {
				rootEntry = entry
				break
			}
		}
		if rootEntry.ComponentID == "" {
			t.Fatal("root stage entry missing")
		}
		if policy == migration.GuestIdentitySafeClone {
			writeMigrationVerificationByte(
				t, filepath.Join(stageDir, rootEntry.RelativePath), 48,
				byte(safeMachineDigit[0]),
			)
		}
		identity, err := migrationAdoptionEvidenceFileIdentity(
			source.home, stageDir, rootEntry,
		)
		if err != nil {
			t.Fatal(err)
		}
		evidence := migrationAdoptionEvidence{
			Schema:      migrationAdoptionEvidenceSchema,
			StageHandle: stage.StageHandle, StageOwnerDigest: ownerDigest,
			Binding: backend.MigrationEffectBinding{
				OperationID:        stageRequest.Binding.OperationID,
				EffectID:           migration.OpaqueID("effect_adopt_" + suffix + "1"),
				CapabilityRevision: stageRequest.Binding.CapabilityRevision,
			},
			EnvironmentRef:  configuration.EnvironmentRef,
			BackendIdentity: configuration.BackendIdentity,
			RootDiskID:      configuration.RootDiskID, RootComponentID: rootEntry.ComponentID,
			PreAdoptionContentDigest: rootEntry.ContentDigest,
			PostAdoptionFileIdentity: identity,
			Request:                  guestRequest, Receipt: receipt,
			ShutdownProof: migration.Digest("sha256:" + strings.Repeat("9", 64)),
			Stopped:       true, TemporaryAuthorityRemoved: true,
			ImportedAuthorityAbsent: true,
		}
		if err := writeMigrationAdoptionEvidenceExclusive(
			source.home, stageDir, evidence,
		); err != nil {
			t.Fatal(err)
		}
		verify.IdentityPolicies = append(verify.IdentityPolicies, migration.IdentitySelection{
			SourceRef: configuration.EnvironmentRef, Policy: policy,
		})
		verify.AdoptionRequests = append(verify.AdoptionRequests, guestRequest)
		verify.AdoptionReceipts = append(verify.AdoptionReceipts, receipt)
	}
	if err := verify.Validate(); err != nil {
		t.Fatal(err)
	}
	return &migrationDestinationVerificationFixture{
		home: source.home, provider: source.provider,
		stageDir: stageDir, owner: owner, request: verify,
	}
}

func (fixture *migrationDestinationVerificationFixture) rootPath(
	environmentRef migration.OpaqueID,
) string {
	for _, configuration := range fixture.owner.Configurations {
		if configuration.EnvironmentRef != environmentRef {
			continue
		}
		for _, entry := range fixture.owner.Entries {
			if entry.DiskID == configuration.RootDiskID {
				return filepath.Join(fixture.stageDir, entry.RelativePath)
			}
		}
	}
	return ""
}

func writeMigrationVerificationByte(
	t *testing.T,
	path string,
	offset int64,
	value byte,
) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{value}, offset); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
