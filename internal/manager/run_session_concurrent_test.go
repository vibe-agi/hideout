package manager

import (
	"os"
	"path/filepath"
	"strings"
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
	defer func() {
		if _, err := core.CloseRunSession(first); err != nil {
			t.Errorf("close first run session: %v", err)
		}
	}()
	second, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := core.CloseRunSession(second); err != nil {
			t.Errorf("close second run session: %v", err)
		}
	}()

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

func TestSessionOnlyProfileChangeUsesSameMachineAndNewSnapshot(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
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
	defer func() {
		if _, err := core.CloseRunSession(first); err != nil {
			t.Errorf("close first run session: %v", err)
		}
	}()

	p, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	p.Env.Public["SESSION_ONLY"] = "changed"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	changedPlan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	changedEnvironment, err := core.SelectRunEnvironment(changedPlan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.BeginRunSession(changedPlan, changedEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := core.CloseRunSession(second); err != nil {
			t.Errorf("close second run session: %v", err)
		}
	}()

	if changedEnvironment.Record.ID != runEnvironment.Record.ID || changedEnvironment.Record.MachineIdentityID != runEnvironment.Record.MachineIdentityID || changedEnvironment.Record.BootConfigurationID != runEnvironment.Record.BootConfigurationID {
		t.Fatalf("session-only change replaced machine: before=%+v after=%+v", runEnvironment.Record, changedEnvironment.Record)
	}
	if first.SessionSnapshotID == second.SessionSnapshotID || second.SessionSnapshotID != changedEnvironment.Configuration.Layers.SessionID {
		t.Fatalf("session snapshots first=%q second=%q desired=%q", first.SessionSnapshotID, second.SessionSnapshotID, changedEnvironment.Configuration.Layers.SessionID)
	}
}

func TestPolicySourceIsImmutableWithinSessionAndChangesForNextSession(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	initialPlan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(initialPlan); err != nil {
		t.Fatal(err)
	}
	p, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID: "command", Path: "policy/command.js", Entrypoints: []string{"decideCommand"},
	}}
	if err := os.MkdirAll(filepath.Join(store.ProfileDir("default"), "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(store.ProfileDir("default"), "policy", "command.js")
	if err := os.WriteFile(sourcePath, []byte("function decideCommand() { return { decision: 'allow', reason: 'v1' }; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
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
	if len(first.PolicyScriptRefs) != 1 || filepath.IsAbs(first.PolicyScriptRefs[0].Path) || first.PolicyScriptRefs[0].Path != "00.js" {
		t.Fatalf("first session policy refs=%+v", first.PolicyScriptRefs)
	}
	firstSnapshotPath := filepath.Join(first.RuntimeSessionDir, "policy", first.PolicyScriptRefs[0].Path)
	firstBytes, err := os.ReadFile(firstSnapshotPath)
	if err != nil || !strings.Contains(string(firstBytes), "'v1'") {
		t.Fatalf("first policy snapshot=%q err=%v", firstBytes, err)
	}

	if err := os.WriteFile(sourcePath, []byte("function decideCommand() { return { decision: 'deny', reason: 'v2' }; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstAfterEdit, err := os.ReadFile(firstSnapshotPath)
	if err != nil || string(firstAfterEdit) != string(firstBytes) {
		t.Fatalf("running session policy changed after profile edit: before=%q after=%q err=%v", firstBytes, firstAfterEdit, err)
	}

	changedPlan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	changedEnvironment, err := core.SelectRunEnvironment(changedPlan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.BeginRunSession(changedPlan, changedEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(filepath.Join(second.RuntimeSessionDir, "policy", second.PolicyScriptRefs[0].Path))
	if err != nil || !strings.Contains(string(secondBytes), "'v2'") {
		t.Fatalf("second policy snapshot=%q err=%v", secondBytes, err)
	}
	if first.SessionSnapshotID == second.SessionSnapshotID {
		t.Fatalf("policy content change kept session snapshot id %q", first.SessionSnapshotID)
	}

	environmentStore := environment.Store{Root: store.Root}
	for _, runSession := range []RunSession{first, second} {
		if _, err := core.CloseRunSession(runSession); err != nil {
			t.Fatal(err)
		}
		if err := environmentStore.ClearSessionRuntime(runEnvironment.Record.ID, runSession.Layout.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitConfigurationIsImmutableWithinSessionAndChangesForNextSession(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	plan, err = core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
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
	firstBytes, err := os.ReadFile(first.GitConfigPath)
	if err != nil || !strings.Contains(string(firstBytes), "Developer") {
		t.Fatalf("first Git snapshot=%q err=%v", firstBytes, err)
	}

	p, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	p.Git.UserName = "Changed Operator"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	firstAfterEdit, err := os.ReadFile(first.GitConfigPath)
	if err != nil || string(firstAfterEdit) != string(firstBytes) {
		t.Fatalf("running session Git config changed: before=%q after=%q err=%v", firstBytes, firstAfterEdit, err)
	}

	changedPlan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	changedEnvironment, err := core.SelectRunEnvironment(changedPlan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.BeginRunSession(changedPlan, changedEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second.GitConfigPath)
	if err != nil || !strings.Contains(string(secondBytes), "Changed Operator") {
		t.Fatalf("second Git snapshot=%q err=%v", secondBytes, err)
	}
	if first.SessionSnapshotID == second.SessionSnapshotID {
		t.Fatalf("Git configuration change kept session snapshot id %q", first.SessionSnapshotID)
	}

	environmentStore := environment.Store{Root: store.Root}
	for _, runSession := range []RunSession{first, second} {
		if _, err := core.CloseRunSession(runSession); err != nil {
			t.Fatal(err)
		}
		if err := environmentStore.ClearSessionRuntime(runEnvironment.Record.ID, runSession.Layout.ID); err != nil {
			t.Fatal(err)
		}
	}
}
