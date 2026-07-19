package workspaceattach

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestPortalProviderOwnsOneRootAndIndependentSessionViews(t *testing.T) {
	spec, firstViewSpec, root := portalProviderFixture(t, "ses_first")
	factory := NewPortalProviderFactory(PortalProviderFactoryOptions{
		Listen: func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
		Advertise: func(address string) (string, error) {
			_, port, err := net.SplitHostPort(address)
			return net.JoinHostPort("host.lima.internal", port), err
		},
	})
	provider, err := factory.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := factory.Start(context.Background(), spec)
	if err != nil || reused != provider {
		t.Fatalf("same provider authority was not reused: %p %p %v", provider, reused, err)
	}
	firstRaw, err := provider.Attach(context.Background(), firstViewSpec)
	if err != nil {
		t.Fatal(err)
	}
	first := firstRaw.(PortalGuestView)
	if !strings.HasPrefix(first.Endpoint(), "host.lima.internal:") {
		t.Fatalf("guest endpoint = %q", first.Endpoint())
	}

	secondViewSpec := firstViewSpec
	secondViewSpec.Attachment.ID = "att_fedcba9876543210"
	secondViewSpec.Attachment.SessionID = "ses_second"
	secondViewSpec.Attachment.GuestViewRef.ID = "view-second"
	secondViewSpec.CredentialAudience = "workspace-view-second"
	secondRaw, err := provider.Attach(context.Background(), secondViewSpec)
	if err != nil {
		t.Fatal(err)
	}
	second := secondRaw.(PortalGuestView)

	firstCredential := filepath.Join(t.TempDir(), "first.bin")
	secondCredential := filepath.Join(t.TempDir(), "second.bin")
	if err := first.WriteCredential(firstCredential); err != nil {
		t.Fatal(err)
	}
	if err := second.WriteCredential(secondCredential); err != nil {
		t.Fatal(err)
	}
	firstData, _ := os.ReadFile(firstCredential)
	secondData, _ := os.ReadFile(secondCredential)
	if bytes.Equal(firstData, secondData) {
		t.Fatal("same-root views shared a credential")
	}
	boundAddress := provider.(*portalProvider).server.Addr()
	firstClient := dialPortalFromCredential(t, boundAddress, firstCredential)
	defer firstClient.Close()
	secondClient := dialPortalFromCredential(t, boundAddress, secondCredential)
	defer secondClient.Close()

	handle, err := firstClient.Open(context.Background(), "live.txt", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if written, err := firstClient.Write(context.Background(), handle, 0, []byte("live")); err != nil || written != 4 {
		t.Fatalf("write = %d, %v", written, err)
	}
	secondHandle, err := secondClient.Open(context.Background(), "live.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstClient.Lock(context.Background(), handle, true); err != nil {
		t.Fatal(err)
	}
	if err := secondClient.Lock(context.Background(), secondHandle, true); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("sibling lock while first view owns it = %v", err)
	}
	if err := first.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "live.txt")); err != nil || string(got) != "live" {
		t.Fatalf("host live write = %q, %v", got, err)
	}
	if observation, err := provider.Release(context.Background()); err != nil || observation.State != ObservationReady {
		t.Fatalf("provider release with active views=%#v, %v", observation, err)
	}
	if _, err := first.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case terminal := <-firstClient.Terminal():
		if !errors.Is(terminal, ErrPortalCredentialRevoked) {
			t.Fatalf("revoked terminal = %v", terminal)
		}
	case <-time.After(time.Second):
		t.Fatal("released view did not revoke its client")
	}
	if _, err := secondClient.Stat(context.Background(), "live.txt"); err != nil {
		t.Fatalf("sibling view was interrupted: %v", err)
	}
	lockDeadline := time.Now().Add(time.Second)
	for {
		err := secondClient.Lock(context.Background(), secondHandle, true)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(lockDeadline) {
			t.Fatalf("sibling lock after first view release = %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := second.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	observation, err := provider.Release(context.Background())
	if err != nil || observation.State != ObservationAbsent {
		t.Fatalf("final provider release = %#v, %v", observation, err)
	}
	observed, err := factory.Observe(context.Background(), specRef(spec))
	if err != nil || observed.State != ObservationAbsent {
		t.Fatalf("released factory observation = %#v, %v", observed, err)
	}
}

func TestPortalProviderRejectsChangedRootBeforeAuthority(t *testing.T) {
	spec, _, root := portalProviderFixture(t, "ses_first")
	old := root + ".old"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPortalProviderFactory(PortalProviderFactoryOptions{}).Start(context.Background(), spec); !errors.Is(err, ErrPortalRootReplaced) {
		t.Fatalf("changed root start = %v", err)
	}
}

func TestPortalViewFlushRejectsChangedRoot(t *testing.T) {
	spec, viewSpec, root := portalProviderFixture(t, "ses_flush_root")
	factory := NewPortalProviderFactory(PortalProviderFactoryOptions{})
	provider, err := factory.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	view, err := provider.Attach(context.Background(), viewSpec)
	if err != nil {
		t.Fatal(err)
	}
	old := root + ".old"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := view.Flush(context.Background()); !errors.Is(err, ErrPortalRootReplaced) {
		t.Fatalf("flush after root replacement=%v", err)
	}
	_, _ = view.Release(context.Background())
	_, _ = provider.Release(context.Background())
}

func portalProviderFixture(t *testing.T, sessionID string) (ProviderSpec, ViewSpec, string) {
	t.Helper()
	root := t.TempDir()
	canonical, identity, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	incarnation := lifecycle.EnvironmentRef{
		EnvironmentID: "env_fixture", StartGeneration: 1, InstanceName: "hideout-fixture",
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	attachment := Attachment{
		ID: "att_0123456789abcdef", SessionID: sessionID, EnvironmentID: "env_fixture",
		Incarnation: incarnation, WorkspaceID: workspaceID, CanonicalHostRoot: canonical,
		RootFileIdentity: identity, RootHandleIdentity: "root-fixture",
		LogicalGuestRoot: LogicalWorkspaceRoot, PhysicalGuestRoot: PhysicalWorkspaceBase + "/" + workspaceID,
		Transport:    SelectedTransport,
		ProviderRef:  lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-fixture", Generation: 1},
		GuestViewRef: lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceGuestView, ID: "view-first", Generation: 1},
		State:        AttachmentPlanned, CreatedAt: time.Now().UTC(),
	}
	spec, err := ProviderSpecFromAttachment(attachment, SelectedLimits())
	if err != nil {
		t.Fatal(err)
	}
	return spec, ViewSpec{Attachment: attachment, CredentialAudience: "workspace-view-first"}, root
}

func TestPortalProviderIssuesNoCredentialBeforeExactBootBinding(t *testing.T) {
	spec, viewSpec, _ := portalProviderFixture(t, "ses_cold_boot")
	observed := spec.Incarnation
	spec.Incarnation.BootID = ""
	viewSpec.Attachment.Incarnation.BootID = ""
	factory := NewPortalProviderFactory(PortalProviderFactoryOptions{})
	provider, err := factory.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Attach(context.Background(), viewSpec); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("pre-boot attach error=%v", err)
	}
	wrong := observed
	wrong.InstanceName = "hideout-other"
	if err := provider.BindIncarnation(context.Background(), wrong); err == nil {
		t.Fatal("provider accepted a different machine at the boot binding barrier")
	}
	if err := provider.BindIncarnation(context.Background(), observed); err != nil {
		t.Fatal(err)
	}
	viewSpec.Attachment.Incarnation = observed
	view, err := provider.Attach(context.Background(), viewSpec)
	if err != nil {
		t.Fatal(err)
	}
	differentBoot := observed
	differentBoot.BootID = "fedcba98-7654-3210-fedc-ba9876543210"
	if err := provider.BindIncarnation(context.Background(), differentBoot); err == nil {
		t.Fatal("provider rebound an established boot incarnation")
	}
	if _, err := view.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func dialPortalFromCredential(t *testing.T, address, path string) *PortalClient {
	t.Helper()
	credential, err := ReadPortalCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialPortal(context.Background(), address, credential, DefaultPortalLimits())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func specRef(spec ProviderSpec) lifecycle.ResourceRef {
	return lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: spec.ProviderID, Generation: spec.Incarnation.StartGeneration}
}
