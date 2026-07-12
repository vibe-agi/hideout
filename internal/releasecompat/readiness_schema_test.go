package releasecompat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestReleaseReadinessSchemaRequiresExactReleaseRows(t *testing.T) {
	schema := compileReleaseReadinessSchema(t)
	if err := validateReleaseReadinessJSON(schema, validReleaseReadinessFixture()); err != nil {
		t.Fatalf("valid release readiness failed schema validation: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing commands", mutate: func(document map[string]any) { delete(document, "commands") }},
		{name: "empty commands", mutate: func(document map[string]any) { document["commands"] = []any{} }},
		{name: "missing product command", mutate: func(document map[string]any) {
			document["commands"] = document["commands"].([]any)[:1]
		}},
		{name: "missing command identity", mutate: func(document map[string]any) {
			delete(document["commands"].([]any)[1].(map[string]any), "name")
		}},
		{name: "blank command identity", mutate: func(document map[string]any) {
			document["commands"].([]any)[1].(map[string]any)["name"] = ""
		}},
		{name: "failed product command", mutate: func(document map[string]any) {
			document["commands"].([]any)[1].(map[string]any)["status"] = "failed"
		}},
		{name: "duplicate local command", mutate: func(document map[string]any) {
			document["commands"].([]any)[1].(map[string]any)["name"] = "local-checks"
		}},
		{name: "missing gates", mutate: func(document map[string]any) { delete(document, "gates") }},
		{name: "empty gates", mutate: func(document map[string]any) { document["gates"] = []any{} }},
		{name: "missing gate3", mutate: func(document map[string]any) {
			document["gates"] = document["gates"].([]any)[:1]
		}},
		{name: "duplicate gate2", mutate: func(document map[string]any) {
			document["gates"].([]any)[1].(map[string]any)["id"] = "gate2-lima"
		}},
		{name: "extra gate", mutate: func(document map[string]any) {
			gates := document["gates"].([]any)
			document["gates"] = append(gates, gates[0])
		}},
		{name: "optional gate", mutate: func(document map[string]any) {
			document["gates"].([]any)[0].(map[string]any)["required"] = false
		}},
		{name: "failed gate", mutate: func(document map[string]any) {
			document["gates"].([]any)[0].(map[string]any)["status"] = "failed"
		}},
		{name: "missing gate runtime", mutate: func(document map[string]any) {
			delete(document["gates"].([]any)[0].(map[string]any), "runtime")
		}},
		{name: "empty gate environment", mutate: func(document map[string]any) {
			document["gates"].([]any)[0].(map[string]any)["runtime"].(map[string]any)["environmentId"] = ""
		}},
		{name: "invalid gate environment", mutate: func(document map[string]any) {
			document["gates"].([]any)[0].(map[string]any)["runtime"].(map[string]any)["environmentId"] = "env_INVALID"
		}},
		{name: "dirty gate candidate", mutate: func(document map[string]any) {
			document["gates"].([]any)[0].(map[string]any)["runtime"].(map[string]any)["candidateDirty"] = true
		}},
		{name: "unknown release field", mutate: func(document map[string]any) { document["unexpectedAuthority"] = true }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := releaseReadinessDocument(t)
			tc.mutate(document)
			if err := validateReleaseReadinessJSON(schema, document); err == nil {
				t.Fatalf("schema accepted fabricated releaseReady document: %+v", document)
			}
		})
	}
}

func compileReleaseReadinessSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "release-readiness.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("release-readiness.schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("release-readiness.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func releaseReadinessDocument(t *testing.T) map[string]any {
	t.Helper()
	data, err := json.Marshal(validReleaseReadinessFixture())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func validateReleaseReadinessJSON(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}
