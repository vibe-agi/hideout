package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestMigrationWorkspaceMappingBindsCanonicalRealIdentityAndClaim(t *testing.T) {
	service, draft, _, storeRoot := migrationWorkspacePlanningFixture(t)
	workspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	draft.WorkspaceMappings = []migration.WorkspaceMapping{{
		ProposalID: "workspace_selected1", Decision: migrationWorkspaceDecisionMapped,
		DestinationPath: alias,
	}}

	plan := planMigrationWorkspaceFixture(t, service, draft, "workspace-plan")
	canonical, identity, err := workspaceattach.CaptureRootIdentity(workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := migration.WorkspaceAction{
		ProposalID: "workspace_selected1", EnvironmentRef: "environment_source1",
		GuestPath: "/workspace", Decision: migrationWorkspaceDecisionMapped,
		DestinationPath: canonical, RootDevice: identity.Device, RootInode: identity.Inode,
	}
	if !slices.Equal(plan.WorkspaceActions, []migration.WorkspaceAction{want}) {
		t.Fatalf("workspace action=%+v, want %+v", plan.WorkspaceActions, want)
	}
	if len(plan.Blockers) != 0 || VerifyMigrationImportPlan(plan) != nil {
		t.Fatalf("mapped workspace plan is not applicable: %+v", plan)
	}

	applyHandle := createManagerImportSecretHandle(
		t, service.SecretInputs, draft.BundleBinding.BundleID,
		migrationBundleFileBindingFixture(), "workspace-apply",
	)
	result, err := service.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: plan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: plan.PlanDigest},
		SecretInputHandle: applyHandle.Handle, ClientBinding: "workspace-apply",
		IdempotencyKey: "workspace-import-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.Store.Load(result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(operation.WorkspaceActions, []migration.WorkspaceAction{want}) {
		t.Fatalf("durable workspace action drifted: %+v", operation.WorkspaceActions)
	}
	foundClaim := false
	for _, claim := range operation.Claims {
		if claim.Class == MigrationClaimDestinationWorkspace {
			foundClaim = claim.Key == canonical
		}
	}
	if !foundClaim {
		t.Fatalf("canonical workspace claim is absent: %+v", operation.Claims)
	}
	if storeRoot == canonical {
		t.Fatal("test fixture accidentally mapped the migration store")
	}
}

func TestMigrationWorkspaceMappingRejectsReplacementBeforeApply(t *testing.T) {
	service, draft, provider, _ := migrationWorkspacePlanningFixture(t)
	parent := t.TempDir()
	workspace := filepath.Join(parent, "project")
	replacement := filepath.Join(parent, "replacement")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacement, 0o700); err != nil {
		t.Fatal(err)
	}
	draft.WorkspaceMappings = []migration.WorkspaceMapping{{
		ProposalID: "workspace_selected1", Decision: migrationWorkspaceDecisionMapped,
		DestinationPath: workspace,
	}}
	plan := planMigrationWorkspaceFixture(t, service, draft, "workspace-replace-plan")
	if err := os.Remove(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, workspace); err != nil {
		t.Fatal(err)
	}

	applyHandle := createManagerImportSecretHandle(
		t, service.SecretInputs, draft.BundleBinding.BundleID,
		migrationBundleFileBindingFixture(), "workspace-replace-apply",
	)
	_, err := service.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: plan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: plan.PlanDigest},
		SecretInputHandle: applyHandle.Handle, ClientBinding: "workspace-replace-apply",
		IdempotencyKey: "workspace-import-0002",
	})
	if !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("replaced workspace error=%v, want stale plan", err)
	}
	if provider.stageCalls != 0 {
		t.Fatalf("replaced workspace reached staging: %d", provider.stageCalls)
	}
}

func TestMigrationWorkspaceMappingRejectsReservedMissingAndNonDirectoryPaths(t *testing.T) {
	service, draft, _, storeRoot := migrationWorkspacePlanningFixture(t)
	ordinaryFile := filepath.Join(t.TempDir(), "project.txt")
	if err := os.WriteFile(ordinaryFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "migration store", path: storeRoot},
		{name: "parent containing migration store", path: filepath.Dir(storeRoot)},
		{name: "missing", path: filepath.Join(t.TempDir(), "missing")},
		{name: "ordinary file", path: ordinaryFile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := draft
			candidate.WorkspaceMappings = []migration.WorkspaceMapping{{
				ProposalID: "workspace_selected1", Decision: migrationWorkspaceDecisionMapped,
				DestinationPath: test.path,
			}}
			handle := createManagerImportSecretHandle(
				t, service.SecretInputs, draft.BundleBinding.BundleID,
				migrationBundleFileBindingFixture(), "workspace-invalid-"+test.name,
			)
			_, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
				Draft: candidate, SecretInputHandle: handle.Handle,
				ClientBinding: "workspace-invalid-" + test.name,
			})
			if !errors.Is(err, ErrMigrationRequestInvalid) {
				t.Fatalf("path %q error=%v, want invalid request", test.path, err)
			}
		})
	}
}

func TestMigrationWorkspacePlanRejectsAuthenticatedProposalSubstitution(t *testing.T) {
	service, draft, _, _ := migrationWorkspacePlanningFixture(t)
	workspace := t.TempDir()
	draft.WorkspaceMappings = []migration.WorkspaceMapping{{
		ProposalID: "workspace_selected1", Decision: migrationWorkspaceDecisionMapped,
		DestinationPath: workspace,
	}}
	plan := planMigrationWorkspaceFixture(t, service, draft, "workspace-binding-plan")
	plan.WorkspaceActions = append([]migration.WorkspaceAction(nil), plan.WorkspaceActions...)
	plan.WorkspaceActions[0].GuestPath = "/substituted"
	plan.PlanDigest = ""
	if err := SealMigrationImportPlan(&plan); err != nil {
		t.Fatal(err)
	}
	applyHandle := createManagerImportSecretHandle(
		t, service.SecretInputs, draft.BundleBinding.BundleID,
		migrationBundleFileBindingFixture(), "workspace-binding-apply",
	)
	_, err := service.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: plan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: plan.PlanDigest},
		SecretInputHandle: applyHandle.Handle, ClientBinding: "workspace-binding-apply",
		IdempotencyKey: "workspace-import-0003",
	})
	if !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("substituted proposal binding error=%v, want stale plan", err)
	}
}

func migrationWorkspacePlanningFixture(
	t *testing.T,
) (MigrationImportService, migration.ImportDraft, *managerMigrationProviderFixture, string) {
	t.Helper()
	bundlePath := filepath.Join(t.TempDir(), "dev.hideout-migration")
	if err := os.WriteFile(bundlePath, []byte("sealed bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := managerImportDraftValidationFixture()
	draft.BundlePath = bundlePath
	manifest := managerMigrationManifestFixture(draft.BundleBinding.BundleID)
	manifest.Environments[0].WorkspaceProposals = []migration.WorkspaceProposal{{
		ProposalID: "workspace_selected1", GuestPath: "/workspace",
		HostPathHint: "[destination path required]", State: "disabled",
	}}
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	t.Cleanup(secretInputs.Close)
	provider := newManagerMigrationProviderFixture()
	fileBinding := migrationBundleFileBindingFixture()
	bundleSource := &managerBundleSourceFixture{
		secretInputs: secretInputs, path: bundlePath,
		inspection: MigrationBundleInspection{
			Binding: draft.BundleBinding, BundleFile: fileBinding, Manifest: manifest,
		},
	}
	storeParent := t.TempDir()
	storeRoot := filepath.Join(storeParent, "hideout-store")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	service := MigrationImportService{
		MigrationService: MigrationService{
			Store: MigrationStore{Root: storeRoot}, Environments: environment.Store{Root: storeRoot},
			Export: provider, Import: provider, SecretInputs: secretInputs,
			NewID: sequentialMigrationIDSource(),
		},
		BundleSource: bundleSource,
	}
	return service, draft, provider, storeRoot
}

func planMigrationWorkspaceFixture(
	t *testing.T,
	service MigrationImportService,
	draft migration.ImportDraft,
	client string,
) migration.ImportPlan {
	t.Helper()
	handle := createManagerImportSecretHandle(
		t, service.SecretInputs, draft.BundleBinding.BundleID,
		migrationBundleFileBindingFixture(), client,
	)
	plan, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: draft, SecretInputHandle: handle.Handle, ClientBinding: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
