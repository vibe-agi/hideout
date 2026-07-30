package liveconsole

import (
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const tailLimit = 20

func Apply(state *State, ev Event) ApplyResult {
	if state == nil {
		return ApplyResult{Status: ResultError, Reason: "nil state"}
	}
	if ev.Version == EventVersionV2 {
		return applyV2(state, ev)
	}
	return applyV1(state, ev)
}

func applyV1(state *State, ev Event) ApplyResult {
	if state.Version == SeedVersionV2 {
		reason := "v1 event cannot update a v2 snapshot"
		markNeedsReseed(state, HealthSchemaMismatch, reason)
		return ApplyResult{Status: ResultStale, Reason: reason}
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
	if !eventMatchesProfile(state.ProfileScope, ev) {
		return ApplyResult{Status: ResultIgnored, Reason: "event outside profile scope"}
	}

	switch ev.Kind {
	case KindEnvironment:
		upsertEnvironment(state, ev.Payload)
	case KindSession:
		upsertSession(state, ev.Payload)
	case KindWorkspaceView:
		upsertWorkspaceView(state, ev.Payload)
	case KindBackground:
		upsertBackground(state, ev.Payload)
	case KindAudit:
		appendAudit(state, ev.Payload)
	case KindExport:
		state.ExportOutcomes = appendCappedOutcome(state.ExportOutcomes, OutcomeRow{
			Status: redactText(ev.Payload.Status), Source: redactText(ev.Payload.Source), ArtifactPath: redactText(ev.Payload.ArtifactPath), Decision: redactText(ev.Payload.Decision),
		})
	case KindCleanup:
		state.CleanupOutcomes = appendCappedOutcome(state.CleanupOutcomes, OutcomeRow{
			Status: redactText(ev.Payload.Status), Sessions: ev.Payload.Sessions, Removed: redactStringSlice(ev.Payload.Removed), SecretState: redactText(ev.Payload.SecretState),
		})
	case KindHostFSWrite:
		upsertHostFSWrite(state, ev.Payload)
	case KindDecision:
		upsertDecision(state, ev.Payload)
	case KindNotice:
		upsertNotice(state, ev.Payload)
	case KindLifecycle:
		upsertLifecycle(state, ev.Payload)
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

func upsertLifecycle(state *State, payload EventPayload) {
	if payload.Lifecycle == nil {
		return
	}
	row := *payload.Lifecycle
	if state.ProfileScope != "" && !stateContainsEnvironment(state, row.EnvironmentID) {
		return
	}
	for i := range state.Lifecycle {
		if state.Lifecycle[i].EnvironmentID == row.EnvironmentID {
			state.Lifecycle[i] = row
			return
		}
	}
	state.Lifecycle = append(state.Lifecycle, row)
}

func stateContainsEnvironment(state *State, environmentID string) bool {
	for _, environment := range state.Overview.Environments {
		if environment.ID == environmentID {
			return true
		}
	}
	return false
}

func markHealth(state *State, health, reason string) {
	state.StreamHealth = StreamHealth{State: health, Reason: reason}
	if reason != "" {
		state.Diagnostics = appendCappedDiagnostic(state.Diagnostics, reason)
	}
}

func eventMatchesProfile(profileName string, ev Event) bool {
	if profileName == "" {
		return true
	}
	profile := ev.Payload.Profile
	if profile == "" && ev.Payload.ProfileProjection != nil {
		profile = ev.Payload.ProfileProjection.Profile
	}
	if profile == "" && ev.Payload.TransitionProjection != nil {
		profile = ev.Payload.TransitionProjection.Profile
	}
	if profile == "" && ev.Payload.ActivityProjection != nil {
		profile = ev.Payload.ActivityProjection.Profile
	}
	if profile == "" {
		profile = ev.Entity.Profile
	}
	return profile == "" || profile == profileName
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
		LastStartedAt:  optionalEventTime(p.LastStartedAt),
		LastEndedAt:    optionalEventTime(p.LastEndedAt),
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
	if next.Schema == "" {
		next.Schema = old.Schema
	}
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
	if next.Mode == "" {
		next.Mode = old.Mode
	}
	if next.SharedSlot == "" {
		next.SharedSlot = old.SharedSlot
	}
	if next.MachineIdentityID == "" {
		next.MachineIdentityID = old.MachineIdentityID
	}
	if next.RecordVersion == "" {
		next.RecordVersion = old.RecordVersion
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
	if next.LastStartedAt == nil {
		next.LastStartedAt = old.LastStartedAt
	}
	if next.LastEndedAt == nil {
		next.LastEndedAt = old.LastEndedAt
	}
	if next.ActiveSessions == 0 {
		next.ActiveSessions = old.ActiveSessions
	}
	if next.ActiveWorkspaceViews == 0 {
		next.ActiveWorkspaceViews = old.ActiveWorkspaceViews
	}
	if next.WorkspaceProviderState == "" {
		next.WorkspaceProviderState = old.WorkspaceProviderState
	}
	if next.OwnerHealth == "" {
		next.OwnerHealth = old.OwnerHealth
	}
	return next
}

func optionalEventTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
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

func upsertWorkspaceView(state *State, p EventPayload) {
	sessionID := p.Session
	if sessionID == "" {
		sessionID = p.ID
	}
	row := manager.SessionSummary{
		ID: sessionID, Profile: p.Profile, EnvironmentID: p.EnvironmentID,
		WorkspaceID: p.WorkspaceID, WorkspaceLabel: redactText(p.WorkspaceLabel),
		GuestWorkspace: p.GuestWorkspace, WorkspaceTransport: p.WorkspaceTransport,
		WorkspaceViewState:     p.WorkspaceViewState,
		WorkspaceRelations:     append([]workspaceattach.RootRelationNotice(nil), p.WorkspaceRelations...),
		WorkspaceCleanupStatus: p.CleanupStatus, WorkspaceBlockerCode: redactText(p.BlockerCode),
	}
	updated := false
	for i := range state.Overview.Sessions {
		if state.Overview.Sessions[i].ID != sessionID {
			continue
		}
		existing := state.Overview.Sessions[i]
		if row.Profile == "" {
			row.Profile = existing.Profile
		}
		if row.EnvironmentID == "" {
			row.EnvironmentID = existing.EnvironmentID
		}
		row.Path = existing.Path
		row.AuditPath = existing.AuditPath
		row.HasAudit = existing.HasAudit
		row.HasBrokerEndpoint = existing.HasBrokerEndpoint
		row.HasNetworkPlan = existing.HasNetworkPlan
		row.HasProxySecretFile = existing.HasProxySecretFile
		row.HasEphemeralState = existing.HasEphemeralState
		row.NetworkMode = existing.NetworkMode
		row.GuestPrivilege = existing.GuestPrivilege
		row.State = existing.State
		row.OwnerStatus = existing.OwnerStatus
		row.TerminalMode = existing.TerminalMode
		row.StartedAt = existing.StartedAt
		row.UpdatedAt = existing.UpdatedAt
		row.CommandClass = existing.CommandClass
		row.CleanupError = existing.CleanupError
		state.Overview.Sessions[i] = row
		updated = true
		break
	}
	if !updated {
		state.Overview.Sessions = append(state.Overview.Sessions, row)
	}
	recomputeWorkspaceMachine(state, p.EnvironmentID)
}

func recomputeWorkspaceMachine(state *State, environmentID string) {
	if environmentID == "" {
		return
	}
	active := 0
	providerState := ""
	for _, row := range state.Overview.Sessions {
		if row.EnvironmentID != environmentID || row.WorkspaceViewState == "" {
			continue
		}
		if workspaceViewIsActive(row.WorkspaceViewState) {
			active++
		}
		providerState = mergeWorkspaceViewState(providerState, row.WorkspaceViewState)
	}
	for i := range state.Overview.Environments {
		machine := &state.Overview.Environments[i]
		if machine.ID != environmentID || machine.Mode != environment.ModeShared {
			continue
		}
		machine.ActiveSessions = active
		machine.ActiveWorkspaceViews = active
		machine.WorkspaceProviderState = providerState
	}
}

func workspaceViewIsActive(state workspaceattach.AttachmentState) bool {
	switch state {
	case workspaceattach.AttachmentProviderStarting, workspaceattach.AttachmentProviderReady,
		workspaceattach.AttachmentViewMounting, workspaceattach.AttachmentReady, workspaceattach.AttachmentDraining:
		return true
	default:
		return false
	}
}

func mergeWorkspaceViewState(current string, state workspaceattach.AttachmentState) string {
	next := "not-started"
	switch state {
	case workspaceattach.AttachmentProviderStarting:
		next = "starting"
	case workspaceattach.AttachmentProviderReady, workspaceattach.AttachmentViewMounting, workspaceattach.AttachmentReady:
		next = "ready"
	case workspaceattach.AttachmentDraining:
		next = "draining"
	case workspaceattach.AttachmentReleased:
		next = "released"
	case workspaceattach.AttachmentUnproved:
		next = "unproved"
	}
	rank := map[string]int{"": 0, "not-started": 1, "released": 2, "starting": 3, "ready": 4, "draining": 5, "unproved": 6}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func upsertBackground(state *State, p EventPayload) {
	row := BackgroundRow{ID: redactText(p.ID), Op: redactText(p.Op), Status: redactText(p.Status)}
	for i := range state.Background {
		if state.Background[i].ID == row.ID {
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
		DecisionID:      redactText(p.DecisionID),
		OperationID:     redactText(p.OperationID),
		Profile:         redactText(p.Profile),
		Status:          redactText(p.Status),
		Operation:       redactText(p.Operation),
		Path:            redactText(p.Path),
		DestinationPath: redactText(p.DestinationPath),
		PrivilegeStatus: redactText(p.PrivilegeStatus),
		Reason:          redactText(p.Reason),
	}
	for i := range state.HostFSWrites {
		if state.HostFSWrites[i].DecisionID == row.DecisionID {
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
		ID:             redactText(p.DecisionID),
		Kind:           redactText(p.RecordKind),
		Status:         redactText(p.Status),
		DefaultOutcome: redactText(p.DefaultOutcome),
		Profile:        redactText(p.Profile),
		Session:        redactText(p.Session),
		Backend:        redactText(p.Backend),
		Reason:         redactText(p.Reason),
		ClaimSurface:   redactText(p.ClaimSurface),
		ClaimOperator:  redactText(p.ClaimOperator),
		ClaimedAt:      p.ClaimedAt,
		ClaimExpiresAt: p.ClaimExpiresAt,
		Revision:       p.Revision,
	}
	for i := range state.Decisions {
		if state.Decisions[i].ID == row.ID {
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
		ID:           redactText(p.NoticeID),
		Kind:         redactText(p.RecordKind),
		Status:       redactText(p.Status),
		Severity:     redactText(p.Severity),
		Acknowledged: p.Acknowledged,
		Profile:      redactText(p.Profile),
		Session:      redactText(p.Session),
		Backend:      redactText(p.Backend),
	}
	for i := range state.Notices {
		if state.Notices[i].ID == row.ID {
			state.Notices[i] = row
			return
		}
	}
	state.Notices = append([]NoticeRow{row}, state.Notices...)
	if len(state.Notices) > tailLimit {
		state.Notices = state.Notices[:tailLimit]
	}
}

func redactText(value string) string {
	return audit.RedactString(value)
}

func redactStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i := range values {
		out[i] = redactText(values[i])
	}
	return out
}
