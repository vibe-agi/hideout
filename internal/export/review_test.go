package export

import (
	"strings"
	"testing"
)

func TestPreExportReviewUsesAuthoritativeRedactedFacts(t *testing.T) {
	plan, err := BuildPlan(Request{
		Source:      SourceAudit,
		AuditEvents: []AuditEvent{testAuditEvent(map[string]any{"target": "https://example.com/private", "capabilityToken": "cap_0123456789abcdef0123456789abcdef"})},
		StoreRoot:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !plan.Review.DecisionRequired {
		t.Fatalf("review should require a decision: %+v", plan.Review)
	}
	text := plan.Review.Text()
	for _, want := range []string{"source=audit", "records=1", "Decision required"} {
		if !strings.Contains(text, want) {
			t.Fatalf("review missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "cap_0123456789abcdef") || strings.Contains(text, "https://example.com/private") {
		t.Fatalf("review leaked source/control-plane values:\n%s", text)
	}
}
