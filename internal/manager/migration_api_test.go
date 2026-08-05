package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestMigrationInspectAPIAuthenticatesSealedBundleAcrossOneShotHandle(t *testing.T) {
	bundlePath := writeManagerSealedBundleFixture(t)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
	defer cache.Close()
	api := API{Migrations: &MigrationAPIService{
		Service: MigrationService{SecretInputs: secretInputs},
		Inspection: MigrationInspectionService{
			SecretInputs: secretInputs,
			Cache:        cache,
		},
	}}
	credential := "Bearer migration-inspect-api-test"
	secretBody, err := json.Marshal(MigrationSecretInputAPIRequest{
		Purpose:    MigrationSecretPurposeInspect,
		BundlePath: bundlePath,
		Passphrase: "manager inspection passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	secretRequest := httptest.NewRequest(
		http.MethodPost, "/api/v1/migration/secret-input", bytes.NewReader(secretBody),
	)
	secretRequest.Header.Set("Authorization", credential)
	secretResponse := httptest.NewRecorder()
	api.serveMigrationSecretInput(secretResponse, secretRequest)
	if secretResponse.Code != http.StatusOK {
		t.Fatalf("secret-input status=%d body=%s", secretResponse.Code, secretResponse.Body.String())
	}
	var secretEnvelope struct {
		Version  string                     `json:"version"`
		Resource string                     `json:"resource"`
		Data     MigrationSecretInputHandle `json:"data"`
		Errors   []string                   `json:"errors"`
	}
	if err := json.Unmarshal(secretResponse.Body.Bytes(), &secretEnvelope); err != nil {
		t.Fatal(err)
	}
	if secretEnvelope.Version != APIVersion ||
		secretEnvelope.Resource != "migration/secret-input" ||
		len(secretEnvelope.Errors) != 0 || secretEnvelope.Data.Validate() != nil {
		t.Fatalf("secret-input envelope=%+v", secretEnvelope)
	}

	inspectBody, err := json.Marshal(MigrationInspectAPIRequest{
		BundlePath: bundlePath, SecretInputHandle: secretEnvelope.Data.Handle,
	})
	if err != nil {
		t.Fatal(err)
	}
	inspectRequest := httptest.NewRequest(
		http.MethodPost, "/api/v1/migration/import/inspect", bytes.NewReader(inspectBody),
	)
	inspectRequest.Header.Set("Authorization", credential)
	inspectResponse := httptest.NewRecorder()
	api.serveMigrationImportInspect(inspectResponse, inspectRequest)
	if inspectResponse.Code != http.StatusOK {
		t.Fatalf("inspect status=%d body=%s", inspectResponse.Code, inspectResponse.Body.String())
	}
	var inspectEnvelope struct {
		Version  string                      `json:"version"`
		Resource string                      `json:"resource"`
		Data     MigrationReadOnlyInspection `json:"data"`
		Errors   []string                    `json:"errors"`
	}
	if err := json.Unmarshal(inspectResponse.Body.Bytes(), &inspectEnvelope); err != nil {
		t.Fatal(err)
	}
	if inspectEnvelope.Version != APIVersion ||
		inspectEnvelope.Resource != "migration/import/inspect" ||
		len(inspectEnvelope.Errors) != 0 ||
		inspectEnvelope.Data.Inventory.BundleID != secretEnvelope.Data.BundleID ||
		!inspectEnvelope.Data.Inventory.Sealed {
		t.Fatalf("inspect envelope=%+v", inspectEnvelope)
	}

	wrongBody, err := json.Marshal(MigrationSecretInputAPIRequest{
		Purpose:    MigrationSecretPurposeInspect,
		BundlePath: bundlePath,
		Passphrase: "wrong manager inspection passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongRequest := httptest.NewRequest(
		http.MethodPost, "/api/v1/migration/secret-input", bytes.NewReader(wrongBody),
	)
	wrongRequest.Header.Set("Authorization", credential)
	wrongResponse := httptest.NewRecorder()
	api.serveMigrationSecretInput(wrongResponse, wrongRequest)
	if wrongResponse.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(
			wrongResponse.Body.String(), "migration.bundle.authentication_failed",
		) || strings.Contains(wrongResponse.Body.String(), "wrong manager") {
		t.Fatalf("wrong-passphrase status=%d body=%s", wrongResponse.Code, wrongResponse.Body.String())
	}
}

func TestMigrationOperationActionRouteBindsCurrentRevisionAndExplicitCancellation(t *testing.T) {
	operation := migrationExportOperationFixture()
	now := operation.UpdatedAt.Add(time.Second)
	store := MigrationStore{Root: t.TempDir(), Now: func() time.Time { return now }}
	operation.Phase = MigrationPhaseClaiming
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	called := 0
	api := API{
		Now: func() time.Time { return now.Add(time.Second) },
		Migrations: &MigrationAPIService{
			Service: MigrationService{Store: store},
			Cancel: func(
				operationID string,
				request MigrationOperationActionAPIRequest,
				clientBinding string,
			) error {
				called++
				if operationID != operation.ID || clientBinding == "" || request.RetainPartial == nil {
					t.Fatalf("callback id=%q request=%+v client=%q", operationID, request, clientBinding)
				}
				_, err := store.RequestCancellation(
					operationID, request.Revision, request.RetainPartial,
				)
				if err != nil {
					t.Errorf("request cancellation: %v", err)
				}
				return err
			},
		},
	}
	body := fmt.Sprintf(`{"revision":%d,"retainPartial":true}`, operation.Revision)
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer operation-action-test")
	response := httptest.NewRecorder()
	api.serveMigrationOperationAction(response, request, operation.ID, "cancel")
	if response.Code != http.StatusOK || called != 1 {
		t.Fatalf("status=%d called=%d body=%s", response.Code, called, response.Body.String())
	}
	var envelope struct {
		Version  string                       `json:"version"`
		Resource string                       `json:"resource"`
		Data     MigrationOperationProjection `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != APIVersion || envelope.Resource != "migration/operation" ||
		envelope.Data.Revision <= operation.Revision ||
		envelope.Data.State != MigrationPhaseCancelling || !envelope.Data.Progress.CancelPending {
		t.Fatalf("action response=%+v", envelope)
	}

	stale := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	api.serveMigrationOperationAction(stale, staleRequest, operation.ID, "cancel")
	if stale.Code != http.StatusConflict || called != 1 {
		t.Fatalf("stale status=%d called=%d body=%s", stale.Code, called, stale.Body.String())
	}
}

func TestMigrationOperationRecoverRouteRejectsUnadvertisedActionBeforeCallback(t *testing.T) {
	store := MigrationStore{Root: t.TempDir()}
	operation := migrationExportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	called := false
	api := API{Migrations: &MigrationAPIService{
		Service: MigrationService{Store: store},
		Recover: func(string, MigrationOperationActionAPIRequest, string) error {
			called = true
			return nil
		},
	}}
	body := fmt.Sprintf(`{"revision":%d,"action":"rollback"}`, operation.Revision)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	api.serveMigrationOperationAction(response, request, operation.ID, "recover")
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t body=%s", response.Code, called, response.Body.String())
	}
}

func TestMigrationManagerExportPlanApplyAndSnapshotContract(t *testing.T) {
	managerRoot := t.TempDir()
	environments := environment.Store{Root: managerRoot}
	profiles := profile.Store{Root: managerRoot}
	if err := profiles.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	record := createStoppedMigrationEnvironment(t, environments, "dev", "hideout-dev")
	provider := newManagerMigrationProviderFixture()
	// Deliberately invert disk-ref and component-ID order. The encrypted record
	// stream must follow the component order consumed by destination providers,
	// not the unrelated source disk identities.
	provider.componentIDForDisk = func(diskRef migration.OpaqueID) migration.OpaqueID {
		if strings.Contains(string(diskRef), "attached") {
			return "component_z_attached0001"
		}
		return "component_a_root0000001"
	}
	now := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x45}, 256)),
	})
	defer secretInputs.Close()
	service := MigrationService{
		Store:        MigrationStore{Root: managerRoot, Now: func() time.Time { return now }},
		Environments: environments, Profiles: profiles,
		Export: provider, SecretInputs: secretInputs,
		ProductVersion: "0.1.0-alpha.4", HostOS: "darwin", HostArch: "arm64",
		Now: func() time.Time { return now }, NewID: sequentialMigrationIDSource(),
	}
	output := filepath.Join(t.TempDir(), "dev.hideout-migration")
	before, err := environments.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.PlanExport(context.Background(), migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeFull,
		EnvironmentNames: []string{"dev"}, IncludeSecretRefs: []string{},
		OutputPath:           output,
		RiskAcknowledgements: []string{MigrationRiskOpaqueGuestContent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMigrationExportPlan(plan); err != nil {
		t.Fatal(err)
	}
	after, err := environments.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || provider.snapshotCreates != 0 {
		t.Fatalf("planning mutated source or snapshotted: before=%+v after=%+v creates=%d", before, after, provider.snapshotCreates)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning created output: %v", err)
	}
	if len(plan.EnvironmentRefs) != 1 || plan.EnvironmentRefs[0] != migration.OpaqueID(record.ID) ||
		len(plan.DiskRefs) != 2 || len(plan.BaseRevisions) != 2 ||
		plan.SourceInventoryDigest.Validate() != nil ||
		!slices.Equal(plan.IncludedClasses, []string{
			"environment-declarations", "persistent-disks", "portable-profiles",
			"profile-application-state",
		}) || len(plan.EnvironmentEstimates) != 1 || len(plan.DiskEstimates) != 2 ||
		plan.EnvironmentEstimates[0].DisplayName != "dev" ||
		plan.EnvironmentEstimates[0].PortableConfigLogicalBytes == 0 ||
		plan.EnvironmentEstimates[0].PortableConfigDigest.Validate() != nil ||
		plan.EnvironmentEstimates[0].ProfileStateLogicalBytes == 0 ||
		plan.EnvironmentEstimates[0].ProfileStateDigest.Validate() != nil ||
		!slices.Equal(plan.EnvironmentEstimates[0].DiskRefs, plan.DiskRefs) ||
		plan.EnvironmentEstimates[0].ReferencedDiskLogicalBytes != 12288 ||
		plan.EnvironmentEstimates[0].EstimatedLogicalBytes !=
			plan.EnvironmentEstimates[0].PortableConfigLogicalBytes+
				plan.EnvironmentEstimates[0].ProfileStateLogicalBytes+12288 ||
		plan.EstimatedPayloadLogicalBytes !=
			plan.EnvironmentEstimates[0].PortableConfigLogicalBytes+
				plan.EnvironmentEstimates[0].ProfileStateLogicalBytes+12288 ||
		!plan.EstimatedPayloadComplete {
		t.Fatalf("unexpected export plan: %+v", plan)
	}

	passphrase := []byte("correct horse battery staple")
	secret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-A",
		BundleID: "migb_managerjourney1", Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	apply := MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: plan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: secret.Handle, ClientBinding: "client-A",
		IdempotencyKey: "export-request-0001",
	}
	type applyResponse struct {
		result MigrationApplyResult
		err    error
	}
	responses := make(chan applyResponse, 8)
	for range 8 {
		go func() {
			result, err := service.ApplyExport(context.Background(), apply)
			responses <- applyResponse{result: result, err: err}
		}()
	}
	var first MigrationApplyResult
	var applyErrors []error
	created := 0
	for range 8 {
		response := <-responses
		if response.err != nil {
			applyErrors = append(applyErrors, response.err)
			continue
		}
		result := response.result
		if first.OperationID == "" {
			first = result
		} else if result.OperationID != first.OperationID {
			t.Fatalf("concurrent apply produced another operation: first=%+v result=%+v", first, result)
		}
		if result.Created {
			created++
		}
	}
	if len(applyErrors) != 0 {
		t.Fatalf("concurrent apply errors=%v", applyErrors)
	}
	if created != 1 || first.State != MigrationPhaseClaiming || first.OperationID == "" {
		t.Fatalf("concurrent apply created=%d first=%+v", created, first)
	}
	replay, err := service.ApplyExport(context.Background(), apply)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Created || replay.OperationID != first.OperationID {
		t.Fatalf("apply replay=%+v first=%+v", replay, first)
	}
	operation, err := service.Store.Load(first.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Bundle.BundleID != secret.BundleID ||
		operation.SourceInventoryDigest != plan.SourceInventoryDigest ||
		operation.Phase != MigrationPhaseClaiming {
		t.Fatalf("durable export operation=%+v", operation)
	}
	encoded, err := os.ReadFile(service.Store.OperationPath(first.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret.Handle, "correct horse", "passphrase", "secretInputHandle"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("operation persisted secret material %q: %s", forbidden, encoded)
		}
	}

	otherPlan := plan
	otherPlan.PlanID = "migplan_different1"
	if err := SealMigrationExportPlan(&otherPlan); err != nil {
		t.Fatal(err)
	}
	apply.Plan = otherPlan
	apply.Confirmation.PlanDigest = otherPlan.PlanDigest
	if _, err := service.ApplyExport(context.Background(), apply); !errors.Is(err, ErrMigrationOperationMismatch) {
		t.Fatalf("same idempotency key accepted another plan: %v", err)
	}

	operation, snapshot, err := service.SnapshotExportSource(context.Background(), first.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != MigrationPhaseWriting || len(snapshot.Components) != 2 ||
		provider.snapshotCreates != 1 {
		t.Fatalf("snapshot operation=%+v snapshot=%+v creates=%d", operation, snapshot, provider.snapshotCreates)
	}
	if snapshot.Components[0].ComponentID >= snapshot.Components[1].ComponentID ||
		snapshot.Components[0].DiskRef <= snapshot.Components[1].DiskRef {
		t.Fatalf("fixture did not invert component and disk order: %+v", snapshot.Components)
	}
	operation, snapshot, err = service.SnapshotExportSource(context.Background(), first.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != MigrationPhaseWriting || len(snapshot.Components) != 2 ||
		provider.snapshotCreates != 1 {
		t.Fatalf("snapshot replay duplicated state: operation=%+v creates=%d", operation, provider.snapshotCreates)
	}
	unchanged, err := environments.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, unchanged) {
		t.Fatalf("snapshot orchestration changed source record: before=%+v after=%+v", before, unchanged)
	}

	written, err := service.WriteAndSealExportBundle(context.Background(), MigrationExportWorkerRequest{
		OperationID: first.OperationID, Snapshot: snapshot,
		SecretInputHandle: secret.Handle, SecretPurpose: MigrationSecretPurposeExportCreate,
		ClientBinding: "client-A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if written.Operation.Phase != MigrationPhaseComplete || written.OutputPath != output ||
		written.Binding.BundleID != secret.BundleID || provider.releaseCalls != 1 {
		t.Fatalf("sealed export result=%+v releases=%d", written, provider.releaseCalls)
	}
	outputInfo, err := os.Lstat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !outputInfo.Mode().IsRegular() || outputInfo.Mode().Perm() != 0o600 {
		t.Fatalf("export output mode=%v", outputInfo.Mode())
	}
	if _, err := os.Lstat(migrationExportPartialPath(output, first.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed export retained partial: %v", err)
	}
	sealedFile, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	inspection, inspectErr := migration.InspectSealedBundle(
		context.Background(), sealedFile, outputInfo.Size(), passphrase,
	)
	closeErr := sealedFile.Close()
	if inspectErr != nil || closeErr != nil {
		t.Fatalf("inspect sealed export: inspect=%v close=%v", inspectErr, closeErr)
	}
	if inspection.Binding != written.Binding || len(inspection.Manifest.Environments) != 1 ||
		len(inspection.Manifest.DiskObjects) != 2 || len(inspection.Manifest.ComponentIndex) != 4 ||
		inspection.Manifest.Environments[0].ProfileStateComponentID == "" ||
		len(inspection.Manifest.Environments[0].WorkspaceProposals) != 1 ||
		inspection.Manifest.Environments[0].WorkspaceProposals[0].State != "disabled" ||
		!slices.Equal(inspection.Manifest.ExcludedClasses,
			[]string{"activity-history", "host-workspace-content", "runtime-state"}) {
		t.Fatalf("sealed manifest=%+v", inspection.Manifest)
	}
	diskComponents := make([]migration.OpaqueID, 0, 2)
	for _, component := range inspection.Manifest.ComponentIndex {
		if component.Kind == "disk" {
			diskComponents = append(diskComponents, component.ComponentID)
		}
	}
	if !slices.Equal(diskComponents, []migration.OpaqueID{
		"component_a_root0000001", "component_z_attached0001",
	}) {
		t.Fatalf("disk records do not follow destination component order: %v", diskComponents)
	}
	for _, claim := range written.Operation.Claims {
		if claim.State != MigrationClaimReleased {
			t.Fatalf("completed export retained claim: %+v", claim)
		}
	}
}

func TestMigrationExportResumesAuthenticatedPartialWithoutRereadingPrefix(t *testing.T) {
	root := t.TempDir()
	environments := environment.Store{Root: root}
	profiles := profile.Store{Root: root}
	profileValue := profile.Default("default")
	profileValue.Network.ProxySecretRef = "local-proxy"
	if err := profiles.Save(profileValue); err != nil {
		t.Fatal(err)
	}
	createStoppedMigrationEnvironment(t, environments, "dev", "hideout-dev")
	provider := newManagerMigrationProviderFixture()
	provider.failReadAfter = 2048
	provider.failReadOnce = true
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x73}, 1024)),
	})
	defer secretInputs.Close()
	secretStore := newManagerSecretStoreFixture()
	if _, err := secretStore.Set(context.Background(), secrets.WriteRequest{
		Ref: "local-proxy", OperationID: "op_export_resume_secret",
		ExpectedGeneration: 0, Value: secretBufferFixture(t, "socks5://127.0.0.1:7890"),
	}); err != nil {
		t.Fatal(err)
	}
	service := MigrationService{
		Store:        MigrationStore{Root: root, Now: func() time.Time { return now }},
		Environments: environments, Profiles: profiles,
		Export: provider, SecretInputs: secretInputs, Secrets: secretStore,
		ProductVersion: "0.1.0-alpha.4", HostOS: "darwin", HostArch: "arm64",
		Now: func() time.Time { return now }, NewID: sequentialMigrationIDSource(),
	}
	output := filepath.Join(t.TempDir(), "dev.hideout-migration")
	plan, err := service.PlanExport(context.Background(), migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeFull,
		EnvironmentNames: []string{"dev"}, IncludeSecretRefs: []string{"local-proxy"},
		OutputPath: output,
		RiskAcknowledgements: []string{
			MigrationRiskOpaqueGuestContent, MigrationRiskSelectedSecrets,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("resume the exact encrypted export")
	createSecret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-resume",
		BundleID: "migb_managerresume1", Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := service.ApplyExport(context.Background(), MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: plan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: createSecret.Handle, ClientBinding: "client-resume",
		IdempotencyKey: "export-resume-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := service.SnapshotExportSource(context.Background(), apply.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteAndSealExportBundle(context.Background(), MigrationExportWorkerRequest{
		OperationID: apply.OperationID, Snapshot: snapshot,
		SecretInputHandle: createSecret.Handle,
		SecretPurpose:     MigrationSecretPurposeExportCreate,
		ClientBinding:     "client-resume",
	}); err == nil {
		t.Fatal("injected provider interruption unexpectedly completed")
	}
	interrupted, err := service.Store.Load(apply.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Phase != MigrationPhaseRecoverableFailure ||
		interrupted.Progress.CheckpointDigest == "" ||
		interrupted.Progress.CompletedLogicalBytes !=
			plan.EnvironmentEstimates[0].ProfileStateLogicalBytes+2048 ||
		interrupted.Progress.ComponentsComplete != 3 {
		t.Fatalf("interrupted operation=%+v", interrupted)
	}
	partial := migrationExportPartialPath(output, apply.OperationID)
	if info, err := os.Lstat(partial); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("recoverable partial: info=%v err=%v", info, err)
	}
	partialBinding, err := bindMigrationExportArtifact(partial)
	if err != nil {
		t.Fatal(err)
	}
	resumeSecret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportResume, ClientBinding: "client-resume",
		BundleID: createSecret.BundleID, BundleFile: &partialBinding,
		Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := service.WriteAndSealExportBundle(context.Background(), MigrationExportWorkerRequest{
		OperationID: apply.OperationID, Snapshot: snapshot,
		SecretInputHandle: resumeSecret.Handle,
		SecretPurpose:     MigrationSecretPurposeExportResume,
		BundleFile:        &partialBinding,
		ClientBinding:     "client-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Operation.Phase != MigrationPhaseComplete || resumed.OutputPath != output ||
		provider.releaseCalls != 1 {
		t.Fatalf("resumed result=%+v releases=%d", resumed, provider.releaseCalls)
	}
	foundResumeOffset := false
	for _, request := range provider.readRequests {
		if request.ComponentID == "component_attached0001" && request.ResumeOffset == 2048 {
			foundResumeOffset = true
		}
	}
	if !foundResumeOffset {
		t.Fatalf("provider reads restarted verified prefix: %+v", provider.readRequests)
	}
	if _, err := os.Lstat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resumed export retained partial: %v", err)
	}
}

func TestDurableExportOrchestrationCrashCuts(t *testing.T) {
	t.Run("source claims acquired", func(t *testing.T) {
		fixture := newDurableExportCrashFixture(t)
		crashErr := errors.New("injected claim-prefix process loss")
		fixture.service.Store.afterClaimWrite = func(index int, _ MigrationClaim) error {
			if index == 0 {
				return crashErr
			}
			return nil
		}
		if _, _, err := fixture.service.SnapshotExportSource(
			context.Background(), fixture.operationID,
		); !errors.Is(err, crashErr) {
			t.Fatalf("claim-prefix crash error=%v", err)
		}

		cleanStore := MigrationStore{Root: fixture.root, Now: fixture.service.Now}
		interrupted, err := cleanStore.Load(fixture.operationID)
		if err != nil {
			t.Fatal(err)
		}
		if interrupted.Phase != MigrationPhaseClaiming || fixture.provider.snapshotCalls != 0 {
			t.Fatalf("claim-prefix operation=%+v snapshotCalls=%d",
				interrupted, fixture.provider.snapshotCalls)
		}
		for _, claim := range interrupted.Claims {
			if claim.State != MigrationClaimPending {
				t.Fatalf("partial claim prefix became visible in operation: %+v", claim)
			}
		}

		restarted := fixture.service
		restarted.Store = cleanStore
		resumed, snapshot, err := restarted.SnapshotExportSource(
			context.Background(), fixture.operationID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if resumed.Phase != MigrationPhaseWriting || snapshot.Validate() != nil ||
			fixture.provider.snapshotCreates != 1 {
			t.Fatalf("claim-prefix resume operation=%+v snapshot=%+v creates=%d",
				resumed, snapshot, fixture.provider.snapshotCreates)
		}
		for _, claim := range resumed.Claims {
			if claim.State != MigrationClaimHeld {
				t.Fatalf("resumed export did not hold every claim: %+v", resumed.Claims)
			}
		}
	})

	t.Run("provider snapshot created", func(t *testing.T) {
		fixture := newDurableExportCrashFixture(t)
		fixture.provider.snapshotResponseLossOnce = true
		if _, _, err := fixture.service.SnapshotExportSource(
			context.Background(), fixture.operationID,
		); !errors.Is(err, errManagerSnapshotResponseLost) {
			t.Fatalf("snapshot response-loss error=%v", err)
		}
		interrupted, err := fixture.service.Store.Load(fixture.operationID)
		if err != nil {
			t.Fatal(err)
		}
		effect, effectErr := migrationOperationEffect(interrupted, MigrationEffectSnapshot)
		if effectErr != nil || interrupted.Phase != MigrationPhaseRecoverableFailure ||
			effect.Status != MigrationEffectRunning || fixture.provider.snapshotCreates != 1 {
			t.Fatalf("snapshot response-loss operation=%+v effect=%+v creates=%d err=%v",
				interrupted, effect, fixture.provider.snapshotCreates, effectErr)
		}

		restarted := fixture.service
		restarted.Store = MigrationStore{Root: fixture.root, Now: fixture.service.Now}
		resumed, snapshot, err := restarted.SnapshotExportSource(
			context.Background(), fixture.operationID,
		)
		if err != nil {
			t.Fatal(err)
		}
		effect, effectErr = migrationOperationEffect(resumed, MigrationEffectSnapshot)
		if effectErr != nil || resumed.Phase != MigrationPhaseWriting ||
			effect.Status != MigrationEffectSucceeded || snapshot.Validate() != nil ||
			fixture.provider.snapshotCalls != 2 || fixture.provider.snapshotCreates != 1 {
			t.Fatalf("snapshot replay operation=%+v effect=%+v snapshot=%+v calls=%d creates=%d err=%v",
				resumed, effect, snapshot, fixture.provider.snapshotCalls,
				fixture.provider.snapshotCreates, effectErr)
		}
	})
}

func TestDurableExportPublishCrashCut(t *testing.T) {
	fixture := newDurableExportCrashFixture(t)
	_, snapshot, err := fixture.service.SnapshotExportSource(
		context.Background(), fixture.operationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.provider.afterReadStart = func(backend.ComponentReadRequest) error {
		return os.WriteFile(fixture.output, []byte("late output conflict"), 0o600)
	}
	_, err = fixture.service.WriteAndSealExportBundle(
		context.Background(), MigrationExportWorkerRequest{
			OperationID: fixture.operationID, Snapshot: snapshot,
			SecretInputHandle: fixture.createSecret.Handle,
			SecretPurpose:     MigrationSecretPurposeExportCreate,
			ClientBinding:     fixture.clientBinding,
		},
	)
	if !errors.Is(err, ErrMigrationOutputConflict) {
		t.Fatalf("pre-publish crash-cut error=%v", err)
	}
	interrupted, err := fixture.service.Store.Load(fixture.operationID)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Phase != MigrationPhaseRecoverableFailure {
		t.Fatalf("pre-publish operation=%+v", interrupted)
	}
	partial := migrationExportPartialPath(fixture.output, fixture.operationID)
	inspection, err := inspectMigrationSealedExport(
		context.Background(), partial, fixture.passphrase, fixture.createSecret.BundleID,
	)
	if err != nil || !inspection.Summary.Sealed {
		t.Fatalf("pre-publish partial inspection=%+v err=%v", inspection, err)
	}
	partialBytes, err := os.ReadFile(partial)
	if err != nil {
		t.Fatal(err)
	}
	partialDigest := sha256.Sum256(partialBytes)
	clear(partialBytes)
	readsBeforeResume := len(fixture.provider.readRequests)
	if readsBeforeResume == 0 {
		t.Fatal("pre-publish cut did not stream any provider component")
	}
	if err := os.Remove(fixture.output); err != nil {
		t.Fatal(err)
	}
	partialBinding, err := bindMigrationExportArtifact(partial)
	if err != nil {
		t.Fatal(err)
	}
	resumeSecret, err := fixture.service.SecretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportResume, ClientBinding: fixture.clientBinding,
		BundleID: fixture.createSecret.BundleID, BundleFile: &partialBinding,
		Passphrase: fixture.passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}

	restarted := fixture.service
	restarted.Store = MigrationStore{Root: fixture.root, Now: fixture.service.Now}
	completed, err := restarted.WriteAndSealExportBundle(
		context.Background(), MigrationExportWorkerRequest{
			OperationID: fixture.operationID, Snapshot: snapshot,
			SecretInputHandle: resumeSecret.Handle,
			SecretPurpose:     MigrationSecretPurposeExportResume,
			BundleFile:        &partialBinding,
			ClientBinding:     fixture.clientBinding,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Operation.Phase != MigrationPhaseComplete ||
		len(fixture.provider.readRequests) != readsBeforeResume || fixture.provider.releaseCalls != 1 {
		t.Fatalf("pre-publish resume=%+v reads=%d want=%d releases=%d",
			completed, len(fixture.provider.readRequests), readsBeforeResume,
			fixture.provider.releaseCalls)
	}
	outputBytes, err := os.ReadFile(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	outputDigest := sha256.Sum256(outputBytes)
	clear(outputBytes)
	if outputDigest != partialDigest {
		t.Fatal("sealed footer recovery rewrote the authenticated artifact")
	}
	if _, err := os.Lstat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published recovery retained partial: %v", err)
	}
}

func TestMigrationExportPlanDigestAndSourceRevisionFailClosed(t *testing.T) {
	root := t.TempDir()
	environments := environment.Store{Root: root}
	profiles := profile.Store{Root: root}
	if err := profiles.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	record := createStoppedMigrationEnvironment(t, environments, "dev", "hideout-dev")
	provider := newManagerMigrationProviderFixture()
	service := MigrationService{
		Store: MigrationStore{Root: root}, Environments: environments, Profiles: profiles,
		Export: provider, NewID: sequentialMigrationIDSource(),
	}
	plan, err := service.PlanExport(context.Background(), migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeFull,
		EnvironmentNames: []string{"dev"}, IncludeSecretRefs: []string{},
		OutputPath:           filepath.Join(t.TempDir(), "dev.hideout-migration"),
		RiskAcknowledgements: []string{MigrationRiskOpaqueGuestContent},
	})
	if err != nil {
		t.Fatal(err)
	}
	mutated := plan
	mutated.OutputPath = filepath.Join(t.TempDir(), "other.hideout-migration")
	if err := VerifyMigrationExportPlan(mutated); !errors.Is(err, ErrMigrationPlanInvalid) {
		t.Fatalf("tampered plan error=%v", err)
	}
	record.LastCommand = "changed-after-plan"
	if err := environments.Save(record); err != nil {
		t.Fatal(err)
	}
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	service.SecretInputs = secretInputs
	secret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-A",
		BundleID: "migb_managerjourney2", Passphrase: []byte("passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyExport(context.Background(), MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: plan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: secret.Handle, ClientBinding: "client-A",
		IdempotencyKey: "export-request-0002",
	})
	if !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("changed source revision error=%v", err)
	}
	if provider.snapshotCreates != 0 {
		t.Fatalf("stale apply created snapshot: %d", provider.snapshotCreates)
	}
}

func TestMigrationExportPortableProfileDigestClosesSameSizePlanRace(t *testing.T) {
	root := t.TempDir()
	environments := environment.Store{Root: root}
	profiles := profile.Store{Root: root}
	profileValue := profile.Default("default")
	profileValue.Git.UserName = "alice"
	if err := profiles.Save(profileValue); err != nil {
		t.Fatal(err)
	}
	createStoppedMigrationEnvironment(t, environments, "dev", "hideout-dev")
	provider := newManagerMigrationProviderFixture()
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	service := MigrationService{
		Store: MigrationStore{Root: root}, Environments: environments, Profiles: profiles,
		Export: provider, SecretInputs: secretInputs,
		NewID: sequentialMigrationIDSource(),
	}
	plan, err := service.PlanExport(context.Background(), migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeFull,
		EnvironmentNames: []string{"dev"}, IncludeSecretRefs: []string{},
		OutputPath:           filepath.Join(t.TempDir(), "dev.hideout-migration"),
		RiskAcknowledgements: []string{MigrationRiskOpaqueGuestContent},
	})
	if err != nil {
		t.Fatal(err)
	}
	profileValue.Git.UserName = "blice"
	portable, err := migration.NormalizePortableProfile(profileValue)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := migration.EncodePortableProfile(portable)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(encoded)) != plan.EnvironmentEstimates[0].PortableConfigLogicalBytes ||
		migrationExportBytesDigest(encoded) == plan.EnvironmentEstimates[0].PortableConfigDigest {
		t.Fatalf(
			"fixture did not preserve size while changing content: bytes=%d plan=%+v",
			len(encoded), plan.EnvironmentEstimates[0],
		)
	}
	clear(encoded)
	if err := profiles.Save(profileValue); err != nil {
		t.Fatal(err)
	}
	secret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-profile-race",
		BundleID: "migb_profilerace001", Passphrase: []byte("passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyExport(context.Background(), MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: plan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: secret.Handle, ClientBinding: "client-profile-race",
		IdempotencyKey: "export-profile-race-0001",
	})
	if !errors.Is(err, ErrMigrationPlanStale) || provider.snapshotCreates != 0 {
		t.Fatalf("same-size profile race error=%v snapshots=%d", err, provider.snapshotCreates)
	}
}

func TestMigrationExportLifecycleRaceAfterApplyAbortsBeforeSnapshot(t *testing.T) {
	root := t.TempDir()
	environments := environment.Store{Root: root}
	profiles := profile.Store{Root: root}
	if err := profiles.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	record := createStoppedMigrationEnvironment(t, environments, "dev", "hideout-dev")
	provider := newManagerMigrationProviderFixture()
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	service := MigrationService{
		Store: MigrationStore{Root: root}, Environments: environments, Profiles: profiles,
		Export: provider, SecretInputs: secretInputs,
		NewID: sequentialMigrationIDSource(),
	}
	plan, err := service.PlanExport(context.Background(), migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeFull,
		EnvironmentNames: []string{"dev"}, IncludeSecretRefs: []string{},
		OutputPath:           filepath.Join(t.TempDir(), "dev.hideout-migration"),
		RiskAcknowledgements: []string{MigrationRiskOpaqueGuestContent},
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-race",
		BundleID: "migb_lifecyclerace0001", Passphrase: []byte("passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := service.ApplyExport(context.Background(), MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: plan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: secret.Handle, ClientBinding: "client-race",
		IdempotencyKey: "export-lifecycle-race-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = environment.StatusRunning
	if err := environments.Save(record); err != nil {
		t.Fatal(err)
	}
	_, _, err = service.SnapshotExportSource(context.Background(), apply.OperationID)
	if !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("lifecycle race error=%v", err)
	}
	if provider.snapshotCreates != 0 || provider.snapshotCalls != 0 {
		t.Fatalf(
			"lifecycle race reached provider snapshot: calls=%d creates=%d",
			provider.snapshotCalls, provider.snapshotCreates,
		)
	}
}

func TestMigrationManagerImportPolicyIsPerDestinationAndBundleStaysReusable(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "dev.hideout-migration")
	bundleBytes := []byte("sealed-bundle-fixture-that-must-remain-unchanged")
	if err := os.WriteFile(bundlePath, bundleBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	binding := migration.BundleBinding{
		BundleID: "migb_multidestination1", FormatVersion: migration.BundleFormatVersion,
		FileDigest:       migration.Digest("sha256:" + strings.Repeat("1", 64)),
		ManifestDigest:   migration.Digest("sha256:" + strings.Repeat("2", 64)),
		CompletionDigest: migration.Digest("sha256:" + strings.Repeat("3", 64)),
	}
	fileBinding := migrationBundleFileBindingFixture()
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	bundleSource := &managerBundleSourceFixture{
		secretInputs: secretInputs, path: bundlePath,
		inspection: MigrationBundleInspection{
			Binding: binding, BundleFile: fileBinding,
			Manifest: managerMigrationManifestFixture(binding.BundleID),
		},
	}
	provider := newManagerMigrationProviderFixture()
	idSource := sequentialMigrationIDSource()
	newDestination := func() MigrationImportService {
		root := t.TempDir()
		base := MigrationService{
			Store: MigrationStore{Root: root}, Environments: environment.Store{Root: root},
			Export: provider, Import: provider, SecretInputs: secretInputs,
			NewID: idSource,
		}
		return MigrationImportService{MigrationService: base, BundleSource: bundleSource}
	}
	firstDestination := newDestination()
	secondDestination := newDestination()
	draft := migration.ImportDraft{
		Schema: MigrationImportDraftSchema, BundlePath: bundlePath, BundleBinding: binding,
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
	planOn := func(
		service MigrationImportService,
		client string,
		policy migration.GuestIdentityPolicy,
	) migration.ImportPlan {
		t.Helper()
		selected := draft
		selected.IdentityPolicies = append(
			[]migration.IdentitySelection(nil), draft.IdentityPolicies...,
		)
		selected.IdentityPolicies[0].Policy = policy
		if policy == migration.GuestIdentityExactRestore {
			selected.RiskAcknowledgements = []string{MigrationRiskExactGuestRestore}
		}
		handle := createManagerImportSecretHandle(
			t, secretInputs, binding.BundleID, fileBinding, client,
		)
		plan, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
			Draft: selected, SecretInputHandle: handle.Handle, ClientBinding: client,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyMigrationImportPlan(plan); err != nil {
			t.Fatal(err)
		}
		if !plan.Compatibility.Available || len(plan.Blockers) != 0 ||
			len(plan.IdentityActions) != 1 ||
			plan.IdentityActions[0].GuestPolicy != policy {
			t.Fatalf("unexpected import plan: %+v", plan)
		}
		return plan
	}
	firstPlan := planOn(
		firstDestination, "client-plan-first", migration.GuestIdentitySafeClone,
	)
	secondPlan := planOn(
		secondDestination, "client-plan-second", migration.GuestIdentitySafeClone,
	)
	if firstPlan.PlanID == secondPlan.PlanID || firstPlan.PlanDigest == secondPlan.PlanDigest {
		t.Fatalf("independent destination reviews shared a plan binding: first=%+v second=%+v", firstPlan, secondPlan)
	}
	if provider.stageCalls != 0 {
		t.Fatalf("planning staged destination state: %d", provider.stageCalls)
	}
	before, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}

	applyOn := func(
		service MigrationImportService,
		client, key string,
		selectedPlan migration.ImportPlan,
	) MigrationOperation {
		t.Helper()
		handle := createManagerImportSecretHandle(
			t, secretInputs, binding.BundleID, fileBinding, client,
		)
		result, err := service.ApplyImport(context.Background(), MigrationImportApplyRequest{
			Schema: MigrationImportApplySchema, Plan: selectedPlan,
			Confirmation: MigrationPlanConfirmation{
				PlanDigest: selectedPlan.PlanDigest,
				AcceptedRiskAcknowledgements: append(
					[]string(nil), selectedPlan.RiskAcknowledgements...,
				),
			},
			SecretInputHandle: handle.Handle, ClientBinding: client, IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Created || result.State != MigrationPhaseClaiming {
			t.Fatalf("apply result=%+v", result)
		}
		operation, err := service.Store.Load(result.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		return operation
	}
	// Identical client and idempotency inputs on independent destination stores
	// must still produce independent operation and Hideout identities.
	first := applyOn(firstDestination, "same-client", "import-request-0001", firstPlan)
	second := applyOn(secondDestination, "same-client", "import-request-0001", secondPlan)
	encodedOperation, err := os.ReadFile(firstDestination.Store.OperationPath(first.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"destination import passphrase", "secretInputHandle", "migh_",
	} {
		if bytes.Contains(encodedOperation, []byte(forbidden)) {
			t.Fatalf("import operation persisted secret input material %q", forbidden)
		}
	}
	inspectCalls := bundleSource.calls
	replay, err := firstDestination.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: firstPlan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: firstPlan.PlanDigest},
		SecretInputHandle: "migh_" + strings.Repeat("A", 32),
		ClientBinding:     "same-client", IdempotencyKey: "import-request-0001",
	})
	if err != nil || replay.Created || replay.OperationID != first.ID {
		t.Fatalf("import replay result=%+v error=%v", replay, err)
	}
	if bundleSource.calls != inspectCalls {
		t.Fatalf("idempotent replay re-opened bundle: before=%d after=%d", inspectCalls, bundleSource.calls)
	}
	changedPlan := firstPlan
	changedPlan.Objects = append([]migration.ImportObject(nil), firstPlan.Objects...)
	changedPlan.EnvironmentActions = append(
		[]migration.EnvironmentAction(nil),
		firstPlan.EnvironmentActions...,
	)
	changedPlan.Objects[0].DestinationName = "dev-other"
	changedPlan.EnvironmentActions[0].DestinationProfileName = "dev-other"
	changedPlan.PlanDigest = ""
	if err := SealMigrationImportPlan(&changedPlan); err != nil {
		t.Fatal(err)
	}
	_, err = firstDestination.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: changedPlan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: changedPlan.PlanDigest},
		SecretInputHandle: "migh_" + strings.Repeat("B", 32),
		ClientBinding:     "same-client", IdempotencyKey: "import-request-0001",
	})
	if !errors.Is(err, ErrMigrationOperationMismatch) || bundleSource.calls != inspectCalls {
		t.Fatalf("changed-plan replay error=%v bundleInspections=%d", err, bundleSource.calls)
	}
	if first.Bundle != second.Bundle || first.BundlePath != second.BundlePath ||
		len(first.DestinationIdentities) != 1 || len(second.DestinationIdentities) != 1 {
		t.Fatalf("multi-destination bundle binding drifted: first=%+v second=%+v", first, second)
	}
	firstIdentity := first.DestinationIdentities[0]
	secondIdentity := second.DestinationIdentities[0]
	if first.ID == second.ID || firstIdentity.ControlIdentity == secondIdentity.ControlIdentity ||
		firstIdentity.BackendIdentity == secondIdentity.BackendIdentity {
		t.Fatalf("separate imports reused destination identities: first=%+v second=%+v", firstIdentity, secondIdentity)
	}

	exactDestination := newDestination()
	exactDraft := draft
	exactDraft.NameMappings = []migration.NameMapping{{
		SourceRef: "environment_source1", DestinationName: "dev-exact",
	}}
	exactDraft.IdentityPolicies = []migration.IdentitySelection{{
		SourceRef: "environment_source1", Policy: migration.GuestIdentityExactRestore,
	}}
	exactDraft.RiskAcknowledgements = []string{MigrationRiskExactGuestRestore}
	exactPlanHandle := createManagerImportSecretHandle(
		t, secretInputs, binding.BundleID, fileBinding, "client-exact-plan",
	)
	exactPlan, err := exactDestination.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: exactDraft, SecretInputHandle: exactPlanHandle.Handle,
		ClientBinding: "client-exact-plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exactPlan.PlanDigest == firstPlan.PlanDigest ||
		exactPlan.IdentityActions[0].GuestPolicy != migration.GuestIdentityExactRestore ||
		!slices.Equal(exactPlan.RiskAcknowledgements, []string{MigrationRiskExactGuestRestore}) {
		t.Fatalf("exact restore was not an import-time reviewed policy: %+v", exactPlan)
	}
	exact := applyOn(exactDestination, "client-C", "import-request-0001", exactPlan)
	if exact.DestinationIdentities[0].ControlIdentity == firstIdentity.ControlIdentity ||
		exact.DestinationIdentities[0].BackendIdentity == firstIdentity.BackendIdentity {
		t.Fatalf("exact guest restore reused Hideout destination identity: %+v", exact.DestinationIdentities)
	}
	after, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !bytes.Equal(after, bundleBytes) {
		t.Fatal("planning or applying an import modified the sealed bundle")
	}
	if provider.stageCalls != 0 {
		t.Fatalf("apply created provider state before the worker effect: %d", provider.stageCalls)
	}
}

func createStoppedMigrationEnvironment(
	t *testing.T,
	store environment.Store,
	name, instance string,
) environment.Record {
	t.Helper()
	profiles := profile.Store{Root: store.Root}
	if _, err := profiles.Load("default"); err != nil {
		if err := profiles.Save(profile.Default("default")); err != nil {
			t.Fatal(err)
		}
	}
	workspace := t.TempDir()
	record, err := store.Create(environment.Spec{
		Name: name, ImageRef: environment.BuiltinBaseImage, Profile: "default",
		Backend: "lima", Mode: environment.ModeDedicated,
		MachineIdentityID:   "sha256:" + strings.Repeat("1", 64),
		BootConfigurationID: "sha256:" + strings.Repeat("2", 64),
		DedicatedWorkspace:  workspace, DedicatedGuestRoot: "/workspace",
		User: "developer", Hostname: name, InstanceName: instance,
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = environment.StatusStopped
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	return record
}

func sequentialMigrationIDSource() MigrationIDSource {
	sequence := 0
	return func(prefix string) (migration.OpaqueID, error) {
		sequence++
		return migration.OpaqueID(fmt.Sprintf("%s_%08d", prefix, sequence)), nil
	}
}

type durableExportCrashFixture struct {
	root          string
	output        string
	operationID   string
	clientBinding string
	passphrase    []byte
	createSecret  MigrationSecretInputHandle
	service       MigrationService
	provider      *managerMigrationProviderFixture
}

func newDurableExportCrashFixture(t *testing.T) durableExportCrashFixture {
	t.Helper()
	root := t.TempDir()
	environments := environment.Store{Root: root}
	profiles := profile.Store{Root: root}
	if err := profiles.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	createStoppedMigrationEnvironment(t, environments, "durable", "hideout-durable")
	provider := newManagerMigrationProviderFixture()
	now := time.Date(2026, 8, 4, 1, 0, 0, 0, time.UTC)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x6d}, 4096)),
	})
	t.Cleanup(secretInputs.Close)
	service := MigrationService{
		Store:        MigrationStore{Root: root, Now: func() time.Time { return now }},
		Environments: environments, Profiles: profiles, Export: provider,
		SecretInputs: secretInputs, ProductVersion: "0.1.0", HostOS: "darwin",
		HostArch: "arm64", Now: func() time.Time { return now },
		NewID: sequentialMigrationIDSource(),
	}
	output := filepath.Join(t.TempDir(), "durable.hideout-migration")
	plan, err := service.PlanExport(context.Background(), migration.ExportRequest{
		Schema: MigrationExportRequestSchema, Mode: migration.ExportModeFull,
		EnvironmentNames: []string{"durable"}, IncludeSecretRefs: []string{},
		OutputPath: output, RiskAcknowledgements: []string{MigrationRiskOpaqueGuestContent},
	})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("durable export crash-cut passphrase")
	clientBinding := "durable-export-client"
	createSecret, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: clientBinding,
		BundleID: "migb_durableexport1", Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := service.ApplyExport(context.Background(), MigrationExportApplyRequest{
		Schema: MigrationExportApplySchema, Plan: plan,
		Confirmation: MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: createSecret.Handle, ClientBinding: clientBinding,
		IdempotencyKey: "durable-export-request-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return durableExportCrashFixture{
		root: root, output: output, operationID: apply.OperationID,
		clientBinding: clientBinding, passphrase: passphrase, createSecret: createSecret,
		service: service, provider: provider,
	}
}

var errManagerSnapshotResponseLost = errors.New("injected snapshot response loss")

type managerMigrationProviderFixture struct {
	capability               backend.MigrationCapabilities
	snapshots                map[string]backend.SourceSnapshot
	snapshotCalls            int
	snapshotCreates          int
	stageCalls               int
	stageDelegate            MigrationDestinationStager
	destinationInspectCalls  int
	adoptCalls               int
	adoptRequests            []backend.DestinationAdoptionRequest
	adoptResponse            func(backend.DestinationAdoptionRequest, int) (backend.DestinationAdoption, error)
	verifyCalls              int
	verifyRequests           []backend.DestinationVerifyRequest
	verifyResponse           func(backend.DestinationVerifyRequest, int) (backend.DestinationProof, error)
	activateCalls            int
	activateRequests         []backend.DestinationActivationRequest
	activateResponse         func(backend.DestinationActivationRequest, int) (backend.DestinationActivation, error)
	rollbackCalls            int
	rollbackRequest          backend.DestinationRollbackRequest
	rollbackRequests         []backend.DestinationRollbackRequest
	rollbackResponse         func(backend.DestinationRollbackRequest, int) error
	releaseCalls             int
	releaseRequests          []backend.SnapshotReleaseRequest
	readRequests             []backend.ComponentReadRequest
	failReadAfter            uint64
	failReadOnce             bool
	snapshotResponseLossOnce bool
	afterReadStart           func(backend.ComponentReadRequest) error
	componentIDForDisk       func(migration.OpaqueID) migration.OpaqueID
}

func newManagerMigrationProviderFixture() *managerMigrationProviderFixture {
	return &managerMigrationProviderFixture{
		capability: backend.MigrationCapabilities{
			Provider: "lima", ProviderVersion: "2.2.0",
			Revision:            migration.Digest("sha256:" + strings.Repeat("a", 64)),
			DiskRepresentations: []string{"raw"},
			ArchitecturePairs: []backend.MigrationArchitecturePair{{
				Host: "darwin/arm64", Guest: "linux/arm64",
			}},
			FullExport: true, FullImport: true, RootDiskKinds: []string{"lima-root"},
			AttachedDiskKinds: []string{"lima-additional"}, SparseExtents: true,
			Limits: migration.DefaultLimits(),
			AdoptionHelper: &backend.MigrationHelperCapability{
				PackageID: migration.AdoptionHelperPackage, Version: "1.0.0",
				GuestArchitecture: "linux/arm64",
				Digest:            migration.Digest("sha256:" + strings.Repeat("f", 64)),
			},
		},
		snapshots: make(map[string]backend.SourceSnapshot),
	}
}

func (provider *managerMigrationProviderFixture) MigrationCapabilities(
	context.Context,
) (backend.MigrationCapabilities, error) {
	return provider.capability, nil
}

func (provider *managerMigrationProviderFixture) InspectMigrationSource(
	_ context.Context,
	request backend.SourceInspectionRequest,
) (backend.SourceInventory, error) {
	if err := request.Validate(); err != nil {
		return backend.SourceInventory{}, err
	}
	instances := make([]backend.MigrationSourceInstance, 0, len(request.Selections))
	disks := make([]backend.MigrationSourceDisk, 0, len(request.Selections)+1)
	edges := make([]backend.MigrationSourceAttachment, 0, len(request.Selections)+1)
	for index, selection := range request.Selections {
		rootRef := migration.OpaqueID(fmt.Sprintf("disk_root%08d", index+1))
		instances = append(instances, backend.MigrationSourceInstance{
			EnvironmentRef:        selection.EnvironmentRef,
			ProviderRef:           migration.OpaqueID(fmt.Sprintf("provider_instance%08d", index+1)),
			Lifecycle:             backend.MigrationLifecycleStopped,
			ConfigurationRevision: 1,
			ConfigurationDigest:   migration.Digest("sha256:" + strings.Repeat("b", 64)),
			RootDiskRef:           rootRef,
		})
		disks = append(disks, backend.MigrationSourceDisk{
			DiskRef:     rootRef,
			ProviderRef: migration.OpaqueID(fmt.Sprintf("provider_rootdisk%08d", index+1)),
			Role:        migration.DiskRoleRoot, Format: "raw",
			LogicalBytes: 8192, AllocatedBytesHint: 4096,
			Consumers: []migration.OpaqueID{selection.EnvironmentRef},
		})
		edges = append(edges, backend.MigrationSourceAttachment{
			EnvironmentRef: selection.EnvironmentRef, DiskRef: rootRef,
			Attachment: migration.DiskRoleRoot, GuestPath: "/",
		})
	}
	if len(request.Selections) == 1 {
		selection := request.Selections[0]
		disks = append(disks, backend.MigrationSourceDisk{
			DiskRef: "disk_attached0001", ProviderRef: "provider_attached0001",
			Role: migration.DiskRoleAttached, Format: "raw",
			LogicalBytes: 4096, AllocatedBytesHint: 2048,
			Consumers: []migration.OpaqueID{selection.EnvironmentRef},
		})
		edges = append(edges, backend.MigrationSourceAttachment{
			EnvironmentRef: selection.EnvironmentRef, DiskRef: "disk_attached0001",
			Attachment: migration.DiskRoleAttached, GuestPath: "/mnt/data",
		})
	}
	slices.SortFunc(disks, func(left, right backend.MigrationSourceDisk) int {
		return strings.Compare(string(left.DiskRef), string(right.DiskRef))
	})
	slices.SortFunc(edges, func(left, right backend.MigrationSourceAttachment) int {
		leftKey := string(left.EnvironmentRef) + "\x00" + string(left.DiskRef)
		rightKey := string(right.EnvironmentRef) + "\x00" + string(right.DiskRef)
		return strings.Compare(leftKey, rightKey)
	})
	inventory := backend.SourceInventory{
		Binding: request.Binding, Provider: "lima", Instances: instances,
		Disks: disks, Attachments: edges,
		ExcludedClasses: []string{"activity-history", "host-workspace-content", "runtime-state"},
		SelectionClosed: true, Capturable: true, Blockers: []backend.MigrationProviderBlocker{},
	}
	digest, err := backend.SourceInventoryDigest(inventory)
	if err != nil {
		return backend.SourceInventory{}, err
	}
	inventory.InventoryDigest = digest
	if err := inventory.Validate(); err != nil {
		return backend.SourceInventory{}, err
	}
	return inventory, nil
}

func (provider *managerMigrationProviderFixture) SnapshotMigrationSource(
	_ context.Context,
	request backend.SourceSnapshotRequest,
) (backend.SourceSnapshot, error) {
	provider.snapshotCalls++
	if err := request.Validate(); err != nil {
		return backend.SourceSnapshot{}, err
	}
	key := string(request.Binding.OperationID) + "\x00" + string(request.Binding.EffectID)
	if snapshot, exists := provider.snapshots[key]; exists {
		return snapshot, nil
	}
	provider.snapshotCreates++
	hash := sha256.Sum256([]byte(key))
	handle := migration.OpaqueID("snapshot_" + hex.EncodeToString(hash[:8]))
	components := make([]backend.MigrationComponent, len(request.DiskRefs))
	for index, diskRef := range request.DiskRefs {
		componentID := migration.OpaqueID("component_" + strings.TrimPrefix(string(diskRef), "disk_"))
		if provider.componentIDForDisk != nil {
			componentID = provider.componentIDForDisk(diskRef)
		}
		components[index] = backend.MigrationComponent{
			ComponentID:    componentID,
			SnapshotHandle: handle, DiskRef: diskRef, Kind: "disk",
			LogicalBytes: map[bool]uint64{true: 4096, false: 8192}[strings.Contains(string(diskRef), "attached")],
		}
	}
	slices.SortFunc(components, func(left, right backend.MigrationComponent) int {
		return strings.Compare(string(left.ComponentID), string(right.ComponentID))
	})
	rootComponents := make(map[migration.OpaqueID]migration.OpaqueID, len(request.Selections))
	for _, component := range components {
		for index, selection := range request.Selections {
			rootRef := migration.OpaqueID(fmt.Sprintf("disk_root%08d", index+1))
			if component.DiskRef == rootRef {
				rootComponents[selection.EnvironmentRef] = component.ComponentID
			}
		}
	}
	identities := make([]backend.MigrationSourceIdentity, len(request.Selections))
	for index, selection := range request.Selections {
		identities[index] = backend.MigrationSourceIdentity{
			EnvironmentRef: selection.EnvironmentRef,
			RootComponent:  rootComponents[selection.EnvironmentRef],
			Evidence: migration.GuestIdentityEvidence{
				MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("6", 64)),
				SSHHostKeyDigests: []migration.Digest{
					migration.Digest("sha256:" + strings.Repeat("7", 64)),
				},
			},
		}
	}
	snapshot := backend.SourceSnapshot{
		Binding: request.Binding, SnapshotHandle: handle, Components: components,
		Identities: identities, Independent: true,
	}
	if err := snapshot.Validate(); err != nil {
		return backend.SourceSnapshot{}, err
	}
	provider.snapshots[key] = snapshot
	if provider.snapshotResponseLossOnce {
		provider.snapshotResponseLossOnce = false
		return backend.SourceSnapshot{}, errManagerSnapshotResponseLost
	}
	return snapshot, nil
}

func (provider *managerMigrationProviderFixture) ReadMigrationComponent(
	ctx context.Context,
	request backend.ComponentReadRequest,
	emit func(backend.MigrationExtent) error,
) error {
	provider.readRequests = append(provider.readRequests, request)
	if ctx == nil || request.Validate() != nil || emit == nil {
		return backend.ErrMigrationProviderRequest
	}
	var component backend.MigrationComponent
	found := false
	for _, snapshot := range provider.snapshots {
		if snapshot.Binding != request.Binding || snapshot.SnapshotHandle != request.SnapshotHandle {
			continue
		}
		for _, candidate := range snapshot.Components {
			if candidate.ComponentID == request.ComponentID {
				component = candidate
				found = true
				break
			}
		}
	}
	if !found || request.ResumeOffset > component.LogicalBytes {
		return backend.ErrMigrationProviderResponse
	}
	if provider.afterReadStart != nil {
		afterReadStart := provider.afterReadStart
		provider.afterReadStart = nil
		if err := afterReadStart(request); err != nil {
			return err
		}
	}
	seed := sha256.Sum256([]byte(component.ComponentID))
	for offset := request.ResumeOffset; offset < component.LogicalBytes; {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := component.LogicalBytes - offset
		failAfterEmit := false
		if provider.failReadOnce && provider.failReadAfter > offset &&
			provider.failReadAfter < offset+length {
			length = provider.failReadAfter - offset
			failAfterEmit = true
		}
		if length > uint64(request.MaxChunkBytes) {
			length = uint64(request.MaxChunkBytes)
		}
		data := make([]byte, int(length))
		for index := range data {
			data[index] = seed[(int(offset)+index)%len(seed)]
		}
		if err := emit(backend.MigrationExtent{
			Kind: migration.ExtentData, LogicalOffset: offset,
			Length: length, Data: data,
		}); err != nil {
			return err
		}
		offset += length
		if failAfterEmit {
			provider.failReadOnce = false
			return errors.New("injected migration component read interruption")
		}
	}
	return nil
}

func (provider *managerMigrationProviderFixture) ReleaseMigrationSnapshot(
	_ context.Context,
	request backend.SnapshotReleaseRequest,
) error {
	provider.releaseCalls++
	provider.releaseRequests = append(provider.releaseRequests, request)
	if err := request.Validate(); err != nil {
		return err
	}
	return nil
}

func (provider *managerMigrationProviderFixture) InspectMigrationDestination(
	_ context.Context,
	request backend.DestinationInspectionRequest,
) (backend.DestinationInventory, error) {
	provider.destinationInspectCalls++
	if err := request.Validate(); err != nil {
		return backend.DestinationInventory{}, err
	}
	return backend.DestinationInventory{
		Binding: request.Binding, Compatible: true,
		CapabilityRevision: provider.capability.Revision,
		AvailableBytes:     1 << 30, SparseExtents: true,
		Conflicts: []migration.OpaqueID{}, Blockers: []backend.MigrationProviderBlocker{},
	}, nil
}

func (provider *managerMigrationProviderFixture) StageMigrationDestination(
	ctx context.Context,
	request backend.DestinationStageRequest,
) (backend.DestinationStage, error) {
	provider.stageCalls++
	if provider.stageDelegate != nil {
		return provider.stageDelegate.StageMigrationDestination(ctx, request)
	}
	return backend.DestinationStage{}, errors.New("stage must be worker-owned")
}

func (provider *managerMigrationProviderFixture) AdoptMigrationDestination(
	_ context.Context,
	request backend.DestinationAdoptionRequest,
) (backend.DestinationAdoption, error) {
	provider.adoptCalls++
	provider.adoptRequests = append(provider.adoptRequests, request)
	if provider.adoptResponse != nil {
		return provider.adoptResponse(request, provider.adoptCalls)
	}
	return backend.DestinationAdoption{}, errors.New("adopt must be worker-owned")
}

func (provider *managerMigrationProviderFixture) VerifyMigrationDestination(
	_ context.Context,
	request backend.DestinationVerifyRequest,
) (backend.DestinationProof, error) {
	provider.verifyCalls++
	provider.verifyRequests = append(provider.verifyRequests, request)
	if provider.verifyResponse != nil {
		return provider.verifyResponse(request, provider.verifyCalls)
	}
	return backend.DestinationProof{}, errors.New("verify must be worker-owned")
}

func (provider *managerMigrationProviderFixture) ActivateMigrationDestination(
	_ context.Context,
	request backend.DestinationActivationRequest,
) (backend.DestinationActivation, error) {
	provider.activateCalls++
	provider.activateRequests = append(provider.activateRequests, request)
	if provider.activateResponse != nil {
		return provider.activateResponse(request, provider.activateCalls)
	}
	return backend.DestinationActivation{
		Binding: request.Binding, StageHandle: request.Proof.StageHandle,
		ProofDigest:   request.Proof.ProofDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
		Stopped:       true, Promoted: true,
	}, nil
}

func (provider *managerMigrationProviderFixture) RollbackMigrationDestination(
	_ context.Context,
	request backend.DestinationRollbackRequest,
) error {
	provider.rollbackCalls++
	provider.rollbackRequest = request
	provider.rollbackRequests = append(provider.rollbackRequests, request)
	if provider.rollbackResponse != nil {
		return provider.rollbackResponse(request, provider.rollbackCalls)
	}
	return nil
}

type managerBundleSourceFixture struct {
	secretInputs *MigrationSecretInputStore
	path         string
	inspection   MigrationBundleInspection
	calls        int
}

func (source *managerBundleSourceFixture) InspectMigrationBundle(
	_ context.Context,
	request MigrationBundleInspectRequest,
) (MigrationBundleInspection, error) {
	if source == nil {
		return MigrationBundleInspection{}, ErrMigrationPlanStale
	}
	source.calls++
	if source.secretInputs == nil || request.BundlePath != source.path ||
		request.ExpectedBinding != source.inspection.Binding {
		return MigrationBundleInspection{}, ErrMigrationPlanStale
	}
	fileBinding := source.inspection.BundleFile
	secret, err := source.secretInputs.Lookup(MigrationSecretInputLookup{
		Handle: request.SecretInputHandle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: request.ClientBinding, BundleFile: &fileBinding,
	})
	if err != nil {
		return MigrationBundleInspection{}, err
	}
	if secret.BundleID != source.inspection.Binding.BundleID {
		return MigrationBundleInspection{}, ErrMigrationSecretInputMismatch
	}
	return source.inspection, nil
}

func createManagerImportSecretHandle(
	t *testing.T,
	store *MigrationSecretInputStore,
	bundleID migration.BundleID,
	fileBinding MigrationBundleFileBinding,
	client string,
) MigrationSecretInputHandle {
	t.Helper()
	handle, err := store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: client,
		BundleID: bundleID, BundleFile: &fileBinding,
		Passphrase: []byte("destination import passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func managerMigrationManifestFixture(bundleID migration.BundleID) migration.Manifest {
	rootDigest := migration.Digest("sha256:" + strings.Repeat("4", 64))
	attachedDigest := migration.Digest("sha256:" + strings.Repeat("5", 64))
	profileState, profileStateDigest := managerMigrationProfileStateFixture()
	return migration.Manifest{
		Schema: "hideout.migration-manifest/v1", BundleID: bundleID,
		FormatVersion: migration.BundleFormatVersion,
		SourceProduct: migration.SourceProduct{
			Version: "0.1.0", HostOS: "darwin", HostArch: "arm64",
			Backend: "lima", BackendVersion: "2.2.0", GuestArch: "aarch64",
		},
		Environments: []migration.EnvironmentSnapshot{{
			SourceEnvironmentRef: "environment_source1", DisplayNameHint: "dev",
			Runtime: "linux", GuestUser: "developer", Backend: "lima", Mode: migration.ExportModeFull,
			ProfileComponentID:      "component_profile1",
			ProfileStateComponentID: "component_state001",
			WorkspaceProposals:      []migration.WorkspaceProposal{},
			AuthorityProposalRefs:   []migration.OpaqueID{},
			GuestIdentityEvidence: migration.GuestIdentityEvidence{
				MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("6", 64)),
				SSHHostKeyDigests: []migration.Digest{
					migration.Digest("sha256:" + strings.Repeat("7", 64)),
				},
			},
			DiskRefs: []migration.OpaqueID{"disk_attached1", "disk_root0001"},
		}},
		DiskObjects: []migration.DiskObject{
			{
				DiskID: "disk_attached1", Role: migration.DiskRoleAttached, Format: "raw",
				LogicalBytes: 4096, AllocatedBytesHint: 2048, ContentDigest: attachedDigest,
				Provider: migration.ProviderDiskFacts{Name: "source-attached", Kind: "lima-additional", Features: []string{}},
			},
			{
				DiskID: "disk_root0001", Role: migration.DiskRoleRoot, Format: "raw",
				LogicalBytes: 8192, AllocatedBytesHint: 4096, ContentDigest: rootDigest,
				Provider: migration.ProviderDiskFacts{Name: "source-root", Kind: "lima-root", Features: []string{}},
			},
		},
		DiskEdges: []migration.DiskEdge{
			{
				EnvironmentRef: "environment_source1", DiskID: "disk_attached1",
				Attachment: migration.DiskRoleAttached, GuestPath: "/mnt/data",
			},
			{
				EnvironmentRef: "environment_source1", DiskID: "disk_root0001",
				Attachment: migration.DiskRoleRoot, GuestPath: "/",
			},
		},
		SecretEntries:      []migration.SecretEntry{},
		AuthorityProposals: []migration.AuthorityProposal{},
		ComponentIndex: []migration.ComponentIndexEntry{
			{
				ComponentID: "component_attached1", Kind: "disk", DiskID: "disk_attached1",
				LogicalBytes: 4096, FirstRecord: 0, LastRecord: 0, RecordCount: 1,
				ContentDigest: attachedDigest,
			},
			{
				ComponentID: "component_profile1", Kind: "profile", LogicalBytes: 128,
				FirstRecord: 1, LastRecord: 1, RecordCount: 1,
				ContentDigest: migration.Digest("sha256:" + strings.Repeat("8", 64)),
			},
			{
				ComponentID: "component_state001", Kind: "profile-state",
				LogicalBytes: uint64(len(profileState)), FirstRecord: 2, LastRecord: 3,
				RecordCount: 2, ContentDigest: profileStateDigest,
			},
			{
				ComponentID: "component_root0001", Kind: "disk", DiskID: "disk_root0001",
				LogicalBytes: 8192, FirstRecord: 4, LastRecord: 4, RecordCount: 1,
				ContentDigest: rootDigest,
			},
		},
		ExcludedClasses:      []string{"activity-history", "host-workspace-content", "runtime-state"},
		RequiredCapabilities: []migration.RequiredCapability{},
	}
}

func managerMigrationProfileStateFixture() ([]byte, migration.Digest) {
	content := []byte("hideout-profile-state-fixture-v1\n")
	digest := sha256.Sum256(content)
	return content, migration.Digest("sha256:" + hex.EncodeToString(digest[:]))
}

var _ backend.MigrationExportProvider = (*managerMigrationProviderFixture)(nil)
var _ backend.MigrationImportProvider = (*managerMigrationProviderFixture)(nil)
var _ MigrationBundleSource = (*managerBundleSourceFixture)(nil)
