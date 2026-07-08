package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/adapterpack"
	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestAdapterPackInstallTestEnablePinsProfileBinding(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	packDir := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked unsafe"} }`)
	installPlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: packDir})
	if err != nil {
		t.Fatalf("install plan: %v", err)
	}
	installResult, err := core.ApplyAdapterPack(installPlan)
	if err != nil {
		t.Fatalf("install apply: %v", err)
	}
	if installResult.Entry == nil || installResult.Revision == nil {
		t.Fatalf("install result missing entry/revision: %+v", installResult)
	}
	if profiles, _ := filepath.Glob(filepath.Join(store.Root, "profiles", "*")); len(profiles) != 0 {
		t.Fatalf("install should not create or mutate profiles: %v", profiles)
	}
	testPlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "test", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID})
	if err != nil {
		t.Fatalf("test plan: %v", err)
	}
	testResult, err := core.ApplyAdapterPack(testPlan)
	if err != nil {
		t.Fatalf("test apply: %v", err)
	}
	if testResult.Test == nil || testResult.Test.Status != adapterpack.TestPassed {
		t.Fatalf("expected passing pack tests: %+v", testResult)
	}
	enablePlan, err := core.PlanAdapterPack(AdapterPackOptions{
		Operation:        "enable",
		ProfileName:      "default",
		PackID:           "example.pack",
		RevisionID:       installResult.Revision.RevisionID,
		AdapterID:        "tool",
		CommandAdapterID: "pack-tool",
	})
	if err != nil {
		t.Fatalf("enable plan: %v", err)
	}
	if _, err := core.ApplyAdapterPack(enablePlan); err != nil {
		t.Fatalf("enable apply: %v", err)
	}
	p, err := store.Load("default")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	adapter := p.CommandAdapters.Adapters["pack-tool"]
	if adapter.PackID != "example.pack" || adapter.PackRevisionID != installResult.Revision.RevisionID || adapter.PackAdapterID != "tool" || adapter.PackLockDigest != installResult.Revision.SourceDigest {
		t.Fatalf("profile binding not pinned: %+v", adapter)
	}
	if _, err := cmdadapter.CompileWithResolver(p, store.ProfileDir("default"), adapterpack.RuntimeResolver{Store: adapterpack.NewStore(store.Root)}); err != nil {
		t.Fatalf("runtime compile with pack resolver: %v", err)
	}
}

func TestAdapterPackLifecycleEvidenceIsAuditable(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	packDir := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked unsafe"} }`)
	plan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: packDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyAdapterPack(plan); err != nil {
		t.Fatal(err)
	}
	events, err := core.AuditEvents(AuditEventFilter{Action: adapterpack.Action, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("adapter pack lifecycle audit event missing")
	}
	details := events[0].Details
	if details["packId"] != "example.pack" || details["operation"] != "install" {
		t.Fatalf("unexpected adapter pack audit details: %+v", details)
	}
	data, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cap_0123456789abcdef") {
		t.Fatalf("control-plane token leaked in audit details: %s", data)
	}
}

func TestAdapterPackEnableRequiresPassingTests(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	packDir := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked unsafe"} }`)
	installPlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: packDir})
	if err != nil {
		t.Fatal(err)
	}
	installResult, err := core.ApplyAdapterPack(installPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.PlanAdapterPack(AdapterPackOptions{
		Operation:   "enable",
		ProfileName: "default",
		PackID:      "example.pack",
		RevisionID:  installResult.Revision.RevisionID,
		AdapterID:   "tool",
	})
	if err == nil {
		t.Fatal("expected enable to require passing pack tests")
	}
}

func TestAdapterPackEnableRejectsUndeclaredCommandsAndCapabilities(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	packDir := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked unsafe"} }`)
	installPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: packDir})
	installResult, err := core.ApplyAdapterPack(installPlan)
	if err != nil {
		t.Fatal(err)
	}
	testPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "test", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID})
	if _, err := core.ApplyAdapterPack(testPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := core.PlanAdapterPack(AdapterPackOptions{
		Operation:   "enable",
		ProfileName: "default",
		PackID:      "example.pack",
		RevisionID:  installResult.Revision.RevisionID,
		AdapterID:   "tool",
		Commands:    []string{"tool-x", "tool-y"},
	}); err == nil {
		t.Fatal("expected undeclared command rejection")
	}
	if _, err := core.PlanAdapterPack(AdapterPackOptions{
		Operation:                   "enable",
		ProfileName:                 "default",
		PackID:                      "example.pack",
		RevisionID:                  installResult.Revision.RevisionID,
		AdapterID:                   "tool",
		AllowedProposalCapabilities: []string{cmdadapter.CapabilityHostFSWritePlan},
	}); err == nil {
		t.Fatal("expected undeclared capability rejection")
	}
	if _, err := store.Load("default"); err == nil {
		t.Fatal("rejected enable should not create profile authority")
	}
}

func TestAdapterPackRevokeMakesRuntimeResolutionFailClosed(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	packDir := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked unsafe"} }`)
	installPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: packDir})
	installResult, err := core.ApplyAdapterPack(installPlan)
	if err != nil {
		t.Fatal(err)
	}
	testPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "test", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID})
	if _, err := core.ApplyAdapterPack(testPlan); err != nil {
		t.Fatal(err)
	}
	revokePlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "revoke", PackID: "example.pack"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyAdapterPack(revokePlan); err != nil {
		t.Fatal(err)
	}
	if _, err := adapterpack.NewStore(store.Root).ResolveRuntime("example.pack", installResult.Revision.RevisionID, "tool", installResult.Revision.SourceDigest); err == nil {
		t.Fatal("expected revoked pack to fail closed at runtime resolution")
	}
}

func TestAdapterPackUpgradeLeavesActiveProfilePinnedUntilReenabled(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	v1 := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked v1"} }`)
	installPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: v1})
	installResult, err := core.ApplyAdapterPack(installPlan)
	if err != nil {
		t.Fatal(err)
	}
	testPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "test", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID})
	if _, err := core.ApplyAdapterPack(testPlan); err != nil {
		t.Fatal(err)
	}
	enablePlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "enable", ProfileName: "default", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID, AdapterID: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyAdapterPack(enablePlan); err != nil {
		t.Fatal(err)
	}

	v2 := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked v2"} }`)
	upgradePlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "upgrade", SourceKind: adapterpack.SourceLocal, SourcePath: v2})
	upgradeResult, err := core.ApplyAdapterPack(upgradePlan)
	if err != nil {
		t.Fatal(err)
	}
	if upgradeResult.Revision.RevisionID == installResult.Revision.RevisionID {
		t.Fatal("upgrade should install a distinct revision")
	}
	entry, err := core.InspectAdapterPack("example.pack")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ActiveRevisionID != upgradeResult.Revision.RevisionID {
		t.Fatalf("registry active revision not upgraded: %+v", entry)
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.CommandAdapters.Adapters["tool"].PackRevisionID; got != installResult.Revision.RevisionID {
		t.Fatalf("profile binding drifted to upgraded revision: got %s want %s", got, installResult.Revision.RevisionID)
	}
}

func TestAdapterPackDisableAndBuiltInMetadata(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	packDir := writeManagerPackFixture(t, `function decideCommandAdapter() { return {outcome: "deny", reason: "blocked unsafe"} }`)
	installPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "install", SourceKind: adapterpack.SourceLocal, SourcePath: packDir})
	installResult, err := core.ApplyAdapterPack(installPlan)
	if err != nil {
		t.Fatal(err)
	}
	testPlan, _ := core.PlanAdapterPack(AdapterPackOptions{Operation: "test", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID})
	if _, err := core.ApplyAdapterPack(testPlan); err != nil {
		t.Fatal(err)
	}
	enablePlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "enable", ProfileName: "default", PackID: "example.pack", RevisionID: installResult.Revision.RevisionID, AdapterID: "tool", CommandAdapterID: "pack-tool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyAdapterPack(enablePlan); err != nil {
		t.Fatal(err)
	}
	disablePlan, err := core.PlanAdapterPack(AdapterPackOptions{Operation: "disable", ProfileName: "default", CommandAdapterID: "pack-tool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyAdapterPack(disablePlan); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CommandAdapters.Adapters["pack-tool"].Enabled {
		t.Fatal("adapter pack binding should be disabled")
	}
	builtin, err := core.InspectAdapterPack(cmdadapter.BuiltinRootSensitiveID)
	if err != nil {
		t.Fatal(err)
	}
	if !builtin.BuiltIn || builtin.State != adapterpack.StateBuiltin {
		t.Fatalf("built-in metadata mismatch: %+v", builtin)
	}
	if _, err := core.ApplyAdapterPack(AdapterPackPlan{Version: adapterpack.PlanVersion, Operation: "revoke", PackID: cmdadapter.BuiltinRootSensitiveID}); err == nil {
		t.Fatal("expected built-in mutation to be rejected")
	}
}

func writeManagerPackFixture(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := adapterpack.Manifest{
		SchemaVersion: adapterpack.ManifestVersion,
		ID:            "example.pack",
		Version:       "1.0.0",
		Adapters: []adapterpack.AdapterSpec{{
			ID:                          "tool",
			Script:                      "adapter.js",
			Entrypoint:                  cmdadapter.DefaultEntrypoint,
			Commands:                    []string{"tool-x"},
			AllowedProposalCapabilities: []string{cmdadapter.CapabilityGuestPrivPlan},
		}},
		Tests: []adapterpack.TestVector{{
			ID:        "denies",
			AdapterID: "tool",
			Context: adapterpack.TestContext{Command: adapterpack.TestCommand{
				Name: "tool-x",
				Argv: []string{"tool-x", "--unsafe"},
				CWD:  "/workspace",
			}},
			Expect: adapterpack.TestExpect{Outcome: "deny", ReasonContains: "blocked"},
		}},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, adapterpack.ManifestFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "adapter.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
