package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActiveSegmentRepairsTornTailAndReportsCoverageGap(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_repair")
	base := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	now := base
	options := testOptionsFunc(root, func() time.Time { return now })

	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	first := testFileRecord(owner, "ses_repair", 1, base, "/workspace/first")
	second := testFileRecord(owner, "ses_repair", 2, base.Add(time.Second), "/workspace/second")
	if err := activity.Append(context.Background(), first); err != nil {
		t.Fatalf("append first record: %v", err)
	}
	if err := activity.Append(context.Background(), second); err != nil {
		t.Fatalf("append second record: %v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	activePath := filepath.Join(root, ownersDirectory, owner.Key(), activeSegmentFile)
	before, err := os.Stat(activePath)
	if err != nil {
		t.Fatalf("stat active segment: %v", err)
	}
	file, err := os.OpenFile(activePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open active segment for fault injection: %v", err)
	}
	if _, err := file.Write([]byte{0, 0, 0}); err != nil {
		_ = file.Close()
		t.Fatalf("inject torn frame: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close injected active segment: %v", err)
	}
	now = base.Add(10 * time.Second)

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen repaired store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	after, err := os.Stat(activePath)
	if err != nil {
		t.Fatalf("stat repaired active segment: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("active repair size = %d, want %d", after.Size(), before.Size())
	}
	repairs := reopened.Repairs()
	if len(repairs) != 1 || repairs[0].Kind != RepairTornWrite ||
		repairs[0].DiscardedBytes != 3 || repairs[0].OwnerKey != owner.Key() {
		t.Fatalf("unexpected repair report: %#v", repairs)
	}

	snapshot, err := reopened.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot repaired owner: %v", err)
	}
	if got := recordIDs(snapshot.Records); !slices.Equal(got, []string{first.ID, second.ID}) {
		t.Fatalf("repaired records = %v", got)
	}
	assertCorruptionGap(t, snapshot)
	assertCoverageGapExtendsThrough(t, snapshot, now)
}

func TestActiveSegmentCRCFailureTruncatesAfterLastValidFrame(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_crc")
	base := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	options := testOptions(root, base)

	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	first := testFileRecord(owner, "ses_crc", 1, base, "/workspace/first")
	second := testFileRecord(owner, "ses_crc", 2, base.Add(time.Second), "/workspace/second")
	if err := activity.Append(context.Background(), first); err != nil {
		t.Fatalf("append first record: %v", err)
	}
	if err := activity.Append(context.Background(), second); err != nil {
		t.Fatalf("append second record: %v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	activePath := filepath.Join(root, ownersDirectory, owner.Key(), activeSegmentFile)
	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("read active segment: %v", err)
	}
	firstFrameBytes := framedRecordBytes(t, data)
	if firstFrameBytes+frameHeaderBytes >= len(data) {
		t.Fatalf("fixture does not contain a second frame")
	}
	data[firstFrameBytes+frameHeaderBytes] ^= 0x01
	if err := os.WriteFile(activePath, data, 0o600); err != nil {
		t.Fatalf("inject CRC failure: %v", err)
	}

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen CRC-repaired store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repairs := reopened.Repairs()
	if len(repairs) != 1 || repairs[0].Kind != RepairCRCFailure ||
		repairs[0].ValidRecords != 1 {
		t.Fatalf("unexpected CRC repair report: %#v", repairs)
	}
	snapshot, err := reopened.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot repaired owner: %v", err)
	}
	if got := recordIDs(snapshot.Records); !slices.Equal(got, []string{first.ID}) {
		t.Fatalf("records after CRC repair = %v, want first only", got)
	}
	assertCorruptionGap(t, snapshot)
}

func TestAppendActivitiesCommitsBoundedBatchAcrossSegmentsAtomically(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_batch")
	other := testReusableOwner(t, "env_batch_other")
	base := time.Date(2026, 7, 29, 2, 30, 0, 0, time.UTC)
	options := Options{
		Root: root, ActiveSegmentBytes: 4 << 10,
		PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
		Now: func() time.Time { return base },
	}
	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	records := make([]workloadtypes.ActivityRecord, 12)
	for index := range records {
		records[index] = testFileRecord(
			owner,
			"ses_batch",
			uint64(index+1),
			base.Add(time.Duration(index)*time.Millisecond),
			"/workspace/"+strings.Repeat("batch-", 80)+
				leftPadSequence(uint64(index+1)),
		)
	}
	if err := activity.AppendActivities(context.Background(), records); err != nil {
		t.Fatalf("append activity batch: %v", err)
	}

	conflicting := []workloadtypes.ActivityRecord{
		testFileRecord(owner, "ses_batch", 100, base, "/workspace/not-committed"),
		testFileRecord(other, "ses_batch", 101, base, "/workspace/foreign"),
	}
	if err := activity.AppendActivities(
		context.Background(),
		conflicting,
	); !errors.Is(err, workloadtypes.ErrOwnerMismatch) {
		t.Fatalf("mixed-owner batch error=%v", err)
	}
	if err := activity.AppendActivities(
		context.Background(),
		make([]workloadtypes.ActivityRecord, MaxAppendBatchEntries+1),
	); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("oversized batch error=%v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	snapshot, err := reopened.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot batch: %v", err)
	}
	if got, want := recordIDs(snapshot.Records), recordIDs(records); !slices.Equal(got, want) {
		t.Fatalf("batch records=%v want=%v", got, want)
	}
}

func TestAppendExecutionsCommitsBatchAndKeepsLatestSnapshot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_execution_batch")
	other := testReusableOwner(t, "env_execution_batch_other")
	base := time.Date(2026, 7, 29, 2, 40, 0, 0, time.UTC)
	options := Options{
		Root: root, ActiveSegmentBytes: 4 << 10,
		PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
		Now: func() time.Time { return base },
	}
	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	executions := make([]workloadtypes.Execution, 8, 9)
	for index := range executions {
		executions[index] = testExecution(
			t,
			owner,
			"ses_execution_batch",
			uint32(100+index),
			uint64(index+1),
			uint64(1_000_000+index*10_000),
			base.Add(time.Duration(index)*time.Millisecond),
		)
		executions[index].Argv = []string{
			"fixture",
			strings.Repeat("bounded-argument-", 24) + leftPadSequence(uint64(index+1)),
		}
	}
	updated := executions[0]
	exitCode := 0
	updated.Exit = &workloadtypes.ExitObservation{
		Code: &exitCode, AtMonoNS: updated.StartedAtMonoNS + 5_000,
		At: updated.StartedAt.Add(5 * time.Millisecond),
	}
	executions = append(executions, updated)
	if err := activity.AppendExecutions(
		context.Background(),
		executions,
	); err != nil {
		t.Fatalf("append execution batch: %v", err)
	}

	conflicting := []workloadtypes.Execution{
		testExecution(
			t, owner, "ses_execution_batch", 200, 100, 2_000_000, base,
		),
		testExecution(
			t, other, "ses_execution_batch", 201, 101, 2_010_000, base,
		),
	}
	if err := activity.AppendExecutions(
		context.Background(),
		conflicting,
	); !errors.Is(err, workloadtypes.ErrOwnerMismatch) {
		t.Fatalf("mixed-owner execution batch error=%v", err)
	}
	if err := activity.AppendExecutions(
		context.Background(),
		make([]workloadtypes.Execution, MaxAppendBatchEntries+1),
	); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("oversized execution batch error=%v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	snapshot, err := reopened.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot execution batch: %v", err)
	}
	if len(snapshot.Executions) != 8 {
		t.Fatalf("execution count=%d want=8", len(snapshot.Executions))
	}
	if snapshot.Executions[0].ID != updated.ID ||
		snapshot.Executions[0].Exit == nil ||
		snapshot.Executions[0].Exit.Code == nil ||
		*snapshot.Executions[0].Exit.Code != exitCode {
		t.Fatalf("latest execution snapshot was not retained: %#v", snapshot.Executions[0])
	}
}

func TestSealScopesBoundIndependentPerCPUSequencesWithoutTimelineOrdering(
	t *testing.T,
) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_per_cpu_sequence")
	base := time.Date(2026, 7, 29, 2, 45, 0, 0, time.UTC)
	activity, err := Open(testOptions(root, base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activity.Close() })

	earlierHigh := testFileRecord(
		owner,
		"ses_per_cpu_sequence",
		900,
		base,
		"/workspace/earlier-high",
	)
	laterLow := testFileRecord(
		owner,
		"ses_per_cpu_sequence",
		1,
		base.Add(time.Millisecond),
		"/workspace/later-low",
	)
	if err := activity.AppendActivities(
		context.Background(),
		[]workloadtypes.ActivityRecord{earlierHigh, laterLow},
	); err != nil {
		t.Fatal(err)
	}
	manifest, err := activity.Seal(context.Background(), owner)
	if err != nil {
		t.Fatalf("seal cross-CPU sequence batch: %v", err)
	}
	if len(manifest.Scopes) != 1 ||
		manifest.Scopes[0].FirstSequence != 1 ||
		manifest.Scopes[0].LastSequence != 900 ||
		!manifest.Scopes[0].FirstAt.Equal(base) ||
		!manifest.Scopes[0].LastAt.Equal(base.Add(time.Millisecond)) {
		t.Fatalf("cross-CPU scope=%+v", manifest.Scopes)
	}
}

func TestSealManifestHashAndMissingIndexRebuild(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_seal")
	base := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	options := testOptions(root, base)

	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	first := testFileRecord(owner, "ses_seal", 1, base, "/workspace/first")
	second := testFileRecord(owner, "ses_seal", 2, base.Add(time.Second), "/workspace/second")
	if err := activity.Append(context.Background(), first); err != nil {
		t.Fatalf("append first record: %v", err)
	}
	if err := activity.Append(context.Background(), second); err != nil {
		t.Fatalf("append second record: %v", err)
	}
	manifest, err := activity.Seal(context.Background(), owner)
	if err != nil {
		t.Fatalf("seal segment: %v", err)
	}
	if manifest.Records != 2 || manifest.Owner.Key() != owner.Key() {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}

	ownerRoot := filepath.Join(root, ownersDirectory, owner.Key())
	segmentPath := filepath.Join(ownerRoot, sealedDirectory, manifest.ID+segmentSuffix)
	indexPath := filepath.Join(ownerRoot, indexDirectory, manifest.ID+indexSuffix)
	manifestPath := filepath.Join(ownerRoot, sealedDirectory, manifest.ID+manifestSuffix)
	assertDigestMatchesFile(t, manifest.SHA256, segmentPath)
	assertDigestMatchesFile(t, manifest.IndexDigest, indexPath)

	onDisk := readManifest(t, manifestPath)
	if !reflect.DeepEqual(onDisk, manifest) {
		t.Fatalf("on-disk manifest differs:\n got %#v\nwant %#v", onDisk, manifest)
	}
	expectedIndexDigest := manifest.IndexDigest
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index for rebuild injection: %v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen store with missing index: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertDigestMatchesFile(t, expectedIndexDigest, indexPath)
	if repairs := reopened.Repairs(); len(repairs) != 1 ||
		repairs[0].Kind != RepairIndexRebuilt ||
		repairs[0].SegmentID != manifest.ID {
		t.Fatalf("unexpected index repair report: %#v", repairs)
	}
	snapshot, err := reopened.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot sealed owner: %v", err)
	}
	if got := recordIDs(snapshot.Records); !slices.Equal(got, []string{first.ID, second.ID}) {
		t.Fatalf("sealed records = %v", got)
	}
	assertNoTemporaryFiles(t, ownerRoot)
}

func TestCorruptSealedSegmentIsQuarantinedAndNeverReturned(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_quarantine")
	base := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	options := testOptions(root, base)

	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	record := testFileRecord(owner, "ses_quarantine", 1, base, "/workspace/evidence")
	if err := activity.Append(context.Background(), record); err != nil {
		t.Fatalf("append record: %v", err)
	}
	manifest, err := activity.Seal(context.Background(), owner)
	if err != nil {
		t.Fatalf("seal segment: %v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	ownerRoot := filepath.Join(root, ownersDirectory, owner.Key())
	segmentPath := filepath.Join(ownerRoot, sealedDirectory, manifest.ID+segmentSuffix)
	data, err := os.ReadFile(segmentPath)
	if err != nil {
		t.Fatalf("read sealed segment: %v", err)
	}
	data[frameHeaderBytes] ^= 0x01
	if err := os.WriteFile(segmentPath, data, 0o600); err != nil {
		t.Fatalf("corrupt sealed segment: %v", err)
	}

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen store with corrupt sealed segment: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	snapshot, err := reopened.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot quarantined owner: %v", err)
	}
	if len(snapshot.Records) != 0 {
		t.Fatalf("corrupt sealed records were returned: %#v", snapshot.Records)
	}
	assertCorruptionGap(t, snapshot)

	quarantined, err := filepath.Glob(filepath.Join(
		ownerRoot, quarantineDirectory, manifest.ID+"*",
	))
	if err != nil {
		t.Fatalf("glob quarantine: %v", err)
	}
	if len(quarantined) < 1 {
		t.Fatalf("corrupt segment %s was not quarantined", manifest.ID)
	}
	if repairs := reopened.Repairs(); len(repairs) != 1 ||
		repairs[0].Kind != RepairSealedQuarantined ||
		repairs[0].SegmentID != manifest.ID {
		t.Fatalf("corrupt segment must be quarantined exactly once: %#v", repairs)
	}
	if _, err := os.Stat(segmentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed corrupt segment still present: %v", err)
	}
}

func TestQuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	ownerA := testReusableOwner(t, "env_quota_a")
	ownerB := testReusableOwner(t, "env_quota_b")
	base := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	now := base
	options := Options{
		Root: root, ActiveSegmentBytes: 4 << 10,
		PerOwnerBytes: 12 << 10, GlobalBytes: 7 << 10,
		Now: func() time.Time { return now },
	}
	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = activity.Close() })

	longPath := func(label string) string {
		return "/workspace/" + label + strings.Repeat("x", 2500)
	}
	oldest := testFileRecord(ownerA, "ses_quota_a", 1, base, longPath("oldest"))
	if err := activity.Append(context.Background(), oldest); err != nil {
		t.Fatalf("append oldest: %v", err)
	}
	oldestManifest, err := activity.Seal(context.Background(), ownerA)
	if err != nil {
		t.Fatalf("seal oldest: %v", err)
	}

	now = now.Add(time.Second)
	middle := testFileRecord(ownerB, "ses_quota_b", 2, base.Add(time.Second), longPath("middle"))
	if err := activity.Append(context.Background(), middle); err != nil {
		t.Fatalf("append middle: %v", err)
	}
	if _, err := activity.Seal(context.Background(), ownerB); err != nil {
		t.Fatalf("seal middle: %v", err)
	}

	now = now.Add(time.Second)
	newest := testFileRecord(ownerA, "ses_quota_a", 3, base.Add(2*time.Second), longPath("newest"))
	if err := activity.Append(context.Background(), newest); err != nil {
		t.Fatalf("append newest: %v", err)
	}
	if _, err := activity.Seal(context.Background(), ownerA); err != nil {
		t.Fatalf("seal newest: %v", err)
	}

	snapshotA, err := activity.Snapshot(context.Background(), ownerA)
	if err != nil {
		t.Fatalf("snapshot owner A: %v", err)
	}
	snapshotB, err := activity.Snapshot(context.Background(), ownerB)
	if err != nil {
		t.Fatalf("snapshot owner B: %v", err)
	}
	if slices.Contains(recordIDs(snapshotA.Records), oldest.ID) {
		t.Fatalf("oldest global segment was retained: %v", recordIDs(snapshotA.Records))
	}
	if !slices.Contains(recordIDs(snapshotA.Records), newest.ID) ||
		!slices.Contains(recordIDs(snapshotB.Records), middle.ID) {
		t.Fatalf("newer segments were pruned: A=%v B=%v",
			recordIDs(snapshotA.Records), recordIDs(snapshotB.Records))
	}
	if !snapshotA.Retention.Pruned ||
		!slices.Contains(snapshotA.Retention.Reasons, RetentionReasonPruned) ||
		!hasCoverageReason(snapshotA.Coverage, CoverageReasonRetentionPruned) {
		t.Fatalf("owner A missing explicit retention gap: %#v", snapshotA)
	}
	if _, err := os.Stat(filepath.Join(
		root, ownersDirectory, ownerA.Key(), sealedDirectory,
		oldestManifest.ID+segmentSuffix,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest segment still exists: %v", err)
	}

	stats, err := activity.Stats()
	if err != nil {
		t.Fatalf("store stats: %v", err)
	}
	if stats.UsedBytes > uint64(options.GlobalBytes+options.ActiveSegmentBytes) {
		t.Fatalf("global quota overshoot = %d, limit+active = %d",
			stats.UsedBytes, options.GlobalBytes+options.ActiveSegmentBytes)
	}
	if stats.LimitBytes != uint64(options.GlobalBytes) ||
		stats.DefaultOwnerLimitBytes != uint64(options.PerOwnerBytes) ||
		stats.ActiveSegmentBytes != uint64(options.ActiveSegmentBytes) {
		t.Fatalf("operator-visible store bounds=%+v", stats)
	}
	for _, owner := range []workloadtypes.ActivityOwner{ownerA, ownerB} {
		ownerStats, err := activity.OwnerStats(owner)
		if err != nil {
			t.Fatalf("owner stats: %v", err)
		}
		if ownerStats.UsedBytes > uint64(options.PerOwnerBytes+options.ActiveSegmentBytes) {
			t.Fatalf("owner quota overshoot = %d, limit+active = %d",
				ownerStats.UsedBytes, options.PerOwnerBytes+options.ActiveSegmentBytes)
		}
	}
}

func TestOwnerRetentionPolicyIsDurableAndCannotDrift(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_retention_policy")
	base := time.Date(2026, 7, 29, 5, 30, 0, 0, time.UTC)
	options := testOptions(root, base)
	policy := workloadtypes.ActivityRetentionPolicy{
		MaxBytes: 6 << 20, MaxAgeSeconds: 6 * 60 * 60,
	}

	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := activity.BindOwnerRetention(
		context.Background(), owner, policy,
	); err != nil {
		t.Fatalf("bind owner retention: %v", err)
	}
	stats, err := activity.OwnerStats(owner)
	if err != nil {
		t.Fatalf("owner stats: %v", err)
	}
	if stats.LimitBytes != uint64(policy.MaxBytes) ||
		stats.MaxAgeSeconds != policy.MaxAgeSeconds {
		t.Fatalf("owner retention stats=%+v want=%+v", stats, policy)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	stats, err = reopened.OwnerStats(owner)
	if err != nil {
		t.Fatalf("owner stats after reopen: %v", err)
	}
	if stats.LimitBytes != uint64(policy.MaxBytes) ||
		stats.MaxAgeSeconds != policy.MaxAgeSeconds {
		t.Fatalf("durable owner retention stats=%+v want=%+v", stats, policy)
	}
	if err := reopened.BindOwnerRetention(
		context.Background(),
		owner,
		workloadtypes.ActivityRetentionPolicy{
			MaxBytes:      policy.MaxBytes + 1024,
			MaxAgeSeconds: policy.MaxAgeSeconds,
		},
	); !errors.Is(err, ErrOwnerRetentionConflict) {
		t.Fatalf("retention drift error=%v", err)
	}
}

func TestOwnerRetentionMaxAgePrunesExpiredSealedHistory(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_retention_age")
	base := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	now := base
	options := testOptionsFunc(root, func() time.Time { return now })
	activity, err := Open(options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = activity.Close() })
	policy := workloadtypes.ActivityRetentionPolicy{
		MaxBytes: 6 << 20, MaxAgeSeconds: 60 * 60,
	}
	if err := activity.BindOwnerRetention(
		context.Background(), owner, policy,
	); err != nil {
		t.Fatalf("bind owner retention: %v", err)
	}

	old := testFileRecord(
		owner, "ses_retention_age", 1, base, "/workspace/old",
	)
	if err := activity.Append(context.Background(), old); err != nil {
		t.Fatalf("append old record: %v", err)
	}
	oldManifest, err := activity.Seal(context.Background(), owner)
	if err != nil {
		t.Fatalf("seal old segment: %v", err)
	}

	now = base.Add(2 * time.Hour)
	current := testFileRecord(
		owner, "ses_retention_age", 2, now, "/workspace/current",
	)
	if err := activity.Append(context.Background(), current); err != nil {
		t.Fatalf("append current record: %v", err)
	}
	if _, err := activity.Seal(context.Background(), owner); err != nil {
		t.Fatalf("seal current segment: %v", err)
	}

	snapshot, err := activity.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatalf("snapshot owner: %v", err)
	}
	if got := recordIDs(snapshot.Records); !slices.Equal(
		got, []string{current.ID},
	) {
		t.Fatalf("age-retained records=%v want current only", got)
	}
	if snapshot.Retention.MaxAgeSeconds != policy.MaxAgeSeconds ||
		!snapshot.Retention.Pruned ||
		!slices.Contains(
			snapshot.Retention.Reasons,
			RetentionReasonPruned,
		) ||
		!hasCoverageReason(
			snapshot.Coverage,
			CoverageReasonRetentionPruned,
		) {
		t.Fatalf("age pruning evidence=%+v", snapshot.Retention)
	}
	if _, err := os.Stat(filepath.Join(
		root,
		ownersDirectory,
		owner.Key(),
		sealedDirectory,
		oldManifest.ID+segmentSuffix,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired segment still exists: %v", err)
	}
}

func TestStoreRejectsUnredactedWrongOwnerAndCancelledWrites(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := testReusableOwner(t, "env_validation")
	other := testReusableOwner(t, "env_validation_other")
	base := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	activity, err := Open(testOptions(root, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = activity.Close() })

	record := testFileRecord(owner, "ses_validation", 1, base, "/workspace/file")
	record.RedactionStatus = workloadtypes.RedactionPending
	if err := activity.Append(context.Background(), record); !errors.Is(err, ErrRecordNotPersistable) {
		t.Fatalf("unredacted append error = %v", err)
	}
	record.RedactionStatus = workloadtypes.RedactionPassed
	if err := activity.appendForOwner(context.Background(), other, record); !errors.Is(err, workloadtypes.ErrOwnerMismatch) {
		t.Fatalf("wrong-owner append error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := activity.Append(ctx, record); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled append error = %v", err)
	}
}

func testOptions(root string, now time.Time) Options {
	return testOptionsFunc(root, func() time.Time { return now })
}

func testOptionsFunc(root string, now func() time.Time) Options {
	return Options{
		Root: root, ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
		Now: now,
	}
}

func testReusableOwner(t *testing.T, environmentID string) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(environmentID, "lima", "incarnation-"+environmentID)
	if err != nil {
		t.Fatalf("new owner: %v", err)
	}
	return owner
}

func testFileRecord(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	sequence uint64,
	at time.Time,
	path string,
) workloadtypes.ActivityRecord {
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     "act_record_" + leftPadSequence(sequence),
		Owner:  owner, SessionID: sessionID,
		Kind: workloadtypes.ActivityFile, Operation: "read",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: path,
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_record_" + leftPadSequence(sequence),
		RedactionStatus: workloadtypes.RedactionPassed,
	}
}

func testExecution(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	pid uint32,
	execSequence, monotonic uint64,
	at time.Time,
) workloadtypes.Execution {
	t.Helper()
	id, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, ExecSequence: execSequence,
		StartedAtMonoNS: monotonic,
	})
	if err != nil {
		t.Fatalf("new execution ID: %v", err)
	}
	return workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id,
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, TID: pid,
		ExecSequence: execSequence, StartedAtMonoNS: monotonic,
		StartedAt: at.UTC(), Executable: "/usr/bin/fixture",
		Argv: []string{"fixture"}, Cwd: "/workspace",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "developer", Group: "developer",
		},
	}
}

func leftPadSequence(sequence uint64) string {
	return fmt.Sprintf("%08d", sequence)
}

func framedRecordBytes(t *testing.T, data []byte) int {
	t.Helper()
	if len(data) < frameHeaderBytes {
		t.Fatalf("segment too short: %d", len(data))
	}
	payloadBytes := int(binary.BigEndian.Uint32(data[:frameHeaderBytes]))
	total := frameHeaderBytes + payloadBytes + frameChecksumBytes
	if payloadBytes <= 0 || total > len(data) {
		t.Fatalf("invalid first frame length %d in %d bytes", payloadBytes, len(data))
	}
	return total
}

func readManifest(t *testing.T, path string) SegmentManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest SegmentManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}

func assertDigestMatchesFile(t *testing.T, expected, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read digest target %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != expected {
		t.Fatalf("digest(%s) = %s, want %s", path, got, expected)
	}
}

func assertCorruptionGap(t *testing.T, snapshot workloadquery.Snapshot) {
	t.Helper()
	if !snapshot.Retention.Corrupt ||
		!slices.Contains(snapshot.Retention.Reasons, RetentionReasonCorrupt) {
		t.Fatalf("missing corruption retention state: %#v", snapshot.Retention)
	}
	if !hasCoverageReason(snapshot.Coverage, CoverageReasonStoreCorrupt) {
		t.Fatalf("missing store-corrupt coverage interval: %#v", snapshot.Coverage)
	}
	for _, interval := range snapshot.Coverage {
		if interval.Reason == CoverageReasonStoreCorrupt &&
			interval.State == workloadtypes.CoverageAvailable {
			t.Fatalf("corrupt interval claims Available: %#v", interval)
		}
	}
}

func assertCoverageGapExtendsThrough(
	t *testing.T,
	snapshot workloadquery.Snapshot,
	through time.Time,
) {
	t.Helper()
	for _, interval := range snapshot.Coverage {
		if interval.Reason != CoverageReasonStoreCorrupt {
			continue
		}
		if interval.EndedAt == nil || interval.EndedAt.Before(through) {
			t.Fatalf("corruption gap ends before detection: %#v", interval)
		}
		return
	}
	t.Fatal("corruption gap is missing")
}

func hasCoverageReason(intervals []workloadtypes.CoverageInterval, reason string) bool {
	return slices.ContainsFunc(intervals, func(interval workloadtypes.CoverageInterval) bool {
		return interval.Reason == reason
	})
}

func recordIDs(records []workloadtypes.ActivityRecord) []string {
	result := make([]string, len(records))
	for index := range records {
		result[index] = records[index].ID
	}
	return result
}

func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("temporary file remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk store: %v", err)
	}
}
