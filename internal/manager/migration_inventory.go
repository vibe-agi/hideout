package manager

import (
	"reflect"
	"slices"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

var (
	migrationConfigIncludedClasses = []string{
		"environment-declarations",
		"portable-profiles",
	}
	migrationFullIncludedClasses = []string{
		"environment-declarations",
		"persistent-disks",
		"portable-profiles",
		"profile-application-state",
	}
)

type migrationExportPlanInventory struct {
	includedClasses              []string
	environmentEstimates         []migration.ExportEnvironmentEstimate
	diskEstimates                []migration.ExportDiskEstimate
	estimatedPayloadLogicalBytes uint64
	estimatedPayloadComplete     bool
}

func (service MigrationService) buildMigrationExportPlanInventory(
	source migrationExportSource,
	inventory backend.SourceInventory,
	mode migration.ExportMode,
	selectedSecretRefs []string,
) (migrationExportPlanInventory, error) {
	if len(source.records) == 0 ||
		(mode != migration.ExportModeConfig && mode != migration.ExportModeFull) ||
		(mode == migration.ExportModeConfig && len(inventory.Disks) != 0) {
		return migrationExportPlanInventory{}, ErrMigrationPlanInvalid
	}

	review := migrationExportPlanInventory{
		includedClasses: migrationExportIncludedClasses(mode, len(selectedSecretRefs) != 0),
		environmentEstimates: make(
			[]migration.ExportEnvironmentEstimate, len(source.records),
		),
		diskEstimates:            make([]migration.ExportDiskEstimate, len(inventory.Disks)),
		estimatedPayloadComplete: len(selectedSecretRefs) == 0,
	}
	environmentIndexes := make(map[migration.OpaqueID]int, len(source.records))
	for index, record := range source.records {
		profileValue, err := service.Profiles.Load(record.Profile)
		if err != nil || profileValue.Identity.User != record.User {
			return migrationExportPlanInventory{}, ErrMigrationPlanStale
		}
		portable, err := migration.NormalizePortableProfile(profileValue)
		if err != nil {
			return migrationExportPlanInventory{}, err
		}
		encoded, err := migration.EncodePortableProfile(portable)
		if err != nil {
			return migrationExportPlanInventory{}, err
		}
		logicalBytes := uint64(len(encoded))
		digest := migrationExportBytesDigest(encoded)
		clear(encoded)
		profileStateLogicalBytes := uint64(0)
		profileStateDigest := migration.Digest("")
		if mode == migration.ExportModeFull {
			profileState, exists := source.profileStates[record.ID]
			if !exists || profileState.LogicalBytes() == 0 {
				return migrationExportPlanInventory{}, ErrMigrationPlanStale
			}
			profileStateLogicalBytes = profileState.LogicalBytes()
			profileStateDigest = migration.Digest(profileState.Digest())
		}
		total, err := migrationExportAddLogicalBytes(
			review.estimatedPayloadLogicalBytes, logicalBytes,
		)
		if err != nil {
			return migrationExportPlanInventory{}, err
		}
		review.estimatedPayloadLogicalBytes = total
		total, err = migrationExportAddLogicalBytes(
			review.estimatedPayloadLogicalBytes, profileStateLogicalBytes,
		)
		if err != nil {
			return migrationExportPlanInventory{}, err
		}
		review.estimatedPayloadLogicalBytes = total
		ref := migration.OpaqueID(record.ID)
		review.environmentEstimates[index] = migration.ExportEnvironmentEstimate{
			EnvironmentRef:             ref,
			DisplayName:                record.Name,
			PortableConfigLogicalBytes: logicalBytes,
			PortableConfigDigest:       digest,
			ProfileStateLogicalBytes:   profileStateLogicalBytes,
			ProfileStateDigest:         profileStateDigest,
			DiskRefs:                   []migration.OpaqueID{},
			EstimatedLogicalBytes:      logicalBytes + profileStateLogicalBytes,
		}
		environmentIndexes[ref] = index
	}

	for index, disk := range inventory.Disks {
		review.diskEstimates[index] = migration.ExportDiskEstimate{
			DiskRef:            disk.DiskRef,
			Role:               disk.Role,
			LogicalBytes:       disk.LogicalBytes,
			AllocatedBytesHint: disk.AllocatedBytesHint,
			Consumers:          append([]migration.OpaqueID(nil), disk.Consumers...),
		}
		total, err := migrationExportAddLogicalBytes(
			review.estimatedPayloadLogicalBytes, disk.LogicalBytes,
		)
		if err != nil {
			return migrationExportPlanInventory{}, err
		}
		review.estimatedPayloadLogicalBytes = total
		for _, consumer := range disk.Consumers {
			environmentIndex, exists := environmentIndexes[consumer]
			if !exists {
				return migrationExportPlanInventory{}, ErrMigrationPlanInvalid
			}
			estimate := &review.environmentEstimates[environmentIndex]
			estimate.DiskRefs = append(estimate.DiskRefs, disk.DiskRef)
			referenced, err := migrationExportAddLogicalBytes(
				estimate.ReferencedDiskLogicalBytes, disk.LogicalBytes,
			)
			if err != nil {
				return migrationExportPlanInventory{}, err
			}
			estimate.ReferencedDiskLogicalBytes = referenced
			profileBytes, err := migrationExportAddLogicalBytes(
				estimate.PortableConfigLogicalBytes, estimate.ProfileStateLogicalBytes,
			)
			if err != nil {
				return migrationExportPlanInventory{}, err
			}
			estimated, err := migrationExportAddLogicalBytes(
				profileBytes, referenced,
			)
			if err != nil {
				return migrationExportPlanInventory{}, err
			}
			estimate.EstimatedLogicalBytes = estimated
		}
	}
	return review, nil
}

func migrationExportIncludedClasses(
	mode migration.ExportMode,
	selectedSecrets bool,
) []string {
	var included []string
	switch mode {
	case migration.ExportModeConfig:
		included = append([]string(nil), migrationConfigIncludedClasses...)
	case migration.ExportModeFull:
		included = append([]string(nil), migrationFullIncludedClasses...)
	}
	if selectedSecrets {
		included = append(included, "selected-secret-values")
	}
	return included
}

func migrationExportAddLogicalBytes(current, value uint64) (uint64, error) {
	if value > migration.HardMaxLogicalBytes ||
		current > migration.HardMaxLogicalBytes-value {
		return 0, migration.ErrLimitExceeded
	}
	return current + value, nil
}

func (review migrationExportPlanInventory) apply(plan *migration.ExportPlan) {
	plan.IncludedClasses = append([]string(nil), review.includedClasses...)
	plan.EnvironmentEstimates = make(
		[]migration.ExportEnvironmentEstimate, len(review.environmentEstimates),
	)
	for index, estimate := range review.environmentEstimates {
		plan.EnvironmentEstimates[index] = estimate
		plan.EnvironmentEstimates[index].DiskRefs = append(
			[]migration.OpaqueID{}, estimate.DiskRefs...,
		)
	}
	plan.DiskEstimates = make(
		[]migration.ExportDiskEstimate, len(review.diskEstimates),
	)
	for index, estimate := range review.diskEstimates {
		plan.DiskEstimates[index] = estimate
		plan.DiskEstimates[index].Consumers = append(
			[]migration.OpaqueID{}, estimate.Consumers...,
		)
	}
	plan.EstimatedPayloadLogicalBytes = review.estimatedPayloadLogicalBytes
	plan.EstimatedPayloadComplete = review.estimatedPayloadComplete
}

func (review migrationExportPlanInventory) matches(plan migration.ExportPlan) bool {
	return slices.Equal(review.includedClasses, plan.IncludedClasses) &&
		reflect.DeepEqual(review.environmentEstimates, plan.EnvironmentEstimates) &&
		reflect.DeepEqual(review.diskEstimates, plan.DiskEstimates) &&
		review.estimatedPayloadLogicalBytes == plan.EstimatedPayloadLogicalBytes &&
		review.estimatedPayloadComplete == plan.EstimatedPayloadComplete
}

func validateMigrationExportPlanInventory(plan migration.ExportPlan) error {
	if !slices.Equal(
		plan.IncludedClasses,
		migrationExportIncludedClasses(plan.Mode, len(plan.SelectedSecretRefs) != 0),
	) || plan.EstimatedPayloadComplete != (len(plan.SelectedSecretRefs) == 0) {
		return ErrMigrationPlanInvalid
	}
	for _, included := range plan.IncludedClasses {
		if slices.Contains(plan.ExcludedClasses, included) {
			return ErrMigrationPlanInvalid
		}
	}
	if (plan.Mode == migration.ExportModeConfig &&
		(len(plan.DiskRefs) != 0 || len(plan.DiskEstimates) != 0)) ||
		(plan.Mode == migration.ExportModeFull &&
			(len(plan.DiskRefs) == 0 || len(plan.DiskEstimates) == 0)) {
		return ErrMigrationPlanInvalid
	}

	environmentIndexes := make(map[migration.OpaqueID]int, len(plan.EnvironmentEstimates))
	expectedDiskRefs := make([][]migration.OpaqueID, len(plan.EnvironmentEstimates))
	var aggregate uint64
	for index, estimate := range plan.EnvironmentEstimates {
		if estimate.EnvironmentRef != plan.EnvironmentRefs[index] ||
			!boundedMigrationDisplayText(estimate.DisplayName, 128) ||
			estimate.DisplayName != redactMigrationText(estimate.DisplayName) ||
			estimate.PortableConfigLogicalBytes == 0 ||
			estimate.PortableConfigLogicalBytes > migration.MaxPortableProfileBytes ||
			estimate.PortableConfigDigest.Validate() != nil ||
			(plan.Mode == migration.ExportModeFull &&
				(estimate.ProfileStateLogicalBytes == 0 ||
					estimate.ProfileStateDigest.Validate() != nil)) ||
			(plan.Mode == migration.ExportModeConfig &&
				(estimate.ProfileStateLogicalBytes != 0 || estimate.ProfileStateDigest != "")) ||
			validateSortedMigrationOpaqueIDs(estimate.DiskRefs, true) != nil {
			return ErrMigrationPlanInvalid
		}
		next, err := migrationExportAddLogicalBytes(
			aggregate, estimate.PortableConfigLogicalBytes,
		)
		if err != nil {
			return ErrMigrationPlanInvalid
		}
		aggregate = next
		next, err = migrationExportAddLogicalBytes(aggregate, estimate.ProfileStateLogicalBytes)
		if err != nil {
			return ErrMigrationPlanInvalid
		}
		aggregate = next
		environmentIndexes[estimate.EnvironmentRef] = index
	}

	for index, estimate := range plan.DiskEstimates {
		if estimate.DiskRef != plan.DiskRefs[index] ||
			(estimate.Role != migration.DiskRoleRoot &&
				estimate.Role != migration.DiskRoleAttached) ||
			estimate.LogicalBytes == 0 ||
			estimate.LogicalBytes > migration.HardMaxLogicalBytes ||
			estimate.AllocatedBytesHint > estimate.LogicalBytes ||
			validateSortedMigrationOpaqueIDs(estimate.Consumers, false) != nil ||
			(estimate.Role == migration.DiskRoleRoot && len(estimate.Consumers) != 1) {
			return ErrMigrationPlanInvalid
		}
		next, err := migrationExportAddLogicalBytes(aggregate, estimate.LogicalBytes)
		if err != nil {
			return ErrMigrationPlanInvalid
		}
		aggregate = next
		for _, consumer := range estimate.Consumers {
			environmentIndex, exists := environmentIndexes[consumer]
			if !exists {
				return ErrMigrationPlanInvalid
			}
			expectedDiskRefs[environmentIndex] = append(
				expectedDiskRefs[environmentIndex], estimate.DiskRef,
			)
		}
	}

	for index, estimate := range plan.EnvironmentEstimates {
		if !slices.Equal(estimate.DiskRefs, expectedDiskRefs[index]) ||
			(plan.Mode == migration.ExportModeFull && len(estimate.DiskRefs) == 0) {
			return ErrMigrationPlanInvalid
		}
		var referenced uint64
		for diskIndex, diskRef := range plan.DiskRefs {
			if slices.Contains(estimate.DiskRefs, diskRef) {
				next, err := migrationExportAddLogicalBytes(
					referenced, plan.DiskEstimates[diskIndex].LogicalBytes,
				)
				if err != nil {
					return ErrMigrationPlanInvalid
				}
				referenced = next
			}
		}
		profileBytes, err := migrationExportAddLogicalBytes(
			estimate.PortableConfigLogicalBytes, estimate.ProfileStateLogicalBytes,
		)
		if err != nil {
			return ErrMigrationPlanInvalid
		}
		estimated, err := migrationExportAddLogicalBytes(
			profileBytes, referenced,
		)
		if err != nil || estimate.ReferencedDiskLogicalBytes != referenced ||
			estimate.EstimatedLogicalBytes != estimated {
			return ErrMigrationPlanInvalid
		}
	}
	if aggregate != plan.EstimatedPayloadLogicalBytes {
		return ErrMigrationPlanInvalid
	}
	return nil
}
