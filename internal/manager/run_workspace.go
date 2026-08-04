package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const WorkspaceAttachPlanVersion = "hideout.workspace-attach-plan/v1"

var ErrSharedWorkspaceDaemonOwnerRequired = errors.New("shared workspace attachment requires the authenticated daemon session owner")
var ErrSharedWorkspaceReadyBarrierRequired = errors.New("shared workspace attachment requires authenticated daemon run streams")

type ExternalWorkspaceMetadataError struct {
	Kind string
}

func (err ExternalWorkspaceMetadataError) Error() string {
	return "code=workspace.metadata.external: alias workspace contains external Git metadata; use a dedicated named environment with preserve mode"
}

// WorkspaceGitSafeDirectories is intentionally empty for a Portal-backed
// shared view. The guest mount synthesizes the non-root target ownership, so
// normal Git operation must not require a safe.directory bypass.
func WorkspaceGitSafeDirectories(attachment workspaceattach.Attachment) ([]string, error) {
	if err := attachment.Validate(); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateAliasWorkspaceMetadata rejects known Git layouts that require a host
// path outside the exact selected workspace. Such paths cannot be made valid by
// the /workspace alias without broadening authority.
func ValidateAliasWorkspaceMetadata(root string) error {
	canonicalRoot, _, err := workspaceattach.CaptureRootIdentity(root)
	if err != nil {
		return err
	}
	gitPath := filepath.Join(canonicalRoot, ".git")
	info, err := os.Lstat(gitPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ExternalWorkspaceMetadataError{Kind: "git-symlink"}
	}
	if info.Mode().IsRegular() {
		target, err := readGitMetadataPath(gitPath, "gitdir:")
		if err != nil {
			return err
		}
		if !workspaceMetadataPathInRoot(canonicalRoot, canonicalRoot, target) {
			return ExternalWorkspaceMetadataError{Kind: "gitdir"}
		}
		return nil
	}
	if !info.IsDir() {
		return ExternalWorkspaceMetadataError{Kind: "git-unsupported"}
	}
	commonDir := filepath.Join(gitPath, "commondir")
	if _, err := os.Lstat(commonDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	target, err := readGitMetadataPath(commonDir, "")
	if err != nil {
		return err
	}
	if !workspaceMetadataPathInRoot(canonicalRoot, gitPath, target) {
		return ExternalWorkspaceMetadataError{Kind: "commondir"}
	}
	return nil
}

func readGitMetadataPath(path, prefix string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "", ExternalWorkspaceMetadataError{Kind: "git-metadata-invalid"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return "", ExternalWorkspaceMetadataError{Kind: "git-metadata-invalid"}
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	if value == "" || strings.ContainsRune(value, 0) {
		return "", ExternalWorkspaceMetadataError{Kind: "git-metadata-invalid"}
	}
	return value, nil
}

func workspaceMetadataPathInRoot(root, base, value string) bool {
	resolved := value
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(base, resolved)
	}
	resolved = filepath.Clean(resolved)
	if canonical, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = canonical
	}
	return pathInRoot(resolved, root)
}

// WorkspaceAttachPlan is authority captured by Manager before any workspace
// provider or guest-view effect starts. It contains no provider handle or
// credential and cannot itself make a host path reachable from the guest.
type WorkspaceAttachPlan struct {
	Version    string                       `json:"version"`
	Attachment workspaceattach.Attachment   `json:"attachment"`
	Provider   workspaceattach.ProviderSpec `json:"provider"`
	View       workspaceattach.ViewSpec     `json:"view"`
}

type WorkspaceAttachPlanOptions struct {
	Now time.Time
}

type runWorkspaceLifecycleRefs struct {
	Provider lifecycle.ResourceRef
	View     lifecycle.ResourceRef
}

func (plan WorkspaceAttachPlan) Validate() error {
	if plan.Version != WorkspaceAttachPlanVersion {
		return errors.New("workspace attach plan version is invalid")
	}
	if err := plan.Attachment.Validate(); err != nil {
		return err
	}
	wantProvider, err := workspaceattach.ProviderSpecFromAttachment(plan.Attachment, workspaceattach.SelectedLimits())
	if err != nil {
		return err
	}
	if plan.Provider != wantProvider {
		return errors.New("workspace attach provider does not match captured authority")
	}
	if plan.View.Attachment != plan.Attachment {
		return errors.New("workspace attach view contains different attachment authority")
	}
	return plan.View.Validate(plan.Provider)
}

// PlanRunWorkspaceAttachment captures the exact selected root and derives only
// opaque correlation identities. The lifecycle registration supplies the
// daemon-observed incarnation but remains metadata, not filesystem authority.
func (c Core) PlanRunWorkspaceAttachment(runSession RunSession, registration lifecycle.Registration, opts WorkspaceAttachPlanOptions) (WorkspaceAttachPlan, error) {
	if c.Store.Root == "" {
		return WorkspaceAttachPlan{}, errors.New("manager store root is required")
	}
	if registration == nil {
		return WorkspaceAttachPlan{}, errors.New("shared workspace attach requires daemon lifecycle registration")
	}
	return c.PlanRunWorkspaceAttachmentForIncarnation(runSession, registration.Incarnation(), opts)
}

// PlanRunWorkspaceAttachmentForIncarnation derives the immutable shared
// workspace authority after establishment preparation and before promotion.
// The prepared incarnation is metadata only; provider authority is still
// created later, after the durable session owner exists.
func (c Core) PlanRunWorkspaceAttachmentForIncarnation(runSession RunSession, incarnation lifecycle.EnvironmentRef, opts WorkspaceAttachPlanOptions) (WorkspaceAttachPlan, error) {
	if c.Store.Root == "" {
		return WorkspaceAttachPlan{}, errors.New("manager store root is required")
	}
	if !runSession.Environment.Active || !environment.UsesWorkspacePortal(runSession.Environment.Record.Mode) {
		return WorkspaceAttachPlan{}, errors.New("workspace Portal attachment requires a Portal-backed environment")
	}
	if runSession.Layout.ID == "" || runSession.Plan.GuestWorkspace != workspaceattach.LogicalWorkspaceRoot {
		return WorkspaceAttachPlan{}, errors.New("workspace attachment session or logical root is invalid")
	}
	if err := incarnation.Validate(incarnation.BootID != ""); err != nil {
		return WorkspaceAttachPlan{}, err
	}
	if incarnation.EnvironmentID != runSession.Environment.Record.ID || incarnation.InstanceName != runSession.Environment.Record.InstanceName {
		return WorkspaceAttachPlan{}, errors.New("workspace attachment incarnation does not match selected environment")
	}
	canonicalRoot, rootIdentity, err := workspaceattach.CaptureRootIdentity(runSession.Plan.Workspace)
	if err != nil {
		return WorkspaceAttachPlan{}, fmt.Errorf("capture workspace root identity: %w", err)
	}
	if err := ValidateWorkspaceMountSafety(canonicalRoot, c.Store.Root); err != nil {
		return WorkspaceAttachPlan{}, err
	}
	workspaceID, err := c.deriveWorkspaceIDFromRoot(canonicalRoot, rootIdentity)
	if err != nil {
		return WorkspaceAttachPlan{}, err
	}
	attachmentID, err := workspaceattach.NewAttachmentID()
	if err != nil {
		return WorkspaceAttachPlan{}, err
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	providerID := opaqueWorkspaceID("provider-", incarnation.EnvironmentID, strconv.FormatUint(incarnation.StartGeneration, 10), workspaceID)
	attachment := workspaceattach.Attachment{
		ID:                 attachmentID,
		SessionID:          runSession.Layout.ID,
		EnvironmentID:      incarnation.EnvironmentID,
		Incarnation:        incarnation,
		WorkspaceID:        workspaceID,
		CanonicalHostRoot:  canonicalRoot,
		RootFileIdentity:   rootIdentity,
		RootHandleIdentity: opaqueWorkspaceID("root-", incarnation.EnvironmentID, strconv.FormatUint(incarnation.StartGeneration, 10), workspaceID),
		LogicalGuestRoot:   workspaceattach.LogicalWorkspaceRoot,
		PhysicalGuestRoot:  path.Join(workspaceattach.PhysicalWorkspaceBase, workspaceID),
		Transport:          workspaceattach.SelectedTransport,
		ProviderRef: lifecycle.ResourceRef{
			Kind: lifecycle.KindWorkspaceHostProvider, ID: providerID, Generation: incarnation.StartGeneration,
		},
		GuestViewRef: lifecycle.ResourceRef{
			Kind: lifecycle.KindWorkspaceGuestView,
			ID:   "view-" + strings.TrimPrefix(attachmentID, "att_"), Generation: incarnation.StartGeneration,
		},
		State: workspaceattach.AttachmentPlanned, CreatedAt: now,
	}
	provider, err := workspaceattach.ProviderSpecFromAttachment(attachment, workspaceattach.SelectedLimits())
	if err != nil {
		return WorkspaceAttachPlan{}, err
	}
	view := workspaceattach.ViewSpec{
		Attachment:         attachment,
		CredentialAudience: workspaceattach.PortalAudience,
	}
	plan := WorkspaceAttachPlan{Version: WorkspaceAttachPlanVersion, Attachment: attachment, Provider: provider, View: view}
	if err := plan.Validate(); err != nil {
		return WorkspaceAttachPlan{}, err
	}
	return plan, nil
}

// ApplyRunWorkspaceAttachment binds a previously captured plan once. Root
// identity is recaptured at the boundary so a rename/replacement between plan
// and apply fails before provider authority exists.
func (c Core) ApplyRunWorkspaceAttachment(runSession RunSession, plan WorkspaceAttachPlan) (RunSession, error) {
	if err := plan.Validate(); err != nil {
		return RunSession{}, err
	}
	if runSession.WorkspaceAttachment.ID != "" {
		return RunSession{}, errors.New("run session already has a workspace attachment")
	}
	if !runSession.Environment.Active || !environment.UsesWorkspacePortal(runSession.Environment.Record.Mode) ||
		plan.Attachment.SessionID != runSession.Layout.ID ||
		plan.Attachment.EnvironmentID != runSession.Environment.Record.ID ||
		plan.Attachment.LogicalGuestRoot != runSession.Plan.GuestWorkspace {
		return RunSession{}, errors.New("workspace attach plan does not match run session")
	}
	canonicalRoot, rootIdentity, err := workspaceattach.CaptureRootIdentity(runSession.Plan.Workspace)
	if err != nil {
		return RunSession{}, fmt.Errorf("recapture workspace root identity: %w", err)
	}
	if canonicalRoot != plan.Attachment.CanonicalHostRoot || rootIdentity != plan.Attachment.RootFileIdentity {
		return RunSession{}, errors.New("workspace root identity changed between plan and apply")
	}
	if err := ValidateWorkspaceMountSafety(canonicalRoot, c.Store.Root); err != nil {
		return RunSession{}, err
	}
	runSession.WorkspaceAttachment = plan.Attachment
	gitSafeDirectories, err := WorkspaceGitSafeDirectories(plan.Attachment)
	if err != nil {
		return RunSession{}, err
	}
	runSession.Env = buildRunSessionEnv(runSession, gitSafeDirectories)
	return runSession, nil
}

func bindRunWorkspaceIncarnation(runSession *RunSession, incarnation lifecycle.EnvironmentRef) error {
	if runSession == nil || !environment.UsesWorkspacePortal(runSession.Environment.Record.Mode) {
		return errors.New("Portal workspace run session is required for boot binding")
	}
	if err := incarnation.Validate(true); err != nil {
		return errors.New("Portal workspace boot binding requires an observed incarnation")
	}
	attachment := runSession.WorkspaceAttachment
	current := attachment.Incarnation
	if current.EnvironmentID != incarnation.EnvironmentID || current.StartGeneration != incarnation.StartGeneration ||
		current.InstanceName != incarnation.InstanceName || (current.BootID != "" && current.BootID != incarnation.BootID) {
		return errors.New("Portal workspace boot binding changed the planned incarnation")
	}
	attachment.Incarnation = incarnation
	if err := attachment.Validate(); err != nil {
		return fmt.Errorf("validate boot-bound workspace attachment: %w", err)
	}
	runSession.WorkspaceAttachment = attachment
	return nil
}

// registerRunWorkspaceLifecycle durably records the complete Portal topology
// before the daemon starts either the host provider or the guest view. Portal
// has no environment-wide workspace service, so this graph intentionally has
// exactly one shared provider and one per-session view.
func registerRunWorkspaceLifecycle(ctx context.Context, registration lifecycle.Registration, attachment workspaceattach.Attachment) (runWorkspaceLifecycleRefs, error) {
	if registration == nil {
		return runWorkspaceLifecycleRefs{}, errors.New("workspace lifecycle registration is required")
	}
	if err := attachment.Validate(); err != nil {
		return runWorkspaceLifecycleRefs{}, err
	}
	provider, err := registration.Register(ctx, lifecycle.RegistrationSpec{
		Kind: lifecycle.KindWorkspaceHostProvider, ID: attachment.ProviderRef.ID,
		OwnerKind: "manager", OwnerID: attachment.ProviderRef.ID, State: lifecycle.StatePlanned,
		Dependencies:         []lifecycle.DependencySpec{{Ref: registration.Root(), StopMode: lifecycle.StopModeDrain}},
		Persistence:          lifecycle.PersistenceEphemeral,
		ClosePolicy:          lifecycle.ClosePreStopDrain,
		PossibleVMDependency: true,
	})
	if err != nil {
		return runWorkspaceLifecycleRefs{}, err
	}
	if provider != attachment.ProviderRef {
		return runWorkspaceLifecycleRefs{}, errors.New("workspace provider lifecycle identity changed during registration")
	}
	view, err := registration.Register(ctx, lifecycle.RegistrationSpec{
		Kind: lifecycle.KindWorkspaceGuestView, ID: attachment.GuestViewRef.ID,
		OwnerKind: "session", OwnerID: attachment.SessionID, State: lifecycle.StatePlanned,
		Dependencies: []lifecycle.DependencySpec{
			{Ref: registration.Root(), StopMode: lifecycle.StopModeDrain},
			{Ref: registration.Session(), StopMode: lifecycle.StopModeDrain},
			{Ref: provider, StopMode: lifecycle.StopModeDrain},
		},
		Persistence:          lifecycle.PersistenceEphemeral,
		ClosePolicy:          lifecycle.ClosePreStopDrain,
		PossibleVMDependency: true,
	})
	if err != nil {
		return runWorkspaceLifecycleRefs{}, err
	}
	if view != attachment.GuestViewRef {
		return runWorkspaceLifecycleRefs{}, errors.New("workspace guest-view lifecycle identity changed during registration")
	}
	if err := registration.Commit(ctx); err != nil {
		return runWorkspaceLifecycleRefs{}, fmt.Errorf("commit workspace lifecycle topology: %w", err)
	}
	return runWorkspaceLifecycleRefs{Provider: provider, View: view}, nil
}

func startRunWorkspaceLifecycle(ctx context.Context, registration lifecycle.Registration, refs runWorkspaceLifecycleRefs) error {
	if err := transitionLifecycleStarting(ctx, registration, refs.Provider); err != nil {
		return fmt.Errorf("start workspace provider lifecycle: %w", err)
	}
	if err := registration.Transition(ctx, refs.View, lifecycle.StateStarting); err != nil {
		return fmt.Errorf("start workspace guest-view lifecycle: %w", err)
	}
	return nil
}

// transitionLifecycleStarting preserves an already-active shared provider when
// a sibling registration joins it. Any other transition failure remains fatal.
func transitionLifecycleStarting(ctx context.Context, registration lifecycle.Registration, ref lifecycle.ResourceRef) error {
	if err := registration.Transition(ctx, ref, lifecycle.StateStarting); err != nil {
		if activeErr := registration.Transition(ctx, ref, lifecycle.StateActive); activeErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func releaseRunWorkspaceLifecycle(ctx context.Context, registration lifecycle.Registration, refs runWorkspaceLifecycleRefs, release func(context.Context) error) error {
	var cleanupErr error
	if release == nil {
		cleanupErr = errors.New("workspace attachment release callback is unavailable")
	} else {
		cleanupErr = release(ctx)
	}
	viewErr := registration.Release(context.Background(), refs.View, cleanupErr)
	providerErr := registration.Release(context.Background(), refs.Provider, cleanupErr)
	return errors.Join(cleanupErr, viewErr, providerErr)
}

func opaqueWorkspaceID(prefix string, parts ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("hideout.workspace.attach"))
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return prefix + hex.EncodeToString(hash.Sum(nil))
}

func workspaceAttachmentStateExists(storeRoot string) (bool, error) {
	store := lifecycle.JournalStore{Root: storeRoot}
	environmentIDs, err := store.ListEnvironmentIDs()
	if err != nil {
		return false, err
	}
	for _, environmentID := range environmentIDs {
		journal, err := store.Load(environmentID)
		if err != nil {
			return false, err
		}
		for _, resource := range journal.Resources {
			if resource.Ref.Kind == lifecycle.KindWorkspaceHostProvider || resource.Ref.Kind == lifecycle.KindWorkspaceGuestView {
				return true, nil
			}
		}
	}
	return false, nil
}
