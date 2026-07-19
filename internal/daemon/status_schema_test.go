package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestDaemonStatusSchemaCarriesSeparateWorkspaceAttachments(t *testing.T) {
	schema := compileDaemonStatusSchema(t)
	workspaceID := "wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	document := map[string]any{
		"version": "hideout.daemon-status/v1", "buildId": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"state": "serving",
		"transport": map[string]any{
			"socket": "/private/store/daemon.sock", "sessionSocket": "/private/store/session.sock",
			"sessionProtocol": "hideout.session/v1",
		},
		"workspaceAttachments": []any{map[string]any{
			"schema": "hideout.workspace-attachment/v1", "attachmentId": "att_fixture",
			"sessionId": "ses_fixture", "environmentId": "env_fixture",
			"incarnation": map[string]any{
				"environmentId": "env_fixture", "startGeneration": 1, "instanceName": "hideout-fixture",
				"bootId": "01234567-89ab-cdef-0123-456789abcdef",
			},
			"workspaceId": workspaceID, "displayLabel": "project [aaaaaaaa]",
			"logicalGuestRoot": "/workspace", "physicalGuestRoot": "/hideout/workspaces/" + workspaceID,
			"transport":    "workspace-portal",
			"providerRef":  map[string]any{"kind": "workspace.host-provider", "id": "provider", "generation": 1},
			"guestViewRef": map[string]any{"kind": "workspace.guest-view", "id": "view", "generation": 1},
			"state":        "ready", "createdAt": "2026-07-17T00:00:00Z",
		}},
	}
	validateDaemonStatusDocument(t, schema, document, true)

	attachment := document["workspaceAttachments"].([]any)[0].(map[string]any)
	attachment["canonicalHostRoot"] = "/Users/operator/private-project"
	validateDaemonStatusDocument(t, schema, document, false)
}

func compileDaemonStatusSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaDir := filepath.Join(filepath.Dir(file), "..", "..", "schemas")
	compiler := jsonschema.NewCompiler()
	const schemaBase = "https://hideout.local/schemas/"
	for _, name := range []string{
		"daemon-status.schema.json", "active-session-summary.schema.json",
		"lifecycle-status.schema.json", "workspace-attachment.schema.json",
	} {
		data, err := os.ReadFile(filepath.Join(schemaDir, name))
		if err != nil {
			t.Fatal(err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(schemaBase+name, document); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile(schemaBase + "daemon-status.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateDaemonStatusDocument(t *testing.T, schema *jsonschema.Schema, document map[string]any, wantValid bool) {
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
		t.Fatalf("daemon status schema: %v\n%s", err, data)
	}
	if !wantValid && err == nil {
		t.Fatalf("daemon status schema accepted invalid document: %s", data)
	}
}
