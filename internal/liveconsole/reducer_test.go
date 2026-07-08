package liveconsole

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReducerAppliesRepresentativeEvents(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	for _, ev := range RepresentativeEvents() {
		result := Apply(&state, ev)
		if ev.Kind == KindTerminal {
			if result.Status != ResultStale || state.StreamHealth.State != HealthDisconnected {
				t.Fatalf("terminal result mismatch: result=%+v health=%+v", result, state.StreamHealth)
			}
			continue
		}
		if result.Status != ResultApplied {
			t.Fatalf("event %s not applied: %+v", ev.Kind, result)
		}
	}
	if len(state.Overview.Environments) != 1 {
		t.Fatalf("environment not applied: %+v", state.Overview.Environments)
	}
	if len(state.Overview.Sessions) != 1 {
		t.Fatalf("session not applied: %+v", state.Overview.Sessions)
	}
	if len(state.Background) != 1 {
		t.Fatalf("background not applied: %+v", state.Background)
	}
	if len(state.AuditTail) == 0 || len(state.DeniedAuditTail) == 0 {
		t.Fatalf("audit tails not applied: audit=%+v denied=%+v", state.AuditTail, state.DeniedAuditTail)
	}
	if len(state.Decisions) != 1 {
		t.Fatalf("decision not applied: %+v", state.Decisions)
	}
	if len(state.Notices) != 1 {
		t.Fatalf("notice not applied: %+v", state.Notices)
	}
}

func TestReducerIgnoresDuplicateAndOldEvents(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	ev := RepresentativeEvents()[0]
	if result := Apply(&state, ev); result.Status != ResultApplied {
		t.Fatalf("first apply: %+v", result)
	}
	if result := Apply(&state, ev); result.Status != ResultIgnored {
		t.Fatalf("duplicate should be ignored: %+v", result)
	}
	old := ev
	old.Seq = 0
	if result := Apply(&state, old); result.Status != ResultIgnored {
		t.Fatalf("old event should be ignored: %+v", result)
	}
}

func TestReducerMarksStaleOnGapAndSchemaMismatch(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	if result := Apply(&state, RepresentativeEvents()[0]); result.Status != ResultApplied {
		t.Fatalf("first apply: %+v", result)
	}
	gap := RepresentativeEvents()[1]
	gap.Seq = 9
	if result := Apply(&state, gap); result.Status != ResultStale || state.StreamHealth.State != HealthStale {
		t.Fatalf("gap should mark stale: result=%+v health=%+v", result, state.StreamHealth)
	}

	state = NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	bad := Event{Version: EventVersion, Kind: KindBackground, Seq: 1, Payload: EventPayload{ID: "bg-1"}}
	if result := Apply(&state, bad); result.Status != ResultStale || state.StreamHealth.State != HealthSchemaMismatch {
		t.Fatalf("schema mismatch should mark stale: result=%+v health=%+v", result, state.StreamHealth)
	}
}

func TestReducerIgnoresUnknownKindWithoutStalingStream(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	if result := Apply(&state, Event{Version: EventVersion, Kind: "future-kind", Seq: 1, Payload: EventPayload{ID: "future"}}); result.Status != ResultIgnored {
		t.Fatalf("unknown kind should be ignored: %+v", result)
	}
	if state.LastSeq != 1 || state.StreamHealth.State != HealthLive {
		t.Fatalf("unknown kind should advance seq without staling stream: %+v", state.StreamHealth)
	}
	if result := Apply(&state, Event{Version: EventVersion, Kind: KindEnvironment, Seq: 2, Payload: EventPayload{ID: "env-2"}}); result.Status != ResultApplied {
		t.Fatalf("event after unknown kind should apply without gap: %+v", result)
	}
}

func TestReducerRedactsControlPlaneDetailsFromAuditEvents(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	result := Apply(&state, Event{
		Version: EventVersion,
		Kind:    KindAudit,
		Seq:     1,
		Payload: EventPayload{
			Action:   "host.open",
			Decision: "allow",
			Details: map[string]any{
				"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
				"note":            "keep-me",
				"machineId":       "0123456789abcdef0123456789abcdef",
				"message":         "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1",
			},
		},
	})
	if result.Status != ResultApplied {
		t.Fatalf("audit event should apply: %+v", result)
	}
	data, err := json.Marshal(state.AuditTail)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"cap_0123456789abcdef", "0123456789abcdef0123456789abcdef", "socks5://127.0.0.1:1"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("audit event leaked control-plane value %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "keep-me") {
		t.Fatalf("user data should remain local and visible: %s", text)
	}
}

func TestReducerUpdatesDecisionAndNoticePanels(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	decision := Event{
		Version: EventVersion,
		Kind:    KindDecision,
		Seq:     1,
		Entity:  EntityRef{Kind: KindDecision, ID: "dec_1", Profile: "default", Session: "ses_1"},
		Payload: EventPayload{
			DecisionID:     "dec_1",
			RecordKind:     "hostfs.write",
			Status:         "pending",
			DefaultOutcome: "discard",
			Profile:        "default",
			Session:        "ses_1",
			Backend:        "native",
		},
	}
	if result := Apply(&state, decision); result.Status != ResultApplied {
		t.Fatalf("decision event should apply: %+v", result)
	}
	if len(state.Decisions) != 1 || state.Decisions[0].ID != "dec_1" || state.Decisions[0].Status != "pending" {
		t.Fatalf("decision row mismatch: %+v", state.Decisions)
	}

	decision.Payload.Status = "applied"
	decision.Payload.Reason = "operator-approved"
	decision.Seq = 2
	if result := Apply(&state, decision); result.Status != ResultApplied {
		t.Fatalf("decision update should apply: %+v", result)
	}
	if len(state.Decisions) != 1 || state.Decisions[0].Status != "applied" || state.Decisions[0].Reason != "operator-approved" {
		t.Fatalf("decision update mismatch: %+v", state.Decisions)
	}

	notice := Event{
		Version: EventVersion,
		Kind:    KindNotice,
		Seq:     3,
		Entity:  EntityRef{Kind: KindNotice, ID: "notice_1", Profile: "default", Session: "ses_1"},
		Payload: EventPayload{
			NoticeID:     "notice_1",
			RecordKind:   "privilege.status",
			Status:       "degraded",
			Severity:     "warning",
			Acknowledged: false,
			Profile:      "default",
			Session:      "ses_1",
			Backend:      "lima",
		},
	}
	if result := Apply(&state, notice); result.Status != ResultApplied {
		t.Fatalf("notice event should apply: %+v", result)
	}
	if len(state.Notices) != 1 || state.Notices[0].ID != "notice_1" || state.Notices[0].Acknowledged {
		t.Fatalf("notice row mismatch: %+v", state.Notices)
	}

	notice.Payload.Acknowledged = true
	notice.Seq = 4
	if result := Apply(&state, notice); result.Status != ResultApplied {
		t.Fatalf("notice ack update should apply: %+v", result)
	}
	if len(state.Notices) != 1 || !state.Notices[0].Acknowledged || state.Notices[0].Severity != "warning" {
		t.Fatalf("notice ack update mismatch: %+v", state.Notices)
	}
}

func TestReducerCredentialTerminal(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	ev := Event{
		Version: EventVersion,
		Kind:    KindTerminal,
		Seq:     1,
		Payload: EventPayload{Reason: "credential invalidated"},
	}
	result := Apply(&state, ev)
	if result.Status != ResultStale || state.StreamHealth.State != HealthCredentialExpired {
		t.Fatalf("credential terminal mismatch: result=%+v health=%+v", result, state.StreamHealth)
	}
}

func TestReducerStreamHealthDisconnectAndReseedRecovery(t *testing.T) {
	state := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	if result := Apply(&state, Event{
		Version: EventVersion,
		Kind:    KindTerminal,
		Seq:     1,
		Payload: EventPayload{Reason: "daemon restart"},
	}); result.Status != ResultStale || state.StreamHealth.State != HealthDisconnected {
		t.Fatalf("disconnect terminal mismatch: result=%+v health=%+v", result, state.StreamHealth)
	}
	if state.LastSeq != 1 {
		t.Fatalf("terminal should advance sequence before re-seed, got %d", state.LastSeq)
	}

	reseed := NewState(BuildSeed(SeedInput{StreamHealth: HealthLive}))
	if reseed.LastSeq != 0 || reseed.StreamHealth.State != HealthLive {
		t.Fatalf("re-seed should reset stale sequence and health: %+v", reseed)
	}
	if result := Apply(&reseed, RepresentativeEvents()[0]); result.Status != ResultApplied {
		t.Fatalf("event after re-seed should apply from seq=1: %+v", result)
	}
}
