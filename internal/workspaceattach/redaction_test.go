package workspaceattach_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestWorkspacePublicProjectionAndEvidenceBoundaryUseRealSentinels(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "operator-private-project")
	if err := os.MkdirAll(hostRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, identity, err := workspaceattach.CaptureRootIdentity(hostRoot)
	if err != nil {
		t.Fatal(err)
	}
	identityKeySentinel := "workspace-identity-key-0123456789abcdef0123456789abcdef"
	capabilityToken := "cap_0123456789abcdef0123456789abcdef"
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	attachment := workspaceattach.Attachment{
		ID: "att_0123456789abcdef0123456789abcdef", SessionID: "ses_redaction", EnvironmentID: "env_redaction",
		Incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: "env_redaction", StartGeneration: 1, InstanceName: "hideout-redaction",
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
		WorkspaceID: workspaceID, CanonicalHostRoot: canonical, RootFileIdentity: identity,
		RootHandleIdentity: identityKeySentinel, LogicalGuestRoot: workspaceattach.LogicalWorkspaceRoot,
		PhysicalGuestRoot: workspaceattach.PhysicalWorkspaceBase + "/" + workspaceID,
		Transport:         workspaceattach.SelectedTransport,
		ProviderRef: lifecycle.ResourceRef{
			Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-redaction", Generation: 1,
		},
		GuestViewRef: lifecycle.ResourceRef{
			Kind: lifecycle.KindWorkspaceGuestView, ID: "view-redaction", Generation: 1,
		},
		State: workspaceattach.AttachmentReady, CreatedAt: time.Now().UTC(),
	}
	if err := attachment.Validate(); err != nil {
		t.Fatal(err)
	}
	publicJSON, err := json.Marshal(attachment.Summary())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{canonical, identityKeySentinel, capabilityToken, "canonicalHostRoot", "rootHandleIdentity"} {
		if strings.Contains(string(publicJSON), forbidden) {
			t.Fatalf("public workspace projection leaked %q: %s", forbidden, publicJSON)
		}
	}
	if !strings.Contains(string(publicJSON), workspaceID) || !strings.Contains(string(publicJSON), "/workspace") {
		t.Fatalf("public workspace projection lost correlation/logical fields: %s", publicJSON)
	}

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	event := audit.Event{
		Session: attachment.SessionID, Profile: "alpha", Backend: "lima",
		Action: "workspace.mapping", Decision: "allow",
		Details: map[string]any{
			"environmentId": attachment.EnvironmentID, "workspaceId": workspaceID,
			"hostPath": canonical, "guestPath": workspaceattach.LogicalWorkspaceRoot,
			"capabilityToken": capabilityToken,
		},
	}
	if err := writer.Emit(event); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	localBody, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{canonical, attachment.EnvironmentID, attachment.SessionID, workspaceID} {
		if !strings.Contains(string(localBody), required) {
			t.Fatalf("operator-local audit lost %q: %s", required, localBody)
		}
	}
	if strings.Contains(string(localBody), capabilityToken) || !strings.Contains(string(localBody), `"capabilityToken":"REDACTED"`) {
		t.Fatalf("operator-local audit control-plane redaction mismatch: %s", localBody)
	}

	var localEvent audit.Event
	if err := json.Unmarshal(localBody, &localEvent); err != nil {
		t.Fatal(err)
	}
	exportEvent := exportboundary.AuditEvent{
		Time: localEvent.Time, Session: localEvent.Session, Profile: localEvent.Profile,
		Backend: localEvent.Backend, Action: localEvent.Action, Decision: localEvent.Decision,
		Details: localEvent.Details,
	}
	blockedOut := filepath.Join(t.TempDir(), "blocked-export.json")
	blocked := exportboundary.Request{
		Source: exportboundary.SourceAudit, AuditEvents: []exportboundary.AuditEvent{exportEvent},
		Out: blockedOut, StoreRoot: t.TempDir(),
	}
	if _, err := exportboundary.Apply(blocked); err == nil || !strings.Contains(err.Error(), "user data is present") {
		t.Fatalf("unacknowledged workspace export error=%v", err)
	}
	if _, err := os.Stat(blockedOut); !os.IsNotExist(err) {
		t.Fatalf("unacknowledged export created an artifact: %v", err)
	}

	approvedOut := filepath.Join(t.TempDir(), "approved-export.json")
	approved := blocked
	approved.Out = approvedOut
	approved.StoreRoot = t.TempDir()
	approved.AcknowledgeFullFidelity = true
	if _, err := exportboundary.Apply(approved); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(approvedOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{canonical, attachment.EnvironmentID, workspaceID, "acknowledge-full-fidelity"} {
		if !strings.Contains(string(exported), required) {
			t.Fatalf("approved export lost %q: %s", required, exported)
		}
	}
	if strings.Contains(string(exported), capabilityToken) {
		t.Fatalf("approved export restored a control-plane token: %s", exported)
	}
}
