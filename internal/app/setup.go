package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
	"golang.org/x/term"
)

func (a app) setupCommand() error {
	if !a.isInteractiveTerminal() {
		return errors.New("setup requires an interactive terminal; automation must use hideout init --no-input")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := a.ensureInitDaemon(ctx, store); err != nil {
		return codedInitError(err)
	}
	prepared, err := a.prepareInit(ctx, store, manager.SetupInitServiceRequest())
	if err != nil {
		return codedInitError(err)
	}
	switch prepared.Review.State {
	case manager.InitStateReady:
		writeSetupReady(a.stdout, prepared.Review)
		return nil
	case manager.InitStateRepairable:
		writeSetupRecovery(a.stdout, prepared.Review)
		return withRecoveryCode(manager.ErrInitNotApplicable, recovery.CodeSetupProfileRepairRequired)
	case manager.InitStateBlocked:
		writeSetupRecovery(a.stdout, prepared.Review)
		return withRecoveryCode(manager.ErrInitNotApplicable, recovery.CodeSetupProfileBlocked)
	case manager.InitStateFresh:
	default:
		return fmt.Errorf("unsupported setup state %q", prepared.Review.State)
	}
	writeSetupReview(a.stdout, prepared.Review)
	confirmed, err := a.confirmSetup(ctx, bufio.NewReader(a.stdin))
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("setup cancelled")
	}
	result, err := a.applyInit(ctx, store, prepared, &manager.InitConfirmation{
		ReviewVersion: prepared.Review.Version,
		PlanDigest:    prepared.Review.PlanDigest,
		Confirmed:     true,
	})
	if err != nil {
		return codedInitError(err)
	}
	writeSetupSuccess(a.stdout, result)
	return nil
}

func (a app) isInteractiveTerminal() bool {
	if a.terminalInteractive != nil {
		return a.terminalInteractive()
	}
	in, inOK := a.stdin.(*os.File)
	out, outOK := a.stdout.(*os.File)
	return inOK && outOK && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))
}

func (a app) confirmSetup(ctx context.Context, reader *bufio.Reader) (bool, error) {
	fmt.Fprint(a.stdout, "Set up this configuration? [y/N]: ")
	type readResult struct {
		value string
		err   error
	}
	result := make(chan readResult, 1)
	go func() {
		value, err := reader.ReadString('\n')
		result <- readResult{value: value, err: err}
	}()
	var value string
	var err error
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("setup interrupted: %w", ctx.Err())
	case read := <-result:
		value, err = read.value, read.err
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false, nil
		}
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func writeSetupReview(w io.Writer, review manager.InitReview) {
	fmt.Fprintln(w, "Set up Hideout")
	fmt.Fprintf(w, "  Isolation: Lima virtual machine\n")
	fmt.Fprintf(w, "  Runtime: %s %s (%s, %s)\n", review.Runtime.Family, review.Runtime.Revision, review.Runtime.Status, formatSetupBytes(review.Runtime.DownloadBytes))
	fmt.Fprintf(w, "  Workspace: the project you run in, read/write at %s\n", review.Workspace.GuestPath)
	fmt.Fprintln(w, "  Sharing: projects share one default VM; hideout env create <name> gives a dedicated VM")
	fmt.Fprintln(w, "  Other files: hidden unless you grant access")
	fmt.Fprintln(w, "  Network: direct; this does not hide your network origin")
	fmt.Fprintln(w, "  Audit: always on")
	fmt.Fprintln(w, "  Now: configuration only; no VM start or runtime download")
}

func writeSetupReady(w io.Writer, review manager.InitReview) {
	fmt.Fprintln(w, "Already set up")
	fmt.Fprintf(w, "  Profile: %s\n", review.Profile)
	fmt.Fprintf(w, "  Network: %s\n", review.Network)
	if review.Runtime.Family != "" {
		fmt.Fprintf(w, "  Runtime: %s %s\n", review.Runtime.Family, review.Runtime.Revision)
	}
	writeSetupNextSteps(w)
}

func writeSetupRecovery(w io.Writer, review manager.InitReview) {
	fmt.Fprintln(w, "Setup needs attention")
	for _, notice := range review.Notices {
		fmt.Fprintf(w, "  %s\n", notice.Summary)
		if notice.Action != "" {
			fmt.Fprintf(w, "  Next: %s\n", notice.Action)
		}
	}
}

func writeSetupSuccess(w io.Writer, _ manager.InitApplyResult) {
	fmt.Fprintln(w, "Hideout configuration is ready.")
	writeSetupNextSteps(w)
}

func writeSetupNextSteps(w io.Writer) {
	fmt.Fprintln(w, "Next:")
	fmt.Fprintln(w, "  hideout doctor")
	fmt.Fprintln(w, "  cd /path/to/project")
	fmt.Fprintln(w, "  hideout run -- git status --short")
	fmt.Fprintln(w, "More:")
	fmt.Fprintln(w, "  hideout run -- code .")
	fmt.Fprintln(w, "  hideout run -- sh -lc 'npm install --global --prefix \"$HOME/.local\" @openai/codex@0.144.1'")
	fmt.Fprintln(w, "  hideout run -- codex --version")
	fmt.Fprintln(w, "Privacy later:")
	fmt.Fprintln(w, "  hideout connect through <proxy-secret> using <resolver>")
}

func formatSetupBytes(value int64) string {
	if value <= 0 {
		return "size unavailable"
	}
	const gib = int64(1 << 30)
	const mib = int64(1 << 20)
	if value >= gib {
		return fmt.Sprintf("%.1f GiB declared download", float64(value)/float64(gib))
	}
	return fmt.Sprintf("%.0f MiB declared download", float64(value)/float64(mib))
}
