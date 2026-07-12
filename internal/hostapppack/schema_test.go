package hostapppack

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/hostcap"
)

func TestGoldenModelsMatchStrictSchemas(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		schemaFile string
		model      func([]byte) error
	}{
		{"manifest", "manifest.valid.json", "host-app-pack.schema.json", func(data []byte) error {
			_, err := DecodeManifest(data)
			return err
		}},
		{"registry", "registry.valid.json", "host-app-pack-registry.schema.json", func(data []byte) error {
			var value Registry
			if err := decodeStrict(data, &value); err != nil {
				return err
			}
			return ValidateRegistry(value)
		}},
		{"enablement", "enablement.valid.json", "host-app-enablement.schema.json", func(data []byte) error {
			var value Enablement
			if err := decodeStrict(data, &value); err != nil {
				return err
			}
			return validateEnablementShape(value)
		}},
		{"inspection", "inspection.valid.json", "host-app-inspection.schema.json", func(data []byte) error {
			var value Inspection
			return decodeStrict(data, &value)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := readFixture(t, tt.fixture)
			if err := tt.model(data); err != nil {
				t.Fatalf("golden fixture failed model validation: %v", err)
			}
			if err := validateSchemaDocument(compileHostAppSchema(t, tt.schemaFile), data); err != nil {
				t.Fatalf("golden fixture failed JSON Schema validation: %v", err)
			}
		})
	}
}

func TestManifestModelAndSchemaRejectShapeDrift(t *testing.T) {
	base := readFixture(t, "manifest.valid.json")
	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing-description", func(value map[string]any) { delete(value, "description") }},
		{"missing-tests", func(value map[string]any) { delete(value, "tests") }},
		{"url-only-hint", func(value map[string]any) { value["installHint"] = map[string]any{"url": "https://example.invalid"} }},
		{"http-hint", func(value map[string]any) {
			value["installHint"] = map[string]any{"text": "Install", "url": "http://example.invalid"}
		}},
		{"backslash-executable", func(value map[string]any) {
			value["apps"].([]any)[0].(map[string]any)["executableRelativePath"] = `Contents\MacOS\Cursor`
		}},
		{"dot-segment-executable", func(value map[string]any) {
			value["apps"].([]any)[0].(map[string]any)["executableRelativePath"] = "Contents/./MacOS/Cursor"
		}},
		{"empty-segment-executable", func(value map[string]any) {
			value["apps"].([]any)[0].(map[string]any)["executableRelativePath"] = "Contents//MacOS/Cursor"
		}},
		{"legacy-guest-path", func(value map[string]any) {
			tests := value["tests"].([]any)
			expected := tests[0].(map[string]any)["expected"].(map[string]any)
			delete(expected, "resource")
			expected["guestPath"] = "/workspace/file"
		}},
	}
	schema := compileHostAppSchema(t, "host-app-pack.schema.json")
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(base, &value); err != nil {
				t.Fatal(err)
			}
			tt.mutate(value)
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeManifest(data); err == nil {
				t.Fatal("model accepted drifted manifest shape")
			}
			if err := validateSchemaDocument(schema, data); err == nil {
				t.Fatal("schema accepted drifted manifest shape")
			}
		})
	}
}

func TestCoreProducedInspectionBundleTreeDigestMatchesSchema(t *testing.T) {
	var produced Inspection
	if err := decodeStrict(readFixture(t, "inspection.valid.json"), &produced); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(produced)
	if err != nil {
		t.Fatal(err)
	}
	schema := compileHostAppSchema(t, "host-app-inspection.schema.json")
	if err := validateSchemaDocument(schema, raw); err != nil {
		t.Fatalf("Core-produced inspection failed its schema: %v", err)
	}
	if got := produced.Entries[0].AppIdentity.ContentDigest; got != hostcap.BundleTreeDigestPrefix+strings.Repeat("c", 64) {
		t.Fatalf("fixture does not exercise the Core bundle-tree identity: %q", got)
	}

	invalidDigests := map[string]string{
		"package-digest":   "sha256:" + strings.Repeat("c", 64),
		"wrong-version":    "bundle-tree-v2:sha256:" + strings.Repeat("c", 64),
		"uppercase-hex":    "bundle-tree-v1:sha256:" + strings.Repeat("C", 64),
		"short-hex":        "bundle-tree-v1:sha256:" + strings.Repeat("c", 63),
		"trailing-content": "bundle-tree-v1:sha256:" + strings.Repeat("c", 64) + "0",
	}
	for name, digest := range invalidDigests {
		t.Run(name, func(t *testing.T) {
			candidate := produced
			candidate.Entries = append([]InspectionEntry(nil), produced.Entries...)
			candidate.Entries[0].AppIdentity.ContentDigest = digest
			data, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateSchemaDocument(schema, data); err == nil {
				t.Fatalf("inspection schema accepted invalid content digest %q", digest)
			}
		})
	}

	t.Run("bundle-prefix-is-not-a-package-digest", func(t *testing.T) {
		candidate := produced
		candidate.Entries = append([]InspectionEntry(nil), produced.Entries...)
		candidate.Entries[0].Package.SourceDigest = produced.Entries[0].AppIdentity.ContentDigest
		data, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateSchemaDocument(schema, data); err == nil {
			t.Fatal("inspection schema conflated package and bundle-tree digest domains")
		}
	})
}

func TestManifestLimitsMatchSchemaConstants(t *testing.T) {
	data, err := os.ReadFile(schemaPath("host-app-pack.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	properties := doc["properties"].(map[string]any)
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"tests", MaxTests, jsonInt(properties["tests"], "maxItems")},
		{"apps", MaxApps, jsonInt(properties["apps"], "maxItems")},
		{"bindings", MaxBindings, jsonInt(properties["bindings"], "maxItems")},
		{"slug-bytes", MaxSlugBytes, jsonInt(defs["slug"], "maxLength")},
		{"pack-id-bytes", MaxPackIDBytes, jsonInt(defs["packId"], "maxLength")},
		{"description-bytes", MaxDescriptionBytes, jsonInt(defs["description"], "maxLength")},
		{"hint-bytes", MaxHintBytes, jsonInt(defs["description"], "maxLength")},
		{"flag-bytes", MaxFlagBytes, jsonInt(defs["flag"], "maxLength")},
		{"bundle-name-bytes", MaxBundleNameBytes, jsonInt(defs["bundleName"], "maxLength")},
		{"executable-bytes", MaxExecutableBytes, jsonInt(defs["relativeExecutable"], "maxLength")},
		{"bundle-names", MaxBundleNames, jsonInt(defs["app"], "properties", "bundleNames", "maxItems")},
		{"grammar-flags", MaxGrammarFlags, jsonInt(defs["flags"], "maxItems")},
		{"argv", MaxArgv, jsonInt(defs["testVector"], "properties", "argv", "maxItems")},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s model limit=%d schema limit=%d", check.name, check.got, check.want)
		}
	}
}

func TestGoldenModelsRejectUnknownFields(t *testing.T) {
	for _, fixture := range []string{"registry.valid.json", "enablement.valid.json", "inspection.valid.json"} {
		t.Run(fixture, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(readFixture(t, fixture), &value); err != nil {
				t.Fatal(err)
			}
			value["unknown"] = true
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var target any
			switch fixture {
			case "registry.valid.json":
				target = &Registry{}
			case "enablement.valid.json":
				target = &Enablement{}
			default:
				target = &Inspection{}
			}
			if err := decodeStrict(data, target); err == nil {
				t.Fatal("strict model accepted unknown field")
			}
		})
	}
}

func TestGoldenModelRoundTripStillMatchesSchema(t *testing.T) {
	tests := []struct {
		fixture, schema string
		decode          func([]byte) any
	}{
		{"manifest.valid.json", "host-app-pack.schema.json", func(data []byte) any {
			value, err := DecodeManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}},
		{"registry.valid.json", "host-app-pack-registry.schema.json", func(data []byte) any {
			var value Registry
			if err := decodeStrict(data, &value); err != nil {
				t.Fatal(err)
			}
			return value
		}},
		{"enablement.valid.json", "host-app-enablement.schema.json", func(data []byte) any {
			var value Enablement
			if err := decodeStrict(data, &value); err != nil {
				t.Fatal(err)
			}
			return value
		}},
		{"inspection.valid.json", "host-app-inspection.schema.json", func(data []byte) any {
			var value Inspection
			if err := decodeStrict(data, &value); err != nil {
				t.Fatal(err)
			}
			return value
		}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			model := tt.decode(readFixture(t, tt.fixture))
			raw, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateSchemaDocument(compileHostAppSchema(t, tt.schema), raw); err != nil {
				t.Fatalf("model JSON tags drifted from schema: %v", err)
			}
		})
	}
}

func compileHostAppSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(schemaPath(name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(name)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateSchemaDocument(schema *jsonschema.Schema, data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func schemaPath(name string) string { return filepath.Join("..", "..", "schemas", name) }

func jsonInt(value any, path ...string) int {
	current := value
	for _, key := range path {
		current = current.(map[string]any)[key]
	}
	return int(current.(float64))
}
