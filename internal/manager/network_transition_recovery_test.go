package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

func TestNetworkTransitionRecoveryCommitsExactEnvelopeWithoutReplay(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkrecover1",
		plan,
	)
	proofs := networkRecoveryProofs(plan, 7, len(plan.Effects))
	checkpointNetworkRecoveryEffects(
		t,
		store,
		operation.ID,
		plan,
		proofs,
		len(plan.Effects)-1,
	)
	if _, err := store.Transition(
		operation.ID,
		OperationProving,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var request NetworkTransitionRecoveryRequest
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			_ context.Context,
			value NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			calls++
			request = value
			return committedNetworkRecoveryObservation(
				operation.ID,
				plan,
				proofs,
			), nil
		}),
	}

	recovered, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != OperationSucceeded ||
		!allOperationEffectsProved(recovered) ||
		calls != 1 ||
		request.OperationID != operation.ID ||
		request.PlanDigest != plan.PlanDigest ||
		request.EnvironmentID != plan.EnvironmentID {
		t.Fatalf(
			"exact committed recovery failed: operation=%+v calls=%d request=%+v",
			recovered,
			calls,
			request,
		)
	}
	replayed, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Phase != OperationSucceeded || calls != 1 {
		t.Fatalf(
			"terminal recovery replay called provider: operation=%+v calls=%d",
			replayed,
			calls,
		)
	}
}

func TestNetworkTransitionRecoveryReconcileOperationUsesRetainedPlan(
	t *testing.T,
) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	calls := 0
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			_ context.Context,
			request NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			calls++
			proofs := networkRecoveryProofs(
				plan,
				17,
				len(plan.Effects),
			)
			return committedNetworkRecoveryObservation(
				request.OperationID,
				plan,
				proofs,
			), nil
		}),
	}
	operation, err := service.Reserve(
		"op_networkretained1",
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	proofs := networkRecoveryProofs(plan, 17, len(plan.Effects))
	checkpointNetworkRecoveryEffects(
		t,
		store,
		operation.ID,
		plan,
		proofs,
		len(plan.Effects)-1,
	)

	recovered, err := service.ReconcileOperation(
		context.Background(),
		operation.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != OperationSucceeded || calls != 1 {
		t.Fatalf(
			"retained-plan recovery failed: operation=%+v calls=%d",
			recovered,
			calls,
		)
	}
	if _, err := os.Stat(service.planPath(operation.ID)); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("terminal network plan was retained: %v", err)
	}
	replayed, err := service.ReconcileOperation(
		context.Background(),
		operation.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Phase != OperationSucceeded || calls != 1 {
		t.Fatalf(
			"terminal retained-plan replay called provider: operation=%+v calls=%d",
			replayed,
			calls,
		)
	}
}

func TestNetworkTransitionRecoveryMissingPlanFailsClosed(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkmissing1",
		plan,
	)
	if _, err := store.Transition(
		operation.ID,
		OperationClaimed,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			context.Context,
			NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			calls++
			return NetworkTransitionRecoveryObservation{}, nil
		}),
	}

	recovered, err := service.ReconcileOperation(
		context.Background(),
		operation.ID,
	)
	if !errors.Is(err, ErrNetworkTransitionRecoveryRequired) {
		t.Fatalf("missing-plan recovery error=%v", err)
	}
	if recovered.Phase != OperationRecoveryRequired ||
		recovered.Recovery.Code != "network-plan-record-unavailable" ||
		calls != 0 {
		t.Fatalf(
			"missing plan did not fail closed: operation=%+v calls=%d",
			recovered,
			calls,
		)
	}
}

func TestNetworkTransitionRecoveryRejectsProviderAheadOfEnvelope(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkrecover2",
		plan,
	)
	proofs := networkRecoveryProofs(plan, 9, len(plan.Effects))
	checkpointNetworkRecoveryEffects(
		t,
		store,
		operation.ID,
		plan,
		proofs,
		0,
	)
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			context.Context,
			NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			return committedNetworkRecoveryObservation(
				operation.ID,
				plan,
				proofs,
			), nil
		}),
	}

	recovered, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if !errors.Is(err, ErrNetworkTransitionRecoveryRequired) {
		t.Fatalf("envelope gap error=%v", err)
	}
	if recovered.Phase != OperationRecoveryRequired ||
		recovered.Recovery.Code != "network-operation-envelope-gap" ||
		recovered.Effects[0].Status != EffectSucceeded ||
		recovered.Effects[1].Status != EffectPending {
		t.Fatalf("provider-ahead state was falsely committed: %+v", recovered)
	}
}

func TestNetworkTransitionRecoveryRequiresExactBindingGenerationAndProbe(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*NetworkTransitionRecoveryObservation)
	}{
		{
			name: "operation binding",
			mutate: func(observation *NetworkTransitionRecoveryObservation) {
				observation.OperationID = "op_unrelatedroute1"
			},
		},
		{
			name: "plan binding",
			mutate: func(observation *NetworkTransitionRecoveryObservation) {
				observation.PlanDigest = strings.Repeat("a", 64)
			},
		},
		{
			name: "secret generation",
			mutate: func(observation *NetworkTransitionRecoveryObservation) {
				observation.Effects[1].Proof.SecretGeneration++
			},
		},
		{
			name: "candidate route generation",
			mutate: func(observation *NetworkTransitionRecoveryObservation) {
				observation.Effects[2].Proof.RouteGeneration++
			},
		},
		{
			name: "proof ordering",
			mutate: func(observation *NetworkTransitionRecoveryObservation) {
				observation.Effects[2].Proof.ObservedAt =
					observation.Effects[1].Proof.ObservedAt.Add(-time.Second)
			},
		},
		{
			name: "probe evidence",
			mutate: func(observation *NetworkTransitionRecoveryObservation) {
				observation.Effects = append(
					observation.Effects[:1],
					observation.Effects[2:]...,
				)
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := OperationStore{Root: t.TempDir()}
			plan := networkRecoveryPlanFixture(t)
			operation := reserveNetworkRecoveryOperation(
				t,
				store,
				"op_networkbinding"+string(rune('a'+index)),
				plan,
			)
			proofs := networkRecoveryProofs(
				plan,
				uint64(20+index),
				len(plan.Effects),
			)
			checkpointNetworkRecoveryEffects(
				t,
				store,
				operation.ID,
				plan,
				proofs,
				len(plan.Effects)-1,
			)
			observation := committedNetworkRecoveryObservation(
				operation.ID,
				plan,
				proofs,
			)
			test.mutate(&observation)
			service := NetworkTransitionRecoveryService{
				Store: store,
				Provider: NetworkTransitionRecoveryProviderFunc(func(
					context.Context,
					NetworkTransitionRecoveryRequest,
				) (NetworkTransitionRecoveryObservation, error) {
					return observation, nil
				}),
			}

			recovered, err := service.Reconcile(
				context.Background(),
				operation.ID,
				plan,
			)
			if !errors.Is(err, ErrNetworkTransitionRecoveryRequired) {
				t.Fatalf("invalid evidence error=%v", err)
			}
			if recovered.Phase != OperationRecoveryRequired ||
				recovered.Recovery.Code !=
					"network-transition-evidence-unproved" ||
				recovered.Terminal() {
				t.Fatalf("invalid evidence became success: %+v", recovered)
			}
		})
	}
}

func TestNetworkTransitionRecoveryRejectsDurableEvidenceMismatch(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkevidence1",
		plan,
	)
	proofs := networkRecoveryProofs(plan, 27, len(plan.Effects))
	checkpointNetworkRecoveryEffects(
		t,
		store,
		operation.ID,
		plan,
		proofs,
		len(plan.Effects)-1,
	)
	observed := append(
		[]NetworkTransitionRecoveryEffect(nil),
		proofs...,
	)
	observed[0].Proof.ConnectionsRetained++
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			context.Context,
			NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			return committedNetworkRecoveryObservation(
				operation.ID,
				plan,
				observed,
			), nil
		}),
	}

	recovered, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if !errors.Is(err, ErrNetworkTransitionRecoveryRequired) {
		t.Fatalf("durable evidence mismatch error=%v", err)
	}
	if recovered.Phase != OperationRecoveryRequired ||
		recovered.Recovery.Code != "network-operation-envelope-mismatch" ||
		recovered.Terminal() {
		t.Fatalf("durable evidence mismatch became success: %+v", recovered)
	}
}

func TestNetworkTransitionRecoveryCommitsProvedRollback(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkrollback1",
		plan,
	)
	proofs := networkRecoveryProofs(plan, 31, 4)
	checkpointNetworkRecoveryEffects(
		t,
		store,
		operation.ID,
		plan,
		proofs,
		3,
	)
	if _, err := store.Transition(
		operation.ID,
		OperationProving,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	rollbackProof := NetworkTransitionProof{
		RouteGeneration:  32,
		SecretGeneration: plan.From.ProxySecretGeneration,
		ObservedAt:       networkRecoveryTime(),
	}
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			context.Context,
			NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			return NetworkTransitionRecoveryObservation{
				OperationID: operation.ID, PlanDigest: plan.PlanDigest,
				EnvironmentID: plan.EnvironmentID,
				Outcome:       NetworkRecoveryRolledBack, Effective: plan.From,
				CandidateRouteGeneration: 31,
				ActiveRouteGeneration:    32,
				Effects:                  proofs,
				RollbackProof:            &rollbackProof,
			}, nil
		}),
	}

	recovered, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != OperationRolledBack ||
		recovered.Result == nil ||
		recovered.Result.Code != "network-route-restored" {
		t.Fatalf("proved rollback did not become terminal: %+v", recovered)
	}
	for index := 0; index < 4; index++ {
		if recovered.Effects[index].Status != EffectRolledBack ||
			len(recovered.Effects[index].Evidence) == 0 {
			t.Fatalf("rollback effect %d=%+v", index, recovered.Effects[index])
		}
	}
	if recovered.Effects[4].Status != EffectPending {
		t.Fatalf("unexecuted drain was changed: %+v", recovered.Effects[4])
	}
}

func TestNetworkTransitionRecoveryFinishesCheckpointedRollbackAfterResponseLoss(
	t *testing.T,
) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkrollback2",
		plan,
	)
	proofs := networkRecoveryProofs(plan, 41, 4)
	checkpointNetworkRecoveryEffects(
		t,
		store,
		operation.ID,
		plan,
		proofs,
		3,
	)
	if _, err := store.Transition(
		operation.ID,
		OperationProving,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(
		operation.ID,
		OperationRollingBack,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	rollbackProof := NetworkTransitionProof{
		RouteGeneration:  42,
		SecretGeneration: plan.From.ProxySecretGeneration,
		ObservedAt:       networkRecoveryTime().Add(time.Second),
	}
	rollbackEvidence := networkTransitionEvidence(
		"network-route-restored",
		plan.EnvironmentID,
		rollbackProof,
	)
	for index := range proofs {
		effect := plan.Effects[index]
		if _, err := store.FinishEffect(
			operation.ID,
			effect.ID,
			effect.Provider,
			EffectRolledBack,
			rollbackEvidence,
		); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			context.Context,
			NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			calls++
			return NetworkTransitionRecoveryObservation{
				OperationID:              operation.ID,
				PlanDigest:               plan.PlanDigest,
				EnvironmentID:            plan.EnvironmentID,
				Outcome:                  NetworkRecoveryRolledBack,
				Effective:                plan.From,
				CandidateRouteGeneration: 41,
				ActiveRouteGeneration:    42,
				Effects:                  proofs,
				RollbackProof:            &rollbackProof,
			}, nil
		}),
	}

	recovered, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != OperationRolledBack || calls != 1 {
		t.Fatalf(
			"checkpointed rollback was not terminalized exactly once: operation=%+v calls=%d",
			recovered,
			calls,
		)
	}
	replayed, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Phase != OperationRolledBack || calls != 1 {
		t.Fatalf(
			"terminal rollback replay called provider: operation=%+v calls=%d",
			replayed,
			calls,
		)
	}
}

func TestNetworkTransitionRecoveryPersistsStableProviderFailure(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	plan := networkRecoveryPlanFixture(t)
	operation := reserveNetworkRecoveryOperation(
		t,
		store,
		"op_networkprovider1",
		plan,
	)
	if _, err := store.Transition(
		operation.ID,
		OperationClaimed,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	service := NetworkTransitionRecoveryService{
		Store: store,
		Provider: NetworkTransitionRecoveryProviderFunc(func(
			context.Context,
			NetworkTransitionRecoveryRequest,
		) (NetworkTransitionRecoveryObservation, error) {
			return NetworkTransitionRecoveryObservation{},
				errors.New("socks5://operator:raw-secret@127.0.0.1:7890")
		}),
	}

	recovered, err := service.Reconcile(
		context.Background(),
		operation.ID,
		plan,
	)
	if !errors.Is(err, ErrNetworkTransitionRecoveryRequired) {
		t.Fatalf("provider failure error=%v", err)
	}
	if recovered.Phase != OperationRecoveryRequired ||
		recovered.Recovery.Code !=
			"network-provider-observation-unavailable" {
		t.Fatalf("provider failure recovery=%+v", recovered)
	}
	data, marshalErr := json.Marshal(recovered)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, forbidden := range []string{
		"raw-secret",
		"operator:",
		"socks5://",
	} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("provider failure leaked %q: %s", forbidden, data)
		}
	}
}

func networkRecoveryPlanFixture(t *testing.T) NetworkTransitionPlan {
	t.Helper()
	from := NetworkRouteConfiguration{
		Mode: netpolicy.ModeTun2Socks, ProxySecretRef: "local-proxy",
		ProxySecretGeneration: 1, MediatedResolver: "1.1.1.1",
	}
	desired := from
	desired.ProxySecretGeneration = 2
	plan := NetworkTransitionPlan{
		Schema: NetworkTransitionPlanSchema, EnvironmentID: "env_recovery",
		Kind: NetworkTransitionRoute, From: from, Desired: desired,
		Effects:  plannedNetworkTransitionEffects(NetworkTransitionRoute),
		Blockers: []Blocker{},
		Rollback: plannedNetworkTransitionRollback(),
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func reserveNetworkRecoveryOperation(
	t *testing.T,
	store OperationStore,
	id string,
	plan NetworkTransitionPlan,
) Operation {
	t.Helper()
	operation, created, err := store.Reserve(
		OperationBinding{
			ID: id, Kind: networkTransitionOperationKind,
			Owner: OperationOwner{
				Kind: "environment", ID: plan.EnvironmentID,
			},
			PlanDigest:   plan.PlanDigest,
			BaseRevision: plan.From.ProxySecretGeneration,
		},
		operationEffectsForNetworkTransition(plan),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatalf("network operation %s was not created", id)
	}
	return operation
}

func checkpointNetworkRecoveryEffects(
	t *testing.T,
	store OperationStore,
	operationID string,
	plan NetworkTransitionPlan,
	proofs []NetworkTransitionRecoveryEffect,
	runningIndex int,
) {
	t.Helper()
	for _, phase := range []string{OperationClaimed, OperationStaging} {
		if _, err := store.Transition(operationID, phase, nil); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index <= runningIndex; index++ {
		effect := plan.Effects[index]
		if _, execute, err := store.BeginEffect(
			operationID,
			effect.ID,
			effect.Provider,
		); err != nil || !execute {
			t.Fatalf(
				"begin effect %s execute=%t err=%v",
				effect.ID,
				execute,
				err,
			)
		}
		if index == runningIndex {
			continue
		}
		if _, err := store.FinishEffect(
			operationID,
			effect.ID,
			effect.Provider,
			EffectSucceeded,
			networkTransitionEvidence(
				networkRecoveryEvidenceCode(effect.ID),
				plan.EnvironmentID,
				proofs[index].Proof,
			),
		); err != nil {
			t.Fatal(err)
		}
	}
}

func networkRecoveryProofs(
	plan NetworkTransitionPlan,
	generation uint64,
	count int,
) []NetworkTransitionRecoveryEffect {
	proofs := make([]NetworkTransitionRecoveryEffect, count)
	for index := range proofs {
		proofs[index] = NetworkTransitionRecoveryEffect{
			ID: plan.Effects[index].ID,
			Proof: NetworkTransitionProof{
				RouteGeneration:     generation,
				SecretGeneration:    plan.Desired.ProxySecretGeneration,
				ConnectionsRetained: uint64(index),
				ObservedAt:          networkRecoveryTime(),
			},
		}
	}
	return proofs
}

func committedNetworkRecoveryObservation(
	operationID string,
	plan NetworkTransitionPlan,
	proofs []NetworkTransitionRecoveryEffect,
) NetworkTransitionRecoveryObservation {
	return NetworkTransitionRecoveryObservation{
		OperationID: operationID, PlanDigest: plan.PlanDigest,
		EnvironmentID: plan.EnvironmentID,
		Outcome:       NetworkRecoveryCommitted, Effective: plan.Desired,
		CandidateRouteGeneration: proofs[0].Proof.RouteGeneration,
		ActiveRouteGeneration:    proofs[0].Proof.RouteGeneration,
		Effects:                  proofs,
	}
}

func networkRecoveryTime() time.Time {
	return time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
}
