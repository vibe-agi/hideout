package productevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcurrentIsolationSemanticValidatorRejectsArbitraryOrIncompleteEvidence(t *testing.T) {
	valid := concurrentIsolationFixture()
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err != nil {
		t.Fatalf("valid isolation evidence: %v", err)
	}
	delete(valid.Checks, "siblingPidHidden")
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatalf("incomplete isolation evidence accepted: %v", err)
	}
	if err := validateConcurrentIsolationArtifact([]byte("all checks passed\n"), runtimePackageCommitFixture); err == nil {
		t.Fatal("arbitrary text satisfied the isolation validator")
	}
	valid = concurrentIsolationFixture()
	valid.Metrics.OwnerReconcileMS = 1000.001
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "within one second") {
		t.Fatalf("slow owner reconciliation accepted: %v", err)
	}
}

func TestConcurrentPerformanceSemanticValidatorRecomputesStatisticsAndIdentity(t *testing.T) {
	expected := runtimeExpectationFixture()
	valid := concurrentPerformanceFixture(expected)
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err != nil {
		t.Fatalf("valid performance evidence: %v", err)
	}

	valid.WarmAttach.P95MS = 1
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "not derived") {
		t.Fatalf("tampered statistics accepted: %v", err)
	}
	valid = concurrentPerformanceFixture(expected)
	valid.Baseline.Commit = valid.Candidate.Commit
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("self-comparison baseline accepted: %v", err)
	}
}

func TestEvaluateConcurrentIsolationUsesRegisteredSemanticValidator(t *testing.T) {
	var requirement ProofRequirement
	for _, candidate := range RequirementsForFeature(Feature034) {
		if candidate.ProofID == Proof034RealIsolation {
			requirement = candidate
			break
		}
	}
	if requirement.ProofID == "" {
		t.Fatal("registered isolation requirement missing")
	}
	root := t.TempDir()
	data := []byte("passed according to a shell script\n")
	path := filepath.Join(root, "result.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	expected := runtimeExpectationFixture()
	proof := runtimeProofFixture(requirement, runtimeBindingFixture())
	proof.EvidenceClass = requirement.RequiredEvidenceClass
	proof.Artifacts = []ArtifactRef{{Kind: "manifest", Path: "result.json", SHA256: hex.EncodeToString(sum[:]), RedactionStatus: RedactionPassed}}
	opts := runtimeEvaluationOptions([]ProofRequirement{requirement}, expected)
	opts.ArtifactRoot = root
	report, err := EvaluateManifest(runtimeManifestWithProofs(expected, proof), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0]; got.Status != EvalArtifactInvalid {
		t.Fatalf("arbitrary artifact status=%+v", got)
	}
}

func concurrentIsolationFixture() concurrentIsolationEvidence {
	evidence := concurrentIsolationEvidence{
		Schema: "hideout.concurrent-sessions-gate2/v1", Status: "passed",
		GeneratedAt: "2026-07-16T12:00:00Z", Commit: runtimePackageCommitFixture,
		Backend: "lima", Host: "macos-arm64", Checks: map[string]bool{},
	}
	for _, check := range requiredConcurrentIsolationChecks {
		evidence.Checks[check] = true
	}
	evidence.NonClaims.GuestRootContainment = "not-claimed"
	evidence.Metrics.OwnerReconcileMS = 125
	return evidence
}

func concurrentPerformanceFixture(expected RuntimeExpectation) concurrentPerformanceEvidence {
	var evidence concurrentPerformanceEvidence
	evidence.Schema = "hideout.concurrent-sessions-performance/v1"
	evidence.Status = "passed"
	evidence.GeneratedAt = "2026-07-16T12:00:00Z"
	evidence.Candidate.Commit = runtimePackageCommitFixture
	evidence.Candidate.EnvironmentID = "env_candidate"
	evidence.Candidate.Instance = "candidate"
	evidence.Baseline.Commit = strings.Repeat("b", 40)
	evidence.Baseline.EnvironmentID = "env_baseline"
	evidence.Baseline.Instance = "baseline"
	evidence.Host.OS = "darwin"
	evidence.Host.Arch = "arm64"
	evidence.Runtime.Family = expected.Family
	evidence.Runtime.Revision = expected.Revision
	evidence.Runtime.ArtifactSHA256 = expected.ArtifactSHA256
	evidence.Runtime.BuildCommit = expected.BuildCommit
	evidence.Methodology.Samples = 30
	evidence.Methodology.Warmups = 3
	evidence.Methodology.ReadyThresholdMS = 2000
	evidence.Methodology.FixtureRatioThreshold = 1.25
	evidence.Methodology.FixtureSHA256 = strings.Repeat("c", 64)
	for range 30 {
		evidence.WarmAttach.SamplesMS = append(evidence.WarmAttach.SamplesMS, 100)
		evidence.WorkspaceFixture.CandidateSamplesMS = append(evidence.WorkspaceFixture.CandidateSamplesMS, 10)
		evidence.WorkspaceFixture.BaselineSamplesMS = append(evidence.WorkspaceFixture.BaselineSamplesMS, 8)
	}
	evidence.WarmAttach.MedianMS = 100
	evidence.WarmAttach.P95MS = 100
	evidence.WorkspaceFixture.CandidateMedianMS = 10
	evidence.WorkspaceFixture.CandidateP95MS = 10
	evidence.WorkspaceFixture.BaselineMedianMS = 8
	evidence.WorkspaceFixture.BaselineP95MS = 8
	evidence.WorkspaceFixture.P95Ratio = 1.25
	return evidence
}
