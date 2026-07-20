package network

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
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

	EnvironmentServiceSchema = "hideout.environment-service/v1"
)

var ErrRoutingUnverified = errors.New("tun2socks routing is not verified")

var serviceBootIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type SecretResolver interface {
	Resolve(ref string) (string, error)
}

type EnvSecretResolver struct {
	Env []string
}

type Spec struct {
	Profile          profile.Profile
	Backend          string
	ArtifactDir      string
	GuestArtifactDir string
	SecretDir        string
	GuestSecretDir   string
	// GuestHelperDir is the guest directory that holds the tun2socks and
	// hideout-dns-stub network helpers for this plan. It defaults to the
	// per-session shim dir; the environment network service (which runs its
	// privileged setup before per-session shims are mounted) sets it to its own
	// mounted service dir so the bootstrap finds the helpers there.
	GuestHelperDir   string
	TargetEnv        []string
	Resolver         SecretResolver
	LocalBypassHosts []string
	Verified         bool
	RuntimeVerify    bool
	DryRun           bool
	// GatewayProxyURL is a Hideout-minted, guest-facing SOCKS endpoint. When
	// present, the resolved operator proxy remains host-only and this URL is the
	// only proxy material written into the guest session handoff.
	GatewayProxyURL string
	GatewayID       string
	ConfigurationID string
}

type Plan struct {
	Mode                     string   `json:"mode"`
	Engine                   string   `json:"engine,omitempty"`
	DNSPolicy                string   `json:"dnsPolicy,omitempty"`
	LocalBypassHosts         []string `json:"localBypassHosts,omitempty"`
	ProxySecretRef           string   `json:"proxySecretRef,omitempty"`
	ProxySecretPath          string   `json:"-"`
	GuestProxySecretPath     string   `json:"guestProxySecretPath,omitempty"`
	MediatedResolver         string   `json:"mediatedResolver,omitempty"`
	Verified                 bool     `json:"verified"`
	RuntimeVerify            bool     `json:"runtimeVerify"`
	FailClosed               bool     `json:"failClosed"`
	Reason                   string   `json:"reason,omitempty"`
	ManifestPath             string   `json:"-"`
	GuestManifestPath        string   `json:"guestManifestPath"`
	BootstrapPath            string   `json:"-"`
	GuestBootstrapPath       string   `json:"guestBootstrapPath"`
	CleanupPath              string   `json:"-"`
	GuestCleanupPath         string   `json:"guestCleanupPath"`
	GuestHelperDir           string   `json:"guestHelperDir,omitempty"`
	ConfigurationFingerprint string   `json:"-"`
	ConfigurationID          string   `json:"-"`
	UpstreamProxyURL         string   `json:"-"`
	GatewayID                string   `json:"-"`
}

type ServiceStatus string

const (
	ServiceStarting  ServiceStatus = "starting"
	ServiceSwitching ServiceStatus = "switching"
	ServiceReady     ServiceStatus = "ready"
	ServiceCleaning  ServiceStatus = "cleaning"
	ServiceFailed    ServiceStatus = "failed"
)

type ServiceState struct {
	Schema                   string        `json:"schema"`
	EnvironmentID            string        `json:"environmentId"`
	Kind                     string        `json:"kind"`
	Status                   ServiceStatus `json:"status"`
	ConfigurationFingerprint string        `json:"configurationFingerprint"`
	ConfigurationID          string        `json:"configurationId"`
	Mode                     string        `json:"mode"`
	GatewayID                string        `json:"gatewayId"`
	Resolver                 string        `json:"resolver,omitempty"`
	BootID                   string        `json:"bootId,omitempty"`
	StartedAt                time.Time     `json:"startedAt"`
	UpdatedAt                time.Time     `json:"updatedAt,omitempty"`
	LastError                string        `json:"lastError,omitempty"`
}

var environmentIDPattern = regexp.MustCompile(`^env_[a-z0-9]+$`)

func Prepare(spec Spec) (Plan, error) {
	if spec.ArtifactDir == "" {
		return Plan{}, errors.New("network artifact directory is required")
	}
	guestArtifactDir := spec.GuestArtifactDir
	if guestArtifactDir == "" {
		guestArtifactDir = "/hideout/session"
	}
	guestHelperDir := strings.TrimSpace(spec.GuestHelperDir)
	if guestHelperDir == "" {
		guestHelperDir = "/hideout/session/shims"
	}
	if ContainsProxyEnv(spec.TargetEnv) {
		return Plan{}, errors.New("target env contains proxy variables")
	}
	plan := Plan{
		Mode:               spec.Profile.Network.Mode,
		GatewayID:          strings.TrimSpace(spec.GatewayID),
		ConfigurationID:    strings.TrimSpace(spec.ConfigurationID),
		Verified:           spec.Profile.Network.Mode == ModeDirect,
		ManifestPath:       filepath.Join(spec.ArtifactDir, "network-plan.json"),
		GuestManifestPath:  guestArtifactDir + "/network-plan.json",
		BootstrapPath:      filepath.Join(spec.ArtifactDir, "network", "bootstrap.sh"),
		GuestBootstrapPath: guestArtifactDir + "/network/bootstrap.sh",
		CleanupPath:        filepath.Join(spec.ArtifactDir, "network", "cleanup.sh"),
		GuestCleanupPath:   guestArtifactDir + "/network/cleanup.sh",
		GuestHelperDir:     guestHelperDir,
	}
	if plan.Mode == "" {
		plan.Mode = ModeDirect
		plan.Verified = true
	}
	switch plan.Mode {
	case ModeDirect:
		plan.DNSPolicy = "guest default resolver over direct route"
		plan.Reason = "direct network mode; host network identity may be visible"
		plan.ConfigurationFingerprint = networkFingerprint(plan, "", "")
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
		plan.UpstreamProxyURL = secret
		plan.ConfigurationFingerprint = networkFingerprint(plan, ref, secret)
		plan.Verified = spec.Verified
		plan.RuntimeVerify = spec.RuntimeVerify
		if !plan.Verified {
			if plan.RuntimeVerify {
				if err := assignProxySecretPaths(&plan, spec); err != nil {
					_ = maybeWriteArtifacts(plan, spec.DryRun)
					return plan, err
				}
				proxyMaterial, err := guestProxyMaterial(secret, spec.GatewayProxyURL)
				if err != nil {
					return plan, err
				}
				if err := writeProxySecret(plan.ProxySecretPath, proxyMaterial, spec.DryRun); err != nil {
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
		if err := assignProxySecretPaths(&plan, spec); err != nil {
			_ = maybeWriteArtifacts(plan, spec.DryRun)
			return plan, err
		}
		proxyMaterial, err := guestProxyMaterial(secret, spec.GatewayProxyURL)
		if err != nil {
			return plan, err
		}
		if err := writeProxySecret(plan.ProxySecretPath, proxyMaterial, spec.DryRun); err != nil {
			return plan, err
		}
		plan.Reason = "tun2socks routing verified"
		return plan, maybeWriteArtifactsWithSecret(plan, spec.DryRun)
	default:
		return plan, fmt.Errorf("unsupported network mode %q", plan.Mode)
	}
}

func guestProxyMaterial(upstream, gateway string) (string, error) {
	if strings.TrimSpace(gateway) == "" {
		return upstream, nil
	}
	if err := validateProxyURL(gateway); err != nil {
		return "", fmt.Errorf("invalid environment network gateway: %w", err)
	}
	parsed, _ := url.Parse(gateway)
	if parsed.Scheme != "socks5" || parsed.User == nil {
		return "", errors.New("environment network gateway requires authenticated socks5")
	}
	username := parsed.User.Username()
	password, passwordSet := parsed.User.Password()
	if username == "" || !passwordSet || password == "" {
		return "", errors.New("environment network gateway credential is incomplete")
	}
	return gateway, nil
}

func assignProxySecretPaths(plan *Plan, spec Spec) error {
	if strings.TrimSpace(spec.SecretDir) == "" || strings.TrimSpace(spec.GuestSecretDir) == "" {
		plan.FailClosed = true
		plan.Reason = "tun2socks requires explicit host and guest session-secret directories"
		return errors.New(plan.Reason)
	}
	plan.ProxySecretPath = filepath.Join(spec.SecretDir, "network", "proxy.url")
	plan.GuestProxySecretPath = spec.GuestSecretDir + "/network/proxy.url"
	return nil
}

func networkFingerprint(plan Plan, secretRef, secret string) string {
	bypass := slices.Clone(plan.LocalBypassHosts)
	slices.Sort(bypass)
	secretDigest := ""
	if secret != "" {
		digest := sha256.Sum256([]byte(secret))
		secretDigest = fmt.Sprintf("%x", digest[:])
	}
	canonical := struct {
		Version           string   `json:"version"`
		Mode              string   `json:"mode"`
		Engine            string   `json:"engine,omitempty"`
		DNSPolicy         string   `json:"dnsPolicy,omitempty"`
		MediatedResolver  string   `json:"mediatedResolver,omitempty"`
		LocalBypassHosts  []string `json:"localBypassHosts,omitempty"`
		ProxySecretRef    string   `json:"proxySecretRef,omitempty"`
		ProxySecretSHA256 string   `json:"proxySecretSHA256,omitempty"`
	}{
		Version:           "hideout.network-service-fingerprint/v1",
		Mode:              plan.Mode,
		Engine:            plan.Engine,
		DNSPolicy:         plan.DNSPolicy,
		MediatedResolver:  plan.MediatedResolver,
		LocalBypassHosts:  bypass,
		ProxySecretRef:    secretRef,
		ProxySecretSHA256: secretDigest,
	}
	data, _ := json.Marshal(canonical)
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:])
}

func BuildServiceState(environmentID string, plan Plan, status ServiceStatus, bootID string, startedAt time.Time, stateErr error) (ServiceState, error) {
	now := time.Now().UTC()
	if startedAt.IsZero() {
		startedAt = now
	}
	if now.Before(startedAt) {
		now = startedAt
	}
	state := ServiceState{
		Schema:                   EnvironmentServiceSchema,
		EnvironmentID:            environmentID,
		Kind:                     "network",
		Status:                   status,
		ConfigurationFingerprint: plan.ConfigurationFingerprint,
		ConfigurationID:          plan.ConfigurationID,
		Mode:                     plan.Mode,
		GatewayID:                plan.GatewayID,
		Resolver:                 plan.MediatedResolver,
		BootID:                   bootID,
		StartedAt:                startedAt.UTC(),
		UpdatedAt:                now,
	}
	if stateErr != nil {
		state.LastError = sanitizeServiceError(stateErr.Error())
	}
	if err := state.Validate(); err != nil {
		return ServiceState{}, err
	}
	return state, nil
}

func (s ServiceState) Validate() error {
	if s.Schema != EnvironmentServiceSchema {
		return fmt.Errorf("unsupported environment-service schema %q", s.Schema)
	}
	if !environmentIDPattern.MatchString(s.EnvironmentID) {
		return fmt.Errorf("invalid environment id %q", s.EnvironmentID)
	}
	if s.Kind != "network" {
		return fmt.Errorf("unsupported environment service kind %q", s.Kind)
	}
	switch s.Status {
	case ServiceStarting, ServiceSwitching, ServiceReady, ServiceCleaning, ServiceFailed:
	default:
		return fmt.Errorf("invalid environment service status %q", s.Status)
	}
	if len(s.ConfigurationFingerprint) != 64 || !lowerHex(s.ConfigurationFingerprint) {
		return errors.New("environment service configuration fingerprint is invalid")
	}
	if !strings.HasPrefix(s.ConfigurationID, "sha256:") || len(s.ConfigurationID) != len("sha256:")+64 || !lowerHex(strings.TrimPrefix(s.ConfigurationID, "sha256:")) {
		return errors.New("environment service configuration id is invalid")
	}
	if s.Mode != ModeDirect && s.Mode != ModeTun2Socks {
		return fmt.Errorf("unsupported environment service mode %q", s.Mode)
	}
	if strings.TrimSpace(s.GatewayID) == "" || len(s.GatewayID) > 128 || strings.ContainsAny(s.GatewayID, " \t\r\n") {
		return errors.New("environment service gateway identity is invalid")
	}
	if s.Mode == ModeTun2Socks && net.ParseIP(s.Resolver) == nil {
		return errors.New("privacy environment service resolver is invalid")
	}
	if s.BootID != "" && !serviceBootIDPattern.MatchString(s.BootID) {
		return errors.New("environment service boot identity is invalid")
	}
	if (s.Status == ServiceSwitching || s.Status == ServiceReady || s.Status == ServiceCleaning) && s.BootID == "" {
		return errors.New("switching, ready, or cleaning environment service must be bound to a guest boot identity")
	}
	if s.StartedAt.IsZero() || s.UpdatedAt.IsZero() || s.UpdatedAt.Before(s.StartedAt) {
		return errors.New("environment service timestamps are invalid")
	}
	if len(s.LastError) > 512 || sanitizeServiceError(s.LastError) != s.LastError {
		return errors.New("environment service error is not safely bounded")
	}
	return nil
}

func WriteServiceState(path string, state ServiceState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func LoadServiceState(path string) (ServiceState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ServiceState{}, err
	}
	if !info.Mode().IsRegular() {
		return ServiceState{}, errors.New("environment service state is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ServiceState{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state ServiceState
	if err := decoder.Decode(&state); err != nil {
		return ServiceState{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return ServiceState{}, errors.New("environment service state contains trailing data")
		}
		return ServiceState{}, err
	}
	if err := state.Validate(); err != nil {
		return ServiceState{}, err
	}
	return state, nil
}

func lowerHex(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func sanitizeServiceError(value string) string {
	value = audit.RedactString(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && r != '\u001b' {
			return r
		}
		return -1
	}, value)
	if len(value) <= 512 {
		return value
	}
	value = value[:512]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
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
		return rewriteNetworkGuestPaths(b.String(), plan)
	case ModeTun2Socks:
		if plan.FailClosed {
			fmt.Fprintf(&b, "echo %s >&2\n", shellQuote("hideout: "+plan.Reason))
			b.WriteString("exit 125\n")
			return rewriteNetworkGuestPaths(b.String(), plan)
		}
		b.WriteString("[ \"$(id -u)\" -eq 0 ] || { echo 'hideout: tun2socks setup requires the Hideout setup identity' >&2; exit 127; }\n")
		// The privileged network setup can run before the per-session shim dir is
		// mounted (environment network service path), so prepend the plan's helper
		// dir — where tun2socks and hideout-dns-stub are placed for this run — to
		// PATH. For a session run this is the session shim dir already on PATH; for
		// the environment service it is the mounted service dir.
		fmt.Fprintf(&b, "PATH=%s:\"$PATH\"\nexport PATH\n", shellQuote(plan.GuestHelperDir))
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
		b.WriteString("gateway_config=/run/hideout/network/tun2socks.yaml\n")
		b.WriteString("umask 077\n")
		b.WriteString("mkdir -p /run/hideout/network\n")
		b.WriteString("printf 'device: tun://hideout0\\nproxy: %s\\nloglevel: warn\\n' \"$proxy_url\" > \"$gateway_config\"\n")
		b.WriteString("chmod 0600 \"$gateway_config\"\n")
		b.WriteString("unset proxy_url\n")
		// Self-heal a retained privacy network from an unclean prior teardown or
		// an interrupted run on this boot: reconcile it to a clean direct slate
		// before capturing the baseline route and re-establishing. hideout0 only
		// exists on the boot that created it (a reboot clears it), so the saved
		// before-route is this boot's real direct route and is safe to restore.
		b.WriteString("if ip link show dev hideout0 >/dev/null 2>&1; then\n")
		b.WriteString("  if [ -r /hideout/session/network/tun2socks.pid ]; then kill \"$(sed -n '1p' /hideout/session/network/tun2socks.pid)\" 2>/dev/null || true; fi\n")
		b.WriteString("  if [ -r /hideout/session/network/dns-stub.pid ]; then kill \"$(sed -n '1p' /hideout/session/network/dns-stub.pid)\" 2>/dev/null || true; fi\n")
		b.WriteString("  if [ -s /hideout/session/network/default-route.before ]; then ip route replace $(sed -n '1p' /hideout/session/network/default-route.before) 2>/dev/null || true; fi\n")
		b.WriteString("  ip link set dev hideout0 down 2>/dev/null || true\n")
		b.WriteString("  ip tuntap del mode tun dev hideout0 2>/dev/null || ip link del dev hideout0 2>/dev/null || true\n")
		b.WriteString("fi\n")
		b.WriteString("default_route=$(ip route show default | head -n 1 || true)\n")
		b.WriteString("printf '%s\\n' \"$default_route\" > /hideout/session/network/default-route.before\n")
		b.WriteString("default_gw=$(printf '%s\\n' \"$default_route\" | awk '{for (i=1;i<=NF;i++) if ($i==\"via\") print $(i+1)}')\n")
		b.WriteString("default_dev=$(printf '%s\\n' \"$default_route\" | awk '{for (i=1;i<=NF;i++) if ($i==\"dev\") print $(i+1)}')\n")
		b.WriteString("[ -n \"$default_dev\" ] || { echo 'hideout: default network device not found' >&2; exit 127; }\n")
		for i, host := range plan.LocalBypassHosts {
			writeLocalBypassSetup(&b, i, host)
		}
		b.WriteString("proxy_url=$(sed -n 's/^proxy: //p' \"$gateway_config\")\n")
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
		b.WriteString("proxy_route_existing=$(ip route show \"$proxy_route_host/32\" 2>/dev/null | head -n 1 || true)\n")
		// Any stale hideout0 was reconciled above; a residual device here means the
		// reconcile could not clear it, so creation fails closed rather than
		// silently adopting foreign guest state.
		b.WriteString("ip tuntap add mode tun dev hideout0 || { echo 'hideout: hideout0 creation failed' >&2; exit 127; }\n")
		b.WriteString("ip addr add 198.18.0.1/15 dev hideout0 || { echo 'hideout: hideout0 address setup failed' >&2; exit 127; }\n")
		b.WriteString("ip link set dev hideout0 up\n")
		b.WriteString("if [ -n \"$proxy_route_existing\" ]; then\n")
		b.WriteString("  : # Preserve an existing host-specific route owned by the guest network.\n")
		b.WriteString("elif [ -n \"$default_gw\" ] && [ \"$proxy_route_host\" != \"$default_gw\" ]; then\n")
		b.WriteString("  ip route replace \"$proxy_route_host\" via \"$default_gw\" dev \"$default_dev\" || { echo 'hideout: proxy endpoint route setup failed' >&2; exit 127; }\n")
		b.WriteString("else\n")
		b.WriteString("  ip route replace \"$proxy_route_host\" dev \"$default_dev\" || { echo 'hideout: proxy endpoint route setup failed' >&2; exit 127; }\n")
		b.WriteString("fi\n")
		b.WriteString("tun2socks -config \"$gateway_config\" > /hideout/session/network/tun2socks.log 2>&1 &\n")
		b.WriteString("unset proxy_url proxy_authority\n")
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
		return rewriteNetworkGuestPaths(b.String(), plan)
	default:
		fmt.Fprintf(&b, "echo %s >&2\n", shellQuote("hideout: unsupported network mode "+plan.Mode))
		b.WriteString("exit 125\n")
		return rewriteNetworkGuestPaths(b.String(), plan)
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
		b.WriteString("tun2socks_stop_failed=0\n")
		b.WriteString("tun2socks_alive() {\n")
		b.WriteString("  kill -0 \"$pid\" 2>/dev/null || return 1\n")
		b.WriteString("  state=$(awk '{print $3}' \"/proc/$pid/stat\" 2>/dev/null || true)\n")
		b.WriteString("  [ \"$state\" != Z ]\n")
		b.WriteString("}\n")
		b.WriteString("if [ -r /hideout/session/network/tun2socks.pid ]; then\n")
		b.WriteString("  pid=$(sed -n '1p' /hideout/session/network/tun2socks.pid)\n")
		b.WriteString("  if [ -n \"$pid\" ]; then\n")
		b.WriteString("    kill \"$pid\" 2>/dev/null || true\n")
		b.WriteString("    i=0\n")
		b.WriteString("    while tun2socks_alive && [ \"$i\" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done\n")
		b.WriteString("    if tun2socks_alive; then\n")
		b.WriteString("      kill -KILL \"$pid\" 2>/dev/null || true\n")
		b.WriteString("      i=0\n")
		b.WriteString("      while tun2socks_alive && [ \"$i\" -lt 10 ]; do sleep 0.1; i=$((i + 1)); done\n")
		b.WriteString("    fi\n")
		b.WriteString("    if tun2socks_alive; then echo 'hideout: tun2socks did not stop during cleanup' >&2; tun2socks_stop_failed=1; fi\n")
		b.WriteString("  fi\n")
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
		b.WriteString("rm -f /hideout/session/network/tun2socks.pid /hideout/session/network/proxy.url /run/hideout/network/tun2socks.yaml\n")
		b.WriteString("write_status 'tun2socks cleanup complete'\n")
		b.WriteString("[ \"$tun2socks_stop_failed\" -eq 0 ] || exit 1\n")
	default:
		b.WriteString("write_status 'network cleanup complete'\n")
	}
	return rewriteNetworkGuestPaths(b.String(), plan)
}

func rewriteNetworkGuestPaths(script string, plan Plan) string {
	guestNetworkDir := filepath.ToSlash(filepath.Dir(plan.GuestBootstrapPath))
	if guestNetworkDir == "." || guestNetworkDir == "" {
		guestNetworkDir = "/hideout/session/network"
	}
	script = strings.ReplaceAll(script, "/hideout/session/network", guestNetworkDir)
	if strings.HasPrefix(guestNetworkDir, "/hideout/runtime/services/") {
		script = strings.ReplaceAll(script, "/hideout/session/shims/hideout-dns-stub", guestNetworkDir+"/hideout-dns-stub")
	}
	return script
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
	fmt.Fprintf(b, "%s_route_existing=$(ip route show \"$%s_route_host/32\" 2>/dev/null | head -n 1 || true)\n", prefix, prefix)
	fmt.Fprintf(b, "if [ -n \"$%s_route_existing\" ]; then\n", prefix)
	b.WriteString("  : # Preserve an existing host-specific route owned by the guest network.\n")
	fmt.Fprintf(b, "elif [ -n \"$default_gw\" ] && [ \"$%s_route_host\" != \"$default_gw\" ]; then\n", prefix)
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
	b.WriteString("command -v hideout-dns-stub >/dev/null 2>&1 || { echo 'hideout: hideout-dns-stub command missing' >&2; exit 127; }\n")
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
	fmt.Fprintf(b, "hideout-dns-stub --listen %s --doh-server \"$mediated_resolver\" > /hideout/session/network/dns-stub.log 2>&1 &\n", dnsStubAddr)
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
