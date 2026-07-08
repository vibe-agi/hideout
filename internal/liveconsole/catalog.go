package liveconsole

import "time"

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
		"stream":       {KindTerminal},
	}
}

func RepresentativeEventKinds() map[string]bool {
	out := map[string]bool{}
	for _, ev := range RepresentativeEvents() {
		out[ev.Kind] = true
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
			Kind:    KindTerminal,
			Seq:     11,
			Entity:  EntityRef{Kind: "stream"},
			Payload: EventPayload{Reason: "stream closed"},
		},
	}
}
