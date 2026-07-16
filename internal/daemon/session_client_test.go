package daemon

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

type testRemoteExit int

func (e testRemoteExit) Error() string { return "test target exit" }
func (e testRemoteExit) ExitCode() int { return int(e) }

func TestRunSessionClientStreamsAndMapsExactRemoteExit(t *testing.T) {
	server, token, fake, _ := newTestSessionServer(t, false)
	fake.runErr = testRemoteExit(17)
	var stdout, stderr bytes.Buffer
	beforeIO := false
	result, err := RunSessionClient(context.Background(), SessionClientOptions{
		Store: profile.Store{Root: server.core.Store.Root},
		Request: manager.RunServiceRequest{
			Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
			Command: []string{"tool"}, AllowWeakIsolation: true,
			Terminal: manager.TerminalDescriptor{Mode: "none"},
		},
		Stdout: &stdout, Stderr: &stderr,
		BeforeIO: func(sessionwire.Started) error { beforeIO = true; return nil },
		OnStarted: func(sessionwire.Started) {
			if !beforeIO {
				t.Fatal("started callback ran before terminal activation")
			}
		},
		Dial:      sessionPipeDial(server),
		ReadToken: func(string) (string, error) { return token, nil },
	})
	var exitErr RemoteExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 17 {
		t.Fatalf("exit error=%T %v result=%+v", err, err, result)
	}
	if result.Completion.ExitCode != 17 || result.Started.SessionID == "" || result.Review.PlanDigest == "" || result.RunResult.SessionID != result.Started.SessionID {
		t.Fatalf("result=%+v", result)
	}
	if stdout.String() != "stdout\x00bytes" || stderr.String() != "stderr-bytes" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunSessionClientRenewsAcrossCredentialRotation(t *testing.T) {
	server, _, _, _ := newTestSessionServer(t, true)
	server.leaseDuration = 90 * time.Millisecond
	server.renewalInterval = 20 * time.Millisecond
	initial := server.credentials.Token()
	readToken := func(string) (string, error) { return server.credentials.Token(), nil }
	go func() {
		time.Sleep(35 * time.Millisecond)
		_, _ = server.credentials.Rotate()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := RunSessionClient(ctx, SessionClientOptions{
		Store: profile.Store{Root: server.core.Store.Root},
		Request: manager.RunServiceRequest{
			Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
			Command: []string{"tool"}, AllowWeakIsolation: true,
			Terminal: manager.TerminalDescriptor{Mode: "none"},
		},
		Dial: sessionPipeDial(server), ReadToken: func(root string) (string, error) {
			if time.Since(started) < 30*time.Millisecond {
				return initial, nil
			}
			return readToken(root)
		},
	})
	var exitErr RemoteExitError
	if !errors.As(err, &exitErr) || result.Completion.Kind != sessionwire.CompletionCancelled {
		t.Fatalf("renewal result=%+v error=%T %v", result, err, err)
	}
	if elapsed := time.Since(started); elapsed < 170*time.Millisecond {
		t.Fatalf("session expired before client cancellation despite renewal: %s", elapsed)
	}
}

func TestRunSessionClientRenewsBeforeDelayedStart(t *testing.T) {
	server, token, fake, _ := newTestSessionServer(t, false)
	server.leaseDuration = 80 * time.Millisecond
	server.renewalInterval = 20 * time.Millisecond
	fake.readyDelay = 140 * time.Millisecond
	started := time.Now()
	result, err := RunSessionClient(context.Background(), SessionClientOptions{
		Store: profile.Store{Root: server.core.Store.Root},
		Request: manager.RunServiceRequest{
			Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
			Command: []string{"tool"}, AllowWeakIsolation: true,
			Terminal: manager.TerminalDescriptor{Mode: "none"},
		},
		Dial: sessionPipeDial(server), ReadToken: func(string) (string, error) { return token, nil },
	})
	if err != nil {
		t.Fatalf("delayed start should survive lease renewal: result=%+v error=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed < fake.readyDelay {
		t.Fatalf("delayed backend did not exercise pre-start renewal: %s", elapsed)
	}
}

func TestRunSessionClientRenewsAcrossMultipleCredentialRotations(t *testing.T) {
	server, initial, _, _ := newTestSessionServer(t, true)
	server.leaseDuration = 90 * time.Millisecond
	server.renewalInterval = 40 * time.Millisecond
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = server.credentials.Rotate()
		time.Sleep(10 * time.Millisecond)
		_, _ = server.credentials.Rotate()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := RunSessionClient(ctx, SessionClientOptions{
		Store: profile.Store{Root: server.core.Store.Root},
		Request: manager.RunServiceRequest{
			Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
			Command: []string{"tool"}, AllowWeakIsolation: true,
			Terminal: manager.TerminalDescriptor{Mode: "none"},
		},
		Dial: sessionPipeDial(server), ReadToken: func(string) (string, error) {
			if time.Since(started) < 30*time.Millisecond {
				return initial, nil
			}
			return server.credentials.Token(), nil
		},
	})
	var exitErr RemoteExitError
	if !errors.As(err, &exitErr) || result.Completion.Kind != sessionwire.CompletionCancelled {
		t.Fatalf("multi-rotation result=%+v error=%T %v", result, err, err)
	}
	if elapsed := time.Since(started); elapsed < 170*time.Millisecond {
		t.Fatalf("session expired after skipped credential generations: %s", elapsed)
	}
}

func TestRunSessionClientFailsClosedOnAuthRefusal(t *testing.T) {
	server, _, _, _ := newTestSessionServer(t, false)
	_, err := RunSessionClient(context.Background(), SessionClientOptions{
		Store:     profile.Store{Root: server.core.Store.Root},
		Request:   manager.RunServiceRequest{Version: manager.RunServiceRequestVersion},
		Dial:      sessionPipeDial(server),
		ReadToken: func(string) (string, error) { return "wrong-token", nil },
	})
	var remote SessionRemoteError
	if !errors.As(err, &remote) || remote.Code != "session.auth.failed" {
		t.Fatalf("auth error=%T %v", err, err)
	}
}

func sessionPipeDial(server *sessionServer) func(context.Context, string) (net.Conn, error) {
	return func(context.Context, string) (net.Conn, error) {
		serverConn, clientConn := net.Pipe()
		go server.serveConn(serverConn)
		return clientConn, nil
	}
}
