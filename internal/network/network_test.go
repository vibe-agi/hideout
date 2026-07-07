package network

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestPrepareDirectWritesManifest(t *testing.T) {
	p := profile.Default("test")
	plan, err := Prepare(Spec{Profile: p, SessionDir: t.TempDir(), TargetEnv: []string{"HOME=/hideout/profile/home"}})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.Mode != ModeDirect || !plan.Verified || plan.FailClosed {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.DNSPolicy == "" {
		t.Fatalf("direct plan should describe DNS policy: %+v", plan)
	}
	if _, err := os.Stat(plan.ManifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	bootstrap, err := os.ReadFile(plan.BootstrapPath)
	if err != nil {
		t.Fatalf("bootstrap missing: %v", err)
	}
	if !strings.Contains(string(bootstrap), "direct network mode") {
		t.Fatalf("direct bootstrap missing status: %s", bootstrap)
	}
	assertShellSyntaxNetworkTest(t, plan.BootstrapPath)
	manifest, err := os.ReadFile(plan.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"guestBootstrapPath": "/hideout/session/network/bootstrap.sh"`) {
		t.Fatalf("manifest missing guest bootstrap path: %s", manifest)
	}
	if !strings.Contains(string(manifest), `"guestCleanupPath": "/hideout/session/network/cleanup.sh"`) {
		t.Fatalf("manifest missing guest cleanup path: %s", manifest)
	}
	if !strings.Contains(string(manifest), `"dnsPolicy": "guest default resolver over direct route"`) {
		t.Fatalf("manifest missing DNS policy: %s", manifest)
	}
	cleanup, err := os.ReadFile(plan.CleanupPath)
	if err != nil {
		t.Fatalf("cleanup missing: %v", err)
	}
	if !strings.Contains(string(cleanup), "network cleanup complete") {
		t.Fatalf("direct cleanup mismatch: %s", cleanup)
	}
	assertShellSyntaxNetworkTest(t, plan.CleanupPath)
}

func TestPrepareRejectsProxyEnvLeak(t *testing.T) {
	p := profile.Default("test")
	_, err := Prepare(Spec{Profile: p, SessionDir: t.TempDir(), TargetEnv: []string{"HTTP_PROXY=http://proxy"}})
	if err == nil || !strings.Contains(err.Error(), "proxy variables") {
		t.Fatalf("expected proxy env leak failure, got %v", err)
	}
	_, err = Prepare(Spec{Profile: p, SessionDir: t.TempDir(), TargetEnv: []string{"ALL_PROXY=socks5://proxy"}})
	if err == nil || !strings.Contains(err.Error(), "proxy variables") {
		t.Fatalf("expected all_proxy env leak failure, got %v", err)
	}
}

func TestTun2SocksRequiresSecretRef(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	plan, err := Prepare(Spec{Profile: p, SessionDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "proxySecretRef") {
		t.Fatalf("expected proxySecretRef failure, got plan=%+v err=%v", plan, err)
	}
	if !plan.FailClosed {
		t.Fatalf("expected fail closed plan: %+v", plan)
	}
}

func TestSecretResolverRejectsInvalidSecretRef(t *testing.T) {
	_, err := (EnvSecretResolver{Env: []string{"HIDEOUT_SECRET_DEFAULT=socks5://proxy"}}).Resolve("../")
	if err == nil || !strings.Contains(err.Error(), "secret ref") {
		t.Fatalf("expected invalid secret ref failure, got %v", err)
	}
	if got := SecretEnvName("../"); got != "" {
		t.Fatalf("invalid secret ref should not map to env name, got %q", got)
	}
}

func TestSecretResolverErrorsDoNotExposeBackingEnvName(t *testing.T) {
	for name, env := range map[string][]string{
		"missing": {},
		"empty":   {SecretEnvName("default-proxy") + "="},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (EnvSecretResolver{Env: env}).Resolve("default-proxy")
			if err == nil {
				t.Fatal("expected secret resolver error")
			}
			if !strings.Contains(err.Error(), "secret ref default-proxy") {
				t.Fatalf("resolver error should mention ref name: %v", err)
			}
			if strings.Contains(err.Error(), "HIDEOUT_SECRET_") {
				t.Fatalf("resolver error leaked backing env name: %v", err)
			}
		})
	}
}

func TestTun2SocksWithoutMediatedResolverFailsClosed(t *testing.T) {
	// With the DNS closure enforced (bootstrap DNAT + connected-subnet block), a
	// connected-subnet-only environment is refused: privacy mode requires a
	// mediated resolver reachable through the privacy path.
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	sessionDir := t.TempDir()
	plan, err := Prepare(Spec{
		Profile:       p,
		SessionDir:    sessionDir,
		RuntimeVerify: true,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if err == nil || !plan.FailClosed {
		t.Fatalf("expected fail closed without a mediated resolver, got plan=%+v err=%v", plan, err)
	}
	if !strings.Contains(plan.Reason, "mediated resolver") {
		t.Fatalf("fail-closed reason should name the mediated resolver requirement: %q", plan.Reason)
	}
	if plan.Mode == ModeDirect || plan.Engine == ModeDirect {
		t.Fatalf("privacy-mode failure must not fall back to direct: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "network", "proxy.url")); !os.IsNotExist(err) {
		t.Fatalf("fail-closed plan must not write the proxy secret; err=%v", err)
	}
}

func TestTun2SocksInvalidMediatedResolverFailsClosed(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "not-an-ip"
	plan, err := Prepare(Spec{
		Profile:    p,
		SessionDir: t.TempDir(),
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if err == nil || !plan.FailClosed {
		t.Fatalf("expected fail closed on a non-IP mediated resolver, got plan=%+v err=%v", plan, err)
	}
}

func TestTun2SocksDoesNotWriteSecretWhenRoutingCannotBeVerified(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	sessionDir := t.TempDir()
	plan, err := Prepare(Spec{
		Profile:    p,
		SessionDir: sessionDir,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if !errors.Is(err, ErrRoutingUnverified) {
		t.Fatalf("expected ErrRoutingUnverified, got plan=%+v err=%v", plan, err)
	}
	if !plan.FailClosed || plan.ProxySecretPath != "" || plan.GuestProxySecretPath != "" {
		t.Fatalf("expected fail closed plan without secret file: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "network", "proxy.url")); !os.IsNotExist(err) {
		t.Fatalf("unverified fail-closed plan should not write proxy secret; err=%v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(sessionDir, "network-plan.json"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if strings.Contains(string(manifest), "user:pass") {
		t.Fatalf("manifest leaked proxy secret: %s", manifest)
	}
	if strings.Contains(string(manifest), "proxy.url") && strings.Contains(string(manifest), "127.0.0.1:1080") {
		t.Fatalf("manifest leaked proxy URL: %s", manifest)
	}
	if !strings.Contains(string(manifest), `"proxySecretRef": "default-proxy"`) {
		t.Fatalf("manifest missing secret ref: %s", manifest)
	}
	bootstrap, err := os.ReadFile(plan.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if !strings.Contains(string(bootstrap), "tun2socks routing must be verified") || !strings.Contains(string(bootstrap), "exit 125") {
		t.Fatalf("fail-closed bootstrap mismatch: %s", bootstrap)
	}
}

func TestTun2SocksRuntimeVerificationPlan(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	sessionDir := t.TempDir()
	plan, err := Prepare(Spec{
		Profile:          p,
		SessionDir:       sessionDir,
		GuestSessionDir:  "/hideout/session",
		LocalBypassHosts: []string{"host.lima.internal", "host.lima.internal"},
		RuntimeVerify:    true,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.Verified || !plan.RuntimeVerify || plan.FailClosed {
		t.Fatalf("unexpected runtime verification plan: %+v", plan)
	}
	info, err := os.Stat(plan.ProxySecretPath)
	if err != nil {
		t.Fatalf("proxy secret file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("proxy secret file mode=%#o want 0600", info.Mode().Perm())
	}
	secretData, err := os.ReadFile(plan.ProxySecretPath)
	if err != nil {
		t.Fatalf("read proxy secret file: %v", err)
	}
	if string(secretData) != "socks5://user:pass@127.0.0.1:1080\n" {
		t.Fatalf("proxy secret file mismatch: %q", secretData)
	}
	if !strings.Contains(plan.Reason, "verified inside the guest") {
		t.Fatalf("unexpected reason: %q", plan.Reason)
	}
	manifest, err := os.ReadFile(filepath.Join(sessionDir, "network-plan.json"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if strings.Contains(string(manifest), "user:pass") || strings.Contains(string(manifest), "127.0.0.1:1080") {
		t.Fatalf("manifest leaked proxy secret: %s", manifest)
	}
	if !strings.Contains(string(manifest), `"runtimeVerify": true`) {
		t.Fatalf("manifest missing runtimeVerify: %s", manifest)
	}
	if !strings.Contains(string(manifest), `"dnsPolicy": "guest DNS is redirected to the declared mediated resolver over the TUN privacy path; connected-subnet resolvers are blocked so no query bypasses the TUN; a connected-subnet-only environment is refused"`) {
		t.Fatalf("manifest missing enforced tun2socks DNS policy: %s", manifest)
	}
	if strings.Contains(string(manifest), "not yet verified") {
		t.Fatalf("DNS policy must not claim the old unverified state: %s", manifest)
	}
	if !strings.Contains(string(manifest), `"localBypassHosts": [
    "host.lima.internal"
  ]`) {
		t.Fatalf("manifest missing local bypass hosts: %s", manifest)
	}
	bootstrap, err := os.ReadFile(plan.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if !strings.Contains(string(bootstrap), "tun2socks --device tun://hideout0 --proxy \"$proxy_url\"") {
		t.Fatalf("runtime verify bootstrap missing tun2socks start: %s", bootstrap)
	}
	// DNS closure: start the DoH stub, redirect guest :53 to it, and blackhole
	// connected-subnet resolvers, established after the default route is on TUN.
	if !strings.Contains(string(bootstrap), "hideout-dns-stub --listen 127.0.0.1:53 --doh-server \"$mediated_resolver\"") {
		t.Fatalf("bootstrap missing DoH stub start: %s", bootstrap)
	}
	if !strings.Contains(string(bootstrap), "nameserver 127.0.0.1") {
		t.Fatalf("bootstrap missing guest resolver override to the stub: %s", bootstrap)
	}
	if !strings.Contains(string(bootstrap), `iptables -I OUTPUT 1 -p "$proto" --dport 53 -d "$ns" -j DROP`) {
		t.Fatalf("bootstrap missing connected-subnet resolver block: %s", bootstrap)
	}
	if !strings.Contains(string(bootstrap), "guest resolver was not pointed at the DNS stub") {
		t.Fatalf("bootstrap missing DNS mediation structural verification: %s", bootstrap)
	}
	stubIdx := strings.Index(string(bootstrap), "hideout-dns-stub --listen")
	defRouteIdx := strings.Index(string(bootstrap), "ip route replace default dev hideout0")
	if stubIdx < 0 || defRouteIdx < 0 || stubIdx < defRouteIdx {
		t.Fatalf("DNS mediation must be established after the default route moves to hideout0")
	}
	mediationCleanup, err := os.ReadFile(plan.CleanupPath)
	if err != nil {
		t.Fatalf("read cleanup: %v", err)
	}
	if !strings.Contains(string(mediationCleanup), "dns-stub.pid") {
		t.Fatalf("cleanup missing DoH stub teardown: %s", mediationCleanup)
	}
	if !strings.Contains(string(mediationCleanup), "/etc/resolv.conf") {
		t.Fatalf("cleanup missing resolv.conf restore: %s", mediationCleanup)
	}
	if !strings.Contains(string(mediationCleanup), `iptables -D OUTPUT -p "$proto" --dport 53 -d "$ns" -j DROP`) {
		t.Fatalf("cleanup missing resolver block rollback: %s", mediationCleanup)
	}
	assertShellSyntaxNetworkTest(t, plan.BootstrapPath)
	if !strings.Contains(string(bootstrap), "default-route.before") {
		t.Fatalf("runtime verify bootstrap should save pre-tun default route: %s", bootstrap)
	}
	if !strings.Contains(string(bootstrap), "rm -f '/hideout/session/network/proxy.url'") {
		t.Fatalf("runtime verify bootstrap should remove proxy secret file after reading it: %s", bootstrap)
	}
	for _, want := range []string{
		"proxy endpoint route setup failed",
		"verified_default_route=$(ip route show default",
		"default-route.after",
		"tun2socks default route verification failed",
		"proxy_route_after=$(ip route get \"$proxy_route_host\"",
		"proxy-route.after",
		"proxy endpoint route verification failed",
		"proxy endpoint route loops through tun2socks",
		"local_bypass_0_route_host=$(awk -v host=\"$local_bypass_0_host\"",
		"local bypass host host.lima.internal must be an IP literal or present in /etc/hosts before tun2socks starts",
		"ip route replace \"$local_bypass_0_route_host\"",
		"local_bypass_0_route_after=$(ip route get \"$local_bypass_0_route_host\"",
		"local-bypass-0-route.after",
		"local bypass route for host.lima.internal loops through tun2socks",
		"tun2socks stopped during route verification",
		"tun2socks route verified",
	} {
		if !strings.Contains(string(bootstrap), want) {
			t.Fatalf("runtime verify bootstrap missing %q: %s", want, bootstrap)
		}
	}
	cleanup, err := os.ReadFile(plan.CleanupPath)
	if err != nil {
		t.Fatalf("read cleanup: %v", err)
	}
	for _, want := range []string{
		"tun2socks.pid",
		"kill \"$pid\"",
		"default-route.before",
		"route_args=${default_route#default }",
		"ip route replace default $route_args",
		"ip tuntap del mode tun dev hideout0",
		"rm -f /hideout/session/network/tun2socks.pid /hideout/session/network/proxy.url",
	} {
		if !strings.Contains(string(cleanup), want) {
			t.Fatalf("cleanup missing %q: %s", want, cleanup)
		}
	}
	if strings.Contains(string(cleanup), "ip route replace default $default_route") {
		t.Fatalf("cleanup must not duplicate the saved default route prefix: %s", cleanup)
	}
	assertShellSyntaxNetworkTest(t, plan.CleanupPath)
	if !strings.Contains(string(bootstrap), "proxy_route_host=$(awk -v host=\"$proxy_host\"") ||
		!strings.Contains(string(bootstrap), "proxy host must be an IP literal or present in /etc/hosts") ||
		!strings.Contains(string(bootstrap), "ip route replace \"$proxy_route_host\"") {
		t.Fatalf("runtime verify bootstrap should avoid pre-TUN DNS resolution for proxy host: %s", bootstrap)
	}
	if strings.Contains(string(bootstrap), "ip route replace \"$proxy_host\"") {
		t.Fatalf("runtime verify bootstrap should route using resolved proxy route host: %s", bootstrap)
	}
	if strings.Contains(string(bootstrap), "tun2socks routing must be verified") {
		t.Fatalf("runtime verify bootstrap should not use host-side fail-closed script: %s", bootstrap)
	}
}

func TestTun2SocksProxySecretWriteFailsClosedOnExistingSymlink(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	sessionDir := t.TempDir()
	networkDir := filepath.Join(sessionDir, "network")
	if err := os.MkdirAll(networkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkTarget := filepath.Join(t.TempDir(), "leak.txt")
	if err := os.WriteFile(symlinkTarget, []byte("safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(symlinkTarget, filepath.Join(networkDir, "proxy.url")); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(Spec{
		Profile:       p,
		SessionDir:    sessionDir,
		RuntimeVerify: true,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "proxy secret file must not already exist") {
		t.Fatalf("expected existing proxy secret path to fail closed, got plan=%+v err=%v", plan, err)
	}
	data, readErr := os.ReadFile(symlinkTarget)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "safe\n" {
		t.Fatalf("proxy secret write followed symlink and modified target: %q", data)
	}
	linkInfo, lstatErr := os.Lstat(filepath.Join(networkDir, "proxy.url"))
	if lstatErr != nil {
		t.Fatalf("proxy symlink should remain for diagnostics: %v", lstatErr)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("proxy path should still be symlink, mode=%v", linkInfo.Mode())
	}
}

func TestTun2SocksRemovesProxySecretWhenArtifactWriteFails(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	sessionDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(sessionDir, "network-plan.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(Spec{
		Profile:       p,
		SessionDir:    sessionDir,
		RuntimeVerify: true,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if err == nil {
		t.Fatalf("expected artifact write failure, got plan=%+v", plan)
	}
	if plan.ProxySecretPath == "" {
		t.Fatalf("expected proxy secret path to be assigned for cleanup evidence: %+v", plan)
	}
	if _, statErr := os.Stat(plan.ProxySecretPath); !os.IsNotExist(statErr) {
		t.Fatalf("artifact failure should remove proxy secret file; err=%v", statErr)
	}
}

func TestTun2SocksDryRunDoesNotWriteArtifactsOrSecret(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	sessionDir := t.TempDir()
	plan, err := Prepare(Spec{
		Profile:       p,
		SessionDir:    sessionDir,
		RuntimeVerify: true,
		DryRun:        true,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !plan.RuntimeVerify || plan.ProxySecretPath == "" {
		t.Fatalf("unexpected dry-run plan: %+v", plan)
	}
	for _, path := range []string{plan.ManifestPath, plan.BootstrapPath, plan.CleanupPath, plan.ProxySecretPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote artifact %s; err=%v", path, err)
		}
	}
}

func TestTun2SocksVerifiedPlan(t *testing.T) {
	p := profile.Default("test")
	p.Network.Mode = ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	plan, err := Prepare(Spec{
		Profile:    p,
		SessionDir: t.TempDir(),
		Verified:   true,
		Resolver: EnvSecretResolver{Env: []string{
			SecretEnvName("default-proxy") + "=http://127.0.0.1:8080",
		}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !plan.Verified || plan.FailClosed || plan.Engine != ModeTun2Socks {
		t.Fatalf("unexpected verified plan: %+v", plan)
	}
	bootstrap, err := os.ReadFile(plan.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if !strings.Contains(string(bootstrap), "tun2socks --device tun://hideout0 --proxy \"$proxy_url\"") {
		t.Fatalf("bootstrap missing tun2socks start: %s", bootstrap)
	}
	if !strings.Contains(string(bootstrap), "ip route replace default dev hideout0 metric 1") {
		t.Fatalf("bootstrap missing route replacement: %s", bootstrap)
	}
	if !strings.Contains(string(bootstrap), "tun2socks route verified") {
		t.Fatalf("bootstrap missing runtime route verification status: %s", bootstrap)
	}
	assertShellSyntaxNetworkTest(t, plan.BootstrapPath)
	assertShellSyntaxNetworkTest(t, plan.CleanupPath)
}

func assertShellSyntaxNetworkTest(t *testing.T, path string) {
	t.Helper()
	out, err := exec.Command("sh", "-n", path).CombinedOutput()
	if err != nil {
		t.Fatalf("shell syntax check failed for %s: %v\n%s", path, err, out)
	}
}
