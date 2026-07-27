package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	defaultSessionLease      = 90 * time.Second
	defaultSessionRenewal    = 30 * time.Second
	defaultSessionWriteBound = 10 * time.Second
	sessionStreamQueueDepth  = 16
	sessionStreamChunkSize   = 32 << 10
)

type sessionServer struct {
	core               manager.Core
	credentials        *credentialManager
	instanceID         string
	registry           *sessionRegistry
	backendFactory     manager.RunServiceBackendFactory
	openerFactory      manager.RunServiceOpenerFactory
	audit              *auditLog
	leaseDuration      time.Duration
	renewalInterval    time.Duration
	writeTimeout       time.Duration
	lifecycle          lifecycle.Registrar
	workspaceProviders workspaceattach.ProviderFactory
}

func (s *sessionServer) serve(listener net.Listener) error {
	if listener == nil {
		return errors.New("daemon session listener is required")
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serveConn(conn)
	}
}

func (s *sessionServer) serveConn(conn net.Conn) {
	if conn == nil {
		return
	}
	defer conn.Close()
	reader := sessionwire.NewReader(conn, sessionwire.ClientToDaemon)
	writer := sessionwire.NewWriter(sessionDeadlineWriter{Conn: conn, Timeout: s.writeBound()}, sessionwire.DaemonToClient)

	hello, generation, err := s.readHello(reader)
	if err != nil {
		s.recordAuthRefusal(err)
		_ = writeSessionError(writer, "session.auth.failed", "session authentication failed", false)
		return
	}
	connectionID, err := newConnectionID()
	if err != nil {
		_ = writeSessionError(writer, "session.start.failed", err.Error(), false)
		return
	}
	if err := writer.WriteControl(sessionwire.TypeHelloAccepted, &sessionwire.HelloAccepted{
		Protocol: sessionwire.Protocol, ConnectionID: connectionID, InstanceID: s.instanceID,
		CredentialGeneration: generation, RenewalIntervalMs: durationMillis(s.renewal()),
		LeaseDurationMs: durationMillis(s.lease()),
	}); err != nil {
		return
	}
	_ = hello

	req, err := readSessionRunRequest(reader)
	if err != nil {
		_ = writeSessionError(writer, "session.request.invalid", err.Error(), false)
		return
	}
	service := manager.RunService{Core: s.core}
	prepared, err := service.Prepare(req)
	if err != nil {
		_ = writeSessionError(writer, "session.plan.failed", err.Error(), false)
		return
	}
	if err := writer.WriteControl(sessionwire.TypeReview, &sessionwire.Review{
		PlanVersion: prepared.Review.PlanVersion, PlanDigest: prepared.Review.PlanDigest,
		ConfirmationRequired: prepared.Review.RequiresConfirmation,
		Summary:              runReviewSummary(prepared.Review),
	}); err != nil {
		return
	}
	for _, notice := range prepared.Review.Notices {
		if err := writer.WriteControl(sessionwire.TypeNotice, &sessionwire.Notice{Code: notice.Code, Summary: notice.Summary}); err != nil {
			return
		}
	}
	if prepared.Review.RequiresConfirmation {
		confirmation, confirmErr := readSessionConfirmation(reader, prepared.Review)
		if confirmErr != nil {
			_ = writeSessionError(writer, "session.confirmation.denied", confirmErr.Error(), false)
			return
		}
		req.Confirmation = confirmation
	}
	if s.backendFactory == nil {
		_ = writeSessionError(writer, "session.backend.unavailable", "daemon run backend is unavailable", false)
		return
	}
	be, err := s.backendFactory(req, prepared.Plan)
	if err != nil {
		_ = writeSessionError(writer, "session.backend.unavailable", err.Error(), false)
		return
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	worker, err := s.registry.register(connectionID, cancelRun)
	if err != nil {
		cancelRun()
		_ = writeSessionError(writer, "session.capacity.refused", err.Error(), true)
		return
	}
	defer func() {
		cancelRun()
		cleanup := ""
		if err := worker.releaseWorkspaceAttachment(context.Background()); err != nil {
			cleanup = err.Error()
		}
		s.registry.finish(connectionID, cleanup)
	}()

	stdinReader, stdinWriter := io.Pipe()
	stdinQueue := make(chan []byte, sessionStreamQueueDepth)
	controls := make(chan backend.RunControl, sessionStreamQueueDepth)
	inputErr := make(chan error, 1)
	leaseReset := make(chan struct{}, 1)
	leaseDeadline := newSessionLeaseDeadline(s.lease())
	go pumpSessionStdin(runCtx, stdinWriter, stdinQueue)
	go s.watchSessionLease(runCtx, cancelRun, leaseDeadline, leaseReset)
	go s.readSessionInput(runCtx, cancelRun, reader, generation, stdinQueue, controls, stdinWriter, leaseDeadline, leaseReset, inputErr)

	var readyMu sync.Mutex
	ready := false
	streams := &backend.RunStreams{
		Terminal: req.Terminal.Mode == "pty", Rows: req.Terminal.Rows,
		Columns: req.Terminal.Columns, Term: req.Terminal.TERM, Stdin: stdinReader,
		Stdout:   &sessionFrameWriter{writer: writer, frameType: sessionwire.TypeStdout},
		Stderr:   &sessionFrameWriter{writer: writer, frameType: sessionwire.TypeStderr},
		PTY:      &sessionFrameWriter{writer: writer, frameType: sessionwire.TypeTerminal},
		Controls: controls,
		Ready: func(proof backend.SessionReadyProof) error {
			if err := proof.Validate(); err != nil {
				return fmt.Errorf("backend reported invalid ready proof: %w", err)
			}
			readyMu.Lock()
			defer readyMu.Unlock()
			if ready {
				return errors.New("backend reported session ready more than once")
			}
			if err := worker.markStarted(sessionStart{
				SessionID: proof.SessionID, EnvironmentID: proof.EnvironmentID,
				Profile: prepared.Plan.ProfileName, Backend: prepared.Plan.Backend,
				TerminalMode:      string(req.Terminal.Mode),
				SessionSnapshotID: proof.SessionSnapshotID,
				CommandClass:      filepath.Base(prepared.Plan.Command[0]),
			}); err != nil {
				return err
			}
			if err := writer.WriteControl(sessionwire.TypeStarted, &sessionwire.Started{
				SessionID: proof.SessionID, EnvironmentID: proof.EnvironmentID,
				Terminal: sessionWireTerminal(req.Terminal), RenewalIntervalMs: durationMillis(s.renewal()),
			}); err != nil {
				return err
			}
			ready = true
			return nil
		},
	}
	opener := func(runSession manager.RunSession) broker.Opener {
		if s.openerFactory == nil {
			return nil
		}
		return s.openerFactory(req, prepared.Plan, runSession)
	}
	var openerForSession func(manager.RunSession) broker.Opener
	if s.openerFactory != nil {
		openerForSession = opener
	}
	// Human startup progress (slow VM boot notice, heartbeat) must reach the
	// operator's terminal, not the daemon's detached stderr.
	if redirector, ok := be.(backend.ProgressRedirector); ok {
		be = redirector.WithProgress(streams.Stderr)
	}
	result, runErr := service.Apply(runCtx, prepared, req, manager.RunServiceDependencies{
		Backend: be, OpenerForSession: openerForSession,
		PrepareWorkspaceAttachment: func(runSession *manager.RunSession) error {
			return worker.prepareWorkspaceAttachment(runCtx, s.workspaceProviders, runSession)
		},
		ActivateWorkspaceAttachment: func(runSession *manager.RunSession) error {
			return worker.activateWorkspaceAttachment(runCtx, runSession)
		},
		ReleaseWorkspaceAttachment: worker.releaseWorkspaceAttachment,
		Streams:                    streams, Lifecycle: s.lifecycle,
	})
	_ = stdinReader.Close()
	select {
	case err := <-inputErr:
		if err != nil && runErr == nil {
			runErr = err
		}
	default:
	}
	readyMu.Lock()
	wasReady := ready
	readyMu.Unlock()
	if !wasReady {
		if runErr == nil {
			runErr = errors.New("backend completed without reporting session ready")
		}
		_ = writeSessionError(writer, "session.start.failed", runErr.Error(), false)
		return
	}
	if cleanupErr := worker.releaseWorkspaceAttachment(context.Background()); cleanupErr != nil {
		if result.CleanupError == "" {
			result.CleanupError = cleanupErr.Error()
		} else {
			result.CleanupError += "; " + cleanupErr.Error()
		}
	}
	completion := completionForRun(result, runErr)
	if err := writer.WriteControl(sessionwire.TypeCompletion, &completion); err != nil {
		return
	}
	if result.CleanupError != "" {
		s.registry.finish(connectionID, result.CleanupError)
	} else {
		s.registry.finish(connectionID, "")
	}
}

func (s *sessionServer) readHello(reader *sessionwire.Reader) (*sessionwire.Hello, uint64, error) {
	if s == nil || s.credentials == nil || s.instanceID == "" || s.registry == nil {
		return nil, 0, errors.New("daemon session server is not initialized")
	}
	frame, err := reader.ReadFrame()
	if err != nil {
		return nil, 0, err
	}
	if frame.Type != sessionwire.TypeHello {
		return nil, 0, errors.New("first session frame must be hello")
	}
	control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return nil, 0, err
	}
	hello := control.(*sessionwire.Hello)
	generation, ok := s.credentials.ValidateGeneration(hello.Token)
	if !ok {
		return nil, 0, errors.New("operator credential is invalid")
	}
	return hello, generation, nil
}

func readSessionRunRequest(reader *sessionwire.Reader) (manager.RunServiceRequest, error) {
	frame, err := reader.ReadFrame()
	if err != nil {
		return manager.RunServiceRequest{}, err
	}
	if frame.Type != sessionwire.TypeRunRequest {
		return manager.RunServiceRequest{}, errors.New("hello must be followed by one run request")
	}
	control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return manager.RunServiceRequest{}, err
	}
	metadata := control.(*sessionwire.RunRequestMetadata)
	var req manager.RunServiceRequest
	decoder := json.NewDecoder(bytes.NewReader(metadata.Request))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid canonical run request")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return req, errors.New("invalid canonical run request")
	}
	if req.Confirmation != nil {
		return req, errors.New("session request confirmation must use a separate frame")
	}
	return req, nil
}

func readSessionConfirmation(reader *sessionwire.Reader, review manager.RunReview) (*manager.RunConfirmation, error) {
	frame, err := reader.ReadFrame()
	if err != nil {
		return nil, err
	}
	if frame.Type != sessionwire.TypeConfirm {
		return nil, errors.New("run confirmation is required")
	}
	control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return nil, err
	}
	confirm := control.(*sessionwire.Confirm)
	if !confirm.Accepted {
		return nil, errors.New("run confirmation was denied")
	}
	if confirm.PlanVersion != review.PlanVersion || confirm.PlanDigest != review.PlanDigest {
		return nil, manager.ErrRunPlanStale
	}
	return &manager.RunConfirmation{PlanVersion: confirm.PlanVersion, PlanDigest: confirm.PlanDigest, Accepted: true}, nil
}

func (s *sessionServer) readSessionInput(
	ctx context.Context,
	cancel context.CancelFunc,
	reader *sessionwire.Reader,
	acceptedGeneration uint64,
	stdinQueue chan<- []byte,
	controls chan<- backend.RunControl,
	stdinWriter *io.PipeWriter,
	leaseDeadline *sessionLeaseDeadline,
	leaseReset chan<- struct{},
	errOut chan<- error,
) {
	defer close(stdinQueue)
	defer close(controls)
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				select {
				case errOut <- err:
				default:
				}
			}
			cancel()
			return
		}
		switch frame.Type {
		case sessionwire.TypeStdin:
			chunk := append([]byte(nil), frame.Payload...)
			select {
			case stdinQueue <- chunk:
			case <-ctx.Done():
				return
			default:
				select {
				case errOut <- errors.New("session stdin backpressure limit exceeded"):
				default:
				}
				cancel()
				return
			}
		case sessionwire.TypeStdinEOF:
			_ = stdinWriter.Close()
		case sessionwire.TypeResize:
			control, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				select {
				case errOut <- decodeErr:
				default:
				}
				cancel()
				return
			}
			resize := control.(*sessionwire.Resize)
			if !sendRunControl(ctx, controls, backend.RunControl{Kind: backend.RunControlResize, Rows: resize.Rows, Columns: resize.Columns}) {
				cancel()
				return
			}
		case sessionwire.TypeSignal:
			control, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				select {
				case errOut <- decodeErr:
				default:
				}
				cancel()
				return
			}
			signal := control.(*sessionwire.Signal)
			if !sendRunControl(ctx, controls, backend.RunControl{Kind: backend.RunControlSignal, Signal: signal.Name}) {
				cancel()
				return
			}
		case sessionwire.TypeCancel:
			cancel()
			return
		case sessionwire.TypeRenew:
			control, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				cancel()
				return
			}
			renew := control.(*sessionwire.Renew)
			generation, ok := s.credentials.ValidateGeneration(renew.Token)
			// The credential manager is authoritative for generation. A client can
			// miss more than one atomic token rotation between reads, so its
			// monotonic hint may lag the validated token's actual generation. Never
			// accept an older token or a hint that claims a future generation.
			if !ok || generation < acceptedGeneration || renew.CredentialGeneration > generation {
				cancel()
				return
			}
			acceptedGeneration = generation
			leaseDeadline.renew(s.lease())
			select {
			case leaseReset <- struct{}{}:
			default:
			}
		default:
			select {
			case errOut <- fmt.Errorf("unexpected session frame %s after start", frame.Type):
			default:
			}
			cancel()
			return
		}
	}
}

func pumpSessionStdin(ctx context.Context, writer *io.PipeWriter, chunks <-chan []byte) {
	defer writer.Close()
	for {
		select {
		case <-ctx.Done():
			_ = writer.CloseWithError(ctx.Err())
			return
		case chunk, ok := <-chunks:
			if !ok {
				return
			}
			if _, err := writer.Write(chunk); err != nil {
				return
			}
		}
	}
}

func sendRunControl(ctx context.Context, controls chan<- backend.RunControl, control backend.RunControl) bool {
	select {
	case controls <- control:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

type sessionLeaseDeadline struct {
	value atomic.Pointer[time.Time]
}

func newSessionLeaseDeadline(duration time.Duration) *sessionLeaseDeadline {
	deadline := &sessionLeaseDeadline{}
	deadline.renew(duration)
	return deadline
}

func (d *sessionLeaseDeadline) renew(duration time.Duration) {
	value := time.Now().Add(duration)
	d.value.Store(&value)
}

func (d *sessionLeaseDeadline) remaining() time.Duration {
	value := d.value.Load()
	if value == nil {
		return 0
	}
	return time.Until(*value)
}

func resetSessionLeaseTimer(timer *time.Timer, deadline *sessionLeaseDeadline) bool {
	remaining := deadline.remaining()
	if remaining <= 0 {
		return false
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(remaining)
	return true
}

func (s *sessionServer) watchSessionLease(
	ctx context.Context,
	cancel context.CancelFunc,
	deadline *sessionLeaseDeadline,
	reset <-chan struct{},
) {
	timer := time.NewTimer(deadline.remaining())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// A validated renewal may have extended the authoritative deadline
			// while both the coalesced wakeup and the old timer were ready. Re-read
			// the deadline before expiring instead of relying on select ordering.
			if resetSessionLeaseTimer(timer, deadline) {
				continue
			}
			cancel()
			return
		case <-reset:
			if !resetSessionLeaseTimer(timer, deadline) {
				cancel()
				return
			}
		}
	}
}

type sessionFrameWriter struct {
	writer    *sessionwire.Writer
	frameType sessionwire.Type
}

func (w *sessionFrameWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		size := len(data)
		if size > sessionStreamChunkSize {
			size = sessionStreamChunkSize
		}
		if err := w.writer.Write(w.frameType, data[:size]); err != nil {
			return written, err
		}
		written += size
		data = data[size:]
	}
	return written, nil
}

type sessionDeadlineWriter struct {
	net.Conn
	Timeout time.Duration
}

func (w sessionDeadlineWriter) Write(data []byte) (int, error) {
	if w.Timeout > 0 {
		_ = w.Conn.SetWriteDeadline(time.Now().Add(w.Timeout))
	}
	return w.Conn.Write(data)
}

func completionForRun(result manager.RunResult, runErr error) sessionwire.Completion {
	resultData, _ := json.Marshal(result)
	completion := sessionwire.Completion{
		Kind: sessionwire.CompletionExit, ExitCode: 0, TargetCompleted: true,
		CleanupCompleted: result.CleanupError == "", SessionID: result.SessionID,
		Result: resultData,
	}
	if result.CleanupError != "" {
		completion.Kind = sessionwire.CompletionCleanupError
		completion.ExitCode = 1
		completion.TargetCompleted = runErr == nil
		completion.Summary = safeSessionText(result.CleanupError, 4096)
		return completion
	}
	if runErr == nil {
		return completion
	}
	if errors.Is(runErr, context.Canceled) {
		completion.Kind = sessionwire.CompletionCancelled
		completion.ExitCode = 130
		completion.TargetCompleted = false
		completion.Summary = "session cancelled"
		return completion
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		completion.ExitCode = exitErr.ExitCode()
		completion.Summary = "target exited with a non-zero status"
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			completion.Kind = sessionwire.CompletionSignal
			completion.Signal = portableSignal(status.Signal())
			completion.ExitCode = 128 + int(status.Signal())
		}
		return completion
	}
	type exitCoder interface{ ExitCode() int }
	var coder exitCoder
	type signalExitCoder interface {
		ExitCode() int
		SignalName() string
	}
	var signalCoder signalExitCoder
	if errors.As(runErr, &signalCoder) && signalCoder.SignalName() != "" {
		completion.Kind = sessionwire.CompletionSignal
		completion.Signal = signalCoder.SignalName()
		completion.ExitCode = signalCoder.ExitCode()
		completion.Summary = "target terminated by signal"
		return completion
	}
	if errors.As(runErr, &coder) && coder.ExitCode() >= 0 && coder.ExitCode() <= 255 {
		completion.ExitCode = coder.ExitCode()
		completion.Summary = "target exited with a non-zero status"
		return completion
	}
	completion.Kind = sessionwire.CompletionProtocolError
	completion.ExitCode = 1
	completion.TargetCompleted = false
	completion.Summary = safeSessionText(runErr.Error(), 4096)
	return completion
}

func portableSignal(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGTSTP:
		return "SIGTSTP"
	case syscall.SIGCONT:
		return "SIGCONT"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return "SIGTERM"
	}
}

func writeSessionError(writer *sessionwire.Writer, code, summary string, retryable bool) error {
	return writer.WriteControl(sessionwire.TypeError, &sessionwire.Error{
		Code: code, Summary: safeSessionText(summary, 4096), Retryable: retryable,
	})
}

func runReviewSummary(review manager.RunReview) string {
	command := "command"
	if len(review.Command) > 0 {
		command = review.Command[0]
	}
	return safeSessionText(fmt.Sprintf("Run %s with profile %s on %s", command, review.Profile, review.Backend), 8192)
}

func safeSessionText(value string, limit int) string {
	value = audit.RedactString(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (unicode.IsPrint(r) && r != '\u001b') {
			return r
		}
		return -1
	}, value)
	if value == "" {
		value = "session operation failed"
	}
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func newConnectionID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create session connection id: %w", err)
	}
	return "conn_" + hex.EncodeToString(value[:]), nil
}

func sessionWireTerminal(value manager.TerminalDescriptor) sessionwire.TerminalDescriptor {
	return sessionwire.TerminalDescriptor{
		Mode: sessionwire.TerminalMode(value.Mode), Rows: value.Rows,
		Columns: value.Columns, Term: value.TERM,
	}
}

func durationMillis(value time.Duration) uint32 {
	millis := value / time.Millisecond
	if millis < 1 {
		return 1
	}
	if millis > time.Duration(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(millis)
}

func (s *sessionServer) lease() time.Duration {
	if s.leaseDuration > 0 {
		return s.leaseDuration
	}
	return defaultSessionLease
}

func (s *sessionServer) renewal() time.Duration {
	if s.renewalInterval > 0 && s.renewalInterval < s.lease() {
		return s.renewalInterval
	}
	return defaultSessionRenewal
}

func (s *sessionServer) writeBound() time.Duration {
	if s.writeTimeout > 0 {
		return s.writeTimeout
	}
	return defaultSessionWriteBound
}

func (s *sessionServer) recordAuthRefusal(err error) {
	if s.audit == nil {
		return
	}
	s.audit.record("daemon.session.auth", "deny", map[string]any{
		"channel": "session-socket", "reason": safeSessionText(err.Error(), 512),
	})
}
