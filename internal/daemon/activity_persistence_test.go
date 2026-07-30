package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workloadobs/coverage"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityCoveragePersistsAcrossServiceAndStoreRestart(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	open := func() *workloadstore.Store {
		activity, err := workloadstore.Open(workloadstore.Options{
			Root: root, ActiveSegmentBytes: 1 << 20,
			PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
			Now: func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("open activity store: %v", err)
		}
		return activity
	}
	persistent := open()
	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 444, allAvailableCoverage()),
	); err != nil {
		t.Fatalf("register boundary: %v", err)
	}
	heartbeat := activityEnvelope(
		preparation, 444, 2, 1, "collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
		t.Fatalf("ingest heartbeat: %v", err)
	}
	gapped := activityEnvelope(
		preparation,
		444,
		2,
		3,
		"collector.heartbeat",
		`{"latestSequence":3,"kernelDropped":0,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, gapped); err != nil {
		t.Fatalf("ingest sequence gap: %v", err)
	}
	provider, err := newDaemonActivityProviderWithStore(
		service,
		persistent,
		"daemon_activityretention",
		"operator-token-activity-retention",
	)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := daemonOperatorObservationWithProvider(
		context.Background(),
		service,
		provider,
		persistent,
		manager.OperatorSnapshotQuery{
			Session: preparation.SessionID, ActivityLimit: 100,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.ActivityRetention) != 1 ||
		!observation.ActivityRetention[0].Owner.Equal(preparation.Owner) ||
		observation.ActivityRetention[0].UsedBytes == 0 ||
		observation.ActivityRetention[0].LimitBytes == 0 ||
		observation.ActivityStoreRetention == nil ||
		observation.ActivityStoreRetention.LimitBytes == 0 ||
		len(observation.Coverage) != len(activitySubsystems)*2 {
		t.Fatalf("operator retention projection=%+v coverage=%d",
			observation.ActivityRetention, len(observation.Coverage))
	}
	if err := persistent.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := open()
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := newActivityService(func() time.Time { return now.Add(time.Minute) })
	restarted.setPersistentStore(reopened)
	source := &daemonActivityQuerySource{
		activity: restarted, persistent: reopened,
	}
	resolved, err := source.ResolveActivityOwner(
		context.Background(),
		manager.ActivityOwnerSelector{
			EnvironmentID:        preparation.Owner.EnvironmentID,
			BackendIncarnationID: preparation.Owner.BackendIncarnationID,
		},
	)
	if err != nil {
		t.Fatalf("resolve persisted owner: %v", err)
	}
	if !resolved.Equal(preparation.Owner) {
		t.Fatalf("resolved owner = %+v", resolved)
	}
	snapshot, err := source.Snapshot(context.Background(), resolved)
	if err != nil {
		t.Fatalf("snapshot persisted owner: %v", err)
	}
	if snapshot.Retention.UsedBytes == 0 || snapshot.Retention.LimitBytes == 0 {
		t.Fatalf("missing persisted retention state: %+v", snapshot.Retention)
	}
	if len(snapshot.Coverage) != len(activitySubsystems)*2 {
		t.Fatalf("persisted coverage intervals = %d, want %d: %#v",
			len(snapshot.Coverage), len(activitySubsystems)*2, snapshot.Coverage)
	}
	current := make(map[string]workloadtypes.CoverageInterval)
	for _, interval := range snapshot.Coverage {
		if interval.EndedAt == nil {
			current[interval.Subsystem] = interval
		}
	}
	for _, subsystem := range activitySubsystems {
		interval, exists := current[subsystem]
		if !exists ||
			interval.State != workloadtypes.CoveragePartial ||
			interval.Reason != coverage.ReasonSequenceGap ||
			interval.DroppedEventCount == 0 {
			t.Fatalf("current %s coverage = %+v, exists=%v",
				subsystem, interval, exists)
		}
	}
}

func TestSessionRegistryDeletesOnlyDisposableActivityAfterCleanTerminal(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root: root, ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	registry := newSessionRegistry(2, func() time.Time { return now })
	registry.activity.setPersistentStore(persistent)
	registry.setActivityCleanup(
		manager.NewActivityCleanupService(
			newDaemonActivityCleanupStore(persistent, registry.activity),
			func() time.Time { return now },
		),
	)
	worker, err := registry.register("conn_disposable", func() {})
	if err != nil {
		t.Fatal(err)
	}

	reusablePreparation := activityPreparationFixture(t)
	targetOwner, err := workloadtypes.NewDisposableOwner(
		"ses_disposable",
		"lima",
		reusablePreparation.Owner.BackendIncarnationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	preparation := reusablePreparation
	preparation.Owner = targetOwner
	preparation.SessionID = targetOwner.SessionID
	preparation.EnvironmentID = "env_disposable"
	if _, err := registry.activity.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := registry.activity.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 444, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	worker.mu.Lock()
	worker.activitySessionID = preparation.SessionID
	worker.mu.Unlock()

	neighbor, err := workloadtypes.NewDisposableOwner(
		"ses_neighbor",
		"lima",
		"neighbor-incarnation",
	)
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := coverage.NewTimeline(
		neighbor, neighbor.SessionID, workloadtypes.SubsystemFile,
	)
	if err != nil {
		t.Fatal(err)
	}
	neighborCoverage, err := timeline.Transition(coverage.Transition{
		State:               workloadtypes.CoverageAvailable,
		Reason:              coverage.ReasonObserverReady,
		CollectorGeneration: 1, At: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := persistent.AppendCoverage(
		context.Background(), neighborCoverage,
	); err != nil {
		t.Fatal(err)
	}
	customAudit := filepath.Join(t.TempDir(), "custom-audit.jsonl")
	if err := os.WriteFile(customAudit, []byte("preserve\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry.finish("conn_disposable", "")
	if _, err := persistent.Snapshot(
		context.Background(), targetOwner,
	); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
		t.Fatalf("terminal disposable owner still exists: %v", err)
	}
	if _, exists := registry.activity.Snapshot(preparation.SessionID); exists {
		t.Fatal("terminal disposable owner remained in daemon memory")
	}
	if _, err := persistent.Snapshot(
		context.Background(), neighbor,
	); err != nil {
		t.Fatalf("neighbor owner was deleted: %v", err)
	}
	data, err := os.ReadFile(customAudit)
	if err != nil || !slices.Equal(data, []byte("preserve\n")) {
		t.Fatalf("custom audit changed: data=%q err=%v", data, err)
	}
}

func TestDaemonActivityCleanupRemovesClosedOwnerFromDiskAndMemory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root: filepath.Join(t.TempDir(), "activity"),
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	activity := newActivityService(func() time.Time { return now })
	activity.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	expectation, err := activity.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	expectation.ObserverStreamToken.Destroy()
	if err := activity.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 445, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	if err := activity.SessionExited(preparation.SessionID, nil, nil); err != nil {
		t.Fatal(err)
	}

	cleanup := manager.NewActivityCleanupService(
		newDaemonActivityCleanupStore(persistent, activity),
		func() time.Time { return now },
	)
	plan, err := cleanup.PlanEnvironment(
		context.Background(),
		preparation.Owner.EnvironmentID,
		manager.ActivityCleanupEnvironmentClean,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Owners) != 1 || !plan.Owners[0].Equal(preparation.Owner) {
		t.Fatalf("cleanup plan=%+v", plan)
	}
	result, err := cleanup.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != manager.ActivityCleanupAbsent {
		t.Fatalf("cleanup result=%+v", result)
	}
	if _, exists := activity.Snapshot(preparation.SessionID); exists {
		t.Fatal("closed activity session remained in daemon memory")
	}
	if _, err := persistent.Snapshot(
		context.Background(),
		preparation.Owner,
	); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
		t.Fatalf("persistent owner remained after cleanup: %v", err)
	}
	source := &daemonActivityQuerySource{
		activity: activity, persistent: persistent,
	}
	if _, err := source.ResolveActivityOwner(
		context.Background(),
		manager.ActivityOwnerSelector{
			EnvironmentID:        preparation.Owner.EnvironmentID,
			BackendIncarnationID: preparation.Owner.BackendIncarnationID,
		},
	); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
		t.Fatalf("cleaned owner remained queryable: %v", err)
	}
}

func TestDaemonActivityCleanupRefusesLiveOwner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 11, 30, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root: filepath.Join(t.TempDir(), "activity"),
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	activity := newActivityService(func() time.Time { return now })
	activity.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	expectation, err := activity.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	expectation.ObserverStreamToken.Destroy()
	if err := activity.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 446, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	cleanup := manager.NewActivityCleanupService(
		newDaemonActivityCleanupStore(persistent, activity),
		func() time.Time { return now },
	)
	plan, err := cleanup.PlanEnvironment(
		context.Background(),
		preparation.Owner.EnvironmentID,
		manager.ActivityCleanupEnvironmentClean,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cleanup.Apply(
		context.Background(),
		plan,
	); !errors.Is(err, errActivityOwnerLive) {
		t.Fatalf("live cleanup error=%v", err)
	}
	if _, exists := activity.Snapshot(preparation.SessionID); !exists {
		t.Fatal("live activity session was forgotten")
	}
	if _, err := persistent.Snapshot(
		context.Background(),
		preparation.Owner,
	); err != nil {
		t.Fatalf("live persistent owner was deleted: %v", err)
	}
}
