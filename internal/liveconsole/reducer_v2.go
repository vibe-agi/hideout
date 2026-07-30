package liveconsole

import (
	"fmt"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const projectionLimit = 256

func applyV2(state *State, event Event) ApplyResult {
	if state.RequiresReseed {
		return ApplyResult{Status: ResultStale, Reason: "authoritative reseed required"}
	}
	if state.Version != SeedVersionV2 {
		reason := "v2 event requires an authoritative v2 snapshot"
		markNeedsReseed(state, HealthSchemaMismatch, reason)
		return ApplyResult{Status: ResultStale, Reason: reason}
	}
	if err := ValidateEvent(event); err != nil {
		markNeedsReseed(state, HealthSchemaMismatch, err.Error())
		return ApplyResult{Status: ResultStale, Reason: err.Error()}
	}
	if event.InstanceID != state.DaemonInstanceID {
		reason := "daemon instance changed"
		markNeedsReseed(state, HealthStale, reason)
		return ApplyResult{Status: ResultStale, Reason: reason}
	}
	if event.CredentialGeneration != state.CredentialGeneration {
		reason := "stream credential generation changed"
		markNeedsReseed(state, HealthCredentialExpired, reason)
		return ApplyResult{Status: ResultStale, Reason: reason}
	}
	if event.Kind == KindTerminal {
		health := HealthDisconnected
		if credentialTerminalReason(event.Payload.Reason) {
			health = HealthCredentialExpired
		}
		markNeedsReseed(state, health, event.Payload.Reason)
		return ApplyResult{Status: ResultStale, Reason: event.Payload.Reason}
	}
	if event.Seq <= state.LastSeq {
		return ApplyResult{Status: ResultIgnored, Reason: "old event"}
	}
	if event.Seq != state.LastSeq+1 {
		reason := "event sequence gap"
		markNeedsReseed(state, HealthStale, reason)
		return ApplyResult{Status: ResultStale, Reason: reason}
	}
	if !knownEventKind(event.Kind) {
		state.LastSeq = event.Seq
		state.StreamHealth = StreamHealth{State: HealthLive}
		state.Diagnostics = appendCappedDiagnostic(
			state.Diagnostics,
			fmt.Sprintf("ignored optional event kind %s", event.Kind),
		)
		return ApplyResult{Status: ResultIgnored, Reason: "unknown optional event kind"}
	}

	state.LastSeq = event.Seq
	state.StreamHealth = StreamHealth{State: HealthLive}
	if !eventMatchesProfile(state.ProfileScope, event) {
		return ApplyResult{Status: ResultIgnored, Reason: "event outside profile scope"}
	}

	switch event.Kind {
	case KindProfile:
		upsertProfileProjection(state, *event.Payload.ProfileProjection)
	case KindTransition:
		upsertTransitionProjection(state, *event.Payload.TransitionProjection)
	case KindOperation:
		upsertOperationProjection(state, *event.Payload.OperationProjection)
	case KindActivity:
		applyActivityProjection(state, *event.Payload.ActivityProjection)
	case KindCoverage:
		upsertCoverageProjections(state, event.Payload.CoverageProjection)
	case KindRisk:
		upsertRiskProjection(state, *event.Payload.RiskProjection)
	case KindCapability:
		upsertCapabilityProjection(state, *event.Payload.CapabilityProjection)
	case KindEnvironment:
		upsertEnvironment(state, event.Payload)
	case KindSession:
		upsertSession(state, event.Payload)
	case KindWorkspaceView:
		upsertWorkspaceView(state, event.Payload)
	case KindBackground:
		upsertBackground(state, event.Payload)
	case KindAudit:
		appendAudit(state, event.Payload)
	case KindExport:
		state.ExportOutcomes = appendCappedOutcome(state.ExportOutcomes, OutcomeRow{
			Status: redactText(event.Payload.Status), Source: redactText(event.Payload.Source),
			ArtifactPath: redactText(event.Payload.ArtifactPath), Decision: redactText(event.Payload.Decision),
		})
	case KindCleanup:
		state.CleanupOutcomes = appendCappedOutcome(state.CleanupOutcomes, OutcomeRow{
			Status: redactText(event.Payload.Status), Sessions: event.Payload.Sessions,
			Removed: redactStringSlice(event.Payload.Removed), SecretState: redactText(event.Payload.SecretState),
		})
	case KindHostFSWrite:
		upsertHostFSWrite(state, event.Payload)
	case KindDecision:
		upsertDecision(state, event.Payload)
	case KindNotice:
		upsertNotice(state, event.Payload)
	case KindLifecycle:
		upsertLifecycle(state, event.Payload)
	}
	return ApplyResult{Status: ResultApplied}
}

func markNeedsReseed(state *State, health, reason string) {
	state.ReadOnly = true
	state.RequiresReseed = true
	markHealth(state, health, reason)
}

func credentialTerminalReason(reason string) bool {
	switch reason {
	case "credential invalidated", "credential-invalidated", "credential expired", "credential-expired":
		return true
	default:
		return false
	}
}

func appendCappedDiagnostic(values []string, value string) []string {
	if len(values) >= tailLimit {
		copy(values, values[len(values)-tailLimit+1:])
		values = values[:tailLimit-1]
	}
	return append(values, value)
}

func upsertProfileProjection(state *State, projection manager.ProfileProjection) {
	cloned := cloneProfileProjections([]manager.ProfileProjection{projection})[0]
	for index := range state.Profiles {
		if state.Profiles[index].Profile == projection.Profile {
			state.Profiles[index] = cloned
			return
		}
	}
	state.Profiles = appendCappedProjection(state.Profiles, cloned)
}

func upsertTransitionProjection(state *State, projection TransitionProjection) {
	cloned := cloneTransitions([]TransitionProjection{projection})[0]
	for index := range state.Transitions {
		if state.Transitions[index].Profile == projection.Profile {
			state.Transitions[index] = cloned
			updateProfileTransition(state, cloned)
			return
		}
	}
	state.Transitions = appendCappedProjection(state.Transitions, cloned)
	updateProfileTransition(state, cloned)
}

func updateProfileTransition(state *State, projection TransitionProjection) {
	for index := range state.Profiles {
		if state.Profiles[index].Profile != projection.Profile {
			continue
		}
		transition := projection.Transition
		transition.Blockers = append([]string(nil), projection.Transition.Blockers...)
		state.Profiles[index].Transition = &transition
		return
	}
}

func upsertOperationProjection(state *State, operation manager.Operation) {
	cloned := cloneOperations([]manager.Operation{operation})[0]
	for index := range state.Operations {
		if state.Operations[index].ID == operation.ID {
			state.Operations[index] = cloned
			return
		}
	}
	state.Operations = appendCappedProjection(state.Operations, cloned)
}

func applyActivityProjection(state *State, delta ActivityProjectionDelta) {
	if delta.Cursor != "" {
		state.Activity.Cursor = delta.Cursor
	}
	for _, count := range delta.Counts {
		found := false
		for index := range state.Activity.Counts {
			if state.Activity.Counts[index].Kind == count.Kind {
				state.Activity.Counts[index] = count
				found = true
				break
			}
		}
		if !found {
			state.Activity.Counts = appendCappedProjection(state.Activity.Counts, count)
		}
	}
	if !delta.LastAt.IsZero() {
		state.Activity.RetainedTo = delta.LastAt
	}
}

func upsertCoverageProjections(state *State, intervals []workloadtypes.CoverageInterval) {
	for _, interval := range cloneCoverage(intervals) {
		found := false
		for index := range state.Coverage {
			if state.Coverage[index].ID == interval.ID {
				state.Coverage[index] = interval
				found = true
				break
			}
		}
		if !found {
			state.Coverage = appendCappedProjection(state.Coverage, interval)
		}
	}
}

func upsertRiskProjection(state *State, finding RiskFinding) {
	cloned := cloneRisks([]RiskFinding{finding})[0]
	for index := range state.Risks {
		if state.Risks[index].ID == finding.ID {
			state.Risks[index] = cloned
			return
		}
	}
	state.Risks = appendCappedProjection(state.Risks, cloned)
}

func upsertCapabilityProjection(state *State, capability CapabilityProjection) {
	cloned := cloneCapabilities([]CapabilityProjection{capability})[0]
	for index := range state.Capabilities {
		if state.Capabilities[index].ID == capability.ID {
			state.Capabilities[index] = cloned
			return
		}
	}
	state.Capabilities = appendCappedProjection(state.Capabilities, cloned)
}

func appendCappedProjection[T any](values []T, value T) []T {
	if len(values) >= projectionLimit {
		copy(values, values[len(values)-projectionLimit+1:])
		values = values[:projectionLimit-1]
	}
	return append(values, value)
}
