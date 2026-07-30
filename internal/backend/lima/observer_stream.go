package lima

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/vibe-agi/hideout/internal/sessionwire"
)

const (
	limaObserverHandshakeTimeout = 3 * time.Second
	limaObserverShutdownTimeout  = 3 * time.Second
	limaObserverReadBufferBytes  = 4 << 20
	limaObserverBatchEnvelopes   = 256
)

type observerStreamExpectation struct {
	Binding      sessionwire.ObserverBinding
	Token        sessionwire.ObserverStreamToken
	HelperDigest string
}

func (expectation observerStreamExpectation) validate() error {
	if err := expectation.Binding.Validate(); err != nil {
		return err
	}
	if err := expectation.Token.Validate(); err != nil {
		return err
	}
	if !strings.HasPrefix(expectation.HelperDigest, "sha256:") ||
		len(expectation.HelperDigest) != len("sha256:")+64 {
		return sessionwire.ErrObserverAuthentication
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(expectation.HelperDigest, "sha256:")); err != nil {
		return sessionwire.ErrObserverAuthentication
	}
	return nil
}

type limaObserverStream struct {
	session *ssh.Session
	stdout  *bufio.Reader
	hello   sessionwire.ObserverHello
	tracker *sessionwire.ObserverSequenceTracker

	readMu sync.Mutex

	waitDone chan struct{}
	waitMu   sync.Mutex
	waitErr  error

	stderr     *boundedBuffer
	stderrDone chan struct{}

	closeOnce sync.Once
	closeErr  error
}

type limaObserverHandshakeResult struct {
	hello   sessionwire.ObserverHello
	tracker *sessionwire.ObserverSequenceTracker
	err     error
}

func openLimaObserverStream(
	ctx context.Context,
	client *ssh.Client,
	expectation observerStreamExpectation,
) (*limaObserverStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return nil, errors.New("Lima observer SSH client is nil")
	}
	if err := expectation.validate(); err != nil {
		return nil, err
	}
	defer expectation.Token.Destroy()
	command, err := limaObserverStreamCommand(expectation.Binding.SessionID)
	if err != nil {
		return nil, err
	}
	sshSession, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open dedicated observer SSH channel: %w", err)
	}
	stdin, err := sshSession.StdinPipe()
	if err != nil {
		_ = sshSession.Close()
		return nil, fmt.Errorf("open observer SSH stdin: %w", err)
	}
	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		_ = sshSession.Close()
		return nil, fmt.Errorf("open observer SSH stdout: %w", err)
	}
	stderrPipe, err := sshSession.StderrPipe()
	if err != nil {
		_ = sshSession.Close()
		return nil, fmt.Errorf("open observer SSH stderr: %w", err)
	}
	stderr := &boundedBuffer{limit: supervisorStderrLimit}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderr, stderrPipe)
		close(stderrDone)
	}()
	commandLine := setupShellCommand(
		"/",
		[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		command,
	)
	if err := sshSession.Start(commandLine); err != nil {
		_ = sshSession.Close()
		<-stderrDone
		return nil, fmt.Errorf("start dedicated observer SSH channel: %w", err)
	}
	stream := &limaObserverStream{
		session:  sshSession,
		stdout:   bufio.NewReaderSize(stdout, limaObserverReadBufferBytes),
		waitDone: make(chan struct{}), stderr: stderr, stderrDone: stderrDone,
	}
	go func() {
		waitErr := sshSession.Wait()
		stream.waitMu.Lock()
		stream.waitErr = waitErr
		stream.waitMu.Unlock()
		close(stream.waitDone)
	}()

	handshake := make(chan limaObserverHandshakeResult, 1)
	go func(handshakeExpectation observerStreamExpectation) {
		defer handshakeExpectation.Token.Destroy()
		open := sessionwire.ObserverStreamOpen{
			Type: sessionwire.ObserverStreamOpenType, Schema: sessionwire.ObserverWireSchema,
			SessionID: handshakeExpectation.Binding.SessionID, Token: handshakeExpectation.Token,
		}
		if err := sessionwire.WriteObserverStreamOpen(stdin, open); err != nil {
			open.Token.Destroy()
			handshake <- limaObserverHandshakeResult{err: err}
			return
		}
		open.Token.Destroy()
		hello, err := sessionwire.ReadObserverHello(stdout)
		if err != nil {
			handshake <- limaObserverHandshakeResult{err: err}
			return
		}
		accepted, err := sessionwire.AcceptObserverHello(
			hello,
			handshakeExpectation.Binding,
			sessionwire.ObserverPeer{UID: 0, HelperDigest: handshakeExpectation.HelperDigest},
		)
		if err != nil {
			handshake <- limaObserverHandshakeResult{err: err}
			return
		}
		if err := sessionwire.WriteObserverAccepted(stdin, accepted); err != nil {
			handshake <- limaObserverHandshakeResult{err: err}
			return
		}
		if err := stdin.Close(); err != nil {
			handshake <- limaObserverHandshakeResult{err: err}
			return
		}
		tracker, err := sessionwire.NewObserverSequenceTracker(handshakeExpectation.Binding)
		handshake <- limaObserverHandshakeResult{hello: hello, tracker: tracker, err: err}
	}(expectation)

	timer := time.NewTimer(limaObserverHandshakeTimeout)
	defer timer.Stop()
	var result limaObserverHandshakeResult
	select {
	case result = <-handshake:
	case <-ctx.Done():
		result.err = ctx.Err()
	case <-timer.C:
		result.err = errors.New("dedicated observer SSH handshake exceeded the readiness bound")
	}
	if result.err != nil {
		cleanupErr := stream.closeAfterHandshakeFailure()
		return nil, formatLimaObserverStreamError(result.err, cleanupErr, stderr.String())
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := stream.closeAfterHandshakeFailure()
		return nil, formatLimaObserverStreamError(err, cleanupErr, stderr.String())
	}
	stream.hello = cloneObserverHello(result.hello)
	stream.tracker = result.tracker
	return stream, nil
}

func limaObserverStreamCommand(sessionID string) ([]string, error) {
	if !sessionViewIDPattern.MatchString(sessionID) || strings.ContainsAny(sessionID, `/\`) {
		return nil, fmt.Errorf("invalid observer stream session id %q", sessionID)
	}
	executable := path.Join(
		GuestRuntimeDir,
		"sessions",
		sessionID,
		"shims",
		"hideout-session-supervisor",
	)
	return []string{executable, "observer-stream", "--session", sessionID}, nil
}

func (stream *limaObserverStream) Read() (
	sessionwire.ObservationEnvelope,
	sessionwire.ObserverSequenceResult,
	error,
) {
	if stream == nil || stream.stdout == nil || stream.tracker == nil {
		return sessionwire.ObservationEnvelope{}, sessionwire.ObserverSequenceResult{},
			errors.New("Lima observer stream is unavailable")
	}
	stream.readMu.Lock()
	defer stream.readMu.Unlock()
	return stream.readLocked()
}

func (stream *limaObserverStream) readLocked() (
	sessionwire.ObservationEnvelope,
	sessionwire.ObserverSequenceResult,
	error,
) {
	envelope, err := sessionwire.ReadObserverEnvelope(stream.stdout)
	if err != nil {
		return sessionwire.ObservationEnvelope{}, sessionwire.ObserverSequenceResult{}, err
	}
	sequence, err := stream.tracker.Observe(envelope)
	if err != nil {
		return sessionwire.ObservationEnvelope{}, sessionwire.ObserverSequenceResult{}, err
	}
	return envelope, sequence, nil
}

// ReadBatch consumes one blocking frame and then every complete frame already
// buffered from the authenticated SSH stream. This preserves wire order while
// allowing the daemon to group durable writes without adding idle latency.
func (stream *limaObserverStream) ReadBatch(
	maximum int,
) ([]sessionwire.ObservationEnvelope, error) {
	if stream == nil || stream.stdout == nil || stream.tracker == nil ||
		maximum < 1 || maximum > limaObserverBatchEnvelopes {
		return nil, errors.New("Lima observer batch is invalid")
	}
	stream.readMu.Lock()
	defer stream.readMu.Unlock()

	result := make([]sessionwire.ObservationEnvelope, 0, maximum)
	for len(result) < maximum {
		if len(result) != 0 && !stream.completeFrameBuffered() {
			break
		}
		envelope, _, err := stream.readLocked()
		if err != nil {
			return result, err
		}
		result = append(result, envelope)
	}
	return result, nil
}

func (stream *limaObserverStream) completeFrameBuffered() bool {
	if stream == nil || stream.stdout == nil ||
		stream.stdout.Buffered() < 4 {
		return false
	}
	header, err := stream.stdout.Peek(4)
	if err != nil {
		return false
	}
	payloadBytes := binary.BigEndian.Uint32(header)
	if payloadBytes == 0 || payloadBytes > sessionwire.MaxObserverFrameSize {
		// Let the normal strict decoder surface the exact protocol error.
		return true
	}
	frameBytes := uint64(sessionwire.ObserverFrameHeaderSize) +
		uint64(payloadBytes)
	return uint64(stream.stdout.Buffered()) >= frameBytes
}

func (stream *limaObserverStream) Hello() sessionwire.ObserverHello {
	if stream == nil {
		return sessionwire.ObserverHello{}
	}
	return cloneObserverHello(stream.hello)
}

func (stream *limaObserverStream) Done() <-chan struct{} {
	if stream == nil {
		return nil
	}
	return stream.waitDone
}

func (stream *limaObserverStream) Err() error {
	if stream == nil {
		return nil
	}
	select {
	case <-stream.waitDone:
	default:
		return nil
	}
	stream.waitMu.Lock()
	defer stream.waitMu.Unlock()
	return stream.waitErr
}

func (stream *limaObserverStream) Close() error {
	if stream == nil {
		return nil
	}
	stream.closeOnce.Do(func() {
		alreadyDone := channelClosed(stream.waitDone)
		sessionErr := stream.session.Close()
		if errors.Is(sessionErr, io.EOF) {
			sessionErr = nil
		}
		var timeoutErr error
		select {
		case <-stream.waitDone:
		case <-time.After(limaObserverShutdownTimeout):
			timeoutErr = errors.New("dedicated observer SSH channel did not stop within the bound")
		}
		select {
		case <-stream.stderrDone:
		case <-time.After(limaObserverShutdownTimeout):
			timeoutErr = errors.Join(timeoutErr, errors.New("observer SSH stderr did not close"))
		}
		var terminalErr error
		if alreadyDone {
			terminalErr = stream.Err()
		}
		stream.closeErr = errors.Join(sessionErr, timeoutErr, terminalErr)
	})
	return stream.closeErr
}

func (stream *limaObserverStream) closeAfterHandshakeFailure() error {
	if stream == nil {
		return nil
	}
	_ = stream.session.Close()
	select {
	case <-stream.waitDone:
	case <-time.After(limaObserverShutdownTimeout):
		return errors.New("failed observer SSH handshake did not stop within the bound")
	}
	select {
	case <-stream.stderrDone:
	case <-time.After(limaObserverShutdownTimeout):
		return errors.New("failed observer SSH stderr did not close")
	}
	return nil
}

func formatLimaObserverStreamError(primary, cleanup error, stderr string) error {
	err := errors.Join(primary, cleanup)
	if text := strings.TrimSpace(stderr); text != "" {
		err = fmt.Errorf("%w: %s", err, text)
	}
	return fmt.Errorf("authenticate dedicated observer SSH channel: %w", err)
}

func cloneObserverHello(hello sessionwire.ObserverHello) sessionwire.ObserverHello {
	hello.Capabilities.Process.Evidence = append(
		[]string(nil),
		hello.Capabilities.Process.Evidence...,
	)
	hello.Capabilities.File.Evidence = append(
		[]string(nil),
		hello.Capabilities.File.Evidence...,
	)
	hello.Capabilities.Network.Evidence = append(
		[]string(nil),
		hello.Capabilities.Network.Evidence...,
	)
	hello.Capabilities.DNS.Evidence = append(
		[]string(nil),
		hello.Capabilities.DNS.Evidence...,
	)
	return hello
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
