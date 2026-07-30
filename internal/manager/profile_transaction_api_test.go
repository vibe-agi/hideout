package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestProfileTransactionAPIRoutesAreSensitivePrivateAndBounded(t *testing.T) {
	want := map[string]bool{
		"profile/transaction/plan":  false,
		"profile/transaction/apply": false,
	}
	for _, spec := range ManagerRoutes() {
		if _, ok := want[spec.Resource]; !ok {
			continue
		}
		if spec.Method != http.MethodPost ||
			!spec.Sensitive ||
			spec.MaxRequestBodyBytes != DefaultRequestBodyLimit ||
			!spec.NoStore ||
			!spec.NoBodyLog {
			t.Fatalf("configuration transaction route contract=%+v", spec)
		}
		want[spec.Resource] = true
	}
	for resource, present := range want {
		if !present {
			t.Fatalf("configuration transaction route %q is missing", resource)
		}
	}
}

func TestProfileTransactionAPIPlanApplyAndReplayDoNotEchoEnvironmentValue(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if _, err := store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(New(store), "ui_token", time.Minute)
	projection, err := (ProfileProjectionService{Store: store}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	const canary = "socks5://api-user:api-password@127.0.0.1:7890"
	change, err := NewTypedChange(ChangeProfileEnvironment, map[string]any{
		"set": map[string]string{"LOCAL_PROXY": canary},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      "default",
		BaseRevision: projection.Revision,
		ClientNonce:  "api-private-review",
		Changes:      []TypedChange{change},
	}

	planResponse := callProfileTransactionAPI(
		t,
		api,
		"profile/transaction/plan",
		draft,
	)
	if planResponse.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", planResponse.Code, planResponse.Body)
	}
	if strings.Contains(planResponse.Body.String(), canary) ||
		strings.Contains(planResponse.Body.String(), "api-password") {
		t.Fatalf("configuration plan exposed environment value: %s", planResponse.Body)
	}
	var plan ConfigurationPlan
	decodeProfileTransactionAPIData(t, planResponse, &plan)
	if err := plan.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	if len(plan.CanonicalChanges) != 1 ||
		!strings.Contains(string(plan.CanonicalChanges[0].Value), "[value provided]") {
		t.Fatalf("public canonical changes=%+v", plan.CanonicalChanges)
	}

	request := configurationApplyRequest(plan)
	applyResponse := callProfileTransactionAPI(
		t,
		api,
		"profile/transaction/apply",
		request,
	)
	if applyResponse.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", applyResponse.Code, applyResponse.Body)
	}
	if strings.Contains(applyResponse.Body.String(), canary) ||
		strings.Contains(applyResponse.Body.String(), "api-password") {
		t.Fatalf("configuration apply exposed environment value: %s", applyResponse.Body)
	}
	var applied ConfigurationApplyResult
	decodeProfileTransactionAPIData(t, applyResponse, &applied)
	if applied.Operation.Kind != profileTransactionOperationKind ||
		applied.Operation.Phase != OperationSucceeded {
		t.Fatalf("configuration apply result=%+v", applied)
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["LOCAL_PROXY"] != canary {
		t.Fatalf("private environment value was not applied: %+v", current.Env.Public)
	}

	replayResponse := callProfileTransactionAPI(
		t,
		api,
		"profile/transaction/apply",
		request,
	)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replayResponse.Code, replayResponse.Body)
	}
	var replayed ConfigurationApplyResult
	decodeProfileTransactionAPIData(t, replayResponse, &replayed)
	if replayed.Operation.ID != applied.Operation.ID ||
		replayed.Operation.Phase != OperationSucceeded ||
		replayed.Projection.Revision != applied.Projection.Revision {
		t.Fatalf("idempotent replay=%+v first=%+v", replayed, applied)
	}
}

func TestLegacyProfileEnvApplyUsesDurableSharedTransaction(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	api := NewAPI(New(store), "ui_token", time.Minute)
	const canary = "legacy-shared-transaction-value"
	response := callProfileTransactionAPI(
		t,
		api,
		"profile/env/apply",
		ProfileEnvAPIRequest{
			ProfileName: "default",
			Operation:   "set",
			Name:        "TRANSACTION_PARITY",
			Value:       canary,
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy apply status=%d body=%s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatalf("legacy response exposed environment value: %s", response.Body)
	}
	var legacy ProfileEnvResult
	decodeProfileTransactionAPIData(t, response, &legacy)
	if !legacy.Applied ||
		len(legacy.Public) != 1 ||
		legacy.Public[0] != "TRANSACTION_PARITY" {
		t.Fatalf("legacy response shape changed: %+v", legacy)
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["TRANSACTION_PARITY"] != canary {
		t.Fatalf("legacy final state=%+v", current.Env.Public)
	}
	operations, err := (OperationStore{Root: store.Root}).List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 ||
		operations[0].Kind != profileTransactionOperationKind ||
		operations[0].Phase != OperationSucceeded {
		t.Fatalf("legacy route did not use shared transaction authority: %+v", operations)
	}
}

func TestLegacyProfileNetworkApplyMatchesGenericTransactionFinalState(t *testing.T) {
	legacyStore := profile.Store{Root: t.TempDir()}
	legacyAPI := NewAPI(New(legacyStore), "ui_token", time.Minute)
	legacyResponse := callProfileTransactionAPI(
		t,
		legacyAPI,
		"profile/network/apply",
		ProfileNetworkAPIRequest{
			ProfileName:      "default",
			Mode:             profile.NetworkModeTun2Socks,
			ProxySecretRef:   "local-proxy",
			MediatedResolver: "1.1.1.1",
		},
	)
	if legacyResponse.Code != http.StatusOK {
		t.Fatalf(
			"legacy network apply status=%d body=%s",
			legacyResponse.Code,
			legacyResponse.Body,
		)
	}
	var legacyResult ProfileNetworkResult
	decodeProfileTransactionAPIData(t, legacyResponse, &legacyResult)
	if !legacyResult.Applied ||
		legacyResult.Network.ProxySecretRef != "local-proxy" {
		t.Fatalf("legacy network response shape changed: %+v", legacyResult)
	}

	genericStore := profile.Store{Root: t.TempDir()}
	if _, err := genericStore.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	genericAPI := NewAPI(New(genericStore), "ui_token", time.Minute)
	projection, err := (ProfileProjectionService{
		Store: genericStore,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	posture, err := NewTypedChange(ChangeNetworkPosture, map[string]any{
		"mode": "proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyRef, err := NewTypedChange(ChangeNetworkProxyRef, map[string]any{
		"ref": "local-proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	dns, err := NewTypedChange(ChangeNetworkDNS, map[string]any{
		"mode": "doh", "serverIp": "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	planResponse := callProfileTransactionAPI(
		t,
		genericAPI,
		"profile/transaction/plan",
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: projection.Revision,
			ClientNonce:  "network-parity",
			Changes:      []TypedChange{posture, proxyRef, dns},
		},
	)
	if planResponse.Code != http.StatusOK {
		t.Fatalf("generic network plan status=%d body=%s", planResponse.Code, planResponse.Body)
	}
	var plan ConfigurationPlan
	decodeProfileTransactionAPIData(t, planResponse, &plan)
	applyResponse := callProfileTransactionAPI(
		t,
		genericAPI,
		"profile/transaction/apply",
		configurationApplyRequest(plan),
	)
	if applyResponse.Code != http.StatusOK {
		t.Fatalf("generic network apply status=%d body=%s", applyResponse.Code, applyResponse.Body)
	}
	legacyProfile, err := legacyStore.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	genericProfile, err := genericStore.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if legacyProfile.Network != genericProfile.Network {
		t.Fatalf(
			"legacy network=%+v generic network=%+v",
			legacyProfile.Network,
			genericProfile.Network,
		)
	}
	operations, err := (OperationStore{Root: legacyStore.Root}).List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 ||
		operations[0].Kind != profileTransactionOperationKind ||
		operations[0].Phase != OperationSucceeded {
		t.Fatalf("legacy network route bypassed shared transaction: %+v", operations)
	}
}

func TestSecretAPIReferenceFeedsConfigurationTransactionWithoutValueEcho(t *testing.T) {
	api, _ := newSecretAPIFixture(t)
	if _, err := api.Core.Store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	secretPlanResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/plan",
		`{"schema":"hideout.secret-draft.v1","ref":"local-proxy","action":"set"}`,
	)
	if secretPlanResponse.Code != http.StatusOK {
		t.Fatalf(
			"secret plan status=%d body=%s",
			secretPlanResponse.Code,
			secretPlanResponse.Body,
		)
	}
	var secretPlan SecretPlan
	decodeSecretAPIData(t, secretPlanResponse, &secretPlan)
	const canary = "socks5://secret-user:secret-password@127.0.0.1:7890"
	secretApplyResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/apply",
		fmt.Sprintf(
			`{"schema":%q,"operationId":%q,"planDigest":%q,"ref":%q,"action":%q,"confirmed":true,"value":%q}`,
			SecretApplySchema,
			secretPlan.OperationID,
			secretPlan.PlanDigest,
			secretPlan.Ref,
			secretPlan.Action,
			canary,
		),
	)
	if secretApplyResponse.Code != http.StatusOK ||
		strings.Contains(secretApplyResponse.Body.String(), canary) {
		t.Fatalf(
			"secret apply status=%d or exposed value: %s",
			secretApplyResponse.Code,
			secretApplyResponse.Body,
		)
	}

	projection, err := (ProfileProjectionService{
		Store: api.Core.Store,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	posture, err := NewTypedChange(ChangeNetworkPosture, map[string]any{
		"mode": "proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyRef, err := NewTypedChange(ChangeNetworkProxyRef, map[string]any{
		"ref": secretPlan.Ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	dns, err := NewTypedChange(ChangeNetworkDNS, map[string]any{
		"mode": "doh", "serverIp": "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	configurationPlanResponse := callProfileTransactionAPI(
		t,
		api,
		"profile/transaction/plan",
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: projection.Revision,
			ClientNonce:  "secret-reference-parity",
			Changes:      []TypedChange{posture, proxyRef, dns},
		},
	)
	if configurationPlanResponse.Code != http.StatusOK ||
		strings.Contains(configurationPlanResponse.Body.String(), canary) ||
		strings.Contains(configurationPlanResponse.Body.String(), "secret-password") {
		t.Fatalf(
			"configuration plan status=%d or exposed secret: %s",
			configurationPlanResponse.Code,
			configurationPlanResponse.Body,
		)
	}
	var configurationPlan ConfigurationPlan
	decodeProfileTransactionAPIData(
		t,
		configurationPlanResponse,
		&configurationPlan,
	)
	configurationApplyResponse := callProfileTransactionAPI(
		t,
		api,
		"profile/transaction/apply",
		configurationApplyRequest(configurationPlan),
	)
	if configurationApplyResponse.Code != http.StatusOK ||
		strings.Contains(configurationApplyResponse.Body.String(), canary) {
		t.Fatalf(
			"configuration apply status=%d or exposed secret: %s",
			configurationApplyResponse.Code,
			configurationApplyResponse.Body,
		)
	}
	current, err := api.Core.Store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Network.ProxySecretRef != secretPlan.Ref {
		t.Fatalf("configuration did not persist secret reference: %+v", current.Network)
	}
}

func TestLegacyCommandProxyAndHostFSApplyUseDurableSharedTransactions(t *testing.T) {
	t.Run("command proxy", func(t *testing.T) {
		store := profile.Store{Root: t.TempDir()}
		api := NewAPI(New(store), "ui_token", time.Minute)
		response := callProfileTransactionAPI(
			t,
			api,
			"profile/command-proxy/apply",
			CommandProxyAPIRequest{
				ProfileName: "default",
				Operation:   "add-open",
				Command:     "browser-open",
			},
		)
		if response.Code != http.StatusOK {
			t.Fatalf("apply status=%d body=%s", response.Code, response.Body)
		}
		var result CommandProxyResult
		decodeProfileTransactionAPIData(t, response, &result)
		if !result.Applied ||
			len(result.Commands) != 3 ||
			result.Commands[0] != "browser-open" {
			t.Fatalf("legacy command proxy response=%+v", result)
		}
		assertSingleSucceededProfileTransaction(t, store)
	})

	t.Run("HostFS", func(t *testing.T) {
		store := profile.Store{Root: t.TempDir()}
		api := NewAPI(New(store), "ui_token", time.Minute)
		response := callProfileTransactionAPI(
			t,
			api,
			"profile/hostfs/apply",
			ProfileHostFSAPIRequest{
				ProfileName: "default",
				Operation:   "add",
				Rule:        "read:/tmp/transaction.txt",
				Reason:      "shared transaction adapter test",
			},
		)
		if response.Code != http.StatusOK {
			t.Fatalf("apply status=%d body=%s", response.Code, response.Body)
		}
		var result ProfileHostFSResult
		decodeProfileTransactionAPIData(t, response, &result)
		if !result.Applied ||
			len(result.Grants) != 1 ||
			result.Grants[0].HostPath != "/tmp/transaction.txt" ||
			result.Plan.PlannedRule == nil ||
			result.Plan.PlannedRule.ID != result.Grants[0].ID {
			t.Fatalf("legacy HostFS response is not self-consistent: %+v", result)
		}
		assertSingleSucceededProfileTransaction(t, store)
	})
}

func assertSingleSucceededProfileTransaction(
	t *testing.T,
	store profile.Store,
) {
	t.Helper()
	operations, err := (OperationStore{Root: store.Root}).List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 ||
		operations[0].Kind != profileTransactionOperationKind ||
		operations[0].Phase != OperationSucceeded {
		t.Fatalf("shared transaction operations=%+v", operations)
	}
}

func callProfileTransactionAPI(
	t *testing.T,
	api API,
	resource string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	request := newAPIJSONRequest(
		http.MethodPost,
		"/api/v1/"+resource,
		body,
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func decodeProfileTransactionAPIData(
	t *testing.T,
	response *httptest.ResponseRecorder,
	out any,
) {
	t.Helper()
	var envelope struct {
		Version  string          `json:"version"`
		Resource string          `json:"resource"`
		Data     json.RawMessage `json:"data"`
		Errors   []string        `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != APIVersion ||
		envelope.Resource == "" ||
		len(envelope.Errors) != 0 {
		t.Fatalf("invalid configuration API envelope: %+v", envelope)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatal(err)
	}
}
