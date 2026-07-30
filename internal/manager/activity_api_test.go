package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityEventsRouteResolvesExactOwnerAndPreservesFiltersPaginationAndGap(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	var resolved ActivityOwnerSelector
	var observed ActivityEventsQuery
	provider := activityAPIProvider{
		resolveOwner: func(_ context.Context, selector ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
			resolved = selector
			return owner, nil
		},
		events: func(_ context.Context, query ActivityEventsQuery) (ActivityEventsPage, error) {
			observed = query
			return ActivityEventsPage{
				Records: []workloadtypes.ActivityRecord{
					activityAPIRecord(t, owner, "act_activityapi01", "file", "write", 10, now),
					activityAPIRecord(t, owner, "act_activityapi02", "dns", "query", 11, now.Add(time.Second)),
				},
				NextCursor: "cur_activitypage02",
				Coverage: []workloadtypes.CoverageInterval{activityAPICoverage(
					t, owner, workloadtypes.SubsystemFile, workloadtypes.CoveragePartial,
					"retention-pruned", true, now,
				)},
				QueryTruncated: true,
			}, nil
		},
	}
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now }, ActivityProvider: provider,
	}
	path := "/api/v1/activity/events?" +
		"environment=env_fixture&incarnation=incarnation-a&" +
		"run=ses_20260729T120000Z_activity&" +
		"from=2026-07-29T11%3A59%3A00Z&to=2026-07-29T12%3A01%3A00Z&" +
		"limit=2&kind=file&kind=dns&operation=write&operation=query&" +
		"execution=exec_activityapi01&path=%2Fworkspace%2Fresult.txt&" +
		"domain=api.example.test&ip=203.0.113.10"
	request := newAPIRequest(http.MethodGet, path)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if resolved.EnvironmentID != "env_fixture" ||
		resolved.BackendIncarnationID != "incarnation-a" ||
		resolved.SessionID != "" {
		t.Fatalf("selector=%+v", resolved)
	}
	if !observed.Owner.Equal(owner) || observed.Limit != 2 ||
		observed.SessionID != "ses_20260729T120000Z_activity" ||
		!reflect.DeepEqual(observed.Kinds, []string{"dns", "file"}) ||
		!reflect.DeepEqual(observed.Operations, []string{"query", "write"}) ||
		!reflect.DeepEqual(observed.Executions, []string{"exec_activityapi01"}) ||
		observed.Path != "/workspace/result.txt" ||
		observed.Domain != "api.example.test" || observed.IP != "203.0.113.10" ||
		observed.From.IsZero() || observed.To.IsZero() {
		t.Fatalf("query=%+v", observed)
	}

	var envelope struct {
		Version  string             `json:"version"`
		Resource string             `json:"resource"`
		Data     ActivityEventsPage `json:"data"`
		Errors   []string           `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != APIVersion || envelope.Resource != "activity/events" ||
		len(envelope.Data.Records) != 2 ||
		envelope.Data.NextCursor != "cur_activitypage02" ||
		!envelope.Data.QueryTruncated ||
		len(envelope.Data.Coverage) != 1 ||
		envelope.Data.Coverage[0].Reason != "retention-pruned" {
		t.Fatalf("activity envelope=%+v", envelope)
	}
}

func TestActivityCursorCannotCrossExactOwner(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-b")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: activityAPIProvider{
			resolveOwner: func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
				return owner, nil
			},
			events: func(context.Context, ActivityEventsQuery) (ActivityEventsPage, error) {
				called = true
				return ActivityEventsPage{}, ErrActivityCursorOwnerMismatch
			},
		},
	}
	request := newAPIRequest(
		http.MethodGet,
		"/api/v1/activity/events?environment=env_fixture&incarnation=incarnation-b&cursor=cur_otherowner",
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if !called || response.Code != http.StatusBadRequest {
		t.Fatalf("called=%v status=%d body=%s", called, response.Code, response.Body.String())
	}
	var envelope APIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.ErrorDetails) != 1 ||
		envelope.ErrorDetails[0].Code != "activity-cursor-owner-mismatch" ||
		envelope.ErrorDetails[0].Recovery == "" {
		t.Fatalf("cursor mismatch guidance=%+v", envelope)
	}
}

func TestActivityExecutionsRouteReturnsScopedTreeWithoutFlatteningParentage(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewDisposableOwner(
		"ses_20260729T120000Z_tree", "lima", "incarnation-disposable",
	)
	if err != nil {
		t.Fatal(err)
	}
	root := activityAPIExecution(t, owner, owner.SessionID, 100, 1, 1000, "")
	child := activityAPIExecution(t, owner, owner.SessionID, 101, 2, 1100, root.ID)
	var observed ActivityExecutionsQuery
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: activityAPIProvider{
			resolveOwner: func(_ context.Context, selector ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
				if selector.SessionID != owner.SessionID {
					t.Fatalf("selector=%+v", selector)
				}
				return owner, nil
			},
			executions: func(_ context.Context, query ActivityExecutionsQuery) (ActivityExecutionsResult, error) {
				observed = query
				return ActivityExecutionsResult{Roots: []ActivityExecutionNode{{
					Execution: root, ActivityCounts: map[string]uint64{"file": 2},
					Children: []ActivityExecutionNode{{
						Execution: child, ActivityCounts: map[string]uint64{"dns": 1},
					}},
				}}}, nil
			},
		},
	}
	request := newAPIRequest(
		http.MethodGet,
		"/api/v1/activity/executions?session="+owner.SessionID+
			"&run="+owner.SessionID+"&root=true",
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !observed.Owner.Equal(owner) ||
		observed.SessionID != owner.SessionID ||
		!observed.RootsOnly || observed.ID != "" {
		t.Fatalf("execution query=%+v", observed)
	}
	var envelope struct {
		Data ActivityExecutionsResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Roots) != 1 ||
		len(envelope.Data.Roots[0].Children) != 1 ||
		envelope.Data.Roots[0].Children[0].Execution.ParentExecutionID != root.ID {
		t.Fatalf("execution tree was flattened or corrupted: %+v", envelope.Data)
	}
}

func TestActivitySummaryRouteReturnsExactOwnerAggregate(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-summary")
	if err != nil {
		t.Fatal(err)
	}
	var observed ActivitySummaryQuery
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: activityAPIProvider{
			resolveOwner: func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
				return owner, nil
			},
			summary: func(_ context.Context, query ActivitySummaryQuery) (ActivitySummaryResult, error) {
				observed = query
				return ActivitySummaryResult{
					Owner: owner, Counts: map[string]uint64{"file": 3},
					CurrentCoverage: []workloadtypes.CoverageInterval{},
					HighestRisks:    []workloadrisk.Finding{},
					Reasons:         []string{},
				}, nil
			},
		},
	}
	request := newAPIRequest(
		http.MethodGet,
		"/api/v1/activity/summary?environment=env_fixture&incarnation=incarnation-summary&"+
			"run=ses_20260729T120000Z_activity&"+
			"from=2026-07-29T11%3A00%3A00Z&to=2026-07-29T13%3A00%3A00Z",
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !observed.Owner.Equal(owner) ||
		observed.SessionID != "ses_20260729T120000Z_activity" ||
		observed.From.IsZero() || observed.To.IsZero() {
		t.Fatalf("summary query=%+v", observed)
	}
	var envelope struct {
		Version  string                `json:"version"`
		Resource string                `json:"resource"`
		Data     ActivitySummaryResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != APIVersion || envelope.Resource != "activity/summary" ||
		envelope.Data.Counts["file"] != 3 || !envelope.Data.Owner.Equal(owner) {
		t.Fatalf("summary envelope=%+v", envelope)
	}
}

func TestActivityCoverageAndRiskRoutesPreserveNormalizedFilters(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewDisposableOwner(
		"ses_20260729T120000Z_filters", "lima", "incarnation-filters",
	)
	if err != nil {
		t.Fatal(err)
	}
	var coverageQuery ActivityCoverageQuery
	var risksQuery ActivityRisksQuery
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: activityAPIProvider{
			resolveOwner: func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
				return owner, nil
			},
			coverage: func(_ context.Context, query ActivityCoverageQuery) (ActivityCoverageResult, error) {
				coverageQuery = query
				return ActivityCoverageResult{
					Intervals: []workloadtypes.CoverageInterval{},
					Current:   []workloadtypes.CoverageInterval{},
				}, nil
			},
			risks: func(_ context.Context, query ActivityRisksQuery) (ActivityRisksResult, error) {
				risksQuery = query
				return ActivityRisksResult{Findings: []workloadrisk.Finding{}}, nil
			},
		},
	}
	for _, path := range []string{
		"/api/v1/activity/coverage?session=" + owner.SessionID +
			"&run=" + owner.SessionID +
			"&subsystem=file&subsystem=dns&from=2026-07-29T11%3A00%3A00Z",
		"/api/v1/activity/risks?session=" + owner.SessionID +
			"&run=" + owner.SessionID +
			"&severity=high&severity=critical&rule=file.write-outside-workspace&" +
			"execution=exec_activityapi01&to=2026-07-29T13%3A00%3A00Z",
	} {
		request := newAPIRequest(http.MethodGet, path)
		request.Header.Set("Authorization", "Bearer ui_token")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if !coverageQuery.Owner.Equal(owner) ||
		coverageQuery.SessionID != owner.SessionID ||
		!reflect.DeepEqual(coverageQuery.Subsystems, []string{"dns", "file"}) ||
		coverageQuery.From.IsZero() {
		t.Fatalf("coverage query=%+v", coverageQuery)
	}
	if !risksQuery.Owner.Equal(owner) ||
		risksQuery.SessionID != owner.SessionID ||
		!reflect.DeepEqual(risksQuery.Severities, []string{"critical", "high"}) ||
		!reflect.DeepEqual(risksQuery.Rules, []string{"file.write-outside-workspace"}) ||
		!reflect.DeepEqual(risksQuery.Executions, []string{"exec_activityapi01"}) ||
		risksQuery.To.IsZero() {
		t.Fatalf("risks query=%+v", risksQuery)
	}
}

func TestActivityRouteMapsTypedQueryFailuresToActionableCodes(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-errors")
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"cursor-invalid", ErrActivityCursorInvalid, http.StatusBadRequest, "activity-cursor-invalid"},
		{"cursor-owner", ErrActivityCursorOwnerMismatch, http.StatusBadRequest, "activity-cursor-owner-mismatch"},
		{"cursor-filter", ErrActivityCursorFilterMismatch, http.StatusBadRequest, "activity-cursor-filter-mismatch"},
		{"cursor-stale", ErrActivityCursorStale, http.StatusConflict, "activity-cursor-stale"},
		{"owner-missing", ErrActivityOwnerNotFound, http.StatusNotFound, "activity-owner-not-found"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			api := API{
				Token: "ui_token", ExpiresAt: now.Add(time.Hour),
				Now: func() time.Time { return now },
				ActivityProvider: activityAPIProvider{
					resolveOwner: func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
						return owner, nil
					},
					events: func(context.Context, ActivityEventsQuery) (ActivityEventsPage, error) {
						return ActivityEventsPage{}, testCase.err
					},
				},
			}
			request := newAPIRequest(
				http.MethodGet,
				"/api/v1/activity/events?environment=env_fixture&incarnation=incarnation-errors",
			)
			request.Header.Set("Authorization", "Bearer ui_token")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != testCase.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope APIResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.ErrorDetails) != 1 ||
				envelope.ErrorDetails[0].Code != testCase.code ||
				envelope.ErrorDetails[0].Recovery == "" {
				t.Fatalf("error envelope=%+v", envelope)
			}
		})
	}
}

func TestActivityRouteRejectsCrossOwnerProviderResponseWithoutLeakingIt(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture", "lima", "incarnation-response",
	)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := workloadtypes.NewReusableOwner(
		"env_foreign", "lima", "incarnation-foreign",
	)
	if err != nil {
		t.Fatal(err)
	}
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: activityAPIProvider{
			resolveOwner: func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
				return owner, nil
			},
			events: func(context.Context, ActivityEventsQuery) (ActivityEventsPage, error) {
				return ActivityEventsPage{Records: []workloadtypes.ActivityRecord{
					activityAPIRecord(
						t, foreign, "act_activityforeign", "file", "write", 1, now,
					),
				}, Coverage: []workloadtypes.CoverageInterval{}}, nil
			},
		},
	}
	request := newAPIRequest(
		http.MethodGet,
		"/api/v1/activity/events?environment=env_fixture&incarnation=incarnation-response",
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/workspace/result.txt") ||
		strings.Contains(response.Body.String(), "env_foreign") {
		t.Fatalf("cross-owner response leaked: %s", response.Body.String())
	}
	var envelope APIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.ErrorDetails) != 1 ||
		envelope.ErrorDetails[0].Code != "activity-response-invalid" {
		t.Fatalf("response=%+v", envelope)
	}
}

func TestActivityRouteRejectsCrossSessionProviderResponse(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture", "lima", "incarnation-session-response",
	)
	if err != nil {
		t.Fatal(err)
	}
	record := activityAPIRecord(
		t, owner, "act_activitysessionx", "file", "write", 1, now,
	)
	record.SessionID = "ses_foreign"
	api := API{
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
		ActivityProvider: activityAPIProvider{
			resolveOwner: func(
				context.Context,
				ActivityOwnerSelector,
			) (workloadtypes.ActivityOwner, error) {
				return owner, nil
			},
			events: func(
				context.Context,
				ActivityEventsQuery,
			) (ActivityEventsPage, error) {
				return ActivityEventsPage{
					Records:  []workloadtypes.ActivityRecord{record},
					Coverage: []workloadtypes.CoverageInterval{},
				}, nil
			},
		},
	}
	request := newAPIRequest(
		http.MethodGet,
		"/api/v1/activity/events?environment=env_fixture&"+
			"incarnation=incarnation-session-response&run=ses_selected",
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "ses_foreign") ||
		strings.Contains(response.Body.String(), "/workspace/result.txt") {
		t.Fatalf("cross-session response leaked: %s", response.Body.String())
	}
}

func TestActivityRoutesRejectAmbiguousInvalidAndUnknownQueriesBeforeProvider(t *testing.T) {
	called := false
	provider := activityAPIProvider{
		resolveOwner: func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
			called = true
			return workloadtypes.ActivityOwner{}, errors.New("must not resolve")
		},
	}
	api := API{
		Token: "ui_token", ExpiresAt: time.Now().Add(time.Hour),
		ActivityProvider: provider,
	}
	for _, path := range []string{
		"/api/v1/activity/events",
		"/api/v1/activity/events?environment=env_fixture",
		"/api/v1/activity/events?incarnation=incarnation-a",
		"/api/v1/activity/events?environment=env_fixture&incarnation=incarnation-a&session=ses_conflict",
		"/api/v1/activity/events?session=not-a-session",
		"/api/v1/activity/events?session=ses_valid&run=not-a-session",
		"/api/v1/activity/events?session=ses_valid&run=ses_first&run=ses_second",
		"/api/v1/activity/events?session=ses_valid&limit=0",
		"/api/v1/activity/events?session=ses_valid&limit=501",
		"/api/v1/activity/events?session=ses_valid&from=not-time",
		"/api/v1/activity/events?session=ses_valid&unknown=value",
		"/api/v1/activity/events?session=ses_valid&kind=file&kind=file",
		"/api/v1/activity/coverage?session=ses_valid&subsystem=unknown",
		"/api/v1/activity/risks?session=ses_valid&severity=urgent",
		"/api/v1/activity/executions?session=ses_valid&root=maybe",
		"/api/v1/activity/executions?session=ses_valid&id=exec_activityapi01&root=true",
	} {
		request := newAPIRequest(http.MethodGet, path)
		request.Header.Set("Authorization", "Bearer ui_token")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if called {
		t.Fatal("invalid activity query reached owner resolution")
	}
}

func TestActivityRouteInventoryIsPrivateReadOnlyAndComplete(t *testing.T) {
	for _, resource := range []string{
		"activity/summary", "activity/events", "activity/executions",
		"activity/coverage", "activity/risks",
	} {
		spec, ok := RecognizeManagerRoute(http.MethodGet, "/api/v1/"+resource)
		if !ok {
			t.Fatalf("%s is not registered", resource)
		}
		if spec.Resource != resource || !spec.NoStore || !spec.NoBodyLog ||
			spec.Sensitive || spec.MaxRequestBodyBytes != 0 {
			t.Fatalf("%s route metadata=%+v", resource, spec)
		}
		if _, ok := RecognizeManagerRoute(http.MethodPost, "/api/v1/"+resource); ok {
			t.Fatalf("%s unexpectedly authorizes POST", resource)
		}
	}
}

type activityAPIProvider struct {
	resolveOwner func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error)
	summary      func(context.Context, ActivitySummaryQuery) (ActivitySummaryResult, error)
	events       func(context.Context, ActivityEventsQuery) (ActivityEventsPage, error)
	executions   func(context.Context, ActivityExecutionsQuery) (ActivityExecutionsResult, error)
	coverage     func(context.Context, ActivityCoverageQuery) (ActivityCoverageResult, error)
	risks        func(context.Context, ActivityRisksQuery) (ActivityRisksResult, error)
}

func (provider activityAPIProvider) ResolveActivityOwner(
	ctx context.Context,
	selector ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	if provider.resolveOwner == nil {
		return workloadtypes.ActivityOwner{}, errors.New("owner resolver was not configured")
	}
	return provider.resolveOwner(ctx, selector)
}

func (provider activityAPIProvider) ActivitySummary(
	ctx context.Context,
	query ActivitySummaryQuery,
) (ActivitySummaryResult, error) {
	if provider.summary == nil {
		return ActivitySummaryResult{}, errors.New("summary provider was not configured")
	}
	return provider.summary(ctx, query)
}

func (provider activityAPIProvider) ActivityEvents(
	ctx context.Context,
	query ActivityEventsQuery,
) (ActivityEventsPage, error) {
	if provider.events == nil {
		return ActivityEventsPage{}, errors.New("event provider was not configured")
	}
	return provider.events(ctx, query)
}

func (provider activityAPIProvider) ActivityExecutions(
	ctx context.Context,
	query ActivityExecutionsQuery,
) (ActivityExecutionsResult, error) {
	if provider.executions == nil {
		return ActivityExecutionsResult{}, errors.New("execution provider was not configured")
	}
	return provider.executions(ctx, query)
}

func (provider activityAPIProvider) ActivityCoverage(
	ctx context.Context,
	query ActivityCoverageQuery,
) (ActivityCoverageResult, error) {
	if provider.coverage == nil {
		return ActivityCoverageResult{}, errors.New("coverage provider was not configured")
	}
	return provider.coverage(ctx, query)
}

func (provider activityAPIProvider) ActivityRisks(
	ctx context.Context,
	query ActivityRisksQuery,
) (ActivityRisksResult, error) {
	if provider.risks == nil {
		return ActivityRisksResult{}, errors.New("risk provider was not configured")
	}
	return provider.risks(ctx, query)
}

func activityAPIRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	id, kind, operation string,
	sequence uint64,
	at time.Time,
) workloadtypes.ActivityRecord {
	t.Helper()
	var subject any
	switch kind {
	case workloadtypes.ActivityFile:
		subject = workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/workspace/result.txt",
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
		}
	case workloadtypes.ActivityDNS:
		subject = workloadtypes.DNSSubject{
			Kind: workloadtypes.ActivityDNS, Query: "api.example.test",
			QueryType: "A", Answers: []string{"203.0.113.10"}, ResponseCode: "NOERROR",
		}
	default:
		t.Fatalf("unsupported fixture kind %q", kind)
	}
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: "ses_20260729T120000Z_activity",
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_activityapi01", PID: 42, UID: 1000, GID: 1000,
		},
		Kind: kind, Operation: operation, Subject: subject,
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  "cov_activityapi01", RedactionStatus: workloadtypes.RedactionPassed,
	}
}

func activityAPICoverage(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	subsystem, state, reason string,
	retentionGap bool,
	at time.Time,
) workloadtypes.CoverageInterval {
	t.Helper()
	interval := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_activitygap01",
		Owner: owner, SessionID: "ses_20260729T120000Z_activity",
		Subsystem: subsystem, State: state, Reason: reason,
		CollectorGeneration: 1, RetentionGap: retentionGap, StartedAt: at,
	}
	if err := interval.Validate(); err != nil {
		t.Fatal(err)
	}
	return interval
}

func activityAPIExecution(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	pid uint32,
	execSequence, mono uint64,
	parent string,
) workloadtypes.Execution {
	t.Helper()
	id, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, ExecSequence: execSequence,
		StartedAtMonoNS: mono,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id,
		Owner: owner, SessionID: sessionID, ParentExecutionID: parent,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, TID: pid,
		ExecSequence: execSequence, StartedAtMonoNS: mono,
		StartedAt:  time.Date(2026, 7, 29, 12, 0, 0, int(mono), time.UTC),
		Executable: "/bin/fixture", Argv: []string{"fixture"}, Cwd: "/workspace",
		Identity: workloadtypes.GuestIdentity{UID: 1000, GID: 1000, User: "developer"},
	}
}
