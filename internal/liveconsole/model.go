package liveconsole

import (
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/manager"
)

const (
	EventVersion = "hideout.daemon-event/v1"
	SeedVersion  = "hideout.live-console-seed/v1"

	KindEnvironment = "environment"
	KindSession     = "session"
	KindBackground  = "background"
	KindAudit       = "audit"
	KindExport      = "export"
	KindCleanup     = "cleanup"
	KindHostFSWrite = "hostfs-write"
	KindDecision    = "decision"
	KindNotice      = "notice"
	KindTerminal    = "terminal"

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
	Version string       `json:"version"`
	Kind    string       `json:"kind"`
	Phase   string       `json:"phase,omitempty"`
	Seq     int          `json:"seq"`
	Entity  EntityRef    `json:"entity,omitempty"`
	Payload EventPayload `json:"payload,omitempty"`
}

type EventPayload struct {
	ID                string         `json:"id,omitempty"`
	Name              string         `json:"name,omitempty"`
	AutoNamed         bool           `json:"autoNamed,omitempty"`
	Status            string         `json:"status,omitempty"`
	Profile           string         `json:"profile,omitempty"`
	Backend           string         `json:"backend,omitempty"`
	Workspace         string         `json:"workspace,omitempty"`
	GuestWorkspace    string         `json:"guestWorkspace,omitempty"`
	ImageRef          string         `json:"imageRef,omitempty"`
	InstanceName      string         `json:"instanceName,omitempty"`
	LastSessionID     string         `json:"lastSessionId,omitempty"`
	LastCommand       string         `json:"lastCommand,omitempty"`
	CreatedAt         time.Time      `json:"createdAt,omitempty"`
	LastStartedAt     time.Time      `json:"lastStartedAt,omitempty"`
	LastEndedAt       time.Time      `json:"lastEndedAt,omitempty"`
	NetworkMode       string         `json:"networkMode,omitempty"`
	HasAudit          bool           `json:"hasAudit,omitempty"`
	HasEphemeralState bool           `json:"hasEphemeralState,omitempty"`
	Op                string         `json:"op,omitempty"`
	Time              time.Time      `json:"time,omitempty"`
	Session           string         `json:"session,omitempty"`
	Action            string         `json:"action,omitempty"`
	Decision          string         `json:"decision,omitempty"`
	Details           map[string]any `json:"details,omitempty"`
	Source            string         `json:"source,omitempty"`
	ArtifactPath      string         `json:"artifactPath,omitempty"`
	Sessions          int            `json:"sessions,omitempty"`
	Removed           []string       `json:"removed,omitempty"`
	SecretState       string         `json:"secretState,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	OperationID       string         `json:"operationId,omitempty"`
	DecisionID        string         `json:"decisionId,omitempty"`
	Operation         string         `json:"operation,omitempty"`
	Path              string         `json:"path,omitempty"`
	DestinationPath   string         `json:"destinationPath,omitempty"`
	PrivilegeStatus   string         `json:"privilegeStatus,omitempty"`
	NoticeID          string         `json:"noticeId,omitempty"`
	RecordKind        string         `json:"recordKind,omitempty"`
	Severity          string         `json:"severity,omitempty"`
	Acknowledged      bool           `json:"acknowledged,omitempty"`
	DefaultOutcome    string         `json:"defaultOutcome,omitempty"`
	Preview           any            `json:"preview,omitempty"`
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
	Status          string `json:"status"`
	Operation       string `json:"operation,omitempty"`
	Path            string `json:"path,omitempty"`
	DestinationPath string `json:"destinationPath,omitempty"`
	PrivilegeStatus string `json:"privilegeStatus,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type DecisionRow struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	DefaultOutcome string `json:"defaultOutcome,omitempty"`
	Profile        string `json:"profile,omitempty"`
	Session        string `json:"session,omitempty"`
	Backend        string `json:"backend,omitempty"`
	Reason         string `json:"reason,omitempty"`
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

type StreamHealth struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type Seed struct {
	Version         string           `json:"version"`
	GeneratedAt     time.Time        `json:"generatedAt,omitempty"`
	Overview        manager.Overview `json:"overview"`
	AuditTail       []audit.Event    `json:"auditTail"`
	DeniedAuditTail []audit.Event    `json:"deniedAuditTail"`
	Background      []BackgroundRow  `json:"background,omitempty"`
	StreamHealth    StreamHealth     `json:"streamHealth"`
	ProfileScope    string           `json:"profileScope,omitempty"`
}

type SeedInput struct {
	GeneratedAt     time.Time
	Overview        manager.Overview
	AuditTail       []audit.Event
	DeniedAuditTail []audit.Event
	Background      []BackgroundRow
	ProfileScope    string
	StreamHealth    string
}

type State struct {
	Version         string                     `json:"version"`
	Overview        manager.Overview           `json:"overview"`
	AuditTail       []audit.Event              `json:"auditTail"`
	DeniedAuditTail []audit.Event              `json:"deniedAuditTail"`
	Background      []BackgroundRow            `json:"background,omitempty"`
	ExportOutcomes  []OutcomeRow               `json:"exportOutcomes,omitempty"`
	CleanupOutcomes []OutcomeRow               `json:"cleanupOutcomes,omitempty"`
	HostFSWrites    []HostFSWriteRow           `json:"hostfsWrites,omitempty"`
	Decisions       []DecisionRow              `json:"decisions,omitempty"`
	Notices         []NoticeRow                `json:"notices,omitempty"`
	StreamHealth    StreamHealth               `json:"streamHealth"`
	LastSeq         int                        `json:"lastSeq"`
	ProfileScope    string                     `json:"profileScope,omitempty"`
	Diagnostics     []string                   `json:"diagnostics,omitempty"`
	Seen            map[string]map[string]bool `json:"-"`
}

type ApplyResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
