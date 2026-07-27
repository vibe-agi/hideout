package main

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func TestReleaseCandidateOptionsBindManifestPackageRuntimeAndArtifactRoot(t *testing.T) {
	const commit = "abcdef012345abcdef012345abcdef012345abcd"
	packageIdentity := &productevidence.PackageIdentity{
		Name:           "hideout",
		ProductVersion: "0.1.0-alpha.2",
		SourceCommit:   commit,
		ArtifactSHA256: strings.Repeat("a", 64),
		HostOS:         "darwin",
		HostArch:       "arm64",
	}
	runtime := &productevidence.RuntimeBinding{
		Schema:         productevidence.RuntimeBindingSchema,
		Family:         "developer-standard",
		Revision:       "2026.07.0",
		ArtifactSHA256: strings.Repeat("b", 64),
		EnvironmentID:  "env_20260727t000000z0123456789abcdef0123",
		HostOS:         "darwin",
		HostArch:       "arm64",
		GuestArch:      "aarch64",
		BuildCommit:    "0123456789ab",
		BuildDirty:     false,
	}
	manifest := productevidence.Manifest{
		Commit:          commit,
		PackageIdentity: packageIdentity,
		Proofs: []productevidence.ProofEntry{{
			ProofID: productevidence.Proof044RealGate2FirstRun,
			Runtime: runtime,
		}},
	}

	opts, err := releaseCandidateOptions(manifest, "/tmp/044-artifacts")
	if err != nil {
		t.Fatal(err)
	}
	if opts.Target != productevidence.RequiredForReleaseCandidate ||
		opts.ExpectedCommit != commit ||
		opts.ExpectedPackage != packageIdentity ||
		opts.ArtifactRoot != "/tmp/044-artifacts" {
		t.Fatalf("release candidate trust anchors = %+v", opts)
	}
	if opts.ExpectedRuntime == nil ||
		opts.ExpectedRuntime.ArtifactSHA256 != runtime.ArtifactSHA256 ||
		opts.ExpectedRuntime.BuildCommit != runtime.BuildCommit ||
		!opts.ExpectedRuntime.RequireClean {
		t.Fatalf("release candidate runtime anchor = %+v", opts.ExpectedRuntime)
	}
	if got := len(opts.Requirements); got != len(productevidence.RequirementsForFeature(productevidence.Feature044)) {
		t.Fatalf("requirements=%d", got)
	}
}

func TestReleaseCandidateOptionsRequirePackageAndGate2Runtime(t *testing.T) {
	if _, err := releaseCandidateOptions(productevidence.Manifest{}, "/tmp/044-artifacts"); err == nil ||
		!strings.Contains(err.Error(), "package identity") {
		t.Fatalf("missing package identity error=%v", err)
	}

	manifest := productevidence.Manifest{
		PackageIdentity: &productevidence.PackageIdentity{},
		Proofs: []productevidence.ProofEntry{{
			ProofID: productevidence.Proof044RealGate2FirstRun,
		}},
	}
	if _, err := releaseCandidateOptions(manifest, "/tmp/044-artifacts"); err == nil ||
		!strings.Contains(err.Error(), "Gate 2 runtime binding") {
		t.Fatalf("missing Gate 2 runtime error=%v", err)
	}
}
