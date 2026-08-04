package modal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/secrets"
)

type MigrationAction string

const (
	MigrationActionResume  MigrationAction = "resume"
	MigrationActionCancel  MigrationAction = "cancel"
	MigrationActionRecover MigrationAction = "recover"
)

type MigrationActionRequest struct {
	Operation     manager.MigrationOperationProjection
	Action        MigrationAction
	RetainPartial bool
	Passphrase    []byte
}

// MigrationProvider is the authenticated Manager action boundary. The modal
// owns only an ephemeral passphrase buffer and the currently displayed
// revision; it never writes operation state or provider storage directly.
type MigrationActionProvider interface {
	ApplyMigrationAction(
		context.Context,
		MigrationActionRequest,
	) (manager.MigrationOperationProjection, error)
}

type MigrationExportSession struct {
	Plan              migration.ExportPlan
	SecretInputHandle string
}

type MigrationImportSession struct {
	Inspection        manager.MigrationReadOnlyInspection
	SecretInputHandle string
}

type MigrationCreationProvider interface {
	PlanMigrationExport(
		context.Context,
		migration.ExportRequest,
		[]byte,
	) (MigrationExportSession, error)
	ApplyMigrationExport(
		context.Context,
		MigrationExportSession,
	) (manager.MigrationApplyResult, error)
	UnlockMigrationImport(
		context.Context,
		string,
		[]byte,
	) (MigrationImportSession, error)
	PlanMigrationImport(
		context.Context,
		migration.ImportDraft,
		string,
	) (migration.ImportPlan, error)
	ApplyMigrationImport(
		context.Context,
		migration.ImportPlan,
		string,
	) (manager.MigrationApplyResult, error)
}

type MigrationProvider interface {
	MigrationActionProvider
	MigrationCreationProvider
}

type MigrationStage string

const (
	MigrationStageInspect  MigrationStage = "inspect"
	MigrationStageConfirm  MigrationStage = "confirm"
	MigrationStageSecret   MigrationStage = "secret-input"
	MigrationStageApplying MigrationStage = "applying"
	MigrationStageTerminal MigrationStage = "terminal"
	MigrationStageStale    MigrationStage = "stale"
	MigrationStageError    MigrationStage = "error"
)

type MigrationOptions struct {
	Context   context.Context
	Provider  MigrationActionProvider
	Operation manager.MigrationOperationProjection
	Mutable   bool
	Reason    string
}

type MigrationOutcome struct {
	Close      bool
	Projection *manager.MigrationOperationProjection
}

type Migration struct {
	ctx       context.Context
	provider  MigrationActionProvider
	operation manager.MigrationOperationProjection
	mutable   bool
	reason    string

	stage         MigrationStage
	action        MigrationAction
	retainPartial bool
	secretInput   []byte
	errorMessage  string
	requestID     uint64
}

type migrationActionMsg struct {
	requestID  uint64
	projection manager.MigrationOperationProjection
	err        error
}

const maxMigrationPassphraseBytes = 1024

func NewMigration(options MigrationOptions) *Migration {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return &Migration{
		ctx: ctx, provider: options.Provider,
		operation: cloneMigrationProjection(options.Operation),
		mutable:   options.Mutable, reason: safeInline(options.Reason),
		stage: MigrationStageInspect,
	}
}

func (editor *Migration) Update(message tea.Msg) (tea.Cmd, MigrationOutcome) {
	if editor == nil {
		return nil, MigrationOutcome{Close: true}
	}
	switch typed := message.(type) {
	case tea.KeyPressMsg:
		return editor.updateKey(typed)
	case migrationActionMsg:
		return editor.receiveAction(typed)
	default:
		return nil, MigrationOutcome{}
	}
}

func (editor *Migration) updateKey(message tea.KeyPressMsg) (tea.Cmd, MigrationOutcome) {
	key := message.String()
	if key == "esc" {
		editor.requestID++
		projection := cloneMigrationProjection(editor.operation)
		editor.Clear()
		return nil, MigrationOutcome{Close: true, Projection: &projection}
	}
	switch editor.stage {
	case MigrationStageInspect:
		switch key {
		case "enter":
			if editor.operation.Recovery.Required {
				return editor.prepareRecovery()
			}
		case "c":
			if editor.operation.TerminalReceipt == nil {
				editor.action = MigrationActionCancel
				editor.retainPartial = editor.operation.Kind == manager.MigrationOperationExport
				editor.stage = MigrationStageConfirm
			}
		}
	case MigrationStageConfirm:
		switch strings.ToLower(key) {
		case "y":
			if editor.action == MigrationActionResume {
				editor.stage = MigrationStageSecret
				return nil, MigrationOutcome{}
			}
			return editor.startAction(nil)
		case "n":
			editor.action = ""
			editor.stage = MigrationStageInspect
		case "t":
			if editor.action == MigrationActionCancel &&
				editor.operation.Kind == manager.MigrationOperationExport {
				editor.retainPartial = !editor.retainPartial
			}
		}
	case MigrationStageSecret:
		switch key {
		case "enter":
			return editor.startSecretAction()
		case "backspace":
			editor.removeSecretRune()
		default:
			editor.appendSecretInput(message)
		}
	case MigrationStageTerminal:
		if key == "enter" {
			projection := cloneMigrationProjection(editor.operation)
			editor.Clear()
			return nil, MigrationOutcome{Close: true, Projection: &projection}
		}
	case MigrationStageError:
		if key == "r" {
			editor.errorMessage = ""
			editor.action = ""
			editor.stage = MigrationStageInspect
		}
	}
	return nil, MigrationOutcome{}
}

func (editor *Migration) prepareRecovery() (tea.Cmd, MigrationOutcome) {
	if !editor.mutable || editor.provider == nil {
		editor.stage = MigrationStageStale
		if editor.reason == "" {
			editor.reason = "authenticated migration authority is unavailable"
		}
		return nil, MigrationOutcome{}
	}
	if len(editor.operation.Recovery.AllowedActions) != 1 {
		editor.stage = MigrationStageError
		editor.errorMessage = "Hideout did not advertise one exact recovery action."
		return nil, MigrationOutcome{}
	}
	switch editor.operation.Recovery.AllowedActions[0] {
	case manager.MigrationRecoveryResume:
		editor.action = MigrationActionResume
	case manager.MigrationRecoveryFinish,
		manager.MigrationRecoveryRollback,
		manager.MigrationRecoveryRemovePartial:
		editor.action = MigrationActionRecover
	case manager.MigrationRecoveryManual:
		editor.stage = MigrationStageStale
		editor.reason = editor.operation.Recovery.NextAction
		return nil, MigrationOutcome{}
	default:
		editor.stage = MigrationStageError
		editor.errorMessage = "The advertised recovery action is invalid."
		return nil, MigrationOutcome{}
	}
	editor.stage = MigrationStageConfirm
	return nil, MigrationOutcome{}
}

func (editor *Migration) startSecretAction() (tea.Cmd, MigrationOutcome) {
	if len(editor.secretInput) == 0 {
		editor.errorMessage = "Passphrase is required."
		return nil, MigrationOutcome{}
	}
	buffer, err := secrets.NewBuffer(editor.secretInput)
	editor.clearSecretInput()
	if err != nil {
		editor.errorMessage = "Passphrase is invalid or too long."
		return nil, MigrationOutcome{}
	}
	return editor.startAction(buffer)
}

func (editor *Migration) startAction(
	passphrase *secrets.Buffer,
) (tea.Cmd, MigrationOutcome) {
	if !editor.mutable || editor.provider == nil || editor.action == "" {
		if passphrase != nil {
			passphrase.Clear()
		}
		editor.stage = MigrationStageStale
		return nil, MigrationOutcome{}
	}
	editor.requestID++
	requestID := editor.requestID
	provider := editor.provider
	ctx := editor.ctx
	request := MigrationActionRequest{
		Operation: cloneMigrationProjection(editor.operation),
		Action:    editor.action, RetainPartial: editor.retainPartial,
	}
	editor.stage = MigrationStageApplying
	editor.errorMessage = ""
	return func() tea.Msg {
		var (
			projection manager.MigrationOperationProjection
			err        error
		)
		invoke := func(value []byte) error {
			request.Passphrase = value
			projection, err = provider.ApplyMigrationAction(ctx, request)
			request.Passphrase = nil
			return err
		}
		if passphrase == nil {
			err = invoke(nil)
		} else {
			err = passphrase.Use(invoke)
			passphrase.Clear()
		}
		return migrationActionMsg{
			requestID: requestID, projection: projection, err: err,
		}
	}, MigrationOutcome{}
}

func (editor *Migration) receiveAction(
	message migrationActionMsg,
) (tea.Cmd, MigrationOutcome) {
	if message.requestID != editor.requestID || editor.stage != MigrationStageApplying {
		return nil, MigrationOutcome{}
	}
	if message.err != nil || message.projection.Validate() != nil ||
		message.projection.OperationID != editor.operation.OperationID ||
		message.projection.Revision < editor.operation.Revision {
		editor.stage = MigrationStageError
		editor.errorMessage = migrationActionError(message.err)
		return nil, MigrationOutcome{}
	}
	editor.operation = cloneMigrationProjection(message.projection)
	editor.stage = MigrationStageTerminal
	editor.action = ""
	projection := cloneMigrationProjection(editor.operation)
	return nil, MigrationOutcome{Projection: &projection}
}

func (editor *Migration) SyncAuthority(
	mutable bool,
	operation manager.MigrationOperationProjection,
	reason string,
) {
	if editor == nil || operation.OperationID != editor.operation.OperationID ||
		operation.Validate() != nil {
		return
	}
	if operation.Revision < editor.operation.Revision {
		return
	}
	if operation.Revision != editor.operation.Revision &&
		(editor.stage == MigrationStageConfirm || editor.stage == MigrationStageSecret) {
		editor.stage = MigrationStageStale
		editor.action = ""
		editor.clearSecretInput()
	}
	editor.operation = cloneMigrationProjection(operation)
	editor.mutable = mutable
	editor.reason = safeInline(reason)
}

func (editor *Migration) View(width int) string {
	if editor == nil {
		return "Migration dialog unavailable.\n"
	}
	operation := editor.operation
	lines := []string{
		"Migration " + safeInline(operation.OperationID),
		strings.ToUpper(safeInline(string(operation.Kind))) + " · " +
			safeInline(operation.PhaseLabel),
		"Bundle " + safeInline(string(operation.BundleID)),
		"Progress " + modalMigrationProgress(operation.Progress),
	}
	switch editor.stage {
	case MigrationStageInspect:
		lines = append(lines, "", "Action")
		if operation.Recovery.Required {
			lines = append(
				lines,
				"  Required "+safeInline(operation.Recovery.Code),
				"  "+safeInline(operation.Recovery.NextAction),
				"",
				"Enter review this exact recovery action · Esc close",
			)
		} else if operation.TerminalReceipt != nil {
			lines = append(
				lines,
				"  Terminal "+safeInline(operation.TerminalReceipt.ResultCode),
				"  No action is pending.",
				"",
				"Esc close",
			)
		} else {
			lines = append(
				lines,
				"  No recovery action is required.",
				"  c requests cancellation at the next safe boundary.",
				"",
				"c review cancellation · Esc close",
			)
		}
	case MigrationStageConfirm:
		lines = append(lines, "", "Confirm "+strings.ToUpper(string(editor.action)))
		if operation.Recovery.Required && operation.Recovery.NextAction != "" {
			lines = append(lines, "Recovery "+safeInline(operation.Recovery.NextAction))
		}
		if editor.action == MigrationActionCancel &&
			operation.Kind == manager.MigrationOperationExport {
			choice := "remove unsealed partial output"
			if editor.retainPartial {
				choice = "retain unsealed partial output for review"
			}
			lines = append(lines, "Partial output: "+choice, "t toggles retain/remove")
		}
		lines = append(
			lines,
			"This request is bound to revision "+fmt.Sprint(operation.Revision)+".",
			"Continue? [y/N]",
		)
	case MigrationStageSecret:
		masked := ""
		if len(editor.secretInput) != 0 {
			masked = "••••••••"
		}
		lines = append(
			lines,
			"", "Unlock the same encrypted bundle",
			"The passphrase stays in memory only until a one-shot Manager handle is created.",
			"> "+masked,
			"", "Enter resume · Esc clears and closes",
		)
	case MigrationStageApplying:
		lines = append(
			lines,
			"", "Sending the exact revision-bound action…",
			"Closing this dialog does not claim that the operation was cancelled.",
		)
	case MigrationStageTerminal:
		lines = append(
			lines,
			"", "Action accepted by Hideout.",
			"Current phase "+safeInline(operation.PhaseLabel),
			"Enter close · progress continues in the Migration HUD",
		)
	case MigrationStageStale:
		reason := editor.reason
		if reason == "" {
			reason = "verified migration state changed or mutation authority is unavailable"
		}
		lines = append(
			lines,
			"", "STALE · read-only", safeInline(reason),
			"Refresh the authoritative snapshot before acting.", "Esc close",
		)
	case MigrationStageError:
		lines = append(
			lines,
			"", "Migration action was not accepted.", safeInline(editor.errorMessage),
			"r return to the refreshed operation · Esc close",
		)
	}
	if editor.errorMessage != "" && editor.stage == MigrationStageSecret {
		lines = append(lines, "", "ERROR "+safeInline(editor.errorMessage))
	}
	return fitLines(strings.Join(lines, "\n")+"\n", width)
}

func (editor *Migration) appendSecretInput(message tea.KeyPressMsg) {
	text := message.Key().Text
	if text == "" || len(editor.secretInput) >= maxMigrationPassphraseBytes {
		return
	}
	for _, character := range text {
		if unicode.IsControl(character) || isBidiControl(character) {
			continue
		}
		encoded := []byte(string(character))
		if len(editor.secretInput)+len(encoded) > maxMigrationPassphraseBytes {
			clear(encoded)
			break
		}
		editor.secretInput = append(editor.secretInput, encoded...)
		clear(encoded)
	}
	editor.errorMessage = ""
}

func (editor *Migration) removeSecretRune() {
	if len(editor.secretInput) == 0 {
		return
	}
	_, size := utf8.DecodeLastRune(editor.secretInput)
	if size <= 0 {
		size = 1
	}
	clear(editor.secretInput[len(editor.secretInput)-size:])
	editor.secretInput = editor.secretInput[:len(editor.secretInput)-size]
	editor.errorMessage = ""
}

func (editor *Migration) clearSecretInput() {
	clear(editor.secretInput)
	editor.secretInput = nil
}

func (editor *Migration) Clear() {
	if editor == nil {
		return
	}
	editor.clearSecretInput()
	editor.action = ""
	editor.errorMessage = ""
}

func (editor *Migration) Stage() MigrationStage {
	if editor == nil {
		return ""
	}
	return editor.stage
}

func (editor *Migration) OperationID() string {
	if editor == nil {
		return ""
	}
	return editor.operation.OperationID
}

func modalMigrationProgress(progress manager.MigrationProgressProjection) string {
	completed := fmt.Sprintf("%d bytes", progress.CompletedLogicalBytes)
	if progress.LogicalTotalKnown {
		completed += fmt.Sprintf(" / %d bytes", progress.TotalLogicalBytes)
	} else {
		completed += " / total unknown"
	}
	eta := "unknown"
	if progress.RemainingKnown {
		eta = fmt.Sprintf("%d seconds", progress.RemainingSeconds)
	}
	return completed + " · elapsed " + fmt.Sprint(progress.ElapsedSeconds) +
		" seconds · ETA " + eta
}

func migrationActionError(err error) string {
	switch {
	case err == nil:
		return "Hideout returned an invalid migration action result."
	case errors.Is(err, context.Canceled):
		return "The local request was cancelled; refresh to learn whether Hideout accepted it."
	case errors.Is(err, context.DeadlineExceeded):
		return "The local request timed out; refresh before retrying the exact operation."
	default:
		return "Refresh the authoritative operation and follow its current recovery action."
	}
}

func cloneMigrationProjection(
	value manager.MigrationOperationProjection,
) manager.MigrationOperationProjection {
	cloned := value
	cloned.Recovery.AllowedActions = append(
		[]manager.MigrationRecoveryAction(nil),
		value.Recovery.AllowedActions...,
	)
	cloned.Warnings = append([]manager.MigrationNotice(nil), value.Warnings...)
	cloned.Effects = append(
		[]manager.MigrationEffectProjection(nil), value.Effects...,
	)
	if value.TerminalReceipt != nil {
		receipt := *value.TerminalReceipt
		receipt.Replacements = append(
			[]manager.MigrationReplacedEnvironment(nil), value.TerminalReceipt.Replacements...,
		)
		receipt.ApprovedAuthority = append(
			[]manager.MigrationApprovedAuthority(nil), value.TerminalReceipt.ApprovedAuthority...,
		)
		receipt.DisabledAuthorityProposalIDs = append(
			receipt.DisabledAuthorityProposalIDs[:0:0],
			value.TerminalReceipt.DisabledAuthorityProposalIDs...,
		)
		cloned.TerminalReceipt = &receipt
	}
	return cloned
}
