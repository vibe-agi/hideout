package manager

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/profilestate"
)

func TestMigrationOperationValidatesImmutableImportPlanAndFreshIdentityPolicy(t *testing.T) {
	operation := migrationImportOperationFixture()
	if err := operation.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"passphrase", "secretInputHandle", "wrappedMasterKey", "privateKey",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("durable operation contains %q: %s", forbidden, encoded)
		}
	}
	if len(operation.IdentityActions) != 2 ||
		operation.IdentityActions[0].GuestPolicy != migration.GuestIdentitySafeClone ||
		operation.IdentityActions[1].GuestPolicy != migration.GuestIdentityExactRestore {
		t.Fatalf("identity policies were not frozen per destination: %+v", operation.IdentityActions)
	}

	mutated := operation.Clone()
	mutated.IdentityActions[0].GuestPolicy = migration.GuestIdentityExactRestore
	if operation.MatchesImmutable(mutated) {
		t.Fatal("identity-policy mutation preserved immutable binding")
	}
	mutated = operation.Clone()
	mutated.ImportObjects[0].DestinationName = "mutated-clone"
	if operation.MatchesImmutable(mutated) {
		t.Fatal("import-object mutation preserved immutable binding")
	}
	mutated = operation.Clone()
	mutated.Effects[0].Provider = "different.provider"
	if operation.MatchesImmutable(mutated) {
		t.Fatal("effect-provider mutation preserved immutable binding")
	}

	operation = migrationVerifiedImportOperationFixture(t)
	cloned := operation.Clone()
	cloned.DestinationStage.Profiles[0].Snapshot.EnvDeny = append(
		cloned.DestinationStage.Profiles[0].Snapshot.EnvDeny, "MIGRATION_MUTATION",
	)
	if reflect.DeepEqual(
		operation.DestinationStage.Profiles[0].Snapshot,
		cloned.DestinationStage.Profiles[0].Snapshot,
	) {
		t.Fatal("destination profile snapshot was not deep-cloned")
	}
	mutated = operation.Clone()
	mutated.DestinationStage.Profiles[0].ContentDigest =
		migration.Digest("sha256:" + strings.Repeat("0", 64))
	if err := mutated.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("tampered destination profile error=%v", err)
	}
}

func TestMigrationCommitDecisionIsOneWayAndReplaySafe(t *testing.T) {
	operation := migrationVerifiedImportOperationFixture(t)
	now := operation.UpdatedAt.Add(time.Second)
	committing, changed, err := operation.Decide(MigrationDecisionCommit, now)
	if err != nil || !changed || committing.Phase != MigrationPhaseCommitting ||
		committing.Decision == nil || committing.Decision.Value != MigrationDecisionCommit {
		t.Fatalf("commit decision changed=%t operation=%+v err=%v", changed, committing, err)
	}
	replay, changed, err := committing.Decide(MigrationDecisionCommit, now.Add(time.Second))
	if err != nil || changed || replay.Revision != committing.Revision {
		t.Fatalf("commit replay changed=%t operation=%+v err=%v", changed, replay, err)
	}
	if _, _, err := committing.Decide(
		MigrationDecisionRollback, now.Add(time.Second),
	); !errors.Is(err, ErrMigrationDecisionConflict) {
		t.Fatalf("opposite decision error=%v", err)
	}
	if operation.Decision != nil {
		t.Fatal("pure decision transition mutated its input")
	}
}

func migrationVerifiedImportOperationFixture(t *testing.T) MigrationOperation {
	t.Helper()
	operation := migrationImportOperationFixture()
	stage := migrationDestinationStageStateFixtureForOperation(
		operation, filepath.Join("/tmp", "hideout-migration-fixture", "profiles"),
	)
	operation.Effects[0].Status = MigrationEffectSucceeded
	operation.DestinationStage = &stage
	adoption := migrationDestinationAdoptionStateFixture(t, operation)
	operation.Effects[1].Status = MigrationEffectSucceeded
	operation.DestinationAdoption = &adoption
	operation.Effects[2].Status = MigrationEffectSucceeded
	operation.Effects[2].Evidence = []MigrationEffectEvidence{{
		Code:       migrationDestinationVerificationEvidenceCode,
		OpaqueRef:  stage.StageHandle,
		Digest:     migration.Digest("sha256:" + strings.Repeat("6", 64)),
		Count:      uint64(len(operation.ExpectedDisks)),
		ObservedAt: operation.UpdatedAt,
	}}
	operation.Phase = MigrationPhaseVerifying
	if err := operation.Validate(); err != nil {
		t.Fatal(err)
	}
	return operation
}

func migrationDestinationStageStateFixture() MigrationDestinationStageState {
	operation := migrationImportOperationFixture()
	return migrationDestinationStageStateFixtureForOperation(
		operation, filepath.Join("/tmp", "hideout-migration-fixture", "profiles"),
	)
}

func migrationDestinationStageStateFixtureForOperation(
	operation MigrationOperation,
	profilesRoot string,
) MigrationDestinationStageState {
	states := make([]MigrationMaterializedProfileState, len(operation.EnvironmentActions))
	for index, action := range operation.EnvironmentActions {
		owner := profilestate.Owner{
			OperationID: operation.ID, ProfileName: action.DestinationProfileName,
			ComponentID:   string(action.ProfileStateComponentID),
			ContentDigest: string(action.ProfileStateContentDigest),
			LogicalBytes:  action.ProfileStateLogicalBytes,
		}
		stagePath, err := profilestate.StagePath(profilesRoot, owner)
		if err != nil {
			panic(err)
		}
		states[index] = MigrationMaterializedProfileState{
			SourceRef: action.SourceRef, ProfileName: action.DestinationProfileName,
			ComponentID:   action.ProfileStateComponentID,
			ContentDigest: action.ProfileStateContentDigest,
			LogicalBytes:  action.ProfileStateLogicalBytes, StagePath: stagePath,
		}
	}
	return MigrationDestinationStageState{
		StageHandle: "stage_fixture1234",
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
		ProfileStates:  states,
		EvidenceDigest: migration.Digest("sha256:" + strings.Repeat("c", 64)),
	}
}

func migrationMaterializedProfilesFixture() []MigrationMaterializedProfile {
	components := []migration.OpaqueID{"component_profile0001", "component_profile0002"}
	profiles := make([]MigrationMaterializedProfile, len(components))
	for index, componentID := range components {
		source := profile.Default("migration-fixture-" + string(rune('a'+index)))
		snapshot, err := migration.NormalizePortableProfile(source)
		if err != nil {
			panic(err)
		}
		encoded, err := migration.EncodePortableProfile(snapshot)
		if err != nil {
			panic(err)
		}
		digest := sha256.Sum256(encoded)
		clear(encoded)
		profiles[index] = MigrationMaterializedProfile{
			ComponentID: componentID,
			ContentDigest: migration.Digest(
				"sha256:" + fmt.Sprintf("%x", digest[:]),
			),
			Snapshot: snapshot,
		}
	}
	return profiles
}

func migrationDestinationAdoptionStateFixture(
	t *testing.T,
	operation MigrationOperation,
) MigrationDestinationAdoptionState {
	t.Helper()
	records := make([]MigrationDestinationAdoptionRecord, len(operation.IdentityActions))
	for index, action := range operation.IdentityActions {
		fixed := backend.DestinationAdoptionRequest{
			Binding: backend.MigrationEffectBinding{
				OperationID:        migration.OpaqueID(operation.ID),
				EffectID:           operation.Effects[1].ID,
				CapabilityRevision: operation.CapabilityRevision,
			},
			StageHandle:    operation.DestinationStage.StageHandle,
			EnvironmentRef: action.SourceRef, Policy: action.GuestPolicy,
			SourceIdentity: operation.SourceGuestIdentities[index].Evidence,
			Helper:         *operation.AdoptionHelper,
		}
		response := migrationDestinationAdoptionFixture(t, fixed, index+1)
		records[index] = MigrationDestinationAdoptionRecord{
			EnvironmentRef: action.SourceRef, Request: response.Request,
			Receipt: response.Receipt, Stopped: response.Stopped,
			TemporaryAuthorityRemoved: response.TemporaryAuthorityRemoved,
		}
	}
	return MigrationDestinationAdoptionState{
		StageHandle: operation.DestinationStage.StageHandle, Records: records,
		EvidenceDigest: migration.Digest("sha256:" + strings.Repeat("d", 64)),
	}
}

func TestMigrationOperationRejectsInvalidClaimsEffectsAndTerminalState(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Claims[0], operation.Claims[1] = operation.Claims[1], operation.Claims[0]
	if err := operation.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("unsorted claims error=%v", err)
	}

	operation = migrationImportOperationFixture()
	operation.Effects[1].ID = operation.Effects[0].ID
	if err := operation.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("duplicate effect error=%v", err)
	}

	operation = migrationImportOperationFixture()
	operation.Phase = MigrationPhaseComplete
	operation.Result = &MigrationOperationResult{Code: "migration.import.complete"}
	if err := operation.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("complete without commit error=%v", err)
	}

	export := migrationExportOperationFixture()
	if err := export.Validate(); err != nil {
		t.Fatalf("valid export: %v", err)
	}
	export.IdentityActions = operation.IdentityActions
	if err := export.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("export accepted destination identity actions: %v", err)
	}
}

func TestMigrationImportClaimsAreClosedAndNamesAreCaseCanonical(t *testing.T) {
	operation := migrationImportOperationFixture()
	for _, class := range []MigrationClaimClass{
		MigrationClaimDestinationName,
		MigrationClaimDestinationProfile,
		MigrationClaimDestinationControl,
		MigrationClaimBackendObject,
		MigrationClaimStagingRoot,
	} {
		t.Run(string(class), func(t *testing.T) {
			candidate := operation.Clone()
			for index, claim := range candidate.Claims {
				if claim.Class == class {
					candidate.Claims = append(candidate.Claims[:index], candidate.Claims[index+1:]...)
					break
				}
			}
			if err := candidate.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
				t.Fatalf("missing %s claim error=%v", class, err)
			}
		})
	}

	upper, err := NewMigrationClaim(MigrationClaimDestinationName, "Dev-Clone")
	if err != nil {
		t.Fatal(err)
	}
	lower, err := NewMigrationClaim(MigrationClaimDestinationName, "dev-clone")
	if err != nil {
		t.Fatal(err)
	}
	if upper.Key != lower.Key || upper.KeyDigest != lower.KeyDigest {
		t.Fatalf("case variants escaped one claim namespace: upper=%+v lower=%+v", upper, lower)
	}
	forged := upper
	forged.Key = "Dev-Clone"
	forged.KeyDigest = migrationClaimDigest(forged.Class, forged.Key)
	if err := forged.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("noncanonical destination claim error=%v", err)
	}
}

func migrationImportOperationFixture() MigrationOperation {
	now := time.Date(2026, 8, 2, 4, 0, 0, 0, time.UTC)
	profiles := migrationMaterializedProfilesFixture()
	profileSizes := make([]uint64, len(profiles))
	for index, materialized := range profiles {
		encoded, err := migration.EncodePortableProfile(materialized.Snapshot)
		if err != nil {
			panic(err)
		}
		profileSizes[index] = uint64(len(encoded))
		clear(encoded)
	}
	claimSpecs := []struct {
		class MigrationClaimClass
		key   string
	}{
		{MigrationClaimDestinationName, "dev-clone"},
		{MigrationClaimDestinationName, "dev-exact"},
		{MigrationClaimDestinationProfile, "dev-clone"},
		{MigrationClaimDestinationProfile, "dev-exact"},
		{MigrationClaimDestinationControl, "env_clone1234"},
		{MigrationClaimDestinationControl, "env_clone5678"},
		{MigrationClaimBackendObject, "backend_clone1234"},
		{MigrationClaimBackendObject, "backend_clone5678"},
		{MigrationClaimStagingRoot, "/tmp/hideout/migration/staging/op_migrationimport1"},
	}
	claims := make([]MigrationClaim, 0, len(claimSpecs))
	for _, spec := range claimSpecs {
		claim, err := NewMigrationClaim(spec.class, spec.key)
		if err != nil {
			panic(err)
		}
		claims = append(claims, claim)
	}
	SortMigrationClaims(claims)
	return MigrationOperation{
		Schema: MigrationOperationSchema, ID: "op_migrationimport1",
		Kind: MigrationOperationImport, PlanID: "plan_import1234",
		PlanDigest: migration.Digest("sha256:" + strings.Repeat("1", 64)),
		Bundle: MigrationOperationBundleBinding{
			BundleID: "migb_fixture1234", FormatVersion: migration.BundleFormatVersion,
			FileDigest:       migration.Digest("sha256:" + strings.Repeat("2", 64)),
			ManifestDigest:   migration.Digest("sha256:" + strings.Repeat("3", 64)),
			CompletionDigest: migration.Digest("sha256:" + strings.Repeat("4", 64)),
		},
		BundlePath: "/tmp/dev.hideout-migration",
		BundleFile: func() *MigrationBundleFileBinding {
			binding := migrationBundleFileBindingFixture()
			return &binding
		}(),
		BaseRevisions: []migration.BaseRevision{{
			Resource: "environment-store", Revision: 7,
			Digest: migration.Digest("sha256:" + strings.Repeat("5", 64)),
		}},
		CapabilityRevision: migration.Digest("sha256:" + strings.Repeat("6", 64)),
		Phase:              MigrationPhaseAwaitingConfirmation, Revision: 1,
		Claims: claims,
		Effects: []MigrationEffect{
			{ID: "effect_stage1234", Kind: MigrationEffectStage, Provider: "backend.lima", Status: MigrationEffectPending, Compensation: MigrationCompensationRollbackStage},
			{ID: "effect_adopt1234", Kind: MigrationEffectAdopt, Provider: "backend.lima", Status: MigrationEffectPending, Compensation: MigrationCompensationRollbackAdoption},
			{ID: "effect_verify1234", Kind: MigrationEffectVerify, Provider: "backend.lima", Status: MigrationEffectPending, Compensation: MigrationCompensationRollbackStage},
			{ID: "effect_activate1234", Kind: MigrationEffectActivate, Provider: "manager", Status: MigrationEffectPending, Compensation: MigrationCompensationDeactivate},
		},
		ImportObjects: []migration.ImportObject{
			{
				SourceRef: "source_environment1", DestinationName: "dev-clone",
				Mode: migration.ExportModeFull, DiskRefs: []migration.OpaqueID{"disk_source0001"},
			},
			{
				SourceRef: "source_environment2", DestinationName: "dev-exact",
				Mode: migration.ExportModeFull, DiskRefs: []migration.OpaqueID{"disk_source0002"},
			},
		},
		EnvironmentActions: []migration.EnvironmentAction{
			{
				SourceRef: "source_environment1", DestinationProfileName: "dev-clone",
				Runtime:   "linux",
				GuestUser: "developer", Backend: "lima",
				ProfileComponentID:        "component_profile0001",
				ProfileContentDigest:      profiles[0].ContentDigest,
				ProfileLogicalBytes:       profileSizes[0],
				ProfileStateComponentID:   "component_state0001",
				ProfileStateContentDigest: migration.Digest("sha256:" + strings.Repeat("c", 64)),
				ProfileStateLogicalBytes:  1024,
			},
			{
				SourceRef: "source_environment2", DestinationProfileName: "dev-exact",
				Runtime:   "linux",
				GuestUser: "developer", Backend: "lima",
				ProfileComponentID:        "component_profile0002",
				ProfileContentDigest:      profiles[1].ContentDigest,
				ProfileLogicalBytes:       profileSizes[1],
				ProfileStateComponentID:   "component_state0002",
				ProfileStateContentDigest: migration.Digest("sha256:" + strings.Repeat("d", 64)),
				ProfileStateLogicalBytes:  2048,
			},
		},
		ExpectedDisks: []migration.DiskObject{
			{
				DiskID: "disk_source0001", Role: migration.DiskRoleRoot, Format: "raw",
				LogicalBytes: 4096, AllocatedBytesHint: 4096,
				ContentDigest: migration.Digest("sha256:" + strings.Repeat("a", 64)),
				Provider: migration.ProviderDiskFacts{
					Name: "source-root-one", Kind: "lima-root", Features: []string{},
				},
			},
			{
				DiskID: "disk_source0002", Role: migration.DiskRoleRoot, Format: "raw",
				LogicalBytes: 8192, AllocatedBytesHint: 8192,
				ContentDigest: migration.Digest("sha256:" + strings.Repeat("b", 64)),
				Provider: migration.ProviderDiskFacts{
					Name: "source-root-two", Kind: "lima-root", Features: []string{},
				},
			},
		},
		ExpectedDiskEdges: []migration.DiskEdge{
			{
				EnvironmentRef: "source_environment1", DiskID: "disk_source0001",
				Attachment: migration.DiskRoleRoot, GuestPath: "/",
			},
			{
				EnvironmentRef: "source_environment2", DiskID: "disk_source0002",
				Attachment: migration.DiskRoleRoot, GuestPath: "/",
			},
		},
		IdentityActions: []migration.IdentityAction{
			{SourceRef: "source_environment1", GuestPolicy: migration.GuestIdentitySafeClone, FreshControlIdentity: true, FreshBackendIdentity: true},
			{SourceRef: "source_environment2", GuestPolicy: migration.GuestIdentityExactRestore, FreshControlIdentity: true, FreshBackendIdentity: true},
		},
		SourceGuestIdentities: []MigrationSourceGuestIdentity{
			{
				SourceRef: "source_environment1",
				Evidence: migration.GuestIdentityEvidence{
					MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("e", 64)),
					SSHHostKeyDigests: []migration.Digest{
						migration.Digest("sha256:" + strings.Repeat("f", 64)),
					},
				},
			},
			{
				SourceRef: "source_environment2",
				Evidence: migration.GuestIdentityEvidence{
					MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("7", 64)),
					SSHHostKeyDigests: []migration.Digest{
						migration.Digest("sha256:" + strings.Repeat("8", 64)),
					},
				},
			},
		},
		AdoptionHelper: &migration.HelperBinding{
			PackageID: migration.AdoptionHelperPackage, Version: "1.0.0",
			SHA256: migration.Digest("sha256:" + strings.Repeat("9", 64)),
		},
		DestinationIdentities: []MigrationDestinationIdentity{
			{
				SourceRef: "source_environment1", ControlIdentity: "env_clone1234",
				BackendIdentity: "backend_clone1234",
			},
			{
				SourceRef: "source_environment2", ControlIdentity: "env_clone5678",
				BackendIdentity: "backend_clone5678",
			},
		},
		DestinationDiskIdentities: []MigrationDestinationDiskIdentity{
			{
				DiskID: "disk_source0001", Role: migration.DiskRoleRoot,
				BackendIdentity: "backend_clone1234",
			},
			{
				DiskID: "disk_source0002", Role: migration.DiskRoleRoot,
				BackendIdentity: "backend_clone5678",
			},
		},
		Recovery:  MigrationRecovery{Code: migrationRecoveryCodeNone, Action: MigrationRecoveryNone},
		CreatedAt: now, UpdatedAt: now,
	}
}

func migrationImportOperationFixtureWithID(id string) MigrationOperation {
	operation := migrationImportOperationFixture()
	operation.ID = id
	for index, claim := range operation.Claims {
		if claim.Class != MigrationClaimStagingRoot {
			continue
		}
		replacement, err := NewMigrationClaim(
			MigrationClaimStagingRoot,
			"/tmp/hideout/migration/staging/"+id,
		)
		if err != nil {
			panic(err)
		}
		operation.Claims[index] = replacement
	}
	SortMigrationClaims(operation.Claims)
	return operation
}

func migrationExportOperationFixture() MigrationOperation {
	operation := migrationImportOperationFixture()
	operation.ID = "op_migrationexport1"
	operation.Kind = MigrationOperationExport
	operation.PlanID = "plan_export1234"
	operation.Phase = MigrationPhaseValidating
	operation.IdentityActions = nil
	operation.EnvironmentActions = nil
	operation.SourceGuestIdentities = nil
	operation.AdoptionHelper = nil
	operation.ImportObjects = nil
	operation.ExpectedDisks = nil
	operation.ExpectedDiskEdges = nil
	operation.DestinationIdentities = nil
	operation.DestinationDiskIdentities = nil
	operation.DestinationAdoption = nil
	operation.BundlePath = ""
	operation.BundleFile = nil
	operation.SourceInventoryDigest = migration.Digest("sha256:" + strings.Repeat("e", 64))
	operation.Bundle.FileDigest = ""
	operation.Bundle.ManifestDigest = ""
	operation.Bundle.CompletionDigest = ""
	operation.Claims = operation.Claims[:0]
	outputClaim, err := NewMigrationClaim(
		MigrationClaimOutputPath, "/tmp/dev.hideout-migration",
	)
	if err != nil {
		panic(err)
	}
	operation.Claims = append(operation.Claims, outputClaim)
	operation.Effects = []MigrationEffect{{
		ID: "effect_snapshot1234", Kind: MigrationEffectSnapshot,
		Provider: "backend.lima", Status: MigrationEffectPending,
		Compensation: MigrationCompensationReleaseSnapshot,
	}}
	return operation
}
