package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"golang.org/x/sys/unix"
)

func (store *Store) recoverAllLocked() error {
	if err := store.recoverPendingDeletionsLocked(); err != nil {
		return err
	}
	ownersRoot := filepath.Join(store.root, ownersDirectory)
	entries, err := os.ReadDir(ownersRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		ownerRoot := filepath.Join(ownersRoot, entry.Name())
		info, err := os.Lstat(ownerRoot)
		if err != nil {
			return err
		}
		if err := validatePrivateDirectory(ownerRoot, info); err != nil {
			return err
		}
		metadata, err := readOwnerMetadata(ownerRoot)
		if err != nil {
			return err
		}
		if entry.Name() != metadata.Owner.Key() {
			return ErrStoreCorrupt
		}
		if err := store.recoverOwnerLocked(metadata.Owner, ownerRoot); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) recoverOwnerLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) error {
	info, err := os.Lstat(ownerRoot)
	if err != nil {
		return err
	}
	if err := validatePrivateDirectory(ownerRoot, info); err != nil {
		return err
	}
	if _, err := store.readOwnerMetadataLocked(ownerRoot, owner); err != nil {
		return err
	}
	for _, name := range []string{
		sealedDirectory, indexDirectory, quarantineDirectory, pruningDirectory,
	} {
		path := filepath.Join(ownerRoot, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if err := ensurePrivateDirectory(path); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := validatePrivateDirectory(path, info); err != nil {
			return err
		}
	}
	if err := store.finishPendingPrunesLocked(owner, ownerRoot); err != nil {
		return err
	}
	activePath := filepath.Join(ownerRoot, activeSegmentFile)
	if _, err := os.Lstat(activePath); errors.Is(err, os.ErrNotExist) {
		if err := createPrivateFile(activePath); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	scan, err := scanSegment(
		activePath,
		store.options.ActiveSegmentBytes,
		func(entry segmentEntry) error { return entry.validate(owner) },
	)
	if err != nil {
		return err
	}
	if scan.FailureKind != "" {
		if err := store.repairActiveLocked(owner, ownerRoot, scan); err != nil {
			return err
		}
	}
	return store.recoverSealedLocked(owner, ownerRoot)
}

func (store *Store) repairActiveLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
	scan segmentScan,
) error {
	if scan.FailureKind == "" || scan.ValidBytes < 0 ||
		scan.ValidBytes > int64(len(scan.Data)) {
		return ErrStoreCorrupt
	}
	state, err := store.loadStateLocked(owner, ownerRoot)
	if err != nil {
		return err
	}
	addStateReason(&state, RetentionReasonCorrupt)
	scopes := buildSegmentScopes(scan.Entries)
	extendSegmentScopesThrough(scopes, store.nowLocked())
	addCoverageGaps(
		&state, owner, "active", CoverageReasonStoreCorrupt,
		scopes,
	)
	if err := store.saveStateLocked(owner, ownerRoot, state); err != nil {
		return err
	}
	activePath := filepath.Join(ownerRoot, activeSegmentFile)
	file, err := openPrivateFile(activePath, unix.O_WRONLY, false)
	if err != nil {
		return err
	}
	if err := file.Truncate(scan.ValidBytes); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	store.repairs = append(store.repairs, Repair{
		Kind: scan.FailureKind, OwnerKey: owner.Key(),
		ValidRecords:   uint64(len(scan.Entries)),
		DiscardedBytes: scan.DiscardedBytes,
	})
	return nil
}

func (store *Store) recoverSealedLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) error {
	sealedRoot := filepath.Join(ownerRoot, sealedDirectory)
	entries, err := os.ReadDir(sealedRoot)
	if err != nil {
		return err
	}
	segments := make(map[string]struct{})
	manifests := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(sealedRoot, name)
		if strings.Contains(name, ".tmp-") {
			if err := removePrivateRegular(path); err != nil {
				return err
			}
			continue
		}
		switch {
		case strings.HasSuffix(name, manifestSuffix):
			segmentID := strings.TrimSuffix(name, manifestSuffix)
			if !validSegmentID(segmentID) {
				return ErrStoreCorrupt
			}
			manifests[segmentID] = struct{}{}
		case strings.HasSuffix(name, segmentSuffix):
			segmentID := strings.TrimSuffix(name, segmentSuffix)
			if !validSegmentID(segmentID) {
				return ErrStoreCorrupt
			}
			segments[segmentID] = struct{}{}
		default:
			return ErrStoreCorrupt
		}
	}
	segmentIDs := make([]string, 0, len(segments))
	for segmentID := range segments {
		segmentIDs = append(segmentIDs, segmentID)
	}
	sort.Strings(segmentIDs)
	for _, segmentID := range segmentIDs {
		manifest, manifestPresent, manifestValid := store.tryReadManifestLocked(
			owner, ownerRoot, segmentID,
		)
		scan, err := scanSegment(
			segmentPath(ownerRoot, segmentID),
			store.options.ActiveSegmentBytes,
			func(entry segmentEntry) error { return entry.validate(owner) },
		)
		if err != nil {
			return err
		}
		if scan.FailureKind != "" {
			scopes := buildSegmentScopes(scan.Entries)
			if manifestPresent && manifestValid {
				scopes = manifest.Scopes
			}
			if err := store.quarantineSegmentLocked(
				owner, ownerRoot, segmentID, scopes,
				scan.DiscardedBytes,
			); err != nil {
				return err
			}
			delete(manifests, segmentID)
			delete(segments, segmentID)
			continue
		}
		_, indexData, indexDigest, err := buildSegmentIndex(
			segmentID, owner, scan.Entries,
		)
		if err != nil {
			return err
		}
		if manifestPresent {
			sum := sha256.Sum256(scan.Data)
			if !manifestValid ||
				manifest.Bytes != uint64(len(scan.Data)) ||
				manifest.Records != uint64(len(scan.Entries)) ||
				manifest.SHA256 != hex.EncodeToString(sum[:]) ||
				manifest.IndexDigest != indexDigest {
				scopes := buildSegmentScopes(scan.Entries)
				if manifestValid {
					scopes = manifest.Scopes
				}
				if err := store.quarantineSegmentLocked(
					owner, ownerRoot, segmentID, scopes, int64(len(scan.Data)),
				); err != nil {
					return err
				}
				delete(manifests, segmentID)
				delete(segments, segmentID)
				continue
			}
		} else {
			info, err := os.Lstat(segmentPath(ownerRoot, segmentID))
			if err != nil {
				return err
			}
			sealedAt := info.ModTime().UTC()
			if sealedAt.IsZero() {
				sealedAt = store.nowLocked()
			}
			manifest, err = buildManifest(
				segmentID, owner, scan.Data, scan.Entries,
				indexDigest, sealedAt,
			)
			if err != nil {
				return err
			}
			if err := writeAtomicBytes(
				filepath.Join(ownerRoot, indexDirectory),
				segmentID+indexSuffix, indexData,
			); err != nil {
				return err
			}
			if _, err := writeAtomicJSON(
				sealedRoot, segmentID+manifestSuffix, manifest,
			); err != nil {
				return err
			}
			store.repairs = append(store.repairs, Repair{
				Kind:     RepairOrphanSealedRecovered,
				OwnerKey: owner.Key(), SegmentID: segmentID,
				ValidRecords: uint64(len(scan.Entries)),
			})
		}
		expected, expectedData, expectedDigest, err := buildSegmentIndex(
			segmentID, owner, scan.Entries,
		)
		if err != nil {
			return err
		}
		rebuilt, err := verifyOrRebuildIndex(
			ownerRoot, manifest, expected, expectedData, expectedDigest,
		)
		if err != nil {
			return err
		}
		if rebuilt {
			store.repairs = append(store.repairs, Repair{
				Kind: RepairIndexRebuilt, OwnerKey: owner.Key(),
				SegmentID:    segmentID,
				ValidRecords: uint64(len(scan.Entries)),
			})
		}
		delete(manifests, segmentID)
	}
	for segmentID := range manifests {
		if err := store.quarantineSegmentLocked(
			owner, ownerRoot, segmentID, nil, 0,
		); err != nil {
			return err
		}
	}
	return store.recoverOrphanIndexesLocked(owner, ownerRoot, segments)
}

func (store *Store) tryReadManifestLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot, segmentID string,
) (SegmentManifest, bool, bool) {
	path := manifestPath(ownerRoot, segmentID)
	data, err := readPrivateFile(path, 2<<20)
	if errors.Is(err, os.ErrNotExist) {
		return SegmentManifest{}, false, false
	}
	if err != nil {
		return SegmentManifest{}, true, false
	}
	var manifest SegmentManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil ||
		validateManifest(manifest, owner, segmentID) != nil {
		return SegmentManifest{}, true, false
	}
	return manifest, true, true
}

func (store *Store) quarantineSegmentLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot, segmentID string,
	scopes []segmentScope,
	discardedBytes int64,
) error {
	state, err := store.loadStateLocked(owner, ownerRoot)
	if err != nil {
		return err
	}
	addStateReason(&state, RetentionReasonCorrupt)
	addCoverageGaps(
		&state, owner, segmentID, CoverageReasonStoreCorrupt, scopes,
	)
	if err := store.saveStateLocked(owner, ownerRoot, state); err != nil {
		return err
	}
	quarantineRoot := filepath.Join(ownerRoot, quarantineDirectory)
	for _, source := range []string{
		segmentPath(ownerRoot, segmentID),
		manifestPath(ownerRoot, segmentID),
		indexPath(ownerRoot, segmentID),
	} {
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
		destination := filepath.Join(quarantineRoot, filepath.Base(source))
		if _, err := os.Lstat(destination); err == nil {
			suffix, randomErr := randomName(6)
			if randomErr != nil {
				return randomErr
			}
			destination += "." + suffix
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
	}
	if err := syncDirectory(quarantineRoot); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(ownerRoot, sealedDirectory)); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(ownerRoot, indexDirectory)); err != nil {
		return err
	}
	store.repairs = append(store.repairs, Repair{
		Kind: RepairSealedQuarantined, OwnerKey: owner.Key(),
		SegmentID: segmentID, DiscardedBytes: discardedBytes,
	})
	return nil
}

func (store *Store) recoverOrphanIndexesLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
	segments map[string]struct{},
) error {
	indexRoot := filepath.Join(ownerRoot, indexDirectory)
	entries, err := os.ReadDir(indexRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(indexRoot, entry.Name())
		if strings.Contains(entry.Name(), ".tmp-") {
			if err := removePrivateRegular(path); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(entry.Name(), indexSuffix) {
			return ErrStoreCorrupt
		}
		segmentID := strings.TrimSuffix(entry.Name(), indexSuffix)
		if !validSegmentID(segmentID) {
			return ErrStoreCorrupt
		}
		if _, exists := segments[segmentID]; exists {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if err := validatePrivateRegular(path, info); err != nil {
			return err
		}
		destination := filepath.Join(
			ownerRoot, quarantineDirectory, entry.Name(),
		)
		if err := os.Rename(path, destination); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) readOwnerEntriesLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) ([]segmentEntry, []string, error) {
	manifests, err := store.listManifestsLocked(owner, ownerRoot)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(manifests, func(left, right int) bool {
		if !manifests[left].FirstAt.Equal(manifests[right].FirstAt) {
			return manifests[left].FirstAt.Before(manifests[right].FirstAt)
		}
		return manifests[left].ID < manifests[right].ID
	})
	var entries []segmentEntry
	var digests []string
	for _, manifest := range manifests {
		scan, err := scanSegment(
			segmentPath(ownerRoot, manifest.ID),
			store.options.ActiveSegmentBytes,
			func(entry segmentEntry) error { return entry.validate(owner) },
		)
		if err != nil || scan.FailureKind != "" {
			return nil, nil, errors.Join(ErrStoreCorrupt, err)
		}
		entries = append(entries, scan.Entries...)
		digests = append(digests, manifest.ID+":"+manifest.SHA256)
	}
	active, err := scanSegment(
		filepath.Join(ownerRoot, activeSegmentFile),
		store.options.ActiveSegmentBytes,
		func(entry segmentEntry) error { return entry.validate(owner) },
	)
	if err != nil || active.FailureKind != "" {
		return nil, nil, errors.Join(ErrStoreCorrupt, err)
	}
	entries = append(entries, active.Entries...)
	activeSum := sha256.Sum256(active.Data)
	digests = append(digests, "active:"+hex.EncodeToString(activeSum[:]))
	return entries, digests, nil
}

func (store *Store) listManifestsLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) ([]SegmentManifest, error) {
	entries, err := os.ReadDir(filepath.Join(ownerRoot, sealedDirectory))
	if err != nil {
		return nil, err
	}
	result := make([]SegmentManifest, 0, len(entries)/2)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), manifestSuffix) {
			continue
		}
		segmentID := strings.TrimSuffix(entry.Name(), manifestSuffix)
		manifest, present, valid := store.tryReadManifestLocked(
			owner, ownerRoot, segmentID,
		)
		if !present || !valid {
			return nil, ErrStoreCorrupt
		}
		result = append(result, manifest)
	}
	return result, nil
}

func readOwnerMetadata(ownerRoot string) (ownerMetadata, error) {
	data, err := readPrivateFile(
		filepath.Join(ownerRoot, ownerMetadataFile),
		64<<10,
	)
	if err != nil {
		return ownerMetadata{}, err
	}
	var metadata ownerMetadata
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil ||
		metadata.Schema != ownerMetadataSchema ||
		metadata.Owner.Validate() != nil ||
		(metadata.Retention != nil &&
			metadata.Retention.Validate() != nil) ||
		metadata.CreatedAt.IsZero() {
		return ownerMetadata{}, ErrStoreCorrupt
	}
	return metadata, nil
}

func removePrivateRegular(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validatePrivateRegular(path, info); err != nil {
		return err
	}
	return os.Remove(path)
}

func corruptionError(format string, arguments ...any) error {
	return errors.Join(ErrStoreCorrupt, fmt.Errorf(format, arguments...))
}
