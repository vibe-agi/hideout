package lima

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"gopkg.in/yaml.v3"
)

const (
	migrationStageOwnerSchema      = "hideout.lima-migration-stage-owner/v1"
	migrationStageCheckpointSchema = "hideout.lima-migration-stage-checkpoint/v1"
	migrationStageCompleteSchema   = "hideout.lima-migration-stage-complete/v1"
	migrationStageConfigSchema     = "hideout.lima-migration-stage-config/v1"
)

type migrationStageOwner struct {
	Schema         string                                  `json:"schema"`
	StageHandle    migration.OpaqueID                      `json:"stageHandle"`
	Binding        backend.MigrationEffectBinding          `json:"binding"`
	Objects        []backend.MigrationDestinationObject    `json:"objects"`
	Disks          []migration.DiskObject                  `json:"disks"`
	Edges          []migration.DiskEdge                    `json:"edges"`
	Components     []backend.MigrationDestinationComponent `json:"components"`
	Entries        []migrationStageEntry                   `json:"entries"`
	Configurations []migrationStageConfiguration           `json:"configurations"`
	ObjectHandles  []migration.OpaqueID                    `json:"objectHandles"`
}

type migrationStageEntry struct {
	ComponentID   migration.OpaqueID `json:"componentId"`
	DiskID        migration.OpaqueID `json:"diskId"`
	Role          migration.DiskRole `json:"role"`
	Format        string             `json:"format"`
	LogicalBytes  uint64             `json:"logicalBytes"`
	ContentDigest migration.Digest   `json:"contentDigest"`
	ObjectHandle  migration.OpaqueID `json:"objectHandle"`
	RelativePath  string             `json:"relativePath"`
}

type migrationStageConfiguration struct {
	EnvironmentRef         migration.OpaqueID   `json:"environmentRef"`
	BackendIdentity        migration.OpaqueID   `json:"backendIdentity"`
	Runtime                string               `json:"runtime"`
	GuestArchitecture      string               `json:"guestArchitecture"`
	GuestUser              string               `json:"guestUser"`
	ProfileComponent       migration.OpaqueID   `json:"profileComponent"`
	RootDiskID             migration.OpaqueID   `json:"rootDiskId"`
	AttachedDiskHandles    []migration.OpaqueID `json:"attachedDiskHandles"`
	YAMLRelativePath       string               `json:"yamlRelativePath"`
	NormalizedRelativePath string               `json:"normalizedRelativePath"`
}

type migrationStageCheckpoint struct {
	Schema        string                         `json:"schema"`
	StageHandle   migration.OpaqueID             `json:"stageHandle"`
	Binding       backend.MigrationEffectBinding `json:"binding"`
	OwnerDigest   migration.Digest               `json:"ownerDigest"`
	ComponentID   migration.OpaqueID             `json:"componentId"`
	DiskID        migration.OpaqueID             `json:"diskId"`
	RelativePath  string                         `json:"relativePath"`
	NextOffset    uint64                         `json:"nextOffset"`
	ContentDigest migration.Digest               `json:"contentDigest"`
	FileIdentity  migrationStageFileIdentity     `json:"fileIdentity"`
}

type migrationStageFileIdentity struct {
	Device          uint64 `json:"device"`
	Inode           uint64 `json:"inode"`
	Links           uint64 `json:"links"`
	Size            int64  `json:"size"`
	Mode            uint32 `json:"mode"`
	ModTimeUnixNano int64  `json:"modTimeUnixNano"`
}

type migrationStageConfigDigest struct {
	EnvironmentRef   migration.OpaqueID `json:"environmentRef"`
	YAMLDigest       migration.Digest   `json:"yamlDigest"`
	NormalizedDigest migration.Digest   `json:"normalizedDigest"`
}

type migrationStageComplete struct {
	Schema         string             `json:"schema"`
	StageHandle    migration.OpaqueID `json:"stageHandle"`
	OwnerDigest    migration.Digest   `json:"ownerDigest"`
	EvidenceDigest migration.Digest   `json:"evidenceDigest"`
}

type migrationStagedLimaConfig struct {
	VMType          string        `yaml:"vmType"`
	Arch            string        `yaml:"arch"`
	MountType       string        `yaml:"mountType"`
	MountInotify    bool          `yaml:"mountInotify"`
	User            user          `yaml:"user"`
	Containerd      containerd    `yaml:"containerd"`
	Mounts          []mount       `yaml:"mounts"`
	PortForwards    []portForward `yaml:"portForwards"`
	AdditionalDisks []string      `yaml:"additionalDisks"`
}

type migrationNormalizedStageConfig struct {
	Schema               string               `json:"schema"`
	EnvironmentRef       migration.OpaqueID   `json:"environmentRef"`
	BackendIdentity      migration.OpaqueID   `json:"backendIdentity"`
	Runtime              string               `json:"runtime"`
	GuestArchitecture    string               `json:"guestArchitecture"`
	GuestUser            string               `json:"guestUser"`
	ProfileComponent     migration.OpaqueID   `json:"profileComponent"`
	RootDiskID           migration.OpaqueID   `json:"rootDiskId"`
	AttachedDiskHandles  []migration.OpaqueID `json:"attachedDiskHandles"`
	HostMountsEnabled    bool                 `json:"hostMountsEnabled"`
	ImportedNetwork      bool                 `json:"importedNetwork"`
	ImportedProvisioning bool                 `json:"importedProvisioning"`
	Runnable             bool                 `json:"runnable"`
}

func (b Backend) StageMigrationDestination(
	ctx context.Context,
	request backend.DestinationStageRequest,
) (backend.DestinationStage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.DestinationStage{}, err
	}
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil {
		return backend.DestinationStage{}, err
	}
	if capability.Revision != request.Binding.CapabilityRevision || !capability.FullImport {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_capability_changed", request.Binding,
			request.StagingHandle, errors.New("destination capability is stale or unavailable"), false,
		)
	}
	if err := validateLimaDestinationStageRequest(request); err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_unsupported", request.Binding,
			request.StagingHandle, err, false,
		)
	}
	home, err := b.migrationLimaHome()
	if err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.lima_home_unsafe", request.Binding,
			request.StagingHandle, err, false,
		)
	}
	owner, err := buildMigrationStageOwner(request)
	if err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_plan_invalid", request.Binding,
			request.StagingHandle, err, false,
		)
	}
	stageDir, ownerDigest, err := prepareMigrationStageOwner(home, owner)
	if err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_ownership_unproved", request.Binding,
			request.StagingHandle, err, true,
		)
	}
	if _, completeErr := loadMigrationStageComplete(stageDir); completeErr == nil {
		return loadCompletedMigrationStage(home, stageDir, owner, ownerDigest)
	} else if !errors.Is(completeErr, os.ErrNotExist) {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", request.Binding,
			request.StagingHandle, completeErr, true,
		)
	}

	checkpoints := make([]backend.MigrationStageCheckpoint, 0, len(owner.Entries))
	for _, entry := range owner.Entries {
		checkpoint, err := materializeMigrationStageEntry(
			ctx, home, stageDir, owner, ownerDigest, entry,
			capability.Limits.MaxChunkBytes, request.ReadComponent,
		)
		if err != nil {
			return backend.DestinationStage{}, migrationStageError(
				"migration.provider.stage_component_invalid", request.Binding,
				entry.ComponentID, err, true,
			)
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	configDigests, err := materializeMigrationStageConfigurations(
		home, stageDir, owner, true,
	)
	if err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_config_invalid", request.Binding,
			request.StagingHandle, err, true,
		)
	}
	evidenceDigest, err := migrationStageEvidenceDigest(checkpoints, configDigests)
	if err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", request.Binding,
			request.StagingHandle, err, true,
		)
	}
	complete := migrationStageComplete{
		Schema: migrationStageCompleteSchema, StageHandle: owner.StageHandle,
		OwnerDigest: ownerDigest, EvidenceDigest: evidenceDigest,
	}
	if err := writeMigrationJSONExclusive(
		filepath.Join(stageDir, "complete.json"), complete,
	); err != nil && !errors.Is(err, os.ErrExist) {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", request.Binding,
			request.StagingHandle, err, true,
		)
	}
	if err := syncMigrationDirectory(stageDir); err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", request.Binding,
			request.StagingHandle, err, true,
		)
	}
	return loadCompletedMigrationStage(home, stageDir, owner, ownerDigest)
}

func validateLimaDestinationStageRequest(request backend.DestinationStageRequest) error {
	for _, object := range request.Objects {
		if object.Runtime != "linux" || object.GuestArchitecture != "linux/arm64" ||
			!migrationProviderObjectName(string(object.BackendIdentity)) {
			return errors.New("destination object is outside the proved Lima guest contract")
		}
	}
	for _, disk := range request.Disks {
		if disk.Format != "raw" {
			return errors.New("destination disk format is outside the proved Lima contract")
		}
		expectedKind := "lima-root"
		if disk.Role == migration.DiskRoleAttached {
			expectedKind = "lima-additional"
		}
		if disk.Provider.Kind != expectedKind {
			return errors.New("destination disk kind is outside the proved Lima contract")
		}
	}
	return nil
}

func buildMigrationStageOwner(
	request backend.DestinationStageRequest,
) (migrationStageOwner, error) {
	componentByDisk := make(
		map[migration.OpaqueID]backend.MigrationDestinationComponent,
		len(request.Components),
	)
	for _, component := range request.Components {
		componentByDisk[component.DiskID] = component
	}
	objectByEnvironment := make(
		map[migration.OpaqueID]backend.MigrationDestinationObject,
		len(request.Objects),
	)
	for _, object := range request.Objects {
		objectByEnvironment[object.EnvironmentRef] = object
	}
	rootEnvironmentByDisk := make(map[migration.OpaqueID]migration.OpaqueID)
	attachedByEnvironment := make(map[migration.OpaqueID][]migration.OpaqueID)
	for _, edge := range request.Edges {
		if edge.Attachment == migration.DiskRoleRoot {
			rootEnvironmentByDisk[edge.DiskID] = edge.EnvironmentRef
		} else {
			handle := migrationDestinationDiskHandle(request, edge.DiskID)
			attachedByEnvironment[edge.EnvironmentRef] = append(
				attachedByEnvironment[edge.EnvironmentRef], handle,
			)
		}
	}

	entries := make([]migrationStageEntry, 0, len(request.Disks))
	objectHandles := make([]migration.OpaqueID, 0, len(request.Objects)+len(request.Disks))
	for _, object := range request.Objects {
		objectHandles = append(objectHandles, object.BackendIdentity)
	}
	for _, disk := range request.Disks {
		component, exists := componentByDisk[disk.DiskID]
		if !exists {
			return migrationStageOwner{}, errors.New("destination disk lacks an authenticated component")
		}
		entry := migrationStageEntry{
			ComponentID: component.ComponentID, DiskID: disk.DiskID,
			Role: disk.Role, Format: disk.Format, LogicalBytes: disk.LogicalBytes,
			ContentDigest: disk.ContentDigest,
		}
		if disk.Role == migration.DiskRoleRoot {
			environmentRef, exists := rootEnvironmentByDisk[disk.DiskID]
			object, objectExists := objectByEnvironment[environmentRef]
			if !exists || !objectExists {
				return migrationStageOwner{}, errors.New("destination root disk has no exact owner")
			}
			entry.ObjectHandle = object.BackendIdentity
			entry.RelativePath = filepath.Join(
				"instances", string(object.BackendIdentity), "disk",
			)
		} else {
			entry.ObjectHandle = migrationDestinationDiskHandle(request, disk.DiskID)
			entry.RelativePath = filepath.Join(
				"disks", string(entry.ObjectHandle), "datadisk",
			)
			objectHandles = append(objectHandles, entry.ObjectHandle)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].ComponentID < entries[right].ComponentID
	})
	sort.Slice(objectHandles, func(left, right int) bool {
		return objectHandles[left] < objectHandles[right]
	})

	rootDiskByEnvironment := make(map[migration.OpaqueID]migration.OpaqueID)
	for diskID, environmentRef := range rootEnvironmentByDisk {
		rootDiskByEnvironment[environmentRef] = diskID
	}
	configurations := make([]migrationStageConfiguration, 0, len(request.Objects))
	for _, object := range request.Objects {
		attached := append([]migration.OpaqueID(nil), attachedByEnvironment[object.EnvironmentRef]...)
		sort.Slice(attached, func(left, right int) bool { return attached[left] < attached[right] })
		configurations = append(configurations, migrationStageConfiguration{
			EnvironmentRef: object.EnvironmentRef, BackendIdentity: object.BackendIdentity,
			Runtime: object.Runtime, GuestArchitecture: object.GuestArchitecture,
			GuestUser: object.GuestUser, ProfileComponent: object.ProfileComponent,
			RootDiskID:          rootDiskByEnvironment[object.EnvironmentRef],
			AttachedDiskHandles: attached,
			YAMLRelativePath: filepath.Join(
				"instances", string(object.BackendIdentity), "lima.yaml",
			),
			NormalizedRelativePath: filepath.Join(
				"instances", string(object.BackendIdentity), "normalized.json",
			),
		})
	}
	return migrationStageOwner{
		Schema: migrationStageOwnerSchema, StageHandle: request.StagingHandle,
		Binding: request.Binding,
		Objects: append([]backend.MigrationDestinationObject(nil), request.Objects...),
		Disks:   append([]migration.DiskObject(nil), request.Disks...),
		Edges:   append([]migration.DiskEdge(nil), request.Edges...),
		Components: append(
			[]backend.MigrationDestinationComponent(nil), request.Components...,
		),
		Entries: entries, Configurations: configurations, ObjectHandles: objectHandles,
	}, nil
}

func (owner migrationStageOwner) validate() error {
	if owner.Schema != migrationStageOwnerSchema || !migrationValidOpaqueRef(owner.StageHandle) ||
		owner.Binding.Validate() != nil {
		return errors.New("destination stage owner envelope is invalid")
	}
	request := backend.DestinationStageRequest{
		Binding: owner.Binding, StagingHandle: owner.StageHandle,
		Objects: owner.Objects, Disks: owner.Disks, Edges: owner.Edges,
		Components: owner.Components,
		ReadComponent: func(
			context.Context, migration.OpaqueID, uint64, uint32,
			func(backend.MigrationExtent) error,
		) error {
			return errors.New("owner validation reader must not execute")
		},
	}
	if err := request.Validate(); err != nil {
		return err
	}
	expected, err := buildMigrationStageOwner(request)
	if err != nil || !reflect.DeepEqual(owner, expected) {
		return errors.New("destination stage owner graph or paths changed")
	}
	return nil
}

func prepareMigrationStageOwner(
	home string,
	expected migrationStageOwner,
) (string, migration.Digest, error) {
	root, err := ensurePrivateMigrationDirectory(home, "_hideout-migration")
	if err != nil {
		return "", "", err
	}
	stagesRoot, err := ensurePrivateMigrationDirectory(root, "stages")
	if err != nil {
		return "", "", err
	}
	stageDir := filepath.Join(stagesRoot, string(expected.StageHandle))
	created := false
	if _, err := os.Lstat(stageDir); errors.Is(err, os.ErrNotExist) {
		if err := verifyMigrationStageDestinationNamesFree(home, expected); err != nil {
			return "", "", err
		}
		if err := os.Mkdir(stageDir, 0o700); err != nil {
			return "", "", err
		}
		if err := syncMigrationDirectory(stagesRoot); err != nil {
			return "", "", err
		}
		created = true
	} else if err != nil {
		return "", "", err
	}
	if _, err := protectedMigrationDirectory(home, stageDir, stageDir); err != nil {
		return "", "", err
	}
	ownerPath := filepath.Join(stageDir, "owner.json")
	if created {
		if err := writeMigrationJSONExclusive(ownerPath, expected); err != nil {
			return "", "", err
		}
		if err := syncMigrationDirectory(stageDir); err != nil {
			return "", "", err
		}
	}
	var observed migrationStageOwner
	if err := readMigrationJSONStrict(ownerPath, &observed); err != nil {
		return "", "", err
	}
	if err := observed.validate(); err != nil || !reflect.DeepEqual(observed, expected) {
		return "", "", errors.New("destination stage owner binding is absent or changed")
	}
	digest, err := migrationJSONDigest(observed)
	if err != nil {
		return "", "", err
	}
	return stageDir, digest, nil
}

func verifyMigrationStageDestinationNamesFree(home string, owner migrationStageOwner) error {
	for _, configuration := range owner.Configurations {
		if _, err := os.Lstat(filepath.Join(home, string(configuration.BackendIdentity))); !errors.Is(err, os.ErrNotExist) {
			return errors.New("destination Lima instance identity is already occupied")
		}
	}
	for _, entry := range owner.Entries {
		if entry.Role != migration.DiskRoleAttached {
			continue
		}
		if _, err := os.Lstat(filepath.Join(home, "_disks", string(entry.ObjectHandle))); !errors.Is(err, os.ErrNotExist) {
			return errors.New("destination Lima disk identity is already occupied")
		}
	}
	return nil
}

func materializeMigrationStageEntry(
	ctx context.Context,
	home,
	stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	entry migrationStageEntry,
	maxChunkBytes uint32,
	read backend.MigrationComponentReader,
) (backend.MigrationStageCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	path := filepath.Join(stageDir, entry.RelativePath)
	if !migrationPathWithin(stageDir, path) {
		return backend.MigrationStageCheckpoint{}, errors.New("stage component escaped its owner directory")
	}
	if err := ensureMigrationStageParent(home, stageDir, filepath.Dir(entry.RelativePath)); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	checkpointsDir, err := ensurePrivateMigrationDirectory(stageDir, "checkpoints")
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	checkpointPath := filepath.Join(checkpointsDir, string(entry.ComponentID)+".json")
	if _, err := os.Lstat(checkpointPath); err == nil {
		return loadMigrationStageCheckpoint(
			home, stageDir, owner, ownerDigest, entry, checkpointPath,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return backend.MigrationStageCheckpoint{}, err
	}

	partial := path + ".partial"
	for _, stale := range []string{path, partial} {
		if err := removeExactMigrationStageFile(stageDir, stale); err != nil {
			return backend.MigrationStageCheckpoint{}, err
		}
	}
	file, err := os.OpenFile(partial, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if err := file.Truncate(int64(entry.LogicalBytes)); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	digester, err := migration.NewLogicalDigester(entry.LogicalBytes)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	nextOffset := uint64(0)
	callbackErr := error(nil)
	err = read(
		ctx, entry.ComponentID, 0, maxChunkBytes,
		func(extent backend.MigrationExtent) error {
			if callbackErr != nil {
				return callbackErr
			}
			if err := ctx.Err(); err != nil {
				callbackErr = err
				return err
			}
			if err := extent.Validate(maxChunkBytes); err != nil ||
				extent.LogicalOffset != nextOffset ||
				extent.Length > entry.LogicalBytes-nextOffset {
				if err == nil {
					err = errors.New("component extent is out of order or out of bounds")
				}
				callbackErr = err
				return err
			}
			if err := digester.WriteExtent(migration.Extent{
				Kind: extent.Kind, LogicalOffset: extent.LogicalOffset,
				Length: extent.Length, Data: extent.Data,
			}); err != nil {
				callbackErr = err
				return err
			}
			if extent.Kind == migration.ExtentData {
				written, err := file.WriteAt(extent.Data, int64(extent.LogicalOffset))
				if err != nil || written != len(extent.Data) {
					if err == nil {
						err = errors.New("short destination component write")
					}
					callbackErr = err
					return err
				}
			}
			nextOffset += extent.Length
			return nil
		},
	)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	if callbackErr != nil {
		return backend.MigrationStageCheckpoint{}, callbackErr
	}
	if nextOffset != entry.LogicalBytes {
		return backend.MigrationStageCheckpoint{}, errors.New("component stream ended before its logical size")
	}
	contentDigest, err := digester.Finish()
	if err != nil || contentDigest != entry.ContentDigest {
		if err == nil {
			err = errors.New("component logical digest does not match its manifest")
		}
		return backend.MigrationStageCheckpoint{}, err
	}
	if err := file.Sync(); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	if err := file.Close(); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	closed = true
	format, logical, _, err := inspectMigrationDiskFile(partial)
	if err != nil || format != entry.Format || logical != entry.LogicalBytes {
		return backend.MigrationStageCheckpoint{}, errors.New("materialized component shape is invalid")
	}
	if err := os.Link(partial, path); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	if err := os.Remove(partial); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	if err := syncMigrationDirectory(filepath.Dir(path)); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	finalInfo, err := os.Lstat(path)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	fileIdentity, err := platformMigrationStageFileIdentity(finalInfo)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	checkpoint := migrationStageCheckpoint{
		Schema: migrationStageCheckpointSchema, StageHandle: owner.StageHandle,
		Binding: owner.Binding, OwnerDigest: ownerDigest,
		ComponentID: entry.ComponentID, DiskID: entry.DiskID,
		RelativePath: entry.RelativePath, NextOffset: entry.LogicalBytes,
		ContentDigest: contentDigest, FileIdentity: fileIdentity,
	}
	if err := writeMigrationJSONExclusive(checkpointPath, checkpoint); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	if err := syncMigrationDirectory(checkpointsDir); err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	return backend.MigrationStageCheckpoint{
		ComponentID: checkpoint.ComponentID, NextOffset: checkpoint.NextOffset,
		ContentDigest: checkpoint.ContentDigest,
	}, nil
}

func loadMigrationStageCheckpoint(
	home,
	stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	entry migrationStageEntry,
	checkpointPath string,
) (backend.MigrationStageCheckpoint, error) {
	checkpoint, err := loadMigrationStageCheckpointMetadata(
		stageDir, owner, ownerDigest, entry, checkpointPath,
	)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	path := filepath.Join(stageDir, entry.RelativePath)
	info, err := protectedMigrationRegularFile(home, path, entry.LogicalBytes)
	if err != nil {
		return backend.MigrationStageCheckpoint{}, err
	}
	fileIdentity, err := platformMigrationStageFileIdentity(info)
	if err != nil || fileIdentity != checkpoint.FileIdentity {
		return backend.MigrationStageCheckpoint{}, errors.New("checkpointed destination component identity changed")
	}
	format, logical, _, err := inspectMigrationDiskFile(path)
	if err != nil || format != entry.Format || logical != entry.LogicalBytes {
		return backend.MigrationStageCheckpoint{}, errors.New("checkpointed destination component shape changed")
	}
	return backend.MigrationStageCheckpoint{
		ComponentID: checkpoint.ComponentID, NextOffset: checkpoint.NextOffset,
		ContentDigest: checkpoint.ContentDigest,
	}, nil
}

func loadMigrationStageCheckpointMetadata(
	stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	entry migrationStageEntry,
	checkpointPath string,
) (migrationStageCheckpoint, error) {
	var checkpoint migrationStageCheckpoint
	if err := readMigrationJSONStrict(checkpointPath, &checkpoint); err != nil {
		return migrationStageCheckpoint{}, err
	}
	if checkpoint.Schema != migrationStageCheckpointSchema ||
		checkpoint.StageHandle != owner.StageHandle || checkpoint.Binding != owner.Binding ||
		checkpoint.OwnerDigest != ownerDigest || checkpoint.ComponentID != entry.ComponentID ||
		checkpoint.DiskID != entry.DiskID || checkpoint.RelativePath != entry.RelativePath ||
		checkpoint.NextOffset != entry.LogicalBytes ||
		checkpoint.ContentDigest != entry.ContentDigest {
		return migrationStageCheckpoint{}, errors.New("destination component checkpoint changed")
	}
	path := filepath.Join(stageDir, entry.RelativePath)
	if !migrationPathWithin(stageDir, path) {
		return migrationStageCheckpoint{}, errors.New("destination component path escaped its stage")
	}
	return checkpoint, nil
}

func materializeMigrationStageConfigurations(
	home,
	stageDir string,
	owner migrationStageOwner,
	create bool,
) ([]migrationStageConfigDigest, error) {
	digests := make([]migrationStageConfigDigest, 0, len(owner.Configurations))
	for _, configuration := range owner.Configurations {
		yamlData, normalizedData, err := migrationStageConfigurationBytes(configuration)
		if err != nil {
			return nil, err
		}
		yamlPath := filepath.Join(stageDir, configuration.YAMLRelativePath)
		normalizedPath := filepath.Join(stageDir, configuration.NormalizedRelativePath)
		if !migrationPathWithin(stageDir, yamlPath) || !migrationPathWithin(stageDir, normalizedPath) {
			return nil, errors.New("normalized destination config escaped its stage")
		}
		if err := ensureMigrationStageParent(
			home, stageDir, filepath.Dir(configuration.YAMLRelativePath),
		); err != nil {
			return nil, err
		}
		if err := writeOrVerifyMigrationStageFile(yamlPath, yamlData, create); err != nil {
			return nil, err
		}
		if err := writeOrVerifyMigrationStageFile(normalizedPath, normalizedData, create); err != nil {
			return nil, err
		}
		if err := syncMigrationDirectory(filepath.Dir(yamlPath)); err != nil {
			return nil, err
		}
		digests = append(digests, migrationStageConfigDigest{
			EnvironmentRef:   configuration.EnvironmentRef,
			YAMLDigest:       migrationBytesDigest(yamlData),
			NormalizedDigest: migrationBytesDigest(normalizedData),
		})
	}
	return digests, nil
}

func migrationStageConfigurationBytes(
	configuration migrationStageConfiguration,
) ([]byte, []byte, error) {
	additional := make([]string, len(configuration.AttachedDiskHandles))
	for index, handle := range configuration.AttachedDiskHandles {
		additional[index] = string(handle)
	}
	denyForwards := []portForward{
		{
			GuestIP: "0.0.0.0", GuestIPMustBeZero: boolPtr(false), Proto: "any",
			GuestPortRange: [2]int{1, 65535}, Ignore: true,
		},
		{
			GuestIP: "127.0.0.1", Proto: "any",
			GuestPortRange: [2]int{1, 65535}, Ignore: true,
		},
	}
	limaConfig := migrationStagedLimaConfig{
		VMType: "vz", Arch: "aarch64", MountType: "virtiofs", MountInotify: false,
		User: user{
			Name: configuration.GuestUser, Comment: "Hideout imported guest user",
			UID: 1000, Home: "/home/" + configuration.GuestUser, Shell: "/bin/bash",
		},
		Containerd: containerd{System: false, User: false},
		Mounts:     []mount{}, PortForwards: denyForwards, AdditionalDisks: additional,
	}
	yamlData, err := yaml.Marshal(limaConfig)
	if err != nil || len(yamlData) == 0 || len(yamlData) > migrationSnapshotMetadataLimit {
		return nil, nil, errors.New("normalized Lima config is invalid or oversized")
	}
	normalized := migrationNormalizedStageConfig{
		Schema:            migrationStageConfigSchema,
		EnvironmentRef:    configuration.EnvironmentRef,
		BackendIdentity:   configuration.BackendIdentity,
		Runtime:           configuration.Runtime,
		GuestArchitecture: configuration.GuestArchitecture,
		GuestUser:         configuration.GuestUser,
		ProfileComponent:  configuration.ProfileComponent,
		RootDiskID:        configuration.RootDiskID,
		AttachedDiskHandles: append(
			[]migration.OpaqueID(nil), configuration.AttachedDiskHandles...,
		),
		HostMountsEnabled: false, ImportedNetwork: false,
		ImportedProvisioning: false, Runnable: false,
	}
	normalizedData, err := json.Marshal(normalized)
	if err != nil || len(normalizedData) == 0 || len(normalizedData) > migrationSnapshotMetadataLimit {
		return nil, nil, errors.New("normalized destination metadata is invalid or oversized")
	}
	normalizedData = append(normalizedData, '\n')
	return yamlData, normalizedData, nil
}

func loadCompletedMigrationStage(
	home,
	stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
) (backend.DestinationStage, error) {
	complete, err := loadMigrationStageComplete(stageDir)
	if err != nil || complete.StageHandle != owner.StageHandle || complete.OwnerDigest != ownerDigest {
		if err == nil {
			err = errors.New("destination stage completion binding changed")
		}
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", owner.Binding,
			owner.StageHandle, err, true,
		)
	}
	checkpoints := make([]backend.MigrationStageCheckpoint, 0, len(owner.Entries))
	for _, entry := range owner.Entries {
		checkpointPath := filepath.Join(
			stageDir, "checkpoints", string(entry.ComponentID)+".json",
		)
		checkpoint, err := loadMigrationStageCheckpoint(
			home, stageDir, owner, ownerDigest, entry, checkpointPath,
		)
		if err != nil {
			return backend.DestinationStage{}, migrationStageError(
				"migration.provider.stage_component_invalid", owner.Binding,
				entry.ComponentID, err, true,
			)
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	configDigests, err := materializeMigrationStageConfigurations(home, stageDir, owner, false)
	if err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_config_invalid", owner.Binding,
			owner.StageHandle, err, true,
		)
	}
	evidenceDigest, err := migrationStageEvidenceDigest(checkpoints, configDigests)
	if err != nil || evidenceDigest != complete.EvidenceDigest {
		if err == nil {
			err = errors.New("destination stage evidence changed")
		}
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", owner.Binding,
			owner.StageHandle, err, true,
		)
	}
	stage := backend.DestinationStage{
		Binding: owner.Binding, StageHandle: owner.StageHandle,
		ObjectHandles: append([]migration.OpaqueID(nil), owner.ObjectHandles...),
		Checkpoints:   checkpoints, Stopped: true, Runnable: false,
	}
	if err := stage.Validate(); err != nil {
		return backend.DestinationStage{}, migrationStageError(
			"migration.provider.stage_completion_invalid", owner.Binding,
			owner.StageHandle, err, true,
		)
	}
	return stage, nil
}

func loadMigrationStageComplete(stageDir string) (migrationStageComplete, error) {
	path := filepath.Join(stageDir, "complete.json")
	if _, err := os.Lstat(path); err != nil {
		return migrationStageComplete{}, err
	}
	var complete migrationStageComplete
	if err := readMigrationJSONStrict(path, &complete); err != nil {
		return migrationStageComplete{}, err
	}
	if complete.Schema != migrationStageCompleteSchema ||
		!migrationValidOpaqueRef(complete.StageHandle) ||
		complete.OwnerDigest.Validate() != nil || complete.EvidenceDigest.Validate() != nil {
		return migrationStageComplete{}, errors.New("destination stage completion metadata is invalid")
	}
	return complete, nil
}

func migrationStageEvidenceDigest(
	checkpoints []backend.MigrationStageCheckpoint,
	configs []migrationStageConfigDigest,
) (migration.Digest, error) {
	return migrationJSONDigest(struct {
		Schema      string                             `json:"schema"`
		Checkpoints []backend.MigrationStageCheckpoint `json:"checkpoints"`
		Configs     []migrationStageConfigDigest       `json:"configs"`
	}{
		Schema:      "hideout.lima-migration-stage-evidence/v1",
		Checkpoints: checkpoints, Configs: configs,
	})
}

func ensureMigrationStageParent(home, stageDir, relative string) error {
	if relative == "." || filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return errors.New("destination stage directory is invalid")
	}
	current := stageDir
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if !migrationProviderObjectName(part) {
			return errors.New("destination stage directory name is invalid")
		}
		var err error
		current, err = ensurePrivateMigrationDirectory(current, part)
		if err != nil {
			return err
		}
	}
	_, err := protectedMigrationDirectory(home, current, current)
	return err
}

func removeExactMigrationStageFile(stageDir, path string) error {
	if !migrationPathWithin(stageDir, path) {
		return errors.New("destination stage cleanup path escaped its owner")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("destination stage cleanup target is not a regular file")
	}
	return os.Remove(path)
}

func protectedMigrationRegularFile(
	home,
	path string,
	logicalBytes uint64,
) (os.FileInfo, error) {
	if !migrationPathWithin(home, path) {
		return nil, errors.New("migration file escaped the Lima home")
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil || physical != path {
		return nil, errors.New("migration file is aliased")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() != int64(logicalBytes) {
		return nil, errors.New("migration file shape or protection changed")
	}
	return info, nil
}

func writeOrVerifyMigrationStageFile(path string, expected []byte, create bool) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if !create {
			return errors.New("completed destination stage metadata is absent")
		}
		// The O_EXCL create below is the only path that may establish this file.
	} else if err != nil {
		return err
	} else if observed, _, err := readStableMigrationFile(path, migrationSnapshotMetadataLimit); err == nil {
		if !bytes.Equal(observed, expected) {
			return errors.New("destination stage metadata changed")
		}
		return nil
	} else {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(expected); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncMigrationDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func migrationBytesDigest(data []byte) migration.Digest {
	digest := sha256.Sum256(data)
	return migration.Digest("sha256:" + hex.EncodeToString(digest[:]))
}

func migrationDestinationDiskHandle(
	request backend.DestinationStageRequest,
	diskID migration.OpaqueID,
) migration.OpaqueID {
	for _, component := range request.Components {
		if component.DiskID == diskID {
			return component.BackendIdentity
		}
	}
	return ""
}

func migrationStageError(
	code string,
	binding backend.MigrationEffectBinding,
	ref migration.OpaqueID,
	cause error,
	recoveryRequired bool,
) error {
	return &backend.MigrationProviderError{
		Code: code, Binding: binding, OpaqueRef: string(ref),
		Retryable: true, RecoveryRequired: recoveryRequired, Cause: cause,
	}
}
