//go:build darwin || linux

package manager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationExportCancellationRemovesOnlyProvedOperationPartial(t *testing.T) {
	root := t.TempDir()
	store := MigrationStore{Root: filepath.Join(root, "store")}
	operation := migrationExportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	output := filepath.Join(root, "dev.hideout-migration")
	claim, err := NewMigrationClaim(MigrationClaimOutputPath, output)
	if err != nil {
		t.Fatal(err)
	}
	operation.Claims = []MigrationClaim{claim}
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	partial := migrationExportPartialPath(output, operation.ID)
	writeMigrationPartialFixture(t, partial, operation.Bundle.BundleID)
	if _, err := store.RequestCancellation(
		operation.ID, operation.Revision, boolPointer(false),
	); err != nil {
		t.Fatal(err)
	}
	service := MigrationService{Store: store, Export: newManagerMigrationProviderFixture()}
	cancelled, err := service.FinalizeExportCancellation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Phase != MigrationPhaseCancelled || cancelled.Result == nil ||
		cancelled.Result.Code != "migration.export.cancelled" ||
		cancelled.Progress.CancelPending || cancelled.Progress.RetainedBytes != 0 {
		t.Fatalf("cancelled operation=%+v", cancelled)
	}
	if _, err := os.Lstat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial survived explicit removal: %v", err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancellation published or removed an unexpected output: %v", err)
	}
}

func TestMigrationExportCancellationRetainsChoiceAndRejectsMismatchedArtifact(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "partial")
	writeMigrationPartialFixture(t, partial, "migb_cancelpartial1")
	retained, err := cancelMigrationExportPartial(partial, "migb_cancelpartial1", true)
	if err != nil || retained == 0 {
		t.Fatalf("retained=%d err=%v", retained, err)
	}
	if _, err := os.Lstat(partial); err != nil {
		t.Fatal(err)
	}
	if _, err := cancelMigrationExportPartial(
		partial, "migb_anotherbundle1", false,
	); !errors.Is(err, ErrMigrationOperationMismatch) {
		t.Fatalf("mismatched partial error=%v", err)
	}
	if _, err := os.Lstat(partial); err != nil {
		t.Fatalf("mismatched partial was removed: %v", err)
	}
	if _, err := cancelMigrationExportPartial(partial, "migb_cancelpartial1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proved partial survived removal: %v", err)
	}
}

func TestMigrationRetainedExportPartialRequiresAdvertisedRevisionBoundRemoval(t *testing.T) {
	root := t.TempDir()
	store := MigrationStore{Root: filepath.Join(root, "store")}
	operation := migrationExportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	output := filepath.Join(root, "retained.hideout-migration")
	claim, err := NewMigrationClaim(MigrationClaimOutputPath, output)
	if err != nil {
		t.Fatal(err)
	}
	operation.Claims = []MigrationClaim{claim}
	reserved, _, err := store.Reserve(operation)
	if err != nil {
		t.Fatal(err)
	}
	partial := migrationExportPartialPath(output, operation.ID)
	writeMigrationPartialFixture(t, partial, operation.Bundle.BundleID)
	if _, err := store.RequestCancellation(
		operation.ID, reserved.Revision, boolPointer(true),
	); err != nil {
		t.Fatal(err)
	}
	service := MigrationService{Store: store, Export: newManagerMigrationProviderFixture()}
	cancelled, err := service.FinalizeExportCancellation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Phase != MigrationPhaseCancelled || cancelled.Progress.RetainedBytes == 0 ||
		cancelled.Recovery.Action != MigrationRecoveryRemovePartial {
		t.Fatalf("retained cancellation did not advertise cleanup: %+v", cancelled)
	}
	if _, err := service.RemoveRetainedExportPartial(
		context.Background(), operation.ID, cancelled.Revision+1,
	); !errors.Is(err, ErrMigrationStoreRevision) {
		t.Fatalf("stale retained cleanup error=%v", err)
	}
	if _, err := os.Lstat(partial); err != nil {
		t.Fatalf("stale cleanup removed the partial: %v", err)
	}
	cleaned, err := service.RemoveRetainedExportPartial(
		context.Background(), operation.ID, cancelled.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Progress.RetainedBytes != 0 || cleaned.Recovery.Action != MigrationRecoveryNone ||
		cleaned.Result == nil || cleaned.Result.Code != "migration.export.cancelled" {
		t.Fatalf("retained cleanup did not close the obligation: %+v", cleaned)
	}
	if _, err := os.Lstat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proved retained partial survived cleanup: %v", err)
	}
}

func writeMigrationPartialFixture(
	t *testing.T,
	path string,
	bundleID migration.BundleID,
) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := migration.NewWriter(file, migration.WriterOptions{
		BundleID: bundleID, CreatedAt: "2026-08-03T00:00:00Z",
		KDF:        migration.KDFParameters{MemoryKiB: 8 << 10, Passes: 1, Lanes: 1},
		Limits:     migration.DefaultLimits(),
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x6d}, 4096)),
		Passphrase: []byte("cancellation fixture passphrase"),
	})
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
