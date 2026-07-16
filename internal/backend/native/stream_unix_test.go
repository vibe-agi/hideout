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
)

func TestRunWithStreamsPreservesPipeOutputAndExit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var ready atomic.Bool
	session := &backend.Session{ID: "ses_native_pipe", Backend: "native", HostWork: t.TempDir()}
	err := (Backend{AllowWeakIsolation: true}).RunWithStreams(
		context.Background(), session,
		[]string{"sh", "-c", "printf 'out\\000bytes'; printf err >&2; exit 17"}, testNativeEnv(),
		backend.RunStreams{
			Stdout: &stdout, Stderr: &stderr,
			Ready: func(got *backend.Session) error {
				if got != session {
					t.Fatal("ready changed session")
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
	session := &backend.Session{ID: "ses_native_open_stdin", Backend: "native", HostWork: t.TempDir()}
	go func() {
		done <- (Backend{AllowWeakIsolation: true}).RunWithStreams(
			context.Background(), session, []string{"sh", "-c", "exit 0"}, testNativeEnv(),
			backend.RunStreams{Stdin: stdin, Ready: func(*backend.Session) error { return nil }},
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
	session, err := (Backend{AllowWeakIsolation: true}).Prepare(context.Background(), backend.RunSpec{
		SessionID: "ses_native_identity", EnvironmentID: "env_native_identity",
		HostWork: t.TempDir(), GuestWork: "/workspace", InstanceName: "native-instance",
		PreserveInstance: true,
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
	session := &backend.Session{ID: "ses_native_pty", Backend: "native", HostWork: t.TempDir()}
	err := (Backend{AllowWeakIsolation: true}).RunWithStreams(
		context.Background(), session, []string{"sh", "-c", "stty size"}, testNativeEnv(),
		backend.RunStreams{
			Terminal: true, Rows: 29, Columns: 103, Term: "xterm-256color", PTY: &terminal,
			Ready: func(*backend.Session) error { return nil },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(terminal.String()); got != "29 103" {
		t.Fatalf("stty size=%q", got)
	}
}

func testNativeEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"TERM=xterm-256color",
	}
}
