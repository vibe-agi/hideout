package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestSecretAPIRoutesAreStrictPrivateAndBounded(t *testing.T) {
	want := map[string]struct {
		method    string
		sensitive bool
		limit     int64
	}{
		"secrets":      {method: http.MethodGet},
		"secret/plan":  {method: http.MethodPost, sensitive: true, limit: SecretRequestBodyLimit},
		"secret/apply": {method: http.MethodPost, sensitive: true, limit: SecretRequestBodyLimit},
	}
	for _, spec := range ManagerRoutes() {
		expectation, ok := want[spec.Resource]
		if !ok {
			continue
		}
		if spec.Method != expectation.method ||
			spec.Sensitive != expectation.sensitive ||
			spec.MaxRequestBodyBytes != expectation.limit ||
			!spec.NoStore ||
			!spec.NoBodyLog {
			t.Fatalf("secret route contract=%+v want=%+v", spec, expectation)
		}
		delete(want, spec.Resource)
	}
	if len(want) != 0 {
		t.Fatalf("secret routes are missing: %+v", want)
	}
	if _, ok := RecognizeManagerRoute(
		http.MethodPost,
		"/api/v1/secret/resolve",
	); ok {
		t.Fatal("public secret resolve route must not exist")
	}
}

func TestSecretAPIPlanApplyReplayDeleteAndListNeverExposeValue(t *testing.T) {
	api, store := newSecretAPIFixture(t)
	planResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/plan",
		`{"schema":"hideout.secret-draft.v1","ref":"local-proxy","action":"set"}`,
	)
	assertSecretAPIStatus(t, planResponse, http.StatusOK)
	var plan SecretPlan
	decodeSecretAPIData(t, planResponse, &plan)
	if err := plan.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(planResponse.Body.String(), `"value"`) {
		t.Fatalf("secret plan contains a value field: %s", planResponse.Body)
	}

	const canary = "socks5://canary-user:canary-password@127.0.0.1:7890"
	applyBody := fmt.Sprintf(
		`{"schema":"%s","operationId":%q,"planDigest":%q,"ref":%q,"action":%q,"confirmed":true,"value":%q}`,
		SecretApplySchema,
		plan.OperationID,
		plan.PlanDigest,
		plan.Ref,
		plan.Action,
		canary,
	)
	applyResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/apply",
		applyBody,
	)
	assertSecretAPIStatus(t, applyResponse, http.StatusOK)
	if strings.Contains(applyResponse.Body.String(), canary) ||
		strings.Contains(applyResponse.Body.String(), "canary-password") {
		t.Fatalf("secret apply response exposed its value: %s", applyResponse.Body)
	}
	var applied SecretApplyResult
	decodeSecretAPIData(t, applyResponse, &applied)
	if applied.Operation.Phase != OperationSucceeded ||
		applied.Reference.Availability != secrets.AvailabilityAvailable ||
		applied.Reference.Generation != 1 {
		t.Fatalf("secret apply result=%+v", applied)
	}
	resolved, err := store.Resolve(context.Background(), "local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Use(func(raw []byte) error {
		if string(raw) != canary {
			return fmt.Errorf("stored value=%q", raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	replayResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/apply",
		fmt.Sprintf(
			`{"schema":"%s","operationId":%q,"planDigest":%q,"ref":%q,"action":%q,"confirmed":true}`,
			SecretApplySchema,
			plan.OperationID,
			plan.PlanDigest,
			plan.Ref,
			plan.Action,
		),
	)
	assertSecretAPIStatus(t, replayResponse, http.StatusOK)
	if store.writeCount() != 1 {
		t.Fatalf("terminal replay wrote secret %d times", store.writeCount())
	}

	listResponse := callSecretAPI(
		t,
		api,
		http.MethodGet,
		"/api/v1/secrets?ref=local-proxy",
		"",
	)
	assertSecretAPIStatus(t, listResponse, http.StatusOK)
	if strings.Contains(listResponse.Body.String(), canary) ||
		strings.Contains(listResponse.Body.String(), "canary-password") {
		t.Fatalf("secret metadata response exposed its value: %s", listResponse.Body)
	}
	var references []secrets.Reference
	decodeSecretAPIData(t, listResponse, &references)
	if len(references) != 1 ||
		references[0].Ref != "local-proxy" ||
		references[0].Generation != 1 {
		t.Fatalf("secret references=%+v", references)
	}

	deletePlanResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/plan",
		`{"schema":"hideout.secret-draft.v1","ref":"local-proxy","action":"delete"}`,
	)
	assertSecretAPIStatus(t, deletePlanResponse, http.StatusOK)
	var deletePlan SecretPlan
	decodeSecretAPIData(t, deletePlanResponse, &deletePlan)
	deleteResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/apply",
		fmt.Sprintf(
			`{"schema":"%s","operationId":%q,"planDigest":%q,"ref":%q,"action":%q,"confirmed":true}`,
			SecretApplySchema,
			deletePlan.OperationID,
			deletePlan.PlanDigest,
			deletePlan.Ref,
			deletePlan.Action,
		),
	)
	assertSecretAPIStatus(t, deleteResponse, http.StatusOK)
	var deleted SecretApplyResult
	decodeSecretAPIData(t, deleteResponse, &deleted)
	if deleted.Reference.Availability != secrets.AvailabilityMissing ||
		deleted.Reference.Generation != 2 {
		t.Fatalf("secret delete result=%+v", deleted)
	}

	resolveResponse := callSecretAPI(
		t,
		api,
		http.MethodPost,
		"/api/v1/secret/resolve",
		`{"ref":"local-proxy"}`,
	)
	assertSecretAPIStatus(t, resolveResponse, http.StatusNotFound)
}

func TestSecretAPIRejectsAmbiguousJSONAndClearsParsedValueOnError(t *testing.T) {
	api, _ := newSecretAPIFixture(t)
	const canary = "socks5://duplicate-user:duplicate-password@127.0.0.1:7890"
	cases := map[string]struct {
		path string
		body string
	}{
		"plan value field": {
			path: "/api/v1/secret/plan",
			body: `{"schema":"hideout.secret-draft.v1","ref":"local-proxy","action":"set","value":"` + canary + `"}`,
		},
		"plan duplicate ref": {
			path: "/api/v1/secret/plan",
			body: `{"schema":"hideout.secret-draft.v1","ref":"local-proxy","ref":"other","action":"set"}`,
		},
		"apply duplicate value": {
			path: "/api/v1/secret/apply",
			body: `{"schema":"hideout.secret-apply.v1","operationId":"op_fixture000001","planDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ref":"local-proxy","action":"set","confirmed":true,"value":"` + canary + `","value":"second"}`,
		},
		"apply unknown after value": {
			path: "/api/v1/secret/apply",
			body: `{"schema":"hideout.secret-apply.v1","operationId":"op_fixture000001","planDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ref":"local-proxy","action":"set","confirmed":true,"value":"` + canary + `","unknown":true}`,
		},
		"apply trailing bytes": {
			path: "/api/v1/secret/apply",
			body: `{"schema":"hideout.secret-apply.v1","operationId":"op_fixture000001","planDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ref":"local-proxy","action":"set","confirmed":true,"value":"` + canary + `"} trailing`,
		},
		"apply invalid surrogate": {
			path: "/api/v1/secret/apply",
			body: `{"schema":"hideout.secret-apply.v1","operationId":"op_fixture000001","planDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ref":"local-proxy","action":"set","confirmed":true,"value":"\ud800"}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := callSecretAPI(
				t,
				api,
				http.MethodPost,
				tc.path,
				tc.body,
			)
			assertSecretAPIStatus(t, response, http.StatusBadRequest)
			if strings.Contains(response.Body.String(), canary) ||
				strings.Contains(response.Body.String(), "duplicate-password") {
				t.Fatalf("invalid request response exposed secret: %s", response.Body)
			}
		})
	}
}

func TestSecretApplyDecoderClearsValueWhenLaterFieldIsInvalid(t *testing.T) {
	const canary = "socks5://clear-user:clear-password@127.0.0.1:7890"
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/secret/apply",
		strings.NewReader(
			`{"schema":"hideout.secret-apply.v1","operationId":"op_fixture000001","planDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ref":"local-proxy","action":"set","confirmed":true,"value":"`+
				canary+
				`","unknown":true}`,
		),
	)
	response := httptest.NewRecorder()
	decoded, err := decodeSecretApplyAPIRequest(response, request)
	if err == nil {
		t.Fatal("decoder accepted an unknown field after the value")
	}
	if decoded.Value == nil {
		t.Fatal("fixture did not reach the value buffer before failing")
	}
	assertManagerSecretBufferCleared(t, decoded.Value)
}

func TestSecretAPIUsesRouteSpecificStreamingAndDeclaredBodyLimit(t *testing.T) {
	api, _ := newSecretAPIFixture(t)
	for _, declared := range []bool{false, true} {
		name := "streaming"
		if declared {
			name = "declared"
		}
		t.Run(name, func(t *testing.T) {
			body := strings.Repeat("x", int(SecretRequestBodyLimit)+1)
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/secret/apply",
				strings.NewReader(body),
			)
			if declared {
				request.ContentLength = int64(len(body))
			} else {
				request.ContentLength = -1
			}
			request.Host = "localhost"
			request.Header.Set("Authorization", "Bearer ui_token")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			assertSecretAPIStatus(t, response, http.StatusRequestEntityTooLarge)
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("oversize secret response is cacheable: %v", response.Header())
			}
		})
	}
}

func TestSecretAPIRejectsUnknownOrRepeatedListQuery(t *testing.T) {
	api, _ := newSecretAPIFixture(t)
	for _, target := range []string{
		"/api/v1/secrets?unknown=true",
		"/api/v1/secrets?ref=local-proxy&ref=other",
		"/api/v1/secrets?ref=invalid%2Fref",
		"/api/v1/secrets?ref=local-proxy;unknown=true",
	} {
		response := callSecretAPI(
			t,
			api,
			http.MethodGet,
			target,
			"",
		)
		assertSecretAPIStatus(t, response, http.StatusBadRequest)
	}
}

func TestSecretAPINeverEchoesProviderErrors(t *testing.T) {
	const canary = "provider failed with socks5://user:password@private.invalid"
	api := NewAPI(
		newSecretAPIFixtureCore(t),
		"ui_token",
		time.Minute,
	)
	provider := &leakingSecretProvider{err: errors.New(canary)}
	api.SecretProvider = provider
	cases := []struct {
		method string
		target string
		body   string
	}{
		{method: http.MethodGet, target: "/api/v1/secrets"},
		{
			method: http.MethodPost,
			target: "/api/v1/secret/plan",
			body:   `{"schema":"hideout.secret-draft.v1","ref":"local-proxy","action":"set"}`,
		},
		{
			method: http.MethodPost,
			target: "/api/v1/secret/apply",
			body:   `{"schema":"hideout.secret-apply.v1","operationId":"op_fixture000001","planDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ref":"local-proxy","action":"set","confirmed":true,"value":"private"}`,
		},
	}
	for _, tc := range cases {
		response := callSecretAPI(t, api, tc.method, tc.target, tc.body)
		if response.Code < 400 {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.target, response.Code, response.Body)
		}
		if strings.Contains(response.Body.String(), canary) ||
			strings.Contains(response.Body.String(), "password@private") {
			t.Fatalf("provider error was exposed: %s", response.Body)
		}
		var envelope APIResponse
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.ErrorDetails) != 1 {
			t.Fatalf("secret error lacks stable detail: %+v", envelope)
		}
	}
	if provider.value == nil {
		t.Fatal("apply fixture did not receive the secret buffer")
	}
	assertManagerSecretBufferCleared(t, provider.value)
}

func newSecretAPIFixture(
	t *testing.T,
) (API, *managerSecretStoreFixture) {
	t.Helper()
	service, store := newSecretServiceFixture(t)
	api := NewAPI(service.Core, "ui_token", time.Minute)
	api.SecretProvider = service
	return api, store
}

func newSecretAPIFixtureCore(t *testing.T) Core {
	t.Helper()
	service, _ := newSecretServiceFixture(t)
	return service.Core
}

func callSecretAPI(
	t *testing.T,
	api API,
	method string,
	target string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Host = "localhost"
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func assertSecretAPIStatus(
	t *testing.T,
	response *httptest.ResponseRecorder,
	want int,
) {
	t.Helper()
	if response.Code != want {
		t.Fatalf(
			"status=%d want=%d body=%s",
			response.Code,
			want,
			response.Body,
		)
	}
}

func decodeSecretAPIData(
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
		t.Fatalf("invalid secret API envelope: %+v", envelope)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatal(err)
	}
}

type leakingSecretProvider struct {
	err   error
	value *secrets.Buffer
}

func (provider *leakingSecretProvider) ListSecrets(
	context.Context,
	string,
) ([]secrets.Reference, error) {
	return nil, provider.err
}

func (provider *leakingSecretProvider) PlanSecret(
	context.Context,
	SecretDraft,
) (SecretPlan, error) {
	return SecretPlan{}, provider.err
}

func (provider *leakingSecretProvider) ApplySecret(
	_ context.Context,
	request SecretApplyRequest,
) (SecretApplyResult, error) {
	provider.value = request.Value
	return SecretApplyResult{}, provider.err
}

var _ SecretProvider = (*leakingSecretProvider)(nil)
