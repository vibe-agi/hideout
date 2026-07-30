package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"golang.org/x/sys/unix"
)

func Open(options Options) (*Store, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	if err := prepareStoreRoot(normalized.Root); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(filepath.Join(normalized.Root, ownersDirectory)); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(filepath.Join(normalized.Root, deletingDirectory)); err != nil {
		return nil, err
	}
	store := &Store{options: normalized, root: normalized.Root}
	store.mu.Lock()
	err = store.recoverAllLocked()
	if err == nil {
		err = store.enforceRetentionLocked()
	}
	store.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return store, nil
}

func normalizeOptions(options Options) (Options, error) {
	if options.ActiveSegmentBytes == 0 {
		options.ActiveSegmentBytes = DefaultActiveSegmentBytes
	}
	if options.PerOwnerBytes == 0 {
		options.PerOwnerBytes = DefaultPerOwnerBytes
	}
	if options.GlobalBytes == 0 {
		options.GlobalBytes = DefaultGlobalBytes
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Root == "" || !filepath.IsAbs(options.Root) ||
		filepath.Clean(options.Root) != options.Root ||
		options.ActiveSegmentBytes < 1024 ||
		options.ActiveSegmentBytes > maxSegmentBytes ||
		options.PerOwnerBytes < 1024 ||
		options.PerOwnerBytes > maxStoreBytes ||
		options.GlobalBytes < 1024 ||
		options.GlobalBytes > maxStoreBytes {
		return Options{}, ErrInvalidOptions
	}
	probe := options.Now()
	if probe.IsZero() {
		return Options{}, ErrInvalidOptions
	}
	return options, nil
}

func (store *Store) Append(
	ctx context.Context,
	record workloadtypes.ActivityRecord,
) error {
	return store.appendForOwner(ctx, record.Owner, record)
}

func (store *Store) AppendRecord(
	ctx context.Context,
	record workloadtypes.ActivityRecord,
) error {
	return store.Append(ctx, record)
}

// AppendActivities durably commits a bounded, single-owner group with one
// fsync per active-segment chunk. Validating and encoding the complete group
// before the first write prevents a malformed later record from partially
// committing an otherwise valid prefix.
func (store *Store) AppendActivities(
	ctx context.Context,
	records []workloadtypes.ActivityRecord,
) error {
	if len(records) == 0 || len(records) > MaxAppendBatchEntries {
		return ErrInvalidOptions
	}
	owner := records[0].Owner
	entries := make([]segmentEntry, len(records))
	for index, record := range records {
		if err := record.ValidatePersistable(); err != nil {
			return errors.Join(ErrRecordNotPersistable, err)
		}
		if !record.Owner.Equal(owner) {
			return workloadtypes.ErrOwnerMismatch
		}
		entries[index] = activityEntry(record)
	}
	return store.appendEntries(ctx, owner, entries)
}

func (store *Store) appendForOwner(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
	record workloadtypes.ActivityRecord,
) error {
	if err := record.ValidatePersistable(); err != nil {
		return errors.Join(ErrRecordNotPersistable, err)
	}
	if !record.Owner.Equal(owner) {
		return workloadtypes.ErrOwnerMismatch
	}
	return store.appendEntry(ctx, owner, activityEntry(record))
}

func (store *Store) AppendExecution(
	ctx context.Context,
	execution workloadtypes.Execution,
) error {
	return store.AppendExecutions(ctx, []workloadtypes.Execution{execution})
}

// AppendExecutions durably commits a bounded, single-owner group with one
// fsync per active-segment chunk. Complete validation before the first write
// prevents a malformed later snapshot from committing a valid prefix.
func (store *Store) AppendExecutions(
	ctx context.Context,
	executions []workloadtypes.Execution,
) error {
	if len(executions) == 0 || len(executions) > MaxAppendBatchEntries {
		return ErrInvalidOptions
	}
	owner := executions[0].Owner
	entries := make([]segmentEntry, len(executions))
	for index, execution := range executions {
		if err := execution.Validate(); err != nil {
			return err
		}
		if !execution.Owner.Equal(owner) {
			return workloadtypes.ErrOwnerMismatch
		}
		entries[index] = executionEntry(execution)
	}
	return store.appendEntries(ctx, owner, entries)
}

func (store *Store) AppendCoverage(
	ctx context.Context,
	interval workloadtypes.CoverageInterval,
) error {
	if err := interval.Validate(); err != nil {
		return err
	}
	return store.appendEntry(ctx, interval.Owner, coverageEntry(interval))
}

func (store *Store) AppendRisk(
	ctx context.Context,
	finding risk.Finding,
) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	return store.appendEntry(ctx, finding.Owner, riskEntry(finding))
}

func (store *Store) appendEntry(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
	entry segmentEntry,
) error {
	return store.appendEntries(ctx, owner, []segmentEntry{entry})
}

func (store *Store) appendEntries(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
	entries []segmentEntry,
) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	if len(entries) == 0 || len(entries) > MaxAppendBatchEntries {
		return ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	frames := make([][]byte, len(entries))
	for index, entry := range entries {
		if err := entry.validate(owner); err != nil {
			return err
		}
		frame, err := encodeFrame(entry, store.options.ActiveSegmentBytes)
		if err != nil {
			return err
		}
		frames[index] = frame
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ownerRoot, err := store.ensureOwnerLocked(owner)
	if err != nil {
		return err
	}
	activePath := filepath.Join(ownerRoot, activeSegmentFile)
	for next := 0; next < len(frames); {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(activePath)
		if err != nil {
			return err
		}
		if err := validatePrivateRegular(activePath, info); err != nil {
			return err
		}
		activeBytes := info.Size()
		if activeBytes > 0 &&
			activeBytes+int64(len(frames[next])) >
				store.options.ActiveSegmentBytes {
			if _, err := store.sealLocked(ctx, owner, ownerRoot); err != nil {
				return err
			}
			continue
		}

		file, err := openPrivateFile(
			activePath,
			unix.O_WRONLY|unix.O_APPEND,
			false,
		)
		if err != nil {
			return err
		}
		for next < len(frames) &&
			activeBytes+int64(len(frames[next])) <=
				store.options.ActiveSegmentBytes {
			if err := writeAll(file, frames[next]); err != nil {
				_ = file.Close()
				return err
			}
			activeBytes += int64(len(frames[next]))
			next++
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) Seal(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (SegmentManifest, error) {
	if ctx == nil {
		return SegmentManifest{}, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return SegmentManifest{}, err
	}
	if err := owner.Validate(); err != nil {
		return SegmentManifest{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return SegmentManifest{}, err
	}
	ownerRoot, err := store.loadOwnerLocked(owner)
	if err != nil {
		return SegmentManifest{}, err
	}
	return store.sealLocked(ctx, owner, ownerRoot)
}

func (store *Store) sealLocked(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) (SegmentManifest, error) {
	if err := ctx.Err(); err != nil {
		return SegmentManifest{}, err
	}
	activePath := filepath.Join(ownerRoot, activeSegmentFile)
	scan, err := scanSegment(
		activePath,
		store.options.ActiveSegmentBytes,
		func(entry segmentEntry) error { return entry.validate(owner) },
	)
	if err != nil {
		return SegmentManifest{}, err
	}
	if scan.FailureKind != "" {
		if err := store.repairActiveLocked(owner, ownerRoot, scan); err != nil {
			return SegmentManifest{}, err
		}
		scan.Data = scan.Data[:scan.ValidBytes]
	}
	if len(scan.Entries) == 0 {
		return SegmentManifest{}, ErrEmptySegment
	}
	segmentID, err := newSegmentID()
	if err != nil {
		return SegmentManifest{}, err
	}
	sealedPath := segmentPath(ownerRoot, segmentID)
	if _, err := os.Lstat(sealedPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return SegmentManifest{}, ErrStoreCorrupt
	}
	file, err := openPrivateFile(activePath, unix.O_RDONLY, false)
	if err != nil {
		return SegmentManifest{}, err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return SegmentManifest{}, err
	}
	if err := os.Rename(activePath, sealedPath); err != nil {
		return SegmentManifest{}, err
	}
	if err := syncDirectory(filepath.Join(ownerRoot, sealedDirectory)); err != nil {
		return SegmentManifest{}, err
	}

	index, indexData, indexDigest, err := buildSegmentIndex(
		segmentID, owner, scan.Entries,
	)
	if err != nil {
		return SegmentManifest{}, err
	}
	_ = index
	if err := writeAtomicBytes(
		filepath.Join(ownerRoot, indexDirectory),
		segmentID+indexSuffix,
		indexData,
	); err != nil {
		return SegmentManifest{}, err
	}
	manifest, err := buildManifest(
		segmentID, owner, scan.Data, scan.Entries,
		indexDigest, store.nowLocked(),
	)
	if err != nil {
		return SegmentManifest{}, err
	}
	if _, err := writeAtomicJSON(
		filepath.Join(ownerRoot, sealedDirectory),
		segmentID+manifestSuffix,
		manifest,
	); err != nil {
		return SegmentManifest{}, err
	}
	if err := createPrivateFile(activePath); err != nil {
		return SegmentManifest{}, err
	}
	if err := syncDirectory(ownerRoot); err != nil {
		return SegmentManifest{}, err
	}
	if err := store.enforceRetentionLocked(); err != nil {
		return SegmentManifest{}, err
	}
	return manifest, nil
}

func (store *Store) Snapshot(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (workloadquery.Snapshot, error) {
	if ctx == nil {
		return workloadquery.Snapshot{}, workloadquery.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return workloadquery.Snapshot{}, err
	}
	if err := owner.Validate(); err != nil {
		return workloadquery.Snapshot{}, workloadquery.ErrOwnerNotFound
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return workloadquery.Snapshot{}, err
	}
	ownerRoot, err := store.loadOwnerLocked(owner)
	if err != nil {
		if errors.Is(err, ErrOwnerNotFound) {
			return workloadquery.Snapshot{}, workloadquery.ErrOwnerNotFound
		}
		return workloadquery.Snapshot{}, err
	}
	if err := store.recoverOwnerLocked(owner, ownerRoot); err != nil {
		return workloadquery.Snapshot{}, err
	}
	if err := store.enforceRetentionLocked(); err != nil {
		return workloadquery.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return workloadquery.Snapshot{}, err
	}

	entries, segmentDigests, err := store.readOwnerEntriesLocked(owner, ownerRoot)
	if err != nil {
		return workloadquery.Snapshot{}, err
	}
	state, err := store.loadStateLocked(owner, ownerRoot)
	if err != nil {
		return workloadquery.Snapshot{}, err
	}
	ownerStats, err := store.ownerStatsLocked(owner, ownerRoot)
	if err != nil {
		return workloadquery.Snapshot{}, err
	}
	snapshot := workloadquery.Snapshot{
		Owner:      owner,
		Records:    []workloadtypes.ActivityRecord{},
		Executions: []workloadtypes.Execution{},
		Coverage:   []workloadtypes.CoverageInterval{},
		Risks:      []risk.Finding{},
		Retention: workloadquery.RetentionState{
			UsedBytes:     ownerStats.UsedBytes,
			LimitBytes:    ownerStats.LimitBytes,
			MaxAgeSeconds: ownerStats.MaxAgeSeconds,
			Pruned:        state.Pruned, Corrupt: state.Corrupt,
			Reasons: append([]string(nil), state.Reasons...),
		},
	}
	recordByID := make(map[string]workloadtypes.ActivityRecord)
	executionByID := make(map[string]workloadtypes.Execution)
	coverageByID := make(map[string]workloadtypes.CoverageInterval)
	riskByID := make(map[string]risk.Finding)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return workloadquery.Snapshot{}, err
		}
		first, last := entry.timeRange()
		observeRetentionRange(&snapshot.Retention, first, last)
		switch entry.Kind {
		case entryActivity:
			recordByID[entry.Activity.ID] = *entry.Activity
		case entryExecution:
			executionByID[entry.Execution.ID] = *entry.Execution
		case entryCoverage:
			coverageByID[entry.Coverage.ID] = *entry.Coverage
		case entryRisk:
			riskByID[entry.Risk.ID] = *entry.Risk
		}
	}
	for _, record := range recordByID {
		snapshot.Records = append(snapshot.Records, record)
	}
	for _, execution := range executionByID {
		snapshot.Executions = append(snapshot.Executions, execution)
	}
	for _, interval := range coverageByID {
		snapshot.Coverage = append(snapshot.Coverage, interval)
	}
	for _, finding := range riskByID {
		snapshot.Risks = append(snapshot.Risks, finding)
	}
	snapshot.Coverage = append(snapshot.Coverage, cloneCoverageList(state.Gaps)...)
	sortSnapshot(&snapshot)
	snapshot.Revision = snapshotRevision(
		owner, segmentDigests, state, snapshot.Retention.UsedBytes,
	)
	return snapshot, nil
}

func (store *Store) Repairs() []Repair {
	if store == nil {
		return []Repair{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Repair(nil), store.repairs...)
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	store.closed = true
	store.mu.Unlock()
	return nil
}

func (store *Store) usableLocked() error {
	if store == nil || store.closed {
		return ErrClosed
	}
	info, err := os.Lstat(store.root)
	if err != nil {
		return errors.Join(ErrInsecurePath, err)
	}
	return validatePrivateDirectory(store.root, info)
}

func (store *Store) ensureOwnerLocked(
	owner workloadtypes.ActivityOwner,
) (string, error) {
	return store.ensureOwnerWithRetentionLocked(owner, nil)
}

func (store *Store) ensureOwnerWithRetentionLocked(
	owner workloadtypes.ActivityOwner,
	requested *workloadtypes.ActivityRetentionPolicy,
) (string, error) {
	if err := owner.Validate(); err != nil {
		return "", err
	}
	if requested != nil && requested.Validate() != nil {
		return "", workloadtypes.ErrActivityRetentionPolicy
	}
	ownerRoot := ownerPath(store.root, owner)
	info, err := os.Lstat(ownerRoot)
	switch {
	case err == nil:
		if err := validatePrivateDirectory(ownerRoot, info); err != nil {
			return "", err
		}
	case errors.Is(err, os.ErrNotExist):
		if err := ensurePrivateDirectory(ownerRoot); err != nil {
			return "", err
		}
	default:
		return "", errors.Join(ErrInsecurePath, err)
	}
	for _, name := range []string{
		sealedDirectory, indexDirectory, quarantineDirectory, pruningDirectory,
	} {
		if err := ensurePrivateDirectory(filepath.Join(ownerRoot, name)); err != nil {
			return "", err
		}
	}
	metadataPath := filepath.Join(ownerRoot, ownerMetadataFile)
	if _, err := os.Lstat(metadataPath); errors.Is(err, os.ErrNotExist) {
		retention := store.defaultOwnerRetentionPolicy()
		if requested != nil {
			retention = *requested
		}
		metadata := ownerMetadata{
			Schema: ownerMetadataSchema, Owner: owner,
			CreatedAt: store.nowLocked(),
			Retention: &retention,
		}
		if _, err := writeAtomicJSON(ownerRoot, ownerMetadataFile, metadata); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	metadata, err := store.readOwnerMetadataLocked(ownerRoot, owner)
	if err != nil {
		return "", err
	}
	if metadata.Retention == nil {
		retention := store.defaultOwnerRetentionPolicy()
		metadata.Retention = &retention
		if _, err := writeAtomicJSON(
			ownerRoot,
			ownerMetadataFile,
			metadata,
		); err != nil {
			return "", err
		}
	}
	if requested != nil && *metadata.Retention != *requested {
		return "", ErrOwnerRetentionConflict
	}
	activePath := filepath.Join(ownerRoot, activeSegmentFile)
	if _, err := os.Lstat(activePath); errors.Is(err, os.ErrNotExist) {
		if err := createPrivateFile(activePath); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else {
		info, err := os.Lstat(activePath)
		if err != nil {
			return "", err
		}
		if err := validatePrivateRegular(activePath, info); err != nil {
			return "", err
		}
	}
	return ownerRoot, nil
}

func (store *Store) loadOwnerLocked(
	owner workloadtypes.ActivityOwner,
) (string, error) {
	ownerRoot := ownerPath(store.root, owner)
	info, err := os.Lstat(ownerRoot)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrOwnerNotFound
	}
	if err != nil {
		return "", err
	}
	if err := validatePrivateDirectory(ownerRoot, info); err != nil {
		return "", err
	}
	if _, err := store.readOwnerMetadataLocked(ownerRoot, owner); err != nil {
		return "", err
	}
	return ownerRoot, nil
}

func (store *Store) readOwnerMetadataLocked(
	ownerRoot string,
	expected workloadtypes.ActivityOwner,
) (ownerMetadata, error) {
	data, err := readPrivateFile(
		filepath.Join(ownerRoot, ownerMetadataFile),
		64<<10,
	)
	if err != nil {
		return ownerMetadata{}, err
	}
	var metadata ownerMetadata
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil ||
		metadata.Schema != ownerMetadataSchema ||
		metadata.CreatedAt.IsZero() ||
		metadata.Owner.Validate() != nil ||
		(metadata.Retention != nil &&
			metadata.Retention.Validate() != nil) ||
		!metadata.Owner.Equal(expected) ||
		filepath.Base(ownerRoot) != metadata.Owner.Key() {
		return ownerMetadata{}, ErrStoreCorrupt
	}
	return metadata, nil
}

func (store *Store) defaultOwnerRetentionPolicy() (
	policy workloadtypes.ActivityRetentionPolicy,
) {
	return workloadtypes.ActivityRetentionPolicy{
		MaxBytes:      store.options.PerOwnerBytes,
		MaxAgeSeconds: 0,
	}
}

func ownerPath(root string, owner workloadtypes.ActivityOwner) string {
	return filepath.Join(root, ownersDirectory, owner.Key())
}

func segmentPath(ownerRoot, segmentID string) string {
	return filepath.Join(ownerRoot, sealedDirectory, segmentID+segmentSuffix)
}

func manifestPath(ownerRoot, segmentID string) string {
	return filepath.Join(ownerRoot, sealedDirectory, segmentID+manifestSuffix)
}

func indexPath(ownerRoot, segmentID string) string {
	return filepath.Join(ownerRoot, indexDirectory, segmentID+indexSuffix)
}

func newSegmentID() (string, error) {
	value, err := randomName(18)
	if err != nil {
		return "", err
	}
	return "seg_" + value, nil
}

func buildManifest(
	segmentID string,
	owner workloadtypes.ActivityOwner,
	data []byte,
	entries []segmentEntry,
	indexDigest string,
	sealedAt time.Time,
) (SegmentManifest, error) {
	if len(data) == 0 || len(entries) == 0 {
		return SegmentManifest{}, ErrEmptySegment
	}
	firstAt, lastAt := entries[0].timeRange()
	firstSeq, lastSeq := entries[0].sequences()
	for _, entry := range entries[1:] {
		first, last := entry.timeRange()
		if first.Before(firstAt) {
			firstAt = first
		}
		if last.After(lastAt) {
			lastAt = last
		}
		sequenceFirst, sequenceLast := entry.sequences()
		if firstSeq == 0 || sequenceFirst != 0 && sequenceFirst < firstSeq {
			firstSeq = sequenceFirst
		}
		if sequenceLast > lastSeq {
			lastSeq = sequenceLast
		}
	}
	sum := sha256.Sum256(data)
	manifest := SegmentManifest{
		Schema: segmentSchema, ID: segmentID, Owner: owner,
		FirstSeq: firstSeq, LastSeq: lastSeq,
		FirstAt: firstAt.UTC(), LastAt: lastAt.UTC(),
		Bytes: uint64(len(data)), Records: uint64(len(entries)),
		State: "sealed", SHA256: hex.EncodeToString(sum[:]),
		IndexDigest: indexDigest,
		CreatedAt:   firstAt.UTC(), SealedAt: sealedAt.UTC(),
		Scopes: buildSegmentScopes(entries),
	}
	if err := validateManifest(manifest, owner, segmentID); err != nil {
		return SegmentManifest{}, err
	}
	return manifest, nil
}

func validateManifest(
	manifest SegmentManifest,
	owner workloadtypes.ActivityOwner,
	segmentID string,
) error {
	if manifest.Schema != segmentSchema ||
		manifest.ID != segmentID ||
		!manifest.Owner.Equal(owner) ||
		!validSegmentID(manifest.ID) ||
		manifest.Bytes == 0 || manifest.Records == 0 ||
		manifest.State != "sealed" ||
		len(manifest.SHA256) != sha256.Size*2 ||
		len(manifest.IndexDigest) != sha256.Size*2 ||
		manifest.FirstAt.IsZero() ||
		manifest.LastAt.Before(manifest.FirstAt) ||
		manifest.CreatedAt.IsZero() || manifest.SealedAt.IsZero() ||
		len(manifest.Scopes) > maxSegmentScopes {
		return ErrStoreCorrupt
	}
	if _, err := hex.DecodeString(manifest.SHA256); err != nil {
		return ErrStoreCorrupt
	}
	if _, err := hex.DecodeString(manifest.IndexDigest); err != nil {
		return ErrStoreCorrupt
	}
	for _, scope := range manifest.Scopes {
		if err := validateSegmentScope(scope, owner); err != nil {
			return err
		}
	}
	return nil
}

func validSegmentID(value string) bool {
	if !strings.HasPrefix(value, "seg_") ||
		len(value) < 16 || len(value) > 96 {
		return false
	}
	for _, character := range value[len("seg_"):] {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func snapshotRevision(
	owner workloadtypes.ActivityOwner,
	digests []string,
	state ownerPersistentState,
	used uint64,
) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("hideout.activity-snapshot/v1\x00"))
	_, _ = hash.Write([]byte(owner.Key()))
	for _, digest := range digests {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(digest))
	}
	stateData, _ := json.Marshal(state)
	_, _ = hash.Write(stateData)
	_, _ = fmt.Fprintf(hash, "\x00%d", used)
	return "rev_" + base64.RawURLEncoding.EncodeToString(hash.Sum(nil)[:18])
}

func observeRetentionRange(
	retention *workloadquery.RetentionState,
	first, last time.Time,
) {
	if first.IsZero() || last.IsZero() {
		return
	}
	first, last = first.UTC(), last.UTC()
	if retention.EarliestAt.IsZero() || first.Before(retention.EarliestAt) {
		retention.EarliestAt = first
	}
	if retention.LatestAt.IsZero() || last.After(retention.LatestAt) {
		retention.LatestAt = last
	}
}

func sortSnapshot(snapshot *workloadquery.Snapshot) {
	sort.Slice(snapshot.Records, func(left, right int) bool {
		if !snapshot.Records[left].FirstAt.Equal(snapshot.Records[right].FirstAt) {
			return snapshot.Records[left].FirstAt.Before(snapshot.Records[right].FirstAt)
		}
		return snapshot.Records[left].ID < snapshot.Records[right].ID
	})
	sort.Slice(snapshot.Executions, func(left, right int) bool {
		if !snapshot.Executions[left].StartedAt.Equal(snapshot.Executions[right].StartedAt) {
			return snapshot.Executions[left].StartedAt.Before(snapshot.Executions[right].StartedAt)
		}
		return snapshot.Executions[left].ID < snapshot.Executions[right].ID
	})
	sort.Slice(snapshot.Coverage, func(left, right int) bool {
		if !snapshot.Coverage[left].StartedAt.Equal(snapshot.Coverage[right].StartedAt) {
			return snapshot.Coverage[left].StartedAt.Before(snapshot.Coverage[right].StartedAt)
		}
		return snapshot.Coverage[left].ID < snapshot.Coverage[right].ID
	})
	sort.Slice(snapshot.Risks, func(left, right int) bool {
		if !snapshot.Risks[left].FirstAt.Equal(snapshot.Risks[right].FirstAt) {
			return snapshot.Risks[left].FirstAt.Before(snapshot.Risks[right].FirstAt)
		}
		return snapshot.Risks[left].ID < snapshot.Risks[right].ID
	})
	slices.Sort(snapshot.Retention.Reasons)
}

func cloneCoverageList(
	input []workloadtypes.CoverageInterval,
) []workloadtypes.CoverageInterval {
	result := make([]workloadtypes.CoverageInterval, len(input))
	for index, interval := range input {
		result[index] = interval
		result[index].Evidence = append(
			[]workloadtypes.CoverageEvidence(nil),
			interval.Evidence...,
		)
		if interval.EndedAt != nil {
			value := *interval.EndedAt
			result[index].EndedAt = &value
		}
		if interval.EndSequence != nil {
			value := *interval.EndSequence
			result[index].EndSequence = &value
		}
	}
	return result
}

func (store *Store) nowLocked() time.Time {
	value := store.options.Now().UTC()
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}
