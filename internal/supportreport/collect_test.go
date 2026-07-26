package supportreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/packagekit"
)

func TestCollectRequiresDoctorAndTreatsSourceBinaryAsNotApplicable(t *testing.T) {
	product := DefaultProduct("dev", "unknown", "unknown")
	if _, err := Collect(CollectOptions{Product: product, Backend: "lima"}); err == nil {
		t.Fatal("collection without required doctor report succeeded")
	}

	builder := doctor.NewBuilder(doctor.Request{Profile: "default", Backend: "lima"})
	builder.Add("store", "store", doctor.StatusPass, "present")
	report, err := Collect(CollectOptions{
		Now:     time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC),
		Product: product, Backend: "lima",
		Executable: filepath.Join(t.TempDir(), "bin", "hideout"),
		Doctor:     builder.Report(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Package.Applicability != "not-applicable" ||
		report.Collection.Package != "not-applicable" {
		t.Fatalf("source package state=%+v collection=%+v", report.Package, report.Collection)
	}
}

func TestCollectRecordsDamagedInstalledPackageWithoutRawPrefix(t *testing.T) {
	prefix := t.TempDir()
	binary := filepath.Join(prefix, "bin", "hideout")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("damaged"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(prefix, filepath.FromSlash(packagekit.InstalledManifest))
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	state := packagekit.InstallState{
		Schema: packagekit.InstallStateSchema, InstalledAt: "2026-07-26T00:00:00Z",
		InstallPrefix: prefix,
		Package: packagekit.InstalledSource{
			Schema: packagekit.ArtifactSchema, BuiltAt: "2026-07-26T00:00:00Z",
			Release: packagekit.ReleaseInfo{
				ProductVersion: "0.1.0-alpha.1", Channel: "alpha", Tag: "v0.1.0-alpha.1",
			},
			Source: packagekit.SourceInfo{
				Repository: "https://github.com/vibe-agi/hideout",
				Commit:     "0123456789012345678901234567890123456789",
			},
			Target: packagekit.Target{
				HostOS: runtime.GOOS, HostArch: runtime.GOARCH, LinuxGuestArch: runtime.GOARCH,
			},
		},
		Files: []packagekit.File{{
			Path: "bin/hideout", Kind: "binary", SHA256: strings.Repeat("0", 64), Executable: true,
		}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	builder := doctor.NewBuilder(doctor.Request{Profile: "default", Backend: "lima"})
	builder.Add("store", "store", doctor.StatusPass, "present")
	report, err := Collect(CollectOptions{
		Product: DefaultProduct("dev", "unknown", "unknown"), Backend: "lima",
		Executable: binary, Doctor: builder.Report(), Protected: []string{prefix},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Package.Applicability != "installed" ||
		report.Package.Verification != "failed" ||
		report.Collection.Package != "failed" ||
		report.Package.Finding == "" {
		t.Fatalf("damaged package was not represented as a finding: %+v", report.Package)
	}
	encoded, err := MarshalValidated(report, []string{prefix})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), prefix) {
		t.Fatalf("support report leaked package prefix:\n%s", encoded)
	}
}

func TestCollectVerifiesInstalledPackageIdentity(t *testing.T) {
	prefix, binary := writeVerifiedInstalledPackage(t)
	builder := doctor.NewBuilder(doctor.Request{Profile: "default", Backend: "lima"})
	builder.Add("store", "store", doctor.StatusPass, "present")
	report, err := Collect(CollectOptions{
		Product: DefaultProduct("0.1.0-alpha.1", strings.Repeat("1", 40), "2026-07-26T00:00:00Z"),
		Backend: "lima", Executable: binary, Doctor: builder.Report(),
		Protected: []string{prefix},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Package.Applicability != "installed" ||
		report.Package.Verification != "passed" ||
		report.Package.ProductVersion != "0.1.0-alpha.1" ||
		report.Package.SourceCommit != strings.Repeat("1", 40) ||
		report.Collection.Package != "collected" {
		t.Fatalf("verified package identity missing: %+v", report.Package)
	}
	if _, err := MarshalValidated(report, []string{prefix}); err != nil {
		t.Fatalf("verified installed report did not validate: %v", err)
	}
}

func writeVerifiedInstalledPackage(t *testing.T) (string, string) {
	t.Helper()
	prefix := t.TempDir()
	var files []packagekit.File
	writeFile := func(rel, kind, body string, executable bool) {
		t.Helper()
		path := filepath.Join(prefix, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if executable {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		sum, err := packagekit.FileSHA256(path)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, packagekit.File{
			Path: rel, Kind: kind, SHA256: sum, Executable: executable,
		})
	}

	writeFile("bin/hideout", "binary", "#!/bin/sh\n", true)
	for _, command := range []string{
		helperbin.LinuxSessionSupervisorCommand,
		helperbin.LinuxWorkspacePortalCommand,
	} {
		rel := "bin/" + command + "-linux-" + runtime.GOARCH
		writeFile(rel, "linux-helper", "#!/bin/sh\n", true)
		sum := files[len(files)-1].SHA256
		manifestData, err := json.Marshal(helperbin.Manifest{
			Version: helperbin.ManifestVersion,
			Command: command, TargetOS: "linux", TargetArch: runtime.GOARCH,
			Artifact: filepath.Base(rel), SHA256: sum,
			Builder: "supportreport-test", BuiltAt: "2026-07-26T00:00:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		writeFile(rel+".manifest.json", "helper-manifest", string(manifestData)+"\n", false)
	}
	tunRel := "bin/" + helperbin.LinuxTun2SocksCommand + "-linux-" + runtime.GOARCH
	writeFile(tunRel, "linux-helper", "#!/bin/sh\n", true)
	tunSum := files[len(files)-1].SHA256
	tunManifest, err := json.Marshal(helperbin.Manifest{
		Version: helperbin.ManifestVersion, Command: helperbin.LinuxTun2SocksCommand,
		TargetOS: "linux", TargetArch: runtime.GOARCH, Artifact: filepath.Base(tunRel),
		SHA256: tunSum, Builder: "supportreport-test", BuiltAt: "2026-07-26T00:00:00Z",
		UpstreamModule: helperbin.Tun2SocksUpstreamModule, UpstreamVersion: helperbin.Tun2SocksUpstreamVersion,
		License: helperbin.Tun2SocksLicense, BuildMode: helperbin.Tun2SocksBuildMode, PackageOwned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(tunRel+".manifest.json", "helper-manifest", string(tunManifest)+"\n", false)
	writeFile("share/hideout/third_party/tun2socks/LICENSE", "doc", "MIT license\n", false)

	state := packagekit.InstallState{
		Schema:        packagekit.InstallStateSchema,
		InstalledAt:   "2026-07-26T00:00:00Z",
		InstallPrefix: prefix,
		Package: packagekit.InstalledSource{
			Schema:  packagekit.ArtifactSchema,
			BuiltAt: "2026-07-26T00:00:00Z",
			Release: packagekit.ReleaseInfo{
				ProductVersion: "0.1.0-alpha.1", Channel: "alpha", Tag: "v0.1.0-alpha.1",
			},
			Source: packagekit.SourceInfo{
				Repository: "https://github.com/vibe-agi/hideout",
				Commit:     strings.Repeat("1", 40),
			},
			Target: packagekit.Target{
				HostOS: runtime.GOOS, HostArch: runtime.GOARCH, LinuxGuestArch: runtime.GOARCH,
			},
		},
		Files: files,
	}
	manifestPath := filepath.Join(prefix, filepath.FromSlash(packagekit.InstalledManifest))
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return prefix, filepath.Join(prefix, "bin", "hideout")
}
