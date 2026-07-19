package manager

import "github.com/vibe-agi/hideout/internal/environment"

type pinnedWorkspaceBinding struct {
	HostRoot  string
	GuestRoot string
}

// pinnedEnvironmentWorkspace is the only manager boundary allowed to inspect
// an environment-level workspace binding. Shared machines deliberately have
// no such binding; their workspace authority belongs to session attachments.
func pinnedEnvironmentWorkspace(record environment.Record) (pinnedWorkspaceBinding, bool) {
	if record.Mode == environment.ModeShared {
		return pinnedWorkspaceBinding{}, false
	}
	hostRoot, guestRoot, ok := record.WorkspaceBinding()
	if !ok {
		return pinnedWorkspaceBinding{}, false
	}
	return pinnedWorkspaceBinding{HostRoot: hostRoot, GuestRoot: guestRoot}, true
}

func addPinnedEnvironmentWorkspace(details map[string]any, record environment.Record) {
	if binding, ok := pinnedEnvironmentWorkspace(record); ok {
		details["workspace"] = binding.HostRoot
		details["guestWorkspace"] = binding.GuestRoot
	}
}
