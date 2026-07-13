package webui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserProofPasses(t *testing.T) {
	if os.Getenv("HIDEOUT_UI_E2E_BROWSER") != "1" {
		t.Skip("set HIDEOUT_UI_E2E_BROWSER=1 to run the real browser proof")
	}
	prereq := DiscoverPrerequisites()
	if err := prereq.Err(); err != nil {
		t.Fatal(err)
	}
	outDir := os.Getenv("HIDEOUT_UI_E2E_OUT")
	if outDir == "" {
		outDir = t.TempDir()
	}
	artifactPrefix := os.Getenv("HIDEOUT_UI_E2E_ARTIFACT_PREFIX")
	if artifactPrefix == "" {
		artifactPrefix = filepath.Base(outDir)
	}
	fixture, err := StartFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	result := runBrowserProof(t, prereq, fixture, outDir)
	assertNoControlPlaneMaterial(t, outDir)
	writeBrowserProofs(t, outDir, artifactPrefix, result)
}
