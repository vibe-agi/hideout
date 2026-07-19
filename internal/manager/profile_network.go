package manager

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
)

type ProfileNetworkOptions struct {
	ProfileName      string
	Mode             string
	ProxySecretRef   string
	MediatedResolver string
}

type ProfileNetworkState struct {
	Profile          string `json:"profile"`
	Mode             string `json:"mode"`
	ProxySecretRef   string `json:"proxySecretRef,omitempty"`
	MediatedResolver string `json:"mediatedResolver,omitempty"`
	ProxyEnvVisible  bool   `json:"proxyEnvVisible"`
}

type ProfileNetworkPlan struct {
	Profile string              `json:"profile"`
	Before  ProfileNetworkState `json:"before"`
	After   ProfileNetworkState `json:"after"`
	Changed bool                `json:"changed"`
	Status  string              `json:"status"`
	Message string              `json:"message"`
}

type ProfileNetworkResult struct {
	Plan    ProfileNetworkPlan  `json:"plan"`
	Applied bool                `json:"applied"`
	Network ProfileNetworkState `json:"network"`
}

func (c Core) ProfileNetwork(profileName string) (ProfileNetworkState, error) {
	name, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return ProfileNetworkState{}, err
	}
	p, err := loadProfileForPlanning(c.Store, name)
	if err != nil {
		return ProfileNetworkState{}, err
	}
	return profileNetworkState(p), nil
}

func (c Core) PlanProfileNetwork(opts ProfileNetworkOptions) (ProfileNetworkPlan, error) {
	name, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return ProfileNetworkPlan{}, err
	}
	current, err := loadProfileForPlanning(c.Store, name)
	if err != nil {
		return ProfileNetworkPlan{}, err
	}

	after := current
	after.Network.ProxyEnvVisible = false
	switch strings.TrimSpace(opts.Mode) {
	case profile.NetworkModeDirect:
		after.Network.Mode = profile.NetworkModeDirect
	case profile.NetworkModeTun2Socks:
		secretRef := strings.TrimSpace(opts.ProxySecretRef)
		if secretRef == "" {
			return ProfileNetworkPlan{}, errors.New("proxy secret reference is required")
		}
		resolver := strings.TrimSpace(opts.MediatedResolver)
		if resolver == "" {
			resolver = strings.TrimSpace(current.Network.MediatedResolver)
		}
		if resolver == "" {
			return ProfileNetworkPlan{}, fmt.Errorf("connection %q needs a mediated resolver; use 'hideout connect through %s using <resolver>'", secretRef, secretRef)
		}
		after.Network.Mode = profile.NetworkModeTun2Socks
		after.Network.ProxySecretRef = secretRef
		after.Network.MediatedResolver = resolver
	default:
		return ProfileNetworkPlan{}, fmt.Errorf("unsupported network mode %q", opts.Mode)
	}
	if err := after.Validate(); err != nil {
		return ProfileNetworkPlan{}, err
	}

	beforeState := profileNetworkState(current)
	afterState := profileNetworkState(after)
	plan := ProfileNetworkPlan{
		Profile: current.Name,
		Before:  beforeState,
		After:   afterState,
		Changed: beforeState != afterState,
		Status:  "pending",
		Message: "change profile network posture on the next eligible attach",
	}
	if !plan.Changed {
		plan.Status = "noop"
		plan.Message = "profile network posture is already selected"
	}
	return plan, nil
}

func (c Core) ApplyProfileNetwork(plan ProfileNetworkPlan) (ProfileNetworkResult, error) {
	if !plan.Changed {
		return ProfileNetworkResult{Plan: plan, Network: plan.After}, nil
	}
	var result ProfileNetworkResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		current, err := c.Store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		if profileNetworkState(current) != plan.Before {
			return errors.New("profile network changed after planning; plan again")
		}
		current.Network = profile.Network{
			Mode:             plan.After.Mode,
			ProxySecretRef:   plan.After.ProxySecretRef,
			MediatedResolver: plan.After.MediatedResolver,
			ProxyEnvVisible:  false,
		}
		if err := current.Validate(); err != nil {
			return err
		}
		if err := c.Store.Save(current); err != nil {
			return err
		}
		result = ProfileNetworkResult{Plan: plan, Applied: true, Network: profileNetworkState(current)}
		return nil
	})
	if err != nil {
		return ProfileNetworkResult{}, err
	}
	return result, nil
}

func profileNetworkState(p profile.Profile) ProfileNetworkState {
	return ProfileNetworkState{
		Profile:          p.Name,
		Mode:             p.Network.Mode,
		ProxySecretRef:   p.Network.ProxySecretRef,
		MediatedResolver: p.Network.MediatedResolver,
		ProxyEnvVisible:  p.Network.ProxyEnvVisible,
	}
}
