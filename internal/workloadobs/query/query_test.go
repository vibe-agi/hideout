package query_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/query"
	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestEventsFiltersDeterministicallyAndBindsCursorToOwnerFiltersAndRevision(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-a")
	otherOwner := reusableOwner(t, "env_query", "incarnation-b")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_query0001", Owner: owner,
		Records: []workloadtypes.ActivityRecord{
			fileRecord(t, owner, "act_queryfile0001", "exec_queryroot0001", 1, at, "/workspace/a.txt"),
			fileRecord(t, owner, "act_queryfile0002", "exec_queryroot0001", 2, at.Add(time.Second), "/workspace/a.txt"),
			dnsRecord(t, owner, "act_querydns00001", "exec_queryroot0001", 3, at.Add(2*time.Second)),
		},
		Coverage: []workloadtypes.CoverageInterval{
			coverageInterval(t, owner, "cov_queryfile0001", workloadtypes.SubsystemFile,
				workloadtypes.CoveragePartial, "retention-pruned", true, at),
		},
	})
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_queryother1", Owner: otherOwner,
	})
	service := newService(t, source)

	first, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, Limit: 1,
		Kinds: []string{workloadtypes.ActivityFile},
		Path:  "/workspace/a.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 1 || first.Records[0].ID != "act_queryfile0001" ||
		first.NextCursor == "" || !first.QueryTruncated ||
		len(first.Coverage) != 1 || !first.Coverage[0].RetentionGap {
		t.Fatalf("first page=%+v", first)
	}

	second, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 1 || second.Records[0].ID != "act_queryfile0002" ||
		second.NextCursor != "" || second.QueryTruncated {
		t.Fatalf("second page=%+v", second)
	}
	if _, err := service.Events(context.Background(), query.EventsQuery{
		Owner: otherOwner, Cursor: first.NextCursor,
	}); !errors.Is(err, query.ErrCursorOwnerMismatch) {
		t.Fatalf("cross-owner cursor error=%v", err)
	}
	if _, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, Cursor: first.NextCursor,
		Kinds: []string{workloadtypes.ActivityDNS},
	}); !errors.Is(err, query.ErrCursorFilterMismatch) {
		t.Fatalf("cross-filter cursor error=%v", err)
	}

	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_query0002", Owner: owner,
		Records: []workloadtypes.ActivityRecord{
			fileRecord(t, owner, "act_queryfile0001", "exec_queryroot0001", 1, at, "/workspace/a.txt"),
			fileRecord(t, owner, "act_queryfile0002", "exec_queryroot0001", 2, at.Add(time.Second), "/workspace/a.txt"),
		},
	})
	if _, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, Cursor: first.NextCursor,
	}); !errors.Is(err, query.ErrCursorStale) {
		t.Fatalf("stale cursor error=%v", err)
	}
}

func TestEventsFiltersFileDomainIPExecutionAndRiskEvidence(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-filters")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	file := fileRecord(
		t, owner, "act_queryfilter01", "exec_queryroot0001", 1, at, "/etc/result.txt",
	)
	dns := dnsRecord(
		t, owner, "act_queryfilter02", "exec_querychild001", 2, at.Add(time.Second),
	)
	finding := riskFinding(t, file, risk.PolicyNotEvaluated)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_queryfilter1", Owner: owner,
		Records: []workloadtypes.ActivityRecord{dns, file},
		Risks:   []risk.Finding{finding},
	})
	service := newService(t, source)

	for name, eventQuery := range map[string]query.EventsQuery{
		"path": {
			Owner: owner, Limit: 100, Path: "/etc/result",
		},
		"domain": {
			Owner: owner, Limit: 100, Domain: "API.EXAMPLE.TEST",
		},
		"ip": {
			Owner: owner, Limit: 100, IP: "203.0.113.10",
		},
		"execution": {
			Owner: owner, Limit: 100, Executions: []string{"exec_querychild001"},
		},
		"risk-id": {
			Owner: owner, Limit: 100, Risks: []string{finding.ID},
		},
		"risk-rule": {
			Owner: owner, Limit: 100, Risks: []string{finding.RuleID},
		},
	} {
		t.Run(name, func(t *testing.T) {
			page, err := service.Events(context.Background(), eventQuery)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 1 {
				t.Fatalf("page=%+v", page)
			}
			want := file.ID
			if name == "domain" || name == "ip" || name == "execution" {
				want = dns.ID
			}
			if page.Records[0].ID != want {
				t.Fatalf("record=%s want=%s", page.Records[0].ID, want)
			}
		})
	}
}

func TestExecutionsPreservesTreeCountsSelectionAndPrunedParents(t *testing.T) {
	owner := disposableOwner(t, "ses_20260729T120000Z_querytree", "incarnation-tree")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	root := execution(t, owner, owner.SessionID, 100, 1, 1000, "")
	child := execution(t, owner, owner.SessionID, 101, 2, 1100, root.ID)
	orphan := execution(t, owner, owner.SessionID, 102, 3, 1200, "exec_querypruned01")
	records := []workloadtypes.ActivityRecord{
		fileRecord(t, owner, "act_querytree0001", root.ID, 1, at, "/workspace/a"),
		fileRecord(t, owner, "act_querytree0002", root.ID, 2, at.Add(time.Second), "/workspace/b"),
		dnsRecord(t, owner, "act_querytree0003", child.ID, 3, at.Add(2*time.Second)),
	}
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_querytree01", Owner: owner,
		Records: records, Executions: []workloadtypes.Execution{orphan, child, root},
	})
	service := newService(t, source)

	result, err := service.Executions(context.Background(), query.ExecutionsQuery{
		Owner: owner, RootsOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Roots) != 2 {
		t.Fatalf("roots=%+v", result.Roots)
	}
	if result.Roots[0].Execution.ID != root.ID ||
		len(result.Roots[0].Children) != 1 ||
		result.Roots[0].Children[0].Execution.ID != child.ID ||
		result.Roots[0].ActivityCounts[workloadtypes.ActivityFile] != 2 ||
		result.Roots[0].Children[0].ActivityCounts[workloadtypes.ActivityDNS] != 1 {
		t.Fatalf("tree/counts=%+v", result.Roots)
	}
	if result.Roots[1].Execution.ID != orphan.ID || !result.Roots[1].ParentUnavailable {
		t.Fatalf("pruned parent was hidden: %+v", result.Roots[1])
	}

	selected, err := service.Executions(context.Background(), query.ExecutionsQuery{
		Owner: owner, ID: child.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Roots) != 1 || selected.Roots[0].Execution.ID != child.ID ||
		len(selected.Roots[0].Children) != 0 {
		t.Fatalf("selected subtree=%+v", selected)
	}
}

func TestSummaryReportsCountsCurrentCoverageRiskRetentionQuotaAndLatestCursor(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-summary")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	file := fileRecord(
		t, owner, "act_querysummary1", "exec_queryroot0001", 1, at, "/etc/agent.conf",
	)
	file.Count = 3
	finding := riskFinding(t, file, risk.PolicyAllowed)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_querysummary1", Owner: owner,
		Records: []workloadtypes.ActivityRecord{file},
		Coverage: []workloadtypes.CoverageInterval{
			coverageInterval(t, owner, "cov_querysummary1", workloadtypes.SubsystemFile,
				workloadtypes.CoverageAvailable, "observer-ready", false, at),
			coverageInterval(t, owner, "cov_querysummary2", workloadtypes.SubsystemFile,
				workloadtypes.CoveragePartial, "retention-pruned", true, at.Add(time.Second)),
		},
		Risks: []risk.Finding{finding},
		Retention: query.RetentionState{
			EarliestAt: at.Add(-time.Minute), LatestAt: at.Add(time.Minute),
			UsedBytes: 2048, LimitBytes: 4096, Pruned: true,
			Corrupt: true, Reasons: []string{"corrupt-segment", "retention-pruned"},
		},
	})
	service := newService(t, source)

	summary, err := service.Summary(context.Background(), query.SummaryQuery{Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Counts[workloadtypes.ActivityFile] != 3 ||
		len(summary.CurrentCoverage) != 1 ||
		summary.CurrentCoverage[0].ID != "cov_querysummary2" ||
		len(summary.HighestRisks) != 1 ||
		summary.HighestRisks[0].ID != finding.ID ||
		summary.RetainedRange.From != at.Add(-time.Minute) ||
		summary.RetainedRange.To != at.Add(time.Minute) ||
		summary.Quota.UsedBytes != 2048 || summary.Quota.LimitBytes != 4096 ||
		!summary.Pruned || !summary.Corrupt ||
		!reflect.DeepEqual(summary.Reasons, []string{"corrupt-segment", "retention-pruned"}) ||
		summary.LatestCursor == "" {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestReusableOwnerQueriesRemainInsideSelectedSession(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-session-scope")
	const (
		sessionA = "ses_20260729T120000Z_alpha"
		sessionB = "ses_20260729T120001Z_beta"
	)
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	executionA := execution(t, owner, sessionA, 100, 1, 1000, "")
	executionB := execution(t, owner, sessionB, 101, 1, 1100, "")
	fileA := fileRecord(
		t, owner, "act_querysessiona1", executionA.ID, 1, at, "/etc/alpha.conf",
	)
	fileA.SessionID = sessionA
	fileA.CoverageID = "cov_querysessiona1"
	dnsA := dnsRecord(
		t, owner, "act_querysessiona2", executionA.ID, 2,
		at.Add(time.Second),
	)
	dnsA.SessionID = sessionA
	dnsA.CoverageID = "cov_querysessiona2"
	fileB := fileRecord(
		t, owner, "act_querysessionb1", executionB.ID, 3,
		at.Add(2*time.Second), "/etc/beta.conf",
	)
	fileB.SessionID = sessionB
	fileB.CoverageID = "cov_querysessionb1"
	coverageA := coverageInterval(
		t, owner, "cov_querysessiona1", workloadtypes.SubsystemProcess,
		workloadtypes.CoverageAvailable, "observer-ready", false, at,
	)
	coverageA.SessionID = sessionA
	coverageB := coverageInterval(
		t, owner, "cov_querysessionb1", workloadtypes.SubsystemProcess,
		workloadtypes.CoverageAvailable, "observer-ready", false,
		at.Add(2*time.Second),
	)
	coverageB.SessionID = sessionB
	riskA := riskFinding(t, fileA, risk.PolicyNotEvaluated)
	riskB := riskFinding(t, fileB, risk.PolicyNotEvaluated)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision:   "rev_querysessions1",
		Owner:      owner,
		Records:    []workloadtypes.ActivityRecord{fileA, dnsA, fileB},
		Executions: []workloadtypes.Execution{executionA, executionB},
		Coverage:   []workloadtypes.CoverageInterval{coverageA, coverageB},
		Risks:      []risk.Finding{riskA, riskB},
	})
	service := newService(t, source)

	summary, err := service.Summary(context.Background(), query.SummaryQuery{
		Owner: owner, SessionID: sessionA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Counts[workloadtypes.ActivityFile] != 1 ||
		summary.Counts[workloadtypes.ActivityDNS] != 1 ||
		len(summary.CurrentCoverage) != 1 ||
		summary.CurrentCoverage[0].SessionID != sessionA ||
		len(summary.HighestRisks) != 1 ||
		summary.HighestRisks[0].SessionID != sessionA ||
		!summary.RetainedRange.From.Equal(at) ||
		!summary.RetainedRange.To.Equal(at.Add(time.Second)) {
		t.Fatalf("session-scoped summary=%+v", summary)
	}

	first, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, SessionID: sessionA, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 1 ||
		first.Records[0].SessionID != sessionA ||
		first.NextCursor == "" ||
		len(first.Coverage) != 1 ||
		first.Coverage[0].SessionID != sessionA {
		t.Fatalf("session-scoped first page=%+v", first)
	}
	second, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, SessionID: sessionA,
		Cursor: first.NextCursor, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 1 ||
		second.Records[0].SessionID != sessionA ||
		second.NextCursor != "" {
		t.Fatalf("session-scoped second page=%+v", second)
	}
	if _, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, SessionID: sessionB,
		Cursor: first.NextCursor, Limit: 1,
	}); !errors.Is(err, query.ErrCursorFilterMismatch) {
		t.Fatalf("cross-session cursor error=%v", err)
	}

	executions, err := service.Executions(
		context.Background(),
		query.ExecutionsQuery{Owner: owner, SessionID: sessionA},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions.Roots) != 1 ||
		executions.Roots[0].Execution.SessionID != sessionA ||
		len(executions.Coverage) != 1 ||
		executions.Coverage[0].SessionID != sessionA {
		t.Fatalf("session-scoped executions=%+v", executions)
	}
	if _, err := service.Executions(
		context.Background(),
		query.ExecutionsQuery{
			Owner: owner, SessionID: sessionA, ID: executionB.ID,
		},
	); !errors.Is(err, query.ErrExecutionNotFound) {
		t.Fatalf("cross-session execution selection error=%v", err)
	}

	coverage, err := service.Coverage(context.Background(), query.CoverageQuery{
		Owner: owner, SessionID: sessionA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.Intervals) != 1 ||
		coverage.Intervals[0].SessionID != sessionA ||
		len(coverage.Current) != 1 ||
		coverage.Current[0].SessionID != sessionA {
		t.Fatalf("session-scoped coverage=%+v", coverage)
	}

	risks, err := service.Risks(context.Background(), query.RisksQuery{
		Owner: owner, SessionID: sessionA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(risks.Findings) != 1 ||
		risks.Findings[0].SessionID != sessionA {
		t.Fatalf("session-scoped risks=%+v", risks)
	}
}

func TestRisksFiltersBySeverityRuleExecutionAndTime(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-risks")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	file := fileRecord(
		t, owner, "act_queryrisks001", "exec_queryroot0001", 1, at, "/etc/agent.conf",
	)
	finding := riskFinding(t, file, risk.PolicyNotEvaluated)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_queryrisks01", Owner: owner,
		Records: []workloadtypes.ActivityRecord{file},
		Risks:   []risk.Finding{finding},
	})
	service := newService(t, source)

	result, err := service.Risks(context.Background(), query.RisksQuery{
		Owner: owner, Severities: []string{risk.SeverityHigh},
		Rules: []string{finding.RuleID}, Executions: []string{"exec_queryroot0001"},
		From: at.Add(-time.Second), To: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].ID != finding.ID {
		t.Fatalf("risks=%+v", result)
	}
	result, err = service.Risks(context.Background(), query.RisksQuery{
		Owner: owner, Executions: []string{"exec_queryother001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("unrelated execution risks=%+v", result)
	}
}

func TestMemorySourceRejectsCrossOwnerDataAndClonesSnapshots(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-clone")
	other := reusableOwner(t, "env_other", "incarnation-clone")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	source := query.NewMemorySource()
	if err := source.Replace(query.Snapshot{
		Revision: "rev_queryinvalid1", Owner: owner,
		Records: []workloadtypes.ActivityRecord{
			fileRecord(t, other, "act_queryinvalid1", "exec_queryroot0001", 1, at, "/tmp/a"),
		},
	}); !errors.Is(err, query.ErrInvalidSnapshot) {
		t.Fatalf("cross-owner snapshot error=%v", err)
	}
	record := fileRecord(
		t, owner, "act_queryclone001", "exec_queryroot0001", 1, at, "/workspace/a",
	)
	snapshot := query.Snapshot{
		Revision: "rev_queryclone01", Owner: owner,
		Records: []workloadtypes.ActivityRecord{record},
	}
	replaceSnapshot(t, source, snapshot)
	snapshot.Records[0].Subject = workloadtypes.FileSubject{
		Kind: workloadtypes.ActivityFile, Path: "/mutated",
		PathState: "resolved", PathClass: "external", FileType: "regular",
	}
	loaded, err := source.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Records[0].Subject.(workloadtypes.FileSubject).Path != "/workspace/a" {
		t.Fatalf("source retained caller alias: %+v", loaded.Records[0])
	}
	loaded.Records[0].ID = "act_querymutated1"
	reloaded, err := source.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Records[0].ID != record.ID {
		t.Fatal("snapshot result aliased source state")
	}
}

func TestMemorySourceRejectsCrossSessionExecutionLinksAndCycles(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-graph")
	first := execution(t, owner, "ses_20260729T120000Z_first", 100, 1, 1000, "")
	second := execution(t, owner, "ses_20260729T120001Z_second", 101, 2, 1100, first.ID)
	source := query.NewMemorySource()
	if err := source.Replace(query.Snapshot{
		Revision: "rev_querygraph001", Owner: owner,
		Executions: []workloadtypes.Execution{first, second},
	}); !errors.Is(err, query.ErrInvalidSnapshot) {
		t.Fatalf("cross-session parent error=%v", err)
	}

	cycleFirst := execution(
		t, owner, "ses_20260729T120002Z_cycle", 102, 3, 1200, "",
	)
	cycleSecond := execution(
		t, owner, "ses_20260729T120002Z_cycle", 103, 4, 1300, cycleFirst.ID,
	)
	cycleFirst.ParentExecutionID = cycleSecond.ID
	if err := source.Replace(query.Snapshot{
		Revision: "rev_querygraph002", Owner: owner,
		Executions: []workloadtypes.Execution{cycleFirst, cycleSecond},
	}); !errors.Is(err, query.ErrInvalidSnapshot) {
		t.Fatalf("execution cycle error=%v", err)
	}
}

func TestEventsRejectsTamperedCursorAndNilContext(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-cursor")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_querycursor01", Owner: owner,
		Records: []workloadtypes.ActivityRecord{
			fileRecord(t, owner, "act_querycursor01", "exec_queryroot0001", 1, at, "/workspace/a"),
			fileRecord(t, owner, "act_querycursor02", "exec_queryroot0001", 2, at.Add(time.Second), "/workspace/b"),
		},
	})
	service := newService(t, source)
	page, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := byte('A')
	if page.NextCursor[len(page.NextCursor)-1] == replacement {
		replacement = 'B'
	}
	tampered := page.NextCursor[:len(page.NextCursor)-1] + string(replacement)
	if _, err := service.Events(context.Background(), query.EventsQuery{
		Owner: owner, Cursor: tampered,
	}); !errors.Is(err, query.ErrCursorInvalid) {
		t.Fatalf("tampered cursor error=%v", err)
	}
	if _, err := service.Events(nil, query.EventsQuery{
		Owner: owner, Limit: 1,
	}); !errors.Is(err, query.ErrInvalidQuery) {
		t.Fatalf("nil context error=%v", err)
	}
}

func TestServiceConcurrentQueriesAreDeterministic(t *testing.T) {
	owner := reusableOwner(t, "env_query", "incarnation-concurrent")
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	source := query.NewMemorySource()
	replaceSnapshot(t, source, query.Snapshot{
		Revision: "rev_queryconcurrent1", Owner: owner,
		Records: []workloadtypes.ActivityRecord{
			fileRecord(t, owner, "act_queryconcur001", "exec_queryroot0001", 1, at, "/workspace/a"),
			fileRecord(t, owner, "act_queryconcur002", "exec_queryroot0001", 2, at.Add(time.Second), "/workspace/b"),
		},
	})
	service := newService(t, source)
	want, err := service.Events(context.Background(), query.EventsQuery{Owner: owner, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 32)
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, err := service.Events(
				context.Background(), query.EventsQuery{Owner: owner, Limit: 100},
			)
			if err != nil {
				errorsFound <- err
				return
			}
			if !reflect.DeepEqual(got, want) {
				errorsFound <- errors.New("concurrent result was nondeterministic")
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

func newService(t *testing.T, source query.Source) *query.Service {
	t.Helper()
	service, err := query.NewService(query.Options{
		Source: source,
		CursorKey: []byte(
			"0123456789abcdef0123456789abcdef",
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func replaceSnapshot(t *testing.T, source *query.MemorySource, snapshot query.Snapshot) {
	t.Helper()
	if err := source.Replace(snapshot); err != nil {
		t.Fatal(err)
	}
}

func reusableOwner(
	t *testing.T,
	environmentID, incarnation string,
) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(environmentID, "lima", incarnation)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func disposableOwner(
	t *testing.T,
	sessionID, incarnation string,
) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewDisposableOwner(sessionID, "lima", incarnation)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func fileRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	id, executionID string,
	sequence uint64,
	at time.Time,
	path string,
) workloadtypes.ActivityRecord {
	t.Helper()
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: activitySession(owner),
		Actor: &workloadtypes.Actor{
			ExecutionID: executionID, PID: 42, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityFile, Operation: "write",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: path,
			PathState: "resolved", PathClass: pathClass(path), FileType: "regular",
			Device: 8, Inode: sequence,
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  "cov_queryfile0001", RedactionStatus: workloadtypes.RedactionPassed,
	}
}

func dnsRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	id, executionID string,
	sequence uint64,
	at time.Time,
) workloadtypes.ActivityRecord {
	t.Helper()
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: activitySession(owner),
		Actor: &workloadtypes.Actor{
			ExecutionID: executionID, PID: 43, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityDNS, Operation: "query",
		Subject: workloadtypes.DNSSubject{
			Kind: workloadtypes.ActivityDNS, Query: "api.example.test",
			QueryType: "A", Answers: []string{"203.0.113.10"},
			ResponseCode: "NOERROR",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  "cov_querydns00001", RedactionStatus: workloadtypes.RedactionPassed,
	}
}

func execution(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	pid uint32,
	execSequence, monotonic uint64,
	parent string,
) workloadtypes.Execution {
	t.Helper()
	id, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, ExecSequence: execSequence,
		StartedAtMonoNS: monotonic,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id,
		Owner: owner, SessionID: sessionID, ParentExecutionID: parent,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, TID: pid,
		ExecSequence: execSequence, StartedAtMonoNS: monotonic,
		StartedAt:  time.Date(2026, 7, 29, 12, 0, 0, int(monotonic), time.UTC),
		Executable: "/bin/fixture", Argv: []string{"fixture"}, Cwd: "/workspace",
		Identity: workloadtypes.GuestIdentity{UID: 1000, GID: 1000, User: "developer"},
	}
}

func coverageInterval(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	id, subsystem, state, reason string,
	retentionGap bool,
	at time.Time,
) workloadtypes.CoverageInterval {
	t.Helper()
	interval := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: id,
		Owner: owner, SessionID: activitySession(owner),
		Subsystem: subsystem, State: state, Reason: reason,
		CollectorGeneration: 1, RetentionGap: retentionGap, StartedAt: at,
	}
	if state == workloadtypes.CoveragePartial {
		interval.DroppedEventCount = 1
	}
	if err := interval.Validate(); err != nil {
		t.Fatal(err)
	}
	return interval
}

func riskFinding(
	t *testing.T,
	record workloadtypes.ActivityRecord,
	policy string,
) risk.Finding {
	t.Helper()
	engine, err := risk.NewEngine(risk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	findings, err := engine.Evaluate([]risk.Evidence{{
		Activity: record, PolicyStatus: policy,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.RuleID == "file.write-outside-workspace" {
			return finding
		}
	}
	t.Fatalf("fixture did not produce outside-workspace risk: %+v", findings)
	return risk.Finding{}
}

func activitySession(owner workloadtypes.ActivityOwner) string {
	if owner.Kind == workloadtypes.OwnerDisposableSession {
		return owner.SessionID
	}
	return "ses_20260729T120000Z_query"
}

func pathClass(path string) string {
	if len(path) >= len("/workspace") && path[:len("/workspace")] == "/workspace" {
		return "workspace"
	}
	return "external"
}
