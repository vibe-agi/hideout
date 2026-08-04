package uiweb_assets

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/operatorhelp"
)

func TestHandlerServesTypedAssetsWithStrictBrowserBoundary(t *testing.T) {
	handler := NewHandler(Options{
		AllowedHost:   "127.0.0.1:3210",
		AllowedOrigin: "http://127.0.0.1:3210",
		ExpiresAt: func() time.Time {
			return time.Date(2026, 7, 29, 12, 30, 0, 0, time.UTC)
		},
		HelpCatalog: operatorhelp.Catalog{
			Schema: operatorhelp.CatalogSchema,
			Commands: []operatorhelp.Command{{
				ID: "run", Name: "run", TaskGroup: "Run safely",
				Purpose:       "ASSET CATALOG SENTINEL </div><script>bad()</script>",
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
		},
	})

	root := callAssetHandler(t, handler, http.MethodGet, "/", "", "")
	if root.Code != http.StatusOK ||
		!strings.HasPrefix(root.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(root.Body.String(), "ASSET CATALOG SENTINEL") ||
		!strings.Contains(root.Body.String(), `hideout run -- \u003ccommand\u003e`) ||
		strings.Count(
			root.Body.String(),
			`type="datetime-local" step="1"`,
		) != 2 ||
		strings.Contains(root.Body.String(), `</div><script>bad()</script>`) {
		t.Fatalf("unsafe or incomplete index response: %d %s", root.Code, root.Body)
	}
	csp := root.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "'unsafe-inline'") ||
		!strings.Contains(csp, "script-src 'self'") ||
		!strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("index CSP=%q", csp)
	}
	for _, asset := range []struct {
		path        string
		contentType string
		marker      string
	}{
		{"/assets/style.css", "text/css", ":root"},
		{"/assets/state.js", "text/javascript", "function applyEvent"},
		{"/assets/client.js", "text/javascript", "new EventSource"},
		{"/assets/activity.js", "text/javascript", "function summarize"},
		{"/assets/config.js", "text/javascript", "function createDraft"},
		{"/assets/migration.js", "text/javascript", "function operationView"},
		{"/assets/presentation.js", "text/javascript", "function safeText"},
		{"/assets/app.js", "text/javascript", "function seedLiveConsole"},
	} {
		response := callAssetHandler(
			t,
			handler,
			http.MethodGet,
			asset.path,
			"",
			"",
		)
		if response.Code != http.StatusOK ||
			!strings.HasPrefix(
				response.Header().Get("Content-Type"),
				asset.contentType,
			) ||
			!strings.Contains(response.Body.String(), asset.marker) {
			t.Fatalf("asset %s response=%d %s", asset.path, response.Code, response.Body)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("asset %s is cacheable", asset.path)
		}
	}
}

func TestHandlerRejectsWrongMethodHostOriginAndUnknownAsset(t *testing.T) {
	handler := NewHandler(Options{
		AllowedHost:   "127.0.0.1:3210",
		AllowedOrigin: "http://127.0.0.1:3210",
		HelpCatalog: operatorhelp.Catalog{
			Schema:   operatorhelp.CatalogSchema,
			Commands: []operatorhelp.Command{},
		},
	})
	cases := []struct {
		name   string
		method string
		path   string
		host   string
		origin string
		status int
	}{
		{"method", http.MethodPost, "/", "", "", http.StatusMethodNotAllowed},
		{"host", http.MethodGet, "/", "attacker.invalid", "", http.StatusForbidden},
		{"origin", http.MethodGet, "/", "", "https://attacker.invalid", http.StatusForbidden},
		{"unknown", http.MethodGet, "/assets/unknown.js", "", "", http.StatusNotFound},
		{"traversal", http.MethodGet, "/assets/../index.html", "", "", http.StatusNotFound},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := callAssetHandler(
				t,
				handler,
				test.method,
				test.path,
				test.host,
				test.origin,
			)
			if response.Code != test.status {
				body, _ := io.ReadAll(response.Result().Body)
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, body)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("rejection is cacheable")
			}
		})
	}
}

func callAssetHandler(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	host string,
	origin string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1:3210"+path, nil)
	if host == "" {
		host = "127.0.0.1:3210"
	}
	request.Host = host
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
