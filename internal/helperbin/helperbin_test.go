package helperbin

import (
	"encoding/json"
	"errors"
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
		filepath.Join(root, "cmd", LinuxSessionSupervisorCommand),
		filepath.Join(root, "cmd", LinuxWorkspacePortalCommand),
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
	if got, want := DefaultLinuxSessionSupervisorPath(root, "arm64"), filepath.Join(root, "bin", "hideout-session-supervisor-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxSessionSupervisorPath=%q want %q", got, want)
	}
	if got, want := DefaultLinuxWorkspacePortalPath(root, "arm64"), filepath.Join(root, "bin", "hideout-workspace-portal-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxWorkspacePortalPath=%q want %q", got, want)
	}
}

func TestResolveLinuxWorkspacePortalRequiresExactManifestIdentity(t *testing.T) {
	t.Setenv(LinuxWorkspacePortalPathEnvironment, "")
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	path := DefaultLinuxWorkspacePortalPath(root, "arm64")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("portal-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLinuxWorkspacePortalPath(root, "arm64"); got != "" {
		t.Fatalf("workspace portal resolved without manifest: %q", got)
	}
	if err := WriteStoreHelperManifest(path, LinuxWorkspacePortalCommand, "arm64"); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLinuxWorkspacePortalPath(root, "arm64"); got != path {
		t.Fatalf("ResolveLinuxWorkspacePortalPath=%q want %q", got, path)
	}
	if err := os.WriteFile(path, []byte("replaced"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLinuxWorkspacePortalPath(root, "arm64"); got != "" {
		t.Fatalf("workspace portal resolved after digest drift: %q", got)
	}
}

func TestResolveLinuxTun2SocksUsesVerifiedPackageHelper(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "hideout")
	helper := filepath.Join(root, "bin", "tun2socks-linux-arm64")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("hideout"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("tun2socks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTun2SocksManifest(helper, "arm64", true); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
		Executable: binary, GOARCH: "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedHelper, err := filepath.EvalSymlinks(helper)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != resolvedHelper || got.Source != Tun2SocksSourcePackage ||
		!got.Manifest.PackageOwned ||
		got.Manifest.UpstreamModule != Tun2SocksUpstreamModule ||
		got.Manifest.UpstreamVersion != Tun2SocksUpstreamVersion ||
		got.Manifest.License != Tun2SocksLicense {
		t.Fatalf("unexpected package helper resolution: %+v", got)
	}
}

func TestResolveLinuxTun2SocksInvalidOverrideFailsWithoutFallback(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "hideout")
	helper := filepath.Join(root, "bin", "tun2socks-linux-arm64")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("hideout"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("packaged"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTun2SocksManifest(helper, "arm64", true); err != nil {
		t.Fatal(err)
	}

	invalid := filepath.Join(root, "invalid-override")
	if err := os.Mkdir(invalid, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
		Executable: binary, GOARCH: "arm64", Override: invalid,
	}); err == nil || got.Path != "" {
		t.Fatalf("invalid explicit override fell through: resolution=%+v err=%v", got, err)
	}
}

func TestResolveLinuxTun2SocksPackagedDamageNeverFallsBackToStore(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, helper string)
	}{
		{
			name: "missing",
			prepare: func(t *testing.T, helper string) {
				t.Helper()
			},
		},
		{
			name: "digest-mismatch",
			prepare: func(t *testing.T, helper string) {
				t.Helper()
				if err := os.WriteFile(helper, []byte("packaged"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := WriteTun2SocksManifest(helper, "arm64", true); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(helper, []byte("replaced"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong-target",
			prepare: func(t *testing.T, helper string) {
				t.Helper()
				if err := os.WriteFile(helper, []byte("packaged"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := WriteTun2SocksManifest(helper, "amd64", true); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			packageRoot := t.TempDir()
			binary := filepath.Join(packageRoot, "bin", "hideout")
			packagedHelper := filepath.Join(packageRoot, "bin", "tun2socks-linux-arm64")
			if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("hideout"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(packageRoot, "package-manifest.json"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, packagedHelper)

			store := t.TempDir()
			storeHelper := DefaultLinuxTun2SocksPath(store, "arm64")
			if err := os.MkdirAll(filepath.Dir(storeHelper), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(storeHelper, []byte("store-helper"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := WriteTun2SocksManifest(storeHelper, "arm64", false); err != nil {
				t.Fatal(err)
			}

			got, err := ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
				Executable: binary,
				StoreRoot:  store,
				GOARCH:     "arm64",
			})
			if !errors.Is(err, ErrPackagedTun2SocksUnavailable) || got.Path != "" {
				t.Fatalf("packaged %s helper fell through to store: resolution=%+v err=%v", test.name, got, err)
			}
		})
	}
}

func TestResolveLinuxTun2SocksStoreRequiresExplicitDevelopmentPermission(t *testing.T) {
	store := t.TempDir()
	helper := DefaultLinuxTun2SocksPath(store, "arm64")
	if err := os.MkdirAll(filepath.Dir(helper), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("store-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTun2SocksManifest(helper, "arm64", false); err != nil {
		t.Fatal(err)
	}
	opts := Tun2SocksResolveOptions{
		Executable: filepath.Join(t.TempDir(), "bin", "hideout"),
		StoreRoot:  store,
		GOARCH:     "arm64",
	}
	got, err := ResolveLinuxTun2Socks(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "" {
		t.Fatalf("store helper resolved without explicit development permission: %+v", got)
	}

	opts.AllowStore = true
	got, err = ResolveLinuxTun2Socks(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != helper || got.Source != Tun2SocksSourceStore || got.Manifest.PackageOwned {
		t.Fatalf("explicitly permitted development store helper did not resolve: %+v", got)
	}
}

func TestCopyVerifiedExecutableRejectsReplacementAfterResolution(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "hideout")
	helper := filepath.Join(root, "bin", "tun2socks-linux-arm64")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("hideout"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("verified-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTun2SocksManifest(helper, "arm64", true); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
		Executable: binary,
		GOARCH:     "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.ExpectedSHA256 == "" || resolution.ExpectedSHA256 != resolution.Manifest.SHA256 {
		t.Fatalf("resolution did not retain verified digest: %+v", resolution)
	}

	if err := os.WriteFile(helper, []byte("replacement-after-resolution"), 0o700); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "tun2socks")
	if err := CopyVerifiedExecutable(resolution.Path, dst, resolution.ExpectedSHA256); err == nil {
		t.Fatal("copy accepted helper bytes replaced after resolution")
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed verified copy left destination behind: %v", err)
	}
}

func TestResolveLinuxTun2SocksIgnoresAmbientPathAndRejectsDigestDrift(t *testing.T) {
	pathDir := t.TempDir()
	ambient := filepath.Join(pathDir, "tun2socks-linux-arm64")
	if err := os.WriteFile(ambient, []byte("ambient"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	got, err := ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
		Executable: filepath.Join(t.TempDir(), "bin", "hideout"),
		GOARCH:     "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "" {
		t.Fatalf("ambient PATH broadened helper resolution: %+v", got)
	}

	store := t.TempDir()
	helper := DefaultLinuxTun2SocksPath(store, "arm64")
	if err := os.MkdirAll(filepath.Dir(helper), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("store-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTun2SocksManifest(helper, "arm64", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveLinuxTun2Socks(Tun2SocksResolveOptions{
		Executable: filepath.Join(t.TempDir(), "bin", "hideout"),
		StoreRoot:  store, GOARCH: "arm64",
		AllowStore: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "" {
		t.Fatalf("digest-drifted store helper resolved: %+v", got)
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

func TestResolveLinuxSessionSupervisorRequiresMatchingManifest(t *testing.T) {
	root := t.TempDir()
	path := DefaultLinuxSessionSupervisorPath(root, "arm64")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("supervisor"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv(LinuxSessionSupervisorPathEnvironment, path)
	if got := ResolveLinuxSessionSupervisorPath(root, "arm64"); got != "" {
		t.Fatalf("resolver accepted helper without manifest: %q", got)
	}
	if err := WriteStoreHelperManifest(path, LinuxSessionSupervisorCommand, "arm64"); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLinuxSessionSupervisorPath(root, "arm64"); got != path {
		t.Fatalf("ResolveLinuxSessionSupervisorPath=%q want %q", got, path)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLinuxSessionSupervisorPath(root, "arm64"); got != "" {
		t.Fatalf("resolver accepted checksum mismatch: %q", got)
	}
}

func TestResolveLinuxSessionSupervisorRejectsUnsupportedArchitectureAndHostFallback(t *testing.T) {
	root := t.TempDir()
	hostBinary := filepath.Join(root, LinuxSessionSupervisorCommand)
	if err := os.WriteFile(hostBinary, []byte("host-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv(LinuxSessionSupervisorPathEnvironment, "")
	if got := ResolveLinuxSessionSupervisorPath("", "386"); got != "" {
		t.Fatalf("unsupported architecture resolved helper %q", got)
	}
	if got := ResolveLinuxSessionSupervisorPath("", "arm64"); got != "" {
		t.Fatalf("resolver fell back to unsuffixed host binary %q", got)
	}
}

func TestBuildLinuxSessionSupervisorRejectsUnsupportedArchitecture(t *testing.T) {
	err := BuildLinuxSessionSupervisor(BuildOptions{Out: filepath.Join(t.TempDir(), "helper"), GOARCH: "386", Source: "."})
	if err == nil {
		t.Fatal("BuildLinuxSessionSupervisor accepted unsupported architecture")
	}
}

func TestBuildLinuxWorkspacePortalRejectsUnsupportedArchitecture(t *testing.T) {
	err := BuildLinuxWorkspacePortal(BuildOptions{Out: filepath.Join(t.TempDir(), "helper"), GOARCH: "386", Source: "."})
	if err == nil {
		t.Fatal("BuildLinuxWorkspacePortal accepted unsupported architecture")
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
