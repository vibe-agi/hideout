package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

const (
	RunServiceRequestVersion = "hideout.run-request/v1"
	RunReviewVersion         = "hideout.run-review/v1"
	maxRunTERMBytes          = 64
)

var ErrRunPlanStale = errors.New("run plan changed after review")

// TerminalDescriptor is the transport-neutral terminal state selected by the
// client. Host file descriptors and ambient terminal state never enter Core.
type TerminalDescriptor struct {
	Mode    runsession.TerminalMode `json:"mode"`
	Rows    uint16                  `json:"rows,omitempty"`
	Columns uint16                  `json:"columns,omitempty"`
	TERM    string                  `json:"term,omitempty"`
}

// RunConfirmation binds an operator decision to the exact review that was
// displayed. It never acts as an acknowledgement for a different plan.
type RunConfirmation struct {
	PlanVersion string `json:"planVersion"`
	PlanDigest  string `json:"planDigest"`
	Accepted    bool   `json:"accepted"`
}

// RunServiceRequest is the canonical executable run intent shared by the
// daemon session transport and the Manager HTTP adapter.
type RunServiceRequest struct {
	Version                    string                       `json:"version"`
	ProfileName                string                       `json:"profile,omitempty"`
	Backend                    string                       `json:"backend,omitempty"`
	NetworkMode                string                       `json:"networkMode,omitempty"`
	ProxySecretRef             string                       `json:"proxySecretRef,omitempty"`
	MediatedResolver           string                       `json:"mediatedResolver,omitempty"`
	Workspace                  string                       `json:"workspace,omitempty"`
	GuestWorkspace             string                       `json:"guestWorkspace,omitempty"`
	AllowUnsafeWorkspace       bool                         `json:"allowUnsafeWorkspace,omitempty"`
	AllowWeakIsolation         bool                         `json:"allowWeakIsolation,omitempty"`
	Ephemeral                  bool                         `json:"ephemeral,omitempty"`
	EnvironmentName            string                       `json:"environmentName,omitempty"`
	RemoveEnvironment          bool                         `json:"removeEnvironment,omitempty"`
	Command                    []string                     `json:"command"`
	PublicEnv                  map[string]string            `json:"publicEnv,omitempty"`
	AuditPath                  string                       `json:"auditPath,omitempty"`
	HostFSRun                  hostfs.Config                `json:"hostfs,omitempty"`
	DisableProfileHostFSGrants bool                         `json:"disableProfileHostFSGrants,omitempty"`
	OpenTargets                []RunOpenTargetOwner         `json:"openTargets,omitempty"`
	EndpointCandidates         []RunEndpointCandidate       `json:"endpointCandidates,omitempty"`
	EndpointExposures          []RunEndpointExposureRequest `json:"endpointExposures,omitempty"`
	PreviewTargets             []string                     `json:"previewTargets,omitempty"`
	Terminal                   TerminalDescriptor           `json:"terminal"`
	Confirmation               *RunConfirmation             `json:"confirmation,omitempty"`
}

type RunReview struct {
	Version              string      `json:"version"`
	PlanVersion          string      `json:"planVersion"`
	PlanDigest           string      `json:"planDigest"`
	RequiresConfirmation bool        `json:"requiresConfirmation"`
	Profile              string      `json:"profile"`
	Backend              string      `json:"backend"`
	Workspace            string      `json:"workspace"`
	GuestWorkspace       string      `json:"guestWorkspace"`
	NetworkMode          string      `json:"networkMode"`
	Command              []string    `json:"command"`
	Notices              []RunNotice `json:"notices,omitempty"`
}

type RunNotice struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

// PreparedRun is intentionally not a wire object. The public review is safe
// to send to a client; the full plan retains profile policy inside Manager.
type PreparedRun struct {
	Review RunReview
	Plan   RunPlan
	// Request is the Manager-normalized effective request. It may contain
	// profile-derived endpoint bindings that the thin client is not trusted to
	// manufacture.
	Request RunServiceRequest
}

type RunServiceDependencies struct {
	Backend          backend.Backend
	OpenerForSession func(RunSession) broker.Opener
	Streams          *backend.RunStreams
	Lifecycle        lifecycle.Registrar
}

type RunServiceBackendFactory func(RunServiceRequest, RunPlan) (backend.Backend, error)
type RunServiceOpenerFactory func(RunServiceRequest, RunPlan, RunSession) broker.Opener

// RunService centralizes run planning, review binding, revalidation, and
// application for every executable host-facing adapter.
type RunService struct {
	Core Core
}

func (s RunService) Prepare(req RunServiceRequest) (PreparedRun, error) {
	req, err := normalizeRunServiceRequest(req)
	if err != nil {
		return PreparedRun{}, err
	}
	plan, err := s.Core.PlanRun(runPlanOptionsFromServiceRequest(req))
	if err != nil {
		return PreparedRun{}, err
	}
	if err := applyRunPublicEnv(&plan, req.PublicEnv); err != nil {
		return PreparedRun{}, err
	}
	req, err = resolveRunServicePreviewTargets(plan, req)
	if err != nil {
		return PreparedRun{}, err
	}
	if err := validateRunIntegrationRequests(plan, req); err != nil {
		return PreparedRun{}, err
	}
	digest, err := digestPreparedRun(plan, req)
	if err != nil {
		return PreparedRun{}, err
	}
	return PreparedRun{
		Plan: plan, Request: req,
		Review: RunReview{
			Version: RunReviewVersion, PlanVersion: plan.Version,
			PlanDigest: digest, RequiresConfirmation: runRequiresConfirmation(plan, req),
			Profile: plan.ProfileName, Backend: plan.Backend,
			Workspace: plan.Workspace, GuestWorkspace: plan.GuestWorkspace,
			NetworkMode: plan.NetworkMode, Command: append([]string(nil), plan.Command...),
			Notices: runServiceNotices(plan),
		},
	}, nil
}

func runServiceNotices(plan RunPlan) []RunNotice {
	rules := hostfs.WorkspaceShadowedRules(plan.RuntimeProfile.HostFS, plan.Workspace)
	if len(rules) == 0 {
		return nil
	}
	out := make([]RunNotice, 0, len(rules))
	for _, rule := range rules {
		out = append(out, RunNotice{
			Code:    "hostfs.rule.workspace-shadowed",
			Summary: fmt.Sprintf("hostfs rule %s (%s) is shadowed by the workspace %s and has no effect inside it", rule.ID, rule.HostPath, plan.Workspace),
		})
	}
	return out
}

func (s RunService) Apply(ctx context.Context, prepared PreparedRun, req RunServiceRequest, deps RunServiceDependencies) (RunResult, error) {
	if deps.Backend == nil {
		return RunResult{}, errors.New("run backend is required")
	}
	current, err := s.Prepare(req)
	if err != nil {
		return RunResult{}, err
	}
	if req.Ephemeral {
		// Ephemeral identity material is intentionally random per run. Revalidation
		// must compare current policy while retaining the exact identity generated
		// for the reviewed plan; regenerating it would make every apply stale by
		// construction.
		current.Plan.RuntimeProfile.Metadata = cloneRunStringMap(prepared.Plan.RuntimeProfile.Metadata)
		digest, digestErr := digestPreparedRun(current.Plan, current.Request)
		if digestErr != nil {
			return RunResult{}, digestErr
		}
		current.Review.PlanDigest = digest
	}
	if prepared.Review.Version != RunReviewVersion ||
		prepared.Review.PlanVersion != current.Review.PlanVersion ||
		prepared.Review.PlanDigest != current.Review.PlanDigest {
		return RunResult{}, ErrRunPlanStale
	}
	if err := validateRunConfirmation(current.Review, req.Confirmation); err != nil {
		return RunResult{}, err
	}
	effective := current.Request
	return s.Core.ApplyRun(ctx, current.Plan, ApplyRunOptions{
		Backend:            deps.Backend,
		RequestedBackend:   effective.Backend,
		AllowWeakIsolation: effective.AllowWeakIsolation,
		Environment: RunEnvironmentOptions{
			EnvName: effective.EnvironmentName, RemoveAfterRun: effective.RemoveEnvironment, Create: true,
		},
		AuditPath:                  effective.AuditPath,
		HostFSRun:                  effective.HostFSRun,
		DisableProfileHostFSGrants: effective.DisableProfileHostFSGrants,
		OpenTargets:                append([]RunOpenTargetOwner(nil), effective.OpenTargets...),
		EndpointCandidates:         append([]RunEndpointCandidate(nil), effective.EndpointCandidates...),
		EndpointExposures:          append([]RunEndpointExposureRequest(nil), effective.EndpointExposures...),
		OpenerForSession:           deps.OpenerForSession,
		TerminalMode:               effective.Terminal.Mode,
		Streams:                    deps.Streams,
		Lifecycle:                  deps.Lifecycle,
	})
}

func normalizeRunServiceRequest(req RunServiceRequest) (RunServiceRequest, error) {
	if req.Version != RunServiceRequestVersion {
		return req, fmt.Errorf("unsupported run request version %q", req.Version)
	}
	if len(req.Command) == 0 {
		return req, errors.New("command is required")
	}
	for i, value := range req.Command {
		if strings.IndexByte(value, 0) >= 0 {
			return req, fmt.Errorf("command[%d] contains NUL", i)
		}
	}
	if req.Ephemeral && strings.TrimSpace(req.EnvironmentName) != "" {
		return req, errors.New("ephemeral runs cannot select a named environment")
	}
	if req.RemoveEnvironment && strings.TrimSpace(req.EnvironmentName) != "" {
		return req, errors.New("remove-after-run cannot be used with a named environment")
	}
	if err := hostfs.ValidateConfig(req.HostFSRun, hostfs.SourceRun); err != nil {
		return req, fmt.Errorf("invalid run HostFS policy: %w", err)
	}
	terminal, err := normalizeTerminalDescriptor(req.Terminal)
	if err != nil {
		return req, err
	}
	req.Terminal = terminal
	req.Command = append([]string(nil), req.Command...)
	req.PublicEnv = cloneRunStringMap(req.PublicEnv)
	req.PreviewTargets = append([]string(nil), req.PreviewTargets...)
	return req, nil
}

func resolveRunServicePreviewTargets(plan RunPlan, req RunServiceRequest) (RunServiceRequest, error) {
	if len(req.PreviewTargets) == 0 {
		return req, nil
	}
	if len(req.OpenTargets) != 0 || len(req.EndpointCandidates) != 0 || len(req.EndpointExposures) != 0 {
		return req, errors.New("preview targets cannot be combined with pre-resolved endpoint bindings")
	}
	owners, candidates, exposures, err := BuildPreviewOpenOptions(plan.RuntimeProfile, req.PreviewTargets)
	if err != nil {
		return req, err
	}
	req.OpenTargets = owners
	req.EndpointCandidates = candidates
	req.EndpointExposures = exposures
	return req, nil
}

func normalizeTerminalDescriptor(value TerminalDescriptor) (TerminalDescriptor, error) {
	switch value.Mode {
	case "", runsession.TerminalNone:
		if value.Rows != 0 || value.Columns != 0 || strings.TrimSpace(value.TERM) != "" {
			return value, errors.New("non-PTY terminal descriptor cannot include rows, columns, or TERM")
		}
		value.Mode = runsession.TerminalNone
		return value, nil
	case runsession.TerminalPTY:
		if value.Rows == 0 || value.Columns == 0 {
			return value, errors.New("PTY terminal descriptor requires non-zero rows and columns")
		}
		value.TERM = strings.TrimSpace(value.TERM)
		if value.TERM == "" {
			value.TERM = "xterm-256color"
		}
		if len(value.TERM) > maxRunTERMBytes || !validTERM(value.TERM) {
			return value, errors.New("PTY TERM contains unsupported characters")
		}
		return value, nil
	default:
		return value, fmt.Errorf("unsupported terminal mode %q", value.Mode)
	}
}

func validTERM(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '+':
		default:
			return false
		}
	}
	return value != ""
}

func runPlanOptionsFromServiceRequest(req RunServiceRequest) RunPlanOptions {
	return RunPlanOptions{
		ProfileName: req.ProfileName, Backend: req.Backend,
		NetworkMode: req.NetworkMode, ProxySecretRef: req.ProxySecretRef,
		MediatedResolver: req.MediatedResolver, Workspace: req.Workspace,
		GuestWorkspace: req.GuestWorkspace, AllowUnsafeWorkspace: req.AllowUnsafeWorkspace,
		Ephemeral: req.Ephemeral, Command: append([]string(nil), req.Command...),
	}
}

func applyRunPublicEnv(plan *RunPlan, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	if plan.RuntimeProfile.Env.Public == nil {
		plan.RuntimeProfile.Env.Public = map[string]string{}
	}
	for name, value := range values {
		if strings.TrimSpace(name) == "" || strings.IndexByte(name, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return errors.New("public run environment contains an invalid entry")
		}
		plan.RuntimeProfile.Env.Public[name] = value
	}
	return plan.RuntimeProfile.Validate()
}

func validateRunIntegrationRequests(plan RunPlan, req RunServiceRequest) error {
	if len(req.EndpointExposures) == 0 && len(req.EndpointCandidates) == 0 && len(req.OpenTargets) == 0 {
		return nil
	}
	dummy := RunSession{Plan: plan}
	_, err := resolveRunEndpointExposures(dummy, req.OpenTargets, req.EndpointCandidates, req.EndpointExposures)
	return err
}

func digestPreparedRun(plan RunPlan, req RunServiceRequest) (string, error) {
	digestRequest := req
	digestRequest.Confirmation = nil
	payload := struct {
		Version        string            `json:"version"`
		Request        RunServiceRequest `json:"request"`
		Plan           RunPlan           `json:"plan"`
		RuntimeProfile any               `json:"runtimeProfile"`
	}{
		Version: "hideout.run-review-digest/v1", Request: digestRequest,
		Plan: plan, RuntimeProfile: plan.RuntimeProfile,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode run review: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func runRequiresConfirmation(_ RunPlan, _ RunServiceRequest) bool {
	// Existing executable run operations carry their explicit acknowledgements
	// in typed request fields. The binding remains ready for a future plan that
	// adds authority requiring a separate confirmation.
	return false
}

func validateRunConfirmation(review RunReview, confirmation *RunConfirmation) error {
	if confirmation == nil {
		if review.RequiresConfirmation {
			return errors.New("run confirmation is required")
		}
		return nil
	}
	if !confirmation.Accepted {
		return errors.New("run confirmation was denied")
	}
	if confirmation.PlanVersion != review.PlanVersion || confirmation.PlanDigest != review.PlanDigest {
		return ErrRunPlanStale
	}
	return nil
}

func cloneRunStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
