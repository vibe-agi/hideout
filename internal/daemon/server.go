package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// authorizeStream authenticates a stream request via the standard operator token
// (Authorization / X-Hideout-UI-Token header) or, for a browser EventSource which
// cannot set headers, a constant-time `?token=` query parameter. Both honor the
// token TTL, so a stream terminates when the credential expires.
func (d *Daemon) authorizeStream(r *http.Request) error {
	if err := d.api.Authorize(r); err == nil {
		return nil
	}
	if tok := r.URL.Query().Get("token"); tok != "" && d.validQueryToken(tok) {
		return nil
	}
	return errors.New("operator token required")
}

func (d *Daemon) validQueryToken(tok string) bool {
	if subtle.ConstantTimeCompare([]byte(tok), []byte(d.token)) != 1 {
		return false
	}
	now := time.Now().UTC()
	if d.api.Now != nil {
		now = d.api.Now().UTC()
	}
	return d.api.ExpiresAt.IsZero() || now.Before(d.api.ExpiresAt)
}

const (
	apiPrefix      = "/api/v1/"
	statusPath     = "/daemon/status"
	stopPath       = "/daemon/stop"
	eventsPath     = "/daemon/events"
	backgroundPath = "/daemon/background"
)

// buildHandler mounts the parity-locked Manager API under /api/v1/ behind an
// auth-refusal recorder, plus the daemon's own status/stop/events endpoints (a
// separate surface outside /api/v1/). Every route requires the operator token.
func (d *Daemon) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(apiPrefix, d.authRecorder(d.api.Handler()))
	d.mountDaemonEndpoints(mux)
	return mux
}

func (d *Daemon) mountDaemonEndpoints(mux *http.ServeMux) {
	for _, endpoint := range DaemonEndpoints() {
		switch endpoint.Path {
		case statusPath:
			mux.Handle(endpoint.Path, d.authRecorder(http.HandlerFunc(d.serveStatus)))
		case stopPath:
			mux.Handle(endpoint.Path, d.authRecorder(http.HandlerFunc(d.serveStop)))
		case eventsPath:
			mux.Handle(endpoint.Path, d.authRecorder(http.HandlerFunc(d.serveEvents)))
		case backgroundPath:
			mux.Handle(endpoint.Path, d.authRecorder(http.HandlerFunc(d.serveBackground)))
		}
	}
}

// serveBackground is the product entry for submitting existing typed environment
// stop/clean apply as background work (FR-010). It authenticates like any request
// and rejects any non-env op class.
func (d *Daemon) serveBackground(w http.ResponseWriter, r *http.Request) {
	if err := d.api.Authorize(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Op  string   `json:"op"`
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	run, err := d.backgroundRun(req.Op, req.IDs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := d.SubmitBackground(req.Op, run)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "op": req.Op, "status": "queued"})
}

// serveEvents streams live events as SSE. It authenticates like any request, then
// fans out events until the client disconnects, the credential expires/rotates
// mid-stream (terminal event, re-subscribe required), or the daemon stops.
func (d *Daemon) serveEvents(w http.ResponseWriter, r *http.Request) {
	if err := d.authorizeStream(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := d.bus.subscribe(64)
	defer d.bus.unsubscribe(sub)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			writeSSE(w, d.bus.terminalEvent(sub, "stream closed"))
			flusher.Flush()
			return
		case <-ticker.C:
			// Mid-stream credential invalidation: the operator token expired, was
			// rotated, or the daemon is stopping — terminate and require a fresh
			// credential to re-subscribe.
			if d.authorizeStream(r) != nil {
				writeSSE(w, d.bus.terminalEvent(sub, "credential invalidated"))
				flusher.Flush()
				return
			}
		case ev := <-sub.ch:
			writeSSE(w, ev)
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// authRecorder wraps a handler so a 401 refusal is recorded in the daemon audit
// log without altering the response or reading any client-supplied token
// material. It observes the response status only.
func (d *Daemon) authRecorder(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusUnauthorized {
			d.audit.record("daemon.auth", "deny", map[string]any{
				"channel": "api",
				"path":    r.URL.Path,
				"reason":  "unauthenticated or invalid operator token",
			})
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Flush forwards to the underlying ResponseWriter so the wrapped streaming
// (SSE) handler can flush events; without it the Flusher interface is hidden.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// serveStatus authenticates with the same operator credential as the Manager
// routes and returns the daemon status. Non-GET methods and bad auth fail closed.
func (d *Daemon) serveStatus(w http.ResponseWriter, r *http.Request) {
	if err := d.api.Authorize(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}
	writeJSON(w, http.StatusOK, d.Status())
}

// serveStop authenticates and triggers an ordered shutdown out of band so the
// response can complete first.
func (d *Daemon) serveStop(w http.ResponseWriter, r *http.Request) {
	if err := d.api.Authorize(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
	go func() { _ = d.Stop(context.Background()) }()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
