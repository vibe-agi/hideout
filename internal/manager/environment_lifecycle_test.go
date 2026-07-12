package manager

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
)

func TestCreateEnvironmentPersistsExplicitRuntimeProvenance(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	var checkedPath string
	var checkedBytes int64
	core := New(store)
	core.RuntimeResolver = testManagerRuntimeResolver
	core.RuntimeDiskCheck = func(path string, required int64) error {
		checkedPath, checkedBytes = path, required
		return nil
	}
	record, err := core.CreateEnvironment(EnvironmentCreateOptions{
		Name: "work", RuntimeFamily: "developer-standard", Profile: "default", Backend: "lima", Workspace: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Runtime == nil || record.Runtime.Family != "developer-standard" || record.ImageRef != record.Runtime.ImageRef() {
		t.Fatalf("runtime provenance not persisted: %+v", record)
	}
	if checkedPath == "" || checkedBytes != (512<<20)+(12<<30)+(1<<30) {
		t.Fatalf("disk precheck path=%q bytes=%d", checkedPath, checkedBytes)
	}
	loaded, err := (environment.Store{Root: store.Root}).LoadByName("work")
	if err != nil || loaded.Runtime == nil || *loaded.Runtime != *record.Runtime {
		t.Fatalf("loaded runtime record=%+v err=%v", loaded, err)
	}
}

func TestEnvironmentCreatePlanIsReadOnlyAndApplyRevalidatesCatalog(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	core.RuntimeResolver = testManagerRuntimeResolver
	core.RuntimeDiskCheck = func(string, int64) error { return nil }
	plan, err := core.PlanEnvironmentCreate(EnvironmentCreateOptions{
		Name: "work", RuntimeFamily: "developer-standard", Profile: "planned", Backend: "lima", Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ProfileWillCreate || plan.Runtime == nil || plan.RuntimeDownloadBytes == 0 || plan.RuntimeVirtualBytes == 0 {
		t.Fatalf("incomplete create plan: %+v", plan)
	}
	if _, err := store.Load("planned"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan wrote profile state: %v", err)
	}
	if records, err := (environment.Store{Root: store.Root}).List(); err != nil || len(records) != 0 {
		t.Fatalf("plan wrote environment state: records=%+v err=%v", records, err)
	}
	core.RuntimeResolver = func(selection runtimecatalog.Selection) (runtimecatalog.Resolution, error) {
		resolved, err := testManagerRuntimeResolver(selection)
		resolved.Provenance.ArtifactSHA256 = strings.Repeat("b", 64)
		resolved.ImageRef = resolved.Provenance.ImageRef()
		return resolved, err
	}
	if _, err := core.ApplyEnvironmentCreate(plan); err == nil || !strings.Contains(err.Error(), "catalog changed") {
		t.Fatalf("catalog drift should fail apply, got %v", err)
	}
	if _, err := store.Load("planned"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed apply wrote profile state: %v", err)
	}
}

func TestCreateEnvironmentRuntimeFailuresWriteNoEnvironment(t *testing.T) {
	for name, mutate := range map[string]func(*Core, *EnvironmentCreateOptions){
		"runtime plus image": func(_ *Core, opts *EnvironmentCreateOptions) { opts.ImageRef = environment.BuiltinBaseImage },
		"unsupported runtime": func(core *Core, _ *EnvironmentCreateOptions) {
			core.RuntimeResolver = func(runtimecatalog.Selection) (runtimecatalog.Resolution, error) {
				return runtimecatalog.Resolution{}, errors.New("unsupported tuple")
			}
		},
		"indeterminate disk": func(core *Core, _ *EnvironmentCreateOptions) {
			core.RuntimeDiskCheck = func(string, int64) error { return errors.New("statfs unavailable") }
		},
		"insufficient disk": func(core *Core, _ *EnvironmentCreateOptions) {
			core.RuntimeDiskCheck = func(string, int64) error { return errors.New("available 1 byte") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := profile.Store{Root: t.TempDir()}
			core := New(store)
			core.RuntimeResolver = testManagerRuntimeResolver
			core.RuntimeDiskCheck = func(string, int64) error { return nil }
			opts := EnvironmentCreateOptions{
				Name: "work", RuntimeFamily: "developer-standard", Profile: "default", Backend: "lima", Workspace: t.TempDir(),
			}
			mutate(&core, &opts)
			if _, err := core.CreateEnvironment(opts); err == nil {
				t.Fatal("expected fail-closed environment creation")
			}
			records, err := (environment.Store{Root: store.Root}).List()
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 0 {
				t.Fatalf("failed runtime create wrote environment records: %+v", records)
			}
		})
	}
}

func TestCreateEnvironmentRuntimeRequiresLima(t *testing.T) {
	core := New(profile.Store{Root: t.TempDir()})
	core.RuntimeResolver = testManagerRuntimeResolver
	if _, err := core.CreateEnvironment(EnvironmentCreateOptions{
		Name: "work", RuntimeFamily: "developer-standard", Backend: "native", Workspace: t.TempDir(),
	}); err == nil || !strings.Contains(err.Error(), "Lima") {
		t.Fatalf("native runtime selection should fail, got %v", err)
	}
}

func testManagerRuntimeResolver(selection runtimecatalog.Selection) (runtimecatalog.Resolution, error) {
	guestArch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[runtime.GOARCH]
	provenance := environment.RuntimeProvenance{
		Family: "developer-standard", Revision: "2026.07.0", CatalogRelease: "2026.07.0",
		ContractID: "developer-standard/v1", ContractDigest: "sha256:" + strings.Repeat("c", 64),
		ArtifactLocation: "https://example.invalid/runtime/2026.07.0/developer-standard.qcow2",
		ArtifactSHA256:   strings.Repeat("a", 64), PackageInventoryDigest: "sha256:" + strings.Repeat("f", 64),
		HostOS: runtime.GOOS, HostArch: runtime.GOARCH, GuestArch: guestArch, Maturity: "preview",
		DownloadBytes: 512 << 20, VirtualBytes: 12 << 30,
	}
	return runtimecatalog.Resolution{
		ImageRef: provenance.ImageRef(), Provenance: provenance,
		Artifact: runtimecatalog.Artifact{DownloadBytes: 512 << 20, VirtualBytes: 12 << 30, PackageInventoryDigest: provenance.PackageInventoryDigest},
	}, nil
}
