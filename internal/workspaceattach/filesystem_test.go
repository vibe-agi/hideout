package workspaceattach

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestSelectedWorkspaceFilesystemDirectWriteOperationMatrix(t *testing.T) {
	if SelectedTransport != CandidatePortal {
		t.Fatalf("filesystem regression adapter is %q, selected transport is %q", CandidatePortal, SelectedTransport)
	}
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "original.txt"), []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original.txt", filepath.Join(root, "dir", "link.txt")); err != nil {
		t.Fatal(err)
	}
	client := dialPortalClient(t, server, authority, "session-filesystem")
	defer client.Close()
	ctx := context.Background()

	info, err := client.Stat(ctx, "dir/original.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len("original")) || !info.Mode.IsRegular() {
		t.Fatalf("original stat = %#v", info)
	}
	linkInfo, err := client.Stat(ctx, "dir/link.txt")
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode&os.ModeSymlink == 0 {
		t.Fatalf("link stat mode = %v", linkInfo.Mode)
	}
	entries, err := client.ReadDir(ctx, "dir")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{entries[0].Name, entries[1].Name}
	slices.Sort(names)
	if !slices.Equal(names, []string{"link.txt", "original.txt"}) {
		t.Fatalf("directory entries = %v", names)
	}
	for _, entry := range entries {
		if entry.Info.Inode == 0 || entry.Info.Mode == 0 {
			t.Fatalf("directory entry omitted prefetched metadata: %#v", entry)
		}
	}

	readHandle, err := client.Open(ctx, "dir/original.txt", os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.Read(ctx, readHandle, 0, 64)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("read = %q", data)
	}
	if err := client.CloseHandle(ctx, readHandle); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(ctx, "dir/link.txt", os.O_RDONLY|syscall.O_NOFOLLOW, 0); !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("no-follow symlink open = %v, want ELOOP", err)
	}

	if err := client.Mkdir(ctx, "new", 0o750); err != nil {
		t.Fatal(err)
	}
	temporary, err := client.Open(ctx, "new/value.tmp", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if written, err := client.Write(ctx, temporary, 0, []byte("replacement")); err != nil || written != len("replacement") {
		t.Fatalf("write = %d, %v", written, err)
	}
	if err := client.Fsync(ctx, temporary); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseHandle(ctx, temporary); err != nil {
		t.Fatal(err)
	}
	if err := client.Rename(ctx, "new/value.tmp", "dir/original.txt"); err != nil {
		t.Fatal(err)
	}
	if err := client.FsyncPath(ctx, "dir"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "dir", "original.txt")); err != nil || string(got) != "replacement" {
		t.Fatalf("host lower after direct replace = %q, %v", got, err)
	}
	if err := client.Truncate(ctx, "dir/original.txt", 5); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "dir", "original.txt")); err != nil || string(got) != "repla" {
		t.Fatalf("host lower after truncate = %q, %v", got, err)
	}

	if err := client.Chmod(ctx, "dir/original.txt", 0o600); err != nil {
		t.Fatal(err)
	}
	wantTime := time.Unix(1_700_000_000, 123_000_000)
	if err := client.Chtimes(ctx, "dir/original.txt", wantTime, wantTime); err != nil {
		t.Fatal(err)
	}
	if err := client.Symlink(ctx, "original.txt", "dir/new-link.txt"); err != nil {
		t.Fatal(err)
	}
	if target, err := client.Readlink(ctx, "dir/new-link.txt"); err != nil || target != "original.txt" {
		t.Fatalf("readlink = %q, %v", target, err)
	}
	if err := client.Remove(ctx, "dir/new-link.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := client.Remove(ctx, "new", true); err != nil {
		t.Fatal(err)
	}
	final, err := os.Stat(filepath.Join(root, "dir", "original.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if final.Mode().Perm() != 0o600 || final.ModTime().Unix() != wantTime.Unix() {
		t.Fatalf("final metadata = mode %o time %v", final.Mode().Perm(), final.ModTime())
	}
}

func TestSelectedWorkspaceFilesystemTruthfulErrnoMatrix(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := dialPortalClient(t, server, authority, "session-errno")
	defer client.Close()
	ctx := context.Background()

	if _, err := client.Stat(ctx, "missing"); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("missing stat error = %v", err)
	}
	if _, err := client.Stat(ctx, "../outside"); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("escape stat error = %v", err)
	}
	if err := client.Remove(ctx, "dir", false); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("unlink directory error = %v", err)
	}
	if err := client.Remove(ctx, "file", true); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("rmdir file error = %v", err)
	}
	if err := client.Symlink(ctx, "../../outside", "bad-link"); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("escaping symlink error = %v", err)
	}
	if _, err := client.Read(ctx, 999, 0, 1); !errors.Is(err, ErrPortalHandleNotFound) {
		t.Fatalf("stale handle error = %v", err)
	}
}

func TestSelectedWorkspaceFilesystemRejectsRootReplacement(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	client := dialPortalClient(t, server, authority, "session-root-replacement")
	defer client.Close()
	old := root + ".old"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stat(context.Background(), "."); !errors.Is(err, ErrPortalRootReplaced) {
		t.Fatalf("replacement error = %v", err)
	}
}

func TestSelectedWorkspaceFilesystemConvergesHostAndTargetMutations(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	client := dialPortalClient(t, server, authority, "session-watcher")
	defer client.Close()

	if err := os.WriteFile(filepath.Join(root, "host.txt"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitPortalEvent(t, client, "host.txt")

	handle, err := client.Open(context.Background(), "portal.txt", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(context.Background(), handle, 0, []byte("portal")); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseHandle(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	waitPortalEvent(t, client, "portal.txt")

	if err := os.WriteFile(filepath.Join(root, "rename-before.txt"), []byte("rename"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitPortalEvent(t, client, "rename-before.txt")
	if err := os.Rename(filepath.Join(root, "rename-before.txt"), filepath.Join(root, "rename-after.txt")); err != nil {
		t.Fatal(err)
	}
	waitPortalEvents(t, client, "rename-before.txt", "rename-after.txt")
}

func TestSelectedWorkspaceFilesystemKeepsSameRootLockOwnersIndependent(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	if err := os.WriteFile(filepath.Join(root, "lock.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := dialPortalClient(t, server, authority, "session-lock-a")
	defer first.Close()
	second := dialPortalClient(t, server, authority, "session-lock-b")
	defer second.Close()

	firstHandle, err := first.Open(context.Background(), "lock.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondHandle, err := second.Open(context.Background(), "lock.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Lock(context.Background(), firstHandle, true); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(context.Background(), secondHandle, true); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("sibling lock error = %v", err)
	}
	if err := first.Unlock(context.Background(), firstHandle); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(context.Background(), secondHandle, true); err != nil {
		t.Fatalf("lock after sibling unlock: %v", err)
	}
}

func waitPortalEvent(t *testing.T, client *PortalClient, path string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-client.Events():
			if event.Path == path {
				if filepath.IsAbs(event.Path) || event.Path == ".." {
					t.Fatalf("watcher leaked non-relative path %q", event.Path)
				}
				return
			}
		case <-deadline:
			t.Fatalf("no watcher event for %q", path)
		}
	}
}

func waitPortalEvents(t *testing.T, client *PortalClient, paths ...string) {
	t.Helper()
	want := make(map[string]bool, len(paths))
	for _, path := range paths {
		want[path] = true
	}
	deadline := time.After(2 * time.Second)
	for len(want) > 0 {
		select {
		case event := <-client.Events():
			delete(want, event.Path)
		case <-deadline:
			t.Fatalf("missing watcher events: %#v", want)
		}
	}
}
