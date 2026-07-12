package appopen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PrepareSafeState materializes package-owned safe configuration beneath an
// already-owned isolated data root. It rejects symlinked components and writes
// atomically; recipe values are data, not untrusted runtime input.
func PrepareSafeState(spec LaunchSpec, root string) error {
	if spec.SafeConfiguration == nil {
		return nil
	}
	return writeSafeSettings(root, spec.SafeConfiguration.RelativePath, spec.SafeConfiguration.Values)
}

// PrepareSafetyProfileState writes only the settings already validated as one
// Core safety effect. It creates the qualified-app/run path without following
// symlinks and then atomically writes the reviewed settings.
func PrepareSafetyProfileState(profile SafetyProfile, identity SafetyIdentity, effect SafeEffect) error {
	if err := ValidateSafetyEffect(profile, identity, effect); err != nil {
		return err
	}
	if !pathWithin(effect.StateBase, effect.StateRoot) || filepath.Clean(effect.StateBase) == filepath.Clean(effect.StateRoot) {
		return errors.New("appopen: unsafe isolated state path")
	}
	if err := ensureStateDirectory(effect.StateBase, effect.StateRoot); err != nil {
		return err
	}
	return writeSafeSettings(effect.StateRoot, effect.SettingsRelativePath, effect.Settings)
}

func ensureStateDirectory(base, target string) error {
	base = filepath.Clean(base)
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("appopen: safe state base is unavailable or symlinked")
	}
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("appopen: isolated state path escapes its base")
	}
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return errors.New("appopen: isolated state component is unsafe")
		}
	}
	return nil
}

func writeSafeSettings(root, relativePath string, values map[string]any) error {
	cleanRoot := filepath.Clean(root)
	if !filepath.IsAbs(cleanRoot) {
		return errors.New("appopen: safe state root must be absolute")
	}
	relative := filepath.Clean(relativePath)
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("appopen: safe configuration path must remain under its root")
	}
	rootInfo, err := os.Lstat(cleanRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("appopen: safe state root is unavailable or symlinked")
	}
	parent := cleanRoot
	parts := strings.Split(filepath.Dir(relative), string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		parent = filepath.Join(parent, part)
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(parent, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("appopen: safe configuration parent is not a real directory")
		}
	}
	target := filepath.Join(cleanRoot, relative)
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("appopen: safe configuration target is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("appopen: encode safe configuration: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(parent, ".safe-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, target)
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
