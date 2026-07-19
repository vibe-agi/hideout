package workspaceattach

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestIdentityKeyConcurrentCreationIsAtomicAndPrivate(t *testing.T) {
	root := t.TempDir()
	const callers = 32
	keys := make([][]byte, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range callers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			keys[index], errs[index] = LoadOrCreateIdentityKey(root, false)
		}(i)
	}
	close(start)
	wait.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatalf("caller %d observed a different identity key", i)
		}
	}
	keyPath := filepath.Join(root, "workspace-identity", "key.json")
	info, err := os.Lstat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity key mode=%v", info.Mode())
	}
	dirInfo, err := os.Lstat(filepath.Dir(keyPath))
	if err != nil {
		t.Fatal(err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("identity directory mode=%v", dirInfo.Mode())
	}
}

func TestIdentityKeyMissingOrCorruptWithExistingStateFailsClosed(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadOrCreateIdentityKey(root, true); err == nil || !strings.Contains(err.Error(), "explicit recovery") {
		t.Fatalf("missing key with state must fail closed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace-identity"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace-identity", "key.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateIdentityKey(root, true); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt key with state must fail closed: %v", err)
	}
}

func TestWorkspaceIDUsesCanonicalRootAndFullRootIdentity(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "project-link")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	key, err := LoadOrCreateIdentityKey(filepath.Join(root, "store"), false)
	if err != nil {
		t.Fatal(err)
	}
	canonical, identity, err := CaptureRootIdentity(workspace)
	if err != nil {
		t.Fatal(err)
	}
	aliasCanonical, aliasIdentity, err := CaptureRootIdentity(alias)
	if err != nil {
		t.Fatal(err)
	}
	first, err := DeriveWorkspaceID(key, canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeriveWorkspaceID(key, aliasCanonical, aliasIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 4+64 {
		t.Fatalf("same root identity diverged: %q %q", first, second)
	}
	replacement := identity
	replacement.Inode++
	replacedID, err := DeriveWorkspaceID(key, canonical, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if replacedID == first {
		t.Fatal("root replacement must change the workspace ID")
	}
	otherKey := bytes.Repeat([]byte{0x5a}, 32)
	otherID, err := DeriveWorkspaceID(otherKey, canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	if otherID == first {
		t.Fatal("different stores must not correlate workspace IDs")
	}
}

func TestWorkspaceIdentityDoesNotTruncateCollisionDomain(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	seen := map[string]bool{}
	for inode := uint64(1); inode <= 2048; inode++ {
		id, err := DeriveWorkspaceID(key, "/workspace-fixture", RootFileIdentity{Device: 1, Inode: inode})
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("workspace ID collision at inode %d", inode)
		}
		seen[id] = true
	}
}

func TestAttachmentSummaryStaysNonAuthoritative(t *testing.T) {
	root := t.TempDir()
	canonical, identity, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x22}, 32)
	workspaceID, err := DeriveWorkspaceID(key, canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	attachment := Attachment{
		ID: "att_0123456789abcdef", SessionID: "ses_fixture", EnvironmentID: "env_fixture",
		Incarnation: lifecycle.EnvironmentRef{EnvironmentID: "env_fixture", StartGeneration: 1, InstanceName: "hideout-fixture", BootID: "01234567-89ab-cdef-0123-456789abcdef"},
		WorkspaceID: workspaceID, CanonicalHostRoot: canonical, RootFileIdentity: identity, RootHandleIdentity: "root-handle-fixture",
		LogicalGuestRoot: LogicalWorkspaceRoot, PhysicalGuestRoot: PhysicalWorkspaceBase + "/" + workspaceID,
		Transport:    SelectedTransport,
		ProviderRef:  lifecycle.ResourceRef{Kind: "workspace.host-provider", ID: "provider-fixture", Generation: 1},
		GuestViewRef: lifecycle.ResourceRef{Kind: "workspace.guest-view", ID: "view-fixture", Generation: 1},
		State:        AttachmentReady, CreatedAt: time.Now().UTC(),
	}
	if err := attachment.Validate(); err != nil {
		t.Fatalf("valid attachment: %v", err)
	}
	encoded, err := json.Marshal(attachment.Summary())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{canonical, "root-handle-fixture", `"device"`, `"inode"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("public summary leaked %q: %s", forbidden, encoded)
		}
	}
}
