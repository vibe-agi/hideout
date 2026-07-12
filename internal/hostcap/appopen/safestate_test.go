package appopen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSafeStateWritesReviewedConfigurationAtomically(t *testing.T) {
	root := t.TempDir()
	spec := LaunchSpec{SafeConfiguration: &SafeConfigurationSpec{
		RelativePath: "User/settings.json",
		Values:       map[string]any{"task.auto": "off", "trust.enabled": true},
	}}
	if err := PrepareSafeState(spec, root); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "User", "settings.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"task.auto": "off"`) || !strings.Contains(string(data), `"trust.enabled": true`) {
		t.Fatalf("safe settings mismatch: %s", data)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("safe settings info=%v err=%v", info, err)
	}
}

func TestPrepareSafeStateRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "User")); err != nil {
		t.Fatal(err)
	}
	spec := LaunchSpec{SafeConfiguration: &SafeConfigurationSpec{RelativePath: "User/settings.json", Values: map[string]any{"safe": true}}}
	if err := PrepareSafeState(spec, root); err == nil {
		t.Fatal("safe state must not follow a symlinked parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("outside path was touched: %v", err)
	}
}
