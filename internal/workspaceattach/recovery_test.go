package workspaceattach

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPortalRecoveryProviderCrashRejectsOldAuthorityAfterRestart(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "restart-lock.txt")
	if err := os.WriteFile(path, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := DefaultPortalLimits()
	oldAuthority := NewPortalCredentialAuthority()
	oldServer := startPortalRecoveryServer(t, root, oldAuthority, limits)
	oldCredential, err := oldAuthority.Issue("session-before-crash", "env-a", "boot-a", PortalAudience, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oldClient, err := DialPortal(context.Background(), oldServer.Addr(), oldCredential, limits)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := oldClient.Open(context.Background(), "restart-lock.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldClient.Lock(context.Background(), handle, true); err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- oldServer.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close crashed provider: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider crash simulation did not terminate within one second")
	}
	select {
	case terminal := <-oldClient.Terminal():
		if terminal == nil {
			t.Fatal("provider crash produced a nil terminal result")
		}
	case <-time.After(time.Second):
		t.Fatal("provider crash did not terminate the old client")
	}
	_ = oldClient.Close()

	newAuthority := NewPortalCredentialAuthority()
	newServer := startPortalRecoveryServer(t, root, newAuthority, limits)
	defer newServer.Close()
	if adopted, err := DialPortal(context.Background(), newServer.Addr(), oldCredential, limits); err == nil {
		adopted.Close()
		t.Fatal("restarted provider adopted an old credential")
	} else if !errors.Is(err, ErrPortalAuthentication) {
		t.Fatalf("old credential rejection = %v", err)
	}
	newCredential, err := newAuthority.Issue("session-after-crash", "env-a", "boot-b", PortalAudience, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	newClient, err := DialPortal(context.Background(), newServer.Addr(), newCredential, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	newHandle, err := newClient.Open(context.Background(), "restart-lock.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := newClient.Lock(context.Background(), newHandle, true); err != nil {
		t.Fatalf("provider restart retained an old lock: %v", err)
	}
}

func TestPortalRecoveryAbruptGuestDisconnectReleasesHandlesAndLocks(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	if err := os.WriteFile(filepath.Join(root, "vm-stop.txt"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldClient := dialPortalClient(t, server, authority, "session-before-vm-stop")
	handle, err := oldClient.Open(context.Background(), "vm-stop.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldClient.Lock(context.Background(), handle, true); err != nil {
		t.Fatal(err)
	}
	if err := oldClient.connection.Close(); err != nil {
		t.Fatal(err)
	}
	defer oldClient.Close()

	deadline := time.Now().Add(time.Second)
	for {
		server.locks.mu.Lock()
		remaining := len(server.locks.locks)
		server.locks.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("abrupt guest disconnect retained %d lock(s)", remaining)
		}
		time.Sleep(time.Millisecond)
	}

	newClient := dialPortalClient(t, server, authority, "session-after-vm-stop")
	defer newClient.Close()
	newHandle, err := newClient.Open(context.Background(), "vm-stop.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := newClient.Lock(context.Background(), newHandle, true); err != nil {
		t.Fatalf("new session lock after abrupt disconnect = %v", err)
	}
}

func startPortalRecoveryServer(
	t *testing.T,
	root string,
	authority *PortalCredentialAuthority,
	limits PortalLimits,
) *PortalServer {
	t.Helper()
	admission, err := NewAdmissionController(admissionLimitsForPortalTest(limits))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewPortalServer(PortalServerOptions{
		Root: root, Authority: authority, Limits: limits, EnvironmentID: "env-a", ProviderID: "provider-recovery", Admission: admission,
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
	return server
}
