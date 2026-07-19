package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/profiletemplate"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
)

const (
	InitServiceRequestVersion = "hideout.init-service-request/v1"
	InitReviewVersion         = "hideout.init-review/v1"

	InitModeSetup = "setup"
	InitModeInit  = "init"

	InitStateFresh      = "fresh"
	InitStateReady      = "ready"
	InitStateRepairable = "repairable"
	InitStateBlocked    = "blocked"
)

var (
	ErrInitPlanStale     = errors.New("init plan is stale; review a new plan")
	ErrInitNotApplicable = errors.New("init plan is not applicable")
)

// InitServiceRequest is the serializable Manager intent used by both the
// opinionated setup surface and advanced init. Function-valued test/runtime
// dependencies deliberately remain on InitService, not on this wire object.
type InitServiceRequest struct {
	Version                    string                     `json:"version"`
	Mode                       string                     `json:"mode"`
	ProfileName                string                     `json:"profile,omitempty"`
	Backend                    string                     `json:"backend,omitempty"`
	Network                    string                     `json:"network,omitempty"`
	ProxySecretRef             string                     `json:"proxySecretRef,omitempty"`
	MediatedResolver           string                     `json:"mediatedResolver,omitempty"`
	TemplateID                 string                     `json:"template,omitempty"`
	PrivilegeStatus            string                     `json:"privilegeStatus,omitempty"`
	PrivilegeReason            string                     `json:"privilegeReason,omitempty"`
	PrivilegeGuidance          string                     `json:"privilegeGuidance,omitempty"`
	PrivilegeSource            string                     `json:"privilegeSource,omitempty"`
	AllowDegradedTemplate      bool                       `json:"allowDegradedTemplate,omitempty"`
	HostFSVisibility           string                     `json:"hostfsVisibility,omitempty"`
	HostFSVisibilityRoots      []string                   `json:"hostfsVisibilityRoots,omitempty"`
	NameDisclosureAcknowledged bool                       `json:"nameDisclosureAcknowledged,omitempty"`
	RuntimeFamily              string                     `json:"runtime,omitempty"`
	ImageRef                   string                     `json:"image,omitempty"`
	ToolPresets                []string                   `json:"toolPresets,omitempty"`
	NPMGlobals                 []profile.NPMGlobalPackage `json:"npmGlobals,omitempty"`
	Onboarding                 bool                       `json:"onboarding,omitempty"`
	ExplicitProfile            bool                       `json:"explicitProfile,omitempty"`
	ExplicitTemplate           bool                       `json:"explicitTemplate,omitempty"`
	ExplicitBackend            bool                       `json:"explicitBackend,omitempty"`
	ExplicitNetwork            bool                       `json:"explicitNetwork,omitempty"`
	ExplicitVisibility         bool                       `json:"explicitVisibility,omitempty"`
	NoInput                    bool                       `json:"noInput,omitempty"`
}

type InitRuntimeReview struct {
	Family         string `json:"family,omitempty"`
	Revision       string `json:"revision,omitempty"`
	Maturity       string `json:"maturity,omitempty"`
	Status         string `json:"status,omitempty"`
	ArtifactSHA256 string `json:"artifactSHA256,omitempty"`
	DownloadBytes  int64  `json:"downloadBytes,omitempty"`
}

type InitWorkspaceReview struct {
	GuestPath string `json:"guestPath"`
	Mode      string `json:"mode"`
}

type InitNotice struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Action  string `json:"action,omitempty"`
}

type InitReview struct {
	Version              string              `json:"version"`
	PlanVersion          string              `json:"planVersion,omitempty"`
	PlanDigest           string              `json:"planDigest"`
	Mode                 string              `json:"mode"`
	State                string              `json:"state"`
	RequiresConfirmation bool                `json:"requiresConfirmation"`
	Profile              string              `json:"profile"`
	Template             string              `json:"template,omitempty"`
	EffectivePosture     string              `json:"effectivePosture,omitempty"`
	Backend              string              `json:"backend"`
	Network              string              `json:"network"`
	Runtime              InitRuntimeReview   `json:"runtime,omitempty"`
	Workspace            InitWorkspaceReview `json:"workspace"`
	OtherFiles           string              `json:"otherFiles"`
	Audit                string              `json:"audit"`
	Notices              []InitNotice        `json:"notices,omitempty"`
}

type InitObservation struct {
	State         string `json:"state"`
	ProfileDigest string `json:"profileDigest,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type PreparedInit struct {
	Request     InitServiceRequest `json:"request"`
	Review      InitReview         `json:"review"`
	Plan        inittask.Plan      `json:"plan"`
	Observation InitObservation    `json:"observation"`
}

type InitConfirmation struct {
	ReviewVersion string `json:"reviewVersion"`
	PlanDigest    string `json:"planDigest"`
	Confirmed     bool   `json:"confirmed"`
}

type InitApplyResult struct {
	Status string          `json:"status"`
	Result inittask.Result `json:"result"`
}

type InitService struct {
	Core           Core
	ResolveRuntime func(runtimecatalog.Selection) (runtimecatalog.Resolution, error)
}

func SetupInitServiceRequest() InitServiceRequest {
	return InitServiceRequest{Version: InitServiceRequestVersion, Mode: InitModeSetup}
}

func (s InitService) Prepare(req InitServiceRequest) (PreparedInit, error) {
	normalized, err := normalizeInitServiceRequest(req)
	if err != nil {
		return PreparedInit{}, err
	}
	return s.prepareNormalized(normalized)
}

func (s InitService) prepareNormalized(req InitServiceRequest) (PreparedInit, error) {
	observation := observeInitProfile(s.Core.Store, req.ProfileName)
	prepared := PreparedInit{Request: req, Observation: observation}
	if req.Mode == InitModeSetup && observation.State != InitStateFresh {
		prepared.Review, prepared.Observation = s.reviewForObservation(req, observation)
		digest, err := digestPreparedInit(prepared)
		if err != nil {
			return PreparedInit{}, err
		}
		prepared.Review.PlanDigest = digest
		return prepared, nil
	}

	plan, err := s.Core.PlanInit(initOptionsFromServiceRequest(req, s.runtimeResolver()))
	if err != nil {
		return PreparedInit{}, err
	}
	prepared.Plan = plan
	prepared.Review, err = s.reviewForPlan(req, observation, plan)
	if err != nil {
		return PreparedInit{}, err
	}
	digest, err := digestPreparedInit(prepared)
	if err != nil {
		return PreparedInit{}, err
	}
	prepared.Review.PlanDigest = digest
	return prepared, nil
}

func (s InitService) Apply(prepared PreparedInit, confirmation *InitConfirmation) (InitApplyResult, error) {
	normalized, err := normalizeInitServiceRequest(prepared.Request)
	if err != nil {
		return InitApplyResult{}, err
	}
	if prepared.Request.Version != normalized.Version || prepared.Request.Mode != normalized.Mode {
		return InitApplyResult{}, ErrInitPlanStale
	}
	providedDigest, err := digestPreparedInit(prepared)
	if err != nil {
		return InitApplyResult{}, err
	}
	if prepared.Review.Version != InitReviewVersion || prepared.Review.PlanDigest != providedDigest {
		return InitApplyResult{}, ErrInitPlanStale
	}
	if prepared.Observation.State != InitStateFresh && prepared.Request.Mode == InitModeSetup {
		return InitApplyResult{}, ErrInitNotApplicable
	}

	var result inittask.Result
	err = s.Core.withProfileMutationLock(normalized.ProfileName, func() error {
		current, prepareErr := s.prepareNormalized(normalized)
		if prepareErr != nil {
			return prepareErr
		}
		if current.Review.PlanVersion != prepared.Review.PlanVersion || current.Review.PlanDigest != prepared.Review.PlanDigest {
			return ErrInitPlanStale
		}
		confirmed, confirmErr := validateInitConfirmation(current.Review, normalized, confirmation)
		if confirmErr != nil {
			return confirmErr
		}
		operation := inittask.OperationInitApply
		if normalized.Mode == InitModeSetup {
			operation = inittask.OperationSetupApply
		}
		result, prepareErr = s.Core.applyInitLocked(current.Plan, inittask.ApplyOptions{
			NoInput:        normalized.NoInput,
			Confirmed:      confirmed,
			Operation:      operation,
			ResolveRuntime: s.ResolveRuntime,
		})
		return prepareErr
	})
	if err != nil {
		return InitApplyResult{Status: "failed", Result: result}, err
	}
	return InitApplyResult{Status: "configured", Result: result}, nil
}

func normalizeInitServiceRequest(req InitServiceRequest) (InitServiceRequest, error) {
	if req.Version != InitServiceRequestVersion {
		return req, fmt.Errorf("unsupported init service request version %q", req.Version)
	}
	switch req.Mode {
	case InitModeSetup:
		if initSetupRequestHasOverrides(req) && !isNormalizedSetupRequest(req) {
			return req, errors.New("setup does not accept configurable init fields")
		}
		req.ProfileName = "default"
		req.TemplateID = profiletemplate.Dev
		req.Backend = "lima"
		req.Network = "direct"
		req.RuntimeFamily = "developer-standard"
		req.HostFSVisibility = profiletemplate.VisibilityNone
		req.Onboarding = true
		req.ExplicitProfile = true
		req.ExplicitTemplate = true
		req.ExplicitBackend = true
		req.ExplicitNetwork = true
		req.ExplicitVisibility = true
		req.NoInput = true
	case InitModeInit:
		if req.ProfileName == "" {
			req.ProfileName = "default"
		}
	default:
		return req, fmt.Errorf("unsupported init service mode %q", req.Mode)
	}
	if err := profile.ValidateName(req.ProfileName); err != nil {
		return req, err
	}
	req.HostFSVisibilityRoots = append([]string(nil), req.HostFSVisibilityRoots...)
	req.ToolPresets = append([]string(nil), req.ToolPresets...)
	req.NPMGlobals = append([]profile.NPMGlobalPackage(nil), req.NPMGlobals...)
	return req, nil
}

func isNormalizedSetupRequest(req InitServiceRequest) bool {
	return req.ProfileName == "default" && req.TemplateID == profiletemplate.Dev &&
		req.Backend == "lima" && req.Network == "direct" &&
		req.RuntimeFamily == "developer-standard" &&
		req.HostFSVisibility == profiletemplate.VisibilityNone && req.Onboarding &&
		req.ExplicitProfile && req.ExplicitTemplate && req.ExplicitBackend &&
		req.ExplicitNetwork && req.ExplicitVisibility && req.NoInput &&
		req.ProxySecretRef == "" && req.MediatedResolver == "" &&
		req.PrivilegeStatus == "" && req.PrivilegeReason == "" &&
		req.PrivilegeGuidance == "" && req.PrivilegeSource == "" &&
		!req.AllowDegradedTemplate && len(req.HostFSVisibilityRoots) == 0 &&
		!req.NameDisclosureAcknowledged && req.ImageRef == "" &&
		len(req.ToolPresets) == 0 && len(req.NPMGlobals) == 0
}

func initSetupRequestHasOverrides(req InitServiceRequest) bool {
	return req.ProfileName != "" || req.Backend != "" || req.Network != "" ||
		req.ProxySecretRef != "" || req.MediatedResolver != "" || req.TemplateID != "" ||
		req.PrivilegeStatus != "" || req.PrivilegeReason != "" || req.PrivilegeGuidance != "" ||
		req.PrivilegeSource != "" || req.AllowDegradedTemplate || req.HostFSVisibility != "" ||
		len(req.HostFSVisibilityRoots) != 0 || req.NameDisclosureAcknowledged ||
		req.RuntimeFamily != "" || req.ImageRef != "" || len(req.ToolPresets) != 0 ||
		len(req.NPMGlobals) != 0 || req.Onboarding || req.ExplicitProfile ||
		req.ExplicitTemplate || req.ExplicitBackend || req.ExplicitNetwork ||
		req.ExplicitVisibility || req.NoInput
}

func initOptionsFromServiceRequest(req InitServiceRequest, resolver func(runtimecatalog.Selection) (runtimecatalog.Resolution, error)) inittask.Options {
	return inittask.Options{
		ProfileName: req.ProfileName, Backend: req.Backend, Network: req.Network,
		ProxySecretRef: req.ProxySecretRef, MediatedResolver: req.MediatedResolver,
		TemplateID: req.TemplateID, PrivilegeStatus: req.PrivilegeStatus,
		PrivilegeReason: req.PrivilegeReason, PrivilegeGuidance: req.PrivilegeGuidance,
		PrivilegeSource: req.PrivilegeSource, AllowDegradedTemplate: req.AllowDegradedTemplate,
		Onboarding: req.Onboarding, ExplicitProfile: req.ExplicitProfile,
		ExplicitTemplate: req.ExplicitTemplate, ExplicitBackend: req.ExplicitBackend,
		ExplicitNetwork: req.ExplicitNetwork, NoInput: req.NoInput,
		VisibilitySelection:        req.HostFSVisibility,
		VisibilityRoots:            append([]string(nil), req.HostFSVisibilityRoots...),
		NameDisclosureAcknowledged: req.NameDisclosureAcknowledged,
		ExplicitVisibility:         req.ExplicitVisibility,
		ToolPresets:                append([]string(nil), req.ToolPresets...),
		NPMGlobals:                 append([]profile.NPMGlobalPackage(nil), req.NPMGlobals...),
		RuntimeFamily:              req.RuntimeFamily, ImageRef: req.ImageRef, ResolveRuntime: resolver,
	}
}

func observeInitProfile(store profile.Store, name string) InitObservation {
	path := store.ProfilePath(name)
	if err := verifyInitProfilePlacement(store, name); err != nil {
		return InitObservation{State: InitStateBlocked, Reason: "profile placement is unsafe or cannot be proved private"}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, dirErr := os.Lstat(store.ProfileDir(name)); dirErr == nil {
			return InitObservation{State: InitStateBlocked, Reason: "profile state is partial and cannot be proved complete"}
		} else if !errors.Is(dirErr, os.ErrNotExist) {
			return InitObservation{State: InitStateBlocked, Reason: "profile state cannot be read safely"}
		}
		return InitObservation{State: InitStateFresh}
	}
	if err != nil {
		return InitObservation{State: InitStateBlocked, Reason: "profile state cannot be read"}
	}
	p, err := store.Load(name)
	if err != nil {
		return InitObservation{State: InitStateBlocked, Reason: "profile state is malformed or unsupported"}
	}
	sum := sha256.Sum256(data)
	observation := InitObservation{State: InitStateReady, ProfileDigest: hex.EncodeToString(sum[:])}
	for _, key := range []string{"profileId", "identityId", "machineId", "createdAt", "lineageMode"} {
		if strings.TrimSpace(p.Metadata[key]) == "" {
			observation.State = InitStateRepairable
			observation.Reason = "profile metadata or identity state is incomplete"
			return observation
		}
	}
	for _, relative := range []string{"machine/machine-id", "identity.json", "home/.gitconfig"} {
		info, statErr := os.Lstat(filepath.Join(store.ProfileDir(name), relative))
		if statErr != nil || !info.Mode().IsRegular() || !initPathOwnedAndPrivate(info) {
			observation.State = InitStateRepairable
			observation.Reason = "profile metadata or identity state is incomplete"
			return observation
		}
	}
	return observation
}

func verifyInitProfilePlacement(store profile.Store, name string) error {
	profileInfo, profileErr := os.Lstat(store.ProfilePath(name))
	profileExists := profileErr == nil
	if profileErr != nil && !errors.Is(profileErr, os.ErrNotExist) {
		return errors.New("profile placement cannot be verified")
	}
	if profileExists && (!profileInfo.Mode().IsRegular() || profileInfo.Mode()&os.ModeSymlink != 0) {
		return errors.New("profile document is not a regular file")
	}
	paths := []struct {
		path string
		dir  bool
	}{
		{path: store.Root, dir: true},
		{path: filepath.Join(store.Root, "profiles"), dir: true},
		{path: store.ProfileDir(name), dir: true},
		{path: store.ProfilePath(name)},
	}
	for _, candidate := range paths {
		path := candidate.path
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("profile placement cannot be verified")
		}
		if info.Mode()&os.ModeSymlink != 0 || (profileExists && !initPathOwnedAndPrivate(info)) {
			return errors.New("profile placement is unsafe")
		}
		if candidate.dir && !info.IsDir() {
			return errors.New("profile placement directory is invalid")
		}
		if !candidate.dir && !info.Mode().IsRegular() {
			return errors.New("profile document is not a regular file")
		}
	}
	return nil
}

func initPathOwnedAndPrivate(info os.FileInfo) bool {
	if info == nil || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func (s InitService) reviewForPlan(req InitServiceRequest, observation InitObservation, plan inittask.Plan) (InitReview, error) {
	review := InitReview{
		Version: InitReviewVersion, PlanVersion: plan.Version, Mode: req.Mode,
		State: observation.State, RequiresConfirmation: req.Mode == InitModeSetup || !req.NoInput,
		Profile: plan.Profile, Template: plan.TemplateID, EffectivePosture: plan.EffectivePosture,
		Backend: plan.Backend, Network: plan.Network,
		Workspace:  InitWorkspaceReview{GuestPath: "/workspace", Mode: "read-write"},
		OtherFiles: "hidden-unless-granted", Audit: "always-on",
	}
	runtimeReview, err := s.runtimeReview(plan.RuntimeSelection)
	if err != nil {
		return InitReview{}, err
	}
	review.Runtime = runtimeReview
	return review, nil
}

func (s InitService) reviewForObservation(req InitServiceRequest, observation InitObservation) (InitReview, InitObservation) {
	review := InitReview{
		Version: InitReviewVersion, Mode: req.Mode, State: observation.State,
		Profile: req.ProfileName, Network: req.Network,
		Workspace:  InitWorkspaceReview{GuestPath: "/workspace", Mode: "read-write"},
		OtherFiles: "hidden-unless-granted", Audit: "always-on",
	}
	if observation.State == InitStateReady || observation.State == InitStateRepairable {
		p, err := s.Core.Store.Load(req.ProfileName)
		if err != nil {
			observation = InitObservation{State: InitStateBlocked, Reason: "profile state changed or cannot be read safely"}
		} else {
			review.Template = p.Metadata["templateId"]
			review.EffectivePosture = p.Metadata["templatePosture"]
			review.Network = p.Network.Mode
			if p.Environment.Runtime != nil {
				runtimeReview, runtimeErr := s.runtimeReview(p.Environment.Runtime)
				if runtimeErr != nil {
					observation = InitObservation{State: InitStateBlocked, Reason: "profile runtime state is unsupported or cannot be verified"}
				} else {
					review.Runtime = runtimeReview
				}
			}
		}
	}
	review.State = observation.State
	return reviewWithInitObservationNotice(review, observation), observation
}

func reviewWithInitObservationNotice(review InitReview, observation InitObservation) InitReview {
	switch observation.State {
	case InitStateRepairable:
		review.Notices = []InitNotice{{Code: "setup.profile.repair-required", Summary: observation.Reason, Action: "hideout doctor --fix --dry-run --profile default --backend lima"}}
	case InitStateBlocked:
		review.Notices = []InitNotice{{Code: "setup.profile.blocked", Summary: observation.Reason, Action: "hideout doctor --profile default --backend lima"}}
	}
	return review
}

func (s InitService) runtimeReview(provenance *environment.RuntimeProvenance) (InitRuntimeReview, error) {
	if provenance == nil {
		return InitRuntimeReview{}, nil
	}
	resolver := s.runtimeResolver()
	resolved, err := resolver(runtimecatalog.Selection{
		Family: provenance.Family, Revision: provenance.Revision,
		HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
	})
	if err != nil {
		return InitRuntimeReview{}, err
	}
	if resolved.Provenance != *provenance {
		return InitRuntimeReview{}, errors.New("runtime review provenance differs from the init plan")
	}
	return InitRuntimeReview{
		Family: resolved.Family.ID, Revision: resolved.Revision.ID,
		Maturity: resolved.Family.Maturity, Status: resolved.Revision.Status,
		ArtifactSHA256: resolved.Artifact.SHA256, DownloadBytes: resolved.Artifact.DownloadBytes,
	}, nil
}

func (s InitService) runtimeResolver() func(runtimecatalog.Selection) (runtimecatalog.Resolution, error) {
	if s.ResolveRuntime != nil {
		return s.ResolveRuntime
	}
	if s.Core.RuntimeResolver != nil {
		return s.Core.RuntimeResolver
	}
	return runtimecatalog.ResolveEmbedded
}

func validateInitConfirmation(review InitReview, req InitServiceRequest, confirmation *InitConfirmation) (bool, error) {
	if !review.RequiresConfirmation {
		return false, nil
	}
	if confirmation == nil || !confirmation.Confirmed {
		return false, errors.New("init confirmation is required")
	}
	if confirmation.ReviewVersion != review.Version || confirmation.PlanDigest != review.PlanDigest {
		return false, ErrInitPlanStale
	}
	if req.NoInput && req.Mode != InitModeSetup {
		return false, errors.New("non-interactive init cannot supply an interactive confirmation")
	}
	return true, nil
}

type canonicalInit struct {
	Request     InitServiceRequest `json:"request"`
	Observation InitObservation    `json:"observation"`
	Plan        inittask.Plan      `json:"plan"`
}

func digestPreparedInit(prepared PreparedInit) (string, error) {
	planData, err := json.Marshal(prepared.Plan)
	if err != nil {
		return "", fmt.Errorf("clone init plan for digest: %w", err)
	}
	var plan inittask.Plan
	if err := json.Unmarshal(planData, &plan); err != nil {
		return "", fmt.Errorf("clone init plan for digest: %w", err)
	}
	projection := canonicalInit{Request: prepared.Request, Observation: prepared.Observation, Plan: plan}
	projection.Plan.StoreRoot = ""
	projection.Plan.EvidencePath = ""
	projection.Plan.ReviewLines = nil
	projection.Plan.Warnings = nil
	projection.Plan.NonClaims = nil
	projection.Plan.NextSteps = nil
	for i := range projection.Plan.Tasks {
		projection.Plan.Tasks[i].Message = ""
		projection.Plan.Tasks[i].Outputs = nil
	}
	data, err := json.Marshal(projection)
	if err != nil {
		return "", fmt.Errorf("encode init plan digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
