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

func TestWebUILiveConsoleHealthAndRedactionContracts(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	tokenPrefix := "cap_" + "0123456789abcdef"
	secretPrefix := "HIDEOUT_" + "SECRET_"
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
		secretPrefix,
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
` + reducer + `
const results = [];
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"environment",seq:1,payload:{id:"env_1",name:"env-one",status:"running",profile:"default"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"audit",seq:2,payload:{action:"host.open",decision:"deny",profile:"default",session:"ses_1",details:{target:"https://example.com"}}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"export",seq:3,payload:{status:"completed",source:"audit",artifactPath:"/tmp/export.json",decision:"redact"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"cleanup",seq:4,payload:{status:"completed",sessions:1,removed:["tmp"],secretState:"removed"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"hostfs-write",seq:5,payload:{decisionId:"hfwdec_123",operationId:"hfwop_123",status:"pending",operation:"replace",path:"/Users/alice/file.txt",privilegeStatus:"enforced"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"decision",seq:6,payload:{decisionId:"dec_share_123",recordKind:"evidence.share",status:"pending",defaultOutcome:"deny",profile:"default",session:"ses_1",backend:"native"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"notice",seq:7,payload:{noticeId:"notice_priv_123",recordKind:"privilege.status",status:"degraded",severity:"warning",acknowledged:false,profile:"default",session:"ses_1",backend:"lima"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"notice",seq:8,payload:{noticeId:"notice_priv_123",recordKind:"privilege.status",status:"degraded",severity:"warning",acknowledged:true,profile:"default",session:"ses_1",backend:"lima"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"future-kind",seq:9,payload:{id:"future"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"environment",seq:10,payload:{id:"env_2",name:"env-two",status:"stopped",profile:"default"}}));
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
	if len(proof.Results) != 10 || !proof.Results[0] || !proof.Results[1] || !proof.Results[2] || !proof.Results[3] || !proof.Results[4] || !proof.Results[5] || !proof.Results[6] || !proof.Results[7] || proof.Results[8] || !proof.Results[9] {
		t.Fatalf("unexpected apply results: %+v", proof.Results)
	}
	if len(proof.Overview.Environments) != 2 || proof.Overview.Environments[0].ID != "env_2" || proof.Overview.Environments[1].ID != "env_1" {
		t.Fatalf("environment events did not update visible state: %+v", proof.Overview.Environments)
	}
	if len(proof.AuditEvents) != 1 || proof.AuditEvents[0].Decision != "deny" || len(proof.DeniedEvents) != 1 {
		t.Fatalf("audit event did not update tails: audit=%+v denied=%+v", proof.AuditEvents, proof.DeniedEvents)
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
	if len(proof.Overview.DecisionRows) != 1 || proof.Overview.DecisionRows[0].ID != "dec_share_123" || proof.Overview.Decisions.Pending != 1 {
		t.Fatalf("decision event did not update visible state: %+v summary=%+v", proof.Overview.DecisionRows, proof.Overview.Decisions)
	}
	if len(proof.Overview.NoticeRows) != 1 || proof.Overview.NoticeRows[0].ID != "notice_priv_123" || !proof.Overview.NoticeRows[0].Acknowledged || proof.Overview.Notices.Unacknowledged != 0 {
		t.Fatalf("notice event did not update visible state: %+v summary=%+v", proof.Overview.NoticeRows, proof.Overview.Notices)
	}
	if proof.RenderCount < 9 || proof.LiveLastSeq != 10 || proof.LiveStreamState != "live" || proof.LiveStreamReason != "" {
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
