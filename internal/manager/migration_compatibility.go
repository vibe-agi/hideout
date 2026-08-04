package manager

import (
	"errors"

	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	// Validation retains room for a maximum component chunk plus bounded
	// metadata/digest work. Rollback retains an independent journal and cleanup
	// margin, so a nearly full destination can still record and remove a failed
	// private stage. Neither reserve is portable-bundle size guessing.
	migrationValidationCapacityReserve = uint64(8 << 20)
	migrationRollbackCapacityReserve   = uint64(1 << 20)
)

func migrationImportCapacityRequirement(
	bundleBytes int64,
	disks []migration.DiskObject,
) (migration.CapacityRequirement, error) {
	if bundleBytes <= 0 {
		return migration.CapacityRequirement{}, ErrMigrationPlanInvalid
	}
	staging, err := migrationDiskLogicalBytes(disks)
	if err != nil || staging == 0 {
		return migration.CapacityRequirement{}, ErrMigrationPlanInvalid
	}
	if staging > migration.HardMaxLogicalBytes-migrationValidationCapacityReserve ||
		staging+migrationValidationCapacityReserve >
			migration.HardMaxLogicalBytes-migrationRollbackCapacityReserve {
		return migration.CapacityRequirement{}, errors.Join(
			ErrMigrationPlanInvalid, errors.New("migration capacity requirement exceeds limits"),
		)
	}
	requirement := migration.CapacityRequirement{
		Schema:      migration.CapacityRequirementSchema,
		BundleBytes: uint64(bundleBytes), StagingBytes: staging,
		ValidationBytes:      migrationValidationCapacityReserve,
		RollbackReserveBytes: migrationRollbackCapacityReserve,
		FinalBytes:           staging,
		PeakAdditionalBytes: staging + migrationValidationCapacityReserve +
			migrationRollbackCapacityReserve,
	}
	if err := requirement.Validate(); err != nil {
		return migration.CapacityRequirement{}, ErrMigrationPlanInvalid
	}
	return requirement, nil
}
