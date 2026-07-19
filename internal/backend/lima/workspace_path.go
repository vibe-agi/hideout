package lima

import (
	"fmt"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const WorkspacePathMechanism = "session-private logical symlink to opaque physical FUSE mount"

type WorkspacePathPlan struct {
	WorkspaceID  string
	LogicalRoot  string
	PhysicalRoot string
	Mechanism    string
}

func BuildWorkspacePathPlan(workspaceID string) (WorkspacePathPlan, error) {
	physicalRoot, err := workspaceattach.ResearchPhysicalWorkspaceRoot(workspaceID)
	if err != nil {
		return WorkspacePathPlan{}, fmt.Errorf("build workspace path plan: %w", err)
	}
	return WorkspacePathPlan{
		WorkspaceID: workspaceID, LogicalRoot: workspaceattach.LogicalWorkspaceRoot,
		PhysicalRoot: physicalRoot, Mechanism: WorkspacePathMechanism,
	}, nil
}
