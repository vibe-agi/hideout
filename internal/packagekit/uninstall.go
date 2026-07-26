package packagekit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
)

type UninstallOptions struct {
	Prefix            string
	Store             string
	DryRun            bool
	Purge             bool
	ConfirmPurgeStore string
}

type UninstallResult struct {
	Prefix        string
	StoreRoot     string
	DryRun        bool
	Purge         bool
	Files         []string
	Dirs          []string
	DurableAction string
}

func Uninstall(opts UninstallOptions) (UninstallResult, error) {
	prefix, err := CleanRoot(opts.Prefix, "install prefix")
	if err != nil {
		return UninstallResult{}, err
	}
	state, err := LoadInstallState(filepath.Join(prefix, filepath.FromSlash(InstalledManifest)))
	if err != nil {
		return UninstallResult{}, fmt.Errorf("load installed package manifest: %w", err)
	}
	if err := verifyInstalledActive(prefix, state); err != nil {
		return UninstallResult{}, err
	}
	store := opts.Store
	if store == "" {
		store = state.StoreRoot
	}
	if store != "" {
		if cleaned, err := CleanRoot(store, "store root"); err == nil {
			store = cleaned
		}
	}
	if opts.Purge {
		if store == "" {
			return UninstallResult{}, fmt.Errorf("purge requires a durable store path")
		}
		if !opts.DryRun {
			confirmed, err := CleanRoot(opts.ConfirmPurgeStore, "confirmed purge store")
			if err != nil {
				return UninstallResult{}, fmt.Errorf("purge requires the exact store path in --confirm-purge: %w", err)
			}
			if confirmed != store {
				return UninstallResult{}, fmt.Errorf("purge confirmation does not match durable store: confirmed=%s store=%s", confirmed, store)
			}
		}
	}
	files := make([]string, 0, len(state.Files)+1)
	for _, file := range state.Files {
		files = append(files, file.Path)
	}
	for _, file := range state.ObsoleteFiles {
		files = append(files, file.Path)
	}
	files = append(files, InstalledManifest)
	dirs := slices.Clone(state.Directories)
	slices.SortFunc(dirs, func(a, b string) int {
		return len(b) - len(a)
	})
	result := UninstallResult{
		Prefix:        prefix,
		StoreRoot:     store,
		DryRun:        opts.DryRun,
		Purge:         opts.Purge,
		Files:         files,
		Dirs:          dirs,
		DurableAction: "preserved",
	}
	if opts.Purge {
		result.DurableAction = "purged"
	}
	root, err := os.OpenRoot(prefix)
	if err != nil {
		return result, fmt.Errorf("open install prefix: %w", err)
	}
	defer root.Close()
	// Validate the complete destructive scope before removing the first file.
	// A corrupt or adversarial installed-state manifest must never turn a
	// rejected uninstall into a partial uninstall.
	for _, rel := range files {
		if _, err := rootedPackagePath(root, rel); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("validate uninstall file %s: %w", rel, err)
		}
	}
	for _, rel := range dirs {
		if rel == "" || rel == "." || rel == "bin" {
			continue
		}
		if _, err := rootedPackagePath(root, rel); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("validate uninstall directory %s: %w", rel, err)
		}
	}
	if opts.DryRun {
		return result, nil
	}
	for _, rel := range files {
		joined, err := rootedPackagePath(root, rel)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, err
		}
		if err := root.Remove(joined); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("remove package file %s: %w", rel, err)
		}
	}
	for _, rel := range dirs {
		if rel == "" || rel == "." || rel == "bin" {
			continue
		}
		joined, err := rootedPackagePath(root, rel)
		if err != nil {
			continue
		}
		_ = root.Remove(joined)
	}
	if joined, err := rootedPackagePath(root, packageMetadataRoot); err == nil {
		_ = root.Remove(joined)
	}
	if opts.Purge && store != "" {
		if err := os.RemoveAll(store); err != nil {
			return result, fmt.Errorf("purge durable store: %w", err)
		}
		writePurgeAudit(store, AuditEvent{
			Operation:     "uninstall",
			Status:        "passed",
			Prefix:        prefix,
			StoreRoot:     store,
			Files:         len(files),
			StaleFiles:    len(state.ObsoleteFiles),
			DurableAction: result.DurableAction,
			Purge:         opts.Purge,
		})
	} else {
		writeAudit(store, AuditEvent{
			Operation:     "uninstall",
			Status:        "passed",
			Prefix:        prefix,
			StoreRoot:     store,
			Files:         len(files),
			StaleFiles:    len(state.ObsoleteFiles),
			DurableAction: result.DurableAction,
			Purge:         opts.Purge,
		})
	}
	return result, nil
}

func RelDir(rel string) string {
	return path.Dir(rel)
}
