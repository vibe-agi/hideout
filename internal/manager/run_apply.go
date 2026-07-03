package manager

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/hostfs"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

const RunResultVersion = "hideout.run-result/v1"

type ApplyRunOptions struct {
	Backend                    backend.Backend
	RequestedBackend           string
	AllowWeakIsolation         bool
	Environment                RunEnvironmentOptions
	AuditPath                  string
	HostFSRun                  hostfs.Config
	DisableProfileHostFSGrants bool
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
	if err := emitRunSetupAudit(runSession.Audit, runSession, opts); err != nil {
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
	runErr := opts.Backend.Run(ctx, session, plan.Command, dataPlane.Env)
	decision := "allow"
	if runErr != nil {
		decision = "error"
		result.Error = runErr.Error()
	}
	_ = runSession.Audit.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  plan.ProfileName,
		Backend:  plan.Backend,
		Action:   "session.end",
		Decision: decision,
		Details:  sessionEndDetails(plan.Command, runErr),
	})
	return result, runErr
}

func runSpec(runSession RunSession, runEnv RunEnvironment, dataPlane RunDataPlane, runNetwork RunNetwork) backend.RunSpec {
	return backend.RunSpec{
		SessionID:                 runSession.Layout.ID,
		EnvironmentID:             runEnv.Record.ID,
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

func emitRunSetupAudit(aw *audit.Writer, runSession RunSession, opts ApplyRunOptions) error {
	hostFSProfile := HostFSProfileForRun(runSession.Plan.RuntimeProfile, opts.DisableProfileHostFSGrants)
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile, Run: opts.HostFSRun})
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
