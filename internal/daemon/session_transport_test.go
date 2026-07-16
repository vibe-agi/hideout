package daemon

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSessionTransportInventoryAndPermissions(t *testing.T) {
	store := testStore(t)
	listener, err := ListenSession(store.Root)
	if err != nil {
		t.Fatalf("ListenSession: %v", err)
	}
	path := listener.Socket()
	if path != SessionSocketPath(store.Root) {
		t.Fatalf("socket = %q, want %q", path, SessionSocketPath(store.Root))
	}
	inventory := SessionTransportFor(store.Root)
	if inventory.Kind != "unix-session" || inventory.Socket != path || inventory.Protocol != SessionProtocolVersion {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}

	dirInfo, err := os.Lstat(runtimeDir(store.Root))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("runtime mode = %#o, want 0700", got)
	}
	socketInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("session path is not a direct unix socket: %v", socketInfo.Mode())
	}
	if got := socketInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %#o, want 0600", got)
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after close: %v", err)
	}
}

func TestListenSessionConcurrentBindHasOneWinner(t *testing.T) {
	store := testStore(t)
	const contenders = 16
	start := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			listener, err := ListenSession(store.Root)
			results <- err
			if err == nil {
				<-release
				_ = listener.Close()
			}
		}()
	}
	close(start)
	winners := 0
	for i := 0; i < contenders; i++ {
		err := <-results
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrSessionAlreadyListening):
		default:
			t.Fatalf("unexpected bind error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("bind winners = %d, want 1", winners)
	}
	close(release)
	wg.Wait()
}

func TestListenSessionReclaimsOnlyStaleSocket(t *testing.T) {
	store := testStore(t)
	if _, err := ensurePlacement(store.Root); err != nil {
		t.Fatal(err)
	}
	path := SessionSocketPath(store.Root)
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if unixListener, ok := stale.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket fixture missing: %v", err)
	}

	listener, err := ListenSession(store.Root)
	if err != nil {
		t.Fatalf("reclaim stale socket: %v", err)
	}
	defer listener.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("rebound session socket is not live: %v", err)
	}
	_ = conn.Close()
}

func TestListenSessionRejectsLiveSocketWithoutOwningIt(t *testing.T) {
	store := testStore(t)
	if _, err := ensurePlacement(store.Root); err != nil {
		t.Fatal(err)
	}
	path := SessionSocketPath(store.Root)
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	if _, err := ListenSession(store.Root); !errors.Is(err, ErrSessionAlreadyListening) {
		t.Fatalf("live session socket error = %v, want ErrSessionAlreadyListening", err)
	}
}

func TestListenSessionRejectsSymlinkAndNonSocketPaths(t *testing.T) {
	tests := []struct {
		name   string
		create func(string) error
	}{
		{
			name: "symlink",
			create: func(path string) error {
				target := filepath.Join(filepath.Dir(path), "target")
				if err := os.WriteFile(target, []byte("not a socket"), 0o600); err != nil {
					return err
				}
				return os.Symlink(target, path)
			},
		},
		{
			name: "regular-file",
			create: func(path string) error {
				return os.WriteFile(path, []byte("not a socket"), 0o600)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			if _, err := ensurePlacement(store.Root); err != nil {
				t.Fatal(err)
			}
			path := SessionSocketPath(store.Root)
			if err := tt.create(path); err != nil {
				t.Fatal(err)
			}
			if _, err := ListenSession(store.Root); err == nil {
				t.Fatal("ListenSession accepted an unsafe path")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("unsafe path was removed: %v", err)
			}
		})
	}
}

func TestListenSessionRejectsUnsafeBindLock(t *testing.T) {
	store := testStore(t)
	dir, err := ensurePlacement(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "lock-target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, sessionSocketLockName)); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenSession(store.Root); err == nil {
		t.Fatal("ListenSession accepted a symlinked bind lock")
	}
}

func TestListenSessionRejectsOverlongSocketPath(t *testing.T) {
	storeRoot := filepath.Join(t.TempDir(), strings.Repeat("a", 80))
	if len(SessionSocketPath(storeRoot)) <= sessionSocketPathMax {
		t.Fatalf("test fixture socket path is not overlong: %s", SessionSocketPath(storeRoot))
	}
	if _, err := ListenSession(storeRoot); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("overlong socket error = %v", err)
	}
}
