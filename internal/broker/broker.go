package broker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	EnvEndpoint = "HIDEOUT_BROKER_ENDPOINT"
	EnvSock     = "HIDEOUT_BROKER_SOCK"
	EnvSession  = "HIDEOUT_SESSION_ID"
	EnvToken    = "HIDEOUT_CAPABILITY_TOKEN"
)

const (
	EndpointUnix = "unix"
	EndpointTCP  = "tcp"
)

const (
	clientOpenDefaultTimeout    = 5 * time.Second
	serverRequestDefaultTimeout = 5 * time.Second
	hostAppOpenRequestTimeout   = 20 * time.Second
)

type Endpoint struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

func UnixEndpoint(path string) Endpoint {
	return Endpoint{Network: EndpointUnix, Address: path}
}

func TCPEndpoint(address string) Endpoint {
	return Endpoint{Network: EndpointTCP, Address: address}
}

func ParseEndpoint(value string) (Endpoint, error) {
	if value == "" {
		return Endpoint{}, errors.New("broker endpoint is required")
	}
	if strings.HasPrefix(value, "unix://") {
		return UnixEndpoint(strings.TrimPrefix(value, "unix://")), nil
	}
	if strings.HasPrefix(value, "tcp://") {
		return TCPEndpoint(strings.TrimPrefix(value, "tcp://")), nil
	}
	return UnixEndpoint(value), nil
}

func (e Endpoint) String() string {
	switch e.Network {
	case EndpointUnix:
		return "unix://" + e.Address
	case EndpointTCP:
		return "tcp://" + e.Address
	default:
		return e.Network + "://" + e.Address
	}
}

type Request struct {
	ID              string         `json:"id"`
	SessionID       string         `json:"sessionId"`
	CapabilityToken string         `json:"capabilityToken"`
	Subject         string         `json:"subject,omitempty"`
	Command         string         `json:"command,omitempty"`
	Argv            []string       `json:"argv,omitempty"`
	Route           string         `json:"route,omitempty"`
	Action          string         `json:"action"`
	Args            map[string]any `json:"args"`
}

type Response struct {
	ID       string         `json:"id"`
	Decision string         `json:"decision"`
	Status   string         `json:"status"`
	ExitCode int            `json:"exitCode"`
	Stdout   string         `json:"stdout,omitempty"`
	Stderr   string         `json:"stderr,omitempty"`
	Error    *ErrorRecord   `json:"error,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

type Opener interface {
	OpenURL(ctx context.Context, target string) error
	OpenFile(ctx context.Context, target string) error
}

type browserProfileOpener interface {
	BrowserProfile() string
}

type NoopOpener struct{}

func (NoopOpener) OpenURL(context.Context, string) error  { return nil }
func (NoopOpener) OpenFile(context.Context, string) error { return nil }

type Server struct {
	SessionID           string
	Token               string
	Socket              string
	Endpoint            Endpoint
	HostRoot            string
	GuestRoot           string
	Profile             string
	ProfileDir          string
	Backend             string
	WorkspaceMode       string
	NetworkMode         string
	Commands            []string
	CommandRegistry     cmdproxy.Registry
	CommandAdapters     cmdadapter.Runtime
	ScriptRefs          []profile.ScriptRef
	Evaluator           policy.Evaluator
	Audit               *audit.Writer
	Opener              Opener
	HostFS              *hostfs.Service
	HostFSReadDecisions HostFSReadDecisionProvider
	HostApp             *hostcap.ProjectionConfig

	listener       net.Listener
	once           sync.Once
	mu             sync.Mutex
	closed         bool
	guestPrivilege privilege.Status
	handlers       sync.WaitGroup
	hostFSAuditMu  sync.Mutex
	hostFSAudit    *hostFSDiscoveryAudit
}

func NewToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "cap_" + hex.EncodeToString(b[:]), nil
}

func NewRequestID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "req_" + hex.EncodeToString(b[:]), nil
}

func (s *Server) Start(ctx context.Context) error {
	if s.SessionID == "" || s.Token == "" || s.Socket == "" {
		return errors.New("broker session, token, and socket are required")
	}
	return s.StartEndpoint(ctx, UnixEndpoint(s.Socket))
}

func (s *Server) StartEndpoint(ctx context.Context, endpoint Endpoint) error {
	if s.SessionID == "" || s.Token == "" || endpoint.Address == "" {
		return errors.New("broker session, token, and endpoint are required")
	}
	if endpoint.Network == "" {
		endpoint.Network = EndpointUnix
	}
	if endpoint.Network == EndpointUnix {
		if err := os.RemoveAll(endpoint.Address); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(endpoint.Address), 0o700); err != nil {
			return err
		}
	}
	ln, err := net.Listen(endpoint.Network, endpoint.Address)
	if err != nil {
		return err
	}
	s.listener = ln
	s.Endpoint = Endpoint{Network: endpoint.Network, Address: ln.Addr().String()}
	if endpoint.Network == EndpointUnix {
		s.Socket = endpoint.Address
		s.Endpoint.Address = endpoint.Address
	}
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	go s.serve()
	return nil
}

func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.listener != nil {
			err = s.listener.Close()
		}
		s.mu.Unlock()
		if s.Endpoint.Network == EndpointUnix && s.Endpoint.Address != "" {
			_ = os.Remove(s.Endpoint.Address)
		} else if s.Socket != "" {
			_ = os.Remove(s.Socket)
		}
	})
	s.handlers.Wait()
	return errors.Join(err, s.flushHostFSDiscoveryAudit())
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.handlers.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.handlers.Done()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(serverRequestDefaultTimeout))
	var req Request
	reader := bufio.NewReader(conn)
	if err := decodeRequest(reader, &req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Decision: string(policy.Deny), Status: "bad-request", ExitCode: 2, Stderr: "malformed broker request"})
		return
	}
	requestTimeout := serverRequestTimeout(req)
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	resp := s.Handle(ctx, req)
	_ = json.NewEncoder(conn).Encode(resp)
}

func serverRequestTimeout(req Request) time.Duration {
	if req.Action == cmdproxy.ActionHostAppOpenResource {
		return hostAppOpenRequestTimeout
	}
	return serverRequestDefaultTimeout
}

func decodeRequest(r *bufio.Reader, req *Request) error {
	data, err := r.ReadBytes('\n')
	if err != nil && len(data) == 0 {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("malformed broker request")
		}
		return err
	}
	return nil
}

func (s *Server) Handle(ctx context.Context, req Request) Response {
	resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126}
	sessionOK := req.SessionID == s.SessionID
	tokenOK := capabilityTokenEqual(req.CapabilityToken, s.Token)
	if !sessionOK || !tokenOK {
		resp.Stderr = "broker authorization failed"
		s.emit(req, resp, nil)
		return resp
	}
	if !s.CommandRegistry.Empty() && req.Command != "" && (req.Action == cmdproxy.ActionHostOpen || req.Action == cmdproxy.ActionCommandAdapter || req.Action == cmdproxy.ActionHostAppOpenResource) {
		registration, ok := s.CommandRegistry.LookupExact(req.Command)
		if ok && registration.Action != req.Action {
			resp.Stderr = "broker command action is not registered for this run"
			resp.Data = map[string]any{"outcome": "refused", "code": hostcap.CodeCommandUnbound}
			s.emit(req, resp, nil)
			return resp
		}
	}
	if isHostFSAction(req.Action) {
		return s.handleHostFS(ctx, req, resp)
	}
	if req.Action == cmdproxy.ActionCommandAdapter {
		return s.handleCommandAdapter(ctx, req, resp)
	}
	if req.Action == cmdproxy.ActionHostAppOpenResource {
		return s.handleHostAppOpen(ctx, req, resp)
	}
	if req.Action != "host.open" {
		resp.Stderr = "unsupported broker action"
		s.emit(req, resp, nil)
		return resp
	}
	if err := validateBrokerRoute(req); err != nil {
		resp.Stderr = err.Error()
		s.emit(req, resp, nil)
		return resp
	}
	target, err := s.validateBrokerRequestEnvelope(req)
	if err != nil {
		resp.Status = "bad-request"
		resp.ExitCode = 2
		resp.Stderr = err.Error()
		s.emit(req, resp, nil)
		return resp
	}
	if err := s.normalizeBrokerRequestCWD(&req); err != nil {
		resp.Status = "bad-request"
		resp.ExitCode = 2
		resp.Stderr = err.Error()
		s.emit(req, resp, nil)
		return resp
	}
	target, err = normalizeFileURLTarget(target)
	if err != nil {
		resp.Stderr = err.Error()
		s.emit(req, resp, map[string]any{"target": target})
		return resp
	}
	isURL := isHTTPURL(target)
	var hostPath string
	if !isURL {
		if cwd, _ := req.Args["cwd"].(string); cwd != "" && !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		target = filepath.Clean(target)
		hostPath, err = s.mapGuestPath(target)
		if err != nil {
			resp.Stderr = err.Error()
			s.emit(req, resp, map[string]any{"target": target})
			return resp
		}
	}
	scriptAudit, scriptResp := s.evaluateCommandScripts(ctx, req, target)
	if scriptResp != nil {
		details := map[string]any{"target": target}
		if len(scriptAudit) > 0 {
			details["policyScripts"] = scriptAudit
		}
		s.emit(req, *scriptResp, details)
		return *scriptResp
	}
	if isURL {
		proposal, err := s.Evaluator.EvaluateOpen(target)
		if err != nil {
			resp.Stderr = err.Error()
			details := map[string]any{"target": target}
			if len(scriptAudit) > 0 {
				details["policyScripts"] = scriptAudit
			}
			s.emit(req, resp, details)
			return resp
		}
		if proposal.Decision != policy.Allow {
			resp.Stderr = proposal.Reason
			details := map[string]any{"target": target}
			if len(scriptAudit) > 0 {
				details["policyScripts"] = scriptAudit
			}
			s.emit(req, resp, details)
			return resp
		}
	} else {
		proposal, err := s.Evaluator.EvaluateWorkspaceOpen()
		if err != nil {
			resp.Stderr = err.Error()
			details := map[string]any{"target": target}
			if len(scriptAudit) > 0 {
				details["policyScripts"] = scriptAudit
			}
			s.emit(req, resp, details)
			return resp
		}
		if proposal.Decision != policy.Allow {
			resp.Stderr = proposal.Reason
			details := map[string]any{"target": target}
			if len(scriptAudit) > 0 {
				details["policyScripts"] = scriptAudit
			}
			s.emit(req, resp, details)
			return resp
		}
	}
	details := map[string]any{"target": target}
	if len(scriptAudit) > 0 {
		details["policyScripts"] = scriptAudit
	}
	if !isURL {
		details["resourceType"] = "workspace-file"
		details["hostPath"] = hostPath
	} else {
		details["resourceType"] = "url"
		details["portBridge"] = "none"
		details["browserControl"] = "disabled"
		details["remoteDebugging"] = "not-exposed"
		if browserProfile := s.browserProfile(); browserProfile != "" {
			details["browserProfileMode"] = "isolated"
			details["browserProfile"] = "present"
		}
	}
	resp.Decision = string(policy.Allow)
	resp.Status = "ok"
	resp.ExitCode = 0
	var preparedDetails map[string]any
	if s.Audit != nil {
		var err error
		preparedDetails, err = s.prepareAuditDetails(req, resp, details)
		if err != nil {
			resp.Decision = string(policy.Deny)
			resp.Status = "denied"
			resp.ExitCode = 126
			resp.Stderr = err.Error()
			s.emitAuditRedactionFailure(req, resp, err)
			return resp
		}
	}
	if err := s.openMapped(ctx, target, hostPath); err != nil {
		resp.Decision = string(policy.Deny)
		resp.Status = "error"
		resp.Stderr = hostOpenFailureMessage(err, isURL)
		resp.ExitCode = 1
		if !isURL {
			delete(details, "hostPath")
		}
		details["status"] = resp.Status
		details["error"] = resp.Stderr
		s.emit(req, resp, details)
		return resp
	}
	if s.Audit != nil {
		s.emitPrepared(req, resp, preparedDetails)
	}
	return resp
}

func (s *Server) handleCommandAdapter(ctx context.Context, req Request, resp Response) Response {
	inv, adapter, err := s.validateCommandAdapterRequest(req)
	if err != nil {
		resp.Status = "bad-request"
		resp.ExitCode = 2
		resp.Stderr = err.Error()
		s.emit(req, resp, cmdadapter.FailureEvidence(inv, err.Error()))
		return resp
	}
	evalCtx := s.commandAdapterContext(req, adapter)
	outcome, err := cmdadapter.Evaluate(ctx, s.Evaluator, adapter, evalCtx)
	if err != nil {
		reason := fmt.Sprintf("command adapter %s: %s", adapter.ID, err)
		resp.Stderr = reason
		s.emit(req, resp, cmdadapter.FailureEvidence(inv, reason))
		return resp
	}
	if outcome.ExitCode == 0 && outcome.Outcome == cmdadapter.OutcomeDeny {
		outcome.ExitCode = 126
	}
	details := cmdadapter.Evidence(inv, outcome)
	if adapter.RootSensitive {
		status := s.GuestPrivilegeStatus()
		details["separationStatus"] = string(status.Status)
		details["nonClaim"] = privilege.NonClaim(status.Status)
		if intent, ok := details["intent"].(map[string]any); ok {
			intent["separationStatus"] = string(status.Status)
			intent["nonClaim"] = privilege.NonClaim(status.Status)
		}
		defer func() {
			s.emitAction(privilege.ActionTargetRootAttempt, req, resp, privilege.TargetRootAttemptDetails(req.Command, req.Argv, status, adapter.ID, resp.Decision, firstNonEmptyString(outcome.Reason, resp.Stderr)))
		}()
	}
	switch outcome.Outcome {
	case cmdadapter.OutcomeDeny:
		resp.Decision = string(policy.Deny)
		resp.Status = "denied"
		resp.ExitCode = outcome.ExitCode
		resp.Stderr = outcome.Stderr
		if resp.Stderr == "" {
			resp.Stderr = outcome.Reason
		}
	case cmdadapter.OutcomeSimulate:
		resp.Decision = string(policy.AuditOnly)
		resp.Status = "simulated"
		resp.ExitCode = outcome.ExitCode
		resp.Stdout = outcome.Stdout
		resp.Stderr = outcome.Stderr
	case cmdadapter.OutcomeRewriteGuest:
		resp.Decision = string(policy.AuditOnly)
		resp.Status = "rewrite-guest"
		resp.ExitCode = 0
		resp.Data = map[string]any{"rewriteArgv": append([]string(nil), outcome.Argv...)}
		if outcome.CWD != "" {
			resp.Data["rewriteCwd"] = outcome.CWD
		}
	case cmdadapter.OutcomeProposeCapability:
		resp.Decision = string(policy.AuditOnly)
		resp.Status = "proposal-unavailable"
		resp.ExitCode = 126
		resp.Stderr = outcome.Reason
		resp.Data = map[string]any{
			"proposal": map[string]any{
				"capability": outcome.Capability,
				"intent":     outcome.Intent,
				"status":     "non-applied",
			},
		}
	default:
		resp.Stderr = "unsupported command adapter outcome"
	}
	s.emit(req, resp, details)
	return resp
}

func (s *Server) SetGuestPrivilegeStatus(status privilege.Status) {
	s.mu.Lock()
	s.guestPrivilege = status
	s.mu.Unlock()
}

func (s *Server) GuestPrivilegeStatus() privilege.Status {
	s.mu.Lock()
	status := s.guestPrivilege
	s.mu.Unlock()
	if status.Status == "" {
		status.Status = privilege.StatusUnknown
		status.Reason = "guest privilege status has not been emitted"
		status.Guidance = "wait for guest privilege status before trusting adapter context"
	}
	return status
}

func (s *Server) validateCommandAdapterRequest(req Request) (cmdadapter.Invocation, cmdadapter.RuntimeAdapter, error) {
	inv := cmdadapter.Invocation{
		Profile: s.Profile,
		Session: s.SessionID,
		Command: req.Command,
		Argv:    append([]string(nil), req.Argv...),
	}
	if err := cmdadapter.ValidateRuntime(s.CommandAdapters); err != nil {
		return inv, cmdadapter.RuntimeAdapter{}, err
	}
	if req.Route != cmdproxy.RouteCommandAdapter {
		return inv, cmdadapter.RuntimeAdapter{}, fmt.Errorf("command adapter route %q is not allowed", req.Route)
	}
	if strings.TrimSpace(req.ID) == "" {
		return inv, cmdadapter.RuntimeAdapter{}, errors.New("broker request id is required")
	}
	if err := s.validateRegisteredCommand(req); err != nil {
		return inv, cmdadapter.RuntimeAdapter{}, err
	}
	if len(req.Argv) == 0 {
		return inv, cmdadapter.RuntimeAdapter{}, errors.New("broker request argv is required")
	}
	cwd, _ := req.Args["cwd"].(string)
	if strings.TrimSpace(cwd) == "" {
		return inv, cmdadapter.RuntimeAdapter{}, errors.New("command adapter request args.cwd is required")
	}
	if !filepath.IsAbs(cwd) || hasURLScheme(cwd) {
		return inv, cmdadapter.RuntimeAdapter{}, errors.New("command adapter request args.cwd must be an absolute guest path")
	}
	inv.CWD = filepath.Clean(cwd)
	adapterID, _ := req.Args["adapterId"].(string)
	adapter, ok := cmdadapter.AdapterForCommand(s.CommandAdapters, req.Command)
	if !ok {
		return inv, cmdadapter.RuntimeAdapter{}, fmt.Errorf("command adapter for command %q is not enabled by profile", req.Command)
	}
	if adapterID != "" && adapterID != adapter.ID {
		return inv, cmdadapter.RuntimeAdapter{}, fmt.Errorf("command adapter id %q does not own command %q", adapterID, req.Command)
	}
	inv.Adapter = adapter
	return inv, adapter, nil
}

func (s *Server) commandAdapterContext(req Request, adapter cmdadapter.RuntimeAdapter) cmdadapter.EvalContext {
	workspaceMode := s.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = "read-write"
	}
	status := s.GuestPrivilegeStatus()
	return cmdadapter.EvalContext{
		Version: "command-adapter/v1",
		Profile: map[string]string{
			"name": s.Profile,
		},
		Session: map[string]any{
			"id":               s.SessionID,
			"backend":          s.Backend,
			"separationStatus": string(status.Status),
			"nonClaim":         privilege.NonClaim(status.Status),
		},
		Adapter: map[string]any{
			"id":     adapter.ID,
			"digest": adapter.Digest,
		},
		Command: map[string]any{
			"name": req.Command,
			"argv": append([]string(nil), req.Argv...),
			"cwd":  req.Args["cwd"],
		},
		Env: map[string]any{
			"keys":    []string{},
			"classes": []string{},
			"count":   0,
		},
		Workspace: map[string]any{
			"guestRoot": s.GuestRoot,
			"mode":      workspaceMode,
		},
		Network: map[string]any{
			"mode": s.NetworkMode,
		},
	}
}

func capabilityTokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func hostOpenFailureMessage(err error, isURL bool) string {
	if err == nil {
		return ""
	}
	if err.Error() == "host opener is not configured" {
		return err.Error()
	}
	if isURL {
		return "host URL opener failed"
	}
	return "host workspace file opener failed"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateBrokerRoute(req Request) error {
	if req.Route == "" {
		return errors.New("broker request route is required")
	}
	if req.Route != string(policy.HostBroker) {
		return fmt.Errorf("broker request route %q is not allowed", req.Route)
	}
	return nil
}

func (s *Server) validateBrokerRequestEnvelope(req Request) (string, error) {
	if strings.TrimSpace(req.ID) == "" {
		return "", errors.New("broker request id is required")
	}
	if err := s.validateRegisteredCommand(req); err != nil {
		return "", err
	}
	for key := range req.Args {
		switch key {
		case "target", "cwd":
		default:
			return "", fmt.Errorf("broker request args.%s is not supported", key)
		}
	}
	target, ok := req.Args["target"].(string)
	if !ok || strings.TrimSpace(target) == "" {
		return "", errors.New("broker request args.target is required")
	}
	if err := validateOpenCommandArgv(req, target); err != nil {
		return "", err
	}
	if cwd, ok := req.Args["cwd"]; ok {
		if _, ok := cwd.(string); !ok {
			return "", errors.New("broker request args.cwd must be a string")
		}
	}
	return target, nil
}

func (s *Server) normalizeBrokerRequestCWD(req *Request) error {
	value, ok := req.Args["cwd"]
	if !ok {
		return nil
	}
	cwd, ok := value.(string)
	if !ok {
		return errors.New("broker request args.cwd must be a string")
	}
	if strings.TrimSpace(cwd) == "" {
		return errors.New("broker request args.cwd is empty")
	}
	if hasURLScheme(cwd) {
		return errors.New("broker request args.cwd must be a guest workspace path")
	}
	clean := filepath.Clean(cwd)
	if !filepath.IsAbs(clean) {
		return errors.New("broker request args.cwd must be an absolute guest path")
	}
	if s.GuestRoot == "" || s.HostRoot == "" {
		return errors.New("workspace mapping is unavailable for broker request args.cwd")
	}
	rel, err := relInsideRoot(s.GuestRoot, clean)
	if err != nil || pathEscapesRoot(rel) {
		return errors.New("broker request args.cwd is outside workspace")
	}
	hostPath := filepath.Join(s.HostRoot, rel)
	hostRel, err := relInsideRoot(s.HostRoot, hostPath)
	if err != nil || pathEscapesRoot(hostRel) {
		return errors.New("broker request args.cwd maps outside workspace")
	}
	resolved, err := filepath.EvalSymlinks(hostPath)
	if err == nil {
		resolvedRoot, rootErr := filepath.EvalSymlinks(s.HostRoot)
		if rootErr != nil {
			resolvedRoot = filepath.Clean(s.HostRoot)
		}
		resolvedRel, relErr := filepath.Rel(resolvedRoot, resolved)
		if relErr != nil || pathEscapesRoot(resolvedRel) {
			return errors.New("broker request args.cwd resolves outside workspace")
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("broker request args.cwd cannot be resolved within workspace")
	}
	req.Args["cwd"] = clean
	return nil
}

func (s *Server) validateRegisteredCommand(req Request) error {
	if len(s.Commands) > 0 {
		if req.Command == "" || req.Subject == "" {
			return errors.New("broker request command metadata is required")
		}
		if len(req.Argv) == 0 {
			return errors.New("broker request argv is required")
		}
		if filepath.Base(req.Argv[0]) != req.Command {
			return fmt.Errorf("broker request argv[0] %q does not match command %q", req.Argv[0], req.Command)
		}
	}
	if req.Command == "" && req.Subject == "" {
		return nil
	}
	allowed := map[string]bool{}
	for _, command := range s.Commands {
		allowed[command] = true
	}
	if len(allowed) == 0 {
		return errors.New("broker command registry is empty")
	}
	command := req.Command
	if strings.HasPrefix(req.Subject, "command:") {
		subjectCommand := strings.TrimPrefix(req.Subject, "command:")
		if command != "" && command != subjectCommand {
			return fmt.Errorf("broker request command %q does not match subject %q", command, req.Subject)
		}
		command = subjectCommand
	}
	if command != "" && !allowed[command] {
		return fmt.Errorf("broker request command %q is not enabled by profile", command)
	}
	return nil
}

func validateOpenCommandArgv(req Request, target string) error {
	if req.Command == "" {
		return nil
	}
	if len(req.Argv) != 2 {
		return fmt.Errorf("broker request argv for %s must be [command target]", req.Command)
	}
	if req.Argv[1] != target {
		return fmt.Errorf("broker request argv target %q does not match args.target %q", req.Argv[1], target)
	}
	return nil
}

func (s *Server) evaluateCommandScripts(ctx context.Context, req Request, target string) ([]map[string]any, *Response) {
	var scripts []map[string]any
	for _, ref := range s.ScriptRefs {
		if !containsString(ref.Entrypoints, "decideCommand") {
			continue
		}
		path := ref.Path
		if !filepath.IsAbs(path) && s.ProfileDir != "" {
			path = filepath.Join(s.ProfileDir, path)
		}
		entry := map[string]any{
			"id":         ref.ID,
			"path":       ref.Path,
			"entrypoint": "decideCommand",
		}
		source, err := os.ReadFile(path)
		if err != nil {
			entry["error"] = err.Error()
			scripts = append(scripts, entry)
			resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126, Stderr: fmt.Sprintf("policy script %s: %s", ref.ID, err)}
			return scripts, &resp
		}
		sum := sha256.Sum256(source)
		entry["sha256"] = hex.EncodeToString(sum[:])
		proposal, err := s.Evaluator.RunCommandScript(string(source), "decideCommand", s.commandContext(req, target))
		if err != nil {
			entry["error"] = err.Error()
			scripts = append(scripts, entry)
			resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126, Stderr: fmt.Sprintf("policy script %s: %s", ref.ID, err)}
			return scripts, &resp
		}
		entry["decision"] = string(proposal.Decision)
		entry["route"] = string(proposal.Route)
		entry["action"] = proposal.Action
		entry["reason"] = proposal.Reason
		if err := ValidateCommandScriptProposal(req, proposal); err != nil {
			entry["error"] = err.Error()
			scripts = append(scripts, entry)
			resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126, Stderr: fmt.Sprintf("policy script %s: %s", ref.ID, err)}
			return scripts, &resp
		}
		scripts = append(scripts, entry)
		switch proposal.Decision {
		case policy.Allow:
			continue
		case policy.AuditOnly:
			reason := proposal.Reason
			if reason == "" {
				reason = "policy script requested audit-only; no host side effect executed"
			}
			resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126, Stderr: reason}
			return scripts, &resp
		case policy.Ask:
			resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126, Stderr: "policy script requested ask but no prompt channel is available"}
			return scripts, &resp
		default:
			reason := proposal.Reason
			if reason == "" {
				reason = "policy script denied request"
			}
			resp := Response{ID: req.ID, Decision: string(policy.Deny), Status: "denied", ExitCode: 126, Stderr: reason}
			return scripts, &resp
		}
	}
	return scripts, nil
}

func ValidateCommandScriptProposal(req Request, proposal policy.Proposal) error {
	if proposal.Action != req.Action {
		return fmt.Errorf("script proposal action %q does not match request action %q", proposal.Action, req.Action)
	}
	switch proposal.Decision {
	case policy.Allow, policy.AuditOnly, policy.Ask:
		if string(proposal.Route) != req.Route {
			return fmt.Errorf("script proposal route %q does not match request route %q", proposal.Route, req.Route)
		}
	}
	return nil
}

func (s *Server) commandContext(req Request, target string) policy.CommandContext {
	workspaceMode := s.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = "read-write"
	}
	resourceType := "workspace-file"
	if isHTTPURL(target) {
		resourceType = "url"
	}
	return policy.CommandContext{
		Version: "policy-script/v1",
		Profile: map[string]string{
			"name": s.Profile,
		},
		Session: map[string]any{
			"id":          s.SessionID,
			"interactive": false,
		},
		Subject: req.Subject,
		Command: map[string]any{
			"name": req.Command,
			// target and argv are canonicalized guest-authored values, provided
			// raw: the requesting process already possesses them, so redacting
			// them from the decision hook protects nothing and breaks real
			// policy such as redirect-URI parsing or query-parameter rules.
			// Control-plane credentials never enter this request shape.
			"argv":         append([]string(nil), req.Argv...),
			"cwd":          req.Args["cwd"],
			"target":       target,
			"action":       req.Action,
			"route":        req.Route,
			"resourceType": resourceType,
		},
		Workspace: map[string]any{
			"guestRoot":       s.GuestRoot,
			"hostRootVisible": false,
			"mode":            workspaceMode,
		},
		Env: map[string]any{
			"safe": map[string]string{},
		},
		Network: map[string]any{
			"mode": s.NetworkMode,
		},
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isHTTPURL(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}

func normalizeFileURLTarget(target string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(target)), "file:") {
		return target, nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return target, fmt.Errorf("file URL %q is invalid: %w", target, err)
	}
	if strings.ToLower(u.Scheme) != "file" {
		return target, nil
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return target, fmt.Errorf("file URL host %q is denied", u.Host)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return target, fmt.Errorf("file URL %q must not include query or fragment", target)
	}
	if u.Path == "" {
		return target, errors.New("file URL path is required")
	}
	if hasEncodedFilePathSeparator(target) {
		return target, fmt.Errorf("file URL %q must not include encoded path separators", target)
	}
	return u.Path, nil
}

func hasEncodedFilePathSeparator(target string) bool {
	lower := strings.ToLower(target)
	return strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c")
}

func hasURLScheme(target string) bool {
	u, err := url.Parse(target)
	return err == nil && u.Scheme != ""
}

func (s *Server) openMapped(ctx context.Context, target, hostPath string) error {
	if s.Opener == nil {
		return errors.New("host opener is not configured")
	}
	if isHTTPURL(target) {
		return s.Opener.OpenURL(ctx, target)
	}
	if hostPath == "" {
		return errors.New("mapped host path is required")
	}
	return s.Opener.OpenFile(ctx, hostPath)
}

func (s *Server) browserProfile() string {
	opener, ok := s.Opener.(browserProfileOpener)
	if !ok {
		return ""
	}
	return opener.BrowserProfile()
}

func (s *Server) mapGuestPath(target string) (string, error) {
	if s.GuestRoot == "" || s.HostRoot == "" {
		return "", errors.New("workspace mapping is unavailable")
	}
	clean := filepath.Clean(target)
	if hasURLScheme(target) {
		return "", fmt.Errorf("URL scheme in file target %q is denied", target)
	}
	if !filepath.IsAbs(clean) {
		return "", errors.New("file open requires an absolute guest path")
	}
	rel, err := relInsideRoot(s.GuestRoot, clean)
	if err != nil || pathEscapesRoot(rel) {
		return "", fmt.Errorf("file target %q is outside workspace", target)
	}
	hostPath := filepath.Join(s.HostRoot, rel)
	hostRel, err := relInsideRoot(s.HostRoot, hostPath)
	if err != nil || pathEscapesRoot(hostRel) {
		return "", fmt.Errorf("file target %q maps outside workspace", target)
	}
	resolved, err := filepath.EvalSymlinks(hostPath)
	if err == nil {
		resolvedRoot, rootErr := filepath.EvalSymlinks(s.HostRoot)
		if rootErr != nil {
			resolvedRoot = filepath.Clean(s.HostRoot)
		}
		resolvedRel, relErr := filepath.Rel(resolvedRoot, resolved)
		if relErr != nil || pathEscapesRoot(resolvedRel) {
			return "", fmt.Errorf("file target %q resolves outside workspace", target)
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return "", fmt.Errorf("file target %q cannot be resolved within workspace", target)
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return "", fmt.Errorf("file target %q is not a regular file or directory", target)
		}
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("file target %q does not exist", target)
	}
	return "", fmt.Errorf("file target %q cannot be resolved within workspace", target)
}

func relInsideRoot(root, target string) (string, error) {
	rel, err := filepath.Rel(root, target)
	if err == nil && !pathEscapesRoot(rel) {
		return rel, nil
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil {
		return rel, err
	}
	resolvedTarget, targetErr := filepath.EvalSymlinks(target)
	if targetErr != nil {
		return rel, err
	}
	return filepath.Rel(resolvedRoot, resolvedTarget)
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) emit(req Request, resp Response, details map[string]any) {
	if s.Audit == nil {
		return
	}
	prepared, err := s.prepareAuditDetails(req, resp, details)
	if err != nil {
		s.emitAuditRedactionFailure(req, resp, err)
		return
	}
	s.emitPrepared(req, resp, prepared)
}

func (s *Server) emitAction(action string, req Request, resp Response, details map[string]any) {
	if s.Audit == nil {
		return
	}
	prepared, err := s.prepareAuditDetails(req, resp, details)
	if err != nil {
		s.emitAuditRedactionFailure(req, resp, err)
		return
	}
	_ = s.Audit.Emit(audit.Event{
		Session:  s.SessionID,
		Profile:  s.Profile,
		Backend:  s.Backend,
		Action:   action,
		Decision: resp.Decision,
		Details:  prepared,
	})
}

func (s *Server) prepareAuditDetails(req Request, resp Response, details map[string]any) (map[string]any, error) {
	if details == nil {
		details = map[string]any{}
	}
	details = cloneAuditDetails(details)
	if resp.Status != "" {
		details["status"] = resp.Status
	}
	if resp.Stderr != "" {
		details["error"] = resp.Stderr
	}
	details["requestId"] = req.ID
	if req.Action != "" && auditEventAction(req) != req.Action {
		details["requestedAction"] = req.Action
	}
	if req.Subject != "" {
		details["subject"] = req.Subject
	}
	if req.Command != "" {
		details["command"] = req.Command
	}
	// Projection requests are audited from the validated structured intent.
	// Raw guest argv can contain user data or host-looking bait and must never
	// be restored by this shared metadata floor.
	if len(req.Argv) > 0 && req.Action != cmdproxy.ActionHostAppOpenResource {
		details["argv"] = append([]string(nil), req.Argv...)
	}
	if req.Route != "" {
		details["route"] = req.Route
	}
	return s.redactAuditDetails(req, resp, details)
}

func (s *Server) redactAuditDetails(req Request, resp Response, details map[string]any) (map[string]any, error) {
	current := cloneAuditDetails(details)
	immutable := cloneAuditDetails(details)
	initialScripts := auditScriptEntries(current["policyScripts"])
	var redactionScripts []map[string]any
	for _, ref := range s.ScriptRefs {
		if !containsString(ref.Entrypoints, "redactAudit") {
			continue
		}
		path := ref.Path
		if !filepath.IsAbs(path) && s.ProfileDir != "" {
			path = filepath.Join(s.ProfileDir, path)
		}
		entry := map[string]any{
			"id":         ref.ID,
			"path":       ref.Path,
			"entrypoint": "redactAudit",
		}
		source, err := os.ReadFile(path)
		if err != nil {
			entry["error"] = err.Error()
			return nil, newAuditRedactionError(fmt.Errorf("audit redaction script %s: %w", ref.ID, err), initialScripts, append(redactionScripts, entry))
		}
		sum := sha256.Sum256(source)
		entry["sha256"] = hex.EncodeToString(sum[:])
		redaction, err := s.Evaluator.RunAuditRedactScript(string(source), "redactAudit", s.auditContext(req, resp, current))
		if err != nil {
			entry["error"] = err.Error()
			return nil, newAuditRedactionError(fmt.Errorf("audit redaction script %s: %w", ref.ID, err), initialScripts, append(redactionScripts, entry))
		}
		current = cloneAuditDetails(redaction.Details)
		delete(current, "policyScripts")
		entry["decision"] = string(policy.AuditOnly)
		if redaction.Reason != "" {
			entry["reason"] = redaction.Reason
		}
		redactionScripts = append(redactionScripts, entry)
	}
	if len(initialScripts) > 0 || len(redactionScripts) > 0 {
		all := append(initialScripts, redactionScripts...)
		current["policyScripts"] = all
	}
	preserveBrokerAuditMetadata(current, immutable)
	if req.Action == cmdproxy.ActionHostAppOpenResource {
		preserveProjectionAuditMetadata(current, immutable)
	}
	return current, nil
}

func preserveBrokerAuditMetadata(current, immutable map[string]any) {
	for _, key := range audit.EvidentiaryKeys {
		if value, ok := immutable[key]; ok {
			current[key] = value
		}
	}
}

func preserveProjectionAuditMetadata(current, immutable map[string]any) {
	for _, key := range []string{"capability", "mode", "relativeTarget", "workspaceWritable", "outcome", "code", "packId", "revisionId", "bindingId", "qualifiedAppRef", "bindingDigest"} {
		if value, ok := immutable[key]; ok {
			current[key] = value
		}
	}
}

type auditRedactionError struct {
	err     error
	scripts []map[string]any
}

func newAuditRedactionError(err error, existing, current []map[string]any) auditRedactionError {
	scripts := make([]map[string]any, 0, len(existing)+len(current))
	for _, entry := range existing {
		scripts = append(scripts, cloneAuditDetails(entry))
	}
	for _, entry := range current {
		scripts = append(scripts, cloneAuditDetails(entry))
	}
	return auditRedactionError{err: err, scripts: scripts}
}

func (e auditRedactionError) Error() string {
	return e.err.Error()
}

func (e auditRedactionError) Unwrap() error {
	return e.err
}

func (s *Server) auditContext(req Request, resp Response, details map[string]any) policy.AuditContext {
	return policy.AuditContext{
		Version:  "policy-audit/v1",
		Profile:  map[string]string{"name": s.Profile},
		Session:  map[string]any{"id": s.SessionID},
		Subject:  req.Subject,
		Action:   req.Action,
		Decision: resp.Decision,
		Details:  audit.RedactDetails(cloneAuditDetails(details)),
		Extra: map[string]interface{}{
			"status":   resp.Status,
			"exitCode": resp.ExitCode,
		},
	}
}

func cloneAuditDetails(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(details)
	if err != nil {
		out := make(map[string]any, len(details))
		for key, value := range details {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		out = map[string]any{}
	}
	return out
}

func auditScriptEntries(value any) []map[string]any {
	switch entries := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			out = append(out, cloneAuditDetails(entry))
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(entries))
		for _, item := range entries {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, cloneAuditDetails(entry))
		}
		return out
	default:
		return nil
	}
}

func (s *Server) emitAuditRedactionFailure(req Request, resp Response, err error) {
	if s.Audit == nil {
		return
	}
	details := map[string]any{
		"requestId":            req.ID,
		"auditRedactionError":  err.Error(),
		"auditRedactionStatus": "failed-closed",
	}
	if req.Action != "" && auditEventAction(req) != req.Action {
		details["requestedAction"] = req.Action
	}
	if req.Subject != "" {
		details["subject"] = req.Subject
	}
	if req.Command != "" {
		details["command"] = req.Command
	}
	if req.Route != "" {
		details["route"] = req.Route
	}
	var redactionErr auditRedactionError
	if errors.As(err, &redactionErr) && len(redactionErr.scripts) > 0 {
		details["policyScripts"] = redactionErr.scripts
	}
	s.emitPrepared(req, resp, details)
}

func (s *Server) emitPrepared(req Request, resp Response, details map[string]any) {
	_ = s.Audit.Emit(audit.Event{
		Session:  s.SessionID,
		Profile:  s.Profile,
		Backend:  s.Backend,
		Action:   auditEventAction(req),
		Decision: resp.Decision,
		Details:  details,
	})
}

func auditEventAction(req Request) string {
	if req.Action == "host.open" {
		return req.Action
	}
	if req.Action == cmdproxy.ActionHostAppOpenResource {
		return "host.app.open-resource"
	}
	if isHostFSAction(req.Action) {
		return req.Action
	}
	return "broker.request"
}

func ClientOpen(ctx context.Context, socket string, req Request) Response {
	return ClientOpenEndpoint(ctx, UnixEndpoint(socket), req)
}

func ClientOpenEndpoint(ctx context.Context, endpoint Endpoint, req Request) Response {
	fallback := Response{ID: req.ID, Decision: string(policy.Deny), Status: "broker-unavailable", ExitCode: 69, Stderr: "broker unavailable"}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, clientOpenDefaultTimeout)
		defer cancel()
	}
	if endpoint.Network == "" {
		endpoint.Network = EndpointUnix
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, endpoint.Network, endpoint.Address)
	if err != nil {
		fallback.Stderr = err.Error()
		return fallback
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stopClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopClose()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		fallback.Stderr = err.Error()
		return fallback
	}
	reader := bufio.NewReader(conn)
	var resp Response
	if err := json.NewDecoder(reader).Decode(&resp); err != nil {
		fallback.Stderr = err.Error()
		return fallback
	}
	return resp
}
