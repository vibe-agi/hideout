package backend

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestActivationReceiptMatchesStrictSchemaAndOmitsControlPlane(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "lima.yaml")
	if err := os.WriteFile(config, []byte("vmType: vz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := BuildActivationReceipt(
		activationTestSession(root, config),
		"01234567-89ab-cdef-0123-456789abcdef",
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/", "owner.lock", "cap_", "HIDEOUT_SECRET_", "\"pid\""} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("activation receipt leaked %q: %s", forbidden, data)
		}
	}

	_, file, _, _ := runtime.Caller(0)
	schemaData, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "schemas", "environment-activation-receipt.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("activation.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("activation.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("activation receipt schema: %v\n%s", err, data)
	}
}
