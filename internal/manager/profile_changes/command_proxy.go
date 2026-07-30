package profilechanges

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/profile"
)

type commandProxyValue struct {
	Operation string `json:"operation"`
	Command   string `json:"command"`
}

func normalizeCommandProxy(
	raw json.RawMessage,
) (commandProxyValue, error) {
	var value commandProxyValue
	if err := decodeStrict(raw, &value); err != nil {
		return commandProxyValue{}, err
	}
	if value.Operation != "add-open" &&
		value.Operation != "remove" {
		return commandProxyValue{}, errors.New(
			"command proxy operation must be add-open or remove",
		)
	}
	if strings.TrimSpace(value.Command) != value.Command ||
		value.Command == "" ||
		len(value.Command) > 128 ||
		containsControl(value.Command) {
		return commandProxyValue{}, errors.New(
			"command proxy name is invalid",
		)
	}
	probe := profile.Default("__typed_command_proxy_validation__")
	if value.Operation == "add-open" {
		probe.CommandProxy.Commands[value.Command] =
			hostOpenCommandProxy()
	} else {
		if value.Command == "open" {
			return commandProxyValue{}, errors.New(
				"required command proxy open cannot be removed",
			)
		}
		probe.CommandProxy.Commands[value.Command] =
			hostOpenCommandProxy()
	}
	if err := probe.Validate(); err != nil {
		return commandProxyValue{}, err
	}
	return value, nil
}

func applyCommandProxy(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, error) {
	value, err := normalizeCommandProxy(raw)
	if err != nil {
		return nil, err
	}
	if desired.CommandProxy.Commands == nil {
		desired.CommandProxy.Commands =
			map[string]profile.CommandProxyCommand{}
	}
	_, exists := desired.CommandProxy.Commands[value.Command]
	after := "absent"
	switch value.Operation {
	case "add-open":
		desired.CommandProxy.Commands[value.Command] =
			hostOpenCommandProxy()
		after = "host.open"
	case "remove":
		delete(desired.CommandProxy.Commands, value.Command)
	default:
		return nil, errors.New("unsupported command proxy operation")
	}
	return []Diff{{
		Kind:   KindProfileCommandProxy,
		Field:  "commandProxy." + value.Command,
		Before: state(exists, "configured", "absent"),
		After:  after,
		Scope:  "new-sessions",
	}}, nil
}

func hostOpenCommandProxy() profile.CommandProxyCommand {
	return profile.CommandProxyCommand{
		Route:      cmdproxy.RouteHostBroker,
		Action:     cmdproxy.ActionHostOpen,
		ArgvSchema: cmdproxy.ArgvSchemaOpenV1,
	}
}
