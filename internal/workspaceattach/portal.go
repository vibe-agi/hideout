package workspaceattach

import (
	"errors"
	"os"
	"time"
)

const PortalAudience = "hideout.workspace-portal/v1"

var (
	ErrPortalFrameTooLarge     = errors.New("workspace portal frame exceeds the admitted limit")
	ErrPortalProtocol          = errors.New("workspace portal protocol violation")
	ErrPortalAuthentication    = errors.New("workspace portal authentication failed")
	ErrPortalCredentialExpired = errors.New("workspace portal credential expired")
	ErrPortalCredentialRevoked = errors.New("workspace portal credential revoked")
	ErrPortalOverloaded        = errors.New("workspace portal session limit reached")
	ErrPortalRootReplaced      = errors.New("workspace portal root identity changed")
	ErrPortalHandleNotFound    = errors.New("workspace portal handle not found")
	ErrPortalNotificationLost  = errors.New("workspace portal notification coherence lost")
)

type PortalLimits struct {
	HandlesPerSession     int
	InFlightPerSession    int
	QueuedBytesPerSession int64
	FrameBytes            int
	DirectoryEntries      int
}

func DefaultPortalLimits() PortalLimits {
	selected := SelectedLimits()
	return PortalLimits{
		HandlesPerSession: selected.HandlesPerSession, InFlightPerSession: selected.InFlightPerSession,
		QueuedBytesPerSession: selected.QueuedBytesPerSession, FrameBytes: selected.FrameBytes,
		DirectoryEntries: selected.DirectoryEntries,
	}
}

func (limits PortalLimits) validate() error {
	if limits.HandlesPerSession < 1 || limits.HandlesPerSession > 1<<20 ||
		limits.InFlightPerSession < 1 || limits.InFlightPerSession > 1<<16 ||
		limits.QueuedBytesPerSession < 4096 || limits.QueuedBytesPerSession > 1<<30 ||
		limits.FrameBytes < 1024 || limits.FrameBytes > 16<<20 ||
		limits.DirectoryEntries < 1 || limits.DirectoryEntries > 1<<20 {
		return errors.New("invalid workspace portal limits")
	}
	return nil
}

const portalCredentialBytes = 32

type PortalCredential struct {
	SessionID   string
	Environment string
	Incarnation string
	Audience    string
	Token       []byte
	Generation  uint64
	ExpiresAt   time.Time
}

type PortalFileInfo struct {
	Mode    os.FileMode
	Size    int64
	ModTime time.Time
	Inode   uint64
	UID     uint32
	GID     uint32
	Nlink   uint64
}

type PortalDirEntry struct {
	Name string
	Info PortalFileInfo
}

type PortalEvent struct {
	Path string
	Op   uint32
}
