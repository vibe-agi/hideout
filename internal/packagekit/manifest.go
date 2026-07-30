package packagekit

import (
	"strings"
	"time"
)

const (
	ArtifactSchema                 = "hideout.package-manifest/v1"
	InstallStateSchema             = "hideout.package-install-state/v1"
	EmbeddedAssetManifestSchema    = "hideout.embedded-asset-manifest/v1"
	InstalledManifest              = "share/hideout/package-manifest.json"
	DefaultPackageRoot             = "hideout"
	BrowserConsoleAssetID          = "browser-console"
	BrowserConsoleContainerPath    = "bin/hideout"
	BrowserConsoleManifestPath     = "runtime/browser-console.assets.json"
	BrowserConsoleAssetLicense     = "Apache-2.0"
	PackageComponentContractSchema = "hideout.package-components/v1"
	PackageComponentContractPath   = "runtime/package-components.json"
	packageMetadataRoot            = "share/hideout"
)

type Manifest struct {
	Schema         string                 `json:"schema"`
	BuiltAt        string                 `json:"builtAt"`
	Release        ReleaseInfo            `json:"release,omitempty"`
	Source         SourceInfo             `json:"source,omitempty"`
	Build          BuildInfo              `json:"build,omitempty"`
	Target         Target                 `json:"target"`
	Runtime        RuntimeInfo            `json:"runtime,omitempty"`
	SigningSummary SigningSummary         `json:"signingSummary,omitempty"`
	Layout         Layout                 `json:"layout"`
	EmbeddedAssets []EmbeddedAssetBinding `json:"embeddedAssets"`
	Files          []File                 `json:"files"`
	Migration      Migration              `json:"migration"`
}

type ReleaseInfo struct {
	ProductVersion string `json:"productVersion"`
	Channel        string `json:"channel"`
	Tag            string `json:"tag"`
}

type SourceInfo struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Dirty      bool   `json:"dirty"`
}

type BuildInfo struct {
	Workflow string `json:"workflow"`
	Ref      string `json:"ref"`
}

type RuntimeInfo struct {
	Family            string `json:"family"`
	Revision          string `json:"revision"`
	CatalogFileSHA256 string `json:"catalogFileSHA256"`
	ArtifactSHA256    string `json:"artifactSHA256"`
}

type SigningSummary struct {
	Mode string `json:"mode"`
}

type Target struct {
	HostOS         string `json:"hostOS"`
	HostArch       string `json:"hostArch"`
	LinuxGuestArch string `json:"linuxGuestArch"`
}

type Layout struct {
	Root        string   `json:"root"`
	Binaries    []string `json:"binaries"`
	Entrypoints []string `json:"entrypoints"`
	Directories []string `json:"directories"`
}

type Migration struct {
	InstallStateSchema   string   `json:"installStateSchema,omitempty"`
	FromInstalledSchemas []string `json:"fromInstalledSchemas,omitempty"`
	MinimumPackageSchema string   `json:"minimumPackageSchema,omitempty"`
	MaximumPackageSchema string   `json:"maximumPackageSchema,omitempty"`
}

type File struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
}

// EmbeddedAssetBinding binds a package-owned container binary to a separately
// checksummed manifest describing the inert assets compiled into that exact
// binary. The manifest itself is also present in Files, so install and repair
// operations retain the same verification boundary.
type EmbeddedAssetBinding struct {
	ID             string `json:"id"`
	Container      string `json:"container"`
	Manifest       string `json:"manifest"`
	ManifestSHA256 string `json:"manifestSHA256"`
	License        string `json:"license"`
}

type EmbeddedAssetManifest struct {
	Schema          string          `json:"schema"`
	ID              string          `json:"id"`
	Container       string          `json:"container"`
	ContainerSHA256 string          `json:"containerSHA256"`
	License         string          `json:"license"`
	Assets          []EmbeddedAsset `json:"assets"`
}

type EmbeddedAsset struct {
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256"`
}

type PackageComponentContract struct {
	Schema     string             `json:"schema"`
	Components []PackageComponent `json:"components"`
}

type PackageComponent struct {
	ID                   string                  `json:"id"`
	Kind                 string                  `json:"kind"`
	ArtifactTemplate     string                  `json:"artifactTemplate,omitempty"`
	ManifestTemplate     string                  `json:"manifestTemplate,omitempty"`
	Container            string                  `json:"container,omitempty"`
	Manifest             string                  `json:"manifest,omitempty"`
	License              string                  `json:"license"`
	SourceLicense        string                  `json:"sourceLicense,omitempty"`
	KernelProgramLicense string                  `json:"kernelProgramLicense,omitempty"`
	LicenseText          string                  `json:"licenseText,omitempty"`
	BuildMode            string                  `json:"buildMode,omitempty"`
	PackageOwned         bool                    `json:"packageOwned"`
	Assets               []PackageComponentAsset `json:"assets,omitempty"`
}

type PackageComponentAsset struct {
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
}

type InstallState struct {
	Schema        string          `json:"schema"`
	InstalledAt   string          `json:"installedAt"`
	InstallPrefix string          `json:"installPrefix"`
	StoreRoot     string          `json:"storeRoot"`
	Package       InstalledSource `json:"package"`
	Files         []File          `json:"files"`
	Directories   []string        `json:"directories"`
	Migration     Migration       `json:"migration"`
	ObsoleteFiles []ObsoleteFile  `json:"obsoleteFiles,omitempty"`
}

type InstalledSource struct {
	Schema  string      `json:"schema"`
	BuiltAt string      `json:"builtAt"`
	Release ReleaseInfo `json:"release,omitempty"`
	Source  SourceInfo  `json:"source,omitempty"`
	Target  Target      `json:"target"`
}

type ObsoleteFile struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
	Reason     string `json:"reason"`
}

type MigrationDecision struct {
	Compatible              bool
	InstalledStateSchema    string
	PreviousPackageSchema   string
	NewPackageSchema        string
	InstalledProductVersion string
	CandidateProductVersion string
	AllowedInstalledSchemas []string
	MinimumPackageSchema    string
	MaximumPackageSchema    string
	Reason                  string
	Guidance                string
}

type ExternalPrerequisiteStatus struct {
	Name         string
	Status       string
	PackageOwned bool
	Source       string
	Hint         string
}

func NewInstallState(prefix, store string, manifest Manifest, files []File, dirs []string, now time.Time) InstallState {
	return InstallState{
		Schema:        InstallStateSchema,
		InstalledAt:   now.UTC().Format(time.RFC3339),
		InstallPrefix: prefix,
		StoreRoot:     store,
		Package: InstalledSource{
			Schema:  manifest.Schema,
			BuiltAt: manifest.BuiltAt,
			Release: manifest.Release,
			Source:  manifest.Source,
			Target:  manifest.Target,
		},
		Files:       files,
		Directories: dirs,
		Migration: Migration{
			InstallStateSchema:   InstallStateSchema,
			FromInstalledSchemas: []string{InstallStateSchema},
			MinimumPackageSchema: ArtifactSchema,
			MaximumPackageSchema: ArtifactSchema,
		},
	}
}

func (m Manifest) SourceCommit() string {
	return strings.TrimSpace(m.Source.Commit)
}

func (m Manifest) SourceDirty() bool {
	return m.Source.Dirty
}

func (s InstalledSource) SourceCommit() string {
	return strings.TrimSpace(s.Source.Commit)
}

func (s InstalledSource) SourceDirty() bool {
	return s.Source.Dirty
}
