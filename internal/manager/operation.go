package manager

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	OperationSchema = "hideout.operation.v1"

	OperationPlanned          = "planned"
	OperationClaimed          = "claimed"
	OperationStaging          = "staging"
	OperationActivating       = "activating"
	OperationProving          = "proving"
	OperationRollingBack      = "rolling-back"
	OperationSucceeded        = "succeeded"
	OperationFailed           = "failed"
	OperationCancelled        = "cancelled"
	OperationRolledBack       = "rolled-back"
	OperationRollbackUnproved = "rollback-unproved"
	OperationRecoveryRequired = "recovery-required"

	EffectPending    = "pending"
	EffectRunning    = "running"
	EffectSucceeded  = "succeeded"
	EffectFailed     = "failed"
	EffectRolledBack = "rolled-back"
	EffectUnproved   = "unproved"

	maxOperationEffects  = 256
	maxOperationEvidence = 256
)

var (
	ErrOperationMismatch         = errors.New("operation identity is already bound to a different request")
	ErrOperationProviderMismatch = errors.New("operation effect is owned by a different provider")
	ErrOperationTerminalUnproved = errors.New("operation terminal state is not proved by durable effect evidence")
	ErrInvalidOperation          = errors.New("operation is invalid")
	ErrInvalidTransition         = errors.New("operation phase transition is invalid")
	operationIDPattern           = regexp.MustCompile(`^op_[A-Za-z0-9_-]{8,124}$`)
	operationCodePattern         = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	evidenceCodePattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

type OperationBinding struct {
	ID           string
	Kind         string
	Owner        OperationOwner
	PlanDigest   string
	BaseRevision uint64
}

type OperationOwner struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Operation struct {
	Schema       string           `json:"schema"`
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Owner        OperationOwner   `json:"owner"`
	PlanDigest   string           `json:"planDigest"`
	BaseRevision uint64           `json:"baseRevision,omitempty"`
	Phase        string           `json:"phase"`
	Effects      []EffectResult   `json:"effects"`
	Result       *OperationResult `json:"result,omitempty"`
	Recovery     Recovery         `json:"recovery"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

type EffectResult struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	Provider string        `json:"provider"`
	Status   string        `json:"phase"`
	Evidence []EvidenceRef `json:"evidence,omitempty"`
}

type EvidenceRef struct {
	Code       string    `json:"code"`
	Ref        string    `json:"value,omitempty"`
	ObservedAt time.Time `json:"observedAt,omitempty"`
}

type OperationResult struct {
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type Recovery struct {
	Code       string `json:"code"`
	Summary    string `json:"summary"`
	NextAction string `json:"nextAction,omitempty"`
}

func (binding OperationBinding) Validate() error {
	if !operationIDPattern.MatchString(binding.ID) || !operationCodePattern.MatchString(binding.Kind) {
		return ErrInvalidOperation
	}
	if err := binding.Owner.Validate(); err != nil {
		return err
	}
	if !profileDigestPattern.MatchString(binding.PlanDigest) {
		return ErrInvalidOperation
	}
	return nil
}

func (owner OperationOwner) Validate() error {
	switch owner.Kind {
	case "profile", "environment", "session", "secret":
	default:
		return ErrInvalidOperation
	}
	if len(owner.ID) == 0 || len(owner.ID) > 128 || strings.TrimSpace(owner.ID) != owner.ID || strings.IndexByte(owner.ID, 0) >= 0 {
		return ErrInvalidOperation
	}
	return nil
}

func (operation Operation) Binding() OperationBinding {
	return OperationBinding{
		ID:           operation.ID,
		Kind:         operation.Kind,
		Owner:        operation.Owner,
		PlanDigest:   operation.PlanDigest,
		BaseRevision: operation.BaseRevision,
	}
}

func (operation Operation) Matches(binding OperationBinding) bool {
	return operation.ID == binding.ID &&
		operation.Kind == binding.Kind &&
		operation.Owner == binding.Owner &&
		operation.PlanDigest == binding.PlanDigest &&
		operation.BaseRevision == binding.BaseRevision
}

func (operation Operation) Terminal() bool {
	return isTerminalOperationPhase(operation.Phase)
}

func (operation Operation) Validate() error {
	if operation.Schema != OperationSchema {
		return fmt.Errorf("%w: unsupported schema %q", ErrInvalidOperation, operation.Schema)
	}
	if err := operation.Binding().Validate(); err != nil {
		return err
	}
	if !validOperationPhase(operation.Phase) || len(operation.Effects) > maxOperationEffects {
		return ErrInvalidOperation
	}
	seen := make(map[string]struct{}, len(operation.Effects))
	for _, effect := range operation.Effects {
		if err := effect.Validate(); err != nil {
			return err
		}
		if _, exists := seen[effect.ID]; exists {
			return fmt.Errorf("%w: duplicate effect %q", ErrInvalidOperation, effect.ID)
		}
		seen[effect.ID] = struct{}{}
	}
	if err := operation.Recovery.Validate(); err != nil {
		return err
	}
	if operation.CreatedAt.IsZero() || operation.UpdatedAt.IsZero() || operation.UpdatedAt.Before(operation.CreatedAt) {
		return ErrInvalidOperation
	}
	if operation.Terminal() {
		if operation.Result == nil || !operationResultMatchesPhase(*operation.Result, operation.Phase) {
			return ErrInvalidOperation
		}
	} else if operation.Result != nil {
		return ErrInvalidOperation
	}
	return nil
}

func (effect EffectResult) Validate() error {
	if len(effect.ID) == 0 || len(effect.ID) > 128 ||
		len(effect.Provider) == 0 || len(effect.Provider) > 128 ||
		strings.TrimSpace(effect.ID) != effect.ID ||
		strings.TrimSpace(effect.Provider) != effect.Provider {
		return ErrInvalidOperation
	}
	switch effect.Kind {
	case "persist", "stage", "activate", "drain", "restart", "cleanup", "prove":
	default:
		return ErrInvalidOperation
	}
	switch effect.Status {
	case EffectPending, EffectRunning, EffectSucceeded, EffectFailed, EffectRolledBack, EffectUnproved:
	default:
		return ErrInvalidOperation
	}
	if len(effect.Evidence) > maxOperationEvidence {
		return ErrInvalidOperation
	}
	for _, evidence := range effect.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (evidence EvidenceRef) Validate() error {
	if !evidenceCodePattern.MatchString(evidence.Code) || len(evidence.Ref) > 1024 || strings.IndexByte(evidence.Ref, 0) >= 0 {
		return ErrInvalidOperation
	}
	return nil
}

func (recovery Recovery) Validate() error {
	if !evidenceCodePattern.MatchString(recovery.Code) ||
		len(recovery.Summary) == 0 || len(recovery.Summary) > 2048 ||
		len(recovery.NextAction) > 1024 ||
		strings.IndexByte(recovery.Summary, 0) >= 0 ||
		strings.IndexByte(recovery.NextAction, 0) >= 0 {
		return ErrInvalidOperation
	}
	return nil
}

func validOperationPhase(phase string) bool {
	switch phase {
	case OperationPlanned, OperationClaimed, OperationStaging,
		OperationActivating, OperationProving, OperationRollingBack,
		OperationSucceeded, OperationFailed, OperationCancelled,
		OperationRolledBack, OperationRollbackUnproved,
		OperationRecoveryRequired:
		return true
	default:
		return false
	}
}

func isTerminalOperationPhase(phase string) bool {
	switch phase {
	case OperationSucceeded, OperationFailed, OperationCancelled,
		OperationRolledBack, OperationRollbackUnproved:
		return true
	default:
		return false
	}
}

func operationResultMatchesPhase(result OperationResult, phase string) bool {
	if len(result.Summary) > 2048 || len(result.Code) > 128 {
		return false
	}
	switch phase {
	case OperationSucceeded:
		return result.Status == OperationSucceeded
	case OperationFailed:
		return result.Status == OperationFailed
	case OperationCancelled:
		return result.Status == OperationCancelled
	case OperationRolledBack:
		return result.Status == OperationRolledBack
	case OperationRollbackUnproved:
		return result.Status == "unproved"
	default:
		return false
	}
}

func operationTransitionAllowed(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case OperationPlanned:
		return to == OperationClaimed || to == OperationCancelled
	case OperationClaimed:
		return to == OperationStaging || to == OperationActivating ||
			to == OperationFailed || to == OperationCancelled ||
			to == OperationRecoveryRequired
	case OperationStaging:
		return to == OperationActivating || to == OperationProving ||
			to == OperationSucceeded || to == OperationRollingBack ||
			to == OperationFailed || to == OperationRecoveryRequired
	case OperationActivating:
		return to == OperationProving || to == OperationSucceeded ||
			to == OperationRollingBack || to == OperationFailed ||
			to == OperationRecoveryRequired
	case OperationProving:
		return to == OperationSucceeded || to == OperationRollingBack ||
			to == OperationFailed || to == OperationRecoveryRequired
	case OperationRollingBack:
		return to == OperationRolledBack || to == OperationRollbackUnproved ||
			to == OperationRecoveryRequired
	case OperationRecoveryRequired:
		return to == OperationProving ||
			to == OperationRollingBack || to == OperationFailed ||
			to == OperationCancelled
	default:
		return false
	}
}
