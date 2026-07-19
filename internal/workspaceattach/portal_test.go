package workspaceattach

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestPortalMultiplexesBinaryFramesOutOfOrder(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	client := dialPortalClient(t, server, authority, "session-a")
	defer client.Close()

	payloads := [][]byte{
		append([]byte{40, 0, 0, 0}, []byte("slow\x00payload")...),
		append([]byte{1, 0, 0, 0}, []byte("fast\x00payload")...),
	}
	results := make([][]byte, len(payloads))
	finished := make(chan int, len(payloads))
	var wg sync.WaitGroup
	for i := range payloads {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var err error
			results[index], err = client.ProbeEcho(context.Background(), payloads[index])
			if err != nil {
				t.Errorf("echo %d: %v", index, err)
			}
			finished <- index
		}(i)
	}
	if first := <-finished; first != 1 {
		t.Fatalf("first completed request = %d, want 1", first)
	}
	wg.Wait()
	for i := range payloads {
		if !bytes.Equal(results[i], payloads[i][4:]) {
			t.Fatalf("echo %d = %q, want %q", i, results[i], payloads[i][4:])
		}
	}
	if _, err := os.Stat(filepath.Join(root, "outside")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("echo mutated workspace: %v", err)
	}
}

func TestPortalCancellationStopsInFlightRequest(t *testing.T) {
	server, authority, _ := newPortalTestServer(t, DefaultPortalLimits())
	client := dialPortalClient(t, server, authority, "session-cancel")
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.ProbeEcho(ctx, append([]byte{250, 0, 0, 0}, []byte("late")...))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled echo error = %v", err)
	}
	if !server.WaitForIdle(time.Second) {
		t.Fatal("cancelled request retained server work")
	}
}

func TestPortalRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	limits := DefaultPortalLimits()
	limits.FrameBytes = 1024
	var output bytes.Buffer
	err := writePortalFrame(&output, portalFrame{RequestID: 1, Opcode: portalOpEcho, Payload: make([]byte, 1025)}, limits.FrameBytes)
	if !errors.Is(err, ErrPortalFrameTooLarge) {
		t.Fatalf("oversized frame error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized frame wrote %d bytes", output.Len())
	}
}

func TestPortalCredentialExpiryRotationAndRevocationTerminateStream(t *testing.T) {
	limits := DefaultPortalLimits()
	server, authority, _ := newPortalTestServer(t, limits)

	t.Run("expiry", func(t *testing.T) {
		credential, err := authority.Issue("session-expiry", "env-a", "boot-a", PortalAudience, 40*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		client, err := DialPortal(context.Background(), server.Addr(), credential, limits)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		select {
		case terminal := <-client.Terminal():
			if !errors.Is(terminal, ErrPortalCredentialExpired) {
				t.Fatalf("expiry terminal = %v", terminal)
			}
		case <-time.After(time.Second):
			t.Fatal("expiry did not terminate stream")
		}
	})

	t.Run("rotation and stale resubscribe", func(t *testing.T) {
		oldCredential, err := authority.Issue("session-rotate", "env-a", "boot-a", PortalAudience, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		client, err := DialPortal(context.Background(), server.Addr(), oldCredential, limits)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := authority.Rotate("session-rotate", time.Minute); err != nil {
			t.Fatal(err)
		}
		select {
		case terminal := <-client.Terminal():
			if !errors.Is(terminal, ErrPortalCredentialRevoked) {
				t.Fatalf("rotation terminal = %v", terminal)
			}
		case <-time.After(time.Second):
			t.Fatal("rotation did not terminate old stream")
		}
		if stale, err := DialPortal(context.Background(), server.Addr(), oldCredential, limits); err == nil {
			stale.Close()
			t.Fatal("stale credential resubscribed")
		}
	})

	t.Run("revocation", func(t *testing.T) {
		credential, err := authority.Issue("session-revoke", "env-a", "boot-a", PortalAudience, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		client, err := DialPortal(context.Background(), server.Addr(), credential, limits)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if err := authority.Revoke("session-revoke"); err != nil {
			t.Fatal(err)
		}
		select {
		case terminal := <-client.Terminal():
			if !errors.Is(terminal, ErrPortalCredentialRevoked) {
				t.Fatalf("revocation terminal = %v", terminal)
			}
		case <-time.After(time.Second):
			t.Fatal("revocation did not terminate stream")
		}
	})
}

func TestPortalDisconnectReleasesHandlesAndLocks(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	if err := os.WriteFile(filepath.Join(root, "disconnect.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := dialPortalClient(t, server, authority, "session-disconnect-a")
	handle, err := first.Open(context.Background(), "disconnect.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Lock(context.Background(), handle, true); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if !server.WaitForIdle(time.Second) {
		t.Fatal("disconnect retained handles")
	}

	second := dialPortalClient(t, server, authority, "session-disconnect-b")
	defer second.Close()
	secondHandle, err := second.Open(context.Background(), "disconnect.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(context.Background(), secondHandle, true); err != nil {
		t.Fatalf("lock retained after disconnect: %v", err)
	}
}

func TestPortalDirectorySnapshotReportsBothRenameNames(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "before.txt"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotPortalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	server := &PortalServer{rootPath: root, watchDir: map[string]map[string]struct{}{root: before}}
	if err := os.Rename(filepath.Join(root, "before.txt"), filepath.Join(root, "after.txt")); err != nil {
		t.Fatal(err)
	}
	events := server.refreshWatchDirectory(root)
	got := make(map[string]fsnotify.Op, len(events))
	for _, event := range events {
		got[event.Path] = fsnotify.Op(event.Op)
	}
	if got["before.txt"] != fsnotify.Remove || got["after.txt"] != fsnotify.Create {
		t.Fatalf("rename events = %#v", got)
	}
}

func newPortalTestServer(t *testing.T, limits PortalLimits) (*PortalServer, *PortalCredentialAuthority, string) {
	t.Helper()
	root := t.TempDir()
	authority := NewPortalCredentialAuthority()
	admission, err := NewAdmissionController(admissionLimitsForPortalTest(limits))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewPortalServer(PortalServerOptions{
		Root: root, Authority: authority, Limits: limits, EnvironmentID: "env-a", ProviderID: "provider-test", Admission: admission,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(listener); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return server, authority, root
}

func admissionLimitsForPortalTest(portal PortalLimits) LimitSet {
	limits := SelectedLimits()
	limits.HandlesPerSession = portal.HandlesPerSession
	limits.HandlesPerProvider = limits.ViewsPerEnvironment * portal.HandlesPerSession
	limits.InFlightPerSession = portal.InFlightPerSession
	limits.InFlightGlobal = limits.ViewsPerEnvironment * portal.InFlightPerSession
	limits.QueuedBytesPerSession = portal.QueuedBytesPerSession
	if limits.QueuedBytesPerSession < int64(portal.FrameBytes) {
		limits.QueuedBytesPerSession = int64(portal.FrameBytes)
	}
	limits.QueuedBytesGlobal = int64(limits.ViewsPerEnvironment) * limits.QueuedBytesPerSession
	limits.FrameBytes = portal.FrameBytes
	limits.DirectoryEntries = portal.DirectoryEntries
	if limits.DirectoryPageEntries > limits.DirectoryEntries {
		limits.DirectoryPageEntries = limits.DirectoryEntries
	}
	if limits.TeardownInFlightPerSession > limits.InFlightPerSession {
		limits.TeardownInFlightPerSession = limits.InFlightPerSession
	}
	return limits
}

func dialPortalClient(t *testing.T, server *PortalServer, authority *PortalCredentialAuthority, session string) *PortalClient {
	t.Helper()
	credential, err := authority.Issue(session, "env-a", "boot-a", PortalAudience, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialPortal(context.Background(), server.Addr(), credential, DefaultPortalLimits())
	if err != nil {
		t.Fatal(err)
	}
	return client
}
