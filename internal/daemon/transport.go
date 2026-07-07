package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	runtimeDirName = "daemon"
	socketName     = "hideoutd.sock"
)

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

// listenSocket binds the Unix socket 0600 under the validated runtime dir.
func listenSocket(storeRoot string) (net.Listener, string, error) {
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
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	return ln, sock, nil
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
