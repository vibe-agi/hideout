package manager

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestWebUILiveConsoleUsesSeedAndTypedEvents(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"function seedLiveConsole()",
		"function applyLiveEvent(event)",
		"function validateLiveEvent(event)",
		"function renderAll()",
		`data-panel="operator-console"`,
		"function consoleActionSummary()",
		"new EventSource(\"/daemon/events?token=\"",
		"applyLiveEvent(JSON.parse(message.data))",
		"overview.environments = upsertByID(overview.environments, payload)",
		"overview.sessions = upsertByID(overview.sessions, payload)",
		"overview.hostfsWrites = upsertByID(overview.hostfsWrites",
		"overview.decisionRows = upsertByID(overview.decisionRows",
		"overview.noticeRows = upsertByID(overview.noticeRows",
		"auditEvents = capTail([row].concat(auditEvents), 20)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("WebUI live console HTML missing %q", want)
		}
	}
	if strings.Contains(html, "setInterval") {
		t.Fatal("WebUI live console must not use a polling timer")
	}
	onMessage := between(t, html, "es.onmessage = function(message)", "es.onerror = function()")
	for _, forbidden := range []string{"load()", "seedLiveConsole()", `api("overview")`, `api("audit/events`} {
		if strings.Contains(onMessage, forbidden) {
			t.Fatalf("EventSource handler must not re-fetch seed data; found %q in:\n%s", forbidden, onMessage)
		}
	}
}

func TestWebUIOperatorConsolePanelRuntime(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	helpers := between(t, html, "function esc(value)", "function splitCSV(value)")
	rt := goja.New()
	script := `
let selectedProfile = "";
let activePanel = "operator-console";
let liveStreamState = "live";
let liveStreamReason = "";
let liveLastSeq = 42;
let auditEvents = [];
let deniedEvents = [];
let panelTitleText = "";
let panelMeta = "";
let panelBody = "";
const panelTitleEl = {set textContent(value) { panelTitleText = value; }};
const panelMetaEl = {set textContent(value) { panelMeta = value; }};
const panelBodyEl = {set innerHTML(value) { panelBody = value; }};
const summaryEl = {set innerHTML(value) {}};
const auditBodyEl = {set innerHTML(value) {}};
const auditMetaEl = {set textContent(value) {}};
const profileScopeEl = {innerHTML:"", value:""};
function syncProfileScopeOptions() {}
function bindSetupPanel() {}
function bindRunPanel() {}
function bindProfileEnvPanel() {}
function bindEnvironmentPanel() {}
function bindCommandProxyPanel() {}
function bindHostFSPanel() {}
function bindHostFSWritePanel() {}
function bindDecisionPanel() {}
function bindNoticePanel() {}
function bindAuditPanel() {}
let overview = {
  profiles: [{name:"default"}],
  environments: [{id:"env_1", name:"work", status:"running", profile:"default"}],
  sessions: [],
  backends: [{name:"lima", available:true}],
  network: {profileDefaults: []},
  capabilities: {}, broker: {}, audit: {}, settings: {}, bundles: {installed:1, enabled:1},
  adapterPacks: [{packId:"root-sensitive"}],
  background: [{id:"bg-1", op:"environment-clean", status:"running"}],
  hostfsWrites: [{id:"hfwdec_1", decisionId:"hfwdec_1", operation:"replace", status:"pending"}],
  decisionRows: [{id:"dec_1", kind:"evidence.share", status:"pending", defaultOutcome:"deny"}],
  noticeRows: [{id:"notice_1", kind:"privilege.status", status:"degraded", severity:"warning", acknowledged:false}],
  decisions: {pending:1, claimed:0, terminal:0},
  notices: {unacknowledged:1, total:1}
};
renderPanel();
JSON.stringify({panelTitle: panelTitleText, panelMeta, panelBody});
`
	value, err := rt.RunString(helpers + script)
	if err != nil {
		t.Fatalf("run operator console panel: %v", err)
	}
	var out struct {
		PanelTitle string `json:"panelTitle"`
		PanelMeta  string `json:"panelMeta"`
		PanelBody  string `json:"panelBody"`
	}
	if err := json.Unmarshal([]byte(value.String()), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, value.String())
	}
	for _, want := range []string{"Operator Console", "manager/operator-console", "Action Required", "Doctor", "Package", "Support", "HostFS Writes", "Decisions", "Notices", "Stream", "hideout doctor --level light"} {
		if !strings.Contains(out.PanelTitle+out.PanelMeta+out.PanelBody, want) {
			t.Fatalf("operator console output missing %q:\n%+v", want, out)
		}
	}
}

func TestWebUIProfileScopeFiltersActionRowsAndSummaries(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	scopeHelpers := between(t, html, "function filterByProfile(values, key)", "function withScopedOverview(fn)")
	rt := goja.New()
	value, err := rt.RunString(`
let selectedProfile = "alpha";
let overview = {
  profiles: [{name:"alpha"}, {name:"beta"}],
  environments: [], sessions: [], network: {profileDefaults: []},
  hostfsWrites: [
    {decisionId:"hfw-alpha", profile:"alpha", status:"pending"},
    {decisionId:"hfw-beta", profile:"beta", status:"pending"}
  ],
  decisionRows: [
    {id:"dec-alpha", profile:"alpha", status:"pending"},
    {id:"dec-beta", profile:"beta", status:"claimed"}
  ],
  noticeRows: [
    {id:"notice-alpha", profile:"alpha", acknowledged:false},
    {id:"notice-beta", profile:"beta", acknowledged:false}
  ]
};
` + scopeHelpers + `
JSON.stringify(scopedOverview());
`)
	if err != nil {
		t.Fatalf("run scoped overview: %v", err)
	}
	var scoped struct {
		HostFSWrites []struct {
			DecisionID string `json:"decisionId"`
		} `json:"hostfsWrites"`
		DecisionRows []struct {
			ID string `json:"id"`
		} `json:"decisionRows"`
		NoticeRows []struct {
			ID string `json:"id"`
		} `json:"noticeRows"`
		Decisions DecisionSummary `json:"decisions"`
		Notices   NoticeSummary   `json:"notices"`
	}
	if err := json.Unmarshal([]byte(value.String()), &scoped); err != nil {
		t.Fatal(err)
	}
	if len(scoped.HostFSWrites) != 1 || scoped.HostFSWrites[0].DecisionID != "hfw-alpha" ||
		len(scoped.DecisionRows) != 1 || scoped.DecisionRows[0].ID != "dec-alpha" ||
		len(scoped.NoticeRows) != 1 || scoped.NoticeRows[0].ID != "notice-alpha" {
		t.Fatalf("cross-profile action rows visible: %+v", scoped)
	}
	if scoped.Decisions.Pending != 1 || scoped.Decisions.Claimed != 0 || scoped.Notices.Unacknowledged != 1 || scoped.Notices.Total != 1 {
		t.Fatalf("scoped summaries include another profile: decisions=%+v notices=%+v", scoped.Decisions, scoped.Notices)
	}
}

func TestWebUIConsoleActionRoutesUseExistingManagerAPI(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	apiHelpers := between(t, html, "function api(path)", "function renderSummary()")
	helpers := between(t, html, "function findDecisionRow(decisionId)", "function profileEnvPayloadFromForm()")
	rt := goja.New()
	value, err := rt.RunString(`
	var token = "ui_test_token";
	var calls = [];
	function makeButton(attrs) {
	  return {
	    attrs: attrs,
	    handler: null,
	    getAttribute: function(name) { return this.attrs[name]; },
	    addEventListener: function(name, fn) { if (name === "click") this.handler = fn; }
	  };
	}
	var decisionButton = makeButton({"data-decision-action": "claim", "data-decision-id": "dec_1"});
	var reopenButton = makeButton({"data-decision-action": "reopen", "data-decision-id": "dec_hfr_1"});
	var noticeButton = makeButton({"data-notice-action": "ack", "data-notice-id": "notice_1"});
	var overview = {
	  decisionRows: [{id: "dec_1", status: "pending"}, {id: "dec_hfr_1", kind: "hostfs.read", status: "denied"}],
	  noticeRows: [{id: "notice_1", status: "degraded", acknowledged: false}]
	};
	function fetch(path, opts) {
	  calls.push({path: path, method: (opts && opts.method) || "GET", body: opts && opts.body || ""});
	  return {then: function() { return {then: function() { return this; }, catch: function() { return this; }}; }};
	}
	var document = {
	  querySelectorAll: function(selector) {
	    if (selector === "[data-decision-action]") return [decisionButton, reopenButton];
	    if (selector === "[data-notice-action]") return [noticeButton];
	    return [];
	  },
	  getElementById: function() { return {className: "", textContent: ""}; }
	};
	function updateDecisionSummary() {}
	function updateNoticeSummary() {}
	function renderAll() {}
	` + apiHelpers + helpers + `
	bindDecisionPanel();
	bindNoticePanel();
	decisionButton.handler();
	reopenButton.handler();
	noticeButton.handler();
	JSON.stringify(calls);
	`)
	if err != nil {
		t.Fatalf("run action route runtime: %v", err)
	}
	var calls []struct {
		Path   string `json:"path"`
		Method string `json:"method"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(value.String()), &calls); err != nil {
		t.Fatalf("decode runtime calls: %v\n%s", err, value.String())
	}
	if len(calls) != 3 {
		t.Fatalf("runtime calls=%+v, want decision claim, HostFS read reopen, and notice ack", calls)
	}
	for _, call := range calls {
		spec, ok := RecognizeManagerRoute(call.Method, call.Path)
		if !ok {
			t.Fatalf("runtime action targeted unknown route: %+v", call)
		}
		if spec.Class != RouteClassManagerAPI {
			t.Fatalf("runtime action targeted wrong route class: %+v spec=%+v", call, spec)
		}
		if call.Method != "POST" {
			t.Fatalf("runtime action used %s, want POST: %+v", call.Method, call)
		}
	}
	if !strings.Contains(calls[1].Path, "decision/reopen") || !strings.Contains(calls[1].Body, "dec_hfr_1") {
		t.Fatalf("HostFS read reopen did not target authenticated Manager route: %+v", calls[1])
	}
	for _, marker := range []string{`data-decision-action="claim"`, `data-decision-action="approve"`, `data-decision-action="deny"`, `data-decision-action="reopen"`, `data-notice-action="ack"`, `expectedVersion: "hideout.decision/v1"`} {
		if !strings.Contains(html, marker) {
			t.Fatalf("WebUI action markup missing %q", marker)
		}
	}
}

func TestWebUILiveConsoleHealthAndRedactionContracts(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	tokenPrefix := "cap_" + "0123456789abcdef"
	secretValue := "HIDEOUT_" + "SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1"
	for _, want := range []string{
		`markLiveHealth("schema-mismatch", "invalid event")`,
		`markLiveHealth("stale", "event sequence gap")`,
		`markLiveHealth(payload.reason === "credential invalidated" ? "credential-expired" : "disconnected"`,
		`markLiveHealth("disconnected", "event stream closed")`,
		`history.replaceState(null, document.title, location.pathname + location.search)`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("WebUI live console missing health/redaction contract %q", want)
		}
	}
	for _, forbidden := range []string{
		"localStorage",
		"sessionStorage",
		tokenPrefix,
		secretValue,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("WebUI live console should not contain %q", forbidden)
		}
	}
}

func TestWebUILiveConsoleReducerIsAuthorityReadOnly(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	reducer := between(t, html, "function applyLiveEvent(event)", "async function seedLiveConsole()")
	for _, forbidden := range []string{"apiPost(", "fetch(", "/api/v1/", "run/apply", "environment/clean/apply"} {
		if strings.Contains(reducer, forbidden) {
			t.Fatalf("live reducer must be read-only; found %q in:\n%s", forbidden, reducer)
		}
	}
	for _, want := range []string{
		`data-run-action="apply"`,
		`data-environment-mode="apply"`,
		`profile/command-proxy/`,
		`profile/hostfs/`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("existing action surface missing %q", want)
		}
	}
}

func TestWebUIProfilesRenderCommandAdapterSummary(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	helpers := between(t, html, "function esc(value)", "function boundaryRowsFromSummary(summary)")
	rt := goja.New()
	value, err := rt.RunString(helpers + `
JSON.stringify(commandAdapterLabels([
  {id:"adapter", enabled:true, commands:["tool-x"]},
  {id:"disabled", enabled:false, commands:[]}
]));
`)
	if err != nil {
		t.Fatalf("run WebUI command adapter helper: %v", err)
	}
	var labels []string
	if err := json.Unmarshal([]byte(value.String()), &labels); err != nil {
		t.Fatalf("decode labels: %v\n%s", err, value.String())
	}
	if len(labels) != 2 || labels[0] != "adapter:on(tool-x)" || labels[1] != "disabled:off(none)" {
		t.Fatalf("unexpected command adapter labels: %+v", labels)
	}
}

func TestWebUISessionsRenderGuestPrivilegeSummary(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	helpers := between(t, html, "function esc(value)", "function boundaryRowsFromSummary(summary)")
	rt := goja.New()
	value, err := rt.RunString(helpers + `
guestPrivilegeLabel({
  status: "degraded",
  targetUid: "1000",
  setupKind: "shared-sudo",
  reason: "target user can run passwordless sudo"
});
`)
	if err != nil {
		t.Fatalf("run WebUI privilege helper: %v", err)
	}
	if got, want := value.String(), "degraded uid=1000 setup=shared-sudo target user can run passwordless sudo"; got != want {
		t.Fatalf("privilege label=%q want %q", got, want)
	}
}

func TestWebUIRendersConcurrentOwnerFieldsWithoutPolling(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC))
	for _, marker := range []string{
		`["activeSessions", e.activeSessions || 0]`,
		`["ownerHealth", e.ownerHealth]`,
		`["ownerStatus", s.ownerStatus]`,
		`["terminalMode", s.terminalMode]`,
		`s.ownerStatus === "unprovable"`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("WebUI owner rendering missing %q", marker)
		}
	}
	onMessage := between(t, html, "es.onmessage = function(message)", "es.onerror = function()")
	if strings.Contains(onMessage, "setInterval") || strings.Contains(onMessage, `api("overview"`) {
		t.Fatalf("owner event rendering introduced polling: %s", onMessage)
	}
}

func TestWebUILiveConsoleProofArtifact(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	onMessage := between(t, html, "es.onmessage = function(message)", "es.onerror = function()")
	reducer := between(t, html, "function applyLiveEvent(event)", "async function seedLiveConsole()")
	proof := struct {
		EventHandlerAppliesPayload bool
		OverviewReads              int
		AuditReads                 int
		RedactionOK                bool
		HealthRendered             bool
	}{
		EventHandlerAppliesPayload: strings.Contains(onMessage, "applyLiveEvent(JSON.parse(message.data))"),
		OverviewReads:              strings.Count(onMessage, `api("overview"`),
		AuditReads:                 strings.Count(onMessage, `api("audit/events`),
		RedactionOK:                !strings.Contains(reducer, "cap_"+"0123456789abcdef") && !strings.Contains(reducer, "HIDEOUT_"+"SECRET_"),
		HealthRendered:             strings.Contains(html, `markLiveHealth("stale", "event sequence gap")`) && strings.Contains(html, "credential-expired"),
	}
	if !proof.EventHandlerAppliesPayload || proof.OverviewReads != 0 || proof.AuditReads != 0 || !proof.RedactionOK || !proof.HealthRendered {
		t.Fatalf("WebUI live proof failed: %+v\nhandler:\n%s", proof, onMessage)
	}
	t.Logf("WebUI live proof: %+v", proof)
}

func TestWebUILiveConsoleReducerExecutesTypedEventsWithoutFetch(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	redaction := between(t, html, "function redactText(value)", "function list(value)")
	reducer := between(t, html, "let liveLastSeq = 0;", "async function seedLiveConsole()")
	rt := goja.New()
	script := `
let overview = null;
let auditEvents = [];
let deniedEvents = [];
let token = "ui_0123456789abcdef0123456789abcdef";
let renderCount = 0;
let fetchCount = 0;
let statuses = [];
function syncProfileScopeOptions() {}
function renderSummary() {}
function renderPanel() { renderCount++; }
function renderAuditTail() {}
function setStatus(value, tone) { statuses.push(value + ":" + (tone || "")); }
function tokenExpiryLabel() { return ""; }
function freshnessLabel() { return "fresh"; }
function fetch() { fetchCount++; throw new Error("live reducer must not fetch"); }
` + redaction + reducer + `
const results = [];
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"environment",seq:1,payload:{id:"env_1",name:"env-one",status:"running",profile:"default"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"audit",seq:2,payload:{action:"host.open",decision:"deny",profile:"default",session:"ses_1",details:{target:"https://example.com",capabilityToken:"cap_0123456789abcdef0123456789abcdef",message:"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1",note:"keep-me"}}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"export",seq:3,payload:{status:"completed",source:"audit",artifactPath:"/tmp/export.json",decision:"redact"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"cleanup",seq:4,payload:{status:"completed",sessions:1,removed:["tmp"],secretState:"removed"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"hostfs-write",seq:5,payload:{decisionId:"hfwdec_123",operationId:"hfwop_123",status:"pending",operation:"replace",path:"/hostfs-overlay/objects/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",destinationPath:"/Users/alice/file.txt",privilegeStatus:"enforced",reason:"keep-me"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"decision",seq:6,payload:{decisionId:"dec_share_123",recordKind:"evidence.share",status:"pending",defaultOutcome:"deny",profile:"default",session:"ses_1",backend:"native"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"notice",seq:7,payload:{noticeId:"notice_priv_123",recordKind:"privilege.status",status:"degraded",severity:"warning",acknowledged:false,profile:"default",session:"ses_1",backend:"lima"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"notice",seq:8,payload:{noticeId:"notice_priv_123",recordKind:"privilege.status",status:"degraded",severity:"warning",acknowledged:true,profile:"default",session:"ses_1",backend:"lima"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"lifecycle",seq:9,payload:{lifecycle:{schema:"hideout.lifecycle-status/v1",environmentId:"env_1",startGeneration:2,backendState:"running",backendObservedAt:"2026-07-16T05:00:00Z",activity:"idle-grace",reconciliation:"complete",retained:[{kind:"hostfs.staged-object",id:"overlay-one",state:"released"}]}}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"future-kind",seq:10,payload:{id:"future"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"environment",seq:11,payload:{id:"env_2",name:"env-two",status:"stopped",profile:"default"}}));
JSON.stringify({results, overview, auditEvents, deniedEvents, renderCount, fetchCount, liveStreamState, liveStreamReason, liveLastSeq, statuses});
`
	value, err := rt.RunString(script)
	if err != nil {
		t.Fatalf("run WebUI reducer harness: %v", err)
	}
	var proof struct {
		Results  []bool `json:"results"`
		Overview struct {
			Environments []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"environments"`
			ExportOutcomes []struct {
				Status       string `json:"status"`
				Source       string `json:"source"`
				ArtifactPath string `json:"artifactPath"`
				Decision     string `json:"decision"`
			} `json:"exportOutcomes"`
			CleanupOutcomes []struct {
				Status      string   `json:"status"`
				Sessions    int      `json:"sessions"`
				Removed     []string `json:"removed"`
				SecretState string   `json:"secretState"`
			} `json:"cleanupOutcomes"`
			HostFSWrites []struct {
				DecisionID      string `json:"decisionId"`
				OperationID     string `json:"operationId"`
				Status          string `json:"status"`
				Operation       string `json:"operation"`
				Path            string `json:"path"`
				DestinationPath string `json:"destinationPath"`
				PrivilegeStatus string `json:"privilegeStatus"`
			} `json:"hostfsWrites"`
			DecisionRows []struct {
				ID             string `json:"id"`
				Kind           string `json:"kind"`
				Status         string `json:"status"`
				DefaultOutcome string `json:"defaultOutcome"`
			} `json:"decisionRows"`
			NoticeRows []struct {
				ID           string `json:"id"`
				Kind         string `json:"kind"`
				Status       string `json:"status"`
				Severity     string `json:"severity"`
				Acknowledged bool   `json:"acknowledged"`
			} `json:"noticeRows"`
			Decisions struct {
				Pending  int `json:"pending"`
				Claimed  int `json:"claimed"`
				Terminal int `json:"terminal"`
			} `json:"decisions"`
			Notices struct {
				Unacknowledged int `json:"unacknowledged"`
				Total          int `json:"total"`
			} `json:"notices"`
			Lifecycle []struct {
				EnvironmentID   string `json:"environmentId"`
				StartGeneration int    `json:"startGeneration"`
				Activity        string `json:"activity"`
				Retained        []struct {
					Kind string `json:"kind"`
				} `json:"retained"`
			} `json:"lifecycle"`
		} `json:"overview"`
		AuditEvents []struct {
			Action   string         `json:"action"`
			Decision string         `json:"decision"`
			Details  map[string]any `json:"details"`
		} `json:"auditEvents"`
		DeniedEvents     []struct{} `json:"deniedEvents"`
		RenderCount      int        `json:"renderCount"`
		FetchCount       int        `json:"fetchCount"`
		LiveStreamState  string     `json:"liveStreamState"`
		LiveStreamReason string     `json:"liveStreamReason"`
		LiveLastSeq      int        `json:"liveLastSeq"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatalf("decode reducer proof: %v\n%s", err, value.String())
	}
	if proof.FetchCount != 0 {
		t.Fatalf("live reducer fetched during event apply: %+v", proof)
	}
	if len(proof.Results) != 11 || !proof.Results[0] || !proof.Results[1] || !proof.Results[2] || !proof.Results[3] || !proof.Results[4] || !proof.Results[5] || !proof.Results[6] || !proof.Results[7] || !proof.Results[8] || proof.Results[9] || !proof.Results[10] {
		t.Fatalf("unexpected apply results: %+v", proof.Results)
	}
	if len(proof.Overview.Environments) != 2 || proof.Overview.Environments[0].ID != "env_2" || proof.Overview.Environments[1].ID != "env_1" {
		t.Fatalf("environment events did not update visible state: %+v", proof.Overview.Environments)
	}
	if len(proof.AuditEvents) != 1 || proof.AuditEvents[0].Decision != "deny" || len(proof.DeniedEvents) != 1 {
		t.Fatalf("audit event did not update tails: audit=%+v denied=%+v", proof.AuditEvents, proof.DeniedEvents)
	}
	if data, err := json.Marshal(proof.AuditEvents[0].Details); err != nil {
		t.Fatal(err)
	} else if text := string(data); strings.Contains(text, "cap_"+"0123456789abcdef") || strings.Contains(text, "socks5://127.0.0.1:1") || !strings.Contains(text, "keep-me") {
		t.Fatalf("audit details redaction mismatch: %s", text)
	}
	if len(proof.Overview.ExportOutcomes) != 1 || proof.Overview.ExportOutcomes[0].ArtifactPath != "/tmp/export.json" {
		t.Fatalf("export event did not update visible state: %+v", proof.Overview.ExportOutcomes)
	}
	if len(proof.Overview.CleanupOutcomes) != 1 || proof.Overview.CleanupOutcomes[0].SecretState != "removed" {
		t.Fatalf("cleanup event did not update visible state: %+v", proof.Overview.CleanupOutcomes)
	}
	if len(proof.Overview.HostFSWrites) != 1 || proof.Overview.HostFSWrites[0].DecisionID != "hfwdec_123" {
		t.Fatalf("HostFS write event did not update visible state: %+v", proof.Overview.HostFSWrites)
	}
	if strings.Contains(proof.Overview.HostFSWrites[0].Path, "0123456789abcdef") || proof.Overview.HostFSWrites[0].DestinationPath != "/Users/alice/file.txt" {
		t.Fatalf("HostFS path redaction mismatch: %+v", proof.Overview.HostFSWrites[0])
	}
	if len(proof.Overview.DecisionRows) != 1 || proof.Overview.DecisionRows[0].ID != "dec_share_123" || proof.Overview.Decisions.Pending != 1 {
		t.Fatalf("decision event did not update visible state: %+v summary=%+v", proof.Overview.DecisionRows, proof.Overview.Decisions)
	}
	if len(proof.Overview.NoticeRows) != 1 || proof.Overview.NoticeRows[0].ID != "notice_priv_123" || !proof.Overview.NoticeRows[0].Acknowledged || proof.Overview.Notices.Unacknowledged != 0 {
		t.Fatalf("notice event did not update visible state: %+v summary=%+v", proof.Overview.NoticeRows, proof.Overview.Notices)
	}
	if len(proof.Overview.Lifecycle) != 1 || proof.Overview.Lifecycle[0].EnvironmentID != "env_1" || proof.Overview.Lifecycle[0].Activity != "idle-grace" || len(proof.Overview.Lifecycle[0].Retained) != 1 {
		t.Fatalf("lifecycle event did not update visible state: %+v", proof.Overview.Lifecycle)
	}
	if proof.RenderCount < 10 || proof.LiveLastSeq != 11 || proof.LiveStreamState != "live" || proof.LiveStreamReason != "" {
		t.Fatalf("stream proof mismatch: %+v", proof)
	}
}

func between(t *testing.T, text, start, end string) string {
	t.Helper()
	i := strings.Index(text, start)
	if i < 0 {
		t.Fatalf("start marker %q not found", start)
	}
	text = text[i:]
	j := strings.Index(text, end)
	if j < 0 {
		t.Fatalf("end marker %q not found after %q", end, start)
	}
	return text[:j]
}
