package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

type BrowserResult struct {
	PanelsVisible             []string           `json:"panelsVisible"`
	LiveUpdateObserved        bool               `json:"liveUpdateObserved"`
	HiddenPollingDetected     bool               `json:"hiddenPollingDetected"`
	Activity                  BrowserActivity    `json:"activity"`
	ActionRoundTrip           BrowserAction      `json:"actionRoundTrip"`
	AuthFailureObserved       bool               `json:"authFailureObserved"`
	CredentialRefreshObserved bool               `json:"credentialRefreshObserved"`
	StaleReadOnlyObserved     bool               `json:"staleReadOnlyObserved"`
	KeyboardNavigation        bool               `json:"keyboardNavigation"`
	ResponsiveLayout          bool               `json:"responsiveLayout"`
	AccessibilityViolations   []string           `json:"accessibilityViolations"`
	DOMNodeCount              int                `json:"domNodeCount"`
	MaxMountedRows            int                `json:"maxMountedRows"`
	Performance               BrowserPerformance `json:"performance"`
	Artifacts                 map[string]string  `json:"artifacts"`
}

type BrowserActivity struct {
	SessionID        string   `json:"sessionId"`
	RecordCount      int      `json:"recordCount"`
	FactsMatched     bool     `json:"factsMatched"`
	ExecutionTree    bool     `json:"executionTree"`
	GuestIdentity    bool     `json:"guestIdentity"`
	Correlation      bool     `json:"correlation"`
	BoundedDOM       bool     `json:"boundedDOM"`
	FiltersExercised []string `json:"filtersExercised"`
}

type BrowserAction struct {
	Action              string `json:"action"`
	RequestObserved     bool   `json:"requestObserved"`
	PayloadValidated    bool   `json:"payloadValidated"`
	ResponseHandled     bool   `json:"responseHandled"`
	VisibleStateChanged bool   `json:"visibleStateChanged"`
	OperationID         string `json:"operationId"`
	RevisionAdvanced    bool   `json:"revisionAdvanced"`
	TerminalPhase       string `json:"terminalPhase"`
}

type BrowserPerformance struct {
	LoadToLiveMS    float64 `json:"loadToLiveMs"`
	MaxFilterMS     float64 `json:"maxFilterMs"`
	LiveUpdateMS    float64 `json:"liveUpdateMs"`
	ConfigurationMS float64 `json:"configurationMs"`
}

func runBrowserProof(t *testing.T, prereq Prerequisites, fixture Fixture, outDir string) BrowserResult {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resultPath := filepath.Join(outDir, "browser-result.json")
	script := filepath.Join(root, "test", "e2e", "webui", "proof.mjs")
	cmd := exec.CommandContext(ctx, prereq.Node, script,
		"--chrome", prereq.ChromePath,
		"--url", fixture.UIURL,
		"--base-url", fixture.BaseURL,
		"--token", fixture.Token,
		"--fixture-url", fixture.ControlURL,
		"--fixture-key", fixture.ControlKey,
		"--session-id", fixture.BrowserEvidence.SessionID,
		"--execution-id", fixture.BrowserEvidence.ExecutionID,
		"--file-path", fixture.BrowserEvidence.FilePath,
		"--domain", fixture.BrowserEvidence.Domain,
		"--ip", fixture.BrowserEvidence.IP,
		"--risk-id", fixture.BrowserEvidence.RiskID,
		"--from", fixture.BrowserEvidence.From,
		"--to", fixture.BrowserEvidence.To,
		"--record-count", fmt.Sprintf(
			"%d",
			fixture.BrowserEvidence.RecordCount,
		),
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
	if len(result.PanelsVisible) != 11 ||
		result.Activity.SessionID != fixture.BrowserEvidence.SessionID ||
		result.Activity.RecordCount != fixture.BrowserEvidence.RecordCount ||
		!result.Activity.FactsMatched ||
		!result.Activity.ExecutionTree ||
		!result.Activity.GuestIdentity ||
		!result.Activity.Correlation ||
		!result.Activity.BoundedDOM ||
		len(result.Activity.FiltersExercised) != 7 {
		t.Fatalf("browser activity/parity proof is incomplete: %+v", result)
	}
	if !result.AuthFailureObserved ||
		!result.CredentialRefreshObserved ||
		!result.StaleReadOnlyObserved ||
		!result.KeyboardNavigation ||
		!result.ResponsiveLayout ||
		len(result.AccessibilityViolations) != 0 {
		t.Fatalf("browser safety/accessibility proof is incomplete: %+v", result)
	}
	if result.ActionRoundTrip.Action != "profile.transaction.apply" ||
		!result.ActionRoundTrip.RequestObserved ||
		!result.ActionRoundTrip.PayloadValidated ||
		!result.ActionRoundTrip.ResponseHandled ||
		!result.ActionRoundTrip.VisibleStateChanged ||
		!result.ActionRoundTrip.RevisionAdvanced ||
		result.ActionRoundTrip.OperationID == "" ||
		result.ActionRoundTrip.TerminalPhase != "succeeded" {
		t.Fatalf(
			"browser proof did not complete configuration transaction: %+v",
			result.ActionRoundTrip,
		)
	}
	if result.DOMNodeCount <= 0 || result.DOMNodeCount > 15000 ||
		result.MaxMountedRows <= 0 || result.MaxMountedRows > 200 ||
		result.Performance.LoadToLiveMS <= 0 ||
		result.Performance.LoadToLiveMS > 5000 ||
		result.Performance.MaxFilterMS > 2000 ||
		result.Performance.LiveUpdateMS > 3000 ||
		result.Performance.ConfigurationMS > 5000 {
		t.Fatalf("browser performance bound failed: %+v", result)
	}
	return result
}

func writeBrowserProofs(t *testing.T, outDir, artifactPrefix string, result BrowserResult) {
	t.Helper()
	proofs := []productevidence.ProofEntry{
		browserProof(productevidence.Proof021WebUIBrowserConsole, "021.FR-001", "WebUI opens in a real local browser context", resultArtifacts(t, outDir, artifactPrefix, result)),
		browserProof(productevidence.Proof021WebUIBrowserLive, "021.FR-003", "A healthy daemon stream event changes visible browser state without hidden polling", resultArtifacts(t, outDir, artifactPrefix, result)),
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
	roles := make([]string, 0, len(result.Artifacts))
	for role := range result.Artifacts {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		name := result.Artifacts[role]
		path := filepath.Join(outDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing browser artifact %s: %v", path, err)
		}
		refs = append(refs, productevidence.ArtifactRef{
			Kind:            browserArtifactKind(t, role),
			Path:            filepath.ToSlash(filepath.Join(artifactPrefix, name)),
			SHA256:          sha256File(t, path),
			RedactionStatus: productevidence.RedactionPassed,
			Description:     "WebUI browser proof " + role,
		})
	}
	return refs
}

func browserArtifactKind(t *testing.T, role string) string {
	t.Helper()
	switch role {
	case "screenshot", "stale-screenshot", "mobile-screenshot":
		return "screenshot"
	case "accessibility":
		return "docs-report"
	case "performance":
		return "log"
	case "event-summary", "log":
		return role
	default:
		t.Fatalf("unsupported browser artifact role %q", role)
		return ""
	}
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
