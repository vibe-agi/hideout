package manager

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

// GatewayNetworkTransitionProvider adapts the daemon-owned environment
// gateway to the generic network transition protocol. It deliberately owns
// only route changes: switching direct/proxy posture or guest DNS also needs a
// backend controller and is coordinated separately.
type GatewayNetworkTransitionProvider struct {
	StoreRoot   string
	Gateways    *netpolicy.GatewayRegistry
	ProbeTarget func(NetworkTransitionPlan) string
	Now         func() time.Time
}

func (provider GatewayNetworkTransitionProvider) ObserveNetworkRoute(
	_ context.Context,
	environmentID string,
) (NetworkRouteConfiguration, error) {
	if provider.Gateways == nil ||
		!networkTransitionEnvironmentPattern.MatchString(environmentID) {
		return NetworkRouteConfiguration{},
			ErrNetworkTransitionProviderUnavailable
	}
	observation, ok := provider.Gateways.RouteObservation(environmentID)
	if !ok || !observation.ActiveAvailable {
		return NetworkRouteConfiguration{},
			ErrNetworkTransitionProviderUnavailable
	}
	return provider.configurationForMetadata(
		environmentID,
		observation.Active,
	)
}

func (provider GatewayNetworkTransitionProvider) StageNetworkCandidate(
	_ context.Context,
	plan NetworkTransitionPlan,
	material NetworkCandidateMaterial,
) (NetworkStagedCandidate, NetworkTransitionProof, error) {
	if provider.Gateways == nil ||
		plan.Kind != NetworkTransitionRoute ||
		plan.From.Mode != netpolicy.ModeTun2Socks ||
		plan.Desired.Mode != netpolicy.ModeTun2Socks ||
		plan.From.MediatedResolver != plan.Desired.MediatedResolver {
		return nil, NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	_, change, err := provider.Gateways.StageRoute(
		plan.EnvironmentID,
		netpolicy.GatewayRouteSpec{
			UpstreamProxyURL: material.UpstreamProxyURL,
			ProxySecretRef:   plan.Desired.ProxySecretRef,
			SecretGeneration: plan.Desired.ProxySecretGeneration,
		},
	)
	if err != nil {
		return nil, NetworkTransitionProof{}, err
	}
	candidate := &gatewayNetworkTransitionCandidate{
		provider: provider,
		plan:     plan,
		change:   change,
	}
	proof, err := candidate.candidateProof(false)
	if err != nil {
		return candidate, proof, err
	}
	return candidate, proof, nil
}

func (provider GatewayNetworkTransitionProvider) CommitNetworkCandidates(
	_ context.Context,
	handles []NetworkStagedCandidate,
) error {
	changes, err := provider.gatewayChanges(handles)
	if err != nil {
		return err
	}
	return netpolicy.CommitGatewayChanges(changes)
}

func (provider GatewayNetworkTransitionProvider) RollbackNetworkCandidates(
	_ context.Context,
	handles []NetworkStagedCandidate,
) ([]NetworkTransitionProof, error) {
	candidates, changes, err := provider.gatewayCandidates(handles)
	if err != nil {
		return nil, err
	}
	if err := netpolicy.RollbackGatewayChanges(changes); err != nil {
		return nil, err
	}
	proofs := make([]NetworkTransitionProof, len(candidates))
	for index, candidate := range candidates {
		observation, ok := provider.Gateways.RouteObservation(
			candidate.plan.EnvironmentID,
		)
		if !ok || !observation.ActiveAvailable {
			return nil, errors.New(
				"previous environment gateway route is unavailable",
			)
		}
		effective, observeErr := provider.configurationForMetadata(
			candidate.plan.EnvironmentID,
			observation.Active,
		)
		if observeErr != nil || effective != candidate.plan.From {
			return nil, errors.New(
				"previous environment gateway route was not restored",
			)
		}
		proofs[index] = proofForGatewayObservation(
			observation,
			observation.Active,
			provider.nowUTC(),
		)
	}
	return proofs, nil
}

func (provider GatewayNetworkTransitionProvider) gatewayChanges(
	handles []NetworkStagedCandidate,
) ([]*netpolicy.GatewayChange, error) {
	_, changes, err := provider.gatewayCandidates(handles)
	return changes, err
}

func (provider GatewayNetworkTransitionProvider) gatewayCandidates(
	handles []NetworkStagedCandidate,
) ([]*gatewayNetworkTransitionCandidate, []*netpolicy.GatewayChange, error) {
	if provider.Gateways == nil || len(handles) == 0 {
		return nil, nil, ErrNetworkTransitionProviderUnavailable
	}
	candidates := make(
		[]*gatewayNetworkTransitionCandidate,
		len(handles),
	)
	changes := make([]*netpolicy.GatewayChange, len(handles))
	for index, handle := range handles {
		candidate, ok := handle.(*gatewayNetworkTransitionCandidate)
		if !ok || candidate == nil || candidate.change == nil ||
			candidate.provider.Gateways != provider.Gateways {
			return nil, nil,
				ErrNetworkTransitionProviderUnavailable
		}
		candidates[index] = candidate
		changes[index] = candidate.change
	}
	return candidates, changes, nil
}

func (provider GatewayNetworkTransitionProvider) configurationForMetadata(
	environmentID string,
	metadata netpolicy.GatewayRouteMetadata,
) (NetworkRouteConfiguration, error) {
	switch metadata.Mode {
	case netpolicy.ModeDirect:
		return NetworkRouteConfiguration{Mode: netpolicy.ModeDirect}, nil
	case netpolicy.ModeTun2Socks:
		if strings.TrimSpace(provider.StoreRoot) == "" {
			return NetworkRouteConfiguration{},
				ErrNetworkTransitionProviderUnavailable
		}
		statePath := filepath.Join(
			(environment.Store{Root: provider.StoreRoot}).
				RuntimeNetworkServiceDir(environmentID),
			"state.json",
		)
		state, err := netpolicy.LoadServiceState(statePath)
		if err != nil ||
			state.EnvironmentID != environmentID ||
			state.Status != netpolicy.ServiceReady ||
			state.Mode != netpolicy.ModeTun2Socks {
			return NetworkRouteConfiguration{},
				ErrNetworkTransitionProviderUnavailable
		}
		configuration := NetworkRouteConfiguration{
			Mode:                  netpolicy.ModeTun2Socks,
			ProxySecretRef:        metadata.ProxySecretRef,
			ProxySecretGeneration: metadata.SecretGeneration,
			MediatedResolver:      state.Resolver,
		}
		if err := configuration.Validate(); err != nil {
			return NetworkRouteConfiguration{},
				ErrNetworkTransitionProviderUnavailable
		}
		return configuration, nil
	default:
		return NetworkRouteConfiguration{},
			ErrNetworkTransitionProviderUnavailable
	}
}

func (provider GatewayNetworkTransitionProvider) nowUTC() time.Time {
	if provider.Now != nil {
		return provider.Now().Round(0).UTC()
	}
	return time.Now().Round(0).UTC()
}

func (provider GatewayNetworkTransitionProvider) probeTarget(
	plan NetworkTransitionPlan,
) string {
	if provider.ProbeTarget != nil {
		return strings.TrimSpace(provider.ProbeTarget(plan))
	}
	host := plan.Desired.MediatedResolver
	if net.ParseIP(host) == nil {
		host = "1.1.1.1"
	}
	return net.JoinHostPort(host, "443")
}

type gatewayNetworkTransitionCandidate struct {
	provider GatewayNetworkTransitionProvider
	plan     NetworkTransitionPlan
	change   *netpolicy.GatewayChange
}

func (candidate *gatewayNetworkTransitionCandidate) ProbeNetworkCandidate(
	ctx context.Context,
) (NetworkTransitionProof, error) {
	if candidate == nil || candidate.change == nil {
		return NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	if err := candidate.change.Probe(
		ctx,
		candidate.provider.probeTarget(candidate.plan),
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	return candidate.candidateProof(false)
}

func (candidate *gatewayNetworkTransitionCandidate) ActivateNetworkCandidate(
	_ context.Context,
) (NetworkTransitionProof, error) {
	if candidate == nil || candidate.change == nil {
		return NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	if err := candidate.change.Activate(); err != nil {
		return NetworkTransitionProof{}, err
	}
	return candidate.candidateProof(true)
}

func (candidate *gatewayNetworkTransitionCandidate) ProveNetworkCandidate(
	_ context.Context,
) (NetworkTransitionProof, error) {
	if candidate == nil || candidate.change == nil {
		return NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	return candidate.candidateProof(true)
}

func (candidate *gatewayNetworkTransitionCandidate) DrainPreviousConnections(
	_ context.Context,
) (NetworkTransitionProof, error) {
	if candidate == nil || candidate.change == nil {
		return NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	return candidate.candidateProof(true)
}

func (candidate *gatewayNetworkTransitionCandidate) CommitNetworkCandidate(
	_ context.Context,
) error {
	if candidate == nil || candidate.change == nil {
		return ErrNetworkTransitionProviderUnavailable
	}
	return candidate.change.Commit()
}

func (candidate *gatewayNetworkTransitionCandidate) RollbackNetworkCandidate(
	_ context.Context,
) (NetworkTransitionProof, error) {
	if candidate == nil || candidate.change == nil {
		return NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	if err := candidate.change.Rollback(); err != nil {
		return NetworkTransitionProof{}, err
	}
	observation, ok := candidate.provider.Gateways.RouteObservation(
		candidate.plan.EnvironmentID,
	)
	if !ok || !observation.ActiveAvailable {
		return NetworkTransitionProof{},
			errors.New("previous environment gateway route is unavailable")
	}
	effective, err := candidate.provider.configurationForMetadata(
		candidate.plan.EnvironmentID,
		observation.Active,
	)
	if err != nil || effective != candidate.plan.From {
		return NetworkTransitionProof{},
			errors.New("previous environment gateway route was not restored")
	}
	return proofForGatewayObservation(
		observation,
		observation.Active,
		candidate.provider.nowUTC(),
	), nil
}

func (candidate *gatewayNetworkTransitionCandidate) candidateProof(
	requireActive bool,
) (NetworkTransitionProof, error) {
	metadata := candidate.change.CandidateRoute()
	if metadata.RouteGeneration == 0 ||
		metadata.Mode != candidate.plan.Desired.Mode ||
		metadata.ProxySecretRef != candidate.plan.Desired.ProxySecretRef ||
		metadata.SecretGeneration !=
			candidate.plan.Desired.ProxySecretGeneration {
		return NetworkTransitionProof{}, ErrInvalidNetworkTransition
	}
	if !requireActive {
		return NetworkTransitionProof{
			RouteGeneration:  metadata.RouteGeneration,
			SecretGeneration: metadata.SecretGeneration,
			ObservedAt:       candidate.provider.nowUTC(),
		}, nil
	}
	observation, ok := candidate.provider.Gateways.RouteObservation(
		candidate.plan.EnvironmentID,
	)
	if !ok || !observation.ActiveAvailable ||
		observation.Active != metadata {
		return NetworkTransitionProof{},
			errors.New("candidate environment gateway route is not active")
	}
	return proofForGatewayObservation(
		observation,
		metadata,
		candidate.provider.nowUTC(),
	), nil
}

func proofForGatewayObservation(
	observation netpolicy.GatewayRouteObservation,
	active netpolicy.GatewayRouteMetadata,
	observedAt time.Time,
) NetworkTransitionProof {
	var retained uint64
	for _, connection := range observation.Connections {
		if connection.RouteGeneration != active.RouteGeneration {
			retained += connection.Count
		}
	}
	return NetworkTransitionProof{
		RouteGeneration:     active.RouteGeneration,
		SecretGeneration:    active.SecretGeneration,
		ConnectionsRetained: retained,
		ObservedAt:          observedAt,
	}
}
