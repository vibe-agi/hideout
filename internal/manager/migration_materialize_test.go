package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMigrationImportOperationMaterializesThenReplaysWithoutAnotherSecret(t *testing.T) {
	fixture := writeManagerMaterializationBundle(t)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
	defer cache.Close()
	inspection := inspectManagerMaterializationBundle(t, fixture, secretInputs, cache)
	stager := &managerMaterializationStager{}
	provider := newManagerMigrationProviderFixture()
	provider.stageDelegate = stager
	root := t.TempDir()
	service := MigrationImportService{
		MigrationService: MigrationService{
			Store: MigrationStore{Root: root}, Environments: environment.Store{Root: root},
			Import: provider, SecretInputs: secretInputs, NewID: sequentialMigrationIDSource(),
		},
		BundleSource:    CachedMigrationBundleSource{SecretInputs: secretInputs, Cache: cache},
		InspectionCache: cache,
	}
	importHandle, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "operation-client",
		BundleID: inspection.Binding.BundleID, BundleFile: &inspection.BundleFile,
		Passphrase: []byte(managerMaterializationPassphrase),
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: migration.ImportDraft{
			Schema: MigrationImportDraftSchema, BundlePath: fixture.path,
			BundleBinding:           inspection.Binding,
			SelectedEnvironmentRefs: []migration.OpaqueID{"environment_source1"},
			NameMappings: []migration.NameMapping{{
				SourceRef: "environment_source1", DestinationName: "materialized-clone",
			}},
			WorkspaceMappings: []migration.WorkspaceMapping{},
			SecretMappings:    []migration.SecretMapping{},
			IdentityPolicies: []migration.IdentitySelection{{
				SourceRef: "environment_source1", Policy: migration.GuestIdentitySafeClone,
			}},
			AuthorityDecisions:   []migration.AuthorityDecision{},
			RiskAcknowledgements: []string{},
		},
		SecretInputHandle: importHandle.Handle, ClientBinding: "operation-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := service.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: plan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: plan.PlanDigest},
		SecretInputHandle: importHandle.Handle, ClientBinding: "operation-client",
		IdempotencyKey: "materialize-request-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, stage, err := service.MaterializeImportDestination(
		context.Background(), MigrationImportMaterializeRequest{
			OperationID: apply.OperationID, SecretInputHandle: importHandle.Handle,
			ClientBinding: "operation-client",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != MigrationPhasePreparingSecrets ||
		operation.Effects[0].Status != MigrationEffectSucceeded ||
		operation.Progress.CompletedLogicalBytes != operation.Progress.TotalLogicalBytes ||
		operation.Progress.ComponentsComplete != operation.Progress.ComponentsTotal ||
		stage.StageHandle != migrationImportStageHandle(operation.ID) ||
		operation.DestinationStage == nil ||
		operation.DestinationStage.StageHandle != stage.StageHandle ||
		len(operation.DestinationStage.ObjectHandles) != len(stage.ObjectHandles) ||
		len(operation.DestinationStage.Checkpoints) != len(stage.Checkpoints) ||
		len(operation.DestinationStage.Profiles) != 1 ||
		operation.DestinationStage.Profiles[0].ComponentID !=
			fixture.manifest.Environments[0].ProfileComponentID ||
		!reflect.DeepEqual(operation.DestinationStage.Profiles[0].Snapshot, fixture.profile) ||
		len(operation.ImportObjects) != 1 ||
		operation.ImportObjects[0].DestinationName != "materialized-clone" {
		t.Fatalf("materialized operation=%+v stage=%+v", operation, stage)
	}
	if provider.stageCalls != 1 || stager.calls != 1 {
		t.Fatalf("provider stage calls=%d delegated=%d", provider.stageCalls, stager.calls)
	}
	replayed, replayStage, err := service.MaterializeImportDestination(
		context.Background(), MigrationImportMaterializeRequest{OperationID: operation.ID},
	)
	if err != nil || replayed.Revision != operation.Revision || replayStage.StageHandle != "" {
		t.Fatalf("completed stage replay operation=%+v stage=%+v err=%v", replayed, replayStage, err)
	}
	if provider.stageCalls != 1 || stager.calls != 1 {
		t.Fatal("completed stage replay re-entered provider or requested another secret")
	}
	if replayed.DestinationStage == nil ||
		!reflect.DeepEqual(replayed.DestinationStage.Profiles, operation.DestinationStage.Profiles) {
		t.Fatalf("replay lost authenticated profile staging: %+v", replayed.DestinationStage)
	}
	rolledBack, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: operation.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Phase != MigrationPhaseRolledBack || rolledBack.Decision == nil ||
		rolledBack.Decision.Value != MigrationDecisionRollback ||
		rolledBack.Effects[0].Status != MigrationEffectCompensated ||
		provider.rollbackCalls != 1 ||
		provider.rollbackRequest.StageHandle != stage.StageHandle ||
		!slices.Equal(provider.rollbackRequest.ObjectHandles, stage.ObjectHandles) {
		t.Fatalf("rolled back operation=%+v request=%+v", rolledBack, provider.rollbackRequest)
	}
	replayedRollback, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: operation.ID},
	)
	if err != nil || replayedRollback.Revision != rolledBack.Revision || provider.rollbackCalls != 1 {
		t.Fatalf("rollback replay operation=%+v calls=%d err=%v",
			replayedRollback, provider.rollbackCalls, err)
	}
}

func TestMigrationImportMaterializationResumesProviderCheckpointWithoutRecountingCapacity(t *testing.T) {
	fixture := writeManagerMaterializationBundle(t)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
	defer cache.Close()
	inspection := inspectManagerMaterializationBundle(t, fixture, secretInputs, cache)
	stager := &managerResumableMaterializationStager{}
	provider := newManagerMigrationProviderFixture()
	provider.stageDelegate = stager
	root := t.TempDir()
	service := MigrationImportService{
		MigrationService: MigrationService{
			Store: MigrationStore{Root: root}, Environments: environment.Store{Root: root},
			Import: provider, SecretInputs: secretInputs, NewID: sequentialMigrationIDSource(),
		},
		BundleSource:    CachedMigrationBundleSource{SecretInputs: secretInputs, Cache: cache},
		InspectionCache: cache,
	}
	newHandle := func() string {
		t.Helper()
		handle, err := secretInputs.Create(MigrationSecretInputRequest{
			Purpose: MigrationSecretPurposeImport, ClientBinding: "resume-client",
			BundleID: inspection.Binding.BundleID, BundleFile: &inspection.BundleFile,
			Passphrase: []byte(managerMaterializationPassphrase),
		})
		if err != nil {
			t.Fatal(err)
		}
		return handle.Handle
	}
	firstHandle := newHandle()
	plan, err := service.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: migration.ImportDraft{
			Schema: MigrationImportDraftSchema, BundlePath: fixture.path,
			BundleBinding:           inspection.Binding,
			SelectedEnvironmentRefs: []migration.OpaqueID{"environment_source1"},
			NameMappings: []migration.NameMapping{{
				SourceRef: "environment_source1", DestinationName: "resumed-clone",
			}},
			WorkspaceMappings: []migration.WorkspaceMapping{},
			SecretMappings:    []migration.SecretMapping{},
			IdentityPolicies: []migration.IdentitySelection{{
				SourceRef: "environment_source1", Policy: migration.GuestIdentitySafeClone,
			}},
			AuthorityDecisions: []migration.AuthorityDecision{},
		},
		SecretInputHandle: firstHandle, ClientBinding: "resume-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := service.ApplyImport(context.Background(), MigrationImportApplyRequest{
		Schema: MigrationImportApplySchema, Plan: plan,
		Confirmation:      MigrationPlanConfirmation{PlanDigest: plan.PlanDigest},
		SecretInputHandle: firstHandle, ClientBinding: "resume-client",
		IdempotencyKey: "materialize-resume-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.MaterializeImportDestination(
		context.Background(), MigrationImportMaterializeRequest{
			OperationID: apply.OperationID, SecretInputHandle: firstHandle,
			ClientBinding: "resume-client",
		},
	)
	if !errors.Is(err, errManagerMaterializationInterrupted) {
		t.Fatalf("first materialization error=%v", err)
	}
	failed, loadErr := service.Store.Load(apply.OperationID)
	if loadErr != nil || failed.Phase != MigrationPhaseRecoverableFailure ||
		failed.Effects[0].Status != MigrationEffectRunning || stager.resumeOffset != 1024 {
		t.Fatalf("partial materialization operation=%+v stager=%+v err=%v",
			failed, stager, loadErr)
	}
	inspectionsBeforeResume := provider.destinationInspectCalls

	// Reconstruct the service value to exercise the same durable store/provider
	// path used after a daemon worker restart. The replacement secret handle is
	// intentional: passphrase handles are one-shot and never survive a failure.
	restarted := MigrationImportService{
		MigrationService: service.MigrationService,
		BundleSource:     service.BundleSource,
		InspectionCache:  cache,
	}
	resumed, _, err := restarted.MaterializeImportDestination(
		context.Background(), MigrationImportMaterializeRequest{
			OperationID: apply.OperationID, SecretInputHandle: newHandle(),
			ClientBinding: "resume-client",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Phase != MigrationPhasePreparingSecrets || stager.calls != 2 ||
		provider.destinationInspectCalls != inspectionsBeforeResume {
		t.Fatalf("resumed operation=%+v stager=%+v inspections=%d before=%d",
			resumed, stager, provider.destinationInspectCalls, inspectionsBeforeResume)
	}
	if len(stager.resumedOffsets) == 0 {
		t.Fatal("resumed provider received no remaining extents")
	}
	for _, offset := range stager.resumedOffsets {
		if offset < stager.resumeOffset {
			t.Fatalf("resumed provider reread verified prefix at offset %d", offset)
		}
	}
}

func TestMigrationImportRollbackUsesFrozenHandlesAfterPartialStageFailure(t *testing.T) {
	root := t.TempDir()
	store := MigrationStore{Root: root}
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AcquireClaims(operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(operation.ID, MigrationPhaseMaterializing, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginEffect(
		operation.ID, "effect_stage1234", "backend.lima",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(
		operation.ID, MigrationPhaseRecoverableFailure, nil,
	); err != nil {
		t.Fatal(err)
	}
	provider := newManagerMigrationProviderFixture()
	service := MigrationImportService{MigrationService: MigrationService{
		Store: store, Environments: environment.Store{Root: root}, Import: provider,
	}}
	rolledBack, err := service.RollbackImportDestination(
		context.Background(), MigrationImportRollbackRequest{OperationID: operation.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedHandles := []migration.OpaqueID{"backend_clone1234", "backend_clone5678"}
	if rolledBack.Phase != MigrationPhaseRolledBack || rolledBack.DestinationStage != nil ||
		rolledBack.Effects[0].Status != MigrationEffectCompensated ||
		provider.rollbackCalls != 1 ||
		provider.rollbackRequest.StageHandle != migrationImportStageHandle(operation.ID) ||
		!slices.Equal(provider.rollbackRequest.ObjectHandles, expectedHandles) {
		t.Fatalf("partial rollback operation=%+v request=%+v", rolledBack, provider.rollbackRequest)
	}
}

func TestMigrationMaterializationStreamsOneImmutableBundleToIndependentDestinations(t *testing.T) {
	fixture := writeManagerMaterializationBundle(t)
	before, err := os.ReadFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
	defer cache.Close()
	inspection := inspectManagerMaterializationBundle(t, fixture, secretInputs, cache)
	stager := &managerMaterializationStager{}
	service := MigrationMaterializationService{
		SecretInputs: secretInputs, Cache: cache, Destination: stager,
	}

	for index := 1; index <= 2; index++ {
		client := fmt.Sprintf("destination-client-%d", index)
		handle, err := secretInputs.Create(MigrationSecretInputRequest{
			Purpose: MigrationSecretPurposeImport, ClientBinding: client,
			BundleID: inspection.Binding.BundleID, BundleFile: &inspection.BundleFile,
			Passphrase: []byte(managerMaterializationPassphrase),
		})
		if err != nil {
			t.Fatal(err)
		}
		destination := managerMaterializationDestinationRequest(
			fixture.manifest, index,
		)
		materialized, err := service.StageDestinationWithProfiles(
			context.Background(), MigrationDestinationMaterializeRequest{
				BundlePath: fixture.path, ExpectedFile: inspection.BundleFile,
				ExpectedBinding: inspection.Binding, SecretInputHandle: handle.Handle,
				ClientBinding: client, Destination: destination,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		stage := materialized.Stage
		if stage.StageHandle != destination.StagingHandle || stage.Binding != destination.Binding ||
			len(stage.Checkpoints) != 1 ||
			stage.Checkpoints[0].ContentDigest != fixture.diskDigest {
			t.Fatalf("destination %d stage=%+v", index, stage)
		}
		if len(materialized.Profiles) != 1 ||
			materialized.Profiles[0].ComponentID != fixture.manifest.Environments[0].ProfileComponentID ||
			!reflect.DeepEqual(materialized.Profiles[0].Snapshot, fixture.profile) {
			t.Fatalf("destination %d profiles=%+v", index, materialized.Profiles)
		}
		if _, err := secretInputs.Lookup(MigrationSecretInputLookup{
			Handle: handle.Handle, Purpose: MigrationSecretPurposeImport,
			ClientBinding: client, BundleFile: &inspection.BundleFile,
		}); !errors.Is(err, ErrMigrationSecretInputRequired) {
			t.Fatalf("destination %d import handle was not consumed: %v", index, err)
		}
	}

	if stager.calls != 2 || len(stager.digests) != 2 ||
		stager.digests[0] != fixture.diskDigest || stager.digests[1] != fixture.diskDigest {
		t.Fatalf("stager calls=%d digests=%v", stager.calls, stager.digests)
	}
	if stager.operationIDs[0] == stager.operationIDs[1] ||
		stager.backendIdentities[0] == stager.backendIdentities[1] {
		t.Fatalf("destination-local identities were reused: operations=%v backends=%v",
			stager.operationIDs, stager.backendIdentities)
	}
	for _, borrowed := range stager.borrowedData {
		if !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
			t.Fatal("provider callback data remained live after callback return")
		}
	}
	after, err := os.ReadFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("destination materialization mutated the reusable sealed bundle")
	}
}

func TestMigrationMaterializationFailsBeforeProviderForWrongKeyAndManifestSubstitution(t *testing.T) {
	fixture := writeManagerMaterializationBundle(t)
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
	defer cache.Close()
	inspection := inspectManagerMaterializationBundle(t, fixture, secretInputs, cache)
	stager := &managerMaterializationStager{}
	service := MigrationMaterializationService{
		SecretInputs: secretInputs, Cache: cache, Destination: stager,
	}

	wrong, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "wrong-key-client",
		BundleID: inspection.Binding.BundleID, BundleFile: &inspection.BundleFile,
		Passphrase: []byte("wrong materialization passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StageDestination(
		context.Background(), MigrationDestinationMaterializeRequest{
			BundlePath: fixture.path, ExpectedFile: inspection.BundleFile,
			ExpectedBinding: inspection.Binding, SecretInputHandle: wrong.Handle,
			ClientBinding: "wrong-key-client",
			Destination:   managerMaterializationDestinationRequest(fixture.manifest, 1),
		},
	); !errors.Is(err, migration.ErrAuthenticationFailed) {
		t.Fatalf("wrong-key materialization error=%v", err)
	}
	if stager.calls != 0 {
		t.Fatal("provider ran before the import passphrase authenticated")
	}

	substituted, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "substitution-client",
		BundleID: inspection.Binding.BundleID, BundleFile: &inspection.BundleFile,
		Passphrase: []byte(managerMaterializationPassphrase),
	})
	if err != nil {
		t.Fatal(err)
	}
	destination := managerMaterializationDestinationRequest(fixture.manifest, 1)
	destination.Objects[0].GuestUser = "substituted"
	if _, err := service.StageDestination(
		context.Background(), MigrationDestinationMaterializeRequest{
			BundlePath: fixture.path, ExpectedFile: inspection.BundleFile,
			ExpectedBinding: inspection.Binding, SecretInputHandle: substituted.Handle,
			ClientBinding: "substitution-client", Destination: destination,
		},
	); !errors.Is(err, ErrMigrationPlanInvalid) {
		t.Fatalf("guest-user substitution error=%v", err)
	}
	if stager.calls != 0 {
		t.Fatal("provider ran for a destination config not bound by the manifest")
	}
	if _, err := secretInputs.Lookup(MigrationSecretInputLookup{
		Handle: substituted.Handle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: "substitution-client", BundleFile: &inspection.BundleFile,
	}); err != nil {
		t.Fatalf("pre-consume plan rejection destroyed retry handle: %v", err)
	}
}

func TestMigrationMaterializationRejectsProviderCursorAndReceiptSubstitution(t *testing.T) {
	fixture := writeManagerMaterializationBundle(t)
	for name, mutate := range map[string]func(*managerMaterializationStager){
		"unaligned resume cursor": func(stager *managerMaterializationStager) {
			stager.resumeOffset = 1
		},
		"substituted checkpoint": func(stager *managerMaterializationStager) {
			stager.substituteCheckpoint = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
			defer secretInputs.Close()
			cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
			defer cache.Close()
			inspection := inspectManagerMaterializationBundle(t, fixture, secretInputs, cache)
			handle, err := secretInputs.Create(MigrationSecretInputRequest{
				Purpose: MigrationSecretPurposeImport, ClientBinding: "cursor-client",
				BundleID: inspection.Binding.BundleID, BundleFile: &inspection.BundleFile,
				Passphrase: []byte(managerMaterializationPassphrase),
			})
			if err != nil {
				t.Fatal(err)
			}
			stager := &managerMaterializationStager{}
			mutate(stager)
			_, err = (MigrationMaterializationService{
				SecretInputs: secretInputs, Cache: cache, Destination: stager,
			}).StageDestination(context.Background(), MigrationDestinationMaterializeRequest{
				BundlePath: fixture.path, ExpectedFile: inspection.BundleFile,
				ExpectedBinding: inspection.Binding, SecretInputHandle: handle.Handle,
				ClientBinding: "cursor-client",
				Destination:   managerMaterializationDestinationRequest(fixture.manifest, 1),
			})
			if err == nil {
				t.Fatal("provider substitution unexpectedly succeeded")
			}
		})
	}
}

type managerMaterializationFixture struct {
	path       string
	manifest   migration.Manifest
	diskDigest migration.Digest
	profile    migration.PortableProfile
}

const managerMaterializationPassphrase = "manager materialization passphrase"

func writeManagerMaterializationBundle(t *testing.T) managerMaterializationFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "materialize.hideout-migration")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := migration.NewWriter(file, migration.WriterOptions{
		BundleID: "migb_materialize001", CreatedAt: "2026-08-02T01:00:00Z",
		KDF:    migration.KDFParameters{MemoryKiB: 8 << 10, Passes: 1, Lanes: 1},
		Limits: migration.DefaultLimits(), Random: bytes.NewReader(managerInspectionRandom(4096)),
		Passphrase: []byte(managerMaterializationPassphrase),
	})
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	portableProfile, err := migration.NormalizePortableProfile(profile.Default("fixture-profile"))
	if err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatal(err)
	}
	profileBytes, err := migration.EncodePortableProfile(portableProfile)
	if err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte{0x31}, 1024)
	last := bytes.Repeat([]byte{0x72}, 1024)
	records := []migration.RecordInput{
		{Type: migration.RecordMetadata, ComponentID: "component_profile0001", Plaintext: profileBytes},
		{Type: migration.RecordRawChunk, ComponentID: "component_root00001", Ordinal: 0, LogicalOffset: 0, Plaintext: first},
		{Type: migration.RecordZeroExtent, ComponentID: "component_root00001", Ordinal: 1, LogicalOffset: 1024, ExtentLength: 512},
		{Type: migration.RecordHoleExtent, ComponentID: "component_root00001", Ordinal: 2, LogicalOffset: 1536, ExtentLength: 512},
		{Type: migration.RecordDataChunk, ComponentID: "component_root00001", Ordinal: 3, LogicalOffset: 2048, Plaintext: last},
	}
	for _, record := range records {
		if _, err := writer.Append(record); err != nil {
			_ = writer.Close()
			_ = file.Close()
			t.Fatal(err)
		}
	}
	diskDigest := managerMaterializationDiskDigest(t, first, last)
	manifest := migration.Manifest{
		Schema: "hideout.migration-manifest/v1", BundleID: "migb_materialize001",
		FormatVersion: migration.BundleFormatVersion,
		SourceProduct: migration.SourceProduct{
			Version: "0.1.0", HostOS: "darwin", HostArch: "arm64",
			Backend: "lima", BackendVersion: "2.2.0", GuestArch: "aarch64",
		},
		Environments: []migration.EnvironmentSnapshot{{
			SourceEnvironmentRef: "environment_source1", DisplayNameHint: "dev",
			Runtime: "linux", GuestUser: "developer", Backend: "lima",
			Mode: migration.ExportModeFull, ProfileComponentID: "component_profile0001",
			WorkspaceProposals:    []migration.WorkspaceProposal{},
			AuthorityProposalRefs: []migration.OpaqueID{},
			GuestIdentityEvidence: migration.GuestIdentityEvidence{
				MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("6", 64)),
				SSHHostKeyDigests: []migration.Digest{
					migration.Digest("sha256:" + strings.Repeat("7", 64)),
				},
			},
			DiskRefs: []migration.OpaqueID{"disk_root0001"},
		}},
		DiskObjects: []migration.DiskObject{{
			DiskID: "disk_root0001", Role: migration.DiskRoleRoot, Format: "raw",
			LogicalBytes: 3072, AllocatedBytesHint: 2048, ContentDigest: diskDigest,
			Provider: migration.ProviderDiskFacts{
				Name: "source-root", Kind: "lima-root", Features: []string{},
			},
		}},
		DiskEdges: []migration.DiskEdge{{
			EnvironmentRef: "environment_source1", DiskID: "disk_root0001",
			Attachment: migration.DiskRoleRoot, GuestPath: "/",
		}},
		SecretEntries: []migration.SecretEntry{}, AuthorityProposals: []migration.AuthorityProposal{},
		ComponentIndex: []migration.ComponentIndexEntry{
			{
				ComponentID: "component_profile0001", Kind: "profile",
				LogicalBytes: uint64(len(profileBytes)), FirstRecord: 0, LastRecord: 0,
				RecordCount: 1, ContentDigest: managerMaterializationBytesDigest(profileBytes),
			},
			{
				ComponentID: "component_root00001", Kind: "disk", DiskID: "disk_root0001",
				LogicalBytes: 3072, FirstRecord: 1, LastRecord: 4, RecordCount: 4,
				ContentDigest: diskDigest,
			},
		},
		ExcludedClasses:      []string{"activity-history", "host-workspace-content", "runtime-state"},
		RequiredCapabilities: []migration.RequiredCapability{},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Seal(manifestBytes); err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return managerMaterializationFixture{
		path: path, manifest: manifest, diskDigest: diskDigest, profile: portableProfile,
	}
}

func inspectManagerMaterializationBundle(
	t *testing.T,
	fixture managerMaterializationFixture,
	secretInputs *MigrationSecretInputStore,
	cache *MigrationInspectionCache,
) MigrationReadOnlyInspection {
	t.Helper()
	probe, err := ProbeMigrationBundleFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeInspect, ClientBinding: "materialization-inspector",
		BundleID: probe.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: []byte(managerMaterializationPassphrase),
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := (MigrationInspectionService{
		SecretInputs: secretInputs, Cache: cache,
	}).Inspect(context.Background(), MigrationReadOnlyInspectRequest{
		BundlePath: fixture.path, ExpectedFile: probe.BundleFile,
		SecretInputHandle: handle.Handle, ClientBinding: "materialization-inspector",
	})
	if err != nil {
		t.Fatal(err)
	}
	return inspection
}

func managerMaterializationDestinationRequest(
	manifest migration.Manifest,
	destination int,
) backend.DestinationStageRequest {
	operation := migration.OpaqueID(fmt.Sprintf("op_migration_destination%04d", destination))
	effect := migration.OpaqueID(fmt.Sprintf("migeffect_stage%04d", destination))
	backendIdentity := migration.OpaqueID(fmt.Sprintf("backend_destination%04d", destination))
	return backend.DestinationStageRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID: operation, EffectID: effect,
			CapabilityRevision: migration.Digest("sha256:" + strings.Repeat("a", 64)),
		},
		StagingHandle: migration.OpaqueID(fmt.Sprintf("stage_destination%04d", destination)),
		Objects: []backend.MigrationDestinationObject{{
			EnvironmentRef: "environment_source1", BackendIdentity: backendIdentity,
			Runtime: "linux", GuestArchitecture: "linux/arm64", GuestUser: "developer",
			ProfileComponent: "component_profile0001",
		}},
		Disks: append([]migration.DiskObject(nil), manifest.DiskObjects...),
		Edges: append([]migration.DiskEdge(nil), manifest.DiskEdges...),
		Components: []backend.MigrationDestinationComponent{{
			ComponentID: "component_root00001", DiskID: "disk_root0001",
			BackendIdentity: backendIdentity, Kind: "disk",
			LogicalBytes: 3072, ContentDigest: manifest.DiskObjects[0].ContentDigest,
		}},
	}
}

type managerMaterializationStager struct {
	calls                int
	resumeOffset         uint64
	substituteCheckpoint bool
	digests              []migration.Digest
	operationIDs         []migration.OpaqueID
	backendIdentities    []migration.OpaqueID
	borrowedData         [][]byte
}

var errManagerMaterializationInterrupted = errors.New("injected destination interruption")

type managerResumableMaterializationStager struct {
	calls          int
	resumeOffset   uint64
	resumedOffsets []uint64
}

func (stager *managerResumableMaterializationStager) StageMigrationDestination(
	ctx context.Context,
	request backend.DestinationStageRequest,
) (backend.DestinationStage, error) {
	stager.calls++
	component := request.Components[0]
	if stager.calls == 1 {
		err := request.ReadComponent(
			ctx, component.ComponentID, 0, 1024,
			func(extent backend.MigrationExtent) error {
				if extent.LogicalOffset >= 1024 {
					return errManagerMaterializationInterrupted
				}
				stager.resumeOffset = extent.LogicalOffset + extent.Length
				return nil
			},
		)
		if !errors.Is(err, errManagerMaterializationInterrupted) {
			return backend.DestinationStage{}, fmt.Errorf(
				"expected injected checkpoint interruption: %w", err,
			)
		}
		return backend.DestinationStage{}, err
	}
	if err := request.ReadComponent(
		ctx, component.ComponentID, stager.resumeOffset, 1024,
		func(extent backend.MigrationExtent) error {
			stager.resumedOffsets = append(stager.resumedOffsets, extent.LogicalOffset)
			return nil
		},
	); err != nil {
		return backend.DestinationStage{}, err
	}
	return backend.DestinationStage{
		Binding: request.Binding, StageHandle: request.StagingHandle,
		ObjectHandles: []migration.OpaqueID{request.Objects[0].BackendIdentity},
		Checkpoints: []backend.MigrationStageCheckpoint{{
			ComponentID: component.ComponentID, NextOffset: component.LogicalBytes,
			ContentDigest: component.ContentDigest,
		}},
		Stopped: true, Runnable: false,
	}, nil
}

func (stager *managerMaterializationStager) StageMigrationDestination(
	ctx context.Context,
	request backend.DestinationStageRequest,
) (backend.DestinationStage, error) {
	stager.calls++
	stager.operationIDs = append(stager.operationIDs, request.Binding.OperationID)
	stager.backendIdentities = append(stager.backendIdentities, request.Objects[0].BackendIdentity)
	checkpoints := make([]backend.MigrationStageCheckpoint, len(request.Components))
	for index, component := range request.Components {
		digester, err := migration.NewLogicalDigester(component.LogicalBytes)
		if err != nil {
			return backend.DestinationStage{}, err
		}
		err = request.ReadComponent(
			ctx, component.ComponentID, stager.resumeOffset, 1024,
			func(extent backend.MigrationExtent) error {
				if len(extent.Data) != 0 {
					stager.borrowedData = append(stager.borrowedData, extent.Data)
				}
				copyExtent := migration.Extent{
					Kind: extent.Kind, LogicalOffset: extent.LogicalOffset,
					Length: extent.Length, Data: append([]byte(nil), extent.Data...),
				}
				return digester.WriteExtent(copyExtent)
			},
		)
		if err != nil {
			return backend.DestinationStage{}, err
		}
		digest, err := digester.Finish()
		if err != nil {
			return backend.DestinationStage{}, err
		}
		stager.digests = append(stager.digests, digest)
		checkpoints[index] = backend.MigrationStageCheckpoint{
			ComponentID: component.ComponentID, NextOffset: component.LogicalBytes,
			ContentDigest: digest,
		}
	}
	if stager.substituteCheckpoint {
		checkpoints[0].ContentDigest = migration.Digest("sha256:" + strings.Repeat("f", 64))
	}
	return backend.DestinationStage{
		Binding: request.Binding, StageHandle: request.StagingHandle,
		ObjectHandles: []migration.OpaqueID{request.Objects[0].BackendIdentity},
		Checkpoints:   checkpoints, Stopped: true, Runnable: false,
	}, nil
}

func managerMaterializationDiskDigest(
	t *testing.T,
	first, last []byte,
) migration.Digest {
	t.Helper()
	digester, err := migration.NewLogicalDigester(3072)
	if err != nil {
		t.Fatal(err)
	}
	for _, extent := range []migration.Extent{
		{Kind: migration.ExtentData, LogicalOffset: 0, Length: 1024, Data: first},
		{Kind: migration.ExtentZero, LogicalOffset: 1024, Length: 512},
		{Kind: migration.ExtentHole, LogicalOffset: 1536, Length: 512},
		{Kind: migration.ExtentData, LogicalOffset: 2048, Length: 1024, Data: last},
	} {
		if err := digester.WriteExtent(extent); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := digester.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func managerMaterializationBytesDigest(value []byte) migration.Digest {
	digest := sha256.Sum256(value)
	return migration.Digest(fmt.Sprintf("sha256:%x", digest[:]))
}
