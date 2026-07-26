package productevidence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestProofRegistryCovers036WithStrictRealAndSupportingNotRunEvidence(t *testing.T) {
	want := []string{
		Proof036Gate0Mechanics,
		Proof036Gate0Model,
		Proof036RealLifecycle,
		Proof036RealPerformance,
		Proof036RealGate2NotRun,
		Proof036DocsClaimBoundary,
	}
	requirements := RequirementsForFeature(Feature036)
	if len(requirements) != len(want) || len(Required036ProofIDs) != len(want) {
		t.Fatalf("036 requirements=%d requiredIDs=%d want %d", len(requirements), len(Required036ProofIDs), len(want))
	}
	seen := map[string]ProofRequirement{}
	for _, requirement := range requirements {
		seen[requirement.ProofID] = requirement
	}
	for _, proofID := range want {
		if _, ok := seen[proofID]; !ok {
			t.Fatalf("036 proof %s is not registered", proofID)
		}
	}
	for _, proofID := range []string{Proof036RealLifecycle, Proof036RealPerformance} {
		requirement := seen[proofID]
		if requirement.Layer != LayerRealGate || requirement.RequiredFor != RequiredForReleaseCandidate ||
			requirement.FreshnessPolicy != FreshnessSameCommitAndPackage ||
			requirement.RuntimePolicy != RuntimePolicyExactReal || requirement.ArtifactPolicy == ArtifactPolicyNone ||
			requirement.RequiredEvidenceClass == "" || requirement.ArtifactValidator == "" {
			t.Fatalf("036 real proof %s has weak scope: %+v", proofID, requirement)
		}
	}
	if seen[Proof036RealGate2NotRun].RequiredFor != RequiredForSupportingOnly ||
		seen[Proof036RealGate2NotRun].RuntimePolicy != RuntimePolicyNone {
		t.Fatalf("036 not-run proof could satisfy a real claim: %+v", seen[Proof036RealGate2NotRun])
	}
}

func TestLifecycleLocalAndModelValidatorsRejectIncompleteEvidence(t *testing.T) {
	local := lifecycleLocalFixture()
	data, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLifecycleLocalArtifact(data, runtimePackageCommitFixture); err != nil {
		t.Fatalf("valid local evidence: %v", err)
	}
	delete(local.Checks, "cleanupBeforeRelease")
	data, _ = json.Marshal(local)
	if err := validateLifecycleLocalArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatalf("incomplete local evidence accepted: %v", err)
	}

	model := lifecycleModelFixture()
	data, _ = json.Marshal(model)
	if err := validateLifecycleModelArtifact(data, runtimePackageCommitFixture); err != nil {
		t.Fatalf("valid model evidence: %v", err)
	}
	model.StopsWithLiveClient = 1
	data, _ = json.Marshal(model)
	if err := validateLifecycleModelArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "exploration") {
		t.Fatalf("unsafe model evidence accepted: %v", err)
	}
	if err := validateLifecycleModelArtifact([]byte("all states passed\n"), runtimePackageCommitFixture); err == nil {
		t.Fatal("arbitrary text satisfied the lifecycle model validator")
	}
}

func TestLifecycleRealValidatorRejectsFalseGreenChecksAndBounds(t *testing.T) {
	real := lifecycleRealFixture()
	data, err := json.Marshal(real)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLifecycleRealArtifact(data, runtimePackageCommitFixture); err != nil {
		t.Fatalf("valid real evidence: %v", err)
	}
	real.Checks["stopUnknownBlocksAttach"] = false
	data, _ = json.Marshal(real)
	if err := validateLifecycleRealArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "stopUnknownBlocksAttach") {
		t.Fatalf("failed real check accepted: %v", err)
	}
	real = lifecycleRealFixture()
	real.Metrics.AttachStopRaces = 99
	data, _ = json.Marshal(real)
	if err := validateLifecycleRealArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "metrics") {
		t.Fatalf("short race run accepted: %v", err)
	}
	real = lifecycleRealFixture()
	real.Dirty = true
	data, _ = json.Marshal(real)
	if err := validateLifecycleRealArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("dirty real evidence accepted: %v", err)
	}
	real = lifecycleRealFixture()
	real.Status = "probe-passed"
	data, _ = json.Marshal(real)
	if err := validateLifecycleRealArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("reduced probe evidence accepted as product proof: %v", err)
	}
}

func TestLifecyclePerformanceValidatorRecomputesUserVisibleCommandThreshold(t *testing.T) {
	expected := runtimeExpectationFixture()
	performance := lifecyclePerformanceFixture(expected)
	data, err := json.Marshal(performance)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLifecyclePerformanceArtifact(data, runtimePackageCommitFixture, &expected); err != nil {
		t.Fatalf("valid performance evidence: %v", err)
	}
	performance.CandidateMedianMS = 1
	data, _ = json.Marshal(performance)
	if err := validateLifecyclePerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "not derived") {
		t.Fatalf("forged median accepted: %v", err)
	}
	performance = lifecyclePerformanceFixture(expected)
	for index := range performance.CandidateSamplesMS {
		performance.CandidateSamplesMS[index] = 111
	}
	performance.CandidateMedianMS = 111
	performance.ObservedDeltaMS = 11
	data, _ = json.Marshal(performance)
	if err := validateLifecyclePerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "regression") {
		t.Fatalf("threshold regression accepted: %v", err)
	}
	performance = lifecyclePerformanceFixture(expected)
	performance.Methodology.Command = "internal benchmark"
	data, _ = json.Marshal(performance)
	if err := validateLifecyclePerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "methodology") {
		t.Fatalf("non-user-visible command accepted: %v", err)
	}
}

func lifecycleLocalFixture() lifecycleLocalEvidence {
	evidence := lifecycleLocalEvidence{
		Schema: "hideout.lifecycle-local-evidence/v1", Status: "passed",
		GeneratedAt: "2026-07-17T00:00:00Z", Commit: runtimePackageCommitFixture,
		Checks: map[string]bool{},
	}
	for _, check := range requiredLifecycleLocalChecks {
		evidence.Checks[check] = true
	}
	return evidence
}

func lifecycleModelFixture() lifecycleModelEvidence {
	evidence := lifecycleModelEvidence{
		Schema: "hideout.lifecycle-model-evidence/v1", Status: "passed",
		GeneratedAt: "2026-07-17T00:00:00Z", Commit: runtimePackageCommitFixture,
		ExhaustiveSequences: 180, PersistedReplaySeeds: 32, ConcurrentReplaySeeds: 32,
		ExploredTransitions: 1848, ScenarioCount: 249,
		StepsPerPersistedReplay: 24, ConcurrentWaitBoundMS: 2000,
		JournalValidAfterEachStep: true, CorruptionRejected: true,
		CoveredEvents: lifecycle.RequiredModelEvents(), Invariants: map[string]bool{},
	}
	for _, invariant := range lifecycle.RequiredModelInvariants() {
		evidence.Invariants[invariant] = true
	}
	return evidence
}

func lifecycleRealFixture() lifecycleRealEvidence {
	evidence := lifecycleRealEvidence{
		Schema: "hideout.lifecycle-real-gate2/v1", Status: "passed",
		GeneratedAt: "2026-07-17T00:00:00Z", Commit: runtimePackageCommitFixture,
		Backend: "lima", HostOS: "darwin", HostArch: "arm64", Checks: map[string]bool{},
	}
	evidence.Metrics.AttachStopRaces = 100
	evidence.Metrics.FinalStopMS = 16000
	evidence.Metrics.StatusReadyMS = 100
	evidence.Metrics.ReconciliationRetryMS = 100
	evidence.Metrics.ShutdownMS = 100
	for _, check := range requiredLifecycleRealChecks {
		evidence.Checks[check] = true
	}
	evidence.NonClaims.GuestRootContainment = "not-claimed"
	evidence.NonClaims.HostAppTermination = "not-owned"
	return evidence
}

func lifecyclePerformanceFixture(expected RuntimeExpectation) lifecyclePerformanceEvidence {
	var evidence lifecyclePerformanceEvidence
	evidence.Schema = "hideout.lifecycle-performance/v1"
	evidence.Status = "passed"
	evidence.GeneratedAt = "2026-07-17T00:00:00Z"
	evidence.Candidate.Commit = runtimePackageCommitFixture
	evidence.Baseline.Commit = strings.Repeat("b", 40)
	evidence.Host.OS = "darwin"
	evidence.Host.Arch = "arm64"
	evidence.Runtime.Family = expected.Family
	evidence.Runtime.Revision = expected.Revision
	evidence.Runtime.ArtifactSHA256 = expected.ArtifactSHA256
	evidence.Runtime.BuildCommit = expected.BuildCommit
	evidence.Methodology.Command = "hideout run -- git status --short"
	evidence.Methodology.Samples = 30
	evidence.Methodology.Warmups = 3
	evidence.Methodology.FixtureSHA256 = strings.Repeat("c", 64)
	evidence.Methodology.SampleOrder = "paired-alternating-ab-ba"
	for range 30 {
		evidence.CandidateSamplesMS = append(evidence.CandidateSamplesMS, 100)
		evidence.BaselineSamplesMS = append(evidence.BaselineSamplesMS, 100)
	}
	evidence.CandidateMedianMS = 100
	evidence.BaselineMedianMS = 100
	evidence.AllowedDeltaMS = 10
	return evidence
}
