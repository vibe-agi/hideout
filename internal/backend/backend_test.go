package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookPathInEnvUsesTargetPath(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := LookPathInEnv("tool", []string{"PATH=" + dir})
	if err != nil {
		t.Fatalf("LookPathInEnv: %v", err)
	}
	if got != tool {
		t.Fatalf("path=%s want %s", got, tool)
	}
}

func TestCommandNotFoundErrorIncludesBackendContext(t *testing.T) {
	err := CommandNotFoundError{
		Backend:   "lima",
		Command:   "missing-tool",
		Path:      "/hideout/session/shims:/usr/bin",
		Workspace: "/workspace",
		Hint:      "no host fallback was attempted",
	}
	text := err.Error()
	for _, want := range []string{"missing-tool", "lima backend PATH", "/hideout/session/shims:/usr/bin", "/workspace", "no host fallback"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error missing %q: %s", want, text)
		}
	}
}
