package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultUIListenAddr = "127.0.0.1:0"

type LocalServerOptions struct {
	Core       Core
	Addr       string
	Token      string
	TTL        time.Duration
	Now        func() time.Time
	RunBackend RunBackendFactory
	RunOpener  RunOpenerFactory
}

type LocalServer struct {
	URL       string
	UIURL     string
	APIURL    string
	Token     string
	ExpiresAt time.Time

	server   *http.Server
	listener net.Listener
	errc     chan error
}

func StartLocalServer(ctx context.Context, opts LocalServerOptions) (*LocalServer, error) {
	addr := opts.Addr
	if addr == "" {
		addr = DefaultUIListenAddr
	}
	if err := validateLocalListenAddr(addr); err != nil {
		return nil, err
	}
	token := opts.Token
	if token == "" {
		var err error
		token, err = NewUIToken()
		if err != nil {
			return nil, err
		}
	}
	ttl := opts.TTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	expiresAt := now.Add(ttl)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	baseURL := "http://" + ln.Addr().String()
	api := API{
		Core:           opts.Core,
		Token:          token,
		ExpiresAt:      expiresAt,
		AllowedOrigins: []string{baseURL},
		AllowedHosts:   []string{ln.Addr().String()},
		Now:            opts.Now,
		RunBackend:     opts.RunBackend,
		RunOpener:      opts.RunOpener,
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", api.Handler())
	mux.Handle("/", originGuard{
		AllowedOrigins: []string{baseURL},
		Next:           http.HandlerFunc(serveUIRoot),
	})
	server := &http.Server{Handler: hostGuard{
		AllowedHosts: []string{ln.Addr().String()},
		Next:         mux,
	}}
	local := &LocalServer{
		URL:       baseURL,
		UIURL:     baseURL + "/#token=" + url.QueryEscape(token),
		APIURL:    baseURL + "/api/v1/overview",
		Token:     token,
		ExpiresAt: expiresAt,
		server:    server,
		listener:  ln,
		errc:      make(chan error, 1),
	}
	go func() {
		<-ctx.Done()
		_ = local.Close()
	}()
	go func() {
		err := server.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		local.errc <- err
	}()
	return local, nil
}

type hostGuard struct {
	AllowedHosts []string
	Next         http.Handler
}

func (g hostGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := checkAllowedHost(r.Host, g.AllowedHosts); err != nil {
		writeAPIError(w, http.StatusForbidden, err.Error())
		return
	}
	g.Next.ServeHTTP(w, r)
}

type originGuard struct {
	AllowedOrigins []string
	Next           http.Handler
}

func (g originGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" {
		allowed := false
		for _, value := range g.AllowedOrigins {
			if origin == value {
				allowed = true
				break
			}
		}
		if !allowed {
			writeAPIError(w, http.StatusForbidden, "origin is not allowed")
			return
		}
	}
	g.Next.ServeHTTP(w, r)
}

func NewUIToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ui_" + hex.EncodeToString(b[:]), nil
}

func validateLocalListenAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address must be host:port: %w", err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("manager API must bind to 127.0.0.1, got %q", host)
	}
	return nil
}

func serveUIRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if r.URL.Path != "/" {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(uiHTML))
}

func (s *LocalServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}

func (s *LocalServer) Wait() error {
	if s == nil || s.errc == nil {
		return nil
	}
	return <-s.errc
}

const uiHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Hideout</title>
<style>
:root{color-scheme:dark;--bg:#0f1211;--panel:#171c1b;--panel2:#111615;--line:#2c3733;--text:#eef3f0;--muted:#99aaa2;--accent:#78dfb2;--blue:#7db7ff;--warn:#e8c86d;--danger:#f08b8b;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text)}
button{font:inherit}
.shell{min-height:100vh;padding:22px}
main{max-width:1240px;margin:0 auto}
header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;border-bottom:1px solid var(--line);padding-bottom:16px;margin-bottom:14px}
h1{font-size:22px;line-height:1.1;font-weight:720;margin:0;letter-spacing:0}
h2{font-size:15px;margin:0;font-weight:680;letter-spacing:0}
h3{font-size:13px;margin:0 0 8px;font-weight:680;letter-spacing:0}
.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:700;letter-spacing:0;text-transform:uppercase}
.status-row{display:flex;align-items:center;gap:10px;flex-wrap:wrap;justify-content:flex-end}
.status{font-size:12px;color:var(--muted);border:1px solid var(--line);border-radius:999px;padding:6px 10px;background:#121716}
.status.ok{color:var(--accent);border-color:#346952}
.status.error{color:var(--danger);border-color:#774141}
.refresh{height:32px;border:1px solid var(--line);border-radius:6px;background:#19201e;color:var(--text);padding:0 12px;cursor:pointer}
.refresh:hover,.tab:hover{border-color:#52645f}
.tabs{display:flex;gap:6px;overflow:auto;padding-bottom:12px;margin-bottom:14px}
.tab{height:34px;border:1px solid var(--line);border-radius:6px;background:#131817;color:var(--muted);padding:0 11px;white-space:nowrap;cursor:pointer}
.tab.active{background:#1c2723;color:var(--text);border-color:#3f725d}
.summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin-bottom:12px}
.metric{border:1px solid var(--line);border-radius:8px;background:var(--panel);padding:13px 14px;min-height:80px}
.metric .label{font-size:11px;color:var(--muted);text-transform:uppercase;font-weight:700;letter-spacing:0}
.metric .value{font-size:24px;font-weight:760;margin-top:8px;letter-spacing:0}
.metric .sub{font-size:12px;color:var(--muted);margin-top:5px;overflow-wrap:anywhere}
.grid{display:grid;grid-template-columns:minmax(0,1.45fr) minmax(320px,.55fr);gap:12px}
.panel{border:1px solid var(--line);border-radius:8px;background:var(--panel);min-width:0}
.panel-head{display:flex;align-items:center;justify-content:space-between;gap:10px;border-bottom:1px solid var(--line);padding:13px 14px}
.panel-body{padding:10px}
.items{display:grid;gap:8px}
.item{border:1px solid #25312d;border-radius:7px;background:var(--panel2);padding:12px;min-width:0}
.item-top{display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:8px}
.title{font-weight:700;font-size:13px;overflow-wrap:anywhere}
.meta{font-size:12px;color:var(--muted);overflow-wrap:anywhere}
.rows{display:grid;grid-template-columns:minmax(120px,180px) minmax(0,1fr);gap:6px 10px;font-size:12px}
.key{color:var(--muted)}
.val{color:var(--text);overflow-wrap:anywhere}
.pill{display:inline-flex;align-items:center;height:22px;border-radius:999px;border:1px solid var(--line);padding:0 8px;font-size:11px;color:var(--muted);white-space:nowrap}
.pill.ok{color:var(--accent);border-color:#346952}
.pill.warn{color:var(--warn);border-color:#6f6035}
.pill.error{color:var(--danger);border-color:#774141}
.pill.info{color:var(--blue);border-color:#385b82}
.empty,.error-box{border:1px dashed var(--line);border-radius:7px;padding:16px;color:var(--muted);font-size:13px}
.error-box{color:var(--danger);border-color:#774141}
.audit-list{display:grid;gap:8px}
.audit-event{border-left:3px solid #3f725d;padding:8px 10px;background:#121716;border-radius:0 7px 7px 0}
.audit-event.deny,.audit-event.error{border-left-color:#774141}
.audit-event .line{display:flex;gap:8px;align-items:center;flex-wrap:wrap;font-size:12px}
.audit-event pre{margin:7px 0 0;white-space:pre-wrap;overflow:auto;font-size:11px;line-height:1.4;color:#c8d4ce}
@media (max-width:900px){.summary{grid-template-columns:repeat(2,minmax(0,1fr))}.grid{grid-template-columns:1fr}header{align-items:stretch;flex-direction:column}.status-row{justify-content:flex-start}}
@media (max-width:560px){.shell{padding:14px}.summary{grid-template-columns:1fr}.rows{grid-template-columns:1fr}.tabs{padding-bottom:8px}}
</style>
</head>
<body>
<div class="shell">
<main>
<header>
  <div>
    <p class="eyebrow">Private operations console</p>
    <h1>Hideout</h1>
  </div>
  <div class="status-row">
    <span class="status" id="status">connecting</span>
    <button class="refresh" id="refresh" type="button">Refresh</button>
  </div>
</header>
<nav class="tabs" id="tabs" aria-label="Hideout domains">
  <button class="tab active" type="button" data-panel="overview">Overview</button>
  <button class="tab" type="button" data-panel="profiles">Profiles</button>
  <button class="tab" type="button" data-panel="sessions">Sessions</button>
  <button class="tab" type="button" data-panel="capabilities">Capabilities</button>
  <button class="tab" type="button" data-panel="broker">Broker</button>
  <button class="tab" type="button" data-panel="network">Network</button>
  <button class="tab" type="button" data-panel="backends">Backends</button>
  <button class="tab" type="button" data-panel="secrets">Secrets</button>
  <button class="tab" type="button" data-panel="audit">Audit</button>
  <button class="tab" type="button" data-panel="settings">Settings</button>
</nav>
<section class="summary" id="summary"></section>
<section class="grid">
  <section class="panel">
    <div class="panel-head"><h2 id="panelTitle">Overview</h2><span class="pill info" id="panelMeta">manager</span></div>
    <div class="panel-body" id="panelBody"></div>
  </section>
  <section class="panel">
    <div class="panel-head"><h2>Audit Tail</h2><span class="pill" id="auditMeta">0 events</span></div>
    <div class="panel-body" id="auditBody"></div>
  </section>
</section>
</main>
</div>
<script>
const params = new URLSearchParams(location.hash.slice(1));
const token = params.get("token") || "";
if (token && window.history && history.replaceState) {
  history.replaceState(null, document.title, location.pathname + location.search);
}
const statusEl = document.getElementById("status");
const summaryEl = document.getElementById("summary");
const panelTitleEl = document.getElementById("panelTitle");
const panelMetaEl = document.getElementById("panelMeta");
const panelBodyEl = document.getElementById("panelBody");
const auditBodyEl = document.getElementById("auditBody");
const auditMetaEl = document.getElementById("auditMeta");
let activePanel = "overview";
let overview = null;
let auditEvents = [];

function esc(value) {
  return String(value == null ? "" : value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
function list(value) {
  if (Array.isArray(value)) return value.length ? value.join(", ") : "none";
  if (value == null || value === "") return "none";
  return String(value);
}
function pill(label, tone) {
  return '<span class="pill ' + esc(tone || "") + '">' + esc(label) + "</span>";
}
function rows(items) {
  return '<div class="rows">' + items.map(function(item) {
    return '<div class="key">' + esc(item[0]) + '</div><div class="val">' + esc(list(item[1])) + '</div>';
  }).join("") + "</div>";
}
function item(title, meta, rowItems, tone) {
  return '<article class="item"><div class="item-top"><div><div class="title">' + esc(title) + '</div><div class="meta">' + esc(meta || "") + '</div></div>' + pill(tone || "ok", tone || "ok") + '</div>' + rows(rowItems) + "</article>";
}
function empty(label) {
  return '<div class="empty">' + esc(label) + '</div>';
}
function metric(label, value, sub) {
  return '<article class="metric"><div class="label">' + esc(label) + '</div><div class="value">' + esc(value) + '</div><div class="sub">' + esc(sub || "") + '</div></article>';
}
function api(path) {
  return fetch("/api/v1/" + path, {headers: {"X-Hideout-UI-Token": token}}).then(async function(response) {
    const text = await response.text();
    let body = {};
    try { body = JSON.parse(text); } catch { body = {errors: [text]}; }
    if (!response.ok) throw new Error((body.errors || [response.statusText]).join("; "));
    return body;
  });
}
function renderSummary() {
  const profiles = overview.profiles || [];
  const sessions = overview.sessions || [];
  const backends = overview.backends || [];
  const available = backends.filter(function(b) { return b.available; }).length;
  const networkModes = (overview.network && overview.network.profileDefaults || []).map(function(n) { return n.mode; });
  summaryEl.innerHTML = [
    metric("Profiles", profiles.length, profiles.map(function(p) { return p.name; }).join(", ")),
    metric("Sessions", sessions.length, sessions.filter(function(s) { return s.hasAudit; }).length + " with audit"),
    metric("Backends", available + "/" + backends.length, backends.map(function(b) { return b.name; }).join(", ")),
    metric("Network", networkModes.length || "direct", networkModes.join(", ") || "direct")
  ].join("");
}
function renderPanel() {
  const title = activePanel.charAt(0).toUpperCase() + activePanel.slice(1);
  panelTitleEl.textContent = title;
  panelMetaEl.textContent = domainOwner(activePanel);
  const renderer = renderers[activePanel] || renderers.overview;
  panelBodyEl.innerHTML = renderer();
}
function domainOwner(name) {
  return {
    overview: "manager",
    profiles: "profile",
    sessions: "manager/backend",
    capabilities: "policy/cmdproxy",
    broker: "broker",
    network: "network/secrets",
    backends: "backend",
    secrets: "secrets",
    audit: "audit",
    settings: "manager"
  }[name] || "manager";
}
const renderers = {
  overview: function() {
    const s = overview.settings || {};
    const c = overview.capabilities || {};
    return '<div class="items">' + [
      item("Manager", overview.version || "hideout.manager/v1", [["storageRoot", overview.storageRoot], ["storeRoot", s.storeRoot], ["maxCapabilities", c.maxCapabilities || []]], "ok"),
      item("Broker", "host boundary", [["actions", overview.broker && overview.broker.actions], ["commandProxies", overview.broker && overview.broker.commandProxies]], "info"),
      item("Audit", "redacted JSONL", [["sessionAuditFiles", overview.audit && overview.audit.sessionAuditFiles], ["eventsLoaded", auditEvents.length]], "ok")
    ].join("") + "</div>";
  },
  profiles: function() {
    const profiles = overview.profiles || [];
    if (!profiles.length) return empty("No profiles");
    return '<div class="items">' + profiles.map(function(p) {
      const tone = p.validationError ? "error" : "ok";
      return item(p.name || "invalid", p.validationError || p.lineageMode || "profile", [["profileId", p.profileId], ["identityId", p.identityId], ["previousIdentityId", p.previousIdentityId], ["networkMode", p.networkMode], ["proxySecretRef", p.proxySecretRef], ["toolPresets", p.toolPresets], ["commandProxies", p.commandProxies]], tone);
    }).join("") + "</div>";
  },
  sessions: function() {
    const sessions = overview.sessions || [];
    if (!sessions.length) return empty("No sessions");
    return '<div class="items">' + sessions.map(function(s) {
      return item(s.id, s.profile || "session", [["backend", s.backend], ["networkMode", s.networkMode], ["hasAudit", s.hasAudit], ["hasBrokerEndpoint", s.hasBrokerEndpoint], ["hasNetworkPlan", s.hasNetworkPlan], ["hasProxySecretFile", s.hasProxySecretFile], ["hasEphemeralState", s.hasEphemeralState]], s.hasProxySecretFile ? "warn" : "ok");
    }).join("") + "</div>";
  },
  capabilities: function() {
    const c = overview.capabilities || {};
    const h = c.hostOpen || {};
    const proxies = c.commandProxies || [];
    return '<div class="items">' + item("Capability ceiling", "final validator", [["maxCapabilities", c.maxCapabilities || []]], "ok") + item("host.open", "host broker", [["mode", h.mode], ["allowUrls", h.allowUrls], ["urlScope", h.urlScope], ["localNetworkPolicy", h.localNetworkPolicy], ["allowWorkspaceFiles", h.allowWorkspaceFiles], ["browserProfile", h.browserProfile], ["browserControl", h.browserControl], ["profiles", h.profiles]], h.allowUrls || h.allowWorkspaceFiles ? "info" : "warn") + (proxies.length ? proxies.map(function(p) {
      return item(p.name, p.action || "command proxy", [["route", p.route], ["action", p.action], ["subject", "command:" + p.name]], p.route === "host-broker" ? "info" : "warn");
    }).join("") : empty("No command proxies")) + "</div>";
  },
  broker: function() {
    const b = overview.broker || {};
    return '<div class="items">' + [
      item("Host Broker", "typed host actions", [["actions", b.actions || []], ["commandProxies", b.commandProxies || []]], "info")
    ].join("") + "</div>";
  },
  network: function() {
    const n = overview.network || {};
    const defaults = n.profileDefaults || [];
    if (!defaults.length) return empty("No network profiles");
    return '<div class="items">' + defaults.map(function(row) {
      return item(row.profile || "profile", row.mode || "direct", [["mode", row.mode], ["proxySecretRef", row.proxySecretRef], ["proxyEnvVisible", row.proxyEnvVisible]], row.mode === "tun2socks" ? "warn" : "ok");
    }).join("") + "</div>";
  },
  backends: function() {
    const backends = overview.backends || [];
    if (!backends.length) return empty("No backend checks");
    return '<div class="items">' + backends.map(function(b) {
      return item(b.name, b.isolation || "backend", [["available", b.available], ["message", b.message]], b.available ? "ok" : "error");
    }).join("") + "</div>";
  },
  secrets: function() {
    const secrets = overview.secrets || [];
    if (!secrets.length) return empty("No secret refs");
    return '<div class="items">' + secrets.map(function(s) {
      return item(s.ref, s.available ? "available" : "missing", [["available", s.available], ["source", s.source]], s.available ? "ok" : "warn");
    }).join("") + "</div>";
  },
  audit: function() {
    return renderAuditEvents(true);
  },
  settings: function() {
    const s = overview.settings || {};
    return '<div class="items">' + item("Settings", "local manager", [["storeRoot", s.storeRoot], ["apiVersion", "hideout.manager-api/v1"], ["uiToken", "short-lived fragment token"]], "ok") + "</div>";
  }
};
function renderAuditEvents(full) {
  const events = full ? auditEvents : auditEvents.slice(0, 8);
  if (!events.length) return empty("No audit events");
  return '<div class="audit-list">' + events.map(function(e) {
    const tone = e.decision === "deny" || e.decision === "error" ? "error" : "";
    const details = JSON.stringify(e.details || {}, null, 2);
    return '<article class="audit-event ' + esc(tone) + '"><div class="line">' + pill(e.decision || "event", tone || "ok") + '<strong>' + esc(e.action || "event") + '</strong><span class="meta">' + esc(e.session || "") + '</span><span class="meta">' + esc(e.profile || "") + '</span></div><pre>' + esc(details) + '</pre></article>';
  }).join("") + "</div>";
}
function renderAuditTail() {
  auditMetaEl.textContent = auditEvents.length + " events";
  auditBodyEl.innerHTML = renderAuditEvents(false);
}
function setStatus(text, tone) {
  statusEl.textContent = text;
  statusEl.className = "status " + (tone || "");
}
async function load() {
  setStatus("connecting", "");
  try {
    const overviewResp = await api("overview");
    overview = overviewResp.data || {};
    try {
      const auditResp = await api("audit/events?limit=20");
      auditEvents = Array.isArray(auditResp.data) ? auditResp.data : [];
    } catch {
      auditEvents = [];
    }
    renderSummary();
    renderPanel();
    renderAuditTail();
    setStatus("connected", "ok");
  } catch (error) {
    overview = {profiles: [], sessions: [], backends: [], network: {profileDefaults: []}, capabilities: {}, broker: {}, audit: {}, settings: {}};
    auditEvents = [];
    summaryEl.innerHTML = "";
    panelBodyEl.innerHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
    auditBodyEl.innerHTML = empty("No audit events");
    setStatus("error", "error");
  }
}
document.getElementById("tabs").addEventListener("click", function(event) {
  const button = event.target.closest("button[data-panel]");
  if (!button) return;
  activePanel = button.getAttribute("data-panel");
  document.querySelectorAll(".tab").forEach(function(tab) { tab.classList.toggle("active", tab === button); });
  if (overview) renderPanel();
});
document.getElementById("refresh").addEventListener("click", load);
load();
</script>
</body>
</html>`

func StripUIFragment(value string) string {
	if before, _, ok := strings.Cut(value, "#"); ok {
		return before
	}
	return value
}
