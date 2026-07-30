package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vibe-agi/hideout/internal/secrets"
)

// NetworkAuthorityResetProof is supplied only after a new daemon has acquired
// the store's singleton lock. That lock proves the former daemon process (and
// therefore its in-memory gateway registry and listeners) no longer owns any
// live route. The proof contains no secret material.
type NetworkAuthorityResetProof struct {
	AuthorityID string
	ObservedAt  time.Time
}

func (proof NetworkAuthorityResetProof) Validate() error {
	if !stableAPIField(proof.AuthorityID) ||
		proof.ObservedAt.IsZero() {
		return ErrInvalidOperation
	}
	return nil
}

func secretLiveNetworkMutationStarted(
	operation Operation,
	plans []NetworkTransitionPlan,
	secretEffect PlannedEffect,
) bool {
	if effectStatus(operation, secretEffect.ID) != EffectPending {
		return true
	}
	for _, plan := range plans {
		for _, effect := range plan.Effects {
			if effectStatus(
				operation,
				profileNetworkEffectID(plan.EnvironmentID, effect.ID),
			) != EffectPending {
				return true
			}
		}
	}
	return false
}

func (service *SecretService) reconcileLiveSecretAfterNetworkAuthorityReset(
	ctx context.Context,
	record secretPlanRecord,
	operation Operation,
	secretEffect PlannedEffect,
	reset NetworkAuthorityResetProof,
) (SecretApplyResult, error) {
	if service == nil || service.Store == nil ||
		record.Plan.Action != secrets.ActionRotate ||
		len(record.NetworkTransitions) == 0 ||
		reset.Validate() != nil {
		return SecretApplyResult{Operation: operation},
			ErrSecretRecoveryRequired
	}

	reference, committed, operation, err :=
		service.reconcileSecretGenerationAfterNetworkAuthorityReset(
			ctx,
			record.Plan,
			operation,
			secretEffect,
		)
	if err != nil {
		return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		}, err
	}
	if committed {
		return service.finishCommittedSecretAfterNetworkAuthorityReset(
			record,
			operation,
			secretEffect,
			reference,
			reset,
		)
	}
	return service.finishUncommittedSecretAfterNetworkAuthorityReset(
		record,
		operation,
		secretEffect,
		reference,
		reset,
	)
}

func (service *SecretService) reconcileSecretGenerationAfterNetworkAuthorityReset(
	ctx context.Context,
	plan SecretPlan,
	operation Operation,
	effect PlannedEffect,
) (secrets.Reference, bool, Operation, error) {
	reference, err := service.Store.Reference(ctx, plan.Ref)
	if err != nil {
		return reference, false, operation, err
	}
	if reference.Validate() != nil ||
		reference.Ref != plan.Ref ||
		reference.Provider != plan.Current.Provider {
		result, recoveryErr := service.requireSecretRecovery(
			operation,
			"secret provider returned invalid or mismatched reference metadata",
		)
		return reference, false, result.Operation, recoveryErr
	}
	status := effectStatus(operation, effect.ID)
	switch status {
	case EffectPending:
		if !sameSecretPlanBase(plan.Current, reference) {
			result, recoveryErr := service.requireSecretRecovery(
				operation,
				"secret generation changed before its provider effect was durably started",
			)
			return reference, false, result.Operation, recoveryErr
		}
		return reference, false, operation, nil
	case EffectRunning:
		if service.Reconciler == nil {
			return reference, false, operation,
				ErrSecretRecoveryRequired
		}
		reconciled, reconcileErr := service.Reconciler.Reconcile(
			ctx,
			secrets.ReconcileRequest{
				Ref: plan.Ref, Action: plan.Action,
				OperationID:        plan.OperationID,
				ExpectedGeneration: plan.BaseGeneration,
			},
		)
		if reconcileErr != nil {
			return reference, false, operation, reconcileErr
		}
		switch {
		case reconciled.Committed && !reconciled.Uncommitted &&
			reconciled.Reference.Validate() == nil &&
			reconciled.Reference.Ref == plan.Ref &&
			reconciled.Reference.Provider ==
				plan.Current.Provider &&
			reconciled.Reference.Generation == plan.NextGeneration &&
			reconciled.Reference.Availability == plan.NextAvailability:
			operation, err = service.operations().FinishEffect(
				operation.ID,
				effect.ID,
				effect.Provider,
				EffectSucceeded,
				secretGenerationEvidence(reconciled.Reference),
			)
			return reconciled.Reference, true, operation, err
		case reconciled.Uncommitted && !reconciled.Committed &&
			reconciled.Reference.Validate() == nil &&
			sameSecretPlanBase(plan.Current, reconciled.Reference):
			operation, err = service.operations().FinishEffect(
				operation.ID,
				effect.ID,
				effect.Provider,
				EffectRolledBack,
				secretGenerationUnchangedEvidence(
					reconciled.Reference,
				),
			)
			return reconciled.Reference, false, operation, err
		default:
			result, recoveryErr := service.requireSecretRecovery(
				operation,
				"secret provider did not prove the reviewed operation committed or stayed unchanged",
			)
			return reference, false, result.Operation, recoveryErr
		}
	case EffectSucceeded:
		if reference.Generation != plan.NextGeneration ||
			reference.Availability != plan.NextAvailability {
			result, recoveryErr := service.requireSecretRecovery(
				operation,
				"durable secret evidence does not match the current reviewed generation",
			)
			return reference, false, result.Operation, recoveryErr
		}
		return reference, true, operation, nil
	case EffectRolledBack:
		if !sameSecretPlanBase(plan.Current, reference) {
			result, recoveryErr := service.requireSecretRecovery(
				operation,
				"rolled-back secret evidence does not match the reviewed base generation",
			)
			return reference, false, result.Operation, recoveryErr
		}
		return reference, false, operation, nil
	default:
		result, recoveryErr := service.requireSecretRecovery(
			operation,
			"secret provider effect has no exact restart-recovery classification",
		)
		return reference, false, result.Operation, recoveryErr
	}
}

func (service *SecretService) finishCommittedSecretAfterNetworkAuthorityReset(
	record secretPlanRecord,
	operation Operation,
	secretEffect PlannedEffect,
	reference secrets.Reference,
	reset NetworkAuthorityResetProof,
) (SecretApplyResult, error) {
	for _, plan := range record.NetworkTransitions {
		for _, planned := range plan.Effects {
			effect := operationEffect(
				operation,
				profileNetworkEffectID(plan.EnvironmentID, planned.ID),
			)
			if effect == nil ||
				effect.Status != EffectSucceeded ||
				!effectProvesPlannedRequirements(*effect, planned) {
				return service.requireSecretRecovery(
					operation,
					"the secret committed but a pre-commit live-route proof is incomplete",
				)
			}
		}
	}

	resetEvidence := networkAuthorityResetEvidence(reset)
	var err error
	operation, err = service.operations().AppendEffectEvidence(
		operation.ID,
		secretEffect.ID,
		secretEffect.Provider,
		resetEvidence,
	)
	if err != nil {
		return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		}, err
	}
	operation, err = advanceSecretRecoveryToProving(
		service.operations(),
		operation,
	)
	if err != nil {
		return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		}, err
	}
	operation, err = service.operations().Terminal(
		operation.ID,
		OperationSucceeded,
		"secret-generation-committed-network-authority-reset",
		"The reviewed secret generation committed; the prior daemon's live-route authority ended and the next eligible attach will bind the new generation.",
	)
	if err != nil {
		return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		}, err
	}
	result := SecretApplyResult{
		Operation: operation,
		Reference: reference,
	}
	_ = service.removePlanRecord(operation.ID)
	if service.hooks.afterTerminal != nil {
		if err := service.hooks.afterTerminal(operation); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (service *SecretService) finishUncommittedSecretAfterNetworkAuthorityReset(
	record secretPlanRecord,
	operation Operation,
	secretEffect PlannedEffect,
	reference secrets.Reference,
	reset NetworkAuthorityResetProof,
) (SecretApplyResult, error) {
	operations := service.operations()
	resetEvidence := networkAuthorityResetEvidence(reset)
	for _, plan := range record.NetworkTransitions {
		for _, planned := range plan.Effects {
			effectID := profileNetworkEffectID(
				plan.EnvironmentID,
				planned.ID,
			)
			effect := operationEffect(operation, effectID)
			if effect == nil {
				return SecretApplyResult{
					Operation: operation,
					Reference: reference,
				}, ErrOperationMismatch
			}
			switch effect.Status {
			case EffectPending:
				continue
			case EffectRunning, EffectSucceeded:
				evidence := append(
					append([]EvidenceRef(nil), effect.Evidence...),
					resetEvidence,
				)
				var err error
				operation, err = operations.FinishEffect(
					operation.ID,
					effect.ID,
					effect.Provider,
					EffectRolledBack,
					evidence,
				)
				if err != nil {
					return SecretApplyResult{
						Operation: operation,
						Reference: reference,
					}, err
				}
			case EffectRolledBack, EffectFailed, EffectUnproved:
				var err error
				operation, err = operations.AppendEffectEvidence(
					operation.ID,
					effect.ID,
					effect.Provider,
					resetEvidence,
				)
				if err != nil {
					return SecretApplyResult{
						Operation: operation,
						Reference: reference,
					}, err
				}
			default:
				return SecretApplyResult{
					Operation: operation,
					Reference: reference,
				}, ErrInvalidOperation
			}
		}
	}

	secretStatus := effectStatus(operation, secretEffect.ID)
	terminalPhase := OperationRolledBack
	code := "secret-generation-unchanged-network-authority-reset"
	summary := "The secret generation stayed unchanged; the prior daemon's staged live routes no longer exist."
	switch secretStatus {
	case EffectPending:
		var execute bool
		var err error
		operation, execute, err = operations.BeginEffect(
			operation.ID,
			secretEffect.ID,
			secretEffect.Provider,
		)
		if err != nil || !execute {
			return SecretApplyResult{
				Operation: operation,
				Reference: reference,
			}, errors.Join(err, ErrSecretRecoveryRequired)
		}
		operation, err = operations.FinishEffect(
			operation.ID,
			secretEffect.ID,
			secretEffect.Provider,
			EffectFailed,
			append(
				secretGenerationUnchangedEvidence(reference),
				resetEvidence,
			),
		)
		if err != nil {
			return SecretApplyResult{
				Operation: operation,
				Reference: reference,
			}, err
		}
		terminalPhase = OperationFailed
		code = "secret-live-transition-aborted-network-authority-reset"
		summary = "The secret generation stayed unchanged; daemon restart invalidated the staged live-route transaction before the secret provider was called."
	case EffectRolledBack:
		var err error
		operation, err = operations.AppendEffectEvidence(
			operation.ID,
			secretEffect.ID,
			secretEffect.Provider,
			resetEvidence,
		)
		if err != nil {
			return SecretApplyResult{
				Operation: operation,
				Reference: reference,
			}, err
		}
	default:
		return service.requireSecretRecovery(
			operation,
			fmt.Sprintf(
				"uncommitted secret effect has unsupported state %s after network authority reset",
				secretStatus,
			),
		)
	}

	var err error
	if terminalPhase == OperationRolledBack {
		if operation.Phase != OperationRollingBack {
			operation, err = operations.Transition(
				operation.ID,
				OperationRollingBack,
			)
		}
	}
	if err == nil {
		operation, err = operations.Terminal(
			operation.ID,
			terminalPhase,
			code,
			summary,
		)
	}
	if err != nil {
		return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		}, err
	}
	result := SecretApplyResult{
		Operation: operation,
		Reference: reference,
	}
	_ = service.removePlanRecord(operation.ID)
	if service.hooks.afterTerminal != nil {
		if err := service.hooks.afterTerminal(operation); err != nil {
			return result, err
		}
	}
	return result, nil
}

func effectProvesPlannedRequirements(
	effect EffectResult,
	planned PlannedEffect,
) bool {
	if len(planned.ProofRequired) == 0 {
		return false
	}
	for _, required := range planned.ProofRequired {
		found := false
		for _, evidence := range effect.Evidence {
			if evidence.Code == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func advanceSecretRecoveryToProving(
	operations OperationService,
	operation Operation,
) (Operation, error) {
	var err error
	switch operation.Phase {
	case OperationClaimed:
		operation, err = operations.Transition(
			operation.ID,
			OperationStaging,
		)
		if err != nil {
			return operation, err
		}
		fallthrough
	case OperationStaging, OperationActivating,
		OperationRecoveryRequired:
		return operations.Transition(
			operation.ID,
			OperationProving,
		)
	case OperationProving:
		return operation, nil
	default:
		return operation, ErrInvalidTransition
	}
}

func networkAuthorityResetEvidence(
	reset NetworkAuthorityResetProof,
) EvidenceRef {
	return EvidenceRef{
		Code:       "network-authority-reset",
		Ref:        "daemon:" + reset.AuthorityID,
		ObservedAt: reset.ObservedAt.Round(0).UTC(),
	}
}
