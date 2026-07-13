package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	releaseSchema = "../../schemas/release-dogfood.schema.json"
	exportSchema  = "../../schemas/export-artifact.schema.json"
)

// baseManifest returns a minimal manifest that satisfies every required
// top-level field, so tests can add or mutate the isolation-evidence
// extensions in isolation.
func baseManifest() map[string]any {
	return map[string]any{
		"schema":    "hideout.release-dogfood.v1",
		"status":    "passed",
		"exitCode":  0,
		"startedAt": "2026-07-07T00:00:00Z",
		"endedAt":   "2026-07-07T00:01:00Z",
		"command":   "scripts/test-phase1.sh --release-candidate",
		"evidence": map[string]any{
			"directory": "/tmp/evidence",
			"log":       "test-release-dogfood.log",
		},
		"git":  map[string]any{"commit": "abc123", "dirty": false},
		"host": map[string]any{"uname": "Darwin", "macOSProductVersion": "15.0"},
		"tools": map[string]any{
			"go": "go1.25.0", "limactl": "1.0.0", "jq": "1.7",
		},
		"operatorProxy": map[string]any{
			"provided": true, "scheme": "socks5", "url": "redacted",
		},
		"browser": map[string]any{
			"realBrowserRequired": true, "browserPathProvided": true, "browserApp": "Chrome",
		},
		"gates": []any{
			"gate0-static-contract", "gate1-native-smoke", "gate2-lima-e2e",
			"gate3-hidden-proxy-operator", "gate4-host-escape-real-browser",
			"capability-probe-smoke", "generic-cli-dogfood-smoke",
		},
		"cleanup": map[string]any{
			"gate4BrowserProcesses": 0, "gate4TempDirs": 0, "hideoutLimaInstances": 0,
		},
		"isolationGates": []any{},
		"environmentSnapshot": map[string]any{
			"proxyMode":         "tun2socks",
			"hostPrerequisites": map[string]any{"gate4Browser": false, "envImageURL": false},
			"externalContext":   "ci",
		},
	}
}

func TestManifestRequiresIsolationEvidenceFields(t *testing.T) {
	m := baseManifest()
	delete(m, "isolationGates")
	if got := validate(t, m); got == 0 {
		t.Fatal("manifest without isolationGates accepted, want rejection")
	}
	m = baseManifest()
	delete(m, "environmentSnapshot")
	if got := validate(t, m); got == 0 {
		t.Fatal("manifest without environmentSnapshot accepted, want rejection")
	}
}

func validate(t *testing.T, manifest map[string]any) int {
	t.Helper()
	return validateWithSchema(t, releaseSchema, manifest)
}

func validateWithSchema(t *testing.T, schema string, document map[string]any) int {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(doc, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return run([]string{schema, doc})
}

func TestBaseManifestValidates(t *testing.T) {
	if got := validate(t, baseManifest()); got != 0 {
		t.Fatalf("base manifest validation exit=%d, want 0", got)
	}
}

func TestExportArtifactSchemaValidatesEnvelope(t *testing.T) {
	artifact := map[string]any{
		"version": "hideout.export/v1",
		"provenance": map[string]any{
			"source":    "audit",
			"createdAt": "2026-07-07T00:00:00Z",
			"redactionStages": []any{
				map[string]any{"name": "control-plane"},
				map[string]any{"name": "audit.redact", "id": "policy", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			},
			"decision": map[string]any{"mode": "redact", "channel": "flag"},
		},
		"recordCount": 1,
		"body":        []any{map[string]any{"action": "host.open"}},
	}
	if got := validateWithSchema(t, exportSchema, artifact); got != 0 {
		t.Fatalf("export artifact validation exit=%d, want 0", got)
	}
	artifact["extra"] = true
	if got := validateWithSchema(t, exportSchema, artifact); got == 0 {
		t.Fatal("export artifact with unknown top-level field accepted, want rejection")
	}
}

func TestSchemaValidatorResolvesSiblingSchemaReferences(t *testing.T) {
	root := t.TempDir()
	shared := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$id":"https://example.test/schemas/shared.schema.json",
  "$defs":{"value":{"type":"string","const":"expected"}}
}`
	main := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$id":"https://example.test/schemas/main.schema.json",
  "type":"object",
  "required":["value"],
  "properties":{"value":{"$ref":"shared.schema.json#/$defs/value"}},
  "additionalProperties":false
}`
	sharedPath := filepath.Join(root, "shared.schema.json")
	mainPath := filepath.Join(root, "main.schema.json")
	if err := os.WriteFile(sharedPath, []byte(shared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := validateWithSchema(t, mainPath, map[string]any{"value": "expected"}); got != 0 {
		t.Fatalf("sibling schema reference validation exit=%d, want 0", got)
	}
	if got := validateWithSchema(t, mainPath, map[string]any{"value": "wrong"}); got == 0 {
		t.Fatal("sibling schema reference accepted invalid value")
	}
}

func TestIsolationEvidenceCommandAccepted(t *testing.T) {
	m := baseManifest()
	m["command"] = "scripts/test-phase1.sh --isolation-evidence"
	if got := validate(t, m); got != 0 {
		t.Fatalf("isolation-evidence command exit=%d, want 0", got)
	}
}

func TestValidIsolationGatesAccepted(t *testing.T) {
	m := baseManifest()
	m["isolationGates"] = []any{
		map[string]any{
			"id": "gate2-lima", "backend": "lima", "environmentName": "auto-env-1",
			"result": "passed", "auditPath": "/audit/a.jsonl", "boundarySummary": "ref",
		},
		map[string]any{
			"id": "env-image", "backend": "lima", "result": "not-run",
			"reason": "no image URL declared",
		},
	}
	m["environmentSnapshot"] = map[string]any{
		"proxyMode": "tun2socks",
		"hostPrerequisites": map[string]any{
			"gate4Browser": false, "envImageURL": false,
		},
		"externalContext": "home wifi, upstream 1.1.1.1",
	}
	if got := validate(t, m); got != 0 {
		t.Fatalf("valid isolation evidence exit=%d, want 0", got)
	}
}

func TestIsolationGateUnknownFieldRejected(t *testing.T) {
	m := baseManifest()
	m["isolationGates"] = []any{
		map[string]any{
			"id": "gate2-lima", "backend": "lima", "result": "passed", "bogus": "x",
		},
	}
	if got := validate(t, m); got == 0 {
		t.Fatal("unknown isolationGates field accepted, want rejection")
	}
}

func TestNotRunWithoutReasonRejected(t *testing.T) {
	m := baseManifest()
	m["isolationGates"] = []any{
		map[string]any{"id": "gate4-host-escape", "backend": "lima", "result": "not-run"},
	}
	if got := validate(t, m); got == 0 {
		t.Fatal("not-run without reason accepted, want rejection")
	}
}

func TestPassedNativeBackendRejected(t *testing.T) {
	m := baseManifest()
	m["isolationGates"] = []any{
		map[string]any{"id": "gate2-lima", "backend": "native", "result": "passed"},
	}
	if got := validate(t, m); got == 0 {
		t.Fatal("passed native isolation claim accepted, want rejection")
	}
}

func TestEnvironmentSnapshotUnknownFieldRejected(t *testing.T) {
	m := baseManifest()
	m["environmentSnapshot"] = map[string]any{
		"proxyMode":         "direct",
		"hostPrerequisites": map[string]any{"gate4Browser": true, "envImageURL": true},
		"externalContext":   "ci",
		"bogus":             "x",
	}
	if got := validate(t, m); got == 0 {
		t.Fatal("unknown environmentSnapshot field accepted, want rejection")
	}
}
