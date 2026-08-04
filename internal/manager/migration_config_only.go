package manager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
)

var migrationConfigExcludedClasses = []string{
	"activity-history",
	"audit-history",
	"caches",
	"command-history",
	"host-runtime-identity",
	"host-workspace-content",
	"hostfs-content",
	"logs",
	"memory-state",
	"process-state",
	"runtime-state",
	"unselected-secret-values",
}

type migrationConfigInventory struct {
	Schema       string                   `json:"schema"`
	Capability   migration.Digest         `json:"capabilityRevision"`
	Environments []migration.OpaqueID     `json:"environments"`
	Revisions    []migration.BaseRevision `json:"revisions"`
	Included     []string                 `json:"included"`
	Excluded     []string                 `json:"excluded"`
}

func (service MigrationService) planConfigExport(
	ctx context.Context,
	request migration.ExportRequest,
) (migration.ExportPlan, error) {
	capability, err := service.configMigrationCapability(ctx)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	source, digest, err := service.resolveConfigExportNames(
		request.EnvironmentNames, capability.Revision,
	)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	planID, err := service.newMigrationID("migplan")
	if err != nil {
		return migration.ExportPlan{}, err
	}
	_, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, planID, source, request.IncludeSecretRefs,
	)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	source.revisions = append(source.revisions, secretRevisions...)
	effects, err := service.newExportPlannedEffects("manager")
	if err != nil {
		return migration.ExportPlan{}, err
	}
	review, err := service.buildMigrationExportPlanInventory(
		source, backend.SourceInventory{}, migration.ExportModeConfig,
		request.IncludeSecretRefs,
	)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	plan := migration.ExportPlan{
		Schema: MigrationExportPlanSchema, PlanID: planID,
		BaseRevisions:              append([]migration.BaseRevision(nil), source.revisions...),
		Mode:                       migration.ExportModeConfig,
		EnvironmentRefs:            migrationEnvironmentRefs(source.records),
		DiskRefs:                   []migration.OpaqueID{},
		SelectedSecretRefs:         append([]string(nil), request.IncludeSecretRefs...),
		ExcludedClasses:            append([]string(nil), migrationConfigExcludedClasses...),
		OutputPath:                 request.OutputPath,
		ProviderCapabilityRevision: capability.Revision,
		SourceInventoryDigest:      digest,
		Warnings:                   []migration.PlanNotice{},
		Effects:                    effects,
		ConfirmationText: fmt.Sprintf(
			"Export portable configuration for %d environment(s); VM disks, host workspaces, history, and caches are excluded; %d explicitly selected secret value(s) are encrypted into the bundle.",
			len(source.records), len(request.IncludeSecretRefs),
		),
		RiskAcknowledgements: append([]string(nil), request.RiskAcknowledgements...),
	}
	review.apply(&plan)
	if err := SealMigrationExportPlan(&plan); err != nil {
		return migration.ExportPlan{}, err
	}
	return plan, nil
}

func (service MigrationService) configMigrationCapability(
	ctx context.Context,
) (backend.MigrationCapabilities, error) {
	if service.Config == nil {
		return backend.MigrationCapabilities{}, ErrMigrationCapabilityUnavailable
	}
	capability, err := service.Config.MigrationCapabilities(ctx)
	if err != nil {
		return backend.MigrationCapabilities{}, err
	}
	if capability.Validate() != nil || capability.FullExport || capability.FullImport ||
		capability.Provider != "native" {
		return backend.MigrationCapabilities{}, ErrMigrationCapabilityUnavailable
	}
	return capability, nil
}

func (service MigrationService) resolveConfigExportNames(
	names []string,
	capability migration.Digest,
) (migrationExportSource, migration.Digest, error) {
	records := make([]environment.Record, 0, len(names))
	for _, name := range names {
		record, err := service.Environments.LoadByName(name)
		if err != nil {
			return migrationExportSource{}, "", err
		}
		records = append(records, record)
	}
	return service.configExportSource(records, capability)
}

func (service MigrationService) resolveConfigExportRefs(
	refs []migration.OpaqueID,
	capability migration.Digest,
) (migrationExportSource, migration.Digest, error) {
	records := make([]environment.Record, 0, len(refs))
	for _, ref := range refs {
		record, err := service.Environments.Load(string(ref))
		if err != nil {
			return migrationExportSource{}, "", err
		}
		records = append(records, record)
	}
	return service.configExportSource(records, capability)
}

func (service MigrationService) configExportSource(
	records []environment.Record,
	capability migration.Digest,
) (migrationExportSource, migration.Digest, error) {
	if len(records) == 0 || len(records) > int(migration.HardMaxEnvironments) ||
		capability.Validate() != nil {
		return migrationExportSource{}, "", ErrMigrationRequestInvalid
	}
	sort.Slice(records, func(left, right int) bool { return records[left].ID < records[right].ID })
	source := migrationExportSource{
		records:   append([]environment.Record(nil), records...),
		revisions: make([]migration.BaseRevision, 0, len(records)),
	}
	refs := make([]migration.OpaqueID, 0, len(records))
	previous := ""
	for _, record := range records {
		if record.Validate() != nil || record.ID <= previous || strings.TrimSpace(record.Profile) == "" {
			return migrationExportSource{}, "", ErrMigrationPlanStale
		}
		ref, err := migration.ParseOpaqueID(record.ID)
		if err != nil {
			return migrationExportSource{}, "", err
		}
		profileValue, err := service.Profiles.Load(record.Profile)
		if err != nil || profileValue.Identity.User != record.User {
			return migrationExportSource{}, "", ErrMigrationPlanStale
		}
		portable, err := migration.NormalizePortableProfile(profileValue)
		if err != nil {
			return migrationExportSource{}, "", err
		}
		digest, err := CanonicalDigest("migration-config-environment-base", struct {
			Record  environment.Record        `json:"record"`
			Profile migration.PortableProfile `json:"profile"`
		}{Record: record, Profile: portable})
		if err != nil {
			return migrationExportSource{}, "", err
		}
		revision := uint64(record.CreatedAt.UnixNano())
		if revision == 0 {
			revision = 1
		}
		source.revisions = append(source.revisions, migration.BaseRevision{
			Resource: "environment-config:" + record.ID,
			Revision: revision, Digest: migration.Digest(digest),
		})
		refs = append(refs, ref)
		previous = record.ID
	}
	inventory := migrationConfigInventory{
		Schema: "hideout.migration-config-inventory/v1", Capability: capability,
		Environments: refs,
		Revisions:    append([]migration.BaseRevision(nil), source.revisions...),
		Included:     append([]string(nil), migrationConfigIncludedClasses...),
		Excluded:     append([]string(nil), migrationConfigExcludedClasses...),
	}
	digest, err := CanonicalDigest("migration-config-inventory", inventory)
	if err != nil {
		return migrationExportSource{}, "", err
	}
	return source, migration.Digest(digest), nil
}

func (service MigrationService) revalidateConfigExportPlan(
	ctx context.Context,
	plan migration.ExportPlan,
) (migrationExportSource, error) {
	capability, err := service.configMigrationCapability(ctx)
	if err != nil || capability.Revision != plan.ProviderCapabilityRevision ||
		plan.Mode != migration.ExportModeConfig || len(plan.DiskRefs) != 0 {
		return migrationExportSource{}, ErrMigrationPlanStale
	}
	if err := inspectMigrationOutputPath(plan.OutputPath); err != nil {
		return migrationExportSource{}, err
	}
	source, digest, err := service.resolveConfigExportRefs(
		plan.EnvironmentRefs, capability.Revision,
	)
	if err != nil {
		return migrationExportSource{}, ErrMigrationPlanStale
	}
	_, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, plan.PlanID, source, plan.SelectedSecretRefs,
	)
	if err != nil {
		return migrationExportSource{}, err
	}
	source.revisions = append(source.revisions, secretRevisions...)
	if !reflect.DeepEqual(source.revisions, plan.BaseRevisions) ||
		digest != plan.SourceInventoryDigest ||
		!slices.Equal(plan.ExcludedClasses, migrationConfigExcludedClasses) {
		return migrationExportSource{}, ErrMigrationPlanStale
	}
	review, err := service.buildMigrationExportPlanInventory(
		source, backend.SourceInventory{}, migration.ExportModeConfig,
		plan.SelectedSecretRefs,
	)
	if err != nil || !review.matches(plan) {
		return migrationExportSource{}, ErrMigrationPlanStale
	}
	return source, nil
}

func migrationOperationConfigExport(operation MigrationOperation) bool {
	return operation.Kind == MigrationOperationExport &&
		len(migrationOperationClaimRefs(operation.Claims, MigrationClaimSourceDisk)) == 0
}

func (service MigrationService) snapshotConfigExportSource(
	ctx context.Context,
	operation MigrationOperation,
) (MigrationOperation, backend.SourceSnapshot, error) {
	if !migrationOperationConfigExport(operation) {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	var err error
	if operation.Phase == MigrationPhaseClaiming {
		if _, _, err = service.Store.AcquireClaims(operation.ID); err == nil {
			operation, err = service.Store.TransitionPhase(
				operation.ID, MigrationPhaseSnapshotting, nil,
			)
		}
	} else if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseSnapshotting, nil,
		)
	} else if operation.Phase != MigrationPhaseSnapshotting &&
		operation.Phase != MigrationPhaseWriting {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	capability, err := service.configMigrationCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationPlanStale
	}
	refs := migrationOperationClaimRefs(operation.Claims, MigrationClaimSourceEnvironment)
	source, digest, err := service.resolveConfigExportRefs(refs, capability.Revision)
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationPlanStale
	}
	_, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, operation.PlanID, source, operation.SelectedSecretRefs,
	)
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	source.revisions = append(source.revisions, secretRevisions...)
	if !reflect.DeepEqual(source.revisions, operation.BaseRevisions) ||
		digest != operation.SourceInventoryDigest {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationPlanStale
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectSnapshot)
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	if effect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(operation.ID, effect.ID, effect.Provider)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
		effect, err = migrationOperationEffect(operation, MigrationEffectSnapshot)
	}
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	if effect.Status == MigrationEffectRunning {
		operation, err = service.Store.FinishEffect(
			operation.ID, effect.ID, effect.Provider, MigrationEffectSucceeded,
			[]MigrationEffectEvidence{{
				Code: "migration.export.config_frozen", Digest: digest,
				Count: uint64(len(source.records)), ObservedAt: service.nowUTC(),
			}},
		)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
	} else if effect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	if operation.Phase == MigrationPhaseSnapshotting {
		operation, err = service.Store.TransitionPhase(operation.ID, MigrationPhaseWriting, nil)
	}
	return operation, backend.SourceSnapshot{}, err
}

func (service MigrationImportService) materializeConfigImportDestination(
	ctx context.Context,
	request MigrationImportMaterializeRequest,
	operation MigrationOperation,
	effect MigrationEffect,
) (MigrationOperation, backend.DestinationStage, error) {
	if !migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) || service.InspectionCache == nil ||
		service.SecretInputs == nil || operation.BundleFile == nil {
		return MigrationOperation{}, backend.DestinationStage{}, ErrMigrationRequestInvalid
	}
	var err error
	switch operation.Phase {
	case MigrationPhaseClaiming:
		operation, _, err = service.Store.AcquireClaims(operation.ID)
		if err == nil {
			operation, err = service.Store.TransitionPhase(
				operation.ID, MigrationPhaseMaterializing, nil,
			)
		}
	case MigrationPhaseRecoverableFailure:
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseMaterializing, nil,
		)
	case MigrationPhaseMaterializing:
	default:
		return MigrationOperation{}, backend.DestinationStage{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	bundleBinding := migration.BundleBinding{
		BundleID: operation.Bundle.BundleID, FormatVersion: operation.Bundle.FormatVersion,
		FileDigest: operation.Bundle.FileDigest, ManifestDigest: operation.Bundle.ManifestDigest,
		CompletionDigest: operation.Bundle.CompletionDigest,
	}
	inspection, err := service.InspectionCache.Get(bundleBinding, *operation.BundleFile)
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	if err := service.revalidateConfigImportOperation(ctx, operation, inspection.Manifest); err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	if effect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(
			operation.ID, effect.ID, effect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
		effect, err = migrationOperationEffect(operation, MigrationEffectStage)
	}
	if err != nil || effect.Status != MigrationEffectRunning {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	var prepareEffect MigrationEffect
	if migrationHasImportedSecretValues(operation.SecretActions) {
		prepareEffect, err = migrationOperationEffect(operation, MigrationEffectPrepareSecret)
		if err == nil && prepareEffect.Status == MigrationEffectPending {
			operation, _, err = service.Store.BeginEffect(
				operation.ID, prepareEffect.ID, prepareEffect.Provider,
			)
			if err == nil {
				prepareEffect, err = migrationOperationEffect(
					operation, MigrationEffectPrepareSecret,
				)
			}
		}
		if err != nil || prepareEffect.Status != MigrationEffectRunning {
			if err == nil {
				err = ErrMigrationOperationInvalid
			}
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
	}
	profiles, preparedSecrets, err := service.readConfigImportProfiles(
		ctx, operation, inspection.Manifest, request.SecretInputHandle, request.ClientBinding,
	)
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	if err := validateMigrationMaterializedProfiles(operation.EnvironmentActions, profiles); err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	handles, err := migrationImportExpectedObjectHandles(operation)
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	checkpoints := make([]MigrationDestinationStageCheckpoint, len(profiles))
	for index, profileValue := range profiles {
		encoded, encodeErr := migration.EncodePortableProfile(profileValue.Snapshot)
		if encodeErr != nil {
			return service.importMaterializationFailure(operation.ID, encodeErr)
		}
		checkpoints[index] = MigrationDestinationStageCheckpoint{
			ComponentID: profileValue.ComponentID, NextOffset: uint64(len(encoded)),
			ContentDigest: profileValue.ContentDigest,
		}
		clear(encoded)
	}
	stageHandle := migrationImportStageHandle(operation.ID)
	stageDigest, err := CanonicalDigest("migration-config-destination-stage", struct {
		StageHandle migration.OpaqueID                    `json:"stageHandle"`
		Handles     []migration.OpaqueID                  `json:"handles"`
		Checkpoints []MigrationDestinationStageCheckpoint `json:"checkpoints"`
		Profiles    []MigrationMaterializedProfile        `json:"profiles"`
		Secrets     []MigrationPreparedSecret             `json:"secrets,omitempty"`
	}{stageHandle, handles, checkpoints, profiles, preparedSecrets})
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	stage := MigrationDestinationStageState{
		StageHandle: stageHandle, ObjectHandles: handles, Checkpoints: checkpoints,
		Profiles:       cloneMigrationMaterializedProfiles(profiles),
		EvidenceDigest: migration.Digest(stageDigest),
	}
	operation, err = service.Store.FinishDestinationStage(
		operation.ID, effect.ID, effect.Provider, stage,
		[]MigrationEffectEvidence{{
			Code: "migration.import.config_stage_verified", OpaqueRef: stageHandle,
			Digest: stage.EvidenceDigest,
			Count:  uint64(len(checkpoints) + len(profiles)), ObservedAt: service.nowUTC(),
		}},
	)
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	if migrationHasImportedSecretValues(operation.SecretActions) {
		digest, digestErr := CanonicalDigest(
			"migration-import-prepared-secrets", preparedSecrets,
		)
		if digestErr != nil {
			return service.importMaterializationFailure(operation.ID, digestErr)
		}
		operation, err = service.Store.FinishPreparedSecrets(
			operation.ID, prepareEffect.ID, preparedSecrets,
			MigrationEffectEvidence{
				Code: "migration.import.secrets_prepared", Digest: migration.Digest(digest),
				Count: uint64(len(preparedSecrets)), ObservedAt: service.nowUTC(),
			},
		)
		if err != nil {
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
	} else if len(preparedSecrets) != 0 {
		return service.importMaterializationFailure(
			operation.ID, ErrMigrationOperationInvalid,
		)
	}
	progress := operation.Progress
	progress.CompletedLogicalBytes = progress.TotalLogicalBytes
	progress.ComponentsComplete = progress.ComponentsTotal
	progress.CheckpointAt = service.nowUTC()
	operation, _, err = service.Store.UpdateProgress(operation.ID, progress)
	if err == nil {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhasePreparingSecrets, nil,
		)
	}
	return operation, backend.DestinationStage{}, err
}

func (service MigrationImportService) readConfigImportProfiles(
	ctx context.Context,
	operation MigrationOperation,
	manifest migration.Manifest,
	secretHandle, clientBinding string,
) ([]MigrationMaterializedProfile, []MigrationPreparedSecret, error) {
	file, observedFile, public, err := openAndBindMigrationBundleFile(operation.BundlePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	if operation.BundleFile == nil || observedFile != *operation.BundleFile ||
		public.BundleID != operation.Bundle.BundleID ||
		public.FormatVersion != operation.Bundle.FormatVersion {
		return nil, nil, migration.ErrBundleChanged
	}
	binding := migration.BundleBinding{
		BundleID: operation.Bundle.BundleID, FormatVersion: operation.Bundle.FormatVersion,
		FileDigest: operation.Bundle.FileDigest, ManifestDigest: operation.Bundle.ManifestDigest,
		CompletionDigest: operation.Bundle.CompletionDigest,
	}
	if err := migration.VerifySealedBundleFile(ctx, file, observedFile.Size, binding); err != nil {
		return nil, nil, err
	}
	components := make(map[migration.OpaqueID]migration.ComponentIndexEntry, len(manifest.ComponentIndex))
	for _, component := range manifest.ComponentIndex {
		components[component.ComponentID] = component
	}
	selectedProfiles := make(map[migration.OpaqueID]struct{}, len(operation.EnvironmentActions))
	for _, action := range operation.EnvironmentActions {
		entry, exists := components[action.ProfileComponentID]
		if !exists || entry.Kind != "profile" || entry.RecordCount == 0 ||
			entry.LogicalBytes != action.ProfileLogicalBytes ||
			entry.ContentDigest != action.ProfileContentDigest {
			return nil, nil, ErrMigrationPlanStale
		}
		selectedProfiles[action.ProfileComponentID] = struct{}{}
	}
	var profiles []MigrationMaterializedProfile
	var preparedSecrets []MigrationPreparedSecret
	err = service.SecretInputs.Consume(MigrationSecretInputUse{
		Handle: secretHandle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: clientBinding, BundleID: binding.BundleID, BundleFile: &observedFile,
	}, func(passphrase []byte) error {
		reader, readerErr := migration.NewReader(file, observedFile.Size, passphrase)
		if readerErr != nil {
			return readerErr
		}
		defer reader.Close()
		preparer, prepareErr := newMigrationImportSecretPreparer(
			ctx, operation.ID, operation.SecretActions, manifest, service.Secrets,
		)
		if prepareErr != nil {
			return prepareErr
		}
		stream := &migrationBundleComponentStream{
			ctx: ctx, reader: reader, components: components,
			selected: map[migration.OpaqueID]struct{}{}, selectedProfiles: selectedProfiles,
			profiles:       make(map[migration.OpaqueID]MigrationMaterializedProfile, len(selectedProfiles)),
			secretPreparer: preparer, expected: binding,
		}
		if finishErr := stream.Finish(); finishErr != nil {
			return finishErr
		}
		profiles, readerErr = stream.Profiles()
		if readerErr != nil {
			return readerErr
		}
		preparedSecrets, readerErr = stream.PreparedSecrets()
		return readerErr
	})
	if err != nil {
		return nil, nil, err
	}
	after, afterPublic, err := bindOpenMigrationBundleFile(operation.BundlePath, file)
	if err != nil || after != observedFile || afterPublic != public {
		if err == nil {
			err = migration.ErrBundleChanged
		}
		return nil, nil, err
	}
	if err := migration.VerifySealedBundleFile(ctx, file, after.Size, binding); err != nil {
		return nil, nil, err
	}
	return profiles, preparedSecrets, nil
}

func (service MigrationImportService) revalidateConfigImportOperation(
	ctx context.Context,
	operation MigrationOperation,
	manifest migration.Manifest,
) error {
	capability, err := service.configMigrationCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision ||
		manifest.Validate(migration.DefaultLimits()) != nil ||
		len(manifest.DiskObjects) != 0 || len(manifest.DiskEdges) != 0 ||
		len(manifest.RequiredCapabilities) != 0 {
		return ErrMigrationPlanStale
	}
	base, err := service.destinationEnvironmentBaseRevision()
	if err != nil || len(operation.BaseRevisions) != 1 || operation.BaseRevisions[0] != base {
		return ErrMigrationPlanStale
	}
	for _, object := range operation.ImportObjects {
		if object.Mode != migration.ExportModeConfig {
			return ErrMigrationPlanStale
		}
		if _, err := service.Environments.LoadByName(object.DestinationName); err == nil {
			return ErrMigrationPlanStale
		} else if !errors.Is(err, environment.ErrNameNotFound) {
			return err
		}
		occupied, err := migrationDestinationProfileOccupied(service.Profiles, object.DestinationName)
		if err != nil || occupied {
			return ErrMigrationPlanStale
		}
	}
	plan := migration.ImportPlan{
		Objects: operation.ImportObjects, ConflictActions: operation.ConflictActions,
		EnvironmentActions: operation.EnvironmentActions,
		WorkspaceActions:   operation.WorkspaceActions, SecretActions: operation.SecretActions,
	}
	if revalidateMigrationEnvironmentActions(plan, manifest) != nil ||
		service.revalidateMigrationConflictActions(plan) != nil ||
		service.revalidateMigrationWorkspaceActions(plan, manifest) != nil ||
		service.revalidateMigrationOperationSecretActions(ctx, operation, manifest) != nil {
		return ErrMigrationPlanStale
	}
	return nil
}

func (service MigrationImportService) adoptConfigImportDestination(
	ctx context.Context,
	operation MigrationOperation,
) (MigrationOperation, []backend.DestinationAdoption, error) {
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, nil, err
	}
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil ||
		operation.AdoptionHelper != nil || operation.DestinationAdoption != nil {
		return MigrationOperation{}, nil, ErrMigrationOperationInvalid
	}
	stageEffect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil || stageEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, nil, ErrMigrationOperationInvalid
	}
	if migrationHasImportedSecretValues(operation.SecretActions) {
		prepareEffect, prepareErr := migrationOperationEffect(
			operation, MigrationEffectPrepareSecret,
		)
		if prepareErr != nil || prepareEffect.Status != MigrationEffectSucceeded {
			return MigrationOperation{}, nil, ErrMigrationOperationInvalid
		}
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectAdopt)
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	if effect.Status == MigrationEffectSucceeded {
		if operation.Phase == MigrationPhaseAdopting {
			operation, err = service.Store.TransitionPhase(
				operation.ID, MigrationPhaseVerifying, nil,
			)
		}
		return operation, nil, err
	}
	switch operation.Phase {
	case MigrationPhasePreparingSecrets, MigrationPhaseRecoverableFailure:
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseAdopting, nil,
		)
	case MigrationPhaseAdopting:
	default:
		return MigrationOperation{}, nil, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	capability, err := service.configMigrationCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision {
		return service.importAdoptionFailure(operation.ID, ErrMigrationPlanStale)
	}
	effect, err = migrationOperationEffect(operation, MigrationEffectAdopt)
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	if effect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(operation.ID, effect.ID, effect.Provider)
		if err != nil {
			return MigrationOperation{}, nil, err
		}
		effect, err = migrationOperationEffect(operation, MigrationEffectAdopt)
	}
	if err != nil || effect.Status != MigrationEffectRunning {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationOperation{}, nil, err
	}
	digest, err := CanonicalDigest("migration-config-fresh-identities", struct {
		Stage      migration.OpaqueID             `json:"stage"`
		Identities []MigrationDestinationIdentity `json:"identities"`
	}{operation.DestinationStage.StageHandle, operation.DestinationIdentities})
	if err != nil {
		return service.importAdoptionFailure(operation.ID, err)
	}
	operation, err = service.Store.FinishEffect(
		operation.ID, effect.ID, effect.Provider, MigrationEffectSucceeded,
		[]MigrationEffectEvidence{{
			Code:      "migration.import.config_identity_fresh",
			OpaqueRef: operation.DestinationStage.StageHandle,
			Digest:    migration.Digest(digest), Count: uint64(len(operation.ImportObjects)),
			ObservedAt: service.nowUTC(),
		}},
	)
	if err == nil {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseVerifying, nil,
		)
	}
	return operation, nil, err
}

func (service MigrationImportService) verifyConfigImportDestination(
	ctx context.Context,
	operation MigrationOperation,
) (MigrationOperation, backend.DestinationProof, error) {
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil ||
		operation.DestinationAdoption != nil || operation.AdoptionHelper != nil {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	stageEffect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil || stageEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	adoptEffect, err := migrationOperationEffect(operation, MigrationEffectAdopt)
	if err != nil || adoptEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	verifyEffect, err := migrationOperationEffect(operation, MigrationEffectVerify)
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	if verifyEffect.Status == MigrationEffectSucceeded {
		proof, proofErr := migrationDestinationVerificationProof(operation)
		return operation, proof, proofErr
	}
	if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseVerifying, nil,
		)
	} else if operation.Phase != MigrationPhaseVerifying {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	capability, err := service.configMigrationCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision ||
		validateMigrationMaterializedProfiles(
			operation.EnvironmentActions, operation.DestinationStage.Profiles,
		) != nil {
		return service.importVerificationFailure(operation.ID, ErrMigrationPlanStale)
	}
	verifyEffect, err = migrationOperationEffect(operation, MigrationEffectVerify)
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	if verifyEffect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(
			operation.ID, verifyEffect.ID, verifyEffect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, backend.DestinationProof{}, err
		}
		verifyEffect, err = migrationOperationEffect(operation, MigrationEffectVerify)
	}
	if err != nil || verifyEffect.Status != MigrationEffectRunning {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	digest, err := CanonicalDigest("migration-config-destination-verification", struct {
		Stage      MigrationDestinationStageState `json:"stage"`
		Identities []MigrationDestinationIdentity `json:"identities"`
	}{*operation.DestinationStage, operation.DestinationIdentities})
	if err != nil {
		return service.importVerificationFailure(operation.ID, err)
	}
	operation, err = service.Store.FinishEffect(
		operation.ID, verifyEffect.ID, verifyEffect.Provider,
		MigrationEffectSucceeded, []MigrationEffectEvidence{{
			Code:      migrationDestinationVerificationEvidenceCode,
			OpaqueRef: operation.DestinationStage.StageHandle,
			Digest:    migration.Digest(digest), Count: 0, ObservedAt: service.nowUTC(),
		}},
	)
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	proof, err := migrationDestinationVerificationProof(operation)
	return operation, proof, err
}

func (service MigrationImportService) compensateConfigImportState(
	operation MigrationOperation,
	stageEffect MigrationEffect,
	adoptEffect MigrationEffect,
) (MigrationOperation, error) {
	handles, err := migrationImportExpectedObjectHandles(operation)
	if err != nil {
		return MigrationOperation{}, err
	}
	stageHandle := migrationImportStageHandle(operation.ID)
	if operation.DestinationStage != nil &&
		(operation.DestinationStage.StageHandle != stageHandle ||
			!slices.Equal(operation.DestinationStage.ObjectHandles, handles)) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	digest, err := CanonicalDigest("migration-config-destination-rollback", struct {
		StageHandle migration.OpaqueID   `json:"stageHandle"`
		Handles     []migration.OpaqueID `json:"handles"`
	}{stageHandle, handles})
	if err != nil {
		return MigrationOperation{}, err
	}
	evidenceDigest := migration.Digest(digest)
	if adoptEffect.Status == MigrationEffectCompensating {
		operation, err = service.Store.FinishEffect(
			operation.ID, adoptEffect.ID, adoptEffect.Provider,
			MigrationEffectCompensated, []MigrationEffectEvidence{{
				Code: "migration.import.adoption_rolled_back", OpaqueRef: stageHandle,
				Digest: evidenceDigest, Count: uint64(len(operation.ImportObjects)),
				ObservedAt: service.nowUTC(),
			}},
		)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	stageEffect, err = migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		return MigrationOperation{}, err
	}
	if stageEffect.Status == MigrationEffectCompensating {
		operation, err = service.Store.FinishEffect(
			operation.ID, stageEffect.ID, stageEffect.Provider,
			MigrationEffectCompensated, []MigrationEffectEvidence{{
				Code: "migration.import.stage_rolled_back", OpaqueRef: stageHandle,
				Digest: evidenceDigest, Count: uint64(len(handles)),
				ObservedAt: service.nowUTC(),
			}},
		)
	}
	return operation, err
}
