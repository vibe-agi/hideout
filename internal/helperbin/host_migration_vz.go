package helperbin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	HostMigrationVZAdoptCommand         = "hideout-migration-vz-adopt"
	HostMigrationVZAdoptLicense         = "Apache-2.0"
	HostMigrationVZAdoptBuildMode       = "apple-vz-zero-network-adoption-v1"
	HostMigrationVZAdoptUpstreamModule  = "github.com/Code-Hex/vz/v3"
	HostMigrationVZAdoptUpstreamVersion = "v3.7.1"
)

type HostMigrationVZAdoptResolution struct {
	Path           string
	ExpectedDigest string
	Manifest       Manifest
}

func DefaultHostMigrationVZAdoptPath(root, hostOS, hostArch string) string {
	if hostOS == "" {
		hostOS = "unknown"
	}
	if hostArch == "" {
		hostArch = "unknown"
	}
	return filepath.Join(
		root, "bin", HostMigrationVZAdoptCommand+"-"+hostOS+"-"+hostArch,
	)
}

// ResolveHostMigrationVZAdopt accepts only a package-owned executable whose
// exact bytes and pinned VZ dependency are bound by its provenance manifest.
// An explicit candidate fails closed; an absent adjacent packaged candidate is
// reported as unavailable so capability discovery can retain config-only mode.
func ResolveHostMigrationVZAdopt(
	candidate, hostOS, hostArch string,
) (HostMigrationVZAdoptResolution, error) {
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	if hostArch == "" {
		hostArch = runtime.GOARCH
	}
	if hostOS != "darwin" || hostArch != "arm64" {
		return HostMigrationVZAdoptResolution{}, fmt.Errorf(
			"unsupported migration VZ executor platform %s/%s", hostOS, hostArch,
		)
	}
	explicit := strings.TrimSpace(candidate) != ""
	if !explicit {
		executable, err := os.Executable()
		if err != nil {
			return HostMigrationVZAdoptResolution{}, nil
		}
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		candidate = filepath.Join(
			filepath.Dir(executable),
			HostMigrationVZAdoptCommand+"-"+hostOS+"-"+hostArch,
		)
	}
	if !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate {
		return HostMigrationVZAdoptResolution{}, errors.New(
			"migration VZ executor path must be clean and absolute",
		)
	}
	manifest, ok := HostMigrationVZAdoptHelperCurrent(candidate, hostOS, hostArch)
	if !ok {
		if explicit {
			return HostMigrationVZAdoptResolution{}, errors.New(
				"configured migration VZ executor is not the current packaged artifact",
			)
		}
		return HostMigrationVZAdoptResolution{}, nil
	}
	return HostMigrationVZAdoptResolution{
		Path: candidate, ExpectedDigest: "sha256:" + manifest.SHA256,
		Manifest: manifest,
	}, nil
}

func HostMigrationVZAdoptHelperCurrent(
	binaryPath, hostOS, hostArch string,
) (Manifest, bool) {
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	if hostArch == "" {
		hostArch = runtime.GOARCH
	}
	if hostOS != "darwin" || hostArch != "arm64" {
		return Manifest{}, false
	}
	info, err := os.Lstat(binaryPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return Manifest{}, false
	}
	manifestInfo, err := os.Lstat(ManifestPath(binaryPath))
	if err != nil || manifestInfo.Mode()&os.ModeSymlink != 0 ||
		!manifestInfo.Mode().IsRegular() || manifestInfo.Mode().Perm()&0o022 != 0 {
		return Manifest{}, false
	}
	manifest, err := ReadManifest(ManifestPath(binaryPath))
	if err != nil || manifest.Version != ManifestVersion ||
		manifest.Command != HostMigrationVZAdoptCommand ||
		manifest.TargetOS != hostOS || manifest.TargetArch != hostArch ||
		manifest.Artifact != filepath.Base(binaryPath) ||
		manifest.Builder != "go build -mod=readonly -trimpath" ||
		manifest.License != HostMigrationVZAdoptLicense ||
		manifest.BuildMode != HostMigrationVZAdoptBuildMode ||
		manifest.UpstreamModule != HostMigrationVZAdoptUpstreamModule ||
		manifest.UpstreamVersion != HostMigrationVZAdoptUpstreamVersion ||
		!manifest.PackageOwned {
		return Manifest{}, false
	}
	if _, err := time.Parse(time.RFC3339, manifest.BuiltAt); err != nil {
		return Manifest{}, false
	}
	sum, err := FileSHA256(binaryPath)
	if err != nil || sum != manifest.SHA256 {
		return Manifest{}, false
	}
	return manifest, true
}

func WriteHostMigrationVZAdoptManifest(
	binaryPath, hostOS, hostArch string,
) error {
	if hostOS != "darwin" || hostArch != "arm64" {
		return fmt.Errorf(
			"unsupported migration VZ executor target %s/%s", hostOS, hostArch,
		)
	}
	sum, err := FileSHA256(binaryPath)
	if err != nil {
		return err
	}
	builtAt, err := helperManifestBuildTimestamp()
	if err != nil {
		return err
	}
	return writeManifest(binaryPath, Manifest{
		Version: ManifestVersion, Command: HostMigrationVZAdoptCommand,
		TargetOS: hostOS, TargetArch: hostArch,
		Artifact: filepath.Base(binaryPath), SHA256: sum,
		Builder: "go build -mod=readonly -trimpath", BuiltAt: builtAt,
		UpstreamModule:  HostMigrationVZAdoptUpstreamModule,
		UpstreamVersion: HostMigrationVZAdoptUpstreamVersion,
		License:         HostMigrationVZAdoptLicense, BuildMode: HostMigrationVZAdoptBuildMode,
		PackageOwned: true,
	})
}

func BuildHostMigrationVZAdopt(opts BuildOptions) error {
	if runtime.GOOS != "darwin" || opts.GOARCH != "arm64" {
		return fmt.Errorf(
			"migration VZ executor build requires a Darwin host and arm64 target",
		)
	}
	if strings.TrimSpace(opts.Out) == "" {
		return errors.New("migration VZ executor output path is required")
	}
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return err
	}
	if ok, err := commandSourceExists(filepath.Join(source, "cmd", HostMigrationVZAdoptCommand)); err != nil || !ok {
		if err == nil {
			err = errors.New("no Go source files")
		}
		return fmt.Errorf("migration VZ executor source is unavailable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Out), 0o700); err != nil {
		return err
	}
	command := exec.Command(
		"go", "build", "-mod=readonly", "-trimpath", "-o", opts.Out,
		"./cmd/"+HostMigrationVZAdoptCommand,
	)
	command.Dir = source
	command.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1")
	data, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"build migration VZ executor: %w\n%s", err, strings.TrimSpace(string(data)),
		)
	}
	if err := os.Chmod(opts.Out, 0o700); err != nil {
		return err
	}
	return WriteHostMigrationVZAdoptManifest(opts.Out, "darwin", "arm64")
}
