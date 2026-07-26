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
	valid = concurrentIsolationFixture()
	valid.Artifacts.SessionPTYEvidenceSHA256 = ""
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "PTY evidence digest") {
		t.Fatalf("missing PTY evidence digest accepted: %v", err)
	}
	valid = concurrentIsolationFixture()
	valid.Checks["daemonCrashTargetsReaped"] = false
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "daemonCrashTargetsReaped") {
		t.Fatalf("failed daemon-crash check accepted: %v", err)
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
	valid.Methodology.CandidateSampling = "inner-workload-only"
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "sampling boundary") {
		t.Fatalf("misrepresented sampling boundary accepted: %v", err)
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
	evidence.Artifacts.SessionPTYEvidenceSHA256 = strings.Repeat("d", 64)
	return evidence
}

func concurrentPerformanceFixture(expected RuntimeExpectation) concurrentPerformanceEvidence {
	var evidence concurrentPerformanceEvidence
	evidence.Schema = "hideout.concurrent-sessions-performance/v2"
	evidence.Status = "passed"
	evidence.GeneratedAt = "2026-07-16T12:00:00Z"
	evidence.Candidate.Commit = runtimePackageCommitFixture
	evidence.Candidate.EnvironmentID = "env_candidate"
	evidence.Candidate.Instance = "candidate"
	evidence.Host.OS = "darwin"
	evidence.Host.Arch = "arm64"
	evidence.Runtime.Family = expected.Family
	evidence.Runtime.Revision = expected.Revision
	evidence.Runtime.ArtifactSHA256 = expected.ArtifactSHA256
	evidence.Runtime.BuildCommit = expected.BuildCommit
	evidence.Methodology.Samples = 30
	evidence.Methodology.Warmups = 3
	evidence.Methodology.ReadyThresholdMS = 2000
	evidence.Methodology.FixtureSHA256 = strings.Repeat("c", 64)
	evidence.Methodology.CandidateSampling = "per-run-host-invocation-to-first-target-byte"
	evidence.Methodology.MeasurementClock = "host-monotonic-observed-first-byte"
	for range 30 {
		evidence.WarmAttach.SamplesMS = append(evidence.WarmAttach.SamplesMS, 100)
	}
	evidence.WarmAttach.MedianMS = 100
	evidence.WarmAttach.P95MS = 100
	return evidence
}
