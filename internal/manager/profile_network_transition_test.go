package manager

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestProfileTransactionDrivesCheckpointedLiveRouteTransition(
	t *testing.T,
) {
	service, store, projection, provider := newLiveProfileRouteFixture(
		t,
		"",
	)
	plan := planProfileProxyReferenceChange(
		t,
		service,
		projection,
		"rotated-proxy",
	)
	if len(plan.Effects) != 6 ||
		plan.Effects[0].ID == "persist-profile" &&
			plan.Effects[1].ID == "persist-profile" {
		t.Fatalf("live route effects=%+v", plan.Effects)
	}
	private, err := os.ReadFile(service.planPath(plan.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(
		string(private),
		"candidate-password",
	) ||
		!strings.Contains(string(private), `"baseNetwork"`) ||
		strings.Contains(
			string(private),
			`"desired":{"schemaVersion"`,
		) {
		t.Fatalf("private live plan boundary is invalid: %s", private)
	}

	result, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded ||
		len(result.Operation.Effects) != 6 {
		t.Fatalf("live route operation=%+v", result.Operation)
	}
	for _, effect := range result.Operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			t.Fatalf("unproved live effect=%+v", effect)
		}
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Network.ProxySecretRef != "rotated-proxy" {
		t.Fatalf("profile network=%+v", current.Network)
	}
	wantTail := []string{
		"stage", "probe", "activate", "prove", "drain", "commit",
	}
	if len(provider.events) < len(wantTail) {
		t.Fatalf("provider events=%v", provider.events)
	}
	gotTail := provider.events[len(provider.events)-len(wantTail):]
	for index := range wantTail {
		if gotTail[index] != wantTail[index] {
			t.Fatalf("provider events=%v want tail=%v", provider.events, wantTail)
		}
	}
}

func TestProfileTransactionLiveRouteFailureRestoresProfileAndRoute(
	t *testing.T,
) {
	service, store, projection, provider := newLiveProfileRouteFixture(
		t,
		"prove",
	)
	plan := planProfileProxyReferenceChange(
		t,
		service,
		projection,
		"rotated-proxy",
	)
	result, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) {
		t.Fatalf("route failure error=%v result=%+v", err, result)
	}
	if result.Operation.Phase != OperationRolledBack ||
		result.Projection.ContentDigest != plan.BaseDigest {
		t.Fatalf("route rollback result=%+v", result)
	}
	current, loadErr := store.Load("default")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if current.Network.ProxySecretRef != "local-proxy" {
		t.Fatalf("failed desired route survived rollback: %+v", current.Network)
	}
	persist := effectIndex(result.Operation.Effects, "persist-profile")
	if persist < 0 ||
		result.Operation.Effects[persist].Status != EffectRolledBack ||
		len(result.Operation.Effects[persist].Evidence) == 0 {
		t.Fatalf("profile restoration is unproved: %+v", result.Operation)
	}
	if !containsNetworkTransitionEvent(provider.events, "rollback") {
		t.Fatalf("provider did not roll back: %v", provider.events)
	}
	if _, statErr := os.Stat(service.planPath(plan.OperationID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("terminal rollback retained plan record: %v", statErr)
	}
}

func TestProfileTransactionCommitsAllLiveEnvironmentRoutesAsOneBatch(
	t *testing.T,
) {
	service, store, projection := newProfileTransactionTestService(t)
	projection = configureLiveProfileNetwork(t, service, store, projection)
	firstID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-live-route-a",
	)
	secondID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-live-route-b",
	)
	from := liveProfileRouteFrom()
	desired := liveProfileRouteDesired()
	provider := newProfileNetworkBatchProvider(
		map[string]NetworkRouteConfiguration{
			firstID: from, secondID: from,
		},
		desired,
	)
	installLiveProfileNetworkCoordinator(
		service,
		provider,
	)

	plan := planProfileProxyReferenceChange(
		t,
		service,
		projection,
		"rotated-proxy",
	)
	if len(plan.Blockers) != 0 || len(plan.Effects) != 11 {
		t.Fatalf("multi-environment plan=%+v", plan)
	}
	result, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded {
		t.Fatalf("multi-environment operation=%+v", result.Operation)
	}
	for _, effect := range result.Operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			t.Fatalf("unproved batch effect=%+v", effect)
		}
	}
	for _, environmentID := range []string{firstID, secondID} {
		if provider.current[environmentID] != desired {
			t.Fatalf(
				"environment %s effective=%+v",
				environmentID,
				provider.current[environmentID],
			)
		}
	}
	firstActivate := eventPosition(
		provider.events,
		"activate:",
	)
	lastProbe := lastEventPosition(provider.events, "probe:")
	if firstActivate < 0 || lastProbe < 0 ||
		lastProbe > firstActivate ||
		provider.events[len(provider.events)-1] != "batch-commit" {
		t.Fatalf("batch ordering=%v", provider.events)
	}
}

func TestProfileTransactionBatchActivationFailureRestoresEveryRoute(
	t *testing.T,
) {
	service, store, projection := newProfileTransactionTestService(t)
	projection = configureLiveProfileNetwork(t, service, store, projection)
	firstID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-live-route-a",
	)
	secondID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-live-route-b",
	)
	from := liveProfileRouteFrom()
	provider := newProfileNetworkBatchProvider(
		map[string]NetworkRouteConfiguration{
			firstID: from, secondID: from,
		},
		liveProfileRouteDesired(),
	)
	ordered := []string{firstID, secondID}
	sort.Strings(ordered)
	provider.failEnvironment = ordered[1]
	provider.failPhase = "activate"
	installLiveProfileNetworkCoordinator(service, provider)

	plan := planProfileProxyReferenceChange(
		t,
		service,
		projection,
		"rotated-proxy",
	)
	result, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) {
		t.Fatalf("batch failure error=%v result=%+v", err, result)
	}
	if result.Operation.Phase != OperationRolledBack ||
		result.Projection.ContentDigest != plan.BaseDigest {
		t.Fatalf("batch rollback result=%+v", result)
	}
	for _, environmentID := range ordered {
		if provider.current[environmentID] != from {
			t.Fatalf(
				"environment %s was not restored: %+v",
				environmentID,
				provider.current[environmentID],
			)
		}
	}
	if countEvent(provider.events, "batch-rollback") != 1 ||
		eventPosition(provider.events, "batch-commit") >= 0 {
		t.Fatalf("batch rollback ordering=%v", provider.events)
	}
}

func TestProfileTransactionAppliesSingleLiveDNSChangeWithoutDaemonRestart(
	t *testing.T,
) {
	service, store, projection := newProfileTransactionTestService(t)
	projection = configureLiveProfileNetwork(
		t,
		service,
		store,
		projection,
	)
	environmentID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-live-dns",
	)
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
		store.Root,
		environmentID,
		binding.ID,
	)
	runtime := &liveDNSRuntimeFixture{
		environmentID: environmentID,
		sessionID:     "ses_profiledns",
		bootID:        liveDNSBootID,
		resolver:      "1.1.1.1",
	}
	provider := LiveNetworkTransitionProvider{
		Gateway: GatewayNetworkTransitionProvider{
			StoreRoot: store.Root,
			Gateways:  gateways,
		},
		Runtimes: liveDNSRuntimeProviderFixture{
			runtime: runtime,
		},
	}
	service.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core:     service.Core,
			Provider: provider,
			Sessions: fixedNetworkTransitionSessions(),
			SecretReferences: fixedNetworkSecretReference{
				value: secrets.Reference{
					Schema: secrets.SecretReferenceSchema,
					Ref:    "local-proxy", Provider: "memory",
					Availability: secrets.AvailabilityAvailable,
					Generation:   1,
					UpdatedAt: time.Date(
						2026, 7, 30, 8, 0, 0, 0,
						time.UTC,
					),
				},
			},
			SecretResolver: fixedProfileNetworkSecretResolver{
				value: netpolicy.SecretResolution{
					Value:      "socks5://127.0.0.1:7890",
					Source:     netpolicy.SecretSourceManaged,
					Generation: 1,
				},
			},
		}
	dnsChange, err := NewTypedChange(
		ChangeNetworkDNS,
		map[string]any{
			"mode":     "doh",
			"serverIp": "9.9.9.9",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(
		context.Background(),
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      projection.Profile,
			BaseRevision: projection.Revision,
			ClientNonce:  "live-dns",
			Changes:      []TypedChange{dnsChange},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 ||
		len(plan.Warnings) != 0 ||
		len(plan.Effects) != 6 {
		t.Fatalf("live DNS plan=%+v", plan)
	}
	result, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded ||
		len(result.Operation.Effects) != 6 ||
		!runtime.released {
		t.Fatalf(
			"live DNS result=%+v runtime=%+v",
			result,
			runtime,
		)
	}
	for _, effect := range result.Operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			t.Fatalf("unproved live DNS effect=%+v", effect)
		}
		if strings.HasSuffix(effect.ID, ".network-prove") &&
			(!strings.Contains(
				effect.Evidence[0].Ref,
				":resolver:9.9.9.9",
			) ||
				!strings.Contains(
					effect.Evidence[0].Ref,
					":boot:"+liveDNSBootID,
				)) {
			t.Fatalf(
				"DNS proof omitted resolver or boot identity: %+v",
				effect,
			)
		}
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Network.MediatedResolver != "9.9.9.9" {
		t.Fatalf("profile DNS=%+v", current.Network)
	}
	effective, err := provider.ObserveNetworkRoute(
		context.Background(),
		environmentID,
	)
	if err != nil ||
		effective.MediatedResolver != "9.9.9.9" {
		t.Fatalf("effective DNS=%+v err=%v", effective, err)
	}
}

func TestProfileTransactionRecoversActivatedDNSAfterDaemonCrash(
	t *testing.T,
) {
	service, store, projection := newProfileTransactionTestService(t)
	projection = configureLiveProfileNetwork(
		t,
		service,
		store,
		projection,
	)
	environmentID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-crash-dns",
	)
	gateways := netpolicy.NewGatewayRegistry()
	t.Cleanup(func() { _ = gateways.Close() })
	binding, gatewayChange, err := gateways.StageRoute(
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
	if err := gatewayChange.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := gatewayChange.Commit(); err != nil {
		t.Fatal(err)
	}
	writeManagerGatewayServiceState(
		t,
		store.Root,
		environmentID,
		binding.ID,
	)
	runtime := &liveDNSRuntimeFixture{
		environmentID: environmentID,
		sessionID:     "ses_crashdns",
		bootID:        liveDNSBootID,
		resolver:      "1.1.1.1",
	}
	runtimeProvider := liveDNSRuntimeProviderFixture{
		runtime: runtime,
	}
	provider := LiveNetworkTransitionProvider{
		Gateway: GatewayNetworkTransitionProvider{
			StoreRoot: store.Root,
			Gateways:  gateways,
		},
		Runtimes: runtimeProvider,
	}
	recoveryProvider :=
		LiveDNSNetworkTransitionRecoveryProvider{
			StoreRoot: store.Root,
			Runtimes:  runtimeProvider,
		}
	service.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core:     service.Core,
			Provider: provider,
			Sessions: fixedNetworkTransitionSessions(),
			SecretReferences: fixedNetworkSecretReference{
				value: secrets.Reference{
					Schema: secrets.SecretReferenceSchema,
					Ref:    "local-proxy", Provider: "memory",
					Availability: secrets.AvailabilityAvailable,
					Generation:   1,
					UpdatedAt: time.Date(
						2026, 7, 30, 8, 0, 0, 0,
						time.UTC,
					),
				},
			},
			SecretResolver: fixedProfileNetworkSecretResolver{
				value: netpolicy.SecretResolution{
					Value:      "socks5://127.0.0.1:7890",
					Source:     netpolicy.SecretSourceManaged,
					Generation: 1,
				},
			},
			RecoveryProvider: recoveryProvider,
		}
	dnsChange, err := NewTypedChange(
		ChangeNetworkDNS,
		map[string]any{
			"mode":     "doh",
			"serverIp": "9.9.9.9",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publicPlan, err := service.Plan(
		context.Background(),
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      projection.Profile,
			BaseRevision: projection.Revision,
			ClientNonce:  "crash-live-dns",
			Changes:      []TypedChange{dnsChange},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.loadPlanRecord(
		publicPlan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.NetworkTransitions) != 1 {
		t.Fatalf("private DNS plans=%+v", record)
	}
	operations := service.operations()
	operation, err := service.ensureConfigurationOperationStaging(
		mustLoadOperation(
			t,
			service.operationStore(),
			publicPlan.OperationID,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	persistEffect, err := configurationPersistEffect(
		record.Plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, execute, err := operations.BeginEffect(
		operation.ID,
		persistEffect.ID,
		persistEffect.Provider,
	)
	if err != nil || !execute {
		t.Fatalf(
			"begin profile persistence execute=%t err=%v",
			execute,
			err,
		)
	}
	if record.DesiredNetwork == nil {
		t.Fatal("private DNS plan omitted desired network")
	}
	desiredProfile := projection.Desired
	desiredProfile.Network = *record.DesiredNetwork
	if err := store.Save(desiredProfile); err != nil {
		t.Fatal(err)
	}
	persistedProjection, err := service.projections().Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if persistedProjection.Revision !=
		record.Plan.BaseRevision+1 ||
		persistedProjection.ContentDigest != record.TargetDigest {
		t.Fatalf(
			"manual persisted projection=%+v target=%s base=%d",
			persistedProjection,
			record.TargetDigest,
			record.Plan.BaseRevision,
		)
	}
	operation, err = operations.FinishEffect(
		operation.ID,
		persistEffect.ID,
		persistEffect.Provider,
		EffectSucceeded,
		[]EvidenceRef{{
			Code: "profile-committed",
			Ref:  "profile:default",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	transitionPlan := record.NetworkTransitions[0]
	checkpoint := &profileNetworkOperationCheckpoint{
		operationID: operation.ID,
		operations:  operations,
	}
	stageEffect := transitionPlan.Effects[0]
	if err := checkpoint.BeforeNetworkTransitionEffect(
		context.Background(),
		transitionPlan,
		stageEffect,
	); err != nil {
		t.Fatal(err)
	}
	handle, stageProof, err := provider.StageNetworkCandidate(
		context.Background(),
		transitionPlan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.AfterNetworkTransitionEffect(
		context.Background(),
		transitionPlan,
		EffectResult{
			ID: stageEffect.ID, Kind: stageEffect.Kind,
			Provider: stageEffect.Provider,
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
	probeEffect := transitionPlan.Effects[1]
	if err := checkpoint.BeforeNetworkTransitionEffect(
		context.Background(),
		transitionPlan,
		probeEffect,
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
		transitionPlan,
		EffectResult{
			ID: probeEffect.ID, Kind: probeEffect.Kind,
			Provider: probeEffect.Provider,
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
	activateEffect := transitionPlan.Effects[2]
	if err := checkpoint.BeforeNetworkTransitionEffect(
		context.Background(),
		transitionPlan,
		activateEffect,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.ActivateNetworkCandidate(
		context.Background(),
	); err != nil {
		t.Fatal(err)
	}
	// Simulated process death: activation returned, but its durable operation
	// effect is still running and no prove/drain/commit call is replayed.
	operation = mustLoadOperation(
		t,
		service.operationStore(),
		publicPlan.OperationID,
	)
	activateIndex := effectIndex(
		operation.Effects,
		profileNetworkEffectID(
			environmentID,
			"network-activate",
		),
	)
	if operation.Phase != OperationActivating ||
		activateIndex < 0 ||
		operation.Effects[activateIndex].Status != EffectRunning {
		t.Fatalf("crash envelope=%+v", operation)
	}
	if len(runtime.reconfigurations) != 1 ||
		runtime.resolver != "9.9.9.9" {
		t.Fatalf("crash runtime=%+v", runtime)
	}
	restarted := NewProfileTransactionService(service.Core)
	restarted.now = service.now
	restarted.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core:             service.Core,
			Provider:         provider,
			Sessions:         fixedNetworkTransitionSessions(),
			SecretReferences: service.NetworkTransitions.SecretReferences,
			SecretResolver:   service.NetworkTransitions.SecretResolver,
			RecoveryProvider: recoveryProvider,
		}
	recovered, err := restarted.ReconcileOperation(
		context.Background(),
		publicPlan.OperationID,
	)
	if err != nil {
		currentOperation := mustLoadOperation(
			t,
			restarted.operationStore(),
			publicPlan.OperationID,
		)
		t.Fatalf(
			"reconcile error=%v operation=%+v",
			err,
			currentOperation,
		)
	}
	if recovered.Operation.Phase != OperationSucceeded ||
		len(runtime.reconfigurations) != 1 {
		t.Fatalf(
			"recovered operation=%+v runtime=%+v",
			recovered.Operation,
			runtime,
		)
	}
	for _, effect := range recovered.Operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			t.Fatalf(
				"recovered effect is unproved: %+v",
				effect,
			)
		}
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Network.MediatedResolver != "9.9.9.9" {
		t.Fatalf("recovered profile=%+v", current.Network)
	}
}

func TestProfilePostureChangeCommitsDesiredAndPreservesActiveSessions(
	t *testing.T,
) {
	service, store, projection := newProfileTransactionTestService(t)
	environmentID := createLiveProfileEnvironment(
		t,
		store,
		projection.Desired,
		"hideout-posture-pending",
	)
	from := NetworkRouteConfiguration{
		Mode: netpolicy.ModeDirect,
	}
	desired := NetworkRouteConfiguration{
		Mode:                  netpolicy.ModeTun2Socks,
		ProxySecretRef:        "local-proxy",
		ProxySecretGeneration: 1,
		MediatedResolver:      "1.1.1.1",
	}
	routeProvider := newSuccessfulNetworkTransitionProvider(
		from,
		desired,
		0,
	)
	routeProvider.observed = []NetworkRouteConfiguration{
		from,
		from,
		from,
		from,
	}
	service.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core: service.Core,
			Provider: profileNetworkEnvironmentProvider{
				environmentID: environmentID,
				provider:      routeProvider,
			},
			Sessions: fixedNetworkTransitionSessions(
				NetworkTransitionSession{
					ID:     "ses_busy",
					Status: NetworkSessionLive,
					Phase:  "running",
				},
			),
			SecretReferences: fixedNetworkSecretReference{
				value: secrets.Reference{
					Schema: secrets.SecretReferenceSchema,
					Ref:    "local-proxy", Provider: "memory",
					Availability: secrets.AvailabilityAvailable,
					Generation:   1,
					UpdatedAt: time.Date(
						2026, 7, 30, 8, 0, 0, 0,
						time.UTC,
					),
				},
			},
		}
	posture, err := NewTypedChange(
		ChangeNetworkPosture,
		map[string]any{"mode": "proxy"},
	)
	if err != nil {
		t.Fatal(err)
	}
	proxyRef, err := NewTypedChange(
		ChangeNetworkProxyRef,
		map[string]any{"ref": "local-proxy"},
	)
	if err != nil {
		t.Fatal(err)
	}
	dns, err := NewTypedChange(
		ChangeNetworkDNS,
		map[string]any{
			"mode":     "doh",
			"serverIp": "1.1.1.1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(
		context.Background(),
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      projection.Profile,
			BaseRevision: projection.Revision,
			ClientNonce:  "posture-pending",
			Changes: []TypedChange{
				posture,
				proxyRef,
				dns,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 ||
		len(plan.Warnings) != 1 ||
		plan.Warnings[0].Code !=
			"network-posture-pending-next-attach" ||
		len(plan.Effects) != 1 {
		t.Fatalf("deferred posture plan=%+v", plan)
	}
	result, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded ||
		routeProvider.activated {
		t.Fatalf(
			"deferred posture result=%+v provider=%+v",
			result,
			routeProvider,
		)
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Network.Mode != profile.NetworkModeTun2Socks ||
		current.Network.ProxySecretRef != "local-proxy" ||
		current.Network.MediatedResolver != "1.1.1.1" {
		t.Fatalf("desired posture was not stored: %+v", current.Network)
	}
}

func newLiveProfileRouteFixture(
	t *testing.T,
	failEvent string,
) (
	*ProfileTransactionService,
	profile.Store,
	ProfileProjection,
	*networkTransitionProviderFixture,
) {
	t.Helper()
	service, store, projection := newProfileTransactionTestService(t)
	current := projection.Desired
	current.Network = profile.Network{
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "local-proxy",
		MediatedResolver: "1.1.1.1",
		ProxyEnvVisible:  false,
	}
	if err := store.Save(current); err != nil {
		t.Fatal(err)
	}
	projection, err := service.projections().Load("default")
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := RuntimeConfigurationForProfile(
		projection.Desired,
		"lima",
		environment.ModeShared,
	)
	if err != nil {
		t.Fatal(err)
	}
	environmentStore := environment.Store{Root: store.Root}
	record, err := environmentStore.Create(environment.Spec{
		Name:                environment.SharedDisplayName("default"),
		ImageRef:            projection.Desired.Environment.BaseImage,
		Profile:             "default",
		Backend:             "lima",
		Mode:                environment.ModeShared,
		SharedSlot:          environment.SharedSlotID("default"),
		MachineIdentityID:   configuration.Layers.MachineID,
		BootConfigurationID: configuration.Layers.BootID,
		InstanceName:        "hideout-live-route",
	})
	if err != nil {
		t.Fatal(err)
	}
	from := NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "local-proxy", ProxySecretGeneration: 1,
		MediatedResolver: "1.1.1.1",
	}
	desired := NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "rotated-proxy", ProxySecretGeneration: 2,
		MediatedResolver: "1.1.1.1",
	}
	provider := newSuccessfulNetworkTransitionProvider(
		from,
		desired,
		3,
	)
	provider.failEvent = failEvent
	provider.failure = errors.New("injected provider failure")
	provider.observed = []NetworkRouteConfiguration{
		from, from, from, from, from,
	}
	reference := fixedNetworkSecretReference{
		value: secrets.Reference{
			Schema: secrets.SecretReferenceSchema,
			Ref:    "rotated-proxy", Provider: "memory",
			Availability: secrets.AvailabilityAvailable,
			Generation:   2,
			UpdatedAt: time.Date(
				2026, 7, 30, 8, 0, 0, 0, time.UTC,
			),
		},
	}
	resolver := fixedProfileNetworkSecretResolver{
		value: netpolicy.SecretResolution{
			Value:  "socks5://candidate-user:candidate-password@127.0.0.1:7890",
			Source: netpolicy.SecretSourceManaged, Generation: 2,
		},
	}
	service.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core: service.Core,
			Provider: profileNetworkEnvironmentProvider{
				environmentID: record.ID,
				provider:      provider,
			},
			Sessions:         fixedNetworkTransitionSessions(),
			SecretReferences: reference,
			SecretResolver:   resolver,
		}
	return service, store, projection, provider
}

func configureLiveProfileNetwork(
	t *testing.T,
	service *ProfileTransactionService,
	store profile.Store,
	projection ProfileProjection,
) ProfileProjection {
	t.Helper()
	current := projection.Desired
	current.Network = profile.Network{
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "local-proxy",
		MediatedResolver: "1.1.1.1",
		ProxyEnvVisible:  false,
	}
	if err := store.Save(current); err != nil {
		t.Fatal(err)
	}
	updated, err := service.projections().Load("default")
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func mustLoadOperation(
	t *testing.T,
	store OperationStore,
	operationID string,
) Operation {
	t.Helper()
	operation, err := store.Load(operationID)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func createLiveProfileEnvironment(
	t *testing.T,
	store profile.Store,
	selected profile.Profile,
	instanceName string,
) string {
	t.Helper()
	configuration, err := RuntimeConfigurationForProfile(
		selected,
		"lima",
		environment.ModeShared,
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := (environment.Store{Root: store.Root}).Create(
		environment.Spec{
			Name:     instanceName,
			ImageRef: selected.Environment.BaseImage,
			Profile:  "default", Backend: "lima",
			Mode:                environment.ModeWorkspaceBound,
			BoundWorkspace:      t.TempDir(),
			BoundGuestRoot:      "/workspace",
			MachineIdentityID:   configuration.Layers.MachineID,
			BootConfigurationID: configuration.Layers.BootID,
			InstanceName:        instanceName,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return record.ID
}

func liveProfileRouteFrom() NetworkRouteConfiguration {
	return NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "local-proxy", ProxySecretGeneration: 1,
		MediatedResolver: "1.1.1.1",
	}
}

func liveProfileRouteDesired() NetworkRouteConfiguration {
	return NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "rotated-proxy", ProxySecretGeneration: 2,
		MediatedResolver: "1.1.1.1",
	}
}

func installLiveProfileNetworkCoordinator(
	service *ProfileTransactionService,
	provider NetworkTransitionProvider,
) {
	service.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core: service.Core, Provider: provider,
			Sessions: fixedNetworkTransitionSessions(),
			SecretReferences: fixedNetworkSecretReference{
				value: secrets.Reference{
					Schema: secrets.SecretReferenceSchema,
					Ref:    "rotated-proxy", Provider: "memory",
					Availability: secrets.AvailabilityAvailable,
					Generation:   2,
					UpdatedAt: time.Date(
						2026, 7, 30, 8, 0, 0, 0, time.UTC,
					),
				},
			},
			SecretResolver: fixedProfileNetworkSecretResolver{
				value: netpolicy.SecretResolution{
					Value: "socks5://candidate-user:" +
						"candidate-password@127.0.0.1:7890",
					Source:     netpolicy.SecretSourceManaged,
					Generation: 2,
				},
			},
		}
}

func planProfileProxyReferenceChange(
	t *testing.T,
	service *ProfileTransactionService,
	projection ProfileProjection,
	ref string,
) ConfigurationPlan {
	t.Helper()
	change, err := NewTypedChange(
		ChangeNetworkProxyRef,
		map[string]any{"ref": ref},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(
		context.Background(),
		ConfigurationDraft{
			Schema: ConfigurationDraftSchema, Profile: projection.Profile,
			BaseRevision: projection.Revision,
			ClientNonce:  "live-route",
			Changes:      []TypedChange{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type fixedNetworkSecretReference struct {
	value secrets.Reference
	err   error
}

func (provider fixedNetworkSecretReference) NetworkSecretReference(
	context.Context,
	string,
) (secrets.Reference, error) {
	return provider.value, provider.err
}

type fixedProfileNetworkSecretResolver struct {
	value netpolicy.SecretResolution
	err   error
}

func (resolver fixedProfileNetworkSecretResolver) Resolve(
	string,
) (string, error) {
	return resolver.value.Value, resolver.err
}

func (resolver fixedProfileNetworkSecretResolver) ResolveSecret(
	string,
) (netpolicy.SecretResolution, error) {
	return resolver.value, resolver.err
}

type profileNetworkEnvironmentProvider struct {
	environmentID string
	provider      *networkTransitionProviderFixture
}

func (provider profileNetworkEnvironmentProvider) ObserveNetworkRoute(
	ctx context.Context,
	environmentID string,
) (NetworkRouteConfiguration, error) {
	if environmentID != provider.environmentID {
		return NetworkRouteConfiguration{},
			ErrNetworkTransitionProviderUnavailable
	}
	return provider.provider.ObserveNetworkRoute(ctx, environmentID)
}

func (provider profileNetworkEnvironmentProvider) StageNetworkCandidate(
	ctx context.Context,
	plan NetworkTransitionPlan,
	material NetworkCandidateMaterial,
) (NetworkStagedCandidate, NetworkTransitionProof, error) {
	if plan.EnvironmentID != provider.environmentID {
		return nil, NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	return provider.provider.StageNetworkCandidate(
		ctx,
		plan,
		material,
	)
}

type profileNetworkBatchProvider struct {
	current         map[string]NetworkRouteConfiguration
	desired         NetworkRouteConfiguration
	generation      map[string]uint64
	events          []string
	failEnvironment string
	failPhase       string
	commitCheck     func() error
}

func newProfileNetworkBatchProvider(
	current map[string]NetworkRouteConfiguration,
	desired NetworkRouteConfiguration,
) *profileNetworkBatchProvider {
	cloned := make(
		map[string]NetworkRouteConfiguration,
		len(current),
	)
	generation := make(map[string]uint64, len(current))
	for environmentID, route := range current {
		cloned[environmentID] = route
		generation[environmentID] = 10
	}
	return &profileNetworkBatchProvider{
		current: cloned, desired: desired, generation: generation,
	}
}

func (provider *profileNetworkBatchProvider) ObserveNetworkRoute(
	_ context.Context,
	environmentID string,
) (NetworkRouteConfiguration, error) {
	value, ok := provider.current[environmentID]
	if !ok {
		return NetworkRouteConfiguration{},
			ErrNetworkTransitionProviderUnavailable
	}
	provider.events = append(
		provider.events,
		"observe:"+environmentID,
	)
	return value, nil
}

func (provider *profileNetworkBatchProvider) StageNetworkCandidate(
	_ context.Context,
	plan NetworkTransitionPlan,
	material NetworkCandidateMaterial,
) (NetworkStagedCandidate, NetworkTransitionProof, error) {
	if provider.current[plan.EnvironmentID] != plan.From ||
		plan.Desired != provider.desired ||
		material.UpstreamProxyURL == "" {
		return nil, NetworkTransitionProof{},
			ErrInvalidNetworkTransition
	}
	provider.events = append(
		provider.events,
		"stage:"+plan.EnvironmentID,
	)
	provider.generation[plan.EnvironmentID]++
	candidate := &profileNetworkBatchCandidate{
		provider:   provider,
		plan:       plan,
		generation: provider.generation[plan.EnvironmentID],
	}
	return candidate, candidate.proof(), nil
}

func (provider *profileNetworkBatchProvider) CommitNetworkCandidates(
	_ context.Context,
	handles []NetworkStagedCandidate,
) error {
	if provider.commitCheck != nil {
		if err := provider.commitCheck(); err != nil {
			return err
		}
	}
	if len(handles) != len(provider.current) {
		return ErrInvalidNetworkTransition
	}
	for _, handle := range handles {
		candidate, ok := handle.(*profileNetworkBatchCandidate)
		if !ok || !candidate.activated ||
			provider.current[candidate.plan.EnvironmentID] !=
				candidate.plan.Desired {
			return ErrInvalidNetworkTransition
		}
	}
	provider.events = append(provider.events, "batch-commit")
	return nil
}

func (provider *profileNetworkBatchProvider) RollbackNetworkCandidates(
	_ context.Context,
	handles []NetworkStagedCandidate,
) ([]NetworkTransitionProof, error) {
	proofs := make([]NetworkTransitionProof, len(handles))
	for index, handle := range handles {
		candidate, ok := handle.(*profileNetworkBatchCandidate)
		if !ok {
			return nil, ErrInvalidNetworkTransition
		}
		provider.generation[candidate.plan.EnvironmentID]++
		provider.current[candidate.plan.EnvironmentID] =
			candidate.plan.From
		proof := candidate.proof()
		proof.RouteGeneration =
			provider.generation[candidate.plan.EnvironmentID]
		proof.SecretGeneration =
			candidate.plan.From.ProxySecretGeneration
		proofs[index] = proof
	}
	provider.events = append(provider.events, "batch-rollback")
	return proofs, nil
}

type profileNetworkBatchCandidate struct {
	provider   *profileNetworkBatchProvider
	plan       NetworkTransitionPlan
	generation uint64
	activated  bool
}

func (candidate *profileNetworkBatchCandidate) ProbeNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	return candidate.run("probe", false)
}

func (candidate *profileNetworkBatchCandidate) ActivateNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	proof, err := candidate.run("activate", true)
	return proof, err
}

func (candidate *profileNetworkBatchCandidate) ProveNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	return candidate.run("prove", false)
}

func (candidate *profileNetworkBatchCandidate) DrainPreviousConnections(
	context.Context,
) (NetworkTransitionProof, error) {
	return candidate.run("drain", false)
}

func (candidate *profileNetworkBatchCandidate) CommitNetworkCandidate(
	context.Context,
) error {
	return errors.New("individual batch candidate commit is forbidden")
}

func (candidate *profileNetworkBatchCandidate) RollbackNetworkCandidate(
	context.Context,
) (NetworkTransitionProof, error) {
	return NetworkTransitionProof{},
		errors.New("individual batch candidate rollback is forbidden")
}

func (candidate *profileNetworkBatchCandidate) run(
	phase string,
	activate bool,
) (NetworkTransitionProof, error) {
	environmentID := candidate.plan.EnvironmentID
	candidate.provider.events = append(
		candidate.provider.events,
		phase+":"+environmentID,
	)
	if candidate.provider.failEnvironment == environmentID &&
		candidate.provider.failPhase == phase {
		return NetworkTransitionProof{},
			errors.New("injected batch provider failure")
	}
	if activate {
		candidate.activated = true
		candidate.provider.current[environmentID] =
			candidate.plan.Desired
	}
	return candidate.proof(), nil
}

func (candidate *profileNetworkBatchCandidate) proof() NetworkTransitionProof {
	return NetworkTransitionProof{
		RouteGeneration:  candidate.generation,
		SecretGeneration: candidate.plan.Desired.ProxySecretGeneration,
		ObservedAt: time.Date(
			2026, 7, 30, 10, 0, 0, 0, time.UTC,
		),
	}
}

func eventPosition(events []string, prefix string) int {
	for index, event := range events {
		if strings.HasPrefix(event, prefix) {
			return index
		}
	}
	return -1
}

func lastEventPosition(events []string, prefix string) int {
	for index := len(events) - 1; index >= 0; index-- {
		if strings.HasPrefix(events[index], prefix) {
			return index
		}
	}
	return -1
}

func countEvent(events []string, exact string) int {
	count := 0
	for _, event := range events {
		if event == exact {
			count++
		}
	}
	return count
}

var _ NetworkTransitionBatchProvider = (*profileNetworkBatchProvider)(nil)
var _ NetworkStagedCandidate = (*profileNetworkBatchCandidate)(nil)
