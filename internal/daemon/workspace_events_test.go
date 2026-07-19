package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestDaemonPublishesWorkspaceViewEventsAndStatusFromOwnedAttachment(t *testing.T) {
	d := startTestDaemon(t)
	sub := d.bus.subscribe(32)
	defer d.bus.unsubscribe(sub)

	worker, err := d.sessions.register("conn-workspace-events", func() {})
	if err != nil {
		t.Fatal(err)
	}
	attachment := daemonWorkspaceAttachmentFixture(t, "ses_workspace_events", "env_workspace_events", "e")
	canonicalSentinel := attachment.CanonicalHostRoot
	physicalSentinel := attachment.PhysicalGuestRoot
	providerSentinel := attachment.ProviderRef.ID
	viewSentinel := attachment.GuestViewRef.ID
	runSession := manager.RunSession{
		Plan:                manager.RunPlan{ProfileName: "alpha", Backend: "lima"},
		WorkspaceAttachment: attachment, RuntimeSessionDir: t.TempDir(),
	}
	factory := workspaceattach.NewPortalProviderFactory(workspaceattach.PortalProviderFactoryOptions{})
	if err := worker.prepareWorkspaceAttachment(context.Background(), factory, &runSession); err != nil {
		t.Fatal(err)
	}
	if err := worker.activateWorkspaceAttachment(context.Background(), &runSession); err != nil {
		t.Fatal(err)
	}
	if err := worker.markStarted(sessionStart{
		SessionID: attachment.SessionID, EnvironmentID: attachment.EnvironmentID,
		Profile: "alpha", Backend: "lima", TerminalMode: "pty",
		SessionSnapshotID: testDaemonSessionSnapshotID, CommandClass: "bash",
	}); err != nil {
		t.Fatal(err)
	}

	ready := waitForWorkspaceViewEvent(t, sub, workspaceattach.AttachmentReady)
	if ready.Entity.Kind != liveconsole.KindWorkspaceView || ready.Entity.ID != attachment.ID ||
		ready.Payload.Session != attachment.SessionID || ready.Payload.EnvironmentID != attachment.EnvironmentID ||
		ready.Payload.WorkspaceID != attachment.WorkspaceID || ready.Payload.WorkspaceLabel == "" ||
		ready.Payload.GuestWorkspace != workspaceattach.LogicalWorkspaceRoot ||
		ready.Payload.WorkspaceTransport != workspaceattach.SelectedTransport {
		t.Fatalf("ready workspace-view event=%+v", ready)
	}
	assertWorkspaceEventHasNoPrivateAuthority(t, ready, canonicalSentinel, physicalSentinel, providerSentinel, viewSentinel)
	assertValidDaemonEvent(t, ready)

	status := d.Status()
	if len(status.Sessions) != 1 || status.Sessions[0].WorkspaceID != attachment.WorkspaceID ||
		len(status.WorkspaceAttachments) != 1 || status.WorkspaceAttachments[0].WorkspaceID != attachment.WorkspaceID ||
		status.WorkspaceAttachments[0].State != workspaceattach.AttachmentReady {
		t.Fatalf("daemon workspace status=%+v", status)
	}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{canonicalSentinel, attachment.RootHandleIdentity, "credential.bin"} {
		if strings.Contains(string(statusJSON), forbidden) {
			t.Fatalf("daemon status leaked private workspace authority %q: %s", forbidden, statusJSON)
		}
	}
	validateDaemonStatusDocument(t, compileDaemonStatusSchema(t), decodeJSONDocument(t, statusJSON), true)

	if err := worker.releaseWorkspaceAttachment(context.Background()); err != nil {
		t.Fatal(err)
	}
	released := waitForWorkspaceViewEvent(t, sub, workspaceattach.AttachmentReleased)
	if released.Payload.CleanupStatus != workspaceattach.CleanupAbsent || released.Payload.BlockerCode != "" {
		t.Fatalf("released workspace-view event=%+v", released)
	}
	assertWorkspaceEventHasNoPrivateAuthority(t, released, canonicalSentinel, physicalSentinel, providerSentinel, viewSentinel)
	d.sessions.finish("conn-workspace-events", "")
}

func waitForWorkspaceViewEvent(t *testing.T, sub *subscriber, state workspaceattach.AttachmentState) Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-sub.ch:
			if event.Kind == liveconsole.KindWorkspaceView && event.Payload.WorkspaceViewState == state {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for workspace-view state %s", state)
		}
	}
}

func assertWorkspaceEventHasNoPrivateAuthority(t *testing.T, event Event, forbidden ...string) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(string(data), value) {
			t.Fatalf("workspace-view event leaked %q: %s", value, data)
		}
	}
}

func decodeJSONDocument(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}
