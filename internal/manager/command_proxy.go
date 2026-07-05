package manager

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/profile"
)

const CommandProxyPlanVersion = "hideout.command-proxy-plan/v1"

type CommandProxyOptions struct {
	ProfileName string
	Operation   string
	Command     string
}

type CommandProxyPlan struct {
	Version        string   `json:"version"`
	Profile        string   `json:"profile"`
	Operation      string   `json:"operation"`
	Command        string   `json:"command"`
	Route          string   `json:"route,omitempty"`
	Action         string   `json:"action,omitempty"`
	ArgvSchema     string   `json:"argvSchema,omitempty"`
	Status         string   `json:"status"`
	Message        string   `json:"message"`
	Exists         bool     `json:"exists"`
	Changed        bool     `json:"changed"`
	CommandsBefore []string `json:"commandsBefore"`
	CommandsAfter  []string `json:"commandsAfter"`
}

type CommandProxyResult struct {
	Version  string           `json:"version"`
	Plan     CommandProxyPlan `json:"plan"`
	Applied  bool             `json:"applied"`
	Commands []string         `json:"commands"`
}

func (c Core) PlanCommandProxy(opts CommandProxyOptions) (CommandProxyPlan, error) {
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return CommandProxyPlan{}, err
	}
	operation := strings.TrimSpace(opts.Operation)
	commandName := strings.TrimSpace(opts.Command)
	if commandName == "" {
		return CommandProxyPlan{}, errors.New("command is required")
	}
	command := hostOpenCommandProxyCommand()
	if err := validateCommandProxyNameByProfile(commandName, command); err != nil {
		return CommandProxyPlan{}, err
	}
	current, err := loadProfileForPlanning(c.Store, profileName)
	if err != nil {
		return CommandProxyPlan{}, err
	}
	before := commandProxyNames(current)
	existing, exists := current.CommandProxy.Commands[commandName]
	afterProfile := current
	if afterProfile.CommandProxy.Commands == nil {
		afterProfile.CommandProxy.Commands = map[string]profile.CommandProxyCommand{}
	}
	plan := CommandProxyPlan{
		Version:        CommandProxyPlanVersion,
		Profile:        current.Name,
		Operation:      operation,
		Command:        commandName,
		Route:          cmdproxy.RouteHostBroker,
		Action:         cmdproxy.ActionHostOpen,
		ArgvSchema:     cmdproxy.ArgvSchemaOpenV1,
		Status:         "pending",
		CommandsBefore: before,
	}
	switch operation {
	case "add-open":
		afterProfile.CommandProxy.Commands[commandName] = command
		plan.Exists = exists
		plan.Changed = !exists || existing != command
		if plan.Changed {
			plan.Message = "register host.open command symbol"
		} else {
			plan.Status = "noop"
			plan.Message = "command symbol is already registered"
		}
	case "remove":
		if commandName == "open" {
			return CommandProxyPlan{}, errors.New("command-proxy open is required and cannot be removed")
		}
		delete(afterProfile.CommandProxy.Commands, commandName)
		plan.Exists = exists
		plan.Changed = exists
		if plan.Changed {
			plan.Message = "remove command symbol registration"
		} else {
			plan.Status = "noop"
			plan.Message = "command symbol is not registered"
		}
	default:
		return CommandProxyPlan{}, fmt.Errorf("unsupported command-proxy operation %q", operation)
	}
	if err := afterProfile.Validate(); err != nil {
		return CommandProxyPlan{}, err
	}
	plan.CommandsAfter = commandProxyNames(afterProfile)
	return plan, nil
}

func (c Core) ApplyCommandProxy(plan CommandProxyPlan) (CommandProxyResult, error) {
	if plan.Version != CommandProxyPlanVersion {
		return CommandProxyResult{}, errors.New("invalid command-proxy plan version")
	}
	var result CommandProxyResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		checked, err := c.PlanCommandProxy(CommandProxyOptions{
			ProfileName: plan.Profile,
			Operation:   plan.Operation,
			Command:     plan.Command,
		})
		if err != nil {
			return err
		}
		if !checked.Changed {
			result = CommandProxyResult{
				Version:  CommandProxyPlanVersion,
				Plan:     checked,
				Applied:  false,
				Commands: checked.CommandsAfter,
			}
			return nil
		}
		p, err := c.Store.LoadOrInit(checked.Profile)
		if err != nil {
			return err
		}
		if p.CommandProxy.Commands == nil {
			p.CommandProxy.Commands = map[string]profile.CommandProxyCommand{}
		}
		switch checked.Operation {
		case "add-open":
			p.CommandProxy.Commands[checked.Command] = hostOpenCommandProxyCommand()
		case "remove":
			delete(p.CommandProxy.Commands, checked.Command)
		default:
			return fmt.Errorf("unsupported command-proxy operation %q", checked.Operation)
		}
		if err := c.Store.Save(p); err != nil {
			return err
		}
		result = CommandProxyResult{
			Version:  CommandProxyPlanVersion,
			Plan:     checked,
			Applied:  checked.Changed,
			Commands: checked.CommandsAfter,
		}
		return nil
	})
	if err != nil {
		return CommandProxyResult{}, err
	}
	return result, nil
}

func hostOpenCommandProxyCommand() profile.CommandProxyCommand {
	return profile.CommandProxyCommand{
		Route:      cmdproxy.RouteHostBroker,
		Action:     cmdproxy.ActionHostOpen,
		ArgvSchema: cmdproxy.ArgvSchemaOpenV1,
	}
}

func normalizeManagerProfileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	if err := profile.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateCommandProxyNameByProfile(commandName string, command profile.CommandProxyCommand) error {
	probe := profile.Default("__command_proxy_validation__")
	probe.CommandProxy.Commands[commandName] = command
	return probe.Validate()
}

func loadProfileForPlanning(store profile.Store, name string) (profile.Profile, error) {
	p, err := store.Load(name)
	if err == nil {
		return p, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return profile.Default(name), nil
	}
	return profile.Profile{}, err
}

func commandProxyNames(p profile.Profile) []string {
	names := make([]string, 0, len(p.CommandProxy.Commands))
	for name := range p.CommandProxy.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
