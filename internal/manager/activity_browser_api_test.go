package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityBrowserAPICompoundNetworkFiltersPaginationCoverageAndCursorBinding(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 12, 30, 0, 0, time.UTC)
	sessionID := "ses_20260729T120000Z_browser"
	owner := activityBrowserReusableOwner(t, "env_browser", "incarnation-browser-a")
	otherOwner := activityBrowserReusableOwner(
		t, "env_browser_other", "incarnation-browser-b",
	)
	execution := activityAPIExecution(t, owner, sessionID, 201, 1, 1001, "")
	coverage := activityBrowserCoverage(
		t, owner, sessionID, "cov_browser_dns_01",
		workloadtypes.SubsystemDNS, now.Add(-time.Minute),
	)
	first := activityBrowserRecord(
		t, owner, sessionID, "act_browser_dns_01",
		workloadtypes.ActivityDNS, "query",
		workloadtypes.DNSSubject{
			Kind: workloadtypes.ActivityDNS, Query: "api.example.test.",
			QueryType: "A", Answers: []string{"203.0.113.10"},
			ResponseCode: "NOERROR", Resolver: "1.1.1.1",
		},
		coverage.ID, execution, 10, now,
	)
	second := activityBrowserRecord(
		t, owner, sessionID, "act_browser_dns_02",
		workloadtypes.ActivityDNS, "query",
		workloadtypes.DNSSubject{
			Kind: workloadtypes.ActivityDNS, Query: "api.example.test",
			QueryType: "A", Answers: []string{"203.0.113.10"},
			ResponseCode: "NOERROR", Resolver: "1.1.1.1",
		},
		coverage.ID, execution, 11, now.Add(time.Second),
	)
	distractor := activityBrowserRecord(
		t, owner, sessionID, "act_browser_dns_03",
		workloadtypes.ActivityDNS, "query",
		workloadtypes.DNSSubject{
			Kind: workloadtypes.ActivityDNS, Query: "updates.example.test",
			QueryType: "A", Answers: []string{"198.51.100.20"},
			ResponseCode: "NOERROR", Resolver: "1.1.1.1",
		},
		coverage.ID, execution, 12, now.Add(2*time.Second),
	)
	engine, err := workloadrisk.NewEngine(workloadrisk.RuleSet{
		Version: "v1",
		Rules: []workloadrisk.Rule{{
			ID: "network.external-destination", Version: "v1",
			Severity: workloadrisk.SeverityHigh,
			Title:    "External DNS destination observed",
			Explanation: "A workload execution resolved an external destination " +
				"through the observed DNS boundary.",
			NextAction: "activity.network",
			Match: func(record workloadtypes.ActivityRecord) (string, bool) {
				subject, ok := record.Subject.(workloadtypes.DNSSubject)
				return "api.example.test", ok &&
					subject.Query == "api.example.test" ||
					ok && subject.Query == "api.example.test."
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := engine.Evaluate([]workloadrisk.Evidence{
		{Activity: first, PolicyStatus: workloadrisk.PolicyNotEvaluated},
		{Activity: second, PolicyStatus: workloadrisk.PolicyNotEvaluated},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || len(findings[0].EvidenceRefs) != 2 {
		t.Fatalf("network findings=%+v", findings)
	}

	source := workloadquery.NewMemorySource()
	snapshot := workloadquery.Snapshot{
		Revision: "rev_browser_network_01", Owner: owner,
		Records: []workloadtypes.ActivityRecord{distractor, second, first},
		Executions: []workloadtypes.Execution{
			execution,
		},
		Coverage: []workloadtypes.CoverageInterval{coverage},
		Risks:    findings,
	}
	if err := source.Replace(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := source.Replace(workloadquery.Snapshot{
		Revision: "rev_browser_other_01", Owner: otherOwner,
	}); err != nil {
		t.Fatal(err)
	}
	api := activityBrowserAPI(t, now, source, owner, otherOwner)

	values := activityBrowserOwnerValues(owner)
	values.Set("run", sessionID)
	values.Set("from", now.Add(-time.Second).Format(time.RFC3339Nano))
	values.Set("to", now.Add(3*time.Second).Format(time.RFC3339Nano))
	values.Set("limit", "1")
	values.Add("kind", workloadtypes.ActivityDNS)
	values.Add("operation", "query")
	values.Add("execution", execution.ID)
	values.Add("risk", findings[0].RuleID)
	values.Set("domain", "API.EXAMPLE.TEST.")
	values.Set("ip", "::ffff:203.0.113.10")
	firstResponse := activityBrowserGET(
		t, api, "/api/v1/activity/events?"+values.Encode(),
	)
	firstPage := activityBrowserEventsPage(t, firstResponse)
	if len(firstPage.Records) != 1 ||
		firstPage.Records[0].ID != first.ID ||
		!firstPage.QueryTruncated || firstPage.NextCursor == "" ||
		len(firstPage.Coverage) != 1 ||
		firstPage.Coverage[0].ID != coverage.ID {
		t.Fatalf("first compound page=%+v", firstPage)
	}

	nextValues := activityBrowserOwnerValues(owner)
	nextValues.Set("cursor", firstPage.NextCursor)
	secondResponse := activityBrowserGET(
		t, api, "/api/v1/activity/events?"+nextValues.Encode(),
	)
	secondPage := activityBrowserEventsPage(t, secondResponse)
	if len(secondPage.Records) != 1 ||
		secondPage.Records[0].ID != second.ID ||
		secondPage.QueryTruncated || secondPage.NextCursor != "" ||
		len(secondPage.Coverage) != 1 ||
		secondPage.Coverage[0].ID != coverage.ID {
		t.Fatalf("inherited cursor page=%+v", secondPage)
	}

	changedValues := activityBrowserOwnerValues(owner)
	changedValues.Set("cursor", firstPage.NextCursor)
	changedValues.Set("domain", "updates.example.test")
	activityBrowserExpectError(
		t, api, changedValues,
		http.StatusBadRequest, "activity-cursor-filter-mismatch",
	)

	otherValues := activityBrowserOwnerValues(otherOwner)
	otherValues.Set("cursor", firstPage.NextCursor)
	activityBrowserExpectError(
		t, api, otherValues,
		http.StatusBadRequest, "activity-cursor-owner-mismatch",
	)

	snapshot.Revision = "rev_browser_network_02"
	if err := source.Replace(snapshot); err != nil {
		t.Fatal(err)
	}
	staleValues := activityBrowserOwnerValues(owner)
	staleValues.Set("cursor", firstPage.NextCursor)
	activityBrowserExpectError(
		t, api, staleValues,
		http.StatusConflict, "activity-cursor-stale",
	)

	tamperedValues := activityBrowserOwnerValues(owner)
	tamperedValues.Set("cursor", activityBrowserTamperCursor(firstPage.NextCursor))
	activityBrowserExpectError(
		t, api, tamperedValues,
		http.StatusBadRequest, "activity-cursor-invalid",
	)
}

func TestActivityBrowserAPIPathProcessTimeRiskAndExecutionCorrelation(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	sessionID := "ses_20260729T130000Z_browser"
	owner := activityBrowserReusableOwner(t, "env_browser_detail", "incarnation-browser-c")
	execution := activityAPIExecution(t, owner, sessionID, 301, 1, 2001, "")
	execution.Identity = workloadtypes.GuestIdentity{
		UID: 0, GID: 0, User: "root", Group: "root",
	}
	if err := execution.Validate(); err != nil {
		t.Fatal(err)
	}
	fileCoverage := activityBrowserCoverage(
		t, owner, sessionID, "cov_browser_file_01",
		workloadtypes.SubsystemFile, now.Add(-time.Minute),
	)
	processCoverage := activityBrowserCoverage(
		t, owner, sessionID, "cov_browser_proc_01",
		workloadtypes.SubsystemProcess, now.Add(-time.Minute),
	)
	fileRecord := activityBrowserRecord(
		t, owner, sessionID, "act_browser_file_01",
		workloadtypes.ActivityFile, "write",
		workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/etc/ssh/sshd_config",
			PathState: "resolved", PathClass: "system", FileType: "regular",
		},
		fileCoverage.ID, execution, 20, now,
	)
	processRecord := activityBrowserRecord(
		t, owner, sessionID, "act_browser_proc_01",
		workloadtypes.ActivityProcess, "exec",
		workloadtypes.ProcessSubject{
			Kind: workloadtypes.ActivityProcess, ExecutionID: execution.ID,
			Executable: execution.Executable,
			Argv:       append([]string(nil), execution.Argv...),
			Cwd:        execution.Cwd, GuestIdentity: execution.Identity,
		},
		processCoverage.ID, execution, 21, now.Add(time.Second),
	)
	outsideWindow := activityBrowserRecord(
		t, owner, sessionID, "act_browser_file_02",
		workloadtypes.ActivityFile, "write",
		workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/etc/ssh/other_config",
			PathState: "resolved", PathClass: "system", FileType: "regular",
		},
		fileCoverage.ID, execution, 22, now.Add(10*time.Minute),
	)
	engine, err := workloadrisk.NewEngine(workloadrisk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	findings, err := engine.Evaluate([]workloadrisk.Evidence{
		{Activity: fileRecord, PolicyStatus: workloadrisk.PolicyNotEvaluated},
		{Activity: processRecord, PolicyStatus: workloadrisk.PolicyNotEvaluated},
		{Activity: outsideWindow, PolicyStatus: workloadrisk.PolicyNotEvaluated},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := workloadquery.NewMemorySource()
	if err := source.Replace(workloadquery.Snapshot{
		Revision: "rev_browser_detail_01", Owner: owner,
		Records:    []workloadtypes.ActivityRecord{outsideWindow, processRecord, fileRecord},
		Executions: []workloadtypes.Execution{execution},
		Coverage: []workloadtypes.CoverageInterval{
			fileCoverage, processCoverage,
		},
		Risks: findings,
	}); err != nil {
		t.Fatal(err)
	}
	api := activityBrowserAPI(t, now, source, owner)

	fileValues := activityBrowserOwnerValues(owner)
	fileValues.Set("run", sessionID)
	fileValues.Set("from", now.Add(-time.Second).Format(time.RFC3339Nano))
	fileValues.Set("to", now.Add(time.Minute).Format(time.RFC3339Nano))
	fileValues.Add("kind", workloadtypes.ActivityFile)
	fileValues.Add("operation", "write")
	fileValues.Add("execution", execution.ID)
	fileValues.Add("risk", "file.write-outside-workspace")
	fileValues.Set("path", "sshd_config")
	filePage := activityBrowserEventsPage(t, activityBrowserGET(
		t, api, "/api/v1/activity/events?"+fileValues.Encode(),
	))
	if len(filePage.Records) != 1 ||
		filePage.Records[0].ID != fileRecord.ID ||
		len(filePage.Coverage) != 1 ||
		filePage.Coverage[0].ID != fileCoverage.ID {
		t.Fatalf("file detail page=%+v", filePage)
	}

	processValues := activityBrowserOwnerValues(owner)
	processValues.Set("run", sessionID)
	processValues.Set("from", now.Add(-time.Second).Format(time.RFC3339Nano))
	processValues.Set("to", now.Add(time.Minute).Format(time.RFC3339Nano))
	processValues.Add("kind", workloadtypes.ActivityProcess)
	processValues.Add("operation", "exec")
	processValues.Add("execution", execution.ID)
	processValues.Add("risk", "process.root-execution")
	processPage := activityBrowserEventsPage(t, activityBrowserGET(
		t, api, "/api/v1/activity/events?"+processValues.Encode(),
	))
	if len(processPage.Records) != 1 ||
		processPage.Records[0].ID != processRecord.ID ||
		len(processPage.Coverage) != 1 ||
		processPage.Coverage[0].ID != processCoverage.ID {
		t.Fatalf("process detail page=%+v", processPage)
	}

	executionValues := activityBrowserOwnerValues(owner)
	executionValues.Set("run", sessionID)
	executionValues.Set("id", execution.ID)
	executionResponse := activityBrowserGET(
		t, api, "/api/v1/activity/executions?"+executionValues.Encode(),
	)
	var executionEnvelope struct {
		Data ActivityExecutionsResult `json:"data"`
	}
	if err := json.Unmarshal(
		executionResponse.Body.Bytes(), &executionEnvelope,
	); err != nil {
		t.Fatal(err)
	}
	if len(executionEnvelope.Data.Roots) != 1 ||
		executionEnvelope.Data.Roots[0].Execution.ID != execution.ID ||
		executionEnvelope.Data.Roots[0].ActivityCounts[workloadtypes.ActivityFile] != 2 ||
		executionEnvelope.Data.Roots[0].ActivityCounts[workloadtypes.ActivityProcess] != 1 ||
		len(executionEnvelope.Data.Coverage) != 1 ||
		executionEnvelope.Data.Coverage[0].ID != processCoverage.ID {
		t.Fatalf("execution correlation=%+v", executionEnvelope.Data)
	}
}

func activityBrowserReusableOwner(
	t *testing.T,
	environmentID string,
	incarnationID string,
) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		environmentID, "lima", incarnationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func activityBrowserCoverage(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	id string,
	subsystem string,
	startedAt time.Time,
) workloadtypes.CoverageInterval {
	t.Helper()
	interval := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: id,
		Owner: owner, SessionID: sessionID,
		Subsystem: subsystem, State: workloadtypes.CoverageAvailable,
		Reason: "collector-active", CollectorGeneration: 1,
		StartedAt: startedAt,
	}
	if err := interval.Validate(); err != nil {
		t.Fatal(err)
	}
	return interval
}

func activityBrowserRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	id string,
	kind string,
	operation string,
	subject any,
	coverageID string,
	execution workloadtypes.Execution,
	sequence uint64,
	at time.Time,
) workloadtypes.ActivityRecord {
	t.Helper()
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: sessionID,
		Actor: &workloadtypes.Actor{
			ExecutionID: execution.ID, PID: execution.PID,
			UID: execution.Identity.UID, GID: execution.Identity.GID,
			User: execution.Identity.User, Group: execution.Identity.Group,
		},
		Kind: kind, Operation: operation, Subject: subject,
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  coverageID, RedactionStatus: workloadtypes.RedactionPassed,
	}
	if err := record.ValidatePersistable(); err != nil {
		t.Fatal(err)
	}
	return record
}

func activityBrowserAPI(
	t *testing.T,
	now time.Time,
	source *workloadquery.MemorySource,
	owners ...workloadtypes.ActivityOwner,
) API {
	t.Helper()
	bySelector := make(map[string]workloadtypes.ActivityOwner, len(owners))
	for _, owner := range owners {
		bySelector[owner.EnvironmentID+"\x00"+owner.BackendIncarnationID] = owner
	}
	query, err := workloadquery.NewService(workloadquery.Options{
		Source: source,
		CursorKey: []byte(
			"0123456789abcdef0123456789abcdef",
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	return API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: ActivityService{
			OwnerResolver: ActivityOwnerResolverFunc(func(
				_ context.Context,
				selector ActivityOwnerSelector,
			) (workloadtypes.ActivityOwner, error) {
				key := selector.EnvironmentID + "\x00" +
					selector.BackendIncarnationID
				owner, exists := bySelector[key]
				if !exists {
					return workloadtypes.ActivityOwner{}, ErrActivityOwnerNotFound
				}
				return owner, nil
			}),
			Query: query,
		},
	}
}

func activityBrowserOwnerValues(owner workloadtypes.ActivityOwner) url.Values {
	return url.Values{
		"environment": {owner.EnvironmentID},
		"incarnation": {owner.BackendIncarnationID},
	}
}

func activityBrowserGET(
	t *testing.T,
	api API,
	path string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := newAPIRequest(http.MethodGet, path)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
	}
	return response
}

func activityBrowserEventsPage(
	t *testing.T,
	response *httptest.ResponseRecorder,
) ActivityEventsPage {
	t.Helper()
	var envelope struct {
		Data ActivityEventsPage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func activityBrowserExpectError(
	t *testing.T,
	api API,
	values url.Values,
	status int,
	code string,
) {
	t.Helper()
	request := newAPIRequest(
		http.MethodGet, "/api/v1/activity/events?"+values.Encode(),
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf(
			"status=%d want=%d body=%s",
			response.Code, status, response.Body.String(),
		)
	}
	var envelope APIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.ErrorDetails) != 1 ||
		envelope.ErrorDetails[0].Code != code {
		t.Fatalf("error=%+v want code=%q", envelope, code)
	}
}

func activityBrowserTamperCursor(cursor string) string {
	if cursor[len(cursor)-1] == 'A' {
		return cursor[:len(cursor)-1] + "B"
	}
	return cursor[:len(cursor)-1] + "A"
}
