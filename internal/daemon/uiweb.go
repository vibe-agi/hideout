package daemon

import (
	"net"
	"net/http"
	"net/url"

	uiwebassets "github.com/vibe-agi/hideout/internal/daemon/uiweb_assets"
)

// startLoopbackUI starts the short-lived tokened loopback UI transport: a
// 127.0.0.1 HTTP server that serves the same WebUI plus the daemon's /api/v1 and
// /daemon/ endpoints, so a browser (which cannot reach a Unix socket) can load the
// panels and open an EventSource on /daemon/events. It is not a trust boundary —
// every request is still operator-token authenticated. Best-effort: a bind failure
// leaves the daemon serving its socket without a browser UI.
func (d *Daemon) startLoopbackUI() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	baseURL := "http://" + ln.Addr().String()
	// The Manager subrouter over loopback needs host/origin allowances for the
	// browser transport; reuse the same Core + token via a copy of the API.
	uiAPI := d.api
	uiAPI.AllowedHosts = []string{ln.Addr().String()}
	uiAPI.AllowedOrigins = []string{baseURL}
	mux := http.NewServeMux()
	mux.Handle(apiPrefix, d.authRecorder(uiAPI.Handler()))
	d.mountDaemonEndpoints(mux)
	mux.Handle(loopbackUIPath, uiwebassets.NewHandler(uiwebassets.Options{
		AllowedHost:   ln.Addr().String(),
		AllowedOrigin: baseURL,
		ExpiresAt:     d.credentials.RotateAt,
		HelpCatalog:   d.helpCatalog,
	}))
	d.uiServer = &http.Server{Handler: loopbackUIRequestGuard{
		allowedHost:   ln.Addr().String(),
		allowedOrigin: baseURL,
		next:          mux,
	}}
	// Keep only the non-secret base URL. UIURL resolves the current credential
	// when the operator asks for a link, so rotation never hands out the stale
	// startup token.
	d.uiURL = baseURL + "/"
	go func() { _ = d.uiServer.Serve(ln) }()
}

// UIURL returns the loopback WebUI URL (empty if the UI transport did not start).
func (d *Daemon) UIURL() string {
	if d == nil || d.uiURL == "" {
		return ""
	}
	return d.uiURL + "#token=" + url.QueryEscape(d.Token())
}

type loopbackUIRequestGuard struct {
	allowedHost   string
	allowedOrigin string
	next          http.Handler
}

func (guard loopbackUIRequestGuard) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Host != guard.allowedHost {
		writeJSON(
			w,
			http.StatusForbidden,
			map[string]string{"error": "host is not allowed"},
		)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" &&
		origin != guard.allowedOrigin {
		writeJSON(
			w,
			http.StatusForbidden,
			map[string]string{"error": "origin is not allowed"},
		)
		return
	}
	guard.next.ServeHTTP(w, r)
}
