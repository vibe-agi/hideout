package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestNormalizeMigrationImportDraftRejectsUnprovedDestinationAuthority(t *testing.T) {
	base := managerImportDraftValidationFixture()
	base.WorkspaceMappings = []migration.WorkspaceMapping{
		{ProposalID: "workspace_second1", Decision: migrationWorkspaceDecisionDisabled},
		{ProposalID: "workspace_first01", Decision: migrationWorkspaceDecisionDisabled},
	}
	base.SecretMappings = []migration.SecretMapping{
		{SourceRef: "secret_second001", Decision: migrationSecretDecisionUnresolved},
		{SourceRef: "secret_first0001", Decision: migrationSecretDecisionUnresolved},
	}
	base.AuthorityDecisions = []migration.AuthorityDecision{
		{ProposalID: "proposal_second1", Decision: migrationAuthorityDecisionRejected},
		{ProposalID: "proposal_first01", Decision: migrationAuthorityDecisionDisabled},
	}
	normalized, err := normalizeMigrationImportDraft(base)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.WorkspaceMappings[0].ProposalID != "workspace_first01" ||
		normalized.SecretMappings[0].SourceRef != "secret_first0001" ||
		normalized.AuthorityDecisions[0].ProposalID != "proposal_first01" {
		t.Fatalf("destination decisions were not canonicalized: %+v", normalized)
	}
	mapped := base
	mapped.WorkspaceMappings = []migration.WorkspaceMapping{{
		ProposalID: "workspace_first01", Decision: migrationWorkspaceDecisionMapped,
		DestinationPath: t.TempDir(),
	}}
	if _, err := normalizeMigrationImportDraft(mapped); err != nil {
		t.Fatalf("syntactically valid mapped workspace was rejected before destination proof: %v", err)
	}

	tests := []struct {
		name string
		edit func(*migration.ImportDraft)
		want error
	}{
		{
			name: "disabled workspace cannot carry authority path",
			edit: func(draft *migration.ImportDraft) {
				draft.WorkspaceMappings = []migration.WorkspaceMapping{{
					ProposalID: "workspace_first01", Decision: migrationWorkspaceDecisionDisabled,
					DestinationPath: "/tmp/project",
				}}
			},
			want: ErrMigrationRequestInvalid,
		},
		{
			name: "mapped workspace path must be absolute and clean",
			edit: func(draft *migration.ImportDraft) {
				draft.WorkspaceMappings = []migration.WorkspaceMapping{{
					ProposalID: "workspace_first01", Decision: migrationWorkspaceDecisionMapped,
					DestinationPath: "../project",
				}}
			},
			want: ErrMigrationRequestInvalid,
		},
		{
			name: "unresolved secret cannot carry destination ref",
			edit: func(draft *migration.ImportDraft) {
				draft.SecretMappings = []migration.SecretMapping{{
					SourceRef: "secret_first0001", Decision: migrationSecretDecisionUnresolved,
					DestinationRef: "local-proxy",
				}}
			},
			want: ErrMigrationRequestInvalid,
		},
		{
			name: "secret rebind syntax is accepted before keychain proof",
			edit: func(draft *migration.ImportDraft) {
				draft.SecretMappings = []migration.SecretMapping{{
					SourceRef: "secret_first0001", Decision: migrationSecretDecisionExistingRef,
					DestinationRef: "local-proxy",
				}}
			},
			want: nil,
		},
		{
			name: "authority approval syntax is accepted before class verification",
			edit: func(draft *migration.ImportDraft) {
				draft.AuthorityDecisions = []migration.AuthorityDecision{{
					ProposalID: "proposal_first01", Decision: migrationAuthorityDecisionApproved,
					DestinationValue: `{"mode":"direct"}`,
				}}
			},
			want: nil,
		},
		{
			name: "approved authority cannot persist credentials",
			edit: func(draft *migration.ImportDraft) {
				draft.AuthorityDecisions = []migration.AuthorityDecision{{
					ProposalID: "proposal_first01", Decision: migrationAuthorityDecisionApproved,
					DestinationValue: "socks5://user:password@example.invalid:1080",
				}}
			},
			want: ErrMigrationRequestInvalid,
		},
		{
			name: "duplicate decisions are rejected",
			edit: func(draft *migration.ImportDraft) {
				draft.AuthorityDecisions = []migration.AuthorityDecision{
					{ProposalID: "proposal_first01", Decision: migrationAuthorityDecisionDisabled},
					{ProposalID: "proposal_first01", Decision: migrationAuthorityDecisionRejected},
				}
			},
			want: ErrMigrationRequestInvalid,
		},
		{
			name: "reserved environment name is rejected",
			edit: func(draft *migration.ImportDraft) {
				draft.NameMappings[0].DestinationName = "Default"
			},
			want: ErrMigrationRequestInvalid,
		},
		{
			name: "environment name length uses the destination model bound",
			edit: func(draft *migration.ImportDraft) {
				draft.NameMappings[0].DestinationName = strings.Repeat("a", 65)
			},
			want: ErrMigrationRequestInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := managerImportDraftValidationFixture()
			test.edit(&draft)
			_, err := normalizeMigrationImportDraft(draft)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestMigrationImportPlanBlocksCaseVariantDestinationProfile(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "dev.hideout-migration")
	if err := os.WriteFile(bundlePath, []byte("sealed bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := managerImportDraftValidationFixture()
	draft.BundlePath = bundlePath
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	provider := newManagerMigrationProviderFixture()
	fileBinding := migrationBundleFileBindingFixture()
	bundleSource := &managerBundleSourceFixture{
		secretInputs: secretInputs, path: bundlePath,
		inspection: MigrationBundleInspection{
			Binding: draft.BundleBinding, BundleFile: fileBinding,
			Manifest: managerMigrationManifestFixture(draft.BundleBinding.BundleID),
		},
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "profiles", "Dev-Clone"), 0o700); err != nil {
		t.Fatal(err)
	}
	service := MigrationImportService{
		MigrationService: MigrationService{
			Store: MigrationStore{Root: root}, Environments: environment.Store{Root: root},
			Export: provider, Import: provider, SecretInputs: secretInputs,
			NewID: sequentialMigrationIDSource(),
		},
		BundleSource: bundleSource,
	}
	handle := createManagerImportSecretHandle(
		t, secretInputs, draft.BundleBinding.BundleID, fileBinding, "client-plan",
	)
	plan, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: draft, SecretInputHandle: handle.Handle, ClientBinding: "client-plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 1 ||
		plan.Blockers[0].Code != "migration.destination.profile_name_conflict" {
		t.Fatalf("case-variant profile blocker=%+v", plan.Blockers)
	}
	if provider.stageCalls != 0 {
		t.Fatalf("profile conflict planning staged provider state: %d", provider.stageCalls)
	}
}

func TestMigrationImportPlanBindsOnlySelectedDestinationProposals(t *testing.T) {
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
	manifest.Environments[0].AuthorityProposalRefs = []migration.OpaqueID{"proposal_selected1"}
	manifest.AuthorityProposals = []migration.AuthorityProposal{
		{ProposalID: "proposal_selected1", Class: "network", SourceSummary: "disabled", State: "disabled"},
		{ProposalID: "proposal_unselected1", Class: "hostfs", SourceSummary: "disabled", State: "disabled"},
	}

	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	provider := newManagerMigrationProviderFixture()
	fileBinding := migrationBundleFileBindingFixture()
	bundleSource := &managerBundleSourceFixture{
		secretInputs: secretInputs, path: bundlePath,
		inspection: MigrationBundleInspection{
			Binding: draft.BundleBinding, BundleFile: fileBinding, Manifest: manifest,
		},
	}
	root := t.TempDir()
	service := MigrationImportService{
		MigrationService: MigrationService{
			Store: MigrationStore{Root: root}, Environments: environment.Store{Root: root},
			Export: provider, Import: provider, SecretInputs: secretInputs,
			NewID: sequentialMigrationIDSource(),
		},
		BundleSource: bundleSource,
	}
	handle := createManagerImportSecretHandle(
		t, secretInputs, draft.BundleBinding.BundleID, fileBinding, "client-plan",
	)
	plan, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: draft, SecretInputHandle: handle.Handle, ClientBinding: "client-plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.DisabledProposals, []migration.OpaqueID{"proposal_selected1"}) {
		t.Fatalf("disabled proposals escaped selection: %v", plan.DisabledProposals)
	}
	if len(plan.EnvironmentActions) != 1 ||
		plan.EnvironmentActions[0].SourceRef != "environment_source1" ||
		plan.EnvironmentActions[0].Runtime != "linux" ||
		plan.EnvironmentActions[0].GuestUser != "developer" ||
		plan.EnvironmentActions[0].Backend != "lima" ||
		plan.EnvironmentActions[0].ProfileComponentID != "component_profile1" ||
		plan.EnvironmentActions[0].ProfileLogicalBytes != 128 ||
		plan.EnvironmentActions[0].ProfileContentDigest !=
			migration.Digest("sha256:"+strings.Repeat("8", 64)) {
		t.Fatalf("authenticated environment facts were not frozen: %+v", plan.EnvironmentActions)
	}
	if len(plan.Blockers) != 1 || plan.Blockers[0].Code != "migration.workspace.mapping_required" ||
		plan.Blockers[0].SourceRef != "workspace_selected1" {
		t.Fatalf("workspace blocker=%+v", plan.Blockers)
	}

	draft.WorkspaceMappings = []migration.WorkspaceMapping{{
		ProposalID: "workspace_selected1", Decision: migrationWorkspaceDecisionDisabled,
	}}
	plan, err = service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: draft, SecretInputHandle: handle.Handle, ClientBinding: "client-plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("explicitly disabled workspace remained a blocker: %+v", plan.Blockers)
	}
	if len(plan.WorkspaceActions) != 1 ||
		plan.WorkspaceActions[0].Decision != migrationWorkspaceDecisionDisabled ||
		plan.WorkspaceActions[0].EnvironmentRef != "environment_source1" ||
		plan.WorkspaceActions[0].GuestPath != "/workspace" ||
		plan.WorkspaceActions[0].DestinationPath != "" {
		t.Fatalf("disabled workspace authority was not frozen safely: %+v", plan.WorkspaceActions)
	}

	tampered := plan
	tampered.EnvironmentActions = append(
		[]migration.EnvironmentAction(nil), plan.EnvironmentActions...,
	)
	tampered.EnvironmentActions[0].ProfileContentDigest =
		migration.Digest("sha256:" + strings.Repeat("9", 64))
	if err := SealMigrationImportPlan(&tampered); err != nil {
		t.Fatal(err)
	}
	if tampered.PlanDigest == plan.PlanDigest {
		t.Fatal("profile component binding did not change the plan digest")
	}
	_, err = service.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: tampered,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: tampered.PlanDigest},
		SecretInputHandle: handle.Handle, ClientBinding: "client-plan",
		IdempotencyKey: "config-binding-stale-0001",
	})
	if !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("substituted profile component binding error=%v", err)
	}

	draft.WorkspaceMappings[0].ProposalID = "workspace_unknown01"
	_, err = service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: draft, SecretInputHandle: handle.Handle, ClientBinding: "client-plan",
	})
	if !errors.Is(err, ErrMigrationPlanInvalid) {
		t.Fatalf("unknown proposal error=%v", err)
	}
	if provider.stageCalls != 0 {
		t.Fatalf("planning created destination state: %d", provider.stageCalls)
	}
}

func managerImportDraftValidationFixture() migration.ImportDraft {
	return migration.ImportDraft{
		Schema: MigrationImportDraftSchema, BundlePath: "/tmp/dev.hideout-migration",
		BundleBinding: migration.BundleBinding{
			BundleID: "migb_validation0001", FormatVersion: migration.BundleFormatVersion,
			FileDigest:       migration.Digest("sha256:" + strings.Repeat("1", 64)),
			ManifestDigest:   migration.Digest("sha256:" + strings.Repeat("2", 64)),
			CompletionDigest: migration.Digest("sha256:" + strings.Repeat("3", 64)),
		},
		SelectedEnvironmentRefs: []migration.OpaqueID{"environment_source1"},
		NameMappings: []migration.NameMapping{{
			SourceRef: "environment_source1", DestinationName: "dev-clone",
		}},
		WorkspaceMappings: []migration.WorkspaceMapping{},
		SecretMappings:    []migration.SecretMapping{},
		IdentityPolicies: []migration.IdentitySelection{{
			SourceRef: "environment_source1", Policy: migration.GuestIdentitySafeClone,
		}},
		AuthorityDecisions: []migration.AuthorityDecision{},
	}
}
