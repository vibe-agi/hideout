package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	runtimeDirName = "daemon"
	socketName     = "hideoutd.sock"
)

type ownedSocketListener struct {
	net.Listener

	path  string
	bound os.FileInfo

	closeOnce sync.Once
	closeErr  error
}

// runtimeDir returns the operator-private daemon runtime directory under a store.
func runtimeDir(storeRoot string) string {
	return filepath.Join(storeRoot, runtimeDirName)
}

// socketPathFor returns the daemon socket path for a store root.
func socketPathFor(storeRoot string) string {
	return filepath.Join(runtimeDir(storeRoot), socketName)
}

// ensurePlacement creates the daemon runtime directory 0700 and verifies it is
// operator-private and not a guest-visible path, reusing the store's private
// layout rather than a new mechanism. It fails closed on any placement/permission
// violation so the socket can never sit somewhere a backend guest could reach.
func ensurePlacement(storeRoot string) (string, error) {
	if storeRoot == "" {
		return "", errors.New("daemon: store root is required")
	}
	dir := runtimeDir(storeRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("daemon: runtime dir %s must not be a symlink", dir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("daemon: runtime path %s is not a directory", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("daemon: runtime dir %s must be operator-private (0700), got %#o", dir, info.Mode().Perm())
	}
	// The resolved runtime dir must stay inside the store — a symlinked ancestor
	// must not redirect it into a workspace/HostFS/guest-visible path.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	resolvedStore, err := filepath.EvalSymlinks(storeRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedStore, resolvedDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("daemon: runtime dir escapes the store: %s", resolvedDir)
	}
	return dir, nil
}

// listenSocket binds the Unix socket 0600 under the validated runtime dir and
// retains the exact inode it owns so delayed shutdown cannot unlink a
// replacement daemon's socket at the same path.
func listenSocket(storeRoot string) (*ownedSocketListener, string, error) {
	dir, err := ensurePlacement(storeRoot)
	if err != nil {
		return nil, "", err
	}
	sock := filepath.Join(dir, socketName)
	// Unix socket paths are bounded by the platform (sun_path, ~104 bytes on
	// macOS). Fail closed with a clear diagnostic rather than a cryptic bind error
	// if the store path is too deep for a socket.
	if len(sock) > 100 {
		return nil, "", fmt.Errorf("daemon: socket path too long for this platform (%d bytes): %s", len(sock), sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, "", err
	}
	unixListener, ok := ln.(*net.UnixListener)
	if !ok {
		_ = ln.Close()
		return nil, "", errors.New("daemon: unix listener has an unexpected implementation")
	}
	unixListener.SetUnlinkOnClose(false)
	info, err := os.Lstat(sock)
	if err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	owned := &ownedSocketListener{Listener: ln, path: sock, bound: info}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		pathErr := fmt.Errorf("daemon: transport path is not a unix socket: %s", sock)
		return nil, "", errors.Join(pathErr, owned.Close())
	}
	if err := proveSocketPathOwnership(unixListener, sock, info); err != nil {
		return nil, "", errors.Join(err, owned.Close())
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = owned.Close()
		return nil, "", err
	}
	current, err := os.Lstat(sock)
	if err != nil {
		_ = owned.Close()
		return nil, "", err
	}
	if current.Mode()&os.ModeSymlink != 0 || current.Mode()&os.ModeSocket == 0 || !os.SameFile(info, current) {
		_ = owned.Close()
		return nil, "", fmt.Errorf("daemon: transport path does not identify the bound unix socket: %s", sock)
	}
	return owned, sock, nil
}

func proveSocketPathOwnership(listener *net.UnixListener, path string, before os.FileInfo) (returnErr error) {
	if listener == nil || before == nil {
		return errors.New("daemon: unix socket ownership proof is incomplete")
	}
	deadline := time.Now().Add(time.Second)
	if err := listener.SetDeadline(deadline); err != nil {
		return err
	}
	defer func() {
		if err := listener.SetDeadline(time.Time{}); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("daemon: clear unix socket ownership proof deadline: %w", err))
		}
	}()

	type dialResult struct {
		connection net.Conn
		err        error
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return errors.New("daemon: unix socket ownership proof deadline expired")
	}
	dialed := make(chan dialResult, 1)
	go func() {
		connection, err := net.DialTimeout("unix", path, remaining)
		dialed <- dialResult{connection: connection, err: err}
	}()
	accepted, acceptErr := listener.Accept()
	result := <-dialed
	if accepted != nil {
		_ = accepted.Close()
	}
	if result.connection != nil {
		_ = result.connection.Close()
	}
	if acceptErr != nil {
		return fmt.Errorf("daemon: prove unix socket listener ownership: %w", acceptErr)
	}
	if result.err != nil {
		return fmt.Errorf("daemon: prove unix socket path ownership: %w", result.err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if after.Mode()&os.ModeSymlink != 0 || after.Mode()&os.ModeSocket == 0 || !os.SameFile(before, after) {
		return fmt.Errorf("daemon: unix socket path changed during ownership proof: %s", path)
	}
	return nil
}

func (l *ownedSocketListener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.Listener != nil {
			if err := l.Listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				l.closeErr = err
			}
		}
		if err := removeOwnedDaemonSocket(l.path, l.bound); err != nil {
			l.closeErr = errors.Join(l.closeErr, err)
		}
	})
	return l.closeErr
}

func removeOwnedDaemonSocket(path string, owned os.FileInfo) error {
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
		return nil
	}
	return os.Remove(path)
}

// probeSocket reports whether a daemon is answering on the socket (liveness).
func probeSocket(path string) bool {
	c, err := net.Dial("unix", path)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
