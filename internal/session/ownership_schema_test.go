package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestActiveSessionSummaryMatchesStrictSchemaAndOmitsControlPlane(t *testing.T) {
	record := validOwnerRecord()
	summary := record.Summary(OwnerLive)
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"owner.lock", "/Users/", "cap_", "HIDEOUT_SECRET_", "\"pid\""} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, data)
		}
	}

	_, file, _, _ := runtime.Caller(0)
	schemaData, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "schemas", "active-session-summary.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("active.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("active.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("summary schema: %v\n%s", err, data)
	}
}
