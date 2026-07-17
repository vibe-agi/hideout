package liveconsole

import (
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

const (
	EventSourceProduction = "production"
	EventSourceSeedOnly   = "seed-only"
	EventSourceTestOnly   = "test-only"

	RedactionControlPlaneStripped = "control-plane-stripped"
)

type EventCatalogEntry struct {
	Kind           string
	ProducerKinds  []string
	RemapFrom      []string
	Source         string
	ProductionSite string
	RequiredFields []string
	Redaction      string
	GoReducer      bool
	JSReducer      bool
	Panels         []string
}

// PanelEventCoverage lists the event kinds each live panel depends on. Tests use
// it as a drift guard: adding a panel or event kind requires updating the
// representative catalog before UI code can claim live coverage.
func PanelEventCoverage() map[string][]string {
	return map[string][]string{
		"environments": {KindEnvironment},
		"sessions":     {KindSession},
		"background":   {KindBackground},
		"audit":        {KindAudit},
		"denied-audit": {KindAudit},
		"exports":      {KindExport},
		"cleanup":      {KindCleanup},
		"hostfs-write": {KindHostFSWrite},
		"decisions":    {KindDecision},
		"notices":      {KindNotice},
		"lifecycle":    {KindLifecycle},
		"stream":       {KindTerminal},
	}
}

func EventCatalog() []EventCatalogEntry {
	out := append([]EventCatalogEntry(nil), eventCatalog...)
	return out
}

func RepresentativeEventKinds() map[string]bool {
	out := map[string]bool{}
	for _, ev := range RepresentativeEvents() {
		out[ev.Kind] = true
	}
	return out
}

func RequiredPayloadFields(kind string) []string {
	for _, entry := range eventCatalog {
		if entry.Kind == kind {
			return append([]string(nil), entry.RequiredFields...)
		}
	}
	return nil
}

func ReducerEventKinds() []string {
	return []string{
		KindEnvironment,
		KindSession,
		KindBackground,
		KindAudit,
		KindExport,
		KindCleanup,
		KindHostFSWrite,
		KindDecision,
		KindNotice,
		KindLifecycle,
		KindTerminal,
	}
}

func EventProducerMappings() map[string]string {
	out := map[string]string{}
	for _, entry := range eventCatalog {
		for _, producer := range entry.ProducerKinds {
			out[producer] = entry.Kind
		}
		for _, producer := range entry.RemapFrom {
			out[producer] = entry.Kind
		}
	}
	return out
}

// RepresentativeEvents is the drift guard between the daemon event schema and
// the panels 007 promises to keep live without re-fetching overview/audit data.
func RepresentativeEvents() []Event {
	now := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	return []Event{
		{
			Version: EventVersion,
			Kind:    KindEnvironment,
			Phase:   "complete",
			Seq:     1,
			Entity:  EntityRef{Kind: KindEnvironment, ID: "env_alpha", Profile: "alpha"},
			Payload: EventPayload{
				ID: "env_alpha", Name: "alpha-env", Status: "running", Profile: "alpha", Backend: "native",
				Workspace: "/workspace", GuestWorkspace: "/workspace", ImageRef: "builtin", LastSessionID: "ses_alpha", LastCommand: "pwd",
			},
		},
		{
			Version: EventVersion,
			Kind:    KindSession,
			Phase:   "complete",
			Seq:     2,
			Entity:  EntityRef{Kind: KindSession, ID: "ses_alpha", Profile: "alpha", Session: "ses_alpha"},
			Payload: EventPayload{ID: "ses_alpha", Profile: "alpha", Backend: "native", Status: "completed", NetworkMode: "direct", HasAudit: true, HasEphemeralState: false},
		},
		{
			Version: EventVersion,
			Kind:    KindBackground,
			Seq:     3,
			Entity:  EntityRef{Kind: KindBackground, ID: "bg-1"},
			Payload: EventPayload{ID: "bg-1", Op: "environment-clean", Status: "completed"},
		},
		{
			Version: EventVersion,
			Kind:    KindAudit,
			Seq:     4,
			Entity:  EntityRef{Kind: KindAudit, ID: "audit-1", Profile: "alpha", Session: "ses_alpha"},
			Payload: EventPayload{Time: now, Session: "ses_alpha", Profile: "alpha", Backend: "native", Action: "run", Decision: "allow", Details: map[string]any{"target": "https://example.com"}},
		},
		{
			Version: EventVersion,
			Kind:    KindAudit,
			Seq:     5,
			Entity:  EntityRef{Kind: KindAudit, ID: "audit-2", Profile: "alpha", Session: "ses_alpha"},
			Payload: EventPayload{Time: now, Session: "ses_alpha", Profile: "alpha", Backend: "native", Action: "host.open", Decision: "deny", Details: map[string]any{"reason": "private address"}},
		},
		{
			Version: EventVersion,
			Kind:    KindExport,
			Seq:     6,
			Entity:  EntityRef{Kind: KindExport, ID: "export-1", Profile: "alpha"},
			Payload: EventPayload{Status: "completed", Source: "audit", ArtifactPath: "/tmp/export.json", Decision: "redact"},
		},
		{
			Version: EventVersion,
			Kind:    KindCleanup,
			Seq:     7,
			Entity:  EntityRef{Kind: KindCleanup, ID: "cleanup-1"},
			Payload: EventPayload{Status: "completed", Sessions: 1, Removed: []string{"ses_alpha"}, SecretState: "removed"},
		},
		{
			Version: EventVersion,
			Kind:    KindHostFSWrite,
			Seq:     8,
			Entity:  EntityRef{Kind: KindHostFSWrite, ID: "hfwdec_123"},
			Payload: EventPayload{
				DecisionID: "hfwdec_123", OperationID: "hfwop_123", Status: "pending", Operation: "replace", Path: "/Users/alice/project-notes.txt", PrivilegeStatus: "enforced",
			},
		},
		{
			Version: EventVersion,
			Kind:    KindDecision,
			Seq:     9,
			Entity:  EntityRef{Kind: KindDecision, ID: "dec_share_123", Profile: "alpha", Session: "ses_alpha"},
			Payload: EventPayload{
				ID: "dec_share_123", DecisionID: "dec_share_123", RecordKind: "evidence.share", Status: "pending", DefaultOutcome: "deny",
				Profile: "alpha", Session: "ses_alpha", Backend: "native",
			},
		},
		{
			Version: EventVersion,
			Kind:    KindNotice,
			Seq:     10,
			Entity:  EntityRef{Kind: KindNotice, ID: "notice_priv_123", Profile: "alpha", Session: "ses_alpha"},
			Payload: EventPayload{
				ID: "notice_priv_123", NoticeID: "notice_priv_123", RecordKind: "privilege.status", Status: "degraded", Severity: "warning",
				Profile: "alpha", Session: "ses_alpha", Backend: "lima", Acknowledged: false,
			},
		},
		{
			Version: EventVersion,
			Kind:    KindLifecycle,
			Seq:     11,
			Entity:  EntityRef{Kind: KindLifecycle, ID: "env_alpha"},
			Payload: EventPayload{Lifecycle: &lifecycle.Status{
				Schema: lifecycle.StatusSchema, EnvironmentID: "env_alpha", StartGeneration: 2,
				BackendState: "running", BackendObservedAt: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC),
				Activity: lifecycle.ActivityIdleGrace, Reconciliation: "complete",
			}},
		},
		{
			Version: EventVersion,
			Kind:    KindTerminal,
			Seq:     12,
			Entity:  EntityRef{Kind: "stream"},
			Payload: EventPayload{Reason: "stream closed"},
		},
	}
}

var eventCatalog = []EventCatalogEntry{
	{
		Kind:           KindEnvironment,
		ProducerKinds:  []string{KindEnvironment},
		Source:         EventSourceProduction,
		ProductionSite: "manager.Core.emitOperation(environment)",
		RequiredFields: []string{"id"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"environments"},
	},
	{
		Kind:           KindSession,
		ProducerKinds:  []string{KindSession},
		RemapFrom:      []string{"run", "operation", "*"},
		Source:         EventSourceProduction,
		ProductionSite: "daemon.eventBus.OperationEvent(session/run/operation/default)",
		RequiredFields: []string{"id"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"sessions"},
	},
	{
		Kind:           KindBackground,
		ProducerKinds:  []string{KindBackground},
		Source:         EventSourceProduction,
		ProductionSite: "daemon.background + manager.Core.emitOperation(background)",
		RequiredFields: []string{"id", "op", "status"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"background"},
	},
	{
		Kind:           KindAudit,
		ProducerKinds:  []string{KindAudit},
		RemapFrom:      []string{"host-app"},
		Source:         EventSourceProduction,
		ProductionSite: "daemon audit tail",
		RequiredFields: []string{"action", "decision"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"audit", "denied-audit"},
	},
	{
		Kind:           KindExport,
		ProducerKinds:  []string{KindExport},
		Source:         EventSourceProduction,
		ProductionSite: "export.ApplyExport",
		RequiredFields: []string{"status"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"exports"},
	},
	{
		Kind:           KindCleanup,
		ProducerKinds:  []string{KindCleanup},
		Source:         EventSourceProduction,
		ProductionSite: "manager.CloseRunSession",
		RequiredFields: []string{"status"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"cleanup"},
	},
	{
		Kind:           KindHostFSWrite,
		ProducerKinds:  []string{KindHostFSWrite},
		Source:         EventSourceProduction,
		ProductionSite: "HostFS write decision provider",
		RequiredFields: []string{"decisionId", "operationId", "status"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"hostfs-write"},
	},
	{
		Kind:           KindDecision,
		ProducerKinds:  []string{KindDecision},
		Source:         EventSourceProduction,
		ProductionSite: "decision store provider",
		RequiredFields: []string{"decisionId", "recordKind", "status"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"decisions"},
	},
	{
		Kind:           KindNotice,
		ProducerKinds:  []string{KindNotice},
		Source:         EventSourceProduction,
		ProductionSite: "notice store provider",
		RequiredFields: []string{"noticeId", "recordKind", "status"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"notices"},
	},
	{
		Kind:           KindLifecycle,
		ProducerKinds:  []string{KindLifecycle},
		Source:         EventSourceProduction,
		ProductionSite: "lifecycle.Coordinator.Publish",
		RequiredFields: []string{"lifecycle"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"lifecycle"},
	},
	{
		Kind:           KindTerminal,
		ProducerKinds:  []string{KindTerminal},
		Source:         EventSourceProduction,
		ProductionSite: "daemon event stream termination",
		RequiredFields: []string{"reason"},
		Redaction:      RedactionControlPlaneStripped,
		GoReducer:      true,
		JSReducer:      true,
		Panels:         []string{"stream"},
	},
}
