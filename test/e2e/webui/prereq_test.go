package webui

import "testing"

func TestPrerequisiteDiscoveryReportsMissingChrome(t *testing.T) {
	t.Setenv("HIDEOUT_CHROME_PATH", "/definitely/missing/chrome")
	p := DiscoverPrerequisites()
	if p.Available() {
		t.Fatal("missing chrome path reported as available")
	}
	found := false
	for _, name := range p.Missing {
		if name == "chrome" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing chrome not reported: %+v", p)
	}
}
