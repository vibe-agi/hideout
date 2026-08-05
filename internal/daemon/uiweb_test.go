package daemon

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/operatorhelp"
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

	// The WebUI root is a CSP-safe shell; behavior is served as typed, auditable
	// same-origin assets rather than one opaque inline program.
	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("GET UI root: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UI root: want 200, got %d", resp.StatusCode)
	}
	html := string(body)
	for _, want := range []string{
		`src="/assets/state.js"`,
		`src="/assets/client.js"`,
		`src="/assets/activity.js"`,
		`src="/assets/config.js"`,
		`src="/assets/migration.js"`,
		`src="/assets/presentation.js"`,
		`src="/assets/app.js"`,
		`href="/assets/style.css"`,
		`data-panel="overview"`,
		`data-panel="timeline"`,
		`data-panel="executions"`,
		`data-panel="files"`,
		`data-panel="network"`,
		`data-panel="coverage"`,
		`data-panel="risks"`,
		`data-panel="operations"`,
		`data-panel="migration"`,
		`data-panel="config"`,
		`data-panel="help"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("loopback WebUI shell missing %q", want)
		}
	}
	clientJS := getLoopbackUIAsset(t, client, base+"/assets/client.js", "text/javascript")
	stateJS := getLoopbackUIAsset(t, client, base+"/assets/state.js", "text/javascript")
	appJS := getLoopbackUIAsset(t, client, base+"/assets/app.js", "text/javascript")
	for _, want := range []string{
		"new EventSource(",
		`"/daemon/events?token="`,
		`"&since="`,
		"handlers.event(JSON.parse(message.data))",
	} {
		if !strings.Contains(clientJS, want) {
			t.Fatalf("loopback WebUI client missing typed event consumer %q", want)
		}
	}
	for _, want := range []string{
		"function validateSnapshot(input)",
		"function validateEvent(input)",
		"function applyEvent(state, input)",
		`requireReseed(state, "stale", "event sequence gap")`,
		`requireReseed(state, "credential-expired"`,
	} {
		if !strings.Contains(stateJS, want) {
			t.Fatalf("loopback WebUI state reducer missing %q", want)
		}
	}
	// Event-triggered scope (FR-009): refresh is driven by events, not a polling
	// timer — none of the executable assets has a setInterval.
	if strings.Contains(clientJS+stateJS+appJS, "setInterval") {
		t.Fatalf("WebUI must refresh on events, not a polling timer (found setInterval)")
	}
	eventHandler := clientJS[strings.Index(clientJS, "source.onmessage = function(message)"):]
	eventHandler = eventHandler[:strings.Index(eventHandler, "source.onerror = function()")]
	if strings.Contains(eventHandler, "snapshot()") ||
		strings.Contains(eventHandler, "fetch(") ||
		strings.Contains(eventHandler, "/api/v1/") {
		t.Fatalf("loopback WebUI event handler must not re-fetch seed data:\n%s", eventHandler)
	}
	if got := resp.Header.Get("Content-Security-Policy"); strings.Contains(got, "'unsafe-inline'") ||
		!strings.Contains(got, "script-src 'self'") ||
		!strings.Contains(got, "style-src 'self'") {
		t.Fatalf("loopback WebUI CSP permits inline code: %q", got)
	}
	_ = getLoopbackUIAsset(t, client, base+"/assets/style.css", "text/css")

	// The event endpoint accepts a browser query-param token (EventSource cannot set
	// headers) and refuses a wrong one.
	snapshot := browserOperatorSnapshot(t, client, d)
	code := getStatus(
		t,
		client,
		base+"/daemon/events?token="+d.Token()+
			"&since="+strconv.Itoa(snapshot.Sequence),
	)
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

func TestLoopbackUIRejectsForeignHostAndOriginOnDaemonEndpoints(
	t *testing.T,
) {
	d := startTestDaemon(t)
	base := strings.TrimSuffix(strings.Split(d.UIURL(), "#")[0], "/")
	client := &http.Client{Timeout: 2 * time.Second}

	requestStatus := func(host, origin string) int {
		t.Helper()
		request, err := http.NewRequest(
			http.MethodGet,
			base+"/daemon/status",
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			request.Host = host
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		request.Header.Set("Authorization", "Bearer "+d.Token())
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("loopback response is cacheable: %v", response.Header)
		}
		return response.StatusCode
	}

	if status := requestStatus("rebound.invalid", ""); status !=
		http.StatusForbidden {
		t.Fatalf("foreign Host status=%d want 403", status)
	}
	if status := requestStatus("", "http://rebound.invalid"); status !=
		http.StatusForbidden {
		t.Fatalf("foreign Origin status=%d want 403", status)
	}
	if status := requestStatus("", base); status != http.StatusOK {
		t.Fatalf("same-origin daemon status=%d want 200", status)
	}
}

func TestLoopbackUIReceivesRenderOnlyHelpCatalog(t *testing.T) {
	catalog := operatorhelp.Catalog{
		Schema: operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{{
			ID: "run", Name: "run", TaskGroup: "Run safely",
			Purpose:       "DAEMON HELP SENTINEL",
			Syntax:        []string{"hideout run -- <command>"},
			Examples:      []string{"hideout run -- true"},
			Prerequisites: []string{"setup"},
			Effects:       []string{"execute"},
			Safety:        []string{"review"},
			Recovery:      []string{"stop"},
			Next:          []string{"hideout tui"},
			Audience:      operatorhelp.AudienceNewUser,
			Stability:     operatorhelp.StabilityStable,
		}},
	}
	d, err := Start(Options{
		Store:       testStore(t),
		HelpCatalog: catalog,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	base := strings.Split(d.UIURL(), "#")[0]
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base)
	if err != nil {
		t.Fatalf("GET UI root: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UI root: want 200, got %d", resp.StatusCode)
	}
	for _, want := range []string{
		"DAEMON HELP SENTINEL",
		`data-panel="help"`,
		`hideout run -- \u003ccommand\u003e`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("loopback help missing %q", want)
		}
	}
}

func getLoopbackUIAsset(
	t *testing.T,
	client *http.Client,
	endpoint string,
	contentType string,
) string {
	t.Helper()
	resp, err := client.Get(endpoint)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), contentType) ||
		resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"asset %s status=%d content-type=%q cache=%q body=%s",
			endpoint,
			resp.StatusCode,
			resp.Header.Get("Content-Type"),
			resp.Header.Get("Cache-Control"),
			body,
		)
	}
	return string(body)
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
// receiving a typed event per daemon event and a closed channel when the stream
// ends.
func TestSubscribeEventsDeliversTypedRefreshEvents(t *testing.T) {
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
	d.bus.OperationEvent("environment", "complete", map[string]any{"action": "clean"})
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering the event")
		}
		if ev.Kind != "environment" || ev.Payload.ID == "" {
			t.Fatalf("unexpected typed event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no typed refresh event delivered")
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
