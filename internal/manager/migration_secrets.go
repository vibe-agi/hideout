package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func (service MigrationImportService) planMigrationSecretActions(
	ctx context.Context,
	draft migration.ImportDraft,
	manifest migration.Manifest,
) ([]migration.SecretAction, []migration.OpaqueID, error) {
	selected := make(map[migration.OpaqueID]struct{}, len(draft.SelectedEnvironmentRefs))
	for _, ref := range draft.SelectedEnvironmentRefs {
		selected[ref] = struct{}{}
	}
	mappings := make(map[migration.OpaqueID]migration.SecretMapping, len(draft.SecretMappings))
	for _, mapping := range draft.SecretMappings {
		mappings[mapping.SourceRef] = mapping
	}
	actions := make([]migration.SecretAction, 0, len(manifest.SecretEntries))
	unresolved := make([]migration.OpaqueID, 0)
	for _, entry := range manifest.SecretEntries {
		environments := make([]migration.OpaqueID, 0, len(entry.EnvironmentRefs))
		for _, ref := range entry.EnvironmentRefs {
			if _, exists := selected[ref]; exists {
				environments = append(environments, ref)
			}
		}
		if len(environments) == 0 {
			continue
		}
		mapping, exists := mappings[entry.SecretRef]
		if !exists || mapping.Decision == migrationSecretDecisionUnresolved ||
			service.Secrets == nil {
			unresolved = append(unresolved, entry.SecretRef)
			continue
		}
		reference, err := service.Secrets.Reference(ctx, mapping.DestinationRef)
		if err != nil {
			return nil, nil, err
		}
		action := migration.SecretAction{
			SourceRef: entry.SecretRef, Transfer: entry.Transfer,
			Decision: mapping.Decision, SourceProvider: entry.Provider,
			DestinationProvider: service.Secrets.Provider(),
			DestinationRef:      mapping.DestinationRef, BaseGeneration: reference.Generation,
			ValueComponentID: entry.ValueComponentID,
			EnvironmentRefs:  append([]migration.OpaqueID(nil), environments...),
		}
		switch mapping.Decision {
		case migrationSecretDecisionExistingRef:
			if reference.Availability != secrets.AvailabilityAvailable ||
				reference.Generation == 0 {
				unresolved = append(unresolved, entry.SecretRef)
				continue
			}
		case migrationSecretDecisionImportValue:
			if entry.Transfer != migration.SecretSelectedValue ||
				entry.ValueComponentID == "" ||
				reference.Availability != secrets.AvailabilityMissing ||
				reference.Generation != 0 {
				unresolved = append(unresolved, entry.SecretRef)
				continue
			}
		default:
			return nil, nil, ErrMigrationRequestInvalid
		}
		actions = append(actions, action)
	}
	sort.Slice(actions, func(left, right int) bool {
		return actions[left].SourceRef < actions[right].SourceRef
	})
	slices.Sort(unresolved)
	unresolved = slices.Compact(unresolved)
	if err := validateMigrationSecretActions(actions); err != nil {
		return nil, nil, err
	}
	return actions, unresolved, nil
}

func (service MigrationImportService) revalidateMigrationSecretActions(
	ctx context.Context,
	plan migration.ImportPlan,
	manifest migration.Manifest,
) error {
	draft := migration.ImportDraft{
		SelectedEnvironmentRefs: migrationImportObjectRefs(plan.Objects),
		SecretMappings:          make([]migration.SecretMapping, len(plan.SecretActions)),
	}
	for index, action := range plan.SecretActions {
		draft.SecretMappings[index] = migration.SecretMapping{
			SourceRef: action.SourceRef, Decision: action.Decision,
			DestinationRef: action.DestinationRef,
		}
	}
	actions, unresolved, err := service.planMigrationSecretActions(ctx, draft, manifest)
	if err != nil {
		return err
	}
	if len(unresolved) != 0 || !migrationSecretActionsEqual(actions, plan.SecretActions) {
		return ErrMigrationPlanStale
	}
	return nil
}

func (service MigrationImportService) revalidateMigrationOperationSecretActions(
	ctx context.Context,
	operation MigrationOperation,
	manifest migration.Manifest,
) error {
	if !migrationHasImportedSecretValues(operation.SecretActions) {
		return service.revalidateMigrationSecretActions(ctx, migration.ImportPlan{
			Objects: operation.ImportObjects, SecretActions: operation.SecretActions,
		}, manifest)
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectPrepareSecret)
	if err != nil {
		return err
	}
	if effect.Status == MigrationEffectPending {
		return service.revalidateMigrationSecretActions(ctx, migration.ImportPlan{
			Objects: operation.ImportObjects, SecretActions: operation.SecretActions,
		}, manifest)
	}
	if service.Secrets == nil {
		return ErrMigrationCapabilityUnavailable
	}
	entries := make(map[migration.OpaqueID]migration.SecretEntry, len(manifest.SecretEntries))
	for _, entry := range manifest.SecretEntries {
		entries[entry.SecretRef] = entry
	}
	reconciler, reconcileAvailable := service.Secrets.(secrets.OperationReconciler)
	for _, action := range operation.SecretActions {
		entry, exists := entries[action.SourceRef]
		if !exists || entry.Transfer != action.Transfer || entry.Provider != action.SourceProvider ||
			entry.ValueComponentID != action.ValueComponentID ||
			!slices.Equal(entry.EnvironmentRefs, action.EnvironmentRefs) ||
			action.DestinationProvider != service.Secrets.Provider() {
			return ErrMigrationPlanStale
		}
		switch action.Decision {
		case migrationSecretDecisionExistingRef:
			reference, referenceErr := service.Secrets.Reference(ctx, action.DestinationRef)
			if referenceErr != nil || reference.Validate() != nil ||
				reference.Availability != secrets.AvailabilityAvailable ||
				reference.Generation != action.BaseGeneration ||
				reference.Provider != action.DestinationProvider {
				return ErrMigrationPlanStale
			}
		case migrationSecretDecisionImportValue:
			if !reconcileAvailable {
				return ErrMigrationCapabilityUnavailable
			}
			result, reconcileErr := reconciler.Reconcile(ctx, secrets.ReconcileRequest{
				Ref: action.DestinationRef, Action: secrets.ActionSet,
				OperationID: migrationImportSecretOperationID(
					operation.ID, action.DestinationRef,
				),
				ExpectedGeneration: action.BaseGeneration,
			})
			if reconcileErr != nil || result.Committed == result.Uncommitted ||
				result.Reference.Validate() != nil ||
				result.Reference.Ref != action.DestinationRef ||
				result.Reference.Provider != action.DestinationProvider {
				return ErrMigrationPlanStale
			}
			if result.Committed &&
				(result.Reference.Availability != secrets.AvailabilityAvailable ||
					result.Reference.Generation != action.BaseGeneration+1) {
				return ErrMigrationPlanStale
			}
			if result.Uncommitted &&
				(result.Reference.Availability != secrets.AvailabilityMissing ||
					result.Reference.Generation != action.BaseGeneration) {
				return ErrMigrationPlanStale
			}
		default:
			return ErrMigrationPlanStale
		}
	}
	return nil
}

func cloneMigrationSecretActions(actions []migration.SecretAction) []migration.SecretAction {
	cloned := append([]migration.SecretAction(nil), actions...)
	for index := range cloned {
		cloned[index].EnvironmentRefs = append(
			[]migration.OpaqueID(nil), actions[index].EnvironmentRefs...,
		)
	}
	return cloned
}

func migrationSecretActionsEqual(left, right []migration.SecretAction) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftAction, rightAction := left[index], right[index]
		if leftAction.SourceRef != rightAction.SourceRef ||
			leftAction.Transfer != rightAction.Transfer ||
			leftAction.Decision != rightAction.Decision ||
			leftAction.SourceProvider != rightAction.SourceProvider ||
			leftAction.DestinationProvider != rightAction.DestinationProvider ||
			leftAction.DestinationRef != rightAction.DestinationRef ||
			leftAction.BaseGeneration != rightAction.BaseGeneration ||
			leftAction.ValueComponentID != rightAction.ValueComponentID ||
			!slices.Equal(leftAction.EnvironmentRefs, rightAction.EnvironmentRefs) {
			return false
		}
	}
	return true
}

func validateMigrationSecretActions(actions []migration.SecretAction) error {
	var previous migration.OpaqueID
	environmentDestinations := make(map[migration.OpaqueID]string)
	destinationRefs := make(map[string]struct{})
	for _, action := range actions {
		if !migrationValidOperationOpaqueID(action.SourceRef) ||
			(previous != "" && previous >= action.SourceRef) ||
			!migrationProviderNamePattern.MatchString(action.SourceProvider) ||
			!migrationProviderNamePattern.MatchString(action.DestinationProvider) ||
			!migrationDestinationNamePattern.MatchString(action.DestinationRef) ||
			validateSortedMigrationOpaqueIDs(action.EnvironmentRefs, false) != nil {
			return ErrMigrationPlanInvalid
		}
		switch action.Decision {
		case migrationSecretDecisionExistingRef:
			if action.BaseGeneration == 0 ||
				(action.Transfer != migration.SecretReferenceOnly &&
					action.Transfer != migration.SecretSelectedValue &&
					action.Transfer != migration.SecretNonExportable) {
				return ErrMigrationPlanInvalid
			}
		case migrationSecretDecisionImportValue:
			if action.BaseGeneration != 0 || action.Transfer != migration.SecretSelectedValue ||
				!migrationValidOperationOpaqueID(action.ValueComponentID) {
				return ErrMigrationPlanInvalid
			}
		default:
			return ErrMigrationPlanInvalid
		}
		if _, duplicate := destinationRefs[action.DestinationRef]; duplicate {
			return ErrMigrationPlanInvalid
		}
		destinationRefs[action.DestinationRef] = struct{}{}
		for _, environmentRef := range action.EnvironmentRefs {
			if existing, exists := environmentDestinations[environmentRef]; exists &&
				existing != action.DestinationRef {
				return ErrMigrationPlanInvalid
			}
			environmentDestinations[environmentRef] = action.DestinationRef
		}
		previous = action.SourceRef
	}
	return nil
}

func migrationImportSecretOperationID(operationID, destinationRef string) string {
	digest := sha256.Sum256([]byte(
		"hideout-migration-import-secret/v1\x00" + operationID + "\x00" + destinationRef,
	))
	return "op_migration_secret_" + hex.EncodeToString(digest[:24])
}

func migrationImportSecretDeleteOperationID(operationID, destinationRef string) string {
	digest := sha256.Sum256([]byte(
		"hideout-migration-import-secret-delete/v1\x00" + operationID + "\x00" + destinationRef,
	))
	return "op_migration_secret_delete_" + hex.EncodeToString(digest[:24])
}
