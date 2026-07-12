package hostapppack

import (
	"path"
	"strings"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
)

// Evidence builds path-free lifecycle evidence from Core-validated plan facts.
// Package description/hints and source locations are intentionally omitted.
func Evidence(operation, decision, profile, packID, revisionID, sourceDigest, permissionFingerprint, identityDigest, reason string) audit.Event {
	operation = strings.TrimSuffix(strings.TrimSpace(operation), "-failed")
	action := "host.app." + operation
	if operation == "add" || operation == "add-install-only" {
		action = "host.app.install"
	}
	details := map[string]any{}
	for key, value := range map[string]string{
		"packId": packID, "revisionId": revisionID, "sourceDigest": sourceDigest,
		"permissionFingerprint": permissionFingerprint, "observedIdentityDigest": identityDigest,
	} {
		if value != "" {
			details[key] = value
		}
	}
	if strings.TrimSpace(reason) != "" {
		// Failure prose can contain provider paths, repository credentials, raw
		// argv, or attacker-controlled package text. Persistent evidence keeps
		// the typed recovery code at the caller and a stable summary only.
		details["reason"] = "host-app lifecycle operation failed"
	}
	return audit.Event{Profile: profile, Backend: "native", Action: action, Decision: decision, Details: audit.RedactDetails(details)}
}

// OpenResourceEvidence returns the public/audit-safe resource facts shared by
// broker launch and refusal events. It accepts only a Core-derived class and a
// bounded relative target; lower host paths and portal/provider credentials
// are omitted rather than transformed into misleading evidence.
func OpenResourceEvidence(resourceClass, relativeTarget string) map[string]any {
	details := map[string]any{}
	switch resourceClass {
	case ResourceWorkspace:
		details["resourceClass"] = ResourceWorkspace
		details["workspaceWritable"] = true
	case ResourceHostFSPortal:
		details["resourceClass"] = ResourceHostFSPortal
	default:
		return details
	}
	if target, ok := safeOpenResourceRelativeTarget(resourceClass, relativeTarget); ok {
		details["relativeTarget"] = target
	}
	return details
}

func safeOpenResourceRelativeTarget(resourceClass, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]byte(value)) > 512 || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || strings.Contains(value, ":") {
		return "", false
	}
	clean := path.Clean(value)
	if clean != value || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "hideout/hostfs/") {
		return "", false
	}
	if resourceClass == ResourceHostFSPortal && (clean == "." || strings.Contains(clean, "/")) {
		return "", false
	}
	for _, r := range clean {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return "", false
		}
	}
	lower := strings.ToLower(clean)
	if strings.Contains(lower, "cap_") || strings.Contains(lower, "claim_") || strings.Contains(lower, "provider-token") || strings.Contains(lower, "portal-token") {
		return "", false
	}
	redacted := audit.RedactDetails(map[string]any{"relativeTarget": clean})
	redactedTarget, ok := redacted["relativeTarget"].(string)
	if !ok || redactedTarget != clean {
		return "", false
	}
	return clean, true
}
