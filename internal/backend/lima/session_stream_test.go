package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestSupervisorStartControlBindsProjectionExpectation(t *testing.T) {
	session := projectionReadySessionFixture()
	start, err := supervisorStartControl(
		session, []string{"code", "."}, []string{"PATH=/hideout/session/shims:/usr/bin"},
		backend.RunStreams{}, nil,
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

func TestSupervisorStartControlBindsPortalTargetPWDToLogicalAlias(t *testing.T) {
	session := projectionReadySessionFixture()
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	session.Workspace = backend.WorkspaceAttachmentSpec{
		GuestRoot: "/workspace", Transport: backend.WorkspaceTransportPortal,
		Portal: &backend.WorkspacePortalBinding{
			PhysicalGuestRoot: "/hideout/workspaces/" + workspaceID,
		},
	}
	start, err := supervisorStartControl(
		session,
		[]string{"bash", "--noprofile", "--norc", "-c", "pwd -L"},
		[]string{"PATH=/usr/bin:/bin", "PWD=/caller-controlled"},
		backend.RunStreams{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := start.Env["PWD"]; got != "/workspace" {
		t.Fatalf("Portal target PWD=%q want logical attachment alias", got)
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
		// Deliver the observed frames on every return path: the client fails
		// closed on the foreign proof and may drop the connection while this
		// handler is still writing, and the test consumes daemonFrames before
		// it checks the server result.
		var observed []sessionwire.Type
		defer func() { daemonFrames <- observed }()
		if _, err := readSupervisorStartForTest(reader); err != nil {
			return err
		}
		foreign := supervisorReadyForTest(session)
		foreign.ProjectionReadiness.CatalogDigest = "sha256:" + strings.Repeat("e", 64)
		if err := writer.WriteControl(sessionwire.TypeSupervisorReady, foreign); err != nil {
			// The rejecting client may close the channel before this write
			// lands; the early close is the behavior under test, not a
			// server failure.
			return nil
		}
		if err := writer.Write(sessionwire.TypeStdout, []byte("must-not-leak")); err != nil {
			return nil
		}
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
		var observed []sessionwire.Type
		defer func() { daemonFrames <- observed }()
		if _, err := readSupervisorStartForTest(reader); err != nil {
			return err
		}
		close(startSeen)
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
		var observed []sessionwire.Type
		defer func() { daemonFrames <- observed }()
		if err := writer.WriteControl(sessionwire.TypeSupervisorReady, supervisorReadyForTest(session)); err != nil {
			return err
		}
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
	wantCommand := rootControlShellCommand(
		"/",
		[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		[]string{GuestSessionSupervisorPath},
	)
	if result.command != wantCommand {
		t.Fatalf("supervisor exec command=%q want %q", result.command, wantCommand)
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

func TestSupervisorProtocolRegistersAndIngestsActivityBeforeTargetCommit(t *testing.T) {
	const observerTail = 512
	session := projectionReadySessionFixture()
	owner, err := workloadtypes.NewReusableOwner(
		session.EnvironmentID,
		"lima",
		"hideout-projection:3:"+session.ExpectedBootID,
	)
	if err != nil {
		t.Fatal(err)
	}
	session.Activity = &backend.ActivityPreparation{
		Owner: owner, SessionID: session.ID, EnvironmentID: session.EnvironmentID,
		Backend: "lima", BackendIncarnationID: owner.BackendIncarnationID,
		GuestBootID: session.ExpectedBootID, ObserverGeneration: 1,
		ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
		Retention:            workloadtypes.DefaultActivityRetentionPolicy(),
	}
	token, err := sessionwire.NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()

	starts := make(chan *sessionwire.SupervisorStart, 1)
	observed := make(chan struct{})
	targetCommitted := make(chan struct{})
	observerEOF := make(chan struct{})
	var observedOnce sync.Once
	var observedCount atomic.Uint64
	client, server := newObserverStreamSSHTestClient(t, func(command string, channel ssh.Channel) error {
		if strings.Contains(command, "observer-stream") {
			start := <-starts
			defer start.Activity.ObserverStreamToken.Destroy()
			open, err := sessionwire.ReadObserverStreamOpen(channel)
			if err != nil {
				return err
			}
			if err := sessionwire.AuthenticateObserverStreamOpen(
				open,
				start.SessionID,
				start.Activity.ObserverStreamToken,
				sessionwire.ObserverStreamPeer{UID: 0},
			); err != nil {
				return err
			}
			binding := sessionwire.ObserverBinding{
				Owner: start.Activity.Owner, SessionID: start.SessionID,
				EnvironmentID:        session.EnvironmentID,
				BackendIncarnationID: start.Activity.Owner.BackendIncarnationID,
				GuestBootID:          start.ExpectedBootID, CgroupID: 8080,
				ObserverGeneration: start.Activity.ObserverGeneration,
			}
			capability := sessionwire.ObserverCapability{
				State:    workloadtypes.CoverageAvailable,
				Evidence: []string{"fixture.available"},
			}
			hello := sessionwire.ObserverHello{
				Type: sessionwire.ObserverHelloType, Schema: sessionwire.ObserverWireSchema,
				Owner: binding.Owner, SessionID: binding.SessionID,
				EnvironmentID:        binding.EnvironmentID,
				BackendIncarnationID: binding.BackendIncarnationID,
				GuestBootID:          binding.GuestBootID, CgroupID: binding.CgroupID,
				ObserverGeneration: binding.ObserverGeneration,
				HelperDigest:       session.Activity.ObserverHelperDigest,
				Capabilities: sessionwire.ObserverCapabilities{
					Process: capability, File: capability, Network: capability, DNS: capability,
				},
			}
			if err := sessionwire.WriteObserverHello(channel, hello); err != nil {
				return err
			}
			accepted, err := sessionwire.ReadObserverAccepted(channel)
			if err != nil {
				return err
			}
			if err := accepted.ValidateBinding(binding); err != nil {
				return err
			}
			payload, _ := json.Marshal(struct {
				LatestSequence uint64 `json:"latestSequence"`
				KernelDropped  uint64 `json:"kernelDropped"`
				RingDropped    uint64 `json:"ringDropped"`
			}{LatestSequence: 1})
			if err := sessionwire.WriteObserverEnvelope(channel, sessionwire.ObservationEnvelope{
				Schema: sessionwire.ObservationSchema, Owner: binding.Owner,
				SessionID: binding.SessionID, CgroupID: binding.CgroupID,
				ObserverGeneration: binding.ObserverGeneration,
				CPU:                0, Sequence: 1, MonotonicNS: 1,
				Kind: "collector.heartbeat", Payload: payload,
			}); err != nil {
				return err
			}
			select {
			case <-targetCommitted:
			case <-time.After(time.Second):
				return errors.New("target was not committed before observer tail")
			}
			for sequence := uint64(2); sequence <= observerTail+1; sequence++ {
				if err := sessionwire.WriteObserverEnvelope(channel, sessionwire.ObservationEnvelope{
					Schema: sessionwire.ObservationSchema, Owner: binding.Owner,
					SessionID: binding.SessionID, CgroupID: binding.CgroupID,
					ObserverGeneration: binding.ObserverGeneration,
					CPU:                0, Sequence: sequence, MonotonicNS: sequence,
					Kind: "process.exec", Payload: json.RawMessage(`{"pid":42}`),
				}); err != nil {
					return err
				}
			}
			return sessionwire.WriteObserverEnvelope(channel, sessionwire.ObservationEnvelope{
				Schema: sessionwire.ObservationSchema, Owner: binding.Owner,
				SessionID: binding.SessionID, CgroupID: binding.CgroupID,
				ObserverGeneration: binding.ObserverGeneration,
				CPU:                sessionwire.ObserverTransportCPU, Sequence: 1,
				MonotonicNS: observerTail + 2,
				Kind:        "collector.goodbye",
				Payload:     json.RawMessage(`{"reason":"relay-drained"}`),
			})
		}

		reader := sessionwire.NewReader(channel, sessionwire.DaemonToSupervisor)
		writer := sessionwire.NewWriter(channel, sessionwire.SupervisorToDaemon)
		start, err := readSupervisorStartForTest(reader)
		if err != nil {
			return err
		}
		if start.Activity == nil {
			return errors.New("supervisor start omitted activity expectation")
		}
		starts <- start
		capability := sessionwire.ObserverCapability{
			State:    workloadtypes.CoverageAvailable,
			Evidence: []string{"fixture.available"},
		}
		coverageValues := (sessionwire.ObserverCapabilities{
			Process: capability, File: capability, Network: capability, DNS: capability,
		}).Coverage()
		activityReady := &sessionwire.SupervisorActivityReady{
			Boundary: workloadtypes.WorkloadBoundary{
				Schema: workloadtypes.WorkloadBoundarySchema, Owner: start.Activity.Owner,
				SessionID:  start.SessionID,
				CgroupPath: "/sys/fs/cgroup/hideout/" + start.SessionID,
				CgroupID:   8080, TargetUser: start.TargetUser,
				State:              workloadtypes.BoundaryReady,
				ObserverGeneration: start.Activity.ObserverGeneration,
				GuestBootID:        start.ExpectedBootID, CreatedAtMonoNS: 1,
			},
			ObserverHelperDigest: start.Activity.ObserverHelperDigest,
			Coverage:             coverageValues,
		}
		ready := supervisorReadyForTest(session)
		ready.Activity = activityReady
		if err := writer.WriteControl(sessionwire.TypeSupervisorReady, ready); err != nil {
			return err
		}
		frame, err := reader.ReadFrame()
		if err != nil {
			return err
		}
		if frame.Type != sessionwire.TypeSupervisorCommit {
			return errors.New("target was not committed after activity readiness")
		}
		select {
		case <-observed:
		case <-time.After(time.Second):
			return errors.New("target commit raced ahead of activity ingestion")
		}
		close(targetCommitted)
		select {
		case <-observerEOF:
		case <-time.After(time.Second):
			return errors.New("observer EOF was not persisted before activity completion")
		}
		return writer.WriteControl(sessionwire.TypeCompletion, &sessionwire.Completion{
			Kind: sessionwire.CompletionExit, ExitCode: 0,
			TargetCompleted: true, CleanupCompleted: true,
			SessionID: session.ID, Summary: "exited",
			Activity: &sessionwire.SupervisorActivityCompletion{
				Owner: start.Activity.Owner, SessionID: start.SessionID,
				CgroupID: 8080, ObserverGeneration: start.Activity.ObserverGeneration,
				BoundaryState: workloadtypes.BoundaryRemoved,
				Coverage:      coverageValues, CleanupProved: true,
			},
		})
	})

	var (
		orderMu        sync.Mutex
		order          []string
		observerClosed bool
		closed         bool
	)
	streams := backend.RunStreams{
		Stdout: io.Discard, Stderr: io.Discard,
		Activity: &backend.ActivityStreams{
			Prepare: func(preparation backend.ActivityPreparation) (sessionwire.SupervisorActivityExpectation, error) {
				if preparation != *session.Activity {
					return sessionwire.SupervisorActivityExpectation{}, errors.New("activity preparation identity drift")
				}
				orderMu.Lock()
				order = append(order, "prepare")
				orderMu.Unlock()
				return sessionwire.SupervisorActivityExpectation{
					Owner:                preparation.Owner,
					ObserverGeneration:   preparation.ObserverGeneration,
					ObserverHelperDigest: preparation.ObserverHelperDigest,
					ObserverStreamToken:  token,
				}, nil
			},
			BoundaryReady: func(ready *sessionwire.SupervisorActivityReady) error {
				if ready == nil || ready.Boundary.CgroupID != 8080 {
					return errors.New("wrong activity boundary")
				}
				orderMu.Lock()
				order = append(order, "boundary")
				orderMu.Unlock()
				return nil
			},
			Observe: func(envelope sessionwire.ObservationEnvelope) error {
				if envelope.Kind != "collector.heartbeat" &&
					envelope.Kind != "process.exec" &&
					envelope.Kind != "collector.goodbye" {
					return errors.New("unexpected activity observation")
				}
				if envelope.Kind != "collector.goodbye" {
					observedCount.Add(1)
				}
				orderMu.Lock()
				order = append(order, "observe")
				orderMu.Unlock()
				observedOnce.Do(func() { close(observed) })
				return nil
			},
			ObserverClosed: func(cause error) error {
				if cause != nil {
					return fmt.Errorf("proved observer drain cause: %w", cause)
				}
				orderMu.Lock()
				order = append(order, "observer-closed")
				observerClosed = true
				orderMu.Unlock()
				close(observerEOF)
				return nil
			},
			SessionClosed: func(completion *sessionwire.SupervisorActivityCompletion, _ error) error {
				if completion == nil || completion.CgroupID != 8080 {
					return errors.New("activity completion was not bound")
				}
				if got := observedCount.Load(); got != observerTail+1 {
					return fmt.Errorf(
						"activity completion raced observer tail: got=%d want=%d",
						got,
						observerTail+1,
					)
				}
				orderMu.Lock()
				if !observerClosed {
					orderMu.Unlock()
					return errors.New("activity session closed before observer EOF receipt")
				}
				order = append(order, "closed")
				closed = true
				orderMu.Unlock()
				return nil
			},
		},
		Ready: func(backend.SessionReadyProof) error {
			orderMu.Lock()
			order = append(order, "ready")
			orderMu.Unlock()
			return nil
		},
	}
	if err := (Backend{}).runSupervisorProtocol(
		context.Background(),
		session,
		client,
		[]string{GuestSessionSupervisorPath},
		[]string{"code", "."},
		[]string{"PATH=/usr/bin"},
		streams,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	result := waitObserverStreamSSHServer(t, server)
	if result.err != nil {
		t.Fatal(result.err)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if !closed {
		t.Fatal("activity session was not closed")
	}
	boundaryIndex, readyIndex := -1, -1
	for index, value := range order {
		switch value {
		case "boundary":
			boundaryIndex = index
		case "ready":
			readyIndex = index
		}
	}
	if boundaryIndex < 0 || readyIndex < 0 || boundaryIndex >= readyIndex {
		t.Fatalf("activity boundary was not registered before target readiness: %v", order)
	}
}

type supervisorProtocolServerResult struct {
	command  string
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
		var execRequest struct {
			Command string
		}
		if err := ssh.Unmarshal(request.Payload, &execRequest); err != nil {
			result.err = err
			_ = channel.Close()
			return
		}
		result.command = execRequest.Command
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
