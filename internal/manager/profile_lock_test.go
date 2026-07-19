package manager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProfileMutationLockUsesProcessSharedFlock(t *testing.T) {
	root := t.TempDir()
	first, err := openProfileMutationLock(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := openProfileMutationLock(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := unix.Flock(int(first.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(first.Fd()), unix.LOCK_UN)
	if err := unix.Flock(int(second.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("second lock error=%v, want EWOULDBLOCK", err)
	}
}

func TestProfileMutationLockRejectsSymlinkedLockDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".locks")); err != nil {
		t.Fatal(err)
	}
	if _, err := openProfileMutationLock(root, "default"); err == nil {
		t.Fatal("symlinked lock directory was accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("lock setup wrote through symlink: %v", entries)
	}
}
