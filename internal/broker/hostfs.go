package broker

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/policy"
)

const hostFSSubject = "hostfs:daemon"

type hostFSArgs struct {
	path   string
	offset int64
	size   int64
}

type hostFSPolicyAudit struct {
	Decision      hostfs.Decision
	Canonicalized bool
}

func isHostFSAction(action string) bool {
	switch action {
	case "host.fs.stat", "host.fs.read", "host.fs.list", "host.fs.write":
		return true
	default:
		return false
	}
}

func (s *Server) handleHostFS(ctx context.Context, req Request, resp Response) Response {
	if err := validateBrokerRoute(req); err != nil {
		resp.Stderr = err.Error()
		s.emit(req, resp, nil)
		return resp
	}
	args, err := validateHostFSRequestEnvelope(req)
	if err != nil {
		resp.Status = "bad-request"
		resp.ExitCode = 2
		resp.Stderr = err.Error()
		s.emit(req, resp, nil)
		return resp
	}
	if s.HostFS == nil {
		resp.Stderr = "HostFS data plane is not configured"
		s.emit(req, resp, hostFSAuditDetails(req, args, nil, hostFSPolicyAudit{}))
		return resp
	}
	policyAudit := s.hostFSPolicyDecision(req.Action, args.path)
	data, err := s.executeHostFS(ctx, req.Action, args)
	if err != nil {
		resp = hostFSErrorResponse(resp, err)
		s.emit(req, resp, hostFSAuditDetails(req, args, nil, policyAudit))
		return resp
	}
	resp.Decision = string(policy.Allow)
	resp.Status = "ok"
	resp.ExitCode = 0
	resp.Data = data
	s.emit(req, resp, hostFSAuditDetails(req, args, data, policyAudit))
	return resp
}

func validateHostFSRequestEnvelope(req Request) (hostFSArgs, error) {
	if strings.TrimSpace(req.ID) == "" {
		return hostFSArgs{}, errors.New("broker request id is required")
	}
	if req.Subject != hostFSSubject {
		return hostFSArgs{}, fmt.Errorf("broker request subject must be %s", hostFSSubject)
	}
	if req.Command != "" || len(req.Argv) > 0 {
		return hostFSArgs{}, errors.New("HostFS broker request must not include command metadata")
	}
	for key := range req.Args {
		switch key {
		case "path", "offset", "size":
		default:
			return hostFSArgs{}, fmt.Errorf("broker request args.%s is not supported", key)
		}
	}
	path, ok := req.Args["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return hostFSArgs{}, errors.New("broker request args.path is required")
	}
	args := hostFSArgs{path: path}
	var err error
	if _, ok := req.Args["offset"]; ok {
		if req.Action != "host.fs.read" {
			return hostFSArgs{}, errors.New("broker request args.offset is only supported for host.fs.read")
		}
		args.offset, err = int64Arg(req.Args["offset"], "offset")
		if err != nil {
			return hostFSArgs{}, err
		}
		if args.offset < 0 {
			return hostFSArgs{}, errors.New("broker request args.offset must be non-negative")
		}
	}
	if _, ok := req.Args["size"]; ok {
		if req.Action != "host.fs.read" {
			return hostFSArgs{}, errors.New("broker request args.size is only supported for host.fs.read")
		}
		args.size, err = int64Arg(req.Args["size"], "size")
		if err != nil {
			return hostFSArgs{}, err
		}
		if args.size < 0 {
			return hostFSArgs{}, errors.New("broker request args.size must be non-negative")
		}
	}
	return args, nil
}

func int64Arg(value any, name string) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if math.Trunc(v) != v || v > float64(math.MaxInt64) || v < float64(math.MinInt64) {
			return 0, fmt.Errorf("broker request args.%s must be an integer", name)
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("broker request args.%s must be an integer", name)
	}
}

func (s *Server) executeHostFS(_ context.Context, action string, args hostFSArgs) (map[string]any, error) {
	switch action {
	case "host.fs.stat":
		info, err := s.HostFS.Stat(args.path)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"kind":    info.Kind,
			"size":    info.Size,
			"mode":    info.Mode,
			"modTime": info.ModTime,
		}, nil
	case "host.fs.read":
		result, err := s.HostFS.Read(args.path, args.offset, args.size)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"kind":       result.Info.Kind,
			"size":       result.Info.Size,
			"mode":       result.Info.Mode,
			"modTime":    result.Info.ModTime,
			"bytes":      result.Bytes,
			"eof":        result.EOF,
			"dataBase64": base64.StdEncoding.EncodeToString(result.Data),
		}, nil
	case "host.fs.list":
		entries, err := s.HostFS.List(args.path)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			out = append(out, map[string]any{
				"name": entry.Name,
				"kind": entry.Kind,
				"size": entry.Size,
				"mode": entry.Mode,
			})
		}
		return map[string]any{"entries": out}, nil
	case "host.fs.write":
		return nil, hostfs.ErrUnsupported
	default:
		return nil, hostfs.ErrUnsupported
	}
}

func (s *Server) hostFSPolicyDecision(action, path string) hostFSPolicyAudit {
	if s.HostFS == nil {
		return hostFSPolicyAudit{}
	}
	op, ok := hostFSActionOp(action)
	if !ok {
		return hostFSPolicyAudit{Decision: hostfs.Decision{Effect: "unsupported", Reason: "unsupported HostFS action"}}
	}
	if op == hostfs.OpWrite {
		return hostFSPolicyAudit{Decision: hostfs.Decision{Effect: "unsupported", Reason: "HostFS v1 is read-only"}}
	}
	decision := s.HostFS.Policy.Decide(op, path)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(resolved) == filepath.Clean(path) {
		return hostFSPolicyAudit{Decision: decision}
	}
	resolvedDecision := s.HostFS.Policy.Decide(op, resolved)
	auditDecision := decision
	switch {
	case decision.Effect == "deny":
		auditDecision = decision
	case resolvedDecision.Effect == "deny":
		auditDecision = resolvedDecision
	case decision.Allowed && !resolvedDecision.Allowed:
		auditDecision = hostfs.Decision{
			Effect: "deny",
			Reason: "symlink target is not granted",
		}
	case decision.Effect == "none" && resolvedDecision.Effect != "none":
		auditDecision = resolvedDecision
	}
	return hostFSPolicyAudit{Decision: auditDecision, Canonicalized: true}
}

func hostFSActionOp(action string) (hostfs.Op, bool) {
	switch action {
	case "host.fs.stat":
		return hostfs.OpStat, true
	case "host.fs.read":
		return hostfs.OpRead, true
	case "host.fs.list":
		return hostfs.OpList, true
	case "host.fs.write":
		return hostfs.OpWrite, true
	default:
		return "", false
	}
}

func hostFSErrorResponse(resp Response, err error) Response {
	switch hostfs.MapError(err) {
	case hostfs.ErrNotFound:
		resp.Stderr = "hostfs path not found"
	case hostfs.ErrUnsupported:
		resp.Stderr = "hostfs operation unsupported"
	default:
		resp.Status = "error"
		resp.ExitCode = 1
		resp.Stderr = "hostfs operation failed"
		return resp
	}
	resp.Status = "denied"
	resp.ExitCode = 126
	return resp
}

func hostFSAuditDetails(req Request, args hostFSArgs, data map[string]any, policyAudit hostFSPolicyAudit) map[string]any {
	decision := policyAudit.Decision
	details := map[string]any{
		"op":   strings.TrimPrefix(req.Action, "host.fs."),
		"path": args.path,
	}
	if policyAudit.Canonicalized {
		details["canonicalized"] = true
	}
	if decision.Effect != "" {
		details["policyEffect"] = decision.Effect
		details["policyReason"] = safeHostFSPolicyReason(decision)
	}
	if decision.RuleID != "" {
		details["ruleId"] = decision.RuleID
	}
	if decision.Source != "" {
		details["source"] = decision.Source
	}
	if req.Action == "host.fs.read" {
		details["offset"] = args.offset
		if args.size > 0 {
			details["size"] = args.size
		}
	}
	if data == nil {
		return details
	}
	if kind, ok := data["kind"]; ok {
		details["kind"] = kind
	}
	if entries, ok := data["entries"].([]map[string]any); ok {
		details["entries"] = len(entries)
	}
	if bytes, ok := data["bytes"]; ok {
		details["bytes"] = bytes
	}
	return details
}

func safeHostFSPolicyReason(decision hostfs.Decision) string {
	switch decision.Effect {
	case "allow":
		return "matched-rule"
	case "none":
		return "no-matching-grant"
	case "deny":
		if decision.Reason == "symlink target is not granted" {
			return "symlink-target-not-granted"
		}
		if decision.RuleID != "" {
			return "matched-deny-rule"
		}
		return "denied"
	case "unsupported":
		return "unsupported"
	default:
		return "unknown"
	}
}
