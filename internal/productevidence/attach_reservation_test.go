package productevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProofRegistryCovers040WithoutLettingNotRunSatisfyRealClaims(t *testing.T) {
	want := []string{
		Proof040Gate0Mechanics,
		Proof040Gate0Model,
		Proof040RealLifecycle,
		Proof040RealPerformance,
		Proof040RealGate2NotRun,
		Proof040DocsClaimBoundary,
	}
	requirements := RequirementsForFeature(Feature040)
	if len(requirements) != len(want) || len(Required040ProofIDs) != len(want) {
		t.Fatalf("040 requirements=%d requiredIDs=%d want %d", len(requirements), len(Required040ProofIDs), len(want))
	}
	seen := map[string]ProofRequirement{}
	for _, requirement := range requirements {
		seen[requirement.ProofID] = requirement
	}
	for _, proofID := range want {
		if _, ok := seen[proofID]; !ok {
			t.Fatalf("040 proof %s is not registered", proofID)
		}
	}
	for _, proofID := range []string{Proof040RealLifecycle, Proof040RealPerformance} {
		requirement := seen[proofID]
		if requirement.Layer != LayerRealGate || requirement.RequiredFor != RequiredForReleaseCandidate ||
			requirement.FreshnessPolicy != FreshnessSameCommitAndPackage ||
			requirement.RuntimePolicy != RuntimePolicyExactReal || requirement.ArtifactPolicy == ArtifactPolicyNone ||
			requirement.RequiredEvidenceClass == "" {
			t.Fatalf("040 real proof %s has weak scope: %+v", proofID, requirement)
		}
	}
	if seen[Proof040RealPerformance].ArtifactValidator != ArtifactValidatorAttachReservationPerformanceV1 {
		t.Fatalf("040 performance lacks semantic validation: %+v", seen[Proof040RealPerformance])
	}
	if seen[Proof040RealGate2NotRun].RequiredFor != RequiredForSupportingOnly ||
		seen[Proof040RealGate2NotRun].RuntimePolicy != RuntimePolicyNone {
		t.Fatalf("040 not-run proof could satisfy a real claim: %+v", seen[Proof040RealGate2NotRun])
	}
}

func TestAttachReservationPerformanceValidatorOwnsCurrentAbsoluteContract(t *testing.T) {
	evidence := attachReservationPerformanceFixture()
	expected := runtimeExpectationFixture()
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAttachReservationPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err != nil {
		t.Fatalf("valid 040 performance evidence: %v", err)
	}

	evidence.CandidateP95MS = 1
	data, _ = json.Marshal(evidence)
	if err := validateAttachReservationPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil ||
		!strings.Contains(err.Error(), "not derived") {
		t.Fatalf("edited p95 accepted: %v", err)
	}

	evidence = attachReservationPerformanceFixture()
	evidence.CandidateSamplesMS[28] = 2001
	evidence.CandidateSamplesMS[29] = 2001
	evidence.CandidateP95MS = 2001
	evidence.CandidateWithinTwoSeconds = 28
	data, _ = json.Marshal(evidence)
	if err := validateAttachReservationPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil ||
		!strings.Contains(err.Error(), "two-second") {
		t.Fatalf("slow warm target accepted: %v", err)
	}

	evidence = attachReservationPerformanceFixture()
	evidence.Methodology.WorkspaceIsolation = "shared-physical-fixture"
	data, _ = json.Marshal(evidence)
	if err := validateAttachReservationPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil ||
		!strings.Contains(err.Error(), "methodology") {
		t.Fatalf("shared workspace accepted: %v", err)
	}

	evidence = attachReservationPerformanceFixture()
	evidence.Baseline.Commit = strings.Repeat("b", 40)
	data, _ = json.Marshal(evidence)
	if err := validateAttachReservationPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil ||
		!strings.Contains(err.Error(), "baseline identity") {
		t.Fatalf("foreign baseline accepted: %v", err)
	}
}

func attachReservationPerformanceFixture() attachReservationPerformanceEvidence {
	var evidence attachReservationPerformanceEvidence
	evidence.Schema = "hideout.attach-reservation-performance/v1"
	evidence.Status = "passed"
	evidence.GeneratedAt = "2026-07-26T00:00:00Z"
	evidence.Candidate.Commit = runtimePackageCommitFixture
	evidence.Baseline.Commit = attachReservationBaselineCommit
	evidence.Host.OS = "darwin"
	evidence.Host.Arch = "arm64"
	expected := runtimeExpectationFixture()
	evidence.Runtime.Family = expected.Family
	evidence.Runtime.Revision = expected.Revision
	evidence.Runtime.ArtifactSHA256 = expected.ArtifactSHA256
	evidence.Runtime.BuildCommit = expected.BuildCommit
	evidence.Methodology.Command = "hideout run -- git status --short"
	evidence.Methodology.Samples = 30
	evidence.Methodology.Warmups = 3
	evidence.Methodology.FixtureSHA256 = strings.Repeat("f", 64)
	evidence.Methodology.SampleOrder = "paired-alternating-ab-ba"
	evidence.Methodology.WorkspaceIsolation = "separate-physical-fixtures"
	evidence.CandidateSamplesMS = make([]float64, 30)
	evidence.BaselineSamplesMS = make([]float64, 30)
	for index := range evidence.CandidateSamplesMS {
		evidence.CandidateSamplesMS[index] = 500
		evidence.BaselineSamplesMS[index] = 200
	}
	evidence.CandidateMedianMS = 500
	evidence.CandidateP95MS = 500
	evidence.CandidateWithinTwoSeconds = 30
	evidence.CandidateSampleCount = 30
	evidence.BaselineMedianMS = 200
	evidence.ObservedDeltaMS = 300
	evidence.AllowedDeltaMS = 10
	return evidence
}
