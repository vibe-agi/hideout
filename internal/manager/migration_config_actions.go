package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

func validateMigrationMaterializedProfiles(
	actions []migration.EnvironmentAction,
	profiles []MigrationMaterializedProfile,
) error {
	expected := make(map[migration.OpaqueID]migration.EnvironmentAction, len(actions))
	for _, action := range actions {
		if existing, duplicate := expected[action.ProfileComponentID]; duplicate {
			if existing.ProfileContentDigest != action.ProfileContentDigest ||
				existing.ProfileLogicalBytes != action.ProfileLogicalBytes ||
				existing.GuestUser != action.GuestUser {
				return ErrMigrationOperationInvalid
			}
			continue
		}
		expected[action.ProfileComponentID] = action
	}
	if len(profiles) != len(expected) || len(profiles) == 0 {
		return ErrMigrationOperationInvalid
	}
	var previous migration.OpaqueID
	for _, materialized := range profiles {
		action, exists := expected[materialized.ComponentID]
		if !exists || materialized.Validate() != nil ||
			materialized.ContentDigest != action.ProfileContentDigest ||
			materialized.Snapshot.Identity.User != action.GuestUser ||
			(previous != "" && previous >= materialized.ComponentID) {
			return ErrMigrationOperationInvalid
		}
		encoded, err := migration.EncodePortableProfile(materialized.Snapshot)
		if err != nil {
			return ErrMigrationOperationInvalid
		}
		logicalBytes := uint64(len(encoded))
		clear(encoded)
		if logicalBytes != action.ProfileLogicalBytes {
			return ErrMigrationOperationInvalid
		}
		previous = materialized.ComponentID
	}
	return nil
}

func validateMigrationMaterializedProfileStates(
	operationID string,
	objects []migration.ImportObject,
	actions []migration.EnvironmentAction,
	states []MigrationMaterializedProfileState,
) error {
	if len(objects) != len(actions) {
		return ErrMigrationOperationInvalid
	}
	expected := make(map[migration.OpaqueID]migration.EnvironmentAction)
	for index, object := range objects {
		action := actions[index]
		if object.SourceRef != action.SourceRef {
			return ErrMigrationOperationInvalid
		}
		if object.Mode == migration.ExportModeConfig {
			continue
		}
		expected[action.SourceRef] = action
	}
	if len(states) != len(expected) {
		return ErrMigrationOperationInvalid
	}
	var previous migration.OpaqueID
	for _, state := range states {
		action, exists := expected[state.SourceRef]
		if !exists || state.Validate(operationID) != nil ||
			state.ProfileName != action.DestinationProfileName ||
			state.ComponentID != action.ProfileStateComponentID ||
			state.ContentDigest != action.ProfileStateContentDigest ||
			state.LogicalBytes != action.ProfileStateLogicalBytes ||
			(previous != "" && previous >= state.SourceRef) {
			return ErrMigrationOperationInvalid
		}
		previous = state.SourceRef
	}
	return nil
}

// migrationDestinationProfiles derives destination-owned, authority-stripped
// profiles from the authenticated snapshots retained in the private stage. The
// sealed bundle stays destination-neutral: names are taken from this import's
// immutable actions, while fresh profile/identity/machine metadata is left to
// profile.EnvironmentBatchParticipant at the visibility commit.
func migrationDestinationProfiles(
	operation MigrationOperation,
) ([]profile.Profile, error) {
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil ||
		operation.Validate() != nil {
		return nil, ErrMigrationOperationInvalid
	}
	materialized := make(
		map[migration.OpaqueID]MigrationMaterializedProfile,
		len(operation.DestinationStage.Profiles),
	)
	for _, value := range operation.DestinationStage.Profiles {
		if _, duplicate := materialized[value.ComponentID]; duplicate {
			return nil, ErrMigrationOperationInvalid
		}
		materialized[value.ComponentID] = value
	}
	secretRefs := make(map[migration.OpaqueID]string)
	for _, action := range operation.SecretActions {
		for _, environmentRef := range action.EnvironmentRefs {
			secretRefs[environmentRef] = action.DestinationRef
		}
	}
	profiles := make([]profile.Profile, len(operation.EnvironmentActions))
	for index, action := range operation.EnvironmentActions {
		value, exists := materialized[action.ProfileComponentID]
		if !exists {
			return nil, ErrMigrationOperationInvalid
		}
		destination, err := value.Snapshot.DestinationProfile(
			action.DestinationProfileName,
		)
		if err != nil || len(destination.Metadata) != 0 ||
			destination.Identity.User != action.GuestUser {
			return nil, ErrMigrationOperationInvalid
		}
		destination.Metadata = migrationDestinationProfileMetadata(
			operation, action.SourceRef,
		)
		destination.Network.ProxySecretRef = secretRefs[action.SourceRef]
		if err := applyMigrationAuthorityActions(
			&destination, action.SourceRef, operation.AuthorityActions,
			operation.SecretActions,
		); err != nil {
			return nil, err
		}
		if err := destination.Validate(); err != nil {
			return nil, ErrMigrationOperationInvalid
		}
		profiles[index] = destination
	}
	sort.Slice(profiles, func(left, right int) bool {
		return profiles[left].Name < profiles[right].Name
	})
	for index := 1; index < len(profiles); index++ {
		if profiles[index-1].Name == profiles[index].Name {
			return nil, ErrMigrationOperationInvalid
		}
	}
	return profiles, nil
}

func migrationDestinationProfileMetadata(
	operation MigrationOperation,
	sourceRef migration.OpaqueID,
) map[string]string {
	derive := func(domain string) string {
		digest := sha256.Sum256([]byte(
			domain + "\x00" + operation.ID + "\x00" + string(sourceRef),
		))
		return hex.EncodeToString(digest[:16])
	}
	return map[string]string{
		"profileId":   "prf_" + derive("hideout-migration-profile/v1"),
		"identityId":  "id_" + derive("hideout-migration-profile-identity/v1"),
		"machineId":   derive("hideout-migration-profile-machine/v1"),
		"createdAt":   operation.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"lineageMode": "migration",
		"createdFrom": operation.ID,
	}
}

// migrationDestinationEnvironmentRecords freezes the runnable Manager records
// only after authenticated profile materialization. Machine/boot configuration
// IDs are derived from the actual destination profile, never guessed at plan
// time. An explicitly mapped workspace becomes this destination's static
// binding; with no approved mapping the named persistent machine uses a
// session-scoped Workspace Portal and retains no host path.
func migrationDestinationEnvironmentRecords(
	operation MigrationOperation,
	profiles []profile.Profile,
) ([]environment.Record, error) {
	if operation.Kind != MigrationOperationImport || operation.Validate() != nil ||
		len(profiles) != len(operation.EnvironmentActions) || len(profiles) == 0 {
		return nil, ErrMigrationOperationInvalid
	}
	profilesByName := make(map[string]profile.Profile, len(profiles))
	for _, value := range profiles {
		key := strings.ToLower(value.Name)
		if value.Validate() != nil || value.Metadata["createdFrom"] != operation.ID ||
			profilesByName[key].Name != "" {
			return nil, ErrMigrationOperationInvalid
		}
		profilesByName[key] = value
	}
	mappedWorkspace := make(
		map[migration.OpaqueID]migration.WorkspaceAction,
		len(operation.WorkspaceActions),
	)
	for _, action := range operation.WorkspaceActions {
		if action.Decision != migrationWorkspaceDecisionMapped {
			continue
		}
		if _, duplicate := mappedWorkspace[action.EnvironmentRef]; duplicate {
			return nil, ErrMigrationOperationInvalid
		}
		mappedWorkspace[action.EnvironmentRef] = action
	}

	records := make([]environment.Record, len(operation.EnvironmentActions))
	for index, action := range operation.EnvironmentActions {
		object := operation.ImportObjects[index]
		identity := operation.DestinationIdentities[index]
		if object.SourceRef != action.SourceRef || identity.SourceRef != action.SourceRef ||
			object.DestinationName != action.DestinationProfileName {
			return nil, ErrMigrationOperationInvalid
		}
		destinationProfile, exists := profilesByName[strings.ToLower(action.DestinationProfileName)]
		if !exists || destinationProfile.Name != action.DestinationProfileName ||
			destinationProfile.Identity.User != action.GuestUser {
			return nil, ErrMigrationOperationInvalid
		}
		mode := environment.ModeDedicatedPortal
		dedicatedWorkspace := ""
		dedicatedGuestRoot := ""
		if workspace, mapped := mappedWorkspace[action.SourceRef]; mapped {
			mode = environment.ModeDedicated
			dedicatedWorkspace = workspace.DestinationPath
			dedicatedGuestRoot = workspace.GuestPath
		}
		configuration, err := RuntimeConfigurationForProfile(
			destinationProfile, action.Backend, mode,
		)
		if err != nil {
			return nil, ErrMigrationOperationInvalid
		}
		record := environment.Record{
			Version: environment.RecordVersion, ID: string(identity.ControlIdentity),
			Name: object.DestinationName, AutoNamed: false,
			ImageRef: destinationProfile.BaseImageOrBuiltin(),
			Runtime:  cloneRuntimeProvenance(destinationProfile.Environment.Runtime),
			Profile:  action.DestinationProfileName, Backend: action.Backend, Mode: mode,
			MachineIdentityID:   configuration.Layers.MachineID,
			BootConfigurationID: configuration.Layers.BootID,
			DedicatedWorkspace:  dedicatedWorkspace,
			DedicatedGuestRoot:  dedicatedGuestRoot,
			User:                action.GuestUser,
			Hostname:            destinationProfile.Identity.Hostname,
			InstanceName:        string(identity.BackendIdentity),
			Status:              environment.StatusStopped,
			CreatedAt:           operation.CreatedAt,
		}
		if object.Mode == migration.ExportModeConfig {
			record.Status = environment.StatusCreated
		}
		if err := record.Validate(); err != nil {
			return nil, ErrMigrationOperationInvalid
		}
		records[index] = record
	}
	sort.Slice(records, func(left, right int) bool {
		return records[left].ID < records[right].ID
	})
	return records, nil
}

func migrationImportEnvironmentActions(
	objects []migration.ImportObject,
	manifest migration.Manifest,
) ([]migration.EnvironmentAction, error) {
	environments := make(map[migration.OpaqueID]migration.EnvironmentSnapshot, len(manifest.Environments))
	for _, environment := range manifest.Environments {
		environments[environment.SourceEnvironmentRef] = environment
	}
	components := make(map[migration.OpaqueID]migration.ComponentIndexEntry, len(manifest.ComponentIndex))
	for _, component := range manifest.ComponentIndex {
		components[component.ComponentID] = component
	}
	actions := make([]migration.EnvironmentAction, len(objects))
	for index, object := range objects {
		environment, exists := environments[object.SourceRef]
		component, componentExists := components[environment.ProfileComponentID]
		if !exists || !componentExists || component.Kind != "profile" ||
			component.RecordCount == 0 || component.LogicalBytes == 0 ||
			component.LogicalBytes > migration.MaxPortableProfileBytes {
			return nil, ErrMigrationPlanInvalid
		}
		actions[index] = migration.EnvironmentAction{
			SourceRef: object.SourceRef, DestinationProfileName: object.DestinationName,
			Runtime:   environment.Runtime,
			GuestUser: environment.GuestUser, Backend: environment.Backend,
			ProfileComponentID:   environment.ProfileComponentID,
			ProfileContentDigest: component.ContentDigest,
			ProfileLogicalBytes:  component.LogicalBytes,
		}
		if object.Mode == migration.ExportModeFull {
			profileState, stateExists := components[environment.ProfileStateComponentID]
			if !stateExists || profileState.Kind != "profile-state" ||
				profileState.RecordCount == 0 || profileState.LogicalBytes == 0 {
				return nil, ErrMigrationPlanInvalid
			}
			actions[index].ProfileStateComponentID = environment.ProfileStateComponentID
			actions[index].ProfileStateContentDigest = profileState.ContentDigest
			actions[index].ProfileStateLogicalBytes = profileState.LogicalBytes
		}
	}
	if validateMigrationEnvironmentActions(objects, actions) != nil {
		return nil, ErrMigrationPlanInvalid
	}
	return actions, nil
}

func revalidateMigrationEnvironmentActions(
	plan migration.ImportPlan,
	manifest migration.Manifest,
) error {
	expected, err := migrationImportEnvironmentActions(plan.Objects, manifest)
	if err != nil || !slices.Equal(expected, plan.EnvironmentActions) {
		return ErrMigrationPlanStale
	}
	return nil
}
