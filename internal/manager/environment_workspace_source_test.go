package manager

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
)

func TestSharedEnvironmentWorkspaceReadsHaveOneExplicitBoundary(t *testing.T) {
	repoRoot := managerRepositoryRoot(t)
	productionRoots := []string{
		"internal/manager",
		"internal/daemon",
		"internal/hostcap",
		"internal/backend",
	}
	forbiddenEverywhere := map[string]bool{
		"HostWorkspace":      true,
		"GuestWorkspaceRoot": true,
	}
	bindingFields := map[string]bool{
		"DedicatedWorkspace": true,
		"DedicatedGuestRoot": true,
		"BoundWorkspace":     true,
		"BoundGuestRoot":     true,
	}

	for _, relativeRoot := range productionRoots {
		root := filepath.Join(repoRoot, relativeRoot)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Errorf("parse production source %s: %v", relative, err)
				return nil
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := selector.Sel.Name
				switch {
				case forbiddenEverywhere[name]:
					t.Errorf("%s reads environment workspace through forbidden selector %s; use immutable attachment authority or pinnedEnvironmentWorkspace", relative, name)
				case name == "WorkspaceBinding" && relative != "internal/manager/environment_workspace.go":
					t.Errorf("%s bypasses the single shared-mode workspace boundary with %s", relative, name)
				case bindingFields[name] && relative != "internal/manager/run_environment.go":
					t.Errorf("%s reads raw environment workspace field %s outside explicit mode construction/validation", relative, name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk production source %s: %v", relativeRoot, err)
		}
	}
}

func TestPinnedEnvironmentWorkspaceRejectsSharedRecords(t *testing.T) {
	shared := environment.Record{
		Mode:               environment.ModeShared,
		DedicatedWorkspace: "/must/not/be/read",
		DedicatedGuestRoot: "/must/not/be/read",
	}
	if binding, ok := pinnedEnvironmentWorkspace(shared); ok || binding != (pinnedWorkspaceBinding{}) {
		t.Fatalf("shared environment exposed a pinned workspace binding: %#v, ok=%v", binding, ok)
	}
	portal := environment.Record{
		Mode:               environment.ModeDedicatedPortal,
		DedicatedWorkspace: "/must/not/be-read",
		DedicatedGuestRoot: "/must/not-be-read",
	}
	if binding, ok := pinnedEnvironmentWorkspace(portal); ok || binding != (pinnedWorkspaceBinding{}) {
		t.Fatalf("dedicated Portal environment exposed a pinned workspace binding: %#v, ok=%v", binding, ok)
	}

	dedicated := environment.Record{
		Mode:               environment.ModeDedicated,
		DedicatedWorkspace: "/host/project",
		DedicatedGuestRoot: "/workspace",
	}
	binding, ok := pinnedEnvironmentWorkspace(dedicated)
	if !ok || binding.HostRoot != "/host/project" || binding.GuestRoot != "/workspace" {
		t.Fatalf("dedicated binding changed: %#v, ok=%v", binding, ok)
	}
}

func managerRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve manager source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
