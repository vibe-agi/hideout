package modal

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	// EditorSecret is the Manager-owned secret lifecycle editor. It is separate
	// from profile typed changes because secret bytes never enter a profile
	// draft or configuration plan.
	EditorSecret = "secret.manage"

	maxConfigInputRunes = 16 << 10
)

type Stage string

const (
	StageEditing     Stage = "editing-draft"
	StagePlanning    Stage = "planning"
	StageReview      Stage = "review"
	StageConfirming  Stage = "confirming"
	StageSecretInput Stage = "secret-input"
	StageApplying    Stage = "applying"
	StageTerminal    Stage = "terminal"
	StageStale       Stage = "stale"
	StageError       Stage = "error"
)

// Provider is the sole authority boundary used by the TUI. Implementations
// must call the authenticated Manager plan/apply routes; the modal itself
// never writes profiles, Keychain entries, VM state, or operation records.
type Provider interface {
	PlanConfiguration(
		context.Context,
		manager.ConfigurationDraft,
	) (manager.ConfigurationPlan, error)
	ApplyConfiguration(
		context.Context,
		manager.ConfigurationApplyRequest,
	) (manager.ConfigurationApplyResult, error)
	PlanSecret(
		context.Context,
		manager.SecretDraft,
	) (manager.SecretPlan, error)
	ApplySecret(
		context.Context,
		manager.SecretApplyRequest,
	) (manager.SecretApplyResult, error)
}

type ConfigOptions struct {
	Context    context.Context
	Provider   Provider
	Projection manager.ProfileProjection
	EditorID   string
	Mutable    bool
	Now        func() time.Time
}

type Outcome struct {
	Close      bool
	Projection *manager.ProfileProjection
	Operation  *manager.Operation
}

type Config struct {
	ctx        context.Context
	provider   Provider
	projection manager.ProfileProjection
	editorID   string
	mutable    bool
	now        func() time.Time

	stage            Stage
	stageBeforeStale Stage
	input            []rune
	secretInput      []byte
	pendingSecret    *secrets.Buffer
	authorityReason  string
	errorMessage     string
	requestID        uint64

	profilePlan  *manager.ConfigurationPlan
	secretPlan   *manager.SecretPlan
	secretDraft  *manager.SecretDraft
	operation    *manager.Operation
	responseLost bool
}

type configurationPlanMsg struct {
	requestID uint64
	plan      manager.ConfigurationPlan
	err       error
}

type secretPlanMsg struct {
	requestID uint64
	plan      manager.SecretPlan
	err       error
}

type configurationApplyMsg struct {
	requestID uint64
	result    manager.ConfigurationApplyResult
	err       error
}

type secretApplyMsg struct {
	requestID uint64
	result    manager.SecretApplyResult
	err       error
}

func NewConfig(options ConfigOptions) *Config {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	editor := &Config{
		ctx: options.Context, provider: options.Provider,
		projection: options.Projection, editorID: options.EditorID,
		mutable: options.Mutable, now: now, stage: StageEditing,
	}
	editor.ctx = ctx
	if !validEditorID(editor.editorID) ||
		editor.projection.Profile == "" ||
		editor.projection.Revision == 0 {
		editor.stage = StageError
		editor.errorMessage = "This configuration field is unavailable."
		return editor
	}
	if !options.Mutable || options.Provider == nil {
		editor.stage = StageStale
		editor.authorityReason = "authenticated mutation authority is unavailable"
	}
	return editor
}

func (editor *Config) Stage() Stage {
	if editor == nil {
		return StageError
	}
	return editor.stage
}

func (editor *Config) OperationID() string {
	if editor == nil {
		return ""
	}
	if editor.operation != nil {
		return editor.operation.ID
	}
	if editor.profilePlan != nil {
		return editor.profilePlan.OperationID
	}
	if editor.secretPlan != nil {
		return editor.secretPlan.OperationID
	}
	return ""
}

func (editor *Config) ResponseLost() bool {
	return editor != nil && editor.responseLost
}

func (editor *Config) SecretRetained() bool {
	return editor != nil &&
		(len(editor.secretInput) != 0 || editor.pendingSecret != nil)
}

func (editor *Config) InputLength() int {
	if editor == nil {
		return 0
	}
	if editor.stage == StageSecretInput {
		return utf8.RuneCount(editor.secretInput)
	}
	return len(editor.input)
}

// Clear removes all client-local sensitive material. It is safe to call more
// than once, including during Ctrl+C and after an apply command completes.
func (editor *Config) Clear() {
	if editor == nil {
		return
	}
	clear(editor.secretInput)
	editor.secretInput = nil
	if editor.pendingSecret != nil {
		editor.pendingSecret.Clear()
		editor.pendingSecret = nil
	}
	clear(editor.input)
	editor.input = nil
	editor.secretDraft = nil
}

// SyncAuthority is called after every authoritative projection update. A
// changed revision/digest invalidates an unsubmitted draft or reviewed plan.
// An already accepted apply is not cancelled: its operation ID remains the
// recovery handle.
func (editor *Config) SyncAuthority(
	mutable bool,
	projection manager.ProfileProjection,
	reason string,
) {
	if editor == nil {
		return
	}
	sameBase := projection.Profile == editor.projection.Profile &&
		projection.Revision == editor.projection.Revision &&
		projection.ContentDigest == editor.projection.ContentDigest
	editor.mutable = mutable
	editor.authorityReason = safeInline(reason)
	if editor.authorityReason == "" && !mutable {
		editor.authorityReason = "authenticated mutation authority is unavailable"
	}
	if editor.stage == StageApplying || editor.stage == StageTerminal {
		return
	}
	if !mutable || !sameBase {
		if editor.stage != StageStale {
			editor.stageBeforeStale = editor.stage
		}
		editor.requestID++
		editor.stage = StageStale
		if !sameBase {
			editor.authorityReason =
				"profile revision changed; discard this draft and review a fresh projection"
		}
		return
	}
	if editor.stage == StageStale &&
		(editor.stageBeforeStale == StageEditing ||
			editor.stageBeforeStale == "") {
		editor.stage = StageEditing
		editor.authorityReason = ""
	}
}

// ObserveOperation resolves an unknown apply outcome from the durable
// operation projection. It never creates a replacement operation.
func (editor *Config) ObserveOperation(operation manager.Operation) {
	if editor == nil || editor.OperationID() == "" ||
		operation.ID != editor.OperationID() ||
		operation.Validate() != nil {
		return
	}
	var digest string
	if editor.profilePlan != nil {
		digest = editor.profilePlan.PlanDigest
	}
	if editor.secretPlan != nil {
		digest = editor.secretPlan.PlanDigest
	}
	if operation.PlanDigest != digest {
		return
	}
	copied := operation
	editor.operation = &copied
	editor.stage = StageTerminal
	editor.responseLost = !operation.Terminal()
	if operation.Terminal() {
		editor.errorMessage = ""
	}
}

func (editor *Config) Update(message tea.Msg) (tea.Cmd, Outcome) {
	if editor == nil {
		return nil, Outcome{Close: true}
	}
	switch typed := message.(type) {
	case configurationPlanMsg:
		return editor.receiveConfigurationPlan(typed)
	case secretPlanMsg:
		return editor.receiveSecretPlan(typed)
	case configurationApplyMsg:
		return editor.receiveConfigurationApply(typed)
	case secretApplyMsg:
		return editor.receiveSecretApply(typed)
	case tea.KeyPressMsg:
		return editor.updateKey(typed)
	default:
		return nil, Outcome{}
	}
}

func (editor *Config) updateKey(
	message tea.KeyPressMsg,
) (tea.Cmd, Outcome) {
	key := message.String()
	if key == "esc" {
		switch editor.stage {
		case StageApplying:
			// Closing an accepted apply never means cancellation.
			editor.Clear()
			return nil, Outcome{
				Close:     true,
				Operation: cloneOperation(editor.operation),
			}
		default:
			editor.requestID++
			editor.Clear()
			return nil, Outcome{Close: true}
		}
	}
	switch editor.stage {
	case StageEditing:
		switch key {
		case "enter":
			return editor.startPlan()
		case "backspace":
			editor.removeInputRune()
		default:
			editor.appendInput(message)
		}
	case StageReview:
		switch key {
		case "a":
			if editor.planBlockedOrExpired() {
				return nil, Outcome{}
			}
			editor.clearOrdinaryInput()
			editor.stage = StageConfirming
			editor.errorMessage = ""
		case "e":
			editor.profilePlan = nil
			editor.secretPlan = nil
			editor.secretDraft = nil
			editor.operation = nil
			editor.responseLost = false
			editor.clearOrdinaryInput()
			editor.stage = StageEditing
		}
	case StageConfirming:
		if editor.highRisk() {
			switch key {
			case "enter":
				if string(editor.input) != editor.confirmationPhrase() {
					editor.errorMessage = "Typed confirmation does not match."
					return nil, Outcome{}
				}
				editor.clearOrdinaryInput()
				return editor.afterConfirmation()
			case "backspace":
				editor.removeInputRune()
			default:
				editor.appendInput(message)
			}
			return nil, Outcome{}
		}
		switch strings.ToLower(key) {
		case "y":
			return editor.afterConfirmation()
		case "n":
			editor.stage = StageReview
		}
	case StageSecretInput:
		switch key {
		case "enter":
			return editor.startSecretApply()
		case "backspace":
			editor.removeSecretRune()
		default:
			editor.appendSecretInput(message)
		}
	case StageError:
		if key == "e" && editor.mutable {
			editor.errorMessage = ""
			editor.clearOrdinaryInput()
			editor.stage = StageEditing
		}
	case StageTerminal:
		if key == "enter" {
			editor.Clear()
			return nil, Outcome{
				Close:     true,
				Operation: cloneOperation(editor.operation),
			}
		}
	}
	return nil, Outcome{}
}

func (editor *Config) startPlan() (tea.Cmd, Outcome) {
	if !editor.mutable || editor.provider == nil {
		editor.stageBeforeStale = StageEditing
		editor.stage = StageStale
		if editor.authorityReason == "" {
			editor.authorityReason =
				"authenticated mutation authority is unavailable"
		}
		return nil, Outcome{}
	}
	editor.errorMessage = ""
	editor.requestID++
	requestID := editor.requestID
	provider := editor.provider
	ctx := editor.ctx
	if editor.editorID == EditorSecret {
		draft, err := parseSecretDraft(string(editor.input))
		if err != nil {
			editor.errorMessage = safeInline(err.Error())
			return nil, Outcome{}
		}
		editor.clearOrdinaryInput()
		editor.secretDraft = &draft
		editor.stage = StagePlanning
		return func() tea.Msg {
			plan, err := provider.PlanSecret(ctx, draft)
			return secretPlanMsg{
				requestID: requestID,
				plan:      plan,
				err:       err,
			}
		}, Outcome{}
	}
	change, err := parseTypedChange(editor.editorID, string(editor.input))
	if err != nil {
		editor.errorMessage = safeInline(err.Error())
		return nil, Outcome{}
	}
	draft := manager.ConfigurationDraft{
		Schema:       manager.ConfigurationDraftSchema,
		Profile:      editor.projection.Profile,
		BaseRevision: editor.projection.Revision,
		ClientNonce: fmt.Sprintf(
			"tui-%d-%d",
			editor.projection.Revision,
			requestID,
		),
		Changes: []manager.TypedChange{change},
	}
	editor.clearOrdinaryInput()
	editor.stage = StagePlanning
	return func() tea.Msg {
		plan, err := provider.PlanConfiguration(ctx, draft)
		return configurationPlanMsg{
			requestID: requestID,
			plan:      plan,
			err:       err,
		}
	}, Outcome{}
}

func (editor *Config) receiveConfigurationPlan(
	message configurationPlanMsg,
) (tea.Cmd, Outcome) {
	if message.requestID != editor.requestID ||
		editor.stage != StagePlanning {
		return nil, Outcome{}
	}
	if message.err != nil {
		if errors.Is(message.err, manager.ErrStaleConfigurationPlan) ||
			errors.Is(message.err, manager.ErrStaleProfileRevision) {
			editor.stageBeforeStale = StagePlanning
			editor.stage = StageStale
			editor.authorityReason =
				"Manager rejected the stale profile revision; refresh before reviewing"
			return nil, Outcome{}
		}
		editor.stage = StageError
		editor.errorMessage = safeAuthorityError(
			"Manager could not create a configuration plan",
			message.err,
		)
		return nil, Outcome{}
	}
	plan := message.plan
	if plan.VerifyDigest() != nil ||
		plan.Profile != editor.projection.Profile ||
		plan.BaseRevision != editor.projection.Revision ||
		plan.BaseDigest != editor.projection.ContentDigest ||
		len(plan.CanonicalChanges) != 1 ||
		plan.CanonicalChanges[0].Kind != editor.editorID {
		editor.stage = StageError
		editor.errorMessage =
			"Manager returned a plan that does not match this exact draft."
		return nil, Outcome{}
	}
	editor.profilePlan = &plan
	editor.stage = StageReview
	return nil, Outcome{}
}

func (editor *Config) receiveSecretPlan(
	message secretPlanMsg,
) (tea.Cmd, Outcome) {
	if message.requestID != editor.requestID ||
		editor.stage != StagePlanning {
		return nil, Outcome{}
	}
	if message.err != nil {
		if errors.Is(message.err, manager.ErrStaleSecretPlan) {
			editor.stageBeforeStale = StagePlanning
			editor.stage = StageStale
			editor.authorityReason =
				"Manager rejected stale secret metadata; refresh before reviewing"
			return nil, Outcome{}
		}
		editor.stage = StageError
		editor.errorMessage = safeAuthorityError(
			"Manager could not create a secret plan",
			message.err,
		)
		return nil, Outcome{}
	}
	plan := message.plan
	if plan.VerifyDigest() != nil ||
		editor.secretDraft == nil ||
		plan.Ref != editor.secretDraft.Ref ||
		plan.Action != editor.secretDraft.Action {
		editor.stage = StageError
		editor.errorMessage =
			"Manager returned an invalid secret plan."
		return nil, Outcome{}
	}
	editor.secretPlan = &plan
	editor.stage = StageReview
	return nil, Outcome{}
}

func (editor *Config) afterConfirmation() (tea.Cmd, Outcome) {
	if editor.planBlockedOrExpired() {
		editor.stage = StageReview
		return nil, Outcome{}
	}
	if editor.secretPlan != nil &&
		editor.secretPlan.Action != secrets.ActionDelete {
		editor.clearSecretInput()
		editor.stage = StageSecretInput
		return nil, Outcome{}
	}
	if editor.secretPlan != nil {
		return editor.startSecretApply()
	}
	return editor.startConfigurationApply()
}

func (editor *Config) startConfigurationApply() (tea.Cmd, Outcome) {
	if editor.profilePlan == nil || !editor.mutable ||
		editor.provider == nil {
		editor.stage = StageStale
		editor.authorityReason =
			"the reviewed configuration plan can no longer be applied"
		return nil, Outcome{}
	}
	plan := *editor.profilePlan
	editor.requestID++
	requestID := editor.requestID
	editor.stage = StageApplying
	provider := editor.provider
	ctx := editor.ctx
	request := manager.ConfigurationApplyRequest{
		Schema:       manager.ConfigurationApplySchema,
		OperationID:  plan.OperationID,
		Profile:      plan.Profile,
		BaseRevision: plan.BaseRevision,
		PlanDigest:   plan.PlanDigest,
		Confirmed:    true,
	}
	return func() tea.Msg {
		result, err := provider.ApplyConfiguration(ctx, request)
		return configurationApplyMsg{
			requestID: requestID,
			result:    result,
			err:       err,
		}
	}, Outcome{}
}

func (editor *Config) startSecretApply() (tea.Cmd, Outcome) {
	if editor.secretPlan == nil || !editor.mutable ||
		editor.provider == nil {
		editor.clearSecretInput()
		editor.stage = StageStale
		editor.authorityReason =
			"the reviewed secret plan can no longer be applied"
		return nil, Outcome{}
	}
	plan := *editor.secretPlan
	var buffer *secrets.Buffer
	if plan.Action != secrets.ActionDelete {
		if len(editor.secretInput) == 0 ||
			!utf8.Valid(editor.secretInput) {
			editor.errorMessage =
				"Secret value must be non-empty valid UTF-8 text."
			return nil, Outcome{}
		}
		var err error
		buffer, err = secrets.NewBuffer(editor.secretInput)
		if err != nil {
			editor.errorMessage =
				"Secret value must be non-empty and bounded."
			return nil, Outcome{}
		}
	}
	editor.clearSecretInput()
	editor.pendingSecret = buffer
	editor.requestID++
	requestID := editor.requestID
	editor.stage = StageApplying
	provider := editor.provider
	ctx := editor.ctx
	request := manager.SecretApplyRequest{
		Schema:      manager.SecretApplySchema,
		OperationID: plan.OperationID,
		PlanDigest:  plan.PlanDigest,
		Ref:         plan.Ref,
		Action:      plan.Action,
		Confirmed:   true,
		Value:       buffer,
	}
	return func() tea.Msg {
		if buffer != nil {
			defer buffer.Clear()
		}
		result, err := provider.ApplySecret(ctx, request)
		return secretApplyMsg{
			requestID: requestID,
			result:    result,
			err:       err,
		}
	}, Outcome{}
}

func (editor *Config) receiveConfigurationApply(
	message configurationApplyMsg,
) (tea.Cmd, Outcome) {
	if message.requestID != editor.requestID ||
		editor.stage != StageApplying {
		return nil, Outcome{}
	}
	if message.err != nil {
		editor.finishResponseLoss(message.err)
		return nil, Outcome{}
	}
	plan := editor.profilePlan
	result := message.result
	if plan == nil ||
		result.Operation.Validate() != nil ||
		result.Operation.ID != plan.OperationID ||
		result.Operation.PlanDigest != plan.PlanDigest ||
		result.Operation.Owner != (manager.OperationOwner{
			Kind: "profile",
			ID:   plan.Profile,
		}) ||
		result.Projection.Profile != plan.Profile ||
		result.Projection.Revision <= plan.BaseRevision {
		editor.finishResponseLoss(
			errors.New("Manager returned a mismatched apply result"),
		)
		return nil, Outcome{}
	}
	operation := result.Operation
	projection := result.Projection
	editor.operation = &operation
	editor.projection = projection
	editor.responseLost = false
	editor.stage = StageTerminal
	editor.errorMessage = ""
	return nil, Outcome{
		Projection: &projection,
		Operation:  &operation,
	}
}

func (editor *Config) receiveSecretApply(
	message secretApplyMsg,
) (tea.Cmd, Outcome) {
	if message.requestID != editor.requestID ||
		editor.stage != StageApplying {
		return nil, Outcome{}
	}
	if editor.pendingSecret != nil {
		editor.pendingSecret.Clear()
		editor.pendingSecret = nil
	}
	if message.err != nil {
		editor.finishResponseLoss(message.err)
		return nil, Outcome{}
	}
	plan := editor.secretPlan
	result := message.result
	if plan == nil ||
		result.Operation.Validate() != nil ||
		result.Operation.ID != plan.OperationID ||
		result.Operation.PlanDigest != plan.PlanDigest ||
		result.Operation.Owner != (manager.OperationOwner{
			Kind: "secret",
			ID:   plan.Ref,
		}) ||
		result.Reference.Validate() != nil ||
		result.Reference.Ref != plan.Ref {
		editor.finishResponseLoss(
			errors.New("Manager returned a mismatched secret result"),
		)
		return nil, Outcome{}
	}
	operation := result.Operation
	editor.operation = &operation
	editor.responseLost = false
	editor.stage = StageTerminal
	editor.errorMessage = ""
	return nil, Outcome{Operation: &operation}
}

func (editor *Config) finishResponseLoss(err error) {
	if editor.pendingSecret != nil {
		editor.pendingSecret.Clear()
		editor.pendingSecret = nil
	}
	editor.stage = StageTerminal
	editor.responseLost = true
	editor.errorMessage = safeAuthorityError(
		"Apply response was not authoritative",
		err,
	)
}

func (editor *Config) planBlockedOrExpired() bool {
	if editor == nil || !editor.mutable {
		return true
	}
	if editor.profilePlan != nil {
		if len(editor.profilePlan.Blockers) != 0 ||
			!editor.now().Before(editor.profilePlan.ExpiresAt) {
			return true
		}
	}
	if editor.secretPlan != nil {
		if len(editor.secretPlan.Blockers) != 0 ||
			!editor.now().Before(editor.secretPlan.ExpiresAt) {
			return true
		}
	}
	return editor.profilePlan == nil && editor.secretPlan == nil
}

func (editor *Config) highRisk() bool {
	if editor == nil {
		return false
	}
	if editor.secretPlan != nil {
		return editor.secretPlan.Action == secrets.ActionDelete
	}
	if editor.profilePlan == nil {
		return false
	}
	for _, warning := range editor.profilePlan.Warnings {
		code := strings.ToLower(warning.Code)
		if strings.Contains(code, "authority-expanded") ||
			strings.Contains(code, "root-sensitive") ||
			strings.Contains(code, "destructive") {
			return true
		}
	}
	for _, effect := range editor.profilePlan.Effects {
		if effect.Kind == "cleanup" {
			return true
		}
	}
	return false
}

func (editor *Config) confirmationPhrase() string {
	if editor.secretPlan != nil {
		return editor.secretPlan.Ref
	}
	return editor.projection.Profile
}

func (editor *Config) appendInput(message tea.KeyPressMsg) {
	text := message.Key().Text
	if text == "" || len(editor.input) >= maxConfigInputRunes {
		return
	}
	for _, character := range text {
		if unicode.IsControl(character) ||
			isBidiControl(character) ||
			len(editor.input) >= maxConfigInputRunes {
			continue
		}
		editor.input = append(editor.input, character)
	}
	editor.errorMessage = ""
}

func (editor *Config) appendSecretInput(message tea.KeyPressMsg) {
	text := message.Key().Text
	if text == "" ||
		int64(len(editor.secretInput)) >= manager.SecretRequestBodyLimit {
		return
	}
	for _, character := range text {
		if unicode.IsControl(character) ||
			isBidiControl(character) {
			continue
		}
		encoded := []byte(string(character))
		if int64(len(editor.secretInput)+len(encoded)) >
			manager.SecretRequestBodyLimit {
			break
		}
		editor.secretInput = append(editor.secretInput, encoded...)
		clear(encoded)
	}
	editor.errorMessage = ""
}

func (editor *Config) removeInputRune() {
	if len(editor.input) == 0 {
		return
	}
	editor.input[len(editor.input)-1] = 0
	editor.input = editor.input[:len(editor.input)-1]
	editor.errorMessage = ""
}

func (editor *Config) removeSecretRune() {
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

func (editor *Config) clearOrdinaryInput() {
	clear(editor.input)
	editor.input = nil
}

func (editor *Config) clearSecretInput() {
	clear(editor.secretInput)
	editor.secretInput = nil
}

func (editor *Config) View(width int) string {
	if editor == nil {
		return "Configuration editor unavailable.\n"
	}
	var lines []string
	switch editor.stage {
	case StageEditing:
		lines = append(
			lines,
			"Edit "+editorLabel(editor.editorID),
			"Draft only · no change has been planned or applied",
			"",
			editorPrompt(editor.editorID),
			"> "+safeInline(string(editor.input)),
			"",
			"Enter review with Manager · Esc cancel",
		)
	case StagePlanning:
		lines = append(
			lines,
			"Planning "+editorLabel(editor.editorID),
			"Manager is validating the exact draft…",
			"Esc closes this client; no configuration has been applied.",
		)
	case StageReview:
		lines = editor.reviewLines()
	case StageConfirming:
		if editor.highRisk() {
			lines = append(
				lines,
				"Confirm high-risk change",
				`To continue, type "`+safeInline(editor.confirmationPhrase())+`":`,
				"> "+safeInline(string(editor.input)),
				"",
				"Enter confirm · Esc cancel",
			)
		} else {
			lines = append(
				lines,
				"Confirm reviewed change",
				"Apply this exact operation? [y/N]",
				"y confirm · n back · Esc cancel",
			)
		}
	case StageSecretInput:
		masked := ""
		if len(editor.secretInput) != 0 {
			masked = "••••••••"
		}
		lines = append(
			lines,
			"Secret value",
			"Input is hidden and never appears in review or output.",
			"> "+masked,
			"",
			"Enter apply exact reviewed operation · Esc clears and cancels",
		)
	case StageApplying:
		lines = append(
			lines,
			"Applying reviewed operation",
			"Operation "+safeInline(editor.OperationID()),
			"Waiting for Manager terminal evidence…",
			"Esc closes this dialog but does not cancel the operation.",
		)
	case StageTerminal:
		lines = editor.terminalLines()
	case StageStale:
		reason := editor.authorityReason
		if reason == "" {
			reason = "authoritative state changed"
		}
		lines = append(
			lines,
			"STALE · read-only",
			safeInline(reason),
			"This draft cannot be planned or applied.",
			"Esc discard",
		)
	case StageError:
		lines = append(
			lines,
			"Configuration plan unavailable",
			safeInline(editor.errorMessage),
			"e edit a fresh draft · Esc cancel",
		)
	}
	if editor.errorMessage != "" &&
		editor.stage != StageError &&
		editor.stage != StageTerminal {
		lines = append(lines, "", "ERROR "+safeInline(editor.errorMessage))
	}
	return fitLines(strings.Join(lines, "\n")+"\n", width)
}

func (editor *Config) reviewLines() []string {
	lines := []string{"Review configuration change"}
	if editor.profilePlan != nil {
		plan := editor.profilePlan
		lines = append(
			lines,
			"Operation "+safeInline(plan.OperationID)+
				" · profile "+safeInline(plan.Profile)+
				fmt.Sprintf(" · revision %d", plan.BaseRevision),
			"Expires "+plan.ExpiresAt.UTC().Format(time.RFC3339),
			"",
			"Before → After · Scope",
		)
		for _, diff := range plan.Diff {
			lines = append(
				lines,
				safeInline(diff.Field)+": "+
					safeInline(diff.Before)+" → "+
					safeInline(diff.After)+" · "+
					safeInline(diff.Scope),
			)
		}
		lines = append(lines, "", "Effects")
		for _, effect := range plan.Effects {
			live := "deferred"
			if effect.Live {
				live = "live"
			}
			lines = append(
				lines,
				safeInline(effect.Kind)+" · "+
					safeInline(effect.Scope)+" · "+live+" · "+
					safeInline(effect.Summary),
			)
		}
		lines = appendReviewWarningsAndBlockers(
			lines,
			plan.Warnings,
			plan.Blockers,
		)
		lines = append(
			lines,
			"",
			"Rollback "+safeInline(plan.Rollback.Mode)+
				" · "+safeInline(plan.Rollback.Summary),
		)
	} else if editor.secretPlan != nil {
		plan := editor.secretPlan
		lines = append(
			lines,
			"Operation "+safeInline(plan.OperationID)+
				" · secret "+safeInline(plan.Ref),
			"Expires "+plan.ExpiresAt.UTC().Format(time.RFC3339),
			"",
			"Before "+safeInline(plan.Current.Availability)+
				fmt.Sprintf(" generation %d", plan.BaseGeneration),
			"After "+safeInline(plan.NextAvailability)+
				fmt.Sprintf(" generation %d", plan.NextGeneration),
			"Value hidden · never present in this plan",
			"",
			"Effects",
		)
		for _, effect := range plan.Effects {
			lines = append(
				lines,
				safeInline(effect.Kind)+" · "+
					safeInline(effect.Scope)+" · "+
					safeInline(effect.Summary),
			)
		}
		lines = appendReviewWarningsAndBlockers(
			lines,
			plan.Warnings,
			plan.Blockers,
		)
		lines = append(
			lines,
			"",
			"Rollback "+safeInline(plan.Rollback.Mode)+
				" · "+safeInline(plan.Rollback.Summary),
		)
	}
	if editor.planBlockedOrExpired() {
		lines = append(
			lines,
			"",
			"APPLY DISABLED · resolve blockers, refresh, and review a new plan",
			"e edit · Esc cancel",
		)
	} else {
		lines = append(
			lines,
			"",
			"a choose Apply · Enter only inspects · e edit · Esc cancel",
		)
	}
	return lines
}

func appendReviewWarningsAndBlockers(
	lines []string,
	warnings []manager.Warning,
	blockers []manager.Blocker,
) []string {
	if len(warnings) != 0 {
		lines = append(lines, "", "Warnings")
		for _, warning := range warnings {
			lines = append(
				lines,
				safeInline(warning.Code)+" · "+
					safeInline(warning.Summary),
			)
		}
	}
	if len(blockers) != 0 {
		lines = append(lines, "", "Blockers")
		for _, blocker := range blockers {
			lines = append(
				lines,
				safeInline(blocker.Code)+" · "+
					safeInline(blocker.Summary)+
					" · recovery "+safeInline(blocker.Recovery),
			)
		}
	}
	return lines
}

func (editor *Config) terminalLines() []string {
	id := safeInline(editor.OperationID())
	if editor.responseLost {
		phase := "OUTCOME UNKNOWN"
		if editor.operation != nil {
			phase = strings.ToUpper(safeInline(editor.operation.Phase))
		}
		return []string{
			phase + " · response lost",
			"Operation " + id,
			safeInline(editor.errorMessage),
			"Inspect this exact ID in Operations; do not create a new plan or apply again.",
			"Enter/Esc close",
		}
	}
	if editor.operation == nil {
		return []string{
			"OUTCOME UNKNOWN",
			"Operation " + id,
			"Inspect Operations for authoritative evidence.",
			"Enter/Esc close",
		}
	}
	lines := []string{
		strings.ToUpper(safeInline(editor.operation.Phase)),
		"Operation " + id,
	}
	if editor.operation.Result != nil {
		lines = append(
			lines,
			safeInline(editor.operation.Result.Summary),
		)
	}
	lines = append(
		lines,
		"Recovery "+safeInline(editor.operation.Recovery.Summary),
		"Enter/Esc close",
	)
	return lines
}

func parseTypedChange(
	kind string,
	input string,
) (manager.TypedChange, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return manager.TypedChange{}, errors.New("Enter a proposed value.")
	}
	var value any
	switch kind {
	case manager.ChangeNetworkPosture:
		value = map[string]any{"mode": input}
	case manager.ChangeNetworkProxyRef:
		value = map[string]any{"ref": input}
	case manager.ChangeNetworkDNS:
		fields := strings.Fields(input)
		switch len(fields) {
		case 1:
			value = map[string]any{"mode": fields[0]}
		case 2:
			value = map[string]any{
				"mode":     fields[0],
				"serverIp": fields[1],
			}
		default:
			return manager.TypedChange{}, errors.New(
				"Use: system, ip <address>, or doh <address>.",
			)
		}
	case manager.ChangeProfileEnvironment:
		parsed, err := parseEnvironmentChange(input)
		if err != nil {
			return manager.TypedChange{}, err
		}
		value = parsed
	case manager.ChangeProfileHostFS:
		parsed, err := parseHostFSChange(input)
		if err != nil {
			return manager.TypedChange{}, err
		}
		value = parsed
	case manager.ChangeProfileCommandProxy:
		fields := strings.Fields(input)
		if len(fields) != 2 {
			return manager.TypedChange{}, errors.New(
				"Use: add-open <command> or remove <command>.",
			)
		}
		value = map[string]any{
			"operation": fields[0],
			"command":   fields[1],
		}
	case manager.ChangeProfileCommandAdapter:
		parsed, err := parseCommandAdapterChange(input)
		if err != nil {
			return manager.TypedChange{}, err
		}
		value = parsed
	case manager.ChangeActivityRetention:
		fields := strings.Fields(input)
		if len(fields) != 2 {
			return manager.TypedChange{}, errors.New(
				"Use: 256MiB lifecycle, 128MiB 7d, or <bytes> <seconds>.",
			)
		}
		maxBytes, firstErr := parseRetentionBytes(fields[0])
		maxAge, secondErr := parseRetentionAge(fields[1])
		if firstErr != nil || secondErr != nil {
			return manager.TypedChange{}, errors.New(
				"Use a binary size such as 256MiB and lifecycle, 12h, 7d, or seconds.",
			)
		}
		value = map[string]any{
			"maxBytes":      maxBytes,
			"maxAgeSeconds": maxAge,
		}
	default:
		return manager.TypedChange{}, errors.New(
			"This Manager configuration capability is unsupported.",
		)
	}
	change, err := manager.NewTypedChange(kind, value)
	if err != nil {
		return manager.TypedChange{}, errors.New(
			"Draft value is invalid for this configuration field.",
		)
	}
	return change, nil
}

func parseRetentionBytes(input string) (int64, error) {
	if value, err := strconv.ParseInt(input, 10, 64); err == nil {
		return value, nil
	}
	upper := strings.ToUpper(strings.TrimSpace(input))
	multipliers := []struct {
		suffix     string
		multiplier int64
	}{
		{suffix: "TIB", multiplier: 1 << 40},
		{suffix: "GIB", multiplier: 1 << 30},
		{suffix: "MIB", multiplier: 1 << 20},
		{suffix: "KIB", multiplier: 1 << 10},
	}
	for _, candidate := range multipliers {
		if !strings.HasSuffix(upper, candidate.suffix) {
			continue
		}
		number := strings.TrimSpace(
			upper[:len(upper)-len(candidate.suffix)],
		)
		value, err := strconv.ParseInt(number, 10, 64)
		if err != nil ||
			value <= 0 ||
			value > (1<<63-1)/candidate.multiplier {
			return 0, errors.New("retention byte size is invalid")
		}
		return value * candidate.multiplier, nil
	}
	return 0, errors.New("retention byte size is invalid")
}

func parseRetentionAge(input string) (int64, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	switch input {
	case "lifecycle", "vm", "0":
		return 0, nil
	}
	if value, err := strconv.ParseInt(input, 10, 64); err == nil {
		return value, nil
	}
	multiplier := int64(0)
	switch {
	case strings.HasSuffix(input, "d"):
		multiplier = 24 * 60 * 60
	case strings.HasSuffix(input, "w"):
		multiplier = 7 * 24 * 60 * 60
	}
	if multiplier != 0 {
		value, err := strconv.ParseInt(input[:len(input)-1], 10, 64)
		if err != nil ||
			value <= 0 ||
			value > (1<<63-1)/multiplier {
			return 0, errors.New("retention age is invalid")
		}
		return value * multiplier, nil
	}
	duration, err := time.ParseDuration(input)
	if err != nil || duration < 0 || duration%time.Second != 0 {
		return 0, errors.New("retention age is invalid")
	}
	return int64(duration / time.Second), nil
}

func parseEnvironmentChange(input string) (map[string]any, error) {
	fields := strings.Fields(input)
	if len(fields) < 2 {
		return nil, errors.New(
			"Use: set NAME VALUE, unset NAME, inherit NAME, uninherit NAME, deny PATTERN, or undeny PATTERN.",
		)
	}
	value := map[string]any{}
	switch fields[0] {
	case "set":
		parts := strings.SplitN(input, " ", 3)
		if len(parts) != 3 || parts[2] == "" {
			return nil, errors.New("Use: set NAME VALUE.")
		}
		value["set"] = map[string]string{parts[1]: parts[2]}
	case "unset", "inherit", "uninherit", "deny", "undeny":
		if len(fields) != 2 {
			return nil, errors.New(
				"Environment list changes accept exactly one name or pattern.",
			)
		}
		value[fields[0]] = []string{fields[1]}
	default:
		return nil, errors.New("Environment operation is unsupported.")
	}
	return value, nil
}

func parseHostFSChange(input string) (map[string]any, error) {
	if strings.HasPrefix(input, "remove ") {
		fields := strings.Fields(input)
		if len(fields) != 2 {
			return nil, errors.New("Use: remove <rule-id>.")
		}
		return map[string]any{
			"operation": "remove",
			"ruleId":    fields[1],
		}, nil
	}
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		return nil, errors.New(
			"Use: add <rule> | <reason> or deny <rule> | <reason>.",
		)
	}
	left := strings.Fields(strings.TrimSpace(parts[0]))
	reason := strings.TrimSpace(parts[1])
	if len(left) < 2 ||
		(left[0] != "add" && left[0] != "deny") ||
		reason == "" {
		return nil, errors.New(
			"Use: add <rule> | <reason> or deny <rule> | <reason>.",
		)
	}
	return map[string]any{
		"operation": left[0],
		"rule": strings.TrimSpace(
			strings.TrimPrefix(
				strings.TrimSpace(parts[0]),
				left[0],
			),
		),
		"reason": reason,
	}, nil
}

func parseCommandAdapterChange(input string) (map[string]any, error) {
	fields := strings.Fields(input)
	if len(fields) < 2 {
		return nil, errors.New(
			"Use an adapter operation followed by its adapter ID.",
		)
	}
	value := map[string]any{
		"operation": fields[0],
		"adapterId": fields[1],
	}
	switch fields[0] {
	case "enable", "disable", "refresh-digest", "remove",
		"add-builtin-root-sensitive":
		if len(fields) != 2 {
			return nil, errors.New(
				"This adapter operation accepts only its adapter ID.",
			)
		}
	case "add-local":
		if len(fields) < 4 {
			return nil, errors.New(
				"Use: add-local <id> <path> <command>[,<command>...].",
			)
		}
		value["path"] = fields[2]
		value["commands"] = strings.Split(fields[3], ",")
	default:
		return nil, errors.New("Adapter operation is unsupported.")
	}
	return value, nil
}

func parseSecretDraft(input string) (manager.SecretDraft, error) {
	fields := strings.Fields(input)
	if len(fields) != 2 {
		return manager.SecretDraft{}, errors.New(
			"Use: set <ref>, rotate <ref>, or delete <ref>.",
		)
	}
	draft := manager.SecretDraft{
		Schema: manager.SecretDraftSchema,
		Action: fields[0],
		Ref:    fields[1],
	}
	if err := draft.Validate(); err != nil {
		return manager.SecretDraft{}, errors.New(
			"Secret action or reference is invalid.",
		)
	}
	return draft, nil
}

func validEditorID(value string) bool {
	if value == EditorSecret {
		return true
	}
	_, ok := manager.DefaultTypedChangeRegistry().Definition(value)
	return ok
}

func editorLabel(value string) string {
	switch value {
	case manager.ChangeNetworkPosture:
		return "Connection mode"
	case manager.ChangeNetworkProxyRef:
		return "Proxy secret reference"
	case manager.ChangeNetworkDNS:
		return "DNS mediation"
	case manager.ChangeProfileEnvironment:
		return "Environment policy"
	case manager.ChangeProfileHostFS:
		return "Host file access"
	case manager.ChangeProfileCommandProxy:
		return "Command proxy"
	case manager.ChangeProfileCommandAdapter:
		return "Command adapters"
	case manager.ChangeActivityRetention:
		return "Activity retention"
	case EditorSecret:
		return "Secret lifecycle"
	default:
		return "Configuration"
	}
}

func editorPrompt(value string) string {
	switch value {
	case manager.ChangeNetworkPosture:
		return "Enter direct or proxy."
	case manager.ChangeNetworkProxyRef:
		return "Enter a managed secret reference, for example local-proxy."
	case manager.ChangeNetworkDNS:
		return "Enter system, ip <address>, or doh <address>."
	case manager.ChangeProfileEnvironment:
		return "Enter set/unset/inherit/uninherit/deny/undeny and one value."
	case manager.ChangeProfileHostFS:
		return "Enter add/deny <rule> | <reason>, or remove <rule-id>."
	case manager.ChangeProfileCommandProxy:
		return "Enter add-open <command> or remove <command>."
	case manager.ChangeProfileCommandAdapter:
		return "Enter an adapter operation and adapter ID."
	case manager.ChangeActivityRetention:
		return "Enter a size and lifetime, for example 256MiB lifecycle or 128MiB 7d."
	case EditorSecret:
		return "Enter set/rotate/delete and a managed reference. Values are requested later."
	default:
		return "Enter a proposed value."
	}
}

func cloneOperation(
	operation *manager.Operation,
) *manager.Operation {
	if operation == nil {
		return nil
	}
	copy := *operation
	return &copy
}

func safeAuthorityError(prefix string, err error) string {
	if err == nil {
		return prefix + "."
	}
	// Manager errors are useful for recovery, but terminal control data never
	// crosses into rendering.
	return prefix + ": " + safeInline(err.Error())
}

func safeInline(value string) string {
	var output strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) ||
			isBidiControl(character) {
			continue
		}
		output.WriteRune(character)
	}
	return strings.TrimSpace(output.String())
}

func isBidiControl(character rune) bool {
	switch character {
	case '\u061c', '\u200e', '\u200f',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}

func fitLines(value string, width int) string {
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	for index, line := range lines {
		runes := []rune(line)
		if len(runes) > width {
			lines[index] = string(runes[:width])
			clear(runes)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
