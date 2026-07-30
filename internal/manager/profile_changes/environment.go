package profilechanges

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	maxEnvironmentEntries = 256
	maxEnvironmentValue   = 8192
)

var environmentNamePattern = regexp.MustCompile(
	`^[A-Za-z_][A-Za-z0-9_]{0,127}$`,
)

type environmentValue struct {
	Set       map[string]string `json:"set,omitempty"`
	Unset     []string          `json:"unset,omitempty"`
	Inherit   []string          `json:"inherit,omitempty"`
	Uninherit []string          `json:"uninherit,omitempty"`
	Deny      []string          `json:"deny,omitempty"`
	Undeny    []string          `json:"undeny,omitempty"`
}

func normalizeEnvironment(raw json.RawMessage) (environmentValue, error) {
	var value environmentValue
	if err := decodeStrict(raw, &value); err != nil {
		return environmentValue{}, err
	}
	total := len(value.Set) + len(value.Unset) +
		len(value.Inherit) + len(value.Uninherit) +
		len(value.Deny) + len(value.Undeny)
	if total == 0 || total > maxEnvironmentEntries {
		return environmentValue{}, errors.New(
			"environment change must contain bounded operations",
		)
	}
	for name, entry := range value.Set {
		if !environmentNamePattern.MatchString(name) ||
			len(entry) > maxEnvironmentValue ||
			containsControl(entry) {
			return environmentValue{}, fmt.Errorf(
				"environment entry %q is invalid",
				name,
			)
		}
	}
	if err := normalizeEnvironmentNamePair(
		&value.Unset,
		nil,
		"unset",
	); err != nil {
		return environmentValue{}, err
	}
	if err := normalizeEnvironmentNamePair(
		&value.Inherit,
		&value.Uninherit,
		"inherit",
	); err != nil {
		return environmentValue{}, err
	}
	if err := normalizeEnvironmentPatternPair(
		&value.Deny,
		&value.Undeny,
	); err != nil {
		return environmentValue{}, err
	}
	for _, name := range value.Unset {
		if _, exists := value.Set[name]; exists {
			return environmentValue{}, fmt.Errorf(
				"environment name %q is both set and unset",
				name,
			)
		}
	}
	return value, nil
}

func normalizeEnvironmentNamePair(
	add, remove *[]string,
	label string,
) error {
	if add != nil {
		if err := normalizeEnvironmentNames(*add, label); err != nil {
			return err
		}
		sort.Strings(*add)
	}
	if remove != nil {
		if err := normalizeEnvironmentNames(
			*remove,
			"un"+label,
		); err != nil {
			return err
		}
		sort.Strings(*remove)
		if overlap := firstSortedOverlap(*add, *remove); overlap != "" {
			return fmt.Errorf(
				"environment name %q is both %s and un%s",
				overlap,
				label,
				label,
			)
		}
	}
	return nil
}

func normalizeEnvironmentNames(values []string, label string) error {
	seen := make(map[string]struct{}, len(values))
	for _, name := range values {
		if !environmentNamePattern.MatchString(name) {
			return fmt.Errorf(
				"environment %s name %q is invalid",
				label,
				name,
			)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf(
				"environment %s name %q is duplicated",
				label,
				name,
			)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func normalizeEnvironmentPatternPair(
	add, remove *[]string,
) error {
	for label, values := range map[string]*[]string{
		"deny":   add,
		"undeny": remove,
	} {
		seen := make(map[string]struct{}, len(*values))
		for _, pattern := range *values {
			if strings.TrimSpace(pattern) != pattern ||
				len(pattern) == 0 ||
				len(pattern) > 128 ||
				containsControl(pattern) {
				return fmt.Errorf(
					"environment %s pattern is invalid",
					label,
				)
			}
			if _, duplicate := seen[pattern]; duplicate {
				return fmt.Errorf(
					"environment %s pattern %q is duplicated",
					label,
					pattern,
				)
			}
			seen[pattern] = struct{}{}
		}
		sort.Strings(*values)
	}
	if overlap := firstSortedOverlap(*add, *remove); overlap != "" {
		return fmt.Errorf(
			"environment pattern %q is both denied and undenied",
			overlap,
		)
	}
	return nil
}

func applyEnvironment(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, error) {
	value, err := normalizeEnvironment(raw)
	if err != nil {
		return nil, err
	}
	if desired.Env.Public == nil {
		desired.Env.Public = map[string]string{}
	}
	var diff []Diff
	setNames := make([]string, 0, len(value.Set))
	for name := range value.Set {
		setNames = append(setNames, name)
	}
	sort.Strings(setNames)
	for _, name := range setNames {
		_, exists := desired.Env.Public[name]
		desired.Env.Public[name] = value.Set[name]
		diff = append(diff, Diff{
			Kind:   KindProfileEnvironment,
			Field:  "env.public." + name,
			Before: state(exists, "present", "absent"),
			After:  "value provided",
			Scope:  "new-sessions",
		})
	}
	for _, name := range value.Unset {
		_, exists := desired.Env.Public[name]
		delete(desired.Env.Public, name)
		diff = append(diff, Diff{
			Kind:   KindProfileEnvironment,
			Field:  "env.public." + name,
			Before: state(exists, "present", "absent"),
			After:  "absent",
			Scope:  "new-sessions",
		})
	}
	for _, name := range value.Inherit {
		exists := contains(desired.Env.Inherit, name)
		desired.Env.Inherit = appendMissing(desired.Env.Inherit, name)
		diff = append(diff, Diff{
			Kind:   KindProfileEnvironment,
			Field:  "env.inherit." + name,
			Before: state(exists, "enabled", "disabled"),
			After:  "enabled", Scope: "new-sessions",
		})
	}
	for _, name := range value.Uninherit {
		exists := contains(desired.Env.Inherit, name)
		desired.Env.Inherit = remove(desired.Env.Inherit, name)
		diff = append(diff, Diff{
			Kind:   KindProfileEnvironment,
			Field:  "env.inherit." + name,
			Before: state(exists, "enabled", "disabled"),
			After:  "disabled", Scope: "new-sessions",
		})
	}
	for _, pattern := range value.Deny {
		exists := contains(desired.Env.Deny, pattern)
		desired.Env.Deny = appendMissing(desired.Env.Deny, pattern)
		diff = append(diff, Diff{
			Kind:   KindProfileEnvironment,
			Field:  "env.deny." + pattern,
			Before: state(exists, "enabled", "disabled"),
			After:  "enabled", Scope: "new-sessions",
		})
	}
	for _, pattern := range value.Undeny {
		exists := contains(desired.Env.Deny, pattern)
		desired.Env.Deny = remove(desired.Env.Deny, pattern)
		diff = append(diff, Diff{
			Kind:   KindProfileEnvironment,
			Field:  "env.deny." + pattern,
			Before: state(exists, "enabled", "disabled"),
			After:  "disabled", Scope: "new-sessions",
		})
	}
	sort.Strings(desired.Env.Inherit)
	sort.Strings(desired.Env.Deny)
	return diff, nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func firstSortedOverlap(left, right []string) string {
	set := make(map[string]struct{}, len(left))
	for _, value := range left {
		set[value] = struct{}{}
	}
	for _, value := range right {
		if _, exists := set[value]; exists {
			return value
		}
	}
	return ""
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendMissing(values []string, target string) []string {
	if contains(values, target) {
		return values
	}
	return append(values, target)
}

func remove(values []string, target string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}
