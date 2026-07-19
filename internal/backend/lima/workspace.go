package lima

import (
	"errors"
	"net"
	"strings"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const workspacePortalGuestHost = "host.lima.internal"

const (
	GuestWorkspacePortalPath = GuestSessionDir + "/shims/hideout-workspace-portal"
	guestWorkspaceRootFS     = GuestRuntimeDir + "/workspace-rootfs"
)

// NewWorkspaceProviderFactory composes the selected host Portal provider with
// Lima's host-to-guest address projection. The provider still binds only to
// host loopback; no selected project fact enters the retained VM config.
func NewWorkspaceProviderFactory() *workspaceattach.PortalProviderFactory {
	return workspaceattach.NewPortalProviderFactory(workspaceattach.PortalProviderFactoryOptions{
		Advertise: WorkspacePortalGuestEndpoint,
	})
}

// WorkspacePortalGuestEndpoint maps an ephemeral host-loopback listener to the
// stable hostname Lima exposes inside the guest.
func WorkspacePortalGuestEndpoint(boundAddress string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(boundAddress))
	if err != nil {
		return "", errors.New("workspace Portal listener address is invalid")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || port == "" || port == "0" {
		return "", errors.New("workspace Portal listener must be bound to an assigned host-loopback port")
	}
	return net.JoinHostPort(workspacePortalGuestHost, port), nil
}
