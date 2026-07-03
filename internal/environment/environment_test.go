package environment

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCreateLatestAndResolve(t *testing.T) {
	store := Store{Root: t.TempDir()}
	spec := Spec{
		Profile:        "default",
		Backend:        "lima",
		Workspace:      filepath.Join(t.TempDir(), "workspace"),
		GuestWorkspace: "/workspace",
		ProfileID:      "prf_1111",
		IdentityID:     "id_2222",
		User:           "developer",
		Hostname:       "devbox",
	}
	rec, err := store.Create(spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.InstanceName = "hideout-default-env-test"
	rec.LastStartedAt = time.Now().UTC()
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Latest(spec)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !ok || got.ID != rec.ID || got.InstanceName != rec.InstanceName {
		t.Fatalf("Latest=%+v ok=%t want %+v", got, ok, rec)
	}
	short := rec.ID[len("env_") : len("env_")+12]
	loaded, err := store.Load(short)
	if err != nil {
		t.Fatalf("Load short prefix: %v", err)
	}
	if loaded.ID != rec.ID {
		t.Fatalf("Load short prefix ID=%s want %s", loaded.ID, rec.ID)
	}
	spec.IdentityID = "id_changed"
	if _, ok, err := store.Latest(spec); err != nil || ok {
		t.Fatalf("Latest should not match changed identity: ok=%t err=%v", ok, err)
	}
}

func TestClearRuntimePreservesMountRootsAndRemovesContents(t *testing.T) {
	store := Store{Root: t.TempDir()}
	rec, err := store.Create(Spec{
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, rel := range []string{
		"tmp/value",
		"shims/open",
		"network/proxy.url",
		"bootstrap/bootstrap.sh",
		"lima.yaml",
	} {
		path := filepath.Join(store.RuntimeDir(rec.ID), rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ClearRuntime(rec.ID); err != nil {
		t.Fatalf("ClearRuntime: %v", err)
	}
	for _, dir := range []string{"tmp", "shims", "network", "bootstrap"} {
		path := filepath.Join(store.RuntimeDir(rec.ID), dir)
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatalf("runtime mount root %s missing: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("runtime mount root %s should be empty, got %v", dir, entries)
		}
	}
	if _, err := os.Stat(filepath.Join(store.RuntimeDir(rec.ID), "lima.yaml")); !os.IsNotExist(err) {
		t.Fatalf("runtime root files should be removed, err=%v", err)
	}
}
