package manager

import (
	"context"
	"errors"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

type MigrationImportVerifyRequest struct {
	OperationID string
}

// VerifyImportDestination proves the private destination stage without
// reopening the encrypted bundle. All expected disk digests, import-time
// identity policies, and nonce-bound adoption receipts come from the durable
// operation, so a long stage or daemon restart does not depend on an inspection
// cache or require the passphrase again.
func (service MigrationImportService) VerifyImportDestination(
	ctx context.Context,
	request MigrationImportVerifyRequest,
) (MigrationOperation, backend.DestinationProof, error) {
	if ctx == nil || !operationIDPattern.MatchString(request.OperationID) {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	operation, err := service.Store.Load(request.OperationID)
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		return service.verifyConfigImportDestination(ctx, operation)
	}
	if service.Import == nil {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationCapabilityUnavailable
	}
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil ||
		operation.DestinationAdoption == nil || operation.AdoptionHelper == nil {
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

	switch operation.Phase {
	case MigrationPhaseVerifying:
	case MigrationPhaseRecoverableFailure:
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseVerifying, nil,
		)
	default:
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	capability, err := service.importCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision ||
		capability.AdoptionHelper == nil ||
		capability.AdoptionHelper.PackageID != operation.AdoptionHelper.PackageID ||
		capability.AdoptionHelper.Version != operation.AdoptionHelper.Version ||
		capability.AdoptionHelper.Digest != operation.AdoptionHelper.SHA256 {
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
		if err != nil {
			return MigrationOperation{}, backend.DestinationProof{}, err
		}
	}
	if verifyEffect.Status != MigrationEffectRunning {
		return MigrationOperation{}, backend.DestinationProof{}, ErrMigrationOperationInvalid
	}

	providerRequest := migrationDestinationVerifyRequest(operation, verifyEffect)
	if err := providerRequest.Validate(); err != nil {
		return service.importVerificationFailure(operation.ID, err)
	}
	proof, err := service.Import.VerifyMigrationDestination(ctx, providerRequest)
	if err != nil {
		return service.importVerificationFailure(operation.ID, err)
	}
	if err := proof.MatchesRequest(providerRequest); err != nil {
		return service.importVerificationFailure(operation.ID, err)
	}
	evidence := []MigrationEffectEvidence{{
		Code:       migrationDestinationVerificationEvidenceCode,
		OpaqueRef:  operation.DestinationStage.StageHandle,
		Digest:     proof.ProofDigest,
		Count:      uint64(len(operation.ExpectedDisks)),
		ObservedAt: service.nowUTC(),
	}}
	operation, err = service.Store.FinishEffect(
		operation.ID, verifyEffect.ID, verifyEffect.Provider,
		MigrationEffectSucceeded, evidence,
	)
	if err != nil {
		return MigrationOperation{}, backend.DestinationProof{}, err
	}
	return operation, proof, nil
}

func migrationDestinationVerifyRequest(
	operation MigrationOperation,
	effect MigrationEffect,
) backend.DestinationVerifyRequest {
	policies := make([]migration.IdentitySelection, len(operation.IdentityActions))
	for index, action := range operation.IdentityActions {
		policies[index] = migration.IdentitySelection{
			SourceRef: action.SourceRef,
			Policy:    action.GuestPolicy,
		}
	}
	receipts := make(
		[]migration.AdoptionReceipt, len(operation.DestinationAdoption.Records),
	)
	requests := make(
		[]migration.AdoptionRequest, len(operation.DestinationAdoption.Records),
	)
	for index, record := range operation.DestinationAdoption.Records {
		requests[index] = cloneMigrationAdoptionRequest(record.Request)
		receipts[index] = cloneMigrationAdoptionReceipt(record.Receipt)
	}
	return backend.DestinationVerifyRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
			CapabilityRevision: operation.CapabilityRevision,
		},
		StageHandle:      operation.DestinationStage.StageHandle,
		ExpectedDisks:    cloneMigrationDiskObjects(operation.ExpectedDisks),
		IdentityPolicies: policies,
		AdoptionRequests: requests,
		AdoptionReceipts: receipts,
	}
}

func cloneMigrationAdoptionRequest(
	request migration.AdoptionRequest,
) migration.AdoptionRequest {
	cloned := request
	cloned.DestinationSSHKeys = append(
		[]string(nil), request.DestinationSSHKeys...,
	)
	cloned.PermittedActions = append(
		[]string(nil), request.PermittedActions...,
	)
	cloned.MountBindings = append(
		[]migration.DiskMountBinding(nil), request.MountBindings...,
	)
	cloned.SourceIdentity.SSHHostKeyDigests = append(
		[]migration.Digest(nil), request.SourceIdentity.SSHHostKeyDigests...,
	)
	return cloned
}

func migrationDestinationVerificationProof(
	operation MigrationOperation,
) (backend.DestinationProof, error) {
	if err := operation.Validate(); err != nil || operation.DestinationStage == nil {
		return backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectVerify)
	if err != nil || effect.Status != MigrationEffectSucceeded || len(effect.Evidence) != 1 {
		return backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	proof := backend.DestinationProof{
		Binding: backend.MigrationEffectBinding{
			OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
			CapabilityRevision: operation.CapabilityRevision,
		},
		StageHandle: operation.DestinationStage.StageHandle,
		ProofDigest: effect.Evidence[0].Digest,
		Stopped:     true, DigestsMatch: true, IdentityPolicySatisfied: true,
		TemporaryAuthorityRemoved: true, ImportedAuthorityAbsent: true,
	}
	if err := proof.Validate(); err != nil {
		return backend.DestinationProof{}, ErrMigrationOperationInvalid
	}
	return proof, nil
}

func cloneMigrationAdoptionReceipt(
	receipt migration.AdoptionReceipt,
) migration.AdoptionReceipt {
	cloned := receipt
	cloned.ActionResults = append(
		[]migration.AdoptionActionResult(nil), receipt.ActionResults...,
	)
	cloned.MountBindings = append(
		[]migration.DiskMountBinding(nil), receipt.MountBindings...,
	)
	if receipt.PostIdentity != nil {
		post := *receipt.PostIdentity
		post.SSHHostKeyDigests = append(
			[]migration.Digest(nil), receipt.PostIdentity.SSHHostKeyDigests...,
		)
		cloned.PostIdentity = &post
	}
	return cloned
}

func (service MigrationImportService) importVerificationFailure(
	operationID string,
	cause error,
) (MigrationOperation, backend.DestinationProof, error) {
	operation, transitionErr := service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
	if transitionErr != nil {
		return MigrationOperation{}, backend.DestinationProof{}, errors.Join(cause, transitionErr)
	}
	return operation, backend.DestinationProof{}, cause
}
