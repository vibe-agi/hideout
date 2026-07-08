package privilege

import (
	"regexp"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
)

var setupSecretRE = regexp.MustCompile(`(?i)\b(?:setupPrivateKey|setupToken|setupCredential|rootControlSSH|rootControlSSHConfig)\s*[:=]?[^\n]*`)

func StatusDetails(status Status) map[string]any {
	details := map[string]any{
		"status":                   string(status.Status),
		"reason":                   status.Reason,
		"guidance":                 status.Guidance,
		"target.uid":               status.Target.UID,
		"target.sudoN":             string(status.Target.SudoN.Status),
		"target.absoluteSudoN":     string(status.Target.AbsoluteSudoN.Status),
		"setup.kind":               string(status.Setup.Kind),
		"setup.separateFromTarget": status.Setup.SeparateFromTarget,
		"nonClaim":                 NonClaim(status.Status),
		"checks":                   checkDetails(status.Checks),
	}
	if status.Target.UID == nil {
		details["target.uid"] = "unknown"
	}
	return audit.RedactDetails(details)
}

func checkDetails(checks []CheckResult) []map[string]any {
	out := make([]map[string]any, 0, len(checks))
	for _, check := range checks {
		detail := map[string]any{
			"name":   string(check.Name),
			"status": string(check.Status),
		}
		if check.Observed != "" {
			detail["observed"] = redactSetupString(check.Observed)
		}
		if check.Error != "" {
			detail["error"] = redactSetupString(check.Error)
		}
		if !check.CheckedAt.IsZero() {
			detail["checkedAt"] = check.CheckedAt
		}
		out = append(out, detail)
	}
	return out
}

func redactSetupString(s string) string {
	s = setupSecretRE.ReplaceAllString(s, "setupCredential=REDACTED")
	return audit.RedactString(s)
}

func PrivilegedSetupDetails(category, status string, setup SetupIdentity, reason string) map[string]any {
	return audit.RedactDetails(map[string]any{
		"category":           category,
		"status":             status,
		"setupIdentityKind":  string(setup.Kind),
		"separateFromTarget": setup.SeparateFromTarget,
		"reason":             redactSetupString(reason),
	})
}

func TargetRootAttemptDetails(command string, argv []string, status Status, adapterID, decision, reason string) map[string]any {
	return audit.RedactDetails(map[string]any{
		"command":          command,
		"argvSummary":      summarizeArgv(argv),
		"separationStatus": string(status.Status),
		"adapterId":        adapterID,
		"decision":         decision,
		"reason":           reason,
		"nonClaim":         NonClaim(status.Status),
	})
}

func summarizeArgv(argv []string) []string {
	const max = 8
	out := append([]string(nil), argv...)
	if len(out) > max {
		out = out[:max]
		out = append(out, "...")
	}
	return out
}

func CredentialLocationClass(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return "hideout-control-plane"
}
