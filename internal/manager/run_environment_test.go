package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
)

func TestSelectRunEnvironmentByNameBindsRecord(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	core := New(store)
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	created, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "work", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := core.SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: created.GuestWorkspace,
		RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err != nil {
		t.Fatalf("select by name: %v", err)
	}
	if selected.Record.ID != created.ID || selected.Record.Name != "work" {
		t.Fatalf("wrong record selected: %+v", selected.Record)
	}

	// unknown name points at env list
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: workspace, GuestWorkspace: created.GuestWorkspace, RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "ghost", Create: true})
	if err == nil || !strings.Contains(err.Error(), "env list") {
		t.Fatalf("unknown name should mention env list, got %v", err)
	}

	// record ids are not accepted as names
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: workspace, GuestWorkspace: created.GuestWorkspace, RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: created.ID, Create: true})
	if err == nil {
		t.Fatal("record id must not resolve as a name")
	}

	// conflicting profile fails closed
	other := profile.Default("other")
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: workspace, GuestWorkspace: created.GuestWorkspace, RuntimeProfile: other,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("conflicting profile must fail closed, got %v", err)
	}

	// conflicting backend fails closed
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "native", Workspace: workspace, GuestWorkspace: created.GuestWorkspace, RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("conflicting backend must fail closed, got %v", err)
	}

	// conflicting workspace fails closed
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: t.TempDir(), GuestWorkspace: created.GuestWorkspace, RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("conflicting workspace must fail closed, got %v", err)
	}
}

func TestSelectRunEnvironmentAutoNamedResolution(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	core := New(store)
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	plan := RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: workspace, RuntimeProfile: p}

	first, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	wantName := environment.AutoName(p.Name, workspace)
	if !first.Created || first.Record.Name != wantName || !first.Record.AutoNamed {
		t.Fatalf("first use should create the auto-named environment %q: %+v", wantName, first.Record)
	}
	if first.Record.ImageRef != p.BaseImageOrBuiltin() {
		t.Fatalf("auto-named environment should pin the profile image default: %+v", first.Record)
	}
	if data, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl")); err != nil || !strings.Contains(string(data), `"env.create"`) {
		t.Fatalf("auto-named first-use create must audit env.create: err=%v data=%s", err, data)
	}

	second, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Record.ID != first.Record.ID {
		t.Fatalf("rerun should reuse the auto-named environment: %+v", second.Record)
	}

	// --rm stays record-less
	rm, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{RemoveAfterRun: true, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if rm.Active {
		t.Fatalf("--rm should stay record-less/disposable: %+v", rm)
	}

	// --ephemeral unchanged
	ephPlan := plan
	ephPlan.Ephemeral = true
	eph, err := core.SelectRunEnvironment(ephPlan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if eph.Active {
		t.Fatalf("--ephemeral should stay record-less: %+v", eph)
	}

	// MRU-style flags are gone from the options surface: resuming by id and
	// --new no longer exist. (Compile-time: RunEnvironmentOptions has no such
	// fields; runtime assertion below guards the listing count.)
	envStore := environment.Store{Root: store.Root}
	records, err := envStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("no silent extra environments may exist: %+v", records)
	}
}

func TestSelectionAndLifecycleRejectForeignVersionRecords(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	// plant a foreign-version record whose name would collide with auto-name
	envStore := environment.Store{Root: store.Root}
	id := "env_20260701t000000zaabbccddee0000000001"
	dir := filepath.Join(store.Root, "environments", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"version":"hideout.environment/v1","id":"` + id + `","profile":"default","backend":"lima","workspace":"` + workspace + `","guestWorkspace":"` + workspace + `","status":"ready","createdAt":"2026-07-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	// selection ignores foreign records and creates fresh
	selected, err := core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: workspace, RuntimeProfile: p}, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatalf("selection must not read through foreign records: %v", err)
	}
	if selected.Record.ID == id {
		t.Fatal("selection must never reuse a foreign-version record")
	}

	// lifecycle ops on the foreign id stop with guidance
	_, err = core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{id}})
	if err == nil || !errors.Is(err, environment.ErrUnsupportedVersion) {
		t.Fatalf("stop plan should reject foreign record with guidance, got %v", err)
	}
	if _, err := envStore.Load(id); !errors.Is(err, environment.ErrUnsupportedVersion) {
		t.Fatalf("load should reject foreign record, got %v", err)
	}
	// listing shows it as unsupported
	records, err := envStore.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range records {
		if rec.ID == id && rec.Status == environment.StatusUnsupportedVersion {
			found = true
		}
	}
	if !found {
		t.Fatalf("foreign record should list as unsupported-version: %+v", records)
	}
}

func TestDriftReportAxesAndSameFileWorkspace(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "drifty", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}

	// backendConfig drift: pinned and current values named, recreate hint given
	envStore := environment.Store{Root: store.Root}
	rec.BackendConfigVersion = "lima-config/old"
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}
	_, err = core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: rec.GuestWorkspace, RuntimeProfile: p}, RunEnvironmentOptions{EnvName: "drifty", Create: true})
	if err == nil {
		t.Fatal("backendConfig drift must fail closed")
	}
	msg := err.Error()
	for _, want := range []string{"backendConfig", "lima-config/old", "hideout env recreate drifty"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("drift report missing %q: %s", want, msg)
		}
	}
	// audit env.drift.denied
	data, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl"))
	if err != nil || !strings.Contains(string(data), `"env.drift.denied"`) {
		t.Fatalf("env.drift.denied audit missing: err=%v data=%s", err, data)
	}

	// workspace drift via a genuinely different directory
	rec.BackendConfigVersion = backendConfigVersion("lima")
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}
	otherWS := t.TempDir()
	_, err = core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: otherWS, GuestWorkspace: otherWS, RuntimeProfile: p}, RunEnvironmentOptions{EnvName: "drifty", Create: true})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("workspace drift must fail closed naming the axis, got %v", err)
	}

	// a symlink to the pinned workspace is the same file identity: no drift
	linked := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(workspace, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: linked, GuestWorkspace: rec.GuestWorkspace, RuntimeProfile: p}, RunEnvironmentOptions{EnvName: "drifty", Create: true}); err != nil {
		t.Fatalf("same file identity must not drift: %v", err)
	}
}

func TestRecreateEnvironmentGuardForceAndRefresh(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	rec, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "rebuild", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	envStore := environment.Store{Root: store.Root}
	rec.BackendConfigVersion = "lima-config/old"
	rec.Status = "running"
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}

	// running without force: refuse with stop hint
	_, err = core.RecreateEnvironment(context.Background(), "rebuild", false, EnvironmentApplyOptions{Operator: fakeEnvOperator{}})
	if err == nil || !strings.Contains(err.Error(), "hideout stop rebuild") {
		t.Fatalf("running guest must refuse recreate without force, got %v", err)
	}

	// force: stop then rebuild under the same name and id, refreshed config
	rebuilt, err := core.RecreateEnvironment(context.Background(), "rebuild", true, EnvironmentApplyOptions{Operator: fakeEnvOperator{}})
	if err != nil {
		t.Fatalf("forced recreate: %v", err)
	}
	if rebuilt.Name != "rebuild" || rebuilt.ID != rec.ID {
		t.Fatalf("recreate must keep name and record id: %+v", rebuilt)
	}
	if rebuilt.BackendConfigVersion != backendConfigVersion("lima") {
		t.Fatalf("recreate must refresh backend config version: %+v", rebuilt)
	}
	if rebuilt.ImageRef != rec.ImageRef || rebuilt.Workspace != rec.Workspace {
		t.Fatalf("recreate must keep pinned declaration and workspace: %+v", rebuilt)
	}
	if rebuilt.Status != "ready" {
		t.Fatalf("recreate should leave a ready record: %+v", rebuilt)
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl"))
	if err != nil || !strings.Contains(string(data), `"env.recreate"`) || !strings.Contains(string(data), `"force":true`) {
		t.Fatalf("env.recreate audit with force missing: %s", data)
	}

	// remove with force on a running guest stops then removes
	rec2, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "removable", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	rec2.Status = "running"
	if err := envStore.Save(rec2); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RemoveEnvironment(context.Background(), "removable", false, EnvironmentApplyOptions{Operator: fakeEnvOperator{}}); err == nil {
		t.Fatal("remove without force must refuse a running guest")
	}
	if _, err := core.RemoveEnvironment(context.Background(), "removable", true, EnvironmentApplyOptions{Operator: fakeEnvOperator{}}); err != nil {
		t.Fatalf("forced remove: %v", err)
	}
}

func TestSelectRunEnvironmentRechecksPinnedRuntimeDiskBeforeCreate(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p, err := store.LoadOrInit("runtime")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := testManagerRuntimeResolver(runtimecatalog.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	p.Environment.BaseImage = ""
	p.Environment.Runtime = &resolved.Provenance
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	plan := RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: workspace, RuntimeProfile: p}
	core := New(store)
	core.RuntimeDiskCheck = func(string, int64) error { return errors.New("available 1 byte") }
	if _, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true}); err == nil || !strings.Contains(err.Error(), "runtime.disk.insufficient") {
		t.Fatalf("run should fail before environment creation on low disk, got %v", err)
	}
	records, err := (environment.Store{Root: store.Root}).List()
	if err != nil || len(records) != 0 {
		t.Fatalf("low-disk run wrote environment state: records=%+v err=%v", records, err)
	}
	core.RuntimeDiskCheck = func(string, int64) error { return nil }
	selected, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Record.Runtime == nil || *selected.Record.Runtime != resolved.Provenance {
		t.Fatalf("auto environment lost pinned runtime provenance: %+v", selected.Record)
	}
}
