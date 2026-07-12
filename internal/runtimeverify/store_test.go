package runtimeverify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreWritesAtomicHostOnlyReceiptOutsideGuestRuntime(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	receipt := validReceipt()
	if err := store.Write(receipt); err != nil {
		t.Fatalf("Write: %v", err)
	}
	path := store.Path(receipt.EnvironmentID)
	if strings.Contains(path, string(filepath.Separator)+"runtime"+string(filepath.Separator)) {
		t.Fatalf("receipt path is under guest-mounted runtime: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode=%#o want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary receipt remains: %v", err)
	}
	loaded, err := store.Load(receipt.EnvironmentID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.EnvironmentID != receipt.EnvironmentID || loaded.Status != receipt.Status {
		t.Fatalf("loaded receipt=%+v", loaded)
	}
}

func TestStoreRejectsInvalidEnvironmentAndSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	receipt := validReceipt()
	receipt.EnvironmentID = "../escape"
	if err := store.Write(receipt); err == nil {
		t.Fatal("path traversal environment id should fail")
	}

	receipt = validReceipt()
	outside := t.TempDir()
	environments := filepath.Join(root, "environments")
	if err := os.MkdirAll(environments, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(environments, receipt.EnvironmentID)); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(receipt); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked environment dir should fail, got %v", err)
	}
}
