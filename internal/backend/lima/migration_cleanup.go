package lima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
)

type migrationCleanupNode struct {
	path     string
	relative string
	info     os.FileInfo
}

// RollbackMigrationDestination removes only the private staging tree whose
// durable owner record proves the exact operation, effect, stage, and object
// handles supplied by Manager. It deliberately does not inspect or remove
// top-level Lima instances or disks: those paths are outside the staging
// authority and are adopted by a separate effect.
func (b Backend) RollbackMigrationDestination(
	ctx context.Context,
	request backend.DestinationRollbackRequest,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return migrationStageError(
			"migration.provider.rollback_cleanup_failed", request.Binding,
			request.StageHandle, err, true,
		)
	}
	home, err := b.migrationLimaHome()
	if err != nil {
		return migrationStageError(
			"migration.provider.lima_home_unsafe", request.Binding,
			request.StageHandle, err, true,
		)
	}
	migrationRoot := filepath.Join(home, "_hideout-migration")
	stagesRoot := filepath.Join(migrationRoot, "stages")
	stageDir := filepath.Join(stagesRoot, string(request.StageHandle))
	if _, err := os.Lstat(stageDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return migrationStageError(
			"migration.provider.rollback_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	for _, directory := range []string{migrationRoot, stagesRoot, stageDir} {
		if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
			return migrationStageError(
				"migration.provider.rollback_ownership_unproved", request.Binding,
				request.StageHandle, err, true,
			)
		}
	}

	var owner migrationStageOwner
	if err := readMigrationJSONStrict(filepath.Join(stageDir, "owner.json"), &owner); err != nil {
		return migrationStageError(
			"migration.provider.rollback_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	if err := owner.validate(); err != nil || owner.StageHandle != request.StageHandle ||
		owner.Binding != request.Binding || !slices.Equal(owner.ObjectHandles, request.ObjectHandles) {
		if err == nil {
			err = errors.New("destination stage rollback binding changed")
		}
		return migrationStageError(
			"migration.provider.rollback_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}

	files, directories, err := inspectMigrationStageCleanupTree(ctx, stageDir, owner)
	if err != nil {
		return migrationStageError(
			"migration.provider.rollback_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	if err := removeMigrationCleanupTree(ctx, stageDir, files, directories); err != nil {
		return migrationStageError(
			"migration.provider.rollback_cleanup_failed", request.Binding,
			request.StageHandle, err, true,
		)
	}
	if err := syncMigrationDirectory(stagesRoot); err != nil {
		return migrationStageError(
			"migration.provider.rollback_cleanup_failed", request.Binding,
			request.StageHandle, err, true,
		)
	}
	return nil
}

func inspectMigrationStageCleanupTree(
	ctx context.Context,
	stageDir string,
	owner migrationStageOwner,
) ([]migrationCleanupNode, []migrationCleanupNode, error) {
	allowedFiles, allowedDirectories, err := migrationStageCleanupAllowlist(owner)
	if err != nil {
		return nil, nil, err
	}
	return inspectMigrationCleanupTree(ctx, stageDir, allowedFiles, allowedDirectories)
}

func inspectMigrationCleanupTree(
	ctx context.Context,
	root string,
	allowedFiles,
	allowedDirectories map[string]struct{},
) ([]migrationCleanupNode, []migrationCleanupNode, error) {
	files := make([]migrationCleanupNode, 0, len(allowedFiles))
	directories := make([]migrationCleanupNode, 0, len(allowedDirectories))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || (relative != "." && !migrationPathWithin(root, path)) {
			return errors.New("migration cleanup path escaped its owner")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("migration cleanup tree contains a link")
		}
		node := migrationCleanupNode{path: path, relative: relative, info: info}
		if entry.IsDir() {
			if _, allowed := allowedDirectories[relative]; !allowed ||
				!info.IsDir() || info.Mode().Perm()&0o022 != 0 {
				return errors.New("migration cleanup tree contains an unknown or unsafe directory")
			}
			directories = append(directories, node)
			return nil
		}
		if _, allowed := allowedFiles[relative]; !allowed ||
			!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("migration cleanup tree contains an unknown or unsafe file")
		}
		files = append(files, node)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, directories, nil
}

func migrationStageCleanupAllowlist(
	owner migrationStageOwner,
) (map[string]struct{}, map[string]struct{}, error) {
	files := map[string]struct{}{
		"owner.json":    {},
		"complete.json": {},
	}
	for _, entry := range owner.Entries {
		files[entry.RelativePath] = struct{}{}
		files[entry.RelativePath+".partial"] = struct{}{}
		files[filepath.Join("checkpoints", string(entry.ComponentID)+".json")] = struct{}{}
	}
	for _, configuration := range owner.Configurations {
		files[configuration.YAMLRelativePath] = struct{}{}
		files[configuration.NormalizedRelativePath] = struct{}{}
		files[migrationAdoptionEvidenceRelativePath(configuration.EnvironmentRef)] = struct{}{}
	}
	return migrationCleanupAllowlistForFiles(files)
}

func inspectMigrationSnapshotCleanupTree(
	ctx context.Context,
	snapshotDir string,
	owner migrationSnapshotOwner,
) ([]migrationCleanupNode, []migrationCleanupNode, error) {
	files := map[string]struct{}{
		"owner.json":             {},
		"complete.json":          {},
		".cow-probe-source":      {},
		".cow-probe-destination": {},
	}
	for _, entry := range owner.Entries {
		files[entry.RelativePath] = struct{}{}
	}
	files[migrationSnapshotIdentityEvidenceRelativePath] = struct{}{}
	for _, selection := range owner.Selections {
		for relative := range migrationIdentityProbeAllowedFiles(owner, selection.EnvironmentRef) {
			files[relative] = struct{}{}
		}
	}
	allowedFiles, allowedDirectories, err := migrationCleanupAllowlistForFiles(files)
	if err != nil {
		return nil, nil, err
	}
	return inspectMigrationCleanupTree(
		ctx, snapshotDir, allowedFiles, allowedDirectories,
	)
}

func migrationCleanupAllowlistForFiles(
	files map[string]struct{},
) (map[string]struct{}, map[string]struct{}, error) {
	directories := map[string]struct{}{".": {}}
	for relative := range files {
		if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
			return nil, nil, errors.New("destination stage cleanup allowlist is invalid")
		}
		for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
			if parent == "" || filepath.IsAbs(parent) || filepath.Clean(parent) != parent ||
				parent == ".." || strings.HasPrefix(parent, ".."+string(filepath.Separator)) {
				return nil, nil, errors.New("destination stage cleanup directory escaped its owner")
			}
			directories[parent] = struct{}{}
		}
	}
	return files, directories, nil
}

func removeMigrationCleanupTree(
	ctx context.Context,
	rootPath string,
	files,
	directories []migrationCleanupNode,
) error {
	if len(directories) == 0 {
		return errors.New("destination stage cleanup root is absent")
	}
	var root migrationCleanupNode
	childDirectories := make([]migrationCleanupNode, 0, len(directories)-1)
	for _, directory := range directories {
		if directory.relative == "." {
			root = directory
		} else {
			childDirectories = append(childDirectories, directory)
		}
	}
	if root.path != rootPath {
		return errors.New("migration cleanup root changed")
	}
	sort.Slice(files, func(left, right int) bool {
		return migrationCleanupNodeBefore(files[left], files[right])
	})
	sort.Slice(childDirectories, func(left, right int) bool {
		return migrationCleanupNodeBefore(childDirectories[left], childDirectories[right])
	})
	for _, node := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyMigrationCleanupNode(root, true); err != nil {
			return err
		}
		if err := removeMigrationCleanupNode(node, false); err != nil {
			return err
		}
	}
	for _, node := range childDirectories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyMigrationCleanupNode(root, true); err != nil {
			return err
		}
		if err := removeMigrationCleanupNode(node, true); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return removeMigrationCleanupNode(root, true)
}

func migrationCleanupNodeBefore(left, right migrationCleanupNode) bool {
	leftDepth := strings.Count(left.relative, string(filepath.Separator))
	rightDepth := strings.Count(right.relative, string(filepath.Separator))
	if leftDepth != rightDepth {
		return leftDepth > rightDepth
	}
	return left.relative < right.relative
}

func removeMigrationCleanupNode(node migrationCleanupNode, directory bool) error {
	if err := verifyMigrationCleanupNode(node, directory); err != nil {
		return err
	}
	return os.Remove(node.path)
}

func verifyMigrationCleanupNode(node migrationCleanupNode, directory bool) error {
	observed, err := os.Lstat(node.path)
	if err != nil {
		return err
	}
	if !os.SameFile(node.info, observed) || observed.Mode() != node.info.Mode() ||
		observed.Mode()&os.ModeSymlink != 0 || observed.IsDir() != directory ||
		(!directory && !observed.Mode().IsRegular()) {
		return errors.New("destination stage cleanup node changed after inspection")
	}
	if !directory &&
		(observed.Size() != node.info.Size() || observed.ModTime() != node.info.ModTime()) {
		return errors.New("destination stage cleanup file changed after inspection")
	}
	return nil
}
