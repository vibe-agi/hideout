package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

type daemonStreamBackend struct {
	mu          sync.Mutex
	legacyCalls int
	streamCalls int
	block       bool
	runErr      error
	readyDelay  time.Duration
}

func (b *daemonStreamBackend) Name() string                    { return "native" }
func (b *daemonStreamBackend) Available(context.Context) error { return nil }
func (b *daemonStreamBackend) Prepare(_ context.Context, spec backend.RunSpec) (*backend.Session, error) {
	return &backend.Session{
		ID: spec.SessionID, EnvironmentID: spec.EnvironmentID, Backend: "native",
		HostWork: spec.HostWork, GuestWork: spec.GuestWork, GuestHome: spec.GuestHome,
		Env: append([]string(nil), spec.Env...), ShimDir: spec.ShimDir,
		ProfileDir: spec.ProfileDir, IdentityMode: spec.IdentityMode,
		IdentityRoot: spec.IdentityRoot, SessionDir: spec.SessionDir,
		RuntimeRoot: spec.RuntimeRoot, TargetUser: spec.TargetUser,
		Broker: spec.Broker, InstanceName: spec.InstanceName,
		PreserveInstance:         spec.PreserveInstance,
		PrivilegeStatusSink:      spec.PrivilegeStatusSink,
		PrivilegedSetupEventSink: spec.PrivilegedSetupEventSink,
	}, nil
}
func (b *daemonStreamBackend) Run(context.Context, *backend.Session, []string, []string) error {
	b.mu.Lock()
	b.legacyCalls++
	b.mu.Unlock()
	return errors.New("legacy backend Run must not serve daemon streams")
}
func (b *daemonStreamBackend) RunWithStreams(ctx context.Context, session *backend.Session, _ []string, _ []string, streams backend.RunStreams) error {
	b.mu.Lock()
	b.streamCalls++
	b.mu.Unlock()
	if streams.Ready == nil {
		return errors.New("ready callback missing")
	}
	if b.readyDelay > 0 {
		timer := time.NewTimer(b.readyDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := streams.Ready(session); err != nil {
		return err
	}
	if b.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := io.WriteString(streams.Stdout, "stdout\x00bytes"); err != nil {
		return err
	}
	if _, err := io.WriteString(streams.Stderr, "stderr-bytes"); err != nil {
		return err
	}
	return b.runErr
}
func (b *daemonStreamBackend) Cleanup(context.Context, *backend.Session) error { return nil }

func TestSessionServerRunsOneCanonicalStreamWithoutLegacyFallback(t *testing.T) {
	server, token, fake, registry := newTestSessionServer(t, false)
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		server.serveConn(serverConn)
		close(done)
	}()
	writer := sessionwire.NewWriter(clientConn, sessionwire.ClientToDaemon)
	reader := sessionwire.NewReader(clientConn, sessionwire.DaemonToClient)
	if err := writer.WriteControl(sessionwire.TypeHello, &sessionwire.Hello{Protocol: sessionwire.Protocol, Token: token, ClientVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	acceptedFrame, err := reader.ReadFrame()
	if err != nil || acceptedFrame.Type != sessionwire.TypeHelloAccepted {
		t.Fatalf("hello response=%+v err=%v", acceptedFrame, err)
	}
	req := manager.RunServiceRequest{
		Version: manager.RunServiceRequestVersion, ProfileName: "default", Backend: "native",
		Workspace: t.TempDir(), Command: []string{"tool"}, AllowWeakIsolation: true,
		Terminal: manager.TerminalDescriptor{Mode: "none"},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteControl(sessionwire.TypeRunRequest, &sessionwire.RunRequestMetadata{
		Schema: sessionwire.RunRequestSchema, RequestID: "req_test", Request: payload,
	}); err != nil {
		t.Fatal(err)
	}

	var sequence []sessionwire.Type
	var stdout, stderr []byte
	var completion sessionwire.Completion
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		sequence = append(sequence, frame.Type)
		switch frame.Type {
		case sessionwire.TypeReview:
			control, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil || control.(*sessionwire.Review).PlanDigest == "" {
				t.Fatalf("review=%+v err=%v", control, decodeErr)
			}
		case sessionwire.TypeStarted:
			if err := writer.Write(sessionwire.TypeStdinEOF, nil); err != nil {
				t.Fatal(err)
			}
		case sessionwire.TypeStdout:
			stdout = append(stdout, frame.Payload...)
		case sessionwire.TypeStderr:
			stderr = append(stderr, frame.Payload...)
		case sessionwire.TypeCompletion:
			control, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			completion = *control.(*sessionwire.Completion)
			goto finished
		case sessionwire.TypeError:
			t.Fatalf("server error frame=%s", frame.Payload)
		}
	}

finished:
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session server did not close")
	}
	if len(sequence) < 5 || sequence[0] != sessionwire.TypeReview || sequence[1] != sessionwire.TypeStarted {
		t.Fatalf("frame sequence=%v", sequence)
	}
	if string(stdout) != "stdout\x00bytes" || string(stderr) != "stderr-bytes" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	if completion.Kind != sessionwire.CompletionExit || completion.ExitCode != 0 || !completion.TargetCompleted || !completion.CleanupCompleted {
		t.Fatalf("completion=%+v", completion)
	}
	fake.mu.Lock()
	legacy, streamed := fake.legacyCalls, fake.streamCalls
	fake.mu.Unlock()
	if legacy != 0 || streamed != 1 {
		t.Fatalf("legacy=%d streamed=%d", legacy, streamed)
	}
	if got := registry.snapshots(); len(got) != 0 {
		t.Fatalf("live workers after completion=%+v", got)
	}
}

func TestSessionServerDisconnectCancelsOnlyOwningWorker(t *testing.T) {
	server, token, _, registry := newTestSessionServer(t, true)
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() { server.serveConn(serverConn); close(done) }()
	writer := sessionwire.NewWriter(clientConn, sessionwire.ClientToDaemon)
	reader := sessionwire.NewReader(clientConn, sessionwire.DaemonToClient)
	if err := writer.WriteControl(sessionwire.TypeHello, &sessionwire.Hello{Protocol: sessionwire.Protocol, Token: token}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadFrame(); err != nil {
		t.Fatal(err)
	}
	req := manager.RunServiceRequest{
		Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
		Command: []string{"tool"}, AllowWeakIsolation: true, Terminal: manager.TerminalDescriptor{Mode: "none"},
	}
	payload, _ := json.Marshal(req)
	if err := writer.WriteControl(sessionwire.TypeRunRequest, &sessionwire.RunRequestMetadata{Schema: sessionwire.RunRequestSchema, RequestID: "req_disconnect", Request: payload}); err != nil {
		t.Fatal(err)
	}
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Type == sessionwire.TypeStarted {
			break
		}
	}
	if len(registry.snapshots()) != 1 {
		t.Fatalf("workers=%+v", registry.snapshots())
	}
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not cancel worker")
	}
	if len(registry.snapshots()) != 0 {
		t.Fatalf("worker survived disconnect: %+v", registry.snapshots())
	}
}

func TestSessionServerDisconnectBeforeStartCancelsPendingBackend(t *testing.T) {
	server, token, fake, registry := newTestSessionServer(t, false)
	fake.readyDelay = 500 * time.Millisecond
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() { server.serveConn(serverConn); close(done) }()
	writer := sessionwire.NewWriter(clientConn, sessionwire.ClientToDaemon)
	reader := sessionwire.NewReader(clientConn, sessionwire.DaemonToClient)
	if err := writer.WriteControl(sessionwire.TypeHello, &sessionwire.Hello{Protocol: sessionwire.Protocol, Token: token}); err != nil {
		t.Fatal(err)
	}
	if frame, err := reader.ReadFrame(); err != nil || frame.Type != sessionwire.TypeHelloAccepted {
		t.Fatalf("hello frame=%+v err=%v", frame, err)
	}
	req := manager.RunServiceRequest{
		Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
		Command: []string{"tool"}, AllowWeakIsolation: true,
		Terminal: manager.TerminalDescriptor{Mode: "none"},
	}
	payload, _ := json.Marshal(req)
	if err := writer.WriteControl(sessionwire.TypeRunRequest, &sessionwire.RunRequestMetadata{
		Schema: sessionwire.RunRequestSchema, RequestID: "req_disconnect_before_start", Request: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if frame, err := reader.ReadFrame(); err != nil || frame.Type != sessionwire.TypeReview {
		t.Fatalf("review frame=%+v err=%v", frame, err)
	}
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pre-start disconnect did not cancel backend activation")
	}
	if got := registry.snapshots(); len(got) != 0 {
		t.Fatalf("pending worker survived disconnect: %+v", got)
	}
	fake.mu.Lock()
	streamCalls := fake.streamCalls
	fake.mu.Unlock()
	if streamCalls != 1 {
		t.Fatalf("stream backend calls=%d want 1", streamCalls)
	}
}

func TestSessionServerRejectsCredentialWithoutEchoingIt(t *testing.T) {
	server, _, _, _ := newTestSessionServer(t, false)
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() { server.serveConn(serverConn); close(done) }()
	writer := sessionwire.NewWriter(clientConn, sessionwire.ClientToDaemon)
	reader := sessionwire.NewReader(clientConn, sessionwire.DaemonToClient)
	secret := "cap_0123456789abcdef0123456789abcdef"
	if err := writer.WriteControl(sessionwire.TypeHello, &sessionwire.Hello{Protocol: sessionwire.Protocol, Token: secret}); err != nil {
		t.Fatal(err)
	}
	frame, err := reader.ReadFrame()
	if err != nil || frame.Type != sessionwire.TypeError {
		t.Fatalf("frame=%+v err=%v", frame, err)
	}
	if strings.Contains(string(frame.Payload), secret) || strings.Contains(string(frame.Payload), "cap_") {
		t.Fatalf("auth refusal leaked token: %s", frame.Payload)
	}
	_ = clientConn.Close()
	<-done
}

func newTestSessionServer(t *testing.T, block bool) (*sessionServer, string, *daemonStreamBackend, *sessionRegistry) {
	t.Helper()
	setDaemonFakeLinuxShim(t)
	store := profile.Store{Root: t.TempDir()}
	credentials, err := newCredentialManager(t.TempDir(), time.Hour, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake := &daemonStreamBackend{block: block}
	registry := newSessionRegistry(8, nil)
	server := &sessionServer{
		core: manager.New(store), credentials: credentials, instanceID: "daemon_test",
		registry: registry, leaseDuration: time.Second, renewalInterval: 250 * time.Millisecond,
		backendFactory: func(manager.RunServiceRequest, manager.RunPlan) (backend.Backend, error) { return fake, nil },
	}
	return server, credentials.Token(), fake, registry
}

func setDaemonFakeLinuxShim(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hideout-shim-linux")
	if err := os.WriteFile(path, []byte("fake linux shim"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteStoreHelperManifest(path, "hideout-shim", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", path)
}
