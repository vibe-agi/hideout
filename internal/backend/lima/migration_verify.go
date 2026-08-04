package lima

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

const migrationAdoptionEvidenceSchema = "hideout.lima-migration-adoption-evidence/v1"

// migrationAdoptionEvidence is written only after the dedicated adoption
// executor has observed guest shutdown and removed its temporary channel. It
// bridges the intentional root-disk mutation between the immutable stage
// checkpoint and destination verification. The executor itself remains
// fail-closed until T034 can prove a package-candidate no-network VZ boot.
type migrationAdoptionEvidence struct {
	Schema                    string                         `json:"schema"`
	StageHandle               migration.OpaqueID             `json:"stageHandle"`
	StageOwnerDigest          migration.Digest               `json:"stageOwnerDigest"`
	Binding                   backend.MigrationEffectBinding `json:"binding"`
	EnvironmentRef            migration.OpaqueID             `json:"environmentRef"`
	BackendIdentity           migration.OpaqueID             `json:"backendIdentity"`
	RootDiskID                migration.OpaqueID             `json:"rootDiskId"`
	RootComponentID           migration.OpaqueID             `json:"rootComponentId"`
	PreAdoptionContentDigest  migration.Digest               `json:"preAdoptionContentDigest"`
	PostAdoptionFileIdentity  migrationStageFileIdentity     `json:"postAdoptionFileIdentity"`
	Request                   migration.AdoptionRequest      `json:"request"`
	Receipt                   migration.AdoptionReceipt      `json:"receipt"`
	ShutdownProof             migration.Digest               `json:"shutdownProof"`
	Stopped                   bool                           `json:"stopped"`
	TemporaryAuthorityRemoved bool                           `json:"temporaryAuthorityRemoved"`
	ImportedAuthorityAbsent   bool                           `json:"importedAuthorityAbsent"`
}

type migrationVerificationDiskEvidence struct {
	ComponentID              migration.OpaqueID         `json:"componentId"`
	DiskID                   migration.OpaqueID         `json:"diskId"`
	Role                     migration.DiskRole         `json:"role"`
	PreAdoptionContentDigest migration.Digest           `json:"preAdoptionContentDigest"`
	CurrentFileIdentity      migrationStageFileIdentity `json:"currentFileIdentity"`
}

type migrationVerificationAdoptionRecord struct {
	EnvironmentRef migration.OpaqueID `json:"environmentRef"`
	EvidenceDigest migration.Digest   `json:"evidenceDigest"`
}

func (b Backend) VerifyMigrationDestination(
	ctx context.Context,
	request backend.DestinationVerifyRequest,
) (backend.DestinationProof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.DestinationProof{}, err
	}
	if err := ctx.Err(); err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_interrupted", request.Binding,
			request.StageHandle, err, true,
		)
	}
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil || capability.Revision != request.Binding.CapabilityRevision ||
		!capability.FullImport || capability.AdoptionHelper == nil {
		if err == nil {
			err = errors.New("destination verification capability is stale or unavailable")
		}
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_capability_changed", request.Binding,
			request.StageHandle, err, false,
		)
	}
	for _, adoptionRequest := range request.AdoptionRequests {
		if adoptionRequest.Helper.PackageID != capability.AdoptionHelper.PackageID ||
			adoptionRequest.Helper.Version != capability.AdoptionHelper.Version ||
			adoptionRequest.Helper.SHA256 != capability.AdoptionHelper.Digest {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_helper_changed", request.Binding,
				adoptionRequest.EnvironmentRef,
				errors.New("adoption helper differs from the current capability"), false,
			)
		}
	}

	home, err := b.migrationLimaHome()
	if err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.lima_home_unsafe", request.Binding,
			request.StageHandle, err, true,
		)
	}
	stageDir := filepath.Join(
		home, "_hideout-migration", "stages", string(request.StageHandle),
	)
	for _, directory := range []string{
		filepath.Join(home, "_hideout-migration"),
		filepath.Join(home, "_hideout-migration", "stages"),
		stageDir,
	} {
		if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_ownership_unproved", request.Binding,
				request.StageHandle, err, true,
			)
		}
	}

	var owner migrationStageOwner
	if err := readMigrationJSONStrict(filepath.Join(stageDir, "owner.json"), &owner); err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	if err := owner.validate(); err != nil || owner.StageHandle != request.StageHandle ||
		owner.Binding.OperationID != request.Binding.OperationID ||
		owner.Binding.CapabilityRevision != request.Binding.CapabilityRevision ||
		owner.Binding.EffectID == request.Binding.EffectID ||
		!reflect.DeepEqual(owner.Disks, request.ExpectedDisks) {
		if err == nil {
			err = errors.New("destination stage owner does not match verification facts")
		}
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	ownerDigest, err := migrationJSONDigest(owner)
	if err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_ownership_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	complete, err := loadMigrationStageComplete(stageDir)
	if err != nil || complete.StageHandle != owner.StageHandle ||
		complete.OwnerDigest != ownerDigest {
		if err == nil {
			err = errors.New("destination stage completion proof changed")
		}
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_stage_invalid", request.Binding,
			request.StageHandle, err, true,
		)
	}

	checkpoints := make([]backend.MigrationStageCheckpoint, 0, len(owner.Entries))
	diskEvidence := make([]migrationVerificationDiskEvidence, 0, len(owner.Entries))
	entriesByDisk := make(map[migration.OpaqueID]migrationStageEntry, len(owner.Entries))
	for _, entry := range owner.Entries {
		if err := ctx.Err(); err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_interrupted", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		checkpointPath := filepath.Join(
			stageDir, "checkpoints", string(entry.ComponentID)+".json",
		)
		checkpoint, err := loadMigrationStageCheckpointMetadata(
			stageDir, owner, ownerDigest, entry, checkpointPath,
		)
		if err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		path := filepath.Join(stageDir, entry.RelativePath)
		info, err := protectedMigrationRegularFile(home, path, entry.LogicalBytes)
		if err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		identity, err := platformMigrationStageFileIdentity(info)
		if err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		format, logical, _, inspectErr := inspectMigrationDiskFile(path)
		if inspectErr != nil || format != entry.Format || logical != entry.LogicalBytes {
			if inspectErr == nil {
				inspectErr = errors.New("destination disk shape changed")
			}
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID, inspectErr, true,
			)
		}
		if entry.Role == migration.DiskRoleAttached && identity != checkpoint.FileIdentity {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID,
				errors.New("attached disk changed after authenticated staging"), true,
			)
		}
		checkpoints = append(checkpoints, backend.MigrationStageCheckpoint{
			ComponentID: checkpoint.ComponentID, NextOffset: checkpoint.NextOffset,
			ContentDigest: checkpoint.ContentDigest,
		})
		diskEvidence = append(diskEvidence, migrationVerificationDiskEvidence{
			ComponentID: entry.ComponentID, DiskID: entry.DiskID, Role: entry.Role,
			PreAdoptionContentDigest: checkpoint.ContentDigest,
			CurrentFileIdentity:      identity,
		})
		entriesByDisk[entry.DiskID] = entry
	}
	configDigests, err := materializeMigrationStageConfigurations(
		home, stageDir, owner, false,
	)
	if err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_config_changed", request.Binding,
			request.StageHandle, err, true,
		)
	}
	stageEvidenceDigest, err := migrationStageEvidenceDigest(checkpoints, configDigests)
	if err != nil || stageEvidenceDigest != complete.EvidenceDigest {
		if err == nil {
			err = errors.New("authenticated stage evidence changed")
		}
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_stage_invalid", request.Binding,
			request.StageHandle, err, true,
		)
	}

	configByEnvironment := make(
		map[migration.OpaqueID]migrationStageConfiguration, len(owner.Configurations),
	)
	for _, configuration := range owner.Configurations {
		configByEnvironment[configuration.EnvironmentRef] = configuration
	}
	adoptionEvidence := make(
		[]migrationVerificationAdoptionRecord, 0, len(request.AdoptionRequests),
	)
	safeMachineIDs := make(map[migration.Digest]struct{})
	safeSSHIDs := make(map[migration.Digest]struct{})
	for index, adoptionRequest := range request.AdoptionRequests {
		configuration, exists := configByEnvironment[adoptionRequest.EnvironmentRef]
		entry, entryExists := entriesByDisk[configuration.RootDiskID]
		if !exists || !entryExists || entry.Role != migration.DiskRoleRoot {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_adoption_invalid", request.Binding,
				adoptionRequest.EnvironmentRef,
				errors.New("adoption environment lacks its exact staged root"), true,
			)
		}
		path := filepath.Join(
			stageDir, migrationAdoptionEvidenceRelativePath(adoptionRequest.EnvironmentRef),
		)
		var observed migrationAdoptionEvidence
		if err := readMigrationJSONStrict(path, &observed); err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_adoption_invalid", request.Binding,
				adoptionRequest.EnvironmentRef, err, true,
			)
		}
		if err := observed.validate(
			owner, ownerDigest, configuration, entry, request.Binding,
			adoptionRequest, request.AdoptionReceipts[index],
		); err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_adoption_invalid", request.Binding,
				adoptionRequest.EnvironmentRef, err, true,
			)
		}
		rootPath := filepath.Join(stageDir, entry.RelativePath)
		rootInfo, err := protectedMigrationRegularFile(home, rootPath, entry.LogicalBytes)
		if err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		rootIdentity, err := platformMigrationStageFileIdentity(rootInfo)
		if err != nil || rootIdentity != observed.PostAdoptionFileIdentity {
			if err == nil {
				err = errors.New("adopted root changed after stopped proof")
			}
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_disk_changed", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		if adoptionRequest.Policy == migration.GuestIdentitySafeClone {
			post := request.AdoptionReceipts[index].PostIdentity
			if post == nil {
				return backend.DestinationProof{}, migrationStageError(
					"migration.provider.verify_adoption_invalid", request.Binding,
					adoptionRequest.EnvironmentRef,
					errors.New("Safe Clone receipt lacks post-adoption identity"), true,
				)
			}
			if _, duplicate := safeMachineIDs[post.MachineIDDigest]; duplicate {
				return backend.DestinationProof{}, migrationStageError(
					"migration.provider.verify_adoption_invalid", request.Binding,
					adoptionRequest.EnvironmentRef,
					errors.New("Safe Clone machine identity was reused"), true,
				)
			}
			safeMachineIDs[post.MachineIDDigest] = struct{}{}
			for _, digest := range post.SSHHostKeyDigests {
				if _, duplicate := safeSSHIDs[digest]; duplicate {
					return backend.DestinationProof{}, migrationStageError(
						"migration.provider.verify_adoption_invalid", request.Binding,
						adoptionRequest.EnvironmentRef,
						errors.New("Safe Clone SSH host identity was reused"), true,
					)
				}
				safeSSHIDs[digest] = struct{}{}
			}
		}
		digest, err := migrationJSONDigest(observed)
		if err != nil {
			return backend.DestinationProof{}, migrationStageError(
				"migration.provider.verify_adoption_invalid", request.Binding,
				adoptionRequest.EnvironmentRef, err, true,
			)
		}
		adoptionEvidence = append(adoptionEvidence, migrationVerificationAdoptionRecord{
			EnvironmentRef: adoptionRequest.EnvironmentRef, EvidenceDigest: digest,
		})
	}

	if err := verifyMigrationStageDestinationNamesFree(home, owner); err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_stopped_unproved", request.Binding,
			request.StageHandle, err, true,
		)
	}
	allowedFiles, allowedDirectories, err := migrationStageVerificationAllowlist(owner)
	if err == nil {
		_, _, err = inspectMigrationCleanupTree(
			ctx, stageDir, allowedFiles, allowedDirectories,
		)
	}
	if err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_temporary_authority_present", request.Binding,
			request.StageHandle, err, true,
		)
	}
	proofDigest, err := migrationJSONDigest(struct {
		Schema              string                                `json:"schema"`
		StageHandle         migration.OpaqueID                    `json:"stageHandle"`
		OwnerDigest         migration.Digest                      `json:"ownerDigest"`
		StageEvidenceDigest migration.Digest                      `json:"stageEvidenceDigest"`
		Disks               []migrationVerificationDiskEvidence   `json:"disks"`
		Adoptions           []migrationVerificationAdoptionRecord `json:"adoptions"`
	}{
		Schema:      "hideout.lima-migration-destination-proof/v1",
		StageHandle: request.StageHandle, OwnerDigest: ownerDigest,
		StageEvidenceDigest: stageEvidenceDigest,
		Disks:               diskEvidence, Adoptions: adoptionEvidence,
	})
	if err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_proof_invalid", request.Binding,
			request.StageHandle, err, true,
		)
	}
	proof := backend.DestinationProof{
		Binding: request.Binding, StageHandle: request.StageHandle,
		ProofDigest: proofDigest, Stopped: true, DigestsMatch: true,
		IdentityPolicySatisfied: true, TemporaryAuthorityRemoved: true,
		ImportedAuthorityAbsent: true,
	}
	if err := proof.Validate(); err != nil {
		return backend.DestinationProof{}, migrationStageError(
			"migration.provider.verify_proof_invalid", request.Binding,
			request.StageHandle, err, true,
		)
	}
	return proof, nil
}

func (evidence migrationAdoptionEvidence) validate(
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	configuration migrationStageConfiguration,
	entry migrationStageEntry,
	verifyBinding backend.MigrationEffectBinding,
	request migration.AdoptionRequest,
	receipt migration.AdoptionReceipt,
) error {
	if evidence.Schema != migrationAdoptionEvidenceSchema ||
		evidence.StageHandle != owner.StageHandle || evidence.StageOwnerDigest != ownerDigest ||
		evidence.Binding.Validate() != nil ||
		evidence.Binding.OperationID != verifyBinding.OperationID ||
		evidence.Binding.CapabilityRevision != verifyBinding.CapabilityRevision ||
		evidence.Binding.EffectID == owner.Binding.EffectID ||
		evidence.Binding.EffectID == verifyBinding.EffectID ||
		evidence.EnvironmentRef != configuration.EnvironmentRef ||
		evidence.Request.DestinationSSHUser != configuration.GuestUser ||
		evidence.BackendIdentity != configuration.BackendIdentity ||
		evidence.RootDiskID != configuration.RootDiskID ||
		evidence.RootDiskID != entry.DiskID ||
		evidence.RootComponentID != entry.ComponentID ||
		evidence.PreAdoptionContentDigest != entry.ContentDigest ||
		evidence.ShutdownProof.Validate() != nil ||
		!evidence.Stopped || !evidence.TemporaryAuthorityRemoved ||
		!evidence.ImportedAuthorityAbsent ||
		!reflect.DeepEqual(evidence.Request, request) ||
		!reflect.DeepEqual(evidence.Receipt, receipt) ||
		evidence.Receipt.MatchesRequest(evidence.Request) != nil {
		return errors.New("adoption evidence binding or outcome changed")
	}
	return nil
}

func migrationAdoptionEvidenceRelativePath(
	environmentRef migration.OpaqueID,
) string {
	return filepath.Join("adoption", string(environmentRef), "evidence.json")
}

func migrationStageVerificationAllowlist(
	owner migrationStageOwner,
) (map[string]struct{}, map[string]struct{}, error) {
	files := map[string]struct{}{
		"owner.json":    {},
		"complete.json": {},
	}
	for _, entry := range owner.Entries {
		files[entry.RelativePath] = struct{}{}
		files[filepath.Join("checkpoints", string(entry.ComponentID)+".json")] = struct{}{}
	}
	for _, configuration := range owner.Configurations {
		files[configuration.YAMLRelativePath] = struct{}{}
		files[configuration.NormalizedRelativePath] = struct{}{}
		files[migrationAdoptionEvidenceRelativePath(configuration.EnvironmentRef)] = struct{}{}
	}
	return migrationCleanupAllowlistForFiles(files)
}

func migrationAdoptionEvidenceFileIdentity(
	home,
	stageDir string,
	entry migrationStageEntry,
) (migrationStageFileIdentity, error) {
	path := filepath.Join(stageDir, entry.RelativePath)
	info, err := protectedMigrationRegularFile(home, path, entry.LogicalBytes)
	if err != nil {
		return migrationStageFileIdentity{}, err
	}
	return platformMigrationStageFileIdentity(info)
}

func writeMigrationAdoptionEvidenceExclusive(
	home,
	stageDir string,
	evidence migrationAdoptionEvidence,
) error {
	relative := migrationAdoptionEvidenceRelativePath(evidence.EnvironmentRef)
	if err := ensureMigrationStageParent(home, stageDir, filepath.Dir(relative)); err != nil {
		return err
	}
	path := filepath.Join(stageDir, relative)
	if !migrationPathWithin(stageDir, path) {
		return errors.New("adoption evidence path escaped its stage")
	}
	if err := writeMigrationJSONExclusive(path, evidence); err != nil {
		return err
	}
	return syncMigrationDirectory(filepath.Dir(path))
}
