package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestProfileTransactionTraceRefinesOperatorConfigurationModel(
	t *testing.T,
) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	service := NewProfileTransactionService(New(store))
	initial, err := (ProfileProjectionService{Store: store}).Load(
		"default",
	)
	if err != nil {
		t.Fatal(err)
	}

	// CreatePlan(client_a) and CreatePlan(client_b) bind two different
	// operations to the same exact profile revision.
	planA := planRefinementEnvironmentChange(
		t,
		service,
		initial.Revision,
		"CLIENT_A",
		"value-a",
		"client-a",
	)
	planB := planRefinementEnvironmentChange(
		t,
		service,
		initial.Revision,
		"CLIENT_B",
		"value-b",
		"client-b",
	)
	if planA.OperationID == planB.OperationID ||
		planA.BaseRevision != planB.BaseRevision {
		t.Fatalf("CreatePlan bindings drifted: a=%+v b=%+v", planA, planB)
	}

	// Claim/Commit(client_b).
	resultB, err := service.Apply(
		context.Background(),
		refinementConfigurationApply(planB),
	)
	if err != nil {
		t.Fatal(err)
	}
	if resultB.Operation.Phase != OperationSucceeded ||
		resultB.Projection.Revision != initial.Revision+1 ||
		countSucceededEffects(resultB.Operation) !=
			len(resultB.Operation.Effects) {
		t.Fatalf("Commit(client_b) trace=%+v", resultB)
	}

	// RejectStale(client_a) cannot add a second commit or effect.
	if _, err := service.Apply(
		context.Background(),
		refinementConfigurationApply(planA),
	); !errors.Is(err, ErrStaleConfigurationPlan) {
		t.Fatalf("RejectStale(client_a) error=%v", err)
	}
	staleOperation, err := service.operationStore().Load(
		planA.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if staleOperation.Phase != OperationCancelled ||
		countStartedEffects(staleOperation) != 0 {
		t.Fatalf(
			"stale plan crossed commit/effect boundary: %+v",
			staleOperation,
		)
	}

	// LoseResponse/RetryExact(operation_b) returns the stored terminal
	// result. The profile revision and effect count do not advance.
	replayedB, err := service.Apply(
		context.Background(),
		refinementConfigurationApply(planB),
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayedB.Operation.ID != resultB.Operation.ID ||
		replayedB.Operation.Phase != OperationSucceeded ||
		replayedB.Projection.Revision != resultB.Projection.Revision ||
		countStartedEffects(replayedB.Operation) !=
			countStartedEffects(resultB.Operation) {
		t.Fatalf(
			"RetryExact did not replay one binding/effect: first=%+v replay=%+v",
			resultB,
			replayedB,
		)
	}

	// RetryMismatch(operation_b, binding_a) is rejected without changing the
	// original operation binding or desired revision.
	mismatch := refinementConfigurationApply(planB)
	mismatch.PlanDigest = planA.PlanDigest
	if _, err := service.Apply(
		context.Background(),
		mismatch,
	); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("RetryMismatch error=%v", err)
	}
	afterMismatch, err := (ProfileProjectionService{
		Store: store,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if afterMismatch.Revision != resultB.Projection.Revision ||
		afterMismatch.Desired.Env.Public["CLIENT_B"] != "value-b" ||
		afterMismatch.Desired.Env.Public["CLIENT_A"] != "" {
		t.Fatalf(
			"mismatch changed committed binding: %+v",
			afterMismatch,
		)
	}
}

func TestSecretAndRouteTraceRefinesSecretTransitionModel(t *testing.T) {
	secretService, secretStore := newSecretServiceFixture(t)
	setPlan, err := secretService.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionSet,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstValue := secretBufferFixture(
		t,
		"socks5://first-user:first-password@127.0.0.1:7890",
	)
	first, err := secretService.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema:      SecretApplySchema,
			OperationID: setPlan.OperationID,
			PlanDigest:  setPlan.PlanDigest,
			Ref:         setPlan.Ref,
			Action:      setPlan.Action,
			Confirmed:   true,
			Value:       firstValue,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertManagerSecretBufferCleared(t, firstValue)
	if first.Reference.Generation != 1 ||
		first.Reference.Availability != secrets.AvailabilityAvailable {
		t.Fatalf("initial secret generation=%+v", first)
	}

	rotatePlan, err := secretService.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rotatedValue := secretBufferFixture(
		t,
		"socks5://rotated-user:rotated-password@127.0.0.1:7890",
	)
	rotated, err := secretService.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema:      SecretApplySchema,
			OperationID: rotatePlan.OperationID,
			PlanDigest:  rotatePlan.PlanDigest,
			Ref:         rotatePlan.Ref,
			Action:      rotatePlan.Action,
			Confirmed:   true,
			Value:       rotatedValue,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertManagerSecretBufferCleared(t, rotatedValue)
	if rotated.Reference.Generation != 2 ||
		secretStore.writeCount() != 2 {
		t.Fatalf(
			"secret activation generation=%+v writes=%d",
			rotated,
			secretStore.writeCount(),
		)
	}

	// LoseResponse/RetryExact returns generation 2 without a third provider
	// effect and without requiring the secret value again.
	replayed, err := secretService.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema:      SecretApplySchema,
			OperationID: rotatePlan.OperationID,
			PlanDigest:  rotatePlan.PlanDigest,
			Ref:         rotatePlan.Ref,
			Action:      rotatePlan.Action,
			Confirmed:   true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Operation.ID != rotated.Operation.ID ||
		replayed.Reference.Generation != 2 ||
		secretStore.writeCount() != 2 {
		t.Fatalf(
			"secret RetryExact replay=%+v writes=%d",
			replayed,
			secretStore.writeCount(),
		)
	}

	current := NetworkRouteConfiguration{
		Mode:                  netpolicy.ModeTun2Socks,
		ProxySecretRef:        "local-proxy",
		ProxySecretGeneration: 1,
		MediatedResolver:      "1.1.1.1",
	}
	desired := current
	desired.ProxySecretGeneration = 2
	provider := newSuccessfulNetworkTransitionProvider(
		current,
		desired,
		2,
	)
	transition := NetworkTransitionService{
		Provider: provider,
		Sessions: fixedNetworkTransitionSessions(
			NetworkTransitionSession{
				ID:     "ses_existing_a",
				Status: NetworkSessionLive,
				Phase:  "running",
			},
		),
	}
	plan, err := transition.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: "env_refinement",
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transition.Apply(
		context.Background(),
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://rotated-user:rotated-password@127.0.0.1:7890",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantSuccessTrace := []string{
		"observe", "observe", "stage", "probe", "activate",
		"prove", "drain", "commit",
	}
	if !reflect.DeepEqual(provider.events, wantSuccessTrace) ||
		result.Phase != NetworkTransitionSucceeded ||
		!result.EffectiveProved ||
		result.Effective != desired ||
		result.ConnectionsRetained != 2 {
		t.Fatalf(
			"successful SecretTransition refinement events=%v result=%+v",
			provider.events,
			result,
		)
	}

	// A post-activation proof failure executes one rollback and proves the
	// previous route effective. Existing connections remain represented by
	// the retained count rather than being killed during pointer rollback.
	rollbackProvider := newSuccessfulNetworkTransitionProvider(
		current,
		desired,
		2,
	)
	rollbackProvider.failEvent = "prove"
	rollbackProvider.failure = errors.New("refinement proof failure")
	rollbackService := NetworkTransitionService{
		Provider: rollbackProvider,
		Sessions: fixedNetworkTransitionSessions(),
	}
	rollbackPlan, err := rollbackService.Plan(
		context.Background(),
		NetworkTransitionDraft{
			EnvironmentID: "env_refinement",
			Desired:       desired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rollbackResult, err := rollbackService.Apply(
		context.Background(),
		rollbackPlan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: "socks5://127.0.0.1:7890",
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) ||
		rollbackResult.Phase != NetworkTransitionRolledBack ||
		!rollbackResult.EffectiveProved ||
		rollbackResult.Effective != current ||
		countTraceEvent(rollbackProvider.events, "activate") != 1 ||
		countTraceEvent(rollbackProvider.events, "rollback") != 1 {
		t.Fatalf(
			"rollback SecretTransition refinement events=%v result=%+v err=%v",
			rollbackProvider.events,
			rollbackResult,
			err,
		)
	}

	encoded, err := json.Marshal(struct {
		SetPlan        SecretPlan              `json:"setPlan"`
		RotatePlan     SecretPlan              `json:"rotatePlan"`
		TransitionPlan NetworkTransitionPlan   `json:"transitionPlan"`
		Result         NetworkTransitionResult `json:"result"`
	}{
		SetPlan: setPlan, RotatePlan: rotatePlan,
		TransitionPlan: plan, Result: result,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"first-password",
		"rotated-password",
		"rotated-user",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf(
				"refinement evidence leaked %q: %s",
				forbidden,
				encoded,
			)
		}
	}
}

func TestSecretAuthorityResetRecoveryTraceRefinesSecretTransitionModel(
	t *testing.T,
) {
	t.Run("committed exact provider and route proofs succeed without replay", func(t *testing.T) {
		service, store, provider, _ := newLiveSecretRotationFixture(t)
		plan, err := service.Plan(
			context.Background(),
			SecretDraft{
				Schema: SecretDraftSchema,
				Ref:    "local-proxy",
				Action: secrets.ActionRotate,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		stageSecretLiveRouteProofs(t, service, plan)
		secretEffect, err := secretProviderEffect(
			plan,
			store.Provider(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, execute, err := service.operations().BeginEffect(
			plan.OperationID,
			secretEffect.ID,
			secretEffect.Provider,
		); err != nil || !execute {
			t.Fatalf(
				"begin provider effect execute=%t err=%v",
				execute,
				err,
			)
		}
		value := secretBufferFixture(
			t,
			"socks5://committed-user:committed-password@127.0.0.1:7890",
		)
		if _, err := store.Set(
			context.Background(),
			secrets.WriteRequest{
				Ref:                plan.Ref,
				OperationID:        plan.OperationID,
				ExpectedGeneration: plan.BaseGeneration,
				Value:              value,
			},
		); err != nil {
			t.Fatal(err)
		}
		value.Clear()
		eventsBeforeReset := append([]string(nil), provider.events...)

		restarted := NewSecretService(service.Core, store)
		restarted.now = service.now
		result, err := restarted.
			ReconcileOperationAfterNetworkAuthorityReset(
				context.Background(),
				plan.OperationID,
				NetworkAuthorityResetProof{
					AuthorityID: "daemon_refinement-committed",
					ObservedAt:  service.nowUTC(),
				},
			)
		if err != nil ||
			result.Operation.Phase != OperationSucceeded ||
			result.Reference.Generation != plan.NextGeneration ||
			store.writeCount() != 1 ||
			!reflect.DeepEqual(provider.events, eventsBeforeReset) {
			t.Fatalf(
				"committed reset trace operation=%+v reference=%+v events=%v writes=%d err=%v",
				result.Operation,
				result.Reference,
				provider.events,
				store.writeCount(),
				err,
			)
		}
		for _, transition := range plan.networkTransitions {
			for _, planned := range transition.Effects {
				effect := operationEffect(
					result.Operation,
					profileNetworkEffectID(
						transition.EnvironmentID,
						planned.ID,
					),
				)
				if effect == nil ||
					!effectProvesPlannedRequirements(
						*effect,
						planned,
					) {
					t.Fatalf(
						"committed reset route proof=%+v planned=%+v",
						effect,
						planned,
					)
				}
			}
		}
	})

	t.Run("uncommitted exact provider aborts after authority reset", func(t *testing.T) {
		service, store, provider, _ := newLiveSecretRotationFixture(t)
		plan, err := service.Plan(
			context.Background(),
			SecretDraft{
				Schema: SecretDraftSchema,
				Ref:    "local-proxy",
				Action: secrets.ActionRotate,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		stageSecretLiveRouteProofs(t, service, plan)
		eventsBeforeReset := append([]string(nil), provider.events...)

		restarted := NewSecretService(service.Core, store)
		restarted.now = service.now
		result, err := restarted.
			ReconcileOperationAfterNetworkAuthorityReset(
				context.Background(),
				plan.OperationID,
				NetworkAuthorityResetProof{
					AuthorityID: "daemon_refinement-uncommitted",
					ObservedAt:  service.nowUTC(),
				},
			)
		if err != nil ||
			result.Operation.Phase != OperationFailed ||
			result.Reference.Generation != plan.BaseGeneration ||
			store.writeCount() != 0 ||
			!reflect.DeepEqual(provider.events, eventsBeforeReset) {
			t.Fatalf(
				"uncommitted reset trace operation=%+v reference=%+v events=%v writes=%d err=%v",
				result.Operation,
				result.Reference,
				provider.events,
				store.writeCount(),
				err,
			)
		}
		for _, effect := range result.Operation.Effects {
			if strings.HasPrefix(effect.ID, "network.") &&
				effect.Status != EffectRolledBack {
				t.Fatalf(
					"uncommitted reset did not invalidate route effect: %+v",
					effect,
				)
			}
		}
	})

	t.Run("provider mismatch remains recovery required", func(t *testing.T) {
		service, store, provider, _ := newLiveSecretRotationFixture(t)
		plan, err := service.Plan(
			context.Background(),
			SecretDraft{
				Schema: SecretDraftSchema,
				Ref:    "local-proxy",
				Action: secrets.ActionRotate,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		stageSecretLiveRouteProofs(t, service, plan)
		store.mu.Lock()
		store.reference.Generation = plan.NextGeneration + 1
		store.reference.UpdatedAt = service.nowUTC()
		store.mu.Unlock()
		eventsBeforeReset := append([]string(nil), provider.events...)

		restarted := NewSecretService(service.Core, store)
		restarted.now = service.now
		result, err := restarted.
			ReconcileOperationAfterNetworkAuthorityReset(
				context.Background(),
				plan.OperationID,
				NetworkAuthorityResetProof{
					AuthorityID: "daemon_refinement-mismatch",
					ObservedAt:  service.nowUTC(),
				},
			)
		if !errors.Is(err, ErrSecretRecoveryRequired) ||
			result.Operation.Phase != OperationRecoveryRequired ||
			store.writeCount() != 0 ||
			!reflect.DeepEqual(provider.events, eventsBeforeReset) {
			t.Fatalf(
				"mismatch reset trace operation=%+v events=%v writes=%d err=%v",
				result.Operation,
				provider.events,
				store.writeCount(),
				err,
			)
		}
	})
}

func TestFormalReleaseGateEnumeratesEveryTLAConfiguration(t *testing.T) {
	root := filepath.Join("..", "..")
	inventoryData, err := os.ReadFile(
		filepath.Join(root, "formal", "inventory.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Schema    string `json:"schema"`
		TLA2Tools struct {
			Version string `json:"version"`
			SHA256  string `json:"sha256"`
			URL     string `json:"url"`
		} `json:"tla2tools"`
		Configurations []struct {
			ID     string `json:"id"`
			Module string `json:"module"`
			Config string `json:"config"`
			Kind   string `json:"kind"`
		} `json:"configurations"`
		GoRefinement struct {
			Packages []struct {
				Path       string `json:"path"`
				ImportPath string `json:"importPath"`
			} `json:"packages"`
			Tests []struct {
				Package        string `json:"package"`
				Name           string `json:"name"`
				Source         string `json:"source"`
				Classification string `json:"classification"`
			} `json:"tests"`
		} `json:"goRefinement"`
	}
	decoder := json.NewDecoder(bytes.NewReader(inventoryData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("formal inventory has trailing JSON: %v", err)
	}
	if inventory.Schema != "hideout.formal-inventory/v1" {
		t.Fatalf("formal inventory schema=%q", inventory.Schema)
	}
	if len(inventory.Configurations) != 16 {
		t.Fatalf(
			"formal inventory configurations=%d want 16",
			len(inventory.Configurations),
		)
	}
	if len(inventory.GoRefinement.Tests) != 27 {
		t.Fatalf(
			"formal inventory Go tests=%d want 27",
			len(inventory.GoRefinement.Tests),
		)
	}

	repositoryConfigurations, err := filepath.Glob(
		filepath.Join(root, "formal", "*.cfg"),
	)
	if err != nil {
		t.Fatal(err)
	}
	featureConfigurations, err := filepath.Glob(
		filepath.Join(root, "formal", "cfg", "*.cfg"),
	)
	if err != nil {
		t.Fatal(err)
	}
	repositoryConfigurations = append(
		repositoryConfigurations,
		featureConfigurations...,
	)
	inventoriedConfigs := make(map[string]string)
	inventoriedModules := make(map[string]struct{})
	configurationIDs := make(map[string]struct{})
	for _, configuration := range inventory.Configurations {
		if _, exists := configurationIDs[configuration.ID]; exists {
			t.Fatalf("duplicate formal configuration id %q", configuration.ID)
		}
		configurationIDs[configuration.ID] = struct{}{}
		if _, exists := inventoriedConfigs[configuration.Config]; exists {
			t.Fatalf(
				"duplicate formal configuration path %q",
				configuration.Config,
			)
		}
		inventoriedConfigs[configuration.Config] = configuration.Module
		inventoriedModules[configuration.Module] = struct{}{}
		for _, path := range []string{
			configuration.Config,
			configuration.Module,
		} {
			info, statErr := os.Lstat(filepath.Join(root, path))
			if statErr != nil {
				t.Fatalf("%s is missing: %v", path, statErr)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("%s is not a regular file", path)
			}
		}
	}
	if len(inventoriedModules) != 12 {
		t.Fatalf("formal inventory modules=%d want 12", len(inventoriedModules))
	}
	if len(repositoryConfigurations) != len(inventoriedConfigs) {
		t.Fatalf(
			"repository cfg count=%d inventory count=%d",
			len(repositoryConfigurations),
			len(inventoriedConfigs),
		)
	}
	for _, configuration := range repositoryConfigurations {
		relative, relativeErr := filepath.Rel(root, configuration)
		if relativeErr != nil {
			t.Fatal(relativeErr)
		}
		relative = filepath.ToSlash(relative)
		if _, exists := inventoriedConfigs[relative]; !exists {
			t.Fatalf("formal inventory omits %s", relative)
		}
	}
	repositoryModules, err := filepath.Glob(
		filepath.Join(root, "formal", "*.tla"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositoryModules) != len(inventoriedModules) {
		t.Fatalf(
			"repository module count=%d inventory count=%d",
			len(repositoryModules),
			len(inventoriedModules),
		)
	}
	for _, module := range repositoryModules {
		relative, relativeErr := filepath.Rel(root, module)
		if relativeErr != nil {
			t.Fatal(relativeErr)
		}
		relative = filepath.ToSlash(relative)
		if _, exists := inventoriedModules[relative]; !exists {
			t.Fatalf("formal inventory omits module %s", relative)
		}
	}

	testIDs := make(map[string]struct{})
	for _, test := range inventory.GoRefinement.Tests {
		id := test.Package + "::" + test.Name
		if _, exists := testIDs[id]; exists {
			t.Fatalf("duplicate formal Go test %s", id)
		}
		testIDs[id] = struct{}{}
		source, readErr := os.ReadFile(filepath.Join(root, test.Source))
		if readErr != nil {
			t.Fatalf("read %s: %v", test.Source, readErr)
		}
		if !strings.Contains(
			string(source),
			"func "+test.Name,
		) {
			t.Fatalf(
				"formal Go inventory maps %s to missing function in %s",
				test.Name,
				test.Source,
			)
		}
	}

	gate, err := os.ReadFile(
		filepath.Join(root, "scripts", "gates", "formal.sh"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"formal/inventory.json",
		"formal-verify.sh",
		"go test -json",
	} {
		if !strings.Contains(string(gate), required) {
			t.Fatalf("formal release gate omits %q", required)
		}
	}
	gateZero, err := os.ReadFile(
		filepath.Join(root, "scripts", "test-gate0.sh"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(gateZero),
		"scripts/gates/formal.sh",
	) {
		t.Fatal("Gate 0 does not execute the consolidated formal gate")
	}
}

func planRefinementEnvironmentChange(
	t *testing.T,
	service *ProfileTransactionService,
	baseRevision uint64,
	name string,
	value string,
	nonce string,
) ConfigurationPlan {
	t.Helper()
	change, err := NewTypedChange(
		ChangeProfileEnvironment,
		map[string]any{
			"set": map[string]string{name: value},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(
		context.Background(),
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: baseRevision,
			ClientNonce:  nonce,
			Changes:      []TypedChange{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func refinementConfigurationApply(
	plan ConfigurationPlan,
) ConfigurationApplyRequest {
	return ConfigurationApplyRequest{
		Schema:       ConfigurationApplySchema,
		OperationID:  plan.OperationID,
		Profile:      plan.Profile,
		BaseRevision: plan.BaseRevision,
		PlanDigest:   plan.PlanDigest,
		Confirmed:    true,
	}
}

func countStartedEffects(operation Operation) int {
	count := 0
	for _, effect := range operation.Effects {
		if effect.Status != EffectPending {
			count++
		}
	}
	return count
}

func countSucceededEffects(operation Operation) int {
	count := 0
	for _, effect := range operation.Effects {
		if effect.Status == EffectSucceeded {
			count++
		}
	}
	return count
}

func countTraceEvent(events []string, expected string) int {
	count := 0
	for _, event := range events {
		if event == expected {
			count++
		}
	}
	return count
}
