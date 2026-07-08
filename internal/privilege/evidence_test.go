package privilege

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPrivilegeEvidenceStripsSetupSecrets(t *testing.T) {
	status := Status{
		Status: StatusDegraded,
		Target: TargetIdentity{
			UID:                   Int(1000),
			SudoN:                 CheckPassed(CheckTargetSudoN, "root"),
			CanPasswordlessSudo:   true,
			PasswordlessSudoKnown: true,
		},
		Setup: SetupIdentity{
			Kind:               SetupRootControlSSH,
			Available:          true,
			SeparateFromTarget: true,
			CredentialLocation: CredentialLocationClass("/Users/null/.hideout/setup/id_ed25519"),
		},
		Reason:   "target user can run passwordless sudo",
		Guidance: "recreate",
	}
	details := StatusDetails(status)
	text := mustJSON(t, details)
	for _, leaked := range []string{
		"/Users/null/.hideout/setup/id_ed25519",
		"cap_0123456789abcdef0123456789abcdef",
		"ui_0123456789abcdef0123456789abcdef",
		"HIDEOUT_SECRET_SETUP",
		"0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("privilege evidence leaked %q:\n%s", leaked, text)
		}
	}
	if !strings.Contains(text, "degraded") || !strings.Contains(text, "does not claim guest-root containment") {
		t.Fatalf("status evidence missing degraded non-claim:\n%s", text)
	}
}

func TestAuditRedactionCoversSetupCredentialFields(t *testing.T) {
	details := StatusDetails(Status{
		Status: StatusUnknown,
		Target: TargetIdentity{UID: Int(1000)},
		Setup:  SetupIdentity{Kind: SetupRootControlSSH, Available: true, SeparateFromTarget: true},
		Checks: []CheckResult{{
			Name:     CheckSetupCredential,
			Status:   CheckError,
			Observed: "setupPrivateKey=-----BEGIN OPENSSH PRIVATE KEY----- HIDEOUT_SECRET_SETUP=secret",
			Error:    "rootControlSSH cap_0123456789abcdef0123456789abcdef machine-id=0123456789abcdef0123456789abcdef",
		}},
		Reason: "setup credential unreadable",
	})
	text := mustJSON(t, details)
	for _, leaked := range []string{
		"OPENSSH PRIVATE KEY", "secret", "cap_0123456789abcdef",
		"0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("setup credential evidence leaked %q:\n%s", leaked, text)
		}
	}
}

func TestTargetRootAttemptEvidenceKeepsAbsolutePathNonClaim(t *testing.T) {
	status := Status{
		Status: StatusDegraded,
		Reason: "target user can run passwordless sudo",
	}
	details := TargetRootAttemptDetails("/usr/bin/sudo", []string{"/usr/bin/sudo", "whoami"}, status, "root-sensitive", "deny", "absolute-path attempt observed")
	text := mustJSON(t, details)
	for _, want := range []string{
		`"command":"/usr/bin/sudo"`,
		`"separationStatus":"degraded"`,
		`"adapterId":"root-sensitive"`,
		`"nonClaim":"Hideout does not claim guest-root containment for this run because privilege separation is degraded."`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("target root attempt evidence missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "root containment enforced") || strings.Contains(text, "absolute-path blocked") {
		t.Fatalf("target root attempt evidence overclaims containment:\n%s", text)
	}
}

func TestPrivilegeStatusSchemaAcceptsRepresentativeStatus(t *testing.T) {
	schema := compilePrivilegeSchema(t)
	status, err := Classify(ClassificationInput{
		Profile: "default",
		Backend: "lima",
		Target: TargetIdentity{
			User:                  "hideout",
			UID:                   Int(1000),
			SudoN:                 CheckFailed(CheckTargetSudoN, "exit 1"),
			AbsoluteSudoN:         CheckFailed(CheckTargetAbsoluteSudo, "exit 1"),
			PasswordlessSudoKnown: true,
		},
		Setup:                   SetupIdentity{Kind: SetupRootControlSSH, Available: true, SeparateFromTarget: true, CredentialLocation: "hideout-control-plane"},
		PrivilegedSetupRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateJSON(schema, status); err != nil {
		t.Fatalf("schema validation failed: %v\n%s", err, mustJSON(t, status))
	}
}

func TestPrivilegeStatusSchemaRejectsInvalidStatus(t *testing.T) {
	schema := compilePrivilegeSchema(t)
	doc := map[string]any{
		"version": "hideout.guest-privilege-status/v1",
		"status":  "maybe",
		"targetIdentity": map[string]any{
			"canPasswordlessSudo":   false,
			"passwordlessSudoKnown": true,
		},
		"setupIdentity": map[string]any{
			"kind":               "none-required",
			"available":          true,
			"separateFromTarget": true,
		},
		"checks": []any{},
		"reason": "bad",
	}
	if err := validateJSON(schema, doc); err == nil {
		t.Fatal("expected invalid status to fail schema validation")
	}
}

func compilePrivilegeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "guest-privilege-status.schema.json"))
	if err != nil {
		t.Fatalf("read privilege schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode privilege schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("guest-privilege-status.schema.json", doc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	schema, err := compiler.Compile("guest-privilege-status.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
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

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
