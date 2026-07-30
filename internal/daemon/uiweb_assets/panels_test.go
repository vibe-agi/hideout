package uiweb_assets

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestActivityViewModelsCoverOwnerTimelineTreeSubjectsAndEvidence(
	t *testing.T,
) {
	runtime := goja.New()
	source := mustAsset("activity.js")
	value, err := runtime.RunString(`
var window = {HideoutConsole: {}};
` + source + `
const snapshot = {
  sessions: [{id:"ses_alpha", environmentId:"env_alpha"}],
  activity: [{
    id:"act_file000001", sessionId:"ses_alpha",
    owner:{kind:"reusable-environment",environmentId:"env_alpha",backend:"lima",backendIncarnationId:"inc-alpha"},
    kind:"file", operation:"write", count:2, bytes:7,
    firstAt:"2026-07-29T10:00:00Z", lastAt:"2026-07-29T10:00:01Z",
    actor:{executionId:"exec_alpha0001",pid:12},
    subject:{kind:"file",path:"/workspace/a.txt",pathClass:"workspace",destructive:false},
    outcome:{status:"observed"}, attribution:"exact", coverageId:"cov_alpha0001"
  }, {
    id:"act_net0000001", sessionId:"ses_alpha",
    owner:{kind:"reusable-environment",environmentId:"env_alpha",backend:"lima",backendIncarnationId:"inc-alpha"},
    kind:"connection", operation:"connect", count:1,
    firstAt:"2026-07-29T10:00:02Z", lastAt:"2026-07-29T10:00:02Z",
    actor:{executionId:"exec_alpha0001",pid:12},
    subject:{kind:"connection",protocol:"tcp",ip:"1.1.1.1",port:443,domain:"example.com",domainAttribution:"dns-exact",route:"proxy"},
    outcome:{status:"observed"}, attribution:"exact", coverageId:"cov_alpha0002"
  }, {
    id:"act_dns0000001", sessionId:"ses_alpha",
    owner:{kind:"reusable-environment",environmentId:"env_alpha",backend:"lima",backendIncarnationId:"inc-alpha"},
    kind:"dns", operation:"query", count:1,
    firstAt:"2026-07-29T10:00:03Z", lastAt:"2026-07-29T10:00:03Z",
    subject:{kind:"dns",query:"example.com",queryType:"A",answers:["1.1.1.1"],responseCode:"NOERROR"},
    outcome:{status:"observed"}, attribution:"exact", coverageId:"cov_alpha0003"
  }],
  coverage: [],
  activityRetention: []
};
const tree = [{
  execution:{id:"exec_alpha0001",executable:"claude",argv:["claude"],pid:12,startedAt:"2026-07-29T10:00:00Z",guestIdentity:{uid:1000,user:"agent"}},
  activityCounts:{file:2,connection:1},
  children:[{
    execution:{id:"exec_child0001",parentExecutionId:"exec_alpha0001",executable:"git",argv:["git","status"],pid:13,startedAt:"2026-07-29T10:00:01Z",identity:{user:"agent"},exit:{code:0}},
    activityCounts:{file:1},
    children:[]
  }]
}];
const coverage = window.HideoutConsole.Activity.coverageView({
  id:"cov_alpha0001", subsystem:"dns", state:"partial", reason:"events-dropped",
  droppedEventCount:3, retentionGap:true, startedAt:"2026-07-29T10:00:00Z",
  evidence:[{code:"drop-count",value:"3"}]
});
const risk = window.HideoutConsole.Activity.riskView({
  id:"risk_alpha0001", ruleId:"network.unattributed", severity:"high",
  confidence:"partial", policyStatus:"not-evaluated", title:"Unattributed connection",
  explanation:"A connection could not be linked exactly.", nextAction:"activity.network",
  evidenceRefs:["act_net0000001"], count:1,
  firstAt:"2026-07-29T10:00:02Z", lastAt:"2026-07-29T10:00:02Z"
});
const operation = window.HideoutConsole.Activity.operationView({
  id:"op_fixture0001",kind:"profile.update",phase:"rolling-back",
  owner:{kind:"profile",id:"default"},
  effects:[{id:"activate",kind:"activate",provider:"network",phase:"failed",evidence:[{code:"probe-failed"}]}],
  recovery:{code:"inspect-route",summary:"Inspect the previous effective route."},
  updatedAt:"2026-07-29T10:00:04Z"
});
const newestInput = [
  {id:"old",lastAt:"2026-07-29T10:00:00Z"},
  {id:"new",lastAt:"2026-07-29T10:00:04Z"},
  {id:"middle",firstAt:"2026-07-29T10:00:02Z"}
];
JSON.stringify({
  query:window.HideoutConsole.Activity.ownerQuery(snapshot,"ses_alpha"),
  file:window.HideoutConsole.Activity.eventView(snapshot.activity[0]),
  network:window.HideoutConsole.Activity.eventView(snapshot.activity[1]),
  dns:window.HideoutConsole.Activity.eventView(snapshot.activity[2]),
  tree:window.HideoutConsole.Activity.flattenExecutions(tree).map((entry) => ({
    depth:entry.depth,
    view:window.HideoutConsole.Activity.executionView(entry.node)
  })),
  newest:window.HideoutConsole.Activity.newestFirst(newestInput).map(
    (entry) => entry.id
  ),
  newestInput:newestInput.map((entry) => entry.id),
  coverage,risk,operation
});
`)
	if err != nil {
		t.Fatalf("run activity view models: %v", err)
	}
	var proof struct {
		Query map[string]any `json:"query"`
		File  struct {
			Detail      string `json:"detail"`
			ExecutionID string `json:"executionId"`
		} `json:"file"`
		Network struct {
			Detail string `json:"detail"`
		} `json:"network"`
		DNS struct {
			Detail string `json:"detail"`
		} `json:"dns"`
		Tree []struct {
			Depth int `json:"depth"`
			View  struct {
				Title    string `json:"title"`
				Outcome  string `json:"outcome"`
				Identity struct {
					UID  int    `json:"uid"`
					User string `json:"user"`
				} `json:"identity"`
			} `json:"view"`
		} `json:"tree"`
		Newest      []string `json:"newest"`
		NewestInput []string `json:"newestInput"`
		Coverage    struct {
			State        string   `json:"state"`
			Dropped      int      `json:"dropped"`
			RetentionGap bool     `json:"retentionGap"`
			Evidence     []string `json:"evidence"`
		} `json:"coverage"`
		Risk struct {
			Severity     string   `json:"severity"`
			Explanation  string   `json:"explanation"`
			EvidenceRefs []string `json:"evidenceRefs"`
		} `json:"risk"`
		Operation struct {
			Phase    string `json:"phase"`
			Recovery struct {
				Code string `json:"code"`
			} `json:"recovery"`
			Effects []struct {
				Phase string `json:"phase"`
			} `json:"effects"`
		} `json:"operation"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Query["environment"] != "env_alpha" ||
		proof.Query["incarnation"] != "inc-alpha" ||
		proof.Query["run"] != "ses_alpha" {
		t.Fatalf("exact reusable owner query=%+v", proof.Query)
	}
	if !strings.Contains(proof.File.Detail, "/workspace/a.txt") ||
		proof.File.ExecutionID != "exec_alpha0001" ||
		!strings.Contains(proof.Network.Detail, "example.com") ||
		!strings.Contains(proof.Network.Detail, "proxy") ||
		!strings.Contains(proof.DNS.Detail, "1.1.1.1") {
		t.Fatalf("closed subject views lost facts: %+v", proof)
	}
	if len(proof.Tree) != 2 ||
		proof.Tree[0].Depth != 0 ||
		proof.Tree[0].View.Title != "claude" ||
		proof.Tree[1].Depth != 1 ||
		proof.Tree[1].View.Outcome != "exit 0" ||
		proof.Tree[0].View.Identity.UID != 1000 ||
		proof.Tree[0].View.Identity.User != "agent" {
		t.Fatalf("execution tree=%+v", proof.Tree)
	}
	if strings.Join(proof.Newest, ",") != "new,middle,old" ||
		strings.Join(proof.NewestInput, ",") != "old,new,middle" {
		t.Fatalf(
			"newest ordering=%v input=%v",
			proof.Newest,
			proof.NewestInput,
		)
	}
	if proof.Coverage.State != "partial" ||
		proof.Coverage.Dropped != 3 ||
		!proof.Coverage.RetentionGap ||
		len(proof.Coverage.Evidence) != 1 ||
		proof.Risk.Severity != "high" ||
		proof.Risk.Explanation == "" ||
		len(proof.Risk.EvidenceRefs) != 1 ||
		proof.Operation.Phase != "rolling-back" ||
		proof.Operation.Recovery.Code != "inspect-route" ||
		len(proof.Operation.Effects) != 1 ||
		proof.Operation.Effects[0].Phase != "failed" {
		t.Fatalf("coverage/risk/operation view facts=%+v", proof)
	}
}

func TestBrowserPanelAssetsUseBoundedManagerReadsAndEventRefresh(t *testing.T) {
	index := mustAsset("index.html")
	for _, marker := range []string{
		`data-panel="overview"`,
		`data-panel="timeline"`,
		`data-panel="executions"`,
		`data-panel="files"`,
		`data-panel="network"`,
		`data-panel="coverage"`,
		`data-panel="risks"`,
		`data-panel="operations"`,
		`id="sessionScope"`,
	} {
		if !strings.Contains(index, marker) {
			t.Fatalf("browser panel shell missing %q", marker)
		}
	}
	client := mustAsset("client.js")
	for _, resource := range []string{
		`"summary"`, `"events"`, `"executions"`, `"coverage"`, `"risks"`,
	} {
		if !strings.Contains(client, resource) {
			t.Fatalf("typed activity client missing %s", resource)
		}
	}
	app := mustAsset("app.js")
	for _, marker := range []string{
		"Promise.all([",
		`root.Activity.summaryQuery(query, eventFilters)`,
		`root.Activity.eventQuery(query, eventFilters)`,
		`root.Client.activity("executions", query)`,
		`root.Activity.coverageQuery(query, eventFilters)`,
		`root.Activity.risksQuery(query, eventFilters)`,
		`["activity", "coverage", "risk"].includes(event.kind)`,
	} {
		if !strings.Contains(app, marker) {
			t.Fatalf("browser panel orchestration missing %q", marker)
		}
	}
	if strings.Contains(client+app, "setInterval") {
		t.Fatal("browser history panels introduced polling")
	}
}

func TestCompoundFiltersCursorInheritanceRetainedGapAndCorrelation(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole: {}};
` + mustAsset("activity.js") + `
const activity = window.HideoutConsole.Activity;
const filters = activity.normalizeFilters({
  kinds:"dns, file, connection",
  operations:"connect,write",
  executions:"exec_alpha0001",
  risks:"risk_alpha0001,file.write-outside-workspace",
  path:" /workspace/src ",
  domain:"Example.COM.",
  ip:"1.1.1.1",
  from:"2026-07-29T10:00:00Z",
  to:"2026-07-29T11:00:00Z"
});
const owner = {environment:"env_alpha",incarnation:"inc-alpha",run:"ses_alpha"};
const eventQuery = activity.eventQuery(owner, filters);
const cursorQuery = activity.cursorQuery(owner, "cursor-owner-filter-revision");
const gap = activity.retainedGapView({
  retainedRange:{from:"2026-07-29T10:15:00Z",to:"2026-07-29T10:45:00Z"},
  pruned:true,corrupt:false,reasons:["quota-pruned"]
}, [{
  retentionGap:true
}], filters);
const event = {
  id:"act_file000001",kind:"file",operation:"write",count:1,
  firstAt:"2026-07-29T10:30:00Z",lastAt:"2026-07-29T10:30:00Z",
  actor:{executionId:"exec_alpha0001",pid:12},
  subject:{kind:"file",path:"/workspace/src/a.go",pathClass:"workspace"},
  outcome:{status:"observed"},attribution:"exact",coverageId:"cov_alpha0001"
};
const correlation = activity.correlate(event, {
  executions:[{
    execution:{id:"exec_alpha0001",executable:"go",argv:["go","test"],pid:12,startedAt:"2026-07-29T10:29:00Z",identity:{user:"agent"}},
    activityCounts:{file:1},children:[]
  }],
  coverage:[{
    id:"cov_alpha0001",subsystem:"file",state:"available",reason:"observer-ready",
    startedAt:"2026-07-29T10:00:00Z"
  }],
  risks:[{
    id:"risk_alpha0001",ruleId:"file.write-outside-workspace",severity:"high",
    confidence:"exact",title:"File changed",explanation:"Observed write.",
    evidenceRefs:["act_file000001"],count:1,
    firstAt:"2026-07-29T10:30:00Z",lastAt:"2026-07-29T10:30:00Z"
  }]
});
JSON.stringify({filters,eventQuery,cursorQuery,cursorKeys:Object.keys(cursorQuery).sort(),gap,correlation});
`)
	if err != nil {
		t.Fatalf("run browser filter/correlation contract: %v", err)
	}
	var proof struct {
		Filters struct {
			Kinds      []string `json:"kinds"`
			Operations []string `json:"operations"`
			Domain     string   `json:"domain"`
			Path       string   `json:"path"`
		} `json:"filters"`
		EventQuery map[string]any `json:"eventQuery"`
		CursorKeys []string       `json:"cursorKeys"`
		Gap        struct {
			Partial bool     `json:"partial"`
			Reasons []string `json:"reasons"`
		} `json:"gap"`
		Correlation struct {
			Execution *struct {
				ID string `json:"id"`
			} `json:"execution"`
			Coverage *struct {
				ID string `json:"id"`
			} `json:"coverage"`
			Risks []struct {
				ID string `json:"id"`
			} `json:"risks"`
		} `json:"correlation"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if strings.Join(proof.Filters.Kinds, ",") != "connection,dns,file" ||
		strings.Join(proof.Filters.Operations, ",") != "connect,write" ||
		proof.Filters.Domain != "example.com" ||
		proof.Filters.Path != "/workspace/src" {
		t.Fatalf("normalized filters=%+v", proof.Filters)
	}
	for _, key := range []string{
		"kind", "operation", "execution", "risk", "path", "domain", "ip",
		"from", "to",
	} {
		if _, ok := proof.EventQuery[key]; !ok {
			t.Fatalf("compound event query lacks %q: %+v", key, proof.EventQuery)
		}
	}
	wantCursorKeys := "cursor,environment,incarnation,limit,run"
	if strings.Join(proof.CursorKeys, ",") != wantCursorKeys {
		t.Fatalf(
			"cursor request changed/invented filters: keys=%v want=%s",
			proof.CursorKeys,
			wantCursorKeys,
		)
	}
	if !proof.Gap.Partial || len(proof.Gap.Reasons) < 3 {
		t.Fatalf("retained gap is not explicit: %+v", proof.Gap)
	}
	if proof.Correlation.Execution == nil ||
		proof.Correlation.Execution.ID != "exec_alpha0001" ||
		proof.Correlation.Coverage == nil ||
		proof.Correlation.Coverage.ID != "cov_alpha0001" ||
		len(proof.Correlation.Risks) != 1 ||
		proof.Correlation.Risks[0].ID != "risk_alpha0001" {
		t.Fatalf("detail correlation=%+v", proof.Correlation)
	}
}
