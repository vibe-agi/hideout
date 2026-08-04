package modal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationExportTUIRendersSharedGoldenPlan(t *testing.T) {
	encoded, err := os.ReadFile("../../migration/testdata/export-plan-surface-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan migration.ExportPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatal(err)
	}
	if err := manager.VerifyMigrationExportPlan(plan); err != nil {
		t.Fatal(err)
	}
	wizard := MigrationWizard{
		mode:          MigrationWizardExport,
		exportSession: &MigrationExportSession{Plan: plan},
	}
	view := strings.Join(wizard.reviewLines(), "\n")
	for _, expected := range []string{
		"Included environment-declarations, persistent-disks, portable-profiles",
		"Payload estimate 9.0 KiB · complete logical payload",
		"ENV dev · environment_source1 · 9.0 KiB · config 1.0 KiB",
		"DISK disk_root0000001 · root · logical 8.0 KiB",
		"used by environment_source1",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("TUI golden plan omitted %q:\n%s", expected, view)
		}
	}
}

func TestMigrationExportWizardUsesReviewedPlanAndEphemeralPassphrase(t *testing.T) {
	provider := &migrationCreationProviderFixture{t: t}
	wizard := NewMigrationWizard(MigrationWizardOptions{
		Context: context.Background(), Provider: provider, Mutable: true,
		Mode: MigrationWizardExport,
		Environments: []manager.EnvironmentSummary{{
			ID: "environment_source1", Name: "dev", Status: "stopped",
		}},
	})

	migrationWizardPress(t, wizard, key(" "))
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	outputPath := filepath.Join(t.TempDir(), "machine.hideout-migration")
	migrationWizardType(t, wizard, outputPath)
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	const passphrase = "wizard-passphrase-私"
	migrationWizardType(t, wizard, passphrase)
	if strings.Contains(wizard.View(38), passphrase) || !strings.Contains(wizard.View(38), "••••") {
		t.Fatalf("export passphrase was not masked:\n%s", wizard.View(38))
	}
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	migrationWizardType(t, wizard, passphrase)
	command := migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	migrationWizardRun(t, wizard, command)

	if wizard.Stage() != MigrationWizardReview || provider.exportPlanCalls != 1 ||
		provider.exportPassphrase != passphrase || len(wizard.secretInput) != 0 ||
		len(wizard.firstSecret) != 0 {
		t.Fatalf(
			"stage=%s calls=%d passphrase=%q secret=%d confirm=%d",
			wizard.Stage(), provider.exportPlanCalls, provider.exportPassphrase,
			len(wizard.secretInput), len(wizard.firstSecret),
		)
	}
	review := wizard.View(120)
	for _, forbidden := range []string{passphrase, provider.exportHandle} {
		if strings.Contains(review, forbidden) {
			t.Fatalf("review leaked protected input %q:\n%s", forbidden, review)
		}
	}
	if wizard.exportSession == nil || wizard.exportSession.Plan.OutputPath != outputPath {
		t.Fatalf("reviewed output path=%q want=%q", wizard.exportSession.Plan.OutputPath, outputPath)
	}
	for _, expected := range []string{
		"Review export plan", "Output ", "CONFIG", "Digest", "Effects",
		"Included environment-declarations", "Payload estimate", "ENV dev",
	} {
		if !strings.Contains(review, expected) {
			t.Fatalf("export review omitted %q:\n%s", expected, review)
		}
	}

	migrationWizardPress(t, wizard, key("a"))
	migrationWizardType(t, wizard, "EXPORT")
	migrationWizardRun(t, wizard, migrationWizardPress(t, wizard, specialKey(tea.KeyEnter)))
	if wizard.Stage() != MigrationWizardTerminal || provider.exportApplyCalls != 1 {
		t.Fatalf("stage=%s apply calls=%d", wizard.Stage(), provider.exportApplyCalls)
	}
	_, outcome := wizard.Update(specialKey(tea.KeyEnter))
	if !outcome.Close || outcome.Result == nil || outcome.Result.OperationID != "op_migration_exportwizard1" {
		t.Fatalf("terminal outcome=%+v", outcome)
	}
	if wizard.exportSession != nil && wizard.exportSession.SecretInputHandle != "" {
		t.Fatal("export handle remained after closing the wizard")
	}
}

func TestMigrationImportWizardRequiresSelectionAndDefaultsToSafeClone(t *testing.T) {
	provider := &migrationCreationProviderFixture{t: t}
	wizard := NewMigrationWizard(MigrationWizardOptions{
		Context: context.Background(), Provider: provider, Mutable: true,
		Mode: MigrationWizardImport,
	})
	bundlePath := filepath.Join(t.TempDir(), "machine.hideout-migration")
	migrationWizardType(t, wizard, bundlePath)
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	const passphrase = "import-only-secret"
	migrationWizardType(t, wizard, passphrase)
	migrationWizardRun(t, wizard, migrationWizardPress(t, wizard, specialKey(tea.KeyEnter)))

	if wizard.Stage() != MigrationWizardImportSelect || provider.unlockCalls != 1 ||
		provider.importPassphrase != passphrase {
		t.Fatalf("stage=%s unlock=%d passphrase=%q", wizard.Stage(), provider.unlockCalls, provider.importPassphrase)
	}
	selection := wizard.View(42)
	if strings.Contains(selection, passphrase) || strings.Contains(selection, provider.importHandle) ||
		!strings.Contains(selection, "Nothing is selected implicitly") {
		t.Fatalf("unsafe import inventory rendering:\n%s", selection)
	}
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	if wizard.Stage() != MigrationWizardImportSelect ||
		!strings.Contains(wizard.View(80), "Select at least one") {
		t.Fatal("unselected authenticated inventory advanced")
	}

	migrationWizardPress(t, wizard, key(" "))
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	decisions := wizard.View(120)
	for _, expected := range []string{
		"Safest defaults", "safe-clone", "workspaces disabled",
		"authority disabled", "secrets unresolved", "separately remove the old VM",
	} {
		if !strings.Contains(decisions, expected) {
			t.Fatalf("safe import decisions omitted %q:\n%s", expected, decisions)
		}
	}
	migrationWizardRun(t, wizard, migrationWizardPress(t, wizard, key("p")))
	if wizard.Stage() != MigrationWizardReview || provider.importPlanCalls != 1 {
		t.Fatalf("stage=%s plan calls=%d", wizard.Stage(), provider.importPlanCalls)
	}
	draft := provider.importDraft
	if len(draft.SelectedEnvironmentRefs) != 1 || len(draft.IdentityPolicies) != 1 ||
		draft.IdentityPolicies[0].Policy != migration.GuestIdentitySafeClone ||
		len(draft.WorkspaceMappings) != 0 || len(draft.SecretMappings) != 0 ||
		len(draft.AuthorityDecisions) != 0 || len(draft.RiskAcknowledgements) != 0 {
		t.Fatalf("unsafe default import draft=%+v", draft)
	}
	review := wizard.View(120)
	for _, expected := range []string{"Compatibility true", "OBJECT", "dev-clone", "safe-clone"} {
		if !strings.Contains(review, expected) {
			t.Fatalf("import review omitted %q:\n%s", expected, review)
		}
	}
	for _, forbidden := range []string{passphrase, provider.importHandle} {
		if strings.Contains(review, forbidden) {
			t.Fatalf("import review leaked protected input %q", forbidden)
		}
	}

	migrationWizardPress(t, wizard, key("a"))
	migrationWizardType(t, wizard, "IMPORT")
	migrationWizardRun(t, wizard, migrationWizardPress(t, wizard, specialKey(tea.KeyEnter)))
	if wizard.Stage() != MigrationWizardTerminal || provider.importApplyCalls != 1 {
		t.Fatalf("stage=%s apply calls=%d", wizard.Stage(), provider.importApplyCalls)
	}
}

func TestMigrationWizardAuthorityLossClearsSecretsAndFailsClosed(t *testing.T) {
	wizard := NewMigrationWizard(MigrationWizardOptions{
		Context: context.Background(), Provider: &migrationCreationProviderFixture{t: t},
		Mutable: true, Mode: MigrationWizardExport,
		Environments: []manager.EnvironmentSummary{{Name: "dev", Status: "stopped"}},
	})
	migrationWizardPress(t, wizard, key(" "))
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	migrationWizardType(t, wizard, filepath.Join(t.TempDir(), "bundle.hideout-migration"))
	migrationWizardPress(t, wizard, specialKey(tea.KeyEnter))
	migrationWizardType(t, wizard, "must-be-cleared")
	wizard.SyncAuthority(false, "snapshot sequence gap")
	if wizard.Stage() != MigrationWizardStale || len(wizard.secretInput) != 0 ||
		len(wizard.firstSecret) != 0 {
		t.Fatalf("stage=%s secret=%d first=%d", wizard.Stage(), len(wizard.secretInput), len(wizard.firstSecret))
	}
	output := wizard.View(32)
	if !strings.Contains(output, "STALE") || strings.Contains(output, "must-be-cleared") {
		t.Fatalf("unsafe stale rendering:\n%s", output)
	}
}

type migrationCreationProviderFixture struct {
	t *testing.T

	exportPlanCalls  int
	exportApplyCalls int
	unlockCalls      int
	importPlanCalls  int
	importApplyCalls int

	exportPassphrase string
	importPassphrase string
	exportHandle     string
	importHandle     string
	importDraft      migration.ImportDraft
}

func (provider *migrationCreationProviderFixture) PlanMigrationExport(
	_ context.Context,
	request migration.ExportRequest,
	passphrase []byte,
) (MigrationExportSession, error) {
	provider.exportPlanCalls++
	provider.exportPassphrase = string(passphrase)
	provider.exportHandle = "migration-secret-export-wizard-handle"
	plan := migration.ExportPlan{
		Schema: manager.MigrationExportPlanSchema, PlanID: "migplan_exportwizard1",
		BaseRevisions: []migration.BaseRevision{{
			Resource: "environment:environment_source1", Revision: 1,
			Digest: migrationWizardDigest("a"),
		}},
		Mode: request.Mode, EnvironmentRefs: []migration.OpaqueID{"environment_source1"},
		DiskRefs: []migration.OpaqueID{}, SelectedSecretRefs: []string{},
		IncludedClasses: []string{"environment-declarations", "portable-profiles"},
		ExcludedClasses: []string{"persistent-disks", "secret-values"},
		EnvironmentEstimates: []migration.ExportEnvironmentEstimate{{
			EnvironmentRef: "environment_source1", DisplayName: "dev",
			PortableConfigLogicalBytes: 1024,
			PortableConfigDigest:       migrationWizardDigest("d"),
			DiskRefs:                   []migration.OpaqueID{},
			EstimatedLogicalBytes:      1024,
		}},
		DiskEstimates:                []migration.ExportDiskEstimate{},
		EstimatedPayloadLogicalBytes: 1024,
		EstimatedPayloadComplete:     true,
		OutputPath:                   request.OutputPath,
		ProviderCapabilityRevision:   migrationWizardDigest("b"),
		SourceInventoryDigest:        migrationWizardDigest("c"),
		Warnings:                     []migration.PlanNotice{},
		Effects: []migration.PlannedEffect{{
			ID: "effect_writebundle1", Kind: "write-bundle", Provider: "lima",
			Compensation: "remove-partial",
		}},
		ConfirmationText:     "Create one encrypted migration bundle.",
		RiskAcknowledgements: append([]string(nil), request.RiskAcknowledgements...),
	}
	if err := manager.SealMigrationExportPlan(&plan); err != nil {
		provider.t.Fatalf("seal export plan: %v; plan=%+v", err, plan)
	}
	return MigrationExportSession{Plan: plan, SecretInputHandle: provider.exportHandle}, nil
}

func (provider *migrationCreationProviderFixture) ApplyMigrationExport(
	_ context.Context,
	session MigrationExportSession,
) (manager.MigrationApplyResult, error) {
	provider.exportApplyCalls++
	if session.SecretInputHandle != provider.exportHandle ||
		manager.VerifyMigrationExportPlan(session.Plan) != nil {
		return manager.MigrationApplyResult{}, errors.New("unreviewed export session")
	}
	return manager.MigrationApplyResult{
		OperationID: "op_migration_exportwizard1", State: manager.MigrationPhaseClaiming,
		Created: true, Next: "hideout migrate status op_migration_exportwizard1",
	}, nil
}

func (provider *migrationCreationProviderFixture) UnlockMigrationImport(
	_ context.Context,
	path string,
	passphrase []byte,
) (MigrationImportSession, error) {
	provider.unlockCalls++
	provider.importPassphrase = string(passphrase)
	provider.importHandle = "migration-secret-import-wizard-handle"
	binding := migration.BundleBinding{
		BundleID: "migb_importwizard001", FormatVersion: migration.BundleFormatVersion,
		FileDigest: migrationWizardDigest("d"), ManifestDigest: migrationWizardDigest("e"),
		CompletionDigest: migrationWizardDigest("f"),
	}
	return MigrationImportSession{
		SecretInputHandle: provider.importHandle,
		Inspection: manager.MigrationReadOnlyInspection{
			Binding: binding,
			Inventory: manager.MigrationBundleInspectionProjection{
				Schema:   manager.MigrationBundleInspectionSchema,
				BundleID: binding.BundleID, FormatVersion: binding.FormatVersion,
				CreatedAt: "2026-08-03T00:00:00Z", Sealed: true,
				EncodedBytes: 4096, LogicalBytes: 8192, RecordCount: 4,
				Environments: []manager.MigrationBundleEnvironmentProjection{{
					SourceRef: "environment_source1", DisplayNameHint: "dev-clone",
					Runtime: "native", Backend: "native", Mode: migration.ExportModeConfig,
					WorkspaceProposals:   []manager.MigrationBundleWorkspaceProjection{},
					AuthorityProposalIDs: []migration.OpaqueID{}, DiskIDs: []migration.OpaqueID{},
					GuestIdentityPresent: true,
				}},
				Disks:                []manager.MigrationBundleDiskProjection{},
				Secrets:              []manager.MigrationBundleSecretProjection{},
				AuthorityProposals:   []manager.MigrationBundleAuthorityProjection{},
				ExcludedClasses:      []string{"persistent-disks", "secret-values"},
				RequiredCapabilities: []migration.RequiredCapability{},
				Components: manager.MigrationBundleComponentCounts{
					Profiles: 1, Environments: 1, Total: 2,
				},
				Warnings: []migration.PlanNotice{},
			},
		},
	}, nil
}

func (provider *migrationCreationProviderFixture) PlanMigrationImport(
	_ context.Context,
	draft migration.ImportDraft,
	handle string,
) (migration.ImportPlan, error) {
	provider.importPlanCalls++
	provider.importDraft = draft
	if handle != provider.importHandle || len(draft.NameMappings) != 1 ||
		len(draft.IdentityPolicies) != 1 {
		return migration.ImportPlan{}, errors.New("unbound import plan request")
	}
	source := draft.SelectedEnvironmentRefs[0]
	name := draft.NameMappings[0].DestinationName
	plan := migration.ImportPlan{
		Schema: manager.MigrationImportPlanSchema, PlanID: "migplan_importwizard1",
		BundlePath: draft.BundlePath, BundleBinding: draft.BundleBinding,
		BaseRevisions: []migration.BaseRevision{{
			Resource: "bundle:" + string(draft.BundleBinding.BundleID), Revision: 1,
			Digest: migrationWizardDigest("1"),
		}},
		Compatibility: migration.Compatibility{
			Backend: "native", Available: true,
			CapabilityRevision: migrationWizardDigest("2"),
		},
		Objects: []migration.ImportObject{{
			SourceRef: source, DestinationName: name,
			Mode: migration.ExportModeConfig, DiskRefs: []migration.OpaqueID{},
		}},
		ConflictActions: []migration.ConflictAction{},
		EnvironmentActions: []migration.EnvironmentAction{{
			SourceRef: source, DestinationProfileName: name,
			Runtime: "native", GuestUser: "developer", Backend: "native",
			ProfileComponentID:   "component_profile01",
			ProfileContentDigest: migrationWizardDigest("3"), ProfileLogicalBytes: 256,
		}},
		IdentityActions: []migration.IdentityAction{{
			SourceRef: source, GuestPolicy: draft.IdentityPolicies[0].Policy,
			FreshControlIdentity: true, FreshBackendIdentity: true,
		}},
		WorkspaceActions: []migration.WorkspaceAction{}, SecretActions: []migration.SecretAction{},
		AuthorityActions: []migration.AuthorityAction{}, DisabledProposals: []migration.OpaqueID{},
		RiskAcknowledgements: append([]string(nil), draft.RiskAcknowledgements...),
		Effects: []migration.PlannedEffect{{
			ID: "effect_claimdestination1", Kind: "claim-destination",
			Provider: "manager", Compensation: "release-claim",
		}},
		Blockers: []migration.PlanNotice{},
	}
	if err := manager.SealMigrationImportPlan(&plan); err != nil {
		provider.t.Fatalf("seal import plan: %v; plan=%+v", err, plan)
	}
	return plan, nil
}

func (provider *migrationCreationProviderFixture) ApplyMigrationImport(
	_ context.Context,
	plan migration.ImportPlan,
	handle string,
) (manager.MigrationApplyResult, error) {
	provider.importApplyCalls++
	if handle != provider.importHandle || manager.VerifyMigrationImportPlan(plan) != nil {
		return manager.MigrationApplyResult{}, errors.New("unreviewed import session")
	}
	return manager.MigrationApplyResult{
		OperationID: "op_migration_importwizard1", State: manager.MigrationPhaseClaiming,
		Created: true, Next: "hideout migrate status op_migration_importwizard1",
	}, nil
}

func migrationWizardDigest(character string) migration.Digest {
	return migration.Digest("sha256:" + strings.Repeat(character, 64))
}

func migrationWizardPress(t *testing.T, wizard *MigrationWizard, message tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	command, outcome := wizard.Update(message)
	if outcome.Close {
		t.Fatal("wizard unexpectedly closed")
	}
	return command
}

func migrationWizardType(t *testing.T, wizard *MigrationWizard, value string) {
	t.Helper()
	for _, character := range value {
		migrationWizardPress(t, wizard, key(string(character)))
	}
}

func migrationWizardRun(t *testing.T, wizard *MigrationWizard, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("expected asynchronous migration wizard command")
	}
	next, outcome := wizard.Update(command())
	if next != nil || outcome.Close {
		t.Fatalf("unexpected async response: next=%t outcome=%+v", next != nil, outcome)
	}
}
