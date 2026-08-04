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
		filepath.Join(root, "cmd", LinuxObserverCommand),
		filepath.Join(root, "cmd", LinuxWorkspacePortalCommand),
		filepath.Join(root, "cmd", LinuxMigrationAdoptCommand),
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
	if got, want := DefaultLinuxObserverPath(root, "arm64"), filepath.Join(root, "bin", "hideout-observer-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxObserverPath=%q want %q", got, want)
	}
	if got, want := DefaultLinuxWorkspacePortalPath(root, "arm64"), filepath.Join(root, "bin", "hideout-workspace-portal-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxWorkspacePortalPath=%q want %q", got, want)
	}
	if got, want := DefaultLinuxMigrationAdoptPath(root, "arm64"), filepath.Join(root, "bin", "hideout-migration-adopt-linux-arm64"); got != want {
		t.Fatalf("DefaultLinuxMigrationAdoptPath=%q want %q", got, want)
	}
}

func TestResolveLinuxMigrationAdoptRequiresStrictPackageManifest(t *testing.T) {
	t.Setenv(LinuxMigrationAdoptPathEnvironment, "")
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	path := DefaultLinuxMigrationAdoptPath(root, "arm64")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("adoption-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStoreHelperManifest(path, LinuxMigrationAdoptCommand, "arm64"); err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveLinuxMigrationAdopt(root, "arm64"); err != nil || resolution.Path != "" {
		t.Fatalf("generic manifest resolution=%+v error=%v", resolution, err)
	}
	if err := WriteLinuxMigrationAdoptManifest(path, "arm64"); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveLinuxMigrationAdopt(root, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Path != path ||
		resolution.ExpectedDigest != "sha256:"+resolution.Manifest.SHA256 ||
		resolution.Manifest.Command != LinuxMigrationAdoptCommand ||
		resolution.Manifest.License != LinuxMigrationAdoptLicense ||
		resolution.Manifest.BuildMode != LinuxMigrationAdoptBuildMode ||
		!resolution.Manifest.PackageOwned {
		t.Fatalf("unexpected adoption resolution: %+v", resolution)
	}
	if err := os.WriteFile(path, []byte("replaced-helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveLinuxMigrationAdopt(root, "arm64"); err != nil || resolution.Path != "" {
		t.Fatalf("digest-drifted resolution=%+v error=%v", resolution, err)
	}
}

func TestResolveLinuxMigrationAdoptExplicitInvalidPathFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LinuxMigrationAdoptCommand+"-linux-arm64")
	if err := os.WriteFile(path, []byte("unmanifested"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LinuxMigrationAdoptPathEnvironment, path)
	if resolution, err := ResolveLinuxMigrationAdopt("", "arm64"); err == nil || resolution.Path != "" {
		t.Fatalf("invalid explicit resolution=%+v error=%v", resolution, err)
	}
}

func TestHostMigrationVZAdoptRequiresPinnedPackageManifest(t *testing.T) {
	root := t.TempDir()
	path := DefaultHostMigrationVZAdoptPath(root, "darwin", "arm64")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture-vz-executor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveHostMigrationVZAdopt(path, "darwin", "arm64"); err == nil || resolution.Path != "" {
		t.Fatalf("executor without manifest resolution=%+v error=%v", resolution, err)
	}
	if err := WriteHostMigrationVZAdoptManifest(path, "darwin", "arm64"); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveHostMigrationVZAdopt(path, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Path != path || resolution.ExpectedDigest == "" ||
		resolution.Manifest.UpstreamModule != HostMigrationVZAdoptUpstreamModule ||
		resolution.Manifest.UpstreamVersion != HostMigrationVZAdoptUpstreamVersion ||
		resolution.Manifest.BuildMode != HostMigrationVZAdoptBuildMode {
		t.Fatalf("executor resolution=%+v", resolution)
	}
	if err := os.WriteFile(path, []byte("replaced-vz-executor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveHostMigrationVZAdopt(path, "darwin", "arm64"); err == nil || resolution.Path != "" {
		t.Fatalf("drifted executor resolution=%+v error=%v", resolution, err)
	}
}

func TestHostMigrationVZAdoptRejectsUnsupportedPlatformAndProvenanceMutation(t *testing.T) {
	root := t.TempDir()
	path := DefaultHostMigrationVZAdoptPath(root, "darwin", "arm64")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture-vz-executor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteHostMigrationVZAdoptManifest(path, "darwin", "arm64"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveHostMigrationVZAdopt(path, "linux", "arm64"); err == nil {
		t.Fatal("Linux host accepted the Darwin VZ executor")
	}
	manifest, err := ReadManifest(ManifestPath(path))
	if err != nil {
		t.Fatal(err)
	}
	manifest.UpstreamVersion = "v0.0.0"
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(path), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := HostMigrationVZAdoptHelperCurrent(path, "darwin", "arm64"); ok {
		t.Fatal("executor with mutated upstream provenance was accepted")
	}
}

func TestLinuxMigrationAdoptManifestRejectsProvenanceMutations(t *testing.T) {
	mutations := map[string]func(*Manifest){
		"not package owned": func(manifest *Manifest) {
			manifest.PackageOwned = false
		},
		"generic builder": func(manifest *Manifest) {
			manifest.Builder = "go build"
		},
		"generic build mode": func(manifest *Manifest) {
			manifest.BuildMode = "generic"
		},
		"wrong license": func(manifest *Manifest) {
			manifest.License = "MIT"
		},
		"unexpected upstream": func(manifest *Manifest) {
			manifest.UpstreamModule = "example.invalid/helper"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := DefaultLinuxMigrationAdoptPath(root, "arm64")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("adoption-helper"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := WriteLinuxMigrationAdoptManifest(path, "arm64"); err != nil {
				t.Fatal(err)
			}
			manifest, err := ReadManifest(ManifestPath(path))
			if err != nil {
				t.Fatal(err)
			}
			mutate(&manifest)
			data, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ManifestPath(path), append(data, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok := LinuxMigrationAdoptHelperCurrent(path, "arm64"); ok {
				t.Fatal("mutated adoption helper manifest was accepted")
			}
		})
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

func TestHelperManifestSourceDateEpochIsDeterministic(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1785456000")
	root := t.TempDir()
	for _, test := range []struct {
		name  string
		write func(string) error
	}{
		{
			name: "store",
			write: func(path string) error {
				return WriteStoreHelperManifest(
					path,
					LinuxSessionSupervisorCommand,
					"arm64",
				)
			},
		},
		{
			name: "observer",
			write: func(path string) error {
				return WriteLinuxObserverManifest(path, "arm64")
			},
		},
		{
			name: "migration-adopt",
			write: func(path string) error {
				return WriteLinuxMigrationAdoptManifest(path, "arm64")
			},
		},
		{
			name: "tun2socks",
			write: func(path string) error {
				return WriteTun2SocksManifest(path, "arm64", true)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, test.name)
			if err := os.WriteFile(path, []byte(test.name), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := test.write(path); err != nil {
				t.Fatal(err)
			}
			manifest, err := ReadManifest(ManifestPath(path))
			if err != nil {
				t.Fatal(err)
			}
			if want := "2026-07-31T00:00:00Z"; manifest.BuiltAt != want {
				t.Fatalf("BuiltAt=%q want %q", manifest.BuiltAt, want)
			}
		})
	}
}

func TestHelperManifestRejectsInvalidSourceDateEpoch(t *testing.T) {
	for _, value := range []string{
		"",
		"-1",
		"1x",
		" 1",
		"9223372036854775807",
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", value)
			path := filepath.Join(t.TempDir(), "helper")
			if err := os.WriteFile(path, []byte("helper"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := WriteStoreHelperManifest(
				path,
				LinuxSessionSupervisorCommand,
				"arm64",
			); err == nil {
				t.Fatalf("accepted SOURCE_DATE_EPOCH=%q", value)
			}
			if _, err := os.Lstat(ManifestPath(path)); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatalf("manifest written after invalid epoch: %v", err)
			}
		})
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

func TestResolveLinuxObserverRequiresLicensedManifestAndReturnsPrefixedDigest(t *testing.T) {
	root := t.TempDir()
	path := DefaultLinuxObserverPath(root, "arm64")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("observer"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv(LinuxObserverPathEnvironment, "")
	if resolution, err := ResolveLinuxObserver(root, "arm64"); err != nil || resolution.Path != "" {
		t.Fatalf("observer without manifest resolution=%+v error=%v", resolution, err)
	}
	if err := WriteStoreHelperManifest(path, LinuxObserverCommand, "arm64"); err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveLinuxObserver(root, "arm64"); err != nil || resolution.Path != "" {
		t.Fatalf("observer with generic manifest resolution=%+v error=%v", resolution, err)
	}
	if err := WriteLinuxObserverManifest(path, "arm64"); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveLinuxObserver(root, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Path != path ||
		resolution.ExpectedDigest != "sha256:"+resolution.Manifest.SHA256 ||
		resolution.Manifest.License != LinuxObserverLicense ||
		!resolution.Manifest.PackageOwned {
		t.Fatalf("observer resolution=%+v", resolution)
	}
	if err := os.WriteFile(path, []byte("replaced"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveLinuxObserver(root, "arm64"); err != nil || resolution.Path != "" {
		t.Fatalf("drifted observer resolution=%+v error=%v", resolution, err)
	}
}

func TestExplicitLinuxObserverPathFailsClosedOnManifestDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hideout-observer-linux-arm64")
	if err := os.WriteFile(path, []byte("observer"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LinuxObserverPathEnvironment, path)
	if _, err := ResolveLinuxObserver("", "arm64"); err == nil {
		t.Fatal("explicit observer without a manifest was accepted")
	}
	if err := WriteLinuxObserverManifest(path, "arm64"); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveLinuxObserver("", "arm64")
	if err != nil || resolution.Path != path {
		t.Fatalf("explicit observer resolution=%+v error=%v", resolution, err)
	}
	manifest, err := ReadManifest(ManifestPath(path))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Builder = "unknown builder"
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(path), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLinuxObserver("", "arm64"); err == nil {
		t.Fatal("explicit observer with untrusted builder identity was accepted")
	}
}

func TestBuildLinuxSessionSupervisorRejectsUnsupportedArchitecture(t *testing.T) {
	err := BuildLinuxSessionSupervisor(BuildOptions{Out: filepath.Join(t.TempDir(), "helper"), GOARCH: "386", Source: "."})
	if err == nil {
		t.Fatal("BuildLinuxSessionSupervisor accepted unsupported architecture")
	}
}

func TestBuildLinuxObserverRejectsUnsupportedArchitecture(t *testing.T) {
	err := BuildLinuxObserver(BuildOptions{
		Out: filepath.Join(t.TempDir(), "helper"), GOARCH: "386", Source: ".",
	})
	if err == nil {
		t.Fatal("BuildLinuxObserver accepted unsupported architecture")
	}
}

func TestBuildLinuxWorkspacePortalRejectsUnsupportedArchitecture(t *testing.T) {
	err := BuildLinuxWorkspacePortal(BuildOptions{Out: filepath.Join(t.TempDir(), "helper"), GOARCH: "386", Source: "."})
	if err == nil {
		t.Fatal("BuildLinuxWorkspacePortal accepted unsupported architecture")
	}
}

func TestBuildLinuxMigrationAdoptRejectsUnsupportedArchitecture(t *testing.T) {
	err := BuildLinuxMigrationAdopt(BuildOptions{
		Out: filepath.Join(t.TempDir(), "helper"), GOARCH: "386", Source: ".",
	})
	if err == nil {
		t.Fatal("BuildLinuxMigrationAdopt accepted unsupported architecture")
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

func TestReadManifestRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for name, data := range map[string]string{
		"unknown-field":  `{"unknown":true}`,
		"trailing-value": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "helper.manifest.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadManifest(path); err == nil {
				t.Fatalf("ReadManifest accepted %s", name)
			}
		})
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
