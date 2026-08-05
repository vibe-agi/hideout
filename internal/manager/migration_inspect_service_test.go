package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationInspectionServiceConsumesInspectHandleWithoutMutatingBundle(t *testing.T) {
	bundlePath := writeManagerSealedBundleFixture(t)
	before, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := ProbeMigrationBundleFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{})
	defer cache.Close()
	handle, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeInspect, ClientBinding: "inspection-client",
		BundleID: probe.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: []byte("manager inspection passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (MigrationInspectionService{
		SecretInputs: secretInputs, Cache: cache,
	}).Inspect(
		context.Background(), MigrationReadOnlyInspectRequest{
			BundlePath: bundlePath, ExpectedFile: probe.BundleFile,
			SecretInputHandle: handle.Handle, ClientBinding: "inspection-client",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.BundleID != probe.BundleID || result.BundleFile != probe.BundleFile ||
		result.Inventory.BundleID != probe.BundleID || !result.Inventory.Sealed ||
		len(result.Inventory.Environments) != 1 || len(result.Inventory.Disks) != 2 {
		t.Fatalf("inspection result=%+v", result)
	}
	after, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only inspection changed the sealed bundle")
	}
	if _, err := secretInputs.Lookup(MigrationSecretInputLookup{
		Handle: handle.Handle, Purpose: MigrationSecretPurposeInspect,
		ClientBinding: "inspection-client", BundleFile: &probe.BundleFile,
	}); !errors.Is(err, ErrMigrationSecretInputRequired) {
		t.Fatalf("inspect handle was not consumed: %v", err)
	}
	importHandle, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "import-client",
		BundleID: probe.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: []byte("manager inspection passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := CachedMigrationBundleSource{SecretInputs: secretInputs, Cache: cache}
	raw, err := source.InspectMigrationBundle(context.Background(), MigrationBundleInspectRequest{
		BundlePath: bundlePath, ExpectedBinding: result.Binding,
		SecretInputHandle: importHandle.Handle, ClientBinding: "import-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw.Manifest.Environments[0].DisplayNameHint = "mutated caller copy"
	raw, err = source.InspectMigrationBundle(context.Background(), MigrationBundleInspectRequest{
		BundlePath: bundlePath, ExpectedBinding: result.Binding,
		SecretInputHandle: importHandle.Handle, ClientBinding: "import-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw.Manifest.Environments[0].DisplayNameHint != "dev" {
		t.Fatal("caller mutated the authenticated inspection cache")
	}
	if _, err := secretInputs.Lookup(MigrationSecretInputLookup{
		Handle: importHandle.Handle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: "import-client", BundleFile: &probe.BundleFile,
	}); err != nil {
		t.Fatalf("plan revalidation consumed the materialization handle: %v", err)
	}
	managerRoot := t.TempDir()
	provider := newManagerMigrationProviderFixture()
	importService := MigrationImportService{
		MigrationService: MigrationService{
			Store:        MigrationStore{Root: managerRoot},
			Environments: environment.Store{Root: managerRoot},
			Export:       provider, Import: provider, SecretInputs: secretInputs,
			NewID: sequentialMigrationIDSource(),
		},
		BundleSource: source,
	}
	plan, err := importService.PlanImport(context.Background(), MigrationImportPlanRequest{
		Draft: migration.ImportDraft{
			Schema: MigrationImportDraftSchema, BundlePath: bundlePath,
			BundleBinding:           result.Binding,
			SelectedEnvironmentRefs: []migration.OpaqueID{"environment_source1"},
			NameMappings: []migration.NameMapping{{
				SourceRef: "environment_source1", DestinationName: "dev-clone",
			}},
			WorkspaceMappings: []migration.WorkspaceMapping{},
			SecretMappings:    []migration.SecretMapping{},
			IdentityPolicies: []migration.IdentitySelection{{
				SourceRef: "environment_source1", Policy: migration.GuestIdentitySafeClone,
			}},
			AuthorityDecisions:   []migration.AuthorityDecision{},
			RiskAcknowledgements: []string{},
		},
		SecretInputHandle: importHandle.Handle, ClientBinding: "import-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BundleBinding != result.Binding || len(plan.Objects) != 1 || len(plan.Blockers) != 0 {
		t.Fatalf("production bundle source plan=%+v", plan)
	}
}

func TestMigrationInspectionServiceRejectsSymlinkWrongKeyAndFileDrift(t *testing.T) {
	bundlePath := writeManagerSealedBundleFixture(t)
	symlink := filepath.Join(t.TempDir(), "bundle.hideout-migration")
	if err := os.Symlink(bundlePath, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeMigrationBundleFile(symlink); err == nil {
		t.Fatal("bundle probe followed a symlink")
	}

	probe, err := ProbeMigrationBundleFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	secretInputs := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer secretInputs.Close()
	wrong, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeInspect, ClientBinding: "wrong-key-client",
		BundleID: probe.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: []byte("wrong passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (MigrationInspectionService{SecretInputs: secretInputs}).Inspect(
		context.Background(), MigrationReadOnlyInspectRequest{
			BundlePath: bundlePath, ExpectedFile: probe.BundleFile,
			SecretInputHandle: wrong.Handle, ClientBinding: "wrong-key-client",
		},
	); !errors.Is(err, migration.ErrAuthenticationFailed) {
		t.Fatalf("wrong-key inspection error=%v", err)
	}

	driftHandle, err := secretInputs.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeInspect, ClientBinding: "drift-client",
		BundleID: probe.BundleID, BundleFile: &probe.BundleFile,
		Passphrase: []byte("manager inspection passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(bundlePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (MigrationInspectionService{SecretInputs: secretInputs}).Inspect(
		context.Background(), MigrationReadOnlyInspectRequest{
			BundlePath: bundlePath, ExpectedFile: probe.BundleFile,
			SecretInputHandle: driftHandle.Handle, ClientBinding: "drift-client",
		},
	); err == nil {
		t.Fatal("changed bundle passed its original file binding")
	}
	if _, err := secretInputs.Lookup(MigrationSecretInputLookup{
		Handle: driftHandle.Handle, Purpose: MigrationSecretPurposeInspect,
		ClientBinding: "drift-client", BundleFile: &probe.BundleFile,
	}); err != nil {
		t.Fatalf("pre-consume file drift destroyed the retry handle: %v", err)
	}
}

func writeManagerSealedBundleFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dev.hideout-migration")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := migration.NewWriter(file, migration.WriterOptions{
		BundleID: "migb_managerinspect1", CreatedAt: "2026-08-02T00:00:00Z",
		KDF:    migration.KDFParameters{MemoryKiB: 8 << 10, Passes: 1, Lanes: 1},
		Limits: migration.DefaultLimits(), Random: bytes.NewReader(managerInspectionRandom(4096)),
		Passphrase: []byte("manager inspection passphrase"),
	})
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	profile := []byte(`{"schema":"fixture.profile/v1"}`)
	profileState, _ := managerMigrationProfileStateFixture()
	for _, record := range []migration.RecordInput{
		{Type: migration.RecordRawChunk, ComponentID: "component_attached1", Plaintext: bytes.Repeat([]byte{1}, 4096)},
		{Type: migration.RecordMetadata, ComponentID: "component_profile1", Plaintext: profile},
		{Type: migration.RecordRawChunk, ComponentID: "component_state001", Plaintext: profileState},
	} {
		if _, err := writer.Append(record); err != nil {
			_ = writer.Close()
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if _, _, err := writer.AppendCheckpoint(migration.CheckpointInput{
		OperationID: "op_managerinspect01",
		CompletedComponents: []migration.OpaqueID{
			"component_attached1", "component_profile1", "component_state001",
		},
		CurrentComponent: "component_state001",
	}); err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := writer.Append(migration.RecordInput{
		Type: migration.RecordRawChunk, ComponentID: "component_root0001",
		Plaintext: bytes.Repeat([]byte{2}, 8192),
	}); err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatal(err)
	}
	manifest := managerMigrationManifestFixture("migb_managerinspect1")
	manifest.ComponentIndex[1].LogicalBytes = uint64(len(profile))
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		_ = writer.Close()
		_ = file.Close()
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
	return path
}

func managerInspectionRandom(size int) []byte {
	value := make([]byte, size)
	for index := range value {
		value[index] = byte(index*29 + 11)
	}
	return value
}

func TestMigrationInspectionProjectionDoesNotNeedSourceHostPaths(t *testing.T) {
	manifest := managerMigrationManifestFixture("migb_projection0003")
	inspection := sealedManagerInspectionFixture(manifest)
	projection, err := ProjectMigrationBundleInspection(inspection)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "destination import passphrase") {
		t.Fatal("projection contained an input secret")
	}
}

func TestMigrationInspectionCacheExpiresAndClonesAuthenticatedManifest(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	cache := NewMigrationInspectionCache(MigrationInspectionCacheOptions{
		Now: func() time.Time { return now }, TTL: time.Minute,
	})
	defer cache.Close()
	manifest := managerMigrationManifestFixture("migb_cachefixture01")
	inspection := sealedManagerInspectionFixture(manifest)
	fileBinding := migrationBundleFileBindingFixture()
	if err := cache.Put(inspection, fileBinding); err != nil {
		t.Fatal(err)
	}
	first, err := cache.Get(inspection.Binding, fileBinding)
	if err != nil {
		t.Fatal(err)
	}
	first.Manifest.Environments[0].DisplayNameHint = "mutated"
	second, err := cache.Get(inspection.Binding, fileBinding)
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest.Environments[0].DisplayNameHint != "dev" {
		t.Fatal("cache returned aliased manifest state")
	}
	now = now.Add(time.Minute)
	if _, err := cache.Get(inspection.Binding, fileBinding); !errors.Is(err, ErrMigrationInspectionRequired) {
		t.Fatalf("expired cache error=%v", err)
	}
	if projected := ProjectMigrationError(ErrMigrationInspectionRequired); projected.Code != "migration.bundle.inspection_required" || !projected.Retryable {
		t.Fatalf("inspection-required projection=%+v", projected)
	}
}
