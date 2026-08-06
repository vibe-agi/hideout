package manager

import (
	"context"
	"errors"
	"sort"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

type MigrationImportAdoptRequest struct {
	OperationID string
}

// AdoptImportDestination executes the import-time guest identity policy frozen
// in this operation. Each destination provider supplies only its ephemeral SSH
// public material and nonces; Manager rechecks the returned request before it
// accepts the receipt. Replaying a running effect therefore asks the provider
// for the same operation/environment-bound result rather than choosing policy
// again.
func (service MigrationImportService) AdoptImportDestination(
	ctx context.Context,
	request MigrationImportAdoptRequest,
) (MigrationOperation, []backend.DestinationAdoption, error) {
	if ctx == nil || !operationIDPattern.MatchString(request.OperationID) {
		return MigrationOperation{}, nil, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, nil, err
	}
	operation, err := service.Store.Load(request.OperationID)
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		return service.adoptConfigImportDestination(ctx, operation)
	}
	if service.Import == nil {
		return MigrationOperation{}, nil, ErrMigrationCapabilityUnavailable
	}
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil ||
		operation.AdoptionHelper == nil {
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
		if operation.DestinationAdoption == nil {
			return MigrationOperation{}, nil, ErrMigrationOperationInvalid
		}
		if operation.Phase == MigrationPhaseAdopting {
			operation, err = service.Store.TransitionPhase(
				operation.ID, MigrationPhaseVerifying, nil,
			)
		}
		return operation, nil, err
	}

	switch operation.Phase {
	case MigrationPhasePreparingSecrets:
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseAdopting, nil,
		)
	case MigrationPhaseRecoverableFailure:
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
	capability, err := service.importCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision ||
		capability.AdoptionHelper == nil ||
		capability.AdoptionHelper.PackageID != operation.AdoptionHelper.PackageID ||
		capability.AdoptionHelper.Version != operation.AdoptionHelper.Version ||
		capability.AdoptionHelper.Digest != operation.AdoptionHelper.SHA256 {
		return service.importAdoptionFailure(operation.ID, ErrMigrationPlanStale)
	}

	effect, err = migrationOperationEffect(operation, MigrationEffectAdopt)
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	if effect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(
			operation.ID, effect.ID, effect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, nil, err
		}
		effect, err = migrationOperationEffect(operation, MigrationEffectAdopt)
		if err != nil {
			return MigrationOperation{}, nil, err
		}
	}
	if effect.Status != MigrationEffectRunning {
		return MigrationOperation{}, nil, ErrMigrationOperationInvalid
	}

	responses := make([]backend.DestinationAdoption, 0, len(operation.IdentityActions))
	records := make([]MigrationDestinationAdoptionRecord, 0, len(operation.IdentityActions))
	safeMachineIDs := make(map[migration.Digest]struct{})
	safeSSHIDs := make(map[migration.Digest]struct{})
	for index, action := range operation.IdentityActions {
		if err := ctx.Err(); err != nil {
			return service.importAdoptionFailure(operation.ID, err)
		}
		source := operation.SourceGuestIdentities[index]
		mountBindings, bindingErr := migrationAdoptionMountBindings(
			operation, action.SourceRef,
		)
		if bindingErr != nil {
			return service.importAdoptionFailure(operation.ID, bindingErr)
		}
		fixed := backend.DestinationAdoptionRequest{
			Binding: backend.MigrationEffectBinding{
				OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
				CapabilityRevision: operation.CapabilityRevision,
			},
			StageHandle:    operation.DestinationStage.StageHandle,
			EnvironmentRef: action.SourceRef, Policy: action.GuestPolicy,
			SourceIdentity: source.Evidence, MountBindings: mountBindings,
			Helper: *operation.AdoptionHelper,
		}
		if err := fixed.Validate(); err != nil {
			return service.importAdoptionFailure(operation.ID, err)
		}
		adoption, err := service.Import.AdoptMigrationDestination(ctx, fixed)
		if err != nil {
			return service.importAdoptionFailure(operation.ID, err)
		}
		if err := adoption.MatchesRequest(fixed); err != nil {
			return service.importAdoptionFailure(operation.ID, err)
		}
		if action.GuestPolicy == migration.GuestIdentitySafeClone {
			post := adoption.Receipt.PostIdentity
			if post == nil {
				return service.importAdoptionFailure(operation.ID, ErrMigrationOperationInvalid)
			}
			if _, duplicate := safeMachineIDs[post.MachineIDDigest]; duplicate {
				return service.importAdoptionFailure(operation.ID, ErrMigrationOperationMismatch)
			}
			safeMachineIDs[post.MachineIDDigest] = struct{}{}
			for _, digest := range post.SSHHostKeyDigests {
				if _, duplicate := safeSSHIDs[digest]; duplicate {
					return service.importAdoptionFailure(operation.ID, ErrMigrationOperationMismatch)
				}
				safeSSHIDs[digest] = struct{}{}
			}
		}
		responses = append(responses, adoption)
		records = append(records, MigrationDestinationAdoptionRecord{
			EnvironmentRef: action.SourceRef, Request: adoption.Request,
			Receipt: adoption.Receipt, Stopped: adoption.Stopped,
			TemporaryAuthorityRemoved: adoption.TemporaryAuthorityRemoved,
		})
	}
	evidenceDigest, err := CanonicalDigest(
		"migration-destination-adoption",
		struct {
			StageHandle migration.OpaqueID                   `json:"stageHandle"`
			Records     []MigrationDestinationAdoptionRecord `json:"records"`
		}{
			StageHandle: operation.DestinationStage.StageHandle, Records: records,
		},
	)
	if err != nil {
		return service.importAdoptionFailure(operation.ID, err)
	}
	state := MigrationDestinationAdoptionState{
		StageHandle: operation.DestinationStage.StageHandle,
		Records:     records, EvidenceDigest: migration.Digest(evidenceDigest),
	}
	operation, err = service.Store.FinishDestinationAdoption(
		operation.ID, effect.ID, effect.Provider, state,
		[]MigrationEffectEvidence{{
			Code: "migration.import.adoption_verified", OpaqueRef: state.StageHandle,
			Digest: state.EvidenceDigest, Count: uint64(len(state.Records)),
			ObservedAt: service.nowUTC(),
		}},
	)
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	operation, err = service.Store.TransitionPhase(
		operation.ID, MigrationPhaseVerifying, nil,
	)
	if err != nil {
		return MigrationOperation{}, nil, err
	}
	return operation, responses, nil
}

func migrationAdoptionMountBindings(
	operation MigrationOperation,
	environmentRef migration.OpaqueID,
) ([]migration.DiskMountBinding, error) {
	identities := make(
		map[migration.OpaqueID]MigrationDestinationDiskIdentity,
		len(operation.DestinationDiskIdentities),
	)
	for _, identity := range operation.DestinationDiskIdentities {
		identities[identity.DiskID] = identity
	}
	bindings := make([]migration.DiskMountBinding, 0)
	for _, edge := range operation.ExpectedDiskEdges {
		if edge.EnvironmentRef != environmentRef ||
			edge.Attachment != migration.DiskRoleAttached {
			continue
		}
		identity, exists := identities[edge.DiskID]
		if !exists || identity.Role != migration.DiskRoleAttached {
			return nil, ErrMigrationOperationInvalid
		}
		binding := migration.DiskMountBinding{
			DiskID: edge.DiskID, SourceGuestPath: edge.GuestPath,
			DestinationGuestPath: "/mnt/lima-" + string(identity.BackendIdentity),
			FSType:               edge.FSType,
		}
		if binding.Validate() != nil {
			return nil, ErrMigrationOperationInvalid
		}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(left, right int) bool {
		return bindings[left].DiskID < bindings[right].DiskID
	})
	return bindings, nil
}

func (service MigrationImportService) importAdoptionFailure(
	operationID string,
	cause error,
) (MigrationOperation, []backend.DestinationAdoption, error) {
	operation, transitionErr := service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
	if transitionErr != nil {
		return MigrationOperation{}, nil, errors.Join(cause, transitionErr)
	}
	return operation, nil, cause
}
