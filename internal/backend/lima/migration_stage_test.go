package lima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"gopkg.in/yaml.v3"
)

func TestStageMigrationDestinationMaterializesBoundConfigAndResumesByComponent(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request, streams, calls := migrationDestinationStageFixture(t, fixture, "first")

	stage, err := fixture.provider.StageMigrationDestination(context.Background(), request)
	if err != nil {
		var providerErr *backend.MigrationProviderError
		if errors.As(err, &providerErr) {
			t.Fatalf("stage error=%v cause=%v", err, providerErr.Cause)
		}
		t.Fatal(err)
	}
	if err := stage.Validate(); err != nil {
		t.Fatal(err)
	}
	if !stage.Stopped || stage.Runnable || len(stage.Checkpoints) != len(request.Components) ||
		*calls != len(request.Components) {
		t.Fatalf("unexpected stage result=%+v reads=%d", stage, *calls)
	}
	for index, component := range request.Components {
		checkpoint := stage.Checkpoints[index]
		if checkpoint.ComponentID != component.ComponentID ||
			checkpoint.NextOffset != component.LogicalBytes ||
			checkpoint.ContentDigest != component.ContentDigest {
			t.Fatalf("checkpoint[%d]=%+v component=%+v", index, checkpoint, component)
		}
	}

	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(request.StagingHandle),
	)
	before := migrationFixtureTreeDigest(t, stageDir)
	for _, object := range request.Objects {
		if _, err := os.Lstat(filepath.Join(fixture.home, string(object.BackendIdentity))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("staged instance became a top-level Lima object: %v", err)
		}
		configPath := filepath.Join(
			stageDir, "instances", string(object.BackendIdentity), "lima.yaml",
		)
		config, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		rendered := string(config)
		for _, forbidden := range []string{
			"hideout-source", "base:", "provision:", "env:",
		} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("normalized config retained forbidden source authority %q:\n%s", forbidden, rendered)
			}
		}
		for _, required := range []string{
			"vmType: vz", "arch: aarch64", "images:", "mounts: []", "additionalDisks:",
		} {
			if !strings.Contains(rendered, required) {
				t.Fatalf("normalized config lacks %q:\n%s", required, rendered)
			}
		}
		var stagedConfig migrationStagedLimaConfig
		if err := yaml.Unmarshal(config, &stagedConfig); err != nil {
			t.Fatal(err)
		}
		if len(stagedConfig.Images) != 1 ||
			stagedConfig.Images[0].Location != migrationImportedRootImageSentinel ||
			stagedConfig.Images[0].Arch != "aarch64" ||
			stagedConfig.Images[0].Digest != "" {
			t.Fatalf("normalized config has unsafe root-image fallback: %+v", stagedConfig.Images)
		}
	}

	for _, stream := range streams {
		if _, err := os.Lstat(stream.sourcePath(stageDir, request)); err != nil {
			t.Fatalf("materialized component %s: %v", stream.componentID, err)
		}
	}
	retry, err := fixture.provider.StageMigrationDestination(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stage.ObjectHandles, retry.ObjectHandles) ||
		!slices.Equal(stage.Checkpoints, retry.Checkpoints) || *calls != len(request.Components) {
		t.Fatalf("stage retry repeated effects: first=%+v retry=%+v reads=%d", stage, retry, *calls)
	}
	if after := migrationFixtureTreeDigest(t, stageDir); after != before {
		t.Fatalf("idempotent stage retry changed durable state: before=%s after=%s", before, after)
	}
}

func TestStageMigrationDestinationRejectsDigestMismatchAndRecoversExactPartial(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request, streams, calls := migrationDestinationStageFixture(t, fixture, "digest")
	original := request.ReadComponent
	request.ReadComponent = func(
		ctx context.Context,
		componentID migration.OpaqueID,
		resume uint64,
		maximum uint32,
		emit func(backend.MigrationExtent) error,
	) error {
		return original(ctx, componentID, resume, maximum, func(extent backend.MigrationExtent) error {
			if extent.Kind == migration.ExtentData && len(extent.Data) != 0 {
				extent.Data = append([]byte(nil), extent.Data...)
				extent.Data[0] ^= 0xff
			}
			return emit(extent)
		})
	}
	_, err := fixture.provider.StageMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.stage_component_invalid" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("digest mismatch error=%v", err)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(request.StagingHandle),
	)
	if _, err := os.Lstat(filepath.Join(stageDir, "complete.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("digest-mismatched stage was marked complete: %v", err)
	}

	request.ReadComponent = original
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), request)
	if err != nil {
		var providerErr *backend.MigrationProviderError
		if errors.As(err, &providerErr) {
			t.Fatalf("recovery error=%v cause=%v", err, providerErr.Cause)
		}
		t.Fatal(err)
	}
	if err := stage.Validate(); err != nil || *calls != len(streams)+1 {
		t.Fatalf("exact partial did not recover: stage=%+v reads=%d err=%v", stage, *calls, err)
	}
}

func TestStageMigrationDestinationRefusesChangedCompletedBytesAndOwnerBinding(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request, streams, calls := migrationDestinationStageFixture(t, fixture, "tamper")
	if _, err := fixture.provider.StageMigrationDestination(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(request.StagingHandle),
	)
	path := streams[0].sourcePath(stageDir, request)
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0x7f}, 0); err != nil {
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
	_, err = fixture.provider.StageMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.stage_component_invalid" ||
		!providerErr.RecoveryRequired || *calls != len(streams) {
		t.Fatalf("changed completed bytes error=%v reads=%d", err, *calls)
	}

	request, _, calls = migrationDestinationStageFixture(t, fixture, "owner")
	if _, err := fixture.provider.StageMigrationDestination(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Objects[0].BackendIdentity = "backend_changedowner1"
	for index := range request.Components {
		if request.Components[index].DiskID == "disk_rootalpha1" {
			request.Components[index].BackendIdentity = request.Objects[0].BackendIdentity
		}
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.provider.StageMigrationDestination(context.Background(), request)
	providerErr = nil
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.stage_ownership_unproved" ||
		!providerErr.RecoveryRequired || *calls != len(streams) {
		t.Fatalf("changed owner binding error=%v reads=%d", err, *calls)
	}

	request, _, calls = migrationDestinationStageFixture(t, fixture, "missing")
	if _, err := fixture.provider.StageMigrationDestination(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stageDir = filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(request.StagingHandle),
	)
	missingConfig := filepath.Join(
		stageDir, "instances", string(request.Objects[0].BackendIdentity), "lima.yaml",
	)
	if err := os.Remove(missingConfig); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.provider.StageMigrationDestination(context.Background(), request)
	providerErr = nil
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.stage_config_invalid" ||
		!providerErr.RecoveryRequired || *calls != len(streams) {
		t.Fatalf("missing completed config error=%v reads=%d", err, *calls)
	}
	if _, err := os.Lstat(missingConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed-stage verification recreated missing config: %v", err)
	}
}

func TestStageMigrationDestinationSamePayloadCreatesDistinctImportObjects(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	firstRequest, _, _ := migrationDestinationStageFixture(t, fixture, "multi_a")
	secondRequest, _, _ := migrationDestinationStageFixture(t, fixture, "multi_b")
	first, err := fixture.provider.StageMigrationDestination(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.provider.StageMigrationDestination(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[migration.OpaqueID]struct{}, len(first.ObjectHandles))
	for _, handle := range first.ObjectHandles {
		seen[handle] = struct{}{}
	}
	for _, handle := range second.ObjectHandles {
		if _, exists := seen[handle]; exists {
			t.Fatalf("separate imports reused provider object identity %s", handle)
		}
	}
	if first.StageHandle == second.StageHandle {
		t.Fatal("separate imports reused a stage handle")
	}
}

func TestRollbackMigrationDestinationRemovesOnlyProvedStageAndIsIdempotent(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request, _, _ := migrationDestinationStageFixture(t, fixture, "rollback")
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	sourceDir := filepath.Join(fixture.home, "hideout-source")
	sourceBefore := migrationFixtureTreeDigest(t, sourceDir)

	liveInstance := filepath.Join(fixture.home, string(request.Objects[0].BackendIdentity))
	if err := os.Mkdir(liveInstance, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveInstance, "sentinel"), []byte("live-instance"), 0o600); err != nil {
		t.Fatal(err)
	}
	disksRoot := filepath.Join(fixture.home, "_disks")
	if err := os.Mkdir(disksRoot, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	liveDisk := filepath.Join(
		disksRoot, string(migrationDestinationDiskHandle(request, "disk_attached1")),
	)
	if err := os.Mkdir(liveDisk, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDisk, "sentinel"), []byte("live-disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	liveInstanceBefore := migrationFixtureTreeDigest(t, liveInstance)
	liveDiskBefore := migrationFixtureTreeDigest(t, liveDisk)

	rollback := migrationDestinationRollbackFixture(request.Binding, stage)
	if err := fixture.provider.RollbackMigrationDestination(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned stage still exists after rollback: %v", err)
	}
	if after := migrationFixtureTreeDigest(t, sourceDir); after != sourceBefore {
		t.Fatalf("rollback changed source tree: before=%s after=%s", sourceBefore, after)
	}
	if after := migrationFixtureTreeDigest(t, liveInstance); after != liveInstanceBefore {
		t.Fatalf("rollback changed top-level instance: before=%s after=%s", liveInstanceBefore, after)
	}
	if after := migrationFixtureTreeDigest(t, liveDisk); after != liveDiskBefore {
		t.Fatalf("rollback changed top-level disk: before=%s after=%s", liveDiskBefore, after)
	}
	if err := fixture.provider.RollbackMigrationDestination(context.Background(), rollback); err != nil {
		t.Fatalf("idempotent rollback failed: %v", err)
	}
}

func TestRollbackMigrationDestinationRejectsBindingAndHandleSubstitution(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request, _, _ := migrationDestinationStageFixture(t, fixture, "ownership")
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	before := migrationFixtureTreeDigest(t, stageDir)

	bindingChanged := migrationDestinationRollbackFixture(request.Binding, stage)
	bindingChanged.Binding.EffectID = "effect_rollbackchanged1"
	assertMigrationRollbackRejected(t, fixture.provider, bindingChanged)
	if after := migrationFixtureTreeDigest(t, stageDir); after != before {
		t.Fatalf("binding substitution changed stage: before=%s after=%s", before, after)
	}

	handleChanged := migrationDestinationRollbackFixture(request.Binding, stage)
	handleChanged.ObjectHandles[0] = "backend_substituted1"
	slices.Sort(handleChanged.ObjectHandles)
	if err := handleChanged.Validate(); err != nil {
		t.Fatal(err)
	}
	assertMigrationRollbackRejected(t, fixture.provider, handleChanged)
	if after := migrationFixtureTreeDigest(t, stageDir); after != before {
		t.Fatalf("handle substitution changed stage: before=%s after=%s", before, after)
	}
}

func TestRollbackMigrationDestinationRejectsUnknownNodesAndLinksBeforeDeletion(t *testing.T) {
	t.Run("unknown-file", func(t *testing.T) {
		fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
			name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
		}})
		request, _, _ := migrationDestinationStageFixture(t, fixture, "unknown")
		stage, err := fixture.provider.StageMigrationDestination(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		stageDir := filepath.Join(
			fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
		)
		if err := os.WriteFile(filepath.Join(stageDir, "unexpected"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := migrationFixtureTreeDigest(t, stageDir)
		assertMigrationRollbackRejected(
			t, fixture.provider, migrationDestinationRollbackFixture(request.Binding, stage),
		)
		if after := migrationFixtureTreeDigest(t, stageDir); after != before {
			t.Fatalf("unknown-node rejection changed stage: before=%s after=%s", before, after)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
			name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
		}})
		request, _, _ := migrationDestinationStageFixture(t, fixture, "symlink")
		stage, err := fixture.provider.StageMigrationDestination(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		stageDir := filepath.Join(
			fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
		)
		external := filepath.Join(fixture.home, "external-sentinel")
		if err := os.WriteFile(external, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		complete := filepath.Join(stageDir, "complete.json")
		if err := os.Remove(complete); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, complete); err != nil {
			t.Fatal(err)
		}
		assertMigrationRollbackRejected(
			t, fixture.provider, migrationDestinationRollbackFixture(request.Binding, stage),
		)
		if data, err := os.ReadFile(external); err != nil || string(data) != "outside" {
			t.Fatalf("rollback followed stage symlink: data=%q err=%v", data, err)
		}
		if info, err := os.Lstat(complete); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink rejection changed stage: info=%v err=%v", info, err)
		}
	})
}

func TestRollbackMigrationDestinationRemovesExactPartialStage(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request, _, _ := migrationDestinationStageFixture(t, fixture, "partial")
	original := request.ReadComponent
	stop := errors.New("test interrupted component stream")
	request.ReadComponent = func(
		ctx context.Context,
		componentID migration.OpaqueID,
		resume uint64,
		maximum uint32,
		emit func(backend.MigrationExtent) error,
	) error {
		return original(ctx, componentID, resume, maximum, func(extent backend.MigrationExtent) error {
			if err := emit(extent); err != nil {
				return err
			}
			return stop
		})
	}
	if _, err := fixture.provider.StageMigrationDestination(context.Background(), request); err == nil {
		t.Fatal("interrupted stage unexpectedly succeeded")
	}
	owner, err := buildMigrationStageOwner(request)
	if err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(request.StagingHandle),
	)
	partial := filepath.Join(stageDir, owner.Entries[0].RelativePath+".partial")
	if _, err := os.Lstat(partial); err != nil {
		t.Fatalf("interrupted stage lacks expected partial: %v", err)
	}
	sourceDir := filepath.Join(fixture.home, "hideout-source")
	sourceBefore := migrationFixtureTreeDigest(t, sourceDir)
	rollback := backend.DestinationRollbackRequest{
		Binding: request.Binding, StageHandle: request.StagingHandle,
		ObjectHandles: append([]migration.OpaqueID(nil), owner.ObjectHandles...),
	}
	if err := fixture.provider.RollbackMigrationDestination(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial stage still exists after rollback: %v", err)
	}
	if after := migrationFixtureTreeDigest(t, sourceDir); after != sourceBefore {
		t.Fatalf("partial rollback changed source tree: before=%s after=%s", sourceBefore, after)
	}
}

func migrationDestinationRollbackFixture(
	binding backend.MigrationEffectBinding,
	stage backend.DestinationStage,
) backend.DestinationRollbackRequest {
	return backend.DestinationRollbackRequest{
		Binding: binding, StageHandle: stage.StageHandle,
		ObjectHandles: append([]migration.OpaqueID(nil), stage.ObjectHandles...),
	}
}

func assertMigrationRollbackRejected(
	t *testing.T,
	provider Backend,
	request backend.DestinationRollbackRequest,
) {
	t.Helper()
	err := provider.RollbackMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.rollback_ownership_unproved" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("rollback rejection=%v", err)
	}
}

type migrationStageStreamFixture struct {
	componentID migration.OpaqueID
	diskID      migration.OpaqueID
	extents     []backend.MigrationExtent
}

func (stream migrationStageStreamFixture) sourcePath(
	stageDir string,
	request backend.DestinationStageRequest,
) string {
	diskByID := make(map[migration.OpaqueID]migration.DiskObject, len(request.Disks))
	for _, disk := range request.Disks {
		diskByID[disk.DiskID] = disk
	}
	if diskByID[stream.diskID].Role == migration.DiskRoleAttached {
		handle := migrationDestinationDiskHandle(request, stream.diskID)
		return filepath.Join(stageDir, "disks", string(handle), "datadisk")
	}
	for _, edge := range request.Edges {
		if edge.DiskID == stream.diskID {
			for _, object := range request.Objects {
				if object.EnvironmentRef == edge.EnvironmentRef {
					return filepath.Join(
						stageDir, "instances", string(object.BackendIdentity), "disk",
					)
				}
			}
		}
	}
	return ""
}

func migrationDestinationStageFixture(
	t *testing.T,
	fixture *migrationSourceFixture,
	suffix string,
) (backend.DestinationStageRequest, []migrationStageStreamFixture, *int) {
	t.Helper()
	capability, err := fixture.provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	streams := []migrationStageStreamFixture{
		{
			componentID: "component_attached1", diskID: "disk_attached1",
			extents: []backend.MigrationExtent{{
				Kind: migration.ExtentZero, LogicalOffset: 0, Length: 8192,
			}},
		},
		{
			componentID: "component_rootalpha1", diskID: "disk_rootalpha1",
			extents: []backend.MigrationExtent{
				{Kind: migration.ExtentData, LogicalOffset: 0, Length: 4, Data: []byte("root")},
				{Kind: migration.ExtentHole, LogicalOffset: 4, Length: 8188},
			},
		},
		{
			componentID: "component_rootbravo1", diskID: "disk_rootbravo1",
			extents: []backend.MigrationExtent{
				{Kind: migration.ExtentData, LogicalOffset: 0, Length: 5, Data: []byte("bravo")},
				{Kind: migration.ExtentHole, LogicalOffset: 5, Length: 8187},
			},
		},
	}
	disks := make([]migration.DiskObject, 0, len(streams))
	components := make([]backend.MigrationDestinationComponent, 0, len(streams))
	for _, stream := range streams {
		digester, err := migration.NewLogicalDigester(8192)
		if err != nil {
			t.Fatal(err)
		}
		for _, extent := range stream.extents {
			if err := digester.WriteExtent(migration.Extent{
				Kind: extent.Kind, LogicalOffset: extent.LogicalOffset,
				Length: extent.Length, Data: extent.Data,
			}); err != nil {
				t.Fatal(err)
			}
		}
		digest, err := digester.Finish()
		if err != nil {
			t.Fatal(err)
		}
		role, kind := migration.DiskRoleRoot, "lima-root"
		backendIdentity := migration.OpaqueID("backend_alpha" + suffix)
		if stream.diskID == "disk_attached1" {
			role, kind = migration.DiskRoleAttached, "lima-additional"
			backendIdentity = migration.OpaqueID("disk_shared" + suffix)
		} else if stream.diskID == "disk_rootbravo1" {
			backendIdentity = migration.OpaqueID("backend_bravo" + suffix)
		}
		disks = append(disks, migration.DiskObject{
			DiskID: stream.diskID, Role: role, Format: "raw", LogicalBytes: 8192,
			AllocatedBytesHint: 4096, ContentDigest: digest,
			Provider: migration.ProviderDiskFacts{
				Name: "source-" + string(stream.diskID), Kind: kind,
			},
		})
		components = append(components, backend.MigrationDestinationComponent{
			ComponentID: stream.componentID, DiskID: stream.diskID,
			BackendIdentity: backendIdentity, Kind: "disk",
			LogicalBytes: 8192, ContentDigest: digest,
		})
	}
	readCalls := 0
	request := backend.DestinationStageRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID:        "operation_stage" + migration.OpaqueID(suffix) + "1",
			EffectID:           "effect_stage" + migration.OpaqueID(suffix) + "1",
			CapabilityRevision: capability.Revision,
		},
		StagingHandle: "stage_handle" + migration.OpaqueID(suffix) + "1",
		Objects: []backend.MigrationDestinationObject{
			{
				EnvironmentRef: "environment_alpha1", BackendIdentity: "backend_alpha" + migration.OpaqueID(suffix),
				Runtime: "linux", GuestArchitecture: "linux/arm64", GuestUser: "developer",
				ProfileComponent: "profile_alpha1",
			},
			{
				EnvironmentRef: "environment_bravo1", BackendIdentity: "backend_bravo" + migration.OpaqueID(suffix),
				Runtime: "linux", GuestArchitecture: "linux/arm64", GuestUser: "developer",
				ProfileComponent: "profile_bravo1",
			},
		},
		Disks: disks,
		Edges: []migration.DiskEdge{
			{EnvironmentRef: "environment_alpha1", DiskID: "disk_attached1", Attachment: migration.DiskRoleAttached, GuestPath: "/mnt/shared"},
			{EnvironmentRef: "environment_alpha1", DiskID: "disk_rootalpha1", Attachment: migration.DiskRoleRoot, GuestPath: "/"},
			{EnvironmentRef: "environment_bravo1", DiskID: "disk_attached1", Attachment: migration.DiskRoleAttached, GuestPath: "/mnt/shared"},
			{EnvironmentRef: "environment_bravo1", DiskID: "disk_rootbravo1", Attachment: migration.DiskRoleRoot, GuestPath: "/"},
		},
		Components: components,
	}
	request.ReadComponent = func(
		ctx context.Context,
		componentID migration.OpaqueID,
		resume uint64,
		maximum uint32,
		emit func(backend.MigrationExtent) error,
	) error {
		readCalls++
		if resume != 0 || maximum == 0 || maximum > migration.HardMaxChunkBytes {
			return errors.New("invalid stage reader request")
		}
		for _, stream := range streams {
			if stream.componentID != componentID {
				continue
			}
			for _, extent := range stream.extents {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := emit(extent); err != nil {
					return err
				}
			}
			return nil
		}
		return errors.New("unknown component")
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request, streams, &readCalls
}
