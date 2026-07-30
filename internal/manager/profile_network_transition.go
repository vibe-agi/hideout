package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

var errProfileNetworkEffectNeedsRecovery = errors.New(
	"profile network effect requires reconciliation",
)

// NetworkSecretReferenceProvider exposes only non-secret availability and
// generation metadata needed to bind a reviewed live route transition.
type NetworkSecretReferenceProvider interface {
	NetworkSecretReference(
		context.Context,
		string,
	) (secrets.Reference, error)
}

// ProfileNetworkTransitionCoordinator discovers live reusable environments
// for one profile and drives their gateway route transitions through the same
// durable operation as the profile mutation.
type ProfileNetworkTransitionCoordinator struct {
	Core             Core
	Provider         NetworkTransitionProvider
	Sessions         NetworkTransitionSessionInventory
	SecretReferences NetworkSecretReferenceProvider
	SecretResolver   netpolicy.DetailedSecretResolver
	RecoveryProvider NetworkTransitionRecoveryProvider
	// Checkpoints observes a live provider boundary only after the canonical
	// operation envelope has durably entered or completed that effect. It is
	// intentionally optional and carries no provider or secret authority.
	Checkpoints NetworkTransitionEffectCheckpoint
}

type profileNetworkTransitionReview struct {
	Plans    []NetworkTransitionPlan
	Blockers []Blocker
	Warnings []Warning
}

func (coordinator *ProfileNetworkTransitionCoordinator) Plan(
	ctx context.Context,
	current profile.Profile,
	desired profile.Profile,
) (profileNetworkTransitionReview, error) {
	if coordinator == nil || coordinator.Provider == nil {
		return profileNetworkTransitionReview{}, nil
	}
	if current.Name != desired.Name ||
		current.Validate() != nil ||
		desired.Validate() != nil {
		return profileNetworkTransitionReview{},
			ErrInvalidConfigurationPlan
	}
	if current.Network == desired.Network {
		return profileNetworkTransitionReview{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := (environment.Store{
		Root: coordinator.Core.Store.Root,
	}).List()
	if err != nil {
		return profileNetworkTransitionReview{}, err
	}
	type liveEnvironment struct {
		id      string
		current NetworkRouteConfiguration
	}
	live := make([]liveEnvironment, 0, len(records))
	for _, record := range records {
		if record.Status == environment.StatusUnsupportedVersion ||
			record.Disposable ||
			record.Profile != current.Name {
			continue
		}
		observed, observeErr := coordinator.Provider.ObserveNetworkRoute(
			ctx,
			record.ID,
		)
		if observeErr != nil {
			continue
		}
		if observed.Validate() != nil {
			return profileNetworkTransitionReview{},
				ErrInvalidNetworkTransition
		}
		live = append(live, liveEnvironment{
			id:      record.ID,
			current: observed,
		})
	}
	sort.Slice(live, func(left, right int) bool {
		return live[left].id < live[right].id
	})
	if len(live) == 0 {
		return profileNetworkTransitionReview{}, nil
	}

	desiredRoute, available, err := coordinator.desiredRoute(
		ctx,
		desired,
	)
	if err != nil {
		return profileNetworkTransitionReview{}, err
	}
	review := profileNetworkTransitionReview{
		Plans:    []NetworkTransitionPlan{},
		Blockers: []Blocker{},
		Warnings: []Warning{},
	}
	dnsMustDefer := false
	dnsPending := make(map[string]struct{})
	warnDNSPending := func(environmentID, reason string) {
		if _, exists := dnsPending[environmentID]; exists {
			return
		}
		dnsPending[environmentID] = struct{}{}
		summary := "Environment " + environmentID +
			" keeps its current resolver until the next eligible attach."
		if reason != "" {
			summary = "Environment " + environmentID + " " + reason +
				"; its resolver change remains pending for the next eligible attach."
		}
		review.Warnings = append(review.Warnings, Warning{
			Code:    "network-dns-pending-next-attach",
			Summary: summary,
		})
	}
	if !available {
		for _, item := range live {
			review.Blockers = append(review.Blockers, Blocker{
				Code:     "network-secret-unavailable",
				Resource: "environment:" + item.id,
				Phase:    "planning",
				Summary:  "The selected proxy secret is unavailable, so the live route cannot be staged.",
				Recovery: "Store or unlock " + desired.Network.ProxySecretRef + " with hideout secret, then review this change again.",
			})
		}
		return review, nil
	}

	for _, item := range live {
		transitionDesired := desiredRoute
		// A proxy reference/generation can move online independently of a DNS
		// change. Keep the guest resolver unchanged for this route transaction
		// and disclose that the DNS portion remains next-attach work.
		routeChanged := item.current.Mode == netpolicy.ModeTun2Socks &&
			transitionDesired.Mode == netpolicy.ModeTun2Socks &&
			(item.current.ProxySecretRef !=
				transitionDesired.ProxySecretRef ||
				item.current.ProxySecretGeneration !=
					transitionDesired.ProxySecretGeneration)
		if routeChanged &&
			item.current.MediatedResolver !=
				transitionDesired.MediatedResolver {
			transitionDesired.MediatedResolver =
				item.current.MediatedResolver
			warnDNSPending(
				item.id,
				"can switch its proxy route online",
			)
		}
		plan, planErr := (NetworkTransitionService{
			Provider: coordinator.Provider,
			Sessions: coordinator.Sessions,
		}).Plan(ctx, NetworkTransitionDraft{
			EnvironmentID: item.id,
			Desired:       transitionDesired,
		})
		switch {
		case errors.Is(planErr, ErrNetworkTransitionNoChange):
			continue
		case planErr != nil:
			return profileNetworkTransitionReview{}, planErr
		}
		if plan.Kind == NetworkTransitionPosture {
			summary := "Environment " + item.id +
				" remains on its current effective network posture; the desired posture will be revalidated and applied by the next eligible attach."
			if len(plan.Blockers) != 0 {
				summary = "Environment " + item.id +
					" has active or unprovable session ownership, so existing sessions remain on their current network posture; the desired posture waits for the next eligible attach."
			}
			review.Warnings = append(review.Warnings, Warning{
				Code:    "network-posture-pending-next-attach",
				Summary: summary,
			})
			continue
		}
		if plan.Kind == NetworkTransitionDNS {
			capabilities, ok := coordinator.Provider.(NetworkTransitionCapabilityProvider)
			if !ok ||
				!capabilities.SupportsNetworkTransitionKind(
					NetworkTransitionDNS,
				) {
				dnsMustDefer = true
				warnDNSPending(
					item.id,
					"has no live DNS controller",
				)
				continue
			}
			if availability, ok := coordinator.Provider.(NetworkTransitionAvailabilityProvider); ok {
				if availability.NetworkTransitionAvailable(
					ctx,
					plan,
				) != nil {
					dnsMustDefer = true
					warnDNSPending(
						item.id,
						"has no eligible daemon-owned runtime",
					)
					continue
				}
			}
		}
		review.Plans = append(review.Plans, plan)
	}
	dnsPlans := 0
	for _, plan := range review.Plans {
		if plan.Kind == NetworkTransitionDNS {
			dnsPlans++
		}
	}
	if dnsPlans > 1 {
		dnsMustDefer = true
	}
	if dnsMustDefer && dnsPlans != 0 {
		retained := review.Plans[:0]
		for _, plan := range review.Plans {
			if plan.Kind == NetworkTransitionDNS {
				warnDNSPending(
					plan.EnvironmentID,
					"cannot join one fully restorable live DNS batch",
				)
				continue
			}
			retained = append(retained, plan)
		}
		review.Plans = retained
	}
	if len(review.Plans) > 1 {
		if _, available := coordinator.Provider.(NetworkTransitionBatchProvider); !available {
			review.Blockers = append(review.Blockers, Blocker{
				Code:     "network-multi-environment-transition-unavailable",
				Phase:    "planning",
				Summary:  "More than one live environment needs the same route change, but this provider cannot commit or restore the complete set.",
				Recovery: "Finish active work in all but one affected environment, or use a provider with coordinated batch transitions.",
			})
			review.Plans = []NetworkTransitionPlan{}
		}
	}
	return review, nil
}

func (coordinator *ProfileNetworkTransitionCoordinator) desiredRoute(
	ctx context.Context,
	desired profile.Profile,
) (NetworkRouteConfiguration, bool, error) {
	switch desired.Network.Mode {
	case profile.NetworkModeDirect:
		return NetworkRouteConfiguration{
			Mode: netpolicy.ModeDirect,
		}, true, nil
	case profile.NetworkModeTun2Socks:
		if coordinator.SecretReferences == nil {
			return NetworkRouteConfiguration{}, false, nil
		}
		reference, err := coordinator.SecretReferences.
			NetworkSecretReference(
				ctx,
				desired.Network.ProxySecretRef,
			)
		if err != nil {
			if errors.Is(err, secrets.ErrSecretMissing) ||
				errors.Is(err, secrets.ErrSecretLocked) ||
				errors.Is(err, secrets.ErrProviderUnavailable) {
				return NetworkRouteConfiguration{}, false, nil
			}
			return NetworkRouteConfiguration{}, false, err
		}
		if reference.Validate() != nil ||
			reference.Ref != desired.Network.ProxySecretRef ||
			reference.Availability != secrets.AvailabilityAvailable ||
			reference.Generation == 0 {
			return NetworkRouteConfiguration{}, false, nil
		}
		route := NetworkRouteConfiguration{
			Mode:                  netpolicy.ModeTun2Socks,
			ProxySecretRef:        reference.Ref,
			ProxySecretGeneration: reference.Generation,
			MediatedResolver:      desired.Network.MediatedResolver,
		}
		if err := route.Validate(); err != nil {
			return NetworkRouteConfiguration{}, false, err
		}
		return route, true, nil
	default:
		return NetworkRouteConfiguration{}, false,
			ErrInvalidNetworkTransition
	}
}

func (coordinator *ProfileNetworkTransitionCoordinator) Apply(
	ctx context.Context,
	operationID string,
	plans []NetworkTransitionPlan,
	operations OperationService,
) (Operation, *NetworkTransitionResult, error) {
	if len(plans) == 0 {
		operation, err := operations.Store.Load(operationID)
		return operation, nil, err
	}
	if coordinator == nil ||
		coordinator.Provider == nil {
		return Operation{}, nil,
			ErrNetworkTransitionProviderUnavailable
	}
	for index, plan := range plans {
		if err := plan.VerifyDigest(); err != nil {
			return Operation{}, nil, err
		}
		if index != 0 &&
			plans[index-1].EnvironmentID >= plan.EnvironmentID {
			return Operation{}, nil, ErrInvalidNetworkTransition
		}
	}
	operation, err := operations.Store.Load(operationID)
	if err != nil {
		return Operation{}, nil, err
	}
	allPending := true
	allProved := true
	for _, plan := range plans {
		state, stateErr := profileNetworkEffectsState(operation, plan)
		if stateErr != nil {
			return operation, nil, stateErr
		}
		allPending = allPending && state == "pending"
		allProved = allProved && state == "proved"
	}
	if allProved {
		effectiveProved := true
		for _, plan := range plans {
			effective, observeErr := coordinator.Provider.
				ObserveNetworkRoute(ctx, plan.EnvironmentID)
			if observeErr != nil || effective != plan.Desired {
				effectiveProved = false
				break
			}
		}
		if effectiveProved {
			return operation, nil, nil
		}
		return coordinator.recoverProfileNetworkTransitions(
			ctx,
			operation,
			plans,
			operations,
		)
	}
	if !allPending {
		return coordinator.recoverProfileNetworkTransitions(
			ctx,
			operation,
			plans,
			operations,
		)
	}

	first := plans[0]
	if coordinator.SecretResolver == nil {
		return operation, nil,
			ErrNetworkTransitionProviderUnavailable
	}
	if first.Desired.Mode != netpolicy.ModeTun2Socks {
		return operation, nil, ErrNetworkTransitionProviderUnavailable
	}
	for _, plan := range plans[1:] {
		if plan.Desired.ProxySecretRef !=
			first.Desired.ProxySecretRef ||
			plan.Desired.ProxySecretGeneration !=
				first.Desired.ProxySecretGeneration {
			return operation, nil,
				ErrInvalidNetworkTransition
		}
	}

	resolution, err := coordinator.SecretResolver.ResolveSecret(
		first.Desired.ProxySecretRef,
	)
	if err != nil {
		return operation, nil, err
	}
	defer func() {
		resolution.Value = ""
	}()
	if resolution.Source != netpolicy.SecretSourceManaged ||
		resolution.Generation !=
			first.Desired.ProxySecretGeneration {
		return operation, nil, ErrNetworkTransitionStale
	}
	checkpoint := &profileNetworkOperationCheckpoint{
		operationID: operationID,
		operations:  operations,
		next:        coordinator.Checkpoints,
	}
	material := NetworkCandidateMaterial{
		UpstreamProxyURL: resolution.Value,
	}
	var results []NetworkTransitionResult
	var applyErr error
	if len(plans) == 1 {
		result, resultErr := (NetworkTransitionService{
			Provider:    coordinator.Provider,
			Sessions:    coordinator.Sessions,
			Checkpoints: checkpoint,
		}).Apply(ctx, first, material)
		results = []NetworkTransitionResult{result}
		applyErr = resultErr
	} else {
		batchProvider, ok := coordinator.Provider.(NetworkTransitionBatchProvider)
		if !ok {
			return operation, nil,
				ErrNetworkTransitionProviderUnavailable
		}
		results, applyErr = applyProfileNetworkTransitionBatch(
			ctx,
			NetworkTransitionService{
				Provider:    coordinator.Provider,
				Sessions:    coordinator.Sessions,
				Checkpoints: checkpoint,
			},
			batchProvider,
			plans,
			material,
			nil,
		)
	}
	var syncErr error
	for index := range results {
		operation, err = syncProfileNetworkTransitionResult(
			operations,
			operationID,
			plans[index],
			results[index],
		)
		syncErr = errors.Join(syncErr, err)
	}
	if syncErr != nil {
		if applyErr == nil {
			return operation, nil, errors.Join(
				errProfileNetworkEffectNeedsRecovery,
				syncErr,
			)
		}
		return operation,
			profileNetworkTransitionRepresentative(results),
			errors.Join(applyErr, syncErr)
	}
	return operation,
		profileNetworkTransitionRepresentative(results),
		applyErr
}

func (coordinator *ProfileNetworkTransitionCoordinator) recoverProfileNetworkTransitions(
	ctx context.Context,
	operation Operation,
	plans []NetworkTransitionPlan,
	operations OperationService,
) (Operation, *NetworkTransitionResult, error) {
	if coordinator == nil ||
		coordinator.RecoveryProvider == nil ||
		len(plans) != 1 ||
		plans[0].Kind != NetworkTransitionDNS {
		return operation, nil,
			errProfileNetworkEffectNeedsRecovery
	}
	plan := plans[0]
	observation, err := coordinator.RecoveryProvider.
		ReconcileNetworkTransition(
			ctx,
			NetworkTransitionRecoveryRequest{
				OperationID:   operation.ID,
				PlanDigest:    plan.PlanDigest,
				EnvironmentID: plan.EnvironmentID,
				From:          plan.From,
				Desired:       plan.Desired,
			},
		)
	if err != nil ||
		validateProfileNetworkRecoveryObservation(
			observation,
			operation,
			plan,
		) != nil {
		return operation, nil,
			errProfileNetworkEffectNeedsRecovery
	}
	switch observation.Outcome {
	case NetworkRecoveryCommitted:
		operation, err = advanceProfileNetworkRecoveryToProving(
			operations,
			operation,
		)
		if err != nil {
			return operation, nil,
				errProfileNetworkEffectNeedsRecovery
		}
		result := newNetworkTransitionResult(plan)
		for index, observed := range observation.Effects {
			effect := plan.Effects[index]
			operationEffectID := profileNetworkEffectID(
				plan.EnvironmentID,
				effect.ID,
			)
			currentIndex := effectIndex(
				operation.Effects,
				operationEffectID,
			)
			if currentIndex < 0 {
				return operation, nil,
					errProfileNetworkEffectNeedsRecovery
			}
			evidence := networkTransitionEvidence(
				networkRecoveryEvidenceCode(effect.ID),
				plan.EnvironmentID,
				observed.Proof,
			)
			switch operation.Effects[currentIndex].Status {
			case EffectPending:
				var execute bool
				operation, execute, err =
					operations.BeginEffect(
						operation.ID,
						operationEffectID,
						effect.Provider,
					)
				if err != nil || !execute {
					return operation, nil,
						errProfileNetworkEffectNeedsRecovery
				}
				operation, err = operations.FinishEffect(
					operation.ID,
					operationEffectID,
					effect.Provider,
					EffectSucceeded,
					evidence,
				)
			case EffectRunning:
				operation, err = operations.FinishEffect(
					operation.ID,
					operationEffectID,
					effect.Provider,
					EffectSucceeded,
					evidence,
				)
			case EffectSucceeded:
				if len(
					operation.Effects[currentIndex].Evidence,
				) == 0 {
					err = ErrInvalidOperation
				}
			default:
				err = ErrInvalidOperation
			}
			if err != nil {
				return operation, nil,
					errProfileNetworkEffectNeedsRecovery
			}
			setNetworkTransitionEffect(
				&result,
				effect.ID,
				EffectSucceeded,
				evidence,
			)
			if effect.ID == "network-drain" {
				result.ConnectionsRetained =
					observed.Proof.ConnectionsRetained
			}
		}
		result.Phase = NetworkTransitionSucceeded
		result.Effective = plan.Desired
		result.EffectiveProved = true
		result.Recovery = Recovery{
			Code:    "network-transition-recovered",
			Summary: "The exact boot-bound DNS state was proved after restart and its durable effects were completed without replaying activation.",
		}
		return operation, &result, nil
	case NetworkRecoveryRolledBack:
		if observation.RollbackProof == nil {
			return operation, nil,
				errProfileNetworkEffectNeedsRecovery
		}
		rollbackEvidence := networkTransitionEvidence(
			"network-route-restored",
			plan.EnvironmentID,
			*observation.RollbackProof,
		)
		result := newNetworkTransitionResult(plan)
		for index, effect := range plan.Effects {
			operationEffectID := profileNetworkEffectID(
				plan.EnvironmentID,
				effect.ID,
			)
			currentIndex := effectIndex(
				operation.Effects,
				operationEffectID,
			)
			if currentIndex < 0 {
				return operation, nil,
					errProfileNetworkEffectNeedsRecovery
			}
			status := operation.Effects[currentIndex].Status
			switch status {
			case EffectRunning, EffectSucceeded:
				operation, err = operations.FinishEffect(
					operation.ID,
					operationEffectID,
					effect.Provider,
					EffectRolledBack,
					rollbackEvidence,
				)
				if err != nil {
					return operation, nil,
						errProfileNetworkEffectNeedsRecovery
				}
				result.Effects[index].Status =
					EffectRolledBack
				result.Effects[index].Evidence =
					append(
						[]EvidenceRef(nil),
						rollbackEvidence...,
					)
			case EffectPending:
			case EffectRolledBack:
				result.Effects[index].Status =
					EffectRolledBack
				result.Effects[index].Evidence =
					append(
						[]EvidenceRef(nil),
						operation.Effects[currentIndex].
							Evidence...,
					)
			default:
				return operation, nil,
					errProfileNetworkEffectNeedsRecovery
			}
		}
		result.Phase = NetworkTransitionRolledBack
		result.Effective = plan.From
		result.EffectiveProved = true
		result.Recovery = networkTransitionRolledBackRecovery()
		return operation,
			&result,
			newNetworkTransitionApplyError(
				ErrNetworkTransitionRolledBack,
			)
	default:
		return operation, nil,
			errProfileNetworkEffectNeedsRecovery
	}
}

func validateProfileNetworkRecoveryObservation(
	observation NetworkTransitionRecoveryObservation,
	operation Operation,
	plan NetworkTransitionPlan,
) error {
	if observation.OperationID != operation.ID ||
		observation.PlanDigest != plan.PlanDigest ||
		observation.EnvironmentID != plan.EnvironmentID ||
		observation.CandidateRouteGeneration == 0 ||
		len(observation.Effects) > len(plan.Effects) {
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
			) != nil ||
			!lastObservedAt.IsZero() &&
				effect.Proof.ObservedAt.Before(lastObservedAt) {
			return ErrInvalidNetworkTransition
		}
		lastObservedAt = effect.Proof.ObservedAt
	}
	switch observation.Outcome {
	case NetworkRecoveryCommitted:
		if observation.Effective != plan.Desired ||
			len(observation.Effects) != len(plan.Effects) ||
			observation.RollbackProof != nil ||
			observation.ActiveRouteGeneration !=
				observation.CandidateRouteGeneration {
			return ErrInvalidNetworkTransition
		}
	case NetworkRecoveryRolledBack:
		if observation.Effective != plan.From ||
			observation.RollbackProof == nil ||
			validateNetworkTransitionRollbackProof(
				plan,
				*observation.RollbackProof,
			) != nil ||
			observation.ActiveRouteGeneration !=
				observation.CandidateRouteGeneration ||
			(!lastObservedAt.IsZero() &&
				observation.RollbackProof.ObservedAt.Before(
					lastObservedAt,
				)) {
			return ErrInvalidNetworkTransition
		}
	default:
		return ErrInvalidNetworkTransition
	}
	return nil
}

func advanceProfileNetworkRecoveryToProving(
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

type profileNetworkBatchItem struct {
	plan                NetworkTransitionPlan
	result              NetworkTransitionResult
	handle              NetworkStagedCandidate
	candidateGeneration uint64
	activated           bool
}

func applyProfileNetworkTransitionBatch(
	ctx context.Context,
	service NetworkTransitionService,
	provider NetworkTransitionBatchProvider,
	plans []NetworkTransitionPlan,
	material NetworkCandidateMaterial,
	beforeCommit func(context.Context) error,
) ([]NetworkTransitionResult, error) {
	if len(plans) == 0 || provider == nil ||
		service.Provider == nil {
		return nil, ErrNetworkTransitionProviderUnavailable
	}
	items := make([]profileNetworkBatchItem, len(plans))
	for index, plan := range plans {
		items[index] = profileNetworkBatchItem{
			plan:   plan,
			result: newNetworkTransitionResult(plan),
		}
		current, err := service.Provider.ObserveNetworkRoute(
			ctx,
			plan.EnvironmentID,
		)
		if err != nil {
			return profileNetworkBatchResults(items),
				newNetworkTransitionProviderError("observe", err)
		}
		if current != plan.From {
			items[index].result.Recovery = Recovery{
				Code:       "network-transition-stale",
				Summary:    "The effective route changed after this transition was reviewed.",
				NextAction: "Review a fresh configuration plan before retrying.",
			}
			return profileNetworkBatchResults(items),
				ErrNetworkTransitionStale
		}
		if blockers := service.postureBlockers(
			ctx,
			plan.EnvironmentID,
			plan.Kind,
		); len(blockers) != 0 {
			items[index].result.Phase = NetworkTransitionBlocked
			items[index].result.Blockers = blockers
			items[index].result.Recovery =
				networkTransitionBlockedRecovery()
			return profileNetworkBatchResults(items),
				ErrNetworkTransitionBlocked
		}
		if err := validateNetworkCandidateMaterial(
			plan.Desired,
			material,
		); err != nil {
			return profileNetworkBatchResults(items), err
		}
	}

	for index := range items {
		item := &items[index]
		if err := service.beforeNetworkTransitionEffect(
			ctx,
			item.plan,
			"network-stage",
		); err != nil {
			return rollbackProfileNetworkTransitionBatch(
				ctx, service, provider, items, err,
			)
		}
		handle, proof, stageErr :=
			service.Provider.StageNetworkCandidate(
				ctx,
				item.plan,
				material,
			)
		item.handle = handle
		item.candidateGeneration = proof.RouteGeneration
		if stageErr != nil {
			setNetworkTransitionEffect(
				&item.result,
				"network-stage",
				EffectFailed,
				nil,
			)
			stageErr = errors.Join(
				stageErr,
				service.afterNetworkTransitionEffect(
					ctx,
					item.plan,
					networkTransitionResultEffect(
						item.result,
						"network-stage",
					),
				),
			)
			return rollbackProfileNetworkTransitionBatch(
				ctx,
				service,
				provider,
				items,
				stageErr,
			)
		}
		if handle == nil {
			setNetworkTransitionEffect(
				&item.result,
				"network-stage",
				EffectUnproved,
				nil,
			)
			stageErr = errors.Join(
				ErrNetworkTransitionRollbackUnproved,
				service.afterNetworkTransitionEffect(
					ctx,
					item.plan,
					networkTransitionResultEffect(
						item.result,
						"network-stage",
					),
				),
			)
			return rollbackProfileNetworkTransitionBatch(
				ctx,
				service,
				provider,
				items,
				stageErr,
			)
		}
		if err := validateNetworkTransitionPhaseProof(
			item.plan,
			"network-stage",
			proof,
			proof.RouteGeneration,
		); err != nil {
			return rollbackProfileNetworkTransitionBatch(
				ctx, service, provider, items, err,
			)
		}
		setNetworkTransitionEffect(
			&item.result,
			"network-stage",
			EffectSucceeded,
			networkTransitionEvidence(
				"network-candidate-staged",
				item.plan.EnvironmentID,
				proof,
			),
		)
		if err := service.afterNetworkTransitionEffect(
			ctx,
			item.plan,
			networkTransitionResultEffect(
				item.result,
				"network-stage",
			),
		); err != nil {
			return rollbackProfileNetworkTransitionBatch(
				ctx, service, provider, items, err,
			)
		}
	}

	if err := runProfileNetworkBatchPhase(
		ctx,
		service,
		items,
		"network-probe",
		"network-candidate-probed",
		func(
			ctx context.Context,
			item *profileNetworkBatchItem,
		) (NetworkTransitionProof, error) {
			return item.handle.ProbeNetworkCandidate(ctx)
		},
	); err != nil {
		return rollbackProfileNetworkTransitionBatch(
			ctx, service, provider, items, err,
		)
	}
	if err := runProfileNetworkBatchPhase(
		ctx,
		service,
		items,
		"network-activate",
		"network-route-activated",
		func(
			ctx context.Context,
			item *profileNetworkBatchItem,
		) (NetworkTransitionProof, error) {
			proof, err :=
				item.handle.ActivateNetworkCandidate(ctx)
			if err == nil {
				item.activated = true
			}
			return proof, err
		},
	); err != nil {
		return rollbackProfileNetworkTransitionBatch(
			ctx, service, provider, items, err,
		)
	}
	if err := runProfileNetworkBatchPhase(
		ctx,
		service,
		items,
		"network-prove",
		"network-route-proved",
		func(
			ctx context.Context,
			item *profileNetworkBatchItem,
		) (NetworkTransitionProof, error) {
			return item.handle.ProveNetworkCandidate(ctx)
		},
	); err != nil {
		return rollbackProfileNetworkTransitionBatch(
			ctx, service, provider, items, err,
		)
	}
	if err := runProfileNetworkBatchPhase(
		ctx,
		service,
		items,
		"network-drain",
		"network-existing-connections-draining",
		func(
			ctx context.Context,
			item *profileNetworkBatchItem,
		) (NetworkTransitionProof, error) {
			return item.handle.DrainPreviousConnections(ctx)
		},
	); err != nil {
		return rollbackProfileNetworkTransitionBatch(
			ctx, service, provider, items, err,
		)
	}

	handles := profileNetworkBatchHandles(items)
	if beforeCommit != nil {
		if err := beforeCommit(ctx); err != nil {
			return rollbackProfileNetworkTransitionBatch(
				ctx, service, provider, items, err,
			)
		}
	}
	if err := provider.CommitNetworkCandidates(ctx, handles); err != nil {
		return rollbackProfileNetworkTransitionBatch(
			ctx, service, provider, items, err,
		)
	}
	for index := range items {
		items[index].result.Phase = NetworkTransitionSucceeded
		items[index].result.Effective = items[index].plan.Desired
		items[index].result.EffectiveProved = true
		items[index].result.Recovery = Recovery{
			Code:    "network-transition-complete",
			Summary: "The candidate route is proved and committed for new connections.",
		}
	}
	return profileNetworkBatchResults(items), nil
}

type profileNetworkBatchPhase func(
	context.Context,
	*profileNetworkBatchItem,
) (NetworkTransitionProof, error)

func runProfileNetworkBatchPhase(
	ctx context.Context,
	service NetworkTransitionService,
	items []profileNetworkBatchItem,
	effectID string,
	evidenceCode string,
	run profileNetworkBatchPhase,
) error {
	for index := range items {
		item := &items[index]
		if err := service.beforeNetworkTransitionEffect(
			ctx,
			item.plan,
			effectID,
		); err != nil {
			return err
		}
		proof, err := run(ctx, item)
		if err != nil {
			setNetworkTransitionEffect(
				&item.result,
				effectID,
				EffectFailed,
				nil,
			)
			return errors.Join(
				err,
				service.afterNetworkTransitionEffect(
					ctx,
					item.plan,
					networkTransitionResultEffect(
						item.result,
						effectID,
					),
				),
			)
		}
		if err := validateNetworkTransitionPhaseProof(
			item.plan,
			effectID,
			proof,
			item.candidateGeneration,
		); err != nil {
			return err
		}
		if effectID == "network-drain" {
			item.result.ConnectionsRetained =
				proof.ConnectionsRetained
		}
		setNetworkTransitionEffect(
			&item.result,
			effectID,
			EffectSucceeded,
			networkTransitionEvidence(
				evidenceCode,
				item.plan.EnvironmentID,
				proof,
			),
		)
		if err := service.afterNetworkTransitionEffect(
			ctx,
			item.plan,
			networkTransitionResultEffect(
				item.result,
				effectID,
			),
		); err != nil {
			return err
		}
	}
	return nil
}

func rollbackProfileNetworkTransitionBatch(
	ctx context.Context,
	service NetworkTransitionService,
	provider NetworkTransitionBatchProvider,
	items []profileNetworkBatchItem,
	operationErr error,
) ([]NetworkTransitionResult, error) {
	var (
		handles     []NetworkStagedCandidate
		handleItems []int
		proofs      []NetworkTransitionProof
		rollbackErr error
	)
	for index := range items {
		if items[index].handle != nil {
			handles = append(handles, items[index].handle)
			handleItems = append(handleItems, index)
		}
	}
	if len(handles) != 0 {
		proofs, rollbackErr = provider.RollbackNetworkCandidates(
			ctx,
			handles,
		)
		if rollbackErr == nil && len(proofs) != len(handles) {
			rollbackErr = ErrInvalidNetworkTransition
		}
	}
	if rollbackErr == nil {
		for proofIndex, itemIndex := range handleItems {
			item := &items[itemIndex]
			proof := proofs[proofIndex]
			if err := validateNetworkTransitionRollbackProof(
				item.plan,
				proof,
			); err != nil ||
				item.plan.Kind != NetworkTransitionDNS &&
					item.candidateGeneration != 0 &&
					proof.RouteGeneration ==
						item.candidateGeneration ||
				item.plan.Kind != NetworkTransitionDNS &&
					item.activated &&
					proof.RouteGeneration <=
						item.candidateGeneration {
				rollbackErr = ErrInvalidNetworkTransition
				break
			}
		}
	}
	if rollbackErr == nil {
		for index := range items {
			if items[index].handle != nil {
				continue
			}
			current, err := service.Provider.ObserveNetworkRoute(
				ctx,
				items[index].plan.EnvironmentID,
			)
			if err != nil || current != items[index].plan.From {
				rollbackErr = errors.Join(
					ErrNetworkTransitionStale,
					err,
				)
				break
			}
		}
	}
	if rollbackErr != nil {
		for index := range items {
			for effectIndex := range items[index].result.Effects {
				switch items[index].result.Effects[effectIndex].Status {
				case EffectSucceeded, EffectRunning:
					items[index].result.Effects[effectIndex].Status =
						EffectUnproved
				}
			}
			items[index].result.Phase =
				NetworkTransitionRollbackUnproved
			items[index].result.Effective =
				NetworkRouteConfiguration{}
			items[index].result.EffectiveProved = false
			items[index].result.Recovery =
				networkTransitionRollbackUnprovedRecovery()
		}
		return profileNetworkBatchResults(items),
			newNetworkTransitionApplyError(
				ErrNetworkTransitionRollbackUnproved,
				operationErr,
				rollbackErr,
			)
	}
	proofsByItem := make(map[int]NetworkTransitionProof, len(proofs))
	for proofIndex, itemIndex := range handleItems {
		proofsByItem[itemIndex] = proofs[proofIndex]
	}
	for index := range items {
		proof, hasProof := proofsByItem[index]
		for effectIndex := range items[index].result.Effects {
			switch items[index].result.Effects[effectIndex].Status {
			case EffectSucceeded, EffectRunning:
				items[index].result.Effects[effectIndex].Status =
					EffectRolledBack
				if hasProof {
					items[index].result.Effects[effectIndex].Evidence =
						networkTransitionEvidence(
							"network-route-restored",
							items[index].plan.EnvironmentID,
							proof,
						)
				}
			}
		}
		items[index].result.Phase = NetworkTransitionRolledBack
		items[index].result.Effective = items[index].plan.From
		items[index].result.EffectiveProved = true
		if hasProof {
			items[index].result.ConnectionsRetained =
				proof.ConnectionsRetained
		}
		items[index].result.Recovery =
			networkTransitionRolledBackRecovery()
	}
	return profileNetworkBatchResults(items),
		newNetworkTransitionApplyError(
			ErrNetworkTransitionRolledBack,
			operationErr,
		)
}

func profileNetworkBatchHandles(
	items []profileNetworkBatchItem,
) []NetworkStagedCandidate {
	handles := make([]NetworkStagedCandidate, len(items))
	for index := range items {
		handles[index] = items[index].handle
	}
	return handles
}

func profileNetworkBatchResults(
	items []profileNetworkBatchItem,
) []NetworkTransitionResult {
	results := make([]NetworkTransitionResult, len(items))
	for index := range items {
		results[index] = items[index].result
	}
	return results
}

func profileNetworkTransitionRepresentative(
	results []NetworkTransitionResult,
) *NetworkTransitionResult {
	if len(results) == 0 {
		return nil
	}
	for _, phase := range []string{
		NetworkTransitionRollbackUnproved,
		NetworkTransitionBlocked,
		NetworkTransitionRolledBack,
	} {
		for index := range results {
			if results[index].Phase == phase {
				result := results[index]
				return &result
			}
		}
	}
	result := results[0]
	return &result
}

func profileNetworkEffectID(
	environmentID string,
	effectID string,
) string {
	return "network." + environmentID + "." +
		strings.TrimPrefix(effectID, "network-")
}

func profileNetworkPlannedEffects(
	plans []NetworkTransitionPlan,
) []PlannedEffect {
	var effects []PlannedEffect
	for _, plan := range plans {
		for _, effect := range plan.Effects {
			cloned := effect
			cloned.ID = profileNetworkEffectID(
				plan.EnvironmentID,
				effect.ID,
			)
			cloned.Summary = "Environment " + plan.EnvironmentID +
				" (" + networkRouteReviewIdentity(plan.From) +
				" -> " +
				networkRouteReviewIdentity(plan.Desired) +
				"): " + effect.Summary
			effects = append(effects, cloned)
		}
	}
	return effects
}

func networkRouteReviewIdentity(
	route NetworkRouteConfiguration,
) string {
	if route.Mode == netpolicy.ModeDirect {
		return "direct"
	}
	return fmt.Sprintf(
		"proxy %s generation %d via resolver %s",
		route.ProxySecretRef,
		route.ProxySecretGeneration,
		route.MediatedResolver,
	)
}

func validStoredProfileNetwork(network profile.Network) bool {
	if network.ProxyEnvVisible ||
		network.Mode != profile.NetworkModeDirect &&
			network.Mode != profile.NetworkModeTun2Socks {
		return false
	}
	if network.ProxySecretRef != "" &&
		secrets.ValidateRef(network.ProxySecretRef) != nil {
		return false
	}
	if network.Mode == profile.NetworkModeTun2Socks &&
		network.ProxySecretRef == "" {
		return false
	}
	return network.MediatedResolver == "" ||
		net.ParseIP(network.MediatedResolver) != nil
}

func cloneProfileNetwork(
	value *profile.Network,
) *profile.Network {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func profileNetworkEffectsState(
	operation Operation,
	plan NetworkTransitionPlan,
) (string, error) {
	pending := 0
	proved := 0
	for _, effect := range plan.Effects {
		index := effectIndex(
			operation.Effects,
			profileNetworkEffectID(plan.EnvironmentID, effect.ID),
		)
		if index < 0 {
			return "", ErrInvalidOperation
		}
		switch operation.Effects[index].Status {
		case EffectPending:
			pending++
		case EffectSucceeded:
			if len(operation.Effects[index].Evidence) == 0 {
				return "", ErrInvalidOperation
			}
			proved++
		default:
			return "partial", nil
		}
	}
	switch {
	case pending == len(plan.Effects):
		return "pending", nil
	case proved == len(plan.Effects):
		return "proved", nil
	default:
		return "partial", nil
	}
}

type profileNetworkOperationCheckpoint struct {
	operationID string
	operations  OperationService
	next        NetworkTransitionEffectCheckpoint
}

func (checkpoint *profileNetworkOperationCheckpoint) BeforeNetworkTransitionEffect(
	ctx context.Context,
	plan NetworkTransitionPlan,
	effect PlannedEffect,
) error {
	if checkpoint == nil {
		return ErrInvalidOperation
	}
	operation, err := checkpoint.operations.Store.Load(
		checkpoint.operationID,
	)
	if err != nil {
		return err
	}
	switch effect.ID {
	case "network-activate":
		if operation.Phase == OperationStaging {
			operation, err = checkpoint.operations.Transition(
				operation.ID,
				OperationActivating,
			)
		}
	case "network-prove":
		if operation.Phase == OperationActivating ||
			operation.Phase == OperationStaging {
			operation, err = checkpoint.operations.Transition(
				operation.ID,
				OperationProving,
			)
		}
	}
	if err != nil {
		return err
	}
	_, execute, err := checkpoint.operations.BeginEffect(
		checkpoint.operationID,
		profileNetworkEffectID(plan.EnvironmentID, effect.ID),
		effect.Provider,
	)
	if err != nil {
		return err
	}
	if !execute {
		return errProfileNetworkEffectNeedsRecovery
	}
	if checkpoint.next != nil {
		return checkpoint.next.BeforeNetworkTransitionEffect(
			ctx,
			plan,
			effect,
		)
	}
	return nil
}

func (checkpoint *profileNetworkOperationCheckpoint) AfterNetworkTransitionEffect(
	ctx context.Context,
	plan NetworkTransitionPlan,
	effect EffectResult,
) error {
	if checkpoint == nil {
		return ErrInvalidOperation
	}
	evidence := append([]EvidenceRef(nil), effect.Evidence...)
	if len(evidence) == 0 {
		evidence = []EvidenceRef{{
			Code: "network-effect-" + effect.Status,
			Ref: "environment:" + plan.EnvironmentID +
				":effect:" + effect.ID,
		}}
	}
	_, err := checkpoint.operations.FinishEffect(
		checkpoint.operationID,
		profileNetworkEffectID(plan.EnvironmentID, effect.ID),
		effect.Provider,
		effect.Status,
		evidence,
	)
	if err == nil && checkpoint.next != nil {
		err = checkpoint.next.AfterNetworkTransitionEffect(
			ctx,
			plan,
			effect,
		)
	}
	return err
}

func syncProfileNetworkTransitionResult(
	operations OperationService,
	operationID string,
	plan NetworkTransitionPlan,
	result NetworkTransitionResult,
) (Operation, error) {
	operation, err := operations.Store.Load(operationID)
	if err != nil {
		return Operation{}, err
	}
	for _, effect := range result.Effects {
		if effect.Status == EffectPending ||
			effect.Status == EffectRunning {
			continue
		}
		evidence := append([]EvidenceRef(nil), effect.Evidence...)
		if len(evidence) == 0 {
			evidence = []EvidenceRef{{
				Code: "network-effect-" + effect.Status,
				Ref: "environment:" + plan.EnvironmentID +
					":effect:" + effect.ID,
			}}
		}
		operation, err = operations.FinishEffect(
			operationID,
			profileNetworkEffectID(plan.EnvironmentID, effect.ID),
			effect.Provider,
			effect.Status,
			evidence,
		)
		if err != nil {
			return operation, err
		}
	}
	return operation, nil
}

func validateProfileNetworkTransitionPlans(
	plans []NetworkTransitionPlan,
	publicEffects []PlannedEffect,
) error {
	expected := profileNetworkPlannedEffects(plans)
	if len(expected)+1 != len(publicEffects) {
		return ErrInvalidConfigurationPlan
	}
	byID := make(map[string]PlannedEffect, len(publicEffects))
	for _, effect := range publicEffects {
		byID[effect.ID] = effect
	}
	for _, effect := range expected {
		actual, ok := byID[effect.ID]
		if !ok || actual.ID != effect.ID ||
			actual.Kind != effect.Kind ||
			actual.Scope != effect.Scope ||
			actual.Provider != effect.Provider ||
			actual.Live != effect.Live ||
			actual.Summary != effect.Summary ||
			!equalStrings(actual.ProofRequired, effect.ProofRequired) {
			return ErrInvalidConfigurationPlan
		}
	}
	for index, plan := range plans {
		if plan.VerifyDigest() != nil {
			return ErrInvalidConfigurationPlan
		}
		if index != 0 &&
			plans[index-1].EnvironmentID >= plan.EnvironmentID {
			return ErrInvalidConfigurationPlan
		}
	}
	return nil
}

func sortProfileNetworkTransitionPlans(
	plans []NetworkTransitionPlan,
) {
	sort.Slice(plans, func(left, right int) bool {
		return plans[left].EnvironmentID <
			plans[right].EnvironmentID
	})
}

func profileNetworkTransitionFailureSummary(
	result *NetworkTransitionResult,
	err error,
) string {
	if result != nil {
		switch result.Phase {
		case NetworkTransitionRolledBack:
			return "The live route change failed and the previous route was restored."
		case NetworkTransitionRollbackUnproved:
			return "The live route change failed and restoration could not be proved."
		case NetworkTransitionBlocked:
			return "The live route change became blocked before activation."
		}
	}
	if err != nil {
		return fmt.Sprintf("The live route change failed (%T).", err)
	}
	return "The live route change did not complete."
}
