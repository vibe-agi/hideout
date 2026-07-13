package releasecompat

import (
	"testing"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func TestBuildReadinessKeepsVersionCommitAndArchiveDistinct(t *testing.T) {
	pkg := &productevidence.PackageIdentity{
		Name: "hideout", ProductVersion: "0.1.0-alpha.1",
		SourceCommit:   "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HostOS:         "darwin", HostArch: "arm64",
	}
	runtime := runtimeExpectationFixture()
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", Package: pkg, Runtime: &runtime})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Schema != ReadinessSchema || ready.SourceCommit != pkg.SourceCommit || ready.Package == nil || ready.Package.ProductVersion != pkg.ProductVersion || ready.Package.ArtifactSHA256 != pkg.ArtifactSHA256 {
		t.Fatalf("readiness=%+v", ready)
	}
	if ready.ReleaseReady {
		t.Fatal("identity alone made release ready")
	}
}

func TestValidateReadinessRejectsIdentitySubstitution(t *testing.T) {
	pkg := &productevidence.PackageIdentity{
		Name: "hideout", ProductVersion: "0.1.0-alpha.1",
		SourceCommit:   "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HostOS:         "darwin", HostArch: "arm64",
	}
	runtime := runtimeExpectationFixture()
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", Package: pkg, Runtime: &runtime})
	if err != nil {
		t.Fatal(err)
	}
	ready.SourceCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := ValidateReadiness(ready); err == nil {
		t.Fatal("substituted source commit passed")
	}
}
