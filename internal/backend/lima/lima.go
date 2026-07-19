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
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/portbridge"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const (
	GuestProfileDir       = "/hideout/profile"
	GuestSessionDir       = "/hideout/session"
	GuestRuntimeDir       = "/hideout/runtime"
	GuestBootstrapPath    = GuestSessionDir + "/bootstrap/bootstrap.sh"
	GuestNetworkBootstrap = GuestSessionDir + "/network/bootstrap.sh"
	cleanupTimeout        = 30 * time.Second
	startupNoticeDelay    = time.Second
	startupNoticeInterval = 30 * time.Second
)

var errRuntimeNotReady = errors.New("lima instance did not become ready; skipped guest cleanup")

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
	LimactlPath   string
	Runner        CommandRunner
	SetupRunner   SetupCommandRunner
	SSHClients    *SSHClientPool
	Stdout        io.Writer
	Stderr        io.Writer
	ControlStdout io.Writer
	ControlStderr io.Writer
	Progress      io.Writer
	Stdin         io.Reader
}

type limaConfig struct {
	Base         []string          `yaml:"base,omitempty"`
	Images       []limaImage       `yaml:"images,omitempty"`
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

type ToolManifest struct {
	Version           string   `json:"version"`
	ExpectedCommands  []string `json:"expectedCommands,omitempty"`
	ProfileID         string   `json:"profileId,omitempty"`
	IdentityID        string   `json:"identityId,omitempty"`
	IdentityMode      string   `json:"identityMode,omitempty"`
	IdentityRoot      string   `json:"identityRoot,omitempty"`
	InstanceName      string   `json:"instanceName"`
	CommandProxyShims []string `json:"commandProxyShims"`
	GuestBootstrap    string   `json:"guestBootstrap"`
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
	if err := spec.Machine.Validate(); err != nil {
		return nil, err
	}
	if err := spec.Workspace.Validate(spec.Machine.Mode); err != nil {
		return nil, err
	}
	if spec.SessionDir == "" {
		return nil, errors.New("session directory is required")
	}
	identityMode := spec.Machine.IdentityMode
	if identityMode == "" {
		identityMode = "persistent"
	}
	identityRoot := spec.Machine.IdentityRoot
	if identityRoot == "" {
		identityRoot = spec.Machine.ProfileDir
	}
	instance := spec.Machine.InstanceName
	if instance == "" {
		instance = InstanceNameForSession(spec.Machine.Profile.Name, spec.SessionID)
	}
	configPath := filepath.Join(spec.SessionDir, "lima.yaml")
	bootstrapPath := filepath.Join(spec.SessionDir, "bootstrap", "bootstrap.sh")
	manifestPath := filepath.Join(spec.SessionDir, "guest-bootstrap.json")
	registry, err := cmdproxy.RegistryFromProfile(spec.Machine.Profile)
	if err != nil {
		return nil, err
	}
	commandProxyShims := append([]string{"hideout-shim"}, registry.ShimNames()...)
	if err := WriteBootstrap(bootstrapPath, commandProxyShims); err != nil {
		return nil, err
	}
	if err := WriteToolManifest(manifestPath, ToolManifest{
		Version:           "hideout.tool-bootstrap/v1",
		ExpectedCommands:  append([]string(nil), spec.Machine.Profile.Tools.ExpectedCommands...),
		ProfileID:         spec.Machine.Profile.Metadata["profileId"],
		IdentityID:        spec.Machine.Profile.Metadata["identityId"],
		IdentityMode:      identityMode,
		IdentityRoot:      identityRoot,
		InstanceName:      instance,
		CommandProxyShims: commandProxyShims,
		GuestBootstrap:    GuestBootstrapPath,
	}); err != nil {
		return nil, err
	}
	var staticMounts *StaticRunMounts
	if spec.Machine.Mode != environment.ModeShared {
		staticMounts = &StaticRunMounts{Workspace: spec.Workspace, SessionDir: spec.SessionDir}
	}
	cfg, err := ConfigForMachineSpec(spec.Machine, staticMounts)
	if err != nil {
		return nil, err
	}
	if err := WriteConfig(configPath, cfg); err != nil {
		return nil, err
	}
	privilegedSetupRequired := spec.PrivilegedSetupRequired || spec.NetworkPrivilegedSetup || spec.HostFSEnabled
	return &backend.Session{
		ID:                        spec.SessionID,
		EnvironmentID:             spec.Machine.EnvironmentID,
		Backend:                   b.Name(),
		HostWork:                  spec.Workspace.HostRoot,
		GuestWork:                 spec.Workspace.GuestRoot,
		Workspace:                 spec.Workspace,
		GuestHome:                 GuestProfileDir + "/home",
		Env:                       append([]string(nil), spec.Env...),
		ShimDir:                   GuestSessionDir + "/shims",
		Broker:                    spec.Broker,
		SessionDir:                spec.SessionDir,
		RuntimeRoot:               spec.Machine.RuntimeRoot,
		SessionIsolationRequired:  spec.SessionIsolationRequired,
		TargetUser:                spec.TargetUser,
		ProfileDir:                spec.Machine.ProfileDir,
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
		PreserveInstance:          spec.Machine.PreserveInstance,
		NetworkPrivilegedSetup:    spec.NetworkPrivilegedSetup,
		PrivilegedSetupRequired:   privilegedSetupRequired,
		PrivilegeStatusSink:       spec.PrivilegeStatusSink,
		PrivilegedSetupEventSink:  spec.PrivilegedSetupEventSink,
		RuntimeContract:           backend.CloneRuntimeContract(spec.RuntimeContract),
		RuntimeInstanceExpected:   cloneRuntimeInstanceExpectation(spec.RuntimeInstanceExpected),
		RuntimeResultSink:         spec.RuntimeResultSink,
		RuntimeCompletionSink:     spec.RuntimeCompletionSink,
	}, nil
}

func (b Backend) Run(ctx context.Context, session *backend.Session, command []string, env []string) (retErr error) {
	if len(command) == 0 {
		return errors.New("command is required")
	}
	if session == nil {
		return errors.New("session is required")
	}
	if session.ConfigPath == "" || session.InstanceName == "" {
		return errors.New("lima session is missing config path or instance name")
	}
	if !session.RuntimeReady {
		if err := b.Activate(ctx, session, env); err != nil {
			return err
		}
	}
	if session.RuntimeCompletionSink != nil {
		defer func() { retErr = errors.Join(retErr, session.RuntimeCompletionSink(retErr)) }()
	}
	if session.SessionIsolationRequired {
		return b.runIsolatedSession(ctx, session, env, command)
	}
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	setupEnv := SetupEnv(env)
	setupWorkdir := GuestSessionDir + "/tmp"
	if session.NetworkBootstrapGuestPath != "" {
		if session.NetworkPrivilegedSetup {
			if err := b.runSetupCommand(ctx, session, setupCategoryNetwork, setupWorkdir, setupEnv, []string{session.NetworkBootstrapGuestPath}, b.stdin()); err != nil {
				return err
			}
		} else {
			if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, setupWorkdir, setupEnv, []string{session.NetworkBootstrapGuestPath}), hostEnv, b.stdin(), b.controlStdout(), b.controlStderr()); err != nil {
				return err
			}
		}
	}
	if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, setupWorkdir, setupEnv, []string{GuestBootstrapPath}), hostEnv, b.stdin(), b.controlStdout(), b.controlStderr()); err != nil {
		return err
	}
	if session.HostFSEnabled {
		if err := b.runSetupCommand(ctx, session, setupCategoryHostFS, session.GuestWork, env, []string{"sh", "-c", HostFSStartScript(session.HostFSGrafts)}, b.stdin()); err != nil {
			return fmt.Errorf("hostfs start: %w", err)
		}
	}
	if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, CommandCheck(command[0])), hostEnv, b.stdin(), b.controlStdout(), b.controlStderr()); err != nil {
		return backend.CommandNotFoundError{
			Backend:   b.Name(),
			Command:   command[0],
			Path:      backend.EnvValue(env, "PATH"),
			Workspace: session.GuestWork,
			Hint:      "install the command in the guest base environment or run in-boundary setup before retrying; no host fallback was attempted",
		}
	}
	if b.canBridgeTerminal() && b.terminalBridgeAvailable(ctx, runner, hostEnv, session, env) {
		return b.runWithTerminalBridge(ctx, runner, hostEnv, session, env, command)
	}
	args := ShellArgs(session.InstanceName, session.GuestWork, env, command)
	return runner.Run(ctx, b.limactl(), args, hostEnv, b.stdin(), b.stdout(), b.stderr())
}

func (b Backend) Activate(ctx context.Context, session *backend.Session, env []string) error {
	_, _, err := b.startAndObserveRuntime(ctx, session, env)
	if err != nil {
		return err
	}
	if session.SessionIsolationRequired {
		bootID, err := b.sessionViewActivationProbe(ctx, session)
		if err != nil {
			session.RuntimeReady = false
			return err
		}
		receipt, err := backend.BuildActivationReceipt(session, bootID, time.Now().UTC())
		if err != nil {
			session.RuntimeReady = false
			return fmt.Errorf("build Lima activation receipt: %w", err)
		}
		if err := backend.WriteActivationReceipt(session.RuntimeRoot, receipt); err != nil {
			session.RuntimeReady = false
			return fmt.Errorf("write Lima activation receipt: %w", err)
		}
		session.ExpectedBootID = receipt.BootID
	}
	return nil
}

func (b Backend) WarmActivationOwner(session *backend.Session) (string, error) {
	if session == nil || !session.SessionIsolationRequired {
		return "", errors.New("warm Lima activation requires an isolated prepared session")
	}
	receipt, err := backend.LoadActivationReceipt(session.RuntimeRoot)
	if err != nil {
		return "", err
	}
	if err := receipt.MatchesSession(session); err != nil {
		return "", err
	}
	return receipt.OwnerSessionID, nil
}

func (b Backend) WarmActivate(ctx context.Context, session *backend.Session, env []string) error {
	if session == nil || session.InstanceName == "" || session.ActivationOwnerID == "" {
		return errors.New("warm Lima activation requires a prepared instance")
	}
	receipt, err := backend.LoadActivationReceipt(session.RuntimeRoot)
	if err != nil {
		return fmt.Errorf("warm Lima activation receipt: %w", err)
	}
	if err := receipt.MatchesSession(session); err != nil {
		return fmt.Errorf("warm Lima activation receipt drift: %w", err)
	}
	if receipt.OwnerSessionID != session.ActivationOwnerID || receipt.OwnerSessionID == session.ID {
		return errors.New("warm Lima activation receipt owner is not the proved sibling owner")
	}
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	exists, err := b.instanceExists(ctx, runner, hostEnv, session.InstanceName)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("warm Lima activation refused because the instance is absent")
	}
	session.ExpectedBootID = receipt.BootID
	session.Env = append([]string(nil), env...)
	session.RunAttempted = true
	session.RuntimeReady = true
	return nil
}

func (b Backend) ReconcileEnvironmentBoot(ctx context.Context, session *backend.Session, configuration environment.BootConfiguration, env []string) error {
	if session == nil || strings.TrimSpace(session.InstanceName) == "" {
		return errors.New("environment boot reconciliation requires a running instance")
	}
	if err := configuration.Validate(); err != nil {
		return err
	}
	script := `set -eu
hostname_value=$1
tmp=/etc/hostname.hideout-tmp
printf '%s\n' "$hostname_value" > "$tmp"
chmod 0644 "$tmp"
mv "$tmp" /etc/hostname
hostname "$hostname_value"
test "$(hostname)" = "$hostname_value"
test "$(cat /etc/hostname)" = "$hostname_value"
`
	bootSession := *session
	bootSession.PrivilegedSetupRequired = true
	return b.runSetupCommand(
		ctx, &bootSession, setupCategoryBoot, "/", SetupEnv(env),
		[]string{"sh", "-c", script, "hideout-boot-reconcile", configuration.Hostname}, nil,
	)
}

func (b Backend) StartEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir, bootstrapPath string, env []string) error {
	if session == nil || strings.TrimSpace(bootstrapPath) == "" {
		return errors.New("environment network bootstrap is required")
	}
	return b.runSetupCommand(ctx, session, setupCategoryNetwork, workdir, SetupEnv(env), []string{bootstrapPath}, nil)
}

func (b Backend) VerifyEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir string, env []string) error {
	if session == nil || !sessionViewBootPattern.MatchString(session.ExpectedBootID) || path.Clean(workdir) != workdir || !path.IsAbs(workdir) {
		return errors.New("environment network verification requires a current boot identity and absolute service directory")
	}
	script := `set -eu
expected_boot=$1
service_dir=$2
test "$(cat /proc/sys/kernel/random/boot_id)" = "$expected_boot"
ip link show dev hideout0 >/dev/null 2>&1
ip route show default | grep -Eq '(^|[[:space:]])dev hideout0([[:space:]]|$)'
test -r "$service_dir/tun2socks.pid"
kill -0 "$(cat "$service_dir/tun2socks.pid")" 2>/dev/null
test -r "$service_dir/dns-stub.pid"
kill -0 "$(cat "$service_dir/dns-stub.pid")" 2>/dev/null
grep -q '^nameserver 127\.0\.0\.1\([[:space:]]\|$\)' /etc/resolv.conf
`
	return b.runSetupCommand(
		ctx, session, setupCategoryNetwork, "/", SetupEnv(env),
		[]string{"sh", "-c", script, "hideout-network-verify", session.ExpectedBootID, workdir}, nil,
	)
}

func (b Backend) VerifyDirectEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir string, env []string) error {
	if session == nil || !sessionViewBootPattern.MatchString(session.ExpectedBootID) || path.Clean(workdir) != workdir || !path.IsAbs(workdir) {
		return errors.New("direct environment network verification requires a current boot identity and absolute service directory")
	}
	script := `set -eu
expected_boot=$1
service_dir=$2
test "$(cat /proc/sys/kernel/random/boot_id)" = "$expected_boot"
if ip link show dev hideout0 >/dev/null 2>&1; then
  echo 'hideout: stale privacy TUN remains while direct network is selected' >&2
  exit 127
fi
default_route=$(ip route show default | head -n 1 || true)
[ -n "$default_route" ] || { echo 'hideout: direct network has no default route' >&2; exit 127; }
case "$default_route" in
  *' dev hideout0'|*' dev hideout0 '* ) echo 'hideout: direct network still routes through hideout0' >&2; exit 127 ;;
esac
if [ -r "$service_dir/dns-stub.pid" ] && kill -0 "$(cat "$service_dir/dns-stub.pid")" 2>/dev/null; then
  echo 'hideout: stale privacy DNS service remains while direct network is selected' >&2
  exit 127
fi
`
	return b.runSetupCommand(
		ctx, session, setupCategoryNetwork, "/", SetupEnv(env),
		[]string{"sh", "-c", script, "hideout-direct-network-verify", session.ExpectedBootID, workdir}, nil,
	)
}

func (b Backend) StopEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir, cleanupPath string, env []string) error {
	if session == nil || strings.TrimSpace(cleanupPath) == "" {
		return errors.New("environment network cleanup is required")
	}
	return b.runSetupCleanup(ctx, session, setupCategoryNetwork, workdir, cleanupGuestEnv(env), []string{cleanupPath})
}

func (b Backend) ReconfigureEnvironmentNetworkDNS(ctx context.Context, session *backend.Session, workdir, oldResolver, newResolver string, env []string) error {
	if session == nil || path.Clean(workdir) != workdir || !path.IsAbs(workdir) || net.ParseIP(oldResolver) == nil || net.ParseIP(newResolver) == nil {
		return errors.New("environment DNS reconfiguration requires a running service and IP-literal resolvers")
	}
	script := `set -eu
service_dir=$1
old_resolver=$2
new_resolver=$3
helper="$service_dir/hideout-dns-stub"
pid_file="$service_dir/dns-stub.pid"
rollback_marker="$service_dir/dns-switch-rollback-proved"
rm -f "$rollback_marker"
test -x "$helper"
test -r "$pid_file"
old_pid=$(sed -n '1p' "$pid_file")
kill "$old_pid" 2>/dev/null || true
i=0
while kill -0 "$old_pid" 2>/dev/null && [ "$i" -lt 30 ]; do sleep 0.1; i=$((i + 1)); done
if kill -0 "$old_pid" 2>/dev/null; then
  echo 'hideout: existing DNS service did not stop for reconfiguration' >&2
  exit 127
fi
start_stub() {
  resolver=$1
  "$helper" --listen 127.0.0.1:53 --doh-server "$resolver" > "$service_dir/dns-stub.log" 2>&1 &
  candidate_pid=$!
  sleep 0.2
  kill -0 "$candidate_pid" 2>/dev/null
}
if start_stub "$new_resolver"; then
  printf '%s\n' "$candidate_pid" > "$pid_file.tmp"
  mv "$pid_file.tmp" "$pid_file"
  printf '%s\n' "$new_resolver" > "$service_dir/mediated-resolver"
  exit 0
fi
if start_stub "$old_resolver"; then
  printf '%s\n' "$candidate_pid" > "$pid_file.tmp"
  mv "$pid_file.tmp" "$pid_file"
  printf '%s\n' "$old_resolver" > "$service_dir/mediated-resolver"
  : > "$rollback_marker"
fi
echo 'hideout: replacement DNS service failed; previous resolver restart was attempted' >&2
exit 127
	`
	reconfigureErr := b.runSetupCommand(
		ctx, session, setupCategoryNetwork, "/", SetupEnv(env),
		[]string{"sh", "-c", script, "hideout-network-dns-reconfigure", workdir, oldResolver, newResolver}, nil,
	)
	if reconfigureErr == nil {
		return nil
	}
	verifyRollback := `set -eu
service_dir=$1
old_resolver=$2
test -f "$service_dir/dns-switch-rollback-proved"
test "$(cat "$service_dir/mediated-resolver")" = "$old_resolver"
test -r "$service_dir/dns-stub.pid"
kill -0 "$(cat "$service_dir/dns-stub.pid")" 2>/dev/null
rm -f "$service_dir/dns-switch-rollback-proved"
`
	rollbackErr := b.runSetupCommand(
		ctx, session, setupCategoryNetwork, "/", SetupEnv(env),
		[]string{"sh", "-c", verifyRollback, "hideout-network-dns-rollback-verify", workdir, oldResolver}, nil,
	)
	return backend.EnvironmentServiceReconfigureError{
		Operation: "reconfigure environment DNS", RollbackProved: rollbackErr == nil,
		Cause: errors.Join(reconfigureErr, rollbackErr),
	}
}

func (b Backend) terminalBridgeAvailable(ctx context.Context, runner CommandRunner, hostEnv []string, session *backend.Session, env []string) bool {
	var out bytes.Buffer
	args := ShellArgs(session.InstanceName, session.GuestWork, env, []string{
		"sh", "-c", "command -v script >/dev/null 2>&1 && printf available || true",
	})
	if err := runner.Run(ctx, b.limactl(), args, hostEnv, nil, &out, b.controlStderr()); err != nil {
		return false
	}
	return strings.TrimSpace(out.String()) == "available"
}

func (b Backend) runWithTerminalBridge(ctx context.Context, runner CommandRunner, hostEnv []string, session *backend.Session, env, command []string) (retErr error) {
	stdin, _ := b.stdin().(*os.File)
	stdout, _ := b.stdout().(*os.File)
	if stdin == nil || stdout == nil {
		return errors.New("terminal bridge requires file-backed stdin and stdout")
	}
	width, height, err := term.GetSize(int(stdout.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}
	state, err := term.MakeRaw(int(stdin.Fd()))
	if err != nil {
		args := ShellArgs(session.InstanceName, session.GuestWork, env, command)
		return runner.Run(ctx, b.limactl(), args, hostEnv, b.stdin(), b.stdout(), b.stderr())
	}
	defer func() {
		retErr = errors.Join(retErr, term.Restore(int(stdin.Fd()), state))
	}()

	args := ShellArgs(session.InstanceName, session.GuestWork, env, terminalBridgeCommand(command, width, height))
	return runner.Run(
		ctx,
		b.limactl(),
		args,
		hostEnv,
		stdin,
		nonTerminalWriter{Writer: b.stdout()},
		nonTerminalWriter{Writer: b.stderr()},
	)
}

func (b Backend) canBridgeTerminal() bool {
	stdin, stdinOK := b.stdin().(*os.File)
	stdout, stdoutOK := b.stdout().(*os.File)
	return stdinOK && stdoutOK && terminalIsTerminal(int(stdin.Fd())) && terminalIsTerminal(int(stdout.Fd()))
}

type nonTerminalWriter struct {
	io.Writer
}

func terminalBridgeCommand(command []string, width, height int) []string {
	quoted := make([]string, 0, len(command))
	for _, arg := range command {
		quoted = append(quoted, shellQuote(arg))
	}
	script := "stty rows " + strconv.Itoa(height) + " cols " + strconv.Itoa(width) + " 2>/dev/null || true; exec " + strings.Join(quoted, " ")
	return []string{"script", "-qefc", script, "/dev/null"}
}

func (b Backend) VerifyRuntime(ctx context.Context, session *backend.Session, env []string) error {
	if session == nil || session.RuntimeContract == nil || session.RuntimeResultSink == nil {
		return errors.New("lima runtime verification requires a prepared session, contract, and result sink")
	}
	_, _, err := b.startAndObserveRuntime(ctx, session, env)
	return err
}

func (b Backend) startAndObserveRuntime(ctx context.Context, session *backend.Session, env []string) (CommandRunner, []string, error) {
	if session == nil {
		return nil, nil, errors.New("session is required")
	}
	if session.ConfigPath == "" || session.InstanceName == "" {
		return nil, nil, errors.New("lima session is missing config path or instance name")
	}
	session.Env = append([]string(nil), env...)
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	if session.RuntimeContract != nil {
		if session.RuntimeInstanceExpected == nil {
			return nil, nil, errors.New("runtime observation requires an instance expectation")
		}
		if err := session.RuntimeInstanceExpected.Validate(); err != nil {
			return nil, nil, err
		}
	}
	startArgs := []string{"start", "--tty=false", "--name", session.InstanceName, session.ConfigPath}
	if session.PreserveInstance || session.EnvironmentID != "" {
		exists, err := b.instanceExists(ctx, runner, hostEnv, session.InstanceName)
		if err != nil {
			return nil, nil, err
		}
		if exists {
			if session.RuntimeContract != nil {
				if _, err := b.inspectRuntimeInstance(ctx, runner, hostEnv, session, false); err != nil {
					return nil, nil, fmt.Errorf("refuse mismatched reusable Lima instance: %w", err)
				}
			}
			startArgs = []string{"start", "--tty=false", session.InstanceName}
		}
	}
	session.RunAttempted = true
	if err := runWithStartupProgress(b.Progress, session.InstanceName, func() error {
		return runner.Run(ctx, b.limactl(), startArgs, hostEnv, nil, b.controlStdout(), b.controlStderr())
	}); err != nil {
		return nil, nil, err
	}
	session.RuntimeReady = true
	var (
		instance backend.RuntimeInstanceObservation
		err      error
	)
	if session.RuntimeContract != nil {
		instance, err = b.inspectRuntimeInstance(ctx, runner, hostEnv, session, true)
		if err != nil {
			return nil, nil, err
		}
	}
	if session.PrivilegeStatusSink != nil {
		status := b.probeGuestPrivilege(ctx, session, runner, hostEnv, GuestEnv(env))
		session.PrivilegeStatus = &status
		if err := session.PrivilegeStatusSink(status); err != nil {
			return nil, nil, err
		}
	}
	if session.RuntimeContract != nil {
		if err := b.observeRuntime(ctx, session, runner, hostEnv, GuestEnv(env), instance); err != nil {
			return nil, nil, err
		}
	}
	return runner, hostEnv, nil
}

func cloneRuntimeInstanceExpectation(value *backend.RuntimeInstanceExpectation) *backend.RuntimeInstanceExpectation {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (b Backend) StartHostToGuestBridge(ctx context.Context, instanceName, guestWork string, env []string, spec portbridge.Spec) (*portbridge.Bridge, error) {
	return b.startSSHHostToGuestBridge(ctx, instanceName, guestWork, env, spec)
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
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	runner := b.runner()
	hostEnv := HostCommandEnv(os.Environ())
	var errs []error
	runtimeUnavailable := session.RunAttempted && !session.RuntimeReady
	if runtimeUnavailable && session.IsolationRunStarted {
		errs = append(errs, errRuntimeNotReady)
	}
	if !session.SessionIsolationRequired && !runtimeUnavailable && session.HostFSEnabled {
		if session.GuestWork == "" {
			errs = append(errs, errors.New("lima session is missing guest workdir"))
		} else if err := b.runSetupCleanup(cleanupCtx, session, setupCategoryHostFS, session.GuestWork, cleanupGuestEnv(session.Env), []string{"sh", "-c", HostFSCleanupScript()}); err != nil {
			errs = append(errs, fmt.Errorf("hostfs cleanup: %w", err))
		}
	}
	if !session.SessionIsolationRequired && !runtimeUnavailable && session.NetworkCleanupGuestPath != "" {
		if session.GuestWork == "" {
			errs = append(errs, errors.New("lima session is missing guest workdir"))
		} else if session.NetworkPrivilegedSetup {
			if err := b.runSetupCleanup(cleanupCtx, session, setupCategoryNetwork, session.GuestWork, cleanupGuestEnv(session.Env), []string{session.NetworkCleanupGuestPath}); err != nil {
				errs = append(errs, fmt.Errorf("network cleanup: %w", err))
			}
		} else if err := runner.Run(cleanupCtx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, cleanupGuestEnv(session.Env), []string{session.NetworkCleanupGuestPath}), hostEnv, nil, b.controlStdout(), b.controlStderr()); err != nil {
			errs = append(errs, fmt.Errorf("network cleanup: %w", err))
		}
	}
	if session.SessionIsolationRequired && session.IsolationRunStarted && !runtimeUnavailable && !session.IsolationCleanupProved {
		if err := b.verifyIsolatedSessionTerminated(cleanupCtx, session); err != nil {
			errs = append(errs, fmt.Errorf("session-view cleanup proof: %w", err))
		}
	}
	if session.PreserveInstance {
		return errors.Join(errs...)
	}
	if err := runner.Run(cleanupCtx, b.limactl(), []string{"delete", "-f", session.InstanceName}, hostEnv, nil, b.controlStdout(), b.controlStderr()); err != nil {
		errs = append(errs, fmt.Errorf("delete lima instance %s: %w", session.InstanceName, err))
	}
	return errors.Join(errs...)
}

func runWithStartupProgress(progress io.Writer, instance string, run func() error) error {
	return runWithStartupProgressTimings(progress, instance, startupNoticeDelay, startupNoticeInterval, run)
}

func runWithStartupProgressTimings(progress io.Writer, instance string, delay, interval time.Duration, run func() error) error {
	if progress == nil || progress == io.Discard {
		return run()
	}
	started := time.Now()
	result := make(chan error, 1)
	go func() {
		result <- run()
	}()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		fmt.Fprintf(progress, "hideout: starting Lima environment %q; first start may download the configured image (use --verbose for backend details)\n", instance)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err == nil {
				fmt.Fprintf(progress, "hideout: Lima environment ready (%s)\n", startupElapsed(time.Since(started)))
			}
			return err
		case <-ticker.C:
			fmt.Fprintf(progress, "hideout: still starting Lima environment %q (%s elapsed)\n", instance, startupElapsed(time.Since(started)))
		}
	}
}

func startupElapsed(elapsed time.Duration) time.Duration {
	if elapsed < time.Second {
		return time.Second
	}
	return elapsed.Round(time.Second)
}

func (b Backend) StopInstance(ctx context.Context, instanceName string) error {
	instanceName = strings.TrimSpace(instanceName)
	if instanceName == "" {
		return errors.New("lima instance name is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stopCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	if b.SSHClients != nil {
		if err := b.SSHClients.InvalidateInstance(instanceName); err != nil {
			return fmt.Errorf("close Lima SSH transports before stop: %w", err)
		}
	}
	return b.runner().Run(stopCtx, b.limactl(), []string{"stop", instanceName}, HostCommandEnv(os.Environ()), nil, b.controlStdout(), b.controlStderr())
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

func SetupEnv(base []string) []string {
	allowed := map[string]bool{
		"PATH":    true,
		"HOME":    true,
		"USER":    true,
		"LOGNAME": true,
		"SHELL":   true,
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

type limaImage struct {
	Location string `yaml:"location"`
	Arch     string `yaml:"arch,omitempty"`
	Digest   string `yaml:"digest,omitempty"`
}

func hostLimaArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	default:
		return ""
	}
}

// compileImageDeclaration turns the environment's pinned base image
// declaration into lima configuration: a template reference for the template
// form, or a digest-verified images entry for the URL form. Invalid or empty
// declarations fail closed — the backend never substitutes a different image
// for the pinned one.
func compileImageDeclaration(ref string) ([]string, []limaImage, error) {
	decl, err := environment.ParseImageDeclaration(ref)
	if err != nil {
		return nil, nil, fmt.Errorf("base image declaration is not usable: %w", err)
	}
	if decl.Form == environment.ImageFormURL {
		return nil, []limaImage{{
			Location: decl.Location,
			Arch:     hostLimaArch(),
			Digest:   "sha256:" + decl.Digest,
		}}, nil
	}
	return []string{decl.Ref}, nil, nil
}

// StaticRunMounts exists only for dedicated, workspace-bound, and disposable
// machines. Shared activation must pass nil so selected project facts cannot
// enter the retained Lima configuration.
type StaticRunMounts struct {
	Workspace  backend.WorkspaceAttachmentSpec
	SessionDir string
}

// ConfigForMachineSpec validates the pinned image declaration and builds the
// Lima machine configuration. Workspace authority is a distinct input that is
// forbidden for shared activation.
func ConfigForMachineSpec(spec backend.MachineActivationSpec, static *StaticRunMounts) (limaConfig, error) {
	if err := spec.Validate(); err != nil {
		return limaConfig{}, err
	}
	identityRoot := spec.IdentityRoot
	if identityRoot == "" {
		identityRoot = spec.ProfileDir
	}
	base, images, err := compileImageDeclaration(spec.ImageRef)
	if err != nil {
		return limaConfig{}, err
	}
	mounts := identityStateMounts(identityRoot)
	switch spec.Mode {
	case environment.ModeShared:
		if static != nil {
			return limaConfig{}, errors.New("shared machine configuration rejects static workspace mounts")
		}
		mounts = append(mounts, mount{Location: spec.RuntimeRoot, MountPoint: GuestRuntimeDir, Writable: true})
	case environment.ModeDedicated, environment.ModeWorkspaceBound:
		if static == nil {
			return limaConfig{}, errors.New("static machine configuration requires an exact workspace mapping")
		}
		if err := static.Workspace.Validate(spec.Mode); err != nil {
			return limaConfig{}, err
		}
		if !filepath.IsAbs(static.SessionDir) {
			return limaConfig{}, errors.New("static machine session directory must be absolute")
		}
		mounts = append([]mount{{
			Location: static.Workspace.HostRoot, MountPoint: static.Workspace.GuestRoot, Writable: true,
		}}, mounts...)
		mounts = append(mounts, sessionStateMounts(static.SessionDir, spec.RuntimeRoot, spec.EnvironmentID != "")...)
	}
	return limaConfig{
		Base:         base,
		Images:       images,
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
		Mounts: mounts,
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
		Provision: []provision{
			{
				Mode: "system",
				Script: strings.TrimSpace(fmt.Sprintf(`#!/bin/sh
set -eu
target_user=%s
	mkdir -p /hideout/profile/home /hideout/profile/config /hideout/profile/cache /hideout/profile/data /hideout/profile/browser /hideout/profile/machine /hideout/session/tmp /hideout/session/shims /hideout/session/network /hideout/session/bootstrap /hideout/hostfs
chown %s:%s /hideout /hideout/hostfs /hideout/session/tmp /hideout/session/shims /hideout/session/network /hideout/session/bootstrap 2>/dev/null || true
target_home=$(getent passwd "$target_user" 2>/dev/null | awk -F: '{print $6}' || true)
if [ -n "$target_home" ] && [ -r "$target_home/.ssh/authorized_keys" ]; then
  mkdir -p /root/.ssh
  chmod 0700 /root/.ssh
  touch /root/.ssh/authorized_keys
  cat "$target_home/.ssh/authorized_keys" >> /root/.ssh/authorized_keys
  awk '!seen[$0]++' /root/.ssh/authorized_keys > /root/.ssh/authorized_keys.tmp
  mv /root/.ssh/authorized_keys.tmp /root/.ssh/authorized_keys
  chmod 0600 /root/.ssh/authorized_keys
fi
mkdir -p /etc/ssh/sshd_config.d 2>/dev/null || true
if [ -d /etc/ssh/sshd_config.d ]; then
  printf 'PermitRootLogin prohibit-password\nPubkeyAuthentication yes\n' > /etc/ssh/sshd_config.d/99-hideout-root-control.conf
  systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || service ssh reload 2>/dev/null || service sshd reload 2>/dev/null || true
fi
if command -v gpasswd >/dev/null 2>&1; then
  gpasswd -d "$target_user" sudo 2>/dev/null || true
  gpasswd -d "$target_user" wheel 2>/dev/null || true
fi
if command -v deluser >/dev/null 2>&1; then
  deluser "$target_user" sudo 2>/dev/null || true
fi
if [ -d /etc/sudoers.d ]; then
  printf '%%s ALL=(ALL:ALL) !ALL\n' "$target_user" > /etc/sudoers.d/99-hideout-target-no-sudo
  chmod 0440 /etc/sudoers.d/99-hideout-target-no-sudo
fi
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
	`, shellQuote(spec.Profile.Identity.User), shellQuote(spec.Profile.Identity.User), shellQuote(spec.Profile.Identity.User), shellQuote(spec.Profile.Identity.Hostname), shellQuote(spec.Profile.Identity.Hostname))) + "\n",
			},
		},
		Message: "Hideout managed Lima instance. Do not mount the real host home.",
	}, nil
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

func sessionStateMounts(sessionDir, runtimeRoot string, reusableEnvironment bool) []mount {
	if reusableEnvironment {
		if runtimeRoot != "" {
			return []mount{{
				Location:   runtimeRoot,
				MountPoint: GuestRuntimeDir,
				Writable:   true,
			}}
		}
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

func WriteBootstrap(path string, commandProxyShims []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	script := BootstrapScript(commandProxyShims)
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

func BootstrapScript(commandProxyShims []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -eu\n")
	b.WriteString("mkdir -p /hideout/profile/home/.local/bin /hideout/profile/config /hideout/profile/cache /hideout/profile/data /hideout/profile/browser /hideout/session/tmp /hideout/session/shims /hideout/session/network /hideout/session/bootstrap\n")
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
  fusermount3 -u /hideout/hostfs 2>/dev/null || fusermount -u /hideout/hostfs 2>/dev/null || umount /hideout/hostfs 2>/dev/null || true
fi
mkdir -p /hideout/session/tmp 2>/dev/null || true
mkdir -p /hideout/hostfs 2>/dev/null || {
  echo 'hideout: setup identity cannot create /hideout/hostfs' >&2
  exit 70
}
for root in Users Volumes private; do
  if [ ! -e "/$root" ]; then
    ln -s "/hideout/hostfs/$root" "/$root" 2>/dev/null || true
  fi
done
`)
	for _, graft := range grafts {
		graft = filepath.Clean(graft)
		if graft == "." || graft == "/" {
			continue
		}
		fmt.Fprintf(&b, `if [ ! -e %s ] && [ ! -L %s ]; then
  mkdir -p "$(dirname %s)" 2>/dev/null || true
  ln -s %s %s 2>/dev/null || true
fi
`, shellQuote(graft), shellQuote(graft), shellQuote(graft), shellQuote("/hideout/hostfs"+graft), shellQuote(graft))
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
nohup env \
  HIDEOUT_BROKER_ENDPOINT="$HIDEOUT_BROKER_ENDPOINT" \
  HIDEOUT_SESSION_ID="$HIDEOUT_SESSION_ID" \
  HIDEOUT_CAPABILITY_TOKEN="$HIDEOUT_CAPABILITY_TOKEN" \
  /hideout/session/shims/hideout-hostfsd --mount /hideout/hostfs > /hideout/session/tmp/hostfsd.log 2>&1 &
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
  fusermount3 -u /hideout/hostfs 2>/dev/null || fusermount -u /hideout/hostfs 2>/dev/null || umount /hideout/hostfs 2>/dev/null || true
fi
if [ -f /hideout/session/tmp/hostfsd.pid ]; then
  kill "$(cat /hideout/session/tmp/hostfsd.pid)" 2>/dev/null || true
fi
rm -f /hideout/session/tmp/hostfsd.pid
`
}

func ShellArgs(instance, workdir string, env []string, command []string) []string {
	args := []string{"shell", "--tty=false", "--workdir", workdir, instance, "--", "env", "-i"}
	args = append(args, env...)
	args = append(args, command...)
	return args
}

func CommandCheck(command string) []string {
	return []string{"sh", "-c", "command -v \"$1\" >/dev/null 2>&1 || exit 127", "hideout-command-check", command}
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
			if filepath.Base(value) == "gitconfig" && filepath.Base(filepath.Dir(value)) == "identity" {
				value = GuestSessionDir + "/identity/gitconfig"
			} else {
				value = GuestProfileDir + "/home/.gitconfig"
			}
		case "PATH":
			value = GuestSessionDir + "/shims:" + GuestProfileDir + "/home/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
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

func (b Backend) controlStdout() io.Writer {
	if b.ControlStdout != nil {
		return b.ControlStdout
	}
	return b.stdout()
}

func (b Backend) controlStderr() io.Writer {
	if b.ControlStderr != nil {
		return b.ControlStderr
	}
	return b.stderr()
}
