package releasechannel

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/packagekit"
)

const (
	PackageVerificationSchema    = "hideout.release-package-verification/v1"
	RuntimeBuildProvenanceSchema = "hideout.runtime-build-provenance/v1"
)

type PackageVerificationObservation struct {
	Schema                string          `json:"schema"`
	ObservedAt            time.Time       `json:"observedAt"`
	Status                string          `json:"status"`
	Mode                  string          `json:"mode"`
	Files                 int             `json:"files"`
	Package               PackageIdentity `json:"package"`
	PackageManifestSHA256 string          `json:"packageManifestSHA256"`
}

type RuntimeBuildProvenance struct {
	Schema   string `json:"schema"`
	Revision string `json:"revision"`
	Source   struct {
		Commit           string `json:"commit"`
		Dirty            bool   `json:"dirty"`
		SourceLockSHA256 string `json:"sourceLockSHA256"`
	} `json:"source"`
	Builder struct {
		ObservedIdentity          string `json:"observedIdentity"`
		ExpectedIdentity          string `json:"expectedIdentity"`
		Attestation               string `json:"attestation"`
		QEMU                      string `json:"qemu"`
		Libguestfs                string `json:"libguestfs"`
		LibguestfsBackend         string `json:"libguestfsBackend"`
		LibguestfsBackendSettings string `json:"libguestfsBackendSettings"`
		LibguestfsAcceleration    string `json:"libguestfsAcceleration"`
	} `json:"builder"`
	Output struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	} `json:"output"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	Promoted    bool      `json:"promoted"`
}

func ObservePackageVerification(root string, pkg PackageIdentity, now time.Time) (PackageVerificationObservation, error) {
	if err := pkg.Validate(); err != nil {
		return PackageVerificationObservation{}, err
	}
	if now.IsZero() {
		return PackageVerificationObservation{}, errors.New("package verification observedAt is required")
	}
	verification, err := packagekit.Verify(root)
	if err != nil {
		return PackageVerificationObservation{}, err
	}
	if verification.Mode != "artifact" {
		return PackageVerificationObservation{}, fmt.Errorf("release package verification requires artifact mode, got %q", verification.Mode)
	}
	manifestPath := filepath.Join(verification.Root, "package-manifest.json")
	manifest, err := packagekit.LoadManifest(manifestPath)
	if err != nil {
		return PackageVerificationObservation{}, err
	}
	if err := packageManifestMatchesIdentity(manifest, pkg); err != nil {
		return PackageVerificationObservation{}, err
	}
	digest, _, err := FileSHA256(manifestPath)
	if err != nil {
		return PackageVerificationObservation{}, err
	}
	return PackageVerificationObservation{
		Schema: PackageVerificationSchema, ObservedAt: now.UTC(), Status: "passed",
		Mode: verification.Mode, Files: verification.Files, Package: pkg,
		PackageManifestSHA256: digest,
	}, nil
}

func (o PackageVerificationObservation) Validate(manifest packagekit.Manifest, pkg PackageIdentity, manifestDigest string) error {
	if o.Schema != PackageVerificationSchema || o.ObservedAt.IsZero() || o.Status != "passed" || o.Mode != "artifact" || o.Files <= 0 {
		return errors.New("package verification observation is incomplete")
	}
	if !o.Package.Equal(pkg) || o.PackageManifestSHA256 != manifestDigest || !IsSHA256(o.PackageManifestSHA256) {
		return errors.New("package verification observation identity does not match evidence bundle")
	}
	if o.Files != len(manifest.Files) {
		return errors.New("package verification observation file count does not match package manifest")
	}
	return packageManifestMatchesIdentity(manifest, pkg)
}

func (p RuntimeBuildProvenance) Validate(manifest packagekit.Manifest) error {
	if p.Schema != RuntimeBuildProvenanceSchema || p.Revision == "" || !IsCommit(p.Source.Commit) || p.Source.Dirty ||
		!IsSHA256(p.Source.SourceLockSHA256) || p.Output.Bytes <= 0 || !IsSHA256(p.Output.SHA256) ||
		p.StartedAt.IsZero() || p.CompletedAt.Before(p.StartedAt) {
		return errors.New("runtime build provenance is incomplete, dirty, or malformed")
	}
	if p.Revision != manifest.Runtime.Revision || p.Output.SHA256 != manifest.Runtime.ArtifactSHA256 {
		return errors.New("runtime build provenance does not match packaged runtime identity")
	}
	if filepath.Base(p.Output.File) != p.Output.File || strings.TrimSpace(p.Output.File) == "" {
		return errors.New("runtime build provenance output file must be a base name")
	}
	if p.Builder.ObservedIdentity == "" || p.Builder.ObservedIdentity != p.Builder.ExpectedIdentity || p.Builder.Attestation == "" {
		return errors.New("runtime build provenance builder identity is not independently bound")
	}
	return nil
}

func packageManifestMatchesIdentity(manifest packagekit.Manifest, pkg PackageIdentity) error {
	if manifest.Release.ProductVersion != pkg.ProductVersion || manifest.Source.Commit != pkg.SourceCommit || manifest.Source.Dirty ||
		manifest.Target.HostOS != pkg.HostOS || manifest.Target.HostArch != pkg.HostArch {
		return errors.New("package manifest does not match canonical package identity")
	}
	return nil
}
