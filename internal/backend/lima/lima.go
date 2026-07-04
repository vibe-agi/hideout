package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/portbridge"
	"github.com/vibe-agi/hideout/internal/profile"
	"gopkg.in/yaml.v3"
)

const (
	GuestProfileDir       = "/hideout/profile"
	GuestSessionDir       = "/hideout/session"
	GuestBootstrapPath    = GuestSessionDir + "/bootstrap/bootstrap.sh"
	GuestNetworkBootstrap = GuestSessionDir + "/network/bootstrap.sh"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error
	LookPath(file string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (ExecRunner) Run(ctx context.Context, name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type Backend struct {
	LimactlPath string
	Runner      CommandRunner
	Stdout      io.Writer
	Stderr      io.Writer
	Stdin       io.Reader
}

type limaConfig struct {
	Base         []string          `yaml:"base,omitempty"`
	VMType       string            `yaml:"vmType,omitempty"`
	MountType    string            `yaml:"mountType,omitempty"`
	MountInotify bool              `yaml:"mountInotify"`
	User         user              `yaml:"user,omitempty"`
	Containerd   containerd        `yaml:"containerd"`
	Mounts       []mount           `yaml:"mounts"`
	PortForwards []portForward     `yaml:"portForwards"`
	Provision    []provision       `yaml:"provision,omitempty"`
	Message      string            `yaml:"message,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
}

type user struct {
	Name    string `yaml:"name,omitempty"`
	Comment string `yaml:"comment,omitempty"`
	UID     int    `yaml:"uid,omitempty"`
	Home    string `yaml:"home,omitempty"`
	Shell   string `yaml:"shell,omitempty"`
}

type containerd struct {
	System bool `yaml:"system"`
	User   bool `yaml:"user"`
}

type mount struct {
	Location   string `yaml:"location"`
	MountPoint string `yaml:"mountPoint"`
	Writable   bool   `yaml:"writable"`
}

type provision struct {
	Mode   string `yaml:"mode"`
	Script string `yaml:"script"`
}

type portForward struct {
	GuestIP           string `yaml:"guestIP,omitempty"`
	GuestIPMustBeZero *bool  `yaml:"guestIPMustBeZero,omitempty"`
	Proto             string `yaml:"proto,omitempty"`
	GuestPortRange    [2]int `yaml:"guestPortRange,omitempty"`
	Ignore            bool   `yaml:"ignore,omitempty"`
}

type ToolPreset struct {
	Name             string   `json:"name"`
	RequiredCommands []string `json:"requiredCommands"`
	RequiredPaths    []string `json:"requiredPaths,omitempty"`
	ProvisionScript  string   `json:"-"`
}

type NPMGlobalTool struct {
	Package  string   `json:"package"`
	Commands []string `json:"commands"`
}

type ToolPlan struct {
	Presets    []ToolPreset    `json:"presets"`
	NPMGlobals []NPMGlobalTool `json:"npmGlobals,omitempty"`
}

type ToolManifest struct {
	Version           string          `json:"version"`
	Presets           []ToolPreset    `json:"presets"`
	NPMGlobals        []NPMGlobalTool `json:"npmGlobals,omitempty"`
	ProfileID         string          `json:"profileId,omitempty"`
	IdentityID        string          `json:"identityId,omitempty"`
	IdentityMode      string          `json:"identityMode,omitempty"`
	IdentityRoot      string          `json:"identityRoot,omitempty"`
	InstanceName      string          `json:"instanceName"`
	CommandProxyShims []string        `json:"commandProxyShims"`
	GuestBootstrap    string          `json:"guestBootstrap"`
}

func (b Backend) Name() string {
	return "lima"
}

func (b Backend) Available(context.Context) error {
	runner := b.runner()
	if _, err := runner.LookPath(b.limactl()); err != nil {
		return fmt.Errorf("limactl is required for lima backend: %w", err)
	}
	return nil
}

func (b Backend) Prepare(_ context.Context, spec backend.RunSpec) (*backend.Session, error) {
	if spec.SessionID == "" {
		return nil, errors.New("session ID is required")
	}
	if spec.Profile.Name == "" {
		return nil, errors.New("profile name is required")
	}
	if spec.HostWork == "" || spec.GuestWork == "" {
		return nil, errors.New("workspace mapping is required")
	}
	if spec.ProfileDir == "" || spec.SessionDir == "" {
		return nil, errors.New("profile and session directories are required")
	}
	identityMode := spec.IdentityMode
	if identityMode == "" {
		identityMode = "persistent"
	}
	identityRoot := spec.IdentityRoot
	if identityRoot == "" {
		identityRoot = spec.ProfileDir
	}
	instance := spec.InstanceName
	if instance == "" {
		instance = InstanceNameForSession(spec.Profile.Name, spec.SessionID)
	}
	configPath := filepath.Join(spec.SessionDir, "lima.yaml")
	bootstrapPath := filepath.Join(spec.SessionDir, "bootstrap", "bootstrap.sh")
	manifestPath := filepath.Join(spec.SessionDir, "tool-preset.json")
	tools, err := ResolveToolPlan(spec.Profile.Tools)
	if err != nil {
		return nil, err
	}
	registry, err := cmdproxy.RegistryFromProfile(spec.Profile)
	if err != nil {
		return nil, err
	}
	commandProxyShims := append([]string{"hideout-shim"}, registry.ShimNames()...)
	if err := WriteBootstrap(bootstrapPath, tools, commandProxyShims); err != nil {
		return nil, err
	}
	if err := WriteToolManifest(manifestPath, ToolManifest{
		Version:           "hideout.tool-bootstrap/v1",
		Presets:           tools.Presets,
		NPMGlobals:        tools.NPMGlobals,
		ProfileID:         spec.Profile.Metadata["profileId"],
		IdentityID:        spec.Profile.Metadata["identityId"],
		IdentityMode:      identityMode,
		IdentityRoot:      identityRoot,
		InstanceName:      instance,
		CommandProxyShims: commandProxyShims,
		GuestBootstrap:    GuestBootstrapPath,
	}); err != nil {
		return nil, err
	}
	cfg := ConfigFromRunSpec(spec)
	if err := WriteConfig(configPath, cfg); err != nil {
		return nil, err
	}
	return &backend.Session{
		ID:                        spec.SessionID,
		EnvironmentID:             spec.EnvironmentID,
		Backend:                   b.Name(),
		HostWork:                  spec.HostWork,
		GuestWork:                 spec.GuestWork,
		GuestHome:                 GuestProfileDir + "/home",
		Env:                       append([]string(nil), spec.Env...),
		ShimDir:                   GuestSessionDir + "/shims",
		Broker:                    spec.Broker,
		SessionDir:                spec.SessionDir,
		ProfileDir:                spec.ProfileDir,
		IdentityMode:              identityMode,
		IdentityRoot:              identityRoot,
		ConfigPath:                configPath,
		BootstrapPath:             bootstrapPath,
		ToolManifestPath:          manifestPath,
		NetworkBootstrapPath:      spec.NetworkBootstrapPath,
		NetworkBootstrapGuestPath: spec.NetworkBootstrapGuestPath,
		NetworkCleanupPath:        spec.NetworkCleanupPath,
		NetworkCleanupGuestPath:   spec.NetworkCleanupGuestPath,
		HostFSEnabled:             spec.HostFSEnabled,
		HostFSGrafts:              append([]string(nil), spec.HostFSGrafts...),
		PortBridges:               append([]backend.PortBridgeEndpoint(nil), spec.PortBridges...),
		InstanceName:              instance,
		PreserveInstance:          spec.PreserveInstance,
	}, nil
}

func (b Backend) Run(ctx context.Context, session *backend.Session, command []string, env []string) error {
	if len(command) == 0 {
		return errors.New("command is required")
	}
	if session == nil {
		return errors.New("session is required")
	}
	if session.ConfigPath == "" || session.InstanceName == "" {
		return errors.New("lima session is missing config path or instance name")
	}
	session.Env = append([]string(nil), env...)
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	startArgs := []string{"start", "--tty=false", "--name", session.InstanceName, session.ConfigPath}
	if session.PreserveInstance || session.EnvironmentID != "" {
		exists, err := b.instanceExists(ctx, runner, hostEnv, session.InstanceName)
		if err != nil {
			return err
		}
		if exists {
			startArgs = []string{"start", "--tty=false", session.InstanceName}
		}
	}
	if err := runner.Run(ctx, b.limactl(), startArgs, hostEnv, nil, b.stdout(), b.stderr()); err != nil {
		return err
	}
	if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, []string{GuestBootstrapPath}), hostEnv, b.stdin(), b.stdout(), b.stderr()); err != nil {
		return err
	}
	if session.NetworkBootstrapGuestPath != "" {
		if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, []string{session.NetworkBootstrapGuestPath}), hostEnv, b.stdin(), b.stdout(), b.stderr()); err != nil {
			return err
		}
	}
	if session.HostFSEnabled {
		if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, []string{"sh", "-c", HostFSStartScript(session.HostFSGrafts)}), hostEnv, b.stdin(), b.stdout(), b.stderr()); err != nil {
			return fmt.Errorf("hostfs start: %w", err)
		}
	}
	if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, CommandCheck(command[0])), hostEnv, b.stdin(), b.stdout(), b.stderr()); err != nil {
		return backend.CommandNotFoundError{
			Backend:   b.Name(),
			Command:   command[0],
			Path:      backend.EnvValue(env, "PATH"),
			Workspace: session.GuestWork,
			Hint:      "install the command in the Lima guest or add a Hideout tool preset; no host fallback was attempted",
		}
	}
	args := ShellArgs(session.InstanceName, session.GuestWork, env, command)
	return runner.Run(ctx, b.limactl(), args, hostEnv, b.stdin(), b.stdout(), b.stderr())
}

func (b Backend) StartHostToGuestBridge(ctx context.Context, instanceName, guestWork string, env []string, spec portbridge.Spec) (*portbridge.Bridge, error) {
	if strings.TrimSpace(instanceName) == "" {
		return nil, errors.New("lima host-to-guest bridge requires instance name")
	}
	if strings.TrimSpace(guestWork) == "" {
		return nil, errors.New("lima host-to-guest bridge requires guest workdir")
	}
	targetHost, targetPort, err := net.SplitHostPort(spec.TargetAddress)
	if err != nil {
		return nil, fmt.Errorf("lima host-to-guest bridge target is invalid: %w", err)
	}
	targetHost = strings.Trim(targetHost, "[]")
	if targetHost == "" || targetHost == "localhost" {
		targetHost = "127.0.0.1"
	}
	if parsed := net.ParseIP(targetHost); parsed == nil || !parsed.IsLoopback() {
		return nil, errors.New("lima host-to-guest bridge target must be guest loopback")
	}
	if _, err := net.LookupPort("tcp", targetPort); err != nil {
		return nil, fmt.Errorf("lima host-to-guest bridge target port is invalid: %w", err)
	}
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	limactl := b.limactl()
	script := `host=$1
port=$2
exec 3<>/dev/tcp/${host}/${port}
cat <&3 &
cat >&3
wait`
	connector := func(connCtx context.Context, inbound net.Conn) error {
		args := ShellArgs(instanceName, guestWork, env, []string{"bash", "-lc", script, "hideout-portbridge", targetHost, targetPort})
		return runner.Run(connCtx, limactl, args, hostEnv, inbound, inbound, io.Discard)
	}
	return portbridge.StartWithConnector(ctx, spec, connector)
}

func (b Backend) instanceExists(ctx context.Context, runner CommandRunner, hostEnv []string, instance string) (bool, error) {
	var out bytes.Buffer
	if err := runner.Run(ctx, b.limactl(), []string{"list", "--quiet"}, hostEnv, nil, &out, io.Discard); err != nil {
		return false, fmt.Errorf("list lima instances: %w", err)
	}
	for _, name := range strings.Fields(out.String()) {
		if name == instance {
			return true, nil
		}
	}
	return false, nil
}

func (b Backend) Cleanup(ctx context.Context, session *backend.Session) error {
	if session == nil {
		return nil
	}
	if session.InstanceName == "" {
		return errors.New("lima session is missing instance name")
	}
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	var errs []error
	if session.HostFSEnabled {
		if session.GuestWork == "" {
			errs = append(errs, errors.New("lima session is missing guest workdir"))
		} else if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, cleanupGuestEnv(session.Env), []string{"sh", "-c", HostFSCleanupScript()}), hostEnv, nil, b.stdout(), b.stderr()); err != nil {
			errs = append(errs, fmt.Errorf("hostfs cleanup: %w", err))
		}
	}
	if session.NetworkCleanupGuestPath != "" {
		if session.GuestWork == "" {
			errs = append(errs, errors.New("lima session is missing guest workdir"))
		} else if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, cleanupGuestEnv(session.Env), []string{session.NetworkCleanupGuestPath}), hostEnv, nil, b.stdout(), b.stderr()); err != nil {
			errs = append(errs, fmt.Errorf("network cleanup: %w", err))
		}
	}
	if session.PreserveInstance {
		return errors.Join(errs...)
	}
	if err := runner.Run(ctx, b.limactl(), []string{"delete", "-f", session.InstanceName}, hostEnv, nil, io.Discard, io.Discard); err != nil {
		errs = append(errs, fmt.Errorf("delete lima instance %s: %w", session.InstanceName, err))
	}
	return errors.Join(errs...)
}

func cleanupGuestEnv(env []string) []string {
	out := append([]string(nil), env...)
	if backend.EnvValue(out, "PATH") == "" {
		out = append(out, "PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
	}
	return out
}

func HostCommandEnv(base []string) []string {
	allowed := map[string]bool{
		"PATH":            true,
		"HOME":            true,
		"USER":            true,
		"LOGNAME":         true,
		"SHELL":           true,
		"TMPDIR":          true,
		"TMP":             true,
		"TEMP":            true,
		"XDG_RUNTIME_DIR": true,
		"LIMA_HOME":       true,
	}
	out := make([]string, 0, len(allowed))
	seen := map[string]bool{}
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !allowed[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
}

func ConfigFromRunSpec(spec backend.RunSpec) limaConfig {
	identityRoot := spec.IdentityRoot
	if identityRoot == "" {
		identityRoot = spec.ProfileDir
	}
	return limaConfig{
		Base:         []string{"template:_images/ubuntu-lts"},
		VMType:       "vz",
		MountType:    "virtiofs",
		MountInotify: true,
		User: user{
			Name:    spec.Profile.Identity.User,
			Comment: "Hideout profile user",
			UID:     1000,
			Home:    guestUserHome(spec.Profile.Identity.User),
			Shell:   "/bin/bash",
		},
		Containerd: containerd{
			System: false,
			User:   false,
		},
		Mounts: append([]mount{
			{Location: spec.HostWork, MountPoint: spec.GuestWork, Writable: true},
		}, append(identityStateMounts(identityRoot), sessionStateMounts(spec.SessionDir, spec.EnvironmentID != "")...)...),
		PortForwards: []portForward{
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
		},
		Provision: append([]provision{
			{
				Mode: "system",
				Script: strings.TrimSpace(fmt.Sprintf(`#!/bin/sh
set -eu
	mkdir -p /hideout/profile/home /hideout/profile/config /hideout/profile/cache /hideout/profile/data /hideout/profile/browser /hideout/profile/machine /hideout/session/tmp /hideout/session/shims /hideout/session/network /hideout/session/bootstrap /hideout/hostfs
chown %s:%s /hideout /hideout/hostfs /hideout/session/tmp /hideout/session/shims /hideout/session/network /hideout/session/bootstrap 2>/dev/null || true
for root in Users Volumes private; do
  if [ ! -e "/$root" ]; then
    ln -s "/hideout/hostfs/$root" "/$root" 2>/dev/null || true
  fi
done
printf '%%s\n' %s > /etc/hostname
if command -v hostname >/dev/null 2>&1; then
  hostname %s || true
fi
if [ -r /hideout/profile/machine/machine-id ]; then
  cp /hideout/profile/machine/machine-id /etc/machine-id
  chmod 0444 /etc/machine-id || true
  mkdir -p /var/lib/dbus
  cp /etc/machine-id /var/lib/dbus/machine-id 2>/dev/null || true
fi
`, shellQuote(spec.Profile.Identity.User), shellQuote(spec.Profile.Identity.User), shellQuote(spec.Profile.Identity.Hostname), shellQuote(spec.Profile.Identity.Hostname))) + "\n",
			},
		}, toolProvisionBlocks(spec.Profile.Tools)...),
		Message: "Hideout managed Lima instance. Do not mount the real host home.",
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func guestUserHome(user string) string {
	if user == "" {
		user = "developer"
	}
	return "/home/" + user
}

func identityStateMounts(identityRoot string) []mount {
	subdirs := []string{"home", "cache", "config", "data", "browser", "machine"}
	mounts := make([]mount, 0, len(subdirs))
	for _, subdir := range subdirs {
		mounts = append(mounts, mount{
			Location:   filepath.Join(identityRoot, subdir),
			MountPoint: GuestProfileDir + "/" + subdir,
			Writable:   subdir != "machine",
		})
	}
	return mounts
}

func sessionStateMounts(sessionDir string, reusableEnvironment bool) []mount {
	if reusableEnvironment {
		return []mount{{
			Location:   sessionDir,
			MountPoint: GuestSessionDir,
			Writable:   true,
		}}
	}
	subdirs := []string{"tmp", "shims", "network", "bootstrap"}
	mounts := make([]mount, 0, len(subdirs))
	for _, subdir := range subdirs {
		mounts = append(mounts, mount{
			Location:   filepath.Join(sessionDir, subdir),
			MountPoint: GuestSessionDir + "/" + subdir,
			Writable:   true,
		})
	}
	return mounts
}

func WriteConfig(path string, cfg limaConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func WriteBootstrap(path string, tools ToolPlan, commandProxyShims []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	script := BootstrapScript(tools, commandProxyShims)
	return os.WriteFile(path, []byte(script), 0o700)
}

func WriteToolManifest(path string, manifest ToolManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func BootstrapScript(tools ToolPlan, commandProxyShims []string) string {
	commands := uniqueStrings(append(
		flatMap(tools.Presets, func(p ToolPreset) []string { return p.RequiredCommands }),
		flatMapNPMGlobals(tools.NPMGlobals, func(p NPMGlobalTool) []string { return p.Commands })...,
	))
	paths := uniqueStrings(flatMap(tools.Presets, func(p ToolPreset) []string { return p.RequiredPaths }))
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -eu\n")
	b.WriteString("mkdir -p /hideout/profile/home /hideout/profile/config /hideout/profile/cache /hideout/profile/data /hideout/profile/browser /hideout/session/tmp /hideout/session/shims /hideout/session/network /hideout/session/bootstrap\n")
	for _, command := range commands {
		fmt.Fprintf(&b, "command -v %s >/dev/null 2>&1 || { echo 'hideout: required guest command missing: %s' >&2; exit 127; }\n", shellQuote(command), command)
	}
	for _, path := range paths {
		fmt.Fprintf(&b, "[ -e %s ] || { echo 'hideout: required guest path missing: %s' >&2; exit 127; }\n", shellQuote(path), path)
	}
	if len(commandProxyShims) == 0 {
		commandProxyShims = append([]string{"hideout-shim"}, cmdproxy.DefaultRegistry().ShimNames()...)
	}
	for _, shim := range commandProxyShims {
		path := GuestSessionDir + "/shims/" + shim
		fmt.Fprintf(&b, "[ -x %s ] || { echo 'hideout: required command proxy shim missing: %s' >&2; exit 127; }\n", shellQuote(path), path)
	}
	return b.String()
}

func HostFSStartScript(grafts []string) string {
	var b strings.Builder
	b.WriteString(`set -eu
if grep -qs ' /hideout/hostfs ' /proc/mounts; then
  fusermount3 -u /hideout/hostfs 2>/dev/null || fusermount -u /hideout/hostfs 2>/dev/null || umount /hideout/hostfs 2>/dev/null || sudo -n umount /hideout/hostfs 2>/dev/null || true
fi
mkdir -p /hideout/session/tmp 2>/dev/null || true
mkdir -p /hideout/hostfs 2>/dev/null || {
  sudo -n mkdir -p /hideout/hostfs
  sudo -n chown "$(id -un):$(id -gn)" /hideout/hostfs 2>/dev/null || true
}
for root in Users Volumes private; do
  if [ ! -e "/$root" ]; then
    ln -s "/hideout/hostfs/$root" "/$root" 2>/dev/null || sudo -n ln -s "/hideout/hostfs/$root" "/$root" 2>/dev/null || true
  fi
done
`)
	for _, graft := range grafts {
		graft = filepath.Clean(graft)
		if graft == "." || graft == "/" {
			continue
		}
		fmt.Fprintf(&b, `if [ ! -e %s ] && [ ! -L %s ]; then
  mkdir -p "$(dirname %s)" 2>/dev/null || sudo -n mkdir -p "$(dirname %s)" 2>/dev/null || true
  ln -s %s %s 2>/dev/null || sudo -n ln -s %s %s 2>/dev/null || true
fi
`, shellQuote(graft), shellQuote(graft), shellQuote(graft), shellQuote(graft), shellQuote("/hideout/hostfs"+graft), shellQuote(graft), shellQuote("/hideout/hostfs"+graft), shellQuote(graft))
	}
	b.WriteString(`
if grep -qs ' /hideout/hostfs ' /proc/mounts; then
  echo 'hideout: existing HostFS mount could not be reset' >&2
  exit 70
fi
if [ ! -e /dev/fuse ]; then
  echo 'hideout: HostFS requires /dev/fuse in the Lima guest' >&2
  exit 70
fi
if [ ! -x /hideout/session/shims/hideout-hostfsd ]; then
  echo 'hideout: hideout-hostfsd is missing from session shims' >&2
  exit 70
fi
rm -f /hideout/session/tmp/hostfsd.pid /hideout/session/tmp/hostfsd.log
if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
  nohup sudo -n env \
    HIDEOUT_BROKER_ENDPOINT="$HIDEOUT_BROKER_ENDPOINT" \
    HIDEOUT_SESSION_ID="$HIDEOUT_SESSION_ID" \
    HIDEOUT_CAPABILITY_TOKEN="$HIDEOUT_CAPABILITY_TOKEN" \
    /hideout/session/shims/hideout-hostfsd --mount /hideout/hostfs > /hideout/session/tmp/hostfsd.log 2>&1 &
else
  nohup /hideout/session/shims/hideout-hostfsd --mount /hideout/hostfs > /hideout/session/tmp/hostfsd.log 2>&1 &
fi
echo "$!" > /hideout/session/tmp/hostfsd.pid
i=0
while [ "$i" -lt 50 ]; do
  if grep -qs ' /hideout/hostfs ' /proc/mounts; then
    exit 0
  fi
  if ! kill -0 "$(cat /hideout/session/tmp/hostfsd.pid)" 2>/dev/null; then
    cat /hideout/session/tmp/hostfsd.log >&2 || true
    exit 70
  fi
  i=$((i + 1))
  sleep 0.1
done
cat /hideout/session/tmp/hostfsd.log >&2 || true
exit 70
`)
	return b.String()
}

func HostFSCleanupScript() string {
	return `set +e
if grep -qs ' /hideout/hostfs ' /proc/mounts; then
  fusermount3 -u /hideout/hostfs 2>/dev/null || fusermount -u /hideout/hostfs 2>/dev/null || umount /hideout/hostfs 2>/dev/null || sudo -n umount /hideout/hostfs 2>/dev/null || true
fi
if [ -f /hideout/session/tmp/hostfsd.pid ]; then
  kill "$(cat /hideout/session/tmp/hostfsd.pid)" 2>/dev/null || true
fi
rm -f /hideout/session/tmp/hostfsd.pid
`
}

func EffectiveToolPresetNames(names []string) []string {
	if len(names) == 0 {
		return []string{"base-dev"}
	}
	return names
}

func ResolveToolPresets(names []string) ([]ToolPreset, error) {
	names = EffectiveToolPresetNames(names)
	out := make([]ToolPreset, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("tool preset cannot be empty")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		switch name {
		case "base-dev":
			out = append(out, ToolPreset{
				Name:             "base-dev",
				RequiredCommands: []string{"sh", "git", "curl"},
				RequiredPaths:    []string{"/etc/ssl/certs"},
			})
		case "node-dev":
			out = append(out, ToolPreset{
				Name:             "node-dev",
				RequiredCommands: []string{"sh", "git", "curl", "node", "npm"},
				RequiredPaths:    []string{"/etc/ssl/certs"},
				ProvisionScript:  NodeDevProvisionScript(),
			})
		default:
			return nil, fmt.Errorf("unsupported tool preset %q", name)
		}
	}
	return out, nil
}

func ResolveToolPlan(tools profile.Tools) (ToolPlan, error) {
	presets, err := ResolveToolPresets(tools.Presets)
	if err != nil {
		return ToolPlan{}, err
	}
	plan := ToolPlan{Presets: presets}
	for _, pkg := range tools.NPMGlobals {
		plan.NPMGlobals = append(plan.NPMGlobals, NPMGlobalTool{
			Package:  strings.TrimSpace(pkg.Package),
			Commands: append([]string(nil), pkg.Commands...),
		})
	}
	return plan, nil
}

func toolProvisionBlocks(tools profile.Tools) []provision {
	plan, err := ResolveToolPlan(tools)
	if err != nil {
		return nil
	}
	var out []provision
	for _, preset := range plan.Presets {
		if strings.TrimSpace(preset.ProvisionScript) == "" {
			continue
		}
		out = append(out, provision{Mode: "system", Script: strings.TrimSpace(preset.ProvisionScript) + "\n"})
	}
	for _, pkg := range plan.NPMGlobals {
		out = append(out, provision{Mode: "system", Script: NPMGlobalProvisionScript(pkg)})
	}
	return out
}

func NodeDevProvisionScript() string {
	return `#!/bin/sh
set -eu
if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  exit 0
fi
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl nodejs npm
command -v node >/dev/null 2>&1
command -v npm >/dev/null 2>&1
`
}

func NPMGlobalProvisionScript(pkg NPMGlobalTool) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -eu\n")
	if len(pkg.Commands) > 0 {
		b.WriteString("if")
		for i, command := range pkg.Commands {
			if i > 0 {
				b.WriteString(" &&")
			}
			fmt.Fprintf(&b, " command -v %s >/dev/null 2>&1", shellQuote(command))
		}
		b.WriteString("; then\n  exit 0\nfi\n")
	}
	fmt.Fprintf(&b, "npm install -g %s\n", shellQuote(pkg.Package))
	for _, command := range pkg.Commands {
		fmt.Fprintf(&b, "command -v %s >/dev/null 2>&1\n", shellQuote(command))
	}
	return b.String()
}

func ShellArgs(instance, workdir string, env []string, command []string) []string {
	args := []string{"shell", "--tty=false", "--workdir", workdir, instance, "--", "env", "-i"}
	args = append(args, env...)
	args = append(args, command...)
	return args
}

func CommandCheck(command string) []string {
	return []string{"sh", "-c", "command -v \"$1\" >/dev/null 2>&1", "hideout-command-check", command}
}

func flatMap(values []ToolPreset, fn func(ToolPreset) []string) []string {
	var out []string
	for _, value := range values {
		out = append(out, fn(value)...)
	}
	return out
}

func flatMapNPMGlobals(values []NPMGlobalTool, fn func(NPMGlobalTool) []string) []string {
	var out []string
	for _, value := range values {
		out = append(out, fn(value)...)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func GuestBrokerEndpoint(listen broker.Endpoint) (broker.Endpoint, error) {
	if listen.Network != broker.EndpointTCP {
		return listen, nil
	}
	_, port, err := net.SplitHostPort(listen.Address)
	if err != nil {
		return broker.Endpoint{}, err
	}
	return broker.TCPEndpoint(net.JoinHostPort("host.lima.internal", port)), nil
}

func GuestEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch name {
		case "HOME":
			value = GuestProfileDir + "/home"
		case "TMPDIR":
			value = GuestSessionDir + "/tmp"
		case "XDG_CONFIG_HOME":
			value = GuestProfileDir + "/config"
		case "XDG_CACHE_HOME":
			value = GuestProfileDir + "/cache"
		case "XDG_DATA_HOME":
			value = GuestProfileDir + "/data"
		case "GIT_CONFIG_GLOBAL":
			value = GuestProfileDir + "/home/.gitconfig"
		case "PATH":
			value = GuestSessionDir + "/shims:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
		}
		out = append(out, name+"="+value)
	}
	return out
}

func InstanceName(profileName string) string {
	return instanceName(profileName, "")
}

func InstanceNameForProfile(profileName, identityID string) string {
	return instanceName(profileName, identityID)
}

func InstanceNameForSession(profileName, sessionID string) string {
	return instanceName(profileName+"-session", sessionSuffix(sessionID))
}

func InstanceNameForEnvironment(profileName, environmentID string) string {
	return instanceName(profileName+"-env", environmentSuffix(environmentID))
}

func instanceName(profileName, identityID string) string {
	name := strings.ToLower(profileName)
	name = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "default"
	}
	suffix := identitySuffix(identityID)
	maxName := 48
	if suffix != "" {
		maxName = 48 - len(suffix) - 1
		if maxName < 8 {
			maxName = 8
		}
	}
	if len(name) > maxName {
		name = name[:maxName]
		name = strings.TrimRight(name, "-")
	}
	if suffix != "" {
		return "hideout-" + name + "-" + suffix
	}
	return "hideout-" + name
}

func identitySuffix(identityID string) string {
	identityID = strings.ToLower(identityID)
	identityID = strings.TrimPrefix(identityID, "id_")
	identityID = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(identityID, "")
	if len(identityID) > 12 {
		identityID = identityID[:12]
	}
	return identityID
}

func sessionSuffix(sessionID string) string {
	sessionID = strings.ToLower(sessionID)
	sessionID = strings.TrimPrefix(sessionID, "ses_")
	sessionID = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(sessionID, "")
	if len(sessionID) > 12 {
		sessionID = sessionID[len(sessionID)-12:]
	}
	return sessionID
}

func environmentSuffix(environmentID string) string {
	environmentID = strings.ToLower(environmentID)
	environmentID = strings.TrimPrefix(environmentID, "env_")
	environmentID = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(environmentID, "")
	if len(environmentID) > 12 {
		environmentID = environmentID[len(environmentID)-12:]
	}
	return environmentID
}

func (b Backend) runner() CommandRunner {
	if b.Runner != nil {
		return b.Runner
	}
	return ExecRunner{}
}

func (b Backend) limactl() string {
	if b.LimactlPath != "" {
		return b.LimactlPath
	}
	return "limactl"
}

func (b Backend) stdin() io.Reader {
	if b.Stdin != nil {
		return b.Stdin
	}
	return os.Stdin
}

func (b Backend) stdout() io.Writer {
	if b.Stdout != nil {
		return b.Stdout
	}
	return os.Stdout
}

func (b Backend) stderr() io.Writer {
	if b.Stderr != nil {
		return b.Stderr
	}
	return os.Stderr
}
