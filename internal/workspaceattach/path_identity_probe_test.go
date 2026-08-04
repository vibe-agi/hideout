package workspaceattach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathIdentityProbeKeepsLogicalRootAndDistinctProjectKeys(t *testing.T) {
	first := "wrk_" + strings.Repeat("a", 64)
	second := "wrk_" + strings.Repeat("b", 64)
	firstRoot, err := ResearchPhysicalWorkspaceRoot(first)
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, err := ResearchPhysicalWorkspaceRoot(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstRoot == secondRoot || firstRoot == LogicalWorkspaceRoot || secondRoot == LogicalWorkspaceRoot {
		t.Fatalf("physical roots are not distinct: %q %q", firstRoot, secondRoot)
	}
	var observations []PathIdentityProbeObservation
	for _, tool := range researchPathTools {
		projectKey, projectKeyMode := firstRoot, "physical-cwd"
		if tool == "go" {
			projectKey, projectKeyMode = LogicalWorkspaceRoot, "logical-pwd-alias"
		}
		fixture := tool == "claude" || tool == "codex"
		observations = append(observations, PathIdentityProbeObservation{
			Tool: tool, Version: "fixture-1", LogicalPWD: LogicalWorkspaceRoot,
			PhysicalCWD: firstRoot, ProjectKey: projectKey, ProjectKeyMode: projectKeyMode,
			ProjectStateKey: func() string {
				if fixture {
					return strings.Repeat("c", 64)
				}
				return ""
			}(),
			HistoryState: fixture, CacheState: fixture, SocketState: fixture, RepresentativeFixture: fixture,
			AfterCdDot: firstRoot, AfterCdLogical: firstRoot, SubprocessCWD: firstRoot, ShellReentryCWD: firstRoot,
		})
	}
	if err := ValidatePathIdentityProbe(first, observations); err != nil {
		t.Fatal(err)
	}
}

func TestPathIdentityProbeEvaluationAndStrictInput(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot, _ := ResearchPhysicalWorkspaceRoot(workspaceID)
	observations := make([]PathIdentityProbeObservation, 0, len(researchPathTools))
	for _, tool := range researchPathTools {
		projectKey, projectKeyMode := physicalRoot, "physical-cwd"
		if tool == "go" {
			projectKey, projectKeyMode = LogicalWorkspaceRoot, "logical-pwd-alias"
		}
		fixture := tool == "claude" || tool == "codex"
		observations = append(observations, PathIdentityProbeObservation{
			Tool: tool, Version: "version-1", LogicalPWD: LogicalWorkspaceRoot,
			PhysicalCWD: physicalRoot, ProjectKey: projectKey, ProjectKeyMode: projectKeyMode,
			ProjectStateKey: func() string {
				if fixture {
					return strings.Repeat("c", 64)
				}
				return ""
			}(),
			HistoryState: fixture, CacheState: fixture, SocketState: fixture, RepresentativeFixture: fixture,
			AfterCdDot: physicalRoot, AfterCdLogical: physicalRoot, SubprocessCWD: physicalRoot, ShellReentryCWD: physicalRoot,
		})
	}
	report, err := EvaluatePathIdentityProbe(PathIdentityProbeInput{
		Schema: PathIdentityInputSchema, WorkspaceID: workspaceID, GitSafeDirectories: []string{physicalRoot},
		UnboundGitRejected: true, Observations: observations,
	}, "session-private-symlink")
	if err != nil {
		t.Fatal(err)
	}
	if report.Result != ResearchCheckPassed || report.PhysicalRoot != physicalRoot || report.LogicalRoot != LogicalWorkspaceRoot {
		t.Fatalf("unexpected report: %#v", report)
	}

	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"schema":"hideout.workspace-path-identity-input/v2","workspaceId":"wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","observations":[],"extra":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPathIdentityProbeInput(path); err == nil {
		t.Fatal("unknown path identity field accepted")
	}
}

func TestPathIdentityProbeRejectsMergedOrHostRevealingIdentity(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot, _ := ResearchPhysicalWorkspaceRoot(workspaceID)
	base := make([]PathIdentityProbeObservation, 0, len(researchPathTools))
	for _, tool := range researchPathTools {
		projectKey, projectKeyMode := physicalRoot, "physical-cwd"
		if tool == "go" {
			projectKey, projectKeyMode = LogicalWorkspaceRoot, "logical-pwd-alias"
		}
		fixture := tool == "claude" || tool == "codex"
		base = append(base, PathIdentityProbeObservation{
			Tool: tool, Version: "fixture-1", LogicalPWD: "/workspace", PhysicalCWD: physicalRoot,
			ProjectKey: projectKey, ProjectKeyMode: projectKeyMode,
			ProjectStateKey: func() string {
				if fixture {
					return strings.Repeat("c", 64)
				}
				return ""
			}(),
			HistoryState: fixture, CacheState: fixture, SocketState: fixture,
			RepresentativeFixture: fixture, AfterCdDot: physicalRoot,
			AfterCdLogical: physicalRoot, SubprocessCWD: physicalRoot, ShellReentryCWD: physicalRoot,
		})
	}

	t.Run("fixed bind merges project identity", func(t *testing.T) {
		observations := append([]PathIdentityProbeObservation(nil), base...)
		observations[2].PhysicalCWD = "/workspace"
		observations[2].ProjectKey = "/workspace"
		if err := ValidatePathIdentityProbe(workspaceID, observations); err == nil {
			t.Fatal("fixed logical-only identity accepted")
		}
	})
	t.Run("host path", func(t *testing.T) {
		observations := append([]PathIdentityProbeObservation(nil), base...)
		observations[1].PhysicalCWD = "/Users/alice/project"
		observations[1].ProjectKey = "/Users/alice/project"
		if err := ValidatePathIdentityProbe(workspaceID, observations); err == nil {
			t.Fatal("host path identity accepted")
		}
	})
	t.Run("representative state missing", func(t *testing.T) {
		observations := append([]PathIdentityProbeObservation(nil), base...)
		observations[5].HistoryState = false
		if err := ValidatePathIdentityProbe(workspaceID, observations); err == nil {
			t.Fatal("incomplete Claude representative state accepted")
		}
	})
	t.Run("Go behavior unclassified", func(t *testing.T) {
		observations := append([]PathIdentityProbeObservation(nil), base...)
		observations[4].ProjectKey = physicalRoot
		observations[4].ProjectKeyMode = "physical-cwd"
		if err := ValidatePathIdentityProbe(workspaceID, observations); err == nil {
			t.Fatal("incorrect Go project-key behavior accepted")
		}
	})
}
