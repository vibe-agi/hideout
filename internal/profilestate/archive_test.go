package profilestate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMigrationProfileStateCaptureAndMaterializePreservesApplicationStateOnly(t *testing.T) {
	source := t.TempDir()
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range IncludedRoots() {
		if err := os.MkdirAll(filepath.Join(source, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteProfileStateFile(t, source, "home/.claude/history.jsonl", []byte("history-v1\n"), 0o600)
	mustWriteProfileStateFile(t, source, "config/claude/settings.json", []byte("{\"theme\":\"dark\"}\n"), 0o640)
	mustWriteProfileStateFile(t, source, "data/claude/session.db", []byte("session-v1\x00bytes"), 0o600)
	mustWriteProfileStateFile(t, source, "browser/auth/session", []byte("browser-token-v1"), 0o600)
	mustWriteProfileStateFile(t, source, "cache/must-not-migrate", []byte("cache-v1"), 0o600)
	mustWriteProfileStateFile(t, source, "machine/machine-id", []byte("source-machine-id\n"), 0o600)
	mustWriteProfileStateFile(t, source, "home/.gitconfig", []byte("source-generated-git\n"), 0o600)
	if err := os.Symlink(".claude/history.jsonl", filepath.Join(source, "home/history-link")); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LogicalBytes() == 0 || snapshot.EntryCount() < 8 || snapshot.Digest() == "" {
		t.Fatalf("incomplete snapshot facts: bytes=%d entries=%d digest=%q", snapshot.LogicalBytes(), snapshot.EntryCount(), snapshot.Digest())
	}

	profilesRoot := filepath.Join(t.TempDir(), "profiles")
	owner := Owner{
		OperationID:   "op_profile_state_roundtrip",
		ProfileName:   "imported",
		ComponentID:   "profilestate_roundtrip_component",
		ContentDigest: snapshot.Digest(),
		LogicalBytes:  snapshot.LogicalBytes(),
	}
	materializer, err := NewMaterializer(profilesRoot, owner)
	if err != nil {
		t.Fatal(err)
	}
	err = snapshot.Write(context.Background(), 7, func(chunk []byte) error {
		// Feed deliberately fragmented authenticated plaintext to prove that the
		// decoder does not depend on bundle record boundaries.
		for len(chunk) > 0 {
			n := 3
			if len(chunk) < n {
				n = len(chunk)
			}
			if err := materializer.Consume(chunk[:n]); err != nil {
				return err
			}
			chunk = chunk[n:]
		}
		return nil
	})
	if err != nil {
		t.Fatalf("write failed after %q (%d/%d): %v", materializer.previousPath, materializer.entriesDone, materializer.expectedEntries, err)
	}
	if err := materializer.Finish(); err != nil {
		t.Fatalf("finish failed after %q (%d/%d): %v", materializer.previousPath, materializer.entriesDone, materializer.expectedEntries, err)
	}
	stage := materializer.Path()
	if err := VerifyStage(stage, owner); err != nil {
		t.Fatal(err)
	}

	assertProfileStateFile(t, stage, "home/.claude/history.jsonl", []byte("history-v1\n"), 0o600)
	assertProfileStateFile(t, stage, "config/claude/settings.json", []byte("{\"theme\":\"dark\"}\n"), 0o640)
	assertProfileStateFile(t, stage, "data/claude/session.db", []byte("session-v1\x00bytes"), 0o600)
	assertProfileStateFile(t, stage, "browser/auth/session", []byte("browser-token-v1"), 0o600)
	target, err := os.Readlink(filepath.Join(stage, "home/history-link"))
	if err != nil || target != ".claude/history.jsonl" {
		t.Fatalf("symlink = %q, %v", target, err)
	}
	for _, excluded := range []string{"cache", "machine", "home/.gitconfig"} {
		if _, err := os.Lstat(filepath.Join(stage, excluded)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("excluded %s exists or failed unexpectedly: %v", excluded, err)
		}
	}
}

func TestMigrationProfileStateCaptureRejectsEscapingSymlinkHardlinkAndSpecialFile(t *testing.T) {
	t.Run("escaping-symlink", func(t *testing.T) {
		root := minimalProfileStateRoot(t)
		if err := os.Symlink("../../outside", filepath.Join(root, "home/escape")); err != nil {
			t.Fatal(err)
		}
		if _, err := Capture(root); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Capture error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("hardlink", func(t *testing.T) {
		root := minimalProfileStateRoot(t)
		mustWriteProfileStateFile(t, root, "home/value", []byte("value"), 0o600)
		if err := os.Link(filepath.Join(root, "home/value"), filepath.Join(root, "data/alias")); err != nil {
			t.Fatal(err)
		}
		if _, err := Capture(root); !errors.Is(err, ErrAliasedFile) {
			t.Fatalf("Capture error = %v, want ErrAliasedFile", err)
		}
	})

	t.Run("special-file", func(t *testing.T) {
		root := minimalProfileStateRoot(t)
		if err := syscall.Mkfifo(filepath.Join(root, "home/fifo"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Capture(root); !errors.Is(err, ErrSpecialFile) {
			t.Fatalf("Capture error = %v, want ErrSpecialFile", err)
		}
	})
}

func TestMigrationProfileStateSnapshotWriteFailsClosedAfterSourceMutation(t *testing.T) {
	root := minimalProfileStateRoot(t)
	mustWriteProfileStateFile(t, root, "home/history", []byte("before"), 0o600)
	snapshot, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteProfileStateFile(t, root, "home/history", []byte("after"), 0o600)
	var encryptedInput bytes.Buffer
	err = snapshot.Write(context.Background(), 64, func(chunk []byte) error {
		_, writeErr := encryptedInput.Write(chunk)
		return writeErr
	})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("Write error = %v, want ErrSourceChanged", err)
	}
	if encryptedInput.Len() >= int(snapshot.LogicalBytes()) {
		t.Fatalf("changed source produced a complete plaintext stream: %d", encryptedInput.Len())
	}
}

func TestMigrationProfileStateMaterializerRejectsSubstitutionAndRemovesOnlyExactOwner(t *testing.T) {
	root := minimalProfileStateRoot(t)
	mustWriteProfileStateFile(t, root, "home/value", []byte("value"), 0o600)
	snapshot, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	profilesRoot := filepath.Join(t.TempDir(), "profiles")
	owner := Owner{
		OperationID:   "op_profile_state_owner",
		ProfileName:   "destination",
		ComponentID:   "profilestate_owner_component",
		ContentDigest: snapshot.Digest(),
		LogicalBytes:  snapshot.LogicalBytes(),
	}
	materializer, err := NewMaterializer(profilesRoot, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Write(context.Background(), 64, materializer.Consume); err != nil {
		t.Fatal(err)
	}
	if err := materializer.Finish(); err != nil {
		t.Fatal(err)
	}
	stage := materializer.Path()
	wrong := owner
	wrong.OperationID = "op_profile_state_substituted"
	if err := RemoveStage(stage, wrong); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("RemoveStage wrong owner = %v, want ErrOwnerMismatch", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("wrong-owner cleanup changed stage: %v", err)
	}
	if err := RemoveStage(stage, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact-owner stage remains: %v", err)
	}
}

func minimalProfileStateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range IncludedRoots() {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustWriteProfileStateFile(t *testing.T, root, relative string, data []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertProfileStateFile(t *testing.T, root, relative string, want []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", relative, got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode.Perm() {
		t.Fatalf("%s mode = %04o, want %04o", relative, info.Mode().Perm(), mode.Perm())
	}
}
