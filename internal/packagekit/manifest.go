package packagekit

import "time"

const (
	ArtifactSchema      = "hideout.package-manifest.v1"
	InstallStateSchema  = "hideout.package-install-state.v1"
	InstalledManifest   = "share/hideout/package-manifest.json"
	DefaultPackageRoot  = "hideout"
	packageMetadataRoot = "share/hideout"
)

type Manifest struct {
	Schema    string    `json:"schema"`
	BuiltAt   string    `json:"builtAt"`
	Git       GitInfo   `json:"git"`
	Target    Target    `json:"target"`
	Layout    Layout    `json:"layout"`
	Files     []File    `json:"files"`
	Migration Migration `json:"migration"`
}

type GitInfo struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
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
	Schema  string  `json:"schema"`
	BuiltAt string  `json:"builtAt"`
	Git     GitInfo `json:"git"`
	Target  Target  `json:"target"`
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
			Git:     manifest.Git,
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
