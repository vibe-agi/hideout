package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAllocateHasNoFilesystemSideEffectsUntilPrepare(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	layout, err := Allocate(root)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !ValidID(layout.ID) || layout.Root != root || layout.Dir != filepath.Join(root, "sessions", layout.ID) {
		t.Fatalf("unexpected allocated layout: %+v", layout)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("allocation published filesystem state: %v", err)
	}

	if err := Prepare(layout); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, path := range []string{layout.TmpDir, layout.ShimDir, layout.HostFSReadDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat prepared path %s: %v", path, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("prepared path %s mode=%v", path, info.Mode())
		}
	}
}

func TestPrepareRejectsInvalidOrAlreadyMaterializedLayout(t *testing.T) {
	if err := Prepare(Layout{}); err == nil {
		t.Fatal("empty layout should fail closed")
	}
	layout, err := Allocate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Prepare(layout); err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	if err := Prepare(layout); err == nil {
		t.Fatal("second Prepare should refuse an existing layout")
	}
}

func TestNewCreatesPrivateHostFSReadStateLayout(t *testing.T) {
	layout, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if layout.HostFSReadDir != filepath.Join(layout.Dir, "hostfs-read") ||
		layout.HostFSReadOwnerLock != filepath.Join(layout.HostFSReadDir, "owner.lock") ||
		layout.HostFSReadProviderLock != filepath.Join(layout.HostFSReadDir, "provider.lock") ||
		layout.HostFSReadStatePath != filepath.Join(layout.HostFSReadDir, "state.json") ||
		layout.HostFSReadGrantsPath != filepath.Join(layout.HostFSReadDir, "grants.json") {
		t.Fatalf("unexpected HostFS read layout: %+v", layout)
	}
	info, err := os.Stat(layout.HostFSReadDir)
	if err != nil {
		t.Fatalf("stat HostFS read dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("HostFS read dir mode=%#o want 0700", info.Mode().Perm())
	}
}

func TestCleanupEphemeralRemovesSensitiveSessionStateAndKeepsAudit(t *testing.T) {
	root := t.TempDir()
	sessionID := "ses_test"
	dir := filepath.Join(root, "sessions", sessionID)
	mustWrite(t, filepath.Join(dir, "tmp", "file"), "tmp")
	mustWrite(t, filepath.Join(dir, "shims", "open"), "shim")
	mustWrite(t, filepath.Join(dir, "bootstrap", "bootstrap.sh"), "#!/bin/sh")
	mustWrite(t, filepath.Join(dir, "identity", "home", ".gitconfig"), "[user]\n")
	mustWrite(t, filepath.Join(dir, "broker.sock"), "sock")
	mustWrite(t, filepath.Join(dir, "broker-endpoint.json"), "{}")
	mustWrite(t, filepath.Join(dir, "network-plan.json"), "{}")
	mustWrite(t, filepath.Join(dir, "network", "bootstrap.sh"), "#!/bin/sh")
	mustWrite(t, filepath.Join(dir, "network", "proxy.url"), "socks5://user:pass@127.0.0.1:1080")
	mustWrite(t, filepath.Join(dir, "hostfs-read", "grants.json"), "{}")
	mustWrite(t, filepath.Join(dir, "hostfs-read", "state.json"), "{}")
	mustWrite(t, filepath.Join(dir, "hostfs-overlay", "objects", "hfwobj_test"), "payload")
	mustWrite(t, filepath.Join(dir, "audit.jsonl"), "{}\n")

	result, err := CleanupEphemeral(root, "", false)
	if err != nil {
		t.Fatalf("CleanupEphemeral: %v", err)
	}
	if result.Sessions != 1 {
		t.Fatalf("sessions=%d want 1", result.Sessions)
	}
	for _, path := range []string{
		filepath.Join(dir, "tmp"),
		filepath.Join(dir, "shims"),
		filepath.Join(dir, "bootstrap"),
		filepath.Join(dir, "identity"),
		filepath.Join(dir, "broker.sock"),
		filepath.Join(dir, "broker-endpoint.json"),
		filepath.Join(dir, "network-plan.json"),
		filepath.Join(dir, "network"),
		filepath.Join(dir, "hostfs-read"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed; err=%v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "hostfs-overlay", "objects", "hfwobj_test")); err != nil {
		t.Fatalf("pending HostFS overlay object should be kept until terminal apply/discard/timeout cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.jsonl")); err != nil {
		t.Fatalf("audit should be kept: %v", err)
	}
}

func TestCleanupEphemeralDryRunDoesNotRemoveFiles(t *testing.T) {
	root := t.TempDir()
	sessionID := "ses_test"
	path := filepath.Join(root, "sessions", sessionID, "network", "proxy.url")
	mustWrite(t, path, "socks5://proxy")

	result, err := CleanupEphemeral(root, sessionID, true)
	if err != nil {
		t.Fatalf("CleanupEphemeral: %v", err)
	}
	if result.Sessions != 1 || len(result.Removed) == 0 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry-run should keep proxy file: %v", err)
	}
}

func TestCleanupEphemeralRemovesShortBrokerSocket(t *testing.T) {
	root := t.TempDir()
	sessionID := "ses_short_socket"
	sessionDir := filepath.Join(root, "sessions", sessionID)
	shortSock := filepath.Join(shortSocketDir(), "hideout-"+sessionID+".sock")
	mustWrite(t, filepath.Join(sessionDir, "audit.jsonl"), "{}\n")
	mustWrite(t, shortSock, "socket")
	t.Cleanup(func() { _ = os.Remove(shortSock) })

	result, err := CleanupEphemeral(root, sessionID, false)
	if err != nil {
		t.Fatalf("CleanupEphemeral: %v", err)
	}
	if result.Sessions != 1 {
		t.Fatalf("sessions=%d want 1", result.Sessions)
	}
	if _, err := os.Stat(shortSock); !os.IsNotExist(err) {
		t.Fatalf("short broker socket should be removed; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "audit.jsonl")); err != nil {
		t.Fatalf("audit should be kept: %v", err)
	}
}

func TestCleanupEphemeralSessionFilter(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "sessions", "ses_first", "network", "proxy.url")
	second := filepath.Join(root, "sessions", "ses_second", "network", "proxy.url")
	mustWrite(t, first, "socks5://first")
	mustWrite(t, second, "socks5://second")

	result, err := CleanupEphemeral(root, "ses_first", false)
	if err != nil {
		t.Fatalf("CleanupEphemeral: %v", err)
	}
	if result.Sessions != 1 {
		t.Fatalf("sessions=%d want 1", result.Sessions)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first session should be cleaned; err=%v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("second session should be kept: %v", err)
	}
}

func TestCleanupEphemeralRejectsInvalidSessionID(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"test", " ses_test", "ses_test ", ".", "..", "ses_bad/path"} {
		t.Run(id, func(t *testing.T) {
			if _, err := CleanupEphemeral(root, id, false); err == nil {
				t.Fatal("expected invalid session id to fail")
			}
		})
	}
}

func TestCleanupEphemeralSkipsNonSessionDirectories(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "sessions", "ses_valid", "network", "proxy.url")
	other := filepath.Join(root, "sessions", "not-a-session", "network", "proxy.url")
	mustWrite(t, valid, "socks5://valid")
	mustWrite(t, other, "socks5://other")

	result, err := CleanupEphemeral(root, "", false)
	if err != nil {
		t.Fatalf("CleanupEphemeral: %v", err)
	}
	if result.Sessions != 1 {
		t.Fatalf("sessions=%d want 1", result.Sessions)
	}
	if _, err := os.Stat(valid); !os.IsNotExist(err) {
		t.Fatalf("valid session should be cleaned; err=%v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-session directory should be kept: %v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
