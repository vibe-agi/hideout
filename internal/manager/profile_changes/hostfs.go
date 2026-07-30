package profilechanges

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
)

type hostFSValue struct {
	Operation string `json:"operation"`
	Rule      string `json:"rule,omitempty"`
	RuleID    string `json:"ruleId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func normalizeHostFS(raw json.RawMessage) (hostFSValue, error) {
	var value hostFSValue
	if err := decodeStrict(raw, &value); err != nil {
		return hostFSValue{}, err
	}
	switch value.Operation {
	case "add", "deny":
		if strings.TrimSpace(value.Rule) != value.Rule ||
			value.Rule == "" ||
			len(value.Rule) > 4096 ||
			strings.TrimSpace(value.Reason) != value.Reason ||
			value.Reason == "" ||
			len(value.Reason) > 1024 ||
			containsControl(value.Rule) ||
			containsControl(value.Reason) ||
			value.RuleID != "" {
			return hostFSValue{}, errors.New(
				"HostFS add/deny requires only a bounded rule and reason",
			)
		}
		flag := "--fs"
		if value.Operation == "deny" {
			flag = "--no-fs"
		}
		if _, err := hostfs.ParseRuleSpec(
			flag,
			value.Rule,
			value.Reason,
		); err != nil {
			return hostFSValue{}, err
		}
	case "remove":
		if strings.TrimSpace(value.RuleID) != value.RuleID ||
			value.RuleID == "" ||
			len(value.RuleID) > 128 ||
			containsControl(value.RuleID) ||
			value.Rule != "" ||
			value.Reason != "" {
			return hostFSValue{}, errors.New(
				"HostFS remove requires only ruleId",
			)
		}
	default:
		return hostFSValue{}, errors.New(
			"HostFS operation must be add, deny, or remove",
		)
	}
	return value, nil
}

func applyHostFS(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, []Warning, error) {
	value, err := normalizeHostFS(raw)
	if err != nil {
		return nil, nil, err
	}
	switch value.Operation {
	case "add", "deny":
		flag := "--fs"
		target := &desired.HostFS.Grants
		effect := "allow"
		if value.Operation == "deny" {
			flag = "--no-fs"
			target = &desired.HostFS.Deny
			effect = "deny"
		}
		rule, err := hostfs.ParseRuleSpec(
			flag,
			value.Rule,
			value.Reason,
		)
		if err != nil {
			return nil, nil, err
		}
		rule.ID, err = deterministicHostFSRuleID(
			desired.HostFS,
			value,
		)
		if err != nil {
			return nil, nil, err
		}
		rule.Reason = value.Reason
		*target = append(*target, rule)
		diff := []Diff{{
			Kind:   KindProfileHostFS,
			Field:  "hostfs." + effect + "." + rule.ID,
			Before: "absent",
			After:  effect + " " + value.Rule,
			Scope:  "new-sessions",
		}}
		var warnings []Warning
		if value.Operation == "add" {
			warnings = append(warnings, Warning{
				Code: "hostfs-authority-expanded",
				Summary: "The new HostFS allow rule expands host file " +
					"authority for future sessions.",
			})
		}
		return diff, warnings, nil
	case "remove":
		var (
			removed hostfs.Rule
			found   bool
		)
		desired.HostFS.Grants, removed, found = removeHostFSRule(
			desired.HostFS.Grants,
			value.RuleID,
		)
		effect := "allow"
		if !found {
			desired.HostFS.Deny, removed, found = removeHostFSRule(
				desired.HostFS.Deny,
				value.RuleID,
			)
			effect = "deny"
		}
		if !found {
			return nil, nil, fmt.Errorf(
				"HostFS rule %q is not configured",
				value.RuleID,
			)
		}
		return []Diff{{
			Kind:   KindProfileHostFS,
			Field:  "hostfs." + effect + "." + value.RuleID,
			Before: effect + " " + removed.HostPath,
			After:  "absent",
			Scope:  "new-sessions",
		}}, nil, nil
	default:
		return nil, nil, errors.New("unsupported HostFS operation")
	}
}

func deterministicHostFSRuleID(
	config hostfs.Config,
	value hostFSValue,
) (string, error) {
	seed, err := json.Marshal(struct {
		Config hostfs.Config
		Value  hostFSValue
	}{
		Config: config,
		Value:  value,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(seed)
	id := "hfs_" + hex.EncodeToString(sum[:6])
	if hostfs.RuleIDExists(config, id) {
		return "", fmt.Errorf(
			"deterministic HostFS rule id %q already exists",
			id,
		)
	}
	return id, nil
}

func removeHostFSRule(
	rules []hostfs.Rule,
	id string,
) ([]hostfs.Rule, hostfs.Rule, bool) {
	for index, rule := range rules {
		if rule.ID != id {
			continue
		}
		out := append([]hostfs.Rule(nil), rules[:index]...)
		out = append(out, rules[index+1:]...)
		return out, rule, true
	}
	return rules, hostfs.Rule{}, false
}
