package daemon

import (
	"net"
	"net/http"
	"net/url"

	"github.com/vibe-agi/hideout/internal/manager"
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
	mux.HandleFunc(loopbackUIPath, func(w http.ResponseWriter, r *http.Request) {
		manager.ServeUIRoot(w, r, d.api.ExpiresAt)
	})
	d.uiServer = &http.Server{Handler: mux}
	d.uiURL = baseURL + "/#token=" + url.QueryEscape(d.Token())
	go func() { _ = d.uiServer.Serve(ln) }()
}

// UIURL returns the loopback WebUI URL (empty if the UI transport did not start).
func (d *Daemon) UIURL() string { return d.uiURL }
