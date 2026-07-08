package cmdadapter

import (
	"fmt"

	"github.com/vibe-agi/hideout/internal/audit"
)

type Invocation struct {
	Profile       string
	Session       string
	Command       string
	Argv          []string
	CWD           string
	Adapter       RuntimeAdapter
	FailureReason string
}

func Evidence(inv Invocation, out Outcome) map[string]any {
	details := map[string]any{
		"adapterId":     inv.Adapter.ID,
		"adapterDigest": inv.Adapter.Digest,
		"command":       inv.Command,
		"argvSummary":   ArgvSummary(inv.Argv),
		"cwd":           inv.CWD,
		"outcome":       out.Outcome,
		"reason":        out.Reason,
	}
	if inv.FailureReason != "" {
		details["failureReason"] = inv.FailureReason
	}
	if out.Intent != nil {
		details["intent"] = out.Intent
	}
	if out.Capability != "" {
		details["proposal"] = map[string]any{
			"capability":  out.Capability,
			"status":      "proposed",
			"suggestions": append([]string(nil), out.Suggestions...),
		}
	}
	if len(out.Argv) > 0 {
		details["rewrite"] = map[string]any{"argvSummary": ArgvSummary(out.Argv)}
	}
	if out.Stdout != "" || out.Stderr != "" {
		details["simulation"] = map[string]any{
			"stdout": out.Stdout,
			"stderr": out.Stderr,
		}
	}
	if inv.Adapter.RootSensitive {
		status := "intent-only"
		if value, ok := out.Intent["separationStatus"].(string); ok {
			status = NormalizeSeparationStatus(value)
		}
		details["separationStatus"] = status
	}
	if len(out.Audit) > 0 {
		details["adapterAudit"] = out.Audit
	}
	return audit.RedactDetails(details)
}

func FailureEvidence(inv Invocation, reason string) map[string]any {
	out := Outcome{Outcome: OutcomeDeny, Reason: reason}
	inv.FailureReason = reason
	return Evidence(inv, out)
}

func ArgvSummary(argv []string) []string {
	const maxArgs = 16
	out := append([]string(nil), argv...)
	if len(out) > maxArgs {
		out = append(out[:maxArgs], fmt.Sprintf("...(+%d)", len(argv)-maxArgs))
	}
	return audit.RedactArgv(out)
}
