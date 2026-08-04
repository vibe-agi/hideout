package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	tuimodal "github.com/vibe-agi/hideout/internal/tui/modal"
)

func TestMigrationKeyboardHUDAndAuthoritativeRefreshDoNotRegress(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	state := v2ModelState()
	state.Migrations = []manager.MigrationOperationProjection{
		modelMigrationProjection(now, "op_migrationnewer1", 2),
		modelMigrationProjection(now.Add(-time.Minute), "op_migrationolder1", 1),
	}
	refreshes := 0
	model := NewModel(ModelOptions{
		State: state, Width: 112, Height: 30, NoColor: true,
		SnapshotProvider: func(context.Context) (liveconsole.State, error) {
			refreshes++
			refreshed := state
			refreshed.LastSeq = state.LastSeq + refreshes
			if refreshes > 1 {
				refreshed.Migrations = []manager.MigrationOperationProjection{
					modelMigrationProjection(now.Add(time.Second), "op_migrationrefreshed1", 3),
				}
			}
			return refreshed, nil
		},
	})
	updated, command := model.Update(key("5"))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("entering migration did not request an authoritative snapshot")
	}
	initialMessage := command()
	loaded, ok := initialMessage.(SnapshotRefreshLoadedMsg)
	if !ok || loaded.err != nil || refreshes != 1 {
		t.Fatalf("initial migration refresh=%T %+v refreshes=%d", initialMessage, initialMessage, refreshes)
	}
	model = updateModel(t, model, loaded)
	if model.ActiveView() != ViewMigration || model.MigrationSelected() != 0 {
		t.Fatalf("migration view=%s selected=%d", model.ActiveView(), model.MigrationSelected())
	}
	model = updateModel(t, model, key("j"))
	if model.MigrationSelected() != 1 {
		t.Fatalf("migration selection=%d", model.MigrationSelected())
	}
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	if model.TopModal() != ModalMigration ||
		!strings.Contains(model.View().Content, "c review cancellation") {
		t.Fatalf("migration action modal did not open:\n%s", model.View().Content)
	}

	_, command = model.Update(SnapshotRefreshTickMsg{At: now})
	if command == nil {
		t.Fatal("migration snapshot refresh did not issue a command")
	}
	message := command()
	loaded, ok = message.(SnapshotRefreshLoadedMsg)
	if !ok || loaded.err != nil || refreshes != 2 {
		t.Fatalf("snapshot refresh message=%T %+v refreshes=%d", message, message, refreshes)
	}
	model = updateModel(t, model, loaded)
	if len(model.ConsoleState().Migrations) != 1 ||
		model.ConsoleState().Migrations[0].OperationID != "op_migrationrefreshed1" {
		t.Fatalf("authoritative migration refresh was not installed: %+v", model.ConsoleState().Migrations)
	}

	regressed := state
	regressed.LastSeq = state.LastSeq
	regressed.Migrations = []manager.MigrationOperationProjection{
		modelMigrationProjection(now.Add(-time.Hour), "op_migrationstale1", 1),
	}
	model.snapshotRequest++
	model.snapshotLoading = true
	model = updateModel(t, model, SnapshotRefreshLoadedMsg{
		requestID: model.snapshotRequest, state: regressed,
	})
	if model.ConsoleState().Migrations[0].OperationID != "op_migrationrefreshed1" {
		t.Fatalf("older in-flight snapshot regressed migration state: %+v", model.ConsoleState().Migrations)
	}
}

func TestSnapshotRefreshRunsOnlyForVisibleMigrationOrFallback(t *testing.T) {
	state := v2ModelState()
	events := make(chan liveconsole.Event)
	refreshes := 0
	model := NewModel(ModelOptions{
		State: state, Events: events,
		SnapshotProvider: func(context.Context) (liveconsole.State, error) {
			refreshes++
			refreshed := state
			refreshed.LastSeq += refreshes
			return refreshed, nil
		},
	})
	if _, command := model.Update(SnapshotRefreshTickMsg{}); command != nil {
		t.Fatal("healthy overview scheduled hidden snapshot polling")
	}
	if refreshes != 0 {
		t.Fatalf("healthy overview refreshes=%d want=0", refreshes)
	}

	updated, command := model.Update(key("5"))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("visible migration did not request a snapshot")
	}
	message := command()
	loaded, ok := message.(SnapshotRefreshLoadedMsg)
	if !ok || loaded.err != nil || refreshes != 1 {
		t.Fatalf("visible migration refresh=%T %+v refreshes=%d", message, message, refreshes)
	}
	model = updateModel(t, model, loaded)
	model = updateModel(t, model, key("1"))
	if _, command := model.Update(SnapshotRefreshTickMsg{}); command != nil {
		t.Fatal("leaving migration kept hidden snapshot polling active")
	}

	updated, command = model.Update(StreamClosedMsg{Reason: "test disconnect"})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("stream close did not start fallback refresh")
	}
	message = command()
	loaded, ok = message.(SnapshotRefreshLoadedMsg)
	if !ok || loaded.err != nil || refreshes != 2 {
		t.Fatalf("fallback refresh=%T %+v refreshes=%d", message, message, refreshes)
	}
}

func TestMigrationHUDOpensGuidedExportImportAndChooseFlows(t *testing.T) {
	state := v2ModelState()
	state.Migrations = nil
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 30, NoColor: true,
	})
	model = updateModel(t, model, key("5"))
	model = updateModel(t, model, key("x"))
	if model.TopModal() != ModalMigration ||
		model.MigrationWizardStage() != tuimodal.MigrationWizardExportSelect ||
		!strings.Contains(model.View().Content, "Export scope") {
		t.Fatalf("export wizard did not open:\n%s", model.View().Content)
	}
	model = updateModel(t, model, specialKey(tea.KeyEsc))
	model = updateModel(t, model, key("i"))
	if model.MigrationWizardStage() != tuimodal.MigrationWizardImportPath ||
		!strings.Contains(model.View().Content, "Import bundle") {
		t.Fatalf("import wizard did not open:\n%s", model.View().Content)
	}
	model = updateModel(t, model, specialKey(tea.KeyEsc))
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	if model.MigrationWizardStage() != tuimodal.MigrationWizardChoose ||
		!strings.Contains(model.View().Content, "Choose a task") {
		t.Fatalf("empty HUD did not open task chooser:\n%s", model.View().Content)
	}
}

func TestMigrationWizardBecomesStaleWhenSnapshotAuthorityIsLost(t *testing.T) {
	state := v2ModelState()
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 30, NoColor: true,
	})
	model = updateModel(t, model, key("5"))
	model = updateModel(t, model, key("x"))
	model = updateModel(t, model, StreamClosedMsg{Reason: "daemon connection lost"})
	if model.MigrationWizardStage() != tuimodal.MigrationWizardStale ||
		!strings.Contains(model.View().Content, "STALE") ||
		!strings.Contains(model.View().Content, "daemon connection lost") {
		t.Fatalf("migration wizard retained stale authority:\n%s", model.View().Content)
	}
}

func modelMigrationProjection(
	at time.Time,
	operationID string,
	revision uint64,
) manager.MigrationOperationProjection {
	return manager.MigrationOperationProjection{
		Schema:      manager.MigrationOperationProjectionSchema,
		OperationID: operationID, Revision: revision,
		BundleID: "migb_modelmigration1", Kind: manager.MigrationOperationExport,
		State:      manager.MigrationPhaseWriting,
		PhaseLabel: "Writing the portable bundle",
		Progress: manager.MigrationProgressProjection{
			LogicalTotalKnown: true, CompletedLogicalBytes: revision,
			TotalLogicalBytes: 10, PhaseStartedAt: at, CheckpointAt: at,
		},
		Recovery: manager.MigrationRecoveryProjection{
			Code:           "migration.operation.none",
			AllowedActions: []manager.MigrationRecoveryAction{},
		},
		Warnings: []manager.MigrationNotice{},
		Effects:  []manager.MigrationEffectProjection{},
	}
}
