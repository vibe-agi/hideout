package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestManagerRouteInventoryRecognizesEveryRoute(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range ManagerRoutes() {
		if spec.Class != RouteClassManagerAPI {
			t.Fatalf("%s %s class=%q", spec.Method, spec.Path, spec.Class)
		}
		if !strings.HasPrefix(spec.Path, "/api/v1/") {
			t.Fatalf("%s %s is not under /api/v1", spec.Method, spec.Path)
		}
		key := spec.Method + " " + spec.Path
		if seen[key] {
			t.Fatalf("duplicate route inventory entry %s", key)
		}
		seen[key] = true
		got, ok := RecognizeManagerRoute(spec.Method, spec.SamplePath())
		if !ok {
			t.Fatalf("inventory route %s sample %s is not recognized", key, spec.SamplePath())
		}
		if got.Resource != spec.Resource || got.Method != spec.Method {
			t.Fatalf("recognized %+v, want %+v", got, spec)
		}
	}
}

func TestManagerRouteInventoryCarriesRequestAndPrivacyMetadata(t *testing.T) {
	sensitive := map[string]bool{}
	for _, spec := range ManagerRoutes() {
		if !spec.NoStore || !spec.NoBodyLog {
			t.Fatalf("%s %s lacks mandatory private-route metadata: %+v", spec.Method, spec.Path, spec)
		}
		switch spec.Method {
		case http.MethodGet:
			if spec.MaxRequestBodyBytes != 0 {
				t.Fatalf("GET route %s has a body allowance: %+v", spec.Path, spec)
			}
		case http.MethodPost:
			if spec.MaxRequestBodyBytes <= 0 || spec.MaxRequestBodyBytes > DefaultRequestBodyLimit {
				t.Fatalf("POST route %s has an invalid body bound: %+v", spec.Path, spec)
			}
		}
		if spec.Sensitive {
			sensitive[spec.Resource] = true
		}
	}
	for _, resource := range []string{
		"run/plan",
		"run/apply",
		"profile/env/plan",
		"profile/env/apply",
		"secret/plan",
		"secret/apply",
	} {
		if !sensitive[resource] {
			t.Fatalf("sensitive request route %q is not classified", resource)
		}
	}
	if SecretRequestBodyLimit != 16<<10 {
		t.Fatalf("secret request bound=%d want %d", SecretRequestBodyLimit, 16<<10)
	}
}

func TestManagerRoutePatternsSupportNamedParametersWithoutTraversal(t *testing.T) {
	patterns := map[string]string{
		"profiles/{profile}/projection": "profiles/default/projection",
		"operations/{operation}":        "operations/op_fixture0001",
		"activity/{owner}/events":       "activity/owner_fixture/events",
	}
	for pattern, resource := range patterns {
		if !routeResourceMatches(pattern, resource) {
			t.Fatalf("pattern %q did not match %q", pattern, resource)
		}
		spec := RouteSpec{Resource: pattern}
		if sample := spec.SamplePath(); strings.Contains(sample, "{") || !strings.HasPrefix(sample, "/api/v1/") {
			t.Fatalf("pattern %q produced invalid sample %q", pattern, sample)
		}
	}
	for _, resource := range []string{
		"profiles//projection",
		"profiles/../projection",
		"profiles/default/extra/projection",
		"profiles/default\n/ projection",
	} {
		if routeResourceMatches("profiles/{profile}/projection", resource) {
			t.Fatalf("unsafe route parameter matched %q", resource)
		}
	}
}

func TestManagerAPIDetailedErrorsRemainBackwardCompatibleAndSchemaValid(t *testing.T) {
	response := httptest.NewRecorder()
	writeAPIDetailedError(response, http.StatusConflict, APIErrorDetail{
		Code: "stale-plan", Field: "baseRevision",
		Message:  "profile changed after this review",
		Recovery: "refresh the profile and review the new diff",
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope APIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Errors) != 1 || len(envelope.ErrorDetails) != 1 ||
		envelope.ErrorDetails[0].Code != "stale-plan" {
		t.Fatalf("detailed error lost legacy or stable shape: %+v", envelope)
	}
	if err := validateManagerAPIDocument(compileManagerAPISchema(t), envelope); err != nil {
		t.Fatalf("detailed error violates Manager schema: %v", err)
	}
}

func TestManagerPOSTBodyLimitRejectsDeclaredAndStreamingOversizeBodies(t *testing.T) {
	api := API{
		Core:      Core{Store: profile.Store{Root: t.TempDir()}},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	cases := []struct {
		name          string
		body          string
		contentLength int64
	}{
		{
			name:          "declared",
			body:          strings.Repeat("x", int(DefaultRequestBodyLimit)+1),
			contentLength: DefaultRequestBodyLimit + 1,
		},
		{
			name:          "streaming",
			body:          "{}" + strings.Repeat(" ", int(DefaultRequestBodyLimit)+1),
			contentLength: -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/init/plan", strings.NewReader(tc.body))
			request.ContentLength = tc.contentLength
			request.Host = "localhost"
			request.Header.Set("Authorization", "Bearer ui_token")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("oversize response is cacheable: %v", response.Header())
			}
			var envelope APIResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.ErrorDetails) != 1 || envelope.ErrorDetails[0].Code != "request-too-large" {
				t.Fatalf("oversize response lacks stable detail: %+v", envelope)
			}
		})
	}
}

func TestStrictJSONRejectsNonJSONTrailingBytes(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/init/plan", strings.NewReader("{} trailing"))
	response := httptest.NewRecorder()
	var value InitAPIRequest
	if err := decodeStrictJSON(response, request, &value, "invalid init request"); err == nil ||
		err.Error() != "invalid init request" {
		t.Fatalf("trailing bytes error=%v", err)
	}
}

func TestManagerRouteInventoryAndResponseSchemaStayInParity(t *testing.T) {
	routes := map[string]bool{}
	for _, spec := range ManagerRoutes() {
		routes[spec.ResponseResource()] = true
	}
	schemaResources := map[string]bool{}
	for _, resource := range managerAPISchemaResourceEnum(t) {
		schemaResources[resource] = true
	}
	for resource := range routes {
		if !schemaResources[resource] {
			t.Errorf("production route %q is absent from manager-api schema", resource)
		}
	}
	for resource := range schemaResources {
		if !routes[resource] {
			t.Errorf("manager-api schema resource %q has no production route", resource)
		}
	}
}

func TestManagerRouteRecognizerRejectsUnknownAndWrongClass(t *testing.T) {
	if _, ok := RecognizeManagerRoute(http.MethodGet, "/api/v1/does-not-exist"); ok {
		t.Fatal("unknown Manager route recognized")
	}
	if _, ok := RecognizeManagerRoute(http.MethodGet, "/daemon/status"); ok {
		t.Fatal("daemon endpoint recognized as Manager route")
	}
	if _, ok := RecognizeManagerRoute(http.MethodPost, "/api/v1/overview"); ok {
		t.Fatal("wrong method recognized for overview")
	}
	if _, ok := RecognizeManagerResourceAnyMethod("overview"); !ok {
		t.Fatal("overview should be recognized by some method")
	}
	if _, ok := RecognizeManagerResourceAnyMethod("does-not-exist"); ok {
		t.Fatal("unknown resource recognized by any-method recognizer")
	}
}

func TestManagerRouteRecognizerCoversDynamicMembers(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodGet, "/api/v1/decisions/dec_123", "decisions/{id}"},
		{http.MethodGet, "/api/v1/notices/notice_123", "notices/{id}"},
		{http.MethodGet, "/api/v1/operations/op_fixture0001", "operations/{operation}"},
		{http.MethodGet, "/api/v1/profiles/default/projection", "profiles/{profile}/projection"},
		{http.MethodPost, "/api/v1/decisions/dec_123/claim", "decisions/{id}/claim"},
		{http.MethodPost, "/api/v1/decisions/dec_123/approve", "decisions/{id}/approve"},
		{http.MethodPost, "/api/v1/decisions/dec_123/deny", "decisions/{id}/deny"},
		{http.MethodPost, "/api/v1/decisions/dec_123/reopen", "decisions/{id}/reopen"},
		{http.MethodPost, "/api/v1/notices/notice_123/ack", "notices/{id}/ack"},
	}
	for _, tc := range cases {
		got, ok := RecognizeManagerRoute(tc.method, tc.path)
		if !ok {
			t.Fatalf("%s %s not recognized", tc.method, tc.path)
		}
		if got.Resource != tc.want {
			t.Fatalf("%s %s recognized as %s, want %s", tc.method, tc.path, got.Resource, tc.want)
		}
	}
}

func TestRuntimeRoutesArePartOfProductionManagerInventory(t *testing.T) {
	want := map[string]bool{
		http.MethodGet + " /api/v1/runtime/catalog":       false,
		http.MethodGet + " /api/v1/runtime/status":        false,
		http.MethodPost + " /api/v1/runtime/verify/plan":  false,
		http.MethodPost + " /api/v1/runtime/verify/apply": false,
	}
	for _, spec := range ManagerRoutes() {
		key := spec.Method + " " + spec.Path
		if _, ok := want[key]; ok {
			want[key] = true
			if spec.Owner != "internal/manager.API" || spec.Class != RouteClassManagerAPI {
				t.Fatalf("runtime route is not Manager-owned: %+v", spec)
			}
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("runtime route missing from production inventory: %s", route)
		}
	}
}

func TestHostAppRoutesAndManagerAPISchemaStayInParity(t *testing.T) {
	want := map[string]string{
		"app/list":    http.MethodGet,
		"app/inspect": http.MethodGet,
		"app/plan":    http.MethodPost,
		"app/apply":   http.MethodPost,
	}
	routes := map[string]string{}
	for _, spec := range ManagerRoutes() {
		if !strings.HasPrefix(spec.Resource, "app/") {
			continue
		}
		if previous, exists := routes[spec.Resource]; exists {
			t.Fatalf("host-app resource %q has duplicate methods %s and %s", spec.Resource, previous, spec.Method)
		}
		routes[spec.Resource] = spec.Method
		if spec.Owner != "internal/manager.API" || spec.Class != RouteClassManagerAPI {
			t.Fatalf("host-app route is not Manager-owned: %+v", spec)
		}
	}

	schemaResources := map[string]bool{}
	for _, resource := range managerAPISchemaResourceEnum(t) {
		if strings.HasPrefix(resource, "app/") {
			if schemaResources[resource] {
				t.Fatalf("duplicate host-app schema resource %q", resource)
			}
			schemaResources[resource] = true
		}
	}

	for resource, method := range want {
		if got := routes[resource]; got != method {
			t.Errorf("route %q method=%q want=%q", resource, got, method)
		}
		if !schemaResources[resource] {
			t.Errorf("schema enum is missing host-app resource %q", resource)
		}
	}
	for resource := range routes {
		if _, ok := want[resource]; !ok {
			t.Errorf("unreviewed host-app route %q is absent from the parity contract", resource)
		}
		if !schemaResources[resource] {
			t.Errorf("host-app route %q is absent from the schema enum", resource)
		}
	}
	for resource := range schemaResources {
		if _, ok := want[resource]; !ok {
			t.Errorf("host-app schema resource %q has no reviewed route", resource)
		}
		if _, ok := routes[resource]; !ok {
			t.Errorf("host-app schema resource %q has no production route", resource)
		}
	}
}

func TestManagerAPISchemaRemainsStrictForHostAppResponses(t *testing.T) {
	schema := compileManagerAPISchema(t)
	valid := map[string]any{
		"version":  APIVersion,
		"resource": "app/list",
		"data":     map[string]any{"hostAppPacks": []any{}},
		"errors":   []any{},
	}
	if err := validateManagerAPIDocument(schema, valid); err != nil {
		t.Fatalf("valid host-app envelope rejected: %v", err)
	}

	cases := map[string]map[string]any{
		"unknown top-level field": {
			"version": APIVersion, "resource": "app/list", "data": map[string]any{}, "errors": []any{}, "extra": true,
		},
		"unknown resource": {
			"version": APIVersion, "resource": "app/not-reviewed", "data": map[string]any{}, "errors": []any{},
		},
		"scalar data": {
			"version": APIVersion, "resource": "app/list", "data": "not-a-typed-shape", "errors": []any{},
		},
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateManagerAPIDocument(schema, document); err == nil {
				t.Fatal("generic Manager API schema accepted a weakened host-app envelope")
			}
		})
	}
}

func TestManagerPOSTDispatchIsGatedByRouteInventory(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	for _, spec := range ManagerRoutes() {
		if spec.Method != http.MethodPost {
			continue
		}
		req := newAPIRequest(http.MethodPost, spec.SamplePath())
		req.Header.Set("Authorization", "Bearer ui_token")
		resp := httptest.NewRecorder()
		api.ServeHTTP(resp, req)
		body := resp.Body.String()
		if resp.Code == http.StatusNotFound && strings.Contains(body, "unknown manager API resource") {
			t.Fatalf("%s was not dispatched through Manager route inventory: %s", spec.SamplePath(), body)
		}
		if resp.Code == http.StatusInternalServerError && strings.Contains(body, "route inventory has no POST handler") {
			t.Fatalf("%s is registered but has no POST handler: %s", spec.SamplePath(), body)
		}
	}

	req := newAPIRequest(http.MethodPost, "/api/v1/does-not-exist")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound || !strings.Contains(resp.Body.String(), "unknown manager API resource") {
		t.Fatalf("unknown POST should fail before dispatch, got %d: %s", resp.Code, resp.Body.String())
	}
}

func managerAPISchemaResourceEnum(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "manager-api.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Properties struct {
			Resource struct {
				Enum []string `json:"enum"`
			} `json:"resource"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode Manager API schema: %v", err)
	}
	if len(document.Properties.Resource.Enum) == 0 {
		t.Fatal("Manager API resource schema must remain a closed enum")
	}
	return document.Properties.Resource.Enum
}

func validateManagerAPIDocument(schema *jsonschema.Schema, document any) error {
	data, err := json.Marshal(document)
	if err != nil {
		return err
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(decoded)
}
