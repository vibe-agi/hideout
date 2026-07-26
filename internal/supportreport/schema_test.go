package supportreport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSupportReportSchemaAcceptsCanonicalAndRejectsUnknownFields(t *testing.T) {
	report := testReport(t)
	schema := compileSupportReportSchema(t)
	if err := validateSupportReportSchema(schema, report); err != nil {
		t.Fatalf("canonical report rejected: %v", err)
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var unknown map[string]any
	if err := json.Unmarshal(data, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["rawAudit"] = []any{}
	if err := validateSupportReportSchema(schema, unknown); err == nil {
		t.Fatal("unknown top-level field accepted")
	}
}

func compileSupportReportSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "support-report.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("support-report.schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("support-report.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateSupportReportSchema(schema *jsonschema.Schema, value any) error {
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
