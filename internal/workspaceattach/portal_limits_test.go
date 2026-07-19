package workspaceattach

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPortalPerSessionHandleAndEnumerationLimits(t *testing.T) {
	limits := DefaultPortalLimits()
	limits.HandlesPerSession = 1
	limits.DirectoryEntries = 2
	server, authority, root := newPortalTestServer(t, limits)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	credential, err := authority.Issue("session-limits", "env-a", "boot-a", PortalAudience, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialPortal(context.Background(), server.Addr(), credential, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	handle, err := client.Open(context.Background(), "a", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(context.Background(), "b", os.O_RDONLY, 0); !errors.Is(err, ErrPortalOverloaded) {
		t.Fatalf("second handle error = %v", err)
	}
	if err := client.CloseHandle(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadDir(context.Background(), "."); !errors.Is(err, syscall.EOVERFLOW) {
		t.Fatalf("directory limit error = %v", err)
	}
}

func TestPortalSaturationDoesNotStarveSiblingOrTeardown(t *testing.T) {
	limits := DefaultPortalLimits()
	limits.InFlightPerSession = 1
	server, authority, root := newPortalTestServer(t, limits)
	if err := os.WriteFile(filepath.Join(root, "value"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstCredential, _ := authority.Issue("session-noisy", "env-a", "boot-a", PortalAudience, time.Minute)
	first, err := DialPortal(context.Background(), server.Addr(), firstCredential, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	secondCredential, _ := authority.Issue("session-sibling", "env-a", "boot-a", PortalAudience, time.Minute)
	second, err := DialPortal(context.Background(), server.Addr(), secondCredential, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	handle, err := first.Open(context.Background(), "value", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	slowDone := make(chan error, 1)
	go func() {
		_, err := first.ProbeEcho(context.Background(), append([]byte{200, 0, 0, 0}, []byte("slow")...))
		slowDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := first.ProbeEcho(context.Background(), append([]byte{1, 0, 0, 0}, []byte("overload")...)); !errors.Is(err, ErrPortalOverloaded) {
		t.Fatalf("same-session overload error = %v", err)
	}
	if _, err := second.Stat(context.Background(), "value"); err != nil {
		t.Fatalf("sibling starved by noisy session: %v", err)
	}
	if err := first.CloseHandle(context.Background(), handle); err != nil {
		t.Fatalf("teardown operation did not use reserved capacity: %v", err)
	}
	if err := <-slowDone; err != nil {
		t.Fatal(err)
	}
}

func TestPortalQueuedByteAdmissionIsBounded(t *testing.T) {
	limits := DefaultPortalLimits()
	limits.QueuedBytesPerSession = 4096
	admission, err := NewAdmissionController(admissionLimitsForPortalTest(limits))
	if err != nil {
		t.Fatal(err)
	}
	state := &portalConnection{
		server:     &PortalServer{environmentID: "env-a", providerID: "provider-queued", admission: admission},
		credential: PortalCredential{SessionID: "session-queued"}, events: make(chan queuedPortalEvent, 64),
	}
	for i := 0; i < 16; i++ {
		if err := state.enqueueEvent(PortalEvent{Path: string(make([]byte, 300)), Op: 1}, limits.QueuedBytesPerSession); err != nil {
			if !errors.Is(err, ErrPortalOverloaded) {
				t.Fatalf("queued byte error = %v", err)
			}
			if state.queuedEventBytes.Load() > limits.QueuedBytesPerSession {
				t.Fatalf("queued bytes exceeded bound: %d", state.queuedEventBytes.Load())
			}
			return
		}
	}
	t.Fatal("queued byte limit did not reject bounded overload")
}
