package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
)

func TestBrokerRequestTimeoutIsExtendedOnlyForBoundedHostAppIdentityWork(t *testing.T) {
	if got := brokerRequestTimeout(cmdproxy.ActionHostAppOpenResource); got != 20*time.Second {
		t.Fatalf("host-app timeout=%s", got)
	}
	for _, action := range []string{"", cmdproxy.ActionHostOpen, cmdproxy.ActionCommandAdapter} {
		if got := brokerRequestTimeout(action); got != 5*time.Second {
			t.Fatalf("action %q timeout=%s", action, got)
		}
	}
}

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

func TestNormalizeInvocationRejectsUnknownActionAndMissingBindingWithoutFallback(t *testing.T) {
	t.Setenv("HIDEOUT_COMMAND_PROXY_ACTION", "host.exec")
	if _, err := normalizeInvocation("code", []string{"."}, "/workspace"); err == nil {
		t.Fatal("unknown projected action must not fall back to host.open")
	}
	t.Setenv("HIDEOUT_COMMAND_PROXY_ACTION", cmdproxy.ActionHostAppOpenResource)
	if _, err := normalizeInvocation("code", []string{"."}, "/workspace"); err == nil {
		t.Fatal("host-app projection without immutable binding metadata must fail closed")
	}
}

func TestExplicitProjectionMetadataDoesNotDependOnActionEnv(t *testing.T) {
	t.Setenv("HIDEOUT_COMMAND_PROXY_ACTION", "")
	setProjectionBindingEnv(t)
	action, adapterID, args, err := parseInvocationMetadata([]string{"--action", cmdproxy.ActionHostAppOpenResource, "code", "-g", "src/a.go:12:3"})
	if err != nil {
		t.Fatal(err)
	}
	if action != cmdproxy.ActionHostAppOpenResource || adapterID != "" {
		t.Fatalf("metadata action=%q adapter=%q", action, adapterID)
	}
	command, commandArgs, err := cmdproxy.ResolveHostOpenInvocation("hideout-shim", args)
	if err != nil {
		t.Fatal(err)
	}
	req, err := normalizeInvocationForAction(command, commandArgs, "/workspace", action, adapterID)
	if err != nil {
		t.Fatal(err)
	}
	if req.Action != cmdproxy.ActionHostAppOpenResource || req.Payload["intent"] == nil {
		t.Fatalf("explicit projection normalization mismatch: %+v", req)
	}
}

func setProjectionBindingEnv(t *testing.T) {
	t.Helper()
	grammar := map[string]any{
		"kind": "open-resource-v1", "resourceCount": 1,
		"gotoFlags": []string{"-g", "--goto"}, "newWindowFlags": []string{"-n", "--new-window"},
		"reuseWindowFlags": []string{"-r", "--reuse-window"}, "unknownFlags": "deny",
	}
	raw, err := json.Marshal(grammar)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_COMMAND_PROXY_GRAMMAR_B64", base64.StdEncoding.EncodeToString(raw))
	t.Setenv("HIDEOUT_COMMAND_PROXY_BINDING_DIGEST", "sha256:"+strings.Repeat("a", 64))
}

func TestExplicitUnknownActionFailsClosed(t *testing.T) {
	action, adapterID, args, err := parseInvocationMetadata([]string{"--action", "host.exec", "code", "."})
	if err != nil {
		t.Fatal(err)
	}
	command, commandArgs, err := cmdproxy.ResolveHostOpenInvocation("hideout-shim", args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeInvocationForAction(command, commandArgs, "/workspace", action, adapterID); err == nil {
		t.Fatal("unknown explicit action must not fall back")
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
