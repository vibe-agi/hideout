package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestAPIDomainResourcesRequireToken(t *testing.T) {
	schema := compileManagerAPISchema(t)
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/overview")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())

	req = newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer wrong_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())

	req = newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}

	req = newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("X-Hideout-UI-Token", "ui_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("ui token status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIIsReadOnlyAndExposesNoControlRoutes(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		req := newAPIRequest(method, "/api/v1/overview")
		req.Header.Set("Authorization", "Bearer ui_token")
		resp := httptest.NewRecorder()
		api.ServeHTTP(resp, req)
		if resp.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d body=%s", method, resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Header().Get("Allow"), http.MethodGet) {
			t.Fatalf("%s should advertise GET-only API, headers=%v", method, resp.Header())
		}
	}
	for _, path := range []string{
		"/api/v1/run",
		"/api/v1/host/open",
		"/api/v1/broker/requests/req_test/allow",
		"/api/v1/profiles/default/rotate-identity",
		"/api/v1/profiles/default/reset",
		"/api/v1/lab/portbridge",
		"/api/v1/lab/browser-control",
		"/api/v1/lab/preview-open",
	} {
		req := newAPIRequest(http.MethodGet, path)
		req.Header.Set("Authorization", "Bearer ui_token")
		resp := httptest.NewRecorder()
		api.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, resp.Code, resp.Body.String())
		}
		if strings.Contains(resp.Body.String(), "lab-probe") || strings.Contains(resp.Body.String(), "host.open") {
			t.Fatalf("%s should not expose control authority details: %s", path, resp.Body.String())
		}
	}
}

func TestAPIRunPlanAndStatus(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	api := API{
		Core: Core{
			Store: store,
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	schema := compileManagerAPISchema(t)
	reqBody := RunAPIRequest{
		ProfileName:        "api-plan",
		Backend:            "native",
		Workspace:          t.TempDir(),
		Command:            []string{"echo", "hi"},
		AllowWeakIsolation: true,
	}
	req := newAPIJSONRequest(http.MethodPost, "/api/v1/run/plan", reqBody)
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	body := resp.Body.String()
	if strings.Contains(body, "cap_") || strings.Contains(body, "HIDEOUT_SECRET") {
		t.Fatalf("run plan response leaked secret-bearing data: %s", body)
	}
	var decoded APIResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Resource != "run/plan" {
		t.Fatalf("resource=%q want run/plan", decoded.Resource)
	}
	plan, ok := decoded.Data.(map[string]any)
	if !ok || plan["version"] != RunPlanVersion || plan["profile"] != "api-plan" || plan["backend"] != "native" {
		t.Fatalf("run plan data mismatch: %+v", decoded.Data)
	}

	req = newAPIRequest(http.MethodGet, "/api/v1/run/status")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	if !strings.Contains(resp.Body.String(), `"resource":"run/status"`) ||
		!strings.Contains(resp.Body.String(), `"sessions":[]`) {
		t.Fatalf("run status response mismatch: %s", resp.Body.String())
	}
}

func TestAPIInitPlanAndApplyConfigureGenericToolSupply(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	api := API{
		Core:      Core{Store: store},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	schema := compileManagerAPISchema(t)
	reqBody := InitAPIRequest{
		ProfileName: "api-tools",
		Backend:     "native",
		Network:     "direct",
		ToolPresets: []string{"base-dev"},
		NPMGlobals: []profile.NPMGlobalPackage{{
			Package:  "@example/agent-cli@1.2.3",
			Commands: []string{"agent-cli", "agent-helper"},
		}},
	}
	req := newAPIJSONRequest(http.MethodPost, "/api/v1/init/plan", reqBody)
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	body := resp.Body.String()
	for _, want := range []string{
		`"resource":"init/plan"`,
		`"version":"hideout.init/v1"`,
		`"kind":"tools.preset.add"`,
		`"kind":"tools.npm-global.add"`,
		`"nextSteps"`,
		`"id":"cli-smoke"`,
		`"command":"hideout run --profile api-tools --backend native --allow-weak-isolation -- agent-cli"`,
		`"@example/agent-cli@1.2.3"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("init/plan missing %q: %s", want, body)
		}
	}
	if _, err := store.Load("api-tools"); err == nil {
		t.Fatal("init/plan mutated profile state")
	}

	req = newAPIJSONRequest(http.MethodPost, "/api/v1/init/apply", reqBody)
	req.Header.Set("X-Hideout-UI-Token", "ui_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	body = resp.Body.String()
	for _, want := range []string{
		`"resource":"init/apply"`,
		`"version":"hideout.init/v1"`,
		`"kind":"tools.npm-global.add"`,
		`"nextSteps"`,
		`"id":"cli-smoke"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("init/apply missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "HIDEOUT_SECRET") || strings.Contains(body, "proxy.url") {
		t.Fatalf("init/apply leaked secret-bearing data: %s", body)
	}
	loaded, err := store.Load("api-tools")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStringForAPITest(loaded.Tools.Presets, "node-dev") {
		t.Fatalf("node-dev preset was not persisted: %+v", loaded.Tools.Presets)
	}
	if len(loaded.Tools.NPMGlobals) != 1 ||
		loaded.Tools.NPMGlobals[0].Package != "@example/agent-cli@1.2.3" ||
		!containsStringForAPITest(loaded.Tools.NPMGlobals[0].Commands, "agent-cli") ||
		!containsStringForAPITest(loaded.Tools.NPMGlobals[0].Commands, "agent-helper") {
		t.Fatalf("npm global tool was not persisted: %+v", loaded.Tools.NPMGlobals)
	}
}

func TestAPIInitApplyConfiguresTun2SocksProxySecretRef(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	api := API{
		Core:      Core{Store: store},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	reqBody := InitAPIRequest{
		ProfileName:    "api-privacy",
		Backend:        "native",
		Network:        "tun2socks",
		ProxySecretRef: "default-proxy",
	}
	req := newAPIJSONRequest(http.MethodPost, "/api/v1/init/apply", reqBody)
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	body := resp.Body.String()
	for _, want := range []string{
		`"resource":"init/apply"`,
		`"kind":"network.mode.select"`,
		`"tun2socks"`,
		`"default-proxy"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("init/apply missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "socks5://") || strings.Contains(body, "HIDEOUT_SECRET_DEFAULT_PROXY") {
		t.Fatalf("init/apply leaked proxy secret value: %s", body)
	}
	loaded, err := store.Load("api-privacy")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Network.Mode != network.ModeTun2Socks || loaded.Network.ProxySecretRef != "default-proxy" {
		t.Fatalf("network settings were not persisted: %+v", loaded.Network)
	}
}

func TestAPICommandProxyPlanAndApply(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	api := API{
		Core:      Core{Store: store},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	schema := compileManagerAPISchema(t)
	reqBody := CommandProxyAPIRequest{
		ProfileName: "api-cmdproxy",
		Operation:   "add-open",
		Command:     "browser-open",
	}
	req := newAPIJSONRequest(http.MethodPost, "/api/v1/profile/command-proxy/plan", reqBody)
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	planBody := resp.Body.String()
	for _, want := range []string{
		`"resource":"profile/command-proxy/plan"`,
		`"version":"hideout.command-proxy-plan/v1"`,
		`"operation":"add-open"`,
		`"command":"browser-open"`,
		`"route":"host-broker"`,
		`"action":"host.open"`,
		`"argvSchema":"open-target-v1"`,
		`"changed":true`,
	} {
		if !strings.Contains(planBody, want) {
			t.Fatalf("command-proxy plan missing %q: %s", want, planBody)
		}
	}
	if _, err := store.Load("api-cmdproxy"); err == nil {
		t.Fatal("command-proxy plan should not create profile state")
	}

	req = newAPIJSONRequest(http.MethodPost, "/api/v1/profile/command-proxy/apply", reqBody)
	req.Header.Set("X-Hideout-UI-Token", "ui_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	applyBody := resp.Body.String()
	for _, want := range []string{
		`"resource":"profile/command-proxy/apply"`,
		`"applied":true`,
		`"command":"browser-open"`,
		`"commands":["browser-open","open","xdg-open"]`,
	} {
		if !strings.Contains(applyBody, want) {
			t.Fatalf("command-proxy apply missing %q: %s", want, applyBody)
		}
	}
	loaded, err := store.Load("api-cmdproxy")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CommandProxy.Commands["browser-open"].Action != "host.open" {
		t.Fatalf("command proxy was not persisted: %+v", loaded.CommandProxy.Commands)
	}
}

func TestAPICommandProxyRejectsUnknownFieldsAndHostExecShape(t *testing.T) {
	api := API{
		Core:      Core{Store: profile.Store{Root: t.TempDir()}},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown raw host command field",
			body: `{"profile":"default","operation":"add-open","command":"browser-open","hostCommand":"/usr/bin/open"}`,
			want: "invalid command-proxy request",
		},
		{
			name: "unsupported operation",
			body: `{"profile":"default","operation":"host-exec","command":"browser-open"}`,
			want: "unsupported command-proxy operation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/profile/command-proxy/plan", strings.NewReader(tc.body))
			req.Host = "127.0.0.1"
			req.Header.Set("Authorization", "Bearer ui_token")
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			api.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
			}
			validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
			if !strings.Contains(resp.Body.String(), tc.want) {
				t.Fatalf("expected %q rejection, got %s", tc.want, resp.Body.String())
			}
		})
	}
}

func TestAPIInitRequestRejectsUnknownFields(t *testing.T) {
	api := API{
		Core:      Core{Store: profile.Store{Root: t.TempDir()}},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/init/plan", strings.NewReader(`{
		"profile": "api-tools",
		"shell": "curl https://example.invalid/install.sh | sh"
	}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer ui_token")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	if !strings.Contains(resp.Body.String(), "invalid init request") ||
		strings.Contains(resp.Body.String(), "curl") {
		t.Fatalf("expected generic init request rejection, got %s", resp.Body.String())
	}
}

func TestAPIRunRequestRejectsHostAuditPath(t *testing.T) {
	api := API{
		Core:      Core{Store: profile.Store{Root: t.TempDir()}},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/run/plan", strings.NewReader(`{
		"backend": "native",
		"command": ["tool"],
		"auditPath": "/tmp/hideout-api-audit.jsonl"
	}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer ui_token")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	if !strings.Contains(resp.Body.String(), "invalid run request") {
		t.Fatalf("expected generic invalid request error, got %s", resp.Body.String())
	}
}

func TestAPIRunApplyUsesManagerApplyRun(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	fake := &applyRunFakeBackend{}
	openerCalled := false
	openerIdentityDir := ""
	api := API{
		Core:      Core{Store: store},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
		RunBackend: func(req RunAPIRequest, plan RunPlan) (backend.Backend, error) {
			if req.Backend != "native" || plan.Backend != "native" {
				t.Fatalf("unexpected backend request=%q plan=%q", req.Backend, plan.Backend)
			}
			return fake, nil
		},
		RunOpener: func(req RunAPIRequest, plan RunPlan, runSession RunSession) broker.Opener {
			if req.ProfileName != "api-apply" || plan.ProfileName != "api-apply" {
				t.Fatalf("unexpected opener request profile: req=%q plan=%q", req.ProfileName, plan.ProfileName)
			}
			openerCalled = true
			openerIdentityDir = runSession.IdentityDir
			return broker.NoopOpener{}
		},
	}
	reqBody := RunAPIRequest{
		ProfileName:        "api-apply",
		Backend:            "native",
		Workspace:          t.TempDir(),
		Command:            []string{"tool", "arg"},
		AllowWeakIsolation: true,
	}
	req := newAPIJSONRequest(http.MethodPost, "/api/v1/run/apply", reqBody)
	req.Header.Set("X-Hideout-UI-Token", "ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	if !strings.Contains(resp.Body.String(), `"resource":"run/apply"`) ||
		!strings.Contains(resp.Body.String(), `"version":"hideout.run-result/v1"`) {
		t.Fatalf("run apply response mismatch: %s", resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "cap_") || strings.Contains(resp.Body.String(), "broker.sock") {
		t.Fatalf("run apply response leaked broker token or socket path: %s", resp.Body.String())
	}
	var applyResp APIResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &applyResp); err != nil {
		t.Fatal(err)
	}
	result, ok := applyResp.Data.(map[string]any)
	if !ok || result["sessionId"] == "" {
		t.Fatalf("run/apply result missing session id: %+v", applyResp.Data)
	}
	if len(fake.calls) == 0 || fake.calls[0] != "available" {
		t.Fatalf("fake backend was not used: %v", fake.calls)
	}
	if !openerCalled || openerIdentityDir == "" {
		t.Fatalf("API run/apply did not configure run opener: called=%v identity=%q", openerCalled, openerIdentityDir)
	}

	statusReq := newAPIRequest(http.MethodGet, "/api/v1/run/status?session="+result["sessionId"].(string))
	statusReq.Header.Set("Authorization", "Bearer ui_token")
	statusResp := httptest.NewRecorder()
	api.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("run/status status=%d body=%s", statusResp.Code, statusResp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), statusResp.Body.Bytes())
	statusBody := statusResp.Body.String()
	for _, want := range []string{
		`"resource":"run/status"`,
		`"id":"` + result["sessionId"].(string) + `"`,
		`"hasAudit":true`,
	} {
		if !strings.Contains(statusBody, want) {
			t.Fatalf("run/status missing %q: %s", want, statusBody)
		}
	}
	if strings.Contains(statusBody, "cap_") ||
		strings.Contains(statusBody, "broker.sock") ||
		strings.Contains(statusBody, "proxy.invalid") ||
		strings.Contains(statusBody, "proxy.url") {
		t.Fatalf("run/status leaked broker or proxy secret details: %s", statusBody)
	}
}

func TestAPIRunApplyRequiresConfiguredBackendFactory(t *testing.T) {
	api := API{
		Core:      Core{Store: profile.Store{Root: t.TempDir()}},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := newAPIJSONRequest(http.MethodPost, "/api/v1/run/apply", RunAPIRequest{
		Backend: "native",
		Command: []string{"tool"},
	})
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
}

func TestAPIEnvironmentLifecyclePlanAndApply(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	envStore := environment.Store{Root: store.Root}
	rec, err := envStore.Create(environment.Spec{
		Profile:        "default",
		Backend:        "lima",
		Workspace:      "/work/project",
		GuestWorkspace: "/workspace",
		InstanceName:   "hideout-env-test",
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	rec.Status = "running"
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}
	operator := &fakeEnvironmentOperator{}
	api := API{
		Core: Core{
			Store: store,
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:       "ui_token",
		ExpiresAt:   time.Now().Add(time.Minute),
		EnvOperator: operator,
	}
	schema := compileManagerAPISchema(t)
	planReq := newAPIJSONRequest(http.MethodPost, "/api/v1/environment/stop/plan", EnvironmentActionAPIRequest{
		IDs: []string{rec.ID},
	})
	planReq.Header.Set("X-Hideout-UI-Token", "ui_token")
	planResp := httptest.NewRecorder()
	api.ServeHTTP(planResp, planReq)
	if planResp.Code != http.StatusOK {
		t.Fatalf("stop plan status=%d body=%s", planResp.Code, planResp.Body.String())
	}
	validateManagerAPIResponse(t, schema, planResp.Body.Bytes())
	if !strings.Contains(planResp.Body.String(), `"resource":"environment/stop/plan"`) ||
		!strings.Contains(planResp.Body.String(), `"targets"`) {
		t.Fatalf("stop plan response missing expected fields: %s", planResp.Body.String())
	}

	applyReq := newAPIJSONRequest(http.MethodPost, "/api/v1/environment/stop/apply", EnvironmentActionAPIRequest{
		IDs: []string{rec.ID},
	})
	applyReq.Header.Set("X-Hideout-UI-Token", "ui_token")
	applyResp := httptest.NewRecorder()
	api.ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("stop apply status=%d body=%s", applyResp.Code, applyResp.Body.String())
	}
	validateManagerAPIResponse(t, schema, applyResp.Body.Bytes())
	if !strings.Contains(applyResp.Body.String(), `"resource":"environment/stop/apply"`) ||
		!strings.Contains(applyResp.Body.String(), `"applied"`) {
		t.Fatalf("stop apply response missing expected fields: %s", applyResp.Body.String())
	}
	if !reflect.DeepEqual(operator.stopped, []string{"hideout-env-test"}) {
		t.Fatalf("operator stop calls mismatch: %+v", operator.stopped)
	}
	loaded, err := envStore.Load(rec.ID)
	if err != nil {
		t.Fatalf("load environment: %v", err)
	}
	if loaded.Status != "stopped" {
		t.Fatalf("environment was not stopped: %+v", loaded)
	}

	cleanReq := newAPIJSONRequest(http.MethodPost, "/api/v1/environment/clean/apply", EnvironmentActionAPIRequest{
		IDs:         []string{rec.ID},
		StoppedOnly: true,
	})
	cleanReq.Header.Set("X-Hideout-UI-Token", "ui_token")
	cleanResp := httptest.NewRecorder()
	api.ServeHTTP(cleanResp, cleanReq)
	if cleanResp.Code != http.StatusOK {
		t.Fatalf("clean apply status=%d body=%s", cleanResp.Code, cleanResp.Body.String())
	}
	validateManagerAPIResponse(t, schema, cleanResp.Body.Bytes())
	if !strings.Contains(cleanResp.Body.String(), `"resource":"environment/clean/apply"`) {
		t.Fatalf("clean apply response missing resource: %s", cleanResp.Body.String())
	}
	if !reflect.DeepEqual(operator.cleaned, []string{"hideout-env-test"}) {
		t.Fatalf("operator clean calls mismatch: %+v", operator.cleaned)
	}
	if _, err := envStore.Load(rec.ID); err == nil {
		t.Fatalf("environment should have been removed")
	}
}

func TestAPIOverviewReturnsEmptyCollectionsAsArrays(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	var decoded APIResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	overview, ok := decoded.Data.(map[string]any)
	if !ok {
		t.Fatalf("overview data has wrong shape: %+v", decoded.Data)
	}
	for _, field := range []string{"profiles", "sessions", "environments", "secrets"} {
		values, ok := overview[field].([]any)
		if !ok || len(values) != 0 {
			t.Fatalf("overview.%s should be an empty array, got %#v", field, overview[field])
		}
	}
	capabilities, ok := overview["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("overview.capabilities has wrong shape: %#v", overview["capabilities"])
	}
	if values, ok := capabilities["maxCapabilities"].([]any); !ok || len(values) != 0 {
		t.Fatalf("capabilities.maxCapabilities should be an empty array, got %#v", capabilities["maxCapabilities"])
	}
	networkSummary, ok := overview["network"].(map[string]any)
	if !ok {
		t.Fatalf("overview.network has wrong shape: %#v", overview["network"])
	}
	if values, ok := networkSummary["profileDefaults"].([]any); !ok || len(values) != 0 {
		t.Fatalf("network.profileDefaults should be an empty array, got %#v", networkSummary["profileDefaults"])
	}
}

func TestAPIRejectsUnexpectedOrigin(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:          "ui_token",
		ExpiresAt:      time.Now().Add(time.Minute),
		AllowedOrigins: []string{"http://127.0.0.1:3000"},
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer ui_token")
	req.Header.Set("Origin", "https://example.com")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}

	req = newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer ui_token")
	req.Header.Set("Origin", "http://127.0.0.1:3000")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIRejectsUnexpectedHost(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:        "ui_token",
		ExpiresAt:    time.Now().Add(time.Minute),
		AllowedHosts: []string{"127.0.0.1:3000"},
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Host = "evil.example"
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), "host is not allowed") {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}

	req = newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Host = "127.0.0.1:3000"
	req.Header.Set("Authorization", "Bearer ui_token")
	resp = httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIRejectsExpiredToken(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Unix(100, 0),
		Now:       func() time.Time { return time.Unix(101, 0) },
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIExposesDomainResourcesWithoutSecretValues(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("default")
	p.Network.Mode = network.ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "ses_1", "audit.jsonl"), `{"time":"2026-07-01T00:00:00Z","session":"ses_1","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://user:pass@example.com/path?token=abc"}}`+"\n", 0o600)
	envRec, err := (environment.Store{Root: store.Root}).Create(environment.Spec{
		Profile:        "default",
		Backend:        "lima",
		Workspace:      "/work/project",
		GuestWorkspace: "/work/project",
		ProfileID:      p.Metadata["profileId"],
		IdentityID:     p.Metadata["identityId"],
		InstanceName:   "hideout-default-env-test",
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	api := API{
		Core: Core{
			Store: store,
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
			SecretEnv: []string{
				network.SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	schema := compileManagerAPISchema(t)
	for _, resource := range []string{
		"overview",
		"profiles",
		"sessions",
		"environments",
		"backends",
		"capabilities",
		"broker",
		"network",
		"secrets",
		"audit",
		"audit/events",
		"settings",
		"init",
		"bundles",
		"projects",
	} {
		req := newAPIRequest(http.MethodGet, "/api/v1/"+resource)
		req.Header.Set("X-Hideout-UI-Token", "ui_token")
		resp := httptest.NewRecorder()
		api.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", resource, resp.Code, resp.Body.String())
		}
		if resp.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s missing no-store header", resource)
		}
		body := resp.Body.String()
		if strings.Contains(body, "user:pass") || strings.Contains(body, "127.0.0.1:1080") {
			t.Fatalf("%s response leaked proxy secret: %s", resource, body)
		}
		validateManagerAPIResponse(t, schema, []byte(body))
		var decoded APIResponse
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("%s decode: %v\n%s", resource, err, body)
		}
		if decoded.Version != APIVersion || decoded.Resource != resource || decoded.Data == nil {
			t.Fatalf("%s unexpected response envelope: %+v", resource, decoded)
		}
		if resource == "capabilities" {
			data, ok := decoded.Data.(map[string]any)
			if !ok {
				t.Fatalf("capabilities data has unexpected shape: %+v", decoded.Data)
			}
			hostOpen, ok := data["hostOpen"].(map[string]any)
			if !ok {
				t.Fatalf("capabilities response missing hostOpen: %+v", data)
			}
			if hostOpen["mode"] != "brokered" ||
				hostOpen["allowUrls"] != true ||
				hostOpen["urlScope"] != "external-http-https-only" ||
				hostOpen["localNetworkPolicy"] != "deny-host-local-private-cgnat-benchmark-link-local-multicast" ||
				hostOpen["allowWorkspaceFiles"] != true ||
				hostOpen["browserProfile"] != "isolated" ||
				hostOpen["browserControl"] != "none" {
				t.Fatalf("capabilities hostOpen mismatch: %+v", hostOpen)
			}
		}
		if resource == "environments" {
			values, ok := decoded.Data.([]any)
			if !ok || len(values) != 1 {
				t.Fatalf("environments data has unexpected shape: %+v", decoded.Data)
			}
			env, ok := values[0].(map[string]any)
			if !ok || env["id"] != envRec.ID || env["instanceName"] != "hideout-default-env-test" {
				t.Fatalf("environment resource mismatch: %+v", values[0])
			}
		}
	}
}

func TestAPIAuditEventsSupportsFilters(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "ses_1", "audit.jsonl"), strings.Join([]string{
		`{"time":"2026-07-01T00:00:00Z","session":"ses_1","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://user:pass@example.com/path?token=abc","identityId":"id_traceable","sourceIdentityId":"id_source","machineId":"0123456789abcdef0123456789abcdef","message":"guest machine-id 0123456789abcdef0123456789abcdef is ready","headerDump":"Authorization: Bearer tok_123\nCookie: sid=abc"}}`,
		`{"time":"2026-07-01T00:00:01Z","session":"ses_1","profile":"default","backend":"native","action":"network.setup","decision":"allow","details":{}}`,
		`{"time":"2026-07-01T00:00:02Z","session":"ses_1","profile":"default","backend":"native","action":"host.fs.read","decision":"deny","details":{"status":"denied","policyEffect":"none"}}`,
	}, "\n")+"\n", 0o600)
	api := API{
		Core: Core{
			Store: store,
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/audit/events?session=ses_1&action=host.open&limit=1")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	body := resp.Body.String()
	if strings.Contains(body, "user:pass") || strings.Contains(body, "token=abc") ||
		strings.Contains(body, "0123456789abcdef0123456789abcdef") ||
		strings.Contains(body, "tok_123") || strings.Contains(body, "sid=abc") {
		t.Fatalf("audit events leaked secret or raw machine-id: %s", body)
	}
	var decoded APIResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	events, ok := decoded.Data.([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("expected one event, got %+v", decoded.Data)
	}
	event, ok := events[0].(map[string]any)
	if !ok || event["action"] != "host.open" {
		t.Fatalf("unexpected event data: %+v", events[0])
	}
	details, ok := event["details"].(map[string]any)
	if !ok {
		t.Fatalf("event details missing: %+v", event)
	}
	if details["identityId"] != "id_traceable" || details["sourceIdentityId"] != "id_source" {
		t.Fatalf("identity lineage IDs should be preserved: %+v", details)
	}
	if details["machineId"] != "REDACTED" {
		t.Fatalf("raw machine-id should be redacted: %+v", details)
	}
	if details["message"] != "guest machine-id REDACTED is ready" {
		t.Fatalf("string machine-id should be redacted: %+v", details)
	}

	denyReq := newAPIRequest(http.MethodGet, "/api/v1/audit/events?decision=deny&limit=20")
	denyReq.Header.Set("Authorization", "Bearer ui_token")
	denyResp := httptest.NewRecorder()
	api.ServeHTTP(denyResp, denyReq)
	if denyResp.Code != http.StatusOK {
		t.Fatalf("deny filter status=%d body=%s", denyResp.Code, denyResp.Body.String())
	}
	var denyDecoded APIResponse
	if err := json.Unmarshal(denyResp.Body.Bytes(), &denyDecoded); err != nil {
		t.Fatal(err)
	}
	denyEvents, ok := denyDecoded.Data.([]any)
	if !ok || len(denyEvents) != 1 {
		t.Fatalf("expected one denied event, got %+v", denyDecoded.Data)
	}
	denyEvent, ok := denyEvents[0].(map[string]any)
	if !ok || denyEvent["decision"] != "deny" || denyEvent["action"] != "host.fs.read" {
		t.Fatalf("unexpected denied event data: %+v", denyEvents[0])
	}
}

func TestAPIAuditEventsRejectsInvalidSessionFilter(t *testing.T) {
	api := API{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/audit/events?session=not-a-session")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
	if strings.Contains(resp.Body.String(), "not-a-session") {
		t.Fatalf("invalid session id response should not echo attacker-controlled id: %s", resp.Body.String())
	}
}

func TestAPIReturnsPartialOverviewErrors(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	badProfile := filepath.Join(store.Root, "profiles", "bad", "profile.json")
	mustWriteManagerTest(t, badProfile, `{"schemaVersion":"hideout.profile/v1","name":"","network":{"mode":"direct"},"policy":{"engine":"builtin+goja"}}`+"\n", 0o600)
	api := API{
		Core: Core{
			Store: store,
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	req := newAPIRequest(http.MethodGet, "/api/v1/overview")
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var decoded APIResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Errors) != 1 || !strings.Contains(decoded.Errors[0], "profile name") {
		t.Fatalf("expected partial overview error, got %+v", decoded.Errors)
	}
	validateManagerAPIResponse(t, compileManagerAPISchema(t), resp.Body.Bytes())
}

func compileManagerAPISchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "manager-api.schema.json"))
	if err != nil {
		t.Fatalf("read manager API schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode manager API schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("manager-api.schema.json", doc); err != nil {
		t.Fatalf("add manager API schema: %v", err)
	}
	schema, err := compiler.Compile("manager-api.schema.json")
	if err != nil {
		t.Fatalf("compile manager API schema: %v", err)
	}
	return schema
}

func newAPIRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1"
	return req
}

func newAPIJSONRequest(method, target string, body any) *http.Request {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(data))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	return req
}

func validateManagerAPIResponse(t *testing.T, schema *jsonschema.Schema, data []byte) {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode manager API response: %v\n%s", err, data)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("manager API response does not match schema: %v\n%s", err, data)
	}
}

func containsStringForAPITest(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
