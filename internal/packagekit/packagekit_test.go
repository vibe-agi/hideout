package packagekit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallWritesInstalledManifestAndVerifiesPrefix(t *testing.T) {
	root := writeTestArtifact(t, nil)
	prefix := filepath.Join(t.TempDir(), "prefix with spaces")
	store := filepath.Join(t.TempDir(), "store")
	result, err := Install(InstallOptions{
		PackageRoot: root,
		Prefix:      prefix,
		StoreRoot:   store,
		Now:         time.Date(2026, 7, 9, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation != "install" {
		t.Fatalf("operation=%s", result.Operation)
	}
	verify, err := Verify(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if verify.Mode != "installed" || verify.Files == 0 {
		t.Fatalf("unexpected verify result: %+v", verify)
	}
	state := readState(t, prefix)
	if state.InstallPrefix != result.Prefix {
		t.Fatalf("install prefix not recorded: %q != %q", state.InstallPrefix, result.Prefix)
	}
	if state.StoreRoot != result.StoreRoot {
		t.Fatalf("store root not recorded: %q != %q", state.StoreRoot, result.StoreRoot)
	}
	state.InstallPrefix = filepath.Join(t.TempDir(), "moved")
	writeState(t, prefix, state)
	if _, err := Verify(prefix); err == nil || !strings.Contains(err.Error(), "prefix mismatch") {
		t.Fatalf("expected relocation mismatch, got %v", err)
	}
}

func TestUpgradeRejectsIncompatibleMigrationBeforeMutation(t *testing.T) {
	first := writeTestArtifact(t, map[string]string{"bin/hideout": "version-a\n"})
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{PackageRoot: first, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(prefix, "bin", "hideout")
	before, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	second := writeTestArtifact(t, map[string]string{"bin/hideout": "version-b\n"})
	manifest := readManifest(t, second)
	manifest.Migration.FromInstalledSchemas = []string{"hideout.package-install-state.v0"}
	writeManifest(t, second, manifest)
	if _, err := Install(InstallOptions{PackageRoot: second, Prefix: prefix, StoreRoot: store}); err == nil || !strings.Contains(err.Error(), "outside migration range") {
		t.Fatalf("expected migration failure, got %v", err)
	}
	after, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("incompatible upgrade mutated binary: before=%q after=%q", before, after)
	}
}

func TestCompatibleUpgradePreservesStoreAndUpdatesFiles(t *testing.T) {
	first := writeTestArtifact(t, map[string]string{"bin/hideout": "version-a\n"})
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{PackageRoot: first, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(store, "profiles", "default", "profile.json")
	if err := os.MkdirAll(filepath.Dir(fixture), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := writeTestArtifact(t, map[string]string{"bin/hideout": "version-b\n"})
	result, err := Install(InstallOptions{PackageRoot: second, Prefix: prefix, StoreRoot: store})
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation != "upgrade" {
		t.Fatalf("operation=%s", result.Operation)
	}
	got, err := os.ReadFile(filepath.Join(prefix, "bin", "hideout"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "version-b\n" {
		t.Fatalf("binary not upgraded: %q", got)
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("store fixture not preserved: %v", err)
	}
}

func TestUninstallDryRunPreserveAndPurge(t *testing.T) {
	root := writeTestArtifact(t, nil)
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{PackageRoot: root, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(store, "evidence", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(fixture), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(prefix, "bin", "not-hideout")
	if err := os.WriteFile(unrelated, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dry, err := Uninstall(UninstallOptions{Prefix: prefix, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || len(dry.Files) == 0 {
		t.Fatalf("unexpected dry-run result: %+v", dry)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "hideout")); err != nil {
		t.Fatalf("dry-run removed binary: %v", err)
	}
	if _, err := Uninstall(UninstallOptions{Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "hideout")); !os.IsNotExist(err) {
		t.Fatalf("package binary still exists or stat failed differently: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated prefix file removed: %v", err)
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("store fixture removed without purge: %v", err)
	}

	if _, err := Install(InstallOptions{PackageRoot: root, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(UninstallOptions{Prefix: prefix, Purge: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("store not purged: %v", err)
	}
}

func writeTestArtifact(t *testing.T, overrides map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]struct {
		kind       string
		executable bool
		data       string
	}{
		"bin/hideout":      {kind: "binary", executable: true, data: "#!/bin/sh\n"},
		"bin/hideout-shim": {kind: "binary", executable: true, data: "#!/bin/sh\n"},
		"bin/hideout-shim-linux-" + runtime.GOARCH:    {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"bin/hideout-hostfsd-linux-" + runtime.GOARCH: {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"install.sh":                           {kind: "installer", executable: true, data: "#!/bin/sh\n"},
		"README.md":                            {kind: "entrypoint", executable: false, data: "readme\n"},
		"README.zh-CN.md":                      {kind: "entrypoint", executable: false, data: "readme zh\n"},
		"schemas/package-manifest.schema.json": {kind: "schema", executable: false, data: "{}\n"},
		"schemas/release-dogfood.schema.json":  {kind: "schema", executable: false, data: "{}\n"},
		"docs/README.md":                       {kind: "doc", executable: false, data: "docs\n"},
		"docs/STATUS.md":                       {kind: "doc", executable: false, data: "status\n"},
	}
	for rel, data := range overrides {
		spec := files[rel]
		spec.data = data
		files[rel] = spec
	}
	manifest := Manifest{
		Schema:  ArtifactSchema,
		BuiltAt: "2026-07-09T00:00:00Z",
		Git:     GitInfo{Commit: "test", Dirty: false},
		Target: Target{
			HostOS:         runtime.GOOS,
			HostArch:       runtime.GOARCH,
			LinuxGuestArch: runtime.GOARCH,
		},
		Layout: Layout{
			Root: DefaultPackageRoot,
			Binaries: []string{
				"bin/hideout",
				"bin/hideout-shim",
				"bin/hideout-shim-linux-" + runtime.GOARCH,
				"bin/hideout-hostfsd-linux-" + runtime.GOARCH,
			},
			Entrypoints: []string{"install.sh", "README.md", "README.zh-CN.md"},
			Directories: []string{"schemas", "docs", "packaging"},
		},
		Migration: Migration{
			InstallStateSchema:   InstallStateSchema,
			FromInstalledSchemas: []string{InstallStateSchema},
		},
	}
	if err := os.MkdirAll(filepath.Join(root, "packaging"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, spec := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if spec.executable {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(spec.data), mode); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(spec.data))
		manifest.Files = append(manifest.Files, File{
			Path:       rel,
			Kind:       spec.kind,
			SHA256:     hex.EncodeToString(sum[:]),
			Executable: spec.executable,
		})
	}
	writeManifest(t, root, manifest)
	return root
}

func readManifest(t *testing.T, root string) Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "package-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func writeManifest(t *testing.T, root string, manifest Manifest) {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package-manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readState(t *testing.T, prefix string) InstallState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(prefix, filepath.FromSlash(InstalledManifest)))
	if err != nil {
		t.Fatal(err)
	}
	var state InstallState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func writeState(t *testing.T, prefix string, state InstallState) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefix, filepath.FromSlash(InstalledManifest)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
