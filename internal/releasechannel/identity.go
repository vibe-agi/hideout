package releasechannel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const (
	PublicReleaseSchema      = "hideout.public-release/v1"
	EvidenceBundleSchema     = "hideout.public-evidence-bundle/v1"
	PublicationReceiptSchema = "hideout.publication-receipt/v1"
	PublishedInventorySchema = "hideout.published-release-inventory/v1"
	BinaryIdentitySchema     = "hideout.binary-identity/v1"
	InitialProductVersion    = "0.1.0-alpha.1"
)

var (
	commitRE           = regexp.MustCompile(`^[a-f0-9]{40}$`)
	digestRE           = regexp.MustCompile(`^[a-f0-9]{64}$`)
	semverPrereleaseRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)
)

type PackageIdentity struct {
	Name           string `json:"name"`
	ProductVersion string `json:"productVersion"`
	SourceCommit   string `json:"sourceCommit"`
	ArtifactSHA256 string `json:"artifactSHA256"`
	HostOS         string `json:"hostOS"`
	HostArch       string `json:"hostArch"`
}

func (p PackageIdentity) Validate() error {
	if p.Name != "hideout" {
		return fmt.Errorf("package identity name must be %q", "hideout")
	}
	if !semverPrereleaseRE.MatchString(p.ProductVersion) {
		return errors.New("package identity productVersion must be canonical SemVer prerelease")
	}
	if !IsCommit(p.SourceCommit) {
		return errors.New("package identity sourceCommit must be 40 lowercase hexadecimal characters")
	}
	if !IsSHA256(p.ArtifactSHA256) {
		return errors.New("package identity artifactSHA256 must be 64 lowercase hexadecimal characters")
	}
	if p.HostOS == "" || p.HostArch == "" {
		return errors.New("package identity hostOS and hostArch are required")
	}
	return nil
}

func (p PackageIdentity) Equal(other PackageIdentity) bool {
	return p == other
}

func IsCommit(value string) bool { return commitRE.MatchString(value) }

func IsSHA256(value string) bool { return digestRE.MatchString(value) }

func IsProductVersion(value string) bool { return semverPrereleaseRE.MatchString(value) }

func FileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func ValidateTag(version, tag string) error {
	if !IsProductVersion(version) {
		return fmt.Errorf("invalid product version %q", version)
	}
	if strings.TrimSpace(tag) != "v"+version {
		return fmt.Errorf("release tag %q does not match version %q", tag, version)
	}
	return nil
}
