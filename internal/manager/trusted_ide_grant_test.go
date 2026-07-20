package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func trustedGrantTestScope(profile string) hostcap.GrantScope {
	return hostcap.GrantScope{
		Profile:         profile,
		WorkspaceID:     "wrk_abc",
		QualifiedAppRef: "builtin.vscode/rev_1/vscode",
		BindingDigest:   "sha256:deadbeef",
	}
}

func setTrustedIDEMode(t *testing.T, root, profile string) {
	t.Helper()
	if err := WriteProjectionIdeMode(root, profile, ProjectionIdeModeTrusted, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedIDEGrantMatchRequiresTrustedModeAndExactKeys(t *testing.T) {
	root := t.TempDir()
	scope := trustedGrantTestScope("default")

	// No grant, no trusted mode → no match.
	if trustedIDEGrantMatches(root, scope) {
		t.Fatal("matched with neither grant nor trusted mode")
	}

	setTrustedIDEMode(t, root, "default")
	// Trusted mode but no grant → still no match (fail closed).
	if trustedIDEGrantMatches(root, scope) {
		t.Fatal("matched in trusted mode without a grant")
	}

	if err := addTrustedIDEGrant(root, "default", TrustedIDEGrant{
		WorkspaceID: scope.WorkspaceID, QualifiedAppRef: scope.QualifiedAppRef, BindingDigest: scope.BindingDigest,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Trusted mode + matching grant → match.
	if !trustedIDEGrantMatches(root, scope) {
		t.Fatal("did not match with trusted mode and a matching grant")
	}

	// Each single-field drift must break the match.
	for name, mut := range map[string]func(hostcap.GrantScope) hostcap.GrantScope{
		"workspace": func(s hostcap.GrantScope) hostcap.GrantScope { s.WorkspaceID = "wrk_other"; return s },
		"appRef": func(s hostcap.GrantScope) hostcap.GrantScope {
			s.QualifiedAppRef = "builtin.vscode/rev_2/vscode"
			return s
		},
		"digest": func(s hostcap.GrantScope) hostcap.GrantScope { s.BindingDigest = "sha256:changed"; return s },
	} {
		if trustedIDEGrantMatches(root, mut(scope)) {
			t.Fatalf("matched despite %s drift", name)
		}
	}

	// Switch to safe mode → grant is inert (no match) even though it exists.
	if err := WriteProjectionIdeMode(root, "default", ProjectionIdeModeSafe, time.Now()); err != nil {
		t.Fatal(err)
	}
	if trustedIDEGrantMatches(root, scope) {
		t.Fatal("matched in safe mode")
	}
}

func TestTrustedIDEGrantAddIsIdempotentAndPrivateAtomic(t *testing.T) {
	root := t.TempDir()
	g := TrustedIDEGrant{WorkspaceID: "wrk_a", QualifiedAppRef: "builtin.vscode/rev_1/vscode", BindingDigest: "sha256:x"}
	for range 3 {
		if err := addTrustedIDEGrant(root, "default", g, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	m := readTrustedIDEGrants(root, "default")
	if len(m.Grants) != 1 {
		t.Fatalf("idempotent add produced %d grants", len(m.Grants))
	}
	if m.Grants[0].GrantedAt.IsZero() {
		t.Fatal("grantedAt not stamped")
	}
	info, err := os.Stat(trustedIDEGrantsPath(root, "default"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("grant file mode = %v, want 0600", info.Mode().Perm())
	}
	// No leftover temp file from the atomic write.
	entries, err := os.ReadDir(filepath.Dir(trustedIDEGrantsPath(root, "default")))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ide-trust-grants.json.tmp") {
			t.Fatalf("atomic write left a temp file: %s", e.Name())
		}
	}
}

func TestTrustedIDEGrantMalformedManifestFailsClosed(t *testing.T) {
	root := t.TempDir()
	setTrustedIDEMode(t, root, "default")
	dir := filepath.Join(root, "profiles", "default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{not json`,
		`{"version":"wrong","profile":"default","grants":[]}`,
		`{"version":"hideout.trusted-ide-grants/v1","profile":"default","grants":[{"workspaceId":"","qualifiedAppRef":"a","bindingDigest":"b"}]}`,
		`{"version":"hideout.trusted-ide-grants/v1","profile":"default","grants":[{"workspaceId":"wrk_a","qualifiedAppRef":"builtin.vscode/rev_1/vscode","bindingDigest":"sha256:x"}],"extra":1}`,
	} {
		if err := os.WriteFile(trustedIDEGrantsPath(root, "default"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if trustedIDEGrantMatches(root, trustedGrantTestScope("default")) {
			t.Fatalf("malformed manifest matched (should fail closed): %s", body)
		}
	}
}

func TestTrustedIDEGrantRemoveByWorkspaceAndAll(t *testing.T) {
	root := t.TempDir()
	setTrustedIDEMode(t, root, "default")
	add := func(ws string) {
		if err := addTrustedIDEGrant(root, "default", TrustedIDEGrant{
			WorkspaceID: ws, QualifiedAppRef: "builtin.vscode/rev_1/vscode", BindingDigest: "sha256:x",
		}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	add("wrk_a")
	add("wrk_b")
	if err := removeTrustedIDEGrantsForWorkspace(root, "default", "wrk_a"); err != nil {
		t.Fatal(err)
	}
	m := readTrustedIDEGrants(root, "default")
	if len(m.Grants) != 1 || m.Grants[0].WorkspaceID != "wrk_b" {
		t.Fatalf("remove-by-workspace left %+v", m.Grants)
	}
	if !hasTrustedIDEGrants(root, "default") {
		t.Fatal("hasTrustedIDEGrants false with one grant left")
	}
	if err := removeAllTrustedIDEGrants(root, "default"); err != nil {
		t.Fatal(err)
	}
	if hasTrustedIDEGrants(root, "default") {
		t.Fatal("grants remain after removeAll")
	}
	// removeAll on absent file is a no-op success.
	if err := removeAllTrustedIDEGrants(root, "default"); err != nil {
		t.Fatalf("removeAll on absent file errored: %v", err)
	}
}

func TestTrustedIDEGrantGuestWorkspaceWriteCannotForge(t *testing.T) {
	root := t.TempDir()
	setTrustedIDEMode(t, root, "default")
	scope := trustedGrantTestScope("default")

	// A guest can only write inside the workspace, never the profile store.
	// Simulate a forged grant file placed in a workspace directory.
	workspace := t.TempDir()
	forged := trustedIDEGrantManifest{
		Version: TrustedIDEGrantsVersion, Profile: "default",
		Grants: []TrustedIDEGrant{{WorkspaceID: scope.WorkspaceID, QualifiedAppRef: scope.QualifiedAppRef, BindingDigest: scope.BindingDigest, GrantedAt: time.Now()}},
	}
	data, _ := json.MarshalIndent(forged, "", "  ")
	if err := os.WriteFile(filepath.Join(workspace, "ide-trust-grants.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// The matcher only reads profiles/<p>/, so a workspace-side forgery is inert.
	if trustedIDEGrantMatches(root, scope) {
		t.Fatal("workspace-side forged grant authorized a trusted launch")
	}
}

// TestDeriveWorkspaceIDIsStableForGrantAndRun is the analyze-U1 gate: the grant
// command derives workspaceID independently, so it must equal what a run
// derives for the same project. Both go through CaptureRootIdentity +
// deriveWorkspaceIDFromRoot, so equality is by construction — this test locks
// that the two derivations stay identical (any divergence would silently make
// grants never match).
func TestDeriveWorkspaceIDIsStableForGrantAndRun(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	core := New(profile.Store{Root: root})

	derive := func() string {
		canonical, identity, err := workspaceattach.CaptureRootIdentity(workspace)
		if err != nil {
			t.Fatal(err)
		}
		id, err := core.deriveWorkspaceIDFromRoot(canonical, identity)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	first := derive()
	if first == "" {
		t.Fatal("empty workspaceID")
	}
	// A second independent derivation (the grant command's path) must equal the
	// first (the run's path) for the same project.
	if second := derive(); second != first {
		t.Fatalf("workspaceID not stable across derivations: %q vs %q", first, second)
	}
	// A different project must derive a different workspaceID.
	otherWorkspace := t.TempDir()
	canonical, identity, err := workspaceattach.CaptureRootIdentity(otherWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	other, err := core.deriveWorkspaceIDFromRoot(canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatal("distinct projects derived the same workspaceID")
	}
}

func TestGrantAndRevokeTrustedIDEPromotesRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	core := New(profile.Store{Root: root})
	if err := WriteProjectionIdeMode(root, "default", ProjectionIdeModeTrusted, time.Now()); err != nil {
		t.Fatal(err)
	}
	canonical, identity, err := workspaceattach.CaptureRootIdentity(workspace)
	if err != nil {
		t.Fatal(err)
	}
	wsID, err := core.deriveWorkspaceIDFromRoot(canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	scope := hostcap.GrantScope{Profile: "default", Command: "code", WorkspaceID: wsID, QualifiedAppRef: "builtin.vscode/rev_1/vscode", BindingDigest: "sha256:x"}
	maybeRecordTrustedIDERequest(root, scope)

	result, err := core.GrantHostAppTrust("default", workspace, "code")
	if err != nil {
		t.Fatalf("GrantTrustedIDE: %v", err)
	}
	if !result.Granted || result.WorkspaceID != wsID {
		t.Fatalf("grant result = %+v", result)
	}
	if !trustedIDEGrantMatches(root, scope) {
		t.Fatal("grant not active after GrantTrustedIDE")
	}
	// Idempotent second grant.
	if r2, err := core.GrantHostAppTrust("default", workspace, "code"); err != nil || r2.Granted {
		t.Fatalf("second grant = %+v err=%v", r2, err)
	}
	// Revoke.
	if err := core.RevokeHostAppTrust("default", workspace, "code"); err != nil {
		t.Fatal(err)
	}
	if trustedIDEGrantMatches(root, scope) {
		t.Fatal("grant still active after revoke")
	}
}

func TestGrantTrustedIDERequiresTrustedModeAndRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	core := New(profile.Store{Root: root})

	// Safe mode → refuse, naming the ide-mode command.
	if _, err := core.GrantHostAppTrust("default", workspace, "code"); err == nil || !strings.Contains(err.Error(), "host-app-mode") {
		t.Fatalf("safe-mode grant err=%v, want ide-mode guidance", err)
	}
	// Trusted mode but no request → refuse, naming the run-once path.
	if err := WriteProjectionIdeMode(root, "default", ProjectionIdeModeTrusted, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := core.GrantHostAppTrust("default", workspace, "code"); err == nil || !strings.Contains(err.Error(), "no host-app trust request") {
		t.Fatalf("no-request grant err=%v, want run-once guidance", err)
	}
}
