package cmdproxy

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	ActionHostOpen     = "host.open"
	ArgvSchemaOpenV1   = "open-target-v1"
	StreamMetadataOnly = "metadata-only"
	DefaultModeAllow   = "allow"
	RouteHostBroker    = "host-broker"
)

type Registration struct {
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases,omitempty"`
	Action         string   `json:"action"`
	ArgvSchema     string   `json:"argvSchema"`
	StreamPolicy   string   `json:"streamPolicy"`
	DefaultMode    string   `json:"defaultMode"`
	AllowedTargets []string `json:"allowedTargets"`
}

type Registry struct {
	commands map[string]Registration
	aliases  map[string]string
}

type Request struct {
	Subject string         `json:"subject"`
	Command string         `json:"command"`
	Argv    []string       `json:"argv"`
	CWD     string         `json:"cwd"`
	Action  string         `json:"action"`
	Route   string         `json:"route"`
	Payload map[string]any `json:"payload"`
}

func DefaultRegistry() Registry {
	return mustRegistry(HostOpenRegistry([]string{"xdg-open"}))
}

func HostOpenRegistry(aliases []string) (Registry, error) {
	aliases = append([]string(nil), aliases...)
	sort.Strings(aliases)
	return NewRegistry([]Registration{
		{
			Name:           "open",
			Aliases:        aliases,
			Action:         ActionHostOpen,
			ArgvSchema:     ArgvSchemaOpenV1,
			StreamPolicy:   StreamMetadataOnly,
			DefaultMode:    DefaultModeAllow,
			AllowedTargets: []string{"url:http", "url:https", "workspace-file"},
		},
	})
}

func mustRegistry(registry Registry, err error) Registry {
	if err != nil {
		panic(err)
	}
	return registry
}

func RegistryFromProfile(p profile.Profile) (Registry, error) {
	if len(p.CommandProxy.Commands) == 0 {
		return Registry{}, errors.New("commandProxy.commands is required")
	}
	if _, ok := p.CommandProxy.Commands["open"]; !ok {
		return Registry{}, errors.New("commandProxy.commands.open is required")
	}
	aliases := []string{}
	for name, command := range p.CommandProxy.Commands {
		switch name {
		case "open":
		case "xdg-open":
			aliases = append(aliases, name)
		default:
			return Registry{}, fmt.Errorf("unsupported command proxy %q", name)
		}
		if command.Route != RouteHostBroker {
			return Registry{}, fmt.Errorf("commandProxy.commands.%s.route must be %s", name, RouteHostBroker)
		}
		if command.Action != ActionHostOpen {
			return Registry{}, fmt.Errorf("commandProxy.commands.%s.action must be %s", name, ActionHostOpen)
		}
	}
	return HostOpenRegistry(aliases)
}

func NewRegistry(registrations []Registration) (Registry, error) {
	r := Registry{
		commands: map[string]Registration{},
		aliases:  map[string]string{},
	}
	for _, reg := range registrations {
		name, err := normalizeRegistryName("command proxy", reg.Name)
		if err != nil {
			return Registry{}, err
		}
		if _, exists := r.commands[name]; exists {
			return Registry{}, fmt.Errorf("duplicate command proxy %q", name)
		}
		reg.Name = name
		r.commands[name] = cloneRegistration(reg)
	}
	for _, reg := range registrations {
		canonical := filepath.Base(reg.Name)
		for _, alias := range reg.Aliases {
			alias, err := normalizeRegistryName("command proxy alias", alias)
			if err != nil {
				return Registry{}, err
			}
			if _, exists := r.commands[alias]; exists {
				return Registry{}, fmt.Errorf("command proxy alias %q conflicts with registered command", alias)
			}
			if existing, exists := r.aliases[alias]; exists {
				return Registry{}, fmt.Errorf("command proxy alias %q is already registered for %q", alias, existing)
			}
			r.aliases[alias] = canonical
		}
	}
	return r, nil
}

func normalizeRegistryName(kind, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("%s name is required", kind)
	}
	if trimmed != name {
		return "", fmt.Errorf("%s %q must not contain surrounding whitespace", kind, name)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%s %q must be a simple command name", kind, name)
	}
	clean := filepath.Base(name)
	if clean != name || clean == "." || clean == ".." || clean == string(filepath.Separator) {
		return "", fmt.Errorf("%s %q must be a simple command name", kind, name)
	}
	return clean, nil
}

func (r Registry) Lookup(name string) (Registration, bool) {
	name = filepath.Base(name)
	if canonical, ok := r.aliases[name]; ok {
		name = canonical
	}
	reg, ok := r.commands[name]
	return cloneRegistration(reg), ok
}

func (r Registry) ShimNames() []string {
	names := make([]string, 0, len(r.commands)+len(r.aliases))
	for name, reg := range r.commands {
		names = append(names, name)
		names = append(names, reg.Aliases...)
	}
	sort.Strings(names)
	return names
}

func (r Registry) Registrations() []Registration {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Registration, 0, len(names))
	for _, name := range names {
		out = append(out, cloneRegistration(r.commands[name]))
	}
	return out
}

func (r Registry) Explain() string {
	names := r.ShimNames()
	if len(names) == 0 {
		return "none"
	}
	return fmt.Sprintf("%v -> %s", names, ActionHostOpen)
}

func (r Registry) ResolveInvocation(program string, args []string) (string, []string, error) {
	command := filepath.Base(program)
	if command == "hideout-shim" {
		if len(args) == 0 {
			return "", nil, errors.New("command proxy name is required")
		}
		commandName := filepath.Base(args[0])
		if _, ok := r.Lookup(commandName); !ok {
			return "", nil, fmt.Errorf("unsupported command proxy %q", args[0])
		}
		return commandName, args[1:], nil
	}
	if _, ok := r.Lookup(command); !ok {
		return "", nil, fmt.Errorf("unsupported command proxy %q", command)
	}
	return command, args, nil
}

func (r Registry) Normalize(command string, args []string, cwd string) (Request, error) {
	command = filepath.Base(command)
	reg, ok := r.Lookup(command)
	if !ok {
		return Request{}, fmt.Errorf("unsupported command proxy %q", command)
	}
	switch reg.ArgvSchema {
	case ArgvSchemaOpenV1:
		cwd, err := normalizeCWD(cwd)
		if err != nil {
			return Request{}, err
		}
		if len(args) < 1 {
			return Request{}, errors.New("open target is required")
		}
		if len(args) != 1 {
			return Request{}, errors.New("open expects exactly one target")
		}
		if strings.TrimSpace(args[0]) == "" {
			return Request{}, errors.New("open target is required")
		}
		argv := append([]string{command}, args...)
		return Request{
			Subject: "command:" + command,
			Command: command,
			Argv:    argv,
			CWD:     cwd,
			Action:  reg.Action,
			Route:   RouteHostBroker,
			Payload: map[string]any{
				"target": args[0],
				"cwd":    cwd,
			},
		}, nil
	default:
		return Request{}, fmt.Errorf("unsupported argv schema %q for command proxy %q", reg.ArgvSchema, command)
	}
}

func normalizeCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("command proxy cwd is required")
	}
	if strings.Contains(cwd, "://") || strings.HasPrefix(cwd, "//") {
		return "", errors.New("command proxy cwd must be an absolute guest path")
	}
	clean := filepath.Clean(cwd)
	if !filepath.IsAbs(clean) {
		return "", errors.New("command proxy cwd must be an absolute guest path")
	}
	return clean, nil
}

func cloneRegistration(reg Registration) Registration {
	reg.Aliases = append([]string(nil), reg.Aliases...)
	reg.AllowedTargets = append([]string(nil), reg.AllowedTargets...)
	return reg
}
