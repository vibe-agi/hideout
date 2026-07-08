package liveconsole

import (
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
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
	return Seed{
		Version:         SeedVersion,
		GeneratedAt:     generatedAt.UTC(),
		Overview:        overview,
		AuditTail:       scopeAudit(input.AuditTail, input.ProfileScope),
		DeniedAuditTail: scopeAudit(input.DeniedAuditTail, input.ProfileScope),
		Background:      append([]BackgroundRow(nil), input.Background...),
		StreamHealth:    StreamHealth{State: health},
		ProfileScope:    input.ProfileScope,
	}
}

func NewState(seed Seed) State {
	return State{
		Version:         seed.Version,
		Overview:        seed.Overview,
		AuditTail:       append([]audit.Event(nil), seed.AuditTail...),
		DeniedAuditTail: append([]audit.Event(nil), seed.DeniedAuditTail...),
		Background:      append([]BackgroundRow(nil), seed.Background...),
		StreamHealth:    seed.StreamHealth,
		ProfileScope:    seed.ProfileScope,
		Seen:            map[string]map[string]bool{},
	}
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
