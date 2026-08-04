package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
)

type MigrationImportMaterializeRequest struct {
	OperationID       string
	SecretInputHandle string
	ClientBinding     string
}

type MigrationImportRollbackRequest struct {
	OperationID string
}

// MaterializeImportDestination executes only the stage-destination effect. It
// can be replayed under the same operation/effect binding, keeps destination
// objects invisible, and consumes an import passphrase only when authenticated
// component bytes are actually needed.
func (service MigrationImportService) MaterializeImportDestination(
	ctx context.Context,
	request MigrationImportMaterializeRequest,
) (MigrationOperation, backend.DestinationStage, error) {
	if ctx == nil || !operationIDPattern.MatchString(request.OperationID) {
		return MigrationOperation{}, backend.DestinationStage{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	operation, err := service.Store.Load(request.OperationID)
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	if operation.Kind != MigrationOperationImport {
		return MigrationOperation{}, backend.DestinationStage{}, ErrMigrationOperationInvalid
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	if effect.Status == MigrationEffectSucceeded {
		if migrationHasImportedSecretValues(operation.SecretActions) {
			prepareEffect, prepareErr := migrationOperationEffect(
				operation, MigrationEffectPrepareSecret,
			)
			if prepareErr != nil {
				return MigrationOperation{}, backend.DestinationStage{}, prepareErr
			}
			if prepareEffect.Status != MigrationEffectSucceeded {
				operation, prepareErr = service.PrepareImportSecrets(
					ctx, MigrationImportPrepareSecretsRequest{
						OperationID: operation.ID, SecretInputHandle: request.SecretInputHandle,
						ClientBinding: request.ClientBinding,
					},
				)
				return operation, backend.DestinationStage{}, prepareErr
			}
		}
		if operation.Phase == MigrationPhaseMaterializing {
			operation, err = service.Store.TransitionPhase(
				operation.ID, MigrationPhasePreparingSecrets, nil,
			)
		}
		return operation, backend.DestinationStage{}, err
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		return service.materializeConfigImportDestination(ctx, request, operation, effect)
	}
	if !migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) || service.InspectionCache == nil ||
		service.SecretInputs == nil || service.Import == nil {
		return MigrationOperation{}, backend.DestinationStage{}, ErrMigrationRequestInvalid
	}

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

	if operation.BundleFile == nil {
		return service.importMaterializationFailure(
			operation.ID, ErrMigrationOperationInvalid,
		)
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
	stageRequest, err := buildMigrationImportStageRequest(operation, inspection.Manifest)
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	if err := service.revalidateImportOperationDestination(
		ctx, operation, inspection.Manifest, stageRequest,
		effect.Status == MigrationEffectPending,
	); err != nil {
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
		if err != nil {
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
	}
	if effect.Status != MigrationEffectRunning {
		return MigrationOperation{}, backend.DestinationStage{}, ErrMigrationOperationInvalid
	}
	var prepareEffect MigrationEffect
	if migrationHasImportedSecretValues(operation.SecretActions) {
		prepareEffect, err = migrationOperationEffect(operation, MigrationEffectPrepareSecret)
		if err != nil {
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
		if prepareEffect.Status == MigrationEffectPending {
			operation, _, err = service.Store.BeginEffect(
				operation.ID, prepareEffect.ID, prepareEffect.Provider,
			)
			if err != nil {
				return MigrationOperation{}, backend.DestinationStage{}, err
			}
			prepareEffect, err = migrationOperationEffect(operation, MigrationEffectPrepareSecret)
		}
		if err != nil || prepareEffect.Status != MigrationEffectRunning {
			if err == nil {
				err = ErrMigrationOperationInvalid
			}
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
	}

	materialized, err := (MigrationMaterializationService{
		SecretInputs: service.SecretInputs, Cache: service.InspectionCache,
		Destination: service.Import, Secrets: service.Secrets,
	}).StageDestinationWithProfiles(ctx, MigrationDestinationMaterializeRequest{
		BundlePath: operation.BundlePath, ExpectedFile: *operation.BundleFile,
		ExpectedBinding: bundleBinding, SecretInputHandle: request.SecretInputHandle,
		ClientBinding: request.ClientBinding, Destination: stageRequest,
		OperationID: operation.ID, SecretActions: cloneMigrationSecretActions(operation.SecretActions),
	})
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	if err := validateMigrationMaterializedProfiles(
		operation.EnvironmentActions, materialized.Profiles,
	); err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	stageDigest, err := CanonicalDigest("migration-destination-stage", materialized)
	if err != nil {
		return service.importMaterializationFailure(operation.ID, err)
	}
	stageState := migrationDestinationStageState(
		materialized, migration.Digest(stageDigest),
	)
	operation, err = service.Store.FinishDestinationStage(
		operation.ID, effect.ID, effect.Provider, stageState,
		[]MigrationEffectEvidence{{
			Code: "migration.import.stage_verified", OpaqueRef: materialized.Stage.StageHandle,
			Digest:     migration.Digest(stageDigest),
			Count:      uint64(len(materialized.Stage.Checkpoints) + len(materialized.Profiles)),
			ObservedAt: service.nowUTC(),
		}},
	)
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	if migrationHasImportedSecretValues(operation.SecretActions) {
		digest, digestErr := CanonicalDigest(
			"migration-import-prepared-secrets", materialized.PreparedSecrets,
		)
		if digestErr != nil {
			return service.importMaterializationFailure(operation.ID, digestErr)
		}
		operation, err = service.Store.FinishPreparedSecrets(
			operation.ID, prepareEffect.ID, materialized.PreparedSecrets,
			MigrationEffectEvidence{
				Code: "migration.import.secrets_prepared", Digest: migration.Digest(digest),
				Count: uint64(len(materialized.PreparedSecrets)), ObservedAt: service.nowUTC(),
			},
		)
		if err != nil {
			return MigrationOperation{}, backend.DestinationStage{}, err
		}
	} else if len(materialized.PreparedSecrets) != 0 {
		return service.importMaterializationFailure(
			operation.ID, ErrMigrationOperationInvalid,
		)
	}
	progress := operation.Progress
	progress.CompletedLogicalBytes = progress.TotalLogicalBytes
	progress.ComponentsComplete = progress.ComponentsTotal
	progress.CheckpointAt = service.nowUTC()
	operation, _, err = service.Store.UpdateProgress(operation.ID, progress)
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	operation, err = service.Store.TransitionPhase(
		operation.ID, MigrationPhasePreparingSecrets, nil,
	)
	if err != nil {
		return MigrationOperation{}, backend.DestinationStage{}, err
	}
	return operation, materialized.Stage, nil
}

func migrationDestinationStageState(
	materialized MigrationDestinationMaterialization,
	evidenceDigest migration.Digest,
) MigrationDestinationStageState {
	stage := materialized.Stage
	checkpoints := make(
		[]MigrationDestinationStageCheckpoint, len(stage.Checkpoints),
	)
	for index, checkpoint := range stage.Checkpoints {
		checkpoints[index] = MigrationDestinationStageCheckpoint{
			ComponentID: checkpoint.ComponentID, NextOffset: checkpoint.NextOffset,
			ContentDigest: checkpoint.ContentDigest,
		}
	}
	return MigrationDestinationStageState{
		StageHandle:    stage.StageHandle,
		ObjectHandles:  append([]migration.OpaqueID(nil), stage.ObjectHandles...),
		Checkpoints:    checkpoints,
		Profiles:       cloneMigrationMaterializedProfiles(materialized.Profiles),
		EvidenceDigest: evidenceDigest,
	}
}

func (service MigrationImportService) importMaterializationFailure(
	operationID string,
	cause error,
) (MigrationOperation, backend.DestinationStage, error) {
	operation, transitionErr := service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
	if transitionErr != nil {
		return MigrationOperation{}, backend.DestinationStage{}, errors.Join(cause, transitionErr)
	}
	return operation, backend.DestinationStage{}, cause
}

func buildMigrationImportStageRequest(
	operation MigrationOperation,
	manifest migration.Manifest,
) (backend.DestinationStageRequest, error) {
	if err := operation.Validate(); err != nil || operation.Kind != MigrationOperationImport ||
		manifest.Validate(migration.DefaultLimits()) != nil {
		return backend.DestinationStageRequest{}, ErrMigrationOperationInvalid
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		return backend.DestinationStageRequest{}, err
	}
	disks, edges, err := migrationImportSelectionFromPlan(migration.ImportPlan{
		Objects: operation.ImportObjects, IdentityActions: operation.IdentityActions,
	}, manifest)
	if err != nil {
		return backend.DestinationStageRequest{}, err
	}
	environments := make(map[migration.OpaqueID]migration.EnvironmentSnapshot, len(manifest.Environments))
	for _, environment := range manifest.Environments {
		environments[environment.SourceEnvironmentRef] = environment
	}
	architecture, ok := migrationDestinationGuestArchitecture(manifest.SourceProduct.GuestArch)
	if !ok {
		return backend.DestinationStageRequest{}, ErrMigrationPlanInvalid
	}
	objects := make([]backend.MigrationDestinationObject, len(operation.ImportObjects))
	for index, object := range operation.ImportObjects {
		environment, exists := environments[object.SourceRef]
		action := operation.EnvironmentActions[index]
		if !exists || operation.DestinationIdentities[index].SourceRef != object.SourceRef ||
			action.SourceRef != object.SourceRef || action.Runtime != environment.Runtime ||
			action.GuestUser != environment.GuestUser || action.Backend != environment.Backend ||
			action.ProfileComponentID != environment.ProfileComponentID {
			return backend.DestinationStageRequest{}, ErrMigrationPlanInvalid
		}
		objects[index] = backend.MigrationDestinationObject{
			EnvironmentRef:  object.SourceRef,
			BackendIdentity: operation.DestinationIdentities[index].BackendIdentity,
			Runtime:         action.Runtime, GuestArchitecture: architecture,
			GuestUser: action.GuestUser, ProfileComponent: action.ProfileComponentID,
			ImageProvenance: cloneMigrationImageProvenance(environment.ImageProvenance),
		}
	}
	componentByDisk := make(map[migration.OpaqueID]migration.ComponentIndexEntry, len(disks))
	for _, component := range manifest.ComponentIndex {
		if component.Kind == "disk" {
			componentByDisk[component.DiskID] = component
		}
	}
	destinationByDisk := make(
		map[migration.OpaqueID]MigrationDestinationDiskIdentity,
		len(operation.DestinationDiskIdentities),
	)
	for _, identity := range operation.DestinationDiskIdentities {
		destinationByDisk[identity.DiskID] = identity
	}
	components := make([]backend.MigrationDestinationComponent, len(disks))
	for index, disk := range disks {
		component, exists := componentByDisk[disk.DiskID]
		destination, destinationExists := destinationByDisk[disk.DiskID]
		if !exists || !destinationExists || destination.Role != disk.Role {
			return backend.DestinationStageRequest{}, ErrMigrationPlanInvalid
		}
		components[index] = backend.MigrationDestinationComponent{
			ComponentID: component.ComponentID, DiskID: disk.DiskID,
			BackendIdentity: destination.BackendIdentity, Kind: "disk",
			LogicalBytes: disk.LogicalBytes, ContentDigest: disk.ContentDigest,
		}
	}
	sort.Slice(components, func(left, right int) bool {
		return components[left].ComponentID < components[right].ComponentID
	})
	request := backend.DestinationStageRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
			CapabilityRevision: operation.CapabilityRevision,
		},
		StagingHandle: migrationImportStageHandle(operation.ID), Objects: objects,
		Disks: disks, Edges: edges, Components: components,
	}
	if err := validateMigrationDestinationAgainstManifest(request, manifest); err != nil {
		return backend.DestinationStageRequest{}, err
	}
	return request, nil
}

func cloneMigrationImageProvenance(value *migration.ImageProvenance) *migration.ImageProvenance {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func migrationImportStageHandle(operationID string) migration.OpaqueID {
	digest := sha256.Sum256([]byte("hideout-migration-stage/v1\x00" + operationID))
	return migration.OpaqueID("stage_" + hex.EncodeToString(digest[:20]))
}

// RollbackImportDestination removes only the exact backend identities frozen
// into this operation. It is usable for both a completed stage and a partial
// stage whose provider call failed before returning checkpoints.
func (service MigrationImportService) RollbackImportDestination(
	ctx context.Context,
	request MigrationImportRollbackRequest,
) (MigrationOperation, error) {
	if ctx == nil || !operationIDPattern.MatchString(request.OperationID) {
		return MigrationOperation{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, err
	}
	operation, err := service.Store.Load(request.OperationID)
	if err != nil {
		return MigrationOperation{}, err
	}
	if operation.Kind != MigrationOperationImport {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	configOnly := migrationImportObjectsConfigOnly(operation.ImportObjects)
	if !configOnly && service.Import == nil {
		return MigrationOperation{}, ErrMigrationCapabilityUnavailable
	}
	if operation.Phase == MigrationPhaseRolledBack {
		return operation, nil
	}
	if operation.Decision == nil {
		operation, _, err = service.Store.Decide(operation.ID, MigrationDecisionRollback)
	} else if operation.Decision.Value != MigrationDecisionRollback {
		return MigrationOperation{}, ErrMigrationDecisionConflict
	} else if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseRollingBack, nil,
		)
	} else if operation.Phase != MigrationPhaseRollingBack {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, err
	}
	operation, err = service.compensateImportVerification(operation)
	if err != nil {
		return MigrationOperation{}, err
	}
	operation, err = service.compensateImportSecrets(ctx, operation)
	if err != nil {
		return operation, err
	}
	operation, err = service.compensateImportProviderState(ctx, operation)
	if err != nil {
		return operation, err
	}
	return service.finishImportRollback(operation)
}

// Verification is read-only. Its compensation records that the proof can no
// longer authorize activation before provider-owned adoption/stage state is
// removed in reverse order.
func (service MigrationImportService) compensateImportVerification(
	operation MigrationOperation,
) (MigrationOperation, error) {
	effect, err := migrationOperationEffect(operation, MigrationEffectVerify)
	if err != nil {
		return MigrationOperation{}, err
	}
	if effect.Status == MigrationEffectPending || effect.Status == MigrationEffectCompensated {
		return operation, nil
	}
	if operation.DestinationStage == nil {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	operation, _, err = service.Store.BeginEffectCompensation(
		operation.ID, effect.ID, effect.Provider,
	)
	if err != nil {
		return MigrationOperation{}, err
	}
	effect, err = migrationOperationEffect(operation, MigrationEffectVerify)
	if err != nil {
		return MigrationOperation{}, err
	}
	if effect.Status == MigrationEffectCompensated {
		return operation, nil
	}
	binding := struct {
		OperationID migration.OpaqueID `json:"operationId"`
		EffectID    migration.OpaqueID `json:"effectId"`
		StageHandle migration.OpaqueID `json:"stageHandle"`
	}{
		OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
		StageHandle: operation.DestinationStage.StageHandle,
	}
	digest, err := CanonicalDigest("migration-verification-rollback", binding)
	if err != nil {
		return MigrationOperation{}, err
	}
	return service.Store.FinishEffect(
		operation.ID, effect.ID, effect.Provider, MigrationEffectCompensated,
		[]MigrationEffectEvidence{{
			Code:      "migration.import.verification_rolled_back",
			OpaqueRef: operation.DestinationStage.StageHandle,
			Digest:    migration.Digest(digest), Count: uint64(len(operation.ExpectedDisks)),
			ObservedAt: service.nowUTC(),
		}},
	)
}

// Adoption and stage cleanup share one provider rollback primitive. Manager
// starts both compensations before the call, binds cleanup to the stage effect
// that owns owner.json, then records adoption before stage. A crash at any cut
// safely replays the provider's idempotent exact-handle cleanup.
func (service MigrationImportService) compensateImportProviderState(
	ctx context.Context,
	operation MigrationOperation,
) (MigrationOperation, error) {
	stageEffect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		return MigrationOperation{}, err
	}
	adoptEffect, err := migrationOperationEffect(operation, MigrationEffectAdopt)
	if err != nil {
		return MigrationOperation{}, err
	}
	if migrationEffectNeedsCompensation(adoptEffect.Status) {
		operation, _, err = service.Store.BeginEffectCompensation(
			operation.ID, adoptEffect.ID, adoptEffect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	stageEffect, err = migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		return MigrationOperation{}, err
	}
	if migrationEffectNeedsCompensation(stageEffect.Status) {
		operation, _, err = service.Store.BeginEffectCompensation(
			operation.ID, stageEffect.ID, stageEffect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	stageEffect, err = migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil {
		return MigrationOperation{}, err
	}
	adoptEffect, err = migrationOperationEffect(operation, MigrationEffectAdopt)
	if err != nil {
		return MigrationOperation{}, err
	}
	if stageEffect.Status != MigrationEffectCompensating &&
		adoptEffect.Status != MigrationEffectCompensating {
		return operation, nil
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		return service.compensateConfigImportState(operation, stageEffect, adoptEffect)
	}
	if service.Import == nil {
		return MigrationOperation{}, ErrMigrationCapabilityUnavailable
	}

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
	rollback := backend.DestinationRollbackRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID: migration.OpaqueID(operation.ID), EffectID: stageEffect.ID,
			CapabilityRevision: operation.CapabilityRevision,
		},
		StageHandle: stageHandle, ObjectHandles: handles,
	}
	if err := rollback.Validate(); err != nil {
		return MigrationOperation{}, err
	}
	if err := service.Import.RollbackMigrationDestination(ctx, rollback); err != nil {
		failed, transitionErr := service.Store.TransitionPhase(
			operation.ID, MigrationPhaseRecoverableFailure, nil,
		)
		if transitionErr != nil {
			return MigrationOperation{}, errors.Join(err, transitionErr)
		}
		return failed, err
	}
	rollbackDigest, err := CanonicalDigest("migration-destination-rollback", rollback)
	if err != nil {
		return MigrationOperation{}, err
	}
	if adoptEffect.Status == MigrationEffectCompensating {
		count := uint64(0)
		if operation.DestinationAdoption != nil {
			count = uint64(len(operation.DestinationAdoption.Records))
		}
		operation, err = service.Store.FinishEffect(
			operation.ID, adoptEffect.ID, adoptEffect.Provider, MigrationEffectCompensated,
			[]MigrationEffectEvidence{{
				Code: "migration.import.adoption_rolled_back", OpaqueRef: stageHandle,
				Digest: migration.Digest(rollbackDigest), Count: count,
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
			operation.ID, stageEffect.ID, stageEffect.Provider, MigrationEffectCompensated,
			[]MigrationEffectEvidence{{
				Code: "migration.import.stage_rolled_back", OpaqueRef: stageHandle,
				Digest: migration.Digest(rollbackDigest), Count: uint64(len(handles)),
				ObservedAt: service.nowUTC(),
			}},
		)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	return operation, nil
}

func migrationEffectNeedsCompensation(status MigrationEffectStatus) bool {
	switch status {
	case MigrationEffectRunning, MigrationEffectSucceeded,
		MigrationEffectFailed, MigrationEffectCompensating, MigrationEffectUnproved:
		return true
	default:
		return false
	}
}

func migrationImportExpectedObjectHandles(
	operation MigrationOperation,
) ([]migration.OpaqueID, error) {
	if err := operation.Validate(); err != nil || operation.Kind != MigrationOperationImport {
		return nil, ErrMigrationOperationInvalid
	}
	handles := make([]migration.OpaqueID, 0, len(operation.DestinationIdentities)+len(operation.DestinationDiskIdentities))
	for _, identity := range operation.DestinationIdentities {
		handles = append(handles, identity.BackendIdentity)
	}
	for _, identity := range operation.DestinationDiskIdentities {
		if identity.Role == migration.DiskRoleAttached {
			handles = append(handles, identity.BackendIdentity)
		}
	}
	slices.Sort(handles)
	if len(handles) == 0 {
		return nil, ErrMigrationOperationInvalid
	}
	for index := 1; index < len(handles); index++ {
		if handles[index-1] == handles[index] {
			return nil, ErrMigrationOperationInvalid
		}
	}
	return handles, nil
}

func (service MigrationImportService) finishImportRollback(
	operation MigrationOperation,
) (MigrationOperation, error) {
	if operation.Cancellation != nil && operation.Progress.CancelPending {
		progress := operation.Progress
		progress.CancelPending = false
		var changed bool
		var err error
		operation, changed, err = service.Store.UpdateProgress(operation.ID, progress)
		_ = changed
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	operation, _, err := service.Store.ReleaseClaims(operation.ID)
	if err != nil {
		return MigrationOperation{}, err
	}
	receiptDigest := migration.Digest("")
	if operation.DestinationStage != nil {
		receiptDigest = operation.DestinationStage.EvidenceDigest
	}
	return service.Store.TransitionPhase(
		operation.ID, MigrationPhaseRolledBack,
		&MigrationOperationResult{
			Code: "migration.import.rolled_back", ReceiptDigest: receiptDigest,
		},
	)
}

func (service MigrationImportService) revalidateImportOperationDestination(
	ctx context.Context,
	operation MigrationOperation,
	manifest migration.Manifest,
	stage backend.DestinationStageRequest,
	beforeFirstMaterialization bool,
) error {
	capability, err := service.importCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision {
		return ErrMigrationPlanStale
	}
	if beforeFirstMaterialization {
		base, err := service.destinationEnvironmentBaseRevision()
		if err != nil || len(operation.BaseRevisions) != 1 || operation.BaseRevisions[0] != base {
			return ErrMigrationPlanStale
		}
	} else {
		for _, claim := range operation.Claims {
			if claim.State != MigrationClaimHeld {
				return ErrMigrationPlanStale
			}
		}
	}
	for _, object := range operation.ImportObjects {
		if _, err := service.Environments.LoadByName(object.DestinationName); err == nil {
			return ErrMigrationPlanStale
		} else if !errors.Is(err, environment.ErrNameNotFound) {
			return err
		}
	}
	if err := service.revalidateMigrationConflictActions(migration.ImportPlan{
		Objects: operation.ImportObjects, ConflictActions: operation.ConflictActions,
	}); err != nil {
		return err
	}
	if err := revalidateMigrationEnvironmentActions(migration.ImportPlan{
		Objects: operation.ImportObjects, EnvironmentActions: operation.EnvironmentActions,
	}, manifest); err != nil {
		return err
	}
	if err := service.revalidateMigrationWorkspaceActions(migration.ImportPlan{
		Objects: operation.ImportObjects, WorkspaceActions: operation.WorkspaceActions,
	}, manifest); err != nil {
		return err
	}
	if err := service.revalidateMigrationOperationSecretActions(
		ctx, operation, manifest,
	); err != nil {
		return err
	}
	requiredBytes, err := migrationDiskLogicalBytes(stage.Disks)
	if err != nil || !operation.Progress.LogicalTotalKnown ||
		requiredBytes != operation.Progress.TotalLogicalBytes {
		return ErrMigrationPlanStale
	}
	if operation.BundleFile == nil {
		return ErrMigrationPlanStale
	}
	capacity, err := migrationImportCapacityRequirement(
		operation.BundleFile.Size, stage.Disks,
	)
	if err != nil {
		return ErrMigrationPlanStale
	}
	// Once the stage effect is durably running, the provider-owned checkpoint is
	// the capacity cursor. Requiring the original peak free-space number again
	// would count already staged bytes twice and could force a large verified
	// prefix to restart. The same capability, claims, source graph, mappings, and
	// capacity formula are still revalidated; the resumed provider write fails
	// recoverably if genuinely remaining space is exhausted.
	if !beforeFirstMaterialization {
		return nil
	}
	request := backend.DestinationInspectionRequest{
		Binding: stage.Binding, ManifestDigest: operation.Bundle.ManifestDigest,
		SourceProduct:   manifest.SourceProduct,
		EnvironmentRefs: migrationImportObjectRefs(operation.ImportObjects),
		Disks:           stage.Disks, Edges: stage.Edges,
		RequiredCapabilities: append(
			[]migration.RequiredCapability(nil), manifest.RequiredCapabilities...,
		),
		RequiredBytes: capacity.PeakAdditionalBytes,
		Capacity:      capacity,
	}
	if err := request.Validate(); err != nil {
		return err
	}
	destination, err := service.Import.InspectMigrationDestination(ctx, request)
	if err != nil {
		return err
	}
	if err := destination.Validate(); err != nil || destination.Binding != stage.Binding ||
		!destination.Compatible || destination.CapabilityRevision != capability.Revision ||
		destination.AvailableBytes < capacity.PeakAdditionalBytes || len(destination.Conflicts) != 0 ||
		len(destination.Blockers) != 0 {
		return ErrMigrationPlanStale
	}
	return nil
}
