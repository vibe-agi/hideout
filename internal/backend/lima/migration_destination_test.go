package lima

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestInspectMigrationDestinationIsReadOnlyAndReportsRealCapacity(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request := migrationDestinationInspectionFixture(t, fixture, "inspect")
	before := migrationFixtureTreeDigest(t, fixture.home)

	first, err := fixture.provider.InspectMigrationDestination(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.provider.InspectMigrationDestination(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstStable, secondStable := first, second
	firstStable.AvailableBytes = 0
	secondStable.AvailableBytes = 0
	if !reflect.DeepEqual(firstStable, secondStable) || !first.Compatible ||
		first.AvailableBytes < request.RequiredBytes ||
		second.AvailableBytes < request.RequiredBytes || len(first.Blockers) != 0 {
		t.Fatalf("destination inventory is incomplete or unstable: first=%+v second=%+v", first, second)
	}
	if after := migrationFixtureTreeDigest(t, fixture.home); after != before {
		t.Fatalf("destination inspection mutated the Lima home: before=%s after=%s", before, after)
	}
}

func TestInspectMigrationDestinationReportsAuthenticatedCompatibilityBlockers(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	request := migrationDestinationInspectionFixture(t, fixture, "blocker")
	request.SourceProduct.HostArch = "amd64"

	inventory, err := fixture.provider.InspectMigrationDestination(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Compatible || !migrationProviderBlockerPresent(
		inventory.Blockers, "migration.provider.source_incompatible",
	) {
		t.Fatalf("incompatible source inventory=%+v", inventory)
	}
}

func migrationDestinationInspectionFixture(
	t *testing.T,
	fixture *migrationSourceFixture,
	suffix string,
) backend.DestinationInspectionRequest {
	t.Helper()
	stage, _, _ := migrationDestinationStageFixture(t, fixture, suffix)
	capability, err := fixture.provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	environments := make([]migration.OpaqueID, len(stage.Objects))
	for index, object := range stage.Objects {
		environments[index] = object.EnvironmentRef
	}
	var required uint64
	for _, disk := range stage.Disks {
		required += disk.LogicalBytes
	}
	request := backend.DestinationInspectionRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID:        migration.OpaqueID("operation_inspect_" + suffix + "1"),
			EffectID:           migration.OpaqueID("effect_inspect_" + suffix + "1"),
			CapabilityRevision: capability.Revision,
		},
		ManifestDigest: migration.Digest("sha256:" + strings.Repeat("7", 64)),
		SourceProduct: migration.SourceProduct{
			Version: "1.0.0", HostOS: "darwin", HostArch: "arm64",
			Backend: "lima", BackendVersion: "2.2.0", GuestArch: "aarch64",
		},
		EnvironmentRefs: environments, Disks: stage.Disks, Edges: stage.Edges,
		RequiredCapabilities: []migration.RequiredCapability{{
			ID: "full-state", Provider: "lima", MinimumVersion: "2.1.0",
		}},
		RequiredBytes: required + (9 << 20),
		Capacity: migration.CapacityRequirement{
			Schema: migration.CapacityRequirementSchema, BundleBytes: 4096,
			StagingBytes: required, ValidationBytes: 8 << 20,
			RollbackReserveBytes: 1 << 20, FinalBytes: required,
			PeakAdditionalBytes: required + (9 << 20),
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request
}

func migrationProviderBlockerPresent(
	blockers []backend.MigrationProviderBlocker,
	code string,
) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}
