package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/operatorintent"
	"github.com/vibe-agi/hideout/internal/profile"
)

// operatorAccess maps the natural allow/deny grammar onto the existing
// Manager profile HostFS transaction adapter. Parsing carries no authority:
// every rule still goes through typed validation and a durable plan/apply
// transaction under the profile mutation lock, exactly like the advanced
// `hideout profile fs` surface.
func (a app) operatorAccess(intent operatorintent.Access) error {
	switch intent.Scope {
	case operatorintent.ScopeProfile:
	case operatorintent.ScopeOnce, operatorintent.ScopeProject:
		return fmt.Errorf("%s is not activated yet: session- and project-scoped access rules need a design ruling recorded in docs/DEBT.md; omit the flag for a durable profile rule or use --for-profile <name>", accessScopeFlag(intent.Scope))
	default:
		return fmt.Errorf("unsupported access scope %q", intent.Scope)
	}
	hostPath, err := resolveOperatorAccessPath(intent.Path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(hostPath)
	if err != nil {
		return fmt.Errorf("access path %s must exist as a plain file or directory; for a not-yet-existing or special path use hideout profile fs <name> add with an explicit selector: %w", hostPath, err)
	}
	selectors, notes, err := operatorAccessSelectors(intent.Operation, hostPath, info)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	adapter := manager.LegacyProfileTransactionAdapter{
		Core: manager.New(store),
	}
	operation := "add"
	effectWord := "allowed"
	if intent.Effect == operatorintent.AccessDeny {
		operation = "deny"
		effectWord = "denied"
	}
	profileName := intent.ProfileName
	if profileName == "" {
		profileName = "default"
	}
	for _, selector := range selectors {
		result, err := adapter.ApplyHostFS(
			context.Background(),
			manager.ProfileHostFSOptions{
				ProfileName: profileName,
				Operation:   operation,
				Rule:        selector,
				Reason:      "operator natural access command",
			},
		)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s %s %s for profile %s (rule %s)\n",
			effectWord, string(intent.Operation), hostPath, profileName, appliedHostFSRuleID(result, selector))
	}
	for _, note := range notes {
		fmt.Fprintln(a.stdout, note)
	}
	return nil
}

func accessScopeFlag(scope operatorintent.AccessScope) string {
	switch scope {
	case operatorintent.ScopeOnce:
		return "--once"
	case operatorintent.ScopeProject:
		return "--for-this-project"
	default:
		return string(scope)
	}
}

func resolveOperatorAccessPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func operatorAccessSelectors(operation operatorintent.AccessOperation, hostPath string, info os.FileInfo) ([]string, []string, error) {
	isDir := info.IsDir()
	if !isDir && !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("access path %s is neither a plain file nor a directory; use hideout profile fs <name> add with an explicit selector", hostPath)
	}
	readSelector := "read:" + hostPath
	writeSelector := "overlay:" + hostPath
	if isDir {
		readSelector = "tree:" + hostPath
		writeSelector = "overlay-tree:" + hostPath
	}
	stagedNote := "note: write access stages changes in a session overlay; applying them to the host still requires your explicit decision approval"
	switch operation {
	case operatorintent.AccessRead:
		return []string{readSelector}, nil, nil
	case operatorintent.AccessWrite:
		return []string{writeSelector}, []string{stagedNote}, nil
	case operatorintent.AccessAll:
		return []string{readSelector, writeSelector}, []string{stagedNote}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported access operation %q", operation)
	}
}

func appliedHostFSRuleID(result manager.ProfileHostFSResult, selector string) string {
	target := strings.TrimPrefix(selector, "overlay-tree:")
	target = strings.TrimPrefix(target, "overlay:")
	target = strings.TrimPrefix(target, "tree:")
	target = strings.TrimPrefix(target, "read:")
	rules := append(append([]manager.ProfileHostFSRuleSummary(nil), result.Grants...), result.Deny...)
	for index := len(rules) - 1; index >= 0; index-- {
		if rules[index].HostPath == target {
			return rules[index].ID
		}
	}
	return "recorded"
}

// operatorHostAppTrust maps `allow|deny host-app <command>` onto Manager's
// trusted host-app grant policy for the current project directory. Grant
// promotes the run-recorded request (the run-accurate app identity) for the
// named command in the workspace the operator is standing in; deny removes it.
// No path is taken from the guest; Core derives the workspace identity from the
// command's working directory.
func (a app) operatorHostAppTrust(intent operatorintent.HostAppTrust) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	profileName := intent.ProfileName
	if profileName == "" {
		profileName = "default"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	core := manager.New(store)
	switch intent.Effect {
	case operatorintent.AccessAllow:
		result, err := core.GrantHostAppTrust(profileName, cwd, intent.Command)
		if err != nil {
			return err
		}
		verb := "allowed"
		if !result.Granted {
			verb = "already allowed"
		}
		fmt.Fprintf(a.stdout, "native host app %q %s for this project under profile %s\n", intent.Command, verb, result.Profile)
		fmt.Fprintf(a.stdout, "it will open natively here; revoke with: hideout deny host-app %s\n", intent.Command)
		return nil
	case operatorintent.AccessDeny:
		if err := core.RevokeHostAppTrust(profileName, cwd, intent.Command); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "native host app %q revoked for this project under profile %s; it now opens in the safe isolated window\n", intent.Command, profileName)
		return nil
	default:
		return fmt.Errorf("unsupported host-app trust effect %q", intent.Effect)
	}
}
