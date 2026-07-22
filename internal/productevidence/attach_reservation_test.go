package productevidence

import "testing"

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
			requirement.RuntimePolicy != RuntimePolicyExactReal || requirement.ArtifactPolicy == ArtifactPolicyNone ||
			requirement.RequiredEvidenceClass == "" {
			t.Fatalf("040 real proof %s has weak scope: %+v", proofID, requirement)
		}
	}
	if seen[Proof040RealGate2NotRun].RequiredFor != RequiredForSupportingOnly ||
		seen[Proof040RealGate2NotRun].RuntimePolicy != RuntimePolicyNone {
		t.Fatalf("040 not-run proof could satisfy a real claim: %+v", seen[Proof040RealGate2NotRun])
	}
}
