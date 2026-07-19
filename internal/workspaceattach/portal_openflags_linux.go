//go:build linux

package workspaceattach

import "syscall"

func portalSupportedLocalOpenFlags() int {
	return syscall.O_ACCMODE | syscall.O_APPEND | syscall.O_CREAT | syscall.O_EXCL |
		syscall.O_TRUNC | syscall.O_SYNC | syscall.O_CLOEXEC | syscall.O_NONBLOCK |
		syscall.O_LARGEFILE | syscall.O_NOFOLLOW | portalIgnoredKernelOpenFlags()
}
