package decision

import (
	"strings"
	"testing"
	"time"
)

func TestEvidenceRedactsDecisionAndNoticeLifecycle(t *testing.T) {
	d := sampleDecision("dec-evidence", time.Now().Add(time.Minute))
	d.Preview.Summary = "claim_0123456789abcdef HIDEOUT_SECRET_PROXY=secret"
	ev := DecisionEvent(ActionDecisionClaim, "allow", d, map[string]any{
		"claimToken": "claim_0123456789abcdef",
		"summary":    d.Preview.Summary,
	})
	redacted := RedactDecision(d)
	if strings.Contains(redacted.Preview.Summary, "claim_0123456789abcdef") || strings.Contains(redacted.Preview.Summary, "secret") {
		t.Fatalf("decision preview leaked: %q", redacted.Preview.Summary)
	}
	if ev.Action != ActionDecisionClaim || ev.Decision != "allow" {
		t.Fatalf("bad event: %#v", ev)
	}
	if got := ev.Details["claimToken"]; got != "REDACTED" {
		t.Fatalf("claim token not redacted: %#v", ev.Details)
	}

	n := sampleNotice("not-evidence")
	nev := NoticeEvent(ActionNoticeAck, "allow", n, map[string]any{
		"uiToken": "ui_0123456789abcdef",
	})
	if nev.Action != ActionNoticeAck || nev.Details["uiToken"] != "REDACTED" {
		t.Fatalf("bad notice event redaction: %#v", nev)
	}
}
