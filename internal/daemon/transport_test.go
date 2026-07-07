package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// T008: placement accepts a private runtime dir under the store.
func TestPlacementAcceptsPrivateRuntimeDirUnderStore(t *testing.T) {
	store := testStore(t)
	dir, err := ensurePlacement(store.Root)
	if err != nil {
		t.Fatalf("ensurePlacement: %v", err)
	}
	if filepath.Dir(dir) != filepath.Clean(store.Root) {
		t.Fatalf("runtime dir %s not directly under store %s", dir, store.Root)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("runtime dir must be operator-private, got %#o", info.Mode().Perm())
	}
}

// T008: placement fails closed for a non-private (guest-reachable-shaped) dir.
func TestPlacementFailsClosedForNonPrivateRuntimeDir(t *testing.T) {
	store := testStore(t)
	dir := runtimeDir(store.Root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePlacement(store.Root); err == nil {
		t.Fatal("ensurePlacement must fail closed for a world-accessible runtime dir")
	}
	if _, err := Start(Options{Store: store}); err == nil {
		t.Fatal("Start must fail closed for non-private placement")
	}
}

// T008: the primary transport is a local Unix socket under the runtime dir (no
// non-local bind by construction).
func TestTransportSocketIsLocalUnderRuntimeDir(t *testing.T) {
	d := startTestDaemon(t)
	if filepath.Dir(d.Socket()) != d.RuntimeDir() {
		t.Fatalf("socket %s not under runtime dir %s", d.Socket(), d.RuntimeDir())
	}
	info, err := os.Lstat(d.Socket())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("primary transport is not a unix socket: %v", info.Mode())
	}
}
