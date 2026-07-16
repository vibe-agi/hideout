package envpolicy

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestBuildDoesNotInheritUnlistedEnvAndSetsSyntheticHome(t *testing.T) {
	p := profile.Default("test")
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		HostEnv: []string{
			"TERM=xterm-256color",
			"HTTP_PROXY=http://user:pass@proxy",
			"SERVICE_TOKEN=secret",
			"PATH=/bin",
		},
	})
	env := strings.Join(result.Env, "\n")
	if strings.Contains(env, "HTTP_PROXY") || strings.Contains(env, "SERVICE_TOKEN") {
		t.Fatalf("sensitive env leaked: %s", env)
	}
	if !strings.Contains(env, "HOME=/tmp/hideout/profile/home") {
		t.Fatalf("synthetic HOME missing: %s", env)
	}
	if !strings.Contains(env, "GIT_CONFIG_GLOBAL=/tmp/hideout/profile/home/.gitconfig") {
		t.Fatalf("synthetic GIT_CONFIG_GLOBAL missing: %s", env)
	}
	if strings.Contains(env, "GIT_OPTIONAL_LOCKS=") {
		t.Fatalf("Git optional-lock policy must retain the tool default: %s", env)
	}
	if !strings.Contains(env, "HOSTNAME=devbox") {
		t.Fatalf("synthetic HOSTNAME missing: %s", env)
	}
	if !strings.Contains(env, "TERM=xterm-256color") {
		t.Fatalf("TERM should be inherited: %s", env)
	}
}

func TestBuildDenyPatternsSupportGeneralGlobs(t *testing.T) {
	p := profile.Default("test")
	p.Env.Deny = []string{"*TOKEN*", "*SECRET*"}
	p.Env.Inherit = append(p.Env.Inherit, "SERVICE_TOKEN", "MY_SECRET_VALUE", "SAFE_PUBLIC")
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		HostEnv: []string{
			"SERVICE_TOKEN=secret",
			"MY_SECRET_VALUE=secret",
			"SAFE_PUBLIC=ok",
		},
	})
	env := strings.Join(result.Env, "\n")
	if strings.Contains(env, "SERVICE_TOKEN=") || strings.Contains(env, "MY_SECRET_VALUE=") {
		t.Fatalf("general deny glob leaked sensitive env: %s", env)
	}
	if !strings.Contains(env, "SAFE_PUBLIC=ok") {
		t.Fatalf("non-sensitive inherited env missing: %s", env)
	}
}

func TestBuildAllowsUserInheritedBusinessEnv(t *testing.T) {
	p := profile.Default("test")
	p.Env.Inherit = append(p.Env.Inherit, "SERVICE_TOKEN")
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		HostEnv: []string{
			"SERVICE_TOKEN=user-approved",
		},
	})
	env := strings.Join(result.Env, "\n")
	if !strings.Contains(env, "SERVICE_TOKEN=user-approved") {
		t.Fatalf("user-approved business env should be inherited: %s", env)
	}
}

func TestBuildSyntheticIdentityOverridesPublicEnv(t *testing.T) {
	p := profile.Default("test")
	p.Env.Public["HOME"] = "/real/home"
	p.Env.Public["HOSTNAME"] = "real-host"
	p.Env.Public["GIT_CONFIG_GLOBAL"] = "/real/home/.gitconfig"
	p.Env.Public["GIT_OPTIONAL_LOCKS"] = "1"
	p.Env.Public["PATH"] = "/real/bin"
	p.Env.Inherit = append(p.Env.Inherit, "PATH")
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		HostEnv:    []string{"PATH=/host/bin"},
	})
	env := strings.Join(result.Env, "\n")
	if strings.Contains(env, "HOME=/real/home") || strings.Contains(env, "HOSTNAME=real-host") || strings.Contains(env, "GIT_CONFIG_GLOBAL=/real/home/.gitconfig") || strings.Contains(env, "PATH=/real/bin") || strings.Contains(env, "PATH=/host/bin") {
		t.Fatalf("public env overrode synthetic identity: %s", env)
	}
	if !strings.Contains(env, "HOME=/tmp/hideout/profile/home") || !strings.Contains(env, "HOSTNAME=devbox") || !strings.Contains(env, "GIT_CONFIG_GLOBAL=/tmp/hideout/profile/home/.gitconfig") {
		t.Fatalf("synthetic identity missing: %s", env)
	}
	if !strings.Contains(env, "GIT_OPTIONAL_LOCKS=1") {
		t.Fatalf("explicit Git optional-lock policy missing: %s", env)
	}
	if !strings.Contains(env, "PATH="+defaultToolPath) {
		t.Fatalf("synthetic PATH missing without shim dir: %s", env)
	}
}

func TestBuildSyntheticPATHPrefixesShimDir(t *testing.T) {
	p := profile.Default("test")
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		ShimDir:    "/tmp/hideout/session/shims",
		HostEnv:    []string{"PATH=/host/bin"},
	})
	env := strings.Join(result.Env, "\n")
	want := "PATH=/tmp/hideout/session/shims:" + defaultToolPath
	if !strings.Contains(env, want) {
		t.Fatalf("synthetic PATH should prefix shim dir, want %q in %s", want, env)
	}
	if strings.Contains(env, "PATH=/host/bin") {
		t.Fatalf("host PATH leaked: %s", env)
	}
}

func TestBuildPinsGitSafeDirectoryToMountedWorkspace(t *testing.T) {
	p := profile.Default("test")
	p.Env.Public["GIT_CONFIG_COUNT"] = "99"
	p.Env.Public["GIT_CONFIG_VALUE_0"] = "*"
	p.Env.Public["GIT_CONFIG_PARAMETERS"] = "'safe.directory=*'"
	result := Build(Spec{
		Profile:          p,
		ProfileDir:       "/tmp/hideout/profile",
		SessionDir:       "/tmp/hideout/session",
		GitSafeDirectory: "/workspace",
	})
	env := strings.Join(result.Env, "\n")
	for _, want := range []string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=/workspace",
		"GIT_CONFIG_KEY_1=safe.directory",
		"GIT_CONFIG_VALUE_1=/workspace/*",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("workspace Git trust missing %q from %s", want, env)
		}
	}
	if strings.Contains(env, "GIT_CONFIG_COUNT=99") || strings.Contains(env, "GIT_CONFIG_VALUE_0=*") || strings.Contains(env, "GIT_CONFIG_PARAMETERS") {
		t.Fatalf("profile overrode Core-owned Git trust: %s", env)
	}
}

func TestBuildHardBlocksProxyEnvEvenWhenProfileAllowsIt(t *testing.T) {
	p := profile.Default("test")
	p.Env.Deny = []string{}
	p.Env.Inherit = append(p.Env.Inherit, "HTTP_PROXY", "ALL_PROXY", "all_proxy")
	p.Env.Public["HTTPS_PROXY"] = "http://public-proxy"
	p.Env.Public["ftp_proxy"] = "http://ftp-proxy"
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		HostEnv: []string{
			"HTTP_PROXY=http://host-proxy",
			"ALL_PROXY=socks5://host-proxy",
			"all_proxy=socks5://lower-host-proxy",
		},
	})
	env := strings.Join(result.Env, "\n")
	for _, blocked := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "all_proxy", "ftp_proxy"} {
		if strings.Contains(env, blocked+"=") {
			t.Fatalf("proxy env %s leaked despite hard block: %s", blocked, env)
		}
	}
	if strings.Contains(strings.Join(result.Inherited, "\n"), "PROXY") || strings.Contains(strings.Join(result.Inherited, "\n"), "proxy") {
		t.Fatalf("proxy env should not be reported as inherited: %+v", result.Inherited)
	}
}

func TestBuildHardBlocksHideoutRuntimeEnvEvenWhenProfileAllowsIt(t *testing.T) {
	p := profile.Default("test")
	p.Env.Deny = []string{}
	p.Env.Inherit = append(p.Env.Inherit, "HIDEOUT_SECRET_DEFAULT_PROXY", "HIDEOUT_CAPABILITY_TOKEN", "HIDEOUT_BROKER_ENDPOINT")
	p.Env.Public["HIDEOUT_SECRET_PUBLIC"] = "secret"
	p.Env.Public["HIDEOUT_SESSION_ID"] = "ses_fake"
	result := Build(Spec{
		Profile:    p,
		ProfileDir: "/tmp/hideout/profile",
		SessionDir: "/tmp/hideout/session",
		HostEnv: []string{
			"HIDEOUT_SECRET_DEFAULT_PROXY=http://user:pass@proxy",
			"HIDEOUT_CAPABILITY_TOKEN=cap_fake",
			"HIDEOUT_BROKER_ENDPOINT=unix:///tmp/fake.sock",
		},
	})
	env := strings.Join(result.Env, "\n")
	if strings.Contains(env, "HIDEOUT_") {
		t.Fatalf("hideout runtime env leaked despite hard block: %s", env)
	}
	if strings.Contains(strings.Join(result.Inherited, "\n"), "HIDEOUT_") {
		t.Fatalf("hideout runtime env should not be reported as inherited: %+v", result.Inherited)
	}
	denied := strings.Join(result.Denied, "\n")
	for _, leaked := range []string{"HIDEOUT_SECRET_DEFAULT_PROXY", "HIDEOUT_CAPABILITY_TOKEN", "HIDEOUT_BROKER_ENDPOINT"} {
		if strings.Contains(denied, leaked) {
			t.Fatalf("denied env should not expose hideout runtime env %s: %+v", leaked, result.Denied)
		}
	}
	if !strings.Contains(denied, "HIDEOUT_*") {
		t.Fatalf("denied env should report hideout namespace generically: %+v", result.Denied)
	}
}
