package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestWorkspaceAttachPlanBindsStableRootAuthorityAndIndependentViews(t *testing.T) {
	storeRoot := t.TempDir()
	if err := os.Chmod(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: profile.Store{Root: storeRoot}}
	registration := workspacePlanRegistration{incarnation: lifecycle.EnvironmentRef{
		EnvironmentID: "env_sharedfixture", StartGeneration: 7, InstanceName: "hideout-default-env-fixture",
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	firstSession := workspaceRunSessionFixture(workspace, "ses_workspace_first")
	first, err := core.PlanRunWorkspaceAttachment(firstSession, registration, WorkspaceAttachPlanOptions{Now: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	secondSession := workspaceRunSessionFixture(workspace, "ses_workspace_second")
	second, err := core.PlanRunWorkspaceAttachment(secondSession, registration, WorkspaceAttachPlanOptions{Now: time.Unix(101, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if first.Attachment.WorkspaceID != second.Attachment.WorkspaceID || first.Attachment.ProviderRef != second.Attachment.ProviderRef {
		t.Fatalf("same root did not retain provider identity: first=%+v second=%+v", first.Attachment, second.Attachment)
	}
	if first.Attachment.ID == second.Attachment.ID || first.Attachment.GuestViewRef == second.Attachment.GuestViewRef {
		t.Fatal("sibling sessions shared attachment or guest-view identity")
	}
	if first.Attachment.LogicalGuestRoot != workspaceattach.LogicalWorkspaceRoot ||
		first.Attachment.PhysicalGuestRoot != workspaceattach.PhysicalWorkspaceBase+"/"+first.Attachment.WorkspaceID {
		t.Fatalf("workspace roots=%+v", first.Attachment)
	}
	publicIdentities := fmt.Sprintf("%s %s %s", first.Attachment.ProviderRef.ID, first.Attachment.GuestViewRef.ID, first.View.CredentialAudience)
	if strings.Contains(publicIdentities, workspace) {
		t.Fatalf("opaque attachment identities leaked host root: %s", publicIdentities)
	}
	bound, err := core.ApplyRunWorkspaceAttachment(firstSession, first)
	if err != nil {
		t.Fatal(err)
	}
	if got := bound.Env.Synthetic["GIT_CONFIG_COUNT"]; got != "1" {
		t.Fatalf("Portal Git config count=%q want 1", got)
	}
	if got := bound.Env.Synthetic["GIT_CONFIG_KEY_0"]; got != "core.preloadIndex" {
		t.Fatalf("Portal Git config key=%q", got)
	}
	if got := bound.Env.Synthetic["GIT_CONFIG_VALUE_0"]; got != "false" {
		t.Fatalf("Portal Git preload value=%q", got)
	}
	for name, value := range bound.Env.Synthetic {
		if strings.HasPrefix(name, "GIT_CONFIG_KEY_") && value == "safe.directory" {
			t.Fatalf("synthetic Portal ownership unexpectedly installed Git trust bypass %q", name)
		}
	}
	first.Attachment.WorkspaceID = "wrk_" + strings.Repeat("f", 64)
	if bound.WorkspaceAttachment.WorkspaceID == first.Attachment.WorkspaceID {
		t.Fatal("run session retained mutable plan alias")
	}
	if _, err := core.ApplyRunWorkspaceAttachment(bound, second); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("second attachment bind was not rejected: %v", err)
	}
}

func TestPlanRunRejectsAliasWorkspaceWithExternalGitMetadata(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	external := filepath.Join(t.TempDir(), "worktree-gitdir")
	if err := os.WriteFile(filepath.Join(workspace, ".git"), []byte("gitdir: "+external+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(store).PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "lima", Workspace: workspace, Command: []string{"true"},
	})
	var metadataErr ExternalWorkspaceMetadataError
	if !errors.As(err, &metadataErr) || metadataErr.Kind != "gitdir" {
		t.Fatalf("PlanRun error=%T %v", err, err)
	}
	if strings.Contains(err.Error(), external) {
		t.Fatalf("external metadata guidance leaked host path: %v", err)
	}
	for _, want := range []string{"workspace.metadata.external", "dedicated named environment", "preserve mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("external metadata guidance missing %q: %v", want, err)
		}
	}
}

func TestTwoDisjointProjectsShareOneIncarnationWithDistinctWorkspaceViews(t *testing.T) {
	storeRoot := t.TempDir()
	if err := os.Chmod(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	projectsRoot := t.TempDir()
	projectA := filepath.Join(projectsRoot, "project-a")
	projectB := filepath.Join(projectsRoot, "project-b")
	for _, project := range []string{projectA, projectB} {
		if err := os.Mkdir(project, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	core := Core{Store: profile.Store{Root: storeRoot}}
	incarnation := lifecycle.EnvironmentRef{
		EnvironmentID: "env_sharedfixture", StartGeneration: 11,
		InstanceName: "hideout-default-env-fixture", BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	registration := workspacePlanRegistration{incarnation: incarnation}
	sessionA := workspaceRunSessionFixture(projectA, "ses_workspace_project_a")
	sessionB := workspaceRunSessionFixture(projectB, "ses_workspace_project_b")
	planA, err := core.PlanRunWorkspaceAttachment(
		sessionA, registration,
		WorkspaceAttachPlanOptions{Now: time.Unix(200, 0)},
	)
	if err != nil {
		t.Fatal(err)
	}
	planB, err := core.PlanRunWorkspaceAttachment(
		sessionB, registration,
		WorkspaceAttachPlanOptions{Now: time.Unix(201, 0)},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, attachment := range []workspaceattach.Attachment{planA.Attachment, planB.Attachment} {
		if attachment.EnvironmentID != incarnation.EnvironmentID || attachment.Incarnation != incarnation {
			t.Fatalf("attachment escaped shared incarnation: %+v", attachment)
		}
		if attachment.LogicalGuestRoot != workspaceattach.LogicalWorkspaceRoot {
			t.Fatalf("logical workspace root=%q", attachment.LogicalGuestRoot)
		}
	}
	if planA.Attachment.WorkspaceID == planB.Attachment.WorkspaceID ||
		planA.Attachment.PhysicalGuestRoot == planB.Attachment.PhysicalGuestRoot ||
		planA.Attachment.ProviderRef == planB.Attachment.ProviderRef ||
		planA.Attachment.GuestViewRef == planB.Attachment.GuestViewRef {
		t.Fatalf("disjoint projects shared workspace authority: a=%+v b=%+v", planA.Attachment, planB.Attachment)
	}
	canonicalA, err := filepath.EvalSymlinks(projectA)
	if err != nil {
		t.Fatal(err)
	}
	canonicalB, err := filepath.EvalSymlinks(projectB)
	if err != nil {
		t.Fatal(err)
	}
	if planA.Attachment.CanonicalHostRoot != canonicalA || planB.Attachment.CanonicalHostRoot != canonicalB {
		t.Fatalf("captured roots a=%q b=%q", planA.Attachment.CanonicalHostRoot, planB.Attachment.CanonicalHostRoot)
	}
	boundA, err := core.ApplyRunWorkspaceAttachment(sessionA, planA)
	if err != nil {
		t.Fatal(err)
	}
	boundB, err := core.ApplyRunWorkspaceAttachment(sessionB, planB)
	if err != nil {
		t.Fatal(err)
	}
	authorityA, err := workspaceAuthorityForDataPlane(boundA)
	if err != nil {
		t.Fatal(err)
	}
	authorityB, err := workspaceAuthorityForDataPlane(boundB)
	if err != nil {
		t.Fatal(err)
	}
	appBinding := hostcap.OpenResourceBinding{
		PackID: "community.editor", RevisionID: "rev_fixture", BindingID: "open-resource",
		QualifiedAppRef: "community.editor/rev_fixture/editor", BindingDigest: "sha256:" + strings.Repeat("a", 64),
		ResourceKinds: []hostcap.ResourceKind{hostcap.KindWorkspace},
	}
	grantA := projectionGrantBindingForRun(boundA, authorityA, appBinding, "editor")
	grantB := projectionGrantBindingForRun(boundB, authorityB, appBinding, "editor")
	if authorityA.HostRoot != canonicalA || authorityB.HostRoot != canonicalB ||
		grantA.WorkspaceID != planA.Attachment.WorkspaceID || grantB.WorkspaceID != planB.Attachment.WorkspaceID ||
		grantA.WorkspaceID == grantB.WorkspaceID {
		t.Fatalf("projection authority crossed workspace attachments: a=%+v b=%+v", grantA, grantB)
	}
}

func TestSharedRunSessionWorkspaceAuthorityUsesOnlyImmutableAttachment(t *testing.T) {
	storeRoot := t.TempDir()
	if err := os.Chmod(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	core := Core{Store: profile.Store{Root: storeRoot}}
	registration := workspacePlanRegistration{incarnation: lifecycle.EnvironmentRef{
		EnvironmentID: "env_sharedfixture", StartGeneration: 2, InstanceName: "hideout-default-env-fixture",
		BootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	runSession := workspaceRunSessionFixture(workspace, "ses_workspace_authority")
	plan, err := core.PlanRunWorkspaceAttachment(runSession, registration, WorkspaceAttachPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := core.ApplyRunWorkspaceAttachment(runSession, plan)
	if err != nil {
		t.Fatal(err)
	}
	// A mutable plan path must never replace the captured attachment authority.
	bound.Plan.Workspace = t.TempDir()
	authority, err := workspaceAuthorityForRunSession(bound)
	if err != nil {
		t.Fatal(err)
	}
	if authority.WorkspaceID != plan.Attachment.WorkspaceID || authority.HostRoot != plan.Attachment.CanonicalHostRoot ||
		authority.GuestRoot != plan.Attachment.LogicalGuestRoot ||
		authority.PhysicalGuestRoot != plan.Attachment.PhysicalGuestRoot {
		t.Fatalf("workspace authority=%+v attachment=%+v", authority, plan.Attachment)
	}
	if _, err := workspaceAuthorityForDataPlane(bound); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("data plane accepted a plan/attachment mismatch: %v", err)
	}
	bound.WorkspaceAttachment = workspaceattach.Attachment{}
	if _, err := workspaceAuthorityForRunSession(bound); err == nil {
		t.Fatal("shared session fell back to mutable plan or last-project workspace authority")
	}
}

func TestWorkspaceAttachApplyRejectsRootReplacement(t *testing.T) {
	storeRoot := t.TempDir()
	if err := os.Chmod(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	workspace := filepath.Join(parent, "project")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: profile.Store{Root: storeRoot}}
	registration := workspacePlanRegistration{incarnation: lifecycle.EnvironmentRef{
		EnvironmentID: "env_sharedfixture", StartGeneration: 3, InstanceName: "hideout-default-env-fixture",
	}}
	runSession := workspaceRunSessionFixture(workspace, "ses_workspace_replace")
	plan, err := core.PlanRunWorkspaceAttachment(runSession, registration, WorkspaceAttachPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(workspace, workspace+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyRunWorkspaceAttachment(runSession, plan); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("replaced root was accepted: %v", err)
	}
}

func TestWorkspaceAttachPlanRequiresSharedDaemonIncarnation(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	if err := os.Chmod(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: profile.Store{Root: storeRoot}}
	runSession := workspaceRunSessionFixture(workspace, "ses_workspace_requirements")
	if _, err := core.PlanRunWorkspaceAttachment(runSession, nil, WorkspaceAttachPlanOptions{}); err == nil || !strings.Contains(err.Error(), "daemon lifecycle") {
		t.Fatalf("nil daemon lifecycle registration was accepted: %v", err)
	}
	runSession.Environment.Record.Mode = environment.ModeWorkspaceBound
	registration := workspacePlanRegistration{incarnation: lifecycle.EnvironmentRef{
		EnvironmentID: "env_sharedfixture", StartGeneration: 1, InstanceName: "hideout-default-env-fixture",
	}}
	if _, err := core.PlanRunWorkspaceAttachment(runSession, registration, WorkspaceAttachPlanOptions{}); err == nil || !strings.Contains(err.Error(), "Portal-backed environment") {
		t.Fatalf("workspace-bound environment was accepted as portal attachment: %v", err)
	}
	runSession.Environment.Record.Mode = environment.ModeDedicatedPortal
	if _, err := core.PlanRunWorkspaceAttachment(runSession, registration, WorkspaceAttachPlanOptions{}); err != nil {
		t.Fatalf("dedicated Portal environment was rejected: %v", err)
	}
}

func TestSharedApplyRunRequiresDaemonLifecycleAndAttachmentOwner(t *testing.T) {
	setFakeLinuxShim(t)
	storeRoot := t.TempDir()
	if err := os.Chmod(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: storeRoot}
	if err := store.Save(profile.Default("shared-owner")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "shared-owner", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &applyRunFakeBackend{name: "lima"}
	_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true},
	})
	if !errors.Is(err, ErrSharedWorkspaceDaemonOwnerRequired) {
		t.Fatalf("shared run without daemon owner error=%v", err)
	}
	if got := fake.calls; len(got) != 1 || got[0] != "available" {
		t.Fatalf("shared run reached backend authority before owner binding: %v", got)
	}
}

func workspaceRunSessionFixture(workspace, sessionID string) RunSession {
	return RunSession{
		Plan: RunPlan{Workspace: workspace, GuestWorkspace: workspaceattach.LogicalWorkspaceRoot},
		Environment: RunEnvironment{Active: true, Record: environment.Record{
			ID: "env_sharedfixture", Mode: environment.ModeShared, InstanceName: "hideout-default-env-fixture",
		}},
		Layout: session.Layout{ID: sessionID},
	}
}

type workspacePlanRegistration struct {
	incarnation lifecycle.EnvironmentRef
}

func (r workspacePlanRegistration) Incarnation() lifecycle.EnvironmentRef { return r.incarnation }
func (workspacePlanRegistration) Root() lifecycle.ResourceRef             { return lifecycle.ResourceRef{} }
func (workspacePlanRegistration) Session() lifecycle.ResourceRef          { return lifecycle.ResourceRef{} }
func (workspacePlanRegistration) Commit(context.Context) error            { return nil }
func (workspacePlanRegistration) BindBoot(context.Context, string) error  { return nil }
func (workspacePlanRegistration) Register(context.Context, lifecycle.RegistrationSpec) (lifecycle.ResourceRef, error) {
	return lifecycle.ResourceRef{}, nil
}
func (workspacePlanRegistration) Transition(context.Context, lifecycle.ResourceRef, lifecycle.ResourceState) error {
	return nil
}
func (workspacePlanRegistration) Release(context.Context, lifecycle.ResourceRef, error) error {
	return nil
}
func (workspacePlanRegistration) RecordFact(context.Context, lifecycle.FactSpec) error { return nil }
func (workspacePlanRegistration) Finish(context.Context, error) error                  { return nil }
