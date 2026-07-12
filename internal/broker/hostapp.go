package broker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/cmdgrammar"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
)

// brokerWorkspaceResolver maps a workspace ResourceRef (absolute guest path) to
// a host path using the broker's existing, symlink-escape-checked workspace
// mapping. The host path stays inside Core; it is never returned to the guest.
type brokerWorkspaceResolver struct {
	s *Server
}

type brokerResourceResolver struct {
	s        *Server
	hostFS   *hostfs.HostAppResource
	resolved hostcap.ResourceRef
}

func (r *brokerResourceResolver) ResolveResource(guestPath string) (hostcap.ResolvedResource, error) {
	clean := filepath.Clean(guestPath)
	if hostPath, err := r.s.mapGuestPath(clean); err == nil {
		rel := r.s.workspaceRelative(clean)
		r.resolved = hostcap.ResourceRef{Kind: hostcap.KindWorkspace, GuestPath: clean, RelativePath: rel}
		return hostcap.ResolvedResource{Ref: r.resolved, HostPath: hostPath}, nil
	}
	const portal = "/hideout/hostfs"
	if clean == portal || !strings.HasPrefix(clean, portal+"/") || r.s.HostFS == nil {
		return hostcap.ResolvedResource{}, &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "resource is outside the workspace and active HostFS portal"}
	}
	hostPath := filepath.Clean(strings.TrimPrefix(clean, portal))
	if !filepath.IsAbs(hostPath) || hostPath == "/" {
		return hostcap.ResolvedResource{}, &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "HostFS portal path is invalid"}
	}
	authority, err := r.s.HostFS.ResolveHostAppResource(hostfs.HostAppResourceOwner{SessionID: r.s.SessionID, Profile: r.s.Profile}, hostPath)
	if err != nil {
		return hostcap.ResolvedResource{}, &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "HostFS resource lacks active content authority"}
	}
	r.hostFS = &authority
	r.resolved = hostcap.ResourceRef{Kind: hostcap.KindHostFS, GuestPath: clean, RelativePath: authority.RelativeTarget()}
	return hostcap.ResolvedResource{Ref: r.resolved, HostPath: authority.HostPath()}, nil
}

func (r *brokerResourceResolver) RevalidateResource(previous hostcap.ResolvedResource) error {
	if previous.Ref.Kind == hostcap.KindHostFS {
		if r.hostFS == nil || r.s.HostFS == nil || previous.Ref.GuestPath != r.resolved.GuestPath {
			return &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "HostFS resource authority is unavailable"}
		}
		if err := r.s.HostFS.RevalidateHostAppResource(hostfs.HostAppResourceOwner{SessionID: r.s.SessionID, Profile: r.s.Profile}, *r.hostFS); err != nil {
			return &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "HostFS resource authority changed before launch"}
		}
		return nil
	}
	hostPath, err := r.s.mapGuestPath(previous.Ref.GuestPath)
	if err != nil || hostPath != previous.HostPath || previous.Ref.Kind != hostcap.KindWorkspace {
		return &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "resource mapping or authority changed before launch"}
	}
	return nil
}

func (r *brokerResourceResolver) auditResource() hostcap.ResourceRef {
	if r == nil {
		return hostcap.ResourceRef{}
	}
	return r.resolved
}

func (r brokerWorkspaceResolver) ResolveWorkspace(ref hostcap.ResourceRef) (string, error) {
	hostPath, err := r.s.mapGuestPath(ref.GuestPath)
	if err != nil {
		return "", &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "resource is outside the workspace"}
	}
	return hostPath, nil
}

func (r brokerWorkspaceResolver) RevalidateWorkspace(ref hostcap.ResourceRef, previouslyResolved string) error {
	hostPath, err := r.s.mapGuestPath(ref.GuestPath)
	if err != nil || hostPath != previouslyResolved {
		return &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "resource mapping changed before launch"}
	}
	return nil
}

// handleHostAppOpen handles the host.app.open-resource projection action. It
// fails closed on any problem and never returns a host path to the guest.
func (s *Server) handleHostAppOpen(ctx context.Context, req Request, resp Response) Response {
	if err := validateBrokerRoute(req); err != nil {
		return s.hostAppRefused(req, resp, hostcap.CodeCommandUnbound, err.Error())
	}
	if err := validateHostAppEnvelope(req); err != nil {
		resp.Status = "bad-request"
		resp.ExitCode = 2
		resp.Stderr = err.Error()
		s.emit(req, resp, nil)
		return resp
	}
	registration, ok := s.CommandRegistry.LookupExact(req.Command)
	if !ok || registration.Action != cmdproxy.ActionHostAppOpenResource {
		return s.hostAppRefused(req, resp, hostcap.CodeCommandUnbound, "projected command is not registered for this run")
	}
	rawIntent, err := json.Marshal(req.Args["intent"])
	if err != nil {
		return s.hostAppRefused(req, resp, hostcap.CodeIntentInvalid, "projection intent is invalid")
	}
	unbound, err := cmdgrammar.DecodeUnboundOpenResourceIntent(rawIntent)
	if err != nil {
		return s.hostAppRefused(req, resp, codeOrDefault(err, hostcap.CodeIntentInvalid), "projection intent is invalid")
	}
	request := hostcap.BoundOpenRequest{Location: unbound.Location, WindowMode: unbound.WindowMode}
	for _, resource := range unbound.Resources {
		request.Resources = append(request.Resources, hostcap.UnboundResource{GuestPath: resource.GuestPath})
	}
	if s.HostApp == nil {
		return s.hostAppRefusedRequest(req, resp, hostcap.CodeProviderUnavailable, "host application projection is not configured", request, "")
	}
	bindingDigest, _ := req.Args["bindingDigest"].(string)
	resolver := &brokerResourceResolver{s: s}
	if binding, ok := s.HostApp.Bindings.ResolveCommand(req.Command); ok && binding.BindingDigest == bindingDigest && binding.Access == hostcap.BindingAccessAskEachRun {
		resource, resolveErr := resolver.ResolveResource(request.Resources[0].GuestPath)
		if resolveErr != nil {
			return s.hostAppRefusedBoundResource(req, resp, codeOrDefault(resolveErr, hostcap.CodePathNoHostMapping), "projection resource is not authorized", request, binding, "", resolver.auditResource())
		}
		scope := hostcap.GrantScopeForBinding(s.HostApp.GrantScopeBase, binding, req.Command, req.SessionID, s.Profile, s.HostApp.RunID)
		if s.HostApp.Grants == nil || !hostcap.TrustedGrantActiveForResource(s.HostApp.Grants, scope, resource.Ref) {
			return s.hostAppRefusedBoundResource(req, resp, hostcap.CodeModeTrustedDenied, "host application access requires an exact resource-scoped operator grant", request, binding, "", resource.Ref)
		}
	}
	result, binding, err := s.HostApp.OpenCommand(ctx, req.Command, bindingDigest, request, resolver, req.SessionID, s.Profile)
	if err != nil {
		return s.hostAppRefusedBoundResource(req, resp, codeOrDefault(err, hostcap.CodeProviderUnavailable), "projection refused", request, binding, "", resolver.auditResource())
	}

	resp.Decision = "allow"
	resp.Status = "ok"
	resp.ExitCode = 0
	outcome := result.Outcome
	if result.Suppressed {
		outcome = "suppressed"
	}
	resp.Data = map[string]any{"outcome": outcome}
	s.emit(req, resp, s.hostAppAuditDetails(req, request, binding, string(result.Mode), outcome, "", resolver.auditResource()))
	return resp
}

func (s *Server) hostAppRefused(req Request, resp Response, code, message string) Response {
	return s.hostAppRefusedRequest(req, resp, code, message, hostcap.BoundOpenRequest{}, "")
}

func (s *Server) hostAppRefusedRequest(req Request, resp Response, code, message string, request hostcap.BoundOpenRequest, mode string) Response {
	return s.hostAppRefusedResource(req, resp, code, message, request, mode, hostcap.ResourceRef{})
}

func (s *Server) hostAppRefusedResource(req Request, resp Response, code, message string, request hostcap.BoundOpenRequest, mode string, resource hostcap.ResourceRef) Response {
	return s.hostAppRefusedBoundResource(req, resp, code, message, request, hostcap.OpenResourceBinding{}, mode, resource)
}

func (s *Server) hostAppRefusedBoundResource(req Request, resp Response, code, message string, request hostcap.BoundOpenRequest, binding hostcap.OpenResourceBinding, mode string, resource hostcap.ResourceRef) Response {
	resp.Decision = "deny"
	resp.Status = "denied"
	resp.ExitCode = 126
	resp.Stderr = message
	resp.Data = map[string]any{"outcome": "refused", "code": code}
	s.emit(req, resp, s.hostAppAuditDetails(req, request, binding, mode, "refused", code, resource))
	return resp
}

// hostAppAuditDetails builds generic host-app open-resource audit details. It carries the command,
// capability, mode, Core-derived resource class, bounded relative target, and
// outcome, never a host absolute path, username, token, or raw argv.
func (s *Server) hostAppAuditDetails(req Request, request hostcap.BoundOpenRequest, binding hostcap.OpenResourceBinding, mode, outcome, code string, resource hostcap.ResourceRef) map[string]any {
	details := map[string]any{
		"event":      "host.app.open-resource",
		"command":    req.Command,
		"capability": "host.app.open-resource",
		"outcome":    outcome,
	}
	if binding.PackID != "" {
		details["packId"] = binding.PackID
		details["revisionId"] = binding.RevisionID
		details["bindingId"] = binding.BindingID
		details["qualifiedAppRef"] = binding.QualifiedAppRef
		details["bindingDigest"] = binding.BindingDigest
	}
	if mode != "" {
		details["mode"] = mode
	}
	if code != "" {
		details["code"] = code
	}
	if len(request.Resources) > 0 {
		if resource.Kind == "" {
			resource = s.auditResourceFromGuestPath(request.Resources[0].GuestPath)
		}
		for key, value := range hostapppack.OpenResourceEvidence(hostcap.PublicResourceClass(resource.Kind), resource.RelativePath) {
			details[key] = value
		}
	}
	return details
}

func (s *Server) auditResourceFromGuestPath(guestPath string) hostcap.ResourceRef {
	clean := filepath.Clean(guestPath)
	const portal = "/hideout/hostfs"
	if strings.HasPrefix(clean, portal+"/") {
		return hostcap.ResourceRef{Kind: hostcap.KindHostFS, GuestPath: clean, RelativePath: filepath.Base(clean)}
	}
	if rel := s.workspaceRelative(clean); rel != "" {
		return hostcap.ResourceRef{Kind: hostcap.KindWorkspace, GuestPath: clean, RelativePath: rel}
	}
	return hostcap.ResourceRef{}
}

// workspaceRelative renders a guest path relative to the guest workspace root
// for audit, with no host path. Falls back to the base name if outside.
func (s *Server) workspaceRelative(guestPath string) string {
	if s.GuestRoot == "" || guestPath == "" {
		return ""
	}
	rel, err := relInsideRoot(s.GuestRoot, filepath.Clean(guestPath))
	if err != nil || pathEscapesRoot(rel) {
		return filepath.Base(guestPath)
	}
	return rel
}

func codeOrDefault(err error, fallback string) string {
	if c := hostcap.CodeOf(err); c != "" {
		return c
	}
	return fallback
}

// validateHostAppEnvelope restricts the projection request to a small arg
// allowlist so nothing unexpected reaches the provider.
func validateHostAppEnvelope(req Request) error {
	if req.Command == "" {
		return errors.New("host app projection request must name a command")
	}
	for key := range req.Args {
		switch key {
		case "intent", "cwd", "bindingDigest":
		default:
			return errors.New("host app projection request arg is not supported: " + key)
		}
	}
	if _, ok := req.Args["intent"]; !ok {
		return errors.New("host app projection request requires an intent")
	}
	if digest, ok := req.Args["bindingDigest"].(string); !ok || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		return errors.New("host app projection request requires an exact binding digest")
	}
	return nil
}
