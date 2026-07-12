package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestDoctorReportMatchesSchema(t *testing.T) {
	b := NewBuilder(Request{Profile: "default", Backend: "native"})
	b.Add("store", "store", StatusPass, "writable")
	b.Add("packaging", "packaging", StatusWarn, "external prerequisite missing",
		WithRecovery(recovery.CodePackagePrerequisiteMissing),
		WithRequired(false),
	)
	report := b.Report()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := compileDoctorSchema(t).Validate(doc); err != nil {
		t.Fatalf("report did not validate schema: %v\n%s", err, data)
	}
}

func compileDoctorSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "doctor-report.schema.json"))
	if err != nil {
		t.Fatalf("read doctor schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode doctor schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("doctor-report.schema.json", doc); err != nil {
		t.Fatalf("add doctor schema: %v", err)
	}
	schema, err := compiler.Compile("doctor-report.schema.json")
	if err != nil {
		t.Fatalf("compile doctor schema: %v", err)
	}
	return schema
}
