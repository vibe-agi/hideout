package modal

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/secrets"
)

type MigrationWizardMode string

const (
	MigrationWizardExport MigrationWizardMode = "export"
	MigrationWizardImport MigrationWizardMode = "import"
)

type MigrationWizardStage string

const (
	MigrationWizardChoose          MigrationWizardStage = "choose"
	MigrationWizardExportSelect    MigrationWizardStage = "export-select"
	MigrationWizardExportPath      MigrationWizardStage = "export-path"
	MigrationWizardExportSecret    MigrationWizardStage = "export-secret"
	MigrationWizardExportConfirm   MigrationWizardStage = "export-secret-confirm"
	MigrationWizardImportPath      MigrationWizardStage = "import-path"
	MigrationWizardImportSecret    MigrationWizardStage = "import-secret"
	MigrationWizardUnlocking       MigrationWizardStage = "unlocking"
	MigrationWizardImportSelect    MigrationWizardStage = "import-select"
	MigrationWizardImportDecisions MigrationWizardStage = "import-decisions"
	MigrationWizardEditDecision    MigrationWizardStage = "edit-decision"
	MigrationWizardPlanning        MigrationWizardStage = "planning"
	MigrationWizardReview          MigrationWizardStage = "review"
	MigrationWizardConfirmApply    MigrationWizardStage = "confirm-apply"
	MigrationWizardApplying        MigrationWizardStage = "applying"
	MigrationWizardTerminal        MigrationWizardStage = "terminal"
	MigrationWizardStale           MigrationWizardStage = "stale"
	MigrationWizardError           MigrationWizardStage = "error"
)

type MigrationWizardOptions struct {
	Context      context.Context
	Provider     MigrationCreationProvider
	Environments []manager.EnvironmentSummary
	Mode         MigrationWizardMode
	Mutable      bool
	Reason       string
}

type MigrationWizardOutcome struct {
	Close  bool
	Result *manager.MigrationApplyResult
}

type migrationWizardEnvironment struct {
	Source      manager.MigrationBundleEnvironmentProjection
	ExportName  string
	ExportState string
	Selected    bool
	Destination string
	Policy      migration.GuestIdentityPolicy
}

type migrationWizardWorkspace struct {
	EnvironmentRef migration.OpaqueID
	Projection     manager.MigrationBundleWorkspaceProjection
	Decision       string
	Destination    string
}

type migrationWizardSecret struct {
	Projection  manager.MigrationBundleSecretProjection
	Decision    string
	Destination string
}

type migrationWizardAuthority struct {
	EnvironmentRef migration.OpaqueID
	Projection     manager.MigrationBundleAuthorityProjection
	Decision       string
	Destination    string
}

type migrationWizardDecisionRow struct {
	Kind  string
	Index int
	Label string
	Value string
}

type MigrationWizard struct {
	ctx      context.Context
	provider MigrationCreationProvider
	mutable  bool
	reason   string
	mode     MigrationWizardMode
	stage    MigrationWizardStage

	environments []migrationWizardEnvironment
	workspaces   []migrationWizardWorkspace
	secrets      []migrationWizardSecret
	authorities  []migrationWizardAuthority
	selected     int
	input        []rune
	secretInput  []byte
	firstSecret  []byte
	editKind     string
	editIndex    int
	requestID    uint64
	errorMessage string

	exportMode    migration.ExportMode
	exportPath    string
	exportSession *MigrationExportSession
	importPath    string
	importSession *MigrationImportSession
	importDraft   *migration.ImportDraft
	importPlan    *migration.ImportPlan
	result        *manager.MigrationApplyResult
}

type migrationWizardExportPlanMsg struct {
	requestID uint64
	session   MigrationExportSession
	err       error
}

type migrationWizardUnlockMsg struct {
	requestID uint64
	session   MigrationImportSession
	err       error
}

type migrationWizardImportPlanMsg struct {
	requestID uint64
	plan      migration.ImportPlan
	err       error
}

type migrationWizardApplyMsg struct {
	requestID uint64
	result    manager.MigrationApplyResult
	err       error
}

const maxMigrationWizardInputRunes = 4096

func NewMigrationWizard(options MigrationWizardOptions) *MigrationWizard {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	wizard := &MigrationWizard{
		ctx: ctx, provider: options.Provider, mutable: options.Mutable,
		reason: safeInline(options.Reason), mode: options.Mode,
		exportMode: migration.ExportModeConfig,
		stage:      MigrationWizardChoose,
		environments: make(
			[]migrationWizardEnvironment, 0, len(options.Environments),
		),
	}
	for _, value := range options.Environments {
		name := value.Name
		if name == "" {
			name = value.ID
		}
		wizard.environments = append(wizard.environments, migrationWizardEnvironment{
			ExportName: name, ExportState: value.Status,
		})
	}
	sort.Slice(wizard.environments, func(left, right int) bool {
		return wizard.environments[left].ExportName < wizard.environments[right].ExportName
	})
	switch options.Mode {
	case MigrationWizardExport:
		wizard.stage = MigrationWizardExportSelect
	case MigrationWizardImport:
		wizard.environments = nil
		wizard.stage = MigrationWizardImportPath
	}
	return wizard
}

func (wizard *MigrationWizard) Update(
	message tea.Msg,
) (tea.Cmd, MigrationWizardOutcome) {
	if wizard == nil {
		return nil, MigrationWizardOutcome{Close: true}
	}
	switch typed := message.(type) {
	case tea.KeyPressMsg:
		return wizard.updateKey(typed)
	case migrationWizardExportPlanMsg:
		return wizard.receiveExportPlan(typed)
	case migrationWizardUnlockMsg:
		return wizard.receiveImportUnlock(typed)
	case migrationWizardImportPlanMsg:
		return wizard.receiveImportPlan(typed)
	case migrationWizardApplyMsg:
		return wizard.receiveApply(typed)
	default:
		return nil, MigrationWizardOutcome{}
	}
}

func (wizard *MigrationWizard) updateKey(
	message tea.KeyPressMsg,
) (tea.Cmd, MigrationWizardOutcome) {
	key := message.String()
	if key == "esc" {
		wizard.requestID++
		wizard.Clear()
		return nil, MigrationWizardOutcome{Close: true}
	}
	switch wizard.stage {
	case MigrationWizardChoose:
		switch strings.ToLower(key) {
		case "e":
			wizard.mode = MigrationWizardExport
			wizard.stage = MigrationWizardExportSelect
		case "i":
			wizard.mode = MigrationWizardImport
			wizard.environments = nil
			wizard.stage = MigrationWizardImportPath
		}
	case MigrationWizardExportSelect:
		return wizard.updateExportSelection(key)
	case MigrationWizardExportPath:
		if key == "enter" {
			return wizard.acceptExportPath()
		}
		wizard.updateOrdinaryInput(message)
	case MigrationWizardExportSecret:
		if key == "enter" {
			if len(wizard.secretInput) == 0 {
				wizard.errorMessage = "Passphrase is required."
				return nil, MigrationWizardOutcome{}
			}
			wizard.firstSecret = append(wizard.firstSecret[:0], wizard.secretInput...)
			wizard.clearSecretInput()
			wizard.stage = MigrationWizardExportConfirm
			return nil, MigrationWizardOutcome{}
		}
		wizard.updateSecretInput(message)
	case MigrationWizardExportConfirm:
		if key == "enter" {
			return wizard.startExportPlan()
		}
		wizard.updateSecretInput(message)
	case MigrationWizardImportPath:
		if key == "enter" {
			return wizard.acceptImportPath()
		}
		wizard.updateOrdinaryInput(message)
	case MigrationWizardImportSecret:
		if key == "enter" {
			return wizard.startImportUnlock()
		}
		wizard.updateSecretInput(message)
	case MigrationWizardImportSelect:
		return wizard.updateImportSelection(key)
	case MigrationWizardImportDecisions:
		return wizard.updateImportDecisions(message)
	case MigrationWizardEditDecision:
		if key == "enter" {
			wizard.applyDecisionEdit()
			return nil, MigrationWizardOutcome{}
		}
		wizard.updateOrdinaryInput(message)
	case MigrationWizardReview:
		switch strings.ToLower(key) {
		case "a":
			if wizard.reviewBlocked() {
				wizard.errorMessage = "Resolve every displayed blocker before applying."
				return nil, MigrationWizardOutcome{}
			}
			wizard.clearOrdinaryInput()
			wizard.stage = MigrationWizardConfirmApply
		case "e":
			wizard.errorMessage = ""
			if wizard.mode == MigrationWizardExport {
				wizard.stage = MigrationWizardExportSelect
			} else {
				wizard.stage = MigrationWizardImportDecisions
			}
		}
	case MigrationWizardConfirmApply:
		if key == "enter" {
			return wizard.startApply()
		}
		wizard.updateOrdinaryInput(message)
	case MigrationWizardTerminal:
		if key == "enter" {
			var result *manager.MigrationApplyResult
			if wizard.result != nil {
				copy := *wizard.result
				result = &copy
			}
			wizard.Clear()
			return nil, MigrationWizardOutcome{Close: true, Result: result}
		}
	case MigrationWizardError:
		if strings.ToLower(key) == "r" {
			wizard.errorMessage = ""
			switch {
			case wizard.mode == MigrationWizardImport && wizard.importSession != nil:
				wizard.stage = MigrationWizardImportDecisions
			case wizard.mode == MigrationWizardImport:
				wizard.stage = MigrationWizardImportPath
			default:
				wizard.stage = MigrationWizardExportSelect
			}
		}
	}
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) updateExportSelection(
	key string,
) (tea.Cmd, MigrationWizardOutcome) {
	if len(wizard.environments) == 0 {
		wizard.errorMessage = "No environment is present in the verified snapshot."
		return nil, MigrationWizardOutcome{}
	}
	switch strings.ToLower(key) {
	case "j", "down":
		wizard.moveSelection(1, len(wizard.environments))
	case "k", "up":
		wizard.moveSelection(-1, len(wizard.environments))
	case " ", "space":
		wizard.environments[wizard.selected].Selected =
			!wizard.environments[wizard.selected].Selected
	case "a":
		all := true
		for _, value := range wizard.environments {
			all = all && value.Selected
		}
		for index := range wizard.environments {
			wizard.environments[index].Selected = !all
		}
	case "m":
		if wizard.exportMode == migration.ExportModeConfig {
			wizard.exportMode = migration.ExportModeFull
		} else {
			wizard.exportMode = migration.ExportModeConfig
		}
	case "enter":
		if len(wizard.selectedExportNames()) == 0 {
			wizard.errorMessage = "Select at least one environment."
			return nil, MigrationWizardOutcome{}
		}
		wizard.clearOrdinaryInput()
		wizard.stage = MigrationWizardExportPath
	}
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) acceptExportPath() (tea.Cmd, MigrationWizardOutcome) {
	path, err := filepath.Abs(strings.TrimSpace(string(wizard.input)))
	if err != nil || strings.TrimSpace(string(wizard.input)) == "" {
		wizard.errorMessage = "Enter a destination bundle file path."
		return nil, MigrationWizardOutcome{}
	}
	wizard.exportPath = filepath.Clean(path)
	wizard.clearOrdinaryInput()
	wizard.clearSecretInput()
	wizard.stage = MigrationWizardExportSecret
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) startExportPlan() (tea.Cmd, MigrationWizardOutcome) {
	if len(wizard.firstSecret) == 0 || len(wizard.secretInput) == 0 ||
		len(wizard.firstSecret) != len(wizard.secretInput) ||
		subtle.ConstantTimeCompare(wizard.firstSecret, wizard.secretInput) != 1 {
		wizard.clearSecretInput()
		clear(wizard.firstSecret)
		wizard.firstSecret = nil
		wizard.stage = MigrationWizardExportSecret
		wizard.errorMessage = "Passphrase confirmation did not match; enter it again."
		return nil, MigrationWizardOutcome{}
	}
	buffer, err := secrets.NewBuffer(wizard.firstSecret)
	clear(wizard.firstSecret)
	wizard.firstSecret = nil
	wizard.clearSecretInput()
	if err != nil {
		wizard.stage = MigrationWizardExportSecret
		wizard.errorMessage = "Passphrase is invalid or too long."
		return nil, MigrationWizardOutcome{}
	}
	request := migration.ExportRequest{
		Schema: manager.MigrationExportRequestSchema, Mode: wizard.exportMode,
		EnvironmentNames:  wizard.selectedExportNames(),
		IncludeSecretRefs: []string{}, OutputPath: wizard.exportPath,
		RiskAcknowledgements: []string{},
	}
	if wizard.exportMode == migration.ExportModeFull {
		request.RiskAcknowledgements = []string{manager.MigrationRiskOpaqueGuestContent}
	}
	if !wizard.canMutate() {
		buffer.Clear()
		wizard.stage = MigrationWizardStale
		return nil, MigrationWizardOutcome{}
	}
	wizard.requestID++
	requestID := wizard.requestID
	provider, ctx := wizard.provider, wizard.ctx
	wizard.stage = MigrationWizardPlanning
	wizard.errorMessage = ""
	return func() tea.Msg {
		var session MigrationExportSession
		invokeErr := buffer.Use(func(passphrase []byte) error {
			var err error
			session, err = provider.PlanMigrationExport(ctx, request, passphrase)
			return err
		})
		buffer.Clear()
		return migrationWizardExportPlanMsg{
			requestID: requestID, session: session, err: invokeErr,
		}
	}, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) receiveExportPlan(
	message migrationWizardExportPlanMsg,
) (tea.Cmd, MigrationWizardOutcome) {
	if message.requestID != wizard.requestID || wizard.stage != MigrationWizardPlanning {
		return nil, MigrationWizardOutcome{}
	}
	if message.err != nil || manager.VerifyMigrationExportPlan(message.session.Plan) != nil ||
		message.session.SecretInputHandle == "" {
		wizard.stage = MigrationWizardError
		wizard.errorMessage = migrationWizardError(message.err)
		return nil, MigrationWizardOutcome{}
	}
	wizard.exportSession = &message.session
	wizard.stage = MigrationWizardReview
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) acceptImportPath() (tea.Cmd, MigrationWizardOutcome) {
	raw := strings.TrimSpace(string(wizard.input))
	path, err := filepath.Abs(raw)
	if err != nil || raw == "" {
		wizard.errorMessage = "Enter an encrypted migration bundle path."
		return nil, MigrationWizardOutcome{}
	}
	wizard.importPath = filepath.Clean(path)
	wizard.clearOrdinaryInput()
	wizard.clearSecretInput()
	wizard.stage = MigrationWizardImportSecret
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) startImportUnlock() (tea.Cmd, MigrationWizardOutcome) {
	if len(wizard.secretInput) == 0 {
		wizard.errorMessage = "Passphrase is required."
		return nil, MigrationWizardOutcome{}
	}
	buffer, err := secrets.NewBuffer(wizard.secretInput)
	wizard.clearSecretInput()
	if err != nil {
		wizard.errorMessage = "Passphrase is invalid or too long."
		return nil, MigrationWizardOutcome{}
	}
	if !wizard.canMutate() {
		buffer.Clear()
		wizard.stage = MigrationWizardStale
		return nil, MigrationWizardOutcome{}
	}
	wizard.requestID++
	requestID := wizard.requestID
	provider, ctx, path := wizard.provider, wizard.ctx, wizard.importPath
	wizard.stage = MigrationWizardUnlocking
	wizard.errorMessage = ""
	return func() tea.Msg {
		var session MigrationImportSession
		invokeErr := buffer.Use(func(passphrase []byte) error {
			var err error
			session, err = provider.UnlockMigrationImport(ctx, path, passphrase)
			return err
		})
		buffer.Clear()
		return migrationWizardUnlockMsg{
			requestID: requestID, session: session, err: invokeErr,
		}
	}, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) receiveImportUnlock(
	message migrationWizardUnlockMsg,
) (tea.Cmd, MigrationWizardOutcome) {
	if message.requestID != wizard.requestID || wizard.stage != MigrationWizardUnlocking {
		return nil, MigrationWizardOutcome{}
	}
	if message.err != nil || message.session.Inspection.Inventory.Validate() != nil ||
		message.session.SecretInputHandle == "" {
		wizard.stage = MigrationWizardError
		wizard.errorMessage = migrationWizardError(message.err)
		return nil, MigrationWizardOutcome{}
	}
	wizard.importSession = &message.session
	wizard.initializeImportChoices(message.session.Inspection)
	wizard.selected = 0
	wizard.stage = MigrationWizardImportSelect
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) initializeImportChoices(
	inspection manager.MigrationReadOnlyInspection,
) {
	wizard.environments = make(
		[]migrationWizardEnvironment, len(inspection.Inventory.Environments),
	)
	wizard.workspaces = nil
	wizard.authorities = nil
	authorityByID := make(
		map[migration.OpaqueID]manager.MigrationBundleAuthorityProjection,
		len(inspection.Inventory.AuthorityProposals),
	)
	for _, proposal := range inspection.Inventory.AuthorityProposals {
		authorityByID[proposal.ProposalID] = proposal
	}
	for index, value := range inspection.Inventory.Environments {
		wizard.environments[index] = migrationWizardEnvironment{
			Source: value, Destination: value.DisplayNameHint,
			Policy: migration.GuestIdentitySafeClone,
		}
		for _, proposal := range value.WorkspaceProposals {
			wizard.workspaces = append(wizard.workspaces, migrationWizardWorkspace{
				EnvironmentRef: value.SourceRef, Projection: proposal,
				Decision: "disabled",
			})
		}
		for _, proposalID := range value.AuthorityProposalIDs {
			proposal, ok := authorityByID[proposalID]
			if !ok {
				continue
			}
			wizard.authorities = append(wizard.authorities, migrationWizardAuthority{
				EnvironmentRef: value.SourceRef, Projection: proposal,
				Decision: "disabled",
			})
		}
	}
	wizard.secrets = make(
		[]migrationWizardSecret, len(inspection.Inventory.Secrets),
	)
	for index, value := range inspection.Inventory.Secrets {
		wizard.secrets[index] = migrationWizardSecret{
			Projection: value, Decision: "unresolved",
		}
	}
	sort.Slice(wizard.environments, func(left, right int) bool {
		return wizard.environments[left].Source.SourceRef <
			wizard.environments[right].Source.SourceRef
	})
}

func (wizard *MigrationWizard) updateImportSelection(
	key string,
) (tea.Cmd, MigrationWizardOutcome) {
	if len(wizard.environments) == 0 {
		wizard.errorMessage = "The authenticated bundle has no environment inventory."
		return nil, MigrationWizardOutcome{}
	}
	switch strings.ToLower(key) {
	case "j", "down":
		wizard.moveSelection(1, len(wizard.environments))
	case "k", "up":
		wizard.moveSelection(-1, len(wizard.environments))
	case " ", "space":
		wizard.environments[wizard.selected].Selected =
			!wizard.environments[wizard.selected].Selected
	case "a":
		all := true
		for _, value := range wizard.environments {
			all = all && value.Selected
		}
		for index := range wizard.environments {
			wizard.environments[index].Selected = !all
		}
	case "enter":
		if len(wizard.selectedImportRefs()) == 0 {
			wizard.errorMessage = "Select at least one environment from the authenticated inventory."
			return nil, MigrationWizardOutcome{}
		}
		wizard.selected = 0
		wizard.stage = MigrationWizardImportDecisions
	}
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) updateImportDecisions(
	message tea.KeyPressMsg,
) (tea.Cmd, MigrationWizardOutcome) {
	rows := wizard.decisionRows()
	if len(rows) == 0 {
		wizard.errorMessage = "No selected import decision is available."
		return nil, MigrationWizardOutcome{}
	}
	wizard.selected = clampWizardIndex(wizard.selected, len(rows))
	key := strings.ToLower(message.String())
	row := rows[wizard.selected]
	switch key {
	case "j", "down":
		wizard.moveSelection(1, len(rows))
	case "k", "up":
		wizard.moveSelection(-1, len(rows))
	case "enter":
		switch row.Kind {
		case "name", "workspace":
			wizard.beginDecisionEdit(row)
		case "identity":
			if wizard.environments[row.Index].Policy == migration.GuestIdentitySafeClone {
				wizard.environments[row.Index].Policy = migration.GuestIdentityExactRestore
			} else {
				wizard.environments[row.Index].Policy = migration.GuestIdentitySafeClone
			}
		}
	case "d":
		switch row.Kind {
		case "workspace":
			wizard.workspaces[row.Index].Decision = "disabled"
			wizard.workspaces[row.Index].Destination = ""
		case "secret":
			wizard.secrets[row.Index].Decision = "unresolved"
			wizard.secrets[row.Index].Destination = ""
		case "authority":
			wizard.authorities[row.Index].Decision = "disabled"
			wizard.authorities[row.Index].Destination = ""
		}
	case "e":
		if row.Kind == "secret" {
			wizard.secrets[row.Index].Decision = "existing-ref"
			wizard.beginDecisionEdit(row)
		}
	case "v":
		if row.Kind == "secret" {
			if !wizard.secrets[row.Index].Projection.ValueIncluded {
				wizard.errorMessage = "This bundle contains only a secret reference, not a transferable value."
				return nil, MigrationWizardOutcome{}
			}
			wizard.secrets[row.Index].Decision = "import-value"
			wizard.beginDecisionEdit(row)
		}
	case "a":
		if row.Kind == "authority" {
			wizard.authorities[row.Index].Decision = "approved"
			wizard.beginDecisionEdit(row)
		}
	case "p":
		return wizard.startImportPlan()
	}
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) beginDecisionEdit(row migrationWizardDecisionRow) {
	wizard.editKind, wizard.editIndex = row.Kind, row.Index
	wizard.clearOrdinaryInput()
	var current string
	switch row.Kind {
	case "name":
		current = wizard.environments[row.Index].Destination
	case "workspace":
		current = wizard.workspaces[row.Index].Destination
	case "secret":
		current = wizard.secrets[row.Index].Destination
	case "authority":
		current = wizard.authorities[row.Index].Destination
	}
	wizard.input = append(wizard.input, []rune(current)...)
	wizard.stage = MigrationWizardEditDecision
}

func (wizard *MigrationWizard) applyDecisionEdit() {
	value := strings.TrimSpace(string(wizard.input))
	switch wizard.editKind {
	case "name":
		if environment.ValidateName(value) != nil {
			wizard.errorMessage = "Destination name is invalid or reserved."
			return
		}
		wizard.environments[wizard.editIndex].Destination = value
	case "workspace":
		path, err := filepath.Abs(value)
		if err != nil || value == "" {
			wizard.errorMessage = "Enter an existing destination directory path, or press d to disable it."
			return
		}
		wizard.workspaces[wizard.editIndex].Decision = "mapped"
		wizard.workspaces[wizard.editIndex].Destination = filepath.Clean(path)
	case "secret":
		if secrets.ValidateRef(value) != nil {
			wizard.errorMessage = "Destination secret reference is invalid."
			return
		}
		wizard.secrets[wizard.editIndex].Destination = value
	case "authority":
		if value == "" || !json.Valid([]byte(value)) {
			wizard.errorMessage = "Approved destination authority must be valid JSON."
			return
		}
		wizard.authorities[wizard.editIndex].Destination = value
	}
	wizard.clearOrdinaryInput()
	wizard.editKind = ""
	wizard.errorMessage = ""
	wizard.stage = MigrationWizardImportDecisions
}

func (wizard *MigrationWizard) startImportPlan() (tea.Cmd, MigrationWizardOutcome) {
	if !wizard.canMutate() || wizard.importSession == nil {
		wizard.stage = MigrationWizardStale
		return nil, MigrationWizardOutcome{}
	}
	draft, err := wizard.buildImportDraft()
	if err != nil {
		wizard.errorMessage = safeInline(err.Error())
		return nil, MigrationWizardOutcome{}
	}
	wizard.requestID++
	requestID := wizard.requestID
	provider, ctx := wizard.provider, wizard.ctx
	handle := wizard.importSession.SecretInputHandle
	wizard.importDraft = &draft
	wizard.stage = MigrationWizardPlanning
	wizard.errorMessage = ""
	return func() tea.Msg {
		plan, err := provider.PlanMigrationImport(ctx, draft, handle)
		return migrationWizardImportPlanMsg{
			requestID: requestID, plan: plan, err: err,
		}
	}, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) receiveImportPlan(
	message migrationWizardImportPlanMsg,
) (tea.Cmd, MigrationWizardOutcome) {
	if message.requestID != wizard.requestID || wizard.stage != MigrationWizardPlanning {
		return nil, MigrationWizardOutcome{}
	}
	if message.err != nil || manager.VerifyMigrationImportPlan(message.plan) != nil {
		wizard.stage = MigrationWizardError
		wizard.errorMessage = migrationWizardError(message.err)
		return nil, MigrationWizardOutcome{}
	}
	wizard.importPlan = &message.plan
	wizard.stage = MigrationWizardReview
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) buildImportDraft() (migration.ImportDraft, error) {
	if wizard.importSession == nil {
		return migration.ImportDraft{}, errors.New("authenticated inventory is unavailable")
	}
	selected := wizard.selectedImportRefs()
	draft := migration.ImportDraft{
		Schema: manager.MigrationImportDraftSchema, BundlePath: wizard.importPath,
		BundleBinding:           wizard.importSession.Inspection.Binding,
		SelectedEnvironmentRefs: selected,
		NameMappings:            []migration.NameMapping{},
		ConflictDecisions:       []migration.ConflictDecision{},
		WorkspaceMappings:       []migration.WorkspaceMapping{},
		SecretMappings:          []migration.SecretMapping{},
		IdentityPolicies:        []migration.IdentitySelection{},
		AuthorityDecisions:      []migration.AuthorityDecision{},
		RiskAcknowledgements:    []string{},
	}
	selectedSet := make(map[migration.OpaqueID]bool, len(selected))
	hasExact := false
	for index := range wizard.environments {
		value := wizard.environments[index]
		if !value.Selected {
			continue
		}
		if environment.ValidateName(value.Destination) != nil {
			return migration.ImportDraft{}, fmt.Errorf(
				"edit destination name for %s before planning", value.Source.SourceRef,
			)
		}
		selectedSet[value.Source.SourceRef] = true
		draft.NameMappings = append(draft.NameMappings, migration.NameMapping{
			SourceRef: value.Source.SourceRef, DestinationName: value.Destination,
		})
		draft.IdentityPolicies = append(draft.IdentityPolicies, migration.IdentitySelection{
			SourceRef: value.Source.SourceRef, Policy: value.Policy,
		})
		hasExact = hasExact || value.Policy == migration.GuestIdentityExactRestore
	}
	for _, value := range wizard.workspaces {
		if !selectedSet[value.EnvironmentRef] {
			continue
		}
		draft.WorkspaceMappings = append(draft.WorkspaceMappings, migration.WorkspaceMapping{
			ProposalID: value.Projection.ProposalID, Decision: value.Decision,
			DestinationPath: value.Destination,
		})
	}
	hasImportedSecret := false
	for _, value := range wizard.secrets {
		draft.SecretMappings = append(draft.SecretMappings, migration.SecretMapping{
			SourceRef: value.Projection.SecretRef, Decision: value.Decision,
			DestinationRef: value.Destination,
		})
		hasImportedSecret = hasImportedSecret || value.Decision == "import-value"
	}
	for _, value := range wizard.authorities {
		if !selectedSet[value.EnvironmentRef] {
			continue
		}
		draft.AuthorityDecisions = append(draft.AuthorityDecisions, migration.AuthorityDecision{
			ProposalID: value.Projection.ProposalID, Decision: value.Decision,
			DestinationValue: value.Destination,
		})
	}
	if hasExact {
		draft.RiskAcknowledgements = append(
			draft.RiskAcknowledgements, manager.MigrationRiskExactGuestRestore,
		)
	}
	if hasImportedSecret {
		draft.RiskAcknowledgements = append(
			draft.RiskAcknowledgements, manager.MigrationRiskSelectedSecrets,
		)
	}
	sort.Slice(draft.NameMappings, func(left, right int) bool {
		return draft.NameMappings[left].SourceRef < draft.NameMappings[right].SourceRef
	})
	sort.Slice(draft.IdentityPolicies, func(left, right int) bool {
		return draft.IdentityPolicies[left].SourceRef < draft.IdentityPolicies[right].SourceRef
	})
	sort.Slice(draft.WorkspaceMappings, func(left, right int) bool {
		return draft.WorkspaceMappings[left].ProposalID < draft.WorkspaceMappings[right].ProposalID
	})
	sort.Slice(draft.SecretMappings, func(left, right int) bool {
		return draft.SecretMappings[left].SourceRef < draft.SecretMappings[right].SourceRef
	})
	sort.Slice(draft.AuthorityDecisions, func(left, right int) bool {
		return draft.AuthorityDecisions[left].ProposalID < draft.AuthorityDecisions[right].ProposalID
	})
	slices.Sort(draft.RiskAcknowledgements)
	return draft, nil
}

func (wizard *MigrationWizard) startApply() (tea.Cmd, MigrationWizardOutcome) {
	phrase := strings.ToUpper(string(wizard.mode))
	if string(wizard.input) != phrase {
		wizard.errorMessage = "Type " + phrase + " exactly to apply the reviewed plan."
		return nil, MigrationWizardOutcome{}
	}
	wizard.clearOrdinaryInput()
	if !wizard.canMutate() {
		wizard.stage = MigrationWizardStale
		return nil, MigrationWizardOutcome{}
	}
	wizard.requestID++
	requestID := wizard.requestID
	provider, ctx := wizard.provider, wizard.ctx
	wizard.stage = MigrationWizardApplying
	wizard.errorMessage = ""
	if wizard.mode == MigrationWizardExport && wizard.exportSession != nil {
		session := *wizard.exportSession
		return func() tea.Msg {
			result, err := provider.ApplyMigrationExport(ctx, session)
			return migrationWizardApplyMsg{requestID: requestID, result: result, err: err}
		}, MigrationWizardOutcome{}
	}
	if wizard.mode == MigrationWizardImport && wizard.importPlan != nil &&
		wizard.importSession != nil {
		plan := *wizard.importPlan
		handle := wizard.importSession.SecretInputHandle
		return func() tea.Msg {
			result, err := provider.ApplyMigrationImport(ctx, plan, handle)
			return migrationWizardApplyMsg{requestID: requestID, result: result, err: err}
		}, MigrationWizardOutcome{}
	}
	wizard.stage = MigrationWizardError
	wizard.errorMessage = "The reviewed migration plan is unavailable."
	return nil, MigrationWizardOutcome{}
}

func (wizard *MigrationWizard) receiveApply(
	message migrationWizardApplyMsg,
) (tea.Cmd, MigrationWizardOutcome) {
	if message.requestID != wizard.requestID || wizard.stage != MigrationWizardApplying {
		return nil, MigrationWizardOutcome{}
	}
	if message.err != nil || message.result.OperationID == "" || message.result.Next == "" {
		wizard.stage = MigrationWizardError
		wizard.errorMessage = migrationWizardError(message.err)
		return nil, MigrationWizardOutcome{}
	}
	wizard.result = &message.result
	wizard.stage = MigrationWizardTerminal
	copy := message.result
	return nil, MigrationWizardOutcome{Result: &copy}
}

func (wizard *MigrationWizard) View(width int) string {
	if wizard == nil {
		return "Migration wizard unavailable.\n"
	}
	lines := []string{"Guided migration"}
	switch wizard.stage {
	case MigrationWizardChoose:
		lines = append(lines,
			"Choose a task:",
			"  e Export this computer into one encrypted bundle",
			"  i Import an encrypted bundle on this computer",
			"", "e export · i import · Esc close",
		)
	case MigrationWizardExportSelect:
		lines = append(lines, wizard.exportSelectionLines()...)
	case MigrationWizardExportPath:
		lines = append(lines,
			"Export destination",
			"The bundle is one encrypted file; an existing path is refused.",
			"> "+safeInline(string(wizard.input)),
			"", "Enter continue · Esc cancel",
		)
	case MigrationWizardExportSecret, MigrationWizardExportConfirm:
		label := "Create bundle passphrase"
		if wizard.stage == MigrationWizardExportConfirm {
			label = "Confirm bundle passphrase"
		}
		masked := ""
		if len(wizard.secretInput) != 0 {
			masked = "••••••••"
		}
		lines = append(lines,
			label,
			"It is never stored in command history, environment variables, URLs, or UI output.",
			"> "+masked,
			"", "Enter continue · Esc clears and cancels",
		)
	case MigrationWizardImportPath:
		lines = append(lines,
			"Import bundle",
			"Enter the encrypted .hideout bundle file path.",
			"> "+safeInline(string(wizard.input)),
			"", "Enter unlock · Esc cancel",
		)
	case MigrationWizardImportSecret:
		masked := ""
		if len(wizard.secretInput) != 0 {
			masked = "••••••••"
		}
		lines = append(lines,
			"Unlock encrypted bundle",
			"The passphrase is exchanged immediately for one-shot in-memory Manager handles.",
			"> "+masked,
			"", "Enter inspect · Esc clears and cancels",
		)
	case MigrationWizardUnlocking:
		lines = append(lines,
			"Authenticating and inspecting the bundle…",
			"No destination state is being changed.",
		)
	case MigrationWizardImportSelect:
		lines = append(lines, wizard.importSelectionLines()...)
	case MigrationWizardImportDecisions:
		lines = append(lines, wizard.importDecisionLines()...)
	case MigrationWizardEditDecision:
		lines = append(lines, wizard.decisionEditLines()...)
	case MigrationWizardPlanning:
		lines = append(lines,
			"Building an immutable Manager plan…",
			"No migration effect has been accepted or started.",
		)
	case MigrationWizardReview:
		lines = append(lines, wizard.reviewLines()...)
	case MigrationWizardConfirmApply:
		phrase := strings.ToUpper(string(wizard.mode))
		lines = append(lines,
			"Confirm reviewed "+string(wizard.mode)+" plan",
			"Type "+phrase+" to apply this exact plan:",
			"> "+safeInline(string(wizard.input)),
			"", "Enter apply · Esc cancel",
		)
	case MigrationWizardApplying:
		lines = append(lines,
			"Submitting the exact reviewed plan…",
			"The operation will continue under its durable ID if this dialog closes.",
		)
	case MigrationWizardTerminal:
		if wizard.result == nil {
			lines = append(lines,
				"Migration result unavailable",
				"Refresh the Migration HUD before retrying.",
				"", "Enter return to the Migration HUD",
			)
			break
		}
		lines = append(lines,
			"Migration accepted",
			"Operation "+safeInline(wizard.result.OperationID),
			"State "+safeInline(string(wizard.result.State)),
			"Next "+safeInline(wizard.result.Next),
			"", "Enter return to the Migration HUD",
		)
	case MigrationWizardStale:
		reason := wizard.reason
		if reason == "" {
			reason = "authenticated daemon mutation authority is unavailable"
		}
		lines = append(lines,
			"STALE · read-only", reason,
			"Refresh the authoritative snapshot before creating a plan.",
			"Esc close",
		)
	case MigrationWizardError:
		lines = append(lines,
			"Migration step was not accepted.",
			wizard.errorMessage,
			"r return to editable choices · Esc close",
		)
	}
	if wizard.errorMessage != "" &&
		wizard.stage != MigrationWizardError && wizard.stage != MigrationWizardTerminal {
		lines = append(lines, "", "ERROR "+safeInline(wizard.errorMessage))
	}
	return fitLines(strings.Join(lines, "\n")+"\n", width)
}

func (wizard *MigrationWizard) exportSelectionLines() []string {
	lines := []string{
		"Export scope · " + strings.ToUpper(string(wizard.exportMode)),
		"Configuration exports profiles and references; Full also copies stopped VM disks.",
	}
	if wizard.exportMode == migration.ExportModeFull {
		lines = append(lines,
			"FULL WARNING: opaque guest disks may contain credentials and device-bound identities.",
			"Choosing Full explicitly acknowledges this encrypted-content risk.",
		)
	} else {
		lines = append(lines, "Secret values and persistent disks are excluded by default.")
	}
	lines = append(lines, "")
	if len(wizard.environments) == 0 {
		return append(lines, "No environments found in the verified snapshot.", "Esc close")
	}
	for index, value := range wizard.environments {
		marker, checked := " ", "[ ]"
		if index == wizard.selected {
			marker = ">"
		}
		if value.Selected {
			checked = "[x]"
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s %s · %s", marker, checked,
			safeInline(value.ExportName), safeInline(value.ExportState),
		))
	}
	return append(lines, "", "j/k move · Space toggle · a all/none · m config/full · Enter continue")
}

func (wizard *MigrationWizard) importSelectionLines() []string {
	inspection := wizard.importSession.Inspection.Inventory
	lines := []string{
		"Authenticated inventory · bundle " + safeInline(string(inspection.BundleID)),
		fmt.Sprintf(
			"%d environment(s) · %d disk(s) · %d secret value(s) · %s logical",
			inspection.Components.Environments, inspection.Components.Disks,
			inspection.Components.SecretValues, formatWizardBytes(inspection.LogicalBytes),
		),
		"Nothing is selected implicitly.", "",
	}
	for index, value := range wizard.environments {
		marker, checked := " ", "[ ]"
		if index == wizard.selected {
			marker = ">"
		}
		if value.Selected {
			checked = "[x]"
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s %s · %s · disks=%d",
			marker, checked, safeInline(value.Source.DisplayNameHint),
			strings.ToUpper(string(value.Source.Mode)), len(value.Source.DiskIDs),
		))
	}
	return append(lines, "", "j/k move · Space toggle · a all/none · Enter decisions")
}

func (wizard *MigrationWizard) importDecisionLines() []string {
	rows := wizard.decisionRows()
	wizard.selected = clampWizardIndex(wizard.selected, len(rows))
	lines := []string{
		"Destination decisions",
		"Safest defaults: Safe Clone, workspaces disabled, authority disabled, secrets unresolved.",
		"A name conflict remains blocked until you edit the destination name or separately remove the old VM.",
		"",
	}
	for index, row := range rows {
		marker := " "
		if index == wizard.selected {
			marker = ">"
		}
		lines = append(lines, fmt.Sprintf(
			"%s %-10s %s · %s", marker, strings.ToUpper(row.Kind),
			safeInline(row.Label), safeInline(row.Value),
		))
	}
	return append(lines, "",
		"Enter edit name/path or toggle identity · d disable/unresolved",
		"Secret: e existing ref · v import encrypted value · Authority: a approve JSON",
		"p build plan · Esc cancel",
	)
}

func (wizard *MigrationWizard) decisionEditLines() []string {
	prompt := "Enter destination value"
	switch wizard.editKind {
	case "name":
		prompt = "Destination environment name"
	case "workspace":
		prompt = "Existing destination workspace directory"
	case "secret":
		prompt = "Destination Hideout secret reference"
	case "authority":
		prompt = "Destination authority JSON (reviewed on this computer)"
	}
	return []string{
		"Edit " + wizard.editKind,
		prompt,
		"> " + safeInline(string(wizard.input)),
		"", "Enter accept · Esc cancels the whole wizard",
	}
}

func (wizard *MigrationWizard) reviewLines() []string {
	if wizard.mode == MigrationWizardExport && wizard.exportSession != nil {
		plan := wizard.exportSession.Plan
		lines := []string{
			"Review export plan " + safeInline(string(plan.PlanID)),
			"Digest " + safeInline(string(plan.PlanDigest)),
			strings.ToUpper(string(plan.Mode)) + " · environments " +
				fmt.Sprint(len(plan.EnvironmentRefs)) + " · disks " + fmt.Sprint(len(plan.DiskRefs)),
			"Included " + wizardList(plan.IncludedClasses),
			migrationWizardPayloadEstimate(plan),
			"Output " + safeInline(plan.OutputPath),
			"Confirmation " + safeInline(plan.ConfirmationText),
			"Excluded " + wizardList(plan.ExcludedClasses),
			"Risks " + wizardList(plan.RiskAcknowledgements),
			"Effects " + wizardEffects(plan.Effects),
		}
		for _, estimate := range plan.EnvironmentEstimates {
			lines = append(lines, fmt.Sprintf(
				"ENV %s · %s · %s · config %s · profile state %s · disks %s",
				safeInline(estimate.DisplayName), safeInline(string(estimate.EnvironmentRef)),
				formatWizardBytes(estimate.EstimatedLogicalBytes),
				formatWizardBytes(estimate.PortableConfigLogicalBytes),
				formatWizardBytes(estimate.ProfileStateLogicalBytes),
				wizardOpaqueIDList(estimate.DiskRefs),
			))
		}
		for _, estimate := range plan.DiskEstimates {
			lines = append(lines, fmt.Sprintf(
				"DISK %s · %s · logical %s · allocated hint %s · used by %s",
				safeInline(string(estimate.DiskRef)), safeInline(string(estimate.Role)),
				formatWizardBytes(estimate.LogicalBytes),
				formatWizardBytes(estimate.AllocatedBytesHint),
				wizardOpaqueIDList(estimate.Consumers),
			))
		}
		for _, warning := range plan.Warnings {
			lines = append(lines, "WARNING "+safeInline(warning.Code)+" · "+safeInline(warning.Summary))
		}
		return append(lines, "", "a apply this plan · e edit choices · Esc cancel")
	}
	if wizard.importPlan == nil {
		return []string{"Reviewed import plan is unavailable.", "e edit choices"}
	}
	plan := wizard.importPlan
	lines := []string{
		"Review import plan " + safeInline(string(plan.PlanID)),
		"Digest " + safeInline(string(plan.PlanDigest)),
		fmt.Sprintf(
			"Compatibility %t · backend %s · required %s · available %s",
			plan.Compatibility.Available, safeInline(plan.Compatibility.Backend),
			formatWizardBytes(plan.Compatibility.RequiredBytes),
			formatWizardBytes(plan.Compatibility.AvailableBytes),
		),
		fmt.Sprintf(
			"Objects %d · identities %d · workspaces %d · secrets %d · authority %d",
			len(plan.Objects), len(plan.IdentityActions), len(plan.WorkspaceActions),
			len(plan.SecretActions), len(plan.AuthorityActions),
		),
		"Effects " + wizardEffects(plan.Effects),
		"Risks " + wizardList(plan.RiskAcknowledgements),
	}
	for _, object := range plan.Objects {
		lines = append(lines,
			"OBJECT "+safeInline(string(object.SourceRef))+" → "+
				safeInline(object.DestinationName)+" · "+strings.ToUpper(string(object.Mode)),
		)
	}
	for _, action := range plan.IdentityActions {
		lines = append(lines,
			"IDENTITY "+safeInline(string(action.SourceRef))+" · "+
				safeInline(string(action.GuestPolicy))+fmt.Sprintf(
				" · fresh control=%t backend=%t",
				action.FreshControlIdentity, action.FreshBackendIdentity,
			),
		)
	}
	for _, action := range plan.WorkspaceActions {
		value := action.Decision
		if action.DestinationPath != "" {
			value += " → " + action.DestinationPath
		}
		lines = append(lines, "WORKSPACE "+safeInline(string(action.ProposalID))+" · "+safeInline(value))
	}
	for _, action := range plan.SecretActions {
		value := action.Decision
		if action.DestinationRef != "" {
			value += " → " + action.DestinationProvider + ":" + action.DestinationRef
		}
		lines = append(lines,
			"SECRET "+safeInline(string(action.SourceRef))+" · "+safeInline(value),
		)
	}
	for _, action := range plan.AuthorityActions {
		lines = append(lines,
			"AUTHORITY "+safeInline(string(action.ProposalID))+" · "+
				safeInline(action.Class)+fmt.Sprintf(" · approved=%t", action.Approved),
		)
	}
	for _, proposalID := range plan.DisabledProposals {
		lines = append(lines, "AUTHORITY "+safeInline(string(proposalID))+" · disabled")
	}
	for _, blocker := range plan.Blockers {
		lines = append(lines,
			"BLOCKED "+safeInline(blocker.Code)+" · "+safeInline(blocker.Summary),
			"  Next "+safeInline(blocker.Remediation),
		)
	}
	if !plan.Compatibility.Available && plan.Compatibility.ReasonCode != "" {
		lines = append(lines, "BLOCKED "+safeInline(plan.Compatibility.ReasonCode))
	}
	return append(lines, "", "a apply when unblocked · e edit choices · Esc cancel")
}

func (wizard *MigrationWizard) decisionRows() []migrationWizardDecisionRow {
	rows := []migrationWizardDecisionRow{}
	selectedRefs := make(map[migration.OpaqueID]bool)
	for index, value := range wizard.environments {
		if !value.Selected || value.Source.SourceRef == "" {
			continue
		}
		selectedRefs[value.Source.SourceRef] = true
		rows = append(rows,
			migrationWizardDecisionRow{
				Kind: "name", Index: index, Label: string(value.Source.SourceRef),
				Value: value.Destination,
			},
			migrationWizardDecisionRow{
				Kind: "identity", Index: index, Label: string(value.Source.SourceRef),
				Value: string(value.Policy),
			},
		)
	}
	for index, value := range wizard.workspaces {
		if selectedRefs[value.EnvironmentRef] {
			detail := value.Decision
			if value.Destination != "" {
				detail += " → " + value.Destination
			}
			rows = append(rows, migrationWizardDecisionRow{
				Kind: "workspace", Index: index,
				Label: string(value.Projection.ProposalID), Value: detail,
			})
		}
	}
	for index, value := range wizard.secrets {
		detail := value.Decision
		if value.Destination != "" {
			detail += " → " + value.Destination
		}
		rows = append(rows, migrationWizardDecisionRow{
			Kind: "secret", Index: index,
			Label: string(value.Projection.SecretRef), Value: detail,
		})
	}
	for index, value := range wizard.authorities {
		if selectedRefs[value.EnvironmentRef] {
			rows = append(rows, migrationWizardDecisionRow{
				Kind: "authority", Index: index,
				Label: string(value.Projection.ProposalID), Value: value.Decision,
			})
		}
	}
	return rows
}

func (wizard *MigrationWizard) selectedExportNames() []string {
	values := make([]string, 0, len(wizard.environments))
	for _, value := range wizard.environments {
		if value.Selected {
			values = append(values, value.ExportName)
		}
	}
	sort.Strings(values)
	return values
}

func (wizard *MigrationWizard) selectedImportRefs() []migration.OpaqueID {
	values := make([]migration.OpaqueID, 0, len(wizard.environments))
	for _, value := range wizard.environments {
		if value.Selected {
			values = append(values, value.Source.SourceRef)
		}
	}
	slices.Sort(values)
	return values
}

func (wizard *MigrationWizard) reviewBlocked() bool {
	if wizard.mode == MigrationWizardExport {
		return wizard.exportSession == nil
	}
	return wizard.importPlan == nil || len(wizard.importPlan.Blockers) != 0 ||
		!wizard.importPlan.Compatibility.Available
}

func (wizard *MigrationWizard) canMutate() bool {
	return wizard.mutable && wizard.provider != nil
}

func (wizard *MigrationWizard) moveSelection(delta, length int) {
	if length == 0 {
		wizard.selected = 0
		return
	}
	wizard.selected = (wizard.selected + delta) % length
	if wizard.selected < 0 {
		wizard.selected += length
	}
}

func (wizard *MigrationWizard) updateOrdinaryInput(message tea.KeyPressMsg) {
	key := message.String()
	if key == "backspace" {
		if len(wizard.input) != 0 {
			wizard.input[len(wizard.input)-1] = 0
			wizard.input = wizard.input[:len(wizard.input)-1]
		}
		wizard.errorMessage = ""
		return
	}
	text := message.Key().Text
	for _, character := range text {
		if unicode.IsControl(character) || isBidiControl(character) ||
			len(wizard.input) >= maxMigrationWizardInputRunes {
			continue
		}
		wizard.input = append(wizard.input, character)
	}
	wizard.errorMessage = ""
}

func (wizard *MigrationWizard) updateSecretInput(message tea.KeyPressMsg) {
	key := message.String()
	if key == "backspace" {
		wizard.removeSecretRune()
		return
	}
	wizard.appendSecretInput(message)
}

func (wizard *MigrationWizard) appendSecretInput(message tea.KeyPressMsg) {
	text := message.Key().Text
	for _, character := range text {
		if unicode.IsControl(character) || isBidiControl(character) {
			continue
		}
		encoded := []byte(string(character))
		if len(wizard.secretInput)+len(encoded) > maxMigrationPassphraseBytes {
			clear(encoded)
			break
		}
		wizard.secretInput = append(wizard.secretInput, encoded...)
		clear(encoded)
	}
	wizard.errorMessage = ""
}

func (wizard *MigrationWizard) removeSecretRune() {
	if len(wizard.secretInput) == 0 {
		return
	}
	_, size := utf8.DecodeLastRune(wizard.secretInput)
	if size == 0 {
		return
	}
	clear(wizard.secretInput[len(wizard.secretInput)-size:])
	wizard.secretInput = wizard.secretInput[:len(wizard.secretInput)-size]
	wizard.errorMessage = ""
}

func (wizard *MigrationWizard) clearOrdinaryInput() {
	clear(wizard.input)
	wizard.input = nil
}

func (wizard *MigrationWizard) clearSecretInput() {
	clear(wizard.secretInput)
	wizard.secretInput = nil
}

func (wizard *MigrationWizard) Clear() {
	if wizard == nil {
		return
	}
	wizard.clearOrdinaryInput()
	wizard.clearSecretInput()
	clear(wizard.firstSecret)
	wizard.firstSecret = nil
	if wizard.exportSession != nil {
		wizard.exportSession.SecretInputHandle = ""
	}
	if wizard.importSession != nil {
		wizard.importSession.SecretInputHandle = ""
	}
	wizard.errorMessage = ""
}

func (wizard *MigrationWizard) Stage() MigrationWizardStage {
	if wizard == nil {
		return ""
	}
	return wizard.stage
}

func (wizard *MigrationWizard) SyncAuthority(mutable bool, reason string) {
	if wizard == nil {
		return
	}
	wizard.mutable = mutable
	wizard.reason = safeInline(reason)
	if !mutable && wizard.stage != MigrationWizardTerminal &&
		wizard.stage != MigrationWizardApplying {
		wizard.clearSecretInput()
		clear(wizard.firstSecret)
		wizard.firstSecret = nil
		wizard.stage = MigrationWizardStale
	}
}

func migrationWizardError(err error) string {
	switch {
	case err == nil:
		return "Hideout returned an invalid migration response."
	case errors.Is(err, context.Canceled):
		return "The local request was cancelled; refresh migration status before retrying."
	case errors.Is(err, context.DeadlineExceeded):
		return "The local request timed out; refresh migration status before retrying."
	default:
		return "Hideout refused this step. Review current choices, blockers, and daemon health."
	}
}

func wizardEffects(values []migration.PlannedEffect) string {
	if len(values) == 0 {
		return "none"
	}
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = safeInline(value.Kind) + " via " + safeInline(value.Provider)
	}
	return strings.Join(out, ", ")
}

func wizardList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = safeInline(value)
	}
	return strings.Join(out, ", ")
}

func wizardOpaqueIDList(values []migration.OpaqueID) string {
	if len(values) == 0 {
		return "none"
	}
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = safeInline(string(value))
	}
	return strings.Join(out, ", ")
}

func migrationWizardPayloadEstimate(plan migration.ExportPlan) string {
	if plan.EstimatedPayloadComplete {
		return "Payload estimate " + formatWizardBytes(plan.EstimatedPayloadLogicalBytes) +
			" · complete logical payload"
	}
	return "Payload estimate at least " + formatWizardBytes(plan.EstimatedPayloadLogicalBytes) +
		" · selected secret value sizes hidden"
}

func formatWizardBytes(value uint64) string {
	const (
		kiB = uint64(1 << 10)
		miB = uint64(1 << 20)
		giB = uint64(1 << 30)
		tiB = uint64(1 << 40)
	)
	switch {
	case value >= tiB:
		return fmt.Sprintf("%.1f TiB", float64(value)/float64(tiB))
	case value >= giB:
		return fmt.Sprintf("%.1f GiB", float64(value)/float64(giB))
	case value >= miB:
		return fmt.Sprintf("%.1f MiB", float64(value)/float64(miB))
	case value >= kiB:
		return fmt.Sprintf("%.1f KiB", float64(value)/float64(kiB))
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func clampWizardIndex(value, length int) int {
	if length == 0 || value < 0 {
		return 0
	}
	if value >= length {
		return length - 1
	}
	return value
}
