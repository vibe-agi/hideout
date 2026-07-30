package uiweb_assets

import (
	"encoding/json"
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
		`data-requires-authority="true"`,
	} {
		if !strings.Contains(client+app+state, required) {
			t.Fatalf("browser authority lifecycle missing %q", required)
		}
	}
}
