package manager

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

const APIVersion = "hideout.manager-api/v1"

type API struct {
	Core                      Core
	Token                     string
	ExpiresAt                 time.Time
	TokenValidator            TokenValidator
	RunCredentialPollInterval time.Duration
	AllowedOrigins            []string
	AllowedHosts              []string
	Now                       func() time.Time
	RunBackend                RunBackendFactory
	RunOpener                 RunOpenerFactory
	EnvOperator               EnvironmentOperator
}

// TokenValidator validates an operator credential without exposing token
// lifecycle state to the Manager API. When configured on API it replaces the
// static Token/ExpiresAt check; callers remain responsible for constant-time
// validation and expiry or rotation policy.
type TokenValidator func(string) bool

type APIResponse struct {
	Version  string   `json:"version"`
	Resource string   `json:"resource,omitempty"`
	Data     any      `json:"data,omitempty"`
	Errors   []string `json:"errors"`
}

type RunBackendFactory func(RunAPIRequest, RunPlan) (backend.Backend, error)
type RunOpenerFactory func(RunAPIRequest, RunPlan, RunSession) broker.Opener

type RunAPIRequest struct {
	ProfileName                string                       `json:"profile,omitempty"`
	Backend                    string                       `json:"backend,omitempty"`
	NetworkMode                string                       `json:"networkMode,omitempty"`
	ProxySecretRef             string                       `json:"proxySecretRef,omitempty"`
	MediatedResolver           string                       `json:"mediatedResolver,omitempty"`
	Workspace                  string                       `json:"workspace,omitempty"`
	GuestWorkspace             string                       `json:"guestWorkspace,omitempty"`
	AllowUnsafeWorkspace       bool                         `json:"allowUnsafeWorkspace,omitempty"`
	Ephemeral                  bool                         `json:"ephemeral,omitempty"`
	Command                    []string                     `json:"command"`
	PublicEnv                  map[string]string            `json:"publicEnv,omitempty"`
	AuditPath                  string                       `json:"auditPath,omitempty"`
	HostFSRun                  hostfs.Config                `json:"hostfs,omitempty"`
	DisableProfileHostFSGrants bool                         `json:"disableProfileHostFSGrants,omitempty"`
	OpenTargets                []RunOpenTargetOwner         `json:"openTargets,omitempty"`
	EndpointCandidates         []RunEndpointCandidate       `json:"endpointCandidates,omitempty"`
	EndpointExposures          []RunEndpointExposureRequest `json:"endpointExposures,omitempty"`
	Terminal                   TerminalDescriptor           `json:"terminal,omitempty"`
	Confirmation               *RunConfirmation             `json:"confirmation,omitempty"`
	AllowWeakIsolation         bool                         `json:"allowWeakIsolation,omitempty"`
	EnvironmentName            string                       `json:"environmentName,omitempty"`
	RemoveEnvironment          bool                         `json:"removeEnvironment,omitempty"`
}

type RunStatusResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

type InitAPIRequest struct {
	ProfileName                string   `json:"profile,omitempty"`
	Backend                    string   `json:"backend,omitempty"`
	Network                    string   `json:"network,omitempty"`
	ProxySecretRef             string   `json:"proxySecretRef,omitempty"`
	MediatedResolver           string   `json:"mediatedResolver,omitempty"`
	TemplateID                 string   `json:"template,omitempty"`
	PrivilegeStatus            string   `json:"privilegeStatus,omitempty"`
	PrivilegeReason            string   `json:"privilegeReason,omitempty"`
	PrivilegeGuidance          string   `json:"privilegeGuidance,omitempty"`
	PrivilegeSource            string   `json:"privilegeSource,omitempty"`
	AllowDegradedTemplate      bool     `json:"allowDegradedTemplate,omitempty"`
	HostFSVisibility           string   `json:"hostfsVisibility,omitempty"`
	HostFSVisibilityRoots      []string `json:"hostfsVisibilityRoots,omitempty"`
	NameDisclosureAcknowledged bool     `json:"nameDisclosureAcknowledged,omitempty"`
	DryRun                     bool     `json:"dryRun,omitempty"`
	RuntimeFamily              string   `json:"runtime,omitempty"`
	ImageRef                   string   `json:"image,omitempty"`
}

type EnvironmentActionAPIRequest struct {
	IDs         []string `json:"ids,omitempty"`
	Idle        string   `json:"idle,omitempty"`
	StoppedOnly bool     `json:"stoppedOnly,omitempty"`
}

type RuntimeVerifyAPIRequest struct {
	EnvironmentName string             `json:"environmentName,omitempty"`
	Plan            *RuntimeVerifyPlan `json:"plan,omitempty"`
}

type CommandProxyAPIRequest struct {
	ProfileName string `json:"profile,omitempty"`
	Operation   string `json:"operation"`
	Command     string `json:"command"`
}

type AdapterPackAPIRequest struct {
	Operation                   string           `json:"operation,omitempty"`
	SourceKind                  string           `json:"sourceKind,omitempty"`
	SourcePath                  string           `json:"sourcePath,omitempty"`
	SourceURL                   string           `json:"sourceUrl,omitempty"`
	SourceCommit                string           `json:"sourceCommit,omitempty"`
	ProfileName                 string           `json:"profile,omitempty"`
	PackID                      string           `json:"packId,omitempty"`
	RevisionID                  string           `json:"revisionId,omitempty"`
	AdapterID                   string           `json:"adapterId,omitempty"`
	CommandAdapterID            string           `json:"commandAdapterId,omitempty"`
	Commands                    []string         `json:"commands,omitempty"`
	AllowedProposalCapabilities []string         `json:"allowedProposalCapabilities,omitempty"`
	Plan                        *AdapterPackPlan `json:"plan,omitempty"`
}

type HostAppPackAPIRequest struct {
	Operation      string            `json:"operation,omitempty"`
	SourceKind     string            `json:"sourceKind,omitempty"`
	SourcePath     string            `json:"sourcePath,omitempty"`
	SourceURL      string            `json:"sourceUrl,omitempty"`
	SourceCommit   string            `json:"sourceCommit,omitempty"`
	ProfileName    string            `json:"profile,omitempty"`
	PackID         string            `json:"packId,omitempty"`
	RevisionID     string            `json:"revisionId,omitempty"`
	BindingIDs     []string          `json:"bindingIds,omitempty"`
	Access         string            `json:"access,omitempty"`
	Replacements   map[string]string `json:"replacements,omitempty"`
	ExpectedDigest string            `json:"expectedDigest,omitempty"`
	InstallOnly    bool              `json:"installOnly,omitempty"`
	Accepted       bool              `json:"accepted,omitempty"`
	Plan           *HostAppPackPlan  `json:"plan,omitempty"`
}

type HostAppPackListAPIResponse struct {
	HostAppPacks []HostAppPackSummary `json:"hostAppPacks"`
}

type ProfileHostFSAPIRequest struct {
	ProfileName string                   `json:"profile,omitempty"`
	Operation   string                   `json:"operation"`
	Rule        string                   `json:"rule,omitempty"`
	RuleID      string                   `json:"ruleId,omitempty"`
	Reason      string                   `json:"reason,omitempty"`
	Migrations  []ProfileHostFSMigration `json:"migrations,omitempty"`
}

type ProfileEnvAPIRequest struct {
	ProfileName string `json:"profile,omitempty"`
	Operation   string `json:"operation"`
	Name        string `json:"name,omitempty"`
	Value       string `json:"value,omitempty"`
}

type ExportAPIRequest struct {
	Source                  string   `json:"source,omitempty"`
	Session                 string   `json:"session,omitempty"`
	Profile                 string   `json:"profile,omitempty"`
	Action                  string   `json:"action,omitempty"`
	Decision                string   `json:"decision,omitempty"`
	Limit                   int      `json:"limit,omitempty"`
	BundlePath              string   `json:"bundle,omitempty"`
	DoctorReportPath        string   `json:"doctorReport,omitempty"`
	From                    string   `json:"from,omitempty"`
	Out                     string   `json:"out,omitempty"`
	Redact                  []string `json:"redact,omitempty"`
	PolicyProfile           string   `json:"policyProfile,omitempty"`
	AcknowledgeFullFidelity bool     `json:"acknowledgeFullFidelity,omitempty"`
	Share                   bool     `json:"share,omitempty"`
}

func NewAPI(core Core, token string, ttl time.Duration) API {
	now := time.Now().UTC()
	return API{
		Core:      core,
		Token:     token,
		ExpiresAt: now.Add(ttl),
		Now:       func() time.Time { return time.Now().UTC() },
	}
}

func (api API) Handler() http.Handler {
	return http.HandlerFunc(api.ServeHTTP)
}

// Authorize reports whether the request carries a valid operator credential,
// using the exact same check as the served Manager routes. It lets the daemon
// (internal/daemon) apply identical authentication to its own status/event
// endpoints without reimplementing token handling (FR-016). It does not alter any
// request or response.
func (api API) Authorize(r *http.Request) error {
	return api.authorize(r)
}

func (api API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := api.checkHost(r); err != nil {
		writeAPIError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := api.authorize(r); err != nil {
		writeAPIError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err := api.checkOrigin(r); err != nil {
		writeAPIError(w, http.StatusForbidden, err.Error())
		return
	}
	resource, ok := strings.CutPrefix(r.URL.Path, "/api/v1/")
	if !ok || resource == "" {
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
		return
	}
	if r.Method == http.MethodPost {
		spec, ok := RecognizeManagerResource(http.MethodPost, resource)
		if !ok {
			if _, exists := RecognizeManagerResourceAnyMethod(resource); exists {
				writeAPIMethodNotAllowed(w, http.MethodGet)
				return
			}
			writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
			return
		}
		api.servePostResource(w, r, spec, resource)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if _, ok := RecognizeManagerResource(http.MethodGet, resource); !ok {
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
		return
	}
	if resource == "audit/events" {
		api.serveAuditEvents(w, r)
		return
	}
	if resource == "hostfs/write/status" {
		api.serveHostFSWriteStatus(w, r)
		return
	}
	if resource == "decisions" {
		api.serveDecisions(w, r)
		return
	}
	if strings.HasPrefix(resource, "decisions/") {
		api.serveDecisionInspect(w, r, strings.TrimPrefix(resource, "decisions/"))
		return
	}
	if resource == "decision/inspect" {
		api.serveDecisionInspect(w, r, r.URL.Query().Get("id"))
		return
	}
	if resource == "decision/status" {
		api.serveDecisionStatus(w, r)
		return
	}
	if resource == "notices" {
		api.serveNotices(w, r)
		return
	}
	if strings.HasPrefix(resource, "notices/") {
		api.serveNoticeInspect(w, r, strings.TrimPrefix(resource, "notices/"))
		return
	}
	if resource == "notice/inspect" {
		api.serveNoticeInspect(w, r, r.URL.Query().Get("id"))
		return
	}
	if resource == "adapter-packs" {
		api.serveAdapterPacks(w, r)
		return
	}
	if resource == "adapter-pack/inspect" {
		api.serveAdapterPackInspect(w, r)
		return
	}
	if resource == "app/list" {
		api.serveHostAppPacks(w, r)
		return
	}
	if resource == "app/inspect" {
		api.serveHostAppPackInspect(w, r)
		return
	}
	if resource == "runtime/catalog" {
		api.serveRuntimeCatalog(w, r)
		return
	}
	if resource == "runtime/status" {
		api.serveRuntimeStatus(w, r)
		return
	}
	overview, err := api.Core.Overview(r.Context())
	if err != nil && overview.Version == "" {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resource == "run/status" {
		api.serveRunStatus(w, r, overview, err)
		return
	}
	data, ok := overviewResource(overview, resource)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
		return
	}
	resp := APIResponse{
		Version:  APIVersion,
		Resource: resource,
		Data:     data,
		Errors:   []string{},
	}
	if err != nil {
		resp.Errors = []string{err.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveRuntimeCatalog(w http.ResponseWriter, r *http.Request) {
	family := strings.TrimSpace(r.URL.Query().Get("family"))
	revision := strings.TrimSpace(r.URL.Query().Get("revision"))
	var data any
	var err error
	if family == "" {
		if revision != "" {
			writeAPIError(w, http.StatusBadRequest, "runtime revision requires a family")
			return
		}
		data, err = api.Core.RuntimeCatalog()
	} else {
		data, err = api.Core.InspectRuntime(family, revision)
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "runtime/catalog", Data: data, Errors: []string{}})
}

func (api API) serveRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.URL.Query().Get("env"))
	if handle == "" {
		writeAPIError(w, http.StatusBadRequest, "runtime status requires env query")
		return
	}
	status, err := api.Core.RuntimeStatus(handle)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "runtime/status", Data: status, Errors: []string{}})
}

func (api API) serveRuntimeVerifyPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRuntimeVerifyAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanRuntimeVerify(req.EnvironmentName)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "runtime/verify/plan", Data: plan, Errors: []string{}})
}

func (api API) serveRuntimeVerifyApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRuntimeVerifyAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Plan == nil {
		writeAPIError(w, http.StatusBadRequest, "runtime verify apply requires plan")
		return
	}
	if api.RunBackend == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime verification backend factory is unavailable")
		return
	}
	backendInstance, err := api.RunBackend(
		RunAPIRequest{Backend: req.Plan.Backend, EnvironmentName: req.Plan.EnvironmentName},
		RunPlan{Backend: req.Plan.Backend},
	)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyRuntimeVerify(r.Context(), *req.Plan, backendInstance)
	resp := APIResponse{Version: APIVersion, Resource: "runtime/verify/apply", Data: result, Errors: []string{}}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) servePostResource(w http.ResponseWriter, r *http.Request, spec RouteSpec, resource string) {
	switch spec.Resource {
	case "decisions/{id}/claim", "decisions/{id}/approve", "decisions/{id}/deny", "decisions/{id}/reopen", "decisions/{id}/revoke":
		api.serveDecisionMemberPost(w, r, resource)
		return
	case "notices/{id}/ack":
		api.serveNoticeMemberPost(w, r, resource)
		return
	}
	switch spec.Resource {
	case "init/plan":
		api.serveInitPlan(w, r)
	case "init/apply":
		api.serveInitApply(w, r)
	case "run/plan":
		api.serveRunPlan(w, r)
	case "run/apply":
		api.serveRunApply(w, r)
	case "environment/stop/plan":
		api.serveEnvironmentStopPlan(w, r)
	case "environment/stop/apply":
		api.serveEnvironmentStopApply(w, r)
	case "environment/clean/plan":
		api.serveEnvironmentCleanPlan(w, r)
	case "environment/clean/apply":
		api.serveEnvironmentCleanApply(w, r)
	case "profile/command-proxy/plan":
		api.serveCommandProxyPlan(w, r)
	case "profile/command-proxy/apply":
		api.serveCommandProxyApply(w, r)
	case "profile/hostfs/plan":
		api.serveProfileHostFSPlan(w, r)
	case "profile/hostfs/apply":
		api.serveProfileHostFSApply(w, r)
	case "profile/env/plan":
		api.serveProfileEnvPlan(w, r)
	case "profile/env/apply":
		api.serveProfileEnvApply(w, r)
	case "evidence/export/plan":
		api.serveExportPlan(w, r)
	case "evidence/export/apply":
		api.serveExportApply(w, r)
	case "hostfs/write/plan":
		api.serveHostFSWritePlan(w, r)
	case "hostfs/write/claim":
		api.serveHostFSWriteClaim(w, r)
	case "hostfs/write/apply":
		api.serveHostFSWriteApply(w, r)
	case "hostfs/write/discard":
		api.serveHostFSWriteDiscard(w, r)
	case "decision/claim":
		api.serveDecisionClaim(w, r)
	case "decision/approve":
		api.serveDecisionApprove(w, r)
	case "decision/deny":
		api.serveDecisionDeny(w, r)
	case "decision/reopen":
		api.serveDecisionReopen(w, r)
	case "decision/revoke":
		api.serveDecisionRevoke(w, r)
	case "notice/ack":
		api.serveNoticeAck(w, r)
	case "adapter-pack/plan":
		api.serveAdapterPackPlan(w, r)
	case "adapter-pack/apply":
		api.serveAdapterPackApply(w, r)
	case "app/plan":
		api.serveHostAppPackPlan(w, r)
	case "app/apply":
		api.serveHostAppPackApply(w, r)
	case "runtime/verify/plan":
		api.serveRuntimeVerifyPlan(w, r)
	case "runtime/verify/apply":
		api.serveRuntimeVerifyApply(w, r)
	default:
		writeAPIError(w, http.StatusInternalServerError, "manager route inventory has no POST handler for "+spec.Resource)
	}
}

func (api API) serveInitPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeInitAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanInit(initOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "init/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveInitApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeInitAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanInit(initOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyInit(plan, inittask.ApplyOptions{
		NoInput: true,
		DryRun:  req.DryRun,
	})
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "init/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveRunPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRunAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	prepared, err := (RunService{Core: api.Core}).Prepare(runServiceRequestFromAPI(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "run/plan",
		Data:     prepared.Review,
		Errors:   []string{},
	})
}

func (api API) serveRunApply(w http.ResponseWriter, r *http.Request) {
	if api.RunBackend == nil {
		writeAPIError(w, http.StatusNotImplemented, "run apply backend factory is not configured")
		return
	}
	req, err := decodeRunAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	serviceRequest := runServiceRequestFromAPI(req)
	service := RunService{Core: api.Core}
	prepared, err := service.Prepare(serviceRequest)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	be, err := api.RunBackend(req, prepared.Plan)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	runCtx, cancelRun := api.bindRunCredentialContext(r)
	defer cancelRun()
	result, runErr := service.Apply(runCtx, prepared, serviceRequest, RunServiceDependencies{
		Backend: be, OpenerForSession: api.runOpenerForSession(req, prepared.Plan),
	})
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "run/apply",
		Data:     result,
		Errors:   []string{},
	}
	if runErr != nil {
		resp.Errors = []string{runErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) runOpenerForSession(req RunAPIRequest, plan RunPlan) func(RunSession) broker.Opener {
	if api.RunOpener == nil {
		return nil
	}
	return func(runSession RunSession) broker.Opener {
		return api.RunOpener(req, plan, runSession)
	}
}

func (api API) serveExportPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeExportAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanExport(exportOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "evidence/export/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveExportApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeExportAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := exportOptionsFromAPIRequest(req)
	plan, err := api.Core.PlanExport(opts)
	if err != nil {
		plan = exportboundary.Plan{Artifact: exportboundary.Artifact{Version: exportboundary.ArtifactVersion}}
	}
	result, applyErr := api.Core.ApplyExport(plan, opts)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "evidence/export/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveHostFSWritePlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostFSWritePlanRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanHostFSWrite(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "hostfs/write/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveHostFSWriteClaim(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostFSWriteClaimRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	claim, err := api.Core.ClaimHostFSWrite(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "hostfs/write/claim",
		Data:     claim,
		Errors:   []string{},
	})
}

func (api API) serveHostFSWriteApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostFSWriteApplyRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyHostFSWrite(req)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "hostfs/write/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveHostFSWriteDiscard(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostFSWriteDiscardRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, discardErr := api.Core.DiscardHostFSWrite(req)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "hostfs/write/discard",
		Data:     result,
		Errors:   []string{},
	}
	if discardErr != nil {
		resp.Errors = []string{discardErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveHostFSWriteStatus(w http.ResponseWriter, r *http.Request) {
	status, err := api.Core.HostFSWriteStatus(HostFSWriteStatusRequest{
		Session: r.URL.Query().Get("session"),
		Profile: r.URL.Query().Get("profile"),
		State:   r.URL.Query().Get("state"),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "hostfs/write/status",
		Data:     status,
		Errors:   []string{},
	})
}

func (api API) serveDecisions(w http.ResponseWriter, r *http.Request) {
	includeTerminal, err := strconv.ParseBool(defaultString(r.URL.Query().Get("includeTerminal"), "false"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid includeTerminal")
		return
	}
	decisions, err := api.Core.ListDecisions(DecisionListRequest{
		Kind:            r.URL.Query().Get("kind"),
		State:           r.URL.Query().Get("state"),
		Profile:         r.URL.Query().Get("profile"),
		Session:         r.URL.Query().Get("session"),
		IncludeTerminal: includeTerminal,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "decisions",
		Data:     decisions,
		Errors:   []string{},
	})
}

func (api API) serveDecisionInspect(w http.ResponseWriter, r *http.Request, id string) {
	decision, err := api.Core.InspectDecision(id)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "decision/inspect",
		Data:     decision,
		Errors:   []string{},
	})
}

func (api API) serveDecisionStatus(w http.ResponseWriter, r *http.Request) {
	status, err := api.Core.DecisionStatus()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "decision/status",
		Data:     status,
		Errors:   []string{},
	})
}

func (api API) serveDecisionClaim(w http.ResponseWriter, r *http.Request) {
	req, err := decodeDecisionClaimRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	claim, err := api.Core.ClaimDecision(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "decision/claim",
		Data:     claim,
		Errors:   []string{},
	})
}

func (api API) serveDecisionApprove(w http.ResponseWriter, r *http.Request) {
	req, err := decodeDecisionResolveRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApproveDecision(req)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "decision/approve",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveDecisionDeny(w http.ResponseWriter, r *http.Request) {
	req, err := decodeDecisionResolveRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, denyErr := api.Core.DenyDecision(req)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "decision/deny",
		Data:     result,
		Errors:   []string{},
	}
	if denyErr != nil {
		resp.Errors = []string{denyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveDecisionReopen(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostFSReadReopenRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, reopenErr := api.Core.ReopenDecision(req)
	resp := APIResponse{Version: APIVersion, Resource: "decision/reopen", Data: result, Errors: []string{}}
	if reopenErr != nil {
		resp.Errors = []string{reopenErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveDecisionRevoke(w http.ResponseWriter, r *http.Request) {
	req, err := decodeDecisionRevokeRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, revokeErr := api.Core.RevokeDecision(req)
	resp := APIResponse{Version: APIVersion, Resource: "decision/revoke", Data: result, Errors: []string{}}
	if revokeErr != nil {
		resp.Errors = []string{revokeErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveNotices(w http.ResponseWriter, r *http.Request) {
	notices, err := api.Core.ListNotices(NoticeListRequest{
		Kind:     r.URL.Query().Get("kind"),
		Profile:  r.URL.Query().Get("profile"),
		Session:  r.URL.Query().Get("session"),
		Severity: r.URL.Query().Get("severity"),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "notices",
		Data:     notices,
		Errors:   []string{},
	})
}

func (api API) serveNoticeInspect(w http.ResponseWriter, r *http.Request, id string) {
	notice, err := api.Core.InspectNotice(id)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "notice/inspect",
		Data:     notice,
		Errors:   []string{},
	})
}

func (api API) serveNoticeAck(w http.ResponseWriter, r *http.Request) {
	req, err := decodeNoticeAckRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	ack, err := api.Core.AckNotice(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "notice/ack",
		Data:     ack,
		Errors:   []string{},
	})
}

func (api API) serveDecisionMemberPost(w http.ResponseWriter, r *http.Request, resource string) {
	parts := strings.Split(strings.TrimPrefix(resource, "decisions/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
		return
	}
	switch parts[1] {
	case "claim":
		req, err := decodeDecisionClaimRequest(w, r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.DecisionID == "" {
			req.DecisionID = parts[0]
		}
		claim, err := api.Core.ClaimDecision(req)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "decision/claim", Data: claim, Errors: []string{}})
	case "approve":
		req, err := decodeDecisionResolveRequest(w, r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.DecisionID == "" {
			req.DecisionID = parts[0]
		}
		result, applyErr := api.Core.ApproveDecision(req)
		resp := APIResponse{Version: APIVersion, Resource: "decision/approve", Data: result, Errors: []string{}}
		if applyErr != nil {
			resp.Errors = []string{applyErr.Error()}
		}
		writeAPIJSON(w, http.StatusOK, resp)
	case "deny":
		req, err := decodeDecisionResolveRequest(w, r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.DecisionID == "" {
			req.DecisionID = parts[0]
		}
		result, denyErr := api.Core.DenyDecision(req)
		resp := APIResponse{Version: APIVersion, Resource: "decision/deny", Data: result, Errors: []string{}}
		if denyErr != nil {
			resp.Errors = []string{denyErr.Error()}
		}
		writeAPIJSON(w, http.StatusOK, resp)
	case "reopen":
		req, err := decodeHostFSReadReopenRequest(w, r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.DecisionID == "" {
			req.DecisionID = parts[0]
		}
		result, reopenErr := api.Core.ReopenDecision(req)
		resp := APIResponse{Version: APIVersion, Resource: "decision/reopen", Data: result, Errors: []string{}}
		if reopenErr != nil {
			resp.Errors = []string{reopenErr.Error()}
		}
		writeAPIJSON(w, http.StatusOK, resp)
	case "revoke":
		req, err := decodeDecisionRevokeRequest(w, r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.DecisionID == "" {
			req.DecisionID = parts[0]
		}
		result, revokeErr := api.Core.RevokeDecision(req)
		resp := APIResponse{Version: APIVersion, Resource: "decision/revoke", Data: result, Errors: []string{}}
		if revokeErr != nil {
			resp.Errors = []string{revokeErr.Error()}
		}
		writeAPIJSON(w, http.StatusOK, resp)
	default:
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
	}
}

func (api API) serveNoticeMemberPost(w http.ResponseWriter, r *http.Request, resource string) {
	parts := strings.Split(strings.TrimPrefix(resource, "notices/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "ack" {
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
		return
	}
	req, err := decodeNoticeAckRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NoticeID == "" {
		req.NoticeID = parts[0]
	}
	ack, err := api.Core.AckNotice(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "notice/ack", Data: ack, Errors: []string{}})
}

func (api API) serveAdapterPacks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("packId") != "" {
		api.serveAdapterPackInspect(w, r)
		return
	}
	packs, err := api.Core.ListAdapterPacks()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "adapter-packs",
		Data:     packs,
		Errors:   []string{},
	})
}

func (api API) serveAdapterPackInspect(w http.ResponseWriter, r *http.Request) {
	packID := r.URL.Query().Get("packId")
	if packID == "" {
		writeAPIError(w, http.StatusBadRequest, "packId is required")
		return
	}
	entry, err := api.Core.InspectAdapterPack(packID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "adapter-pack/inspect",
		Data:     entry,
		Errors:   []string{},
	})
}

func (api API) serveAdapterPackPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAdapterPackAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanAdapterPack(adapterPackOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "adapter-pack/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveAdapterPackApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAdapterPackAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan := AdapterPackPlan{}
	if req.Plan != nil {
		plan = *req.Plan
	} else {
		plan, err = api.Core.PlanAdapterPack(adapterPackOptionsFromAPIRequest(req))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	result, applyErr := api.Core.ApplyAdapterPack(plan)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "adapter-pack/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveHostAppPacks(w http.ResponseWriter, r *http.Request) {
	packs, err := api.Core.ListHostAppPacks()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "app/list",
		Data:     HostAppPackListAPIResponse{HostAppPacks: packs},
		Errors:   []string{},
	})
}

func (api API) serveHostAppPackInspect(w http.ResponseWriter, r *http.Request) {
	packID := r.URL.Query().Get("packId")
	if packID == "" {
		writeAPIError(w, http.StatusBadRequest, "packId is required")
		return
	}
	inspection, err := api.Core.InspectHostAppPack(packID, r.URL.Query().Get("profile"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "app/inspect", Data: inspection.Status, Errors: []string{}})
}

func (api API) serveHostAppPackPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostAppPackAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanHostAppPack(hostAppPackOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "app/plan", Data: plan, Errors: []string{}})
}

func (api API) serveHostAppPackApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeHostAppPackAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Plan == nil {
		writeAPIError(w, http.StatusBadRequest, "host-app apply requires the exact reviewed plan")
		return
	}
	if req.Operation == "" || req.Operation != req.Plan.Operation {
		writeAPIError(w, http.StatusBadRequest, "host-app apply operation must match the reviewed plan operation")
		return
	}
	if !req.Accepted {
		writeAPIError(w, http.StatusBadRequest, "host-app apply requires explicit acceptance")
		return
	}
	plan, err := api.reviewedHostAppPackPlan(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyHostAppPack(plan)
	if applyErr != nil {
		writeAPIError(w, http.StatusBadRequest, applyErr.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{Version: APIVersion, Resource: "app/apply", Data: result, Errors: []string{}})
}

func (api API) reviewedHostAppPackPlan(req HostAppPackAPIRequest) (HostAppPackPlan, error) {
	plan := *req.Plan
	if plan.Version != HostAppPackPlanVersion {
		return HostAppPackPlan{}, errors.New("invalid host-app plan version")
	}
	sourceApply := plan.Operation == "add" || plan.Operation == "update" ||
		((plan.Operation == "validate" || plan.Operation == "test") && plan.SourceReview.Kind != "")
	if sourceApply {
		source := hostapppack.SourceSpec{
			Kind:   req.SourceKind,
			Path:   req.SourcePath,
			URL:    req.SourceURL,
			Commit: req.SourceCommit,
		}
		if source.Kind == "" {
			return HostAppPackPlan{}, fmt.Errorf("host-app %s apply requires the original source locator alongside the reviewed plan", plan.Operation)
		}
		plan.Source = source
	}
	checked, err := api.Core.PlanHostAppPack(hostAppPackOptionsFromReviewedPlan(plan))
	if err != nil {
		return HostAppPackPlan{}, err
	}
	submittedJSON, err := json.Marshal(plan)
	if err != nil {
		return HostAppPackPlan{}, errors.New("invalid host-app reviewed plan")
	}
	checkedJSON, err := json.Marshal(checked)
	if err != nil {
		return HostAppPackPlan{}, errors.New("invalid host-app reviewed plan")
	}
	if string(submittedJSON) != string(checkedJSON) {
		return HostAppPackPlan{}, fmt.Errorf("host-app %s reviewed plan is stale or malformed", plan.Operation)
	}
	return checked, nil
}

func (api API) serveEnvironmentStopPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeEnvironmentActionAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts, err := environmentActionOptionsFromAPIRequest(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanEnvironmentStop(opts)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "environment/stop/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveEnvironmentStopApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeEnvironmentActionAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts, err := environmentActionOptionsFromAPIRequest(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanEnvironmentStop(opts)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyEnvironmentStop(r.Context(), plan, EnvironmentApplyOptions{Operator: api.EnvOperator})
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "environment/stop/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveEnvironmentCleanPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeEnvironmentActionAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts, err := environmentActionOptionsFromAPIRequest(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanEnvironmentClean(opts)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "environment/clean/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveEnvironmentCleanApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeEnvironmentActionAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts, err := environmentActionOptionsFromAPIRequest(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanEnvironmentClean(opts)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyEnvironmentClean(r.Context(), plan, EnvironmentApplyOptions{Operator: api.EnvOperator})
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "environment/clean/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveCommandProxyPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeCommandProxyAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanCommandProxy(commandProxyOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "profile/command-proxy/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveCommandProxyApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeCommandProxyAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanCommandProxy(commandProxyOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyCommandProxy(plan)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "profile/command-proxy/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveProfileHostFSPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeProfileHostFSAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanProfileHostFS(profileHostFSOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "profile/hostfs/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveProfileHostFSApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeProfileHostFSAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanProfileHostFS(profileHostFSOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyProfileHostFS(plan)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "profile/hostfs/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveProfileEnvPlan(w http.ResponseWriter, r *http.Request) {
	req, err := decodeProfileEnvAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanProfileEnv(profileEnvOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "profile/env/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveProfileEnvApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeProfileEnvAPIRequest(w, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Core.PlanProfileEnv(profileEnvOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, applyErr := api.Core.ApplyProfileEnv(plan)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "profile/env/apply",
		Data:     result,
		Errors:   []string{},
	}
	if applyErr != nil {
		resp.Errors = []string{applyErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) serveRunStatus(w http.ResponseWriter, r *http.Request, overview Overview, overviewErr error) {
	sessions := nonNilSlice(overview.Sessions)
	if rawProfile := strings.TrimSpace(r.URL.Query().Get("profile")); rawProfile != "" {
		if err := profile.ValidateName(rawProfile); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid profile name")
			return
		}
		filtered := make([]SessionSummary, 0, len(sessions))
		for _, summary := range sessions {
			if summary.Profile == rawProfile {
				filtered = append(filtered, summary)
			}
		}
		sessions = nonNilSlice(filtered)
	}
	if rawSession := r.URL.Query().Get("session"); rawSession != "" {
		if !session.ValidID(rawSession) {
			writeAPIError(w, http.StatusBadRequest, "invalid session id")
			return
		}
		var filtered []SessionSummary
		for _, summary := range sessions {
			if summary.ID == rawSession {
				filtered = append(filtered, summary)
				break
			}
		}
		sessions = nonNilSlice(filtered)
	}
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "run/status",
		Data:     RunStatusResponse{Sessions: sessions},
		Errors:   []string{},
	}
	if overviewErr != nil {
		resp.Errors = []string{overviewErr.Error()}
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func decodeRunAPIRequest(w http.ResponseWriter, r *http.Request) (RunAPIRequest, error) {
	var req RunAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid run request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid run request")
	}
	if len(req.Command) == 0 {
		return req, errors.New("command is required")
	}
	return req, nil
}

func decodeRuntimeVerifyAPIRequest(w http.ResponseWriter, r *http.Request) (RuntimeVerifyAPIRequest, error) {
	var req RuntimeVerifyAPIRequest
	if err := decodeStrictJSON(w, r, &req, "invalid runtime verify request"); err != nil {
		return req, err
	}
	if strings.TrimSpace(req.EnvironmentName) == "" && req.Plan == nil {
		return req, errors.New("runtime verify request requires environmentName or plan")
	}
	return req, nil
}

func decodeInitAPIRequest(w http.ResponseWriter, r *http.Request) (InitAPIRequest, error) {
	var req InitAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid init request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid init request")
	}
	return req, nil
}

func decodeEnvironmentActionAPIRequest(w http.ResponseWriter, r *http.Request) (EnvironmentActionAPIRequest, error) {
	var req EnvironmentActionAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid environment action request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid environment action request")
	}
	return req, nil
}

func decodeCommandProxyAPIRequest(w http.ResponseWriter, r *http.Request) (CommandProxyAPIRequest, error) {
	var req CommandProxyAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid command-proxy request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid command-proxy request")
	}
	return req, nil
}

func decodeAdapterPackAPIRequest(w http.ResponseWriter, r *http.Request) (AdapterPackAPIRequest, error) {
	var req AdapterPackAPIRequest
	if err := decodeStrictJSON(w, r, &req, "invalid adapter-pack request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeHostAppPackAPIRequest(w http.ResponseWriter, r *http.Request) (HostAppPackAPIRequest, error) {
	var req HostAppPackAPIRequest
	if err := decodeStrictJSON(w, r, &req, "invalid host-app request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeProfileHostFSAPIRequest(w http.ResponseWriter, r *http.Request) (ProfileHostFSAPIRequest, error) {
	var req ProfileHostFSAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid profile-hostfs request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid profile-hostfs request")
	}
	return req, nil
}

func decodeHostFSWritePlanRequest(w http.ResponseWriter, r *http.Request) (HostFSWritePlanRequest, error) {
	var req HostFSWritePlanRequest
	if err := decodeStrictJSON(w, r, &req, "invalid hostfs-write plan request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeHostFSWriteClaimRequest(w http.ResponseWriter, r *http.Request) (HostFSWriteClaimRequest, error) {
	var req HostFSWriteClaimRequest
	if err := decodeStrictJSON(w, r, &req, "invalid hostfs-write claim request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeHostFSWriteApplyRequest(w http.ResponseWriter, r *http.Request) (HostFSWriteApplyRequest, error) {
	var req HostFSWriteApplyRequest
	if err := decodeStrictJSON(w, r, &req, "invalid hostfs-write apply request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeHostFSWriteDiscardRequest(w http.ResponseWriter, r *http.Request) (HostFSWriteDiscardRequest, error) {
	var req HostFSWriteDiscardRequest
	if err := decodeStrictJSON(w, r, &req, "invalid hostfs-write discard request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeDecisionClaimRequest(w http.ResponseWriter, r *http.Request) (DecisionClaimRequest, error) {
	var req DecisionClaimRequest
	if err := decodeStrictJSON(w, r, &req, "invalid decision claim request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeDecisionResolveRequest(w http.ResponseWriter, r *http.Request) (DecisionResolveRequest, error) {
	var req DecisionResolveRequest
	if err := decodeStrictJSON(w, r, &req, "invalid decision resolve request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeDecisionRevokeRequest(w http.ResponseWriter, r *http.Request) (DecisionRevokeRequest, error) {
	var req DecisionRevokeRequest
	if err := decodeStrictJSON(w, r, &req, "invalid decision revoke request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeHostFSReadReopenRequest(w http.ResponseWriter, r *http.Request) (HostFSReadReopenRequest, error) {
	var req HostFSReadReopenRequest
	if err := decodeStrictJSON(w, r, &req, "invalid decision reopen request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeNoticeAckRequest(w http.ResponseWriter, r *http.Request) (NoticeAckRequest, error) {
	var req NoticeAckRequest
	if err := decodeStrictJSON(w, r, &req, "invalid notice ack request"); err != nil {
		return req, err
	}
	return req, nil
}

func decodeProfileEnvAPIRequest(w http.ResponseWriter, r *http.Request) (ProfileEnvAPIRequest, error) {
	var req ProfileEnvAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid profile-env request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid profile-env request")
	}
	return req, nil
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, out any, message string) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return errors.New(message)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return errors.New(message)
	}
	return nil
}

func decodeExportAPIRequest(w http.ResponseWriter, r *http.Request) (ExportAPIRequest, error) {
	var req ExportAPIRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, errors.New("invalid export request")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return req, errors.New("invalid export request")
	}
	return req, nil
}

func initOptionsFromAPIRequest(req InitAPIRequest) inittask.Options {
	return inittask.Options{
		ProfileName:                req.ProfileName,
		Backend:                    req.Backend,
		Network:                    req.Network,
		ProxySecretRef:             req.ProxySecretRef,
		MediatedResolver:           req.MediatedResolver,
		TemplateID:                 req.TemplateID,
		PrivilegeStatus:            req.PrivilegeStatus,
		PrivilegeReason:            req.PrivilegeReason,
		PrivilegeGuidance:          req.PrivilegeGuidance,
		PrivilegeSource:            req.PrivilegeSource,
		AllowDegradedTemplate:      req.AllowDegradedTemplate,
		Onboarding:                 req.TemplateID != "",
		ExplicitProfile:            req.ProfileName != "",
		ExplicitTemplate:           req.TemplateID != "",
		ExplicitBackend:            req.Backend != "",
		ExplicitNetwork:            req.Network != "",
		NoInput:                    true,
		VisibilitySelection:        req.HostFSVisibility,
		VisibilityRoots:            append([]string(nil), req.HostFSVisibilityRoots...),
		NameDisclosureAcknowledged: req.NameDisclosureAcknowledged,
		ExplicitVisibility:         req.HostFSVisibility != "",
		RuntimeFamily:              req.RuntimeFamily,
		ImageRef:                   req.ImageRef,
	}
}

func runPlanOptionsFromAPIRequest(req RunAPIRequest) RunPlanOptions {
	return runPlanOptionsFromServiceRequest(runServiceRequestFromAPI(req))
}

func runServiceRequestFromAPI(req RunAPIRequest) RunServiceRequest {
	return RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: req.ProfileName, Backend: req.Backend,
		NetworkMode: req.NetworkMode, ProxySecretRef: req.ProxySecretRef,
		MediatedResolver: req.MediatedResolver, Workspace: req.Workspace,
		GuestWorkspace: req.GuestWorkspace, AllowUnsafeWorkspace: req.AllowUnsafeWorkspace,
		AllowWeakIsolation: req.AllowWeakIsolation, Ephemeral: req.Ephemeral,
		EnvironmentName: req.EnvironmentName, RemoveEnvironment: req.RemoveEnvironment,
		Command: append([]string(nil), req.Command...), PublicEnv: cloneRunStringMap(req.PublicEnv),
		AuditPath: req.AuditPath, HostFSRun: req.HostFSRun,
		DisableProfileHostFSGrants: req.DisableProfileHostFSGrants,
		OpenTargets:                append([]RunOpenTargetOwner(nil), req.OpenTargets...),
		EndpointCandidates:         append([]RunEndpointCandidate(nil), req.EndpointCandidates...),
		EndpointExposures:          append([]RunEndpointExposureRequest(nil), req.EndpointExposures...),
		Terminal:                   req.Terminal, Confirmation: req.Confirmation,
	}
}

func (api API) bindRunCredentialContext(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	token := bearerToken(r.Header.Get("Authorization"))
	if !api.validCredential(token) {
		token = strings.TrimSpace(r.Header.Get("X-Hideout-UI-Token"))
	}
	if token == "" {
		cancel()
		return ctx, cancel
	}
	interval := api.RunCredentialPollInterval
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !api.validCredential(token) {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func (api API) validCredential(token string) bool {
	if api.TokenValidator != nil {
		return api.TokenValidator(token)
	}
	now := time.Now().UTC()
	if api.Now != nil {
		now = api.Now().UTC()
	}
	return (api.ExpiresAt.IsZero() || now.Before(api.ExpiresAt)) && tokenEqual(token, api.Token)
}

func commandProxyOptionsFromAPIRequest(req CommandProxyAPIRequest) CommandProxyOptions {
	return CommandProxyOptions{
		ProfileName: req.ProfileName,
		Operation:   req.Operation,
		Command:     req.Command,
	}
}

func adapterPackOptionsFromAPIRequest(req AdapterPackAPIRequest) AdapterPackOptions {
	return AdapterPackOptions{
		Operation:                   req.Operation,
		SourceKind:                  req.SourceKind,
		SourcePath:                  req.SourcePath,
		SourceURL:                   req.SourceURL,
		SourceCommit:                req.SourceCommit,
		ProfileName:                 req.ProfileName,
		PackID:                      req.PackID,
		RevisionID:                  req.RevisionID,
		AdapterID:                   req.AdapterID,
		CommandAdapterID:            req.CommandAdapterID,
		Commands:                    append([]string(nil), req.Commands...),
		AllowedProposalCapabilities: append([]string(nil), req.AllowedProposalCapabilities...),
	}
}

func hostAppPackOptionsFromAPIRequest(req HostAppPackAPIRequest) HostAppPackOptions {
	return HostAppPackOptions{
		Operation: req.Operation, SourceKind: req.SourceKind, SourcePath: req.SourcePath,
		SourceURL: req.SourceURL, SourceCommit: req.SourceCommit, ProfileName: req.ProfileName,
		PackID: req.PackID, RevisionID: req.RevisionID, BindingIDs: append([]string(nil), req.BindingIDs...),
		Access: req.Access, Replacements: cloneStringMap(req.Replacements), ExpectedDigest: req.ExpectedDigest,
		InstallOnly: req.InstallOnly,
	}
}

func hostAppPackOptionsFromReviewedPlan(plan HostAppPackPlan) HostAppPackOptions {
	return HostAppPackOptions{
		Operation:      plan.Operation,
		SourceKind:     plan.Source.Kind,
		SourcePath:     plan.Source.Path,
		SourceURL:      plan.Source.URL,
		SourceCommit:   plan.Source.Commit,
		ProfileName:    plan.Profile,
		PackID:         plan.PackID,
		RevisionID:     plan.RevisionID,
		BindingIDs:     append([]string(nil), plan.BindingIDs...),
		Access:         plan.Access,
		Replacements:   cloneStringMap(plan.Replacements),
		ExpectedDigest: plan.ExpectedSourceDigest,
		InstallOnly:    plan.InstallOnly,
		Reason:         plan.Reason,
	}
}

func profileHostFSOptionsFromAPIRequest(req ProfileHostFSAPIRequest) ProfileHostFSOptions {
	return ProfileHostFSOptions{
		ProfileName: req.ProfileName,
		Operation:   req.Operation,
		Rule:        req.Rule,
		RuleID:      req.RuleID,
		Reason:      req.Reason,
		Migrations:  append([]ProfileHostFSMigration(nil), req.Migrations...),
	}
}

func profileEnvOptionsFromAPIRequest(req ProfileEnvAPIRequest) ProfileEnvOptions {
	return ProfileEnvOptions{
		ProfileName: req.ProfileName,
		Operation:   req.Operation,
		Name:        req.Name,
		Value:       req.Value,
	}
}

func exportOptionsFromAPIRequest(req ExportAPIRequest) ExportOptions {
	source := exportboundary.SourceKind(req.Source)
	if source == "" {
		source = exportboundary.SourceAudit
	}
	return ExportOptions{
		Source:                  source,
		Session:                 req.Session,
		Profile:                 req.Profile,
		Action:                  req.Action,
		Decision:                req.Decision,
		Limit:                   req.Limit,
		BundlePath:              req.BundlePath,
		DoctorReportPath:        req.DoctorReportPath,
		From:                    req.From,
		Out:                     req.Out,
		Redact:                  append([]string(nil), req.Redact...),
		PolicyProfile:           req.PolicyProfile,
		AcknowledgeFullFidelity: req.AcknowledgeFullFidelity,
		Share:                   req.Share,
	}
}

func environmentActionOptionsFromAPIRequest(req EnvironmentActionAPIRequest) (EnvironmentActionOptions, error) {
	opts := EnvironmentActionOptions{
		IDs:         append([]string(nil), req.IDs...),
		StoppedOnly: req.StoppedOnly,
	}
	if req.Idle != "" {
		idle, err := time.ParseDuration(req.Idle)
		if err != nil || idle < 0 {
			return opts, errors.New("idle must be a non-negative duration")
		}
		opts.Idle = idle
		opts.IdleSet = true
	}
	return opts, nil
}

func (api API) serveAuditEvents(w http.ResponseWriter, r *http.Request) {
	filter, err := auditEventFilterFromQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	events, auditErr := api.Core.AuditEvents(filter)
	resp := APIResponse{
		Version:  APIVersion,
		Resource: "audit/events",
		Data:     events,
		Errors:   []string{},
	}
	if auditErr != nil {
		resp.Errors = append(resp.Errors, auditErr.Error())
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (api API) authorize(r *http.Request) error {
	if api.TokenValidator != nil {
		if api.TokenValidator(bearerToken(r.Header.Get("Authorization"))) {
			return nil
		}
		if api.TokenValidator(r.Header.Get("X-Hideout-UI-Token")) {
			return nil
		}
		return errors.New("manager API token is required")
	}
	if api.Token == "" {
		return errors.New("manager API token is not configured")
	}
	now := time.Now().UTC()
	if api.Now != nil {
		now = api.Now().UTC()
	}
	if !api.ExpiresAt.IsZero() && !now.Before(api.ExpiresAt) {
		return errors.New("manager API token expired")
	}
	if tokenEqual(bearerToken(r.Header.Get("Authorization")), api.Token) {
		return nil
	}
	if tokenEqual(r.Header.Get("X-Hideout-UI-Token"), api.Token) {
		return nil
	}
	return errors.New("manager API token is required")
}

func tokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerToken(value string) string {
	prefix := "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func (api API) checkOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	for _, allowed := range api.AllowedOrigins {
		if origin == allowed {
			return nil
		}
	}
	return errors.New("origin is not allowed")
}

func (api API) checkHost(r *http.Request) error {
	return checkAllowedHost(r.Host, api.AllowedHosts)
}

func checkAllowedHost(requestHost string, allowedHosts []string) error {
	host := strings.TrimSpace(requestHost)
	if host == "" {
		return errors.New("host header is required")
	}
	if len(allowedHosts) == 0 {
		name, err := hostName(host)
		if err != nil {
			return err
		}
		switch name {
		case "127.0.0.1", "localhost", "::1":
			return nil
		default:
			return errors.New("host is not allowed")
		}
	}
	for _, allowed := range allowedHosts {
		ok, err := hostMatchesAllowed(host, allowed)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return errors.New("host is not allowed")
}

func hostMatchesAllowed(host, allowed string) (bool, error) {
	hostName, hostPort, err := splitHostPortOptional(host)
	if err != nil {
		return false, err
	}
	allowedName, allowedPort, err := splitHostPortOptional(allowed)
	if err != nil {
		return false, err
	}
	if hostName != allowedName {
		return false, nil
	}
	return allowedPort == "" || hostPort == allowedPort, nil
}

func hostName(host string) (string, error) {
	name, _, err := splitHostPortOptional(host)
	return name, err
}

func splitHostPortOptional(value string) (string, string, error) {
	raw := strings.TrimSpace(strings.ToLower(value))
	if raw == "" {
		return "", "", errors.New("host header is required")
	}
	if strings.ContainsAny(raw, `/\@`) {
		return "", "", errors.New("host header is invalid")
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
		if host == "" || port == "" {
			return "", "", errors.New("host header is invalid")
		}
		return host, port, nil
	}
	host := strings.TrimSuffix(strings.Trim(raw, "[]"), ".")
	if host == "" {
		return "", "", errors.New("host header is invalid")
	}
	return host, "", nil
}

func overviewResource(overview Overview, resource string) (any, bool) {
	switch resource {
	case "overview":
		return overview, true
	case "profiles":
		return nonNilSlice(overview.Profiles), true
	case "sessions":
		return nonNilSlice(overview.Sessions), true
	case "environments":
		return nonNilSlice(overview.Environments), true
	case "backends":
		return nonNilSlice(overview.Backends), true
	case "capabilities":
		return overview.Capabilities, true
	case "broker":
		return overview.Broker, true
	case "network":
		return overview.Network, true
	case "secrets":
		return nonNilSlice(overview.Secrets), true
	case "audit":
		return overview.Audit, true
	case "settings":
		return overview.Settings, true
	case "init":
		return overview.Init, true
	case "bundles":
		return overview.Bundles, true
	case "projects":
		return overview.Projects, true
	case "adapter-packs":
		return nonNilSlice(overview.AdapterPacks), true
	case "decisions":
		return overview.Decisions, true
	case "notices":
		return overview.Notices, true
	case "decision/status":
		return overview.DecisionStatus, true
	default:
		return nil, false
	}
}

func auditEventFilterFromQuery(r *http.Request) (AuditEventFilter, error) {
	q := r.URL.Query()
	filter := AuditEventFilter{
		Session:  q.Get("session"),
		Profile:  q.Get("profile"),
		Action:   q.Get("action"),
		Decision: q.Get("decision"),
	}
	if filter.Session != "" && !session.ValidID(filter.Session) {
		return filter, errors.New("invalid session id")
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			return filter, errors.New("limit must be between 1 and 1000")
		}
		filter.Limit = limit
	}
	return filter, nil
}

func nonNilSlice[S ~[]E, E any](value S) S {
	if value == nil {
		return S{}
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeAPIJSON(w, status, APIResponse{
		Version: APIVersion,
		Errors:  []string{message},
	})
}

func writeAPIMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	if len(methods) == 0 {
		methods = []string{http.MethodGet}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
