package workspaceattach

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

type PortalServerOptions struct {
	Root          string
	Authority     *PortalCredentialAuthority
	Limits        PortalLimits
	EnvironmentID string
	ProviderID    string
	Admission     AdmissionController
}

type PortalServer struct {
	rootPath      string
	root          *os.Root
	rootInfo      os.FileInfo
	authority     *PortalCredentialAuthority
	limits        PortalLimits
	environmentID string
	providerID    string
	admission     AdmissionController
	locks         portalLockTable

	mu       sync.Mutex
	listener net.Listener
	address  string
	closed   bool
	conns    map[net.Conn]struct{}
	wg       sync.WaitGroup
	active   atomic.Int64
	watcher  *fsnotify.Watcher
	watchDir map[string]map[string]struct{}
	states   map[*portalConnection]struct{}
}

type portalLockTable struct {
	mu    sync.Mutex
	locks map[string]portalLockOwner
}

type portalLockOwner struct {
	sessionID string
	handleID  uint64
}

func NewPortalServer(options PortalServerOptions) (*PortalServer, error) {
	if options.Authority == nil || options.Admission == nil || options.EnvironmentID == "" || options.ProviderID == "" {
		return nil, errors.New("workspace portal credential authority is required")
	}
	if err := options.Limits.validate(); err != nil {
		return nil, err
	}
	canonical, root, info, err := openPortalRootAuthority(options.Root)
	if err != nil {
		return nil, err
	}
	return &PortalServer{
		rootPath: canonical, root: root, rootInfo: info, authority: options.Authority,
		limits: options.Limits, environmentID: options.EnvironmentID, providerID: options.ProviderID,
		admission: options.Admission, conns: make(map[net.Conn]struct{}),
		states:   make(map[*portalConnection]struct{}),
		locks:    portalLockTable{locks: make(map[string]portalLockOwner)},
		watchDir: make(map[string]map[string]struct{}),
	}, nil
}

func (server *PortalServer) Start(listener net.Listener) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed || server.listener != nil {
		return errors.New("workspace portal server cannot be started")
	}
	server.listener = listener
	server.address = listener.Addr().String()
	if err := server.startWatcherLocked(); err != nil {
		server.listener = nil
		server.address = ""
		return err
	}
	server.wg.Add(1)
	go server.accept()
	return nil
}

func (server *PortalServer) Addr() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.address
}

func (server *PortalServer) accept() {
	defer server.wg.Done()
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		server.mu.Lock()
		if server.closed {
			server.mu.Unlock()
			connection.Close()
			return
		}
		server.conns[connection] = struct{}{}
		server.wg.Add(1)
		server.mu.Unlock()
		go func() {
			defer server.wg.Done()
			server.serveConnection(connection)
			server.mu.Lock()
			delete(server.conns, connection)
			server.mu.Unlock()
		}()
	}
}

func (server *PortalServer) Close() error {
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return nil
	}
	server.closed = true
	listener := server.listener
	connections := make([]net.Conn, 0, len(server.conns))
	for connection := range server.conns {
		connections = append(connections, connection)
	}
	server.mu.Unlock()
	if listener != nil {
		listener.Close()
	}
	if server.watcher != nil {
		server.watcher.Close()
	}
	for _, connection := range connections {
		connection.Close()
	}
	server.wg.Wait()
	return server.root.Close()
}

func (server *PortalServer) WaitForIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if server.active.Load() == 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return server.active.Load() == 0
}

func (server *PortalServer) FlushSession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("workspace Portal flush requires a session id")
	}
	if err := server.checkRootIdentity(); err != nil {
		return err
	}
	server.mu.Lock()
	states := make([]*portalConnection, 0, len(server.states))
	for state := range server.states {
		if state.credential.SessionID == sessionID {
			states = append(states, state)
		}
	}
	server.mu.Unlock()
	for _, state := range states {
		if err := state.flushHandles(ctx); err != nil {
			return err
		}
	}
	return nil
}

type portalConnection struct {
	server           *PortalServer
	connection       net.Conn
	credential       PortalCredential
	writeMu          sync.Mutex
	requestMu        sync.Mutex
	requests         map[uint64]context.CancelFunc
	handleMu         sync.Mutex
	handles          map[uint64]*portalHandle
	nextHandle       uint64
	closed           chan struct{}
	events           chan queuedPortalEvent
	queuedEventBytes atomic.Int64
	closeOnce        sync.Once
	wg               sync.WaitGroup
}

type portalHandle struct {
	file      *os.File
	path      string
	lock      bool
	append    bool
	admission AdmissionLease
}

type queuedPortalEvent struct {
	event     PortalEvent
	admission AdmissionLease
}

func (state *portalConnection) flushHandles(ctx context.Context) error {
	state.handleMu.Lock()
	handles := make([]*os.File, 0, len(state.handles))
	for _, handle := range state.handles {
		handles = append(handles, handle.file)
	}
	state.handleMu.Unlock()
	for _, file := range handles {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := file.Sync(); err != nil {
			return unwrapPortalPathError(err)
		}
	}
	return nil
}

func (server *PortalServer) serveConnection(connection net.Conn) {
	state := &portalConnection{
		server: server, connection: connection, requests: make(map[uint64]context.CancelFunc),
		handles: make(map[uint64]*portalHandle),
		closed:  make(chan struct{}),
		events:  make(chan queuedPortalEvent, 256),
	}
	defer state.close()
	frame, err := readPortalFrame(connection, server.limits.FrameBytes)
	if err != nil || frame.Kind != portalKindRequest || frame.Opcode != portalOpHello || frame.RequestID != 0 {
		_ = state.write(portalFrame{Kind: portalKindResponse, Opcode: portalOpHello, Status: portalStatusProtocol})
		return
	}
	candidate, err := decodePortalCredential(frame.Payload)
	if err != nil {
		_ = state.write(portalFrame{Kind: portalKindResponse, Opcode: portalOpHello, Status: portalStatusProtocol})
		return
	}
	lease, err := server.authority.authenticate(candidate)
	if err != nil {
		_ = state.write(portalFrame{Kind: portalKindResponse, Opcode: portalOpHello, Status: portalStatusForError(err)})
		return
	}
	state.credential = lease.credential
	server.mu.Lock()
	server.states[state] = struct{}{}
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.states, state)
		server.mu.Unlock()
	}()
	if err := state.write(portalFrame{Kind: portalKindResponse, Opcode: portalOpHello, Status: 0}); err != nil {
		return
	}
	state.wg.Add(2)
	go func() { defer state.wg.Done(); state.watchCredential(lease) }()
	go func() { defer state.wg.Done(); state.writeEvents() }()

	for {
		frame, err := readPortalFrame(connection, server.limits.FrameBytes)
		if err != nil {
			return
		}
		if frame.Kind != portalKindRequest || frame.RequestID == 0 {
			state.terminal(ErrPortalProtocol)
			return
		}
		if frame.Opcode == portalOpCancel {
			state.cancel(frame.RequestID)
			continue
		}
		usesReservedCapacity := frame.Opcode == portalOpClose || frame.Opcode == portalOpUnlock
		class := AdmissionOrdinary
		if usesReservedCapacity {
			class = AdmissionTeardown
		}
		admission, admissionErr := server.admission.Acquire(context.Background(), AdmissionRequest{
			EnvironmentID: server.environmentID, ProviderID: server.providerID,
			SessionID: state.credential.SessionID, Class: class, InFlight: 1, FrameBytes: len(frame.Payload),
		})
		if admissionErr != nil {
			if err := state.write(portalFrame{Kind: portalKindResponse, Opcode: frame.Opcode, RequestID: frame.RequestID, Status: portalStatusOverloaded}); err != nil {
				return
			}
			continue
		}
		requestContext, cancel := context.WithCancel(context.Background())
		state.requestMu.Lock()
		if _, exists := state.requests[frame.RequestID]; exists {
			state.requestMu.Unlock()
			cancel()
			admission.Release()
			state.terminal(ErrPortalProtocol)
			return
		}
		state.requests[frame.RequestID] = cancel
		state.requestMu.Unlock()
		state.wg.Add(1)
		server.active.Add(1)
		go func(frame portalFrame, admission AdmissionLease) {
			defer state.wg.Done()
			defer server.active.Add(-1)
			defer admission.Release()
			payload, operationErr := state.dispatch(requestContext, frame)
			state.requestMu.Lock()
			delete(state.requests, frame.RequestID)
			state.requestMu.Unlock()
			if errors.Is(requestContext.Err(), context.Canceled) {
				operationErr = contextCanceledError
			}
			if err := state.write(portalFrame{
				Kind: portalKindResponse, Opcode: frame.Opcode, RequestID: frame.RequestID,
				Status: portalStatusForError(operationErr), Payload: payload,
			}); err != nil {
				_ = state.connection.Close()
			}
		}(frame, admission)
	}
}

func (state *portalConnection) watchCredential(lease portalCredentialLease) {
	delay := time.Until(lease.credential.ExpiresAt)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-lease.changed:
		state.terminal(ErrPortalCredentialRevoked)
	case <-timer.C:
		state.terminal(ErrPortalCredentialExpired)
	case <-state.closed:
	}
}

func (state *portalConnection) terminal(err error) {
	_ = state.write(portalFrame{Kind: portalKindTerminal, Status: portalStatusForError(err)})
	_ = state.connection.Close()
}

func (state *portalConnection) write(frame portalFrame) error {
	state.writeMu.Lock()
	defer state.writeMu.Unlock()
	return writePortalFrame(state.connection, frame, state.server.limits.FrameBytes)
}

func (state *portalConnection) cancel(requestID uint64) {
	state.requestMu.Lock()
	cancel := state.requests[requestID]
	state.requestMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (state *portalConnection) close() {
	state.closeOnce.Do(func() {
		close(state.closed)
		state.connection.Close()
		state.requestMu.Lock()
		for _, cancel := range state.requests {
			cancel()
		}
		state.requestMu.Unlock()
		state.wg.Wait()
		state.handleMu.Lock()
		for id, handle := range state.handles {
			if handle.lock {
				_ = state.server.locks.unlock(
					state.credential.SessionID,
					id,
					handle.path,
				)
			}
			_ = handle.file.Close()
			handle.admission.Release()
		}
		state.handles = nil
		state.handleMu.Unlock()
		for {
			select {
			case queued := <-state.events:
				state.queuedEventBytes.Add(-portalEventBytes(queued.event))
				queued.admission.Release()
			default:
				return
			}
		}
	})
}

func (state *portalConnection) dispatch(ctx context.Context, frame portalFrame) ([]byte, error) {
	if err := state.server.checkRootIdentity(); err != nil {
		return nil, err
	}
	switch frame.Opcode {
	case portalOpEcho:
		if len(frame.Payload) < 4 {
			return nil, ErrPortalProtocol
		}
		delay := time.Duration(binary.LittleEndian.Uint32(frame.Payload[:4])) * time.Millisecond
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, contextCanceledError
		case <-timer.C:
			return append([]byte(nil), frame.Payload[4:]...), nil
		}
	case portalOpOpen:
		return state.open(frame.Payload)
	case portalOpClose:
		return nil, state.closeHandle(frame.Payload)
	case portalOpLock:
		return nil, state.lock(frame.Payload)
	case portalOpUnlock:
		return nil, state.unlock(frame.Payload)
	case portalOpStat:
		return state.stat(frame.Payload)
	case portalOpReadDir:
		return state.readDir(frame.Payload)
	case portalOpRead:
		return state.read(frame.Payload)
	case portalOpWrite:
		return state.writeFile(frame.Payload)
	case portalOpFsync:
		return nil, state.fsync(frame.Payload)
	case portalOpFsyncPath:
		return nil, state.fsyncPath(frame.Payload)
	case portalOpMkdir:
		return nil, state.mkdir(frame.Payload)
	case portalOpRemove:
		return nil, state.remove(frame.Payload)
	case portalOpRename:
		return nil, state.rename(frame.Payload)
	case portalOpSymlink:
		return nil, state.symlink(frame.Payload)
	case portalOpReadlink:
		return state.readlink(frame.Payload)
	case portalOpChmod:
		return nil, state.chmod(frame.Payload)
	case portalOpChtimes:
		return nil, state.chtimes(frame.Payload)
	case portalOpTruncate:
		return nil, state.truncate(frame.Payload)
	case portalOpCheckLock:
		return state.checkLock(frame.Payload)
	default:
		return nil, ErrPortalProtocol
	}
}

func (server *PortalServer) checkRootIdentity() error {
	return provePortalRootIdentity(server.rootPath, server.rootInfo)
}

func (state *portalConnection) open(payload []byte) ([]byte, error) {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil {
		return nil, ErrPortalProtocol
	}
	flags, err := decoder.uint32()
	if err != nil {
		return nil, ErrPortalProtocol
	}
	mode, err := decoder.uint32()
	if err != nil || decoder.done() != nil {
		return nil, ErrPortalProtocol
	}
	path, err = portalRelativePath(path)
	if err != nil {
		return nil, err
	}
	state.handleMu.Lock()
	defer state.handleMu.Unlock()
	if len(state.handles) >= state.server.limits.HandlesPerSession {
		return nil, ErrPortalOverloaded
	}
	admission, err := state.server.admission.Acquire(context.Background(), AdmissionRequest{
		EnvironmentID: state.server.environmentID, ProviderID: state.server.providerID,
		SessionID: state.credential.SessionID, Class: AdmissionOrdinary, Handles: 1,
	})
	if err != nil {
		return nil, err
	}
	hostFlags, err := decodePortalOpenFlags(flags)
	if err != nil {
		admission.Release()
		return nil, err
	}
	if hostFlags&syscall.O_NOFOLLOW != 0 {
		info, statErr := state.server.root.Lstat(path)
		switch {
		case statErr == nil && info.Mode()&os.ModeSymlink != 0:
			admission.Release()
			return nil, syscall.ELOOP
		case statErr != nil && !(errors.Is(statErr, os.ErrNotExist) && hostFlags&syscall.O_CREAT != 0):
			admission.Release()
			return nil, unwrapPortalPathError(statErr)
		}
		// os.Root already confines traversal but does not accept O_NOFOLLOW.
		hostFlags &^= syscall.O_NOFOLLOW
	}
	appendMode := hostFlags&syscall.O_APPEND != 0
	file, err := state.server.root.OpenFile(path, hostFlags, os.FileMode(mode)&0o777)
	if err != nil {
		admission.Release()
		return nil, unwrapPortalPathError(err)
	}
	state.nextHandle++
	if state.nextHandle == 0 {
		state.nextHandle++
	}
	state.handles[state.nextHandle] = &portalHandle{
		file: file, path: path, append: appendMode, admission: admission,
	}
	var encoder portalEncoder
	encoder.uint64(state.nextHandle)
	return encoder.Bytes(), nil
}

func (state *portalConnection) closeHandle(payload []byte) error {
	handleID, err := portalHandlePayload(payload)
	if err != nil {
		return err
	}
	state.handleMu.Lock()
	handle := state.handles[handleID]
	if handle != nil {
		delete(state.handles, handleID)
	}
	state.handleMu.Unlock()
	if handle == nil {
		return ErrPortalHandleNotFound
	}
	var unlockErr error
	if handle.lock {
		unlockErr = state.server.locks.unlock(
			state.credential.SessionID,
			handleID,
			handle.path,
		)
	}
	closeErr := handle.file.Close()
	handle.admission.Release()
	return errors.Join(unlockErr, closeErr)
}

func (state *portalConnection) lock(payload []byte) error {
	decoder := newPortalDecoder(payload)
	handleID, err := decoder.uint64()
	if err != nil {
		return ErrPortalProtocol
	}
	exclusive, err := decoder.boolean()
	if err != nil || decoder.done() != nil || !exclusive {
		return ErrPortalProtocol
	}
	state.handleMu.Lock()
	handle := state.handles[handleID]
	state.handleMu.Unlock()
	if handle == nil {
		return ErrPortalHandleNotFound
	}
	if err := state.server.locks.lock(state.credential.SessionID, handleID, handle.path); err != nil {
		return err
	}
	state.handleMu.Lock()
	handle.lock = true
	state.handleMu.Unlock()
	return nil
}

func (state *portalConnection) unlock(payload []byte) error {
	handleID, err := portalHandlePayload(payload)
	if err != nil {
		return err
	}
	state.handleMu.Lock()
	handle := state.handles[handleID]
	if handle == nil {
		state.handleMu.Unlock()
		return ErrPortalHandleNotFound
	}
	handle.lock = false
	state.handleMu.Unlock()
	return state.server.locks.unlock(state.credential.SessionID, handleID, handle.path)
}

func (state *portalConnection) checkLock(payload []byte) ([]byte, error) {
	handleID, err := portalHandlePayload(payload)
	if err != nil {
		return nil, err
	}
	state.handleMu.Lock()
	handle := state.handles[handleID]
	state.handleMu.Unlock()
	if handle == nil {
		return nil, ErrPortalHandleNotFound
	}
	available := state.server.locks.available(state.credential.SessionID, handleID, handle.path)
	var encoder portalEncoder
	encoder.boolean(available)
	return encoder.Bytes(), nil
}

func portalHandlePayload(payload []byte) (uint64, error) {
	decoder := newPortalDecoder(payload)
	handle, err := decoder.uint64()
	if err != nil || decoder.done() != nil {
		return 0, ErrPortalProtocol
	}
	return handle, nil
}

func (table *portalLockTable) lock(sessionID string, handleID uint64, path string) error {
	table.mu.Lock()
	defer table.mu.Unlock()
	if owner, exists := table.locks[path]; exists && (owner.sessionID != sessionID || owner.handleID != handleID) {
		return syscall.EWOULDBLOCK
	}
	table.locks[path] = portalLockOwner{sessionID: sessionID, handleID: handleID}
	return nil
}

func (table *portalLockTable) unlock(sessionID string, handleID uint64, path string) error {
	table.mu.Lock()
	defer table.mu.Unlock()
	owner, exists := table.locks[path]
	if !exists {
		return nil
	}
	if owner.sessionID != sessionID || owner.handleID != handleID {
		return syscall.EPERM
	}
	delete(table.locks, path)
	return nil
}

func (table *portalLockTable) available(sessionID string, handleID uint64, path string) bool {
	table.mu.Lock()
	defer table.mu.Unlock()
	owner, exists := table.locks[path]
	return !exists || (owner.sessionID == sessionID && owner.handleID == handleID)
}

func portalRelativePath(value string) (string, error) {
	if value == "" || value == "." {
		return ".", nil
	}
	if strings.ContainsRune(value, 0) || filepath.IsAbs(value) || filepath.Clean(value) != value || value == ".." || len(value) > 4096 {
		return "", syscall.EACCES
	}
	if len(value) >= 3 && value[:3] == "../" {
		return "", syscall.EACCES
	}
	return value, nil
}

func unwrapPortalPathError(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}

func (state *portalConnection) stat(payload []byte) ([]byte, error) {
	path, err := portalPathPayload(payload)
	if err != nil {
		return nil, err
	}
	info, err := state.server.root.Lstat(path)
	if err != nil {
		return nil, unwrapPortalPathError(err)
	}
	return encodePortalFileInfo(info), nil
}

func (state *portalConnection) readDir(payload []byte) ([]byte, error) {
	path, err := portalPathPayload(payload)
	if err != nil {
		return nil, err
	}
	directory, err := state.server.root.Open(path)
	if err != nil {
		return nil, unwrapPortalPathError(err)
	}
	defer directory.Close()
	entries, err := directory.ReadDir(state.server.limits.DirectoryEntries + 1)
	if err != nil {
		return nil, unwrapPortalPathError(err)
	}
	if len(entries) > state.server.limits.DirectoryEntries {
		return nil, syscall.EOVERFLOW
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var encoder portalEncoder
	encoder.uint32(uint32(len(entries)))
	for _, entry := range entries {
		info, err := state.server.root.Lstat(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, unwrapPortalPathError(err)
		}
		encoder.string(entry.Name())
		encoder.bytes(encodePortalFileInfo(info))
	}
	return encoder.Bytes(), nil
}

func (state *portalConnection) read(payload []byte) ([]byte, error) {
	decoder := newPortalDecoder(payload)
	handleID, err := decoder.uint64()
	if err != nil {
		return nil, ErrPortalProtocol
	}
	offset, err := decoder.int64()
	if err != nil || offset < 0 {
		return nil, ErrPortalProtocol
	}
	size, err := decoder.uint32()
	if err != nil || decoder.done() != nil || size > uint32(state.server.limits.FrameBytes-64) {
		return nil, ErrPortalProtocol
	}
	handle := state.handle(handleID)
	if handle == nil {
		return nil, ErrPortalHandleNotFound
	}
	buffer := make([]byte, int(size))
	n, readErr := handle.file.ReadAt(buffer, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, unwrapPortalPathError(readErr)
	}
	return buffer[:n], nil
}

func (state *portalConnection) writeFile(payload []byte) ([]byte, error) {
	decoder := newPortalDecoder(payload)
	handleID, err := decoder.uint64()
	if err != nil {
		return nil, ErrPortalProtocol
	}
	offset, err := decoder.int64()
	if err != nil || offset < 0 {
		return nil, ErrPortalProtocol
	}
	data, err := decoder.bytes(state.server.limits.FrameBytes - 64)
	if err != nil || decoder.done() != nil {
		return nil, ErrPortalProtocol
	}
	handle := state.handle(handleID)
	if handle == nil {
		return nil, ErrPortalHandleNotFound
	}
	var (
		n        int
		writeErr error
	)
	if handle.append {
		// os.File.WriteAt intentionally rejects O_APPEND descriptors. Preserve
		// POSIX append semantics on the authority-holding host instead of
		// trusting a potentially stale guest-provided offset.
		n, writeErr = handle.file.Write(data)
	} else {
		n, writeErr = handle.file.WriteAt(data, offset)
	}
	var encoder portalEncoder
	encoder.uint32(uint32(n))
	if writeErr != nil {
		return encoder.Bytes(), unwrapPortalPathError(writeErr)
	}
	return encoder.Bytes(), nil
}

func (state *portalConnection) fsync(payload []byte) error {
	handleID, err := portalHandlePayload(payload)
	if err != nil {
		return err
	}
	handle := state.handle(handleID)
	if handle == nil {
		return ErrPortalHandleNotFound
	}
	return unwrapPortalPathError(handle.file.Sync())
}

func (state *portalConnection) fsyncPath(payload []byte) error {
	path, err := portalPathPayload(payload)
	if err != nil {
		return err
	}
	file, err := state.server.root.Open(path)
	if err != nil {
		return unwrapPortalPathError(err)
	}
	defer file.Close()
	return unwrapPortalPathError(file.Sync())
}

func (state *portalConnection) mkdir(payload []byte) error {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	mode, err := decoder.uint32()
	if err != nil || decoder.done() != nil {
		return ErrPortalProtocol
	}
	path, err = portalRelativePath(path)
	if err != nil {
		return err
	}
	return unwrapPortalPathError(state.server.root.Mkdir(path, os.FileMode(mode)&0o777))
}

func (state *portalConnection) remove(payload []byte) error {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	directory, err := decoder.boolean()
	if err != nil || decoder.done() != nil {
		return ErrPortalProtocol
	}
	path, err = portalRelativePath(path)
	if err != nil {
		return err
	}
	info, err := state.server.root.Lstat(path)
	if err != nil {
		return unwrapPortalPathError(err)
	}
	if directory && !info.IsDir() {
		return syscall.ENOTDIR
	}
	if !directory && info.IsDir() {
		return syscall.EISDIR
	}
	return unwrapPortalPathError(state.server.root.Remove(path))
}

func (state *portalConnection) rename(payload []byte) error {
	decoder := newPortalDecoder(payload)
	oldPath, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	newPath, err := decoder.string(4096)
	if err != nil || decoder.done() != nil {
		return ErrPortalProtocol
	}
	oldPath, err = portalRelativePath(oldPath)
	if err != nil {
		return err
	}
	newPath, err = portalRelativePath(newPath)
	if err != nil {
		return err
	}
	return unwrapPortalPathError(state.server.root.Rename(oldPath, newPath))
}

func (state *portalConnection) symlink(payload []byte) error {
	decoder := newPortalDecoder(payload)
	target, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	linkPath, err := decoder.string(4096)
	if err != nil || decoder.done() != nil {
		return ErrPortalProtocol
	}
	linkPath, err = portalRelativePath(linkPath)
	if err != nil {
		return err
	}
	if filepath.IsAbs(target) || target == "" {
		return syscall.EACCES
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
	if _, err := portalRelativePath(resolved); err != nil {
		return syscall.EACCES
	}
	return unwrapPortalPathError(state.server.root.Symlink(target, linkPath))
}

func (state *portalConnection) readlink(payload []byte) ([]byte, error) {
	path, err := portalPathPayload(payload)
	if err != nil {
		return nil, err
	}
	target, err := state.server.root.Readlink(path)
	if err != nil {
		return nil, unwrapPortalPathError(err)
	}
	var encoder portalEncoder
	encoder.string(target)
	return encoder.Bytes(), nil
}

func (state *portalConnection) chmod(payload []byte) error {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	mode, err := decoder.uint32()
	if err != nil || decoder.done() != nil {
		return ErrPortalProtocol
	}
	path, err = portalRelativePath(path)
	if err != nil {
		return err
	}
	file, err := state.server.root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return unwrapPortalPathError(err)
	}
	defer file.Close()
	return unwrapPortalPathError(file.Chmod(os.FileMode(mode) & 0o777))
}

func (state *portalConnection) chtimes(payload []byte) error {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	atimeSec, err := decoder.int64()
	if err != nil {
		return ErrPortalProtocol
	}
	atimeNS, err := decoder.int64()
	if err != nil {
		return ErrPortalProtocol
	}
	mtimeSec, err := decoder.int64()
	if err != nil {
		return ErrPortalProtocol
	}
	mtimeNS, err := decoder.int64()
	if err != nil || decoder.done() != nil {
		return ErrPortalProtocol
	}
	path, err = portalRelativePath(path)
	if err != nil {
		return err
	}
	file, err := state.server.root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return unwrapPortalPathError(err)
	}
	defer file.Close()
	times := []unix.Timeval{
		unix.NsecToTimeval(time.Unix(atimeSec, atimeNS).UnixNano()),
		unix.NsecToTimeval(time.Unix(mtimeSec, mtimeNS).UnixNano()),
	}
	return unwrapPortalPathError(unix.Futimes(int(file.Fd()), times))
}

func (state *portalConnection) truncate(payload []byte) error {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil {
		return ErrPortalProtocol
	}
	size, err := decoder.int64()
	if err != nil || size < 0 || decoder.done() != nil {
		return ErrPortalProtocol
	}
	path, err = portalRelativePath(path)
	if err != nil {
		return err
	}
	file, err := state.server.root.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return unwrapPortalPathError(err)
	}
	defer file.Close()
	return unwrapPortalPathError(file.Truncate(size))
}

func (state *portalConnection) handle(handleID uint64) *portalHandle {
	state.handleMu.Lock()
	defer state.handleMu.Unlock()
	return state.handles[handleID]
}

func portalPathPayload(payload []byte) (string, error) {
	decoder := newPortalDecoder(payload)
	path, err := decoder.string(4096)
	if err != nil || decoder.done() != nil {
		return "", ErrPortalProtocol
	}
	return portalRelativePath(path)
}

func encodePortalFileInfo(info os.FileInfo) []byte {
	var encoder portalEncoder
	encoder.uint32(uint32(info.Mode()))
	encoder.int64(info.Size())
	encoder.int64(info.ModTime().Unix())
	encoder.int64(int64(info.ModTime().Nanosecond()))
	encoder.uint64(portalInode(info))
	uid, gid, nlink := portalOwnership(info)
	encoder.uint32(uid)
	encoder.uint32(gid)
	encoder.uint64(nlink)
	return encoder.Bytes()
}

func portalInode(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}

func portalOwnership(info os.FileInfo) (uint32, uint32, uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid, stat.Gid, uint64(stat.Nlink)
	}
	return 0, 0, 1
}

func (server *PortalServer) startWatcherLocked() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := filepath.WalkDir(server.rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if err := watcher.Add(path); err != nil {
				return err
			}
			names, err := snapshotPortalDirectory(path)
			if err != nil {
				return err
			}
			server.watchDir[path] = names
		}
		return nil
	}); err != nil {
		watcher.Close()
		return err
	}
	server.watcher = watcher
	server.wg.Add(1)
	go server.watch()
	return nil
}

func (server *PortalServer) watch() {
	defer server.wg.Done()
	for {
		select {
		case event, ok := <-server.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if server.watcher.Add(event.Name) == nil {
						if names, snapshotErr := snapshotPortalDirectory(event.Name); snapshotErr == nil {
							server.watchDir[event.Name] = names
						}
					}
				}
			}
			events := map[string]PortalEvent{}
			if candidate, ok := server.portalEvent(event.Name, event.Op); ok {
				events[candidate.Path] = candidate
			}
			if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				for _, candidate := range server.refreshWatchDirectory(filepath.Dir(event.Name)) {
					events[candidate.Path] = candidate
				}
			}
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				if _, err := os.Stat(event.Name); errors.Is(err, os.ErrNotExist) {
					delete(server.watchDir, event.Name)
				}
			}
			paths := make([]string, 0, len(events))
			for path := range events {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			for _, path := range paths {
				server.broadcast(events[path])
			}
		case _, ok := <-server.watcher.Errors:
			if !ok {
				return
			}
			server.terminateConnections(ErrPortalNotificationLost)
			return
		}
	}
}

func (server *PortalServer) terminateConnections(err error) {
	server.mu.Lock()
	states := make([]*portalConnection, 0, len(server.states))
	for state := range server.states {
		states = append(states, state)
	}
	server.mu.Unlock()
	for _, state := range states {
		state.terminal(err)
	}
}

func snapshotPortalDirectory(path string) (map[string]struct{}, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = struct{}{}
	}
	return names, nil
}

func (server *PortalServer) portalEvent(path string, op fsnotify.Op) (PortalEvent, bool) {
	relative, err := filepath.Rel(server.rootPath, path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		return PortalEvent{}, false
	}
	return PortalEvent{Path: filepath.ToSlash(relative), Op: uint32(op)}, true
}

func (server *PortalServer) refreshWatchDirectory(path string) []PortalEvent {
	previous, tracked := server.watchDir[path]
	if !tracked {
		return nil
	}
	current, err := snapshotPortalDirectory(path)
	if err != nil {
		return nil
	}
	server.watchDir[path] = current
	events := make([]PortalEvent, 0)
	for name := range previous {
		if _, exists := current[name]; exists {
			continue
		}
		if event, ok := server.portalEvent(filepath.Join(path, name), fsnotify.Remove); ok {
			events = append(events, event)
		}
	}
	for name := range current {
		if _, existed := previous[name]; existed {
			continue
		}
		if event, ok := server.portalEvent(filepath.Join(path, name), fsnotify.Create); ok {
			events = append(events, event)
		}
	}
	return events
}

func (server *PortalServer) broadcast(event PortalEvent) {
	server.mu.Lock()
	states := make([]*portalConnection, 0, len(server.states))
	for state := range server.states {
		states = append(states, state)
	}
	server.mu.Unlock()
	for _, state := range states {
		if err := state.enqueueEvent(event, server.limits.QueuedBytesPerSession); err != nil {
			state.terminal(err)
		}
	}
}

func (state *portalConnection) writeEvents() {
	for {
		select {
		case queued := <-state.events:
			event := queued.event
			state.queuedEventBytes.Add(-portalEventBytes(event))
			queued.admission.Release()
			var encoder portalEncoder
			encoder.string(event.Path)
			encoder.uint32(event.Op)
			if err := state.write(portalFrame{Kind: portalKindEvent, Opcode: portalOpNotify, Payload: encoder.Bytes()}); err != nil {
				return
			}
		case <-state.closed:
			return
		}
	}
}

func (state *portalConnection) enqueueEvent(event PortalEvent, limit int64) error {
	size := portalEventBytes(event)
	admission, err := state.server.admission.Acquire(context.Background(), AdmissionRequest{
		EnvironmentID: state.server.environmentID, ProviderID: state.server.providerID,
		SessionID: state.credential.SessionID, Class: AdmissionOrdinary, QueuedBytes: size,
	})
	if err != nil {
		return err
	}
	for {
		current := state.queuedEventBytes.Load()
		if size > limit || current > limit-size {
			admission.Release()
			return ErrPortalOverloaded
		}
		if state.queuedEventBytes.CompareAndSwap(current, current+size) {
			break
		}
	}
	select {
	case state.events <- queuedPortalEvent{event: event, admission: admission}:
		return nil
	default:
		state.queuedEventBytes.Add(-size)
		admission.Release()
		return ErrPortalOverloaded
	}
}

func portalEventBytes(event PortalEvent) int64 {
	return int64(len(event.Path) + 8)
}

var _ = io.EOF
var _ = fmt.Sprintf
