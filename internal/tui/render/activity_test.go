package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityReducedCoverageNeverPresentsNoRowsAsProofOfNoActivity(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewDisposableOwner(
		"ses_activityempty",
		"lima",
		"incarnation-empty",
	)
	if err != nil {
		t.Fatal(err)
	}
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_activityempty1",
		Owner: owner, SessionID: owner.SessionID, Subsystem: workloadtypes.SubsystemDNS,
		State: workloadtypes.CoverageUnavailable, Reason: "encrypted-dns",
		CollectorGeneration: 1, RetentionGap: true, StartedAt: now,
	}
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{
		DaemonInstanceID: "daemon_activityempty", CredentialGeneration: 1,
		EventSequence: 1, StreamHealth: liveconsole.HealthLive,
		Overview: manager.Overview{
			Version: "hideout.manager/v1",
			Sessions: []manager.SessionSummary{{
				ID: owner.SessionID, State: "running", CommandClass: "claude",
			}},
		},
		Coverage: []workloadtypes.CoverageInterval{coverage},
	}))
	output := Activity(ActivityInput{
		State: state, SessionID: owner.SessionID, Tab: "dns",
		Data: ActivityData{
			Owner: owner,
			Summary: manager.ActivitySummaryResult{
				Owner: owner, Counts: map[string]uint64{},
				CurrentCoverage: []workloadtypes.CoverageInterval{coverage},
				Reasons:         []string{"retention-pruned"}, Pruned: true,
			},
			Events: manager.ActivityEventsPage{
				Records:  []workloadtypes.ActivityRecord{},
				Coverage: []workloadtypes.CoverageInterval{coverage},
			},
			Coverage: manager.ActivityCoverageResult{
				Intervals: []workloadtypes.CoverageInterval{coverage},
				Current:   []workloadtypes.CoverageInterval{coverage},
			},
		},
		Loaded: true, Now: now,
	}, Options{Width: 96, Height: 24, NoColor: true})
	for _, expected := range []string{
		"No matching activity is not proof of zero activity",
		"DNS Unavailable",
		"encrypted-dns",
		"retention gap",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("reduced-coverage activity view missing %q:\n%s", expected, output)
		}
	}
}

func TestActivitySanitizesObservedFieldsAndFitsTerminal(t *testing.T) {
	fixture := renderFixture()
	record := fixture.Activity.Recent
	if len(record) == 0 {
		owner := fixture.Coverage[0].Owner
		record = []workloadtypes.ActivityRecord{{
			Schema: workloadtypes.ActivityRecordSchema, ID: "act_renderunsafe1",
			Owner: owner, SessionID: "ses_alpha",
			Kind: workloadtypes.ActivityFile, Operation: "write",
			Subject: workloadtypes.FileSubject{
				Kind:      workloadtypes.ActivityFile,
				Path:      "/workspace/a\x1b]8;;https://evil.invalid\aCLICK\x1b]8;;\a\u202efile",
				PathState: "resolved", PathClass: "workspace", FileType: "regular",
			},
			Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
			Count:   1, FirstAt: time.Now().UTC(), LastAt: time.Now().UTC(),
			FirstSequence: 1, LastSequence: 1,
			Attribution:     workloadtypes.AttributionExact,
			CoverageID:      fixture.Coverage[0].ID,
			RedactionStatus: workloadtypes.RedactionPassed,
		}}
	}
	output := Activity(ActivityInput{
		State: fixture, SessionID: "ses_alpha", Tab: "all",
		Data: ActivityData{
			Owner: fixture.Coverage[0].Owner,
			Events: manager.ActivityEventsPage{
				Records: record, Coverage: fixture.Coverage,
			},
			Coverage: manager.ActivityCoverageResult{
				Intervals: fixture.Coverage, Current: fixture.Coverage,
			},
		},
		Loaded: true,
	}, Options{Width: 82, Height: 22, NoColor: true})
	for _, forbidden := range []string{"\x1b", "\a", "\u202e", "https://evil.invalid"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("activity render leaked terminal control %q: %q", forbidden, output)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if DisplayWidth(line) > 82 {
			t.Fatalf("activity line width=%d exceeds terminal width: %q", DisplayWidth(line), line)
		}
	}
}

func TestActivityCoverageHUDExplainsHistoryRetentionQuotaAndDamage(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewDisposableOwner(
		"ses_coveragehud",
		"lima",
		"incarnation-coverage-hud",
	)
	if err != nil {
		t.Fatal(err)
	}
	available := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_coveragecurrent",
		Owner: owner, SessionID: owner.SessionID,
		Subsystem: workloadtypes.SubsystemFile,
		State:     workloadtypes.CoverageAvailable, Reason: "collector-recovered",
		CollectorGeneration: 2, StartSequence: 10, StartedAt: now,
	}
	endedAt := now.Add(-30 * time.Minute)
	endSequence := uint64(9)
	degraded := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_coveragehistory",
		Owner: owner, SessionID: owner.SessionID,
		Subsystem: workloadtypes.SubsystemFile,
		State:     workloadtypes.CoveragePartial, Reason: "ring-overflow",
		CollectorGeneration: 1, DroppedEventCount: 4,
		StartSequence: 1, EndSequence: &endSequence,
		StartedAt: now.Add(-time.Hour), EndedAt: &endedAt,
	}
	summary := manager.ActivitySummaryResult{
		Owner: owner, Counts: map[string]uint64{},
		CurrentCoverage: []workloadtypes.CoverageInterval{available},
		Pruned:          true, Corrupt: true,
		Reasons: []string{"corrupt-segment", "retention-pruned"},
	}
	summary.RetainedRange.From = now.Add(-time.Hour)
	summary.RetainedRange.To = now
	summary.Quota.UsedBytes = 1024
	summary.Quota.LimitBytes = 4096
	output := Activity(ActivityInput{
		State: liveconsole.State{}, SessionID: owner.SessionID, Tab: ActivityTabAll,
		Data: ActivityData{
			Owner:   owner,
			Summary: summary,
			Events: manager.ActivityEventsPage{
				Records:  []workloadtypes.ActivityRecord{},
				Coverage: []workloadtypes.CoverageInterval{degraded, available},
			},
			Coverage: manager.ActivityCoverageResult{
				Intervals: []workloadtypes.CoverageInterval{degraded, available},
				Current:   []workloadtypes.CoverageInterval{available},
			},
		},
		Loaded: true, Now: now,
	}, Options{Width: 150, Height: 28, NoColor: true})
	for _, expected := range []string{
		"history 2 intervals",
		"1 reduced",
		"4 events known lost",
		"stored evidence",
		"1.0 KiB of 4.0 KiB",
		"pruned",
		"corrupt",
		"corrupt-segment",
		"retention-pruned",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("coverage HUD missing %q:\n%s", expected, output)
		}
	}
}

func TestActivityCoverageRendersIndependentSubsystemEvidence(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_coverage_matrix",
		"lima",
		"incarnation-coverage-matrix",
	)
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		subsystem  string
		state      string
		reason     string
		generation uint64
		dropped    uint64
		retention  bool
	}
	fixtures := []fixture{
		{
			subsystem: workloadtypes.SubsystemProcess,
			state:     workloadtypes.CoverageAvailable,
			reason:    "observer-ready", generation: 2,
		},
		{
			subsystem: workloadtypes.SubsystemFile,
			state:     workloadtypes.CoveragePartial,
			reason:    "ring-overflow", generation: 3, dropped: 4,
		},
		{
			subsystem: workloadtypes.SubsystemNetwork,
			state:     workloadtypes.CoveragePartial,
			reason:    "schema-mismatch", generation: 4, dropped: 2,
		},
		{
			subsystem: workloadtypes.SubsystemDNS,
			state:     workloadtypes.CoverageUnavailable,
			reason:    "retention-pruned", generation: 5, retention: true,
		},
	}
	intervals := make([]workloadtypes.CoverageInterval, 0, len(fixtures))
	records := make([]workloadtypes.ActivityRecord, 0, len(fixtures))
	for index, current := range fixtures {
		sequence := uint64(index + 11)
		interval := workloadtypes.CoverageInterval{
			Schema: workloadtypes.CoverageIntervalSchema,
			ID:     fmt.Sprintf("cov_coverage_matrix_%d", index),
			Owner:  owner, SessionID: "ses_coverage_matrix",
			Subsystem: current.subsystem, State: current.state,
			Reason:              current.reason,
			CollectorGeneration: current.generation,
			DroppedEventCount:   current.dropped,
			RetentionGap:        current.retention,
			StartSequence:       sequence,
			StartedAt:           now.Add(time.Duration(index) * time.Second),
		}
		if err := interval.Validate(); err != nil {
			t.Fatalf("coverage fixture %s: %v", current.subsystem, err)
		}
		intervals = append(intervals, interval)
		records = append(records, workloadtypes.ActivityRecord{
			Schema: workloadtypes.ActivityRecordSchema,
			ID:     fmt.Sprintf("act_coverage_matrix_%d", index),
			Owner:  owner, SessionID: "ses_coverage_matrix",
			Kind:      current.subsystem,
			Operation: "observe",
			Subject: workloadtypes.GenericSubject{
				Kind: current.subsystem, Code: "coverage-fixture",
			},
			Outcome: workloadtypes.Outcome{
				Status: workloadtypes.OutcomeSucceeded,
			},
			Count:           1,
			FirstAt:         interval.StartedAt,
			LastAt:          interval.StartedAt,
			FirstSequence:   sequence,
			LastSequence:    sequence,
			Attribution:     workloadtypes.AttributionExact,
			CoverageID:      interval.ID,
			RedactionStatus: workloadtypes.RedactionPassed,
		})
	}
	input := ActivityInput{
		State: liveconsole.State{
			StreamHealth: liveconsole.StreamHealth{
				State: liveconsole.HealthLive,
			},
		},
		SessionID: "ses_coverage_matrix",
		Tab:       ActivityTabAll,
		Data: ActivityData{
			Owner: owner,
			Events: manager.ActivityEventsPage{
				Records: records, Coverage: intervals,
			},
			Coverage: manager.ActivityCoverageResult{
				Intervals: intervals, Current: intervals,
			},
		},
		Loaded: true, DetailOpen: true, Now: now.Add(time.Minute),
	}
	overview := Activity(input, Options{
		Width: 160, Height: 32, NoColor: true,
	})
	for _, expected := range []string{
		"process Available",
		"file Partial (ring-overflow)",
		"network Partial (schema-mismatch)",
		"DNS Unavailable (retention-pruned) retention gap",
	} {
		if !strings.Contains(overview, expected) {
			t.Fatalf("coverage HUD missing %q:\n%s", expected, overview)
		}
	}
	rows := ActivityRows(input)
	if len(rows) != len(fixtures) {
		t.Fatalf("coverage matrix rows=%d want=%d", len(rows), len(fixtures))
	}
	for _, current := range fixtures {
		selected := -1
		for index, row := range rows {
			record, ok := activityRecordByID(input, row.ID)
			if !ok {
				continue
			}
			interval, ok := activityCoverageByID(input, record.CoverageID)
			if ok && interval.Subsystem == current.subsystem {
				selected = index
				break
			}
		}
		if selected < 0 {
			t.Fatalf("coverage row missing for %s", current.subsystem)
		}
		input.Selected = selected
		output := Activity(input, Options{
			Width: 160, Height: 32, NoColor: true,
		})
		for _, expected := range []string{
			"coverage " + current.subsystem + " " + current.state,
			"reason " + current.reason,
			fmt.Sprintf("collector run %d", current.generation),
			fmt.Sprintf("dropped %d", current.dropped),
			fmt.Sprintf("retention gap %t", current.retention),
		} {
			if !strings.Contains(output, expected) {
				t.Fatalf(
					"coverage %s detail missing %q:\n%s",
					current.subsystem,
					expected,
					output,
				)
			}
		}
	}
}
