package export

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceReadersInlineReferencedEvidence(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-release-dogfood.log")
	mustWriteExportTest(t, logPath, "release log user-data\n")
	mustWriteExportTest(t, filepath.Join(dir, "manifest.json"), `{
	  "schema": "hideout.release-dogfood.v1",
	  "evidence": {"directory": "`+dir+`", "log": "test-release-dogfood.log"},
	  "isolationGates": [{"id": "gate3-hidden-proxy", "auditPath": "`+logPath+`"}],
	  "gates": ["gate0"]
	}`)
	bundle, err := readBundleSource(dir)
	if err != nil {
		t.Fatalf("readBundleSource: %v", err)
	}
	bundleJSON := mustJSONExportTest(t, bundle.Body)
	if !strings.Contains(bundleJSON, "release log user-data") ||
		!strings.Contains(bundleJSON, "inline:evidence.logContent") ||
		strings.Contains(bundleJSON, "auditPath") ||
		strings.Contains(bundleJSON, dir) ||
		strings.Contains(bundleJSON, logPath) {
		t.Fatalf("bundle source should inline log and remove local refs: %s", bundleJSON)
	}

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	mustWriteExportTest(t, auditPath, `{"time":"2026-07-07T00:00:00Z","session":"ses_1","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://example.com"}}`+"\n")
	boundary, err := readBoundarySummarySource(map[string]any{
		"version":   "hideout.boundary-summary/v1",
		"evidence":  "available",
		"auditPath": auditPath,
	}, "")
	if err != nil {
		t.Fatalf("readBoundarySummarySource: %v", err)
	}
	boundaryJSON := mustJSONExportTest(t, boundary.Body)
	if !strings.Contains(boundaryJSON, "https://example.com") || strings.Contains(boundaryJSON, "auditPath") || strings.Contains(boundaryJSON, auditPath) {
		t.Fatalf("boundary source should inline audit and remove auditPath: %s", boundaryJSON)
	}
}

func TestSourceReadersRefuseMissingReferences(t *testing.T) {
	dir := t.TempDir()
	mustWriteExportTest(t, filepath.Join(dir, "manifest.json"), `{
  "schema": "hideout.release-dogfood.v1",
  "evidence": {"directory": "`+dir+`", "log": "missing.log"}
}`)
	if _, err := readBundleSource(dir); err == nil {
		t.Fatal("bundle source should refuse a missing referenced log")
	}
	gateDir := t.TempDir()
	mustWriteExportTest(t, filepath.Join(gateDir, "test-release-dogfood.log"), "release log\n")
	mustWriteExportTest(t, filepath.Join(gateDir, "manifest.json"), `{
  "schema": "hideout.release-dogfood.v1",
  "evidence": {"directory": "`+gateDir+`", "log": "test-release-dogfood.log"},
  "isolationGates": [{"id": "gate2-lima", "auditPath": "`+filepath.Join(gateDir, "missing-audit.log")+`"}]
}`)
	if _, err := readBundleSource(gateDir); err == nil {
		t.Fatal("bundle source should refuse a missing isolation gate auditPath")
	}
	if _, err := readBoundarySummarySource(map[string]any{
		"version":   "hideout.boundary-summary/v1",
		"evidence":  "available",
		"auditPath": filepath.Join(t.TempDir(), "missing-audit.jsonl"),
	}, ""); err == nil {
		t.Fatal("boundary source should refuse a missing auditPath")
	}
}
