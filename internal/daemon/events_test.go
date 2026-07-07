package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func readEventStream(t *testing.T, d *Daemon, token string, timeout time.Duration) []Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", d.Socket())
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+eventsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var events []Event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
			events = append(events, ev)
			if ev.Kind == "terminal" {
				return events
			}
		}
	}
	return events
}

// T022: events are redacted, ordered, and non-durable (a late subscriber sees no
// history).
func TestEventBusRedactsOrdersAndKeepsNoHistory(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe(16)

	bus.OperationEvent("operation", "start", map[string]any{
		"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
		"note":            "keep-me",
	})
	bus.publishAudit("host.open", "allow", map[string]any{"target": "user-url"})

	first := <-sub.ch
	if first.Seq != 1 || first.Kind != "operation" {
		t.Fatalf("unexpected first event: %+v", first)
	}
	if got := first.Payload["capabilityToken"]; got != "REDACTED" && got != nil {
		t.Fatalf("control-plane token not redacted on stream: %v", first.Payload)
	}
	if first.Payload["note"] != "keep-me" {
		t.Fatalf("local user data should be verbatim: %v", first.Payload)
	}
	second := <-sub.ch
	if second.Seq != 2 || second.Kind != "audit" {
		t.Fatalf("events out of order: %+v", second)
	}

	// No history: a late subscriber receives nothing already published.
	late := bus.subscribe(4)
	select {
	case ev := <-late.ch:
		t.Fatalf("late subscriber replayed history: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// T023: a slow subscriber whose bounded buffer fills is dropped with a terminal
// signal rather than stalling the bus.
func TestEventBusBackpressureDropsSlowSubscriber(t *testing.T) {
	bus := newEventBus()
	slow := bus.subscribe(1) // tiny buffer, never drained
	fast := bus.subscribe(64)

	for i := 0; i < 5; i++ {
		bus.publish("operation", "progress", map[string]any{"i": i})
	}

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber should have been terminated")
	}
	// The fast subscriber is unaffected.
	select {
	case ev := <-fast.ch:
		if ev.Kind != "operation" {
			t.Fatalf("fast subscriber got wrong event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("fast subscriber should still receive events")
	}
}

// T024: the subscribe endpoint is a separate surface outside /api/v1/ with the
// same auth.
func TestEventsEndpointSeparateSurfaceAndAuth(t *testing.T) {
	d := startTestDaemon(t)
	if code, _ := daemonDo(t, d, http.MethodGet, eventsPath, ""); code != http.StatusUnauthorized {
		t.Fatalf("/daemon/events without token: want 401, got %d", code)
	}
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/events", d.Token()); code != http.StatusNotFound {
		t.Fatalf("/api/v1/events should not be a Manager route, got %d", code)
	}
}

// T022 (wire): an operation event flows over the SSE endpoint, redacted.
func TestEventsStreamDeliversRedactedEvents(t *testing.T) {
	d := startTestDaemon(t)
	done := make(chan []Event, 1)
	go func() { done <- readEventStream(t, d, d.Token(), 2*time.Second) }()
	time.Sleep(150 * time.Millisecond) // ensure the subscriber is attached
	d.bus.OperationEvent("operation", "start", map[string]any{
		"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
		"note":            "keep-me",
	})
	time.Sleep(150 * time.Millisecond)
	_ = d.Stop(context.Background()) // triggers a terminal event so the reader returns
	events := <-done
	sawOp := false
	for _, ev := range events {
		if ev.Kind == "operation" {
			sawOp = true
			if s, _ := json.Marshal(ev); strings.Contains(string(s), "cap_0123456789abcdef") {
				t.Fatalf("stream leaked control-plane token: %s", s)
			}
		}
	}
	if !sawOp {
		t.Fatalf("did not observe the operation event on the stream: %+v", events)
	}
}

// T026: mid-stream credential invalidation — an active subscription terminates
// when the credential expires; a resubscribe with the stale token is refused.
func TestEventsMidStreamCredentialInvalidation(t *testing.T) {
	store := testStore(t)
	d, err := Start(Options{Store: store, TTL: 400 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })

	events := readEventStream(t, d, d.Token(), 3*time.Second)
	sawTerminal := false
	for _, ev := range events {
		if ev.Kind == "terminal" {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatalf("stream should terminate on credential expiry, got: %+v", events)
	}
	// The now-expired token is refused on resubscribe (and is auditable via T009).
	if code, _ := daemonDo(t, d, http.MethodGet, eventsPath, d.Token()); code != http.StatusUnauthorized {
		t.Fatalf("resubscribe with expired token: want 401, got %d", code)
	}
}
