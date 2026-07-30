package liveconsole

import (
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	EventVersion            = "hideout.daemon-event/v1"
	EventVersionV1          = EventVersion
	EventVersionV2          = "hideout.daemon-event/v2"
	SeedVersion             = "hideout.live-console-seed/v1"
	SeedVersionV1           = SeedVersion
	OperatorSnapshotVersion = "hideout.operator-snapshot.v1"
	SeedVersionV2           = OperatorSnapshotVersion

	KindProfile       = "profile"
	KindTransition    = "transition"
	KindOperation     = "operation"
	KindEnvironment   = "environment"
	KindSession       = "session"
	KindWorkspaceView = "workspace-view"
	KindActivity      = "activity"
	KindCoverage      = "coverage"
	KindRisk          = "risk"
	KindCapability    = "capability"
	KindBackground    = "background"
	KindAudit         = "audit"
	KindExport        = "export"
	KindCleanup       = "cleanup"
	KindHostFSWrite   = "hostfs-write"
	KindDecision      = "decision"
	KindNotice        = "notice"
	KindLifecycle     = "lifecycle"
	KindTerminal      = "terminal"

	HealthSeeding           = "seeding"
	HealthLive              = "live"
	HealthIdleLive          = "idle-live"
	HealthStale             = "stale"
	HealthDisconnected      = "disconnected"
	HealthCredentialExpired = "credential-expired"
	HealthSchemaMismatch    = "schema-mismatch"
	HealthDaemonless        = "daemon-less"

	ResultApplied = "applied"
	ResultIgnored = "ignored"
	ResultStale   = "stale"
	ResultError   = "error"
)

type EntityRef struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	Profile string `json:"profile,omitempty"`
	Session string `json:"session,omitempty"`
}

type Event struct {
	Version              string       `json:"version"`
	InstanceID           string       `json:"instanceId,omitempty"`
	CredentialGeneration uint64       `json:"credentialGeneration,omitempty"`
	Kind                 string       `json:"kind"`
	Optional             bool         `json:"optional,omitempty"`
	Phase                string       `json:"phase,omitempty"`
	Seq                  int          `json:"seq"`
	Entity               EntityRef    `json:"entity,omitzero"`
	Payload              EventPayload `json:"payload,omitempty"`
}

type EventPayload struct {
	ID                 string                               `json:"id,omitempty"`
	Name               string                               `json:"name,omitempty"`
	AutoNamed          bool                                 `json:"autoNamed,omitempty"`
	Status             string                               `json:"status,omitempty"`
	Profile            string                               `json:"profile,omitempty"`
	Backend            string                               `json:"backend,omitempty"`
	Workspace          string                               `json:"workspace,omitempty"`
	GuestWorkspace     string                               `json:"guestWorkspace,omitempty"`
	ImageRef           string                               `json:"imageRef,omitempty"`
	InstanceName       string                               `json:"instanceName,omitempty"`
	LastSessionID      string                               `json:"lastSessionId,omitempty"`
	LastCommand        string                               `json:"lastCommand,omitempty"`
	CreatedAt          time.Time                            `json:"createdAt,omitzero"`
	LastStartedAt      time.Time                            `json:"lastStartedAt,omitzero"`
	LastEndedAt        time.Time                            `json:"lastEndedAt,omitzero"`
	NetworkMode        string                               `json:"networkMode,omitempty"`
	HasAudit           bool                                 `json:"hasAudit,omitempty"`
	HasEphemeralState  bool                                 `json:"hasEphemeralState,omitempty"`
	Op                 string                               `json:"op,omitempty"`
	Time               time.Time                            `json:"time,omitzero"`
	Session            string                               `json:"session,omitempty"`
	Action             string                               `json:"action,omitempty"`
	Decision           string                               `json:"decision,omitempty"`
	Details            map[string]any                       `json:"details,omitempty"`
	Source             string                               `json:"source,omitempty"`
	ArtifactPath       string                               `json:"artifactPath,omitempty"`
	Sessions           int                                  `json:"sessions,omitempty"`
	Removed            []string                             `json:"removed,omitempty"`
	SecretState        string                               `json:"secretState,omitempty"`
	Reason             string                               `json:"reason,omitempty"`
	OperationID        string                               `json:"operationId,omitempty"`
	DecisionID         string                               `json:"decisionId,omitempty"`
	Operation          string                               `json:"operation,omitempty"`
	Path               string                               `json:"path,omitempty"`
	DestinationPath    string                               `json:"destinationPath,omitempty"`
	PrivilegeStatus    string                               `json:"privilegeStatus,omitempty"`
	NoticeID           string                               `json:"noticeId,omitempty"`
	RecordKind         string                               `json:"recordKind,omitempty"`
	Severity           string                               `json:"severity,omitempty"`
	Acknowledged       bool                                 `json:"acknowledged,omitempty"`
	DefaultOutcome     string                               `json:"defaultOutcome,omitempty"`
	ClaimSurface       string                               `json:"claimSurface,omitempty"`
	ClaimOperator      string                               `json:"claimOperator,omitempty"`
	ClaimedAt          time.Time                            `json:"claimedAt,omitzero"`
	ClaimExpiresAt     time.Time                            `json:"claimExpiresAt,omitzero"`
	Revision           int                                  `json:"revision,omitempty"`
	Preview            any                                  `json:"preview,omitempty"`
	Lifecycle          *lifecycle.Status                    `json:"lifecycle,omitempty"`
	AttachmentID       string                               `json:"attachmentId,omitempty"`
	EnvironmentID      string                               `json:"environmentId,omitempty"`
	WorkspaceID        string                               `json:"workspaceId,omitempty"`
	WorkspaceLabel     string                               `json:"workspaceLabel,omitempty"`
	WorkspaceTransport string                               `json:"workspaceTransport,omitempty"`
	WorkspaceViewState workspaceattach.AttachmentState      `json:"workspaceViewState,omitempty"`
	WorkspaceRelations []workspaceattach.RootRelationNotice `json:"workspaceRelations,omitempty"`
	BlockerCode        string                               `json:"blockerCode,omitempty"`
	CleanupStatus      string                               `json:"cleanupStatus,omitempty"`

	ProfileProjection    *manager.ProfileProjection       `json:"profileProjection,omitempty"`
	TransitionProjection *TransitionProjection            `json:"transitionProjection,omitempty"`
	OperationProjection  *manager.Operation               `json:"operationProjection,omitempty"`
	ActivityProjection   *ActivityProjectionDelta         `json:"summary,omitempty"`
	CoverageProjection   []workloadtypes.CoverageInterval `json:"coverage,omitempty"`
	RiskProjection       *RiskFinding                     `json:"riskProjection,omitempty"`
	CapabilityProjection *CapabilityProjection            `json:"capabilityProjection,omitempty"`
}

type TransitionProjection struct {
	Profile    string                    `json:"profile"`
	Transition manager.ProfileTransition `json:"transition"`
}

type ActivityCount struct {
	Kind  string `json:"kind"`
	Count uint64 `json:"count"`
}

// ActivityProjection is the bounded snapshot view. Recent records have already
// passed workload redaction; event deltas use ActivityProjectionDelta and
// never carry argv, paths, or packet metadata.
type ActivityProjection struct {
	Cursor       string                         `json:"cursor,omitempty"`
	Counts       []ActivityCount                `json:"counts"`
	Recent       []workloadtypes.ActivityRecord `json:"recent,omitempty"`
	RetainedFrom time.Time                      `json:"retainedFrom,omitempty"`
	RetainedTo   time.Time                      `json:"retainedTo,omitempty"`
	Truncated    bool                           `json:"truncated"`
}

type ActivityProjectionDelta struct {
	Profile  string          `json:"profile,omitempty"`
	Session  string          `json:"session,omitempty"`
	Cursor   string          `json:"cursor,omitempty"`
	Counts   []ActivityCount `json:"counts,omitempty"`
	Appended uint64          `json:"appended,omitempty"`
	LastAt   time.Time       `json:"lastAt,omitempty"`
}

type RiskFinding struct {
	ID           string    `json:"id"`
	RuleID       string    `json:"ruleId"`
	RuleVersion  string    `json:"ruleVersion"`
	Severity     string    `json:"severity"`
	Title        string    `json:"title"`
	Explanation  string    `json:"explanation"`
	EvidenceRefs []string  `json:"evidenceRefs"`
	Confidence   string    `json:"confidence"`
	PolicyStatus string    `json:"policyStatus"`
	FirstAt      time.Time `json:"firstAt"`
	LastAt       time.Time `json:"lastAt"`
	Count        uint64    `json:"count"`
	NextAction   string    `json:"nextAction,omitempty"`
}

type CapabilityProjection struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	Provider   string   `json:"provider,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Mutable    bool     `json:"mutable"`
	ActionRefs []string `json:"actionRefs,omitempty"`
}

type NextActionRef struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command"`
	Risk    string `json:"risk,omitempty"`
}

type BackgroundRow struct {
	ID     string `json:"id"`
	Op     string `json:"op"`
	Status string `json:"status"`
}

type OutcomeRow struct {
	Status       string   `json:"status,omitempty"`
	Source       string   `json:"source,omitempty"`
	ArtifactPath string   `json:"artifactPath,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	Sessions     int      `json:"sessions,omitempty"`
	Removed      []string `json:"removed,omitempty"`
	SecretState  string   `json:"secretState,omitempty"`
}

type HostFSWriteRow struct {
	DecisionID      string `json:"decisionId"`
	OperationID     string `json:"operationId"`
	Profile         string `json:"profile,omitempty"`
	Status          string `json:"status"`
	Operation       string `json:"operation,omitempty"`
	Path            string `json:"path,omitempty"`
	DestinationPath string `json:"destinationPath,omitempty"`
	PrivilegeStatus string `json:"privilegeStatus,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type DecisionRow struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	DefaultOutcome string    `json:"defaultOutcome,omitempty"`
	Profile        string    `json:"profile,omitempty"`
	Session        string    `json:"session,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	ClaimSurface   string    `json:"claimSurface,omitempty"`
	ClaimOperator  string    `json:"claimOperator,omitempty"`
	ClaimedAt      time.Time `json:"claimedAt,omitzero"`
	ClaimExpiresAt time.Time `json:"claimExpiresAt,omitzero"`
	Revision       int       `json:"revision,omitempty"`
}

type NoticeRow struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Severity     string `json:"severity,omitempty"`
	Acknowledged bool   `json:"acknowledged"`
	Profile      string `json:"profile,omitempty"`
	Session      string `json:"session,omitempty"`
	Backend      string `json:"backend,omitempty"`
}

type StatusRow struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Next   string `json:"next,omitempty"`
}

type ActionRequiredSummary struct {
	HostFSWrites int `json:"hostfsWrites"`
	Decisions    int `json:"decisions"`
	Notices      int `json:"notices"`
	Total        int `json:"total"`
}

type StreamHealth struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type Seed struct {
	Version                string                                            `json:"version"`
	GeneratedAt            time.Time                                         `json:"generatedAt,omitempty"`
	DaemonInstanceID       string                                            `json:"instanceId,omitempty"`
	CredentialGeneration   uint64                                            `json:"credentialGeneration,omitempty"`
	EventSequence          int                                               `json:"sequence,omitempty"`
	Overview               manager.Overview                                  `json:"overview"`
	Profiles               []manager.ProfileProjection                       `json:"profiles,omitempty"`
	Transitions            []TransitionProjection                            `json:"transitions,omitempty"`
	Operations             []manager.Operation                               `json:"operations,omitempty"`
	Activity               ActivityProjection                                `json:"activity,omitzero"`
	Coverage               []workloadtypes.CoverageInterval                  `json:"coverage,omitempty"`
	ActivityRetention      []manager.OperatorActivityRetentionProjection     `json:"activityRetention,omitempty"`
	ActivityStoreRetention *manager.OperatorActivityStoreRetentionProjection `json:"activityStoreRetention,omitempty"`
	Risks                  []RiskFinding                                     `json:"risks,omitempty"`
	Capabilities           []CapabilityProjection                            `json:"capabilities,omitempty"`
	NextActions            []NextActionRef                                   `json:"nextActions,omitempty"`
	AuditTail              []audit.Event                                     `json:"auditTail"`
	DeniedAuditTail        []audit.Event                                     `json:"deniedAuditTail"`
	Background             []BackgroundRow                                   `json:"background,omitempty"`
	HostFSWrites           []HostFSWriteRow                                  `json:"hostfsWrites,omitempty"`
	Decisions              []DecisionRow                                     `json:"decisions,omitempty"`
	Notices                []NoticeRow                                       `json:"notices,omitempty"`
	StatusRows             []StatusRow                                       `json:"statusRows,omitempty"`
	Lifecycle              []lifecycle.Status                                `json:"lifecycle,omitempty"`
	StreamHealth           StreamHealth                                      `json:"streamHealth"`
	ProfileScope           string                                            `json:"profileScope,omitempty"`
}

type SeedInput struct {
	GeneratedAt            time.Time
	DaemonInstanceID       string
	CredentialGeneration   uint64
	EventSequence          int
	Overview               manager.Overview
	Profiles               []manager.ProfileProjection
	Transitions            []TransitionProjection
	Operations             []manager.Operation
	Activity               ActivityProjection
	Coverage               []workloadtypes.CoverageInterval
	ActivityRetention      []manager.OperatorActivityRetentionProjection
	ActivityStoreRetention *manager.OperatorActivityStoreRetentionProjection
	Risks                  []RiskFinding
	Capabilities           []CapabilityProjection
	NextActions            []NextActionRef
	AuditTail              []audit.Event
	DeniedAuditTail        []audit.Event
	Background             []BackgroundRow
	HostFSWrites           []HostFSWriteRow
	Decisions              []DecisionRow
	Notices                []NoticeRow
	StatusRows             []StatusRow
	Lifecycle              []lifecycle.Status
	ProfileScope           string
	StreamHealth           string
}

type State struct {
	Version                string                                            `json:"version"`
	DaemonInstanceID       string                                            `json:"instanceId,omitempty"`
	CredentialGeneration   uint64                                            `json:"credentialGeneration,omitempty"`
	Overview               manager.Overview                                  `json:"overview"`
	Profiles               []manager.ProfileProjection                       `json:"profiles,omitempty"`
	Transitions            []TransitionProjection                            `json:"transitions,omitempty"`
	Operations             []manager.Operation                               `json:"operations,omitempty"`
	Activity               ActivityProjection                                `json:"activity"`
	Coverage               []workloadtypes.CoverageInterval                  `json:"coverage,omitempty"`
	ActivityRetention      []manager.OperatorActivityRetentionProjection     `json:"activityRetention,omitempty"`
	ActivityStoreRetention *manager.OperatorActivityStoreRetentionProjection `json:"activityStoreRetention,omitempty"`
	Risks                  []RiskFinding                                     `json:"risks,omitempty"`
	Capabilities           []CapabilityProjection                            `json:"capabilities,omitempty"`
	NextActions            []NextActionRef                                   `json:"nextActions,omitempty"`
	AuditTail              []audit.Event                                     `json:"auditTail"`
	DeniedAuditTail        []audit.Event                                     `json:"deniedAuditTail"`
	Background             []BackgroundRow                                   `json:"background,omitempty"`
	StatusRows             []StatusRow                                       `json:"statusRows,omitempty"`
	Lifecycle              []lifecycle.Status                                `json:"lifecycle,omitempty"`
	ExportOutcomes         []OutcomeRow                                      `json:"exportOutcomes,omitempty"`
	CleanupOutcomes        []OutcomeRow                                      `json:"cleanupOutcomes,omitempty"`
	HostFSWrites           []HostFSWriteRow                                  `json:"hostfsWrites,omitempty"`
	Decisions              []DecisionRow                                     `json:"decisions,omitempty"`
	Notices                []NoticeRow                                       `json:"notices,omitempty"`
	StreamHealth           StreamHealth                                      `json:"streamHealth"`
	LastSeq                int                                               `json:"lastSeq"`
	ProfileScope           string                                            `json:"profileScope,omitempty"`
	ReadOnly               bool                                              `json:"readOnly"`
	RequiresReseed         bool                                              `json:"requiresReseed"`
	Diagnostics            []string                                          `json:"diagnostics,omitempty"`
	Seen                   map[string]map[string]bool                        `json:"-"`
}

type ApplyResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (state State) CanMutate() bool {
	if state.Version != SeedVersionV2 ||
		!daemonInstancePattern.MatchString(state.DaemonInstanceID) ||
		state.CredentialGeneration == 0 ||
		state.LastSeq < 0 ||
		state.ReadOnly ||
		state.RequiresReseed {
		return false
	}
	return state.StreamHealth.State == HealthLive || state.StreamHealth.State == HealthIdleLive
}

func ActionRequired(state State) ActionRequiredSummary {
	out := ActionRequiredSummary{}
	for _, row := range state.HostFSWrites {
		if actionableStatus(row.Status) {
			out.HostFSWrites++
		}
	}
	for _, row := range state.Decisions {
		if actionableStatus(row.Status) {
			out.Decisions++
		}
	}
	for _, row := range state.Notices {
		if !row.Acknowledged {
			out.Notices++
		}
	}
	out.Total = out.HostFSWrites + out.Decisions + out.Notices
	return out
}

func actionableStatus(status string) bool {
	switch status {
	case "", "pending", "claimed", "ready", "requires-decision":
		return true
	default:
		return false
	}
}
