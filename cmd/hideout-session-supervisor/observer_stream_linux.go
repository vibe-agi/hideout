//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	"golang.org/x/sys/unix"
)

const (
	observerStreamRuntimeRoot    = "/run/hideout-observer-streams"
	observerRelayQueueEntries    = 4096
	observerRelayHandshakeWait   = 3 * time.Second
	maxObserverRelayAuthAttempts = 8
	observerRelayShutdownWait    = 3 * time.Second
)

type observerRelayOptions struct {
	Root                      string
	QueueEntries              int
	QueueBytes                int
	HandshakeWait             time.Duration
	PeerUID                   func(*net.UnixConn) (uint32, error)
	MonotonicNS               func() (uint64, error)
	SkipRootOwnerCheckForTest bool
}

type observerRelay struct {
	root       string
	socketPath string
	binding    sessionwire.ObserverBinding
	hello      sessionwire.ObserverHello
	token      sessionwire.ObserverStreamToken
	queue      *sessionwire.ObserverQueue
	listener   *net.UnixListener
	options    observerRelayOptions

	authenticated     chan struct{}
	authenticatedOnce sync.Once
	done              chan struct{}
	closeOnce         sync.Once
	closing           atomic.Bool
	lossSequence      atomic.Uint64

	connMu sync.Mutex
	conn   *net.UnixConn
	errMu  sync.Mutex
	err    error
}

func newObserverRelay(
	binding sessionwire.ObserverBinding,
	hello sessionwire.ObserverHello,
	token sessionwire.ObserverStreamToken,
	options observerRelayOptions,
) (*observerRelay, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if err := hello.Validate(); err != nil {
		return nil, err
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}
	if !hello.Owner.Equal(binding.Owner) ||
		hello.SessionID != binding.SessionID ||
		hello.EnvironmentID != binding.EnvironmentID ||
		hello.BackendIncarnationID != binding.BackendIncarnationID ||
		hello.GuestBootID != binding.GuestBootID ||
		hello.CgroupID != binding.CgroupID ||
		hello.ObserverGeneration != binding.ObserverGeneration {
		return nil, sessionwire.ErrObserverIdentity
	}
	if options.Root == "" {
		options.Root = observerStreamRuntimeRoot
	}
	if options.QueueEntries == 0 {
		options.QueueEntries = observerRelayQueueEntries
	}
	if options.QueueBytes == 0 {
		options.QueueBytes = sessionwire.DefaultObserverQueueBytes
	}
	if options.HandshakeWait <= 0 {
		options.HandshakeWait = observerRelayHandshakeWait
	}
	if options.PeerUID == nil {
		options.PeerUID = observerUnixPeerUID
	}
	if options.MonotonicNS == nil {
		options.MonotonicNS = supervisorMonotonicNS
	}
	if err := prepareObserverRelayRoot(options.Root, options.SkipRootOwnerCheckForTest); err != nil {
		return nil, err
	}
	socketPath, err := observerRelaySocketPath(options.Root, binding.SessionID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(socketPath); err == nil {
		return nil, errors.New("observer relay socket already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	queue, err := sessionwire.NewObserverQueueWithByteLimit(
		options.QueueEntries,
		options.QueueBytes,
	)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		queue.Close()
		return nil, fmt.Errorf("listen on observer relay socket: %w", err)
	}
	cleanup := func() {
		_ = listener.Close()
		queue.Close()
		_ = os.Remove(socketPath)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("protect observer relay socket: %w", err)
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		cleanup()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 ||
		(!options.SkipRootOwnerCheckForTest && stat.Uid != 0) {
		cleanup()
		return nil, errors.New("observer relay endpoint is not a root-owned private Unix socket")
	}
	relay := &observerRelay{
		root:          options.Root,
		socketPath:    socketPath,
		binding:       binding,
		hello:         hello,
		token:         token,
		queue:         queue,
		listener:      listener,
		options:       options,
		authenticated: make(chan struct{}),
		done:          make(chan struct{}),
	}
	go relay.serve(token)
	return relay, nil
}

func prepareObserverRelayRoot(root string, skipOwnerCheck bool) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("observer relay root must be a clean absolute path")
	}
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create observer relay root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		(!skipOwnerCheck && stat.Uid != 0) {
		uid := uint32(math.MaxUint32)
		if ok {
			uid = stat.Uid
		}
		return fmt.Errorf(
			"observer relay root is not a root-owned private directory: mode=%#o uid=%d",
			info.Mode().Perm(),
			uid,
		)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return fmt.Errorf("protect observer relay root: %w", err)
	}
	info, err = os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("observer relay root permissions could not be made private")
	}
	return nil
}

func observerRelaySocketPath(root, sessionID string) (string, error) {
	if len(sessionID) > maxSessionIDBytes || !sessionIDPattern.MatchString(sessionID) {
		return "", fmt.Errorf("invalid observer relay session id %q", sessionID)
	}
	sum := sha256.Sum256([]byte(sessionID))
	name := hex.EncodeToString(sum[:16]) + ".sock"
	socketPath := filepath.Join(root, name)
	if len(socketPath) >= len(unix.RawSockaddrUnix{}.Path) {
		return "", errors.New("observer relay Unix socket path exceeds the platform bound")
	}
	return socketPath, nil
}

func observerUnixPeerUID(connection *net.UnixConn) (uint32, error) {
	if connection == nil {
		return 0, errors.New("observer relay connection is nil")
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var (
		credential *unix.Ucred
		controlErr error
	)
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if controlErr != nil {
		return 0, controlErr
	}
	if credential == nil {
		return 0, errors.New("observer relay peer credential is unavailable")
	}
	return credential.Uid, nil
}

func runObserverStreamBridge(
	sessionID, root string,
	reader io.Reader,
	writer io.Writer,
) error {
	if reader == nil || writer == nil {
		return errors.New("observer stream bridge endpoint is nil")
	}
	socketPath, err := observerRelaySocketPath(root, sessionID)
	if err != nil {
		return err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	rootStat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 ||
		rootInfo.Mode().Perm() != 0o700 || rootStat.Uid != 0 {
		return errors.New("observer stream bridge root is not root-owned and private")
	}
	socketInfo, err := os.Lstat(socketPath)
	if err != nil {
		return err
	}
	socketStat, ok := socketInfo.Sys().(*syscall.Stat_t)
	if !ok || socketInfo.Mode()&os.ModeSocket == 0 ||
		socketInfo.Mode().Perm() != 0o600 || socketStat.Uid != 0 {
		return errors.New("observer stream bridge endpoint is not root-owned and private")
	}
	connection, err := net.DialTimeout("unix", socketPath, observerRelayHandshakeWait)
	if err != nil {
		return fmt.Errorf("connect observer stream bridge: %w", err)
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return errors.New("observer stream bridge did not open a Unix connection")
	}
	defer unixConnection.Close()

	inputDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(unixConnection, reader)
		closeErr := unixConnection.CloseWrite()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		inputDone <- errors.Join(copyErr, closeErr)
	}()
	_, outputErr := io.Copy(writer, unixConnection)
	_ = unixConnection.Close()
	inputErr := <-inputDone
	if errors.Is(outputErr, net.ErrClosed) {
		outputErr = nil
	}
	if errors.Is(inputErr, net.ErrClosed) {
		inputErr = nil
	}
	return errors.Join(inputErr, outputErr)
}

func (relay *observerRelay) serve(expectedToken sessionwire.ObserverStreamToken) {
	defer close(relay.done)
	defer expectedToken.Destroy()
	defer func() {
		_ = relay.listener.Close()
		_ = os.Remove(relay.socketPath)
	}()

	var authenticated *net.UnixConn
	for attempt := 0; attempt < maxObserverRelayAuthAttempts; attempt++ {
		connection, err := relay.listener.AcceptUnix()
		if err != nil {
			if !relay.closing.Load() {
				relay.setError(fmt.Errorf("accept observer relay connection: %w", err))
			}
			return
		}
		if err := relay.authenticate(connection, expectedToken); err != nil {
			_ = connection.Close()
			continue
		}
		authenticated = connection
		break
	}
	if authenticated == nil {
		if !relay.closing.Load() {
			relay.setError(errors.New("observer relay authentication attempt bound exceeded"))
		}
		return
	}
	_ = relay.listener.Close()
	relay.connMu.Lock()
	relay.conn = authenticated
	relay.connMu.Unlock()
	relay.authenticatedOnce.Do(func() { close(relay.authenticated) })
	if err := relay.stream(authenticated); err != nil && !relay.closing.Load() {
		relay.setError(err)
	}
	_ = authenticated.Close()
}

func (relay *observerRelay) authenticate(
	connection *net.UnixConn,
	expectedToken sessionwire.ObserverStreamToken,
) error {
	uid, err := relay.options.PeerUID(connection)
	if err != nil {
		return err
	}
	if err := connection.SetDeadline(time.Now().Add(relay.options.HandshakeWait)); err != nil {
		return err
	}
	open, err := sessionwire.ReadObserverStreamOpen(connection)
	if err != nil {
		return err
	}
	defer open.Token.Destroy()
	if err := sessionwire.AuthenticateObserverStreamOpen(
		open,
		relay.binding.SessionID,
		expectedToken,
		sessionwire.ObserverStreamPeer{UID: uid, TargetControlled: uid != 0},
	); err != nil {
		return err
	}
	if err := sessionwire.WriteObserverHello(connection, relay.hello); err != nil {
		return err
	}
	accepted, err := sessionwire.ReadObserverAccepted(connection)
	if err != nil {
		return err
	}
	if err := accepted.ValidateBinding(relay.binding); err != nil {
		return err
	}
	return connection.SetDeadline(time.Time{})
}

func (relay *observerRelay) Enqueue(envelope sessionwire.ObservationEnvelope) error {
	if relay == nil {
		return errors.New("observer relay is nil")
	}
	var encoded bytes.Buffer
	if err := sessionwire.WriteObserverEnvelope(&encoded, envelope); err != nil {
		return err
	}
	frame := encoded.Bytes()
	defer clear(frame)
	return relay.queue.Enqueue(frame)
}

func (relay *observerRelay) stream(connection *net.UnixConn) error {
	for {
		for {
			frame, ok := relay.queue.Dequeue()
			if !ok {
				break
			}
			err := writeObserverRelayFrame(connection, frame)
			clear(frame)
			if err != nil {
				return fmt.Errorf("write observer relay frame: %w", err)
			}
		}
		if loss := relay.queue.LossSummary(); loss.Dropped != 0 {
			if err := relay.writeLoss(connection, loss); err != nil {
				return err
			}
			continue
		}
		select {
		case <-relay.queue.Notify():
		case <-relay.queue.Done():
			return nil
		}
	}
}

func (relay *observerRelay) writeLoss(
	connection *net.UnixConn,
	loss sessionwire.ObserverLossSummary,
) error {
	monotonicNS, err := relay.options.MonotonicNS()
	if err != nil || monotonicNS == 0 {
		return errors.New("observe monotonic observer transport loss time")
	}
	payload, err := json.Marshal(struct {
		Dropped      uint64 `json:"dropped"`
		DroppedBytes uint64 `json:"droppedBytes"`
		Reason       string `json:"reason"`
		Scope        string `json:"scope"`
	}{
		Dropped:      loss.Dropped,
		DroppedBytes: loss.DroppedBytes,
		Reason:       loss.Reason,
		Scope:        "guest-observer-transport",
	})
	if err != nil {
		return err
	}
	defer clear(payload)
	envelope := sessionwire.ObservationEnvelope{
		Schema:             sessionwire.ObservationSchema,
		Owner:              relay.binding.Owner,
		SessionID:          relay.binding.SessionID,
		CgroupID:           relay.binding.CgroupID,
		ObserverGeneration: relay.binding.ObserverGeneration,
		CPU:                sessionwire.ObserverTransportCPU,
		Sequence:           relay.lossSequence.Add(1),
		MonotonicNS:        monotonicNS,
		Kind:               "collector.loss",
		Payload:            payload,
	}
	if err := sessionwire.WriteObserverEnvelope(connection, envelope); err != nil {
		return fmt.Errorf("write observer relay loss: %w", err)
	}
	return nil
}

func writeObserverRelayFrame(writer io.Writer, frame []byte) error {
	for len(frame) != 0 {
		written, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(frame) {
			return io.ErrShortWrite
		}
		frame = frame[written:]
	}
	return nil
}

func (relay *observerRelay) Authenticated() <-chan struct{} {
	if relay == nil {
		return nil
	}
	return relay.authenticated
}

func (relay *observerRelay) Done() <-chan struct{} {
	if relay == nil {
		return nil
	}
	return relay.done
}

func (relay *observerRelay) Err() error {
	if relay == nil {
		return nil
	}
	relay.errMu.Lock()
	defer relay.errMu.Unlock()
	return relay.err
}

func (relay *observerRelay) setError(err error) {
	if relay == nil || err == nil {
		return
	}
	relay.errMu.Lock()
	relay.err = errors.Join(relay.err, err)
	relay.errMu.Unlock()
}

func (relay *observerRelay) Close() error {
	if relay == nil {
		return nil
	}
	var closeErr error
	relay.closeOnce.Do(func() {
		relay.closing.Store(true)
		relay.token.Destroy()
		relay.queue.Close()
		listenerErr := relay.listener.Close()
		if errors.Is(listenerErr, net.ErrClosed) {
			listenerErr = nil
		}
		closeErr = errors.Join(closeErr, listenerErr)
		relay.connMu.Lock()
		if relay.conn != nil {
			connectionErr := relay.conn.Close()
			if errors.Is(connectionErr, net.ErrClosed) {
				connectionErr = nil
			}
			closeErr = errors.Join(closeErr, connectionErr)
		}
		relay.connMu.Unlock()
		select {
		case <-relay.done:
		case <-time.After(observerRelayShutdownWait):
			closeErr = errors.Join(closeErr, errors.New("observer relay did not stop within the bound"))
		}
		if err := os.Remove(relay.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			closeErr = errors.Join(closeErr, err)
		}
	})
	return errors.Join(closeErr, relay.Err())
}
