package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// T007: single-instance lifecycle — start, status, second start refused, ordered
// stop leaves no socket/lock, and a restart after a clean stop succeeds.
func TestDaemonSingleInstanceLifecycle(t *testing.T) {
	store := testStore(t)
	d, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := d.Status(); st.State != "serving" || st.Version != statusVersion {
		t.Fatalf("unexpected status: %+v", st)
	}
	if _, err := Start(Options{Store: store}); !IsAlreadyRunning(err) {
		t.Fatalf("second Start: want already-running, got %v", err)
	}
	sock := d.Socket()
	lock := filepath.Join(d.RuntimeDir(), lockName)
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed after stop: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock should be removed after stop: %v", err)
	}
	d2, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("restart after clean stop: %v", err)
	}
	_ = d2.Stop(context.Background())
}

// T007/T012 (Edge): a stale socket file from a crash is reclaimed on next start.
func TestDaemonReclaimsStaleSocketFile(t *testing.T) {
	store := testStore(t)
	dir, err := ensurePlacement(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, socketName), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("Start over stale socket file must reclaim: %v", err)
	}
	_ = d.Stop(context.Background())
}
