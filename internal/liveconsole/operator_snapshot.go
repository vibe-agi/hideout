package liveconsole

import (
	"errors"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/session"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func NewStateFromOperatorSnapshot(snapshot manager.OperatorSnapshot) (State, error) {
	if err := snapshot.Validate(); err != nil {
		return State{}, err
	}
	overview := manager.Overview{
		Version:      "hideout.manager/v1",
		Sessions:     make([]manager.SessionSummary, 0, len(snapshot.Sessions)),
		Environments: make([]manager.EnvironmentSummary, 0, len(snapshot.Environments)),
	}
	for _, value := range snapshot.Sessions {
		overview.Sessions = append(overview.Sessions, manager.SessionSummary{
			ID: value.ID, EnvironmentID: value.EnvironmentID, Profile: value.Profile,
			State: session.OwnerState(value.State), CommandClass: value.Command,
			StartedAt: value.StartedAt, WorkspaceID: value.WorkspaceID,
			WorkspaceLabel: value.WorkspaceLabel, GuestWorkspace: value.GuestWorkspace,
			WorkspaceTransport:     value.WorkspaceTransport,
			WorkspaceViewState:     value.WorkspaceViewState,
			WorkspaceRelations:     slices.Clone(value.WorkspaceRelations),
			WorkspaceCleanupStatus: value.WorkspaceCleanupStatus,
			WorkspaceBlockerCode:   value.WorkspaceBlockerCode,
		})
	}
	for _, value := range snapshot.Environments {
		projection := manager.EnvironmentSummary{
			ID: value.ID, Name: value.Name, Profile: value.Profile,
			Backend: value.Backend, Status: value.Status,
			Mode: value.Mode, SharedSlot: value.SharedSlot,
			MachineIdentityID: value.MachineIdentityID,
			Workspace:         value.Workspace, InstanceName: value.InstanceName,
			LastSessionID:          value.LastSessionID,
			LastCommand:            value.LastCommand,
			ActiveSessions:         value.ActiveSessions,
			ActiveWorkspaceViews:   value.ActiveWorkspaceViews,
			WorkspaceProviderState: value.WorkspaceProviderState,
			OwnerHealth:            value.OwnerHealth, CreatedAt: value.CreatedAt,
		}
		if !value.LastStartedAt.IsZero() {
			started := value.LastStartedAt
			projection.LastStartedAt = &started
		}
		if !value.LastEndedAt.IsZero() {
			ended := value.LastEndedAt
			projection.LastEndedAt = &ended
		}
		overview.Environments = append(
			overview.Environments,
			projection,
		)
	}
	// The canonical snapshot is sorted for stable transport. The operator view
	// starts on the newest workload, falling back to the timestamp-bearing
	// session ID when legacy rows have no StartedAt metadata.
	sort.SliceStable(overview.Sessions, func(left, right int) bool {
		leftStarted := overview.Sessions[left].StartedAt
		rightStarted := overview.Sessions[right].StartedAt
		switch {
		case leftStarted.Equal(rightStarted):
			return overview.Sessions[left].ID > overview.Sessions[right].ID
		case leftStarted.IsZero():
			return false
		case rightStarted.IsZero():
			return true
		default:
			return leftStarted.After(rightStarted)
		}
	})
	counts := make(map[string]uint64)
	for _, record := range snapshot.Activity {
		counts[record.Kind] += record.Count
	}
	activityCounts := make([]ActivityCount, 0, len(counts))
	for kind, count := range counts {
		activityCounts = append(activityCounts, ActivityCount{Kind: kind, Count: count})
	}
	sort.Slice(activityCounts, func(left, right int) bool {
		return activityCounts[left].Kind < activityCounts[right].Kind
	})
	risks := make([]RiskFinding, 0, len(snapshot.Risks))
	for _, value := range snapshot.Risks {
		explanation := value.Explanation
		if explanation == "" {
			explanation = value.Title
		}
		policyStatus := value.PolicyStatus
		if policyStatus == "" {
			policyStatus = "not-evaluated"
		}
		firstAt, lastAt := value.FirstAt, value.LastAt
		if firstAt.IsZero() {
			firstAt = snapshot.GeneratedAt
		}
		if lastAt.IsZero() {
			lastAt = firstAt
		}
		count := value.Count
		if count == 0 {
			count = 1
		}
		risks = append(risks, RiskFinding{
			ID: value.ID, RuleID: value.RuleID, RuleVersion: value.RuleVersion,
			Severity: value.Severity, Title: value.Title, Explanation: explanation,
			EvidenceRefs: append([]string(nil), value.EvidenceRefs...),
			Confidence:   value.Confidence, PolicyStatus: policyStatus,
			FirstAt: firstAt, LastAt: lastAt, Count: count, NextAction: value.NextAction,
		})
	}
	capabilities := make([]CapabilityProjection, 0, len(snapshot.Capabilities))
	for _, value := range snapshot.Capabilities {
		status, err := consoleCoverageStatus(value.State)
		if err != nil {
			return State{}, err
		}
		capabilities = append(capabilities, CapabilityProjection{
			ID: value.ID, Status: status, Provider: value.Provider,
			Reason: value.Reason, Mutable: value.Mutable,
			ActionRefs: append([]string(nil), value.ActionRefs...),
		})
	}
	actions := make([]NextActionRef, 0, len(snapshot.NextActions))
	for _, id := range snapshot.NextActions {
		actions = append(actions, consoleNextAction(id))
	}
	decisions := make([]DecisionRow, 0, len(snapshot.Decisions))
	for _, value := range snapshot.Decisions {
		decisions = append(decisions, DecisionRow{
			ID: value.ID, Kind: value.Kind, Status: value.Status,
			Summary: value.Summary, DefaultOutcome: value.DefaultOutcome,
			Profile: value.Profile, Session: value.Session, Backend: value.Backend,
			ClaimSurface:   value.ClaimSurface,
			ClaimExpiresAt: value.ClaimExpiresAt, Revision: value.Revision,
		})
	}
	notices := make([]NoticeRow, 0, len(snapshot.Notices))
	for _, value := range snapshot.Notices {
		notices = append(notices, NoticeRow{
			ID: value.ID, Kind: value.Kind, Status: value.Status,
			Summary: value.Summary, Severity: value.Severity,
			Acknowledged: value.Acknowledged,
			Profile:      value.Profile, Session: value.Session, Backend: value.Backend,
			Revision: value.Revision,
		})
	}
	seed := BuildSeed(SeedInput{
		GeneratedAt:      snapshot.GeneratedAt,
		DaemonInstanceID: snapshot.InstanceID, CredentialGeneration: snapshot.CredentialGeneration,
		EventSequence: snapshot.Sequence, StreamHealth: snapshot.StreamHealth.State,
		Overview: overview, Profiles: snapshot.Profiles, Operations: snapshot.Operations,
		Migrations: snapshot.Migrations,
		Activity: ActivityProjection{
			Cursor: snapshot.ActivityCursor, Counts: activityCounts,
			Recent: append([]workloadtypes.ActivityRecord(nil), snapshot.Activity...),
		},
		Coverage: append([]workloadtypes.CoverageInterval(nil), snapshot.Coverage...),
		ActivityRetention: cloneActivityRetention(
			snapshot.ActivityRetention,
		),
		ActivityStoreRetention: cloneActivityStoreRetention(
			snapshot.ActivityStoreRetention,
		),
		Risks: risks, Capabilities: capabilities, NextActions: actions,
		Decisions: decisions, Notices: notices,
	})
	state := NewState(seed)
	state.StreamHealth = StreamHealth{
		State: snapshot.StreamHealth.State, Reason: snapshot.StreamHealth.Reason,
	}
	if snapshot.StreamHealth.State != HealthLive && snapshot.StreamHealth.State != HealthIdleLive {
		state.ReadOnly = true
	}
	return state, nil
}

func consoleCoverageStatus(value string) (string, error) {
	switch value {
	case manager.OperatorCapabilityAvailable:
		return workloadtypes.CoverageAvailable, nil
	case manager.OperatorCapabilityPartial:
		return workloadtypes.CoveragePartial, nil
	case manager.OperatorCapabilityUnavailable:
		return workloadtypes.CoverageUnavailable, nil
	default:
		return "", errors.New("operator capability state is invalid")
	}
}

func consoleNextAction(id string) NextActionRef {
	action := NextActionRef{ID: id, Label: id}
	switch id {
	case "run.start":
		action.Label = "start a protected command"
		action.Command = "hideout run -- <command>"
	case "activity.inspect":
		action.Label = "inspect recent activity"
		action.Command = "hideout activity summary"
	case "activity.files":
		action.Label = "inspect file activity"
		action.Command = "hideout activity events --kind file"
	case "snapshot.refresh":
		action.Label = "refresh authoritative snapshot"
		action.Command = "hideout tui"
	case "doctor.activity":
		action.Label = "check activity support"
		action.Command = "hideout doctor --feature activity"
	case "doctor.overview":
		action.Label = "check manager health"
		action.Command = "hideout doctor"
	case "doctor.profiles":
		action.Label = "repair profile projections"
		action.Command = "hideout doctor --feature profiles"
	case "doctor.operations":
		action.Label = "inspect operation history"
		action.Command = "hideout tui"
	case "migration.inspect":
		action.Label = "inspect a migration bundle"
		action.Command = "hideout migrate inspect <bundle.hideout>"
	case "migration.export":
		action.Label = "export this Hideout"
		action.Command = "hideout migrate export --all --out <bundle.hideout>"
	case "migration.status":
		action.Label = "inspect migration progress"
		action.Command = "hideout migrate status <operation-id>"
	case "migration.recover":
		action.Label = "continue migration recovery"
		action.Command = "hideout migrate recover <operation-id>"
	case "doctor.migration":
		action.Label = "check migration state"
		action.Command = "hideout doctor --feature migration"
	}
	return action
}
