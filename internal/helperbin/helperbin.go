package helperbin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const ManifestVersion = "hideout.helper-manifest/v1"

type BuildOptions struct {
	Out     string
	GOARCH  string
	Source  string
	Command string
}

type Manifest struct {
	Version    string `json:"version"`
	Command    string `json:"command"`
	TargetOS   string `json:"targetOS"`
	TargetArch string `json:"targetArch"`
	Artifact   string `json:"artifact"`
	SHA256     string `json:"sha256"`
	Builder    string `json:"builder"`
	BuiltAt    string `json:"builtAt"`
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
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if path := os.Getenv("HIDEOUT_LINUX_TUN2SOCKS_PATH"); path != "" {
		if FileExists(path) {
			return path
		}
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"tun2socks-linux-" + goarch, "tun2socks-linux"} {
			candidate := filepath.Join(filepath.Dir(exe), name)
			if FileExists(candidate) {
				return candidate
			}
		}
	}
	for _, name := range []string{"tun2socks-linux-" + goarch, "tun2socks-linux"} {
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
	manifest := Manifest{
		Version:    ManifestVersion,
		Command:    command,
		TargetOS:   "linux",
		TargetArch: goarch,
		Artifact:   filepath.Base(binaryPath),
		SHA256:     sum,
		Builder:    "go build",
		BuiltAt:    time.Now().UTC().Format(time.RFC3339),
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

func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
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
