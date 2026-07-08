package decision

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecisionValidationAndPublicRedaction(t *testing.T) {
	now := time.Date(2026, 7, 8, 1, 2, 3, 0, time.UTC)
	d := Decision{
		ID:             "dec-test",
		Kind:           KindHostFSWrite,
		State:          StateClaimed,
		DefaultOutcome: DefaultOutcomeDiscard,
		TimeoutAt:      now.Add(time.Minute),
		Claim:          &Claim{Surface: "cli", ClaimedAt: now, ExpiresAt: now.Add(time.Minute), TokenHash: HashClaimToken("claim_0123456789abcdef")},
		Preview:        Preview{Summary: "path /tmp/x token claim_0123456789abcdef"},
		AuditRef:       "audit:test",
	}
	d.Normalize(now)
	if err := ValidateDecision(d); err != nil {
		t.Fatalf("ValidateDecision: %v", err)
	}
	public := RedactDecision(d)
	if public.Claim.TokenHash != "" {
		t.Fatalf("public decision leaked token hash")
	}
	if strings.Contains(public.Preview.Summary, "claim_0123456789abcdef") {
		t.Fatalf("public decision leaked claim token: %q", public.Preview.Summary)
	}
}

func TestNoticeRejectsApprovalSemantics(t *testing.T) {
	now := time.Now().UTC()
	n := Notice{
		ID:       "not-test",
		Kind:     KindPrivilegeStatus,
		Severity: NoticeSeverityWarning,
		Status:   "degraded",
		Preview:  Preview{Summary: "target can sudo"},
		AuditRef: "audit:notice",
	}
	n.Normalize(now)
	if err := ValidateNotice(n); err != nil {
		t.Fatalf("ValidateNotice: %v", err)
	}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"claim", "approve", "deny", "discard", "lease", "timeout", "providerRef", "defaultOutcome"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("notice contains approval field %q in %s", forbidden, data)
		}
	}
}

func TestDecisionValidationRejectsMalformedKindsAndTransitions(t *testing.T) {
	now := time.Now().UTC()
	d := Decision{
		ID:             "dec-bad",
		Kind:           "future.unknown",
		State:          StatePending,
		DefaultOutcome: DefaultOutcomeDeny,
		TimeoutAt:      now.Add(time.Minute),
		Preview:        Preview{Summary: "test"},
		AuditRef:       "audit:test",
		Version:        DecisionVersion,
	}
	if err := ValidateDecision(d); err == nil {
		t.Fatalf("expected unknown kind rejection")
	}
	d.Kind = KindAdapterProposal
	d.State = StateClaimed
	if err := ValidateDecision(d); err == nil {
		t.Fatalf("expected claimed decision without claim rejection")
	}
	d.State = StatePending
	d.Claim = &Claim{Surface: "cli"}
	if err := ValidateDecision(d); err == nil {
		t.Fatalf("expected claim metadata outside claimed state rejection")
	}
}
