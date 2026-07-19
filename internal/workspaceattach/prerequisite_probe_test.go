package workspaceattach

import (
	"errors"
	"os"
	"testing"
)

func TestProbeHostRootPrerequisiteObservesRealOpenAndWatcher(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/fixture.txt", []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := ProbeHostRootPrerequisite(root)
	if report.Schema != HostRootPrerequisiteSchema || report.Status != ResearchCheckPassed || report.TCCStatus != "available" || report.Scope != "probed-root-only" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Principals) != 3 || len(report.Checks) < 5 {
		t.Fatalf("incomplete prerequisite inventory: %+v", report)
	}
}

func TestHostRootPrerequisiteDoesNotTurnUnknownIntoPermissionDecision(t *testing.T) {
	if status, _ := classifyHostRootPrerequisite(os.ErrPermission); status != "denied" {
		t.Fatalf("permission status = %q", status)
	}
	if status, _ := classifyHostRootPrerequisite(errors.New("probe failed")); status != "unknown" {
		t.Fatalf("unknown status = %q", status)
	}
	report := ProbeHostRootPrerequisite(t.TempDir() + "/missing")
	if report.Status != ResearchCheckFailed || report.TCCStatus != "unknown" {
		t.Fatalf("missing root report = %+v", report)
	}
}
