package lima

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	migrationSnapshotOwnerSchema    = "hideout.lima-migration-snapshot-owner/v1"
	migrationSnapshotCompleteSchema = "hideout.lima-migration-snapshot-complete/v1"
	migrationSnapshotMetadataLimit  = 1 << 20
	migrationSnapshotCommandTimeout = 2 * time.Minute
)

type migrationSnapshotOwner struct {
	Schema          string                             `json:"schema"`
	SnapshotHandle  migration.OpaqueID                 `json:"snapshotHandle"`
	Binding         backend.MigrationEffectBinding     `json:"binding"`
	InventoryDigest migration.Digest                   `json:"inventoryDigest"`
	Selections      []backend.MigrationSourceSelection `json:"selections"`
	DiskRefs        []migration.OpaqueID               `json:"diskRefs"`
	Entries         []migrationSnapshotEntry           `json:"entries"`
}

type migrationSnapshotEntry struct {
	ComponentID   migration.OpaqueID `json:"componentId"`
	DiskRef       migration.OpaqueID `json:"diskRef"`
	Role          migration.DiskRole `json:"role"`
	Format        string             `json:"format"`
	LogicalBytes  uint64             `json:"logicalBytes"`
	SourceObject  string             `json:"sourceObject"`
	CloneInstance string             `json:"cloneInstance,omitempty"`
	RelativePath  string             `json:"relativePath,omitempty"`
}

type migrationSnapshotComplete struct {
	Schema         string             `json:"schema"`
	SnapshotHandle migration.OpaqueID `json:"snapshotHandle"`
	OwnerDigest    migration.Digest   `json:"ownerDigest"`
}

var _ backend.MigrationExportProvider = Backend{}

func (b Backend) SnapshotMigrationSource(
	ctx context.Context,
	request backend.SourceSnapshotRequest,
) (backend.SourceSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.SourceSnapshot{}, err
	}
	inventory, err := b.InspectMigrationSource(ctx, backend.SourceInspectionRequest{
		Binding: request.Binding, Mode: migration.ExportModeFull,
		Selections: request.Selections,
	})
	if err != nil {
		return backend.SourceSnapshot{}, err
	}
	if inventory.InventoryDigest != request.InventoryDigest || !inventory.Capturable {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_source_changed", request.Binding, "",
			errors.New("source inventory is stale or no longer capturable"), false,
		)
	}
	observedDiskRefs := make([]migration.OpaqueID, len(inventory.Disks))
	for index, disk := range inventory.Disks {
		observedDiskRefs[index] = disk.DiskRef
	}
	if !slices.Equal(observedDiskRefs, request.DiskRefs) {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_selection_changed", request.Binding, "",
			errors.New("snapshot disk selection does not equal the inspected graph"), false,
		)
	}

	home, err := b.migrationLimaHome()
	if err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.lima_home_unsafe", request.Binding, "", err, false,
		)
	}
	owner, err := b.buildMigrationSnapshotOwner(ctx, request, inventory)
	if err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_plan_failed", request.Binding, "", err, false,
		)
	}
	snapshotDir, ownerDigest, err := prepareMigrationSnapshotOwner(home, owner)
	if err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_ownership_unproved",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	if complete, completeErr := loadMigrationSnapshotComplete(snapshotDir); completeErr == nil {
		if complete.SnapshotHandle != owner.SnapshotHandle || complete.OwnerDigest != ownerDigest {
			return backend.SourceSnapshot{}, migrationSnapshotError(
				"migration.provider.snapshot_ownership_unproved",
				request.Binding, owner.SnapshotHandle,
				errors.New("snapshot completion binding does not match owner"), true,
			)
		}
		return b.loadCompletedMigrationSnapshot(home, snapshotDir, owner, ownerDigest)
	} else if !errors.Is(completeErr, os.ErrNotExist) {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_ownership_unproved",
			request.Binding, owner.SnapshotHandle, completeErr, true,
		)
	}

	if err := b.probeMigrationCloneFile(snapshotDir); err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.cow_unavailable",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	for _, entry := range owner.Entries {
		if err := b.materializeMigrationSnapshotEntry(
			ctx, home, snapshotDir, entry,
		); err != nil {
			return backend.SourceSnapshot{}, migrationSnapshotError(
				"migration.provider.snapshot_materialization_failed",
				request.Binding, entry.ComponentID, err, true,
			)
		}
	}
	if err := verifyMigrationSnapshotDetached(home, owner); err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_detachment_unproved",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	if _, err := b.observeMigrationSnapshotIdentities(
		ctx, home, snapshotDir, owner, ownerDigest,
	); err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.source_identity_unproved",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	postInventory, err := b.InspectMigrationSource(ctx, backend.SourceInspectionRequest{
		Binding: request.Binding, Mode: migration.ExportModeFull,
		Selections: request.Selections,
	})
	if err != nil || !postInventory.Capturable ||
		postInventory.InventoryDigest != request.InventoryDigest {
		if err == nil {
			err = errors.New("source changed while snapshot materialized")
		}
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_source_changed",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	if err := writeMigrationJSONExclusive(
		filepath.Join(snapshotDir, "complete.json"),
		migrationSnapshotComplete{
			Schema:         migrationSnapshotCompleteSchema,
			SnapshotHandle: owner.SnapshotHandle,
			OwnerDigest:    ownerDigest,
		},
	); err != nil && !errors.Is(err, os.ErrExist) {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_completion_failed",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	if err := syncMigrationDirectory(snapshotDir); err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_completion_failed",
			request.Binding, owner.SnapshotHandle, err, true,
		)
	}
	return b.loadCompletedMigrationSnapshot(home, snapshotDir, owner, ownerDigest)
}

// ReleaseMigrationSnapshot removes only a snapshot whose durable owner record
// exactly matches the operation/effect binding. Missing state is an idempotent
// success; an occupied or rebound handle fails closed.
func (b Backend) ReleaseMigrationSnapshot(
	ctx context.Context,
	request backend.SnapshotReleaseRequest,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := b.migrationLimaHome()
	if err != nil {
		return migrationSnapshotError(
			"migration.provider.lima_home_unsafe", request.Binding,
			request.SnapshotHandle, err, false,
		)
	}
	migrationRoot := filepath.Join(home, "_hideout-migration")
	snapshotsRoot := filepath.Join(migrationRoot, "snapshots")
	snapshotDir := filepath.Join(snapshotsRoot, string(request.SnapshotHandle))
	if _, err := os.Lstat(snapshotDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return migrationSnapshotError(
			"migration.provider.snapshot_cleanup_unproved", request.Binding,
			request.SnapshotHandle, err, true,
		)
	}
	for _, directory := range []string{migrationRoot, snapshotsRoot, snapshotDir} {
		if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
			return migrationSnapshotError(
				"migration.provider.snapshot_cleanup_unproved", request.Binding,
				request.SnapshotHandle, err, true,
			)
		}
	}
	var owner migrationSnapshotOwner
	if err := readMigrationJSONStrict(filepath.Join(snapshotDir, "owner.json"), &owner); err != nil ||
		owner.validate(home, snapshotDir) != nil || owner.SnapshotHandle != request.SnapshotHandle ||
		owner.Binding != request.Binding {
		if err == nil {
			err = errors.New("snapshot cleanup owner binding changed")
		}
		return migrationSnapshotError(
			"migration.provider.snapshot_cleanup_unproved", request.Binding,
			request.SnapshotHandle, err, true,
		)
	}
	files, directories, err := inspectMigrationSnapshotCleanupTree(ctx, snapshotDir, owner)
	if err != nil {
		return migrationSnapshotError(
			"migration.provider.snapshot_cleanup_unproved", request.Binding,
			request.SnapshotHandle, err, true,
		)
	}
	for _, entry := range owner.Entries {
		if entry.CloneInstance == "" {
			continue
		}
		cloneDir := filepath.Join(home, entry.CloneInstance)
		if _, err := os.Lstat(cloneDir); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return migrationSnapshotError(
				"migration.provider.snapshot_cleanup_unproved", request.Binding,
				request.SnapshotHandle, err, true,
			)
		}
		if err := b.verifyOneMigrationSnapshotCloneState(ctx, home, entry); err != nil {
			return migrationSnapshotError(
				"migration.provider.snapshot_cleanup_unproved", request.Binding,
				request.SnapshotHandle, err, true,
			)
		}
		if err := removeOwnedMigrationClone(home, entry.CloneInstance); err != nil {
			return migrationSnapshotError(
				"migration.provider.snapshot_cleanup_unproved", request.Binding,
				request.SnapshotHandle, err, true,
			)
		}
	}
	if err := removeMigrationCleanupTree(ctx, snapshotDir, files, directories); err != nil {
		return migrationSnapshotError(
			"migration.provider.snapshot_cleanup_failed", request.Binding,
			request.SnapshotHandle, err, true,
		)
	}
	if err := syncMigrationDirectory(snapshotsRoot); err != nil {
		return migrationSnapshotError(
			"migration.provider.snapshot_cleanup_failed", request.Binding,
			request.SnapshotHandle, err, true,
		)
	}
	return nil
}

func (b Backend) buildMigrationSnapshotOwner(
	ctx context.Context,
	request backend.SourceSnapshotRequest,
	inventory backend.SourceInventory,
) (migrationSnapshotOwner, error) {
	instances, err := b.migrationInstanceInventory(ctx)
	if err != nil {
		return migrationSnapshotOwner{}, err
	}
	instanceByName, err := indexMigrationInstances(instances)
	if err != nil {
		return migrationSnapshotOwner{}, err
	}
	diskByRef := make(map[migration.OpaqueID]backend.MigrationSourceDisk, len(inventory.Disks))
	for _, disk := range inventory.Disks {
		diskByRef[disk.DiskRef] = disk
	}
	entryByDisk := make(map[migration.OpaqueID]migrationSnapshotEntry, len(inventory.Disks))
	for _, selection := range request.Selections {
		rootRef := migrationOpaqueRef("root", selection.ProviderInstance)
		root, exists := diskByRef[rootRef]
		if !exists || root.Role != migration.DiskRoleRoot {
			return migrationSnapshotOwner{}, errors.New("source root disk disappeared from inventory")
		}
		componentID := migrationOpaqueRef("component", string(rootRef))
		entryByDisk[rootRef] = migrationSnapshotEntry{
			ComponentID: componentID,
			DiskRef:     rootRef, Role: migration.DiskRoleRoot,
			Format: root.Format, LogicalBytes: root.LogicalBytes,
			SourceObject:  selection.ProviderInstance,
			CloneInstance: migrationSnapshotCloneName(request.Binding, selection.EnvironmentRef),
			RelativePath:  filepath.Join("roots", string(componentID)+".disk"),
		}
		instance, exists := instanceByName[selection.ProviderInstance]
		if !exists {
			return migrationSnapshotOwner{}, errors.New("selected Lima instance disappeared")
		}
		for _, disk := range instance.Config.AdditionalDisks {
			diskRef := migrationOpaqueRef("attached", disk.Name)
			attached, exists := diskByRef[diskRef]
			if !exists || attached.Role != migration.DiskRoleAttached {
				return migrationSnapshotOwner{}, errors.New("attached disk disappeared from inventory")
			}
			componentID := migrationOpaqueRef("component", string(diskRef))
			entryByDisk[diskRef] = migrationSnapshotEntry{
				ComponentID: componentID,
				DiskRef:     diskRef, Role: migration.DiskRoleAttached,
				Format: attached.Format, LogicalBytes: attached.LogicalBytes,
				SourceObject: disk.Name,
				RelativePath: filepath.Join("disks", string(componentID)+".disk"),
			}
		}
	}
	if len(entryByDisk) != len(inventory.Disks) {
		return migrationSnapshotOwner{}, errors.New("snapshot plan is not closed over inspected disks")
	}
	entries := make([]migrationSnapshotEntry, 0, len(entryByDisk))
	for _, entry := range entryByDisk {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].ComponentID < entries[right].ComponentID
	})
	return migrationSnapshotOwner{
		Schema:          migrationSnapshotOwnerSchema,
		SnapshotHandle:  migrationSnapshotHandle(request),
		Binding:         request.Binding,
		InventoryDigest: request.InventoryDigest,
		Selections:      append([]backend.MigrationSourceSelection(nil), request.Selections...),
		DiskRefs:        append([]migration.OpaqueID(nil), request.DiskRefs...),
		Entries:         entries,
	}, nil
}

func prepareMigrationSnapshotOwner(
	home string,
	expected migrationSnapshotOwner,
) (string, migration.Digest, error) {
	root, err := ensurePrivateMigrationDirectory(home, "_hideout-migration")
	if err != nil {
		return "", "", err
	}
	snapshotsRoot, err := ensurePrivateMigrationDirectory(root, "snapshots")
	if err != nil {
		return "", "", err
	}
	snapshotDir := filepath.Join(snapshotsRoot, string(expected.SnapshotHandle))
	created := false
	if err := os.Mkdir(snapshotDir, 0o700); err == nil {
		created = true
		if err := syncMigrationDirectory(snapshotsRoot); err != nil {
			return "", "", err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", "", err
	}
	if _, err := protectedMigrationDirectory(
		home, snapshotDir, snapshotDir,
	); err != nil {
		return "", "", err
	}
	ownerPath := filepath.Join(snapshotDir, "owner.json")
	if created {
		for _, entry := range expected.Entries {
			if entry.CloneInstance == "" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(home, entry.CloneInstance)); !errors.Is(err, os.ErrNotExist) {
				return "", "", errors.New("snapshot clone instance name is already occupied")
			}
		}
		if err := writeMigrationJSONExclusive(ownerPath, expected); err != nil {
			return "", "", err
		}
		if err := syncMigrationDirectory(snapshotDir); err != nil {
			return "", "", err
		}
	}
	var observed migrationSnapshotOwner
	if err := readMigrationJSONStrict(ownerPath, &observed); err != nil {
		return "", "", err
	}
	if err := observed.validate(home, snapshotDir); err != nil || !reflect.DeepEqual(observed, expected) {
		return "", "", errors.New("snapshot owner binding is absent or changed")
	}
	digest, err := migrationJSONDigest(observed)
	if err != nil {
		return "", "", err
	}
	return snapshotDir, digest, nil
}

func (owner migrationSnapshotOwner) validate(home, snapshotDir string) error {
	if owner.Schema != migrationSnapshotOwnerSchema ||
		!migrationValidOpaqueRef(owner.SnapshotHandle) || owner.Binding.Validate() != nil ||
		owner.InventoryDigest.Validate() != nil || len(owner.Selections) == 0 ||
		len(owner.DiskRefs) == 0 || len(owner.Entries) != len(owner.DiskRefs) ||
		!migrationPathWithin(home, snapshotDir) {
		return errors.New("snapshot owner envelope is invalid")
	}
	if err := (backend.SourceSnapshotRequest{
		Binding: owner.Binding, InventoryDigest: owner.InventoryDigest,
		Selections: owner.Selections, DiskRefs: owner.DiskRefs,
	}).Validate(); err != nil {
		return err
	}
	seenDisks := make(map[migration.OpaqueID]struct{}, len(owner.Entries))
	var previous migration.OpaqueID
	for _, entry := range owner.Entries {
		if !migrationValidOpaqueRef(entry.ComponentID) || !migrationValidOpaqueRef(entry.DiskRef) ||
			(entry.Role != migration.DiskRoleRoot && entry.Role != migration.DiskRoleAttached) ||
			entry.Format != "raw" ||
			entry.LogicalBytes == 0 || entry.LogicalBytes > migration.HardMaxLogicalBytes ||
			!migrationProviderObjectName(entry.SourceObject) ||
			(previous != "" && previous >= entry.ComponentID) {
			return errors.New("snapshot owner entry is invalid")
		}
		if _, exists := seenDisks[entry.DiskRef]; exists {
			return errors.New("snapshot owner repeats a disk")
		}
		seenDisks[entry.DiskRef] = struct{}{}
		switch entry.Role {
		case migration.DiskRoleRoot:
			if !migrationProviderObjectName(entry.CloneInstance) ||
				!validMigrationSnapshotRelativePath(entry.RelativePath, "roots") {
				return errors.New("snapshot root entry is invalid")
			}
		case migration.DiskRoleAttached:
			if entry.CloneInstance != "" ||
				!validMigrationSnapshotRelativePath(entry.RelativePath, "disks") {
				return errors.New("snapshot attached entry is invalid")
			}
		}
		previous = entry.ComponentID
	}
	for _, diskRef := range owner.DiskRefs {
		if _, exists := seenDisks[diskRef]; !exists {
			return errors.New("snapshot owner is not closed over disk refs")
		}
	}
	return nil
}

func (b Backend) probeMigrationCloneFile(snapshotDir string) error {
	source := filepath.Join(snapshotDir, ".cow-probe-source")
	destination := filepath.Join(snapshotDir, ".cow-probe-destination")
	for _, path := range []string{source, destination} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("copy-on-write probe path is occupied")
		}
	}
	if err := os.WriteFile(source, bytes.Repeat([]byte{0xa5}, 4096), 0o600); err != nil {
		return err
	}
	defer os.Remove(source)
	if err := b.cloneMigrationFile(source, destination); err != nil {
		return err
	}
	defer os.Remove(destination)
	data, err := os.ReadFile(destination)
	if err != nil || len(data) != 4096 || !bytes.Equal(data, bytes.Repeat([]byte{0xa5}, 4096)) {
		return errors.New("copy-on-write probe content mismatch")
	}
	return nil
}

func (b Backend) cloneMigrationFile(source, destination string) error {
	if b.Migration != nil && b.Migration.FileCloner != nil {
		return b.Migration.FileCloner(source, destination)
	}
	return platformMigrationCloneFile(source, destination)
}

func (b Backend) materializeMigrationSnapshotEntry(
	ctx context.Context,
	home,
	snapshotDir string,
	entry migrationSnapshotEntry,
) error {
	switch entry.Role {
	case migration.DiskRoleRoot:
		cloneDir := filepath.Join(home, entry.CloneInstance)
		destination := filepath.Join(snapshotDir, entry.RelativePath)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil {
			format, logical, _, inspectErr := inspectMigrationDiskFile(destination)
			if inspectErr != nil || format != entry.Format || logical != entry.LogicalBytes {
				return errors.New("existing detached root snapshot does not match its plan")
			}
			if _, cloneErr := os.Lstat(cloneDir); cloneErr == nil {
				if err := b.verifyOneMigrationSnapshotCloneState(ctx, home, entry); err != nil {
					return err
				}
				if err := removeOwnedMigrationClone(home, entry.CloneInstance); err != nil {
					return err
				}
			} else if !errors.Is(cloneErr, os.ErrNotExist) {
				return cloneErr
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Lstat(cloneDir); errors.Is(err, os.ErrNotExist) {
			cloneCtx, cancel := context.WithTimeout(ctx, migrationSnapshotCommandTimeout)
			defer cancel()
			args := []string{
				"clone", "--tty=false", "--mount-none",
				"--set", ".additionalDisks = []",
				entry.SourceObject, entry.CloneInstance,
			}
			if err := b.runner().Run(
				cloneCtx, b.limactl(), args, HostCommandEnv(os.Environ()),
				nil, io.Discard, io.Discard,
			); err != nil {
				return err
			}
			if cloneCtx.Err() != nil {
				return cloneCtx.Err()
			}
		} else if err != nil {
			return err
		}
		if _, err := protectedMigrationDirectory(home, cloneDir, cloneDir); err != nil {
			return err
		}
		if err := b.verifyOneMigrationSnapshotCloneState(ctx, home, entry); err != nil {
			return err
		}
		format, logical, _, err := inspectMigrationDiskFile(filepath.Join(cloneDir, "disk"))
		if err != nil || format != entry.Format || logical != entry.LogicalBytes {
			return errors.New("cloned root disk does not match snapshot plan")
		}
		if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			return errors.New("detached root snapshot destination is occupied")
		}
		if err := os.Rename(filepath.Join(cloneDir, "disk"), destination); err != nil {
			return err
		}
		if err := syncMigrationRegularFile(destination); err != nil {
			return err
		}
		if err := syncMigrationDirectory(filepath.Dir(destination)); err != nil {
			return err
		}
		if err := removeOwnedMigrationClone(home, entry.CloneInstance); err != nil {
			return err
		}
	case migration.DiskRoleAttached:
		destination := filepath.Join(snapshotDir, entry.RelativePath)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
			source := filepath.Join(home, "_disks", entry.SourceObject, "datadisk")
			if err := b.cloneMigrationFile(source, destination); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		format, logical, _, err := inspectMigrationDiskFile(destination)
		if err != nil || format != entry.Format || logical != entry.LogicalBytes {
			return errors.New("cloned attached disk does not match snapshot plan")
		}
		if err := syncMigrationRegularFile(destination); err != nil {
			return err
		}
		if err := syncMigrationDirectory(filepath.Dir(destination)); err != nil {
			return err
		}
	default:
		return errors.New("snapshot entry role is unsupported")
	}
	return nil
}

func (b Backend) verifyOneMigrationSnapshotCloneState(
	ctx context.Context,
	home string,
	entry migrationSnapshotEntry,
) error {
	instances, err := b.migrationInstanceInventory(ctx)
	if err != nil {
		return err
	}
	byName, err := indexMigrationInstances(instances)
	if err != nil {
		return err
	}
	instance, exists := byName[entry.CloneInstance]
	if !exists || migrationLifecycleFromLimaStatus(instance.Status) != backend.MigrationLifecycleStopped ||
		len(instance.Errors) != 0 || len(instance.Config.AdditionalDisks) != 0 ||
		instance.Dir != filepath.Join(home, entry.CloneInstance) {
		return errors.New("snapshot clone is absent, runnable, or retains disk authority")
	}
	return nil
}

func verifyMigrationSnapshotDetached(home string, owner migrationSnapshotOwner) error {
	for _, entry := range owner.Entries {
		if entry.Role != migration.DiskRoleRoot {
			continue
		}
		if _, err := os.Lstat(filepath.Join(home, entry.CloneInstance)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("temporary Lima snapshot clone remains registered")
		}
	}
	return nil
}

func removeOwnedMigrationClone(home, cloneName string) error {
	if !migrationProviderObjectName(cloneName) {
		return errors.New("snapshot clone name is invalid")
	}
	path := filepath.Join(home, cloneName)
	if _, err := protectedMigrationDirectory(home, path, path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func (b Backend) loadCompletedMigrationSnapshot(
	home,
	snapshotDir string,
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
) (backend.SourceSnapshot, error) {
	components := make([]backend.MigrationComponent, 0, len(owner.Entries))
	for _, entry := range owner.Entries {
		path := migrationSnapshotEntryPath(home, snapshotDir, entry)
		format, logical, _, err := inspectMigrationDiskFile(path)
		if err != nil || format != entry.Format || logical != entry.LogicalBytes {
			return backend.SourceSnapshot{}, migrationSnapshotError(
				"migration.provider.snapshot_verification_failed",
				owner.Binding, entry.ComponentID,
				errors.New("snapshot component no longer matches owner metadata"), true,
			)
		}
		components = append(components, backend.MigrationComponent{
			ComponentID:    entry.ComponentID,
			SnapshotHandle: owner.SnapshotHandle,
			DiskRef:        entry.DiskRef,
			Kind:           "disk", LogicalBytes: entry.LogicalBytes,
		})
	}
	identityEvidence, err := loadMigrationSnapshotIdentityEvidence(
		snapshotDir, owner, ownerDigest,
	)
	if err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.source_identity_unproved",
			owner.Binding, owner.SnapshotHandle, err, true,
		)
	}
	snapshot := backend.SourceSnapshot{
		Binding: owner.Binding, SnapshotHandle: owner.SnapshotHandle,
		Components: components, Identities: identityEvidence.Identities,
		Independent: true, SourceClaimsRequired: false,
	}
	if err := snapshot.Validate(); err != nil {
		return backend.SourceSnapshot{}, migrationSnapshotError(
			"migration.provider.snapshot_verification_failed",
			owner.Binding, owner.SnapshotHandle, err, true,
		)
	}
	return snapshot, nil
}

func migrationSnapshotEntryPath(
	home,
	snapshotDir string,
	entry migrationSnapshotEntry,
) string {
	_ = home
	return filepath.Join(snapshotDir, entry.RelativePath)
}

func migrationSnapshotHandle(request backend.SourceSnapshotRequest) migration.OpaqueID {
	return migrationOpaqueRef(
		"snapshot",
		string(request.Binding.OperationID)+"\x00"+
			string(request.Binding.EffectID)+"\x00"+string(request.InventoryDigest),
	)
}

func migrationSnapshotCloneName(
	binding backend.MigrationEffectBinding,
	environmentRef migration.OpaqueID,
) string {
	digest := sha256.Sum256([]byte(
		"hideout.lima-migration-snapshot-clone/v1\x00" +
			string(binding.OperationID) + "\x00" + string(binding.EffectID) + "\x00" +
			string(environmentRef),
	))
	return "hideout-mig-" + hex.EncodeToString(digest[:12])
}

func validMigrationSnapshotRelativePath(value, directory string) bool {
	if value == "" || filepath.IsAbs(value) || filepath.Clean(value) != value ||
		strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	parts := strings.Split(value, string(filepath.Separator))
	return len(parts) == 2 && parts[0] == directory && parts[1] != "" &&
		parts[1] != "." && parts[1] != ".."
}

func ensurePrivateMigrationDirectory(parent, name string) (string, error) {
	if !migrationProviderObjectName(strings.TrimPrefix(name, "_")) {
		return "", errors.New("migration directory name is invalid")
	}
	path := filepath.Join(parent, name)
	created := false
	if err := os.Mkdir(path, 0o700); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("migration directory is not private")
	}
	if created {
		if err := syncMigrationDirectory(parent); err != nil {
			return "", err
		}
	}
	return path, nil
}

func writeMigrationJSONExclusive(path string, value any) (retErr error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > migrationSnapshotMetadataLimit {
		return errors.New("migration metadata is invalid or oversized")
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

func syncMigrationRegularFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr)
}

func readMigrationJSONStrict(path string, destination any) error {
	data, _, err := readStableMigrationFile(path, migrationSnapshotMetadataLimit)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("migration metadata has trailing content")
	}
	return nil
}

func loadMigrationSnapshotComplete(snapshotDir string) (migrationSnapshotComplete, error) {
	completePath := filepath.Join(snapshotDir, "complete.json")
	if _, err := os.Lstat(completePath); err != nil {
		return migrationSnapshotComplete{}, err
	}
	var complete migrationSnapshotComplete
	err := readMigrationJSONStrict(completePath, &complete)
	if err != nil {
		return migrationSnapshotComplete{}, err
	}
	if complete.Schema != migrationSnapshotCompleteSchema ||
		!migrationValidOpaqueRef(complete.SnapshotHandle) || complete.OwnerDigest.Validate() != nil {
		return migrationSnapshotComplete{}, errors.New("snapshot completion metadata is invalid")
	}
	return complete, nil
}

func migrationJSONDigest(value any) (migration.Digest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return migration.Digest(fmt.Sprintf("sha256:%x", digest[:])), nil
}

func migrationValidOpaqueRef(value migration.OpaqueID) bool {
	_, err := migration.ParseOpaqueID(string(value))
	return err == nil
}

func migrationSnapshotError(
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
