package cmdadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	OutcomeDeny              = "deny"
	OutcomeSimulate          = "simulate"
	OutcomeRewriteGuest      = "rewriteGuest"
	OutcomeProposeCapability = "proposeCapability"
)

type Outcome struct {
	Outcome     string         `json:"outcome"`
	Reason      string         `json:"reason"`
	ExitCode    int            `json:"exitCode,omitempty"`
	Stdout      string         `json:"stdout,omitempty"`
	Stderr      string         `json:"stderr,omitempty"`
	Intent      map[string]any `json:"intent,omitempty"`
	Audit       map[string]any `json:"audit,omitempty"`
	Argv        []string       `json:"argv,omitempty"`
	CWD         string         `json:"cwd,omitempty"`
	Capability  string         `json:"capability,omitempty"`
	Suggestions []string       `json:"suggestions,omitempty"`
}

func DecodeOutcome(data []byte) (Outcome, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var out Outcome
	if err := dec.Decode(&out); err != nil {
		return Outcome{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Outcome{}, errors.New("command adapter output must contain a single JSON value")
	} else if !errors.Is(err, io.EOF) {
		return Outcome{}, err
	}
	return out, nil
}

func ValidateOutcome(out Outcome, adapter RuntimeAdapter) error {
	if strings.TrimSpace(out.Outcome) == "" {
		return errors.New("command adapter outcome is required")
	}
	if strings.TrimSpace(out.Reason) == "" {
		return errors.New("command adapter reason is required")
	}
	switch out.Outcome {
	case OutcomeDeny:
		return validateDeny(out)
	case OutcomeSimulate:
		return validateSimulate(out, adapter)
	case OutcomeRewriteGuest:
		return validateRewrite(out)
	case OutcomeProposeCapability:
		return validateProposal(out, adapter)
	default:
		return fmt.Errorf("unsupported command adapter outcome %q", out.Outcome)
	}
}

func validateDeny(out Outcome) error {
	if out.ExitCode == 0 {
		return nil
	}
	if out.ExitCode < 0 || out.ExitCode > 255 {
		return errors.New("deny exitCode must be between 0 and 255")
	}
	return nil
}

func validateSimulate(out Outcome, adapter RuntimeAdapter) error {
	if out.ExitCode < 0 || out.ExitCode > 255 {
		return errors.New("simulate exitCode must be between 0 and 255")
	}
	if adapter.RootSensitive && out.ExitCode == 0 {
		return errors.New("root-sensitive adapters must not simulate successful system mutation")
	}
	return nil
}

func validateRewrite(out Outcome) error {
	if len(out.Argv) == 0 {
		return errors.New("rewriteGuest argv is required")
	}
	name := filepath.Base(out.Argv[0])
	if name != out.Argv[0] || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return errors.New("rewriteGuest argv[0] must be a simple guest command")
	}
	if IsRootSensitiveCommand(name) {
		return errors.New("rewriteGuest must not target a root-sensitive command")
	}
	for _, arg := range out.Argv {
		if strings.Contains(arg, "\x00") {
			return errors.New("rewriteGuest argv must not contain NUL")
		}
	}
	if out.CWD != "" && (!filepath.IsAbs(out.CWD) || strings.Contains(out.CWD, "://")) {
		return errors.New("rewriteGuest cwd must be an absolute guest path")
	}
	return nil
}

func validateProposal(out Outcome, adapter RuntimeAdapter) error {
	if strings.TrimSpace(out.Capability) == "" {
		return errors.New("proposeCapability capability is required")
	}
	if !HasCapability(adapter, out.Capability) {
		return fmt.Errorf("capability %q is not declared by adapter %s", out.Capability, adapter.ID)
	}
	if len(out.Intent) == 0 {
		return errors.New("proposeCapability intent is required")
	}
	return nil
}
