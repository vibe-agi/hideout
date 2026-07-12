package manager

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestDiscoverOnlyPolicyMaterializesHostFSGraft(t *testing.T) {
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: hostfs.Config{Grants: []hostfs.Rule{{
		ID: "hfs_see", HostPath: "/Users/alice/Documents", Ops: []hostfs.Op{hostfs.OpDiscover}, Scope: hostfs.ScopeDir, Reason: "navigate",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Grants) != 1 {
		t.Fatalf("discover policy was not active: %+v", policy)
	}
	if got := HostFSGrafts(policy); !reflect.DeepEqual(got, []string{"/Users/alice/Documents"}) {
		t.Fatalf("discover grafts=%v", got)
	}
}

func TestProfileHostFSPlanAndApplyDiscoverRule(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	root := filepath.Join(t.TempDir(), "Documents")
	plan, err := core.PlanProfileHostFS(ProfileHostFSOptions{
		ProfileName: "discover",
		Operation:   "add",
		Rule:        "see-dir:" + root,
		Reason:      "navigate selected documents",
	})
	if err != nil {
		t.Fatalf("PlanProfileHostFS: %v", err)
	}
	if plan.PlannedRule == nil || !reflect.DeepEqual(plan.PlannedRule.Ops, []hostfs.Op{hostfs.OpDiscover}) || plan.PlannedRule.Scope != hostfs.ScopeDir {
		t.Fatalf("plan did not expose discover posture: %+v", plan)
	}
	result, err := core.ApplyProfileHostFS(plan)
	if err != nil {
		t.Fatalf("ApplyProfileHostFS: %v", err)
	}
	if !result.Applied || len(result.Grants) != 1 || result.Grants[0].Ops[0] != hostfs.OpDiscover {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	loaded, err := store.Load("discover")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.HostFS.Grants) != 1 || loaded.HostFS.Grants[0].Scope != hostfs.ScopeDir {
		t.Fatalf("discover rule was not persisted: %+v", loaded.HostFS)
	}
}
