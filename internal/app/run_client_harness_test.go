package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/backend/native"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
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
	initDaemonPrepare = func(_ context.Context, store profile.Store, request manager.InitServiceRequest) (manager.PreparedInit, error) {
		return (manager.InitService{Core: manager.New(store)}).Prepare(request)
	}
	initDaemonApply = func(_ context.Context, store profile.Store, prepared manager.PreparedInit, confirmation *manager.InitConfirmation) (manager.InitApplyResult, error) {
		return (manager.InitService{Core: manager.New(store)}).Apply(prepared, confirmation)
	}
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
	var streams *backend.RunStreams
	if prepared.Plan.Backend == "lima" {
		be = appTestDaemonStreamBackend{Backend: be}
		streams = &backend.RunStreams{
			Stdin: opts.Stdin, Stdout: opts.Stdout, Stderr: opts.Stderr, PTY: opts.Terminal,
			Ready: func(backend.SessionReadyProof) error { return nil },
		}
	}
	runResult, runErr := service.Apply(ctx, prepared, opts.Request, manager.RunServiceDependencies{
		Backend: be,
		OpenerForSession: func(runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, opts.Stdout, opts.Stderr)
		},
		PrepareWorkspaceAttachment: func(runSession *manager.RunSession) error {
			if err := runSession.WorkspaceAttachment.Validate(); err != nil {
				return err
			}
			credentialPath := filepath.Join(runSession.RuntimeSessionDir, "workspace", "credential.bin")
			if err := os.MkdirAll(filepath.Dir(credentialPath), 0o700); err != nil {
				return err
			}
			runtime := workspaceattach.PortalRuntime{
				Endpoint:            "host.lima.internal:46035",
				CredentialHostPath:  credentialPath,
				CredentialGuestPath: workspaceattach.PortalCredentialGuestPath,
			}
			if err := runtime.Validate(runSession.WorkspaceAttachment); err != nil {
				return err
			}
			runSession.WorkspacePortal = &runtime
			return nil
		},
		ActivateWorkspaceAttachment: func(runSession *manager.RunSession) error {
			if err := runSession.WorkspaceAttachment.Incarnation.Validate(true); err != nil {
				return err
			}
			return os.WriteFile(runSession.WorkspacePortal.CredentialHostPath, []byte("app-test-workspace-credential"), 0o600)
		},
		ReleaseWorkspaceAttachment: func(context.Context) error { return nil },
		Lifecycle:                  appTestLifecycleRegistrar{},
		Streams:                    streams,
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

type appTestDaemonStreamBackend struct {
	backend.Backend
}

func (b appTestDaemonStreamBackend) Activate(ctx context.Context, session *backend.Session, env []string) error {
	activator, ok := b.Backend.(backend.Activator)
	if !ok {
		return errors.New("test Lima backend is missing activation")
	}
	return activator.Activate(ctx, session, env)
}

func (b appTestDaemonStreamBackend) WarmActivate(ctx context.Context, session *backend.Session, env []string) error {
	warm, ok := b.Backend.(backend.WarmActivator)
	if !ok {
		return errors.New("test Lima backend is missing warm activation")
	}
	return warm.WarmActivate(ctx, session, env)
}

func (b appTestDaemonStreamBackend) WarmActivationOwner(session *backend.Session) (string, error) {
	provider, ok := b.Backend.(backend.WarmActivationReceiptProvider)
	if !ok {
		return "", errors.New("test Lima backend is missing warm activation receipt")
	}
	return provider.WarmActivationOwner(session)
}

func (b appTestDaemonStreamBackend) RunWithStreams(ctx context.Context, session *backend.Session, command []string, env []string, streams backend.RunStreams) error {
	proof, err := backend.ReadyProofForSession(session, backend.SessionReadyAuthenticatedSupervisor)
	if err != nil {
		return err
	}
	if streams.Ready == nil {
		return errors.New("test daemon stream is missing ready callback")
	}
	if err := streams.Ready(proof); err != nil {
		return err
	}
	if session.Workspace.Transport == backend.WorkspaceTransportPortal {
		// This harness proves app/manager lifecycle behavior without an SSH guest.
		// Real Portal execution is exclusively exercised through the daemon
		// supervisor stream and the packaged Lima gate.
		return nil
	}
	return b.Backend.Run(ctx, session, command, env)
}

func (b appTestDaemonStreamBackend) StartEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir, bootstrap string, env []string) error {
	controller, ok := b.Backend.(backend.EnvironmentNetworkServiceController)
	if !ok {
		return errors.New("test Lima backend is missing environment network controller")
	}
	return controller.StartEnvironmentNetwork(ctx, session, workdir, bootstrap, env)
}

func (b appTestDaemonStreamBackend) VerifyEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir string, env []string) error {
	controller, ok := b.Backend.(backend.EnvironmentNetworkServiceController)
	if !ok {
		return errors.New("test Lima backend is missing environment network controller")
	}
	return controller.VerifyEnvironmentNetwork(ctx, session, workdir, env)
}

func (b appTestDaemonStreamBackend) VerifyDirectEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir string, env []string) error {
	controller, ok := b.Backend.(backend.EnvironmentNetworkServiceController)
	if !ok {
		// Direct mode has no guest-side service to start. Focused harnesses that
		// do not model a real guest may therefore accept the no-op verification.
		return nil
	}
	return controller.VerifyDirectEnvironmentNetwork(ctx, session, workdir, env)
}

func (b appTestDaemonStreamBackend) StopEnvironmentNetwork(ctx context.Context, session *backend.Session, workdir, cleanup string, env []string) error {
	controller, ok := b.Backend.(backend.EnvironmentNetworkServiceController)
	if !ok {
		return errors.New("test Lima backend is missing environment network controller")
	}
	return controller.StopEnvironmentNetwork(ctx, session, workdir, cleanup, env)
}

// appTestLifecycleRegistrar models the already-established daemon owner only
// for focused CLI tests. Production run never uses this in-process adapter.
type appTestLifecycleRegistrar struct{}

func (appTestLifecycleRegistrar) ActiveAttachObservation(_ context.Context, environmentID, instanceName string) (backend.LifecycleObservation, bool) {
	return backend.LifecycleObservation{
		State: backend.LifecycleAbsent, InstanceName: instanceName, ObservedAt: time.Now().UTC(),
	}, true
}

func (appTestLifecycleRegistrar) BeginAttach(_ context.Context, req lifecycle.AttachRequest) (lifecycle.Registration, error) {
	return &appTestLifecycleRegistration{
		incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: req.EnvironmentID, StartGeneration: 1, InstanceName: req.InstanceName,
		},
		root:    lifecycle.ResourceRef{Kind: lifecycle.KindBackendIncarnation, ID: req.EnvironmentID, Generation: 1},
		session: lifecycle.ResourceRef{Kind: lifecycle.KindRunSession, ID: req.SessionID, Generation: 1},
	}, nil
}

type appTestLifecycleRegistration struct {
	mu          sync.Mutex
	incarnation lifecycle.EnvironmentRef
	root        lifecycle.ResourceRef
	session     lifecycle.ResourceRef
}

func (r *appTestLifecycleRegistration) Incarnation() lifecycle.EnvironmentRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.incarnation
}
func (r *appTestLifecycleRegistration) Root() lifecycle.ResourceRef    { return r.root }
func (r *appTestLifecycleRegistration) Session() lifecycle.ResourceRef { return r.session }
func (*appTestLifecycleRegistration) Commit(context.Context) error     { return nil }
func (r *appTestLifecycleRegistration) BindBoot(_ context.Context, bootID string) error {
	r.mu.Lock()
	r.incarnation.BootID = bootID
	r.mu.Unlock()
	return nil
}
func (*appTestLifecycleRegistration) Register(_ context.Context, spec lifecycle.RegistrationSpec) (lifecycle.ResourceRef, error) {
	return lifecycle.ResourceRef{Kind: spec.Kind, ID: spec.ID, Generation: 1}, nil
}
func (*appTestLifecycleRegistration) Transition(context.Context, lifecycle.ResourceRef, lifecycle.ResourceState) error {
	return nil
}
func (*appTestLifecycleRegistration) Release(context.Context, lifecycle.ResourceRef, error) error {
	return nil
}
func (*appTestLifecycleRegistration) RecordFact(context.Context, lifecycle.FactSpec) error {
	return nil
}
func (*appTestLifecycleRegistration) Finish(context.Context, error) error { return nil }

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

func TestNotifyPendingWriteDecisionsCountsOnlyThisSessionsPendingWrites(t *testing.T) {
	root := t.TempDir()
	store := decision.NewStore(root)
	now := time.Now().UTC()
	mk := func(id, kind, state, session string) decision.Decision {
		return decision.Decision{
			ID: id, Kind: kind, State: state,
			Source:         decision.Source{Profile: "default", Session: session, Backend: "lima", Surface: "hostfs"},
			Preview:        decision.Preview{Summary: "test staged write"},
			AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
			DefaultOutcome: decision.DefaultOutcomeDeny,
			TimeoutAt:      now.Add(time.Hour), CreatedAt: now,
		}
	}
	for _, d := range []decision.Decision{
		mk("hfwdec_aaaaaaaaaaaaaaaaaaaa", decision.KindHostFSWrite, decision.StatePending, "ses_this"),
		mk("hfwdec_bbbbbbbbbbbbbbbbbbbb", decision.KindHostFSWrite, decision.StatePending, "ses_this"),
		mk("hfwdec_cccccccccccccccccccc", decision.KindHostFSWrite, decision.StatePending, "ses_other"),
		mk("hfwdec_dddddddddddddddddddd", decision.KindHostFSWrite, decision.StateDenied, "ses_this"),
		mk("hfrdec_eeeeeeeeeeeeeeeeeeee", decision.KindHostFSRead, decision.StatePending, "ses_this"),
	} {
		if _, err := store.CreateOrUpdateDecision(d); err != nil {
			t.Fatalf("seed decision %s: %v", d.ID, err)
		}
	}
	var stderr bytes.Buffer
	a := app{stderr: &stderr}
	a.notifyPendingWriteDecisions(root, "ses_this")
	if got := stderr.String(); !strings.Contains(got, "2 staged write decisions await your review: hideout decision list") {
		t.Fatalf("notification = %q", got)
	}
	stderr.Reset()
	a.notifyPendingWriteDecisions(root, "ses_quiet")
	if stderr.Len() != 0 {
		t.Fatalf("session without pending writes produced output: %q", stderr.String())
	}
	stderr.Reset()
	a.notifyPendingWriteDecisions(root, "")
	if stderr.Len() != 0 {
		t.Fatalf("empty session id produced output: %q", stderr.String())
	}
}
