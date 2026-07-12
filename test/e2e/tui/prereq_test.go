package tui

import "testing"

func TestPrerequisitesMissingScriptPath(t *testing.T) {
	t.Setenv("HIDEOUT_TUI_SCRIPT_PATH", "")
	t.Setenv("PATH", "")
	prereq := DiscoverPrerequisites()
	if prereq.Available() {
		t.Fatalf("prerequisites should be unavailable without script/go: %+v", prereq)
	}
	if prereq.Reason == "" {
		t.Fatalf("missing prerequisites should include a reason")
	}
}
