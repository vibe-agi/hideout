package helperbin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	LinuxMigrationAdoptCommand         = "hideout-migration-adopt"
	LinuxMigrationAdoptPathEnvironment = "HIDEOUT_LINUX_MIGRATION_ADOPT_PATH"
	LinuxMigrationAdoptLicense         = "Apache-2.0"
	LinuxMigrationAdoptBuildMode       = "strict-data-only-adoption-v1"
)

type LinuxMigrationAdoptResolution struct {
	Path           string
	ExpectedDigest string
	Manifest       Manifest
}

func DefaultLinuxMigrationAdoptPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(
		storeRoot,
		"bin",
		LinuxMigrationAdoptCommand+"-linux-"+goarch,
	)
}

// ResolveLinuxMigrationAdopt accepts only the architecture-specific,
// package-owned helper whose bytes match its strict provenance manifest. An
// unsuffixed host binary is never a fallback for this guest trust boundary.
func ResolveLinuxMigrationAdopt(
	storeRoot,
	goarch string,
) (LinuxMigrationAdoptResolution, error) {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !SupportedLinuxGuestArch(goarch) {
		return LinuxMigrationAdoptResolution{}, fmt.Errorf(
			"unsupported Linux migration adoption architecture %q",
			goarch,
		)
	}
	resolve := func(candidate string) (LinuxMigrationAdoptResolution, bool) {
		manifest, ok := LinuxMigrationAdoptHelperCurrent(candidate, goarch)
		if !ok {
			return LinuxMigrationAdoptResolution{}, false
		}
		return LinuxMigrationAdoptResolution{
			Path: candidate, ExpectedDigest: "sha256:" + manifest.SHA256,
			Manifest: manifest,
		}, true
	}
	if path := os.Getenv(LinuxMigrationAdoptPathEnvironment); path != "" {
		if resolution, ok := resolve(path); ok {
			return resolution, nil
		}
		return LinuxMigrationAdoptResolution{}, fmt.Errorf(
			"%s does not identify a current packaged adoption helper",
			LinuxMigrationAdoptPathEnvironment,
		)
	}
	name := LinuxMigrationAdoptCommand + "-linux-" + goarch
	if executable, err := os.Executable(); err == nil {
		if resolution, ok := resolve(filepath.Join(filepath.Dir(executable), name)); ok {
			return resolution, nil
		}
	}
	if strings.TrimSpace(storeRoot) != "" {
		if resolution, ok := resolve(DefaultLinuxMigrationAdoptPath(storeRoot, goarch)); ok {
			return resolution, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		if resolution, ok := resolve(path); ok {
			return resolution, nil
		}
	}
	return LinuxMigrationAdoptResolution{}, nil
}

func ResolveLinuxMigrationAdoptPath(storeRoot, goarch string) string {
	resolution, err := ResolveLinuxMigrationAdopt(storeRoot, goarch)
	if err != nil {
		return ""
	}
	return resolution.Path
}

func BuildLinuxMigrationAdopt(opts BuildOptions) error {
	if !SupportedLinuxGuestArch(opts.GOARCH) {
		return fmt.Errorf(
			"unsupported Linux migration adoption architecture %q",
			opts.GOARCH,
		)
	}
	opts.Command = LinuxMigrationAdoptCommand
	if err := BuildLinuxCommand(opts); err != nil {
		return err
	}
	return WriteLinuxMigrationAdoptManifest(opts.Out, opts.GOARCH)
}

func WriteLinuxMigrationAdoptManifest(binaryPath, goarch string) error {
	if goarch == "" {
		goarch = runtime.GOARCH
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
		Version: ManifestVersion, Command: LinuxMigrationAdoptCommand,
		TargetOS: "linux", TargetArch: goarch,
		Artifact: filepath.Base(binaryPath), SHA256: sum,
		Builder: "go build -trimpath", BuiltAt: builtAt,
		License: LinuxMigrationAdoptLicense, BuildMode: LinuxMigrationAdoptBuildMode,
		PackageOwned: true,
	})
}

func LinuxMigrationAdoptHelperCurrent(
	binaryPath,
	goarch string,
) (Manifest, bool) {
	if goarch == "" {
		goarch = runtime.GOARCH
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
		manifest.Command != LinuxMigrationAdoptCommand ||
		manifest.TargetOS != "linux" || manifest.TargetArch != goarch ||
		manifest.Artifact != filepath.Base(binaryPath) ||
		manifest.Builder != "go build -trimpath" ||
		manifest.License != LinuxMigrationAdoptLicense ||
		manifest.BuildMode != LinuxMigrationAdoptBuildMode ||
		!manifest.PackageOwned || manifest.UpstreamModule != "" ||
		manifest.UpstreamVersion != "" {
		return Manifest{}, false
	}
	if _, err := time.Parse(time.RFC3339, manifest.BuiltAt); err != nil {
		return Manifest{}, false
	}
	sum, err := FileSHA256(binaryPath)
	if err != nil || manifest.SHA256 != sum {
		return Manifest{}, false
	}
	return manifest, true
}
