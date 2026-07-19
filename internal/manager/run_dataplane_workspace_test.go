package manager

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/helperbin"
)

func TestMaterializeWorkspacePortalRequiresManifestVerifiedProductionHelper(t *testing.T) {
	source := filepath.Join(t.TempDir(), helperbin.LinuxWorkspacePortalCommand+"-linux-"+runtime.GOARCH)
	if err := os.WriteFile(source, []byte("portal-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv(helperbin.LinuxWorkspacePortalPathEnvironment, source)

	destination := t.TempDir()
	err := MaterializeWorkspacePortal(destination, "lima", true)
	if err == nil || !strings.Contains(err.Error(), "manifest-verified") {
		t.Fatalf("MaterializeWorkspacePortal without manifest error=%v", err)
	}
	if err := helperbin.WriteStoreHelperManifest(source, helperbin.LinuxWorkspacePortalCommand, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeWorkspacePortal(destination, "lima", true); err != nil {
		t.Fatalf("MaterializeWorkspacePortal: %v", err)
	}
	materialized := filepath.Join(destination, helperbin.LinuxWorkspacePortalCommand)
	data, err := os.ReadFile(materialized)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "portal-helper" {
		t.Fatalf("materialized portal=%q", data)
	}
	info, err := os.Stat(materialized)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("materialized mode=%#o want 0700", info.Mode().Perm())
	}
}

func TestMaterializeWorkspacePortalIsScopedToSharedLimaSessions(t *testing.T) {
	for _, test := range []struct {
		name     string
		backend  string
		required bool
	}{
		{name: "static-lima", backend: "lima", required: false},
		{name: "native", backend: "native", required: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			destination := t.TempDir()
			if err := MaterializeWorkspacePortal(destination, test.backend, test.required); err != nil {
				t.Fatalf("MaterializeWorkspacePortal: %v", err)
			}
			if _, err := os.Stat(filepath.Join(destination, helperbin.LinuxWorkspacePortalCommand)); !os.IsNotExist(err) {
				t.Fatalf("unexpected portal materialization: %v", err)
			}
		})
	}
}
