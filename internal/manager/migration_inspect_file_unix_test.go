//go:build darwin || linux

package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestBundleProbeRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	if _, err := ProbeMigrationBundleFile(t.TempDir()); err == nil {
		t.Fatal("migration bundle probe accepted a directory")
	}

	fifo := filepath.Join(t.TempDir(), "bundle.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := ProbeMigrationBundleFile(fifo)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("migration bundle probe accepted a named pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("migration bundle probe blocked while opening a named pipe")
	}

	info, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("named-pipe sentinel changed: mode=%v", info.Mode())
	}
}
