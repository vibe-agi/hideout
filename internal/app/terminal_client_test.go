//go:build darwin || linux

package app

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	"golang.org/x/term"
)

func TestRunClientTerminalAutoRawResizeAndRestore(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(slave, &pty.Winsize{Rows: 31, Cols: 97}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	client, err := newRunClientTerminal("auto", slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	if client.Descriptor.Mode != "pty" || client.Descriptor.Rows != 31 || client.Descriptor.Columns != 97 {
		t.Fatalf("descriptor=%+v", client.Descriptor)
	}
	if err := client.Activate(sessionwire.Started{}); err != nil {
		t.Fatal(err)
	}
	raw, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, raw) {
		t.Fatal("terminal state did not enter raw mode")
	}
	if err := pty.Setsize(slave, &pty.Winsize{Rows: 43, Cols: 121}); err != nil {
		t.Fatal(err)
	}
	client.resizeSignals <- syscall.SIGWINCH
	select {
	case resize := <-client.Controls:
		if resize.Kind != backend.RunControlResize || resize.Rows != 43 || resize.Columns != 121 {
			t.Fatalf("resize=%+v", resize)
		}
	case <-time.After(time.Second):
		t.Fatal("SIGWINCH did not produce a resize control")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored")
	}
}

func TestRunClientTerminalModesFailClosed(t *testing.T) {
	var input, output bytes.Buffer
	if _, err := newRunClientTerminal("always", &input, &output); err == nil {
		t.Fatal("--terminal always accepted non-terminal streams")
	}
	client, err := newRunClientTerminal("never", &input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if client.Descriptor.Mode != "none" || client.Controls != nil {
		t.Fatalf("never mode=%+v", client)
	}
	if _, err := newRunClientTerminal("sometimes", &input, &output); err == nil {
		t.Fatal("unknown terminal mode succeeded")
	}
}

func TestExecutableRunUsesDaemonClientAndPreservesExitCode(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(workspace)

	oldEnsure, oldSession, oldExecutable := ensureRunDaemon, runDaemonSession, runExecutable
	t.Cleanup(func() {
		ensureRunDaemon, runDaemonSession, runExecutable = oldEnsure, oldSession, oldExecutable
	})
	ensureCalls := 0
	sessionCalls := 0
	ensureRunDaemon = func(_ context.Context, opts daemon.EnsureStartedOptions) (daemon.Status, error) {
		ensureCalls++
		if opts.Store.Root != filepath.Join(home, ".hideout") || opts.Executable != "/test/hideout" {
			t.Fatalf("ensure opts=%+v", opts)
		}
		return daemon.Status{}, nil
	}
	runExecutable = func() (string, error) { return "/test/hideout", nil }
	runDaemonSession = func(_ context.Context, opts daemon.SessionClientOptions) (daemon.SessionClientResult, error) {
		sessionCalls++
		if opts.Request.Workspace != workspace || opts.Request.Terminal.Mode != "none" || opts.Request.Command[0] != "tool" {
			t.Fatalf("request=%+v", opts.Request)
		}
		completion := sessionwire.Completion{
			Kind: sessionwire.CompletionExit, ExitCode: 23, TargetCompleted: true,
			CleanupCompleted: true, SessionID: "ses_20260716T120000Z_abcdef0123456789",
		}
		return daemon.SessionClientResult{Completion: completion}, daemon.RemoteExitError{Completion: completion}
	}
	var stdout, stderr bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--", "tool"}, &stdout, &stderr)
	if code != 23 || ensureCalls != 1 || sessionCalls != 1 {
		t.Fatalf("code=%d ensure=%d session=%d stderr=%q", code, ensureCalls, sessionCalls, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("ordinary target exit printed wrapper diagnostics: %q", stderr.String())
	}
}

func TestExecutableRunHasNoEmbeddedFallbackAfterDaemonFailure(t *testing.T) {
	oldEnsure, oldSession, oldExecutable := ensureRunDaemon, runDaemonSession, runExecutable
	t.Cleanup(func() {
		ensureRunDaemon, runDaemonSession, runExecutable = oldEnsure, oldSession, oldExecutable
	})
	ensureRunDaemon = func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
		return daemon.Status{}, errors.New("authenticated readiness failed")
	}
	runExecutable = func() (string, error) { return "/test/hideout", nil }
	runDaemonSession = func(context.Context, daemon.SessionClientOptions) (daemon.SessionClientResult, error) {
		t.Fatal("session client ran after daemon readiness failure")
		return daemon.SessionClientResult{}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--", "true"}, &stdout, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("authenticated readiness failed")) {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
