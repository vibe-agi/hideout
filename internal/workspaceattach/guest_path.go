package workspaceattach

import "github.com/vibe-agi/hideout/internal/workspacepath"

var (
	ErrGuestWorkspaceIdentityInvalid       = workspacepath.ErrIdentityInvalid
	ErrGuestWorkspacePathInvalid           = workspacepath.ErrPathInvalid
	ErrGuestWorkspacePathOutsideAttachment = workspacepath.ErrOutsideAttachment
)

type GuestPathAlias = workspacepath.Alias

const (
	GuestPathLogicalAlias  = workspacepath.LogicalAlias
	GuestPathPhysicalAlias = workspacepath.PhysicalAlias
)

// GuestWorkspacePath is the attachment-bound identity of one path. Callers
// use WorkspaceID plus RelativePath as authority; LogicalPath is the operator
// projection and PhysicalPath is an internal runtime alias.
type GuestWorkspacePath = workspacepath.Identity

func ResolveGuestWorkspacePath(workspaceID, guestPath string) (GuestWorkspacePath, error) {
	return workspacepath.Resolve(workspaceID, guestPath)
}
