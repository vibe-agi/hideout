package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

func TestStartupOperationRecoveryLeavesUnconfirmedPlanUntouched(t *testing.T) {
	store := manager.OperationStore{Root: t.TempDir()}
	operation := reserveStartupRecoveryOperation(
		t,
		store,
		"op_startplan0001",
		profileStartupRecoveryBinding("op_startplan0001"),
	)
	calls := 0
	recovery := startupOperationRecovery{
		Store:      store,
		Operations: manager.OperationService{Store: store},
		ReconcileProfile: func(
			context.Context,
			string,
		) (manager.Operation, error) {
			calls++
			return manager.Operation{}, nil
		},
	}

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || current.Phase != manager.OperationPlanned {
		t.Fatalf("planned operation changed: calls=%d operation=%+v", calls, current)
	}
}

func TestStartupOperationRecoveryCompletesProvedProfileEffectOnce(t *testing.T) {
	store := manager.OperationStore{Root: t.TempDir()}
	operation := reserveStartupRecoveryOperation(
		t,
		store,
		"op_startprofile1",
		profileStartupRecoveryBinding("op_startprofile1"),
	)
	transitionStartupRecoveryOperation(
		t,
		store,
		operation.ID,
		manager.OperationClaimed,
		manager.OperationStaging,
	)
	operations := manager.OperationService{Store: store}
	calls := 0
	recovery := startupOperationRecovery{
		Store:      store,
		Operations: operations,
		ReconcileProfile: func(
			_ context.Context,
			id string,
		) (manager.Operation, error) {
			calls++
			current, execute, err := operations.BeginEffect(
				id,
				"persist-profile",
				"manager.profile",
			)
			if err != nil {
				return current, err
			}
			if execute {
				current, err = operations.FinishEffect(
					id,
					"persist-profile",
					"manager.profile",
					manager.EffectSucceeded,
					[]manager.EvidenceRef{{
						Code: "profile-persisted",
					}},
				)
				if err != nil {
					return current, err
				}
			}
			return operations.Terminal(
				id,
				manager.OperationSucceeded,
				"profile-committed",
				"The reviewed profile configuration was committed.",
			)
		},
	}

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || current.Phase != manager.OperationSucceeded ||
		current.Effects[0].Status != manager.EffectSucceeded ||
		len(current.Effects[0].Evidence) != 1 {
		t.Fatalf("startup reconciliation was not idempotent: calls=%d operation=%+v", calls, current)
	}
}

func TestStartupOperationRecoveryMarksUnknownProviderRequired(t *testing.T) {
	store := manager.OperationStore{Root: t.TempDir()}
	binding := profileStartupRecoveryBinding("op_startunknown1")
	binding.Kind = "adapter.transaction"
	operation := reserveStartupRecoveryOperation(
		t,
		store,
		binding.ID,
		binding,
	)
	transitionStartupRecoveryOperation(
		t,
		store,
		operation.ID,
		manager.OperationClaimed,
	)
	recovery := startupOperationRecovery{
		Store:      store,
		Operations: manager.OperationService{Store: store},
	}

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != manager.OperationRecoveryRequired ||
		current.Recovery.Code != "operation-provider-unavailable" {
		t.Fatalf("unknown provider recovery=%+v", current)
	}
}

func TestStartupOperationRecoveryDispatchesNetworkObservation(t *testing.T) {
	store := manager.OperationStore{Root: t.TempDir()}
	operation := reserveStartupRecoveryOperation(
		t,
		store,
		"op_startnetwork1",
		manager.OperationBinding{
			ID:           "op_startnetwork1",
			Kind:         "network.transition",
			Owner:        manager.OperationOwner{Kind: "environment", ID: "env_recovery"},
			PlanDigest:   startupRecoveryDigest("c"),
			BaseRevision: 2,
		},
	)
	transitionStartupRecoveryOperation(
		t,
		store,
		operation.ID,
		manager.OperationClaimed,
	)
	operations := manager.OperationService{Store: store}
	calls := 0
	recovery := startupOperationRecovery{
		Store:      store,
		Operations: operations,
		ReconcileNetwork: func(
			_ context.Context,
			id string,
		) (manager.Operation, error) {
			calls++
			return operations.RequireRecovery(
				id,
				"network-route-observation-unproved",
				"The exact route binding cannot be proved.",
				"Inspect the route before retrying.",
			)
		},
	}

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 ||
		current.Phase != manager.OperationRecoveryRequired ||
		current.Recovery.Code != "network-route-observation-unproved" {
		t.Fatalf(
			"network recovery was not dispatched: calls=%d operation=%+v",
			calls,
			current,
		)
	}
}

func TestStartupOperationRecoveryRedactsRawProviderFailure(t *testing.T) {
	store := manager.OperationStore{Root: t.TempDir()}
	operation := reserveStartupRecoveryOperation(
		t,
		store,
		"op_startsecret1",
		manager.OperationBinding{
			ID:           "op_startsecret1",
			Kind:         "secret.rotate",
			Owner:        manager.OperationOwner{Kind: "secret", ID: "local-proxy"},
			PlanDigest:   startupRecoveryDigest("b"),
			BaseRevision: 4,
		},
	)
	transitionStartupRecoveryOperation(
		t,
		store,
		operation.ID,
		manager.OperationClaimed,
		manager.OperationStaging,
	)
	operations := manager.OperationService{Store: store}
	if _, execute, err := operations.BeginEffect(
		operation.ID,
		"persist-profile",
		"manager.profile",
	); err != nil || !execute {
		t.Fatalf("begin running effect execute=%t err=%v", execute, err)
	}
	var audits []map[string]any
	recovery := startupOperationRecovery{
		Store:      store,
		Operations: operations,
		ReconcileSecret: func(
			context.Context,
			string,
		) (manager.Operation, error) {
			return manager.Operation{},
				errors.New("socks5://admin:top-secret@127.0.0.1:7890")
		},
		Record: func(_ string, _ string, details map[string]any) {
			audits = append(audits, details)
		},
	}

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != manager.OperationRecoveryRequired ||
		current.Recovery.Code != "provider-completion-unproved" {
		t.Fatalf("running provider failure recovery=%+v", current)
	}
	encoded, err := json.Marshal(struct {
		Operation manager.Operation
		Audits    []map[string]any
	}{Operation: current, Audits: audits})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"top-secret",
		"admin:",
		"socks5://",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("provider error leaked %q into durable or audit data: %s", forbidden, encoded)
		}
	}
}

func TestDaemonStartReconcilesAcceptedProfileBeforeServing(t *testing.T) {
	store := testStore(t)
	core := manager.New(store)
	projection, err := (manager.ProfileProjectionService{
		Store: store,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	change, err := manager.NewTypedChange(
		manager.ChangeProfileEnvironment,
		map[string]any{
			"set": map[string]string{
				"STARTUP_RECOVERY": "proved",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manager.NewProfileTransactionService(core).Plan(
		context.Background(),
		manager.ConfigurationDraft{
			Schema:       manager.ConfigurationDraftSchema,
			Profile:      projection.Profile,
			BaseRevision: projection.Revision,
			ClientNonce:  "daemon-startup-recovery",
			Changes:      []manager.TypedChange{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	operationStore := manager.OperationStore{Root: store.Root}
	if _, err := operationStore.Transition(
		plan.OperationID,
		manager.OperationClaimed,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	d, err := Start(Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.Stop(context.Background())
	})

	operation, err := operationStore.Load(plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != manager.OperationSucceeded ||
		operation.Effects[0].Status != manager.EffectSucceeded ||
		len(operation.Effects[0].Evidence) == 0 ||
		current.Env.Public["STARTUP_RECOVERY"] != "proved" {
		t.Fatalf(
			"daemon served before accepted profile operation was proved: operation=%+v profile=%+v",
			operation,
			current.Env.Public,
		)
	}
}

func TestDaemonStartReconcilesAcceptedNetworkWithoutRouteReplay(
	t *testing.T,
) {
	store := testStore(t)
	current := manager.NetworkRouteConfiguration{
		Mode:                  netpolicy.ModeTun2Socks,
		ProxySecretRef:        "local-proxy",
		ProxySecretGeneration: 1,
		MediatedResolver:      "1.1.1.1",
	}
	desired := current
	desired.ProxySecretGeneration = 2
	provider := &daemonNetworkPlanProvider{current: current}
	plan, err := (manager.NetworkTransitionService{
		Provider: provider,
	}).Plan(
		context.Background(),
		manager.NetworkTransitionDraft{
			EnvironmentID: "env_recovery",
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	operationStore := manager.OperationStore{Root: store.Root}
	recovery := manager.NetworkTransitionRecoveryService{
		Store: operationStore,
	}
	operation, err := recovery.Reserve(
		"op_daemonnetwork01",
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operationStore.Transition(
		operation.ID,
		manager.OperationClaimed,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	d, err := Start(Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.Stop(context.Background())
	})
	reconciled, err := operationStore.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Phase != manager.OperationRecoveryRequired ||
		reconciled.Recovery.Code != "network-provider-unavailable" ||
		provider.stageCalls != 0 {
		t.Fatalf(
			"daemon network recovery replayed or falsely committed: operation=%+v stageCalls=%d",
			reconciled,
			provider.stageCalls,
		)
	}
}

func TestDaemonSnapshotReseedsRecoveredOperationWithoutAdoptingOrphan(t *testing.T) {
	store := testStore(t)
	operationStore := manager.OperationStore{Root: store.Root}
	binding := profileStartupRecoveryBinding("op_startreseed01")
	binding.Kind = "adapter.transaction"
	operation := reserveStartupRecoveryOperation(
		t,
		operationStore,
		binding.ID,
		binding,
	)
	transitionStartupRecoveryOperation(
		t,
		operationStore,
		operation.ID,
		manager.OperationClaimed,
	)
	d, err := Start(Options{
		Store: store,
		LiveResources: func(string) []LiveResource {
			return []LiveResource{{
				ID: "env-unproved-survivor", Kind: "environment",
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.Stop(context.Background())
	})

	snapshot, err := d.operatorSnapshot(
		context.Background(),
		manager.OperatorSnapshotQuery{},
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := liveconsole.NewStateFromOperatorSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sequence == 0 ||
		len(snapshot.Operations) != 1 ||
		snapshot.Operations[0].ID != operation.ID ||
		snapshot.Operations[0].Phase !=
			manager.OperationRecoveryRequired ||
		len(state.Operations) != 1 ||
		state.Operations[0].Recovery.Code !=
			"operation-provider-unavailable" {
		t.Fatalf(
			"recovered operation was not reseeded: snapshot=%+v state=%+v",
			snapshot.Operations,
			state.Operations,
		)
	}
	if len(d.Orphans()) != 1 ||
		d.Orphans()[0].ID != "env-unproved-survivor" ||
		d.own.owns("env-unproved-survivor") {
		t.Fatalf(
			"unproved live resource was adopted during reseed: orphans=%+v",
			d.Orphans(),
		)
	}
}

func reserveStartupRecoveryOperation(
	t *testing.T,
	store manager.OperationStore,
	id string,
	binding manager.OperationBinding,
) manager.Operation {
	t.Helper()
	binding.ID = id
	operation, created, err := store.Reserve(
		binding,
		[]manager.EffectResult{{
			ID:       "persist-profile",
			Kind:     "persist",
			Provider: "manager.profile",
			Status:   manager.EffectPending,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatalf("operation %s was not created", id)
	}
	return operation
}

func transitionStartupRecoveryOperation(
	t *testing.T,
	store manager.OperationStore,
	id string,
	phases ...string,
) {
	t.Helper()
	for _, phase := range phases {
		if _, err := store.Transition(id, phase, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func profileStartupRecoveryBinding(id string) manager.OperationBinding {
	return manager.OperationBinding{
		ID:           id,
		Kind:         "profile.transaction",
		Owner:        manager.OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest:   startupRecoveryDigest("a"),
		BaseRevision: 1,
	}
}

func startupRecoveryDigest(value string) string {
	return "sha256:" + strings.Repeat(value, 64)
}

type daemonNetworkPlanProvider struct {
	current    manager.NetworkRouteConfiguration
	stageCalls int
}

func (provider *daemonNetworkPlanProvider) ObserveNetworkRoute(
	context.Context,
	string,
) (manager.NetworkRouteConfiguration, error) {
	return provider.current, nil
}

func (provider *daemonNetworkPlanProvider) StageNetworkCandidate(
	context.Context,
	manager.NetworkTransitionPlan,
	manager.NetworkCandidateMaterial,
) (
	manager.NetworkStagedCandidate,
	manager.NetworkTransitionProof,
	error,
) {
	provider.stageCalls++
	return nil, manager.NetworkTransitionProof{}, errors.New(
		"network stage must not be called during daemon recovery",
	)
}
