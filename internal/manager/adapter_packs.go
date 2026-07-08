package manager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/adapterpack"
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

type AdapterPackOptions struct {
	Operation                   string
	SourceKind                  string
	SourcePath                  string
	SourceURL                   string
	SourceCommit                string
	ProfileName                 string
	PackID                      string
	RevisionID                  string
	AdapterID                   string
	CommandAdapterID            string
	Commands                    []string
	AllowedProposalCapabilities []string
}

type AdapterPackPlan struct {
	Version                     string                     `json:"version"`
	Operation                   string                     `json:"operation"`
	Profile                     string                     `json:"profile,omitempty"`
	PackID                      string                     `json:"packId,omitempty"`
	RevisionID                  string                     `json:"revisionId,omitempty"`
	AdapterID                   string                     `json:"adapterId,omitempty"`
	CommandAdapterID            string                     `json:"commandAdapterId,omitempty"`
	Source                      adapterpack.SourceSpec     `json:"source,omitempty"`
	Status                      string                     `json:"status"`
	Message                     string                     `json:"message"`
	Changed                     bool                       `json:"changed"`
	Validation                  map[string]string          `json:"validation,omitempty"`
	Commands                    []string                   `json:"commands,omitempty"`
	AllowedProposalCapabilities []string                   `json:"allowedProposalCapabilities,omitempty"`
	Entry                       *adapterpack.RegistryEntry `json:"entry,omitempty"`
}

type AdapterPackResult struct {
	Version  string                     `json:"version"`
	Plan     AdapterPackPlan            `json:"plan"`
	Applied  bool                       `json:"applied"`
	Entry    *adapterpack.RegistryEntry `json:"entry,omitempty"`
	Revision *adapterpack.Revision      `json:"revision,omitempty"`
	Test     *adapterpack.TestResult    `json:"test,omitempty"`
}

type AdapterPackSummary struct {
	PackID           string `json:"packId"`
	State            string `json:"state"`
	ActiveRevisionID string `json:"activeRevisionId,omitempty"`
	BuiltIn          bool   `json:"builtIn,omitempty"`
	Description      string `json:"description,omitempty"`
	RevisionCount    int    `json:"revisionCount"`
}

func (c Core) adapterPackStore() adapterpack.Store {
	return adapterpack.NewStore(c.Store.Root)
}

func (c Core) ListAdapterPacks() ([]AdapterPackSummary, error) {
	store := c.adapterPackStore()
	entries, err := store.List()
	if err != nil {
		return nil, err
	}
	entries = append(entries, builtInAdapterPackEntry())
	out := make([]AdapterPackSummary, 0, len(entries))
	for _, entry := range entries {
		out = append(out, adapterPackSummary(entry))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PackID < out[j].PackID })
	return out, nil
}

func (c Core) InspectAdapterPack(packID string) (adapterpack.RegistryEntry, error) {
	if packID == cmdadapter.BuiltinRootSensitiveID || packID == cmdadapter.BuiltinRootSensitiveKey {
		return builtInAdapterPackEntry(), nil
	}
	return c.adapterPackStore().Inspect(packID)
}

func (c Core) PlanAdapterPack(opts AdapterPackOptions) (AdapterPackPlan, error) {
	operation := strings.TrimSpace(opts.Operation)
	if operation == "" {
		return AdapterPackPlan{}, errors.New("adapter pack operation is required")
	}
	plan := AdapterPackPlan{
		Version:    adapterpack.PlanVersion,
		Operation:  operation,
		Status:     "pending",
		Changed:    true,
		Validation: map[string]string{},
		Source: adapterpack.SourceSpec{
			Kind:   opts.SourceKind,
			Path:   opts.SourcePath,
			URL:    opts.SourceURL,
			Commit: opts.SourceCommit,
		},
	}
	switch operation {
	case "install", "upgrade":
		if plan.Source.Kind == "" {
			plan.Source.Kind = adapterpack.SourceLocal
		}
		plan.Message = operation + " adapter pack"
	case "test":
		plan.PackID = strings.TrimSpace(opts.PackID)
		plan.RevisionID = strings.TrimSpace(opts.RevisionID)
		if plan.PackID == "" {
			return AdapterPackPlan{}, errors.New("pack id is required")
		}
		entry, rev, err := c.adapterPackRevision(plan.PackID, plan.RevisionID)
		if err != nil {
			return AdapterPackPlan{}, err
		}
		plan.RevisionID = rev.RevisionID
		plan.Entry = &entry
		plan.Message = "test adapter pack"
	case "enable":
		return c.planAdapterPackEnable(opts, plan)
	case "disable":
		profileName, err := normalizeManagerProfileName(opts.ProfileName)
		if err != nil {
			return AdapterPackPlan{}, err
		}
		adapterID := strings.TrimSpace(opts.CommandAdapterID)
		if adapterID == "" {
			adapterID = strings.TrimSpace(opts.AdapterID)
		}
		if adapterID == "" {
			return AdapterPackPlan{}, errors.New("command adapter id is required")
		}
		plan.Profile = profileName
		plan.CommandAdapterID = adapterID
		plan.Message = "disable adapter pack binding"
	case "revoke":
		plan.PackID = strings.TrimSpace(opts.PackID)
		if plan.PackID == "" {
			return AdapterPackPlan{}, errors.New("pack id is required")
		}
		entry, err := c.adapterPackStore().Inspect(plan.PackID)
		if err != nil {
			return AdapterPackPlan{}, err
		}
		plan.Entry = &entry
		plan.Changed = entry.State != adapterpack.StateRevoked
		if !plan.Changed {
			plan.Status = "noop"
		}
		plan.Message = "revoke adapter pack"
	default:
		return AdapterPackPlan{}, fmt.Errorf("unsupported adapter pack operation %q", operation)
	}
	return plan, nil
}

func (c Core) ApplyAdapterPack(plan AdapterPackPlan) (AdapterPackResult, error) {
	if plan.Version != adapterpack.PlanVersion {
		return AdapterPackResult{}, errors.New("invalid adapter pack plan version")
	}
	result := AdapterPackResult{Version: adapterpack.PlanVersion, Plan: plan}
	var entry adapterpack.RegistryEntry
	var rev adapterpack.Revision
	var test adapterpack.TestResult
	var err error
	switch plan.Operation {
	case "install", "upgrade":
		entry, rev, err = c.adapterPackStore().Install(plan.Source)
		if err == nil {
			result.Entry = &entry
			result.Revision = &rev
			result.Applied = true
		}
	case "test":
		test, err = c.adapterPackStore().RunTests(contextBackground(), plan.PackID, plan.RevisionID)
		if err == nil {
			result.Test = &test
			entry, _ = c.adapterPackStore().Inspect(plan.PackID)
			result.Entry = &entry
			result.Applied = true
		}
	case "enable":
		return c.applyAdapterPackEnable(plan)
	case "disable":
		return c.applyAdapterPackDisable(plan)
	case "revoke":
		return c.applyAdapterPackRevoke(plan)
	default:
		return AdapterPackResult{}, fmt.Errorf("unsupported adapter pack operation %q", plan.Operation)
	}
	if auditErr := c.recordAdapterPackAudit(plan.Operation, entry, rev, err); err == nil && auditErr != nil {
		err = auditErr
	}
	if err != nil {
		return AdapterPackResult{}, err
	}
	return result, nil
}

func (c Core) planAdapterPackEnable(opts AdapterPackOptions, plan AdapterPackPlan) (AdapterPackPlan, error) {
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return AdapterPackPlan{}, err
	}
	entry, rev, err := c.adapterPackRevision(strings.TrimSpace(opts.PackID), strings.TrimSpace(opts.RevisionID))
	if err != nil {
		return AdapterPackPlan{}, err
	}
	if rev.TestStatus != adapterpack.TestPassed {
		return AdapterPackPlan{}, fmt.Errorf("adapter pack %q revision %q tests must pass before enablement", entry.PackID, rev.RevisionID)
	}
	adapterID := strings.TrimSpace(opts.AdapterID)
	if adapterID == "" {
		return AdapterPackPlan{}, errors.New("pack adapter id is required")
	}
	resolved, err := c.adapterPackStore().ResolveRuntime(entry.PackID, rev.RevisionID, adapterID, rev.SourceDigest)
	if err != nil {
		return AdapterPackPlan{}, err
	}
	commandAdapterID := strings.TrimSpace(opts.CommandAdapterID)
	if commandAdapterID == "" {
		commandAdapterID = adapterID
	}
	commands := append([]string(nil), opts.Commands...)
	if len(commands) == 0 {
		commands = append([]string(nil), resolved.Commands...)
	}
	if !stringSetSubset(commands, resolved.Commands) {
		return AdapterPackPlan{}, errors.New("adapter pack enable commands must be declared by the pack adapter")
	}
	capabilities := append([]string(nil), opts.AllowedProposalCapabilities...)
	if len(capabilities) == 0 {
		capabilities = append([]string(nil), resolved.AllowedProposalCapabilities...)
	}
	if !stringSetSubset(capabilities, resolved.AllowedProposalCapabilities) {
		return AdapterPackPlan{}, errors.New("adapter pack enable capabilities must be declared by the pack adapter")
	}
	current, err := loadProfileForPlanning(c.Store, profileName)
	if err != nil {
		return AdapterPackPlan{}, err
	}
	after := current
	after.CommandAdapters.Adapters = cloneCommandAdapterMap(current.CommandAdapters.Adapters)
	ensureCommandAdapters(&after)
	after.CommandAdapters.Adapters[commandAdapterID] = profile.CommandAdapter{
		Enabled:                     true,
		Digest:                      resolved.Digest,
		Entrypoint:                  resolved.Entrypoint,
		Commands:                    commands,
		AllowedProposalCapabilities: capabilities,
		Description:                 resolved.Description,
		PackID:                      entry.PackID,
		PackRevisionID:              rev.RevisionID,
		PackAdapterID:               adapterID,
		PackLockDigest:              rev.SourceDigest,
	}
	if err := after.Validate(); err != nil {
		return AdapterPackPlan{}, err
	}
	plan.Profile = profileName
	plan.PackID = entry.PackID
	plan.RevisionID = rev.RevisionID
	plan.AdapterID = adapterID
	plan.CommandAdapterID = commandAdapterID
	plan.Commands = sortedStringsForManager(commands)
	plan.AllowedProposalCapabilities = sortedStringsForManager(capabilities)
	plan.Entry = &entry
	plan.Validation["core"] = "passed"
	plan.Validation["tests"] = "passed"
	plan.Message = "enable adapter pack binding"
	return plan, nil
}

func (c Core) applyAdapterPackEnable(plan AdapterPackPlan) (AdapterPackResult, error) {
	var result AdapterPackResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		checked, err := c.PlanAdapterPack(AdapterPackOptions{
			Operation:                   "enable",
			ProfileName:                 plan.Profile,
			PackID:                      plan.PackID,
			RevisionID:                  plan.RevisionID,
			AdapterID:                   plan.AdapterID,
			CommandAdapterID:            plan.CommandAdapterID,
			Commands:                    plan.Commands,
			AllowedProposalCapabilities: plan.AllowedProposalCapabilities,
		})
		if err != nil {
			return err
		}
		p, err := c.Store.LoadOrInit(checked.Profile)
		if err != nil {
			return err
		}
		ensureCommandAdapters(&p)
		resolved, err := c.adapterPackStore().ResolveRuntime(checked.PackID, checked.RevisionID, checked.AdapterID, "")
		if err != nil {
			return err
		}
		p.CommandAdapters.Adapters[checked.CommandAdapterID] = profile.CommandAdapter{
			Enabled:                     true,
			Digest:                      resolved.Digest,
			Entrypoint:                  resolved.Entrypoint,
			Commands:                    append([]string(nil), checked.Commands...),
			AllowedProposalCapabilities: append([]string(nil), checked.AllowedProposalCapabilities...),
			Description:                 resolved.Description,
			PackID:                      checked.PackID,
			PackRevisionID:              checked.RevisionID,
			PackAdapterID:               checked.AdapterID,
			PackLockDigest:              resolved.LockDigest,
		}
		if err := c.Store.Save(p); err != nil {
			return err
		}
		result = AdapterPackResult{Version: adapterpack.PlanVersion, Plan: checked, Applied: true, Entry: checked.Entry}
		return nil
	})
	if err != nil {
		_ = c.recordAdapterPackAudit(plan.Operation, adapterpack.RegistryEntry{PackID: plan.PackID}, adapterpack.Revision{RevisionID: plan.RevisionID}, err)
		return AdapterPackResult{}, err
	}
	if result.Entry != nil {
		rev := result.Entry.Revisions[result.Plan.RevisionID]
		_ = c.recordAdapterPackAudit(plan.Operation, *result.Entry, rev, nil)
	}
	return result, nil
}

func (c Core) applyAdapterPackDisable(plan AdapterPackPlan) (AdapterPackResult, error) {
	var result AdapterPackResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		p, err := c.Store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		ensureCommandAdapters(&p)
		adapter, ok := p.CommandAdapters.Adapters[plan.CommandAdapterID]
		if !ok {
			return fmt.Errorf("command adapter %q is not configured", plan.CommandAdapterID)
		}
		adapter.Enabled = false
		p.CommandAdapters.Adapters[plan.CommandAdapterID] = adapter
		if err := p.Validate(); err != nil {
			return err
		}
		if err := c.Store.Save(p); err != nil {
			return err
		}
		result = AdapterPackResult{Version: adapterpack.PlanVersion, Plan: plan, Applied: true}
		return nil
	})
	if err != nil {
		return AdapterPackResult{}, err
	}
	return result, nil
}

func (c Core) applyAdapterPackRevoke(plan AdapterPackPlan) (AdapterPackResult, error) {
	store := c.adapterPackStore()
	registry, err := store.Load()
	if err != nil {
		return AdapterPackResult{}, err
	}
	entry, ok := registry.Packs[plan.PackID]
	if !ok {
		return AdapterPackResult{}, fmt.Errorf("adapter pack %q is not installed", plan.PackID)
	}
	entry.State = adapterpack.StateRevoked
	registry.Packs[plan.PackID] = entry
	if err := store.Save(registry); err != nil {
		return AdapterPackResult{}, err
	}
	_ = c.recordAdapterPackAudit(plan.Operation, entry, adapterpack.Revision{}, nil)
	return AdapterPackResult{Version: adapterpack.PlanVersion, Plan: plan, Applied: true, Entry: &entry}, nil
}

func (c Core) adapterPackRevision(packID, revisionID string) (adapterpack.RegistryEntry, adapterpack.Revision, error) {
	if packID == "" {
		return adapterpack.RegistryEntry{}, adapterpack.Revision{}, errors.New("pack id is required")
	}
	entry, err := c.adapterPackStore().Inspect(packID)
	if err != nil {
		return adapterpack.RegistryEntry{}, adapterpack.Revision{}, err
	}
	if entry.State == adapterpack.StateRevoked {
		return adapterpack.RegistryEntry{}, adapterpack.Revision{}, fmt.Errorf("adapter pack %q is revoked", packID)
	}
	if revisionID == "" {
		revisionID = entry.ActiveRevisionID
	}
	rev, ok := entry.Revisions[revisionID]
	if !ok {
		return adapterpack.RegistryEntry{}, adapterpack.Revision{}, fmt.Errorf("adapter pack %q revision %q is not installed", packID, revisionID)
	}
	return entry, rev, nil
}

func (c Core) recordAdapterPackAudit(operation string, entry adapterpack.RegistryEntry, rev adapterpack.Revision, opErr error) error {
	if c.Store.Root == "" {
		return nil
	}
	layout, err := session.New(c.Store.Root)
	if err != nil {
		return err
	}
	aw, err := audit.NewFile(layout.AuditPath)
	if err != nil {
		return err
	}
	decision := "allow"
	reason := ""
	if opErr != nil {
		decision = "deny"
		reason = opErr.Error()
	}
	event := adapterpack.Evidence(operation, decision, entry, rev, reason)
	event.Session = layout.ID
	if err := aw.Emit(event); err != nil {
		_ = aw.Close()
		_, _ = session.CleanupEphemeral(c.Store.Root, layout.ID, false)
		return err
	}
	if err := aw.Close(); err != nil {
		_, _ = session.CleanupEphemeral(c.Store.Root, layout.ID, false)
		return err
	}
	_, _ = session.CleanupEphemeral(c.Store.Root, layout.ID, false)
	return nil
}

func builtInAdapterPackEntry() adapterpack.RegistryEntry {
	adapter := cmdadapter.BuiltinRootSensitiveProfileAdapter()
	return adapterpack.RegistryEntry{
		PackID:           cmdadapter.BuiltinRootSensitiveID,
		State:            adapterpack.StateBuiltin,
		ActiveRevisionID: "builtin",
		BuiltIn:          true,
		Description:      adapter.Description,
		Revisions: map[string]adapterpack.Revision{
			"builtin": {
				RevisionID:       "builtin",
				Version:          "builtin",
				Source:           adapterpack.SourceLock{Kind: "builtin"},
				ManifestDigest:   adapter.Digest,
				SourceDigest:     adapter.Digest,
				ValidationStatus: "passed",
				TestStatus:       adapterpack.TestPassed,
			},
		},
	}
}

func adapterPackSummary(entry adapterpack.RegistryEntry) AdapterPackSummary {
	return AdapterPackSummary{
		PackID:           entry.PackID,
		State:            entry.State,
		ActiveRevisionID: entry.ActiveRevisionID,
		BuiltIn:          entry.BuiltIn,
		Description:      entry.Description,
		RevisionCount:    len(entry.Revisions),
	}
}

func contextBackground() context.Context {
	return context.Background()
}

func stringSetSubset(values, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		set[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}
