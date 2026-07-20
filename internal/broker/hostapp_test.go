package broker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/cmdgrammar"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

type recordingLauncher struct {
	argv        [][]string
	beforeGuard func()
}

func (r *recordingLauncher) Run(_ context.Context, argv []string, guard appopen.LaunchGuard) error {
	if r.beforeGuard != nil {
		r.beforeGuard()
	}
	if guard != nil {
		if err := guard(); err != nil {
			return err
		}
	}
	r.argv = append(r.argv, argv)
	return nil
}

type projectionHostFSAuthority struct {
	sessionID   string
	profile     string
	active      bool
	readAllowed bool
	checks      int
}

func (a *projectionHostFSAuthority) AllowsRead(hostfs.ReadGrantCheck) (bool, error) {
	return a.readAllowed, nil
}

func (a *projectionHostFSAuthority) ValidateHostAppResource(check hostfs.HostAppResourceCheck) error {
	a.checks++
	if !a.active || check.Owner.SessionID != a.sessionID || check.Owner.Profile != a.profile {
		return errors.New("HostFS portal owner ended")
	}
	return nil
}

type projectionGrantChecker bool

func (g projectionGrantChecker) TrustedGrantActive(hostcap.GrantScope) bool { return bool(g) }
func (g projectionGrantChecker) TrustedGrantActiveForResource(hostcap.GrantScope, hostcap.ResourceRef) bool {
	return bool(g)
}

type workspaceOnlyProjectionGrant struct{}

func (workspaceOnlyProjectionGrant) TrustedGrantActive(hostcap.GrantScope) bool { return true }
func (workspaceOnlyProjectionGrant) TrustedGrantActiveForResource(_ hostcap.GrantScope, resource hostcap.ResourceRef) bool {
	return resource.Kind == hostcap.KindWorkspace
}

const placeholderBindingDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testProjectionRegistry(t *testing.T, digest string) cmdproxy.Registry {
	t.Helper()
	grammar := cmdgrammar.OpenResourceGrammar{Kind: cmdgrammar.GrammarOpenResourceV1, ResourceCount: 1, GotoFlags: []string{"-g", "--goto"}, NewWindowFlags: []string{"-n", "--new-window"}, ReuseWindowFlags: []string{"-r", "--reuse-window"}, UnknownFlags: cmdgrammar.UnknownFlagsDeny}
	registry, err := cmdproxy.NewRegistry([]cmdproxy.Registration{{
		Name: "code", Action: cmdproxy.ActionHostAppOpenResource, ArgvSchema: cmdproxy.ArgvSchemaOpenResourceV1,
		StreamPolicy: cmdproxy.StreamMetadataOnly, DefaultMode: cmdproxy.DefaultModeAllow,
		AllowedTargets: []string{"workspace-file", "workspace-dir"}, OwnerType: cmdproxy.OwnerHostAppProjection,
		BindingDigest: digest, OpenResourceGrammar: &grammar,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func newProjectionServer(t *testing.T, hostRoot string, hostApp *hostcap.ProjectionConfig, digest string) (Server, string) {
	t.Helper()
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return Server{
		SessionID:       "ses_1",
		Token:           "cap_good",
		Profile:         "privacy",
		Backend:         "lima",
		HostRoot:        hostRoot,
		GuestRoot:       "/workspace",
		WorkspaceID:     "wrk_broker_projection_fixture",
		CommandRegistry: testProjectionRegistry(t, digest),
		Evaluator:       policy.NewEvaluator(profile.Default("privacy")),
		Audit:           writer,
		HostApp:         hostApp,
	}, auditPath
}

func projectionRequest(intent map[string]any, digest string) Request {
	return Request{
		ID:              "req_1",
		SessionID:       "ses_1",
		CapabilityToken: "cap_good",
		Subject:         "command:code",
		Command:         "code",
		Route:           "host-broker",
		Action:          "host.app.open-resource",
		Args:            map[string]any{"intent": intent, "cwd": "/workspace", "bindingDigest": digest},
	}
}

func codeIntent(guestPath string) map[string]any {
	return map[string]any{
		"resources":  []any{map[string]any{"guestPath": guestPath}},
		"windowMode": "reuse",
	}
}

func projectionConfig(t *testing.T, launcher appopen.Launcher) (*hostcap.ProjectionConfig, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Applications")
	executable := filepath.Join(root, "Editor.app", "Contents", "MacOS", "Code")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("editor"), 0o700); err != nil {
		t.Fatal(err)
	}
	qualified := "builtin.vscode/rev_0123456789abcdef/vscode"
	expectation := hostcap.ApplicationExpectation{QualifiedAppRef: qualified, BundleNames: []string{"Editor.app"}, ExecutableRelativePath: "Contents/MacOS/Code", ExpectedBundleID: "com.microsoft.VSCode", ExpectedTeamID: "UBF8T346G9"}
	opts := hostcap.ApplicationIdentityOptions{
		Roots: []hostcap.ApplicationRoot{{Class: hostcap.ApplicationRootOperator, Path: root}}, OperatorUID: uint32(os.Getuid()),
		ObserveSigning: func(string) (hostcap.SigningObservation, error) {
			return hostcap.SigningObservation{Signed: true, Trusted: true, TrustAnchor: "test-system-policy", BundleID: "com.microsoft.VSCode", TeamID: "UBF8T346G9", CodeIdentity: "observed-code"}, nil
		},
	}
	identity, err := hostcap.ResolveApplicationIdentity(expectation, opts)
	if err != nil {
		t.Fatal(err)
	}
	binding := hostcap.OpenResourceBinding{
		PackID: "builtin.vscode", RevisionID: "rev_0123456789abcdef", BindingID: "code-command", QualifiedAppRef: qualified,
		Commands: []string{"code"}, CapabilityID: hostcap.CapabilityAppOpenResource, ResourceKinds: []hostcap.ResourceKind{hostcap.KindWorkspace}, ResultPolicy: hostcap.ResultNone,
		Access: hostcap.BindingAccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
		Grammar:     hostcap.BindingGrammar{Kind: hostcap.BindingGrammarV1, ResourceCount: 1, GotoFlags: []string{"-g", "--goto"}, NewWindowFlags: []string{"-n", "--new-window"}, ReuseWindowFlags: []string{"-r", "--reuse-window"}, UnknownFlags: "deny"},
		Application: expectation, Launch: appopen.LaunchSpec{GotoFlag: "--goto", NewWindowFlag: "--new-window", ReuseWindowFlag: "--reuse-window", GotoSeparator: ":"},
		SourceDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", PermissionFingerprint: "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		ObservedIdentityDigest: identity.IdentityDigest(), ObservedIdentity: identity,
	}
	binding, err = hostcap.FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := hostcap.NewBindingCatalog([]hostcap.OpenResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	return &hostcap.ProjectionConfig{
		Platform: hostcap.PlatformDarwin, SafeUserDataDir: state,
		Launcher: launcher, Bindings: catalog, RunID: "run_1", WorkspaceID: "wrk_broker_projection_fixture",
		ValidateLifecycle: func(hostcap.OpenResourceBinding) error { return nil },
		RevalidateIdentity: func(expectation hostcap.ApplicationExpectation, previous hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
			return hostcap.RevalidateApplicationIdentity(expectation, previous, opts)
		},
	}, binding.BindingDigest
}

func TestProjectionMissingIntentIsBadRequest(t *testing.T) {
	hostRoot := t.TempDir()
	server, _ := newProjectionServer(t, hostRoot, &hostcap.ProjectionConfig{}, placeholderBindingDigest)
	req := projectionRequest(nil, placeholderBindingDigest)
	req.Args = map[string]any{"cwd": "/workspace"}
	resp := server.Handle(context.Background(), req)
	if resp.Status != "bad-request" {
		t.Fatalf("missing intent should be bad-request: %+v", resp)
	}
}

func TestProjectionDisabledFailsClosed(t *testing.T) {
	hostRoot := t.TempDir()
	server, auditPath := newProjectionServer(t, hostRoot, nil, placeholderBindingDigest) // projection not configured
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/a.go"), placeholderBindingDigest))
	if resp.Status != "denied" || resp.Data["code"] != hostcap.CodeProviderUnavailable {
		t.Fatalf("nil HostApp should fail closed: %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"relativeTarget":"a.go"`) || !strings.Contains(string(data), `"code":"projection.provider.unavailable"`) {
		t.Fatalf("valid refused intent lost typed audit context: %s", data)
	}
	assertNoHostPath(t, resp, hostRoot)
}

func TestProjectionLifecycleRevocationFailsBeforeHostEffect(t *testing.T) {
	hostRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostRoot, "a.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, digest := projectionConfig(t, launcher)
	checks := 0
	projection.ValidateLifecycle = func(hostcap.OpenResourceBinding) error {
		checks++
		return &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "binding disabled after run start"}
	}
	server, _ := newProjectionServer(t, hostRoot, projection, digest)
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/a.go"), digest))
	if resp.Status != "denied" || resp.Data["code"] != hostcap.CodeCommandUnbound || checks != 1 {
		t.Fatalf("revoked lifecycle did not fail closed: response=%+v checks=%d", resp, checks)
	}
	if len(launcher.argv) != 0 {
		t.Fatalf("revoked lifecycle reached host launcher: %v", launcher.argv)
	}
}

func TestProjectionLifecycleRevocationAtLaunchGuardFailsClosed(t *testing.T) {
	hostRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostRoot, "a.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, digest := projectionConfig(t, launcher)
	checks := 0
	projection.ValidateLifecycle = func(hostcap.OpenResourceBinding) error {
		checks++
		if checks == 1 {
			return nil
		}
		return &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "binding disabled before launch"}
	}
	server, _ := newProjectionServer(t, hostRoot, projection, digest)
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/a.go"), digest))
	if resp.Status != "denied" || resp.Data["code"] != hostcap.CodeCommandUnbound || checks != 2 {
		t.Fatalf("launch-boundary lifecycle race did not fail closed: response=%+v checks=%d", resp, checks)
	}
	if len(launcher.argv) != 0 {
		t.Fatalf("launch-boundary lifecycle race started a host process: %v", launcher.argv)
	}
}

func TestProjectionEscapeAndHappyPath(t *testing.T) {
	hostRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostRoot, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, digest := projectionConfig(t, launcher)
	server, _ := newProjectionServer(t, hostRoot, projection, digest)

	// Escape path fails closed with no host path leaked.
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/../etc/passwd"), digest))
	if resp.Status != "denied" {
		t.Fatalf("escape path must be denied: %+v", resp)
	}
	assertNoHostPath(t, resp, hostRoot)
	if len(launcher.argv) != 0 {
		t.Fatalf("escape path must not launch: %v", launcher.argv)
	}

	resp = server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/a.go"), digest))
	if resp.Status != "ok" || resp.Data["outcome"] != "launched" {
		t.Fatalf("happy path should launch: %+v", resp)
	}
	// The safe launch must disclose its posture and the trusted upgrade path;
	// a silent open is indistinguishable from the operator's native IDE.
	if !strings.Contains(resp.Stderr, "safe host app window") ||
		!strings.Contains(resp.Stderr, "extensions disabled") ||
		!strings.Contains(resp.Stderr, "hideout profile host-app-mode") ||
		!strings.Contains(resp.Stderr, "hideout allow host-app") {
		t.Fatalf("safe launch did not disclose posture and upgrade path: %q", resp.Stderr)
	}
	assertNoHostPath(t, resp, hostRoot)
	if len(launcher.argv) != 1 {
		t.Fatalf("expected one launch, got %d", len(launcher.argv))
	}
	// The host path IS in the argv (Core-internal), but must not be in the guest
	// response.
	joined := strings.Join(launcher.argv[0], " ")
	if !strings.Contains(joined, hostRoot) {
		t.Fatalf("host argv should contain the resolved host path: %v", launcher.argv[0])
	}
}

func TestProjectionWorkspaceAuthorityKeepsConcurrentSessionRootsSeparate(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	for _, root := range roots {
		if err := os.WriteFile(filepath.Join(root, "same.go"), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for index, root := range roots {
		launcher := &recordingLauncher{}
		projection, digest := projectionConfig(t, launcher)
		server, _ := newProjectionServer(t, root, projection, digest)
		authorityID := "wrk_concurrent_" + string(rune('a'+index))
		server.SessionID = "ses_concurrent_" + string(rune('a'+index))
		server.WorkspaceID = authorityID
		projection.WorkspaceID = authorityID
		req := projectionRequest(codeIntent("/workspace/same.go"), digest)
		req.SessionID = server.SessionID
		resp := server.Handle(context.Background(), req)
		if resp.Status != "ok" || len(launcher.argv) != 1 || !strings.Contains(strings.Join(launcher.argv[0], " "), filepath.Join(root, "same.go")) {
			t.Fatalf("session %d did not resolve through its attachment: response=%+v argv=%v", index, resp, launcher.argv)
		}
		otherRoot := roots[1-index]
		if strings.Contains(strings.Join(launcher.argv[0], " "), otherRoot) {
			t.Fatalf("session %d crossed into sibling root %q: %v", index, otherRoot, launcher.argv[0])
		}

		projection.WorkspaceID = "wrk_wrong_attachment"
		resp = server.Handle(context.Background(), req)
		if resp.Status != "denied" || resp.Data["code"] != hostcap.CodePathNoHostMapping || len(launcher.argv) != 1 {
			t.Fatalf("session %d accepted mismatched attachment authority: response=%+v argv=%v", index, resp, launcher.argv)
		}
	}
}

func TestProjectionBindingAndIntentForgeryNeverFallsBack(t *testing.T) {
	hostRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostRoot, "a.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, digest := projectionConfig(t, launcher)
	server, _ := newProjectionServer(t, hostRoot, projection, digest)

	for name, mutate := range map[string]func(*Request){
		"path command": func(req *Request) { req.Command = "/tmp/code" },
		"unregistered command": func(req *Request) {
			req.Command = "editor"
			req.Subject = "command:editor"
		},
		"wrong digest": func(req *Request) { req.Args["bindingDigest"] = placeholderBindingDigest },
		"forged app":   func(req *Request) { req.Args["intent"].(map[string]any)["appRef"] = "other" },
		"forged capability": func(req *Request) {
			req.Args["intent"].(map[string]any)["capabilityId"] = "host.exec"
		},
		"forged result policy": func(req *Request) {
			req.Args["intent"].(map[string]any)["resultPolicy"] = "stream"
		},
		"forged kind": func(req *Request) {
			req.Args["intent"].(map[string]any)["resources"].([]any)[0].(map[string]any)["kind"] = "workspace"
		},
		"forged host path": func(req *Request) { req.Args["intent"].(map[string]any)["hostPath"] = "/Users/alice" },
		"unknown envelope": func(req *Request) { req.Args["capability"] = "host.exec" },
		"action fallback": func(req *Request) {
			req.Action = cmdproxy.ActionHostOpen
			req.Args = map[string]any{"target": ".", "cwd": "/workspace"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := projectionRequest(codeIntent("/workspace/a.go"), digest)
			mutate(&req)
			resp := server.Handle(context.Background(), req)
			if resp.Status == "ok" || len(launcher.argv) != 0 {
				t.Fatalf("forgery launched or fell back: response=%+v argv=%v", resp, launcher.argv)
			}
		})
	}
}

func TestProjectionDerivesHostFSPortalKindFromLivePolicy(t *testing.T) {
	hostFile := filepath.Join(t.TempDir(), "portal.txt")
	if err := os.WriteFile(hostFile, []byte("portal"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, _ := projectionConfig(t, launcher)
	bindings := projection.Bindings.Bindings()
	if len(bindings) != 1 {
		t.Fatalf("bindings=%+v", bindings)
	}
	binding := bindings[0]
	binding.ResourceKinds = []hostcap.ResourceKind{hostcap.KindWorkspace, hostcap.KindHostFS}
	binding, err := hostcap.FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	projection.Bindings, err = hostcap.NewBindingCatalog([]hostcap.OpenResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: hostfs.Config{Grants: []hostfs.Rule{{HostPath: hostFile, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile, Reason: "test portal"}}}, StoreRoot: filepath.Join(t.TempDir(), "store")})
	if err != nil {
		t.Fatal(err)
	}
	server, auditPath := newProjectionServer(t, t.TempDir(), projection, binding.BindingDigest)
	authority := &projectionHostFSAuthority{sessionID: server.SessionID, profile: server.Profile, active: true}
	service := hostfs.NewService(policy)
	service.ReadAuthority = authority
	server.HostFS = &service
	guestPath := "/hideout/hostfs" + hostFile
	resp := server.Handle(context.Background(), projectionRequest(codeIntent(guestPath), binding.BindingDigest))
	if resp.Status != "ok" || len(launcher.argv) != 1 || !strings.Contains(strings.Join(launcher.argv[0], " "), hostFile) {
		t.Fatalf("live HostFS mapping was not derived and launched: response=%+v argv=%v", resp, launcher.argv)
	}
	assertNoHostPath(t, resp, hostFile)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"resourceClass":"hostfs-portal"`) || !strings.Contains(string(data), `"relativeTarget":"portal.txt"`) || strings.Contains(string(data), hostFile) || strings.Contains(string(data), "/hideout/hostfs") {
		t.Fatalf("HostFS projection evidence was incomplete or leaked a lower path: %s", data)
	}
}

func TestProjectionRechecksHostFSAuthorityAfterAppApprovalAtLaunchBoundary(t *testing.T) {
	hostFile := filepath.Join(t.TempDir(), "approved.txt")
	if err := os.WriteFile(hostFile, []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	authority := &projectionHostFSAuthority{sessionID: "ses_1", profile: "privacy", active: true}
	launcher := &recordingLauncher{beforeGuard: func() { authority.active = false }}
	projection, _ := projectionConfig(t, launcher)
	binding := projection.Bindings.Bindings()[0]
	binding.ResourceKinds = []hostcap.ResourceKind{hostcap.KindWorkspace, hostcap.KindHostFS}
	binding.Access = hostcap.BindingAccessAskEachRun
	binding.SafetyProfileID, binding.SafetyProfileVersion = "", ""
	var err error
	binding, err = hostcap.FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	projection.Bindings, err = hostcap.NewBindingCatalog([]hostcap.OpenResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	projection.Grants = projectionGrantChecker(true)
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: hostfs.Config{Grants: []hostfs.Rule{{
		ID: "hfs_content", HostPath: hostFile, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile, Reason: "existing content",
	}}}, StoreRoot: filepath.Join(t.TempDir(), "store")})
	if err != nil {
		t.Fatal(err)
	}
	service := hostfs.NewService(policy)
	service.ReadAuthority = authority
	server, auditPath := newProjectionServer(t, t.TempDir(), projection, binding.BindingDigest)
	server.HostFS = &service
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/hideout/hostfs"+hostFile), binding.BindingDigest))
	if resp.Status != "denied" || resp.Data["code"] != hostcap.CodePathNoHostMapping || len(launcher.argv) != 0 || authority.checks < 3 {
		t.Fatalf("stale HostFS authority reached launch: response=%+v argv=%v checks=%d", resp, launcher.argv, authority.checks)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), hostFile) || strings.Contains(string(data), "/hideout/hostfs") || !strings.Contains(string(data), `"resourceClass":"hostfs-portal"`) {
		t.Fatalf("refusal evidence leaked or lost its class: %s", data)
	}
	assertProjectionRefusalBindingEvidence(t, auditPath, binding, hostFile)
}

func TestProjectionWorkspaceApprovalCannotAuthorizeHostFSPortalClass(t *testing.T) {
	hostFile := filepath.Join(t.TempDir(), "portal.txt")
	if err := os.WriteFile(hostFile, []byte("portal"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, _ := projectionConfig(t, launcher)
	binding := projection.Bindings.Bindings()[0]
	binding.ResourceKinds = []hostcap.ResourceKind{hostcap.KindWorkspace, hostcap.KindHostFS}
	binding.Access = hostcap.BindingAccessAskEachRun
	binding.SafetyProfileID, binding.SafetyProfileVersion = "", ""
	var err error
	binding, err = hostcap.FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	projection.Bindings, err = hostcap.NewBindingCatalog([]hostcap.OpenResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	projection.Grants = workspaceOnlyProjectionGrant{}
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: hostfs.Config{Grants: []hostfs.Rule{{
		ID: "hfs_content", HostPath: hostFile, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile, Reason: "existing content",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	authority := &projectionHostFSAuthority{sessionID: "ses_1", profile: "privacy", active: true}
	service := hostfs.NewService(policy)
	service.ReadAuthority = authority
	server, auditPath := newProjectionServer(t, t.TempDir(), projection, binding.BindingDigest)
	server.HostFS = &service
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/hideout/hostfs"+hostFile), binding.BindingDigest))
	if resp.Status != "denied" || resp.Data["code"] != hostcap.CodeModeTrustedDenied || len(launcher.argv) != 0 {
		t.Fatalf("workspace approval crossed into HostFS: response=%+v argv=%v", resp, launcher.argv)
	}
	// US2: the trusted-denied refusal must name the grant command, not dead-end.
	if !strings.Contains(resp.Stderr, "hideout allow host-app") {
		t.Fatalf("trusted-denied refusal did not name the grant command: %q", resp.Stderr)
	}
	assertProjectionRefusalBindingEvidence(t, auditPath, binding, hostFile)
}

func TestProjectionLateIdentityAndSafetyRefusalsRetainImmutableBindingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wantCode string
		mutate   func(*hostcap.ProjectionConfig, hostcap.OpenResourceBinding)
	}{
		{
			name:     "identity",
			wantCode: hostcap.CodeAppIdentityDrift,
			mutate: func(projection *hostcap.ProjectionConfig, binding hostcap.OpenResourceBinding) {
				projection.RevalidateIdentity = func(hostcap.ApplicationExpectation, hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
					return hostcap.ObservedApplicationIdentity{}, errors.New("identity disappeared at " + binding.ObservedIdentity.BundlePath)
				}
			},
		},
		{
			name:     "safety",
			wantCode: hostcap.CodeProviderUnavailable,
			mutate: func(projection *hostcap.ProjectionConfig, _ hostcap.OpenResourceBinding) {
				projection.SafeUserDataDir = filepath.Join(t.TempDir(), "missing-safe-state")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostRoot := t.TempDir()
			if err := os.WriteFile(filepath.Join(hostRoot, "a.go"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			launcher := &recordingLauncher{}
			projection, digest := projectionConfig(t, launcher)
			binding, ok := projection.Bindings.ResolveCommand("code")
			if !ok {
				t.Fatal("test projection binding is missing")
			}
			tc.mutate(projection, binding)
			server, auditPath := newProjectionServer(t, hostRoot, projection, digest)
			resp := server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/a.go"), digest))
			if resp.Status != "denied" || resp.Data["code"] != tc.wantCode || len(launcher.argv) != 0 {
				t.Fatalf("late %s failure did not refuse: response=%+v argv=%v", tc.name, resp, launcher.argv)
			}
			assertProjectionRefusalBindingEvidence(t, auditPath, binding, hostRoot, binding.ObservedIdentity.BundlePath, binding.ObservedIdentity.ExecutablePath)
		})
	}
}

func TestProjectionAuditUsesTypedIdeActionAndNeverRestoresRawArgv(t *testing.T) {
	hostRoot := t.TempDir()
	server, auditPath := newProjectionServer(t, hostRoot, nil, placeholderBindingDigest)
	req := projectionRequest(codeIntent("/workspace/a.go"), placeholderBindingDigest)
	req.Argv = []string{"code", "/Users/alice/private-token-cap_0123456789abcdef", "https://user:secret@example.test/repo.git", "ui_fedcba9876543210fedcba9876543210"}
	_ = server.Handle(context.Background(), req)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, data)
	}
	if event.Action != "host.app.open-resource" {
		t.Fatalf("projection audit action = %q, want host.app.open-resource", event.Action)
	}
	if _, ok := event.Details["argv"]; ok || strings.Contains(string(data), "private-token") || strings.Contains(string(data), "/Users/alice") || strings.Contains(string(data), "user:secret") || strings.Contains(string(data), "ui_fedcba") {
		t.Fatalf("projection audit leaked raw argv: %s", data)
	}
}

func TestProjectionAuditRedactionCannotEraseTypedEvidence(t *testing.T) {
	hostRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostRoot, "a.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	projection, digest := projectionConfig(t, launcher)
	server, auditPath := newProjectionServer(t, hostRoot, projection, digest)
	profileDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(profileDir, "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "policy", "redact.js"), []byte(`function redactAudit(ctx) { return {details: {}, reason: "drop"}; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	server.ProfileDir = profileDir
	server.ScriptRefs = []profile.ScriptRef{{ID: "drop", Path: "policy/redact.js", Entrypoints: []string{"redactAudit"}}}
	resp := server.Handle(context.Background(), projectionRequest(codeIntent("/workspace/a.go"), digest))
	if resp.Status != "ok" {
		t.Fatalf("projection failed: %+v", resp)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"host.app.open-resource"`, `"capability":"host.app.open-resource"`, `"mode":"safe"`, `"relativeTarget":"a.go"`, `"workspaceWritable":true`, `"outcome":"launched"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("projection evidentiary field %s was erased: %s", want, data)
		}
	}
}

func assertNoHostPath(t *testing.T, resp Response, hostRoot string) {
	t.Helper()
	blob := resp.Stdout + "\n" + resp.Stderr
	for _, v := range resp.Data {
		blob += "\n" + toStr(v)
	}
	if strings.Contains(blob, hostRoot) {
		t.Fatalf("guest response leaked host path %q: %+v", hostRoot, resp)
	}
}

func assertProjectionRefusalBindingEvidence(t *testing.T, auditPath string, binding hostcap.OpenResourceBinding, forbiddenPaths ...string) {
	t.Helper()
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatalf("projection refusal audit is empty: %s", raw)
	}
	var event audit.Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &event); err != nil {
		t.Fatalf("decode projection refusal audit: %v\n%s", err, raw)
	}
	if event.Action != "host.app.open-resource" || event.Decision != "deny" || event.Details["outcome"] != "refused" {
		t.Fatalf("unexpected projection refusal event: %+v", event)
	}
	for key, want := range map[string]string{
		"packId":          binding.PackID,
		"revisionId":      binding.RevisionID,
		"bindingId":       binding.BindingID,
		"qualifiedAppRef": binding.QualifiedAppRef,
		"bindingDigest":   binding.BindingDigest,
	} {
		if got, _ := event.Details[key].(string); got != want {
			t.Fatalf("projection refusal %s=%q want %q: %+v", key, got, want, event.Details)
		}
	}
	for _, forbidden := range forbiddenPaths {
		if forbidden != "" && strings.Contains(string(raw), forbidden) {
			t.Fatalf("projection refusal audit leaked host path %q: %s", forbidden, raw)
		}
	}
	if strings.Contains(string(raw), "/hideout/hostfs") {
		t.Fatalf("projection refusal audit leaked the provider portal path: %s", raw)
	}
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
