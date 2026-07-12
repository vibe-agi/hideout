package tui

import (
	"os"
	"path/filepath"
	"testing"

	webui "github.com/vibe-agi/hideout/test/e2e/webui"
)

func TestTUIProofPasses(t *testing.T) {
	if os.Getenv("HIDEOUT_UI_E2E_TUI") != "1" {
		t.Skip("set HIDEOUT_UI_E2E_TUI=1 to run the TUI PTY proof")
	}
	prereq := DiscoverPrerequisites()
	if !prereq.Available() {
		t.Skip(prereq.Reason)
	}
	outDir := os.Getenv("HIDEOUT_UI_E2E_OUT")
	if outDir == "" {
		outDir = t.TempDir()
	}
	artifactPrefix := os.Getenv("HIDEOUT_UI_E2E_ARTIFACT_PREFIX")
	if artifactPrefix == "" {
		artifactPrefix = "tui"
	}
	fixture, err := webui.StartFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	result := runTUIProof(t, prereq, fixture, outDir)
	writeTUIProofs(t, outDir, artifactPrefix, result)
	assertNoControlPlaneMaterial(t, outDir)
}

func TestTUIArtifactRedactionCanary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "terminal-capture.txt"), []byte("HIDEOUT_*=REDACTED token=REDACTED keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tui-result.json"), []byte(`{"summary":"keep-me"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertNoControlPlaneMaterial(t, dir)
}
