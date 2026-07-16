package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/backend/native"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

// Existing app tests exercise CLI parsing and Manager behavior in-process. The
// production path has no embedded fallback; this explicit test-only adapter
// keeps those focused tests fast while daemon/session packages cover the real
// transport and ownership boundary.
func TestMain(m *testing.M) {
	ensureRunDaemon = func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
		return daemon.Status{}, nil
	}
	runExecutable = func() (string, error) { return "/test/hideout", nil }
	runDaemonSession = runSessionInProcessForAppTests
	os.Exit(m.Run())
}

func runSessionInProcessForAppTests(ctx context.Context, opts daemon.SessionClientOptions) (daemon.SessionClientResult, error) {
	return runSessionInProcessWithBackendFactory(ctx, opts, func(prepared manager.PreparedRun) backend.Backend {
		switch prepared.Plan.Backend {
		case "lima":
			return lima.Backend{
				Stdout: opts.Stdout, Stderr: opts.Stderr, Stdin: opts.Stdin,
				ControlStdout: io.Discard, ControlStderr: io.Discard,
				SetupRunner: appSessionSetupRunner{},
			}
		default:
			return native.Backend{
				AllowWeakIsolation: opts.Request.AllowWeakIsolation,
				Stdout:             opts.Stdout, Stderr: opts.Stderr, Stdin: opts.Stdin,
			}
		}
	})
}

func runSessionInProcessWithBackendFactory(
	ctx context.Context,
	opts daemon.SessionClientOptions,
	backendFactory func(manager.PreparedRun) backend.Backend,
) (daemon.SessionClientResult, error) {
	service := manager.RunService{Core: manager.New(opts.Store)}
	prepared, err := service.Prepare(opts.Request)
	if err != nil {
		return daemon.SessionClientResult{}, err
	}
	review := sessionwire.Review{
		PlanVersion: prepared.Review.PlanVersion, PlanDigest: prepared.Review.PlanDigest,
		ConfirmationRequired: prepared.Review.RequiresConfirmation,
		Summary:              "test run review",
	}
	if opts.OnReview != nil {
		opts.OnReview(review)
	}
	if opts.OnNotice != nil {
		for _, notice := range prepared.Review.Notices {
			opts.OnNotice(sessionwire.Notice{Code: notice.Code, Summary: notice.Summary})
		}
	}
	if review.ConfirmationRequired {
		if opts.Confirm == nil {
			return daemon.SessionClientResult{}, errors.New("run confirmation is required")
		}
		accepted, err := opts.Confirm(review)
		if err != nil || !accepted {
			if err == nil {
				err = errors.New("run confirmation was denied")
			}
			return daemon.SessionClientResult{}, err
		}
		opts.Request.Confirmation = &manager.RunConfirmation{
			PlanVersion: review.PlanVersion, PlanDigest: review.PlanDigest, Accepted: true,
		}
	}
	if backendFactory == nil {
		return daemon.SessionClientResult{}, errors.New("test run backend factory is required")
	}
	be := backendFactory(prepared)
	runResult, runErr := service.Apply(ctx, prepared, opts.Request, manager.RunServiceDependencies{
		Backend: be,
		OpenerForSession: func(runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, opts.Stdout, opts.Stderr)
		},
	})
	result := daemon.SessionClientResult{Review: review, RunResult: runResult}
	result.Completion = appTestCompletion(runResult, runErr)
	if runErr == nil {
		return result, nil
	}
	if runResult.CleanupError != "" {
		return result, daemon.RemoteExitError{Completion: result.Completion}
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return result, daemon.RemoteExitError{Completion: result.Completion}
	}
	return result, runErr
}

func appTestCompletion(result manager.RunResult, err error) sessionwire.Completion {
	out := sessionwire.Completion{
		Kind: sessionwire.CompletionExit, ExitCode: 0, TargetCompleted: true,
		CleanupCompleted: result.CleanupError == "", SessionID: result.SessionID,
	}
	if result.CleanupError != "" {
		out.Kind = sessionwire.CompletionCleanupError
		out.ExitCode = 1
		out.Summary = result.CleanupError
		return out
	}
	if err == nil {
		return out
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			out.Kind = sessionwire.CompletionSignal
			out.Signal = status.Signal().String()
			out.ExitCode = 128 + int(status.Signal())
		}
		return out
	}
	out.ExitCode = 1
	return out
}
