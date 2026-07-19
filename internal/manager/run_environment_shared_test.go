package manager

import (
	"errors"
	"sync"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestSharedAutomaticFirstRunConvergesOnOneStableSlot(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := sharedRunProfile(t, store, "default")
	workspaces := []string{t.TempDir(), t.TempDir()}

	const callers = 16
	results := make(chan RunEnvironment, callers)
	errorsOut := make(chan error, callers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < callers; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			result, err := selectAutomaticRunEnvironmentForPlatform(
				environment.Store{Root: store.Root}, p, "lima", workspaces[index%len(workspaces)], "/workspace",
				false, RunEnvironmentOptions{Create: true}, "darwin", "arm64",
			)
			if err != nil {
				errorsOut <- err
				return
			}
			results <- result
		}(i)
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsOut)
	for err := range errorsOut {
		t.Errorf("concurrent automatic selection: %v", err)
	}
	var environmentID string
	for result := range results {
		if !result.Active || result.Record.Mode != environment.ModeShared || result.Record.Name != "default" ||
			result.Record.SharedSlot != environment.SharedSlotID("default") {
			t.Fatalf("unexpected shared selection: %+v", result)
		}
		if environmentID == "" {
			environmentID = result.Record.ID
		} else if result.Record.ID != environmentID {
			t.Fatalf("concurrent first run created multiple environments: %s and %s", environmentID, result.Record.ID)
		}
	}
	records, err := (environment.Store{Root: store.Root}).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != environmentID {
		t.Fatalf("stable slot contains %d records: %+v", len(records), records)
	}
	if _, _, ok := records[0].WorkspaceBinding(); ok {
		t.Fatalf("shared machine retained a project binding: %+v", records[0])
	}
}

func TestSharedSlotReportsMachineDriftButIgnoresSessionFacts(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := sharedRunProfile(t, store, "default")
	firstWorkspace := t.TempDir()
	secondWorkspace := t.TempDir()
	first, err := selectAutomaticRunEnvironmentForPlatform(
		environment.Store{Root: store.Root}, p, "lima", firstWorkspace, "/workspace", false,
		RunEnvironmentOptions{Create: true}, "darwin", "arm64",
	)
	if err != nil {
		t.Fatal(err)
	}

	second, err := selectAutomaticRunEnvironmentForPlatform(
		environment.Store{Root: store.Root}, p, "lima", secondWorkspace, "/workspace", false,
		RunEnvironmentOptions{Create: true}, "darwin", "arm64",
	)
	if err != nil {
		t.Fatalf("workspace/session change drifted shared slot: %v", err)
	}
	if second.Record.ID != first.Record.ID {
		t.Fatalf("session-only change selected another machine: %s != %s", second.Record.ID, first.Record.ID)
	}

	drifted := p
	drifted.Environment.BaseImage = "template:_images/debian-12"
	_, err = selectAutomaticRunEnvironmentForPlatform(
		environment.Store{Root: store.Root}, drifted, "lima", secondWorkspace, "/workspace", false,
		RunEnvironmentOptions{Create: true}, "darwin", "arm64",
	)
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("machine posture change did not report drift: %v", err)
	}
	found := false
	for _, axis := range drift.Axes {
		if axis.Axis == "machine" {
			found = true
		}
	}
	if !found {
		t.Fatalf("machine identity axis missing: %+v", drift.Axes)
	}
	records, listErr := (environment.Store{Root: store.Root}).List()
	if listErr != nil || len(records) != 1 {
		t.Fatalf("drift silently created another automatic machine: records=%+v err=%v", records, listErr)
	}
}

func sharedRunProfile(t *testing.T, store profile.Store, name string) profile.Profile {
	t.Helper()
	p, err := store.LoadOrInit(name)
	if err != nil {
		t.Fatal(err)
	}
	p.Workspace.PathMode = "alias"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	return p
}
