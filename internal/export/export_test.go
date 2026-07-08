package export

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/adapterpack"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestExportControlPlaneCleanAcrossSourcesAndResolvesReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func(t *testing.T, out, storeRoot string) Request
		want string
	}{
		{
			name: "audit",
			req: func(t *testing.T, out, storeRoot string) Request {
				return baseRequest(out, storeRoot, []AuditEvent{testAuditEvent(map[string]any{
					"target":          "https://example.com/path?token=user",
					"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
					"machineId":       "0123456789abcdef0123456789abcdef",
					"message":         "HIDEOUT_SECRET_TOKEN=super-secret",
				})})
			},
			want: "https://example.com/path?token=user",
		},
		{
			name: "bundle",
			req: func(t *testing.T, out, storeRoot string) Request {
				dir := t.TempDir()
				logPath := filepath.Join(dir, "test-release-dogfood.log")
				mustWriteExportTest(t, logPath, "bundle log user-value HIDEOUT_SECRET_PROXY=secret\n")
				mustWriteExportTest(t, filepath.Join(dir, "manifest.json"), `{
  "schema": "hideout.release-dogfood.v1",
  "evidence": {"directory": "`+dir+`", "log": "test-release-dogfood.log"},
  "gates": ["gate0"],
  "capabilityToken": "cap_0123456789abcdef0123456789abcdef"
}`)
				req := Request{
					Source:                  SourceBundle,
					BundlePath:              dir,
					Out:                     out,
					StoreRoot:               storeRoot,
					AcknowledgeFullFidelity: true,
				}
				return req
			},
			want: "bundle log user-value",
		},
		{
			name: "boundary",
			req: func(t *testing.T, out, storeRoot string) Request {
				auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
				mustWriteExportTest(t, auditPath, `{"time":"2026-07-07T00:00:00Z","session":"ses_1","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://example.com/user","machineId":"0123456789abcdef0123456789abcdef"}}`+"\n")
				return Request{
					Source: SourceBoundarySummary,
					BoundarySummary: map[string]any{
						"version":   "hideout.boundary-summary/v1",
						"evidence":  "available",
						"auditPath": auditPath,
					},
					BoundaryAuditPath:       auditPath,
					Out:                     out,
					StoreRoot:               storeRoot,
					AcknowledgeFullFidelity: true,
				}
			},
			want: "https://example.com/user",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "artifact.json")
			storeRoot := t.TempDir()
			result, err := Apply(tc.req(t, out, storeRoot))
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if result.ArtifactPath != out {
				t.Fatalf("artifact path=%q want %q", result.ArtifactPath, out)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, leaked := range []string{
				"HIDEOUT_SECRET", "super-secret", "cap_0123456789abcdef",
				"capabilityToken", "0123456789abcdef0123456789abcdef",
			} {
				if strings.Contains(string(data), leaked) {
					t.Fatalf("%s artifact leaked %q:\n%s", tc.name, leaked, data)
				}
			}
			if !strings.Contains(string(data), tc.want) {
				t.Fatalf("%s artifact missing resolved user evidence %q:\n%s", tc.name, tc.want, data)
			}
		})
	}
}

func TestExportStripsPrivilegedSetupCredentials(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	_, err := Apply(baseRequest(out, t.TempDir(), []AuditEvent{{
		Time:     time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
		Session:  "ses_1",
		Profile:  "default",
		Backend:  "lima",
		Action:   "hideout.privileged_setup",
		Decision: "succeeded",
		Details: map[string]any{
			"category":           "network",
			"setupIdentityKind":  "root-control-ssh",
			"separateFromTarget": true,
			"reason":             "setupCredential=raw-secret rootControlSSHConfig=/Users/null/.lima/hideout/ssh.config",
		},
	}}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leaked := range []string{"raw-secret", "rootControlSSHConfig", "/Users/null/.lima/hideout/ssh.config"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("privileged setup export leaked %q:\n%s", leaked, text)
		}
	}
	if !strings.Contains(text, `"setupIdentityKind": "root-control-ssh"`) {
		t.Fatalf("privileged setup export lost non-secret evidence:\n%s", text)
	}
}

func TestExportIncludesHostFSWriteEvidenceWithoutClaimTokenOrOverlayObjectPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	event := testAuditEvent(map[string]any{
		"operation":       "replace",
		"path":            "/Users/alice/project/config.json",
		"operationId":     "hfwop_123",
		"decisionId":      "hfwdec_123",
		"status":          "applied",
		"changedPaths":    []string{"/Users/alice/project/config.json"},
		"claimToken":      "claim_0123456789abcdef0123456789abcdef",
		"tokenHash":       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"overlayObject":   "/Users/alice/.hideout/sessions/ses_1/hostfs-overlay/objects/hfwobj_secret",
		"privilegeStatus": "enforced",
	})
	event.Action = "host.fs.overlay.apply"
	_, err := Apply(baseRequest(out, t.TempDir(), []AuditEvent{event}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"host.fs.overlay.apply", "hfwop_123", "hfwdec_123", "/Users/alice/project/config.json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("artifact missing HostFS write evidence %q:\n%s", want, text)
		}
	}
	for _, leaked := range []string{"claim_0123456789abcdef", "tokenHash", "hfwobj_secret", "hostfs-overlay/objects"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("artifact leaked %q:\n%s", leaked, text)
		}
	}
}

func TestExportIncludesAdapterPackLifecycleEvidenceWithoutControlPlaneSecrets(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	event := testAuditEvent(map[string]any{
		"operation":      "enable",
		"packId":         "example.pack",
		"state":          "installed",
		"revisionId":     "rev_abc",
		"manifestDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sourceDigest":   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"testStatus":     "passed",
		"reason":         "cap_0123456789abcdef0123456789abcdef HIDEOUT_SECRET_PACK=raw",
	})
	event.Action = adapterpack.Action
	_, err := Apply(baseRequest(out, t.TempDir(), []AuditEvent{event}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{adapterpack.Action, "example.pack", "rev_abc", "passed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("artifact missing adapter-pack evidence %q:\n%s", want, text)
		}
	}
	for _, leaked := range []string{"cap_0123456789abcdef", "HIDEOUT_SECRET_PACK", "raw"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("artifact leaked %q:\n%s", leaked, text)
		}
	}
}

func TestExportEmptyAuditSelectionProducesZeroRecordArtifact(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	result, err := Apply(Request{
		Source:                  SourceAudit,
		Out:                     out,
		StoreRoot:               t.TempDir(),
		AcknowledgeFullFidelity: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.RecordCount != 0 {
		t.Fatalf("record count=%d want 0", result.RecordCount)
	}
	artifact := readArtifactForTest(t, out)
	if artifact.RecordCount != 0 || artifact.Notice != "0 records matched" {
		t.Fatalf("zero export artifact mismatch: %+v", artifact)
	}
}

func TestExportEmptyAuditSelectionAllowsRedactSelector(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	result, err := Apply(Request{
		Source:          SourceAudit,
		Out:             out,
		StoreRoot:       t.TempDir(),
		RedactSelectors: []string{"target"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.RecordCount != 0 {
		t.Fatalf("record count=%d want 0", result.RecordCount)
	}
	artifact := readArtifactForTest(t, out)
	if artifact.RecordCount != 0 || artifact.Notice != "0 records matched" ||
		artifact.Provenance.Decision.Mode != DecisionRedact {
		t.Fatalf("zero redact export artifact mismatch: %+v", artifact)
	}
}

func TestExportRejectsNonLocalOut(t *testing.T) {
	_, err := Apply(baseRequest("https://example.com/artifact.json", t.TempDir(), []AuditEvent{}))
	if err == nil || !strings.Contains(err.Error(), "local path") {
		t.Fatalf("expected local path refusal, got %v", err)
	}
}

func TestExportMissingDecisionFailsClosedWithNoArtifactAndMetaAudit(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	storeRoot := t.TempDir()
	_, err := Apply(Request{
		Source:      SourceAudit,
		AuditEvents: []AuditEvent{testAuditEvent(map[string]any{"target": "https://example.com/private"})},
		Out:         out,
		StoreRoot:   storeRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "user data is present") {
		t.Fatalf("expected missing decision refusal, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("artifact should not exist after fail-closed export: %v", statErr)
	}
	events := readMetaAuditEvents(t, storeRoot)
	if len(events) != 1 || events[0].Decision != "deny" || events[0].Details["reason"] == "" {
		t.Fatalf("fail-closed meta-audit mismatch: %+v", events)
	}
}

func TestExportRedactionAndSerializationErrorsFailClosedWithNoPartial(t *testing.T) {
	storeRoot := t.TempDir()
	writeRedactionProfile(t, storeRoot, "default", `function redactAudit(ctx) { return { reason: "missing details" }; }`)
	policyOut := filepath.Join(t.TempDir(), "policy.json")
	_, err := Apply(Request{
		Source:                  SourceAudit,
		AuditEvents:             []AuditEvent{testAuditEvent(map[string]any{"target": "secret"})},
		Out:                     policyOut,
		StoreRoot:               storeRoot,
		AcknowledgeFullFidelity: true,
	})
	if err == nil || !strings.Contains(err.Error(), "audit redaction script") {
		t.Fatalf("expected policy error, got %v", err)
	}
	if _, statErr := os.Stat(policyOut); !os.IsNotExist(statErr) {
		t.Fatalf("policy-error artifact should not exist: %v", statErr)
	}

	stripOut := filepath.Join(t.TempDir(), "strip.json")
	_, err = Apply(Request{
		Source:                  SourceAudit,
		AuditEvents:             []AuditEvent{testAuditEvent(map[string]any{"target": math.Inf(1)})},
		Out:                     stripOut,
		StoreRoot:               t.TempDir(),
		AcknowledgeFullFidelity: true,
	})
	if err == nil {
		t.Fatal("expected serialization/control-plane strip failure")
	}
	if _, statErr := os.Stat(stripOut); !os.IsNotExist(statErr) {
		t.Fatalf("strip-error artifact should not exist: %v", statErr)
	}
}

func TestExportMetaAuditSuccessIsLocalSummaryOnly(t *testing.T) {
	out := filepath.Join(t.TempDir(), "artifact.json")
	storeRoot := t.TempDir()
	_, err := Apply(baseRequest(out, storeRoot, []AuditEvent{testAuditEvent(map[string]any{
		"target": "https://example.com/private",
	})}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	events := readMetaAuditEvents(t, storeRoot)
	if len(events) != 1 {
		t.Fatalf("meta-audit count=%d want 1: %+v", len(events), events)
	}
	event := events[0]
	if event.Action != Action || event.Decision != "allow" {
		t.Fatalf("meta-audit action/decision mismatch: %+v", event)
	}
	if event.Details["out"] != out {
		t.Fatalf("meta-audit should keep local out path verbatim: %+v", event.Details)
	}
	if strings.Contains(mustJSONExportTest(t, event.Details), "https://example.com/private") {
		t.Fatalf("meta-audit embedded source evidence: %+v", event.Details)
	}
}

func baseRequest(out, storeRoot string, events []AuditEvent) Request {
	return Request{
		Source:                  SourceAudit,
		AuditEvents:             events,
		Out:                     out,
		StoreRoot:               storeRoot,
		AcknowledgeFullFidelity: true,
		Now: func() time.Time {
			return time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
		},
	}
}

func testAuditEvent(details map[string]any) AuditEvent {
	return AuditEvent{
		Time:     time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
		Session:  "ses_1",
		Profile:  "default",
		Backend:  "native",
		Action:   "host.open",
		Decision: "allow",
		Details:  details,
	}
}

func readArtifactForTest(t *testing.T, path string) Artifact {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	return artifact
}

func readMetaAuditEvents(t *testing.T, storeRoot string) []AuditEvent {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(storeRoot, "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []AuditEvent
	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var event AuditEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			if event.Action == Action {
				events = append(events, event)
			}
		}
		if err := scanner.Err(); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return events
}

func mustWriteExportTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSONExportTest(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeRedactionProfile(t *testing.T, storeRoot, name, source string) {
	t.Helper()
	store := profile.Store{Root: storeRoot}
	p := profile.Default(name)
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "export-redact",
		Path:        "policy/redact.js",
		Entrypoints: []string{"redactAudit"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	mustWriteExportTest(t, filepath.Join(store.ProfileDir(name), "policy", "redact.js"), source)
}
