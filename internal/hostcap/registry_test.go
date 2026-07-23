package hostcap

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRegistryValidates(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("registry does not validate: %v", err)
	}
}

func TestRegistryHasImplementedAndDesignReady(t *testing.T) {
	d, ok := Lookup(CapabilityAppOpenResource)
	if !ok {
		t.Fatal("host.app.open-resource not registered")
	}
	if d.Status != StatusImplemented {
		t.Fatalf("host.app.open-resource status = %q, want implemented", d.Status)
	}
	if d.ResultPolicy != ResultNone {
		t.Fatalf("host.app.open-resource resultPolicy = %q, want none", d.ResultPolicy)
	}
	if d.ResidualPolicy != ResidualExternalUnmanaged {
		t.Fatalf("host.app.open-resource residualPolicy = %q, want external-unmanaged", d.ResidualPolicy)
	}
	if d.DecisionPolicy != DecisionDefaultAllowAudited {
		t.Fatalf("safe-mode decisionPolicy = %q, want default-allow-audited", d.DecisionPolicy)
	}
	if d.IntentSchema != IntentSchemaOpenResourceV2 {
		t.Fatalf("host.app.open-resource intentSchema = %q, want unbound v2", d.IntentSchema)
	}
	wantKinds := map[ResourceKind]bool{KindWorkspace: true, KindHostFS: true}
	if len(d.ResourceKinds) != len(wantKinds) {
		t.Fatalf("host.app.open-resource resourceKinds = %v", d.ResourceKinds)
	}
	for _, kind := range d.ResourceKinds {
		if !wantKinds[kind] {
			t.Fatalf("unexpected open-resource kind %q", kind)
		}
	}
	for _, id := range []string{CapabilityServiceBridge, CapabilityAutomationInvoke} {
		dd, ok := Lookup(id)
		if !ok {
			t.Fatalf("%s should be present as design-ready", id)
		}
		if dd.Status != StatusDesignReady {
			t.Fatalf("%s status = %q, want design-ready", id, dd.Status)
		}
	}
}

func TestResultNoneCannotHideManagedResidual(t *testing.T) {
	descriptor := CapabilityDescriptor{
		ID: "host.test.managed-result-none", RiskClass: RiskLow,
		IntentSchema: "test-intent/v1", ResultPolicy: ResultNone,
		ResidualPolicy: ResidualManaged, ProviderRef: ProviderAppOpenResource,
		DecisionPolicy: DecisionDefaultAllowAudited, LifecyclePolicy: LifecycleSession,
		Platforms: []Platform{PlatformDarwin}, Status: StatusImplemented,
	}
	prior := registry
	registry = append(append([]CapabilityDescriptor(nil), prior...), descriptor)
	t.Cleanup(func() { registry = prior })
	if err := Validate(); err == nil {
		t.Fatal("result-none descriptor concealed a managed residual")
	}
}

func TestExternalUnmanagedHandoffInvariant(t *testing.T) {
	if err := EnsureExternalUnmanagedHandoff(CapabilityAppOpenResource); err != nil {
		t.Fatal(err)
	}
	if err := EnsureExternalUnmanagedHandoff(CapabilityServiceBridge); err == nil {
		t.Fatal("managed service bridge passed the external handoff invariant")
	}
}

func TestEnsureImplementedFailsClosedForDesignReadyAndUnknown(t *testing.T) {
	if _, err := EnsureImplemented(CapabilityServiceBridge); CodeOf(err) != CodeCapabilityDesignReady {
		t.Fatalf("design-ready dispatch code = %q, want %q", CodeOf(err), CodeCapabilityDesignReady)
	}
	if _, err := EnsureImplemented(CapabilityAutomationInvoke); CodeOf(err) != CodeCapabilityDesignReady {
		t.Fatalf("applescript dispatch code = %q, want %q", CodeOf(err), CodeCapabilityDesignReady)
	}
	if _, err := EnsureImplemented("host.made.up"); CodeOf(err) != CodeCommandUnbound {
		t.Fatalf("unknown dispatch code = %q, want %q", CodeOf(err), CodeCommandUnbound)
	}
	if _, err := EnsureImplemented(CapabilityAppOpenResource); err != nil {
		t.Fatalf("implemented dispatch should succeed, got %v", err)
	}
}

func TestRegistryHasNoRuntimeRegistrationAPI(t *testing.T) {
	// Compile-time guarantee is the real enforcement (no exported adder). This
	// test documents the invariant and guards against an accidental mutation
	// helper by asserting Registry() returns a copy.
	a := Registry()
	if len(a) == 0 {
		t.Fatal("registry is empty")
	}
	a[0].ID = "mutated"
	b := Registry()
	if b[0].ID == "mutated" {
		t.Fatal("Registry() must return a copy; internal state was mutated")
	}
}

func TestPublicCapabilityDescriptorsMatchStrictSchemaAndDecoder(t *testing.T) {
	schema := loadCapabilityDescriptorSchema(t)
	for _, descriptor := range Registry() {
		raw, err := json.Marshal(descriptor)
		if err != nil {
			t.Fatal(err)
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); err != nil {
			t.Fatalf("descriptor %s failed public schema: %v\n%s", descriptor.ID, err, raw)
		}
		decoded, err := DecodeCapabilityDescriptor(raw)
		if err != nil {
			t.Fatalf("descriptor %s failed strict decode: %v\n%s", descriptor.ID, err, raw)
		}
		if decoded.ID != descriptor.ID || !strings.Contains(string(raw), `"residualPolicy"`) ||
			strings.Contains(string(raw), `"ResidualPolicy"`) {
			t.Fatalf("descriptor %s public shape drifted: %s", descriptor.ID, raw)
		}
	}

	base, err := json.Marshal(Registry()[0])
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(base, &document); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string][]byte{}
	withUnknown := cloneJSONDocument(t, document)
	withUnknown["unexpected"] = true
	fixtures["unknown"] = mustJSON(t, withUnknown)
	missing := cloneJSONDocument(t, document)
	delete(missing, "residualPolicy")
	fixtures["missing"] = mustJSON(t, missing)
	incompatible := cloneJSONDocument(t, document)
	incompatible["residualPolicy"] = 7
	fixtures["incompatible"] = mustJSON(t, incompatible)
	fixtures["trailing"] = append(append([]byte(nil), base...), []byte("\n{}")...)

	for name, raw := range fixtures {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCapabilityDescriptor(raw); err == nil {
				t.Fatalf("strict decoder accepted %s descriptor: %s", name, raw)
			}
			value, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if name == "trailing" {
				if parseErr == nil {
					t.Fatalf("schema JSON parser accepted trailing descriptor: %s", raw)
				}
				return
			}
			if parseErr != nil {
				t.Fatalf("fixture %s is not JSON: %v", name, parseErr)
			}
			if err := schema.Validate(value); err == nil {
				t.Fatalf("public schema accepted %s descriptor: %s", name, raw)
			}
		})
	}
}

func loadCapabilityDescriptorSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "capability-descriptor.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("capability-descriptor.schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("capability-descriptor.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func cloneJSONDocument(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw := mustJSON(t, value)
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestErrorTypedCode(t *testing.T) {
	var e *Error
	if !errors.As(&Error{Code: CodeIntentInvalid}, &e) {
		t.Fatal("Error should satisfy errors.As(*Error)")
	}
}
