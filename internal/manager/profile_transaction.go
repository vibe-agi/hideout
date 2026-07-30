package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	ConfigurationApplySchema = "hideout.configuration-apply.v1"

	profileTransactionPlanRecordSchema = "hideout.configuration-plan-record.v1"
	profileTransactionOperationKind    = "profile.transaction"
	profileTransactionRecordDomain     = "configuration-plan-record"
	defaultConfigurationPlanTTL        = 15 * time.Minute
	maxConfigurationPlanRecordBytes    = 1 << 20
)

var (
	ErrStaleConfigurationPlan = errors.New(
		"configuration plan is stale",
	)
	ErrConfigurationPlanExpired = errors.New(
		"configuration plan has expired",
	)
	ErrConfigurationConfirmationRequired = errors.New(
		"configuration apply requires confirmation",
	)
	ErrConfigurationMutationConflict = errors.New(
		"configuration mutation key is already owned",
	)
	ErrConfigurationBlocked = errors.New(
		"configuration plan is blocked",
	)
	ErrConfigurationRecoveryRequired = errors.New(
		"configuration operation requires reconciliation",
	)
	ErrConfigurationProviderUnavailable = errors.New(
		"configuration change provider is unavailable",
	)
	ErrConfigurationNoChange = errors.New(
		"configuration change has no effect",
	)

	profileTransactionApplyLocksMu sync.Mutex
	profileTransactionApplyLocks   = map[string]*profileTransactionApplyLock{}

	configurationMutationOwnersMu sync.Mutex
	configurationMutationOwners   = map[string]string{}
)

type profileTransactionApplyLock struct {
	mutex sync.Mutex
	refs  int
}

type ConfigurationApplyRequest struct {
	Schema       string `json:"schema"`
	OperationID  string `json:"operationId"`
	Profile      string `json:"profile"`
	BaseRevision uint64 `json:"baseRevision"`
	PlanDigest   string `json:"planDigest"`
	Confirmed    bool   `json:"confirmed"`
}

func (request ConfigurationApplyRequest) Validate() error {
	if request.Schema != ConfigurationApplySchema ||
		!operationIDPattern.MatchString(request.OperationID) ||
		request.BaseRevision == 0 ||
		!profileDigestPattern.MatchString(request.PlanDigest) {
		return ErrInvalidConfigurationPlan
	}
	if _, err := normalizeManagerProfileName(request.Profile); err != nil {
		return ErrInvalidConfigurationPlan
	}
	return nil
}

// ConfigurationApplyRequestForOperation reconstructs the exact idempotent
// retry binding from a validated durable profile-configuration operation. It
// does not create a new plan or operation identity.
func ConfigurationApplyRequestForOperation(
	operation Operation,
) (ConfigurationApplyRequest, error) {
	if err := operation.Validate(); err != nil ||
		operation.Kind != profileTransactionOperationKind ||
		operation.Owner.Kind != "profile" ||
		operation.BaseRevision == 0 {
		return ConfigurationApplyRequest{}, ErrInvalidConfigurationPlan
	}
	request := ConfigurationApplyRequest{
		Schema:       ConfigurationApplySchema,
		OperationID:  operation.ID,
		Profile:      operation.Owner.ID,
		BaseRevision: operation.BaseRevision,
		PlanDigest:   operation.PlanDigest,
		Confirmed:    true,
	}
	if err := request.Validate(); err != nil {
		return ConfigurationApplyRequest{}, err
	}
	return request, nil
}

type ConfigurationApplyResult struct {
	Operation  Operation         `json:"operation"`
	Projection ProfileProjection `json:"projection"`
}

type ConfigurationMutationConflictError struct {
	Key              string
	OwnerOperationID string
	OwnerKind        string
	OwnerPhase       string
	Recovery         string
}

func (err *ConfigurationMutationConflictError) Error() string {
	if err == nil {
		return ErrConfigurationMutationConflict.Error()
	}
	ownerKind := err.OwnerKind
	if ownerKind == "" {
		ownerKind = lifecycle.MutationOwnerConfiguration
	}
	ownerPhase := err.OwnerPhase
	if ownerPhase == "" {
		ownerPhase = lifecycle.MutationPhaseApplying
	}
	recovery := err.Recovery
	if recovery == "" {
		recovery = "inspect the owning operation and retry after it reaches a terminal phase"
	}
	return fmt.Sprintf(
		"%s: key=%s owner=%s/%s phase=%s recovery=%s",
		ErrConfigurationMutationConflict,
		err.Key,
		ownerKind,
		err.OwnerOperationID,
		ownerPhase,
		recovery,
	)
}

func (err *ConfigurationMutationConflictError) Unwrap() error {
	return ErrConfigurationMutationConflict
}

type profileTransactionBuild struct {
	Desired            profile.Profile
	Diff               []ReviewDiff
	Effects            []PlannedEffect
	Blockers           []Blocker
	Warnings           []Warning
	Rollback           RollbackPlan
	NetworkTransitions []NetworkTransitionPlan
}

type profileTransactionHooks struct {
	afterPersist          func(Operation) error
	afterProjectionCommit func(Operation) error
	afterTerminal         func(Operation) error
}

type profileTransactionPlanRecord struct {
	Schema             string                  `json:"schema"`
	Plan               ConfigurationPlan       `json:"plan"`
	PrivateChanges     []TypedChange           `json:"privateChanges"`
	BaseNetwork        *profile.Network        `json:"baseNetwork,omitempty"`
	DesiredNetwork     *profile.Network        `json:"desiredNetwork,omitempty"`
	TargetDigest       string                  `json:"targetDigest"`
	MutationKeys       []string                `json:"mutationKeys"`
	NetworkTransitions []NetworkTransitionPlan `json:"networkTransitions"`
	RecordDigest       string                  `json:"recordDigest"`

	Desired profile.Profile `json:"-"`
}

type profileTransactionPlanAuthority struct {
	Schema             string                  `json:"schema"`
	Plan               ConfigurationPlan       `json:"plan"`
	PrivateChanges     []TypedChange           `json:"privateChanges"`
	BaseNetwork        *profile.Network        `json:"baseNetwork,omitempty"`
	DesiredNetwork     *profile.Network        `json:"desiredNetwork,omitempty"`
	TargetDigest       string                  `json:"targetDigest"`
	MutationKeys       []string                `json:"mutationKeys"`
	NetworkTransitions []NetworkTransitionPlan `json:"networkTransitions"`
}

type ProfileTransactionService struct {
	Core       Core
	Registry   TypedChangeRegistry
	Operations OperationStore
	PlanTTL    time.Duration
	Mutations  lifecycle.MutationCoordinator
	// NetworkTransitions is injected only by the daemon hosting the live
	// gateway/backend authority. Daemon-less services keep next-attach
	// semantics and never manufacture live effects.
	NetworkTransitions *ProfileNetworkTransitionCoordinator

	now            func() time.Time
	build          func(context.Context, profile.Profile, []TypedChange) (profileTransactionBuild, error)
	persistProfile func(profile.Profile) error
	hooks          profileTransactionHooks
}

func NewProfileTransactionService(core Core) *ProfileTransactionService {
	service := &ProfileTransactionService{
		Core:       core,
		Registry:   DefaultTypedChangeRegistry(),
		Operations: OperationStore{Root: core.Store.Root},
		PlanTTL:    defaultConfigurationPlanTTL,
		persistProfile: func(value profile.Profile) error {
			return core.Store.Save(value)
		},
	}
	service.build = service.buildTypedProfileChanges
	return service
}

// SetClock installs the hosting control plane's clock. It is intended for the
// one daemon-owned service assembled before serving requests; callers must not
// change it while the service is in use.
func (service *ProfileTransactionService) SetClock(now func() time.Time) {
	if service != nil {
		service.now = now
	}
}

func (service *ProfileTransactionService) Plan(
	ctx context.Context,
	draft ConfigurationDraft,
) (ConfigurationPlan, error) {
	if err := checkProfileTransactionContext(ctx); err != nil {
		return ConfigurationPlan{}, err
	}
	if service == nil {
		return ConfigurationPlan{}, ErrConfigurationProviderUnavailable
	}
	registry := service.registry()
	normalized, err := registry.NormalizeDraft(draft)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	projection, err := service.projections().Load(normalized.Profile)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	if projection.Revision != normalized.BaseRevision {
		return ConfigurationPlan{}, fmt.Errorf(
			"%w: current revision=%d",
			ErrStaleConfigurationPlan,
			projection.Revision,
		)
	}
	build, err := service.buildChanges(
		ctx,
		projection.Desired,
		normalized.Changes,
	)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	build, err = normalizeProfileTransactionBuild(projection.Desired, build)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	operationID, err := NewOperationID()
	if err != nil {
		return ConfigurationPlan{}, err
	}
	keys, err := configurationMutationKeys(registry, normalized.Changes)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	record, err := service.buildPlanRecord(
		operationID,
		projection,
		normalized.Changes,
		build,
		keys,
		service.nowUTC().Add(service.planTTL()),
	)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	if err := service.writePlanRecord(record); err != nil {
		return ConfigurationPlan{}, err
	}
	operationStore := service.operationStore()
	_, created, err := operationStore.Reserve(
		operationBindingForConfigurationPlan(record.Plan),
		operationEffectsForConfigurationPlan(record.Plan),
	)
	if err != nil || !created {
		_ = service.removePlanRecord(operationID)
		if err != nil {
			return ConfigurationPlan{}, err
		}
		return ConfigurationPlan{}, ErrOperationMismatch
	}
	return record.Plan, nil
}

// InspectPlan returns the exact durable canonical review bound to an operation.
// It is read-only and revalidates both the private plan record and the public
// operation binding before returning anything to a client.
func (service *ProfileTransactionService) InspectPlan(
	ctx context.Context,
	operationID string,
) (ConfigurationPlan, error) {
	if err := checkProfileTransactionContext(ctx); err != nil {
		return ConfigurationPlan{}, err
	}
	if service == nil {
		return ConfigurationPlan{}, ErrConfigurationProviderUnavailable
	}
	operation, err := service.operationStore().Load(operationID)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	if operation.Kind != profileTransactionOperationKind ||
		operation.Owner.Kind != "profile" {
		return ConfigurationPlan{}, ErrOperationMismatch
	}
	record, err := service.loadPlanRecord(operationID)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	if !operation.Matches(
		operationBindingForConfigurationPlan(record.Plan),
	) {
		return ConfigurationPlan{}, ErrOperationMismatch
	}
	return record.Plan, nil
}

func (service *ProfileTransactionService) Apply(
	ctx context.Context,
	request ConfigurationApplyRequest,
) (ConfigurationApplyResult, error) {
	if err := request.Validate(); err != nil {
		return ConfigurationApplyResult{}, err
	}
	if service == nil {
		return ConfigurationApplyResult{}, ErrConfigurationProviderUnavailable
	}
	key := service.Core.Store.Root + "\x00" + request.OperationID
	release := acquireProfileTransactionApplyLock(key)
	defer release()
	return service.applyLocked(ctx, request)
}

// ApplyCurrent is the compatibility adapter for explicit legacy mutation
// commands that did not carry a revision or operation ID. It snapshots the
// current projection, creates the same durable reviewed transaction as modern
// clients, and immediately confirms that exact plan. It never retries a stale
// plan on the caller's behalf.
func (service *ProfileTransactionService) ApplyCurrent(
	ctx context.Context,
	profileName, clientNonce string,
	changes []TypedChange,
) (ConfigurationApplyResult, error) {
	if service == nil {
		return ConfigurationApplyResult{}, ErrConfigurationProviderUnavailable
	}
	name, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	if _, err := service.Core.Store.LoadOrInit(name); err != nil {
		return ConfigurationApplyResult{}, err
	}
	projection, err := service.projections().Load(name)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	plan, err := service.Plan(ctx, ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      name,
		BaseRevision: projection.Revision,
		ClientNonce:  clientNonce,
		Changes:      cloneTypedChanges(changes),
	})
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	return service.Apply(ctx, ConfigurationApplyRequest{
		Schema:       ConfigurationApplySchema,
		OperationID:  plan.OperationID,
		Profile:      plan.Profile,
		BaseRevision: plan.BaseRevision,
		PlanDigest:   plan.PlanDigest,
		Confirmed:    true,
	})
}

func (service *ProfileTransactionService) applyLocked(
	ctx context.Context,
	request ConfigurationApplyRequest,
) (ConfigurationApplyResult, error) {
	if err := checkProfileTransactionContext(ctx); err != nil {
		return ConfigurationApplyResult{}, err
	}
	operationStore := service.operationStore()
	operation, err := operationStore.Load(request.OperationID)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	requestBinding := OperationBinding{
		ID: request.OperationID, Kind: profileTransactionOperationKind,
		Owner:      OperationOwner{Kind: "profile", ID: request.Profile},
		PlanDigest: request.PlanDigest, BaseRevision: request.BaseRevision,
	}
	if !operation.Matches(requestBinding) {
		return ConfigurationApplyResult{}, ErrOperationMismatch
	}
	if operation.Terminal() {
		projection, projectionErr := service.projections().Load(request.Profile)
		return ConfigurationApplyResult{
			Operation: operation, Projection: projection,
		}, projectionErr
	}
	record, err := service.loadPlanRecord(request.OperationID)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	if request.Profile != record.Plan.Profile ||
		request.BaseRevision != record.Plan.BaseRevision ||
		request.PlanDigest != record.Plan.PlanDigest {
		return ConfigurationApplyResult{}, ErrOperationMismatch
	}
	if !operation.Matches(operationBindingForConfigurationPlan(record.Plan)) {
		return ConfigurationApplyResult{}, ErrOperationMismatch
	}
	if !request.Confirmed {
		return ConfigurationApplyResult{}, ErrConfigurationConfirmationRequired
	}
	if operation.Phase == OperationPlanned &&
		!service.nowUTC().Before(record.Plan.ExpiresAt) {
		service.cancelPlannedOperation(
			record.Plan.OperationID,
			"plan-expired",
			"The reviewed configuration plan expired before it was applied.",
		)
		return ConfigurationApplyResult{}, ErrConfigurationPlanExpired
	}
	release, err := acquireConfigurationMutationKeys(
		ctx,
		service.Core.Store.Root,
		record.Plan.Profile,
		record.Plan.OperationID,
		record.MutationKeys,
		service.Mutations,
	)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	defer release()

	var result ConfigurationApplyResult
	err = service.Core.withProfileMutationLock(
		record.Plan.Profile,
		func() error {
			var applyErr error
			result, applyErr = service.applyUnderProfileLock(ctx, record)
			return applyErr
		},
	)
	return result, err
}

// ReconcileOperation resumes only an already accepted durable operation. A
// merely planned operation still requires an explicit client confirmation.
// The private plan record and operation binding are revalidated before any
// provider effect is considered.
func (service *ProfileTransactionService) ReconcileOperation(
	ctx context.Context,
	operationID string,
) (ConfigurationApplyResult, error) {
	if service == nil {
		return ConfigurationApplyResult{},
			ErrConfigurationProviderUnavailable
	}
	if err := checkProfileTransactionContext(ctx); err != nil {
		return ConfigurationApplyResult{}, err
	}
	key := service.Core.Store.Root + "\x00" + operationID
	releaseApply := acquireProfileTransactionApplyLock(key)
	defer releaseApply()

	operation, err := service.operationStore().Load(operationID)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	if operation.Kind != profileTransactionOperationKind ||
		operation.Owner.Kind != "profile" {
		return ConfigurationApplyResult{}, ErrOperationMismatch
	}
	if operation.Terminal() {
		projection, projectionErr := service.projections().Load(
			operation.Owner.ID,
		)
		return ConfigurationApplyResult{
			Operation: operation, Projection: projection,
		}, projectionErr
	}
	if operation.Phase == OperationPlanned {
		return ConfigurationApplyResult{Operation: operation},
			ErrConfigurationConfirmationRequired
	}
	record, err := service.loadPlanRecord(operationID)
	if err != nil {
		return ConfigurationApplyResult{Operation: operation}, err
	}
	if !operation.Matches(
		operationBindingForConfigurationPlan(record.Plan),
	) {
		return ConfigurationApplyResult{Operation: operation},
			ErrOperationMismatch
	}
	releaseKeys, err := acquireConfigurationMutationKeys(
		ctx,
		service.Core.Store.Root,
		record.Plan.Profile,
		record.Plan.OperationID,
		record.MutationKeys,
		service.Mutations,
	)
	if err != nil {
		return ConfigurationApplyResult{Operation: operation}, err
	}
	defer releaseKeys()

	var result ConfigurationApplyResult
	err = service.Core.withProfileMutationLock(
		record.Plan.Profile,
		func() error {
			var reconcileErr error
			result, reconcileErr = service.applyUnderProfileLock(
				ctx,
				record,
			)
			return reconcileErr
		},
	)
	return result, err
}

func (service *ProfileTransactionService) applyUnderProfileLock(
	ctx context.Context,
	record profileTransactionPlanRecord,
) (ConfigurationApplyResult, error) {
	if err := checkProfileTransactionContext(ctx); err != nil {
		return ConfigurationApplyResult{}, err
	}
	operationStore := service.operationStore()
	operation, err := operationStore.Load(record.Plan.OperationID)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	if operation.Terminal() {
		projection, projectionErr := service.projections().loadLocked(
			record.Plan.Profile,
		)
		return ConfigurationApplyResult{
			Operation: operation, Projection: projection,
		}, projectionErr
	}
	projection, err := service.projections().loadLocked(record.Plan.Profile)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	baseCurrent := projection.Revision == record.Plan.BaseRevision &&
		projection.ContentDigest == record.Plan.BaseDigest
	targetCommitted := projection.Revision == record.Plan.BaseRevision+1 &&
		projection.ContentDigest == record.TargetDigest
	switch {
	case baseCurrent:
		replanned, replanErr := service.replanLocked(ctx, record, projection)
		if replanErr != nil {
			service.cancelPlannedOperation(
				record.Plan.OperationID,
				"stale-plan",
				"The authoritative configuration no longer matches the reviewed plan.",
			)
			return ConfigurationApplyResult{}, replanErr
		}
		record = replanned
		if len(record.Plan.Blockers) != 0 {
			service.cancelPlannedOperation(
				record.Plan.OperationID,
				"plan-blocked",
				"The reviewed configuration has active blockers.",
			)
			return ConfigurationApplyResult{}, ErrConfigurationBlocked
		}
	case targetCommitted && operation.Phase == OperationPlanned:
		service.cancelPlannedOperation(
			record.Plan.OperationID,
			"stale-plan",
			"The profile changed after this configuration was reviewed.",
		)
		return ConfigurationApplyResult{}, ErrStaleConfigurationPlan
	case targetCommitted:
		if operation.Phase != OperationStaging &&
			operation.Phase != OperationActivating &&
			!(operation.Phase == OperationRecoveryRequired &&
				len(record.NetworkTransitions) != 0) &&
			operation.Phase != OperationProving {
			return service.requireConfigurationRecovery(
				operation,
				"target state exists without an owned persistence checkpoint",
			)
		}
	default:
		if operation.Phase == OperationPlanned {
			service.cancelPlannedOperation(
				record.Plan.OperationID,
				"stale-plan",
				"The profile changed after this configuration was reviewed.",
			)
			return ConfigurationApplyResult{}, ErrStaleConfigurationPlan
		}
		return service.requireConfigurationRecovery(
			operation,
			"profile state diverged after the operation was claimed",
		)
	}

	operation, err = service.ensureConfigurationOperationStaging(operation)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	persistEffect, err := configurationPersistEffect(record.Plan)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	operationService := service.operations()
	operation, execute, err := operationService.BeginEffect(
		operation.ID,
		persistEffect.ID,
		persistEffect.Provider,
	)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	switch {
	case execute:
		persist := service.persistProfile
		if persist == nil {
			persist = service.Core.Store.Save
		}
		if err := persist(record.Desired); err != nil {
			_, _ = operationService.FinishEffect(
				operation.ID,
				persistEffect.ID,
				persistEffect.Provider,
				EffectFailed,
				[]EvidenceRef{{Code: "profile-persist-failed"}},
			)
			terminal, terminalErr := operationService.Terminal(
				operation.ID,
				OperationFailed,
				"profile-persist-failed",
				"The reviewed profile configuration could not be persisted.",
			)
			if terminalErr == nil && terminal.Terminal() {
				_ = service.removePlanRecord(operation.ID)
			}
			return ConfigurationApplyResult{}, err
		}
		if service.hooks.afterPersist != nil {
			if err := service.hooks.afterPersist(operation); err != nil {
				return ConfigurationApplyResult{}, err
			}
		}
		operation, err = operationService.FinishEffect(
			operation.ID,
			persistEffect.ID,
			persistEffect.Provider,
			EffectSucceeded,
			[]EvidenceRef{{
				Code: "profile-persisted",
				Ref:  "profile:" + record.Plan.Profile,
			}},
		)
		if err != nil {
			return ConfigurationApplyResult{}, err
		}
	case targetCommitted:
		operation, err = reconcileConfigurationPersistEffect(
			operationService,
			operation,
			persistEffect.ID,
			persistEffect.Provider,
			record.Plan.Profile,
		)
		if err != nil {
			return ConfigurationApplyResult{}, err
		}
	default:
		return service.requireConfigurationRecovery(
			operation,
			"a running persistence effect has no authoritative commit proof",
		)
	}
	if operation.Phase == OperationStaging {
		operation, err = operationService.Transition(
			operation.ID,
			OperationProving,
		)
		if err != nil {
			return ConfigurationApplyResult{}, err
		}
	}
	projection, err = service.projections().loadLocked(record.Plan.Profile)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	if projection.Revision != record.Plan.BaseRevision+1 ||
		projection.ContentDigest != record.TargetDigest {
		return service.requireConfigurationRecovery(
			operation,
			"persisted profile does not match the reviewed target projection",
		)
	}
	if len(record.NetworkTransitions) != 0 {
		if service.NetworkTransitions == nil {
			return service.requireConfigurationRecovery(
				operation,
				"live network transition provider is unavailable",
			)
		}
		var transitionResult *NetworkTransitionResult
		operation, transitionResult, err =
			service.NetworkTransitions.Apply(
				ctx,
				operation.ID,
				record.NetworkTransitions,
				operationService,
			)
		if err != nil {
			if transitionResult == nil &&
				errors.Is(
					err,
					errProfileNetworkEffectNeedsRecovery,
				) {
				return service.requireConfigurationRecovery(
					operation,
					"live network effect is partially checkpointed",
				)
			}
			return service.rollbackProfileAfterNetworkFailure(
				record,
				operation,
				transitionResult,
				err,
			)
		}
	}
	if service.hooks.afterProjectionCommit != nil {
		if err := service.hooks.afterProjectionCommit(operation); err != nil {
			return ConfigurationApplyResult{}, err
		}
	}
	operation, err = operationService.Terminal(
		operation.ID,
		OperationSucceeded,
		"profile-committed",
		"The reviewed profile configuration was committed.",
	)
	if err != nil {
		return ConfigurationApplyResult{}, err
	}
	result := ConfigurationApplyResult{
		Operation: operation, Projection: projection,
	}
	_ = service.removePlanRecord(operation.ID)
	if service.hooks.afterTerminal != nil {
		if err := service.hooks.afterTerminal(operation); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (service *ProfileTransactionService) rollbackProfileAfterNetworkFailure(
	record profileTransactionPlanRecord,
	operation Operation,
	transition *NetworkTransitionResult,
	transitionErr error,
) (ConfigurationApplyResult, error) {
	operations := service.operations()
	persistEffect, effectErr := configurationPersistEffect(record.Plan)
	if effectErr != nil {
		return service.requireConfigurationRecovery(
			operation,
			"profile persistence effect is unavailable during rollback",
		)
	}
	persist := service.persistProfile
	if persist == nil {
		persist = service.Core.Store.Save
	}
	var projection ProfileProjection
	var restoreErr error
	if record.BaseNetwork == nil {
		restoreErr = errors.New(
			"reviewed base network is unavailable",
		)
	} else {
		current, loadErr := service.projections().loadLocked(
			record.Plan.Profile,
		)
		if loadErr != nil {
			restoreErr = loadErr
		} else {
			restored := current.Desired
			restored.Network = *record.BaseNetwork
			restoreErr = persist(restored)
		}
	}
	if restoreErr == nil {
		projection, restoreErr = service.projections().loadLocked(
			record.Plan.Profile,
		)
		if restoreErr == nil &&
			projection.ContentDigest != record.Plan.BaseDigest {
			restoreErr = errors.New(
				"restored profile does not match the reviewed base",
			)
		}
	}
	persistStatus := EffectRolledBack
	persistEvidence := []EvidenceRef{{
		Code: "profile-restored",
		Ref:  "profile:" + record.Plan.Profile,
	}}
	terminalPhase := OperationRolledBack
	terminalCode := "network-route-restored"
	terminalSummary := profileNetworkTransitionFailureSummary(
		transition,
		transitionErr,
	)
	if restoreErr != nil {
		persistStatus = EffectUnproved
		persistEvidence = []EvidenceRef{{
			Code: "profile-restore-unproved",
			Ref:  "profile:" + record.Plan.Profile,
		}}
	}
	if restoreErr != nil ||
		transition != nil &&
			transition.Phase ==
				NetworkTransitionRollbackUnproved {
		terminalPhase = OperationRollbackUnproved
		terminalCode = "network-rollback-unproved"
		terminalSummary = "The profile or effective network rollback could not be proved."
	}
	operation, effectErr = operations.FinishEffect(
		operation.ID,
		persistEffect.ID,
		persistEffect.Provider,
		persistStatus,
		persistEvidence,
	)
	if effectErr != nil {
		return service.requireConfigurationRecovery(
			operation,
			"could not checkpoint profile restoration",
		)
	}
	if operation.Phase != OperationRollingBack {
		operation, effectErr = operations.Transition(
			operation.ID,
			OperationRollingBack,
		)
		if effectErr != nil {
			return service.requireConfigurationRecovery(
				operation,
				"could not enter configuration rollback",
			)
		}
	}
	operation, effectErr = operations.Terminal(
		operation.ID,
		terminalPhase,
		terminalCode,
		terminalSummary,
	)
	if effectErr != nil {
		return service.requireConfigurationRecovery(
			operation,
			"could not commit configuration rollback evidence",
		)
	}
	result := ConfigurationApplyResult{
		Operation:  operation,
		Projection: projection,
	}
	_ = service.removePlanRecord(operation.ID)
	if service.hooks.afterTerminal != nil {
		if hookErr := service.hooks.afterTerminal(operation); hookErr != nil {
			return result, errors.Join(transitionErr, hookErr)
		}
	}
	return result, errors.Join(transitionErr, restoreErr)
}

func (service *ProfileTransactionService) removePlanRecord(
	operationID string,
) error {
	path := service.planPath(operationID)
	if err := os.Remove(path); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncOperationDirectory(filepath.Dir(path))
}

func (service *ProfileTransactionService) replanLocked(
	ctx context.Context,
	record profileTransactionPlanRecord,
	projection ProfileProjection,
) (profileTransactionPlanRecord, error) {
	build, err := service.buildChanges(
		ctx,
		projection.Desired,
		record.PrivateChanges,
	)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	build, err = normalizeProfileTransactionBuild(projection.Desired, build)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	replanned, err := service.buildPlanRecord(
		record.Plan.OperationID,
		projection,
		record.PrivateChanges,
		build,
		record.MutationKeys,
		record.Plan.ExpiresAt,
	)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	if replanned.Plan.PlanDigest != record.Plan.PlanDigest ||
		replanned.TargetDigest != record.TargetDigest {
		return profileTransactionPlanRecord{}, ErrStaleConfigurationPlan
	}
	return replanned, nil
}

func (service *ProfileTransactionService) buildPlanRecord(
	operationID string,
	projection ProfileProjection,
	changes []TypedChange,
	build profileTransactionBuild,
	mutationKeys []string,
	expiresAt time.Time,
) (profileTransactionPlanRecord, error) {
	reviewedChanges, err := service.registry().ReviewChanges(changes)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	plan := ConfigurationPlan{
		Schema:           ConfigurationPlanSchema,
		OperationID:      operationID,
		Profile:          projection.Profile,
		BaseRevision:     projection.Revision,
		BaseDigest:       projection.ContentDigest,
		CanonicalChanges: clonePlanCollection(reviewedChanges),
		Diff:             clonePlanCollection(build.Diff),
		Effects:          clonePlanCollection(build.Effects),
		Blockers:         clonePlanCollection(build.Blockers),
		Warnings:         clonePlanCollection(build.Warnings),
		Rollback:         build.Rollback,
		ExpiresAt:        expiresAt.Round(0).UTC(),
	}
	plan.Rollback.Effects = clonePlanCollection(build.Rollback.Effects)
	if err := plan.Seal(); err != nil {
		return profileTransactionPlanRecord{}, err
	}
	targetDigest, err := CanonicalDigest(
		CanonicalDomainProfileProjection,
		build.Desired,
	)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	var baseNetwork, desiredNetwork *profile.Network
	if len(build.NetworkTransitions) != 0 {
		base := projection.Desired.Network
		target := build.Desired.Network
		baseNetwork = &base
		desiredNetwork = &target
	}
	record := profileTransactionPlanRecord{
		Schema:         profileTransactionPlanRecordSchema,
		Plan:           plan,
		PrivateChanges: cloneTypedChanges(changes),
		BaseNetwork:    baseNetwork,
		DesiredNetwork: desiredNetwork,
		Desired:        build.Desired,
		TargetDigest:   targetDigest,
		MutationKeys:   append([]string(nil), mutationKeys...),
		NetworkTransitions: append(
			[]NetworkTransitionPlan(nil),
			build.NetworkTransitions...,
		),
	}
	record.RecordDigest, err = digestProfileTransactionPlanRecord(record)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	if err := service.validatePlanRecord(record); err != nil {
		return profileTransactionPlanRecord{}, err
	}
	return record, nil
}

func clonePlanCollection[T any](values []T) []T {
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}

func normalizeProfileTransactionBuild(
	current profile.Profile,
	build profileTransactionBuild,
) (profileTransactionBuild, error) {
	if build.Desired.Name != current.Name ||
		build.Desired.SchemaVersion != current.SchemaVersion ||
		build.Desired.Validate() != nil ||
		len(build.Effects) == 0 {
		return profileTransactionBuild{}, ErrInvalidConfigurationPlan
	}
	sortProfileNetworkTransitionPlans(build.NetworkTransitions)
	if err := validateProfileNetworkTransitionPlans(
		build.NetworkTransitions,
		build.Effects,
	); err != nil {
		return profileTransactionBuild{}, err
	}
	sort.Slice(build.Diff, func(left, right int) bool {
		leftKey := build.Diff[left].Kind + "\x00" +
			build.Diff[left].Field + "\x00" + build.Diff[left].Scope
		rightKey := build.Diff[right].Kind + "\x00" +
			build.Diff[right].Field + "\x00" + build.Diff[right].Scope
		return leftKey < rightKey
	})
	sort.Slice(build.Effects, func(left, right int) bool {
		return build.Effects[left].ID < build.Effects[right].ID
	})
	sort.Slice(build.Blockers, func(left, right int) bool {
		leftKey := build.Blockers[left].Code + "\x00" +
			build.Blockers[left].Resource
		rightKey := build.Blockers[right].Code + "\x00" +
			build.Blockers[right].Resource
		return leftKey < rightKey
	})
	sort.Slice(build.Warnings, func(left, right int) bool {
		return build.Warnings[left].Code < build.Warnings[right].Code
	})
	sort.Strings(build.Rollback.Effects)
	return build, nil
}

func (service *ProfileTransactionService) buildChanges(
	ctx context.Context,
	current profile.Profile,
	changes []TypedChange,
) (profileTransactionBuild, error) {
	if service.build == nil {
		return profileTransactionBuild{}, ErrConfigurationProviderUnavailable
	}
	return service.build(ctx, current, cloneTypedChanges(changes))
}

func (service *ProfileTransactionService) ensureConfigurationOperationStaging(
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
		operation.Phase != OperationActivating &&
		operation.Phase != OperationRecoveryRequired &&
		operation.Phase != OperationProving {
		return Operation{}, ErrConfigurationRecoveryRequired
	}
	return operation, nil
}

func reconcileConfigurationPersistEffect(
	operations OperationService,
	operation Operation,
	effectID, provider, profileName string,
) (Operation, error) {
	index := effectIndex(operation.Effects, effectID)
	if index < 0 {
		return Operation{}, ErrInvalidOperation
	}
	switch operation.Effects[index].Status {
	case EffectRunning:
		return operations.FinishEffect(
			operation.ID,
			effectID,
			provider,
			EffectSucceeded,
			[]EvidenceRef{{
				Code: "profile-committed",
				Ref:  "profile:" + profileName,
			}},
		)
	case EffectSucceeded:
		return operation, nil
	default:
		return Operation{}, ErrConfigurationRecoveryRequired
	}
}

func (service *ProfileTransactionService) requireConfigurationRecovery(
	operation Operation,
	reason string,
) (ConfigurationApplyResult, error) {
	if operation.Phase != OperationRecoveryRequired &&
		!operation.Terminal() {
		updated, err := service.operations().RequireRecovery(
			operation.ID,
			"configuration-state-unproved",
			"The accepted configuration operation cannot yet prove its authoritative target state.",
			"Inspect the operation evidence and retry the same operation ID.",
		)
		if err == nil {
			operation = updated
		}
	}
	return ConfigurationApplyResult{Operation: operation}, fmt.Errorf(
		"%w: %s",
		ErrConfigurationRecoveryRequired,
		reason,
	)
}

func (service *ProfileTransactionService) cancelPlannedOperation(
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

func configurationPersistEffect(
	plan ConfigurationPlan,
) (PlannedEffect, error) {
	var persist PlannedEffect
	for _, effect := range plan.Effects {
		if effect.Provider == networkTransitionProviderName {
			continue
		}
		if effect.Kind != "persist" ||
			effect.Provider != "manager.profile" {
			return PlannedEffect{}, ErrConfigurationProviderUnavailable
		}
		if persist.ID != "" {
			return PlannedEffect{}, ErrInvalidConfigurationPlan
		}
		persist = effect
	}
	if persist.ID == "" {
		return PlannedEffect{}, ErrInvalidConfigurationPlan
	}
	return persist, nil
}

func configurationMutationKeys(
	registry TypedChangeRegistry,
	changes []TypedChange,
) ([]string, error) {
	set := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		definition, ok := registry.Definition(change.Kind)
		if !ok {
			return nil, fmt.Errorf(
				"%w: %s",
				ErrUnknownTypedChange,
				change.Kind,
			)
		}
		set[definition.MutationKey] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, ErrInvalidConfigurationDraft
	}
	return keys, nil
}

func acquireConfigurationMutationKeys(
	ctx context.Context,
	root, profileName, operationID string,
	keys []string,
	coordinator lifecycle.MutationCoordinator,
) (func(), error) {
	configurationMutationOwnersMu.Lock()
	for _, key := range keys {
		lockKey := configurationMutationLockKey(root, profileName, key)
		if owner := configurationMutationOwners[lockKey]; owner != "" {
			configurationMutationOwnersMu.Unlock()
			scopedKey := lifecycle.ProfileMutationKey(profileName, key)
			if scopedKey == "" {
				scopedKey = key
			}
			return nil, &ConfigurationMutationConflictError{
				Key: scopedKey, OwnerOperationID: owner,
				OwnerKind:  lifecycle.MutationOwnerConfiguration,
				OwnerPhase: lifecycle.MutationPhaseApplying,
				Recovery:   "inspect the owning operation and retry after it reaches a terminal phase",
			}
		}
	}
	for _, key := range keys {
		lockKey := configurationMutationLockKey(root, profileName, key)
		configurationMutationOwners[lockKey] = operationID
	}
	configurationMutationOwnersMu.Unlock()
	releaseLocal := func() {
		configurationMutationOwnersMu.Lock()
		defer configurationMutationOwnersMu.Unlock()
		for _, key := range keys {
			lockKey := configurationMutationLockKey(root, profileName, key)
			if configurationMutationOwners[lockKey] == operationID {
				delete(configurationMutationOwners, lockKey)
			}
		}
	}
	if coordinator == nil {
		return releaseLocal, nil
	}
	scopedKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		scoped := lifecycle.ProfileMutationKey(profileName, key)
		if scoped == "" {
			releaseLocal()
			return nil, ErrInvalidConfigurationPlan
		}
		scopedKeys = append(scopedKeys, scoped)
	}
	lease, err := coordinator.AcquireMutation(ctx, lifecycle.MutationRequest{
		Keys: scopedKeys,
		Owner: lifecycle.MutationOwner{
			Kind: lifecycle.MutationOwnerConfiguration,
			ID:   operationID, Phase: lifecycle.MutationPhaseApplying,
			Recovery: "inspect the owning configuration operation and retry after it reaches a terminal phase",
		},
	})
	if err != nil {
		releaseLocal()
		var conflict *lifecycle.MutationConflictError
		if errors.As(err, &conflict) {
			return nil, &ConfigurationMutationConflictError{
				Key: conflict.Key, OwnerOperationID: conflict.Owner.ID,
				OwnerKind:  conflict.Owner.Kind,
				OwnerPhase: conflict.Owner.Phase,
				Recovery:   conflict.Owner.Recovery,
			}
		}
		return nil, err
	}
	return func() {
		lease.Release()
		releaseLocal()
	}, nil
}

func configurationMutationLockKey(root, profileName, key string) string {
	return root + "\x00" + profileName + "\x00" + key
}

func acquireProfileTransactionApplyLock(key string) func() {
	profileTransactionApplyLocksMu.Lock()
	entry := profileTransactionApplyLocks[key]
	if entry == nil {
		entry = &profileTransactionApplyLock{}
		profileTransactionApplyLocks[key] = entry
	}
	entry.refs++
	profileTransactionApplyLocksMu.Unlock()

	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()
		profileTransactionApplyLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(profileTransactionApplyLocks, key)
		}
		profileTransactionApplyLocksMu.Unlock()
	}
}

func operationBindingForConfigurationPlan(
	plan ConfigurationPlan,
) OperationBinding {
	return OperationBinding{
		ID: plan.OperationID, Kind: profileTransactionOperationKind,
		Owner:      OperationOwner{Kind: "profile", ID: plan.Profile},
		PlanDigest: plan.PlanDigest, BaseRevision: plan.BaseRevision,
	}
}

func operationEffectsForConfigurationPlan(
	plan ConfigurationPlan,
) []EffectResult {
	effects := make([]EffectResult, 0, len(plan.Effects))
	for _, effect := range plan.Effects {
		effects = append(effects, EffectResult{
			ID: effect.ID, Kind: effect.Kind,
			Provider: effect.Provider, Status: EffectPending,
		})
	}
	return effects
}

func cloneTypedChanges(changes []TypedChange) []TypedChange {
	cloned := make([]TypedChange, len(changes))
	for index := range changes {
		cloned[index] = TypedChange{
			Kind:  changes[index].Kind,
			Value: append(json.RawMessage(nil), changes[index].Value...),
		}
	}
	return cloned
}

func (service *ProfileTransactionService) registry() TypedChangeRegistry {
	if len(service.Registry.definitions) == 0 {
		return DefaultTypedChangeRegistry()
	}
	return service.Registry
}

func (service *ProfileTransactionService) operationStore() OperationStore {
	store := service.Operations
	if strings.TrimSpace(store.Root) == "" {
		store.Root = service.Core.Store.Root
	}
	if store.Now == nil {
		store.Now = service.now
	}
	return store
}

func (service *ProfileTransactionService) operations() OperationService {
	return OperationService{
		Store: service.operationStore(), Observer: service.Core.Observer,
	}
}

func (service *ProfileTransactionService) projections() ProfileProjectionService {
	return ProfileProjectionService{
		Store: service.Core.Store, Now: service.now,
	}
}

func (service *ProfileTransactionService) nowUTC() time.Time {
	if service.now != nil {
		return service.now().Round(0).UTC()
	}
	return time.Now().Round(0).UTC()
}

func (service *ProfileTransactionService) planTTL() time.Duration {
	if service.PlanTTL > 0 {
		return service.PlanTTL
	}
	return defaultConfigurationPlanTTL
}

func checkProfileTransactionContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (service *ProfileTransactionService) planPath(
	operationID string,
) string {
	return filepath.Join(
		service.Core.Store.Root,
		"operations",
		"configuration-plans",
		operationID+".json",
	)
}

func (service *ProfileTransactionService) writePlanRecord(
	record profileTransactionPlanRecord,
) error {
	if err := service.validatePlanRecord(record); err != nil {
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
	if len(data) > maxConfigurationPlanRecordBytes {
		return errors.New("configuration plan record exceeds size bound")
	}
	path := service.planPath(record.Plan.OperationID)
	if _, err := os.Lstat(path); err == nil {
		return ErrOperationMismatch
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(dir, ".configuration-plan-*.tmp")
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

func (service *ProfileTransactionService) loadPlanRecord(
	operationID string,
) (profileTransactionPlanRecord, error) {
	if !operationIDPattern.MatchString(operationID) {
		return profileTransactionPlanRecord{}, ErrInvalidConfigurationPlan
	}
	path := service.planPath(operationID)
	info, err := os.Lstat(path)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 ||
		info.Size() > maxConfigurationPlanRecordBytes {
		return profileTransactionPlanRecord{}, errors.New(
			"configuration plan record must be a bounded private regular file",
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return profileTransactionPlanRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record profileTransactionPlanRecord
	if err := decoder.Decode(&record); err != nil {
		return profileTransactionPlanRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return profileTransactionPlanRecord{}, errors.New(
				"configuration plan record contains trailing data",
			)
		}
		return profileTransactionPlanRecord{}, err
	}
	if record.Plan.OperationID != operationID {
		return profileTransactionPlanRecord{}, ErrOperationMismatch
	}
	if err := service.validatePlanRecord(record); err != nil {
		return profileTransactionPlanRecord{}, err
	}
	return record, nil
}

func (service *ProfileTransactionService) validatePlanRecord(
	record profileTransactionPlanRecord,
) error {
	if record.Schema != profileTransactionPlanRecordSchema ||
		!profileDigestPattern.MatchString(record.TargetDigest) ||
		!profileDigestPattern.MatchString(record.RecordDigest) {
		return ErrInvalidConfigurationPlan
	}
	if err := record.Plan.VerifyDigest(); err != nil {
		return err
	}
	hasNetworkTransitions := len(record.NetworkTransitions) != 0
	if hasNetworkTransitions !=
		(record.BaseNetwork != nil &&
			record.DesiredNetwork != nil) ||
		!hasNetworkTransitions &&
			(record.BaseNetwork != nil ||
				record.DesiredNetwork != nil) {
		return ErrInvalidConfigurationPlan
	}
	if hasNetworkTransitions &&
		(!validStoredProfileNetwork(*record.BaseNetwork) ||
			!validStoredProfileNetwork(*record.DesiredNetwork) ||
			!onlyNetworkTypedChanges(record.PrivateChanges)) {
		return ErrInvalidConfigurationPlan
	}
	if record.Desired.Name != "" {
		if record.Desired.Validate() != nil ||
			record.Desired.Name != record.Plan.Profile {
			return ErrInvalidConfigurationPlan
		}
		targetDigest, digestErr := CanonicalDigest(
			CanonicalDomainProfileProjection,
			record.Desired,
		)
		if digestErr != nil || targetDigest != record.TargetDigest ||
			hasNetworkTransitions &&
				record.Desired.Network != *record.DesiredNetwork {
			return ErrInvalidConfigurationPlan
		}
	}
	if err := validateProfileNetworkTransitionPlans(
		record.NetworkTransitions,
		record.Plan.Effects,
	); err != nil {
		return err
	}
	registry := service.registry()
	normalized, err := registry.NormalizeDraft(ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      record.Plan.Profile,
		BaseRevision: record.Plan.BaseRevision,
		Changes:      record.PrivateChanges,
	})
	if err != nil ||
		!rawChangesEqual(normalized.Changes, record.PrivateChanges) {
		return ErrInvalidConfigurationPlan
	}
	reviewed, err := registry.ReviewChanges(record.PrivateChanges)
	if err != nil ||
		!rawChangesEqual(reviewed, record.Plan.CanonicalChanges) {
		return ErrInvalidConfigurationPlan
	}
	expectedRecordDigest, err := digestProfileTransactionPlanRecord(record)
	if err != nil || expectedRecordDigest != record.RecordDigest {
		return ErrInvalidConfigurationPlan
	}
	keys, err := configurationMutationKeys(
		registry,
		record.PrivateChanges,
	)
	if err != nil || !equalStrings(keys, record.MutationKeys) {
		return ErrInvalidConfigurationPlan
	}
	return nil
}

func digestProfileTransactionPlanRecord(
	record profileTransactionPlanRecord,
) (string, error) {
	return CanonicalDigest(
		profileTransactionRecordDomain,
		profileTransactionPlanAuthority{
			Schema:         record.Schema,
			Plan:           record.Plan,
			PrivateChanges: cloneTypedChanges(record.PrivateChanges),
			BaseNetwork:    cloneProfileNetwork(record.BaseNetwork),
			DesiredNetwork: cloneProfileNetwork(record.DesiredNetwork),
			TargetDigest:   record.TargetDigest,
			MutationKeys:   append([]string(nil), record.MutationKeys...),
			NetworkTransitions: append(
				[]NetworkTransitionPlan(nil),
				record.NetworkTransitions...,
			),
		},
	)
}

func ensurePrivateConfigurationPlanDirectory(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil &&
		!errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 ||
		!info.IsDir() ||
		info.Mode().Perm() != 0o700 {
		return errors.New(
			"configuration plan directory must be a private real directory",
		)
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
