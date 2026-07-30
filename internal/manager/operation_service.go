package manager

import "fmt"

// OperationService is the narrow owner of durable phase/effect transitions.
// Providers may execute an effect only when BeginEffect returns execute=true.
// A running effect is reconciliation work after response loss or restart and
// must never be invoked blindly a second time.
type OperationService struct {
	Store    OperationStore
	Observer EventObserver
}

func (service OperationService) Transition(
	id, phase string,
) (Operation, error) {
	return service.Store.Transition(id, phase, nil)
}

func (service OperationService) BeginEffect(
	id, effectID, provider string,
) (Operation, bool, error) {
	return service.Store.BeginEffect(id, effectID, provider)
}

func (service OperationService) FinishEffect(
	id, effectID, provider, status string,
	evidence []EvidenceRef,
) (Operation, error) {
	return service.Store.FinishEffect(
		id,
		effectID,
		provider,
		status,
		evidence,
	)
}

func (service OperationService) AppendEffectEvidence(
	id, effectID, provider string,
	evidence EvidenceRef,
) (Operation, error) {
	return service.Store.AppendEffectEvidence(
		id,
		effectID,
		provider,
		evidence,
	)
}

func (service OperationService) Terminal(
	id, phase, code, summary string,
) (Operation, error) {
	current, err := service.Store.Load(id)
	if err != nil {
		return Operation{}, err
	}
	if !current.Terminal() {
		if err := validateOperationTerminalEvidence(
			current,
			phase,
		); err != nil {
			return Operation{}, err
		}
	}
	operation, changed, err := service.Store.TransitionIfChanged(
		id,
		phase,
		&OperationResult{
			Status:  operationTerminalResultStatus(phase),
			Code:    code,
			Summary: summary,
		},
	)
	if err != nil {
		return Operation{}, err
	}
	if changed && service.Observer != nil {
		service.Observer.OperationEvent(
			"operation",
			phase,
			map[string]any{
				"id": operation.ID, "kind": operation.Kind,
				"ownerKind": operation.Owner.Kind,
				"ownerId":   operation.Owner.ID,
				"code":      code,
			},
		)
	}
	return operation, nil
}

func operationTerminalResultStatus(phase string) string {
	if phase == OperationRollbackUnproved {
		return "unproved"
	}
	return phase
}

func (service OperationService) SetRecovery(
	id, code, summary, nextAction string,
) (Operation, error) {
	return service.updateRecovery(
		id,
		Recovery{
			Code: code, Summary: summary, NextAction: nextAction,
		},
		false,
	)
}

func (service OperationService) RequireRecovery(
	id, code, summary, nextAction string,
) (Operation, error) {
	return service.updateRecovery(
		id,
		Recovery{
			Code: code, Summary: summary, NextAction: nextAction,
		},
		true,
	)
}

func (service OperationService) updateRecovery(
	id string,
	recovery Recovery,
	require bool,
) (Operation, error) {
	var (
		operation Operation
		changed   bool
		err       error
	)
	if require {
		operation, changed, err = service.Store.RequireRecovery(id, recovery)
	} else {
		operation, changed, err = service.Store.SetRecovery(id, recovery)
	}
	if err != nil {
		return Operation{}, err
	}
	if changed && service.Observer != nil {
		service.Observer.OperationEvent(
			"operation",
			operation.Phase,
			map[string]any{
				"id": operation.ID, "kind": operation.Kind,
				"ownerKind": operation.Owner.Kind,
				"ownerId":   operation.Owner.ID,
				"code":      recovery.Code,
			},
		)
	}
	return operation, nil
}

func validateOperationTerminalEvidence(
	operation Operation,
	phase string,
) error {
	switch phase {
	case OperationCancelled:
		return nil
	case OperationSucceeded:
		if len(operation.Effects) == 0 {
			return fmt.Errorf(
				"%w: succeeded operation has no effects",
				ErrOperationTerminalUnproved,
			)
		}
		for _, effect := range operation.Effects {
			if effect.Status != EffectSucceeded ||
				len(effect.Evidence) == 0 {
				return fmt.Errorf(
					"%w: effect %s is %s",
					ErrOperationTerminalUnproved,
					effect.ID,
					effect.Status,
				)
			}
		}
		return nil
	case OperationFailed:
		return requireOperationEffectEvidence(
			operation,
			EffectFailed,
		)
	case OperationRolledBack:
		return requireOperationEffectEvidence(
			operation,
			EffectRolledBack,
		)
	case OperationRollbackUnproved:
		return requireOperationEffectEvidence(
			operation,
			EffectUnproved,
		)
	default:
		return ErrInvalidTransition
	}
}

func requireOperationEffectEvidence(
	operation Operation,
	status string,
) error {
	for _, effect := range operation.Effects {
		if effect.Status == status && len(effect.Evidence) != 0 {
			return nil
		}
	}
	return fmt.Errorf(
		"%w: no %s effect has durable evidence",
		ErrOperationTerminalUnproved,
		status,
	)
}
