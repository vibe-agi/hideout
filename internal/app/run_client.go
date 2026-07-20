package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	"golang.org/x/term"
)

var (
	ensureRunDaemon  = daemon.EnsureStarted
	runDaemonSession = daemon.RunSessionClient
	runExecutable    = os.Executable
)

func (a app) runViaDaemon(opts runOptions) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	clientCWD, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve client working directory: %w", err)
	}
	workspace := strings.TrimSpace(opts.workspace)
	if workspace == "" {
		workspace = clientCWD
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve client workspace: %w", err)
	}
	auditPath := strings.TrimSpace(opts.auditPath)
	if auditPath != "" && auditPath != "off" && !filepath.IsAbs(auditPath) {
		auditPath = filepath.Join(clientCWD, auditPath)
	}

	clientTerminal, err := newRunClientTerminal(opts.terminalMode, a.stdin, a.stdout)
	if err != nil {
		return err
	}
	defer clientTerminal.Close()

	// The daemon builds HostFS credential-root hiding relative to its own process
	// home; forward the client's effective home so a per-run relocated HOME also
	// gets its ~/.ssh-style dirs hidden (additive defense in depth). Empty on
	// error falls back to the daemon process home.
	operatorHome, _ := os.UserHomeDir()

	request := manager.RunServiceRequest{
		Version:     manager.RunServiceRequestVersion,
		ProfileName: opts.profileName, Backend: opts.backendName,
		NetworkMode: opts.networkMode, ProxySecretRef: opts.proxySecret,
		MediatedResolver: opts.mediatedResolver, Workspace: workspace,
		GuestWorkspace: opts.guestWorkspace, AllowUnsafeWorkspace: opts.allowUnsafeWorkspace,
		AllowWeakIsolation: opts.allowWeakIsolation, Ephemeral: opts.ephemeral,
		EnvironmentName: opts.envName, RemoveEnvironment: opts.removeEnvironment,
		Command: append([]string(nil), opts.command...), PublicEnv: cloneStringMap(opts.envPublic),
		AuditPath: auditPath, HostFSRun: opts.hostFSRun,
		DisableProfileHostFSGrants: opts.noProfileHostFSGrants,
		OperatorHome:               operatorHome,
		PreviewTargets:             append([]string(nil), opts.previewTargets...),
		Terminal:                   clientTerminal.Descriptor,
	}
	if len(request.Command) == 0 {
		return errors.New("command is required after --")
	}

	executableFn := a.daemonExecutable
	if executableFn == nil {
		executableFn = runExecutable
	}
	executable, err := executableFn()
	if err != nil {
		return fmt.Errorf("resolve hideout executable: %w", err)
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ensure := a.ensureDaemon
	if ensure == nil {
		ensure = ensureRunDaemon
	}
	if _, err := ensure(ctx, daemon.EnsureStartedOptions{
		Store: store, Executable: executable, BuildID: daemonBuildID(), Diagnostics: a.stderr,
	}); err != nil {
		return err
	}
	sessionClient := a.sessionClient
	if sessionClient == nil {
		sessionClient = runDaemonSession
	}
	result, runErr := sessionClient(ctx, daemon.SessionClientOptions{
		Store: store, Request: request, ClientVersion: runClientVersion(),
		Stdin: a.stdin, Stdout: a.stdout, Stderr: a.stderr, Terminal: a.stdout,
		Controls: clientTerminal.Controls, BeforeIO: clientTerminal.Activate,
		Confirm: a.confirmRunReview,
		OnNotice: func(notice sessionwire.Notice) {
			if strings.HasSuffix(notice.Code, ".workspace-shadowed") {
				fmt.Fprintf(a.stderr, "warning: %s\n", notice.Summary)
				return
			}
			fmt.Fprintf(a.stderr, "hideout: %s: %s\n", notice.Code, notice.Summary)
		},
	})
	if opts.verbose && result.RunResult.Version != "" {
		a.writeRunResultSummary(result.RunResult)
	}
	a.notifyPendingWriteDecisions(store.Root, result.Started.SessionID)
	return runErr
}

// notifyPendingWriteDecisions surfaces staged HostFS writes left behind by
// this session. Guest-visible write success without this line is a maze: the
// operator would have to already know that decisions exist and where to look.
// It must go through Manager's aggregated listing: operator-center decision
// records are materialized lazily from the session overlay store, so reading
// the decision store directly right after a run observes nothing.
// Best-effort presentation only; it never alters run outcome or decisions.
func (a app) notifyPendingWriteDecisions(storeRoot, sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	pending, err := manager.New(profile.Store{Root: storeRoot}).ListDecisions(manager.DecisionListRequest{
		Kind: decision.KindHostFSWrite, State: decision.StatePending, Session: sessionID,
	})
	if err != nil || len(pending) == 0 {
		return
	}
	noun := "decision"
	if len(pending) > 1 {
		noun = "decisions"
	}
	fmt.Fprintf(a.stderr, "hideout: %d staged write %s await your review: hideout decision list\n", len(pending), noun)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func runClientVersion() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	commit := strings.TrimSpace(Commit)
	if commit == "" || commit == "unknown" {
		return version
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return version + "+" + commit
}

func (a app) confirmRunReview(review sessionwire.Review) (bool, error) {
	input, inputOK := a.stdin.(*os.File)
	output, outputOK := a.stderr.(*os.File)
	if !inputOK || !outputOK || !term.IsTerminal(int(input.Fd())) || !term.IsTerminal(int(output.Fd())) {
		return false, errors.New("run confirmation requires an interactive terminal")
	}
	fmt.Fprintln(a.stderr, review.Summary)
	fmt.Fprint(a.stderr, "Continue? [y/N] ")
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

type runClientTerminal struct {
	Descriptor manager.TerminalDescriptor
	Controls   <-chan backend.RunControl

	stdinFile   *os.File
	stdoutFile  *os.File
	controlSink chan backend.RunControl
	done        chan struct{}

	mu            sync.Mutex
	rawState      *term.State
	resizeSignals chan os.Signal
	active        bool
	closed        bool
}

func newRunClientTerminal(mode string, stdin io.Reader, stdout io.Writer) (*runClientTerminal, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}
	stdinFile, stdinFileOK := stdin.(*os.File)
	stdoutFile, stdoutFileOK := stdout.(*os.File)
	isTerminal := stdinFileOK && stdoutFileOK && term.IsTerminal(int(stdinFile.Fd())) && term.IsTerminal(int(stdoutFile.Fd()))
	switch mode {
	case "auto":
	case "always":
		if !isTerminal {
			return nil, errors.New("--terminal always requires terminal stdin and stdout")
		}
	case "never":
		isTerminal = false
	default:
		return nil, fmt.Errorf("unsupported --terminal mode %q (use auto, always, or never)", mode)
	}
	out := &runClientTerminal{done: make(chan struct{})}
	if !isTerminal {
		out.Descriptor.Mode = runsession.TerminalNone
		return out, nil
	}
	columns, rows, err := term.GetSize(int(stdoutFile.Fd()))
	if err != nil || columns <= 0 || rows <= 0 {
		columns, rows = 80, 24
	}
	controls := make(chan backend.RunControl, 1)
	out.stdinFile = stdinFile
	out.stdoutFile = stdoutFile
	out.controlSink = controls
	out.Controls = controls
	out.Descriptor = manager.TerminalDescriptor{
		Mode: runsession.TerminalPTY, Rows: boundedTerminalDimension(rows),
		Columns: boundedTerminalDimension(columns), TERM: clientTERM(),
	}
	return out, nil
}

func (t *runClientTerminal) Activate(_ sessionwire.Started) error {
	if t == nil || t.Descriptor.Mode != runsession.TerminalPTY {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("terminal client closed before session start")
	}
	if t.active {
		return nil
	}
	state, err := term.MakeRaw(int(t.stdinFile.Fd()))
	if err != nil {
		return fmt.Errorf("enter raw terminal mode: %w", err)
	}
	t.rawState = state
	t.active = true
	t.resizeSignals = make(chan os.Signal, 1)
	signal.Notify(t.resizeSignals, syscall.SIGWINCH)
	go t.watchResize()
	return nil
}

func (t *runClientTerminal) watchResize() {
	for {
		select {
		case <-t.done:
			return
		case <-t.resizeSignals:
			columns, rows, err := term.GetSize(int(t.stdoutFile.Fd()))
			if err != nil || columns <= 0 || rows <= 0 {
				continue
			}
			control := backend.RunControl{
				Kind: backend.RunControlResize, Rows: boundedTerminalDimension(rows),
				Columns: boundedTerminalDimension(columns),
			}
			select {
			case t.controlSink <- control:
			default:
				select {
				case <-t.controlSink:
				default:
				}
				select {
				case t.controlSink <- control:
				default:
				}
			}
		}
	}
}

func (t *runClientTerminal) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.done)
	if t.resizeSignals != nil {
		signal.Stop(t.resizeSignals)
	}
	if t.rawState != nil {
		return term.Restore(int(t.stdinFile.Fd()), t.rawState)
	}
	return nil
}

func boundedTerminalDimension(value int) uint16 {
	if value < 1 {
		return 1
	}
	if value > int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(value)
}

func clientTERM() string {
	value := strings.TrimSpace(os.Getenv("TERM"))
	if value == "" || len(value) > 64 {
		return sessionwire.DefaultTERM
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '+' {
			if index != 0 || (r != '-' && r != '_' && r != '.' && r != '+') {
				continue
			}
		}
		return sessionwire.DefaultTERM
	}
	return value
}

func errorExitCode(err error) int {
	if err == nil {
		return 0
	}
	type exitCoder interface{ ExitCode() int }
	var coder exitCoder
	if errors.As(err, &coder) {
		code := coder.ExitCode()
		if code > 0 && code <= 255 {
			return code
		}
	}
	return 1
}

func silentTargetExit(err error) bool {
	var remote daemon.RemoteExitError
	if !errors.As(err, &remote) {
		return false
	}
	return remote.Completion.Kind == sessionwire.CompletionExit || remote.Completion.Kind == sessionwire.CompletionSignal
}
