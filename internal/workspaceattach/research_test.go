package workspaceattach

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestResearchDecisionValidAccepted(t *testing.T) {
	root, decision := validResearchDecision(t)
	if err := validateResearchSchema(t, decision); err != nil {
		t.Fatalf("schema rejected valid decision: %v", err)
	}
	if err := ValidateResearchDecision(decision, ResearchEvaluationOptions{
		ArtifactRoot: root, ExpectedCommit: decision.Provenance.Commit,
	}); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
}

func TestPhaseRDecisionArtifactAcceptedForResearchAndRejectedForPromotion(t *testing.T) {
	root := filepath.Join("..", "..", "dist", "workspace-research", "035")
	path := filepath.Join(root, "decision.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		// The Phase R research artifact is deliberately retained outside the
		// repository (gitignored dist/workspace-research); a checkout without
		// it has nothing to verify. The 035 claim stays bound to the retained
		// evidence manifest, not to this local re-read.
		t.Skipf("workspace-research decision artifact is not retained in this checkout: %v", err)
	}
	const evidenceCommit = "f35faa81e0e425765a2768d1cc3192f4cda67772"
	decision, err := LoadResearchDecision(path, ResearchEvaluationOptions{
		ArtifactRoot: root, ExpectedCommit: evidenceCommit, AllowDirty: true,
	})
	if err != nil {
		t.Fatalf("Phase R decision rejected: %v", err)
	}
	if decision.Result != ResearchAccepted || decision.SelectedCandidate != CandidatePortal {
		t.Fatalf("Phase R decision = %s/%s", decision.Result, decision.SelectedCandidate)
	}
	if err := validateResearchSchema(t, decision); err != nil {
		t.Fatalf("Phase R decision schema rejected: %v", err)
	}
	if _, err := LoadResearchDecision(path, ResearchEvaluationOptions{
		ArtifactRoot: root, ExpectedCommit: evidenceCommit,
	}); err == nil {
		t.Fatal("dirty Phase R decision authorized promotion")
	}
}

func TestResearchDecisionAllowsDirtyResearchButRejectsDirtyPromotion(t *testing.T) {
	root, decision := validResearchDecision(t)
	decision.Provenance.Dirty = true
	options := ResearchEvaluationOptions{
		ArtifactRoot: root, ExpectedCommit: decision.Provenance.Commit, AllowDirty: true,
	}
	if err := ValidateResearchDecision(decision, options); err != nil {
		t.Fatalf("dirty research decision rejected: %v", err)
	}
	options.AllowDirty = false
	if err := ValidateResearchDecision(decision, options); err == nil {
		t.Fatal("dirty research decision authorized promotion")
	}
}

func TestWriteResearchDecisionValidatesAndReplacesAtomically(t *testing.T) {
	root, decision := validResearchDecision(t)
	path := filepath.Join(root, "nested", "decision.json")
	options := ResearchEvaluationOptions{ArtifactRoot: root, ExpectedCommit: decision.Provenance.Commit}
	if err := WriteResearchDecision(path, decision, options); err != nil {
		t.Fatalf("write decision: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("decision mode = %o, want 600", info.Mode().Perm())
	}
	decision.CandidateResults[1].Summary = "replacement remains fully validated"
	if err := WriteResearchDecision(path, decision, options); err != nil {
		t.Fatalf("replace decision: %v", err)
	}
	loaded, err := LoadResearchDecision(path, options)
	if err != nil {
		t.Fatalf("load decision: %v", err)
	}
	if loaded.CandidateResults[1].Summary != decision.CandidateResults[1].Summary {
		t.Fatal("atomic replacement did not publish the new decision")
	}
	matches, err := filepath.Glob(filepath.Join(root, "nested", ".workspace-research-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary decisions remain: %v", matches)
	}

	invalid := cloneResearchDecision(t, decision)
	invalid.SelectedCandidate = CandidateVZ
	invalidPath := filepath.Join(root, "invalid.json")
	if err := WriteResearchDecision(invalidPath, invalid, options); err == nil {
		t.Fatal("invalid decision was written")
	}
	if _, err := os.Stat(invalidPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid decision artifact exists: %v", err)
	}
	if err := WriteResearchDecision(filepath.Join(root, "..", "escape.json"), decision, options); err == nil {
		t.Fatal("decision path escaped the artifact root")
	}
}

func TestResearchDecisionRejectsFalseGreenVariants(t *testing.T) {
	root, valid := validResearchDecision(t)
	tests := []struct {
		name   string
		mutate func(*ResearchDecision)
	}{
		{"selected candidate did not pass", func(d *ResearchDecision) { d.CandidateResults[1].Status = CandidateFailed }},
		{"both candidates passed", func(d *ResearchDecision) { d.CandidateResults[0].Status = CandidatePassed }},
		{"thresholds failed", func(d *ResearchDecision) { d.Performance.ThresholdsPassed = false }},
		{"too few baseline samples", func(d *ResearchDecision) { d.Performance.Metrics[0].Baseline.Samples = 29 }},
		{"measured threshold breach", func(d *ResearchDecision) {
			d.Performance.Metrics[0].Candidate.MedianMS = 2500
			d.Performance.Metrics[0].Candidate.P95MS = 2600
		}},
		{"missing required metric baseline", func(d *ResearchDecision) { d.Performance.Metrics[0].Baseline = nil }},
		{"unknown metric", func(d *ResearchDecision) { d.Performance.Metrics[0].ID = "unknown" }},
		{"duplicate candidates", func(d *ResearchDecision) { d.CandidateResults[0].Candidate = CandidatePortal }},
		{"failed candidate has no failed check", func(d *ResearchDecision) { d.CandidateResults[0].Checks[0].Result = ResearchCheckPassed }},
		{"missing selected limits", func(d *ResearchDecision) { d.Limits = nil }},
		{"missing required tool", func(d *ResearchDecision) { d.PathIdentity.Tools = d.PathIdentity.Tools[:6] }},
		{"failed required operation", func(d *ResearchDecision) { d.OperationMatrix[0].Result = ResearchCheckFailed }},
		{"dirty promotion", func(d *ResearchDecision) { d.Provenance.Dirty = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := cloneResearchDecision(t, valid)
			tt.mutate(&decision)
			if err := ValidateResearchDecision(decision, ResearchEvaluationOptions{
				ArtifactRoot: root, ExpectedCommit: valid.Provenance.Commit,
			}); err == nil {
				t.Fatal("false-green decision accepted")
			}
		})
	}
}

func TestResearchDecisionRejectsArtifactEscapeMissingAndDigestMismatch(t *testing.T) {
	root, valid := validResearchDecision(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("lexical escape", func(t *testing.T) {
		decision := cloneResearchDecision(t, valid)
		decision.Artifacts[0].Path = "../outside"
		if err := ValidateResearchDecision(decision, ResearchEvaluationOptions{ArtifactRoot: root, ExpectedCommit: valid.Provenance.Commit}); err == nil {
			t.Fatal("artifact escape accepted")
		}
	})

	t.Run("symlink escape", func(t *testing.T) {
		link := filepath.Join(root, "escape")
		if err := os.Symlink(filepath.Dir(outside), link); err != nil {
			t.Fatal(err)
		}
		decision := cloneResearchDecision(t, valid)
		decision.Artifacts[0].Path = filepath.Join("escape", filepath.Base(outside))
		decision.Artifacts[0].SHA256 = digestBytes([]byte("outside"))
		if err := ValidateResearchDecision(decision, ResearchEvaluationOptions{ArtifactRoot: root, ExpectedCommit: valid.Provenance.Commit}); err == nil {
			t.Fatal("symlink artifact escape accepted")
		}
	})

	t.Run("missing", func(t *testing.T) {
		decision := cloneResearchDecision(t, valid)
		decision.Artifacts[0].Path = "missing.json"
		if err := ValidateResearchDecision(decision, ResearchEvaluationOptions{ArtifactRoot: root, ExpectedCommit: valid.Provenance.Commit}); err == nil {
			t.Fatal("missing artifact accepted")
		}
	})

	t.Run("digest mismatch", func(t *testing.T) {
		decision := cloneResearchDecision(t, valid)
		decision.Artifacts[0].SHA256 = digestBytes([]byte("wrong"))
		if err := ValidateResearchDecision(decision, ResearchEvaluationOptions{ArtifactRoot: root, ExpectedCommit: valid.Provenance.Commit}); err == nil {
			t.Fatal("artifact digest mismatch accepted")
		}
	})
}

func TestResearchDecisionRejectsStaleCommitAndUnknownJSON(t *testing.T) {
	root, valid := validResearchDecision(t)
	if err := ValidateResearchDecision(valid, ResearchEvaluationOptions{
		ArtifactRoot: root, ExpectedCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}); err == nil {
		t.Fatal("stale commit accepted")
	}

	path := filepath.Join(root, "decision.json")
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"feature":"035"`), []byte(`"feature":"035","unknown":true`), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResearchDecision(path, ResearchEvaluationOptions{
		ArtifactRoot: root, ExpectedCommit: valid.Provenance.Commit,
	}); err == nil {
		t.Fatal("unknown JSON field accepted")
	}
}

func validResearchDecision(t *testing.T) (string, ResearchDecision) {
	t.Helper()
	root := t.TempDir()
	artifactData := []byte("evidence\n")
	if err := os.WriteFile(filepath.Join(root, "evidence.txt"), artifactData, 0o600); err != nil {
		t.Fatal(err)
	}
	tools := make([]ResearchToolObservation, 0, 7)
	for _, name := range []string{"bash", "git", "node", "python", "go", "claude", "codex"} {
		tools = append(tools, ResearchToolObservation{Name: name, Version: "fixture-1", Result: CandidatePassed, EvidenceRef: "evidence.txt"})
	}
	passedChecks := []ResearchCheck{{ID: "complete", Result: ResearchCheckPassed, EvidenceRefs: []string{"evidence.txt"}}}
	failedChecks := []ResearchCheck{{ID: "control-path", Result: ResearchCheckFailed, EvidenceRefs: []string{"evidence.txt"}}}
	return root, ResearchDecision{
		Schema: ResearchSchema, Feature: ResearchFeature, Result: ResearchAccepted,
		SelectedCandidate: CandidatePortal,
		CandidateResults: []ResearchCandidateResult{
			{Candidate: CandidateVZ, Status: CandidateFailed, Checks: failedChecks, Summary: "supported control path unavailable", FailureCodes: []string{"control-path-unavailable"}},
			{Candidate: CandidatePortal, Status: CandidatePassed, Checks: passedChecks, Summary: "all mandatory checks passed"},
		},
		PathIdentity: ResearchPathIdentity{
			LogicalRoot: "/workspace", PhysicalRootPattern: "/hideout/workspaces/<workspaceId>",
			Mechanism: "session-private logical symlink to opaque physical mount", Tools: tools,
		},
		OperationMatrix: []ResearchOperation{{Operation: "open", Support: "required", Result: ResearchCheckPassed, EvidenceRef: "evidence.txt"}},
		Limits:          &ResearchLimits{ViewsPerEnvironment: 16, HandlesPerSession: 4096, InFlightPerSession: 256, QueuedBytesPerSession: 8 << 20, FrameBytes: 1 << 20, DirectoryEntries: 65536},
		Performance:     validResearchPerformance(),
		Topology:        ResearchTopology{HostProcesses: []string{"hideoutd"}, GuestProcesses: []string{"hideout-workspacefs"}, ControlPaths: []string{"private authenticated session channel"}, DataPaths: []string{"binary multiplexed portal"}},
		Provenance: ResearchProvenance{
			Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", HostArch: "arm64", MacOSVersion: "fixture", LimaVersion: "fixture", RuntimeDigest: digestBytes([]byte("runtime")), FixtureDigest: digestBytes([]byte("fixture")), ToolVersions: map[string]string{"go": runtime.Version()},
		},
		Artifacts:  []ResearchArtifact{{Path: "evidence.txt", SHA256: digestBytes(artifactData)}},
		DecisionAt: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
}

func validResearchPerformance() ResearchPerformance {
	result := func(median, p95 float64) ResearchPerformanceResult {
		return ResearchPerformanceResult{Samples: 30, MedianMS: median, P95MS: p95}
	}
	baseline := func(median, p95 float64) *ResearchPerformanceResult {
		value := result(median, p95)
		return &value
	}
	return ResearchPerformance{
		Metrics: []ResearchPerformanceMetric{
			{ID: PerformanceGitStatus, Baseline: baseline(100, 120), Candidate: result(150, 180)},
			{ID: PerformancePackageScan, Baseline: baseline(200, 240), Candidate: result(450, 520)},
			{ID: PerformanceAtomicHostToGuest, Candidate: result(20, 40)},
			{ID: PerformanceAtomicGuestToHost, Candidate: result(20, 40)},
			{ID: PerformanceMountReady, Candidate: result(100, 200)},
			{ID: PerformanceFirstByte, Baseline: baseline(700, 800), Candidate: result(850, 1000)},
		},
		ThresholdsPassed: true,
		RawSamplesRef:    "evidence.txt",
	}
}

func cloneResearchDecision(t *testing.T, value ResearchDecision) ResearchDecision {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out ResearchDecision
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateResearchSchema(t *testing.T, value ResearchDecision) error {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-research-decision.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return schema.Validate(instance)
}
