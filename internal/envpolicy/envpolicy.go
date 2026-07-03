package envpolicy

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
)

const defaultToolPath = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

type Result struct {
	Env       []string
	Denied    []string
	Inherited []string
	Synthetic map[string]string
}

type Spec struct {
	Profile    profile.Profile
	ProfileDir string
	SessionDir string
	ShimDir    string
	HostEnv    []string
}

func Build(spec Spec) Result {
	host := spec.HostEnv
	if host == nil {
		host = os.Environ()
	}
	deny := map[string]bool{}
	for _, kv := range host {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || isHardBlockedEnv(name) || matchesAny(name, spec.Profile.Env.Deny) {
			if ok {
				deny[name] = true
			}
			continue
		}
		if contains(spec.Profile.Env.Inherit, name) {
			deny[name] = false
			_ = value
		}
	}

	out := map[string]string{}
	inherited := []string{}
	for _, kv := range host {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || deny[name] || isHardBlockedEnv(name) || matchesAny(name, spec.Profile.Env.Deny) {
			continue
		}
		if contains(spec.Profile.Env.Inherit, name) {
			out[name] = value
			inherited = append(inherited, name)
		}
	}

	synthetic := map[string]string{
		"HOME":              filepath.Join(spec.ProfileDir, "home"),
		"USER":              spec.Profile.Identity.User,
		"LOGNAME":           spec.Profile.Identity.User,
		"HOSTNAME":          spec.Profile.Identity.Hostname,
		"TMPDIR":            filepath.Join(spec.SessionDir, "tmp"),
		"XDG_CONFIG_HOME":   filepath.Join(spec.ProfileDir, "config"),
		"XDG_CACHE_HOME":    filepath.Join(spec.ProfileDir, "cache"),
		"XDG_DATA_HOME":     filepath.Join(spec.ProfileDir, "data"),
		"GIT_CONFIG_GLOBAL": filepath.Join(spec.ProfileDir, "home", ".gitconfig"),
		"TZ":                spec.Profile.Identity.Timezone,
		"LANG":              spec.Profile.Identity.Locale,
		"LC_ALL":            spec.Profile.Identity.Locale,
		"PATH":              defaultToolPath,
	}
	if spec.ShimDir != "" {
		synthetic["PATH"] = spec.ShimDir + ":" + defaultToolPath
	}
	for k, v := range spec.Profile.Env.Public {
		if !isHardBlockedEnv(k) && !matchesAny(k, spec.Profile.Env.Deny) {
			out[k] = v
		}
	}
	for k, v := range synthetic {
		out[k] = v
	}

	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+out[k])
	}

	deniedSet := map[string]bool{}
	for k, v := range deny {
		if v || matchesAny(k, spec.Profile.Env.Deny) {
			deniedSet[observedDeniedName(k)] = true
		}
	}
	denied := make([]string, 0, len(deniedSet))
	for k := range deniedSet {
		denied = append(denied, k)
	}
	sort.Strings(denied)
	sort.Strings(inherited)

	return Result{Env: env, Denied: denied, Inherited: inherited, Synthetic: synthetic}
}

func observedDeniedName(name string) string {
	if strings.HasPrefix(name, "HIDEOUT_") {
		return "HIDEOUT_*"
	}
	return name
}

func isHardBlockedEnv(name string) bool {
	return isBlockedProxyEnv(name) || strings.HasPrefix(name, "HIDEOUT_")
}

func isBlockedProxyEnv(name string) bool {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy":
		return true
	default:
		return false
	}
}

func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "*") && strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
			return true
		}
		if p == name {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
