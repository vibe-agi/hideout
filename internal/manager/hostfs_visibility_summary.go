package manager

import (
	"slices"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
)

const hostFSVisibilityNonClaim = "visible host names are user data; discover does not grant content, full metadata, writes, workspace filtering, or guest-root containment"

type HostFSVisibilityPosture struct {
	Posture        string `json:"posture"`
	DiscoverGrants int    `json:"discoverGrants"`
	DiscoverDeny   int    `json:"discoverDeny"`
	MaxListEntries int    `json:"maxListEntries"`
	MaxDepth       int    `json:"maxDepth"`
	NonClaim       string `json:"nonClaim"`
}

func summarizeHostFSVisibility(p profile.Profile, policy hostfs.EffectivePolicy) HostFSVisibilityPosture {
	out := HostFSVisibilityPosture{
		Posture:        "none-by-default",
		MaxListEntries: hostfs.DefaultMaxListEntries,
		MaxDepth:       hostfs.MaxDiscoverDepth,
		NonClaim:       hostFSVisibilityNonClaim,
	}
	for _, grant := range policy.Grants {
		if slices.Contains(grant.Ops, hostfs.OpDiscover) {
			out.DiscoverGrants++
		}
	}
	for _, grant := range policy.Deny {
		if slices.Contains(grant.Ops, hostfs.OpDiscover) {
			out.DiscoverDeny++
		}
	}
	if out.DiscoverGrants > 0 {
		out.Posture = strings.TrimSpace(p.Metadata["hostfsVisibilityPosture"])
		if out.Posture == "" {
			out.Posture = "custom-explicit"
		}
	}
	return out
}
