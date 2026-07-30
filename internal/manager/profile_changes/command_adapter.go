package profilechanges

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/profile"
)

type commandAdapterValue struct {
	Operation                   string   `json:"operation"`
	AdapterID                   string   `json:"adapterId"`
	Path                        string   `json:"path,omitempty"`
	Entrypoint                  string   `json:"entrypoint,omitempty"`
	Commands                    []string `json:"commands,omitempty"`
	AllowedProposalCapabilities []string `json:"allowedProposalCapabilities,omitempty"`
}

func normalizeCommandAdapter(
	raw json.RawMessage,
) (commandAdapterValue, error) {
	var value commandAdapterValue
	if err := decodeStrict(raw, &value); err != nil {
		return commandAdapterValue{}, err
	}
	if strings.TrimSpace(value.AdapterID) != value.AdapterID ||
		value.AdapterID == "" ||
		len(value.AdapterID) > 128 ||
		containsControl(value.AdapterID) {
		return commandAdapterValue{}, errors.New("adapterId is invalid")
	}
	switch value.Operation {
	case "add-local":
		if strings.TrimSpace(value.Path) != value.Path ||
			value.Path == "" ||
			len(value.Path) > 4096 ||
			containsControl(value.Path) ||
			len(value.Commands) == 0 {
			return commandAdapterValue{}, errors.New(
				"add-local requires a bounded path and commands",
			)
		}
		if value.Entrypoint == "" {
			value.Entrypoint = cmdadapter.DefaultEntrypoint
		}
	case "add-builtin-root-sensitive":
		if value.Path != "" ||
			value.Entrypoint != "" ||
			len(value.Commands) != 0 ||
			len(value.AllowedProposalCapabilities) != 0 {
			return commandAdapterValue{}, errors.New(
				"built-in adapter does not accept local source fields",
			)
		}
	case "enable", "disable", "refresh-digest", "remove":
		if value.Path != "" ||
			value.Entrypoint != "" ||
			len(value.Commands) != 0 ||
			len(value.AllowedProposalCapabilities) != 0 {
			return commandAdapterValue{}, errors.New(
				"existing-adapter operation accepts only adapterId",
			)
		}
	default:
		return commandAdapterValue{}, errors.New(
			"command adapter operation is unsupported",
		)
	}
	if err := normalizeUniqueStrings(
		&value.Commands,
		"commands",
	); err != nil {
		return commandAdapterValue{}, err
	}
	if err := normalizeUniqueStrings(
		&value.AllowedProposalCapabilities,
		"allowedProposalCapabilities",
	); err != nil {
		return commandAdapterValue{}, err
	}
	return value, nil
}

func applyCommandAdapter(
	desired *profile.Profile,
	raw json.RawMessage,
	options Options,
) ([]Diff, []Warning, error) {
	value, err := normalizeCommandAdapter(raw)
	if err != nil {
		return nil, nil, err
	}
	if desired.CommandAdapters.Adapters == nil {
		desired.CommandAdapters.Adapters =
			map[string]profile.CommandAdapter{}
	}
	before, exists := desired.CommandAdapters.Adapters[value.AdapterID]
	var (
		after    profile.CommandAdapter
		present  = true
		warnings []Warning
	)
	switch value.Operation {
	case "add-local":
		runtimeAdapter := cmdadapter.RuntimeAdapter{
			ID:   value.AdapterID,
			Path: value.Path,
			Commands: append(
				[]string(nil),
				value.Commands...,
			),
		}
		_, digest, err := cmdadapter.ResolveSource(
			options.ProfileDir,
			runtimeAdapter,
		)
		if err != nil {
			return nil, nil, err
		}
		after = profile.CommandAdapter{
			Enabled:    true,
			Path:       value.Path,
			Digest:     digest,
			Entrypoint: value.Entrypoint,
			Commands: append(
				[]string(nil),
				value.Commands...,
			),
			AllowedProposalCapabilities: append(
				[]string(nil),
				value.AllowedProposalCapabilities...,
			),
		}
	case "add-builtin-root-sensitive":
		after = cmdadapter.BuiltinRootSensitiveProfileAdapter()
		warnings = append(warnings, Warning{
			Code: "root-sensitive-intent-adapter",
			Summary: "The built-in root-sensitive adapter observes command " +
				"intent but does not itself enforce guest privilege.",
		})
	case "enable":
		if !exists {
			return nil, nil, fmt.Errorf(
				"command adapter %q is not configured",
				value.AdapterID,
			)
		}
		after = before
		after.Enabled = true
		if _, err := cmdadapter.CompileAdapter(
			options.ProfileDir,
			value.AdapterID,
			after,
		); err != nil {
			return nil, nil, err
		}
	case "disable":
		if !exists {
			return nil, nil, fmt.Errorf(
				"command adapter %q is not configured",
				value.AdapterID,
			)
		}
		after = before
		after.Enabled = false
	case "refresh-digest":
		if !exists {
			return nil, nil, fmt.Errorf(
				"command adapter %q is not configured",
				value.AdapterID,
			)
		}
		after = before
		_, digest, err := cmdadapter.ResolveSource(
			options.ProfileDir,
			cmdadapter.RuntimeAdapter{
				ID:      value.AdapterID,
				Path:    after.Path,
				Builtin: after.Builtin,
			},
		)
		if err != nil {
			return nil, nil, err
		}
		after.Digest = digest
	case "remove":
		present = false
	default:
		return nil, nil, errors.New(
			"unsupported command adapter operation",
		)
	}
	if present {
		desired.CommandAdapters.Adapters[value.AdapterID] = after
	} else {
		delete(desired.CommandAdapters.Adapters, value.AdapterID)
	}
	beforeState := "absent"
	if exists {
		beforeState = state(before.Enabled, "enabled", "disabled")
	}
	afterState := "absent"
	if present {
		afterState = state(after.Enabled, "enabled", "disabled")
	}
	if exists && present && reflect.DeepEqual(before, after) {
		afterState = beforeState
	}
	return []Diff{{
		Kind:   KindProfileCommandAdapter,
		Field:  "commandAdapters." + value.AdapterID,
		Before: beforeState, After: afterState,
		Scope: "new-sessions",
	}}, warnings, nil
}

func normalizeUniqueStrings(values *[]string, label string) error {
	seen := make(map[string]struct{}, len(*values))
	for _, value := range *values {
		if strings.TrimSpace(value) != value ||
			value == "" ||
			len(value) > 256 ||
			containsControl(value) {
			return fmt.Errorf("%s contains an invalid value", label)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s contains duplicate %q", label, value)
		}
		seen[value] = struct{}{}
	}
	sort.Strings(*values)
	return nil
}
