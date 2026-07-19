package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

type SessionClientOptions struct {
	Store         profile.Store
	Request       manager.RunServiceRequest
	ClientVersion string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	Terminal      io.Writer
	Controls      <-chan backend.RunControl
	Confirm       func(sessionwire.Review) (bool, error)
	OnReview      func(sessionwire.Review)
	BeforeIO      func(sessionwire.Started) error
	OnStarted     func(sessionwire.Started)
	OnNotice      func(sessionwire.Notice)

	Dial      func(context.Context, string) (net.Conn, error)
	ReadToken func(string) (string, error)
}

type SessionClientResult struct {
	Review     sessionwire.Review
	Started    sessionwire.Started
	Completion sessionwire.Completion
	RunResult  manager.RunResult
}

type RemoteExitError struct {
	Completion sessionwire.Completion
}

func (e RemoteExitError) Error() string {
	if e.Completion.Summary != "" {
		return e.Completion.Summary
	}
	return fmt.Sprintf("remote target exited with status %d", e.Completion.ExitCode)
}

func (e RemoteExitError) ExitCode() int { return e.Completion.ExitCode }

type SessionRemoteError struct {
	Code      string
	Summary   string
	Retryable bool
}

func (e SessionRemoteError) Error() string {
	if e.Code == "" {
		return e.Summary
	}
	return "code=" + e.Code + ": " + e.Summary
}

func RunSessionClient(ctx context.Context, opts SessionClientOptions) (SessionClientResult, error) {
	var result SessionClientResult
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Store.Root == "" {
		store, err := profile.DefaultStore()
		if err != nil {
			return result, err
		}
		opts.Store = store
	}
	if opts.Dial == nil {
		opts.Dial = dialSessionSocket
	}
	if opts.ReadToken == nil {
		opts.ReadToken = readToken
	}
	token, err := opts.ReadToken(opts.Store.Root)
	if err != nil {
		return result, fmt.Errorf("daemon session credential: %w", err)
	}
	conn, err := opts.Dial(ctx, SessionSocketPath(opts.Store.Root))
	if err != nil {
		return result, fmt.Errorf("connect daemon session transport: %w", err)
	}
	defer conn.Close()
	reader := sessionwire.NewReader(conn, sessionwire.DaemonToClient)
	writer := sessionwire.NewWriter(sessionDeadlineWriter{Conn: conn, Timeout: defaultSessionWriteBound}, sessionwire.ClientToDaemon)
	if err := writer.WriteControl(sessionwire.TypeHello, &sessionwire.Hello{
		Protocol: sessionwire.Protocol, Token: token, ClientVersion: opts.ClientVersion,
	}); err != nil {
		return result, err
	}
	frame, err := reader.ReadFrame()
	if err != nil {
		return result, err
	}
	if frame.Type == sessionwire.TypeError {
		return result, decodeRemoteSessionError(frame)
	}
	if frame.Type != sessionwire.TypeHelloAccepted {
		return result, fmt.Errorf("daemon session expected hello-accepted, got %s", frame.Type)
	}
	control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return result, err
	}
	accepted := *control.(*sessionwire.HelloAccepted)
	requestData, err := json.Marshal(opts.Request)
	if err != nil {
		return result, fmt.Errorf("encode canonical run request: %w", err)
	}
	requestID, err := newConnectionID()
	if err != nil {
		return result, err
	}
	if err := writer.WriteControl(sessionwire.TypeRunRequest, &sessionwire.RunRequestMetadata{
		Schema: sessionwire.RunRequestSchema, RequestID: requestID, Request: requestData,
	}); err != nil {
		return result, err
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go func() {
		select {
		case <-ctx.Done():
			_ = writer.Write(sessionwire.TypeCancel, nil)
			cancelRun()
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		case <-runCtx.Done():
		}
	}()
	started := false
	completed := false
	var renewalOnce sync.Once
	var ioPumpsOnce sync.Once
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if completed {
				return result, nil
			}
			return result, fmt.Errorf("daemon session ended before completion: %w", err)
		}
		if frame.Type.IsExtension() {
			continue
		}
		switch frame.Type {
		case sessionwire.TypeReview:
			if result.Review.PlanDigest != "" || started {
				return result, errors.New("daemon sent duplicate or late run review")
			}
			decoded, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				return result, decodeErr
			}
			result.Review = *decoded.(*sessionwire.Review)
			if opts.OnReview != nil {
				opts.OnReview(result.Review)
			}
			if result.Review.ConfirmationRequired {
				if opts.Confirm == nil {
					return result, errors.New("run confirmation is required but no client confirmation handler is available")
				}
				allow, confirmErr := opts.Confirm(result.Review)
				if confirmErr != nil {
					return result, confirmErr
				}
				if err := writer.WriteControl(sessionwire.TypeConfirm, &sessionwire.Confirm{
					PlanVersion: result.Review.PlanVersion, PlanDigest: result.Review.PlanDigest, Accepted: allow,
				}); err != nil {
					return result, err
				}
				if !allow {
					return result, errors.New("run confirmation was denied")
				}
			}
			// Start lease renewal after the review/confirmation exchange, before
			// backend activation. A cold Lima start can legitimately take longer
			// than one session lease, while stdin and terminal controls must still
			// remain gated on Started.
			renewalOnce.Do(func() {
				go renewSessionCredential(runCtx, writer, opts.Store.Root, token, accepted, opts.ReadToken)
			})
		case sessionwire.TypeStarted:
			if started || result.Review.PlanDigest == "" {
				return result, errors.New("daemon sent duplicate or unreviewed session start")
			}
			decoded, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				return result, decodeErr
			}
			result.Started = *decoded.(*sessionwire.Started)
			if opts.BeforeIO != nil {
				if err := opts.BeforeIO(result.Started); err != nil {
					_ = writer.Write(sessionwire.TypeCancel, nil)
					return result, err
				}
			}
			started = true
			if opts.OnStarted != nil {
				opts.OnStarted(result.Started)
			}
			ioPumpsOnce.Do(func() {
				go pumpClientStdin(runCtx, writer, opts.Stdin)
				go pumpClientControls(runCtx, writer, opts.Controls)
			})
		case sessionwire.TypeTerminal, sessionwire.TypeStdout, sessionwire.TypeStderr:
			if !started {
				if frame.Type != sessionwire.TypeStderr {
					return result, fmt.Errorf("daemon sent %s before session start", frame.Type)
				}
				// Pre-start stderr carries the daemon's control-plane startup
				// progress (slow VM boot notice, heartbeat): the target has
				// not run yet, so display it. Target data channels (stdout,
				// terminal) stay refused until the started control frame.
				if opts.Stderr != nil {
					if _, err := opts.Stderr.Write(frame.Payload); err != nil {
						return result, err
					}
				}
				break
			}
			var dst io.Writer
			switch frame.Type {
			case sessionwire.TypeTerminal:
				dst = opts.Terminal
				if dst == nil {
					dst = opts.Stdout
				}
			case sessionwire.TypeStdout:
				dst = opts.Stdout
			case sessionwire.TypeStderr:
				dst = opts.Stderr
			}
			if dst != nil {
				if _, err := dst.Write(frame.Payload); err != nil {
					return result, err
				}
			}
		case sessionwire.TypeNotice:
			decoded, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				return result, decodeErr
			}
			if opts.OnNotice != nil {
				opts.OnNotice(*decoded.(*sessionwire.Notice))
			}
		case sessionwire.TypeError:
			return result, decodeRemoteSessionError(frame)
		case sessionwire.TypeCompletion:
			if !started || completed {
				return result, errors.New("daemon sent duplicate or unstarted completion")
			}
			decoded, decodeErr := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if decodeErr != nil {
				return result, decodeErr
			}
			result.Completion = *decoded.(*sessionwire.Completion)
			if len(result.Completion.Result) != 0 {
				decoder := json.NewDecoder(bytes.NewReader(result.Completion.Result))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&result.RunResult); err != nil {
					return result, errors.New("daemon session completion contains an invalid run result")
				}
				if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
					return result, errors.New("daemon session completion contains an invalid run result")
				}
				if result.RunResult.SessionID != "" && result.RunResult.SessionID != result.Started.SessionID {
					return result, errors.New("daemon session completion changed session identity")
				}
			}
			completed = true
			cancelRun()
			if result.Completion.ExitCode != 0 || result.Completion.Kind != sessionwire.CompletionExit || !result.Completion.CleanupCompleted {
				return result, RemoteExitError{Completion: result.Completion}
			}
			return result, nil
		default:
			return result, fmt.Errorf("unexpected daemon session frame %s", frame.Type)
		}
	}
}

func dialSessionSocket(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func pumpClientStdin(ctx context.Context, writer *sessionwire.Writer, stdin io.Reader) {
	if stdin == nil {
		_ = writer.Write(sessionwire.TypeStdinEOF, nil)
		return
	}
	buffer := make([]byte, sessionStreamChunkSize)
	for {
		n, err := stdin.Read(buffer)
		if n > 0 {
			if writeErr := writer.Write(sessionwire.TypeStdin, buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = writer.Write(sessionwire.TypeStdinEOF, nil)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func pumpClientControls(ctx context.Context, writer *sessionwire.Writer, controls <-chan backend.RunControl) {
	if controls == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case control, ok := <-controls:
			if !ok {
				return
			}
			switch control.Kind {
			case backend.RunControlResize:
				if writer.WriteControl(sessionwire.TypeResize, &sessionwire.Resize{Rows: control.Rows, Columns: control.Columns}) != nil {
					return
				}
			case backend.RunControlSignal:
				if writer.WriteControl(sessionwire.TypeSignal, &sessionwire.Signal{Name: control.Signal}) != nil {
					return
				}
			}
		}
	}
}

func renewSessionCredential(
	ctx context.Context,
	writer *sessionwire.Writer,
	storeRoot, initialToken string,
	accepted sessionwire.HelloAccepted,
	readToken func(string) (string, error),
) {
	interval := time.Duration(accepted.RenewalIntervalMs) * time.Millisecond
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	token := initialToken
	generation := accepted.CredentialGeneration
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := readToken(storeRoot)
			if err != nil {
				return
			}
			if current != token {
				token = current
				generation++
			}
			if writer.WriteControl(sessionwire.TypeRenew, &sessionwire.Renew{
				Token: token, CredentialGeneration: generation,
			}) != nil {
				return
			}
		}
	}
}

func decodeRemoteSessionError(frame sessionwire.Frame) error {
	control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return err
	}
	remote := control.(*sessionwire.Error)
	return SessionRemoteError{Code: remote.Code, Summary: remote.Summary, Retryable: remote.Retryable}
}
