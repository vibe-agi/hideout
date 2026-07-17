package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const defaultLifecycleReplaySeed int64 = 36036017

func TestCoordinatorPersistedJournalRandomizedReplay(t *testing.T) {
	seed := lifecycleReplaySeed(t)
	t.Logf("lifecycle persisted replay seed=%d", seed)
	random := rand.New(rand.NewSource(seed))
	for scenario := 0; scenario < 32; scenario++ {
		scenarioSeed := random.Int63()
		t.Run(fmt.Sprintf("seed-%d", scenarioSeed), func(t *testing.T) {
			runPersistedReplay(t, scenarioSeed, scenario%8 == 7)
		})
	}
}

func runPersistedReplay(t *testing.T, seed int64, corrupt bool) {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	store := JournalStore{Root: root}
	coordinator := replayCoordinator(t, store, "daemon-replay-0", clock)
	bootID := testBootID
	if err := coordinator.Reconcile(context.Background(), replayReconcileInput(bootID, clock.now)); err != nil {
		t.Fatalf("seed=%d initial reconcile: %v", seed, err)
	}
	registrations := map[string]Registration{}
	for step := 0; step < 24; step++ {
		client := fmt.Sprintf("ses-%d", random.Intn(2))
		switch random.Intn(6) {
		case 0:
			if registrations[client] == nil {
				request := testAttachRequest(backend.LifecycleRunning, bootID)
				request.SessionID = client
				if registration, err := coordinator.BeginAttach(context.Background(), request); err == nil {
					registrations[client] = registration
				}
			}
		case 1:
			if registration := registrations[client]; registration != nil {
				_ = registration.Finish(context.Background(), nil)
				delete(registrations, client)
			}
		case 2:
			clock.advance(time.Duration(random.Intn(int(DefaultIdleGrace/time.Millisecond)+1)) * time.Millisecond)
		case 3:
			_ = coordinator.Close()
			registrations = map[string]Registration{}
			coordinator = replayCoordinator(t, store, fmt.Sprintf("daemon-replay-%d", step+1), clock)
			_ = coordinator.Reconcile(context.Background(), replayReconcileInput(bootID, clock.now))
		case 4:
			if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err == nil && started {
				_ = coordinator.Reconcile(context.Background(), ReconcileInput{
					EnvironmentID: "env-test", InstanceName: "hideout-test",
					Observation: backend.LifecycleObservation{
						State: backend.LifecycleUnknown, InstanceName: "hideout-test",
						ObservedAt: clock.now, ReasonCode: "inventory-unavailable",
					},
				})
			}
		case 5:
			if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err == nil && started {
				_ = coordinator.Reconcile(context.Background(), replayReconcileInput(bootID, clock.now))
			}
		}
		assertReplayJournal(t, store, seed, step)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatalf("seed=%d close: %v", seed, err)
	}
	if !corrupt {
		return
	}
	dir, err := store.environmentDir("env-test", false)
	if err != nil {
		t.Fatalf("seed=%d locate journal: %v", seed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, journalFileName), []byte("{\"schema\":"), 0o600); err != nil {
		t.Fatalf("seed=%d corrupt journal: %v", seed, err)
	}
	replacement := replayCoordinator(t, store, "daemon-replay-corrupt", clock)
	if err := replacement.Reconcile(context.Background(), replayReconcileInput(bootID, clock.now)); err == nil {
		t.Fatalf("seed=%d truncated persisted journal was accepted", seed)
	}
}

func TestCoordinatorRandomizedConcurrentReplay(t *testing.T) {
	seed := lifecycleReplaySeed(t) ^ 0x5eed
	t.Logf("lifecycle concurrent replay seed=%d", seed)
	random := rand.New(rand.NewSource(seed))
	for scenario := 0; scenario < 32; scenario++ {
		scenarioSeed := random.Int63()
		t.Run(fmt.Sprintf("seed-%d", scenarioSeed), func(t *testing.T) {
			runConcurrentReplay(t, scenarioSeed)
		})
	}
}

func runConcurrentReplay(t *testing.T, seed int64) {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	coordinator, clock := newTestCoordinator(t, false, nil)
	if err := coordinator.Reconcile(context.Background(), replayReconcileInput(testBootID, clock.now)); err != nil {
		t.Fatalf("seed=%d initial reconcile: %v", seed, err)
	}
	delays := make([]time.Duration, 5)
	for index := range delays {
		delays[index] = time.Duration(random.Intn(4)) * time.Millisecond
	}
	start := make(chan struct{})
	done := make(chan struct{}, 5)
	var wg sync.WaitGroup
	launch := func(index int, run func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(delays[index])
			run()
			done <- struct{}{}
		}()
	}
	for client := 0; client < 2; client++ {
		client := client
		launch(client, func() {
			request := testAttachRequest(backend.LifecycleRunning, testBootID)
			request.SessionID = fmt.Sprintf("ses-%d", client)
			registration, err := coordinator.BeginAttach(context.Background(), request)
			if err == nil {
				time.Sleep(time.Duration(client+1) * time.Millisecond)
				_ = registration.Finish(context.Background(), nil)
			}
		})
	}
	launch(2, func() { clock.advance(DefaultIdleGrace) })
	launch(3, func() {
		if started, err := coordinator.BeginReconciliation(context.Background(), "env-test"); err == nil && started {
			_ = coordinator.Reconcile(context.Background(), replayReconcileInput("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", replayClockNow(clock)))
		}
	})
	launch(4, func() { _ = coordinator.Close() })
	close(start)
	wait := make(chan struct{})
	go func() {
		wg.Wait()
		close(wait)
	}()
	select {
	case <-wait:
	case <-time.After(2 * time.Second):
		t.Fatalf("seed=%d concurrent lifecycle replay deadlocked (%d/%d completed)", seed, len(done), cap(done))
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed=%d load final journal: %v", seed, err)
	}
	if err == nil {
		if err := journal.Validate(); err != nil {
			t.Fatalf("seed=%d final journal: %v", seed, err)
		}
	}
}

func replayCoordinator(t *testing.T, store JournalStore, daemonID string, clock *testClock) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, DaemonID: daemonID, IdleGrace: DefaultIdleGrace,
		Now:       func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now },
		AfterFunc: clock.after,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func replayReconcileInput(bootID string, observedAt time.Time) ReconcileInput {
	return ReconcileInput{
		EnvironmentID: "env-test", InstanceName: "hideout-test",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-test",
			BootID: bootID, ObservedAt: observedAt,
		},
	}
}

func replayClockNow(clock *testClock) time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func assertReplayJournal(t *testing.T, store JournalStore, seed int64, step int) {
	t.Helper()
	journal, err := store.Load("env-test")
	if err != nil {
		t.Fatalf("seed=%d step=%d load journal: %v", seed, step, err)
	}
	if err := journal.Validate(); err != nil {
		t.Fatalf("seed=%d step=%d validate journal: %v", seed, step, err)
	}
}

func lifecycleReplaySeed(t *testing.T) int64 {
	t.Helper()
	value := os.Getenv("HIDEOUT_LIFECYCLE_REPLAY_SEED")
	if value == "" {
		return defaultLifecycleReplaySeed
	}
	seed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("invalid HIDEOUT_LIFECYCLE_REPLAY_SEED %q: %v", value, err)
	}
	return seed
}
