package workspacepath

import (
	"errors"
	"strings"
	"testing"
)

func TestBindingResolvesOnlyItsLogicalAndPhysicalAliases(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot := PhysicalBase + "/" + workspaceID
	binding, err := NewBinding(workspaceID, LogicalRoot, physicalRoot)
	if err != nil {
		t.Fatal(err)
	}

	logical, err := binding.Resolve("/workspace/src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := binding.Resolve(physicalRoot + "/src/./main.go")
	if err != nil {
		t.Fatal(err)
	}
	if logical.RelativePath != "src/main.go" ||
		logical.LogicalPath != "/workspace/src/main.go" ||
		logical.PhysicalPath != physicalRoot+"/src/main.go" ||
		logical.SourceAlias != LogicalAlias {
		t.Fatalf("logical identity = %#v", logical)
	}
	if physical.WorkspaceID != logical.WorkspaceID ||
		physical.RelativePath != logical.RelativePath ||
		physical.LogicalPath != logical.LogicalPath ||
		physical.PhysicalPath != logical.PhysicalPath ||
		physical.SourceAlias != PhysicalAlias {
		t.Fatalf("physical identity = %#v", physical)
	}
}

func TestBindingRejectsMalformedTraversalAndSiblingPaths(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot := PhysicalBase + "/" + workspaceID
	siblingRoot := PhysicalBase + "/wrk_" + strings.Repeat("b", 64)
	binding, err := BindingFromPhysicalRoot(LogicalRoot, physicalRoot)
	if err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{
		"relative", "/etc/passwd", PhysicalBase, siblingRoot + "/secret",
		"/workspace/../etc/passwd", physicalRoot + "/../" + strings.TrimPrefix(siblingRoot, PhysicalBase+"/"),
		"/workspace/file\x00suffix",
	} {
		_, resolveErr := binding.Resolve(candidate)
		if !errors.Is(resolveErr, ErrPathInvalid) && !errors.Is(resolveErr, ErrOutsideAttachment) {
			t.Fatalf("candidate %q error = %v", candidate, resolveErr)
		}
	}

	for _, malformed := range []string{
		"wrk_fixture", "wrk_" + strings.Repeat("A", 64), "wrk_" + strings.Repeat("a", 63),
	} {
		if _, resolveErr := Resolve(malformed, LogicalRoot); !errors.Is(resolveErr, ErrIdentityInvalid) {
			t.Fatalf("workspace ID %q error = %v", malformed, resolveErr)
		}
	}
}
