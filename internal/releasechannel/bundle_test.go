package releasechannel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/productevidence"
)

func TestEvidenceBundleValidatesContainedArtifacts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proofs", "proof.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"status":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundleFixture(digest, size)
	if err := bundle.Validate(root, []string{"033.release.package-identity"}); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceBundleRejectsControlPlaneMaterial(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proofs", "proof.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "cap_0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	err = bundleFixture(digest, size).Validate(root, []string{"033.release.package-identity"})
	if err == nil || !strings.Contains(err.Error(), "control-plane") {
		t.Fatalf("error=%v", err)
	}
}

func TestEvidenceBundleRejectsWrongProofSet(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proofs", "proof.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	err = bundleFixture(digest, size).Validate(root, []string{"033.release.other"})
	if err == nil {
		t.Fatal("wrong proof set unexpectedly passed")
	}
}

func TestBuildEvidenceBundleDerivesSortedFeatureIDsFromProofManifests(t *testing.T) {
	root := t.TempDir()
	manifest := productevidence.Manifest{
		Version:     productevidence.Schema,
		GeneratedAt: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
		Commit:      testCommit,
		Proofs: []productevidence.ProofEntry{
			{
				ProofID: "046.release.package-migration", FeatureID: productevidence.Feature046,
				Mode: "local-fast", EvidenceClass: "release-package-migration",
				Status: productevidence.StatusPassed, CommandSummary: "validated migration",
				CoveredClaims: []productevidence.CoveredClaim{{
					ClaimID: "046.release.package-migration", Source: "test-plan",
					Description: "migration evidence is bound to the package",
				}},
				Artifacts: []productevidence.ArtifactRef{}, RedactionStatus: productevidence.RedactionPassed,
			},
			{
				ProofID: "045.release.closure", FeatureID: productevidence.Feature045,
				Mode: "local-fast", EvidenceClass: "release-closure",
				Status: productevidence.StatusPassed, CommandSummary: "validated operator console",
				CoveredClaims: []productevidence.CoveredClaim{{
					ClaimID: "045.release.closure", Source: "test-plan",
					Description: "operator console evidence is complete",
				}},
				Artifacts: []productevidence.ArtifactRef{}, RedactionStatus: productevidence.RedactionPassed,
			},
		},
	}
	path := filepath.Join(root, "proofs", "release", "manifest.json")
	if err := productevidence.WriteFile(path, manifest); err != nil {
		t.Fatal(err)
	}
	pkg := PackageIdentity{
		Name: "hideout", ProductVersion: InitialProductVersion, SourceCommit: testCommit,
		ArtifactSHA256: testDigest, HostOS: "darwin", HostArch: "arm64",
	}
	bundle, err := BuildEvidenceBundle(
		root,
		pkg,
		[]string{"046.release.package-migration", "045.release.closure"},
		time.Date(2026, 8, 5, 0, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{productevidence.Feature045, productevidence.Feature046}
	if strings.Join(bundle.FeatureIDs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("feature IDs=%v want %v", bundle.FeatureIDs, want)
	}
}

func TestFeatureIDsRequireCanonicalSortedUniqueValues(t *testing.T) {
	if err := validateFeatureIDs([]string{
		productevidence.Feature045,
		productevidence.Feature046,
	}); err != nil {
		t.Fatal(err)
	}
	for _, featureIDs := range [][]string{
		{productevidence.Feature046, productevidence.Feature045},
		{productevidence.Feature045, productevidence.Feature045},
		{"45-operator-observability-console"},
		{"045-Operator-observability-console"},
		{"045-operator--observability-console"},
	} {
		if err := validateFeatureIDs(featureIDs); err == nil {
			t.Fatalf("invalid feature IDs passed: %v", featureIDs)
		}
	}
}

func bundleFixture(digest string, size int64) EvidenceBundle {
	return EvidenceBundle{
		Schema: EvidenceBundleSchema, GeneratedAt: time.Now().UTC(), SourceCommit: testCommit,
		Package:        PackageIdentity{Name: "hideout", ProductVersion: InitialProductVersion, SourceCommit: testCommit, ArtifactSHA256: testDigest, HostOS: "darwin", HostArch: "arm64"},
		RegistrySchema: productevidence.RegistrySchema,
		ProofIDs:       []string{"033.release.package-identity"}, RedactionStatus: productevidence.RedactionPassed,
		Files: []BundleFile{{
			Path: "proofs/proof.json", Kind: "manifest", SHA256: digest, Bytes: size,
			RedactionStatus: productevidence.RedactionPassed,
			ExportDecision:  exportboundary.ExportDecision{Mode: exportboundary.DecisionRedact, Channel: exportboundary.DecisionChannelFlag},
			RedactionStages: []exportboundary.RedactionStage{{Name: "control-plane"}, {Name: exportboundary.PublicEvidenceLocalPathStage}},
		}},
	}
}
