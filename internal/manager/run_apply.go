package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
	runsession "github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	RunResultVersion          = "hideout.run-result/v1"
	previewOpenDelay          = 250 * time.Millisecond
	previewOpenReadyTimeout   = 30 * time.Second
	previewOpenReadyPoll      = 100 * time.Millisecond
	previewOpenRequestTimeout = 750 * time.Millisecond
)

type ApplyRunOptions struct {
	Backend                     backend.Backend
	RequestedBackend            string
	AllowWeakIsolation          bool
	Environment                 RunEnvironmentOptions
	AuditPath                   string
	HostFSRun                   hostfs.Config
	DisableProfileHostFSGrants  bool
	OperatorHome                string
	PortBridges                 []RunPortBridgeRequest
	OpenTargets                 []RunOpenTargetOwner
	EndpointCandidates          []RunEndpointCandidate
	EndpointExposures           []RunEndpointExposureRequest
	Network                     RunNetworkOptions
	Opener                      broker.Opener
	OpenerForSession            func(RunSession) broker.Opener
	PrepareWorkspaceAttachment  func(*RunSession) error
	ActivateWorkspaceAttachment func(*RunSession) error
	ReleaseWorkspaceAttachment  func(context.Context) error
	TerminalMode                runsession.TerminalMode
	Streams                     *backend.RunStreams
	Lifecycle                   lifecycle.Registrar
}

type RunResult struct {
	Version          string `json:"version"`
	SessionID        string `json:"sessionId"`
	Profile          string `json:"profile"`
	Backend          string `json:"backend"`
	EnvironmentID    string `json:"environmentId,omitempty"`
	EnvironmentName  string `json:"environmentName,omitempty"`
	InstanceName     string `json:"instanceName,omitempty"`
	PreserveInstance bool   `json:"preserveInstance,omitempty"`
	// EnvironmentDisposition reports what happened to a disposable (--rm)
	// environment: "removed" after a proved teardown, "cleanup-required" when
	// the record was retained for reconcile/clean. Empty for reusable runs.
	EnvironmentDisposition string           `json:"environmentDisposition,omitempty"`
	AuditPath              string           `json:"auditPath,omitempty"`
	BoundarySummary        *BoundarySummary `json:"boundarySummary,omitempty"`
	Command                []string         `json:"command"`
	Error                  string           `json:"error,omitempty"`
	CleanupError           string           `json:"cleanupError,omitempty"`
}

func (c Core) ApplyRun(ctx context.Context, plan RunPlan, opts ApplyRunOptions) (result RunResult, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Backend == nil {
		return result, errors.New("backend is required")
	}
	if len(plan.Command) == 0 {
		return result, errors.New("command is required")
	}
	result = RunResult{
		Version: RunResultVersion,
		Profile: plan.ProfileName,
		Backend: plan.Backend,
		Command: append([]string(nil), plan.Command...),
	}
	if err := validateRunPolicy(plan); err != nil {
		return result, err
	}
	if err := opts.Backend.Available(ctx); err != nil {
		return result, err
	}
	if _, err := c.EnsureRunInitialized(plan); err != nil {
		return result, err
	}
	runEnv, err := c.SelectRunEnvironment(plan, opts.Environment)
	if err != nil {
		return result, err
	}
	environmentStore := environment.Store{Root: c.Store.Root}
	var (
		allocatedLayout     *runsession.Layout
		establishment       lifecycle.EstablishmentReservation
		preparedIncarnation lifecycle.EnvironmentRef
	)
	if opts.Lifecycle != nil && runEnv.Active && runEnv.Record.Backend == "lima" {
		layout, allocateErr := c.AllocateRunSession()
		if allocateErr != nil {
			return result, allocateErr
		}
		allocatedLayout = &layout
		result.SessionID = layout.ID
		establishment, err = opts.Lifecycle.ReserveAttach(ctx, lifecycle.EstablishmentRequest{
			EnvironmentID: runEnv.Record.ID,
			SessionID:     layout.ID,
		})
		if err != nil {
			var blocked *lifecycle.AttachBlockedError
			if errors.As(err, &blocked) && blocked.ReasonCode == "owner-requires-explicit-recovery" {
				return result, &EnvironmentOwnerError{
					Code: recovery.CodeSessionCleanupFailed, EnvironmentID: runEnv.Record.ID,
					Err: fmt.Errorf("stale session owner requires explicit recovery: %w", runsession.ErrOwnerCleanupFailed),
				}
			}
			return result, err
		}
		defer func() {
			if establishment == nil {
				return
			}
			abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			abortErr := establishment.Abort(abortCtx, retErr)
			cancel()
			if abortErr != nil {
				result.CleanupError = appendCleanupError(result.CleanupError, abortErr)
				if retErr == nil {
					result.Error = abortErr.Error()
					retErr = abortErr
				}
			}
		}()
	}
	var transitionLock *environment.Lock
	if runEnv.Active {
		transitionLock, err = environmentStore.LockContext(ctx, runEnv.Record.ID)
		if err != nil {
			return result, err
		}
		defer func() {
			if transitionLock == nil {
				return
			}
			if unlockErr := transitionLock.Unlock(); unlockErr != nil && retErr == nil {
				result.Error = unlockErr.Error()
				retErr = unlockErr
			}
			transitionLock = nil
		}()
		// Selection precedes the cross-process transition lock. Re-read the
		// record while holding that lock so a concurrent clean cannot turn a
		// stale in-memory selection into a resurrected environment.
		current, loadErr := environmentStore.Load(runEnv.Record.ID)
		if loadErr != nil {
			return result, fmt.Errorf("reload selected environment after transition lock: %w", loadErr)
		}
		expectedSpec, specErr := runEnvironmentSpecForRecord(
			current, plan.RuntimeProfile, plan.Backend, plan.Workspace, plan.GuestWorkspace,
		)
		if specErr != nil {
			return result, specErr
		}
		if validateErr := ValidateEnvironmentRecord(current, expectedSpec); validateErr != nil {
			return result, validateErr
		}
		runEnv.Record = current
		runEnv.BootReconfigure = current.BootConfigurationID != expectedSpec.BootConfigurationID
		if err := requireAttachableEnvironmentOwners(environmentStore, current.ID); err != nil {
			return result, err
		}
		if establishment != nil {
			observation, observeErr := lifecycleObservationForAttach(
				ctx, opts.Lifecycle, opts.Backend, runEnv.Record.ID, runEnv.Record.InstanceName,
			)
			if observeErr != nil {
				return result, observeErr
			}
			preparedIncarnation, err = establishment.Prepare(ctx, lifecycle.AttachRequest{
				EnvironmentID: runEnv.Record.ID,
				InstanceName:  runEnv.Record.InstanceName,
				SessionID:     allocatedLayout.ID,
				Observation:   observation,
			})
			if err != nil {
				return result, err
			}
		}
	}
	runSession, err := c.BeginRunSession(plan, runEnv, RunSessionOptions{AllocatedLayout: allocatedLayout})
	if err != nil {
		return result, err
	}
	result.SessionID = runSession.Layout.ID
	result.AuditPath = runSession.AuditPath
	runEnv = runSession.Environment
	var (
		owner                  *runsession.Owner
		lifecycleCleanupErr    error
		disposalProved         bool
		lifecycleRegistration  lifecycle.Registration
		lifecycleSupervisorRef lifecycle.ResourceRef
		lifecycleTargetRef     lifecycle.ResourceRef
		lifecycleNetworkRef    lifecycle.ResourceRef
		workspaceLifecycleRefs runWorkspaceLifecycleRefs
	)
	// Install cleanup before any post-materialization validation or ownership
	// step. Defers run in reverse order: session authority closes first, then a
	// pre-owner runtime fallback or the durable-owner cleanup path.
	defer func() {
		if owner == nil {
			return
		}
		disposition, finishErr := c.finishConcurrentRunEnvironment(ctx, &transitionLock, runEnv, owner, runSession.Layout.ID, lifecycleCleanupErr, disposalProved, lifecycleRegistration)
		result.EnvironmentDisposition = disposition
		if finishErr != nil {
			result.CleanupError = appendCleanupError(result.CleanupError, finishErr)
			if retErr == nil {
				result.Error = finishErr.Error()
				retErr = finishErr
			}
		}
	}()
	defer func() {
		if owner != nil || !runEnv.Active {
			return
		}
		cleanupErr := environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
		if cleanupErr != nil {
			result.CleanupError = appendCleanupError(result.CleanupError, cleanupErr)
			if retErr == nil {
				result.Error = cleanupErr.Error()
				retErr = cleanupErr
			}
		}
	}()
	defer func() {
		_, closeErr := c.CloseRunSession(runSession)
		summary := SummarizeRunBoundary(result.AuditPath)
		result.BoundarySummary = &summary
		if closeErr != nil && retErr == nil {
			result.Error = closeErr.Error()
			retErr = closeErr
		}
		lifecycleCleanupErr = errors.Join(lifecycleCleanupErr, closeErr)
		result.CleanupError = appendCleanupError(result.CleanupError, closeErr)
	}()
	if runEnv.Record.Mode == environment.ModeShared {
		if establishment == nil {
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, ErrSharedWorkspaceDaemonOwnerRequired
		}
		if opts.PrepareWorkspaceAttachment == nil || opts.ActivateWorkspaceAttachment == nil || opts.ReleaseWorkspaceAttachment == nil {
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, ErrSharedWorkspaceDaemonOwnerRequired
		}
		if opts.Streams == nil || opts.Streams.Ready == nil {
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, ErrSharedWorkspaceReadyBarrierRequired
		}
		workspacePlan, planErr := c.PlanRunWorkspaceAttachmentForIncarnation(runSession, preparedIncarnation, WorkspaceAttachPlanOptions{})
		if planErr != nil {
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, planErr
		}
		runSession, err = c.ApplyRunWorkspaceAttachment(runSession, workspacePlan)
		if err != nil {
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, err
		}
	}
	if runEnv.Active {
		terminalMode := opts.TerminalMode
		if terminalMode == "" {
			terminalMode = runsession.TerminalNone
		}
		workspaceAuthority, authorityErr := workspaceAuthorityForRunSession(runSession)
		if authorityErr != nil {
			if lifecycleRegistration != nil {
				_ = lifecycleRegistration.Transition(ctx, lifecycleRegistration.Session(), lifecycle.StateFailed)
				_ = lifecycleRegistration.Finish(ctx, authorityErr)
			}
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, authorityErr
		}
		now := time.Now().UTC()
		owner, err = runsession.AcquireOwner(environmentStore.OwnerRoot(runEnv.Record.ID), runsession.OwnerRecord{
			Schema:            runsession.ActiveSessionSchema,
			SessionID:         runSession.Layout.ID,
			EnvironmentID:     runEnv.Record.ID,
			Profile:           plan.ProfileName,
			Backend:           plan.Backend,
			WorkspaceID:       workspaceAuthority.WorkspaceID,
			SessionSnapshotID: runSession.SessionSnapshotID,
			State:             runsession.OwnerStatePreparing,
			TerminalMode:      terminalMode,
			StartedAt:         now,
			UpdatedAt:         now,
			CommandClass:      ownerCommandClass(plan.Command[0]),
		})
		if err != nil {
			if lifecycleRegistration != nil {
				_ = lifecycleRegistration.Transition(ctx, lifecycleRegistration.Session(), lifecycle.StateFailed)
				_ = lifecycleRegistration.Finish(ctx, nil)
			}
			_ = environmentStore.ClearSessionRuntime(runEnv.Record.ID, runSession.Layout.ID)
			return result, err
		}
	}
	if establishment != nil {
		lifecycleRegistration, err = establishment.Promote(ctx)
		if err != nil {
			return result, err
		}
		establishment = nil
		lifecycleSupervisorRef, err = lifecycleRegistration.Register(ctx, lifecycle.RegistrationSpec{
			Kind: lifecycle.KindGuestSupervisor, ID: runSession.Layout.ID,
			OwnerKind: "session", OwnerID: runSession.Layout.ID, State: lifecycle.StatePlanned,
			Dependencies: []lifecycle.DependencySpec{
				{Ref: lifecycleRegistration.Root(), StopMode: lifecycle.StopModeDrain},
				{Ref: lifecycleRegistration.Session(), StopMode: lifecycle.StopModeDrain},
			},
			Persistence: lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.CloseCoTerminateWithRoot,
			PossibleVMDependency: true,
		})
		if err != nil {
			return result, err
		}
		lifecycleTargetRef, err = lifecycleRegistration.Register(ctx, lifecycle.RegistrationSpec{
			Kind: lifecycle.KindGuestTarget, ID: runSession.Layout.ID,
			OwnerKind: "session", OwnerID: runSession.Layout.ID, State: lifecycle.StatePlanned,
			Dependencies: []lifecycle.DependencySpec{{Ref: lifecycleSupervisorRef, StopMode: lifecycle.StopModeDrain}},
			Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.CloseCoTerminateWithRoot,
			PossibleVMDependency: true,
		})
		if err != nil {
			return result, err
		}
	}
	if runEnv.Record.Mode == environment.ModeShared {
		workspaceLifecycleRefs, err = registerRunWorkspaceLifecycle(ctx, lifecycleRegistration, runSession.WorkspaceAttachment)
		if err != nil {
			return result, err
		}
		if err := startRunWorkspaceLifecycle(ctx, lifecycleRegistration, workspaceLifecycleRefs); err != nil {
			return result, err
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cleanupErr := releaseRunWorkspaceLifecycle(cleanupCtx, lifecycleRegistration, workspaceLifecycleRefs, opts.ReleaseWorkspaceAttachment)
			cancel()
			lifecycleCleanupErr = errors.Join(lifecycleCleanupErr, cleanupErr)
			result.CleanupError = appendCleanupError(result.CleanupError, cleanupErr)
			if cleanupErr != nil && retErr == nil {
				result.Error = cleanupErr.Error()
				retErr = cleanupErr
			}
		}()
		if err := opts.PrepareWorkspaceAttachment(&runSession); err != nil {
			return result, fmt.Errorf("prepare shared workspace provider with daemon session owner: %w", err)
		}
		if err := lifecycleRegistration.Transition(ctx, workspaceLifecycleRefs.Provider, lifecycle.StateActive); err != nil {
			return result, fmt.Errorf("activate workspace provider lifecycle: %w", err)
		}
	}
	if plan.Ephemeral {
		if err := profile.MaterializeIdentityState(runSession.IdentityDir, plan.RuntimeProfile); err != nil {
			return result, err
		}
	}
	runSession, err = c.OpenRunSessionAudit(runSession, RunAuditOptions{AuditPath: opts.AuditPath})
	if err != nil {
		return result, err
	}
	result.AuditPath = runSession.AuditPath
	c.emitOperation("session", "start", runSessionOperationDetails(runSession, "running"))
	defer func() {
		status := "completed"
		phase := "complete"
		if retErr != nil {
			status = "failed"
			phase = "failed"
		}
		c.emitOperation("session", phase, runSessionOperationDetails(runSession, status))
	}()
	if err := emitRunSetupAudit(runSession.Audit, runSession, opts, c.Store.Root); err != nil {
		return result, err
	}
	runNetwork, netErr := c.PrepareRunNetwork(runSession, opts.Network)
	defer func() {
		if runNetwork.GatewayChange == nil {
			return
		}
		rollbackErr := runNetwork.GatewayChange.Rollback()
		lifecycleCleanupErr = errors.Join(lifecycleCleanupErr, rollbackErr)
		result.CleanupError = appendCleanupError(result.CleanupError, rollbackErr)
		if rollbackErr != nil && retErr == nil {
			result.Error = rollbackErr.Error()
			retErr = rollbackErr
		}
	}()
	defer func() {
		if !runNetwork.EnvironmentServiceStart {
			return
		}
		rollbackErr := c.discardUnstartedEnvironmentNetworkService(runSession.Environment.Record.ID)
		lifecycleCleanupErr = errors.Join(lifecycleCleanupErr, rollbackErr)
		result.CleanupError = appendCleanupError(result.CleanupError, rollbackErr)
		if rollbackErr != nil && retErr == nil {
			result.Error = rollbackErr.Error()
			retErr = rollbackErr
		}
	}()
	if netErr != nil {
		return result, netErr
	}
	opener := opts.Opener
	if opts.OpenerForSession != nil {
		opener = opts.OpenerForSession(runSession)
	}
	dataPlane, err := c.StartRunDataPlane(ctx, runSession, runNetwork, RunDataPlaneOptions{
		HostFSRun:                  opts.HostFSRun,
		DisableProfileHostFSGrants: opts.DisableProfileHostFSGrants,
		OperatorHome:               opts.OperatorHome,
		Backend:                    opts.Backend,
		PortBridges:                opts.PortBridges,
		OpenTargets:                opts.OpenTargets,
		EndpointCandidates:         opts.EndpointCandidates,
		EndpointExposures:          opts.EndpointExposures,
		Opener:                     opener,
		RequireSessionSupervisor:   opts.Streams != nil,
		Lifecycle:                  lifecycleRegistration,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := c.CloseRunDataPlane(dataPlane); closeErr != nil {
			lifecycleCleanupErr = errors.Join(lifecycleCleanupErr, closeErr)
			result.CleanupError = appendCleanupError(result.CleanupError, closeErr)
			if retErr == nil {
				result.Error = closeErr.Error()
				retErr = closeErr
			}
		}
	}()
	runSpec := c.runSpec(runSession, runEnv, dataPlane, runNetwork)
	if err := c.attachRuntimeVerification(&runSpec, runSession, runEnv, opts.Backend.Name(), runtimeVerificationSessionAuthority); err != nil {
		return result, err
	}
	session, err := opts.Backend.Prepare(ctx, runSpec)
	if err != nil {
		return result, err
	}
	// Manager owns the immutable policy/configuration snapshot. Bind it after
	// backend preparation so no backend implementation can omit or rewrite it.
	session.SessionSnapshotID = runSession.SessionSnapshotID
	result.EnvironmentID = session.EnvironmentID
	result.EnvironmentName = runEnv.Record.Name
	result.InstanceName = session.InstanceName
	result.PreserveInstance = session.PreserveInstance
	defer func() {
		cleanupErr := opts.Backend.Cleanup(ctx, session)
		decision := "allow"
		details := map[string]any{
			"session":          session.ID,
			"environment":      session.EnvironmentID,
			"instance":         session.InstanceName,
			"preserveInstance": session.PreserveInstance,
		}
		if runEnv.Active && runEnv.Record.Disposable {
			// A clean backend cleanup plus a stable-absent inventory observation
			// is the destruction proof the disposable teardown consumes; anything
			// less retains the record instead of faking success.
			disposalProved = cleanupErr == nil && disposableCleanupProved(opts.Backend, runEnv.Record)
			details["disposable"] = true
			details["disposalProved"] = disposalProved
		}
		if runEnv.Active {
			lifecycleCleanupErr = errors.Join(lifecycleCleanupErr, cleanupErr)
		} else {
			runEnv, cleanupErr = c.FinishRunEnvironment(runEnv, cleanupErr)
		}
		if cleanupErr != nil {
			decision = "error"
			details["error"] = cleanupErr.Error()
			result.CleanupError = appendCleanupError(result.CleanupError, cleanupErr)
			if retErr == nil {
				result.Error = cleanupErr.Error()
				retErr = cleanupErr
			}
		}
		_ = runSession.Audit.Emit(audit.Event{
			Session:  runSession.Layout.ID,
			Profile:  plan.ProfileName,
			Backend:  plan.Backend,
			Action:   "backend.cleanup",
			Decision: decision,
			Details:  details,
		})
	}()
	if runEnv.Active {
		liveOwners, ownerErr := liveOwnerIDs(environmentStore.OwnerRoot(runEnv.Record.ID))
		if ownerErr != nil {
			return result, ownerErr
		}
		activated := false
		if len(liveOwners) > 1 {
			warm, warmOK := opts.Backend.(backend.WarmActivator)
			provider, providerOK := opts.Backend.(backend.WarmActivationReceiptProvider)
			if warmOK && providerOK {
				receiptOwner, receiptErr := provider.WarmActivationOwner(session)
				if receiptErr == nil && receiptOwner != session.ID && liveOwners[receiptOwner] {
					session.ActivationOwnerID = receiptOwner
					if err := warm.WarmActivate(ctx, session, dataPlane.Env); err != nil {
						return result, err
					}
					activated = true
				}
			}
		}
		if !activated {
			if activator, ok := opts.Backend.(backend.Activator); ok {
				if err := activator.Activate(ctx, session, dataPlane.Env); err != nil {
					return result, err
				}
			}
		}
		if runEnv.BootReconfigure {
			controller, ok := opts.Backend.(backend.EnvironmentBootController)
			if !ok {
				return result, errors.New("selected backend cannot reconcile the environment boot configuration")
			}
			if err := controller.ReconcileEnvironmentBoot(ctx, session, runEnv.Configuration.Boot, dataPlane.Env); err != nil {
				return result, fmt.Errorf("reconcile environment boot configuration: %w", err)
			}
			rec := runEnv.Record
			rec.BootConfigurationID = runEnv.Configuration.Layers.BootID
			rec.Hostname = runEnv.Configuration.Boot.Hostname
			if err := environmentStore.Save(rec); err != nil {
				return result, fmt.Errorf("record reconciled environment boot configuration: %w", err)
			}
			runEnv.Record = rec
			runEnv.BootReconfigure = false
			runSession.Environment = runEnv
			_ = runSession.Audit.Emit(audit.Event{
				Session: runSession.Layout.ID, Profile: plan.ProfileName, Backend: plan.Backend,
				Action: "environment.boot.reconfigure", Decision: "allow",
				Details: map[string]any{"environmentId": rec.ID, "bootConfigurationId": rec.BootConfigurationID, "hostname": rec.Hostname},
			})
		}
		if lifecycleRegistration != nil {
			if err := lifecycleRegistration.BindBoot(ctx, session.ExpectedBootID); err != nil {
				return result, err
			}
		}
		if runEnv.Record.Mode == environment.ModeShared {
			if err := bindRunWorkspaceIncarnation(&runSession, lifecycleRegistration.Incarnation()); err != nil {
				return result, err
			}
			if err := opts.ActivateWorkspaceAttachment(&runSession); err != nil {
				return result, fmt.Errorf("activate shared workspace view with daemon session owner: %w", err)
			}
			runSession.WorkspaceAttachment.State = workspaceattach.AttachmentViewMounting
		}
		controller, _ := opts.Backend.(backend.EnvironmentNetworkServiceController)
		if lifecycleRegistration != nil && runNetwork.EnvironmentService {
			lifecycleNetworkRef, err = lifecycleRegistration.Register(ctx, lifecycle.RegistrationSpec{
				Kind: lifecycle.KindNetworkService, ID: runEnv.Record.ID,
				OwnerKind: "manager", OwnerID: runEnv.Record.ID, State: lifecycle.StatePlanned,
				Dependencies: []lifecycle.DependencySpec{{Ref: lifecycleRegistration.Root(), StopMode: lifecycle.StopModeDrain}},
				Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain,
				PossibleVMDependency: true,
			})
			if err != nil {
				return result, err
			}
		}
		if err := c.StartRunNetworkService(ctx, runSession, &runNetwork, controller, session, dataPlane.Env); err != nil {
			return result, err
		}
		if lifecycleRegistration != nil && lifecycleNetworkRef.Kind != "" {
			if err := activateLifecycleResource(ctx, lifecycleRegistration, lifecycleNetworkRef); err != nil {
				return result, err
			}
		}
		var startErr error
		runEnv, startErr = c.StartRunEnvironment(runEnv, runSession.Layout.ID, plan.Command)
		if startErr != nil {
			return result, startErr
		}
		if err := owner.Update(runsession.OwnerStateRunning, ""); err != nil {
			return result, err
		}
		if lifecycleRegistration != nil {
			if err := lifecycleRegistration.Transition(ctx, lifecycleRegistration.Session(), lifecycle.StateActive); err != nil {
				return result, err
			}
		}
		if err := transitionLock.Unlock(); err != nil {
			transitionLock = nil
			return result, err
		}
		transitionLock = nil
	}
	previewCtx, cancelPreview := context.WithCancel(ctx)
	previewEvents := startRunPreviews(previewCtx, runSession, dataPlane, opener)
	var runErr error
	deferSharedReady := lifecycleRegistration != nil && runEnv.Record.Mode == environment.ModeShared
	if lifecycleRegistration != nil && !deferSharedReady {
		if err := activateLifecycleResource(ctx, lifecycleRegistration, lifecycleSupervisorRef); err != nil {
			cancelPreview()
			return result, err
		}
		if err := activateLifecycleResource(ctx, lifecycleRegistration, lifecycleTargetRef); err != nil {
			cancelPreview()
			return result, err
		}
	}
	if opts.Streams != nil {
		streamRunner, ok := opts.Backend.(backend.StreamRunner)
		if !ok {
			runErr = errors.New("backend does not support daemon run streams")
		} else {
			streams := *opts.Streams
			if deferSharedReady {
				downstreamReady := streams.Ready
				readyActivated := false
				streams.Ready = func(proof backend.SessionReadyProof) error {
					if readyActivated {
						return errors.New("shared workspace ready barrier was entered more than once")
					}
					if err := proof.ValidateSession(session, true); err != nil {
						return err
					}
					if err := activateLifecycleResource(ctx, lifecycleRegistration, workspaceLifecycleRefs.View); err != nil {
						return err
					}
					if err := activateLifecycleResource(ctx, lifecycleRegistration, lifecycleSupervisorRef); err != nil {
						return err
					}
					if err := activateLifecycleResource(ctx, lifecycleRegistration, lifecycleTargetRef); err != nil {
						return err
					}
					runSession.WorkspaceAttachment.State = workspaceattach.AttachmentReady
					if err := downstreamReady(proof); err != nil {
						return err
					}
					readyActivated = true
					return nil
				}
				runErr = streamRunner.RunWithStreams(ctx, session, plan.Command, dataPlane.Env, streams)
			} else {
				runErr = streamRunner.RunWithStreams(ctx, session, plan.Command, dataPlane.Env, streams)
			}
		}
	} else {
		runErr = opts.Backend.Run(ctx, session, plan.Command, dataPlane.Env)
	}
	runErr = runtimeRunError(runEnv, runErr)
	cancelPreview()
	var previewAuditErr error
	for _, event := range <-previewEvents {
		if err := runSession.Audit.Emit(event); err != nil && previewAuditErr == nil {
			previewAuditErr = err
		}
	}
	decision := "allow"
	if runErr != nil {
		decision = "error"
		result.Error = runErr.Error()
	} else if previewAuditErr != nil {
		decision = "error"
		result.Error = previewAuditErr.Error()
	}
	_ = runSession.Audit.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  plan.ProfileName,
		Backend:  plan.Backend,
		Action:   "session.end",
		Decision: decision,
		Details:  sessionEndDetails(plan.Command, runErr),
	})
	if runErr != nil {
		return result, runErr
	}
	if previewAuditErr != nil {
		return result, previewAuditErr
	}
	return result, nil
}

func lifecycleObservationForAttach(ctx context.Context, registrar lifecycle.Registrar, runBackend backend.Backend, environmentID, instanceName string) (backend.LifecycleObservation, error) {
	if cached, ok := registrar.(lifecycle.ActiveAttachObservationProvider); ok {
		if observation, available := cached.ActiveAttachObservation(ctx, environmentID, instanceName); available {
			return observation, nil
		}
	}
	observer, ok := runBackend.(backend.LifecycleObserver)
	if !ok {
		return backend.LifecycleObservation{}, errors.New("lima backend does not provide lifecycle observation")
	}
	return observer.ObserveLifecycle(ctx, instanceName), nil
}

func ownerCommandClass(command string) string {
	command = filepath.Base(strings.TrimSpace(command))
	if command == "." || command == "" || len(command) > 128 {
		return "command"
	}
	for _, r := range command {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._+-", r) {
			continue
		}
		return "command"
	}
	return command
}

func appendCleanupError(current string, err error) string {
	if err == nil {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return err.Error()
	}
	return current + "; " + err.Error()
}

func liveOwnerIDs(root string) (map[string]bool, error) {
	owners, err := runsession.ListOwners(root)
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(owners))
	for _, owner := range owners {
		switch owner.Status {
		case runsession.OwnerLive:
			live[owner.SessionID] = true
		case runsession.OwnerUnprovable:
			return nil, fmt.Errorf("session %s: %w", owner.SessionID, runsession.ErrOwnerUnprovable)
		}
	}
	return live, nil
}

func requireAttachableEnvironmentOwners(store environment.Store, environmentID string) error {
	owners, err := runsession.ListOwners(store.OwnerRoot(environmentID))
	if err != nil {
		return &EnvironmentOwnerError{Code: recovery.CodeSessionOwnerUnprovable, EnvironmentID: environmentID, Err: err}
	}
	for _, owner := range owners {
		if owner.Status == runsession.OwnerUnprovable {
			return &EnvironmentOwnerError{
				Code: recovery.CodeSessionOwnerUnprovable, EnvironmentID: environmentID,
				Err: fmt.Errorf("session %s: %w", owner.SessionID, runsession.ErrOwnerUnprovable),
			}
		}
		if owner.Status == runsession.OwnerStale || owner.Record.State == runsession.OwnerStateCleaning || owner.Record.State == runsession.OwnerStateFailed {
			return &EnvironmentOwnerError{
				Code: recovery.CodeSessionCleanupFailed, EnvironmentID: environmentID,
				Err: fmt.Errorf("session %s requires explicit recovery before a new attach: %w", owner.SessionID, runsession.ErrOwnerCleanupFailed),
			}
		}
	}
	return nil
}

func startRunPreviews(ctx context.Context, runSession RunSession, dataPlane RunDataPlane, opener broker.Opener) <-chan []audit.Event {
	out := make(chan []audit.Event, 1)
	if !hasRunPreviews(dataPlane) {
		out <- nil
		close(out)
		return out
	}
	go func() {
		defer close(out)
		timer := time.NewTimer(previewOpenDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			out <- nil
			return
		case <-timer.C:
		}
		out <- openRunPreviewEvents(ctx, runSession, dataPlane, opener)
	}()
	return out
}

func hasRunPreviews(dataPlane RunDataPlane) bool {
	for _, endpoint := range dataPlane.PortBridges {
		if endpoint.Action == policy.ActionEndpointExposeHostToGuest && endpoint.Owner == OpenTargetPreviewOpen {
			return true
		}
	}
	return false
}

func openRunPreviewEvents(ctx context.Context, runSession RunSession, dataPlane RunDataPlane, opener broker.Opener) []audit.Event {
	if opener == nil {
		opener = broker.NoopOpener{}
	}
	var events []audit.Event
	for _, endpoint := range dataPlane.PortBridges {
		if endpoint.Action != policy.ActionEndpointExposeHostToGuest || endpoint.Owner != OpenTargetPreviewOpen {
			continue
		}
		target, err := previewURLForEndpoint(endpoint.ListenAddress)
		if err != nil {
			events = append(events, audit.Event{
				Session:  runSession.Layout.ID,
				Profile:  runSession.Plan.ProfileName,
				Backend:  runSession.Plan.Backend,
				Action:   "preview.open",
				Decision: "error",
				Details: map[string]any{
					"status":           "error",
					"owner":            endpoint.Owner,
					"source":           endpoint.Source,
					"endpointCategory": endpoint.EndpointCategory,
					"endpoint":         "present",
					"reason":           err.Error(),
				},
			})
			continue
		}
		if err := waitForPreviewHTTPReady(ctx, target); err != nil {
			events = append(events, audit.Event{
				Session:  runSession.Layout.ID,
				Profile:  runSession.Plan.ProfileName,
				Backend:  runSession.Plan.Backend,
				Action:   "preview.open",
				Decision: "error",
				Details: map[string]any{
					"status":           "error",
					"owner":            endpoint.Owner,
					"source":           endpoint.Source,
					"endpointCategory": endpoint.EndpointCategory,
					"endpoint":         "present",
					"reason":           err.Error(),
				},
			})
			continue
		}
		if err := opener.OpenURL(ctx, target); err != nil {
			events = append(events, audit.Event{
				Session:  runSession.Layout.ID,
				Profile:  runSession.Plan.ProfileName,
				Backend:  runSession.Plan.Backend,
				Action:   "preview.open",
				Decision: "error",
				Details: map[string]any{
					"status":           "error",
					"owner":            endpoint.Owner,
					"source":           endpoint.Source,
					"endpointCategory": endpoint.EndpointCategory,
					"endpoint":         "present",
					"reason":           err.Error(),
				},
			})
			continue
		}
		events = append(events, audit.Event{
			Session:  runSession.Layout.ID,
			Profile:  runSession.Plan.ProfileName,
			Backend:  runSession.Plan.Backend,
			Action:   "preview.open",
			Decision: string(policy.Allow),
			Details: map[string]any{
				"status":           "ok",
				"owner":            endpoint.Owner,
				"source":           endpoint.Source,
				"endpointCategory": endpoint.EndpointCategory,
				"endpoint":         "present",
			},
		})
	}
	return events
}

func previewURLForEndpoint(listenAddress string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", fmt.Errorf("preview endpoint listen address is invalid: %w", err)
	}
	if host == "" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/", nil
}

func waitForPreviewHTTPReady(ctx context.Context, target string) error {
	readyCtx, cancel := context.WithTimeout(ctx, previewOpenReadyTimeout)
	defer cancel()
	client := &http.Client{
		Timeout: previewOpenRequestTimeout,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for {
		req, err := http.NewRequestWithContext(readyCtx, http.MethodGet, target, nil)
		if err != nil {
			return errors.New("preview endpoint URL is invalid")
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			return nil
		}
		select {
		case <-readyCtx.Done():
			if ctx.Err() != nil {
				return errors.New("preview endpoint wait canceled")
			}
			return errors.New("preview endpoint did not become ready before timeout")
		case <-time.After(previewOpenReadyPoll):
		}
	}
}

func (c Core) runSpec(runSession RunSession, runEnv RunEnvironment, dataPlane RunDataPlane, runNetwork RunNetwork) backend.RunSpec {
	networkBootstrapPath := runNetwork.Plan.BootstrapPath
	networkBootstrapGuestPath := runNetwork.Plan.GuestBootstrapPath
	networkCleanupPath := runNetwork.Plan.CleanupPath
	networkCleanupGuestPath := runNetwork.Plan.GuestCleanupPath
	if runNetwork.EnvironmentService {
		// The environment service controller owns VM-global setup. The target
		// namespace must never start or stop it as session-local authority.
		networkBootstrapPath = ""
		networkBootstrapGuestPath = ""
		networkCleanupPath = ""
		networkCleanupGuestPath = ""
	}
	machineMode := environment.ModeWorkspaceBound
	workspaceTransport := backend.WorkspaceTransportStatic
	if runEnv.Active {
		machineMode = runEnv.Record.Mode
		if machineMode == environment.ModeShared {
			workspaceTransport = backend.WorkspaceTransportPortal
		}
	}
	workspace := backend.WorkspaceAttachmentSpec{
		HostRoot: runSession.Plan.Workspace, GuestRoot: runSession.Plan.GuestWorkspace, Transport: workspaceTransport,
	}
	if machineMode == environment.ModeShared && runSession.WorkspacePortal != nil {
		workspace.Portal = &backend.WorkspacePortalBinding{
			PhysicalGuestRoot:   runSession.WorkspaceAttachment.PhysicalGuestRoot,
			Endpoint:            runSession.WorkspacePortal.Endpoint,
			CredentialGuestPath: runSession.WorkspacePortal.CredentialGuestPath,
		}
	}
	return backend.RunSpec{
		Machine: backend.MachineActivationSpec{
			EnvironmentID: runEnv.Record.ID, ImageRef: runImageRef(runEnv, runSession.Plan.RuntimeProfile),
			Profile: runSession.Plan.RuntimeProfile, ProfileDir: runSession.ProfileDir,
			IdentityMode: IdentityMode(runSession.Plan), IdentityRoot: runSession.IdentityDir,
			RuntimeRoot: runEnv.RuntimeDir, InstanceName: runEnv.InstanceName,
			PreserveInstance: runEnv.PreserveInstance, Mode: machineMode,
			Runtime: runtimePresentation(runSession.Plan.RuntimeProfile),
		},
		Workspace:                 workspace,
		SessionID:                 runSession.Layout.ID,
		Command:                   append([]string(nil), runSession.Plan.Command...),
		Env:                       append([]string(nil), dataPlane.Env...),
		GuestHome:                 filepath.Join(runSession.IdentityDir, "home"),
		ShimDir:                   runSession.RuntimeShimDir,
		SessionDir:                runSession.RuntimeSessionDir,
		SessionIsolationRequired:  runEnv.Active && runSession.Plan.Backend == "lima",
		TargetUser:                runSession.Plan.RuntimeProfile.Identity.User,
		Broker:                    dataPlane.BrokerGuestEndpoint,
		NetworkBootstrapPath:      networkBootstrapPath,
		NetworkBootstrapGuestPath: networkBootstrapGuestPath,
		NetworkCleanupPath:        networkCleanupPath,
		NetworkCleanupGuestPath:   networkCleanupGuestPath,
		HostFSEnabled:             dataPlane.HostFSEnabled,
		HostFSGrafts:              append([]string(nil), dataPlane.HostFSGrafts...),
		PortBridges:               append([]backend.PortBridgeEndpoint(nil), dataPlane.PortBridges...),
		AuditPath:                 runSession.AuditPath,
		NetworkPrivilegedSetup:    runNetwork.Plan.Mode == netpolicy.ModeTun2Socks,
		PrivilegedSetupRequired:   runNetwork.Plan.Mode == netpolicy.ModeTun2Socks || dataPlane.HostFSEnabled,
		PrivilegeStatusSink: func(status privilege.Status) error {
			if dataPlane.Broker != nil {
				dataPlane.Broker.SetGuestPrivilegeStatus(status)
			}
			auditErr := runSession.Audit.Emit(audit.Event{
				Session:  runSession.Layout.ID,
				Profile:  runSession.Plan.ProfileName,
				Backend:  runSession.Plan.Backend,
				Action:   privilege.ActionGuestPrivilegeStatus,
				Decision: "allow",
				Details:  privilege.StatusDetails(status),
			})
			var noticeErr error
			if status.Status != "enforced" {
				_, noticeErr = c.CreateNotice(decision.Notice{
					ID:       "privilege-" + runSession.Layout.ID,
					Kind:     decision.KindPrivilegeStatus,
					Severity: privilegeNoticeSeverity(status),
					Status:   string(status.Status),
					Source: decision.Source{
						Profile: runSession.Plan.ProfileName,
						Session: runSession.Layout.ID,
						Backend: runSession.Plan.Backend,
					},
					Payload:  privilege.StatusDetails(status),
					Preview:  decision.Preview{Summary: "guest privilege status is " + string(status.Status), Facts: privilege.StatusDetails(status)},
					AuditRef: "audit:privilege:" + runSession.Layout.ID,
				})
			}
			return errors.Join(auditErr, noticeErr)
		},
		PrivilegedSetupEventSink: func(event backend.PrivilegedSetupEvent) error {
			action := event.Action
			if action == "" {
				action = privilege.ActionPrivilegedSetup
			}
			decision := "allow"
			if event.Status == "failed" {
				decision = "error"
			}
			emitErr := runSession.Audit.Emit(audit.Event{
				Session:  runSession.Layout.ID,
				Profile:  runSession.Plan.ProfileName,
				Backend:  runSession.Plan.Backend,
				Action:   action,
				Decision: decision,
				Details:  privilege.PrivilegedSetupDetails(event.Category, event.Status, event.Setup, event.Reason),
			})
			// Privileged cleanup runs from the deferred run finalization, which by
			// design executes after CloseRunSession has closed the per-session audit
			// (see the deferred cleanup ordering). Rather than drop the teardown
			// record (which would leave a one-sided audit trail — setup logged,
			// teardown not — and make a misbehaving privileged teardown unauditable),
			// route it to the durable operator-center audit, which outlives the
			// per-session writer. Best-effort: a closed session writer or a
			// durable-sink error must not fail cleanup that already succeeded.
			if action == privilege.ActionPrivilegedCleanup && errors.Is(emitErr, audit.ErrWriterClosed) {
				_ = c.emitOperatorCenterAudit(audit.Event{
					Session:  runSession.Layout.ID,
					Profile:  runSession.Plan.ProfileName,
					Backend:  runSession.Plan.Backend,
					Action:   action,
					Decision: decision,
					Details:  privilege.PrivilegedSetupDetails(event.Category, event.Status, event.Setup, event.Reason),
				})
				return nil
			}
			return emitErr
		},
	}
}

func runtimePresentation(p profile.Profile) *backend.RuntimePresentation {
	if p.Environment.Runtime == nil {
		return nil
	}
	value := p.Environment.Runtime
	return &backend.RuntimePresentation{
		Family: value.Family, Revision: value.Revision, Maturity: value.Maturity,
		DownloadBytes: value.DownloadBytes,
	}
}

func privilegeNoticeSeverity(status privilege.Status) string {
	switch status.Status {
	case "degraded":
		return decision.NoticeSeverityWarning
	case "unknown":
		return decision.NoticeSeverityError
	default:
		return decision.NoticeSeverityInfo
	}
}

func validateRunPolicy(plan RunPlan) error {
	evaluator := policy.NewEvaluator(plan.RuntimeProfile)
	if _, err := evaluator.Validate(policy.Proposal{
		Decision:  policy.AuditOnly,
		Route:     policy.GuestDirect,
		Action:    "guest.exec",
		Resources: []string{"guest-command:" + plan.Command[0]},
		Reason:    "top-level command execution",
	}); err != nil {
		return err
	}
	if _, err := evaluator.Validate(networkConnectProposal(plan.RuntimeProfile.Network.Mode, "session network setup")); err != nil {
		return err
	}
	return nil
}

// runImageRef resolves the base image declaration for backend preparation:
// the environment's pinned declaration, or the profile default for
// record-less disposable and ephemeral runs.
func runImageRef(runEnv RunEnvironment, p profile.Profile) string {
	if strings.TrimSpace(runEnv.Record.ImageRef) != "" {
		return runEnv.Record.ImageRef
	}
	return p.BaseImageOrBuiltin()
}

func emitRunSetupAudit(aw *audit.Writer, runSession RunSession, opts ApplyRunOptions, storeRoot string) error {
	hostFSProfile := HostFSProfileForRun(runSession.Plan.RuntimeProfile, opts.DisableProfileHostFSGrants)
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile, Run: opts.HostFSRun, StoreRoot: storeRoot, Home: opts.OperatorHome})
	if err != nil {
		return err
	}
	requestedBackend := opts.RequestedBackend
	if requestedBackend == "" {
		requestedBackend = "auto"
	}
	env := runSession.Env
	plan := runSession.Plan
	var workspaceAuthority runSessionWorkspaceAuthority
	if runSession.Environment.Active {
		workspaceAuthority, err = workspaceAuthorityForRunSession(runSession)
		if err != nil {
			return err
		}
	}
	for _, event := range []audit.Event{
		{
			Session:  runSession.Layout.ID,
			Profile:  plan.ProfileName,
			Backend:  plan.Backend,
			Action:   "backend.selected",
			Decision: "allow",
			Details: map[string]any{
				"requested":          requestedBackend,
				"resolved":           plan.Backend,
				"weakIsolation":      plan.Backend == "native",
				"allowWeakIsolation": opts.AllowWeakIsolation,
			},
		},
		{
			Session:  runSession.Layout.ID,
			Profile:  plan.ProfileName,
			Backend:  plan.Backend,
			Action:   "hostfs.policy",
			Decision: "audit-only",
			Details: map[string]any{
				"roots":                 []string{"/hideout/hostfs", "/Users", "/Volumes", "/private"},
				"default":               "hidden",
				"profileGrants":         len(hostFSProfile.Grants),
				"profileGrantsDisabled": opts.DisableProfileHostFSGrants,
				"runGrants":             len(opts.HostFSRun.Grants),
				"runDenyRules":          len(opts.HostFSRun.Deny),
				"totalGrants":           len(hostFSPolicy.Grants),
				"denyRules":             len(hostFSPolicy.Deny),
				"write":                 "unsupported",
				"dataPlane":             hostFSDataPlaneAuditState(plan.Backend, len(hostFSPolicy.Grants) > 0),
			},
		},
		{
			Session:  runSession.Layout.ID,
			Profile:  plan.ProfileName,
			Backend:  plan.Backend,
			Action:   "workspace.mapping",
			Decision: "allow",
			Details: map[string]any{
				"environmentId":    runSession.Environment.Record.ID,
				"workspaceId":      workspaceAuthority.WorkspaceID,
				"host":             plan.Workspace,
				"guest":            plan.GuestWorkspace,
				"mode":             plan.RuntimeProfile.Workspace.Mode,
				"pathMode":         plan.RuntimeProfile.Workspace.PathMode,
				"readWrite":        plan.RuntimeProfile.Workspace.Mode == "read-write",
				"workspaceVisible": true,
			},
		},
		{
			Session:  runSession.Layout.ID,
			Profile:  plan.ProfileName,
			Backend:  plan.Backend,
			Action:   "env.policy",
			Decision: "allow",
			Details: map[string]any{
				"synthetic":        sortedMapKeys(env.Synthetic),
				"inherited":        append([]string(nil), env.Inherited...),
				"deniedObserved":   append([]string(nil), env.Denied...),
				"deniedPatterns":   append([]string(nil), plan.RuntimeProfile.Env.Deny...),
				"public":           sortedMapKeys(plan.RuntimeProfile.Env.Public),
				"proxyEnv":         "absent",
				"identityMode":     IdentityMode(plan),
				"identityId":       plan.RuntimeProfile.Metadata["identityId"],
				"lineageMode":      plan.RuntimeProfile.Metadata["lineageMode"],
				"sourceIdentityId": plan.RuntimeProfile.Metadata["sourceIdentityId"],
			},
		},
		{
			Session:  runSession.Layout.ID,
			Profile:  plan.ProfileName,
			Backend:  plan.Backend,
			Action:   "command.start",
			Decision: "audit-only",
			Details: map[string]any{
				"program":   commandProgram(plan.Command),
				"argv":      append([]string(nil), plan.Command...),
				"route":     "guest-direct",
				"authority": "guest.exec",
				"topLevel":  true,
			},
		},
	} {
		if err := aw.Emit(event); err != nil {
			return err
		}
	}
	return nil
}

func sessionEndDetails(command []string, runErr error) map[string]any {
	details := map[string]any{
		"command": strings.Join(command, " "),
	}
	if runErr != nil {
		details["error"] = runErr.Error()
	}
	return details
}

func hostFSDataPlaneAuditState(backendName string, enabled bool) string {
	if !enabled {
		return "inactive"
	}
	if backendName == "lima" {
		return "lima-fuse"
	}
	return "not-mounted"
}

func commandProgram(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return command[0]
}

func IdentityMode(plan RunPlan) string {
	if plan.Ephemeral {
		return "ephemeral"
	}
	return "persistent"
}

func networkConnectProposal(mode, reason string) policy.Proposal {
	if mode == "" {
		mode = netpolicy.ModeDirect
	}
	return policy.Proposal{
		Decision:  policy.AuditOnly,
		Route:     policy.GuestDirect,
		Action:    "network.connect",
		Resources: []string{"network:" + mode},
		Reason:    reason,
	}
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
