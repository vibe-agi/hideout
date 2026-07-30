package helperbin

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	ManifestVersion                       = "hideout.helper-manifest/v1"
	LinuxTun2SocksCommand                 = "tun2socks"
	LinuxTun2SocksPathEnvironment         = "HIDEOUT_LINUX_TUN2SOCKS_PATH"
	Tun2SocksUpstreamModule               = "github.com/xjasonlyu/tun2socks/v2"
	Tun2SocksUpstreamVersion              = "v2.6.0"
	Tun2SocksLicense                      = "MIT"
	Tun2SocksBuildMode                    = "source-built-pinned-module"
	Tun2SocksSourceOverride               = "explicit-override"
	Tun2SocksSourcePackage                = "package-owned"
	Tun2SocksSourceStore                  = "store-manifest"
	LinuxSessionSupervisorCommand         = "hideout-session-supervisor"
	LinuxSessionSupervisorPathEnvironment = "HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH"
	LinuxObserverCommand                  = "hideout-observer"
	LinuxObserverPathEnvironment          = "HIDEOUT_LINUX_OBSERVER_PATH"
	LinuxObserverLicense                  = "Apache-2.0"
	LinuxObserverBuildMode                = "embedded-core-bpf"
	LinuxWorkspacePortalCommand           = "hideout-workspace-portal"
	LinuxWorkspacePortalPathEnvironment   = "HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH"
)

var ErrPackagedTun2SocksUnavailable = errors.New("packaged tun2socks helper is unavailable")

type BuildOptions struct {
	Out     string
	GOARCH  string
	Source  string
	Command string
}

type Manifest struct {
	Version         string `json:"version"`
	Command         string `json:"command"`
	TargetOS        string `json:"targetOS"`
	TargetArch      string `json:"targetArch"`
	Artifact        string `json:"artifact"`
	SHA256          string `json:"sha256"`
	Builder         string `json:"builder"`
	BuiltAt         string `json:"builtAt"`
	UpstreamModule  string `json:"upstreamModule,omitempty"`
	UpstreamVersion string `json:"upstreamVersion,omitempty"`
	License         string `json:"license,omitempty"`
	BuildMode       string `json:"buildMode,omitempty"`
	PackageOwned    bool   `json:"packageOwned,omitempty"`
}

type Tun2SocksResolveOptions struct {
	Executable string
	StoreRoot  string
	GOARCH     string
	Override   string
	AllowStore bool
}

type Tun2SocksResolution struct {
	Path           string
	Source         string
	ExpectedSHA256 string
	Manifest       Manifest
}

type LinuxObserverResolution struct {
	Path           string
	ExpectedDigest string
	Manifest       Manifest
}

func DefaultLinuxShimPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(storeRoot, "bin", "hideout-shim-linux-"+goarch)
}

func DefaultLinuxHostFSDPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(storeRoot, "bin", "hideout-hostfsd-linux-"+goarch)
}

func DefaultLinuxTun2SocksPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(storeRoot, "bin", LinuxTun2SocksCommand+"-linux-"+goarch)
}

func DefaultLinuxSessionSupervisorPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(storeRoot, "bin", LinuxSessionSupervisorCommand+"-linux-"+goarch)
}

func DefaultLinuxObserverPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(storeRoot, "bin", LinuxObserverCommand+"-linux-"+goarch)
}

func DefaultLinuxWorkspacePortalPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = "unknown"
	}
	return filepath.Join(storeRoot, "bin", LinuxWorkspacePortalCommand+"-linux-"+goarch)
}

func ResolveShimPath() string {
	if shimPath := os.Getenv("HIDEOUT_SHIM_PATH"); shimPath != "" {
		if FileExists(shimPath) {
			return shimPath
		}
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "hideout-shim")
		if FileExists(candidate) {
			return candidate
		}
	}
	if path, err := exec.LookPath("hideout-shim"); err == nil {
		return path
	}
	return ""
}

func ResolveLinuxShimPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if shimPath := os.Getenv("HIDEOUT_LINUX_SHIM_PATH"); shimPath != "" {
		if FileExists(shimPath) {
			return shimPath
		}
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"hideout-shim-linux-" + goarch, "hideout-shim-linux"} {
			candidate := filepath.Join(filepath.Dir(exe), name)
			if FileExists(candidate) {
				return candidate
			}
		}
	}
	if storeRoot != "" {
		if candidate := DefaultLinuxShimPath(storeRoot, goarch); StoreHelperCurrent(candidate, "hideout-shim", goarch) {
			return candidate
		}
	}
	for _, name := range []string{"hideout-shim-linux-" + goarch, "hideout-shim-linux"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func ResolveLinuxTun2SocksPath(goarch string) string {
	storeRoot := ""
	resolution, err := ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
		StoreRoot: storeRoot, GOARCH: goarch,
		Override: os.Getenv(LinuxTun2SocksPathEnvironment),
	})
	if err != nil {
		return ""
	}
	return resolution.Path
}

func ResolveLinuxTun2Socks(opts Tun2SocksResolveOptions) (Tun2SocksResolution, error) {
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !SupportedLinuxGuestArch(goarch) {
		return Tun2SocksResolution{}, fmt.Errorf("unsupported Linux tun2socks architecture %q", goarch)
	}
	if strings.TrimSpace(opts.Override) != "" {
		path := opts.Override
		digest, err := validateExplicitTun2Socks(path, goarch)
		if err != nil {
			return Tun2SocksResolution{}, fmt.Errorf("invalid explicit %s: %w", LinuxTun2SocksPathEnvironment, err)
		}
		manifest, _ := ReadManifest(ManifestPath(path))
		return Tun2SocksResolution{
			Path: path, Source: Tun2SocksSourceOverride, ExpectedSHA256: digest, Manifest: manifest,
		}, nil
	}
	executable := strings.TrimSpace(opts.Executable)
	if executable == "" {
		executable, _ = os.Executable()
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	if executable != "" {
		candidate := filepath.Join(filepath.Dir(executable), LinuxTun2SocksCommand+"-linux-"+goarch)
		if manifest, ok := Tun2SocksHelperCurrent(candidate, goarch, true); ok {
			return Tun2SocksResolution{
				Path: candidate, Source: Tun2SocksSourcePackage, ExpectedSHA256: manifest.SHA256, Manifest: manifest,
			}, nil
		}
		if packagedExecutable(executable) {
			return Tun2SocksResolution{}, fmt.Errorf("%w: %s", ErrPackagedTun2SocksUnavailable, candidate)
		}
	}
	if opts.AllowStore && strings.TrimSpace(opts.StoreRoot) != "" {
		candidate := DefaultLinuxTun2SocksPath(opts.StoreRoot, goarch)
		if manifest, ok := Tun2SocksHelperCurrent(candidate, goarch, false); ok {
			return Tun2SocksResolution{
				Path: candidate, Source: Tun2SocksSourceStore, ExpectedSHA256: manifest.SHA256, Manifest: manifest,
			}, nil
		}
	}
	return Tun2SocksResolution{}, nil
}

func packagedExecutable(executable string) bool {
	binDir := filepath.Dir(executable)
	for _, marker := range []string{
		filepath.Join(binDir, "..", "package-manifest.json"),
		filepath.Join(binDir, "..", "share", "hideout", "package-manifest.json"),
	} {
		_, err := os.Lstat(filepath.Clean(marker))
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

// ResolveLinuxDNSStubPath locates the prebuilt guest DNS stub (DoH resolver)
// binary, mirroring ResolveLinuxTun2SocksPath: HIDEOUT_LINUX_DNS_STUB_PATH, then
// a binary next to the current executable, then PATH.
func ResolveLinuxDNSStubPath(goarch string) string {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if path := os.Getenv("HIDEOUT_LINUX_DNS_STUB_PATH"); path != "" {
		if FileExists(path) {
			return path
		}
		return ""
	}
	names := []string{"hideout-dns-stub-linux-" + goarch, "hideout-dns-stub-linux"}
	if exe, err := os.Executable(); err == nil {
		for _, name := range names {
			candidate := filepath.Join(filepath.Dir(exe), name)
			if FileExists(candidate) {
				return candidate
			}
		}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func ResolveLinuxHostFSDPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if path := os.Getenv("HIDEOUT_LINUX_HOSTFSD_PATH"); path != "" {
		if FileExists(path) {
			return path
		}
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"hideout-hostfsd-linux-" + goarch, "hideout-hostfsd-linux"} {
			candidate := filepath.Join(filepath.Dir(exe), name)
			if FileExists(candidate) {
				return candidate
			}
		}
	}
	if storeRoot != "" {
		if candidate := DefaultLinuxHostFSDPath(storeRoot, goarch); StoreHelperCurrent(candidate, "hideout-hostfsd", goarch) {
			return candidate
		}
	}
	for _, name := range []string{"hideout-hostfsd-linux-" + goarch, "hideout-hostfsd-linux"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// ResolveLinuxSessionSupervisorPath locates only an architecture-specific,
// manifest-verified Linux guest helper. It never falls back to a host binary.
func ResolveLinuxSessionSupervisorPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !SupportedLinuxGuestArch(goarch) {
		return ""
	}
	if path := os.Getenv(LinuxSessionSupervisorPathEnvironment); path != "" {
		if StoreHelperCurrent(path, LinuxSessionSupervisorCommand, goarch) {
			return path
		}
		return ""
	}
	name := LinuxSessionSupervisorCommand + "-linux-" + goarch
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if StoreHelperCurrent(candidate, LinuxSessionSupervisorCommand, goarch) {
			return candidate
		}
	}
	if storeRoot != "" {
		candidate := DefaultLinuxSessionSupervisorPath(storeRoot, goarch)
		if StoreHelperCurrent(candidate, LinuxSessionSupervisorCommand, goarch) {
			return candidate
		}
	}
	if path, err := exec.LookPath(name); err == nil && StoreHelperCurrent(path, LinuxSessionSupervisorCommand, goarch) {
		return path
	}
	return ""
}

// ResolveLinuxObserver returns both the manifest-bound executable and the
// digest the daemon must put into supervisor start authority. An explicit path
// is never accepted without the same exact manifest identity as a packaged
// helper.
func ResolveLinuxObserver(
	storeRoot,
	goarch string,
) (LinuxObserverResolution, error) {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !SupportedLinuxGuestArch(goarch) {
		return LinuxObserverResolution{}, fmt.Errorf(
			"unsupported Linux observer architecture %q",
			goarch,
		)
	}
	resolve := func(candidate string) (LinuxObserverResolution, bool) {
		manifest, ok := LinuxObserverHelperCurrent(candidate, goarch)
		if !ok {
			return LinuxObserverResolution{}, false
		}
		return LinuxObserverResolution{
			Path: candidate, ExpectedDigest: "sha256:" + manifest.SHA256,
			Manifest: manifest,
		}, true
	}
	if path := os.Getenv(LinuxObserverPathEnvironment); path != "" {
		if resolution, ok := resolve(path); ok {
			return resolution, nil
		}
		return LinuxObserverResolution{}, fmt.Errorf(
			"%s does not identify a current packaged observer",
			LinuxObserverPathEnvironment,
		)
	}
	name := LinuxObserverCommand + "-linux-" + goarch
	if executable, err := os.Executable(); err == nil {
		if resolution, ok := resolve(filepath.Join(filepath.Dir(executable), name)); ok {
			return resolution, nil
		}
	}
	if storeRoot != "" {
		if resolution, ok := resolve(DefaultLinuxObserverPath(storeRoot, goarch)); ok {
			return resolution, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		if resolution, ok := resolve(path); ok {
			return resolution, nil
		}
	}
	return LinuxObserverResolution{}, nil
}

func ResolveLinuxObserverPath(storeRoot, goarch string) string {
	resolution, err := ResolveLinuxObserver(storeRoot, goarch)
	if err != nil {
		return ""
	}
	return resolution.Path
}

// ResolveLinuxWorkspacePortalPath accepts only a manifest-bound Linux helper.
// The research probe binary is deliberately not a fallback product helper.
func ResolveLinuxWorkspacePortalPath(storeRoot, goarch string) string {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !SupportedLinuxGuestArch(goarch) {
		return ""
	}
	if path := os.Getenv(LinuxWorkspacePortalPathEnvironment); path != "" {
		if StoreHelperCurrent(path, LinuxWorkspacePortalCommand, goarch) {
			return path
		}
		return ""
	}
	name := LinuxWorkspacePortalCommand + "-linux-" + goarch
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if StoreHelperCurrent(candidate, LinuxWorkspacePortalCommand, goarch) {
			return candidate
		}
	}
	if storeRoot != "" {
		candidate := DefaultLinuxWorkspacePortalPath(storeRoot, goarch)
		if StoreHelperCurrent(candidate, LinuxWorkspacePortalCommand, goarch) {
			return candidate
		}
	}
	if path, err := exec.LookPath(name); err == nil && StoreHelperCurrent(path, LinuxWorkspacePortalCommand, goarch) {
		return path
	}
	return ""
}

func BuildLinuxSessionSupervisor(opts BuildOptions) error {
	if !SupportedLinuxGuestArch(opts.GOARCH) {
		return fmt.Errorf("unsupported Linux guest supervisor architecture %q", opts.GOARCH)
	}
	opts.Command = LinuxSessionSupervisorCommand
	return BuildLinuxCommand(opts)
}

func BuildLinuxObserver(opts BuildOptions) error {
	if !SupportedLinuxGuestArch(opts.GOARCH) {
		return fmt.Errorf("unsupported Linux observer architecture %q", opts.GOARCH)
	}
	opts.Command = LinuxObserverCommand
	if err := BuildLinuxCommand(opts); err != nil {
		return err
	}
	return WriteLinuxObserverManifest(opts.Out, opts.GOARCH)
}

func BuildLinuxWorkspacePortal(opts BuildOptions) error {
	if !SupportedLinuxGuestArch(opts.GOARCH) {
		return fmt.Errorf("unsupported Linux workspace portal architecture %q", opts.GOARCH)
	}
	opts.Command = LinuxWorkspacePortalCommand
	return BuildLinuxCommand(opts)
}

func BuildLinuxTun2Socks(opts BuildOptions) error {
	if !SupportedLinuxGuestArch(opts.GOARCH) {
		return fmt.Errorf("unsupported Linux tun2socks architecture %q", opts.GOARCH)
	}
	if strings.TrimSpace(opts.Out) == "" {
		return errors.New("linux tun2socks output path is required")
	}
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return err
	}
	moduleDir := filepath.Join(source, "tools", "tun2socks-build")
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return fmt.Errorf("pinned tun2socks build module is unavailable: %w", err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "go.sum")); err != nil {
		return fmt.Errorf("pinned tun2socks build sums are unavailable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Out), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", opts.Out, Tun2SocksUpstreamModule)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH="+opts.GOARCH,
		"CGO_ENABLED=0",
	)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build pinned Linux tun2socks: %w\n%s", err, strings.TrimSpace(string(data)))
	}
	if err := os.Chmod(opts.Out, 0o700); err != nil {
		return err
	}
	return WriteTun2SocksManifest(opts.Out, opts.GOARCH, true)
}

func SupportedLinuxGuestArch(goarch string) bool {
	return goarch == "arm64" || goarch == "amd64"
}

func BuildLinuxCommand(opts BuildOptions) error {
	if strings.TrimSpace(opts.Command) == "" {
		return errors.New("helper command is required")
	}
	if strings.TrimSpace(opts.GOARCH) == "" {
		return errors.New("linux helper GOARCH is required")
	}
	if strings.TrimSpace(opts.Out) == "" {
		return errors.New("linux helper output path is required")
	}
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return err
	}
	pkg := filepath.Join(source, "cmd", opts.Command)
	if ok, err := commandSourceExists(pkg); err != nil || !ok {
		if err == nil {
			err = errors.New("no Go source files")
		}
		return fmt.Errorf("%s source not found under %s: %w", opts.Command, pkg, err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Out), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", opts.Out, "./cmd/"+opts.Command)
	cmd.Dir = source
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH="+opts.GOARCH,
		"CGO_ENABLED=0",
	)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build linux %s: %w\n%s", opts.Command, err, strings.TrimSpace(string(data)))
	}
	if err := os.Chmod(opts.Out, 0o700); err != nil {
		return err
	}
	return WriteStoreHelperManifest(opts.Out, opts.Command, opts.GOARCH)
}

func ManifestPath(binaryPath string) string {
	return binaryPath + ".manifest.json"
}

func StoreHelperCurrent(binaryPath, command, goarch string) bool {
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !FileExists(binaryPath) {
		return false
	}
	manifest, err := ReadManifest(ManifestPath(binaryPath))
	if err != nil {
		return false
	}
	if manifest.Version != ManifestVersion ||
		manifest.Command != command ||
		manifest.TargetOS != "linux" ||
		manifest.TargetArch != goarch ||
		manifest.Artifact != filepath.Base(binaryPath) {
		return false
	}
	sum, err := FileSHA256(binaryPath)
	if err != nil {
		return false
	}
	return manifest.SHA256 == sum
}

func WriteStoreHelperManifest(binaryPath, command, goarch string) error {
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
	manifest := Manifest{
		Version:    ManifestVersion,
		Command:    command,
		TargetOS:   "linux",
		TargetArch: goarch,
		Artifact:   filepath.Base(binaryPath),
		SHA256:     sum,
		Builder:    "go build",
		BuiltAt:    builtAt,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := ManifestPath(binaryPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func WriteTun2SocksManifest(binaryPath, goarch string, packageOwned bool) error {
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
		Version:         ManifestVersion,
		Command:         LinuxTun2SocksCommand,
		TargetOS:        "linux",
		TargetArch:      goarch,
		Artifact:        filepath.Base(binaryPath),
		SHA256:          sum,
		Builder:         "go build -mod=readonly",
		BuiltAt:         builtAt,
		UpstreamModule:  Tun2SocksUpstreamModule,
		UpstreamVersion: Tun2SocksUpstreamVersion,
		License:         Tun2SocksLicense,
		BuildMode:       Tun2SocksBuildMode,
		PackageOwned:    packageOwned,
	})
}

func WriteLinuxObserverManifest(binaryPath, goarch string) error {
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
		Version:      ManifestVersion,
		Command:      LinuxObserverCommand,
		TargetOS:     "linux",
		TargetArch:   goarch,
		Artifact:     filepath.Base(binaryPath),
		SHA256:       sum,
		Builder:      "go build -trimpath",
		BuiltAt:      builtAt,
		License:      LinuxObserverLicense,
		BuildMode:    LinuxObserverBuildMode,
		PackageOwned: true,
	})
}

func helperManifestBuildTimestamp() (string, error) {
	raw, set := os.LookupEnv("SOURCE_DATE_EPOCH")
	if !set {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	if raw == "" || strings.Trim(raw, "0123456789") != "" {
		return "", errors.New(
			"SOURCE_DATE_EPOCH must be a non-negative decimal integer",
		)
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || epoch < 0 {
		return "", errors.New(
			"SOURCE_DATE_EPOCH must be a non-negative decimal integer",
		)
	}
	instant := time.Unix(epoch, 0).UTC()
	if instant.Year() < 0 || instant.Year() > 9999 {
		return "", errors.New(
			"SOURCE_DATE_EPOCH is outside the RFC3339 date range",
		)
	}
	return instant.Format(time.RFC3339), nil
}

func LinuxObserverHelperCurrent(binaryPath, goarch string) (Manifest, bool) {
	info, err := os.Lstat(binaryPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return Manifest{}, false
	}
	manifest, err := ReadManifest(ManifestPath(binaryPath))
	if err != nil {
		return Manifest{}, false
	}
	if manifest.Version != ManifestVersion ||
		manifest.Command != LinuxObserverCommand ||
		manifest.TargetOS != "linux" ||
		manifest.TargetArch != goarch ||
		manifest.Artifact != filepath.Base(binaryPath) ||
		manifest.Builder != "go build -trimpath" ||
		manifest.License != LinuxObserverLicense ||
		manifest.BuildMode != LinuxObserverBuildMode ||
		!manifest.PackageOwned {
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

func Tun2SocksHelperCurrent(binaryPath, goarch string, requirePackageOwned bool) (Manifest, bool) {
	info, err := os.Lstat(binaryPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return Manifest{}, false
	}
	manifest, err := ReadManifest(ManifestPath(binaryPath))
	if err != nil {
		return Manifest{}, false
	}
	if manifest.Version != ManifestVersion ||
		manifest.Command != LinuxTun2SocksCommand ||
		manifest.TargetOS != "linux" ||
		manifest.TargetArch != goarch ||
		manifest.Artifact != filepath.Base(binaryPath) ||
		manifest.UpstreamModule != Tun2SocksUpstreamModule ||
		manifest.UpstreamVersion != Tun2SocksUpstreamVersion ||
		manifest.License != Tun2SocksLicense ||
		manifest.BuildMode != Tun2SocksBuildMode ||
		(requirePackageOwned && !manifest.PackageOwned) {
		return Manifest{}, false
	}
	sum, err := FileSHA256(binaryPath)
	if err != nil || manifest.SHA256 != sum {
		return Manifest{}, false
	}
	return manifest, true
}

func validateExplicitTun2Socks(path, goarch string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("path must be clean and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("path must be a non-symlink regular file")
	}
	if info.Mode()&0o111 == 0 {
		return "", errors.New("path must be executable")
	}
	if manifest, ok := Tun2SocksHelperCurrent(path, goarch, false); ok {
		return manifest.SHA256, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("path is neither a matching helper manifest nor a Linux ELF executable")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, openedInfo) {
		return "", errors.New("path changed while validating the explicit helper")
	}
	elfFile, err := elf.NewFile(file)
	if err != nil {
		return "", errors.New("path is neither a matching helper manifest nor a Linux ELF executable")
	}
	defer elfFile.Close()
	wantMachine := elf.Machine(elf.EM_NONE)
	switch goarch {
	case "arm64":
		wantMachine = elf.EM_AARCH64
	case "amd64":
		wantMachine = elf.EM_X86_64
	}
	if elfFile.Machine != wantMachine {
		return "", fmt.Errorf("Linux ELF target is %s, want %s", elfFile.Machine, wantMachine)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func CopyVerifiedExecutable(src, dst, expectedSHA256 string) error {
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	decoded, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("verified executable requires a valid SHA-256 digest")
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("verified executable source must be a non-symlink executable regular file")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	openedInfo, err := in.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) {
		return errors.New("verified executable source changed while opening")
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".verified-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), in); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedSHA256 {
		return fmt.Errorf("verified executable digest mismatch: expected %s got %s", expectedSHA256, actual)
	}
	if err := tmp.Chmod(0o700); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	keep = true
	return nil
}

func writeManifest(binaryPath string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := ManifestPath(binaryPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Manifest{}, err
	}
	return manifest, nil
}

func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func FindSourceRoot(start string) (string, error) {
	if strings.TrimSpace(start) == "" {
		start = "."
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if sourceRoot(current) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("Hideout source root not found from %s", start)
		}
		current = parent
	}
}

func sourceRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "hideout-shim")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "hideout-hostfsd")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", LinuxSessionSupervisorCommand)); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", LinuxObserverCommand)); err != nil {
		return false
	}
	return true
}

func FileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func commandSourceExists(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return true, nil
		}
	}
	return false, nil
}
