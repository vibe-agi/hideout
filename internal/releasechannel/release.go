package releasechannel

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PublicRelease struct {
	Schema               string              `json:"schema"`
	Version              string              `json:"version"`
	Channel              string              `json:"channel"`
	Maturity             string              `json:"maturity"`
	Tag                  string              `json:"tag"`
	Source               Source              `json:"source"`
	License              License             `json:"license"`
	Artifacts            []ReleaseArtifact   `json:"artifacts"`
	Signing              SigningSummary      `json:"signing"`
	Notarization         NotarizationSummary `json:"notarization"`
	Runtime              RuntimeSummary      `json:"runtime"`
	Checksums            ChecksumsSummary    `json:"checksums"`
	SupportMatrixVersion string              `json:"supportMatrixVersion"`
	NonClaims            []string            `json:"nonClaims"`
	GeneratedAt          time.Time           `json:"generatedAt"`
}

type Source struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Dirty      bool   `json:"dirty"`
}

type License struct {
	SPDX              string `json:"spdx"`
	ThirdPartyNotices string `json:"thirdPartyNotices"`
}

type ReleaseArtifact struct {
	Kind                  string `json:"kind"`
	Name                  string `json:"name"`
	HostOS                string `json:"hostOS,omitempty"`
	HostArch              string `json:"hostArch,omitempty"`
	SHA256                string `json:"sha256"`
	Bytes                 int64  `json:"bytes"`
	PackageManifestSHA256 string `json:"packageManifestSHA256,omitempty"`
	BundleManifestSHA256  string `json:"bundleManifestSHA256,omitempty"`
	ReadinessSHA256       string `json:"readinessSHA256,omitempty"`
	Status                string `json:"status,omitempty"`
}

type SigningSummary struct {
	Status            string `json:"status"`
	TeamID            string `json:"teamId,omitempty"`
	CommonName        string `json:"commonName,omitempty"`
	ObservationSHA256 string `json:"observationSHA256,omitempty"`
}

type NotarizationSummary struct {
	Status           string `json:"status"`
	SubmissionID     string `json:"submissionId,omitempty"`
	SubmissionSHA256 string `json:"submissionSHA256,omitempty"`
	TicketMode       string `json:"ticketMode"`
	StapleStatus     string `json:"stapleStatus"`
}

type RuntimeSummary struct {
	Family            string `json:"family"`
	Revision          string `json:"revision"`
	CatalogFileSHA256 string `json:"catalogFileSHA256"`
	ArtifactSHA256    string `json:"artifactSHA256"`
}

type ChecksumsSummary struct {
	Name   string   `json:"name"`
	Covers []string `json:"covers"`
}

func (r PublicRelease) Validate(assetRoot string) error {
	if r.Schema != PublicReleaseSchema {
		return fmt.Errorf("unsupported public release schema %q", r.Schema)
	}
	if err := ValidateTag(r.Version, r.Tag); err != nil {
		return err
	}
	if r.Channel != "alpha" || r.Maturity != "public-supervised-alpha" {
		return errors.New("public release must use alpha/public-supervised-alpha")
	}
	if r.Source.Repository != "https://github.com/vibe-agi/hideout" || !IsCommit(r.Source.Commit) || r.Source.Dirty {
		return errors.New("public release source must be the clean canonical hideout commit")
	}
	if r.License.SPDX != "Apache-2.0" || r.License.ThirdPartyNotices != "THIRD_PARTY_NOTICES.md" {
		return errors.New("public release requires Apache-2.0 and separate third-party notices")
	}
	if r.GeneratedAt.IsZero() || r.SupportMatrixVersion == "" || len(r.NonClaims) == 0 {
		return errors.New("generatedAt, supportMatrixVersion, and nonClaims are required")
	}
	wantPackage := fmt.Sprintf("hideout-v%s-darwin-arm64.tar.gz", r.Version)
	wantEvidence := fmt.Sprintf("hideout-v%s-evidence.tar.gz", r.Version)
	wantRelease := fmt.Sprintf("hideout-v%s-release.json", r.Version)
	wantAssets := []string{wantPackage, wantEvidence, wantRelease, "SHA256SUMS"}
	if len(r.Artifacts) != 2 {
		return errors.New("release manifest must declare exactly package and evidence artifacts")
	}
	seen := map[string]bool{}
	for _, artifact := range r.Artifacts {
		if seen[artifact.Name] {
			return fmt.Errorf("duplicate release artifact %q", artifact.Name)
		}
		seen[artifact.Name] = true
		if artifact.Kind != "package" && artifact.Kind != "evidence" {
			return fmt.Errorf("unsupported release artifact kind %q", artifact.Kind)
		}
		if artifact.Kind == "package" && (artifact.Name != wantPackage || artifact.HostOS != "darwin" || artifact.HostArch != "arm64" || !IsSHA256(artifact.PackageManifestSHA256)) {
			return errors.New("package artifact identity is invalid")
		}
		if artifact.Kind == "evidence" && (artifact.Name != wantEvidence || artifact.Status != "passed" || !IsSHA256(artifact.BundleManifestSHA256) || !IsSHA256(artifact.ReadinessSHA256)) {
			return errors.New("evidence artifact identity is invalid")
		}
		if err := VerifyRootedRegularFile(assetRoot, artifact.Name, artifact.SHA256, artifact.Bytes); err != nil {
			return err
		}
	}
	if err := r.Signing.ValidatePublic(); err != nil {
		return err
	}
	if err := r.Notarization.ValidatePublic(); err != nil {
		return err
	}
	if r.Runtime.Family == "" || r.Runtime.Revision == "" || !IsSHA256(r.Runtime.CatalogFileSHA256) || !IsSHA256(r.Runtime.ArtifactSHA256) {
		return errors.New("runtime identity is invalid")
	}
	if r.Checksums.Name != "SHA256SUMS" {
		return errors.New("checksums name must be SHA256SUMS")
	}
	wantCovered := []string{wantEvidence, wantPackage, wantRelease}
	gotCovered := append([]string(nil), r.Checksums.Covers...)
	sort.Strings(gotCovered)
	sort.Strings(wantCovered)
	if strings.Join(gotCovered, "\x00") != strings.Join(wantCovered, "\x00") {
		return errors.New("checksums coverage does not match the exact release asset set")
	}
	for _, name := range wantAssets[2:] {
		if _, _, err := RootedFileSHA256(assetRoot, name); err != nil {
			return fmt.Errorf("required release asset %q: %w", name, err)
		}
	}
	return ValidateChecksums(assetRoot, r.Checksums)
}

func ValidateChecksums(root string, summary ChecksumsSummary) error {
	data, err := ReadRootedBounded(root, summary.Name, MaxJSONBytes)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(summary.Covers) {
		return errors.New("SHA256SUMS has an unexpected line count")
	}
	previous := ""
	seen := map[string]bool{}
	for _, line := range lines {
		parts := strings.Split(line, "  ")
		if len(parts) != 2 || !IsSHA256(parts[0]) || strings.Contains(parts[1], "/") {
			return fmt.Errorf("invalid SHA256SUMS line %q", line)
		}
		name := parts[1]
		if previous != "" && name <= previous {
			return errors.New("SHA256SUMS must be sorted by unique basename")
		}
		previous = name
		seen[name] = true
		actual, _, err := RootedFileSHA256(root, name)
		if err != nil || actual != parts[0] {
			return fmt.Errorf("SHA256SUMS digest mismatch for %q", name)
		}
	}
	for _, name := range summary.Covers {
		if !seen[name] {
			return fmt.Errorf("SHA256SUMS does not cover %q", name)
		}
	}
	return nil
}
