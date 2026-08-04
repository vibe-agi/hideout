package packagekit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/vibe-agi/hideout/internal/helperbin"
)

const (
	observerComponentID          = "linux-observer"
	observerArtifactTemplate     = "bin/hideout-observer-linux-{linuxGuestArch}"
	observerManifestTemplate     = observerArtifactTemplate + ".manifest.json"
	observerSourceLicense        = "Apache-2.0 OR GPL-2.0-only"
	observerKernelProgramLicense = "GPL"
	observerKernelLicenseText    = "LICENSES/GPL-2.0-only.txt"
	migrationAdoptComponentID    = "linux-migration-adopt"
	migrationAdoptArtifact       = "bin/hideout-migration-adopt-linux-{linuxGuestArch}"
	migrationAdoptManifest       = migrationAdoptArtifact + ".manifest.json"
	migrationVZAdoptComponentID  = "host-migration-vz-adopt"
	migrationVZAdoptArtifact     = "bin/hideout-migration-vz-adopt-{hostOS}-{hostArch}"
	migrationVZAdoptManifest     = migrationVZAdoptArtifact + ".manifest.json"
	embeddedAssetComponentKind   = "embedded-assets"
	linuxHelperComponentKind     = "linux-helper"
	hostHelperComponentKind      = "host-helper"
)

func ExpectedPackageComponentContract() PackageComponentContract {
	assets := BrowserConsoleAssets()
	componentAssets := make([]PackageComponentAsset, len(assets))
	for index, asset := range assets {
		componentAssets[index] = PackageComponentAsset{
			Path:      asset.Path,
			MediaType: asset.MediaType,
		}
	}
	return PackageComponentContract{
		Schema: PackageComponentContractSchema,
		Components: []PackageComponent{
			{
				ID:                   observerComponentID,
				Kind:                 linuxHelperComponentKind,
				ArtifactTemplate:     observerArtifactTemplate,
				ManifestTemplate:     observerManifestTemplate,
				License:              helperbin.LinuxObserverLicense,
				SourceLicense:        observerSourceLicense,
				KernelProgramLicense: observerKernelProgramLicense,
				LicenseText:          observerKernelLicenseText,
				BuildMode:            helperbin.LinuxObserverBuildMode,
				PackageOwned:         true,
			},
			{
				ID:               migrationAdoptComponentID,
				Kind:             linuxHelperComponentKind,
				ArtifactTemplate: migrationAdoptArtifact,
				ManifestTemplate: migrationAdoptManifest,
				License:          helperbin.LinuxMigrationAdoptLicense,
				BuildMode:        helperbin.LinuxMigrationAdoptBuildMode,
				PackageOwned:     true,
			},
			{
				ID:               migrationVZAdoptComponentID,
				Kind:             hostHelperComponentKind,
				ArtifactTemplate: migrationVZAdoptArtifact,
				ManifestTemplate: migrationVZAdoptManifest,
				License:          helperbin.HostMigrationVZAdoptLicense,
				BuildMode:        helperbin.HostMigrationVZAdoptBuildMode,
				UpstreamModule:   helperbin.HostMigrationVZAdoptUpstreamModule,
				UpstreamVersion:  helperbin.HostMigrationVZAdoptUpstreamVersion,
				PackageOwned:     true,
			},
			{
				ID:           BrowserConsoleAssetID,
				Kind:         embeddedAssetComponentKind,
				Container:    BrowserConsoleContainerPath,
				Manifest:     BrowserConsoleManifestPath,
				License:      BrowserConsoleAssetLicense,
				PackageOwned: true,
				Assets:       componentAssets,
			},
		},
	}
}

func ValidatePackageComponentContract(contract PackageComponentContract) error {
	if contract.Schema != PackageComponentContractSchema {
		return fmt.Errorf("unsupported package component contract schema %q", contract.Schema)
	}
	expected := ExpectedPackageComponentContract()
	if !reflect.DeepEqual(contract.Components, expected.Components) {
		return errors.New("package component contract does not match the supported helper and browser inventory")
	}
	return nil
}

func LoadPackageComponentContract(contractPath string) (PackageComponentContract, error) {
	file, err := os.Open(contractPath)
	if err != nil {
		return PackageComponentContract{}, fmt.Errorf("open package component contract: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var contract PackageComponentContract
	if err := decoder.Decode(&contract); err != nil {
		return PackageComponentContract{}, fmt.Errorf("parse package component contract: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return PackageComponentContract{}, fmt.Errorf("parse package component contract: %w", err)
	}
	if err := ValidatePackageComponentContract(contract); err != nil {
		return PackageComponentContract{}, err
	}
	return contract, nil
}

func verifyPackageComponentContract(root string, files []File, installed bool) error {
	contractRel := PackageComponentContractPath
	if installed {
		var err error
		contractRel, err = installPathForArtifact(contractRel)
		if err != nil {
			return err
		}
	}
	var contractFile File
	var found bool
	for _, file := range files {
		if file.Path == contractRel {
			contractFile = file
			found = true
			break
		}
	}
	if !found || contractFile.Kind != "runtime-contract" || contractFile.Executable {
		return fmt.Errorf("package requires component contract %q", contractRel)
	}
	contractPath, err := JoinRelative(root, contractRel)
	if err != nil {
		return err
	}
	_, err = LoadPackageComponentContract(contractPath)
	return err
}
