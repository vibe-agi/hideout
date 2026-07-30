package manager

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

const liveDNSBootID = "01234567-89ab-cdef-0123-456789abcdef"

type liveDNSRuntimeFixture struct {
	environmentID      string
	sessionID          string
	bootID             string
	resolver           string
	failVerifyResolver string
	reconfigurations   [][2]string
	verifications      []string
	released           bool
}

func (runtime *liveDNSRuntimeFixture) EnvironmentID() string {
	return runtime.environmentID
}

func (runtime *liveDNSRuntimeFixture) SessionID() string {
	return runtime.sessionID
}

func (runtime *liveDNSRuntimeFixture) BootID() string {
	return runtime.bootID
}

func (runtime *liveDNSRuntimeFixture) ReconfigureDNS(
	_ context.Context,
	oldResolver string,
	newResolver string,
) error {
	runtime.reconfigurations = append(
		runtime.reconfigurations,
		[2]string{oldResolver, newResolver},
	)
	if runtime.resolver != oldResolver {
		return errors.New("runtime resolver does not match transition source")
	}
	runtime.resolver = newResolver
	return nil
}

func (runtime *liveDNSRuntimeFixture) VerifyDNS(
	_ context.Context,
	resolver string,
) error {
	runtime.verifications = append(
		runtime.verifications,
		resolver,
	)
	if resolver == runtime.failVerifyResolver {
		return errors.New("injected exact resolver proof failure")
	}
	if runtime.resolver != resolver {
		return errors.New("runtime resolver proof mismatch")
	}
	return nil
}

func (runtime *liveDNSRuntimeFixture) Release() {
	runtime.released = true
}

type liveDNSRuntimeProviderFixture struct {
	runtime *liveDNSRuntimeFixture
}

func (provider liveDNSRuntimeProviderFixture) EnvironmentNetworkRuntimeAvailable(
	_ context.Context,
	environmentID string,
) error {
	if provider.runtime == nil ||
		provider.runtime.environmentID != environmentID ||
		provider.runtime.released {
		return ErrEnvironmentNetworkRuntimeUnavailable
	}
	return nil
}

func (provider liveDNSRuntimeProviderFixture) AcquireEnvironmentNetworkRuntime(
	ctx context.Context,
	environmentID string,
) (EnvironmentNetworkRuntimeLease, error) {
	if err := provider.EnvironmentNetworkRuntimeAvailable(
		ctx,
		environmentID,
	); err != nil {
		return nil, err
	}
	return provider.runtime, nil
}

func TestLiveNetworkTransitionCommitsExactDNSAndHostEvidence(
	t *testing.T,
) {
	provider, runtime, environmentID := newLiveDNSProviderFixture(t)
	service := NetworkTransitionService{Provider: provider}
	from, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := from
	desired.MediatedResolver = "9.9.9.9"
	plan, err := service.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: environmentID,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != NetworkTransitionDNS ||
		!provider.SupportsNetworkTransitionKind(plan.Kind) {
		t.Fatalf("DNS plan=%+v", plan)
	}
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != NetworkTransitionSucceeded ||
		result.Effective != desired ||
		!result.EffectiveProved ||
		!runtime.released {
		t.Fatalf("DNS transition result=%+v runtime=%+v", result, runtime)
	}
	effective, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil || effective != desired {
		t.Fatalf("effective DNS=%+v err=%v", effective, err)
	}
	state, err := netpolicy.LoadServiceState(
		liveDNSStatePath(provider, environmentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != netpolicy.ServiceReady ||
		state.Resolver != desired.MediatedResolver ||
		state.BootID != liveDNSBootID {
		t.Fatalf("committed DNS state=%+v", state)
	}
	if len(runtime.reconfigurations) != 1 ||
		runtime.reconfigurations[0] !=
			[2]string{"1.1.1.1", "9.9.9.9"} {
		t.Fatalf(
			"DNS reconfigurations=%v",
			runtime.reconfigurations,
		)
	}
}

func TestLiveNetworkTransitionProofFailureRestoresDNSAndState(
	t *testing.T,
) {
	provider, runtime, environmentID := newLiveDNSProviderFixture(t)
	runtime.failVerifyResolver = "9.9.9.9"
	service := NetworkTransitionService{Provider: provider}
	from, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := from
	desired.MediatedResolver = "9.9.9.9"
	plan, err := service.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: environmentID,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) ||
		result.Phase != NetworkTransitionRolledBack ||
		!result.EffectiveProved ||
		result.Effective != from ||
		!runtime.released {
		t.Fatalf(
			"DNS rollback result=%+v runtime=%+v err=%v",
			result,
			runtime,
			err,
		)
	}
	if runtime.resolver != from.MediatedResolver ||
		len(runtime.reconfigurations) != 2 ||
		runtime.reconfigurations[1] !=
			[2]string{"9.9.9.9", "1.1.1.1"} {
		t.Fatalf("DNS runtime was not restored: %+v", runtime)
	}
	effective, observeErr := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if observeErr != nil || effective != from {
		t.Fatalf(
			"host DNS evidence was not restored: %+v err=%v",
			effective,
			observeErr,
		)
	}
}

func TestLiveDNSRecoveryCompletesActivatedGuestWithoutReplayingMutation(
	t *testing.T,
) {
	provider, runtime, environmentID := newLiveDNSProviderFixture(t)
	service := NetworkTransitionService{Provider: provider}
	from, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := from
	desired.MediatedResolver = "9.9.9.9"
	plan, err := service.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: environmentID,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handle, _, err := provider.StageNetworkCandidate(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.ProbeNetworkCandidate(
		context.Background(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.ActivateNetworkCandidate(
		context.Background(),
	); err != nil {
		t.Fatal(err)
	}
	if runtime.resolver != desired.MediatedResolver ||
		len(runtime.reconfigurations) != 1 {
		t.Fatalf("activation fixture=%+v", runtime)
	}

	recovery := LiveDNSNetworkTransitionRecoveryProvider{
		StoreRoot: provider.Gateway.StoreRoot,
		Runtimes:  provider.Runtimes,
	}
	observation, err := recovery.ReconcileNetworkTransition(
		context.Background(),
		NetworkTransitionRecoveryRequest{
			OperationID:   "op_dns_recovery",
			PlanDigest:    plan.PlanDigest,
			EnvironmentID: environmentID,
			From:          from,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Outcome != NetworkRecoveryCommitted ||
		observation.Effective != desired ||
		len(observation.Effects) != 5 ||
		observation.RollbackProof != nil ||
		len(runtime.reconfigurations) != 1 {
		t.Fatalf(
			"committed recovery=%+v runtime=%+v",
			observation,
			runtime,
		)
	}
	state, err := netpolicy.LoadServiceState(
		liveDNSStatePath(provider, environmentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != netpolicy.ServiceReady ||
		state.Resolver != desired.MediatedResolver {
		t.Fatalf("recovered DNS state=%+v", state)
	}
	journal, err := loadLiveNetworkTransitionJournal(
		filepath.Join(
			filepath.Dir(
				liveDNSStatePath(provider, environmentID),
			),
			"transition.json",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != liveNetworkJournalCommitted ||
		journal.CompletedEffects != 5 {
		t.Fatalf("recovered journal=%+v", journal)
	}
}

func TestLiveDNSRecoveryProvesOldGuestAndRestoresHostState(
	t *testing.T,
) {
	provider, runtime, environmentID := newLiveDNSProviderFixture(t)
	service := NetworkTransitionService{Provider: provider}
	from, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := from
	desired.MediatedResolver = "9.9.9.9"
	plan, err := service.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: environmentID,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handle, _, err := provider.StageNetworkCandidate(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.ProbeNetworkCandidate(
		context.Background(),
	); err != nil {
		t.Fatal(err)
	}

	recovery := LiveDNSNetworkTransitionRecoveryProvider{
		StoreRoot: provider.Gateway.StoreRoot,
		Runtimes:  provider.Runtimes,
	}
	observation, err := recovery.ReconcileNetworkTransition(
		context.Background(),
		NetworkTransitionRecoveryRequest{
			OperationID:   "op_dns_rollback",
			PlanDigest:    plan.PlanDigest,
			EnvironmentID: environmentID,
			From:          from,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Outcome != NetworkRecoveryRolledBack ||
		observation.Effective != from ||
		len(observation.Effects) != 2 ||
		observation.RollbackProof == nil ||
		len(runtime.reconfigurations) != 0 {
		t.Fatalf(
			"rolled-back recovery=%+v runtime=%+v",
			observation,
			runtime,
		)
	}
	state, err := netpolicy.LoadServiceState(
		liveDNSStatePath(provider, environmentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != netpolicy.ServiceReady ||
		state.Resolver != from.MediatedResolver {
		t.Fatalf("restored DNS state=%+v", state)
	}
}

func TestProfileNetworkRecoveryMarksStartedDNSEffectsRolledBack(
	t *testing.T,
) {
	provider, _, environmentID := newLiveDNSProviderFixture(t)
	service := NetworkTransitionService{Provider: provider}
	from, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := from
	desired.MediatedResolver = "9.9.9.9"
	plan, err := service.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: environmentID,
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	operationStore := OperationStore{Root: t.TempDir()}
	_, created, err := operationStore.Reserve(
		OperationBinding{
			ID: operationID, Kind: profileTransactionOperationKind,
			Owner: OperationOwner{
				Kind: "profile",
				ID:   "default",
			},
			PlanDigest:   plan.PlanDigest,
			BaseRevision: 1,
		},
		operationEffectsForConfigurationPlan(
			ConfigurationPlan{
				Effects: profileNetworkPlannedEffects(
					[]NetworkTransitionPlan{plan},
				),
			},
		),
	)
	if err != nil || !created {
		t.Fatalf("reserve operation created=%t err=%v", created, err)
	}
	operations := OperationService{Store: operationStore}
	operation, err := operations.Transition(
		operationID,
		OperationClaimed,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err = operations.Transition(
		operation.ID,
		OperationStaging,
	)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &profileNetworkOperationCheckpoint{
		operationID: operation.ID,
		operations:  operations,
	}
	stage := plan.Effects[0]
	if err := checkpoint.BeforeNetworkTransitionEffect(
		context.Background(),
		plan,
		stage,
	); err != nil {
		t.Fatal(err)
	}
	handle, stageProof, err := provider.StageNetworkCandidate(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.AfterNetworkTransitionEffect(
		context.Background(),
		plan,
		EffectResult{
			ID: stage.ID, Kind: stage.Kind,
			Provider: stage.Provider,
			Status:   EffectSucceeded,
			Evidence: networkTransitionEvidence(
				"network-candidate-staged",
				environmentID,
				stageProof,
			),
		},
	); err != nil {
		t.Fatal(err)
	}
	probe := plan.Effects[1]
	if err := checkpoint.BeforeNetworkTransitionEffect(
		context.Background(),
		plan,
		probe,
	); err != nil {
		t.Fatal(err)
	}
	probeProof, err := handle.ProbeNetworkCandidate(
		context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.AfterNetworkTransitionEffect(
		context.Background(),
		plan,
		EffectResult{
			ID: probe.ID, Kind: probe.Kind,
			Provider: probe.Provider,
			Status:   EffectSucceeded,
			Evidence: networkTransitionEvidence(
				"network-candidate-probed",
				environmentID,
				probeProof,
			),
		},
	); err != nil {
		t.Fatal(err)
	}
	coordinator := &ProfileNetworkTransitionCoordinator{
		Provider: provider,
		RecoveryProvider: LiveDNSNetworkTransitionRecoveryProvider{
			StoreRoot: provider.Gateway.StoreRoot,
			Runtimes:  provider.Runtimes,
		},
	}
	recoveredOperation, result, err := coordinator.Apply(
		context.Background(),
		operationID,
		[]NetworkTransitionPlan{plan},
		operations,
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) ||
		result == nil ||
		result.Phase != NetworkTransitionRolledBack ||
		result.Effective != from {
		t.Fatalf(
			"rollback recovery operation=%+v result=%+v err=%v",
			recoveredOperation,
			result,
			err,
		)
	}
	for _, effectID := range []string{
		"network-stage",
		"network-probe",
	} {
		index := effectIndex(
			recoveredOperation.Effects,
			profileNetworkEffectID(
				environmentID,
				effectID,
			),
		)
		if index < 0 ||
			recoveredOperation.Effects[index].Status !=
				EffectRolledBack ||
			len(recoveredOperation.Effects[index].Evidence) == 0 {
			t.Fatalf(
				"started effect %s was not restored: %+v",
				effectID,
				recoveredOperation,
			)
		}
	}
}

func newLiveDNSProviderFixture(
	t *testing.T,
) (
	LiveNetworkTransitionProvider,
	*liveDNSRuntimeFixture,
	string,
) {
	t.Helper()
	const environmentID = "env_livedns"
	root := t.TempDir()
	gateways := netpolicy.NewGatewayRegistry()
	t.Cleanup(func() { _ = gateways.Close() })
	binding, change, err := gateways.StageRoute(
		environmentID,
		netpolicy.GatewayRouteSpec{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
			ProxySecretRef:   "local-proxy",
			SecretGeneration: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	writeManagerGatewayServiceState(
		t,
		root,
		environmentID,
		binding.ID,
	)
	runtime := &liveDNSRuntimeFixture{
		environmentID: environmentID,
		sessionID:     "ses_livedns",
		bootID:        liveDNSBootID,
		resolver:      "1.1.1.1",
	}
	provider := LiveNetworkTransitionProvider{
		Gateway: GatewayNetworkTransitionProvider{
			StoreRoot: root,
			Gateways:  gateways,
		},
		Runtimes: liveDNSRuntimeProviderFixture{
			runtime: runtime,
		},
	}
	return provider, runtime, environmentID
}

func liveDNSStatePath(
	provider LiveNetworkTransitionProvider,
	environmentID string,
) string {
	return filepath.Join(
		(environment.Store{Root: provider.Gateway.StoreRoot}).
			RuntimeNetworkServiceDir(environmentID),
		"state.json",
	)
}
