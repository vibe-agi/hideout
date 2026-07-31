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
