//go:build hideout_e2e

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	runsession "github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	browserConsoleSessionID = "ses_browser_console_e2e"
	browserConsoleBootID    = "01234567-89ab-cdef-0123-456789abcdef"
	browserConsoleCgroupID  = uint64(4545)
	browserConsolePID       = uint32(4242)
	browserConsoleFilePath  = "/outside/browser-e2e.txt"
	browserConsoleDomain    = "example.test"
	browserConsoleIP        = "203.0.113.10"
	browserConsoleRiskID    = "risk_browser_console_0001"
)

// browserConsoleEvidence is the non-secret identity contract serialized for
// the real-browser proof. Keeping the type private avoids adding test concepts
// to the normal daemon surface.
type browserConsoleEvidence struct {
	SessionID            string `json:"sessionId"`
	EnvironmentID        string `json:"environmentId"`
	BackendIncarnationID string `json:"backendIncarnationId"`
	ExecutionID          string `json:"executionId"`
	FilePath             string `json:"filePath"`
	Domain               string `json:"domain"`
	IP                   string `json:"ip"`
	RiskID               string `json:"riskId"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	RecordCount          int    `json:"recordCount"`
}

// PublishWorkspaceViewEvidence exposes the production workspace-view publisher
// only to the real browser/PTY evidence build. It adds no product endpoint and
// carries no capability authority.
func (d *Daemon) PublishWorkspaceViewEvidence(views []manager.WorkspaceViewSnapshot) {
	if d == nil || d.bus == nil {
		return
	}
	d.bus.publishWorkspaceViews(views)
}

// SeedBrowserConsoleEvidence prepares one exact reusable owner, one live
// session, a parent/child execution tree, all four observation subjects,
// partial coverage, an explainable risk, and enough retained rows to exercise
// the browser's DOM bound. It is deliberately unavailable in normal builds.
func (d *Daemon) SeedBrowserConsoleEvidence(
	environmentID string,
) ([]byte, func() error, error) {
	if d == nil || d.sessions == nil || d.activityStore == nil ||
		d.bus == nil {
		return nil, nil,
			errors.New("browser console fixture requires a running daemon")
	}
	if !strings.HasPrefix(environmentID, "env_") {
		return nil, nil,
			errors.New("browser console fixture environment is invalid")
	}
	incarnationID := "hideout-browser-console:1:" + browserConsoleBootID
	owner, err := workloadtypes.NewReusableOwner(
		environmentID,
		"lima",
		incarnationID,
	)
	if err != nil {
		return nil, nil, err
	}
	base := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
	preparation := backend.ActivityPreparation{
		Owner:                owner,
		SessionID:            browserConsoleSessionID,
		EnvironmentID:        environmentID,
		Backend:              "lima",
		BackendIncarnationID: incarnationID,
		GuestBootID:          browserConsoleBootID,
		ObserverGeneration:   1,
		ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
		Retention:            workloadtypes.DefaultActivityRetentionPolicy(),
	}
	expectation, err := d.sessions.activity.Prepare(preparation)
	if err != nil {
		return nil, nil, err
	}
	defer expectation.ObserverStreamToken.Destroy()
	coverageSummaries := []sessionwire.SupervisorCoverageSummary{
		{
			Subsystem: workloadtypes.SubsystemProcess,
			State:     workloadtypes.CoverageAvailable,
			Reason:    "collector-ready",
			Evidence:  []string{"tracepoint.exec"},
		},
		{
			Subsystem: workloadtypes.SubsystemFile,
			State:     workloadtypes.CoverageAvailable,
			Reason:    "collector-ready",
			Evidence:  []string{"fentry.vfs"},
		},
		{
			Subsystem: workloadtypes.SubsystemNetwork,
			State:     workloadtypes.CoverageAvailable,
			Reason:    "collector-ready",
			Evidence:  []string{"cgroup.connect4"},
		},
		{
			Subsystem: workloadtypes.SubsystemDNS,
			State:     workloadtypes.CoveragePartial,
			Reason:    "encrypted-dns-unobserved",
			Evidence:  []string{"resolver-boundary"},
		},
	}
	if err := d.sessions.activity.RegisterBoundary(
		browserConsoleSessionID,
		&sessionwire.SupervisorActivityReady{
			Boundary: workloadtypes.WorkloadBoundary{
				Schema:    workloadtypes.WorkloadBoundarySchema,
				Owner:     owner,
				SessionID: browserConsoleSessionID,
				CgroupPath: "/sys/fs/cgroup/hideout/" +
					browserConsoleSessionID,
				CgroupID:           browserConsoleCgroupID,
				TargetUser:         "developer",
				State:              workloadtypes.BoundaryReady,
				ObserverGeneration: 1,
				GuestBootID:        browserConsoleBootID,
				CreatedAtMonoNS:    100,
			},
			ObserverHelperDigest: preparation.ObserverHelperDigest,
			Coverage:             coverageSummaries,
		},
	); err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	worker, err := d.sessions.register("conn_browser_console_e2e", cancel)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	go func() {
		<-ctx.Done()
		d.sessions.finish("conn_browser_console_e2e", "")
	}()
	if err := worker.markStarted(sessionStart{
		SessionID:         browserConsoleSessionID,
		EnvironmentID:     environmentID,
		Profile:           "default",
		Backend:           "lima",
		TerminalMode:      "interactive",
		SessionSnapshotID: "sha256:" + strings.Repeat("b", 64),
		CommandClass:      "ai-cli",
	}); err != nil {
		cancel()
		return nil, nil, err
	}

	executions, executionID, err := browserConsoleExecutions(owner, base)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := d.activityStore.AppendExecutions(ctx, executions); err != nil {
		cancel()
		return nil, nil, err
	}
	coverage := browserConsoleCoverage(owner, base)
	for _, interval := range coverage {
		if err := d.activityStore.AppendCoverage(ctx, interval); err != nil {
			cancel()
			return nil, nil, err
		}
	}
	records := browserConsoleRecords(owner, executionID, base)
	if err := d.activityStore.AppendActivities(ctx, records); err != nil {
		cancel()
		return nil, nil, err
	}
	finding := workloadrisk.Finding{
		Schema:            workloadrisk.FindingSchema,
		ID:                browserConsoleRiskID,
		Owner:             owner,
		SessionID:         browserConsoleSessionID,
		CoverageID:        "cov_browser_file_0001",
		RuleSetVersion:    "v1",
		RuleID:            "file.write-outside-workspace",
		RuleVersion:       "v1",
		Severity:          workloadrisk.SeverityHigh,
		Confidence:        workloadrisk.ConfidenceExact,
		PolicyStatus:      workloadrisk.PolicyNotEvaluated,
		PolicyDisposition: workloadrisk.PolicyDispositionNotEvaluated,
		Title:             "File changed outside the workspace",
		Explanation: "The browser fixture observed an exact write in a " +
			"resolved external path.",
		NextAction:   "activity.files",
		Count:        1,
		EvidenceRefs: []string{"act_browser_file_write"},
		FirstAt:      base.Add(227 * time.Millisecond),
		LastAt:       base.Add(227 * time.Millisecond),
	}
	if err := d.activityStore.AppendRisk(ctx, finding); err != nil {
		cancel()
		return nil, nil, err
	}
	evidence := browserConsoleEvidence{
		SessionID:            browserConsoleSessionID,
		EnvironmentID:        environmentID,
		BackendIncarnationID: incarnationID,
		ExecutionID:          executionID,
		FilePath:             browserConsoleFilePath,
		Domain:               browserConsoleDomain,
		IP:                   browserConsoleIP,
		RiskID:               browserConsoleRiskID,
		From: base.Add(-5 * time.Second).
			Truncate(time.Second).Format(time.RFC3339),
		To: base.Add(5 * time.Second).
			Truncate(time.Second).Format(time.RFC3339),
		RecordCount: len(records),
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	environmentStore := environment.Store{Root: d.store.Root}
	if err := environmentStore.PrepareRuntimeRoot(environmentID); err != nil {
		cancel()
		return nil, nil, err
	}
	sessionOwner, err := runsession.AcquireOwner(
		environmentStore.OwnerRoot(environmentID),
		runsession.OwnerRecord{
			Schema:            runsession.ActiveSessionSchema,
			SessionID:         browserConsoleSessionID,
			EnvironmentID:     environmentID,
			Profile:           "default",
			Backend:           "lima",
			WorkspaceID:       "wrk_" + strings.Repeat("c", 64),
			SessionSnapshotID: preparation.ObserverHelperDigest,
			State:             runsession.OwnerStateRunning,
			TerminalMode:      runsession.TerminalPTY,
			StartedAt:         base,
			UpdatedAt:         base,
			CommandClass:      "ai-cli",
		},
	)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	cleanup := func() error {
		cancel()
		var waitErr error
		select {
		case <-worker.done:
		case <-time.After(time.Second):
			waitErr = errors.New(
				"browser console session worker did not stop",
			)
		}
		return errors.Join(waitErr, sessionOwner.Close())
	}
	return encoded, cleanup, nil
}

// PublishBrowserConsoleLiveRecord appends a retained record before publishing
// only its bounded activity delta, matching the production detail-refresh path.
func (d *Daemon) PublishBrowserConsoleLiveRecord() error {
	if d == nil || d.activityStore == nil || d.bus == nil {
		return errors.New("browser console live fixture is unavailable")
	}
	snapshot, ok := d.sessions.activity.Snapshot(browserConsoleSessionID)
	if !ok {
		return errors.New("browser console activity owner is unavailable")
	}
	executionID, err := browserConsoleExecutionID(snapshot.Owner)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	record := browserConsoleRecord(
		snapshot.Owner,
		executionID,
		"act_browser_live_update",
		workloadtypes.ActivityFile,
		"write",
		workloadtypes.FileSubject{
			Kind:      workloadtypes.ActivityFile,
			Path:      "/workspace/live-update.txt",
			PathState: "resolved",
			PathClass: "workspace",
			FileType:  "regular",
		},
		"cov_browser_file_0001",
		999,
		now,
	)
	if err := d.activityStore.Append(context.Background(), record); err != nil {
		return err
	}
	return d.bus.publishActivityProjection(
		"default",
		browserConsoleSessionID,
		liveconsole.ActivityProjectionDelta{
			Counts: []liveconsole.ActivityCount{{
				Kind: workloadtypes.ActivityFile, Count: 1,
			}},
			Appended: 1,
			LastAt:   now,
		},
	)
}

// PublishBrowserConsoleSequenceGap advances one sequence without delivery and
// then emits a valid event. It models transport loss at the browser boundary.
func (d *Daemon) PublishBrowserConsoleSequenceGap() error {
	if d == nil || d.bus == nil {
		return errors.New("browser console gap fixture is unavailable")
	}
	d.bus.mu.Lock()
	if d.bus.closed {
		d.bus.mu.Unlock()
		return errors.New("browser console event bus is closed")
	}
	d.bus.seq++
	d.bus.mu.Unlock()
	return d.bus.publishCapabilityProjection(liveconsole.CapabilityProjection{
		ID:         "browser.console.gap",
		Status:     workloadtypes.CoveragePartial,
		Provider:   "e2e",
		Reason:     "injected-sequence-gap",
		Mutable:    false,
		ActionRefs: []string{"console.refresh"},
	})
}

// RotateBrowserConsoleCredential rotates the real daemon credential and emits
// a current-generation event so an already-open browser fails closed.
func (d *Daemon) RotateBrowserConsoleCredential() (string, error) {
	if d == nil || d.credentials == nil || d.bus == nil {
		return "", errors.New("browser console credential fixture is unavailable")
	}
	token, err := d.credentials.Rotate()
	if err != nil {
		return "", err
	}
	if err := d.bus.publishCapabilityProjection(
		liveconsole.CapabilityProjection{
			ID:         "browser.console.credential",
			Status:     workloadtypes.CoverageAvailable,
			Provider:   "daemon",
			Reason:     "credential-rotated",
			Mutable:    false,
			ActionRefs: []string{"console.reauthenticate"},
		},
	); err != nil {
		return "", err
	}
	return token, nil
}

func browserConsoleExecutions(
	owner workloadtypes.ActivityOwner,
	base time.Time,
) ([]workloadtypes.Execution, string, error) {
	rootID, err := browserConsoleExecutionID(owner)
	if err != nil {
		return nil, "", err
	}
	childInput := workloadtypes.ExecutionIdentityInput{
		Owner:              owner,
		SessionID:          browserConsoleSessionID,
		GuestBootID:        browserConsoleBootID,
		ObserverGeneration: 1,
		PID:                browserConsolePID + 1,
		ExecSequence:       2,
		StartedAtMonoNS:    2000,
	}
	childID, err := workloadtypes.NewExecutionID(childInput)
	if err != nil {
		return nil, "", err
	}
	identity := workloadtypes.GuestIdentity{
		UID: 1000, GID: 1000, User: "developer", Group: "developer",
	}
	exitCode := 0
	return []workloadtypes.Execution{
		{
			Schema: workloadtypes.ExecutionSchema,
			ID:     rootID,
			Owner:  owner, SessionID: browserConsoleSessionID,
			GuestBootID: browserConsoleBootID, ObserverGeneration: 1,
			PID: browserConsolePID, TID: browserConsolePID,
			ExecSequence: 1, StartedAtMonoNS: 1000,
			StartedAt:  base,
			Executable: "/usr/local/bin/claude",
			Argv:       []string{"claude", "--safe-mode"},
			Cwd:        "/workspace",
			Identity:   identity,
		},
		{
			Schema: workloadtypes.ExecutionSchema,
			ID:     childID,
			Owner:  owner, SessionID: browserConsoleSessionID,
			ParentExecutionID:  rootID,
			GuestBootID:        browserConsoleBootID,
			ObserverGeneration: 1,
			PID:                childInput.PID, TID: childInput.PID,
			ExecSequence:    childInput.ExecSequence,
			StartedAtMonoNS: childInput.StartedAtMonoNS,
			StartedAt:       base.Add(10 * time.Millisecond),
			Executable:      "/usr/bin/curl",
			Argv:            []string{"curl", "https://example.test/health"},
			Cwd:             "/workspace",
			Identity:        identity,
			Exit: &workloadtypes.ExitObservation{
				Code: &exitCode, AtMonoNS: 3000,
				At: base.Add(20 * time.Millisecond),
			},
		},
	}, rootID, nil
}

func browserConsoleExecutionID(
	owner workloadtypes.ActivityOwner,
) (string, error) {
	return workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner:              owner,
		SessionID:          browserConsoleSessionID,
		GuestBootID:        browserConsoleBootID,
		ObserverGeneration: 1,
		PID:                browserConsolePID,
		ExecSequence:       1,
		StartedAtMonoNS:    1000,
	})
}

func browserConsoleCoverage(
	owner workloadtypes.ActivityOwner,
	base time.Time,
) []workloadtypes.CoverageInterval {
	type coverageSpec struct {
		id, subsystem, state, reason, evidence string
		dropped                                uint64
	}
	specs := []coverageSpec{
		{
			"cov_browser_process_01",
			workloadtypes.SubsystemProcess,
			workloadtypes.CoverageAvailable,
			"collector-ready",
			"tracepoint.exec",
			0,
		},
		{
			"cov_browser_file_0001",
			workloadtypes.SubsystemFile,
			workloadtypes.CoverageAvailable,
			"collector-ready",
			"fentry.vfs",
			0,
		},
		{
			"cov_browser_network_01",
			workloadtypes.SubsystemNetwork,
			workloadtypes.CoverageAvailable,
			"collector-ready",
			"cgroup.connect4",
			0,
		},
		{
			"cov_browser_dns_00001",
			workloadtypes.SubsystemDNS,
			workloadtypes.CoveragePartial,
			"encrypted-dns-unobserved",
			"resolver-boundary",
			1,
		},
	}
	result := make([]workloadtypes.CoverageInterval, 0, len(specs))
	for _, spec := range specs {
		result = append(result, workloadtypes.CoverageInterval{
			Schema:              workloadtypes.CoverageIntervalSchema,
			ID:                  spec.id,
			Owner:               owner,
			SessionID:           browserConsoleSessionID,
			Subsystem:           spec.subsystem,
			State:               spec.state,
			Reason:              spec.reason,
			CollectorGeneration: 1,
			DroppedEventCount:   spec.dropped,
			Evidence: []workloadtypes.CoverageEvidence{{
				Code: spec.evidence,
			}},
			StartSequence: 1,
			StartedAt:     base,
		})
	}
	return result
}

func browserConsoleRecords(
	owner workloadtypes.ActivityOwner,
	executionID string,
	base time.Time,
) []workloadtypes.ActivityRecord {
	records := make([]workloadtypes.ActivityRecord, 0, 229)
	for index := 0; index < 225; index++ {
		at := base.Add(time.Duration(index) * time.Millisecond)
		records = append(records, browserConsoleRecord(
			owner,
			executionID,
			fmt.Sprintf("act_browser_history_%04d", index),
			workloadtypes.ActivityFile,
			"open",
			workloadtypes.FileSubject{
				Kind: workloadtypes.ActivityFile,
				Path: fmt.Sprintf(
					"/workspace/history/file-%03d.txt",
					index,
				),
				PathState: "resolved",
				PathClass: "workspace",
				FileType:  "regular",
			},
			"cov_browser_file_0001",
			uint64(index+1),
			at,
		))
	}
	records = append(records, browserConsoleRecord(
		owner,
		executionID,
		"act_browser_process_exec",
		workloadtypes.ActivityProcess,
		"exec",
		workloadtypes.ProcessSubject{
			Kind:        workloadtypes.ActivityProcess,
			ExecutionID: executionID,
			Executable:  "/usr/local/bin/claude",
			Argv:        []string{"claude", "--safe-mode"},
			Cwd:         "/workspace",
			GuestIdentity: workloadtypes.GuestIdentity{
				UID: 1000, GID: 1000,
				User: "developer", Group: "developer",
			},
		},
		"cov_browser_process_01",
		226,
		base.Add(226*time.Millisecond),
	))
	records = append(records, browserConsoleRecord(
		owner,
		executionID,
		"act_browser_file_write",
		workloadtypes.ActivityFile,
		"write",
		workloadtypes.FileSubject{
			Kind:        workloadtypes.ActivityFile,
			Path:        browserConsoleFilePath,
			PathState:   "resolved",
			PathClass:   "external",
			FileType:    "regular",
			Destructive: true,
		},
		"cov_browser_file_0001",
		227,
		base.Add(227*time.Millisecond),
	))
	records = append(records, browserConsoleRecord(
		owner,
		executionID,
		"act_browser_network_connect",
		workloadtypes.ActivityConnection,
		"connect",
		workloadtypes.NetworkSubject{
			Kind:              workloadtypes.ActivityConnection,
			Protocol:          "tcp",
			IP:                browserConsoleIP,
			Port:              443,
			Domain:            browserConsoleDomain,
			DomainAttribution: workloadtypes.AttributionExact,
			CorrelationReason: "dns-exact",
			Route:             "direct",
			Direction:         "egress",
		},
		"cov_browser_network_01",
		228,
		base.Add(228*time.Millisecond),
	))
	records = append(records, browserConsoleRecord(
		owner,
		executionID,
		"act_browser_dns_query",
		workloadtypes.ActivityDNS,
		"query",
		workloadtypes.DNSSubject{
			Kind:         workloadtypes.ActivityDNS,
			Query:        browserConsoleDomain,
			QueryType:    "A",
			Answers:      []string{browserConsoleIP},
			TTLSeconds:   60,
			ResponseCode: "NOERROR",
			Resolver:     "1.1.1.1",
		},
		"cov_browser_dns_00001",
		229,
		base.Add(229*time.Millisecond),
	))
	return records
}

func browserConsoleRecord(
	owner workloadtypes.ActivityOwner,
	executionID, id, kind, operation string,
	subject any,
	coverageID string,
	sequence uint64,
	at time.Time,
) workloadtypes.ActivityRecord {
	return workloadtypes.ActivityRecord{
		Schema:    workloadtypes.ActivityRecordSchema,
		ID:        id,
		Owner:     owner,
		SessionID: browserConsoleSessionID,
		Actor: &workloadtypes.Actor{
			ExecutionID: executionID,
			PID:         browserConsolePID,
			UID:         1000,
			GID:         1000,
			User:        "developer",
			Group:       "developer",
		},
		Kind: kind, Operation: operation, Subject: subject,
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeSucceeded,
		},
		Count: 1, Bytes: 128,
		FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      coverageID,
		RedactionStatus: workloadtypes.RedactionPassed,
	}
}
