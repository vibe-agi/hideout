package manager

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const ProfileEnvPlanVersion = "hideout.profile-env-plan/v1"

type ProfileEnvOptions struct {
	ProfileName string
	Operation   string
	Name        string
	Value       string
}

type ProfileEnvPlan struct {
	Version       string   `json:"version"`
	Profile       string   `json:"profile"`
	Operation     string   `json:"operation"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	ValueProvided bool     `json:"valueProvided,omitempty"`
	Status        string   `json:"status"`
	Message       string   `json:"message"`
	Changed       bool     `json:"changed"`
	PublicBefore  []string `json:"publicBefore"`
	PublicAfter   []string `json:"publicAfter"`
	InheritBefore []string `json:"inheritBefore"`
	InheritAfter  []string `json:"inheritAfter"`
	DenyBefore    []string `json:"denyBefore"`
	DenyAfter     []string `json:"denyAfter"`
	value         string
}

type ProfileEnvResult struct {
	Version string         `json:"version"`
	Plan    ProfileEnvPlan `json:"plan"`
	Applied bool           `json:"applied"`
	Public  []string       `json:"public"`
	Inherit []string       `json:"inherit"`
	Deny    []string       `json:"deny"`
}

func (c Core) PlanProfileEnv(opts ProfileEnvOptions) (ProfileEnvPlan, error) {
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return ProfileEnvPlan{}, err
	}
	operation := strings.TrimSpace(opts.Operation)
	current, err := loadProfileForPlanning(c.Store, profileName)
	if err != nil {
		return ProfileEnvPlan{}, err
	}
	afterProfile := current
	plan := ProfileEnvPlan{
		Version:       ProfileEnvPlanVersion,
		Profile:       current.Name,
		Operation:     operation,
		Status:        "pending",
		PublicBefore:  sortedProfileEnvPublicKeys(current.Env.Public),
		InheritBefore: sortedStringsForManager(current.Env.Inherit),
		DenyBefore:    sortedStringsForManager(current.Env.Deny),
	}
	name := strings.TrimSpace(opts.Name)
	switch operation {
	case "set":
		if name == "" {
			return ProfileEnvPlan{}, errors.New("env name is required")
		}
		if afterProfile.Env.Public == nil {
			afterProfile.Env.Public = map[string]string{}
		}
		existing, exists := afterProfile.Env.Public[name]
		afterProfile.Env.Public[name] = opts.Value
		plan.Kind = "env.public"
		plan.Name = name
		plan.ValueProvided = true
		plan.value = opts.Value
		plan.Changed = !exists || existing != opts.Value
		if plan.Changed {
			plan.Message = "set profile public env value"
		} else {
			plan.Status = "noop"
			plan.Message = "profile public env value is already set"
		}
	case "unset":
		if name == "" {
			return ProfileEnvPlan{}, errors.New("env name is required")
		}
		_, exists := afterProfile.Env.Public[name]
		delete(afterProfile.Env.Public, name)
		plan.Kind = "env.public"
		plan.Name = name
		plan.Changed = exists
		if plan.Changed {
			plan.Message = "unset profile public env value"
		} else {
			plan.Status = "noop"
			plan.Message = "profile public env value is not set"
		}
	case "inherit":
		if name == "" {
			return ProfileEnvPlan{}, errors.New("env name or pattern is required")
		}
		beforeLen := len(afterProfile.Env.Inherit)
		afterProfile.Env.Inherit = appendStringIfMissing(afterProfile.Env.Inherit, name)
		plan.Kind = "env.inherit"
		plan.Name = name
		plan.Changed = len(afterProfile.Env.Inherit) != beforeLen
		if plan.Changed {
			plan.Message = "inherit host env name"
		} else {
			plan.Status = "noop"
			plan.Message = "host env name is already inherited"
		}
	case "uninherit":
		if name == "" {
			return ProfileEnvPlan{}, errors.New("env name or pattern is required")
		}
		beforeLen := len(afterProfile.Env.Inherit)
		afterProfile.Env.Inherit = removeStringForManager(afterProfile.Env.Inherit, name)
		plan.Kind = "env.inherit"
		plan.Name = name
		plan.Changed = len(afterProfile.Env.Inherit) != beforeLen
		if plan.Changed {
			plan.Message = "remove inherited host env name"
		} else {
			plan.Status = "noop"
			plan.Message = "host env name is not inherited"
		}
	case "deny":
		if name == "" {
			return ProfileEnvPlan{}, errors.New("env name or pattern is required")
		}
		beforeLen := len(afterProfile.Env.Deny)
		afterProfile.Env.Deny = appendStringIfMissing(afterProfile.Env.Deny, name)
		plan.Kind = "env.deny"
		plan.Name = name
		plan.Changed = len(afterProfile.Env.Deny) != beforeLen
		if plan.Changed {
			plan.Message = "deny env name or pattern"
		} else {
			plan.Status = "noop"
			plan.Message = "env deny rule already exists"
		}
	case "undeny":
		if name == "" {
			return ProfileEnvPlan{}, errors.New("env name or pattern is required")
		}
		beforeLen := len(afterProfile.Env.Deny)
		afterProfile.Env.Deny = removeStringForManager(afterProfile.Env.Deny, name)
		plan.Kind = "env.deny"
		plan.Name = name
		plan.Changed = len(afterProfile.Env.Deny) != beforeLen
		if plan.Changed {
			plan.Message = "remove env deny rule"
		} else {
			plan.Status = "noop"
			plan.Message = "env deny rule is not present"
		}
	default:
		return ProfileEnvPlan{}, fmt.Errorf("unsupported profile-env operation %q", operation)
	}
	if err := afterProfile.Validate(); err != nil {
		return ProfileEnvPlan{}, err
	}
	plan.PublicAfter = sortedProfileEnvPublicKeys(afterProfile.Env.Public)
	plan.InheritAfter = sortedStringsForManager(afterProfile.Env.Inherit)
	plan.DenyAfter = sortedStringsForManager(afterProfile.Env.Deny)
	return plan, nil
}

func (c Core) ApplyProfileEnv(plan ProfileEnvPlan) (ProfileEnvResult, error) {
	if plan.Version != ProfileEnvPlanVersion {
		return ProfileEnvResult{}, errors.New("invalid profile-env plan version")
	}
	if !plan.Changed {
		return ProfileEnvResult{
			Version: ProfileEnvPlanVersion,
			Plan:    plan,
			Applied: false,
			Public:  append([]string(nil), plan.PublicAfter...),
			Inherit: append([]string(nil), plan.InheritAfter...),
			Deny:    append([]string(nil), plan.DenyAfter...),
		}, nil
	}
	p, err := c.Store.LoadOrInit(plan.Profile)
	if err != nil {
		return ProfileEnvResult{}, err
	}
	switch plan.Operation {
	case "set":
		if plan.Name == "" {
			return ProfileEnvResult{}, errors.New("env name is required")
		}
		if !plan.ValueProvided {
			return ProfileEnvResult{}, errors.New("env value is required")
		}
		if p.Env.Public == nil {
			p.Env.Public = map[string]string{}
		}
		p.Env.Public[plan.Name] = plan.value
	case "unset":
		delete(p.Env.Public, plan.Name)
	case "inherit":
		p.Env.Inherit = appendStringIfMissing(p.Env.Inherit, plan.Name)
	case "uninherit":
		p.Env.Inherit = removeStringForManager(p.Env.Inherit, plan.Name)
	case "deny":
		p.Env.Deny = appendStringIfMissing(p.Env.Deny, plan.Name)
	case "undeny":
		p.Env.Deny = removeStringForManager(p.Env.Deny, plan.Name)
	default:
		return ProfileEnvResult{}, fmt.Errorf("unsupported profile-env operation %q", plan.Operation)
	}
	if err := p.Validate(); err != nil {
		return ProfileEnvResult{}, err
	}
	if err := c.Store.Save(p); err != nil {
		return ProfileEnvResult{}, err
	}
	return ProfileEnvResult{
		Version: ProfileEnvPlanVersion,
		Plan:    plan,
		Applied: plan.Changed,
		Public:  sortedProfileEnvPublicKeys(p.Env.Public),
		Inherit: sortedStringsForManager(p.Env.Inherit),
		Deny:    sortedStringsForManager(p.Env.Deny),
	}, nil
}

func sortedProfileEnvPublicKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringsForManager(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func appendStringIfMissing(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func removeStringForManager(values []string, value string) []string {
	out := values[:0]
	for _, item := range values {
		if item == value {
			continue
		}
		out = append(out, item)
	}
	return out
}
