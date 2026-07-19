//go:build darwin

package workspaceattach

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const darwinMaxPathBytes = 1024

func canonicalPathFromOpenFile(file *os.File, fallback string) (string, error) {
	if file == nil {
		return "", errors.New("workspace root file is required")
	}
	var buffer [darwinMaxPathBytes]byte
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", errno
	}
	end := bytes.IndexByte(buffer[:], 0)
	if end <= 0 {
		return "", errors.New("workspace root canonical path is unavailable")
	}
	path := filepath.Clean(string(buffer[:end]))
	if !filepath.IsAbs(path) {
		return "", errors.New("workspace root canonical path is not absolute")
	}
	return path, nil
}
