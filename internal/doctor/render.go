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
	}
	fmt.Fprintf(w, "summary: pass=%d warn=%d error=%d skipped=%d unsupported=%d\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Error, report.Summary.Skipped, report.Summary.Unsupported)
}

func FindingIDs(report Report) []string {
	ids := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		ids = append(ids, finding.CheckID)
	}
	sort.Strings(ids)
	return ids
}
