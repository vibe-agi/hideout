package lima

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/portbridge"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestPrepareWritesLimaYAML(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.InstanceName != "hideout-client-a-session-1" {
		t.Fatalf("instance=%s", session.InstanceName)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode: %v\n%s", err, data)
	}
	if cfg.VMType != "vz" || cfg.MountType != "virtiofs" || !cfg.MountInotify {
		t.Fatalf("unexpected VM settings: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Base, []string{"template:_images/ubuntu-lts"}) {
		t.Fatalf("lima config should inherit image metadata without default mounts: %+v", cfg.Base)
	}
	if !reflect.DeepEqual(cfg.PortForwards, []portForward{
		{
			GuestIP:           "0.0.0.0",
			GuestIPMustBeZero: boolPtr(false),
			Proto:             "any",
			GuestPortRange:    [2]int{1, 65535},
			Ignore:            true,
		},
		{
			GuestIP:        "127.0.0.1",
			Proto:          "any",
			GuestPortRange: [2]int{1, 65535},
			Ignore:         true,
		},
	}) {
		t.Fatalf("lima config must disable automatic guest loopback forwarding: %+v", cfg.PortForwards)
	}
	for _, forbidden := range []string{"template://", "template:ubuntu-lts", "template:_default/mounts"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("lima config should not include %s:\n%s", forbidden, data)
		}
	}
	if cfg.User.Name != spec.Machine.Profile.Identity.User ||
		cfg.User.Home != "/home/"+spec.Machine.Profile.Identity.User ||
		cfg.User.UID != 1000 ||
		cfg.User.Shell != "/bin/bash" {
		t.Fatalf("lima config should pin guest OS user identity for os.userInfo: %+v", cfg.User)
	}
	if len(cfg.Provision) != 1 {
		t.Fatalf("unexpected provision scripts: %+v", cfg.Provision)
	}
	if strings.Contains(cfg.Provision[0].Script, "apt-get") || strings.Contains(cfg.Provision[0].Script, "curl ") {
		t.Fatalf("provision must not perform network package installation before session network policy: %s", cfg.Provision[0].Script)
	}
	if strings.Contains(cfg.Provision[0].Script, "rm -rf \"$profile_home\"") ||
		strings.Contains(cfg.Provision[0].Script, "ln -s /hideout/profile/home") {
		t.Fatalf("provision must not replace Lima login home and break SSH authorization: %s", cfg.Provision[0].Script)
	}
	for _, want := range []string{
		"target_user='developer'",
		"/root/.ssh/authorized_keys",
		"PermitRootLogin prohibit-password",
		"gpasswd -d \"$target_user\" sudo",
		"/etc/sudoers.d/99-hideout-target-no-sudo",
		"$target_user\" > /etc/sudoers.d/99-hideout-target-no-sudo",
	} {
		if !strings.Contains(cfg.Provision[0].Script, want) {
			t.Fatalf("provision missing setup separation fragment %q:\n%s", want, cfg.Provision[0].Script)
		}
	}
	if !strings.Contains(cfg.Provision[0].Script, "printf '%s\\n' 'devbox' > /etc/hostname") {
		t.Fatalf("provision should set profile hostname: %s", cfg.Provision[0].Script)
	}
	if !strings.Contains(cfg.Provision[0].Script, "/hideout/profile/machine/machine-id") ||
		!strings.Contains(cfg.Provision[0].Script, "/etc/machine-id") {
		t.Fatalf("provision should set guest machine-id from profile identity state: %s", cfg.Provision[0].Script)
	}
	wantMounts := []mount{
		{Location: spec.Workspace.HostRoot, MountPoint: spec.Workspace.GuestRoot, Writable: true},
		{Location: filepath.Join(spec.Machine.ProfileDir, "home"), MountPoint: GuestProfileDir + "/home", Writable: true},
		{Location: filepath.Join(spec.Machine.ProfileDir, "cache"), MountPoint: GuestProfileDir + "/cache", Writable: true},
		{Location: filepath.Join(spec.Machine.ProfileDir, "config"), MountPoint: GuestProfileDir + "/config", Writable: true},
		{Location: filepath.Join(spec.Machine.ProfileDir, "data"), MountPoint: GuestProfileDir + "/data", Writable: true},
		{Location: filepath.Join(spec.Machine.ProfileDir, "browser"), MountPoint: GuestProfileDir + "/browser", Writable: true},
		{Location: filepath.Join(spec.Machine.ProfileDir, "machine"), MountPoint: GuestProfileDir + "/machine", Writable: false},
		{Location: filepath.Join(spec.SessionDir, "tmp"), MountPoint: GuestSessionDir + "/tmp", Writable: true},
		{Location: filepath.Join(spec.SessionDir, "shims"), MountPoint: GuestSessionDir + "/shims", Writable: true},
		{Location: filepath.Join(spec.SessionDir, "network"), MountPoint: GuestSessionDir + "/network", Writable: true},
		{Location: filepath.Join(spec.SessionDir, "bootstrap"), MountPoint: GuestSessionDir + "/bootstrap", Writable: true},
	}
	if !reflect.DeepEqual(cfg.Mounts, wantMounts) {
		t.Fatalf("mounts=%+v want %+v", cfg.Mounts, wantMounts)
	}
	blockedMounts := map[string]string{
		"/var/run/docker.sock": "docker socket",
	}
	if home := os.Getenv("HOME"); home != "" {
		blockedMounts[home] = "real home"
		blockedMounts[filepath.Join(home, ".docker")] = "docker config"
	}
	for _, m := range cfg.Mounts {
		if label, blocked := blockedMounts[m.Location]; blocked {
			t.Fatalf("lima config must not mount %s: %+v", label, cfg.Mounts)
		}
		switch m.Location {
		case spec.Machine.ProfileDir, filepath.Join(spec.Machine.ProfileDir, "profile.json"), filepath.Join(spec.Machine.ProfileDir, "identity.json"), filepath.Join(spec.Machine.ProfileDir, "policy"):
			t.Fatalf("lima config must not expose profile control-plane path as a guest mount: %+v", m)
		}
		switch m.Location {
		case spec.SessionDir, filepath.Join(spec.SessionDir, "audit.jsonl"), filepath.Join(spec.SessionDir, "lima.yaml"), filepath.Join(spec.SessionDir, "guest-bootstrap.json"), filepath.Join(spec.SessionDir, "broker-endpoint.json"), filepath.Join(spec.SessionDir, "network-plan.json"):
			t.Fatalf("lima config must not expose session control-plane path as a guest mount: %+v", m)
		}
	}
	bootstrap, err := os.ReadFile(session.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if !bytes.Contains(bootstrap, []byte("/hideout/session/shims/hideout-shim")) {
		t.Fatalf("bootstrap missing shim check:\n%s", bootstrap)
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	if !bytes.Contains(manifest, []byte(`"commandProxyShims"`)) {
		t.Fatalf("manifest missing command proxy shims: %s", manifest)
	}
	if !bytes.Contains(manifest, []byte(`"guestBootstrap": "/hideout/session/bootstrap/bootstrap.sh"`)) {
		t.Fatalf("manifest missing guest bootstrap path: %s", manifest)
	}
	if !bytes.Contains(manifest, []byte(`"identityId": "id_abcdef1234567890"`)) ||
		!bytes.Contains(manifest, []byte(`"identityMode": "persistent"`)) ||
		!bytes.Contains(manifest, []byte(`"instanceName": "hideout-client-a-session-1"`)) {
		t.Fatalf("manifest missing profile identity binding: %s", manifest)
	}
}

func TestPrepareDoesNotPersistHostToGuestPortForward(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.PortBridges = []backend.PortBridgeEndpoint{{
		ID:               "ep_manual_preview_1",
		Owner:            "preview.open",
		Action:           "endpoint.expose.host-to-guest",
		Source:           "manual",
		ClosePolicy:      "session-end",
		Lifetime:         "run",
		Direction:        "host-to-guest",
		ListenScope:      "loopback",
		ListenAddress:    "127.0.0.1:49152",
		TargetScope:      "guest",
		TargetAddress:    "127.0.0.1:5173",
		EndpointCategory: "host-loopback",
	}}
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(cfg.PortForwards, []portForward{
		{
			GuestIP:           "0.0.0.0",
			GuestIPMustBeZero: boolPtr(false),
			Proto:             "any",
			GuestPortRange:    [2]int{1, 65535},
			Ignore:            true,
		},
		{
			GuestIP:        "127.0.0.1",
			Proto:          "any",
			GuestPortRange: [2]int{1, 65535},
			Ignore:         true,
		},
	}) {
		t.Fatalf("lima config must not persist run-scoped host-to-guest forwards: %+v", cfg.PortForwards)
	}
	for _, forward := range cfg.PortForwards {
		if !forward.Ignore {
			t.Fatalf("default non-owned port forward must remain ignored: %+v", cfg.PortForwards)
		}
	}
}

func TestStartHostToGuestBridgeUsesSSHDirectTCPIP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LIMA_HOME", "")
	clientSigner, clientKey := testSSHSigner(t)
	hostSigner, _ := testSSHSigner(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ssh: %v", err)
	}
	defer listener.Close()
	targets := make(chan directTCPIPTarget, 1)
	startDirectTCPIPSSHServer(t, listener, hostSigner, clientSigner.PublicKey(), targets)
	identityPath := filepath.Join(home, "id_lima")
	if err := os.WriteFile(identityPath, clientKey, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	_, sshPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split ssh addr: %v", err)
	}
	configDir := filepath.Join(home, ".lima", "hideout-test")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir ssh config dir: %v", err)
	}
	sshConfig := strings.Join([]string{
		"Host hideout-test",
		"  HostName 127.0.0.1",
		"  Port " + sshPort,
		"  User lima",
		"  IdentityFile " + strconv.Quote(identityPath),
		"  StrictHostKeyChecking no",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "ssh.config"), []byte(sshConfig), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}
	bridge, err := (Backend{}).StartHostToGuestBridge(context.Background(), "hideout-test", "/workspace", []string{"PATH=/usr/bin:/bin"}, testPortBridgeSpec("127.0.0.1:0", "127.0.0.1:5173"))
	if err != nil {
		t.Fatalf("StartHostToGuestBridge: %v", err)
	}
	defer bridge.Close()
	conn, err := net.Dial("tcp", bridge.ListenAddress())
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	tcpConn := conn.(*net.TCPConn)
	if _, err := tcpConn.Write([]byte("ping")); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	if err := tcpConn.CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	got, err := io.ReadAll(tcpConn)
	if err != nil {
		t.Fatalf("read bridge: %v", err)
	}
	if string(got) != "guest:ping" {
		t.Fatalf("bridge response=%q", got)
	}
	target := <-targets
	if target.Host != "127.0.0.1" || target.Port != 5173 {
		t.Fatalf("direct-tcpip target mismatch: %+v", target)
	}
}

func TestLimaHostKeyCallbackDefaultConfigIsLoopbackOnly(t *testing.T) {
	hostSigner, _ := testSSHSigner(t)
	callback, err := (limaSSHConfig{
		UserKnownHostsFile:               os.DevNull,
		StrictHostKeyChecking:            "no",
		NoHostAuthenticationForLocalhost: "yes",
	}).hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := callback("127.0.0.1:60022", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 60022}, hostSigner.PublicKey()); err != nil {
		t.Fatalf("loopback host key callback rejected default Lima endpoint: %v", err)
	}
	if err := callback("192.0.2.10:22", &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}, hostSigner.PublicKey()); err == nil {
		t.Fatalf("loopback host key callback accepted non-loopback endpoint")
	}
}

func TestPrepareMountsEnvironmentRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.SessionDir = filepath.Join(root, "environments", spec.Machine.EnvironmentID, "runtime")
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode: %v\n%s", err, data)
	}
	foundRoot := false
	for _, m := range cfg.Mounts {
		if m.Location == spec.SessionDir && m.MountPoint == GuestSessionDir && m.Writable {
			foundRoot = true
		}
		for _, subdir := range []string{"tmp", "shims", "network", "bootstrap"} {
			if m.Location == filepath.Join(spec.SessionDir, subdir) || m.MountPoint == GuestSessionDir+"/"+subdir {
				t.Fatalf("environment runtime should mount stable root, not child runtime mount: %+v", m)
			}
		}
	}
	if !foundRoot {
		t.Fatalf("environment runtime root mount missing in %+v", cfg.Mounts)
	}
}

func TestPrepareUsesSessionScopedInstanceForEphemeralIdentity(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.IdentityMode = "ephemeral"
	spec.Machine.IdentityRoot = filepath.Join(spec.SessionDir, "identity")
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.InstanceName != "hideout-client-a-session-1" {
		t.Fatalf("instance=%s", session.InstanceName)
	}
	if session.IdentityMode != "ephemeral" || session.IdentityRoot != spec.Machine.IdentityRoot {
		t.Fatalf("session identity binding mismatch: %+v spec=%+v", session, spec)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode: %v\n%s", err, data)
	}
	for _, subdir := range []string{"home", "cache", "config", "data", "browser", "machine"} {
		want := filepath.Join(spec.Machine.IdentityRoot, subdir)
		found := false
		for _, m := range cfg.Mounts {
			if m.Location == filepath.Join(spec.Machine.ProfileDir, subdir) {
				t.Fatalf("ephemeral mount should use identity root, not persistent profile dir: %+v", m)
			}
			if m.Location == want && m.MountPoint == GuestProfileDir+"/"+subdir {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ephemeral identity mount missing %s in %+v", want, cfg.Mounts)
		}
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	for _, want := range []string{
		`"identityMode": "ephemeral"`,
		`"identityRoot": "` + spec.Machine.IdentityRoot + `"`,
		`"instanceName": "hideout-client-a-session-1"`,
	} {
		if !bytes.Contains(manifest, []byte(want)) {
			t.Fatalf("manifest missing %s: %s", want, manifest)
		}
	}
}

func TestPrepareUsesProfileCommandProxyShims(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	delete(spec.Machine.Profile.CommandProxy.Commands, "xdg-open")
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	bootstrap, err := os.ReadFile(session.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if bytes.Contains(bootstrap, []byte("/hideout/session/shims/xdg-open")) {
		t.Fatalf("bootstrap should not require omitted xdg-open shim:\n%s", bootstrap)
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	if bytes.Contains(manifest, []byte(`"xdg-open"`)) {
		t.Fatalf("manifest should not include omitted xdg-open shim: %s", manifest)
	}
}

func TestPrepareDoesNotProvisionToolsFromLegacyFields(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.Profile.Tools.Presets = []string{"base-dev", "node-dev"}
	spec.Machine.Profile.Tools.NPMGlobals = []profile.NPMGlobalPackage{{
		Package:  "@example/agent-cli@1.2.3",
		Commands: []string{"agent-cli"},
	}}
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode: %v\n%s", err, data)
	}
	if len(cfg.Provision) != 1 {
		t.Fatalf("tool provisioning must not be embedded in lima.yaml: %+v", cfg.Provision)
	}
	if strings.Contains(cfg.Provision[0].Script, "apt-get") || strings.Contains(cfg.Provision[0].Script, "npm install") {
		t.Fatalf("lima yaml provision must not perform network tool setup: %+v", cfg.Provision)
	}
	bootstrap, err := os.ReadFile(session.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	bootstrapText := string(bootstrap)
	for _, forbidden := range []string{
		"apt-get install",
		"npm install",
		"required guest command missing: node",
		"required guest command missing: agent-cli",
	} {
		if strings.Contains(bootstrapText, forbidden) {
			t.Fatalf("bootstrap must not include tool provisioning/check %q:\n%s", forbidden, bootstrapText)
		}
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	for _, forbidden := range []string{`"presets"`, `"npmGlobals"`, `"node-dev"`, `"@example/agent-cli@1.2.3"`} {
		if bytes.Contains(manifest, []byte(forbidden)) {
			t.Fatalf("manifest must not include legacy tool data %s: %s", forbidden, manifest)
		}
	}
	if bytes.Contains(manifest, []byte("npm install")) {
		t.Fatalf("manifest must not embed provision script: %s", manifest)
	}
}

func TestGeneratedYAMLValidatesWithLimactlWhenAvailable(t *testing.T) {
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl not installed")
	}
	root := t.TempDir()
	session, err := (Backend{Runner: fakeRunner{lookPath: limactl}}).Prepare(context.Background(), testRunSpec(root))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cmd := exec.Command(limactl, "validate", session.ConfigPath)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("limactl validate: %v\n%s", err, data)
	}
}

func TestGeneratedYAMLUsesImageOnlyBaseWithoutDefaultHomeMount(t *testing.T) {
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl not installed")
	}
	resolvedLimactl, err := filepath.EvalSymlinks(limactl)
	if err != nil {
		t.Fatalf("resolve limactl: %v", err)
	}
	templatePath := filepath.Join(filepath.Dir(filepath.Dir(resolvedLimactl)), "share", "lima", "templates", "_images", "ubuntu-lts.yaml")
	templateData, err := os.ReadFile(templatePath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("installed Lima does not expose its image-only template at %s; real Gate 2 owns final mount observation", templatePath)
	}
	if err != nil {
		t.Fatalf("read Lima image-only template: %v", err)
	}
	var imageBase limaConfig
	if err := yaml.Unmarshal(templateData, &imageBase); err != nil {
		t.Fatalf("decode Lima image-only template: %v", err)
	}
	if len(imageBase.Images) == 0 {
		t.Fatalf("Lima image-only template has no images: %s", templatePath)
	}
	if len(imageBase.Mounts) != 0 {
		t.Fatalf("Lima image-only template unexpectedly grants mounts: %+v", imageBase.Mounts)
	}

	root := t.TempDir()
	session, err := (Backend{Runner: fakeRunner{lookPath: limactl}}).Prepare(context.Background(), testRunSpec(root))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode generated config: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(cfg.Base, []string{"template:_images/ubuntu-lts"}) {
		t.Fatalf("generated config must inherit only Lima image metadata: %+v", cfg.Base)
	}
	for _, m := range cfg.Mounts {
		if m.Location == "~" || m.Location == os.Getenv("HOME") {
			t.Fatalf("generated config includes host home mount: %+v\n%s", m, data)
		}
	}
}

func TestGuestEnvRewritesHostPaths(t *testing.T) {
	got := GuestEnv([]string{
		"HOME=/Users/alice/.hideout/profiles/default/home",
		"TMPDIR=/Users/alice/.hideout/sessions/s1/tmp",
		"XDG_CONFIG_HOME=/Users/alice/.hideout/profiles/default/config",
		"XDG_CACHE_HOME=/Users/alice/.hideout/profiles/default/cache",
		"XDG_DATA_HOME=/Users/alice/.hideout/profiles/default/data",
		"GIT_CONFIG_GLOBAL=/Users/alice/.hideout/profiles/default/home/.gitconfig",
		"PATH=/host/shims:/usr/bin",
		"HOSTNAME=devbox",
		"LANG=en_US.UTF-8",
	})
	want := []string{
		"HOME=/hideout/profile/home",
		"TMPDIR=/hideout/session/tmp",
		"XDG_CONFIG_HOME=/hideout/profile/config",
		"XDG_CACHE_HOME=/hideout/profile/cache",
		"XDG_DATA_HOME=/hideout/profile/data",
		"GIT_CONFIG_GLOBAL=/hideout/profile/home/.gitconfig",
		"PATH=/hideout/session/shims:/hideout/profile/home/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOSTNAME=devbox",
		"LANG=en_US.UTF-8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GuestEnv=%v want %v", got, want)
	}
}

func TestGuestRuntimePathIsShimsThenDurableUserThenSystem(t *testing.T) {
	env := GuestEnv([]string{"PATH=/Users/alice/bin:/opt/homebrew/bin:/usr/bin"})
	got := backend.EnvValue(env, "PATH")
	want := "/hideout/session/shims:/hideout/profile/home/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if got != want {
		t.Fatalf("guest PATH=%q want %q", got, want)
	}
	if strings.Contains(got, "/Users/alice") || strings.Contains(got, "/opt/homebrew") {
		t.Fatalf("guest PATH imported host path: %q", got)
	}
	bootstrap := BootstrapScript(nil)
	if !strings.Contains(bootstrap, "mkdir -p /hideout/profile/home/.local/bin") {
		t.Fatalf("bootstrap does not create durable user bin: %s", bootstrap)
	}
}

func TestGuestEnvMapsSessionGitSnapshotWithoutExposingHostPath(t *testing.T) {
	env := GuestEnv([]string{
		"GIT_CONFIG_GLOBAL=/Users/alice/.hideout/environments/env_1/runtime/sessions/ses_1/identity/gitconfig",
	})
	if got := backend.EnvValue(env, "GIT_CONFIG_GLOBAL"); got != "/hideout/session/identity/gitconfig" {
		t.Fatalf("guest session Git config=%q", got)
	}
}

func TestGuestBrokerEndpointUsesLimaHostAlias(t *testing.T) {
	got, err := GuestBrokerEndpoint(broker.TCPEndpoint("0.0.0.0:4321"))
	if err != nil {
		t.Fatalf("GuestBrokerEndpoint: %v", err)
	}
	if got.String() != "tcp://host.lima.internal:4321" {
		t.Fatalf("endpoint=%s", got.String())
	}
}

func TestRunProbesGuestPrivilegeBeforeTargetCommand(t *testing.T) {
	statusEmitted := false
	runner := &privilegeProbeRunner{statusEmitted: &statusEmitted}
	session := &backend.Session{
		ID:           "ses_1",
		Backend:      "lima",
		ConfigPath:   "/tmp/lima.yaml",
		GuestWork:    "/workspace",
		GuestHome:    "/home/hideout",
		InstanceName: "hideout-test",
		PrivilegeStatusSink: func(status privilege.Status) error {
			statusEmitted = true
			if status.Status != privilege.StatusEnforced {
				t.Fatalf("status=%q want enforced: %+v", status.Status, status)
			}
			return nil
		},
	}
	if err := (Backend{Runner: runner}).Run(context.Background(), session, []string{"echo", "ok"}, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !statusEmitted {
		t.Fatal("privilege status was not emitted")
	}
	if runner.targetBeforeStatus {
		t.Fatal("target command ran before privilege status emission")
	}
	if len(runner.probeWorkdirs) == 0 {
		t.Fatal("no privilege probe workdirs were captured")
	}
	for _, workdir := range runner.probeWorkdirs {
		// Probes run during activation, before any per-session workspace view
		// exists in shared machines; a workspace working directory would fail
		// every check with "cd: no such file or directory".
		if workdir != "/" {
			t.Fatalf("privilege probe used workspace-dependent workdir %q", workdir)
		}
	}
}

func TestWithProgressRedirectsOnlyTheProgressWriter(t *testing.T) {
	var buf bytes.Buffer
	base := Backend{Progress: io.Discard, Stdout: os.Stdout, ControlStdout: io.Discard}
	redirector, ok := any(base).(backend.ProgressRedirector)
	if !ok {
		t.Fatal("lima backend must implement backend.ProgressRedirector")
	}
	replaced, ok := redirector.WithProgress(&buf).(Backend)
	if !ok {
		t.Fatal("WithProgress must return a lima backend")
	}
	if replaced.Progress != io.Writer(&buf) {
		t.Fatalf("progress writer was not replaced: %T", replaced.Progress)
	}
	if base.Progress != io.Discard {
		t.Fatal("WithProgress mutated the original backend")
	}
	if replaced.Stdout != base.Stdout || replaced.ControlStdout != base.ControlStdout {
		t.Fatal("WithProgress changed unrelated writers")
	}
}

func TestRunReportsDegradedWhenTargetCanPasswordlessSudo(t *testing.T) {
	runner := &privilegeProbeRunner{sudoSucceeds: true}
	var got privilege.Status
	session := &backend.Session{
		ID:           "ses_1",
		Backend:      "lima",
		ConfigPath:   "/tmp/lima.yaml",
		GuestWork:    "/workspace",
		GuestHome:    "/home/hideout",
		InstanceName: "hideout-test",
		PrivilegeStatusSink: func(status privilege.Status) error {
			got = status
			return nil
		},
	}
	if err := (Backend{Runner: runner}).Run(context.Background(), session, []string{"true"}, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != privilege.StatusDegraded || !strings.Contains(got.Reason, "passwordless sudo") {
		t.Fatalf("status=%+v want degraded passwordless sudo", got)
	}
}

func TestRunReportsUnknownWhenPrivilegeProbeAmbiguous(t *testing.T) {
	runner := &privilegeProbeRunner{uidOutput: "not-a-uid\n"}
	var got privilege.Status
	session := &backend.Session{
		ID:           "ses_1",
		Backend:      "lima",
		ConfigPath:   "/tmp/lima.yaml",
		GuestWork:    "/workspace",
		GuestHome:    "/home/hideout",
		InstanceName: "hideout-test",
		PrivilegeStatusSink: func(status privilege.Status) error {
			got = status
			return nil
		},
	}
	if err := (Backend{Runner: runner}).Run(context.Background(), session, []string{"true"}, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != privilege.StatusUnknown {
		t.Fatalf("status=%+v want unknown", got)
	}
}

func TestRunBuildsStartAndShellCommands(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.invalid:8080")
	t.Setenv("HIDEOUT_SECRET_DEFAULT_PROXY", "socks5://user:pass@127.0.0.1:1080")
	t.Setenv("SERVICE_TOKEN", "secret")
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	runEnv := []string{
		"HOME=/hideout/profile/home",
		"PATH=/hideout/session/shims:/usr/bin",
		"SERVICE_TOKEN=secret",
		"HIDEOUT_BROKER_ENDPOINT=tcp://127.0.0.1:1",
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, runEnv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.calls) != 5 {
		t.Fatalf("calls=%+v", runner.calls)
	}
	if !reflect.DeepEqual(runner.calls[0].args, []string{"start", "--tty=false", "--name", session.InstanceName, session.ConfigPath}) {
		t.Fatalf("start args=%v", runner.calls[0].args)
	}
	wantNetwork := []string{
		"shell", "--tty=false", "--workdir", GuestSessionDir + "/tmp", session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", GuestSessionDir + "/network/bootstrap.sh",
	}
	if !reflect.DeepEqual(runner.calls[1].args, wantNetwork) {
		t.Fatalf("network bootstrap args=%v want %v", runner.calls[1].args, wantNetwork)
	}
	wantBootstrap := []string{
		"shell", "--tty=false", "--workdir", GuestSessionDir + "/tmp", session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", GuestBootstrapPath,
	}
	if !reflect.DeepEqual(runner.calls[2].args, wantBootstrap) {
		t.Fatalf("bootstrap args=%v want %v", runner.calls[2].args, wantBootstrap)
	}
	wantCheck := []string{
		"shell", "--tty=false", "--workdir", spec.Workspace.GuestRoot, session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", "SERVICE_TOKEN=secret", "HIDEOUT_BROKER_ENDPOINT=tcp://127.0.0.1:1", "sh", "-c", "command -v \"$1\" >/dev/null 2>&1 || exit 127", "hideout-command-check", "sh",
	}
	if !reflect.DeepEqual(runner.calls[3].args, wantCheck) {
		t.Fatalf("command check args=%v want %v", runner.calls[3].args, wantCheck)
	}
	wantShell := []string{
		"shell", "--tty=false", "--workdir", spec.Workspace.GuestRoot, session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", "SERVICE_TOKEN=secret", "HIDEOUT_BROKER_ENDPOINT=tcp://127.0.0.1:1", "sh", "-c", "pwd",
	}
	if !reflect.DeepEqual(runner.calls[4].args, wantShell) {
		t.Fatalf("shell args=%v want %v", runner.calls[4].args, wantShell)
	}
	for _, index := range []int{1, 2} {
		setupArgs := strings.Join(runner.calls[index].args, "\n")
		if strings.Contains(setupArgs, "SERVICE_TOKEN=") || strings.Contains(setupArgs, "HIDEOUT_BROKER_ENDPOINT=") {
			t.Fatalf("setup call %d leaked target env:\n%s", index, setupArgs)
		}
	}
	for _, call := range runner.calls {
		hostEnv := strings.Join(call.env, "\n")
		if strings.Contains(hostEnv, "HTTP_PROXY=") || strings.Contains(hostEnv, "HIDEOUT_SECRET_") || strings.Contains(hostEnv, "SERVICE_TOKEN=") {
			t.Fatalf("limactl host env leaked sensitive values: %s", hostEnv)
		}
	}
}

func TestTerminalBridgeCommandPreservesStructuredArgv(t *testing.T) {
	got := terminalBridgeCommand([]string{"tool", "a'b; touch /tmp/should-not-run", "line 2"}, 132, 43)
	want := []string{
		"script",
		"-qefc",
		"stty rows 43 cols 132 2>/dev/null || true; exec 'tool' 'a'\"'\"'b; touch /tmp/should-not-run' 'line 2'",
		"/dev/null",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal bridge argv=%q want %q", got, want)
	}
}

func TestTerminalBridgeAvailabilityUsesGuestProbe(t *testing.T) {
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", terminalBridge: true}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session := &backend.Session{InstanceName: "hideout-test", GuestWork: "/workspace"}
	if !b.terminalBridgeAvailable(context.Background(), runner, []string{"PATH=/usr/bin"}, session, []string{"PATH=/usr/bin"}) {
		t.Fatal("expected script(1) guest probe to enable terminal bridge")
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0].args, " "), "command -v script") {
		t.Fatalf("unexpected terminal bridge probe calls: %+v", runner.calls)
	}

	runner = &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	if b.terminalBridgeAvailable(context.Background(), runner, []string{"PATH=/usr/bin"}, session, []string{"PATH=/usr/bin"}) {
		t.Fatal("missing script(1) must retain the legacy terminal path")
	}
}

func TestNonTerminalWriterHidesFileDescriptorFromLimactl(t *testing.T) {
	w := nonTerminalWriter{Writer: os.Stdout}
	if _, ok := any(w).(interface{ Fd() uintptr }); ok {
		t.Fatal("terminal transport writer must not expose an fd to the child process")
	}
}

func TestRunUsesSetupIdentityForPrivilegedNetworkBootstrap(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.NetworkPrivilegedSetup = true
	spec.PrivilegedSetupRequired = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	setup := &recordingSetupRunner{}
	b := Backend{Runner: runner, SetupRunner: setup, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	runEnv := []string{
		"HOME=/hideout/profile/home",
		"PATH=/hideout/session/shims:/usr/bin",
		"SERVICE_TOKEN=secret",
		"HIDEOUT_BROKER_ENDPOINT=tcp://127.0.0.1:1",
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, runEnv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(setup.calls) != 2 {
		t.Fatalf("setup calls=%+v", setup.calls)
	}
	if !setup.calls[0].check {
		t.Fatalf("first setup call should prove setup identity: %+v", setup.calls)
	}
	gotNetwork := setup.calls[1]
	if gotNetwork.workdir != GuestSessionDir+"/tmp" || !reflect.DeepEqual(gotNetwork.command, []string{GuestNetworkBootstrap}) {
		t.Fatalf("network setup call=%+v", gotNetwork)
	}
	setupText := strings.Join(gotNetwork.env, "\n")
	if strings.Contains(setupText, "SERVICE_TOKEN=") || strings.Contains(setupText, "HIDEOUT_BROKER_ENDPOINT=") {
		t.Fatalf("network setup leaked target env: %s", setupText)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("target runner calls=%+v", runner.calls)
	}
	if strings.Contains(strings.Join(runner.calls[1].args, "\x00"), GuestNetworkBootstrap) {
		t.Fatalf("privileged network bootstrap must not use target shell: %+v", runner.calls)
	}
}

func TestRunFailsClosedWhenSetupIdentityUnavailable(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.NetworkPrivilegedSetup = true
	spec.PrivilegedSetupRequired = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	setup := &recordingSetupRunner{failCheck: true}
	b := Backend{Runner: runner, SetupRunner: setup, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"})
	if err == nil || !strings.Contains(err.Error(), "privileged setup identity is unavailable") {
		t.Fatalf("expected setup identity fail-closed, got %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("setup identity failure should stop after start; calls=%+v", runner.calls)
	}
}

func TestRunSeparatesControlOutputFromTargetOutput(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", emitOutput: true}
	var targetOut, targetErr, controlOut, controlErr bytes.Buffer
	b := Backend{
		Runner:        runner,
		Stdin:         bytes.NewBufferString(""),
		Stdout:        &targetOut,
		Stderr:        &targetErr,
		ControlStdout: &controlOut,
		ControlStderr: &controlErr,
	}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	runEnv := []string{
		"HOME=/hideout/profile/home",
		"PATH=/hideout/session/shims:/usr/bin",
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, runEnv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := controlOut.String(), "call1\ncall2\ncall3\ncall4\n"; got != want {
		t.Fatalf("control stdout=%q want %q", got, want)
	}
	if got, want := targetOut.String(), "call5\n"; got != want {
		t.Fatalf("target stdout=%q want %q", got, want)
	}
	if targetErr.Len() != 0 || controlErr.Len() != 0 {
		t.Fatalf("unexpected stderr target=%q control=%q", targetErr.String(), controlErr.String())
	}
}

func TestCleanupAndStopUseControlOutput(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", emitOutput: true}
	var targetOut, targetErr, controlOut, controlErr bytes.Buffer
	b := Backend{
		Runner:        runner,
		Stdin:         bytes.NewBufferString(""),
		Stdout:        &targetOut,
		Stderr:        &targetErr,
		ControlStdout: &controlOut,
		ControlStderr: &controlErr,
	}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if err := b.StopInstance(context.Background(), session.InstanceName); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if got, want := runner.calls[len(runner.calls)-1].args, []string{"stop", "--force", session.InstanceName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("StopInstance args=%v want %v", got, want)
	}
	if got, want := controlOut.String(), "call1\ncall2\ncall3\n"; got != want {
		t.Fatalf("control stdout=%q want %q", got, want)
	}
	if targetOut.Len() != 0 || targetErr.Len() != 0 || controlErr.Len() != 0 {
		t.Fatalf("unexpected output targetOut=%q targetErr=%q controlErr=%q", targetOut.String(), targetErr.String(), controlErr.String())
	}
}

func TestRunStartsHostFSBeforeCommandCheckWhenEnabled(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.HostFSEnabled = true
	spec.HostFSGrafts = []string{"/Users/alice/Downloads"}
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	setup := &recordingSetupRunner{}
	b := Backend{Runner: runner, SetupRunner: setup, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !session.HostFSEnabled {
		t.Fatalf("session should carry HostFS enabled state")
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.calls) != 5 {
		t.Fatalf("calls=%+v", runner.calls)
	}
	if len(setup.calls) != 2 {
		t.Fatalf("setup calls=%+v", setup.calls)
	}
	hostFSStart := strings.Join(setup.calls[1].command, "\x00")
	if !strings.Contains(hostFSStart, "hideout-hostfsd") ||
		!strings.Contains(hostFSStart, "/hideout/hostfs") ||
		!strings.Contains(hostFSStart, "/Users/alice/Downloads") {
		t.Fatalf("HostFS start command missing daemon or mountpoint: %v", setup.calls[1].command)
	}
	if setup.calls[1].workdir != session.GuestWork {
		t.Fatalf("HostFS setup workdir=%q want %q", setup.calls[1].workdir, session.GuestWork)
	}
	commandCheck := strings.Join(runner.calls[3].args, "\x00")
	if !strings.Contains(commandCheck, "hideout-command-check") {
		t.Fatalf("command check should happen after HostFS start: %v", runner.calls[3].args)
	}
}

func TestRunFailsClosedWhenHostFSStartFails(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.HostFSEnabled = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", failCall: 4}
	setup := &recordingSetupRunner{failRun: true}
	b := Backend{Runner: runner, SetupRunner: setup, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"})
	if err == nil || !strings.Contains(err.Error(), "hostfs start") {
		t.Fatalf("expected hostfs start failure, got %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("HostFS start failure should stop before command check; calls=%+v", runner.calls)
	}
	if len(setup.calls) != 2 {
		t.Fatalf("expected setup check + hostfs attempt, calls=%+v", setup.calls)
	}
}

func TestHostFSStartScriptFailsClosedOnMissingPrerequisites(t *testing.T) {
	script := HostFSStartScript([]string{"/Users/alice/Downloads"})
	for _, want := range []string{
		"fusermount3 -u /hideout/hostfs",
		"setup identity cannot create /hideout/hostfs",
		"existing HostFS mount could not be reset",
		"[ ! -e /dev/fuse ]",
		"HostFS requires /dev/fuse",
		"[ ! -x /hideout/session/shims/hideout-hostfsd ]",
		"hideout-hostfsd is missing",
		"exit 70",
		"mkdir -p \"$(dirname '/Users/alice/Downloads')\" 2>/dev/null || true",
		"nohup env",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("HostFS start script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "sudo -n") {
		t.Fatalf("HostFS setup script must not fall back to target sudo:\n%s", script)
	}
}

func TestRunStartsExistingPreservedInstanceByName(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", listOutput: spec.Machine.InstanceName + "\n"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.calls) != 6 {
		t.Fatalf("calls=%+v", runner.calls)
	}
	if !reflect.DeepEqual(runner.calls[0].args, []string{"list", "--quiet"}) {
		t.Fatalf("first call should list lima instances, got %v", runner.calls[0].args)
	}
	if !reflect.DeepEqual(runner.calls[1].args, []string{"start", "--tty=false", session.InstanceName}) {
		t.Fatalf("existing preserved instance should start by name, got %v", runner.calls[1].args)
	}
}

func TestRunRetriesExistingPreservedInstanceAfterAmbiguousStartFailure(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = true
	runner := &recordingRunner{
		lookPath: "/opt/homebrew/bin/limactl", listOutput: spec.Machine.InstanceName + "\n",
		failCalls: map[int]bool{2: true},
	}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Run(context.Background(), session, []string{"true"}, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatalf("Run after idempotent existing-instance retry: %v", err)
	}
	wantStart := []string{"start", "--tty=false", session.InstanceName}
	if len(runner.calls) < 3 || !reflect.DeepEqual(runner.calls[1].args, wantStart) || !reflect.DeepEqual(runner.calls[2].args, wantStart) {
		t.Fatalf("existing instance start was not retried exactly by name: %+v", runner.calls)
	}
}

func TestRunFailsClosedWhenExistingInstanceStartRetryFails(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = true
	runner := &recordingRunner{
		lookPath: "/opt/homebrew/bin/limactl", listOutput: spec.Machine.InstanceName + "\n",
		failCalls: map[int]bool{2: true, 3: true},
	}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = b.Run(context.Background(), session, []string{"true"}, []string{"PATH=/usr/bin:/bin"})
	if err == nil || !strings.Contains(err.Error(), "retry existing Lima instance start") {
		t.Fatalf("two failed starts did not fail closed: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("failed existing instance start should stop after one retry: %+v", runner.calls)
	}
}

func TestRunStartsExistingEnvironmentInstanceByNameWhenRemoveAfterRun(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = false
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", listOutput: spec.Machine.InstanceName + "\n"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(runner.calls[0].args, []string{"list", "--quiet"}) {
		t.Fatalf("first call should list lima instances, got %v", runner.calls[0].args)
	}
	if !reflect.DeepEqual(runner.calls[1].args, []string{"start", "--tty=false", session.InstanceName}) {
		t.Fatalf("existing remove-after-run environment should start by name, got %v", runner.calls[1].args)
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	last := runner.calls[len(runner.calls)-1]
	if !reflect.DeepEqual(last.args, []string{"delete", "-f", session.InstanceName}) {
		t.Fatalf("remove-after-run cleanup should delete instance, got %v", last.args)
	}
}

func TestRunCreatesMissingPreservedInstanceFromConfig(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.calls) != 6 {
		t.Fatalf("calls=%+v", runner.calls)
	}
	wantStart := []string{"start", "--tty=false", "--name", session.InstanceName, session.ConfigPath}
	if !reflect.DeepEqual(runner.calls[1].args, wantStart) {
		t.Fatalf("missing preserved instance should start from config, got %v want %v", runner.calls[1].args, wantStart)
	}
}

func TestStartupProgressIsQuietForFastStartAndVisibleForSlowStart(t *testing.T) {
	var progress bytes.Buffer
	if err := runWithStartupProgressTimings(&progress, "hideout-fast", nil, time.Hour, time.Hour, func() error { return nil }); err != nil {
		t.Fatalf("fast start: %v", err)
	}
	if progress.Len() != 0 {
		t.Fatalf("fast start should stay quiet, got %q", progress.String())
	}

	progress.Reset()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runWithStartupProgressTimings(&progress, "hideout-slow", &backend.RuntimePresentation{
			Family: "developer-standard", Revision: "2026.07.0", Maturity: "preview", DownloadBytes: 1 << 30,
		}, time.Millisecond, time.Hour, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	time.Sleep(10 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("slow start: %v", err)
	}
	for _, want := range []string{
		`starting Lima environment "hideout-slow"`,
		"runtime developer-standard@2026.07.0",
		"preview",
		"1.0 GiB declared download",
		"first use may download it",
		"Lima environment ready",
	} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("slow start progress missing %q: %q", want, progress.String())
		}
	}
	for _, forbidden := range []string{"%", "downloaded bytes", "estimated"} {
		if strings.Contains(progress.String(), forbidden) {
			t.Fatalf("startup progress fabricated observation %q: %q", forbidden, progress.String())
		}
	}
}

func TestCleanupSkipsGuestCommandsWhenRunStartDidNotComplete(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260715t120000zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", failCall: 2}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Run(context.Background(), session, []string{"true"}, []string{"PATH=/usr/bin:/bin"}); err == nil {
		t.Fatal("Run should fail while starting the instance")
	}
	if !session.RunAttempted || session.RuntimeReady {
		t.Fatalf("start state attempted=%v ready=%v", session.RunAttempted, session.RuntimeReady)
	}
	runner.calls = nil
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("Cleanup after pre-target start failure: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("cleanup must not enter a guest that never became ready: %+v", runner.calls)
	}
}

func TestHostCommandEnvUsesSmallAllowlist(t *testing.T) {
	got := HostCommandEnv([]string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"USER=alice",
		"HTTP_PROXY=http://user:pass@proxy.invalid:8080",
		"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080",
		"SERVICE_TOKEN=secret",
		"LIMA_HOME=/Users/alice/.lima-hideout",
	})
	text := strings.Join(got, "\n")
	for _, want := range []string{"PATH=/bin", "HOME=/Users/alice", "USER=alice", "LIMA_HOME=/Users/alice/.lima-hideout"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sanitized host env missing %s: %v", want, got)
		}
	}
	for _, denied := range []string{"HTTP_PROXY=", "HIDEOUT_SECRET_", "SERVICE_TOKEN="} {
		if strings.Contains(text, denied) {
			t.Fatalf("sanitized host env leaked %s: %v", denied, got)
		}
	}
}

func TestRunReportsMissingGuestCommandWithBackendContext(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", failCall: 4}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = b.Run(context.Background(), session, []string{"missing-tool"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"})
	var notFound backend.CommandNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected CommandNotFoundError, got %T %v", err, err)
	}
	if notFound.Backend != "lima" || notFound.Command != "missing-tool" || notFound.Workspace != spec.Workspace.GuestRoot {
		t.Fatalf("unexpected missing command context: %+v", notFound)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("target command should not run after failed command check: calls=%+v", runner.calls)
	}
}

func TestCleanupRunsNetworkCleanupScript(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	want := []string{
		"shell", "--tty=false", "--workdir", spec.Workspace.GuestRoot, session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", GuestSessionDir + "/network/cleanup.sh",
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("cleanup calls=%+v want %v", runner.calls, want)
	}
	if !reflect.DeepEqual(runner.calls[1].args, []string{"delete", "-f", session.InstanceName}) {
		t.Fatalf("cleanup should delete session instance, calls=%+v", runner.calls)
	}
}

func TestCleanupUsesLastRunGuestEnv(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	runEnv := []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"}
	if err := b.Run(context.Background(), session, []string{"sh", "-c", "true"}, runEnv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	runner.calls = nil
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	want := []string{
		"shell", "--tty=false", "--workdir", spec.Workspace.GuestRoot, session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", GuestSessionDir + "/network/cleanup.sh",
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("cleanup calls=%+v want %v", runner.calls, want)
	}
	if !reflect.DeepEqual(runner.calls[1].args, []string{"delete", "-f", session.InstanceName}) {
		t.Fatalf("cleanup should delete session instance, calls=%+v", runner.calls)
	}
}

func TestCleanupPreservesEnvironmentInstance(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.Machine.InstanceName = InstanceNameForEnvironment(spec.Machine.Profile.Name, spec.Machine.EnvironmentID)
	spec.Machine.PreserveInstance = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.InstanceName != spec.Machine.InstanceName || session.EnvironmentID != spec.Machine.EnvironmentID || !session.PreserveInstance {
		t.Fatalf("environment session fields not preserved: %+v", session)
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("preserved instance cleanup should only run network cleanup, calls=%+v", runner.calls)
	}
	if strings.Contains(strings.Join(runner.calls[0].args, " "), "delete -f") {
		t.Fatalf("preserved instance cleanup must not delete: %+v", runner.calls)
	}
}

func TestCleanupDeletesInstanceEvenWhenNetworkCleanupFails(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", failCall: 1}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = b.Cleanup(context.Background(), session)
	if err == nil || !strings.Contains(err.Error(), "network cleanup") {
		t.Fatalf("expected network cleanup error, got %v", err)
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[1].args, []string{"delete", "-f", session.InstanceName}) {
		t.Fatalf("cleanup must still delete session instance after network cleanup failure: %+v", runner.calls)
	}
}

func TestCleanupUsesFreshContextAfterRunCancellation(t *testing.T) {
	runner := &cleanupContextRunner{lookPath: "/opt/homebrew/bin/limactl"}
	setup := &recordingSetupRunner{}
	b := Backend{Runner: runner, SetupRunner: setup, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session := &backend.Session{
		InstanceName:            "hideout-canceled-session",
		GuestWork:               "/workspace",
		HostFSEnabled:           true,
		PrivilegedSetupRequired: true,
		NetworkCleanupGuestPath: GuestSessionDir + "/network/cleanup.sh",
		NetworkPrivilegedSetup:  false,
		Env:                     []string{"PATH=/usr/bin:/bin"},
		PreserveInstance:        false,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Cleanup(ctx, session); err != nil {
		t.Fatalf("Cleanup with canceled run context: %v", err)
	}
	if len(setup.calls) != 2 {
		t.Fatalf("cleanup should run hostfs cleanup through setup identity: %+v", setup.calls)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("cleanup should run network cleanup and delete through lima runner: %+v", runner.calls)
	}
	if !reflect.DeepEqual(runner.calls[1].args, []string{"delete", "-f", session.InstanceName}) {
		t.Fatalf("cleanup must delete session instance after cancellation: %+v", runner.calls)
	}
}

func TestInstanceNameIsStableAndSafe(t *testing.T) {
	if got := InstanceName("Client A / Prod"); got != "hideout-client-a-prod" {
		t.Fatalf("InstanceName=%s", got)
	}
	if got := InstanceNameForProfile("Client A / Prod", "id_0123456789abcdef"); got != "hideout-client-a-prod-0123456789ab" {
		t.Fatalf("InstanceNameForProfile=%s", got)
	}
	if got := InstanceNameForSession("Client A / Prod", "ses_20260702T082535Z_737c930a75b2ac6f9c7d"); got != "hideout-client-a-prod-session-75b2ac6f9c7d" {
		t.Fatalf("InstanceNameForSession=%s", got)
	}
	if got := InstanceNameForEnvironment("Client A / Prod", "env_20260702t124639zd658adca03dc559f43e9"); got != "hideout-client-a-prod-env-03dc559f43e9" {
		t.Fatalf("InstanceNameForEnvironment=%s", got)
	}
	if InstanceNameForProfile("Client A / Prod", "id_aaaaaaaaaaaa0000") == InstanceNameForProfile("Client A / Prod", "id_bbbbbbbbbbbb0000") {
		t.Fatal("different identities must not share the same Lima instance name")
	}
}

func testRunSpec(root string) backend.RunSpec {
	p := profile.Default("Client A")
	p.Metadata = map[string]string{
		"profileId":  "prf_1111222233334444",
		"identityId": "id_abcdef1234567890",
	}
	return backend.RunSpec{
		Machine: backend.MachineActivationSpec{
			ImageRef: "template:_images/ubuntu-lts", Profile: p,
			ProfileDir:   filepath.Join(root, "profiles", "client-a"),
			IdentityMode: "persistent", IdentityRoot: filepath.Join(root, "profiles", "client-a"),
			InstanceName: "hideout-client-a-session-1", Mode: environment.ModeWorkspaceBound,
		},
		Workspace: backend.WorkspaceAttachmentSpec{
			HostRoot: filepath.Join(root, "workspace"), GuestRoot: "/Users/alice/project", Transport: backend.WorkspaceTransportStatic,
		},
		SessionID:                 "ses_1",
		Command:                   []string{"sh"},
		Env:                       []string{"HOME=/hideout/profile/home"},
		GuestHome:                 GuestProfileDir + "/home",
		ShimDir:                   filepath.Join(root, "sessions", "ses_1", "shims"),
		SessionDir:                filepath.Join(root, "sessions", "ses_1"),
		Broker:                    broker.TCPEndpoint("host.lima.internal:1234"),
		NetworkBootstrapPath:      filepath.Join(root, "sessions", "ses_1", "network", "bootstrap.sh"),
		NetworkBootstrapGuestPath: GuestSessionDir + "/network/bootstrap.sh",
		NetworkCleanupPath:        filepath.Join(root, "sessions", "ses_1", "network", "cleanup.sh"),
		NetworkCleanupGuestPath:   GuestSessionDir + "/network/cleanup.sh",
		AuditPath:                 filepath.Join(root, "sessions", "ses_1", "audit.jsonl"),
	}
}

func testPortBridgeSpec(listenAddress, targetAddress string) portbridge.Spec {
	return portbridge.Spec{
		ID:               "ep_manual_preview_1",
		Owner:            "preview.open",
		Action:           "endpoint.expose.host-to-guest",
		Source:           "manual",
		ClosePolicy:      "session-end",
		Lifetime:         portbridge.LifetimeRun,
		Direction:        portbridge.DirectionHostToGuest,
		ListenScope:      portbridge.ListenScopeLoopback,
		ListenAddress:    listenAddress,
		TargetScope:      portbridge.TargetScopeGuest,
		TargetAddress:    targetAddress,
		EndpointCategory: portbridge.EndpointCategoryHostLoopback,
	}
}

type directTCPIPTarget struct {
	Host string
	Port uint32
}

func testSSHSigner(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate ssh key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("new ssh signer: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return signer, pemKey
}

func startDirectTCPIPSSHServer(t *testing.T, listener net.Listener, hostSigner ssh.Signer, allowedClientKey ssh.PublicKey, targets chan<- directTCPIPTarget) {
	t.Helper()
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), allowedClientKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unexpected client key")
		},
	}
	serverConfig.AddHostKey(hostSigner)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		conn, chans, reqs, err := ssh.NewServerConn(raw, serverConfig)
		if err != nil {
			_ = raw.Close()
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(reqs)
		for next := range chans {
			if next.ChannelType() != "direct-tcpip" {
				_ = next.Reject(ssh.UnknownChannelType, "unsupported channel")
				continue
			}
			var payload struct {
				Host           string
				Port           uint32
				OriginatorHost string
				OriginatorPort uint32
			}
			if err := ssh.Unmarshal(next.ExtraData(), &payload); err != nil {
				_ = next.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, requests, err := next.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(requests)
			targets <- directTCPIPTarget{Host: payload.Host, Port: payload.Port}
			go func() {
				defer ch.Close()
				data, _ := io.ReadAll(ch)
				_, _ = ch.Write(append([]byte("guest:"), data...))
			}()
		}
	}()
}

type fakeRunner struct {
	lookPath string
}

func (f fakeRunner) LookPath(string) (string, error) {
	return f.lookPath, nil
}

func (fakeRunner) Run(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return nil
}

type recordingRunner struct {
	lookPath       string
	calls          []recordedCall
	failCall       int
	failCalls      map[int]bool
	listOutput     string
	emitOutput     bool
	terminalBridge bool
}

type recordedCall struct {
	name string
	args []string
	env  []string
}

type recordedSetupCall struct {
	instance string
	workdir  string
	env      []string
	command  []string
	check    bool
}

func (r *recordingRunner) LookPath(string) (string, error) {
	return r.lookPath, nil
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string, env []string, _ io.Reader, stdout, _ io.Writer) error {
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)})
	if (r.failCall > 0 && len(r.calls) == r.failCall) || r.failCalls[len(r.calls)] {
		return errors.New("guest command not found")
	}
	if len(args) >= 5 && args[0] == "list" && args[1] == "--format" && args[2] == "json" {
		// Current Lima reports the macOS arm64 host as aarch64 in inventory.
		_, _ = fmt.Fprintf(stdout, `{"name":"hideout-runtime-test","status":"Running","vmType":"vz","arch":"aarch64","HostOS":"darwin","HostArch":"aarch64","config":{"vmType":"vz","arch":"aarch64","images":[{"location":"https://example.invalid/runtime.qcow2","arch":"aarch64","digest":"sha256:%s"}]}}`+"\n", strings.Repeat("a", 64))
		return nil
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "command -v script") && r.terminalBridge {
		_, _ = io.WriteString(stdout, "available")
		return nil
	}
	if strings.HasSuffix(joined, " cat /proc/sys/kernel/random/boot_id") {
		_, _ = io.WriteString(stdout, "01234567-89ab-cdef-0123-456789abcdef\n")
		return nil
	}
	if strings.HasSuffix(joined, " uname -m") {
		_, _ = io.WriteString(stdout, "aarch64\n")
		return nil
	}
	if r.emitOutput {
		_, _ = io.WriteString(stdout, "call"+strconv.Itoa(len(r.calls))+"\n")
	}
	if len(args) == 2 && args[0] == "list" && args[1] == "--quiet" && r.listOutput != "" {
		_, _ = io.WriteString(stdout, r.listOutput)
	}
	return nil
}

type recordingSetupRunner struct {
	calls     []recordedSetupCall
	failCheck bool
	failRun   bool
}

func TestReconcileEnvironmentBootUsesSetupIdentity(t *testing.T) {
	runner := &recordingSetupRunner{}
	b := Backend{SetupRunner: runner}
	session := &backend.Session{InstanceName: "hideout-shared", TargetUser: "developer"}
	configuration := environment.BootConfiguration{
		Schema:   environment.BootConfigurationSchema,
		Hostname: "hideout-dev",
	}

	if err := b.ReconcileEnvironmentBoot(context.Background(), session, configuration, []string{"HOME=/tmp/ignored"}); err != nil {
		t.Fatalf("reconcile environment boot: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("setup calls = %d, want check and run", len(runner.calls))
	}
	if !runner.calls[0].check || runner.calls[0].instance != session.InstanceName {
		t.Fatalf("unexpected setup check: %#v", runner.calls[0])
	}
	call := runner.calls[1]
	if call.instance != session.InstanceName || call.workdir != "/" {
		t.Fatalf("unexpected setup target: %#v", call)
	}
	if len(call.command) != 5 || call.command[0] != "sh" || call.command[1] != "-c" || call.command[4] != configuration.Hostname {
		t.Fatalf("unexpected boot reconciliation command: %#v", call.command)
	}
	if !strings.Contains(call.command[2], "mv \"$tmp\" /etc/hostname") || !strings.Contains(call.command[2], "hostname \"$hostname_value\"") {
		t.Fatalf("boot reconciliation is not atomic and explicit: %q", call.command[2])
	}
	if session.PrivilegedSetupRequired {
		t.Fatal("reconciliation mutated the target session authority")
	}
}

func TestReconcileEnvironmentBootFailsClosed(t *testing.T) {
	valid := environment.BootConfiguration{
		Schema:   environment.BootConfigurationSchema,
		Hostname: "hideout-dev",
	}

	t.Run("invalid configuration", func(t *testing.T) {
		runner := &recordingSetupRunner{}
		b := Backend{SetupRunner: runner}
		err := b.ReconcileEnvironmentBoot(context.Background(), &backend.Session{InstanceName: "hideout-shared"}, environment.BootConfiguration{}, nil)
		if err == nil || len(runner.calls) != 0 {
			t.Fatalf("invalid configuration was executed: err=%v calls=%#v", err, runner.calls)
		}
	})

	t.Run("setup identity unavailable", func(t *testing.T) {
		runner := &recordingSetupRunner{failCheck: true}
		b := Backend{SetupRunner: runner}
		err := b.ReconcileEnvironmentBoot(context.Background(), &backend.Session{InstanceName: "hideout-shared"}, valid, nil)
		if err == nil || !strings.Contains(err.Error(), "setup identity unavailable") {
			t.Fatalf("setup identity failure was not propagated: %v", err)
		}
	})

	t.Run("setup command fails", func(t *testing.T) {
		runner := &recordingSetupRunner{failRun: true}
		b := Backend{SetupRunner: runner}
		err := b.ReconcileEnvironmentBoot(context.Background(), &backend.Session{InstanceName: "hideout-shared"}, valid, nil)
		if err == nil || !strings.Contains(err.Error(), "setup run failed") {
			t.Fatalf("setup failure was not propagated: %v", err)
		}
	})
}

func (r *recordingSetupRunner) Check(_ context.Context, instanceName string) error {
	r.calls = append(r.calls, recordedSetupCall{instance: instanceName, check: true})
	if r.failCheck {
		return errors.New("setup identity unavailable")
	}
	return nil
}

func (r *recordingSetupRunner) Run(_ context.Context, instanceName, workdir string, env []string, command []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	r.calls = append(r.calls, recordedSetupCall{
		instance: instanceName,
		workdir:  workdir,
		env:      append([]string(nil), env...),
		command:  append([]string(nil), command...),
	})
	if r.failRun {
		return errors.New("setup run failed")
	}
	return nil
}

type privilegeProbeRunner struct {
	uidOutput          string
	sudoSucceeds       bool
	statusEmitted      *bool
	targetBeforeStatus bool
	probeWorkdirs      []string
}

func (r *privilegeProbeRunner) LookPath(string) (string, error) {
	return "/opt/homebrew/bin/limactl", nil
}

func shellArgsWorkdir(args []string) string {
	for index, arg := range args {
		if arg == "--workdir" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func (r *privilegeProbeRunner) Run(_ context.Context, _ string, args []string, _ []string, _ io.Reader, stdout, stderr io.Writer) error {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "id -u") || strings.Contains(joined, "sudo -n true") {
		r.probeWorkdirs = append(r.probeWorkdirs, shellArgsWorkdir(args))
	}
	switch {
	case len(args) >= 1 && args[0] == "start":
		return nil
	case strings.Contains(joined, "id -u"):
		out := r.uidOutput
		if out == "" {
			out = "1000\n"
		}
		_, _ = io.WriteString(stdout, out)
		return nil
	case strings.Contains(joined, "/usr/bin/sudo -n true"):
		if r.sudoSucceeds {
			return nil
		}
		_, _ = io.WriteString(stderr, "sudo: a password is required\n")
		return errors.New("exit status 1")
	case strings.Contains(joined, "sudo -n true"):
		if r.sudoSucceeds {
			return nil
		}
		_, _ = io.WriteString(stderr, "sudo: a password is required\n")
		return errors.New("exit status 1")
	case strings.Contains(joined, GuestBootstrapPath):
		return nil
	case strings.Contains(joined, "command -v"):
		return nil
	case strings.Contains(joined, " echo ok") || strings.HasSuffix(joined, " true"):
		if r.statusEmitted != nil && !*r.statusEmitted {
			r.targetBeforeStatus = true
		}
		return nil
	default:
		return nil
	}
}

type cleanupContextRunner struct {
	lookPath string
	calls    []recordedCall
}

func (r *cleanupContextRunner) LookPath(string) (string, error) {
	return r.lookPath, nil
}

func (r *cleanupContextRunner) Run(ctx context.Context, name string, args []string, env []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)})
	return nil
}

func TestConfigForMachineSpecCompilesImageDeclaration(t *testing.T) {
	root := t.TempDir()
	base := backend.MachineActivationSpec{
		Profile: profile.Default("img-test"), ProfileDir: filepath.Join(root, "profile"),
		IdentityRoot: filepath.Join(root, "profile"), Mode: environment.ModeWorkspaceBound,
	}
	static := &StaticRunMounts{
		Workspace:  backend.WorkspaceAttachmentSpec{HostRoot: filepath.Join(root, "workspace"), GuestRoot: "/workspace", Transport: backend.WorkspaceTransportStatic},
		SessionDir: filepath.Join(root, "session"),
	}

	spec := base
	spec.ImageRef = "template:_images/debian-13"
	cfg, err := ConfigForMachineSpec(spec, static)
	if err != nil {
		t.Fatalf("template declaration should build: %v", err)
	}
	if len(cfg.Base) != 1 || cfg.Base[0] != "template:_images/debian-13" {
		t.Fatalf("template declaration should map to base template: %+v", cfg.Base)
	}
	if len(cfg.Images) != 0 {
		t.Fatalf("template form should not emit images entries: %+v", cfg.Images)
	}

	spec = base
	spec.ImageRef = "https://example.com/images/dev.qcow2#sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg, err = ConfigForMachineSpec(spec, static)
	if err != nil {
		t.Fatalf("url declaration should build: %v", err)
	}
	if len(cfg.Base) != 0 {
		t.Fatalf("url form should not use a base template: %+v", cfg.Base)
	}
	if len(cfg.Images) != 1 ||
		cfg.Images[0].Location != "https://example.com/images/dev.qcow2" ||
		cfg.Images[0].Digest != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("url form should emit a digest-verified images entry: %+v", cfg.Images)
	}

	// The declaration is the single source and the backend never substitutes
	// a different image: an empty or unusable ref fails closed.
	spec = base
	spec.ImageRef = ""
	if _, err := ConfigForMachineSpec(spec, static); err == nil {
		t.Fatal("empty image declaration must fail closed, not fall back")
	}
	spec.ImageRef = "ubuntu:24.04"
	if _, err := ConfigForMachineSpec(spec, static); err == nil {
		t.Fatal("unusable image declaration must fail closed, not fall back")
	}
}
