package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestDaemonActivityQueryProviderServesExactOwnerCoverageAndRevision(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service := newActivityService(func() time.Time { return now })
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	expectation.ObserverStreamToken.Destroy()
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 444, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	provider, err := newDaemonActivityProvider(
		service, "daemon_activityquery", "operator-token-fixture",
	)
	if err != nil {
		t.Fatal(err)
	}
	selector := manager.ActivityOwnerSelector{
		EnvironmentID:        preparation.Owner.EnvironmentID,
		BackendIncarnationID: preparation.Owner.BackendIncarnationID,
	}
	owner, err := provider.ResolveActivityOwner(context.Background(), selector)
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Equal(preparation.Owner) {
		t.Fatalf("owner=%+v want=%+v", owner, preparation.Owner)
	}
	summary, err := provider.ActivitySummary(
		context.Background(),
		manager.ActivitySummaryQuery{Owner: owner},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Owner.Equal(owner) || len(summary.CurrentCoverage) != 4 ||
		len(summary.Counts) != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	page, err := provider.ActivityEvents(
		context.Background(),
		manager.ActivityEventsQuery{Owner: owner, Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 0 || len(page.Coverage) != 4 {
		t.Fatalf("events=%+v", page)
	}

	source := &daemonActivityQuerySource{activity: service}
	before, err := source.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(
		preparation.SessionID,
		activityEnvelope(
			preparation, 444, 0, 1, "collector.heartbeat",
			`{"latestSequence":1,"kernelDropped":0,"ringDropped":0}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	after, err := source.Snapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision == after.Revision {
		t.Fatalf("activity revision did not advance: %s", before.Revision)
	}
	if _, err := provider.ResolveActivityOwner(
		context.Background(),
		manager.ActivityOwnerSelector{
			EnvironmentID:        preparation.Owner.EnvironmentID,
			BackendIncarnationID: "foreign-incarnation",
		},
	); !errors.Is(err, manager.ErrActivityOwnerNotFound) {
		t.Fatalf("foreign owner error=%v", err)
	}
}

func TestDaemonActivityQueryResolvesSessionOnlyForDisposableOwner(t *testing.T) {
	service := newActivityService(nil)
	reusable := activityPreparationFixture(t)
	reusableExpectation, err := service.Prepare(reusable)
	if err != nil {
		t.Fatal(err)
	}
	reusableExpectation.ObserverStreamToken.Destroy()
	disposable := reusable
	disposable.SessionID = "ses_20260729T120000Z_disposablequery"
	disposable.Owner, err = workloadtypes.NewDisposableOwner(
		disposable.SessionID,
		disposable.Backend,
		disposable.BackendIncarnationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	disposableExpectation, err := service.Prepare(disposable)
	if err != nil {
		t.Fatal(err)
	}
	disposableExpectation.ObserverStreamToken.Destroy()
	source := &daemonActivityQuerySource{activity: service}

	owner, err := source.ResolveActivityOwner(
		context.Background(),
		manager.ActivityOwnerSelector{SessionID: disposable.SessionID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Equal(disposable.Owner) {
		t.Fatalf("owner=%+v want=%+v", owner, disposable.Owner)
	}
	if _, err := source.ResolveActivityOwner(
		context.Background(),
		manager.ActivityOwnerSelector{SessionID: reusable.SessionID},
	); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
		t.Fatalf("reusable session selector error=%v", err)
	}
}
