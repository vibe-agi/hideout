package native

import (
	"context"
	"testing"
)

func TestConfigMigrationHarnessAdvertisesNoDiskAuthority(t *testing.T) {
	capability, err := (ConfigMigrationHarness{
		HostOS: "darwin", HostArch: "arm64",
	}).MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capability.Provider != "native" || capability.FullExport || capability.FullImport ||
		len(capability.DiskRepresentations) != 0 || capability.AdoptionHelper != nil {
		t.Fatalf("unexpected config-only capability: %#v", capability)
	}
}
