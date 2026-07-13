package releasechannel

import (
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/packagekit"
)

func TestPackageVerificationObservationRequiresExactIdentity(t *testing.T) {
	manifest := packagekit.Manifest{
		Release: packagekit.ReleaseInfo{ProductVersion: InitialProductVersion},
		Source:  packagekit.SourceInfo{Commit: testCommit},
		Target:  packagekit.Target{HostOS: "darwin", HostArch: "arm64"},
		Files:   []packagekit.File{{Path: "bin/hideout"}},
	}
	pkg := PackageIdentity{Name: "hideout", ProductVersion: InitialProductVersion, SourceCommit: testCommit, ArtifactSHA256: testDigest, HostOS: "darwin", HostArch: "arm64"}
	observation := PackageVerificationObservation{
		Schema: PackageVerificationSchema, ObservedAt: time.Now(), Status: "passed",
		Mode: "artifact", Files: 1, Package: pkg, PackageManifestSHA256: testDigest,
	}
	if err := observation.Validate(manifest, pkg, testDigest); err != nil {
		t.Fatal(err)
	}
	observation.Package.ArtifactSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := observation.Validate(manifest, pkg, testDigest); err == nil {
		t.Fatal("mismatched package verification identity unexpectedly passed")
	}
}

func TestRuntimeBuildProvenanceRequiresExactPackagedDigest(t *testing.T) {
	manifest := packagekit.Manifest{Runtime: packagekit.RuntimeInfo{Revision: "2026.07.0", ArtifactSHA256: testDigest}}
	var provenance RuntimeBuildProvenance
	provenance.Schema = RuntimeBuildProvenanceSchema
	provenance.Revision = manifest.Runtime.Revision
	provenance.Source.Commit = testCommit
	provenance.Source.SourceLockSHA256 = testDigest
	provenance.Builder.ObservedIdentity = "builder@sha256:" + testDigest
	provenance.Builder.ExpectedIdentity = provenance.Builder.ObservedIdentity
	provenance.Builder.Attestation = "workflow-declared"
	provenance.Output.File = "runtime.qcow2"
	provenance.Output.SHA256 = testDigest
	provenance.Output.Bytes = 1
	provenance.StartedAt = time.Now().Add(-time.Minute)
	provenance.CompletedAt = time.Now()
	if err := provenance.Validate(manifest); err != nil {
		t.Fatal(err)
	}
	provenance.Output.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := provenance.Validate(manifest); err == nil {
		t.Fatal("mismatched runtime provenance unexpectedly passed")
	}
}
