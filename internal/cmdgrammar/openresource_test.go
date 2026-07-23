package cmdgrammar

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

func testOpenResourceGrammar() OpenResourceGrammar {
	return OpenResourceGrammar{
		Kind:             GrammarOpenResourceV1,
		ResourceCount:    1,
		GotoFlags:        []string{"-g", "--goto"},
		NewWindowFlags:   []string{"-n", "--new-window"},
		ReuseWindowFlags: []string{"-r", "--reuse-window"},
		UnknownFlags:     UnknownFlagsDeny,
	}
}

func TestParseOpenResourceIsAliasAgnosticAndUnbound(t *testing.T) {
	grammar := testOpenResourceGrammar()
	for _, command := range []string{"code", "cursor", "zed"} {
		intent, err := ParseOpenResource(grammar, []string{command, "src/main.go"}, "/workspace")
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if len(intent.Resources) != 1 || intent.Resources[0].GuestPath != "/workspace/src/main.go" {
			t.Fatalf("%s: unexpected intent %+v", command, intent)
		}
		raw, err := json.Marshal(intent)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"appRef", "app", "binding", "capability", "hostPath", "executable", "argv", "resultPolicy", "access"} {
			if strings.Contains(string(raw), `"`+forbidden+`"`) {
				t.Fatalf("unbound intent contains %q: %s", forbidden, raw)
			}
		}
	}
}

func TestParseOpenResourceGotoAndWindowModes(t *testing.T) {
	grammar := testOpenResourceGrammar()
	intent, err := ParseOpenResource(grammar, []string{"editor", "--new-window", "--goto=src/a.go:12:3"}, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if intent.Location == nil || intent.Location.Line != 12 || intent.Location.Column != 3 || intent.WindowMode != hostcap.WindowNew {
		t.Fatalf("unexpected goto intent: %+v", intent)
	}
	if intent.Resources[0].GuestPath != "/workspace/src/a.go" {
		t.Fatalf("unexpected guest path: %+v", intent.Resources[0])
	}
}

func TestParseOpenResourceRejectsUnknownConflictingAndExtraArguments(t *testing.T) {
	grammar := testOpenResourceGrammar()
	for _, argv := range [][]string{
		{"editor", "--wait", "."},
		{"editor", "-n", "-r", "."},
		{"editor", "a", "b"},
		{"editor", "-g", "a.go:2", "b"},
		{"editor"},
	} {
		if _, err := ParseOpenResource(grammar, argv, "/workspace"); hostcap.CodeOf(err) != hostcap.CodeFlagUnrecognized {
			t.Fatalf("argv %v should fail closed, got %v", argv, err)
		}
	}
}

func TestParseOpenResourceRejectsMalformedGrammarAndBounds(t *testing.T) {
	bad := testOpenResourceGrammar()
	bad.UnknownFlags = "allow"
	if _, err := ParseOpenResource(bad, []string{"editor", "."}, "/workspace"); err == nil {
		t.Fatal("unknown-flag allow policy must be rejected")
	}
	bad = testOpenResourceGrammar()
	bad.GotoFlags = []string{"-g", "-g"}
	if _, err := ParseOpenResource(bad, []string{"editor", "."}, "/workspace"); err == nil {
		t.Fatal("duplicate aliases must be rejected")
	}
	tooLong := strings.Repeat("x", MaxOpenResourceArgumentBytes+1)
	if _, err := ParseOpenResource(testOpenResourceGrammar(), []string{"editor", tooLong}, "/workspace"); hostcap.CodeOf(err) != hostcap.CodeFlagUnrecognized {
		t.Fatalf("oversized argument should fail closed, got %v", err)
	}
	argv := []string{"editor"}
	for range MaxOpenResourceArguments + 1 {
		argv = append(argv, "x")
	}
	if _, err := ParseOpenResource(testOpenResourceGrammar(), argv, "/workspace"); hostcap.CodeOf(err) != hostcap.CodeFlagUnrecognized {
		t.Fatalf("oversized argv should fail closed, got %v", err)
	}
}

func TestUnboundOpenResourceIntentMatchesStrictSchema(t *testing.T) {
	intent, err := ParseOpenResource(testOpenResourceGrammar(), []string{"editor", "."}, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	schema := loadOpenResourceIntentSchema(t)
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil || schema.Validate(value) != nil {
		t.Fatalf("valid unbound intent rejected: %s err=%v", raw, err)
	}

	var forged map[string]any
	if err := json.Unmarshal(raw, &forged); err != nil {
		t.Fatal(err)
	}
	forged["appRef"] = "vscode"
	if err := schema.Validate(forged); err == nil {
		t.Fatal("schema must reject app selection in an unbound intent")
	}
}

func TestUnboundIntentRejectsForgedRelativeMetadataAndResourceBounds(t *testing.T) {
	schema := loadOpenResourceIntentSchema(t)
	for _, fixture := range []struct {
		raw          string
		schemaReject bool
	}{
		{`{"resources":[{"guestPath":"/workspace/a","relativePath":"../../host"}]}`, true},
		{`{"windowMode":"reuse"}`, true},
		{`{"resources":"not-an-array"}`, true},
		{`{"resources":[{"guestPath":"/workspace/a"},{"guestPath":"/workspace/b"}]}`, true},
		// JSON Schema bounds structure; Go owns canonical path semantics.
		{`{"resources":[{"guestPath":"/workspace/../host"}]}`, false},
		{`{"resources":[{"guestPath":"` + strings.Repeat("/x", MaxOpenResourceArgumentBytes) + `"}]}`, true},
	} {
		raw := fixture.raw
		if _, err := DecodeUnboundOpenResourceIntent([]byte(raw)); hostcap.CodeOf(err) != hostcap.CodeIntentInvalid {
			t.Fatalf("forged unbound intent should fail strict decode: %s err=%v", raw, err)
		}
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("invalid test JSON: %v", err)
		}
		if err := schema.Validate(value); (err != nil) != fixture.schemaReject {
			t.Fatalf("schema rejection mismatch for forged intent: %s err=%v", raw, err)
		}
	}
	trailing := `{"resources":[{"guestPath":"/workspace/a"}]} {}`
	if _, err := DecodeUnboundOpenResourceIntent([]byte(trailing)); hostcap.CodeOf(err) != hostcap.CodeIntentInvalid {
		t.Fatalf("trailing intent passed strict decode: %v", err)
	}
	if _, err := jsonschema.UnmarshalJSON(strings.NewReader(trailing)); err == nil {
		t.Fatal("schema JSON parser accepted a trailing intent document")
	}
}

func loadOpenResourceIntentSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaData, err := os.ReadFile(filepath.Join("..", "..", "schemas", "open-resource-intent.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("intent.schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("intent.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
