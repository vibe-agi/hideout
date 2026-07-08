package manager

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/hostfs"
)

const ProfileHostFSPlanVersion = "hideout.profile-hostfs-plan/v1"

type ProfileHostFSOptions struct {
	ProfileName string
	Operation   string
	Rule        string
	RuleID      string
	Reason      string
}

type ProfileHostFSRuleSummary struct {
	ID        string       `json:"id"`
	Effect    string       `json:"effect"`
	HostPath  string       `json:"hostPath"`
	Ops       []hostfs.Op  `json:"ops,omitempty"`
	Overlay   bool         `json:"overlay,omitempty"`
	Scope     hostfs.Scope `json:"scope"`
	Reason    string       `json:"reason,omitempty"`
	CreatedAt string       `json:"createdAt,omitempty"`
}

type ProfileHostFSPlan struct {
	Version      string                     `json:"version"`
	Profile      string                     `json:"profile"`
	Operation    string                     `json:"operation"`
	Rule         string                     `json:"rule,omitempty"`
	RuleID       string                     `json:"ruleId,omitempty"`
	Reason       string                     `json:"reason,omitempty"`
	Status       string                     `json:"status"`
	Message      string                     `json:"message"`
	Changed      bool                       `json:"changed"`
	PlannedRule  *ProfileHostFSRuleSummary  `json:"plannedRule,omitempty"`
	RemovedRule  *ProfileHostFSRuleSummary  `json:"removedRule,omitempty"`
	GrantsBefore []ProfileHostFSRuleSummary `json:"grantsBefore"`
	GrantsAfter  []ProfileHostFSRuleSummary `json:"grantsAfter"`
	DenyBefore   []ProfileHostFSRuleSummary `json:"denyBefore"`
	DenyAfter    []ProfileHostFSRuleSummary `json:"denyAfter"`
}

type ProfileHostFSResult struct {
	Version string                     `json:"version"`
	Plan    ProfileHostFSPlan          `json:"plan"`
	Applied bool                       `json:"applied"`
	Grants  []ProfileHostFSRuleSummary `json:"grants"`
	Deny    []ProfileHostFSRuleSummary `json:"deny"`
}

func (c Core) PlanProfileHostFS(opts ProfileHostFSOptions) (ProfileHostFSPlan, error) {
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return ProfileHostFSPlan{}, err
	}
	operation := strings.TrimSpace(opts.Operation)
	current, err := loadProfileForPlanning(c.Store, profileName)
	if err != nil {
		return ProfileHostFSPlan{}, err
	}
	afterProfile := current
	plan := ProfileHostFSPlan{
		Version:      ProfileHostFSPlanVersion,
		Profile:      current.Name,
		Operation:    operation,
		Status:       "pending",
		GrantsBefore: profileHostFSRuleSummaries(current.HostFS.Grants, false),
		DenyBefore:   profileHostFSRuleSummaries(current.HostFS.Deny, true),
	}
	switch operation {
	case "add":
		rule, summary, err := planProfileHostFSRule(opts, current.HostFS, false)
		if err != nil {
			return ProfileHostFSPlan{}, err
		}
		afterProfile.HostFS.Grants = append(afterProfile.HostFS.Grants, rule)
		plan.Rule = opts.Rule
		plan.Reason = strings.TrimSpace(opts.Reason)
		plan.RuleID = rule.ID
		plan.PlannedRule = &summary
		plan.Changed = true
		plan.Message = "add profile HostFS allow rule"
	case "deny":
		rule, summary, err := planProfileHostFSRule(opts, current.HostFS, true)
		if err != nil {
			return ProfileHostFSPlan{}, err
		}
		afterProfile.HostFS.Deny = append(afterProfile.HostFS.Deny, rule)
		plan.Rule = opts.Rule
		plan.Reason = strings.TrimSpace(opts.Reason)
		plan.RuleID = rule.ID
		plan.PlannedRule = &summary
		plan.Changed = true
		plan.Message = "add profile HostFS deny rule"
	case "remove":
		ruleID := strings.TrimSpace(opts.RuleID)
		if ruleID == "" {
			return ProfileHostFSPlan{}, errors.New("ruleId is required")
		}
		plan.RuleID = ruleID
		var removed *ProfileHostFSRuleSummary
		afterProfile.HostFS.Grants, removed = removeProfileHostFSRule(afterProfile.HostFS.Grants, ruleID, false)
		if removed == nil {
			afterProfile.HostFS.Deny, removed = removeProfileHostFSRule(afterProfile.HostFS.Deny, ruleID, true)
		}
		if removed == nil {
			return ProfileHostFSPlan{}, fmt.Errorf("profile HostFS rule %q not found", ruleID)
		}
		plan.RemovedRule = removed
		plan.Changed = true
		plan.Message = "remove profile HostFS rule"
	default:
		return ProfileHostFSPlan{}, fmt.Errorf("unsupported profile-hostfs operation %q", operation)
	}
	if err := afterProfile.Validate(); err != nil {
		return ProfileHostFSPlan{}, err
	}
	plan.GrantsAfter = profileHostFSRuleSummaries(afterProfile.HostFS.Grants, false)
	plan.DenyAfter = profileHostFSRuleSummaries(afterProfile.HostFS.Deny, true)
	return plan, nil
}

func (c Core) ApplyProfileHostFS(plan ProfileHostFSPlan) (ProfileHostFSResult, error) {
	if plan.Version != ProfileHostFSPlanVersion {
		return ProfileHostFSResult{}, errors.New("invalid profile-hostfs plan version")
	}
	var result ProfileHostFSResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		p, err := c.Store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		switch plan.Operation {
		case "add":
			rule, err := ruleFromProfileHostFSPlan(plan, p.HostFS, false)
			if err != nil {
				return err
			}
			p.HostFS.Grants = append(p.HostFS.Grants, rule)
		case "deny":
			rule, err := ruleFromProfileHostFSPlan(plan, p.HostFS, true)
			if err != nil {
				return err
			}
			p.HostFS.Deny = append(p.HostFS.Deny, rule)
		case "remove":
			var removed *ProfileHostFSRuleSummary
			p.HostFS.Grants, removed = removeProfileHostFSRule(p.HostFS.Grants, plan.RuleID, false)
			if removed == nil {
				p.HostFS.Deny, removed = removeProfileHostFSRule(p.HostFS.Deny, plan.RuleID, true)
			}
			if removed == nil {
				return fmt.Errorf("profile HostFS rule %q not found", plan.RuleID)
			}
		default:
			return fmt.Errorf("unsupported profile-hostfs operation %q", plan.Operation)
		}
		if err := p.Validate(); err != nil {
			return err
		}
		if err := c.Store.Save(p); err != nil {
			return err
		}
		result = ProfileHostFSResult{
			Version: ProfileHostFSPlanVersion,
			Plan:    plan,
			Applied: plan.Changed,
			Grants:  profileHostFSRuleSummaries(p.HostFS.Grants, false),
			Deny:    profileHostFSRuleSummaries(p.HostFS.Deny, true),
		}
		return nil
	})
	if err != nil {
		return ProfileHostFSResult{}, err
	}
	return result, nil
}

func planProfileHostFSRule(opts ProfileHostFSOptions, config hostfs.Config, deny bool) (hostfs.Rule, ProfileHostFSRuleSummary, error) {
	flagName := "--fs"
	if deny {
		flagName = "--no-fs"
	}
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		return hostfs.Rule{}, ProfileHostFSRuleSummary{}, errors.New("reason is required")
	}
	rule, err := hostfs.ParseRuleSpec(flagName, opts.Rule, reason)
	if err != nil {
		return hostfs.Rule{}, ProfileHostFSRuleSummary{}, err
	}
	now := time.Now().UTC()
	rule.ID, err = hostfs.NewRuleID(config)
	if err != nil {
		return hostfs.Rule{}, ProfileHostFSRuleSummary{}, err
	}
	rule.CreatedAt = &now
	rule.Reason = reason
	summary := profileHostFSRuleSummary(rule, deny)
	return rule, summary, nil
}

func ruleFromProfileHostFSPlan(plan ProfileHostFSPlan, config hostfs.Config, deny bool) (hostfs.Rule, error) {
	flagName := "--fs"
	if deny {
		flagName = "--no-fs"
	}
	reason := strings.TrimSpace(plan.Reason)
	if reason == "" {
		return hostfs.Rule{}, errors.New("reason is required")
	}
	if strings.TrimSpace(plan.RuleID) == "" {
		return hostfs.Rule{}, errors.New("ruleId is required")
	}
	if hostfs.RuleIDExists(config, plan.RuleID) {
		return hostfs.Rule{}, fmt.Errorf("profile HostFS rule %q already exists", plan.RuleID)
	}
	rule, err := hostfs.ParseRuleSpec(flagName, plan.Rule, reason)
	if err != nil {
		return hostfs.Rule{}, err
	}
	rule.ID = strings.TrimSpace(plan.RuleID)
	rule.Reason = reason
	if plan.PlannedRule != nil && strings.TrimSpace(plan.PlannedRule.CreatedAt) != "" {
		createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(plan.PlannedRule.CreatedAt))
		if err != nil {
			return hostfs.Rule{}, fmt.Errorf("invalid planned rule createdAt: %w", err)
		}
		createdAt = createdAt.UTC()
		rule.CreatedAt = &createdAt
	} else {
		now := time.Now().UTC()
		rule.CreatedAt = &now
	}
	return rule, nil
}

func profileHostFSRuleSummaries(rules []hostfs.Rule, deny bool) []ProfileHostFSRuleSummary {
	out := make([]ProfileHostFSRuleSummary, 0, len(rules))
	for _, rule := range rules {
		out = append(out, profileHostFSRuleSummary(rule, deny))
	}
	return out
}

func profileHostFSRuleSummary(rule hostfs.Rule, deny bool) ProfileHostFSRuleSummary {
	effect := "allow"
	if deny {
		effect = "deny"
	}
	createdAt := ""
	if rule.CreatedAt != nil {
		createdAt = rule.CreatedAt.Format(time.RFC3339Nano)
	}
	return ProfileHostFSRuleSummary{
		ID:        rule.ID,
		Effect:    effect,
		HostPath:  rule.HostPath,
		Ops:       append([]hostfs.Op(nil), rule.Ops...),
		Overlay:   rule.Overlay,
		Scope:     rule.Scope,
		Reason:    rule.Reason,
		CreatedAt: createdAt,
	}
}

func removeProfileHostFSRule(rules []hostfs.Rule, id string, deny bool) ([]hostfs.Rule, *ProfileHostFSRuleSummary) {
	for i, rule := range rules {
		if rule.ID != id {
			continue
		}
		removed := profileHostFSRuleSummary(rule, deny)
		out := append([]hostfs.Rule(nil), rules[:i]...)
		out = append(out, rules[i+1:]...)
		return out, &removed
	}
	return rules, nil
}
