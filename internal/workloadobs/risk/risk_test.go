package risk_test

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestRiskEngineProducesVersionedExplainableDeterministicFindings(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	first := riskFileRecord(t, "act_riskfixture01", "/etc/agent.conf", "external", "write", false)
	second := riskFileRecord(t, "act_riskfixture02", "/etc/agent.conf", "external", "write", false)
	second.FirstAt = first.FirstAt.Add(time.Second)
	second.LastAt = second.FirstAt
	second.FirstSequence, second.LastSequence = 2, 2
	evidence := []risk.Evidence{
		{Activity: first, PolicyStatus: risk.PolicyAllowed},
		{Activity: second, PolicyStatus: risk.PolicyAllowed},
	}
	findings, err := engine.Evaluate(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings=%+v", findings)
	}
	finding := findings[0]
	if finding.RuleID != "file.write-outside-workspace" ||
		finding.RuleVersion != "v1" || finding.Severity != "high" ||
		finding.Confidence != risk.ConfidenceExact ||
		finding.PolicyStatus != risk.PolicyAllowed ||
		finding.Count != 2 ||
		!reflect.DeepEqual(finding.EvidenceRefs, []string{first.ID, second.ID}) ||
		finding.NextAction != "activity.files" {
		t.Fatalf("finding=%+v", finding)
	}
	if finding.Title == "" || finding.Explanation == "" || finding.ID == "" {
		t.Fatalf("finding is not explainable: %+v", finding)
	}
	if err := finding.Validate(); err != nil {
		t.Fatalf("invalid finding: %v", err)
	}

	reverse, err := engine.Evaluate([]risk.Evidence{evidence[1], evidence[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(findings, reverse) {
		t.Fatalf("input order changed findings\nforward=%+v\nreverse=%+v", findings, reverse)
	}
}

func TestRiskEngineKeepsObservedRiskSeparateFromPolicyDecision(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	record := riskFileRecord(t, "act_riskpolicy01", "/etc/agent.conf", "external", "write", false)
	for _, testCase := range []struct {
		policy      string
		disposition string
	}{
		{risk.PolicyAllowed, risk.PolicyDispositionAllowedObserved},
		{risk.PolicyDenied, risk.PolicyDispositionViolation},
		{risk.PolicyNotEvaluated, risk.PolicyDispositionNotEvaluated},
	} {
		t.Run(testCase.policy, func(t *testing.T) {
			findings, err := engine.Evaluate([]risk.Evidence{{
				Activity: record, PolicyStatus: testCase.policy,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 ||
				findings[0].PolicyStatus != testCase.policy ||
				findings[0].PolicyDisposition != testCase.disposition ||
				findings[0].Severity != "high" {
				t.Fatalf("policy changed observed risk: %+v", findings)
			}
		})
	}
	prevented := record
	prevented.ID = "act_riskprevent01"
	prevented.Outcome = workloadtypes.Outcome{Status: workloadtypes.OutcomeDenied}
	findings, err := engine.Evaluate([]risk.Evidence{{
		Activity: prevented, PolicyStatus: risk.PolicyDenied,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 ||
		findings[0].PolicyDisposition != risk.PolicyDispositionPrevented ||
		findings[0].Severity != "high" {
		t.Fatalf("policy prevention was confused with observed severity: %+v", findings)
	}
}

func TestRiskEngineMapsEvidenceAttributionToHonestConfidence(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		attribution string
		confidence  string
	}{
		{workloadtypes.AttributionExact, risk.ConfidenceExact},
		{workloadtypes.AttributionInferred, risk.ConfidenceInferred},
		{workloadtypes.AttributionMediated, risk.ConfidenceLimited},
		{workloadtypes.AttributionUnknown, risk.ConfidenceLimited},
	}
	for index, testCase := range tests {
		t.Run(testCase.attribution, func(t *testing.T) {
			record := riskFileRecord(
				t, "act_riskconfidence0"+string(rune('1'+index)),
				"/etc/agent.conf", "external", "write", false,
			)
			record.Attribution = testCase.attribution
			findings, err := engine.Evaluate([]risk.Evidence{{
				Activity: record, PolicyStatus: risk.PolicyNotEvaluated,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || findings[0].Confidence != testCase.confidence {
				t.Fatalf("findings=%+v want confidence %s", findings, testCase.confidence)
			}
		})
	}
}

func TestRiskEnginePreservesEachDestructiveEvidenceReference(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	first := riskFileRecord(t, "act_riskdelete001", "/workspace/a", "workspace", "unlink", true)
	second := riskFileRecord(t, "act_riskdelete002", "/workspace/b", "workspace", "unlink", true)
	second.FirstSequence, second.LastSequence = 2, 2
	second.FirstAt = first.FirstAt.Add(time.Millisecond)
	second.LastAt = second.FirstAt
	findings, err := engine.Evaluate([]risk.Evidence{
		{Activity: first, PolicyStatus: risk.PolicyNotEvaluated},
		{Activity: second, PolicyStatus: risk.PolicyNotEvaluated},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("destructive findings were collapsed: %+v", findings)
	}
	for _, finding := range findings {
		if finding.RuleID != "file.destructive-change" ||
			len(finding.EvidenceRefs) != 1 {
			t.Fatalf("destructive evidence not preserved: %+v", finding)
		}
	}
}

func TestRiskEngineNeverGroupsAcrossCoverageOrPolicyDisposition(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	base := riskFileRecord(t, "act_riskisolate01", "/etc/agent.conf", "external", "write", false)
	differentCoverage := base
	differentCoverage.ID = "act_riskisolate02"
	differentCoverage.CoverageID = "cov_riskfixture2"
	differentCoverage.FirstSequence = 2
	differentCoverage.LastSequence = 2
	differentCoverage.FirstAt = base.FirstAt.Add(time.Millisecond)
	differentCoverage.LastAt = differentCoverage.FirstAt
	differentPolicy := base
	differentPolicy.ID = "act_riskisolate03"
	differentPolicy.FirstSequence = 3
	differentPolicy.LastSequence = 3
	differentPolicy.FirstAt = base.FirstAt.Add(2 * time.Millisecond)
	differentPolicy.LastAt = differentPolicy.FirstAt

	findings, err := engine.Evaluate([]risk.Evidence{
		{Activity: base, PolicyStatus: risk.PolicyAllowed},
		{Activity: differentCoverage, PolicyStatus: risk.PolicyAllowed},
		{Activity: differentPolicy, PolicyStatus: risk.PolicyDenied},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("coverage/policy evidence was cross-grouped: %+v", findings)
	}
}

func TestRiskEngineDeduplicatesExactEvidenceAndRejectsIDRebinding(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	record := riskFileRecord(t, "act_riskdedupe001", "/etc/agent.conf", "external", "write", false)
	findings, err := engine.Evaluate([]risk.Evidence{
		{Activity: record, PolicyStatus: risk.PolicyAllowed},
		{Activity: record, PolicyStatus: risk.PolicyAllowed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Count != 1 ||
		len(findings[0].EvidenceRefs) != 1 {
		t.Fatalf("exact retry double-counted: %+v", findings)
	}
	rebound := record
	rebound.Count = 2
	if _, err := engine.Evaluate([]risk.Evidence{
		{Activity: record, PolicyStatus: risk.PolicyAllowed},
		{Activity: rebound, PolicyStatus: risk.PolicyAllowed},
	}); !errors.Is(err, risk.ErrEvidenceRebound) {
		t.Fatalf("evidence ID rebinding error=%v", err)
	}
}

func TestRiskEngineRejectsUnredactedOrCrossSessionEvidence(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	pending := riskFileRecord(
		t, "act_riskpending001", "/etc/agent.conf", "external", "write", false,
	)
	pending.RedactionStatus = workloadtypes.RedactionPending
	if _, err := engine.Evaluate([]risk.Evidence{{
		Activity: pending, PolicyStatus: risk.PolicyNotEvaluated,
	}}); !errors.Is(err, risk.ErrInvalidEvidence) {
		t.Fatalf("pending redaction error=%v", err)
	}

	owner, err := workloadtypes.NewDisposableOwner(
		"ses_20260729T120000Z_owner", "lima", "incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	crossSession := riskFileRecord(
		t, "act_riskowner001", "/etc/agent.conf", "external", "write", false,
	)
	crossSession.Owner = owner
	if _, err := engine.Evaluate([]risk.Evidence{{
		Activity: crossSession, PolicyStatus: risk.PolicyNotEvaluated,
	}}); !errors.Is(err, risk.ErrInvalidEvidence) {
		t.Fatalf("cross-session owner error=%v", err)
	}
}

func TestRiskEngineNeverGroupsAcrossOwnerOrSession(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	base := riskFileRecord(
		t, "act_riskbinding01", "/etc/agent.conf", "external", "write", false,
	)
	differentSession := base
	differentSession.ID = "act_riskbinding02"
	differentSession.SessionID = "ses_20260729T120001Z_risk"
	differentSession.FirstSequence, differentSession.LastSequence = 2, 2
	differentSession.FirstAt = base.FirstAt.Add(time.Millisecond)
	differentSession.LastAt = differentSession.FirstAt
	differentOwner := base
	differentOwner.ID = "act_riskbinding03"
	differentOwner.Owner, err = workloadtypes.NewReusableOwner(
		"env_fixture_other", "lima", "incarnation-b",
	)
	if err != nil {
		t.Fatal(err)
	}
	differentOwner.FirstSequence, differentOwner.LastSequence = 3, 3
	differentOwner.FirstAt = base.FirstAt.Add(2 * time.Millisecond)
	differentOwner.LastAt = differentOwner.FirstAt

	findings, err := engine.Evaluate([]risk.Evidence{
		{Activity: base, PolicyStatus: risk.PolicyAllowed},
		{Activity: differentSession, PolicyStatus: risk.PolicyAllowed},
		{Activity: differentOwner, PolicyStatus: risk.PolicyAllowed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("owner/session evidence was cross-grouped: %+v", findings)
	}
}

func TestRiskEngineDisclosesCountOverflow(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	first := riskFileRecord(
		t, "act_riskoverflow01", "/etc/agent.conf", "external", "write", false,
	)
	first.Count = math.MaxUint64
	second := first
	second.ID = "act_riskoverflow02"
	second.FirstSequence, second.LastSequence = 2, 2
	second.FirstAt = first.FirstAt.Add(time.Millisecond)
	second.LastAt = second.FirstAt

	findings, err := engine.Evaluate([]risk.Evidence{
		{Activity: first, PolicyStatus: risk.PolicyAllowed},
		{Activity: second, PolicyStatus: risk.PolicyAllowed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Count != math.MaxUint64 ||
		!findings[0].CountTruncated {
		t.Fatalf("count overflow was not disclosed: %+v", findings)
	}
}

func TestRiskEngineReportsRootExecutionWithExactEvidence(t *testing.T) {
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	record := riskProcessRecord(t, "act_riskroot0001", 0)
	findings, err := engine.Evaluate([]risk.Evidence{{
		Activity: record, PolicyStatus: risk.PolicyNotEvaluated,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 ||
		findings[0].RuleID != "process.root-execution" ||
		findings[0].Severity != risk.SeverityCritical ||
		findings[0].Confidence != risk.ConfidenceExact ||
		!reflect.DeepEqual(findings[0].EvidenceRefs, []string{record.ID}) {
		t.Fatalf("root execution finding=%+v", findings)
	}
}

func TestRiskEngineRejectsInvalidRuleCatalog(t *testing.T) {
	match := func(workloadtypes.ActivityRecord) (string, bool) {
		return "all", true
	}
	rule := risk.Rule{
		ID: "test.rule", Version: "v1", Severity: risk.SeverityLow,
		Title: "Test rule", Explanation: "Test-only rule.",
		NextAction: "activity.summary", Match: match,
	}
	for _, rules := range []risk.RuleSet{
		{},
		{Version: "v1", Rules: []risk.Rule{rule, rule}},
		{Version: "v1", Rules: []risk.Rule{{ID: "invalid", Version: "v1"}}},
	} {
		if _, err := risk.NewEngine(rules); !errors.Is(err, risk.ErrInvalidRuleSet) {
			t.Fatalf("rules=%+v error=%v", rules, err)
		}
	}
}

func TestRiskEngineRejectsInvalidRuntimeRuleGroup(t *testing.T) {
	engine, err := risk.NewEngine(risk.RuleSet{
		Version: "v1",
		Rules: []risk.Rule{{
			ID: "test.bad-group", Version: "v1", Severity: risk.SeverityLow,
			Title: "Test rule", Explanation: "Test-only rule.",
			NextAction: "activity.summary",
			Match: func(workloadtypes.ActivityRecord) (string, bool) {
				return "unsafe\nkey", true
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := riskFileRecord(
		t, "act_riskbadgroup1", "/etc/agent.conf", "external", "write", false,
	)
	if _, err := engine.Evaluate([]risk.Evidence{{
		Activity: record, PolicyStatus: risk.PolicyNotEvaluated,
	}}); !errors.Is(err, risk.ErrInvalidRuleSet) {
		t.Fatalf("runtime rule group error=%v", err)
	}
}

func riskFileRecord(
	t *testing.T,
	id, path, pathClass, operation string,
	destructive bool,
) workloadtypes.ActivityRecord {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: "ses_20260729T120000Z_risk",
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_riskfixture01", PID: 42, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityFile, Operation: operation,
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: path,
			PathState: "resolved", PathClass: pathClass, FileType: "regular",
			Device: 8, Inode: 99, Destructive: destructive,
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: 1, LastSequence: 1,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  "cov_riskfixture1", RedactionStatus: workloadtypes.RedactionPassed,
	}
}

func riskProcessRecord(
	t *testing.T,
	id string,
	uid uint32,
) workloadtypes.ActivityRecord {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture", "lima", "incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: "ses_20260729T120000Z_risk",
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_riskroot0001", PID: 42, UID: uid, GID: uid,
		},
		Kind: workloadtypes.ActivityProcess, Operation: "exec",
		Subject: workloadtypes.ProcessSubject{
			Kind: workloadtypes.ActivityProcess, ExecutionID: "exec_riskroot0001",
			Executable: "/usr/bin/id", Argv: []string{"id"},
			GuestIdentity: workloadtypes.GuestIdentity{UID: uid, GID: uid},
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: 1, LastSequence: 1,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  "cov_riskfixture1", RedactionStatus: workloadtypes.RedactionPassed,
	}
}
