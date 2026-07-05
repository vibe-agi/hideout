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
	Core        Core
	Addr        string
	Token       string
	TTL         time.Duration
	Now         func() time.Time
	RunBackend  RunBackendFactory
	RunOpener   RunOpenerFactory
	EnvOperator EnvironmentOperator
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
		EnvOperator:    opts.EnvOperator,
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", api.Handler())
	mux.Handle("/", originGuard{
		AllowedOrigins: []string{baseURL},
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveUIRoot(w, r, expiresAt)
		}),
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

func serveUIRoot(w http.ResponseWriter, r *http.Request, expiresAt time.Time) {
	setUIRootSecurityHeaders(w.Header())
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if r.URL.Path != "/" {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderUIHTML(expiresAt)))
}

func setUIRootSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

func renderUIHTML(expiresAt time.Time) string {
	return strings.ReplaceAll(uiHTML, uiExpiresAtPlaceholder, expiresAt.UTC().Format(time.RFC3339))
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

const uiExpiresAtPlaceholder = "__HIDEOUT_UI_EXPIRES_AT__"

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
.action-row{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:10px}
.action{height:32px;border:1px solid #3f725d;border-radius:6px;background:#183026;color:var(--text);padding:0 12px;cursor:pointer}
.action.secondary{border-color:var(--line);background:#19201e;color:var(--muted)}
.action:disabled{opacity:.55;cursor:not-allowed}
.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}
.field{display:grid;gap:5px}
.field label{font-size:11px;color:var(--muted);font-weight:700;text-transform:uppercase;letter-spacing:0}
.field input,.field select{height:34px;border:1px solid var(--line);border-radius:6px;background:#0f1413;color:var(--text);padding:0 10px;font:inherit;font-size:13px;min-width:0}
.field input:focus,.field select:focus{outline:1px solid #3f725d;border-color:#3f725d}
.check-row{display:flex;gap:8px;align-items:center;color:var(--muted);font-size:12px;padding-top:22px}
.check-row input{width:16px;height:16px;accent-color:#78dfb2}
.result{margin-top:10px;display:grid;gap:8px}
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
.scope-bar{display:flex;gap:10px;align-items:center;justify-content:flex-end;margin:12px 0 0;color:var(--muted);font-size:12px}
.scope-bar select{width:auto;min-width:170px}
@media (max-width:900px){.summary{grid-template-columns:repeat(2,minmax(0,1fr))}.grid{grid-template-columns:1fr}header{align-items:stretch;flex-direction:column}.status-row{justify-content:flex-start}}
@media (max-width:560px){.shell{padding:14px}.summary{grid-template-columns:1fr}.rows{grid-template-columns:1fr}.form-grid{grid-template-columns:1fr}.tabs{padding-bottom:8px}.scope-bar{align-items:stretch;flex-direction:column}.scope-bar select{width:100%}}
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
  <button class="tab" type="button" data-panel="setup">Setup</button>
  <button class="tab" type="button" data-panel="run">Run</button>
  <button class="tab" type="button" data-panel="profiles">Profiles</button>
  <button class="tab" type="button" data-panel="environments">Environments</button>
  <button class="tab" type="button" data-panel="sessions">Sessions</button>
  <button class="tab" type="button" data-panel="capabilities">Capabilities</button>
  <button class="tab" type="button" data-panel="broker">Broker</button>
  <button class="tab" type="button" data-panel="network">Network</button>
  <button class="tab" type="button" data-panel="backends">Backends</button>
  <button class="tab" type="button" data-panel="secrets">Secrets</button>
  <button class="tab" type="button" data-panel="audit">Audit</button>
  <button class="tab" type="button" data-panel="settings">Settings</button>
</nav>
<section class="scope-bar" aria-label="Profile scope">
  <label for="profileScope">Profile scope</label>
  <select id="profileScope"><option value="">All profiles</option></select>
</section>
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
const uiTokenExpiresAt = "__HIDEOUT_UI_EXPIRES_AT__";
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
const profileScopeEl = document.getElementById("profileScope");
let activePanel = "overview";
let overview = null;
let auditEvents = [];
let deniedEvents = [];
let auditExplorerEvents = null;
let setupResultHTML = "";
let runResultHTML = "";
let environmentResultHTML = "";
let commandProxyResultHTML = "";
let selectedProfile = "";
const panelRowLimit = 50;

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
function npmGlobalLabels(value) {
  if (!Array.isArray(value) || !value.length) return [];
  return value.map(function(pkg) {
    const name = pkg && pkg.package ? pkg.package : "npm package";
    const commands = pkg && Array.isArray(pkg.commands) && pkg.commands.length ? " (" + pkg.commands.join(", ") + ")" : "";
    return name + commands;
  });
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
function normalizedNetworkMode(mode) {
  return mode || "direct";
}
function networkRisk(mode) {
  const normalized = normalizedNetworkMode(mode);
  if (normalized === "tun2socks") return "proxy hides origin path, not data egress";
  if (normalized === "direct") return "direct exposes network identity";
  return "";
}
function deniedAuditEvents() {
  return deniedEvents;
}
function freshnessLabel() {
  return "updated " + new Date().toLocaleTimeString();
}
function tokenExpiryLabel() {
  if (!uiTokenExpiresAt) return "";
  const expires = new Date(uiTokenExpiresAt);
  if (Number.isNaN(expires.getTime())) return "";
  return "token expires " + expires.toLocaleTimeString();
}
function panelLimitNotice(label, visible, total) {
  if (visible >= total) return "";
  return '<div class="empty">Showing newest ' + esc(visible) + ' of ' + esc(total) + ' ' + esc(label) + '</div>';
}
function visibleEnvironmentsForPanel(environments) {
  if (!Array.isArray(environments) || environments.length <= panelRowLimit) return environments || [];
  return environments.slice(0, panelRowLimit);
}
function visibleSessionsForPanel(sessions) {
  if (!Array.isArray(sessions) || sessions.length <= panelRowLimit) return sessions || [];
  return sessions.slice(sessions.length - panelRowLimit);
}
function filterByProfile(values, key) {
  if (!selectedProfile || !Array.isArray(values)) return values || [];
  return values.filter(function(value) { return value && value[key] === selectedProfile; });
}
function scopedOverview() {
  const source = overview || {};
  if (!selectedProfile) return source;
  const network = Object.assign({}, source.network || {});
  network.profileDefaults = filterByProfile(network.profileDefaults, "profile");
  return Object.assign({}, source, {
    profiles: filterByProfile(source.profiles, "name"),
    environments: filterByProfile(source.environments, "profile"),
    sessions: filterByProfile(source.sessions, "profile"),
    network: network
  });
}
function withScopedOverview(fn) {
  const source = overview;
  overview = scopedOverview();
  try { return fn(); } finally { overview = source; }
}
function scopedAuditEvents(source) {
  if (!selectedProfile || !Array.isArray(source)) return source || [];
  return source.filter(function(event) { return event && event.profile === selectedProfile; });
}
function syncProfileScopeOptions() {
  const profiles = (overview && overview.profiles) || [];
  const seen = {};
  const options = ['<option value="">All profiles</option>'];
  profiles.forEach(function(profile) {
    if (!profile || !profile.name || seen[profile.name]) return;
    seen[profile.name] = true;
    options.push('<option value="' + esc(profile.name) + '">' + esc(profile.name) + '</option>');
  });
  if (selectedProfile && !seen[selectedProfile]) {
    options.push('<option value="' + esc(selectedProfile) + '">' + esc(selectedProfile) + ' (missing)</option>');
  }
  profileScopeEl.innerHTML = options.join("");
  profileScopeEl.value = selectedProfile;
}
function sessionNextCommands(session) {
  if (!session || !session.id) return [];
  const commands = [];
  if (session.hasAudit) commands.push("audit=hideout audit show --session " + session.id);
  if (session.hasEphemeralState) commands.push("cleanup-check=hideout cleanup --session " + session.id + " --dry-run");
  return commands;
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
function apiPost(path, payload) {
  return fetch("/api/v1/" + path, {method: "POST", headers: {"Content-Type": "application/json", "X-Hideout-UI-Token": token}, body: JSON.stringify(payload)}).then(async function(response) {
    const text = await response.text();
    let body = {};
    try { body = JSON.parse(text); } catch { body = {errors: [text]}; }
    if (!response.ok) throw new Error((body.errors || [response.statusText]).join("; "));
    return body;
  });
}
function renderSummary() {
  return withScopedOverview(function() {
  const profiles = overview.profiles || [];
  const environments = overview.environments || [];
  const sessions = overview.sessions || [];
  const backends = overview.backends || [];
  const available = backends.filter(function(b) { return b.available; }).length;
  const networkModes = (overview.network && overview.network.profileDefaults || []).map(function(n) { return normalizedNetworkMode(n.mode); });
  const denied = deniedAuditEvents();
  summaryEl.innerHTML = [
    metric("Profiles", profiles.length, profiles.map(function(p) { return p.name; }).join(", ")),
    metric("Environments", environments.length, environments.slice(0, 3).map(function(e) { return (e.status || "unknown") + ":" + (e.profile || "-"); }).join(", ")),
    metric("Sessions", sessions.length, sessions.filter(function(s) { return s.hasAudit; }).length + " with audit"),
    metric("Backends", available + "/" + backends.length, backends.map(function(b) { return b.name; }).join(", ")),
    metric("Denied", denied.length, denied.slice(0, 3).map(function(e) { return e.action || "event"; }).join(", ")),
    metric("Network", networkModes.length || "direct", networkModes.join(", ") || "direct")
  ].join("");
  });
}
function renderPanel() {
  return withScopedOverview(function() {
  const title = activePanel.charAt(0).toUpperCase() + activePanel.slice(1);
  panelTitleEl.textContent = title;
  panelMetaEl.textContent = domainOwner(activePanel) + (selectedProfile ? " · " + selectedProfile : "");
  const renderer = renderers[activePanel] || renderers.overview;
  panelBodyEl.innerHTML = renderer();
  if (activePanel === "setup") bindSetupPanel();
  if (activePanel === "run") bindRunPanel();
  if (activePanel === "environments") bindEnvironmentPanel();
  if (activePanel === "capabilities") bindCommandProxyPanel();
  if (activePanel === "audit") bindAuditPanel();
  });
}
function domainOwner(name) {
  return {
    overview: "manager",
    setup: "init/tool-supply",
    run: "manager/backend",
    profiles: "profile",
    environments: "manager/environment",
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
    const init = overview.init || {};
    const initSteps = init.nextSteps || [];
    return '<div class="items">' + [
      item("Manager", overview.version || "hideout.manager/v1", [["storageRoot", overview.storageRoot], ["storeRoot", s.storeRoot], ["maxCapabilities", c.maxCapabilities || []]], "ok"),
      item("Init", init.initialized ? "initialized" : "needs setup", [["profile", init.profile], ["pendingTasks", init.pendingTasks], ["nextSteps", initSteps.map(function(step) { return (step.label || step.id) + ": " + step.command; })]], init.pendingTasks ? "warn" : "ok"),
      item("Broker", "host boundary", [["actions", overview.broker && overview.broker.actions], ["commandProxies", overview.broker && overview.broker.commandProxies]], "info"),
      item("Environments", "reusable guest state", [["count", (overview.environments || []).length], ["running", (overview.environments || []).filter(function(e) { return e.status === "running"; }).length]], "info"),
      item("Audit", "redacted JSONL", [["sessionAuditFiles", overview.audit && overview.audit.sessionAuditFiles], ["eventsLoaded", auditEvents.length]], "ok")
    ].join("") + "</div>";
  },
  setup: function() {
    const profiles = overview.profiles || [];
    const profileOptions = profiles.length ? profiles.map(function(p) {
      return '<option value="' + esc(p.name) + '">' + esc(p.name) + '</option>';
    }).join("") : '<option value="default">default</option>';
    return '<form id="setupForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="setupProfile">Profile</label><select id="setupProfile">' + profileOptions + '</select></div>' +
      '<div class="field"><label for="setupBackend">Backend</label><select id="setupBackend"><option value="lima">lima</option><option value="native">native</option></select></div>' +
      '<div class="field"><label for="setupNetwork">Network</label><select id="setupNetwork"><option value="direct">direct</option><option value="tun2socks">tun2socks</option></select></div>' +
      '<div class="field"><label for="setupProxySecret">Proxy secret ref</label><input id="setupProxySecret" placeholder="default-proxy"></div>' +
      '<div class="field"><label for="setupPreset">Tool preset</label><input id="setupPreset" value="node-dev"></div>' +
      '<div class="field"><label for="setupPackage">NPM package</label><input id="setupPackage" placeholder="@scope/tool@version"></div>' +
      '<div class="field"><label for="setupCommands">NPM commands</label><input id="setupCommands" placeholder="tool, helper"></div>' +
      '</div>' +
      '<div class="action-row"><button class="action secondary" type="button" data-setup-action="plan" data-api="init/plan">Plan</button><button class="action" type="button" data-setup-action="apply" data-api="init/apply">Apply</button><span class="meta" id="setupStatus">ready</span></div>' +
      '</form><div class="result" id="setupResult">' + setupResultHTML + '</div>';
  },
  run: function() {
    const profiles = overview.profiles || [];
    const profileOptions = profiles.length ? profiles.map(function(p) {
      return '<option value="' + esc(p.name) + '">' + esc(p.name) + '</option>';
    }).join("") : '<option value="default">default</option>';
    return '<form id="runForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="runProfile">Profile</label><select id="runProfile">' + profileOptions + '</select></div>' +
      '<div class="field"><label for="runBackend">Backend</label><select id="runBackend"><option value="lima">lima</option><option value="native">native</option></select></div>' +
      '<div class="field"><label for="runNetwork">Network override</label><select id="runNetwork"><option value="">profile default</option><option value="direct">direct</option><option value="tun2socks">tun2socks</option></select></div>' +
      '<div class="field"><label for="runWorkspace">Workspace</label><input id="runWorkspace" placeholder="/absolute/project/path"></div>' +
      '<div class="field"><label for="runCommand">Command argv</label><input id="runCommand" value="pwd" spellcheck="false"></div>' +
      '<label class="check-row"><input id="runAllowWeak" type="checkbox"> allow native weak isolation</label>' +
      '<label class="check-row"><input id="runNewEnv" type="checkbox"> new environment</label>' +
      '<label class="check-row"><input id="runRemoveEnv" type="checkbox"> remove environment after run</label>' +
      '</div>' +
      '<div class="action-row"><button class="action secondary" type="button" data-run-action="plan" data-api="run/plan">Plan</button><button class="action" type="button" data-run-action="apply" data-api="run/apply">Apply</button><span class="meta" id="runStatus">ready</span></div>' +
      '</form><div class="result" id="runResult">' + runResultHTML + '</div>';
  },
  profiles: function() {
    const profiles = overview.profiles || [];
    if (!profiles.length) return empty("No profiles");
    return '<div class="items">' + profiles.map(function(p) {
      const tone = p.validationError ? "error" : "ok";
      return item(p.name || "invalid", p.validationError || p.lineageMode || "profile", [["profileId", p.profileId], ["identityId", p.identityId], ["previousIdentityId", p.previousIdentityId], ["networkMode", p.networkMode], ["proxySecretRef", p.proxySecretRef], ["toolPresets", p.toolPresets], ["npmGlobals", npmGlobalLabels(p.npmGlobals)], ["commandProxies", p.commandProxies]], tone);
    }).join("") + "</div>";
  },
  environments: function() {
    const environments = overview.environments || [];
    const visibleEnvironments = visibleEnvironmentsForPanel(environments);
    const envHTML = environments.length ? panelLimitNotice("environments", visibleEnvironments.length, environments.length) + '<div class="items">' + visibleEnvironments.map(function(e) {
      const tone = e.status === "running" ? "warn" : e.status === "stopped" ? "info" : "ok";
      return item(e.id, e.status || "environment", [["profile", e.profile], ["backend", e.backend], ["instance", e.instanceName], ["workspace", e.workspace], ["guestWorkspace", e.guestWorkspace], ["lastSessionId", e.lastSessionId], ["lastCommand", e.lastCommand], ["lastStartedAt", e.lastStartedAt], ["lastEndedAt", e.lastEndedAt]], tone);
    }).join("") + "</div>" : empty("No reusable environments");
    return '<form id="environmentForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="envAction">Action</label><select id="envAction"><option value="stop">stop</option><option value="clean">clean</option></select></div>' +
      '<div class="field"><label for="envIds">Environment IDs</label><input id="envIds" placeholder="empty = all, or env_..., abc123"></div>' +
      '<div class="field"><label for="envIdle">Idle filter</label><input id="envIdle" placeholder="24h, 30m"></div>' +
      '<label class="check-row"><input id="envStoppedOnly" type="checkbox"> stopped only</label>' +
      '</div>' +
      '<div class="action-row"><button class="action secondary" type="button" data-environment-mode="plan">Plan</button><button class="action" type="button" data-environment-mode="apply">Apply</button><span class="meta" id="environmentStatus">ready</span></div>' +
      '</form><div class="result" id="environmentResult">' + environmentResultHTML + '</div>' + envHTML;
  },
  sessions: function() {
    const sessions = overview.sessions || [];
    if (!sessions.length) return empty("No sessions");
    const visibleSessions = visibleSessionsForPanel(sessions);
    return panelLimitNotice("sessions", visibleSessions.length, sessions.length) + '<div class="items">' + visibleSessions.map(function(s) {
      return item(s.id, s.profile || "session", [["backend", s.backend], ["networkMode", s.networkMode], ["auditPath", s.auditPath], ["hasAudit", s.hasAudit], ["hasBrokerEndpoint", s.hasBrokerEndpoint], ["hasNetworkPlan", s.hasNetworkPlan], ["hasProxySecretFile", s.hasProxySecretFile], ["hasEphemeralState", s.hasEphemeralState], ["next", sessionNextCommands(s)]], s.hasProxySecretFile ? "warn" : "ok");
    }).join("") + "</div>";
  },
  capabilities: function() {
    const c = overview.capabilities || {};
    const h = c.hostOpen || {};
    const proxies = c.commandProxies || [];
    const profiles = overview.profiles || [];
    const profileOptions = profiles.length ? profiles.map(function(p) {
      return '<option value="' + esc(p.name) + '">' + esc(p.name) + '</option>';
    }).join("") : '<option value="default">default</option>';
    const commandForm = '<form id="commandProxyForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="commandProxyProfile">Profile</label><select id="commandProxyProfile">' + profileOptions + '</select></div>' +
      '<div class="field"><label for="commandProxyOperation">Operation</label><select id="commandProxyOperation"><option value="add-open">add host.open symbol</option><option value="remove">remove symbol</option></select></div>' +
      '<div class="field"><label for="commandProxyCommand">Command symbol</label><input id="commandProxyCommand" placeholder="browser-open"></div>' +
      '</div>' +
      '<div class="action-row"><button class="action secondary" type="button" data-command-proxy-action="plan">Plan</button><button class="action" type="button" data-command-proxy-action="apply">Apply</button><span class="meta" id="commandProxyStatus">ready</span></div>' +
      '</form><div class="result" id="commandProxyResult">' + commandProxyResultHTML + '</div>';
    return commandForm + '<div class="items">' + item("Capability ceiling", "final validator", [["maxCapabilities", c.maxCapabilities || []]], "ok") + item("host.open", "host broker", [["mode", h.mode], ["allowUrls", h.allowUrls], ["urlScope", h.urlScope], ["localNetworkPolicy", h.localNetworkPolicy], ["allowWorkspaceFiles", h.allowWorkspaceFiles], ["browserProfile", h.browserProfile], ["browserControl", h.browserControl], ["profiles", h.profiles]], h.allowUrls || h.allowWorkspaceFiles ? "info" : "warn") + (proxies.length ? proxies.map(function(p) {
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
      const mode = normalizedNetworkMode(row.mode);
      return item(row.profile || "profile", mode, [["mode", mode], ["risk", networkRisk(mode)], ["proxySecretRef", row.proxySecretRef], ["proxyEnvVisible", row.proxyEnvVisible]], mode === "tun2socks" ? "warn" : "ok");
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
    return renderAuditExplorer();
  },
  settings: function() {
    const s = overview.settings || {};
    return '<div class="items">' + item("Settings", "local manager", [["storeRoot", s.storeRoot], ["apiVersion", "hideout.manager-api/v1"], ["uiToken", "short-lived fragment token"], ["tokenExpiresAt", uiTokenExpiresAt]], "ok") + "</div>";
  }
};
function splitCSV(value) {
  return String(value || "").split(",").map(function(part) { return part.trim(); }).filter(Boolean);
}
function splitArgv(value) {
  const input = String(value || "");
  const out = [];
  let current = "";
  let quote = "";
  let escaped = false;
  for (let i = 0; i < input.length; i++) {
    const ch = input[i];
    if (escaped) {
      current += ch;
      escaped = false;
      continue;
    }
    if (ch === "\\") {
      escaped = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = "";
      else current += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      continue;
    }
    if (/\s/.test(ch)) {
      if (current) {
        out.push(current);
        current = "";
      }
      continue;
    }
    current += ch;
  }
  if (escaped) current += "\\";
  if (quote) throw new Error("command has an unterminated quote");
  if (current) out.push(current);
  return out;
}
function setupPayloadFromForm() {
  const npmPackage = document.getElementById("setupPackage").value.trim();
  const npmCommands = splitCSV(document.getElementById("setupCommands").value);
  const npmGlobals = npmPackage ? [{package: npmPackage, commands: npmCommands}] : [];
  return {
    profile: document.getElementById("setupProfile").value,
    backend: document.getElementById("setupBackend").value,
    network: document.getElementById("setupNetwork").value,
    proxySecretRef: document.getElementById("setupProxySecret").value.trim(),
    toolPresets: splitCSV(document.getElementById("setupPreset").value),
    npmGlobals: npmGlobals
  };
}
function renderSetupResponse(resource, response) {
  const errors = response.errors || [];
  const data = response.data || {};
  const plan = data.plan || data;
  const tasks = plan.tasks || [];
  const nextSteps = plan.nextSteps || [];
  const applied = data.applied || [];
  const skipped = data.skipped || [];
  const header = item(resource, plan.profile || "init", [["backend", plan.backend], ["network", plan.network], ["applied", applied.length], ["skipped", skipped.length], ["tasks", tasks.length]], errors.length ? "error" : "ok");
  const errorHTML = errors.map(function(err) { return '<div class="error-box">' + esc(err) + '</div>'; }).join("");
  const taskHTML = tasks.length ? '<div class="items">' + tasks.map(function(task) {
    const tone = task.status === "pending" ? "warn" : task.status === "blocked" ? "error" : "ok";
    return item(task.kind, task.message || task.id, [["status", task.status], ["targetScope", task.targetScope], ["risk", task.risk], ["inputs", task.inputs || []], ["outputs", task.outputs || []]], tone);
  }).join("") + '</div>' : empty("No init tasks");
  const nextHTML = nextSteps.length ? '<h3>Next steps</h3><div class="items">' + nextSteps.map(function(step) {
    return item(step.label || step.id || "next", step.message || "", [["command", step.command]], step.id === "resolve-blocked" ? "warn" : "info");
  }).join("") + '</div>' : "";
  return header + errorHTML + taskHTML + nextHTML;
}
function setSetupBusy(busy, text) {
  const status = document.getElementById("setupStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-setup-action]").forEach(function(button) { button.disabled = busy; });
}
function bindSetupPanel() {
  const form = document.getElementById("setupForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  document.querySelectorAll("[data-setup-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-setup-action");
      setSetupBusy(true, action);
      try {
        const response = await apiPost("init/" + action, setupPayloadFromForm());
        setupResultHTML = renderSetupResponse("init/" + action, response);
        document.getElementById("setupResult").innerHTML = setupResultHTML;
        setSetupBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
        if (action === "apply" && !(response.errors && response.errors.length)) await load();
      } catch (error) {
        setupResultHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        document.getElementById("setupResult").innerHTML = setupResultHTML;
        setSetupBusy(false, "error");
      }
    });
  });
}
function runPayloadFromForm() {
  const command = splitArgv(document.getElementById("runCommand").value);
  if (!command.length) throw new Error("command is required");
  const workspace = document.getElementById("runWorkspace").value.trim();
  const networkMode = document.getElementById("runNetwork").value;
  const payload = {
    profile: document.getElementById("runProfile").value,
    backend: document.getElementById("runBackend").value,
    command: command,
    allowWeakIsolation: document.getElementById("runAllowWeak").checked,
    newEnvironment: document.getElementById("runNewEnv").checked,
    removeEnvironment: document.getElementById("runRemoveEnv").checked
  };
  if (workspace) payload.workspace = workspace;
  if (networkMode) payload.networkMode = networkMode;
  return payload;
}
function renderRunResponse(resource, response) {
  const errors = response.errors || [];
  const data = response.data || {};
  const summary = data.boundarySummary || {};
  const header = item(resource, data.profile || "run", [["backend", data.backend], ["workspace", data.workspace], ["guestWorkspace", data.guestWorkspace], ["command", data.command || []], ["sessionId", data.sessionId], ["environmentId", data.environmentId], ["auditPath", data.auditPath], ["error", data.error]], errors.length || data.error ? "error" : "ok");
  const errorHTML = errors.map(function(err) { return '<div class="error-box">' + esc(err) + '</div>'; }).join("");
  const boundary = data.boundarySummary ? item("Boundary Summary", summary.evidence || "audit", [["host.open", summary.hostOpen ? JSON.stringify(summary.hostOpen) : ""], ["hostfs", summary.hostfs ? JSON.stringify(summary.hostfs) : ""], ["portbridge", summary.portbridge ? JSON.stringify(summary.portbridge) : ""]], "info") : "";
  return header + errorHTML + boundary;
}
function setRunBusy(busy, text) {
  const status = document.getElementById("runStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-run-action]").forEach(function(button) { button.disabled = busy; });
}
function bindRunPanel() {
  const form = document.getElementById("runForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  document.querySelectorAll("[data-run-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-run-action");
      setRunBusy(true, action);
      try {
        const response = await apiPost("run/" + action, runPayloadFromForm());
        runResultHTML = renderRunResponse("run/" + action, response);
        document.getElementById("runResult").innerHTML = runResultHTML;
        setRunBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
        if (action === "apply") await load();
      } catch (error) {
        runResultHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        document.getElementById("runResult").innerHTML = runResultHTML;
        setRunBusy(false, "error");
      }
    });
  });
}
function environmentPayloadFromForm() {
  const action = document.getElementById("envAction").value;
  const payload = {
    ids: splitCSV(document.getElementById("envIds").value),
    stoppedOnly: action === "clean" && document.getElementById("envStoppedOnly").checked
  };
  const idle = document.getElementById("envIdle").value.trim();
  if (idle) payload.idle = idle;
  return payload;
}
function renderEnvironmentTargets(title, targets, tone) {
  if (!Array.isArray(targets) || !targets.length) return empty(title + ": none");
  return '<h3>' + esc(title) + '</h3><div class="items">' + targets.map(function(target) {
    return item(target.id || "environment", target.reason || target.status || "", [["profile", target.profile], ["backend", target.backend], ["instance", target.instanceName], ["workspace", target.workspace], ["reason", target.reason], ["lastEndedAt", target.lastEndedAt]], target.reason ? "warn" : tone);
  }).join("") + '</div>';
}
function renderEnvironmentResponse(resource, response) {
  const errors = response.errors || [];
  const data = response.data || {};
  const plan = data.plan || data;
  const applied = data.applied || [];
  const targets = plan.targets || [];
  const skipped = plan.skipped || data.skipped || [];
  const header = item(resource, plan.action || "environment", [["requestedIds", plan.requestedIds || []], ["total", plan.total], ["targets", targets.length], ["applied", applied.length], ["skipped", skipped.length]], errors.length ? "error" : "ok");
  const errorHTML = errors.map(function(err) { return '<div class="error-box">' + esc(err) + '</div>'; }).join("");
  return header + errorHTML + renderEnvironmentTargets("Applied", applied, "ok") + renderEnvironmentTargets("Targets", targets, "info") + renderEnvironmentTargets("Skipped", skipped, "warn");
}
function setEnvironmentBusy(busy, text) {
  const status = document.getElementById("environmentStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-environment-mode]").forEach(function(button) { button.disabled = busy; });
}
function bindEnvironmentPanel() {
  const form = document.getElementById("environmentForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  document.querySelectorAll("[data-environment-mode]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const mode = button.getAttribute("data-environment-mode");
      const action = document.getElementById("envAction").value;
      const resource = "environment/" + action + "/" + mode;
      setEnvironmentBusy(true, mode);
      try {
        const response = await apiPost(resource, environmentPayloadFromForm());
        environmentResultHTML = renderEnvironmentResponse(resource, response);
        document.getElementById("environmentResult").innerHTML = environmentResultHTML;
        setEnvironmentBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
        if (mode === "apply") await load();
      } catch (error) {
        environmentResultHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        document.getElementById("environmentResult").innerHTML = environmentResultHTML;
        setEnvironmentBusy(false, "error");
      }
    });
  });
}
function commandProxyPayloadFromForm() {
  return {
    profile: document.getElementById("commandProxyProfile").value,
    operation: document.getElementById("commandProxyOperation").value,
    command: document.getElementById("commandProxyCommand").value.trim()
  };
}
function renderCommandProxyResponse(resource, response) {
  const errors = response.errors || [];
  const data = response.data || {};
  const plan = data.plan || data;
  const header = item(resource, plan.profile || "profile", [["operation", plan.operation], ["command", plan.command], ["route", plan.route], ["action", plan.action], ["argvSchema", plan.argvSchema], ["status", plan.status], ["changed", plan.changed], ["applied", data.applied], ["before", plan.commandsBefore || []], ["after", plan.commandsAfter || data.commands || []]], errors.length ? "error" : plan.changed || data.applied ? "ok" : "info");
  const errorHTML = errors.map(function(err) { return '<div class="error-box">' + esc(err) + '</div>'; }).join("");
  return header + errorHTML;
}
function setCommandProxyBusy(busy, text) {
  const status = document.getElementById("commandProxyStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-command-proxy-action]").forEach(function(button) { button.disabled = busy; });
}
function bindCommandProxyPanel() {
  const form = document.getElementById("commandProxyForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  if (selectedProfile && document.getElementById("commandProxyProfile")) {
    document.getElementById("commandProxyProfile").value = selectedProfile;
  }
  document.querySelectorAll("[data-command-proxy-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-command-proxy-action");
      const resource = "profile/command-proxy/" + action;
      setCommandProxyBusy(true, action);
      try {
        const response = await apiPost(resource, commandProxyPayloadFromForm());
        commandProxyResultHTML = renderCommandProxyResponse(resource, response);
        document.getElementById("commandProxyResult").innerHTML = commandProxyResultHTML;
        setCommandProxyBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
        if (action === "apply" && !(response.errors && response.errors.length)) await load();
      } catch (error) {
        commandProxyResultHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        document.getElementById("commandProxyResult").innerHTML = commandProxyResultHTML;
        setCommandProxyBusy(false, "error");
      }
    });
  });
}
function renderAuditEvents(full, sourceEvents) {
  const source = sourceEvents || auditEvents;
  const events = full ? source : source.slice(0, 8);
  if (!events.length) return empty("No audit events");
  return '<div class="audit-list">' + events.map(function(e) {
    const tone = e.decision === "deny" || e.decision === "error" ? "error" : "";
    const details = JSON.stringify(e.details || {}, null, 2);
    return '<article class="audit-event ' + esc(tone) + '"><div class="line">' + pill(e.decision || "event", tone || "ok") + '<strong>' + esc(e.action || "event") + '</strong><span class="meta">' + esc(e.session || "") + '</span><span class="meta">' + esc(e.profile || "") + '</span></div><pre>' + esc(details) + '</pre></article>';
  }).join("") + "</div>";
}
function renderAuditExplorer() {
  const source = auditExplorerEvents || scopedAuditEvents(auditEvents);
  return '<form id="auditFilterForm" class="item">' +
    '<div class="form-grid">' +
    '<div class="field"><label for="auditSession">Session</label><input id="auditSession" placeholder="ses_..."></div>' +
    '<div class="field"><label for="auditProfile">Profile</label><input id="auditProfile" placeholder="default" value="' + esc(selectedProfile) + '"></div>' +
    '<div class="field"><label for="auditAction">Action</label><input id="auditAction" placeholder="host.open"></div>' +
    '<div class="field"><label for="auditDecision">Decision</label><select id="auditDecision"><option value="">any</option><option value="allow">allow</option><option value="deny">deny</option><option value="audit-only">audit-only</option><option value="unsupported">unsupported</option><option value="error">error</option></select></div>' +
    '<div class="field"><label for="auditLimit">Limit</label><input id="auditLimit" type="number" min="1" max="1000" value="50"></div>' +
    '</div>' +
    '<div class="action-row"><button class="action secondary" type="button" data-audit-action="filter">Filter</button><button class="action secondary" type="button" data-audit-action="reset">Reset</button><span class="meta" id="auditExplorerStatus">ready</span></div>' +
    '</form><div class="result" id="auditExplorerResult">' + renderAuditEvents(true, source) + '</div>';
}
function auditFilterFromForm() {
  const params = new URLSearchParams();
  const fields = [
    ["session", document.getElementById("auditSession").value.trim()],
    ["profile", document.getElementById("auditProfile").value.trim() || selectedProfile],
    ["action", document.getElementById("auditAction").value.trim()],
    ["decision", document.getElementById("auditDecision").value]
  ];
  fields.forEach(function(field) {
    if (field[1]) params.set(field[0], field[1]);
  });
  const limit = document.getElementById("auditLimit").value.trim() || "50";
  params.set("limit", limit);
  return params.toString();
}
function setAuditBusy(busy, text) {
  const status = document.getElementById("auditExplorerStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-audit-action]").forEach(function(button) { button.disabled = busy; });
}
function bindAuditPanel() {
  const form = document.getElementById("auditFilterForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  document.querySelectorAll("[data-audit-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-audit-action");
      if (action === "reset") {
        auditExplorerEvents = null;
        document.getElementById("auditExplorerResult").innerHTML = renderAuditEvents(true);
        setAuditBusy(false, "ready");
        return;
      }
      setAuditBusy(true, "filtering");
      try {
        const response = await api("audit/events?" + auditFilterFromForm());
        auditExplorerEvents = Array.isArray(response.data) ? response.data : [];
        document.getElementById("auditExplorerResult").innerHTML = renderAuditEvents(true, auditExplorerEvents);
        setAuditBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
      } catch (error) {
        document.getElementById("auditExplorerResult").innerHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        setAuditBusy(false, "error");
      }
    });
  });
}
function renderAuditTail() {
  const scopedAudit = scopedAuditEvents(auditEvents);
  const denied = scopedAuditEvents(deniedAuditEvents());
  auditMetaEl.textContent = denied.length + " denied / " + scopedAudit.length + " events" + (selectedProfile ? " · " + selectedProfile : "");
  auditBodyEl.innerHTML = denied.length ? '<h3>Recent denied</h3>' + renderAuditEvents(false, denied) + '<h3>Recent audit</h3>' + renderAuditEvents(false, scopedAudit) : renderAuditEvents(false, scopedAudit);
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
    syncProfileScopeOptions();
    try {
      const profileQuery = selectedProfile ? "&profile=" + encodeURIComponent(selectedProfile) : "";
      const auditResp = await api("audit/events?limit=20" + profileQuery);
      const deniedResp = await api("audit/events?decision=deny&limit=20" + profileQuery);
      auditEvents = Array.isArray(auditResp.data) ? auditResp.data : [];
      deniedEvents = Array.isArray(deniedResp.data) ? deniedResp.data : [];
    } catch {
      auditEvents = [];
      deniedEvents = [];
    }
    renderSummary();
    renderPanel();
    renderAuditTail();
    const expiry = tokenExpiryLabel();
    setStatus("connected · " + freshnessLabel() + (expiry ? " · " + expiry : ""), "ok");
  } catch (error) {
    overview = {profiles: [], environments: [], sessions: [], backends: [], network: {profileDefaults: []}, capabilities: {}, broker: {}, audit: {}, settings: {}};
    syncProfileScopeOptions();
    auditEvents = [];
    deniedEvents = [];
    summaryEl.innerHTML = "";
    panelBodyEl.innerHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
    auditBodyEl.innerHTML = empty("No audit events");
    setStatus("error", "error");
  }
}
profileScopeEl.addEventListener("change", function() {
  selectedProfile = profileScopeEl.value;
  auditExplorerEvents = null;
  load();
});
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
