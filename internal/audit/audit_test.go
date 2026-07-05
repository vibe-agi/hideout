package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRedactStringURLSecrets(t *testing.T) {
	got := RedactString("https://user:pass@example.com/path?token=abc&ok=1")
	if got != "https://example.com/path?ok=1&token=REDACTED" {
		t.Fatalf("unexpected redaction: %s", got)
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
		if event.Details["target"] != "https://example.com/?token=REDACTED" {
			t.Fatalf("event was not redacted: %+v", event.Details)
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

func TestRedactStringURLSubstringAndAssignments(t *testing.T) {
	got := RedactString("curl --token abc --api-key=sk-123 https://user:pass@example.com/path?token=abc&ok=1 password=hunter2")
	if got != "curl --token REDACTED --api-key=REDACTED https://example.com/path?ok=1&token=REDACTED password=REDACTED" {
		t.Fatalf("unexpected redaction: %s", got)
	}
}

func TestRedactStringHeaderSecrets(t *testing.T) {
	in := "Authorization: Bearer tok_123\nX-API-Key: sk-123\nCookie: sid=abc; theme=dark\nmachine-id=0123456789abcdef0123456789abcdef\nguestMachineID: fedcba9876543210fedcba9876543210\nok=value"
	got := RedactString(in)
	want := "Authorization: Bearer REDACTED\nX-API-Key: REDACTED\nCookie: REDACTED\nmachine-id=REDACTED\nguestMachineID: REDACTED\nok=value"
	if got != want {
		t.Fatalf("unexpected redaction:\n%s", got)
	}
}

func TestRedactDetailsRecursiveAndKeyAware(t *testing.T) {
	got := RedactDetails(map[string]any{
		"capabilityToken":  "cap_secret",
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
	if got["capabilityToken"] != "REDACTED" {
		t.Fatalf("capability token not redacted: %+v", got)
	}
	if got["proxySecretRef"] != "default-proxy" {
		t.Fatalf("secret ref should be preserved: %+v", got)
	}
	if got["identityId"] != "id_traceable" || got["sourceIdentityId"] != "id_source" {
		t.Fatalf("identity lineage IDs should be preserved: %+v", got)
	}
	if got["machineId"] != "REDACTED" {
		t.Fatalf("machineId should be redacted: %+v", got)
	}
	if got["message"] != "guest machine-id REDACTED is ready" {
		t.Fatalf("message machine-id should be redacted: %+v", got)
	}
	if got["target"] != "https://example.com/path?auth=REDACTED&ok=1" {
		t.Fatalf("target not redacted: %+v", got["target"])
	}
	argv := got["argv"].([]string)
	if argv[2] != "REDACTED" || argv[3] != "--api-key=REDACTED" || argv[4] != "https://example.com/?code=REDACTED" {
		t.Fatalf("argv not redacted: %+v", argv)
	}
	nested := got["nested"].(map[string]any)
	if nested["password"] != "REDACTED" {
		t.Fatalf("nested password not redacted: %+v", nested)
	}
	if nested["guestMachineID"] != "REDACTED" {
		t.Fatalf("nested machine ID not redacted: %+v", nested)
	}
	urls := nested["urls"].([]any)
	if urls[0] != "https://example.com/callback?code=REDACTED" {
		t.Fatalf("nested URL not redacted: %+v", urls)
	}
	if urls[1].(map[string]any)["apiKey"] != "REDACTED" {
		t.Fatalf("nested api key not redacted: %+v", urls[1])
	}
	env := got["env"].(map[string]string)
	if env["SERVICE_TOKEN"] != "REDACTED" || env["TERM"] != "xterm-256color" {
		t.Fatalf("env redaction mismatch: %+v", env)
	}
}

func TestRedactHideoutSecretBackingNames(t *testing.T) {
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
	if argv[1] != "HIDEOUT_*=REDACTED" || argv[2] != "HIDEOUT_*" || argv[3] != "REDACTED" {
		t.Fatalf("argv leaked hideout secret backing name: %+v", argv)
	}
	env := got["env"].(map[string]string)
	if _, ok := env["HIDEOUT_SECRET_DEFAULT_PROXY"]; ok {
		t.Fatalf("env key leaked hideout secret backing name: %+v", env)
	}
	if env["HIDEOUT_*"] != "REDACTED" {
		t.Fatalf("env value should be redacted under generic key: %+v", env)
	}
}
