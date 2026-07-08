package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/broker"
)

func TestRunAcceptsConfiguredCommandNameUntilBrokerBoundary(t *testing.T) {
	withArgs(t, []string{filepath.Join(t.TempDir(), "browser-open"), "https://example.com"})
	withWorkingDir(t, t.TempDir())
	clearBrokerEnv(t)

	if got := run(); got != 69 {
		t.Fatalf("run exit=%d want missing-broker boundary 69", got)
	}
}

func TestWrapperModeRejectsMissingBrokerEnv(t *testing.T) {
	withArgs(t, []string{"hideout-shim", "open", "https://example.com"})
	withWorkingDir(t, t.TempDir())
	clearBrokerEnv(t)

	if code := run(); code != 69 {
		t.Fatalf("expected broker env failure exit 69, got %d", code)
	}
}

func TestRunRejectsInvalidOpenShapeBeforeBroker(t *testing.T) {
	withArgs(t, []string{filepath.Join(t.TempDir(), "browser-open"), "https://example.com", "--extra"})
	withWorkingDir(t, t.TempDir())
	clearBrokerEnv(t)

	if got := run(); got != 2 {
		t.Fatalf("run exit=%d want local normalization failure 2", got)
	}
}

func TestRewriteCommandPathExcludesShimDirectory(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	for dir := range map[string]bool{shimDir: true, realDir: true} {
		path := filepath.Join(dir, "tool-real")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rewriteCommandPath("tool-real", shimDir, shimDir+string(os.PathListSeparator)+realDir)
	if err != nil {
		t.Fatalf("rewriteCommandPath: %v", err)
	}
	if got != filepath.Join(realDir, "tool-real") {
		t.Fatalf("rewrite path=%q want real dir", got)
	}
	if _, err := rewriteCommandPath("tool-real", shimDir, shimDir); err == nil {
		t.Fatal("expected missing real command outside shim dir to fail")
	}
}

func TestBrokerEndpointFromEnvPrefersEndpoint(t *testing.T) {
	t.Setenv(broker.EnvEndpoint, "tcp://127.0.0.1:1234")
	t.Setenv(broker.EnvSock, "/tmp/ignored.sock")
	got, err := brokerEndpointFromEnv()
	if err != nil {
		t.Fatalf("brokerEndpointFromEnv: %v", err)
	}
	if got.Network != broker.EndpointTCP || got.Address != "127.0.0.1:1234" {
		t.Fatalf("endpoint=%+v", got)
	}
}

func TestBrokerEndpointFromEnvFallsBackToSock(t *testing.T) {
	t.Setenv(broker.EnvEndpoint, "")
	t.Setenv(broker.EnvSock, "/tmp/hideout.sock")
	got, err := brokerEndpointFromEnv()
	if err != nil {
		t.Fatalf("brokerEndpointFromEnv: %v", err)
	}
	if got.Network != broker.EndpointUnix || got.Address != "/tmp/hideout.sock" {
		t.Fatalf("endpoint=%+v", got)
	}
}

func clearBrokerEnv(t *testing.T) {
	t.Helper()
	t.Setenv(broker.EnvEndpoint, "")
	t.Setenv(broker.EnvSock, "")
	t.Setenv(broker.EnvSession, "")
	t.Setenv(broker.EnvToken, "")
}

func withArgs(t *testing.T, args []string) {
	t.Helper()
	oldArgs := os.Args
	os.Args = append([]string(nil), args...)
	t.Cleanup(func() { os.Args = oldArgs })
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}
