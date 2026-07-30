package manager

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

func TestNetworkTransitionPlanReturnsEveryExactPostureBlocker(t *testing.T) {
	provider := &networkTransitionProviderFixture{
		observed: []NetworkRouteConfiguration{{
			Mode: netpolicy.ModeDirect,
		}},
	}
	sessions := []NetworkTransitionSession{
		{ID: "ses_z", Status: NetworkSessionLive, Phase: "running"},
		{ID: "ses_a", Status: NetworkSessionUnprovable, Phase: "unknown"},
	}
	service := NetworkTransitionService{
		Provider: provider,
		Sessions: NetworkTransitionSessionInventoryFunc(
			func(context.Context, string) ([]NetworkTransitionSession, error) {
				return append([]NetworkTransitionSession(nil), sessions...), nil
			},
		),
	}
	plan, err := service.Plan(context.Background(), NetworkTransitionDraft{
		EnvironmentID: "env_transition",
		Desired: NetworkRouteConfiguration{
			Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
			ProxySecretGeneration: 2, MediatedResolver: "1.1.1.1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != NetworkTransitionPosture || len(plan.Blockers) != 2 {
		t.Fatalf("posture plan=%+v", plan)
	}
	if plan.Blockers[0].Owner != "ses_a" ||
		plan.Blockers[0].Code != "network-posture-session-unprovable" ||
		plan.Blockers[1].Owner != "ses_z" ||
		plan.Blockers[1].Code != "network-posture-session-active" {
		t.Fatalf("blockers are not exact and sorted: %+v", plan.Blockers)
	}
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://user:password@127.0.0.1:7890",
		},
	)
	if !errors.Is(err, ErrNetworkTransitionBlocked) ||
		result.Phase != NetworkTransitionBlocked ||
		containsNetworkTransitionEvent(provider.events, "stage") {
		t.Fatalf("blocked apply result=%+v events=%v err=%v", result, provider.events, err)
	}
}

func TestNetworkTransitionRouteStagesProbesActivatesProvesDrainsAndCommits(t *testing.T) {
	current := NetworkRouteConfiguration{
		Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
		ProxySecretGeneration: 1, MediatedResolver: "1.1.1.1",
	}
	desired := current
	desired.ProxySecretGeneration = 2
	provider := newSuccessfulNetworkTransitionProvider(current, desired, 4)
	service := NetworkTransitionService{
		Provider: provider,
		Sessions: fixedNetworkTransitionSessions(
			NetworkTransitionSession{
				ID: "ses_existing", Status: NetworkSessionLive,
				Phase: "running",
			},
		),
	}
	plan, err := service.Plan(context.Background(), NetworkTransitionDraft{
		EnvironmentID: "env_transition", Desired: desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != NetworkTransitionRoute || len(plan.Blockers) != 0 {
		t.Fatalf("route plan=%+v", plan)
	}
	const secret = "socks5://candidate-user:candidate-password@127.0.0.1:7890"
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{UpstreamProxyURL: secret},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"observe", "observe",
		"stage", "probe", "activate", "prove", "drain", "commit",
	}
	if !reflect.DeepEqual(provider.events, wantEvents) {
		t.Fatalf("transition events=%v want=%v", provider.events, wantEvents)
	}
	if result.Phase != NetworkTransitionSucceeded ||
		!result.EffectiveProved ||
		result.Effective != desired ||
		result.ConnectionsRetained != 4 {
		t.Fatalf("successful result=%+v", result)
	}
	for _, effect := range result.Effects {
		if effect.Status != EffectSucceeded {
			t.Fatalf("effect did not succeed: %+v", effect)
		}
	}
	encoded, err := json.Marshal(struct {
		Plan   NetworkTransitionPlan   `json:"plan"`
		Result NetworkTransitionResult `json:"result"`
	}{Plan: plan, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) ||
		strings.Contains(string(encoded), "candidate-password") {
		t.Fatalf("network transition serialized secret material: %s", encoded)
	}
}

func TestNetworkTransitionDirectAndDNSChangesUseSameTransaction(t *testing.T) {
	tests := []struct {
		name     string
		current  NetworkRouteConfiguration
		desired  NetworkRouteConfiguration
		kind     string
		material NetworkCandidateMaterial
	}{
		{
			name: "proxy-to-direct",
			current: NetworkRouteConfiguration{
				Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
				ProxySecretGeneration: 3, MediatedResolver: "1.1.1.1",
			},
			desired: NetworkRouteConfiguration{Mode: netpolicy.ModeDirect},
			kind:    NetworkTransitionPosture,
		},
		{
			name: "dns",
			current: NetworkRouteConfiguration{
				Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
				ProxySecretGeneration: 3, MediatedResolver: "1.1.1.1",
			},
			desired: NetworkRouteConfiguration{
				Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
				ProxySecretGeneration: 3, MediatedResolver: "9.9.9.9",
			},
			kind: NetworkTransitionDNS,
			material: NetworkCandidateMaterial{
				UpstreamProxyURL: "socks5://127.0.0.1:7890",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := newSuccessfulNetworkTransitionProvider(
				test.current,
				test.desired,
				0,
			)
			service := NetworkTransitionService{
				Provider: provider,
				Sessions: fixedNetworkTransitionSessions(),
			}
			plan, err := service.Plan(
				context.Background(),
				NetworkTransitionDraft{
					EnvironmentID: "env_transition",
					Desired:       test.desired,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Kind != test.kind {
				t.Fatalf("kind=%q want=%q", plan.Kind, test.kind)
			}
			result, err := service.Apply(
				context.Background(),
				plan,
				test.material,
			)
			if err != nil ||
				result.Phase != NetworkTransitionSucceeded ||
				result.Effective != test.desired {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestNetworkTransitionFailureRollsBackWithoutLeakingProviderError(t *testing.T) {
	tests := []struct {
		name      string
		failEvent string
		want      []string
	}{
		{
			name: "probe", failEvent: "probe",
			want: []string{
				"observe", "observe",
				"stage", "probe", "rollback",
			},
		},
		{
			name: "proof", failEvent: "prove",
			want: []string{
				"observe", "observe",
				"stage", "probe", "activate", "prove", "rollback",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, desired := networkTransitionRouteFixture()
			const canary = "provider-user:provider-password@private.invalid"
			providerErr := errors.New(canary)
			provider := newSuccessfulNetworkTransitionProvider(
				current,
				desired,
				2,
			)
			provider.failEvent = test.failEvent
			provider.failure = providerErr
			service := NetworkTransitionService{
				Provider: provider,
				Sessions: fixedNetworkTransitionSessions(),
			}
			plan, err := service.Plan(
				context.Background(),
				NetworkTransitionDraft{
					EnvironmentID: "env_transition",
					Desired:       desired,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Apply(
				context.Background(),
				plan,
				NetworkCandidateMaterial{
					UpstreamProxyURL: "socks5://127.0.0.1:7890",
				},
			)
			if !errors.Is(err, providerErr) ||
				strings.Contains(err.Error(), canary) {
				t.Fatalf("safe wrapped failure=%v", err)
			}
			if result.Phase != NetworkTransitionRolledBack ||
				!result.EffectiveProved ||
				result.Effective != current {
				t.Fatalf("rollback result=%+v", result)
			}
			if !reflect.DeepEqual(provider.events, test.want) {
				t.Fatalf("events=%v want=%v", provider.events, test.want)
			}
			encoded, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if strings.Contains(string(encoded), canary) {
				t.Fatalf("rollback result leaked provider error: %s", encoded)
			}
		})
	}
}

func TestNetworkTransitionStageFailureProvesEffectiveState(t *testing.T) {
	t.Run("unchanged route", func(t *testing.T) {
		current, desired := networkTransitionRouteFixture()
		provider := newSuccessfulNetworkTransitionProvider(
			current,
			desired,
			0,
		)
		provider.observed = append(provider.observed, current)
		provider.failEvent = "stage"
		provider.failure = errors.New(
			"stage user:password@private.invalid",
		)
		service := NetworkTransitionService{
			Provider: provider,
			Sessions: fixedNetworkTransitionSessions(),
		}
		plan, err := service.Plan(
			context.Background(),
			NetworkTransitionDraft{
				EnvironmentID: "env_transition",
				Desired:       desired,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Apply(
			context.Background(),
			plan,
			NetworkCandidateMaterial{
				UpstreamProxyURL: "socks5://127.0.0.1:7890",
			},
		)
		if !errors.Is(err, ErrNetworkTransitionRolledBack) ||
			result.Phase != NetworkTransitionRolledBack ||
			!result.EffectiveProved ||
			result.Effective != current ||
			strings.Contains(err.Error(), "user:password") {
			t.Fatalf("proved stage failure result=%+v err=%v", result, err)
		}
		want := []string{"observe", "observe", "stage", "observe"}
		if !reflect.DeepEqual(provider.events, want) {
			t.Fatalf("stage failure events=%v want=%v", provider.events, want)
		}
	})

	t.Run("changed or unavailable route", func(t *testing.T) {
		for _, observed := range []NetworkRouteConfiguration{
			{},
			{
				Mode:                  netpolicy.ModeTun2Socks,
				ProxySecretRef:        "local-proxy",
				ProxySecretGeneration: 9,
				MediatedResolver:      "1.1.1.1",
			},
		} {
			current, desired := networkTransitionRouteFixture()
			provider := newSuccessfulNetworkTransitionProvider(
				current,
				desired,
				0,
			)
			if observed != (NetworkRouteConfiguration{}) {
				provider.observed = append(provider.observed, observed)
			}
			provider.failEvent = "stage"
			provider.failure = errors.New("stage failed")
			service := NetworkTransitionService{
				Provider: provider,
				Sessions: fixedNetworkTransitionSessions(),
			}
			plan, err := service.Plan(
				context.Background(),
				NetworkTransitionDraft{
					EnvironmentID: "env_transition",
					Desired:       desired,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Apply(
				context.Background(),
				plan,
				NetworkCandidateMaterial{
					UpstreamProxyURL: "socks5://127.0.0.1:7890",
				},
			)
			if !errors.Is(err, ErrNetworkTransitionRollbackUnproved) ||
				result.Phase != NetworkTransitionRollbackUnproved ||
				result.EffectiveProved ||
				result.Effective != (NetworkRouteConfiguration{}) {
				t.Fatalf(
					"unproved stage result=%+v observed=%+v err=%v",
					result,
					observed,
					err,
				)
			}
		}
	})
}

func TestNetworkTransitionMissingStageHandleIsRecoveryRequired(t *testing.T) {
	current, desired := networkTransitionRouteFixture()
	provider := newSuccessfulNetworkTransitionProvider(current, desired, 0)
	provider.missingStageHandle = true
	service := NetworkTransitionService{
		Provider: provider,
		Sessions: fixedNetworkTransitionSessions(),
	}
	plan, err := service.Plan(context.Background(), NetworkTransitionDraft{
		EnvironmentID: "env_transition",
		Desired:       desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRollbackUnproved) ||
		result.Phase != NetworkTransitionRollbackUnproved ||
		result.EffectiveProved ||
		result.Recovery.Code != "network-rollback-unproved" {
		t.Fatalf("missing stage handle result=%+v err=%v", result, err)
	}
}

func TestNetworkTransitionRollbackUnprovedNeverClaimsPriorEffective(t *testing.T) {
	current, desired := networkTransitionRouteFixture()
	provider := newSuccessfulNetworkTransitionProvider(current, desired, 1)
	provider.failEvent = "prove"
	provider.failure = errors.New("candidate proof failed")
	provider.rollbackFailure = errors.New(
		"rollback user:password@private.invalid",
	)
	service := NetworkTransitionService{
		Provider: provider,
		Sessions: fixedNetworkTransitionSessions(),
	}
	plan, err := service.Plan(context.Background(), NetworkTransitionDraft{
		EnvironmentID: "env_transition", Desired: desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRollbackUnproved) ||
		result.Phase != NetworkTransitionRollbackUnproved ||
		result.EffectiveProved ||
		result.Effective != (NetworkRouteConfiguration{}) ||
		result.Recovery.Code != "network-rollback-unproved" ||
		result.Recovery.NextAction == "" {
		t.Fatalf("unproved rollback result=%+v err=%v", result, err)
	}
	if strings.Contains(err.Error(), "user:password") {
		t.Fatalf("unproved rollback leaked provider detail: %v", err)
	}
}

func TestNetworkTransitionRejectsCandidateRouteGenerationDrift(t *testing.T) {
	for _, phase := range []string{"probe", "activate", "prove", "drain"} {
		t.Run(phase, func(t *testing.T) {
			current, desired := networkTransitionRouteFixture()
			provider := newSuccessfulNetworkTransitionProvider(
				current,
				desired,
				1,
			)
			provider.proofGeneration[phase] = 77
			service := NetworkTransitionService{
				Provider: provider,
				Sessions: fixedNetworkTransitionSessions(),
			}
			plan, err := service.Plan(
				context.Background(),
				NetworkTransitionDraft{
					EnvironmentID: "env_transition",
					Desired:       desired,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Apply(
				context.Background(),
				plan,
				NetworkCandidateMaterial{
					UpstreamProxyURL: "socks5://127.0.0.1:7890",
				},
			)
			if !errors.Is(err, ErrNetworkTransitionRolledBack) ||
				result.Phase != NetworkTransitionRolledBack ||
				!result.EffectiveProved ||
				result.Effective != current {
				t.Fatalf(
					"generation drift phase=%s result=%+v err=%v",
					phase,
					result,
					err,
				)
			}
		})
	}
}

func TestNetworkTransitionActivatedRollbackRequiresNewRouteGeneration(t *testing.T) {
	current, desired := networkTransitionRouteFixture()
	provider := newSuccessfulNetworkTransitionProvider(current, desired, 1)
	provider.failEvent = "prove"
	provider.failure = errors.New("candidate proof failed")
	provider.rollbackGeneration = provider.candidateGeneration
	service := NetworkTransitionService{
		Provider: provider,
		Sessions: fixedNetworkTransitionSessions(),
	}
	plan, err := service.Plan(context.Background(), NetworkTransitionDraft{
		EnvironmentID: "env_transition", Desired: desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRollbackUnproved) ||
		result.Phase != NetworkTransitionRollbackUnproved ||
		result.EffectiveProved {
		t.Fatalf("non-advancing rollback result=%+v err=%v", result, err)
	}
}

func TestNetworkTransitionApplyRechecksEffectiveStateAndBlockers(t *testing.T) {
	t.Run("effective drift", func(t *testing.T) {
		current, desired := networkTransitionRouteFixture()
		drifted := current
		drifted.ProxySecretGeneration = 99
		provider := newSuccessfulNetworkTransitionProvider(
			current,
			desired,
			0,
		)
		provider.observed[1] = drifted
		service := NetworkTransitionService{
			Provider: provider,
			Sessions: fixedNetworkTransitionSessions(),
		}
		plan, err := service.Plan(
			context.Background(),
			NetworkTransitionDraft{
				EnvironmentID: "env_transition",
				Desired:       desired,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Apply(
			context.Background(),
			plan,
			NetworkCandidateMaterial{
				UpstreamProxyURL: "socks5://127.0.0.1:7890",
			},
		)
		if !errors.Is(err, ErrNetworkTransitionStale) ||
			containsNetworkTransitionEvent(provider.events, "stage") {
			t.Fatalf("stale apply events=%v err=%v", provider.events, err)
		}
	})

	t.Run("new posture blocker", func(t *testing.T) {
		current := NetworkRouteConfiguration{Mode: netpolicy.ModeDirect}
		desired := NetworkRouteConfiguration{
			Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
			ProxySecretGeneration: 1, MediatedResolver: "1.1.1.1",
		}
		provider := newSuccessfulNetworkTransitionProvider(
			current,
			desired,
			0,
		)
		inventoryCalls := 0
		service := NetworkTransitionService{
			Provider: provider,
			Sessions: NetworkTransitionSessionInventoryFunc(
				func(context.Context, string) ([]NetworkTransitionSession, error) {
					inventoryCalls++
					if inventoryCalls == 1 {
						return []NetworkTransitionSession{}, nil
					}
					return []NetworkTransitionSession{{
						ID: "ses_new", Status: NetworkSessionLive,
						Phase: "running",
					}}, nil
				},
			),
		}
		plan, err := service.Plan(
			context.Background(),
			NetworkTransitionDraft{
				EnvironmentID: "env_transition",
				Desired:       desired,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Apply(
			context.Background(),
			plan,
			NetworkCandidateMaterial{
				UpstreamProxyURL: "socks5://127.0.0.1:7890",
			},
		)
		if !errors.Is(err, ErrNetworkTransitionBlocked) ||
			len(result.Blockers) != 1 ||
			result.Blockers[0].Owner != "ses_new" ||
			containsNetworkTransitionEvent(provider.events, "stage") {
			t.Fatalf("rechecked blocker result=%+v events=%v err=%v", result, provider.events, err)
		}
	})
}

func containsNetworkTransitionEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func networkTransitionRouteFixture() (
	NetworkRouteConfiguration,
	NetworkRouteConfiguration,
) {
	current := NetworkRouteConfiguration{
		Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
		ProxySecretGeneration: 1, MediatedResolver: "1.1.1.1",
	}
	desired := current
	desired.ProxySecretGeneration = 2
	return current, desired
}

func fixedNetworkTransitionSessions(
	sessions ...NetworkTransitionSession,
) NetworkTransitionSessionInventory {
	return NetworkTransitionSessionInventoryFunc(
		func(context.Context, string) ([]NetworkTransitionSession, error) {
			return append([]NetworkTransitionSession(nil), sessions...), nil
		},
	)
}

type networkTransitionProviderFixture struct {
	observed            []NetworkRouteConfiguration
	current             NetworkRouteConfiguration
	desired             NetworkRouteConfiguration
	events              []string
	failEvent           string
	failure             error
	rollbackFailure     error
	retained            uint64
	candidateGeneration uint64
	rollbackGeneration  uint64
	proofGeneration     map[string]uint64
	activated           bool
	missingStageHandle  bool
}

func newSuccessfulNetworkTransitionProvider(
	current NetworkRouteConfiguration,
	desired NetworkRouteConfiguration,
	retained uint64,
) *networkTransitionProviderFixture {
	return &networkTransitionProviderFixture{
		observed:            []NetworkRouteConfiguration{current, current},
		current:             current,
		desired:             desired,
		retained:            retained,
		candidateGeneration: 7,
		rollbackGeneration:  8,
		proofGeneration:     make(map[string]uint64),
	}
}

func (provider *networkTransitionProviderFixture) ObserveNetworkRoute(
	context.Context,
	string,
) (NetworkRouteConfiguration, error) {
	provider.events = append(provider.events, "observe")
	if len(provider.observed) == 0 {
		return NetworkRouteConfiguration{}, errors.New("observation exhausted")
	}
	value := provider.observed[0]
	provider.observed = provider.observed[1:]
	return value, nil
}

func (provider *networkTransitionProviderFixture) StageNetworkCandidate(
	_ context.Context,
	plan NetworkTransitionPlan,
	material NetworkCandidateMaterial,
) (NetworkStagedCandidate, NetworkTransitionProof, error) {
	provider.events = append(provider.events, "stage")
	if provider.failEvent == "stage" {
		return nil, NetworkTransitionProof{}, provider.failure
	}
	if plan.Desired != provider.desired {
		return nil, NetworkTransitionProof{}, errors.New("wrong desired route")
	}
	if plan.Desired.Mode == netpolicy.ModeTun2Socks &&
		material.UpstreamProxyURL == "" {
		return nil, NetworkTransitionProof{}, errors.New("missing material")
	}
	if provider.missingStageHandle {
		return nil, provider.proof("stage", 0), nil
	}
	return provider, provider.proof("stage", 0), nil
}

func (provider *networkTransitionProviderFixture) ProbeNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	provider.events = append(provider.events, "probe")
	if provider.failEvent == "probe" {
		return NetworkTransitionProof{}, provider.failure
	}
	return provider.proof("probe", 0), nil
}

func (provider *networkTransitionProviderFixture) ActivateNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	provider.events = append(provider.events, "activate")
	if provider.failEvent == "activate" {
		return NetworkTransitionProof{}, provider.failure
	}
	provider.activated = true
	return provider.proof("activate", provider.retained), nil
}

func (provider *networkTransitionProviderFixture) ProveNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	provider.events = append(provider.events, "prove")
	if provider.failEvent == "prove" {
		return NetworkTransitionProof{}, provider.failure
	}
	return provider.proof("prove", provider.retained), nil
}

func (provider *networkTransitionProviderFixture) DrainPreviousConnections(
	context.Context,
) (NetworkTransitionProof, error) {
	provider.events = append(provider.events, "drain")
	if provider.failEvent == "drain" {
		return NetworkTransitionProof{}, provider.failure
	}
	return provider.proof("drain", provider.retained), nil
}

func (provider *networkTransitionProviderFixture) CommitNetworkCandidate(
	context.Context,
) error {
	provider.events = append(provider.events, "commit")
	if provider.failEvent == "commit" {
		return provider.failure
	}
	return nil
}

func (provider *networkTransitionProviderFixture) RollbackNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	provider.events = append(provider.events, "rollback")
	if provider.rollbackFailure != nil {
		return NetworkTransitionProof{}, provider.rollbackFailure
	}
	proof := provider.proof("rollback", provider.retained)
	if provider.activated && provider.rollbackGeneration != 0 {
		proof.RouteGeneration = provider.rollbackGeneration
	} else {
		proof.RouteGeneration = provider.candidateGeneration - 1
	}
	proof.SecretGeneration = provider.current.ProxySecretGeneration
	return proof, nil
}

func (provider *networkTransitionProviderFixture) proof(
	phase string,
	retained uint64,
) NetworkTransitionProof {
	generation := provider.candidateGeneration
	if override := provider.proofGeneration[phase]; override != 0 {
		generation = override
	}
	proof := NetworkTransitionProof{
		RouteGeneration:     generation,
		SecretGeneration:    provider.desired.ProxySecretGeneration,
		ConnectionsRetained: retained,
		ObservedAt: time.Date(
			2026, 7, 29, 22, 0, 0, 0, time.UTC,
		),
	}
	if provider.current.Mode == netpolicy.ModeTun2Socks &&
		provider.desired.Mode == netpolicy.ModeTun2Socks &&
		provider.current.ProxySecretRef ==
			provider.desired.ProxySecretRef &&
		provider.current.ProxySecretGeneration ==
			provider.desired.ProxySecretGeneration &&
		provider.current.MediatedResolver !=
			provider.desired.MediatedResolver {
		proof.MediatedResolver =
			provider.desired.MediatedResolver
		if phase == "stage" ||
			phase == "probe" ||
			phase == "rollback" {
			proof.MediatedResolver =
				provider.current.MediatedResolver
		}
		proof.RuntimeBootID = liveDNSBootID
	}
	return proof
}

var _ NetworkTransitionProvider = (*networkTransitionProviderFixture)(nil)
var _ NetworkStagedCandidate = (*networkTransitionProviderFixture)(nil)
