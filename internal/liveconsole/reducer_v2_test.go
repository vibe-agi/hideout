package liveconsole

import "testing"

func TestReducerV2RejectsDaemonInstanceChangeUntilReseed(t *testing.T) {
	state := newV2ReducerState()
	result := Apply(&state, Event{
		Version:              EventVersionV2,
		InstanceID:           "daemon_fixture_b",
		CredentialGeneration: 7,
		Kind:                 KindEnvironment,
		Seq:                  11,
		Payload:              EventPayload{ID: "env_fixture"},
	})
	if result.Status != ResultStale || state.StreamHealth.State != HealthStale {
		t.Fatalf("instance change did not stale the state: result=%+v state=%+v", result, state)
	}
	if state.LastSeq != 10 || state.CanMutate() {
		t.Fatalf("instance change advanced or left mutation enabled: seq=%d mutable=%t", state.LastSeq, state.CanMutate())
	}

	reseed := NewState(BuildSeed(SeedInput{
		StreamHealth:         HealthLive,
		DaemonInstanceID:     "daemon_fixture_b",
		CredentialGeneration: 8,
		EventSequence:        20,
	}))
	if !reseed.CanMutate() || reseed.LastSeq != 20 || reseed.DaemonInstanceID != "daemon_fixture_b" {
		t.Fatalf("authoritative reseed did not restore v2 state: %+v", reseed)
	}
}

func TestReducerV2SequenceGapIsStickyReadOnly(t *testing.T) {
	state := newV2ReducerState()
	gap := Event{
		Version:              EventVersionV2,
		InstanceID:           state.DaemonInstanceID,
		CredentialGeneration: state.CredentialGeneration,
		Kind:                 KindEnvironment,
		Seq:                  12,
		Payload:              EventPayload{ID: "env_fixture"},
	}
	if result := Apply(&state, gap); result.Status != ResultStale {
		t.Fatalf("gap result=%+v want stale", result)
	}
	if state.CanMutate() || state.LastSeq != 10 {
		t.Fatalf("gap did not preserve read-only seed: seq=%d mutable=%t", state.LastSeq, state.CanMutate())
	}
	next := gap
	next.Seq = 11
	if result := Apply(&state, next); result.Status != ResultStale || result.Reason != "authoritative reseed required" {
		t.Fatalf("post-gap event escaped sticky stale state: %+v", result)
	}
	if len(state.Overview.Environments) != 0 {
		t.Fatalf("post-gap event mutated stale projection: %+v", state.Overview.Environments)
	}
}

func TestReducerV2UnknownOptionalEventAdvancesSequence(t *testing.T) {
	state := newV2ReducerState()
	optional := Event{
		Version:              EventVersionV2,
		InstanceID:           state.DaemonInstanceID,
		CredentialGeneration: state.CredentialGeneration,
		Optional:             true,
		Kind:                 "future-optional-kind",
		Seq:                  11,
	}
	if result := Apply(&state, optional); result.Status != ResultIgnored {
		t.Fatalf("optional future event result=%+v want ignored", result)
	}
	if state.LastSeq != 11 || !state.CanMutate() {
		t.Fatalf("optional event broke stream continuity: seq=%d mutable=%t", state.LastSeq, state.CanMutate())
	}

	required := optional
	required.Optional = false
	required.Seq = 12
	if result := Apply(&state, required); result.Status != ResultStale {
		t.Fatalf("unknown required event result=%+v want stale", result)
	}
	if state.CanMutate() {
		t.Fatal("unknown required event left mutation enabled")
	}
}

func TestReducerV2TerminalDoesNotInventBroadcastSequence(t *testing.T) {
	state := newV2ReducerState()
	result := Apply(&state, Event{
		Version:              EventVersionV2,
		InstanceID:           state.DaemonInstanceID,
		CredentialGeneration: state.CredentialGeneration,
		Kind:                 KindTerminal,
		Seq:                  0,
		Payload:              EventPayload{Reason: "daemon restart"},
	})
	if result.Status != ResultStale || state.StreamHealth.State != HealthDisconnected {
		t.Fatalf("terminal result=%+v health=%+v", result, state.StreamHealth)
	}
	if state.LastSeq != 10 || state.CanMutate() {
		t.Fatalf("terminal changed broadcast sequence or mutation state: seq=%d mutable=%t", state.LastSeq, state.CanMutate())
	}
}

func newV2ReducerState() State {
	return NewState(BuildSeed(SeedInput{
		StreamHealth:         HealthLive,
		DaemonInstanceID:     "daemon_fixture_a",
		CredentialGeneration: 7,
		EventSequence:        10,
	}))
}
