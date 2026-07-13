package releasechannel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testCommit = "0123456789abcdef0123456789abcdef01234567"
	testDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestPublicReleaseValidatesExactAssets(t *testing.T) {
	root, release := releaseFixture(t)
	if err := release.Validate(root); err != nil {
		t.Fatal(err)
	}
}

func TestPublicReleaseRejectsIdentityAndAssetMutations(t *testing.T) {
	root, base := releaseFixture(t)
	tests := []struct {
		name   string
		mutate func(*PublicRelease)
	}{
		{"abbreviated commit", func(r *PublicRelease) { r.Source.Commit = testCommit[:12] }},
		{"dirty source", func(r *PublicRelease) { r.Source.Dirty = true }},
		{"tag mismatch", func(r *PublicRelease) { r.Tag = "v0.1.0-alpha.2" }},
		{"unsigned", func(r *PublicRelease) { r.Signing.Status = "developer-preview-unsigned" }},
		{"notary rejected", func(r *PublicRelease) { r.Notarization.Status = "rejected" }},
		{"missing artifact", func(r *PublicRelease) { r.Artifacts = r.Artifacts[:1] }},
		{"wrong target", func(r *PublicRelease) { r.Artifacts[0].HostArch = "amd64" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			r.Artifacts = append([]ReleaseArtifact(nil), base.Artifacts...)
			tt.mutate(&r)
			if err := r.Validate(root); err == nil {
				t.Fatal("mutation unexpectedly passed")
			}
		})
	}
}

func TestPublicReleaseRejectsChangedPackageBytes(t *testing.T) {
	root, release := releaseFixture(t)
	packagePath := filepath.Join(root, release.Artifacts[0].Name)
	if err := os.WriteFile(packagePath, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := release.Validate(root); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyRootedRegularFileRejectsSymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "artifact"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(filepath.Join(outside, "artifact"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRootedRegularFile(root, "escape/artifact", digest, size); err == nil {
		t.Fatal("symlink ancestor unexpectedly passed")
	}
}

func releaseFixture(t *testing.T) (string, PublicRelease) {
	t.Helper()
	root := t.TempDir()
	version := InitialProductVersion
	packageName := fmt.Sprintf("hideout-v%s-darwin-arm64.tar.gz", version)
	evidenceName := fmt.Sprintf("hideout-v%s-evidence.tar.gz", version)
	releaseName := fmt.Sprintf("hideout-v%s-release.json", version)
	write := func(name, body string) (string, int64) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		digest, size, err := FileSHA256(path)
		if err != nil {
			t.Fatal(err)
		}
		return digest, size
	}
	packageDigest, packageSize := write(packageName, "package-bytes")
	evidenceDigest, evidenceSize := write(evidenceName, "evidence-bytes")
	releaseDigest, _ := write(releaseName, "release-manifest-bytes")
	lines := []string{
		packageDigest + "  " + packageName,
		evidenceDigest + "  " + evidenceName,
		releaseDigest + "  " + releaseName,
	}
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, PublicRelease{
		Schema: PublicReleaseSchema, Version: version, Channel: "alpha",
		Maturity: "public-supervised-alpha", Tag: "v" + version,
		Source:  Source{Repository: "https://github.com/vibe-agi/hideout", Commit: testCommit},
		License: License{SPDX: "Apache-2.0", ThirdPartyNotices: "THIRD_PARTY_NOTICES.md"},
		Artifacts: []ReleaseArtifact{
			{Kind: "package", Name: packageName, HostOS: "darwin", HostArch: "arm64", SHA256: packageDigest, Bytes: packageSize, PackageManifestSHA256: testDigest},
			{Kind: "evidence", Name: evidenceName, SHA256: evidenceDigest, Bytes: evidenceSize, BundleManifestSHA256: testDigest, ReadinessSHA256: testDigest, Status: "passed"},
		},
		Signing:              SigningSummary{Status: "developer-id-verified", TeamID: "TEAM123", CommonName: "Developer ID Application: Test (TEAM123)", ObservationSHA256: testDigest},
		Notarization:         NotarizationSummary{Status: "accepted", SubmissionID: "00000000-0000-0000-0000-000000000001", SubmissionSHA256: testDigest, TicketMode: "online", StapleStatus: "not-applicable-tar-gz"},
		Runtime:              RuntimeSummary{Family: "developer-standard", Revision: "2026.07.0", CatalogFileSHA256: testDigest, ArtifactSHA256: testDigest},
		Checksums:            ChecksumsSummary{Name: "SHA256SUMS", Covers: []string{packageName, evidenceName, releaseName}},
		SupportMatrixVersion: "2026-07-13", NonClaims: []string{"workspace-dlp"},
		GeneratedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
	}
}
