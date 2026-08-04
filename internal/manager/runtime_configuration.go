package manager

import (
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

type RuntimeConfiguration = environment.Configuration

type sessionNetworkSnapshot struct {
	ProxyEnvVisible bool `json:"proxyEnvVisible"`
}

type runtimeContractSnapshot struct {
	ContractID             string `json:"contractId,omitempty"`
	ContractDigest         string `json:"contractDigest,omitempty"`
	PackageInventoryDigest string `json:"packageInventoryDigest,omitempty"`
	GuestArch              string `json:"guestArch,omitempty"`
}

type sessionProfileSnapshot struct {
	Workspace        profile.Workspace        `json:"workspace"`
	User             string                   `json:"user"`
	Hostname         string                   `json:"hostname"`
	Timezone         string                   `json:"timezone"`
	Locale           string                   `json:"locale"`
	Env              profile.Env              `json:"env"`
	Git              profile.Git              `json:"git"`
	Tools            profile.Tools            `json:"tools"`
	Runtime          runtimeContractSnapshot  `json:"runtime,omitempty"`
	Network          sessionNetworkSnapshot   `json:"network"`
	HostCapabilities profile.HostCapabilities `json:"hostCapabilities"`
	EndpointExposure profile.EndpointExposure `json:"endpointExposure"`
	HostFS           any                      `json:"hostfs"`
	CommandProxy     profile.CommandProxy     `json:"commandProxy"`
	CommandAdapters  profile.CommandAdapters  `json:"commandAdapters"`
	Policy           profile.Policy           `json:"policy"`
	Audit            profile.Audit            `json:"audit"`
}

func RuntimeConfigurationForProfile(p profile.Profile, backendName string, mode environment.Mode) (RuntimeConfiguration, error) {
	machine, boot, machineID, bootID, err := MachineBootConfigurationForProfile(p, backendName, mode)
	if err != nil {
		return RuntimeConfiguration{}, err
	}

	egress := "direct"
	serviceNetwork := environment.NetworkServiceConfiguration{Egress: egress}
	if p.Network.Mode == profile.NetworkModeTun2Socks {
		egress = "proxy"
		serviceNetwork = environment.NetworkServiceConfiguration{
			Egress: egress, ProxySecretRef: p.Network.ProxySecretRef,
			Resolver: p.Network.MediatedResolver,
		}
	}
	services := environment.EnvironmentServiceConfiguration{
		Schema:  environment.EnvironmentServiceConfigurationSchema,
		Network: serviceNetwork,
	}
	snapshot, sessionID, err := SessionSnapshotForProfile(p)
	if err != nil {
		return RuntimeConfiguration{}, err
	}

	servicesID, err := services.ID()
	if err != nil {
		return RuntimeConfiguration{}, err
	}
	return environment.Configuration{
		Machine: machine, Boot: boot, Services: services, Session: snapshot,
		Layers: environment.ConfigurationLayers{
			MachineID: machineID, BootID: bootID, ServicesID: servicesID, SessionID: sessionID,
		},
	}, nil
}

// SessionSnapshotForProfile captures only inputs projected into a new target
// process. It is independent of machine and service lifecycle state so two
// concurrent sessions in one environment can carry different snapshots.
func SessionSnapshotForProfile(p profile.Profile) (environment.SessionSnapshot, string, error) {
	runtimeSnapshot := runtimeContractSnapshot{}
	if p.Environment.Runtime != nil {
		runtimeSnapshot = runtimeContractSnapshot{
			ContractID: p.Environment.Runtime.ContractID, ContractDigest: p.Environment.Runtime.ContractDigest,
			PackageInventoryDigest: p.Environment.Runtime.PackageInventoryDigest, GuestArch: p.Environment.Runtime.GuestArch,
		}
	}

	snapshotDigest, err := environment.DigestCanonical(sessionProfileSnapshot{
		Workspace: p.Workspace, User: p.Identity.User, Hostname: p.Identity.Hostname,
		Timezone: p.Identity.Timezone, Locale: p.Identity.Locale,
		Env: p.Env, Git: p.Git, Tools: p.Tools, Runtime: runtimeSnapshot,
		Network:          sessionNetworkSnapshot{ProxyEnvVisible: p.Network.ProxyEnvVisible},
		HostCapabilities: p.HostCapabilities, EndpointExposure: p.EndpointExposure,
		HostFS: p.HostFS, CommandProxy: p.CommandProxy, CommandAdapters: p.CommandAdapters,
		Policy: p.Policy, Audit: p.Audit,
	})
	if err != nil {
		return environment.SessionSnapshot{}, "", err
	}
	snapshot := environment.SessionSnapshot{
		Schema: environment.SessionSnapshotSchema, ProfileID: p.Metadata["profileId"], IdentityID: p.Metadata["identityId"], Digest: snapshotDigest,
	}
	sessionID, err := snapshot.ID()
	if err != nil {
		return environment.SessionSnapshot{}, "", err
	}
	return snapshot, sessionID, nil
}

// MachineBootConfigurationForProfile computes only lifecycle state required to
// select or create the guest. Invalid environment-service or session inputs
// must not erase a valid machine identity or turn their errors into recreate
// diagnostics.
func MachineBootConfigurationForProfile(p profile.Profile, backendName string, mode environment.Mode) (environment.MachineIdentity, environment.BootConfiguration, string, string, error) {
	image, err := environment.ImageIdentityFor(p.BaseImageOrBuiltin(), p.Environment.Runtime)
	if err != nil {
		return environment.MachineIdentity{}, environment.BootConfiguration{}, "", "", err
	}
	machine := environment.MachineIdentity{
		Schema: environment.MachineIdentitySchema, Backend: backendName, Image: image,
		TargetUser: p.Identity.User, GuestMachineID: p.Metadata["machineId"], WorkspaceIsolation: "static-mount",
	}
	switch backendName {
	case "lima":
		machine.TargetUID = 1000
		machine.VMType = "vz"
		machine.MountType = "virtiofs"
		if environment.UsesWorkspacePortal(mode) {
			machine.WorkspaceIsolation = "workspace-portal"
		} else {
			machine.StaticWorkspace = &environment.StaticWorkspaceIdentity{
				AccessMode: p.Workspace.Mode,
				PathMode:   p.Workspace.PathMode,
			}
		}
	case "native":
		machine.TargetUser = "host-user"
		machine.TargetUID = runtimeUID()
		machine.GuestMachineID = "host-unmodified"
		machine.VMType = "host-process"
		machine.MountType = "host-filesystem"
		machine.WorkspaceIsolation = "host-process-workspace"
	default:
		return environment.MachineIdentity{}, environment.BootConfiguration{}, "", "", fmt.Errorf("unsupported backend %q", backendName)
	}
	boot := environment.BootConfiguration{Schema: environment.BootConfigurationSchema, Hostname: p.Identity.Hostname}
	if backendName == "native" {
		boot.Hostname = "host-unmodified"
	}

	machineID, err := machine.ID()
	if err != nil {
		return environment.MachineIdentity{}, environment.BootConfiguration{}, "", "", err
	}
	bootID, err := boot.ID()
	if err != nil {
		return environment.MachineIdentity{}, environment.BootConfiguration{}, "", "", err
	}
	return machine, boot, machineID, bootID, nil
}

var runtimeUID = func() int {
	// The native backend is a weak host-process harness; UID 0 is a valid
	// observation there and is not confused with Lima's target-user claim.
	return os.Getuid()
}
