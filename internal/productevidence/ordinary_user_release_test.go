package productevidence

import (
	"strings"
	"testing"
)

func TestOrdinaryUserReleaseRegistryCoversTargetedRealAndPublicProofs(t *testing.T) {
	requirements := RequirementsForFeature(Feature044)
	want := []string{
		Proof044Gate0Journeys,
		Proof044RealGate2FirstRun,
		Proof044RealGate3Privacy,
		Proof044PackageUI,
		Proof044ReleaseCandidate,
		Proof044DocsTruth,
		Proof044PublicReceipt,
	}
	if len(requirements) != len(want) {
		t.Fatalf("requirements=%d want=%d: %+v", len(requirements), len(want), requirements)
	}
	seen := map[string]ProofRequirement{}
	for _, requirement := range requirements {
		seen[requirement.ProofID] = requirement
	}
	for _, proofID := range want {
		if _, ok := seen[proofID]; !ok {
			t.Fatalf("proof %s is not registered", proofID)
		}
	}
	for _, proofID := range []string{Proof044RealGate2FirstRun, Proof044RealGate3Privacy} {
		requirement := seen[proofID]
		if requirement.Layer != LayerRealGate ||
			requirement.RequiredFor != RequiredForReleaseCandidate ||
			requirement.FreshnessPolicy != FreshnessSameCommitAndPackage ||
			requirement.RuntimePolicy != RuntimePolicyExactReal ||
			requirement.ArtifactPolicy == ArtifactPolicyNone {
			t.Fatalf("real proof %s is not exact-package release evidence: %+v", proofID, requirement)
		}
	}
	if got := seen[Proof044PublicReceipt].RequiredFor; got != RequiredForPublicRelease {
		t.Fatalf("public receipt requiredFor=%s", got)
	}
}

func TestRequire044TargetedComplete(t *testing.T) {
	targeted := requirementsByTarget(t, Feature044, RequiredForTargetedCompletion)
	targetedManifest := aggregateTestManifest(t, proofIDs(targeted)...)
	if err := Require044TargetedComplete(targetedManifest); err != nil {
		t.Fatal(err)
	}
}

func TestRequire044ReleaseCompleteRejectsMissingRealPrivacyProof(t *testing.T) {
	requirements := requirementsByTarget(t, Feature044, RequiredForReleaseCandidate)
	ids := proofIDs(requirements)
	filtered := ids[:0]
	for _, proofID := range ids {
		if proofID != Proof044RealGate3Privacy {
			filtered = append(filtered, proofID)
		}
	}
	manifest := aggregateTestManifest(t, filtered...)
	err := Require044ReleaseComplete(manifest, EvaluationOptions{})
	if err == nil || !strings.Contains(err.Error(), Proof044RealGate3Privacy) {
		t.Fatalf("missing privacy proof error=%v", err)
	}
}

func TestRequire044ReleaseCompleteRejectsMissingUIProof(t *testing.T) {
	ids := proofIDs(RequirementsForFeature(Feature044))
	filtered := ids[:0]
	for _, proofID := range ids {
		if proofID != Proof044PackageUI {
			filtered = append(filtered, proofID)
		}
	}
	manifest := aggregateTestManifest(t, filtered...)
	err := Require044ReleaseComplete(manifest, EvaluationOptions{})
	if err == nil || !strings.Contains(err.Error(), Proof044PackageUI) {
		t.Fatalf("missing UI proof error=%v", err)
	}
}

func TestRequire044PublicCompleteRejectsMissingReceipt(t *testing.T) {
	ids := proofIDs(RequirementsForFeature(Feature044))
	filtered := ids[:0]
	for _, proofID := range ids {
		if proofID != Proof044PublicReceipt {
			filtered = append(filtered, proofID)
		}
	}
	manifest := aggregateTestManifest(t, filtered...)
	err := Require044PublicComplete(manifest, EvaluationOptions{})
	if err == nil || !strings.Contains(err.Error(), Proof044PublicReceipt) {
		t.Fatalf("missing public receipt proof error=%v", err)
	}
}

func requirementsByTarget(t *testing.T, featureID, target string) []ProofRequirement {
	t.Helper()
	var out []ProofRequirement
	for _, requirement := range RequirementsForFeature(featureID) {
		if requirement.RequiredFor == target {
			out = append(out, requirement)
		}
	}
	if len(out) == 0 {
		t.Fatalf("feature %s has no requirements for %s", featureID, target)
	}
	return out
}

func proofIDs(requirements []ProofRequirement) []string {
	out := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		out = append(out, requirement.ProofID)
	}
	return out
}
