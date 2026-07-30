package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

// LiveDNSNetworkTransitionRecoveryProvider reconciles only a previously
// journaled DNS transition. It never stages or activates a candidate. The
// exact guest boot and resolver must already prove either Desired or From;
// recovery then converges the host state file to that observed reality.
type LiveDNSNetworkTransitionRecoveryProvider struct {
	StoreRoot string
	Runtimes  EnvironmentNetworkRuntimeProvider
	Now       func() time.Time
}

func (provider LiveDNSNetworkTransitionRecoveryProvider) SupportsNetworkTransitionRecoveryKind(
	kind string,
) bool {
	return kind == NetworkTransitionDNS
}

func (provider LiveDNSNetworkTransitionRecoveryProvider) ReconcileNetworkTransition(
	ctx context.Context,
	request NetworkTransitionRecoveryRequest,
) (NetworkTransitionRecoveryObservation, error) {
	unproved := NetworkTransitionRecoveryObservation{
		OperationID:   request.OperationID,
		PlanDigest:    request.PlanDigest,
		EnvironmentID: request.EnvironmentID,
		Outcome:       NetworkRecoveryUnproved,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return unproved, err
	}
	if provider.Runtimes == nil ||
		request.OperationID == "" ||
		!profileDigestPattern.MatchString(request.PlanDigest) ||
		!networkTransitionEnvironmentPattern.MatchString(
			request.EnvironmentID,
		) {
		return unproved, nil
	}
	kind, err := classifyNetworkTransition(
		request.From,
		request.Desired,
	)
	if err != nil || kind != NetworkTransitionDNS {
		return unproved, nil
	}
	serviceDir := (environment.Store{
		Root: provider.StoreRoot,
	}).RuntimeNetworkServiceDir(request.EnvironmentID)
	journalPath := filepath.Join(serviceDir, "transition.json")
	journal, err := loadLiveNetworkTransitionJournal(journalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return unproved, nil
		}
		return unproved, nil
	}
	if journal.PlanDigest != request.PlanDigest ||
		journal.EnvironmentID != request.EnvironmentID ||
		journal.From != request.From ||
		journal.Desired != request.Desired {
		return unproved, nil
	}

	runtime, err := provider.Runtimes.
		AcquireEnvironmentNetworkRuntime(
			ctx,
			request.EnvironmentID,
		)
	if err != nil {
		return unproved, nil
	}
	defer runtime.Release()
	if runtime.EnvironmentID() != request.EnvironmentID ||
		runtime.BootID() != journal.RuntimeBootID {
		return unproved, nil
	}

	var effective NetworkRouteConfiguration
	switch {
	case runtime.VerifyDNS(
		ctx,
		request.Desired.MediatedResolver,
	) == nil:
		effective = request.Desired
	case runtime.VerifyDNS(
		ctx,
		request.From.MediatedResolver,
	) == nil:
		effective = request.From
	default:
		return unproved, nil
	}

	statePath := filepath.Join(serviceDir, "state.json")
	if effective == request.Desired {
		state := journal.PreviousState
		state.Status = netpolicy.ServiceReady
		state.Resolver = request.Desired.MediatedResolver
		state.UpdatedAt = provider.nowUTC(state.StartedAt)
		state.LastError = ""
		if err := netpolicy.WriteServiceState(
			statePath,
			state,
		); err != nil {
			return unproved, nil
		}
		journal.Phase = liveNetworkJournalCommitted
		journal.CompletedEffects = 5
		journal.UpdatedAt = provider.nowUTC(
			journal.PreviousState.StartedAt,
		)
		if err := writeLiveNetworkTransitionJournal(
			journalPath,
			journal,
		); err != nil {
			return unproved, nil
		}
		return provider.committedObservation(
			request,
			journal,
		), nil
	}

	restored := journal.PreviousState
	restored.Status = netpolicy.ServiceReady
	restored.UpdatedAt = provider.nowUTC(restored.StartedAt)
	restored.LastError = ""
	if err := netpolicy.WriteServiceState(
		statePath,
		restored,
	); err != nil {
		return unproved, nil
	}
	journal.Phase = liveNetworkJournalRolledBack
	journal.UpdatedAt = provider.nowUTC(
		journal.PreviousState.StartedAt,
	)
	if err := writeLiveNetworkTransitionJournal(
		journalPath,
		journal,
	); err != nil {
		return unproved, nil
	}
	return provider.rolledBackObservation(
		request,
		journal,
	), nil
}

func (provider LiveDNSNetworkTransitionRecoveryProvider) committedObservation(
	request NetworkTransitionRecoveryRequest,
	journal liveNetworkTransitionJournal,
) NetworkTransitionRecoveryObservation {
	effects := make([]NetworkTransitionRecoveryEffect, 5)
	for index, effect := range plannedNetworkTransitionEffects(
		NetworkTransitionDNS,
	) {
		resolver := request.Desired.MediatedResolver
		if index < 2 {
			resolver = request.From.MediatedResolver
		}
		effects[index] = NetworkTransitionRecoveryEffect{
			ID: effect.ID,
			Proof: provider.proof(
				journal,
				resolver,
				index,
			),
		}
	}
	return NetworkTransitionRecoveryObservation{
		OperationID:   request.OperationID,
		PlanDigest:    request.PlanDigest,
		EnvironmentID: request.EnvironmentID,
		Outcome:       NetworkRecoveryCommitted,
		Effective:     request.Desired,
		CandidateRouteGeneration: journal.
			CandidateRouteGeneration,
		ActiveRouteGeneration: journal.
			CandidateRouteGeneration,
		Effects: effects,
	}
}

func (provider LiveDNSNetworkTransitionRecoveryProvider) rolledBackObservation(
	request NetworkTransitionRecoveryRequest,
	journal liveNetworkTransitionJournal,
) NetworkTransitionRecoveryObservation {
	count := journal.CompletedEffects
	effects := make([]NetworkTransitionRecoveryEffect, count)
	planned := plannedNetworkTransitionEffects(
		NetworkTransitionDNS,
	)
	for index := 0; index < count; index++ {
		resolver := request.Desired.MediatedResolver
		if index < 2 {
			resolver = request.From.MediatedResolver
		}
		effects[index] = NetworkTransitionRecoveryEffect{
			ID: planned[index].ID,
			Proof: provider.proof(
				journal,
				resolver,
				index,
			),
		}
	}
	rollback := provider.proof(
		journal,
		request.From.MediatedResolver,
		count,
	)
	return NetworkTransitionRecoveryObservation{
		OperationID:   request.OperationID,
		PlanDigest:    request.PlanDigest,
		EnvironmentID: request.EnvironmentID,
		Outcome:       NetworkRecoveryRolledBack,
		Effective:     request.From,
		CandidateRouteGeneration: journal.
			CandidateRouteGeneration,
		ActiveRouteGeneration: journal.
			CandidateRouteGeneration,
		Effects:       effects,
		RollbackProof: &rollback,
	}
}

func (provider LiveDNSNetworkTransitionRecoveryProvider) proof(
	journal liveNetworkTransitionJournal,
	resolver string,
	offset int,
) NetworkTransitionProof {
	return NetworkTransitionProof{
		RouteGeneration: journal.CandidateRouteGeneration,
		SecretGeneration: journal.Desired.
			ProxySecretGeneration,
		MediatedResolver: resolver,
		RuntimeBootID:    journal.RuntimeBootID,
		ObservedAt: provider.nowUTC(
			journal.PreviousState.StartedAt,
		).Add(time.Duration(offset) * time.Nanosecond),
	}
}

func (provider LiveDNSNetworkTransitionRecoveryProvider) nowUTC(
	startedAt time.Time,
) time.Time {
	var now time.Time
	if provider.Now != nil {
		now = provider.Now().Round(0).UTC()
	} else {
		now = time.Now().Round(0).UTC()
	}
	if now.Before(startedAt) {
		return startedAt
	}
	return now
}
