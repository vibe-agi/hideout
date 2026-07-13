package releasechannel

import (
	"testing"
	"time"
)

func TestPublishedInventoryAllowsNoPublicReleaseAndValidatesCurrent(t *testing.T) {
	base := PublishedInventory{Schema: PublishedInventorySchema, GeneratedAt: time.Now()}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Current = &InventoryEntry{
		Version: InitialProductVersion, Tag: "v" + InitialProductVersion,
		Maturity: "public-supervised-alpha", Platform: "darwin/arm64", Backend: "lima",
		Package:       PackageIdentity{Name: "hideout", ProductVersion: InitialProductVersion, SourceCommit: testCommit, ArtifactSHA256: testDigest, HostOS: "darwin", HostArch: "arm64"},
		ReleaseURL:    "https://github.com/vibe-agi/hideout/releases/tag/v" + InitialProductVersion,
		ReceiptSHA256: testDigest, SupportMatrix: "2026-07-13", NonClaims: []string{"workspace-dlp"},
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Current.Package.ArtifactSHA256 = ""
	if err := base.Validate(); err == nil {
		t.Fatal("incomplete current release passed")
	}
}
