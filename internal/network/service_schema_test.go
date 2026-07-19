package network

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEnvironmentServiceStateMatchesStrictSchema(t *testing.T) {
	state := ServiceState{
		Schema:                   EnvironmentServiceSchema,
		EnvironmentID:            "env_20260716t120000z0123456789abcdef",
		Kind:                     "network",
		Status:                   ServiceReady,
		ConfigurationFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ConfigurationID:          "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Mode:                     ModeDirect,
		GatewayID:                "gw_test",
		BootID:                   "01234567-89ab-cdef-0123-456789abcdef",
		StartedAt:                time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		UpdatedAt:                time.Date(2026, 7, 16, 12, 0, 1, 0, time.UTC),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	schemaData, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "schemas", "environment-service-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("service.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("service.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("service state schema: %v\n%s", err, data)
	}
}
