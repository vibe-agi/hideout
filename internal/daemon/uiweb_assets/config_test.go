package uiweb_assets

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestBrowserConfigurationCanonicalJSONMatchesManagerPlanDigestInput(
	t *testing.T,
) {
	change, err := manager.NewTypedChange(
		manager.ChangeNetworkPosture,
		map[string]any{"mode": "proxy"},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := manager.ConfigurationPlan{
		Schema:           manager.ConfigurationPlanSchema,
		OperationID:      "op_fixture0001",
		Profile:          "default",
		BaseRevision:     4,
		BaseDigest:       "sha256:" + strings.Repeat("a", 64),
		CanonicalChanges: []manager.TypedChange{change},
		Diff: []manager.ReviewDiff{{
			Kind: manager.ChangeNetworkPosture, Field: "network.mode",
			Before: "direct", After: "tun2socks", Scope: "environment",
		}},
		Effects: []manager.PlannedEffect{{
			ID: "persist-profile", Kind: "persist", Scope: "profile",
			Provider: "manager.profile", Live: true,
			Summary: "Persist the reviewed profile configuration.",
			ProofRequired: []string{
				"profile-committed",
			},
		}},
		Blockers: []manager.Blocker{},
		Warnings: []manager.Warning{},
		Rollback: manager.RollbackPlan{
			Mode:    "restore-previous",
			Summary: "Restore the previous profile configuration.",
			Effects: []string{"persist-profile"},
		},
		ExpiresAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	plan.PlanDigest = ""
	want, err := manager.CanonicalJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	runtime := goja.New()
	if err := runtime.Set("planJSON", string(encoded)); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.RunString(`
var window = {HideoutConsole: {}};
` + mustAsset("config.js") + `
window.HideoutConsole.Config.canonicalJSON(JSON.parse(planJSON));
`)
	if err != nil {
		t.Fatalf("run browser canonical JSON: %v", err)
	}
	if value.String() != string(want) {
		t.Fatalf(
			"browser canonical plan input differs from Manager\nbrowser=%s\nmanager=%s",
			value.String(),
			want,
		)
	}
}

func TestBrowserConfigurationDraftLayersConfirmationAndTerminalEvidence(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole: {}};
` + mustAsset("config.js") + `
const config = window.HideoutConsole.Config;
const digest = "sha256:" + "a".repeat(64);
const planDigest = "sha256:" + "b".repeat(64);
const snapshot = {
  profiles: [{
    schema:"hideout.profile-projection.v1",
    profile:"default",revision:4,contentDigest:digest,
    desired:{
      name:"default",
      network:{mode:"direct",proxyEnvVisible:false},
      env:{public:{EDITOR:"vim"},inherit:["TERM"],deny:["TOKEN*"]},
      hostfs:{grants:[],deny:[]},
      commandProxy:{commands:{open:{}}},
      commandAdapters:{adapters:{}}
    },
    effective:{
      status:"effective",
      network:{
        mode:"direct",proxySecretRef:"local-proxy",secretGeneration:4,
        dns:"system",observedAt:"2026-07-29T12:00:00Z"
      },
      sessions:[
        {sessionId:"ses_current",snapshotId:"snap_1",profileRevision:4,current:true},
        {sessionId:"ses_old",snapshotId:"snap_0",profileRevision:3,current:false}
      ]
    },
    transition:{
      operationId:"op_transition01",kind:"network-route",phase:"staging",
      blockers:["session-active"],startedAt:"2026-07-29T12:00:00Z"
    }
  }],
  capabilities:config.DEFINITIONS.map((entry) => ({
    id:entry.capability,state:"available",provider:"manager",mutable:true
  }))
};
let transaction = config.createTransaction(snapshot,"default");
transaction = config.editTransaction(
  transaction,"network.proxyRef",
  config.buildChange("network.proxyRef",{ref:"local-proxy"})
);
transaction = config.editTransaction(
  transaction,"network.dns",
  config.buildChange("network.dns",{mode:"doh",serverIp:"1.1.1.1"})
);
transaction = config.editTransaction(
  transaction,"network.posture",
  config.buildChange("network.posture",{mode:"proxy"})
);
transaction = config.editTransaction(
  transaction,"profile.environment",
  config.buildChange("profile.environment",{
    operation:"set",name:"EDITOR",value:"code"
  })
);
transaction = config.editTransaction(
  transaction,"profile.environment",
  config.buildChange("profile.environment",{
    operation:"inherit",name:"LANG"
  })
);
const planning = config.startReview(transaction);
const plan = {
  schema:"hideout.configuration-plan.v1",
  operationId:"op_fixture0001",
  profile:"default",baseRevision:4,baseDigest:digest,
  canonicalChanges:planning.draft.changes.map((change) => ({
    kind:change.kind,
    value:change.kind === "profile.environment" ?
      {set:{EDITOR:"[value provided]"},inherit:["LANG"]} : change.value
  })),
  diff:[{
    kind:"network.posture",field:"network.mode",
    before:"direct",after:"tun2socks",scope:"environment"
  }],
  effects:[{
    effectId:"persist-profile",kind:"persist",scope:"profile",
    provider:"manager.profile",live:true,
    summary:"Persist reviewed profile.",proofRequired:["profile-committed"]
  }],
  blockers:[],warnings:[],
  rollback:{mode:"restore-previous",summary:"Restore prior profile.",effects:["persist-profile"]},
  planDigest,expiresAt:"2026-07-29T13:00:00Z"
};
const reviewed = Object.assign({},planning,{stage:config.STAGE_REVIEW,plan});
const current = snapshot.profiles[0];
const ready = config.confirmability(
  reviewed,current,true,new Date("2026-07-29T12:30:00Z")
);
const blockedPlan = JSON.parse(JSON.stringify(plan));
blockedPlan.blockers = [{
  code:"session-active",summary:"An old connection is active.",
  recovery:"Wait for the connection to drain."
}];
const blocked = config.confirmability(
  Object.assign({},reviewed,{plan:blockedPlan}),
  current,true,new Date("2026-07-29T12:30:00Z")
);
const expired = config.confirmability(
  reviewed,current,true,new Date("2026-07-29T13:00:00Z")
);
const stale = config.sync(
  reviewed,
  Object.assign({},snapshot,{profiles:[
    Object.assign({},current,{revision:5})
  ]}),
  true,
  "",
  new Date("2026-07-29T12:30:00Z")
);
const terminal = config.terminalView({
  plan,
  responseLost:false,
  error:"",
  operation:{
    schema:"hideout.operation.v1",id:"op_fixture0001",
    kind:"profile.transaction",owner:{kind:"profile",id:"default"},
    planDigest,baseRevision:4,phase:"succeeded",
    effects:[{
      id:"persist-profile",kind:"persist",provider:"manager.profile",
      phase:"succeeded",
      evidence:[{code:"profile-persisted",value:"profile:default"}]
    }],
    result:{status:"succeeded",code:"profile-committed",summary:"Committed."},
    recovery:{code:"profile-committed",summary:"Configuration is committed."}
  }
});
const rows = config.rows(snapshot);
JSON.stringify({
  changes:planning.draft.changes,
  fingerprintBound:planning.reviewedDraftFingerprint ===
    config.draftFingerprint(planning.draft),
  ready,blocked,expired,stale,
  planView:config.planView(plan,new Date("2026-07-29T12:30:00Z")),
  terminal,rows
});
`)
	if err != nil {
		t.Fatalf("run browser configuration state model: %v", err)
	}
	var proof struct {
		Changes []struct {
			Kind  string         `json:"kind"`
			Value map[string]any `json:"value"`
		} `json:"changes"`
		FingerprintBound bool `json:"fingerprintBound"`
		Ready            struct {
			Allowed bool `json:"allowed"`
		} `json:"ready"`
		Blocked struct {
			Allowed bool `json:"allowed"`
		} `json:"blocked"`
		Expired struct {
			Allowed bool `json:"allowed"`
		} `json:"expired"`
		Stale struct {
			Stage           string `json:"stage"`
			AuthorityReason string `json:"authorityReason"`
		} `json:"stale"`
		PlanView struct {
			Changes []struct {
				Kind  string         `json:"kind"`
				Value map[string]any `json:"value"`
			} `json:"changes"`
			Effects []struct {
				Live          bool     `json:"live"`
				ProofRequired []string `json:"proofRequired"`
			} `json:"effects"`
			Rollback struct {
				Mode string `json:"mode"`
			} `json:"rollback"`
		} `json:"planView"`
		Terminal struct {
			Phase    string `json:"phase"`
			Terminal bool   `json:"terminal"`
			Effects  []struct {
				Evidence []struct {
					Code string `json:"code"`
				} `json:"evidence"`
			} `json:"effects"`
		} `json:"terminal"`
		Rows []struct {
			Effective struct {
				CurrentSessions int `json:"currentSessions"`
				OlderSessions   int `json:"olderSessions"`
			} `json:"effective"`
			Transition struct {
				Phase string `json:"phase"`
			} `json:"transition"`
			Fields []struct {
				Kind      string `json:"kind"`
				Effective string `json:"effective"`
				Editable  bool   `json:"editable"`
			} `json:"fields"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if len(proof.Changes) != 4 || !proof.FingerprintBound {
		t.Fatalf("local batch draft=%+v", proof)
	}
	var environment map[string]any
	for _, change := range proof.Changes {
		if change.Kind == manager.ChangeProfileEnvironment {
			environment = change.Value
		}
	}
	if environment == nil ||
		environment["set"] == nil ||
		environment["inherit"] == nil {
		t.Fatalf("environment operations were not merged: %+v", environment)
	}
	if !proof.Ready.Allowed ||
		proof.Blocked.Allowed ||
		proof.Expired.Allowed ||
		proof.Stale.Stage != "stale" ||
		!strings.Contains(proof.Stale.AuthorityReason, "revision changed") {
		t.Fatalf("review authority semantics=%+v", proof)
	}
	var reviewedEnvironment map[string]any
	for _, change := range proof.PlanView.Changes {
		if change.Kind == manager.ChangeProfileEnvironment {
			reviewedEnvironment = change.Value
		}
	}
	reviewJSON, _ := json.Marshal(reviewedEnvironment)
	if strings.Contains(string(reviewJSON), `"code"`) ||
		!strings.Contains(string(reviewJSON), "[value provided]") {
		t.Fatalf("canonical review exposed or lost environment value: %s", reviewJSON)
	}
	if len(proof.PlanView.Effects) != 1 ||
		!proof.PlanView.Effects[0].Live ||
		len(proof.PlanView.Effects[0].ProofRequired) != 1 ||
		proof.PlanView.Rollback.Mode != "restore-previous" ||
		proof.Terminal.Phase != manager.OperationSucceeded ||
		!proof.Terminal.Terminal ||
		len(proof.Terminal.Effects) != 1 ||
		len(proof.Terminal.Effects[0].Evidence) != 1 ||
		proof.Terminal.Effects[0].Evidence[0].Code != "profile-persisted" {
		t.Fatalf("review/terminal evidence=%+v", proof)
	}
	if len(proof.Rows) != 1 ||
		proof.Rows[0].Effective.CurrentSessions != 1 ||
		proof.Rows[0].Effective.OlderSessions != 1 ||
		proof.Rows[0].Transition.Phase != "staging" ||
		len(proof.Rows[0].Fields) != len(manager.DefaultConfigurationCapabilities(false)) {
		t.Fatalf("desired/effective/transition layers=%+v", proof.Rows)
	}
	for _, field := range proof.Rows[0].Fields {
		if !field.Editable {
			t.Fatalf("available Manager capability rendered read-only: %+v", proof.Rows)
		}
		if field.Kind == manager.ChangeNetworkProxyRef &&
			field.Effective != "local-proxy · generation 4" {
			t.Fatalf(
				"browser hid effective secret generation: %+v",
				field,
			)
		}
	}
}

func TestBrowserConfigurationClientUsesClosedMutationAuthority(t *testing.T) {
	client := mustAsset("client.js")
	for _, marker := range []string{
		`"profile/transaction/plan"`,
		`"profile/transaction/apply"`,
		`/api/v1/operations/`,
		`/api/v1/profiles/`,
		`confirmed: true`,
	} {
		if !strings.Contains(client+mustAsset("config.js"), marker) {
			t.Fatalf("browser configuration authority missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"localStorage",
		"sessionStorage",
		"document.cookie",
		"setInterval",
	} {
		if strings.Contains(client+mustAsset("config.js"), forbidden) {
			t.Fatalf("browser configuration introduced forbidden %q", forbidden)
		}
	}
}
