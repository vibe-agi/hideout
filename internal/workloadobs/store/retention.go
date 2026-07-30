package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type sealedDescriptor struct {
	Owner     workloadtypes.ActivityOwner
	OwnerRoot string
	Manifest  SegmentManifest
}

func (store *Store) Stats() (StoreStats, error) {
	if store == nil {
		return StoreStats{}, ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return StoreStats{}, err
	}
	if err := store.enforceRetentionLocked(); err != nil {
		return StoreStats{}, err
	}
	return store.statsLocked()
}

func (store *Store) OwnerStats(
	owner workloadtypes.ActivityOwner,
) (OwnerStats, error) {
	if store == nil {
		return OwnerStats{}, ErrClosed
	}
	if err := owner.Validate(); err != nil {
		return OwnerStats{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return OwnerStats{}, err
	}
	if err := store.enforceRetentionLocked(); err != nil {
		return OwnerStats{}, err
	}
	ownerRoot, err := store.loadOwnerLocked(owner)
	if err != nil {
		return OwnerStats{}, err
	}
	return store.ownerStatsLocked(owner, ownerRoot)
}

func (store *Store) statsLocked() (StoreStats, error) {
	owners, err := store.listOwnersLocked()
	if err != nil {
		return StoreStats{}, err
	}
	result := StoreStats{
		LimitBytes:             uint64(store.options.GlobalBytes),
		DefaultOwnerLimitBytes: uint64(store.options.PerOwnerBytes),
		ActiveSegmentBytes:     uint64(store.options.ActiveSegmentBytes),
		Owners:                 len(owners),
	}
	for _, owner := range owners {
		ownerRoot := ownerPath(store.root, owner)
		stats, err := store.ownerStatsLocked(owner, ownerRoot)
		if err != nil {
			return StoreStats{}, err
		}
		result.UsedBytes += stats.UsedBytes
		result.Segments += stats.Segments
	}
	return result, nil
}

func (store *Store) ownerStatsLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) (OwnerStats, error) {
	metadata, err := store.readOwnerMetadataLocked(ownerRoot, owner)
	if err != nil {
		return OwnerStats{}, err
	}
	retention := store.defaultOwnerRetentionPolicy()
	if metadata.Retention != nil {
		retention = *metadata.Retention
	}
	result := OwnerStats{
		Owner: owner, LimitBytes: uint64(retention.MaxBytes),
		MaxAgeSeconds: retention.MaxAgeSeconds,
	}
	activePath := filepath.Join(ownerRoot, activeSegmentFile)
	info, err := os.Lstat(activePath)
	if err != nil {
		return OwnerStats{}, err
	}
	if err := validatePrivateRegular(activePath, info); err != nil {
		return OwnerStats{}, err
	}
	result.UsedBytes += uint64(info.Size())
	if info.Size() > 0 {
		result.Segments++
	}
	entries, err := os.ReadDir(filepath.Join(ownerRoot, sealedDirectory))
	if err != nil {
		return OwnerStats{}, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), segmentSuffix) {
			continue
		}
		path := filepath.Join(ownerRoot, sealedDirectory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return OwnerStats{}, err
		}
		if err := validatePrivateRegular(path, info); err != nil {
			return OwnerStats{}, err
		}
		result.UsedBytes += uint64(info.Size())
		result.Segments++
	}
	return result, nil
}

func (store *Store) BindOwnerRetention(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
	policy workloadtypes.ActivityRetentionPolicy,
) error {
	if store == nil {
		return ErrClosed
	}
	if ctx == nil {
		return ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return err
	}
	if _, err := store.ensureOwnerWithRetentionLocked(
		owner,
		&policy,
	); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.enforceRetentionLocked()
}

func (store *Store) enforceRetentionLocked() error {
	owners, err := store.listOwnersLocked()
	if err != nil {
		return err
	}
	now := store.nowLocked()
	for _, owner := range owners {
		ownerRoot := ownerPath(store.root, owner)
		metadata, err := store.readOwnerMetadataLocked(ownerRoot, owner)
		if err != nil {
			return err
		}
		retention := store.defaultOwnerRetentionPolicy()
		if metadata.Retention != nil {
			retention = *metadata.Retention
		}
		if retention.MaxAgeSeconds > 0 {
			cutoff := now.Add(
				-time.Duration(retention.MaxAgeSeconds) * time.Second,
			)
			descriptors, err := store.ownerSealedDescriptorsLocked(
				owner,
				ownerRoot,
			)
			if err != nil {
				return err
			}
			for _, descriptor := range descriptors {
				if descriptor.Manifest.LastAt.After(cutoff) {
					continue
				}
				if err := store.pruneSegmentLocked(descriptor); err != nil {
					return err
				}
			}
		}
		for {
			stats, err := store.ownerStatsLocked(owner, ownerRoot)
			if err != nil {
				return err
			}
			if stats.UsedBytes <= stats.LimitBytes {
				break
			}
			descriptors, err := store.ownerSealedDescriptorsLocked(
				owner, ownerRoot,
			)
			if err != nil {
				return err
			}
			if len(descriptors) == 0 {
				break
			}
			if err := store.pruneSegmentLocked(descriptors[0]); err != nil {
				return err
			}
		}
	}
	for {
		stats, err := store.statsLocked()
		if err != nil {
			return err
		}
		if stats.UsedBytes <= uint64(store.options.GlobalBytes) {
			return nil
		}
		descriptors, err := store.allSealedDescriptorsLocked()
		if err != nil {
			return err
		}
		if len(descriptors) == 0 {
			return nil
		}
		if err := store.pruneSegmentLocked(descriptors[0]); err != nil {
			return err
		}
	}
}

func (store *Store) ownerSealedDescriptorsLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) ([]sealedDescriptor, error) {
	manifests, err := store.listManifestsLocked(owner, ownerRoot)
	if err != nil {
		return nil, err
	}
	result := make([]sealedDescriptor, len(manifests))
	for index, manifest := range manifests {
		result[index] = sealedDescriptor{
			Owner: owner, OwnerRoot: ownerRoot, Manifest: manifest,
		}
	}
	sortDescriptors(result)
	return result, nil
}

func (store *Store) allSealedDescriptorsLocked() ([]sealedDescriptor, error) {
	owners, err := store.listOwnersLocked()
	if err != nil {
		return nil, err
	}
	var result []sealedDescriptor
	for _, owner := range owners {
		ownerRoot := ownerPath(store.root, owner)
		descriptors, err := store.ownerSealedDescriptorsLocked(
			owner, ownerRoot,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptors...)
	}
	sortDescriptors(result)
	return result, nil
}

func sortDescriptors(descriptors []sealedDescriptor) {
	sort.Slice(descriptors, func(left, right int) bool {
		leftTime := descriptors[left].Manifest.SealedAt
		rightTime := descriptors[right].Manifest.SealedAt
		if !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if !descriptors[left].Manifest.FirstAt.Equal(
			descriptors[right].Manifest.FirstAt,
		) {
			return descriptors[left].Manifest.FirstAt.Before(
				descriptors[right].Manifest.FirstAt,
			)
		}
		if descriptors[left].Owner.Key() != descriptors[right].Owner.Key() {
			return descriptors[left].Owner.Key() <
				descriptors[right].Owner.Key()
		}
		return descriptors[left].Manifest.ID <
			descriptors[right].Manifest.ID
	})
}

func (store *Store) pruneSegmentLocked(
	descriptor sealedDescriptor,
) error {
	owner := descriptor.Owner
	ownerRoot := descriptor.OwnerRoot
	segmentID := descriptor.Manifest.ID
	state, err := store.loadStateLocked(owner, ownerRoot)
	if err != nil {
		return err
	}
	addStateReason(&state, RetentionReasonPruned)
	addCoverageGaps(
		&state, owner, segmentID,
		CoverageReasonRetentionPruned, descriptor.Manifest.Scopes,
	)
	if !slices.Contains(state.PendingPruneIDs, segmentID) {
		state.PendingPruneIDs = append(state.PendingPruneIDs, segmentID)
	}
	if err := store.saveStateLocked(owner, ownerRoot, state); err != nil {
		return err
	}
	if err := store.movePruneFilesLocked(ownerRoot, segmentID); err != nil {
		return err
	}
	if err := store.removePruningFilesLocked(ownerRoot, segmentID); err != nil {
		return err
	}
	state, err = store.loadStateLocked(owner, ownerRoot)
	if err != nil {
		return err
	}
	state.PendingPruneIDs = slices.DeleteFunc(
		state.PendingPruneIDs,
		func(value string) bool { return value == segmentID },
	)
	return store.saveStateLocked(owner, ownerRoot, state)
}

func (store *Store) finishPendingPrunesLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) error {
	state, err := store.loadStateLocked(owner, ownerRoot)
	if err != nil {
		return err
	}
	if len(state.PendingPruneIDs) == 0 {
		return store.removeOrphanPruningFilesLocked(ownerRoot)
	}
	for _, segmentID := range append([]string(nil), state.PendingPruneIDs...) {
		if err := store.movePruneFilesLocked(ownerRoot, segmentID); err != nil {
			return err
		}
		if err := store.removePruningFilesLocked(ownerRoot, segmentID); err != nil {
			return err
		}
		state.PendingPruneIDs = slices.DeleteFunc(
			state.PendingPruneIDs,
			func(value string) bool { return value == segmentID },
		)
	}
	if err := store.saveStateLocked(owner, ownerRoot, state); err != nil {
		return err
	}
	return store.removeOrphanPruningFilesLocked(ownerRoot)
}

func (store *Store) movePruneFilesLocked(
	ownerRoot, segmentID string,
) error {
	pruningRoot := filepath.Join(ownerRoot, pruningDirectory)
	sources := []string{
		segmentPath(ownerRoot, segmentID),
		manifestPath(ownerRoot, segmentID),
		indexPath(ownerRoot, segmentID),
	}
	for _, source := range sources {
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := validatePrivateRegular(source, info); err != nil {
			return err
		}
		destination := filepath.Join(pruningRoot, filepath.Base(source))
		if _, err := os.Lstat(destination); err == nil {
			return ErrStoreCorrupt
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
	}
	for _, directory := range []string{
		filepath.Join(ownerRoot, sealedDirectory),
		filepath.Join(ownerRoot, indexDirectory),
		pruningRoot,
	} {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) removePruningFilesLocked(
	ownerRoot, segmentID string,
) error {
	pruningRoot := filepath.Join(ownerRoot, pruningDirectory)
	for _, suffix := range []string{segmentSuffix, manifestSuffix, indexSuffix} {
		if err := removePrivateRegular(
			filepath.Join(pruningRoot, segmentID+suffix),
		); err != nil {
			return err
		}
	}
	return syncDirectory(pruningRoot)
}

func (store *Store) removeOrphanPruningFilesLocked(ownerRoot string) error {
	root := filepath.Join(ownerRoot, pruningDirectory)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		// A pruning artifact without the pending intent is not safely
		// attributable to a completed quota operation.
		return ErrStoreCorrupt
	}
	return nil
}

func (store *Store) listOwnersLocked() ([]workloadtypes.ActivityOwner, error) {
	entries, err := os.ReadDir(filepath.Join(store.root, ownersDirectory))
	if err != nil {
		return nil, err
	}
	result := make([]workloadtypes.ActivityOwner, 0, len(entries))
	for _, entry := range entries {
		ownerRoot := filepath.Join(store.root, ownersDirectory, entry.Name())
		info, err := os.Lstat(ownerRoot)
		if err != nil {
			return nil, err
		}
		if err := validatePrivateDirectory(ownerRoot, info); err != nil {
			return nil, err
		}
		metadata, err := readOwnerMetadata(ownerRoot)
		if err != nil {
			return nil, err
		}
		if metadata.Owner.Key() != entry.Name() {
			return nil, ErrStoreCorrupt
		}
		result = append(result, metadata.Owner)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Key() < result[right].Key()
	})
	return result, nil
}

func descriptorAge(descriptor sealedDescriptor) time.Time {
	return descriptor.Manifest.SealedAt
}
