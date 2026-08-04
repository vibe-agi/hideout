package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

// FinalizeExportCancellation applies the already-durable operator choice. It
// removes only the operation-derived partial path after proving a regular,
// private file with the operation's bundle identity. A published output is
// never removed by cancellation.
func (service MigrationService) FinalizeExportCancellation(
	ctx context.Context,
	operationID string,
) (MigrationOperation, error) {
	if ctx == nil || service.Export == nil || !operationIDPattern.MatchString(operationID) {
		return MigrationOperation{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, err
	}
	operation, err := service.Store.Load(operationID)
	if err != nil {
		return MigrationOperation{}, err
	}
	if operation.Kind != MigrationOperationExport || operation.Cancellation == nil {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	if operation.Phase == MigrationPhaseCancelled {
		return operation, nil
	}
	if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseCancelling, nil,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	if operation.Phase != MigrationPhaseCancelling {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}

	outputPath, err := migrationExportOutputPath(operation)
	if err != nil {
		return MigrationOperation{}, err
	}
	if exists, err := migrationExportRegularPathExists(outputPath); err != nil {
		return MigrationOperation{}, err
	} else if exists {
		// Crossing publication is the export commit boundary. Never reinterpret a
		// published, user-owned file as cancellation scratch.
		return MigrationOperation{}, ErrMigrationOutputConflict
	}
	partialPath := migrationExportPartialPath(outputPath, operation.ID)
	retainedBytes, err := cancelMigrationExportPartial(
		partialPath, operation.Bundle.BundleID, operation.Cancellation.RetainPartial,
	)
	if err != nil {
		return MigrationOperation{}, err
	}
	if err := releaseCancelledMigrationSnapshot(ctx, service, operation); err != nil {
		return MigrationOperation{}, err
	}

	operation, _, err = service.Store.ReleaseClaims(operation.ID)
	if err != nil {
		return MigrationOperation{}, err
	}
	progress := operation.Progress
	progress.CancelPending = false
	progress.RetainedBytes = retainedBytes
	operation, _, err = service.Store.UpdateProgress(operation.ID, progress)
	if err != nil {
		return MigrationOperation{}, err
	}
	return service.Store.TransitionPhase(
		operation.ID, MigrationPhaseCancelled,
		&MigrationOperationResult{Code: "migration.export.cancelled"},
	)
}

// RemoveRetainedExportPartial executes the explicit post-cancellation cleanup
// advertised by the terminal operation. A crash after unlink but before the
// ledger update is safe: replay proves absence and then retires the same
// revision-bound obligation. It never targets the published bundle path.
func (service MigrationService) RemoveRetainedExportPartial(
	ctx context.Context,
	operationID string,
	expectedRevision uint64,
) (MigrationOperation, error) {
	if ctx == nil || !operationIDPattern.MatchString(operationID) || expectedRevision == 0 {
		return MigrationOperation{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, err
	}
	operation, err := service.Store.Load(operationID)
	if err != nil {
		return MigrationOperation{}, err
	}
	if operation.Revision != expectedRevision {
		return MigrationOperation{}, ErrMigrationStoreRevision
	}
	if operation.Kind != MigrationOperationExport ||
		operation.Phase != MigrationPhaseCancelled || operation.Cancellation == nil ||
		!operation.Cancellation.RetainPartial || operation.Progress.RetainedBytes == 0 ||
		operation.Recovery.Action != MigrationRecoveryRemovePartial {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	outputPath, err := migrationExportOutputPath(operation)
	if err != nil {
		return MigrationOperation{}, err
	}
	partialPath := migrationExportPartialPath(outputPath, operation.ID)
	if _, err := cancelMigrationExportPartial(
		partialPath, operation.Bundle.BundleID, false,
	); err != nil {
		return MigrationOperation{}, err
	}
	return service.Store.CompleteRetainedExportPartialRemoval(
		operation.ID, expectedRevision,
	)
}

func cancelMigrationExportPartial(
	partialPath string,
	bundleID migration.BundleID,
	retain bool,
) (uint64, error) {
	file, err := openMigrationBundleNoFollow(partialPath)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 {
		if err == nil {
			err = ErrMigrationOutputConflict
		}
		return 0, err
	}
	public, err := migration.InspectBundleHeader(file, info.Size())
	if err != nil || public.BundleID != bundleID {
		if err == nil {
			err = ErrMigrationOperationMismatch
		}
		return 0, err
	}
	if retain {
		return uint64(info.Size()), nil
	}
	pathInfo, err := os.Lstat(partialPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathInfo) {
		if err == nil {
			err = ErrMigrationOutputConflict
		}
		return 0, err
	}
	if err := os.Remove(partialPath); err != nil {
		return 0, err
	}
	if err := syncMigrationExportDirectory(filepath.Dir(partialPath)); err != nil {
		return 0, err
	}
	return 0, nil
}

func releaseCancelledMigrationSnapshot(
	ctx context.Context,
	service MigrationService,
	operation MigrationOperation,
) error {
	effect, err := migrationOperationEffect(operation, MigrationEffectSnapshot)
	if err != nil {
		return err
	}
	switch effect.Status {
	case MigrationEffectPending, MigrationEffectCompensated:
		return nil
	case MigrationEffectSucceeded:
		if len(effect.Evidence) != 1 || effect.Evidence[0].OpaqueRef == "" {
			return ErrMigrationOperationInvalid
		}
		return service.Export.ReleaseMigrationSnapshot(ctx, backend.SnapshotReleaseRequest{
			Binding: backend.MigrationEffectBinding{
				OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
				CapabilityRevision: operation.CapabilityRevision,
			},
			SnapshotHandle: effect.Evidence[0].OpaqueRef,
		})
	default:
		// The provider may own an effect whose handle was never durably proved.
		// Claiming cancellation would leak or guess that object, so recovery stays
		// explicit instead.
		return ErrMigrationOperationInvalid
	}
}
