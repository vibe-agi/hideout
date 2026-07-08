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
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/policy"
)

const hostFSSubject = "hostfs:daemon"

type hostFSArgs struct {
	path            string
	destinationPath string
	offset          int64
	size            int64
	dataBase64      string
	hasDataBase64   bool
	truncate        bool
	mode            string
	uid             int64
	gid             int64
	hasUID          bool
	hasGID          bool
}

type hostFSPolicyAudit struct {
	Decision      hostfs.Decision
	Canonicalized bool
}

func isHostFSAction(action string) bool {
	switch action {
	case "host.fs.stat", "host.fs.read", "host.fs.list", "host.fs.write",
		"host.fs.write.create", "host.fs.write.replace", "host.fs.write.append", "host.fs.write.truncate",
		"host.fs.write.mkdir", "host.fs.write.delete", "host.fs.write.rename", "host.fs.write.chmod", "host.fs.write.chown":
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
		details := hostFSAuditDetails(req, args, nil, policyAudit)
		s.emit(req, resp, details)
		if isHostFSWriteAction(req.Action) {
			s.emitAction(overlay.ActionDeny, req, resp, details)
		}
		return resp
	}
	resp.Decision = string(policy.Allow)
	resp.Status = "ok"
	resp.ExitCode = 0
	resp.Data = data
	details := hostFSAuditDetails(req, args, data, policyAudit)
	s.emit(req, resp, details)
	if isHostFSWriteAction(req.Action) && data["staged"] == true {
		s.emitAction(overlay.ActionStage, req, resp, details)
		s.emitAction(overlay.ActionPending, req, resp, details)
	}
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
		case "path", "destinationPath", "offset", "size", "dataBase64", "truncate", "mode", "uid", "gid":
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
		args.offset, err = int64Arg(req.Args["offset"], "offset")
		if err != nil {
			return hostFSArgs{}, err
		}
		if args.offset < 0 {
			return hostFSArgs{}, errors.New("broker request args.offset must be non-negative")
		}
	}
	if _, ok := req.Args["size"]; ok {
		args.size, err = int64Arg(req.Args["size"], "size")
		if err != nil {
			return hostFSArgs{}, err
		}
		if args.size < 0 {
			return hostFSArgs{}, errors.New("broker request args.size must be non-negative")
		}
	}
	if raw, ok := req.Args["destinationPath"]; ok {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return hostFSArgs{}, errors.New("broker request args.destinationPath must be a non-empty string")
		}
		args.destinationPath = value
	}
	if raw, ok := req.Args["dataBase64"]; ok {
		value, ok := raw.(string)
		if !ok {
			return hostFSArgs{}, errors.New("broker request args.dataBase64 must be a string")
		}
		if _, err := base64.StdEncoding.DecodeString(value); err != nil {
			return hostFSArgs{}, errors.New("broker request args.dataBase64 must be valid base64")
		}
		args.dataBase64 = value
		args.hasDataBase64 = true
	}
	if raw, ok := req.Args["truncate"]; ok {
		value, ok := raw.(bool)
		if !ok {
			return hostFSArgs{}, errors.New("broker request args.truncate must be a boolean")
		}
		args.truncate = value
	}
	if raw, ok := req.Args["mode"]; ok {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return hostFSArgs{}, errors.New("broker request args.mode must be a non-empty string")
		}
		args.mode = value
	}
	if raw, ok := req.Args["uid"]; ok {
		args.uid, err = int64Arg(raw, "uid")
		if err != nil {
			return hostFSArgs{}, err
		}
		args.hasUID = true
	}
	if raw, ok := req.Args["gid"]; ok {
		args.gid, err = int64Arg(raw, "gid")
		if err != nil {
			return hostFSArgs{}, err
		}
		args.hasGID = true
	}
	if err := validateHostFSActionArgs(req.Action, args); err != nil {
		return hostFSArgs{}, err
	}
	return args, nil
}

func validateHostFSActionArgs(action string, args hostFSArgs) error {
	readOnly := action == "host.fs.stat" || action == "host.fs.read" || action == "host.fs.list"
	if readOnly {
		if args.destinationPath != "" || args.hasDataBase64 || args.truncate || args.mode != "" || args.hasUID || args.hasGID {
			return errors.New("write-class HostFS args are not supported for read-only actions")
		}
		if action != "host.fs.read" && (args.offset != 0 || args.size != 0) {
			return errors.New("broker request args.offset and args.size are only supported for host.fs.read")
		}
		return nil
	}
	switch action {
	case "host.fs.write":
	case "host.fs.write.create", "host.fs.write.replace", "host.fs.write.append":
		if !args.hasDataBase64 {
			return errors.New("broker request args.dataBase64 is required for content write actions")
		}
	case "host.fs.write.truncate":
		if args.size < 0 {
			return errors.New("broker request args.size must be non-negative")
		}
	case "host.fs.write.mkdir":
		if args.hasDataBase64 || args.destinationPath != "" {
			return errors.New("mkdir does not support content or destination args")
		}
	case "host.fs.write.delete":
		if args.hasDataBase64 || args.destinationPath != "" || args.mode != "" || args.hasUID || args.hasGID {
			return errors.New("delete only supports path")
		}
	case "host.fs.write.rename":
		if args.destinationPath == "" {
			return errors.New("broker request args.destinationPath is required for rename")
		}
	case "host.fs.write.chmod":
		if args.mode == "" {
			return errors.New("broker request args.mode is required for chmod")
		}
	case "host.fs.write.chown":
		if !args.hasUID && !args.hasGID {
			return errors.New("broker request args.uid or args.gid is required for chown")
		}
	default:
		return fmt.Errorf("unsupported HostFS action %q", action)
	}
	return nil
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
	case "host.fs.write", "host.fs.write.create", "host.fs.write.replace", "host.fs.write.append", "host.fs.write.truncate",
		"host.fs.write.mkdir", "host.fs.write.delete", "host.fs.write.rename", "host.fs.write.chmod", "host.fs.write.chown":
		req := hostfs.WriteRequest{
			Op:              hostFSActionOpMust(action),
			Path:            args.path,
			DestinationPath: args.destinationPath,
			Offset:          args.offset,
			Size:            args.size,
			Mode:            args.mode,
		}
		if args.hasDataBase64 {
			data, err := base64.StdEncoding.DecodeString(args.dataBase64)
			if err != nil {
				return nil, err
			}
			req.Data = data
		}
		if args.hasUID {
			req.UID = &args.uid
		}
		if args.hasGID {
			req.GID = &args.gid
		}
		result, err := s.HostFS.StageWrite(req)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"operationId": result.OperationID,
			"decisionId":  result.DecisionID,
			"staged":      result.Staged,
			"hostChanged": result.HostChanged,
		}, nil
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
	if isHostFSWriteAction(action) {
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
	case "host.fs.write.create":
		return hostfs.OpCreate, true
	case "host.fs.write.replace":
		return hostfs.OpWrite, true
	case "host.fs.write.append":
		return hostfs.OpAppend, true
	case "host.fs.write.truncate":
		return hostfs.OpTruncate, true
	case "host.fs.write.mkdir":
		return hostfs.OpMkdir, true
	case "host.fs.write.delete":
		return hostfs.OpDelete, true
	case "host.fs.write.rename":
		return hostfs.OpRename, true
	case "host.fs.write.chmod":
		return hostfs.OpChmod, true
	case "host.fs.write.chown":
		return hostfs.OpChown, true
	default:
		return "", false
	}
}

func hostFSActionOpMust(action string) hostfs.Op {
	op, ok := hostFSActionOp(action)
	if !ok {
		return hostfs.OpWrite
	}
	return op
}

func isHostFSWriteAction(action string) bool {
	return action == "host.fs.write" || strings.HasPrefix(action, "host.fs.write.")
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
	if isHostFSWriteAction(req.Action) {
		details["hostChanged"] = false
		if args.destinationPath != "" {
			details["destinationPath"] = args.destinationPath
		}
		if args.hasDataBase64 {
			details["bytes"] = decodedBase64Len(args.dataBase64)
		}
		if args.mode != "" {
			details["mode"] = args.mode
		}
		if args.hasUID {
			details["uid"] = args.uid
		}
		if args.hasGID {
			details["gid"] = args.gid
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
	if staged, ok := data["staged"]; ok {
		details["staged"] = staged
	}
	if hostChanged, ok := data["hostChanged"]; ok {
		details["hostChanged"] = hostChanged
	}
	if operationID, ok := data["operationId"]; ok {
		details["operationId"] = operationID
	}
	if decisionID, ok := data["decisionId"]; ok {
		details["decisionId"] = decisionID
	}
	return details
}

func decodedBase64Len(value string) int {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return 0
	}
	return len(data)
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
		if decision.Reason == hostfs.ReservedRootReason {
			return "reserved-control-plane"
		}
		return "denied"
	case "unsupported":
		return "unsupported"
	default:
		return "unknown"
	}
}
