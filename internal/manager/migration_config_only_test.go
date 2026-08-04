package manager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend/native"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestConfigOnlyMigrationRoundTripUsesNoDiskProvider(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 3, 6, 0, 0, 0, time.UTC)
	passphrase := []byte("config-only round-trip passphrase")
	sourceRoot := t.TempDir()
	sourceProfiles := profile.Store{Root: sourceRoot}
	sourceProfile := profile.Default("default")
	sourceProfile.Git.UserName = "Portable Developer"
	sourceProfile.Network.ProxySecretRef = "local-proxy"
	if err := sourceProfiles.Save(sourceProfile); err != nil {
		t.Fatal(err)
	}
	sourceEnvironments := environment.Store{Root: sourceRoot}
	sourceRecord := createStoppedMigrationEnvironment(
		t, sourceEnvironments, "config-source", "hideout-config-source",
	)
	sourceBefore, err := sourceEnvironments.Load(sourceRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	exportSecrets := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x52}, 4096)),
	})
	defer exportSecrets.Close()
	sourceSecretStore := newManagerSecretStoreFixture()
	if _, err := sourceSecretStore.Set(ctx, secrets.WriteRequest{
		Ref: "local-proxy", OperationID: "op_config_secret_source",
		ExpectedGeneration: 0, Value: secretBufferFixture(t, "socks5://127.0.0.1:7890"),
	}); err != nil {
		t.Fatal(err)
	}
	harness := native.ConfigMigrationHarness{HostOS: "darwin", HostArch: "arm64"}
	exportService := MigrationService{
		Store:        MigrationStore{Root: sourceRoot, Now: func() time.Time { return now }},
		Environments: sourceEnvironments, Profiles: sourceProfiles,
		Config: harness, SecretInputs: exportSecrets, Secrets: sourceSecretStore,
		Now: func() time.Time { return now }, NewID: sequentialMigrationIDSource(),
		ProductVersion: "0.1.0", HostOS: "darwin", HostArch: "arm64",
	}
	referenceOnlySecrets, _, err := exportService.inspectMigrationExportSecrets(
		ctx, "migplan_reference_only", migrationExportSource{
			records: []environment.Record{sourceBefore},
		}, nil,
	)
	if err != nil || len(referenceOnlySecrets) != 1 ||
		referenceOnlySecrets[0].entry.Transfer != migration.SecretReferenceOnly ||
		referenceOnlySecrets[0].entry.ValueComponentID != "" {
		t.Fatalf("default secret inventory=%+v err=%v", referenceOnlySecrets, err)
	}
	output := filepath.Join(t.TempDir(), "config.hideout-migration")
	exportPlan, err := exportService.PlanExport(ctx, migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeConfig,
		EnvironmentNames: []string{sourceRecord.Name}, IncludeSecretRefs: []string{"local-proxy"},
		OutputPath: output, RiskAcknowledgements: []string{MigrationRiskSelectedSecrets},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exportPlan.DiskRefs) != 0 || len(exportPlan.DiskEstimates) != 0 ||
		exportPlan.Mode != migration.ExportModeConfig ||
		!slices.Equal(exportPlan.IncludedClasses, []string{
			"environment-declarations", "portable-profiles", "selected-secret-values",
		}) || len(exportPlan.EnvironmentEstimates) != 1 ||
		exportPlan.EnvironmentEstimates[0].DisplayName != sourceRecord.Name ||
		exportPlan.EnvironmentEstimates[0].PortableConfigDigest.Validate() != nil ||
		exportPlan.EstimatedPayloadLogicalBytes !=
			exportPlan.EnvironmentEstimates[0].PortableConfigLogicalBytes ||
		exportPlan.EstimatedPayloadComplete {
		t.Fatalf("config export plan=%+v", exportPlan)
	}
	exportSecret, err := exportSecrets.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "config-export-client",
		BundleID: "migb_configroundtrip1", Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	exportApply, err := exportService.ApplyExport(ctx, MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: exportPlan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest:                   exportPlan.PlanDigest,
			AcceptedRiskAcknowledgements: []string{MigrationRiskSelectedSecrets},
		},
		SecretInputHandle: exportSecret.Handle, ClientBinding: "config-export-client",
		IdempotencyKey: "config-export-request-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, snapshot, err := exportService.SnapshotExportSource(ctx, exportApply.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if requestSnapshotNotEmpty(snapshot) || operation.Phase != MigrationPhaseWriting {
		t.Fatalf("config snapshot=%+v operation=%+v", snapshot, operation)
	}
	exported, err := exportService.WriteAndSealExportBundle(ctx, MigrationExportWorkerRequest{
		OperationID: exportApply.OperationID, Snapshot: snapshot,
		SecretInputHandle: exportSecret.Handle,
		SecretPurpose:     MigrationSecretPurposeExportCreate,
		ClientBinding:     "config-export-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exported.Operation.Phase != MigrationPhaseComplete {
		t.Fatalf("config export terminal=%+v", exported.Operation)
	}
	bundleBytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bundleBytes, []byte("socks5://127.0.0.1:7890")) {
		t.Fatal("encrypted migration bundle exposed selected secret plaintext")
	}
	clear(bundleBytes)
	afterSource, err := sourceEnvironments.Load(sourceRecord.ID)
	if err != nil || afterSource.InstanceName != sourceBefore.InstanceName ||
		afterSource.Status != sourceBefore.Status {
		t.Fatalf("config export changed source: before=%+v after=%+v err=%v", sourceBefore, afterSource, err)
	}

	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	sealed, err := migration.InspectSealedBundle(ctx, file, info.Size(), passphrase)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("inspect config bundle: inspect=%v close=%v", err, closeErr)
	}
	if len(sealed.Manifest.DiskObjects) != 0 || len(sealed.Manifest.DiskEdges) != 0 ||
		len(sealed.Manifest.Environments) != 1 ||
		len(sealed.Manifest.SecretEntries) != 1 ||
		sealed.Manifest.SecretEntries[0].Transfer != migration.SecretSelectedValue ||
		sealed.Manifest.Environments[0].Mode != migration.ExportModeConfig ||
		!migration.IsConfigIdentityUnavailableEvidence(
			sealed.Manifest.Environments[0].GuestIdentityEvidence,
		) {
		t.Fatalf("config manifest=%+v", sealed.Manifest)
	}

	destinationRoot := t.TempDir()
	destinationSecrets := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x63}, 4096)),
	})
	defer destinationSecrets.Close()
	destinationSecretStore := newManagerSecretStoreFixture()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{
		Now: func() time.Time { return now },
	})
	defer cache.Close()
	probe, err := ProbeMigrationBundleFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(sealed, probe.BundleFile); err != nil {
		t.Fatal(err)
	}
	importSecret, err := destinationSecrets.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "config-import-client",
		BundleID: sealed.Binding.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	destinationEnvironments := environment.Store{Root: destinationRoot}
	base := MigrationService{
		Store:        MigrationStore{Root: destinationRoot, Now: func() time.Time { return now }},
		Environments: destinationEnvironments, Profiles: profile.Store{Root: destinationRoot},
		Config: harness, SecretInputs: destinationSecrets, Secrets: destinationSecretStore,
		Now: func() time.Time { return now }, NewID: sequentialMigrationIDSource(),
		ProductVersion: "0.1.0", HostOS: "darwin", HostArch: "arm64",
	}
	imports := MigrationImportService{
		MigrationService: base,
		BundleSource: CachedMigrationBundleSource{
			SecretInputs: destinationSecrets, Cache: cache,
		},
		InspectionCache: cache,
	}
	sourceEnvironment := sealed.Manifest.Environments[0]
	workspaceMappings := make([]migration.WorkspaceMapping, 0, len(sourceEnvironment.WorkspaceProposals))
	for _, proposal := range sourceEnvironment.WorkspaceProposals {
		workspaceMappings = append(workspaceMappings, migration.WorkspaceMapping{
			ProposalID: proposal.ProposalID, Decision: migrationWorkspaceDecisionDisabled,
		})
	}
	importPlan, err := imports.PlanImport(ctx, MigrationImportPlanRequest{
		Draft: migration.ImportDraft{
			Schema: MigrationImportDraftSchema, BundlePath: output,
			BundleBinding:           sealed.Binding,
			SelectedEnvironmentRefs: []migration.OpaqueID{sourceEnvironment.SourceEnvironmentRef},
			NameMappings: []migration.NameMapping{{
				SourceRef: sourceEnvironment.SourceEnvironmentRef, DestinationName: "config-copy",
			}},
			WorkspaceMappings: workspaceMappings,
			SecretMappings: []migration.SecretMapping{{
				SourceRef: "local-proxy", Decision: migrationSecretDecisionImportValue,
				DestinationRef: "copied-proxy",
			}},
			IdentityPolicies: []migration.IdentitySelection{{
				SourceRef: sourceEnvironment.SourceEnvironmentRef,
				Policy:    migration.GuestIdentitySafeClone,
			}},
			AuthorityDecisions:   []migration.AuthorityDecision{},
			RiskAcknowledgements: []string{MigrationRiskSelectedSecrets},
		},
		SecretInputHandle: importSecret.Handle, ClientBinding: "config-import-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(importPlan.Blockers) != 0 || !importPlan.Compatibility.Available ||
		!migrationImportObjectsConfigOnly(importPlan.Objects) {
		t.Fatalf("config import plan=%+v", importPlan)
	}
	importApply, err := imports.ApplyImport(ctx, MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: importPlan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest:                   importPlan.PlanDigest,
			AcceptedRiskAcknowledgements: []string{MigrationRiskSelectedSecrets},
		},
		SecretInputHandle: importSecret.Handle, ClientBinding: "config-import-client",
		IdempotencyKey: "config-import-request-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err = imports.MaterializeImportDestination(ctx, MigrationImportMaterializeRequest{
		OperationID: importApply.OperationID, SecretInputHandle: importSecret.Handle,
		ClientBinding: "config-import-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err = imports.AdoptImportDestination(ctx, MigrationImportAdoptRequest{
		OperationID: operation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err = imports.VerifyImportDestination(ctx, MigrationImportVerifyRequest{
		OperationID: operation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err = imports.CommitImportDestination(ctx, MigrationImportCommitRequest{
		OperationID: operation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != MigrationPhaseComplete {
		t.Fatalf("config import terminal=%+v", operation)
	}
	destinationRecord, err := destinationEnvironments.LoadByName("config-copy")
	if err != nil {
		t.Fatal(err)
	}
	if destinationRecord.Status != environment.StatusCreated ||
		destinationRecord.InstanceName == sourceRecord.InstanceName ||
		destinationRecord.ID == sourceRecord.ID {
		t.Fatalf("config destination identity/status=%+v source=%+v", destinationRecord, sourceRecord)
	}
	destinationProfile, err := base.Profiles.Load("config-copy")
	if err != nil {
		t.Fatal(err)
	}
	if destinationProfile.Git.UserName != sourceProfile.Git.UserName ||
		destinationProfile.Network.ProxySecretRef != "copied-proxy" ||
		len(destinationProfile.HostFS.Grants) != 0 || len(destinationProfile.Env.Public) != 0 {
		t.Fatalf("config destination profile=%+v", destinationProfile)
	}
	resolved, err := destinationSecretStore.Resolve(ctx, "copied-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Use(func(value []byte) error {
		if string(value) != "socks5://127.0.0.1:7890" {
			t.Fatalf("imported secret value=%q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rollbackInput, err := destinationSecrets.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "config-rollback-client",
		BundleID: sealed.Binding.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackSecretStore := newManagerSecretStoreFixture()
	rollbackImports := imports
	rollbackImports.MigrationService.Secrets = rollbackSecretStore
	rollbackPlan, err := rollbackImports.PlanImport(ctx, MigrationImportPlanRequest{
		Draft: migration.ImportDraft{
			Schema: MigrationImportDraftSchema, BundlePath: output,
			BundleBinding: sealed.Binding,
			SelectedEnvironmentRefs: []migration.OpaqueID{
				sourceEnvironment.SourceEnvironmentRef,
			},
			NameMappings: []migration.NameMapping{{
				SourceRef:       sourceEnvironment.SourceEnvironmentRef,
				DestinationName: "config-rollback",
			}},
			WorkspaceMappings: workspaceMappings,
			SecretMappings: []migration.SecretMapping{{
				SourceRef: "local-proxy", Decision: migrationSecretDecisionImportValue,
				DestinationRef: "rollback-proxy",
			}},
			IdentityPolicies: []migration.IdentitySelection{{
				SourceRef: sourceEnvironment.SourceEnvironmentRef,
				Policy:    migration.GuestIdentitySafeClone,
			}},
			AuthorityDecisions:   []migration.AuthorityDecision{},
			RiskAcknowledgements: []string{MigrationRiskSelectedSecrets},
		},
		SecretInputHandle: rollbackInput.Handle, ClientBinding: "config-rollback-client",
	})
	if err != nil || len(rollbackPlan.Blockers) != 0 {
		t.Fatalf("rollback import plan=%+v err=%v", rollbackPlan, err)
	}
	rollbackApply, err := rollbackImports.ApplyImport(ctx, MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: rollbackPlan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest:                   rollbackPlan.PlanDigest,
			AcceptedRiskAcknowledgements: []string{MigrationRiskSelectedSecrets},
		},
		SecretInputHandle: rollbackInput.Handle,
		ClientBinding:     "config-rollback-client",
		IdempotencyKey:    "config-import-rollback-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackSecretStore.failNextWriteAfterCommit(errors.New("secret provider response lost"))
	rollbackOperation, _, err := rollbackImports.MaterializeImportDestination(
		ctx, MigrationImportMaterializeRequest{
			OperationID:       rollbackApply.OperationID,
			SecretInputHandle: rollbackInput.Handle,
			ClientBinding:     "config-rollback-client",
		},
	)
	if err == nil || rollbackOperation.Phase != MigrationPhaseRecoverableFailure {
		t.Fatalf("secret response-loss cut operation=%+v err=%v", rollbackOperation, err)
	}
	rollbackRetryInput, err := destinationSecrets.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "config-rollback-client",
		BundleID: sealed.Binding.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackOperation, _, err = rollbackImports.MaterializeImportDestination(
		ctx, MigrationImportMaterializeRequest{
			OperationID:       rollbackApply.OperationID,
			SecretInputHandle: rollbackRetryInput.Handle,
			ClientBinding:     "config-rollback-client",
		},
	)
	if err != nil || len(rollbackOperation.PreparedSecrets) != 1 {
		t.Fatalf("prepared rollback secret operation=%+v err=%v", rollbackOperation, err)
	}
	rollbackOperation, err = rollbackImports.RollbackImportDestination(
		ctx, MigrationImportRollbackRequest{OperationID: rollbackOperation.ID},
	)
	if err != nil || rollbackOperation.Phase != MigrationPhaseRolledBack {
		t.Fatalf("secret rollback operation=%+v err=%v", rollbackOperation, err)
	}
	rolledBackReference, err := rollbackSecretStore.Reference(ctx, "rollback-proxy")
	if err != nil || rolledBackReference.Availability != secrets.AvailabilityMissing ||
		rolledBackReference.Generation != 2 {
		t.Fatalf("rolled-back secret reference=%+v err=%v", rolledBackReference, err)
	}
}
