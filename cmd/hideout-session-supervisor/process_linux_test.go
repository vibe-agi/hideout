//go:build linux

package main

import (
	"errors"
	"io"
	"os"
	"os/user"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/sessionwire"
)

type recordingWire struct {
	mu         sync.Mutex
	outputs    map[outputKind][]byte
	written    chan struct{}
	controls   chan controlResult
	completion targetCompletion
}

func newRecordingWire() *recordingWire {
	return &recordingWire{
		outputs:  make(map[outputKind][]byte),
		written:  make(chan struct{}, 64),
		controls: make(chan controlResult, 4),
	}
}

func (*recordingWire) ReadStart() (startSpec, error) { return startSpec{}, io.EOF }
func (*recordingWire) ReadCommit() error             { return nil }
func (w *recordingWire) ReadControl() (supervisorControl, error) {
	result := <-w.controls
	return result.control, result.err
}
func (*recordingWire) WriteReady(*sessionwire.SupervisorActivityReady) error { return nil }
func (*recordingWire) WriteError(string, string) error                       { return nil }
func (w *recordingWire) WriteCompletion(completion targetCompletion) error {
	w.mu.Lock()
	w.completion = completion
	w.mu.Unlock()
	return nil
}
func (w *recordingWire) WriteOutput(kind outputKind, data []byte) error {
	w.mu.Lock()
	w.outputs[kind] = append(w.outputs[kind], data...)
	w.mu.Unlock()
	select {
	case w.written <- struct{}{}:
	default:
	}
	return nil
}

func (w *recordingWire) output(kind outputKind) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.outputs[kind])
}

func (w *recordingWire) completed() targetCompletion {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.completion
}

type commitFailureWire struct {
	spec          startSpec
	commitErr     error
	ready         bool
	readyActivity *sessionwire.SupervisorActivityReady
}

func (w *commitFailureWire) ReadStart() (startSpec, error) { return w.spec, nil }
func (w *commitFailureWire) ReadCommit() error             { return w.commitErr }
func (*commitFailureWire) ReadControl() (supervisorControl, error) {
	return supervisorControl{}, io.EOF
}
func (w *commitFailureWire) WriteReady(activity *sessionwire.SupervisorActivityReady) error {
	w.ready = true
	w.readyActivity = activity
	return nil
}
func (*commitFailureWire) WriteOutput(outputKind, []byte) error   { return nil }
func (*commitFailureWire) WriteError(string, string) error        { return nil }
func (*commitFailureWire) WriteCompletion(targetCompletion) error { return nil }

func TestSupervisorDoesNotStartTargetBeforeCommit(t *testing.T) {
	commitErr := errors.New("activation was not committed")
	sessionID := "ses_20260716T120000Z_0123456789abcdef"
	wire := &commitFailureWire{
		commitErr: commitErr,
		spec: startSpec{
			Protocol: testProtocol, SessionID: sessionID, TargetUser: "developer",
			GuestWork: "/workspace", Argv: []string{"true"}, Terminal: terminalSpec{Mode: "none"},
			ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
			SessionSource:  "/hideout/runtime/sessions/" + sessionID,
		},
	}
	started := false
	err := runSupervisorWire(
		wire,
		func(startSpec) error { return nil },
		func(startSpec, supervisorWire) (*targetProcess, error) {
			started = true
			return nil, errors.New("target starter must not run")
		},
		func(*targetProcess, supervisorWire) error {
			return errors.New("target runner must not run")
		},
	)
	if !errors.Is(err, commitErr) {
		t.Fatalf("runSupervisorWire error=%v", err)
	}
	if !wire.ready {
		t.Fatal("supervisor did not publish authenticated readiness before waiting for commit")
	}
	if started {
		t.Fatal("target starter ran before daemon commit")
	}
}

func TestPipeTargetKeepsStdoutStderrSeparateAndReaps(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "none"}, []string{"sh", "-c", "printf stdout; printf stderr >&2; exit 7"})
	wire := newRecordingWire()
	process, err := startLinuxTestTarget(t, spec, wire)
	if err != nil {
		t.Fatal(err)
	}
	process.queue.begin()
	result := <-process.wait
	if err := process.finishOutput(); err != nil {
		t.Fatal(err)
	}
	if result.completion.Kind != "exit" || result.completion.ExitCode != 7 || !result.completion.Completed {
		t.Fatalf("completion=%+v wait=%v", result.completion, result.err)
	}
	if got := wire.output(outputStdout); got != "stdout" {
		t.Fatalf("stdout=%q", got)
	}
	if got := wire.output(outputStderr); got != "stderr" {
		t.Fatalf("stderr=%q", got)
	}
	if got := wire.output(outputTerminal); got != "" {
		t.Fatalf("terminal=%q", got)
	}
}

func TestPTYTargetUsesInitialAndDynamicSize(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "pty", Rows: 24, Columns: 80, Term: "xterm-256color"}, []string{"sh", "-c", "stty size; IFS= read -r line; stty size; printf ':%s:' \"$line\""})
	wire := newRecordingWire()
	process, err := startLinuxTestTarget(t, spec, wire)
	if err != nil {
		t.Fatal(err)
	}
	process.queue.begin()
	waitForOutput(t, wire, "24 80")
	if err := process.resize(40, 100); err != nil {
		t.Fatal(err)
	}
	if err := process.writeStdin([]byte("ready\n")); err != nil {
		t.Fatal(err)
	}
	result := <-process.wait
	if err := process.finishOutput(); err != nil {
		t.Fatal(err)
	}
	if result.completion.ExitCode != 0 {
		t.Fatalf("completion=%+v wait=%v", result.completion, result.err)
	}
	output := strings.ReplaceAll(wire.output(outputTerminal), "\r", "")
	for _, want := range []string{"24 80", "40 100", ":ready:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("terminal output %q does not contain %q", output, want)
		}
	}
}

func TestTransportEOFTerminatesAndReapsProcessGroup(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "none"}, []string{"sh", "-c", "trap 'exit 0' TERM; printf ready; while :; do sleep 1; done"})
	wire := newRecordingWire()
	process, err := startLinuxTestTarget(t, spec, wire)
	if err != nil {
		t.Fatal(err)
	}
	process.queue.begin()
	waitForStreamOutput(t, wire, outputStdout, "ready")
	wire.controls <- controlResult{err: io.EOF}
	if err := superviseTarget(process, wire); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-process.pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d remains after transport EOF: %v", process.pid, err)
	}
}

func TestCancelProducesTypedCompletionAfterReaping(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "none"}, []string{"sh", "-c", "trap 'exit 0' TERM; printf ready; while :; do sleep 1; done"})
	wire := newRecordingWire()
	process, err := startLinuxTestTarget(t, spec, wire)
	if err != nil {
		t.Fatal(err)
	}
	process.queue.begin()
	waitForStreamOutput(t, wire, outputStdout, "ready")
	wire.controls <- controlResult{control: supervisorControl{Kind: controlCancel}}
	if err := superviseTarget(process, wire); err != nil {
		t.Fatal(err)
	}
	completion := wire.completed()
	if completion.Kind != "cancelled" || completion.ExitCode != 130 || !completion.Completed {
		t.Fatalf("completion=%+v", completion)
	}
}

func TestPipeStdinEOFCausesRealChildEOF(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "none"}, []string{"sh", "-c", "cat; printf eof >&2"})
	wire := newRecordingWire()
	process, err := startLinuxTestTarget(t, spec, wire)
	if err != nil {
		t.Fatal(err)
	}
	process.queue.begin()
	if err := process.writeStdin([]byte("body")); err != nil {
		t.Fatal(err)
	}
	if err := process.closeInput(); err != nil {
		t.Fatal(err)
	}
	result := <-process.wait
	if err := process.finishOutput(); err != nil {
		t.Fatal(err)
	}
	if result.completion.ExitCode != 0 || wire.output(outputStdout) != "body" || wire.output(outputStderr) != "eof" {
		t.Fatalf("completion=%+v stdout=%q stderr=%q", result.completion, wire.output(outputStdout), wire.output(outputStderr))
	}
}

func TestPipeStdinEOFAfterTargetExitIsIdempotent(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "none"}, []string{"true"})
	wire := newRecordingWire()
	process, err := startLinuxTestTarget(t, spec, wire)
	if err != nil {
		t.Fatal(err)
	}
	process.queue.begin()
	result := <-process.wait
	if result.completion.ExitCode != 0 {
		t.Fatalf("completion=%+v wait=%v", result.completion, result.err)
	}
	if err := process.closeInput(); err != nil {
		t.Fatalf("EOF after target exit: %v", err)
	}
	if err := process.finishOutput(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionPreservesExitAndSignalStatus(t *testing.T) {
	exit := completionFromWaitStatus(syscall.WaitStatus(42 << 8))
	if exit.Kind != "exit" || exit.ExitCode != 42 || !exit.Completed {
		t.Fatalf("exit=%+v", exit)
	}
	signaled := completionFromWaitStatus(syscall.WaitStatus(syscall.SIGTERM))
	if signaled.Kind != "signal" || signaled.ExitCode != 143 || signaled.Signal != "SIGTERM" || !signaled.Completed {
		t.Fatalf("signal=%+v", signaled)
	}
	segv := completionFromWaitStatus(syscall.WaitStatus(syscall.SIGSEGV))
	if segv.Signal != "SIGSEGV" {
		t.Fatalf("segv=%+v", segv)
	}
}

func TestBoundedSummaryStripsTerminalControlsAndKeepsValidUTF8(t *testing.T) {
	summary := boundedSummary(errors.New("\x1b[31msecret\x00 " + strings.Repeat("界", 400)))
	if strings.Contains(summary, "\x1b") || strings.Contains(summary, "\x00") {
		t.Fatalf("summary retained terminal controls: %q", summary)
	}
	if len(summary) > maxTypedSummaryBytes || !utf8.ValidString(summary) {
		t.Fatalf("summary length=%d valid=%v", len(summary), utf8.ValidString(summary))
	}
}

func TestOutputQueueFailsClosedInsteadOfDropping(t *testing.T) {
	wire := &blockingWire{release: make(chan struct{})}
	queue := newOutputQueue(wire)
	queue.begin()
	for index := 0; index < outputQueueDepth+2; index++ {
		err := queue.write(outputStdout, []byte("x"))
		if errors.Is(err, errOutputBackpressure) {
			close(wire.release)
			_ = queue.closeAndWait()
			return
		}
	}
	t.Fatal("output queue silently accepted data beyond its bound")
}

type blockingWire struct{ release chan struct{} }

func (*blockingWire) ReadStart() (startSpec, error)                         { return startSpec{}, io.EOF }
func (*blockingWire) ReadCommit() error                                     { return nil }
func (*blockingWire) ReadControl() (supervisorControl, error)               { return supervisorControl{}, io.EOF }
func (*blockingWire) WriteReady(*sessionwire.SupervisorActivityReady) error { return nil }
func (*blockingWire) WriteError(string, string) error                       { return nil }
func (*blockingWire) WriteCompletion(targetCompletion) error                { return nil }
func (w *blockingWire) WriteOutput(outputKind, []byte) error                { <-w.release; return nil }

func startLinuxTestTarget(
	t *testing.T,
	spec startSpec,
	wire supervisorWire,
) (*targetProcess, error) {
	t.Helper()
	return startTargetWithSessionCgroup(spec, wire, sessionCgroupOptions{
		Root:                       t.TempDir(),
		Backend:                    newFakeSessionCgroupBackend(),
		SkipAtomicPlacementForTest: true,
	})
}

func linuxTestStart(t *testing.T, terminal terminalSpec, argv []string) startSpec {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("target credential tests require the fixed root launcher")
	}
	account := linuxTestAccount(t)
	workdir, err := os.MkdirTemp("", "hideout-supervisor-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workdir) })
	if err := os.Chmod(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return startSpec{
		Protocol:       testProtocol,
		SessionID:      "ses_20260716T120000Z_0123456789abcdef",
		TargetUser:     account.Username,
		GuestWork:      workdir,
		Argv:           argv,
		Env:            []string{"HOME=" + workdir, "PATH=/usr/bin:/bin"},
		Terminal:       terminal,
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		SessionSource:  "/hideout/runtime/sessions/ses_20260716T120000Z_0123456789abcdef",
	}
}

func linuxTestAccount(t *testing.T) *user.User {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Uid != "0" {
		return current
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
		account, lookupErr := user.Lookup(sudoUser)
		if lookupErr == nil && account.Uid != "0" {
			return account
		}
	}
	for _, name := range []string{"nobody", "daemon"} {
		account, lookupErr := user.Lookup(name)
		if lookupErr == nil && account.Uid != "0" {
			return account
		}
	}
	t.Skip("Linux test host has no non-root account")
	return nil
}

func waitForOutput(t *testing.T, wire *recordingWire, substring string) {
	waitForStreamOutput(t, wire, outputTerminal, substring)
}

func waitForStreamOutput(t *testing.T, wire *recordingWire, kind outputKind, substring string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(wire.output(kind), substring) {
			return
		}
		select {
		case <-wire.written:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %q in %q", substring, wire.output(kind))
		}
	}
}
