package manager

import (
	"context"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/secrets"
)

type migrationExportSecret struct {
	entry      migration.SecretEntry
	generation uint64
}

// inspectMigrationExportSecrets builds a value-free, closed inventory from the
// selected profiles. Secret values remain in the runtime provider; only an
// explicitly selected value receives an encrypted component identifier.
func (service MigrationService) inspectMigrationExportSecrets(
	ctx context.Context,
	planID migration.OpaqueID,
	source migrationExportSource,
	selectedRefs []string,
) ([]migrationExportSecret, []migration.BaseRevision, error) {
	selected := make(map[string]struct{}, len(selectedRefs))
	for _, ref := range selectedRefs {
		if secrets.ValidateRef(ref) != nil {
			return nil, nil, ErrMigrationRequestInvalid
		}
		selected[ref] = struct{}{}
	}
	environmentsByRef := make(map[string][]migration.OpaqueID)
	profiles := service.Profiles
	if profiles.Root == "" {
		profiles.Root = service.Environments.Root
	}
	for _, record := range source.records {
		profileValue, err := profiles.Load(record.Profile)
		if err != nil || profileValue.Identity.User != record.User {
			return nil, nil, ErrMigrationPlanStale
		}
		ref := profileValue.Network.ProxySecretRef
		if ref == "" {
			continue
		}
		if secrets.ValidateRef(ref) != nil {
			return nil, nil, ErrMigrationPlanStale
		}
		environmentsByRef[ref] = append(
			environmentsByRef[ref], migration.OpaqueID(record.ID),
		)
	}
	for ref := range selected {
		if len(environmentsByRef[ref]) == 0 {
			return nil, nil, ErrMigrationRequestInvalid
		}
	}
	if len(environmentsByRef) == 0 {
		return []migrationExportSecret{}, []migration.BaseRevision{}, nil
	}
	if service.Secrets == nil {
		return nil, nil, ErrMigrationCapabilityUnavailable
	}
	refs := make([]string, 0, len(environmentsByRef))
	for ref := range environmentsByRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	entries := make([]migrationExportSecret, 0, len(refs))
	revisions := make([]migration.BaseRevision, 0, len(refs))
	for _, ref := range refs {
		reference, err := service.Secrets.Reference(ctx, ref)
		if err != nil {
			return nil, nil, err
		}
		if reference.Validate() != nil || reference.Ref != ref ||
			reference.Provider != service.Secrets.Provider() ||
			reference.Availability != secrets.AvailabilityAvailable ||
			reference.Generation == 0 {
			return nil, nil, ErrMigrationCapabilityUnavailable
		}
		environmentRefs := append([]migration.OpaqueID(nil), environmentsByRef[ref]...)
		slices.Sort(environmentRefs)
		transfer := migration.SecretReferenceOnly
		componentID := migration.OpaqueID("")
		if _, included := selected[ref]; included {
			transfer = migration.SecretSelectedValue
			componentID = migrationExportDerivedID(
				"secretcomponent", string(planID), ref,
			)
		}
		entry := migration.SecretEntry{
			SecretRef: migration.OpaqueID(ref), DisplayName: "Hideout secret " + ref,
			Provider: reference.Provider, RequiredAvailability: secrets.AvailabilityAvailable,
			EnvironmentRefs: environmentRefs, Transfer: transfer,
			ValueComponentID: componentID,
		}
		digest, err := CanonicalDigest("migration-secret-reference-base", struct {
			Ref          string `json:"ref"`
			Provider     string `json:"provider"`
			Availability string `json:"availability"`
			Generation   uint64 `json:"generation"`
		}{ref, reference.Provider, reference.Availability, reference.Generation})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, migrationExportSecret{
			entry: entry, generation: reference.Generation,
		})
		revisions = append(revisions, migration.BaseRevision{
			Resource: "secret:" + ref, Revision: reference.Generation,
			Digest: migration.Digest(digest),
		})
	}
	return entries, revisions, nil
}
