package liveconsole

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestBuildSeedScopesProfileAndRedactsAudit(t *testing.T) {
	seed := BuildSeed(SeedInput{
		GeneratedAt:  fixedTime(),
		ProfileScope: "alpha",
		Overview: manager.Overview{
			Version: "hideout.manager/v1",
			Profiles: []manager.ProfileSummary{
				{Name: "alpha"},
				{Name: "beta"},
			},
			Environments: []manager.EnvironmentSummary{
				{ID: "env-alpha", Name: "alpha-env", Profile: "alpha", Status: "stopped"},
				{ID: "env-beta", Name: "beta-env", Profile: "beta", Status: "stopped"},
			},
			Sessions: []manager.SessionSummary{
				{ID: "ses_alpha", Profile: "alpha", HasAudit: true},
				{ID: "ses_beta", Profile: "beta", HasAudit: true},
			},
			Network: manager.NetworkSummary{ProfileDefaults: []manager.ProfileNetworkSummary{
				{Profile: "alpha", Mode: "direct"},
				{Profile: "beta", Mode: "tun2socks"},
			}},
		},
		AuditTail: []audit.Event{
			{Profile: "alpha", Action: "run", Decision: "allow", Details: map[string]any{"uiToken": "ui_0123456789abcdef"}},
			{Profile: "beta", Action: "run", Decision: "allow"},
		},
		DeniedAuditTail: []audit.Event{
			{Profile: "alpha", Action: "host.open", Decision: "deny", Details: map[string]any{"message": "HIDEOUT_SECRET_PROXY=top-secret"}},
		},
		HostFSWrites: []HostFSWriteRow{
			{DecisionID: "hfw-alpha", Profile: "alpha", Status: "pending"},
			{DecisionID: "hfw-beta", Profile: "beta", Status: "pending"},
		},
		Decisions: []DecisionRow{
			{ID: "dec-alpha", Profile: "alpha", Status: "pending"},
			{ID: "dec-beta", Profile: "beta", Status: "pending"},
		},
		Notices: []NoticeRow{
			{ID: "notice-alpha", Profile: "alpha"},
			{ID: "notice-beta", Profile: "beta"},
		},
	})

	if seed.Version != SeedVersion || seed.GeneratedAt.IsZero() {
		t.Fatalf("seed header mismatch: %+v", seed)
	}
	if got := len(seed.Overview.Profiles); got != 1 || seed.Overview.Profiles[0].Name != "alpha" {
		t.Fatalf("profiles not scoped: %+v", seed.Overview.Profiles)
	}
	if got := len(seed.Overview.Environments); got != 1 || seed.Overview.Environments[0].ID != "env-alpha" {
		t.Fatalf("environments not scoped: %+v", seed.Overview.Environments)
	}
	if got := len(seed.AuditTail); got != 1 || seed.AuditTail[0].Profile != "alpha" {
		t.Fatalf("audit not scoped: %+v", seed.AuditTail)
	}
	if len(seed.HostFSWrites) != 1 || seed.HostFSWrites[0].DecisionID != "hfw-alpha" ||
		len(seed.Decisions) != 1 || seed.Decisions[0].ID != "dec-alpha" ||
		len(seed.Notices) != 1 || seed.Notices[0].ID != "notice-alpha" {
		t.Fatalf("action rows not scoped: hostfs=%+v decisions=%+v notices=%+v", seed.HostFSWrites, seed.Decisions, seed.Notices)
	}
	state := NewState(seed)
	if len(state.HostFSWrites) != 1 || len(state.Decisions) != 1 || len(state.Notices) != 1 {
		t.Fatalf("action rows missing from initial state: %+v", state)
	}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leaked := range []string{"ui_0123456789abcdef", "top-secret", "HIDEOUT_SECRET_PROXY"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("seed leaked control-plane material %q: %s", leaked, text)
		}
	}
}

func TestReducerConsumesButDoesNotApplyOtherProfileEvents(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{ProfileScope: "alpha", StreamHealth: HealthLive}))
	result := Apply(&state, Event{
		Version: EventVersion,
		Kind:    KindDecision,
		Seq:     1,
		Entity:  EntityRef{Kind: KindDecision, ID: "dec-beta", Profile: "beta"},
		Payload: EventPayload{DecisionID: "dec-beta", RecordKind: "evidence.share", Status: "pending", Profile: "beta"},
	})
	if result.Status != ResultIgnored || state.LastSeq != 1 || len(state.Decisions) != 0 {
		t.Fatalf("other-profile event changed scoped state: result=%+v state=%+v", result, state)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
}
