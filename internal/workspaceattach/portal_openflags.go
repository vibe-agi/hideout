package workspaceattach

import (
	"syscall"
)

func encodePortalOpenFlags(flags int) (uint32, error) {
	var encoded uint32
	switch flags & syscall.O_ACCMODE {
	case syscall.O_RDONLY:
		encoded = portalOpenReadOnly
	case syscall.O_WRONLY:
		encoded = portalOpenWriteOnly
	case syscall.O_RDWR:
		encoded = portalOpenReadWrite
	default:
		return 0, syscall.EINVAL
	}
	if flags&syscall.O_APPEND != 0 {
		encoded |= portalOpenAppend
	}
	if flags&syscall.O_CREAT != 0 {
		encoded |= portalOpenCreate
	}
	if flags&syscall.O_EXCL != 0 {
		encoded |= portalOpenExclusive
	}
	if flags&syscall.O_TRUNC != 0 {
		encoded |= portalOpenTruncate
	}
	if flags&syscall.O_SYNC != 0 {
		encoded |= portalOpenSync
	}
	if flags&syscall.O_NOFOLLOW != 0 {
		encoded |= portalOpenNoFollow
	}
	if unsupported := flags &^ portalSupportedLocalOpenFlags(); unsupported != 0 {
		return 0, syscall.ENOTSUP
	}
	return encoded, nil
}

func decodePortalOpenFlags(encoded uint32) (int, error) {
	known := uint32(3) | portalOpenAppend | portalOpenCreate | portalOpenExclusive | portalOpenTruncate | portalOpenSync | portalOpenNoFollow
	if encoded&^known != 0 {
		return 0, syscall.EINVAL
	}
	var flags int
	switch encoded & 3 {
	case portalOpenReadOnly:
		flags = syscall.O_RDONLY
	case portalOpenWriteOnly:
		flags = syscall.O_WRONLY
	case portalOpenReadWrite:
		flags = syscall.O_RDWR
	default:
		return 0, syscall.EINVAL
	}
	if encoded&portalOpenAppend != 0 {
		flags |= syscall.O_APPEND
	}
	if encoded&portalOpenCreate != 0 {
		flags |= syscall.O_CREAT
	}
	if encoded&portalOpenExclusive != 0 {
		flags |= syscall.O_EXCL
	}
	if encoded&portalOpenTruncate != 0 {
		flags |= syscall.O_TRUNC
	}
	if encoded&portalOpenSync != 0 {
		flags |= syscall.O_SYNC
	}
	if encoded&portalOpenNoFollow != 0 {
		flags |= syscall.O_NOFOLLOW
	}
	return flags, nil
}
