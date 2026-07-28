package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	sessionSocketName     = "hideoutd-session.sock"
	sessionSocketLockName = "hideoutd-session.lock"
	sessionSocketPathMax  = 100

	// SessionProtocolVersion identifies the non-HTTP full-duplex run transport.
	SessionProtocolVersion = "hideout.session/v1"
)

var ErrSessionAlreadyListening = errors.New("daemon: session transport already listening")

// SessionTransportInventory describes the daemon's private non-HTTP run
// transport. It is deliberately separate from the Manager HTTP route catalog.
type SessionTransportInventory struct {
	Kind     string `json:"kind"`
	Socket   string `json:"socket"`
	Protocol string `json:"protocol"`
}

// SessionSocketPath returns the stable session socket path for a store root.
func SessionSocketPath(storeRoot string) string {
	return filepath.Join(runtimeDir(storeRoot), sessionSocketName)
}

// SessionTransportFor returns the non-HTTP session transport inventory.
func SessionTransportFor(storeRoot string) SessionTransportInventory {
	return SessionTransportInventory{
		Kind:     "unix-session",
		Socket:   SessionSocketPath(storeRoot),
		Protocol: SessionProtocolVersion,
	}
}

// SessionListener owns the session socket and its bind lock. Close removes only
// the socket inode created by this listener, so a replacement listener can never
// be deleted by a delayed cleanup.
type SessionListener struct {
	net.Listener

	path     string
	bound    os.FileInfo
	lockFile *os.File
	lockPath string

	closeOnce sync.Once
	closeErr  error
}

// ListenSession binds the daemon's private non-HTTP session socket. A separate
// bind lock serializes stale-socket reclamation and makes concurrent bind races
// converge on one winner. The daemon's process lock remains the authoritative
// single-instance boundary.
func ListenSession(storeRoot string) (*SessionListener, error) {
	dir, err := ensurePlacement(storeRoot)
	if err != nil {
		return nil, err
	}
	path := SessionSocketPath(storeRoot)
	if len(path) > sessionSocketPathMax {
		return nil, fmt.Errorf("daemon: session socket path too long for this platform (%d bytes): %s", len(path), path)
	}

	lockPath := filepath.Join(dir, sessionSocketLockName)
	if err := validateSessionBindLock(lockPath); err != nil {
		return nil, err
	}
	lockFile, err := acquireLock(lockPath)
	if err != nil {
		if IsAlreadyRunning(err) {
			return nil, ErrSessionAlreadyListening
		}
		return nil, err
	}
	keepLock := true
	defer func() {
		if keepLock {
			releaseLock(lockFile, lockPath)
		}
	}()
	if err := os.Chmod(lockPath, 0o600); err != nil {
		return nil, fmt.Errorf("daemon: secure session bind lock: %w", err)
	}

	if err := prepareSessionSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("daemon: bind session socket: %w", err)
	}
	unixListener, ok := ln.(*net.UnixListener)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("daemon: unix session listener has an unexpected implementation")
	}
	unixListener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("daemon: inspect session socket: %w", err)
	}
	keepLock = false
	owned := &SessionListener{
		Listener: ln,
		path:     path,
		bound:    info,
		lockFile: lockFile,
		lockPath: lockPath,
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		pathErr := fmt.Errorf("daemon: session transport path is not a unix socket: %s", path)
		return nil, errors.Join(pathErr, owned.Close())
	}
	if err := proveSocketPathOwnership(unixListener, path, info); err != nil {
		return nil, errors.Join(err, owned.Close())
	}

	if err := os.Chmod(path, 0o600); err != nil {
		_ = owned.Close()
		return nil, fmt.Errorf("daemon: secure session socket: %w", err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		_ = owned.Close()
		return nil, fmt.Errorf("daemon: inspect secured session socket: %w", err)
	}
	if current.Mode()&os.ModeSocket == 0 || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) {
		_ = owned.Close()
		return nil, fmt.Errorf("daemon: session transport path changed while securing: %s", path)
	}
	return owned, nil
}

// Socket returns the listener's stable filesystem path.
func (l *SessionListener) Socket() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Close stops accepting sessions, removes only this listener's socket inode,
// and releases the bind lock. It is idempotent.
func (l *SessionListener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		var errs []error
		if l.Listener != nil {
			if err := l.Listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, err)
			}
		}
		if err := removeOwnedSessionSocket(l.path, l.bound); err != nil {
			errs = append(errs, err)
		}
		releaseLock(l.lockFile, l.lockPath)
		l.closeErr = errors.Join(errs...)
	})
	return l.closeErr
}

func validateSessionBindLock(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("daemon: session bind lock must be a regular file: %s", path)
	}
	return nil
}

func prepareSessionSocket(path string) error {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("daemon: refusing symlinked session socket path: %s", path)
	}
	if before.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("daemon: refusing non-socket session transport path: %s", path)
	}

	conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return ErrSessionAlreadyListening
	}

	// Revalidate the exact inode after the liveness probe. A concurrent replacement
	// is never treated as the stale socket we observed.
	after, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("daemon: revalidate stale session socket: %w", err)
	}
	if after.Mode()&os.ModeSymlink != 0 || after.Mode()&os.ModeSocket == 0 || !os.SameFile(before, after) {
		return fmt.Errorf("daemon: session socket changed during stale cleanup: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("daemon: remove stale session socket: %w", err)
	}
	return nil
}

func removeOwnedSessionSocket(path string, owned os.FileInfo) error {
	if path == "" || owned == nil {
		return nil
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || current.Mode()&os.ModeSocket == 0 || !os.SameFile(owned, current) {
		return fmt.Errorf("daemon: refusing to remove session socket not owned by listener: %s", path)
	}
	return os.Remove(path)
}
