package packagekit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
)

type UninstallOptions struct {
	Prefix string
	Store  string
	DryRun bool
	Purge  bool
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
	if err := VerifyInstalled(prefix, state); err != nil {
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
	files := make([]string, 0, len(state.Files)+1)
	for _, file := range state.Files {
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
	if opts.DryRun {
		return result, nil
	}
	for _, rel := range files {
		joined, err := JoinRelative(prefix, rel)
		if err != nil {
			return result, err
		}
		if err := os.Remove(joined); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("remove package file %s: %w", rel, err)
		}
	}
	for _, rel := range dirs {
		if rel == "" || rel == "." || rel == "bin" {
			continue
		}
		joined, err := JoinRelative(prefix, rel)
		if err != nil {
			return result, err
		}
		_ = os.Remove(joined)
	}
	_ = os.Remove(filepath.Join(prefix, filepath.FromSlash(packageMetadataRoot)))
	if opts.Purge && store != "" {
		if err := os.RemoveAll(store); err != nil {
			return result, fmt.Errorf("purge durable store: %w", err)
		}
		writePurgeAudit(store, AuditEvent{
			Operation: "uninstall",
			Status:    "passed",
			Prefix:    prefix,
			StoreRoot: store,
			Files:     len(files),
			Purge:     opts.Purge,
		})
	} else {
		writeAudit(store, AuditEvent{
			Operation: "uninstall",
			Status:    "passed",
			Prefix:    prefix,
			StoreRoot: store,
			Files:     len(files),
			Purge:     opts.Purge,
		})
	}
	return result, nil
}

func RelDir(rel string) string {
	return path.Dir(rel)
}
