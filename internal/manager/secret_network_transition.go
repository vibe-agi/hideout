package manager

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

var errSecretLiveProviderUncommitted = errors.New(
	"secret provider proved the live rotation was not committed",
)

type secretNetworkTransitionReview struct {
	Plans        []NetworkTransitionPlan
	Profiles     []string
	Environments []string
	Blockers     []Blocker
	Warnings     []Warning
}

// planSecretNetworkTransitions discovers both stored profile references and
// authoritative live gateway routes. A rotate can update the latter online;
// deleting a referenced secret fails closed because a later attach would
// otherwise lose its reviewed network dependency.
func planSecretNetworkTransitions(
	ctx context.Context,
	core Core,
	coordinator *ProfileNetworkTransitionCoordinator,
	draft SecretDraft,
	nextGeneration uint64,
) (secretNetworkTransitionReview, error) {
	review := secretNetworkTransitionReview{
		Plans:        []NetworkTransitionPlan{},
		Profiles:     []string{},
		Environments: []string{},
		Blockers:     []Blocker{},
		Warnings:     []Warning{},
	}
	summaries, profileErrors := core.profileSummaries()
	if len(profileErrors) != 0 {
		return review, errors.Join(profileErrors...)
	}
	profileSet := make(map[string]struct{})
	for _, summary := range summaries {
		if summary.NetworkMode != profile.NetworkModeTun2Socks ||
			summary.ProxySecretRef != draft.Ref {
			continue
		}
		review.Profiles = append(review.Profiles, summary.Name)
		profileSet[summary.Name] = struct{}{}
		if draft.Action == secrets.ActionDelete {
			review.Blockers = append(review.Blockers, Blocker{
				Code:     "secret-referenced-by-profile",
				Resource: "profile:" + summary.Name,
				Phase:    "planning",
				Summary: "Profile " + summary.Name +
					" still selects this proxy secret.",
				Recovery: "Move the profile to direct mode or another proxy secret, then review deletion again.",
			})
		}
	}
	if len(profileSet) == 0 {
		return review, nil
	}

	records, err := (environment.Store{Root: core.Store.Root}).List()
	if err != nil {
		return review, err
	}
	if coordinator == nil || coordinator.Provider == nil {
		if draft.Action == secrets.ActionRotate {
			review.Warnings = append(review.Warnings, Warning{
				Code:    "secret-live-routes-not-observed",
				Summary: "Referenced profiles will use the new generation on their next eligible attach; no live gateway authority is available to move them online now.",
			})
		}
		return review, nil
	}

	for _, record := range records {
		if record.Status == environment.StatusUnsupportedVersion ||
			record.Disposable {
			continue
		}
		if _, affected := profileSet[record.Profile]; !affected {
			continue
		}
		observed, observeErr :=
			coordinator.Provider.ObserveNetworkRoute(ctx, record.ID)
		if observeErr != nil {
			continue
		}
		if observed.Validate() != nil {
			return review, ErrInvalidNetworkTransition
		}
		if observed.Mode != netpolicy.ModeTun2Socks ||
			observed.ProxySecretRef != draft.Ref {
			continue
		}
		review.Environments = append(
			review.Environments,
			record.ID,
		)
		if draft.Action == secrets.ActionDelete {
			review.Blockers = append(review.Blockers, Blocker{
				Code:     "secret-used-by-live-route",
				Resource: "environment:" + record.ID,
				Phase:    "planning",
				Summary: "Environment " + record.ID +
					" is actively routed through this proxy secret.",
				Recovery: "Move its profile to direct mode or another proxy and prove the live transition before deleting the secret.",
			})
			continue
		}
		if draft.Action != secrets.ActionRotate {
			continue
		}
		desired := observed
		desired.ProxySecretGeneration = nextGeneration
		plan, planErr := (NetworkTransitionService{
			Provider: coordinator.Provider,
			Sessions: coordinator.Sessions,
		}).Plan(ctx, NetworkTransitionDraft{
			EnvironmentID: record.ID,
			Desired:       desired,
		})
		switch {
		case errors.Is(planErr, ErrNetworkTransitionNoChange):
			continue
		case planErr != nil:
			return review, planErr
		}
		if plan.Kind != NetworkTransitionRoute ||
			len(plan.Blockers) != 0 {
			review.Blockers = append(
				review.Blockers,
				plan.Blockers...,
			)
			if len(plan.Blockers) == 0 {
				review.Blockers = append(review.Blockers, Blocker{
					Code:     "secret-live-route-transition-unavailable",
					Resource: "environment:" + record.ID,
					Phase:    "planning",
					Summary:  "The live route cannot stage this secret generation safely.",
					Recovery: "Finish the active environment and rotate the secret for its next attach.",
				})
			}
			continue
		}
		review.Plans = append(review.Plans, plan)
	}
	sort.Strings(review.Profiles)
	sort.Strings(review.Environments)
	sortProfileNetworkTransitionPlans(review.Plans)
	if len(review.Plans) != 0 {
		if _, available := coordinator.Provider.(NetworkTransitionBatchProvider); !available {
			review.Blockers = append(review.Blockers, Blocker{
				Code:     "secret-live-route-batch-unavailable",
				Phase:    "planning",
				Summary:  "The gateway provider cannot coordinate the secret commit with complete live-route commit or restoration.",
				Recovery: "Finish affected live environments or use a provider with coordinated network transitions.",
			})
			review.Plans = []NetworkTransitionPlan{}
		}
	}
	if draft.Action == secrets.ActionRotate &&
		len(review.Environments) != 0 &&
		len(review.Plans) == 0 &&
		len(review.Blockers) == 0 {
		review.Warnings = append(review.Warnings, Warning{
			Code:    "secret-live-routes-already-current",
			Summary: "Observed live routes already identify the reviewed next generation.",
		})
	}
	sort.Slice(review.Blockers, func(left, right int) bool {
		if review.Blockers[left].Code != review.Blockers[right].Code {
			return review.Blockers[left].Code <
				review.Blockers[right].Code
		}
		return review.Blockers[left].Resource <
			review.Blockers[right].Resource
	})
	sort.Slice(review.Warnings, func(left, right int) bool {
		return review.Warnings[left].Code <
			review.Warnings[right].Code
	})
	return review, nil
}

func (service *SecretService) applyWithLiveNetworkTransitions(
	ctx context.Context,
	request SecretApplyRequest,
	record secretPlanRecord,
	operation Operation,
	secretEffect PlannedEffect,
) (SecretApplyResult, error) {
	if service == nil ||
		service.NetworkTransitions == nil ||
		service.NetworkTransitions.Provider == nil ||
		record.Plan.Action != secrets.ActionRotate ||
		request.Value == nil {
		return SecretApplyResult{Operation: operation},
			ErrSecretRecoveryRequired
	}
	batchProvider, ok := service.NetworkTransitions.Provider.(NetworkTransitionBatchProvider)
	if !ok {
		return SecretApplyResult{Operation: operation},
			ErrSecretProviderUnavailable
	}
	var (
		materialValue string
		providerValue *secrets.Buffer
	)
	if err := request.Value.Use(func(raw []byte) error {
		var err error
		providerValue, err = secrets.NewBuffer(raw)
		if err != nil {
			return err
		}
		materialValue = string(raw)
		return nil
	}); err != nil {
		return SecretApplyResult{Operation: operation}, err
	}
	defer func() {
		materialValue = ""
		if providerValue != nil {
			providerValue.Clear()
		}
	}()

	operations := service.operations()
	var committed secrets.Reference
	beforeCommit := func(commitCtx context.Context) error {
		current, execute, err := operations.BeginEffect(
			record.Plan.OperationID,
			secretEffect.ID,
			secretEffect.Provider,
		)
		if err != nil {
			return err
		}
		switch {
		case execute:
			committed, err = service.executeProvider(
				commitCtx,
				providerValue,
				record.Plan,
			)
			if err == nil {
				current, err = operations.FinishEffect(
					current.ID,
					secretEffect.ID,
					secretEffect.Provider,
					EffectSucceeded,
					secretGenerationEvidence(committed),
				)
				operation = current
				return err
			}
			return service.reconcileLiveSecretProviderFailure(
				commitCtx,
				record.Plan,
				secretEffect,
				err,
				&operation,
				&committed,
			)
		case effectStatus(current, secretEffect.ID) ==
			EffectRunning:
			return service.reconcileLiveSecretProviderFailure(
				commitCtx,
				record.Plan,
				secretEffect,
				ErrSecretRecoveryRequired,
				&operation,
				&committed,
			)
		case effectStatus(current, secretEffect.ID) ==
			EffectSucceeded:
			committed, err = service.Store.Reference(
				commitCtx,
				record.Plan.Ref,
			)
			operation = current
			return err
		default:
			operation = current
			return ErrSecretRecoveryRequired
		}
	}

	checkpoint := &profileNetworkOperationCheckpoint{
		operationID: record.Plan.OperationID,
		operations:  operations,
	}
	results, applyErr := applyProfileNetworkTransitionBatch(
		ctx,
		NetworkTransitionService{
			Provider:    service.NetworkTransitions.Provider,
			Sessions:    service.NetworkTransitions.Sessions,
			Checkpoints: checkpoint,
		},
		batchProvider,
		record.NetworkTransitions,
		NetworkCandidateMaterial{
			UpstreamProxyURL: materialValue,
		},
		beforeCommit,
	)
	var (
		syncErr error
		err     error
	)
	for index := range results {
		operation, err = syncProfileNetworkTransitionResult(
			operations,
			record.Plan.OperationID,
			record.NetworkTransitions[index],
			results[index],
		)
		syncErr = errors.Join(syncErr, err)
	}
	if syncErr != nil {
		recovered, recoveryErr := operations.RequireRecovery(
			record.Plan.OperationID,
			"secret-network-evidence-unproved",
			"The secret or live-route effect completed without durable synchronized evidence.",
			"Restart the daemon and inspect the same operation before another secret mutation.",
		)
		if recoveryErr == nil {
			operation = recovered
		}
		return SecretApplyResult{
				Operation: operation,
				Reference: committed,
			},
			errors.Join(
				ErrSecretRecoveryRequired,
				applyErr,
				syncErr,
				recoveryErr,
			)
	}
	operation, err = service.operationStore().Load(
		record.Plan.OperationID,
	)
	if err != nil {
		return SecretApplyResult{}, err
	}
	if applyErr != nil {
		return service.finishFailedLiveSecretTransition(
			ctx,
			record,
			operation,
			applyErr,
		)
	}
	if committed.Ref == "" {
		committed, err = service.Store.Reference(
			ctx,
			record.Plan.Ref,
		)
		if err != nil {
			return SecretApplyResult{Operation: operation}, err
		}
	}
	if committed.Generation != record.Plan.NextGeneration ||
		committed.Availability !=
			record.Plan.NextAvailability ||
		effectStatus(operation, secretEffect.ID) !=
			EffectSucceeded {
		return service.requireSecretRecovery(
			operation,
			"secret generation or live-route commit is not proved",
		)
	}
	operation, err = operations.Terminal(
		operation.ID,
		OperationSucceeded,
		"secret-generation-and-live-routes-committed",
		"The reviewed secret generation and every eligible live route were committed.",
	)
	if err != nil {
		return SecretApplyResult{}, err
	}
	result := SecretApplyResult{
		Operation: operation,
		Reference: committed,
	}
	_ = service.removePlanRecord(operation.ID)
	if service.hooks.afterTerminal != nil {
		if err := service.hooks.afterTerminal(operation); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (service *SecretService) reconcileLiveSecretProviderFailure(
	ctx context.Context,
	plan SecretPlan,
	effect PlannedEffect,
	providerErr error,
	operation *Operation,
	reference *secrets.Reference,
) error {
	if service.Reconciler == nil {
		return errors.Join(
			ErrSecretRecoveryRequired,
			providerErr,
		)
	}
	reconciled, err := service.Reconciler.Reconcile(
		ctx,
		secrets.ReconcileRequest{
			Ref: plan.Ref, Action: plan.Action,
			OperationID:        plan.OperationID,
			ExpectedGeneration: plan.BaseGeneration,
		},
	)
	if err != nil {
		return errors.Join(
			ErrSecretRecoveryRequired,
			providerErr,
			err,
		)
	}
	switch {
	case reconciled.Committed && !reconciled.Uncommitted:
		updated, finishErr := service.operations().FinishEffect(
			plan.OperationID,
			effect.ID,
			effect.Provider,
			EffectSucceeded,
			secretGenerationEvidence(reconciled.Reference),
		)
		if operation != nil {
			*operation = updated
		}
		if reference != nil {
			*reference = reconciled.Reference
		}
		return finishErr
	case reconciled.Uncommitted && !reconciled.Committed &&
		sameSecretPlanBase(plan.Current, reconciled.Reference):
		updated, finishErr := service.operations().FinishEffect(
			plan.OperationID,
			effect.ID,
			effect.Provider,
			EffectRolledBack,
			secretGenerationUnchangedEvidence(
				reconciled.Reference,
			),
		)
		if operation != nil {
			*operation = updated
		}
		if reference != nil {
			*reference = reconciled.Reference
		}
		return errors.Join(
			errSecretLiveProviderUncommitted,
			providerErr,
			finishErr,
		)
	default:
		return errors.Join(
			ErrSecretRecoveryRequired,
			providerErr,
		)
	}
}

func (service *SecretService) finishFailedLiveSecretTransition(
	ctx context.Context,
	record secretPlanRecord,
	operation Operation,
	applyErr error,
) (SecretApplyResult, error) {
	reference, referenceErr := service.Store.Reference(
		ctx,
		record.Plan.Ref,
	)
	secretEffect, effectErr := secretProviderEffect(
		record.Plan,
		service.Store.Provider(),
	)
	if effectErr != nil {
		return SecretApplyResult{Operation: operation},
			errors.Join(applyErr, referenceErr, effectErr)
	}
	secretStatus := effectStatus(operation, secretEffect.ID)
	if secretStatus == EffectSucceeded ||
		secretStatus == EffectRunning {
		recovered, recoveryErr := service.operations().RequireRecovery(
			operation.ID,
			"secret-committed-live-route-unproved",
			"The Keychain generation may be committed, but every live route commit is not proved.",
			"Restart the daemon and reconcile this operation before another secret mutation.",
		)
		if recoveryErr == nil {
			operation = recovered
		}
		return SecretApplyResult{
				Operation: operation,
				Reference: reference,
			},
			errors.Join(
				ErrSecretRecoveryRequired,
				applyErr,
				referenceErr,
				recoveryErr,
			)
	}

	terminalPhase := OperationFailed
	code := "secret-live-transition-failed"
	summary := "The live route transition failed before the secret generation changed."
	for _, effect := range operation.Effects {
		switch effect.Status {
		case EffectUnproved:
			terminalPhase = OperationRollbackUnproved
			code = "secret-live-transition-rollback-unproved"
			summary = "The secret generation did not change, but restoration of every live route could not be proved."
		case EffectRolledBack:
			if terminalPhase != OperationRollbackUnproved {
				terminalPhase = OperationRolledBack
				code = "secret-live-transition-rolled-back"
				summary = "The secret generation stayed unchanged and every staged live route was restored."
			}
		}
	}
	operations := service.operations()
	if terminalPhase == OperationRolledBack ||
		terminalPhase == OperationRollbackUnproved {
		if operation.Phase != OperationRollingBack {
			operation, effectErr = operations.Transition(
				operation.ID,
				OperationRollingBack,
			)
		}
	}
	if effectErr == nil {
		operation, effectErr = operations.Terminal(
			operation.ID,
			terminalPhase,
			code,
			summary,
		)
	}
	if effectErr == nil &&
		terminalPhase != OperationRollbackUnproved {
		_ = service.removePlanRecord(operation.ID)
	}
	return SecretApplyResult{
			Operation: operation,
			Reference: reference,
		},
		errors.Join(applyErr, referenceErr, effectErr)
}

func secretGenerationUnchangedEvidence(
	reference secrets.Reference,
) []EvidenceRef {
	return []EvidenceRef{{
		Code: "secret-generation-unchanged",
		Ref: "secret:" + reference.Ref + "@generation:" +
			fmt.Sprintf("%d", reference.Generation),
		ObservedAt: reference.UpdatedAt,
	}}
}
