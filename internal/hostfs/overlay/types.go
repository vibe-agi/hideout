package overlay

import "time"

const (
	PlanVersion   = "hideout.hostfs-write-plan/v1"
	ResultVersion = "hideout.hostfs-write-result/v1"
	StatusVersion = "hideout.hostfs-write-status/v1"

	StateStaged    = "staged"
	StatePending   = "pending"
	StateClaimed   = "claimed"
	StateApplied   = "applied"
	StateDiscarded = "discarded"
	StateExpired   = "expired"
	StateConflict  = "conflict"
	StateFailed    = "failed"
	StateDenied    = "denied"

	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

type Operation struct {
	Version         string    `json:"version"`
	ID              string    `json:"id"`
	SessionID       string    `json:"sessionId"`
	Profile         string    `json:"profile"`
	Backend         string    `json:"backend"`
	Operation       string    `json:"operation"`
	RequestedPath   string    `json:"requestedPath"`
	CanonicalPath   string    `json:"canonicalPath,omitempty"`
	DestinationPath string    `json:"destinationPath,omitempty"`
	GrantID         string    `json:"grantId,omitempty"`
	GrantSource     string    `json:"grantSource,omitempty"`
	BaseSnapshot    Snapshot  `json:"baseSnapshot"`
	NewSnapshot     Snapshot  `json:"newSnapshot,omitempty"`
	ContentObject   string    `json:"contentObject,omitempty"`
	Preview         Preview   `json:"preview,omitempty"`
	RequestedMode   string    `json:"requestedMode,omitempty"`
	RequestedOwner  string    `json:"requestedOwner,omitempty"`
	RequestedGroup  string    `json:"requestedGroup,omitempty"`
	Privilege       Privilege `json:"privilegeStatus"`
	DecisionID      string    `json:"decisionId"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type Snapshot struct {
	Exists      bool      `json:"exists"`
	Kind        string    `json:"kind"`
	Device      uint64    `json:"device,omitempty"`
	Inode       uint64    `json:"inode,omitempty"`
	Size        int64     `json:"size,omitempty"`
	MTime       time.Time `json:"mtime,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	UID         int       `json:"uid,omitempty"`
	GID         int       `json:"gid,omitempty"`
	ContentHash string    `json:"contentHash,omitempty"`
	LinkTarget  string    `json:"linkTarget,omitempty"`
}

type Preview struct {
	Kind      string `json:"kind,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	Lines     int    `json:"lines,omitempty"`
}

type Privilege struct {
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	Source    string    `json:"source,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

type PolicyRef struct {
	GrantID     string `json:"grantId,omitempty"`
	Source      string `json:"source,omitempty"`
	DenyMatched bool   `json:"denyMatched,omitempty"`
}

type Decision struct {
	Version         string    `json:"version"`
	DecisionID      string    `json:"decisionId"`
	OperationID     string    `json:"operationId"`
	State           string    `json:"state"`
	Operation       string    `json:"operation"`
	Path            string    `json:"path"`
	DestinationPath string    `json:"destinationPath,omitempty"`
	Preview         Preview   `json:"preview,omitempty"`
	Policy          PolicyRef `json:"policy,omitempty"`
	Privilege       Privilege `json:"privilege"`
	TimeoutAt       time.Time `json:"timeoutAt,omitempty"`
	Claim           *Claim    `json:"claim,omitempty"`
	Warnings        []string  `json:"warnings,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
	AuditRefs       []string  `json:"auditRefs,omitempty"`
}

type Claim struct {
	Surface   string    `json:"surface"`
	Operator  string    `json:"operator,omitempty"`
	ClaimedAt time.Time `json:"claimedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"-"`
	TokenHash string    `json:"tokenHash,omitempty"`
}

type ClaimResponse struct {
	DecisionID     string    `json:"decisionId"`
	State          string    `json:"state"`
	ClaimToken     string    `json:"claimToken"`
	ClaimExpiresAt time.Time `json:"claimExpiresAt"`
}

type Result struct {
	Version                  string    `json:"version"`
	DecisionID               string    `json:"decisionId"`
	OperationID              string    `json:"operationId"`
	Decision                 string    `json:"decision"`
	Status                   string    `json:"status"`
	ChangedPaths             []string  `json:"changedPaths,omitempty"`
	ConflictReason           string    `json:"conflictReason,omitempty"`
	PartialMutationPrevented bool      `json:"partialMutationPrevented,omitempty"`
	Privilege                Privilege `json:"privilege"`
	AuditRef                 string    `json:"auditRef,omitempty"`
}

type StatusEntry struct {
	DecisionID      string    `json:"decisionId"`
	OperationID     string    `json:"operationId"`
	State           string    `json:"state"`
	Operation       string    `json:"operation"`
	Path            string    `json:"path"`
	DestinationPath string    `json:"destinationPath,omitempty"`
	TimeoutAt       time.Time `json:"timeoutAt,omitempty"`
	PrivilegeStatus string    `json:"privilegeStatus,omitempty"`
}
