package manager

import (
	"context"
	"testing"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

func TestProfileNetworkObserverRunsAfterDurableEffectCheckpoints(
	t *testing.T,
) {
	from := NetworkRouteConfiguration{
		Mode:                  netpolicy.ModeTun2Socks,
		ProxySecretRef:        "local-proxy",
		ProxySecretGeneration: 2,
		MediatedResolver:      "1.1.1.1",
	}
	desired := from
	desired.MediatedResolver = "9.9.9.9"
	plan := NetworkTransitionPlan{
		Schema:        NetworkTransitionPlanSchema,
		EnvironmentID: "env_checkpoint",
		Kind:          NetworkTransitionDNS,
		From:          from,
		Desired:       desired,
		Effects:       plannedNetworkTransitionEffects(NetworkTransitionDNS),
		Rollback:      plannedNetworkTransitionRollback(),
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	store := OperationStore{Root: t.TempDir()}
	const operationID = "op_checkpointobserver1"
	_, created, err := store.Reserve(
		OperationBinding{
			ID: operationID, Kind: profileTransactionOperationKind,
			Owner:        OperationOwner{Kind: "profile", ID: "default"},
			PlanDigest:   plan.PlanDigest,
			BaseRevision: 1,
		},
		operationEffectsForConfigurationPlan(ConfigurationPlan{
			Effects: profileNetworkPlannedEffects(
				[]NetworkTransitionPlan{plan},
			),
		}),
	)
	if err != nil || !created {
		t.Fatalf("reserve created=%t err=%v", created, err)
	}
	operations := OperationService{Store: store}
	for _, phase := range []string{
		OperationClaimed,
		OperationStaging,
		OperationProving,
	} {
		if _, err := operations.Transition(operationID, phase); err != nil {
			t.Fatal(err)
		}
	}
	observer := &durableNetworkCheckpointObserver{
		store:       store,
		operationID: operationID,
	}
	checkpoint := &profileNetworkOperationCheckpoint{
		operationID: operationID,
		operations:  operations,
		next:        observer,
	}
	stage := plan.Effects[0]
	if err := checkpoint.BeforeNetworkTransitionEffect(
		context.Background(),
		plan,
		stage,
	); err != nil {
		t.Fatal(err)
	}
	evidence := []EvidenceRef{{
		Code: "network-candidate-staged",
		Ref:  "environment:" + plan.EnvironmentID,
	}}
	if err := checkpoint.AfterNetworkTransitionEffect(
		context.Background(),
		plan,
		EffectResult{
			ID: stage.ID, Kind: stage.Kind,
			Provider: stage.Provider,
			Status:   EffectSucceeded,
			Evidence: evidence,
		},
	); err != nil {
		t.Fatal(err)
	}
	if observer.err != nil {
		t.Fatal(observer.err)
	}
	if observer.beforePhase != EffectRunning ||
		observer.afterPhase != EffectSucceeded ||
		len(observer.afterEvidence) != 1 ||
		observer.afterEvidence[0] != evidence[0] {
		t.Fatalf("observer checkpoints=%+v", observer)
	}
}

type durableNetworkCheckpointObserver struct {
	store         OperationStore
	operationID   string
	beforePhase   string
	afterPhase    string
	afterEvidence []EvidenceRef
	err           error
}

func (observer *durableNetworkCheckpointObserver) BeforeNetworkTransitionEffect(
	_ context.Context,
	plan NetworkTransitionPlan,
	effect PlannedEffect,
) error {
	operation, err := observer.store.Load(observer.operationID)
	if err != nil {
		observer.err = err
		return err
	}
	result, ok := checkpointOperationEffect(
		operation,
		profileNetworkEffectID(plan.EnvironmentID, effect.ID),
	)
	if !ok {
		observer.err = ErrInvalidOperation
		return observer.err
	}
	observer.beforePhase = result.Status
	return nil
}

func (observer *durableNetworkCheckpointObserver) AfterNetworkTransitionEffect(
	_ context.Context,
	plan NetworkTransitionPlan,
	effect EffectResult,
) error {
	operation, err := observer.store.Load(observer.operationID)
	if err != nil {
		observer.err = err
		return err
	}
	result, ok := checkpointOperationEffect(
		operation,
		profileNetworkEffectID(plan.EnvironmentID, effect.ID),
	)
	if !ok {
		observer.err = ErrInvalidOperation
		return observer.err
	}
	observer.afterPhase = result.Status
	observer.afterEvidence = append(
		[]EvidenceRef(nil),
		result.Evidence...,
	)
	return nil
}

func checkpointOperationEffect(
	operation Operation,
	id string,
) (EffectResult, bool) {
	for _, effect := range operation.Effects {
		if effect.ID == id {
			return effect, true
		}
	}
	return EffectResult{}, false
}

var _ NetworkTransitionEffectCheckpoint = (*durableNetworkCheckpointObserver)(nil)
