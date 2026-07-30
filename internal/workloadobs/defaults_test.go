package workloadobs_test

import (
	"testing"
	"time"

	workloadobs "github.com/vibe-agi/hideout/internal/workloadobs"
	"github.com/vibe-agi/hideout/internal/workloadobs/aggregate"
	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	"github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestReleaseFrozenDefaultsRemainWiredAcrossPackages(t *testing.T) {
	if workloadobs.DefaultsVersion != "v1" ||
		workloadobs.DefaultFileEventAggregationWindow != 500*time.Millisecond ||
		aggregate.DefaultWindow != workloadobs.DefaultActivityAggregationWindow ||
		aggregate.DefaultMaxInputs !=
			workloadobs.DefaultActivityAggregationInputs ||
		store.DefaultActiveSegmentBytes !=
			workloadobs.DefaultActivityActiveSegmentBytes ||
		store.DefaultPerOwnerBytes != workloadobs.DefaultActivityPerOwnerBytes ||
		store.DefaultGlobalBytes != workloadobs.DefaultActivityGlobalBytes ||
		workloadtypes.DefaultActivityRetentionMaxBytes !=
			workloadobs.DefaultActivityPerOwnerBytes ||
		workloadtypes.DefaultActivityRetentionMaxAgeSeconds !=
			workloadobs.DefaultActivityRetentionMaxAgeSeconds {
		t.Fatal("workload observation defaults drifted")
	}

	ruleSet := risk.DefaultRuleSet()
	if ruleSet.Version != workloadobs.DefaultRiskRuleSetVersion ||
		len(ruleSet.Rules) != 3 {
		t.Fatalf("risk defaults=%+v", ruleSet)
	}
	expected := map[string]struct {
		severity     string
		preserveEach bool
	}{
		"file.write-outside-workspace": {
			severity: workloadobs.DefaultRiskOutsideWorkspaceSeverity,
		},
		"file.destructive-change": {
			severity:     workloadobs.DefaultRiskDestructiveFileSeverity,
			preserveEach: true,
		},
		"process.root-execution": {
			severity:     workloadobs.DefaultRiskRootExecutionSeverity,
			preserveEach: true,
		},
	}
	for _, rule := range ruleSet.Rules {
		want, ok := expected[rule.ID]
		if !ok ||
			rule.Version != workloadobs.DefaultRiskRuleVersion ||
			rule.Severity != want.severity ||
			rule.PreserveEach != want.preserveEach {
			t.Fatalf("risk rule drifted: %+v", rule)
		}
		delete(expected, rule.ID)
	}
	if len(expected) != 0 {
		t.Fatalf("missing risk defaults=%v", expected)
	}
}
