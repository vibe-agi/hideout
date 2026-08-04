package packagekit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/vibe-agi/hideout/internal/helperbin"
)

var (
	fullCommitRE     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	sha256RE         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	productVersionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)
)

type VerifyResult struct {
	Root          string
	Mode          string
	Files         int
	Prerequisites []ExternalPrerequisiteStatus
}

type platformMismatchError struct {
	packageOS   string
	packageArch string
	hostOS      string
	hostArch    string
}

func (e *platformMismatchError) Error() string {
	return fmt.Sprintf("package target %s/%s does not match host %s/%s", e.packageOS, e.packageArch, e.hostOS, e.hostArch)
}

func IsPlatformMismatch(err error) bool {
	var mismatch *platformMismatchError
	return errors.As(err, &mismatch)
}

func Verify(root string) (VerifyResult, error) {
	cleanRoot, err := CleanRoot(root, "package root")
	if err != nil {
		return VerifyResult{}, err
	}
	if st, err := os.Stat(cleanRoot); err != nil {
		return VerifyResult{}, fmt.Errorf("stat package root: %w", err)
	} else if !st.IsDir() {
		return VerifyResult{}, fmt.Errorf("package root is not a directory: %s", cleanRoot)
	}
	artifactPath := filepath.Join(cleanRoot, "package-manifest.json")
	if _, err := os.Stat(artifactPath); err == nil {
		manifest, err := LoadManifest(artifactPath)
		if err != nil {
			return VerifyResult{}, err
		}
		if err := VerifyArtifact(cleanRoot, manifest); err != nil {
			return VerifyResult{}, err
		}
		return VerifyResult{Root: cleanRoot, Mode: "artifact", Files: len(manifest.Files), Prerequisites: ExternalPrerequisites(cleanRoot)}, nil
	}
	statePath := filepath.Join(cleanRoot, filepath.FromSlash(InstalledManifest))
	state, err := LoadInstallState(statePath)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("open installed package manifest: %w; hint: reinstall Hideout into %s", err, cleanRoot)
	}
	if err := VerifyInstalled(cleanRoot, state); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{Root: cleanRoot, Mode: "installed", Files: len(state.Files), Prerequisites: ExternalPrerequisites(cleanRoot)}, nil
}

// VerifyDistribution validates a package artifact independently of the host
// running the verifier. End-user package verification still enforces the host
// target; release promotion needs to inspect exact macOS bytes on a separate
// validation runner without pretending those bytes are runnable there.
func VerifyDistribution(root string) (VerifyResult, error) {
	cleanRoot, err := CleanRoot(root, "package root")
	if err != nil {
		return VerifyResult{}, err
	}
	if st, err := os.Stat(cleanRoot); err != nil {
		return VerifyResult{}, fmt.Errorf("stat package root: %w", err)
	} else if !st.IsDir() {
		return VerifyResult{}, fmt.Errorf("package root is not a directory: %s", cleanRoot)
	}
	manifest, err := LoadManifestForDistribution(filepath.Join(cleanRoot, "package-manifest.json"))
	if err != nil {
		return VerifyResult{}, err
	}
	if err := VerifyArtifact(cleanRoot, manifest); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{Root: cleanRoot, Mode: "artifact", Files: len(manifest.Files), Prerequisites: ExternalPrerequisites(cleanRoot)}, nil
}

func LoadManifest(path string) (Manifest, error) {
	return loadManifest(path, true)
}

func LoadManifestForDistribution(path string) (Manifest, error) {
	return loadManifest(path, false)
}

func loadManifest(path string, requireHostTarget bool) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open package-manifest.json: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse package-manifest.json: %w", err)
	}
	if err := validateManifest(manifest, requireHostTarget); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func LoadInstallState(path string) (InstallState, error) {
	f, err := os.Open(path)
	if err != nil {
		return InstallState{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var state InstallState
	if err := dec.Decode(&state); err != nil {
		return InstallState{}, fmt.Errorf("parse installed package manifest: %w", err)
	}
	if state.Schema != InstallStateSchema {
		return InstallState{}, fmt.Errorf("unsupported installed package schema %q", state.Schema)
	}
	if strings.TrimSpace(state.InstallPrefix) == "" {
		return InstallState{}, errors.New("installed package manifest installPrefix is required")
	}
	return state, nil
}

func VerifyArtifact(root string, manifest Manifest) error {
	if manifest.Layout.Root != DefaultPackageRoot {
		return fmt.Errorf("package manifest layout.root must be %s", DefaultPackageRoot)
	}
	if !containsString(manifest.Layout.Binaries, "bin/hideout") {
		return errors.New("package manifest layout.binaries must include bin/hideout")
	}
	if !containsString(manifest.Layout.Entrypoints, "install.sh") {
		return errors.New("package manifest layout.entrypoints must include install.sh")
	}
	if !containsString(manifest.Layout.Entrypoints, "README.md") {
		return errors.New("package manifest layout.entrypoints must include README.md")
	}
	if !containsString(manifest.Layout.Directories, "schemas") {
		return errors.New("package manifest layout.directories must include schemas")
	}
	observerRel := "bin/" + helperbin.LinuxObserverCommand + "-linux-" +
		manifest.Target.LinuxGuestArch
	if !containsString(manifest.Layout.Binaries, observerRel) {
		return fmt.Errorf(
			"package manifest layout.binaries must include %s",
			observerRel,
		)
	}
	if err := verifyLayout(root, manifest.Layout); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, file := range manifest.Files {
		if _, ok := seen[file.Path]; ok {
			return fmt.Errorf("package manifest contains duplicate file path %q", file.Path)
		}
		seen[file.Path] = struct{}{}
		if err := verifyFile(root, file); err != nil {
			return err
		}
	}
	for _, rel := range append(append([]string{}, manifest.Layout.Binaries...), manifest.Layout.Entrypoints...) {
		if _, ok := seen[rel]; !ok {
			return fmt.Errorf("package manifest layout path %q is not covered by file checksums", rel)
		}
	}
	for _, rel := range []string{
		"LICENSE",
		"LICENSES/GPL-2.0-only.txt",
		"THIRD_PARTY_NOTICES.md",
		"SECURITY.md",
		"README.md",
		"runtime/catalog.json",
		PackageComponentContractPath,
		BrowserConsoleManifestPath,
	} {
		if _, ok := seen[rel]; !ok {
			return fmt.Errorf("package manifest must checksum %q", rel)
		}
	}
	if err := verifyRequiredLinuxSessionHelpers(root, manifest.Target.LinuxGuestArch, manifest.Files); err != nil {
		return err
	}
	if err := verifyRequiredHostMigrationVZExecutor(root, manifest.Target, manifest.Files); err != nil {
		return err
	}
	if err := verifyPackageComponentContract(root, manifest.Files, false); err != nil {
		return err
	}
	if err := verifyEmbeddedBrowserConsole(
		root,
		manifest.Files,
		manifest.EmbeddedAssets,
		false,
	); err != nil {
		return err
	}
	return nil
}

func VerifyInstalled(prefix string, state InstallState) error {
	cleanPrefix, err := CleanRoot(prefix, "install prefix")
	if err != nil {
		return err
	}
	if err := verifyInstalledActive(cleanPrefix, state); err != nil {
		return err
	}
	if err := verifyRequiredLinuxSessionHelpers(cleanPrefix, state.Package.Target.LinuxGuestArch, state.Files); err != nil {
		return err
	}
	if err := verifyRequiredHostMigrationVZExecutor(
		cleanPrefix, state.Package.Target, state.Files,
	); err != nil {
		return err
	}
	if err := verifyPackageComponentContract(cleanPrefix, state.Files, true); err != nil {
		return err
	}
	if err := verifyEmbeddedBrowserConsole(
		cleanPrefix,
		state.Files,
		nil,
		true,
	); err != nil {
		return err
	}
	for _, stale := range state.ObsoleteFiles {
		joined, err := JoinRelative(cleanPrefix, stale.Path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(joined); err == nil {
			return fmt.Errorf("obsolete package-owned file %q remains from previous package; hint: run hideout package repair --prefix %s", stale.Path, cleanPrefix)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("obsolete package-owned file %q: %w", stale.Path, err)
		}
	}
	return nil
}

func verifyRequiredLinuxSessionHelpers(root, guestArch string, files []File) error {
	indexed := make(map[string]File, len(files))
	for _, file := range files {
		indexed[file.Path] = file
	}
	for _, command := range []string{helperbin.LinuxSessionSupervisorCommand, helperbin.LinuxWorkspacePortalCommand} {
		binaryRel := "bin/" + command + "-linux-" + guestArch
		manifestRel := binaryRel + ".manifest.json"
		binary, binaryOK := indexed[binaryRel]
		manifest, manifestOK := indexed[manifestRel]
		if !binaryOK || binary.Kind != "linux-helper" || !binary.Executable {
			return fmt.Errorf("package requires executable Linux helper %q", binaryRel)
		}
		if !manifestOK || manifest.Kind != "helper-manifest" || manifest.Executable {
			return fmt.Errorf("package requires helper manifest %q", manifestRel)
		}
		binaryPath, err := JoinRelative(root, binaryRel)
		if err != nil {
			return err
		}
		if !helperbin.StoreHelperCurrent(binaryPath, command, guestArch) {
			return fmt.Errorf("package Linux helper identity is invalid for %q", binaryRel)
		}
	}
	tunBinaryRel := "bin/" + helperbin.LinuxTun2SocksCommand + "-linux-" + guestArch
	tunManifestRel := tunBinaryRel + ".manifest.json"
	tunBinary, binaryOK := indexed[tunBinaryRel]
	tunManifest, manifestOK := indexed[tunManifestRel]
	if !binaryOK || tunBinary.Kind != "linux-helper" || !tunBinary.Executable {
		return fmt.Errorf("package requires executable Linux helper %q", tunBinaryRel)
	}
	if !manifestOK || tunManifest.Kind != "helper-manifest" || tunManifest.Executable {
		return fmt.Errorf("package requires helper manifest %q", tunManifestRel)
	}
	tunPath, err := JoinRelative(root, tunBinaryRel)
	if err != nil {
		return err
	}
	if _, ok := helperbin.Tun2SocksHelperCurrent(tunPath, guestArch, true); !ok {
		return fmt.Errorf("package Linux helper identity is invalid for %q", tunBinaryRel)
	}
	observerBinaryRel := "bin/" + helperbin.LinuxObserverCommand + "-linux-" + guestArch
	observerManifestRel := observerBinaryRel + ".manifest.json"
	observerBinary, observerBinaryOK := indexed[observerBinaryRel]
	observerManifest, observerManifestOK := indexed[observerManifestRel]
	if !observerBinaryOK ||
		observerBinary.Kind != "linux-helper" ||
		!observerBinary.Executable {
		return fmt.Errorf(
			"package requires executable Linux helper %q",
			observerBinaryRel,
		)
	}
	if !observerManifestOK ||
		observerManifest.Kind != "helper-manifest" ||
		observerManifest.Executable {
		return fmt.Errorf(
			"package requires helper manifest %q",
			observerManifestRel,
		)
	}
	observerPath, err := JoinRelative(root, observerBinaryRel)
	if err != nil {
		return err
	}
	if _, ok := helperbin.LinuxObserverHelperCurrent(observerPath, guestArch); !ok {
		return fmt.Errorf(
			"package Linux helper identity is invalid for %q",
			observerBinaryRel,
		)
	}
	migrationBinaryRel := "bin/" + helperbin.LinuxMigrationAdoptCommand + "-linux-" + guestArch
	migrationManifestRel := migrationBinaryRel + ".manifest.json"
	migrationBinary, migrationBinaryOK := indexed[migrationBinaryRel]
	migrationManifest, migrationManifestOK := indexed[migrationManifestRel]
	if !migrationBinaryOK || migrationBinary.Kind != "linux-helper" ||
		!migrationBinary.Executable {
		return fmt.Errorf(
			"package requires executable Linux helper %q",
			migrationBinaryRel,
		)
	}
	if !migrationManifestOK || migrationManifest.Kind != "helper-manifest" ||
		migrationManifest.Executable {
		return fmt.Errorf(
			"package requires helper manifest %q",
			migrationManifestRel,
		)
	}
	migrationPath, err := JoinRelative(root, migrationBinaryRel)
	if err != nil {
		return err
	}
	if _, ok := helperbin.LinuxMigrationAdoptHelperCurrent(
		migrationPath,
		guestArch,
	); !ok {
		return fmt.Errorf(
			"package Linux helper identity is invalid for %q",
			migrationBinaryRel,
		)
	}
	licenseFound := false
	for _, rel := range []string{
		"third_party/tun2socks/LICENSE",
		"share/hideout/third_party/tun2socks/LICENSE",
	} {
		if file, ok := indexed[rel]; ok && file.Kind == "doc" && !file.Executable {
			licenseFound = true
		}
	}
	if !licenseFound {
		return errors.New("package requires third-party tun2socks license")
	}
	observerLicenseFound := false
	for _, rel := range []string{
		"LICENSES/GPL-2.0-only.txt",
		"share/hideout/LICENSES/GPL-2.0-only.txt",
	} {
		if file, ok := indexed[rel]; ok && file.Kind == "doc" && !file.Executable {
			observerLicenseFound = true
		}
	}
	if !observerLicenseFound {
		return errors.New("package requires GPL-2.0-only text for embedded observer programs")
	}
	return nil
}

func verifyRequiredHostMigrationVZExecutor(
	root string,
	target Target,
	files []File,
) error {
	if target.HostOS != "darwin" || target.HostArch != "arm64" {
		return nil
	}
	indexed := make(map[string]File, len(files))
	for _, file := range files {
		indexed[file.Path] = file
	}
	binaryRel := "bin/" + helperbin.HostMigrationVZAdoptCommand + "-darwin-arm64"
	manifestRel := binaryRel + ".manifest.json"
	binary, binaryOK := indexed[binaryRel]
	manifest, manifestOK := indexed[manifestRel]
	if !binaryOK || binary.Kind != "binary" || !binary.Executable {
		return fmt.Errorf("package requires executable host helper %q", binaryRel)
	}
	if !manifestOK || manifest.Kind != "helper-manifest" || manifest.Executable {
		return fmt.Errorf("package requires host helper manifest %q", manifestRel)
	}
	binaryPath, err := JoinRelative(root, binaryRel)
	if err != nil {
		return err
	}
	if _, ok := helperbin.HostMigrationVZAdoptHelperCurrent(
		binaryPath, target.HostOS, target.HostArch,
	); !ok {
		return fmt.Errorf("package host helper identity is invalid for %q", binaryRel)
	}
	licenseFound := false
	for _, rel := range []string{
		"third_party/vz/LICENSE",
		"share/hideout/third_party/vz/LICENSE",
	} {
		if file, ok := indexed[rel]; ok && file.Kind == "doc" && !file.Executable {
			licenseFound = true
		}
	}
	if !licenseFound {
		return errors.New("package requires third-party Code-Hex/vz license")
	}
	return nil
}

func validateManifest(manifest Manifest, requireHostTarget bool) error {
	if manifest.Schema != ArtifactSchema {
		return fmt.Errorf("unsupported package manifest schema %q", manifest.Schema)
	}
	if strings.TrimSpace(manifest.BuiltAt) == "" {
		return errors.New("package manifest builtAt is required")
	}
	if !productVersionRE.MatchString(manifest.Release.ProductVersion) || manifest.Release.Tag != "v"+manifest.Release.ProductVersion {
		return errors.New("package manifest release productVersion/tag is invalid")
	}
	if manifest.Release.Channel != "alpha" && manifest.Release.Channel != "developer-preview" {
		return errors.New("package manifest release channel is invalid")
	}
	if manifest.Source.Repository != "https://github.com/vibe-agi/hideout" || !fullCommitRE.MatchString(manifest.Source.Commit) {
		return errors.New("package manifest source repository/full commit is invalid")
	}
	if manifest.Release.Channel == "alpha" && manifest.Source.Dirty {
		return errors.New("public alpha package source must be clean")
	}
	if strings.TrimSpace(manifest.Build.Workflow) == "" || strings.TrimSpace(manifest.Build.Ref) == "" {
		return errors.New("package manifest build workflow/ref is required")
	}
	if manifest.Runtime.Family == "" || manifest.Runtime.Revision == "" || !sha256RE.MatchString(manifest.Runtime.CatalogFileSHA256) || !sha256RE.MatchString(manifest.Runtime.ArtifactSHA256) {
		return errors.New("package manifest runtime identity is invalid")
	}
	if manifest.Release.Channel == "alpha" && manifest.SigningSummary.Mode != "developer-id-observed" {
		return errors.New("public alpha package requires developer-id-observed signing mode")
	}
	if strings.TrimSpace(manifest.Target.HostOS) == "" || strings.TrimSpace(manifest.Target.HostArch) == "" || strings.TrimSpace(manifest.Target.LinuxGuestArch) == "" {
		return errors.New("package manifest target hostOS, hostArch, and linuxGuestArch are required")
	}
	if requireHostTarget && (manifest.Target.HostOS != runtime.GOOS || manifest.Target.HostArch != runtime.GOARCH) {
		return &platformMismatchError{
			packageOS: manifest.Target.HostOS, packageArch: manifest.Target.HostArch,
			hostOS: runtime.GOOS, hostArch: runtime.GOARCH,
		}
	}
	if len(manifest.Files) == 0 {
		return errors.New("package manifest has no files")
	}
	if err := validateEmbeddedAssetBindings(manifest.EmbeddedAssets); err != nil {
		return err
	}
	if manifest.Migration.InstallStateSchema == "" {
		return errors.New("package manifest migration.installStateSchema is required")
	}
	if len(manifest.Migration.FromInstalledSchemas) == 0 {
		return errors.New("package manifest migration.fromInstalledSchemas is required")
	}
	if strings.TrimSpace(manifest.Migration.MinimumPackageSchema) == "" || strings.TrimSpace(manifest.Migration.MaximumPackageSchema) == "" {
		return errors.New("package manifest migration minimumPackageSchema and maximumPackageSchema are required")
	}
	return nil
}

func verifyLayout(root string, layout Layout) error {
	for _, rel := range layout.Binaries {
		if err := requirePath(root, rel, true, true); err != nil {
			return fmt.Errorf("package manifest binary %q: %w", rel, err)
		}
	}
	for _, rel := range layout.Entrypoints {
		if err := requirePath(root, rel, false, true); err != nil {
			return fmt.Errorf("package manifest entrypoint %q: %w", rel, err)
		}
	}
	for _, rel := range layout.Directories {
		joined, err := JoinRelative(root, rel)
		if err != nil {
			return fmt.Errorf("package manifest directory path %q: %w", rel, err)
		}
		st, err := os.Lstat(joined)
		if err != nil {
			return fmt.Errorf("package manifest directory %q: %w", rel, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("package manifest directory %q must not be a symlink", rel)
		}
		if !st.IsDir() {
			return fmt.Errorf("package manifest directory %q is not a directory", rel)
		}
	}
	return nil
}

func verifyFile(root string, file File) error {
	if !isSupportedKind(file.Kind) {
		return fmt.Errorf("package manifest file %q has unsupported kind %q", file.Path, file.Kind)
	}
	if err := requirePath(root, file.Path, file.Executable, true); err != nil {
		return fmt.Errorf("package manifest file %q: %w", file.Path, err)
	}
	joined, _ := JoinRelative(root, file.Path)
	got, err := FileSHA256(joined)
	if err != nil {
		return fmt.Errorf("read package manifest file %q: %w", file.Path, err)
	}
	if len(file.SHA256) != 64 {
		return fmt.Errorf("package manifest file %q has invalid sha256", file.Path)
	}
	if got != file.SHA256 {
		return fmt.Errorf("package checksum mismatch for %s: want %s got %s; hint: rebuild or reinstall package", file.Path, file.SHA256, got)
	}
	return nil
}

func verifyInstalledActive(prefix string, state InstallState) error {
	cleanPrefix, err := CleanRoot(prefix, "install prefix")
	if err != nil {
		return err
	}
	recorded, err := filepath.Abs(state.InstallPrefix)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(recorded); err == nil {
		recorded = resolved
	}
	if recorded != cleanPrefix {
		return fmt.Errorf("installed package prefix mismatch: manifest=%s actual=%s; hint: reinstall Hideout instead of relocating package files", recorded, cleanPrefix)
	}
	for _, rel := range state.Directories {
		joined, err := JoinRelative(cleanPrefix, rel)
		if err != nil {
			return fmt.Errorf("installed package directory path %q: %w", rel, err)
		}
		st, err := os.Lstat(joined)
		if err != nil {
			return fmt.Errorf("installed package directory %q: %w", rel, err)
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("installed package directory %q must be a real directory", rel)
		}
	}
	for _, file := range state.Files {
		if err := verifyFile(cleanPrefix, file); err != nil {
			return err
		}
	}
	return nil
}

func requirePath(root, rel string, executable bool, regular bool) error {
	joined, err := JoinRelative(root, rel)
	if err != nil {
		return err
	}
	st, err := os.Lstat(joined)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("must not be a symlink")
	}
	if regular && !st.Mode().IsRegular() {
		return errors.New("is not a regular file")
	}
	if executable && st.Mode()&0o111 == 0 {
		return errors.New("is not executable")
	}
	return nil
}

func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func isSupportedKind(kind string) bool {
	return slices.Contains([]string{
		"binary",
		"linux-helper",
		"helper-manifest",
		"installer",
		"entrypoint",
		"schema",
		"doc",
		"script",
		"packaging",
		"host-app-core-data",
		"host-app-example",
		"runtime-catalog",
		"runtime-contract",
		"runtime-build",
		"embedded-asset-manifest",
	}, kind)
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
