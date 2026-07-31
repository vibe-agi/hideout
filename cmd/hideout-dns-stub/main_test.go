package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishReadyMarkerBindsExactProcessAndPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns-stub.ready")
	if err := publishReadyMarker(path, 4242); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "4242\n" {
		t.Fatalf("ready marker=%q, want exact process ID", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("ready marker mode=%#o, want 0600", info.Mode().Perm())
	}
	if err := publishReadyMarker(path, 4243); err == nil {
		t.Fatal("existing ready marker was overwritten")
	}
}

func TestPublishReadyMarkerRejectsUnsafeTarget(t *testing.T) {
	if err := publishReadyMarker("relative.ready", 1); err == nil {
		t.Fatal("relative ready marker was accepted")
	}
	if err := publishReadyMarker(filepath.Join(t.TempDir(), "ready"), 0); err == nil {
		t.Fatal("non-positive process ID was accepted")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "ready")
	if err := os.WriteFile(target, []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := publishReadyMarker(link, 7); err == nil {
		t.Fatal("symlink ready marker was accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "owned\n" {
		t.Fatalf("symlink target was modified: %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ready marker symlink was replaced: mode=%s", info.Mode())
	}
}
