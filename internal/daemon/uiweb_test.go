package daemon

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// T032: the daemon serves the WebUI over a loopback UI transport, and that WebUI
// consumes the daemon event stream (its HTML opens an EventSource on
// /daemon/events); the event endpoint accepts the browser's query-param token.
func TestLoopbackUIServesEventConsumingWebUI(t *testing.T) {
	d := startTestDaemon(t)
	if d.UIURL() == "" {
		t.Fatal("daemon did not start a loopback UI transport")
	}
	// Base loopback URL (strip the #token fragment).
	base := d.UIURL()
	if i := strings.Index(base, "/#"); i >= 0 {
		base = base[:i]
	}
	client := &http.Client{Timeout: 2 * time.Second}

	// The WebUI root is served and wires an EventSource to the daemon stream.
	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("GET UI root: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UI root: want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "EventSource") || !strings.Contains(string(body), "/daemon/events") {
		t.Fatalf("WebUI does not consume the daemon event stream (no EventSource on /daemon/events)")
	}
	// Event-triggered scope (FR-009): refresh is driven by events, not a polling
	// timer — the served WebUI has no setInterval.
	if strings.Contains(string(body), "setInterval") {
		t.Fatalf("WebUI must refresh on events, not a polling timer (found setInterval)")
	}

	// The event endpoint accepts a browser query-param token (EventSource cannot set
	// headers) and refuses a wrong one.
	code := getStatus(t, client, base+"/daemon/events?token="+d.Token())
	if code != http.StatusOK {
		t.Fatalf("/daemon/events?token=<valid>: want 200, got %d", code)
	}
	if code := getStatus(t, client, base+"/daemon/events?token=wrong"); code != http.StatusUnauthorized {
		t.Fatalf("/daemon/events?token=<wrong>: want 401, got %d", code)
	}
	// The Manager API is reachable over the loopback transport for the panels.
	if code := getStatusAuthed(t, client, base+"/api/v1/overview", d.Token()); code != http.StatusOK {
		t.Fatalf("/api/v1/overview over loopback: want 200, got %d", code)
	}
}

func getStatus(t *testing.T, client *http.Client, url string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		// SSE connections may be cancelled by the context after headers; a non-nil
		// response still carries the status.
		if resp == nil {
			t.Fatalf("GET %s: %v", url, err)
		}
	}
	code := resp.StatusCode
	_ = resp.Body.Close()
	return code
}

func getStatusAuthed(t *testing.T, client *http.Client, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	code := resp.StatusCode
	_ = resp.Body.Close()
	return code
}

// T033: a client (the TUI) consumes the daemon event stream via SubscribeEvents,
// receiving a refresh signal per event and a closed channel when the stream ends.
func TestSubscribeEventsDeliversRefreshSignals(t *testing.T) {
	d := startTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := SubscribeEvents(ctx, d.store.Root)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // ensure the subscription is attached
	d.bus.OperationEvent("environment", "complete", map[string]any{"action": "clean"})
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering the event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh signal delivered for the event")
	}
	// When the daemon stops, the stream ends and the channel closes.
	_ = d.Stop(context.Background())
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed as expected
			}
		case <-deadline:
			t.Fatal("channel did not close after daemon stop")
		}
	}
}

// SubscribeEvents on a store with no daemon returns an error (client falls back).
func TestSubscribeEventsNoDaemon(t *testing.T) {
	store := testStore(t)
	if _, err := SubscribeEvents(context.Background(), store.Root); err == nil {
		t.Fatal("SubscribeEvents should error when no daemon is running")
	}
}
