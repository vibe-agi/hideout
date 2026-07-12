package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestHostAppLifecycleActionsAreAcceptedByAuditSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "audit-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode audit schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("audit-event.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("audit-event.schema.json")
	if err != nil {
		t.Fatal(err)
	}

	actions := []string{
		"host.app.install",
		"host.app.validate",
		"host.app.test",
		"host.app.trust",
		"host.app.enable",
		"host.app.update",
		"host.app.permission-diff",
		"host.app.conflict",
		"host.app.disable",
		"host.app.revoke",
		"host.app.remove",
		"host.app.launch",
		"host.app.refuse",
		"host.app.identity-drift",
		"host.app.digest-mismatch",
	}
	for _, action := range actions {
		event := Event{
			Time:     time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
			Session:  "session-032",
			Profile:  "default",
			Backend:  "native",
			Action:   action,
			Decision: "allow",
			Details:  map[string]any{"packId": "community.editor"},
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); err != nil {
			t.Fatalf("audit action %q rejected by schema: %v", action, err)
		}
	}
}

func TestRedactDetailsStripsAllControlPlaneFieldNames(t *testing.T) {
	// Every Core control-plane field name in controlPlaneKeys must be stripped,
	// so none can be silently dropped from the set without a test failing.
	for _, key := range []string{"capabilityToken", "brokerToken", "uiToken", "managerToken", "claimToken", "tokenHash", "overlayObject", "contentObject"} {
		got := RedactDetails(map[string]any{key: "sensitive-control-plane-value"})
		if got[key] != "REDACTED" {
			t.Fatalf("control-plane field %q should be redacted, got %+v", key, got)
		}
	}
}

func TestWriterEmitStripsControlPlaneToDisk(t *testing.T) {
	// The storage-time redaction wiring in Writer.Emit must strip control-plane
	// material into the on-disk JSONL, not only when RedactDetails is called
	// directly.
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Emit(Event{
		Action:   "host.open",
		Decision: "allow",
		Details: map[string]any{
			"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
			"machineId":       "0123456789abcdef0123456789abcdef",
			"target":          "https://example.com/?token=user-value",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "cap_0123456789abcdef0123456789abcdef") || strings.Contains(text, "0123456789abcdef0123456789abcdef") {
		t.Fatalf("on-disk audit leaked control-plane material: %s", text)
	}
	if !strings.Contains(text, "token=user-value") {
		t.Fatalf("on-disk audit should preserve user data verbatim: %s", text)
	}
}

func TestRedactStringPreservesUserURLData(t *testing.T) {
	// User/application data in a URL is host-local evidence and is preserved
	// verbatim; Core does not guess which values are secrets.
	cases := []string{
		"https://user:pass@example.com/path?token=abc&ok=1",
		"https://example.com/callback?code=authcode&state=xyz",
		"https://example.com/ship?postal_code=90210&area_code=415",
		"https://example.com/i18n?language_code=en-US",
	}
	for _, in := range cases {
		if got := RedactString(in); got != in {
			t.Fatalf("user URL data should be preserved verbatim: in=%q got=%q", in, got)
		}
	}
}

func TestRedactStringStripsSetupCredentialAssignments(t *testing.T) {
	in := "setupCredential=raw-secret rootControlSSHConfig=/Users/null/.lima/hideout/ssh.config setupToken:cap_0123456789abcdef0123456789abcdef keep-user-data"
	got := RedactString(in)
	for _, leaked := range []string{"raw-secret", "rootControlSSHConfig=/Users", "cap_0123456789abcdef"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("setup credential assignment leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "keep-user-data") {
		t.Fatalf("user data should remain: %s", got)
	}
}

func TestRedactStringStripsHostFSOverlayObjectPathsAndClaimTokens(t *testing.T) {
	in := "/Users/null/.hideout/sessions/ses_1/hostfs-overlay/objects/hfwobj_secret claim_0123456789abcdef0123456789abcdef keep-user-path=/Users/alice/project/config.json"
	got := RedactString(in)
	for _, leaked := range []string{"hfwobj_secret", "claim_0123456789abcdef"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("HostFS control-plane material leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "keep-user-path=/Users/alice/project/config.json") {
		t.Fatalf("user path should remain: %s", got)
	}
}

func TestWriterSerializesConcurrentEmits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const events = 64
	var wg sync.WaitGroup
	for i := 0; i < events; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Emit(Event{
				Session:  "ses_concurrent",
				Profile:  "default",
				Backend:  "native",
				Action:   "host.open",
				Decision: "allow",
				Details:  map[string]any{"target": "https://example.com/?token=secret"},
			}); err != nil {
				t.Errorf("Emit: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, line := range splitNonEmptyLines(string(data)) {
		lines++
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL line %d: %v\n%s", lines, err, line)
		}
		if event.Details["target"] != "https://example.com/?token=secret" {
			t.Fatalf("user URL data should be preserved verbatim: %+v", event.Details)
		}
	}
	if lines != events {
		t.Fatalf("lines=%d want %d\n%s", lines, events, data)
	}
	if err := w.Emit(Event{Action: "after.close"}); err == nil {
		t.Fatalf("Emit after Close should fail")
	}
}

func splitNonEmptyLines(s string) []string {
	var out []string
	start := 0
	for i, ch := range s {
		if ch != '\n' {
			continue
		}
		if start < i {
			out = append(out, s[start:i])
		}
		start = i + 1
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func TestRedactStringPreservesUserCommandData(t *testing.T) {
	// Command flags and assignments are user data; Core does not guess.
	in := "curl --token abc --api-key=sk-123 https://user:pass@example.com/path?token=abc&ok=1 password=hunter2"
	if got := RedactString(in); got != in {
		t.Fatalf("user command data should be preserved verbatim:\n in=%q\ngot=%q", in, got)
	}
}

func TestRedactStringPreservesUserHeadersButStripsMachineID(t *testing.T) {
	// User-supplied headers are preserved verbatim. Generated machine-id is
	// Core identity material of known shape and is stripped deterministically.
	in := "Authorization: Bearer tok_123\nX-API-Key: sk-123\nCookie: sid=abc; theme=dark\nmachine-id=0123456789abcdef0123456789abcdef\nguestMachineID: fedcba9876543210fedcba9876543210\nok=value"
	got := RedactString(in)
	want := "Authorization: Bearer tok_123\nX-API-Key: sk-123\nCookie: sid=abc; theme=dark\nmachine-id=REDACTED\nguestMachineID: REDACTED\nok=value"
	if got != want {
		t.Fatalf("unexpected redaction:\n%s", got)
	}
}

func TestRedactValuePreservesUserFieldsContainingMachineIDSubstring(t *testing.T) {
	// Only Core's own machine-id field names (machineId, guestMachineId and
	// separator variants) are control-plane. User business fields that merely
	// contain the substring are user data and stay verbatim.
	got := RedactDetails(map[string]any{
		"customerMachineId":         "customer-asset-42",
		"externalMachineIdentifier": "rack-7-slot-3",
	})
	if got["customerMachineId"] != "customer-asset-42" {
		t.Fatalf("user field customerMachineId should be preserved verbatim: %+v", got)
	}
	if got["externalMachineIdentifier"] != "rack-7-slot-3" {
		t.Fatalf("user field externalMachineIdentifier should be preserved verbatim: %+v", got)
	}
}

func TestRedactStringStripsHideoutSecretColonAndJSONForms(t *testing.T) {
	// The HIDEOUT_SECRET_* namespace is self-known, so backing values adjacent
	// to the name are stripped in every formatting Core or tooling can emit:
	// KEY=value, KEY: value, and JSON "KEY":"value".
	cases := []struct{ in, want string }{
		{
			in:   "HIDEOUT_SECRET_DEFAULT_PROXY: socks5://user:pass@127.0.0.1:1080",
			want: "HIDEOUT_*=REDACTED",
		},
		{
			in:   `{"HIDEOUT_SECRET_DEFAULT_PROXY":"socks5://user:pass@127.0.0.1:1080"}`,
			want: `{"HIDEOUT_*":"REDACTED"}`,
		},
		{
			in:   `env dump: HIDEOUT_SECRET_OTHER:socks5://127.0.0.1:1080 done`,
			want: "env dump: HIDEOUT_*=REDACTED done",
		},
		{
			// Deliberate over-eat: in prose, the single token following
			// "HIDEOUT_SECRET_FOO:" is consumed. Core cannot tell prose from a
			// value dump, and eating one adjacent token is the conservative
			// side for a self-known secret namespace.
			in:   "unknown variable HIDEOUT_SECRET_FOO: not found",
			want: "unknown variable HIDEOUT_*=REDACTED found",
		},
		{
			// A bare name followed by prose without a separator is still just
			// a name collapse; no value is eaten.
			in:   "missing HIDEOUT_SECRET_DEFAULT_PROXY and exiting",
			want: "missing HIDEOUT_* and exiting",
		},
	}
	for _, tt := range cases {
		if got := RedactString(tt.in); got != tt.want {
			t.Fatalf("hideout secret form not stripped:\n in=%q\ngot=%q\nwant=%q", tt.in, got, tt.want)
		}
	}
}

func TestRedactStringStripsControlPlaneTokens(t *testing.T) {
	// Core-minted token values (cap_/ui_ + hex) are self-known and stripped.
	in := "broker cap_0123456789abcdef0123456789abcdef ui token ui_fedcba9876543210fedcba9876543210 done"
	want := "broker REDACTED ui token REDACTED done"
	if got := RedactString(in); got != want {
		t.Fatalf("control-plane token not stripped:\n%s", got)
	}
	// A user string that merely starts with cap_ but is not a minted token
	// (non-hex) is preserved.
	if got := RedactString("cap_manual_preview_1"); got != "cap_manual_preview_1" {
		t.Fatalf("non-token cap_ value should be preserved: %s", got)
	}
}

func TestRedactDetailsDeterministic(t *testing.T) {
	// Deterministic redaction: Core strips only its own control-plane field
	// names, minted token values, generated machine-id, and the
	// HIDEOUT_SECRET_* namespace. All user/application data is preserved.
	got := RedactDetails(map[string]any{
		"capabilityToken":  "cap_0123456789abcdef0123456789abcdef",
		"proxySecretRef":   "default-proxy",
		"identityId":       "id_traceable",
		"sourceIdentityId": "id_source",
		"machineId":        "0123456789abcdef0123456789abcdef",
		"message":          "guest machine-id 0123456789abcdef0123456789abcdef is ready",
		"target":           "https://user:pass@example.com/path?auth=abc&ok=1",
		"argv":             []string{"open", "--token", "abc123", "--api-key=sk-123", "https://example.com/?code=abc"},
		"nested": map[string]any{
			"password":       "hunter2",
			"guestMachineID": "fedcba9876543210fedcba9876543210",
			"urls": []any{
				"https://example.com/callback?code=abc",
				map[string]any{"apiKey": "abc123"},
			},
		},
		"env": map[string]string{
			"SERVICE_TOKEN": "secret",
			"TERM":          "xterm-256color",
		},
	})
	// Control-plane and identity material: stripped.
	if got["capabilityToken"] != "REDACTED" {
		t.Fatalf("control-plane capability token field not redacted: %+v", got)
	}
	if got["machineId"] != "REDACTED" {
		t.Fatalf("machineId field should be redacted: %+v", got)
	}
	if got["message"] != "guest machine-id REDACTED is ready" {
		t.Fatalf("message machine-id should be redacted: %+v", got)
	}
	nested := got["nested"].(map[string]any)
	if nested["guestMachineID"] != "REDACTED" {
		t.Fatalf("nested machine ID field should be redacted: %+v", nested)
	}
	// Identifiers and user data: preserved verbatim.
	if got["proxySecretRef"] != "default-proxy" {
		t.Fatalf("secret ref identifier should be preserved: %+v", got)
	}
	if got["identityId"] != "id_traceable" || got["sourceIdentityId"] != "id_source" {
		t.Fatalf("identity lineage IDs should be preserved: %+v", got)
	}
	if got["target"] != "https://user:pass@example.com/path?auth=abc&ok=1" {
		t.Fatalf("user target should be preserved verbatim: %+v", got["target"])
	}
	argv := got["argv"].([]string)
	if argv[2] != "abc123" || argv[3] != "--api-key=sk-123" || argv[4] != "https://example.com/?code=abc" {
		t.Fatalf("user argv should be preserved verbatim: %+v", argv)
	}
	if nested["password"] != "hunter2" {
		t.Fatalf("user password field should be preserved verbatim: %+v", nested)
	}
	urls := nested["urls"].([]any)
	if urls[0] != "https://example.com/callback?code=abc" {
		t.Fatalf("user URL should be preserved verbatim: %+v", urls)
	}
	if urls[1].(map[string]any)["apiKey"] != "abc123" {
		t.Fatalf("user apiKey field should be preserved verbatim: %+v", urls[1])
	}
	env := got["env"].(map[string]string)
	if env["SERVICE_TOKEN"] != "secret" || env["TERM"] != "xterm-256color" {
		t.Fatalf("user env should be preserved verbatim: %+v", env)
	}
}

func TestRedactHideoutSecretBackingNames(t *testing.T) {
	// The HIDEOUT_SECRET_* backing namespace is self-known: names collapse to
	// HIDEOUT_*, assignments and env-map values under such keys are REDACTED.
	// A bare resolved value carried as an unlabeled argv token (arg[3]) is
	// preserved because Core cannot distinguish it from a user URL; real proxy
	// secrets reach audit only under the HIDEOUT_SECRET_* form above, never as
	// a bare argv element.
	got := RedactDetails(map[string]any{
		"message": "missing HIDEOUT_SECRET_DEFAULT_PROXY and HIDEOUT_SECRET_OTHER=socks5://user:pass@127.0.0.1:1080",
		"argv": []string{
			"env",
			"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080",
			"HIDEOUT_SECRET_OTHER",
			"socks5://user:pass@127.0.0.1:1080",
		},
		"env": map[string]string{
			"HIDEOUT_SECRET_DEFAULT_PROXY": "socks5://user:pass@127.0.0.1:1080",
		},
	})
	text := got["message"].(string)
	if text != "missing HIDEOUT_* and HIDEOUT_*=REDACTED" {
		t.Fatalf("message leaked hideout secret backing name: %s", text)
	}
	argv := got["argv"].([]string)
	if argv[1] != "HIDEOUT_*=REDACTED" || argv[2] != "HIDEOUT_*" || argv[3] != "socks5://user:pass@127.0.0.1:1080" {
		t.Fatalf("argv redaction mismatch: %+v", argv)
	}
	env := got["env"].(map[string]string)
	if _, ok := env["HIDEOUT_SECRET_DEFAULT_PROXY"]; ok {
		t.Fatalf("env key leaked hideout secret backing name: %+v", env)
	}
	if env["HIDEOUT_*"] != "REDACTED" {
		t.Fatalf("env value should be redacted under generic key: %+v", env)
	}
}
