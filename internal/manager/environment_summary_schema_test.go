package manager

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEnvironmentSummarySchemaSeparatesSharedMachineFromWorkspaceBinding(t *testing.T) {
	schema := compileEnvironmentSummarySchema(t)
	shared := map[string]any{
		"schema": "hideout.environment-summary/v1", "id": "env_shared", "name": "default",
		"mode": "shared", "sharedSlot": "default",
		"machineIdentityId":   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bootConfigurationId": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"profile":             "default", "backend": "lima", "status": "running",
		"createdAt": "2026-07-17T00:00:00Z", "activeSessions": 2, "activeWorkspaceViews": 2,
	}
	validateEnvironmentSummary(t, schema, shared, true)

	for _, field := range []string{"workspace", "guestWorkspace", "workspaceLabel"} {
		invalid := cloneEnvironmentSummary(t, shared)
		invalid[field] = "/workspace-must-not-enter-shared-machine-summary"
		validateEnvironmentSummary(t, schema, invalid, false)
	}

	dedicated := cloneEnvironmentSummary(t, shared)
	dedicated["mode"] = "dedicated"
	delete(dedicated, "sharedSlot")
	dedicated["workspace"] = "/Users/operator/project"
	dedicated["guestWorkspace"] = "/workspace"
	dedicated["workspaceLabel"] = "project [01234567]"
	validateEnvironmentSummary(t, schema, dedicated, true)
	delete(dedicated, "workspace")
	validateEnvironmentSummary(t, schema, dedicated, false)
}

func compileEnvironmentSummarySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "schemas", "environment-summary.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("environment-summary.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("environment-summary.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateEnvironmentSummary(t *testing.T, schema *jsonschema.Schema, document map[string]any, wantValid bool) {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	err = schema.Validate(value)
	if wantValid && err != nil {
		t.Fatalf("environment summary schema: %v\n%s", err, data)
	}
	if !wantValid && err == nil {
		t.Fatalf("environment summary schema accepted invalid document: %s", data)
	}
}

func cloneEnvironmentSummary(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
