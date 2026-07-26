//go:build darwin || linux

package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

const nativeTestSessionSnapshotID = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestRunWithStreamsPreservesPipeOutputAndExit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var ready atomic.Bool
	session := &backend.Session{ID: "ses_native_pipe", EnvironmentID: "env_native_pipe", SessionSnapshotID: nativeTestSessionSnapshotID, InstanceName: "native-pipe", Backend: "native", HostWork: t.TempDir()}
	err := (Backend{AllowWeakIsolation: true}).RunWithStreams(
		context.Background(), session,
		[]string{"sh", "-c", "printf 'out\\000bytes'; printf err >&2; exit 17"}, testNativeEnv(),
		backend.RunStreams{
			Stdout: &stdout, Stderr: &stderr,
			Ready: func(got backend.SessionReadyProof) error {
				if err := got.ValidateSession(session, false); err != nil {
					t.Fatal(err)
				}
				ready.Store(true)
				return nil
			},
		},
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 17 {
		t.Fatalf("error=%T %v", err, err)
	}
	if !ready.Load() || stdout.String() != "out\x00bytes" || stderr.String() != "err" {
		t.Fatalf("ready=%v stdout=%q stderr=%q", ready.Load(), stdout.String(), stderr.String())
	}
}

func TestRunWithStreamsDoesNotWaitForClientStdinAfterTargetExit(t *testing.T) {
	stdin, keepOpen := io.Pipe()
	defer stdin.Close()
	defer keepOpen.Close()
	done := make(chan error, 1)
	session := &backend.Session{ID: "ses_native_open_stdin", EnvironmentID: "env_native_open_stdin", SessionSnapshotID: nativeTestSessionSnapshotID, InstanceName: "native-open-stdin", Backend: "native", HostWork: t.TempDir()}
	go func() {
		done <- (Backend{AllowWeakIsolation: true}).RunWithStreams(
			context.Background(), session, []string{"sh", "-c", "exit 0"}, testNativeEnv(),
			backend.RunStreams{Stdin: stdin, Ready: func(backend.SessionReadyProof) error { return nil }},
		)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("target exit waited for client stdin EOF")
	}
}

func TestPreparePreservesDaemonSessionIdentity(t *testing.T) {
	root := t.TempDir()
	session, err := (Backend{AllowWeakIsolation: true}).Prepare(context.Background(), backend.RunSpec{
		Machine: backend.MachineActivationSpec{
			EnvironmentID: "env_native_identity", ImageRef: environment.BuiltinBaseImage,
			Profile: profile.Default("native"), ProfileDir: root,
			InstanceName: "native-instance", PreserveInstance: true, Mode: environment.ModeWorkspaceBound,
		},
		Workspace: backend.WorkspaceAttachmentSpec{HostRoot: root, GuestRoot: "/workspace", Transport: backend.WorkspaceTransportStatic},
		SessionID: "ses_native_identity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "ses_native_identity" || session.EnvironmentID != "env_native_identity" {
		t.Fatalf("session identity=%q environment=%q", session.ID, session.EnvironmentID)
	}
	if session.InstanceName != "native-instance" || !session.PreserveInstance {
		t.Fatalf("instance=%q preserve=%v", session.InstanceName, session.PreserveInstance)
	}
}

func TestRunWithStreamsAllocatesPTYWithInitialSize(t *testing.T) {
	var terminal bytes.Buffer
	session := &backend.Session{ID: "ses_native_pty", EnvironmentID: "env_native_pty", SessionSnapshotID: nativeTestSessionSnapshotID, InstanceName: "native-pty", Backend: "native", HostWork: t.TempDir()}
	err := (Backend{AllowWeakIsolation: true}).RunWithStreams(
		context.Background(), session, []string{"sh", "-c", "stty size"}, testNativeEnv(),
		backend.RunStreams{
			Terminal: true, Rows: 29, Columns: 103, Term: "xterm-256color", PTY: &terminal,
			Ready: func(backend.SessionReadyProof) error { return nil },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(terminal.String()); got != "29 103" {
		t.Fatalf("stty size=%q", got)
	}
}

func TestNormalizePTYCopyErrorAcceptsDeliberateClose(t *testing.T) {
	err := &os.PathError{Op: "read", Path: "/dev/ptmx", Err: os.ErrClosed}
	if got := normalizePTYCopyError(err); got != nil {
		t.Fatalf("deliberate PTY close error=%v", got)
	}

	unexpected := errors.New("unexpected PTY read failure")
	if got := normalizePTYCopyError(unexpected); !errors.Is(got, unexpected) {
		t.Fatalf("unexpected PTY error was not preserved: %v", got)
	}
}

func testNativeEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"TERM=xterm-256color",
	}
}
