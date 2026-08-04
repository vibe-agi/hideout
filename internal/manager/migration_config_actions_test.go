package manager

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestMigrationDestinationProfilesUseImportNamesAndFreshMetadata(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhasePreparingSecrets
	operation.Effects[0].Status = MigrationEffectSucceeded
	stage := migrationDestinationStageStateFixture()
	operation.DestinationStage = &stage
	if err := operation.Validate(); err != nil {
		t.Fatal(err)
	}

	profiles, err := migrationDestinationProfiles(operation)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[0].Name != "dev-clone" ||
		profiles[1].Name != "dev-exact" {
		t.Fatalf("destination profile names=%v", []string{profiles[0].Name, profiles[1].Name})
	}
	for _, value := range profiles {
		if value.Metadata["profileId"] == "" || value.Metadata["identityId"] == "" ||
			value.Metadata["machineId"] == "" ||
			value.Metadata["lineageMode"] != "migration" ||
			value.Metadata["createdFrom"] != operation.ID ||
			value.Network.ProxySecretRef != "" ||
			value.Network.MediatedResolver != "" ||
			len(value.Policy.ScriptRefs) != 0 ||
			len(value.HostFS.Grants) != 0 ||
			len(value.EndpointExposure.HostToGuest) != 0 {
			t.Fatalf("destination profile retained identity or authority: %+v", value)
		}
	}
	if profiles[0].Metadata["profileId"] == profiles[1].Metadata["profileId"] ||
		profiles[0].Metadata["identityId"] == profiles[1].Metadata["identityId"] ||
		profiles[0].Metadata["machineId"] == profiles[1].Metadata["machineId"] {
		t.Fatalf("import environments shared fresh metadata: %+v", profiles)
	}

	profiles[0].Env.Deny = append(profiles[0].Env.Deny, "MUTATED")
	rebuilt, err := migrationDestinationProfiles(operation)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range rebuilt[0].Env.Deny {
		if name == "MUTATED" {
			t.Fatal("destination profile aliases a prior derived result")
		}
	}
	if rebuilt[0].Metadata["profileId"] != profiles[0].Metadata["profileId"] {
		t.Fatal("replaying one import changed its destination profile identity")
	}
	other := migrationImportOperationFixtureWithID("op_migrationimport2")
	other.Phase = MigrationPhasePreparingSecrets
	other.Effects[0].Status = MigrationEffectSucceeded
	otherStage := migrationDestinationStageStateFixture()
	other.DestinationStage = &otherStage
	otherProfiles, err := migrationDestinationProfiles(other)
	if err != nil {
		t.Fatal(err)
	}
	if otherProfiles[0].Metadata["profileId"] == profiles[0].Metadata["profileId"] ||
		otherProfiles[0].Metadata["identityId"] == profiles[0].Metadata["identityId"] ||
		otherProfiles[0].Metadata["machineId"] == profiles[0].Metadata["machineId"] {
		t.Fatal("separate imports reused destination profile identity")
	}
}

func TestMigrationDestinationProfilesRejectTamperedStageBinding(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhasePreparingSecrets
	operation.Effects[0].Status = MigrationEffectSucceeded
	stage := migrationDestinationStageStateFixture()
	stage.Profiles[0].ComponentID = migration.OpaqueID("component_substitute1")
	operation.DestinationStage = &stage

	if _, err := migrationDestinationProfiles(operation); !errors.Is(
		err, ErrMigrationOperationInvalid,
	) {
		t.Fatalf("tampered profile binding error=%v", err)
	}
}

func TestMigrationDestinationEnvironmentRecordsResetConfigurationAtImport(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhasePreparingSecrets
	operation.Effects[0].Status = MigrationEffectSucceeded
	stage := migrationDestinationStageStateFixture()
	operation.DestinationStage = &stage
	profiles, err := migrationDestinationProfiles(operation)
	if err != nil {
		t.Fatal(err)
	}

	records, err := migrationDestinationEnvironmentRecords(operation, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("destination records=%d", len(records))
	}
	byName := make(map[string]environment.Record, len(records))
	for _, record := range records {
		byName[record.Name] = record
		if record.Mode != environment.ModeDedicatedPortal || record.AutoNamed ||
			record.HostWorkspace() != "" || record.GuestWorkspaceRoot() != "" ||
			record.Status != environment.StatusStopped {
			t.Fatalf("unmapped import gained static host authority: %+v", record)
		}
		profile := profileNamed(t, profiles, record.Profile)
		configuration, err := RuntimeConfigurationForProfile(
			profile, record.Backend, environment.ModeDedicatedPortal,
		)
		if err != nil {
			t.Fatal(err)
		}
		if record.MachineIdentityID != configuration.Layers.MachineID ||
			record.BootConfigurationID != configuration.Layers.BootID {
			t.Fatalf("record used a pre-materialization identity: %+v", record)
		}
	}
	if byName["dev-clone"].ID != "env_clone1234" ||
		byName["dev-clone"].InstanceName != "backend_clone1234" ||
		byName["dev-exact"].ID != "env_clone5678" ||
		byName["dev-exact"].InstanceName != "backend_clone5678" {
		t.Fatalf("destination control/backend identities drifted: %+v", records)
	}

	other := migrationImportOperationFixtureWithID("op_migrationimport2")
	other.Phase = MigrationPhasePreparingSecrets
	other.Effects[0].Status = MigrationEffectSucceeded
	otherStage := migrationDestinationStageStateFixture()
	other.DestinationStage = &otherStage
	otherProfiles, err := migrationDestinationProfiles(other)
	if err != nil {
		t.Fatal(err)
	}
	otherRecords, err := migrationDestinationEnvironmentRecords(other, otherProfiles)
	if err != nil {
		t.Fatal(err)
	}
	if otherRecords[0].MachineIdentityID == records[0].MachineIdentityID {
		t.Fatal("independent imports reused the destination machine configuration identity")
	}
}

func TestMigrationDestinationEnvironmentRecordsUseOnlyReviewedDestinationMapping(t *testing.T) {
	operation := migrationImportOperationFixture()
	workspace := filepath.Clean(t.TempDir())
	canonical, identity, err := workspaceattach.CaptureRootIdentity(workspace)
	if err != nil {
		t.Fatal(err)
	}
	operation.WorkspaceActions = []migration.WorkspaceAction{{
		ProposalID: "workspace_proposal1", EnvironmentRef: "source_environment1",
		GuestPath: "/workspace", Decision: migrationWorkspaceDecisionMapped,
		DestinationPath: canonical, RootDevice: identity.Device, RootInode: identity.Inode,
	}}
	claim, err := NewMigrationClaim(MigrationClaimDestinationWorkspace, canonical)
	if err != nil {
		t.Fatal(err)
	}
	operation.Claims = append(operation.Claims, claim)
	SortMigrationClaims(operation.Claims)
	operation.Phase = MigrationPhasePreparingSecrets
	operation.Effects[0].Status = MigrationEffectSucceeded
	stage := migrationDestinationStageStateFixture()
	operation.DestinationStage = &stage
	profiles, err := migrationDestinationProfiles(operation)
	if err != nil {
		t.Fatal(err)
	}
	records, err := migrationDestinationEnvironmentRecords(operation, profiles)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]environment.Record, len(records))
	for _, record := range records {
		byName[record.Name] = record
	}
	mapped := byName["dev-clone"]
	if mapped.Mode != environment.ModeDedicated || mapped.DedicatedWorkspace != canonical ||
		mapped.DedicatedGuestRoot != "/workspace" {
		t.Fatalf("reviewed destination mapping not frozen exactly: %+v", mapped)
	}
	unmapped := byName["dev-exact"]
	if unmapped.Mode != environment.ModeDedicatedPortal || unmapped.HostWorkspace() != "" {
		t.Fatalf("unmapped sibling gained host authority: %+v", unmapped)
	}
}

func TestMigrationWorkspaceActionsRejectMultipleStaticMappingsPerEnvironment(t *testing.T) {
	actions := []migration.WorkspaceAction{
		{ProposalID: "workspace_a", EnvironmentRef: "source_environment1", GuestPath: "/workspace", Decision: migrationWorkspaceDecisionMapped, DestinationPath: "/tmp/a", RootDevice: 1, RootInode: 1},
		{ProposalID: "workspace_b", EnvironmentRef: "source_environment1", GuestPath: "/other", Decision: migrationWorkspaceDecisionMapped, DestinationPath: "/tmp/b", RootDevice: 1, RootInode: 2},
	}
	if err := validateMigrationWorkspaceActions(actions); !errors.Is(err, ErrMigrationPlanInvalid) {
		t.Fatalf("multiple static mappings error=%v", err)
	}
}

func profileNamed(t *testing.T, profiles []profile.Profile, name string) profile.Profile {
	t.Helper()
	for _, value := range profiles {
		if value.Name == name {
			return value
		}
	}
	t.Fatalf("profile %q not found", name)
	return profile.Profile{}
}
