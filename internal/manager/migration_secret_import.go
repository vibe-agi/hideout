package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/secrets"
)

type MigrationImportPrepareSecretsRequest struct {
	OperationID       string
	SecretInputHandle string
	ClientBinding     string
}

func (service MigrationImportService) PrepareImportSecrets(
	ctx context.Context,
	request MigrationImportPrepareSecretsRequest,
) (MigrationOperation, error) {
	if ctx == nil || !operationIDPattern.MatchString(request.OperationID) ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) || service.SecretInputs == nil ||
		service.InspectionCache == nil || service.Secrets == nil {
		return MigrationOperation{}, ErrMigrationRequestInvalid
	}
	operation, err := service.Store.Load(request.OperationID)
	if err != nil {
		return MigrationOperation{}, err
	}
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil ||
		operation.BundleFile == nil ||
		!migrationHasImportedSecretValues(operation.SecretActions) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	stageEffect, err := migrationOperationEffect(operation, MigrationEffectStage)
	if err != nil || stageEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectPrepareSecret)
	if err != nil {
		return MigrationOperation{}, err
	}
	if effect.Status == MigrationEffectSucceeded {
		if operation.Phase == MigrationPhaseMaterializing {
			operation, err = service.Store.TransitionPhase(
				operation.ID, MigrationPhasePreparingSecrets, nil,
			)
		}
		return operation, err
	}
	if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhasePreparingSecrets, nil,
		)
	} else if operation.Phase == MigrationPhaseMaterializing {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhasePreparingSecrets, nil,
		)
	} else if operation.Phase != MigrationPhasePreparingSecrets {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, err
	}
	binding := migration.BundleBinding{
		BundleID: operation.Bundle.BundleID, FormatVersion: operation.Bundle.FormatVersion,
		FileDigest: operation.Bundle.FileDigest, ManifestDigest: operation.Bundle.ManifestDigest,
		CompletionDigest: operation.Bundle.CompletionDigest,
	}
	inspection, err := service.InspectionCache.Get(binding, *operation.BundleFile)
	if err != nil {
		return service.importSecretPreparationFailure(operation.ID, err)
	}
	if effect.Status == MigrationEffectPending {
		if err := service.revalidateMigrationSecretActions(ctx, migration.ImportPlan{
			Objects: operation.ImportObjects, SecretActions: operation.SecretActions,
		}, inspection.Manifest); err != nil {
			return service.importSecretPreparationFailure(operation.ID, err)
		}
		operation, _, err = service.Store.BeginEffect(
			operation.ID, effect.ID, effect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
		effect, err = migrationOperationEffect(operation, MigrationEffectPrepareSecret)
	}
	if err != nil || effect.Status != MigrationEffectRunning {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationOperation{}, err
	}
	prepared, err := service.readImportSecretValues(
		ctx, operation, inspection.Manifest, request.SecretInputHandle, request.ClientBinding,
	)
	if err != nil {
		return service.importSecretPreparationFailure(operation.ID, err)
	}
	digest, err := CanonicalDigest("migration-import-prepared-secrets", prepared)
	if err != nil {
		return service.importSecretPreparationFailure(operation.ID, err)
	}
	return service.Store.FinishPreparedSecrets(
		operation.ID, effect.ID, prepared, MigrationEffectEvidence{
			Code: "migration.import.secrets_prepared", Digest: migration.Digest(digest),
			Count: uint64(len(prepared)), ObservedAt: service.nowUTC(),
		},
	)
}

func (service MigrationImportService) importSecretPreparationFailure(
	operationID string,
	cause error,
) (MigrationOperation, error) {
	operation, err := service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
	if err != nil {
		return MigrationOperation{}, errors.Join(cause, err)
	}
	return operation, cause
}

func (service MigrationImportService) readImportSecretValues(
	ctx context.Context,
	operation MigrationOperation,
	manifest migration.Manifest,
	secretHandle, clientBinding string,
) ([]MigrationPreparedSecret, error) {
	file, observedFile, public, err := openAndBindMigrationBundleFile(operation.BundlePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if operation.BundleFile == nil || observedFile != *operation.BundleFile ||
		public.BundleID != operation.Bundle.BundleID ||
		public.FormatVersion != operation.Bundle.FormatVersion {
		return nil, migration.ErrBundleChanged
	}
	binding := migration.BundleBinding{
		BundleID: operation.Bundle.BundleID, FormatVersion: operation.Bundle.FormatVersion,
		FileDigest: operation.Bundle.FileDigest, ManifestDigest: operation.Bundle.ManifestDigest,
		CompletionDigest: operation.Bundle.CompletionDigest,
	}
	if err := migration.VerifySealedBundleFile(ctx, file, observedFile.Size, binding); err != nil {
		return nil, err
	}
	var prepared []MigrationPreparedSecret
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
		var completion migration.Digest
		for {
			record, nextErr := reader.Next()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return nextErr
			}
			if record.Header.Type == migration.RecordCompletion {
				completion = record.FrameDigest
			}
			_, consumeErr := preparer.Consume(record)
			clear(record.Plaintext)
			if consumeErr != nil {
				return consumeErr
			}
		}
		summary, summaryErr := reader.Summary()
		if summaryErr != nil || !summary.Sealed || summary.BundleID != binding.BundleID ||
			summary.ManifestDigest != binding.ManifestDigest ||
			completion != binding.CompletionDigest {
			if summaryErr != nil {
				return summaryErr
			}
			return migration.ErrCorruptBundle
		}
		prepared, readerErr = preparer.Prepared()
		return readerErr
	})
	if err != nil {
		return nil, err
	}
	after, afterPublic, err := bindOpenMigrationBundleFile(operation.BundlePath, file)
	if err != nil || after != observedFile || afterPublic != public {
		if err == nil {
			err = migration.ErrBundleChanged
		}
		return nil, err
	}
	if err := migration.VerifySealedBundleFile(ctx, file, after.Size, binding); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (service MigrationImportService) compensateImportSecrets(
	ctx context.Context,
	operation MigrationOperation,
) (MigrationOperation, error) {
	if !migrationHasImportedSecretValues(operation.SecretActions) {
		return operation, nil
	}
	if service.Secrets == nil {
		return MigrationOperation{}, ErrMigrationCapabilityUnavailable
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectPrepareSecret)
	if err != nil {
		return MigrationOperation{}, err
	}
	if effect.Status == MigrationEffectPending || effect.Status == MigrationEffectCompensated {
		return operation, nil
	}
	operation, _, err = service.Store.BeginEffectCompensation(
		operation.ID, effect.ID, effect.Provider,
	)
	if err != nil {
		return MigrationOperation{}, err
	}
	effect, err = migrationOperationEffect(operation, MigrationEffectPrepareSecret)
	if err != nil || effect.Status != MigrationEffectCompensating {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationOperation{}, err
	}
	prepared := append([]MigrationPreparedSecret(nil), operation.PreparedSecrets...)
	if len(prepared) == 0 {
		reconciler, ok := service.Secrets.(secrets.OperationReconciler)
		if !ok {
			return service.importSecretRollbackFailure(
				operation.ID, ErrMigrationCapabilityUnavailable,
			)
		}
		for _, action := range operation.SecretActions {
			if action.Decision != migrationSecretDecisionImportValue {
				continue
			}
			setOperationID := migrationImportSecretOperationID(
				operation.ID, action.DestinationRef,
			)
			result, reconcileErr := reconciler.Reconcile(ctx, secrets.ReconcileRequest{
				Ref: action.DestinationRef, Action: secrets.ActionSet,
				OperationID: setOperationID, ExpectedGeneration: action.BaseGeneration,
			})
			if reconcileErr != nil {
				return service.importSecretRollbackFailure(operation.ID, reconcileErr)
			}
			if result.Uncommitted && !result.Committed {
				continue
			}
			if !result.Committed || result.Uncommitted || result.Reference.Validate() != nil ||
				result.Reference.Ref != action.DestinationRef ||
				result.Reference.Provider != action.DestinationProvider ||
				result.Reference.Availability != secrets.AvailabilityAvailable ||
				result.Reference.Generation != action.BaseGeneration+1 {
				return service.importSecretRollbackFailure(
					operation.ID, ErrMigrationOperationInvalid,
				)
			}
			prepared = append(prepared, MigrationPreparedSecret{
				SourceRef: action.SourceRef, DestinationRef: action.DestinationRef,
				Provider: action.DestinationProvider, BaseGeneration: action.BaseGeneration,
				Generation: result.Reference.Generation, OperationID: setOperationID,
			})
		}
		sort.Slice(prepared, func(left, right int) bool {
			return prepared[left].SourceRef < prepared[right].SourceRef
		})
	}
	for index := len(prepared) - 1; index >= 0; index-- {
		value := prepared[index]
		reference, deleteErr := service.Secrets.Delete(ctx, secrets.DeleteRequest{
			Ref: value.DestinationRef,
			OperationID: migrationImportSecretDeleteOperationID(
				operation.ID, value.DestinationRef,
			),
			ExpectedGeneration: value.Generation,
		})
		if deleteErr != nil {
			return service.importSecretRollbackFailure(operation.ID, deleteErr)
		}
		if reference.Validate() != nil || reference.Ref != value.DestinationRef ||
			reference.Provider != value.Provider ||
			reference.Availability != secrets.AvailabilityMissing ||
			reference.Generation != value.Generation+1 {
			return service.importSecretRollbackFailure(
				operation.ID, ErrMigrationOperationInvalid,
			)
		}
	}
	digest, err := CanonicalDigest("migration-import-secret-rollback", prepared)
	if err != nil {
		return service.importSecretRollbackFailure(operation.ID, err)
	}
	return service.Store.FinishPreparedSecretCompensation(
		operation.ID, effect.ID, prepared, MigrationEffectEvidence{
			Code: "migration.import.secrets_rolled_back", Digest: migration.Digest(digest),
			Count: uint64(len(prepared)), ObservedAt: service.nowUTC(),
		},
	)
}

func (service MigrationImportService) importSecretRollbackFailure(
	operationID string,
	cause error,
) (MigrationOperation, error) {
	operation, err := service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
	if err != nil {
		return MigrationOperation{}, errors.Join(cause, err)
	}
	return operation, cause
}

type migrationImportSecretPreparer struct {
	ctx         context.Context
	operationID string
	store       secrets.RuntimeStore
	actions     map[migration.OpaqueID]migration.SecretAction
	components  map[migration.OpaqueID]migration.ComponentIndexEntry
	payloadSeen map[migration.OpaqueID]struct{}
	checkpoints map[migration.OpaqueID]struct{}
	prepared    map[migration.OpaqueID]MigrationPreparedSecret
}

func newMigrationImportSecretPreparer(
	ctx context.Context,
	operationID string,
	actions []migration.SecretAction,
	manifest migration.Manifest,
	store secrets.RuntimeStore,
) (*migrationImportSecretPreparer, error) {
	selected := make(map[migration.OpaqueID]migration.SecretAction)
	entries := make(map[migration.OpaqueID]migration.SecretEntry, len(manifest.SecretEntries))
	for _, entry := range manifest.SecretEntries {
		entries[entry.SecretRef] = entry
	}
	components := make(map[migration.OpaqueID]migration.ComponentIndexEntry)
	for _, component := range manifest.ComponentIndex {
		components[component.ComponentID] = component
	}
	for _, action := range actions {
		if action.Decision != migrationSecretDecisionImportValue {
			continue
		}
		entry, exists := entries[action.SourceRef]
		component, componentExists := components[action.ValueComponentID]
		if !exists || !componentExists || entry.Transfer != migration.SecretSelectedValue ||
			entry.ValueComponentID != action.ValueComponentID ||
			component.Kind != "secret-value" || component.LogicalBytes == 0 ||
			component.RecordCount != 2 || action.BaseGeneration != 0 {
			return nil, ErrMigrationPlanInvalid
		}
		selected[action.ValueComponentID] = action
	}
	if len(selected) == 0 {
		return &migrationImportSecretPreparer{
			ctx: ctx, actions: selected, components: components,
			payloadSeen: map[migration.OpaqueID]struct{}{},
			checkpoints: map[migration.OpaqueID]struct{}{},
			prepared:    map[migration.OpaqueID]MigrationPreparedSecret{},
		}, nil
	}
	if ctx == nil || !operationIDPattern.MatchString(operationID) || store == nil {
		return nil, ErrMigrationCapabilityUnavailable
	}
	return &migrationImportSecretPreparer{
		ctx: ctx, operationID: operationID, store: store,
		actions: selected, components: components,
		payloadSeen: make(map[migration.OpaqueID]struct{}, len(selected)),
		checkpoints: make(map[migration.OpaqueID]struct{}, len(selected)),
		prepared:    make(map[migration.OpaqueID]MigrationPreparedSecret, len(selected)),
	}, nil
}

func (preparer *migrationImportSecretPreparer) Consume(
	record migration.Record,
) (bool, error) {
	if preparer == nil {
		return false, ErrMigrationOperationInvalid
	}
	action, selected := preparer.actions[record.Header.ComponentID]
	if !selected {
		return false, nil
	}
	component := preparer.components[record.Header.ComponentID]
	if record.Sequence < component.FirstRecord || record.Sequence > component.LastRecord {
		return true, migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("secret component record escaped its authenticated range"),
		)
	}
	if record.Header.Type == migration.RecordCheckpoint {
		_, payloadSeen := preparer.payloadSeen[record.Header.ComponentID]
		_, duplicate := preparer.checkpoints[record.Header.ComponentID]
		if !payloadSeen || duplicate || record.Sequence != component.LastRecord {
			return true, migrationComponentStreamError(
				record.Header.ComponentID, record.Sequence,
				errors.New("secret component checkpoint is invalid"),
			)
		}
		preparer.checkpoints[record.Header.ComponentID] = struct{}{}
		return true, nil
	}
	if record.Header.Type != migration.RecordSecretValue ||
		record.Sequence != component.FirstRecord || record.Header.Ordinal != 0 ||
		record.Header.LogicalOffset != 0 ||
		record.Header.PlaintextLength != component.LogicalBytes {
		return true, migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("secret value component shape is invalid"),
		)
	}
	if _, duplicate := preparer.payloadSeen[record.Header.ComponentID]; duplicate {
		return true, migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("secret value component is duplicated"),
		)
	}
	digest := sha256.Sum256(record.Plaintext)
	if migration.Digest("sha256:"+hex.EncodeToString(digest[:])) != component.ContentDigest {
		return true, migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("secret value component digest changed"),
		)
	}
	buffer, err := secrets.NewBuffer(record.Plaintext)
	if err != nil {
		return true, migrationComponentStreamError(record.Header.ComponentID, record.Sequence, err)
	}
	operationID := migrationImportSecretOperationID(
		preparer.operationID, action.DestinationRef,
	)
	reference, err := preparer.store.Set(preparer.ctx, secrets.WriteRequest{
		Ref: action.DestinationRef, OperationID: operationID,
		ExpectedGeneration: action.BaseGeneration, Value: buffer,
	})
	if err != nil {
		return true, err
	}
	if reference.Validate() != nil || reference.Ref != action.DestinationRef ||
		reference.Provider != action.DestinationProvider ||
		reference.Availability != secrets.AvailabilityAvailable ||
		reference.Generation != action.BaseGeneration+1 {
		return true, ErrMigrationPlanStale
	}
	preparer.payloadSeen[record.Header.ComponentID] = struct{}{}
	preparer.prepared[action.SourceRef] = MigrationPreparedSecret{
		SourceRef: action.SourceRef, DestinationRef: action.DestinationRef,
		Provider: action.DestinationProvider, BaseGeneration: action.BaseGeneration,
		Generation: reference.Generation, OperationID: operationID,
	}
	return true, nil
}

func (preparer *migrationImportSecretPreparer) Prepared() (
	[]MigrationPreparedSecret,
	error,
) {
	if preparer == nil || len(preparer.prepared) != len(preparer.actions) ||
		len(preparer.checkpoints) != len(preparer.actions) {
		return nil, ErrMigrationOperationInvalid
	}
	prepared := make([]MigrationPreparedSecret, 0, len(preparer.prepared))
	for _, value := range preparer.prepared {
		prepared = append(prepared, value)
	}
	sort.Slice(prepared, func(left, right int) bool {
		return prepared[left].SourceRef < prepared[right].SourceRef
	})
	return prepared, nil
}
