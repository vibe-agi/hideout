package modal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/session"
)

const maxLifecycleConfirmationRunes = 256

// LifecycleProvider is the existing authenticated Manager environment
// plan/apply authority. The TUI never invokes a backend, edits an environment
// record, or broadens an exact environment ID into an implicit selector.
type LifecycleProvider interface {
	PlanEnvironment(
		context.Context,
		string,
		manager.EnvironmentActionAPIRequest,
	) (manager.EnvironmentActionPlan, error)
	ApplyEnvironment(
		context.Context,
		string,
		manager.EnvironmentActionAPIRequest,
	) (manager.EnvironmentActionResult, error)
}

type LifecycleOptions struct {
	Context               context.Context
	Provider              LifecycleProvider
	Environments          []manager.EnvironmentSummary
	Sessions              []manager.SessionSummary
	SelectedEnvironmentID string
	Mutable               bool
}

type LifecycleOutcome struct {
	Close  bool
	Result *manager.EnvironmentActionResult
}

type Lifecycle struct {
	ctx          context.Context
	provider     LifecycleProvider
	environments []manager.EnvironmentSummary
	sessions     []manager.SessionSummary
	selected     int
	action       string
	mutable      bool

	stage           Stage
	authorityReason string
	errorMessage    string
	input           []rune
	requestID       uint64
	baseFingerprint string
	plan            *manager.EnvironmentActionPlan
	result          *manager.EnvironmentActionResult
}

type lifecyclePlanMsg struct {
	requestID uint64
	action    string
	targetID  string
	plan      manager.EnvironmentActionPlan
	err       error
}

type lifecycleApplyMsg struct {
	requestID uint64
	action    string
	targetID  string
	result    manager.EnvironmentActionResult
	err       error
}

type lifecycleBlocker struct {
	code     string
	summary  string
	recovery string
}

func NewLifecycle(options LifecycleOptions) *Lifecycle {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	editor := &Lifecycle{
		ctx: ctx, provider: options.Provider,
		environments: cloneLifecycleEnvironments(options.Environments),
		sessions:     append([]manager.SessionSummary(nil), options.Sessions...),
		action:       manager.EnvironmentActionStop,
		mutable:      options.Mutable,
		stage:        StageEditing,
	}
	editor.sortEnvironments(options.SelectedEnvironmentID)
	if len(editor.environments) == 0 {
		editor.stage = StageError
		editor.errorMessage =
			"No current environments are present in this scope."
		return editor
	}
	if !options.Mutable || options.Provider == nil {
		editor.stage = StageStale
		editor.authorityReason =
			"authenticated lifecycle mutation authority is unavailable"
	}
	return editor
}

func (editor *Lifecycle) Stage() Stage {
	if editor == nil {
		return StageError
	}
	return editor.stage
}

func (editor *Lifecycle) Action() string {
	if editor == nil {
		return ""
	}
	return editor.action
}

func (editor *Lifecycle) SelectedEnvironmentID() string {
	if editor == nil || len(editor.environments) == 0 ||
		editor.selected < 0 ||
		editor.selected >= len(editor.environments) {
		return ""
	}
	return editor.environments[editor.selected].ID
}

func (editor *Lifecycle) Clear() {
	if editor == nil {
		return
	}
	editor.requestID++
	editor.clearInput()
}

func (editor *Lifecycle) Update(
	message tea.Msg,
) (tea.Cmd, LifecycleOutcome) {
	if editor == nil {
		return nil, LifecycleOutcome{Close: true}
	}
	switch typed := message.(type) {
	case lifecyclePlanMsg:
		return editor.receivePlan(typed)
	case lifecycleApplyMsg:
		return editor.receiveApply(typed)
	case tea.KeyPressMsg:
		return editor.updateKey(typed)
	default:
		return nil, LifecycleOutcome{}
	}
}

func (editor *Lifecycle) updateKey(
	message tea.KeyPressMsg,
) (tea.Cmd, LifecycleOutcome) {
	key := message.String()
	if key == "esc" {
		editor.requestID++
		editor.clearInput()
		return nil, LifecycleOutcome{Close: true}
	}
	switch editor.stage {
	case StageEditing:
		switch key {
		case "j", "down":
			editor.moveSelection(1)
		case "k", "up":
			editor.moveSelection(-1)
		case "s":
			editor.action = manager.EnvironmentActionStop
		case "c":
			editor.action = manager.EnvironmentActionClean
		case "enter":
			return editor.startPlan()
		}
	case StageReview:
		switch key {
		case "a":
			if editor.applyDisabled() {
				return nil, LifecycleOutcome{}
			}
			editor.clearInput()
			editor.stage = StageConfirming
		case "e":
			editor.plan = nil
			editor.result = nil
			editor.baseFingerprint = ""
			editor.clearInput()
			editor.stage = StageEditing
		}
	case StageConfirming:
		if editor.action == manager.EnvironmentActionClean {
			switch key {
			case "enter":
				if string(editor.input) !=
					editor.SelectedEnvironmentID() {
					editor.errorMessage =
						"Typed confirmation does not match the exact environment ID."
					return nil, LifecycleOutcome{}
				}
				editor.clearInput()
				return editor.startApply()
			case "backspace":
				editor.removeInputRune()
			default:
				editor.appendInput(message)
			}
			return nil, LifecycleOutcome{}
		}
		switch strings.ToLower(key) {
		case "y":
			return editor.startApply()
		case "n":
			editor.stage = StageReview
		}
	case StageTerminal:
		if key == "enter" {
			editor.clearInput()
			return nil, LifecycleOutcome{
				Close:  true,
				Result: cloneLifecycleResult(editor.result),
			}
		}
	case StageError:
		if key == "e" && editor.mutable &&
			len(editor.environments) != 0 {
			editor.errorMessage = ""
			editor.stage = StageEditing
		}
	}
	return nil, LifecycleOutcome{}
}

func (editor *Lifecycle) startPlan() (tea.Cmd, LifecycleOutcome) {
	targetID := editor.SelectedEnvironmentID()
	if !editor.mutable || editor.provider == nil || targetID == "" {
		editor.stage = StageStale
		editor.authorityReason =
			"authenticated lifecycle mutation authority is unavailable"
		return nil, LifecycleOutcome{}
	}
	editor.errorMessage = ""
	editor.requestID++
	requestID := editor.requestID
	action := editor.action
	request := exactLifecycleRequest(targetID)
	editor.baseFingerprint = editor.selectedFingerprint()
	editor.stage = StagePlanning
	provider := editor.provider
	ctx := editor.ctx
	return func() tea.Msg {
		plan, err := provider.PlanEnvironment(ctx, action, request)
		return lifecyclePlanMsg{
			requestID: requestID,
			action:    action,
			targetID:  targetID,
			plan:      plan,
			err:       err,
		}
	}, LifecycleOutcome{}
}

func (editor *Lifecycle) receivePlan(
	message lifecyclePlanMsg,
) (tea.Cmd, LifecycleOutcome) {
	if message.requestID != editor.requestID ||
		editor.stage != StagePlanning {
		return nil, LifecycleOutcome{}
	}
	if message.err != nil {
		editor.stage = StageError
		editor.errorMessage = safeAuthorityError(
			"Hideout could not create an environment lifecycle plan",
			message.err,
		)
		return nil, LifecycleOutcome{}
	}
	if message.action != editor.action ||
		message.targetID != editor.SelectedEnvironmentID() ||
		!validExactLifecyclePlan(
			message.plan,
			message.action,
			message.targetID,
		) {
		editor.stage = StageError
		editor.errorMessage =
			"Hideout returned a lifecycle plan for a different environment."
		return nil, LifecycleOutcome{}
	}
	plan := message.plan
	editor.plan = &plan
	editor.stage = StageReview
	return nil, LifecycleOutcome{}
}

func (editor *Lifecycle) startApply() (tea.Cmd, LifecycleOutcome) {
	targetID := editor.SelectedEnvironmentID()
	if editor.plan == nil || editor.applyDisabled() ||
		editor.provider == nil || targetID == "" {
		editor.stage = StageStale
		editor.authorityReason =
			"the reviewed lifecycle plan can no longer be applied"
		return nil, LifecycleOutcome{}
	}
	editor.requestID++
	requestID := editor.requestID
	action := editor.action
	request := exactLifecycleRequest(targetID)
	request.OperationID = editor.plan.OperationID
	request.PlanDigest = editor.plan.PlanDigest
	request.Confirmed = true
	editor.stage = StageApplying
	provider := editor.provider
	ctx := editor.ctx
	return func() tea.Msg {
		result, err := provider.ApplyEnvironment(
			ctx,
			action,
			request,
		)
		return lifecycleApplyMsg{
			requestID: requestID,
			action:    action,
			targetID:  targetID,
			result:    result,
			err:       err,
		}
	}, LifecycleOutcome{}
}

func (editor *Lifecycle) receiveApply(
	message lifecycleApplyMsg,
) (tea.Cmd, LifecycleOutcome) {
	if message.requestID != editor.requestID ||
		editor.stage != StageApplying {
		return nil, LifecycleOutcome{}
	}
	if message.err != nil {
		editor.stage = StageTerminal
		editor.errorMessage = safeAuthorityError(
			"Lifecycle outcome was not proved",
			message.err,
		)
		return nil, LifecycleOutcome{}
	}
	if message.action != editor.action ||
		message.targetID != editor.SelectedEnvironmentID() ||
		!validExactLifecycleResult(
			message.result,
			message.action,
			message.targetID,
		) {
		editor.stage = StageTerminal
		editor.errorMessage =
			"Hideout returned a lifecycle result for a different environment."
		return nil, LifecycleOutcome{}
	}
	result := message.result
	editor.result = &result
	editor.errorMessage = ""
	editor.stage = StageTerminal
	return nil, LifecycleOutcome{Result: &result}
}

// SyncAuthority invalidates an unaccepted plan when the selected environment
// projection changes. The Manager still rechecks ownership and backend state
// under its own locks; this client-side check only prevents knowingly stale
// review from remaining actionable.
func (editor *Lifecycle) SyncAuthority(
	mutable bool,
	environments []manager.EnvironmentSummary,
	sessions []manager.SessionSummary,
	reason string,
) {
	if editor == nil {
		return
	}
	if editor.stage == StageApplying || editor.stage == StageTerminal {
		return
	}
	selectedID := editor.SelectedEnvironmentID()
	editor.environments = cloneLifecycleEnvironments(environments)
	editor.sessions = append([]manager.SessionSummary(nil), sessions...)
	editor.sortEnvironments(selectedID)
	editor.mutable = mutable
	editor.authorityReason = safeInline(reason)
	if !mutable || editor.provider == nil {
		editor.stage = StageStale
		if editor.authorityReason == "" {
			editor.authorityReason =
				"authenticated lifecycle mutation authority is unavailable"
		}
		return
	}
	if selectedID == "" || editor.SelectedEnvironmentID() != selectedID {
		editor.stage = StageStale
		editor.authorityReason =
			"the selected environment is no longer present; refresh before planning"
		return
	}
	if editor.baseFingerprint != "" &&
		editor.selectedFingerprint() != editor.baseFingerprint {
		editor.requestID++
		editor.stage = StageStale
		editor.authorityReason =
			"environment state changed; refresh and review a new lifecycle plan"
	}
}

func (editor *Lifecycle) applyDisabled() bool {
	if editor == nil || !editor.mutable || editor.plan == nil ||
		len(editor.plan.Targets) != 1 {
		return true
	}
	return len(editor.activeBlockers()) != 0
}

func (editor *Lifecycle) activeBlockers() []lifecycleBlocker {
	if editor == nil {
		return nil
	}
	targetID := editor.SelectedEnvironmentID()
	environment, ok := editor.selectedEnvironment()
	if !ok {
		return []lifecycleBlocker{{
			code:     "environment.missing",
			summary:  "the exact environment is absent from the current verified state",
			recovery: "refresh the snapshot",
		}}
	}
	blockers := make([]lifecycleBlocker, 0)
	visibleLive := 0
	for _, value := range editor.sessions {
		if value.EnvironmentID != targetID {
			continue
		}
		if value.OwnerStatus != session.OwnerLive &&
			value.OwnerStatus != session.OwnerUnprovable &&
			value.State != session.OwnerStatePreparing &&
			value.State != session.OwnerStateRunning &&
			value.State != session.OwnerStateCleaning {
			continue
		}
		visibleLive++
		state := string(value.State)
		if state == "" {
			state = string(value.OwnerStatus)
		}
		blockers = append(blockers, lifecycleBlocker{
			code: "session.active",
			summary: "session " + safeInline(value.ID) +
				" is " + safeInline(state),
			recovery: "exit the workload and wait for session cleanup",
		})
	}
	if environment.ActiveSessions > visibleLive {
		blockers = append(blockers, lifecycleBlocker{
			code: "session.active-count",
			summary: fmt.Sprintf(
				"%d additional active session(s) are not listed in this scope",
				environment.ActiveSessions-visibleLive,
			),
			recovery: "inspect the full environment scope and end active sessions",
		})
	}
	if environment.OwnerHealth == string(session.OwnerUnprovable) {
		blockers = append(blockers, lifecycleBlocker{
			code:     "session.owner-unprovable",
			summary:  "session ownership cannot be proved absent",
			recovery: "run lifecycle recovery before retrying",
		})
	}
	if environment.ActiveWorkspaceViews > 0 {
		blockers = append(blockers, lifecycleBlocker{
			code: "workspace-view.active",
			summary: fmt.Sprintf(
				"%d active workspace view(s) remain",
				environment.ActiveWorkspaceViews,
			),
			recovery: "close the owning sessions and wait for workspace-view cleanup",
		})
	}
	return blockers
}

func (editor *Lifecycle) View(width int) string {
	if editor == nil {
		return "Environment lifecycle unavailable.\n"
	}
	var lines []string
	switch editor.stage {
	case StageEditing:
		lines = []string{
			"Environment lifecycle",
			"Draft only · selecting an action has no effect",
			"",
			lifecycleActionLine(editor.action),
			"",
			"ENVIRONMENT | PROFILE | STATUS | ACTIVE",
		}
		for index, value := range editor.environments {
			marker := " "
			if index == editor.selected {
				marker = ">"
			}
			lines = append(lines, fmt.Sprintf(
				"%s %s | %s | %s | %d",
				marker,
				safeInline(value.ID),
				safeInline(value.Profile),
				safeInline(value.Status),
				value.ActiveSessions,
			))
		}
		lines = append(
			lines,
			"",
			"j/k select · s stop · c clean · Enter review · Esc cancel",
		)
	case StagePlanning:
		lines = []string{
			"Planning environment " + safeInline(editor.action),
			"Target " + safeInline(editor.SelectedEnvironmentID()),
			"Resolving the exact lifecycle target…",
			"Esc closes this client; no lifecycle action has been applied.",
		}
	case StageReview:
		lines = editor.reviewLines()
	case StageConfirming:
		if editor.action == manager.EnvironmentActionClean {
			lines = []string{
				"Confirm destructive environment clean",
				`To continue, type "` +
					safeInline(editor.SelectedEnvironmentID()) + `":`,
				"> " + safeInline(string(editor.input)),
				"",
				"Enter confirm exact target · Esc cancel",
			}
		} else {
			lines = []string{
				"Confirm environment stop",
				"Stop this exact environment? [y/N]",
				"y confirm · n back · Esc cancel",
			}
		}
	case StageApplying:
		lines = []string{
			"Applying environment " + safeInline(editor.action),
			"Target " + safeInline(editor.SelectedEnvironmentID()),
			"Rechecking active users and VM state…",
			"Esc closes this dialog; it does not cancel an accepted request.",
		}
	case StageTerminal:
		lines = editor.terminalLines()
	case StageStale:
		reason := editor.authorityReason
		if reason == "" {
			reason = "verified environment state changed"
		}
		lines = []string{
			"STALE · read-only",
			safeInline(reason),
			"This lifecycle draft cannot be planned or applied.",
			"Esc discard",
		}
	case StageError:
		lines = []string{
			"Environment lifecycle plan unavailable",
			safeInline(editor.errorMessage),
			"e return to selection · Esc cancel",
		}
	}
	if editor.errorMessage != "" &&
		editor.stage != StageError &&
		editor.stage != StageTerminal {
		lines = append(
			lines,
			"",
			"ERROR "+safeInline(editor.errorMessage),
		)
	}
	return fitLines(strings.Join(lines, "\n")+"\n", width)
}

func (editor *Lifecycle) reviewLines() []string {
	plan := editor.plan
	if plan == nil {
		return []string{"Environment lifecycle plan unavailable"}
	}
	lines := []string{
		"Review environment " + safeInline(plan.Action),
		"Exact target " + safeInline(editor.SelectedEnvironmentID()),
		"Hideout rechecks this ID, active users, and VM state before applying.",
		"",
	}
	if plan.Action == manager.EnvironmentActionClean {
		lines = append(
			lines,
			"Effect · removes environment metadata and backend state; this cannot be undone",
			"Activity cleanup remains subject to exact lifecycle completion evidence.",
		)
	} else {
		lines = append(
			lines,
			"Effect · stops the backend instance and retains environment metadata",
			"Existing sessions are not killed; active ownership blocks the request.",
		)
	}
	lines = append(lines, "", "Targets")
	for _, target := range plan.Targets {
		lines = append(
			lines,
			"  "+safeInline(target.ID)+" · "+
				safeInline(target.Backend)+" · "+
				safeInline(target.Status)+" · instance "+
				safeInline(target.InstanceName),
		)
	}
	if len(plan.Targets) == 0 {
		lines = append(lines, "  none")
	}
	if len(plan.Skipped) != 0 {
		lines = append(lines, "", "Skipped")
		for _, target := range plan.Skipped {
			lines = append(
				lines,
				"  "+safeInline(target.ID)+" · "+
					safeInline(target.Reason),
			)
		}
	}
	blockers := editor.activeBlockers()
	if len(blockers) != 0 {
		lines = append(lines, "", "ACTIVE BLOCKERS")
		for _, blocker := range blockers {
			lines = append(
				lines,
				"  "+safeInline(blocker.code)+" · "+
					safeInline(blocker.summary),
				"    recovery · "+safeInline(blocker.recovery),
			)
		}
	}
	if editor.applyDisabled() {
		lines = append(
			lines,
			"",
			"APPLY DISABLED · resolve blockers or choose a valid target, then review again",
			"e select · Esc cancel",
		)
	} else {
		lines = append(
			lines,
			"",
			"a choose Apply · Enter only inspects · e select · Esc cancel",
		)
	}
	return lines
}

func (editor *Lifecycle) terminalLines() []string {
	targetID := safeInline(editor.SelectedEnvironmentID())
	if editor.errorMessage != "" || editor.result == nil {
		return []string{
			"LIFECYCLE OUTCOME UNPROVED",
			"Action " + safeInline(editor.action) +
				" · target " + targetID,
			safeInline(editor.errorMessage),
			"Refresh current environment state before deciding whether another action is needed.",
			"Enter/Esc close",
		}
	}
	if len(editor.result.Applied) == 0 {
		lines := []string{
			"NO LIFECYCLE CHANGE",
			"Action " + safeInline(editor.action) +
				" · target " + targetID,
		}
		for _, target := range editor.result.Skipped {
			lines = append(
				lines,
				"Skipped "+safeInline(target.ID)+
					" · "+safeInline(target.Reason),
			)
		}
		return append(lines, "Enter/Esc close")
	}
	title := strings.ToUpper(editor.action) + " COMPLETE"
	lines := []string{title}
	for _, target := range editor.result.Applied {
		status := target.Status
		if editor.action == manager.EnvironmentActionClean {
			status = "removed"
		}
		lines = append(
			lines,
			safeInline(target.ID)+" · "+safeInline(status),
		)
	}
	return append(
		lines,
		"Hideout returned verified final lifecycle evidence.",
		"Enter/Esc close",
	)
}

func (editor *Lifecycle) moveSelection(delta int) {
	if len(editor.environments) == 0 || delta == 0 {
		return
	}
	editor.selected =
		(editor.selected + delta) % len(editor.environments)
	if editor.selected < 0 {
		editor.selected += len(editor.environments)
	}
	editor.plan = nil
	editor.result = nil
	editor.baseFingerprint = ""
	editor.errorMessage = ""
}

func (editor *Lifecycle) sortEnvironments(preferred string) {
	sort.SliceStable(editor.environments, func(left, right int) bool {
		leftTime := lifecycleSortTime(editor.environments[left])
		rightTime := lifecycleSortTime(editor.environments[right])
		if leftTime.Equal(rightTime) {
			return editor.environments[left].ID <
				editor.environments[right].ID
		}
		if leftTime.IsZero() {
			return false
		}
		if rightTime.IsZero() {
			return true
		}
		return leftTime.After(rightTime)
	})
	editor.selected = 0
	for index, value := range editor.environments {
		if value.ID == preferred {
			editor.selected = index
			break
		}
	}
}

func lifecycleSortTime(value manager.EnvironmentSummary) time.Time {
	if value.LastStartedAt != nil && !value.LastStartedAt.IsZero() {
		return *value.LastStartedAt
	}
	return value.CreatedAt
}

func (editor *Lifecycle) selectedEnvironment() (
	manager.EnvironmentSummary,
	bool,
) {
	if editor == nil || len(editor.environments) == 0 ||
		editor.selected < 0 ||
		editor.selected >= len(editor.environments) {
		return manager.EnvironmentSummary{}, false
	}
	return editor.environments[editor.selected], true
}

func (editor *Lifecycle) selectedFingerprint() string {
	value, ok := editor.selectedEnvironment()
	if !ok {
		return ""
	}
	lastStarted, lastEnded := "", ""
	if value.LastStartedAt != nil {
		lastStarted = value.LastStartedAt.UTC().Format(
			"2006-01-02T15:04:05.999999999Z07:00",
		)
	}
	if value.LastEndedAt != nil {
		lastEnded = value.LastEndedAt.UTC().Format(
			"2006-01-02T15:04:05.999999999Z07:00",
		)
	}
	return fmt.Sprintf(
		"%q|%q|%q|%q|%q|%d|%d|%q|%q|%q",
		value.ID,
		value.Status,
		value.Backend,
		value.InstanceName,
		value.LastSessionID,
		value.ActiveSessions,
		value.ActiveWorkspaceViews,
		value.OwnerHealth,
		lastStarted,
		lastEnded,
	)
}

func (editor *Lifecycle) appendInput(message tea.KeyPressMsg) {
	text := message.Key().Text
	if text == "" || len(editor.input) >=
		maxLifecycleConfirmationRunes {
		return
	}
	for _, character := range text {
		if unicode.IsControl(character) ||
			isBidiControl(character) ||
			len(editor.input) >= maxLifecycleConfirmationRunes {
			continue
		}
		editor.input = append(editor.input, character)
	}
	editor.errorMessage = ""
}

func (editor *Lifecycle) removeInputRune() {
	if len(editor.input) == 0 {
		return
	}
	editor.input[len(editor.input)-1] = 0
	editor.input = editor.input[:len(editor.input)-1]
	editor.errorMessage = ""
}

func (editor *Lifecycle) clearInput() {
	clear(editor.input)
	editor.input = nil
}

func lifecycleActionLine(action string) string {
	stop, clean := "stop", "clean"
	if action == manager.EnvironmentActionClean {
		clean = "> clean"
	} else {
		stop = "> stop"
	}
	return "Action  " + stop + "   " + clean
}

func exactLifecycleRequest(
	targetID string,
) manager.EnvironmentActionAPIRequest {
	return manager.EnvironmentActionAPIRequest{
		IDs: []string{targetID},
	}
}

func validExactLifecyclePlan(
	plan manager.EnvironmentActionPlan,
	action string,
	targetID string,
) bool {
	if action != manager.EnvironmentActionStop &&
		action != manager.EnvironmentActionClean ||
		plan.Action != action ||
		len(plan.RequestedIDs) != 1 ||
		plan.RequestedIDs[0] != targetID ||
		plan.Filter != (manager.EnvironmentActionFilter{}) ||
		plan.Force ||
		plan.Total != 1 ||
		len(plan.Targets)+len(plan.Skipped) != 1 ||
		!validLifecycleOperationID(plan.OperationID) ||
		!validLifecyclePlanDigest(plan.PlanDigest) {
		return false
	}
	for _, target := range append(
		append(
			[]manager.EnvironmentActionTarget(nil),
			plan.Targets...,
		),
		plan.Skipped...,
	) {
		if target.ID != targetID ||
			len(target.Profile) > 128 ||
			len(target.Backend) > 128 ||
			len(target.Status) > 64 ||
			len(target.Workspace) > 4096 ||
			len(target.GuestWorkspace) > 4096 ||
			len(target.InstanceName) > 256 ||
			len(target.LastSessionID) > 128 ||
			len(target.LastCommand) > 8192 ||
			len(target.Reason) > 1024 ||
			strings.IndexByte(target.LastCommand, 0) >= 0 {
			return false
		}
	}
	return true
}

func validExactLifecycleResult(
	result manager.EnvironmentActionResult,
	action string,
	targetID string,
) bool {
	if !validExactLifecyclePlan(result.Plan, action, targetID) {
		return false
	}
	if result.Operation == nil ||
		result.Operation.Validate() != nil ||
		result.Operation.ID != result.Plan.OperationID ||
		result.Operation.Kind != "environment."+action ||
		result.Operation.Owner != (manager.OperationOwner{
			Kind: "environment", ID: targetID,
		}) ||
		result.Operation.PlanDigest != result.Plan.PlanDigest ||
		result.Operation.Phase != manager.OperationSucceeded {
		return false
	}
	if len(result.Applied)+len(result.Skipped) == 0 ||
		len(result.Applied)+len(result.Skipped) > 2 {
		return false
	}
	for _, target := range append(
		append(
			[]manager.EnvironmentActionTarget(nil),
			result.Applied...,
		),
		result.Skipped...,
	) {
		if target.ID != targetID {
			return false
		}
	}
	return true
}

func cloneLifecycleEnvironments(
	values []manager.EnvironmentSummary,
) []manager.EnvironmentSummary {
	out := append([]manager.EnvironmentSummary(nil), values...)
	for index := range out {
		if out[index].LastStartedAt != nil {
			value := *out[index].LastStartedAt
			out[index].LastStartedAt = &value
		}
		if out[index].LastEndedAt != nil {
			value := *out[index].LastEndedAt
			out[index].LastEndedAt = &value
		}
	}
	return out
}

func cloneLifecycleResult(
	value *manager.EnvironmentActionResult,
) *manager.EnvironmentActionResult {
	if value == nil {
		return nil
	}
	copied := *value
	copied.Plan.RequestedIDs = append(
		[]string(nil),
		value.Plan.RequestedIDs...,
	)
	copied.Plan.Targets = append(
		[]manager.EnvironmentActionTarget(nil),
		value.Plan.Targets...,
	)
	copied.Plan.Skipped = append(
		[]manager.EnvironmentActionTarget(nil),
		value.Plan.Skipped...,
	)
	copied.Applied = append(
		[]manager.EnvironmentActionTarget(nil),
		value.Applied...,
	)
	copied.Skipped = append(
		[]manager.EnvironmentActionTarget(nil),
		value.Skipped...,
	)
	if value.Operation != nil {
		operation := *value.Operation
		operation.Effects = append(
			[]manager.EffectResult(nil),
			value.Operation.Effects...,
		)
		for index := range operation.Effects {
			operation.Effects[index].Evidence = append(
				[]manager.EvidenceRef(nil),
				value.Operation.Effects[index].Evidence...,
			)
		}
		copied.Operation = &operation
	}
	return &copied
}

func validLifecycleOperationID(value string) bool {
	if len(value) < len("op_")+8 || len(value) > len("op_")+124 ||
		!strings.HasPrefix(value, "op_") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "op_") {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validLifecyclePlanDigest(value string) bool {
	if len(value) != len("sha256:")+64 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
