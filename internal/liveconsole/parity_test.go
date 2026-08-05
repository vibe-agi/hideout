package liveconsole_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	tuirender "github.com/vibe-agi/hideout/internal/tui/render"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestParityFixtureSnapshotAndDetailsExposeIdenticalSurfaceFacts(t *testing.T) {
	fixture := newParityFixture(t)

	// The scriptable CLI's --json commands expose these Manager values
	// directly. TUI and browser consumers must not reinterpret their meaning.
	cliFacts := parityFactsFromSnapshot(
		t, fixture.finalSnapshot, fixture.details,
	)

	tuiState, err := liveconsole.NewStateFromOperatorSnapshot(
		fixture.finalSnapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	tuiFacts := parityFactsFromState(t, tuiState, fixture.details)
	assertParityFacts(t, "TUI", tuiFacts, cliFacts)
	assertParityTUIRows(t, tuiState, fixture.details)

	browserSnapshot, browserDetails := parityBrowserRoundTrip(
		t, fixture.finalSnapshot, fixture.details,
	)
	browserState, err := liveconsole.NewStateFromOperatorSnapshot(
		browserSnapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	browserFacts := parityFactsFromState(
		t, browserState, browserDetails,
	)
	assertParityFacts(t, "browser", browserFacts, cliFacts)

	if _, ok := browserDetails.Events.Records[0].Subject.(workloadtypes.FileSubject); !ok {
		t.Fatalf(
			"browser JSON lost the closed activity subject union: %T",
			browserDetails.Events.Records[0].Subject,
		)
	}
}

func TestParityFixtureEventStreamConvergesWithAuthoritativeRefresh(t *testing.T) {
	fixture := newParityFixture(t)
	want := parityFactsFromSnapshot(
		t, fixture.finalSnapshot, fixture.details,
	)

	tuiState, err := liveconsole.NewStateFromOperatorSnapshot(
		fixture.initialSnapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range fixture.events {
		if result := liveconsole.Apply(&tuiState, event); result.Status != liveconsole.ResultApplied {
			t.Fatalf("TUI event %s result=%+v", event.Kind, result)
		}
	}
	if tuiState.LastSeq != fixture.finalSnapshot.Sequence ||
		!tuiState.CanMutate() {
		t.Fatalf(
			"TUI did not converge to a live mutable state: seq=%d state=%+v",
			tuiState.LastSeq,
			tuiState.StreamHealth,
		)
	}
	assertParityFacts(
		t,
		"TUI event stream",
		parityFactsFromState(t, tuiState, fixture.details),
		want,
	)

	browserSeed, browserDetails := parityBrowserRoundTrip(
		t, fixture.initialSnapshot, fixture.details,
	)
	browserState, err := liveconsole.NewStateFromOperatorSnapshot(browserSeed)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range fixture.events {
		event := parityBrowserEventRoundTrip(t, source)
		if result := liveconsole.Apply(
			&browserState,
			event,
		); result.Status != liveconsole.ResultApplied {
			t.Fatalf("browser event %s result=%+v", event.Kind, result)
		}
	}
	assertParityFacts(
		t,
		"browser event stream",
		parityFactsFromState(t, browserState, browserDetails),
		want,
	)
}

type parityFixture struct {
	initialSnapshot manager.OperatorSnapshot
	finalSnapshot   manager.OperatorSnapshot
	events          []liveconsole.Event
	details         parityDetails
}

type parityDetails struct {
	Summary    manager.ActivitySummaryResult    `json:"summary"`
	Events     manager.ActivityEventsPage       `json:"events"`
	Executions manager.ActivityExecutionsResult `json:"executions"`
	Coverage   manager.ActivityCoverageResult   `json:"coverage"`
	Risks      manager.ActivityRisksResult      `json:"risks"`
}

type parityFacts struct {
	Health              string
	ReadOnly            bool
	Profile             string
	DesiredNetwork      string
	DesiredProxyRef     string
	EffectiveNetwork    string
	EffectiveProxyRef   string
	EffectiveGeneration uint64
	TransitionPhase     string
	TransitionBlockers  []string
	OperationID         string
	OperationPhase      string
	OperationResult     string
	OperationEvidence   []string
	ActivityCursor      string
	ActivityCounts      map[string]uint64
	CoverageID          string
	CoverageState       string
	CoverageReason      string
	DroppedEvents       uint64
	RiskID              string
	RiskRule            string
	RiskSeverity        string
	RiskConfidence      string
	RiskPolicy          string
	RiskEvidence        []string
	DetailRecords       []parityRecordFact
	DetailExecutions    []parityExecutionFact
	DetailCoverage      []parityCoverageFact
	QueryTruncated      bool
	NextCursor          string
}

type parityRecordFact struct {
	ID          string
	Kind        string
	Operation   string
	ExecutionID string
	Path        string
	Count       uint64
}

type parityExecutionFact struct {
	ID       string
	ParentID string
	Counts   map[string]uint64
}

type parityCoverageFact struct {
	ID      string
	State   string
	Reason  string
	Dropped uint64
}

func newParityFixture(t *testing.T) parityFixture {
	t.Helper()
	now := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	sessionID := "ses_20260729T140000Z_parity"
	owner, err := workloadtypes.NewReusableOwner(
		"env_parity",
		"lima",
		"incarnation-parity-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	execution := parityExecution(t, owner, sessionID, now.Add(-5*time.Minute))
	initialCoverage := parityCoverage(
		t,
		owner,
		sessionID,
		workloadtypes.CoverageAvailable,
		"collector-active",
		0,
		now.Add(-5*time.Minute),
	)
	finalCoverage := parityCoverage(
		t,
		owner,
		sessionID,
		workloadtypes.CoveragePartial,
		"ring-overflow",
		1,
		now.Add(-5*time.Minute),
	)
	first := parityFileRecord(
		t,
		owner,
		sessionID,
		execution,
		initialCoverage.ID,
		"act_parity_file_01",
		1,
		now.Add(-2*time.Minute),
	)
	second := parityFileRecord(
		t,
		owner,
		sessionID,
		execution,
		finalCoverage.ID,
		"act_parity_file_02",
		2,
		now.Add(-time.Minute),
	)
	initialRisk := parityRisk(t, first)
	finalRisk := parityRisk(t, first, second)

	initialOperation := parityOperation(
		now,
		manager.OperationActivating,
		manager.EffectRunning,
		nil,
	)
	finalOperation := parityOperation(
		now,
		manager.OperationSucceeded,
		manager.EffectSucceeded,
		&manager.OperationResult{
			Status:  manager.OperationSucceeded,
			Code:    "route-proved",
			Summary: "Proxy route activation was proved.",
		},
	)
	initialProfile := parityProfileProjection(
		now,
		initialOperation.ID,
		profile.NetworkModeDirect,
		"",
		0,
		&manager.ProfileTransition{
			OperationID: initialOperation.ID,
			Kind:        "network",
			Phase:       manager.OperationActivating,
			Blockers:    []string{"active-session"},
			StartedAt:   now.Add(-time.Minute),
		},
	)
	finalProfile := parityProfileProjection(
		now.Add(5*time.Second),
		finalOperation.ID,
		"proxy",
		"local-proxy",
		3,
		nil,
	)
	capability := manager.OperatorCapabilityProjection{
		ID:       manager.CapabilityConfigNetworkPosture,
		State:    manager.OperatorCapabilityAvailable,
		Provider: "manager", Mutable: true,
		ActionRefs: []string{"config.network"},
	}
	initialSnapshot := paritySnapshot(
		now,
		41,
		owner,
		sessionID,
		initialProfile,
		initialOperation,
		[]workloadtypes.ActivityRecord{first},
		initialCoverage,
		initialRisk,
		capability,
		"cur_parity_initial",
	)
	finalSnapshot := paritySnapshot(
		now.Add(5*time.Second),
		46,
		owner,
		sessionID,
		finalProfile,
		finalOperation,
		[]workloadtypes.ActivityRecord{first, second},
		finalCoverage,
		finalRisk,
		capability,
		"cur_parity_final",
	)

	details := parityDetailFixture(
		owner,
		sessionID,
		execution,
		[]workloadtypes.ActivityRecord{first, second},
		finalCoverage,
		finalRisk,
	)
	events := []liveconsole.Event{
		parityProjectionEvent(
			42,
			liveconsole.KindOperation,
			liveconsole.EventPayload{
				OperationProjection: &finalOperation,
			},
		),
		parityProjectionEvent(
			43,
			liveconsole.KindCoverage,
			liveconsole.EventPayload{
				CoverageProjection: []workloadtypes.CoverageInterval{
					finalCoverage,
				},
			},
		),
		parityProjectionEvent(
			44,
			liveconsole.KindRisk,
			liveconsole.EventPayload{
				RiskProjection: parityLiveRisk(finalRisk),
			},
		),
		parityProjectionEvent(
			45,
			liveconsole.KindActivity,
			liveconsole.EventPayload{
				ActivityProjection: &liveconsole.ActivityProjectionDelta{
					Profile: "default", Session: sessionID,
					Cursor: "cur_parity_final",
					Counts: []liveconsole.ActivityCount{{
						Kind: workloadtypes.ActivityFile, Count: 2,
					}},
					Appended: 1, LastAt: second.LastAt,
				},
			},
		),
		parityProjectionEvent(
			46,
			liveconsole.KindProfile,
			liveconsole.EventPayload{
				ProfileProjection: &finalProfile,
			},
		),
	}
	for _, event := range events {
		if err := liveconsole.ValidateEvent(event); err != nil {
			t.Fatalf("event %s: %v", event.Kind, err)
		}
	}
	return parityFixture{
		initialSnapshot: initialSnapshot,
		finalSnapshot:   finalSnapshot,
		events:          events,
		details:         details,
	}
}

func paritySnapshot(
	timestamp time.Time,
	sequence int,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	projection manager.ProfileProjection,
	operation manager.Operation,
	records []workloadtypes.ActivityRecord,
	coverage workloadtypes.CoverageInterval,
	finding workloadrisk.Finding,
	capability manager.OperatorCapabilityProjection,
	cursor string,
) manager.OperatorSnapshot {
	snapshot := manager.OperatorSnapshot{
		Schema:      manager.OperatorSnapshotSchema,
		GeneratedAt: timestamp, InstanceID: "daemon_parity",
		CredentialGeneration: 7, Sequence: sequence,
		StreamHealth: manager.OperatorStreamHealth{
			State: manager.OperatorHealthLive,
		},
		Profiles: []manager.ProfileProjection{projection},
		Sessions: []manager.OperatorSessionProjection{{
			ID: sessionID, EnvironmentID: owner.EnvironmentID,
			Profile: "default", State: "running",
			Command: "claude", StartedAt: timestamp.Add(-10 * time.Minute),
		}},
		Environments: []manager.OperatorEnvironmentProjection{{
			ID: owner.EnvironmentID, Name: "parity", Profile: "default",
			Backend: "lima", Status: "running",
			InstanceName: "hideout-parity", ActiveSessions: 1,
			OwnerHealth: "live", CreatedAt: timestamp.Add(-time.Hour),
		}},
		Activity:       append([]workloadtypes.ActivityRecord(nil), records...),
		ActivityCursor: cursor,
		Coverage:       []workloadtypes.CoverageInterval{coverage},
		ActivityRetention: []manager.OperatorActivityRetentionProjection{{
			Owner:      owner,
			EarliestAt: timestamp.Add(-time.Hour), LatestAt: timestamp,
			UsedBytes: 2048, LimitBytes: 4096, MaxAgeSeconds: 3600,
			Reasons: []string{},
		}},
		ActivityStoreRetention: &manager.OperatorActivityStoreRetentionProjection{
			UsedBytes: 2048, LimitBytes: 1 << 20,
			DefaultOwnerLimitBytes: 4096,
			ActiveSegmentBytes:     1024,
			Owners:                 1, Segments: 1,
		},
		Risks: []manager.RiskFinding{
			parityManagerRisk(finding),
		},
		Operations:   []manager.Operation{operation},
		Decisions:    []manager.OperatorDecisionProjection{},
		Notices:      []manager.OperatorNoticeProjection{},
		Capabilities: []manager.OperatorCapabilityProjection{capability},
		NextActions:  []string{"activity.inspect"},
	}
	if err := snapshot.Validate(); err != nil {
		panic("invalid parity snapshot: " + err.Error())
	}
	return snapshot
}

func parityProfileProjection(
	updatedAt time.Time,
	operationID string,
	effectiveMode string,
	effectiveRef string,
	generation uint64,
	transition *manager.ProfileTransition,
) manager.ProfileProjection {
	desired := profile.Default("default")
	desired.Network.Mode = profile.NetworkModeTun2Socks
	desired.Network.ProxySecretRef = "local-proxy"
	desired.Network.MediatedResolver = "1.1.1.1"
	return manager.ProfileProjection{
		Schema:  manager.ProfileProjectionSchema,
		Profile: "default", Revision: 7,
		ContentDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Desired:       desired,
		Effective: manager.ProfileEffective{
			Status: manager.EffectiveCurrent,
			Network: &manager.EffectiveNetwork{
				Mode: effectiveMode, ProxySecretRef: effectiveRef,
				SecretGeneration: generation, DNS: "1.1.1.1",
				ObservedAt: updatedAt,
			},
			Sessions: []manager.EffectiveSessionSnapshot{{
				SessionID:       "ses_20260729T140000Z_parity",
				SnapshotID:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				ProfileRevision: 7,
				Current:         true,
			}},
		},
		Transition: transition,
		UpdatedAt:  updatedAt,
	}
}

func parityOperation(
	now time.Time,
	phase string,
	effectPhase string,
	result *manager.OperationResult,
) manager.Operation {
	evidence := []manager.EvidenceRef{}
	recovery := manager.Recovery{
		Code: "retry.proof", Summary: "Retry proof using the exact operation ID.",
		NextAction: "hideout tui",
	}
	if phase == manager.OperationSucceeded {
		evidence = []manager.EvidenceRef{{
			Code: "proxy.route.proved",
			Ref:  "route-generation-3", ObservedAt: now.Add(4 * time.Second),
		}}
		recovery = manager.Recovery{
			Code: "none", Summary: "No recovery is required.",
		}
	}
	operation := manager.Operation{
		Schema: manager.OperationSchema,
		ID:     "op_parity_network_01", Kind: "profile.transaction",
		Owner:        manager.OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BaseRevision: 7, Phase: phase,
		Effects: []manager.EffectResult{{
			ID: "activate-proxy-route", Kind: "activate",
			Provider: "lima-network", Status: effectPhase,
			Evidence: evidence,
		}},
		Result: result, Recovery: recovery,
		CreatedAt: now.Add(-2 * time.Minute),
		UpdatedAt: now.Add(4 * time.Second),
	}
	if phase != manager.OperationSucceeded {
		operation.UpdatedAt = now.Add(-time.Minute)
	}
	if err := operation.Validate(); err != nil {
		panic("invalid parity operation: " + err.Error())
	}
	return operation
}

func parityExecution(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	startedAt time.Time,
) workloadtypes.Execution {
	t.Helper()
	input := workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "boot-parity-fixture",
		ObserverGeneration: 1, PID: 401, ExecSequence: 1,
		StartedAtMonoNS: uint64(startedAt.UnixNano()),
	}
	id, err := workloadtypes.NewExecutionID(input)
	if err != nil {
		t.Fatal(err)
	}
	execution := workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id,
		Owner: owner, SessionID: sessionID,
		GuestBootID: input.GuestBootID, ObserverGeneration: 1,
		PID: 401, TID: 401, ExecSequence: 1,
		StartedAtMonoNS: input.StartedAtMonoNS, StartedAt: startedAt,
		Executable: "/usr/bin/claude", Argv: []string{"claude"},
		Cwd: "/workspace",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "developer", Group: "developer",
		},
		Limitations: []string{},
	}
	if err := execution.Validate(); err != nil {
		t.Fatal(err)
	}
	return execution
}

func parityCoverage(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	state string,
	reason string,
	dropped uint64,
	startedAt time.Time,
) workloadtypes.CoverageInterval {
	t.Helper()
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema,
		ID:     "cov_parity_file_01", Owner: owner, SessionID: sessionID,
		Subsystem: workloadtypes.SubsystemFile,
		State:     state, Reason: reason, CollectorGeneration: 1,
		DroppedEventCount: dropped, StartedAt: startedAt,
	}
	if err := coverage.Validate(); err != nil {
		t.Fatal(err)
	}
	return coverage
}

func parityFileRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	execution workloadtypes.Execution,
	coverageID string,
	id string,
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
		Kind: workloadtypes.ActivityFile, Operation: "write",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/etc/ssh/sshd_config",
			PathState: "resolved", PathClass: "system", FileType: "regular",
		},
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

func parityRisk(
	t *testing.T,
	records ...workloadtypes.ActivityRecord,
) workloadrisk.Finding {
	t.Helper()
	engine, err := workloadrisk.NewEngine(workloadrisk.DefaultRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	evidence := make([]workloadrisk.Evidence, 0, len(records))
	for _, record := range records {
		evidence = append(evidence, workloadrisk.Evidence{
			Activity: record, PolicyStatus: workloadrisk.PolicyAllowed,
		})
	}
	findings, err := engine.Evaluate(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.RuleID == "file.write-outside-workspace" {
			return finding
		}
	}
	t.Fatal("parity risk fixture did not produce the expected finding")
	return workloadrisk.Finding{}
}

func parityManagerRisk(finding workloadrisk.Finding) manager.RiskFinding {
	return manager.RiskFinding{
		ID: finding.ID, RuleID: finding.RuleID,
		RuleVersion: finding.RuleVersion, Severity: finding.Severity,
		Title: finding.Title, Explanation: finding.Explanation,
		EvidenceRefs: append([]string(nil), finding.EvidenceRefs...),
		Confidence:   finding.Confidence, PolicyStatus: finding.PolicyStatus,
		FirstAt: finding.FirstAt, LastAt: finding.LastAt,
		Count: finding.Count, NextAction: finding.NextAction,
	}
}

func parityLiveRisk(finding workloadrisk.Finding) *liveconsole.RiskFinding {
	value := liveconsole.RiskFinding{
		ID: finding.ID, RuleID: finding.RuleID,
		RuleVersion: finding.RuleVersion, Severity: finding.Severity,
		Title: finding.Title, Explanation: finding.Explanation,
		EvidenceRefs: append([]string(nil), finding.EvidenceRefs...),
		Confidence:   finding.Confidence, PolicyStatus: finding.PolicyStatus,
		FirstAt: finding.FirstAt, LastAt: finding.LastAt,
		Count: finding.Count, NextAction: finding.NextAction,
	}
	return &value
}

func parityDetailFixture(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	execution workloadtypes.Execution,
	records []workloadtypes.ActivityRecord,
	coverage workloadtypes.CoverageInterval,
	finding workloadrisk.Finding,
) parityDetails {
	counts := map[string]uint64{
		workloadtypes.ActivityFile: uint64(len(records)),
	}
	return parityDetails{
		Summary: manager.ActivitySummaryResult{
			Owner: owner, Counts: counts,
			CurrentCoverage: []workloadtypes.CoverageInterval{coverage},
			HighestRisks:    []workloadrisk.Finding{finding},
			RetainedRange: workloadquery.TimeRange{
				From: coverage.StartedAt, To: records[len(records)-1].LastAt,
			},
			Quota: workloadquery.QuotaSummary{
				UsedBytes: 2048, LimitBytes: 4096, MaxAgeSeconds: 3600,
			},
			Reasons: []string{},
		},
		Events: manager.ActivityEventsPage{
			Records:  append([]workloadtypes.ActivityRecord(nil), records...),
			Coverage: []workloadtypes.CoverageInterval{coverage},
		},
		Executions: manager.ActivityExecutionsResult{
			Roots: []manager.ActivityExecutionNode{{
				Execution: execution,
				ActivityCounts: map[string]uint64{
					workloadtypes.ActivityFile: uint64(len(records)),
				},
				Children: []manager.ActivityExecutionNode{},
			}},
			Coverage: []workloadtypes.CoverageInterval{},
		},
		Coverage: manager.ActivityCoverageResult{
			Intervals: []workloadtypes.CoverageInterval{coverage},
			Current:   []workloadtypes.CoverageInterval{coverage},
		},
		Risks: manager.ActivityRisksResult{
			Findings: []workloadrisk.Finding{finding},
		},
	}
}

func parityProjectionEvent(
	sequence int,
	kind string,
	payload liveconsole.EventPayload,
) liveconsole.Event {
	return liveconsole.Event{
		Version:    liveconsole.EventVersionV2,
		InstanceID: "daemon_parity", CredentialGeneration: 7,
		Kind: kind, Seq: sequence,
		Entity: liveconsole.EntityRef{
			Kind: kind, ID: "parity", Profile: "default",
			Session: "ses_20260729T140000Z_parity",
		},
		Payload: payload,
	}
}

func parityFactsFromSnapshot(
	t *testing.T,
	snapshot manager.OperatorSnapshot,
	details parityDetails,
) parityFacts {
	t.Helper()
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	projection := snapshot.Profiles[0]
	operation := snapshot.Operations[0]
	coverage := snapshot.Coverage[0]
	risk := snapshot.Risks[0]
	counts := make(map[string]uint64)
	for _, record := range snapshot.Activity {
		counts[record.Kind] += record.Count
	}
	facts := parityFacts{
		Health: snapshot.StreamHealth.State,
		ReadOnly: snapshot.StreamHealth.State != manager.OperatorHealthLive &&
			snapshot.StreamHealth.State != manager.OperatorHealthIdleLive,
		Profile:             projection.Profile,
		DesiredNetwork:      projection.Desired.Network.Mode,
		DesiredProxyRef:     projection.Desired.Network.ProxySecretRef,
		EffectiveNetwork:    projection.Effective.Network.Mode,
		EffectiveProxyRef:   projection.Effective.Network.ProxySecretRef,
		EffectiveGeneration: projection.Effective.Network.SecretGeneration,
		OperationID:         operation.ID,
		OperationPhase:      operation.Phase,
		OperationResult:     parityOperationResult(operation),
		OperationEvidence:   parityOperationEvidence(operation),
		ActivityCursor:      snapshot.ActivityCursor,
		ActivityCounts:      counts,
		CoverageID:          coverage.ID,
		CoverageState:       coverage.State,
		CoverageReason:      coverage.Reason,
		DroppedEvents:       coverage.DroppedEventCount,
		RiskID:              risk.ID,
		RiskRule:            risk.RuleID,
		RiskSeverity:        risk.Severity,
		RiskConfidence:      risk.Confidence,
		RiskPolicy:          risk.PolicyStatus,
		RiskEvidence:        append([]string(nil), risk.EvidenceRefs...),
	}
	if projection.Transition != nil {
		facts.TransitionPhase = projection.Transition.Phase
		facts.TransitionBlockers = append(
			[]string(nil),
			projection.Transition.Blockers...,
		)
	}
	parityAttachDetailFacts(&facts, details)
	return facts
}

func parityFactsFromState(
	t *testing.T,
	state liveconsole.State,
	details parityDetails,
) parityFacts {
	t.Helper()
	if len(state.Profiles) != 1 || len(state.Operations) != 1 ||
		len(state.Coverage) != 1 || len(state.Risks) != 1 {
		t.Fatalf("incomplete parity state: %+v", state)
	}
	projection := state.Profiles[0]
	operation := state.Operations[0]
	coverage := state.Coverage[0]
	risk := state.Risks[0]
	counts := make(map[string]uint64, len(state.Activity.Counts))
	for _, count := range state.Activity.Counts {
		counts[count.Kind] = count.Count
	}
	facts := parityFacts{
		Health:              state.StreamHealth.State,
		ReadOnly:            state.ReadOnly,
		Profile:             projection.Profile,
		DesiredNetwork:      projection.Desired.Network.Mode,
		DesiredProxyRef:     projection.Desired.Network.ProxySecretRef,
		EffectiveNetwork:    projection.Effective.Network.Mode,
		EffectiveProxyRef:   projection.Effective.Network.ProxySecretRef,
		EffectiveGeneration: projection.Effective.Network.SecretGeneration,
		OperationID:         operation.ID,
		OperationPhase:      operation.Phase,
		OperationResult:     parityOperationResult(operation),
		OperationEvidence:   parityOperationEvidence(operation),
		ActivityCursor:      state.Activity.Cursor,
		ActivityCounts:      counts,
		CoverageID:          coverage.ID,
		CoverageState:       coverage.State,
		CoverageReason:      coverage.Reason,
		DroppedEvents:       coverage.DroppedEventCount,
		RiskID:              risk.ID,
		RiskRule:            risk.RuleID,
		RiskSeverity:        risk.Severity,
		RiskConfidence:      risk.Confidence,
		RiskPolicy:          risk.PolicyStatus,
		RiskEvidence:        append([]string(nil), risk.EvidenceRefs...),
	}
	if projection.Transition != nil {
		facts.TransitionPhase = projection.Transition.Phase
		facts.TransitionBlockers = append(
			[]string(nil),
			projection.Transition.Blockers...,
		)
	}
	parityAttachDetailFacts(&facts, details)
	return facts
}

func parityAttachDetailFacts(facts *parityFacts, details parityDetails) {
	for _, record := range details.Events.Records {
		value := parityRecordFact{
			ID: record.ID, Kind: record.Kind,
			Operation: record.Operation, Count: record.Count,
		}
		if record.Actor != nil {
			value.ExecutionID = record.Actor.ExecutionID
		}
		if subject, ok := record.Subject.(workloadtypes.FileSubject); ok {
			value.Path = subject.Path
		}
		facts.DetailRecords = append(facts.DetailRecords, value)
	}
	sort.Slice(facts.DetailRecords, func(left, right int) bool {
		return facts.DetailRecords[left].ID < facts.DetailRecords[right].ID
	})
	for _, root := range details.Executions.Roots {
		parityAppendExecutionFacts(&facts.DetailExecutions, root)
	}
	sort.Slice(facts.DetailExecutions, func(left, right int) bool {
		return facts.DetailExecutions[left].ID < facts.DetailExecutions[right].ID
	})
	for _, interval := range details.Coverage.Intervals {
		facts.DetailCoverage = append(
			facts.DetailCoverage,
			parityCoverageFact{
				ID: interval.ID, State: interval.State,
				Reason: interval.Reason, Dropped: interval.DroppedEventCount,
			},
		)
	}
	sort.Slice(facts.DetailCoverage, func(left, right int) bool {
		return facts.DetailCoverage[left].ID < facts.DetailCoverage[right].ID
	})
	facts.QueryTruncated = details.Events.QueryTruncated
	facts.NextCursor = details.Events.NextCursor
}

func parityAppendExecutionFacts(
	facts *[]parityExecutionFact,
	node manager.ActivityExecutionNode,
) {
	counts := make(map[string]uint64, len(node.ActivityCounts))
	for kind, count := range node.ActivityCounts {
		counts[kind] = count
	}
	*facts = append(*facts, parityExecutionFact{
		ID:       node.Execution.ID,
		ParentID: node.Execution.ParentExecutionID,
		Counts:   counts,
	})
	for _, child := range node.Children {
		parityAppendExecutionFacts(facts, child)
	}
}

func parityOperationResult(operation manager.Operation) string {
	if operation.Result == nil {
		return ""
	}
	return operation.Result.Status + ":" + operation.Result.Code
}

func parityOperationEvidence(operation manager.Operation) []string {
	var values []string
	for _, effect := range operation.Effects {
		for _, evidence := range effect.Evidence {
			values = append(
				values,
				effect.ID+":"+evidence.Code+":"+evidence.Ref,
			)
		}
	}
	sort.Strings(values)
	return values
}

func assertParityTUIRows(
	t *testing.T,
	state liveconsole.State,
	details parityDetails,
) {
	t.Helper()
	var networkRow *tuirender.ConfigRow
	for _, row := range tuirender.ConfigRows(state) {
		if row.CapabilityID == manager.CapabilityConfigNetworkPosture {
			value := row
			networkRow = &value
			break
		}
	}
	if networkRow == nil ||
		networkRow.Desired != "proxy" ||
		networkRow.Effective != "proxy" ||
		networkRow.Transition != "none" ||
		!networkRow.Editable {
		t.Fatalf("TUI config row lost parity facts: %+v", networkRow)
	}
	operations := tuirender.OperationRows(tuirender.OperationsInput{
		State: state,
	})
	if len(operations) != 1 ||
		operations[0].ID != "op_parity_network_01" ||
		operations[0].Phase != manager.OperationSucceeded {
		t.Fatalf("TUI operation rows lost parity facts: %+v", operations)
	}
	data := tuirender.ActivityData{
		Owner:   details.Summary.Owner,
		Summary: details.Summary, Events: details.Events,
		Executions: details.Executions, Coverage: details.Coverage,
		Risks: details.Risks,
	}
	allRows := tuirender.ActivityRows(tuirender.ActivityInput{
		State: state, SessionID: "ses_20260729T140000Z_parity",
		Tab: tuirender.ActivityTabAll, Data: data, Loaded: true,
	})
	seen := make(map[string]bool)
	for _, row := range allRows {
		seen[row.ID] = true
	}
	for _, id := range []string{
		"act_parity_file_01",
		"act_parity_file_02",
		details.Risks.Findings[0].ID,
	} {
		if !seen[id] {
			t.Fatalf("TUI activity rows omit %s: %+v", id, allRows)
		}
	}
	commandRows := tuirender.ActivityRows(tuirender.ActivityInput{
		State: state, SessionID: "ses_20260729T140000Z_parity",
		Tab: tuirender.ActivityTabCommands, Data: data, Loaded: true,
	})
	if len(commandRows) != 1 ||
		commandRows[0].ID != details.Executions.Roots[0].Execution.ID {
		t.Fatalf("TUI execution detail lost parity facts: %+v", commandRows)
	}
}

func assertParityFacts(
	t *testing.T,
	surface string,
	got parityFacts,
	want parityFacts,
) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf(
			"%s parity mismatch\n got: %s\nwant: %s",
			surface,
			gotJSON,
			wantJSON,
		)
	}
}

func parityBrowserRoundTrip(
	t *testing.T,
	snapshot manager.OperatorSnapshot,
	details parityDetails,
) (manager.OperatorSnapshot, parityDetails) {
	t.Helper()
	wire := struct {
		Snapshot struct {
			Version  string                   `json:"version"`
			Resource string                   `json:"resource"`
			Data     manager.OperatorSnapshot `json:"data"`
			Errors   []string                 `json:"errors"`
		} `json:"snapshot"`
		Details parityDetails `json:"details"`
	}{}
	wire.Snapshot.Version = manager.APIVersion
	wire.Snapshot.Resource = "operator/snapshot"
	wire.Snapshot.Data = snapshot
	wire.Snapshot.Errors = []string{}
	wire.Details = details
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Snapshot struct {
			Version  string                   `json:"version"`
			Resource string                   `json:"resource"`
			Data     manager.OperatorSnapshot `json:"data"`
			Errors   []string                 `json:"errors"`
		} `json:"snapshot"`
		Details parityDetails `json:"details"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Snapshot.Version != manager.APIVersion ||
		decoded.Snapshot.Resource != "operator/snapshot" ||
		len(decoded.Snapshot.Errors) != 0 {
		t.Fatalf("invalid browser snapshot envelope: %+v", decoded.Snapshot)
	}
	return decoded.Snapshot.Data, decoded.Details
}

func parityBrowserEventRoundTrip(
	t *testing.T,
	event liveconsole.Event,
) liveconsole.Event {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded liveconsole.Event
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if err := liveconsole.ValidateEvent(decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
