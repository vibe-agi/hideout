package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	networkTransitionOperationKind = "network.transition"

	NetworkRecoveryCommitted  = "committed"
	NetworkRecoveryRolledBack = "rolled-back"
	NetworkRecoveryIncomplete = "incomplete"
	NetworkRecoveryUnproved   = "unproved"
)

var ErrNetworkTransitionRecoveryRequired = errors.New(
	"network transition requires reconciliation",
)

var ErrNetworkTransitionConfirmationRequired = errors.New(
	"network transition confirmation is required",
)

// NetworkTransitionRecoveryRequest is a non-secret provider query. The
// operation envelope lets a provider distinguish this exact reviewed
// transition from an unrelated route that happens to have the same shape.
type NetworkTransitionRecoveryRequest struct {
	OperationID   string
	PlanDigest    string
	EnvironmentID string
	From          NetworkRouteConfiguration
	Desired       NetworkRouteConfiguration
}

type NetworkTransitionRecoveryEffect struct {
	ID    string
	Proof NetworkTransitionProof
}

// NetworkTransitionRecoveryObservation is a closed, non-secret provider
// observation. Effects must be an ordered prefix of the reviewed plan.
type NetworkTransitionRecoveryObservation struct {
	OperationID              string
	PlanDigest               string
	EnvironmentID            string
	Outcome                  string
	Effective                NetworkRouteConfiguration
	CandidateRouteGeneration uint64
	ActiveRouteGeneration    uint64
	Effects                  []NetworkTransitionRecoveryEffect
	RollbackProof            *NetworkTransitionProof
}

type NetworkTransitionRecoveryProvider interface {
	ReconcileNetworkTransition(
		context.Context,
		NetworkTransitionRecoveryRequest,
	) (NetworkTransitionRecoveryObservation, error)
}

type NetworkTransitionRecoveryCapability interface {
	SupportsNetworkTransitionRecoveryKind(string) bool
}

type NetworkTransitionRecoveryProviderFunc func(
	context.Context,
	NetworkTransitionRecoveryRequest,
) (NetworkTransitionRecoveryObservation, error)

func (provider NetworkTransitionRecoveryProviderFunc) ReconcileNetworkTransition(
	ctx context.Context,
	request NetworkTransitionRecoveryRequest,
) (NetworkTransitionRecoveryObservation, error) {
	if provider == nil {
		return NetworkTransitionRecoveryObservation{},
			ErrNetworkTransitionProviderUnavailable
	}
	return provider(ctx, request)
}

// NetworkTransitionRecoveryService reconciles provider evidence into an
// existing durable operation. It has no stage/activate capability, so restart
// recovery cannot accidentally replay a network mutation.
type NetworkTransitionRecoveryService struct {
	Store      OperationStore
	Operations OperationService
	Provider   NetworkTransitionRecoveryProvider
}

func (service NetworkTransitionRecoveryService) Reconcile(
	ctx context.Context,
	operationID string,
	plan NetworkTransitionPlan,
) (Operation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	if err := plan.VerifyDigest(); err != nil {
		return Operation{}, err
	}
	operation, err := service.operationStore().Load(operationID)
	if err != nil {
		return Operation{}, err
	}
	if !networkOperationMatchesPlan(operation, plan) {
		return Operation{}, ErrOperationMismatch
	}
	if operation.Terminal() {
		return operation, nil
	}
	if operation.Phase == OperationPlanned {
		return operation, ErrNetworkTransitionConfirmationRequired
	}
	if service.Provider == nil {
		return service.requireRecovery(
			operation,
			"network-provider-unavailable",
			"The network provider is unavailable, so the accepted route transition cannot be proved.",
			"Repair the network provider, then retry the same operation ID.",
		)
	}
	if capability, ok := service.Provider.(NetworkTransitionRecoveryCapability); ok {
		kind, classifyErr := classifyNetworkTransition(
			plan.From,
			plan.Desired,
		)
		if classifyErr != nil ||
			!capability.SupportsNetworkTransitionRecoveryKind(kind) {
			return service.requireRecovery(
				operation,
				"network-provider-unavailable",
				"The network provider is unavailable for this transition kind, so the accepted route transition cannot be proved.",
				"Repair the network provider, then retry the same operation ID.",
			)
		}
	}

	observation, providerErr := service.Provider.ReconcileNetworkTransition(
		ctx,
		NetworkTransitionRecoveryRequest{
			OperationID: operation.ID, PlanDigest: plan.PlanDigest,
			EnvironmentID: plan.EnvironmentID,
			From:          plan.From, Desired: plan.Desired,
		},
	)
	if providerErr != nil {
		return service.requireRecovery(
			operation,
			"network-provider-observation-unavailable",
			"The network provider could not return authoritative recovery evidence.",
			"Repair the provider and retry the same operation ID.",
		)
	}
	if err := observation.validate(operation, plan); err != nil {
		return service.requireRecovery(
			operation,
			"network-transition-evidence-unproved",
			"The observed route does not prove this exact reviewed network transition.",
			"Stop new attaches, inspect the environment route, and retry recovery.",
		)
	}

	operation, err = service.applyObservedEffects(
		operation,
		plan,
		observation,
	)
	if err != nil {
		switch {
		case errors.Is(err, errNetworkOperationEnvelopeGap):
			return service.requireRecovery(
				operation,
				"network-operation-envelope-gap",
				"Provider evidence is ahead of the durable operation envelope.",
				"Inspect the route binding and recover this operation without replaying it.",
			)
		case errors.Is(err, errNetworkOperationEnvelopeMismatch):
			return service.requireRecovery(
				operation,
				"network-operation-envelope-mismatch",
				"The durable network effect evidence does not match the provider's exact route observation.",
				"Stop new attaches, inspect the operation evidence and route binding, then retry recovery.",
			)
		}
		return Operation{}, err
	}

	switch observation.Outcome {
	case NetworkRecoveryCommitted:
		if !allOperationEffectsProved(operation) {
			return service.requireRecovery(
				operation,
				"network-transition-proof-incomplete",
				"The candidate route is committed but one or more durable effect proofs are incomplete.",
				"Inspect provider evidence and retry recovery without replaying the transition.",
			)
		}
		operation, err = service.advanceToProving(operation)
		if err != nil {
			return Operation{}, err
		}
		return service.operations().Terminal(
			operation.ID,
			OperationSucceeded,
			"network-route-committed",
			"The reviewed route generation was proved and committed.",
		)
	case NetworkRecoveryRolledBack:
		return service.commitObservedRollback(operation, plan, observation)
	case NetworkRecoveryIncomplete:
		return service.requireRecovery(
			operation,
			"network-transition-incomplete",
			"The provider reports a partially completed network transition.",
			"Keep new attaches paused and complete explicit network recovery.",
		)
	default:
		return service.requireRecovery(
			operation,
			"network-transition-state-unproved",
			"The provider cannot prove whether the reviewed route transition completed.",
			"Stop new attaches, inspect the environment route, and recover explicitly.",
		)
	}
}

var errNetworkOperationEnvelopeGap = errors.New(
	"network provider evidence is ahead of the operation envelope",
)

var errNetworkOperationEnvelopeMismatch = errors.New(
	"network provider evidence does not match the operation envelope",
)

func (service NetworkTransitionRecoveryService) applyObservedEffects(
	operation Operation,
	plan NetworkTransitionPlan,
	observation NetworkTransitionRecoveryObservation,
) (Operation, error) {
	if err := validateNetworkOperationEffectEnvelope(
		operation,
		observation,
	); err != nil {
		return operation, err
	}
	operations := service.operations()
	for index, observed := range observation.Effects {
		effect := operation.Effects[index]
		evidence := networkTransitionEvidence(
			networkRecoveryEvidenceCode(observed.ID),
			plan.EnvironmentID,
			observed.Proof,
		)
		switch effect.Status {
		case EffectRunning:
			updated, err := operations.FinishEffect(
				operation.ID,
				effect.ID,
				effect.Provider,
				EffectSucceeded,
				evidence,
			)
			if err != nil {
				return operation, err
			}
			operation = updated
		case EffectSucceeded:
			switch {
			case len(effect.Evidence) == 0:
				updated, err := operations.FinishEffect(
					operation.ID,
					effect.ID,
					effect.Provider,
					EffectSucceeded,
					evidence,
				)
				if err != nil {
					return operation, err
				}
				operation = updated
			case !slices.Equal(effect.Evidence, evidence):
				return operation, errNetworkOperationEnvelopeMismatch
			}
		case EffectRolledBack:
			if observation.Outcome != NetworkRecoveryRolledBack ||
				observation.RollbackProof == nil ||
				!slices.Equal(
					effect.Evidence,
					networkTransitionEvidence(
						"network-route-restored",
						plan.EnvironmentID,
						*observation.RollbackProof,
					),
				) {
				return operation, errNetworkOperationEnvelopeMismatch
			}
		case EffectPending:
			return operation, errNetworkOperationEnvelopeGap
		default:
			return operation, errNetworkOperationEnvelopeMismatch
		}
	}
	return operation, nil
}

func validateNetworkOperationEffectEnvelope(
	operation Operation,
	observation NetworkTransitionRecoveryObservation,
) error {
	if observation.Outcome == NetworkRecoveryUnproved {
		return nil
	}
	envelopeBarrier := false
	for index, effect := range operation.Effects {
		if index >= len(observation.Effects) {
			if effect.Status != EffectPending {
				return errNetworkOperationEnvelopeMismatch
			}
			continue
		}
		switch effect.Status {
		case EffectPending:
			envelopeBarrier = true
		case EffectRunning:
			if envelopeBarrier {
				return errNetworkOperationEnvelopeMismatch
			}
			envelopeBarrier = true
		case EffectSucceeded:
			if envelopeBarrier {
				return errNetworkOperationEnvelopeMismatch
			}
		case EffectRolledBack:
			if envelopeBarrier ||
				observation.Outcome != NetworkRecoveryRolledBack {
				return errNetworkOperationEnvelopeMismatch
			}
		default:
			return errNetworkOperationEnvelopeMismatch
		}
	}
	return nil
}

func (service NetworkTransitionRecoveryService) commitObservedRollback(
	operation Operation,
	plan NetworkTransitionPlan,
	observation NetworkTransitionRecoveryObservation,
) (Operation, error) {
	if observation.RollbackProof == nil {
		return service.requireRecovery(
			operation,
			"network-rollback-unproved",
			"The provider did not supply stable prior-route restoration evidence.",
			"Stop new attaches and recover the environment route explicitly.",
		)
	}
	operations := service.operations()
	var err error
	switch operation.Phase {
	case OperationRollingBack:
	case OperationRecoveryRequired:
		operation, err = operations.Transition(
			operation.ID,
			OperationRollingBack,
		)
	default:
		operation, err = operations.Transition(
			operation.ID,
			OperationRollingBack,
		)
	}
	if err != nil {
		return Operation{}, err
	}
	rollbackEvidence := networkTransitionEvidence(
		"network-route-restored",
		plan.EnvironmentID,
		*observation.RollbackProof,
	)
	rolledBack := false
	for _, effect := range operation.Effects {
		switch effect.Status {
		case EffectRunning, EffectSucceeded:
			operation, err = operations.FinishEffect(
				operation.ID,
				effect.ID,
				effect.Provider,
				EffectRolledBack,
				rollbackEvidence,
			)
			if err != nil {
				return Operation{}, err
			}
			rolledBack = true
		case EffectRolledBack:
			if !slices.Equal(effect.Evidence, rollbackEvidence) {
				return service.requireRecovery(
					operation,
					"network-rollback-envelope-mismatch",
					"The durable rollback evidence does not match the provider's exact restored route.",
					"Stop new attaches, inspect the operation evidence and route binding, then retry recovery.",
				)
			}
			rolledBack = true
		}
	}
	if !rolledBack {
		return service.requireRecovery(
			operation,
			"network-rollback-envelope-unproved",
			"The prior route is visible, but no durable provider effect can be bound to its restoration.",
			"Inspect the operation envelope before choosing retry or cancellation.",
		)
	}
	return operations.Terminal(
		operation.ID,
		OperationRolledBack,
		"network-route-restored",
		"The prior route generation was restored and proved.",
	)
}

func (service NetworkTransitionRecoveryService) advanceToProving(
	operation Operation,
) (Operation, error) {
	operations := service.operations()
	var err error
	switch operation.Phase {
	case OperationClaimed:
		operation, err = operations.Transition(
			operation.ID,
			OperationStaging,
		)
		if err != nil {
			return Operation{}, err
		}
		fallthrough
	case OperationStaging, OperationActivating,
		OperationRecoveryRequired:
		operation, err = operations.Transition(
			operation.ID,
			OperationProving,
		)
	case OperationProving:
	default:
		err = ErrInvalidTransition
	}
	return operation, err
}

func (service NetworkTransitionRecoveryService) requireRecovery(
	operation Operation,
	code, summary, nextAction string,
) (Operation, error) {
	updated, err := service.operations().RequireRecovery(
		operation.ID,
		code,
		summary,
		nextAction,
	)
	if err != nil {
		return Operation{}, err
	}
	return updated, ErrNetworkTransitionRecoveryRequired
}

func (service NetworkTransitionRecoveryService) operationStore() OperationStore {
	store := service.Store
	if store.Root == "" {
		store = service.Operations.Store
	}
	return store
}

func (service NetworkTransitionRecoveryService) operations() OperationService {
	operations := service.Operations
	if operations.Store.Root == "" {
		operations.Store = service.Store
	}
	return operations
}

func networkOperationMatchesPlan(
	operation Operation,
	plan NetworkTransitionPlan,
) bool {
	if operation.ID == "" ||
		operation.Kind != networkTransitionOperationKind ||
		operation.Owner != (OperationOwner{
			Kind: "environment",
			ID:   plan.EnvironmentID,
		}) ||
		operation.PlanDigest != plan.PlanDigest ||
		operation.BaseRevision != plan.From.ProxySecretGeneration ||
		len(operation.Effects) != len(plan.Effects) {
		return false
	}
	for index, planned := range plan.Effects {
		effect := operation.Effects[index]
		if effect.ID != planned.ID ||
			effect.Kind != planned.Kind ||
			effect.Provider != planned.Provider {
			return false
		}
	}
	return true
}

func (observation NetworkTransitionRecoveryObservation) validate(
	operation Operation,
	plan NetworkTransitionPlan,
) error {
	if observation.OperationID != operation.ID ||
		observation.PlanDigest != plan.PlanDigest ||
		observation.EnvironmentID != plan.EnvironmentID ||
		len(observation.Effects) > len(plan.Effects) {
		return ErrInvalidNetworkTransition
	}
	if observation.Outcome == NetworkRecoveryUnproved {
		if len(observation.Effects) != 0 ||
			observation.RollbackProof != nil ||
			observation.Effective != (NetworkRouteConfiguration{}) ||
			observation.CandidateRouteGeneration != 0 ||
			observation.ActiveRouteGeneration != 0 {
			return ErrInvalidNetworkTransition
		}
		return nil
	}
	if observation.Effective.Validate() != nil ||
		observation.CandidateRouteGeneration == 0 {
		return ErrInvalidNetworkTransition
	}
	var lastObservedAt time.Time
	for index, effect := range observation.Effects {
		if effect.ID != plan.Effects[index].ID ||
			validateNetworkTransitionPhaseProof(
				plan,
				effect.ID,
				effect.Proof,
				observation.CandidateRouteGeneration,
			) != nil {
			return ErrInvalidNetworkTransition
		}
		if !lastObservedAt.IsZero() &&
			effect.Proof.ObservedAt.Before(lastObservedAt) {
			return ErrInvalidNetworkTransition
		}
		lastObservedAt = effect.Proof.ObservedAt
	}
	switch observation.Outcome {
	case NetworkRecoveryCommitted:
		if observation.Effective != plan.Desired ||
			observation.ActiveRouteGeneration !=
				observation.CandidateRouteGeneration ||
			len(observation.Effects) != len(plan.Effects) ||
			observation.RollbackProof != nil {
			return ErrInvalidNetworkTransition
		}
	case NetworkRecoveryRolledBack:
		if observation.Effective != plan.From ||
			observation.RollbackProof == nil ||
			validateNetworkTransitionRollbackProof(
				plan,
				*observation.RollbackProof,
			) != nil ||
			(!lastObservedAt.IsZero() &&
				observation.RollbackProof.ObservedAt.Before(lastObservedAt)) ||
			observation.ActiveRouteGeneration !=
				observation.RollbackProof.RouteGeneration ||
			plan.Kind != NetworkTransitionDNS &&
				observation.ActiveRouteGeneration ==
					observation.CandidateRouteGeneration {
			return ErrInvalidNetworkTransition
		}
		if plan.Kind != NetworkTransitionDNS &&
			networkRecoveryObservedActivation(observation) &&
			observation.ActiveRouteGeneration <=
				observation.CandidateRouteGeneration {
			return ErrInvalidNetworkTransition
		}
	case NetworkRecoveryIncomplete:
		if observation.RollbackProof != nil ||
			observation.ActiveRouteGeneration == 0 {
			return ErrInvalidNetworkTransition
		}
		if networkRecoveryObservedActivation(observation) {
			if observation.Effective != plan.Desired ||
				observation.ActiveRouteGeneration !=
					observation.CandidateRouteGeneration {
				return ErrInvalidNetworkTransition
			}
		} else if observation.Effective != plan.From ||
			plan.Kind != NetworkTransitionDNS &&
				observation.ActiveRouteGeneration ==
					observation.CandidateRouteGeneration {
			return ErrInvalidNetworkTransition
		}
	default:
		return ErrInvalidNetworkTransition
	}
	return nil
}

func networkRecoveryObservedActivation(
	observation NetworkTransitionRecoveryObservation,
) bool {
	for _, effect := range observation.Effects {
		if effect.ID == "network-activate" {
			return true
		}
	}
	return false
}

func networkRecoveryEvidenceCode(effectID string) string {
	switch effectID {
	case "network-stage":
		return "network-candidate-staged"
	case "network-probe":
		return "network-candidate-probed"
	case "network-activate":
		return "network-route-activated"
	case "network-prove":
		return "network-route-proved"
	case "network-drain":
		return "network-existing-connections-draining"
	default:
		panic(fmt.Sprintf("unknown network transition effect %q", effectID))
	}
}

func allOperationEffectsProved(operation Operation) bool {
	if len(operation.Effects) == 0 {
		return false
	}
	for _, effect := range operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			return false
		}
	}
	return true
}

func operationEffectsForNetworkTransition(
	plan NetworkTransitionPlan,
) []EffectResult {
	effects := make([]EffectResult, len(plan.Effects))
	for index, planned := range plan.Effects {
		effects[index] = EffectResult{
			ID: planned.ID, Kind: planned.Kind,
			Provider: planned.Provider,
			Status:   EffectPending,
		}
	}
	return effects
}
