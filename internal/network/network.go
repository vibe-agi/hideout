package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	ModeDirect    = "direct"
	ModeTun2Socks = "tun2socks"

	// dnsStubAddr is the guest-local address the DoH DNS stub listens on; the
	// guest resolver is pointed here and connected-subnet resolvers are blocked.
	dnsStubAddr = "127.0.0.1:53"
	dnsStubIP   = "127.0.0.1"
)

var ErrRoutingUnverified = errors.New("tun2socks routing is not verified")

type SecretResolver interface {
	Resolve(ref string) (string, error)
}

type EnvSecretResolver struct {
	Env []string
}

type Spec struct {
	Profile          profile.Profile
	Backend          string
	SessionDir       string
	GuestSessionDir  string
	TargetEnv        []string
	Resolver         SecretResolver
	LocalBypassHosts []string
	Verified         bool
	RuntimeVerify    bool
	DryRun           bool
}

type Plan struct {
	Mode                 string   `json:"mode"`
	Engine               string   `json:"engine,omitempty"`
	DNSPolicy            string   `json:"dnsPolicy,omitempty"`
	LocalBypassHosts     []string `json:"localBypassHosts,omitempty"`
	ProxySecretRef       string   `json:"proxySecretRef,omitempty"`
	ProxySecretPath      string   `json:"-"`
	GuestProxySecretPath string   `json:"guestProxySecretPath,omitempty"`
	MediatedResolver     string   `json:"mediatedResolver,omitempty"`
	Verified             bool     `json:"verified"`
	RuntimeVerify        bool     `json:"runtimeVerify"`
	FailClosed           bool     `json:"failClosed"`
	Reason               string   `json:"reason,omitempty"`
	ManifestPath         string   `json:"-"`
	GuestManifestPath    string   `json:"guestManifestPath"`
	BootstrapPath        string   `json:"-"`
	GuestBootstrapPath   string   `json:"guestBootstrapPath"`
	CleanupPath          string   `json:"-"`
	GuestCleanupPath     string   `json:"guestCleanupPath"`
}

func Prepare(spec Spec) (Plan, error) {
	if spec.SessionDir == "" {
		return Plan{}, errors.New("session directory is required")
	}
	guestSessionDir := spec.GuestSessionDir
	if guestSessionDir == "" {
		guestSessionDir = "/hideout/session"
	}
	if ContainsProxyEnv(spec.TargetEnv) {
		return Plan{}, errors.New("target env contains proxy variables")
	}
	plan := Plan{
		Mode:               spec.Profile.Network.Mode,
		Verified:           spec.Profile.Network.Mode == ModeDirect,
		ManifestPath:       filepath.Join(spec.SessionDir, "network-plan.json"),
		GuestManifestPath:  guestSessionDir + "/network-plan.json",
		BootstrapPath:      filepath.Join(spec.SessionDir, "network", "bootstrap.sh"),
		GuestBootstrapPath: guestSessionDir + "/network/bootstrap.sh",
		CleanupPath:        filepath.Join(spec.SessionDir, "network", "cleanup.sh"),
		GuestCleanupPath:   guestSessionDir + "/network/cleanup.sh",
	}
	if plan.Mode == "" {
		plan.Mode = ModeDirect
		plan.Verified = true
	}
	switch plan.Mode {
	case ModeDirect:
		plan.DNSPolicy = "guest default resolver over direct route"
		plan.Reason = "direct network mode; host network identity may be visible"
		return plan, maybeWriteArtifacts(plan, spec.DryRun)
	case ModeTun2Socks:
		localBypassHosts, err := normalizeLocalBypassHosts(spec.LocalBypassHosts)
		if err != nil {
			plan.FailClosed = true
			plan.Reason = err.Error()
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, err
		}
		plan.LocalBypassHosts = localBypassHosts
		if spec.Profile.Network.ProxyEnvVisible {
			plan.FailClosed = true
			plan.Reason = "proxy env visibility is not allowed in tun2socks mode"
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, errors.New(plan.Reason)
		}
		ref := spec.Profile.Network.ProxySecretRef
		if strings.TrimSpace(ref) == "" {
			plan.FailClosed = true
			plan.Reason = "tun2socks requires network.proxySecretRef"
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, errors.New(plan.Reason)
		}
		resolver := spec.Resolver
		if resolver == nil {
			resolver = EnvSecretResolver{}
		}
		secret, err := resolver.Resolve(ref)
		if err != nil {
			plan.FailClosed = true
			plan.ProxySecretRef = ref
			plan.Reason = err.Error()
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, err
		}
		if err := validateProxyURL(secret); err != nil {
			plan.FailClosed = true
			plan.ProxySecretRef = ref
			plan.Reason = err.Error()
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, err
		}
		mediatedResolver := strings.TrimSpace(spec.Profile.Network.MediatedResolver)
		if mediatedResolver == "" {
			plan.FailClosed = true
			plan.ProxySecretRef = ref
			plan.Reason = "tun2socks requires a mediated resolver: blocking the connected-subnet bypass closes the DNS leak but does not provide working DNS, so a mediated resolver reachable through the privacy path must be declared; a connected-subnet-only environment is refused"
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, errors.New(plan.Reason)
		}
		if net.ParseIP(mediatedResolver) == nil {
			plan.FailClosed = true
			plan.ProxySecretRef = ref
			plan.Reason = fmt.Sprintf("network.mediatedResolver %q is not an IP literal", mediatedResolver)
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, errors.New(plan.Reason)
		}
		plan.MediatedResolver = mediatedResolver
		plan.Engine = ModeTun2Socks
		plan.DNSPolicy = "guest DNS is redirected to the declared mediated resolver over the TUN privacy path; connected-subnet resolvers are blocked so no query bypasses the TUN; a connected-subnet-only environment is refused"
		plan.ProxySecretRef = ref
		plan.Verified = spec.Verified
		plan.RuntimeVerify = spec.RuntimeVerify
		if !plan.Verified {
			if plan.RuntimeVerify {
				secretPath := filepath.Join(spec.SessionDir, "network", "proxy.url")
				plan.ProxySecretPath = secretPath
				plan.GuestProxySecretPath = guestSessionDir + "/network/proxy.url"
				if err := writeProxySecret(secretPath, secret, spec.DryRun); err != nil {
					return plan, err
				}
				plan.Reason = "tun2socks routing will be verified inside the guest before launching target command"
				return plan, maybeWriteArtifactsWithSecret(plan, spec.DryRun)
			}
			plan.FailClosed = true
			plan.Reason = "tun2socks routing must be verified before launching target command"
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, ErrRoutingUnverified
		}
		secretPath := filepath.Join(spec.SessionDir, "network", "proxy.url")
		plan.ProxySecretPath = secretPath
		plan.GuestProxySecretPath = guestSessionDir + "/network/proxy.url"
		if err := writeProxySecret(secretPath, secret, spec.DryRun); err != nil {
			return plan, err
		}
		plan.Reason = "tun2socks routing verified"
		return plan, maybeWriteArtifactsWithSecret(plan, spec.DryRun)
	default:
		return plan, fmt.Errorf("unsupported network mode %q", plan.Mode)
	}
}

func normalizeLocalBypassHosts(hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if strings.Contains(host, "://") || strings.ContainsAny(host, " \t\r\n/\\") {
			return nil, fmt.Errorf("local bypass host %q is invalid", host)
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out, nil
}

func maybeWriteArtifacts(plan Plan, dryRun bool) error {
	if dryRun {
		return nil
	}
	return writeArtifacts(plan)
}

func maybeWriteArtifactsWithSecret(plan Plan, dryRun bool) error {
	err := maybeWriteArtifacts(plan, dryRun)
	if err != nil && !dryRun && plan.ProxySecretPath != "" {
		_ = os.Remove(plan.ProxySecretPath)
	}
	return err
}

func writeProxySecret(path, secret string, dryRun bool) error {
	if dryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("proxy secret file must not already exist: %w", err)
	}
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.WriteString(secret + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	removeOnError = false
	return nil
}

func ContainsProxyEnv(env []string) bool {
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch name {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
			"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy":
			return true
		}
	}
	return false
}

func (r EnvSecretResolver) Resolve(ref string) (string, error) {
	env := r.Env
	if env == nil {
		env = os.Environ()
	}
	name, err := secrets.EnvName(ref)
	if err != nil {
		return "", err
	}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			if v == "" {
				return "", fmt.Errorf("secret ref %s is empty", ref)
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("secret ref %s is not set", ref)
}

func SecretEnvName(ref string) string {
	name, err := secrets.EnvName(ref)
	if err != nil {
		return ""
	}
	return name
}

func validateProxyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("proxy secret must be a URL")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
}

func writeArtifacts(plan Plan) error {
	if err := writeBootstrap(plan); err != nil {
		return err
	}
	if err := writeCleanup(plan); err != nil {
		return err
	}
	return writeManifest(plan)
}

func writeBootstrap(plan Plan) error {
	if plan.BootstrapPath == "" {
		return errors.New("network bootstrap path is required")
	}
	if err := os.MkdirAll(filepath.Dir(plan.BootstrapPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(plan.BootstrapPath, []byte(BootstrapScript(plan)), 0o700)
}

func writeCleanup(plan Plan) error {
	if plan.CleanupPath == "" {
		return errors.New("network cleanup path is required")
	}
	if err := os.MkdirAll(filepath.Dir(plan.CleanupPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(plan.CleanupPath, []byte(CleanupScript(plan)), 0o700)
}

func BootstrapScript(plan Plan) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -eu\n")
	b.WriteString(networkStatusHelpers())
	switch plan.Mode {
	case ModeDirect, "":
		b.WriteString("write_status 'direct network mode'\n")
		return b.String()
	case ModeTun2Socks:
		if plan.FailClosed {
			fmt.Fprintf(&b, "echo %s >&2\n", shellQuote("hideout: "+plan.Reason))
			b.WriteString("exit 125\n")
			return b.String()
		}
		b.WriteString("[ \"$(id -u)\" -eq 0 ] || { echo 'hideout: tun2socks setup requires the Hideout setup identity' >&2; exit 127; }\n")
		proxyPath := plan.GuestProxySecretPath
		if proxyPath == "" {
			proxyPath = "/hideout/session/network/proxy.url"
		}
		b.WriteString("command -v tun2socks >/dev/null 2>&1 || { echo 'hideout: tun2socks command missing' >&2; exit 127; }\n")
		b.WriteString("command -v ip >/dev/null 2>&1 || { echo 'hideout: ip command missing' >&2; exit 127; }\n")
		b.WriteString("[ -c /dev/net/tun ] || { echo 'hideout: /dev/net/tun is unavailable' >&2; exit 127; }\n")
		fmt.Fprintf(&b, "[ -r %s ] || { echo 'hideout: proxy secret file missing' >&2; exit 127; }\n", shellQuote(proxyPath))
		fmt.Fprintf(&b, "proxy_url=$(sed -n '1p' %s)\n", shellQuote(proxyPath))
		fmt.Fprintf(&b, "rm -f %s\n", shellQuote(proxyPath))
		b.WriteString("[ -n \"$proxy_url\" ] || { echo 'hideout: proxy secret is empty' >&2; exit 127; }\n")
		b.WriteString("default_route=$(ip route show default | head -n 1 || true)\n")
		b.WriteString("printf '%s\\n' \"$default_route\" > /hideout/session/network/default-route.before\n")
		b.WriteString("default_gw=$(printf '%s\\n' \"$default_route\" | awk '{for (i=1;i<=NF;i++) if ($i==\"via\") print $(i+1)}')\n")
		b.WriteString("default_dev=$(printf '%s\\n' \"$default_route\" | awk '{for (i=1;i<=NF;i++) if ($i==\"dev\") print $(i+1)}')\n")
		b.WriteString("[ -n \"$default_dev\" ] || { echo 'hideout: default network device not found' >&2; exit 127; }\n")
		for i, host := range plan.LocalBypassHosts {
			writeLocalBypassSetup(&b, i, host)
		}
		b.WriteString("proxy_authority=${proxy_url#*://}\n")
		b.WriteString("proxy_authority=${proxy_authority%%/*}\n")
		b.WriteString("proxy_authority=${proxy_authority##*@}\n")
		b.WriteString("case \"$proxy_authority\" in\n")
		b.WriteString("  \\[*\\]*) proxy_host=${proxy_authority#\\[}; proxy_host=${proxy_host%%\\]*} ;;\n")
		b.WriteString("  *) proxy_host=${proxy_authority%%:*} ;;\n")
		b.WriteString("esac\n")
		b.WriteString("[ -n \"$proxy_host\" ] || { echo 'hideout: proxy host parse failed' >&2; exit 127; }\n")
		b.WriteString("proxy_route_host=$proxy_host\n")
		b.WriteString("case \"$proxy_host\" in\n")
		b.WriteString("  *[!0-9.]*)\n")
		b.WriteString("    proxy_route_host=$(awk -v host=\"$proxy_host\" '($1 !~ /^#/){for (i=2;i<=NF;i++) if ($i==host) {print $1; exit}}' /etc/hosts)\n")
		b.WriteString("    [ -n \"$proxy_route_host\" ] || { echo 'hideout: proxy host must be an IP literal or present in /etc/hosts before tun2socks starts' >&2; exit 127; }\n")
		b.WriteString("    ;;\n")
		b.WriteString("esac\n")
		b.WriteString("ip tuntap add mode tun dev hideout0 2>/dev/null || true\n")
		b.WriteString("ip addr add 198.18.0.1/15 dev hideout0 2>/dev/null || true\n")
		b.WriteString("ip link set dev hideout0 up\n")
		b.WriteString("if [ -n \"$default_gw\" ]; then\n")
		b.WriteString("  ip route replace \"$proxy_route_host\" via \"$default_gw\" dev \"$default_dev\" || { echo 'hideout: proxy endpoint route setup failed' >&2; exit 127; }\n")
		b.WriteString("else\n")
		b.WriteString("  ip route replace \"$proxy_route_host\" dev \"$default_dev\" || { echo 'hideout: proxy endpoint route setup failed' >&2; exit 127; }\n")
		b.WriteString("fi\n")
		b.WriteString("tun2socks --device tun://hideout0 --proxy \"$proxy_url\" > /hideout/session/network/tun2socks.log 2>&1 &\n")
		b.WriteString("echo $! > /hideout/session/network/tun2socks.pid\n")
		b.WriteString("sleep 0.2\n")
		b.WriteString("kill -0 \"$(cat /hideout/session/network/tun2socks.pid)\" 2>/dev/null || { echo 'hideout: tun2socks failed to start' >&2; exit 127; }\n")
		b.WriteString("ip route replace default dev hideout0 metric 1\n")
		b.WriteString("verified_default_route=$(ip route show default | head -n 1 || true)\n")
		b.WriteString("printf '%s\\n' \"$verified_default_route\" > /hideout/session/network/default-route.after\n")
		b.WriteString("case \"$verified_default_route\" in\n")
		b.WriteString("  *' dev hideout0'|*' dev hideout0 '* ) ;;\n")
		b.WriteString("  *) echo 'hideout: tun2socks default route verification failed' >&2; exit 127 ;;\n")
		b.WriteString("esac\n")
		writeDNSMediationSetup(&b, plan.MediatedResolver)
		for i, host := range plan.LocalBypassHosts {
			writeLocalBypassVerify(&b, i, host)
		}
		b.WriteString("proxy_route_after=$(ip route get \"$proxy_route_host\" 2>/dev/null | head -n 1 || true)\n")
		b.WriteString("printf '%s\\n' \"$proxy_route_after\" > /hideout/session/network/proxy-route.after\n")
		b.WriteString("[ -n \"$proxy_route_after\" ] || { echo 'hideout: proxy endpoint route verification failed' >&2; exit 127; }\n")
		b.WriteString("case \"$proxy_route_after\" in\n")
		b.WriteString("  *' dev hideout0'|*' dev hideout0 '* ) echo 'hideout: proxy endpoint route loops through tun2socks' >&2; exit 127 ;;\n")
		b.WriteString("esac\n")
		b.WriteString("kill -0 \"$(cat /hideout/session/network/tun2socks.pid)\" 2>/dev/null || { echo 'hideout: tun2socks stopped during route verification' >&2; exit 127; }\n")
		writeDNSMediationVerify(&b, plan.MediatedResolver)
		b.WriteString("write_status 'tun2socks route verified'\n")
		return b.String()
	default:
		fmt.Fprintf(&b, "echo %s >&2\n", shellQuote("hideout: unsupported network mode "+plan.Mode))
		b.WriteString("exit 125\n")
		return b.String()
	}
}

func CleanupScript(plan Plan) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set +e\n")
	b.WriteString(networkStatusHelpers())
	switch plan.Mode {
	case ModeTun2Socks:
		b.WriteString("[ \"$(id -u)\" -eq 0 ] || { echo 'hideout: tun2socks cleanup requires the Hideout setup identity' >&2; exit 0; }\n")
		b.WriteString("if [ -r /hideout/session/network/tun2socks.pid ]; then\n")
		b.WriteString("  pid=$(sed -n '1p' /hideout/session/network/tun2socks.pid)\n")
		b.WriteString("  if [ -n \"$pid\" ]; then kill \"$pid\" 2>/dev/null || true; fi\n")
		b.WriteString("fi\n")
		writeDNSMediationCleanup(&b)
		b.WriteString("if command -v ip >/dev/null 2>&1; then\n")
		b.WriteString("  if [ -s /hideout/session/network/default-route.before ]; then\n")
		b.WriteString("    default_route=$(sed -n '1p' /hideout/session/network/default-route.before)\n")
		b.WriteString("    route_args=${default_route#default }\n")
		b.WriteString("    if [ -n \"$route_args\" ] && [ \"$route_args\" != \"$default_route\" ]; then ip route replace default $route_args 2>/dev/null || true; fi\n")
		b.WriteString("  fi\n")
		b.WriteString("  ip link set dev hideout0 down 2>/dev/null || true\n")
		b.WriteString("  ip tuntap del mode tun dev hideout0 2>/dev/null || true\n")
		b.WriteString("fi\n")
		b.WriteString("rm -f /hideout/session/network/tun2socks.pid /hideout/session/network/proxy.url\n")
		b.WriteString("write_status 'tun2socks cleanup complete'\n")
	default:
		b.WriteString("write_status 'network cleanup complete'\n")
	}
	return b.String()
}

func networkStatusHelpers() string {
	return `ensure_network_dir() {
  mkdir -p /hideout/session/network 2>/dev/null || true
}
write_status() {
  ensure_network_dir
  printf '%s\n' "$*" > /hideout/session/network/status 2>/dev/null || echo 'hideout: network status write failed' >&2
}
ensure_network_dir
`
}

func writeLocalBypassSetup(b *strings.Builder, index int, host string) {
	prefix := fmt.Sprintf("local_bypass_%d", index)
	fmt.Fprintf(b, "%s_host=%s\n", prefix, shellQuote(host))
	fmt.Fprintf(b, "%s_route_host=$%s_host\n", prefix, prefix)
	fmt.Fprintf(b, "case \"$%s_host\" in\n", prefix)
	b.WriteString("  *[!0-9.]*)\n")
	fmt.Fprintf(b, "    %s_route_host=$(awk -v host=\"$%s_host\" '($1 !~ /^#/){for (i=2;i<=NF;i++) if ($i==host) {print $1; exit}}' /etc/hosts)\n", prefix, prefix)
	fmt.Fprintf(b, "    [ -n \"$%s_route_host\" ] || { echo %s >&2; exit 127; }\n", prefix, shellQuote("hideout: local bypass host "+host+" must be an IP literal or present in /etc/hosts before tun2socks starts"))
	b.WriteString("    ;;\n")
	b.WriteString("esac\n")
	b.WriteString("if [ -n \"$default_gw\" ]; then\n")
	fmt.Fprintf(b, "  ip route replace \"$%s_route_host\" via \"$default_gw\" dev \"$default_dev\" || { echo %s >&2; exit 127; }\n", prefix, shellQuote("hideout: local bypass route setup failed for "+host))
	b.WriteString("else\n")
	fmt.Fprintf(b, "  ip route replace \"$%s_route_host\" dev \"$default_dev\" || { echo %s >&2; exit 127; }\n", prefix, shellQuote("hideout: local bypass route setup failed for "+host))
	b.WriteString("fi\n")
}

func writeLocalBypassVerify(b *strings.Builder, index int, host string) {
	prefix := fmt.Sprintf("local_bypass_%d", index)
	fmt.Fprintf(b, "%s_route_after=$(ip route get \"$%s_route_host\" 2>/dev/null | head -n 1 || true)\n", prefix, prefix)
	fmt.Fprintf(b, "printf '%%s\\n' \"$%s_route_after\" > /hideout/session/network/local-bypass-%d-route.after\n", prefix, index)
	fmt.Fprintf(b, "[ -n \"$%s_route_after\" ] || { echo %s >&2; exit 127; }\n", prefix, shellQuote("hideout: local bypass route verification failed for "+host))
	fmt.Fprintf(b, "case \"$%s_route_after\" in\n", prefix)
	fmt.Fprintf(b, "  *' dev hideout0'|*' dev hideout0 '* ) echo %s >&2; exit 127 ;;\n", shellQuote("hideout: local bypass route for "+host+" loops through tun2socks"))
	b.WriteString("esac\n")
}

// writeDNSMediationSetup emits the structural DNS closure. It starts the
// guest-local DoH stub (which forwards each DNS query as DoH/HTTPS to the
// declared mediated resolver over the TUN and the SOCKS CONNECT proxy),
// redirects all guest DNS to the stub, and blackholes the connected-subnet
// resolvers so no query bypasses the TUN. Emitted only when a mediated resolver
// is declared (required in tun2socks mode). Requires iptables and the stub.
func writeDNSMediationSetup(b *strings.Builder, mediatedResolver string) {
	if mediatedResolver == "" {
		return
	}
	b.WriteString("command -v ip >/dev/null 2>&1 || { echo 'hideout: ip is required for DNS mediation' >&2; exit 127; }\n")
	b.WriteString("[ -x /hideout/session/shims/hideout-dns-stub ] || { echo 'hideout: hideout-dns-stub is missing from session shims' >&2; exit 127; }\n")
	fmt.Fprintf(b, "mediated_resolver=%s\n", shellQuote(mediatedResolver))
	b.WriteString("printf '%s\\n' \"$mediated_resolver\" > /hideout/session/network/mediated-resolver\n")
	// Capture the guest's real upstream resolvers before repointing DNS: both the
	// /etc/resolv.conf nameservers and, when systemd-resolved is in use, its
	// actual upstream DNS servers (the connected-subnet resolver such as
	// 192.168.5.3 is systemd-resolved's upstream, not a resolv.conf nameserver).
	b.WriteString("ns_list=$( { awk '/^[[:space:]]*nameserver/ {print $2}' /etc/resolv.conf 2>/dev/null; if command -v resolvectl >/dev/null 2>&1; then resolvectl status 2>/dev/null | awk -F: '/DNS Servers/{n=split($2,a,\" \"); for (i=1;i<=n;i++) if (a[i] != \"\") print a[i]}'; fi; } | sort -u | tr '\\n' ' ')\n")
	b.WriteString("printf '%s\\n' \"$ns_list\" > /hideout/session/network/resolvers.before\n")
	b.WriteString("chmod 0644 /hideout/session/network/resolvers.before 2>/dev/null || true\n")
	// Start the DoH stub on 127.0.0.1:53. DNS in on :53, DoH out over HTTPS to
	// the mediated resolver, which routes through the TUN and the CONNECT proxy.
	fmt.Fprintf(b, "/hideout/session/shims/hideout-dns-stub --listen %s --doh-server \"$mediated_resolver\" > /hideout/session/network/dns-stub.log 2>&1 &\n", dnsStubAddr)
	b.WriteString("echo $! > /hideout/session/network/dns-stub.pid\n")
	b.WriteString("sleep 0.2\n")
	b.WriteString("kill -0 \"$(cat /hideout/session/network/dns-stub.pid)\" 2>/dev/null || { echo 'hideout: hideout-dns-stub failed to start' >&2; exit 127; }\n")
	// Point the guest resolver at the stub. Overriding /etc/resolv.conf (which is
	// usually a systemd-resolved symlink) makes libc's DNS use the stub directly;
	// resolvectl repoints systemd-resolved (the nss-resolve path) at the stub too.
	b.WriteString("if [ -L /etc/resolv.conf ] || [ -f /etc/resolv.conf ]; then cp -a /etc/resolv.conf /hideout/session/network/resolv.conf.orig 2>/dev/null || readlink /etc/resolv.conf > /hideout/session/network/resolv.conf.link 2>/dev/null || true; fi\n")
	fmt.Fprintf(b, "rm -f /etc/resolv.conf 2>/dev/null || true; printf 'nameserver %s\\noptions edns0\\n' > /etc/resolv.conf 2>/dev/null || true\n", dnsStubIP)
	b.WriteString("if command -v resolvectl >/dev/null 2>&1; then\n")
	b.WriteString("  default_link=$(ip route show default | awk '{for (i=1;i<=NF;i++) if ($i==\"dev\") print $(i+1); exit}')\n")
	fmt.Fprintf(b, "  [ -n \"$default_link\" ] && resolvectl dns \"$default_link\" %s >/dev/null 2>&1 || true\n", dnsStubIP)
	b.WriteString("  resolvectl flush-caches >/dev/null 2>&1 || true\n")
	b.WriteString("fi\n")
	// Structural leak closure: DROP DNS (udp/tcp :53) to every connected-subnet
	// resolver so no query reaches it off the TUN, even if an app hardcodes it.
	// Blocking only :53 (not the whole IP) keeps the resolver host usable on
	// other ports — e.g. the proxy endpoint at the Lima gateway. Skip the
	// mediated resolver, the loopback stub, and resolvers already via the TUN. A
	// resolver that cannot be blocked leaves the leak open, so failure fails the
	// run closed.
	b.WriteString("command -v iptables >/dev/null 2>&1 || { echo 'hideout: iptables is required to block connected-subnet resolvers' >&2; exit 127; }\n")
	b.WriteString("for ns in $ns_list; do\n")
	b.WriteString("  [ \"$ns\" = \"$mediated_resolver\" ] && continue\n")
	fmt.Fprintf(b, "  [ \"$ns\" = %s ] && continue\n", shellQuote(dnsStubIP))
	b.WriteString("  case \"$ns\" in 127.*|::1) continue ;; esac\n")
	b.WriteString("  ns_route=$(ip route get \"$ns\" 2>/dev/null | head -n 1 || true)\n")
	b.WriteString("  case \"$ns_route\" in *' dev hideout0'*) continue ;; esac\n")
	b.WriteString("  for proto in udp tcp; do\n")
	b.WriteString("    iptables -C OUTPUT -p \"$proto\" --dport 53 -d \"$ns\" -j DROP 2>/dev/null \\\n")
	b.WriteString("      || iptables -I OUTPUT 1 -p \"$proto\" --dport 53 -d \"$ns\" -j DROP \\\n")
	b.WriteString("      || { echo \"hideout: failed to block connected-subnet resolver $ns\" >&2; exit 127; }\n")
	b.WriteString("  done\n")
	b.WriteString("done\n")
}

// writeDNSMediationVerify confirms the guest resolver is pointed at the DoH stub
// and the stub is alive before the target launches (a structural check). The
// observable bidirectional proof (forward: a target-style resolution plus HTTPS
// fetch traverse the mediated DoH path; reverse: every captured connected-subnet
// resolver is unreachable — a direct query fails) is performed by Gate 3.
func writeDNSMediationVerify(b *strings.Builder, mediatedResolver string) {
	if mediatedResolver == "" {
		return
	}
	fmt.Fprintf(b, "grep -q '^nameserver %s' /etc/resolv.conf 2>/dev/null || { echo 'hideout: guest resolver was not pointed at the DNS stub' >&2; exit 127; }\n", dnsStubIP)
	b.WriteString("kill -0 \"$(cat /hideout/session/network/dns-stub.pid)\" 2>/dev/null || { echo 'hideout: hideout-dns-stub stopped before target launch' >&2; exit 127; }\n")
	b.WriteString("write_status 'dns mediation enforced'\n")
}

// writeDNSMediationCleanup stops the stub and removes the DNS redirect rules and
// blackhole routes, symmetric with writeDNSMediationSetup.
func writeDNSMediationCleanup(b *strings.Builder) {
	b.WriteString("if [ -r /hideout/session/network/dns-stub.pid ]; then\n")
	b.WriteString("  dns_stub_pid=$(sed -n '1p' /hideout/session/network/dns-stub.pid)\n")
	b.WriteString("  if [ -n \"$dns_stub_pid\" ]; then kill \"$dns_stub_pid\" 2>/dev/null || true; fi\n")
	b.WriteString("fi\n")
	// Restore /etc/resolv.conf (original symlink or file) and remove blackholes.
	b.WriteString("if [ -f /hideout/session/network/resolv.conf.link ]; then\n")
	b.WriteString("  ln -sf \"$(cat /hideout/session/network/resolv.conf.link)\" /etc/resolv.conf 2>/dev/null || true\n")
	b.WriteString("elif [ -f /hideout/session/network/resolv.conf.orig ]; then\n")
	b.WriteString("  cp -a /hideout/session/network/resolv.conf.orig /etc/resolv.conf 2>/dev/null || true\n")
	b.WriteString("fi\n")
	b.WriteString("if command -v resolvectl >/dev/null 2>&1; then resolvectl revert \"$(ip route show default | awk '{for (i=1;i<=NF;i++) if ($i==\"dev\") print $(i+1); exit}')\" >/dev/null 2>&1 || true; fi\n")
	b.WriteString("if command -v iptables >/dev/null 2>&1 && [ -r /hideout/session/network/resolvers.before ]; then\n")
	b.WriteString("  for ns in $(cat /hideout/session/network/resolvers.before); do\n")
	b.WriteString("    for proto in udp tcp; do iptables -D OUTPUT -p \"$proto\" --dport 53 -d \"$ns\" -j DROP 2>/dev/null || true; done\n")
	b.WriteString("  done\n")
	b.WriteString("fi\n")
	b.WriteString("rm -f /hideout/session/network/mediated-resolver /hideout/session/network/dns-stub.pid /hideout/session/network/resolvers.before\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeManifest(plan Plan) error {
	if plan.ManifestPath == "" {
		return errors.New("network manifest path is required")
	}
	if err := os.MkdirAll(filepath.Dir(plan.ManifestPath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(plan.ManifestPath, data, 0o600)
}
