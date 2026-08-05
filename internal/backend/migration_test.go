package backend

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationProviderIsOptionalAndBaseBackendIsUnchanged(t *testing.T) {
	base := reflect.TypeOf((*Backend)(nil)).Elem()
	wantBase := []string{"Available", "Cleanup", "Name", "Prepare", "Run"}
	if base.NumMethod() != len(wantBase) {
		t.Fatalf("base backend methods=%d want=%d", base.NumMethod(), len(wantBase))
	}
	for index, name := range wantBase {
		if base.Method(index).Name != name {
			t.Fatalf("base backend method[%d]=%q want=%q", index, base.Method(index).Name, name)
		}
	}
	provider := reflect.TypeOf((*MigrationProvider)(nil)).Elem()
	if provider.NumMethod() != 11 {
		t.Fatalf("migration provider methods=%d", provider.NumMethod())
	}
	if methods := reflect.TypeOf((*MigrationExportProvider)(nil)).Elem().NumMethod(); methods != 5 {
		t.Fatalf("migration export provider methods=%d", methods)
	}
	if methods := reflect.TypeOf((*MigrationImportProvider)(nil)).Elem().NumMethod(); methods != 7 {
		t.Fatalf("migration import provider methods=%d", methods)
	}
}

func TestMigrationEffectBindingAndCapabilitiesFailClosed(t *testing.T) {
	binding := MigrationEffectBinding{
		OperationID: "operation_fixture1234",
		EffectID:    "effect_fixture1234",
		CapabilityRevision: migration.Digest(
			"sha256:" + strings.Repeat("a", 64),
		),
	}
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := binding
	invalid.EffectID = "../../disk"
	if err := invalid.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
		t.Fatalf("invalid effect binding error=%v", err)
	}

	capabilities := migrationCapabilityFixture()
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	capabilities.AdoptionHelper = nil
	if err := capabilities.Validate(); !errors.Is(err, ErrMigrationProviderCapability) {
		t.Fatalf("missing helper error=%v", err)
	}
	capabilities = migrationCapabilityFixture()
	capabilities.Limits.MaxLogicalBytes = migration.HardMaxLogicalBytes + 1
	if err := capabilities.Validate(); !errors.Is(err, ErrMigrationProviderCapability) {
		t.Fatalf("oversized capability error=%v", err)
	}
}

func TestMigrationSourceInspectionRequiresExactSortedProviderObjects(t *testing.T) {
	request := SourceInspectionRequest{
		Binding: migrationEffectBindingFixture(),
		Mode:    migration.ExportModeFull,
		Selections: []MigrationSourceSelection{
			{EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha"},
			{EnvironmentRef: "environment_bravo1", ProviderInstance: "hideout-bravo"},
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*SourceInspectionRequest){
		"config-only provider request": func(value *SourceInspectionRequest) {
			value.Mode = migration.ExportModeConfig
		},
		"derived path": func(value *SourceInspectionRequest) {
			value.Selections[0].ProviderInstance = "../../private"
		},
		"unsorted manager identities": func(value *SourceInspectionRequest) {
			value.Selections[0], value.Selections[1] = value.Selections[1], value.Selections[0]
		},
		"duplicate backend object": func(value *SourceInspectionRequest) {
			value.Selections[1].ProviderInstance = value.Selections[0].ProviderInstance
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := request
			mutated.Selections = append([]MigrationSourceSelection(nil), request.Selections...)
			mutate(&mutated)
			if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMigrationSourceInventoryValidatesDigestAndGraphClosure(t *testing.T) {
	inventory := migrationSourceInventoryFixture(t)
	if err := inventory.Validate(); err != nil {
		t.Fatal(err)
	}

	mutated := inventory
	mutated.Attachments = append([]MigrationSourceAttachment(nil), inventory.Attachments...)
	mutated.Attachments[0].DiskRef = "disk_missing1234"
	if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("unclosed graph error=%v", err)
	}

	mutated = inventory
	mutated.Capturable = false
	if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("false capturable claim error=%v", err)
	}

	mutated = inventory
	mutated.Disks = append([]MigrationSourceDisk(nil), inventory.Disks...)
	mutated.Disks[0].AllocatedBytesHint = mutated.Disks[0].LogicalBytes + 1
	digest, err := SourceInventoryDigest(mutated)
	if err != nil {
		t.Fatal(err)
	}
	mutated.InventoryDigest = digest
	if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("impossible allocated-size hint error=%v", err)
	}

	mutated = inventory
	mutated.InventoryDigest = migration.Digest("sha256:" + strings.Repeat("f", 64))
	if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("substituted digest error=%v", err)
	}

	mutated = inventory
	mutated.Binding.OperationID = "operation_revalidation2"
	mutated.Binding.EffectID = "effect_revalidation2"
	digest, err = SourceInventoryDigest(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if digest != inventory.InventoryDigest {
		t.Fatalf("read-only caller binding changed source facts: before=%s after=%s", inventory.InventoryDigest, digest)
	}
	mutated.InventoryDigest = digest
	if err := mutated.Validate(); err != nil {
		t.Fatalf("same source facts under apply binding failed validation: %v", err)
	}
}

func TestSourceSnapshotRequiresIdentityEvidenceBoundToCapturedRoots(t *testing.T) {
	snapshot := SourceSnapshot{
		Binding: migrationEffectBindingFixture(), SnapshotHandle: "snapshot_fixture1234",
		Components: []MigrationComponent{{
			ComponentID: "component_root1234", SnapshotHandle: "snapshot_fixture1234",
			DiskRef: "disk_root1234", Kind: "disk", LogicalBytes: 4096,
		}},
		Identities: []MigrationSourceIdentity{{
			EnvironmentRef: "environment_alpha1", RootComponent: "component_root1234",
			Evidence: migration.GuestIdentityEvidence{
				MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("a", 64)),
				SSHHostKeyDigests: []migration.Digest{
					migration.Digest("sha256:" + strings.Repeat("b", 64)),
				},
			},
		}},
		Independent: true,
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}

	missing := snapshot
	missing.Identities = nil
	if err := missing.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("missing identity evidence error=%v", err)
	}
	substituted := snapshot
	substituted.Identities = append([]MigrationSourceIdentity(nil), snapshot.Identities...)
	substituted.Identities[0].RootComponent = "component_other1234"
	if err := substituted.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("substituted root identity error=%v", err)
	}
}

func TestMigrationExtentAndProviderErrorAreBoundedAndRedacted(t *testing.T) {
	extent := MigrationExtent{
		Kind: migration.ExtentData, LogicalOffset: 0, Length: 4,
		Data: []byte("data"),
	}
	if err := extent.Validate(4); err != nil {
		t.Fatal(err)
	}
	extent.Data = nil
	if err := extent.Validate(4); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("invalid extent error=%v", err)
	}
	if err := (MigrationExtent{
		Kind: migration.ExtentHole, LogicalOffset: 0, Length: 4096,
	}).Validate(4); err != nil {
		t.Fatalf("payload-free sparse extent inherited data chunk bound: %v", err)
	}
	if err := (MigrationExtent{
		Kind: migration.ExtentData, LogicalOffset: 0, Length: 5,
		Data: make([]byte, 5),
	}).Validate(4); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("oversized data extent error=%v", err)
	}

	providerErr := &MigrationProviderError{
		Code: "migration.provider.snapshot_failed",
		Binding: MigrationEffectBinding{
			OperationID: "operation_fixture1234", EffectID: "effect_fixture1234",
		},
		OpaqueRef: "../../private/path",
		Cause: errors.New(
			"socks5://user:password@127.0.0.1:7890 /Users/alice/private",
		),
	}
	message := providerErr.Error()
	if !strings.HasPrefix(message, "migration.provider.snapshot_failed") ||
		strings.Contains(message, "password") || strings.Contains(message, "/Users") ||
		strings.Contains(message, "../../") {
		t.Fatalf("provider error leaked privileged diagnostics: %q", message)
	}
	if !errors.Is(providerErr, providerErr.Cause) {
		t.Fatal("provider error did not retain its privileged cause")
	}
}

func TestDestinationStageRequestBindsFreshObjectsComponentsAndClosedDiskGraph(t *testing.T) {
	request := migrationDestinationStageRequestFixture()
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	stage := DestinationStage{
		Binding: request.Binding, StageHandle: request.StagingHandle,
		ObjectHandles: []migration.OpaqueID{"backend_alpha1", "limadisk_shared1"},
		Checkpoints: []MigrationStageCheckpoint{{
			ComponentID:   request.Components[0].ComponentID,
			NextOffset:    request.Components[0].LogicalBytes,
			ContentDigest: request.Components[0].ContentDigest,
		}},
		Stopped: true,
	}
	if err := stage.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*DestinationStageRequest){
		"component substitution": func(value *DestinationStageRequest) {
			value.Components[0].DiskID = "disk_missing1234"
		},
		"root identity substitution": func(value *DestinationStageRequest) {
			value.Components[0].BackendIdentity = "backend_substitute1"
		},
		"unbound backend identity": func(value *DestinationStageRequest) {
			value.Objects[0].BackendIdentity = "../../private"
		},
		"invalid guest user": func(value *DestinationStageRequest) {
			value.Objects[0].GuestUser = "Root User"
		},
		"root guest user": func(value *DestinationStageRequest) {
			value.Objects[0].GuestUser = "root"
		},
		"root made read-only": func(value *DestinationStageRequest) {
			value.Edges[0].ReadOnly = true
		},
		"unreachable disk": func(value *DestinationStageRequest) {
			value.Edges = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := request
			mutated.Objects = append([]MigrationDestinationObject(nil), request.Objects...)
			mutated.Disks = append([]migration.DiskObject(nil), request.Disks...)
			mutated.Edges = append([]migration.DiskEdge(nil), request.Edges...)
			mutated.Components = append(
				[]MigrationDestinationComponent(nil), request.Components...,
			)
			mutate(&mutated)
			if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDestinationInspectionValidatesClosedGraphAndBoundedProviderResponse(t *testing.T) {
	stage := migrationDestinationStageRequestFixture()
	request := DestinationInspectionRequest{
		Binding:        stage.Binding,
		ManifestDigest: migration.Digest("sha256:" + strings.Repeat("e", 64)),
		SourceProduct: migration.SourceProduct{
			Version: "0.1.0", HostOS: "darwin", HostArch: "arm64",
			Backend: "lima", BackendVersion: "2.2.0", GuestArch: "arm64",
		},
		EnvironmentRefs:   []migration.OpaqueID{"environment_alpha1"},
		Disks:             append([]migration.DiskObject(nil), stage.Disks...),
		ProfileStateBytes: 512,
		Edges:             append([]migration.DiskEdge(nil), stage.Edges...),
		RequiredCapabilities: []migration.RequiredCapability{{
			ID: "full-state", Provider: "lima", MinimumVersion: "2.2.0",
		}},
		RequiredBytes: 6656,
		Capacity: migration.CapacityRequirement{
			Schema: migration.CapacityRequirementSchema, BundleBytes: 1024,
			StagingBytes: 4608, ValidationBytes: 1024,
			RollbackReserveBytes: 1024, FinalBytes: 4608,
			PeakAdditionalBytes: 6656,
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DestinationInspectionRequest){
		"required bytes drift": func(value *DestinationInspectionRequest) {
			value.RequiredBytes++
		},
		"edge escapes selection": func(value *DestinationInspectionRequest) {
			value.Edges[0].EnvironmentRef = "environment_other01"
		},
		"source architecture is not a token": func(value *DestinationInspectionRequest) {
			value.SourceProduct.GuestArch = "linux/arm64"
		},
		"capability version carries control text": func(value *DestinationInspectionRequest) {
			value.RequiredCapabilities[0].MinimumVersion = "2.2.0\nspoofed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := request
			mutated.Disks = append([]migration.DiskObject(nil), request.Disks...)
			mutated.Edges = append([]migration.DiskEdge(nil), request.Edges...)
			mutated.RequiredCapabilities = append(
				[]migration.RequiredCapability(nil), request.RequiredCapabilities...,
			)
			mutate(&mutated)
			if err := mutated.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	inventory := DestinationInventory{
		Binding: request.Binding, Compatible: false,
		CapabilityRevision: request.Binding.CapabilityRevision,
		AvailableBytes:     8192, SparseExtents: true,
		Conflicts: []migration.OpaqueID{"backend_conflict1"},
		Blockers: []MigrationProviderBlocker{{
			Code:    "migration.provider.architecture_mismatch",
			Summary: "The guest architecture is unsupported.",
		}},
	}
	if err := inventory.Validate(); err != nil {
		t.Fatal(err)
	}
	mutatedInventory := inventory
	mutatedInventory.Conflicts = []migration.OpaqueID{"backend_zconflict1", "backend_aconflict1"}
	if err := mutatedInventory.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("unsorted conflict error=%v", err)
	}
	mutatedInventory = inventory
	mutatedInventory.Blockers = []MigrationProviderBlocker{{
		Code: "not-a-provider-code", Summary: "invalid",
	}}
	if err := mutatedInventory.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("invalid blocker error=%v", err)
	}
}

func TestDestinationAdoptionVerificationAndRollbackContractsFailClosed(t *testing.T) {
	binding := migrationEffectBindingFixture()
	binding.OperationID = "op_migration_operation1234"
	sourceIdentity := migration.GuestIdentityEvidence{
		MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("1", 64)),
		SSHHostKeyDigests: []migration.Digest{
			migration.Digest("sha256:" + strings.Repeat("2", 64)),
		},
	}
	helper := migration.HelperBinding{
		PackageID: migration.AdoptionHelperPackage, Version: "1.0.0",
		SHA256: migration.Digest("sha256:" + strings.Repeat("3", 64)),
	}
	request := DestinationAdoptionRequest{
		Binding: binding, StageHandle: "stage_fixture1234",
		EnvironmentRef: "environment_alpha1", Policy: migration.GuestIdentitySafeClone,
		SourceIdentity: sourceIdentity, Helper: helper,
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	guestRequest := migration.AdoptionRequest{
		Schema: migration.AdoptionRequestSchema, OperationID: binding.OperationID,
		EnvironmentRef: request.EnvironmentRef, RequestNonce: "request_nonce001",
		ReceiptNonce: "receipt_nonce001", Policy: request.Policy,
		SourceIdentity:     sourceIdentity,
		DestinationSSHUser: "developer",
		DestinationSSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture"},
		PermittedActions: []string{
			migration.AdoptionActionResetMachineID,
			migration.AdoptionActionResetSSHHostKeys,
			migration.AdoptionActionInstallSSHKeys,
		},
		Helper: helper,
	}
	if err := guestRequest.Validate(); err != nil {
		t.Fatal(err)
	}
	postIdentity := migration.GuestIdentityEvidence{
		MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("4", 64)),
		SSHHostKeyDigests: []migration.Digest{
			migration.Digest("sha256:" + strings.Repeat("5", 64)),
		},
	}
	receipt := migration.AdoptionReceipt{
		Schema: migration.AdoptionReceiptSchema, OperationID: binding.OperationID,
		EnvironmentRef: guestRequest.EnvironmentRef,
		RequestNonce:   guestRequest.RequestNonce, ReceiptNonce: guestRequest.ReceiptNonce,
		Policy: guestRequest.Policy, Helper: helper,
		ActionResults: []migration.AdoptionActionResult{
			{Action: migration.AdoptionActionResetMachineID, Status: migration.AdoptionActionStatusCompleted},
			{Action: migration.AdoptionActionResetSSHHostKeys, Status: migration.AdoptionActionStatusCompleted},
			{Action: migration.AdoptionActionInstallSSHKeys, Status: migration.AdoptionActionStatusCompleted},
		},
		PostIdentity: &postIdentity, Status: migration.AdoptionReceiptStatusCompleted,
		CompletionMarker: true,
	}
	if err := receipt.MatchesRequest(guestRequest); err != nil {
		t.Fatal(err)
	}
	adoption := DestinationAdoption{
		Binding: binding, StageHandle: request.StageHandle, Request: guestRequest, Receipt: receipt,
		Stopped: true, TemporaryAuthorityRemoved: true,
	}
	if err := adoption.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	stage := migrationDestinationStageRequestFixture()
	verify := DestinationVerifyRequest{
		Binding: binding, StageHandle: request.StageHandle,
		ExpectedDisks: append([]migration.DiskObject(nil), stage.Disks...),
		IdentityPolicies: []migration.IdentitySelection{{
			SourceRef: request.EnvironmentRef, Policy: request.Policy,
		}},
		AdoptionRequests: []migration.AdoptionRequest{guestRequest},
		AdoptionReceipts: []migration.AdoptionReceipt{receipt},
	}
	if err := verify.Validate(); err != nil {
		t.Fatal(err)
	}
	proof := DestinationProof{
		Binding: binding, StageHandle: request.StageHandle,
		ProofDigest: migration.Digest("sha256:" + strings.Repeat("6", 64)),
		Stopped:     true, DigestsMatch: true, IdentityPolicySatisfied: true,
		TemporaryAuthorityRemoved: true, ImportedAuthorityAbsent: true,
	}
	if err := proof.MatchesRequest(verify); err != nil {
		t.Fatal(err)
	}
	rollback := DestinationRollbackRequest{
		Binding: binding, StageHandle: request.StageHandle,
		ObjectHandles: []migration.OpaqueID{"backend_alpha1", "limadisk_shared1"},
	}
	if err := rollback.Validate(); err != nil {
		t.Fatal(err)
	}
	activationBinding := binding
	activationBinding.EffectID = "effect_activate1234"
	activationRequest := DestinationActivationRequest{
		Binding: activationBinding, Proof: proof,
		ObjectHandles: []migration.OpaqueID{"backend_alpha1", "limadisk_shared1"},
	}
	if err := activationRequest.Validate(); err != nil {
		t.Fatal(err)
	}
	activation := DestinationActivation{
		Binding: activationBinding, StageHandle: proof.StageHandle,
		ProofDigest:   proof.ProofDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), activationRequest.ObjectHandles...),
		Stopped:       true, Promoted: true,
	}
	if err := activation.MatchesRequest(activationRequest); err != nil {
		t.Fatal(err)
	}

	adoption.Request.OperationID = "op_migration_substitute123"
	if err := adoption.MatchesRequest(request); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("adoption operation substitution error=%v", err)
	}
	adoption.Request = guestRequest
	adoption.Receipt.Status = migration.AdoptionReceiptStatusFailed
	if err := adoption.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("failed adoption response error=%v", err)
	}
	verify.AdoptionReceipts[0].Policy = migration.GuestIdentityExactRestore
	if err := verify.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
		t.Fatalf("receipt policy substitution error=%v", err)
	}
	verify.AdoptionReceipts[0].Policy = request.Policy
	verify.AdoptionRequests[0].SourceIdentity.MachineIDDigest =
		postIdentity.MachineIDDigest
	if err := verify.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
		t.Fatalf("adoption source identity substitution error=%v", err)
	}
	verify.AdoptionRequests[0] = guestRequest
	proof.ImportedAuthorityAbsent = false
	if err := proof.Validate(); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("unproved imported authority error=%v", err)
	}
	proof.ImportedAuthorityAbsent = true
	proof.StageHandle = "stage_substitute1234"
	if err := proof.MatchesRequest(verify); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("verification stage substitution error=%v", err)
	}
	rollback.ObjectHandles[0], rollback.ObjectHandles[1] =
		rollback.ObjectHandles[1], rollback.ObjectHandles[0]
	if err := rollback.Validate(); !errors.Is(err, ErrMigrationProviderRequest) {
		t.Fatalf("unsorted rollback objects error=%v", err)
	}
	activation.ProofDigest = migration.Digest("sha256:" + strings.Repeat("7", 64))
	if err := activation.MatchesRequest(activationRequest); !errors.Is(err, ErrMigrationProviderResponse) {
		t.Fatalf("activation proof substitution error=%v", err)
	}
}

func migrationCapabilityFixture() MigrationCapabilities {
	return MigrationCapabilities{
		Provider: "lima", ProviderVersion: "2.2.0",
		Revision:            migration.Digest("sha256:" + strings.Repeat("b", 64)),
		DiskRepresentations: []string{"raw", "qcow2"},
		ArchitecturePairs: []MigrationArchitecturePair{{
			Host: "darwin/arm64", Guest: "linux/arm64",
		}},
		FullExport: true, FullImport: true,
		RootDiskKinds: []string{"qcow2"}, AttachedDiskKinds: []string{"raw"},
		SparseExtents: true, Limits: migration.DefaultLimits(),
		AdoptionHelper: &MigrationHelperCapability{
			PackageID: "hideout-migration-adopt",
			Version:   "1.0.0", GuestArchitecture: "linux/arm64",
			Digest: migration.Digest("sha256:" + strings.Repeat("c", 64)),
		},
	}
}

func migrationEffectBindingFixture() MigrationEffectBinding {
	return MigrationEffectBinding{
		OperationID: "operation_fixture1234",
		EffectID:    "effect_fixture1234",
		CapabilityRevision: migration.Digest(
			"sha256:" + strings.Repeat("a", 64),
		),
	}
}

func migrationSourceInventoryFixture(t *testing.T) SourceInventory {
	t.Helper()
	inventory := SourceInventory{
		Binding: migrationEffectBindingFixture(), Provider: "lima",
		Instances: []MigrationSourceInstance{{
			EnvironmentRef:        "environment_alpha1",
			ProviderRef:           "provider_instance1",
			Lifecycle:             MigrationLifecycleStopped,
			ConfigurationRevision: 1,
			ConfigurationDigest:   migration.Digest("sha256:" + strings.Repeat("b", 64)),
			RootDiskRef:           "disk_root1234",
		}},
		Disks: []MigrationSourceDisk{{
			DiskRef: "disk_root1234", ProviderRef: "provider_disk1234",
			Role: migration.DiskRoleRoot, Format: "raw",
			LogicalBytes: 4096, AllocatedBytesHint: 4096,
			Consumers: []migration.OpaqueID{"environment_alpha1"},
		}},
		Attachments: []MigrationSourceAttachment{{
			EnvironmentRef: "environment_alpha1", DiskRef: "disk_root1234",
			Attachment: migration.DiskRoleRoot, GuestPath: "/",
		}},
		ExcludedClasses: []string{"logs"}, SelectionClosed: true, Capturable: true,
	}
	digest, err := SourceInventoryDigest(inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventory.InventoryDigest = digest
	return inventory
}

func migrationDestinationStageRequestFixture() DestinationStageRequest {
	digest := migration.Digest("sha256:" + strings.Repeat("d", 64))
	return DestinationStageRequest{
		Binding: migrationEffectBindingFixture(), StagingHandle: "stage_fixture1234",
		Objects: []MigrationDestinationObject{{
			EnvironmentRef: "environment_alpha1", BackendIdentity: "backend_alpha1",
			Runtime: "linux", GuestArchitecture: "linux/arm64", GuestUser: "developer",
			ProfileComponent: "profile_alpha1234",
		}},
		Disks: []migration.DiskObject{{
			DiskID: "disk_root1234", Role: migration.DiskRoleRoot, Format: "raw",
			LogicalBytes: 4096, AllocatedBytesHint: 4096, ContentDigest: digest,
			Provider: migration.ProviderDiskFacts{Name: "source-root", Kind: "lima-root"},
		}},
		Edges: []migration.DiskEdge{{
			EnvironmentRef: "environment_alpha1", DiskID: "disk_root1234",
			Attachment: migration.DiskRoleRoot, GuestPath: "/",
		}},
		Components: []MigrationDestinationComponent{{
			ComponentID: "component_root1234", DiskID: "disk_root1234",
			BackendIdentity: "backend_alpha1", Kind: "disk",
			LogicalBytes: 4096, ContentDigest: digest,
		}},
		ReadComponent: func(
			context.Context, migration.OpaqueID, uint64, uint32,
			func(MigrationExtent) error,
		) error {
			return nil
		},
	}
}
