package packagekit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type RepairOptions struct {
	Prefix string
	DryRun bool
}

type RepairRejection struct {
	Path   string
	Reason string
}

type RepairResult struct {
	Prefix        string
	StoreRoot     string
	DryRun        bool
	Considered    []string
	Removed       []string
	Rejected      []RepairRejection
	DurableAction string
}

func DetectObsoleteFiles(existing InstallState, newFiles []File) []ObsoleteFile {
	newSet := map[string]struct{}{}
	for _, file := range newFiles {
		newSet[file.Path] = struct{}{}
	}
	seen := map[string]struct{}{}
	var obsolete []ObsoleteFile
	add := func(file File, reason string) {
		if file.Path == "" {
			return
		}
		if _, ok := newSet[file.Path]; ok {
			return
		}
		if _, ok := seen[file.Path]; ok {
			return
		}
		seen[file.Path] = struct{}{}
		obsolete = append(obsolete, ObsoleteFile{
			Path:       file.Path,
			Kind:       file.Kind,
			SHA256:     file.SHA256,
			Executable: file.Executable,
			Reason:     reason,
		})
	}
	for _, file := range existing.Files {
		add(file, "absent from new package manifest")
	}
	for _, file := range existing.ObsoleteFiles {
		add(File{Path: file.Path, Kind: file.Kind, SHA256: file.SHA256, Executable: file.Executable}, file.Reason)
	}
	slices.SortFunc(obsolete, func(a, b ObsoleteFile) int { return cmpString(a.Path, b.Path) })
	return obsolete
}

func RepairObsoleteFiles(opts RepairOptions) (RepairResult, error) {
	prefix, err := CleanRoot(opts.Prefix, "install prefix")
	if err != nil {
		return RepairResult{}, err
	}
	state, err := LoadInstallState(filepath.Join(prefix, filepath.FromSlash(InstalledManifest)))
	if err != nil {
		return RepairResult{}, fmt.Errorf("load installed package manifest: %w", err)
	}
	if err := verifyInstalledActive(prefix, state); err != nil {
		return RepairResult{}, err
	}
	root, err := os.OpenRoot(prefix)
	if err != nil {
		return RepairResult{}, fmt.Errorf("open install prefix: %w", err)
	}
	defer root.Close()
	result := RepairResult{
		Prefix:        prefix,
		StoreRoot:     state.StoreRoot,
		DryRun:        opts.DryRun,
		DurableAction: "preserved",
	}
	var remaining []ObsoleteFile
	for _, stale := range state.ObsoleteFiles {
		result.Considered = append(result.Considered, stale.Path)
		joined, err := rootedPackagePath(root, stale.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			result.Rejected = append(result.Rejected, RepairRejection{Path: stale.Path, Reason: err.Error()})
			remaining = append(remaining, stale)
			continue
		}
		st, err := root.Lstat(joined)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			result.Rejected = append(result.Rejected, RepairRejection{Path: stale.Path, Reason: err.Error()})
			remaining = append(remaining, stale)
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			result.Rejected = append(result.Rejected, RepairRejection{Path: stale.Path, Reason: "not a regular package file"})
			remaining = append(remaining, stale)
			continue
		}
		if opts.DryRun {
			continue
		}
		joined, err = rootedPackagePath(root, stale.Path)
		if err == nil {
			err = root.Remove(joined)
		}
		if err != nil {
			result.Rejected = append(result.Rejected, RepairRejection{Path: stale.Path, Reason: err.Error()})
			remaining = append(remaining, stale)
			continue
		}
		result.Removed = append(result.Removed, stale.Path)
	}
	if !opts.DryRun {
		state.ObsoleteFiles = remaining
		if err := saveInstallState(prefix, state); err != nil {
			return result, err
		}
	}
	writeAudit(state.StoreRoot, AuditEvent{
		Operation:     "repair",
		Status:        "passed",
		Prefix:        prefix,
		StoreRoot:     state.StoreRoot,
		Files:         len(result.Removed),
		StaleFiles:    len(result.Considered),
		RepairRemoved: len(result.Removed),
	})
	return result, nil
}

func cmpString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
