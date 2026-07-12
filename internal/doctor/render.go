package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func WriteHuman(w io.Writer, report Report) {
	fmt.Fprintln(w, "Hideout doctor")
	fmt.Fprintf(w, "profile: %s backend: %s level: %s\n", report.Profile, report.Backend, report.Level)
	for _, finding := range report.Findings {
		status := finding.Status
		if status == StatusPass {
			status = "ok"
		}
		if finding.Summary == "" {
			fmt.Fprintf(w, "%s: %s\n", finding.CheckID, status)
		} else {
			fmt.Fprintf(w, "%s: %s %s\n", finding.CheckID, status, finding.Summary)
		}
		fmt.Fprintf(w, "  severity: %s required=%t\n", finding.Severity, finding.Required)
		if finding.Code != "" {
			fmt.Fprintf(w, "  code: %s\n", finding.Code)
		}
		if finding.Reason != "" {
			fmt.Fprintf(w, "  reason: %s\n", finding.Reason)
		}
		if finding.Hint != "" {
			fmt.Fprintf(w, "  hint: %s\n", finding.Hint)
		}
		writeHumanDetailStrings(w, "observed", finding.Details["observedFacts"])
		writeHumanDetailStrings(w, "candidate", finding.Details["candidateCauses"])
		writeHumanDetailStrings(w, "gate-required", finding.Details["gateRequired"])
		for _, action := range finding.NextActions {
			fmt.Fprintf(w, "  next: %s\n", action)
		}
		for _, ref := range finding.EvidenceRefs {
			fmt.Fprintf(w, "  evidence: %s\n", ref)
		}
	}
	fmt.Fprintf(w, "summary: pass=%d warn=%d error=%d skipped=%d unsupported=%d\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Error, report.Summary.Skipped, report.Summary.Unsupported)
}

func writeHumanDetailStrings(w io.Writer, label string, value any) {
	switch items := value.(type) {
	case []string:
		for _, item := range items {
			fmt.Fprintf(w, "  %s: %s\n", label, item)
		}
	case []any:
		for _, item := range items {
			fmt.Fprintf(w, "  %s: %v\n", label, item)
		}
	}
}

func FindingIDs(report Report) []string {
	ids := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		ids = append(ids, finding.CheckID)
	}
	sort.Strings(ids)
	return ids
}
