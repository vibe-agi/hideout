package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const testDaemonSessionSnapshotID = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestSessionRegistryCapacityIdentityAndIndependentCancellation(t *testing.T) {
	r := newSessionRegistry(2, func() time.Time { return time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC) })
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	a, err := r.register("conn-a", cancelA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.register("conn-b", cancelB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.register("conn-c", func() {}); !errors.Is(err, errSessionCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	if err := a.markStarted(sessionStart{SessionID: "ses_a", EnvironmentID: "env_a", Profile: "default", Backend: "lima", TerminalMode: "pty", SessionSnapshotID: testDaemonSessionSnapshotID}); err != nil {
		t.Fatal(err)
	}
	if err := b.markStarted(sessionStart{SessionID: "ses_b", EnvironmentID: "env_a", Profile: "default", Backend: "lima", TerminalMode: "none", SessionSnapshotID: testDaemonSessionSnapshotID}); err != nil {
		t.Fatal(err)
	}
	r.cancelConnection("conn-a")
	select {
	case <-ctxA.Done():
	case <-time.After(time.Second):
		t.Fatal("connection A was not cancelled")
	}
	select {
	case <-ctxB.Done():
		t.Fatal("connection B was cancelled with sibling")
	default:
	}
	r.finish("conn-a", "")
	if got := r.snapshots(); len(got) != 1 || got[0].SessionID != "ses_b" {
		t.Fatalf("snapshots=%+v", got)
	}
	r.finish("conn-b", "")
}

func TestSessionRegistryCancelEnvironmentTargetsOnlyThatEnvironment(t *testing.T) {
	r := newSessionRegistry(3, func() time.Time { return time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC) })
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	ctxC, cancelC := context.WithCancel(context.Background())
	a, err := r.register("conn-a", cancelA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.register("conn-b", cancelB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.register("conn-c", cancelC); err != nil {
		t.Fatal(err)
	}
	if err := a.markStarted(sessionStart{SessionID: "ses_a", EnvironmentID: "env_target", Profile: "default", Backend: "lima", TerminalMode: "pty", SessionSnapshotID: testDaemonSessionSnapshotID}); err != nil {
		t.Fatal(err)
	}
	if err := b.markStarted(sessionStart{SessionID: "ses_b", EnvironmentID: "env_other", Profile: "default", Backend: "lima", TerminalMode: "none", SessionSnapshotID: testDaemonSessionSnapshotID}); err != nil {
		t.Fatal(err)
	}
	// conn-c never started a session and carries no environment identity; a
	// per-environment cancellation must not touch it.
	if cancelled := r.cancelEnvironment("env_target"); cancelled != 1 {
		t.Fatalf("cancelEnvironment cancelled %d sessions, want 1", cancelled)
	}
	select {
	case <-ctxA.Done():
	case <-time.After(time.Second):
		t.Fatal("target environment session was not cancelled")
	}
	select {
	case <-ctxB.Done():
		t.Fatal("sibling environment session was cancelled")
	default:
	}
	select {
	case <-ctxC.Done():
		t.Fatal("unstarted connection was cancelled")
	default:
	}
	r.finish("conn-a", "")
	r.finish("conn-b", "")
	r.finish("conn-c", "")
}

func TestSessionRegistryLeasesExactLiveDNSRuntimeAgainstTeardown(
	t *testing.T,
) {
	registry := newSessionRegistry(1, nil)
	worker, err := registry.register("conn-dns", func() {})
	if err != nil {
		t.Fatal(err)
	}
	var switches [][2]string
	var verifies []string
	releaseRuntime, err :=
		worker.RegisterEnvironmentNetworkRuntime(
			manager.EnvironmentNetworkRuntimeRegistration{
				EnvironmentID: "env_dns",
				SessionID:     "ses_dns",
				BootID: "01234567-89ab-cdef-0123-" +
					"456789abcdef",
				ReconfigureDNS: func(
					_ context.Context,
					oldResolver string,
					newResolver string,
				) error {
					switches = append(
						switches,
						[2]string{oldResolver, newResolver},
					)
					return nil
				},
				VerifyDNS: func(
					_ context.Context,
					resolver string,
				) error {
					verifies = append(verifies, resolver)
					return nil
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.markStarted(sessionStart{
		SessionID: "ses_dns", EnvironmentID: "env_dns",
		Profile: "default", Backend: "lima",
		SessionSnapshotID: testDaemonSessionSnapshotID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.EnvironmentNetworkRuntimeAvailable(
		context.Background(),
		"env_dns",
	); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.AcquireEnvironmentNetworkRuntime(
		context.Background(),
		"env_dns",
	)
	if err != nil {
		t.Fatal(err)
	}
	if lease.EnvironmentID() != "env_dns" ||
		lease.SessionID() != "ses_dns" ||
		lease.BootID() !=
			"01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("lease identity is not exact: %+v", lease)
	}
	if err := lease.VerifyDNS(
		context.Background(),
		"1.1.1.1",
	); err != nil {
		t.Fatal(err)
	}
	if err := lease.ReconfigureDNS(
		context.Background(),
		"1.1.1.1",
		"9.9.9.9",
	); err != nil {
		t.Fatal(err)
	}

	releaseStarted := make(chan struct{})
	releaseDone := make(chan struct{})
	go func() {
		close(releaseStarted)
		releaseRuntime()
		close(releaseDone)
	}()
	<-releaseStarted
	select {
	case <-releaseDone:
		t.Fatal("runtime teardown passed an active mutation lease")
	default:
	}
	lease.Release()
	select {
	case <-releaseDone:
	case <-time.After(time.Second):
		t.Fatal("runtime teardown did not resume after lease release")
	}
	if err := registry.EnvironmentNetworkRuntimeAvailable(
		context.Background(),
		"env_dns",
	); !errors.Is(
		err,
		manager.ErrEnvironmentNetworkRuntimeUnavailable,
	) {
		t.Fatalf("released runtime availability=%v", err)
	}
	if len(verifies) != 1 || verifies[0] != "1.1.1.1" ||
		len(switches) != 1 ||
		switches[0] != [2]string{"1.1.1.1", "9.9.9.9"} {
		t.Fatalf(
			"runtime calls verifies=%v switches=%v",
			verifies,
			switches,
		)
	}
	registry.finish("conn-dns", "")
}

func TestSessionWorkerSolelyOwnsImmutableWorkspaceAttachment(t *testing.T) {
	r := newSessionRegistry(1, nil)
	worker, err := r.register("conn-workspace", func() {})
	if err != nil {
		t.Fatal(err)
	}
	attachment := daemonWorkspaceAttachmentFixture(t, "ses_workspace", "env_workspace", "a")
	observedIncarnation := attachment.Incarnation
	attachment.Incarnation.BootID = ""
	factory := workspaceattach.NewPortalProviderFactory(workspaceattach.PortalProviderFactoryOptions{})
	runSession := manager.RunSession{WorkspaceAttachment: attachment, RuntimeSessionDir: t.TempDir()}
	if err := worker.prepareWorkspaceAttachment(context.Background(), factory, &runSession); err != nil {
		t.Fatal(err)
	}
	if runSession.WorkspacePortal == nil {
		t.Fatal("daemon worker did not prepare a Portal runtime")
	}
	if _, err := workspaceattach.ReadPortalCredential(runSession.WorkspacePortal.CredentialHostPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace credential existed before boot binding: %v", err)
	}
	runSession.WorkspaceAttachment.Incarnation = observedIncarnation
	if err := worker.activateWorkspaceAttachment(context.Background(), &runSession); err != nil {
		t.Fatal(err)
	}
	attachment.WorkspaceID = "wrk_" + strings.Repeat("f", 64)
	if worker.attachment.WorkspaceID == attachment.WorkspaceID {
		t.Fatal("daemon worker retained mutable attachment alias")
	}
	if err := worker.prepareWorkspaceAttachment(context.Background(), factory, &runSession); err == nil || !strings.Contains(err.Error(), "already owns") {
		t.Fatalf("second attachment bind error=%v", err)
	}
	if err := worker.markStarted(sessionStart{
		SessionID: "ses_other", EnvironmentID: "env_workspace", Profile: "default", Backend: "lima",
		SessionSnapshotID: testDaemonSessionSnapshotID,
	}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched backend ready identity error=%v", err)
	}
	if err := worker.markStarted(sessionStart{
		SessionID: "ses_workspace", EnvironmentID: "env_workspace", Profile: "default", Backend: "lima",
		SessionSnapshotID: testDaemonSessionSnapshotID,
	}); err != nil {
		t.Fatal(err)
	}
	snapshots := r.snapshots()
	if len(snapshots) != 1 || snapshots[0].WorkspaceID != worker.attachment.WorkspaceID {
		t.Fatalf("worker snapshot did not use daemon-owned attachment identity: %+v", snapshots)
	}
	if err := worker.releaseWorkspaceAttachment(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.finish("conn-workspace", "")
}

func TestDaemonWorkersHoldDistinctProjectViewsOnOneMachineIncarnation(t *testing.T) {
	registry := newSessionRegistry(2, nil)
	workerA, err := registry.register("conn-project-a", func() {})
	if err != nil {
		t.Fatal(err)
	}
	workerB, err := registry.register("conn-project-b", func() {})
	if err != nil {
		t.Fatal(err)
	}
	attachmentA := daemonWorkspaceAttachmentFixture(t, "ses_project_a", "env_shared", "a")
	attachmentB := daemonWorkspaceAttachmentFixture(t, "ses_project_b", "env_shared", "b")
	factory := workspaceattach.NewPortalProviderFactory(workspaceattach.PortalProviderFactoryOptions{})
	for _, item := range []struct {
		worker     *sessionWorker
		attachment workspaceattach.Attachment
	}{
		{workerA, attachmentA},
		{workerB, attachmentB},
	} {
		runSession := manager.RunSession{WorkspaceAttachment: item.attachment, RuntimeSessionDir: t.TempDir()}
		if err := item.worker.prepareWorkspaceAttachment(context.Background(), factory, &runSession); err != nil {
			t.Fatal(err)
		}
		if err := item.worker.activateWorkspaceAttachment(context.Background(), &runSession); err != nil {
			t.Fatal(err)
		}
		if err := item.worker.markStarted(sessionStart{
			SessionID: item.attachment.SessionID, EnvironmentID: item.attachment.EnvironmentID,
			Profile: "default", Backend: "lima", TerminalMode: "pty", SessionSnapshotID: testDaemonSessionSnapshotID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if attachmentA.Incarnation != attachmentB.Incarnation ||
		attachmentA.Incarnation.InstanceName == "" || attachmentA.Incarnation.BootID == "" {
		t.Fatalf("workers do not share one concrete machine incarnation: a=%+v b=%+v", attachmentA.Incarnation, attachmentB.Incarnation)
	}
	if workerA.attachment.WorkspaceID == workerB.attachment.WorkspaceID ||
		workerA.attachment.GuestViewRef == workerB.attachment.GuestViewRef ||
		workerA.attachment.PhysicalGuestRoot == workerB.attachment.PhysicalGuestRoot {
		t.Fatalf("daemon workers shared project-view authority: a=%+v b=%+v", workerA.attachment, workerB.attachment)
	}
	snapshots := registry.snapshots()
	if len(snapshots) != 2 || snapshots[0].EnvironmentID != "env_shared" || snapshots[1].EnvironmentID != "env_shared" ||
		snapshots[0].WorkspaceID == snapshots[1].WorkspaceID {
		t.Fatalf("daemon project-view snapshots=%+v", snapshots)
	}
	views := registry.workspaceViewSnapshots()
	if len(views) != 2 {
		t.Fatalf("workspace view snapshots=%+v", views)
	}
	for _, view := range views {
		if view.Attachment.State != workspaceattach.AttachmentReady || len(view.Relations) != 1 ||
			view.Relations[0].Relation != workspaceattach.RootDisjoint ||
			view.Relations[0].WorkspaceID != view.Attachment.WorkspaceID {
			t.Fatalf("workspace view relation=%+v", view)
		}
	}
	encoded, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	for _, attachment := range []workspaceattach.Attachment{attachmentA, attachmentB} {
		if strings.Contains(string(encoded), attachment.CanonicalHostRoot) ||
			strings.Contains(string(encoded), attachment.RootHandleIdentity) {
			t.Fatalf("workspace view snapshot leaked private authority: %s", encoded)
		}
	}
	if err := workerA.releaseWorkspaceAttachment(context.Background()); err != nil {
		t.Fatal(err)
	}
	released := registry.workspaceViewSnapshots()
	if len(released) != 2 || released[0].Attachment.State != workspaceattach.AttachmentReleased ||
		released[0].Attachment.CleanupProof == nil || released[0].Attachment.CleanupProof.Status != workspaceattach.CleanupAbsent {
		t.Fatalf("released workspace view did not carry absence proof: %+v", released)
	}
	if err := workerB.releaseWorkspaceAttachment(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry.finish("conn-project-a", "")
	registry.finish("conn-project-b", "")
}

func daemonWorkspaceAttachmentFixture(t *testing.T, sessionID, environmentID, marker string) workspaceattach.Attachment {
	t.Helper()
	root := t.TempDir()
	canonical, identity, err := workspaceattach.CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "wrk_" + strings.Repeat(marker, 64)
	return workspaceattach.Attachment{
		ID: "att_" + strings.Repeat(marker, 32), SessionID: sessionID, EnvironmentID: environmentID,
		Incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: environmentID, StartGeneration: 1, InstanceName: "hideout-default-env-workspace",
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
		WorkspaceID: workspaceID, CanonicalHostRoot: canonical,
		RootFileIdentity: identity, RootHandleIdentity: "root-handle-" + marker,
		LogicalGuestRoot:  workspaceattach.LogicalWorkspaceRoot,
		PhysicalGuestRoot: workspaceattach.PhysicalWorkspaceBase + "/" + workspaceID,
		Transport:         workspaceattach.SelectedTransport,
		ProviderRef:       lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-workspace-" + marker, Generation: 1},
		GuestViewRef:      lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceGuestView, ID: "view-workspace-" + marker, Generation: 1},
		State:             workspaceattach.AttachmentPlanned, CreatedAt: time.Now().UTC(),
	}
}

func TestSessionRegistryStopSerializesAgainstRegister(t *testing.T) {
	r := newSessionRegistry(32, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, _ = r.register(fmt.Sprintf("conn-%d", index), func() {})
		}(i)
	}
	r.beginStop()
	wg.Wait()
	if _, err := r.register("late", func() {}); !errors.Is(err, errSessionRegistryStopping) {
		t.Fatalf("late registration error=%v", err)
	}
	for _, snapshot := range r.snapshots() {
		r.finish(snapshot.ConnectionID, "")
	}
}

func TestSessionRegistryDrainIsBounded(t *testing.T) {
	r := newSessionRegistry(1, nil)
	_, err := r.register("stuck", func() {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error=%v", err)
	}
	r.finish("stuck", "cleanup failed")
}
