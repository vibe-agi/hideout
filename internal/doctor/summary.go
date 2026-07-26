package doctor

import (
	"fmt"
	"io"
	"strings"
)

const (
	ReadinessReady     = "ready"
	ReadinessAttention = "attention"
	ReadinessBlocked   = "blocked"
)

type ActionableFinding struct {
	SourceFindingID string
	Status          string
	RecoveryCode    string
	Reason          string
	NextAction      string
}

type ReadinessSummary struct {
	State              string
	Profile            string
	Backend            string
	NetworkPosture     string
	BoundarySummary    string
	ActionableFindings []ActionableFinding
	NextCommands       []string
	DetailHint         string
}

// ProjectReadiness is a read-only presentation projection. It does not run
// checks, reinterpret a finding's status, or grant repair authority.
func ProjectReadiness(report Report) ReadinessSummary {
	out := ReadinessSummary{
		State:           ReadinessReady,
		Profile:         strings.TrimSpace(report.Profile),
		Backend:         strings.TrimSpace(report.Backend),
		NetworkPosture:  networkPosture(report.Findings),
		BoundarySummary: isolationBoundary(report.Backend),
		DetailHint:      "hideout doctor --verbose",
	}
	if out.Profile == "" {
		out.Profile = "default"
	}
	if out.Backend == "" {
		out.Backend = "unknown"
	}

	seenActions := map[string]bool{}
	for _, finding := range report.Findings {
		if finding.Status == StatusError && finding.Required {
			out.State = ReadinessBlocked
		}
		if finding.Status != StatusWarn && finding.Status != StatusError {
			continue
		}

		action := firstOrdinaryAction(finding.NextActions)
		if action == "" {
			action = projectedAction(finding)
		}
		if finding.Status == StatusWarn && action == "" {
			continue
		}
		if action == "" {
			action = out.DetailHint
		}
		if seenActions[action] {
			continue
		}
		seenActions[action] = true

		reason := strings.TrimSpace(finding.Reason)
		if reason == "" {
			reason = strings.TrimSpace(finding.Summary)
		}
		out.ActionableFindings = append(out.ActionableFindings, ActionableFinding{
			SourceFindingID: finding.CheckID,
			Status:          finding.Status,
			RecoveryCode:    finding.Code,
			Reason:          reason,
			NextAction:      action,
		})
		out.NextCommands = append(out.NextCommands, action)
	}

	if out.State != ReadinessBlocked && len(out.ActionableFindings) > 0 {
		out.State = ReadinessAttention
	}
	if len(out.NextCommands) == 0 {
		out.NextCommands = []string{"hideout run -- git status --short"}
	}
	return out
}

func WriteReadiness(w io.Writer, summary ReadinessSummary) {
	fmt.Fprintf(w, "Hideout doctor: %s\n", readinessLabel(summary.State))
	fmt.Fprintf(w, "Profile: %s\n", summary.Profile)
	fmt.Fprintf(w, "Isolation: %s\n", summary.BoundarySummary)
	fmt.Fprintf(w, "Network: %s\n", summary.NetworkPosture)
	for _, finding := range summary.ActionableFindings {
		identity := finding.SourceFindingID
		if finding.RecoveryCode != "" {
			identity += "/" + finding.RecoveryCode
		}
		fmt.Fprintf(w, "Problem [%s]: %s\n", identity, finding.Reason)
		fmt.Fprintf(w, "Next: %s\n", finding.NextAction)
	}
	if len(summary.ActionableFindings) == 0 && len(summary.NextCommands) > 0 {
		fmt.Fprintf(w, "Next: %s\n", summary.NextCommands[0])
	}
	fmt.Fprintf(w, "Details: %s\n", summary.DetailHint)
}

func readinessLabel(state string) string {
	switch state {
	case ReadinessBlocked:
		return "Blocked"
	case ReadinessAttention:
		return "Needs attention"
	default:
		return "Ready"
	}
}

func networkPosture(findings []Finding) string {
	for _, finding := range findings {
		if finding.CheckID != "network" {
			continue
		}
		switch {
		case strings.Contains(finding.Summary, "mode=tun2socks"):
			return "private — proxy and mediated DNS are enforced"
		case strings.Contains(finding.Summary, "mode=direct"):
			return "direct — network origin visible"
		}
	}
	return "unknown — use verbose details"
}

func isolationBoundary(backend string) string {
	switch strings.TrimSpace(backend) {
	case "lima":
		return "Lima VM; selected project writable; guest root not contained"
	case "native":
		return "native development harness; no VM isolation"
	default:
		return strings.TrimSpace(backend) + "; verify detailed boundary"
	}
}

func firstOrdinaryAction(actions []string) string {
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action == "" || isMaintainerAction(action) {
			continue
		}
		if strings.HasPrefix(action, "hideout ") ||
			strings.HasPrefix(action, "brew ") {
			return action
		}
	}
	return ""
}

func isMaintainerAction(action string) bool {
	action = strings.TrimSpace(strings.ToLower(action))
	return strings.Contains(action, "scripts/test-") ||
		strings.HasPrefix(action, "scripts/") ||
		strings.HasPrefix(action, "./scripts/") ||
		strings.HasPrefix(action, "go test ") ||
		strings.HasPrefix(action, "make ")
}

func projectedAction(finding Finding) string {
	switch finding.CheckID {
	case "profile", "profile-init", "identity":
		return "hideout setup"
	case "support-matrix":
		return "hideout support matrix"
	case "backend":
		if strings.Contains(strings.ToLower(finding.Summary), "native") {
			return "hideout doctor --backend lima"
		}
		return "brew install lima"
	case "network":
		return "hideout help privacy"
	case "runtime":
		return "hideout doctor --feature runtime --verbose"
	case "packaging":
		return "hideout doctor --feature packaging --verbose"
	}
	return ""
}
