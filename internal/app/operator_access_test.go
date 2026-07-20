package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func newOperatorAccessTestApp(t *testing.T) (app, *bytes.Buffer, profile.Store) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_STORE_ROOT", filepath.Join(home, ".hideout"))
	var stdout, stderr bytes.Buffer
	a := app{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr}
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	return a, &stdout, store
}

func TestOperatorAccessAllowAndDenyWriteDurableProfileRules(t *testing.T) {
	a, stdout, store := newOperatorAccessTestApp(t)
	work := t.TempDir()
	file := filepath.Join(work, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := a.run([]string{"allow", "read", file}); err != nil {
		t.Fatalf("allow read file: %v", err)
	}
	if err := a.run([]string{"allow", "read", dir}); err != nil {
		t.Fatalf("allow read dir: %v", err)
	}
	if err := a.run([]string{"deny", "write", dir}); err != nil {
		t.Fatalf("deny write dir: %v", err)
	}
	if err := a.run([]string{"allow", "all", file}); err != nil {
		t.Fatalf("allow all file: %v", err)
	}

	p, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	type shape struct {
		HostPath string
		Ops      []hostfs.Op
		Scope    hostfs.Scope
		Overlay  bool
	}
	var grants, denies []shape
	for _, rule := range p.HostFS.Grants {
		grants = append(grants, shape{rule.HostPath, rule.Ops, rule.Scope, rule.Overlay})
	}
	for _, rule := range p.HostFS.Deny {
		denies = append(denies, shape{rule.HostPath, rule.Ops, rule.Scope, rule.Overlay})
	}
	resolvedFile, resolvedDir := file, dir
	wantGrants := []shape{
		{resolvedFile, []hostfs.Op{hostfs.OpRead}, hostfs.ScopeExactFile, false},
		{resolvedDir, []hostfs.Op{hostfs.OpRead, hostfs.OpList}, hostfs.ScopeRecursiveDir, false},
		{resolvedFile, []hostfs.Op{hostfs.OpRead}, hostfs.ScopeExactFile, false},
	}
	if len(grants) != len(wantGrants)+1 {
		t.Fatalf("grants = %+v", grants)
	}
	for index, want := range wantGrants {
		if !reflect.DeepEqual(grants[index], want) {
			t.Fatalf("grant[%d] = %+v, want %+v", index, grants[index], want)
		}
	}
	lastGrant := grants[len(grants)-1]
	if lastGrant.HostPath != resolvedFile || !lastGrant.Overlay || lastGrant.Scope != hostfs.ScopeExactFile {
		t.Fatalf("allow all write half = %+v", lastGrant)
	}
	if len(denies) != 1 || denies[0].HostPath != resolvedDir || !denies[0].Overlay || denies[0].Scope != hostfs.ScopeRecursiveDir {
		t.Fatalf("denies = %+v", denies)
	}

	output := stdout.String()
	for _, want := range []string{"allowed read", "denied write", "allowed all", "stages changes in a session overlay", "decision approval"} {
		if !strings.Contains(output, want) {
			t.Fatalf("access output missing %q:\n%s", want, output)
		}
	}
}

func TestOperatorAccessMatchesAdvancedProfileFSAuthority(t *testing.T) {
	a, _, store := newOperatorAccessTestApp(t)
	work := t.TempDir()
	file := filepath.Join(work, "spec.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := a.run([]string{"allow", "read", file, "--for-profile", "natural"}); err != nil {
		t.Fatalf("natural allow: %v", err)
	}
	if err := a.run([]string{"profile", "fs", "advanced", "add", "--fs", "read:" + file, "--reason", "advanced parity fixture"}); err != nil {
		t.Fatalf("advanced add: %v", err)
	}

	natural, err := store.Load("natural")
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := store.Load("advanced")
	if err != nil {
		t.Fatal(err)
	}
	if len(natural.HostFS.Grants) != 1 || len(advanced.HostFS.Grants) != 1 {
		t.Fatalf("grants natural=%d advanced=%d", len(natural.HostFS.Grants), len(advanced.HostFS.Grants))
	}
	n, adv := natural.HostFS.Grants[0], advanced.HostFS.Grants[0]
	if n.HostPath != adv.HostPath || !reflect.DeepEqual(n.Ops, adv.Ops) || n.Scope != adv.Scope || n.Overlay != adv.Overlay {
		t.Fatalf("authority parity broken:\nnatural  = %+v\nadvanced = %+v", n, adv)
	}
}

// TestOperatorAllowHostAppFailsClosedAndDenyIsNoOp covers the app-level dispatch
// of `allow|deny host-app <command>`. The grant success + idempotency promotion
// path is covered hermetically at the Manager layer
// (TestGrantAndRevokeTrustedIDEPromotesRequest); seeding a run request needs
// Manager-internal helpers, so this test asserts the app wiring and the
// fail-closed branches reachable from the CLI surface.
func TestOperatorAllowHostAppFailsClosedAndDenyIsNoOp(t *testing.T) {
	a, stdout, store := newOperatorAccessTestApp(t)
	// Core derives the workspace identity under the store root, which must be 0700.
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	t.Chdir(workspace)

	// 1. Safe mode (default): `allow host-app code` refuses, names the mode
	//    upgrade, and does not create the profile. Mutation proof: removing the
	//    trusted-mode gate in GrantHostAppTrust changes the refusal to the
	//    no-request message, so this host-app-mode assertion goes red (verified
	//    2026-07-20).
	err := a.run([]string{"allow", "host-app", "code"})
	if err == nil || !strings.Contains(err.Error(), "host-app-mode") {
		t.Fatalf("safe-mode allow err=%v, want host-app-mode guidance", err)
	}
	if _, statErr := os.Lstat(store.ProfilePath("default")); !os.IsNotExist(statErr) {
		t.Fatalf("failed grant touched the profile: %v", statErr)
	}

	// 2. Trusted mode but no run-recorded request → refuse and name the run-once
	//    path; still no grant is written.
	if err := manager.WriteProjectionIdeMode(store.Root, "default", manager.ProjectionIdeModeTrusted, time.Now()); err != nil {
		t.Fatal(err)
	}
	err = a.run([]string{"allow", "host-app", "code"})
	if err == nil || !strings.Contains(err.Error(), "no host-app trust request") {
		t.Fatalf("no-request allow err=%v, want run-once guidance", err)
	}
	if manager.New(store).HasHostAppTrustGrants("default") {
		t.Fatal("no-request allow wrote a grant")
	}

	// 3. `deny host-app code` with no grant is a no-op success that names the safe
	//    fallback, exercising the deny dispatch wiring with no host path leak.
	stdout.Reset()
	if err := a.run([]string{"deny", "host-app", "code"}); err != nil {
		t.Fatalf("deny no-op: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "revoked") || !strings.Contains(out, "safe isolated window") {
		t.Fatalf("deny output = %q", out)
	}
	if strings.Contains(out, workspace) {
		t.Fatalf("deny output leaked host path: %q", out)
	}
}

func TestOperatorAccessFailsClosedOnInactiveScopesAndBadPaths(t *testing.T) {
	a, _, store := newOperatorAccessTestApp(t)
	work := t.TempDir()
	file := filepath.Join(work, "x.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "once", args: []string{"allow", "read", file, "--once"}, want: "not activated"},
		{name: "project", args: []string{"allow", "read", file, "--for-this-project"}, want: "not activated"},
		{name: "missing", args: []string{"allow", "read", filepath.Join(work, "absent")}, want: "must exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := a.run(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
			if _, statErr := os.Lstat(store.ProfilePath("default")); !os.IsNotExist(statErr) {
				t.Fatalf("failed access command touched the profile: %v", statErr)
			}
		})
	}
}
