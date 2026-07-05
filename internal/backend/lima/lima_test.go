package lima

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/portbridge"
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
	if cfg.User.Name != spec.Profile.Identity.User ||
		cfg.User.Home != "/home/"+spec.Profile.Identity.User ||
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
	if !strings.Contains(cfg.Provision[0].Script, "printf '%s\\n' 'devbox' > /etc/hostname") {
		t.Fatalf("provision should set profile hostname: %s", cfg.Provision[0].Script)
	}
	if !strings.Contains(cfg.Provision[0].Script, "/hideout/profile/machine/machine-id") ||
		!strings.Contains(cfg.Provision[0].Script, "/etc/machine-id") {
		t.Fatalf("provision should set guest machine-id from profile identity state: %s", cfg.Provision[0].Script)
	}
	wantMounts := []mount{
		{Location: spec.HostWork, MountPoint: spec.GuestWork, Writable: true},
		{Location: filepath.Join(spec.ProfileDir, "home"), MountPoint: GuestProfileDir + "/home", Writable: true},
		{Location: filepath.Join(spec.ProfileDir, "cache"), MountPoint: GuestProfileDir + "/cache", Writable: true},
		{Location: filepath.Join(spec.ProfileDir, "config"), MountPoint: GuestProfileDir + "/config", Writable: true},
		{Location: filepath.Join(spec.ProfileDir, "data"), MountPoint: GuestProfileDir + "/data", Writable: true},
		{Location: filepath.Join(spec.ProfileDir, "browser"), MountPoint: GuestProfileDir + "/browser", Writable: true},
		{Location: filepath.Join(spec.ProfileDir, "machine"), MountPoint: GuestProfileDir + "/machine", Writable: false},
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
		case spec.ProfileDir, filepath.Join(spec.ProfileDir, "profile.json"), filepath.Join(spec.ProfileDir, "identity.json"), filepath.Join(spec.ProfileDir, "policy"):
			t.Fatalf("lima config must not expose profile control-plane path as a guest mount: %+v", m)
		}
		switch m.Location {
		case spec.SessionDir, filepath.Join(spec.SessionDir, "audit.jsonl"), filepath.Join(spec.SessionDir, "lima.yaml"), filepath.Join(spec.SessionDir, "tool-preset.json"), filepath.Join(spec.SessionDir, "broker-endpoint.json"), filepath.Join(spec.SessionDir, "network-plan.json"):
			t.Fatalf("lima config must not expose session control-plane path as a guest mount: %+v", m)
		}
	}
	bootstrap, err := os.ReadFile(session.BootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if !bytes.Contains(bootstrap, []byte("required guest command missing")) {
		t.Fatalf("bootstrap missing command checks:\n%s", bootstrap)
	}
	if !bytes.Contains(bootstrap, []byte("/hideout/session/shims/hideout-shim")) {
		t.Fatalf("bootstrap missing shim check:\n%s", bootstrap)
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	if !bytes.Contains(manifest, []byte(`"name": "base-dev"`)) {
		t.Fatalf("manifest missing base-dev: %s", manifest)
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
	spec.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.SessionDir = filepath.Join(root, "environments", spec.EnvironmentID, "runtime")
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
	spec.IdentityMode = "ephemeral"
	spec.IdentityRoot = filepath.Join(spec.SessionDir, "identity")
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.InstanceName != "hideout-client-a-session-1" {
		t.Fatalf("instance=%s", session.InstanceName)
	}
	if session.IdentityMode != "ephemeral" || session.IdentityRoot != spec.IdentityRoot {
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
		want := filepath.Join(spec.IdentityRoot, subdir)
		found := false
		for _, m := range cfg.Mounts {
			if m.Location == filepath.Join(spec.ProfileDir, subdir) {
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
		`"identityRoot": "` + spec.IdentityRoot + `"`,
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
	delete(spec.Profile.CommandProxy.Commands, "xdg-open")
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

func TestPrepareAddsNodeDevProvisionWhenPresetEnabled(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Profile.Tools.Presets = []string{"base-dev", "node-dev"}
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
	for _, want := range []string{
		"apt-get install -y ca-certificates curl nodejs npm",
		"command -v node",
		"command -v npm",
	} {
		if !strings.Contains(bootstrapText, want) {
			t.Fatalf("node-dev bootstrap missing provision %q:\n%s", want, bootstrapText)
		}
	}
	for _, want := range []string{
		"required guest command missing: node",
		"required guest command missing: npm",
	} {
		if !strings.Contains(bootstrapText, want) {
			t.Fatalf("node-dev bootstrap missing command check %q:\n%s", want, bootstrapText)
		}
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	if !bytes.Contains(manifest, []byte(`"name": "node-dev"`)) {
		t.Fatalf("manifest missing node-dev: %s", manifest)
	}
	if bytes.Contains(manifest, []byte("npm install")) {
		t.Fatalf("manifest must not embed provision script: %s", manifest)
	}
}

func TestPrepareAddsUserNPMGlobalProvision(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Profile.Tools.Presets = []string{"base-dev", "node-dev"}
	spec.Profile.Tools.NPMGlobals = []profile.NPMGlobalPackage{{
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
	for _, want := range []string{
		"npm install -g '@example/agent-cli@1.2.3'",
		"command -v 'agent-cli'",
	} {
		if !strings.Contains(bootstrapText, want) {
			t.Fatalf("npm global bootstrap missing provision %q:\n%s", want, bootstrapText)
		}
	}
	if !strings.Contains(bootstrapText, "required guest command missing: agent-cli") {
		t.Fatalf("bootstrap missing npm global command check:\n%s", bootstrap)
	}
	manifest, err := os.ReadFile(session.ToolManifestPath)
	if err != nil {
		t.Fatalf("read tool manifest: %v", err)
	}
	for _, want := range []string{`"package": "@example/agent-cli@1.2.3"`, `"agent-cli"`} {
		if !bytes.Contains(manifest, []byte(want)) {
			t.Fatalf("manifest missing %s: %s", want, manifest)
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

func TestGeneratedYAMLDoesNotInheritDefaultHomeMountWhenCreated(t *testing.T) {
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl not installed")
	}
	limaRoot, err := os.MkdirTemp("/tmp", "hideout-lima-merge.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(limaRoot)
	})
	limaHome := filepath.Join(limaRoot, "l")
	if err := os.MkdirAll(limaHome, 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	session, err := (Backend{Runner: fakeRunner{lookPath: limactl}}).Prepare(context.Background(), testRunSpec(root))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	instance := "hmt"
	t.Cleanup(func() {
		cmd := exec.Command(limactl, "delete", "-f", instance)
		cmd.Env = append(os.Environ(), "LIMA_HOME="+limaHome)
		_ = cmd.Run()
	})
	cmd := exec.Command(limactl, "create", "--tty=false", "--name", instance, session.ConfigPath)
	cmd.Env = append(os.Environ(), "LIMA_HOME="+limaHome)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("limactl create: %v\n%s", err, data)
	}
	finalConfig := filepath.Join(limaHome, instance, "lima.yaml")
	data, err = os.ReadFile(finalConfig)
	if err != nil {
		t.Fatalf("read final lima config: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml decode final config: %v\n%s", err, data)
	}
	for _, m := range cfg.Mounts {
		if m.Location == "~" || m.Location == os.Getenv("HOME") {
			t.Fatalf("final lima config inherited host home mount: %+v\n%s", m, data)
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
		"PATH=/hideout/session/shims:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOSTNAME=devbox",
		"LANG=en_US.UTF-8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GuestEnv=%v want %v", got, want)
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

func TestResolveToolPresetsRejectsUnknown(t *testing.T) {
	if _, err := ResolveToolPresets([]string{"unknown"}); err == nil {
		t.Fatal("expected unknown preset to fail")
	}
}

func TestResolveToolPresetsIncludesNodeDev(t *testing.T) {
	presets, err := ResolveToolPresets([]string{"base-dev", "node-dev"})
	if err != nil {
		t.Fatalf("ResolveToolPresets: %v", err)
	}
	if len(presets) != 2 || presets[1].Name != "node-dev" {
		t.Fatalf("unexpected presets: %+v", presets)
	}
	for _, want := range []string{"node", "npm"} {
		if !slices.Contains(presets[1].RequiredCommands, want) {
			t.Fatalf("node-dev required commands missing %s: %+v", want, presets[1].RequiredCommands)
		}
	}
	if !strings.Contains(presets[1].ProvisionScript, "apt-get install -y ca-certificates curl nodejs npm") {
		t.Fatalf("node-dev provision missing package install: %s", presets[1].ProvisionScript)
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
		"shell", "--tty=false", "--workdir", spec.GuestWork, session.InstanceName, "--", "env", "-i",
		"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", "SERVICE_TOKEN=secret", "HIDEOUT_BROKER_ENDPOINT=tcp://127.0.0.1:1", "sh", "-c", "command -v \"$1\" >/dev/null 2>&1", "hideout-command-check", "sh",
	}
	if !reflect.DeepEqual(runner.calls[3].args, wantCheck) {
		t.Fatalf("command check args=%v want %v", runner.calls[3].args, wantCheck)
	}
	wantShell := []string{
		"shell", "--tty=false", "--workdir", spec.GuestWork, session.InstanceName, "--", "env", "-i",
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
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
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
	if len(runner.calls) != 6 {
		t.Fatalf("calls=%+v", runner.calls)
	}
	hostFSStart := strings.Join(runner.calls[3].args, "\x00")
	if !strings.Contains(hostFSStart, "hideout-hostfsd") ||
		!strings.Contains(hostFSStart, "/hideout/hostfs") ||
		!strings.Contains(hostFSStart, "/Users/alice/Downloads") {
		t.Fatalf("HostFS start command missing daemon or mountpoint: %v", runner.calls[3].args)
	}
	commandCheck := strings.Join(runner.calls[4].args, "\x00")
	if !strings.Contains(commandCheck, "hideout-command-check") {
		t.Fatalf("command check should happen after HostFS start: %v", runner.calls[4].args)
	}
}

func TestRunFailsClosedWhenHostFSStartFails(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.HostFSEnabled = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", failCall: 4}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = b.Run(context.Background(), session, []string{"sh", "-c", "pwd"}, []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin"})
	if err == nil || !strings.Contains(err.Error(), "hostfs start") {
		t.Fatalf("expected hostfs start failure, got %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("HostFS start failure should stop before command check; calls=%+v", runner.calls)
	}
}

func TestHostFSStartScriptFailsClosedOnMissingPrerequisites(t *testing.T) {
	script := HostFSStartScript([]string{"/Users/alice/Downloads"})
	for _, want := range []string{
		"fusermount3 -u /hideout/hostfs",
		"sudo -n umount /hideout/hostfs",
		"existing HostFS mount could not be reset",
		"[ ! -e /dev/fuse ]",
		"HostFS requires /dev/fuse",
		"[ ! -x /hideout/session/shims/hideout-hostfsd ]",
		"hideout-hostfsd is missing",
		"exit 70",
		"sudo -n mkdir -p \"$(dirname '/Users/alice/Downloads')\" 2>/dev/null || true",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("HostFS start script missing %q:\n%s", want, script)
		}
	}
}

func TestRunStartsExistingPreservedInstanceByName(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.InstanceName = InstanceNameForEnvironment(spec.Profile.Name, spec.EnvironmentID)
	spec.PreserveInstance = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", listOutput: spec.InstanceName + "\n"}
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

func TestRunStartsExistingEnvironmentInstanceByNameWhenRemoveAfterRun(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.InstanceName = InstanceNameForEnvironment(spec.Profile.Name, spec.EnvironmentID)
	spec.PreserveInstance = false
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", listOutput: spec.InstanceName + "\n"}
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
	spec.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.InstanceName = InstanceNameForEnvironment(spec.Profile.Name, spec.EnvironmentID)
	spec.PreserveInstance = true
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
	if notFound.Backend != "lima" || notFound.Command != "missing-tool" || notFound.Workspace != spec.GuestWork {
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
		"shell", "--tty=false", "--workdir", spec.GuestWork, session.InstanceName, "--", "env", "-i",
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
		"shell", "--tty=false", "--workdir", spec.GuestWork, session.InstanceName, "--", "env", "-i",
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
	spec.EnvironmentID = "env_20260702t124639zabcdef1234567890"
	spec.InstanceName = InstanceNameForEnvironment(spec.Profile.Name, spec.EnvironmentID)
	spec.PreserveInstance = true
	runner := &recordingRunner{lookPath: "/opt/homebrew/bin/limactl"}
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.InstanceName != spec.InstanceName || session.EnvironmentID != spec.EnvironmentID || !session.PreserveInstance {
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
	b := Backend{Runner: runner, Stdin: bytes.NewBufferString(""), Stdout: io.Discard, Stderr: io.Discard}
	session := &backend.Session{
		InstanceName:            "hideout-canceled-session",
		GuestWork:               "/workspace",
		HostFSEnabled:           true,
		NetworkCleanupGuestPath: GuestSessionDir + "/network/cleanup.sh",
		Env:                     []string{"PATH=/usr/bin:/bin"},
		PreserveInstance:        false,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Cleanup(ctx, session); err != nil {
		t.Fatalf("Cleanup with canceled run context: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("cleanup should run hostfs cleanup, network cleanup, and delete: %+v", runner.calls)
	}
	if !reflect.DeepEqual(runner.calls[2].args, []string{"delete", "-f", session.InstanceName}) {
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
		SessionID:                 "ses_1",
		Profile:                   p,
		Command:                   []string{"sh"},
		Env:                       []string{"HOME=/hideout/profile/home"},
		HostWork:                  filepath.Join(root, "workspace"),
		GuestWork:                 "/Users/alice/project",
		GuestHome:                 GuestProfileDir + "/home",
		ShimDir:                   filepath.Join(root, "sessions", "ses_1", "shims"),
		ProfileDir:                filepath.Join(root, "profiles", "client-a"),
		IdentityMode:              "persistent",
		IdentityRoot:              filepath.Join(root, "profiles", "client-a"),
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
	lookPath   string
	calls      []recordedCall
	failCall   int
	listOutput string
	emitOutput bool
}

type recordedCall struct {
	name string
	args []string
	env  []string
}

func (r *recordingRunner) LookPath(string) (string, error) {
	return r.lookPath, nil
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string, env []string, _ io.Reader, stdout, _ io.Writer) error {
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)})
	if r.failCall > 0 && len(r.calls) == r.failCall {
		return errors.New("guest command not found")
	}
	if r.emitOutput {
		_, _ = io.WriteString(stdout, "call"+strconv.Itoa(len(r.calls))+"\n")
	}
	if len(args) == 2 && args[0] == "list" && args[1] == "--quiet" && r.listOutput != "" {
		_, _ = io.WriteString(stdout, r.listOutput)
	}
	return nil
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
