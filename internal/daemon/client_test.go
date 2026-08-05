package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestSubscribeEventsDeliversTypedEvents(t *testing.T) {
	d := startTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, err := FetchOperatorSnapshot(
		ctx,
		d.store.Root,
		manager.OperatorSnapshotQuery{
			ActivityLimit: manager.DefaultOperatorActivityLimit,
		},
	)
	if err != nil {
		t.Fatalf("FetchOperatorSnapshot: %v", err)
	}
	ch, err := SubscribeEvents(ctx, d.store.Root, snapshot.Sequence)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	d.bus.OperationEvent(liveconsole.KindEnvironment, "complete", map[string]any{
		"id":      "env_live",
		"profile": "default",
		"status":  "running",
	})
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering the event")
		}
		if err := liveconsole.ValidateEvent(ev); err != nil {
			t.Fatalf("invalid typed event: %v", err)
		}
		if ev.Kind != liveconsole.KindEnvironment || ev.Payload.ID != "env_live" || ev.Payload.Profile != "default" {
			t.Fatalf("unexpected typed event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no typed event delivered")
	}
	_ = d.Stop(context.Background())
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close after daemon stop")
		}
	}
}

func TestSubscribeEventsRejectsStaleSnapshotSequence(t *testing.T) {
	d := startTestDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := FetchOperatorSnapshot(
		ctx,
		d.store.Root,
		manager.OperatorSnapshotQuery{
			ActivityLimit: manager.DefaultOperatorActivityLimit,
		},
	)
	if err != nil {
		t.Fatalf("FetchOperatorSnapshot: %v", err)
	}
	d.bus.OperationEvent(liveconsole.KindEnvironment, "complete", map[string]any{
		"id": "env_between_seed_and_stream",
	})
	if _, err := SubscribeEvents(
		ctx,
		d.store.Root,
		snapshot.Sequence,
	); err == nil || !strings.Contains(err.Error(), "409 Conflict") {
		t.Fatalf("stale snapshot stream error=%v want 409 Conflict", err)
	}
	if got := browserSubscriberCount(d); got != 0 {
		t.Fatalf("stale TUI stream registered %d subscribers", got)
	}
}

func TestSubscribeEventsNoDaemon(t *testing.T) {
	store := testStore(t)
	if _, err := SubscribeEvents(context.Background(), store.Root, 0); err == nil {
		t.Fatal("SubscribeEvents should error when no daemon is running")
	}
}

func TestFetchStatusSeedsExistingBackgroundWork(t *testing.T) {
	d := startTestDaemon(t)
	release := make(chan struct{})
	id, err := d.SubmitBackground("environment-stop", func(context.Context) error {
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer close(release)
	status, err := FetchStatus(context.Background(), d.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range status.Background {
		if row.ID == id && (row.Status == "queued" || row.Status == "running") {
			return
		}
	}
	t.Fatalf("background operation missing from status seed: %+v", status.Background)
}

func TestBrowserUIURLUsesDaemonBaseAndCurrentFragmentCredential(t *testing.T) {
	d := startTestDaemon(t)
	status, err := FetchStatus(context.Background(), d.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if status.Transport.BrowserURL == "" ||
		strings.Contains(status.Transport.BrowserURL, "token") ||
		strings.Contains(status.Transport.BrowserURL, "#") {
		t.Fatalf("status browser URL is absent or carries authority: %q", status.Transport.BrowserURL)
	}
	got, err := BrowserUIURL(d.store.Root, status)
	if err != nil {
		t.Fatal(err)
	}
	if got != d.UIURL() {
		t.Fatalf("BrowserUIURL=%q want current daemon URL %q", got, d.UIURL())
	}
}

func TestBrowserUIURLRejectsForeignOrNonLoopbackStatus(t *testing.T) {
	d := startTestDaemon(t)
	status := d.Status()
	for name, mutate := range map[string]func(*Status){
		"missing URL": func(value *Status) { value.Transport.BrowserURL = "" },
		"foreign host": func(value *Status) {
			value.Transport.BrowserURL = "http://example.com:1234/"
		},
		"non-http": func(value *Status) {
			value.Transport.BrowserURL = "https://127.0.0.1:1234/"
		},
		"query authority": func(value *Status) {
			value.Transport.BrowserURL = "http://127.0.0.1:1234/?token=bad"
		},
		"foreign store": func(value *Status) {
			value.Transport.Socket = socketPathFor(t.TempDir())
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := status
			mutate(&candidate)
			if got, err := BrowserUIURL(d.store.Root, candidate); err == nil {
				t.Fatalf("accepted invalid browser status as %q", got)
			}
		})
	}
}

func TestStopRunningWaitsForCompleteShutdownAndAllowsImmediateRestart(t *testing.T) {
	store := testStore(t)
	shutdownEntered := make(chan struct{})
	shutdownRelease := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(shutdownRelease) }) }
	d, err := Start(Options{Store: store, BackendShutdown: func() error {
		close(shutdownEntered)
		<-shutdownRelease
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release()
		_ = d.Stop(context.Background())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- StopRunning(ctx, store.Root) }()
	select {
	case <-shutdownEntered:
	case <-time.After(time.Second):
		t.Fatal("ordered shutdown did not reach the injected backend boundary")
	}
	select {
	case err := <-result:
		t.Fatalf("StopRunning returned before the daemon released ownership: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	release()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StopRunning did not observe completed shutdown")
	}

	restarted, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("immediate restart after successful StopRunning: %v", err)
	}
	if err := restarted.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
