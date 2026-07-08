package manager

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/envpolicy"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

type RunSessionOptions struct {
	ExplainOnly bool
}

type RunAuditOptions struct {
	AuditPath string
}

type RunSession struct {
	Plan              RunPlan
	Environment       RunEnvironment
	Layout            session.Layout
	ProfileDir        string
	IdentityDir       string
	RuntimeSessionDir string
	RuntimeShimDir    string
	Env               envpolicy.Result
	Audit             *audit.Writer
	AuditPath         string
}

func (c Core) BeginRunSession(plan RunPlan, runEnv RunEnvironment, opts RunSessionOptions) (RunSession, error) {
	if c.Store.Root == "" {
		return RunSession{}, errors.New("manager store root is required")
	}
	layout, err := session.New(c.Store.Root)
	if err != nil {
		return RunSession{}, err
	}
	out := RunSession{
		Plan:              plan,
		Environment:       runEnv,
		Layout:            layout,
		RuntimeSessionDir: layout.Dir,
		RuntimeShimDir:    layout.ShimDir,
		Audit:             audit.NewDiscard(),
		AuditPath:         "off",
	}
	if runEnv.Active {
		out.RuntimeSessionDir = runEnv.RuntimeDir
		out.RuntimeShimDir = runEnv.ShimDir
		if !opts.ExplainOnly {
			if err := c.PrepareRunEnvironment(runEnv); err != nil {
				_, cleanupErr := session.CleanupEphemeral(c.Store.Root, layout.ID, false)
				return RunSession{}, errors.Join(err, cleanupErr)
			}
		}
	}
	out.ProfileDir = c.Store.ProfileDir(plan.ProfileName)
	out.IdentityDir = RunIdentityDir(layout, out.ProfileDir, plan.Ephemeral)
	out.Env = envpolicy.Build(envpolicy.Spec{
		Profile:    plan.RuntimeProfile,
		ProfileDir: out.IdentityDir,
		SessionDir: out.RuntimeSessionDir,
		ShimDir:    out.RuntimeShimDir,
	})
	return out, nil
}

func (c Core) OpenRunSessionAudit(runSession RunSession, opts RunAuditOptions) (RunSession, error) {
	auditPath := ResolveRunAuditPath(runSession.Plan.Profile, opts.AuditPath, runSession.Layout)
	runSession.AuditPath = auditPath
	if auditPath == "off" {
		return runSession, nil
	}
	aw, err := audit.NewFile(auditPath)
	if err != nil {
		return runSession, err
	}
	runSession.Audit = aw
	return runSession, nil
}

func (c Core) CloseRunSession(runSession RunSession) (session.CleanupResult, error) {
	if c.Store.Root == "" {
		return session.CleanupResult{}, errors.New("manager store root is required")
	}
	result, cleanupErr := session.CleanupEphemeral(c.Store.Root, runSession.Layout.ID, false)
	decision := "allow"
	details := CleanupAuditDetails(result)
	details["id"] = runSession.Layout.ID
	details["session"] = runSession.Layout.ID
	details["profile"] = runSession.Plan.ProfileName
	details["backend"] = runSession.Plan.Backend
	details["status"] = "completed"
	details["secretState"] = "removed"
	if cleanupErr != nil {
		decision = "error"
		details["error"] = cleanupErr.Error()
		details["status"] = "failed"
		details["secretState"] = "failed"
	}
	phase := "complete"
	if cleanupErr != nil {
		phase = "failed"
	}
	c.emitOperation("cleanup", phase, details)
	aw := runSession.Audit
	if aw == nil {
		aw = audit.NewDiscard()
	}
	_ = aw.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  runSession.Plan.ProfileName,
		Backend:  runSession.Plan.Backend,
		Action:   "session.cleanup",
		Decision: decision,
		Details:  details,
	})
	closeErr := aw.Close()
	return result, errors.Join(cleanupErr, closeErr)
}

func RunIdentityDir(layout session.Layout, profileDir string, ephemeral bool) string {
	if ephemeral {
		return filepath.Join(layout.Dir, "identity")
	}
	return profileDir
}

func ResolveRunAuditPath(p profile.Profile, auditPath string, layout session.Layout) string {
	switch auditPath {
	case "off":
		return "off"
	case "":
		if !p.Audit.Enabled {
			return "off"
		}
		return layout.AuditPath
	default:
		return auditPath
	}
}

func CleanupAuditDetails(result session.CleanupResult) map[string]any {
	types := make([]string, 0, len(result.Removed))
	seen := map[string]bool{}
	for _, path := range result.Removed {
		typ := CleanupAuditType(path)
		if typ == "" || seen[typ] {
			continue
		}
		seen[typ] = true
		types = append(types, typ)
	}
	sort.Strings(types)
	return map[string]any{
		"sessions":     result.Sessions,
		"removedCount": len(result.Removed),
		"removedTypes": types,
	}
}

func runSessionOperationDetails(runSession RunSession, status string) map[string]any {
	return map[string]any{
		"id":                runSession.Layout.ID,
		"session":           runSession.Layout.ID,
		"profile":           runSession.Plan.ProfileName,
		"backend":           runSession.Plan.Backend,
		"status":            status,
		"networkMode":       runSession.Plan.NetworkMode,
		"hasAudit":          runSession.AuditPath != "" && runSession.AuditPath != "off",
		"hasEphemeralState": true,
	}
}

func CleanupAuditType(path string) string {
	base := filepath.Base(path)
	switch base {
	case "tmp":
		return "tmp"
	case "shims":
		return "shims"
	case "bootstrap":
		return "bootstrap"
	case "identity":
		return "identity"
	case "network":
		return "networkRuntime"
	case "broker.sock":
		return "brokerSocket"
	case "broker-endpoint.json":
		return "brokerEndpoint"
	case "network-plan.json":
		return "networkPlan"
	default:
		if strings.HasPrefix(base, "hideout-") && strings.HasSuffix(base, ".sock") {
			return "brokerSocket"
		}
		return "sessionRuntime"
	}
}
