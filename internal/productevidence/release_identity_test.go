package productevidence

import (
	"testing"
	"time"
)

const releaseIdentityCommit = "0123456789abcdef0123456789abcdef01234567"

func TestPackageIdentitySeparatesVersionCommitDigestAndTarget(t *testing.T) {
	identity := releasePackageIdentity("a")
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	if identity.Commit() != releaseIdentityCommit {
		t.Fatalf("commit=%q", identity.Commit())
	}
	for _, mutate := range []func(*PackageIdentity){
		func(p *PackageIdentity) { p.ProductVersion = "v0.1.0-alpha.1" },
		func(p *PackageIdentity) { p.SourceCommit = releaseIdentityCommit[:12] },
		func(p *PackageIdentity) { p.ArtifactSHA256 = "short" },
		func(p *PackageIdentity) { p.HostArch = "" },
	} {
		copy := identity
		mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Fatal("invalid package identity passed")
		}
	}
}

func TestSameCommitChangedArchiveIsStale(t *testing.T) {
	req := ProofRequirement{
		FeatureID: Feature033, ProofID: Proof033PackageIdentity,
		Layer: LayerReleaseCandidate, RequiredFor: RequiredForReleaseCandidate,
		FreshnessPolicy: FreshnessSameCommitAndPackage,
		ArtifactPolicy:  ArtifactPolicyNone, RuntimePolicy: RuntimePolicyNone,
		ClaimIDs: []string{"033.FR-006"},
	}
	actual := releasePackageIdentity("a")
	expected := releasePackageIdentity("b")
	manifest := Manifest{
		Version: Schema, GeneratedAt: time.Now(), Commit: releaseIdentityCommit,
		PackageIdentity: &actual,
		Proofs: []ProofEntry{{
			ProofID: req.ProofID, FeatureID: req.FeatureID, Mode: "unit",
			EvidenceClass: "release-identity", Status: StatusPassed,
			CommandSummary:  "release identity test",
			CoveredClaims:   []CoveredClaim{{ClaimID: "033.FR-006", Source: "spec", Description: "changed bytes are stale"}},
			RedactionStatus: RedactionPassed,
		}},
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements: []ProofRequirement{req}, Target: RequiredForReleaseCandidate,
		ExpectedCommit: releaseIdentityCommit, ExpectedPackage: &expected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0].Status; got != EvalStale {
		t.Fatalf("status=%s", got)
	}
}

func TestPublicReleaseTargetIncludesCandidateAndPublicRequirements(t *testing.T) {
	candidate := ProofRequirement{FeatureID: Feature033, ProofID: "candidate", Layer: LayerReleaseCandidate, RequiredFor: RequiredForReleaseCandidate, FreshnessPolicy: FreshnessNone, ArtifactPolicy: ArtifactPolicyNone, RuntimePolicy: RuntimePolicyNone, ClaimIDs: []string{"candidate"}}
	public := ProofRequirement{FeatureID: Feature033, ProofID: "public", Layer: LayerReleaseCandidate, RequiredFor: RequiredForPublicRelease, FreshnessPolicy: FreshnessNone, ArtifactPolicy: ArtifactPolicyNone, RuntimePolicy: RuntimePolicyNone, ClaimIDs: []string{"public"}}
	if !requirementAppliesToTarget(candidate, RequiredForPublicRelease) || !requirementAppliesToTarget(public, RequiredForPublicRelease) {
		t.Fatal("public-release target did not compose candidate and post-public requirements")
	}
	if requirementAppliesToTarget(public, RequiredForReleaseCandidate) {
		t.Fatal("post-public proof became a candidate prerequisite")
	}
}

func releasePackageIdentity(ch string) PackageIdentity {
	return PackageIdentity{
		Name: "hideout", ProductVersion: "0.1.0-alpha.1", SourceCommit: releaseIdentityCommit,
		ArtifactSHA256: repeatHex(ch), HostOS: "darwin", HostArch: "arm64",
	}
}

func repeatHex(ch string) string {
	value := ""
	for len(value) < 64 {
		value += ch
	}
	return value[:64]
}
