package migration

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	PortableProfileSchema   = "hideout.migration-portable-profile/v1"
	MaxPortableProfileBytes = 1 << 20
)

var portableProfileExcludedClasses = []string{
	"profile.audit-disable",
	"profile.command-adapters",
	"profile.command-proxy-authority",
	"profile.endpoint-exposure",
	"profile.env.inherit",
	"profile.env.public-values",
	"profile.host-capabilities",
	"profile.hostfs",
	"profile.identity-state",
	"profile.network",
	"profile.policy-authority",
	"profile.policy-scripts",
}

// PortableProfile is the strict, inert profile component carried by a bundle.
// It intentionally has no fields capable of naming a host path, secret,
// endpoint, proxy, executable adapter, policy script, or source identity ID.
// Those source facts are handled as disabled proposals by Manager instead.
type PortableProfile struct {
	Schema           string                     `json:"schema"`
	SourceSchema     string                     `json:"sourceSchema"`
	NameHint         string                     `json:"nameHint"`
	Identity         PortableProfileIdentity    `json:"identity"`
	Workspace        PortableProfileWorkspace   `json:"workspace"`
	EnvDeny          []string                   `json:"envDeny"`
	Git              PortableProfileGit         `json:"git"`
	ExpectedCommands []string                   `json:"expectedCommands"`
	Environment      PortableProfileEnvironment `json:"environment"`
	Activity         *PortableProfileActivity   `json:"activity,omitempty"`
	ExcludedClasses  []string                   `json:"excludedClasses"`
}

type PortableProfileIdentity struct {
	User     string `json:"user"`
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
}

type PortableProfileWorkspace struct {
	PathMode string `json:"pathMode"`
}

type PortableProfileGit struct {
	UserName  string `json:"userName"`
	UserEmail string `json:"userEmail"`
}

type PortableProfileEnvironment struct {
	BaseImage string                         `json:"baseImage,omitempty"`
	Runtime   *environment.RuntimeProvenance `json:"runtime,omitempty"`
}

type PortableProfileActivity struct {
	MaxBytes      int64 `json:"maxBytes"`
	MaxAgeSeconds int64 `json:"maxAgeSeconds"`
}

func (snapshot PortableProfile) Validate() error {
	if snapshot.Schema != PortableProfileSchema ||
		snapshot.SourceSchema != profile.SchemaVersion ||
		!slices.Equal(snapshot.ExcludedClasses, portableProfileExcludedClasses) ||
		!validPortableProfileStrings(snapshot) ||
		!strictSortedPortableStrings(snapshot.EnvDeny) ||
		!strictSortedPortableStrings(snapshot.ExpectedCommands) {
		return errors.New("portable profile envelope is invalid")
	}
	if (snapshot.Environment.Runtime == nil) == (snapshot.Environment.BaseImage == "") {
		return errors.New("portable profile image provenance is incomplete or ambiguous")
	}
	if err := validatePortableProfileImage(snapshot.Environment); err != nil {
		return err
	}
	candidate, err := snapshot.destinationProfile(snapshot.NameHint)
	if err != nil {
		return err
	}
	if candidate.Identity.User != snapshot.Identity.User ||
		candidate.Identity.Hostname != snapshot.Identity.Hostname ||
		candidate.Identity.Timezone != snapshot.Identity.Timezone ||
		candidate.Identity.Locale != snapshot.Identity.Locale {
		return errors.New("portable profile identity is invalid")
	}
	return nil
}

// NormalizePortableProfile extracts only portable, non-authoritative fields
// from the current profile schema. Unknown or legacy source schemas fail closed;
// v1 never guesses an upgrade for fields whose authority meaning may have
// changed.
func NormalizePortableProfile(source profile.Profile) (PortableProfile, error) {
	if err := source.Validate(); err != nil {
		return PortableProfile{}, err
	}
	if source.SchemaVersion != profile.SchemaVersion {
		return PortableProfile{}, fmt.Errorf("unsupported source profile schema %q", source.SchemaVersion)
	}
	snapshot := PortableProfile{
		Schema: PortableProfileSchema, SourceSchema: source.SchemaVersion,
		NameHint: source.Name,
		Identity: PortableProfileIdentity{
			User: source.Identity.User, Hostname: source.Identity.Hostname,
			Timezone: source.Identity.Timezone, Locale: source.Identity.Locale,
		},
		Workspace: PortableProfileWorkspace{PathMode: source.Workspace.PathMode},
		EnvDeny:   clonePortableStrings(source.Env.Deny),
		Git: PortableProfileGit{
			UserName: source.Git.UserName, UserEmail: source.Git.UserEmail,
		},
		ExpectedCommands: clonePortableStrings(source.Tools.ExpectedCommands),
		Environment: PortableProfileEnvironment{
			BaseImage: source.Environment.BaseImage,
			Runtime:   clonePortableRuntime(source.Environment.Runtime),
		},
		ExcludedClasses: clonePortableStrings(portableProfileExcludedClasses),
	}
	if snapshot.Environment.Runtime == nil {
		snapshot.Environment.BaseImage = source.BaseImageOrBuiltin()
	}
	if source.Activity != nil {
		snapshot.Activity = &PortableProfileActivity{
			MaxBytes:      source.Activity.Retention.MaxBytes,
			MaxAgeSeconds: source.Activity.Retention.MaxAgeSeconds,
		}
	}
	sort.Strings(snapshot.EnvDeny)
	sort.Strings(snapshot.ExpectedCommands)
	if err := snapshot.Validate(); err != nil {
		return PortableProfile{}, err
	}
	return snapshot, nil
}

// EncodePortableProfile emits the same canonical JSON required of migration
// metadata records, so the component digest is stable across source machines.
func EncodePortableProfile(snapshot PortableProfile) ([]byte, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	encoded, err := canonicalMarshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxPortableProfileBytes {
		clear(encoded)
		return nil, ErrLimitExceeded
	}
	return encoded, nil
}

// DecodePortableProfile accepts exactly one canonical, current-schema document.
// No source metadata or authority field can be smuggled through unknown JSON.
func DecodePortableProfile(encoded []byte) (PortableProfile, error) {
	var snapshot PortableProfile
	if err := decodeCanonicalJSON(encoded, MaxPortableProfileBytes, &snapshot); err != nil {
		return PortableProfile{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return PortableProfile{}, err
	}
	return snapshot, nil
}

// DestinationProfile returns a valid destination-owned profile with fresh
// metadata and with imported host/network/script authority ineffective. It is
// suitable for private staging only; Manager must still finish proposal review
// and the atomic visibility commit before an environment may reference it.
func (snapshot PortableProfile) DestinationProfile(name string) (profile.Profile, error) {
	if err := snapshot.Validate(); err != nil {
		return profile.Profile{}, err
	}
	return snapshot.destinationProfile(name)
}

func (snapshot PortableProfile) Clone() PortableProfile {
	cloned := snapshot
	cloned.EnvDeny = clonePortableStrings(snapshot.EnvDeny)
	cloned.ExpectedCommands = clonePortableStrings(snapshot.ExpectedCommands)
	cloned.ExcludedClasses = clonePortableStrings(snapshot.ExcludedClasses)
	cloned.Environment.Runtime = clonePortableRuntime(snapshot.Environment.Runtime)
	if snapshot.Activity != nil {
		activity := *snapshot.Activity
		cloned.Activity = &activity
	}
	return cloned
}

func (snapshot PortableProfile) destinationProfile(name string) (profile.Profile, error) {
	destination := profile.Default(name)
	destination.Identity = profile.Identity{
		User: snapshot.Identity.User, Hostname: snapshot.Identity.Hostname,
		Timezone: snapshot.Identity.Timezone, Locale: snapshot.Identity.Locale,
	}
	destination.Workspace.PathMode = snapshot.Workspace.PathMode
	destination.Env.Public = map[string]string{}
	destination.Env.Deny = clonePortableStrings(snapshot.EnvDeny)
	destination.Env.Inherit = []string{}
	destination.Git = profile.Git{
		UserName: snapshot.Git.UserName, UserEmail: snapshot.Git.UserEmail,
	}
	destination.Tools.ExpectedCommands = clonePortableStrings(snapshot.ExpectedCommands)
	destination.Environment = profile.EnvironmentConfig{
		BaseImage: snapshot.Environment.BaseImage,
		Runtime:   clonePortableRuntime(snapshot.Environment.Runtime),
	}
	if snapshot.Activity == nil {
		destination.Activity = nil
	} else {
		destination.Activity = &profile.ActivityConfig{}
		destination.Activity.Retention.MaxBytes = snapshot.Activity.MaxBytes
		destination.Activity.Retention.MaxAgeSeconds = snapshot.Activity.MaxAgeSeconds
	}
	// Retain required structural defaults while removing every imported source
	// authority. guest.exec is guest-local; all host/network capabilities and
	// imported executable policy are absent.
	destination.HostCapabilities.Open.AllowURLs = false
	destination.HostCapabilities.Open.AllowLocalURLs = false
	destination.HostCapabilities.Open.AllowPrivateNetworkURLs = false
	destination.HostCapabilities.Open.AllowWorkspaceFiles = false
	destination.EndpointExposure.HostToGuest = nil
	destination.HostFS.Grants = nil
	destination.HostFS.Deny = nil
	destination.CommandAdapters.Adapters = nil
	destination.Policy.MaxCapabilities = []string{"guest.exec"}
	destination.Policy.ScriptRefs = nil
	destination.Audit.Enabled = true
	destination.Metadata = nil
	if err := destination.Validate(); err != nil {
		return profile.Profile{}, err
	}
	return destination, nil
}

func clonePortableRuntime(
	value *environment.RuntimeProvenance,
) *environment.RuntimeProvenance {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func validatePortableProfileImage(value PortableProfileEnvironment) error {
	if value.Runtime != nil {
		return value.Runtime.Validate()
	}
	declaration, err := environment.ParseImageDeclaration(value.BaseImage)
	if err != nil {
		return err
	}
	if declaration.Form != environment.ImageFormURL {
		return nil
	}
	location, err := url.Parse(declaration.Location)
	if err != nil || location.RawQuery != "" {
		return errors.New("portable profile image URL must not contain a query")
	}
	return nil
}

func clonePortableStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func strictSortedPortableStrings(values []string) bool {
	for index, value := range values {
		if !validManifestText(value, 1, 4096) ||
			(index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func validPortableProfileStrings(snapshot PortableProfile) bool {
	runtimeLocationValid := snapshot.Environment.Runtime == nil ||
		validManifestText(snapshot.Environment.Runtime.ArtifactLocation, 1, 4096)
	return runtimeLocationValid && validManifestText(snapshot.NameHint, 1, 128) &&
		validManifestText(snapshot.Identity.User, 1, 32) &&
		validManifestText(snapshot.Identity.Hostname, 1, 253) &&
		validManifestText(snapshot.Identity.Timezone, 1, 128) &&
		validManifestText(snapshot.Identity.Locale, 1, 128) &&
		validManifestText(snapshot.Workspace.PathMode, 1, 32) &&
		validManifestText(snapshot.Git.UserName, 1, 512) &&
		validManifestText(snapshot.Git.UserEmail, 1, 512) &&
		validManifestText(snapshot.Environment.BaseImage, 0, 4096)
}
