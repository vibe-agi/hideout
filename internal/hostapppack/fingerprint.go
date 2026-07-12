package hostapppack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

type PermissionItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type PermissionChange struct {
	Key    string `json:"key"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type PermissionDiff struct {
	Added        []PermissionItem   `json:"added"`
	Removed      []PermissionItem   `json:"removed"`
	Changed      []PermissionChange `json:"changed"`
	TotalChanges int                `json:"totalChanges"`
	Truncated    bool               `json:"truncated"`
}

const MaxPermissionDiffEntries = 128

func PermissionFingerprint(manifest Manifest) (string, error) {
	return BasePermissionFingerprint(manifest)
}

func BasePermissionFingerprint(manifest Manifest) (string, error) {
	if err := ValidateManifest(manifest); err != nil {
		return "", err
	}
	return digestPermissionItems(PermissionItems(manifest))
}

func EffectivePermissionFingerprint(manifest Manifest, context EffectivePermissionContext) (string, error) {
	items, err := EffectivePermissionItems(manifest, context)
	if err != nil {
		return "", err
	}
	return digestPermissionItems(items)
}

func EffectivePermissionItems(manifest Manifest, context EffectivePermissionContext) ([]PermissionItem, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	if err := validateEffectivePermissionContext(context); err != nil {
		return nil, err
	}
	bindingIDs, replacements, err := effectiveEnablementAuthority(manifest, context)
	if err != nil {
		return nil, err
	}
	items := append(PermissionItems(manifest),
		PermissionItem{Key: "core/access", Value: context.Access},
		PermissionItem{Key: "core/safety-profile/id", Value: context.SafetyProfileID},
		PermissionItem{Key: "core/safety-profile/version", Value: context.SafetyProfileVersion},
	)
	for _, bindingID := range bindingIDs {
		items = append(items, PermissionItem{Key: "core/enabled-binding", Value: bindingID})
	}
	commands := make([]string, 0, len(replacements))
	for command := range replacements {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, command := range commands {
		items = append(items, PermissionItem{Key: "core/command-replacement/" + command, Value: replacements[command]})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Key == items[j].Key {
			return items[i].Value < items[j].Value
		}
		return items[i].Key < items[j].Key
	})
	return items, nil
}

func effectiveEnablementAuthority(manifest Manifest, context EffectivePermissionContext) ([]string, map[string]string, error) {
	available := make(map[string]BindingSpec, len(manifest.Bindings))
	for _, binding := range manifest.Bindings {
		available[binding.ID] = binding
	}
	bindingIDs := append([]string(nil), context.BindingIDs...)
	if len(bindingIDs) == 0 {
		for _, binding := range manifest.Bindings {
			bindingIDs = append(bindingIDs, binding.ID)
		}
	}
	sort.Strings(bindingIDs)
	selectedCommands := map[string]bool{}
	for i, bindingID := range bindingIDs {
		if i > 0 && bindingIDs[i-1] == bindingID {
			return nil, nil, fmt.Errorf("effective permission binding %q is duplicated", bindingID)
		}
		binding, ok := available[bindingID]
		if !ok {
			return nil, nil, fmt.Errorf("effective permission binding %q is not in the manifest", bindingID)
		}
		for _, command := range binding.Commands {
			selectedCommands[command] = true
		}
	}
	replacements := make(map[string]string, len(context.ConflictReplacements))
	for command, owner := range context.ConflictReplacements {
		if !selectedCommands[command] {
			return nil, nil, fmt.Errorf("command replacement %q is outside the enabled bindings", command)
		}
		if err := validateText("command replacement owner", owner, 1, 384); err != nil {
			return nil, nil, err
		}
		replacements[command] = owner
	}
	return bindingIDs, replacements, nil
}

func DiffEffectivePermissions(before Manifest, beforeContext EffectivePermissionContext, after Manifest, afterContext EffectivePermissionContext) (PermissionDiff, error) {
	beforeItems, err := EffectivePermissionItems(before, beforeContext)
	if err != nil {
		return PermissionDiff{}, err
	}
	afterItems, err := EffectivePermissionItems(after, afterContext)
	if err != nil {
		return PermissionDiff{}, err
	}
	return diffPermissionItems(beforeItems, afterItems), nil
}

func digestPermissionItems(items []PermissionItem) (string, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func PermissionItems(manifest Manifest) []PermissionItem {
	manifest = NormalizeManifest(manifest)
	items := []PermissionItem{{Key: "pack/id", Value: manifest.ID}}
	for _, app := range manifest.Apps {
		prefix := "apps/" + app.ID + "/"
		items = append(items,
			PermissionItem{Key: prefix + "id", Value: app.ID},
			PermissionItem{Key: prefix + "qualified-app", Value: manifest.ID + "/" + app.ID},
			PermissionItem{Key: prefix + "executable-relative-path", Value: app.ExecutableRelativePath},
			PermissionItem{Key: prefix + "expected-bundle-id", Value: app.ExpectedBundleID},
			PermissionItem{Key: prefix + "expected-team-id", Value: app.ExpectedTeamID},
			PermissionItem{Key: prefix + "safety-profile", Value: app.RequestedSafetyProfile},
			PermissionItem{Key: prefix + "launch/goto", Value: app.Launch.GotoFlag},
			PermissionItem{Key: prefix + "launch/new-window", Value: app.Launch.NewWindowFlag},
			PermissionItem{Key: prefix + "launch/reuse-window", Value: app.Launch.ReuseWindowFlag},
			PermissionItem{Key: prefix + "launch/goto-separator", Value: app.Launch.GotoSeparator},
		)
		for _, platform := range app.Platforms {
			items = append(items, PermissionItem{Key: prefix + "platforms", Value: platform})
		}
		for _, bundle := range app.BundleNames {
			items = append(items, PermissionItem{Key: prefix + "bundle-names", Value: bundle})
		}
	}
	for _, binding := range manifest.Bindings {
		prefix := "bindings/" + binding.ID + "/"
		items = append(items,
			PermissionItem{Key: prefix + "id", Value: binding.ID},
			PermissionItem{Key: prefix + "app", Value: manifest.ID + "/" + binding.AppID},
			PermissionItem{Key: prefix + "capability", Value: binding.CapabilityID},
			PermissionItem{Key: prefix + "result-policy", Value: binding.ResultPolicy},
			PermissionItem{Key: prefix + "host-data-return", Value: strconv.FormatBool(binding.ResultPolicy != ResultNone)},
			PermissionItem{Key: prefix + "access", Value: binding.RequestedAccess},
			PermissionItem{Key: prefix + "grammar/kind", Value: binding.Grammar.Kind},
			PermissionItem{Key: prefix + "grammar/count", Value: strconv.Itoa(binding.Grammar.ResourceCount)},
			PermissionItem{Key: prefix + "grammar/unknown", Value: binding.Grammar.UnknownFlags},
		)
		for _, command := range binding.Commands {
			items = append(items, PermissionItem{Key: prefix + "commands", Value: command})
		}
		for _, resource := range binding.ResourceKinds {
			items = append(items, PermissionItem{Key: prefix + "resource-kinds", Value: resource})
		}
		for _, flag := range binding.Grammar.GotoFlags {
			items = append(items, PermissionItem{Key: prefix + "grammar/goto-flags", Value: flag})
		}
		for _, flag := range binding.Grammar.NewWindowFlags {
			items = append(items, PermissionItem{Key: prefix + "grammar/new-window-flags", Value: flag})
		}
		for _, flag := range binding.Grammar.ReuseWindowFlags {
			items = append(items, PermissionItem{Key: prefix + "grammar/reuse-window-flags", Value: flag})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Key == items[j].Key {
			return items[i].Value < items[j].Value
		}
		return items[i].Key < items[j].Key
	})
	return items
}

func validateEffectivePermissionContext(context EffectivePermissionContext) error {
	switch context.Access {
	case AccessSafe:
		if err := validateLocalID(context.SafetyProfileID); err != nil {
			return fmt.Errorf("safe permission context requires a valid safety profile id: %w", err)
		}
		if err := validateText("safety profile version", context.SafetyProfileVersion, 1, MaxVersionBytes); err != nil {
			return err
		}
	case AccessAskEachRun:
		if context.SafetyProfileID != "" || context.SafetyProfileVersion != "" {
			return errors.New("ask-each-run permission context must not claim a selected safety profile")
		}
	default:
		return fmt.Errorf("permission context access %q is unsupported", context.Access)
	}
	return nil
}

func DiffPermissions(before, after Manifest) PermissionDiff {
	return diffPermissionItems(PermissionItems(before), PermissionItems(after))
}

func diffPermissionItems(beforeItems, afterItems []PermissionItem) PermissionDiff {
	beforePairs := permissionPairSet(beforeItems)
	afterPairs := permissionPairSet(afterItems)
	diff := PermissionDiff{}
	for _, item := range afterItems {
		if _, exists := beforePairs[permissionPair(item)]; !exists {
			diff.Added = append(diff.Added, item)
		}
	}
	for _, item := range beforeItems {
		if _, exists := afterPairs[permissionPair(item)]; !exists {
			diff.Removed = append(diff.Removed, item)
		}
	}
	beforeByKey := valuesByKey(beforeItems)
	afterByKey := valuesByKey(afterItems)
	for key, beforeValues := range beforeByKey {
		afterValues, exists := afterByKey[key]
		if !exists || len(beforeValues) != 1 || len(afterValues) != 1 || beforeValues[0] == afterValues[0] {
			continue
		}
		diff.Changed = append(diff.Changed, PermissionChange{Key: key, Before: beforeValues[0], After: afterValues[0]})
	}
	sort.Slice(diff.Changed, func(i, j int) bool { return diff.Changed[i].Key < diff.Changed[j].Key })
	return boundPermissionDiff(diff)
}

func boundPermissionDiff(diff PermissionDiff) PermissionDiff {
	diff.TotalChanges = len(diff.Changed) + len(diff.Added) + len(diff.Removed)
	remaining := MaxPermissionDiffEntries
	if len(diff.Changed) > remaining {
		diff.Changed = diff.Changed[:remaining]
		diff.Added = nil
		diff.Removed = nil
		diff.Truncated = true
		return diff
	}
	remaining -= len(diff.Changed)
	if len(diff.Added) > remaining {
		diff.Added = diff.Added[:remaining]
		diff.Removed = nil
		diff.Truncated = true
		return diff
	}
	remaining -= len(diff.Added)
	if len(diff.Removed) > remaining {
		diff.Removed = diff.Removed[:remaining]
		diff.Truncated = true
	}
	return diff
}

func permissionPairSet(items []PermissionItem) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[permissionPair(item)] = struct{}{}
	}
	return out
}

func permissionPair(item PermissionItem) string {
	return fmt.Sprintf("%d:%s%d:%s", len(item.Key), item.Key, len(item.Value), item.Value)
}

func valuesByKey(items []PermissionItem) map[string][]string {
	out := map[string][]string{}
	for _, item := range items {
		out[item.Key] = append(out[item.Key], item.Value)
	}
	return out
}
