package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/operatorhelp"
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
	HelpCatalog operatorhelp.Catalog
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
			serveUIRootWithCatalog(w, r, expiresAt, opts.HelpCatalog)
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

// ServeUIRoot serves the WebUI root HTML. The daemon (internal/daemon) reuses it
// to serve the same WebUI over its loopback UI transport so the existing panels
// can consume the daemon event stream.
func ServeUIRoot(w http.ResponseWriter, r *http.Request, expiresAt time.Time) {
	serveUIRootWithCatalog(
		w,
		r,
		expiresAt,
		emptyOperatorHelpCatalog(),
	)
}

func ServeUIRootWithCatalog(
	w http.ResponseWriter,
	r *http.Request,
	expiresAt time.Time,
	catalog operatorhelp.Catalog,
) {
	serveUIRootWithCatalog(w, r, expiresAt, catalog)
}

func serveUIRootWithCatalog(
	w http.ResponseWriter,
	r *http.Request,
	expiresAt time.Time,
	catalog operatorhelp.Catalog,
) {
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
	_, _ = w.Write([]byte(renderUIHTMLWithCatalog(expiresAt, catalog)))
}

func setUIRootSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

func renderUIHTML(expiresAt time.Time) string {
	return renderUIHTMLWithCatalog(
		expiresAt,
		emptyOperatorHelpCatalog(),
	)
}

func renderUIHTMLWithCatalog(
	expiresAt time.Time,
	catalog operatorhelp.Catalog,
) string {
	if err := catalog.Validate(); err != nil {
		catalog = emptyOperatorHelpCatalog()
	}
	catalogJSON, err := json.Marshal(catalog)
	if err != nil {
		catalogJSON = []byte(
			`{"schema":"hideout.operator-help.v1","commands":[]}`,
		)
	}
	rendered := strings.ReplaceAll(
		uiHTML,
		uiExpiresAtPlaceholder,
		expiresAt.UTC().Format(time.RFC3339),
	)
	return strings.ReplaceAll(
		rendered,
		uiHelpCatalogPlaceholder,
		string(catalogJSON),
	)
}

func emptyOperatorHelpCatalog() operatorhelp.Catalog {
	return operatorhelp.Catalog{
		Schema:   operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{},
	}
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
const uiHelpCatalogPlaceholder = "__HIDEOUT_HELP_CATALOG__"

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
.cli-hint{margin:0 0 10px;border:1px solid var(--line);border-radius:7px;background:#101513;padding:9px 11px;color:var(--muted);font-size:12px;overflow-wrap:anywhere}
.cli-hint code,.rows code{color:var(--text);font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
.help-search{margin-bottom:10px}
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
  <button class="tab" type="button" data-panel="help">Help</button>
  <button class="tab" type="button" data-panel="operator-console">Operator Console</button>
  <button class="tab" type="button" data-panel="setup">Setup</button>
  <button class="tab" type="button" data-panel="run">Run</button>
  <button class="tab" type="button" data-panel="profiles">Profiles</button>
  <button class="tab" type="button" data-panel="environments">Environments</button>
  <button class="tab" type="button" data-panel="sessions">Sessions</button>
  <button class="tab" type="button" data-panel="lifecycle">Lifecycle</button>
  <button class="tab" type="button" data-panel="capabilities">Capabilities</button>
  <button class="tab" type="button" data-panel="hostfs-writes">HostFS Writes</button>
  <button class="tab" type="button" data-panel="decisions">Decisions</button>
  <button class="tab" type="button" data-panel="notices">Notices</button>
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
const helpCatalog = __HIDEOUT_HELP_CATALOG__;
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
let hostFSResultHTML = "";
let profileEnvResultHTML = "";
let selectedProfile = "";
let helpSearchQuery = "";
const panelRowLimit = 50;

function esc(value) {
  return String(value == null ? "" : value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
function redactText(value) {
  if (value == null) return "";
  return String(value)
    .replace(/HIDEOUT_SECRET_[A-Za-z0-9_]+=[^\s,;]*/g, "HIDEOUT_*=REDACTED")
    .replace(/HIDEOUT_SECRET_[A-Za-z0-9_]+/g, "HIDEOUT_*")
    .replace(/setupCredential=[^\s,;]*/g, "setupCredential=REDACTED")
    .replace(/cap_[0-9a-fA-F]{32,}/g, "REDACTED")
    .replace(/\/hostfs-overlay\/objects\/[0-9a-fA-F]{16,}/g, "/hostfs-overlay/objects/REDACTED");
}
function redactValue(value) {
  if (typeof value === "string") return redactText(value);
  if (Array.isArray(value)) return value.map(redactValue);
  if (value && typeof value === "object") {
    const out = {};
    Object.keys(value).forEach(function(key) { out[redactText(key)] = redactValue(value[key]); });
    return out;
  }
  return value;
}
function list(value) {
  if (Array.isArray(value)) return value.length ? value.map(redactValue).join(", ") : "none";
  if (value == null || value === "") return "none";
  return redactText(value);
}
function expectedCommandDiagnosticLabels(value) {
  if (!Array.isArray(value) || !value.length) return [];
  return value.map(function(diag) {
    if (!diag || !diag.command) return "unknown:not-checkable";
    const status = diag.status || "not-checkable";
    const blocks = diag.blocksRequestedRun ? " blocks-run" : "";
    return diag.command + ":" + status + blocks;
  });
}
function commandAdapterLabels(value) {
  if (!Array.isArray(value) || !value.length) return [];
  return value.map(function(adapter) {
    if (!adapter || !adapter.id) return "unknown:off";
    const state = adapter.enabled ? "on" : "off";
    const commands = Array.isArray(adapter.commands) && adapter.commands.length ? adapter.commands.join("|") : "none";
    return adapter.id + ":" + state + "(" + commands + ")";
  });
}
function guestPrivilegeLabel(value) {
  if (!value || !value.status) return "unknown";
  const parts = [value.status];
  if (value.targetUid) parts.push("uid=" + value.targetUid);
  if (value.setupKind) parts.push("setup=" + value.setupKind);
  if (value.reason) parts.push(value.reason);
  return parts.join(" ");
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
  return '<article class="item"><div class="item-top"><div><div class="title">' + esc(redactText(title)) + '</div><div class="meta">' + esc(redactText(meta || "")) + '</div></div>' + pill(tone || "ok", tone || "ok") + '</div>' + rows(rowItems) + "</article>";
}
function boundaryRowsFromSummary(summary) {
  const capabilities = Array.isArray(summary && summary.capabilities) ? summary.capabilities : [];
  const result = [];
  const activity = summary && summary.activityObservation;
  if (activity) {
    result.push(["activity scope", (activity.scope || "unknown") + " · owner=" + (activity.ownerBinding || "unknown")]);
    result.push(["activity privacy", "localPaths=" + (activity.localPathVisibility || "unknown") + " · share=" + (activity.shareablePathTreatment || "unknown")]);
    result.push(["activity retention", "maxBytes=" + (activity.retentionMaxBytes || 0) + " · maxAge=" + (activity.retentionMaxAgeSeconds ? activity.retentionMaxAgeSeconds + "s" : "owner-lifecycle") + " · " + (activity.retentionLifecycle || "")]);
    result.push(["not captured", Array.isArray(activity.excludedData) ? activity.excludedData.join(", ") : "unknown"]);
    result.push(["coverage non-claim", activity.coverageNonClaim || "unknown"]);
  }
  capabilities.forEach(function(capability) {
    const counts = [
      "allowed=" + (capability.allowed || 0),
      "denied=" + (capability.denied || 0)
    ];
    if (capability.unsupported) counts.push("unsupported=" + capability.unsupported);
    if (capability.error) counts.push("error=" + capability.error);
    if (capability.auditOnly) counts.push("auditOnly=" + capability.auditOnly);
    const context = [];
    if (capability.owner) context.push("owner=" + capability.owner);
    if (capability.source) context.push("source=" + capability.source);
    if (capability.lifetime) context.push("lifetime=" + capability.lifetime);
    if (capability.closeReason) context.push("closed=" + capability.closeReason);
    if (capability.endpointCategory) context.push("category=" + capability.endpointCategory);
    result.push([capability.capability || "capability", counts.concat(context).join(" · ")]);
  });
  return result.length ? result : [["capabilities", "none"]];
}
function empty(label) {
  return '<div class="empty">' + esc(redactText(label)) + '</div>';
}
function metric(label, value, sub) {
  return '<article class="metric"><div class="label">' + esc(redactText(label)) + '</div><div class="value">' + esc(redactText(value)) + '</div><div class="sub">' + esc(redactText(sub || "")) + '</div></article>';
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
	const environments = filterByProfile(source.environments, "profile");
	const environmentIDs = new Set(environments.map(function(value) { return value && value.id; }));
	const lifecycle = (source.lifecycle || []).filter(function(value) { return value && environmentIDs.has(value.environmentId); });
	const hostfsWrites = filterByProfile(source.hostfsWrites, "profile");
	const decisionRows = filterByProfile(source.decisionRows, "profile");
	const noticeRows = filterByProfile(source.noticeRows, "profile");
	const decisions = decisionRows.reduce(function(summary, row) {
	  const status = row && row.status || "";
	  if (status === "pending") summary.pending++;
	  else if (status === "claimed") summary.claimed++;
	  else summary.terminal++;
	  return summary;
	}, {pending: 0, claimed: 0, terminal: 0});
	const notices = {
	  unacknowledged: noticeRows.filter(function(row) { return row && !row.acknowledged; }).length,
	  total: noticeRows.length
	};
  return Object.assign({}, source, {
    profiles: filterByProfile(source.profiles, "name"),
    environments: environments,
    sessions: filterByProfile(source.sessions, "profile"),
	  network: network,
	  hostfsWrites: hostfsWrites,
	  decisionRows: decisionRows,
	  noticeRows: noticeRows,
	  decisions: decisions,
	  notices: notices,
	  lifecycle: lifecycle
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
function actionableStatus(status) {
  return !status || status === "pending" || status === "claimed" || status === "ready" || status === "requires-decision";
}
function consoleActionSummary() {
  const hostfsWrites = (overview.hostfsWrites || []).filter(function(row) { return actionableStatus(row.status); }).length;
  const decisions = (overview.decisionRows || []).filter(function(row) { return actionableStatus(row.status); }).length;
  const notices = (overview.noticeRows || []).filter(function(row) { return row && !row.acknowledged; }).length;
  return {hostfsWrites: hostfsWrites, decisions: decisions, notices: notices, total: hostfsWrites + decisions + notices};
}
function consoleStatusRows() {
  const bundles = overview.bundles || {};
  const packs = overview.adapterPacks || [];
  const backends = overview.backends || [];
  const support = backends.length ? backends.map(function(b) { return (b.name || "backend") + ":" + (b.available ? "available" : "unavailable"); }).join(", ") : "run hideout support matrix";
  return [
    {id: "doctor", label: "Doctor", status: "explicit", detail: "not run on console load", next: "hideout doctor --level light"},
    {id: "package", label: "Package", status: "read-only", detail: "bundles=" + (bundles.installed || 0) + " enabled=" + (bundles.enabled || 0) + " adapterPacks=" + packs.length, next: "hideout package verify <install-prefix>"},
    {id: "support", label: "Support", status: "matrix", detail: support, next: "hideout support matrix"}
  ];
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
  const background = overview.background || [];
  const lifecycle = overview.lifecycle || [];
  const hostfsWrites = overview.hostfsWrites || [];
  const decisions = overview.decisionRows || [];
  const notices = overview.noticeRows || [];
  const actionSummary = consoleActionSummary();
  const available = backends.filter(function(b) { return b.available; }).length;
  const networkModes = (overview.network && overview.network.profileDefaults || []).map(function(n) { return normalizedNetworkMode(n.mode); });
  const denied = deniedAuditEvents();
  summaryEl.innerHTML = [
    metric("Action Required", actionSummary.total, "hostfs=" + actionSummary.hostfsWrites + " decisions=" + actionSummary.decisions + " notices=" + actionSummary.notices),
    metric("Profiles", profiles.length, profiles.map(function(p) { return p.name; }).join(", ")),
    metric("Environments", environments.length, environments.slice(0, 3).map(function(e) { return (e.status || "unknown") + ":" + (e.profile || "-"); }).join(", ")),
    metric("Sessions", sessions.length, sessions.filter(function(s) { return s.hasAudit; }).length + " with audit"),
    metric("Lifecycle", lifecycle.length, lifecycle.slice(0, 3).map(function(row) { return (row.activity || "unknown") + ":" + (row.environmentId || "environment"); }).join(", ")),
    metric("Backends", available + "/" + backends.length, backends.map(function(b) { return b.name; }).join(", ")),
    metric("Background", background.length, background.slice(0, 3).map(function(b) { return (b.op || "op") + ":" + (b.status || "unknown"); }).join(", ")),
    metric("HostFS Writes", hostfsWrites.length, hostfsWrites.slice(0, 3).map(function(w) { return (w.operation || "op") + ":" + (w.status || "pending"); }).join(", ")),
    metric("Decisions", decisions.length || ((overview.decisions && overview.decisions.pending) || 0), decisions.slice(0, 3).map(function(d) { return (d.kind || "decision") + ":" + (d.status || "pending"); }).join(", ")),
    metric("Notices", notices.length || ((overview.notices && overview.notices.unacknowledged) || 0), notices.slice(0, 3).map(function(n) { return (n.kind || "notice") + ":" + (n.status || "info"); }).join(", ")),
    metric("Denied", denied.length, denied.slice(0, 3).map(function(e) { return e.action || "event"; }).join(", ")),
    metric("Network", networkModes.length || "direct", networkModes.join(", ") || "direct")
  ].join("");
  });
}
function renderPanel() {
  return withScopedOverview(function() {
  const title = panelTitle(activePanel);
  panelTitleEl.textContent = title;
  panelMetaEl.textContent = domainOwner(activePanel) + (selectedProfile ? " · " + selectedProfile : "");
  const renderer = renderers[activePanel] || renderers.overview;
  const cliHint = activePanel === "help" ? "" : canonicalCLIForPanel(activePanel);
  panelBodyEl.innerHTML = cliHint + renderer();
  if (activePanel === "help") bindHelpPanel();
  if (activePanel === "setup") bindSetupPanel();
  if (activePanel === "run") bindRunPanel();
  if (activePanel === "profiles") bindProfileEnvPanel();
  if (activePanel === "environments") bindEnvironmentPanel();
  if (activePanel === "capabilities") {
    bindCommandProxyPanel();
    bindHostFSPanel();
  }
  if (activePanel === "hostfs-writes") bindHostFSWritePanel();
  if (activePanel === "decisions") bindDecisionPanel();
  if (activePanel === "notices") bindNoticePanel();
  if (activePanel === "audit") bindAuditPanel();
  });
}
function panelTitle(name) {
  return String(name || "overview").split("-").map(function(part) {
    return part.charAt(0).toUpperCase() + part.slice(1);
  }).join(" ");
}
function domainOwner(name) {
  return {
    overview: "manager",
    help: "command catalog",
    "operator-console": "manager/operator-console",
    setup: "init/tool-supply",
    run: "manager/backend",
    profiles: "profile",
    environments: "manager/environment",
    sessions: "manager/backend",
    lifecycle: "daemon/lifecycle",
    capabilities: "policy/cmdproxy",
    "hostfs-writes": "manager/hostfs-overlay",
    decisions: "manager/decision-center",
    notices: "manager/decision-center",
    broker: "broker",
    network: "network/secrets",
    backends: "backend",
    secrets: "secrets",
    audit: "audit",
    settings: "manager"
  }[name] || "manager";
}
const panelCommandIDs = {
  overview: "tui",
  "operator-console": "tui",
  setup: "setup",
  run: "run",
  profiles: "profile",
  environments: "env",
  sessions: "session",
  lifecycle: "stop",
  capabilities: "profile",
  "hostfs-writes": "hostfs",
  decisions: "decision",
  notices: "notice",
  broker: "audit",
  network: "connect",
  backends: "doctor",
  secrets: "secret",
  audit: "audit",
  settings: "daemon"
};
function helpCommand(id) {
  const catalog = typeof helpCatalog === "undefined" ? null : helpCatalog;
  const commands = catalog && Array.isArray(catalog.commands) ? catalog.commands : [];
  return commands.find(function(command) {
    return command && !command.hidden && (command.id === id || command.name === id);
  }) || null;
}
function canonicalCLIForPanel(panel) {
  const command = helpCommand(panelCommandIDs[panel]);
  if (!command || !Array.isArray(command.syntax) || !command.syntax.length) return "";
  return '<div class="cli-hint">Canonical CLI: <code>' + esc(command.syntax[0]) +
    '</code> · <code>hideout help ' + esc(command.name) + '</code></div>';
}
function helpSearchText(command) {
  return [
    command.id,
    command.name,
    (command.aliases || []).join(" "),
    (command.searchTerms || []).join(" "),
    command.taskGroup,
    command.purpose,
    command.audience,
    command.stability,
    (command.syntax || []).join(" "),
    (command.effects || []).join(" "),
    (command.safety || []).join(" "),
    (command.recovery || []).join(" ")
  ].join("\n").toLowerCase();
}
function renderHelp() {
  const query = String(helpSearchQuery || "").trim().toLowerCase();
  const catalog = typeof helpCatalog === "undefined" ? null : helpCatalog;
  const commands = (catalog && Array.isArray(catalog.commands) ? catalog.commands : []).filter(function(command) {
    return command && !command.hidden && (!query || helpSearchText(command).includes(query));
  });
  const search = '<div class="field help-search"><label for="helpSearch">Search commands</label>' +
    '<input id="helpSearch" aria-label="Search commands" value="' + esc(helpSearchQuery) +
    '" placeholder="proxy, files, clean, developer"></div>';
  if (!commands.length) return search + empty(query ? "No matching commands" : "Command catalog unavailable");
  const sections = ["stable", "advanced", "lab"].map(function(stability) {
    const rows = commands.filter(function(command) { return command.stability === stability; });
    if (!rows.length) return "";
    return '<h3>' + esc(stability === "lab" ? "Lab · unsupported" : stability) + '</h3><div class="items">' +
      rows.map(function(command) {
        return item(
          command.name,
          command.taskGroup + " · " + command.audience,
          [
            ["purpose", command.purpose],
            ["CLI", (command.syntax || [])[0]],
            ["effects", command.effects || []],
            ["safety", command.safety || []],
            ["recovery", command.recovery || []],
            ["details", "hideout help " + command.name]
          ],
          stability === "lab" ? "warn" : stability === "stable" ? "ok" : "info"
        );
      }).join("") + "</div>";
  }).join("");
  return search + sections;
}
function bindHelpPanel() {
  const input = document.getElementById("helpSearch");
  if (!input) return;
  input.addEventListener("input", function() {
    helpSearchQuery = input.value;
    panelBodyEl.innerHTML = renderHelp();
    bindHelpPanel();
    const next = document.getElementById("helpSearch");
    if (next) {
      next.focus();
      next.setSelectionRange(next.value.length, next.value.length);
    }
  });
}
const renderers = {
  overview: function() {
    const s = overview.settings || {};
    const c = overview.capabilities || {};
    const init = overview.init || {};
    const initSteps = init.nextSteps || [];
    const background = overview.background || [];
    const lifecycle = overview.lifecycle || [];
    const exports = overview.exportOutcomes || [];
    const cleanups = overview.cleanupOutcomes || [];
    const hostfsWrites = overview.hostfsWrites || [];
    const decisions = overview.decisionRows || [];
    const notices = overview.noticeRows || [];
    return '<div class="items">' + [
      item("Manager", overview.version || "hideout.manager/v1", [["storageRoot", overview.storageRoot], ["storeRoot", s.storeRoot], ["maxCapabilities", c.maxCapabilities || []]], "ok"),
      item("Init", init.initialized ? "initialized" : "needs setup", [["profile", init.profile], ["pendingTasks", init.pendingTasks], ["nextSteps", initSteps.map(function(step) { return (step.label || step.id) + ": " + step.command; })]], init.pendingTasks ? "warn" : "ok"),
      item("Broker", "host boundary", [["actions", overview.broker && overview.broker.actions], ["commandProxies", overview.broker && overview.broker.commandProxies]], "info"),
      item("Environments", "reusable guest state", [["count", (overview.environments || []).length], ["running", (overview.environments || []).filter(function(e) { return e.status === "running"; }).length]], "info"),
      item("Lifecycle", "dependency-derived VM activity", [["environments", lifecycle.length], ["states", lifecycle.slice(0, 5).map(function(row) { return (row.environmentId || "environment") + ":" + (row.activity || "unknown"); })]], lifecycle.some(function(row) { return row.activity === "blocked-unproved" || row.activity === "stopping-unknown"; }) ? "error" : "info"),
      item("Background", "daemon work", [["count", background.length], ["recent", background.slice(0, 5).map(function(b) { return (b.id || "bg") + ":" + (b.op || "op") + ":" + (b.status || "unknown"); })]], background.some(function(b) { return b.status === "failed"; }) ? "error" : "info"),
      item("HostFS Writes", "staged host mutations", [["pending", hostfsWrites.length], ["recent", hostfsWrites.slice(0, 5).map(function(w) { return (w.operation || "op") + ":" + (w.status || "pending") + ":" + (w.path || ""); })]], hostfsWrites.some(function(w) { return w.status === "failed" || w.status === "conflict"; }) ? "error" : "warn"),
      item("Decisions", "actionable operator queue", [["rows", decisions.length], ["summary", overview.decisions && ("pending=" + (overview.decisions.pending || 0) + " claimed=" + (overview.decisions.claimed || 0) + " terminal=" + (overview.decisions.terminal || 0))], ["recent", decisions.slice(0, 5).map(function(d) { return (d.kind || "decision") + ":" + (d.status || "pending") + ":" + (d.id || ""); })]], decisions.some(function(d) { return d.status === "failed" || d.status === "timed-out"; }) ? "error" : decisions.length ? "warn" : "ok"),
      item("Notices", "operator facts", [["rows", notices.length], ["summary", overview.notices && ("unacked=" + (overview.notices.unacknowledged || 0) + " total=" + (overview.notices.total || 0))], ["recent", notices.slice(0, 5).map(function(n) { return (n.kind || "notice") + ":" + (n.status || "info") + ":" + (n.id || ""); })]], notices.some(function(n) { return !n.acknowledged && (n.severity === "error" || n.severity === "warning"); }) ? "warn" : "ok"),
      item("Exports", "share boundary", [["recent", exports.slice(0, 5).map(function(e) { return (e.source || "source") + ":" + (e.status || "unknown") + ":" + (e.decision || ""); })]], exports.some(function(e) { return e.status === "failed"; }) ? "error" : "ok"),
      item("Cleanup", "local lifecycle", [["recent", cleanups.slice(0, 5).map(function(c) { return (c.status || "unknown") + ":sessions=" + (c.sessions || 0) + ":" + (c.secretState || ""); })]], cleanups.some(function(c) { return c.status === "failed"; }) ? "error" : "ok"),
      item("Audit", "redacted JSONL", [["sessionAuditFiles", overview.audit && overview.audit.sessionAuditFiles], ["eventsLoaded", auditEvents.length]], "ok")
    ].join("") + "</div>";
  },
  "operator-console": function() {
    const actionSummary = consoleActionSummary();
    const background = overview.background || [];
    const environments = overview.environments || [];
    const lifecycle = overview.lifecycle || [];
    const hostfsWrites = overview.hostfsWrites || [];
    const decisions = overview.decisionRows || [];
    const notices = overview.noticeRows || [];
    const statusRows = consoleStatusRows();
    const streamTone = liveStreamState === "live" || liveStreamState === "idle-live" ? "ok" : liveStreamState === "daemon-less" ? "warn" : "error";
    return '<div class="items">' + [
      item("Action Required", "existing authority only", [["total", actionSummary.total], ["hostfsWrites", actionSummary.hostfsWrites], ["decisions", actionSummary.decisions], ["notices", actionSummary.notices]], actionSummary.total ? "warn" : "ok"),
      item("Stream", liveStreamState || "seeding", [["state", liveStreamState], ["reason", liveStreamReason], ["lastSeq", liveLastSeq], ["fallback", liveStreamState === "disconnected" ? "manual refresh or daemon-less interval" : "none while healthy"]], streamTone),
      item("Environments", "manager overview", [["count", environments.length], ["running", environments.filter(function(e) { return e.status === "running"; }).length], ["recent", environments.slice(0, 5).map(function(e) { return (e.name || e.id || "env") + ":" + (e.status || "unknown"); })]], "info"),
      item("Lifecycle", "daemon-derived activity", [["count", lifecycle.length], ["recent", lifecycle.slice(0, 5).map(function(row) { return (row.environmentId || "environment") + ":" + (row.activity || "unknown"); })]], lifecycle.some(function(row) { return row.activity === "blocked-unproved" || row.activity === "stopping-unknown"; }) ? "error" : "info"),
      item("Background", "daemon env stop/clean only", [["count", background.length], ["recent", background.slice(0, 5).map(function(b) { return (b.id || "bg") + ":" + (b.op || "op") + ":" + (b.status || "unknown"); })]], background.some(function(b) { return b.status === "failed"; }) ? "error" : "info"),
      item("HostFS Writes", "staged write decisions", [["actionable", actionSummary.hostfsWrites], ["recent", hostfsWrites.slice(0, 5).map(function(w) { return (w.decisionId || "write") + ":" + (w.status || "pending"); })]], actionSummary.hostfsWrites ? "warn" : "ok"),
      item("Decisions", "decision center", [["actionable", actionSummary.decisions], ["summary", overview.decisions && ("pending=" + (overview.decisions.pending || 0) + " claimed=" + (overview.decisions.claimed || 0) + " terminal=" + (overview.decisions.terminal || 0))], ["recent", decisions.slice(0, 5).map(function(d) { return (d.id || "decision") + ":" + (d.status || "pending"); })]], actionSummary.decisions ? "warn" : "ok"),
      item("Notices", "acknowledge facts", [["unacknowledged", actionSummary.notices], ["summary", overview.notices && ("unacked=" + (overview.notices.unacknowledged || 0) + " total=" + (overview.notices.total || 0))], ["recent", notices.slice(0, 5).map(function(n) { return (n.id || "notice") + ":" + (n.acknowledged ? "ack" : "open"); })]], actionSummary.notices ? "warn" : "ok")
    ].concat(statusRows.map(function(row) {
      return item(row.label, row.status, [["detail", row.detail], ["next", row.next]], row.status === "explicit" ? "info" : "ok");
    })).join("") + "</div>";
  },
  lifecycle: function() {
    const values = overview.lifecycle || [];
    if (!values.length) return empty("No lifecycle observations");
    return '<div class="items">' + values.map(function(row) {
      const tone = row.activity === "blocked-unproved" || row.activity === "stopping-unknown" ? "error" : row.activity === "idle-grace" || row.activity === "stopping" ? "warn" : "info";
      return item(row.environmentId || "environment", row.activity || "unknown", [["backend", row.backendState], ["backend observed", row.backendObservedAt], ["generation", row.startGeneration], ["reconciliation", row.reconciliation], ["idleDeadline", row.idleDeadline], ["pins", (row.pins || []).map(function(value) { return value.kind + ":" + value.state; })], ["drains", (row.drains || []).map(function(value) { return value.kind + ":" + value.state; })], ["retained history facts", (row.retained || []).map(function(value) { return value.kind + ":" + value.state; })], ["handoff history facts", (row.handoffs || []).map(function(value) { return value.kind + ":" + value.state; })], ["orphans", (row.orphans || []).map(function(value) { return value.kind + ":" + value.state; })], ["reason", row.reasonCode]], tone);
    }).join("") + "</div>";
  },
  "hostfs-writes": function() {
    const rows = overview.hostfsWrites || [];
    if (!rows.length) return empty("No pending HostFS write decisions");
    return '<div class="items">' + rows.map(function(row) {
      const details = [["operationId", row.operationId], ["decisionId", row.decisionId], ["operation", row.operation], ["path", row.path], ["destination", row.destinationPath], ["privilege", row.privilegeStatus], ["reason", row.reason]];
      const actions = '<div class="action-row">' +
        '<button class="action secondary" type="button" data-hostfs-write-action="claim" data-decision-id="' + esc(row.decisionId) + '">Claim</button>' +
        (row.claimToken ? '<button class="action secondary" type="button" data-hostfs-write-action="release" data-decision-id="' + esc(row.decisionId) + '">Release</button>' : '') +
        '<button class="action" type="button" data-hostfs-write-action="apply" data-decision-id="' + esc(row.decisionId) + '">Apply</button>' +
        '<button class="action secondary" type="button" data-hostfs-write-action="discard" data-decision-id="' + esc(row.decisionId) + '">Discard</button>' +
        '</div>';
      return item(row.decisionId || row.operationId || "pending", row.status || "pending", details, row.privilegeStatus === "enforced" ? "warn" : "error") + actions;
    }).join("") + '<div class="result" id="hostFSWriteStatus"></div></div>';
  },
  decisions: function() {
    const rows = overview.decisionRows || [];
    if (!rows.length) return empty("No actionable decisions");
    return '<div class="items">' + rows.map(function(row) {
      const details = [["kind", row.kind], ["status", row.status], ["defaultOutcome", row.defaultOutcome], ["profile", row.profile], ["session", row.session], ["backend", row.backend], ["reason", row.reason], ["claimSurface", row.claimSurface], ["claimOperator", row.claimOperator], ["claimExpiresAt", row.claimExpiresAt], ["revision", row.revision]];
      const tone = row.status === "failed" || row.status === "timed-out" ? "error" : row.status === "pending" || row.status === "claimed" ? "warn" : "ok";
	      const reopenable = row.kind === "hostfs.read" && (row.status === "denied" || row.status === "timed-out");
	      const revocable = row.kind === "host-app.open-resource" && row.status === "approved";
	      const disabled = row.status === "applied" || row.status === "denied" || row.status === "timed-out" || row.status === "failed";
	      const actions = revocable ? '<div class="action-row"><button class="action secondary" type="button" data-decision-action="revoke" data-decision-id="' + esc(row.id) + '">Revoke</button></div>' : reopenable ? '<div class="action-row"><button class="action secondary" type="button" data-decision-action="reopen" data-decision-id="' + esc(row.id) + '">Reopen</button></div>' : disabled ? "" : '<div class="action-row">' +
        '<button class="action secondary" type="button" data-decision-action="claim" data-decision-id="' + esc(row.id) + '">Claim</button>' +
        (row.claimToken ? '<button class="action secondary" type="button" data-decision-action="release" data-decision-id="' + esc(row.id) + '">Release</button>' : '') +
        '<button class="action" type="button" data-decision-action="approve" data-decision-id="' + esc(row.id) + '">Approve</button>' +
        '<button class="action secondary" type="button" data-decision-action="deny" data-decision-id="' + esc(row.id) + '">Deny</button>' +
        '</div>';
      return item(row.id || "decision", row.kind || "actionable", details, tone) + actions;
    }).join("") + '<div class="result" id="decisionStatus"></div></div>';
  },
  notices: function() {
    const rows = overview.noticeRows || [];
    if (!rows.length) return empty("No unacknowledged notices");
    return '<div class="items">' + rows.map(function(row) {
      const details = [["kind", row.kind], ["status", row.status], ["severity", row.severity], ["acknowledged", row.acknowledged], ["profile", row.profile], ["session", row.session], ["backend", row.backend]];
      const tone = row.severity === "error" ? "error" : !row.acknowledged && row.severity === "warning" ? "warn" : "info";
      const actions = row.acknowledged ? "" : '<div class="action-row"><button class="action secondary" type="button" data-notice-action="ack" data-notice-id="' + esc(row.id) + '">Acknowledge</button></div>';
      return item(row.id || "notice", row.acknowledged ? "acknowledged" : "unacknowledged", details, tone) + actions;
    }).join("") + '<div class="result" id="noticeStatus"></div></div>';
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
    const profileOptions = profiles.length ? profiles.map(function(p) {
      return '<option value="' + esc(p.name) + '">' + esc(p.name) + '</option>';
    }).join("") : '<option value="default">default</option>';
    const envForm = '<form id="profileEnvForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="profileEnvProfile">Profile</label><select id="profileEnvProfile">' + profileOptions + '</select></div>' +
      '<div class="field"><label for="profileEnvOperation">Operation</label><select id="profileEnvOperation"><option value="set">set public</option><option value="unset">unset public</option><option value="inherit">inherit host env</option><option value="uninherit">remove inherited env</option><option value="deny">deny env</option><option value="undeny">remove deny</option></select></div>' +
      '<div class="field"><label for="profileEnvName">Name or pattern</label><input id="profileEnvName" placeholder="SERVICE_TOKEN or SSH_*"></div>' +
      '<div class="field"><label for="profileEnvValue">Value</label><input id="profileEnvValue" placeholder="only used by set public"></div>' +
      '</div>' +
      '<div class="action-row"><button class="action secondary" type="button" data-profile-env-action="plan">Plan</button><button class="action" type="button" data-profile-env-action="apply">Apply</button><span class="meta" id="profileEnvStatus">ready</span></div>' +
      '</form><div class="result" id="profileEnvResult">' + profileEnvResultHTML + '</div>';
    if (!profiles.length) return envForm + empty("No profiles");
    return envForm + '<div class="items">' + profiles.map(function(p) {
      const tone = p.validationError ? "error" : "ok";
      return item(p.name || "invalid", p.validationError || p.lineageMode || "profile", [["profileId", p.profileId], ["identityId", p.identityId], ["previousIdentityId", p.previousIdentityId], ["networkMode", p.networkMode], ["proxySecretRef", p.proxySecretRef], ["envPublic", p.envPublic], ["envInherit", p.envInherit], ["envDeny", p.envDeny], ["expectedCommands", p.expectedCommands], ["expectedDiagnostics", expectedCommandDiagnosticLabels(p.expectedCommandDiagnostics)], ["commandProxies", p.commandProxies], ["commandAdapters", commandAdapterLabels(p.commandAdapters)], ["hostfs", "allow=" + (p.hostfsGrants || 0) + " deny=" + (p.hostfsDeny || 0)]], tone);
    }).join("") + "</div>";
  },
  environments: function() {
    const environments = overview.environments || [];
    const visibleEnvironments = visibleEnvironmentsForPanel(environments);
    const envHTML = environments.length ? panelLimitNotice("environments", visibleEnvironments.length, environments.length) + '<div class="items">' + visibleEnvironments.map(function(e) {
      const tone = e.status === "running" ? "warn" : e.status === "stopped" ? "info" : "ok";
      const title = e.name ? (e.name + (e.autoNamed ? " (auto)" : "")) : e.id;
      const trustDomain = e.mode === "shared" ? "shared guest kernel, root disk, tools, caches and profile state" : "separate machine identity";
      return item(title, e.status || "environment", [["mode", e.mode], ["sharedSlot", e.sharedSlot], ["machineIdentityId", e.machineIdentityId], ["trustDomain", trustDomain], ["image", e.imageRef], ["profile", e.profile], ["backend", e.backend], ["instance", e.instanceName], ["workspace", e.workspace], ["guestWorkspace", e.guestWorkspace], ["activeSessions", e.activeSessions || 0], ["activeWorkspaceViews", e.activeWorkspaceViews || 0], ["workspaceProviderState", e.workspaceProviderState], ["ownerHealth", e.ownerHealth], ["id", e.id], ["lastSessionId", e.lastSessionId], ["lastCommand", e.lastCommand], ["lastStartedAt", e.lastStartedAt], ["lastEndedAt", e.lastEndedAt]], tone);
    }).join("") + "</div>" : empty("No reusable environments");
    return '<form id="environmentForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="envAction">Action</label><select id="envAction"><option value="stop">stop</option><option value="clean">clean</option></select></div>' +
      '<div class="field"><label for="envIds">Environment names or IDs</label><input id="envIds" placeholder="empty = all, or work, env_..."></div>' +
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
      const relations = (s.workspaceRelations || []).map(function(row) { return (row.relation || "unknown") + ":" + (row.selectedPosition || "peer") + ":" + (row.otherWorkspaceId || "workspace"); });
      return item(s.workspaceLabel || s.id, s.profile || "session", [["sessionId", s.id], ["backend", s.backend], ["environmentId", s.environmentId], ["workspaceId", s.workspaceId], ["guestWorkspace", s.guestWorkspace], ["workspaceTransport", s.workspaceTransport], ["workspaceViewState", s.workspaceViewState], ["workspaceRelations", relations], ["workspaceBlocker", s.workspaceBlockerCode], ["state", s.state], ["ownerStatus", s.ownerStatus], ["terminalMode", s.terminalMode], ["commandClass", s.commandClass], ["networkMode", s.networkMode], ["guestPrivilege", guestPrivilegeLabel(s.guestPrivilege)], ["auditPath", s.auditPath], ["hasAudit", s.hasAudit], ["hasBrokerEndpoint", s.hasBrokerEndpoint], ["hasNetworkPlan", s.hasNetworkPlan], ["hasProxySecretFile", s.hasProxySecretFile], ["hasEphemeralState", s.hasEphemeralState], ["next", sessionNextCommands(s)]], s.workspaceViewState === "unproved" || s.ownerStatus === "unprovable" || s.cleanupError ? "error" : s.ownerStatus === "live" ? "warn" : s.hasProxySecretFile ? "warn" : "ok");
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
    const hostFSForm = '<form id="hostFSForm" class="item">' +
      '<div class="form-grid">' +
      '<div class="field"><label for="hostFSProfile">Profile</label><select id="hostFSProfile">' + profileOptions + '</select></div>' +
      '<div class="field"><label for="hostFSOperation">Operation</label><select id="hostFSOperation"><option value="add">allow rule</option><option value="deny">deny rule</option><option value="remove">remove rule</option></select></div>' +
      '<div class="field"><label for="hostFSRule">Rule</label><input id="hostFSRule" placeholder="read:/absolute/file or tree:/absolute/dir"></div>' +
      '<div class="field"><label for="hostFSRuleID">Rule ID</label><input id="hostFSRuleID" placeholder="hfs_... for remove"></div>' +
      '<div class="field"><label for="hostFSReason">Reason</label><input id="hostFSReason" placeholder="why this host path is needed"></div>' +
      '</div>' +
      '<div class="action-row"><button class="action secondary" type="button" data-hostfs-action="plan">Plan</button><button class="action" type="button" data-hostfs-action="apply">Apply</button><span class="meta" id="hostFSStatus">ready</span></div>' +
      '</form><div class="result" id="hostFSResult">' + hostFSResultHTML + '</div>';
    return commandForm + hostFSForm + '<div class="items">' + item("Capability ceiling", "final validator", [["maxCapabilities", c.maxCapabilities || []]], "ok") + item("host.open", "host broker", [["mode", h.mode], ["allowUrls", h.allowUrls], ["urlScope", h.urlScope], ["localNetworkPolicy", h.localNetworkPolicy], ["allowWorkspaceFiles", h.allowWorkspaceFiles], ["browserProfile", h.browserProfile], ["browserControl", h.browserControl], ["profiles", h.profiles]], h.allowUrls || h.allowWorkspaceFiles ? "info" : "warn") + (profiles.length ? item("HostFS profile rules", "read-only portal", [["profiles", profiles.map(function(p) { return (p.name || "-") + ": allow=" + (p.hostfsGrants || 0) + " deny=" + (p.hostfsDeny || 0); })]], "info") : "") + (proxies.length ? proxies.map(function(p) {
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
  },
  help: function() {
    return renderHelp();
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
  return {
    profile: document.getElementById("setupProfile").value,
    backend: document.getElementById("setupBackend").value,
    network: document.getElementById("setupNetwork").value,
    proxySecretRef: document.getElementById("setupProxySecret").value.trim()
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
  const boundary = data.boundarySummary ? item("Boundary Summary", summary.evidence || "audit", boundaryRowsFromSummary(summary), "info") : "";
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
function hostFSPayloadFromForm() {
  return {
    profile: document.getElementById("hostFSProfile").value,
    operation: document.getElementById("hostFSOperation").value,
    rule: document.getElementById("hostFSRule").value.trim(),
    ruleId: document.getElementById("hostFSRuleID").value.trim(),
    reason: document.getElementById("hostFSReason").value.trim()
  };
}
function hostFSRuleLabel(rule) {
  if (!rule) return "";
  return (rule.effect || "") + ":" + (rule.hostPath || "") + " " + (rule.id || "");
}
function renderHostFSResponse(resource, response) {
  const errors = response.errors || [];
  const data = response.data || {};
  const plan = data.plan || data;
  const planned = plan.plannedRule || plan.removedRule || {};
  const grants = (plan.grantsAfter || data.grants || []).map(hostFSRuleLabel);
  const deny = (plan.denyAfter || data.deny || []).map(hostFSRuleLabel);
  const header = item(resource, plan.profile || "profile", [["operation", plan.operation], ["rule", plan.rule], ["ruleId", plan.ruleId || planned.id], ["planned", hostFSRuleLabel(planned)], ["status", plan.status], ["changed", plan.changed], ["applied", data.applied], ["grants", grants], ["deny", deny]], errors.length ? "error" : plan.changed || data.applied ? "ok" : "info");
  const errorHTML = errors.map(function(err) { return '<div class="error-box">' + esc(err) + '</div>'; }).join("");
  return header + errorHTML;
}
function setHostFSBusy(busy, text) {
  const status = document.getElementById("hostFSStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-hostfs-action]").forEach(function(button) { button.disabled = busy; });
}
function bindHostFSPanel() {
  const form = document.getElementById("hostFSForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  if (selectedProfile && document.getElementById("hostFSProfile")) {
    document.getElementById("hostFSProfile").value = selectedProfile;
  }
  document.querySelectorAll("[data-hostfs-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-hostfs-action");
      const resource = "profile/hostfs/" + action;
      setHostFSBusy(true, action);
      try {
        const response = await apiPost(resource, hostFSPayloadFromForm());
        hostFSResultHTML = renderHostFSResponse(resource, response);
        document.getElementById("hostFSResult").innerHTML = hostFSResultHTML;
        setHostFSBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
        if (action === "apply" && !(response.errors && response.errors.length)) await load();
      } catch (error) {
        hostFSResultHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        document.getElementById("hostFSResult").innerHTML = hostFSResultHTML;
        setHostFSBusy(false, "error");
      }
    });
  });
}
function findHostFSWriteRow(decisionId) {
  const rows = overview.hostfsWrites || [];
  for (let i = 0; i < rows.length; i++) {
    if (rows[i] && rows[i].decisionId === decisionId) return rows[i];
  }
  return null;
}
function setHostFSWriteStatus(text, tone) {
  const status = document.getElementById("hostFSWriteStatus");
  if (!status) return;
  status.className = tone === "error" ? "error-box" : "meta";
  status.textContent = text || "";
}
function bindHostFSWritePanel() {
  document.querySelectorAll("[data-hostfs-write-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-hostfs-write-action");
      const decisionId = button.getAttribute("data-decision-id");
      const row = findHostFSWriteRow(decisionId);
      if (!row) {
        setHostFSWriteStatus("decision not found", "error");
        return;
      }
      try {
        if (action === "claim") {
          const response = await apiPost("hostfs/write/claim", {decisionId: decisionId, expectedVersion: "hideout.hostfs-write-plan/v1", surface: "webui", leaseSeconds: 60});
          if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
          row.claimToken = response.data && response.data.claimToken;
          row.status = "claimed";
          row.claimExpiresAt = response.data && response.data.claimExpiresAt;
          row.revision = response.data && response.data.revision;
          setHostFSWriteStatus("claimed " + decisionId + " until " + (row.claimExpiresAt || "lease expiry"), "ok");
          renderPanel();
          return;
        }
        if (action === "release") {
          if (!row.claimToken) throw new Error("this browser does not own the claim token");
          const response = await apiPost("decision/release", {decisionId: decisionId, expectedVersion: "hideout.decision/v1", expectedRevision: row.revision || 0, claimToken: row.claimToken, reason: "web decision dialog closed"});
          if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
          row.claimToken = "";
          row.status = "pending";
          row.claimExpiresAt = "";
          row.revision = response.data && response.data.revision;
          setHostFSWriteStatus("released " + decisionId, "ok");
          renderPanel();
          return;
        }
        if (!row.claimToken) {
          setHostFSWriteStatus("claim this decision before " + action, "error");
          return;
        }
        const resource = action === "discard" ? "hostfs/write/discard" : "hostfs/write/apply";
        const response = await apiPost(resource, {decisionId: decisionId, expectedVersion: "hideout.hostfs-write-plan/v1", claimToken: row.claimToken, reason: "operator-denied"});
        if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
        row.claimToken = "";
        row.status = response.data && response.data.status || action;
        setHostFSWriteStatus(action + " " + row.status, "ok");
        renderPanel();
      } catch (error) {
        setHostFSWriteStatus(error.message || String(error), "error");
      }
    });
  });
}
function findDecisionRow(decisionId) {
  const rows = overview.decisionRows || [];
  for (let i = 0; i < rows.length; i++) {
    if (rows[i] && rows[i].id === decisionId) return rows[i];
  }
  return null;
}
function setDecisionStatus(text, tone) {
  const status = document.getElementById("decisionStatus");
  if (!status) return;
  status.className = tone === "error" ? "error-box" : "meta";
  status.textContent = text || "";
}
function decisionRouteForAction(action) {
  if (action === "deny") return "decision/deny";
  if (action === "approve") return "decision/approve";
  if (action === "release") return "decision/release";
  if (action === "reopen") return "decision/reopen";
  if (action === "revoke") return "decision/revoke";
  return "decision/claim";
}
function noticeAckRoute() {
  return "notice/ack";
}
function bindDecisionPanel() {
  document.querySelectorAll("[data-decision-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-decision-action");
      const decisionId = button.getAttribute("data-decision-id");
      const row = findDecisionRow(decisionId);
      if (!row) {
        setDecisionStatus("decision not found", "error");
        return;
      }
      try {
        if (action === "reopen" || action === "revoke") {
          const reason = action === "reopen" ? "operator-reopened" : "operator-revoked";
          const response = await apiPost(decisionRouteForAction(action), {decisionId: decisionId, expectedVersion: "hideout.decision/v1", reason: reason});
          if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
          row.claimToken = "";
          row.status = action === "reopen" ? "pending" : "denied";
          updateDecisionSummary();
          setDecisionStatus((action === "reopen" ? "reopened " : "revoked ") + decisionId, "ok");
          renderAll();
          return;
        }
        if (action === "claim") {
          const response = await apiPost(decisionRouteForAction(action), {decisionId: decisionId, expectedVersion: "hideout.decision/v1", surface: "webui", leaseSeconds: 60});
          if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
          row.claimToken = response.data && response.data.claimToken;
          row.status = "claimed";
          row.claimSurface = response.data && response.data.surface;
          row.claimedAt = response.data && response.data.claimedAt;
          row.claimExpiresAt = response.data && response.data.claimExpiresAt;
          row.revision = response.data && response.data.revision;
          updateDecisionSummary();
          setDecisionStatus("claimed " + decisionId + " until " + (row.claimExpiresAt || "lease expiry"), "ok");
          renderAll();
          return;
        }
        if (action === "release") {
          if (!row.claimToken) throw new Error("this browser does not own the claim token");
          const response = await apiPost(decisionRouteForAction(action), {decisionId: decisionId, expectedVersion: "hideout.decision/v1", expectedRevision: row.revision || 0, claimToken: row.claimToken, reason: "web decision dialog closed"});
          if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
          row.claimToken = "";
          row.status = "pending";
          row.claimSurface = "";
          row.claimOperator = "";
          row.claimedAt = "";
          row.claimExpiresAt = "";
          row.revision = response.data && response.data.revision;
          updateDecisionSummary();
          setDecisionStatus("released " + decisionId, "ok");
          renderAll();
          return;
        }
        if (!row.claimToken) {
          setDecisionStatus("claim this decision before " + action, "error");
          return;
        }
        const resource = decisionRouteForAction(action);
        const response = await apiPost(resource, {decisionId: decisionId, expectedVersion: "hideout.decision/v1", claimToken: row.claimToken, reason: action === "deny" ? "operator-denied" : "operator-approved"});
        if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
        row.claimToken = "";
        row.status = action === "deny" ? "denied" : "applied";
        updateDecisionSummary();
        setDecisionStatus(action + " " + decisionId, "ok");
        renderAll();
      } catch (error) {
        setDecisionStatus(error.message || String(error), "error");
      }
    });
  });
}
function findNoticeRow(noticeId) {
  const rows = overview.noticeRows || [];
  for (let i = 0; i < rows.length; i++) {
    if (rows[i] && rows[i].id === noticeId) return rows[i];
  }
  return null;
}
function setNoticeStatus(text, tone) {
  const status = document.getElementById("noticeStatus");
  if (!status) return;
  status.className = tone === "error" ? "error-box" : "meta";
  status.textContent = text || "";
}
function bindNoticePanel() {
  document.querySelectorAll("[data-notice-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const noticeId = button.getAttribute("data-notice-id");
      const row = findNoticeRow(noticeId);
      if (!row) {
        setNoticeStatus("notice not found", "error");
        return;
      }
      try {
        const response = await apiPost(noticeAckRoute(), {noticeId: noticeId, surface: "webui"});
        if (response.errors && response.errors.length) throw new Error(response.errors.join("; "));
        row.acknowledged = true;
        row.status = "acknowledged";
        updateNoticeSummary();
        setNoticeStatus("acknowledged " + noticeId, "ok");
        renderAll();
      } catch (error) {
        setNoticeStatus(error.message || String(error), "error");
      }
    });
  });
}
function profileEnvPayloadFromForm() {
  return {
    profile: document.getElementById("profileEnvProfile").value,
    operation: document.getElementById("profileEnvOperation").value,
    name: document.getElementById("profileEnvName").value.trim(),
    value: document.getElementById("profileEnvValue").value
  };
}
function renderProfileEnvResponse(resource, response) {
  const errors = response.errors || [];
  const data = response.data || {};
  const plan = data.plan || data;
  const header = item(resource, plan.profile || "profile", [["operation", plan.operation], ["kind", plan.kind], ["name", plan.name], ["valueProvided", plan.valueProvided], ["status", plan.status], ["changed", plan.changed], ["applied", data.applied], ["public", plan.publicAfter || data.public || []], ["inherit", plan.inheritAfter || data.inherit || []], ["deny", plan.denyAfter || data.deny || []]], errors.length ? "error" : plan.changed || data.applied ? "ok" : "info");
  const errorHTML = errors.map(function(err) { return '<div class="error-box">' + esc(err) + '</div>'; }).join("");
  return header + errorHTML;
}
function setProfileEnvBusy(busy, text) {
  const status = document.getElementById("profileEnvStatus");
  if (status) status.textContent = text || (busy ? "working" : "ready");
  document.querySelectorAll("[data-profile-env-action]").forEach(function(button) { button.disabled = busy; });
}
function bindProfileEnvPanel() {
  const form = document.getElementById("profileEnvForm");
  if (!form) return;
  form.addEventListener("submit", function(event) { event.preventDefault(); });
  if (selectedProfile && document.getElementById("profileEnvProfile")) {
    document.getElementById("profileEnvProfile").value = selectedProfile;
  }
  document.querySelectorAll("[data-profile-env-action]").forEach(function(button) {
    button.addEventListener("click", async function() {
      const action = button.getAttribute("data-profile-env-action");
      const resource = "profile/env/" + action;
      setProfileEnvBusy(true, action);
      try {
        const response = await apiPost(resource, profileEnvPayloadFromForm());
        profileEnvResultHTML = renderProfileEnvResponse(resource, response);
        document.getElementById("profileEnvResult").innerHTML = profileEnvResultHTML;
        setProfileEnvBusy(false, response.errors && response.errors.length ? "needs attention" : "ready");
        if (action === "apply" && !(response.errors && response.errors.length)) await load();
      } catch (error) {
        profileEnvResultHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
        document.getElementById("profileEnvResult").innerHTML = profileEnvResultHTML;
        setProfileEnvBusy(false, "error");
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
let liveLastSeq = 0;
let liveStreamState = "seeding";
let liveStreamReason = "";

function renderAll() {
  syncProfileScopeOptions();
  renderSummary();
  renderPanel();
  renderAuditTail();
}
function daemonAPI(path) {
  return fetch("/daemon/" + path, {headers: {"X-Hideout-UI-Token": token}}).then(async function(response) {
    const text = await response.text();
    let body = {};
    try { body = JSON.parse(text); } catch { body = {error: text}; }
    if (!response.ok) throw new Error(body.error || response.statusText);
    return body;
  });
}
function seedEmptyOverview() {
  return {profiles: [], environments: [], sessions: [], backends: [], network: {profileDefaults: []}, capabilities: {}, broker: {}, audit: {}, settings: {}, background: [], lifecycle: [], exportOutcomes: [], cleanupOutcomes: [], hostfsWrites: [], decisionRows: [], noticeRows: [], decisions: {pending: 0, claimed: 0, terminal: 0}, notices: {unacknowledged: 0, total: 0}};
}
function ensureLiveCollections() {
  if (!overview) overview = seedEmptyOverview();
  if (!Array.isArray(overview.environments)) overview.environments = [];
  if (!Array.isArray(overview.sessions)) overview.sessions = [];
  if (!Array.isArray(overview.background)) overview.background = [];
  if (!Array.isArray(overview.lifecycle)) overview.lifecycle = [];
  if (!Array.isArray(overview.exportOutcomes)) overview.exportOutcomes = [];
  if (!Array.isArray(overview.cleanupOutcomes)) overview.cleanupOutcomes = [];
  if (!Array.isArray(overview.hostfsWrites)) overview.hostfsWrites = [];
  if (!Array.isArray(overview.decisionRows)) overview.decisionRows = [];
  if (!Array.isArray(overview.noticeRows)) overview.noticeRows = [];
  if (!overview.decisions) overview.decisions = {pending: 0, claimed: 0, terminal: 0};
  if (!overview.notices) overview.notices = {unacknowledged: 0, total: 0};
}
function upsertByID(values, row) {
  const rows = Array.isArray(values) ? values.slice() : [];
  const id = row && row.id;
  if (!id) return rows;
  for (let i = 0; i < rows.length; i++) {
    if (rows[i] && rows[i].id === id) {
      rows[i] = Object.assign({}, rows[i], row);
      return rows;
    }
  }
  rows.unshift(row);
  return rows;
}
function workspaceViewActive(state) {
  return state === "provider-starting" || state === "provider-ready" || state === "view-mounting" || state === "ready" || state === "draining";
}
function workspaceProviderState(rows) {
  const rank = {"":0,"not-started":1,"released":2,"starting":3,"ready":4,"draining":5,"unproved":6};
  let selected = "";
  (rows || []).forEach(function(row) {
    const state = row && row.workspaceViewState;
    let next = "not-started";
    if (state === "provider-starting") next = "starting";
    else if (state === "provider-ready" || state === "view-mounting" || state === "ready") next = "ready";
    else if (state === "draining") next = "draining";
    else if (state === "released") next = "released";
    else if (state === "unproved") next = "unproved";
    if ((rank[next] || 0) > (rank[selected] || 0)) selected = next;
  });
  return selected;
}
function workspaceRelationRows(values) {
  if (!Array.isArray(values)) return [];
  return values.map(function(value) {
    return {
      relation: redactText(value && value.relation),
      selectedPosition: redactText(value && value.selectedPosition),
      workspaceId: redactText(value && value.workspaceId),
      otherWorkspaceId: redactText(value && value.otherWorkspaceId)
    };
  });
}
function workspaceViewRow(payload) {
  return {
    id: redactText(payload.session || payload.id),
    profile: redactText(payload.profile),
    environmentId: redactText(payload.environmentId),
    workspaceId: redactText(payload.workspaceId),
    workspaceLabel: redactText(payload.workspaceLabel),
    guestWorkspace: redactText(payload.guestWorkspace),
    workspaceTransport: redactText(payload.workspaceTransport),
    workspaceViewState: redactText(payload.workspaceViewState),
    workspaceRelations: workspaceRelationRows(payload.workspaceRelations),
    workspaceBlockerCode: redactText(payload.blockerCode),
    workspaceCleanupStatus: redactText(payload.cleanupStatus)
  };
}
function recomputeWorkspaceMachine(environmentId) {
  if (!environmentId) return;
  const views = (overview.sessions || []).filter(function(row) {
    return row && row.environmentId === environmentId && row.workspaceViewState;
  });
  const active = views.filter(function(row) { return workspaceViewActive(row.workspaceViewState); }).length;
  overview.environments = (overview.environments || []).map(function(row) {
    if (!row || row.id !== environmentId || row.mode !== "shared") return row;
    return Object.assign({}, row, {
      activeSessions: active,
      activeWorkspaceViews: active,
      workspaceProviderState: workspaceProviderState(views)
    });
  });
}
function capTail(values, limit) {
  const rows = Array.isArray(values) ? values.slice(0, limit || 20) : [];
  return rows;
}
function eventAuditRow(payload) {
  return {
    time: payload.time,
    session: redactText(payload.session),
    profile: redactText(payload.profile),
    backend: redactText(payload.backend),
    action: redactText(payload.action),
    decision: redactText(payload.decision),
    details: redactValue(payload.details || {})
  };
}
function decisionRowFromPayload(payload) {
  return {
    id: redactText(payload.decisionId || payload.id),
    kind: redactText(payload.recordKind),
    status: redactText(payload.status),
    defaultOutcome: redactText(payload.defaultOutcome),
    profile: redactText(payload.profile),
    session: redactText(payload.session),
    backend: redactText(payload.backend),
    reason: redactText(payload.reason),
    claimSurface: redactText(payload.claimSurface),
    claimOperator: redactText(payload.claimOperator),
    claimedAt: payload.claimedAt || "",
    claimExpiresAt: payload.claimExpiresAt || "",
    revision: Number(payload.revision || 0)
  };
}
function noticeRowFromPayload(payload) {
  return {
    id: redactText(payload.noticeId || payload.id),
    kind: redactText(payload.recordKind),
    status: redactText(payload.status),
    severity: redactText(payload.severity),
    acknowledged: !!payload.acknowledged,
    profile: redactText(payload.profile),
    session: redactText(payload.session),
    backend: redactText(payload.backend)
  };
}
function updateDecisionSummary() {
  ensureLiveCollections();
  const rows = overview.decisionRows || [];
  overview.decisions = rows.reduce(function(summary, row) {
    const status = row && row.status || "";
    if (status === "pending") summary.pending++;
    else if (status === "claimed") summary.claimed++;
    else summary.terminal++;
    return summary;
  }, {pending: 0, claimed: 0, terminal: 0});
}
function updateNoticeSummary() {
  ensureLiveCollections();
  const rows = overview.noticeRows || [];
  overview.notices = {
    unacknowledged: rows.filter(function(row) { return row && !row.acknowledged; }).length,
    total: rows.length
  };
}
function markLiveHealth(state, reason) {
  liveStreamState = state;
  liveStreamReason = reason || "";
  const tone = state === "live" || state === "idle-live" ? "ok" : "error";
  setStatus(state + (liveStreamReason ? " · " + liveStreamReason : ""), tone);
}
function validateLiveEvent(event) {
  if (!event || event.version !== "hideout.daemon-event/v1" || !event.kind || typeof event.seq !== "number") {
    markLiveHealth("schema-mismatch", "invalid event");
    return false;
  }
  if (event.seq <= liveLastSeq) return false;
  if (liveLastSeq && event.seq !== liveLastSeq + 1) {
    markLiveHealth("stale", "event sequence gap");
    return false;
  }
  liveLastSeq = event.seq;
  return true;
}
function applyLiveEvent(event) {
  if (!validateLiveEvent(event)) return false;
  ensureLiveCollections();
  const payload = event.payload || {};
  if (event.kind === "terminal") {
    markLiveHealth(payload.reason === "credential invalidated" ? "credential-expired" : "disconnected", payload.reason || "stream closed");
    renderAll();
    return false;
  }
  liveStreamState = "live";
  liveStreamReason = "";
  if (event.kind === "environment") {
    overview.environments = upsertByID(overview.environments, payload);
  } else if (event.kind === "session") {
    overview.sessions = upsertByID(overview.sessions, payload);
  } else if (event.kind === "workspace-view") {
    const row = workspaceViewRow(payload);
    if (!payload.attachmentId || !payload.session || !row.id || !row.environmentId || !row.workspaceId || !row.workspaceLabel || !row.guestWorkspace || !row.workspaceTransport || !row.workspaceViewState) {
      markLiveHealth("schema-mismatch", "invalid workspace-view event");
      renderAll();
      return false;
    }
    overview.sessions = upsertByID(overview.sessions, row);
    recomputeWorkspaceMachine(row.environmentId);
  } else if (event.kind === "background") {
    overview.background = upsertByID(overview.background, {id: payload.id, op: payload.op, status: payload.status});
  } else if (event.kind === "audit") {
    const row = eventAuditRow(payload);
    auditEvents = capTail([row].concat(auditEvents), 20);
    if (row.decision === "deny") deniedEvents = capTail([row].concat(deniedEvents), 20);
  } else if (event.kind === "export") {
    overview.exportOutcomes = capTail([{status: redactText(payload.status), source: redactText(payload.source), artifactPath: redactText(payload.artifactPath), decision: redactText(payload.decision)}].concat(overview.exportOutcomes), 20);
  } else if (event.kind === "cleanup") {
    overview.cleanupOutcomes = capTail([{status: redactText(payload.status), sessions: payload.sessions, removed: redactValue(payload.removed || []), secretState: redactText(payload.secretState)}].concat(overview.cleanupOutcomes), 20);
  } else if (event.kind === "hostfs-write") {
	 overview.hostfsWrites = upsertByID(overview.hostfsWrites, {id: redactText(payload.decisionId), decisionId: redactText(payload.decisionId), operationId: redactText(payload.operationId), profile: redactText(payload.profile), status: redactText(payload.status), operation: redactText(payload.operation), path: redactText(payload.path), destinationPath: redactText(payload.destinationPath), privilegeStatus: redactText(payload.privilegeStatus), reason: redactText(payload.reason)});
  } else if (event.kind === "decision") {
    overview.decisionRows = upsertByID(overview.decisionRows, decisionRowFromPayload(payload));
    updateDecisionSummary();
  } else if (event.kind === "notice") {
    overview.noticeRows = upsertByID(overview.noticeRows, noticeRowFromPayload(payload));
    updateNoticeSummary();
  } else if (event.kind === "lifecycle") {
    const row = redactValue(payload.lifecycle || {});
    if (!row.environmentId) return false;
    overview.lifecycle = upsertByID(overview.lifecycle, Object.assign({id: redactText(row.environmentId)}, row));
  } else {
    return false;
  }
  renderAll();
  const expiry = tokenExpiryLabel();
  setStatus("live · " + freshnessLabel() + (expiry ? " · " + expiry : ""), "ok");
  return true;
}
async function seedLiveConsole() {
  setStatus("connecting", "");
  try {
    const overviewResp = await api("overview");
    overview = Object.assign(seedEmptyOverview(), overviewResp.data || {});
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
    try {
      const status = await daemonAPI("status");
      overview.background = Array.isArray(status.background) ? status.background : [];
      overview.lifecycle = Array.isArray(status.lifecycle) ? status.lifecycle.map(function(row) { return Object.assign({id: redactText(row.environmentId)}, redactValue(row)); }) : [];
    } catch {}
    try {
	  const hostfsStatus = await api("hostfs/write/status" + (selectedProfile ? "?profile=" + encodeURIComponent(selectedProfile) : ""));
      overview.hostfsWrites = hostfsStatus.data && Array.isArray(hostfsStatus.data.pending) ? hostfsStatus.data.pending.map(function(row) {
        return Object.assign({}, row, {
          id: redactText(row.decisionId),
          decisionId: redactText(row.decisionId),
		  operationId: redactText(row.operationId),
		  profile: redactText(row.profile),
          status: redactText(row.status),
          operation: redactText(row.operation),
          path: redactText(row.path),
          destinationPath: redactText(row.destinationPath),
          privilegeStatus: redactText(row.privilegeStatus),
          reason: redactText(row.reason)
        });
      }) : [];
    } catch {
      overview.hostfsWrites = [];
    }
    try {
      const decisionsResp = await api("decisions?includeTerminal=true");
      overview.decisionRows = Array.isArray(decisionsResp.data) ? decisionsResp.data.map(function(row) {
        const facts = row.preview && row.preview.facts || {};
        const reason = row.kind === "hostfs.read" && facts.untrustedReason ? "untrusted target input: " + redactText(facts.untrustedReason) : row.preview && redactText(row.preview.summary);
        return {id: redactText(row.id), kind: redactText(row.kind), status: redactText(row.state || row.status), defaultOutcome: redactText(row.defaultOutcome), profile: row.source && redactText(row.source.profile), session: row.source && redactText(row.source.session), backend: row.source && redactText(row.source.backend), reason: reason, claimSurface: row.claim && redactText(row.claim.surface), claimOperator: row.claim && redactText(row.claim.operator), claimedAt: row.claim && row.claim.claimedAt, claimExpiresAt: row.claim && row.claim.expiresAt, revision: Number(row.revision || 0)};
      }) : [];
      updateDecisionSummary();
    } catch {
      overview.decisionRows = [];
      updateDecisionSummary();
    }
    try {
      const noticesResp = await api("notices");
      overview.noticeRows = Array.isArray(noticesResp.data) ? noticesResp.data.map(function(row) { return {id: redactText(row.id), kind: redactText(row.kind), status: redactText(row.status), severity: redactText(row.severity), acknowledged: !!row.acknowledged, profile: row.source && redactText(row.source.profile), session: row.source && redactText(row.source.session), backend: row.source && redactText(row.source.backend)}; }) : [];
      updateNoticeSummary();
    } catch {
      overview.noticeRows = [];
      updateNoticeSummary();
    }
    liveLastSeq = 0;
    liveStreamState = "live";
    liveStreamReason = "";
    renderAll();
    const expiry = tokenExpiryLabel();
    setStatus("connected · " + freshnessLabel() + (expiry ? " · " + expiry : ""), "ok");
  } catch (error) {
    overview = seedEmptyOverview();
    syncProfileScopeOptions();
    auditEvents = [];
    deniedEvents = [];
    summaryEl.innerHTML = "";
    panelBodyEl.innerHTML = '<div class="error-box">' + esc(error.message || error) + '</div>';
    auditBodyEl.innerHTML = empty("No audit events");
    setStatus("error", "error");
  }
}
function releaseOwnedClaims(reason) {
  if (!overview || !token) return Promise.resolve([]);
  const owned = [];
  const seen = new Set();
  [overview.decisionRows || [], overview.hostfsWrites || []].forEach(function(rows) {
    rows.forEach(function(row) {
      if (!row || !row.id && !row.decisionId || !row.claimToken) return;
      const decisionId = row.id || row.decisionId;
      if (seen.has(decisionId)) return;
      seen.add(decisionId);
      owned.push({decisionId: decisionId, claimToken: row.claimToken, revision: Number(row.revision || 0)});
      row.claimToken = "";
    });
  });
  return Promise.allSettled(owned.map(function(claim) {
    return fetch("/api/v1/decision/release", {
      method: "POST",
      headers: {"Content-Type": "application/json", "X-Hideout-UI-Token": token},
      body: JSON.stringify({decisionId: claim.decisionId, expectedVersion: "hideout.decision/v1", expectedRevision: claim.revision, claimToken: claim.claimToken, reason: reason || "webui disconnected"}),
      keepalive: true
    });
  }));
}
async function load() {
  await releaseOwnedClaims("webui refreshed or changed scope");
  return seedLiveConsole();
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
window.addEventListener("pagehide", function() { releaseOwnedClaims("webui page closed"); });
seedLiveConsole();
// Live state from the hideoutd event stream when served over the daemon's
// loopback UI transport. The stream applies typed event payloads to local state
// and renders without overview/audit re-fetches while healthy. When absent
// (command-scoped ui), the EventSource errors and is closed, and manual refresh
// keeps working unchanged.
try {
  if (window.EventSource && token) {
    const es = new EventSource("/daemon/events?token=" + encodeURIComponent(token));
    es.onmessage = function(message) {
      try {
        applyLiveEvent(JSON.parse(message.data));
      } catch {
        markLiveHealth("schema-mismatch", "invalid event json");
      }
    };
    es.onerror = function() { releaseOwnedClaims("webui event stream disconnected"); markLiveHealth("disconnected", "event stream closed"); es.close(); };
  }
} catch (e) {}
</script>
</body>
</html>`

func StripUIFragment(value string) string {
	if before, _, ok := strings.Cut(value, "#"); ok {
		return before
	}
	return value
}
