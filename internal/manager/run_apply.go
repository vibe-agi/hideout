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
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostfs"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	RunResultVersion          = "hideout.run-result/v1"
	previewOpenDelay          = 250 * time.Millisecond
	previewOpenReadyTimeout   = 30 * time.Second
	previewOpenReadyPoll      = 100 * time.Millisecond
	previewOpenRequestTimeout = 750 * time.Millisecond
)

type ApplyRunOptions struct {
	Backend                    backend.Backend
	RequestedBackend           string
	AllowWeakIsolation         bool
	Environment                RunEnvironmentOptions
	AuditPath                  string
	HostFSRun                  hostfs.Config
	DisableProfileHostFSGrants bool
	PortBridges                []RunPortBridgeRequest
	OpenTargets                []RunOpenTargetOwner
	EndpointCandidates         []RunEndpointCandidate
	EndpointExposures          []RunEndpointExposureRequest
	Network                    RunNetworkOptions
	Opener                     broker.Opener
	OpenerForSession           func(RunSession) broker.Opener
}

type RunResult struct {
	Version          string           `json:"version"`
	SessionID        string           `json:"sessionId"`
	Profile          string           `json:"profile"`
	Backend          string           `json:"backend"`
	EnvironmentID    string           `json:"environmentId,omitempty"`
	EnvironmentName  string           `json:"environmentName,omitempty"`
	InstanceName     string           `json:"instanceName,omitempty"`
	PreserveInstance bool             `json:"preserveInstance,omitempty"`
	AuditPath        string           `json:"auditPath,omitempty"`
	BoundarySummary  *BoundarySummary `json:"boundarySummary,omitempty"`
	Command          []string         `json:"command"`
	Error            string           `json:"error,omitempty"`
	CleanupError     string           `json:"cleanupError,omitempty"`
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
	if runEnv.Active {
		envLock, err := (environment.Store{Root: c.Store.Root}).Lock(runEnv.Record.ID)
		if err != nil {
			return result, err
		}
		defer func() {
			if unlockErr := envLock.Unlock(); unlockErr != nil && retErr == nil {
				result.Error = unlockErr.Error()
				retErr = unlockErr
			}
		}()
	}
	runSession, err := c.BeginRunSession(plan, runEnv, RunSessionOptions{})
	if err != nil {
		return result, err
	}
	result.SessionID = runSession.Layout.ID
	result.AuditPath = runSession.AuditPath
	runEnv = runSession.Environment
	defer func() {
		_, closeErr := c.CloseRunSession(runSession)
		summary := SummarizeRunBoundary(result.AuditPath)
		result.BoundarySummary = &summary
		if closeErr != nil && retErr == nil {
			result.Error = closeErr.Error()
			retErr = closeErr
		}
	}()
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
	if err := emitRunSetupAudit(runSession.Audit, runSession, opts, c.Store.Root); err != nil {
		return result, err
	}
	runNetwork, netErr := c.PrepareRunNetwork(runSession, opts.Network)
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
		Backend:                    opts.Backend,
		PortBridges:                opts.PortBridges,
		OpenTargets:                opts.OpenTargets,
		EndpointCandidates:         opts.EndpointCandidates,
		EndpointExposures:          opts.EndpointExposures,
		Opener:                     opener,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := c.CloseRunDataPlane(dataPlane); closeErr != nil && retErr == nil {
			result.Error = closeErr.Error()
			retErr = closeErr
		}
	}()
	session, err := opts.Backend.Prepare(ctx, runSpec(runSession, runEnv, dataPlane, runNetwork))
	if err != nil {
		return result, err
	}
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
		runEnv, cleanupErr = c.FinishRunEnvironment(runEnv, cleanupErr)
		if cleanupErr != nil {
			decision = "error"
			details["error"] = cleanupErr.Error()
			result.CleanupError = cleanupErr.Error()
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
		var startErr error
		runEnv, startErr = c.StartRunEnvironment(runEnv, runSession.Layout.ID, plan.Command)
		if startErr != nil {
			return result, startErr
		}
	}
	previewCtx, cancelPreview := context.WithCancel(ctx)
	previewEvents := startRunPreviews(previewCtx, runSession, dataPlane, opener)
	runErr := opts.Backend.Run(ctx, session, plan.Command, dataPlane.Env)
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

func runSpec(runSession RunSession, runEnv RunEnvironment, dataPlane RunDataPlane, runNetwork RunNetwork) backend.RunSpec {
	return backend.RunSpec{
		SessionID:                 runSession.Layout.ID,
		EnvironmentID:             runEnv.Record.ID,
		ImageRef:                  runImageRef(runEnv, runSession.Plan.RuntimeProfile),
		Profile:                   runSession.Plan.RuntimeProfile,
		Command:                   append([]string(nil), runSession.Plan.Command...),
		Env:                       append([]string(nil), dataPlane.Env...),
		HostWork:                  runSession.Plan.Workspace,
		GuestWork:                 runSession.Plan.GuestWorkspace,
		GuestHome:                 filepath.Join(runSession.IdentityDir, "home"),
		ShimDir:                   runSession.RuntimeShimDir,
		ProfileDir:                runSession.ProfileDir,
		IdentityMode:              IdentityMode(runSession.Plan),
		IdentityRoot:              runSession.IdentityDir,
		SessionDir:                runSession.RuntimeSessionDir,
		Broker:                    dataPlane.BrokerGuestEndpoint,
		NetworkBootstrapPath:      runNetwork.Plan.BootstrapPath,
		NetworkBootstrapGuestPath: runNetwork.Plan.GuestBootstrapPath,
		NetworkCleanupPath:        runNetwork.Plan.CleanupPath,
		NetworkCleanupGuestPath:   runNetwork.Plan.GuestCleanupPath,
		HostFSEnabled:             dataPlane.HostFSEnabled,
		HostFSGrafts:              append([]string(nil), dataPlane.HostFSGrafts...),
		PortBridges:               append([]backend.PortBridgeEndpoint(nil), dataPlane.PortBridges...),
		InstanceName:              runEnv.InstanceName,
		PreserveInstance:          runEnv.PreserveInstance,
		AuditPath:                 runSession.AuditPath,
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
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile, Run: opts.HostFSRun, StoreRoot: storeRoot})
	if err != nil {
		return err
	}
	requestedBackend := opts.RequestedBackend
	if requestedBackend == "" {
		requestedBackend = "auto"
	}
	env := runSession.Env
	plan := runSession.Plan
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
