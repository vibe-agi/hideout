package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityCommandsShareExactOwnerQueriesAcrossHumanAndJSON(t *testing.T) {
	fixture := newActivityCLIFixture(t)
	root := initializeActivityCLIStore(t)
	provider := &activityCLIProvider{fixture: fixture}
	snapshot := fixture.snapshot
	newApp := func(output *bytes.Buffer) app {
		return app{
			stdout: output, stderr: output, stdin: strings.NewReader(""),
			activityProvider: func(storeRoot string) manager.ActivityProvider {
				if storeRoot != root {
					t.Fatalf("activity provider store root=%q want=%q", storeRoot, root)
				}
				return provider
			},
			activitySnapshot: func(
				_ context.Context,
				storeRoot string,
				query manager.OperatorSnapshotQuery,
			) (manager.OperatorSnapshot, error) {
				if storeRoot != root || query.Session != "ses_activitycli" {
					t.Fatalf("snapshot query root=%q query=%+v", storeRoot, query)
				}
				return snapshot, nil
			},
		}
	}

	var human bytes.Buffer
	a := newApp(&human)
	if err := a.run([]string{
		"activity", "events",
		"--session", "ses_activitycli",
		"--kind", "file",
		"--operation", "write",
		"--path", "/workspace",
		"--limit", "25",
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Activity events",
		"act_activitycli01",
		"/workspace/alpha.txt",
		"exec_",
		"coverage file Partial",
		"query-truncated: false",
	} {
		if !strings.Contains(human.String(), expected) {
			t.Fatalf("human events missing %q:\n%s", expected, human.String())
		}
	}
	query := provider.lastEventsQuery()
	if !query.Owner.Equal(fixture.owner) || query.Limit != 25 ||
		query.SessionID != "ses_activitycli" ||
		len(query.Kinds) != 1 || query.Kinds[0] != workloadtypes.ActivityFile ||
		len(query.Operations) != 1 || query.Operations[0] != "write" ||
		query.Path != "/workspace" {
		t.Fatalf("events query=%+v", query)
	}

	var structured bytes.Buffer
	a = newApp(&structured)
	if err := a.run([]string{
		"activity", "events", "--session", "ses_activitycli", "--json",
	}); err != nil {
		t.Fatal(err)
	}
	var decoded manager.ActivityEventsPage
	if err := json.Unmarshal(structured.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, structured.String())
	}
	if len(decoded.Records) != 1 ||
		decoded.Records[0].ID != fixture.record.ID ||
		decoded.Records[0].Subject.(workloadtypes.FileSubject).Path !=
			"/workspace/alpha.txt" {
		t.Fatalf("JSON events lost canonical fields: %+v", decoded)
	}
}

func TestActivityCommandsExposeSummaryExecutionsCoverageAndRiskParity(t *testing.T) {
	fixture := newActivityCLIFixture(t)
	initializeActivityCLIStore(t)
	provider := &activityCLIProvider{fixture: fixture}
	makeApp := func(output *bytes.Buffer) app {
		return app{
			stdout: output, stderr: output,
			activityProvider: func(string) manager.ActivityProvider {
				return provider
			},
			activitySnapshot: func(
				context.Context,
				string,
				manager.OperatorSnapshotQuery,
			) (manager.OperatorSnapshot, error) {
				return fixture.snapshot, nil
			},
		}
	}
	cases := []struct {
		subcommand string
		flags      []string
		human      []string
	}{
		{
			subcommand: "summary",
			human: []string{
				"Activity summary", "file: 2", "retained:",
				"highest risks", "file.write-outside-workspace/v1",
			},
		},
		{
			subcommand: "executions",
			flags:      []string{"--roots"},
			human: []string{
				"Activity executions", fixture.execution.ID,
				"gofmt -w /workspace/alpha.txt", "file=2",
			},
		},
		{
			subcommand: "coverage",
			flags:      []string{"--subsystem", "file"},
			human: []string{
				"Activity coverage", "coverage file Partial",
				"fanotify-fallback", "dropped=2",
			},
		},
		{
			subcommand: "risks",
			flags:      []string{"--severity", "high"},
			human: []string{
				"Activity risks", fixture.risk.ID,
				"file.write-outside-workspace/v1",
				"allowed/allowed-observed", fixture.record.ID,
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.subcommand, func(t *testing.T) {
			var output bytes.Buffer
			args := []string{
				"activity", testCase.subcommand,
				"--session", "ses_activitycli",
			}
			args = append(args, testCase.flags...)
			if err := makeApp(&output).run(args); err != nil {
				t.Fatal(err)
			}
			for _, expected := range testCase.human {
				if !strings.Contains(output.String(), expected) {
					t.Fatalf("%s missing %q:\n%s", testCase.subcommand, expected, output.String())
				}
			}

			output.Reset()
			args = append(args, "--json")
			if err := makeApp(&output).run(args); err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(output.Bytes(), &value); err != nil {
				t.Fatalf("%s JSON invalid: %v\n%s", testCase.subcommand, err, output.String())
			}
		})
	}
	if query := provider.lastSummaryQuery(); query.SessionID != "ses_activitycli" {
		t.Fatalf("summary session scope was not forwarded: %+v", query)
	}
	if query := provider.lastExecutionsQuery(); !query.RootsOnly ||
		query.SessionID != "ses_activitycli" {
		t.Fatalf("execution root filter was not forwarded: %+v", query)
	}
	if query := provider.lastCoverageQuery(); len(query.Subsystems) != 1 ||
		query.Subsystems[0] != workloadtypes.SubsystemFile ||
		query.SessionID != "ses_activitycli" {
		t.Fatalf("coverage filters=%+v", query)
	}
	if query := provider.lastRisksQuery(); len(query.Severities) != 1 ||
		query.Severities[0] != workloadrisk.SeverityHigh ||
		query.SessionID != "ses_activitycli" {
		t.Fatalf("risk filters=%+v", query)
	}
}

func TestActivityCommandDefaultsToNewestSessionAndRejectsAmbiguousOwner(t *testing.T) {
	fixture := newActivityCLIFixture(t)
	initializeActivityCLIStore(t)
	provider := &activityCLIProvider{fixture: fixture}
	older := fixture.snapshot.Sessions[0]
	older.ID = "ses_activityolder"
	older.StartedAt = older.StartedAt.Add(-time.Hour)
	fixture.snapshot.Sessions = append(
		[]manager.OperatorSessionProjection{older},
		fixture.snapshot.Sessions...,
	)
	var snapshotQuery manager.OperatorSnapshotQuery
	var output bytes.Buffer
	a := app{
		stdout: &output, stderr: &output,
		activityProvider: func(string) manager.ActivityProvider {
			return provider
		},
		activitySnapshot: func(
			_ context.Context,
			_ string,
			query manager.OperatorSnapshotQuery,
		) (manager.OperatorSnapshot, error) {
			snapshotQuery = query
			return fixture.snapshot, nil
		},
	}
	if err := a.run([]string{"activity", "summary"}); err != nil {
		t.Fatal(err)
	}
	if snapshotQuery.Session != "" ||
		!strings.Contains(output.String(), "session ses_activitycli") {
		t.Fatalf("default selection query=%+v output=%s", snapshotQuery, output.String())
	}

	output.Reset()
	err := a.run([]string{
		"activity", "summary",
		"--session", "ses_activitycli",
		"--environment", fixture.owner.EnvironmentID,
		"--incarnation", fixture.owner.BackendIncarnationID,
	})
	if err == nil || !strings.Contains(err.Error(), "either --session or") {
		t.Fatalf("ambiguous owner error=%v", err)
	}
}

func TestActivityCommandRejectsInvalidFiltersBeforeQuery(t *testing.T) {
	fixture := newActivityCLIFixture(t)
	initializeActivityCLIStore(t)
	makeApp := func(provider *activityCLIProvider) app {
		return app{
			stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
			activityProvider: func(string) manager.ActivityProvider {
				return provider
			},
			activitySnapshot: func(
				context.Context,
				string,
				manager.OperatorSnapshotQuery,
			) (manager.OperatorSnapshot, error) {
				return fixture.snapshot, nil
			},
		}
	}
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "short execution filter",
			args: []string{
				"activity", "events", "--session", "ses_activitycli",
				"--execution", "exec_",
			},
		},
		{
			name: "short risk filter",
			args: []string{
				"activity", "events", "--session", "ses_activitycli",
				"--risk", "risk_",
			},
		},
		{
			name: "invalid risk rule",
			args: []string{
				"activity", "risks", "--session", "ses_activitycli",
				"--rule", ".",
			},
		},
		{
			name: "control in path",
			args: []string{
				"activity", "events", "--session", "ses_activitycli",
				"--path", "/workspace/\nforged",
			},
		},
		{
			name: "oversized domain",
			args: []string{
				"activity", "events", "--session", "ses_activitycli",
				"--domain", strings.Repeat("a", 254),
			},
		},
		{
			name: "oversized cursor",
			args: []string{
				"activity", "events", "--session", "ses_activitycli",
				"--cursor", strings.Repeat("x", 4097),
			},
		},
		{
			name: "reversed time range",
			args: []string{
				"activity", "summary", "--session", "ses_activitycli",
				"--from", "2026-07-29T12:00:01Z",
				"--to", "2026-07-29T12:00:00Z",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &activityCLIProvider{fixture: fixture}
			if err := makeApp(provider).run(testCase.args); err == nil {
				t.Fatalf("expected invalid filter rejection for %v", testCase.args)
			}
			if provider.queryCount() != 0 {
				t.Fatalf("invalid filter reached provider: %v", testCase.args)
			}
		})
	}

	provider := &activityCLIProvider{fixture: fixture}
	args := []string{
		"activity", "events", "--session", "ses_activitycli",
	}
	for index := 0; index < 129; index++ {
		args = append(args, "--operation", "op."+strconv.Itoa(index))
	}
	if err := makeApp(provider).run(args); err == nil {
		t.Fatal("expected excessive repeated filters to be rejected")
	}
	if provider.queryCount() != 0 {
		t.Fatal("excessive repeated filters reached provider")
	}
}

func TestActivityCommandResolvesExplicitReusableOwner(t *testing.T) {
	fixture := newActivityCLIFixture(t)
	initializeActivityCLIStore(t)
	provider := &activityCLIProvider{fixture: fixture}
	var output bytes.Buffer
	a := app{
		stdout: &output, stderr: &output,
		activityProvider: func(string) manager.ActivityProvider {
			return provider
		},
		activitySnapshot: func(
			context.Context,
			string,
			manager.OperatorSnapshotQuery,
		) (manager.OperatorSnapshot, error) {
			t.Fatal("explicit reusable owner must not depend on a session snapshot")
			return manager.OperatorSnapshot{}, nil
		},
	}
	if err := a.run([]string{
		"activity", "summary",
		"--environment", fixture.owner.EnvironmentID,
		"--incarnation", fixture.owner.BackendIncarnationID,
	}); err != nil {
		t.Fatal(err)
	}
	selector := provider.lastOwnerSelector()
	if selector.EnvironmentID != fixture.owner.EnvironmentID ||
		selector.BackendIncarnationID != fixture.owner.BackendIncarnationID ||
		selector.SessionID != "" {
		t.Fatalf("owner selector=%+v", selector)
	}
	if !strings.Contains(output.String(), "environment="+fixture.owner.EnvironmentID) {
		t.Fatalf("human owner output missing exact environment:\n%s", output.String())
	}
	if query := provider.lastSummaryQuery(); query.SessionID != "" {
		t.Fatalf("environment-wide query was unexpectedly session-scoped: %+v", query)
	}
}

func TestActivityHumanOutputEscapesTerminalControlsButJSONPreservesData(t *testing.T) {
	fixture := newActivityCLIFixture(t)
	initializeActivityCLIStore(t)
	record := fixture.record
	record.Subject = workloadtypes.FileSubject{
		Kind:      workloadtypes.ActivityFile,
		Path:      "/workspace/a\x1b]8;;https://evil.invalid\aCLICK\x1b]8;;\afile",
		PathState: "resolved", PathClass: "workspace", FileType: "regular",
	}
	fixture.record = record
	provider := &activityCLIProvider{fixture: fixture}
	makeApp := func(output *bytes.Buffer) app {
		return app{
			stdout: output, stderr: output,
			activityProvider: func(string) manager.ActivityProvider { return provider },
			activitySnapshot: func(
				context.Context,
				string,
				manager.OperatorSnapshotQuery,
			) (manager.OperatorSnapshot, error) {
				return fixture.snapshot, nil
			},
		}
	}
	var human bytes.Buffer
	if err := makeApp(&human).run([]string{
		"activity", "events", "--session", "ses_activitycli",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(human.String(), "\x1b\a") ||
		!strings.Contains(human.String(), `\u001b`) {
		t.Fatalf("human output is terminal-unsafe: %q", human.String())
	}
	var structured bytes.Buffer
	if err := makeApp(&structured).run([]string{
		"activity", "events", "--session", "ses_activitycli", "--json",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(structured.String(), "\x1b\a") ||
		!strings.Contains(structured.String(), `\u001b`) {
		t.Fatalf("JSON output is unsafe or lost escaped data: %q", structured.String())
	}
}

func initializeActivityCLIStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HIDEOUT_STORE_ROOT", root)
	store := profile.Store{Root: root}
	if _, err := store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	return root
}

type activityCLIFixture struct {
	owner     workloadtypes.ActivityOwner
	coverage  workloadtypes.CoverageInterval
	record    workloadtypes.ActivityRecord
	execution workloadtypes.Execution
	risk      workloadrisk.Finding
	snapshot  manager.OperatorSnapshot
}

func newActivityCLIFixture(t *testing.T) activityCLIFixture {
	t.Helper()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_activitycli",
		"lima",
		"incarnation-activity-cli",
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: "ses_activitycli", GuestBootID: "boot-activity-cli",
		ObserverGeneration: 1, PID: 2201, ExecSequence: 1,
		StartedAtMonoNS: uint64(now.UnixNano()),
	}
	executionID, err := workloadtypes.NewExecutionID(identity)
	if err != nil {
		t.Fatal(err)
	}
	execution := workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: executionID,
		Owner: owner, SessionID: identity.SessionID,
		GuestBootID: identity.GuestBootID, ObserverGeneration: 1,
		PID: identity.PID, TID: identity.PID, ExecSequence: 1,
		StartedAtMonoNS: identity.StartedAtMonoNS, StartedAt: now,
		Executable: "/usr/bin/gofmt",
		Argv:       []string{"gofmt", "-w", "/workspace/alpha.txt"},
		Cwd:        "/workspace",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "hideout",
		},
		Limitations: []string{},
	}
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_activitycli01",
		Owner: owner, SessionID: identity.SessionID,
		Subsystem: workloadtypes.SubsystemFile,
		State:     workloadtypes.CoveragePartial, Reason: "fanotify-fallback",
		CollectorGeneration: 1, DroppedEventCount: 2,
		Evidence:  []workloadtypes.CoverageEvidence{{Code: "fanotify-fallback"}},
		StartedAt: now.Add(-time.Minute),
	}
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: "act_activitycli01",
		Owner: owner, SessionID: identity.SessionID,
		Actor: &workloadtypes.Actor{
			ExecutionID: execution.ID, PID: execution.PID,
			UID: 1000, GID: 1000, User: "hideout",
		},
		Kind: workloadtypes.ActivityFile, Operation: "write",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/workspace/alpha.txt",
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   2, Bytes: 64, FirstAt: now.Add(-time.Second), LastAt: now,
		FirstSequence: 1, LastSequence: 2,
		Attribution: workloadtypes.AttributionExact, CoverageID: coverage.ID,
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	risk := workloadrisk.Finding{
		Schema: workloadrisk.FindingSchema, ID: "risk_activitycli00001",
		Owner: owner, SessionID: identity.SessionID, CoverageID: coverage.ID,
		RuleSetVersion: "v1", RuleID: "file.write-outside-workspace", RuleVersion: "v1",
		Severity: workloadrisk.SeverityHigh, Confidence: workloadrisk.ConfidenceExact,
		PolicyStatus:      workloadrisk.PolicyAllowed,
		PolicyDisposition: workloadrisk.PolicyDispositionAllowedObserved,
		Title:             "File changed outside the workspace",
		Explanation:       "A workload execution changed a file outside the workspace.",
		NextAction:        "activity.files", Count: 2,
		EvidenceRefs: []string{record.ID}, FirstAt: record.FirstAt, LastAt: record.LastAt,
	}
	snapshot := manager.OperatorSnapshot{
		Schema: manager.OperatorSnapshotSchema, GeneratedAt: now,
		InstanceID: "daemon_activitycli", CredentialGeneration: 1,
		Sequence:     1,
		StreamHealth: manager.OperatorStreamHealth{State: manager.OperatorHealthLive},
		Profiles:     []manager.ProfileProjection{},
		Sessions: []manager.OperatorSessionProjection{{
			ID: identity.SessionID, EnvironmentID: owner.EnvironmentID,
			Profile: "default", State: "running", Command: "claude",
			StartedAt: now.Add(-time.Minute),
		}},
		Activity: []manager.ActivityProjection{record},
		Coverage: []manager.CoverageProjection{coverage},
		Risks:    []manager.RiskFinding{}, Operations: []manager.Operation{},
		Capabilities: []manager.OperatorCapabilityProjection{},
		NextActions:  []string{},
	}
	return activityCLIFixture{
		owner: owner, coverage: coverage, record: record,
		execution: execution, risk: risk, snapshot: snapshot,
	}
}

type activityCLIProvider struct {
	mu      sync.Mutex
	fixture activityCLIFixture

	ownerSelector    manager.ActivityOwnerSelector
	summaryQuery     manager.ActivitySummaryQuery
	summaryQueries   int
	eventsQuery      manager.ActivityEventsQuery
	eventsQueries    int
	executionsQuery  manager.ActivityExecutionsQuery
	executionQueries int
	coverageQuery    manager.ActivityCoverageQuery
	coverageQueries  int
	risksQuery       manager.ActivityRisksQuery
	riskQueries      int
}

func (provider *activityCLIProvider) ResolveActivityOwner(
	_ context.Context,
	selector manager.ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	provider.mu.Lock()
	provider.ownerSelector = selector
	provider.mu.Unlock()
	if provider.fixture.owner.Validate() != nil {
		return workloadtypes.ActivityOwner{}, manager.ErrActivityOwnerNotFound
	}
	return provider.fixture.owner, nil
}

func (provider *activityCLIProvider) ActivitySummary(
	_ context.Context,
	query manager.ActivitySummaryQuery,
) (manager.ActivitySummaryResult, error) {
	if !query.Owner.Equal(provider.fixture.owner) {
		return manager.ActivitySummaryResult{}, manager.ErrActivityOwnerNotFound
	}
	provider.mu.Lock()
	provider.summaryQuery = query
	provider.summaryQueries++
	provider.mu.Unlock()
	return manager.ActivitySummaryResult{
		Owner:           query.Owner,
		Counts:          map[string]uint64{workloadtypes.ActivityFile: 2},
		CurrentCoverage: []workloadtypes.CoverageInterval{provider.fixture.coverage},
		HighestRisks:    []workloadrisk.Finding{provider.fixture.risk},
		RetainedRange:   manager.ActivitySummaryResult{}.RetainedRange,
		Quota:           manager.ActivitySummaryResult{}.Quota,
		Reasons:         []string{"fanotify-fallback"},
		LatestCursor:    "cur_activity-cli",
	}, nil
}

func (provider *activityCLIProvider) ActivityEvents(
	_ context.Context,
	query manager.ActivityEventsQuery,
) (manager.ActivityEventsPage, error) {
	provider.mu.Lock()
	provider.eventsQuery = query
	provider.eventsQueries++
	provider.mu.Unlock()
	return manager.ActivityEventsPage{
		Records:  []workloadtypes.ActivityRecord{provider.fixture.record},
		Coverage: []workloadtypes.CoverageInterval{provider.fixture.coverage},
	}, nil
}

func (provider *activityCLIProvider) ActivityExecutions(
	_ context.Context,
	query manager.ActivityExecutionsQuery,
) (manager.ActivityExecutionsResult, error) {
	provider.mu.Lock()
	provider.executionsQuery = query
	provider.executionQueries++
	provider.mu.Unlock()
	return manager.ActivityExecutionsResult{
		Roots: []manager.ActivityExecutionNode{{
			Execution:      provider.fixture.execution,
			ActivityCounts: map[string]uint64{workloadtypes.ActivityFile: 2},
			Children:       []manager.ActivityExecutionNode{},
		}},
		Coverage: []workloadtypes.CoverageInterval{provider.fixture.coverage},
	}, nil
}

func (provider *activityCLIProvider) ActivityCoverage(
	_ context.Context,
	query manager.ActivityCoverageQuery,
) (manager.ActivityCoverageResult, error) {
	provider.mu.Lock()
	provider.coverageQuery = query
	provider.coverageQueries++
	provider.mu.Unlock()
	return manager.ActivityCoverageResult{
		Intervals: []workloadtypes.CoverageInterval{provider.fixture.coverage},
		Current:   []workloadtypes.CoverageInterval{provider.fixture.coverage},
	}, nil
}

func (provider *activityCLIProvider) ActivityRisks(
	_ context.Context,
	query manager.ActivityRisksQuery,
) (manager.ActivityRisksResult, error) {
	provider.mu.Lock()
	provider.risksQuery = query
	provider.riskQueries++
	provider.mu.Unlock()
	return manager.ActivityRisksResult{
		Findings: []workloadrisk.Finding{provider.fixture.risk},
	}, nil
}

func (provider *activityCLIProvider) lastEventsQuery() manager.ActivityEventsQuery {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.eventsQuery
}

func (provider *activityCLIProvider) lastSummaryQuery() manager.ActivitySummaryQuery {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.summaryQuery
}

func (provider *activityCLIProvider) lastExecutionsQuery() manager.ActivityExecutionsQuery {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.executionsQuery
}

func (provider *activityCLIProvider) lastCoverageQuery() manager.ActivityCoverageQuery {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.coverageQuery
}

func (provider *activityCLIProvider) lastRisksQuery() manager.ActivityRisksQuery {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.risksQuery
}

func (provider *activityCLIProvider) lastOwnerSelector() manager.ActivityOwnerSelector {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.ownerSelector
}

func (provider *activityCLIProvider) queryCount() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.summaryQueries +
		provider.eventsQueries +
		provider.executionQueries +
		provider.coverageQueries +
		provider.riskQueries
}

func TestActivityCommandRequiresRunningDaemonForAuthoritativeQuery(t *testing.T) {
	initializeActivityCLIStore(t)
	var output bytes.Buffer
	a := app{
		stdout: &output,
		stderr: &output,
		activitySnapshot: func(
			context.Context,
			string,
			manager.OperatorSnapshotQuery,
		) (manager.OperatorSnapshot, error) {
			return manager.OperatorSnapshot{}, errors.New("daemon unavailable")
		},
	}
	err := a.run([]string{"activity", "summary"})
	if err == nil || !strings.Contains(err.Error(), "running Hideout daemon") {
		t.Fatalf("daemon recovery error=%v", err)
	}
}
