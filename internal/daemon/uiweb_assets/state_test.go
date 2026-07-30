package uiweb_assets

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func runBrowserStateProof(t *testing.T, script string, proof any) {
	t.Helper()
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole: {}};
` + mustAsset("state.js") + script)
	if err != nil {
		t.Fatalf("run browser state reducer: %v", err)
	}
	if err := json.Unmarshal([]byte(value.String()), proof); err != nil {
		t.Fatalf("decode browser state proof: %v\n%s", err, value.String())
	}
}

func TestBrowserEventV2ReducerAppliesCanonicalProjectionDeltas(t *testing.T) {
	var proof struct {
		Results           []string `json:"results"`
		Sequence          int      `json:"sequence"`
		SnapshotSequence  int      `json:"snapshotSequence"`
		Health            string   `json:"health"`
		CanMutate         bool     `json:"canMutate"`
		ProfileRevision   int      `json:"profileRevision"`
		ProfileName       string   `json:"profileName"`
		TransitionPhase   string   `json:"transitionPhase"`
		ActivityCursor    string   `json:"activityCursor"`
		ActivityRetained  string   `json:"activityRetained"`
		ActivityCounts    []string `json:"activityCounts"`
		CoverageIDs       []string `json:"coverageIds"`
		CoverageStates    []string `json:"coverageStates"`
		CapabilityState   string   `json:"capabilityState"`
		CapabilityStatus  string   `json:"capabilityStatus"`
		RiskID            string   `json:"riskId"`
		OperationID       string   `json:"operationId"`
		Diagnostic        string   `json:"diagnostic"`
		SeedCloneRevision int      `json:"seedCloneRevision"`
	}
	runBrowserStateProof(t, `
const State = window.HideoutConsole.State;
const digest = "sha256:" + "a".repeat(64);
const at = "2026-07-29T10:00:00Z";
function coverage(id, state, reason, dropped) {
  return {
    schema:"hideout.coverage-interval.v1",id,
    owner:{
      kind:"disposable-session",sessionId:"ses_fixture0001",
      backend:"lima",backendIncarnationId:"inc_fixture0001"
    },sessionId:"ses_fixture0001",
    subsystem:"process",state,reason,
    collectorGeneration:1,droppedEventCount:dropped || 0,
    retentionGap:false,evidence:[],startedAt:at
  };
}

const source = {
  schema:State.SNAPSHOT_SCHEMA,generatedAt:at,
  instanceId:"daemon_fixture01",credentialGeneration:3,sequence:4,
  streamHealth:{state:"live"},
  profiles:[{
    schema:"hideout.profile-projection.v1",profile:"default",revision:1,
    contentDigest:digest,desired:{name:"default"},
    effective:{status:"effective",sessions:[]},updatedAt:at
  }],
  sessions:[],environments:[],
  activity:[{kind:"file",count:2}],
  activityCursor:"cursor-4",
  coverage:[
    coverage("cov_keep0001","Available","collector-active",0),
    coverage("cov_update01","Available","collector-active",0)
  ],
  risks:[],operations:[],
  capabilities:[{
    id:"network.posture",state:"available",provider:"manager",
    mutable:true,actionRefs:[]
  }],
  nextActions:[]
};
const state = State.seed(source);
source.profiles[0].revision = 99;
const seedCloneRevision = state.snapshot.profiles[0].revision;
const results = [];
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"future-projection",optional:true,seq:5
}).status);
const profileEvent = {
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"profile",seq:6,payload:{
    profileProjection:{
      schema:"hideout.profile-projection.v1",profile:"default",revision:2,
      contentDigest:digest,desired:{name:"default"},
      effective:{status:"effective",sessions:[]},updatedAt:"2026-07-29T10:01:00Z"
    }
  }
};
results.push(State.applyEvent(state,profileEvent).status);
profileEvent.payload.profileProjection.desired.name = "mutated-after-apply";
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"transition",seq:7,payload:{
    transitionProjection:{
      profile:"default",transition:{
        operationId:"op_fixture0001",kind:"network-route",phase:"staging",
        blockers:[],startedAt:"2026-07-29T10:02:00Z"
      }
    }
  }
}).status);
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"activity",seq:8,payload:{
    summary:{
      profile:"default",session:"ses_fixture0001",cursor:"cursor-8",
      counts:[{kind:"file",count:7},{kind:"dns",count:1}],
      appended:2,lastAt:"2026-07-29T10:03:00Z"
    }
  }
}).status);
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"coverage",seq:9,
  entity:{kind:"session",session:"ses_fixture0001"},
  payload:{coverage:[
    coverage("cov_update01","Partial","events-dropped",2),
    coverage("cov_new000001","Unavailable","collector-stopped",0)
  ]}
}).status);
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"capability",seq:10,payload:{
    capabilityProjection:{
      id:"network.posture",status:"Partial",provider:"manager",
      reason:"proof pending",mutable:true,actionRefs:["doctor.activity"]
    }
  }
}).status);
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"risk",seq:11,payload:{
    riskProjection:{
      id:"risk_fixture0001",ruleId:"network.unattributed",ruleVersion:"1",
      severity:"medium",title:"Connection attribution is incomplete.",
      explanation:"One connection lacks exact process attribution.",
      evidenceRefs:[],confidence:"limited",policyStatus:"not-evaluated",
      firstAt:at,lastAt:at,count:1
    }
  }
}).status);
results.push(State.applyEvent(state,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"operation",seq:12,payload:{
    operationProjection:{
      schema:"hideout.operation.v1",id:"op_fixture0002",
      kind:"profile.transaction",owner:{kind:"profile",id:"default"},
      planDigest:digest,baseRevision:2,phase:"staging",effects:[],
      recovery:{code:"inspect-operation",summary:"Inspect the durable operation."},
      createdAt:at,updatedAt:"2026-07-29T10:04:00Z"
    }
  }
}).status);
JSON.stringify({
  results,
  sequence:state.lastSeq,
  snapshotSequence:state.snapshot.sequence,
  health:state.health.state,
  canMutate:State.canMutate(state),
  profileRevision:state.snapshot.profiles[0].revision,
  profileName:state.snapshot.profiles[0].desired.name,
  transitionPhase:state.snapshot.profiles[0].transition.phase,
  activityCursor:state.snapshot.activitySummary.cursor,
  activityRetained:state.snapshot.activitySummary.retainedTo,
  activityCounts:state.snapshot.activitySummary.counts.map(
    (row) => row.kind + ":" + row.count
  ),
  coverageIds:state.snapshot.coverage.map((row) => row.id),
  coverageStates:state.snapshot.coverage.map((row) => row.state),
  capabilityState:state.snapshot.capabilities[0].state,
  capabilityStatus:state.snapshot.capabilities[0].status,
  riskId:state.snapshot.risks[0].id,
  operationId:state.snapshot.operations[0].id,
  diagnostic:state.diagnostics[0],
  seedCloneRevision
});
`, &proof)

	wantResults := []string{
		"ignored", "applied", "applied", "applied",
		"applied", "applied", "applied", "applied",
	}
	if !equalStrings(proof.Results, wantResults) {
		t.Fatalf("apply results=%v want=%v", proof.Results, wantResults)
	}
	if proof.Sequence != 12 || proof.SnapshotSequence != 12 ||
		proof.Health != "live" || !proof.CanMutate {
		t.Fatalf("unexpected stream state: %+v", proof)
	}
	if proof.ProfileRevision != 2 || proof.ProfileName != "default" ||
		proof.TransitionPhase != "staging" || proof.SeedCloneRevision != 1 {
		t.Fatalf("profile projection was not cloned/upserted: %+v", proof)
	}
	if proof.ActivityCursor != "cursor-8" ||
		proof.ActivityRetained != "2026-07-29T10:03:00Z" ||
		!equalStrings(proof.ActivityCounts, []string{"file:7", "dns:1"}) {
		t.Fatalf("activity delta mismatch: %+v", proof)
	}
	if !equalStrings(
		proof.CoverageIDs,
		[]string{"cov_keep0001", "cov_update01", "cov_new000001"},
	) || !equalStrings(
		proof.CoverageStates,
		[]string{"Available", "Partial", "Unavailable"},
	) {
		t.Fatalf("coverage did not upsert by interval ID: %+v", proof)
	}
	if proof.CapabilityState != "partial" ||
		proof.CapabilityStatus != "Partial" ||
		proof.RiskID != "risk_fixture0001" ||
		proof.OperationID != "op_fixture0002" ||
		proof.Diagnostic != "ignored optional event kind future-projection" {
		t.Fatalf("projection result mismatch: %+v", proof)
	}
}

func TestBrowserEventV2ReducerConsumesGoWireTags(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	events := []liveconsole.Event{
		{
			Version:              liveconsole.EventVersionV2,
			InstanceID:           "daemon_fixture01",
			CredentialGeneration: 3,
			Kind:                 liveconsole.KindActivity,
			Seq:                  1,
			Entity: liveconsole.EntityRef{
				Kind:    "session",
				Session: "ses_fixture0001",
			},
			Payload: liveconsole.EventPayload{
				ActivityProjection: &liveconsole.ActivityProjectionDelta{
					Session: "ses_fixture0001",
					Cursor:  "cursor-from-go",
					Counts: []liveconsole.ActivityCount{{
						Kind: "process", Count: 4,
					}},
					LastAt: now,
				},
			},
		},
		{
			Version:              liveconsole.EventVersionV2,
			InstanceID:           "daemon_fixture01",
			CredentialGeneration: 3,
			Kind:                 liveconsole.KindCoverage,
			Seq:                  2,
			Entity: liveconsole.EntityRef{
				Kind:    "session",
				Session: "ses_fixture0001",
			},
			Payload: liveconsole.EventPayload{
				CoverageProjection: []workloadtypes.CoverageInterval{{
					Schema: workloadtypes.CoverageIntervalSchema,
					ID:     "cov_fixture0001",
					Owner: workloadtypes.ActivityOwner{
						Kind:                 workloadtypes.OwnerDisposableSession,
						SessionID:            "ses_fixture0001",
						Backend:              "lima",
						BackendIncarnationID: "inc_fixture0001",
					},
					SessionID:           "ses_fixture0001",
					Subsystem:           workloadtypes.SubsystemProcess,
					State:               workloadtypes.CoverageAvailable,
					Reason:              "collector-active",
					CollectorGeneration: 1,
					StartedAt:           now,
				}},
			},
		},
	}
	for _, event := range events {
		if err := liveconsole.ValidateEvent(event); err != nil {
			t.Fatalf("invalid Go event fixture: %v", err)
		}
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	runtime := goja.New()
	if err := runtime.Set("eventJSON", string(encoded)); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.RunString(`
var window = {HideoutConsole: {}};
` + mustAsset("state.js") + `
const State = window.HideoutConsole.State;
const events = JSON.parse(eventJSON);
const state = State.seed({
  schema:State.SNAPSHOT_SCHEMA,
  generatedAt:"2026-07-29T10:00:00Z",
  instanceId:"daemon_fixture01",credentialGeneration:3,sequence:0,
  streamHealth:{state:"live"},profiles:[],sessions:[],environments:[],
  activity:[],coverage:[],risks:[],operations:[],capabilities:[],nextActions:[]
});
const results = events.map((event) => State.applyEvent(state,event).status);
JSON.stringify({
  results,
  cursor:state.snapshot.activitySummary.cursor,
  count:state.snapshot.activitySummary.counts[0].count,
  coverageState:state.snapshot.coverage[0].state,
  sequence:state.lastSeq
});
`)
	if err != nil {
		t.Fatalf("run Go wire events in browser reducer: %v", err)
	}
	var proof struct {
		Results       []string `json:"results"`
		Cursor        string   `json:"cursor"`
		Count         int      `json:"count"`
		CoverageState string   `json:"coverageState"`
		Sequence      int      `json:"sequence"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if !equalStrings(proof.Results, []string{"applied", "applied"}) ||
		proof.Cursor != "cursor-from-go" ||
		proof.Count != 4 ||
		proof.CoverageState != workloadtypes.CoverageAvailable ||
		proof.Sequence != 2 {
		t.Fatalf("Go wire projection mismatch: %+v", proof)
	}
}

func TestBrowserEventV2ReducerFailsClosedUntilAuthoritativeReseed(t *testing.T) {
	var proof struct {
		OptionalStatus       string `json:"optionalStatus"`
		OptionalSequence     int    `json:"optionalSequence"`
		OldStatus            string `json:"oldStatus"`
		GapHealth            string `json:"gapHealth"`
		GapSequence          int    `json:"gapSequence"`
		LateStatus           string `json:"lateStatus"`
		RequiredHealth       string `json:"requiredHealth"`
		InstanceHealth       string `json:"instanceHealth"`
		CredentialHealth     string `json:"credentialHealth"`
		GenericTerminal      string `json:"genericTerminal"`
		CredentialTerminal   string `json:"credentialTerminal"`
		OldStillStale        bool   `json:"oldStillStale"`
		ReseedCanMutate      bool   `json:"reseedCanMutate"`
		ReseedSequence       int    `json:"reseedSequence"`
		ReseedRevision       int    `json:"reseedRevision"`
		ReseedEventViews     int    `json:"reseedEventViews"`
		ProjectionLimit      int    `json:"projectionLimit"`
		ProjectionFirst      string `json:"projectionFirst"`
		ProjectionLast       string `json:"projectionLast"`
		OutsideScopeStatus   string `json:"outsideScopeStatus"`
		OutsideScopeSequence int    `json:"outsideScopeSequence"`
		RefreshHealth        string `json:"refreshHealth"`
		RefreshCanMutate     bool   `json:"refreshCanMutate"`
		ExpiredHealth        string `json:"expiredHealth"`
		ExpiredCanMutate     bool   `json:"expiredCanMutate"`
	}
	runBrowserStateProof(t, `
const State = window.HideoutConsole.State;
const digest = "sha256:" + "a".repeat(64);
const at = "2026-07-29T10:00:00Z";
function snapshot(sequence) {
  return {
    schema:State.SNAPSHOT_SCHEMA,generatedAt:at,
    instanceId:"daemon_fixture01",credentialGeneration:3,sequence,
    streamHealth:{state:"live"},
    profiles:[{
      schema:"hideout.profile-projection.v1",profile:"default",revision:1,
      contentDigest:digest,desired:{name:"default"},
      effective:{status:"effective",sessions:[]},updatedAt:at
    }],
    sessions:[],environments:[],activity:[],coverage:[],risks:[],
    operations:[],capabilities:[],nextActions:[]
  };
}
function optionalEvent(seq, overrides) {
  return Object.assign({
    version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
    credentialGeneration:3,kind:"future-projection",optional:true,seq
  },overrides || {});
}
const optionalState = State.seed(snapshot(4));
const optionalStatus = State.applyEvent(optionalState,optionalEvent(5)).status;
const optionalSequence = optionalState.lastSeq;
const oldStatus = State.applyEvent(optionalState,optionalEvent(5)).status;

const gapState = State.seed(snapshot(4));
State.applyEvent(gapState,optionalEvent(6));
const gapHealth = gapState.health.state;
const gapSequence = gapState.lastSeq;
const lateStatus = State.applyEvent(gapState,optionalEvent(5)).status;

const requiredState = State.seed(snapshot(4));
State.applyEvent(requiredState,Object.assign(optionalEvent(5),{
  kind:"future-required",optional:false
}));

const instanceState = State.seed(snapshot(4));
State.applyEvent(instanceState,optionalEvent(5,{
  instanceId:"daemon_restarted01"
}));
const credentialState = State.seed(snapshot(4));
State.applyEvent(credentialState,optionalEvent(5,{
  credentialGeneration:4
}));

function terminal(reason) {
  return {
    version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
    credentialGeneration:3,kind:"terminal",seq:0,
    entity:{kind:"stream"},payload:{reason}
  };
}
const genericTerminalState = State.seed(snapshot(4));
State.applyEvent(
  genericTerminalState,
  terminal("credential transport closed")
);
const credentialTerminalState = State.seed(snapshot(4));
State.applyEvent(
  credentialTerminalState,
  terminal("credential-expired")
);

gapState.snapshot.background.push({id:"old-event-only"});
const fresh = snapshot(6);
fresh.profiles[0].revision = 7;
const reseeded = State.reseed(gapState,fresh);
fresh.profiles[0].revision = 99;

const limited = State.seed(snapshot(4));
limited.snapshot.risks = Array.from({length:256},(_,index) => ({
  id:"seed-" + String(index).padStart(3,"0")
}));
State.applyEvent(limited,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"risk",seq:5,payload:{
    riskProjection:{
      id:"risk_fixture0002",ruleId:"file.write",ruleVersion:"1",
      severity:"low",title:"Write observed.",
      explanation:"A file write was observed.",evidenceRefs:[],
      confidence:"exact",policyStatus:"allowed",
      firstAt:at,lastAt:at,count:1
    }
  }
});

const scoped = State.seed(snapshot(4),"default");
const outsideScopeStatus = State.applyEvent(scoped,{
  version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
  credentialGeneration:3,kind:"profile",seq:5,
  entity:{kind:"profile",profile:"other"},
  payload:{profileProjection:{
    schema:"hideout.profile-projection.v1",profile:"other",revision:1,
    contentDigest:digest,desired:{name:"other"},
    effective:{status:"effective",sessions:[]},updatedAt:at
  }}
}).status;

const refreshing = State.seed(snapshot(4));
State.beginReseed(refreshing,"manual refresh");
const expired = State.seed(snapshot(4));
State.expireCredential(expired,"credential rejected");

JSON.stringify({
  optionalStatus,optionalSequence,oldStatus,
  gapHealth,gapSequence,lateStatus,
  requiredHealth:requiredState.health.state,
  instanceHealth:instanceState.health.state,
  credentialHealth:credentialState.health.state,
  genericTerminal:genericTerminalState.health.state,
  credentialTerminal:credentialTerminalState.health.state,
  oldStillStale:gapState.requiresReseed,
  reseedCanMutate:State.canMutate(reseeded),
  reseedSequence:reseeded.lastSeq,
  reseedRevision:reseeded.snapshot.profiles[0].revision,
  reseedEventViews:reseeded.snapshot.background.length,
  projectionLimit:limited.snapshot.risks.length,
  projectionFirst:limited.snapshot.risks[0].id,
  projectionLast:limited.snapshot.risks[255].id,
  outsideScopeStatus,
  outsideScopeSequence:scoped.lastSeq,
  refreshHealth:refreshing.health.state,
  refreshCanMutate:State.canMutate(refreshing),
  expiredHealth:expired.health.state,
  expiredCanMutate:State.canMutate(expired)
});
`, &proof)

	if proof.OptionalStatus != "ignored" || proof.OptionalSequence != 5 ||
		proof.OldStatus != "ignored" {
		t.Fatalf("optional/old event semantics mismatch: %+v", proof)
	}
	if proof.GapHealth != "stale" || proof.GapSequence != 4 ||
		proof.LateStatus != "stale" {
		t.Fatalf("sequence gap was not sticky: %+v", proof)
	}
	if proof.RequiredHealth != "schema-mismatch" ||
		proof.InstanceHealth != "stale" ||
		proof.CredentialHealth != "credential-expired" {
		t.Fatalf("identity/schema fail-closed state mismatch: %+v", proof)
	}
	if proof.GenericTerminal != "disconnected" ||
		proof.CredentialTerminal != "credential-expired" {
		t.Fatalf("terminal reason classification mismatch: %+v", proof)
	}
	if !proof.OldStillStale || !proof.ReseedCanMutate ||
		proof.ReseedSequence != 6 || proof.ReseedRevision != 7 ||
		proof.ReseedEventViews != 0 {
		t.Fatalf("authoritative re-seed did not replace state: %+v", proof)
	}
	if proof.ProjectionLimit != 256 ||
		proof.ProjectionFirst != "seed-001" ||
		proof.ProjectionLast != "risk_fixture0002" {
		t.Fatalf("bounded append projection mismatch: %+v", proof)
	}
	if proof.OutsideScopeStatus != "ignored" ||
		proof.OutsideScopeSequence != 5 {
		t.Fatalf("profile scope did not advance sequence safely: %+v", proof)
	}
	if proof.RefreshHealth != "seeding" || proof.RefreshCanMutate ||
		proof.ExpiredHealth != "credential-expired" ||
		proof.ExpiredCanMutate {
		t.Fatalf("refresh/credential loss did not disable mutation: %+v", proof)
	}
}

func TestBrowserEventV2ReducerMaintainsLegacyLivePanelProjections(t *testing.T) {
	var proof struct {
		Statuses            []string `json:"statuses"`
		EnvironmentStatus   string   `json:"environmentStatus"`
		SessionState        string   `json:"sessionState"`
		WorkspaceState      string   `json:"workspaceState"`
		BackgroundStatus    string   `json:"backgroundStatus"`
		AuditCount          int      `json:"auditCount"`
		DeniedAuditCount    int      `json:"deniedAuditCount"`
		ExportStatus        string   `json:"exportStatus"`
		CleanupStatus       string   `json:"cleanupStatus"`
		HostFSStatus        string   `json:"hostfsStatus"`
		DecisionStatus      string   `json:"decisionStatus"`
		NoticeStatus        string   `json:"noticeStatus"`
		LifecycleGeneration int      `json:"lifecycleGeneration"`
	}
	runBrowserStateProof(t, `
const State = window.HideoutConsole.State;
const at = "2026-07-29T10:00:00Z";
const state = State.seed({
  schema:State.SNAPSHOT_SCHEMA,generatedAt:at,
  instanceId:"daemon_fixture01",credentialGeneration:3,sequence:0,
  streamHealth:{state:"live"},profiles:[],sessions:[],environments:[],
  activity:[],coverage:[],risks:[],operations:[],capabilities:[],nextActions:[]
});
let seq = 0;
const statuses = [];
function apply(kind,payload,entity) {
  seq++;
  statuses.push(State.applyEvent(state,{
    version:State.EVENT_VERSION,instanceId:"daemon_fixture01",
    credentialGeneration:3,kind,seq,entity:entity || {kind},payload
  }).status);
}
apply("environment",{
  id:"env_fixture0001",name:"fixture",profile:"default",
  backend:"lima",status:"running"
});
apply("session",{
  id:"ses_fixture0001",profile:"default",status:"running"
});
apply("workspace-view",{
  id:"ses_fixture0001",attachmentId:"att_fixture0001",
  session:"ses_fixture0001",environmentId:"env_fixture0001",
  profile:"default",workspaceId:"wrk_fixture0001",
  workspaceLabel:"project",guestWorkspace:"/workspace",
  workspaceTransport:"workspace-portal",workspaceViewState:"ready"
});
apply("background",{id:"bg_fixture0001",op:"clean",status:"completed"});
apply("audit",{
  time:at,session:"ses_fixture0001",profile:"default",backend:"lima",
  action:"network.connect",decision:"deny",details:{reason:"policy"}
});
apply("export",{status:"completed",source:"activity",artifactPath:"/tmp/a.json"});
apply("cleanup",{status:"completed",sessions:1,removed:["ses_fixture0001"]});
apply("hostfs-write",{
  decisionId:"hfwdec_fixture01",operationId:"hfwop_fixture01",
  status:"pending",operation:"replace",path:"/workspace/a"
});
apply("decision",{
  decisionId:"dec_fixture0001",recordKind:"evidence.share",
  status:"pending",defaultOutcome:"deny"
});
apply("notice",{
  noticeId:"notice_fixture01",recordKind:"privilege.status",
  status:"degraded",severity:"warning"
});
apply("lifecycle",{lifecycle:{
  schema:"hideout.lifecycle-status/v1",environmentId:"env_fixture0001",
  startGeneration:2,backendState:"running"
}});
JSON.stringify({
  statuses,
  environmentStatus:state.snapshot.environments[0].status,
  sessionState:state.snapshot.sessions[0].state,
  workspaceState:state.snapshot.sessions[0].workspaceViewState,
  backgroundStatus:state.snapshot.background[0].status,
  auditCount:state.snapshot.auditTail.length,
  deniedAuditCount:state.snapshot.deniedAuditTail.length,
  exportStatus:state.snapshot.exportOutcomes[0].status,
  cleanupStatus:state.snapshot.cleanupOutcomes[0].status,
  hostfsStatus:state.snapshot.hostfsWrites[0].status,
  decisionStatus:state.snapshot.decisions[0].status,
  noticeStatus:state.snapshot.notices[0].status,
  lifecycleGeneration:state.snapshot.lifecycle[0].startGeneration
});
`, &proof)

	for index, status := range proof.Statuses {
		if status != "applied" {
			t.Fatalf("legacy event %d status=%q, proof=%+v", index, status, proof)
		}
	}
	if len(proof.Statuses) != 11 ||
		proof.EnvironmentStatus != "running" ||
		proof.SessionState != "running" ||
		proof.WorkspaceState != "ready" ||
		proof.BackgroundStatus != "completed" ||
		proof.AuditCount != 1 ||
		proof.DeniedAuditCount != 1 ||
		proof.ExportStatus != "completed" ||
		proof.CleanupStatus != "completed" ||
		proof.HostFSStatus != "pending" ||
		proof.DecisionStatus != "pending" ||
		proof.NoticeStatus != "degraded" ||
		proof.LifecycleGeneration != 2 {
		t.Fatalf("legacy live projection mismatch: %+v", proof)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
