package decision

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DecisionVersion        = "hideout.decision/v1"
	DecisionClaimVersion   = "hideout.decision-claim/v1"
	DecisionReleaseVersion = "hideout.decision-claim-release/v1"
	DecisionResultVersion  = "hideout.decision-result/v1"
	NoticeVersion          = "hideout.notice/v1"
	StatusVersion          = "hideout.decision-status/v1"

	KindHostFSWrite         = "hostfs.write"
	KindHostFSRead          = "hostfs.read"
	KindHostAppOpenResource = "host-app.open-resource"
	KindAdapterProposal     = "adapter.proposal"
	KindEvidenceShare       = "evidence.share"
	KindPrivilegeStatus     = "privilege.status"
	KindBackgroundStatus    = "background.status"
	KindRuntimeStatus       = "runtime.status"

	StatePending   = "pending"
	StateClaimed   = "claimed"
	StateApproved  = "approved"
	StateApplied   = "applied"
	StateDenied    = "denied"
	StateDiscarded = "discarded"
	StateTimedOut  = "timed-out"
	StateFailed    = "failed"
	StateStale     = "stale"

	NoticeSeverityInfo    = "info"
	NoticeSeverityWarning = "warning"
	NoticeSeverityError   = "error"

	DefaultOutcomeDeny      = "deny"
	DefaultOutcomeDiscard   = "discard"
	DefaultOutcomeNoRelease = "no-release"

	ActionApprove = "approve"
	ActionApply   = "apply"
	ActionDeny    = "deny"
	ActionDiscard = "discard"
	ActionReopen  = "reopen"
	ActionRevoke  = "revoke"
)

type Source struct {
	Profile string `json:"profile,omitempty"`
	Session string `json:"session,omitempty"`
	Backend string `json:"backend,omitempty"`
	Surface string `json:"surface,omitempty"`
}

type Preview struct {
	Summary         string         `json:"summary,omitempty"`
	Facts           map[string]any `json:"facts,omitempty"`
	Diff            any            `json:"diff,omitempty"`
	UserDataPresent bool           `json:"userDataPresent,omitempty"`
}

type Claim struct {
	Surface   string    `json:"surface"`
	Operator  string    `json:"operator,omitempty"`
	ClaimedAt time.Time `json:"claimedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	TokenHash string    `json:"tokenHash,omitempty"`
}

type ProviderRef struct {
	Provider    string         `json:"provider"`
	OperationID string         `json:"operationId,omitempty"`
	DecisionID  string         `json:"decisionId,omitempty"`
	SessionID   string         `json:"sessionId,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
}

type Decision struct {
	Version        string         `json:"version"`
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Source         Source         `json:"source,omitempty"`
	State          string         `json:"state"`
	Risk           map[string]any `json:"risk,omitempty"`
	ProposedAction map[string]any `json:"proposedAction,omitempty"`
	Preview        Preview        `json:"preview"`
	AllowedActions []string       `json:"allowedActions,omitempty"`
	DefaultOutcome string         `json:"defaultOutcome"`
	TimeoutAt      time.Time      `json:"timeoutAt"`
	Claim          *Claim         `json:"claim,omitempty"`
	ProviderRef    ProviderRef    `json:"providerRef,omitempty"`
	AuditRef       string         `json:"auditRef"`
	Revision       int            `json:"revision"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

type ClaimResponse struct {
	Version        string    `json:"version"`
	DecisionID     string    `json:"decisionId"`
	State          string    `json:"state"`
	ClaimToken     string    `json:"claimToken"`
	Surface        string    `json:"surface"`
	ClaimedAt      time.Time `json:"claimedAt"`
	ClaimExpiresAt time.Time `json:"claimExpiresAt"`
	LeaseSeconds   int64     `json:"leaseSeconds"`
	Takeover       bool      `json:"takeover,omitempty"`
	Revision       int       `json:"revision"`
}

type ClaimOptions struct {
	Surface          string
	Operator         string
	Lease            time.Duration
	ExpectedRevision int
	TakeoverExpired  bool
}

type ClaimRelease struct {
	Version           string    `json:"version"`
	DecisionID        string    `json:"decisionId"`
	State             string    `json:"state"`
	Reason            string    `json:"reason"`
	ReleasedAt        time.Time `json:"releasedAt"`
	PreviousSurface   string    `json:"previousSurface,omitempty"`
	PreviousClaimedAt time.Time `json:"previousClaimedAt,omitzero"`
	PreviousExpiresAt time.Time `json:"previousExpiresAt,omitzero"`
	Expired           bool      `json:"expired,omitempty"`
	Revision          int       `json:"revision"`
}

type ReleasedClaim struct {
	Release  ClaimRelease
	Decision Decision
}

type Resolution struct {
	Version        string         `json:"version"`
	DecisionID     string         `json:"decisionId"`
	Kind           string         `json:"kind"`
	Status         string         `json:"status"`
	Decision       string         `json:"decision"`
	Reason         string         `json:"reason,omitempty"`
	ProviderResult map[string]any `json:"providerResult,omitempty"`
	AuditRef       string         `json:"auditRef,omitempty"`
	Revision       int            `json:"revision"`
}

type Notice struct {
	Version        string         `json:"version"`
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Source         Source         `json:"source,omitempty"`
	Severity       string         `json:"severity"`
	Status         string         `json:"status"`
	Payload        map[string]any `json:"payload,omitempty"`
	Preview        Preview        `json:"preview"`
	Acknowledged   bool           `json:"acknowledged"`
	AcknowledgedBy string         `json:"acknowledgedBy,omitempty"`
	AcknowledgedAt time.Time      `json:"acknowledgedAt,omitempty"`
	AuditRef       string         `json:"auditRef"`
	Revision       int            `json:"revision"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

type Acknowledgement struct {
	NoticeID       string    `json:"noticeId"`
	Surface        string    `json:"surface"`
	AcknowledgedAt time.Time `json:"acknowledgedAt"`
	AuditRef       string    `json:"auditRef"`
	Revision       int       `json:"revision"`
}

type ListFilter struct {
	Kind            string
	State           string
	Profile         string
	Session         string
	Severity        string
	IncludeTerminal bool
}

type Status struct {
	Version           string    `json:"version"`
	GeneratedAt       time.Time `json:"generatedAt"`
	PendingDecisions  int       `json:"pendingDecisions"`
	ClaimedDecisions  int       `json:"claimedDecisions"`
	TerminalDecisions int       `json:"terminalDecisions"`
	UnackedNotices    int       `json:"unackedNotices"`
	OldestPendingAge  string    `json:"oldestPendingAge,omitempty"`
	TimeoutRisk       int       `json:"timeoutRisk"`
	DecisionIDs       []string  `json:"decisionIds,omitempty"`
	NoticeIDs         []string  `json:"noticeIds,omitempty"`
}

func (d *Decision) Normalize(now time.Time) {
	if d.Version == "" {
		d.Version = DecisionVersion
	}
	d.ID = strings.TrimSpace(d.ID)
	d.Kind = strings.TrimSpace(d.Kind)
	if d.State == "" {
		d.State = StatePending
	}
	if d.DefaultOutcome == "" {
		d.DefaultOutcome = defaultOutcomeForKind(d.Kind)
	}
	if d.AuditRef == "" {
		d.AuditRef = "audit:decision:" + d.ID
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if d.Revision <= 0 {
		d.Revision = 1
	}
}

func (n *Notice) Normalize(now time.Time) {
	if n.Version == "" {
		n.Version = NoticeVersion
	}
	n.ID = strings.TrimSpace(n.ID)
	n.Kind = strings.TrimSpace(n.Kind)
	if n.Severity == "" {
		n.Severity = NoticeSeverityInfo
	}
	if n.AuditRef == "" {
		n.AuditRef = "audit:notice:" + n.ID
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	if n.Revision <= 0 {
		n.Revision = 1
	}
}

func ValidateDecision(d Decision) error {
	if d.Version != DecisionVersion {
		return fmt.Errorf("unsupported decision version %q", d.Version)
	}
	if d.ID == "" {
		return errors.New("decision id is required")
	}
	if !KnownDecisionKind(d.Kind) {
		return fmt.Errorf("unsupported decision kind %q", d.Kind)
	}
	if !KnownDecisionState(d.State) {
		return fmt.Errorf("unsupported decision state %q", d.State)
	}
	if d.DefaultOutcome == "" {
		return errors.New("decision defaultOutcome is required")
	}
	if d.TimeoutAt.IsZero() && !DecisionTerminal(d.State) {
		return errors.New("decision timeoutAt is required")
	}
	if d.Preview.Summary == "" && len(d.Preview.Facts) == 0 && d.Preview.Diff == nil {
		return errors.New("decision preview is required")
	}
	if d.AuditRef == "" {
		return errors.New("decision auditRef is required")
	}
	if d.State == StateClaimed && d.Claim == nil {
		return errors.New("claimed decision requires claim metadata")
	}
	if d.State != StateClaimed && d.Claim != nil {
		return errors.New("claim metadata is only allowed in claimed state")
	}
	if d.Claim != nil {
		if strings.TrimSpace(d.Claim.Surface) == "" {
			return errors.New("decision claim surface is required")
		}
		if d.Claim.ClaimedAt.IsZero() || d.Claim.ExpiresAt.IsZero() ||
			!d.Claim.ExpiresAt.After(d.Claim.ClaimedAt) {
			return errors.New("decision claim lease is invalid")
		}
		if strings.TrimSpace(d.Claim.TokenHash) == "" {
			return errors.New("decision claim token hash is required")
		}
	}
	return nil
}

func ValidateNotice(n Notice) error {
	if n.Version != NoticeVersion {
		return fmt.Errorf("unsupported notice version %q", n.Version)
	}
	if n.ID == "" {
		return errors.New("notice id is required")
	}
	if !KnownNoticeKind(n.Kind) {
		return fmt.Errorf("unsupported notice kind %q", n.Kind)
	}
	if n.Severity != NoticeSeverityInfo && n.Severity != NoticeSeverityWarning && n.Severity != NoticeSeverityError {
		return fmt.Errorf("unsupported notice severity %q", n.Severity)
	}
	if n.Status == "" {
		return errors.New("notice status is required")
	}
	if n.AuditRef == "" {
		return errors.New("notice auditRef is required")
	}
	if n.Preview.Summary == "" && len(n.Preview.Facts) == 0 {
		return errors.New("notice preview is required")
	}
	return nil
}

func KnownDecisionKind(kind string) bool {
	switch kind {
	case KindHostFSWrite, KindHostFSRead, KindHostAppOpenResource, KindAdapterProposal, KindEvidenceShare:
		return true
	default:
		return false
	}
}

func KnownNoticeKind(kind string) bool {
	switch kind {
	case KindPrivilegeStatus, KindBackgroundStatus, KindRuntimeStatus:
		return true
	default:
		return false
	}
}

func KnownDecisionState(state string) bool {
	switch state {
	case StatePending, StateClaimed, StateApproved, StateApplied, StateDenied, StateDiscarded, StateTimedOut, StateFailed, StateStale:
		return true
	default:
		return false
	}
}

func DecisionTerminal(state string) bool {
	switch state {
	case StateApproved, StateApplied, StateDenied, StateDiscarded, StateTimedOut, StateFailed, StateStale:
		return true
	default:
		return false
	}
}

func ActionAllowed(d Decision, action string) bool {
	for _, allowed := range d.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}

func defaultOutcomeForKind(kind string) string {
	switch kind {
	case KindHostFSWrite:
		return DefaultOutcomeDiscard
	case KindEvidenceShare:
		return DefaultOutcomeNoRelease
	default:
		return DefaultOutcomeDeny
	}
}
