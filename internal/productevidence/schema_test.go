package productevidence

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/backend"
)

func TestManifestMatchesSchema(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	if err := validateJSON(schema, validManifest().Sanitized()); err != nil {
		t.Fatalf("valid manifest failed schema validation: %v", err)
	}
}

func TestSchemaRejectsUnknownTopLevelField(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	var doc map[string]any
	data, err := json.Marshal(validManifest().Sanitized())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["extra"] = true
	if err := validateJSON(schema, doc); err == nil {
		t.Fatal("schema accepted unknown top-level field")
	}
}

func TestSchemaRejectsNotRunWithoutReason(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	m := validManifest().Sanitized()
	m.Proofs[0].Status = StatusNotRun
	m.Proofs[0].RedactionStatus = RedactionNotRun
	m.Proofs[0].NotRunReason = ""
	m.Proofs[0].Prerequisites = nil
	if err := validateJSON(schema, m); err == nil {
		t.Fatal("schema accepted not-run proof without reason or prerequisite")
	}
}

func TestSchemaRejectsStaleAsProofStatus(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	m := validManifest().Sanitized()
	m.Proofs[0].Status = EvalStale
	if err := validateJSON(schema, m); err == nil {
		t.Fatal("schema accepted stale as proof status")
	}
	if err := m.Validate(); err == nil {
		t.Fatal("manifest validation accepted stale as proof status")
	}
}

func TestFirstRunEvidenceMatchesSchema(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	m := NewManifest("abc123", false)
	m.Proofs = FirstRunLocalFastProofs()
	if err := validateJSON(schema, m.Sanitized()); err != nil {
		t.Fatalf("first-run manifest failed schema validation: %v", err)
	}
}

func TestHostFSDecisionEvidenceMatchesSchema(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	m := NewManifest("abc123", false)
	m.Proofs = HostFSDecisionLocalFastProofs()
	if err := validateJSON(schema, m.Sanitized()); err != nil {
		t.Fatalf("hostfs decision manifest failed schema validation: %v", err)
	}
}

func TestDoctorPackageRecoveryEvidenceMatchesSchema(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	m := NewManifest("abc123", false)
	m.Proofs = DoctorPackageRecoveryLocalFastProofs()
	if err := validateJSON(schema, m.Sanitized()); err != nil {
		t.Fatalf("doctor package recovery manifest failed schema validation: %v", err)
	}
}

func TestDocsTruthEvidenceMatchesSchema(t *testing.T) {
	schema := compileProductEvidenceSchema(t)
	m := NewManifest("abc123", false)
	m.Proofs = DocsTruthProofs()
	if err := validateJSON(schema, m.Sanitized()); err != nil {
		t.Fatalf("docs truth manifest failed schema validation: %v", err)
	}
}

func TestProjectionReadinessManifestMatchesStrictSchema(t *testing.T) {
	schema := compileSchemaFile(t, "projection-readiness.schema.json")
	manifest := backend.ProjectionReadinessManifest{
		Schema: backend.ProjectionReadinessManifestSchema, SessionID: "ses_ready", EnvironmentID: "env_ready",
		SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
		CatalogDigest:     "sha256:" + strings.Repeat("d", 64),
		Entries: []backend.ProjectionReadinessEntry{
			{Name: "hideout-shim", RelativePath: "hideout-shim", SHA256: "sha256:" + strings.Repeat("2", 64), Kind: backend.ProjectionEntryDispatcher},
		},
	}
	if err := validateJSON(schema, manifest); err != nil {
		t.Fatalf("valid projection readiness manifest failed schema validation: %v", err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var unknown map[string]any
	if err := json.Unmarshal(data, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["hostPath"] = "/private/should-not-appear"
	if err := validateJSON(schema, unknown); err == nil {
		t.Fatal("projection readiness schema accepted an unknown private path field")
	}
	delete(unknown, "hostPath")
	delete(unknown, "catalogDigest")
	if err := validateJSON(schema, unknown); err == nil {
		t.Fatal("projection readiness schema accepted a missing catalog digest")
	}
}

func compileProductEvidenceSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	return compileSchemaFile(t, "product-hardening-evidence.schema.json")
}

func compileSchemaFile(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateJSON(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}
