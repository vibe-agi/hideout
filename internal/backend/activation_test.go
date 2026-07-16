package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActivationReceiptRoundTripAndIdentityDrift(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "lima.yaml")
	if err := os.WriteFile(config, []byte("vmType: vz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := activationTestSession(root, config)
	receipt, err := BuildActivationReceipt(session, "01234567-89ab-cdef-0123-456789abcdef", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteActivationReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadActivationReceipt(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != receipt || loaded.OwnerSessionID != session.ID {
		t.Fatalf("loaded=%+v want=%+v", loaded, receipt)
	}
	if err := loaded.MatchesSession(session); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("vmType: qemu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loaded.MatchesSession(session); err == nil {
		t.Fatal("activation receipt accepted changed config")
	}
	if err := RemoveActivationReceipt(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, activationReceiptFile)); !os.IsNotExist(err) {
		t.Fatalf("activation receipt remains: %v", err)
	}
}

func TestActivationReceiptRejectsUnknownFieldsAndSymlinkRoots(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "lima.yaml")
	if err := os.WriteFile(config, []byte("vmType: vz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := BuildActivationReceipt(activationTestSession(root, config), "01234567-89ab-cdef-0123-456789abcdef", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteActivationReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, activationReceiptFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "\n}", ",\n  \"unexpected\": true\n}", 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadActivationReceipt(root); err == nil {
		t.Fatal("activation receipt accepted unknown field")
	}
	link := filepath.Join(t.TempDir(), "runtime-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteActivationReceipt(link, receipt); err == nil {
		t.Fatal("activation receipt accepted symlink runtime root")
	}
}

func activationTestSession(root, config string) *Session {
	return &Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", EnvironmentID: "env_0123456789abcdef",
		InstanceName: "hideout-default", ConfigPath: config, RuntimeRoot: root,
		RuntimeContract: &RuntimeContract{ID: "developer-standard/v1", Digest: "sha256:" + strings.Repeat("a", 64)},
		RuntimeInstanceExpected: &RuntimeInstanceExpectation{
			ImageLocation: "https://example.invalid/runtime.qcow2", ImageSHA256: strings.Repeat("b", 64),
			PackageInventorySHA256: strings.Repeat("c", 64), HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64", VMType: "vz",
		},
	}
}
