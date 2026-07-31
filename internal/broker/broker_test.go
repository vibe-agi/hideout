package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	hostfspkg "github.com/vibe-agi/hideout/internal/hostfs"
	overlaypkg "github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestServerRequestTimeoutIsExtendedOnlyForHostAppOpen(t *testing.T) {
	if got := serverRequestTimeout(Request{Action: cmdproxy.ActionHostAppOpenResource}); got != 70*time.Second {
		t.Fatalf("host-app timeout=%s", got)
	}
	if got := serverOperationTimeout(Request{Action: cmdproxy.ActionHostAppOpenResource}); got != 65*time.Second {
		t.Fatalf("host-app operation timeout=%s", got)
	}
	for _, action := range []string{"", cmdproxy.ActionHostOpen, cmdproxy.ActionCommandAdapter} {
		if got := serverRequestTimeout(Request{Action: action}); got != 5*time.Second {
			t.Fatalf("action %q timeout=%s", action, got)
		}
		if got := serverOperationTimeout(Request{Action: action}); got != 5*time.Second {
			t.Fatalf("action %q operation timeout=%s", action, got)
		}
	}
}

func TestReadinessProbeRequiresExactSessionAuthorityAndPerformsNoHostEffect(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{SessionID: "ses_1", Token: "cap_good", Opener: opener}
	valid := Request{
		ID: "req_ready", SessionID: "ses_1", CapabilityToken: "cap_good",
		Action: ActionReadinessProbe,
	}
	if resp := server.Handle(context.Background(), valid); resp.ExitCode != 0 || resp.Status != "ok" || resp.Decision != string(policy.Allow) {
		t.Fatalf("readiness response=%+v", resp)
	}
	bad := valid
	bad.ID = "req_bad"
	bad.CapabilityToken = "cap_bad"
	if resp := server.Handle(context.Background(), bad); resp.ExitCode == 0 {
		t.Fatalf("unauthorized readiness probe passed: %+v", resp)
	}
	for name, mutate := range map[string]func(*Request){
		"subject": func(req *Request) { req.Subject = "effect-subject" },
		"command": func(req *Request) { req.Command = "code" },
		"argv":    func(req *Request) { req.Argv = []string{"code", "."} },
		"route":   func(req *Request) { req.Route = string(policy.HostBroker) },
		"args":    func(req *Request) { req.Args = map[string]any{"target": "file.txt"} },
	} {
		t.Run("rejects_"+name, func(t *testing.T) {
			contaminated := valid
			contaminated.ID = "req_contaminated_" + name
			mutate(&contaminated)
			resp := server.Handle(context.Background(), contaminated)
			if resp.ExitCode == 0 || resp.Status != "bad-request" {
				t.Fatalf("effect-bearing readiness probe passed: %+v", resp)
			}
		})
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("readiness probe reached a host effect: %+v %+v", opener.urls, opener.files)
	}
}

type recordingOpener struct {
	urls  []string
	files []string
}

func (r *recordingOpener) OpenURL(_ context.Context, target string) error {
	r.urls = append(r.urls, target)
	return nil
}

func (r *recordingOpener) OpenFile(_ context.Context, target string) error {
	r.files = append(r.files, target)
	return nil
}

type blockingOpener struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockingOpener) OpenURL(context.Context, string) error {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return nil
}

func (b blockingOpener) OpenFile(context.Context, string) error {
	return errors.New("unexpected file open")
}

type failingOpener struct {
	urlErr  error
	fileErr error
}

func (f failingOpener) OpenURL(context.Context, string) error {
	if f.urlErr != nil {
		return f.urlErr
	}
	return errors.New("url opener failed")
}

func (f failingOpener) OpenFile(context.Context, string) error {
	if f.fileErr != nil {
		return f.fileErr
	}
	return errors.New("file opener failed")
}

type browserRecordingOpener struct {
	recordingOpener
	browserProfile string
}

func (r *browserRecordingOpener) BrowserProfile() string {
	return r.browserProfile
}

func publicHostEvaluator(p profile.Profile) policy.Evaluator {
	evaluator := policy.NewEvaluator(p)
	evaluator.ResolveHost = func(string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	return evaluator
}

func hostFSRequest(id, action, path string, extra map[string]any) Request {
	args := map[string]any{"path": path}
	for key, value := range extra {
		args[key] = value
	}
	return Request{
		ID:              id,
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "hostfs:daemon",
		Route:           "host-broker",
		Action:          action,
		Args:            args,
	}
}

func testHostFSService(t *testing.T, config hostfspkg.Config) *hostfspkg.Service {
	t.Helper()
	policy, err := hostfspkg.Build(hostfspkg.BuildInput{Profile: config})
	if err != nil {
		t.Fatal(err)
	}
	service := hostfspkg.NewService(policy)
	return &service
}

func testHostFSOverlayService(t *testing.T, config hostfspkg.Config, overlayRoot string) *hostfspkg.Service {
	t.Helper()
	service := testHostFSService(t, config)
	store, err := overlaypkg.NewStore(overlayRoot)
	if err != nil {
		t.Fatal(err)
	}
	service.Overlay = store
	service.Context = hostfspkg.OverlayContext{
		SessionID: "ses_1",
		Profile:   "default",
		Backend:   "native",
		Privilege: overlaypkg.Privilege{Status: "enforced", Reason: "target-no-sudo"},
	}
	return service
}

func auditPathFragment(t *testing.T, path string) string {
	t.Helper()
	encoded, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	return `"path":` + string(encoded)
}

func TestHandleRejectsBadToken(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_bad",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || resp.Stderr != "broker authorization failed" {
		t.Fatalf("expected bad token to fail: %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("bad token should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"deny"`,
		`"error":"broker authorization failed"`,
		`"requestId":"req_1"`,
		`"subject":"command:open"`,
		`"route":"host-broker"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("audit missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "cap_good") || strings.Contains(text, "cap_bad") || strings.Contains(text, "capabilityToken") {
		t.Fatalf("authorization audit leaked capability token material: %s", text)
	}
}

func TestCapabilityTokenEqualRequiresExactNonEmptyMatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
		ok   bool
	}{
		{name: "exact", got: "cap_good", want: "cap_good", ok: true},
		{name: "empty got", got: "", want: "cap_good"},
		{name: "empty want", got: "cap_good", want: ""},
		{name: "both empty", got: "", want: ""},
		{name: "prefix", got: "cap_good_extra", want: "cap_good"},
		{name: "wrong", got: "cap_bad", want: "cap_good"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := capabilityTokenEqual(tc.got, tc.want); got != tc.ok {
				t.Fatalf("capabilityTokenEqual(%q, %q)=%v want %v", tc.got, tc.want, got, tc.ok)
			}
		})
	}
}

func TestHandleAllowsHTTPSOpen(t *testing.T) {
	opener := &recordingOpener{}
	evaluator := policy.NewEvaluator(profile.Default("test"))
	evaluator.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "example.com" {
			return nil, errors.New("unexpected host")
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Evaluator: evaluator,
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Action:          "host.open",
		Route:           "host-broker",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	if len(opener.urls) != 1 || opener.urls[0] != "https://example.com" {
		t.Fatalf("opener did not see URL: %+v", opener.urls)
	}
}

func TestHandleHostFSReadAllowsGrantedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "visible.txt")
	if err := os.WriteFile(path, []byte("hello hostfs"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		HostFS: testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
			HostPath: path,
			Ops:      []hostfspkg.Op{hostfspkg.OpRead},
			Scope:    hostfspkg.ScopeExactFile,
			Reason:   "test",
		}}}),
		Audit: writer,
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_read", "host.fs.read", path, map[string]any{"offset": 0, "size": 5}))
	if resp.ExitCode != 0 || resp.Status != "ok" {
		t.Fatalf("expected HostFS read allow, got %+v", resp)
	}
	data, ok := resp.Data["dataBase64"].(string)
	if !ok {
		t.Fatalf("read response missing dataBase64: %+v", resp.Data)
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("read data=%q want hello", decoded)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.fs.read"`,
		`"decision":"allow"`,
		auditPathFragment(t, path),
		`"bytes":5`,
		`"policyEffect":"allow"`,
		`"policyReason":"matched-rule"`,
		`"ruleId":"profile-0"`,
		`"source":"profile"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("HostFS audit missing %q: %s", want, auditData)
		}
	}
}

func TestHandleHostFSStatAllowsGrantedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "visible.txt")
	if err := os.WriteFile(path, []byte("hello hostfs"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		HostFS: testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
			ID:       "hfs_stat_file",
			HostPath: path,
			Ops:      []hostfspkg.Op{hostfspkg.OpStat},
			Scope:    hostfspkg.ScopeExactFile,
			Reason:   "stat test",
		}}}),
		Audit: writer,
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_stat", "host.fs.stat", path, nil))
	if resp.ExitCode != 0 || resp.Status != "ok" {
		t.Fatalf("expected HostFS stat allow, got %+v", resp)
	}
	if resp.Data["kind"] != "file" || resp.Data["size"] != int64(len("hello hostfs")) {
		t.Fatalf("unexpected stat response data: %+v", resp.Data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.fs.stat"`,
		`"decision":"allow"`,
		auditPathFragment(t, path),
		`"policyEffect":"allow"`,
		`"policyReason":"matched-rule"`,
		`"ruleId":"hfs_stat_file"`,
		`"source":"profile"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("HostFS stat audit missing %q: %s", want, auditData)
		}
	}
}

func TestHandleHostFSDeniesUngrantPathWithoutLeak(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		HostFS:    testHostFSService(t, hostfspkg.Config{}),
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_deny", "host.fs.read", path, nil))
	if resp.Status != "denied" || resp.ExitCode != 126 || resp.Stderr != "hostfs path not found" || resp.Error == nil || resp.Error.Code != HostFSErrorPathHidden || resp.Error.Errno != ErrnoENOENT {
		t.Fatalf("expected hidden denial, got %+v", resp)
	}
	statResp := server.Handle(context.Background(), hostFSRequest("req_stat_hidden", "host.fs.stat", path, nil))
	if statResp.Status != "denied" || statResp.Error == nil || statResp.Error.Code != HostFSErrorPathHidden || statResp.Error.Errno != ErrnoENOENT {
		t.Fatalf("hidden stat did not preserve typed ENOENT: %+v", statResp)
	}
	if strings.Contains(resp.Stderr, root) || strings.Contains(resp.Stderr, "secret.txt") {
		t.Fatalf("HostFS response leaked host path: %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.fs.read"`,
		`"decision":"deny"`,
		auditPathFragment(t, path),
		`"hostfs path not found"`,
		`"policyEffect":"none"`,
		`"policyReason":"no-matching-grant"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("HostFS deny audit missing %q: %s", want, auditData)
		}
	}
}

func TestHandleHostFSAuditsDenyRuleIDWithoutPathLeak(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private-notes.txt")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		HostFS: testHostFSService(t, hostfspkg.Config{
			Grants: []hostfspkg.Rule{{
				ID:       "hfs_allow_dir",
				HostPath: root,
				Ops:      []hostfspkg.Op{hostfspkg.OpRead},
				Scope:    hostfspkg.ScopeDir,
				Reason:   "allow dir",
			}},
			Deny: []hostfspkg.Rule{{
				ID:       "hfs_deny_private",
				HostPath: filepath.Join(root, "private-*.txt"),
				Ops:      []hostfspkg.Op{hostfspkg.OpRead},
				Scope:    hostfspkg.ScopeGlob,
				Reason:   "sensitive path should not leak",
			}},
		}),
		Audit: writer,
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_deny_rule", "host.fs.read", path, nil))
	if resp.Status != "denied" || resp.ExitCode != 126 {
		t.Fatalf("expected HostFS deny rule to fail closed, got %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.fs.read"`,
		`"decision":"deny"`,
		auditPathFragment(t, path),
		`"policyEffect":"deny"`,
		`"policyReason":"matched-deny-rule"`,
		`"ruleId":"hfs_deny_private"`,
		`"source":"profile"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("HostFS deny-rule audit missing %q: %s", want, auditData)
		}
	}
	if strings.Contains(string(auditData), "sensitive path should not leak") {
		t.Fatalf("HostFS deny-rule audit leaked sensitive reason: %s", auditData)
	}
}

func TestHandleHostFSSymlinkEscapeAuditsCanonicalDeny(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		HostFS: testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
			ID:       "hfs_allow_dir",
			HostPath: root,
			Ops:      []hostfspkg.Op{hostfspkg.OpRead},
			Scope:    hostfspkg.ScopeDir,
			Reason:   "allow immediate files",
		}}}),
		Audit: writer,
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_symlink_escape", "host.fs.read", link, nil))
	if resp.Status != "denied" || resp.ExitCode != 126 || resp.Stderr != "hostfs path not found" {
		t.Fatalf("expected HostFS symlink escape denial, got %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	auditText := string(auditData)
	for _, want := range []string{
		`"action":"host.fs.read"`,
		`"decision":"deny"`,
		auditPathFragment(t, link),
		`"canonicalized":true`,
		`"policyEffect":"deny"`,
		`"policyReason":"symlink-target-not-granted"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("HostFS symlink audit missing %q: %s", want, auditData)
		}
	}
	if strings.Contains(auditText, outside) {
		t.Fatalf("HostFS symlink audit should not leak resolved target path: %s", auditData)
	}
}

func TestHandleHostFSListFiltersUngrantSiblings(t *testing.T) {
	root := t.TempDir()
	visible := filepath.Join(root, "visible.txt")
	hidden := filepath.Join(root, "hidden.txt")
	if err := os.WriteFile(visible, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidden, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostFS: testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
			HostPath: visible,
			Ops:      []hostfspkg.Op{hostfspkg.OpRead},
			Scope:    hostfspkg.ScopeExactFile,
			Reason:   "test",
		}}}),
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_list", "host.fs.list", root, nil))
	if resp.ExitCode != 0 {
		t.Fatalf("expected HostFS list allow, got %+v", resp)
	}
	entries, ok := resp.Data["entries"].([]map[string]any)
	if !ok {
		t.Fatalf("list response missing entries: %+v", resp.Data)
	}
	if len(entries) != 1 || entries[0]["name"] != "visible.txt" {
		t.Fatalf("list leaked sibling or missed granted file: %+v", entries)
	}
}

func TestHandleHostFSRejectsBadToken(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "visible.txt")
	if err := os.WriteFile(path, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostFS: testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
			HostPath: path,
			Ops:      []hostfspkg.Op{hostfspkg.OpRead},
			Scope:    hostfspkg.ScopeExactFile,
			Reason:   "test",
		}}}),
	}
	req := hostFSRequest("req_bad_token", "host.fs.read", path, nil)
	req.CapabilityToken = "cap_bad"
	resp := server.Handle(context.Background(), req)
	if resp.ExitCode == 0 || resp.Stderr != "broker authorization failed" {
		t.Fatalf("expected bad token to fail, got %+v", resp)
	}
}

func TestHandleHostFSWriteIsUnsupported(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostFS:    testHostFSService(t, hostfspkg.Config{}),
		Audit:     writer,
	}
	path := filepath.Join(t.TempDir(), "file.txt")
	resp := server.Handle(context.Background(), hostFSRequest("req_write", "host.fs.write", path, nil))
	if resp.Status != "denied" || resp.Stderr != "hostfs operation unsupported" {
		t.Fatalf("expected write to be unsupported, got %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.fs.write"`,
		`"decision":"deny"`,
		auditPathFragment(t, path),
		`"op":"write"`,
		`"policyEffect":"unsupported"`,
		`"policyReason":"unsupported"`,
		`"hostfs operation unsupported"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("HostFS write audit missing %q: %s", want, auditData)
		}
	}
}

func TestHandleHostFSWriteStagesWithoutHostMutation(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostFS: testHostFSOverlayService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
			ID:       "hfs_overlay",
			HostPath: path,
			Ops:      []hostfspkg.Op{hostfspkg.OpWrite},
			Overlay:  true,
			Scope:    hostfspkg.ScopeExactFile,
			Reason:   "operator write",
		}}}, filepath.Join(root, ".overlay")),
		Audit: writer,
	}
	resp := server.Handle(context.Background(), hostFSRequest("req_write_stage", "host.fs.write.replace", path, map[string]any{"dataBase64": base64.StdEncoding.EncodeToString([]byte("staged"))}))
	if resp.Status != "ok" || resp.ExitCode != 0 {
		t.Fatalf("expected staged write ok, got %+v", resp)
	}
	if resp.Data["staged"] != true || resp.Data["hostChanged"] != false || resp.Data["operationId"] == "" || resp.Data["decisionId"] == "" {
		t.Fatalf("staged response missing evidence fields: %+v", resp.Data)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "lower" {
		t.Fatalf("lower host file changed: %q err=%v", got, err)
	}
	result, err := server.HostFS.Read(path, 0, 0)
	if err != nil {
		t.Fatalf("overlay read: %v", err)
	}
	if got := hostfspkg.ReadResultDataString(result); got != "staged" {
		t.Fatalf("overlay read=%q want staged", got)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.fs.write.replace"`,
		`"action":"host.fs.overlay.stage"`,
		`"action":"host.fs.overlay.pending"`,
		`"decision":"allow"`,
		`"staged":true`,
		`"hostChanged":false`,
		`"operationId":"hfwop_`,
		`"decisionId":"hfwdec_`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("HostFS write audit missing %q: %s", want, auditData)
		}
	}
	if strings.Contains(string(auditData), base64.StdEncoding.EncodeToString([]byte("staged"))) {
		t.Fatalf("HostFS write audit leaked raw data payload: %s", auditData)
	}
}

func TestHandleHostFSWriteEnvelopeValidation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostFS:    testHostFSService(t, hostfspkg.Config{}),
	}
	for _, tt := range []struct {
		name   string
		action string
		extra  map[string]any
		want   string
	}{
		{name: "unknown arg", action: "host.fs.write.replace", extra: map[string]any{"dataBase64": "ZA==", "surprise": true}, want: "args.surprise"},
		{name: "missing content", action: "host.fs.write.create", extra: nil, want: "dataBase64 is required"},
		{name: "rename missing destination", action: "host.fs.write.rename", extra: nil, want: "destinationPath is required"},
		{name: "bad base64", action: "host.fs.write.replace", extra: map[string]any{"dataBase64": "not base64!"}, want: "valid base64"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := server.Handle(context.Background(), hostFSRequest("req_bad_"+tt.name, tt.action, path, tt.extra))
			if resp.Status != "bad-request" || !strings.Contains(resp.Stderr, tt.want) {
				t.Fatalf("response=%+v want bad-request containing %q", resp, tt.want)
			}
		})
	}
}

func TestHandleAuditsIsolatedBrowserProfileForURLOpen(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	browserProfile := filepath.Join(t.TempDir(), "identity", "browser")
	opener := &browserRecordingOpener{browserProfile: browserProfile}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Commands:  []string{"open"},
		Evaluator: publicHostEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	if len(opener.urls) != 1 || opener.urls[0] != "https://example.com" {
		t.Fatalf("opener did not see URL: %+v", opener.urls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	if event.Details["resourceType"] != "url" {
		t.Fatalf("audit missing URL resource type: %+v", event.Details)
	}
	if event.Details["browserProfileMode"] != "isolated" {
		t.Fatalf("audit missing isolated browser profile mode: %+v", event.Details)
	}
	if event.Details["browserProfile"] != "present" {
		t.Fatalf("audit browserProfile=%v want presence marker", event.Details["browserProfile"])
	}
	if event.Details["portBridge"] != "none" ||
		event.Details["browserControl"] != "disabled" ||
		event.Details["remoteDebugging"] != "not-exposed" {
		t.Fatalf("audit does not prove host.open avoided browser control channels: %+v", event.Details)
	}
	if strings.Contains(string(data), browserProfile) {
		t.Fatalf("audit leaked browser profile path: %s", data)
	}
}

func TestHandleRejectsLocalBrowserURLBeforeHostOpen(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_local_url",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "http://127.0.0.1:3000"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "http://127.0.0.1:3000"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "profile policy") {
		t.Fatalf("expected local browser URL denial, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("local browser URL should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"host.open"`, `"decision":"deny"`, `"target":"http://127.0.0.1:3000"`, "profile policy"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestHandleFailsClosedWhenHostOpenerIsMissing(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Commands:  []string{"open", "xdg-open"},
		Evaluator: publicHostEvaluator(profile.Default("test")),
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_no_opener",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status != "error" || resp.ExitCode != 1 || !strings.Contains(resp.Stderr, "host opener is not configured") {
		t.Fatalf("expected missing opener to fail closed, got %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	validateAuditJSONLWithSchema(t, auditPath)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"host.open"`, `"decision":"deny"`, `"status":"error"`, `"target":"https://example.com"`, `"host opener is not configured"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestHandleRedactsHostOpenerFailureDetails(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	hostRoot := t.TempDir()
	target := filepath.Join(hostRoot, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener: failingOpener{
			fileErr: errors.New("open " + target + ": permission denied"),
		},
		Audit: writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_open_fail",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/src/main.go"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/src/main.go"},
	})
	if resp.Status != "error" || resp.ExitCode != 1 || resp.Stderr != "host workspace file opener failed" {
		t.Fatalf("expected sanitized opener failure, got %+v", resp)
	}
	if strings.Contains(resp.Stderr, hostRoot) || strings.Contains(resp.Stderr, target) {
		t.Fatalf("response leaked host path: %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), hostRoot) || strings.Contains(string(data), target) || strings.Contains(string(data), "hostPath") {
		t.Fatalf("audit leaked host opener failure details: %s", data)
	}
	for _, want := range []string{`"target":"/workspace/src/main.go"`, `"resourceType":"workspace-file"`, `"host workspace file opener failed"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestBrokerEnvelopeSchemaValidatesRequestAndResponse(t *testing.T) {
	schema := compileBrokerEnvelopeSchema(t)
	req := Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com", "cwd": "/workspace"},
	}
	if err := validateBrokerEnvelope(schema, req); err != nil {
		t.Fatalf("valid broker request should match schema: %v", err)
	}
	resp := Response{ID: "req_1", Decision: "allow", Status: "ok", ExitCode: 0}
	if err := validateBrokerEnvelope(schema, resp); err != nil {
		t.Fatalf("valid broker response should match schema: %v", err)
	}
	hostFSReq := Request{
		ID:              "req_hostfs",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "hostfs:daemon",
		Route:           "host-broker",
		Action:          "host.fs.read",
		Args:            map[string]any{"path": "/Users/alice/Downloads/file.txt", "offset": 0, "size": 4096},
	}
	if err := validateBrokerEnvelope(schema, hostFSReq); err != nil {
		t.Fatalf("valid HostFS broker request should match schema: %v", err)
	}
	hostFSResp := Response{ID: "req_hostfs", Decision: "allow", Status: "ok", ExitCode: 0, Data: map[string]any{"dataBase64": "aGk=", "bytes": 2}}
	if err := validateBrokerEnvelope(schema, hostFSResp); err != nil {
		t.Fatalf("valid HostFS broker response should match schema: %v", err)
	}
	badRequest := Response{ID: "req_1", Decision: "deny", Status: "bad-request", ExitCode: 2, Stderr: "broker request args.target is required"}
	if err := validateBrokerEnvelope(schema, badRequest); err != nil {
		t.Fatalf("valid bad-request broker response should match schema: %v", err)
	}
	for name, resp := range map[string]Response{
		"ok with deny": {
			ID:       "req_1",
			Decision: "deny",
			Status:   "ok",
			ExitCode: 0,
		},
		"denied without stderr": {
			ID:       "req_1",
			Decision: "deny",
			Status:   "denied",
			ExitCode: 126,
		},
		"bad request wrong exit": {
			ID:       "req_1",
			Decision: "deny",
			Status:   "bad-request",
			ExitCode: 126,
			Stderr:   "bad request",
		},
		"error without stderr": {
			ID:       "req_1",
			Decision: "deny",
			Status:   "error",
			ExitCode: 1,
		},
		"ask decision": {
			ID:       "req_1",
			Decision: "ask",
			Status:   "denied",
			ExitCode: 126,
			Stderr:   "ask requires a prompt channel",
		},
		"audit-only decision": {
			ID:       "req_1",
			Decision: "audit-only",
			Status:   "denied",
			ExitCode: 126,
			Stderr:   "audit-only is not a broker response decision",
		},
		"error decision": {
			ID:       "req_1",
			Decision: "error",
			Status:   "error",
			ExitCode: 1,
			Stderr:   "opener failed",
		},
	} {
		if err := validateBrokerEnvelope(schema, resp); err == nil {
			t.Fatalf("expected schema to reject response %s", name)
		}
	}
	for name, req := range map[string]Request{
		"missing subject": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"missing command": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"empty argv": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"missing argv target": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"extra argv": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com", "--token", "secret"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"subject command mismatch": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "xdg-open",
			Argv:            []string{"xdg-open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"argv command mismatch": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"xdg-open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"missing route": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"bad route": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "guest-direct",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"missing target": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{},
		},
		"blank target": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "   "},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "   "},
		},
		"unsupported args field": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "stdin": "payload"},
		},
		"empty cwd": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "cwd": ""},
		},
		"relative cwd": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "cwd": "workspace"},
		},
		"url cwd": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "cwd": "https://example.com"},
		},
		"network-path cwd": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "cwd": "//host/share"},
		},
		"hostfs missing subject": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.fs.read",
			Args:            map[string]any{"path": "/Users/alice/Downloads/file.txt"},
		},
		"hostfs command metadata": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "hostfs:daemon",
			Command:         "open",
			Route:           "host-broker",
			Action:          "host.fs.read",
			Args:            map[string]any{"path": "/Users/alice/Downloads/file.txt"},
		},
		"hostfs stat offset": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "hostfs:daemon",
			Route:           "host-broker",
			Action:          "host.fs.stat",
			Args:            map[string]any{"path": "/Users/alice/Downloads/file.txt", "offset": 1},
		},
	} {
		if err := validateBrokerEnvelope(schema, req); err == nil {
			t.Fatalf("expected schema to reject %s", name)
		}
	}
}

func TestHandleRejectsCommandProxyWithoutHostBrokerRoute(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	for _, req := range []Request{
		{
			ID:              "req_missing_route",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		{
			ID:              "req_bad_route",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:open",
			Command:         "open",
			Argv:            []string{"open", "https://example.com"},
			Route:           "guest-direct",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
	} {
		resp := server.Handle(context.Background(), req)
		if resp.ExitCode == 0 {
			t.Fatalf("expected route validation to fail for %+v; resp=%+v", req, resp)
		}
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("bad route should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
}

func TestHandleRejectsUnsupportedCommandProxyPayload(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com", "stdin": "SECRET_STDIN_PAYLOAD"},
	})
	if resp.Status != "bad-request" || resp.ExitCode != 2 || !strings.Contains(resp.Stderr, "args.stdin is not supported") {
		t.Fatalf("expected unsupported payload to fail closed, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("unsupported payload should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"deny"`,
		`"status":"bad-request"`,
		`"error":"broker request args.stdin is not supported"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("audit missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "SECRET_STDIN_PAYLOAD") || strings.Contains(text, `"stdin"`) {
		t.Fatalf("unsupported stdin payload leaked into audit: %s", text)
	}
}

func TestHandleRejectsMalformedHTTPURL(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https:example.com"},
	})
	if resp.ExitCode == 0 {
		t.Fatalf("expected malformed URL to fail: %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("malformed URL should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
}

func TestHandleRejectsMalformedBrokerEnvelope(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	for name, req := range map[string]Request{
		"missing id": {
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"missing target": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{},
		},
		"bad cwd type": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "cwd": 42},
		},
		"unsupported args field": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com", "stdin": "payload"},
		},
		"bad subject": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Subject:         "command:shell",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
		"bad command": {
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Command:         "shell",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			resp := server.Handle(context.Background(), req)
			if resp.Status != "bad-request" || resp.ExitCode != 2 {
				t.Fatalf("expected bad request, got %+v", resp)
			}
			if len(opener.urls) != 0 || len(opener.files) != 0 {
				t.Fatalf("malformed request should not open host resources: urls=%v files=%v", opener.urls, opener.files)
			}
		})
	}
}

func TestHandleRejectsInvalidCWDBeforeScripts(t *testing.T) {
	for name, tc := range map[string]struct {
		cwd     string
		message string
	}{
		"empty cwd":     {cwd: "", message: "args.cwd is empty"},
		"relative cwd":  {cwd: "workspace", message: "absolute guest path"},
		"url cwd":       {cwd: "https://example.com", message: "guest workspace path"},
		"workspace out": {cwd: "/private", message: "outside workspace"},
	} {
		t.Run(name, func(t *testing.T) {
			opener := &recordingOpener{}
			server := Server{
				SessionID:  "ses_1",
				Token:      "cap_good",
				ProfileDir: t.TempDir(),
				HostRoot:   t.TempDir(),
				GuestRoot:  "/workspace",
				Commands:   []string{"open"},
				Evaluator:  policy.NewEvaluator(profile.Default("test")),
				Opener:     opener,
				ScriptRefs: []profile.ScriptRef{{
					ID:          "must-not-run",
					Path:        "policy/missing.js",
					Entrypoints: []string{"decideCommand"},
				}},
			}
			resp := server.Handle(context.Background(), Request{
				ID:              "req_1",
				SessionID:       "ses_1",
				CapabilityToken: "cap_good",
				Subject:         "command:open",
				Command:         "open",
				Argv:            []string{"open", "https://example.com"},
				Route:           "host-broker",
				Action:          "host.open",
				Args:            map[string]any{"target": "https://example.com", "cwd": tc.cwd},
			})
			if resp.Status != "bad-request" || resp.ExitCode != 2 {
				t.Fatalf("expected bad request, got %+v", resp)
			}
			if !strings.Contains(resp.Stderr, "args.cwd") || !strings.Contains(resp.Stderr, tc.message) {
				t.Fatalf("expected cwd validation error containing %q, got %+v", tc.message, resp)
			}
			if strings.Contains(resp.Stderr, "policy script") {
				t.Fatalf("cwd validation should happen before scripts, got %+v", resp)
			}
			if len(opener.urls) != 0 || len(opener.files) != 0 {
				t.Fatalf("invalid cwd should not open host resources: urls=%v files=%v", opener.urls, opener.files)
			}
		})
	}
}

func TestNormalizeBrokerRequestCWDMapsNativeHostWorkspaceToGuestAlias(t *testing.T) {
	hostRoot := t.TempDir()
	hostCWD := filepath.Join(hostRoot, "src", "pkg")
	if err := os.MkdirAll(hostCWD, 0o700); err != nil {
		t.Fatal(err)
	}
	server := Server{Backend: "native", HostRoot: hostRoot, GuestRoot: "/workspace"}
	req := Request{Args: map[string]any{"cwd": hostCWD}}
	if err := server.normalizeBrokerRequestCWD(&req); err != nil {
		t.Fatal(err)
	}
	if got, want := req.Args["cwd"], "/workspace/src/pkg"; got != want {
		t.Fatalf("normalized cwd=%v want %q", got, want)
	}
}

func TestNormalizeBrokerRequestCWDDoesNotAcceptHostWorkspaceForGuestBackend(t *testing.T) {
	hostRoot := t.TempDir()
	server := Server{Backend: "lima", HostRoot: hostRoot, GuestRoot: "/workspace"}
	req := Request{Args: map[string]any{"cwd": hostRoot}}
	if err := server.normalizeBrokerRequestCWD(&req); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected guest backend to reject host cwd, got %v", err)
	}
}

func TestHandleRejectsInvalidCWDWithoutAuditingRawPath(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	sensitiveCWD := "/Users/alice/private"
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  t.TempDir(),
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    &recordingOpener{},
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com", "cwd": sensitiveCWD},
	})
	if resp.Status != "bad-request" || resp.ExitCode != 2 {
		t.Fatalf("expected bad request, got %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), sensitiveCWD) {
		t.Fatalf("audit leaked raw cwd: %s", data)
	}
	if !strings.Contains(string(data), `"error":"broker request args.cwd is outside workspace"`) {
		t.Fatalf("audit missing sanitized cwd error: %s", data)
	}
}

func TestHandleUnsupportedActionAuditUsesBrokerRequest(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_unsupported",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.exec",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "unsupported broker action") {
		t.Fatalf("expected unsupported action denial, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("unsupported action should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	validateAuditJSONLWithSchema(t, auditPath)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"broker.request"`, `"requestedAction":"host.exec"`, `"decision":"deny"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
	if strings.Contains(string(data), `"action":"host.exec"`) {
		t.Fatalf("audit used unsupported action as event action: %s", data)
	}
}

func TestHandleRejectsCommandProxyWithoutRegisteredCommands(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status != "bad-request" || resp.ExitCode != 2 || !strings.Contains(resp.Stderr, "command registry is empty") {
		t.Fatalf("expected empty registry to fail closed, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("unregistered command should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
}

func TestHandleAcceptsRegisteredCustomCommandProxy(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Commands:  []string{"open", "browser-open"},
		Evaluator: publicHostEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:browser-open",
		Command:         "browser-open",
		Argv:            []string{"browser-open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode != 0 || resp.Decision != "allow" {
		t.Fatalf("expected registered custom command to be allowed, got %+v", resp)
	}
	if len(opener.urls) != 1 || opener.urls[0] != "https://example.com" {
		t.Fatalf("registered custom command did not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
}

func TestHandleRejectsMissingCommandMetadataWhenRegistryConfigured(t *testing.T) {
	for name, tc := range map[string]struct {
		req     Request
		message string
	}{
		"missing subject and command": {
			req: Request{
				ID:              "req_1",
				SessionID:       "ses_1",
				CapabilityToken: "cap_good",
				Route:           "host-broker",
				Action:          "host.open",
				Args:            map[string]any{"target": "https://example.com"},
			},
			message: "command metadata is required",
		},
		"missing subject": {
			req: Request{
				ID:              "req_1",
				SessionID:       "ses_1",
				CapabilityToken: "cap_good",
				Command:         "open",
				Argv:            []string{"open", "https://example.com"},
				Route:           "host-broker",
				Action:          "host.open",
				Args:            map[string]any{"target": "https://example.com"},
			},
			message: "command metadata is required",
		},
		"missing command": {
			req: Request{
				ID:              "req_1",
				SessionID:       "ses_1",
				CapabilityToken: "cap_good",
				Subject:         "command:open",
				Argv:            []string{"open", "https://example.com"},
				Route:           "host-broker",
				Action:          "host.open",
				Args:            map[string]any{"target": "https://example.com"},
			},
			message: "command metadata is required",
		},
		"missing argv": {
			req: Request{
				ID:              "req_1",
				SessionID:       "ses_1",
				CapabilityToken: "cap_good",
				Subject:         "command:open",
				Command:         "open",
				Route:           "host-broker",
				Action:          "host.open",
				Args:            map[string]any{"target": "https://example.com"},
			},
			message: "argv is required",
		},
		"argv command mismatch": {
			req: Request{
				ID:              "req_1",
				SessionID:       "ses_1",
				CapabilityToken: "cap_good",
				Subject:         "command:open",
				Command:         "open",
				Argv:            []string{"xdg-open", "https://example.com"},
				Route:           "host-broker",
				Action:          "host.open",
				Args:            map[string]any{"target": "https://example.com"},
			},
			message: "does not match command",
		},
	} {
		t.Run(name, func(t *testing.T) {
			opener := &recordingOpener{}
			server := Server{
				SessionID: "ses_1",
				Token:     "cap_good",
				Commands:  []string{"open", "xdg-open"},
				Evaluator: policy.NewEvaluator(profile.Default("test")),
				Opener:    opener,
			}
			resp := server.Handle(context.Background(), tc.req)
			if resp.Status != "bad-request" || resp.ExitCode != 2 || !strings.Contains(resp.Stderr, tc.message) {
				t.Fatalf("expected %s to fail, got %+v", tc.message, resp)
			}
			if len(opener.urls) != 0 || len(opener.files) != 0 {
				t.Fatalf("metadata-free request should not reach opener: urls=%v files=%v", opener.urls, opener.files)
			}
		})
	}
}

func TestHandleRejectsCommandDisabledByProfile(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:xdg-open",
		Command:         "xdg-open",
		Argv:            []string{"xdg-open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status != "bad-request" || resp.ExitCode != 2 || !strings.Contains(resp.Stderr, "not enabled by profile") {
		t.Fatalf("expected disabled command to fail, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("disabled command should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
}

func TestHandleRejectsCommandSubjectMismatch(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Commands:  []string{"open", "xdg-open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "xdg-open",
		Argv:            []string{"xdg-open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status != "bad-request" || resp.ExitCode != 2 || !strings.Contains(resp.Stderr, "does not match subject") {
		t.Fatalf("expected mismatched command metadata to fail, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("mismatched command should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
}

func TestHandleRejectsCustomAndOutsideFileURLSchemes(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  t.TempDir(),
		GuestRoot: "/workspace",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	for _, target := range []string{
		"vscode://file/workspace/src/main.go",
		"file:///etc/passwd",
		"file://remote-host/workspace/src/main.go",
		"file:///workspace/src/main.go?download=1",
		"file:///workspace/src/main.go#fragment",
	} {
		resp := server.Handle(context.Background(), Request{
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": target},
		})
		if resp.ExitCode == 0 {
			t.Fatalf("expected %s to fail: %+v", target, resp)
		}
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("custom/outside file URL should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
}

func TestHandleRejectsEncodedFileURLPathSeparators(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostRoot, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "secret.txt"), []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	for _, target := range []string{
		"file:///workspace/src%2f..%2fsecret.txt",
		"file:///workspace/src%5cmain.go",
	} {
		resp := server.Handle(context.Background(), Request{
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": target},
		})
		if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "encoded path separators") {
			t.Fatalf("expected encoded file URL separator denial for %s, got %+v", target, resp)
		}
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("encoded file URL separator should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
}

func TestHandleMapsWorkspaceFileURL(t *testing.T) {
	hostRoot := t.TempDir()
	target := filepath.Join(hostRoot, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, targetURL := range []string{
		"file:///workspace/src/main.go",
		"file://localhost/workspace/src/main.go",
	} {
		opener := &recordingOpener{}
		server := Server{
			SessionID: "ses_1",
			Token:     "cap_good",
			HostRoot:  hostRoot,
			GuestRoot: "/workspace",
			Evaluator: policy.NewEvaluator(profile.Default("test")),
			Opener:    opener,
		}
		resp := server.Handle(context.Background(), Request{
			ID:              "req_1",
			SessionID:       "ses_1",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": targetURL},
		})
		if resp.ExitCode != 0 {
			t.Fatalf("expected workspace file URL allow for %s, got %+v", targetURL, resp)
		}
		if len(opener.files) != 1 || opener.files[0] != want {
			t.Fatalf("mapped file URL mismatch for %s: got %+v want %s", targetURL, opener.files, want)
		}
	}
}

func TestHandleMapsWorkspaceFile(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	guestRoot := "/workspace"
	target := filepath.Join(hostRoot, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: guestRoot,
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/src/main.go"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/src/main.go"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(opener.files) != 1 || opener.files[0] != want {
		t.Fatalf("mapped file mismatch: got %+v want %s", opener.files, want)
	}
}

func TestHandleRejectsMissingMappedWorkspaceFile(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_missing_file",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/src/missing.go"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/src/missing.go"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "does not exist") {
		t.Fatalf("expected missing workspace file denial, got %+v", resp)
	}
	if strings.Contains(resp.Stderr, hostRoot) {
		t.Fatalf("missing file response leaked host workspace path: %+v", resp)
	}
	if len(opener.files) != 0 || len(opener.urls) != 0 {
		t.Fatalf("missing workspace file should not reach opener: files=%v urls=%v", opener.files, opener.urls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), hostRoot) {
		t.Fatalf("missing file audit leaked host workspace path: %s", data)
	}
}

func TestHandleRejectsOpenArgvThatDoesNotMatchPayload(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  t.TempDir(),
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_bad_argv",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/src/main.go", "--token", "abc123"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/src/main.go"},
	})
	if resp.Status != "bad-request" || resp.ExitCode != 2 || !strings.Contains(resp.Stderr, "argv for open must be [command target]") {
		t.Fatalf("expected inconsistent argv to fail closed, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("bad argv should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	// Rejected argv is recorded verbatim: it is user data in host-local
	// evidence, and Core does not guess which flag values are secrets.
	for _, want := range []string{`"status":"bad-request"`, `"decision":"deny"`, `"--token"`, `"abc123"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestHandleRespectsWorkspaceFileCapabilityFlag(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	target := filepath.Join(hostRoot, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := profile.Default("test")
	p.HostCapabilities.Open.AllowWorkspaceFiles = false
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(p),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/src/main.go"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/src/main.go"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "workspace file open is disabled") {
		t.Fatalf("expected workspace file capability denial, got %+v", resp)
	}
	if len(opener.files) != 0 || len(opener.urls) != 0 {
		t.Fatalf("disabled workspace file open should not reach opener: files=%v urls=%v", opener.files, opener.urls)
	}
}

func TestHandleMapsWorkspaceFileWhenWorkspaceRootIsSymlink(t *testing.T) {
	opener := &recordingOpener{}
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	linkRoot := filepath.Join(parent, "link")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realRoot, "doc.txt")
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  linkRoot,
		GuestRoot: linkRoot,
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", target},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": target},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected symlink-equivalent workspace file allow, got %+v", resp)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(opener.files) != 1 || opener.files[0] != want {
		t.Fatalf("mapped symlink workspace file mismatch: got %+v want %s", opener.files, want)
	}
}

func TestHandleRejectsCommandProxyWorkspaceSymlinkEscape(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(hostRoot, "outside-link")); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/outside-link"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/outside-link"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "resolves outside workspace") {
		t.Fatalf("expected symlink escape denial, got %+v", resp)
	}
	if strings.Contains(resp.Stderr, hostRoot) || strings.Contains(resp.Stderr, outside) {
		t.Fatalf("symlink escape response leaked host path: %+v", resp)
	}
	if len(opener.files) != 0 || len(opener.urls) != 0 {
		t.Fatalf("symlink escape should not reach opener: files=%v urls=%v", opener.files, opener.urls)
	}
}

func TestHandleRejectsWorkspaceSpecialFile(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot, err := os.MkdirTemp("/tmp", "hideout-special-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(hostRoot)
	socketPath := filepath.Join(hostRoot, "debug.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/debug.sock"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/debug.sock"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "not a regular file or directory") {
		t.Fatalf("expected special file denial, got %+v", resp)
	}
	if strings.Contains(resp.Stderr, hostRoot) || strings.Contains(resp.Stderr, socketPath) {
		t.Fatalf("special file response leaked host path: %+v", resp)
	}
	if len(opener.files) != 0 || len(opener.urls) != 0 {
		t.Fatalf("special file should not reach opener: files=%v urls=%v", opener.files, opener.urls)
	}
}

func TestHandleNormalizesAndMapsWorkspacePathBeforeCommandPolicyScript(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	target := filepath.Join(hostRoot, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "path.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  if (ctx.command.target !== "/workspace/src/main.go") {
    return hideout.decision.deny({ action: "host.open", resources: ["workspace-file"], reason: "target was not normalized: " + ctx.command.target });
  }
  if (ctx.command.cwd !== "/workspace" || ctx.command.action !== "host.open" || ctx.command.route !== "host-broker" || ctx.command.resourceType !== "workspace-file") {
    return hideout.decision.deny({ action: "host.open", resources: ["workspace-file"], reason: "command context was not canonical" });
  }
  return hideout.decision.allow({ route: "host-broker", action: "host.open", resources: ["workspace-file"], reason: "normalized workspace target" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		HostRoot:   hostRoot,
		GuestRoot:  "/workspace",
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "path-policy",
			Path:        "policy/path.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "src/../src/main.go"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "src/../src/main.go", "cwd": "/workspace"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected normalized workspace target allow, got %+v", resp)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(opener.files) != 1 || opener.files[0] != want {
		t.Fatalf("mapped file mismatch: got %+v want %s", opener.files, want)
	}
}

func TestHandleAllowsWorkspaceFileBeginningWithDots(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostRoot, "..notes"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/..notes"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	want := filepath.Join(hostRoot, "..notes")
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	if len(opener.files) != 1 || opener.files[0] != want {
		t.Fatalf("mapped file mismatch: got %+v want %s", opener.files, want)
	}
}

func TestHandleRejectsWorkspaceParentTraversal(t *testing.T) {
	opener := &recordingOpener{}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  t.TempDir(),
		GuestRoot: "/workspace",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/../secret.txt"},
	})
	if resp.ExitCode == 0 {
		t.Fatalf("expected parent traversal denial, got %+v", resp)
	}
	if len(opener.files) != 0 {
		t.Fatalf("parent traversal should not reach opener: %+v", opener.files)
	}
}

func TestHandleRejectsWorkspaceSymlinkEscape(t *testing.T) {
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(hostRoot, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/link"},
	})
	if resp.ExitCode == 0 {
		t.Fatalf("expected symlink escape to fail: %+v", resp)
	}
	if len(opener.files) != 0 {
		t.Fatalf("symlink escape should not reach opener: %+v", opener.files)
	}
}

func TestHandleAuditsResourceTypeAndMappedHostPath(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	opener := &recordingOpener{}
	hostRoot := t.TempDir()
	target := filepath.Join(hostRoot, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("workspace file"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		HostRoot:  hostRoot,
		GuestRoot: "/workspace",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "/workspace/src/main.go"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "/workspace/src/main.go"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	if event.Details["resourceType"] != "workspace-file" {
		t.Fatalf("audit missing resourceType: %+v", event.Details)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if event.Details["hostPath"] != want {
		t.Fatalf("audit mapped hostPath=%v want %s", event.Details["hostPath"], want)
	}
	if _, ok := event.Details["browserProfile"]; ok {
		t.Fatalf("workspace file audit should not include browserProfile: %+v", event.Details)
	}
	if _, ok := event.Details["browserProfileMode"]; ok {
		t.Fatalf("workspace file audit should not include browserProfileMode: %+v", event.Details)
	}
	if event.Details["subject"] != "command:open" || event.Details["command"] != "open" || event.Details["route"] != "host-broker" {
		t.Fatalf("audit missing command proxy metadata: %+v", event.Details)
	}
	argv, ok := event.Details["argv"].([]any)
	if !ok || len(argv) != 2 || argv[0] != "open" || argv[1] != "/workspace/src/main.go" {
		t.Fatalf("audit argv malformed: %+v", event.Details["argv"])
	}
}

func TestHandleRunsCommandPolicyScriptAndAuditsHash(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "deny.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("function decideCommand(ctx) { if (ctx.command.action !== 'host.open' || ctx.command.route !== 'host-broker' || ctx.command.resourceType !== 'url') { return hideout.decision.deny({ action: 'host.open', resources: ['url:https'], reason: 'command context was not canonical' }); } return hideout.decision.deny({ action: 'host.open', resources: ['url:https'], reason: 'script denied' }); }"), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "deny-open",
			Path:        "policy/deny.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "script denied") {
		t.Fatalf("expected script denial, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("script denial should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	scripts, ok := event.Details["policyScripts"].([]any)
	if !ok || len(scripts) != 1 {
		t.Fatalf("audit missing policyScripts: %+v", event.Details)
	}
	script, ok := scripts[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected policy script shape: %+v", scripts[0])
	}
	if script["id"] != "deny-open" || script["entrypoint"] != "decideCommand" || script["decision"] != "deny" {
		t.Fatalf("unexpected policy script audit: %+v", script)
	}
	if hash, _ := script["sha256"].(string); len(hash) != 64 {
		t.Fatalf("script hash missing from audit: %+v", script)
	}
}

func TestNewTokenFormatIsStrippedByAuditRedactor(t *testing.T) {
	// Couple the minted broker token format to the audit redactor so that a
	// change to NewToken() that no longer matches controlPlaneTokenRE fails
	// here instead of silently defeating redaction.
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	got := audit.RedactString("broker endpoint token " + token + " end")
	if strings.Contains(got, token) {
		t.Fatalf("minted broker token was not stripped by audit redactor: %q -> %q", token, got)
	}
	if got != "broker endpoint token REDACTED end" {
		t.Fatalf("unexpected redaction of minted token: %q", got)
	}
}

func TestCommandScriptContextArgvIsRaw(t *testing.T) {
	// The command.decide script context must expose argv verbatim, so a
	// regression that reinstates argv redaction fails here.
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "argv.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	// The script denies unless argv[1] carries the raw query value from the
	// target. Legacy heuristic argv redaction would have rewritten token=abc123.
	source := `function decideCommand(ctx) {
  var arg = ctx.command.argv[1] || "";
  if (arg.indexOf("token=abc123") < 0) {
    return hideout.decision.deny({ route: "deny", action: "host.open", resources: ["url:https"], reason: "argv was not raw: " + arg });
  }
  return hideout.decision.allow({ route: "host-broker", action: "host.open", resources: ["url:https"], reason: "raw argv available" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  publicHostEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      audit.NewDiscard(),
		ScriptRefs: []profile.ScriptRef{{
			ID:          "argv",
			Path:        "policy/argv.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	target := "https://example.com/?token=abc123"
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", target},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": target},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("script should have seen raw argv and allowed, got %+v", resp)
	}
}

func TestHandleGivesCommandPolicyScriptRawTargetAndAuditsVerbatim(t *testing.T) {
	// The command policy script receives the canonicalized target verbatim so
	// it can make real security decisions on it (here: deny URLs that carry
	// embedded credentials). User data is host-local evidence and is written
	// to the local audit file verbatim; only Hideout-minted control-plane
	// credentials are stripped.
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "raw-target.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  var u = hideout.url.parse(ctx.command.target);
  if (u.host.indexOf("@") >= 0) {
    return hideout.decision.deny({ route: "deny", action: "host.open", resources: ["url:https"], reason: "URL carries embedded credentials" });
  }
  if (ctx.command.target.indexOf("token=abc") < 0) {
    return hideout.decision.deny({ route: "deny", action: "host.open", resources: ["url:https"], reason: "script did not receive raw target: " + ctx.command.target });
  }
  return hideout.decision.allow({ route: "host-broker", action: "host.open", resources: ["url:https"], reason: "raw URL context available" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  publicHostEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "raw-target",
			Path:        "policy/raw-target.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	target := "https://example.com/path?token=abc&ok=1"
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", target},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": target},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow with raw script context, got %+v", resp)
	}
	if len(opener.urls) != 1 || opener.urls[0] != target {
		t.Fatalf("opener should receive original URL target: %+v", opener.urls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	// User query data is host-local evidence, recorded verbatim.
	if !strings.Contains(string(data), "token=abc") {
		t.Fatalf("local audit should preserve user URL data verbatim: %s", data)
	}
	if !strings.Contains(string(data), `"decision":"allow"`) {
		t.Fatalf("audit missing script allow metadata: %s", data)
	}
}

func TestHandleDeniesCommandPolicyScriptOnRawCredentialURL(t *testing.T) {
	// Companion to the test above: a URL that embeds credentials is now
	// visible to the script (raw), which can deny it. This is a policy win
	// that heuristic input redaction previously made impossible.
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "raw-target.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  if (ctx.command.target.indexOf("@") >= 0 && ctx.command.target.indexOf("://") >= 0 && ctx.command.target.indexOf("@", ctx.command.target.indexOf("://")) >= 0) {
    return hideout.decision.deny({ route: "deny", action: "host.open", resources: ["url:https"], reason: "URL carries embedded credentials" });
  }
  return hideout.decision.allow({ route: "host-broker", action: "host.open", resources: ["url:https"], reason: "ok" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      audit.NewDiscard(),
		ScriptRefs: []profile.ScriptRef{{
			ID:          "raw-target",
			Path:        "policy/raw-target.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	target := "https://user:pass@example.com/path?ok=1"
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", target},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": target},
	})
	if resp.Decision != string(policy.Deny) {
		t.Fatalf("script should deny credential-bearing URL using raw target, got %+v", resp)
	}
	if len(opener.urls) != 0 {
		t.Fatalf("denied URL must not reach opener: %+v", opener.urls)
	}
}

func TestHandleRejectsCommandPolicyScriptActionMismatch(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "mismatch.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  return hideout.decision.allow({ route: 'portbridge', action: 'endpoint.expose.host-to-guest', resources: ['candidate:manual_preview_1'], reason: 'try to mint endpoint exposure' });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "mismatch",
			Path:        "policy/mismatch.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "does not match request action") {
		t.Fatalf("expected script action mismatch denial, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("script mismatch should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"error":"script proposal action \"endpoint.expose.host-to-guest\" does not match request action \"host.open\""`) {
		t.Fatalf("audit missing script mismatch error: %s", data)
	}
}

func TestHandleRejectsCommandPolicyScriptRouteMismatch(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "mismatch.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  return hideout.decision.auditOnly({ route: 'fake', action: 'host.open', resources: ['url:https'], reason: 'wrong route' });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "mismatch",
			Path:        "policy/mismatch.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "host.open must use host-broker route unless denied") {
		t.Fatalf("expected script route denial, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("script mismatch should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"error":"host.open must use host-broker route unless denied"`) {
		t.Fatalf("audit missing script route error: %s", data)
	}
}

func TestHandleRejectsCommandPolicyScriptLabProbeAuthority(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "lab-probe.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  return hideout.decision.allow({ route: "lab-probe", action: "portbridge.probe", resources: ["portbridge:loopback"], reason: "try to mint probe authority" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "lab-probe",
			Path:        "policy/lab-probe.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, `unsupported action "portbridge.probe"`) {
		t.Fatalf("expected lab-probe script authority to fail closed, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("lab-probe script authority should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"deny"`,
		`"id":"lab-probe"`,
		`"error":"unsupported action \"portbridge.probe\""`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestHandleRejectsInvalidCommandPolicyScriptOutputShape(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "bad-shape.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  const proposal = hideout.decision.allow({ route: "host-broker", action: "host.open", resources: ["url:https"], reason: "looks valid" });
  proposal.hostExec = "please";
  return proposal;
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "bad-shape",
			Path:        "policy/bad-shape.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "unknown field") {
		t.Fatalf("expected invalid script output shape to fail closed, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("invalid script output should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"deny"`,
		`"id":"bad-shape"`,
		`"error":"json: unknown field \"hostExec\""`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestHandleCommandPolicyScriptAskFailsClosedWithoutPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "ask.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  return hideout.decision.ask({ route: 'host-broker', action: 'host.open', resources: ['url:https'], reason: 'needs prompt' });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "ask-open",
			Path:        "policy/ask.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "no prompt channel") {
		t.Fatalf("expected ask to fail closed without prompt channel, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("ask without prompt should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"decision":"deny"`) || !strings.Contains(string(data), `"decision":"ask"`) {
		t.Fatalf("audit should record denied request and ask script proposal: %s", data)
	}
}

func TestHandleCommandPolicyScriptAuditOnlyDoesNotGrantHostOpen(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "audit-only.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function decideCommand(ctx) {
  return hideout.decision.auditOnly({ route: 'host-broker', action: 'host.open', resources: ['url:https'], reason: 'observe only' });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	p := profile.Default("test")
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(p),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "audit-only-open",
			Path:        "policy/audit-only.js",
			Entrypoints: []string{"decideCommand"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "observe only") {
		t.Fatalf("expected audit-only script to stop URL open before builtin allow, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("audit-only script should not grant host side effect: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"decision":"deny"`) || !strings.Contains(string(data), `"decision":"audit-only"`) {
		t.Fatalf("audit should record denied request and audit-only script proposal: %s", data)
	}
}

func TestHandleRunsAuditRedactionScript(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "redact.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `
function redactAudit(ctx) {
  const details = ctx.details;
  details.target = "REDACTED_BY_REDACT_AUDIT";
  details.argv = ["open", "REDACTED_ARGV"];
  details.customRedaction = ctx.extra.status;
  return { details, reason: "redacted target and argv" };
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  publicHostEvaluator(profile.Default("test")),
		Opener:     &recordingOpener{},
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "redact-open",
			Path:        "policy/redact.js",
			Entrypoints: []string{"redactAudit"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com/private?ticket=abc"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com/private?ticket=abc"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "ticket=abc") {
		t.Fatalf("audit should be redacted by script: %s", data)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	if event.Details["target"] != "REDACTED_BY_REDACT_AUDIT" || event.Details["customRedaction"] != "ok" {
		t.Fatalf("audit details were not rewritten by script: %+v", event.Details)
	}
	scripts, ok := event.Details["policyScripts"].([]any)
	if !ok || len(scripts) != 1 {
		t.Fatalf("audit missing redaction script metadata: %+v", event.Details)
	}
	script, ok := scripts[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected policy script shape: %+v", scripts[0])
	}
	if script["id"] != "redact-open" || script["entrypoint"] != "redactAudit" || script["decision"] != "audit-only" {
		t.Fatalf("unexpected redaction script metadata: %+v", script)
	}
	if hash, _ := script["sha256"].(string); len(hash) != 64 {
		t.Fatalf("script hash missing from audit: %+v", script)
	}
}

func TestHandleAuditRedactionScriptsCannotForgePolicyScriptMetadata(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	policyDir := filepath.Join(profileDir, "policy")
	if err := os.MkdirAll(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(policyDir, "first.js")
	first := `
function redactAudit(ctx) {
  const details = ctx.details;
  details.policyScripts = [{ id: "forged", entrypoint: "redactAudit", sha256: "fake" }];
  details.firstRan = true;
  return { details, reason: "try to forge policy script metadata" };
}`
	if err := os.WriteFile(firstPath, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(policyDir, "second.js")
	second := `
function redactAudit(ctx) {
  const details = ctx.details;
  details.sawForgedPolicyScripts = Boolean(ctx.details.policyScripts);
  return { details, reason: "check reserved policy script metadata" };
}`
	if err := os.WriteFile(secondPath, []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  publicHostEvaluator(profile.Default("test")),
		Opener:     &recordingOpener{},
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{
			{
				ID:          "first-redact",
				Path:        "policy/first.js",
				Entrypoints: []string{"redactAudit"},
			},
			{
				ID:          "second-redact",
				Path:        "policy/second.js",
				Entrypoints: []string{"redactAudit"},
			},
		},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	if event.Details["sawForgedPolicyScripts"] != false {
		t.Fatalf("second redaction script saw forged policyScripts: %+v", event.Details)
	}
	scripts, ok := event.Details["policyScripts"].([]any)
	if !ok || len(scripts) != 2 {
		t.Fatalf("audit should contain only broker-owned redaction metadata: %+v", event.Details)
	}
	for _, item := range scripts {
		script, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected policy script metadata: %+v", item)
		}
		if script["id"] == "forged" || script["sha256"] == "fake" {
			t.Fatalf("forged policy script metadata reached audit: %+v", scripts)
		}
		if hash, _ := script["sha256"].(string); len(hash) != 64 {
			t.Fatalf("broker-owned script hash missing: %+v", script)
		}
	}
}

func TestHandleAuditRedactionScriptCannotRemoveBrokerMetadata(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "redact.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `
function redactAudit(ctx) {
  return { details: { target: "REDACTED" }, reason: "dropped metadata" };
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  publicHostEvaluator(profile.Default("test")),
		Opener:     &recordingOpener{},
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "redact-open",
			Path:        "policy/redact.js",
			Entrypoints: []string{"redactAudit"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com/private?ticket=abc"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com/private?ticket=abc"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	for key, want := range map[string]any{
		"requestId": "req_1",
		"subject":   "command:open",
		"command":   "open",
		"route":     "host-broker",
		"status":    "ok",
	} {
		if event.Details[key] != want {
			t.Fatalf("redaction script removed immutable audit metadata %s: %+v", key, event.Details)
		}
	}
	if event.Details["target"] != "REDACTED" {
		t.Fatalf("redaction script should still be able to redact target: %+v", event.Details)
	}
}

func TestHandleAuditRedactionScriptCannotRemoveRequestedAction(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "redact.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `
function redactAudit(ctx) {
  return { details: {}, reason: "drop everything" };
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		Backend:    "native",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     &recordingOpener{},
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "redact-open",
			Path:        "policy/redact.js",
			Entrypoints: []string{"redactAudit"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_unsupported",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.exec",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 {
		t.Fatalf("expected deny, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	if event.Action != "broker.request" {
		t.Fatalf("unsupported action should audit as broker.request: %+v", event)
	}
	for key, want := range map[string]any{
		"requestId":       "req_unsupported",
		"subject":         "command:open",
		"command":         "open",
		"route":           "host-broker",
		"requestedAction": "host.exec",
		"status":          "denied",
		"error":           "unsupported broker action",
	} {
		if event.Details[key] != want {
			t.Fatalf("redaction script removed immutable audit metadata %s: %+v", key, event.Details)
		}
	}
}

func TestHandleAuditRedactionScriptFailureFailsClosed(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "bad-redact.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("function redactAudit(ctx) { return { reason: 'missing details' }; }"), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Commands:   []string{"open"},
		Evaluator:  publicHostEvaluator(profile.Default("test")),
		Opener:     opener,
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "bad-redact",
			Path:        "policy/bad-redact.js",
			Entrypoints: []string{"redactAudit"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Stderr, "audit redaction script bad-redact") {
		t.Fatalf("expected redaction failure, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("redaction failure should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "auditRedactionStatus") || !strings.Contains(string(data), "failed-closed") {
		t.Fatalf("audit should record failed-closed redaction: %s", data)
	}
	if !strings.Contains(string(data), `"id":"bad-redact"`) || !strings.Contains(string(data), `"entrypoint":"redactAudit"`) || !strings.Contains(string(data), `"sha256":"`) {
		t.Fatalf("audit should record failed redaction script metadata: %s", data)
	}
}

func TestHandleRecordsUserURLVerbatimInLocalAudit(t *testing.T) {
	// User/application URL data is host-local evidence and is written to the
	// local audit file verbatim. Only Hideout-minted control-plane credentials
	// are stripped; Core does not guess at user secrets.
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	server := Server{
		SessionID: "ses_1",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    &recordingOpener{},
		Audit:     writer,
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://user:pass@example.com/path?token=abc&ok=1"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "user:pass") || !strings.Contains(string(data), "token=abc") {
		t.Fatalf("local audit should preserve user URL data verbatim: %s", data)
	}
}

func TestHandleGivesAuditRedactionScriptRawDetails(t *testing.T) {
	// The audit.redact script receives user data verbatim and may choose to
	// redact presentation fields itself. This is user-owned redaction, not
	// Core heuristic guessing.
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "policy", "redact.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `function redactAudit(ctx) {
  var out = ctx.details;
  out.sawRawTarget = String(ctx.details.target).indexOf("token=abc") >= 0;
  out.target = "REDACTED_BY_USER_POLICY";
  return { details: out, reason: "user policy redacted its own field" };
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	server := Server{
		SessionID:  "ses_1",
		Token:      "cap_good",
		Profile:    "test",
		ProfileDir: profileDir,
		Evaluator:  policy.NewEvaluator(profile.Default("test")),
		Opener:     &recordingOpener{},
		Audit:      writer,
		ScriptRefs: []profile.ScriptRef{{
			ID:          "redact",
			Path:        "policy/redact.js",
			Entrypoints: []string{"redactAudit"},
		}},
	}
	resp := server.Handle(context.Background(), Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://user:pass@example.com/path?token=abc&ok=1"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow, got %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	// Script saw the raw target (proving no input redaction).
	if !strings.Contains(string(data), `"sawRawTarget":true`) {
		t.Fatalf("redaction script did not receive raw target: %s", data)
	}
	// The user policy's own redaction is applied to the stored field.
	if !strings.Contains(string(data), "REDACTED_BY_USER_POLICY") {
		t.Fatalf("user-owned redaction was not applied: %s", data)
	}
	if strings.Contains(string(data), "token=abc") {
		t.Fatalf("user policy chose to redact target but raw value persisted: %s", data)
	}
}

func TestParseEndpointSupportsUnixAndTCP(t *testing.T) {
	tests := []struct {
		raw     string
		network string
		address string
	}{
		{raw: "/tmp/hideout.sock", network: EndpointUnix, address: "/tmp/hideout.sock"},
		{raw: "unix:///tmp/hideout.sock", network: EndpointUnix, address: "/tmp/hideout.sock"},
		{raw: "tcp://127.0.0.1:4444", network: EndpointTCP, address: "127.0.0.1:4444"},
	}
	for _, tt := range tests {
		got, err := ParseEndpoint(tt.raw)
		if err != nil {
			t.Fatalf("ParseEndpoint(%q): %v", tt.raw, err)
		}
		if got.Network != tt.network || got.Address != tt.address {
			t.Fatalf("ParseEndpoint(%q)=%+v, want network=%s address=%s", tt.raw, got, tt.network, tt.address)
		}
	}
}

func TestTCPClientOpenEndpoint(t *testing.T) {
	opener := &recordingOpener{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := &Server{
		SessionID: "ses_tcp",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	if err := server.StartEndpoint(ctx, TCPEndpoint("127.0.0.1:0")); err != nil {
		t.Fatalf("start tcp broker: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reqCancel()
	resp := ClientOpenEndpoint(reqCtx, server.Endpoint, Request{
		ID:              "req_tcp",
		SessionID:       "ses_tcp",
		CapabilityToken: "cap_good",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("expected allow over tcp endpoint, got %+v", resp)
	}
	if len(opener.urls) != 1 || opener.urls[0] != "https://example.com" {
		t.Fatalf("opener did not see URL: %+v", opener.urls)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if got := server.TransportObservation(); got.Accepted != 1 || got.RequestParsed != 1 ||
		got.RequestParseFailed != 0 || got.ResponseWritten != 1 || got.ResponseWriteFailed != 0 {
		t.Fatalf("transport observation=%+v", got)
	}
}

func TestServerCloseWaitsForInFlightHandlers(t *testing.T) {
	opener := blockingOpener{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := &Server{
		SessionID: "ses_tcp",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     audit.NewDiscard(),
	}
	if err := server.StartEndpoint(ctx, TCPEndpoint("127.0.0.1:0")); err != nil {
		t.Fatalf("start tcp broker: %v", err)
	}
	responseDone := make(chan Response, 1)
	go func() {
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer reqCancel()
		responseDone <- ClientOpenEndpoint(reqCtx, server.Endpoint, Request{
			ID:              "req_close_wait",
			SessionID:       "ses_tcp",
			CapabilityToken: "cap_good",
			Route:           "host-broker",
			Action:          "host.open",
			Args:            map[string]any{"target": "https://example.com"},
		})
	}()
	select {
	case <-opener.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not enter opener")
	}
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- server.Close()
	}()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before in-flight handler completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(opener.release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after handler completed")
	}
	select {
	case resp := <-responseDone:
		if resp.ExitCode != 0 {
			t.Fatalf("expected in-flight request to finish before close, got %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive response")
	}
}

func TestTCPClientBadTokenFailsClosedAndDoesNotLeakToken(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	opener := &recordingOpener{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := &Server{
		SessionID: "ses_tcp",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
		Audit:     writer,
	}
	if err := server.StartEndpoint(ctx, TCPEndpoint("127.0.0.1:0")); err != nil {
		t.Fatalf("start tcp broker: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reqCancel()
	resp := ClientOpenEndpoint(reqCtx, server.Endpoint, Request{
		ID:              "req_tcp_bad_token",
		SessionID:       "ses_tcp",
		CapabilityToken: "cap_bad",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.ExitCode == 0 || resp.Stderr != "broker authorization failed" {
		t.Fatalf("expected TCP bad token request to fail closed, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("TCP bad token request should not reach opener: urls=%+v files=%+v", opener.urls, opener.files)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	validateAuditJSONLWithSchema(t, auditPath)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"deny"`,
		`"error":"broker authorization failed"`,
		`"requestId":"req_tcp_bad_token"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("audit missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "cap_good") || strings.Contains(text, "cap_bad") || strings.Contains(text, "capabilityToken") {
		t.Fatalf("TCP authorization audit leaked capability token material: %s", text)
	}
}

func TestClientOpenEndpointHonorsContextDeadlineWaitingForResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		<-release
	}()
	reqCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	resp := ClientOpenEndpoint(reqCtx, TCPEndpoint(ln.Addr().String()), Request{
		ID:              "req_timeout",
		SessionID:       "ses_timeout",
		CapabilityToken: "cap_timeout",
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	close(release)
	<-done
	if resp.Status != "broker-unavailable" || resp.ExitCode != 69 {
		t.Fatalf("expected broker-unavailable timeout response, got %+v", resp)
	}
	if strings.TrimSpace(resp.Stderr) == "" {
		t.Fatalf("timeout response should include an error: %+v", resp)
	}
}

func TestEndpointFailsClosedWhenHostOpenerIsMissing(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := &Server{
		SessionID: "ses_tcp",
		Token:     "cap_good",
		Profile:   "test",
		Backend:   "native",
		Commands:  []string{"open"},
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Audit:     writer,
	}
	if err := server.StartEndpoint(ctx, TCPEndpoint("127.0.0.1:0")); err != nil {
		t.Fatalf("start tcp broker: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reqCancel()
	resp := ClientOpenEndpoint(reqCtx, server.Endpoint, Request{
		ID:              "req_no_opener",
		SessionID:       "ses_tcp",
		CapabilityToken: "cap_good",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status != "error" || resp.ExitCode != 1 || !strings.Contains(resp.Stderr, "host opener is not configured") {
		t.Fatalf("expected missing opener to fail closed over endpoint, got %+v", resp)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	validateAuditJSONLWithSchema(t, auditPath)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"host.open"`, `"decision":"deny"`, `"status":"error"`, `"target":"https://example.com"`, `"host opener is not configured"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("audit missing %q: %s", want, data)
		}
	}
}

func TestEndpointRejectsUnknownTopLevelBrokerRequestField(t *testing.T) {
	opener := &recordingOpener{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := &Server{
		SessionID: "ses_tcp",
		Token:     "cap_good",
		Evaluator: policy.NewEvaluator(profile.Default("test")),
		Opener:    opener,
	}
	if err := server.StartEndpoint(ctx, TCPEndpoint("127.0.0.1:0")); err != nil {
		t.Fatalf("start tcp broker: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	conn, err := net.DialTimeout(server.Endpoint.Network, server.Endpoint.Address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw := `{"id":"req_extra","sessionId":"ses_tcp","capabilityToken":"cap_good","subject":"command:open","command":"open","argv":["open","https://example.com"],"route":"host-broker","action":"host.open","args":{"target":"https://example.com"},"hostExec":"please"}`
	if _, err := conn.Write([]byte(raw + "\n")); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "bad-request" || resp.ExitCode != 2 {
		t.Fatalf("expected bad request for unknown top-level field, got %+v", resp)
	}
	if len(opener.urls) != 0 || len(opener.files) != 0 {
		t.Fatalf("unknown top-level field should not reach opener: urls=%v files=%v", opener.urls, opener.files)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if got := server.TransportObservation(); got.Accepted != 1 || got.RequestParsed != 0 ||
		got.RequestParseFailed != 1 || got.ResponseWritten != 1 || got.ResponseWriteFailed != 0 {
		t.Fatalf("transport observation=%+v", got)
	}
}

func compileBrokerEnvelopeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "broker-envelope.schema.json"))
	if err != nil {
		t.Fatalf("read broker envelope schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode broker envelope schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("broker-envelope.schema.json", doc); err != nil {
		t.Fatalf("add broker envelope schema: %v", err)
	}
	schema, err := compiler.Compile("broker-envelope.schema.json")
	if err != nil {
		t.Fatalf("compile broker envelope schema: %v", err)
	}
	return schema
}

func compileAuditEventSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "audit-event.schema.json"))
	if err != nil {
		t.Fatalf("read audit event schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode audit event schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("audit-event.schema.json", doc); err != nil {
		t.Fatalf("add audit event schema: %v", err)
	}
	schema, err := compiler.Compile("audit-event.schema.json")
	if err != nil {
		t.Fatalf("compile audit event schema: %v", err)
	}
	return schema
}

func validateBrokerEnvelope(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}

func validateAuditJSONLWithSchema(t *testing.T, path string) {
	t.Helper()
	schema := compileAuditEventSchema(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("audit file is empty")
	}
	for i, line := range lines {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(line)))
		if err != nil {
			t.Fatalf("decode audit line %d: %v", i+1, err)
		}
		if err := schema.Validate(doc); err != nil {
			t.Fatalf("validate audit line %d: %v\n%s", i+1, err, line)
		}
	}
}
