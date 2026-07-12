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
