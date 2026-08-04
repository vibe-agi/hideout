package lima

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	migrationActivationIntentSchema   = "hideout.lima-migration-activation-intent/v1"
	migrationActivationCompleteSchema = "hideout.lima-migration-activation-complete/v1"
)

type migrationActivationIntent struct {
	Schema        string                         `json:"schema"`
	Binding       backend.MigrationEffectBinding `json:"binding"`
	Proof         backend.DestinationProof       `json:"proof"`
	OwnerDigest   migration.Digest               `json:"ownerDigest"`
	ObjectHandles []migration.OpaqueID           `json:"objectHandles"`
}

type migrationActivationComplete struct {
	Schema        string                         `json:"schema"`
	Binding       backend.MigrationEffectBinding `json:"binding"`
	StageHandle   migration.OpaqueID             `json:"stageHandle"`
	ProofDigest   migration.Digest               `json:"proofDigest"`
	OwnerDigest   migration.Digest               `json:"ownerDigest"`
	ObjectHandles []migration.OpaqueID           `json:"objectHandles"`
}

// ActivateMigrationDestination is a one-way, restart-safe stopped promotion.
// It first independently replays destination verification, durably records an
// activation intent, and only then moves operation-owned private directories
// into previously absent Lima names using no-replace renames. Manager remains
// the sole visibility authority after this method returns.
func (b Backend) ActivateMigrationDestination(
	ctx context.Context,
	request backend.DestinationActivationRequest,
) (backend.DestinationActivation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.DestinationActivation{}, err
	}
	if err := ctx.Err(); err != nil {
		return backend.DestinationActivation{}, err
	}
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil || !capability.FullImport ||
		capability.Revision != request.Binding.CapabilityRevision {
		if err == nil {
			err = errors.New("destination activation capability is stale or unavailable")
		}
		return backend.DestinationActivation{}, migrationStageError(
			"migration.provider.activation_capability_changed", request.Binding,
			request.Proof.StageHandle, err, false,
		)
	}
	home, err := b.migrationLimaHome()
	if err != nil {
		return backend.DestinationActivation{}, migrationStageError(
			"migration.provider.lima_home_unsafe", request.Binding,
			request.Proof.StageHandle, err, true,
		)
	}
	stageDir := filepath.Join(
		home, "_hideout-migration", "stages", string(request.Proof.StageHandle),
	)
	for _, directory := range []string{
		filepath.Join(home, "_hideout-migration"),
		filepath.Join(home, "_hideout-migration", "stages"),
		stageDir,
	} {
		if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_ownership_unproved", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
	}
	owner, ownerDigest, err := loadMigrationActivationOwner(stageDir, request)
	if err != nil {
		return backend.DestinationActivation{}, migrationStageError(
			"migration.provider.activation_ownership_unproved", request.Binding,
			request.Proof.StageHandle, err, true,
		)
	}
	intent := migrationActivationIntent{
		Schema: migrationActivationIntentSchema, Binding: request.Binding,
		Proof: request.Proof, OwnerDigest: ownerDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
	}
	intentPath := filepath.Join(stageDir, "activation-intent.json")
	intentExists := false
	if _, err := os.Lstat(intentPath); err == nil {
		intentExists = true
		var observed migrationActivationIntent
		if err := readMigrationJSONStrict(intentPath, &observed); err != nil ||
			!reflect.DeepEqual(observed, intent) {
			if err == nil {
				err = errors.New("activation intent binding changed")
			}
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_ownership_unproved", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return backend.DestinationActivation{}, err
	}
	if !intentExists {
		evidence, err := migrationActivationAdoptionEvidence(stageDir, owner)
		if err != nil {
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_proof_changed", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
		verified, err := b.VerifyMigrationDestination(
			ctx, migrationActivationVerifyRequestFromEvidence(
				owner, request.Proof.Binding, evidence,
			),
		)
		if err != nil || !reflect.DeepEqual(verified, request.Proof) {
			if err == nil {
				err = errors.New("activation proof does not equal a fresh provider verification")
			}
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_proof_changed", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
		if err := writeMigrationJSONExclusive(intentPath, intent); err != nil {
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_intent_failed", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
		if err := syncMigrationDirectory(stageDir); err != nil {
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_intent_failed", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
	}

	completePath := filepath.Join(stageDir, "activation-complete.json")
	complete := migrationActivationComplete{
		Schema: migrationActivationCompleteSchema, Binding: request.Binding,
		StageHandle: request.Proof.StageHandle, ProofDigest: request.Proof.ProofDigest,
		OwnerDigest:   ownerDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
	}
	if _, err := os.Lstat(completePath); err == nil {
		var observed migrationActivationComplete
		if err := readMigrationJSONStrict(completePath, &observed); err != nil ||
			!reflect.DeepEqual(observed, complete) ||
			verifyMigrationActivatedObjects(home, stageDir, owner, capability.ProviderVersion) != nil {
			if err == nil {
				err = errors.New("activation completion proof changed")
			}
			return backend.DestinationActivation{}, migrationStageError(
				"migration.provider.activation_completion_invalid", request.Binding,
				request.Proof.StageHandle, err, true,
			)
		}
		return migrationActivationResponse(request), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return backend.DestinationActivation{}, err
	}

	if err := promoteMigrationActivationObjects(
		ctx, home, stageDir, owner, capability.ProviderVersion,
	); err != nil {
		return backend.DestinationActivation{}, migrationStageError(
			"migration.provider.activation_promotion_failed", request.Binding,
			request.Proof.StageHandle, err, true,
		)
	}
	if err := writeMigrationJSONExclusive(completePath, complete); err != nil &&
		!errors.Is(err, os.ErrExist) {
		return backend.DestinationActivation{}, migrationStageError(
			"migration.provider.activation_completion_failed", request.Binding,
			request.Proof.StageHandle, err, true,
		)
	}
	if err := syncMigrationDirectory(stageDir); err != nil {
		return backend.DestinationActivation{}, migrationStageError(
			"migration.provider.activation_completion_failed", request.Binding,
			request.Proof.StageHandle, err, true,
		)
	}
	return migrationActivationResponse(request), nil
}

func loadMigrationActivationOwner(
	stageDir string,
	request backend.DestinationActivationRequest,
) (migrationStageOwner, migration.Digest, error) {
	var owner migrationStageOwner
	if err := readMigrationJSONStrict(filepath.Join(stageDir, "owner.json"), &owner); err != nil {
		return migrationStageOwner{}, "", err
	}
	if err := owner.validate(); err != nil ||
		owner.StageHandle != request.Proof.StageHandle ||
		owner.Binding.OperationID != request.Binding.OperationID ||
		owner.Binding.CapabilityRevision != request.Binding.CapabilityRevision ||
		owner.Binding.EffectID == request.Binding.EffectID ||
		!slices.Equal(owner.ObjectHandles, request.ObjectHandles) {
		return migrationStageOwner{}, "", errors.New("activation owner does not match the request")
	}
	digest, err := migrationJSONDigest(owner)
	return owner, digest, err
}

func promoteMigrationActivationObjects(
	ctx context.Context,
	home, stageDir string,
	owner migrationStageOwner,
	limaVersion string,
) error {
	disksRoot, err := ensurePrivateMigrationDirectory(home, "_disks")
	if err != nil {
		return err
	}
	attached := make([]migrationStageEntry, 0)
	for _, entry := range owner.Entries {
		if entry.Role == migration.DiskRoleAttached {
			attached = append(attached, entry)
		}
	}
	for _, entry := range attached {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := filepath.Join(stageDir, "disks", string(entry.ObjectHandle))
		destination := filepath.Join(disksRoot, string(entry.ObjectHandle))
		if err := promoteMigrationDirectoryNoReplace(
			source, destination,
			func(path string) error {
				return verifyMigrationActivationDiskDirectory(
					home, stageDir, owner, entry, path,
				)
			},
		); err != nil {
			return err
		}
	}
	for _, configuration := range owner.Configurations {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := filepath.Join(
			stageDir, "instances", string(configuration.BackendIdentity),
		)
		destination := filepath.Join(home, string(configuration.BackendIdentity))
		if _, err := os.Lstat(source); err == nil {
			if err := ensureMigrationActivationLimaVersion(source, limaVersion); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := promoteMigrationDirectoryNoReplace(
			source, destination,
			func(path string) error {
				return verifyMigrationActivationInstanceDirectory(
					home, stageDir, owner, configuration, path, limaVersion,
				)
			},
		); err != nil {
			return err
		}
	}
	return verifyMigrationActivatedObjects(home, stageDir, owner, limaVersion)
}

func promoteMigrationDirectoryNoReplace(
	source, destination string,
	verify func(string) error,
) error {
	sourceExists, err := migrationActivationDirectoryExists(source)
	if err != nil {
		return err
	}
	destinationExists, err := migrationActivationDirectoryExists(destination)
	if err != nil {
		return err
	}
	switch {
	case sourceExists && !destinationExists:
		if err := verify(source); err != nil {
			return err
		}
		if err := renameMigrationDirectoryNoReplace(source, destination); err != nil {
			return err
		}
		if err := syncMigrationDirectory(filepath.Dir(source)); err != nil {
			return err
		}
		if err := syncMigrationDirectory(filepath.Dir(destination)); err != nil {
			return err
		}
		return verify(destination)
	case !sourceExists && destinationExists:
		return verify(destination)
	case sourceExists && destinationExists:
		return errors.New("activation source and destination are both occupied")
	default:
		return errors.New("activation source and destination are both absent")
	}
}

func migrationActivationDirectoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return false, errors.New("activation directory is unsafe")
	}
	return true, nil
}

func ensureMigrationActivationLimaVersion(directory, version string) error {
	if !supportedMigrationLimaImportVersion(version) {
		return errors.New("activation Lima version is unsupported")
	}
	path := filepath.Join(directory, "lima-version")
	expected := []byte(version + "\n")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(expected)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return err
		}
		return syncMigrationDirectory(directory)
	} else if err != nil {
		return err
	}
	return verifyMigrationActivationLimaVersion(directory, version)
}

func verifyMigrationActivationLimaVersion(directory, version string) error {
	if !supportedMigrationLimaImportVersion(version) {
		return errors.New("activation Lima version is unsupported")
	}
	path := filepath.Join(directory, "lima-version")
	expected := []byte(version + "\n")
	observed, _, err := readStableMigrationFile(path, migrationSnapshotMetadataLimit)
	if err != nil || !bytes.Equal(observed, expected) {
		return errors.New("activation Lima version metadata changed")
	}
	return nil
}

func verifyMigrationActivatedObjects(
	home, stageDir string,
	owner migrationStageOwner,
	limaVersion string,
) error {
	for _, entry := range owner.Entries {
		if entry.Role != migration.DiskRoleAttached {
			continue
		}
		path := filepath.Join(home, "_disks", string(entry.ObjectHandle))
		if err := verifyMigrationActivationDiskDirectory(
			home, stageDir, owner, entry, path,
		); err != nil {
			return err
		}
	}
	for _, configuration := range owner.Configurations {
		path := filepath.Join(home, string(configuration.BackendIdentity))
		if err := verifyMigrationActivationInstanceDirectory(
			home, stageDir, owner, configuration, path, limaVersion,
		); err != nil {
			return err
		}
	}
	return nil
}

func verifyMigrationActivationDiskDirectory(
	home, stageDir string,
	owner migrationStageOwner,
	entry migrationStageEntry,
	directory string,
) error {
	if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
		return err
	}
	if err := migrationActivationDirectoryEntries(directory, []string{"datadisk"}); err != nil {
		return err
	}
	path := filepath.Join(directory, "datadisk")
	info, err := protectedMigrationRegularFile(home, path, entry.LogicalBytes)
	if err != nil {
		return err
	}
	identity, err := platformMigrationStageFileIdentity(info)
	if err != nil {
		return err
	}
	ownerDigest, err := migrationJSONDigest(owner)
	if err != nil {
		return err
	}
	checkpointPath := filepath.Join(
		stageDir, "checkpoints", string(entry.ComponentID)+".json",
	)
	checkpoint, err := loadMigrationStageCheckpointMetadata(
		stageDir, owner, ownerDigest, entry, checkpointPath,
	)
	if err != nil || checkpoint.FileIdentity != identity {
		return errors.New("activated attached disk identity changed")
	}
	format, logical, _, err := inspectMigrationDiskFile(path)
	if err != nil || format != entry.Format || logical != entry.LogicalBytes {
		return errors.New("activated attached disk shape changed")
	}
	return nil
}

func verifyMigrationActivationInstanceDirectory(
	home, stageDir string,
	owner migrationStageOwner,
	configuration migrationStageConfiguration,
	directory, limaVersion string,
) error {
	if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
		return err
	}
	if err := migrationActivationDirectoryEntries(
		directory, []string{"disk", "lima-version", "lima.yaml", "normalized.json"},
	); err != nil {
		return err
	}
	if err := verifyMigrationActivationLimaVersion(directory, limaVersion); err != nil {
		return err
	}
	yamlData, normalizedData, err := migrationStageConfigurationBytes(configuration)
	if err != nil {
		return err
	}
	for path, expected := range map[string][]byte{
		filepath.Join(directory, "lima.yaml"):       yamlData,
		filepath.Join(directory, "normalized.json"): normalizedData,
	} {
		observed, _, err := readStableMigrationFile(path, migrationSnapshotMetadataLimit)
		if err != nil || !bytes.Equal(observed, expected) {
			return errors.New("activated Lima configuration changed")
		}
	}
	entry, exists := migrationActivationRootEntry(owner, configuration.RootDiskID)
	if !exists {
		return errors.New("activated instance root binding is absent")
	}
	rootPath := filepath.Join(directory, "disk")
	rootInfo, err := protectedMigrationRegularFile(home, rootPath, entry.LogicalBytes)
	if err != nil {
		return err
	}
	rootIdentity, err := platformMigrationStageFileIdentity(rootInfo)
	if err != nil {
		return err
	}
	var adoption migrationAdoptionEvidence
	if err := readMigrationJSONStrict(
		filepath.Join(stageDir, migrationAdoptionEvidenceRelativePath(configuration.EnvironmentRef)),
		&adoption,
	); err != nil || adoption.PostAdoptionFileIdentity != rootIdentity ||
		!adoption.Stopped || !adoption.TemporaryAuthorityRemoved ||
		!adoption.ImportedAuthorityAbsent {
		return errors.New("activated root lacks its stopped adoption proof")
	}
	format, logical, _, err := inspectMigrationDiskFile(rootPath)
	if err != nil || format != entry.Format || logical != entry.LogicalBytes {
		return errors.New("activated root disk shape changed")
	}
	return nil
}

func migrationActivationRootEntry(
	owner migrationStageOwner,
	diskID migration.OpaqueID,
) (migrationStageEntry, bool) {
	for _, entry := range owner.Entries {
		if entry.DiskID == diskID && entry.Role == migration.DiskRoleRoot {
			return entry, true
		}
	}
	return migrationStageEntry{}, false
}

func migrationActivationDirectoryEntries(directory string, expected []string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	actual := make([]string, len(entries))
	for index, entry := range entries {
		actual[index] = entry.Name()
	}
	sort.Strings(actual)
	expected = append([]string(nil), expected...)
	sort.Strings(expected)
	if !slices.Equal(actual, expected) {
		return errors.New("activation directory contains an unexpected node")
	}
	return nil
}

func migrationActivationResponse(
	request backend.DestinationActivationRequest,
) backend.DestinationActivation {
	return backend.DestinationActivation{
		Binding: request.Binding, StageHandle: request.Proof.StageHandle,
		ProofDigest:   request.Proof.ProofDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
		Stopped:       true, Promoted: true,
	}
}

func migrationActivationAdoptionEvidence(
	stageDir string,
	owner migrationStageOwner,
) ([]migrationAdoptionEvidence, error) {
	values := make([]migrationAdoptionEvidence, 0, len(owner.Configurations))
	for _, configuration := range owner.Configurations {
		var value migrationAdoptionEvidence
		if err := readMigrationJSONStrict(
			filepath.Join(stageDir, migrationAdoptionEvidenceRelativePath(configuration.EnvironmentRef)),
			&value,
		); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func migrationActivationVerifyRequestFromEvidence(
	owner migrationStageOwner,
	binding backend.MigrationEffectBinding,
	evidence []migrationAdoptionEvidence,
) backend.DestinationVerifyRequest {
	request := backend.DestinationVerifyRequest{
		Binding: binding, StageHandle: owner.StageHandle,
		ExpectedDisks:    append([]migration.DiskObject(nil), owner.Disks...),
		IdentityPolicies: make([]migration.IdentitySelection, len(evidence)),
		AdoptionRequests: make([]migration.AdoptionRequest, len(evidence)),
		AdoptionReceipts: make([]migration.AdoptionReceipt, len(evidence)),
	}
	for index, value := range evidence {
		request.IdentityPolicies[index] = migration.IdentitySelection{
			SourceRef: value.EnvironmentRef, Policy: value.Request.Policy,
		}
		request.AdoptionRequests[index] = value.Request
		request.AdoptionReceipts[index] = value.Receipt
	}
	sort.Slice(request.IdentityPolicies, func(left, right int) bool {
		return request.IdentityPolicies[left].SourceRef < request.IdentityPolicies[right].SourceRef
	})
	sort.Slice(request.AdoptionRequests, func(left, right int) bool {
		return request.AdoptionRequests[left].EnvironmentRef < request.AdoptionRequests[right].EnvironmentRef
	})
	sort.Slice(request.AdoptionReceipts, func(left, right int) bool {
		return request.AdoptionReceipts[left].EnvironmentRef < request.AdoptionReceipts[right].EnvironmentRef
	})
	return request
}
