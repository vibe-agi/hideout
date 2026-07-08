package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestCommandAdapterPlanApplyAndDigestDrift(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("default")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	profileDir := store.ProfileDir("default")
	if err := os.MkdirAll(filepath.Join(profileDir, "adapters"), 0o700); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(profileDir, "adapters", "tool.js")
	if err := os.WriteFile(scriptPath, []byte(`function decideCommandAdapter(){return {outcome:"deny",reason:"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanCommandAdapter(CommandAdapterOptions{
		ProfileName: "default",
		Operation:   "add-local",
		AdapterID:   "tool",
		Path:        "adapters/tool.js",
		Entrypoint:  cmdadapter.DefaultEntrypoint,
		Commands:    []string{"tool-x"},
	})
	if err != nil {
		t.Fatalf("PlanCommandAdapter: %v", err)
	}
	if plan.Digest == "" || !plan.Changed {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if _, err := core.ApplyCommandAdapter(plan); err != nil {
		t.Fatalf("ApplyCommandAdapter: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte(`function decideCommandAdapter(){return {outcome:"deny",reason:"changed"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := core.PlanCommandAdapter(CommandAdapterOptions{ProfileName: "default", Operation: "enable", AdapterID: "tool"}); err == nil {
		t.Fatal("expected digest drift to fail closed")
	}
	refresh, err := core.PlanCommandAdapter(CommandAdapterOptions{ProfileName: "default", Operation: "refresh-digest", AdapterID: "tool"})
	if err != nil {
		t.Fatalf("refresh digest plan: %v", err)
	}
	if refresh.Digest == plan.Digest {
		t.Fatal("expected refreshed digest to change")
	}
	disable, err := core.PlanCommandAdapter(CommandAdapterOptions{ProfileName: "default", Operation: "disable", AdapterID: "tool"})
	if err != nil {
		t.Fatalf("disable plan: %v", err)
	}
	if _, err := core.ApplyCommandAdapter(disable); err != nil {
		t.Fatalf("disable apply: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte(`function decideCommandAdapter(){return {outcome:"deny",reason:"changed-again"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	refreshDisabled, err := core.PlanCommandAdapter(CommandAdapterOptions{ProfileName: "default", Operation: "refresh-digest", AdapterID: "tool"})
	if err != nil {
		t.Fatalf("refresh disabled digest plan: %v", err)
	}
	if refreshDisabled.Enabled {
		t.Fatal("refresh-digest should preserve disabled state")
	}
	if _, err := core.ApplyCommandAdapter(refreshDisabled); err != nil {
		t.Fatalf("refresh disabled digest apply: %v", err)
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CommandAdapters.Adapters["tool"].Enabled {
		t.Fatal("refresh-digest re-enabled disabled adapter")
	}
}

func TestCommandAdapterDuplicateCommandRejected(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	_, err := core.PlanCommandAdapter(CommandAdapterOptions{
		ProfileName: "default",
		Operation:   "add-builtin-root-sensitive",
		AdapterID:   "root-sensitive",
	})
	if err != nil {
		t.Fatalf("builtin plan: %v", err)
	}
	_, err = core.PlanCommandAdapter(CommandAdapterOptions{
		ProfileName: "default",
		Operation:   "add-local",
		AdapterID:   "bad",
		Path:        "missing.js",
		Commands:    []string{"open"},
	})
	if err == nil {
		t.Fatal("expected duplicate command or missing artifact to fail")
	}
}
