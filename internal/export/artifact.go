package export

import "time"

const (
	ArtifactVersion = "hideout.export/v1"
	Action          = "evidence.export"
)

type SourceKind string

const (
	SourceAudit           SourceKind = "audit"
	SourceBundle          SourceKind = "bundle"
	SourceBoundarySummary SourceKind = "boundary-summary"
)

type DecisionMode string

const (
	DecisionRedact                  DecisionMode = "redact"
	DecisionAcknowledgeFullFidelity DecisionMode = "acknowledge-full-fidelity"
)

type DecisionChannel string

const (
	DecisionChannelFlag        DecisionChannel = "flag"
	DecisionChannelInteractive DecisionChannel = "interactive"
)

type Artifact struct {
	Version     string     `json:"version"`
	Provenance  Provenance `json:"provenance"`
	RecordCount int        `json:"recordCount"`
	Body        any        `json:"body"`
	Notice      string     `json:"notice,omitempty"`
}

type Provenance struct {
	Source          SourceKind       `json:"source"`
	Commit          string           `json:"commit,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	RedactionStages []RedactionStage `json:"redactionStages"`
	Decision        ExportDecision   `json:"decision"`
}

type RedactionStage struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

type ExportDecision struct {
	Mode    DecisionMode    `json:"mode"`
	Channel DecisionChannel `json:"channel"`
}

type Request struct {
	Source            SourceKind
	AuditEvents       []AuditEvent
	BundlePath        string
	BoundarySummary   any
	BoundaryAuditPath string

	Out                     string
	StoreRoot               string
	ProfileName             string
	PolicyProfile           string
	RedactSelectors         []string
	AcknowledgeFullFidelity bool
	InteractiveConfirmed    bool
	Commit                  string
	Now                     func() time.Time
}

type AuditEvent struct {
	Time     time.Time      `json:"time"`
	Session  string         `json:"session"`
	Profile  string         `json:"profile"`
	Backend  string         `json:"backend"`
	Action   string         `json:"action"`
	Decision string         `json:"decision"`
	Details  map[string]any `json:"details,omitempty"`
}

type Plan struct {
	Artifact Artifact `json:"artifact"`
	Review   Review   `json:"review"`
}

type Result struct {
	ArtifactPath     string `json:"artifactPath"`
	MetaAuditPath    string `json:"metaAuditPath,omitempty"`
	MetaAuditSession string `json:"metaAuditSession,omitempty"`
	RecordCount      int    `json:"recordCount"`
}
