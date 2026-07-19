package workspaceattach

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestPortalFilesystemStatusesUseStableProtocolValues(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int32
		want   error
	}{
		{name: "not found", err: syscall.ENOENT, status: portalStatusFSNotFound, want: syscall.ENOENT},
		{name: "permission", err: syscall.EACCES, status: portalStatusFSPermission, want: syscall.EACCES},
		{name: "permission from EPERM", err: syscall.EPERM, status: portalStatusFSPermission, want: syscall.EACCES},
		{name: "exists", err: syscall.EEXIST, status: portalStatusFSExists, want: syscall.EEXIST},
		{name: "not directory", err: syscall.ENOTDIR, status: portalStatusFSNotDir, want: syscall.ENOTDIR},
		{name: "is directory", err: syscall.EISDIR, status: portalStatusFSIsDir, want: syscall.EISDIR},
		{name: "not empty", err: syscall.ENOTEMPTY, status: portalStatusFSNotEmpty, want: syscall.ENOTEMPTY},
		{name: "read only", err: syscall.EROFS, status: portalStatusFSReadOnly, want: syscall.EROFS},
		{name: "no space", err: syscall.ENOSPC, status: portalStatusFSNoSpace, want: syscall.ENOSPC},
		{name: "invalid", err: syscall.EINVAL, status: portalStatusFSInvalid, want: syscall.EINVAL},
		{name: "overflow", err: syscall.EOVERFLOW, status: portalStatusFSOverflow, want: syscall.EOVERFLOW},
		{name: "unsupported", err: syscall.EOPNOTSUPP, status: portalStatusFSUnsupported, want: syscall.EOPNOTSUPP},
		{name: "symlink loop", err: syscall.ELOOP, status: portalStatusFSLoop, want: syscall.ELOOP},
		{name: "busy", err: syscall.EBUSY, status: portalStatusFSBusy, want: syscall.EBUSY},
		{name: "bad descriptor", err: syscall.EBADF, status: portalStatusFSBadFD, want: syscall.EBADF},
		{name: "interrupted", err: syscall.EINTR, status: portalStatusFSInterrupted, want: syscall.EINTR},
		{name: "would block", err: syscall.EWOULDBLOCK, status: portalStatusFSWouldBlock, want: syscall.EWOULDBLOCK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("operation: %w", test.err)
			if got := portalStatusForError(wrapped); got != test.status {
				t.Fatalf("status = %d, want protocol status %d", got, test.status)
			}
			if got := portalErrorForStatus(test.status); !errors.Is(got, test.want) {
				t.Fatalf("decoded error = %v, want %v", got, test.want)
			}
			if int32(test.err.(syscall.Errno)) == test.status {
				t.Fatalf("protocol status %d accidentally aliases the host errno", test.status)
			}
		})
	}
}

func TestPortalUnknownErrorFailsClosedAsIO(t *testing.T) {
	status := portalStatusForError(errors.New("unclassified failure"))
	if status != portalStatusFSIO {
		t.Fatalf("status = %d, want %d", status, portalStatusFSIO)
	}
	if err := portalErrorForStatus(status); !errors.Is(err, syscall.EIO) {
		t.Fatalf("decoded error = %v, want EIO", err)
	}
}

func TestPortalNotificationLossHasAStableTerminalStatus(t *testing.T) {
	if got := portalStatusForError(ErrPortalNotificationLost); got != portalStatusNotifyLost {
		t.Fatalf("notification-loss status=%d want %d", got, portalStatusNotifyLost)
	}
	if got := portalErrorForStatus(portalStatusNotifyLost); !errors.Is(got, ErrPortalNotificationLost) {
		t.Fatalf("decoded notification-loss error=%v", got)
	}
}

func TestPortalRejectsUnrecognizedPositiveStatus(t *testing.T) {
	if err := portalErrorForStatus(42); err == nil || errors.Is(err, syscall.Errno(42)) {
		t.Fatalf("unrecognized status must not be treated as a host errno: %v", err)
	}
}
