package workspaceattach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type portalResponse struct {
	payload []byte
	err     error
}

type PortalClient struct {
	connection   net.Conn
	limits       PortalLimits
	writeMu      sync.Mutex
	pendingMu    sync.Mutex
	pending      map[uint64]chan portalResponse
	nextID       atomic.Uint64
	terminal     chan error
	events       chan PortalEvent
	done         chan struct{}
	closeOnce    sync.Once
	terminalOnce sync.Once
}

func DialPortal(ctx context.Context, address string, credential PortalCredential, limits PortalLimits) (*PortalClient, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	payload, err := encodePortalCredential(credential)
	if err != nil {
		connection.Close()
		return nil, err
	}
	if err := writePortalFrame(connection, portalFrame{Kind: portalKindRequest, Opcode: portalOpHello, Payload: payload}, limits.FrameBytes); err != nil {
		connection.Close()
		return nil, err
	}
	response, err := readPortalFrame(connection, limits.FrameBytes)
	if err != nil {
		connection.Close()
		return nil, err
	}
	if response.Kind != portalKindResponse || response.Opcode != portalOpHello || response.RequestID != 0 {
		connection.Close()
		return nil, ErrPortalProtocol
	}
	if err := portalErrorForStatus(response.Status); err != nil {
		connection.Close()
		return nil, err
	}
	client := &PortalClient{
		connection: connection, limits: limits, pending: make(map[uint64]chan portalResponse),
		terminal: make(chan error, 1), done: make(chan struct{}),
		events: make(chan PortalEvent, 256),
	}
	go client.readResponses()
	return client, nil
}

func (client *PortalClient) Terminal() <-chan error     { return client.terminal }
func (client *PortalClient) Events() <-chan PortalEvent { return client.events }

func (client *PortalClient) Close() error {
	var result error
	client.closeOnce.Do(func() {
		result = client.connection.Close()
		<-client.done
	})
	return result
}

func (client *PortalClient) ProbeEcho(ctx context.Context, payload []byte) ([]byte, error) {
	return client.call(ctx, portalOpEcho, payload)
}

func (client *PortalClient) Open(ctx context.Context, path string, flags int, mode os.FileMode) (uint64, error) {
	encodedFlags, err := encodePortalOpenFlags(flags)
	if err != nil {
		return 0, fmt.Errorf("encode portal open flags %#x: %w", flags, err)
	}
	var encoder portalEncoder
	encoder.string(path)
	encoder.uint32(encodedFlags)
	encoder.uint32(uint32(mode.Perm()))
	payload, err := client.call(ctx, portalOpOpen, encoder.Bytes())
	if err != nil {
		return 0, fmt.Errorf("portal open request: %w", err)
	}
	decoder := newPortalDecoder(payload)
	handle, err := decoder.uint64()
	if err != nil || decoder.done() != nil {
		return 0, ErrPortalProtocol
	}
	return handle, nil
}

func (client *PortalClient) CloseHandle(ctx context.Context, handle uint64) error {
	var encoder portalEncoder
	encoder.uint64(handle)
	_, err := client.call(ctx, portalOpClose, encoder.Bytes())
	return err
}

func (client *PortalClient) Lock(ctx context.Context, handle uint64, exclusive bool) error {
	var encoder portalEncoder
	encoder.uint64(handle)
	encoder.boolean(exclusive)
	_, err := client.call(ctx, portalOpLock, encoder.Bytes())
	return err
}

func (client *PortalClient) Unlock(ctx context.Context, handle uint64) error {
	var encoder portalEncoder
	encoder.uint64(handle)
	_, err := client.call(ctx, portalOpUnlock, encoder.Bytes())
	return err
}

func (client *PortalClient) LockAvailable(ctx context.Context, handle uint64) (bool, error) {
	var encoder portalEncoder
	encoder.uint64(handle)
	payload, err := client.call(ctx, portalOpCheckLock, encoder.Bytes())
	if err != nil {
		return false, err
	}
	decoder := newPortalDecoder(payload)
	available, err := decoder.boolean()
	if err != nil || decoder.done() != nil {
		return false, ErrPortalProtocol
	}
	return available, nil
}

func (client *PortalClient) Stat(ctx context.Context, path string) (PortalFileInfo, error) {
	var encoder portalEncoder
	encoder.string(path)
	payload, err := client.call(ctx, portalOpStat, encoder.Bytes())
	if err != nil {
		return PortalFileInfo{}, err
	}
	return decodePortalFileInfo(payload)
}

func (client *PortalClient) ReadDir(ctx context.Context, path string) ([]PortalDirEntry, error) {
	var encoder portalEncoder
	encoder.string(path)
	payload, err := client.call(ctx, portalOpReadDir, encoder.Bytes())
	if err != nil {
		return nil, err
	}
	decoder := newPortalDecoder(payload)
	count, err := decoder.uint32()
	if err != nil || count > uint32(client.limits.DirectoryEntries) {
		return nil, ErrPortalProtocol
	}
	entries := make([]PortalDirEntry, 0, count)
	for range count {
		name, err := decoder.string(4096)
		if err != nil || name == "" || name == "." || name == ".." {
			return nil, ErrPortalProtocol
		}
		encodedInfo, err := decoder.bytes(128)
		if err != nil {
			return nil, ErrPortalProtocol
		}
		info, err := decodePortalFileInfo(encodedInfo)
		if err != nil {
			return nil, ErrPortalProtocol
		}
		entries = append(entries, PortalDirEntry{Name: name, Info: info})
	}
	if decoder.done() != nil {
		return nil, ErrPortalProtocol
	}
	return entries, nil
}

func (client *PortalClient) Read(ctx context.Context, handle uint64, offset int64, size int) ([]byte, error) {
	if offset < 0 || size < 0 || size > client.limits.FrameBytes-64 {
		return nil, ErrPortalProtocol
	}
	var encoder portalEncoder
	encoder.uint64(handle)
	encoder.int64(offset)
	encoder.uint32(uint32(size))
	return client.call(ctx, portalOpRead, encoder.Bytes())
}

func (client *PortalClient) Write(ctx context.Context, handle uint64, offset int64, data []byte) (int, error) {
	if offset < 0 || len(data) > client.limits.FrameBytes-64 {
		return 0, ErrPortalFrameTooLarge
	}
	var encoder portalEncoder
	encoder.uint64(handle)
	encoder.int64(offset)
	encoder.bytes(data)
	payload, err := client.call(ctx, portalOpWrite, encoder.Bytes())
	if err != nil {
		return 0, err
	}
	decoder := newPortalDecoder(payload)
	written, err := decoder.uint32()
	if err != nil || decoder.done() != nil {
		return 0, ErrPortalProtocol
	}
	return int(written), nil
}

func (client *PortalClient) Fsync(ctx context.Context, handle uint64) error {
	var encoder portalEncoder
	encoder.uint64(handle)
	_, err := client.call(ctx, portalOpFsync, encoder.Bytes())
	return err
}

func (client *PortalClient) FsyncPath(ctx context.Context, path string) error {
	var encoder portalEncoder
	encoder.string(path)
	_, err := client.call(ctx, portalOpFsyncPath, encoder.Bytes())
	return err
}

func (client *PortalClient) Mkdir(ctx context.Context, path string, mode os.FileMode) error {
	var encoder portalEncoder
	encoder.string(path)
	encoder.uint32(uint32(mode.Perm()))
	_, err := client.call(ctx, portalOpMkdir, encoder.Bytes())
	return err
}

func (client *PortalClient) Remove(ctx context.Context, path string, directory bool) error {
	var encoder portalEncoder
	encoder.string(path)
	encoder.boolean(directory)
	_, err := client.call(ctx, portalOpRemove, encoder.Bytes())
	return err
}

func (client *PortalClient) Rename(ctx context.Context, oldPath, newPath string) error {
	var encoder portalEncoder
	encoder.string(oldPath)
	encoder.string(newPath)
	_, err := client.call(ctx, portalOpRename, encoder.Bytes())
	return err
}

func (client *PortalClient) Symlink(ctx context.Context, target, linkPath string) error {
	var encoder portalEncoder
	encoder.string(target)
	encoder.string(linkPath)
	_, err := client.call(ctx, portalOpSymlink, encoder.Bytes())
	return err
}

func (client *PortalClient) Readlink(ctx context.Context, path string) (string, error) {
	var encoder portalEncoder
	encoder.string(path)
	payload, err := client.call(ctx, portalOpReadlink, encoder.Bytes())
	if err != nil {
		return "", err
	}
	decoder := newPortalDecoder(payload)
	target, err := decoder.string(4096)
	if err != nil || decoder.done() != nil {
		return "", ErrPortalProtocol
	}
	return target, nil
}

func (client *PortalClient) Chmod(ctx context.Context, path string, mode os.FileMode) error {
	var encoder portalEncoder
	encoder.string(path)
	encoder.uint32(uint32(mode.Perm()))
	_, err := client.call(ctx, portalOpChmod, encoder.Bytes())
	return err
}

func (client *PortalClient) Chtimes(ctx context.Context, path string, atime, mtime time.Time) error {
	var encoder portalEncoder
	encoder.string(path)
	encoder.int64(atime.Unix())
	encoder.int64(int64(atime.Nanosecond()))
	encoder.int64(mtime.Unix())
	encoder.int64(int64(mtime.Nanosecond()))
	_, err := client.call(ctx, portalOpChtimes, encoder.Bytes())
	return err
}

func (client *PortalClient) Truncate(ctx context.Context, path string, size int64) error {
	if size < 0 {
		return ErrPortalProtocol
	}
	var encoder portalEncoder
	encoder.string(path)
	encoder.int64(size)
	_, err := client.call(ctx, portalOpTruncate, encoder.Bytes())
	return err
}

func decodePortalFileInfo(payload []byte) (PortalFileInfo, error) {
	decoder := newPortalDecoder(payload)
	mode, err := decoder.uint32()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	size, err := decoder.int64()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	seconds, err := decoder.int64()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	nanoseconds, err := decoder.int64()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	inode, err := decoder.uint64()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	uid, err := decoder.uint32()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	gid, err := decoder.uint32()
	if err != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	nlink, err := decoder.uint64()
	if err != nil || decoder.done() != nil {
		return PortalFileInfo{}, ErrPortalProtocol
	}
	return PortalFileInfo{Mode: os.FileMode(mode), Size: size, ModTime: time.Unix(seconds, nanoseconds), Inode: inode, UID: uid, GID: gid, Nlink: nlink}, nil
}

func (client *PortalClient) call(ctx context.Context, opcode uint16, payload []byte) ([]byte, error) {
	if len(payload) > client.limits.FrameBytes {
		return nil, ErrPortalFrameTooLarge
	}
	requestID := client.nextID.Add(1)
	if requestID == 0 {
		return nil, ErrPortalProtocol
	}
	response := make(chan portalResponse, 1)
	client.pendingMu.Lock()
	select {
	case <-client.done:
		client.pendingMu.Unlock()
		return nil, net.ErrClosed
	default:
	}
	client.pending[requestID] = response
	client.pendingMu.Unlock()
	client.writeMu.Lock()
	err := writePortalFrame(client.connection, portalFrame{
		Kind: portalKindRequest, Opcode: opcode, RequestID: requestID, Payload: payload,
	}, client.limits.FrameBytes)
	client.writeMu.Unlock()
	if err != nil {
		client.removePending(requestID)
		return nil, err
	}
	select {
	case value := <-response:
		return value.payload, value.err
	case <-ctx.Done():
		client.removePending(requestID)
		client.writeMu.Lock()
		_ = writePortalFrame(client.connection, portalFrame{Kind: portalKindRequest, Opcode: portalOpCancel, RequestID: requestID}, client.limits.FrameBytes)
		client.writeMu.Unlock()
		return nil, ctx.Err()
	case <-client.done:
		client.removePending(requestID)
		return nil, net.ErrClosed
	}
}

func (client *PortalClient) removePending(requestID uint64) {
	client.pendingMu.Lock()
	delete(client.pending, requestID)
	client.pendingMu.Unlock()
}

func (client *PortalClient) readResponses() {
	defer close(client.done)
	for {
		frame, err := readPortalFrame(client.connection, client.limits.FrameBytes)
		if err != nil {
			client.terminate(err)
			return
		}
		if frame.Kind == portalKindTerminal {
			terminal := portalErrorForStatus(frame.Status)
			client.terminate(terminal)
			return
		}
		if frame.Kind == portalKindEvent && frame.Opcode == portalOpNotify && frame.RequestID == 0 {
			decoder := newPortalDecoder(frame.Payload)
			path, pathErr := decoder.string(4096)
			op, opErr := decoder.uint32()
			if pathErr != nil || opErr != nil || decoder.done() != nil {
				client.terminate(ErrPortalProtocol)
				return
			}
			select {
			case client.events <- PortalEvent{Path: path, Op: op}:
			default:
				client.terminate(ErrPortalOverloaded)
				return
			}
			continue
		}
		if frame.Kind != portalKindResponse || frame.RequestID == 0 {
			client.terminate(ErrPortalProtocol)
			return
		}
		client.pendingMu.Lock()
		response := client.pending[frame.RequestID]
		delete(client.pending, frame.RequestID)
		client.pendingMu.Unlock()
		if response != nil {
			response <- portalResponse{payload: frame.Payload, err: portalErrorForStatus(frame.Status)}
		}
	}
}

func (client *PortalClient) terminate(err error) {
	if err == nil {
		err = ErrPortalProtocol
	}
	client.terminalOnce.Do(func() { client.terminal <- err })
	client.failAll(err)
}

func (client *PortalClient) failAll(err error) {
	client.pendingMu.Lock()
	pending := client.pending
	client.pending = make(map[uint64]chan portalResponse)
	client.pendingMu.Unlock()
	for _, response := range pending {
		response <- portalResponse{err: err}
	}
	if !errors.Is(err, net.ErrClosed) {
		client.connection.Close()
	}
}

var _ = fmt.Sprintf
