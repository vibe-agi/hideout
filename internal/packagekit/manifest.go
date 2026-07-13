package packagekit

import (
	"strings"
	"time"
)

const (
	ArtifactSchema      = "hideout.package-manifest/v1"
	InstallStateSchema  = "hideout.package-install-state/v1"
	InstalledManifest   = "share/hideout/package-manifest.json"
	DefaultPackageRoot  = "hideout"
	packageMetadataRoot = "share/hideout"
)

type Manifest struct {
	Schema         string         `json:"schema"`
	BuiltAt        string         `json:"builtAt"`
	Release        ReleaseInfo    `json:"release,omitempty"`
	Source         SourceInfo     `json:"source,omitempty"`
	Build          BuildInfo      `json:"build,omitempty"`
	Target         Target         `json:"target"`
	Runtime        RuntimeInfo    `json:"runtime,omitempty"`
	SigningSummary SigningSummary `json:"signingSummary,omitempty"`
	Layout         Layout         `json:"layout"`
	Files          []File         `json:"files"`
	Migration      Migration      `json:"migration"`
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
