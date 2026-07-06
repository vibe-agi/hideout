package manager

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/session"
)

const APIVersion = "hideout.manager-api/v1"

type API struct {
	Core           Core
	Token          string
	ExpiresAt      time.Time
	AllowedOrigins []string
	AllowedHosts   []string
	Now            func() time.Time
	RunBackend     RunBackendFactory
	RunOpener      RunOpenerFactory
	EnvOperator    EnvironmentOperator
}

type APIResponse struct {
	Version  string   `json:"version"`
	Resource string   `json:"resource,omitempty"`
	Data     any      `json:"data,omitempty"`
	Errors   []string `json:"errors"`
}

type RunBackendFactory func(RunAPIRequest, RunPlan) (backend.Backend, error)
type RunOpenerFactory func(RunAPIRequest, RunPlan, RunSession) broker.Opener

type RunAPIRequest struct {
	ProfileName        string   `json:"profile,omitempty"`
	Backend            string   `json:"backend,omitempty"`
	NetworkMode        string   `json:"networkMode,omitempty"`
	ProxySecretRef     string   `json:"proxySecretRef,omitempty"`
	Workspace          string   `json:"workspace,omitempty"`
	GuestWorkspace     string   `json:"guestWorkspace,omitempty"`
	Ephemeral          bool     `json:"ephemeral,omitempty"`
	Command            []string `json:"command"`
	AllowWeakIsolation bool     `json:"allowWeakIsolation,omitempty"`
	EnvironmentName    string   `json:"environmentName,omitempty"`
	RemoveEnvironment  bool     `json:"removeEnvironment,omitempty"`
}

type RunStatusResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

type InitAPIRequest struct {
	ProfileName    string `json:"profile,omitempty"`
	Backend        string `json:"backend,omitempty"`
	Network        string `json:"network,omitempty"`
	ProxySecretRef string `json:"proxySecretRef,omitempty"`
	DryRun         bool   `json:"dryRun,omitempty"`
}

type EnvironmentActionAPIRequest struct {
	IDs         []string `json:"ids,omitempty"`
	Idle        string   `json:"idle,omitempty"`
	StoppedOnly bool     `json:"stoppedOnly,omitempty"`
}

type CommandProxyAPIRequest struct {
	ProfileName string `json:"profile,omitempty"`
	Operation   string `json:"operation"`
	Command     string `json:"command"`
}

type ProfileHostFSAPIRequest struct {
	ProfileName string `json:"profile,omitempty"`
	Operation   string `json:"operation"`
	Rule        string `json:"rule,omitempty"`
	RuleID      string `json:"ruleId,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type ProfileEnvAPIRequest struct {
	ProfileName string `json:"profile,omitempty"`
	Operation   string `json:"operation"`
	Name        string `json:"name,omitempty"`
	Value       string `json:"value,omitempty"`
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
		api.servePostResource(w, r, resource)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if resource == "audit/events" {
		api.serveAuditEvents(w, r)
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

func (api API) servePostResource(w http.ResponseWriter, r *http.Request, resource string) {
	switch resource {
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
	default:
		writeAPIMethodNotAllowed(w, http.MethodGet)
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
	plan, err := api.Core.PlanRun(runPlanOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "run/plan",
		Data:     plan,
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
	plan, err := api.Core.PlanRun(runPlanOptionsFromAPIRequest(req))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	be, err := api.RunBackend(req, plan)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, runErr := api.Core.ApplyRun(r.Context(), plan, ApplyRunOptions{
		Backend:            be,
		RequestedBackend:   req.Backend,
		AllowWeakIsolation: req.AllowWeakIsolation,
		Environment: RunEnvironmentOptions{
			EnvName:        req.EnvironmentName,
			RemoveAfterRun: req.RemoveEnvironment,
			Create:         true,
		},
		OpenerForSession: api.runOpenerForSession(req, plan),
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

func initOptionsFromAPIRequest(req InitAPIRequest) inittask.Options {
	return inittask.Options{
		ProfileName:    req.ProfileName,
		Backend:        req.Backend,
		Network:        req.Network,
		ProxySecretRef: req.ProxySecretRef,
		NoInput:        true,
	}
}

func runPlanOptionsFromAPIRequest(req RunAPIRequest) RunPlanOptions {
	return RunPlanOptions{
		ProfileName:    req.ProfileName,
		Backend:        req.Backend,
		NetworkMode:    req.NetworkMode,
		ProxySecretRef: req.ProxySecretRef,
		Workspace:      req.Workspace,
		GuestWorkspace: req.GuestWorkspace,
		Ephemeral:      req.Ephemeral,
		Command:        append([]string(nil), req.Command...),
	}
}

func commandProxyOptionsFromAPIRequest(req CommandProxyAPIRequest) CommandProxyOptions {
	return CommandProxyOptions{
		ProfileName: req.ProfileName,
		Operation:   req.Operation,
		Command:     req.Command,
	}
}

func profileHostFSOptionsFromAPIRequest(req ProfileHostFSAPIRequest) ProfileHostFSOptions {
	return ProfileHostFSOptions{
		ProfileName: req.ProfileName,
		Operation:   req.Operation,
		Rule:        req.Rule,
		RuleID:      req.RuleID,
		Reason:      req.Reason,
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
