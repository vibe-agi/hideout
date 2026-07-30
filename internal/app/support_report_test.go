package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	doctorpkg "github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/packagekit"
)

func TestSupportReportRequiresExplicitSafeOutputAndDoesNotCreateStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)

	var out, stderr bytes.Buffer
	if code := Main([]string{"support", "report"}, &out, &stderr); code == 0 {
		t.Fatalf("support report without --out succeeded: %s", out.String())
	}
	if !strings.Contains(stderr.String(), "--out") {
		t.Fatalf("missing-output error is not actionable: %s", stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".hideout")); !os.IsNotExist(err) {
		t.Fatalf("support report created store state: %v", err)
	}
}

func TestSupportReportWritesLocalBoundedSourceReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)
	outPath := filepath.Join(t.TempDir(), "hideout-support.json")

	var out, stderr bytes.Buffer
	code := Main([]string{
		"support", "report",
		"--out", outPath,
		"--profile", "missing",
		"--backend", "lima",
		"--workspace", t.TempDir(),
	}, &out, &stderr)
	if code != 0 {
		t.Fatalf("support report exit=%d stderr=%s stdout=%s", code, stderr.String(), out.String())
	}
	if !strings.Contains(out.String(), "local-only") ||
		!strings.Contains(out.String(), "inspect before sharing") {
		t.Fatalf("support result did not explain local delivery:\n%s", out.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Schema  string `json:"schema"`
		Package struct {
			Applicability string `json:"applicability"`
			Verification  string `json:"verification"`
		} `json:"package"`
		Doctor    doctorpkg.Report `json:"doctor"`
		Redaction struct {
			ExcludedDataClasses []string `json:"excludedDataClasses"`
		} `json:"redaction"`
		Provenance struct {
			Uploaded bool `json:"uploaded"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode support report: %v\n%s", err, data)
	}
	if report.Schema != "hideout.support-report/v1" ||
		report.Package.Applicability != "not-applicable" ||
		report.Package.Verification != "not-applicable" ||
		report.Provenance.Uploaded {
		t.Fatalf("unexpected source support report: %+v", report)
	}
	if len(data) > 1<<20 {
		t.Fatalf("support report exceeds 1 MiB: %d", len(data))
	}
	var privacyFinding *doctorpkg.Finding
	for index := range report.Doctor.Findings {
		if report.Doctor.Findings[index].CheckID == "activity-privacy" {
			privacyFinding = &report.Doctor.Findings[index]
			break
		}
	}
	if privacyFinding == nil {
		t.Fatalf("support report lacks activity privacy disclosure: %+v", report.Doctor)
	}
	privacyFacts := fmt.Sprint(privacyFinding.Details["observedFacts"])
	for _, want := range []string{
		"localPathVisibility=visible-in-authenticated-local-view",
		"shareableSupport=activity-records-and-raw-paths-excluded",
		"coverageNonClaim=no-events-does-not-prove-no-behavior",
		"retentionOwner=exact-environment-or-disposable-session-plus-backend-incarnation",
		"desiredOwnerLimitBytes=268435456",
		"desiredMaxAge=owner-lifecycle",
		"defaultGlobalSafetyLimitBytes=1073741824",
		"defaultActiveSegmentAllowanceBytes=8388608",
	} {
		if !strings.Contains(privacyFacts, want) {
			t.Fatalf("support privacy facts missing %q:\n%s", want, privacyFacts)
		}
	}
	exclusions := strings.Join(report.Redaction.ExcludedDataClasses, ",")
	for _, want := range []string{
		"activity-record",
		"activity-local-path",
		"activity-command-argv",
		"activity-domain",
		"activity-ip",
	} {
		if !strings.Contains(exclusions, want) {
			t.Fatalf("support redaction exclusions missing %q: %s", want, exclusions)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".hideout")); !os.IsNotExist(err) {
		t.Fatalf("support collection mutated store: %v", err)
	}
}

func TestSupportReportRefusesExistingOutputWithoutOverwrite(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "hideout-support.json")
	if err := os.WriteFile(outPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Main([]string{"support", "report", "--out", outPath}, &out, &stderr); code == 0 {
		t.Fatalf("support report overwrote existing destination: %s", out.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("existing output changed: %q", data)
	}
}

func TestSupportReportRepresentsDamagedInstalledPackageAsFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
			Schema:  packagekit.ArtifactSchema,
			Release: packagekit.ReleaseInfo{ProductVersion: "0.1.0-alpha.1"},
			Source:  packagekit.SourceInfo{Commit: strings.Repeat("1", 40)},
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

	outPath := filepath.Join(t.TempDir(), "support.json")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr, stdin: strings.NewReader(""),
		supportExecutable: func() string { return binary },
	}
	if err := a.run([]string{"support", "report", "--out", outPath}); err != nil {
		t.Fatalf("support report: %v stderr=%s", err, stderr.String())
	}
	var report struct {
		Package struct {
			Applicability string `json:"applicability"`
			Verification  string `json:"verification"`
			Finding       string `json:"finding"`
		} `json:"package"`
		Collection struct {
			Package string `json:"package"`
		} `json:"collection"`
	}
	reportData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatal(err)
	}
	if report.Package.Applicability != "installed" ||
		report.Package.Verification != "failed" ||
		report.Package.Finding == "" ||
		report.Collection.Package != "failed" {
		t.Fatalf("damaged package was reported as success: %+v", report)
	}
	if strings.Contains(string(reportData), prefix) {
		t.Fatalf("support report leaked installed prefix:\n%s", reportData)
	}
}
