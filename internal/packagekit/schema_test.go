package packagekit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCanonicalPackageSchemaAcceptsManifestAndInstallState(t *testing.T) {
	schema := compilePackageSchema(t)
	root := writeTestArtifact(t, nil)
	manifest := readManifest(t, root)
	if err := validatePackageSchema(schema, manifest); err != nil {
		t.Fatalf("canonical package manifest rejected: %v", err)
	}
	state := NewInstallState("/opt/hideout", "/tmp/store", manifest, manifest.Files, manifest.Layout.Directories, time.Now())
	if err := validatePackageSchema(schema, state); err != nil {
		t.Fatalf("canonical install state rejected: %v", err)
	}
}

func TestCanonicalPackageSchemaRejectsLegacyAndUnknownFields(t *testing.T) {
	schema := compilePackageSchema(t)
	root := writeTestArtifact(t, nil)
	manifest := readManifest(t, root)

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["schema"] = "hideout.package-manifest.v1"
	if err := validatePackageSchema(schema, document); err == nil {
		t.Fatal("unpublished legacy package schema unexpectedly passed")
	}
	document["schema"] = ArtifactSchema
	document["unexpected"] = true
	if err := validatePackageSchema(schema, document); err == nil {
		t.Fatal("unknown package field unexpectedly passed")
	}
}

func compilePackageSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "package-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("package-manifest.schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("package-manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validatePackageSchema(schema *jsonschema.Schema, value any) error {
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
