package tui

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityViewQueriesManagerTabsAndShowsCorrelatedRiskEvidence(t *testing.T) {
	fixture := newActivityModelFixture(t)
	provider := &activityModelProvider{
		owner: fixture.owner,
		summary: manager.ActivitySummaryResult{
			Owner:           fixture.owner,
			Counts:          map[string]uint64{workloadtypes.ActivityFile: 2},
			CurrentCoverage: []workloadtypes.CoverageInterval{fixture.coverage},
			HighestRisks:    []workloadrisk.Finding{fixture.risk},
			Reasons:         []string{},
		},
		events: manager.ActivityEventsPage{
			Records:  []workloadtypes.ActivityRecord{fixture.file, fixture.foreignSessionFile},
			Coverage: []workloadtypes.CoverageInterval{fixture.coverage},
		},
		executions: manager.ActivityExecutionsResult{
			Roots:    []manager.ActivityExecutionNode{fixture.root},
			Coverage: []workloadtypes.CoverageInterval{fixture.coverage},
		},
		coverage: manager.ActivityCoverageResult{
			Intervals: []workloadtypes.CoverageInterval{fixture.coverage},
			Current:   []workloadtypes.CoverageInterval{fixture.coverage},
		},
		risks: manager.ActivityRisksResult{Findings: []workloadrisk.Finding{fixture.risk}},
	}
	model := NewModel(ModelOptions{
		State: fixture.state, Width: 112, Height: 30, NoColor: true,
		ActivityProvider: provider,
	})

	var command tea.Cmd
	model, command = updateModelWithCommand(t, model, key("2"))
	if model.ActiveView() != ViewActivity || command == nil || !model.ActivityLoading() {
		t.Fatalf(
			"activity query did not start: view=%s loading=%t cmd=%v",
			model.ActiveView(),
			model.ActivityLoading(),
			command,
		)
	}
	model = updateModel(t, model, command())
	if model.ActivityLoading() || model.ActivityError() != "" {
		t.Fatalf("activity query did not complete: loading=%t error=%q", model.ActivityLoading(), model.ActivityError())
	}
	if provider.callCount("summary") != 1 ||
		provider.callCount("events") != 1 ||
		provider.callCount("executions") != 1 ||
		provider.callCount("coverage") != 1 ||
		provider.callCount("risks") != 1 {
		t.Fatalf("manager queries=%v", provider.callsSnapshot())
	}
	if sessions := provider.querySessions(); !reflect.DeepEqual(
		sessions,
		[]string{
			"ses_alpha", "ses_alpha", "ses_alpha", "ses_alpha", "ses_alpha",
		},
	) {
		t.Fatalf("activity queries were not session-scoped: %v", sessions)
	}
	rendered := model.View().Content
	for _, expected := range []string{
		"Activity / [All]",
		"/workspace/alpha.txt",
		"gofmt",
		"coverage file Partial",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("activity render missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "/workspace/beta.txt") {
		t.Fatalf("selected-session view mixed another session:\n%s", rendered)
	}

	model, command = updateModelWithCommand(t, model, specialKey(tea.KeyLeft))
	if model.ActivityTab() != ActivityTabRisks || command == nil {
		t.Fatalf("left did not wrap to Risks: tab=%s cmd=%v", model.ActivityTab(), command)
	}
	model = updateModel(t, model, command())
	eventQuery := provider.lastEventsQuery()
	if len(eventQuery.Risks) != 1 || eventQuery.Risks[0] != fixture.risk.ID {
		t.Fatalf("risk evidence was not requested through Manager: %+v", eventQuery)
	}
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	rendered = model.View().Content
	for _, expected := range []string{
		"file.write-outside-workspace/v1",
		"allowed / allowed-observed",
		fixture.file.ID,
		"/workspace/alpha.txt",
		"activity.files",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("risk detail missing %q:\n%s", expected, rendered)
		}
	}
}

func TestActivityViewUsesTabQueryAndClientLocalFilterWithoutLeakingStaleRows(t *testing.T) {
	fixture := newActivityModelFixture(t)
	provider := &activityModelProvider{
		owner: fixture.owner,
		summary: manager.ActivitySummaryResult{
			Owner: fixture.owner, Counts: map[string]uint64{},
			CurrentCoverage: []workloadtypes.CoverageInterval{fixture.coverage},
			HighestRisks:    []workloadrisk.Finding{}, Reasons: []string{},
		},
		events: manager.ActivityEventsPage{
			Records:  []workloadtypes.ActivityRecord{fixture.file},
			Coverage: []workloadtypes.CoverageInterval{fixture.coverage},
		},
		executions: manager.ActivityExecutionsResult{
			Roots:    []manager.ActivityExecutionNode{fixture.root},
			Coverage: []workloadtypes.CoverageInterval{fixture.coverage},
		},
		coverage: manager.ActivityCoverageResult{
			Intervals: []workloadtypes.CoverageInterval{fixture.coverage},
			Current:   []workloadtypes.CoverageInterval{fixture.coverage},
		},
		risks: manager.ActivityRisksResult{Findings: []workloadrisk.Finding{}},
	}
	model := NewModel(ModelOptions{
		State: fixture.state, Width: 100, Height: 28, NoColor: true,
		ActivityProvider: provider,
	})
	model, first := updateModelWithCommand(t, model, key("2"))
	model, second := updateModelWithCommand(t, model, specialKey(tea.KeyRight))
	if model.ActivityTab() != ActivityTabCommands || first == nil || second == nil {
		t.Fatalf("command tab query did not start: tab=%s", model.ActivityTab())
	}
	model = updateModel(t, model, second())
	query := provider.lastEventsQuery()
	if len(query.Kinds) != 1 || query.Kinds[0] != workloadtypes.ActivityProcess {
		t.Fatalf("Commands tab query kinds=%v", query.Kinds)
	}
	// An older All response must not replace the newer Commands result.
	model = updateModel(t, model, first())
	if model.ActivityTab() != ActivityTabCommands || model.ActivityLoading() {
		t.Fatalf("stale query response changed current state: tab=%s loading=%t", model.ActivityTab(), model.ActivityLoading())
	}

	model = updateModel(t, model, key("/"))
	if model.Focus() != FocusFilter {
		t.Fatalf("filter focus=%s", model.Focus())
	}
	model = updateModel(t, model, key("definitely-no-match"))
	model = updateModel(t, model, specialKey(tea.KeyEnter))
	if model.Focus() != FocusPrimary || model.ActivityFilter() != "definitely-no-match" {
		t.Fatalf("client filter was not retained: focus=%s filter=%q", model.Focus(), model.ActivityFilter())
	}
	if !strings.Contains(model.View().Content, "No matching activity") {
		t.Fatalf("client filter did not affect rows:\n%s", model.View().Content)
	}
	if provider.callCount("events") != 2 {
		t.Fatalf("client-local free-text filter unexpectedly queried Manager: %v", provider.callsSnapshot())
	}
}

func TestActivityViewExplainsUnavailableOwnerAndReducedCoverage(t *testing.T) {
	state := v2ModelState()
	state.Coverage = nil
	state.Activity.Recent = nil
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 28, NoColor: true,
		ActivityProvider: &activityModelProvider{},
	})
	model, command := updateModelWithCommand(t, model, key("2"))
	if command != nil || model.ActivityLoading() {
		t.Fatal("activity query started without an exact owner")
	}
	rendered := model.View().Content
	for _, expected := range []string{
		"Activity unavailable",
		"this workload",
		"hideout doctor --feature activity",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("owner recovery missing %q:\n%s", expected, rendered)
		}
	}
}

type activityModelProvider struct {
	mu sync.Mutex

	owner      workloadtypes.ActivityOwner
	summary    manager.ActivitySummaryResult
	events     manager.ActivityEventsPage
	executions manager.ActivityExecutionsResult
	coverage   manager.ActivityCoverageResult
	risks      manager.ActivityRisksResult

	calls           []string
	eventsQueries   []manager.ActivityEventsQuery
	summaryQuery    manager.ActivitySummaryQuery
	executionsQuery manager.ActivityExecutionsQuery
	coverageQuery   manager.ActivityCoverageQuery
	risksQuery      manager.ActivityRisksQuery
}

func (provider *activityModelProvider) ResolveActivityOwner(
	_ context.Context,
	selector manager.ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	provider.record("resolve")
	if selector.SessionID != "" && provider.owner.Kind == workloadtypes.OwnerDisposableSession {
		return provider.owner, nil
	}
	return provider.owner, nil
}

func (provider *activityModelProvider) ActivitySummary(
	_ context.Context,
	query manager.ActivitySummaryQuery,
) (manager.ActivitySummaryResult, error) {
	provider.mu.Lock()
	provider.calls = append(provider.calls, "summary")
	provider.summaryQuery = query
	provider.mu.Unlock()
	return provider.summary, nil
}

func (provider *activityModelProvider) ActivityEvents(
	_ context.Context,
	query manager.ActivityEventsQuery,
) (manager.ActivityEventsPage, error) {
	provider.mu.Lock()
	provider.calls = append(provider.calls, "events")
	provider.eventsQueries = append(provider.eventsQueries, query)
	provider.mu.Unlock()
	return provider.events, nil
}

func (provider *activityModelProvider) ActivityExecutions(
	_ context.Context,
	query manager.ActivityExecutionsQuery,
) (manager.ActivityExecutionsResult, error) {
	provider.mu.Lock()
	provider.calls = append(provider.calls, "executions")
	provider.executionsQuery = query
	provider.mu.Unlock()
	return provider.executions, nil
}

func (provider *activityModelProvider) ActivityCoverage(
	_ context.Context,
	query manager.ActivityCoverageQuery,
) (manager.ActivityCoverageResult, error) {
	provider.mu.Lock()
	provider.calls = append(provider.calls, "coverage")
	provider.coverageQuery = query
	provider.mu.Unlock()
	return provider.coverage, nil
}

func (provider *activityModelProvider) ActivityRisks(
	_ context.Context,
	query manager.ActivityRisksQuery,
) (manager.ActivityRisksResult, error) {
	provider.mu.Lock()
	provider.calls = append(provider.calls, "risks")
	provider.risksQuery = query
	provider.mu.Unlock()
	return provider.risks, nil
}

func (provider *activityModelProvider) record(name string) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls = append(provider.calls, name)
}

func (provider *activityModelProvider) callCount(name string) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	count := 0
	for _, call := range provider.calls {
		if call == name {
			count++
		}
	}
	return count
}

func (provider *activityModelProvider) callsSnapshot() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.calls...)
}

func (provider *activityModelProvider) lastEventsQuery() manager.ActivityEventsQuery {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.eventsQueries) == 0 {
		return manager.ActivityEventsQuery{}
	}
	return provider.eventsQueries[len(provider.eventsQueries)-1]
}

func (provider *activityModelProvider) querySessions() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	eventSession := ""
	if len(provider.eventsQueries) != 0 {
		eventSession = provider.eventsQueries[0].SessionID
	}
	return []string{
		provider.summaryQuery.SessionID,
		eventSession,
		provider.executionsQuery.SessionID,
		provider.coverageQuery.SessionID,
		provider.risksQuery.SessionID,
	}
}

type activityModelFixture struct {
	owner              workloadtypes.ActivityOwner
	state              liveconsole.State
	coverage           workloadtypes.CoverageInterval
	file               workloadtypes.ActivityRecord
	foreignSessionFile workloadtypes.ActivityRecord
	risk               workloadrisk.Finding
	root               manager.ActivityExecutionNode
}

func newActivityModelFixture(t *testing.T) activityModelFixture {
	t.Helper()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_activityfixture",
		"lima",
		"incarnation-activity-fixture",
	)
	if err != nil {
		t.Fatal(err)
	}
	execution := activityModelExecution(t, owner, "ses_alpha", 2001, 1, now)
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema,
		ID:     "cov_activityfile1", Owner: owner, SessionID: "ses_alpha",
		Subsystem: workloadtypes.SubsystemFile, State: workloadtypes.CoveragePartial,
		Reason: "fanotify-fallback", CollectorGeneration: 1,
		DroppedEventCount: 2, StartedAt: now.Add(-time.Minute),
		Evidence: []workloadtypes.CoverageEvidence{{Code: "fanotify-fallback"}},
	}
	file := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     "act_activityfile1", Owner: owner, SessionID: "ses_alpha",
		Actor: &workloadtypes.Actor{
			ExecutionID: execution.ID, PID: execution.PID, UID: 1000, GID: 1000,
			User: "hideout",
		},
		Kind: workloadtypes.ActivityFile, Operation: "write",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/workspace/alpha.txt",
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   2, Bytes: 64, FirstAt: now.Add(-10 * time.Second), LastAt: now,
		FirstSequence: 1, LastSequence: 2,
		Attribution: workloadtypes.AttributionExact, CoverageID: coverage.ID,
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	foreign := file
	foreign.ID = "act_activityfile2"
	foreign.SessionID = "ses_beta"
	foreign.Subject = workloadtypes.FileSubject{
		Kind: workloadtypes.ActivityFile, Path: "/workspace/beta.txt",
		PathState: "resolved", PathClass: "workspace", FileType: "regular",
	}
	risk := workloadrisk.Finding{
		Schema: workloadrisk.FindingSchema, ID: "risk_activityevidence01",
		Owner: owner, SessionID: "ses_alpha", CoverageID: coverage.ID,
		RuleSetVersion: "v1", RuleID: "file.write-outside-workspace", RuleVersion: "v1",
		Severity: workloadrisk.SeverityHigh, Confidence: workloadrisk.ConfidenceExact,
		PolicyStatus:      workloadrisk.PolicyAllowed,
		PolicyDisposition: workloadrisk.PolicyDispositionAllowedObserved,
		Title:             "File changed outside the workspace",
		Explanation:       "A workload execution changed a file outside the workspace.",
		NextAction:        "activity.files", Count: 2,
		EvidenceRefs: []string{file.ID}, FirstAt: file.FirstAt, LastAt: file.LastAt,
	}
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{
		DaemonInstanceID: "daemon_activityfixture", CredentialGeneration: 3,
		EventSequence: 8, StreamHealth: liveconsole.HealthLive,
		Overview: manager.Overview{
			Version: "hideout.manager/v1",
			Sessions: []manager.SessionSummary{
				{ID: "ses_alpha", EnvironmentID: owner.EnvironmentID, Profile: "default", State: "running", CommandClass: "claude"},
				{ID: "ses_beta", EnvironmentID: owner.EnvironmentID, Profile: "default", State: "running", CommandClass: "codex"},
			},
		},
		Activity: liveconsole.ActivityProjection{
			Counts: []liveconsole.ActivityCount{{Kind: workloadtypes.ActivityFile, Count: 2}},
			Recent: []workloadtypes.ActivityRecord{file},
		},
		Coverage: []workloadtypes.CoverageInterval{coverage},
	}))
	return activityModelFixture{
		owner: owner, state: state, coverage: coverage, file: file,
		foreignSessionFile: foreign, risk: risk,
		root: manager.ActivityExecutionNode{
			Execution:      execution,
			ActivityCounts: map[string]uint64{workloadtypes.ActivityFile: 2},
			Children:       []manager.ActivityExecutionNode{},
		},
	}
}

func activityModelExecution(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	pid uint32,
	sequence uint64,
	at time.Time,
) workloadtypes.Execution {
	t.Helper()
	identity := workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID, GuestBootID: "boot-activity-fixture",
		ObserverGeneration: 1, PID: pid, ExecSequence: sequence,
		StartedAtMonoNS: uint64(at.UnixNano()),
	}
	id, err := workloadtypes.NewExecutionID(identity)
	if err != nil {
		t.Fatal(err)
	}
	return workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id, Owner: owner, SessionID: sessionID,
		GuestBootID: identity.GuestBootID, ObserverGeneration: 1,
		PID: pid, TID: pid, ExecSequence: sequence,
		StartedAtMonoNS: identity.StartedAtMonoNS, StartedAt: at,
		Executable: "/usr/bin/gofmt", Argv: []string{"gofmt", "-w", "/workspace/alpha.txt"},
		Cwd: "/workspace", Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "hideout",
		},
		Limitations: []string{},
	}
}

func updateModelWithCommand(
	t *testing.T,
	model *Model,
	message tea.Msg,
) (*Model, tea.Cmd) {
	t.Helper()
	updated, command := model.Update(message)
	next, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}
	return next, command
}
