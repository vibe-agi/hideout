package lima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"gopkg.in/yaml.v3"
)

type importedLimaRuntimeState struct {
	configPath string
	normalized migrationNormalizedStageConfig
}

// reconcileImportedRuntimeMounts admits only destination-generated profile,
// runtime, and explicitly reviewed workspace mounts into an activated import.
// The migration provider intentionally publishes mounts: []; ordinary Lima
// start-by-name would otherwise ignore the current run's generated config.
func (b Backend) reconcileImportedRuntimeMounts(
	ctx context.Context,
	runner CommandRunner,
	hostEnv []string,
	session *backend.Session,
) error {
	state, err := loadImportedLimaRuntimeState(hostEnv, session)
	if err != nil || state == nil {
		return err
	}
	expected, err := expectedImportedRuntimeMounts(session)
	if err != nil {
		return err
	}
	observed, err := readImportedLimaRuntimeConfig(state.configPath)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(observed.Mounts, expected) {
		return nil
	}
	encoded, err := json.Marshal(expected)
	if err != nil {
		return fmt.Errorf("encode imported Lima runtime mounts: %w", err)
	}
	setExpression := ".mounts = " + string(encoded)
	if err := runner.Run(
		ctx,
		b.limactl(),
		[]string{"edit", "--tty=false", "--set", setExpression, session.InstanceName},
		hostEnv,
		nil,
		b.controlStdout(),
		b.controlStderr(),
	); err != nil {
		return fmt.Errorf("edit imported Lima runtime mounts: %w", err)
	}
	updated, err := readImportedLimaRuntimeConfig(state.configPath)
	if err != nil {
		return fmt.Errorf("verify imported Lima runtime mounts: %w", err)
	}
	if !reflect.DeepEqual(updated.Mounts, expected) {
		return errors.New("imported Lima runtime mount edit did not commit the exact destination mount set")
	}
	return nil
}

func expectedImportedRuntimeMounts(session *backend.Session) ([]mount, error) {
	if session == nil || session.EnvironmentID == "" ||
		!filepath.IsAbs(session.IdentityRoot) || filepath.Clean(session.IdentityRoot) != session.IdentityRoot ||
		!filepath.IsAbs(session.RuntimeRoot) || filepath.Clean(session.RuntimeRoot) != session.RuntimeRoot {
		return nil, errors.New("imported Lima runtime requires clean destination identity and runtime roots")
	}
	expected := identityStateMounts(session.IdentityRoot)
	switch session.Workspace.Transport {
	case backend.WorkspaceTransportPortal:
	case backend.WorkspaceTransportStatic:
		if !filepath.IsAbs(session.Workspace.HostRoot) ||
			filepath.Clean(session.Workspace.HostRoot) != session.Workspace.HostRoot ||
			!filepath.IsAbs(session.Workspace.GuestRoot) ||
			filepath.Clean(session.Workspace.GuestRoot) != session.Workspace.GuestRoot {
			return nil, errors.New("imported Lima runtime workspace mapping is invalid")
		}
		expected = append([]mount{{
			Location: session.Workspace.HostRoot, MountPoint: session.Workspace.GuestRoot, Writable: true,
		}}, expected...)
	default:
		return nil, errors.New("imported Lima runtime workspace transport is invalid")
	}
	expected = append(expected, mount{
		Location: session.RuntimeRoot, MountPoint: GuestRuntimeDir, Writable: true,
	})

	generated, err := readImportedLimaRuntimeConfig(session.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read generated destination Lima config: %w", err)
	}
	if !reflect.DeepEqual(generated.Mounts, expected) {
		return nil, errors.New("generated destination Lima config contains unexpected imported runtime mounts")
	}
	return expected, nil
}

func loadImportedLimaRuntimeState(
	hostEnv []string,
	session *backend.Session,
) (*importedLimaRuntimeState, error) {
	if session == nil || strings.TrimSpace(session.InstanceName) == "" {
		return nil, errors.New("imported Lima runtime session is incomplete")
	}
	home, err := resolveLimaHome(hostEnv)
	if err != nil {
		return nil, err
	}
	instanceDir := filepath.Join(home, session.InstanceName)
	normalizedPath := filepath.Join(instanceDir, "normalized.json")
	if _, err := os.Lstat(normalizedPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect imported Lima marker: %w", err)
	}
	if _, err := protectedMigrationDirectory(home, instanceDir, instanceDir); err != nil {
		return nil, fmt.Errorf("protect imported Lima instance: %w", err)
	}
	var normalized migrationNormalizedStageConfig
	if err := readMigrationJSONStrict(normalizedPath, &normalized); err != nil {
		return nil, fmt.Errorf("read imported Lima marker: %w", err)
	}
	if err := validateImportedLimaRuntimeMarker(normalized, session); err != nil {
		return nil, err
	}
	configPath := filepath.Join(instanceDir, "lima.yaml")
	config, err := readImportedLimaRuntimeConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("read imported Lima disk bindings: %w", err)
	}
	if err := validateMigrationStageDiskBindings(
		config.AdditionalDisks, normalized.AttachedDiskMounts,
	); err != nil {
		return nil, err
	}
	rootPath := filepath.Join(instanceDir, "disk")
	if _, err := protectedMigrationRegularFile(
		home, rootPath, normalized.RootDiskLogicalBytes,
	); err != nil {
		return nil, fmt.Errorf("protect imported Lima root disk: %w", err)
	}
	return &importedLimaRuntimeState{
		configPath: configPath, normalized: normalized,
	}, nil
}

func validateImportedLimaRuntimeMarker(
	value migrationNormalizedStageConfig,
	session *backend.Session,
) error {
	if value.Schema != migrationStageConfigSchema ||
		value.BackendIdentity != migration.OpaqueID(session.InstanceName) ||
		!migrationValidOpaqueRef(value.EnvironmentRef) ||
		!migrationValidOpaqueRef(value.ProfileComponent) ||
		!migrationValidOpaqueRef(value.RootDiskID) ||
		value.RootDiskLogicalBytes == 0 ||
		value.Runtime != "linux" || value.GuestArchitecture != "linux/arm64" ||
		value.GuestUser == "" ||
		value.HostMountsEnabled || value.ImportedNetwork || value.ImportedProvisioning || value.Runnable {
		return errors.New("imported Lima runtime marker is invalid or grants pre-run authority")
	}
	if session.TargetUser != "" && value.GuestUser != session.TargetUser {
		return errors.New("imported Lima runtime marker guest user does not match the destination profile")
	}
	previous := migration.OpaqueID("")
	for _, handle := range value.AttachedDiskHandles {
		if !migrationValidOpaqueRef(handle) || (previous != "" && previous >= handle) {
			return errors.New("imported Lima runtime marker attached-disk bindings are invalid")
		}
		previous = handle
	}
	if len(value.AttachedDiskMounts) != len(value.AttachedDiskHandles) {
		return errors.New("imported Lima runtime marker attached-disk mount closure is invalid")
	}
	mountHandles := make([]migration.OpaqueID, len(value.AttachedDiskMounts))
	for index, binding := range value.AttachedDiskMounts {
		if binding.Validate() != nil {
			return errors.New("imported Lima runtime marker attached-disk mount is invalid")
		}
		mountHandles[index] = migration.OpaqueID(strings.TrimPrefix(
			binding.DestinationGuestPath, "/mnt/lima-",
		))
	}
	slices.Sort(mountHandles)
	if !slices.Equal(mountHandles, value.AttachedDiskHandles) {
		return errors.New("imported Lima runtime marker attached-disk handles changed")
	}
	if (value.RuntimeImageLocation == "") != (value.RuntimeImageDigest == "") {
		return errors.New("imported Lima runtime marker image provenance is incomplete")
	}
	if value.RuntimeImageLocation != "" &&
		(strings.TrimSpace(value.RuntimeImageLocation) != value.RuntimeImageLocation ||
			len(value.RuntimeImageLocation) > 4096 || value.RuntimeImageDigest.Validate() != nil) {
		return errors.New("imported Lima runtime marker image provenance is invalid")
	}
	return nil
}

func readImportedLimaRuntimeConfig(path string) (migrationStagedLimaConfig, error) {
	data, info, err := readStableMigrationFile(path, migrationSnapshotMetadataLimit)
	if err != nil {
		return migrationStagedLimaConfig{}, err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return migrationStagedLimaConfig{}, errors.New("Lima runtime config is writable by other users")
	}
	var config migrationStagedLimaConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return migrationStagedLimaConfig{}, err
	}
	return config, nil
}

func (b Backend) importedRuntimeImageMatches(
	hostEnv []string,
	session *backend.Session,
	images []runtimeLimaImage,
) (bool, error) {
	state, err := loadImportedLimaRuntimeState(hostEnv, session)
	if err != nil || state == nil {
		return false, err
	}
	if session.RuntimeInstanceExpected == nil {
		return false, errors.New("imported Lima runtime image comparison lacks an expectation")
	}
	expected := session.RuntimeInstanceExpected
	if state.normalized.RuntimeImageLocation != expected.ImageLocation ||
		state.normalized.RuntimeImageDigest != migration.Digest("sha256:"+expected.ImageSHA256) {
		return false, errors.New("imported Lima runtime marker does not bind the selected runtime image provenance")
	}
	if len(images) != 1 || images[0].Location != migrationImportedRootImageSentinel ||
		images[0].Arch != expected.GuestArch || images[0].Digest != "" {
		return false, errors.New("imported Lima config does not retain the fail-closed root image sentinel")
	}
	return true, nil
}
