//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/creack/pty"
)

const (
	outputQueueDepth     = 64
	outputChunkBytes     = 32 * 1024
	processStopGrace     = 2 * time.Second
	outputDrainTimeout   = 2 * time.Second
	supervisorHeartbeat  = 15 * time.Second
	maxTypedSummaryBytes = 512
)

type outputRecord struct {
	kind outputKind
	data []byte
}

type outputQueue struct {
	frames    chan outputRecord
	start     chan struct{}
	failed    chan error
	done      chan struct{}
	once      sync.Once
	startOnce sync.Once
	discard   bool
	errMu     sync.Mutex
	err       error
}

func newOutputQueue(wire supervisorWire) *outputQueue {
	queue := &outputQueue{
		frames: make(chan outputRecord, outputQueueDepth),
		start:  make(chan struct{}),
		failed: make(chan error, 1),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(queue.done)
		<-queue.start
		for record := range queue.frames {
			if queue.discard {
				continue
			}
			if err := wire.WriteOutput(record.kind, record.data); err != nil {
				queue.fail(err)
				return
			}
		}
	}()
	return queue
}

func (q *outputQueue) begin() {
	q.startOnce.Do(func() { close(q.start) })
}

func (q *outputQueue) discardOutput() {
	q.discard = true
	q.begin()
}

func (q *outputQueue) write(kind outputKind, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	copyOfData := append([]byte(nil), data...)
	select {
	case q.frames <- outputRecord{kind: kind, data: copyOfData}:
		return nil
	default:
		q.fail(errOutputBackpressure)
		return errOutputBackpressure
	}
}

func (q *outputQueue) fail(err error) {
	if err == nil {
		return
	}
	q.errMu.Lock()
	if q.err == nil {
		q.err = err
		select {
		case q.failed <- err:
		default:
		}
	}
	q.errMu.Unlock()
}

func (q *outputQueue) failure() <-chan error {
	return q.failed
}

func (q *outputQueue) closeAndWait() error {
	q.begin()
	q.once.Do(func() { close(q.frames) })
	select {
	case <-q.done:
		q.errMu.Lock()
		defer q.errMu.Unlock()
		return q.err
	case <-time.After(outputDrainTimeout):
		return errors.New("supervisor output did not drain within the bound")
	}
}

type queueWriter struct {
	queue *outputQueue
	kind  outputKind
}

func (w queueWriter) Write(data []byte) (int, error) {
	if err := w.queue.write(w.kind, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

type waitResult struct {
	completion targetCompletion
	err        error
	cleanupErr error
}

type targetProcess struct {
	cmd       *exec.Cmd
	pid       int
	cgroup    *sessionCgroup
	activity  *observerSession
	pty       *os.File
	stdin     io.WriteCloser
	wait      chan waitResult
	queue     *outputQueue
	stdinOnce sync.Once
	outputWG  sync.WaitGroup
}

func startTarget(spec startSpec, wire supervisorWire) (*targetProcess, error) {
	return startTargetWithSessionCgroup(spec, wire, sessionCgroupOptions{
		Root: defaultSessionCgroupRoot, SessionID: spec.SessionID,
	})
}

func startTargetWithSessionCgroup(
	spec startSpec,
	wire supervisorWire,
	cgroupOptions sessionCgroupOptions,
) (*targetProcess, error) {
	credential, err := credentialForUser(spec.TargetUser)
	if err != nil {
		return nil, err
	}
	commandPath, err := resolveTargetCommand(spec.Argv[0], spec.GuestWork, spec.Env)
	if err != nil {
		return nil, err
	}
	if cgroupOptions.Root == "" {
		cgroupOptions.Root = defaultSessionCgroupRoot
	}
	if cgroupOptions.SkipAtomicPlacementForTest &&
		cgroupOptions.Backend == nil {
		return nil, errors.New("atomic cgroup placement cannot be disabled for the OS backend")
	}
	activity, _ := spec.activityRuntime.(*observerSession)
	if spec.Activity != nil && activity == nil {
		return nil, errors.New("observed target is missing its pre-commit activity boundary")
	}
	if spec.Activity == nil && activity != nil {
		return nil, errors.New("target carries an activity boundary without start authority")
	}
	var group *sessionCgroup
	if activity != nil {
		group = activity.group
		if group == nil || activity.binding.SessionID != spec.SessionID ||
			group.ID() != activity.binding.CgroupID {
			return nil, errors.New("prepared target activity boundary identity changed")
		}
	} else {
		cgroupOptions.SessionID = spec.SessionID
		group, err = newSessionCgroup(cgroupOptions)
		if err != nil {
			return nil, fmt.Errorf("create target session cgroup: %w", err)
		}
	}
	cleanupUnstarted := func() {
		if activity != nil {
			_ = activity.Abort(observerShutdownWait)
			return
		}
		_ = group.ProveEmptyAndRemove()
		_ = group.Close()
	}
	cmd := &exec.Cmd{
		Path: commandPath,
		Args: append([]string(nil), spec.Argv...),
		Dir:  spec.GuestWork,
		Env:  append([]string(nil), spec.Env...),
	}
	cmd.WaitDelay = processStopGrace
	queue := newOutputQueue(wire)
	process := &targetProcess{
		cmd: cmd, cgroup: group, activity: activity,
		queue: queue, wait: make(chan waitResult, 1),
	}

	if spec.Terminal.Mode == "pty" {
		cmd.Env = replaceEnv(cmd.Env, "TERM", spec.Terminal.Term)
		attrs := &syscall.SysProcAttr{
			Credential: credential,
			Setsid:     true,
			Setctty:    true,
		}
		if err := bindTargetSessionCgroup(
			group,
			attrs,
			cgroupOptions.SkipAtomicPlacementForTest,
		); err != nil {
			_ = queue.closeAndWait()
			cleanupUnstarted()
			return nil, fmt.Errorf("bind PTY target cgroup: %w", err)
		}
		master, err := pty.StartWithAttrs(cmd, &pty.Winsize{Rows: spec.Terminal.Rows, Cols: spec.Terminal.Columns}, attrs)
		if err != nil {
			_ = queue.closeAndWait()
			cleanupUnstarted()
			return nil, fmt.Errorf("start PTY target: %w", err)
		}
		process.pty = master
		process.stdin = master
		process.pid = cmd.Process.Pid
		process.outputWG.Add(1)
		go process.copyPTYOutput()
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: credential,
			Setpgid:    true,
		}
		if err := bindTargetSessionCgroup(
			group,
			cmd.SysProcAttr,
			cgroupOptions.SkipAtomicPlacementForTest,
		); err != nil {
			_ = queue.closeAndWait()
			cleanupUnstarted()
			return nil, fmt.Errorf("bind pipe target cgroup: %w", err)
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			_ = queue.closeAndWait()
			cleanupUnstarted()
			return nil, fmt.Errorf("open target stdin: %w", err)
		}
		cmd.Stdout = queueWriter{queue: queue, kind: outputStdout}
		cmd.Stderr = queueWriter{queue: queue, kind: outputStderr}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = queue.closeAndWait()
			cleanupUnstarted()
			return nil, fmt.Errorf("start pipe target: %w", err)
		}
		process.stdin = stdin
		process.pid = cmd.Process.Pid
	}

	go func() {
		err := cmd.Wait()
		if process.pty != nil {
			_ = process.pty.Close()
		}
		process.outputWG.Wait()
		var observerErr error
		if activity != nil {
			observerErr = activity.Stop(observerShutdownWait)
		}
		cleanupErr := errors.Join(
			observerErr,
			waitForSessionCgroupCleanup(process.cgroup, processStopGrace),
		)
		if closeErr := process.cgroup.Close(); closeErr != nil {
			cleanupErr = errors.Join(cleanupErr, closeErr)
		}
		completion := completionFromWait(err)
		completion.CleanupCompleted = cleanupErr == nil
		if activity != nil {
			completion.Activity = activity.Completion(cleanupErr)
		}
		process.wait <- waitResult{
			completion: completion, err: err,
			cleanupErr: cleanupErr,
		}
	}()
	return process, nil
}

func bindTargetSessionCgroup(
	group *sessionCgroup,
	attributes *syscall.SysProcAttr,
	skipForTest bool,
) error {
	if skipForTest {
		if group == nil || attributes == nil {
			return errSessionCgroupIdentity
		}
		return nil
	}
	return group.BindTarget(attributes)
}

func waitForSessionCgroupCleanup(
	group *sessionCgroup,
	timeout time.Duration,
) error {
	if group == nil {
		return errSessionCgroupIdentity
	}
	if timeout <= 0 {
		timeout = processStopGrace
	}
	deadline := time.Now().Add(timeout)
	for {
		err := group.ProveEmptyAndRemove()
		if err == nil {
			return nil
		}
		if !errors.Is(err, errSessionCgroupNotEmpty) ||
			!time.Now().Before(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *targetProcess) copyPTYOutput() {
	defer p.outputWG.Done()
	buffer := make([]byte, outputChunkBytes)
	for {
		n, err := p.pty.Read(buffer)
		if n > 0 {
			if writeErr := p.queue.write(outputTerminal, buffer[:n]); writeErr != nil {
				_ = p.signal(syscall.SIGTERM)
				return
			}
		}
		if err != nil {
			// Linux PTYs commonly return EIO after the slave closes.
			if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
				return
			}
			_ = p.signal(syscall.SIGTERM)
			return
		}
	}
}

func (p *targetProcess) writeStdin(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	_, err := p.stdin.Write(data)
	return err
}

func (p *targetProcess) closeInput() error {
	var closeErr error
	p.stdinOnce.Do(func() {
		if p.pty != nil {
			_, closeErr = p.pty.Write([]byte{0x04})
			return
		}
		closeErr = p.stdin.Close()
	})
	// exec.Cmd.Wait closes pipe endpoints after the target exits. A concurrent
	// daemon EOF is therefore an idempotent close, not a protocol failure.
	if errors.Is(closeErr, os.ErrClosed) {
		return nil
	}
	return closeErr
}

func (p *targetProcess) resize(rows, columns uint16) error {
	if p.pty == nil {
		return errors.New("resize is invalid for a non-PTY target")
	}
	if rows == 0 || columns == 0 {
		return errors.New("resize dimensions must be non-zero")
	}
	return pty.Setsize(p.pty, &pty.Winsize{Rows: rows, Cols: columns})
}

func (p *targetProcess) signal(signal syscall.Signal) error {
	if p.pid <= 0 {
		return errors.New("target process group is unavailable")
	}
	err := syscall.Kill(-p.pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (p *targetProcess) terminateAndWait() waitResult {
	_ = p.signal(syscall.SIGTERM)
	select {
	case result := <-p.wait:
		return result
	case <-time.After(processStopGrace):
		if p.cgroup != nil {
			_ = p.cgroup.Kill()
		}
		_ = p.signal(syscall.SIGKILL)
		select {
		case result := <-p.wait:
			return result
		case <-time.After(processStopGrace):
			cleanupErr := errors.New("target process group was not reaped within the bound")
			completion := targetCompletion{
				Kind: "protocol-error", ExitCode: 125, Completed: false,
				CleanupCompleted: false,
			}
			if p.activity != nil {
				_ = p.activity.Stop(observerShutdownWait)
				completion.Activity = p.activity.Completion(cleanupErr)
			}
			return waitResult{
				completion: completion,
				err:        cleanupErr,
				cleanupErr: cleanupErr,
			}
		}
	}
}

func (p *targetProcess) finishOutput() error {
	return p.queue.closeAndWait()
}

func credentialForUser(name string) (*syscall.Credential, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("resolve target user %q: %w", name, err)
	}
	uid, err := parseIdentity(account.Uid)
	if err != nil || uid == 0 {
		return nil, fmt.Errorf("target user %q does not resolve to a non-root uid", name)
	}
	gid, err := parseIdentity(account.Gid)
	if err != nil {
		return nil, fmt.Errorf("target user %q has an invalid gid", name)
	}
	groupIDs, err := account.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("resolve target user %q groups: %w", name, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	seen := map[uint32]struct{}{gid: {}}
	for _, raw := range groupIDs {
		group, parseErr := parseIdentity(raw)
		if parseErr != nil {
			return nil, fmt.Errorf("target user %q has an invalid supplementary gid", name)
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return &syscall.Credential{Uid: uid, Gid: gid, Groups: groups}, nil
}

func parseIdentity(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	return uint32(parsed), err
}

func resolveTargetCommand(name, workdir string, env []string) (string, error) {
	if strings.ContainsRune(name, 0) || name == "" {
		return "", errors.New("target command is invalid")
	}
	if strings.ContainsRune(name, '/') {
		candidate := name
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workdir, candidate)
		}
		return executablePath(candidate)
	}
	pathValue, ok := envValue(env, "PATH")
	if !ok || pathValue == "" {
		return "", errors.New("target PATH is required for a command name")
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = workdir
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(workdir, directory)
		}
		candidate, err := executablePath(filepath.Join(directory, name))
		if err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("target command %q was not found in PATH", name)
}

func executablePath(candidate string) (string, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("target path is not executable")
	}
	return candidate, nil
}

func envValue(env []string, name string) (string, bool) {
	for _, assignment := range env {
		key, value, ok := strings.Cut(assignment, "=")
		if ok && key == name {
			return value, true
		}
	}
	return "", false
}

func replaceEnv(env []string, name, value string) []string {
	assignment := name + "=" + value
	for index, current := range env {
		key, _, ok := strings.Cut(current, "=")
		if ok && key == name {
			env[index] = assignment
			return env
		}
	}
	return append(env, assignment)
}

func signalNumber(name string) (syscall.Signal, error) {
	normalized, err := normalizeSignal(name)
	if err != nil {
		return 0, err
	}
	signals := map[string]syscall.Signal{
		"SIGHUP":  syscall.SIGHUP,
		"SIGINT":  syscall.SIGINT,
		"SIGQUIT": syscall.SIGQUIT,
		"SIGTERM": syscall.SIGTERM,
		"SIGTSTP": syscall.SIGTSTP,
		"SIGCONT": syscall.SIGCONT,
		"SIGKILL": syscall.SIGKILL,
	}
	return signals[normalized], nil
}

func completionFromWait(err error) targetCompletion {
	if err == nil {
		return targetCompletion{Kind: "exit", ExitCode: 0, Completed: true}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return completionFromWaitStatus(status)
		}
	}
	return targetCompletion{Kind: "protocol-error", ExitCode: 125, Completed: true}
}

func completionFromWaitStatus(status syscall.WaitStatus) targetCompletion {
	if status.Signaled() {
		signal := status.Signal()
		return targetCompletion{
			Kind:      "signal",
			ExitCode:  128 + int(signal),
			Signal:    portableSignalName(signal),
			Completed: true,
		}
	}
	return targetCompletion{Kind: "exit", ExitCode: status.ExitStatus(), Completed: true}
}

func portableSignalName(signal syscall.Signal) string {
	signals := map[syscall.Signal]string{
		syscall.SIGHUP:  "SIGHUP",
		syscall.SIGINT:  "SIGINT",
		syscall.SIGQUIT: "SIGQUIT",
		syscall.SIGILL:  "SIGILL",
		syscall.SIGTRAP: "SIGTRAP",
		syscall.SIGABRT: "SIGABRT",
		syscall.SIGBUS:  "SIGBUS",
		syscall.SIGFPE:  "SIGFPE",
		syscall.SIGTERM: "SIGTERM",
		syscall.SIGUSR1: "SIGUSR1",
		syscall.SIGSEGV: "SIGSEGV",
		syscall.SIGUSR2: "SIGUSR2",
		syscall.SIGPIPE: "SIGPIPE",
		syscall.SIGALRM: "SIGALRM",
		syscall.SIGTSTP: "SIGTSTP",
		syscall.SIGCONT: "SIGCONT",
		syscall.SIGKILL: "SIGKILL",
	}
	if name, ok := signals[signal]; ok {
		return name
	}
	return fmt.Sprintf("SIG%d", int(signal))
}

func boundedSummary(err error) string {
	if err == nil {
		return ""
	}
	value := strings.ToValidUTF8(err.Error(), "?")
	var summary strings.Builder
	for _, r := range value {
		if !unicode.IsPrint(r) || r == '\u001b' {
			continue
		}
		width := utf8.RuneLen(r)
		if summary.Len()+width > maxTypedSummaryBytes {
			break
		}
		summary.WriteRune(r)
	}
	return summary.String()
}
