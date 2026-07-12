package runtimecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

const (
	ContractSchema        = "hideout.runtime-contract/v1"
	ObservationBoundary   = "boundary"
	ObservationBaseline   = "baseline"
	maxObservations       = 64
	maxVersionArgs        = 4
	maxVersionArgBytes    = 128
	maxOutputPatternBytes = 256
	maxDescriptionBytes   = 256
)

var commandNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:-]*$`)

var forbiddenProbeCommands = map[string]struct{}{
	"bash": {},
	"dash": {},
	"env":  {},
	"fish": {},
	"sh":   {},
	"sudo": {},
	"zsh":  {},
}

type Contract struct {
	Schema       string        `json:"schema"`
	ID           string        `json:"id"`
	Observations []Observation `json:"observations"`
}

type Observation struct {
	ID            string   `json:"id"`
	Class         string   `json:"class"`
	Command       string   `json:"command"`
	VersionArgs   []string `json:"versionArgs,omitempty"`
	OutputPattern string   `json:"outputPattern,omitempty"`
	Description   string   `json:"description"`
}

func ParseContract(data []byte) (Contract, error) {
	var contract Contract
	if err := decodeStrict(data, &contract); err != nil {
		return Contract{}, fmt.Errorf("decode runtime contract: %w", err)
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

func (c Contract) Validate() error {
	if c.Schema != ContractSchema {
		return fmt.Errorf("unsupported runtime contract schema %q", c.Schema)
	}
	if err := validateID("contract id", c.ID); err != nil {
		return err
	}
	if len(c.Observations) > maxObservations {
		return fmt.Errorf("runtime contract allows at most %d observations", maxObservations)
	}
	seen := map[string]struct{}{}
	for i, observation := range c.Observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("observations[%d]: %w", i, err)
		}
		if _, exists := seen[observation.ID]; exists {
			return fmt.Errorf("duplicate observation id %q", observation.ID)
		}
		seen[observation.ID] = struct{}{}
	}
	return nil
}

func (o Observation) Validate() error {
	if err := validateID("observation id", o.ID); err != nil {
		return err
	}
	if o.Class != ObservationBoundary && o.Class != ObservationBaseline {
		return fmt.Errorf("unsupported observation class %q", o.Class)
	}
	if !commandNamePattern.MatchString(o.Command) || strings.ContainsAny(o.Command, `/\`) {
		return fmt.Errorf("observation command %q must be a simple command name", o.Command)
	}
	if _, forbidden := forbiddenProbeCommands[strings.ToLower(o.Command)]; forbidden {
		return fmt.Errorf("observation command %q is a forbidden shell or authority wrapper", o.Command)
	}
	if len(o.VersionArgs) > maxVersionArgs {
		return fmt.Errorf("observation %q has too many version args", o.ID)
	}
	for _, arg := range o.VersionArgs {
		if err := validateVersionArg(arg); err != nil {
			return fmt.Errorf("observation %q: %w", o.ID, err)
		}
	}
	if len(o.OutputPattern) > maxOutputPatternBytes {
		return fmt.Errorf("observation %q output pattern is too long", o.ID)
	}
	if o.OutputPattern != "" {
		if !strings.HasPrefix(o.OutputPattern, "^") || !strings.HasSuffix(o.OutputPattern, "$") {
			return fmt.Errorf("observation %q output pattern must be anchored", o.ID)
		}
		if _, err := regexp.Compile(o.OutputPattern); err != nil {
			return fmt.Errorf("observation %q output pattern: %w", o.ID, err)
		}
	}
	if strings.TrimSpace(o.Description) == "" || len(o.Description) > maxDescriptionBytes || containsControl(o.Description) {
		return fmt.Errorf("observation %q description must be printable and bounded", o.ID)
	}
	return nil
}

func validateVersionArg(arg string) error {
	if arg == "" || len(arg) > maxVersionArgBytes || containsControl(arg) {
		return errors.New("version arg must be non-empty, printable, and bounded")
	}
	if arg == "-c" || arg == "--command" || strings.ContainsAny(arg, `;&|<>$`) || strings.Contains(arg, "=") {
		return fmt.Errorf("version arg %q contains forbidden shell or environment syntax", arg)
	}
	return nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}
