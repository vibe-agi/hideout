package lima

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

const (
	supervisorHeartbeatInterval = 5 * time.Second
	supervisorCancelBound       = 5 * time.Second
	supervisorStderrLimit       = 64 << 10
	supervisorInputChunk        = 32 << 10
)

type supervisorTargetError struct {
	completion sessionwire.Completion
}

func (e supervisorTargetError) Error() string {
	if e.completion.Summary != "" {
		return e.completion.Summary
	}
	if e.completion.Signal != "" {
		return fmt.Sprintf("target terminated by %s", e.completion.Signal)
	}
	return fmt.Sprintf("target exited with status %d", e.completion.ExitCode)
}

func (e supervisorTargetError) ExitCode() int      { return e.completion.ExitCode }
func (e supervisorTargetError) SignalName() string { return e.completion.Signal }

func (b Backend) RunWithStreams(ctx context.Context, session *backend.Session, command []string, env []string, streams backend.RunStreams) (retErr error) {
	if ctx == nil {
		return errors.New("lima daemon session requires a context")
	}
	if session == nil || len(command) == 0 {
		return errors.New("lima daemon session requires a prepared session and command")
	}
	if !session.SessionIsolationRequired {
		return errors.New("lima daemon session requires the isolated session supervisor")
	}
	if !session.RuntimeReady {
		if streams.Stderr != nil {
			b.Progress = streams.Stderr
		}
		if err := b.Activate(ctx, session, env); err != nil {
			return err
		}
	}
	if session.RuntimeCompletionSink != nil {
		defer func() { retErr = errors.Join(retErr, session.RuntimeCompletionSink(retErr)) }()
	}
	return b.runIsolatedSupervisor(ctx, session, command, env, streams)
}

func (b Backend) runIsolatedSupervisor(ctx context.Context, session *backend.Session, command, env []string, streams backend.RunStreams) error {
	setup, setupCategories, err := b.supervisorSetupIdentity(ctx, session)
	if err != nil {
		return err
	}
	targetUser := session.TargetUser
	if targetUser == "" {
		targetUser = "developer"
	}
	viewCommand, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: session.ID, TargetUser: targetUser, GuestWork: session.GuestWork,
		Env: env, Command: []string{GuestSessionSupervisorPath}, RunBootstrap: true,
		NetworkBootstrapPath: session.NetworkBootstrapGuestPath,
		NetworkCleanupPath:   session.NetworkCleanupGuestPath,
		HostFSEnabled:        session.HostFSEnabled,
		HostFSGrafts:         session.HostFSGrafts,
		ExpectedBootID:       session.ExpectedBootID,
		SessionSupervisor:    true,
		RequiredRuntimePaths: sessionRuntimePrerequisites(session, true),
		Workspace:            session.Workspace,
	})
	if err != nil {
		return err
	}

	lease, err := b.acquireSSHClientForUser(ctx, session.InstanceName, "root")
	if err != nil {
		return err
	}
	defer lease.Close()
	client := lease.Client()

	session.IsolationRunStarted = true
	setupReported := false
	onReady := func() error {
		if err := b.emitSupervisorSetupStatus(
			session, setup, setupCategories, "succeeded",
			"privileged setup completed inside the fixed guest session supervisor",
		); err != nil {
			return err
		}
		setupReported = true
		return nil
	}
	runErr := b.runSupervisorProtocol(ctx, session, client, viewCommand, command, env, streams, onReady)
	runErr = classifyProjectionReadinessRunError(ctx, session, runErr)
	if runErr != nil {
		runErr = fmt.Errorf("supervisor protocol: %w", runErr)
	}
	if runErr != nil && !setupReported {
		runErr = errors.Join(runErr, b.emitSupervisorSetupStatus(
			session, setup, setupCategories, "failed", runErr.Error(),
		))
	}
	proofCtx, cancel := context.WithTimeout(context.Background(), sessionViewProbeTimeout)
	defer cancel()
	if err := b.proveIsolatedSessionTerminatedWithClient(proofCtx, session, client); err != nil {
		return errors.Join(runErr, err)
	}
	session.IsolationCleanupProved = true
	cleanupSetup := rootControlSSHSetupIdentity()
	cleanupSetup.Proof = "existing authenticated root SSH transport proved the exact supervisor process tree absent"
	eventErr := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
		Action: privilege.ActionPrivilegedCleanup, Category: "session-view", Status: "succeeded",
		Setup: cleanupSetup, Reason: "supervisor-owned session cleanup proved through the owning authenticated transport",
	})
	return errors.Join(runErr, eventErr)
}

type supervisorFrameResult struct {
	frame sessionwire.Frame
	err   error
}

type supervisorInputResult struct {
	data []byte
	err  error
}

func (b Backend) runSupervisorProtocol(
	ctx context.Context,
	prepared *backend.Session,
	client *ssh.Client,
	viewCommand, command, env []string,
	streams backend.RunStreams,
	onReady func() error,
) (retErr error) {
	activityExpectation, activityPrepared, err := prepareBackendActivity(prepared, streams)
	if err != nil {
		return err
	}
	var (
		observerStream         *limaObserverStream
		activityCompletion     *sessionwire.SupervisorActivityCompletion
		activityObserverClosed bool
		activitySessionClosed  bool
	)
	defer func() {
		if observerStream != nil {
			closeErr := observerStream.Close()
			observerStream = nil
			if activityPrepared && !activityObserverClosed {
				activityObserverClosed = true
				observerCause := errors.Join(retErr, closeErr)
				retErr = errors.Join(
					retErr,
					closeErr,
					streams.Activity.ObserverClosed(observerCause),
				)
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
		if activityExpectation != nil {
			activityExpectation.ObserverStreamToken.Destroy()
		}
		if activityPrepared && !activitySessionClosed {
			retErr = errors.Join(
				retErr,
				streams.Activity.SessionClosed(activityCompletion, retErr),
			)
		}
	}()
	sshSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open supervisor ssh session: %w", err)
	}
	defer sshSession.Close()
	stdin, err := sshSession.StdinPipe()
	if err != nil {
		return fmt.Errorf("open supervisor stdin: %w", err)
	}
	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open supervisor stdout: %w", err)
	}
	stderr, err := sshSession.StderrPipe()
	if err != nil {
		return fmt.Errorf("open supervisor stderr: %w", err)
	}
	stderrCapture := &boundedBuffer{limit: supervisorStderrLimit}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderrCapture, stderr)
		close(stderrDone)
	}()

	commandLine := rootControlShellCommand("/", []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, viewCommand)
	if err := sshSession.Start(commandLine); err != nil {
		return fmt.Errorf("start supervisor ssh session: %w", err)
	}
	writer := sessionwire.NewWriter(stdin, sessionwire.DaemonToSupervisor)
	reader := sessionwire.NewReader(stdout, sessionwire.SupervisorToDaemon)
	start, err := supervisorStartControl(
		prepared,
		command,
		env,
		streams,
		activityExpectation,
	)
	if err != nil {
		return fmt.Errorf("build supervisor start: %w", err)
	}
	if err := writer.WriteControl(sessionwire.TypeSupervisorStart, start); err != nil {
		return fmt.Errorf("write supervisor start: %w", err)
	}

	frames := make(chan supervisorFrameResult, 1)
	go readSupervisorFrames(reader, frames)
	wait := make(chan error, 1)
	go func() { wait <- sshSession.Wait() }()

	var completion *sessionwire.Completion
	var activityReady *sessionwire.SupervisorActivityReady
	var protocolErr error
	ready := false
	cancelSent := false
	var cancelTimer <-chan time.Time
	ctxDone := ctx.Done()
	var sshWaitErr error
	cancelForProtocolError := func(failure error) {
		if failure == nil {
			return
		}
		if protocolErr == nil {
			protocolErr = failure
		}
		if !cancelSent && completion == nil {
			_ = writer.Write(sessionwire.TypeCancel, nil)
			cancelSent = true
			cancelTimer = time.After(supervisorCancelBound)
		}
	}
	heartbeat := time.NewTicker(supervisorHeartbeatInterval)
	defer heartbeat.Stop()
	input := make(chan supervisorInputResult, 1)
	if streams.Stdin != nil {
		go readSupervisorInput(streams.Stdin, input)
	} else {
		close(input)
	}
	controls := streams.Controls
	var observerResults <-chan error

	for {
		if wait == nil && frames == nil {
			_ = stdin.Close()
			<-stderrDone
			if completion == nil {
				failure := protocolErr
				if failure == nil && sshWaitErr != nil {
					failure = fmt.Errorf("wait for supervisor: %w", sshWaitErr)
				}
				if failure == nil {
					failure = errors.New("guest supervisor exited without completion")
				}
				if text := strings.TrimSpace(stderrCapture.String()); text != "" {
					failure = fmt.Errorf("%w: %s", failure, text)
				}
				return failure
			}
			if protocolErr != nil {
				return protocolErr
			}
			if cancelSent && ctx.Err() != nil {
				return ctx.Err()
			}
			return completionError(*completion)
		}
		select {
		case result, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			if result.err != nil {
				if completion == nil {
					cancelForProtocolError(fmt.Errorf("read supervisor frame: %w", result.err))
				}
				frames = nil
				continue
			}
			switch result.frame.Type {
			case sessionwire.TypeSupervisorReady:
				if ready {
					return errors.New("guest supervisor reported ready more than once")
				}
				control, decodeErr := sessionwire.DecodeControl(result.frame.Type, result.frame.Payload)
				if decodeErr != nil {
					return fmt.Errorf("decode supervisor ready: %w", decodeErr)
				}
				reported := control.(*sessionwire.SupervisorReady)
				if reported.SessionID != prepared.ID || reported.Terminal != start.Terminal {
					return errors.New("guest supervisor ready identity drift")
				}
				if streams.Ready == nil {
					return errors.New("daemon stream is missing the ready callback")
				}
				if err := applySupervisorProjectionReadiness(prepared, reported.ProjectionReadiness); err != nil {
					return err
				}
				if activityExpectation == nil {
					if reported.Activity != nil {
						return errors.New("guest supervisor reported unexpected activity readiness")
					}
				} else {
					if reported.Activity == nil {
						return errors.New("guest supervisor omitted activity readiness")
					}
					if err := reported.Activity.ValidateExpectation(
						prepared.ID,
						activityExpectation,
					); err != nil {
						return fmt.Errorf("validate supervisor activity readiness: %w", err)
					}
					if err := streams.Activity.BoundaryReady(reported.Activity); err != nil {
						return fmt.Errorf("register daemon activity boundary: %w", err)
					}
					activityReady = cloneSupervisorActivityReady(reported.Activity)
					binding := sessionwire.ObserverBinding{
						Owner:                activityExpectation.Owner,
						SessionID:            prepared.ID,
						EnvironmentID:        prepared.Activity.EnvironmentID,
						BackendIncarnationID: prepared.Activity.BackendIncarnationID,
						GuestBootID:          reported.Activity.Boundary.GuestBootID,
						CgroupID:             reported.Activity.Boundary.CgroupID,
						ObserverGeneration:   activityExpectation.ObserverGeneration,
					}
					opened, openErr := openLimaObserverStream(
						ctx,
						client,
						observerStreamExpectation{
							Binding:      binding,
							Token:        activityExpectation.ObserverStreamToken,
							HelperDigest: activityExpectation.ObserverHelperDigest,
						},
					)
					activityExpectation.ObserverStreamToken.Destroy()
					if openErr != nil {
						return fmt.Errorf("open daemon activity stream: %w", openErr)
					}
					observerStream = opened
					if err := validateObserverReadyHello(
						reported.Activity,
						opened.Hello(),
					); err != nil {
						return err
					}
					if err := ingestFirstLimaObservation(
						ctx,
						opened,
						streams.Activity,
					); err != nil {
						return fmt.Errorf("ingest initial activity observation: %w", err)
					}
					results := make(chan error, 1)
					observerResults = results
					go pumpLimaObserverStream(opened, streams.Activity, results)
				}
				if onReady != nil {
					if err := onReady(); err != nil {
						return fmt.Errorf("record supervisor setup readiness: %w", err)
					}
				}
				proof, proofErr := backend.ReadyProofForSession(prepared, backend.SessionReadyAuthenticatedSupervisor)
				if proofErr != nil {
					return fmt.Errorf("build authenticated supervisor ready proof: %w", proofErr)
				}
				if err := streams.Ready(proof); err != nil {
					return fmt.Errorf("publish daemon session readiness: %w", err)
				}
				if err := writer.Write(sessionwire.TypeSupervisorCommit, nil); err != nil {
					return fmt.Errorf("commit supervisor target start: %w", err)
				}
				ready = true
			case sessionwire.TypeTerminal, sessionwire.TypeStdout, sessionwire.TypeStderr:
				if !ready {
					return errors.New("guest supervisor emitted target output before ready")
				}
				if err := writeSupervisorOutput(result.frame, streams); err != nil {
					cancelForProtocolError(fmt.Errorf("write supervisor output: %w", err))
				}
			case sessionwire.TypeSupervisorError:
				control, decodeErr := sessionwire.DecodeControl(result.frame.Type, result.frame.Payload)
				if decodeErr != nil {
					return fmt.Errorf("decode supervisor error: %w", decodeErr)
				}
				reported := control.(*sessionwire.Error)
				if status, reason, ok := projectionReadinessDisposition(reported.Code); ok {
					cancelForProtocolError(&backend.ProjectionReadinessError{
						Status: status, ReasonCode: reason,
						Hint: "projection readiness was refused before target start; retry after checking the session",
						Err:  fmt.Errorf("guest supervisor readiness refusal: %s", reported.Summary),
					})
				} else {
					cancelForProtocolError(fmt.Errorf("guest supervisor error: %s", reported.Summary))
				}
			case sessionwire.TypeCompletion:
				control, decodeErr := sessionwire.DecodeControl(result.frame.Type, result.frame.Payload)
				if decodeErr != nil {
					return fmt.Errorf("decode supervisor completion: %w", decodeErr)
				}
				value := control.(*sessionwire.Completion)
				if value.SessionID != prepared.ID {
					return errors.New("guest supervisor completion identity drift")
				}
				if len(value.Result) != 0 {
					return errors.New("guest supervisor cannot supply a Manager run result")
				}
				if activityExpectation == nil {
					if value.Activity != nil {
						return errors.New("guest supervisor reported unexpected activity completion")
					}
				} else {
					if value.Activity == nil {
						return errors.New("guest supervisor omitted activity completion")
					}
					if activityReady == nil {
						return errors.New("guest supervisor activity completion lacks a ready boundary")
					}
					if err := value.Activity.ValidateReady(
						prepared.ID,
						activityReady,
					); err != nil {
						return fmt.Errorf("validate supervisor activity completion: %w", err)
					}
					activityCompletion = value.Activity
					var observerCloseCause error
					if observerResults != nil {
						observerCloseCause = awaitLimaObserverDrain(
							observerStream,
							observerResults,
						)
						observerResults = nil
					}
					if observerStream != nil {
						closeErr := observerStream.Close()
						observerStream = nil
						observerCloseCause = errors.Join(
							observerCloseCause,
							closeErr,
						)
					}
					if !activityObserverClosed {
						activityObserverClosed = true
						if err := streams.Activity.ObserverClosed(
							observerCloseCause,
						); err != nil {
							return fmt.Errorf(
								"close daemon observer stream: %w",
								err,
							)
						}
					}
					if err := streams.Activity.SessionClosed(
						value.Activity,
						completionError(*value),
					); err != nil {
						return fmt.Errorf("close daemon activity session: %w", err)
					}
					activitySessionClosed = true
				}
				completion = value
				input = nil
				controls = nil
			default:
				return fmt.Errorf("unexpected guest supervisor frame %s", result.frame.Type)
			}

		case incoming, ok := <-input:
			if !ok {
				input = nil
				if ready && completion == nil {
					if err := writer.Write(sessionwire.TypeStdinEOF, nil); err != nil {
						cancelForProtocolError(fmt.Errorf("write supervisor stdin EOF: %w", err))
					}
				}
				continue
			}
			if incoming.err != nil {
				cancelForProtocolError(fmt.Errorf("read supervisor input: %w", incoming.err))
				input = nil
				continue
			}
			if !ready {
				return errors.New("daemon attempted supervisor stdin before ready")
			}
			if err := writer.Write(sessionwire.TypeStdin, incoming.data); err != nil {
				cancelForProtocolError(fmt.Errorf("write supervisor stdin: %w", err))
			}

		case control, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if !ready {
				return errors.New("daemon attempted supervisor control before ready")
			}
			if err := writeSupervisorControl(writer, control); err != nil {
				cancelForProtocolError(fmt.Errorf("write supervisor control: %w", err))
			}

		case observerErr, ok := <-observerResults:
			observerResults = nil
			if activitySessionClosed {
				continue
			}
			if !ok {
				observerErr = errors.New(
					"daemon observer stream ended without a terminal result",
				)
			}
			if observerStream != nil {
				observerErr = errors.Join(observerErr, observerStream.Close())
				observerStream = nil
			}
			activityObserverClosed = true
			if err := streams.Activity.ObserverClosed(observerErr); err != nil {
				cancelForProtocolError(fmt.Errorf(
					"close daemon observer stream: %w",
					err,
				))
			}

		case <-heartbeat.C:
			if ready && completion == nil {
				if err := writer.Write(sessionwire.TypeHeartbeat, nil); err != nil {
					cancelForProtocolError(fmt.Errorf("write supervisor heartbeat: %w", err))
				}
			}

		case <-ctxDone:
			if !ready && completion == nil {
				_ = sshSession.Signal(ssh.SIGKILL)
				_ = sshSession.Close()
				return preCommitCancellationError(ctx)
			}
			if !cancelSent && completion == nil {
				_ = writer.Write(sessionwire.TypeCancel, nil)
				cancelSent = true
				cancelTimer = time.After(supervisorCancelBound)
			}
			ctxDone = nil

		case <-cancelTimer:
			_ = sshSession.Signal(ssh.SIGKILL)
			_ = sshSession.Close()
			return errors.Join(ctx.Err(), errors.New("guest supervisor did not stop within the cancellation bound"))

		case sshWaitErr = <-wait:
			wait = nil
		}
	}
}

type limaObserverReadResult struct {
	envelope sessionwire.ObservationEnvelope
	err      error
}

func prepareBackendActivity(
	prepared *backend.Session,
	streams backend.RunStreams,
) (*sessionwire.SupervisorActivityExpectation, bool, error) {
	if prepared == nil {
		return nil, false, errors.New("activity preparation requires a backend session")
	}
	switch {
	case prepared.Activity == nil && streams.Activity == nil:
		return nil, false, nil
	case prepared.Activity == nil:
		return nil, false, errors.New("daemon activity callbacks lack Manager-bound identity")
	case streams.Activity == nil:
		return nil, false, errors.New("Manager-bound activity lacks daemon callbacks")
	}
	if err := streams.Activity.Validate(); err != nil {
		return nil, false, err
	}
	if err := prepared.Activity.Validate(); err != nil {
		return nil, false, err
	}
	expectation, err := streams.Activity.Prepare(*prepared.Activity)
	if err != nil {
		return nil, false, err
	}
	failPrepared := func(cause error) (
		*sessionwire.SupervisorActivityExpectation,
		bool,
		error,
	) {
		expectation.ObserverStreamToken.Destroy()
		return nil, false, errors.Join(
			cause,
			streams.Activity.SessionClosed(nil, cause),
		)
	}
	if err := expectation.Validate(prepared.ID); err != nil {
		return failPrepared(err)
	}
	if !expectation.Owner.Equal(prepared.Activity.Owner) ||
		expectation.ObserverGeneration != prepared.Activity.ObserverGeneration ||
		expectation.ObserverHelperDigest != prepared.Activity.ObserverHelperDigest {
		return failPrepared(errors.New(
			"daemon activity expectation does not match Manager-bound identity",
		))
	}
	return &expectation, true, nil
}

func ingestFirstLimaObservation(
	ctx context.Context,
	stream *limaObserverStream,
	callbacks *backend.ActivityStreams,
) error {
	if stream == nil || callbacks == nil {
		return errors.New("initial activity ingestion is unavailable")
	}
	result := make(chan limaObserverReadResult, 1)
	go func() {
		envelope, _, err := stream.Read()
		result <- limaObserverReadResult{envelope: envelope, err: err}
	}()
	timer := time.NewTimer(limaObserverHandshakeTimeout)
	defer timer.Stop()
	select {
	case observed := <-result:
		if observed.err != nil {
			return observed.err
		}
		return callbacks.Observe(observed.envelope)
	case <-ctx.Done():
		_ = stream.Close()
		return ctx.Err()
	case <-timer.C:
		_ = stream.Close()
		return errors.New("initial observer event exceeded the readiness bound")
	}
}

func pumpLimaObserverStream(
	stream *limaObserverStream,
	callbacks *backend.ActivityStreams,
	results chan<- error,
) {
	defer close(results)
	for {
		envelopes, readErr := stream.ReadBatch(limaObserverBatchEnvelopes)
		terminal := false
		for index, envelope := range envelopes {
			if envelope.Kind != "collector.goodbye" {
				continue
			}
			if terminal || index != len(envelopes)-1 {
				results <- errors.New(
					"observer transport completion was not the final envelope",
				)
				return
			}
			terminal = true
		}
		if len(envelopes) != 0 {
			var observeErr error
			if callbacks.ObserveBatch != nil {
				observeErr = callbacks.ObserveBatch(envelopes)
			} else {
				for _, envelope := range envelopes {
					if observeErr = callbacks.Observe(envelope); observeErr != nil {
						break
					}
				}
			}
			if observeErr != nil {
				results <- fmt.Errorf("ingest observer envelope batch: %w", observeErr)
				return
			}
		}
		if terminal {
			trailing, terminalErr := stream.ReadBatch(1)
			if len(trailing) != 0 {
				results <- errors.New(
					"observer transport emitted an envelope after completion",
				)
				return
			}
			if !errors.Is(terminalErr, io.EOF) {
				results <- fmt.Errorf(
					"observer transport did not end after completion: %w",
					terminalErr,
				)
				return
			}
			results <- nil
			return
		}
		if readErr != nil {
			results <- readErr
			return
		}
	}
}

func awaitLimaObserverDrain(
	stream *limaObserverStream,
	results <-chan error,
) error {
	if stream == nil || results == nil {
		return errors.New("daemon observer drain is unavailable")
	}
	timer := time.NewTimer(limaObserverShutdownTimeout)
	defer timer.Stop()
	select {
	case err, ok := <-results:
		if !ok {
			return errors.New("daemon observer stream ended without a drain marker")
		}
		return err
	case <-timer.C:
		_ = stream.Close()
		return errors.New("daemon observer stream did not drain within the bound")
	}
}

func validateObserverReadyHello(
	ready *sessionwire.SupervisorActivityReady,
	hello sessionwire.ObserverHello,
) error {
	if ready == nil {
		return errors.New("observer readiness is unavailable")
	}
	if err := hello.Validate(); err != nil {
		return err
	}
	expected := make(map[string]sessionwire.SupervisorCoverageSummary, len(ready.Coverage))
	for _, summary := range ready.Coverage {
		expected[summary.Subsystem] = summary
	}
	actualValues := hello.Capabilities.Coverage()
	if len(expected) != len(actualValues) {
		return errors.New("observer hello coverage differs from supervisor readiness")
	}
	for _, actual := range actualValues {
		want, ok := expected[actual.Subsystem]
		if !ok || want.State != actual.State ||
			want.Reason != actual.Reason ||
			want.DroppedEventCount != actual.DroppedEventCount ||
			!equalObserverEvidence(want.Evidence, actual.Evidence) {
			return errors.New("observer hello coverage differs from supervisor readiness")
		}
	}
	return nil
}

func equalObserverEvidence(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneSupervisorActivityReady(
	ready *sessionwire.SupervisorActivityReady,
) *sessionwire.SupervisorActivityReady {
	if ready == nil {
		return nil
	}
	cloned := *ready
	cloned.Coverage = make([]sessionwire.SupervisorCoverageSummary, len(ready.Coverage))
	for index := range ready.Coverage {
		cloned.Coverage[index] = ready.Coverage[index]
		cloned.Coverage[index].Evidence = append(
			[]string(nil),
			ready.Coverage[index].Evidence...,
		)
	}
	return &cloned
}

func applySupervisorProjectionReadiness(
	prepared *backend.Session,
	readyProjection *sessionwire.SupervisorProjectionReadinessReady,
) error {
	if prepared == nil {
		return errors.New("guest supervisor projection readiness requires a prepared session")
	}
	if prepared.ProjectionReadiness == nil {
		if readyProjection != nil {
			return errors.New("guest supervisor reported unexpected projection readiness")
		}
		return nil
	}
	if readyProjection == nil {
		return errors.New("guest supervisor omitted projection readiness")
	}
	if readyProjection.EnvironmentID != prepared.ProjectionReadiness.Manifest.EnvironmentID ||
		readyProjection.SessionSnapshotID != prepared.ProjectionReadiness.Manifest.SessionSnapshotID {
		return errors.New("guest supervisor projection readiness identity drift")
	}
	observation := backend.ProjectionReadinessObservation{
		Status:          backend.ProjectionReadinessStatus(readyProjection.Status),
		CatalogDigest:   readyProjection.CatalogDigest,
		ExpectedEntries: readyProjection.ExpectedEntries,
		ObservedEntries: readyProjection.ObservedEntries,
		DurationMillis:  readyProjection.DurationMillis,
		TargetProjected: readyProjection.TargetProjected,
	}
	if err := observation.Validate(prepared.ProjectionReadiness); err != nil {
		return fmt.Errorf("validate guest projection readiness: %w", err)
	}
	prepared.ProjectionReadinessObservation = &observation
	return nil
}

func preCommitCancellationError(ctx context.Context) error {
	var cause error
	if ctx != nil {
		cause = ctx.Err()
	}
	if cause == nil {
		cause = context.Canceled
	}
	return &backend.ProjectionReadinessError{
		Status:     backend.ProjectionReadinessCancelled,
		ReasonCode: backend.ProjectionReadinessCancellation,
		Hint:       "run cancelled before target start; retry when the session is ready",
		Err:        cause,
	}
}

func projectionReadinessDisposition(code string) (backend.ProjectionReadinessStatus, backend.ProjectionReadinessReason, bool) {
	reason := backend.ProjectionReadinessReason(code)
	switch reason {
	case backend.ProjectionReadinessManifestMissing,
		backend.ProjectionReadinessCatalogDrift,
		backend.ProjectionReadinessIdentityDrift,
		backend.ProjectionReadinessEntryMissing,
		backend.ProjectionReadinessEntryInvalid,
		backend.ProjectionReadinessDigestMismatch:
		return backend.ProjectionReadinessRefused, reason, true
	case backend.ProjectionReadinessTimeout:
		return backend.ProjectionReadinessTimedOut, reason, true
	case backend.ProjectionReadinessCancellation:
		return backend.ProjectionReadinessCancelled, reason, true
	default:
		return "", "", false
	}
}

func classifyProjectionReadinessRunError(ctx context.Context, session *backend.Session, err error) error {
	if err == nil || session == nil || session.ProjectionReadiness == nil ||
		session.ProjectionReadinessObservation != nil {
		return err
	}
	var typed *backend.ProjectionReadinessError
	if errors.As(err, &typed) {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return preCommitCancellationError(ctx)
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "session runtime files did not become visible"):
		return &backend.ProjectionReadinessError{
			Status: backend.ProjectionReadinessTimedOut, ReasonCode: backend.ProjectionReadinessTimeout,
			Hint: "session projection did not become visible within two seconds; retry after checking the environment",
			Err:  err,
		}
	case strings.Contains(text, "guest boot identity changed"):
		return &backend.ProjectionReadinessError{
			Status: backend.ProjectionReadinessRefused, ReasonCode: backend.ProjectionReadinessIdentityDrift,
			Hint: "environment identity changed before target start; retry with the current environment",
			Err:  err,
		}
	default:
		return err
	}
}

func supervisorStartControl(
	prepared *backend.Session,
	command, env []string,
	streams backend.RunStreams,
	activity *sessionwire.SupervisorActivityExpectation,
) (*sessionwire.SupervisorStart, error) {
	terminal := sessionwire.TerminalDescriptor{Mode: sessionwire.TerminalNone}
	if streams.Terminal {
		terminal = sessionwire.TerminalDescriptor{Mode: sessionwire.TerminalPTY, Rows: streams.Rows, Columns: streams.Columns, Term: streams.Term}
		if terminal.Term == "" {
			terminal.Term = sessionwire.DefaultTERM
		}
	}
	targetEnv := env
	if prepared.Workspace.Transport == backend.WorkspaceTransportPortal {
		targetEnv = sessionViewTargetEnv(env, prepared.GuestWork)
	}
	values := make(map[string]string, len(targetEnv))
	for _, assignment := range targetEnv {
		name, value, ok := strings.Cut(assignment, "=")
		if !ok || name == "" {
			return nil, errors.New("supervisor environment contains an invalid assignment")
		}
		values[name] = value
	}
	start := &sessionwire.SupervisorStart{
		Protocol: sessionwire.SupervisorProtocol, SessionID: prepared.ID,
		TargetUser: prepared.TargetUser, GuestWork: prepared.GuestWork,
		Argv: append([]string(nil), command...), Env: values, Terminal: terminal,
		ExpectedBootID: prepared.ExpectedBootID,
		SessionSource:  GuestRuntimeDir + "/sessions/" + prepared.ID,
		Activity:       activity,
	}
	if prepared.ProjectionReadiness != nil {
		expectation := prepared.ProjectionReadiness
		start.ProjectionReadiness = &sessionwire.SupervisorProjectionReadinessExpectation{
			EnvironmentID:     expectation.Manifest.EnvironmentID,
			SessionSnapshotID: expectation.Manifest.SessionSnapshotID,
			CatalogDigest:     expectation.Manifest.CatalogDigest,
			ExpectedEntries:   len(expectation.Manifest.Entries),
			TargetProjected:   expectation.TargetProjected,
		}
	}
	if start.TargetUser == "" {
		start.TargetUser = "developer"
	}
	if err := start.Validate(); err != nil {
		return nil, err
	}
	return start, nil
}

func readSupervisorFrames(reader *sessionwire.Reader, results chan<- supervisorFrameResult) {
	defer close(results)
	for {
		frame, err := reader.ReadFrame()
		results <- supervisorFrameResult{frame: frame, err: err}
		if err != nil {
			return
		}
	}
}

func readSupervisorInput(reader io.Reader, results chan<- supervisorInputResult) {
	defer close(results)
	buffer := make([]byte, supervisorInputChunk)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			results <- supervisorInputResult{data: append([]byte(nil), buffer[:n]...)}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				results <- supervisorInputResult{err: err}
			}
			return
		}
	}
}

func writeSupervisorControl(writer *sessionwire.Writer, control backend.RunControl) error {
	switch control.Kind {
	case backend.RunControlResize:
		return writer.WriteControl(sessionwire.TypeResize, &sessionwire.Resize{Rows: control.Rows, Columns: control.Columns})
	case backend.RunControlSignal:
		return writer.WriteControl(sessionwire.TypeSignal, &sessionwire.Signal{Name: control.Signal})
	default:
		return fmt.Errorf("unsupported daemon run control %q", control.Kind)
	}
}

func writeSupervisorOutput(frame sessionwire.Frame, streams backend.RunStreams) error {
	var writer io.Writer
	switch frame.Type {
	case sessionwire.TypeTerminal:
		if !streams.Terminal {
			return errors.New("guest supervisor emitted terminal output for a pipe session")
		}
		writer = streams.PTY
	case sessionwire.TypeStdout:
		if streams.Terminal {
			return errors.New("guest supervisor emitted stdout for a PTY session")
		}
		writer = streams.Stdout
	case sessionwire.TypeStderr:
		if streams.Terminal {
			return errors.New("guest supervisor emitted stderr for a PTY session")
		}
		writer = streams.Stderr
	}
	if writer == nil {
		return errors.New("daemon stream output writer is unavailable")
	}
	_, err := writer.Write(frame.Payload)
	return err
}

func completionError(completion sessionwire.Completion) error {
	if completion.Kind == sessionwire.CompletionExit && completion.ExitCode == 0 {
		return nil
	}
	return supervisorTargetError{completion: completion}
}

type boundedBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	inputLen := len(data)
	remaining := b.limit - b.data.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.data.Write(data)
	}
	return inputLen, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}
