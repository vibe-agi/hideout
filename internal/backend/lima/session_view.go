package lima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/privilege"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

const (
	sessionViewProbeTimeout = 10 * time.Second
	sessionGuardianWait     = 2 * time.Second
	sessionGuardianRoot     = "/run/hideout/session-guardians"
	sessionGuardianBeat     = 250 * time.Millisecond
)

var (
	sessionViewIDPattern   = regexp.MustCompile(`^ses_[A-Za-z0-9_]+$`)
	sessionViewUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	sessionViewEnvPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	sessionViewBootPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type SessionViewSpec struct {
	SessionID            string
	TargetUser           string
	GuestWork            string
	Env                  []string
	Command              []string
	RunBootstrap         bool
	NetworkBootstrapPath string
	NetworkCleanupPath   string
	HostFSEnabled        bool
	HostFSGrafts         []string
	ExpectedBootID       string
	GuardianControl      bool
}

func BuildSessionViewCommand(spec SessionViewSpec) ([]string, error) {
	if !sessionViewIDPattern.MatchString(spec.SessionID) || strings.ContainsAny(spec.SessionID, `/\`) {
		return nil, fmt.Errorf("invalid session id %q", spec.SessionID)
	}
	if !sessionViewUserPattern.MatchString(spec.TargetUser) || spec.TargetUser == "root" {
		return nil, fmt.Errorf("invalid non-root target user %q", spec.TargetUser)
	}
	if !path.IsAbs(spec.GuestWork) || path.Clean(spec.GuestWork) != spec.GuestWork || strings.ContainsRune(spec.GuestWork, 0) {
		return nil, fmt.Errorf("invalid guest workdir %q", spec.GuestWork)
	}
	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return nil, errors.New("session view target command is required")
	}
	for _, value := range spec.Env {
		if !sessionViewEnvPattern.MatchString(value) || strings.ContainsRune(value, 0) {
			return nil, errors.New("session view environment contains an invalid assignment")
		}
	}
	for _, arg := range spec.Command {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("session view argv contains NUL")
		}
	}
	if spec.ExpectedBootID != "" && !sessionViewBootPattern.MatchString(spec.ExpectedBootID) {
		return nil, errors.New("session view expected boot identity is invalid")
	}
	source := GuestRuntimeDir + "/sessions/" + spec.SessionID
	for _, scriptPath := range []string{spec.NetworkBootstrapPath, spec.NetworkCleanupPath} {
		if scriptPath != "" && (!path.IsAbs(scriptPath) || !strings.HasPrefix(path.Clean(scriptPath), GuestSessionDir+"/network/")) {
			return nil, fmt.Errorf("network script path %q is outside the session view", scriptPath)
		}
	}

	var script strings.Builder
	script.WriteString("set -eu\n")
	if spec.ExpectedBootID != "" {
		fmt.Fprintf(&script, "test \"$(cat /proc/sys/kernel/random/boot_id)\" = %s || { echo 'hideout: guest boot identity changed' >&2; exit 125; }\n", shellQuote(spec.ExpectedBootID))
	}
	script.WriteString("mount --make-rprivate /\n")
	script.WriteString("mount --bind \"$1\" /hideout/session\n")
	script.WriteString("mount --make-private /hideout/session\n")
	script.WriteString("mount -t tmpfs -o mode=000,size=4096 hideout-runtime-private /hideout/runtime\n")
	script.WriteString("shift\n")
	for _, assignment := range sessionViewSetupEnv(spec.Env) {
		fmt.Fprintf(&script, "export %s\n", shellQuote(assignment))
	}
	if spec.NetworkBootstrapPath != "" {
		fmt.Fprintf(&script, "%s\n", shellJoin([]string{spec.NetworkBootstrapPath}))
	}
	if spec.RunBootstrap {
		fmt.Fprintf(&script, "%s\n", shellJoin([]string{GuestBootstrapPath}))
	}
	if spec.HostFSEnabled {
		fmt.Fprintf(&script, "sh -c %s\n", shellQuote(HostFSStartScript(spec.HostFSGrafts)))
	}
	script.WriteString("cleanup_session_view() {\n")
	script.WriteString(":\n")
	if spec.HostFSEnabled {
		script.WriteString(HostFSCleanupScript())
	}
	if spec.NetworkCleanupPath != "" {
		fmt.Fprintf(&script, "%s || true\n", shellJoin([]string{spec.NetworkCleanupPath}))
	}
	script.WriteString("}\n")
	script.WriteString("trap cleanup_session_view EXIT HUP INT TERM\n")
	fmt.Fprintf(&script, "cd %s\n", shellQuote(spec.GuestWork))
	script.WriteString("set +e\n")
	target := []string{"setpriv", "--reuid=" + spec.TargetUser, "--regid=" + spec.TargetUser, "--init-groups", "--", "env", "-i"}
	target = append(target, spec.Env...)
	target = append(target, spec.Command...)
	fmt.Fprintf(&script, "%s\n", shellJoin(target))
	script.WriteString("status=$?\nset -e\ncleanup_session_view\ntrap - EXIT HUP INT TERM\nexit \"$status\"\n")

	viewCommand := []string{
		"unshare", "--mount", "--pid", "--fork", "--kill-child=KILL", "--mount-proc=/proc", "--",
		"sh", "-c", script.String(), "hideout-session-view", source,
	}
	if !spec.GuardianControl {
		return viewCommand, nil
	}
	control := sessionGuardianControlPath(spec.SessionID)
	launcher := `set -eu
control=$1
source=$2
shift 2
umask 077
mkdir -p "${control%/*}"
chmod 0700 "${control%/*}"
tmp="${control}.tmp.$$"
trap 'rm -f "$tmp"' EXIT HUP INT TERM
start_time=$(awk '{print $22}' "/proc/$$/stat")
printf '%s %s\n' "$$" "$start_time" >"$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$control"
trap - EXIT HUP INT TERM
exec "$@"
`
	command := []string{"sh", "-c", launcher, "hideout-session-launcher", control, source}
	return append(command, viewCommand...), nil
}

func sessionGuardianControlPath(sessionID string) string {
	return sessionGuardianRoot + "/" + sessionID + ".pid"
}

func sessionViewSetupEnv(env []string) []string {
	allowed := map[string]bool{
		"HIDEOUT_BROKER_ENDPOINT":  true,
		"HIDEOUT_SESSION_ID":       true,
		"HIDEOUT_CAPABILITY_TOKEN": true,
	}
	values := make(map[string]string, len(allowed))
	for _, assignment := range env {
		name, _, ok := strings.Cut(assignment, "=")
		if ok && allowed[name] {
			values[name] = assignment
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, values[name])
	}
	return out
}

func SessionViewPrimitiveProbeCommand() []string {
	return []string{"sh", "-c", `set -eu
for command in bash unshare mount setpriv; do
  command -v "$command" >/dev/null 2>&1 || { echo "hideout: required session-view primitive missing: $command" >&2; exit 127; }
done
unshare --mount --pid --fork --kill-child=KILL --mount-proc=/proc -- sh -c 'test "$(id -u)" -eq 0 && test -r /proc/self/status'
cat /proc/sys/kernel/random/boot_id
`}
}

func SessionGuardianCommand(sessionID string) ([]string, error) {
	if !sessionViewIDPattern.MatchString(sessionID) || strings.ContainsAny(sessionID, `/\`) {
		return nil, fmt.Errorf("invalid session guardian id %q", sessionID)
	}
	control := sessionGuardianControlPath(sessionID)
	source := GuestRuntimeDir + "/sessions/" + sessionID
	script := `set -eu
trap '' HUP INT TERM
control=$1
source=$2
while true; do
  outcome=
  if ! IFS= read -r -t 2 outcome; then
    break
  fi
  case "$outcome" in
    ping) ;;
    done) rm -f "$control"; exit 0 ;;
    *) echo 'hideout: invalid session guardian heartbeat' >&2; exit 1 ;;
  esac
done
attempt=0
while [ "$attempt" -lt 100 ] && [ ! -s "$control" ]; do
  attempt=$((attempt + 1))
  sleep 0.01
done
[ -s "$control" ] || exit 0
pid=
start_time=
extra=
IFS=' ' read -r pid start_time extra <"$control" || true
case "$pid" in ''|*[!0-9]*) echo 'hideout: invalid session guardian process identity' >&2; exit 1 ;; esac
case "$start_time" in ''|*[!0-9]*) echo 'hideout: invalid session guardian process identity' >&2; exit 1 ;; esac
[ -z "$extra" ] || { echo 'hideout: invalid session guardian process identity' >&2; exit 1; }
if [ ! -r "/proc/$pid/stat" ]; then
  rm -f "$control"
  exit 0
fi
current_start=$(awk '{print $22}' "/proc/$pid/stat")
[ "$current_start" = "$start_time" ] || { echo 'hideout: session guardian process identity changed' >&2; exit 1; }
tr '\000' '\n' 2>/dev/null <"/proc/$pid/cmdline" | grep -Fqx "$source" || {
  echo 'hideout: session guardian command identity changed' >&2
  exit 1
}
kill -KILL "$pid" 2>/dev/null || true
attempt=0
while [ "$attempt" -lt 100 ] && [ -e "/proc/$pid" ]; do
  state=$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true)
  [ "$state" = Z ] && break
  attempt=$((attempt + 1))
  sleep 0.01
done
if [ -e "/proc/$pid" ]; then
  state=$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true)
  [ "$state" = Z ] || { echo 'hideout: session guardian could not terminate the namespace parent' >&2; exit 1; }
fi
rm -f "$control"
`
	return []string{"bash", "-c", script, "hideout-session-guardian", control, source}, nil
}

func (b Backend) verifyIsolatedSessionTerminated(ctx context.Context, session *backend.Session) error {
	command, err := sessionTerminationProofCommand(session)
	if err != nil {
		return err
	}
	return b.runSetupCleanup(
		ctx,
		session,
		"session-view",
		"/",
		[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		command,
	)
}

func sessionTerminationProofCommand(session *backend.Session) ([]string, error) {
	if session == nil || !sessionViewIDPattern.MatchString(session.ID) {
		return nil, errors.New("isolated cleanup requires a valid session identity")
	}
	script := `set -eu
session_id=$1
attempt=0
while [ "$attempt" -lt 50 ]; do
  found=0
  for env_file in /proc/[0-9]*/environ; do
    [ -r "$env_file" ] || continue
    if tr '\000' '\n' 2>/dev/null <"$env_file" | grep -Fqx "HIDEOUT_SESSION_ID=$session_id"; then
      found=1
      break
    fi
  done
  [ "$found" -eq 0 ] && exit 0
  attempt=$((attempt + 1))
  sleep 0.1
done
echo 'hideout: isolated target process tree remains after session exit' >&2
exit 1
`
	return []string{"sh", "-c", script, "hideout-session-cleanup", session.ID}, nil
}

func (b Backend) proveIsolatedSessionTerminatedWithClient(ctx context.Context, session *backend.Session, client *ssh.Client) error {
	command, err := sessionTerminationProofCommand(session)
	if err != nil {
		return err
	}
	if err := b.runSSHClientCommand(ctx, client, command, nil, b.controlStdout(), b.controlStderr()); err != nil {
		return fmt.Errorf("session-view cleanup: %w", err)
	}
	return nil
}

func (b Backend) checkSessionViewPrimitives(ctx context.Context, session *backend.Session) error {
	_, err := b.sessionViewActivationProbe(ctx, session)
	return err
}

// ProbeSessionIsolation performs the same authenticated root-control primitive
// probe used by activation, without starting a target or mutating policy.
func (b Backend) ProbeSessionIsolation(ctx context.Context, instanceName string) error {
	return b.checkSessionViewPrimitives(ctx, &backend.Session{InstanceName: instanceName})
}

func (b Backend) sessionViewActivationProbe(ctx context.Context, session *backend.Session) (string, error) {
	if session == nil || strings.TrimSpace(session.InstanceName) == "" {
		return "", errors.New("session-view prerequisite check requires a Lima instance")
	}
	probeCtx, cancel := context.WithTimeout(ctx, sessionViewProbeTimeout)
	defer cancel()
	capture := &boundedRuntimeCapture{limit: 256}
	err := b.setupRunner().Run(
		probeCtx,
		session.InstanceName,
		"/",
		[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		SessionViewPrimitiveProbeCommand(),
		nil,
		capture,
		b.controlStderr(),
	)
	if err != nil {
		return "", fmt.Errorf("session isolation primitives unavailable: %w", err)
	}
	bootID := strings.TrimSpace(capture.String())
	if capture.truncated || !sessionViewBootPattern.MatchString(bootID) {
		return "", errors.New("session isolation activation probe returned an invalid boot identity")
	}
	return bootID, nil
}

func (b Backend) runIsolatedSession(ctx context.Context, session *backend.Session, env, command []string) error {
	targetUser := session.TargetUser
	if targetUser == "" {
		targetUser = "developer"
	}
	checkCommand, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID:      session.ID,
		TargetUser:     targetUser,
		GuestWork:      session.GuestWork,
		Env:            env,
		Command:        CommandCheck(command[0]),
		ExpectedBootID: session.ExpectedBootID,
	})
	if err != nil {
		return err
	}
	runPreflight := func() error {
		return b.setupRunner().Run(
			ctx,
			session.InstanceName,
			"/",
			[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			checkCommand,
			nil,
			b.controlStdout(),
			b.controlStderr(),
		)
	}
	if b.SetupRunner != nil {
		if err := runPreflight(); err != nil {
			return isolatedCommandPreflightError(b, session, command, env, err)
		}
	}
	viewCommand, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID:            session.ID,
		TargetUser:           targetUser,
		GuestWork:            session.GuestWork,
		Env:                  env,
		Command:              command,
		RunBootstrap:         true,
		NetworkBootstrapPath: session.NetworkBootstrapGuestPath,
		NetworkCleanupPath:   session.NetworkCleanupGuestPath,
		HostFSEnabled:        session.HostFSEnabled,
		HostFSGrafts:         session.HostFSGrafts,
		ExpectedBootID:       session.ExpectedBootID,
		GuardianControl:      true,
	})
	if err != nil {
		return err
	}
	if b.SetupRunner != nil {
		return b.setupRunner().Run(
			ctx,
			session.InstanceName,
			"/",
			[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			viewCommand,
			b.stdin(),
			b.stdout(),
			b.stderr(),
		)
	}

	client, err := b.newSSHClientForUser(ctx, session.InstanceName, "root")
	if err != nil {
		return err
	}
	defer client.Close()
	if err := b.runSSHClientCommand(ctx, client, checkCommand, nil, b.controlStdout(), b.controlStderr()); err != nil {
		return isolatedCommandPreflightError(b, session, command, env, err)
	}
	var runErr error
	if b.canBridgeTerminal() {
		runErr = b.runIsolatedPTYWithClient(ctx, session, client, viewCommand)
	} else {
		runErr = b.runGuardedSSHClientCommand(ctx, session, client, viewCommand, b.stdin(), b.stdout(), b.stderr())
	}
	if ctx.Err() != nil {
		return runErr
	}
	proofCtx, cancel := context.WithTimeout(context.Background(), sessionViewProbeTimeout)
	defer cancel()
	if err := b.proveIsolatedSessionTerminatedWithClient(proofCtx, session, client); err != nil {
		return errors.Join(runErr, err)
	}
	session.IsolationCleanupProved = true
	setup := rootControlSSHSetupIdentity()
	setup.Proof = "existing authenticated root SSH transport proved the exact session process tree absent"
	eventErr := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
		Action: privilege.ActionPrivilegedCleanup, Category: "session-view", Status: "succeeded",
		Setup: setup, Reason: "session-view cleanup proved through the owning authenticated transport",
	})
	return errors.Join(runErr, eventErr)
}

func isolatedCommandPreflightError(b Backend, session *backend.Session, command, env []string, err error) error {
	var status interface{ ExitStatus() int }
	if errors.As(err, &status) && status.ExitStatus() == 127 {
		return backend.CommandNotFoundError{
			Backend: b.Name(), Command: command[0], Path: backend.EnvValue(env, "PATH"), Workspace: session.GuestWork,
			Hint: "install the command in the guest environment; no host or sibling-session fallback was attempted",
		}
	}
	return fmt.Errorf("session isolation command preflight failed: %w", err)
}

func (b Backend) runSSHClientCommand(ctx context.Context, client *ssh.Client, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open isolated ssh session: %w", err)
	}
	defer session.Close()
	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stderr
	return runCancelableSSHCommand(ctx, session, client, setupShellCommand("/", []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, command))
}

type sshSessionGuardian struct {
	session    *ssh.Session
	heartbeat  *sessionGuardianHeartbeat
	finishOnce sync.Once
	wait       <-chan error
}

type sessionGuardianHeartbeat struct {
	ticker  *time.Ticker
	finish  chan bool
	pending []byte
	eof     bool
}

func (r *sessionGuardianHeartbeat) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.eof {
		return 0, io.EOF
	}
	select {
	case normal := <-r.finish:
		r.ticker.Stop()
		r.eof = true
		if !normal {
			return 0, io.EOF
		}
		r.pending = []byte("done\n")
	case <-r.ticker.C:
		r.pending = []byte("ping\n")
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func startSSHSessionGuardian(client *ssh.Client, sessionID string, stderr io.Writer) (*sshSessionGuardian, error) {
	command, err := SessionGuardianCommand(sessionID)
	if err != nil {
		return nil, err
	}
	remote, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open isolated session guardian: %w", err)
	}
	heartbeat := &sessionGuardianHeartbeat{ticker: time.NewTicker(sessionGuardianBeat), finish: make(chan bool, 1)}
	remote.Stdin = heartbeat
	remote.Stdout = io.Discard
	remote.Stderr = stderr
	if err := remote.Start(setupShellCommand("/", []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, command)); err != nil {
		heartbeat.ticker.Stop()
		_ = remote.Close()
		return nil, fmt.Errorf("start isolated session guardian: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- remote.Wait() }()
	return &sshSessionGuardian{session: remote, heartbeat: heartbeat, wait: wait}, nil
}

func (g *sshSessionGuardian) finish(normal bool) error {
	if g == nil {
		return errors.New("isolated session guardian is nil")
	}
	signaled := false
	g.finishOnce.Do(func() {
		g.heartbeat.finish <- normal
		signaled = true
	})
	if !signaled {
		return errors.New("isolated session guardian was already finished")
	}
	select {
	case waitErr := <-g.wait:
		_ = g.session.Close()
		return waitErr
	case <-time.After(sessionGuardianWait):
		_ = g.session.Close()
		return errors.New("isolated session guardian did not terminate")
	}
}

func waitGuardedSSHSession(ctx context.Context, client *ssh.Client, target *ssh.Session, guardian *sshSessionGuardian) error {
	targetWait := make(chan error, 1)
	go func() { targetWait <- target.Wait() }()
	select {
	case targetErr := <-targetWait:
		return errors.Join(ctx.Err(), targetErr, guardian.finish(true))
	case guardianErr := <-guardian.wait:
		guardian.finishOnce.Do(func() { guardian.heartbeat.finish <- false })
		_ = target.Signal(ssh.SIGKILL)
		_ = client.Close()
		<-targetWait
		if guardianErr == nil {
			guardianErr = errors.New("session guardian exited before target")
		}
		return fmt.Errorf("isolated session guardian failed: %w", guardianErr)
	case <-ctx.Done():
		_ = target.Signal(ssh.SIGTERM)
		guardianErr := guardian.finish(false)
		_ = client.Close()
		select {
		case <-targetWait:
		case <-time.After(sessionGuardianWait):
		}
		return errors.Join(ctx.Err(), guardianErr)
	}
}

func (b Backend) runGuardedSSHClientCommand(ctx context.Context, prepared *backend.Session, client *ssh.Client, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	guardian, err := startSSHSessionGuardian(client, prepared.ID, b.controlStderr())
	if err != nil {
		return err
	}
	target, err := client.NewSession()
	if err != nil {
		return errors.Join(fmt.Errorf("open isolated ssh session: %w", err), guardian.finish(true))
	}
	defer target.Close()
	target.Stdin = stdin
	target.Stdout = stdout
	target.Stderr = stderr
	if err := target.Start(setupShellCommand("/", []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, command)); err != nil {
		return errors.Join(err, guardian.finish(true))
	}
	return waitGuardedSSHSession(ctx, client, target, guardian)
}

func (b Backend) runIsolatedPTYWithClient(ctx context.Context, prepared *backend.Session, client *ssh.Client, command []string) (retErr error) {
	stdin, _ := b.stdin().(*os.File)
	stdout, _ := b.stdout().(*os.File)
	if stdin == nil || stdout == nil {
		return errors.New("isolated terminal requires file-backed stdin and stdout")
	}
	guardian, err := startSSHSessionGuardian(client, prepared.ID, b.controlStderr())
	if err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		return errors.Join(fmt.Errorf("open isolated terminal ssh session: %w", err), guardian.finish(true))
	}
	defer session.Close()
	width, height := initialTerminalDimensions(int(stdout.Fd()), term.GetSize)
	if err := session.RequestPty("xterm-256color", height, width, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		return errors.Join(fmt.Errorf("request isolated terminal: %w", err), guardian.finish(true))
	}
	state, err := term.MakeRaw(int(stdin.Fd()))
	if err != nil {
		return errors.Join(err, guardian.finish(true))
	}
	defer func() { retErr = errors.Join(retErr, term.Restore(int(stdin.Fd()), state)) }()
	session.Stdin = stdin
	session.Stdout = nonTerminalWriter{Writer: b.stdout()}
	session.Stderr = nonTerminalWriter{Writer: b.stderr()}
	if err := session.Start(setupShellCommand("/", []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, command)); err != nil {
		return errors.Join(err, guardian.finish(true))
	}
	return waitGuardedSSHSession(ctx, client, session, guardian)
}

func initialTerminalDimensions(fd int, getSize func(int) (int, int, error)) (int, int) {
	width, height, err := getSize(fd)
	if err != nil || width <= 0 || height <= 0 {
		return 80, 24
	}
	return width, height
}

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}
