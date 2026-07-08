package liveconsole

import (
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/manager"
)

const tailLimit = 20

func Apply(state *State, ev Event) ApplyResult {
	if state == nil {
		return ApplyResult{Status: ResultError, Reason: "nil state"}
	}
	if err := ValidateEvent(ev); err != nil {
		markHealth(state, HealthSchemaMismatch, err.Error())
		return ApplyResult{Status: ResultStale, Reason: err.Error()}
	}
	if ev.Seq <= state.LastSeq {
		return ApplyResult{Status: ResultIgnored, Reason: "old event"}
	}
	if state.LastSeq != 0 && ev.Seq != state.LastSeq+1 {
		markHealth(state, HealthStale, "event sequence gap")
		return ApplyResult{Status: ResultStale, Reason: "event sequence gap"}
	}
	state.LastSeq = ev.Seq
	state.StreamHealth = StreamHealth{State: HealthLive}

	switch ev.Kind {
	case KindEnvironment:
		upsertEnvironment(state, ev.Payload)
	case KindSession:
		upsertSession(state, ev.Payload)
	case KindBackground:
		upsertBackground(state, ev.Payload)
	case KindAudit:
		appendAudit(state, ev.Payload)
	case KindExport:
		state.ExportOutcomes = appendCappedOutcome(state.ExportOutcomes, OutcomeRow{
			Status: ev.Payload.Status, Source: ev.Payload.Source, ArtifactPath: ev.Payload.ArtifactPath, Decision: ev.Payload.Decision,
		})
	case KindCleanup:
		state.CleanupOutcomes = appendCappedOutcome(state.CleanupOutcomes, OutcomeRow{
			Status: ev.Payload.Status, Sessions: ev.Payload.Sessions, Removed: ev.Payload.Removed, SecretState: ev.Payload.SecretState,
		})
	case KindHostFSWrite:
		upsertHostFSWrite(state, ev.Payload)
	case KindDecision:
		upsertDecision(state, ev.Payload)
	case KindNotice:
		upsertNotice(state, ev.Payload)
	case KindTerminal:
		switch ev.Payload.Reason {
		case "credential invalidated":
			markHealth(state, HealthCredentialExpired, ev.Payload.Reason)
		default:
			markHealth(state, HealthDisconnected, ev.Payload.Reason)
		}
		return ApplyResult{Status: ResultStale, Reason: ev.Payload.Reason}
	default:
		return ApplyResult{Status: ResultIgnored, Reason: "unknown event kind"}
	}
	return ApplyResult{Status: ResultApplied}
}

func markHealth(state *State, health, reason string) {
	state.StreamHealth = StreamHealth{State: health, Reason: reason}
	state.Diagnostics = append(state.Diagnostics, reason)
}

func upsertEnvironment(state *State, p EventPayload) {
	row := manager.EnvironmentSummary{
		ID:             p.ID,
		Name:           p.Name,
		AutoNamed:      p.AutoNamed,
		ImageRef:       p.ImageRef,
		Profile:        p.Profile,
		Backend:        p.Backend,
		Status:         p.Status,
		Workspace:      p.Workspace,
		GuestWorkspace: p.GuestWorkspace,
		InstanceName:   p.InstanceName,
		LastSessionID:  p.LastSessionID,
		LastCommand:    p.LastCommand,
		CreatedAt:      p.CreatedAt,
		LastStartedAt:  p.LastStartedAt,
		LastEndedAt:    p.LastEndedAt,
	}
	for i := range state.Overview.Environments {
		if state.Overview.Environments[i].ID == p.ID || (p.Name != "" && state.Overview.Environments[i].Name == p.Name) {
			state.Overview.Environments[i] = mergeEnvironment(state.Overview.Environments[i], row)
			return
		}
	}
	state.Overview.Environments = append([]manager.EnvironmentSummary{row}, state.Overview.Environments...)
}

func mergeEnvironment(old, next manager.EnvironmentSummary) manager.EnvironmentSummary {
	if next.Name == "" {
		next.Name = old.Name
	}
	if next.Profile == "" {
		next.Profile = old.Profile
	}
	if next.Backend == "" {
		next.Backend = old.Backend
	}
	if next.ImageRef == "" {
		next.ImageRef = old.ImageRef
	}
	if next.Workspace == "" {
		next.Workspace = old.Workspace
	}
	if next.GuestWorkspace == "" {
		next.GuestWorkspace = old.GuestWorkspace
	}
	if next.InstanceName == "" {
		next.InstanceName = old.InstanceName
	}
	if next.LastSessionID == "" {
		next.LastSessionID = old.LastSessionID
	}
	if next.LastCommand == "" {
		next.LastCommand = old.LastCommand
	}
	if next.Status == "" {
		next.Status = old.Status
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = old.CreatedAt
	}
	if next.LastStartedAt.IsZero() {
		next.LastStartedAt = old.LastStartedAt
	}
	if next.LastEndedAt.IsZero() {
		next.LastEndedAt = old.LastEndedAt
	}
	return next
}

func upsertSession(state *State, p EventPayload) {
	row := manager.SessionSummary{
		ID:                p.ID,
		Profile:           p.Profile,
		HasAudit:          p.HasAudit,
		HasEphemeralState: p.HasEphemeralState,
		NetworkMode:       p.NetworkMode,
	}
	for i := range state.Overview.Sessions {
		if state.Overview.Sessions[i].ID == p.ID {
			state.Overview.Sessions[i] = mergeSession(state.Overview.Sessions[i], row)
			return
		}
	}
	state.Overview.Sessions = append(state.Overview.Sessions, row)
}

func mergeSession(old, next manager.SessionSummary) manager.SessionSummary {
	if next.Profile == "" {
		next.Profile = old.Profile
	}
	if next.Path == "" {
		next.Path = old.Path
	}
	if next.AuditPath == "" {
		next.AuditPath = old.AuditPath
	}
	if next.NetworkMode == "" {
		next.NetworkMode = old.NetworkMode
	}
	next.HasAudit = next.HasAudit || old.HasAudit
	next.HasBrokerEndpoint = next.HasBrokerEndpoint || old.HasBrokerEndpoint
	next.HasNetworkPlan = next.HasNetworkPlan || old.HasNetworkPlan
	next.HasProxySecretFile = next.HasProxySecretFile || old.HasProxySecretFile
	next.HasEphemeralState = next.HasEphemeralState || old.HasEphemeralState
	return next
}

func upsertBackground(state *State, p EventPayload) {
	row := BackgroundRow{ID: p.ID, Op: p.Op, Status: p.Status}
	for i := range state.Background {
		if state.Background[i].ID == p.ID {
			state.Background[i] = row
			return
		}
	}
	state.Background = append(state.Background, row)
}

func appendAudit(state *State, p EventPayload) {
	event := audit.Event{
		Time:     p.Time,
		Session:  p.Session,
		Profile:  p.Profile,
		Backend:  p.Backend,
		Action:   p.Action,
		Decision: p.Decision,
		Details:  audit.RedactDetails(p.Details),
	}
	state.AuditTail = appendCappedAudit(event, state.AuditTail)
	if p.Decision == "deny" {
		state.DeniedAuditTail = appendCappedAudit(event, state.DeniedAuditTail)
	}
}

func appendCappedAudit(next audit.Event, existing []audit.Event) []audit.Event {
	out := append([]audit.Event{next}, existing...)
	if len(out) > tailLimit {
		return out[:tailLimit]
	}
	return out
}

func appendCappedOutcome(values []OutcomeRow, next OutcomeRow) []OutcomeRow {
	out := append([]OutcomeRow{next}, values...)
	if len(out) > tailLimit {
		return out[:tailLimit]
	}
	return out
}

func upsertHostFSWrite(state *State, p EventPayload) {
	row := HostFSWriteRow{
		DecisionID:      p.DecisionID,
		OperationID:     p.OperationID,
		Status:          p.Status,
		Operation:       p.Operation,
		Path:            p.Path,
		DestinationPath: p.DestinationPath,
		PrivilegeStatus: p.PrivilegeStatus,
		Reason:          p.Reason,
	}
	for i := range state.HostFSWrites {
		if state.HostFSWrites[i].DecisionID == p.DecisionID {
			state.HostFSWrites[i] = row
			return
		}
	}
	state.HostFSWrites = append([]HostFSWriteRow{row}, state.HostFSWrites...)
	if len(state.HostFSWrites) > tailLimit {
		state.HostFSWrites = state.HostFSWrites[:tailLimit]
	}
}

func upsertDecision(state *State, p EventPayload) {
	row := DecisionRow{
		ID:             p.DecisionID,
		Kind:           p.RecordKind,
		Status:         p.Status,
		DefaultOutcome: p.DefaultOutcome,
		Profile:        p.Profile,
		Session:        p.Session,
		Backend:        p.Backend,
		Reason:         p.Reason,
	}
	for i := range state.Decisions {
		if state.Decisions[i].ID == p.DecisionID {
			state.Decisions[i] = row
			return
		}
	}
	state.Decisions = append([]DecisionRow{row}, state.Decisions...)
	if len(state.Decisions) > tailLimit {
		state.Decisions = state.Decisions[:tailLimit]
	}
}

func upsertNotice(state *State, p EventPayload) {
	row := NoticeRow{
		ID:           p.NoticeID,
		Kind:         p.RecordKind,
		Status:       p.Status,
		Severity:     p.Severity,
		Acknowledged: p.Acknowledged,
		Profile:      p.Profile,
		Session:      p.Session,
		Backend:      p.Backend,
	}
	for i := range state.Notices {
		if state.Notices[i].ID == p.NoticeID {
			state.Notices[i] = row
			return
		}
	}
	state.Notices = append([]NoticeRow{row}, state.Notices...)
	if len(state.Notices) > tailLimit {
		state.Notices = state.Notices[:tailLimit]
	}
}
