package releasechannel

import (
	"testing"
	"time"
)

func TestPublicationReceiptMatchesRelease(t *testing.T) {
	_, release := releaseFixture(t)
	packageIdentity := packageIdentityFromRelease(release)
	receipt := PublicationReceipt{
		Schema: PublicationReceiptSchema, Status: "public-verified", ObservedAt: time.Now(),
		Version: release.Version, Tag: release.Tag, SourceCommit: release.Source.Commit,
		ReleaseID: 123, URL: "https://github.com/vibe-agi/hideout/releases/tag/" + release.Tag,
		Prerelease: true, Immutable: true, Package: packageIdentity,
		EvidenceSHA256: testDigest, ProofStatus: "satisfied",
	}
	for _, name := range []string{"hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz", "hideout-v0.1.0-alpha.1-evidence.tar.gz", "hideout-v0.1.0-alpha.1-release.json", "SHA256SUMS"} {
		receipt.Assets = append(receipt.Assets, DownloadedAsset{Name: name, Bytes: 1, APISHA256: testDigest, DownloadSHA256: testDigest})
	}
	if err := receipt.Validate(release); err != nil {
		t.Fatal(err)
	}
	receipt.Assets[0].DownloadSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := receipt.Validate(release); err == nil {
		t.Fatal("download digest drift passed")
	}
}
