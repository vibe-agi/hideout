package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestReusableRunSessionsUseUniqueRuntimeChildrenAndClearOnlyOwner(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(first)
	second, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(second)

	if first.Layout.ID == second.Layout.ID || first.RuntimeSessionDir == second.RuntimeSessionDir || first.RuntimeShimDir == second.RuntimeShimDir {
		t.Fatalf("session runtime paths are not unique: first=%+v second=%+v", first, second)
	}
	environmentStore := environment.Store{Root: store.Root}
	for _, runSession := range []RunSession{first, second} {
		wantRuntime := environmentStore.RuntimeSessionDir(runEnvironment.Record.ID, runSession.Layout.ID)
		wantShims := environmentStore.SessionShimDir(runEnvironment.Record.ID, runSession.Layout.ID)
		if runSession.RuntimeSessionDir != wantRuntime || runSession.RuntimeShimDir != wantShims {
			t.Fatalf("session %s runtime=%q shims=%q want=%q %q", runSession.Layout.ID, runSession.RuntimeSessionDir, runSession.RuntimeShimDir, wantRuntime, wantShims)
		}
		if err := os.WriteFile(filepath.Join(runSession.RuntimeSessionDir, "owner-marker"), []byte(runSession.Layout.ID), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := environmentStore.ClearSessionRuntime(runEnvironment.Record.ID, first.Layout.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.RuntimeSessionDir); !os.IsNotExist(err) {
		t.Fatalf("first runtime remains after owner cleanup: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(second.RuntimeSessionDir, "owner-marker"))
	if err != nil {
		t.Fatalf("sibling marker was removed: %v", err)
	}
	if string(marker) != second.Layout.ID {
		t.Fatalf("sibling marker=%q", marker)
	}
	if err := environmentStore.ClearSessionRuntime(runEnvironment.Record.ID, second.Layout.ID); err != nil {
		t.Fatal(err)
	}
}
