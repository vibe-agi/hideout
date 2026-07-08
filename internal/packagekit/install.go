package packagekit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"
)

type InstallOptions struct {
	PackageRoot string
	Prefix      string
	StoreRoot   string
	Now         time.Time
}

type InstallResult struct {
	Operation    string
	Prefix       string
	StoreRoot    string
	ManifestPath string
	FilesCopied  int
}

func Install(opts InstallOptions) (InstallResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	packageRoot, err := CleanRoot(opts.PackageRoot, "package root")
	if err != nil {
		return InstallResult{}, err
	}
	prefix, err := cleanOrCreateRoot(opts.Prefix, "install prefix", 0o755)
	if err != nil {
		return InstallResult{}, err
	}
	store, err := cleanOrCreateRoot(opts.StoreRoot, "store root", 0o700)
	if err != nil {
		return InstallResult{}, err
	}
	manifest, err := LoadManifest(filepath.Join(packageRoot, "package-manifest.json"))
	if err != nil {
		return InstallResult{}, err
	}
	if err := VerifyArtifact(packageRoot, manifest); err != nil {
		return InstallResult{}, err
	}
	operation := "install"
	existing, err := loadExistingState(prefix)
	if err == nil {
		operation = "upgrade"
		if !slices.Contains(manifest.Migration.FromInstalledSchemas, existing.Schema) {
			return InstallResult{}, fmt.Errorf("installed package schema %q is outside migration range %v", existing.Schema, manifest.Migration.FromInstalledSchemas)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return InstallResult{}, err
	}

	installedFiles := make([]File, 0, len(manifest.Files))
	dirs := []string{"bin", packageMetadataRoot}
	for _, file := range manifest.Files {
		src, err := JoinRelative(packageRoot, file.Path)
		if err != nil {
			return InstallResult{}, err
		}
		dstRel, err := installPathForArtifact(file.Path)
		if err != nil {
			return InstallResult{}, err
		}
		dst, err := JoinRelative(prefix, dstRel)
		if err != nil {
			return InstallResult{}, err
		}
		if err := copyFile(src, dst, file.Executable); err != nil {
			return InstallResult{}, fmt.Errorf("copy %s: %w", file.Path, err)
		}
		sum, err := FileSHA256(dst)
		if err != nil {
			return InstallResult{}, err
		}
		installedFiles = append(installedFiles, File{
			Path:       dstRel,
			Kind:       file.Kind,
			SHA256:     sum,
			Executable: file.Executable,
		})
		dirs = appendInstallDirs(dirs, dstRel)
	}
	dirs = uniqueStrings(dirs)
	state := NewInstallState(prefix, store, manifest, installedFiles, dirs, now)
	manifestPath, err := JoinRelative(prefix, InstalledManifest)
	if err != nil {
		return InstallResult{}, err
	}
	if err := writeJSONFile(manifestPath, state, 0o644); err != nil {
		return InstallResult{}, err
	}
	writeAudit(store, AuditEvent{
		Operation: operation,
		Status:    "passed",
		Prefix:    prefix,
		StoreRoot: store,
		Files:     len(installedFiles),
	})
	return InstallResult{
		Operation:    operation,
		Prefix:       prefix,
		StoreRoot:    store,
		ManifestPath: manifestPath,
		FilesCopied:  len(installedFiles),
	}, nil
}

func loadExistingState(prefix string) (InstallState, error) {
	statePath, err := JoinRelative(prefix, InstalledManifest)
	if err != nil {
		return InstallState{}, err
	}
	return LoadInstallState(statePath)
}

func cleanOrCreateRoot(root, label string, mode os.FileMode) (string, error) {
	if root == "" {
		return "", errors.New(label + " is required")
	}
	if err := os.MkdirAll(root, mode); err != nil {
		return "", err
	}
	clean, err := CleanRoot(root, label)
	if err != nil {
		return "", err
	}
	return clean, nil
}

func copyFile(src, dst string, executable bool) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func writeJSONFile(path string, value any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func appendInstallDirs(dirs []string, rel string) []string {
	dir := path.Dir(rel)
	for dir != "." && dir != "/" {
		dirs = append(dirs, dir)
		dir = path.Dir(dir)
	}
	return dirs
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := values[:0]
	for _, value := range values {
		if value == "" || value == "." {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
