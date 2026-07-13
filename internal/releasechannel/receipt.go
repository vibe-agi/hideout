package releasechannel

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PublicationReceipt struct {
	Schema         string            `json:"schema"`
	Status         string            `json:"status"`
	ObservedAt     time.Time         `json:"observedAt"`
	Version        string            `json:"version"`
	Tag            string            `json:"tag"`
	SourceCommit   string            `json:"sourceCommit"`
	ReleaseID      int64             `json:"releaseId"`
	URL            string            `json:"url"`
	Prerelease     bool              `json:"prerelease"`
	Immutable      bool              `json:"immutable"`
	Package        PackageIdentity   `json:"package"`
	EvidenceSHA256 string            `json:"evidenceSHA256"`
	Assets         []DownloadedAsset `json:"assets"`
	ProofStatus    string            `json:"proofStatus"`
}

type DownloadedAsset struct {
	Name           string `json:"name"`
	Bytes          int64  `json:"bytes"`
	APISHA256      string `json:"apiSHA256"`
	DownloadSHA256 string `json:"downloadSHA256"`
}

func (r PublicationReceipt) Validate(release PublicRelease) error {
	if r.Schema != PublicationReceiptSchema || r.Status != "public-verified" || r.ObservedAt.IsZero() || r.ReleaseID <= 0 {
		return errors.New("publication receipt is incomplete")
	}
	if r.Version != release.Version || r.Tag != release.Tag || r.SourceCommit != release.Source.Commit || !r.Package.Equal(packageIdentityFromRelease(release)) {
		return errors.New("publication receipt identity does not match release")
	}
	expectedURL := "https://github.com/vibe-agi/hideout/releases/tag/" + r.Tag
	if !r.Prerelease || !r.Immutable || r.URL != expectedURL || !IsSHA256(r.EvidenceSHA256) || r.ProofStatus != "satisfied" {
		return errors.New("publication receipt lacks immutable anonymous proof")
	}
	want := []string{fmt.Sprintf("hideout-v%s-darwin-arm64.tar.gz", r.Version), fmt.Sprintf("hideout-v%s-evidence.tar.gz", r.Version), fmt.Sprintf("hideout-v%s-release.json", r.Version), "SHA256SUMS"}
	got := make([]string, 0, len(r.Assets))
	seen := map[string]bool{}
	for _, asset := range r.Assets {
		if seen[asset.Name] || asset.Bytes <= 0 || !IsSHA256(asset.APISHA256) || asset.APISHA256 != asset.DownloadSHA256 {
			return fmt.Errorf("downloaded asset %q is invalid", asset.Name)
		}
		seen[asset.Name] = true
		got = append(got, asset.Name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return errors.New("publication receipt asset set does not match release")
	}
	return nil
}

func packageIdentityFromRelease(release PublicRelease) PackageIdentity {
	for _, artifact := range release.Artifacts {
		if artifact.Kind == "package" {
			return PackageIdentity{Name: "hideout", ProductVersion: release.Version, SourceCommit: release.Source.Commit, ArtifactSHA256: artifact.SHA256, HostOS: artifact.HostOS, HostArch: artifact.HostArch}
		}
	}
	return PackageIdentity{}
}
