package supportreport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/releasecompat"
)

type CollectOptions struct {
	Now        time.Time
	Product    Product
	Backend    string
	Executable string
	Doctor     doctor.Report
	Protected  []string
}

func Collect(opts CollectOptions) (Report, error) {
	if opts.Doctor.Schema != doctor.Schema {
		return Report{}, fmt.Errorf("required doctor collection did not produce %s", doctor.Schema)
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	matrix := releasecompat.BuiltinMatrix()
	platform := supportEntry(matrix, releasecompat.CurrentPlatformSubject())
	backend := supportEntry(matrix, releasecompat.BackendSubject(opts.Backend))
	pkg, packageStatus := collectPackage(opts.Executable, opts.Protected)
	return Report{
		Schema:      Schema,
		GeneratedAt: now,
		Product:     opts.Product,
		Support: Support{
			Schema: matrix.Schema, Version: matrix.Version,
			Platform: platform, Backend: backend,
		},
		Package:  pkg,
		Doctor:   opts.Doctor,
		Recovery: RecoveryEntries(),
		Collection: Collection{
			Product: "collected", Support: "collected", Package: packageStatus,
			Doctor: "collected", Recovery: "collected",
		},
		Redaction: Redaction{
			Mode: "shareable-support",
			ExcludedDataClasses: []string{
				"raw-audit", "workspace-content", "secret-backing", "proxy-value",
				"control-plane-token", "machine-id", "raw-host-path",
			},
		},
		Provenance: Provenance{
			Command:  "hideout support report --out <path>",
			Delivery: "local-file-only", Uploaded: false, MaxBytes: MaxBytes,
		},
	}, nil
}

func supportEntry(matrix releasecompat.Matrix, subject string) SupportEntry {
	entry, ok := releasecompat.FindEntry(matrix, subject)
	if !ok {
		return SupportEntry{Subject: subject, Level: releasecompat.LevelUnsupported}
	}
	return SupportEntry{Subject: entry.Subject, Level: entry.Level}
}

func collectPackage(executable string, protected []string) (Package, string) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		executable, _ = os.Executable()
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	prefix := filepath.Dir(filepath.Dir(executable))
	manifestPath := filepath.Join(prefix, filepath.FromSlash(packagekit.InstalledManifest))
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Package{Applicability: "not-applicable", Verification: "not-applicable"}, "not-applicable"
	}

	out := Package{Applicability: "installed", Verification: "failed"}
	state, stateErr := packagekit.LoadInstallState(manifestPath)
	if stateErr == nil {
		out.Schema = state.Schema
		out.ProductVersion = state.Package.Release.ProductVersion
		out.SourceCommit = state.Package.Source.Commit
		out.Target = state.Package.Target.HostOS + "/" + state.Package.Target.HostArch
		out.FileCount = len(state.Files)
	}
	verification, verifyErr := packagekit.Verify(prefix)
	if verifyErr != nil {
		out.Finding = Sanitize(verifyErr.Error(), append(protected, prefix))
		return out, "failed"
	}
	out.Verification = "passed"
	out.FileCount = verification.Files
	return out, "collected"
}
