package packagekit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
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

func TestWorkspacePortalHelperIsVerifiedRepairedAndUninstalled(t *testing.T) {
	root := writeTestArtifact(t, nil)
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{PackageRoot: root, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	portal := filepath.Join(prefix, "bin", helperbin.LinuxWorkspacePortalCommand+"-linux-"+runtime.GOARCH)
	if !helperbin.StoreHelperCurrent(portal, helperbin.LinuxWorkspacePortalCommand, runtime.GOARCH) {
		t.Fatal("installed workspace Portal helper identity is not current")
	}
	if err := os.Remove(portal); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(prefix); err == nil {
		t.Fatal("installed package verification accepted a missing workspace Portal helper")
	}
	if _, err := Install(InstallOptions{PackageRoot: root, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatalf("reinstall did not repair workspace Portal helper: %v", err)
	}
	if _, err := Verify(prefix); err != nil {
		t.Fatalf("repaired package verification: %v", err)
	}
	if _, err := Uninstall(UninstallOptions{Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{portal, helperbin.ManifestPath(portal)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall retained workspace Portal asset %q: %v", path, err)
		}
	}
}

func TestPackageVerificationRejectsSelfConsistentOuterManifestWithInvalidPortalIdentity(t *testing.T) {
	rel := "bin/" + helperbin.LinuxWorkspacePortalCommand + "-linux-" + runtime.GOARCH + ".manifest.json"
	root := writeTestArtifact(t, map[string]string{rel: "{}\n"})
	if _, err := Verify(root); err == nil || !strings.Contains(err.Error(), "workspace-portal") {
		t.Fatalf("Verify invalid nested Portal identity error=%v", err)
	}
}

func TestPackageVerificationRejectsSelfConsistentOuterManifestWithInvalidObserverIdentity(t *testing.T) {
	rel := "bin/" + helperbin.LinuxObserverCommand + "-linux-" +
		runtime.GOARCH + ".manifest.json"
	root := writeTestArtifact(t, map[string]string{rel: "{}\n"})
	if _, err := Verify(root); err == nil ||
		!strings.Contains(err.Error(), "hideout-observer") {
		t.Fatalf("Verify invalid nested observer identity error=%v", err)
	}
}

func TestPackageVerificationRejectsInvalidEmbeddedBrowserManifest(t *testing.T) {
	root := writeTestArtifact(t, map[string]string{
		BrowserConsoleManifestPath: "{}\n",
	})
	if _, err := Verify(root); err == nil ||
		!strings.Contains(err.Error(), "embedded asset manifest") {
		t.Fatalf("Verify invalid embedded asset manifest error=%v", err)
	}
}

func TestPackageVerificationRejectsDriftedComponentContract(t *testing.T) {
	root := writeTestArtifact(t, map[string]string{
		PackageComponentContractPath: "{}\n",
	})
	if _, err := Verify(root); err == nil ||
		!strings.Contains(err.Error(), "component contract") {
		t.Fatalf("Verify invalid package component contract error=%v", err)
	}
}

func TestPackageVerificationRejectsSelfConsistentWrongBrowserContainerDigest(t *testing.T) {
	root := writeTestArtifact(t, nil)
	assetPath := filepath.Join(
		root,
		filepath.FromSlash(BrowserConsoleManifestPath),
	)
	assetManifest, err := LoadEmbeddedAssetManifest(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	assetManifest.ContainerSHA256 = strings.Repeat("f", 64)
	assetData, err := json.MarshalIndent(assetManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	assetData = append(assetData, '\n')
	if err := os.WriteFile(assetPath, assetData, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := readManifest(t, root)
	assetSHA256 := BytesSHA256(assetData)
	for index := range manifest.Files {
		if manifest.Files[index].Path == BrowserConsoleManifestPath {
			manifest.Files[index].SHA256 = assetSHA256
		}
	}
	manifest.EmbeddedAssets[0].ManifestSHA256 = assetSHA256
	writeManifest(t, root, manifest)
	if _, err := Verify(root); err == nil ||
		!strings.Contains(err.Error(), "container digest mismatch") {
		t.Fatalf("Verify wrong embedded container digest error=%v", err)
	}
}

func TestInstalledPackageVerifiesObserverAndEmbeddedBrowserManifest(t *testing.T) {
	root := writeTestArtifact(t, nil)
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{
		PackageRoot: root,
		Prefix:      prefix,
		StoreRoot:   store,
	}); err != nil {
		t.Fatal(err)
	}
	observer := filepath.Join(
		prefix,
		"bin",
		helperbin.LinuxObserverCommand+"-linux-"+runtime.GOARCH,
	)
	if _, ok := helperbin.LinuxObserverHelperCurrent(
		observer,
		runtime.GOARCH,
	); !ok {
		t.Fatal("installed observer helper identity is not current")
	}
	if _, err := os.Stat(filepath.Join(
		prefix,
		filepath.FromSlash("share/hideout/"+BrowserConsoleManifestPath),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(prefix); err != nil {
		t.Fatalf("Verify installed package: %v", err)
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

func TestInstallRejectsSemanticDowngradeBeforeMutation(t *testing.T) {
	first := writeTestArtifact(t, map[string]string{"bin/hideout": "version-new\n"})
	firstManifest := readManifest(t, first)
	firstManifest.Release = ReleaseInfo{ProductVersion: "0.1.0-alpha.10", Channel: "developer-preview", Tag: "v0.1.0-alpha.10"}
	writeManifest(t, first, firstManifest)
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

	older := writeTestArtifact(t, map[string]string{"bin/hideout": "version-old\n"})
	olderManifest := readManifest(t, older)
	olderManifest.Release = ReleaseInfo{ProductVersion: "0.1.0-alpha.2", Channel: "developer-preview", Tag: "v0.1.0-alpha.2"}
	writeManifest(t, older, olderManifest)
	if _, err := Install(InstallOptions{PackageRoot: older, Prefix: prefix, StoreRoot: store}); err == nil || !strings.Contains(err.Error(), "unsupported package downgrade") {
		t.Fatalf("expected semantic downgrade failure, got %v", err)
	}
	after, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("downgrade mutated binary: before=%q after=%q", before, after)
	}
}

func TestInstallRejectsDifferentIdentityAtSameVersionBeforeMutation(t *testing.T) {
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

	conflict := writeTestArtifact(t, map[string]string{"bin/hideout": "version-b\n"})
	manifest := readManifest(t, conflict)
	manifest.Source.Commit = "fedcba9876543210fedcba9876543210fedcba98"
	writeManifest(t, conflict, manifest)
	if _, err := Install(InstallOptions{PackageRoot: conflict, Prefix: prefix, StoreRoot: store}); err == nil || !strings.Contains(err.Error(), "same-version package identity differs") {
		t.Fatalf("expected same-version identity failure, got %v", err)
	}
	after, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("identity mismatch mutated binary: before=%q after=%q", before, after)
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

func TestUpgradeReportsObsoleteFilesAndRepairRemovesOnlyThose(t *testing.T) {
	first := writeTestArtifact(t, nil)
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{PackageRoot: first, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	obsoletePath := filepath.Join(prefix, "share", "hideout", "README.zh-CN.md")
	unrelated := filepath.Join(prefix, "share", "hideout", "operator-note.txt")
	if err := os.WriteFile(unrelated, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := writeTestArtifact(t, nil)
	manifest := readManifest(t, second)
	manifest.Layout.Entrypoints = []string{"install.sh", "README.md"}
	var files []File
	for _, file := range manifest.Files {
		if file.Path != "README.zh-CN.md" {
			files = append(files, file)
		}
	}
	manifest.Files = files
	writeManifest(t, second, manifest)

	result, err := Install(InstallOptions{PackageRoot: second, Prefix: prefix, StoreRoot: store})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ObsoleteFiles) != 1 || result.ObsoleteFiles[0].Path != "share/hideout/README.zh-CN.md" {
		t.Fatalf("unexpected obsolete files: %+v", result.ObsoleteFiles)
	}
	if _, err := os.Stat(obsoletePath); err != nil {
		t.Fatalf("upgrade removed obsolete file without repair: %v", err)
	}
	if _, err := Verify(prefix); err == nil || !strings.Contains(err.Error(), "package repair --prefix") {
		t.Fatalf("expected verify repair hint, got %v", err)
	}

	dry, err := RepairObsoleteFiles(RepairOptions{Prefix: prefix, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Considered) != 1 || len(dry.Removed) != 0 {
		t.Fatalf("unexpected dry-run repair: %+v", dry)
	}
	if _, err := os.Stat(obsoletePath); err != nil {
		t.Fatalf("dry-run removed obsolete file: %v", err)
	}
	applied, err := RepairObsoleteFiles(RepairOptions{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Removed) != 1 || applied.Removed[0] != "share/hideout/README.zh-CN.md" {
		t.Fatalf("unexpected repair result: %+v", applied)
	}
	if _, err := os.Stat(obsoletePath); !os.IsNotExist(err) {
		t.Fatalf("obsolete file still exists after repair: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("repair removed unrelated file: %v", err)
	}
	if _, err := Verify(prefix); err != nil {
		t.Fatalf("verify after repair: %v", err)
	}
}

func TestRepairAndUninstallRejectSymlinkedObsoleteAncestors(t *testing.T) {
	for _, operation := range []string{"repair", "uninstall"} {
		t.Run(operation, func(t *testing.T) {
			artifact := writeTestArtifact(t, nil)
			prefix := filepath.Join(t.TempDir(), "prefix")
			store := filepath.Join(t.TempDir(), "store")
			if _, err := Install(InstallOptions{PackageRoot: artifact, Prefix: prefix, StoreRoot: store}); err != nil {
				t.Fatal(err)
			}

			outside := t.TempDir()
			victim := filepath.Join(outside, "victim.txt")
			if err := os.WriteFile(victim, []byte("outside\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(prefix, "share", "hideout", "old")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			state := readState(t, prefix)
			state.ObsoleteFiles = append(state.ObsoleteFiles, ObsoleteFile{
				Path:   "share/hideout/old/victim.txt",
				Kind:   "doc",
				SHA256: strings.Repeat("0", 64),
				Reason: "test stale file",
			})
			writeState(t, prefix, state)

			switch operation {
			case "repair":
				result, err := RepairObsoleteFiles(RepairOptions{Prefix: prefix})
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Rejected) != 1 || !strings.Contains(result.Rejected[0].Reason, "symbolic link") {
					t.Fatalf("repair did not reject symlinked ancestor: %+v", result)
				}
			case "uninstall":
				if _, err := Uninstall(UninstallOptions{Prefix: prefix}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
					t.Fatalf("uninstall did not reject symlinked ancestor: %v", err)
				}
				if _, err := os.Stat(filepath.Join(prefix, "bin", "hideout")); err != nil {
					t.Fatalf("rejected uninstall partially removed active package files: %v", err)
				}
			}
			if got, err := os.ReadFile(victim); err != nil || string(got) != "outside\n" {
				t.Fatalf("outside file changed: data=%q err=%v", got, err)
			}
		})
	}
}

func TestUpgradeRejectsPreviousPackageSchemaBeforeMutation(t *testing.T) {
	first := writeTestArtifact(t, map[string]string{"bin/hideout": "version-a\n"})
	prefix := filepath.Join(t.TempDir(), "prefix")
	store := filepath.Join(t.TempDir(), "store")
	if _, err := Install(InstallOptions{PackageRoot: first, Prefix: prefix, StoreRoot: store}); err != nil {
		t.Fatal(err)
	}
	state := readState(t, prefix)
	state.Package.Schema = "hideout.package-manifest.v0"
	writeState(t, prefix, state)
	bin := filepath.Join(prefix, "bin", "hideout")
	before, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	second := writeTestArtifact(t, map[string]string{"bin/hideout": "version-b\n"})
	if _, err := Install(InstallOptions{PackageRoot: second, Prefix: prefix, StoreRoot: store}); err == nil || !strings.Contains(err.Error(), "previous package schema is outside migration range") {
		t.Fatalf("expected previous package schema failure, got %v", err)
	}
	after, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("incompatible package schema mutated binary: before=%q after=%q", before, after)
	}
}

func TestPrivacyHelperPrerequisiteIsPackageOwnedAndFailClosed(t *testing.T) {
	var found bool
	for _, prereq := range ExternalPrerequisites() {
		if prereq.Name != "tun2socks" {
			continue
		}
		found = true
		if !prereq.PackageOwned {
			t.Fatalf("tun2socks must be package-owned, got %+v", prereq)
		}
		if prereq.Status == "" || !strings.Contains(prereq.Hint, "package") {
			t.Fatalf("unexpected prerequisite status: %+v", prereq)
		}
	}
	if !found {
		t.Fatal("missing tun2socks prerequisite status")
	}
}

func TestRuntimeMetadataSourceEmbeddedPackageAndInstallParity(t *testing.T) {
	catalog, contract := runtimecatalog.EmbeddedBytes()
	sourceCatalog, err := os.ReadFile(filepath.Join("..", "runtimecatalog", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	sourceContract, err := os.ReadFile(filepath.Join("..", "runtimecatalog", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(catalog, sourceCatalog) || !bytes.Equal(contract, sourceContract) {
		t.Fatal("embedded runtime metadata differs from source bytes")
	}
	artifact := writeTestArtifact(t, map[string]string{
		"runtime/catalog.json":  string(catalog),
		"runtime/contract.json": string(contract),
	})
	if _, err := Verify(artifact); err != nil {
		t.Fatalf("Verify artifact: %v", err)
	}
	prefix := filepath.Join(t.TempDir(), "prefix")
	if _, err := Install(InstallOptions{PackageRoot: artifact, Prefix: prefix, StoreRoot: filepath.Join(t.TempDir(), "store")}); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string][]byte{
		"share/hideout/runtime/catalog.json":  catalog,
		"share/hideout/runtime/contract.json": contract,
	} {
		got, err := os.ReadFile(filepath.Join(prefix, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("installed runtime metadata %s differs", rel)
		}
	}
	if err := os.WriteFile(filepath.Join(artifact, "runtime", "catalog.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(artifact); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("runtime metadata drift should fail package verification, got %v", err)
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
	if _, err := Uninstall(UninstallOptions{Prefix: prefix, Purge: true}); err == nil || !strings.Contains(err.Error(), "confirm-purge") {
		t.Fatalf("purge without exact store confirmation was accepted: %v", err)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("unconfirmed purge changed store: %v", err)
	}
	if _, err := Uninstall(UninstallOptions{Prefix: prefix, Purge: true, ConfirmPurgeStore: store}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("store not purged: %v", err)
	}
	purgeAudit := filepath.Join(filepath.Dir(store), "hideout-package-purge-audit.jsonl")
	data, err := os.ReadFile(purgeAudit)
	if err != nil {
		t.Fatalf("purge audit missing: %v", err)
	}
	if !strings.Contains(string(data), `"operation":"uninstall"`) || !strings.Contains(string(data), `"purge":true`) {
		t.Fatalf("purge audit missing uninstall/purge event: %s", data)
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
		"bin/hideout-shim-linux-" + runtime.GOARCH:                                    {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"bin/hideout-hostfsd-linux-" + runtime.GOARCH:                                 {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"bin/" + helperbin.LinuxSessionSupervisorCommand + "-linux-" + runtime.GOARCH: {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"bin/" + helperbin.LinuxObserverCommand + "-linux-" + runtime.GOARCH:          {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"bin/" + helperbin.LinuxWorkspacePortalCommand + "-linux-" + runtime.GOARCH:   {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"bin/" + helperbin.LinuxTun2SocksCommand + "-linux-" + runtime.GOARCH:         {kind: "linux-helper", executable: true, data: "#!/bin/sh\n"},
		"install.sh":                           {kind: "installer", executable: true, data: "#!/bin/sh\n"},
		"README.md":                            {kind: "entrypoint", executable: false, data: "readme\n"},
		"README.zh-CN.md":                      {kind: "entrypoint", executable: false, data: "readme zh\n"},
		"LICENSE":                              {kind: "doc", executable: false, data: "license\n"},
		"LICENSES/GPL-2.0-only.txt":            {kind: "doc", executable: false, data: "GPL-2.0-only\n"},
		"THIRD_PARTY_NOTICES.md":               {kind: "doc", executable: false, data: "notices\n"},
		"SECURITY.md":                          {kind: "doc", executable: false, data: "security\n"},
		"third_party/tun2socks/LICENSE":        {kind: "doc", executable: false, data: "MIT license\n"},
		"schemas/package-manifest.schema.json": {kind: "schema", executable: false, data: "{}\n"},
		"schemas/release-dogfood.schema.json":  {kind: "schema", executable: false, data: "{}\n"},
		"docs/README.md":                       {kind: "doc", executable: false, data: "docs\n"},
		"docs/STATUS.md":                       {kind: "doc", executable: false, data: "status\n"},
		"runtime/catalog.json":                 {kind: "runtime-catalog", executable: false, data: "{}\n"},
		"runtime/contract.json":                {kind: "runtime-contract", executable: false, data: "{}\n"},
	}
	componentContract, err := json.MarshalIndent(
		ExpectedPackageComponentContract(),
		"",
		"  ",
	)
	if err != nil {
		t.Fatal(err)
	}
	files[PackageComponentContractPath] = struct {
		kind       string
		executable bool
		data       string
	}{
		kind: "runtime-contract",
		data: string(componentContract) + "\n",
	}
	for rel, data := range overrides {
		spec := files[rel]
		spec.data = data
		files[rel] = spec
	}
	for _, command := range []string{helperbin.LinuxSessionSupervisorCommand, helperbin.LinuxWorkspacePortalCommand} {
		binaryRel := "bin/" + command + "-linux-" + runtime.GOARCH
		binary := files[binaryRel]
		sum := sha256.Sum256([]byte(binary.data))
		helperManifest, err := json.MarshalIndent(helperbin.Manifest{
			Version: helperbin.ManifestVersion, Command: command, TargetOS: "linux", TargetArch: runtime.GOARCH,
			Artifact: filepath.Base(binaryRel), SHA256: hex.EncodeToString(sum[:]), Builder: "unit-test", BuiltAt: "2026-07-09T00:00:00Z",
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		files[binaryRel+".manifest.json"] = struct {
			kind       string
			executable bool
			data       string
		}{kind: "helper-manifest", data: string(helperManifest) + "\n"}
	}
	tunBinaryRel := "bin/" + helperbin.LinuxTun2SocksCommand + "-linux-" + runtime.GOARCH
	tunSum := sha256.Sum256([]byte(files[tunBinaryRel].data))
	tunManifest, err := json.MarshalIndent(helperbin.Manifest{
		Version: helperbin.ManifestVersion, Command: helperbin.LinuxTun2SocksCommand,
		TargetOS: "linux", TargetArch: runtime.GOARCH, Artifact: filepath.Base(tunBinaryRel),
		SHA256: hex.EncodeToString(tunSum[:]), Builder: "unit-test", BuiltAt: "2026-07-09T00:00:00Z",
		UpstreamModule: helperbin.Tun2SocksUpstreamModule, UpstreamVersion: helperbin.Tun2SocksUpstreamVersion,
		License: helperbin.Tun2SocksLicense, BuildMode: helperbin.Tun2SocksBuildMode, PackageOwned: true,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[tunBinaryRel+".manifest.json"] = struct {
		kind       string
		executable bool
		data       string
	}{kind: "helper-manifest", data: string(tunManifest) + "\n"}
	observerBinaryRel := "bin/" + helperbin.LinuxObserverCommand + "-linux-" + runtime.GOARCH
	observerSum := sha256.Sum256([]byte(files[observerBinaryRel].data))
	observerManifest, err := json.MarshalIndent(helperbin.Manifest{
		Version: helperbin.ManifestVersion, Command: helperbin.LinuxObserverCommand,
		TargetOS: "linux", TargetArch: runtime.GOARCH,
		Artifact: filepath.Base(observerBinaryRel),
		SHA256:   hex.EncodeToString(observerSum[:]),
		Builder:  "go build -trimpath", BuiltAt: "2026-07-09T00:00:00Z",
		License: helperbin.LinuxObserverLicense, BuildMode: helperbin.LinuxObserverBuildMode,
		PackageOwned: true,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[observerBinaryRel+".manifest.json"] = struct {
		kind       string
		executable bool
		data       string
	}{kind: "helper-manifest", data: string(observerManifest) + "\n"}
	for rel, data := range overrides {
		spec := files[rel]
		spec.data = data
		files[rel] = spec
	}
	embeddedAssets := BrowserConsoleAssets()
	for index := range embeddedAssets {
		embeddedAssets[index].SHA256 = BytesSHA256(
			[]byte("embedded fixture " + embeddedAssets[index].Path + "\n"),
		)
	}
	browserManifest, err := json.MarshalIndent(EmbeddedAssetManifest{
		Schema:          EmbeddedAssetManifestSchema,
		ID:              BrowserConsoleAssetID,
		Container:       BrowserConsoleContainerPath,
		ContainerSHA256: BytesSHA256([]byte(files["bin/hideout"].data)),
		License:         BrowserConsoleAssetLicense,
		Assets:          embeddedAssets,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[BrowserConsoleManifestPath] = struct {
		kind       string
		executable bool
		data       string
	}{
		kind: "embedded-asset-manifest",
		data: string(browserManifest) + "\n",
	}
	for rel, data := range overrides {
		spec := files[rel]
		spec.data = data
		files[rel] = spec
	}
	browserManifestSHA256 := BytesSHA256(
		[]byte(files[BrowserConsoleManifestPath].data),
	)
	manifest := Manifest{
		Schema:  ArtifactSchema,
		BuiltAt: "2026-07-09T00:00:00Z",
		Release: ReleaseInfo{ProductVersion: "0.1.0-alpha.1", Channel: "developer-preview", Tag: "v0.1.0-alpha.1"},
		Source:  SourceInfo{Repository: "https://github.com/vibe-agi/hideout", Commit: "0123456789abcdef0123456789abcdef01234567"},
		Build:   BuildInfo{Workflow: "unit-test", Ref: "refs/heads/test"},
		Target: Target{
			HostOS:         runtime.GOOS,
			HostArch:       runtime.GOARCH,
			LinuxGuestArch: runtime.GOARCH,
		},
		Runtime: RuntimeInfo{
			Family: "developer-standard", Revision: "2026.07.0",
			CatalogFileSHA256: strings.Repeat("a", 64), ArtifactSHA256: strings.Repeat("b", 64),
		},
		SigningSummary: SigningSummary{Mode: "developer-preview-unsigned"},
		Layout: Layout{
			Root: DefaultPackageRoot,
			Binaries: []string{
				"bin/hideout",
				"bin/hideout-shim",
				"bin/hideout-shim-linux-" + runtime.GOARCH,
				"bin/hideout-hostfsd-linux-" + runtime.GOARCH,
				"bin/" + helperbin.LinuxSessionSupervisorCommand + "-linux-" + runtime.GOARCH,
				"bin/" + helperbin.LinuxObserverCommand + "-linux-" + runtime.GOARCH,
				"bin/" + helperbin.LinuxWorkspacePortalCommand + "-linux-" + runtime.GOARCH,
				"bin/" + helperbin.LinuxTun2SocksCommand + "-linux-" + runtime.GOARCH,
			},
			Entrypoints: []string{"install.sh", "README.md", "README.zh-CN.md"},
			Directories: []string{"schemas", "docs", "packaging", "runtime", "third_party"},
		},
		EmbeddedAssets: []EmbeddedAssetBinding{{
			ID:             BrowserConsoleAssetID,
			Container:      BrowserConsoleContainerPath,
			Manifest:       BrowserConsoleManifestPath,
			ManifestSHA256: browserManifestSHA256,
			License:        BrowserConsoleAssetLicense,
		}},
		Migration: Migration{
			InstallStateSchema:   InstallStateSchema,
			FromInstalledSchemas: []string{InstallStateSchema},
			MinimumPackageSchema: ArtifactSchema,
			MaximumPackageSchema: ArtifactSchema,
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
