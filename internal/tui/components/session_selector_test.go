package components

import (
	"testing"

	"github.com/vibe-agi/hideout/internal/manager"
)

func TestSessionSelectorReplaceClearsPriorSnapshotRows(t *testing.T) {
	var selector SessionSelector
	selector.Replace([]manager.SessionSummary{
		{ID: "ses_alpha"}, {ID: "ses_beta"},
	}, "ses_beta")
	if selector.Selected() != "ses_beta" {
		t.Fatalf("initial selection=%q", selector.Selected())
	}
	selector.Replace([]manager.SessionSummary{{ID: "ses_gamma"}}, "ses_beta")
	rows := selector.Rows()
	if len(rows) != 1 || rows[0].ID != "ses_gamma" || selector.Selected() != "ses_gamma" {
		t.Fatalf("replacement retained old session rows: rows=%+v selected=%q", rows, selector.Selected())
	}
}
