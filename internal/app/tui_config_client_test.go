package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestTUIConfigurationClientUsesSharedTransactionWithoutValueEcho(
	t *testing.T,
) {
	const canary = "socks5://client-user:client-password@private.invalid"
	client, store, closeServer := newTUIConfigurationClientFixture(t)
	defer closeServer()
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
				"SERVICE_ENDPOINT": canary,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.PlanConfiguration(
		context.Background(),
		manager.ConfigurationDraft{
			Schema:       manager.ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: projection.Revision,
			ClientNonce:  "tui-client-canary",
			Changes:      []manager.TypedChange{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedPlan), canary) ||
		!strings.Contains(string(encodedPlan), "[value provided]") {
		t.Fatalf("TUI plan leaked or omitted marker: %s", encodedPlan)
	}
	result, err := client.ApplyConfiguration(
		context.Background(),
		manager.ConfigurationApplyRequest{
			Schema:       manager.ConfigurationApplySchema,
			OperationID:  plan.OperationID,
			Profile:      plan.Profile,
			BaseRevision: plan.BaseRevision,
			PlanDigest:   plan.PlanDigest,
			Confirmed:    true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedResult), canary) ||
		result.Projection.Desired.Env.Public["SERVICE_ENDPOINT"] !=
			"[value provided]" {
		t.Fatalf("TUI result exposed environment value: %s", encodedResult)
	}
	persisted, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Env.Public["SERVICE_ENDPOINT"] != canary ||
		result.Operation.Phase != manager.OperationSucceeded {
		t.Fatalf(
			"shared transaction did not commit exact private value: operation=%+v persisted=%q",
			result.Operation,
			persisted.Env.Public["SERVICE_ENDPOINT"],
		)
	}
}

func TestTUIConfigurationClientMapsStalePlanWithoutMutation(
	t *testing.T,
) {
	client, store, closeServer := newTUIConfigurationClientFixture(t)
	defer closeServer()
	projection, err := (manager.ProfileProjectionService{
		Store: store,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	change, err := manager.NewTypedChange(
		manager.ChangeNetworkPosture,
		map[string]any{"mode": "proxy"},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.PlanConfiguration(
		context.Background(),
		manager.ConfigurationDraft{
			Schema:       manager.ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: projection.Revision,
			Changes:      []manager.TypedChange{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	external, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	external.Git.UserName = "external-change"
	if err := store.Save(external); err != nil {
		t.Fatal(err)
	}
	_, err = client.ApplyConfiguration(
		context.Background(),
		manager.ConfigurationApplyRequest{
			Schema:       manager.ConfigurationApplySchema,
			OperationID:  plan.OperationID,
			Profile:      plan.Profile,
			BaseRevision: plan.BaseRevision,
			PlanDigest:   plan.PlanDigest,
			Confirmed:    true,
		},
	)
	if !errors.Is(err, manager.ErrStaleConfigurationPlan) {
		t.Fatalf("stale client error=%v", err)
	}
	persisted, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Network.Mode != profile.NetworkModeDirect ||
		persisted.Git.UserName != "external-change" {
		t.Fatalf("stale apply changed desired profile: %+v", persisted)
	}
}

func TestTUIConfigurationClientUsesCanonicalProfileNetworkWireContract(
	t *testing.T,
) {
	client, store, closeServer := newTUIConfigurationClientFixture(t)
	defer closeServer()
	result, err := client.ApplyProfileNetwork(
		context.Background(),
		manager.ProfileNetworkOptions{
			ProfileName:      "default",
			Mode:             profile.NetworkModeTun2Socks,
			ProxySecretRef:   "managed-proxy",
			MediatedResolver: "1.1.1.1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied ||
		result.Network.Profile != "default" ||
		result.Network.Mode != profile.NetworkModeTun2Socks ||
		result.Network.ProxySecretRef != "managed-proxy" ||
		result.Network.MediatedResolver != "1.1.1.1" {
		t.Fatalf("profile network result mismatch: %+v", result)
	}
	persisted, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Network.Mode != profile.NetworkModeTun2Socks ||
		persisted.Network.ProxySecretRef != "managed-proxy" ||
		persisted.Network.MediatedResolver != "1.1.1.1" {
		t.Fatalf("profile network did not persist: %+v", persisted.Network)
	}
}

func TestTUIConfigurationClientPreservesAPIErrorMessageWithoutDetails(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(manager.APIResponse{
			Version: manager.APIVersion,
			Errors:  []string{"specific profile-network failure"},
		})
	}))
	defer server.Close()
	client := newTUIConfigurationClient("fixture-root")
	client.dial = func(
		string,
	) (*http.Client, string, string, error) {
		return server.Client(), server.URL, "fixture-token", nil
	}
	_, err := client.ApplyProfileNetwork(
		context.Background(),
		manager.ProfileNetworkOptions{
			ProfileName: "default",
			Mode:        profile.NetworkModeDirect,
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "specific profile-network failure") {
		t.Fatalf("API error=%v, want preserved Manager message", err)
	}
}

func TestTUILifecycleClientUsesExactAuthenticatedPlanApplyRoutes(
	t *testing.T,
) {
	const targetID = "env_clientfixture"
	plan := manager.EnvironmentActionPlan{
		Action:       manager.EnvironmentActionStop,
		RequestedIDs: []string{targetID},
		OperationID:  "op_clientfixture",
		PlanDigest:   "sha256:" + strings.Repeat("b", 64),
		Targets: []manager.EnvironmentActionTarget{{
			ID: targetID, Profile: "default", Backend: "lima",
			Status: "running", InstanceName: "hideout-client-fixture",
		}},
		Skipped: []manager.EnvironmentActionTarget{},
		Total:   1,
	}
	applied := plan.Targets[0]
	applied.Status = "stopped"
	result := manager.EnvironmentActionResult{
		Plan: plan, Applied: []manager.EnvironmentActionTarget{applied},
		Skipped:   []manager.EnvironmentActionTarget{},
		Operation: tuiLifecycleOperationFixture(plan, targetID),
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		calls++
		if request.Method != http.MethodPost ||
			request.Header.Get("Authorization") !=
				"Bearer tui-lifecycle-token" ||
			request.Host != "localhost" {
			t.Errorf(
				"request authority mismatch: method=%s host=%s auth=%q",
				request.Method,
				request.Host,
				request.Header.Get("Authorization"),
			)
		}
		var body manager.EnvironmentActionAPIRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.IDs) != 1 || body.IDs[0] != targetID ||
			body.Idle != "" || body.StoppedOnly {
			t.Errorf("lifecycle request broadened target: %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/environment/stop/plan":
			if body.OperationID != "" ||
				body.PlanDigest != "" || body.Confirmed {
				t.Errorf("plan request reused operation identity: %+v", body)
			}
			_ = json.NewEncoder(writer).Encode(manager.APIResponse{
				Version:  manager.APIVersion,
				Resource: "environment/stop/plan",
				Data:     plan,
				Errors:   []string{},
			})
		case "/api/v1/environment/stop/apply":
			if body.OperationID != plan.OperationID ||
				body.PlanDigest != plan.PlanDigest ||
				!body.Confirmed {
				t.Errorf("apply request lost reviewed identity: %+v", body)
			}
			_ = json.NewEncoder(writer).Encode(manager.APIResponse{
				Version:  manager.APIVersion,
				Resource: "environment/stop/apply",
				Data:     result,
				Errors:   []string{},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newTUIConfigurationClient("fixture-root")
	client.dial = func(
		string,
	) (*http.Client, string, string, error) {
		return server.Client(), server.URL,
			"tui-lifecycle-token", nil
	}
	request := manager.EnvironmentActionAPIRequest{
		IDs: []string{targetID},
	}
	gotPlan, err := client.PlanEnvironment(
		context.Background(),
		manager.EnvironmentActionStop,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlan.Action != manager.EnvironmentActionStop ||
		gotPlan.RequestedIDs[0] != targetID {
		t.Fatalf("plan mismatch: %+v", gotPlan)
	}
	gotResult, err := client.ApplyEnvironment(
		context.Background(),
		manager.EnvironmentActionStop,
		manager.EnvironmentActionAPIRequest{
			IDs:         []string{targetID},
			OperationID: gotPlan.OperationID,
			PlanDigest:  gotPlan.PlanDigest,
			Confirmed:   true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(gotResult.Applied) != 1 ||
		gotResult.Applied[0].ID != targetID ||
		gotResult.Applied[0].Status != "stopped" {
		t.Fatalf(
			"lifecycle result mismatch: calls=%d result=%+v",
			calls,
			gotResult,
		)
	}
}

func TestTUILifecycleClientRejectsMismatchedAuthorityResult(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_ = json.NewEncoder(writer).Encode(manager.APIResponse{
			Version:  manager.APIVersion,
			Resource: "environment/clean/plan",
			Data: manager.EnvironmentActionPlan{
				Action:      manager.EnvironmentActionClean,
				OperationID: "op_differentfixture",
				PlanDigest:  "sha256:" + strings.Repeat("c", 64),
				RequestedIDs: []string{
					"env_differentfixture",
				},
				Targets: []manager.EnvironmentActionTarget{{
					ID: "env_differentfixture",
				}},
				Skipped: []manager.EnvironmentActionTarget{},
				Total:   1,
			},
			Errors: []string{},
		})
	}))
	defer server.Close()
	client := newTUIConfigurationClient("fixture-root")
	client.dial = func(
		string,
	) (*http.Client, string, string, error) {
		return server.Client(), server.URL,
			"tui-lifecycle-token", nil
	}
	_, err := client.PlanEnvironment(
		context.Background(),
		manager.EnvironmentActionClean,
		manager.EnvironmentActionAPIRequest{
			IDs: []string{"env_expectedfixture"},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "mismatched lifecycle plan") {
		t.Fatalf("mismatched lifecycle authority error=%v", err)
	}
}

func tuiLifecycleOperationFixture(
	plan manager.EnvironmentActionPlan,
	targetID string,
) *manager.Operation {
	now := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	return &manager.Operation{
		Schema: manager.OperationSchema,
		ID:     plan.OperationID, Kind: "environment." + plan.Action,
		Owner: manager.OperationOwner{
			Kind: "environment", ID: targetID,
		},
		PlanDigest: plan.PlanDigest,
		Phase:      manager.OperationSucceeded,
		Effects: []manager.EffectResult{{
			ID: "environment-0", Kind: "prove",
			Provider: "daemon.lifecycle." + plan.Action,
			Status:   manager.EffectSucceeded,
			Evidence: []manager.EvidenceRef{{
				Code: "backend-terminal-stable",
			}},
		}},
		Result: &manager.OperationResult{
			Status:  manager.OperationSucceeded,
			Code:    "environment-action-completed",
			Summary: "The exact environment action completed.",
		},
		Recovery: manager.Recovery{
			Code:    "retry-operation",
			Summary: "Retry the same operation identity.",
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func newTUIConfigurationClientFixture(
	t *testing.T,
) (*tuiConfigurationClient, profile.Store, func()) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	desired := profile.Default("default")
	desired.Network.ProxySecretRef = "local-proxy"
	desired.Network.MediatedResolver = "1.1.1.1"
	if err := store.Save(desired); err != nil {
		t.Fatal(err)
	}
	api := manager.API{
		Core:         manager.New(store),
		Token:        "tui-client-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		AllowedHosts: []string{"localhost"},
	}
	server := httptest.NewServer(api.Handler())
	client := newTUIConfigurationClient(store.Root)
	client.dial = func(
		string,
	) (*http.Client, string, string, error) {
		return server.Client(), server.URL, "tui-client-token", nil
	}
	return client, store, server.Close
}
