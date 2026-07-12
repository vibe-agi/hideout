package productevidence

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestQuickstartManifestOnlyScenario(t *testing.T) {
	root := repoRoot(t)
	out := t.TempDir()
	cmd := exec.Command("bash", "scripts/test-ui-e2e.sh", "--manifest-only", "--out", out)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quickstart manifest-only command failed: %v\n%s", err, output)
	}
	manifest, err := ReadFile(filepath.Join(out, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	agg, err := AggregateManifests(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := agg.RequirePassed(Proof021EvidenceSchema); err != nil {
		t.Fatal(err)
	}
}

func TestRequireExecutedRejectsNotRunLane(t *testing.T) {
	root := repoRoot(t)
	out := t.TempDir()
	cmd := exec.Command("bash", "scripts/test-ui-e2e.sh", "--browser", "--require-executed", "--out", out)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HIDEOUT_UI_E2E_DISABLE_BROWSER=1")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("require-executed should fail when browser lane is not-run, err=%v output=%s", err, output)
	}
	manifest, readErr := ReadFile(filepath.Join(out, "product-hardening-evidence.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	foundNotRun := false
	for _, proof := range manifest.Proofs {
		if proof.ProofID == Proof021WebUIBrowserConsole && proof.Status == StatusNotRun {
			foundNotRun = true
		}
	}
	if !foundNotRun {
		t.Fatalf("manifest did not record browser not-run proof: %+v", manifest.Proofs)
	}
}

func TestQuickstartDocumentsCompletionCommand(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "specs", "021-ui-e2e-proof", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	required := []string{
		"scripts/test-ui-e2e.sh --manifest-only --out",
		"scripts/test-ui-e2e.sh --browser --out",
		"scripts/test-ui-e2e.sh --tui --out",
		"scripts/test-ui-e2e.sh --all --require-executed --out",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("quickstart missing %q", want)
		}
	}
}

func TestFirstRunQuickstartDocumentsLocalFastCommand(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "specs", "022-alpha-first-run-e2e", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	required := []string{
		"scripts/test-first-run-e2e.sh --local-fast --out",
		"schemas/product-hardening-evidence.schema.json",
		"scripts/test-first-run-docs-smoke.sh",
		"scripts/test-first-run-e2e.sh \\",
		"--real-backend",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("quickstart missing %q", want)
		}
	}
}

func TestHostFSDecisionQuickstartDocumentsLocalFastCommand(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "specs", "023-hostfs-decision-e2e", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	required := []string{
		"scripts/test-hostfs-decision-e2e.sh --local-fast --out",
		"schemas/product-hardening-evidence.schema.json",
		"scripts/test-hostfs-decision-e2e.sh --real-gate2 --out",
		"--require-real",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("quickstart missing %q", want)
		}
	}
}

func TestDoctorPackageRecoveryQuickstartDocumentsLocalFastCommand(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "specs", "024-doctor-package-recovery-e2e", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	required := []string{
		"scripts/test-doctor-package-recovery-e2e.sh",
		"--local-fast",
		"schemas/product-hardening-evidence.schema.json",
		"scripts/test-gate0.sh",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("quickstart missing %q", want)
		}
	}
}

func TestDocsTruthQuickstartDocumentsSmokeCommand(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "specs", "025-documentation-truth-gate", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	required := []string{
		"scripts/test-doc-truth-smoke.sh --out",
		"schemas/product-hardening-evidence.schema.json",
		"scripts/test-gate0.sh",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("quickstart missing %q", want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate current file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("cannot locate repository root")
		}
		dir = next
	}
}
