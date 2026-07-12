package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
)

func TestSubscribeEventsDeliversTypedEvents(t *testing.T) {
	d := startTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := SubscribeEvents(ctx, d.store.Root)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // ensure the subscription is attached
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

func TestSubscribeEventsNoDaemon(t *testing.T) {
	store := testStore(t)
	if _, err := SubscribeEvents(context.Background(), store.Root); err == nil {
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
