package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const selectorPolicy = `
function redactAudit(ctx) {
  const details = ctx.details;
  const selected = ctx.extra.exportRedaction || [];
  for (let i = 0; i < selected.length; i++) {
    details[selected[i]] = "REDACTED_BY_POLICY";
  }
  return { details, reason: "export redaction" };
}`

func TestExportRedactSelectorScrubsUserFieldAndPreservesUnselected(t *testing.T) {
	storeRoot := t.TempDir()
	writeRedactionProfile(t, storeRoot, "default", selectorPolicy)
	out := filepath.Join(t.TempDir(), "artifact.json")
	_, err := Apply(Request{
		Source:          SourceAudit,
		AuditEvents:     []AuditEvent{testAuditEvent(map[string]any{"target": "secret-target", "note": "keep-me", "command": "open"})},
		Out:             out,
		StoreRoot:       storeRoot,
		RedactSelectors: []string{"target"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "secret-target") || !strings.Contains(text, "REDACTED_BY_POLICY") {
		t.Fatalf("selected user field was not redacted:\n%s", text)
	}
	if !strings.Contains(text, "keep-me") || !strings.Contains(text, `"command"`) || !strings.Contains(text, `"open"`) {
		t.Fatalf("unselected user/evidentiary fields should remain:\n%s", text)
	}
	if !strings.Contains(text, `"id"`) || !strings.Contains(text, `"export-redact"`) || !strings.Contains(text, `"sha256"`) {
		t.Fatalf("artifact provenance missing policy id/sha:\n%s", text)
	}
}

func TestExportRejectsEvidentiarySelectorBeforeScript(t *testing.T) {
	storeRoot := t.TempDir()
	writeRedactionProfile(t, storeRoot, "default", selectorPolicy)
	out := filepath.Join(t.TempDir(), "artifact.json")
	_, err := Apply(Request{
		Source:          SourceAudit,
		AuditEvents:     []AuditEvent{testAuditEvent(map[string]any{"command": "open", "target": "secret"})},
		Out:             out,
		StoreRoot:       storeRoot,
		RedactSelectors: []string{"command"},
	})
	if err == nil || !strings.Contains(err.Error(), "non-redactable evidentiary") {
		t.Fatalf("expected evidentiary selector refusal, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("artifact should not exist after evidentiary selector refusal: %v", statErr)
	}
}

func TestExportRejectsPolicyMutationOfEvidentiaryField(t *testing.T) {
	storeRoot := t.TempDir()
	writeRedactionProfile(t, storeRoot, "default", `
function redactAudit(ctx) {
  const details = ctx.details;
  details.command = "mutated";
  details.target = "REDACTED_BY_POLICY";
  return { details };
}`)
	out := filepath.Join(t.TempDir(), "artifact.json")
	_, err := Apply(Request{
		Source:          SourceAudit,
		AuditEvents:     []AuditEvent{testAuditEvent(map[string]any{"command": "open", "target": "secret"})},
		Out:             out,
		StoreRoot:       storeRoot,
		RedactSelectors: []string{"target"},
	})
	if err == nil || !strings.Contains(err.Error(), "evidentiary field") {
		t.Fatalf("expected policy evidentiary mutation refusal, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("artifact should not exist after policy mutation refusal: %v", statErr)
	}
}

func TestExportAcknowledgeDoesNotBypassConfiguredPolicy(t *testing.T) {
	storeRoot := t.TempDir()
	writeRedactionProfile(t, storeRoot, "default", `
function redactAudit(ctx) {
  const details = ctx.details;
  details.target = "REDACTED_BY_POLICY";
  return { details };
}`)
	out := filepath.Join(t.TempDir(), "artifact.json")
	_, err := Apply(Request{
		Source:                  SourceAudit,
		AuditEvents:             []AuditEvent{testAuditEvent(map[string]any{"target": "secret-target", "note": "keep-me"})},
		Out:                     out,
		StoreRoot:               storeRoot,
		AcknowledgeFullFidelity: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "secret-target") || !strings.Contains(text, "REDACTED_BY_POLICY") || !strings.Contains(text, "keep-me") {
		t.Fatalf("acknowledge bypassed policy or lost residual:\n%s", text)
	}
}

func TestExportResolvesPolicyPerAuditEventProfile(t *testing.T) {
	storeRoot := t.TempDir()
	writeRedactionProfile(t, storeRoot, "alpha", `function redactAudit(ctx) { const d = ctx.details; d.target = "ALPHA"; return { details: d }; }`)
	writeRedactionProfile(t, storeRoot, "beta", `function redactAudit(ctx) { const d = ctx.details; d.target = "BETA"; return { details: d }; }`)
	out := filepath.Join(t.TempDir(), "artifact.json")
	alpha := testAuditEvent(map[string]any{"target": "one"})
	alpha.Profile = "alpha"
	beta := testAuditEvent(map[string]any{"target": "two"})
	beta.Profile = "beta"
	_, err := Apply(Request{
		Source:          SourceAudit,
		AuditEvents:     []AuditEvent{alpha, beta},
		Out:             out,
		StoreRoot:       storeRoot,
		RedactSelectors: []string{"target"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "ALPHA") || !strings.Contains(text, "BETA") || strings.Contains(text, `"target":"one"`) || strings.Contains(text, `"target":"two"`) {
		t.Fatalf("per-profile policy resolution mismatch:\n%s", text)
	}
}
