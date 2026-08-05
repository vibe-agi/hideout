package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestOverviewGoldenLayouts(t *testing.T) {
	base := renderFixture()
	cases := []struct {
		name    string
		state   liveconsole.State
		options Options
		color   bool
	}{
		{name: "plain", state: base, options: Options{Width: 96, Height: 26, Unicode: false, NoColor: true}},
		{name: "unicode", state: base, options: Options{Width: 96, Height: 26, Unicode: true, NoColor: false}, color: true},
		{name: "no-color", state: base, options: Options{Width: 96, Height: 26, Unicode: true, NoColor: true}},
		{name: "narrow", state: base, options: Options{Width: 64, Height: 24, Unicode: true, NoColor: true}},
		{name: "idle", state: idleRenderFixture(), options: Options{Width: 96, Height: 26, Unicode: false, NoColor: true}},
		{name: "error", state: staleRenderFixture(), options: Options{Width: 96, Height: 26, Unicode: false, NoColor: true}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := Overview(OverviewInput{
				State: testCase.state,
				Now:   time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			}, testCase.options)
			if testCase.color {
				if !strings.Contains(actual, "\x1b[") {
					t.Fatal("color-capable render contains no ANSI styling")
				}
				actual = StripANSI(actual)
			} else if strings.Contains(actual, "\x1b[") {
				t.Fatalf("no-color/plain render contains ANSI: %q", actual)
			}
			expected, err := os.ReadFile(filepath.Join("..", "testdata", testCase.name+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if actual != string(expected) {
				t.Fatalf("%s render mismatch\n--- want ---\n%s\n--- got ---\n%s", testCase.name, expected, actual)
			}
		})
	}
}

func TestOverviewSanitizesTerminalControlAndBidiInput(t *testing.T) {
	state := renderFixture()
	state.Overview.Sessions[0].CommandClass = "claude\x1b]8;;https://evil.invalid\aCLICK\x1b]8;;\a\u202eexe"
	output := Overview(OverviewInput{State: state}, Options{
		Width: 96, Height: 26, Unicode: false, NoColor: true,
	})
	for _, forbidden := range []string{"\x1b", "\a", "\u202e", "https://evil.invalid"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("render leaked terminal control %q: %q", forbidden, output)
		}
	}
	if !strings.Contains(output, "claudeCLICKexe") {
		t.Fatalf("sanitization removed ordinary context: %q", output)
	}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if DisplayWidth(line) > 96 {
			t.Fatalf("render line width=%d exceeds terminal width: %q", DisplayWidth(line), line)
		}
	}
}

func TestOverviewSeparatesMachineAndWorkspaceViews(t *testing.T) {
	state := renderFixture()
	state.Overview.Environments = []manager.EnvironmentSummary{{
		ID: "env_shared", Name: "default shared", Status: "running",
		MachineIdentityID: "machine_fixture", ActiveSessions: 2,
		ActiveWorkspaceViews: 2, WorkspaceProviderState: "ready",
		OwnerHealth: "live",
	}}
	relation := workspaceattach.RootRelationNotice{
		Relation:         workspaceattach.RootDisjoint,
		SelectedPosition: workspaceattach.RelationPositionPeer,
		OtherWorkspaceID: "wrk_bbbbbbbb",
	}
	state.Overview.Sessions = []manager.SessionSummary{
		{
			ID: "ses_a", Profile: "default", EnvironmentID: "env_shared",
			State: "running", CommandClass: "claude", WorkspaceID: "wrk_aaaaaaaa",
			WorkspaceLabel: "project-a [aaaaaaaa]", GuestWorkspace: "/workspace",
			WorkspaceTransport: "portal", WorkspaceViewState: workspaceattach.AttachmentReady,
			WorkspaceRelations: []workspaceattach.RootRelationNotice{relation},
		},
		{
			ID: "ses_b", Profile: "default", EnvironmentID: "env_shared",
			State: "running", CommandClass: "codex", WorkspaceID: "wrk_bbbbbbbb",
			WorkspaceLabel: "project-b [bbbbbbbb]", GuestWorkspace: "/workspace",
			WorkspaceTransport: "portal", WorkspaceViewState: workspaceattach.AttachmentReady,
			WorkspaceRelations: []workspaceattach.RootRelationNotice{{
				Relation:         workspaceattach.RootDisjoint,
				SelectedPosition: workspaceattach.RelationPositionPeer,
				OtherWorkspaceID: "wrk_aaaaaaaa",
			}},
		},
	}
	output := Overview(OverviewInput{State: state}, Options{
		Width: 120, Height: 40, Unicode: true, NoColor: true,
	})
	for _, expected := range []string{
		"Environment", "sessions=2 active-views=2", "machine-id=machine_fixture",
		"Workspace views", "workspace-view=ready project-a [aaaaaaaa]",
		"workspace-view=ready project-b [bbbbbbbb]",
		"relation=disjoint position=peer",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("overview missing %q:\n%s", expected, output)
		}
	}
	if count := strings.Count(output, "machine-id="); count != 1 {
		t.Fatalf("machine rows=%d want=1:\n%s", count, output)
	}
	if count := strings.Count(output, "workspace-view="); count != 2 {
		t.Fatalf("workspace rows=%d want=2:\n%s", count, output)
	}
}

func TestOverviewActionCenterOmitsTerminalAndAcknowledgedRows(t *testing.T) {
	state := renderFixture()
	state.Decisions = []liveconsole.DecisionRow{
		{ID: "decision-pending", Kind: decision.KindEvidenceShare, Status: decision.StatePending, Summary: "review pending share"},
		{ID: "decision-applied", Kind: decision.KindHostFSWrite, Status: decision.StateApplied, Summary: "already applied"},
	}
	state.Notices = []liveconsole.NoticeRow{
		{ID: "notice-current", Kind: decision.KindPrivilegeStatus, Status: "degraded", Summary: "review current notice"},
		{ID: "notice-acknowledged", Kind: decision.KindBackgroundStatus, Status: "ready", Summary: "already acknowledged", Acknowledged: true},
	}
	output := Overview(OverviewInput{State: state}, Options{
		Width: 96, Height: 26, NoColor: true,
	})
	for _, expected := range []string{"decision-pending", "notice-current"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("action center omitted %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"decision-applied", "notice-acknowledged"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("action center retained inactive %q:\n%s", forbidden, output)
		}
	}
}

func renderFixture() liveconsole.State {
	now := time.Date(2026, 7, 29, 11, 59, 57, 0, time.UTC)
	return liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{
		DaemonInstanceID: "daemon_fixture", CredentialGeneration: 4, EventSequence: 8,
		StreamHealth: liveconsole.HealthLive,
		Overview: manager.Overview{
			Version: "hideout.manager/v1",
			Sessions: []manager.SessionSummary{{
				ID: "ses_alpha", Profile: "default", State: "running", CommandClass: "claude",
				NetworkMode: "tun2socks", StartedAt: now.Add(-2 * time.Minute),
			}},
		},
		Profiles: []manager.ProfileProjection{{
			Schema: manager.ProfileProjectionSchema, Profile: "default", Revision: 2,
			Desired: profile.Profile{Name: "default", Network: profile.Network{
				Mode: profile.NetworkModeTun2Socks, ProxySecretRef: "local-proxy",
				MediatedResolver: "1.1.1.1",
			}},
			Effective: manager.ProfileEffective{
				Status: "effective",
				Network: &manager.EffectiveNetwork{
					Mode: "proxy", ProxySecretRef: "local-proxy", DNS: "1.1.1.1",
				},
				Sessions: []manager.EffectiveSessionSnapshot{},
			},
			UpdatedAt: now,
		}},
		Activity: liveconsole.ActivityProjection{
			Counts: []liveconsole.ActivityCount{{Kind: workloadtypes.ActivityFile, Count: 3}},
		},
		Coverage: []workloadtypes.CoverageInterval{
			renderCoverage("cov_process001", workloadtypes.SubsystemProcess, workloadtypes.CoverageAvailable, "observer-ready", now),
			renderCoverage("cov_file000001", workloadtypes.SubsystemFile, workloadtypes.CoveragePartial, "fanotify-fallback", now),
			renderCoverage("cov_network001", workloadtypes.SubsystemNetwork, workloadtypes.CoverageAvailable, "observer-ready", now),
			renderCoverage("cov_dns0000001", workloadtypes.SubsystemDNS, workloadtypes.CoveragePartial, "encrypted-dns", now),
		},
		Risks: []liveconsole.RiskFinding{{
			ID: "risk_fixture0001", RuleID: "file.outside-workspace", RuleVersion: "v1",
			Severity: "high", Title: "wrote outside workspace",
			Explanation: "a descendant wrote outside the workspace", EvidenceRefs: []string{},
			Confidence: "exact", PolicyStatus: "not-evaluated",
			FirstAt: now, LastAt: now, Count: 1, NextAction: "activity.files",
		}},
		NextActions: []liveconsole.NextActionRef{{
			ID: "activity.files", Label: "inspect file activity", Command: "hideout activity events --kind file",
		}},
	}))
}

func idleRenderFixture() liveconsole.State {
	state := renderFixture()
	state.Overview.Sessions = nil
	state.StreamHealth = liveconsole.StreamHealth{State: liveconsole.HealthIdleLive}
	state.Risks = nil
	state.Coverage = nil
	state.Activity = liveconsole.ActivityProjection{}
	state.NextActions = []liveconsole.NextActionRef{{
		ID: "run.start", Label: "start a protected command", Command: "hideout run -- <command>",
	}}
	return state
}

func staleRenderFixture() liveconsole.State {
	state := renderFixture()
	state.StreamHealth = liveconsole.StreamHealth{State: liveconsole.HealthStale, Reason: "event sequence gap"}
	state.ReadOnly = true
	state.RequiresReseed = true
	state.NextActions = []liveconsole.NextActionRef{{
		ID: "snapshot.refresh", Label: "refresh verified state", Command: "hideout tui",
	}}
	return state
}

func renderCoverage(id, subsystem, state, reason string, at time.Time) workloadtypes.CoverageInterval {
	owner, _ := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	return workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: id, Owner: owner,
		SessionID: "ses_alpha", Subsystem: subsystem, State: state, Reason: reason,
		CollectorGeneration: 1, StartedAt: at,
	}
}
