package native

import (
	"context"
	"crypto/sha256"
	"fmt"
	"runtime"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

const configMigrationVersion = "1.0.0"

// ConfigMigrationHarness advertises only the backend-independent configuration
// migration envelope. It deliberately implements capability discovery, not the
// disk provider interfaces: Manager owns portable profile materialization and
// no VM path, disk handle, or guest identity is available through this value.
type ConfigMigrationHarness struct {
	HostOS   string
	HostArch string
}

func (h ConfigMigrationHarness) MigrationCapabilities(
	ctx context.Context,
) (backend.MigrationCapabilities, error) {
	if ctx == nil {
		return backend.MigrationCapabilities{}, backend.ErrMigrationProviderRequest
	}
	if err := ctx.Err(); err != nil {
		return backend.MigrationCapabilities{}, err
	}
	hostOS := strings.TrimSpace(h.HostOS)
	hostArch := strings.TrimSpace(h.HostArch)
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	if hostArch == "" {
		hostArch = runtime.GOARCH
	}
	guestArch := hostArch
	if guestArch != "arm64" && guestArch != "amd64" {
		return backend.MigrationCapabilities{}, backend.ErrMigrationProviderCapability
	}
	facts := "hideout.native-config-migration/v1\x00" + hostOS + "/" + hostArch
	digest := sha256.Sum256([]byte(facts))
	capability := backend.MigrationCapabilities{
		Provider: "native", ProviderVersion: configMigrationVersion,
		Revision: migration.Digest(fmt.Sprintf("sha256:%x", digest[:])),
		ArchitecturePairs: []backend.MigrationArchitecturePair{{
			Host: hostOS + "/" + hostArch, Guest: "linux/" + guestArch,
		}},
		Limits: migration.DefaultLimits(),
	}
	if err := capability.Validate(); err != nil {
		return backend.MigrationCapabilities{}, err
	}
	return capability, nil
}

var _ backend.MigrationCapabilityProvider = ConfigMigrationHarness{}
