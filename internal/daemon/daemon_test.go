package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// T007: single-instance lifecycle — start, status, second start refused, ordered
// stop removes the socket, releases the stable lock inode, and permits restart.
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
	lockInfo, err := os.Stat(lock)
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("stable lock inode should remain private after stop: info=%v err=%v", lockInfo, err)
	}
	d2, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("restart after clean stop: %v", err)
	}
	_ = d2.Stop(context.Background())
}

func TestDaemonLockUsesOneStableInodeAcrossOwners(t *testing.T) {
	store := testStore(t)
	dir, err := ensurePlacement(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lockName)
	first, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock(first, path)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock path disappeared between owners: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock ownership moved to a different inode")
	}
	second, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire stable lock after release: %v", err)
	}
	defer releaseLock(second, path)
	if _, err := acquireLock(path); !IsAlreadyRunning(err) {
		t.Fatalf("same stable inode admitted a competing owner: %v", err)
	}
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

func TestDaemonStopClosesProcessScopedBackendResourcesOnce(t *testing.T) {
	store := testStore(t)
	closed := 0
	d, err := Start(Options{Store: store, BackendShutdown: func() error {
		closed++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("backend shutdown calls=%d want 1", closed)
	}
}

func TestDaemonStopsWhenCredentialRuntimeDirectoryDisappears(t *testing.T) {
	store := testStore(t)
	d, err := Start(Options{Store: store, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	if err := os.RemoveAll(d.RuntimeDir()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop after its credential runtime directory disappeared")
	}
}

func TestDaemonBacksOffTransientCredentialRotationFailure(t *testing.T) {
	store := testStore(t)
	d, err := Start(Options{Store: store, TTL: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	tokenPath := filepath.Join(d.RuntimeDir(), tokenName)
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tokenPath, 0o700); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(d.RuntimeDir(), auditName)
	deadline := time.Now().Add(time.Second)
	for credentialRotationAuditCount(t, auditPath) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("credential rotation failure was not audited")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if got := credentialRotationAuditCount(t, auditPath); got != 1 {
		t.Fatalf("credential rotation failures=%d want=1 during initial retry backoff", got)
	}
}

func credentialRotationAuditCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"action":"daemon.credential.rotate"`)
}
