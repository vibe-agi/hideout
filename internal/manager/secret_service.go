package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	SecretDraftSchema = "hideout.secret-draft.v1"
	SecretPlanSchema  = "hideout.secret-plan.v1"
	SecretApplySchema = "hideout.secret-apply.v1"

	secretPlanRecordSchema    = "hideout.secret-plan-record.v1"
	secretPlanCanonicalDomain = "secret-plan"
	secretRecordDomain        = "secret-plan-record"
	defaultSecretPlanTTL      = 15 * time.Minute
	maxSecretPlanRecordBytes  = 512 << 10
)

var (
	ErrInvalidSecretDraft         = errors.New("secret draft is invalid")
	ErrInvalidSecretPlan          = errors.New("secret plan is invalid")
	ErrInvalidSecretApply         = errors.New("secret apply request is invalid")
	ErrStaleSecretPlan            = errors.New("secret plan is stale")
	ErrSecretPlanExpired          = errors.New("secret plan has expired")
	ErrSecretConfirmationRequired = errors.New(
		"secret apply requires confirmation",
	)
	ErrSecretValueRequired = errors.New(
		"secret value is required to execute this operation",
	)
	ErrSecretPlanBlocked = errors.New(
		"secret plan is blocked",
	)
	ErrSecretRecoveryRequired = errors.New(
		"secret operation requires reconciliation",
	)
	ErrSecretProviderUnavailable = errors.New(
		"secret operation provider is unavailable",
	)

	secretApplyLocksMu sync.Mutex
	secretApplyLocks   = map[string]*secretApplyLock{}

	secretMutationOwnersMu sync.Mutex
	secretMutationOwners   = map[string]string{}
)

type SecretDraft struct {
	Schema string `json:"schema"`
	Ref    string `json:"ref"`
	Action string `json:"action"`
}

type SecretPlan struct {
	Schema               string            `json:"schema"`
	OperationID          string            `json:"operationId"`
	PlanDigest           string            `json:"planDigest"`
	Ref                  string            `json:"ref"`
	Action               string            `json:"action"`
	BaseGeneration       uint64            `json:"baseGeneration"`
	Current              secrets.Reference `json:"current"`
	NextAvailability     string            `json:"nextAvailability"`
	NextGeneration       uint64            `json:"nextGeneration"`
	AffectedProfiles     []string          `json:"affectedProfiles"`
	AffectedEnvironments []string          `json:"affectedEnvironments"`
	Effects              []PlannedEffect   `json:"effects"`
	Blockers             []Blocker         `json:"blockers"`
	Warnings             []Warning         `json:"warnings"`
	Rollback             RollbackPlan      `json:"rollback"`
	ExpiresAt            time.Time         `json:"expiresAt"`

	networkTransitions []NetworkTransitionPlan
}

type SecretApplyRequest struct {
	Schema      string          `json:"schema"`
	OperationID string          `json:"operationId"`
	PlanDigest  string          `json:"planDigest"`
	Ref         string          `json:"ref"`
	Action      string          `json:"action"`
	Confirmed   bool            `json:"confirmed"`
	Value       *secrets.Buffer `json:"-"`
}

type SecretApplyResult struct {
	Operation Operation         `json:"operation"`
	Reference secrets.Reference `json:"reference"`
}

type SecretProvider interface {
	ListSecrets(context.Context, string) ([]secrets.Reference, error)
	PlanSecret(context.Context, SecretDraft) (SecretPlan, error)
	ApplySecret(context.Context, SecretApplyRequest) (SecretApplyResult, error)
}

type secretPlanRecord struct {
	Schema             string                  `json:"schema"`
	Plan               SecretPlan              `json:"plan"`
	NetworkTransitions []NetworkTransitionPlan `json:"networkTransitions"`
	RecordDigest       string                  `json:"recordDigest"`
}

type secretPlanRecordAuthority struct {
	Schema             string                  `json:"schema"`
	Plan               SecretPlan              `json:"plan"`
	NetworkTransitions []NetworkTransitionPlan `json:"networkTransitions"`
}

type secretServiceHooks struct {
	afterTerminal func(Operation) error
}

type SecretService struct {
	Core       Core
	Store      secrets.Store
	Reconciler secrets.OperationReconciler
	Operations OperationStore
	PlanTTL    time.Duration
	// NetworkTransitions is injected by the daemon that owns live gateway
	// authority. A daemon-less SecretService keeps the original Keychain-only,
	// next-attach behavior.
	NetworkTransitions *ProfileNetworkTransitionCoordinator

	now   func() time.Time
	hooks secretServiceHooks
}

type secretApplyLock struct {
	mutex sync.Mutex
	refs  int
}

func NewSecretService(
	core Core,
	store secrets.Store,
) *SecretService {
	service := &SecretService{
		Core:       core,
		Store:      store,
		Operations: OperationStore{Root: core.Store.Root},
		PlanTTL:    defaultSecretPlanTTL,
	}
	if reconciler, ok := store.(secrets.OperationReconciler); ok {
		service.Reconciler = reconciler
	}
	return service
}

func (draft SecretDraft) Validate() error {
	if draft.Schema != SecretDraftSchema ||
		secrets.ValidateRef(draft.Ref) != nil ||
		!validSecretAction(draft.Action) {
		return ErrInvalidSecretDraft
	}
	return nil
}

func (plan *SecretPlan) Seal() error {
	if plan == nil {
		return ErrInvalidSecretPlan
	}
	plan.PlanDigest = ""
	if err := plan.validate(false); err != nil {
		return err
	}
	digest, err := CanonicalDigest(secretPlanCanonicalDomain, *plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = digest
	return plan.Validate()
}

func (plan SecretPlan) Validate() error {
	return plan.validate(true)
}

func (plan SecretPlan) VerifyDigest() error {
	if err := plan.Validate(); err != nil {
		return err
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	expected, err := CanonicalDigest(secretPlanCanonicalDomain, plan)
	if err != nil {
		return err
	}
	if provided != expected {
		return fmt.Errorf("%w: digest mismatch", ErrInvalidSecretPlan)
	}
	return nil
}

func (plan SecretPlan) validate(requireDigest bool) error {
	if plan.Schema != SecretPlanSchema ||
		!operationIDPattern.MatchString(plan.OperationID) ||
		secrets.ValidateRef(plan.Ref) != nil ||
		!validSecretAction(plan.Action) ||
		plan.ExpiresAt.IsZero() ||
		len(plan.Effects) == 0 ||
		len(plan.Effects) > maxPlanReviewItems ||
		len(plan.Blockers) > maxPlanReviewItems ||
		len(plan.Warnings) > maxPlanReviewItems ||
		len(plan.AffectedProfiles) > maxPlanReviewItems ||
		len(plan.AffectedEnvironments) > maxPlanReviewItems ||
		plan.NextGeneration != plan.BaseGeneration+1 {
		return ErrInvalidSecretPlan
	}
	if requireDigest && !profileDigestPattern.MatchString(plan.PlanDigest) {
		return ErrInvalidSecretPlan
	}
	if !requireDigest && plan.PlanDigest != "" {
		return ErrInvalidSecretPlan
	}
	if err := plan.Current.Validate(); err != nil ||
		plan.Current.Ref != plan.Ref ||
		plan.Current.Generation != plan.BaseGeneration {
		return ErrInvalidSecretPlan
	}
	switch plan.Action {
	case secrets.ActionSet, secrets.ActionRotate:
		if plan.NextAvailability != secrets.AvailabilityAvailable {
			return ErrInvalidSecretPlan
		}
	case secrets.ActionDelete:
		if plan.NextAvailability != secrets.AvailabilityMissing {
			return ErrInvalidSecretPlan
		}
	}
	if !sortedUniqueStableNames(plan.AffectedProfiles) ||
		!sortedUniqueStableNames(plan.AffectedEnvironments) {
		return ErrInvalidSecretPlan
	}
	seenEffects := make(map[string]struct{}, len(plan.Effects))
	for _, effect := range plan.Effects {
		if err := effect.Validate(); err != nil {
			return ErrInvalidSecretPlan
		}
		if _, exists := seenEffects[effect.ID]; exists {
			return ErrInvalidSecretPlan
		}
		seenEffects[effect.ID] = struct{}{}
	}
	for _, blocker := range plan.Blockers {
		if err := blocker.Validate(); err != nil {
			return ErrInvalidSecretPlan
		}
	}
	for _, warning := range plan.Warnings {
		if err := warning.Validate(); err != nil {
			return ErrInvalidSecretPlan
		}
	}
	if err := plan.Rollback.Validate(); err != nil {
		return ErrInvalidSecretPlan
	}
	return nil
}

func (request SecretApplyRequest) Validate() error {
	if request.Schema != SecretApplySchema ||
		!operationIDPattern.MatchString(request.OperationID) ||
		!profileDigestPattern.MatchString(request.PlanDigest) ||
		secrets.ValidateRef(request.Ref) != nil ||
		!validSecretAction(request.Action) {
		return ErrInvalidSecretApply
	}
	return nil
}

func (service *SecretService) ListSecrets(
	ctx context.Context,
	ref string,
) ([]secrets.Reference, error) {
	if err := checkSecretServiceContext(ctx); err != nil {
		return nil, err
	}
	if service == nil || service.Store == nil {
		return nil, ErrSecretProviderUnavailable
	}
	if ref != "" {
		if err := secrets.ValidateRef(ref); err != nil {
			return nil, err
		}
		reference, err := service.Store.Reference(ctx, ref)
		if err != nil {
			return nil, err
		}
		if err := reference.Validate(); err != nil {
			return nil, err
		}
		return []secrets.Reference{reference}, nil
	}
	references, err := service.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(references) > maxKeychainListReferences {
		return nil, errors.New("secret reference list exceeds bound")
	}
	references = append([]secrets.Reference(nil), references...)
	for _, reference := range references {
		if err := reference.Validate(); err != nil {
			return nil, err
		}
	}
	secrets.SortReferences(references)
	if references == nil {
		references = []secrets.Reference{}
	}
	return references, nil
}

const maxKeychainListReferences = 4096

func (service *SecretService) PlanSecret(
	ctx context.Context,
	draft SecretDraft,
) (SecretPlan, error) {
	return service.Plan(ctx, draft)
}

func (service *SecretService) Plan(
	ctx context.Context,
	draft SecretDraft,
) (SecretPlan, error) {
	if err := draft.Validate(); err != nil {
		return SecretPlan{}, err
	}
	if err := checkSecretServiceContext(ctx); err != nil {
		return SecretPlan{}, err
	}
	if service == nil || service.Store == nil {
		return SecretPlan{}, ErrSecretProviderUnavailable
	}
	current, err := service.Store.Reference(ctx, draft.Ref)
	if err != nil {
		return SecretPlan{}, err
	}
	if err := current.Validate(); err != nil ||
		current.Ref != draft.Ref ||
		current.Provider != service.Store.Provider() ||
		current.Generation == math.MaxUint64 {
		return SecretPlan{}, ErrInvalidSecretPlan
	}
	operationID, err := NewOperationID()
	if err != nil {
		return SecretPlan{}, err
	}
	plan, err := service.buildPlan(
		ctx,
		operationID,
		draft,
		current,
		service.nowUTC().Add(service.planTTL()),
	)
	if err != nil {
		return SecretPlan{}, err
	}
	record, err := newSecretPlanRecord(plan)
	if err != nil {
		return SecretPlan{}, err
	}
	if err := service.writePlanRecord(record); err != nil {
		return SecretPlan{}, err
	}
	_, created, err := service.operationStore().Reserve(
		operationBindingForSecretPlan(plan),
		operationEffectsForSecretPlan(plan),
	)
	if err != nil || !created {
		_ = service.removePlanRecord(operationID)
		if err != nil {
			return SecretPlan{}, err
		}
		return SecretPlan{}, ErrOperationMismatch
	}
	return plan, nil
}

func (service *SecretService) ApplySecret(
	ctx context.Context,
	request SecretApplyRequest,
) (SecretApplyResult, error) {
	return service.Apply(ctx, request)
}

func (service *SecretService) Apply(
	ctx context.Context,
	request SecretApplyRequest,
) (SecretApplyResult, error) {
	if request.Value != nil {
		defer request.Value.Clear()
	}
	if err := request.Validate(); err != nil {
		return SecretApplyResult{}, err
	}
	if service == nil || service.Store == nil {
		return SecretApplyResult{}, ErrSecretProviderUnavailable
	}
	key := service.Core.Store.Root + "\x00secret\x00" + request.OperationID
	release := acquireSecretApplyLock(key)
	defer release()
	return service.applyLocked(ctx, request)
}

func (service *SecretService) applyLocked(
	ctx context.Context,
	request SecretApplyRequest,
) (SecretApplyResult, error) {
	if err := checkSecretServiceContext(ctx); err != nil {
		return SecretApplyResult{}, err
	}
	operation, err := service.operationStore().Load(request.OperationID)
	if err != nil {
		return SecretApplyResult{}, err
	}
	binding := OperationBinding{
		ID:           request.OperationID,
		Kind:         "secret." + request.Action,
		Owner:        OperationOwner{Kind: "secret", ID: request.Ref},
		PlanDigest:   request.PlanDigest,
		BaseRevision: operation.BaseRevision,
	}
	if !operation.Matches(binding) {
		return SecretApplyResult{}, ErrOperationMismatch
	}
	if operation.Terminal() {
		reference, referenceErr := service.Store.Reference(ctx, request.Ref)
		return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		}, referenceErr
	}
	record, err := service.loadPlanRecord(request.OperationID)
	if err != nil {
		return SecretApplyResult{}, err
	}
	if request.Ref != record.Plan.Ref ||
		request.Action != record.Plan.Action ||
		request.PlanDigest != record.Plan.PlanDigest ||
		!operation.Matches(operationBindingForSecretPlan(record.Plan)) {
		return SecretApplyResult{}, ErrOperationMismatch
	}
	if !request.Confirmed {
		return SecretApplyResult{}, ErrSecretConfirmationRequired
	}
	if operation.Phase == OperationPlanned &&
		!service.nowUTC().Before(record.Plan.ExpiresAt) {
		service.cancelPlannedOperation(
			record.Plan.OperationID,
			"secret-plan-expired",
			"The reviewed secret plan expired before it was applied.",
		)
		_ = service.removePlanRecord(record.Plan.OperationID)
		return SecretApplyResult{}, ErrSecretPlanExpired
	}
	release, err := acquireSecretMutation(
		service.Core.Store.Root,
		record.Plan.Ref,
		record.Plan.OperationID,
	)
	if err != nil {
		return SecretApplyResult{}, err
	}
	defer release()
	return service.applyUnderMutationLock(ctx, request, record)
}

// ReconcileOperation inspects an already accepted secret operation without
// requiring the client to resend secret material. Deletes and provider commits
// can complete from durable intent; an uncommitted set/rotate remains
// retryable and explicitly asks for the value again.
func (service *SecretService) ReconcileOperation(
	ctx context.Context,
	operationID string,
) (SecretApplyResult, error) {
	return service.reconcileOperation(ctx, operationID, nil)
}

// ReconcileOperationAfterNetworkAuthorityReset is the startup-only recovery
// path for a daemon that has acquired the store's singleton lock. The proof
// means every in-process gateway owned by the prior daemon is gone; it does not
// claim that a live route survived the restart.
func (service *SecretService) ReconcileOperationAfterNetworkAuthorityReset(
	ctx context.Context,
	operationID string,
	reset NetworkAuthorityResetProof,
) (SecretApplyResult, error) {
	if err := reset.Validate(); err != nil {
		return SecretApplyResult{}, err
	}
	return service.reconcileOperation(ctx, operationID, &reset)
}

func (service *SecretService) reconcileOperation(
	ctx context.Context,
	operationID string,
	reset *NetworkAuthorityResetProof,
) (SecretApplyResult, error) {
	if service == nil || service.Store == nil {
		return SecretApplyResult{}, ErrSecretProviderUnavailable
	}
	if err := checkSecretServiceContext(ctx); err != nil {
		return SecretApplyResult{}, err
	}
	key := service.Core.Store.Root + "\x00secret\x00" + operationID
	releaseApply := acquireSecretApplyLock(key)
	defer releaseApply()

	operation, err := service.operationStore().Load(operationID)
	if err != nil {
		return SecretApplyResult{}, err
	}
	if operation.Owner.Kind != "secret" ||
		!strings.HasPrefix(operation.Kind, "secret.") {
		return SecretApplyResult{}, ErrOperationMismatch
	}
	if operation.Terminal() {
		reference, referenceErr := service.Store.Reference(
			ctx,
			operation.Owner.ID,
		)
		return SecretApplyResult{
			Operation: operation, Reference: reference,
		}, referenceErr
	}
	if operation.Phase == OperationPlanned {
		return SecretApplyResult{Operation: operation},
			ErrSecretConfirmationRequired
	}
	record, err := service.loadPlanRecord(operationID)
	if err != nil {
		return SecretApplyResult{Operation: operation}, err
	}
	if !operation.Matches(operationBindingForSecretPlan(record.Plan)) {
		return SecretApplyResult{Operation: operation},
			ErrOperationMismatch
	}
	effect, err := secretProviderEffect(
		record.Plan,
		service.Store.Provider(),
	)
	if err != nil {
		return SecretApplyResult{Operation: operation}, err
	}
	if reset != nil && len(record.NetworkTransitions) != 0 &&
		secretLiveNetworkMutationStarted(
			operation,
			record.NetworkTransitions,
			effect,
		) {
		releaseMutation, err := acquireSecretMutation(
			service.Core.Store.Root,
			record.Plan.Ref,
			record.Plan.OperationID,
		)
		if err != nil {
			return SecretApplyResult{Operation: operation}, err
		}
		defer releaseMutation()
		return service.reconcileLiveSecretAfterNetworkAuthorityReset(
			ctx,
			record,
			operation,
			effect,
			*reset,
		)
	}
	if record.Plan.Action != secrets.ActionDelete &&
		effectStatus(operation, effect.ID) == EffectPending {
		operation, err = service.operations().SetRecovery(
			operation.ID,
			"secret-value-required",
			"The accepted secret operation has not called the provider and needs the value again.",
			"Retry the same operation ID with the secret value.",
		)
		return SecretApplyResult{Operation: operation},
			errors.Join(ErrSecretValueRequired, err)
	}
	releaseMutation, err := acquireSecretMutation(
		service.Core.Store.Root,
		record.Plan.Ref,
		record.Plan.OperationID,
	)
	if err != nil {
		return SecretApplyResult{Operation: operation}, err
	}
	defer releaseMutation()

	result, reconcileErr := service.applyUnderMutationLock(
		ctx,
		SecretApplyRequest{
			Schema: SecretApplySchema, OperationID: record.Plan.OperationID,
			PlanDigest: record.Plan.PlanDigest, Ref: record.Plan.Ref,
			Action: record.Plan.Action, Confirmed: true,
		},
		record,
	)
	if errors.Is(reconcileErr, ErrSecretValueRequired) {
		recovered, recoveryErr := service.operations().SetRecovery(
			operation.ID,
			"secret-value-required",
			"The provider proves no commit for this accepted secret operation; the value is needed again.",
			"Retry the same operation ID with the secret value.",
		)
		if recoveryErr == nil {
			result.Operation = recovered
		}
		reconcileErr = errors.Join(reconcileErr, recoveryErr)
	}
	return result, reconcileErr
}

func (service *SecretService) applyUnderMutationLock(
	ctx context.Context,
	request SecretApplyRequest,
	record secretPlanRecord,
) (SecretApplyResult, error) {
	operation, err := service.operationStore().Load(record.Plan.OperationID)
	if err != nil {
		return SecretApplyResult{}, err
	}
	current, err := service.Store.Reference(ctx, record.Plan.Ref)
	if err != nil {
		return SecretApplyResult{}, err
	}
	if operation.Phase == OperationPlanned {
		if !sameSecretPlanBase(record.Plan.Current, current) {
			service.cancelSecretPlan(
				operation.ID,
				"stale-secret-plan",
				"The secret generation or availability changed after review.",
			)
			return SecretApplyResult{}, ErrStaleSecretPlan
		}
		replanned, replanErr := service.buildPlan(
			ctx,
			record.Plan.OperationID,
			SecretDraft{
				Schema: SecretDraftSchema,
				Ref:    record.Plan.Ref,
				Action: record.Plan.Action,
			},
			current,
			record.Plan.ExpiresAt,
		)
		if replanErr != nil ||
			replanned.PlanDigest != record.Plan.PlanDigest ||
			!sameSecretNetworkTransitionPlans(
				replanned.networkTransitions,
				record.NetworkTransitions,
			) {
			service.cancelSecretPlan(
				operation.ID,
				"stale-secret-plan",
				"The authoritative secret plan changed after review.",
			)
			if replanErr != nil {
				return SecretApplyResult{}, replanErr
			}
			return SecretApplyResult{}, ErrStaleSecretPlan
		}
		if len(record.Plan.Blockers) != 0 {
			service.cancelSecretPlan(
				operation.ID,
				"secret-plan-blocked",
				"The reviewed secret plan has active blockers.",
			)
			return SecretApplyResult{}, ErrSecretPlanBlocked
		}
		if record.Plan.Action != secrets.ActionDelete &&
			request.Value == nil {
			return SecretApplyResult{}, ErrSecretValueRequired
		}
	}
	operation, err = service.ensureStaging(operation)
	if err != nil {
		return SecretApplyResult{}, err
	}
	effect, err := secretProviderEffect(record.Plan, service.Store.Provider())
	if err != nil {
		return SecretApplyResult{}, err
	}
	if record.Plan.Action != secrets.ActionDelete &&
		request.Value == nil &&
		effectStatus(operation, effect.ID) == EffectPending {
		return SecretApplyResult{Operation: operation},
			ErrSecretValueRequired
	}
	if len(record.NetworkTransitions) != 0 {
		return service.applyWithLiveNetworkTransitions(
			ctx,
			request,
			record,
			operation,
			effect,
		)
	}
	operations := service.operations()
	operation, execute, err := operations.BeginEffect(
		operation.ID,
		effect.ID,
		effect.Provider,
	)
	if err != nil {
		return SecretApplyResult{}, err
	}
	var reference secrets.Reference
	switch {
	case execute:
		reference, err = service.executeProvider(
			ctx,
			request.Value,
			record.Plan,
		)
		if err != nil {
			return SecretApplyResult{Operation: operation}, err
		}
		operation, err = operations.FinishEffect(
			operation.ID,
			effect.ID,
			effect.Provider,
			EffectSucceeded,
			secretGenerationEvidence(reference),
		)
		if err != nil {
			return SecretApplyResult{}, err
		}
	case effectStatus(operation, effect.ID) == EffectRunning:
		reference, operation, err = service.reconcileRunningEffect(
			ctx,
			request.Value,
			record.Plan,
			operation,
			effect,
		)
		if err != nil {
			return SecretApplyResult{Operation: operation}, err
		}
	case effectStatus(operation, effect.ID) == EffectSucceeded:
		reference, err = service.Store.Reference(ctx, record.Plan.Ref)
		if err != nil {
			return SecretApplyResult{Operation: operation}, err
		}
	default:
		return service.requireSecretRecovery(
			operation,
			"secret provider effect has an unsupported durable state",
		)
	}
	if operation.Phase == OperationStaging ||
		operation.Phase == OperationRecoveryRequired {
		operation, err = operations.Transition(
			operation.ID,
			OperationProving,
		)
		if err != nil {
			return SecretApplyResult{}, err
		}
	}
	reference, err = service.Store.Reference(ctx, record.Plan.Ref)
	if err != nil {
		return SecretApplyResult{Operation: operation}, err
	}
	if reference.Generation != record.Plan.NextGeneration ||
		reference.Availability != record.Plan.NextAvailability {
		return service.requireSecretRecovery(
			operation,
			"secret provider state does not prove the reviewed generation",
		)
	}
	operation, err = operations.Terminal(
		operation.ID,
		OperationSucceeded,
		"secret-generation-committed",
		"The reviewed secret generation was committed.",
	)
	if err != nil {
		return SecretApplyResult{}, err
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

func (service *SecretService) reconcileRunningEffect(
	ctx context.Context,
	value *secrets.Buffer,
	plan SecretPlan,
	operation Operation,
	effect PlannedEffect,
) (secrets.Reference, Operation, error) {
	if service.Reconciler == nil {
		return secrets.Reference{}, operation, ErrSecretRecoveryRequired
	}
	reconciled, err := service.Reconciler.Reconcile(
		ctx,
		secrets.ReconcileRequest{
			Ref:                plan.Ref,
			Action:             plan.Action,
			OperationID:        plan.OperationID,
			ExpectedGeneration: plan.BaseGeneration,
		},
	)
	if err != nil {
		return secrets.Reference{}, operation, err
	}
	if reconciled.Committed == reconciled.Uncommitted {
		result, recoveryErr := service.requireSecretRecovery(
			operation,
			"secret provider did not return one exact commit or non-commit proof",
		)
		return secrets.Reference{}, result.Operation, recoveryErr
	}
	if reconciled.Committed {
		operation, err = service.operations().FinishEffect(
			operation.ID,
			effect.ID,
			effect.Provider,
			EffectSucceeded,
			secretGenerationEvidence(reconciled.Reference),
		)
		return reconciled.Reference, operation, err
	}
	if !reconciled.Uncommitted {
		result, recoveryErr := service.requireSecretRecovery(
			operation,
			"secret provider completion remains ambiguous",
		)
		return secrets.Reference{}, result.Operation, recoveryErr
	}
	if !sameSecretPlanBase(plan.Current, reconciled.Reference) {
		result, recoveryErr := service.requireSecretRecovery(
			operation,
			"secret generation changed while provider completion was unknown",
		)
		return secrets.Reference{}, result.Operation, recoveryErr
	}
	if plan.Action != secrets.ActionDelete && value == nil {
		return secrets.Reference{}, operation, ErrSecretValueRequired
	}
	reference, err := service.executeProvider(ctx, value, plan)
	if err != nil {
		return secrets.Reference{}, operation, err
	}
	operation, err = service.operations().FinishEffect(
		operation.ID,
		effect.ID,
		effect.Provider,
		EffectSucceeded,
		secretGenerationEvidence(reference),
	)
	return reference, operation, err
}

func (service *SecretService) executeProvider(
	ctx context.Context,
	value *secrets.Buffer,
	plan SecretPlan,
) (secrets.Reference, error) {
	switch plan.Action {
	case secrets.ActionSet, secrets.ActionRotate:
		if value == nil {
			return secrets.Reference{}, ErrSecretValueRequired
		}
		return service.Store.Set(ctx, secrets.WriteRequest{
			Ref:                plan.Ref,
			OperationID:        plan.OperationID,
			ExpectedGeneration: plan.BaseGeneration,
			Value:              value,
		})
	case secrets.ActionDelete:
		return service.Store.Delete(ctx, secrets.DeleteRequest{
			Ref:                plan.Ref,
			OperationID:        plan.OperationID,
			ExpectedGeneration: plan.BaseGeneration,
		})
	default:
		return secrets.Reference{}, ErrInvalidSecretPlan
	}
}

func (service *SecretService) buildPlan(
	ctx context.Context,
	operationID string,
	draft SecretDraft,
	current secrets.Reference,
	expiresAt time.Time,
) (SecretPlan, error) {
	if draft.Validate() != nil ||
		current.Ref != draft.Ref ||
		current.Generation == math.MaxUint64 {
		return SecretPlan{}, ErrInvalidSecretPlan
	}
	blockers := make([]Blocker, 0, 1)
	addBlocker := func(code, summary, recovery string) {
		blockers = append(blockers, Blocker{
			Code:     code,
			Resource: "secret:" + draft.Ref,
			Summary:  summary,
			Recovery: recovery,
		})
	}
	switch current.Availability {
	case secrets.AvailabilityLocked:
		addBlocker(
			"secret-provider-locked",
			"The secret provider is locked.",
			"unlock the login Keychain and create a fresh plan",
		)
	case secrets.AvailabilityUnavailable:
		addBlocker(
			"secret-provider-unavailable",
			"The secret provider is unavailable.",
			"run hideout doctor --feature secrets and create a fresh plan",
		)
	}
	switch draft.Action {
	case secrets.ActionSet:
		if current.Availability == secrets.AvailabilityAvailable {
			addBlocker(
				"secret-already-exists",
				"The secret reference already has a value.",
				"use the rotate action and review a fresh plan",
			)
		}
	case secrets.ActionRotate:
		if current.Availability == secrets.AvailabilityMissing {
			addBlocker(
				"secret-missing",
				"The secret reference does not have a value to rotate.",
				"use the set action and review a fresh plan",
			)
		}
	case secrets.ActionDelete:
		if current.Availability == secrets.AvailabilityMissing {
			addBlocker(
				"secret-missing",
				"The secret reference is already missing.",
				"refresh secret status before requesting another delete",
			)
		}
	}
	nextAvailability := secrets.AvailabilityAvailable
	effectID := "secret-write"
	effectKind := "persist"
	summary := "Store the reviewed secret generation in the system Keychain."
	if draft.Action == secrets.ActionDelete {
		nextAvailability = secrets.AvailabilityMissing
		effectID = "secret-delete"
		effectKind = "cleanup"
		summary = "Remove secret bytes and retain a recovery-safe generation tombstone."
	}
	networkReview, err := planSecretNetworkTransitions(
		ctx,
		service.Core,
		service.NetworkTransitions,
		draft,
		current.Generation+1,
	)
	if err != nil {
		return SecretPlan{}, err
	}
	blockers = append(blockers, networkReview.Blockers...)
	effects := profileNetworkPlannedEffects(networkReview.Plans)
	effects = append(effects, PlannedEffect{
		ID:            effectID,
		Kind:          effectKind,
		Scope:         "profile",
		Provider:      service.Store.Provider(),
		Live:          true,
		Summary:       summary,
		ProofRequired: []string{"secret-generation-committed"},
	})
	rollbackEffects := make([]string, len(effects))
	for index := range effects {
		rollbackEffects[index] = effects[index].ID
	}
	plan := SecretPlan{
		Schema:               SecretPlanSchema,
		OperationID:          operationID,
		Ref:                  draft.Ref,
		Action:               draft.Action,
		BaseGeneration:       current.Generation,
		Current:              current,
		NextAvailability:     nextAvailability,
		NextGeneration:       current.Generation + 1,
		AffectedProfiles:     networkReview.Profiles,
		AffectedEnvironments: networkReview.Environments,
		Effects:              effects,
		Blockers:             blockers,
		Warnings:             networkReview.Warnings,
		Rollback: RollbackPlan{
			Mode:    "provider-reconcile",
			Summary: "Restore every staged live route and reconcile the Keychain generation before retrying.",
			Effects: rollbackEffects,
		},
		ExpiresAt: expiresAt.Round(0).UTC(),
		networkTransitions: append(
			[]NetworkTransitionPlan(nil),
			networkReview.Plans...,
		),
	}
	sort.Slice(plan.Blockers, func(left, right int) bool {
		if plan.Blockers[left].Code != plan.Blockers[right].Code {
			return plan.Blockers[left].Code <
				plan.Blockers[right].Code
		}
		return plan.Blockers[left].Resource <
			plan.Blockers[right].Resource
	})
	if err := plan.Seal(); err != nil {
		return SecretPlan{}, err
	}
	return plan, nil
}

func (service *SecretService) ensureStaging(
	operation Operation,
) (Operation, error) {
	operations := service.operations()
	var err error
	if operation.Phase == OperationPlanned {
		operation, err = operations.Transition(
			operation.ID,
			OperationClaimed,
		)
		if err != nil {
			return Operation{}, err
		}
	}
	if operation.Phase == OperationClaimed {
		operation, err = operations.Transition(
			operation.ID,
			OperationStaging,
		)
		if err != nil {
			return Operation{}, err
		}
	}
	if operation.Phase != OperationStaging &&
		operation.Phase != OperationProving &&
		operation.Phase != OperationRecoveryRequired {
		return Operation{}, ErrSecretRecoveryRequired
	}
	return operation, nil
}

func (service *SecretService) requireSecretRecovery(
	operation Operation,
	reason string,
) (SecretApplyResult, error) {
	if operation.Phase != OperationRecoveryRequired &&
		!operation.Terminal() {
		updated, err := service.operations().RequireRecovery(
			operation.ID,
			"secret-provider-state-unproved",
			"The accepted secret operation cannot yet prove its provider generation.",
			"Unlock or repair the secret provider, then retry the same operation ID.",
		)
		if err == nil {
			operation = updated
		}
	}
	return SecretApplyResult{Operation: operation}, fmt.Errorf(
		"%w: %s",
		ErrSecretRecoveryRequired,
		reason,
	)
}

func (service *SecretService) cancelSecretPlan(
	operationID, code, summary string,
) {
	operation, err := service.operationStore().Load(operationID)
	if err != nil || operation.Phase != OperationPlanned {
		return
	}
	terminal, err := service.operations().Terminal(
		operationID,
		OperationCancelled,
		code,
		summary,
	)
	if err == nil && terminal.Terminal() {
		_ = service.removePlanRecord(operationID)
	}
}

func (service *SecretService) cancelPlannedOperation(
	operationID, code, summary string,
) {
	service.cancelSecretPlan(operationID, code, summary)
}

func secretProviderEffect(
	plan SecretPlan,
	provider string,
) (PlannedEffect, error) {
	var selected PlannedEffect
	for _, effect := range plan.Effects {
		if effect.Provider != provider {
			continue
		}
		if selected.ID != "" {
			return PlannedEffect{}, ErrInvalidSecretPlan
		}
		selected = effect
	}
	if selected.ID == "" {
		return PlannedEffect{}, ErrSecretProviderUnavailable
	}
	return selected, nil
}

func secretGenerationEvidence(
	reference secrets.Reference,
) []EvidenceRef {
	return []EvidenceRef{{
		Code: "secret-generation-committed",
		Ref: fmt.Sprintf(
			"secret:%s@generation:%d:%s",
			reference.Ref,
			reference.Generation,
			reference.Availability,
		),
		ObservedAt: reference.UpdatedAt,
	}}
}

func effectStatus(operation Operation, effectID string) string {
	index := effectIndex(operation.Effects, effectID)
	if index < 0 {
		return ""
	}
	return operation.Effects[index].Status
}

func sameSecretPlanBase(
	expected, current secrets.Reference,
) bool {
	return expected.Ref == current.Ref &&
		expected.Provider == current.Provider &&
		expected.Availability == current.Availability &&
		expected.Generation == current.Generation &&
		expected.UpdatedAt.Equal(current.UpdatedAt) &&
		expected.Reason == current.Reason
}

func operationBindingForSecretPlan(plan SecretPlan) OperationBinding {
	return OperationBinding{
		ID:           plan.OperationID,
		Kind:         "secret." + plan.Action,
		Owner:        OperationOwner{Kind: "secret", ID: plan.Ref},
		PlanDigest:   plan.PlanDigest,
		BaseRevision: plan.BaseGeneration,
	}
}

func operationEffectsForSecretPlan(plan SecretPlan) []EffectResult {
	effects := make([]EffectResult, 0, len(plan.Effects))
	for _, effect := range plan.Effects {
		effects = append(effects, EffectResult{
			ID:       effect.ID,
			Kind:     effect.Kind,
			Provider: effect.Provider,
			Status:   EffectPending,
		})
	}
	return effects
}

func validSecretAction(action string) bool {
	switch action {
	case secrets.ActionSet, secrets.ActionRotate, secrets.ActionDelete:
		return true
	default:
		return false
	}
}

func sortedUniqueStableNames(values []string) bool {
	previous := ""
	for _, value := range values {
		if !stableAPIField(value) ||
			(previous != "" && value <= previous) {
			return false
		}
		previous = value
	}
	return true
}

func newSecretPlanRecord(
	plan SecretPlan,
) (secretPlanRecord, error) {
	record := secretPlanRecord{
		Schema: secretPlanRecordSchema, Plan: plan,
		NetworkTransitions: append(
			[]NetworkTransitionPlan(nil),
			plan.networkTransitions...,
		),
	}
	record.Plan.networkTransitions = nil
	var err error
	record.RecordDigest, err = digestSecretPlanRecord(record)
	if err != nil {
		return secretPlanRecord{}, err
	}
	if err := validateSecretPlanRecord(record); err != nil {
		return secretPlanRecord{}, err
	}
	return record, nil
}

func validateSecretPlanRecord(record secretPlanRecord) error {
	if record.Schema != secretPlanRecordSchema ||
		!profileDigestPattern.MatchString(record.RecordDigest) ||
		record.Plan.VerifyDigest() != nil {
		return ErrInvalidSecretPlan
	}
	if err := validateProfileNetworkTransitionPlans(
		record.NetworkTransitions,
		record.Plan.Effects,
	); err != nil {
		return ErrInvalidSecretPlan
	}
	affected := make(map[string]struct{}, len(record.Plan.AffectedEnvironments))
	for _, environmentID := range record.Plan.AffectedEnvironments {
		affected[environmentID] = struct{}{}
	}
	for _, plan := range record.NetworkTransitions {
		if _, ok := affected[plan.EnvironmentID]; !ok {
			return ErrInvalidSecretPlan
		}
	}
	expected, err := digestSecretPlanRecord(record)
	if err != nil || expected != record.RecordDigest {
		return ErrInvalidSecretPlan
	}
	return nil
}

func digestSecretPlanRecord(
	record secretPlanRecord,
) (string, error) {
	return CanonicalDigest(
		secretRecordDomain,
		secretPlanRecordAuthority{
			Schema:             record.Schema,
			Plan:               record.Plan,
			NetworkTransitions: record.NetworkTransitions,
		},
	)
}

func sameSecretNetworkTransitionPlans(
	left []NetworkTransitionPlan,
	right []NetworkTransitionPlan,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].EnvironmentID != right[index].EnvironmentID ||
			left[index].PlanDigest != right[index].PlanDigest {
			return false
		}
	}
	return true
}

func (service *SecretService) planPath(operationID string) string {
	return filepath.Join(
		service.Core.Store.Root,
		"operations",
		"secret-plans",
		operationID+".json",
	)
}

func (service *SecretService) writePlanRecord(
	record secretPlanRecord,
) error {
	if err := validateSecretPlanRecord(record); err != nil {
		return err
	}
	if err := service.operationStore().ensureDirectory(); err != nil {
		return err
	}
	dir := filepath.Dir(service.planPath(record.Plan.OperationID))
	if err := ensurePrivateConfigurationPlanDirectory(dir); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxSecretPlanRecordBytes {
		return errors.New("secret plan record exceeds size bound")
	}
	path := service.planPath(record.Plan.OperationID)
	if _, err := os.Lstat(path); err == nil {
		return ErrOperationMismatch
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(dir, ".secret-plan-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keep = false
	return syncOperationDirectory(dir)
}

func (service *SecretService) loadPlanRecord(
	operationID string,
) (secretPlanRecord, error) {
	if !operationIDPattern.MatchString(operationID) {
		return secretPlanRecord{}, ErrInvalidSecretPlan
	}
	path := service.planPath(operationID)
	info, err := os.Lstat(path)
	if err != nil {
		return secretPlanRecord{}, err
	}
	if !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 ||
		info.Size() > maxSecretPlanRecordBytes {
		return secretPlanRecord{}, errors.New(
			"secret plan record must be a bounded private regular file",
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return secretPlanRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record secretPlanRecord
	if err := decoder.Decode(&record); err != nil {
		return secretPlanRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return secretPlanRecord{}, errors.New(
				"secret plan record contains trailing data",
			)
		}
		return secretPlanRecord{}, err
	}
	if record.Plan.OperationID != operationID {
		return secretPlanRecord{}, ErrOperationMismatch
	}
	if err := validateSecretPlanRecord(record); err != nil {
		return secretPlanRecord{}, err
	}
	return record, nil
}

func (service *SecretService) removePlanRecord(
	operationID string,
) error {
	path := service.planPath(operationID)
	if err := os.Remove(path); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncOperationDirectory(filepath.Dir(path))
}

func (service *SecretService) operationStore() OperationStore {
	store := service.Operations
	if strings.TrimSpace(store.Root) == "" {
		store.Root = service.Core.Store.Root
	}
	if store.Now == nil {
		store.Now = service.now
	}
	return store
}

func (service *SecretService) operations() OperationService {
	return OperationService{
		Store:    service.operationStore(),
		Observer: service.Core.Observer,
	}
}

func (service *SecretService) planTTL() time.Duration {
	if service.PlanTTL > 0 {
		return service.PlanTTL
	}
	return defaultSecretPlanTTL
}

func (service *SecretService) nowUTC() time.Time {
	if service.now != nil {
		return service.now().Round(0).UTC()
	}
	return time.Now().Round(0).UTC()
}

func acquireSecretApplyLock(key string) func() {
	secretApplyLocksMu.Lock()
	entry := secretApplyLocks[key]
	if entry == nil {
		entry = &secretApplyLock{}
		secretApplyLocks[key] = entry
	}
	entry.refs++
	secretApplyLocksMu.Unlock()
	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()
		secretApplyLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(secretApplyLocks, key)
		}
		secretApplyLocksMu.Unlock()
	}
}

func acquireSecretMutation(
	root, ref, operationID string,
) (func(), error) {
	key := root + "\x00secret\x00" + ref
	secretMutationOwnersMu.Lock()
	defer secretMutationOwnersMu.Unlock()
	if owner := secretMutationOwners[key]; owner != "" {
		return nil, &ConfigurationMutationConflictError{
			Key:              "secret." + ref,
			OwnerOperationID: owner,
		}
	}
	secretMutationOwners[key] = operationID
	return func() {
		secretMutationOwnersMu.Lock()
		defer secretMutationOwnersMu.Unlock()
		if secretMutationOwners[key] == operationID {
			delete(secretMutationOwners, key)
		}
	}, nil
}

func checkSecretServiceContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var _ SecretProvider = (*SecretService)(nil)
