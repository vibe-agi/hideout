package adapterpack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateManifestRejectsUnsupportedAuthority(t *testing.T) {
	m := validManifest()
	m.Adapters[0].AllowedProposalCapabilities = []string{"host.exec"}
	if err := ValidateManifest(m, ""); err == nil {
		t.Fatal("expected unsupported capability rejection")
	}
}

func TestValidateManifestRequiresTestsForEnableGateButAllowsInstall(t *testing.T) {
	m := validManifest()
	m.Tests = nil
	if err := ValidateManifest(m, ""); err != nil {
		t.Fatalf("installable manifest without tests should validate: %v", err)
	}
}

func TestLoadManifestChecksScriptPath(t *testing.T) {
	dir := t.TempDir()
	writePackManifest(t, dir, validManifest())
	if _, _, err := LoadManifest(filepath.Join(dir, ManifestFileName)); err == nil {
		t.Fatal("expected missing script rejection")
	}
	if err := os.WriteFile(filepath.Join(dir, "adapter.js"), []byte("function decideCommandAdapter(){return {outcome:'deny',reason:'x'}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadManifest(filepath.Join(dir, ManifestFileName)); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
}

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: ManifestVersion,
		ID:            "example.pack",
		Version:       "1.0.0",
		Adapters: []AdapterSpec{{
			ID:                          "tool",
			Script:                      "adapter.js",
			Entrypoint:                  "decideCommandAdapter",
			Commands:                    []string{"tool-x"},
			AllowedProposalCapabilities: []string{CapabilityGuestPrivilegePlan, CapabilityHostFSWritePlan},
		}},
		Tests: []TestVector{{
			ID:        "denies",
			AdapterID: "tool",
			Context: TestContext{Command: TestCommand{
				Name: "tool-x",
				Argv: []string{"tool-x", "--unsafe"},
				CWD:  "/workspace",
			}},
			Expect: TestExpect{Outcome: "deny", ReasonContains: "blocked"},
		}},
	}
}
