package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestProjectionIdeModeDefaultsSafe(t *testing.T) {
	root := t.TempDir()
	if got := ReadProjectionIdeMode(root, "privacy"); got != ProjectionIdeModeSafe {
		t.Fatalf("default mode = %q, want safe", got)
	}
	if (decisionIdeGrantChecker{storeRoot: root}).TrustedGrantActive(hostcap.GrantScope{SessionID: "s", Profile: "privacy"}) {
		t.Fatal("no trusted grant should be active by default")
	}
}

func projectionGrantFixture(t *testing.T) (Core, profile.Profile, RunSession, projectionGrantBinding) {
	t.Helper()
	root := t.TempDir()
	store := profile.Store{Root: root}
	p, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	core := New(store)
	workspace := t.TempDir()
	runSession := RunSession{
		Plan: RunPlan{
			ProfileName:    p.Name,
			Backend:        "lima",
			Workspace:      workspace,
			GuestWorkspace: "/workspace",
			RuntimeProfile: p,
		},
		Environment: RunEnvironment{Active: true, Record: environment.Record{
			ID: "env_projection", Profile: p.Name,
			Mode: environment.ModeWorkspaceBound, BoundWorkspace: workspace, BoundGuestRoot: "/workspace",
		}},
		Layout: session.Layout{ID: "ses_projection_1"},
	}
	appBinding := hostcap.OpenResourceBinding{
		PackID: "community.editor", RevisionID: "rev_0123456789abcdef", BindingID: "open-resource",
		QualifiedAppRef: "community.editor/rev_0123456789abcdef/editor",
		BindingDigest:   "sha256:" + strings.Repeat("a", 64), Commands: []string{"editor"},
		ResourceKinds: []hostcap.ResourceKind{hostcap.KindWorkspace, hostcap.KindHostFS},
	}
	authority, err := workspaceAuthorityForRunSession(runSession)
	if err != nil {
		t.Fatal(err)
	}
	return core, p, runSession, projectionGrantBindingForRun(runSession, authority, appBinding, "editor")
}

func projectionGrantCheckerForTest(root string, binding projectionGrantBinding) decisionIdeGrantChecker {
	return decisionIdeGrantChecker{storeRoot: root, bindings: map[string]projectionGrantBinding{binding.Command: binding}}
}

func TestProjectionIdeModeRequestIsNotAuthority(t *testing.T) {
	core, p, _, binding := projectionGrantFixture(t)

	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	if ReadProjectionIdeMode(core.Store.Root, p.Name) != ProjectionIdeModeTrusted {
		t.Fatal("trusted should be recorded as the requested mode")
	}
	checker := projectionGrantCheckerForTest(core.Store.Root, binding)
	if checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("a profile mode file must never be sufficient authority")
	}
	got, err := core.ProjectionIdeMode(p.Name)
	if err != nil || got != ProjectionIdeModeTrusted {
		t.Fatalf("ProjectionIdeMode = %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(core.Store.Root, "profiles", p.Name, "ide-mode.json")); err != nil {
		t.Fatalf("mode should persist under the reserved store: %v", err)
	}
}

func TestProjectionTrustedGrantDecisionApproveRevokeAndBinding(t *testing.T) {
	core, p, runSession, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != decision.KindHostAppOpenResource || d.Source.Session != binding.SessionID || d.Source.Profile != binding.Profile {
		t.Fatalf("decision binding mismatch: %+v", d)
	}
	if !slices.Contains(d.AllowedActions, decision.ActionRevoke) {
		t.Fatalf("trusted IDE decision must advertise revocation: %+v", d.AllowedActions)
	}
	checker := projectionGrantCheckerForTest(core.Store.Root, binding)
	if checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("pending decision is not a live grant")
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken, Reason: "operator-approved"}); err != nil {
		t.Fatal(err)
	}
	if !checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("approved exact binding should activate trusted IDE")
	}
	for name, mutate := range map[string]func(*hostcap.GrantScope){
		"session":     func(s *hostcap.GrantScope) { s.SessionID = "other-session" },
		"profile":     func(s *hostcap.GrantScope) { s.Profile = "other-profile" },
		"run":         func(s *hostcap.GrantScope) { s.RunID = "other-run" },
		"workspace":   func(s *hostcap.GrantScope) { s.WorkspaceID = "other-workspace" },
		"environment": func(s *hostcap.GrantScope) { s.EnvironmentID = "other-environment" },
		"pack":        func(s *hostcap.GrantScope) { s.PackID = "community.other" },
		"revision":    func(s *hostcap.GrantScope) { s.RevisionID = "rev_other" },
		"binding":     func(s *hostcap.GrantScope) { s.BindingID = "other-binding" },
		"application": func(s *hostcap.GrantScope) { s.QualifiedAppRef = "community.other/rev/app" },
		"digest":      func(s *hostcap.GrantScope) { s.BindingDigest = "sha256:" + strings.Repeat("b", 64) },
		"command":     func(s *hostcap.GrantScope) { s.Command = "other-editor" },
	} {
		t.Run("grant-does-not-cross-"+name, func(t *testing.T) {
			scope := binding.scope()
			mutate(&scope)
			if checker.TrustedGrantActive(scope) {
				t.Fatalf("approved grant crossed %s boundary: %+v", name, scope)
			}
		})
	}

	changed := binding
	changed.IdentityID = "different-identity"
	if projectionGrantCheckerForTest(core.Store.Root, changed).TrustedGrantActive(changed.scope()) {
		t.Fatal("identity drift must invalidate the grant")
	}
	if _, err := core.RevokeDecision(DecisionRevokeRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Reason: "operator-revoked"}); err != nil {
		t.Fatal(err)
	}
	if checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("revoked decision must stop the next trusted launch")
	}

	raw, err := os.ReadFile(filepath.Join(core.Store.Root, "operator-center", "decisions", d.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), runSession.Plan.Workspace) {
		t.Fatalf("decision record must use a workspace identity, not host path: %s", raw)
	}
}

func TestProjectionDecisionBindsPathFreeResourceAuthorityClasses(t *testing.T) {
	core, p, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	privateDecision, err := core.inspectDecisionPrivate(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantClasses := "hostfs-portal,workspace"
	if binding.ResourceClasses != wantClasses || privateDecision.ProviderRef.Data["resourceClasses"] != wantClasses || privateDecision.ProviderRef.Data["hostfsAuthority"] != "existing-content-only" || privateDecision.ProviderRef.Data["hostfsOwner"] != "same-session" {
		t.Fatalf("resource authority binding is incomplete: binding=%+v data=%+v", binding, privateDecision.ProviderRef.Data)
	}
	if d.Preview.Facts["resourceClasses"] != wantClasses {
		t.Fatalf("decision preview resource classes=%v", d.Preview.Facts["resourceClasses"])
	}
	raw, err := json.Marshal(privateDecision)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/", "/private/", "/hideout/hostfs", "portalToken", "providerToken", "canonicalPath"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("decision leaked lower HostFS authority %q: %s", forbidden, raw)
		}
	}
	changed := binding
	changed.ResourceClasses = "workspace"
	if projectionGrantMatches(privateDecision, changed) {
		t.Fatal("approval crossed a different resource-authority class set")
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	checker := projectionGrantCheckerForTest(core.Store.Root, binding)
	if !checker.TrustedGrantActiveForResource(binding.scope(), hostcap.ResourceRef{Kind: hostcap.KindWorkspace}) || !checker.TrustedGrantActiveForResource(binding.scope(), hostcap.ResourceRef{Kind: hostcap.KindHostFS}) {
		t.Fatal("exact approved binding did not admit its declared resource classes")
	}
	checker.bindings[binding.Command] = changed
	if checker.TrustedGrantActiveForResource(changed.scope(), hostcap.ResourceRef{Kind: hostcap.KindHostFS}) {
		t.Fatal("workspace-only approval admitted a HostFS portal resource")
	}
}

func TestProjectionSessionEndInvalidatesApprovedGrant(t *testing.T) {
	core, p, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	if err := core.invalidateProjectionGrant(d.ID, "session-ended"); err != nil {
		t.Fatal(err)
	}
	if projectionGrantCheckerForTest(core.Store.Root, binding).TrustedGrantActive(binding.scope()) {
		t.Fatal("session-ended grant must not survive")
	}
}

func TestProjectionHostFSAuthorityLossInvalidatesApprovedAppDecision(t *testing.T) {
	core, p, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	checker := projectionGrantCheckerForTest(core.Store.Root, binding)
	if !checker.TrustedGrantActiveForResource(binding.scope(), hostcap.ResourceRef{Kind: hostcap.KindHostFS}) {
		t.Fatal("approved HostFS-class app decision was not active")
	}
	provider := &hostFSReadProvider{core: core, sessionID: binding.SessionID}
	provider.HostAppResourceAuthorityLost(hostcapResourceOwner(binding), "hostfs-resource-authority-lost")
	if checker.TrustedGrantActiveForResource(binding.scope(), hostcap.ResourceRef{Kind: hostcap.KindHostFS}) {
		t.Fatal("stale app decision restored lost HostFS authority")
	}
	private, err := core.inspectDecisionPrivate(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if private.State != decision.StateStale {
		t.Fatalf("resource-loss decision state=%q want stale", private.State)
	}
}

func hostcapResourceOwner(binding projectionGrantBinding) hostfs.HostAppResourceOwner {
	return hostfs.HostAppResourceOwner{SessionID: binding.SessionID, Profile: binding.Profile}
}

func TestProjectionTrustedGrantRevokesThroughManagerAPI(t *testing.T) {
	core, p, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(DecisionRevokeRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Reason: "webui-revoked"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decision/revoke", bytes.NewReader(payload))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer ui_token")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	API{Core: core, Token: "ui_token"}.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || strings.Contains(resp.Body.String(), `"errors":["`) {
		t.Fatalf("revoke API status=%d body=%s", resp.Code, resp.Body.String())
	}
	if projectionGrantCheckerForTest(core.Store.Root, binding).TrustedGrantActive(binding.scope()) {
		t.Fatal("Manager API revoke must remove live authority")
	}
}

func TestProjectionSettingSafeInvalidatesProfileGrants(t *testing.T) {
	core, p, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}

	if err := core.SetProjectionIdeMode(p.Name, ProjectionIdeModeSafe); err != nil {
		t.Fatal(err)
	}
	if ReadProjectionIdeMode(core.Store.Root, p.Name) != ProjectionIdeModeSafe {
		t.Fatal("after revoke, requested mode should be safe")
	}
	if projectionGrantCheckerForTest(core.Store.Root, binding).TrustedGrantActive(binding.scope()) {
		t.Fatal("safe selection must invalidate profile grants")
	}
}

func TestProjectionInspectionSeparatesPolicyFromObservedGuestState(t *testing.T) {
	root := t.TempDir()
	core := New(profile.Store{Root: root})
	inspection, err := core.ProjectionInspection("missing")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status == "pass" || inspection.ProfileStatus != "missing" || inspection.PathShadowObservation != "not-run" {
		t.Fatalf("missing profile inspection was falsely green: %+v", inspection)
	}
	blob, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), root) || !strings.Contains(string(blob), "no fallback") {
		t.Fatalf("inspection leaked a host path or omitted policy: %s", blob)
	}
}

func TestProjectionSafeDataDirRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "safe-data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareProjectionSafeDataDir(link); err == nil {
		t.Fatal("safe IDE state must not follow a symlink")
	}
}

func TestProjectionIdeModeRejectsUnknownAndMissingProfile(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: root}
	core := New(store)
	if err := core.SetProjectionIdeMode("nope", ProjectionIdeModeTrusted); err == nil {
		t.Fatal("setting mode for a missing profile should fail")
	}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	if err := core.SetProjectionIdeMode("privacy", "reckless"); err == nil {
		t.Fatal("unknown mode should be rejected")
	}
}

func TestProjectionModeGrantCheckerReadsControlPlaneNotWorkspace(t *testing.T) {
	// Writing a trusted marker anywhere the guest could reach (simulated: not the
	// control-plane path) must not flip the mode. Only the reserved-store file
	// counts.
	root := t.TempDir()
	// A stray file elsewhere is ignored.
	if err := os.WriteFile(filepath.Join(t.TempDir(), "ide-mode.json"), []byte(`{"mode":"trusted-host-ide"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if (decisionIdeGrantChecker{storeRoot: root}).TrustedGrantActive(hostcap.GrantScope{SessionID: "s", Profile: "privacy"}) {
		t.Fatal("a workspace marker must not activate trusted mode")
	}
}

func TestProjectionDeduperWindow(t *testing.T) {
	d := newProjectionDeduper()
	if !d.Reserve("k") {
		t.Fatal("first should be allowed")
	}
	if d.Reserve("k") {
		t.Fatal("immediate duplicate should be suppressed")
	}
	d.Release("k")
	if !d.Reserve("k") {
		t.Fatal("released failed launch should be retryable")
	}
	d.Commit("k")
	if d.Reserve("k") {
		t.Fatal("committed launch should remain suppressed")
	}
	if !d.Reserve("other") {
		t.Fatal("different key should be allowed")
	}
}
