//go:build hideout_e2e

package daemon

import "github.com/vibe-agi/hideout/internal/manager"

// PublishWorkspaceViewEvidence exposes the production workspace-view publisher
// only to the real browser/PTY evidence build. It adds no product endpoint and
// carries no capability authority.
func (d *Daemon) PublishWorkspaceViewEvidence(views []manager.WorkspaceViewSnapshot) {
	if d == nil || d.bus == nil {
		return
	}
	d.bus.publishWorkspaceViews(views)
}
