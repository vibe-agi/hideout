//go:build linux

package main

import (
	"strings"
	"testing"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	processcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/process"
	"github.com/vibe-agi/hideout/internal/workspacepath"
)

func TestObservedWorkspacePathsUseLogicalAttachmentIdentity(t *testing.T) {
	binding := observerWorkspaceBinding(t, "a")
	physical := binding.PhysicalRoot + "/src/main.go"
	target := binding.PhysicalRoot + "/src/main_test.go"

	fileEvent, err := normalizeObservedWorkspaceFileEvent(filecollector.Event{
		Path: physical, TargetPath: target, PathClass: "unknown",
	}, &binding)
	if err != nil {
		t.Fatal(err)
	}
	if fileEvent.Path != "/workspace/src/main.go" ||
		fileEvent.TargetPath != "/workspace/src/main_test.go" ||
		fileEvent.PathClass != "workspace" {
		t.Fatalf("normalized file event = %#v", fileEvent)
	}

	processEvent, err := normalizeObservedWorkspaceProcessEvent(processcollector.Event{
		Cwd:        binding.PhysicalRoot + "/src",
		Executable: binding.PhysicalRoot + "/bin/tool",
		Argv: []string{
			"tool", binding.PhysicalRoot + "/argument-is-path",
			"--message=" + binding.PhysicalRoot + "/embedded-text",
		},
	}, &binding)
	if err != nil {
		t.Fatal(err)
	}
	if processEvent.Cwd != "/workspace/src" || processEvent.Executable != "/workspace/bin/tool" ||
		processEvent.Argv[1] != "/workspace/argument-is-path" ||
		processEvent.Argv[2] != "--message="+binding.PhysicalRoot+"/embedded-text" {
		t.Fatalf("normalized process event = %#v", processEvent)
	}

	system, mapped, err := normalizeObservedWorkspacePath("/usr/bin/git", &binding)
	if err != nil || mapped || system != "/usr/bin/git" {
		t.Fatalf("system path = %q mapped=%t error=%v", system, mapped, err)
	}
}

func TestObservedWorkspaceArgumentsHideUnboundPhysicalAliases(t *testing.T) {
	binding := observerWorkspaceBinding(t, "a")
	sibling := observerWorkspaceBinding(t, "b")
	event, err := normalizeObservedWorkspaceProcessEvent(processcollector.Event{
		Argv: []string{"tool", sibling.PhysicalRoot + "/secret", "/workspace/../etc/passwd"},
	}, &binding)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(event.Argv); index++ {
		if event.Argv[index] != unboundObservedWorkspaceArgument {
			t.Fatalf("argument %d = %q", index, event.Argv[index])
		}
	}
}

func TestObservedWorkspacePathsRejectSiblingAndTraversal(t *testing.T) {
	binding := observerWorkspaceBinding(t, "a")
	sibling := observerWorkspaceBinding(t, "b")
	for _, candidate := range []string{
		sibling.PhysicalRoot + "/secret",
		workspacepath.PhysicalBase,
		binding.PhysicalRoot + "/../" + sibling.WorkspaceID + "/secret",
		"/workspace/../etc/passwd",
	} {
		if _, _, err := normalizeObservedWorkspacePath(candidate, &binding); err == nil {
			t.Fatalf("unbound observed path %q was accepted", candidate)
		}
	}
}

func observerWorkspaceBinding(t *testing.T, marker string) workspacepath.Binding {
	t.Helper()
	workspaceID := "wrk_" + strings.Repeat(marker, 64)
	binding, err := workspacepath.NewBinding(
		workspaceID,
		workspacepath.LogicalRoot,
		workspacepath.PhysicalBase+"/"+workspaceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
