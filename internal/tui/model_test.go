package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/operatorhelp"
)

func TestModelResizeKeepsQuitAndHelpAvailableBelowMinimum(t *testing.T) {
	model := NewModel(ModelOptions{
		State: v2ModelState(), Width: 100, Height: 30, NoColor: true, Unicode: false,
	})
	if model.Layout() != LayoutNormal {
		t.Fatalf("initial layout=%s", model.Layout())
	}
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 68, Height: 24})
	if model.Layout() != LayoutNarrow {
		t.Fatalf("narrow layout=%s", model.Layout())
	}
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 40, Height: 10})
	if model.Layout() != LayoutTooSmall {
		t.Fatalf("too-small layout=%s", model.Layout())
	}
	model = updateModel(t, model, key("?"))
	if model.ModalDepth() != 1 || model.TopModal() != ModalHelp {
		t.Fatalf("help unavailable below minimum: depth=%d top=%s", model.ModalDepth(), model.TopModal())
	}
	model = updateModel(t, model, specialKey(tea.KeyEsc))
	if model.ModalDepth() != 0 {
		t.Fatalf("escape did not close help: depth=%d", model.ModalDepth())
	}
	_, quit := model.Update(key("q"))
	if quit == nil {
		t.Fatal("quit unavailable below minimum")
	}
}

func TestModelHelpModalUsesInjectedCatalogForActiveView(t *testing.T) {
	catalog := operatorhelp.Catalog{
		Schema: operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{{
			ID: "activity", Name: "activity", TaskGroup: "Observe",
			Purpose:       "CATALOG SENTINEL activity evidence.",
			Syntax:        []string{"hideout activity summary"},
			Examples:      []string{"hideout activity summary"},
			Prerequisites: []string{"daemon"},
			Effects:       []string{"read-only"},
			Safety:        []string{"coverage-aware"},
			Recovery:      []string{"inspect coverage"},
			Next:          []string{"hideout tui"},
			Audience:      operatorhelp.AudienceNewUser,
			Stability:     operatorhelp.StabilityStable,
		}},
	}
	model := NewModel(ModelOptions{
		State: v2ModelState(), Width: 100, Height: 30,
		NoColor: true, HelpCatalog: catalog,
	})
	model = updateModel(t, model, key("2"))
	model = updateModel(t, model, key("?"))
	content := model.View().Content
	for _, want := range []string{
		"Hideout · Help · Activity",
		"CATALOG SENTINEL activity evidence.",
		"hideout activity summary",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("catalog help missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "1-5 views") {
		t.Fatalf("legacy non-contextual help remains:\n%s", content)
	}
	if !strings.Contains(content, "1-6 views") {
		t.Fatalf("current view shortcuts are missing:\n%s", content)
	}
}

func TestModelFocusSessionSelectionAndDetailDrillDown(t *testing.T) {
	model := NewModel(ModelOptions{
		State: v2ModelState(), Width: 100, Height: 30, SessionID: "ses_alpha",
	})
	if model.SelectedSession() != "ses_alpha" || model.Focus() != FocusPrimary {
		t.Fatalf("initial selection/focus: session=%q focus=%s", model.SelectedSession(), model.Focus())
	}
	model = updateModel(t, model, specialKey(tea.KeyTab))
	if model.Focus() != FocusDetails {
		t.Fatalf("tab focus=%s want details", model.Focus())
	}
	model = updateModel(t, model, specialKey(tea.KeyTab))
	if model.Focus() != FocusFooter {
		t.Fatalf("second tab focus=%s want footer", model.Focus())
	}
	model = updateModel(t, model, shiftSpecialKey(tea.KeyTab))
	if model.Focus() != FocusDetails {
		t.Fatalf("backtab focus=%s want details", model.Focus())
	}
	model = updateModel(t, model, key("j"))
	if model.SelectedSession() != "ses_beta" {
		t.Fatalf("session selection=%q want ses_beta", model.SelectedSession())
	}
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	if !model.DetailOpen() || model.Layout() != LayoutNormal {
		t.Fatalf("enter did not open side detail: open=%t layout=%s", model.DetailOpen(), model.Layout())
	}
	model = updateModel(t, model, key("2"))
	if model.ActiveView() != ViewActivity || model.DetailOpen() {
		t.Fatalf("view switch did not reset detail: view=%s detail=%t", model.ActiveView(), model.DetailOpen())
	}
}

func TestModelCoalescesContiguousEventBurstWithoutLosingSequence(t *testing.T) {
	model := NewModel(ModelOptions{State: v2ModelState(), Width: 100, Height: 30})
	for sequence := 11; sequence <= 74; sequence++ {
		model = updateModel(t, model, EventMsg{Event: liveconsole.Event{
			Version: liveconsole.EventVersionV2, InstanceID: "daemon_fixture",
			CredentialGeneration: 4, Kind: fmt.Sprintf("optional-%d", sequence),
			Optional: true, Seq: sequence,
		}})
	}
	if model.PendingEvents() != 64 {
		t.Fatalf("pending event count=%d want 64", model.PendingEvents())
	}
	if model.ConsoleState().LastSeq != 10 {
		t.Fatalf("event burst rendered before frame coalescing: seq=%d", model.ConsoleState().LastSeq)
	}
	model = updateModel(t, model, FrameTickMsg{})
	if model.PendingEvents() != 0 || model.ConsoleState().LastSeq != 74 {
		t.Fatalf("coalesced event sequence mismatch: pending=%d seq=%d", model.PendingEvents(), model.ConsoleState().LastSeq)
	}
	if !model.MutationEnabled() {
		t.Fatalf("contiguous optional burst incorrectly disabled mutation: %+v", model.ConsoleState().StreamHealth)
	}
}

func TestModelSequenceGapMakesConfigReadOnlyUntilAuthoritativeSnapshot(t *testing.T) {
	model := NewModel(ModelOptions{State: v2ModelState(), Width: 100, Height: 30})
	model = updateModel(t, model, EventMsg{Event: liveconsole.Event{
		Version: liveconsole.EventVersionV2, InstanceID: "daemon_fixture",
		CredentialGeneration: 4, Kind: "future-optional", Optional: true, Seq: 12,
	}})
	model = updateModel(t, model, FrameTickMsg{})
	if model.MutationEnabled() || model.ConsoleState().StreamHealth.State != liveconsole.HealthStale {
		t.Fatalf("gap did not disable mutation: mutable=%t health=%+v", model.MutationEnabled(), model.ConsoleState().StreamHealth)
	}
	model = updateModel(t, model, key("3"))
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	if model.ModalDepth() != 0 {
		t.Fatalf("stale config opened a mutation modal: depth=%d", model.ModalDepth())
	}

	reseed := v2ModelState()
	reseed.DaemonInstanceID = "daemon_reseeded"
	reseed.CredentialGeneration = 5
	reseed.LastSeq = 20
	model = updateModel(t, model, SnapshotMsg{State: reseed})
	if !model.MutationEnabled() || model.ConsoleState().LastSeq != 20 ||
		model.ConsoleState().DaemonInstanceID != "daemon_reseeded" {
		t.Fatalf("authoritative reseed did not restore model: %+v", model.ConsoleState())
	}
}

func TestModelOperationsSelectionDetailAndExactResponseLossResume(
	t *testing.T,
) {
	state := v2ModelState()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	state.Operations = []manager.Operation{
		modelOperationFixture(
			"op_newerfixture0001",
			manager.OperationRecoveryRequired,
			now,
		),
		modelOperationFixture(
			"op_resumefixture0001",
			manager.OperationSucceeded,
			now.Add(-time.Minute),
		),
	}
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true,
	})
	model = updateModel(t, model, key("4"))
	if model.ActiveView() != ViewOperations ||
		model.OperationsSelected() != 0 {
		t.Fatalf(
			"operations initial view=%s selected=%d",
			model.ActiveView(),
			model.OperationsSelected(),
		)
	}
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	if !model.DetailOpen() ||
		!strings.Contains(
			model.View().Content,
			"Effect persist-profile",
		) {
		t.Fatalf(
			"operation details did not open:\n%s",
			model.View().Content,
		)
	}

	model.operationLookupID = "op_resumefixture0001"
	model.clampOperationsSelection()
	if model.OperationsSelected() != 1 ||
		!strings.Contains(
			model.View().Content,
			"Resumed exact operation op_resumefixture0001",
		) {
		t.Fatalf(
			"exact response-loss operation was not selected:\n%s",
			model.View().Content,
		)
	}
	model = updateModel(t, model, key("j"))
	if model.OperationLookupID() != "" ||
		model.OperationsSelected() != 0 {
		t.Fatalf(
			"manual browse retained stale lookup: lookup=%q selected=%d",
			model.OperationLookupID(),
			model.OperationsSelected(),
		)
	}
}

func modelOperationFixture(
	id string,
	phase string,
	at time.Time,
) manager.Operation {
	operation := manager.Operation{
		Schema: manager.OperationSchema,
		ID:     id,
		Kind:   "profile.transaction",
		Owner: manager.OperationOwner{
			Kind: "profile", ID: "default",
		},
		PlanDigest:   "sha256:" + strings.Repeat("a", 64),
		BaseRevision: 1,
		Phase:        phase,
		Effects: []manager.EffectResult{{
			ID:       "persist-profile",
			Kind:     "persist",
			Provider: "manager",
			Status:   manager.EffectSucceeded,
			Evidence: []manager.EvidenceRef{{
				Code:       "profile.revision",
				Ref:        "2",
				ObservedAt: at,
			}},
		}},
		Recovery: manager.Recovery{
			Code:    "inspect-operation",
			Summary: "inspect the exact operation",
		},
		CreatedAt: at,
		UpdatedAt: at,
	}
	if phase == manager.OperationSucceeded {
		operation.Result = &manager.OperationResult{
			Status:  phase,
			Code:    "configuration-applied",
			Summary: "configuration committed",
		}
	}
	return operation
}

func v2ModelState() liveconsole.State {
	return liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{
		DaemonInstanceID: "daemon_fixture", CredentialGeneration: 4, EventSequence: 10,
		StreamHealth: liveconsole.HealthLive,
		Overview: manager.Overview{Version: "hideout.manager/v1", Sessions: []manager.SessionSummary{
			{ID: "ses_alpha", Profile: "alpha", State: "running", CommandClass: "claude"},
			{ID: "ses_beta", Profile: "beta", State: "running", CommandClass: "codex"},
		}},
	}))
}

func updateModel(t *testing.T, model *Model, message tea.Msg) *Model {
	t.Helper()
	updated, _ := model.Update(message)
	next, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}
	return next
}

func key(value string) tea.KeyPressMsg {
	runes := []rune(value)
	return tea.KeyPressMsg(tea.Key{Text: value, Code: runes[0]})
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func shiftSpecialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Mod: tea.ModShift})
}
