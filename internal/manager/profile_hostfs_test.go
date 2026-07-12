package manager

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestProfileHostFSMigrateListRequiresCompleteReviewedMappingAndAppliesAtomically(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("legacy")
	p.HostFS.Grants = []hostfs.Rule{
		{ID: "hfs_old_one", HostPath: t.TempDir(), Ops: []hostfs.Op{hostfs.OpList}, Scope: hostfs.ScopeDir, Reason: "legacy one-level list"},
		{ID: "hfs_old_two", HostPath: t.TempDir(), Ops: []hostfs.Op{hostfs.OpList}, Scope: hostfs.ScopeRecursiveDir, Reason: "legacy tree list"},
	}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	if _, err := core.PlanProfileHostFS(ProfileHostFSOptions{
		ProfileName: "legacy", Operation: "migrate-list", Reason: "reviewed disclosure",
		Migrations: []ProfileHostFSMigration{{RuleID: "hfs_old_one", Selector: "see-dir"}},
	}); err == nil || !strings.Contains(err.Error(), "missing: hfs_old_two") {
		t.Fatalf("incomplete migration should fail closed: %v", err)
	}
	if _, err := core.PlanProfileHostFS(ProfileHostFSOptions{
		ProfileName: "legacy", Operation: "migrate-list", Reason: "reviewed disclosure",
		Migrations: []ProfileHostFSMigration{{RuleID: "hfs_old_one", Selector: "see-dir"}, {RuleID: "not-a-rule", Selector: "see-tree"}},
	}); err == nil || !strings.Contains(err.Error(), "unrelated rule") {
		t.Fatalf("unrelated mapping should fail closed: %v", err)
	}

	plan, err := core.PlanProfileHostFS(ProfileHostFSOptions{
		ProfileName: "legacy", Operation: "migrate-list", Reason: "reviewed disclosure",
		Migrations: []ProfileHostFSMigration{{RuleID: "hfs_old_one", Selector: "see-dir"}, {RuleID: "hfs_old_two", Selector: "see-tree"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RequiresConfirmation || len(plan.Migrations) != 2 || plan.SourceDigest == "" || !strings.Contains(plan.Migrations[0].OldDisclosure, "grant-derived") || !strings.Contains(plan.Migrations[1].NewDisclosure, "coarse") {
		t.Fatalf("migration plan does not expose disclosure change: %+v", plan)
	}
	result, err := core.ApplyProfileHostFS(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatalf("migration not applied: %+v", result)
	}
	loaded, err := store.Load("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.HostFS.Grants) != 2 || loaded.HostFS.Grants[0].ID != "hfs_old_one" || loaded.HostFS.Grants[0].Scope != hostfs.ScopeDir || loaded.HostFS.Grants[1].Scope != hostfs.ScopeRecursiveDir {
		t.Fatalf("migrated rules drifted: %+v", loaded.HostFS.Grants)
	}
	for _, rule := range loaded.HostFS.Grants {
		if !slices.Equal(rule.Ops, []hostfs.Op{hostfs.OpDiscover}) {
			t.Fatalf("legacy list authority remained: %+v", rule)
		}
	}
	auditData, err := os.ReadFile(filepath.Join(store.Root, "operator-center", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditData), `"action":"profile.hostfs.migrate-list"`) || !strings.Contains(string(auditData), `"count":2`) {
		t.Fatalf("migration audit missing: %s", auditData)
	}
}

func TestProfileHostFSMigrateListRejectsDriftAndMalformedRawProfileWithoutWriting(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("legacy")
	p.HostFS.Grants = []hostfs.Rule{{ID: "hfs_old", HostPath: t.TempDir(), Ops: []hostfs.Op{hostfs.OpList}, Scope: hostfs.ScopeDir, Reason: "legacy"}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanProfileHostFS(ProfileHostFSOptions{ProfileName: "legacy", Operation: "migrate-list", Reason: "reviewed", Migrations: []ProfileHostFSMigration{{RuleID: "hfs_old", Selector: "see-dir"}}})
	if err != nil {
		t.Fatal(err)
	}
	p.Metadata = map[string]string{"drift": "true"}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyProfileHostFS(plan); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Fatalf("drifted migration should fail closed: %v", err)
	}
	loaded, err := store.LoadLegacyListCandidate("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(loaded.Profile.HostFS.Grants[0].Ops, hostfs.OpList) {
		t.Fatal("drift failure partially migrated profile")
	}

	data, err := os.ReadFile(store.ProfilePath("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"name": "legacy"`, `"name": "legacy", "unknown": true`, 1))
	if err := os.WriteFile(store.ProfilePath("legacy"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadLegacyListCandidate("legacy"); err == nil {
		t.Fatal("unknown raw profile field should not enter migration path")
	}
}
