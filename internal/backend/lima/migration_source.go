package lima

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"gopkg.in/yaml.v3"
)

const (
	migrationInventoryOutputLimit = 8 << 20
	migrationConfigBytesLimit     = 4 << 20
	migrationInventoryItemLimit   = 1024
	migrationInspectionTimeout    = 15 * time.Second
	migrationSourceConfigRevision = 1
)

var (
	migrationLimaInstanceVersion = regexp.MustCompile(`^2\.(?:1|2)\.[0-9]+$`)
	migrationLimaObjectName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	migrationLimaFSType          = regexp.MustCompile(`^[a-z][a-z0-9.-]{1,63}$`)
)

type migrationLimaDiskConfig struct {
	Name   string   `json:"name"`
	Format *bool    `json:"format,omitempty"`
	FSType *string  `json:"fsType,omitempty"`
	FSArgs []string `json:"fsArgs,omitempty"`
}

type migrationLimaInstanceInventory struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Dir         string `json:"dir"`
	VMType      string `json:"vmType"`
	Arch        string `json:"arch"`
	LimaVersion string `json:"limaVersion"`
	Errors      []any  `json:"errors,omitempty"`
	Config      struct {
		VMType          string                    `json:"vmType"`
		Arch            string                    `json:"arch"`
		AdditionalDisks []migrationLimaDiskConfig `json:"additionalDisks,omitempty"`
	} `json:"config"`
}

type migrationLimaDiskInventory struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Format      string `json:"format"`
	Dir         string `json:"dir"`
	Instance    string `json:"instance"`
	InstanceDir string `json:"instanceDir"`
}

type migrationInspectedInstance struct {
	selection     backend.MigrationSourceSelection
	providerRef   migration.OpaqueID
	configDigest  migration.Digest
	directory     string
	rootPath      string
	rootFormat    string
	rootLogical   uint64
	rootAllocated uint64
	disks         []migrationLimaDiskConfig
}

// InspectMigrationSource is intentionally read-only. It asks Lima for its live
// inventory, then independently binds that inventory to protected on-disk
// directories and the proved v2 consolidated-root layout. It never invokes a
// lifecycle, clone, snapshot, or disk mutation command.
func (b Backend) InspectMigrationSource(
	ctx context.Context,
	request backend.SourceInspectionRequest,
) (backend.SourceInventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.SourceInventory{}, err
	}
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil {
		return backend.SourceInventory{}, err
	}
	if capability.Revision != request.Binding.CapabilityRevision || !capability.FullExport {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.capability_stale",
			request.Binding,
			"",
			errors.New("Lima migration capability is unavailable or changed"),
		)
	}
	home, err := b.migrationLimaHome()
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.lima_home_unsafe", request.Binding, "", err,
		)
	}

	instances, err := b.migrationInstanceInventory(ctx)
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.source_inventory_failed", request.Binding, "", err,
		)
	}
	instanceByName, err := indexMigrationInstances(instances)
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.source_inventory_ambiguous", request.Binding, "", err,
		)
	}
	diskInventory, err := b.migrationDiskInventory(ctx)
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.disk_inventory_failed", request.Binding, "", err,
		)
	}
	diskByName, err := indexMigrationDisks(diskInventory)
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.disk_inventory_ambiguous", request.Binding, "", err,
		)
	}

	selectedNames := make(map[string]backend.MigrationSourceSelection, len(request.Selections))
	for _, selection := range request.Selections {
		selectedNames[selection.ProviderInstance] = selection
	}

	inventory := backend.SourceInventory{
		Binding:  request.Binding,
		Provider: "lima",
		ExcludedClasses: []string{
			"activity-history",
			"audit-history",
			"caches",
			"command-history",
			"host-runtime-identity",
			"host-workspace-content",
			"hostfs-content",
			"logs",
			"memory-state",
			"process-state",
			"runtime-state",
			"unselected-secret-values",
		},
		SelectionClosed: true,
	}
	inspected := make([]migrationInspectedInstance, 0, len(request.Selections))
	_, _, expectedGuestArch := b.migrationArchitectures()
	var aggregateLogical uint64
	for _, selection := range request.Selections {
		info, exists := instanceByName[selection.ProviderInstance]
		if !exists {
			return backend.SourceInventory{}, migrationSourceError(
				"migration.provider.source_missing",
				request.Binding,
				migrationOpaqueRef("instance", selection.ProviderInstance),
				errors.New("selected Lima instance is absent"),
			)
		}
		item, inspectErr := inspectMigrationInstance(
			home, expectedGuestArch, selection, info,
		)
		if inspectErr != nil {
			return backend.SourceInventory{}, migrationSourceError(
				"migration.provider.source_layout_unproved",
				request.Binding,
				migrationOpaqueRef("instance", selection.ProviderInstance),
				inspectErr,
			)
		}
		inspected = append(inspected, item)
		lifecycle := migrationLifecycleFromLimaStatus(info.Status)
		if lifecycle != backend.MigrationLifecycleStopped {
			appendMigrationSourceBlocker(&inventory, backend.MigrationProviderBlocker{
				Code:        "migration.provider.source_not_stopped",
				Summary:     "A selected Lima environment is not proved stopped.",
				Remediation: "Stop the selected environment and retry the read-only migration preview.",
			})
		}
		inventory.Instances = append(inventory.Instances, backend.MigrationSourceInstance{
			EnvironmentRef:        selection.EnvironmentRef,
			ProviderRef:           item.providerRef,
			Lifecycle:             lifecycle,
			ConfigurationRevision: migrationSourceConfigRevision,
			ConfigurationDigest:   item.configDigest,
			RootDiskRef:           migrationOpaqueRef("root", selection.ProviderInstance),
		})
		if item.rootLogical > migration.HardMaxLogicalBytes-aggregateLogical {
			return backend.SourceInventory{}, migrationSourceError(
				"migration.provider.source_limit_exceeded", request.Binding, item.providerRef,
				errors.New("selected root disks exceed the migration logical-byte envelope"),
			)
		}
		aggregateLogical += item.rootLogical
		rootRef := migrationOpaqueRef("root", selection.ProviderInstance)
		inventory.Disks = append(inventory.Disks, backend.MigrationSourceDisk{
			DiskRef:            rootRef,
			ProviderRef:        migrationOpaqueRef("root-provider", selection.ProviderInstance),
			Role:               migration.DiskRoleRoot,
			Format:             item.rootFormat,
			LogicalBytes:       item.rootLogical,
			AllocatedBytesHint: item.rootAllocated,
			Consumers:          []migration.OpaqueID{selection.EnvironmentRef},
		})
		inventory.Attachments = append(inventory.Attachments, backend.MigrationSourceAttachment{
			EnvironmentRef: selection.EnvironmentRef,
			DiskRef:        rootRef,
			Attachment:     migration.DiskRoleRoot,
			GuestPath:      "/",
		})
	}

	consumers, err := migrationDiskConsumers(instances)
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.source_graph_unproved", request.Binding, "", err,
		)
	}
	selectedDiskNames := make(map[string][]migration.OpaqueID)
	attachedFSTypes := make(map[string]string)
	filesystemByDisk := make(map[string]string)
	for _, item := range inspected {
		for _, attached := range item.disks {
			fsType := "ext4"
			if attached.FSType != nil {
				fsType = *attached.FSType
			}
			if !migrationLimaFSType.MatchString(fsType) || fsType == "swap" {
				return backend.SourceInventory{}, migrationSourceError(
					"migration.provider.attached_disk_filesystem_unsupported",
					request.Binding,
					migrationOpaqueRef("attached", attached.Name),
					errors.New("attached disk filesystem cannot be preserved safely"),
				)
			}
			if existing, exists := filesystemByDisk[attached.Name]; exists && existing != fsType {
				return backend.SourceInventory{}, migrationSourceError(
					"migration.provider.attached_disk_filesystem_mismatch",
					request.Binding,
					migrationOpaqueRef("attached", attached.Name),
					errors.New("shared attached disk consumers declare different filesystems"),
				)
			}
			filesystemByDisk[attached.Name] = fsType
			selectedDiskNames[attached.Name] = append(
				selectedDiskNames[attached.Name], item.selection.EnvironmentRef,
			)
			attachedFSTypes[string(item.selection.EnvironmentRef)+"\x00"+attached.Name] = fsType
			for _, consumerName := range consumers[attached.Name] {
				if _, selected := selectedNames[consumerName]; !selected {
					inventory.SelectionClosed = false
				}
			}
		}
	}
	if !inventory.SelectionClosed {
		appendMigrationSourceBlocker(&inventory, backend.MigrationProviderBlocker{
			Code:        "migration.provider.shared_disk_selection_open",
			Summary:     "A selected attached disk is also configured by an unselected Lima environment.",
			Remediation: "Select every environment that references the shared disk, or use config-only migration.",
		})
	}

	diskNames := make([]string, 0, len(selectedDiskNames))
	for name := range selectedDiskNames {
		diskNames = append(diskNames, name)
	}
	sort.Strings(diskNames)
	for _, name := range diskNames {
		disk, exists := diskByName[name]
		if !exists {
			return backend.SourceInventory{}, migrationSourceError(
				"migration.provider.attached_disk_missing",
				request.Binding,
				migrationOpaqueRef("attached", name),
				errors.New("configured Lima disk is absent"),
			)
		}
		format, logical, allocated, diskErr := inspectMigrationAttachedDisk(home, name, disk)
		if diskErr != nil {
			return backend.SourceInventory{}, migrationSourceError(
				"migration.provider.attached_disk_layout_unproved",
				request.Binding,
				migrationOpaqueRef("attached", name),
				diskErr,
			)
		}
		if disk.Instance != "" || disk.InstanceDir != "" {
			appendMigrationSourceBlocker(&inventory, backend.MigrationProviderBlocker{
				Code:        "migration.provider.attached_disk_locked",
				Summary:     "A selected attached disk still has a live Lima ownership lock.",
				Remediation: "Stop every disk consumer and resolve stale Lima disk locks before retrying.",
			})
		}
		if logical > migration.HardMaxLogicalBytes-aggregateLogical {
			return backend.SourceInventory{}, migrationSourceError(
				"migration.provider.source_limit_exceeded",
				request.Binding,
				migrationOpaqueRef("attached", name),
				errors.New("selected disks exceed the migration logical-byte envelope"),
			)
		}
		aggregateLogical += logical
		refs := append([]migration.OpaqueID(nil), selectedDiskNames[name]...)
		sort.Slice(refs, func(left, right int) bool { return refs[left] < refs[right] })
		diskRef := migrationOpaqueRef("attached", name)
		inventory.Disks = append(inventory.Disks, backend.MigrationSourceDisk{
			DiskRef:            diskRef,
			ProviderRef:        migrationOpaqueRef("attached-provider", name),
			Role:               migration.DiskRoleAttached,
			Format:             format,
			LogicalBytes:       logical,
			AllocatedBytesHint: allocated,
			Consumers:          refs,
		})
		for _, environmentRef := range refs {
			inventory.Attachments = append(inventory.Attachments, backend.MigrationSourceAttachment{
				EnvironmentRef: environmentRef,
				DiskRef:        diskRef,
				Attachment:     migration.DiskRoleAttached,
				GuestPath:      "/mnt/lima-" + name,
				FSType:         attachedFSTypes[string(environmentRef)+"\x00"+name],
			})
		}
	}

	sortMigrationSourceInventory(&inventory)
	inventory.Capturable = inventory.SelectionClosed && len(inventory.Blockers) == 0
	inventory.InventoryDigest, err = backend.SourceInventoryDigest(inventory)
	if err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.source_inventory_failed", request.Binding, "", err,
		)
	}
	if err := inventory.Validate(); err != nil {
		return backend.SourceInventory{}, migrationSourceError(
			"migration.provider.source_inventory_failed", request.Binding, "", err,
		)
	}
	return inventory, nil
}

func (b Backend) migrationInstanceInventory(
	ctx context.Context,
) ([]migrationLimaInstanceInventory, error) {
	return runMigrationJSONInventory[migrationLimaInstanceInventory](
		ctx, b, []string{"list", "--format", "json", "--all-fields"},
	)
}

func (b Backend) migrationDiskInventory(
	ctx context.Context,
) ([]migrationLimaDiskInventory, error) {
	return runMigrationJSONInventory[migrationLimaDiskInventory](
		ctx, b, []string{"disk", "list", "--json"},
	)
}

func runMigrationJSONInventory[T any](
	ctx context.Context,
	b Backend,
	args []string,
) ([]T, error) {
	probeCtx, cancel := context.WithTimeout(ctx, migrationInspectionTimeout)
	defer cancel()
	capture := &boundedRuntimeCapture{limit: migrationInventoryOutputLimit}
	if err := b.runner().Run(
		probeCtx,
		b.limactl(),
		args,
		HostCommandEnv(os.Environ()),
		nil,
		capture,
		io.Discard,
	); err != nil {
		return nil, err
	}
	if probeCtx.Err() != nil {
		return nil, probeCtx.Err()
	}
	if capture.truncated {
		return nil, errors.New("Lima migration inventory exceeded the output limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(capture.buf.Bytes()))
	items := make([]T, 0)
	for {
		var item T
		if err := decoder.Decode(&item); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if len(items) == migrationInventoryItemLimit {
			return nil, errors.New("Lima migration inventory exceeded the item limit")
		}
		items = append(items, item)
	}
	return items, nil
}

func indexMigrationInstances(
	instances []migrationLimaInstanceInventory,
) (map[string]migrationLimaInstanceInventory, error) {
	out := make(map[string]migrationLimaInstanceInventory, len(instances))
	for _, instance := range instances {
		if !migrationProviderObjectName(instance.Name) {
			return nil, errors.New("Lima inventory contains an invalid instance name")
		}
		if _, exists := out[instance.Name]; exists {
			return nil, errors.New("Lima inventory contains a duplicate instance")
		}
		out[instance.Name] = instance
	}
	return out, nil
}

func indexMigrationDisks(
	disks []migrationLimaDiskInventory,
) (map[string]migrationLimaDiskInventory, error) {
	out := make(map[string]migrationLimaDiskInventory, len(disks))
	for _, disk := range disks {
		if !migrationProviderObjectName(disk.Name) {
			return nil, errors.New("Lima inventory contains an invalid disk name")
		}
		if _, exists := out[disk.Name]; exists {
			return nil, errors.New("Lima inventory contains a duplicate disk")
		}
		out[disk.Name] = disk
	}
	return out, nil
}

func inspectMigrationInstance(
	home string,
	expectedGuestArch string,
	selection backend.MigrationSourceSelection,
	info migrationLimaInstanceInventory,
) (migrationInspectedInstance, error) {
	if len(info.Errors) != 0 || !migrationLimaInstanceVersion.MatchString(info.LimaVersion) ||
		info.VMType != "vz" || info.Config.VMType != "vz" ||
		info.Arch == "" || info.Config.Arch == "" || info.Arch != info.Config.Arch ||
		normalizeLimaHostArch(info.Arch) != expectedGuestArch {
		return migrationInspectedInstance{}, errors.New("Lima instance facts are incomplete or unsupported")
	}
	directory, err := protectedMigrationDirectory(
		home,
		filepath.Join(home, selection.ProviderInstance),
		info.Dir,
	)
	if err != nil {
		return migrationInspectedInstance{}, err
	}
	versionBytes, _, err := readStableMigrationFile(
		filepath.Join(directory, "lima-version"), 128,
	)
	if err != nil || strings.TrimSpace(string(versionBytes)) != info.LimaVersion {
		return migrationInspectedInstance{}, errors.New("Lima instance version file does not match inventory")
	}
	configBytes, configInfo, err := readStableMigrationFile(
		filepath.Join(directory, "lima.yaml"), migrationConfigBytesLimit,
	)
	if err != nil {
		return migrationInspectedInstance{}, err
	}
	disks, err := migrationAdditionalDisksFromYAML(configBytes)
	if err != nil || !equalMigrationDiskConfigs(disks, info.Config.AdditionalDisks) {
		return migrationInspectedInstance{}, errors.New("Lima disk configuration does not match its resolved inventory")
	}
	configHash := sha256.Sum256(configBytes)
	configDigest := migration.Digest("sha256:" + hex.EncodeToString(configHash[:]))
	if configInfo.Size() != int64(len(configBytes)) {
		return migrationInspectedInstance{}, errors.New("Lima configuration changed during inspection")
	}
	rootPath := filepath.Join(directory, "disk")
	format, logical, allocated, err := inspectMigrationDiskFile(rootPath)
	if err != nil || format != "raw" {
		if err == nil {
			err = errors.New("only raw VZ root disks are proved for full migration")
		}
		return migrationInspectedInstance{}, err
	}
	for _, legacy := range []string{"basedisk", "diffdisk"} {
		if _, legacyErr := os.Lstat(filepath.Join(directory, legacy)); legacyErr == nil {
			return migrationInspectedInstance{}, errors.New("legacy Lima root-disk layout is unsupported")
		} else if !errors.Is(legacyErr, os.ErrNotExist) {
			return migrationInspectedInstance{}, legacyErr
		}
	}
	return migrationInspectedInstance{
		selection:     selection,
		providerRef:   migrationOpaqueRef("instance", selection.ProviderInstance),
		configDigest:  configDigest,
		directory:     directory,
		rootPath:      rootPath,
		rootFormat:    format,
		rootLogical:   logical,
		rootAllocated: allocated,
		disks:         disks,
	}, nil
}

func inspectMigrationAttachedDisk(
	home,
	name string,
	disk migrationLimaDiskInventory,
) (string, uint64, uint64, error) {
	directory, err := protectedMigrationDirectory(
		home,
		filepath.Join(home, "_disks", name),
		disk.Dir,
	)
	if err != nil {
		return "", 0, 0, err
	}
	format, logical, allocated, err := inspectMigrationDiskFile(
		filepath.Join(directory, "datadisk"),
	)
	if err != nil || format != "raw" {
		if err == nil {
			err = errors.New("only raw Lima attached disks are proved for full migration")
		}
		return "", 0, 0, err
	}
	if disk.Size <= 0 || uint64(disk.Size) != logical || disk.Format != format {
		return "", 0, 0, errors.New("Lima disk file does not match disk inventory")
	}
	return format, logical, allocated, nil
}

func protectedMigrationDirectory(home, expected, reported string) (string, error) {
	if reported == "" || !filepath.IsAbs(reported) || filepath.Clean(reported) != reported ||
		filepath.Clean(expected) != expected || reported != expected {
		return "", errors.New("Lima object directory does not match the expected storage root")
	}
	physical, err := filepath.EvalSymlinks(reported)
	if err != nil || physical != reported || !migrationPathWithin(home, physical) {
		return "", errors.New("Lima object directory is aliased or outside the storage root")
	}
	info, err := os.Lstat(physical)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("Lima object directory is not protected")
	}
	return physical, nil
}

func migrationPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readStableMigrationFile(path string, limit int) ([]byte, os.FileInfo, error) {
	if limit <= 0 {
		return nil, nil, errors.New("migration file limit is invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() < 0 || before.Size() > int64(limit) {
		return nil, nil, errors.New("migration source file is absent, aliased, special, or oversized")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, nil, errors.New("migration source file changed before inspection")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, nil, errors.New("migration source file could not be read within its bound")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) {
		return nil, nil, errors.New("migration source file changed during inspection")
	}
	return data, after, nil
}

func inspectMigrationDiskFile(path string) (string, uint64, uint64, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() <= 0 {
		return "", 0, 0, errors.New("migration disk is absent, aliased, empty, or special")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", 0, 0, errors.New("migration disk changed before inspection")
	}
	header := make([]byte, 32)
	if _, err := io.ReadFull(file, header); err != nil {
		return "", 0, 0, errors.New("migration disk header is unreadable")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) {
		return "", 0, 0, errors.New("migration disk changed during inspection")
	}
	logical := uint64(opened.Size())
	format := "raw"
	if bytes.Equal(header[:4], []byte{'Q', 'F', 'I', 0xfb}) {
		version := binary.BigEndian.Uint32(header[4:8])
		logical = binary.BigEndian.Uint64(header[24:32])
		if (version != 2 && version != 3) || logical == 0 {
			return "", 0, 0, errors.New("qcow2 disk version or virtual size is unsupported")
		}
		format = "qcow2"
	}
	if logical == 0 || logical > migration.HardMaxLogicalBytes {
		return "", 0, 0, errors.New("migration disk logical size is outside the supported envelope")
	}
	// The portable v1 inventory treats allocated bytes as an upper-bound hint.
	// Exact sparse allocation is re-probed by the snapshot extent reader.
	return format, logical, uint64(opened.Size()), nil
}

func migrationAdditionalDisksFromYAML(data []byte) ([]migrationLimaDiskConfig, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("Lima configuration contains multiple YAML documents")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("Lima configuration root is not a mapping")
	}
	root := document.Content[0]
	seen := make(map[string]struct{}, len(root.Content)/2)
	var additional *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, errors.New("Lima configuration contains a non-string top-level key")
		}
		if _, exists := seen[key.Value]; exists {
			return nil, errors.New("Lima configuration contains a duplicate top-level key")
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "base":
			if !migrationYAMLNodeEmpty(value) {
				return nil, errors.New("layered Lima base configuration is unsupported for full migration")
			}
		case "additionalDisks":
			additional = value
		}
	}
	if additional == nil || migrationYAMLNodeEmpty(additional) {
		return nil, nil
	}
	if additional.Kind != yaml.SequenceNode {
		return nil, errors.New("Lima additionalDisks is not a sequence")
	}
	out := make([]migrationLimaDiskConfig, 0, len(additional.Content))
	seenNames := make(map[string]struct{}, len(additional.Content))
	for _, node := range additional.Content {
		disk, err := migrationDiskConfigFromYAML(node)
		if err != nil {
			return nil, err
		}
		if _, exists := seenNames[disk.Name]; exists {
			return nil, errors.New("Lima additionalDisks contains a duplicate disk")
		}
		seenNames[disk.Name] = struct{}{}
		out = append(out, disk)
	}
	return out, nil
}

func migrationDiskConfigFromYAML(node *yaml.Node) (migrationLimaDiskConfig, error) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		if !migrationProviderObjectName(node.Value) {
			return migrationLimaDiskConfig{}, errors.New("Lima disk name is invalid")
		}
		return migrationLimaDiskConfig{Name: node.Value}, nil
	}
	if node.Kind != yaml.MappingNode {
		return migrationLimaDiskConfig{}, errors.New("Lima disk entry is neither a name nor a mapping")
	}
	var disk migrationLimaDiskConfig
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return migrationLimaDiskConfig{}, errors.New("Lima disk entry has a non-string key")
		}
		if _, exists := seen[key.Value]; exists {
			return migrationLimaDiskConfig{}, errors.New("Lima disk entry has a duplicate key")
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "name":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return migrationLimaDiskConfig{}, errors.New("Lima disk name is not a string")
			}
			disk.Name = value.Value
		case "format":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" ||
				(value.Value != "true" && value.Value != "false") {
				return migrationLimaDiskConfig{}, errors.New("Lima disk format flag is not a boolean")
			}
			flag := value.Value == "true"
			disk.Format = &flag
		case "fsType":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" ||
				!migrationLimaFSType.MatchString(value.Value) {
				return migrationLimaDiskConfig{}, errors.New("Lima disk fsType is invalid")
			}
			fsType := value.Value
			disk.FSType = &fsType
		case "fsArgs":
			if value.Kind != yaml.SequenceNode || len(value.Content) > 64 {
				return migrationLimaDiskConfig{}, errors.New("Lima disk fsArgs is invalid")
			}
			for _, argument := range value.Content {
				if argument.Kind != yaml.ScalarNode || argument.Tag != "!!str" ||
					len(argument.Value) > 1024 || strings.ContainsAny(argument.Value, "\x00\r\n") {
					return migrationLimaDiskConfig{}, errors.New("Lima disk fsArg is invalid")
				}
				disk.FSArgs = append(disk.FSArgs, argument.Value)
			}
		default:
			return migrationLimaDiskConfig{}, errors.New("Lima disk entry contains an unsupported field")
		}
	}
	if !migrationProviderObjectName(disk.Name) {
		return migrationLimaDiskConfig{}, errors.New("Lima disk name is invalid")
	}
	return disk, nil
}

func migrationYAMLNodeEmpty(node *yaml.Node) bool {
	return node == nil || (node.Kind == yaml.ScalarNode && node.Tag == "!!null") ||
		(node.Kind == yaml.SequenceNode && len(node.Content) == 0)
}

func equalMigrationDiskConfigs(left, right []migrationLimaDiskConfig) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name ||
			!equalOptionalBool(left[index].Format, right[index].Format) ||
			!equalOptionalString(left[index].FSType, right[index].FSType) ||
			!equalStrings(left[index].FSArgs, right[index].FSArgs) {
			return false
		}
	}
	return true
}

func equalOptionalBool(left, right *bool) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}

func equalOptionalString(left, right *string) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func migrationDiskConsumers(
	instances []migrationLimaInstanceInventory,
) (map[string][]string, error) {
	consumers := make(map[string][]string)
	for _, instance := range instances {
		seen := make(map[string]struct{}, len(instance.Config.AdditionalDisks))
		for _, disk := range instance.Config.AdditionalDisks {
			if !migrationProviderObjectName(disk.Name) {
				return nil, errors.New("Lima instance inventory contains an invalid disk name")
			}
			if _, exists := seen[disk.Name]; exists {
				return nil, errors.New("Lima instance inventory contains a duplicate disk attachment")
			}
			seen[disk.Name] = struct{}{}
			consumers[disk.Name] = append(consumers[disk.Name], instance.Name)
		}
	}
	for name := range consumers {
		sort.Strings(consumers[name])
	}
	return consumers, nil
}

func migrationProviderObjectName(value string) bool {
	return value != "" && len(value) <= 128 && value[0] != '_' &&
		migrationLimaObjectName.MatchString(value)
}

func migrationLifecycleFromLimaStatus(status string) backend.MigrationLifecycleState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "stopped":
		return backend.MigrationLifecycleStopped
	case "running":
		return backend.MigrationLifecycleRunning
	case "starting", "stopping":
		return backend.MigrationLifecycleTransitioning
	default:
		return backend.MigrationLifecycleUnknown
	}
}

func migrationOpaqueRef(class, value string) migration.OpaqueID {
	digest := sha256.Sum256([]byte("hideout.lima-migration-object/v1\x00" + class + "\x00" + value))
	return migration.OpaqueID("lima_" + hex.EncodeToString(digest[:]))
}

func sortMigrationSourceInventory(inventory *backend.SourceInventory) {
	sort.Slice(inventory.Instances, func(left, right int) bool {
		return inventory.Instances[left].EnvironmentRef < inventory.Instances[right].EnvironmentRef
	})
	sort.Slice(inventory.Disks, func(left, right int) bool {
		return inventory.Disks[left].DiskRef < inventory.Disks[right].DiskRef
	})
	sort.Slice(inventory.Attachments, func(left, right int) bool {
		if inventory.Attachments[left].EnvironmentRef == inventory.Attachments[right].EnvironmentRef {
			return inventory.Attachments[left].DiskRef < inventory.Attachments[right].DiskRef
		}
		return inventory.Attachments[left].EnvironmentRef < inventory.Attachments[right].EnvironmentRef
	})
	sort.Slice(inventory.Blockers, func(left, right int) bool {
		if inventory.Blockers[left].Code == inventory.Blockers[right].Code {
			return inventory.Blockers[left].Summary < inventory.Blockers[right].Summary
		}
		return inventory.Blockers[left].Code < inventory.Blockers[right].Code
	})
}

func appendMigrationSourceBlocker(
	inventory *backend.SourceInventory,
	blocker backend.MigrationProviderBlocker,
) {
	for _, existing := range inventory.Blockers {
		if existing.Code == blocker.Code && existing.Summary == blocker.Summary {
			return
		}
	}
	inventory.Blockers = append(inventory.Blockers, blocker)
}

func migrationSourceError(
	code string,
	binding backend.MigrationEffectBinding,
	ref migration.OpaqueID,
	cause error,
) error {
	return &backend.MigrationProviderError{
		Code:      code,
		Binding:   binding,
		OpaqueRef: string(ref),
		Cause:     cause,
	}
}
