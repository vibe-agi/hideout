package fanotify

import (
	"errors"
	"slices"
	"testing"
	"time"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestKindsForMaskIsDeterministicAndRejectsUnsupportedEvidence(t *testing.T) {
	kinds, err := KindsForMask(MaskOpen | MaskAccess | MaskModify | MaskOnDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := []filecollector.EventKind{
		filecollector.EventOpen,
		filecollector.EventRead,
		filecollector.EventWrite,
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds=%q want=%q", kinds, want)
	}
	for name, mask := range map[string]uint64{
		"empty":       0,
		"overflow":    MaskQueueOverflow,
		"unknown":     1 << 63,
		"close-write": MaskCloseWrite,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := KindsForMask(mask); !errors.Is(err, ErrUnsupportedMask) {
				t.Fatalf("error=%v want=%v", err, ErrUnsupportedMask)
			}
		})
	}
}

func TestNormalizerEmitsHonestPartialFileEvidence(t *testing.T) {
	boundary, actor := fanotifyTestBoundary(t)
	normalizer, err := NewNormalizer(
		boundary,
		"cov_fanotify_fixture",
		filecollector.NewPathClassifier([]string{"/workspace"}, nil, nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	event, err := normalizer.Normalize(RawEvent{
		Kind: filecollector.EventWrite, Sequence: 7, At: at,
		PID: 42, Actor: actor, ActorResolved: true,
		Path: "/workspace/output.txt", PathState: "resolved",
		FileType: "regular", Device: 8, Inode: 99, MountID: 77,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLimitations := []string{
		"actor-inferred",
		"bytes-unavailable",
		"fanotify-merged",
		"membership-receipt-pid",
		"mmap-unavailable",
		"outcome-unavailable",
		"timestamp-receipt",
	}
	if event.Attribution != workloadtypes.AttributionInferred ||
		event.Actor.ExecutionID != actor.ExecutionID ||
		event.Bytes != 0 ||
		event.PathClass != "workspace" ||
		event.Outcome.Status != workloadtypes.OutcomeUnknown ||
		!slices.Equal(event.Limitations, wantLimitations) {
		t.Fatalf("event=%+v", event)
	}
	fileNormalizer, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		t.Fatal(err)
	}
	record, err := fileNormalizer.Normalize(event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Operation != "write" ||
		record.Attribution != workloadtypes.AttributionInferred ||
		record.Actor == nil ||
		record.Actor.ExecutionID != actor.ExecutionID {
		t.Fatalf("record=%+v", record)
	}

	unknown, err := normalizer.Normalize(RawEvent{
		Kind: filecollector.EventRead, Sequence: 8, At: at,
		PID: 44, PathState: "unknown", FileType: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Attribution != workloadtypes.AttributionUnknown ||
		unknown.Actor != (workloadtypes.Actor{}) ||
		!slices.Contains(unknown.Limitations, "actor-unresolved") ||
		!slices.Contains(unknown.Limitations, "identity-unavailable") ||
		!slices.Contains(unknown.Limitations, "path-unavailable") {
		t.Fatalf("unknown event=%+v", unknown)
	}
	if _, err := fileNormalizer.Normalize(unknown); err != nil {
		t.Fatalf("unknown evidence must remain persistable: %v", err)
	}
}

func TestCoverageTrackerIsAlwaysPartialAndAccountsForLossLowerBounds(t *testing.T) {
	boundary, _ := fanotifyTestBoundary(t)
	startedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	tracker, err := NewCoverageTracker(
		boundary,
		"cov_fanotify_fixture",
		3,
		11,
		startedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	interval := tracker.Snapshot()
	if interval.State != workloadtypes.CoveragePartial ||
		interval.Reason != "fanotify-fallback" ||
		interval.DroppedEventCount != 0 ||
		!coverageHasEvidence(
			interval,
			"fanotify.drop-count-semantics",
			"known-target-lower-bound",
		) ||
		!coverageHasEvidence(interval, "fanotify.events-may-merge", "") ||
		!coverageHasEvidence(interval, "fanotify.mark-scope", "mount") ||
		!coverageHasEvidence(
			interval,
			"fanotify.membership-semantics",
			"receipt-time-pid",
		) ||
		!coverageHasEvidence(interval, "fanotify.mmap-unavailable", "") ||
		!coverageHasEvidence(interval, "fanotify.operation-scope", "open,read,write") ||
		!coverageHasEvidence(interval, "fanotify.queue-overflow-possible", "") {
		t.Fatalf("initial interval=%+v", interval)
	}
	if err := interval.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordLoss(LossQueueOverflow); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordLoss(LossFilterUnresolved); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordLoss(LossUnsupportedMask); err != nil {
		t.Fatal(err)
	}
	interval = tracker.Snapshot()
	if interval.State != workloadtypes.CoveragePartial ||
		interval.DroppedEventCount != 1 ||
		!coverageHasEvidence(interval, "fanotify.queue-overflow", "1") ||
		!coverageHasEvidence(interval, "fanotify.filter-unresolved", "1") ||
		!coverageHasEvidence(interval, "fanotify.unsupported-mask", "1") {
		t.Fatalf("lossy interval=%+v", interval)
	}
	endedAt := startedAt.Add(time.Minute)
	if err := tracker.Close(19, endedAt); err != nil {
		t.Fatal(err)
	}
	interval = tracker.Snapshot()
	if interval.EndSequence == nil || *interval.EndSequence != 19 ||
		interval.EndedAt == nil || !interval.EndedAt.Equal(endedAt) {
		t.Fatalf("closed interval=%+v", interval)
	}
}

func coverageHasEvidence(
	interval workloadtypes.CoverageInterval,
	code, value string,
) bool {
	for _, evidence := range interval.Evidence {
		if evidence.Code == code && evidence.Value == value {
			return true
		}
	}
	return false
}

func fanotifyTestBoundary(t *testing.T) (filecollector.Boundary, workloadtypes.Actor) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_20260729T120000Z_fanotify"
	executionID, err := workloadtypes.NewExecutionID(
		workloadtypes.ExecutionIdentityInput{
			Owner: owner, SessionID: sessionID,
			GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
			ObserverGeneration: 1, PID: 42, ExecSequence: 1,
			StartedAtMonoNS: 1000,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return filecollector.Boundary{
			Owner: owner, SessionID: sessionID,
			CgroupID: 3141, ObserverGeneration: 1,
		}, workloadtypes.Actor{
			ExecutionID: executionID,
			PID:         42, UID: 1000, GID: 1000,
		}
}
