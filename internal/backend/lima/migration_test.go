package lima

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func TestMigrationCapabilitiesBindVersionLayoutAndPackagedHelper(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	runner := &migrationCapabilityRunner{version: "limactl version 2.2.0\n"}
	provider := Backend{
		Runner: runner,
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: helper,
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
			adoptionIsolationProber: migrationTestAdoptionIsolation,
		},
	}

	first, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("unchanged capability drifted: first=%+v second=%+v", first, second)
	}
	if !first.FullExport || !first.FullImport || first.Unavailable != nil ||
		first.ProviderVersion != "2.2.0" || !first.SparseExtents ||
		first.AdoptionHelper == nil ||
		first.AdoptionHelper.PackageID != helperbin.LinuxMigrationAdoptCommand ||
		first.AdoptionHelper.Version != migrationAdoptionVersion ||
		first.AdoptionHelper.GuestArchitecture != "linux/arm64" ||
		first.AdoptionHelper.Digest.Validate() != nil || first.Revision.Validate() != nil {
		t.Fatalf("unexpected migration capability: %+v", first)
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[0], []string{"--version"}) {
		t.Fatalf("version probe calls=%v", runner.calls)
	}
	rendered := strings.Join([]string{
		first.Provider,
		first.ProviderVersion,
		string(first.Revision),
		first.AdoptionHelper.PackageID,
		string(first.AdoptionHelper.Digest),
	}, " ")
	if strings.Contains(rendered, home) || strings.Contains(rendered, helper) {
		t.Fatalf("capability leaked host paths: %s", rendered)
	}
}

func TestMigrationCapabilitiesDisableFullImportWithoutProvedAdoptionIsolation(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	provider := Backend{
		Runner: &migrationCapabilityRunner{version: "limactl version 2.2.0\n"},
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: helper,
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
		},
	}
	capability, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capability.FullExport || capability.FullImport ||
		capability.AdoptionHelper == nil || capability.Unavailable == nil ||
		capability.Unavailable.Code != "migration.provider.adoption_isolation_unproved" {
		t.Fatalf("unexpected unproved-isolation capability: %+v", capability)
	}
}

func TestMigrationCapabilitiesUseOnlyPackagedZeroNetworkVZExecutor(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationProductionCapabilityRunner{
		version: "limactl version 2.2.0\n", probe: vzexecutor.CurrentProbe(),
	}
	provider := Backend{
		Runner: runner,
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: helper, AdoptionExecutorPath: executor,
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
		},
	}
	capability, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capability.FullImport || capability.Unavailable != nil {
		t.Fatalf("packaged executor capability=%+v", capability)
	}
	if len(runner.calls) != 2 || runner.calls[1].name != executor ||
		!reflect.DeepEqual(runner.calls[1].args, []string{"--probe"}) {
		t.Fatalf("executor probe calls=%+v", runner.calls)
	}

	runner.probe.NetworkDeviceCount = 1
	blocked, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if blocked.FullImport || blocked.Unavailable == nil ||
		blocked.Unavailable.Code != "migration.provider.adoption_isolation_unproved" {
		t.Fatalf("network-enabled executor capability=%+v", blocked)
	}
}

func TestMigrationCapabilityRevisionBindsPackagedVZExecutorBytes(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	provider := Backend{
		Runner: &migrationProductionCapabilityRunner{
			version: "limactl version 2.2.0\n", probe: vzexecutor.CurrentProbe(),
		},
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: helper, AdoptionExecutorPath: executor,
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
		},
	}
	before, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executor, []byte("executor-v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteHostMigrationVZAdoptManifest(
		executor, "darwin", "arm64",
	); err != nil {
		t.Fatal(err)
	}
	after, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !before.FullImport || !after.FullImport || before.Revision == after.Revision {
		t.Fatalf("executor bytes did not change capability: before=%+v after=%+v", before, after)
	}
}

func TestMigrationCapabilitiesDisableOnlyImportWhenHelperIsUnavailable(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	provider := Backend{
		Runner: &migrationCapabilityRunner{version: "limactl version 2.1.3\n"},
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: filepath.Join(t.TempDir(), "missing"),
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
		},
	}
	capability, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capability.FullExport || capability.FullImport || capability.AdoptionHelper != nil ||
		capability.Unavailable == nil ||
		capability.Unavailable.Code != "migration.provider.adoption_helper_unavailable" {
		t.Fatalf("unexpected helper-unavailable capability: %+v", capability)
	}
}

func TestMigrationCapabilitiesFailClosedForUnprovedVersionAndPlatform(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	for name, options := range map[string]struct {
		version  string
		hostOS   string
		hostArch string
		code     string
	}{
		"future Lima minor": {
			version: "limactl version 2.3.0\n", hostOS: "darwin",
			code: "migration.provider.lima_version_unsupported",
		},
		"trailing version content": {
			version: "limactl version 2.2.0\nuntrusted\n", hostOS: "darwin",
			code: "migration.provider.lima_version_unsupported",
		},
		"unsupported host": {
			version: "limactl version 2.2.0\n", hostOS: "linux",
			code: "migration.provider.platform_unsupported",
		},
		"unproved Intel Mac": {
			version: "limactl version 2.2.0\n", hostOS: "darwin", hostArch: "amd64",
			code: "migration.provider.platform_unsupported",
		},
	} {
		t.Run(name, func(t *testing.T) {
			hostArch := options.hostArch
			if hostArch == "" {
				hostArch = "arm64"
			}
			provider := Backend{
				Runner: &migrationCapabilityRunner{version: options.version},
				Migration: &MigrationOptions{
					LimaHome: home, HelperPath: helper,
					HostOS: options.hostOS, HostArch: hostArch, GuestArch: hostArch,
				},
			}
			capability, err := provider.MigrationCapabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if capability.FullExport || capability.FullImport ||
				capability.Unavailable == nil ||
				capability.Unavailable.Code != options.code {
				t.Fatalf("unexpected fail-closed capability: %+v", capability)
			}
		})
	}
}

func TestMigrationCapabilityRevisionChangesWithHelperBytes(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	provider := Backend{
		Runner: &migrationCapabilityRunner{version: "limactl version 2.2.0\n"},
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: helper,
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
			adoptionIsolationProber: migrationTestAdoptionIsolation,
		},
	}
	before, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("helper-v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteLinuxMigrationAdoptManifest(helper, "arm64"); err != nil {
		t.Fatal(err)
	}
	after, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision == after.Revision ||
		before.AdoptionHelper.Digest == after.AdoptionHelper.Digest {
		t.Fatalf("helper change did not invalidate capability: before=%+v after=%+v", before, after)
	}
}

func TestMigrationCapabilityRevisionBindsAdoptionIsolationProof(t *testing.T) {
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	proof := "test-only-offline-adoption-executor-v1"
	provider := Backend{
		Runner: &migrationCapabilityRunner{version: "limactl version 2.2.0\n"},
		Migration: &MigrationOptions{
			LimaHome: home, HelperPath: helper,
			HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
			adoptionIsolationProber: func(context.Context, string, string) (string, error) {
				return proof, nil
			},
		},
	}
	before, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	proof = "test-only-offline-adoption-executor-v2"
	after, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !before.FullImport || !after.FullImport || before.Revision == after.Revision {
		t.Fatalf("isolation proof did not invalidate capability: before=%+v after=%+v", before, after)
	}
}

func TestInspectMigrationSourceIsReadOnlyAndIdempotent(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1",
		status: "Stopped",
	}})
	request := fixture.request(t, []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}})
	before := migrationFixtureTreeDigest(t, fixture.home)
	first, err := fixture.provider.InspectMigrationSource(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.provider.InspectMigrationSource(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	after := migrationFixtureTreeDigest(t, fixture.home)
	if before != after {
		t.Fatalf("source tree changed during inspection: before=%s after=%s", before, after)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("idempotent inspection drifted:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if !first.Capturable || !first.SelectionClosed || len(first.Blockers) != 0 ||
		len(first.Instances) != 1 || len(first.Disks) != 1 ||
		len(first.Attachments) != 1 || first.InventoryDigest.Validate() != nil ||
		first.Instances[0].Lifecycle != backend.MigrationLifecycleStopped {
		t.Fatalf("unexpected source inventory: %+v", first)
	}
	for _, call := range fixture.runner.calls {
		if !migrationReadOnlyInspectionCall(call) {
			t.Fatalf("source inspection issued a mutating or unknown command: %v", call)
		}
	}
}

func TestInspectMigrationSourceRequiresSharedDiskSelectionClosure(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{
		{
			name: "hideout-alpha", environmentRef: "environment_alpha1",
			status: "Stopped", additionalDisks: []string{"shared-data"},
		},
		{
			name: "hideout-bravo", environmentRef: "environment_bravo1",
			status: "Stopped", additionalDisks: []string{"shared-data"},
		},
	})

	openRequest := fixture.request(t, []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}})
	open, err := fixture.provider.InspectMigrationSource(context.Background(), openRequest)
	if err != nil {
		t.Fatal(err)
	}
	if open.SelectionClosed || open.Capturable ||
		!migrationInventoryHasBlocker(open, "migration.provider.shared_disk_selection_open") {
		t.Fatalf("open shared-disk selection was accepted: %+v", open)
	}

	closedRequest := fixture.request(t, []backend.MigrationSourceSelection{
		{EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha"},
		{EnvironmentRef: "environment_bravo1", ProviderInstance: "hideout-bravo"},
	})
	closed, err := fixture.provider.InspectMigrationSource(context.Background(), closedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.SelectionClosed || !closed.Capturable || len(closed.Blockers) != 0 ||
		len(closed.Instances) != 2 || len(closed.Disks) != 3 || len(closed.Attachments) != 4 {
		t.Fatalf("closed shared-disk selection was not capturable: %+v", closed)
	}
	attached := 0
	for _, disk := range closed.Disks {
		if disk.Role != migration.DiskRoleAttached {
			continue
		}
		attached++
		if len(disk.Consumers) != 2 || disk.Consumers[0] != "environment_alpha1" ||
			disk.Consumers[1] != "environment_bravo1" {
			t.Fatalf("shared disk consumers=%v", disk.Consumers)
		}
	}
	if attached != 1 {
		t.Fatalf("attached disk objects=%d want=1", attached)
	}
}

func TestInspectMigrationSourceRequiresExactStoppedState(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1",
		status: "Running",
	}})
	request := fixture.request(t, []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}})
	inventory, err := fixture.provider.InspectMigrationSource(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Capturable || inventory.SelectionClosed != true ||
		!migrationInventoryHasBlocker(inventory, "migration.provider.source_not_stopped") ||
		inventory.Instances[0].Lifecycle != backend.MigrationLifecycleRunning {
		t.Fatalf("running source was accepted: %+v", inventory)
	}
}

func TestInspectMigrationSourceFailsClosedForLegacyOrAliasedRoot(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *migrationSourceFixture){
		"legacy root layout": func(t *testing.T, fixture *migrationSourceFixture) {
			path := filepath.Join(fixture.home, "hideout-alpha", "diffdisk")
			if err := os.WriteFile(path, []byte("legacy"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"aliased root disk": func(t *testing.T, fixture *migrationSourceFixture) {
			instanceDir := filepath.Join(fixture.home, "hideout-alpha")
			rootPath := filepath.Join(instanceDir, "disk")
			aliasTarget := filepath.Join(instanceDir, "disk-target")
			if err := os.Rename(rootPath, aliasTarget); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(aliasTarget, rootPath); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
				name: "hideout-alpha", environmentRef: "environment_alpha1",
				status: "Stopped",
			}})
			mutate(t, fixture)
			request := fixture.request(t, []backend.MigrationSourceSelection{{
				EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
			}})
			_, err := fixture.provider.InspectMigrationSource(context.Background(), request)
			var providerErr *backend.MigrationProviderError
			if !errors.As(err, &providerErr) ||
				providerErr.Code != "migration.provider.source_layout_unproved" ||
				strings.Contains(err.Error(), fixture.home) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSnapshotMigrationSourceCreatesDetachedIdempotentCOWComponents(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{
		{
			name: "hideout-alpha", environmentRef: "environment_alpha1",
			status: "Stopped", additionalDisks: []string{"shared-data"},
		},
		{
			name: "hideout-bravo", environmentRef: "environment_bravo1",
			status: "Stopped", additionalDisks: []string{"shared-data"},
		},
	})
	selections := []backend.MigrationSourceSelection{
		{EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha"},
		{EnvironmentRef: "environment_bravo1", ProviderInstance: "hideout-bravo"},
	}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(context.Background(), inspectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	diskRefs := make([]migration.OpaqueID, len(inventory.Disks))
	for index, disk := range inventory.Disks {
		diskRefs[index] = disk.DiskRef
	}
	request := backend.SourceSnapshotRequest{
		Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
		Selections: selections, DiskRefs: diskRefs,
	}
	alphaBefore := migrationFixtureTreeDigest(t, filepath.Join(fixture.home, "hideout-alpha"))
	bravoBefore := migrationFixtureTreeDigest(t, filepath.Join(fixture.home, "hideout-bravo"))
	diskBefore := migrationFixtureTreeDigest(t, filepath.Join(fixture.home, "_disks", "shared-data"))

	first, err := fixture.provider.SnapshotMigrationSource(context.Background(), request)
	if err != nil {
		var providerErr *backend.MigrationProviderError
		if errors.As(err, &providerErr) {
			t.Fatalf("%v: cause=%v", err, providerErr.Cause)
		}
		t.Fatal(err)
	}
	second, err := fixture.provider.SnapshotMigrationSource(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !first.Independent || first.SourceClaimsRequired ||
		len(first.Components) != 3 || len(first.Identities) != 2 || first.Validate() != nil ||
		first.Identities[0].Evidence.Equal(first.Identities[1].Evidence) {
		t.Fatalf("unexpected snapshot:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if alphaBefore != migrationFixtureTreeDigest(t, filepath.Join(fixture.home, "hideout-alpha")) ||
		bravoBefore != migrationFixtureTreeDigest(t, filepath.Join(fixture.home, "hideout-bravo")) ||
		diskBefore != migrationFixtureTreeDigest(t, filepath.Join(fixture.home, "_disks", "shared-data")) {
		t.Fatal("snapshot modified a source instance or attached disk")
	}
	cloneCalls := 0
	for _, call := range fixture.runner.calls {
		if len(call) != 0 && call[0] == "clone" {
			cloneCalls++
			if len(call) != 7 || call[1] != "--tty=false" || call[2] != "--mount-none" ||
				call[3] != "--set" || call[4] != ".additionalDisks = []" {
				t.Fatalf("unsafe Lima clone command: %v", call)
			}
		}
	}
	if cloneCalls != 2 {
		t.Fatalf("clone calls=%d want=2; retry recreated a snapshot", cloneCalls)
	}
	for _, instance := range fixture.runner.instances {
		if strings.HasPrefix(instance.Name, "hideout-mig-") {
			if _, err := os.Lstat(instance.Dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary runnable snapshot clone remains: %s", instance.Name)
			}
		}
	}
}

func TestSnapshotMigrationSourceInvalidatesLifecycleChangeDuringCapture(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1", status: "Stopped",
	}})
	fixture.provider.Migration.sourceIdentityObserver = func(
		ctx context.Context,
		home string,
		owner migrationSnapshotOwner,
		ownerDigest migration.Digest,
	) ([]backend.MigrationSourceIdentity, []migration.Digest, error) {
		identities, proofs, err := migrationTestSourceIdentityObserver(
			ctx, home, owner, ownerDigest,
		)
		// Simulate another lifecycle actor starting the exact source after the
		// detached disk copy but before Manager accepts capture completion.
		fixture.runner.instances[0].Status = "Running"
		return identities, proofs, err
	}
	selections := []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(
		context.Background(), inspectionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.provider.SnapshotMigrationSource(
		context.Background(), backend.SourceSnapshotRequest{
			Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
			Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
		},
	)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.snapshot_source_changed" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("lifecycle-race snapshot error=%v", err)
	}
}

func TestSnapshotMigrationSourceObservesIdentityThroughDisposableZeroNetworkCOWProbe(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1", status: "Stopped",
	}})
	sourceRoot := filepath.Join(fixture.home, "hideout-alpha", "disk")
	if err := os.Chmod(sourceRoot, 0o644); err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	fixture.provider.Migration.AdoptionExecutorPath = executor
	fixture.provider.Migration.adoptionIsolationProber = nil
	fixture.provider.Migration.sourceIdentityObserver = nil
	selections := []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(
		context.Background(), inspectionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := migrationFixtureTreeDigest(
		t, filepath.Join(fixture.home, "hideout-alpha"),
	)
	snapshot, err := fixture.provider.SnapshotMigrationSource(
		context.Background(), backend.SourceSnapshotRequest{
			Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
			Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
		},
	)
	if err != nil {
		var providerErr *backend.MigrationProviderError
		if errors.As(err, &providerErr) {
			t.Fatalf("identity snapshot error=%v cause=%v", err, providerErr.Cause)
		}
		t.Fatal(err)
	}
	if snapshot.Validate() != nil || len(snapshot.Identities) != 1 ||
		fixture.runner.identityExecutions != 1 ||
		snapshot.Identities[0].EnvironmentRef != "environment_alpha1" {
		t.Fatalf(
			"snapshot=%+v identityExecutions=%d",
			snapshot, fixture.runner.identityExecutions,
		)
	}
	if after := migrationFixtureTreeDigest(
		t, filepath.Join(fixture.home, "hideout-alpha"),
	); after != sourceBefore {
		t.Fatalf("identity probe changed source instance: before=%s after=%s", sourceBefore, after)
	}
	info, err := os.Lstat(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("identity probe changed source root mode: mode=%v", info.Mode())
	}
	snapshotDir := filepath.Join(
		fixture.home, "_hideout-migration", "snapshots", string(snapshot.SnapshotHandle),
	)
	entries, err := os.ReadDir(filepath.Join(snapshotDir, "identity-probes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("identity probe authority remains: entries=%v err=%v", entries, err)
	}
	if _, err := os.Lstat(filepath.Join(
		snapshotDir, migrationSnapshotIdentityEvidenceRelativePath,
	)); err != nil {
		t.Fatalf("durable source identity evidence is absent: %v", err)
	}
	replayed, err := fixture.provider.SnapshotMigrationSource(
		context.Background(), backend.SourceSnapshotRequest{
			Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
			Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
		},
	)
	if err != nil || !reflect.DeepEqual(snapshot, replayed) ||
		fixture.runner.identityExecutions != 1 {
		t.Fatalf(
			"replayed=%+v err=%v identityExecutions=%d",
			replayed, err, fixture.runner.identityExecutions,
		)
	}
}

func TestReleaseMigrationSnapshotRequiresExactOwnerAndIsIdempotent(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1", status: "Stopped",
	}})
	selections := []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(context.Background(), inspectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.provider.SnapshotMigrationSource(
		context.Background(), backend.SourceSnapshotRequest{
			Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
			Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDir := filepath.Join(
		fixture.home, "_hideout-migration", "snapshots", string(snapshot.SnapshotHandle),
	)
	wrong := inspectionRequest.Binding
	wrong.OperationID = "operation_different1234"
	err = fixture.provider.ReleaseMigrationSnapshot(context.Background(), backend.SnapshotReleaseRequest{
		Binding: wrong, SnapshotHandle: snapshot.SnapshotHandle,
	})
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.snapshot_cleanup_unproved" {
		t.Fatalf("wrong-owner cleanup error=%v", err)
	}
	if _, err := os.Lstat(snapshotDir); err != nil {
		t.Fatalf("wrong owner removed snapshot: %v", err)
	}
	request := backend.SnapshotReleaseRequest{
		Binding: inspectionRequest.Binding, SnapshotHandle: snapshot.SnapshotHandle,
	}
	unknown := filepath.Join(snapshotDir, "unexpected")
	if err := os.WriteFile(unknown, []byte("do-not-delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeRejectedCleanup := migrationFixtureTreeDigest(t, snapshotDir)
	err = fixture.provider.ReleaseMigrationSnapshot(context.Background(), request)
	providerErr = nil
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.snapshot_cleanup_unproved" {
		t.Fatalf("unknown-node cleanup error=%v", err)
	}
	if after := migrationFixtureTreeDigest(t, snapshotDir); after != beforeRejectedCleanup {
		t.Fatalf("unknown-node rejection changed snapshot: before=%s after=%s", beforeRejectedCleanup, after)
	}
	if err := os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	if err := fixture.provider.ReleaseMigrationSnapshot(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(snapshotDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot remains after release: %v", err)
	}
	if err := fixture.provider.ReleaseMigrationSnapshot(context.Background(), request); err != nil {
		t.Fatalf("release replay failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.home, "hideout-alpha", "disk")); err != nil {
		t.Fatalf("snapshot cleanup changed source disk: %v", err)
	}
}

func TestSnapshotMigrationSourceRejectsRunnableTemporaryClone(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1", status: "Stopped",
	}})
	fixture.runner.cloneStatus = "Running"
	selections := []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(context.Background(), inspectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	request := backend.SourceSnapshotRequest{
		Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
		Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
	}
	_, err = fixture.provider.SnapshotMigrationSource(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) || !providerErr.RecoveryRequired ||
		providerErr.Code != "migration.provider.snapshot_materialization_failed" {
		if providerErr != nil {
			t.Fatalf("runnable clone error=%v cause=%v", err, providerErr.Cause)
		}
		t.Fatalf("runnable clone error=%v", err)
	}
}

func TestReadMigrationComponentStreamsBoundedSparseExtentsAndResumes(t *testing.T) {
	const logicalBytes = 16 << 20
	const maxChunkBytes = 64 << 10
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1", status: "Stopped",
	}})
	sourceRoot := filepath.Join(fixture.home, "hideout-alpha", "disk")
	if err := rewriteSparseMigrationFixture(sourceRoot, logicalBytes); err != nil {
		t.Fatal(err)
	}
	selections := []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(context.Background(), inspectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.provider.SnapshotMigrationSource(
		context.Background(),
		backend.SourceSnapshotRequest{
			Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
			Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDir := filepath.Join(
		fixture.home, "_hideout-migration", "snapshots", string(snapshot.SnapshotHandle),
	)
	var owner migrationSnapshotOwner
	if err := readMigrationJSONStrict(filepath.Join(snapshotDir, "owner.json"), &owner); err != nil {
		t.Fatal(err)
	}
	componentPath := migrationSnapshotEntryPath(fixture.home, snapshotDir, owner.Entries[0])
	if err := rewriteSparseMigrationFixture(componentPath, logicalBytes); err != nil {
		t.Fatal(err)
	}
	request := backend.ComponentReadRequest{
		Binding: inspectionRequest.Binding, SnapshotHandle: snapshot.SnapshotHandle,
		ComponentID:   snapshot.Components[0].ComponentID,
		MaxChunkBytes: maxChunkBytes,
	}
	var extents []backend.MigrationExtent
	if err := fixture.provider.ReadMigrationComponent(
		context.Background(), request,
		func(extent backend.MigrationExtent) error {
			extents = append(extents, extent)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	assertMigrationExtentCoverage(t, extents, 0, logicalBytes, maxChunkBytes)
	hasSparse, hasData := false, false
	for _, extent := range extents {
		hasSparse = hasSparse || extent.Kind == migration.ExtentHole ||
			extent.Kind == migration.ExtentZero
		hasData = hasData || extent.Kind == migration.ExtentData
	}
	if !hasSparse || !hasData {
		t.Fatalf("sparse stream did not distinguish data and sparse zero ranges: extents=%d", len(extents))
	}
	digester, err := migration.NewLogicalDigester(logicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, extent := range extents {
		if err := digester.WriteExtent(migration.Extent{
			Kind: extent.Kind, LogicalOffset: extent.LogicalOffset,
			Length: extent.Length, Data: extent.Data,
		}); err != nil {
			t.Fatalf("provider emitted a noncanonical digest stream: %v", err)
		}
	}
	if _, err := digester.Finish(); err != nil {
		t.Fatal(err)
	}

	request.ResumeOffset = 4 << 20
	extents = nil
	if err := fixture.provider.ReadMigrationComponent(
		context.Background(), request,
		func(extent backend.MigrationExtent) error {
			extents = append(extents, extent)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	assertMigrationExtentCoverage(t, extents, 4<<20, logicalBytes, maxChunkBytes)
}

func TestReadMigrationComponentPropagatesCancellationAndCallbackFailure(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-alpha", environmentRef: "environment_alpha1", status: "Stopped",
	}})
	selections := []backend.MigrationSourceSelection{{
		EnvironmentRef: "environment_alpha1", ProviderInstance: "hideout-alpha",
	}}
	inspectionRequest := fixture.request(t, selections)
	inventory, err := fixture.provider.InspectMigrationSource(context.Background(), inspectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.provider.SnapshotMigrationSource(
		context.Background(),
		backend.SourceSnapshotRequest{
			Binding: inspectionRequest.Binding, InventoryDigest: inventory.InventoryDigest,
			Selections: selections, DiskRefs: []migration.OpaqueID{inventory.Disks[0].DiskRef},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := backend.ComponentReadRequest{
		Binding: inspectionRequest.Binding, SnapshotHandle: snapshot.SnapshotHandle,
		ComponentID: snapshot.Components[0].ComponentID, MaxChunkBytes: 1024,
	}
	callbackErr := errors.New("stop after first extent")
	if err := fixture.provider.ReadMigrationComponent(
		context.Background(), request,
		func(backend.MigrationExtent) error { return callbackErr },
	); !errors.Is(err, callbackErr) {
		t.Fatalf("callback error=%v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fixture.provider.ReadMigrationComponent(
		cancelled, request,
		func(backend.MigrationExtent) error { return nil },
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

type migrationCapabilityRunner struct {
	version  string
	lookPath string
	runErr   error
	calls    [][]string
}

type migrationSourceInstanceFixture struct {
	name            string
	environmentRef  migration.OpaqueID
	status          string
	additionalDisks []string
}

type migrationSourceFixture struct {
	home     string
	provider Backend
	runner   *migrationSourceRunner
}

type migrationSourceRunner struct {
	version            string
	home               string
	cloneStatus        string
	instances          []migrationLimaInstanceInventory
	disks              []migrationLimaDiskInventory
	calls              [][]string
	identityExecutions int
}

func (runner *migrationSourceRunner) LookPath(string) (string, error) {
	return "/opt/homebrew/bin/limactl", nil
}

func (runner *migrationSourceRunner) Run(
	_ context.Context,
	_ string,
	args []string,
	_ []string,
	stdin io.Reader,
	stdout,
	_ io.Writer,
) error {
	runner.calls = append(runner.calls, append([]string(nil), args...))
	switch {
	case reflect.DeepEqual(args, []string{"--version"}):
		_, err := io.WriteString(stdout, runner.version)
		return err
	case reflect.DeepEqual(args, []string{"list", "--format", "json", "--all-fields"}):
		visible := make([]migrationLimaInstanceInventory, 0, len(runner.instances))
		for _, instance := range runner.instances {
			if _, err := os.Lstat(instance.Dir); err == nil {
				visible = append(visible, instance)
			}
		}
		return encodeMigrationInventory(stdout, visible)
	case reflect.DeepEqual(args, []string{"disk", "list", "--json"}):
		return encodeMigrationInventory(stdout, runner.disks)
	case reflect.DeepEqual(args, []string{"--probe"}):
		return json.NewEncoder(stdout).Encode(vzexecutor.CurrentProbe())
	case len(args) == 0:
		return runner.runIdentityObservation(stdin, stdout)
	case len(args) == 7 && args[0] == "clone" && args[1] == "--tty=false" &&
		args[2] == "--mount-none" && args[3] == "--set" &&
		args[4] == ".additionalDisks = []":
		return runner.cloneInstance(args[5], args[6])
	default:
		return fmt.Errorf("unexpected migration source command: %v", args)
	}
}

func (runner *migrationSourceRunner) runIdentityObservation(
	stdin io.Reader,
	stdout io.Writer,
) error {
	runner.identityExecutions++
	var execution vzexecutor.ExecutionRequest
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&execution); err != nil {
		return err
	}
	paths, err := execution.Paths()
	if err != nil {
		return err
	}
	var request migration.IdentityObservationRequest
	data, err := os.ReadFile(paths.GuestRequest)
	if err != nil {
		return err
	}
	requestDecoder := json.NewDecoder(strings.NewReader(string(data)))
	requestDecoder.DisallowUnknownFields()
	if err := requestDecoder.Decode(&request); err != nil || request.Validate() != nil {
		return errors.New("identity observation fixture request is invalid")
	}
	probeInfo, err := os.Lstat(paths.RootDisk)
	if err != nil || probeInfo.Mode()&os.ModeSymlink != 0 ||
		!probeInfo.Mode().IsRegular() || probeInfo.Mode().Perm() != 0o600 {
		return errors.New("identity observation fixture root is not private")
	}
	probeDisk, err := os.OpenFile(paths.RootDisk, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, writeErr := probeDisk.WriteAt([]byte{0x5a}, 0)
	syncErr := probeDisk.Sync()
	closeErr := probeDisk.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	identity := migration.GuestIdentityEvidence{
		MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("a", 64)),
		SSHHostKeyDigests: []migration.Digest{
			migration.Digest("sha256:" + strings.Repeat("b", 64)),
		},
	}
	receipt := migration.IdentityObservationReceipt{
		Schema:      migration.IdentityObservationReceiptSchema,
		OperationID: request.OperationID, EnvironmentRef: request.EnvironmentRef,
		RequestNonce: request.RequestNonce, ReceiptNonce: request.ReceiptNonce,
		Helper: request.Helper, Identity: &identity,
		Status: migration.AdoptionReceiptStatusCompleted, CompletionMarker: true,
	}
	if err := writeMigrationAdoptionJSONFixture(paths.GuestReceipt, receipt, 0o600); err != nil {
		return err
	}
	response := vzexecutor.ExecutionResponse{
		Schema:         vzexecutor.ExecutionResponseSchema,
		ExecutionNonce: execution.ExecutionNonce,
		Started:        true, Stopped: true, NetworkDeviceCount: 0,
		ReceiptObserved: true,
		StopReason:      vzexecutor.StopReasonReceiptAndGuestShutdown,
	}
	proof, err := response.ExpectedShutdownProof()
	if err != nil {
		return err
	}
	response.ShutdownProof = proof
	if err := writeMigrationAdoptionJSONFixture(
		paths.ExecutorResponse, response, 0o600,
	); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(response)
}

func (runner *migrationSourceRunner) cloneInstance(sourceName, cloneName string) error {
	var source migrationLimaInstanceInventory
	found := false
	for _, candidate := range runner.instances {
		if candidate.Name == sourceName {
			source, found = candidate, true
			break
		}
	}
	if !found {
		return errors.New("snapshot source is absent")
	}
	cloneDir := filepath.Join(runner.home, cloneName)
	if err := os.Mkdir(cloneDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "lima-version"), []byte(source.LimaVersion+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(cloneDir, "lima.yaml"),
		[]byte("vmType: vz\narch: aarch64\nadditionalDisks: []\n"),
		0o600,
	); err != nil {
		return err
	}
	if err := copyMigrationFixtureFile(
		filepath.Join(source.Dir, "disk"), filepath.Join(cloneDir, "disk"),
	); err != nil {
		return err
	}
	status := runner.cloneStatus
	if status == "" {
		status = "Stopped"
	}
	clone := migrationLimaInstanceInventory{
		Name: cloneName, Status: status, Dir: cloneDir,
		VMType: "vz", Arch: "aarch64", LimaVersion: source.LimaVersion,
	}
	clone.Config.VMType = "vz"
	clone.Config.Arch = "aarch64"
	runner.instances = append(runner.instances, clone)
	return nil
}

func encodeMigrationInventory[T any](writer io.Writer, items []T) error {
	encoder := json.NewEncoder(writer)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func newMigrationSourceFixture(
	t *testing.T,
	instances []migrationSourceInstanceFixture,
) *migrationSourceFixture {
	t.Helper()
	home := privateMigrationHomeFixture(t)
	helper := migrationAdoptionHelperFixture(t, "arm64", "helper-v1")
	runner := &migrationSourceRunner{version: "limactl version 2.2.0\n", home: home}
	diskNames := make(map[string]struct{})
	for index, source := range instances {
		instanceDir := filepath.Join(home, source.name)
		if err := os.Mkdir(instanceDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(instanceDir, "lima-version"), []byte("2.2.0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var config strings.Builder
		config.WriteString("vmType: vz\narch: aarch64\n")
		if len(source.additionalDisks) != 0 {
			config.WriteString("additionalDisks:\n")
			for _, diskName := range source.additionalDisks {
				fmt.Fprintf(&config, "- %s\n", diskName)
				diskNames[diskName] = struct{}{}
			}
		}
		if err := os.WriteFile(filepath.Join(instanceDir, "lima.yaml"), []byte(config.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		rootBytes := []byte(fmt.Sprintf("root-%02d-%s", index, source.name))
		rootBytes = append(rootBytes, make([]byte, 4096-len(rootBytes))...)
		if err := os.WriteFile(filepath.Join(instanceDir, "disk"), rootBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		info := migrationLimaInstanceInventory{
			Name: source.name, Status: source.status, Dir: instanceDir,
			VMType: "vz", Arch: "aarch64", LimaVersion: "2.2.0",
		}
		info.Config.VMType = "vz"
		info.Config.Arch = "aarch64"
		for _, diskName := range source.additionalDisks {
			info.Config.AdditionalDisks = append(
				info.Config.AdditionalDisks,
				migrationLimaDiskConfig{Name: diskName},
			)
		}
		runner.instances = append(runner.instances, info)
	}
	if len(diskNames) != 0 {
		disksRoot := filepath.Join(home, "_disks")
		if err := os.Mkdir(disksRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(diskNames))
		for name := range diskNames {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			diskDir := filepath.Join(disksRoot, name)
			if err := os.Mkdir(diskDir, 0o700); err != nil {
				t.Fatal(err)
			}
			data := append([]byte("attached-"+name), make([]byte, 8192-len("attached-"+name))...)
			if err := os.WriteFile(filepath.Join(diskDir, "datadisk"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			runner.disks = append(runner.disks, migrationLimaDiskInventory{
				Name: name, Size: int64(len(data)), Format: "raw", Dir: diskDir,
			})
		}
	}
	return &migrationSourceFixture{
		home:   home,
		runner: runner,
		provider: Backend{
			Runner: runner,
			Migration: &MigrationOptions{
				LimaHome: home, HelperPath: helper,
				HostOS: "darwin", HostArch: "arm64", GuestArch: "arm64",
				FileCloner:              copyMigrationFixtureFile,
				adoptionIsolationProber: migrationTestAdoptionIsolation,
				sourceIdentityObserver:  migrationTestSourceIdentityObserver,
			},
		},
	}
}

func migrationTestSourceIdentityObserver(
	_ context.Context,
	_ string,
	owner migrationSnapshotOwner,
	_ migration.Digest,
) ([]backend.MigrationSourceIdentity, []migration.Digest, error) {
	identities := make([]backend.MigrationSourceIdentity, 0, len(owner.Selections))
	proofs := make([]migration.Digest, 0, len(owner.Selections))
	for _, selection := range owner.Selections {
		var root migrationSnapshotEntry
		for _, entry := range owner.Entries {
			if entry.Role == migration.DiskRoleRoot &&
				entry.SourceObject == selection.ProviderInstance {
				root = entry
				break
			}
		}
		if root.ComponentID == "" {
			return nil, nil, errors.New("test source identity root is absent")
		}
		digest := func(label string) migration.Digest {
			value := sha256.Sum256([]byte(label + "\x00" + string(selection.EnvironmentRef)))
			return migration.Digest(fmt.Sprintf("sha256:%x", value[:]))
		}
		identities = append(identities, backend.MigrationSourceIdentity{
			EnvironmentRef: selection.EnvironmentRef, RootComponent: root.ComponentID,
			Evidence: migration.GuestIdentityEvidence{
				MachineIDDigest:   digest("machine"),
				SSHHostKeyDigests: []migration.Digest{digest("ssh")},
			},
		})
		proofs = append(proofs, digest("proof"))
	}
	return identities, proofs, nil
}

func migrationTestAdoptionIsolation(
	context.Context,
	string,
	string,
) (string, error) {
	return "test-only-offline-adoption-executor-v1", nil
}

func copyMigrationFixtureFile(source, destination string) (retErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, input.Close()) }()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("migration fixture clone source is not a regular file")
	}
	output, err := os.OpenFile(
		destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm(),
	)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, output.Close()) }()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Sync()
}

func rewriteSparseMigrationFixture(path string, logicalBytes int64) (retErr error) {
	if err := os.Remove(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	if err := file.Truncate(logicalBytes); err != nil {
		return err
	}
	if _, err := file.WriteAt([]byte("alpha-data"), 0); err != nil {
		return err
	}
	if _, err := file.WriteAt([]byte("omega-data"), logicalBytes*3/4); err != nil {
		return err
	}
	return file.Sync()
}

func assertMigrationExtentCoverage(
	t *testing.T,
	extents []backend.MigrationExtent,
	start,
	end uint64,
	maxChunk uint32,
) {
	t.Helper()
	offset := start
	for index, extent := range extents {
		if extent.LogicalOffset != offset || extent.Length == 0 ||
			(extent.Kind == migration.ExtentData && extent.Length > uint64(maxChunk)) ||
			extent.Validate(maxChunk) != nil {
			t.Fatalf("extent[%d]=%+v offset=%d", index, extent, offset)
		}
		if index > 0 && extent.Kind != migration.ExtentData &&
			extent.Kind == extents[index-1].Kind {
			t.Fatalf("extent[%d] is adjacent noncanonical %s", index, extent.Kind)
		}
		offset += extent.Length
	}
	if offset != end {
		t.Fatalf("extent coverage ended at %d want=%d: %+v", offset, end, extents)
	}
}

func (fixture *migrationSourceFixture) request(
	t *testing.T,
	selections []backend.MigrationSourceSelection,
) backend.SourceInspectionRequest {
	t.Helper()
	capability, err := fixture.provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return backend.SourceInspectionRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID:        "operation_fixture1234",
			EffectID:           "effect_fixture1234",
			CapabilityRevision: capability.Revision,
		},
		Mode:       migration.ExportModeFull,
		Selections: append([]backend.MigrationSourceSelection(nil), selections...),
	}
}

func migrationFixtureTreeDigest(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00", relative, info.Mode().String(), info.Size())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, _ = hash.Write(data)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(hash, target)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func migrationReadOnlyInspectionCall(args []string) bool {
	return reflect.DeepEqual(args, []string{"--version"}) ||
		reflect.DeepEqual(args, []string{"list", "--format", "json", "--all-fields"}) ||
		reflect.DeepEqual(args, []string{"disk", "list", "--json"})
}

func migrationInventoryHasBlocker(inventory backend.SourceInventory, code string) bool {
	for _, blocker := range inventory.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func (runner *migrationCapabilityRunner) LookPath(string) (string, error) {
	if runner.lookPath == "missing" {
		return "", errors.New("missing limactl")
	}
	if runner.lookPath == "" {
		return "/opt/homebrew/bin/limactl", nil
	}
	return runner.lookPath, nil
}

func (runner *migrationCapabilityRunner) Run(
	_ context.Context,
	_ string,
	args []string,
	_ []string,
	_ io.Reader,
	stdout,
	_ io.Writer,
) error {
	runner.calls = append(runner.calls, append([]string(nil), args...))
	if runner.runErr != nil {
		return runner.runErr
	}
	if !reflect.DeepEqual(args, []string{"--version"}) {
		return errors.New("unexpected migration capability command")
	}
	_, err := io.WriteString(stdout, runner.version)
	return err
}

func privateMigrationHomeFixture(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "lima")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func migrationAdoptionHelperFixture(
	t *testing.T,
	arch,
	contents string,
) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), helperbin.LinuxMigrationAdoptCommand+"-linux-"+arch)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteLinuxMigrationAdoptManifest(path, arch); err != nil {
		t.Fatal(err)
	}
	return path
}

type migrationProductionCapabilityCall struct {
	name string
	args []string
}

type migrationProductionCapabilityRunner struct {
	version string
	probe   vzexecutor.Probe
	calls   []migrationProductionCapabilityCall
}

func (runner *migrationProductionCapabilityRunner) LookPath(
	string,
) (string, error) {
	return "/opt/homebrew/bin/limactl", nil
}

func (runner *migrationProductionCapabilityRunner) Run(
	_ context.Context,
	name string,
	args []string,
	_ []string,
	_ io.Reader,
	stdout,
	_ io.Writer,
) error {
	runner.calls = append(runner.calls, migrationProductionCapabilityCall{
		name: name, args: append([]string(nil), args...),
	})
	if reflect.DeepEqual(args, []string{"--version"}) {
		_, err := io.WriteString(stdout, runner.version)
		return err
	}
	if reflect.DeepEqual(args, []string{"--probe"}) {
		return json.NewEncoder(stdout).Encode(runner.probe)
	}
	return errors.New("unexpected production migration capability command")
}

func migrationVZAdoptionExecutorFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(
		t.TempDir(), helperbin.HostMigrationVZAdoptCommand+"-darwin-arm64",
	)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteHostMigrationVZAdoptManifest(
		path, "darwin", "arm64",
	); err != nil {
		t.Fatal(err)
	}
	return path
}
