package packagekit

import (
	"path/filepath"
	"runtime"
	"testing"
)

const releaseTestCommit = "0123456789abcdef0123456789abcdef01234567"

func TestCanonicalPackageManifestAndInstallState(t *testing.T) {
	root := writeTestArtifact(t, nil)
	manifest := readManifest(t, root)
	manifest.Release = ReleaseInfo{ProductVersion: "0.1.0-alpha.1", Channel: "alpha", Tag: "v0.1.0-alpha.1"}
	manifest.Source = SourceInfo{Repository: "https://github.com/vibe-agi/hideout", Commit: releaseTestCommit}
	manifest.Build = BuildInfo{Workflow: "test", Ref: "refs/tags/v0.1.0-alpha.1"}
	manifest.Runtime = RuntimeInfo{Family: "developer-standard", Revision: "2026.07.0", CatalogFileSHA256: testSHA('a'), ArtifactSHA256: testSHA('b')}
	manifest.SigningSummary = SigningSummary{Mode: "developer-id-observed"}
	writeManifest(t, root, manifest)
	loaded, err := LoadManifest(filepath.Join(root, "package-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SourceCommit() != releaseTestCommit || loaded.SourceDirty() {
		t.Fatalf("source=%+v", loaded.Source)
	}
	prefix := t.TempDir()
	store := t.TempDir()
	if _, err := Install(InstallOptions{PackageRoot: root, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	state := readState(t, prefix)
	if state.Schema != InstallStateSchema || state.Package.Release.ProductVersion != "0.1.0-alpha.1" || state.Package.SourceCommit() != releaseTestCommit {
		t.Fatalf("state=%+v", state)
	}
}

func TestCanonicalPackageManifestRejectsAbbreviatedOrDirtyPublicSource(t *testing.T) {
	base := Manifest{
		Schema: ArtifactSchema, BuiltAt: "2026-07-13T00:00:00Z",
		Release:        ReleaseInfo{ProductVersion: "0.1.0-alpha.1", Channel: "alpha", Tag: "v0.1.0-alpha.1"},
		Source:         SourceInfo{Repository: "https://github.com/vibe-agi/hideout", Commit: releaseTestCommit},
		Build:          BuildInfo{Workflow: "test", Ref: "refs/tags/v0.1.0-alpha.1"},
		Target:         Target{HostOS: runtime.GOOS, HostArch: runtime.GOARCH, LinuxGuestArch: runtime.GOARCH},
		Runtime:        RuntimeInfo{Family: "developer-standard", Revision: "2026.07.0", CatalogFileSHA256: testSHA('a'), ArtifactSHA256: testSHA('b')},
		SigningSummary: SigningSummary{Mode: "developer-id-observed"},
		Files:          []File{{Path: "x", Kind: "doc", SHA256: testSHA('c')}},
		Migration:      Migration{InstallStateSchema: InstallStateSchema, FromInstalledSchemas: []string{InstallStateSchema}, MinimumPackageSchema: ArtifactSchema, MaximumPackageSchema: ArtifactSchema},
	}
	short := base
	short.Source.Commit = releaseTestCommit[:12]
	if err := validateManifest(short, true); err == nil {
		t.Fatal("abbreviated source commit passed")
	}
	dirty := base
	dirty.Source.Dirty = true
	if err := validateManifest(dirty, true); err == nil {
		t.Fatal("dirty public source passed")
	}
}

func TestDistributionManifestValidationDoesNotPretendTargetIsRunnable(t *testing.T) {
	manifest := Manifest{
		Schema: ArtifactSchema, BuiltAt: "2026-07-13T00:00:00Z",
		Release:        ReleaseInfo{ProductVersion: "0.1.0-alpha.1", Channel: "alpha", Tag: "v0.1.0-alpha.1"},
		Source:         SourceInfo{Repository: "https://github.com/vibe-agi/hideout", Commit: releaseTestCommit},
		Build:          BuildInfo{Workflow: "test", Ref: "refs/tags/v0.1.0-alpha.1"},
		Target:         Target{HostOS: "different-os", HostArch: "different-arch", LinuxGuestArch: "aarch64"},
		Runtime:        RuntimeInfo{Family: "developer-standard", Revision: "2026.07.0", CatalogFileSHA256: testSHA('a'), ArtifactSHA256: testSHA('b')},
		SigningSummary: SigningSummary{Mode: "developer-id-observed"},
		Files:          []File{{Path: "x", Kind: "doc", SHA256: testSHA('c')}},
		Migration:      Migration{InstallStateSchema: InstallStateSchema, FromInstalledSchemas: []string{InstallStateSchema}, MinimumPackageSchema: ArtifactSchema, MaximumPackageSchema: ArtifactSchema},
	}
	if err := validateManifest(manifest, true); err == nil || !IsPlatformMismatch(err) {
		t.Fatalf("end-user validation should reject another target, got %v", err)
	}
	if err := validateManifest(manifest, false); err != nil {
		t.Fatalf("distribution validation should inspect another target: %v", err)
	}
}

func testSHA(ch byte) string {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = ch
	}
	return string(buf)
}
