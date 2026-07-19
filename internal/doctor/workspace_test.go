package doctor

import (
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestWorkspaceDiagnosticsPassWithObservedPrerequisites(t *testing.T) {
	b := NewBuilder(Request{})
	b.AddWorkspaceDiagnostics(workspaceDiagnosticFixture())
	report := b.Report()
	for _, id := range []string{
		"feature-workspace", "feature-workspace-root", "feature-workspace-mode",
		"feature-workspace-mode-drift", "feature-workspace-metadata", "feature-workspace-lifecycle",
	} {
		finding := workspaceFinding(t, report, id)
		if finding.Status != StatusPass || finding.Code != "" {
			t.Fatalf("%s = %+v", id, finding)
		}
	}
	if principals := workspaceFinding(t, report, "feature-workspace-root").Details["principals"]; principals == nil {
		t.Fatal("workspace root finding omitted TCC principal inventory")
	}
}

func TestWorkspaceDiagnosticsKeepFailuresDistinct(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkspaceDiagnosticInput)
		id     string
		code   string
	}{
		{"transport", func(in *WorkspaceDiagnosticInput) { in.TransportSupported = false }, "feature-workspace", recovery.CodeWorkspaceTransportUnsupported},
		{"permission", func(in *WorkspaceDiagnosticInput) {
			in.Root.Status, in.Root.TCCStatus = workspaceattach.ResearchCheckFailed, "denied"
		}, "feature-workspace-root", recovery.CodeWorkspaceHostPermission},
		{"root identity", func(in *WorkspaceDiagnosticInput) {
			in.Root.Status, in.Root.TCCStatus = workspaceattach.ResearchCheckFailed, "unknown"
		}, "feature-workspace-root", recovery.CodeWorkspaceRootUnstable},
		{"preserve", func(in *WorkspaceDiagnosticInput) { in.PathMode = "preserve" }, "feature-workspace-mode", recovery.CodeEnvironmentSharedPreserve},
		{"mode drift", func(in *WorkspaceDiagnosticInput) { in.Environments[0].SharedSlot = "slot_drifted" }, "feature-workspace-mode-drift", recovery.CodeEnvironmentCompatibilityDrift},
		{"external metadata", func(in *WorkspaceDiagnosticInput) {
			in.MetadataStatus, in.MetadataKind = WorkspaceMetadataExternal, "commondir"
		}, "feature-workspace-metadata", recovery.CodeWorkspaceExternalMetadata},
		{"cleanup", func(in *WorkspaceDiagnosticInput) {
			in.Attachments = []workspaceattach.AttachmentSummary{{State: workspaceattach.AttachmentUnproved, CleanupProof: &workspaceattach.CleanupProof{Status: workspaceattach.CleanupUnproved, ObservedAt: time.Now(), ReasonCode: "cleanup-unproved"}}}
		}, "feature-workspace-lifecycle", recovery.CodeWorkspaceCleanupUnproved},
		{"lifecycle", func(in *WorkspaceDiagnosticInput) {
			in.Lifecycle = []lifecycle.Status{{Activity: lifecycle.ActivityBlocked}}
		}, "feature-workspace-lifecycle", recovery.CodeWorkspaceCleanupUnproved},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := workspaceDiagnosticFixture()
			test.mutate(&input)
			b := NewBuilder(Request{})
			b.AddWorkspaceDiagnostics(input)
			finding := workspaceFinding(t, b.Report(), test.id)
			if finding.Status != StatusError || finding.Code != test.code || finding.Reason == "" || finding.Hint == "" || len(finding.NextActions) == 0 {
				t.Fatalf("finding = %+v", finding)
			}
		})
	}
}

func workspaceDiagnosticFixture() WorkspaceDiagnosticInput {
	return WorkspaceDiagnosticInput{
		Backend: "lima", PathMode: "alias", SelectedTransport: workspaceattach.SelectedTransport,
		TransportSupported: true, ExpectedSharedSlot: "slot_expected", DaemonObserved: true,
		Root: workspaceattach.HostRootPrerequisiteReport{
			Schema: workspaceattach.HostRootPrerequisiteSchema, Status: workspaceattach.ResearchCheckPassed,
			TCCStatus: "available", Scope: "probed-root-only", Principals: workspaceattach.HostRootPrincipalInventory(),
			Checks: []workspaceattach.HostRootPrerequisiteCheck{{ID: "root-open", Status: workspaceattach.ResearchCheckPassed}},
		},
		MetadataStatus: WorkspaceMetadataPassed,
		Environments: []WorkspaceEnvironmentObservation{{
			ID: "env_fixture", Mode: "shared", AutoNamed: true, SharedSlot: "slot_expected",
			ActiveSessions: 1, ActiveWorkspaceViews: 1, WorkspaceProviderState: "ready", OwnerHealth: "live",
		}},
	}
}

func workspaceFinding(t *testing.T, report Report, id string) Finding {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.CheckID == id {
			return finding
		}
	}
	t.Fatalf("finding %q missing from %+v", id, report.Findings)
	return Finding{}
}
