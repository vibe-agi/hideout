package manager

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	RunPlanVersion      = "hideout.run-plan/v1"
	AliasGuestWorkspace = "/workspace"
)

type RunPlanOptions struct {
	ProfileName    string
	Backend        string
	NetworkMode    string
	ProxySecretRef string
	Workspace      string
	GuestWorkspace string
	Ephemeral      bool
	Command        []string
}

type RunPlan struct {
	Version        string          `json:"version"`
	ProfileName    string          `json:"profile"`
	Backend        string          `json:"backend"`
	Workspace      string          `json:"workspace"`
	GuestWorkspace string          `json:"guestWorkspace"`
	WorkspaceMode  string          `json:"workspaceMode"`
	PathMode       string          `json:"pathMode"`
	NetworkMode    string          `json:"networkMode"`
	ProxySecretRef string          `json:"proxySecretRef,omitempty"`
	Ephemeral      bool            `json:"ephemeral"`
	Command        []string        `json:"command"`
	Profile        profile.Profile `json:"-"`
	RuntimeProfile profile.Profile `json:"-"`
}

func (c Core) PlanRun(opts RunPlanOptions) (RunPlan, error) {
	if c.Store.Root == "" {
		return RunPlan{}, errors.New("manager store root is required")
	}
	if len(opts.Command) == 0 {
		return RunPlan{}, errors.New("command is required after --")
	}
	profileName := opts.ProfileName
	if profileName == "" {
		profileName = "default"
	}
	p, err := c.Store.LoadOrInit(profileName)
	if err != nil {
		return RunPlan{}, err
	}
	if opts.NetworkMode != "" {
		p.Network.Mode = opts.NetworkMode
	}
	if opts.ProxySecretRef != "" {
		p.Network.ProxySecretRef = opts.ProxySecretRef
	}
	if err := p.Validate(); err != nil {
		return RunPlan{}, err
	}
	runtimeProfile := p
	if opts.Ephemeral {
		runtimeProfile, err = profile.EphemeralIdentityProfile(p)
		if err != nil {
			return RunPlan{}, err
		}
	}
	hostWorkspace, guestWorkspace, err := ResolveWorkspaceMapping(opts.Workspace, opts.GuestWorkspace, runtimeProfile)
	if err != nil {
		return RunPlan{}, err
	}
	backendName := ResolveBackendName(opts.Backend)
	if backendName != "native" && backendName != "lima" {
		return RunPlan{}, fmt.Errorf("backend %q is not implemented yet", backendName)
	}
	return RunPlan{
		Version:        RunPlanVersion,
		ProfileName:    p.Name,
		Backend:        backendName,
		Workspace:      hostWorkspace,
		GuestWorkspace: guestWorkspace,
		WorkspaceMode:  runtimeProfile.Workspace.Mode,
		PathMode:       runtimeProfile.Workspace.PathMode,
		NetworkMode:    runtimeProfile.Network.Mode,
		ProxySecretRef: runtimeProfile.Network.ProxySecretRef,
		Ephemeral:      opts.Ephemeral,
		Command:        append([]string(nil), opts.Command...),
		Profile:        p,
		RuntimeProfile: runtimeProfile,
	}, nil
}

func ResolveBackendName(name string) string {
	if name == "" || name == "auto" {
		return "lima"
	}
	return name
}

func ResolveWorkspaceMapping(hostWorkspace, guestWorkspace string, p profile.Profile) (string, string, error) {
	if hostWorkspace == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		hostWorkspace = wd
	} else if !filepath.IsAbs(hostWorkspace) {
		abs, err := filepath.Abs(hostWorkspace)
		if err != nil {
			return "", "", err
		}
		hostWorkspace = abs
	}
	hostWorkspace = filepath.Clean(hostWorkspace)
	st, err := os.Stat(hostWorkspace)
	if err != nil {
		return "", "", fmt.Errorf("workspace %q is not accessible: %w", hostWorkspace, err)
	}
	if !st.IsDir() {
		return "", "", fmt.Errorf("workspace %q is not a directory", hostWorkspace)
	}
	if guestWorkspace != "" {
		guestWorkspace, err = normalizeGuestWorkspace(guestWorkspace)
		if err != nil {
			return "", "", err
		}
		return hostWorkspace, guestWorkspace, nil
	}
	if p.Workspace.PathMode == "alias" {
		return hostWorkspace, AliasGuestWorkspace, nil
	}
	return hostWorkspace, hostWorkspace, nil
}

func normalizeGuestWorkspace(guestWorkspace string) (string, error) {
	if strings.TrimSpace(guestWorkspace) == "" {
		return "", errors.New("guest workspace is required")
	}
	if strings.Contains(guestWorkspace, "://") || strings.HasPrefix(guestWorkspace, "//") {
		return "", fmt.Errorf("guest workspace %q must be an absolute guest path", guestWorkspace)
	}
	if strings.Contains(guestWorkspace, `\`) {
		return "", fmt.Errorf("guest workspace %q must use slash-separated guest paths", guestWorkspace)
	}
	clean := path.Clean(guestWorkspace)
	if !path.IsAbs(clean) {
		return "", fmt.Errorf("guest workspace %q must be absolute", guestWorkspace)
	}
	if clean == "/" {
		return "", errors.New("guest workspace must not be the guest root")
	}
	return clean, nil
}
