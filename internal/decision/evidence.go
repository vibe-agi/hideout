package decision

import (
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
)

const (
	ActionDecisionCreate  = "decision.create"
	ActionDecisionClaim   = "decision.claim"
	ActionDecisionApply   = "decision.apply"
	ActionDecisionDeny    = "decision.deny"
	ActionDecisionTimeout = "decision.timeout"
	ActionDecisionReopen  = "decision.reopen"
	ActionDecisionRevoke  = "decision.revoke"
	ActionDecisionStale   = "decision.stale-claim"
	ActionNoticeCreate    = "notice.create"
	ActionNoticeAck       = "notice.ack"
)

func DecisionEvent(action, auditDecision string, d Decision, details map[string]any) audit.Event {
	if details == nil {
		details = map[string]any{}
	}
	details["decisionId"] = d.ID
	details["kind"] = d.Kind
	details["state"] = d.State
	details["source"] = d.Source
	return audit.Event{
		Time:     time.Now().UTC(),
		Session:  d.Source.Session,
		Profile:  d.Source.Profile,
		Backend:  d.Source.Backend,
		Action:   action,
		Decision: auditDecision,
		Details:  audit.RedactDetails(details),
	}
}

func NoticeEvent(action, auditDecision string, n Notice, details map[string]any) audit.Event {
	if details == nil {
		details = map[string]any{}
	}
	details["noticeId"] = n.ID
	details["kind"] = n.Kind
	details["status"] = n.Status
	details["severity"] = n.Severity
	details["source"] = n.Source
	return audit.Event{
		Time:     time.Now().UTC(),
		Session:  n.Source.Session,
		Profile:  n.Source.Profile,
		Backend:  n.Source.Backend,
		Action:   action,
		Decision: auditDecision,
		Details:  audit.RedactDetails(details),
	}
}
