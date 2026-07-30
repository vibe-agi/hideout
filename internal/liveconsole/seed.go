package liveconsole

import (
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
)

func BuildSeed(input SeedInput) Seed {
	generatedAt := input.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	health := input.StreamHealth
	if health == "" {
		health = HealthLive
	}
	overview := scopeOverview(input.Overview, input.ProfileScope)
	version := SeedVersion
	if input.DaemonInstanceID != "" || input.CredentialGeneration != 0 || input.EventSequence != 0 {
		version = SeedVersionV2
	}
	return Seed{
		Version:              version,
		GeneratedAt:          generatedAt.UTC(),
		DaemonInstanceID:     input.DaemonInstanceID,
		CredentialGeneration: input.CredentialGeneration,
		EventSequence:        input.EventSequence,
		Overview:             overview,
		Profiles:             cloneProfileProjections(input.Profiles),
		Transitions:          cloneTransitions(input.Transitions),
		Operations:           cloneOperations(input.Operations),
		Activity:             cloneActivityProjection(input.Activity),
		Coverage:             cloneCoverage(input.Coverage),
		ActivityRetention:    cloneActivityRetention(input.ActivityRetention),
		ActivityStoreRetention: cloneActivityStoreRetention(
			input.ActivityStoreRetention,
		),
		Risks:           cloneRisks(input.Risks),
		Capabilities:    cloneCapabilities(input.Capabilities),
		NextActions:     append([]NextActionRef(nil), input.NextActions...),
		AuditTail:       scopeAudit(input.AuditTail, input.ProfileScope),
		DeniedAuditTail: scopeAudit(input.DeniedAuditTail, input.ProfileScope),
		Background:      append([]BackgroundRow(nil), input.Background...),
		HostFSWrites:    filterHostFSWrites(input.HostFSWrites, input.ProfileScope),
		Decisions:       filterDecisionRows(input.Decisions, input.ProfileScope),
		Notices:         filterNoticeRows(input.Notices, input.ProfileScope),
		StatusRows:      append([]StatusRow(nil), input.StatusRows...),
		Lifecycle:       filterLifecycle(input.Lifecycle, overview),
		StreamHealth:    StreamHealth{State: health},
		ProfileScope:    input.ProfileScope,
	}
}

func NewState(seed Seed) State {
	readOnly := seed.Version != SeedVersionV2 ||
		!daemonInstancePattern.MatchString(seed.DaemonInstanceID) ||
		seed.CredentialGeneration == 0 ||
		seed.EventSequence < 0 ||
		(seed.StreamHealth.State != HealthLive && seed.StreamHealth.State != HealthIdleLive)
	return State{
		Version:              seed.Version,
		DaemonInstanceID:     seed.DaemonInstanceID,
		CredentialGeneration: seed.CredentialGeneration,
		Overview:             seed.Overview,
		Profiles:             cloneProfileProjections(seed.Profiles),
		Transitions:          cloneTransitions(seed.Transitions),
		Operations:           cloneOperations(seed.Operations),
		Activity:             cloneActivityProjection(seed.Activity),
		Coverage:             cloneCoverage(seed.Coverage),
		ActivityRetention:    cloneActivityRetention(seed.ActivityRetention),
		ActivityStoreRetention: cloneActivityStoreRetention(
			seed.ActivityStoreRetention,
		),
		Risks:           cloneRisks(seed.Risks),
		Capabilities:    cloneCapabilities(seed.Capabilities),
		NextActions:     append([]NextActionRef(nil), seed.NextActions...),
		AuditTail:       append([]audit.Event(nil), seed.AuditTail...),
		DeniedAuditTail: append([]audit.Event(nil), seed.DeniedAuditTail...),
		Background:      append([]BackgroundRow(nil), seed.Background...),
		HostFSWrites:    append([]HostFSWriteRow(nil), seed.HostFSWrites...),
		Decisions:       append([]DecisionRow(nil), seed.Decisions...),
		Notices:         append([]NoticeRow(nil), seed.Notices...),
		StatusRows:      append([]StatusRow(nil), seed.StatusRows...),
		Lifecycle:       append([]lifecycle.Status(nil), seed.Lifecycle...),
		StreamHealth:    seed.StreamHealth,
		LastSeq:         seed.EventSequence,
		ProfileScope:    seed.ProfileScope,
		ReadOnly:        readOnly,
		Seen:            map[string]map[string]bool{},
	}
}

func filterLifecycle(values []lifecycle.Status, overview manager.Overview) []lifecycle.Status {
	if len(values) == 0 {
		return nil
	}
	if len(overview.Environments) == 0 {
		return append([]lifecycle.Status(nil), values...)
	}
	visible := make(map[string]bool, len(overview.Environments))
	for _, environment := range overview.Environments {
		visible[environment.ID] = true
	}
	out := make([]lifecycle.Status, 0, len(values))
	for _, value := range values {
		if visible[value.EnvironmentID] {
			out = append(out, value)
		}
	}
	return out
}

func scopeOverview(in manager.Overview, profileName string) manager.Overview {
	if profileName == "" {
		return in
	}
	in.Profiles = filterProfiles(in.Profiles, profileName)
	in.Environments = filterEnvironments(in.Environments, profileName)
	in.Sessions = filterSessions(in.Sessions, profileName)
	in.Network.ProfileDefaults = filterNetwork(in.Network.ProfileDefaults, profileName)
	return in
}

func scopeAudit(events []audit.Event, profileName string) []audit.Event {
	out := make([]audit.Event, 0, len(events))
	for _, event := range events {
		if profileName == "" || event.Profile == profileName {
			event.Details = audit.RedactDetails(event.Details)
			out = append(out, event)
		}
	}
	return out
}

func filterProfiles(values []manager.ProfileSummary, profileName string) []manager.ProfileSummary {
	out := make([]manager.ProfileSummary, 0, len(values))
	for _, value := range values {
		if value.Name == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterEnvironments(values []manager.EnvironmentSummary, profileName string) []manager.EnvironmentSummary {
	out := make([]manager.EnvironmentSummary, 0, len(values))
	for _, value := range values {
		if value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterSessions(values []manager.SessionSummary, profileName string) []manager.SessionSummary {
	out := make([]manager.SessionSummary, 0, len(values))
	for _, value := range values {
		if value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterNetwork(values []manager.ProfileNetworkSummary, profileName string) []manager.ProfileNetworkSummary {
	out := make([]manager.ProfileNetworkSummary, 0, len(values))
	for _, value := range values {
		if value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterHostFSWrites(values []HostFSWriteRow, profileName string) []HostFSWriteRow {
	out := make([]HostFSWriteRow, 0, len(values))
	for _, value := range values {
		if profileName == "" || value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterDecisionRows(values []DecisionRow, profileName string) []DecisionRow {
	out := make([]DecisionRow, 0, len(values))
	for _, value := range values {
		if profileName == "" || value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterNoticeRows(values []NoticeRow, profileName string) []NoticeRow {
	out := make([]NoticeRow, 0, len(values))
	for _, value := range values {
		if profileName == "" || value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}
