package manager

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/profile"
)

type hostAppAPIEnvelope struct {
	Version  string          `json:"version"`
	Resource string          `json:"resource"`
	Data     json.RawMessage `json:"data"`
	Errors   []string        `json:"errors"`
}

func TestAPIHostAppLifecycleResponseShapeParity(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	packDir := writeManagerHostAppPack(t, root, "community.api-editor", "api-editor")
	core := New(store)
	configureManagerHostAppIdentity(t, &core, root)
	api := NewAPI(core, "ui_token", time.Minute)
	schema := compileManagerAPISchema(t)

	for _, operation := range []string{"validate", "test"} {
		sourceRequest := HostAppPackAPIRequest{
			Operation: operation, SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
		}
		planEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/plan", sourceRequest, http.StatusOK)
		plan := decodeHostAppAPIData[HostAppPackPlan](t, planEnvelope)
		if plan.Operation != operation || plan.PackID != "community.api-editor" || plan.SourceReview.Kind != hostapppack.SourceLocal || plan.ExpectedSourceDigest == "" {
			t.Fatalf("source %s plan mismatch: %+v", operation, plan)
		}
		assertHostAppRegistryCount(t, store.Root, 0)
		sourceRequest.Accepted, sourceRequest.Plan = true, &plan
		resultEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/apply", sourceRequest, http.StatusOK)
		result := decodeHostAppAPIData[HostAppPackResult](t, resultEnvelope)
		if !result.Applied || result.Plan.Operation != operation || result.Revision != nil {
			t.Fatalf("source %s result mismatch: %+v", operation, result)
		}
		if operation == "test" && (result.Test == nil || result.Test.Status != hostapppack.TestPassed) {
			t.Fatalf("source test omitted quality result: %+v", result)
		}
		assertHostAppRegistryCount(t, store.Root, 0)
	}

	addRequest := HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
	}
	planEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/plan", addRequest, http.StatusOK)
	addPlan := decodeHostAppAPIData[HostAppPackPlan](t, planEnvelope)
	if addPlan.Operation != "add" || addPlan.Version != HostAppPackPlanVersion || addPlan.Profile != "default" ||
		addPlan.Review.PackID != "community.api-editor" || !addPlan.Review.UntrustedPackageFields ||
		addPlan.ExpectedIdentityDigest == "" || len(addPlan.CommandPlan.Owners) == 0 ||
		addPlan.Message != "test, install, and enable exact host-app bindings for future runs only" {
		t.Fatalf("add plan shape mismatch: %+v", addPlan)
	}
	if !strings.HasPrefix(addPlan.ExpectedSourceDigest, "sha256:") || addPlan.SourceReview.Kind != hostapppack.SourceLocal {
		t.Fatalf("add plan source facts mismatch: %+v", addPlan)
	}
	if bytes.Contains(planEnvelope.Data, []byte(root)) || bytes.Contains(planEnvelope.Data, []byte(packDir)) || !bytes.Contains(planEnvelope.Data, []byte("local-directory")) {
		t.Fatalf("plan leaked or omitted sanitized source locator: %s", planEnvelope.Data)
	}
	assertHostAppRegistryCount(t, store.Root, 0)

	applyRequest := HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Accepted:   true,
		Plan:       &addPlan,
	}
	applyEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/apply", applyRequest, http.StatusOK)
	addResult := decodeHostAppAPIData[HostAppPackResult](t, applyEnvelope)
	if !addResult.Applied || addResult.Plan.Operation != "add" || addResult.Entry == nil || addResult.Revision == nil || addResult.Test == nil || addResult.Enablement == nil {
		t.Fatalf("add result shape mismatch: %+v", addResult)
	}
	if addResult.Plan.ExpectedSourceDigest != addPlan.ExpectedSourceDigest || addResult.Revision.RevisionID != addPlan.RevisionID || addResult.Enablement.Profile != addPlan.Profile {
		t.Fatalf("add result drifted from reviewed plan: plan=%+v result=%+v", addPlan, addResult)
	}

	listEnvelope := callHostAppAPI(t, api, schema, http.MethodGet, "/api/v1/app/list", nil, http.StatusOK)
	listData := decodeHostAppAPIData[HostAppPackListAPIResponse](t, listEnvelope)
	wantPacks, err := core.ListHostAppPacks()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listData.HostAppPacks, wantPacks) {
		t.Fatalf("Manager/CLI list model drift: got=%+v want=%+v", listData.HostAppPacks, wantPacks)
	}

	inspectEnvelope := callHostAppAPI(t, api, schema, http.MethodGet, "/api/v1/app/inspect?packId=community.api-editor&profile=default", nil, http.StatusOK)
	inspection := decodeHostAppAPIData[hostapppack.Inspection](t, inspectEnvelope)
	wantInspection, err := core.InspectHostAppPack("community.api-editor", "default")
	if err != nil {
		t.Fatal(err)
	}
	inspection.GeneratedAt = time.Time{}
	wantInspection.Status.GeneratedAt = time.Time{}
	if !reflect.DeepEqual(inspection, wantInspection.Status) {
		t.Fatalf("Manager/CLI inspection model drift: got=%+v want=%+v", inspection, wantInspection.Status)
	}

	for _, operation := range []string{"validate", "test"} {
		planRequest := HostAppPackAPIRequest{
			Operation:  operation,
			PackID:     addPlan.PackID,
			RevisionID: addPlan.RevisionID,
		}
		opPlanEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/plan", planRequest, http.StatusOK)
		opPlan := decodeHostAppAPIData[HostAppPackPlan](t, opPlanEnvelope)
		if opPlan.Operation != operation || opPlan.PackID != addPlan.PackID || opPlan.RevisionID != addPlan.RevisionID || opPlan.ExpectedSourceDigest != addPlan.ExpectedSourceDigest {
			t.Fatalf("%s plan shape mismatch: %+v", operation, opPlan)
		}
		opApplyEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/apply", HostAppPackAPIRequest{
			Operation: operation,
			Accepted:  true,
			Plan:      &opPlan,
		}, http.StatusOK)
		opResult := decodeHostAppAPIData[HostAppPackResult](t, opApplyEnvelope)
		if !opResult.Applied || opResult.Plan.Operation != operation || opResult.Revision == nil {
			t.Fatalf("%s result shape mismatch: %+v", operation, opResult)
		}
		if operation == "test" && (opResult.Test == nil || opResult.Test.Status != hostapppack.TestPassed) {
			t.Fatalf("test result omitted quality result: %+v", opResult)
		}
	}

	enablePlanEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/plan", HostAppPackAPIRequest{
		Operation:   "enable",
		ProfileName: "default",
		PackID:      addPlan.PackID,
		RevisionID:  addPlan.RevisionID,
	}, http.StatusOK)
	enablePlan := decodeHostAppAPIData[HostAppPackPlan](t, enablePlanEnvelope)
	if enablePlan.Operation != "enable" || enablePlan.Profile != "default" || len(enablePlan.CommandPlan.Owners) == 0 || enablePlan.ExpectedIdentityDigest == "" {
		t.Fatalf("enable plan shape mismatch: %+v", enablePlan)
	}
	enableApplyEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/apply", HostAppPackAPIRequest{
		Operation: "enable",
		Accepted:  true,
		Plan:      &enablePlan,
	}, http.StatusOK)
	enableResult := decodeHostAppAPIData[HostAppPackResult](t, enableApplyEnvelope)
	if !enableResult.Applied || enableResult.Plan.Operation != "enable" || enableResult.Revision == nil || enableResult.Enablement == nil || enableResult.Enablement.State != hostapppack.EnablementEnabled {
		t.Fatalf("enable result shape mismatch: %+v", enableResult)
	}
}

func TestAPIHostAppApplyRejectsAnythingButExactReviewedPlan(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	packDir := writeManagerHostAppPack(t, root, "community.api-fixation", "api-fixation-editor")
	core := New(store)
	configureManagerHostAppIdentity(t, &core, root)
	api := NewAPI(core, "ui_token", time.Minute)
	schema := compileManagerAPISchema(t)

	planEnvelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/plan", HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
	}, http.StatusOK)
	reviewedPlan := decodeHostAppAPIData[HostAppPackPlan](t, planEnvelope)

	requireRejected := func(name string, request any, want string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			envelope := callHostAppAPI(t, api, schema, http.MethodPost, "/api/v1/app/apply", request, http.StatusBadRequest)
			if len(envelope.Errors) != 1 || !strings.Contains(envelope.Errors[0], want) {
				t.Fatalf("error=%v, want substring %q", envelope.Errors, want)
			}
			assertHostAppRegistryCount(t, store.Root, 0)
		})
	}

	requireRejected("omitted plan", HostAppPackAPIRequest{
		Operation: "add",
		Accepted:  true,
	}, "reviewed plan")

	var malformedPlan map[string]any
	if err := json.Unmarshal(planEnvelope.Data, &malformedPlan); err != nil {
		t.Fatal(err)
	}
	malformedPlan["unexpectedAuthority"] = true
	requireRejected("malformed plan", map[string]any{
		"operation":  "add",
		"sourceKind": hostapppack.SourceLocal,
		"sourcePath": packDir,
		"accepted":   true,
		"plan":       malformedPlan,
	}, "invalid host-app request")

	requireRejected("operation mismatch", HostAppPackAPIRequest{
		Operation:  "enable",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Accepted:   true,
		Plan:       &reviewedPlan,
	}, "operation")
	requireRejected("omitted operation", HostAppPackAPIRequest{
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Accepted:   true,
		Plan:       &reviewedPlan,
	}, "operation")

	invalidVersion := reviewedPlan
	invalidVersion.Version = "hideout.host-app-pack-plan/v0"
	requireRejected("malformed typed plan", HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Accepted:   true,
		Plan:       &invalidVersion,
	}, "plan version")

	alteredPlan := reviewedPlan
	alteredPlan.Message = "different effects than the reviewed response"
	requireRejected("altered reviewed plan", HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Accepted:   true,
		Plan:       &alteredPlan,
	}, "stale or malformed")

	requireRejected("unaccepted reviewed plan", HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Plan:       &reviewedPlan,
	}, "explicit acceptance")

	manifestPath := filepath.Join(packDir, hostapppack.ManifestFileName)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(manifest, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	requireRejected("stale reviewed plan", HostAppPackAPIRequest{
		Operation:  "add",
		SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir,
		Accepted:   true,
		Plan:       &reviewedPlan,
	}, "digest mismatch")
}

func callHostAppAPI(t *testing.T, api API, schema *jsonschema.Schema, method, target string, body any, wantStatus int) hostAppAPIEnvelope {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = newAPIRequest(method, target)
	} else {
		req = newAPIJSONRequest(method, target, body)
	}
	req.Header.Set("Authorization", "Bearer ui_token")
	resp := httptest.NewRecorder()
	api.ServeHTTP(resp, req)
	if resp.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, target, resp.Code, wantStatus, resp.Body.String())
	}
	validateManagerAPIResponse(t, schema, resp.Body.Bytes())
	var envelope hostAppAPIEnvelope
	decoder := json.NewDecoder(bytes.NewReader(resp.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode %s %s response: %v\n%s", method, target, err, resp.Body.String())
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("%s %s response has trailing JSON: %v", method, target, err)
	}
	if envelope.Version != APIVersion {
		t.Fatalf("%s %s version=%q", method, target, envelope.Version)
	}
	if wantStatus == http.StatusOK {
		if len(envelope.Errors) != 0 || envelope.Resource == "" || len(envelope.Data) == 0 {
			t.Fatalf("%s %s success envelope=%+v", method, target, envelope)
		}
		wantResource := strings.TrimPrefix(strings.SplitN(target, "?", 2)[0], "/api/v1/")
		if envelope.Resource != wantResource {
			t.Fatalf("%s %s resource=%q want=%q", method, target, envelope.Resource, wantResource)
		}
	} else if len(envelope.Errors) == 0 || len(envelope.Data) != 0 {
		t.Fatalf("%s %s error envelope=%+v", method, target, envelope)
	}
	return envelope
}

func decodeHostAppAPIData[T any](t *testing.T, envelope hostAppAPIEnvelope) T {
	t.Helper()
	var value T
	decoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s data: %v\n%s", envelope.Resource, err, envelope.Data)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("%s data has trailing JSON: %v", envelope.Resource, err)
	}
	return value
}

func assertHostAppRegistryCount(t *testing.T, root string, want int) {
	t.Helper()
	registry, err := hostapppack.NewStore(root).LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Packs) != want {
		t.Fatalf("registry pack count=%d want=%d: %+v", len(registry.Packs), want, registry)
	}
}
