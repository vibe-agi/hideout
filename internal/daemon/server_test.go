package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
)

func testStore(t *testing.T) profile.Store {
	t.Helper()
	// Use a short root: Unix socket paths are length-bounded (~104 bytes on macOS)
	// and the default t.TempDir() under $TMPDIR (/var/folders/...) is too long. A
	// short /tmp root mirrors production stores (~/.hideout), which are short.
	root, err := os.MkdirTemp("/tmp", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatalf("seed default profile: %v", err)
	}
	return store
}

func startTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	store := testStore(t)
	d, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	return d
}

func daemonDo(t *testing.T, d *Daemon, method, path, token string) (int, []byte) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", d.Socket())
		},
	}}
	req, err := http.NewRequest(method, "http://localhost"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func daemonPost(t *testing.T, d *Daemon, path, body, token string) (int, []byte) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", d.Socket())
		},
	}}
	req, err := http.NewRequest(http.MethodPost, "http://localhost"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// T009: authentication + unauth audit (no/wrong/expired/valid token).
func TestDaemonAuthRefusesAndAudits(t *testing.T) {
	d := startTestDaemon(t)

	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/overview", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", code)
	}
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/overview", "wrong-token-value"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", code)
	}
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/overview", d.Token()); code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", code)
	}

	// The refusals are recorded in the daemon-local audit log with channel+reason,
	// and no client-supplied token material appears.
	auditData, err := os.ReadFile(filepath.Join(d.RuntimeDir(), auditName))
	if err != nil {
		t.Fatalf("read daemon audit: %v", err)
	}
	text := string(auditData)
	if strings.Contains(text, "wrong-token-value") {
		t.Fatalf("daemon audit leaked supplied token material:\n%s", text)
	}
	denies := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev["action"] == "daemon.auth" && ev["decision"] == "deny" {
			details, _ := ev["details"].(map[string]any)
			if details["channel"] == "api" && details["reason"] != "" {
				denies++
			}
		}
	}
	if denies < 2 {
		t.Fatalf("want >=2 audited auth refusals with channel+reason, got %d\n%s", denies, text)
	}
}

// T009: an expired operator token is refused on a plain request and audited.
func TestDaemonExpiredTokenRefusedAndAudited(t *testing.T) {
	store := testStore(t)
	d, err := Start(Options{Store: store, TTL: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	token := d.Token()
	// Valid immediately.
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/overview", token); code != http.StatusOK {
		t.Fatalf("valid token before expiry: want 200, got %d", code)
	}
	time.Sleep(300 * time.Millisecond) // past the TTL
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/overview", token); code != http.StatusUnauthorized {
		t.Fatalf("expired token: want 401, got %d", code)
	}
	auditData, _ := os.ReadFile(filepath.Join(d.RuntimeDir(), auditName))
	if !strings.Contains(string(auditData), "daemon.auth") {
		t.Fatalf("expired-token refusal not audited:\n%s", auditData)
	}
}

// managerGETRoutes and managerPOSTRoutes are the current 32-route Manager surface
// (16 GET + 16 POST), including the two special-cased GET resources. The drift
// guard asserts the daemon serves exactly this set.
var managerGETRoutes = []string{
	"audit/events", "run/status", "overview", "profiles", "sessions",
	"environments", "backends", "capabilities", "broker", "network",
	"secrets", "audit", "settings", "init", "bundles", "projects",
}

var managerPOSTRoutes = []string{
	"init/plan", "init/apply", "run/plan", "run/apply",
	"environment/stop/plan", "environment/stop/apply",
	"environment/clean/plan", "environment/clean/apply",
	"profile/command-proxy/plan", "profile/command-proxy/apply",
	"profile/hostfs/plan", "profile/hostfs/apply",
	"profile/env/plan", "profile/env/apply",
	"evidence/export/plan", "evidence/export/apply",
}

// T010: Manager parity drift guard — every one of the 32 routes is recognized by
// the daemon (not "unknown manager API resource"); an unknown resource still 404s.
func TestDaemonServesFull32RouteManagerParity(t *testing.T) {
	d := startTestDaemon(t)
	if len(managerGETRoutes)+len(managerPOSTRoutes) != 32 {
		t.Fatalf("route inventory drifted from 32: %d", len(managerGETRoutes)+len(managerPOSTRoutes))
	}
	unknown := "unknown manager API resource"
	for _, r := range managerGETRoutes {
		code, body := daemonDo(t, d, http.MethodGet, "/api/v1/"+r, d.Token())
		if code == http.StatusNotFound && strings.Contains(string(body), unknown) {
			t.Fatalf("GET route %s not served (drift): %s", r, body)
		}
	}
	for _, r := range managerPOSTRoutes {
		code, body := daemonDo(t, d, http.MethodPost, "/api/v1/"+r, d.Token())
		if code == http.StatusNotFound && strings.Contains(string(body), unknown) {
			t.Fatalf("POST route %s not served (drift): %s", r, body)
		}
	}
	// A genuinely unknown Manager resource still 404s as unknown.
	if code, body := daemonDo(t, d, http.MethodGet, "/api/v1/does-not-exist", d.Token()); code != http.StatusNotFound || !strings.Contains(string(body), unknown) {
		t.Fatalf("unknown resource: want 404 unknown, got %d: %s", code, body)
	}
}

// T011: daemon-specific endpoints are a separate surface outside /api/v1/.
func TestDaemonSpecificEndpointsAreSeparateSurface(t *testing.T) {
	d := startTestDaemon(t)
	if code, body := daemonDo(t, d, http.MethodGet, "/daemon/status", d.Token()); code != http.StatusOK {
		t.Fatalf("/daemon/status: want 200, got %d: %s", code, body)
	}
	// It requires the same auth.
	if code, _ := daemonDo(t, d, http.MethodGet, "/daemon/status", ""); code != http.StatusUnauthorized {
		t.Fatalf("/daemon/status without token: want 401, got %d", code)
	}
	// It is NOT a Manager route.
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/daemon/status", d.Token()); code != http.StatusNotFound {
		t.Fatalf("/api/v1/daemon/status should not be a Manager route, got %d", code)
	}
}

// T012: the daemon exposes no confirmation/prompt surface (no daemon-mediated
// prompting); a confirmation route does not exist and serving is headless.
func TestDaemonHasNoPromptSurface(t *testing.T) {
	d := startTestDaemon(t)
	// The daemon's own surface exposes no prompt/confirm endpoint.
	for _, path := range []string{"/daemon/confirm", "/daemon/prompt"} {
		if code, _ := daemonDo(t, d, http.MethodPost, path, d.Token()); code != http.StatusNotFound {
			t.Fatalf("daemon must expose no prompt endpoint; %s returned %d", path, code)
		}
	}
	// The parity-locked Manager surface adds no confirm operation either (it is not
	// a route, so it never succeeds — the daemon never mediates approval).
	if code, _ := daemonDo(t, d, http.MethodPost, "/api/v1/confirm", d.Token()); code == http.StatusOK {
		t.Fatalf("no confirm operation must exist on the Manager surface, got 200")
	}
}

var _ = time.Second
