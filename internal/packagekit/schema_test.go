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

func TestCanonicalEmbeddedAssetSchemaAcceptsExactInventoryAndRejectsDrift(t *testing.T) {
	schema := compileSchemaFile(t, "embedded-asset-manifest.schema.json")
	assets := BrowserConsoleAssets()
	for index := range assets {
		assets[index].SHA256 = BytesSHA256([]byte(assets[index].Path))
	}
	manifest := EmbeddedAssetManifest{
		Schema:          EmbeddedAssetManifestSchema,
		ID:              BrowserConsoleAssetID,
		Container:       BrowserConsoleContainerPath,
		ContainerSHA256: BytesSHA256([]byte("container")),
		License:         BrowserConsoleAssetLicense,
		Assets:          assets,
	}
	if err := validatePackageSchema(schema, manifest); err != nil {
		t.Fatalf("canonical embedded asset manifest rejected: %v", err)
	}
	manifest.Assets[0].Path = "renamed.html"
	if err := validatePackageSchema(schema, manifest); err == nil {
		t.Fatal("drifted embedded asset inventory unexpectedly passed")
	}
}

func TestCanonicalPackageComponentContractSchemaMatchesGoAuthority(t *testing.T) {
	schema := compileSchemaFile(t, "package-components.schema.json")
	contract := ExpectedPackageComponentContract()
	if err := validatePackageSchema(schema, contract); err != nil {
		t.Fatalf("canonical package component contract rejected: %v", err)
	}
	contract.Components[0].License = "unknown"
	if err := validatePackageSchema(schema, contract); err == nil {
		t.Fatal("drifted observer license unexpectedly passed")
	}
}

func compilePackageSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	return compileSchemaFile(t, "package-manifest.schema.json")
}

func compileSchemaFile(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(name)
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
