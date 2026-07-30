package tui

import (
	"context"
	"errors"
	"os"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/operatorhelp"
	"github.com/vibe-agi/hideout/internal/tui/components"
	tuimodal "github.com/vibe-agi/hideout/internal/tui/modal"
	tuirender "github.com/vibe-agi/hideout/internal/tui/render"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type Layout string

const (
	LayoutNormal   Layout = "normal"
	LayoutNarrow   Layout = "narrow"
	LayoutTooSmall Layout = "too-small"
)

type Focus string

const (
	FocusPrimary Focus = "primary"
	FocusDetails Focus = "details"
	FocusFooter  Focus = "footer"
	FocusFilter  Focus = "filter"
)

type View string

const (
	ViewOverview   View = "overview"
	ViewActivity   View = "activity"
	ViewConfig     View = "config"
	ViewOperations View = "operations"
	ViewHelp       View = "help"
)

type ActivityTab string

const (
	ActivityTabAll      ActivityTab = tuirender.ActivityTabAll
	ActivityTabCommands ActivityTab = tuirender.ActivityTabCommands
	ActivityTabFiles    ActivityTab = tuirender.ActivityTabFiles
	ActivityTabNetwork  ActivityTab = tuirender.ActivityTabNetwork
	ActivityTabDNS      ActivityTab = tuirender.ActivityTabDNS
	ActivityTabRisks    ActivityTab = tuirender.ActivityTabRisks
)

var activityTabOrder = []ActivityTab{
	ActivityTabAll,
	ActivityTabCommands,
	ActivityTabFiles,
	ActivityTabNetwork,
	ActivityTabDNS,
	ActivityTabRisks,
}

type Modal string

const (
	ModalHelp      Modal = "help"
	ModalConfig    Modal = "config"
	ModalLifecycle Modal = "lifecycle"
)

type EventMsg struct {
	Event liveconsole.Event
}

type SnapshotMsg struct {
	State liveconsole.State
}

type FrameTickMsg struct {
	At time.Time
}

type StreamClosedMsg struct {
	Reason string
}

type ActivityLoadedMsg struct {
	requestID uint64
	sessionID string
	tab       ActivityTab
	data      tuirender.ActivityData
}

type ActivityFailedMsg struct {
	requestID uint64
	sessionID string
	tab       ActivityTab
	err       error
}

type ModelOptions struct {
	State         liveconsole.State
	Width         int
	Height        int
	SessionID     string
	NoColor       bool
	Unicode       bool
	Events        <-chan liveconsole.Event
	FrameInterval time.Duration
	Now           func() time.Time
	Context       context.Context
	HelpCatalog   operatorhelp.Catalog

	// ActivityProvider is the authenticated, read-only Manager activity query
	// surface. Configuration mutations never flow through this dependency.
	ActivityProvider manager.ActivityProvider
	// ConfigProvider is the authenticated Manager plan/apply authority. The
	// model creates only client-local drafts and never writes configuration.
	ConfigProvider tuimodal.Provider
	// LifecycleProvider is the existing authenticated Manager environment
	// stop/clean plan/apply authority.
	LifecycleProvider tuimodal.LifecycleProvider
}

type Model struct {
	state         liveconsole.State
	width         int
	height        int
	layout        Layout
	focus         Focus
	view          View
	modals        []Modal
	detailOpen    bool
	noColor       bool
	unicode       bool
	events        <-chan liveconsole.Event
	frameInterval time.Duration
	pending       []liveconsole.Event
	now           func() time.Time
	selector      components.SessionSelector
	widgets       components.Widgets

	activityContext  context.Context
	activityProvider manager.ActivityProvider
	activityTab      ActivityTab
	activityData     tuirender.ActivityData
	activitySelected int
	activityFilter   string
	activityLoaded   bool
	activityLoading  bool
	activityError    string
	activityRequest  uint64
	activityDirty    bool

	configProvider tuimodal.Provider
	configSelected int
	configModal    *tuimodal.Config

	lifecycleProvider tuimodal.LifecycleProvider
	lifecycleModal    *tuimodal.Lifecycle

	operationsSelected int
	operationLookupID  string
	helpCatalog        operatorhelp.Catalog
}

const maxPendingEvents = 256

func NewModel(options ModelOptions) *Model {
	interval := options.FrameInterval
	if interval <= 0 {
		interval = time.Second / 30
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	noColor := options.NoColor || os.Getenv("NO_COLOR") != ""
	activityContext := options.Context
	if activityContext == nil {
		activityContext = context.Background()
	}
	model := &Model{
		state: options.State, width: options.Width, height: options.Height,
		focus: FocusPrimary, view: ViewOverview,
		noColor: noColor, unicode: options.Unicode, events: options.Events,
		frameInterval: interval, now: now,
		widgets:         components.NewWidgets(options.Width, options.Height),
		activityContext: activityContext, activityProvider: options.ActivityProvider,
		configProvider:    options.ConfigProvider,
		lifecycleProvider: options.LifecycleProvider,
		activityTab:       ActivityTabAll,
		helpCatalog:       options.HelpCatalog.Clone(),
	}
	model.selector.Replace(options.State.Overview.Sessions, options.SessionID)
	model.layout = layoutForSize(options.Width, options.Height)
	return model
}

func (model *Model) Init() tea.Cmd {
	var commands []tea.Cmd
	if model.events != nil {
		commands = append(commands, model.waitForEvent(), model.frameTick())
	}
	commands = append(commands, model.widgets.Viewport.Init())
	return tea.Batch(commands...)
}

func (model *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = typed.Width, typed.Height
		model.layout = layoutForSize(typed.Width, typed.Height)
		model.widgets.Resize(typed.Width, typed.Height)
		return model, nil
	case EventMsg:
		if len(model.pending) >= maxPendingEvents {
			model.pending = nil
			model.state.ReadOnly = true
			model.state.RequiresReseed = true
			model.state.StreamHealth = liveconsole.StreamHealth{
				State: liveconsole.HealthStale, Reason: "TUI event queue overflow",
			}
		} else {
			model.pending = append(model.pending, typed.Event)
		}
		return model, model.waitForEvent()
	case FrameTickMsg:
		activityChanged := false
		for _, event := range model.pending {
			switch event.Kind {
			case liveconsole.KindActivity, liveconsole.KindCoverage, liveconsole.KindRisk:
				activityChanged = true
			}
			liveconsole.Apply(&model.state, event)
		}
		model.pending = nil
		model.selector.Replace(model.state.Overview.Sessions, model.selector.Selected())
		model.clampOperationsSelection()
		model.syncConfigModal()
		model.syncLifecycleModal()
		var activityCommand tea.Cmd
		if activityChanged && model.view == ViewActivity {
			if model.activityLoading {
				model.activityDirty = true
			} else {
				activityCommand = model.requestActivity()
			}
		}
		if model.events != nil {
			return model, tea.Batch(model.frameTick(), activityCommand)
		}
		return model, activityCommand
	case SnapshotMsg:
		preferred := model.selector.Selected()
		model.state = typed.State
		model.pending = nil
		model.selector.Replace(model.state.Overview.Sessions, preferred)
		model.detailOpen = false
		model.clearActivityForScope()
		model.clampOperationsSelection()
		model.syncConfigModal()
		model.syncLifecycleModal()
		if model.view == ViewActivity {
			return model, model.requestActivity()
		}
		return model, nil
	case StreamClosedMsg:
		model.state.ReadOnly = true
		model.state.RequiresReseed = true
		model.state.StreamHealth = liveconsole.StreamHealth{
			State: liveconsole.HealthDisconnected, Reason: typed.Reason,
		}
		model.syncConfigModal()
		model.syncLifecycleModal()
		return model, nil
	case ActivityLoadedMsg:
		if typed.requestID != model.activityRequest ||
			typed.sessionID != model.SelectedSession() ||
			typed.tab != model.activityTab {
			return model, nil
		}
		model.activityData = typed.data
		model.activityLoaded = true
		model.activityLoading = false
		model.activityError = ""
		model.clampActivitySelection()
		if model.activityDirty {
			model.activityDirty = false
			return model, model.requestActivity()
		}
		return model, nil
	case ActivityFailedMsg:
		if typed.requestID != model.activityRequest ||
			typed.sessionID != model.SelectedSession() ||
			typed.tab != model.activityTab {
			return model, nil
		}
		model.activityLoading = false
		model.activityError = activityErrorMessage(typed.err)
		if model.activityDirty {
			model.activityDirty = false
			return model, model.requestActivity()
		}
		return model, nil
	case tea.KeyPressMsg:
		return model.updateKey(typed)
	default:
		if model.TopModal() == ModalConfig && model.configModal != nil {
			return model.updateConfigModal(message)
		}
		if model.TopModal() == ModalLifecycle &&
			model.lifecycleModal != nil {
			return model.updateLifecycleModal(message)
		}
		return model, model.widgets.Update(message, string(model.focus))
	}
}

func (model *Model) View() tea.View {
	state := model.stateForSelectedSession()
	options := tuirender.Options{
		Width: model.width, Height: model.height,
		Unicode: model.unicode, NoColor: model.noColor,
	}
	var content string
	switch model.view {
	case ViewActivity:
		content = tuirender.Activity(model.activityRenderInput(state), options)
	case ViewConfig:
		content = tuirender.Config(tuirender.ConfigInput{
			State: model.state, Selected: model.configSelected,
		}, options)
	case ViewOperations:
		content = tuirender.Operations(tuirender.OperationsInput{
			State:      model.state,
			Selected:   model.operationsSelected,
			DetailOpen: model.detailOpen,
			LookupID:   model.operationLookupID,
		}, options)
	default:
		content = tuirender.Overview(tuirender.OverviewInput{
			State: state, Now: model.now(),
		}, options)
	}
	if len(model.modals) > 0 {
		switch model.TopModal() {
		case ModalHelp:
			contextName, commandIDs := model.helpContext()
			content = tuirender.Help(tuirender.HelpInput{
				Catalog:    model.helpCatalog,
				Context:    contextName,
				CommandIDs: commandIDs,
			}, options)
		case ModalConfig:
			if model.configModal != nil {
				// A modal replaces the body so it remains visible even when
				// the underlying Config list fills the terminal height.
				content = "Hideout · Config\n\n" +
					model.configModal.View(model.width)
			}
		case ModalLifecycle:
			if model.lifecycleModal != nil {
				content = "Hideout · Environments\n\n" +
					model.lifecycleModal.View(model.width)
			}
		}
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "Hideout"
	return view
}

func (model *Model) helpContext() (string, []string) {
	if len(model.modals) > 1 {
		switch model.modals[len(model.modals)-2] {
		case ModalConfig:
			return "Configuration", []string{"connect", "secret", "profile"}
		case ModalLifecycle:
			return "Environments", []string{"env", "stop", "clean"}
		}
	}
	switch model.view {
	case ViewActivity:
		return "Activity", []string{"activity", "audit"}
	case ViewConfig:
		return "Configuration", []string{"connect", "secret", "profile"}
	case ViewOperations:
		return "Operations", []string{"decision", "hostfs", "daemon"}
	case ViewHelp:
		return "Overview", []string{"setup", "doctor", "run", "help"}
	default:
		return "Overview", []string{"run", "tui", "env", "stop"}
	}
}

func (model *Model) updateKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	if key == "ctrl+c" {
		if model.configModal != nil {
			model.configModal.Clear()
		}
		if model.lifecycleModal != nil {
			model.lifecycleModal.Clear()
		}
		return model, tea.Quit
	}
	if len(model.modals) > 0 {
		switch model.TopModal() {
		case ModalHelp:
			if key == "esc" {
				model.modals = model.modals[:len(model.modals)-1]
			}
			return model, nil
		case ModalConfig:
			if key == "?" {
				model.modals = append(model.modals, ModalHelp)
				return model, nil
			}
			return model.updateConfigModal(message)
		case ModalLifecycle:
			if key == "?" {
				model.modals = append(model.modals, ModalHelp)
				return model, nil
			}
			return model.updateLifecycleModal(message)
		}
		return model, nil
	}
	if model.focus == FocusFilter {
		switch key {
		case "esc":
			model.widgets.Filter.SetValue(model.activityFilter)
			model.widgets.Filter.Blur()
			model.focus = FocusPrimary
			return model, nil
		case "enter":
			model.activityFilter = model.widgets.Filter.Value()
			model.activitySelected = 0
			model.detailOpen = false
			model.widgets.Filter.Blur()
			model.focus = FocusPrimary
			return model, nil
		}
		return model, model.widgets.Update(message, string(model.focus))
	}
	switch key {
	case "q":
		return model, tea.Quit
	case "?":
		model.modals = append(model.modals, ModalHelp)
	case "tab":
		model.focus = nextFocus(model.focus)
	case "shift+tab":
		model.focus = previousFocus(model.focus)
	case "j", "down":
		if model.view == ViewActivity {
			model.moveActivitySelection(1)
		} else if model.view == ViewConfig {
			model.moveConfigSelection(1)
		} else if model.view == ViewOperations {
			model.moveOperationsSelection(1)
		} else {
			model.selector.Move(1)
		}
		model.detailOpen = false
	case "k", "up":
		if model.view == ViewActivity {
			model.moveActivitySelection(-1)
		} else if model.view == ViewConfig {
			model.moveConfigSelection(-1)
		} else if model.view == ViewOperations {
			model.moveOperationsSelection(-1)
		} else {
			model.selector.Move(-1)
		}
		model.detailOpen = false
	case "left", "h":
		if model.view == ViewActivity {
			model.moveActivityTab(-1)
			return model, model.requestActivity()
		}
	case "right", "l":
		if model.view == ViewActivity {
			model.moveActivityTab(1)
			return model, model.requestActivity()
		}
	case "enter":
		if model.view == ViewConfig {
			model.openConfigModal()
		} else if model.view == ViewActivity {
			if len(tuirender.ActivityRows(model.activityRenderInput(
				model.stateForSelectedSession(),
			))) > 0 {
				model.detailOpen = true
			}
		} else if model.view == ViewOperations {
			if len(tuirender.OperationRows(
				model.operationsRenderInput(),
			)) != 0 {
				model.detailOpen = true
			}
		} else if model.SelectedSession() != "" {
			model.detailOpen = true
		}
	case "/":
		if model.view != ViewActivity {
			return model, nil
		}
		model.widgets.Filter.SetValue(model.activityFilter)
		model.focus = FocusFilter
		return model, model.widgets.Filter.Focus()
	case "r":
		if model.view == ViewActivity {
			return model, model.requestActivity()
		}
	case "e":
		if model.view == ViewOverview {
			model.openLifecycleModal()
		}
	case "1":
		model.setView(ViewOverview)
	case "2":
		model.setView(ViewActivity)
		return model, model.requestActivity()
	case "3":
		model.setView(ViewConfig)
		model.clampConfigSelection()
	case "4":
		model.setView(ViewOperations)
		model.clampOperationsSelection()
	case "5":
		model.setView(ViewHelp)
		model.modals = append(model.modals, ModalHelp)
	}
	return model, nil
}

func (model *Model) waitForEvent() tea.Cmd {
	if model.events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-model.events
		if !ok {
			return StreamClosedMsg{Reason: "daemon event stream closed"}
		}
		return EventMsg{Event: event}
	}
}

func (model *Model) frameTick() tea.Cmd {
	return tea.Tick(model.frameInterval, func(at time.Time) tea.Msg {
		return FrameTickMsg{At: at}
	})
}

func (model *Model) requestActivity() tea.Cmd {
	model.activityRequest++
	requestID := model.activityRequest
	sessionID := model.SelectedSession()
	tab := model.activityTab
	owner, ok := activityOwnerForSession(model.state, sessionID)
	if !ok {
		model.activityLoading = false
		model.activityLoaded = false
		model.activityData = tuirender.ActivityData{}
		model.activityError = "exact workload owner is not present in the authoritative snapshot"
		return nil
	}
	if model.activityProvider == nil {
		model.activityLoading = false
		model.activityLoaded = false
		model.activityData = tuirender.ActivityData{}
		model.activityError = "authoritative Manager activity queries are unavailable"
		return nil
	}
	model.activityLoading = true
	model.activityError = ""
	provider := model.activityProvider
	ctx := model.activityContext
	return func() tea.Msg {
		data, err := queryActivity(
			ctx, provider, owner, sessionID, tab,
		)
		if err != nil {
			return ActivityFailedMsg{
				requestID: requestID,
				sessionID: sessionID,
				tab:       tab,
				err:       err,
			}
		}
		return ActivityLoadedMsg{
			requestID: requestID,
			sessionID: sessionID,
			tab:       tab,
			data:      data,
		}
	}
}

func queryActivity(
	ctx context.Context,
	provider manager.ActivityProvider,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	tab ActivityTab,
) (tuirender.ActivityData, error) {
	if ctx == nil || provider == nil || owner.Validate() != nil ||
		(manager.ActivityOwnerSelector{SessionID: sessionID}).Validate() != nil ||
		(owner.Kind == workloadtypes.OwnerDisposableSession &&
			owner.SessionID != sessionID) {
		return tuirender.ActivityData{}, manager.ErrActivityQueryInvalid
	}
	if err := ctx.Err(); err != nil {
		return tuirender.ActivityData{}, err
	}
	risks, err := provider.ActivityRisks(
		ctx,
		manager.ActivityRisksQuery{Owner: owner, SessionID: sessionID},
	)
	if err != nil {
		return tuirender.ActivityData{}, err
	}
	summary, err := provider.ActivitySummary(
		ctx,
		manager.ActivitySummaryQuery{Owner: owner, SessionID: sessionID},
	)
	if err != nil {
		return tuirender.ActivityData{}, err
	}
	eventQuery := manager.ActivityEventsQuery{
		Owner: owner, SessionID: sessionID,
		Limit: workloadquery.MaximumLimit, Kinds: activityKindsForTab(tab),
	}
	if tab == ActivityTabRisks {
		eventQuery.Risks = activityRiskIDs(risks)
	}
	events, err := provider.ActivityEvents(ctx, eventQuery)
	if err != nil {
		return tuirender.ActivityData{}, err
	}
	executions, err := provider.ActivityExecutions(
		ctx,
		manager.ActivityExecutionsQuery{Owner: owner, SessionID: sessionID},
	)
	if err != nil {
		return tuirender.ActivityData{}, err
	}
	coverage, err := provider.ActivityCoverage(
		ctx,
		manager.ActivityCoverageQuery{Owner: owner, SessionID: sessionID},
	)
	if err != nil {
		return tuirender.ActivityData{}, err
	}
	data := tuirender.ActivityData{
		Owner: owner, Summary: summary, Events: events,
		Executions: executions, Coverage: coverage, Risks: risks,
	}
	if err := validateActivityData(data); err != nil {
		return tuirender.ActivityData{}, err
	}
	return data, nil
}

func activityKindsForTab(tab ActivityTab) []string {
	switch tab {
	case ActivityTabCommands:
		return []string{workloadtypes.ActivityProcess}
	case ActivityTabFiles:
		return []string{workloadtypes.ActivityFile}
	case ActivityTabNetwork:
		return []string{workloadtypes.ActivityConnection}
	case ActivityTabDNS:
		return []string{workloadtypes.ActivityDNS}
	default:
		return nil
	}
}

func activityRiskIDs(result manager.ActivityRisksResult) []string {
	values := make([]string, 0, min(len(result.Findings), 128))
	for _, finding := range result.Findings {
		values = append(values, finding.ID)
	}
	sort.Strings(values)
	if len(values) > 128 {
		values = values[:128]
	}
	return values
}

func validateActivityData(data tuirender.ActivityData) error {
	owner := data.Owner
	if owner.Validate() != nil || !data.Summary.Owner.Equal(owner) {
		return errors.Join(
			workloadquery.ErrInvalidSnapshot,
			errors.New("activity summary owner mismatch"),
		)
	}
	for kind := range data.Summary.Counts {
		if !validActivityKind(kind) {
			return errors.Join(
				workloadquery.ErrInvalidSnapshot,
				errors.New("activity summary kind is invalid"),
			)
		}
	}
	if len(data.Summary.HighestRisks) > manager.DefaultActivityHighestRiskLimit {
		return errors.Join(
			workloadquery.ErrInvalidSnapshot,
			errors.New("activity summary risk list is unbounded"),
		)
	}
	for _, record := range data.Events.Records {
		if record.ValidatePersistable() != nil || !record.Owner.Equal(owner) {
			return errors.Join(
				workloadquery.ErrInvalidSnapshot,
				errors.New("activity event failed exact-owner validation"),
			)
		}
	}
	if data.Events.NextCursor != "" && !data.Events.QueryTruncated {
		return errors.Join(
			workloadquery.ErrInvalidSnapshot,
			errors.New("activity cursor was returned without truncation"),
		)
	}
	for _, values := range [][]workloadtypes.CoverageInterval{
		data.Summary.CurrentCoverage,
		data.Events.Coverage,
		data.Executions.Coverage,
		data.Coverage.Intervals,
		data.Coverage.Current,
	} {
		if err := validateActivityCoverage(owner, values); err != nil {
			return err
		}
	}
	for _, findings := range [][]workloadrisk.Finding{
		data.Summary.HighestRisks,
		data.Risks.Findings,
	} {
		for _, finding := range findings {
			if finding.Validate() != nil || !finding.Owner.Equal(owner) {
				return errors.Join(
					workloadquery.ErrInvalidSnapshot,
					errors.New("activity risk failed exact-owner validation"),
				)
			}
		}
	}
	seen := make(map[string]struct{})
	for _, root := range data.Executions.Roots {
		if err := validateActivityExecutionNode(root, owner, "", seen, 1); err != nil {
			return err
		}
	}
	return nil
}

func validateActivityCoverage(
	owner workloadtypes.ActivityOwner,
	values []workloadtypes.CoverageInterval,
) error {
	for _, interval := range values {
		if interval.Validate() != nil || !interval.Owner.Equal(owner) {
			return errors.Join(
				workloadquery.ErrInvalidSnapshot,
				errors.New("activity coverage failed exact-owner validation"),
			)
		}
	}
	return nil
}

func validateActivityExecutionNode(
	node manager.ActivityExecutionNode,
	owner workloadtypes.ActivityOwner,
	parentID string,
	seen map[string]struct{},
	depth int,
) error {
	if depth > 1024 ||
		node.Execution.Validate() != nil ||
		!node.Execution.Owner.Equal(owner) ||
		(parentID != "" && node.Execution.ParentExecutionID != parentID) {
		return errors.Join(
			workloadquery.ErrInvalidSnapshot,
			errors.New("activity execution tree is invalid"),
		)
	}
	if _, exists := seen[node.Execution.ID]; exists {
		return errors.Join(
			workloadquery.ErrInvalidSnapshot,
			errors.New("activity execution tree contains a duplicate"),
		)
	}
	seen[node.Execution.ID] = struct{}{}
	for kind := range node.ActivityCounts {
		if !validActivityKind(kind) {
			return errors.Join(
				workloadquery.ErrInvalidSnapshot,
				errors.New("activity execution count kind is invalid"),
			)
		}
	}
	for _, child := range node.Children {
		if err := validateActivityExecutionNode(
			child,
			owner,
			node.Execution.ID,
			seen,
			depth+1,
		); err != nil {
			return err
		}
	}
	return nil
}

func validActivityKind(kind string) bool {
	switch kind {
	case workloadtypes.ActivityProcess, workloadtypes.ActivityFile,
		workloadtypes.ActivityConnection, workloadtypes.ActivityDNS,
		workloadtypes.ActivityRisk:
		return true
	default:
		return false
	}
}

func activityOwnerForSession(
	state liveconsole.State,
	sessionID string,
) (workloadtypes.ActivityOwner, bool) {
	if sessionID == "" {
		return workloadtypes.ActivityOwner{}, false
	}
	var (
		owner workloadtypes.ActivityOwner
		found bool
	)
	observe := func(candidate workloadtypes.ActivityOwner, candidateSession string) bool {
		if candidateSession != sessionID || candidate.Validate() != nil {
			return true
		}
		if !found {
			owner = candidate
			found = true
			return true
		}
		return owner.Equal(candidate)
	}
	for _, interval := range state.Coverage {
		if !observe(interval.Owner, interval.SessionID) {
			return workloadtypes.ActivityOwner{}, false
		}
	}
	for _, record := range state.Activity.Recent {
		if !observe(record.Owner, record.SessionID) {
			return workloadtypes.ActivityOwner{}, false
		}
	}
	return owner, found
}

func (model *Model) activityRenderInput(state liveconsole.State) tuirender.ActivityInput {
	filter := model.activityFilter
	if model.focus == FocusFilter {
		filter = model.widgets.Filter.Value()
	}
	return tuirender.ActivityInput{
		State: state, SessionID: model.SelectedSession(),
		Tab: string(model.activityTab), Filter: filter,
		Data: model.activityData, Selected: model.activitySelected,
		DetailOpen: model.detailOpen, Loaded: model.activityLoaded,
		Loading: model.activityLoading, Error: model.activityError,
		Now: model.now(),
	}
}

func (model *Model) moveActivityTab(delta int) {
	if delta == 0 {
		return
	}
	current := 0
	for index, tab := range activityTabOrder {
		if tab == model.activityTab {
			current = index
			break
		}
	}
	current = (current + delta) % len(activityTabOrder)
	if current < 0 {
		current += len(activityTabOrder)
	}
	model.activityTab = activityTabOrder[current]
	model.activitySelected = 0
	model.detailOpen = false
	model.activityError = ""
}

func (model *Model) moveActivitySelection(delta int) {
	rows := tuirender.ActivityRows(model.activityRenderInput(
		model.stateForSelectedSession(),
	))
	if len(rows) == 0 || delta == 0 {
		return
	}
	model.activitySelected = (model.activitySelected + delta) % len(rows)
	if model.activitySelected < 0 {
		model.activitySelected += len(rows)
	}
}

func (model *Model) clampActivitySelection() {
	rows := tuirender.ActivityRows(model.activityRenderInput(
		model.stateForSelectedSession(),
	))
	if len(rows) == 0 {
		model.activitySelected = 0
		return
	}
	if model.activitySelected >= len(rows) {
		model.activitySelected = len(rows) - 1
	}
	if model.activitySelected < 0 {
		model.activitySelected = 0
	}
}

func (model *Model) clearActivityForScope() {
	model.activityRequest++
	model.activityData = tuirender.ActivityData{}
	model.activitySelected = 0
	model.activityLoaded = false
	model.activityLoading = false
	model.activityError = ""
	model.activityDirty = false
}

func (model *Model) moveConfigSelection(delta int) {
	rows := tuirender.ConfigRows(model.state)
	if len(rows) == 0 || delta == 0 {
		model.configSelected = 0
		return
	}
	model.configSelected = (model.configSelected + delta) % len(rows)
	if model.configSelected < 0 {
		model.configSelected += len(rows)
	}
}

func (model *Model) operationsRenderInput() tuirender.OperationsInput {
	return tuirender.OperationsInput{
		State:      model.state,
		Selected:   model.operationsSelected,
		DetailOpen: model.detailOpen,
		LookupID:   model.operationLookupID,
	}
}

func (model *Model) moveOperationsSelection(delta int) {
	rows := tuirender.OperationRows(model.operationsRenderInput())
	if len(rows) == 0 || delta == 0 {
		model.operationsSelected = 0
		return
	}
	model.operationLookupID = ""
	model.operationsSelected =
		(model.operationsSelected + delta) % len(rows)
	if model.operationsSelected < 0 {
		model.operationsSelected += len(rows)
	}
}

func (model *Model) clampOperationsSelection() {
	rows := tuirender.OperationRows(model.operationsRenderInput())
	if model.operationLookupID != "" {
		for index, row := range rows {
			if row.ID == model.operationLookupID {
				model.operationsSelected = index
				model.detailOpen = true
				return
			}
		}
	}
	switch {
	case len(rows) == 0:
		model.operationsSelected = 0
	case model.operationsSelected >= len(rows):
		model.operationsSelected = len(rows) - 1
	case model.operationsSelected < 0:
		model.operationsSelected = 0
	}
}

func (model *Model) clampConfigSelection() {
	rows := tuirender.ConfigRows(model.state)
	switch {
	case len(rows) == 0:
		model.configSelected = 0
	case model.configSelected >= len(rows):
		model.configSelected = len(rows) - 1
	case model.configSelected < 0:
		model.configSelected = 0
	}
}

func (model *Model) openConfigModal() {
	rows := tuirender.ConfigRows(model.state)
	if len(rows) == 0 || model.configProvider == nil {
		return
	}
	model.clampConfigSelection()
	row := rows[model.configSelected]
	if !row.Editable {
		return
	}
	projection, ok := profileProjectionForConfig(model.state)
	if !ok {
		return
	}
	model.configModal = tuimodal.NewConfig(tuimodal.ConfigOptions{
		Context:    model.activityContext,
		Provider:   model.configProvider,
		Projection: projection,
		EditorID:   row.EditorID,
		Mutable:    model.MutationEnabled(),
		Now:        model.now,
	})
	model.modals = append(model.modals, ModalConfig)
}

func (model *Model) updateConfigModal(
	message tea.Msg,
) (tea.Model, tea.Cmd) {
	if model.configModal == nil {
		if model.TopModal() == ModalConfig {
			model.modals = model.modals[:len(model.modals)-1]
		}
		return model, nil
	}
	operationID := model.configModal.OperationID()
	command, outcome := model.configModal.Update(message)
	if outcome.Projection != nil {
		model.upsertProfileProjection(*outcome.Projection)
	}
	if outcome.Operation != nil {
		model.upsertOperation(*outcome.Operation)
		operationID = outcome.Operation.ID
	}
	if outcome.Close {
		if operationID != "" {
			model.operationLookupID = operationID
			model.clampOperationsSelection()
		}
		model.configModal.Clear()
		model.configModal = nil
		if model.TopModal() == ModalConfig {
			model.modals = model.modals[:len(model.modals)-1]
		}
	}
	return model, command
}

func (model *Model) syncConfigModal() {
	if model.configModal == nil {
		return
	}
	projection, ok := profileProjectionForConfig(model.state)
	reason := model.state.StreamHealth.Reason
	mutable := ok && model.MutationEnabled() &&
		model.configProvider != nil
	if !ok {
		reason = "authoritative profile projection is unavailable"
	}
	model.configModal.SyncAuthority(mutable, projection, reason)
	operationID := model.configModal.OperationID()
	if operationID == "" {
		return
	}
	for _, operation := range model.state.Operations {
		if operation.ID == operationID {
			model.configModal.ObserveOperation(operation)
			return
		}
	}
}

func (model *Model) openLifecycleModal() {
	if model.lifecycleProvider == nil ||
		!model.MutationEnabled() ||
		len(model.state.Overview.Environments) == 0 {
		return
	}
	model.lifecycleModal = tuimodal.NewLifecycle(
		tuimodal.LifecycleOptions{
			Context:               model.activityContext,
			Provider:              model.lifecycleProvider,
			Environments:          model.state.Overview.Environments,
			Sessions:              model.state.Overview.Sessions,
			SelectedEnvironmentID: model.selectedEnvironmentID(),
			Mutable:               model.MutationEnabled(),
		},
	)
	model.modals = append(model.modals, ModalLifecycle)
}

func (model *Model) updateLifecycleModal(
	message tea.Msg,
) (tea.Model, tea.Cmd) {
	if model.lifecycleModal == nil {
		if model.TopModal() == ModalLifecycle {
			model.modals = model.modals[:len(model.modals)-1]
		}
		return model, nil
	}
	command, outcome := model.lifecycleModal.Update(message)
	if outcome.Result != nil {
		model.applyLifecycleResult(*outcome.Result)
	}
	if outcome.Close {
		model.lifecycleModal.Clear()
		model.lifecycleModal = nil
		if model.TopModal() == ModalLifecycle {
			model.modals = model.modals[:len(model.modals)-1]
		}
	}
	return model, command
}

func (model *Model) syncLifecycleModal() {
	if model.lifecycleModal == nil {
		return
	}
	model.lifecycleModal.SyncAuthority(
		model.MutationEnabled() &&
			model.lifecycleProvider != nil,
		model.state.Overview.Environments,
		model.state.Overview.Sessions,
		model.state.StreamHealth.Reason,
	)
}

func (model *Model) selectedEnvironmentID() string {
	selectedSession := model.SelectedSession()
	if selectedSession == "" {
		return ""
	}
	for _, value := range model.state.Overview.Sessions {
		if value.ID == selectedSession {
			return value.EnvironmentID
		}
	}
	return ""
}

func (model *Model) applyLifecycleResult(
	result manager.EnvironmentActionResult,
) {
	applied := make(map[string]manager.EnvironmentActionTarget)
	for _, target := range result.Applied {
		applied[target.ID] = target
	}
	if len(applied) == 0 {
		return
	}
	if result.Plan.Action == manager.EnvironmentActionClean {
		environments := model.state.Overview.Environments[:0]
		for _, value := range model.state.Overview.Environments {
			if _, removed := applied[value.ID]; removed {
				continue
			}
			environments = append(environments, value)
		}
		model.state.Overview.Environments = environments
	} else if result.Plan.Action == manager.EnvironmentActionStop {
		for index := range model.state.Overview.Environments {
			environmentID :=
				model.state.Overview.Environments[index].ID
			target, ok := applied[environmentID]
			if !ok {
				continue
			}
			status := target.Status
			if status == "" {
				status = "stopped"
			}
			model.state.Overview.Environments[index].Status = status
			model.state.Overview.Environments[index].ActiveSessions = 0
			model.state.Overview.Environments[index].OwnerHealth = ""
		}
	}
	sessions := model.state.Overview.Sessions[:0]
	for _, value := range model.state.Overview.Sessions {
		if _, changed := applied[value.EnvironmentID]; changed {
			continue
		}
		sessions = append(sessions, value)
	}
	model.state.Overview.Sessions = sessions
	model.selector.Replace(
		model.state.Overview.Sessions,
		model.selector.Selected(),
	)
}

func profileProjectionForConfig(
	state liveconsole.State,
) (manager.ProfileProjection, bool) {
	profileName := state.ProfileScope
	if profileName == "" && len(state.Overview.Sessions) != 0 {
		profileName = state.Overview.Sessions[0].Profile
	}
	if profileName != "" {
		for _, projection := range state.Profiles {
			if projection.Profile == profileName {
				return projection, true
			}
		}
	}
	if len(state.Profiles) == 0 {
		return manager.ProfileProjection{}, false
	}
	return state.Profiles[0], true
}

func (model *Model) upsertProfileProjection(
	projection manager.ProfileProjection,
) {
	for index := range model.state.Profiles {
		if model.state.Profiles[index].Profile == projection.Profile {
			model.state.Profiles[index] = projection
			return
		}
	}
	model.state.Profiles = append(model.state.Profiles, projection)
	sort.Slice(model.state.Profiles, func(left, right int) bool {
		return model.state.Profiles[left].Profile <
			model.state.Profiles[right].Profile
	})
}

func (model *Model) upsertOperation(operation manager.Operation) {
	for index := range model.state.Operations {
		if model.state.Operations[index].ID == operation.ID {
			model.state.Operations[index] = operation
			model.clampOperationsSelection()
			return
		}
	}
	model.state.Operations = append(
		[]manager.Operation{operation},
		model.state.Operations...,
	)
	if len(model.state.Operations) > 500 {
		model.state.Operations = model.state.Operations[:500]
	}
	model.clampOperationsSelection()
}

func activityErrorMessage(err error) string {
	switch {
	case err == nil:
		return "authoritative Manager activity query failed"
	case errors.Is(err, context.Canceled):
		return "activity refresh was cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "activity refresh timed out"
	case errors.Is(err, manager.ErrActivityOwnerNotFound):
		return "the exact workload activity owner is no longer retained"
	case errors.Is(err, manager.ErrActivityCursorStale):
		return "activity history changed; refresh from the newest cursor"
	case errors.Is(err, workloadquery.ErrInvalidSnapshot):
		return "Manager returned an invalid exact-owner activity response"
	default:
		return "authoritative Manager activity query failed"
	}
}

func (model *Model) stateForSelectedSession() liveconsole.State {
	selected := model.SelectedSession()
	if selected == "" || len(model.state.Overview.Sessions) < 2 {
		return model.state
	}
	state := model.state
	state.Overview.Sessions = append([]manager.SessionSummary(nil), model.state.Overview.Sessions...)
	for index := range state.Overview.Sessions {
		if state.Overview.Sessions[index].ID != selected {
			continue
		}
		state.Overview.Sessions[0], state.Overview.Sessions[index] =
			state.Overview.Sessions[index], state.Overview.Sessions[0]
		break
	}
	return state
}

func (model *Model) setView(view View) {
	model.view = view
	model.detailOpen = false
	if view == ViewActivity {
		model.activitySelected = 0
	}
}

func layoutForSize(width, height int) Layout {
	switch {
	case width < 48 || height < 12:
		return LayoutTooSmall
	case width < 80 || height < 20:
		return LayoutNarrow
	default:
		return LayoutNormal
	}
}

func nextFocus(focus Focus) Focus {
	switch focus {
	case FocusPrimary:
		return FocusDetails
	case FocusDetails:
		return FocusFooter
	default:
		return FocusPrimary
	}
}

func previousFocus(focus Focus) Focus {
	switch focus {
	case FocusPrimary:
		return FocusFooter
	case FocusFooter:
		return FocusDetails
	default:
		return FocusPrimary
	}
}

func (model *Model) Layout() Layout                  { return model.layout }
func (model *Model) ModalDepth() int                 { return len(model.modals) }
func (model *Model) Focus() Focus                    { return model.focus }
func (model *Model) DetailOpen() bool                { return model.detailOpen }
func (model *Model) ActiveView() View                { return model.view }
func (model *Model) PendingEvents() int              { return len(model.pending) }
func (model *Model) ConsoleState() liveconsole.State { return model.state }
func (model *Model) MutationEnabled() bool           { return model.state.CanMutate() }
func (model *Model) SelectedSession() string         { return model.selector.Selected() }
func (model *Model) ActivityTab() ActivityTab        { return model.activityTab }
func (model *Model) ActivityFilter() string          { return model.activityFilter }
func (model *Model) ActivityLoading() bool           { return model.activityLoading }
func (model *Model) ActivityError() string           { return model.activityError }
func (model *Model) ConfigSelected() int             { return model.configSelected }
func (model *Model) OperationsSelected() int         { return model.operationsSelected }
func (model *Model) OperationLookupID() string       { return model.operationLookupID }

func (model *Model) ConfigStage() tuimodal.Stage {
	if model.configModal == nil {
		return ""
	}
	return model.configModal.Stage()
}

func (model *Model) LifecycleStage() tuimodal.Stage {
	if model.lifecycleModal == nil {
		return ""
	}
	return model.lifecycleModal.Stage()
}

func (model *Model) TopModal() Modal {
	if len(model.modals) == 0 {
		return ""
	}
	return model.modals[len(model.modals)-1]
}
