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

func TestHostFSReadDecisionRedactsUntrustedControlPlaneTextAndProviderState(t *testing.T) {
	d := Decision{
		Kind:  KindHostFSRead,
		State: StatePending,
		ProposedAction: map[string]any{
			"operation":       "read",
			"untrustedReason": "inspect HIDEOUT_SECRET_PROXY=secret cap_0123456789abcdef",
		},
		Preview: Preview{Summary: "target says HIDEOUT_SECRET_PROXY=secret"},
		ProviderRef: ProviderRef{
			Provider:  KindHostFSRead,
			SessionID: "ses_private",
			Data:      map[string]any{"grantPath": "/private/store/grants.json"},
		},
	}
	redacted := RedactDecision(d)
	if redacted.ProviderRef.Provider != "" || redacted.ProviderRef.SessionID != "" || redacted.ProviderRef.Data != nil {
		t.Fatalf("public decision leaked provider state: %+v", redacted.ProviderRef)
	}
	for _, value := range []string{redacted.Preview.Summary, redacted.ProposedAction["untrustedReason"].(string)} {
		if strings.Contains(value, "secret") || strings.Contains(value, "cap_0123456789abcdef") {
			t.Fatalf("public decision leaked control-plane text: %q", value)
		}
	}
}
