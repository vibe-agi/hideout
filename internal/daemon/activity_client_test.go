package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityClientQueriesAuthenticatedManagerWithExactOwner(t *testing.T) {
	d := startTestDaemon(t)
	preparation := activityPreparationFixture(t)
	expectation, err := d.sessions.activity.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	expectation.ObserverStreamToken.Destroy()
	if err := d.sessions.activity.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 444, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	client := NewActivityClient(d.store.Root)
	selector := manager.ActivityOwnerSelector{
		EnvironmentID:        preparation.Owner.EnvironmentID,
		BackendIncarnationID: preparation.Owner.BackendIncarnationID,
	}
	owner, err := client.ResolveActivityOwner(context.Background(), selector)
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Equal(preparation.Owner) {
		t.Fatalf("owner=%+v want=%+v", owner, preparation.Owner)
	}
	summary, err := client.ActivitySummary(
		context.Background(),
		manager.ActivitySummaryQuery{
			Owner: owner, SessionID: preparation.SessionID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Owner.Equal(owner) || len(summary.CurrentCoverage) != 4 {
		t.Fatalf("summary=%+v", summary)
	}
	page, err := client.ActivityEvents(
		context.Background(),
		manager.ActivityEventsQuery{
			Owner: owner, SessionID: preparation.SessionID, Limit: 25,
			Kinds: []string{workloadtypes.ActivityFile},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 0 || len(page.Coverage) != 1 ||
		page.Coverage[0].Subsystem != workloadtypes.SubsystemFile {
		t.Fatalf("events=%+v", page)
	}
	coverage, err := client.ActivityCoverage(
		context.Background(),
		manager.ActivityCoverageQuery{
			Owner: owner, SessionID: preparation.SessionID,
			Subsystems: []string{workloadtypes.SubsystemDNS},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.Current) != 1 ||
		coverage.Current[0].Subsystem != workloadtypes.SubsystemDNS {
		t.Fatalf("coverage=%+v", coverage)
	}
	executions, err := client.ActivityExecutions(
		context.Background(),
		manager.ActivityExecutionsQuery{
			Owner: owner, SessionID: preparation.SessionID, RootsOnly: true,
		},
	)
	if err != nil || len(executions.Roots) != 0 {
		t.Fatalf("executions=%+v err=%v", executions, err)
	}
	risks, err := client.ActivityRisks(
		context.Background(),
		manager.ActivityRisksQuery{
			Owner: owner, SessionID: preparation.SessionID,
			Severities: []string{"high"},
		},
	)
	if err != nil || len(risks.Findings) != 0 {
		t.Fatalf("risks=%+v err=%v", risks, err)
	}

	foreign, err := workloadtypes.NewReusableOwner(
		preparation.Owner.EnvironmentID,
		preparation.Owner.Backend,
		"foreign-incarnation",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ActivitySummary(
		context.Background(),
		manager.ActivitySummaryQuery{Owner: foreign},
	)
	if !errors.Is(err, manager.ErrActivityOwnerNotFound) {
		t.Fatalf("foreign exact owner error=%v", err)
	}
	_, err = client.ActivityEvents(
		context.Background(),
		manager.ActivityEventsQuery{
			Owner: owner, Limit: 25, Cursor: "cur_not-authenticated",
		},
	)
	if !errors.Is(err, manager.ErrActivityCursorInvalid) {
		t.Fatalf("invalid cursor error=%v", err)
	}
	_, err = client.ActivitySummary(
		context.Background(),
		manager.ActivitySummaryQuery{
			Owner: owner, SessionID: "not-a-session",
		},
	)
	if !errors.Is(err, manager.ErrActivityQueryInvalid) {
		t.Fatalf("invalid session scope error=%v", err)
	}
}

func TestDaemonOperatorObservationProjectsCoverageAndHonestCapabilities(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service := newActivityService(func() time.Time { return now })
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	expectation.ObserverStreamToken.Destroy()
	summaries := allAvailableCoverage()
	summaries[1].State = workloadtypes.CoveragePartial
	summaries[1].Reason = "fanotify-fallback"
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 444, summaries),
	); err != nil {
		t.Fatal(err)
	}

	observation, err := daemonOperatorObservation(
		context.Background(),
		service,
		manager.OperatorSnapshotQuery{Session: preparation.SessionID, ActivityLimit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Coverage) != 4 || len(observation.Activity) != 0 ||
		len(observation.Risks) != 0 {
		t.Fatalf("observation=%+v", observation)
	}
	states := make(map[string]string)
	for _, capability := range observation.Capabilities {
		states[capability.ID] = capability.State
	}
	if states["activity.file"] != manager.OperatorCapabilityPartial ||
		states["activity.process"] != manager.OperatorCapabilityAvailable {
		t.Fatalf("capability states=%v", states)
	}
}
