package lima

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"golang.org/x/sys/unix"
)

var _ backend.MigrationProvider = Backend{}

// InspectMigrationDestination is the read-only full-import preflight. It binds
// the authenticated source graph to the current provider revision and reports
// real destination capacity without creating a stage, disk, or Lima instance.
func (b Backend) InspectMigrationDestination(
	ctx context.Context,
	request backend.DestinationInspectionRequest,
) (backend.DestinationInventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.DestinationInventory{}, err
	}
	if err := ctx.Err(); err != nil {
		return backend.DestinationInventory{}, err
	}
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil {
		return backend.DestinationInventory{}, err
	}
	inventory := backend.DestinationInventory{
		Binding: request.Binding, CapabilityRevision: capability.Revision,
		SparseExtents: capability.SparseExtents,
		Conflicts:     []migration.OpaqueID{},
		Blockers:      []backend.MigrationProviderBlocker{},
	}
	addBlocker := func(code, summary, remediation string) {
		inventory.Blockers = append(inventory.Blockers, backend.MigrationProviderBlocker{
			Code: code, Summary: summary, Remediation: remediation,
		})
	}
	if capability.Revision != request.Binding.CapabilityRevision || !capability.FullImport {
		addBlocker(
			"migration.provider.capability_stale",
			"The destination Lima migration capability is unavailable or changed.",
			"Repair the installed migration provider and create a new import plan.",
		)
	}
	if !limaMigrationSourceProductCompatible(request.SourceProduct, capability) {
		addBlocker(
			"migration.provider.source_incompatible",
			"The authenticated source host, guest architecture, backend, or Lima version is outside the proved import range.",
			"Use a compatible native-architecture destination or import configuration only.",
		)
	}
	if !limaMigrationRequiredCapabilitiesSupported(
		request.RequiredCapabilities, capability,
	) {
		addBlocker(
			"migration.provider.required_capability_unavailable",
			"The destination does not satisfy every authenticated required capability.",
			"Install a compatible Hideout/Lima package or import configuration only.",
		)
	}
	if !limaMigrationDestinationGraphSupported(request, capability) {
		addBlocker(
			"migration.provider.destination_graph_unsupported",
			"The selected disk representation or attachment graph is outside the proved Lima import contract.",
			"Remove the unsupported selection or use configuration-only migration.",
		)
	}
	home, homeErr := b.migrationLimaHome()
	if homeErr != nil {
		addBlocker(
			"migration.provider.lima_home_unsafe",
			"The destination Lima storage root cannot be safely resolved.",
			"Repair the private Lima home and create a new import plan.",
		)
	} else {
		available, capacityErr := migrationDestinationAvailableBytes(home)
		if capacityErr != nil {
			addBlocker(
				"migration.provider.capacity_unproved",
				"Destination free space could not be proved.",
				"Repair the destination filesystem and create a new import plan.",
			)
		} else {
			inventory.AvailableBytes = available
			if available < request.RequiredBytes {
				addBlocker(
					"migration.provider.capacity_insufficient",
					"Destination free space is below the reviewed migration requirement.",
					"Free destination space or select fewer environments and create a new plan.",
				)
			}
		}
	}
	sort.Slice(inventory.Blockers, func(left, right int) bool {
		leftKey := inventory.Blockers[left].Code + "\x00" + inventory.Blockers[left].Summary
		rightKey := inventory.Blockers[right].Code + "\x00" + inventory.Blockers[right].Summary
		return leftKey < rightKey
	})
	inventory.Compatible = len(inventory.Blockers) == 0
	if err := inventory.Validate(); err != nil {
		return backend.DestinationInventory{}, err
	}
	return inventory, nil
}

func migrationDestinationAvailableBytes(path string) (uint64, error) {
	var facts unix.Statfs_t
	if err := unix.Statfs(path, &facts); err != nil {
		return 0, err
	}
	blockSize := uint64(facts.Bsize)
	availableBlocks := uint64(facts.Bavail)
	if blockSize == 0 || availableBlocks > math.MaxUint64/blockSize {
		return 0, errors.New("destination filesystem capacity is invalid")
	}
	return availableBlocks * blockSize, nil
}

func limaMigrationSourceProductCompatible(
	product migration.SourceProduct,
	capability backend.MigrationCapabilities,
) bool {
	if product.HostOS != "darwin" || product.HostArch != "arm64" ||
		product.GuestArch != "aarch64" || product.Backend != capability.Provider ||
		!supportedMigrationLimaImportVersion(product.BackendVersion) {
		return false
	}
	return slices.ContainsFunc(
		capability.ArchitecturePairs,
		func(pair backend.MigrationArchitecturePair) bool {
			return pair.Host == "darwin/arm64" && pair.Guest == "linux/arm64"
		},
	)
}

func supportedMigrationLimaImportVersion(value string) bool {
	return strings.HasPrefix(value, "2.1.") || strings.HasPrefix(value, "2.2.")
}

func limaMigrationRequiredCapabilitiesSupported(
	required []migration.RequiredCapability,
	capability backend.MigrationCapabilities,
) bool {
	for _, item := range required {
		if item.ID != "full-state" || item.Provider != capability.Provider ||
			(item.MinimumVersion != "" &&
				!compatibleMigrationLimaMinimum(item.MinimumVersion, capability.ProviderVersion)) {
			return false
		}
	}
	return true
}

func compatibleMigrationLimaMinimum(minimum, current string) bool {
	var minimumMajor, minimumMinor, minimumPatch int
	var currentMajor, currentMinor, currentPatch int
	if _, err := fmt.Sscanf(minimum, "%d.%d.%d", &minimumMajor, &minimumMinor, &minimumPatch); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(current, "%d.%d.%d", &currentMajor, &currentMinor, &currentPatch); err != nil {
		return false
	}
	if minimumMajor != 2 || currentMajor != 2 {
		return false
	}
	if currentMinor != minimumMinor {
		// The proved v1 compatibility range accepts 2.1 and 2.2 artifacts in
		// either direction; it does not infer compatibility for another minor.
		return (minimumMinor == 1 || minimumMinor == 2) &&
			(currentMinor == 1 || currentMinor == 2)
	}
	return currentPatch >= minimumPatch
}

func limaMigrationDestinationGraphSupported(
	request backend.DestinationInspectionRequest,
	capability backend.MigrationCapabilities,
) bool {
	for _, disk := range request.Disks {
		if !slices.Contains(capability.DiskRepresentations, disk.Format) || disk.Format != "raw" {
			return false
		}
		expectedKind := "lima-root"
		if disk.Role == migration.DiskRoleAttached {
			expectedKind = "lima-additional"
		}
		if disk.Provider.Kind != expectedKind {
			return false
		}
		for _, feature := range disk.Provider.Features {
			if feature != "sparse-extents" || !capability.SparseExtents {
				return false
			}
		}
	}
	return true
}
