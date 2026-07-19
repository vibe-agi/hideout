package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	MachineIdentitySchema                 = "hideout.machine-identity/v1"
	BootConfigurationSchema               = "hideout.boot-configuration/v1"
	EnvironmentServiceConfigurationSchema = "hideout.environment-services/v1"
	SessionSnapshotSchema                 = "hideout.session-snapshot/v1"
)

// ValidConfigurationID reports whether value is a canonical identity emitted
// by one of the configuration-layer ID methods.
func ValidConfigurationID(value string) bool {
	return strings.HasPrefix(value, "sha256:") && isLowerHex(strings.TrimPrefix(value, "sha256:"), 64)
}

// ChangeImpact is the strongest lifecycle operation required to apply a
// configuration change. The ordering is intentional and used by MaxImpact.
type ChangeImpact string

const (
	ImpactNone        ChangeImpact = "none"
	ImpactLive        ChangeImpact = "live"
	ImpactNewSession  ChangeImpact = "new-session"
	ImpactReconfigure ChangeImpact = "reconfigure"
	ImpactRestart     ChangeImpact = "restart"
	ImpactRecreate    ChangeImpact = "recreate"
)

var impactOrder = map[ChangeImpact]int{
	ImpactNone: 0, ImpactLive: 1, ImpactNewSession: 2,
	ImpactReconfigure: 3, ImpactRestart: 4, ImpactRecreate: 5,
}

func MaxImpact(values ...ChangeImpact) ChangeImpact {
	out := ImpactNone
	for _, value := range values {
		if impactOrder[value] > impactOrder[out] {
			out = value
		}
	}
	return out
}

// ImageIdentity is content-oriented. A pinned URL is identified by its digest,
// not by the distributor URL or catalog metadata. Built-in templates have no
// digest today, so their canonical template name remains their identity.
type ImageIdentity struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	GuestArch string `json:"guestArch,omitempty"`
}

func ImageIdentityFor(imageRef string, runtime *RuntimeProvenance) (ImageIdentity, error) {
	declaration, err := ParseImageDeclaration(imageRef)
	if err != nil {
		return ImageIdentity{}, err
	}
	out := ImageIdentity{}
	switch declaration.Form {
	case ImageFormTemplate:
		out.Kind, out.Value = "template", declaration.Template
	case ImageFormURL:
		out.Kind, out.Value = "sha256", declaration.Digest
	default:
		return ImageIdentity{}, errors.New("unsupported image identity form")
	}
	if runtime != nil {
		if err := runtime.Validate(); err != nil {
			return ImageIdentity{}, err
		}
		if runtime.ImageRef() != imageRef {
			return ImageIdentity{}, errors.New("runtime provenance does not match image identity")
		}
		out.GuestArch = runtime.GuestArch
	}
	return out, nil
}

func (identity ImageIdentity) Validate() error {
	if (identity.Kind != "template" && identity.Kind != "sha256") || strings.TrimSpace(identity.Value) == "" {
		return errors.New("image identity is incomplete")
	}
	if identity.Kind == "sha256" && !isLowerHex(identity.Value, 64) {
		return errors.New("image identity digest is invalid")
	}
	return nil
}

// MachineIdentity contains only disk genesis and isolation structure. Policy,
// network, presentation, catalog metadata, and session inputs do not belong
// here because they can be reconciled without replacing the guest disk.
type MachineIdentity struct {
	Schema             string                   `json:"schema"`
	Backend            string                   `json:"backend"`
	Image              ImageIdentity            `json:"image"`
	TargetUser         string                   `json:"targetUser"`
	TargetUID          int                      `json:"targetUid"`
	GuestMachineID     string                   `json:"guestMachineId"`
	VMType             string                   `json:"vmType"`
	MountType          string                   `json:"mountType"`
	WorkspaceIsolation string                   `json:"workspaceIsolation"`
	StaticWorkspace    *StaticWorkspaceIdentity `json:"staticWorkspace,omitempty"`
}

// StaticWorkspaceIdentity contains only the profile-controlled shape of a
// Lima mount baked into machine configuration. The exact host/guest binding is
// pinned separately by the dedicated/workspace-bound environment record.
// Shared Portal and native host-process workspaces deliberately omit it.
type StaticWorkspaceIdentity struct {
	AccessMode string `json:"accessMode"`
	PathMode   string `json:"pathMode"`
}

func (identity MachineIdentity) Validate() error {
	if identity.Schema != MachineIdentitySchema || strings.TrimSpace(identity.Backend) == "" ||
		strings.TrimSpace(identity.TargetUser) == "" ||
		identity.TargetUID < 0 || strings.TrimSpace(identity.GuestMachineID) == "" || strings.TrimSpace(identity.VMType) == "" || strings.TrimSpace(identity.MountType) == "" ||
		strings.TrimSpace(identity.WorkspaceIsolation) == "" {
		return errors.New("machine identity is incomplete")
	}
	switch identity.WorkspaceIsolation {
	case "static-mount":
		if identity.StaticWorkspace == nil || strings.TrimSpace(identity.StaticWorkspace.AccessMode) == "" || strings.TrimSpace(identity.StaticWorkspace.PathMode) == "" {
			return errors.New("static workspace machine identity is incomplete")
		}
	case "workspace-portal", "host-process-workspace":
		if identity.StaticWorkspace != nil {
			return errors.New("dynamic workspace isolation cannot carry static mount identity")
		}
	default:
		return fmt.Errorf("unsupported workspace isolation %q", identity.WorkspaceIsolation)
	}
	if identity.Backend == "lima" && !isLowerHex(identity.GuestMachineID, 32) {
		return errors.New("guest machine identity is invalid")
	}
	return identity.Image.Validate()
}

func (identity MachineIdentity) ID() (string, error) {
	if err := identity.Validate(); err != nil {
		return "", err
	}
	return configurationID(identity)
}

// BootConfiguration is environment-global presentation applied by a
// privileged reconciler. It can be changed without replacing the guest disk.
type BootConfiguration struct {
	Schema   string `json:"schema"`
	Hostname string `json:"hostname"`
}

func (configuration BootConfiguration) Validate() error {
	if configuration.Schema != BootConfigurationSchema || strings.TrimSpace(configuration.Hostname) == "" {
		return errors.New("boot configuration is incomplete")
	}
	return nil
}

func (configuration BootConfiguration) ID() (string, error) {
	if err := configuration.Validate(); err != nil {
		return "", err
	}
	return configurationID(configuration)
}

type NetworkServiceConfiguration struct {
	Egress         string `json:"egress"`
	ProxySecretRef string `json:"proxySecretRef,omitempty"`
	Resolver       string `json:"resolver,omitempty"`
}

// EnvironmentServiceConfiguration is mutable environment-wide state. Its ID
// selects a service generation; it is deliberately not a machine identity.
type EnvironmentServiceConfiguration struct {
	Schema  string                      `json:"schema"`
	Network NetworkServiceConfiguration `json:"network"`
}

func (configuration EnvironmentServiceConfiguration) Validate() error {
	if configuration.Schema != EnvironmentServiceConfigurationSchema {
		return errors.New("environment service configuration schema is invalid")
	}
	switch configuration.Network.Egress {
	case "direct":
		if configuration.Network.ProxySecretRef != "" || configuration.Network.Resolver != "" {
			return errors.New("direct egress cannot carry proxy configuration")
		}
	case "proxy":
		if strings.TrimSpace(configuration.Network.ProxySecretRef) == "" || strings.TrimSpace(configuration.Network.Resolver) == "" {
			return errors.New("proxy egress requires a proxy reference and resolver")
		}
	default:
		return fmt.Errorf("unsupported environment egress %q", configuration.Network.Egress)
	}
	return nil
}

func (configuration EnvironmentServiceConfiguration) ID() (string, error) {
	if err := configuration.Validate(); err != nil {
		return "", err
	}
	return configurationID(configuration)
}

// SessionSnapshot identifies the complete policy/configuration snapshot used
// by one run. Digest is produced from a canonical, secret-free profile view.
type SessionSnapshot struct {
	Schema        string                  `json:"schema"`
	ProfileID     string                  `json:"profileId"`
	IdentityID    string                  `json:"identityId"`
	Digest        string                  `json:"digest"`
	PolicySources []SessionSourceIdentity `json:"policySources,omitempty"`
}

// SessionSourceIdentity binds mutable profile-owned source to the immutable
// bytes captured for one session. Runtime consumers use the private snapshot,
// while this identity remains path-free and safe to expose in state summaries.
type SessionSourceIdentity struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

func (snapshot SessionSnapshot) Validate() error {
	if snapshot.Schema != SessionSnapshotSchema || strings.TrimSpace(snapshot.ProfileID) == "" || strings.TrimSpace(snapshot.IdentityID) == "" ||
		!strings.HasPrefix(snapshot.Digest, "sha256:") || !isLowerHex(strings.TrimPrefix(snapshot.Digest, "sha256:"), 64) {
		return errors.New("session snapshot is incomplete")
	}
	seen := make(map[string]struct{}, len(snapshot.PolicySources))
	for _, source := range snapshot.PolicySources {
		if strings.TrimSpace(source.ID) == "" || !strings.HasPrefix(source.Digest, "sha256:") ||
			!isLowerHex(strings.TrimPrefix(source.Digest, "sha256:"), 64) {
			return errors.New("session policy source identity is incomplete")
		}
		if _, exists := seen[source.ID]; exists {
			return fmt.Errorf("session policy source id %q is duplicated", source.ID)
		}
		seen[source.ID] = struct{}{}
	}
	return nil
}

func (snapshot SessionSnapshot) ID() (string, error) {
	if err := snapshot.Validate(); err != nil {
		return "", err
	}
	return configurationID(snapshot)
}

type ConfigurationLayers struct {
	MachineID  string `json:"machineId"`
	BootID     string `json:"bootId"`
	ServicesID string `json:"servicesId"`
	SessionID  string `json:"sessionId"`
}

// Configuration is the desired state for one run, separated by the lifecycle
// operation that can make each part effective. A profile field may project into
// more than one layer; for example hostname is reconciled in the guest and also
// copied into each new process environment.
type Configuration struct {
	Machine  MachineIdentity                 `json:"machine"`
	Boot     BootConfiguration               `json:"boot"`
	Services EnvironmentServiceConfiguration `json:"services"`
	Session  SessionSnapshot                 `json:"session"`
	Layers   ConfigurationLayers             `json:"layers"`
}

type ConfigurationChange struct {
	Layer   string       `json:"layer"`
	Impact  ChangeImpact `json:"impact"`
	Pinned  string       `json:"pinned"`
	Current string       `json:"current"`
}

func CompareConfigurations(pinned, current Configuration) []ConfigurationChange {
	var changes []ConfigurationChange
	appendChange := func(layer string, impact ChangeImpact, before, after string) {
		if before != after {
			changes = append(changes, ConfigurationChange{Layer: layer, Impact: impact, Pinned: before, Current: after})
		}
	}
	appendChange("machine", ImpactRecreate, pinned.Layers.MachineID, current.Layers.MachineID)
	appendChange("boot", ImpactReconfigure, pinned.Layers.BootID, current.Layers.BootID)
	appendChange("environment-services", ImpactLive, pinned.Layers.ServicesID, current.Layers.ServicesID)
	appendChange("session", ImpactNewSession, pinned.Layers.SessionID, current.Layers.SessionID)
	return changes
}

func RequiredImpact(changes []ConfigurationChange) ChangeImpact {
	impact := ImpactNone
	for _, change := range changes {
		impact = MaxImpact(impact, change.Impact)
	}
	return impact
}

func DigestCanonical(value any) (string, error) {
	return configurationID(value)
}

func configurationID(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
