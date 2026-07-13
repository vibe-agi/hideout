package productevidence

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func TestProofRegistryValidatesAndIsDeterministic(t *testing.T) {
	view, err := RegistryView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Schema != RegistrySchema {
		t.Fatalf("schema=%q", view.Schema)
	}
	if err := ValidateRegistry(view.Requirements); err != nil {
		t.Fatal(err)
	}
	first, err := RegistryJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := RegistryJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("registry JSON is not deterministic:\n%s\n---\n%s", first, second)
	}
	var decoded ProofRegistryView
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Requirements) != len(view.Requirements) {
		t.Fatalf("decoded %d requirements, want %d", len(decoded.Requirements), len(view.Requirements))
	}
}

func TestProofRegistryCovers021To025RequiredProofsExactlyOnce(t *testing.T) {
	view, err := RegistryView()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, req := range view.Requirements {
		seen[req.ProofID]++
	}
	for _, proofID := range append(append(append(append(
		append([]string{}, Required021ProofIDs...),
		Required022LocalFastProofIDs...),
		Required023LocalFastProofIDs...),
		Required024LocalFastProofIDs...),
		Required025ProofIDs...) {
		if seen[proofID] != 1 {
			t.Fatalf("proof %s appears %d times in registry, want 1", proofID, seen[proofID])
		}
	}
}

func TestProofRegistryRejectsDuplicateProofID(t *testing.T) {
	reqs := ProductHardeningRequirements()
	reqs = append(reqs, reqs[0])
	if err := ValidateRegistry(reqs); err == nil {
		t.Fatal("duplicate proof id should fail")
	}
}

func TestRequiredProofIDsRemainFeatureScoped(t *testing.T) {
	for _, proofID := range Required021ProofIDs {
		if !slices.ContainsFunc(RequirementsForFeature(Feature021), func(req ProofRequirement) bool {
			return req.ProofID == proofID && req.FeatureID == Feature021
		}) {
			t.Fatalf("021 proof %s missing from feature-scoped requirements", proofID)
		}
	}
}

func TestPublicReleaseTargetIncludesCandidateAndPublicProofs(t *testing.T) {
	ids := RequiredProofIDsForTarget(RequiredForPublicRelease)
	for _, proofID := range []string{
		Proof033PackageIdentity,
		Proof033PublicDownload,
		Proof033DocsPublicTruth,
	} {
		if !slices.Contains(ids, proofID) {
			t.Fatalf("public-release target is missing %s", proofID)
		}
	}
	if slices.Contains(ids, Proof021EvidenceSchema) {
		t.Fatal("targeted-completion proof leaked into public-release target")
	}
}

func TestRuntimeProofRegistryCoversLocalAndRealClaims(t *testing.T) {
	reqs := RequirementsForFeature(Feature031)
	if len(reqs) != 8 || len(Required031ProofIDs) != 8 {
		t.Fatalf("031 requirements=%d requiredIDs=%d want 8", len(reqs), len(Required031ProofIDs))
	}
	seen := map[string]ProofRequirement{}
	for _, req := range reqs {
		seen[req.ProofID] = req
	}
	if seen[Proof031Gate0Mechanics].Layer != LayerGate0 {
		t.Fatalf("gate0 proof layer=%q", seen[Proof031Gate0Mechanics].Layer)
	}
	for _, id := range []string{Proof031RealImage, Proof031Baseline, Proof031AgentInstall, Proof031AgentPrivacy, Proof031BoundaryRegression} {
		if seen[id].Layer != LayerRealGate || seen[id].ArtifactPolicy != ArtifactPolicyExistsAndDigestIfSupplied || seen[id].RuntimePolicy != RuntimePolicyExactReal {
			t.Fatalf("real proof %s has weak requirement: %+v", id, seen[id])
		}
	}
}

func TestHostAppPackProofRegistryRequiresArtifactBackedRealEvidence(t *testing.T) {
	want := []string{
		Proof032Gate0Lifecycle,
		Proof032Gate0Binding,
		Proof032Gate0IdentitySafety,
		Proof032RealGate2External,
	}
	reqs := RequirementsForFeature(Feature032)
	if len(reqs) != len(want) || len(Required032ProofIDs) != len(want) {
		t.Fatalf("032 requirements=%d requiredIDs=%d want %d", len(reqs), len(Required032ProofIDs), len(want))
	}
	seen := map[string]ProofRequirement{}
	for _, req := range reqs {
		seen[req.ProofID] = req
	}
	for _, proofID := range want {
		req, ok := seen[proofID]
		if !ok {
			t.Fatalf("032 proof %s is not registered", proofID)
		}
		if req.ArtifactPolicy != ArtifactPolicyExistsAndDigestIfSupplied {
			t.Fatalf("032 proof %s has weak artifact policy: %+v", proofID, req)
		}
	}
	for _, proofID := range []string{Proof032Gate0Lifecycle, Proof032Gate0Binding, Proof032Gate0IdentitySafety} {
		if seen[proofID].Layer != LayerGate0 || seen[proofID].RequiredFor != RequiredForTargetedCompletion {
			t.Fatalf("032 Gate 0 proof %s has wrong scope: %+v", proofID, seen[proofID])
		}
	}
	real := seen[Proof032RealGate2External]
	if real.Layer != LayerRealGate || real.RequiredFor != RequiredForReleaseCandidate ||
		real.RequiredMode != "real-gate" || real.RequiredEvidenceClass != "host-app-pack-external-real-gate2" {
		t.Fatalf("032 real proof cannot satisfy release requirements: %+v", real)
	}
}

func TestProofRegistryCovers029WithRealArtifactAndNotRunBoundaries(t *testing.T) {
	want := []string{
		Proof029UnitPolicy,
		Proof029UnitTypedErrno,
		Proof029LocalDecisionLifecycle,
		Proof029LocalRedaction,
		Proof029RealGate2Namespace,
		Proof029RealGate2LiveGrant,
		Proof029RealGate2NotRun,
		Proof029DocsClaimBoundary,
	}
	reqs := RequirementsForFeature(Feature029)
	if len(reqs) != len(want) || len(Required029ProofIDs) != len(want) {
		t.Fatalf("029 requirements=%d requiredIDs=%d want %d", len(reqs), len(Required029ProofIDs), len(want))
	}
	seen := map[string]ProofRequirement{}
	for _, req := range reqs {
		seen[req.ProofID] = req
	}
	for _, proofID := range want {
		if _, ok := seen[proofID]; !ok {
			t.Fatalf("029 proof %s is not registered", proofID)
		}
	}
	for _, proofID := range []string{Proof029LocalDecisionLifecycle, Proof029LocalRedaction, Proof029RealGate2Namespace, Proof029RealGate2LiveGrant, Proof029RealGate2NotRun} {
		if seen[proofID].ArtifactPolicy == ArtifactPolicyNone {
			t.Fatalf("029 proof %s can pass without a real artifact", proofID)
		}
	}
	if seen[Proof029RealGate2Namespace].RequiredFor != RequiredForReleaseCandidate || seen[Proof029RealGate2LiveGrant].RequiredFor != RequiredForReleaseCandidate {
		t.Fatal("real Gate 2 proofs must be release-candidate requirements")
	}
	if seen[Proof029RealGate2NotRun].RequiredFor != RequiredForSupportingOnly {
		t.Fatal("not-run proof must remain supporting-only")
	}
}

func TestProofRegistryCovers030WithoutLettingNotRunSatisfyRelease(t *testing.T) {
	want := []string{
		Proof030Gate0Mechanics,
		Proof030RealGate2CodeOpen,
		Proof030RealGate2PrivacyChannels,
		Proof030RealGate2TrustedGrant,
		Proof030RealGate2NotRun,
		Proof030DocsClaimBoundary,
	}
	reqs := RequirementsForFeature(Feature030)
	if len(reqs) != len(want) || len(Required030ProofIDs) != len(want) {
		t.Fatalf("030 requirements=%d requiredIDs=%d want %d", len(reqs), len(Required030ProofIDs), len(want))
	}
	seen := map[string]ProofRequirement{}
	for _, req := range reqs {
		seen[req.ProofID] = req
	}
	for _, proofID := range want {
		if _, ok := seen[proofID]; !ok {
			t.Fatalf("030 proof %s is not registered", proofID)
		}
	}
	for _, proofID := range []string{Proof030RealGate2CodeOpen, Proof030RealGate2PrivacyChannels, Proof030RealGate2TrustedGrant} {
		if seen[proofID].RequiredFor != RequiredForReleaseCandidate || seen[proofID].ArtifactPolicy == ArtifactPolicyNone {
			t.Fatalf("030 real proof %s must be an artifact-backed release requirement: %+v", proofID, seen[proofID])
		}
	}
	if seen[Proof030RealGate2NotRun].RequiredFor != RequiredForSupportingOnly {
		t.Fatal("030 not-run evidence must never satisfy release readiness")
	}
}
