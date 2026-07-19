package liveconsole

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestWorkspaceViewCatalogHasIndependentProductionContract(t *testing.T) {
	entry, ok := catalogKinds()[KindWorkspaceView]
	if !ok {
		t.Fatal("workspace-view event kind is not cataloged")
	}
	wantFields := []string{
		"attachmentId", "session", "environmentId", "workspaceId", "workspaceLabel",
		"guestWorkspace", "workspaceTransport", "workspaceViewState",
	}
	if !reflect.DeepEqual(entry.RequiredFields, wantFields) {
		t.Fatalf("workspace-view required fields=%v want %v", entry.RequiredFields, wantFields)
	}
	if entry.Source != EventSourceProduction || entry.ProductionSite == "" || !entry.GoReducer || !entry.JSReducer {
		t.Fatalf("workspace-view catalog row is not production-complete: %+v", entry)
	}
	if !reflect.DeepEqual(entry.Panels, []string{"environments", "sessions"}) {
		t.Fatalf("workspace-view panels=%v", entry.Panels)
	}
	if mapping := EventProducerMappings()[KindWorkspaceView]; mapping != KindWorkspaceView {
		t.Fatalf("workspace-view producer mapping=%q", mapping)
	}
}

func TestWorkspaceViewReducerSeparatesMachineAndScopedViews(t *testing.T) {
	machine := manager.EnvironmentSummary{
		Schema: manager.EnvironmentSummarySchema, ID: "env_shared", Name: "default", AutoNamed: true,
		Profile: "alpha", Backend: "lima", Status: environment.StatusRunning, Mode: environment.ModeShared,
		SharedSlot: "default-alpha", MachineIdentityID: "sha256:" + strings.Repeat("a", 64),
		CreatedAt: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
	state := NewState(BuildSeed(SeedInput{
		Overview:     manager.Overview{Environments: []manager.EnvironmentSummary{machine}},
		ProfileScope: "alpha", StreamHealth: HealthLive,
	}))
	workspaceA := "wrk_" + strings.Repeat("a", 64)
	workspaceB := "wrk_" + strings.Repeat("b", 64)
	relationA := workspaceattach.RootRelationNotice{
		Relation: workspaceattach.RootDisjoint, SelectedPosition: workspaceattach.RelationPositionPeer,
		WorkspaceID: workspaceA, OtherWorkspaceID: workspaceB,
	}
	relationB := workspaceattach.RootRelationNotice{
		Relation: workspaceattach.RootDisjoint, SelectedPosition: workspaceattach.RelationPositionPeer,
		WorkspaceID: workspaceB, OtherWorkspaceID: workspaceA,
	}

	for index, event := range []Event{
		workspaceViewEventFixture(1, "alpha", "att_a", "ses_a", "env_shared", workspaceA, "project-a [aaaaaaaa]", workspaceattach.AttachmentReady, []workspaceattach.RootRelationNotice{relationA}),
		workspaceViewEventFixture(2, "alpha", "att_b", "ses_b", "env_shared", workspaceB, "project-b [bbbbbbbb]", workspaceattach.AttachmentReady, []workspaceattach.RootRelationNotice{relationB}),
	} {
		if result := Apply(&state, event); result.Status != ResultApplied {
			t.Fatalf("workspace event %d result=%+v", index, result)
		}
	}

	if len(state.Overview.Environments) != 1 {
		t.Fatalf("machine rows=%+v", state.Overview.Environments)
	}
	gotMachine := state.Overview.Environments[0]
	if gotMachine.Mode != environment.ModeShared || gotMachine.Workspace != "" || gotMachine.GuestWorkspace != "" ||
		gotMachine.WorkspaceLabel != "" || gotMachine.ActiveSessions != 2 || gotMachine.ActiveWorkspaceViews != 2 || gotMachine.WorkspaceProviderState != "ready" {
		t.Fatalf("workspace events conflated machine and views: %+v", gotMachine)
	}
	if len(state.Overview.Sessions) != 2 {
		t.Fatalf("workspace view rows=%+v", state.Overview.Sessions)
	}
	for _, row := range state.Overview.Sessions {
		if row.EnvironmentID != "env_shared" || row.Profile != "alpha" || row.WorkspaceID == "" || row.WorkspaceLabel == "" ||
			row.GuestWorkspace != workspaceattach.LogicalWorkspaceRoot || row.WorkspaceTransport != workspaceattach.SelectedTransport ||
			row.WorkspaceViewState != workspaceattach.AttachmentReady || len(row.WorkspaceRelations) != 1 {
			t.Fatalf("workspace view row=%+v", row)
		}
	}

	released := workspaceViewEventFixture(3, "alpha", "att_a", "ses_a", "env_shared", workspaceA, "project-a [aaaaaaaa]", workspaceattach.AttachmentReleased, nil)
	if result := Apply(&state, released); result.Status != ResultApplied {
		t.Fatalf("released workspace event result=%+v", result)
	}
	if got := state.Overview.Environments[0]; got.ActiveSessions != 1 || got.ActiveWorkspaceViews != 1 || got.WorkspaceProviderState != "ready" {
		t.Fatalf("released view did not preserve sibling machine state: %+v", got)
	}

	beforeSessions := len(state.Overview.Sessions)
	outside := workspaceViewEventFixture(4, "beta", "att_c", "ses_c", "env_beta", "wrk_"+strings.Repeat("c", 64), "private [cccccccc]", workspaceattach.AttachmentReady, nil)
	if result := Apply(&state, outside); result.Status != ResultIgnored || result.Reason != "event outside profile scope" {
		t.Fatalf("out-of-scope workspace event result=%+v", result)
	}
	if len(state.Overview.Sessions) != beforeSessions || state.LastSeq != 4 {
		t.Fatalf("out-of-scope event changed visible rows or sequence: rows=%+v seq=%d", state.Overview.Sessions, state.LastSeq)
	}

	if result := Apply(&state, Event{Version: EventVersion, Kind: "future-workspace-view", Seq: 5, Payload: EventPayload{ID: "future"}}); result.Status != ResultIgnored {
		t.Fatalf("unknown event result=%+v", result)
	}
	if len(state.Overview.Sessions) != beforeSessions || state.LastSeq != 5 || state.StreamHealth.State != HealthLive {
		t.Fatalf("unknown event changed state: rows=%+v seq=%d health=%+v", state.Overview.Sessions, state.LastSeq, state.StreamHealth)
	}
}

func workspaceViewEventFixture(seq int, profile, attachmentID, sessionID, environmentID, workspaceID, label string, state workspaceattach.AttachmentState, relations []workspaceattach.RootRelationNotice) Event {
	return Event{
		Version: EventVersion, Kind: KindWorkspaceView, Phase: "progress", Seq: seq,
		Entity: EntityRef{Kind: KindWorkspaceView, ID: attachmentID, Profile: profile, Session: sessionID},
		Payload: EventPayload{
			ID: sessionID, AttachmentID: attachmentID, Session: sessionID, EnvironmentID: environmentID,
			Profile: profile, WorkspaceID: workspaceID, WorkspaceLabel: label,
			GuestWorkspace: workspaceattach.LogicalWorkspaceRoot, WorkspaceTransport: workspaceattach.SelectedTransport,
			WorkspaceViewState: state, WorkspaceRelations: relations,
		},
	}
}
