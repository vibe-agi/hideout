package lima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

func TestSupervisorStartControlBindsProjectionExpectation(t *testing.T) {
	session := projectionReadySessionFixture()
	start, err := supervisorStartControl(
		session, []string{"code", "."}, []string{"PATH=/hideout/session/shims:/usr/bin"},
		backend.RunStreams{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if start.ProjectionReadiness == nil {
		t.Fatal("supervisor start omitted projection readiness")
	}
	if start.ProjectionReadiness.CatalogDigest != session.ProjectionReadiness.Manifest.CatalogDigest ||
		start.ProjectionReadiness.ExpectedEntries != len(session.ProjectionReadiness.Manifest.Entries) ||
		!start.ProjectionReadiness.TargetProjected {
		t.Fatalf("projection start=%+v", start.ProjectionReadiness)
	}
}

func TestApplySupervisorProjectionReadinessRejectsForeignOrIncompleteProof(t *testing.T) {
	session := projectionReadySessionFixture()
	valid := &sessionwire.SupervisorProjectionReadinessReady{
		Status: "ready", EnvironmentID: session.EnvironmentID,
		SessionSnapshotID: session.SessionSnapshotID,
		CatalogDigest:     session.ProjectionReadiness.Manifest.CatalogDigest,
		ExpectedEntries:   1, ObservedEntries: 1, DurationMillis: 7, TargetProjected: true,
	}
	if err := applySupervisorProjectionReadiness(session, valid); err != nil {
		t.Fatal(err)
	}
	if session.ProjectionReadinessObservation == nil {
		t.Fatal("matching supervisor proof did not bind an observation")
	}
	session.ProjectionReadinessObservation = nil
	foreign := *valid
	foreign.CatalogDigest = "sha256:" + strings.Repeat("e", 64)
	if err := applySupervisorProjectionReadiness(session, &foreign); err == nil {
		t.Fatal("foreign catalog readiness was accepted")
	}
	if err := applySupervisorProjectionReadiness(session, nil); err == nil {
		t.Fatal("omitted projection readiness was accepted")
	}
}

func TestPreCommitCancellationIsTypedAndBoundedWithoutTargetGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := preCommitCancellationError(ctx)
	var readiness *backend.ProjectionReadinessError
	if !errors.As(err, &readiness) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%T %v", err, err)
	}
	if readiness.Status != backend.ProjectionReadinessCancelled ||
		readiness.ReasonCode != backend.ProjectionReadinessCancellation ||
		!strings.Contains(readiness.Hint, "before target start") {
		t.Fatalf("cancellation disposition=%+v", readiness)
	}
}

func TestProjectionReadinessRunFailuresClassifyTimeoutAndIdentityDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status backend.ProjectionReadinessStatus
		reason backend.ProjectionReadinessReason
	}{
		{
			name:   "two second visibility timeout",
			err:    errors.New("session runtime files did not become visible before deadline"),
			status: backend.ProjectionReadinessTimedOut, reason: backend.ProjectionReadinessTimeout,
		},
		{
			name:   "boot identity drift",
			err:    errors.New("guest boot identity changed before isolated target start"),
			status: backend.ProjectionReadinessRefused, reason: backend.ProjectionReadinessIdentityDrift,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := projectionReadySessionFixture()
			err := classifyProjectionReadinessRunError(context.Background(), session, test.err)
			var readiness *backend.ProjectionReadinessError
			if !errors.As(err, &readiness) || !errors.Is(err, test.err) {
				t.Fatalf("classified error=%T %v", err, err)
			}
			if readiness.Status != test.status || readiness.ReasonCode != test.reason ||
				!strings.Contains(readiness.Hint, "retry") {
				t.Fatalf("disposition=%+v", readiness)
			}
		})
	}
}

func TestSupervisorProtocolRejectsForeignReadinessBeforeCommitOrOutput(t *testing.T) {
	session := projectionReadySessionFixture()
	daemonFrames := make(chan []sessionwire.Type, 1)
	client, serverDone := newSupervisorProtocolTestClient(t, func(
		channel ssh.Channel,
		reader *sessionwire.Reader,
		writer *sessionwire.Writer,
	) error {
		if _, err := readSupervisorStartForTest(reader); err != nil {
			return err
		}
		foreign := supervisorReadyForTest(session)
		foreign.ProjectionReadiness.CatalogDigest = "sha256:" + strings.Repeat("e", 64)
		if err := writer.WriteControl(sessionwire.TypeSupervisorReady, foreign); err != nil {
			return err
		}
		if err := writer.Write(sessionwire.TypeStdout, []byte("must-not-leak")); err != nil {
			return err
		}
		var observed []sessionwire.Type
		defer func() { daemonFrames <- observed }()
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			observed = append(observed, frame.Type)
		}
	})

	var stdout bytes.Buffer
	readyCalls := 0
	err := (Backend{}).runSupervisorProtocol(
		context.Background(), session, client,
		[]string{GuestSessionSupervisorPath}, []string{"code", "."}, []string{"PATH=/usr/bin"},
		backend.RunStreams{
			Stdout: &stdout, Stderr: io.Discard,
			Ready: func(backend.SessionReadyProof) error {
				readyCalls++
				return nil
			},
		},
		func() error {
			t.Fatal("setup readiness ran for a foreign projection proof")
			return nil
		},
	)
	if err == nil {
		t.Fatalf("foreign readiness error=%v", err)
	}
	result := waitSupervisorProtocolServer(t, serverDone)
	frames := <-daemonFrames
	if result.err != nil {
		t.Fatal(result.err)
	}
	if readyCalls != 0 || session.ProjectionReadinessObservation != nil ||
		stdout.Len() != 0 || containsSessionFrame(frames, sessionwire.TypeSupervisorCommit) {
		t.Fatalf("foreign proof readyCalls=%d stdout=%q daemonFrames=%v", readyCalls, stdout.String(), frames)
	}
	for _, frameType := range []sessionwire.Type{
		sessionwire.TypeStdin, sessionwire.TypeStdinEOF, sessionwire.TypeResize, sessionwire.TypeSignal,
	} {
		if containsSessionFrame(frames, frameType) {
			t.Fatalf("foreign proof forwarded pre-commit frame %s: %v", frameType, frames)
		}
	}
}

func TestSupervisorProtocolPreCommitCancellationClosesImmediately(t *testing.T) {
	session := projectionReadySessionFixture()
	startSeen := make(chan struct{})
	daemonFrames := make(chan []sessionwire.Type, 1)
	client, serverDone := newSupervisorProtocolTestClient(t, func(
		_ ssh.Channel,
		reader *sessionwire.Reader,
		_ *sessionwire.Writer,
	) error {
		if _, err := readSupervisorStartForTest(reader); err != nil {
			return err
		}
		close(startSeen)
		var observed []sessionwire.Type
		defer func() { daemonFrames <- observed }()
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			observed = append(observed, frame.Type)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	started := time.Now()
	go func() {
		runDone <- (Backend{}).runSupervisorProtocol(
			ctx, session, client,
			[]string{GuestSessionSupervisorPath}, []string{"code", "."}, []string{"PATH=/usr/bin"},
			backend.RunStreams{Stdout: io.Discard, Stderr: io.Discard, Ready: func(backend.SessionReadyProof) error { return nil }},
			nil,
		)
	}()
	select {
	case <-startSeen:
	case <-time.After(time.Second):
		t.Fatal("supervisor start was not observed")
	}
	cancel()
	var err error
	select {
	case err = <-runDone:
	case <-time.After(time.Second):
		t.Fatal("pre-commit cancellation waited for target grace")
	}
	var readiness *backend.ProjectionReadinessError
	if !errors.As(err, &readiness) || !errors.Is(err, context.Canceled) ||
		readiness.ReasonCode != backend.ProjectionReadinessCancellation {
		t.Fatalf("pre-commit cancellation error=%T %v", err, err)
	}
	result := waitSupervisorProtocolServer(t, serverDone)
	frames := <-daemonFrames
	if result.err != nil {
		t.Fatal(result.err)
	}
	if time.Since(started) >= time.Second {
		t.Fatalf("pre-commit cancellation duration=%s", time.Since(started))
	}
	if containsSessionFrame(frames, sessionwire.TypeSupervisorCommit) ||
		containsSessionFrame(frames, sessionwire.TypeCancel) {
		t.Fatalf("pre-commit cancellation used committed protocol: %v", frames)
	}
	if !containsString(result.requests, "signal") {
		t.Fatalf("pre-commit cancellation did not kill the owning SSH session: requests=%v", result.requests)
	}
}

func TestSupervisorProtocolPostCommitCancellationKeepsGracefulProtocol(t *testing.T) {
	session := projectionReadySessionFixture()
	commitSeen := make(chan struct{})
	daemonFrames := make(chan []sessionwire.Type, 1)
	client, serverDone := newSupervisorProtocolTestClient(t, func(
		channel ssh.Channel,
		reader *sessionwire.Reader,
		writer *sessionwire.Writer,
	) error {
		if _, err := readSupervisorStartForTest(reader); err != nil {
			return err
		}
		if err := writer.WriteControl(sessionwire.TypeSupervisorReady, supervisorReadyForTest(session)); err != nil {
			return err
		}
		var observed []sessionwire.Type
		defer func() { daemonFrames <- observed }()
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				return err
			}
			observed = append(observed, frame.Type)
			switch frame.Type {
			case sessionwire.TypeSupervisorCommit:
				close(commitSeen)
			case sessionwire.TypeCancel:
				if err := writer.WriteControl(sessionwire.TypeCompletion, &sessionwire.Completion{
					Kind: sessionwire.CompletionCancelled, ExitCode: 130,
					TargetCompleted: false, CleanupCompleted: true,
					SessionID: session.ID, Summary: "cancelled",
				}); err != nil {
					return err
				}
				_, err = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{130}))
				return err
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	readyCalls := 0
	go func() {
		runDone <- (Backend{}).runSupervisorProtocol(
			ctx, session, client,
			[]string{GuestSessionSupervisorPath}, []string{"code", "."}, []string{"PATH=/usr/bin"},
			backend.RunStreams{
				Stdout: io.Discard, Stderr: io.Discard,
				Ready: func(backend.SessionReadyProof) error {
					readyCalls++
					return nil
				},
			},
			nil,
		)
	}()
	select {
	case <-commitSeen:
	case <-time.After(time.Second):
		t.Fatal("matching readiness was not committed")
	}
	cancel()
	var err error
	select {
	case err = <-runDone:
	case <-time.After(time.Second):
		t.Fatal("post-commit cancellation did not complete")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("post-commit cancellation error=%T %v", err, err)
	}
	result := waitSupervisorProtocolServer(t, serverDone)
	frames := <-daemonFrames
	if result.err != nil {
		t.Fatal(result.err)
	}
	if readyCalls != 1 || session.ProjectionReadinessObservation == nil {
		t.Fatalf("matching proof readyCalls=%d observation=%+v", readyCalls, session.ProjectionReadinessObservation)
	}
	if !containsSessionFrame(frames, sessionwire.TypeSupervisorCommit) ||
		!containsSessionFrame(frames, sessionwire.TypeCancel) {
		t.Fatalf("post-commit cancellation frames=%v", frames)
	}
	if containsString(result.requests, "signal") {
		t.Fatalf("post-commit cancellation bypassed graceful protocol: requests=%v", result.requests)
	}
}

type supervisorProtocolServerResult struct {
	requests []string
	err      error
}

func newSupervisorProtocolTestClient(
	t *testing.T,
	serve func(ssh.Channel, *sessionwire.Reader, *sessionwire.Writer) error,
) (*ssh.Client, <-chan supervisorProtocolServerResult) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	hostSigner, _ := testSSHSigner(t)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(hostSigner)
	serverDone := make(chan supervisorProtocolServerResult, 1)
	go func() {
		result := supervisorProtocolServerResult{}
		serverConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			result.err = acceptErr
			serverDone <- result
			return
		}
		defer func() {
			_ = serverConn.Close()
			serverDone <- result
		}()
		connection, incoming, requests, err := ssh.NewServerConn(serverConn, serverConfig)
		if err != nil {
			result.err = err
			return
		}
		defer connection.Close()
		go ssh.DiscardRequests(requests)
		next, ok := <-incoming
		if !ok {
			result.err = errors.New("supervisor SSH session channel was not opened")
			return
		}
		if next.ChannelType() != "session" {
			result.err = errors.New("unexpected supervisor SSH channel type")
			return
		}
		channel, channelRequests, err := next.Accept()
		if err != nil {
			result.err = err
			return
		}
		request, ok := <-channelRequests
		if !ok || request.Type != "exec" {
			result.err = errors.New("supervisor SSH exec request was not received")
			_ = channel.Close()
			return
		}
		if err := request.Reply(true, nil); err != nil {
			result.err = err
			_ = channel.Close()
			return
		}
		var (
			requestMu sync.Mutex
			requestWG sync.WaitGroup
		)
		requestWG.Add(1)
		go func() {
			defer requestWG.Done()
			for request := range channelRequests {
				requestMu.Lock()
				result.requests = append(result.requests, request.Type)
				requestMu.Unlock()
				_ = request.Reply(true, nil)
			}
		}()
		result.err = serve(
			channel,
			sessionwire.NewReader(channel, sessionwire.DaemonToSupervisor),
			sessionwire.NewWriter(channel, sessionwire.SupervisorToDaemon),
		)
		_ = channel.Close()
		_ = connection.Close()
		requestWG.Wait()
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig := &ssh.ClientConfig{
		User: "root", HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout: time.Second,
	}
	connection, channels, requests, err := ssh.NewClientConn(clientConn, listener.Addr().String(), clientConfig)
	if err != nil {
		_ = clientConn.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(connection, channels, requests)
	t.Cleanup(func() { _ = client.Close() })
	return client, serverDone
}

func waitSupervisorProtocolServer(
	t *testing.T,
	done <-chan supervisorProtocolServerResult,
) supervisorProtocolServerResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("supervisor protocol test server did not stop")
		return supervisorProtocolServerResult{}
	}
}

func readSupervisorStartForTest(reader *sessionwire.Reader) (*sessionwire.SupervisorStart, error) {
	frame, err := reader.ReadFrame()
	if err != nil {
		return nil, err
	}
	if frame.Type != sessionwire.TypeSupervisorStart {
		return nil, errors.New("first daemon frame was not supervisor-start")
	}
	control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return nil, err
	}
	return control.(*sessionwire.SupervisorStart), nil
}

func supervisorReadyForTest(session *backend.Session) *sessionwire.SupervisorReady {
	expectation := session.ProjectionReadiness
	return &sessionwire.SupervisorReady{
		Protocol: sessionwire.SupervisorProtocol, SessionID: session.ID,
		Terminal: sessionwire.TerminalDescriptor{Mode: sessionwire.TerminalNone},
		ProjectionReadiness: &sessionwire.SupervisorProjectionReadinessReady{
			Status:        string(backend.ProjectionReadinessReady),
			EnvironmentID: expectation.Manifest.EnvironmentID, SessionSnapshotID: expectation.Manifest.SessionSnapshotID,
			CatalogDigest: expectation.Manifest.CatalogDigest, ExpectedEntries: len(expectation.Manifest.Entries),
			ObservedEntries: len(expectation.Manifest.Entries), DurationMillis: 7,
			TargetProjected: expectation.TargetProjected,
		},
	}
}

func containsSessionFrame(values []sessionwire.Type, target sessionwire.Type) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func projectionReadySessionFixture() *backend.Session {
	snapshot := "sha256:" + strings.Repeat("c", 64)
	session := &backend.Session{
		ID: "ses_projection", EnvironmentID: "env_projection", SessionSnapshotID: snapshot,
		InstanceName: "hideout-projection", ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		TargetUser: "developer", GuestWork: "/workspace",
		ProjectionReadiness: &backend.ProjectionReadinessExpectation{
			ManifestRelativePath: backend.ProjectionReadinessManifestFile,
			Deadline:             backend.MaxProjectionReadinessDeadline,
			TargetProjected:      true,
			Manifest: backend.ProjectionReadinessManifest{
				Schema: backend.ProjectionReadinessManifestSchema, SessionID: "ses_projection",
				EnvironmentID: "env_projection", SessionSnapshotID: snapshot,
				Entries: []backend.ProjectionReadinessEntry{{
					Name: "hideout-shim", RelativePath: "hideout-shim",
					SHA256: "sha256:" + strings.Repeat("1", 64), Kind: backend.ProjectionEntryDispatcher,
				}},
			},
		},
	}
	catalog, _ := backend.ProjectionReadinessCatalogDigest(session.ProjectionReadiness.Manifest)
	session.ProjectionReadiness.Manifest.CatalogDigest = catalog
	return session
}
