package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadinessSummaryDerivesReadyAttentionAndBlocked(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		report := Report{
			Profile: "default",
			Backend: "lima",
			Findings: []Finding{
				{CheckID: "network", Status: StatusPass, Summary: "mode=direct dns=host"},
				{CheckID: "workspace", Status: StatusPass, Summary: "workspace is writable"},
			},
		}
		got := ProjectReadiness(report)
		if got.State != ReadinessReady {
			t.Fatalf("state=%q want=%q: %+v", got.State, ReadinessReady, got)
		}
		if got.NetworkPosture != "direct — network origin visible" {
			t.Fatalf("network posture=%q", got.NetworkPosture)
		}
		if len(got.ActionableFindings) != 0 {
			t.Fatalf("ready projection exposed findings: %+v", got.ActionableFindings)
		}
		if len(got.NextCommands) != 1 || got.NextCommands[0] != "hideout run -- git status --short" {
			t.Fatalf("ready next commands=%v", got.NextCommands)
		}
	})

	t.Run("attention", func(t *testing.T) {
		report := Report{
			Profile: "default",
			Backend: "lima",
			Findings: []Finding{
				{CheckID: "profile", Status: StatusWarn, Summary: "default missing"},
				{CheckID: "profile-init", Status: StatusWarn, Summary: "profile state is not materialized"},
			},
		}
		got := ProjectReadiness(report)
		if got.State != ReadinessAttention {
			t.Fatalf("state=%q want=%q: %+v", got.State, ReadinessAttention, got)
		}
		if len(got.ActionableFindings) != 1 {
			t.Fatalf("setup findings were not coalesced: %+v", got.ActionableFindings)
		}
		finding := got.ActionableFindings[0]
		if finding.SourceFindingID != "profile" || finding.NextAction != "hideout setup" {
			t.Fatalf("finding lost source/action: %+v", finding)
		}
	})

	t.Run("blocked", func(t *testing.T) {
		report := Report{
			Profile: "default",
			Backend: "lima",
			Findings: []Finding{{
				CheckID:  "runtime",
				Status:   StatusError,
				Required: true,
				Code:     "runtime-damaged",
				Reason:   "runtime digest did not match",
				Summary:  "runtime is damaged",
			}},
		}
		got := ProjectReadiness(report)
		if got.State != ReadinessBlocked {
			t.Fatalf("state=%q want=%q: %+v", got.State, ReadinessBlocked, got)
		}
		if len(got.ActionableFindings) != 1 {
			t.Fatalf("blocked projection findings=%+v", got.ActionableFindings)
		}
		finding := got.ActionableFindings[0]
		if finding.SourceFindingID != "runtime" || finding.RecoveryCode != "runtime-damaged" {
			t.Fatalf("finding lost traceability: %+v", finding)
		}
		if finding.Reason != "runtime digest did not match" || finding.NextAction == "" {
			t.Fatalf("finding is not actionable: %+v", finding)
		}
	})
}

func TestReadinessSummaryFiltersMaintainerOnlyActions(t *testing.T) {
	report := Report{
		Profile: "default",
		Backend: "lima",
		Findings: []Finding{{
			CheckID:     "privacy-evidence",
			Status:      StatusWarn,
			Summary:     "real privacy evidence is not promoted",
			NextActions: []string{"scripts/test-gate3-hidden-proxy.sh"},
		}},
	}
	got := ProjectReadiness(report)
	if got.State != ReadinessReady || len(got.ActionableFindings) != 0 {
		t.Fatalf("maintainer-only warning affected ordinary readiness: %+v", got)
	}

	var out bytes.Buffer
	WriteReadiness(&out, got)
	if strings.Contains(out.String(), "scripts/test-") {
		t.Fatalf("concise output exposed maintainer action:\n%s", out.String())
	}
	if lines := nonBlankLines(out.String()); lines > 10 {
		t.Fatalf("healthy summary has %d non-blank lines, want <=10:\n%s", lines, out.String())
	}
}

func TestReadinessSummaryPreservesSafeActionWhenFindingAlsoHasMaintainerAction(t *testing.T) {
	report := Report{
		Profile: "default",
		Backend: "lima",
		Findings: []Finding{{
			CheckID: "daemon",
			Status:  StatusWarn,
			Summary: "daemon is not available",
			NextActions: []string{
				"scripts/test-daemon-smoke.sh",
				"hideout daemon start",
			},
		}},
	}
	got := ProjectReadiness(report)
	if got.State != ReadinessAttention || len(got.ActionableFindings) != 1 {
		t.Fatalf("safe action was not retained: %+v", got)
	}
	if got.ActionableFindings[0].NextAction != "hideout daemon start" {
		t.Fatalf("wrong ordinary action: %+v", got.ActionableFindings[0])
	}
}

func nonBlankLines(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
