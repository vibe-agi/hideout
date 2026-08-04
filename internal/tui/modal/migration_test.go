package modal

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestMigrationResumeUsesExactRevisionAndDoesNotRenderPassphrase(t *testing.T) {
	operation := migrationModalProjection(manager.MigrationRecoveryResume)
	provider := &migrationActionProviderFixture{}
	editor := NewMigration(MigrationOptions{
		Context: context.Background(), Provider: provider,
		Operation: operation, Mutable: true,
	})

	migrationModalPress(t, editor, specialKey(tea.KeyEnter))
	if editor.Stage() != MigrationStageConfirm ||
		!strings.Contains(editor.View(100), operation.Recovery.NextAction) {
		t.Fatalf("resume review is missing:\n%s", editor.View(100))
	}
	migrationModalPress(t, editor, key("y"))
	const passphrase = "resume-migration-secret"
	migrationModalType(t, editor, passphrase)
	secretView := editor.View(72)
	if strings.Contains(secretView, passphrase) || !strings.Contains(secretView, "••••") {
		t.Fatalf("resume passphrase was not masked:\n%s", secretView)
	}
	command := migrationModalPress(t, editor, specialKey(tea.KeyEnter))
	migrationModalRun(t, editor, command)
	if provider.calls != 1 || provider.request.Action != MigrationActionResume ||
		provider.request.Operation.OperationID != operation.OperationID ||
		provider.request.Operation.Revision != operation.Revision ||
		provider.passphrase != passphrase || len(editor.secretInput) != 0 ||
		editor.Stage() != MigrationStageTerminal {
		t.Fatalf(
			"calls=%d request=%+v passphrase=%q secret=%d stage=%s",
			provider.calls, provider.request, provider.passphrase,
			len(editor.secretInput), editor.Stage(),
		)
	}
	if strings.Contains(editor.View(100), passphrase) {
		t.Fatal("terminal migration result rendered the passphrase")
	}
}

func TestMigrationCancelMakesPartialBundleDispositionExplicit(t *testing.T) {
	operation := migrationModalProjection("")
	operation.State = manager.MigrationPhaseWriting
	operation.PhaseLabel = "Writing the portable bundle"
	operation.Recovery = manager.MigrationRecoveryProjection{
		Code:           "migration.operation.none",
		AllowedActions: []manager.MigrationRecoveryAction{},
	}
	provider := &migrationActionProviderFixture{}
	editor := NewMigration(MigrationOptions{
		Context: context.Background(), Provider: provider,
		Operation: operation, Mutable: true,
	})
	migrationModalPress(t, editor, key("c"))
	if !strings.Contains(editor.View(100), "retain unsealed partial output") {
		t.Fatalf("default partial disposition is hidden:\n%s", editor.View(100))
	}
	migrationModalPress(t, editor, key("t"))
	if !strings.Contains(editor.View(100), "remove unsealed partial output") {
		t.Fatalf("toggled partial disposition is hidden:\n%s", editor.View(100))
	}
	migrationModalRun(t, editor, migrationModalPress(t, editor, key("y")))
	if provider.request.Action != MigrationActionCancel || provider.request.RetainPartial {
		t.Fatalf("cancel request=%+v", provider.request)
	}
}

func TestMigrationRevisionChangeInvalidatesPendingSecretAuthority(t *testing.T) {
	operation := migrationModalProjection(manager.MigrationRecoveryResume)
	editor := NewMigration(MigrationOptions{
		Context: context.Background(), Provider: &migrationActionProviderFixture{},
		Operation: operation, Mutable: true,
	})
	migrationModalPress(t, editor, specialKey(tea.KeyEnter))
	migrationModalPress(t, editor, key("y"))
	migrationModalType(t, editor, "clear-on-revision-change")
	newer := operation
	newer.Revision++
	if err := newer.Validate(); err != nil {
		t.Fatalf("newer projection is invalid: %v", err)
	}
	editor.SyncAuthority(true, newer, "")
	if editor.Stage() != MigrationStageStale || len(editor.secretInput) != 0 {
		t.Fatalf("stage=%s secret bytes=%d", editor.Stage(), len(editor.secretInput))
	}
}

type migrationActionProviderFixture struct {
	calls      int
	request    MigrationActionRequest
	passphrase string
}

func (provider *migrationActionProviderFixture) ApplyMigrationAction(
	_ context.Context,
	request MigrationActionRequest,
) (manager.MigrationOperationProjection, error) {
	provider.calls++
	provider.request = request
	provider.request.Passphrase = nil
	provider.passphrase = string(request.Passphrase)
	updated := request.Operation
	updated.Revision++
	updated.State = manager.MigrationPhaseWriting
	updated.PhaseLabel = "Writing the portable bundle"
	updated.Recovery = manager.MigrationRecoveryProjection{
		Code:           "migration.operation.none",
		AllowedActions: []manager.MigrationRecoveryAction{},
	}
	updated.Progress.PhaseStartedAt = updated.Progress.CheckpointAt
	return updated, nil
}

func migrationModalProjection(
	action manager.MigrationRecoveryAction,
) manager.MigrationOperationProjection {
	at := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	projection := manager.MigrationOperationProjection{
		Schema:      manager.MigrationOperationProjectionSchema,
		OperationID: "op_migration_modalfixture1", Revision: 4,
		BundleID: "migb_modalfixture1", Kind: manager.MigrationOperationExport,
		State:      manager.MigrationPhaseRecoverableFailure,
		PhaseLabel: "Waiting for recovery",
		Progress: manager.MigrationProgressProjection{
			LogicalTotalKnown: true, CompletedLogicalBytes: 4,
			TotalLogicalBytes: 10, PhaseStartedAt: at, CheckpointAt: at,
		},
		Warnings: []manager.MigrationNotice{}, Effects: []manager.MigrationEffectProjection{},
	}
	if action == "" {
		projection.Recovery = manager.MigrationRecoveryProjection{
			Code:           "migration.operation.none",
			AllowedActions: []manager.MigrationRecoveryAction{},
		}
		return projection
	}
	projection.Recovery = manager.MigrationRecoveryProjection{
		Required: true, Code: "migration.export.resume_required",
		NextAction:     "Resume the same migration operation.",
		AllowedActions: []manager.MigrationRecoveryAction{action},
	}
	return projection
}

func migrationModalPress(t *testing.T, editor *Migration, message tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	command, outcome := editor.Update(message)
	if outcome.Close {
		t.Fatal("migration modal unexpectedly closed")
	}
	return command
}

func migrationModalType(t *testing.T, editor *Migration, value string) {
	t.Helper()
	for _, character := range value {
		migrationModalPress(t, editor, key(string(character)))
	}
}

func migrationModalRun(t *testing.T, editor *Migration, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("expected asynchronous migration action")
	}
	next, outcome := editor.Update(command())
	if next != nil || outcome.Close || outcome.Projection == nil {
		t.Fatalf("unexpected migration response: next=%t outcome=%+v", next != nil, outcome)
	}
}
