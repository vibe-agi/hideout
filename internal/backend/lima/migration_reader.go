package lima

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

var errMigrationSparseSeekUnsupported = errors.New(
	"sparse disk extent discovery is unsupported on this platform",
)

func (b Backend) ReadMigrationComponent(
	ctx context.Context,
	request backend.ComponentReadRequest,
	emit func(backend.MigrationExtent) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if emit == nil {
		return backend.ErrMigrationProviderRequest
	}
	home, snapshotDir, owner, entry, err := b.loadMigrationSnapshotComponent(request)
	if err != nil {
		return migrationSnapshotError(
			"migration.provider.component_unavailable",
			request.Binding, request.ComponentID, err, true,
		)
	}
	path := migrationSnapshotEntryPath(home, snapshotDir, entry)
	file, err := os.Open(path)
	if err != nil {
		return migrationSnapshotError(
			"migration.provider.component_unavailable",
			request.Binding, request.ComponentID, err, true,
		)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != int64(entry.LogicalBytes) ||
		entry.Format != "raw" {
		return migrationSnapshotError(
			"migration.provider.component_changed",
			request.Binding, request.ComponentID,
			errors.New("snapshot component shape changed"), true,
		)
	}
	if request.ResumeOffset > entry.LogicalBytes {
		return backend.ErrMigrationProviderRequest
	}
	if err := readMigrationSparseExtents(
		ctx, file, request.ResumeOffset, entry.LogicalBytes,
		request.MaxChunkBytes, emit,
	); err != nil {
		return migrationSnapshotError(
			"migration.provider.component_read_failed",
			request.Binding, request.ComponentID, err, true,
		)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() ||
		!after.ModTime().Equal(before.ModTime()) {
		return migrationSnapshotError(
			"migration.provider.component_changed",
			request.Binding, request.ComponentID,
			errors.New("snapshot component changed while being read"), true,
		)
	}
	_ = owner
	return nil
}

func (b Backend) loadMigrationSnapshotComponent(
	request backend.ComponentReadRequest,
) (string, string, migrationSnapshotOwner, migrationSnapshotEntry, error) {
	home, err := b.migrationLimaHome()
	if err != nil {
		return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{}, err
	}
	snapshotDir := filepath.Join(
		home, "_hideout-migration", "snapshots", string(request.SnapshotHandle),
	)
	if _, err := protectedMigrationDirectory(home, snapshotDir, snapshotDir); err != nil {
		return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{}, err
	}
	var owner migrationSnapshotOwner
	if err := readMigrationJSONStrict(filepath.Join(snapshotDir, "owner.json"), &owner); err != nil {
		return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{}, err
	}
	if err := owner.validate(home, snapshotDir); err != nil ||
		owner.SnapshotHandle != request.SnapshotHandle || owner.Binding != request.Binding {
		return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{},
			errors.New("snapshot owner does not match component read binding")
	}
	ownerDigest, err := migrationJSONDigest(owner)
	if err != nil {
		return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{}, err
	}
	complete, err := loadMigrationSnapshotComplete(snapshotDir)
	if err != nil || complete.SnapshotHandle != owner.SnapshotHandle ||
		complete.OwnerDigest != ownerDigest {
		return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{},
			errors.New("snapshot completion proof is absent or changed")
	}
	for _, entry := range owner.Entries {
		if entry.ComponentID == request.ComponentID {
			return home, snapshotDir, owner, entry, nil
		}
	}
	return "", "", migrationSnapshotOwner{}, migrationSnapshotEntry{},
		errors.New("snapshot component is absent")
}

func readMigrationSparseExtents(
	ctx context.Context,
	file *os.File,
	start,
	logicalBytes uint64,
	maxChunkBytes uint32,
	emit func(backend.MigrationExtent) error,
) error {
	if start == logicalBytes {
		return nil
	}
	if start > logicalBytes || logicalBytes > uint64(^uint64(0)>>1) {
		return errors.New("sparse extent bounds are invalid")
	}
	coalescer := migrationExtentCoalescer{
		ctx: ctx, maxChunkBytes: maxChunkBytes, emit: emit,
	}
	offset := start
	for offset < logicalBytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		dataOffset, err := migrationSeekData(int(file.Fd()), int64(offset))
		if migrationSeekNoMoreData(err) {
			if err := emitMigrationExtentRange(
				ctx, migration.ExtentHole, offset, logicalBytes,
				maxChunkBytes, nil, coalescer.accept,
			); err != nil {
				return err
			}
			return coalescer.flush()
		}
		if err != nil || dataOffset < int64(offset) || uint64(dataOffset) > logicalBytes {
			if err == nil {
				err = errors.New("filesystem returned an invalid data extent")
			}
			return errors.Join(errMigrationSparseSeekUnsupported, err)
		}
		if uint64(dataOffset) > offset {
			if err := emitMigrationExtentRange(
				ctx, migration.ExtentHole, offset, uint64(dataOffset),
				maxChunkBytes, nil, coalescer.accept,
			); err != nil {
				return err
			}
			offset = uint64(dataOffset)
			if offset == logicalBytes {
				break
			}
		}
		holeOffset, err := migrationSeekHole(int(file.Fd()), int64(offset))
		if migrationSeekNoMoreData(err) {
			holeOffset = int64(logicalBytes)
			err = nil
		}
		if err != nil || holeOffset <= int64(offset) || uint64(holeOffset) > logicalBytes {
			if err == nil {
				err = errors.New("filesystem returned an invalid hole extent")
			}
			return errors.Join(errMigrationSparseSeekUnsupported, err)
		}
		if err := emitMigrationExtentRange(
			ctx, migration.ExtentData, offset, uint64(holeOffset),
			maxChunkBytes, file, coalescer.accept,
		); err != nil {
			return err
		}
		offset = uint64(holeOffset)
	}
	return coalescer.flush()
}

// migrationExtentCoalescer keeps the provider stream canonical without putting
// an artificial chunk-size ceiling on payload-free sparse ranges. Data buffers
// remain bounded; adjacent Hole or Zero observations are merged before they
// cross the provider boundary.
type migrationExtentCoalescer struct {
	ctx           context.Context
	maxChunkBytes uint32
	emit          func(backend.MigrationExtent) error
	pending       *backend.MigrationExtent
}

func (coalescer *migrationExtentCoalescer) accept(extent backend.MigrationExtent) error {
	if err := coalescer.ctx.Err(); err != nil {
		return err
	}
	if extent.Kind == migration.ExtentHole || extent.Kind == migration.ExtentZero {
		if coalescer.pending != nil && coalescer.pending.Kind == extent.Kind &&
			coalescer.pending.LogicalOffset+coalescer.pending.Length == extent.LogicalOffset {
			coalescer.pending.Length += extent.Length
			return nil
		}
		if err := coalescer.flush(); err != nil {
			return err
		}
		copy := extent
		coalescer.pending = &copy
		return nil
	}
	if err := coalescer.flush(); err != nil {
		return err
	}
	return coalescer.emit(extent)
}

func (coalescer *migrationExtentCoalescer) flush() error {
	if coalescer.pending == nil {
		return nil
	}
	if err := coalescer.ctx.Err(); err != nil {
		return err
	}
	extent := *coalescer.pending
	coalescer.pending = nil
	if err := extent.Validate(coalescer.maxChunkBytes); err != nil {
		return err
	}
	return coalescer.emit(extent)
}

func emitMigrationExtentRange(
	ctx context.Context,
	kind migration.ExtentKind,
	start,
	end uint64,
	maxChunkBytes uint32,
	file *os.File,
	emit func(backend.MigrationExtent) error,
) error {
	for offset := start; offset < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := end - offset
		if length > uint64(maxChunkBytes) {
			length = uint64(maxChunkBytes)
		}
		extent := backend.MigrationExtent{
			Kind: kind, LogicalOffset: offset, Length: length,
		}
		if kind == migration.ExtentData {
			extent.Data = make([]byte, int(length))
			if _, err := file.ReadAt(extent.Data, int64(offset)); err != nil {
				return err
			}
			if migrationBytesAreZero(extent.Data) {
				extent.Kind = migration.ExtentZero
				extent.Data = nil
			}
		}
		if err := extent.Validate(maxChunkBytes); err != nil {
			return err
		}
		if err := emit(extent); err != nil {
			return err
		}
		offset += length
	}
	return nil
}

func migrationBytesAreZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
