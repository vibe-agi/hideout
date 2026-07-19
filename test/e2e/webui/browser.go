package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

type BrowserResult struct {
	PanelsVisible          []string          `json:"panelsVisible"`
	LiveUpdateObserved     bool              `json:"liveUpdateObserved"`
	HiddenPollingDetected  bool              `json:"hiddenPollingDetected"`
	WorkspaceMachineCount  int               `json:"workspaceMachineCount"`
	WorkspaceViewCount     int               `json:"workspaceViewCount"`
	WorkspaceRelationSeen  bool              `json:"workspaceRelationSeen"`
	WorkspaceReleaseSeen   bool              `json:"workspaceReleaseSeen"`
	WorkspacePrivateHidden bool              `json:"workspacePrivateHidden"`
	ActionRoundTrip        BrowserAction     `json:"actionRoundTrip"`
	AuthFailureObserved    bool              `json:"authFailureObserved"`
	Artifacts              map[string]string `json:"artifacts"`
}

type BrowserAction struct {
	Action              string `json:"action"`
	RequestObserved     bool   `json:"requestObserved"`
	PayloadValidated    bool   `json:"payloadValidated"`
	ResponseHandled     bool   `json:"responseHandled"`
	VisibleStateChanged bool   `json:"visibleStateChanged"`
}

func runBrowserProof(t *testing.T, prereq Prerequisites, fixture Fixture, outDir string) BrowserResult {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	resultPath := filepath.Join(outDir, "browser-result.json")
	script := filepath.Join(root, "test", "e2e", "webui", "proof.mjs")
	cmd := exec.CommandContext(ctx, prereq.Node, script,
		"--chrome", prereq.ChromePath,
		"--url", fixture.UIURL,
		"--base-url", fixture.BaseURL,
		"--token", fixture.Token,
		"--notice-id", fixture.NoticeID,
		"--fixture-url", fixture.ControlURL,
		"--fixture-key", fixture.ControlKey,
		"--out", outDir,
	)
	cmd.Env = append(os.Environ(), "HIDEOUT_UI_E2E_HEADLESS="+envDefault("HIDEOUT_UI_E2E_HEADLESS", "1"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser proof failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read browser result: %v", err)
	}
	var result BrowserResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse browser result: %v\n%s", err, data)
	}
	if result.HiddenPollingDetected {
		t.Fatalf("browser proof detected hidden polling: %+v", result)
	}
	if !result.LiveUpdateObserved {
		t.Fatalf("browser proof did not observe live update: %+v", result)
	}
	if result.WorkspaceMachineCount != 1 || result.WorkspaceViewCount != 2 ||
		!result.WorkspaceRelationSeen || !result.WorkspaceReleaseSeen || !result.WorkspacePrivateHidden {
		t.Fatalf("browser workspace-view proof is incomplete: %+v", result)
	}
	if !result.AuthFailureObserved {
		t.Fatalf("browser proof did not observe auth failure: %+v", result)
	}
	if result.ActionRoundTrip.Action != "notice.ack" || !result.ActionRoundTrip.RequestObserved || !result.ActionRoundTrip.PayloadValidated || !result.ActionRoundTrip.ResponseHandled || !result.ActionRoundTrip.VisibleStateChanged {
		t.Fatalf("browser proof did not complete notice ack round trip: %+v", result.ActionRoundTrip)
	}
	return result
}

func writeBrowserProofs(t *testing.T, outDir, artifactPrefix string, result BrowserResult) {
	t.Helper()
	proofs := []productevidence.ProofEntry{
		browserProof(productevidence.Proof021WebUIBrowserConsole, "021.FR-001", "WebUI opens in a real local browser context", resultArtifacts(t, outDir, artifactPrefix, result)),
		browserProof(productevidence.Proof021WebUIBrowserLive, "021.FR-003", "A healthy daemon stream event changes visible browser state without hidden polling", resultArtifacts(t, outDir, artifactPrefix, result)),
		browserProof(productevidence.Proof021WebUIBrowserNoticeAck, "021.FR-004", "Browser notice acknowledgement round trip uses authenticated Manager route and updates visible state", resultArtifacts(t, outDir, artifactPrefix, result)),
		browserProof(productevidence.Proof021WebUIBrowserAuth, "021.FR-005", "Wrong credentials produce a visible browser refusal", resultArtifacts(t, outDir, artifactPrefix, result)),
	}
	path := filepath.Join(outDir, "proofs.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	for _, proof := range proofs {
		proof = proof.Sanitized()
		if err := proof.Validate(); err != nil {
			t.Fatalf("invalid browser proof %s: %v", proof.ProofID, err)
		}
		if err := enc.Encode(proof); err != nil {
			t.Fatal(err)
		}
	}
}

func browserProof(proofID, claimID, description string, artifacts []productevidence.ArtifactRef) productevidence.ProofEntry {
	host := productevidence.CurrentHost()
	return productevidence.ProofEntry{
		ProofID:        proofID,
		FeatureID:      productevidence.Feature021,
		Mode:           "browser-e2e",
		EvidenceClass:  "local-ui-e2e",
		Status:         productevidence.StatusPassed,
		CommandSummary: "scripts/test-ui-e2e.sh --browser --out <evidence-dir>",
		CoveredClaims: []productevidence.CoveredClaim{{
			ClaimID:     claimID,
			Source:      "spec",
			Description: description,
			Scope:       "browser",
		}},
		Prerequisites: []productevidence.PrerequisiteStatus{
			{Name: "node", Status: "available"},
			{Name: "chrome", Status: "available"},
		},
		Artifacts:       artifacts,
		RedactionStatus: productevidence.RedactionPassed,
		Host:            &host,
	}
}

func resultArtifacts(t *testing.T, outDir, artifactPrefix string, result BrowserResult) []productevidence.ArtifactRef {
	t.Helper()
	refs := []productevidence.ArtifactRef{}
	for kind, name := range result.Artifacts {
		path := filepath.Join(outDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing browser artifact %s: %v", path, err)
		}
		refs = append(refs, productevidence.ArtifactRef{
			Kind:            kind,
			Path:            filepath.ToSlash(filepath.Join(artifactPrefix, name)),
			SHA256:          sha256File(t, path),
			RedactionStatus: productevidence.RedactionPassed,
			Description:     "WebUI browser proof " + kind,
		})
	}
	return refs
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func assertNoControlPlaneMaterial(t *testing.T, outDir string) {
	t.Helper()
	err := filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, ".png") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if productevidence.ContainsControlPlaneMaterial(string(data)) {
			return errors.New("unredacted control-plane material in " + path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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
