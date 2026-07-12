package hostapppack_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostapppack"
)

func TestInspectionAndCommandPlanningStayWithinProductBudgets(t *testing.T) {
	if cmdproxy.MaxProjectedHostAppCommands != hostapppack.MaxCommandsPerProfile {
		t.Fatalf("planner limit=%d model limit=%d", cmdproxy.MaxProjectedHostAppCommands, hostapppack.MaxCommandsPerProfile)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "manifest.valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	const iterations = 20
	inspectStart := time.Now()
	for range iterations {
		manifest, err := hostapppack.DecodeManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hostapppack.EffectivePermissionFingerprint(manifest, hostapppack.EffectivePermissionContext{Access: hostapppack.AccessAskEachRun}); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(inspectStart); elapsed > 500*time.Millisecond {
		t.Fatalf("%d local inspect/permission plans took %s, budget 500ms", iterations, elapsed)
	}

	owners := make([]cmdproxy.HostAppCommandOwner, cmdproxy.MaxProjectedHostAppCommands)
	for i := range owners {
		owners[i] = cmdproxy.HostAppCommandOwner{
			Command: fmt.Sprintf("editor-%02d", i),
			Owner:   fmt.Sprintf("community.editor-%d/binding-%02d", i/16, i),
		}
	}
	compileStart := time.Now()
	for range iterations {
		plan, err := cmdproxy.PlanHostAppCommandOwners(nil, owners, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Owners) != len(owners) {
			t.Fatalf("compiled owners=%d want %d", len(plan.Owners), len(owners))
		}
	}
	if elapsed := time.Since(compileStart); elapsed > 100*time.Millisecond {
		t.Fatalf("%d 64-command ownership plans took %s, budget 100ms", iterations, elapsed)
	}
}
