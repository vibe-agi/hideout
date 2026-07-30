package coverage

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestTimelinePreservesLossAndRecoveryAsSeparateIntervals(t *testing.T) {
	timeline := timelineFixture(t)
	base := time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
	timeline.newID = sequentialCoverageIDs()
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoverageAvailable, Reason: ReasonObserverReady,
		CollectorGeneration: 1, Sequence: 1, At: base,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.MarkLoss(ReasonSequenceGap, 2, 4, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoverageAvailable, Reason: ReasonCollectorRecovered,
		CollectorGeneration: 1, Sequence: 5, At: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	intervals := timeline.Intervals(time.Time{}, time.Time{})
	if len(intervals) != 3 || intervals[0].EndedAt == nil || intervals[1].EndedAt == nil || intervals[2].EndedAt != nil {
		t.Fatalf("coverage intervals were merged or left ambiguous: %+v", intervals)
	}
	if intervals[1].DroppedEventCount != 2 || intervals[1].State != workloadtypes.CoveragePartial {
		t.Fatalf("loss interval is incomplete: %+v", intervals[1])
	}
	reasons := timeline.GapReasons(base, base.Add(3*time.Second))
	if len(reasons) != 1 || reasons[0] != ReasonSequenceGap {
		t.Fatalf("gap reasons=%v", reasons)
	}
	summary := timeline.Summary()
	if summary.Intervals != 3 || summary.DegradedIntervals != 1 ||
		summary.DroppedEventCount != 2 || summary.CurrentState != workloadtypes.CoverageAvailable {
		t.Fatalf("coverage summary=%+v", summary)
	}
}

func TestTimelineRejectsFalseAvailableAndRecordsRestartAndRetention(t *testing.T) {
	timeline := timelineFixture(t)
	base := time.Date(2026, 7, 29, 7, 30, 0, 0, time.UTC)
	timeline.newID = sequentialCoverageIDs()
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoverageAvailable, Reason: ReasonObserverReady,
		CollectorGeneration: 1, DroppedEventCount: 1, Sequence: 1, At: base,
	}); !errors.Is(err, workloadtypes.ErrFalseAvailableCoverage) {
		t.Fatalf("false Available error=%v want %v", err, workloadtypes.ErrFalseAvailableCoverage)
	}
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoveragePartial, Reason: ReasonCollectorRestarted,
		CollectorGeneration: 2, Sequence: 0, At: base,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.MarkRetentionGap(ReasonRetentionPruned, 1, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err := timeline.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !current.RetentionGap || current.CollectorGeneration != 2 {
		t.Fatalf("retention gap did not preserve generation: %+v", current)
	}
}

func TestTimelineQueriesAreExactOwnerCopies(t *testing.T) {
	timeline := timelineFixture(t)
	base := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	timeline.newID = sequentialCoverageIDs()
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoverageAvailable, Reason: ReasonObserverReady,
		CollectorGeneration: 1, Sequence: 1, At: base,
		Evidence: []workloadtypes.CoverageEvidence{{Code: "fixture", Value: "original"}},
	}); err != nil {
		t.Fatal(err)
	}
	first := timeline.Intervals(time.Time{}, time.Time{})
	first[0].Evidence[0].Value = "mutated-copy"
	second := timeline.Intervals(time.Time{}, time.Time{})
	if second[0].Evidence[0].Value != "original" {
		t.Fatal("query caller mutated authoritative coverage")
	}
	other, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "other-incarnation")
	if err != nil {
		t.Fatal(err)
	}
	if second[0].ValidateForOwner(other) == nil {
		t.Fatal("coverage query result crossed owner")
	}
}

func TestTimelineTransitionFailureDoesNotCloseCurrentInterval(t *testing.T) {
	timeline := timelineFixture(t)
	base := time.Date(2026, 7, 29, 8, 30, 0, 0, time.UTC)
	timeline.newID = sequentialCoverageIDs()
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoverageAvailable, Reason: ReasonObserverReady,
		CollectorGeneration: 1, Sequence: 1, At: base,
	}); err != nil {
		t.Fatal(err)
	}

	generateErr := errors.New("id generator unavailable")
	timeline.newID = func() (string, error) { return "", generateErr }
	if _, err := timeline.Transition(Transition{
		State: workloadtypes.CoveragePartial, Reason: ReasonSequenceGap,
		CollectorGeneration: 1, DroppedEventCount: 1,
		Sequence: 2, At: base.Add(time.Second),
	}); !errors.Is(err, generateErr) {
		t.Fatalf("transition error=%v want %v", err, generateErr)
	}

	intervals := timeline.Intervals(time.Time{}, time.Time{})
	if len(intervals) != 1 || intervals[0].EndedAt != nil || intervals[0].EndSequence != nil {
		t.Fatalf("failed transition mutated authoritative interval: %+v", intervals)
	}
}

func TestTimelineRecordsEveryRequiredCoverageDegradationAsAnInterval(t *testing.T) {
	tests := []struct {
		name           string
		reason         string
		state          string
		dropped        uint64
		retentionGap   bool
		nextGeneration uint64
		wantLoss       bool
		wantGapReason  bool
	}{
		{
			name: "hook loss", reason: ReasonHookUnavailable,
			state: workloadtypes.CoveragePartial, nextGeneration: 1,
			wantGapReason: true,
		},
		{
			name: "ring overflow", reason: ReasonRingOverflow,
			state: workloadtypes.CoveragePartial, dropped: 7, nextGeneration: 1,
			wantLoss: true, wantGapReason: true,
		},
		{
			name: "observer restart", reason: ReasonObserverRestarted,
			state: workloadtypes.CoveragePartial, nextGeneration: 2,
			wantLoss: true, wantGapReason: true,
		},
		{
			name: "daemon restart", reason: ReasonDaemonDisconnected,
			state: workloadtypes.CoverageUnavailable, nextGeneration: 1,
			wantLoss: true, wantGapReason: true,
		},
		{
			name: "schema mismatch", reason: ReasonSchemaMismatch,
			state: workloadtypes.CoveragePartial, dropped: 1, nextGeneration: 1,
			wantLoss: true, wantGapReason: true,
		},
		{
			name: "path ambiguity", reason: ReasonPathUnresolved,
			state: workloadtypes.CoveragePartial, nextGeneration: 1,
			wantGapReason: true,
		},
		{
			name: "actor ambiguity", reason: ReasonActorUnresolved,
			state: workloadtypes.CoveragePartial, nextGeneration: 1,
			wantGapReason: true,
		},
		{
			name: "encrypted DNS", reason: ReasonEncryptedDNS,
			state: workloadtypes.CoveragePartial, nextGeneration: 1,
			wantGapReason: true,
		},
		{
			name: "retention", reason: ReasonRetentionPruned,
			state: workloadtypes.CoveragePartial, retentionGap: true, nextGeneration: 1,
			wantLoss: true, wantGapReason: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			timeline := timelineFixture(t)
			timeline.newID = sequentialCoverageIDs()
			base := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
			if _, err := timeline.Transition(Transition{
				State: workloadtypes.CoverageAvailable, Reason: ReasonObserverReady,
				CollectorGeneration: 1, Sequence: 10, At: base,
			}); err != nil {
				t.Fatal(err)
			}

			degraded, err := timeline.Transition(Transition{
				State: test.state, Reason: test.reason,
				CollectorGeneration: test.nextGeneration,
				DroppedEventCount:   test.dropped, RetentionGap: test.retentionGap,
				Sequence: 11, At: base.Add(time.Second),
				Evidence: []workloadtypes.CoverageEvidence{{
					Code: "fixture", Value: test.name,
				}},
			})
			if err != nil {
				t.Fatalf("degrade coverage: %v", err)
			}
			if degraded.State == workloadtypes.CoverageAvailable ||
				degraded.Reason != test.reason ||
				degraded.DroppedEventCount != test.dropped ||
				degraded.RetentionGap != test.retentionGap {
				t.Fatalf("degraded interval=%+v", degraded)
			}
			definition, ok := Reason(test.reason)
			if !ok || definition.Loss != test.wantLoss {
				t.Fatalf("reason definition=%+v ok=%t want loss=%t", definition, ok, test.wantLoss)
			}

			if _, err := timeline.Transition(Transition{
				State: workloadtypes.CoverageAvailable, Reason: ReasonCollectorRecovered,
				CollectorGeneration: test.nextGeneration, Sequence: 12,
				At: base.Add(2 * time.Second),
			}); err != nil {
				t.Fatalf("recover coverage: %v", err)
			}
			intervals := timeline.Intervals(time.Time{}, time.Time{})
			if len(intervals) != 3 ||
				intervals[0].State != workloadtypes.CoverageAvailable ||
				intervals[0].EndedAt == nil ||
				intervals[1].State == workloadtypes.CoverageAvailable ||
				intervals[1].EndedAt == nil ||
				intervals[2].State != workloadtypes.CoverageAvailable ||
				intervals[2].EndedAt != nil {
				t.Fatalf("historical degradation was erased or merged: %+v", intervals)
			}
			reasons := timeline.GapReasons(base, base.Add(3*time.Second))
			if test.wantGapReason && (!slices.Contains(reasons, test.reason)) {
				t.Fatalf("gap reasons=%v want %q", reasons, test.reason)
			}
		})
	}
}

func TestTimelineRejectsLossReasonsWithoutLossEvidence(t *testing.T) {
	tests := []string{
		ReasonRingOverflow,
		ReasonSchemaMismatch,
	}
	for _, reason := range tests {
		t.Run(reason, func(t *testing.T) {
			timeline := timelineFixture(t)
			timeline.newID = sequentialCoverageIDs()
			_, err := timeline.Transition(Transition{
				State: workloadtypes.CoveragePartial, Reason: reason,
				CollectorGeneration: 1, Sequence: 1,
				At: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
			})
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("error=%v want %v", err, ErrInvalidTransition)
			}
		})
	}
}

func timelineFixture(t *testing.T) *Timeline {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := NewTimeline(owner, "ses_fixture", workloadtypes.SubsystemProcess)
	if err != nil {
		t.Fatal(err)
	}
	return timeline
}

func sequentialCoverageIDs() func() (string, error) {
	index := 0
	return func() (string, error) {
		index++
		return fmt.Sprintf("cov_fixture%04d", index), nil
	}
}
