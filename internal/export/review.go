package export

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Review struct {
	Source           SourceKind       `json:"source"`
	RecordCount      int              `json:"recordCount"`
	RedactionStages  []RedactionStage `json:"redactionStages"`
	RedactSelectors  []string         `json:"redactSelectors,omitempty"`
	ResidualKeys     []string         `json:"residualKeys,omitempty"`
	DecisionRequired bool             `json:"decisionRequired"`
	Decision         ExportDecision   `json:"decision"`
}

func BuildReview(source SourceKind, recordCount int, stages []RedactionStage, selectors, residual []string, decision ExportDecision, required bool) Review {
	return Review{
		Source:           source,
		RecordCount:      recordCount,
		RedactionStages:  append([]RedactionStage(nil), stages...),
		RedactSelectors:  append([]string(nil), selectors...),
		ResidualKeys:     append([]string(nil), residual...),
		DecisionRequired: required,
		Decision:         decision,
	}
}

func (r Review) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Export review: source=%s records=%d\n", r.Source, r.RecordCount)
	if len(r.RedactionStages) > 0 {
		fmt.Fprintf(&b, "Redaction stages:")
		for _, stage := range r.RedactionStages {
			if stage.ID != "" {
				fmt.Fprintf(&b, " %s:%s", stage.Name, stage.ID)
			} else {
				fmt.Fprintf(&b, " %s", stage.Name)
			}
		}
		b.WriteByte('\n')
	}
	if len(r.RedactSelectors) > 0 {
		fmt.Fprintf(&b, "Redacted selectors: %s\n", strings.Join(r.RedactSelectors, ","))
	}
	if len(r.ResidualKeys) > 0 {
		fmt.Fprintf(&b, "Included residual user data: %s\n", strings.Join(r.ResidualKeys, ","))
	}
	if r.DecisionRequired {
		b.WriteString("Decision required: confirm inclusion or choose redaction\n")
	} else if r.Decision.Mode != "" {
		fmt.Fprintf(&b, "Decision: %s via %s\n", r.Decision.Mode, r.Decision.Channel)
	}
	return b.String()
}

func (r Review) MarshalStableJSON() ([]byte, error) {
	return json.Marshal(r)
}
