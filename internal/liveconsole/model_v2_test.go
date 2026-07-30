package liveconsole

import (
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestV2SeedOwnsNestedProjectionStateAndEnablesMutation(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	endSequence := uint64(3)
	input := SeedInput{
		GeneratedAt:          now,
		DaemonInstanceID:     "daemon_fixture_a",
		CredentialGeneration: 7,
		EventSequence:        10,
		Profiles: []manager.ProfileProjection{{
			Schema: manager.ProfileProjectionSchema, Profile: "default", Revision: 2,
			Desired:    profile.Profile{Name: "default", Metadata: map[string]string{"owner": "input"}},
			Transition: &manager.ProfileTransition{Blockers: []string{"input-blocker"}},
		}},
		Transitions: []TransitionProjection{{
			Profile: "default",
			Transition: manager.ProfileTransition{
				OperationID: "op_fixture0001", Kind: "network", Phase: "staging",
				Blockers: []string{"input-blocker"}, StartedAt: now,
			},
		}},
		Operations: []manager.Operation{{
			ID: "op_fixture0001",
			Effects: []manager.EffectResult{{
				ID: "effect-1", Evidence: []manager.EvidenceRef{{Code: "fixture"}},
			}},
		}},
		Activity: ActivityProjection{
			Counts: []ActivityCount{{Kind: workloadtypes.ActivityProcess, Count: 1}},
			Recent: []workloadtypes.ActivityRecord{{
				Actor: &workloadtypes.Actor{User: "alice"},
				Subject: workloadtypes.ProcessSubject{
					Argv: []string{"tool", "input"},
				},
			}},
		},
		Coverage: []workloadtypes.CoverageInterval{{
			Evidence:    []workloadtypes.CoverageEvidence{{Code: "fixture", Value: "input"}},
			EndSequence: &endSequence,
		}},
		Risks: []RiskFinding{{
			ID: "risk_fixture0001", EvidenceRefs: []string{"act_fixture0001"},
		}},
		Capabilities: []CapabilityProjection{{
			ID: "network.proxy", ActionRefs: []string{"config.proxy"},
		}},
	}

	seed := BuildSeed(input)
	input.Profiles[0].Desired.Metadata["owner"] = "mutated"
	input.Transitions[0].Transition.Blockers[0] = "mutated"
	input.Operations[0].Effects[0].Evidence[0].Code = "mutated"
	input.Activity.Counts[0].Count = 99
	input.Activity.Recent[0].Actor.User = "mutated"
	input.Activity.Recent[0].Subject.(workloadtypes.ProcessSubject).Argv[1] = "mutated"
	input.Coverage[0].Evidence[0].Value = "mutated"
	input.Risks[0].EvidenceRefs[0] = "mutated"
	input.Capabilities[0].ActionRefs[0] = "mutated"

	state := NewState(seed)
	seed.Capabilities[0].ActionRefs[0] = "seed-mutated"

	if seed.Version != SeedVersionV2 || state.LastSeq != 10 || !state.CanMutate() {
		t.Fatalf("v2 seed header/state mismatch: seed=%+v state=%+v", seed, state)
	}
	if state.Profiles[0].Desired.Metadata["owner"] != "input" ||
		state.Transitions[0].Transition.Blockers[0] != "input-blocker" ||
		state.Operations[0].Effects[0].Evidence[0].Code != "fixture" ||
		state.Activity.Counts[0].Count != 1 ||
		state.Activity.Recent[0].Actor.User != "alice" ||
		state.Activity.Recent[0].Subject.(workloadtypes.ProcessSubject).Argv[1] != "input" ||
		state.Coverage[0].Evidence[0].Value != "input" ||
		state.Risks[0].EvidenceRefs[0] != "act_fixture0001" ||
		state.Capabilities[0].ActionRefs[0] != "config.proxy" {
		t.Fatalf("projection aliases escaped into v2 state: %+v", state)
	}
}

func TestV1SeedRemainsConsumableButReadOnly(t *testing.T) {
	seed := BuildSeed(SeedInput{StreamHealth: HealthLive})
	if err := validateJSON(compileSchema(t, "../../schemas/live-console-seed.schema.json"), seed); err != nil {
		t.Fatalf("v1 compatibility seed schema validation: %v", err)
	}
	state := NewState(seed)
	if state.CanMutate() {
		t.Fatal("legacy v1 seed must not authorize mutation")
	}
	result := Apply(&state, Event{
		Version: EventVersion, Kind: KindEnvironment, Seq: 1,
		Payload: EventPayload{ID: "env_fixture"},
	})
	if result.Status != ResultApplied || len(state.Overview.Environments) != 1 {
		t.Fatalf("v1 compatibility projection stopped working: result=%+v state=%+v", result, state)
	}
}

func TestV2ProjectionEventValidatesAgainstSchemaAndReducer(t *testing.T) {
	state := newV2ReducerState()
	event := Event{
		Version: EventVersionV2, InstanceID: state.DaemonInstanceID,
		CredentialGeneration: state.CredentialGeneration,
		Kind:                 KindCapability, Seq: 11,
		Payload: EventPayload{CapabilityProjection: &CapabilityProjection{
			ID: "network.proxy", Status: workloadtypes.CoverageAvailable,
			Provider: "keychain", Mutable: true, ActionRefs: []string{"config.proxy"},
		}},
	}
	if err := ValidateEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := validateJSON(compileSchema(t, "../../schemas/daemon-event-v2.schema.json"), event); err != nil {
		t.Fatalf("v2 schema validation: %v", err)
	}
	if result := Apply(&state, event); result.Status != ResultApplied {
		t.Fatalf("v2 projection apply: %+v", result)
	}
	if len(state.Capabilities) != 1 || state.Capabilities[0].Provider != "keychain" || !state.CanMutate() {
		t.Fatalf("capability projection mismatch: %+v", state)
	}
}

func TestV2SchemaAllowsUnknownOptionalKindForReducerCompatibility(t *testing.T) {
	event := Event{
		Version: EventVersionV2, InstanceID: "daemon_fixture_a",
		CredentialGeneration: 7, Kind: "future-optional-kind", Optional: true, Seq: 1,
	}
	if err := ValidateEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := validateJSON(compileSchema(t, "../../schemas/daemon-event-v2.schema.json"), event); err != nil {
		t.Fatalf("optional event schema validation: %v", err)
	}
}
