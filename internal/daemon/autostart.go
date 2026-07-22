package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	// InternalDaemonServeCommand is the hidden process role entered by the installed
	// hideout executable. It is not a second distributable or a user-facing command.
	InternalDaemonServeCommand = "__daemon-serve"

	autostartLockName = "daemon-autostart.lock"
	autostartLogName  = "daemon-autostart.log"

	defaultAutostartTimeout = 10 * time.Second
	defaultAutostartPoll    = 25 * time.Millisecond
	maxStartupDiagnostic    = 8 * 1024
)

// DaemonStartRequest is passed to an injected detached-process starter. Output
// points at an operator-private 0600 file and must not be retained by an injected
// starter after Start returns.
type DaemonStartRequest struct {
	Executable string
	Args       []string
	Env        []string
	Output     io.Writer
}

// DaemonStarter starts a detached daemon process and returns once process creation
// has either succeeded or failed. Authenticated readiness is checked separately.
type DaemonStarter func(DaemonStartRequest) error

// DaemonReadinessProbe performs an authenticated status read. Socket existence or
// process existence alone is never readiness.
type DaemonReadinessProbe func(context.Context, string) (Status, error)

// EnsureStartedOptions configures race-safe daemon auto-start. Starter and Probe
// are injectable so concurrency, failure, and timeout behavior can be tested
// without spawning an embedded daemon.
type EnsureStartedOptions struct {
	Store        profile.Store
	Executable   string
	BuildID      string
	Timeout      time.Duration
	PollInterval time.Duration
	Starter      DaemonStarter
	Probe        DaemonReadinessProbe
	// Stopper performs the ordered shutdown used to replace an idle daemon
	// from another build. Nil means the production StopRunning path.
	Stopper     func(context.Context, string) error
	Diagnostics io.Writer
}

// InternalDaemonServeArgs returns a fresh argv for the hidden daemon process role.
func InternalDaemonServeArgs() []string {
	return []string{InternalDaemonServeCommand}
}

// EnsureStarted returns only after the daemon answers an authenticated HTTP status
// probe. Concurrent clients serialize process creation through a short-lived
// autostart lock, while daemon.lock remains the authoritative instance owner. This
// function never starts Manager/Core in-process and has no embedded fallback.
func EnsureStarted(ctx context.Context, opts EnsureStartedOptions) (Status, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Store.Root == "" {
		store, err := profile.DefaultStore()
		if err != nil {
			return Status{}, err
		}
		opts.Store = store
	}
	dir, err := ensurePlacement(opts.Store.Root)
	if err != nil {
		return Status{}, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultAutostartTimeout
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultAutostartPoll
	}
	if opts.Probe == nil {
		opts.Probe = FetchStatus
	}
	if opts.Starter == nil {
		opts.Starter = startDetachedDaemon
	}
	if opts.Executable == "" {
		opts.Executable, err = os.Executable()
		if err != nil {
			return Status{}, fmt.Errorf("daemon auto-start: resolve current executable: %w", err)
		}
	}
	opts.BuildID, err = resolveBuildID(opts.BuildID)
	if err != nil {
		return Status{}, fmt.Errorf("daemon auto-start: resolve exact build identity: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if status, err := opts.Probe(waitCtx, opts.Store.Root); err == nil && daemonStatusServing(opts.Store.Root, status) {
		if status.BuildID == opts.BuildID {
			if status.LimaHome != resolveLimaHome() {
				return Status{}, daemonLimaHomeMismatchError(status)
			}
			return status, nil
		}
		if err := replaceStaleBuildDaemon(waitCtx, opts, status); err != nil {
			return Status{}, err
		}
		// The idle previous-build daemon completed ordered shutdown; continue
		// into the normal start path below.
	}

	autostartLockPath := filepath.Join(dir, autostartLockName)
	lockFile, err := acquireLock(autostartLockPath)
	if err != nil {
		if IsAlreadyRunning(err) {
			return waitForDaemonReady(waitCtx, opts.Store.Root, opts.BuildID, opts.PollInterval, opts.Probe)
		}
		return Status{}, fmt.Errorf("daemon auto-start: acquire start lock: %w", err)
	}
	defer releaseLock(lockFile, autostartLockPath)
	if err := os.Chmod(autostartLockPath, 0o600); err != nil {
		return Status{}, fmt.Errorf("daemon auto-start: secure start lock: %w", err)
	}

	// Another process may have become ready while this client waited for the start
	// lock. Re-probe before creating a process.
	if status, err := opts.Probe(waitCtx, opts.Store.Root); err == nil && daemonStatusServing(opts.Store.Root, status) {
		if status.BuildID == opts.BuildID {
			if status.LimaHome != resolveLimaHome() {
				return Status{}, daemonLimaHomeMismatchError(status)
			}
			return status, nil
		}
		if err := replaceStaleBuildDaemon(waitCtx, opts, status); err != nil {
			return Status{}, err
		}
	}

	// A held daemon.lock means a daemon process already owns startup. Do not create a
	// competing process; wait for that owner to become authentically ready.
	instanceLockPath := filepath.Join(dir, lockName)
	instanceLock, lockErr := acquireLock(instanceLockPath)
	if lockErr != nil {
		if IsAlreadyRunning(lockErr) {
			return waitForDaemonReady(waitCtx, opts.Store.Root, opts.BuildID, opts.PollInterval, opts.Probe)
		}
		return Status{}, fmt.Errorf("daemon auto-start: inspect instance ownership: %w", lockErr)
	}
	releaseLock(instanceLock, instanceLockPath)

	output, err := openAutostartLog(dir)
	if err != nil {
		return Status{}, err
	}
	startRequest := DaemonStartRequest{
		Executable: opts.Executable,
		Args:       InternalDaemonServeArgs(),
		Env:        environmentWithStoreRoot(os.Environ(), opts.Store.Root),
		Output:     output,
	}
	startErr := opts.Starter(startRequest)
	closeErr := output.Close()
	if startErr != nil {
		emitStartupDiagnostic(opts.Diagnostics, filepath.Join(dir, autostartLogName))
		return Status{}, fmt.Errorf("daemon auto-start: create detached process: %s", audit.RedactString(startErr.Error()))
	}
	if closeErr != nil {
		return Status{}, fmt.Errorf("daemon auto-start: close startup log: %w", closeErr)
	}

	status, err := waitForDaemonReady(waitCtx, opts.Store.Root, opts.BuildID, opts.PollInterval, opts.Probe)
	if err != nil {
		emitStartupDiagnostic(opts.Diagnostics, filepath.Join(dir, autostartLogName))
	}
	return status, err
}

func waitForDaemonReady(ctx context.Context, storeRoot, buildID string, interval time.Duration, probe DaemonReadinessProbe) (Status, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		if status, err := probe(ctx, storeRoot); err == nil {
			if daemonStatusReady(storeRoot, buildID, status) {
				if status.LimaHome != resolveLimaHome() {
					return Status{}, daemonLimaHomeMismatchError(status)
				}
				return status, nil
			}
			if daemonStatusServing(storeRoot, status) && status.BuildID != buildID {
				return Status{}, daemonBuildMismatchError(status)
			} else {
				lastErr = errors.New("authenticated status did not report serving state")
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			message := "authenticated readiness timed out"
			if lastErr != nil {
				message += ": " + audit.RedactString(lastErr.Error())
			}
			return Status{}, fmt.Errorf("daemon auto-start: %s: %w", message, ctx.Err())
		case <-ticker.C:
		}
	}
}

func daemonStatusReady(storeRoot, buildID string, status Status) bool {
	return daemonStatusServing(storeRoot, status) && status.BuildID == buildID
}

func daemonStatusServing(storeRoot string, status Status) bool {
	return status.Version == statusVersion && status.State == "serving" &&
		sameSocketPath(status.Transport.Socket, socketPathFor(storeRoot)) &&
		sameSocketPath(status.Transport.SessionSocket, SessionSocketPath(storeRoot)) &&
		status.Transport.SessionProtocol == SessionProtocolVersion
}

// sameSocketPath compares the physical runtime directory while preserving the
// socket basename. This accepts platform aliases such as macOS /tmp ->
// /private/tmp without accepting a different daemon endpoint.
func sameSocketPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}
	if filepath.Base(left) != filepath.Base(right) {
		return false
	}
	leftDir, leftErr := filepath.EvalSymlinks(filepath.Dir(left))
	rightDir, rightErr := filepath.EvalSymlinks(filepath.Dir(right))
	return leftErr == nil && rightErr == nil && filepath.Clean(leftDir) == filepath.Clean(rightDir)
}

func daemonBuildMismatchError(status Status) error {
	running := strings.TrimSpace(status.BuildID)
	if running == "" {
		running = "unidentified"
	} else if len(running) > 12 {
		running = running[:12]
	}
	return fmt.Errorf("daemon auto-start: running daemon build %s differs from the installed CLI; run hideout daemon stop, then retry", running)
}

// replaceStaleBuildDaemon performs an ordered shutdown of an idle daemon from
// another build so the current CLI can start its own build in place. A daemon
// with live sessions or without a provable build identity is never stopped
// automatically; those cases keep the fail-closed mismatch error.
func replaceStaleBuildDaemon(ctx context.Context, opts EnsureStartedOptions, status Status) error {
	if strings.TrimSpace(status.BuildID) == "" || len(status.Sessions) != 0 {
		return daemonBuildMismatchError(status)
	}
	stopper := opts.Stopper
	if stopper == nil {
		stopper = StopRunning
	}
	if opts.Diagnostics != nil {
		fmt.Fprintln(opts.Diagnostics, "hideoutd build changed; replacing the idle previous-build daemon")
	}
	if err := stopper(ctx, opts.Store.Root); err != nil {
		return fmt.Errorf("%s (automatic replacement failed: %s)", daemonBuildMismatchError(status).Error(), audit.RedactString(err.Error()))
	}
	return nil
}

func startDetachedDaemon(req DaemonStartRequest) error {
	if strings.TrimSpace(req.Executable) == "" {
		return errors.New("daemon executable is required")
	}
	if strings.HasSuffix(filepath.Base(req.Executable), ".test") {
		return errors.New("daemon auto-start refuses a Go test binary; tests must inject a daemon starter")
	}
	cmd := exec.Command(req.Executable, req.Args...)
	cmd.Env = append([]string(nil), req.Env...)
	cmd.Dir = string(os.PathSeparator)
	cmd.Stdin = nil
	cmd.Stdout = req.Output
	cmd.Stderr = req.Output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap while this client remains alive. Setsid and inherited private log FDs let
	// the daemon continue independently if the starting client exits first.
	go func() { _ = cmd.Wait() }()
	return nil
}

func openAutostartLog(dir string) (*os.File, error) {
	path := filepath.Join(dir, autostartLogName)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("daemon auto-start: startup log must be a regular file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon auto-start: open private startup log: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// resolveLimaHome names the lima world this process would use: an explicit
// absolute LIMA_HOME, or the default ~/.lima. The result is normalized to a
// physical path so platform aliases (macOS /tmp -> /private/tmp) compare
// equal. Client and daemon must resolve the same world before the daemon may
// observe or control backend inventory on the client's behalf.
func resolveLimaHome() string {
	root := strings.TrimSpace(os.Getenv("LIMA_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".lima")
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if physical, err := filepath.EvalSymlinks(root); err == nil {
		root = physical
	}
	return filepath.Clean(root)
}

func daemonLimaHomeMismatchError(status Status) error {
	running := strings.TrimSpace(status.LimaHome)
	if running == "" {
		running = "an unidentified lima home"
	}
	return fmt.Errorf("daemon lima home mismatch: the running daemon resolved %s while this invocation resolves %s; run hideout daemon stop, then retry", running, resolveLimaHome())
}

func environmentWithStoreRoot(env []string, storeRoot string) []string {
	const key = "HIDEOUT_STORE_ROOT="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, key) {
			out = append(out, item)
		}
	}
	return append(out, key+storeRoot)
}

func emitStartupDiagnostic(dst io.Writer, path string) {
	if dst == nil {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	start := info.Size() - maxStartupDiagnostic
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStartupDiagnostic))
	if err != nil {
		return
	}
	text := strings.TrimSpace(audit.RedactString(string(data)))
	if text != "" {
		_, _ = fmt.Fprintf(dst, "hideoutd startup: %s\n", text)
	}
}
