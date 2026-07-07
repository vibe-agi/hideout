package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/audit"
	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestCoreExportPlanApplyParityWithDirectExport(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	writeManagerExportProfile(t, store, "default", `
function redactAudit(ctx) {
  const details = ctx.details;
  const selected = ctx.extra.exportRedaction || [];
  for (let i = 0; i < selected.length; i++) {
    details[selected[i]] = "REDACTED_BY_POLICY";
  }
  return { details };
}`)
	writeManagerExportAudit(t, store.Root, "ses_export", audit.Event{
		Session:  "ses_export",
		Profile:  "default",
		Backend:  "native",
		Action:   "host.open",
		Decision: "allow",
		Details:  map[string]any{"target": "secret-target", "note": "keep-me", "command": "open"},
	})
	core := New(store)
	managerOut := filepath.Join(t.TempDir(), "manager.json")
	opts := ExportOptions{
		Source:  SourceAuditForManagerTest(),
		Session: "ses_export",
		Out:     managerOut,
		Redact:  []string{"target"},
	}
	plan, err := core.PlanExport(opts)
	if err != nil {
		t.Fatalf("PlanExport: %v", err)
	}
	if plan.Review.Source != exportboundary.SourceAudit || plan.Review.RecordCount != 1 {
		t.Fatalf("manager plan review mismatch: %+v", plan.Review)
	}
	result, err := core.ApplyExport(plan, opts)
	if err != nil {
		t.Fatalf("ApplyExport: %v", err)
	}
	if result.ArtifactPath != managerOut {
		t.Fatalf("artifact path=%q want %q", result.ArtifactPath, managerOut)
	}

	events, err := core.AuditEvents(AuditEventFilter{Session: "ses_export", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	directOut := filepath.Join(t.TempDir(), "direct.json")
	_, err = exportboundary.Apply(exportboundary.Request{
		Source:          exportboundary.SourceAudit,
		AuditEvents:     exportAuditEvents(events),
		Out:             directOut,
		StoreRoot:       store.Root,
		RedactSelectors: []string{"target"},
	})
	if err != nil {
		t.Fatalf("direct export: %v", err)
	}
	managerArtifact := readManagerExportArtifact(t, managerOut)
	directArtifact := readManagerExportArtifact(t, directOut)
	if !strings.Contains(mustJSONManagerExport(t, managerArtifact.Body), "REDACTED_BY_POLICY") ||
		mustJSONManagerExport(t, managerArtifact.Body) != mustJSONManagerExport(t, directArtifact.Body) {
		t.Fatalf("manager/direct artifact body mismatch:\nmanager=%s\ndirect=%s", mustJSONManagerExport(t, managerArtifact.Body), mustJSONManagerExport(t, directArtifact.Body))
	}
}

func TestCoreExportFailClosedParityAndAckPolicy(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	writeManagerExportAudit(t, store.Root, "ses_export", audit.Event{
		Session:  "ses_export",
		Profile:  "default",
		Backend:  "native",
		Action:   "host.open",
		Decision: "allow",
		Details:  map[string]any{"target": "secret-target"},
	})
	core := New(store)
	missingOut := filepath.Join(t.TempDir(), "missing.json")
	missingPlan, err := core.PlanExport(ExportOptions{Source: exportboundary.SourceAudit, Session: "ses_export", Out: missingOut})
	if err != nil {
		t.Fatalf("PlanExport missing decision should return review: %v", err)
	}
	_, managerErr := core.ApplyExport(missingPlan, ExportOptions{Source: exportboundary.SourceAudit, Session: "ses_export", Out: missingOut})
	if managerErr == nil || !strings.Contains(managerErr.Error(), "user data is present") {
		t.Fatalf("manager missing decision error mismatch: %v", managerErr)
	}
	if _, statErr := os.Stat(missingOut); !os.IsNotExist(statErr) {
		t.Fatalf("missing-decision artifact should not exist: %v", statErr)
	}

	writeManagerExportProfile(t, store, "default", `
function redactAudit(ctx) {
  const details = ctx.details;
  details.target = "REDACTED_BY_POLICY";
  return { details };
}`)
	ackOut := filepath.Join(t.TempDir(), "ack.json")
	ackOpts := ExportOptions{Source: exportboundary.SourceAudit, Session: "ses_export", Out: ackOut, AcknowledgeFullFidelity: true}
	ackPlan, err := core.PlanExport(ackOpts)
	if err != nil {
		t.Fatalf("PlanExport ack: %v", err)
	}
	if _, err := core.ApplyExport(ackPlan, ackOpts); err != nil {
		t.Fatalf("ApplyExport ack: %v", err)
	}
	ackData, err := os.ReadFile(ackOut)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ackData), "secret-target") || !strings.Contains(string(ackData), "REDACTED_BY_POLICY") {
		t.Fatalf("acknowledge bypassed manager policy:\n%s", ackData)
	}

	writeManagerExportProfile(t, store, "default", `function redactAudit(ctx) { return { reason: "bad" }; }`)
	badOut := filepath.Join(t.TempDir(), "bad.json")
	badOpts := ExportOptions{Source: exportboundary.SourceAudit, Session: "ses_export", Out: badOut, AcknowledgeFullFidelity: true}
	badPlan, err := core.PlanExport(badOpts)
	if err == nil {
		_, err = core.ApplyExport(badPlan, badOpts)
	}
	if err == nil || !strings.Contains(err.Error(), "audit redaction script") {
		t.Fatalf("expected policy failure, got %v", err)
	}
	if _, statErr := os.Stat(badOut); !os.IsNotExist(statErr) {
		t.Fatalf("policy-failure artifact should not exist: %v", statErr)
	}
}

func SourceAuditForManagerTest() exportboundary.SourceKind {
	return exportboundary.SourceAudit
}

func writeManagerExportProfile(t *testing.T, store profile.Store, name, source string) {
	t.Helper()
	p := profile.Default(name)
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "export-redact",
		Path:        "policy/redact.js",
		Entrypoints: []string{"redactAudit"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.ProfileDir(name), "policy", "redact.js")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeManagerExportAudit(t *testing.T, storeRoot, session string, event audit.Event) {
	t.Helper()
	path := filepath.Join(storeRoot, "sessions", session, "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readManagerExportArtifact(t *testing.T, path string) exportboundary.Artifact {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact exportboundary.Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	return artifact
}

func mustJSONManagerExport(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
