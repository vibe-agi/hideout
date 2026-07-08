package packagekit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type VerifyResult struct {
	Root  string
	Mode  string
	Files int
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
		return VerifyResult{Root: cleanRoot, Mode: "artifact", Files: len(manifest.Files)}, nil
	}
	statePath := filepath.Join(cleanRoot, filepath.FromSlash(InstalledManifest))
	state, err := LoadInstallState(statePath)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("open installed package manifest: %w; hint: reinstall Hideout into %s", err, cleanRoot)
	}
	if err := VerifyInstalled(cleanRoot, state); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{Root: cleanRoot, Mode: "installed", Files: len(state.Files)}, nil
}

func LoadManifest(path string) (Manifest, error) {
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
	if err := validateManifest(manifest); err != nil {
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
	return nil
}

func VerifyInstalled(prefix string, state InstallState) error {
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

func validateManifest(manifest Manifest) error {
	if manifest.Schema != ArtifactSchema {
		return fmt.Errorf("unsupported package manifest schema %q", manifest.Schema)
	}
	if strings.TrimSpace(manifest.BuiltAt) == "" {
		return errors.New("package manifest builtAt is required")
	}
	if strings.TrimSpace(manifest.Git.Commit) == "" {
		return errors.New("package manifest git.commit is required")
	}
	if strings.TrimSpace(manifest.Target.HostOS) == "" || strings.TrimSpace(manifest.Target.HostArch) == "" || strings.TrimSpace(manifest.Target.LinuxGuestArch) == "" {
		return errors.New("package manifest target hostOS, hostArch, and linuxGuestArch are required")
	}
	if manifest.Target.HostOS != runtime.GOOS || manifest.Target.HostArch != runtime.GOARCH {
		return fmt.Errorf("package target %s/%s does not match host %s/%s", manifest.Target.HostOS, manifest.Target.HostArch, runtime.GOOS, runtime.GOARCH)
	}
	if len(manifest.Files) == 0 {
		return errors.New("package manifest has no files")
	}
	if manifest.Migration.InstallStateSchema == "" {
		return errors.New("package manifest migration.installStateSchema is required")
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
	}, kind)
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
