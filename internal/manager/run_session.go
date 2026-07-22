package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/envpolicy"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

type RunSessionOptions struct {
	ExplainOnly     bool
	AllocatedLayout *session.Layout
}

type RunAuditOptions struct {
	AuditPath string
}

type RunSession struct {
	Plan                RunPlan
	Environment         RunEnvironment
	Layout              session.Layout
	ProfileDir          string
	IdentityDir         string
	RuntimeSessionDir   string
	RuntimeShimDir      string
	SessionSnapshotID   string
	GitConfigPath       string
	PolicyScriptRefs    []profile.ScriptRef
	Env                 envpolicy.Result
	Audit               *audit.Writer
	AuditPath           string
	WorkspaceAttachment workspaceattach.Attachment
	WorkspacePortal     *workspaceattach.PortalRuntime
}

type runSessionWorkspaceAuthority struct {
	WorkspaceID string
	HostRoot    string
	GuestRoot   string
}

// workspaceAuthorityForRunSession is the only owner/receipt boundary for a
// run workspace. Shared machines require the immutable session attachment;
// pinned modes require the environment's mode-specific machine binding.
func workspaceAuthorityForRunSession(runSession RunSession) (runSessionWorkspaceAuthority, error) {
	if !runSession.Environment.Active {
		return runSessionWorkspaceAuthority{}, errors.New("run session has no reusable environment workspace authority")
	}
	record := runSession.Environment.Record
	if record.Mode == environment.ModeShared {
		attachment := runSession.WorkspaceAttachment
		if err := attachment.Validate(); err != nil {
			return runSessionWorkspaceAuthority{}, fmt.Errorf("shared run workspace attachment: %w", err)
		}
		if attachment.SessionID != runSession.Layout.ID || attachment.EnvironmentID != record.ID ||
			attachment.LogicalGuestRoot != runSession.Plan.GuestWorkspace {
			return runSessionWorkspaceAuthority{}, errors.New("shared run workspace attachment does not match the session")
		}
		return runSessionWorkspaceAuthority{
			WorkspaceID: attachment.WorkspaceID,
			HostRoot:    attachment.CanonicalHostRoot,
			GuestRoot:   attachment.LogicalGuestRoot,
		}, nil
	}
	binding, ok := pinnedEnvironmentWorkspace(record)
	if !ok {
		return runSessionWorkspaceAuthority{}, errors.New("reusable environment has no mode-specific workspace authority")
	}
	if binding.HostRoot == "" || binding.GuestRoot == "" {
		return runSessionWorkspaceAuthority{}, errors.New("pinned environment workspace authority is incomplete")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("hideout.owner.machine-workspace"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(filepath.Clean(binding.HostRoot)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(filepath.Clean(binding.GuestRoot)))
	return runSessionWorkspaceAuthority{
		WorkspaceID: "wrk_" + hex.EncodeToString(hash.Sum(nil)),
		HostRoot:    binding.HostRoot,
		GuestRoot:   binding.GuestRoot,
	}, nil
}

// workspaceAuthorityForDataPlane resolves one immutable mapping before any
// broker or host-app effect starts. Shared mode has exactly one source: the
// session attachment. Pinned modes use their explicit machine binding, while a
// record-less disposable run captures the session plan directly and never
// consults environment history.
func workspaceAuthorityForDataPlane(runSession RunSession) (runSessionWorkspaceAuthority, error) {
	if runSession.Environment.Active {
		authority, err := workspaceAuthorityForRunSession(runSession)
		if err != nil {
			return runSessionWorkspaceAuthority{}, err
		}
		canonical, identity, err := workspaceattach.CaptureRootIdentity(runSession.Plan.Workspace)
		if err != nil {
			return runSessionWorkspaceAuthority{}, fmt.Errorf("capture data-plane workspace root: %w", err)
		}
		authorityCanonical, authorityIdentity, err := workspaceattach.CaptureRootIdentity(authority.HostRoot)
		if err != nil {
			return runSessionWorkspaceAuthority{}, fmt.Errorf("revalidate data-plane workspace authority: %w", err)
		}
		if canonical != authorityCanonical || identity != authorityIdentity || filepath.Clean(runSession.Plan.GuestWorkspace) != filepath.Clean(authority.GuestRoot) {
			return runSessionWorkspaceAuthority{}, errors.New("run plan workspace does not match the immutable session authority")
		}
		return authority, nil
	}

	canonical, identity, err := workspaceattach.CaptureRootIdentity(runSession.Plan.Workspace)
	if err != nil {
		return runSessionWorkspaceAuthority{}, fmt.Errorf("capture record-less workspace authority: %w", err)
	}
	guestRoot := filepath.Clean(runSession.Plan.GuestWorkspace)
	if !filepath.IsAbs(guestRoot) {
		return runSessionWorkspaceAuthority{}, errors.New("record-less workspace guest root must be absolute")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("hideout.owner.session-workspace"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(runSession.Layout.ID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(canonical))
	_, _ = hash.Write([]byte{0})
	_, _ = fmt.Fprintf(hash, "%d:%d", identity.Device, identity.Inode)
	return runSessionWorkspaceAuthority{
		WorkspaceID: "wrk_" + hex.EncodeToString(hash.Sum(nil)),
		HostRoot:    canonical,
		GuestRoot:   guestRoot,
	}, nil
}

func (c Core) BeginRunSession(plan RunPlan, runEnv RunEnvironment, opts RunSessionOptions) (RunSession, error) {
	if c.Store.Root == "" {
		return RunSession{}, errors.New("manager store root is required")
	}
	sessionSnapshot, _, err := SessionSnapshotForProfile(plan.RuntimeProfile)
	if err != nil {
		return RunSession{}, fmt.Errorf("build session configuration snapshot: %w", err)
	}
	var layout session.Layout
	if opts.AllocatedLayout != nil {
		layout = *opts.AllocatedLayout
		if layout.Root != c.Store.Root {
			return RunSession{}, errors.New("allocated session layout does not belong to the manager store")
		}
		if !opts.ExplainOnly {
			if err := session.Prepare(layout); err != nil {
				return RunSession{}, err
			}
		}
	} else {
		layout, err = session.New(c.Store.Root)
		if err != nil {
			return RunSession{}, err
		}
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
		store := environment.Store{Root: c.Store.Root}
		out.RuntimeSessionDir = store.RuntimeSessionDir(runEnv.Record.ID, layout.ID)
		out.RuntimeShimDir = store.SessionShimDir(runEnv.Record.ID, layout.ID)
		if !opts.ExplainOnly {
			if _, err := store.PrepareSessionRuntime(runEnv.Record.ID, layout.ID); err != nil {
				_, cleanupErr := session.CleanupEphemeral(c.Store.Root, layout.ID, false)
				return RunSession{}, errors.Join(err, cleanupErr)
			}
		}
	}
	out.ProfileDir = c.Store.ProfileDir(plan.ProfileName)
	out.IdentityDir = RunIdentityDir(layout, out.ProfileDir, plan.Ephemeral)
	out.GitConfigPath = filepath.Join(out.RuntimeSessionDir, "identity", "gitconfig")
	if !opts.ExplainOnly {
		if err := profile.MaterializeGitConfig(out.GitConfigPath, plan.RuntimeProfile.Git); err != nil {
			_, cleanupErr := session.CleanupEphemeral(c.Store.Root, layout.ID, false)
			if runEnv.Active {
				cleanupErr = errors.Join(cleanupErr, (environment.Store{Root: c.Store.Root}).ClearSessionRuntime(runEnv.Record.ID, layout.ID))
			}
			return RunSession{}, errors.Join(fmt.Errorf("snapshot session Git configuration: %w", err), cleanupErr)
		}
	}
	policyRefs, policySources, err := snapshotSessionPolicyScripts(
		out.ProfileDir,
		out.RuntimeSessionDir,
		plan.RuntimeProfile.Policy.ScriptRefs,
		opts.ExplainOnly,
	)
	if err != nil {
		_, cleanupErr := session.CleanupEphemeral(c.Store.Root, layout.ID, false)
		if runEnv.Active {
			cleanupErr = errors.Join(cleanupErr, (environment.Store{Root: c.Store.Root}).ClearSessionRuntime(runEnv.Record.ID, layout.ID))
		}
		return RunSession{}, errors.Join(fmt.Errorf("snapshot session policy scripts: %w", err), cleanupErr)
	}
	sessionSnapshot.PolicySources = policySources
	sessionSnapshotID, err := sessionSnapshot.ID()
	if err != nil {
		_, cleanupErr := session.CleanupEphemeral(c.Store.Root, layout.ID, false)
		if runEnv.Active {
			cleanupErr = errors.Join(cleanupErr, (environment.Store{Root: c.Store.Root}).ClearSessionRuntime(runEnv.Record.ID, layout.ID))
		}
		return RunSession{}, errors.Join(fmt.Errorf("finalize session configuration snapshot: %w", err), cleanupErr)
	}
	out.PolicyScriptRefs = policyRefs
	out.SessionSnapshotID = sessionSnapshotID
	out.Environment.Configuration.Session = sessionSnapshot
	out.Environment.Configuration.Layers.SessionID = sessionSnapshotID
	gitSafeDirectories := []string{plan.GuestWorkspace}
	if runEnv.Active && runEnv.Record.Mode == environment.ModeShared {
		// Portal ownership is synthesized as the target UID. Shared sessions do
		// not need a Git ownership bypass before their attachment is verified.
		gitSafeDirectories = nil
	}
	out.Env = buildRunSessionEnv(out, gitSafeDirectories)
	return out, nil
}

// AllocateRunSession reserves only an opaque session identity. It does not
// publish a global or environment-local runtime directory; BeginRunSession
// materializes the supplied layout after lifecycle admission succeeds.
func (c Core) AllocateRunSession() (session.Layout, error) {
	if c.Store.Root == "" {
		return session.Layout{}, errors.New("manager store root is required")
	}
	return session.Allocate(c.Store.Root)
}

func snapshotSessionPolicyScripts(profileDir, runtimeDir string, refs []profile.ScriptRef, dryRun bool) ([]profile.ScriptRef, []environment.SessionSourceIdentity, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	profileRoot, err := filepath.EvalSymlinks(profileDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve profile root: %w", err)
	}
	policyDir := filepath.Join(runtimeDir, "policy")
	if !dryRun {
		if err := os.Mkdir(policyDir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create session policy directory: %w", err)
		}
	}

	out := make([]profile.ScriptRef, 0, len(refs))
	identities := make([]environment.SessionSourceIdentity, 0, len(refs))
	for index, ref := range refs {
		sourcePath, err := filepath.EvalSymlinks(filepath.Join(profileRoot, filepath.FromSlash(ref.Path)))
		if err != nil {
			return nil, nil, fmt.Errorf("resolve policy script %s: %w", ref.ID, err)
		}
		rel, err := filepath.Rel(profileRoot, sourcePath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, nil, fmt.Errorf("policy script %s resolves outside the profile", ref.ID)
		}
		info, err := os.Stat(sourcePath)
		if err != nil {
			return nil, nil, fmt.Errorf("stat policy script %s: %w", ref.ID, err)
		}
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("policy script %s is not a regular file", ref.ID)
		}
		if info.Size() > policy.MaxScriptSourceBytes {
			return nil, nil, fmt.Errorf("policy script %s exceeds %d bytes", ref.ID, policy.MaxScriptSourceBytes)
		}
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, nil, fmt.Errorf("read policy script %s: %w", ref.ID, err)
		}
		if len(source) > policy.MaxScriptSourceBytes {
			return nil, nil, fmt.Errorf("policy script %s exceeds %d bytes", ref.ID, policy.MaxScriptSourceBytes)
		}
		sum := sha256.Sum256(source)
		identities = append(identities, environment.SessionSourceIdentity{
			ID: ref.ID, Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})

		snapshotName := fmt.Sprintf("%02d.js", index)
		snapshotPath := filepath.Join(policyDir, snapshotName)
		if !dryRun {
			file, err := os.OpenFile(snapshotPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return nil, nil, fmt.Errorf("create policy snapshot %s: %w", ref.ID, err)
			}
			_, writeErr := file.Write(source)
			closeErr := file.Close()
			if err := errors.Join(writeErr, closeErr); err != nil {
				return nil, nil, fmt.Errorf("write policy snapshot %s: %w", ref.ID, err)
			}
		}
		captured := ref
		if !dryRun {
			captured.Path = snapshotName
		}
		captured.Entrypoints = append([]string(nil), ref.Entrypoints...)
		out = append(out, captured)
	}
	return out, identities, nil
}

func buildRunSessionEnv(runSession RunSession, gitSafeDirectories []string) envpolicy.Result {
	return envpolicy.Build(envpolicy.Spec{
		Profile:                runSession.Plan.RuntimeProfile,
		ProfileDir:             runSession.IdentityDir,
		SessionDir:             runSession.RuntimeSessionDir,
		ShimDir:                runSession.RuntimeShimDir,
		GitConfigPath:          runSession.GitConfigPath,
		GitSafeDirectories:     append([]string(nil), gitSafeDirectories...),
		DisableGitPreloadIndex: runSession.Environment.Active && runSession.Environment.Record.Mode == environment.ModeShared,
	})
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
