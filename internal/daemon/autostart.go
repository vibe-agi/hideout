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
	Timeout      time.Duration
	PollInterval time.Duration
	Starter      DaemonStarter
	Probe        DaemonReadinessProbe
	Diagnostics  io.Writer
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

	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if status, ok := probeReady(waitCtx, opts.Store.Root, opts.Probe); ok {
		return status, nil
	}

	autostartLockPath := filepath.Join(dir, autostartLockName)
	lockFile, err := acquireLock(autostartLockPath)
	if err != nil {
		if IsAlreadyRunning(err) {
			return waitForDaemonReady(waitCtx, opts.Store.Root, opts.PollInterval, opts.Probe)
		}
		return Status{}, fmt.Errorf("daemon auto-start: acquire start lock: %w", err)
	}
	defer releaseLock(lockFile, autostartLockPath)
	if err := os.Chmod(autostartLockPath, 0o600); err != nil {
		return Status{}, fmt.Errorf("daemon auto-start: secure start lock: %w", err)
	}

	// Another process may have become ready while this client waited for the start
	// lock. Re-probe before creating a process.
	if status, ok := probeReady(waitCtx, opts.Store.Root, opts.Probe); ok {
		return status, nil
	}

	// A held daemon.lock means a daemon process already owns startup. Do not create a
	// competing process; wait for that owner to become authentically ready.
	instanceLockPath := filepath.Join(dir, lockName)
	instanceLock, lockErr := acquireLock(instanceLockPath)
	if lockErr != nil {
		if IsAlreadyRunning(lockErr) {
			return waitForDaemonReady(waitCtx, opts.Store.Root, opts.PollInterval, opts.Probe)
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

	status, err := waitForDaemonReady(waitCtx, opts.Store.Root, opts.PollInterval, opts.Probe)
	if err != nil {
		emitStartupDiagnostic(opts.Diagnostics, filepath.Join(dir, autostartLogName))
	}
	return status, err
}

func waitForDaemonReady(ctx context.Context, storeRoot string, interval time.Duration, probe DaemonReadinessProbe) (Status, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		if status, err := probe(ctx, storeRoot); err == nil {
			if daemonStatusReady(storeRoot, status) {
				return status, nil
			}
			lastErr = errors.New("authenticated status did not report serving state")
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

func probeReady(ctx context.Context, storeRoot string, probe DaemonReadinessProbe) (Status, bool) {
	status, err := probe(ctx, storeRoot)
	return status, err == nil && daemonStatusReady(storeRoot, status)
}

func daemonStatusReady(storeRoot string, status Status) bool {
	return status.Version == statusVersion && status.State == "serving" &&
		filepath.Clean(status.Transport.Socket) == filepath.Clean(socketPathFor(storeRoot)) &&
		filepath.Clean(status.Transport.SessionSocket) == filepath.Clean(SessionSocketPath(storeRoot)) &&
		status.Transport.SessionProtocol == SessionProtocolVersion
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
