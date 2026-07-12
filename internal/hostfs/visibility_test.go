package hostfs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDiscoverSelectorsAndRejectsGlobsAndLegacyList(t *testing.T) {
	tests := []struct {
		value string
		scope Scope
	}{
		{"see:/Users/alice/Documents/report.txt", ScopeExactFile},
		{"see-dir:/Users/alice/Documents", ScopeDir},
		{"see-tree:/Users/alice/src", ScopeRecursiveDir},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			rule, err := ParseRuleSpec("--fs", tt.value, "navigate")
			if err != nil {
				t.Fatalf("ParseRuleSpec: %v", err)
			}
			if rule.Scope != tt.scope || len(rule.Ops) != 1 || rule.Ops[0] != OpDiscover {
				t.Fatalf("unexpected discover rule: %+v", rule)
			}
		})
	}
	for _, value := range []string{
		"see:/Users/alice/*.txt",
		"see-dir:/Users/alice/Doc?ments",
		"see-tree:/Users/alice/[ab]",
	} {
		if _, err := ParseRuleSpec("--fs", value, "navigate"); err == nil || !strings.Contains(err.Error(), "does not support glob") {
			t.Fatalf("expected discover glob rejection for %q, got %v", value, err)
		}
	}
	if _, err := ParseRuleSpec("--fs", "list:/Users/alice/Documents", "legacy"); err == nil || !strings.Contains(err.Error(), "migrate-list") {
		t.Fatalf("expected guided legacy list rejection, got %v", err)
	}
}

func TestVisibilityScopesAreExactOneLevelAndRecursive(t *testing.T) {
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{
		{ID: "hfs_exact", HostPath: "/Users/alice/exact.txt", Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "exact"},
		{ID: "hfs_dir", HostPath: "/Users/alice/Documents", Ops: []Op{OpDiscover}, Scope: ScopeDir, Reason: "one level"},
		{ID: "hfs_tree", HostPath: "/Users/alice/src", Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "tree"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path       string
		state      VisibilityState
		explicit   bool
		enumerable bool
	}{
		{"/Users/alice/exact.txt", VisibilityExact, true, false},
		{"/Users/alice/Documents", VisibilityEnumerable, true, true},
		{"/Users/alice/Documents/one.txt", VisibilityExact, true, false},
		{"/Users/alice/Documents/nested/two.txt", VisibilityHidden, false, false},
		{"/Users/alice/src", VisibilityEnumerable, true, true},
		{"/Users/alice/src/a", VisibilityEnumerable, true, true},
		{"/Users/alice/src/a/b.txt", VisibilityEnumerable, true, true},
		{"/Users/alice/other", VisibilityHidden, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := policy.Visibility(tt.path)
			if got.State != tt.state || got.ExplicitDomain != tt.explicit || got.Enumerable != tt.enumerable {
				t.Fatalf("Visibility(%q)=%+v", tt.path, got)
			}
		})
	}
}

func TestDiscoverDenySuppressesDiscoveryButNotExactContentGrant(t *testing.T) {
	path := "/Users/alice/.ssh/config"
	policy, err := Build(BuildInput{Profile: Config{
		Grants: []Rule{
			readGrant(path, ScopeExactFile, "operator exact read"),
			{ID: "hfs_home", HostPath: "/Users/alice", Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "home names"},
		},
		Deny: []Rule{{ID: "hfs_hide_ssh", HostPath: "/Users/alice/.ssh", Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "broad discovery exclusion"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Decide(OpRead, path); !decision.Allowed {
		t.Fatalf("discover deny revoked exact read: %+v", decision)
	}
	visibility := policy.Visibility(path)
	if visibility.State != VisibilityContentGranted || !visibility.DiscoverDenied || !visibility.ExplicitDomain {
		t.Fatalf("exact content visibility was not preserved: %+v", visibility)
	}
}

func TestReservedRootRemainsAbsoluteForDiscoverAndContent(t *testing.T) {
	store := filepath.Join(t.TempDir(), ".hideout")
	legacy := EffectivePolicy{
		ReservedRoots: []string{store},
		Grants: []Grant{
			{Rule: Rule{ID: "hfs_see", HostPath: store, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "bad legacy"}},
			{Rule: readGrant(filepath.Join(store, "profile.json"), ScopeExactFile, "bad legacy")},
		},
	}
	path := filepath.Join(store, "profile.json")
	visibility := legacy.Visibility(path)
	if visibility.State != VisibilityHidden || !visibility.ReservedHidden {
		t.Fatalf("reserved path became visible: %+v", visibility)
	}
	if decision := legacy.Decide(OpRead, path); decision.Allowed || decision.Reason != ReservedRootReason {
		t.Fatalf("reserved content became readable: %+v", decision)
	}
}
