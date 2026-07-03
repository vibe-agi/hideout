package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestRunExecutesCommandResolvedFromTargetEnvPath(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "hideout-target-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf target-path\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	b := Backend{AllowWeakIsolation: true, Stdout: &out, Stderr: io.Discard}
	if err := b.Run(context.Background(), &backend.Session{HostWork: t.TempDir()}, []string{"hideout-target-tool"}, []string{"PATH=" + dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "target-path" {
		t.Fatalf("stdout=%q", out.String())
	}
}

func TestRunReportsMissingCommandWithBackendContext(t *testing.T) {
	b := Backend{AllowWeakIsolation: true, Stdout: io.Discard, Stderr: io.Discard}
	err := b.Run(context.Background(), &backend.Session{HostWork: t.TempDir()}, []string{"hideout-missing-command"}, []string{"PATH=" + t.TempDir()})
	var notFound backend.CommandNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected CommandNotFoundError, got %T %v", err, err)
	}
	if notFound.Backend != "native" || notFound.Command != "hideout-missing-command" {
		t.Fatalf("unexpected error context: %+v", notFound)
	}
}

func TestRunDoesNotFallbackToHostPATH(t *testing.T) {
	hostDir := t.TempDir()
	targetDir := t.TempDir()
	tool := filepath.Join(hostDir, "hideout-host-only-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", hostDir)
	b := Backend{AllowWeakIsolation: true, Stdout: io.Discard, Stderr: io.Discard}
	err := b.Run(context.Background(), &backend.Session{HostWork: t.TempDir()}, []string{"hideout-host-only-tool"}, []string{"PATH=" + targetDir})
	var notFound backend.CommandNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected CommandNotFoundError, got %T %v", err, err)
	}
}
