package manager

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/profile"
)

const CommandAdapterPlanVersion = "hideout.command-adapter-plan/v1"

type CommandAdapterOptions struct {
	ProfileName                 string
	Operation                   string
	AdapterID                   string
	Path                        string
	Entrypoint                  string
	Commands                    []string
	AllowedProposalCapabilities []string
	Builtin                     string
}

type CommandAdapterPlan struct {
	Version                     string   `json:"version"`
	Profile                     string   `json:"profile"`
	Operation                   string   `json:"operation"`
	AdapterID                   string   `json:"adapterId"`
	Enabled                     bool     `json:"enabled"`
	Path                        string   `json:"path,omitempty"`
	Digest                      string   `json:"digest,omitempty"`
	Entrypoint                  string   `json:"entrypoint,omitempty"`
	Commands                    []string `json:"commands,omitempty"`
	AllowedProposalCapabilities []string `json:"allowedProposalCapabilities,omitempty"`
	Builtin                     string   `json:"builtin,omitempty"`
	Description                 string   `json:"description,omitempty"`
	Status                      string   `json:"status"`
	Message                     string   `json:"message"`
	Changed                     bool     `json:"changed"`
	Warnings                    []string `json:"warnings,omitempty"`
	CommandsBefore              []string `json:"commandsBefore"`
	CommandsAfter               []string `json:"commandsAfter"`
}

type CommandAdapterResult struct {
	Version  string             `json:"version"`
	Plan     CommandAdapterPlan `json:"plan"`
	Applied  bool               `json:"applied"`
	Adapters []string           `json:"adapters"`
}

func (c Core) PlanCommandAdapter(opts CommandAdapterOptions) (CommandAdapterPlan, error) {
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return CommandAdapterPlan{}, err
	}
	operation := strings.TrimSpace(opts.Operation)
	adapterID := strings.TrimSpace(opts.AdapterID)
	if adapterID == "" {
		return CommandAdapterPlan{}, errors.New("adapter id is required")
	}
	current, err := loadProfileForPlanning(c.Store, profileName)
	if err != nil {
		return CommandAdapterPlan{}, err
	}
	after := current
	after.CommandAdapters.Adapters = cloneCommandAdapterMap(current.CommandAdapters.Adapters)
	ensureCommandAdapters(&after)
	plan := CommandAdapterPlan{
		Version:        CommandAdapterPlanVersion,
		Profile:        current.Name,
		Operation:      operation,
		AdapterID:      adapterID,
		Status:         "pending",
		CommandsBefore: commandOwnerNames(current),
	}
	switch operation {
	case "add-local":
		adapter, err := c.localAdapterFromOptions(current.Name, opts)
		if err != nil {
			return CommandAdapterPlan{}, err
		}
		before, exists := after.CommandAdapters.Adapters[adapterID]
		after.CommandAdapters.Adapters[adapterID] = adapter
		plan.fillAdapter(adapter)
		plan.Changed = !exists || !reflect.DeepEqual(before, adapter)
		plan.Message = "add local command adapter"
	case "add-builtin-root-sensitive":
		adapter := cmdadapter.BuiltinRootSensitiveProfileAdapter()
		before, exists := after.CommandAdapters.Adapters[adapterID]
		after.CommandAdapters.Adapters[adapterID] = adapter
		plan.fillAdapter(adapter)
		plan.Changed = !exists || !reflect.DeepEqual(before, adapter)
		plan.Message = "add built-in root-sensitive intent adapter"
		plan.Warnings = append(plan.Warnings, "root-sensitive adapter captures command intent only until 009 privilege separation is enforced")
	case "enable":
		adapter, ok := after.CommandAdapters.Adapters[adapterID]
		if !ok {
			return CommandAdapterPlan{}, fmt.Errorf("command adapter %q is not configured", adapterID)
		}
		adapter.Enabled = true
		if err := c.verifyAdapterDigest(current.Name, adapterID, adapter); err != nil {
			return CommandAdapterPlan{}, err
		}
		after.CommandAdapters.Adapters[adapterID] = adapter
		plan.fillAdapter(adapter)
		plan.Changed = !current.CommandAdapters.Adapters[adapterID].Enabled
		plan.Message = "enable command adapter"
	case "disable":
		adapter, ok := after.CommandAdapters.Adapters[adapterID]
		if !ok {
			return CommandAdapterPlan{}, fmt.Errorf("command adapter %q is not configured", adapterID)
		}
		adapter.Enabled = false
		after.CommandAdapters.Adapters[adapterID] = adapter
		plan.fillAdapter(adapter)
		plan.Changed = current.CommandAdapters.Adapters[adapterID].Enabled
		plan.Message = "disable command adapter"
	case "refresh-digest":
		adapter, ok := after.CommandAdapters.Adapters[adapterID]
		if !ok {
			return CommandAdapterPlan{}, fmt.Errorf("command adapter %q is not configured", adapterID)
		}
		_, digest, err := cmdadapter.ResolveSource(c.Store.ProfileDir(current.Name), cmdadapter.RuntimeAdapter{
			ID:      adapterID,
			Path:    adapter.Path,
			Builtin: adapter.Builtin,
		})
		if err != nil {
			return CommandAdapterPlan{}, err
		}
		adapter.Digest = digest
		after.CommandAdapters.Adapters[adapterID] = adapter
		plan.fillAdapter(adapter)
		plan.Changed = current.CommandAdapters.Adapters[adapterID].Digest != digest
		plan.Message = "refresh command adapter digest"
	case "remove":
		adapter, ok := after.CommandAdapters.Adapters[adapterID]
		if ok {
			plan.fillAdapter(adapter)
			delete(after.CommandAdapters.Adapters, adapterID)
		}
		plan.Changed = ok
		plan.Message = "remove command adapter"
	default:
		return CommandAdapterPlan{}, fmt.Errorf("unsupported command-adapter operation %q", operation)
	}
	if err := after.Validate(); err != nil {
		return CommandAdapterPlan{}, err
	}
	plan.CommandsAfter = commandOwnerNames(after)
	if !plan.Changed {
		plan.Status = "noop"
	}
	return plan, nil
}

func (c Core) ApplyCommandAdapter(plan CommandAdapterPlan) (CommandAdapterResult, error) {
	if plan.Version != CommandAdapterPlanVersion {
		return CommandAdapterResult{}, errors.New("invalid command-adapter plan version")
	}
	var result CommandAdapterResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		checked, err := c.PlanCommandAdapter(CommandAdapterOptions{
			ProfileName:                 plan.Profile,
			Operation:                   plan.Operation,
			AdapterID:                   plan.AdapterID,
			Path:                        plan.Path,
			Entrypoint:                  plan.Entrypoint,
			Commands:                    plan.Commands,
			AllowedProposalCapabilities: plan.AllowedProposalCapabilities,
			Builtin:                     plan.Builtin,
		})
		if err != nil {
			return err
		}
		if !checked.Changed {
			result = CommandAdapterResult{Version: CommandAdapterPlanVersion, Plan: checked, Applied: false, Adapters: adapterNamesFromPlanProfile(c.Store, checked.Profile)}
			return nil
		}
		p, err := c.Store.LoadOrInit(checked.Profile)
		if err != nil {
			return err
		}
		ensureCommandAdapters(&p)
		switch checked.Operation {
		case "add-local", "add-builtin-root-sensitive", "enable", "disable", "refresh-digest":
			p.CommandAdapters.Adapters[checked.AdapterID] = profile.CommandAdapter{
				Enabled:                     checked.Enabled,
				Path:                        checked.Path,
				Digest:                      checked.Digest,
				Entrypoint:                  checked.Entrypoint,
				Commands:                    append([]string(nil), checked.Commands...),
				AllowedProposalCapabilities: append([]string(nil), checked.AllowedProposalCapabilities...),
				Builtin:                     checked.Builtin,
				Description:                 checked.Description,
			}
		case "remove":
			delete(p.CommandAdapters.Adapters, checked.AdapterID)
		default:
			return fmt.Errorf("unsupported command-adapter operation %q", checked.Operation)
		}
		if err := c.Store.Save(p); err != nil {
			return err
		}
		result = CommandAdapterResult{Version: CommandAdapterPlanVersion, Plan: checked, Applied: true, Adapters: commandAdapterNames(p)}
		return nil
	})
	if err != nil {
		return CommandAdapterResult{}, err
	}
	return result, nil
}

func (c Core) localAdapterFromOptions(profileName string, opts CommandAdapterOptions) (profile.CommandAdapter, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return profile.CommandAdapter{}, errors.New("adapter path is required")
	}
	adapter := profile.CommandAdapter{
		Enabled:                     true,
		Path:                        path,
		Entrypoint:                  opts.Entrypoint,
		Commands:                    append([]string(nil), opts.Commands...),
		AllowedProposalCapabilities: append([]string(nil), opts.AllowedProposalCapabilities...),
	}
	if adapter.Entrypoint == "" {
		adapter.Entrypoint = cmdadapter.DefaultEntrypoint
	}
	_, digest, err := cmdadapter.ResolveSource(c.Store.ProfileDir(profileName), cmdadapter.RuntimeAdapter{
		ID:      opts.AdapterID,
		Path:    adapter.Path,
		Builtin: adapter.Builtin,
	})
	if err != nil {
		return profile.CommandAdapter{}, err
	}
	adapter.Digest = digest
	return adapter, nil
}

func (c Core) verifyAdapterDigest(profileName, adapterID string, adapter profile.CommandAdapter) error {
	_, err := cmdadapter.CompileAdapter(c.Store.ProfileDir(profileName), adapterID, adapter)
	return err
}

func ensureCommandAdapters(p *profile.Profile) {
	if p.CommandAdapters.Adapters == nil {
		p.CommandAdapters.Adapters = map[string]profile.CommandAdapter{}
	}
}

func cloneCommandAdapterMap(src map[string]profile.CommandAdapter) map[string]profile.CommandAdapter {
	if src == nil {
		return nil
	}
	dst := make(map[string]profile.CommandAdapter, len(src))
	for id, adapter := range src {
		adapter.Commands = append([]string(nil), adapter.Commands...)
		adapter.AllowedProposalCapabilities = append([]string(nil), adapter.AllowedProposalCapabilities...)
		dst[id] = adapter
	}
	return dst
}

func (p *CommandAdapterPlan) fillAdapter(adapter profile.CommandAdapter) {
	p.Enabled = adapter.Enabled
	p.Path = adapter.Path
	p.Digest = adapter.Digest
	p.Entrypoint = adapter.Entrypoint
	if p.Entrypoint == "" {
		p.Entrypoint = cmdadapter.DefaultEntrypoint
	}
	p.Commands = append([]string(nil), adapter.Commands...)
	sort.Strings(p.Commands)
	p.AllowedProposalCapabilities = append([]string(nil), adapter.AllowedProposalCapabilities...)
	sort.Strings(p.AllowedProposalCapabilities)
	p.Builtin = adapter.Builtin
	p.Description = adapter.Description
}

func commandOwnerNames(p profile.Profile) []string {
	names := commandProxyNames(p)
	for _, adapter := range p.CommandAdapters.Adapters {
		if !adapter.Enabled {
			continue
		}
		names = append(names, adapter.Commands...)
	}
	sort.Strings(names)
	return names
}

func commandAdapterNames(p profile.Profile) []string {
	names := make([]string, 0, len(p.CommandAdapters.Adapters))
	for name := range p.CommandAdapters.Adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func adapterNamesFromPlanProfile(store profile.Store, name string) []string {
	p, err := store.Load(name)
	if err != nil {
		return nil
	}
	return commandAdapterNames(p)
}
