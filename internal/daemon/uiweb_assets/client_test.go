package uiweb_assets

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestBrowserCredentialGrammarMatchesManagerIssuer(t *testing.T) {
	token, err := manager.NewUIToken()
	if err != nil {
		t.Fatal(err)
	}
	grammar := regexp.MustCompile(`^ui_[0-9a-f]{48}$`)
	if !grammar.MatchString(token) {
		t.Fatalf("Manager issued token outside browser grammar: length=%d", len(token))
	}
	if !strings.Contains(mustAsset("client.js"), grammar.String()) {
		t.Fatalf("browser client does not use Manager token grammar %q", grammar)
	}
}

func TestBrowserClientRefreshesCredentialOnlyFromClearedFragment(t *testing.T) {
	runtime := goja.New()
	value, err := runtime.RunString(`
const tokenA = "ui_" + "a".repeat(48);
const tokenB = "ui_" + "b".repeat(48);
const listeners = {};
let cleared = 0;
var document = {title:"Hideout"};
var window = {
  HideoutConsole:{},
  location:{hash:"#token=" + tokenA,pathname:"/",search:"?view=console"},
  history:{replaceState:function(_state,_title,path) {
    cleared++;
    window.location.hash = "";
    window.replacedPath = path;
  }},
  addEventListener:function(kind,handler) { listeners[kind] = handler; }
};
class URLSearchParams {
  constructor(raw) {
    this.values = {};
    String(raw || "").split("&").forEach((entry) => {
      const parts = entry.split("=");
      if (parts[0]) this.values[decodeURIComponent(parts[0])] =
        decodeURIComponent(parts.slice(1).join("="));
    });
  }
  get(name) { return this.values[name] || null; }
}
` + mustAsset("client.js") + `
const Client = window.HideoutConsole.Client;
const initial = Client.credentialState();
const refreshes = [];
Client.onCredentialRefresh((state) => refreshes.push(state));
window.location.hash = "#token=" + tokenB;
listeners.hashchange();
const refreshed = Client.credentialState();
window.location.hash = "#token=" + tokenB;
listeners.hashchange();
const duplicate = Client.credentialState();
window.location.hash = "#token=not-a-hideout-token";
listeners.hashchange();
const invalid = Client.credentialState();
JSON.stringify({
  initial,refreshed,duplicate,invalid,refreshes,cleared,
  replacedPath:window.replacedPath,
  fragment:window.location.hash,
  exports:Object.keys(Client).sort()
});
`)
	if err != nil {
		t.Fatalf("run browser credential refresh: %v", err)
	}
	var proof struct {
		Initial struct {
			Available bool `json:"available"`
			Epoch     int  `json:"epoch"`
		} `json:"initial"`
		Refreshed struct {
			Available bool `json:"available"`
			Epoch     int  `json:"epoch"`
		} `json:"refreshed"`
		Duplicate struct {
			Epoch int `json:"epoch"`
		} `json:"duplicate"`
		Invalid struct {
			Epoch int `json:"epoch"`
		} `json:"invalid"`
		Refreshes []struct {
			Epoch int `json:"epoch"`
		} `json:"refreshes"`
		Cleared      int      `json:"cleared"`
		ReplacedPath string   `json:"replacedPath"`
		Fragment     string   `json:"fragment"`
		Exports      []string `json:"exports"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if !proof.Initial.Available || proof.Initial.Epoch != 1 ||
		!proof.Refreshed.Available || proof.Refreshed.Epoch != 2 ||
		proof.Duplicate.Epoch != 2 || proof.Invalid.Epoch != 2 {
		t.Fatalf("credential epochs are not monotonic and bounded: %+v", proof)
	}
	if len(proof.Refreshes) != 1 || proof.Refreshes[0].Epoch != 2 ||
		proof.Cleared != 4 ||
		proof.ReplacedPath != "/?view=console" ||
		proof.Fragment != "" {
		t.Fatalf("fragment credential was not safely consumed: %+v", proof)
	}
	for _, name := range proof.Exports {
		if name == "token" || name == "getToken" {
			t.Fatalf("raw browser credential was exported: %+v", proof.Exports)
		}
	}
}

func TestBrowserClientHTTP401InvalidatesAuthorityWithoutLeakingToken(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
const secretToken = "ui_" + "c".repeat(48);
const listeners = {};
let requestProof = {};
var document = {title:"Hideout"};
var window = {
  HideoutConsole:{},
  location:{hash:"#token=" + secretToken,pathname:"/",search:""},
  history:{replaceState:function() { window.location.hash = ""; }},
  addEventListener:function(kind,handler) { listeners[kind] = handler; }
};
class URLSearchParams {
  constructor(raw) {
    this.value = String(raw || "").startsWith("token=") ?
      decodeURIComponent(String(raw).slice(6)) : "";
  }
  get(name) { return name === "token" ? this.value : null; }
}
class Headers {
  constructor() { this.values = {}; }
  set(name,value) { this.values[name.toLowerCase()] = value; }
  get(name) { return this.values[name.toLowerCase()] || ""; }
}
class AbortController {
  constructor() {
    this.signal = {};
    this.aborted = false;
  }
  abort() { this.aborted = true; }
}
var fetch = function(path,init) {
  requestProof = {
    path,
    headerMatched:init.headers.get("X-Hideout-UI-Token") === secretToken,
    credentials:init.credentials,
    cache:init.cache,
    signal:Boolean(init.signal)
  };
  return Promise.resolve({
    ok:false,status:401,statusText:"Unauthorized",
    text:function() {
      return Promise.resolve('{"errors":["unauthorized"]}');
    }
  });
};
var EventSource = function() {};
` + mustAsset("client.js") + `
const Client = window.HideoutConsole.Client;
const authority = [];
Client.onAuthorityLost((state) => authority.push(state));
Client.snapshot().then(
  () => JSON.stringify({unexpected:true}),
  (error) => JSON.stringify({
    code:error.code,
    credentialExpired:error.credentialExpired,
    message:error.message,
    authority,
    requestProof,
    rawTokenExposed:error.message.includes(secretToken)
  })
);
`)
	if err != nil {
		t.Fatalf("run browser 401 invalidation: %v", err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		t.Fatalf("browser request did not return a promise: %T", value.Export())
	}
	if promise.State() != goja.PromiseStateFulfilled {
		t.Fatalf("browser request promise state=%v", promise.State())
	}
	var proof struct {
		Code              string `json:"code"`
		CredentialExpired bool   `json:"credentialExpired"`
		Message           string `json:"message"`
		Authority         []struct {
			State string `json:"state"`
			Epoch int    `json:"epoch"`
		} `json:"authority"`
		RequestProof struct {
			Path          string `json:"path"`
			HeaderMatched bool   `json:"headerMatched"`
			Credentials   string `json:"credentials"`
			Cache         string `json:"cache"`
			Signal        bool   `json:"signal"`
		} `json:"requestProof"`
		RawTokenExposed bool `json:"rawTokenExposed"`
	}
	if err := json.Unmarshal(
		[]byte(promise.Result().String()),
		&proof,
	); err != nil {
		t.Fatal(err)
	}
	if proof.Code != "credential-expired" ||
		!proof.CredentialExpired ||
		len(proof.Authority) != 1 ||
		proof.Authority[0].State != "credential-expired" ||
		proof.Authority[0].Epoch != 1 {
		t.Fatalf("401 did not invalidate browser authority: %+v", proof)
	}
	if proof.RequestProof.Path != "/api/v1/operator/snapshot?activityLimit=100" ||
		!proof.RequestProof.HeaderMatched ||
		proof.RequestProof.Credentials != "omit" ||
		proof.RequestProof.Cache != "no-store" ||
		!proof.RequestProof.Signal ||
		proof.RawTokenExposed {
		t.Fatalf("browser request boundary mismatch: %+v", proof)
	}
}

func TestBrowserClientRejectsResponseFromSupersededCredential(t *testing.T) {
	runtime := goja.New()
	value, err := runtime.RunString(`
const tokenA = "ui_" + "d".repeat(48);
const tokenB = "ui_" + "e".repeat(48);
const listeners = {};
let resolveFetch;
let aborted = false;
var document = {title:"Hideout"};
var window = {
  HideoutConsole:{},
  location:{hash:"#token=" + tokenA,pathname:"/",search:""},
  history:{replaceState:function() { window.location.hash = ""; }},
  addEventListener:function(kind,handler) { listeners[kind] = handler; }
};
class URLSearchParams {
  constructor(raw) {
    this.value = String(raw || "").startsWith("token=") ?
      decodeURIComponent(String(raw).slice(6)) : "";
  }
  get(name) { return name === "token" ? this.value : null; }
}
class Headers {
  constructor() { this.values = {}; }
  set(name,value) { this.values[name] = value; }
}
class AbortController {
  constructor() { this.signal = {}; }
  abort() { aborted = true; }
}
var fetch = function() {
  return new Promise((resolve) => { resolveFetch = resolve; });
};
var EventSource = function() {};
` + mustAsset("client.js") + `
const Client = window.HideoutConsole.Client;
const oldRequest = Client.snapshot();
window.location.hash = "#token=" + tokenB;
listeners.hashchange();
resolveFetch({
  ok:true,status:200,statusText:"OK",
  text:function() {
    return Promise.resolve(JSON.stringify({
      version:"hideout.manager-api/v1",
      resource:"operator/snapshot",
      data:{unexpected:"old credential response"}
    }));
  }
});
oldRequest.then(
  () => JSON.stringify({unexpected:true}),
  (error) => JSON.stringify({
    code:error.code,
    aborted,
    epoch:Client.credentialState().epoch,
    available:Client.credentialState().available
  })
);
`)
	if err != nil {
		t.Fatalf("run superseded browser request: %v", err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled {
		t.Fatalf("superseded request promise=%T state=%v", value.Export(), promise)
	}
	var proof struct {
		Code       string `json:"code"`
		Aborted    bool   `json:"aborted"`
		Epoch      int    `json:"epoch"`
		Available  bool   `json:"available"`
		Unexpected bool   `json:"unexpected"`
	}
	if err := json.Unmarshal([]byte(promise.Result().String()), &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Unexpected ||
		proof.Code != "credential-refreshed" ||
		!proof.Aborted ||
		proof.Epoch != 2 ||
		!proof.Available {
		t.Fatalf("superseded response retained authority: %+v", proof)
	}
}

func TestBrowserMigrationClientUsesExactManagerRoutesAndKeepsSecretsOutOfURLs(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
const operatorToken = "ui_" + "f".repeat(48);
const passphrase = "socks5://user:secret@proxy.example";
const operationID = "op_migrationwebclient1";
const requests = [];
var document = {title:"Hideout"};
var window = {
  HideoutConsole:{},
  location:{hash:"#token=" + operatorToken,pathname:"/",search:""},
  history:{replaceState:function() { window.location.hash = ""; }},
  addEventListener:function() {}
};
class URLSearchParams {
  constructor(raw) {
    this.value = String(raw || "").startsWith("token=") ?
      decodeURIComponent(String(raw).slice(6)) : "";
  }
  get(name) { return name === "token" ? this.value : null; }
}
class Headers {
  constructor(initial) {
    this.values = {};
    for (const [name,value] of Object.entries(initial || {})) {
      this.set(name,value);
    }
  }
  set(name,value) { this.values[String(name).toLowerCase()] = String(value); }
  get(name) { return this.values[String(name).toLowerCase()] || ""; }
}
class AbortController {
  constructor() { this.signal = {}; }
  abort() {}
}
var EventSource = function() {};
var fetch = function(path,init) {
  const method = init && init.method || "GET";
  const body = init && init.body || "";
  requests.push({
    path,method,body,
    tokenMatched:init.headers.get("X-Hideout-UI-Token") === operatorToken,
    contentType:init.headers.get("Content-Type"),
    credentials:init.credentials,
    cache:init.cache
  });
  let resource = String(path).slice("/api/v1/".length);
  if (resource.startsWith("migration/operations/")) {
    resource = "migration/operation";
  }
  return Promise.resolve({
    ok:true,status:200,statusText:"OK",
    text:function() {
      return Promise.resolve(JSON.stringify({
        version:"hideout.manager-api/v1",resource,data:{accepted:true},errors:[]
      }));
    }
  });
};
` + mustAsset("client.js") + `
const Client = window.HideoutConsole.Client;
Client.migrationSecretInput({
  purpose:"inspect",bundlePath:"/tmp/source.bundle",passphrase
}).then(() => Client.migrationExportPlan({schema:"hideout.migration-export-request/v1"}))
  .then(() => Client.migrationExportApply({plan:{planDigest:"sha256:abc"}}))
  .then(() => Client.migrationImportInspect({
    bundlePath:"/tmp/source.bundle",secretInputHandle:"migh_handle0001"
  }))
  .then(() => Client.migrationImportPlan({
    importDraft:{schema:"hideout.migration-import-draft/v1"},
    secretInputHandle:"migh_handle0002"
  }))
  .then(() => Client.migrationImportApply({
    plan:{planDigest:"sha256:def"},secretInputHandle:"migh_handle0003"
  }))
  .then(() => Client.migrationOperation(operationID))
  .then(() => Client.migrationAction(operationID,"recover",{
    revision:7,recoveryAction:"rollback"
  }))
  .then(() => JSON.stringify({requests,passphrase}));
`)
	if err != nil {
		t.Fatalf("run browser migration client: %v", err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled {
		t.Fatalf("migration request promise=%T state=%v", value.Export(), promise)
	}
	var proof struct {
		Passphrase string `json:"passphrase"`
		Requests   []struct {
			Path         string `json:"path"`
			Method       string `json:"method"`
			Body         string `json:"body"`
			TokenMatched bool   `json:"tokenMatched"`
			ContentType  string `json:"contentType"`
			Credentials  string `json:"credentials"`
			Cache        string `json:"cache"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(promise.Result().String()), &proof); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		"/api/v1/migration/secret-input",
		"/api/v1/migration/export/plan",
		"/api/v1/migration/export/apply",
		"/api/v1/migration/import/inspect",
		"/api/v1/migration/import/plan",
		"/api/v1/migration/import/apply",
		"/api/v1/migration/operations/op_migrationwebclient1",
		"/api/v1/migration/operations/op_migrationwebclient1/recover",
	}
	if len(proof.Requests) != len(wantPaths) {
		t.Fatalf("migration requests=%+v", proof.Requests)
	}
	for i, request := range proof.Requests {
		if request.Path != wantPaths[i] ||
			strings.Contains(request.Path, proof.Passphrase) ||
			!request.TokenMatched || request.Credentials != "omit" ||
			request.Cache != "no-store" {
			t.Fatalf("migration request[%d]=%+v", i, request)
		}
		if i == 6 {
			if request.Method != http.MethodGet || request.Body != "" {
				t.Fatalf("migration operation request=%+v", request)
			}
			continue
		}
		if request.Method != http.MethodPost ||
			request.ContentType != "application/json" {
			t.Fatalf("migration mutation request[%d]=%+v", i, request)
		}
	}
	if !strings.Contains(proof.Requests[0].Body, proof.Passphrase) {
		t.Fatalf("protected input was not carried in the one-shot request body")
	}
	for _, request := range proof.Requests[1:] {
		if strings.Contains(request.Body, proof.Passphrase) {
			t.Fatalf("protected input escaped one-shot route: %+v", request)
		}
	}
}

func TestBrowserDecisionAndNoticeClientsUseAuthenticatedMemberRoutes(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
const operatorToken = "ui_" + "a".repeat(48);
const decisionID = "decision-web-client";
const noticeID = "notice-web-client";
const claimToken = "claim_secret_web_client";
const requests = [];
let claimCalls = 0;
var document = {title:"Hideout"};
var window = {
  HideoutConsole:{},
  location:{hash:"#token=" + operatorToken,pathname:"/",search:""},
  history:{replaceState:function() { window.location.hash = ""; }},
  addEventListener:function() {}
};
class URLSearchParams {
  constructor(raw) {
    this.value = String(raw || "").startsWith("token=") ?
      decodeURIComponent(String(raw).slice(6)) : "";
  }
  get(name) { return name === "token" ? this.value : null; }
}
class Headers {
  constructor(initial) {
    this.values = {};
    for (const [name,value] of Object.entries(initial || {})) {
      this.set(name,value);
    }
  }
  set(name,value) { this.values[String(name).toLowerCase()] = String(value); }
  get(name) { return this.values[String(name).toLowerCase()] || ""; }
}
class AbortController {
  constructor() { this.signal = {}; }
  abort() {}
}
var EventSource = function() {};
var fetch = function(path,init) {
  const method = init && init.method || "GET";
  const body = init && init.body || "";
  requests.push({
    path,method,body,
    tokenMatched:init.headers.get("X-Hideout-UI-Token") === operatorToken,
    contentType:init.headers.get("Content-Type"),
    credentials:init.credentials,
    cache:init.cache,
    signal:Boolean(init.signal),
    keepalive:Boolean(init.keepalive)
  });
  let resource = "";
  let data = {};
  if (path === "/api/v1/decisions/" + decisionID) {
    resource = "decision/inspect";
    data = {
      id:decisionID,revision:7,state:"pending",kind:"evidence.share",
      preview:{summary:"review"},allowedActions:["approve","deny"],
      defaultOutcome:"no-release"
    };
  } else if (path.endsWith("/claim")) {
    resource = "decision/claim";
    claimCalls++;
    data = {
      decisionId:decisionID,claimToken,
      revision:claimCalls === 1 ? 8 : 10
    };
  } else if (path.endsWith("/approve")) {
    resource = "decision/approve";
    data = {decisionId:decisionID,status:"applied"};
  } else if (path.endsWith("/release")) {
    resource = "decision/release";
    data = {decisionId:decisionID,state:"pending"};
  } else if (path.endsWith("/ack")) {
    resource = "notice/ack";
    data = {noticeId:noticeID};
  }
  return Promise.resolve({
    ok:true,status:200,statusText:"OK",
    text:function() {
      return Promise.resolve(JSON.stringify({
        version:"hideout.manager-api/v1",resource,data,errors:[]
      }));
    }
  });
};
` + mustAsset("client.js") + `
const Client = window.HideoutConsole.Client;
Client.decisionInspect(decisionID)
  .then((record) => Client.decisionClaim(decisionID,record.revision))
  .then((claim) => Client.decisionRelease(
    decisionID,claim.claimToken,claim.revision,true
  ))
  .then(() => Client.decisionClaim(decisionID,9))
  .then((claim) => Client.decisionResolve(
    decisionID,"approve",claim.claimToken
  ))
  .then(() => Client.noticeAck(noticeID))
  .then(() => JSON.stringify({requests,claimToken}));
`)
	if err != nil {
		t.Fatalf("run browser action-center client: %v", err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled {
		t.Fatalf("action-center promise=%T state=%v", value.Export(), promise)
	}
	var proof struct {
		ClaimToken string `json:"claimToken"`
		Requests   []struct {
			Path         string `json:"path"`
			Method       string `json:"method"`
			Body         string `json:"body"`
			TokenMatched bool   `json:"tokenMatched"`
			ContentType  string `json:"contentType"`
			Credentials  string `json:"credentials"`
			Cache        string `json:"cache"`
			Signal       bool   `json:"signal"`
			Keepalive    bool   `json:"keepalive"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(promise.Result().String()), &proof); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		"/api/v1/decisions/decision-web-client",
		"/api/v1/decisions/decision-web-client/claim",
		"/api/v1/decisions/decision-web-client/release",
		"/api/v1/decisions/decision-web-client/claim",
		"/api/v1/decisions/decision-web-client/approve",
		"/api/v1/notices/notice-web-client/ack",
	}
	if len(proof.Requests) != len(wantPaths) {
		t.Fatalf("action-center requests=%+v", proof.Requests)
	}
	for index, request := range proof.Requests {
		if request.Path != wantPaths[index] ||
			strings.Contains(request.Path, proof.ClaimToken) ||
			!request.TokenMatched || request.Credentials != "omit" ||
			request.Cache != "no-store" || !request.Signal {
			t.Fatalf("action-center request[%d]=%+v", index, request)
		}
		if index == 0 {
			if request.Method != http.MethodGet || request.Body != "" {
				t.Fatalf("decision inspect request=%+v", request)
			}
			continue
		}
		if request.Method != http.MethodPost ||
			request.ContentType != "application/json" {
			t.Fatalf("action-center mutation[%d]=%+v", index, request)
		}
		if request.Keepalive != (index == 2) {
			t.Fatalf("action-center keepalive[%d]=%+v", index, request)
		}
	}
	var claimBody, secondClaimBody, approveBody, releaseBody, noticeBody map[string]any
	for encoded, target := range map[string]*map[string]any{
		proof.Requests[1].Body: &claimBody,
		proof.Requests[2].Body: &releaseBody,
		proof.Requests[3].Body: &secondClaimBody,
		proof.Requests[4].Body: &approveBody,
		proof.Requests[5].Body: &noticeBody,
	} {
		if err := json.Unmarshal([]byte(encoded), target); err != nil {
			t.Fatal(err)
		}
	}
	if claimBody["decisionId"] != "decision-web-client" ||
		claimBody["expectedVersion"] != "hideout.decision/v1" ||
		claimBody["expectedRevision"] != float64(7) ||
		claimBody["surface"] != "webui" ||
		claimBody["leaseSeconds"] != float64(60) ||
		secondClaimBody["expectedRevision"] != float64(9) ||
		approveBody["claimToken"] != proof.ClaimToken ||
		releaseBody["claimToken"] != proof.ClaimToken ||
		releaseBody["expectedRevision"] != float64(8) ||
		noticeBody["noticeId"] != "notice-web-client" ||
		noticeBody["surface"] != "webui" {
		t.Fatalf(
			"action-center bodies claim=%v secondClaim=%v approve=%v release=%v notice=%v",
			claimBody,
			secondClaimBody,
			approveBody,
			releaseBody,
			noticeBody,
		)
	}
}

func TestBrowserClientBindsEventStreamToSnapshotSequence(t *testing.T) {
	runtime := goja.New()
	value, err := runtime.RunString(`
const secretToken = "ui_" + "f".repeat(48);
const sources = [];
let opened = 0;
let closed = 0;
var document = {title:"Hideout"};
var window = {
  HideoutConsole:{},
  location:{hash:"#token=" + secretToken,pathname:"/",search:""},
  history:{replaceState:function() { window.location.hash = ""; }},
  addEventListener:function() {}
};
class URLSearchParams {
  constructor(raw) {
    this.value = String(raw || "").startsWith("token=") ?
      decodeURIComponent(String(raw).slice(6)) : "";
  }
  get(name) { return name === "token" ? this.value : null; }
}
function EventSource(path) {
  this.path = path;
  this.close = function() { closed++; };
  sources.push(this);
}
` + mustAsset("client.js") + `
const Client = window.HideoutConsole.Client;
const handlers = {
  open:function() { opened++; },
  event:function() {},
  error:function() {}
};
const stream = Client.events(handlers,17);
sources[0].onopen();
let invalid = "";
try {
  Client.events(handlers,-1);
} catch (error) {
  invalid = error.message;
}
stream.close();
JSON.stringify({
  path:sources[0].path,
  sourceCount:sources.length,
  opened,
  closed,
  invalid
});
`)
	if err != nil {
		t.Fatalf("run sequence-bound browser event stream: %v", err)
	}
	var proof struct {
		Path        string `json:"path"`
		SourceCount int    `json:"sourceCount"`
		Opened      int    `json:"opened"`
		Closed      int    `json:"closed"`
		Invalid     string `json:"invalid"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	wantPath := "/daemon/events?token=ui_" + strings.Repeat("f", 48) +
		"&since=17"
	if proof.Path != wantPath || proof.SourceCount != 1 || proof.Opened != 1 ||
		proof.Closed != 1 ||
		proof.Invalid != "event stream snapshot sequence is invalid" {
		t.Fatalf("sequence-bound browser event stream mismatch: %+v", proof)
	}
}

func TestBrowserClientAndAppHaveNoHealthyStreamPolling(t *testing.T) {
	client := mustAsset("client.js")
	app := mustAsset("app.js")
	state := mustAsset("state.js")
	for _, forbidden := range []string{
		"setInterval",
		"localStorage",
		"sessionStorage",
		"document.cookie",
	} {
		if strings.Contains(client+app+state, forbidden) {
			t.Fatalf("browser authority lifecycle introduced %q", forbidden)
		}
	}
	for _, required := range []string{
		"function refreshCredentialFromLocation()",
		"requestEpoch !== credentialEpoch",
		"function notifyAuthorityLost(reason)",
		"function scheduleAuthoritativeReseed(reason)",
		"reconnectDelays",
		`state.health.state === "credential-expired"`,
		"root.State.beginReseed(",
		"root.State.streamConnected(state)",
		`"&since="`,
		`data-requires-authority="true"`,
		"async function decisionInspect(",
		"async function decisionClaim(",
		"async function decisionRelease(",
		"async function decisionResolve(",
		"async function noticeAck(",
		`acknowledge.dataset.action = "ack-notice"`,
		`review.dataset.action = "review-decision"`,
		"activeDecisionReview !== decision.id",
		"result.claimToken,",
		"result.revision,",
	} {
		if !strings.Contains(client+app+state, required) {
			t.Fatalf("browser authority lifecycle missing %q", required)
		}
	}
}
