package workspaceattach

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveGuestWorkspacePathUnifiesLogicalAndPhysicalAliases(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot := PhysicalWorkspaceBase + "/" + workspaceID

	logical, err := ResolveGuestWorkspacePath(workspaceID, "/workspace/src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := ResolveGuestWorkspacePath(workspaceID, physicalRoot+"/src/./main.go")
	if err != nil {
		t.Fatal(err)
	}
	if logical.WorkspaceID != workspaceID || logical.RelativePath != "src/main.go" ||
		logical.LogicalPath != "/workspace/src/main.go" || logical.PhysicalPath != physicalRoot+"/src/main.go" ||
		logical.SourceAlias != GuestPathLogicalAlias {
		t.Fatalf("logical identity = %#v", logical)
	}
	if physical.WorkspaceID != logical.WorkspaceID || physical.RelativePath != logical.RelativePath ||
		physical.LogicalPath != logical.LogicalPath || physical.PhysicalPath != logical.PhysicalPath ||
		physical.SourceAlias != GuestPathPhysicalAlias {
		t.Fatalf("physical identity = %#v, logical = %#v", physical, logical)
	}

	for _, candidate := range []string{"/workspace", "/workspace/", physicalRoot, physicalRoot + "/"} {
		resolved, err := ResolveGuestWorkspacePath(workspaceID, candidate)
		if err != nil {
			t.Fatalf("resolve root %q: %v", candidate, err)
		}
		if resolved.RelativePath != "." || resolved.LogicalPath != LogicalWorkspaceRoot || resolved.PhysicalPath != physicalRoot {
			t.Fatalf("root identity for %q = %#v", candidate, resolved)
		}
	}
}

func TestResolveGuestWorkspacePathRejectsUnboundPaths(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot := PhysicalWorkspaceBase + "/" + workspaceID
	siblingRoot := PhysicalWorkspaceBase + "/wrk_" + strings.Repeat("b", 64)

	for _, candidate := range []string{
		"relative/file", "/Users/alice/project/file", "/etc/passwd",
		"/workspace-other/file", PhysicalWorkspaceBase, siblingRoot + "/secret",
		"/workspace/../etc/passwd", "/workspace/src/../../etc/passwd",
		physicalRoot + "/../" + strings.TrimPrefix(siblingRoot, PhysicalWorkspaceBase+"/") + "/secret",
		"/workspace/file\x00suffix",
	} {
		_, err := ResolveGuestWorkspacePath(workspaceID, candidate)
		if !errors.Is(err, ErrGuestWorkspacePathOutsideAttachment) && !errors.Is(err, ErrGuestWorkspacePathInvalid) {
			t.Fatalf("unbound path %q error = %v", candidate, err)
		}
	}
}

func TestResolveGuestWorkspacePathRejectsNonProductionIdentity(t *testing.T) {
	for _, workspaceID := range []string{
		"", "wrk_short", "ws_" + strings.Repeat("a", 64),
		"wrk_" + strings.Repeat("A", 64), "wrk_" + strings.Repeat("a", 63) + "/",
	} {
		_, err := ResolveGuestWorkspacePath(workspaceID, LogicalWorkspaceRoot)
		if !errors.Is(err, ErrGuestWorkspaceIdentityInvalid) {
			t.Fatalf("workspace ID %q error = %v", workspaceID, err)
		}
	}
}
