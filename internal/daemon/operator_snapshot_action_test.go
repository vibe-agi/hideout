package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestOperatorSnapshotMaintainsDecisionBeforeTakingSequenceFence(
	t *testing.T,
) {
	d := startTestDaemon(t)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	d.api.Core.DecisionNow = func() time.Time { return now }
	if _, err := d.api.Core.CreateDecision(decision.Decision{
		ID: "decision-expire-before-snapshot", Kind: decision.KindEvidenceShare,
		Source:         decision.Source{Profile: "default"},
		Preview:        decision.Preview{Summary: "Review expiring evidence share"},
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		DefaultOutcome: decision.DefaultOutcomeNoRelease,
		TimeoutAt:      now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)

	type result struct {
		snapshot manager.OperatorSnapshot
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		snapshot, err := d.operatorSnapshot(
			context.Background(),
			manager.OperatorSnapshotQuery{},
		)
		completed <- result{snapshot: snapshot, err: err}
	}()

	select {
	case outcome := <-completed:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if len(outcome.snapshot.Decisions) != 0 {
			t.Fatalf(
				"timed-out decision remained actionable: %+v",
				outcome.snapshot.Decisions,
			)
		}
		if outcome.snapshot.Sequence < 2 {
			t.Fatalf(
				"snapshot sequence %d did not include create and timeout events",
				outcome.snapshot.Sequence,
			)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("operator snapshot deadlocked while decision timeout emitted an event")
	}
}
