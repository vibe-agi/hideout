package adapterpack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	slashpath "path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

func LoadManifest(path string) (Manifest, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Manifest{}, nil, errors.New("adapter pack manifest must contain a single JSON value")
	}
	if err := ValidateManifest(manifest, filepath.Dir(path)); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, data, nil
}

func ValidateManifest(manifest Manifest, sourceRoot string) error {
	if manifest.SchemaVersion != ManifestVersion {
		return fmt.Errorf("unsupported adapter pack schema %q", manifest.SchemaVersion)
	}
	if err := ValidatePackID(manifest.ID); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if strings.TrimSpace(manifest.Version) == "" || strings.TrimSpace(manifest.Version) != manifest.Version {
		return errors.New("version must be a non-empty normalized string")
	}
	if strings.TrimSpace(manifest.Description) != manifest.Description {
		return errors.New("description must not contain surrounding whitespace")
	}
	if len(manifest.Adapters) == 0 {
		return errors.New("adapters is required")
	}
	adapterIDs := map[string]struct{}{}
	for i, adapter := range manifest.Adapters {
		label := fmt.Sprintf("adapters[%d]", i)
		if err := validateAdapterSpec(label, adapter, sourceRoot); err != nil {
			return err
		}
		if _, ok := adapterIDs[adapter.ID]; ok {
			return fmt.Errorf("%s.id duplicates adapter %q", label, adapter.ID)
		}
		adapterIDs[adapter.ID] = struct{}{}
	}
	testIDs := map[string]struct{}{}
	for i, test := range manifest.Tests {
		label := fmt.Sprintf("tests[%d]", i)
		if err := validateTestVector(label, test, adapterIDs); err != nil {
			return err
		}
		if _, ok := testIDs[test.ID]; ok {
			return fmt.Errorf("%s.id duplicates test %q", label, test.ID)
		}
		testIDs[test.ID] = struct{}{}
	}
	return nil
}

func validateAdapterSpec(label string, adapter AdapterSpec, sourceRoot string) error {
	if err := ValidatePackID(adapter.ID); err != nil {
		return fmt.Errorf("%s.id: %w", label, err)
	}
	if adapter.Entrypoint != "" && !validJSIdentifier(adapter.Entrypoint) {
		return fmt.Errorf("%s.entrypoint must be a simple JavaScript identifier", label)
	}
	if err := ValidatePackPath(adapter.Script); err != nil {
		return fmt.Errorf("%s.script: %w", label, err)
	}
	if sourceRoot != "" {
		info, err := os.Stat(filepath.Join(sourceRoot, filepath.FromSlash(adapter.Script)))
		if err != nil {
			return fmt.Errorf("%s.script: %w", label, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s.script must be a regular file", label)
		}
	}
	if len(adapter.Commands) == 0 {
		return fmt.Errorf("%s.commands is required", label)
	}
	seenCommands := map[string]struct{}{}
	for i, command := range adapter.Commands {
		if err := ValidateCommandName(command); err != nil {
			return fmt.Errorf("%s.commands[%d]: %w", label, i, err)
		}
		if _, ok := seenCommands[command]; ok {
			return fmt.Errorf("%s.commands[%d] duplicates command %q", label, i, command)
		}
		seenCommands[command] = struct{}{}
	}
	seenCaps := map[string]struct{}{}
	for i, capability := range adapter.AllowedProposalCapabilities {
		if !KnownProposalCapability(capability) {
			return fmt.Errorf("%s.allowedProposalCapabilities[%d] %q is unsupported", label, i, capability)
		}
		if _, ok := seenCaps[capability]; ok {
			return fmt.Errorf("%s.allowedProposalCapabilities[%d] duplicates capability %q", label, i, capability)
		}
		seenCaps[capability] = struct{}{}
	}
	if strings.TrimSpace(adapter.Description) != adapter.Description {
		return fmt.Errorf("%s.description must not contain surrounding whitespace", label)
	}
	return nil
}

func validateTestVector(label string, test TestVector, adapters map[string]struct{}) error {
	if err := ValidatePackID(test.ID); err != nil {
		return fmt.Errorf("%s.id: %w", label, err)
	}
	if _, ok := adapters[test.AdapterID]; !ok {
		return fmt.Errorf("%s.adapterId %q does not name a pack adapter", label, test.AdapterID)
	}
	if err := ValidateCommandName(test.Context.Command.Name); err != nil {
		return fmt.Errorf("%s.context.command.name: %w", label, err)
	}
	if len(test.Context.Command.Argv) == 0 {
		return fmt.Errorf("%s.context.command.argv is required", label)
	}
	if test.Context.Command.CWD != "" && !filepath.IsAbs(test.Context.Command.CWD) {
		return fmt.Errorf("%s.context.command.cwd must be absolute when set", label)
	}
	switch test.Expect.Outcome {
	case "deny", "simulate", "rewriteGuest", "proposeCapability":
	default:
		return fmt.Errorf("%s.expect.outcome %q is unsupported", label, test.Expect.Outcome)
	}
	if test.Expect.Capability != "" && !KnownProposalCapability(test.Expect.Capability) {
		return fmt.Errorf("%s.expect.capability %q is unsupported", label, test.Expect.Capability)
	}
	return nil
}

func ValidatePackID(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("id %q must not contain surrounding whitespace", value)
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("id %q contains unsupported characters", value)
		}
	}
	return nil
}

func ValidateCommandName(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("command is required")
	}
	if strings.TrimSpace(value) != value || value == "." || value == ".." || strings.ContainsAny(value, `/\`) || strings.ContainsFunc(value, unicode.IsSpace) {
		return fmt.Errorf("command %q must be a simple command name", value)
	}
	return nil
}

func ValidatePackPath(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("path is required")
	}
	if strings.TrimSpace(value) != value {
		return errors.New("path must not contain surrounding whitespace")
	}
	if strings.Contains(value, "\x00") || strings.Contains(value, `\`) || filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return errors.New("path must be a relative slash-separated path")
	}
	clean := slashpath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || clean != value {
		return errors.New("path must be normalized and stay inside the pack")
	}
	return nil
}

func KnownProposalCapability(value string) bool {
	return slices.Contains([]string{CapabilityGuestPrivilegePlan, CapabilityHostFSWritePlan}, value)
}

func validJSIdentifier(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_', r == '$':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
