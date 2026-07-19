package manager

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestWebUIWorkspaceViewReducerKeepsOneMachineAndTwoScopedViewsWithoutFetch(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC))
	redaction := between(t, html, "function redactText(value)", "function list(value)")
	reducer := between(t, html, "let liveLastSeq = 0;", "async function seedLiveConsole()")
	runtime := goja.New()
	script := `
let overview = {
  environments: [{schema:"hideout.environment-summary/v1",id:"env_shared",name:"default",profile:"alpha",backend:"lima",status:"running",mode:"shared",sharedSlot:"default-alpha",machineIdentityId:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",activeSessions:0,activeWorkspaceViews:0}],
  sessions: [], network:{profileDefaults:[]}
};
let auditEvents = [];
let deniedEvents = [];
let token = "ui_0123456789abcdef0123456789abcdef";
let renderCount = 0;
let fetchCount = 0;
function syncProfileScopeOptions() {}
function renderSummary() {}
function renderPanel() { renderCount++; }
function renderAuditTail() {}
function setStatus() {}
function tokenExpiryLabel() { return ""; }
function freshnessLabel() { return "fresh"; }
function fetch() { fetchCount++; throw new Error("workspace-view reducer must not fetch"); }
` + redaction + reducer + `
const relationA = {relation:"disjoint",selectedPosition:"peer",workspaceId:"wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",otherWorkspaceId:"wrk_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"};
const relationB = {relation:"disjoint",selectedPosition:"peer",workspaceId:"wrk_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",otherWorkspaceId:"wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"};
const results = [];
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"workspace-view",seq:1,entity:{kind:"workspace-view",id:"att_a",profile:"alpha",session:"ses_a"},payload:{id:"ses_a",attachmentId:"att_a",session:"ses_a",environmentId:"env_shared",profile:"alpha",workspaceId:relationA.workspaceId,workspaceLabel:"project-a [aaaaaaaa]",guestWorkspace:"/workspace",workspaceTransport:"workspace-portal",workspaceViewState:"ready",workspaceRelations:[relationA],canonicalHostRoot:"/Users/private/a",providerCredential:"cap_0123456789abcdef0123456789abcdef"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"workspace-view",seq:2,entity:{kind:"workspace-view",id:"att_b",profile:"alpha",session:"ses_b"},payload:{id:"ses_b",attachmentId:"att_b",session:"ses_b",environmentId:"env_shared",profile:"alpha",workspaceId:relationB.workspaceId,workspaceLabel:"project-b [bbbbbbbb]",guestWorkspace:"/workspace",workspaceTransport:"workspace-portal",workspaceViewState:"ready",workspaceRelations:[relationB],rootHandleIdentity:"private-root-handle"}}));
const beforeRelease = JSON.parse(JSON.stringify(overview));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"future-workspace-view",seq:3,payload:{id:"future"}}));
results.push(applyLiveEvent({version:"hideout.daemon-event/v1",kind:"workspace-view",seq:4,entity:{kind:"workspace-view",id:"att_a",profile:"alpha",session:"ses_a"},payload:{id:"ses_a",attachmentId:"att_a",session:"ses_a",environmentId:"env_shared",profile:"alpha",workspaceId:relationA.workspaceId,workspaceLabel:"project-a [aaaaaaaa]",guestWorkspace:"/workspace",workspaceTransport:"workspace-portal",workspaceViewState:"released",workspaceRelations:[],cleanupStatus:"absent"}}));
JSON.stringify({results, beforeRelease, afterRelease:overview, fetchCount, renderCount, liveLastSeq, liveStreamState});
`
	value, err := runtime.RunString(script)
	if err != nil {
		t.Fatalf("run workspace-view WebUI reducer: %v", err)
	}
	var proof struct {
		Results       []bool `json:"results"`
		BeforeRelease struct {
			Environments []EnvironmentSummary `json:"environments"`
			Sessions     []SessionSummary     `json:"sessions"`
		} `json:"beforeRelease"`
		AfterRelease struct {
			Environments []EnvironmentSummary `json:"environments"`
			Sessions     []SessionSummary     `json:"sessions"`
		} `json:"afterRelease"`
		FetchCount      int    `json:"fetchCount"`
		RenderCount     int    `json:"renderCount"`
		LiveLastSeq     int    `json:"liveLastSeq"`
		LiveStreamState string `json:"liveStreamState"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatalf("decode workspace reducer proof: %v\n%s", err, value.String())
	}
	if len(proof.Results) != 4 || !proof.Results[0] || !proof.Results[1] || proof.Results[2] || !proof.Results[3] {
		t.Fatalf("workspace reducer results=%v", proof.Results)
	}
	if proof.FetchCount != 0 || proof.RenderCount != 3 || proof.LiveLastSeq != 4 || proof.LiveStreamState != "live" {
		t.Fatalf("workspace reducer runtime proof=%+v", proof)
	}
	if len(proof.BeforeRelease.Environments) != 1 || len(proof.BeforeRelease.Sessions) != 2 {
		t.Fatalf("before release machine/views=%+v", proof.BeforeRelease)
	}
	machine := proof.BeforeRelease.Environments[0]
	if machine.Mode != "shared" || machine.Workspace != "" || machine.GuestWorkspace != "" ||
		machine.ActiveSessions != 2 || machine.ActiveWorkspaceViews != 2 || machine.WorkspaceProviderState != "ready" {
		t.Fatalf("before release machine=%+v", machine)
	}
	for _, view := range proof.BeforeRelease.Sessions {
		if view.EnvironmentID != "env_shared" || view.Profile != "alpha" || view.WorkspaceID == "" ||
			view.WorkspaceLabel == "" || view.GuestWorkspace != "/workspace" || view.WorkspaceTransport != "workspace-portal" ||
			view.WorkspaceViewState != "ready" || len(view.WorkspaceRelations) != 1 {
			t.Fatalf("workspace view=%+v", view)
		}
	}
	if got := proof.AfterRelease.Environments[0]; got.ActiveSessions != 1 || got.ActiveWorkspaceViews != 1 || got.WorkspaceProviderState != "ready" {
		t.Fatalf("after release machine=%+v", got)
	}
	encoded := value.String()
	for _, forbidden := range []string{"/Users/private/a", "private-root-handle", "cap_0123456789abcdef"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("WebUI workspace state leaked %q: %s", forbidden, encoded)
		}
	}
}
