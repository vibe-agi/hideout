package helperbin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFindSourceRootWalksUpFromPackageDirectory(t *testing.T) {
	root, err := FindSourceRoot(".")
	if err != nil {
		t.Fatalf("FindSourceRoot: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "go.mod"),
		filepath.Join(root, "cmd", "hideout-shim"),
		filepath.Join(root, "cmd", "hideout-hostfsd"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("source root %q missing %s: %v", root, path, err)
		}
	}
}

func TestDefaultLinuxHelperPathsUseStoreBin(t *testing.T) {
	root := t.TempDir()
	if got, want := DefaultLinuxShimPath(root, "arm64"), filepath.Join(root, "bin", "hideout-shim-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxShimPath=%q want %q", got, want)
	}
	if got, want := DefaultLinuxHostFSDPath(root, "arm64"), filepath.Join(root, "bin", "hideout-hostfsd-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxHostFSDPath=%q want %q", got, want)
	}
}

func TestStoreHelperManifestCurrent(t *testing.T) {
	root := t.TempDir()
	path := DefaultLinuxShimPath(root, "unitarch")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("helper-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if StoreHelperCurrent(path, "hideout-shim", "unitarch") {
		t.Fatal("helper without manifest should not be current")
	}
	if err := WriteStoreHelperManifest(path, "hideout-shim", "unitarch"); err != nil {
		t.Fatal(err)
	}
	if !StoreHelperCurrent(path, "hideout-shim", "unitarch") {
		t.Fatal("helper with matching manifest should be current")
	}
	manifest, err := ReadManifest(ManifestPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ManifestVersion ||
		manifest.Command != "hideout-shim" ||
		manifest.TargetOS != "linux" ||
		manifest.TargetArch != "unitarch" ||
		manifest.Artifact != filepath.Base(path) ||
		manifest.SHA256 == "" {
		t.Fatalf("manifest mismatch: %+v", manifest)
	}
	if err := os.WriteFile(path, []byte("changed-helper-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if StoreHelperCurrent(path, "hideout-shim", "unitarch") {
		t.Fatal("helper should not be current after binary changes")
	}
}

func TestResolveLinuxStoreHelpersRequireCurrentManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	shim := DefaultLinuxShimPath(root, "unitarch")
	hostfsd := DefaultLinuxHostFSDPath(root, "unitarch")
	for _, path := range []string{shim, hostfsd} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got := ResolveLinuxShimPath(root, "unitarch"); got != "" {
		t.Fatalf("ResolveLinuxShimPath without manifest=%q want empty", got)
	}
	if got := ResolveLinuxHostFSDPath(root, "unitarch"); got != "" {
		t.Fatalf("ResolveLinuxHostFSDPath without manifest=%q want empty", got)
	}
	if err := WriteStoreHelperManifest(shim, "hideout-shim", "unitarch"); err != nil {
		t.Fatal(err)
	}
	if err := WriteStoreHelperManifest(hostfsd, "hideout-hostfsd", "unitarch"); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLinuxShimPath(root, "unitarch"); got != shim {
		t.Fatalf("ResolveLinuxShimPath=%q want %q", got, shim)
	}
	if got := ResolveLinuxHostFSDPath(root, "unitarch"); got != hostfsd {
		t.Fatalf("ResolveLinuxHostFSDPath=%q want %q", got, hostfsd)
	}
}

func TestExplicitLinuxHelperEnvBypassesStoreManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dev-helper")
	if err := os.WriteFile(path, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", path)
	if got := ResolveLinuxShimPath(t.TempDir(), "unitarch"); got != path {
		t.Fatalf("ResolveLinuxShimPath explicit env=%q want %q", got, path)
	}
}

func TestReadManifestRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.manifest.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(path); err == nil {
		t.Fatal("ReadManifest should reject invalid JSON")
	}
}

func TestManifestSchemaIsStableJSON(t *testing.T) {
	root := t.TempDir()
	path := DefaultLinuxHostFSDPath(root, "unitarch")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hostfsd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStoreHelperManifest(path, "hideout-hostfsd", "unitarch"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ManifestPath(path))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "command", "targetOS", "targetArch", "artifact", "sha256", "builder", "builtAt"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("manifest missing key %q: %s", key, data)
		}
	}
}
