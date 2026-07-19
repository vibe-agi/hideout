package manager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestOverviewSeparatesSharedMachineFromActiveWorkspaceViews(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	sharedRunProfile(t, store, "default")
	envStore := environment.Store{Root: store.Root}
	record, err := envStore.Create(environment.Spec{
		Name: "default", AutoNamed: true, ImageRef: environment.BuiltinBaseImage,
		Profile: "default", Backend: "lima", Mode: environment.ModeShared,
		SharedSlot: environment.SharedSlotID("default"), MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(),
		InstanceName: "hideout-shared-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = environment.StatusRunning
	record.LastSessionID = "ses_historical_workspace_must_not_select"
	record.LastCommand = "historical-workspace-command"
	if err := envStore.Save(record); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	workspaceA := "wrk_" + strings.Repeat("a", 64)
	workspaceB := "wrk_" + strings.Repeat("b", 64)
	ownerA := acquireWorkspaceSummaryOwner(t, envStore, session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_workspace_a", EnvironmentID: record.ID,
		Profile: "default", Backend: "lima", WorkspaceID: workspaceA, SessionSnapshotID: testSessionSnapshotID(),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalPTY,
		StartedAt: now, UpdatedAt: now, CommandClass: "shell",
	})
	defer ownerA.Close()
	ownerB := acquireWorkspaceSummaryOwner(t, envStore, session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_workspace_b", EnvironmentID: record.ID,
		Profile: "default", Backend: "lima", WorkspaceID: workspaceB, SessionSnapshotID: testSessionSnapshotID(),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second), CommandClass: "agent",
	})
	defer ownerB.Close()

	canonicalSentinel := "/Users/host-secret/projects/private-a"
	core := New(store)
	core.ActiveWorkspaceViews = func() []WorkspaceViewSnapshot {
		return []WorkspaceViewSnapshot{
			workspaceViewSnapshotFixture(record.ID, "ses_workspace_a", workspaceA, "private-a [aaaaaaaa]", canonicalSentinel,
				workspaceattach.RootRelationNotice{Relation: workspaceattach.RootDisjoint, SelectedPosition: workspaceattach.RelationPositionPeer, WorkspaceID: workspaceA, OtherWorkspaceID: workspaceB}),
			workspaceViewSnapshotFixture(record.ID, "ses_workspace_b", workspaceB, "private-b [bbbbbbbb]", "/Users/host-secret/projects/private-b",
				workspaceattach.RootRelationNotice{Relation: workspaceattach.RootDisjoint, SelectedPosition: workspaceattach.RelationPositionPeer, WorkspaceID: workspaceB, OtherWorkspaceID: workspaceA}),
		}
	}
	overview, err := core.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Environments) != 1 {
		t.Fatalf("environment rows=%+v", overview.Environments)
	}
	machine := overview.Environments[0]
	if machine.Schema != EnvironmentSummarySchema || machine.Mode != environment.ModeShared ||
		machine.SharedSlot != environment.SharedSlotID("default") || machine.MachineIdentityID != testEnvironmentMachineIdentityID() {
		t.Fatalf("shared machine summary=%+v", machine)
	}
	if machine.Workspace != "" || machine.GuestWorkspace != "" || machine.WorkspaceLabel != "" ||
		machine.LastSessionID != "" || machine.LastCommand != "" {
		t.Fatalf("shared machine retained selected/last workspace state: %+v", machine)
	}
	if machine.ActiveSessions != 2 || machine.ActiveWorkspaceViews != 2 || machine.WorkspaceProviderState != "ready" {
		t.Fatalf("shared machine counts/provider=%+v", machine)
	}
	machineJSON, err := json.Marshal(machine)
	if err != nil {
		t.Fatal(err)
	}
	var machineDocument map[string]any
	if err := json.Unmarshal(machineJSON, &machineDocument); err != nil {
		t.Fatal(err)
	}
	validateEnvironmentSummary(t, compileEnvironmentSummarySchema(t), machineDocument, true)
	if len(overview.Sessions) != 2 {
		t.Fatalf("session rows=%+v", overview.Sessions)
	}
	for _, row := range overview.Sessions {
		if row.EnvironmentID != record.ID || row.WorkspaceID == "" || row.WorkspaceLabel == "" ||
			row.GuestWorkspace != workspaceattach.LogicalWorkspaceRoot || row.WorkspaceTransport != workspaceattach.SelectedTransport ||
			row.WorkspaceViewState != workspaceattach.AttachmentReady || len(row.WorkspaceRelations) != 1 {
			t.Fatalf("workspace view row=%+v", row)
		}
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{canonicalSentinel, "/Users/host-secret", "root-handle-secret", "provider-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("public overview leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestScopeOverviewKeepsMachineAndWorkspaceViewsInOneProfile(t *testing.T) {
	overview := Overview{
		Profiles: []ProfileSummary{{Name: "alpha"}, {Name: "beta"}},
		Environments: []EnvironmentSummary{
			{ID: "env_alpha", Profile: "alpha"}, {ID: "env_beta", Profile: "beta"},
		},
		Sessions: []SessionSummary{
			{ID: "ses_alpha", Profile: "alpha", EnvironmentID: "env_alpha", WorkspaceID: "wrk_" + strings.Repeat("a", 64)},
			{ID: "ses_beta", Profile: "beta", EnvironmentID: "env_beta", WorkspaceID: "wrk_" + strings.Repeat("b", 64)},
		},
		Network: NetworkSummary{ProfileDefaults: []ProfileNetworkSummary{{Profile: "alpha"}, {Profile: "beta"}}},
	}
	scoped := ScopeOverview(overview, "beta")
	if len(scoped.Profiles) != 1 || scoped.Profiles[0].Name != "beta" ||
		len(scoped.Environments) != 1 || scoped.Environments[0].ID != "env_beta" ||
		len(scoped.Sessions) != 1 || scoped.Sessions[0].ID != "ses_beta" ||
		len(scoped.Network.ProfileDefaults) != 1 || scoped.Network.ProfileDefaults[0].Profile != "beta" {
		t.Fatalf("profile-scoped machine/view overview=%+v", scoped)
	}
}

func acquireWorkspaceSummaryOwner(t *testing.T, store environment.Store, record session.OwnerRecord) *session.Owner {
	t.Helper()
	owner, err := session.AcquireOwner(store.OwnerRoot(record.EnvironmentID), record)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func workspaceViewSnapshotFixture(environmentID, sessionID, workspaceID, label, canonical string, relation workspaceattach.RootRelationNotice) WorkspaceViewSnapshot {
	now := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	return WorkspaceViewSnapshot{
		Attachment: workspaceattach.AttachmentSummary{
			Schema: workspaceattach.AttachmentSummarySchema, AttachmentID: "att_" + strings.Repeat("c", 32),
			SessionID: sessionID, EnvironmentID: environmentID, WorkspaceID: workspaceID,
			DisplayLabel: label, LogicalGuestRoot: workspaceattach.LogicalWorkspaceRoot,
			PhysicalGuestRoot: workspaceattach.PhysicalWorkspaceBase + "/" + workspaceID,
			Transport:         workspaceattach.SelectedTransport, State: workspaceattach.AttachmentReady, CreatedAt: now,
		},
		Relations:          []workspaceattach.RootRelationNotice{relation},
		CanonicalHostRoot:  canonical,
		RootHandleIdentity: "root-handle-secret",
		ProviderCredential: "provider-secret",
	}
}
