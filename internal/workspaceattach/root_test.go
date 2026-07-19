package workspaceattach

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalRootRejectsTraversalAncestorSymlinkAndRenameEscapes(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	client := dialPortalClient(t, server, authority, "session-root-adversary")
	defer client.Close()
	ctx := context.Background()

	for _, candidate := range []string{"../secret", "escape/secret"} {
		if _, err := client.Stat(ctx, candidate); err == nil {
			t.Fatalf("root escape %q was accepted", candidate)
		}
	}
	if err := client.Rename(ctx, "inside", "escape/moved"); err == nil {
		t.Fatal("rename through an escaping ancestor symlink was accepted")
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != "outside" {
		t.Fatalf("outside reserved fixture changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "inside")); err != nil || string(got) != "inside" {
		t.Fatalf("inside source changed after rejected rename: %q %v", got, err)
	}
}

func TestPortalRootReplacementNeverSwitchesAuthority(t *testing.T) {
	server, authority, root := newPortalTestServer(t, DefaultPortalLimits())
	client := dialPortalClient(t, server, authority, "session-root-replace")
	defer client.Close()
	old := root + ".old"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "replacement"), []byte("wrong-root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stat(context.Background(), "replacement"); !errors.Is(err, ErrPortalRootReplaced) {
		t.Fatalf("replacement root error=%v", err)
	}
}

func TestCaptureRootIdentityCollapsesFilesystemAliases(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Project-é")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	wantCanonical, wantIdentity, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	symlinkAlias := filepath.Join(parent, "alias")
	if err := os.Symlink(root, symlinkAlias); err != nil {
		t.Fatal(err)
	}
	canonical, identity, err := CaptureRootIdentity(symlinkAlias)
	if err != nil || canonical != wantCanonical || identity != wantIdentity {
		t.Fatalf("symlink alias canonical=%q identity=%+v err=%v", canonical, identity, err)
	}

	// Case and Unicode normalization aliases are filesystem-dependent. When the
	// host resolves one, it must collapse to the same captured object identity.
	for _, alias := range []string{strings.ToLower(root), filepath.Join(parent, "Project-e\u0301")} {
		canonical, identity, err := CaptureRootIdentity(alias)
		if err != nil {
			continue
		}
		if canonical != wantCanonical || identity != wantIdentity {
			t.Fatalf("filesystem alias %q diverged: canonical=%q identity=%+v", alias, canonical, identity)
		}
	}
}

func TestPortalRelativePathRejectsAllParentTraversalForms(t *testing.T) {
	for _, candidate := range []string{"..", "../x", filepath.Join("a", "..", "..", "x"), string([]byte{'a', 0, 'b'})} {
		if _, err := portalRelativePath(candidate); err == nil {
			t.Fatalf("portal path %q was accepted", candidate)
		}
	}
	if _, err := portalRelativePath("safe/path"); err != nil {
		t.Fatalf("safe path error=%v", err)
	}
}
