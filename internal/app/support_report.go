package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	doctorpkg "github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/releasecompat"
	"github.com/vibe-agi/hideout/internal/supportreport"
)

type supportReportOptions struct {
	out       string
	profile   string
	backend   string
	workspace string
	overwrite bool
}

func (a app) supportReport(args []string) error {
	opts, err := parseSupportReportOptions(args)
	if err != nil {
		return err
	}
	if err := supportreport.ValidateDestination(opts.out, opts.overwrite); err != nil {
		return err
	}
	protected := supportReportProtectedValues(opts.workspace)
	doctorReport, err := collectReadOnlySupportDoctor(opts, protected)
	if err != nil {
		return err
	}
	executable := currentExecutable()
	if a.supportExecutable != nil {
		executable = a.supportExecutable()
	}
	report, err := supportreport.Collect(supportreport.CollectOptions{
		Product: supportreport.DefaultProduct(Version, Commit, BuildTime),
		Backend: opts.backend, Executable: executable,
		Doctor: doctorReport, Protected: protected,
	})
	if err != nil {
		return err
	}
	data, err := supportreport.MarshalValidated(report, protected)
	if err != nil {
		return err
	}
	if err := supportreport.WriteAtomic(opts.out, data, opts.overwrite); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "support report: wrote local-only file (%d bytes); inspect before sharing; nothing was uploaded\n", len(data))
	return nil
}

func parseSupportReportOptions(args []string) (supportReportOptions, error) {
	opts := supportReportOptions{profile: "default", backend: "auto"}
	fs := flag.NewFlagSet("support report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.out, "out", "", "local support report destination")
	fs.StringVar(&opts.profile, "profile", "default", "profile name")
	fs.StringVar(&opts.backend, "backend", "auto", "auto or lima")
	fs.StringVar(&opts.workspace, "workspace", "", "selected workspace")
	fs.BoolVar(&opts.overwrite, "overwrite", false, "replace an existing owned regular file")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected support report argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(opts.out) == "" {
		return opts, errors.New("usage: hideout support report --out <path>")
	}
	if strings.TrimSpace(opts.profile) == "" {
		return opts, errors.New("--profile must not be empty")
	}
	switch opts.backend {
	case "auto", "lima":
	default:
		return opts, fmt.Errorf("unsupported support report backend %q", opts.backend)
	}
	if opts.workspace != "" {
		if !filepath.IsAbs(opts.workspace) || filepath.Clean(opts.workspace) != opts.workspace {
			return opts, errors.New("--workspace must be a clean absolute path")
		}
	}
	return opts, nil
}

func collectReadOnlySupportDoctor(opts supportReportOptions, protected []string) (doctorpkg.Report, error) {
	backend := resolveBackendName(opts.backend)
	builder := doctorpkg.NewBuilder(doctorpkg.Request{
		Profile: opts.profile, Backend: backend, Level: doctorpkg.LevelLight,
	})
	store, err := profile.DefaultStore()
	if err != nil {
		return doctorpkg.Report{}, fmt.Errorf("collect doctor store: %w", err)
	}
	switch info, statErr := os.Stat(store.Root); {
	case statErr == nil && info.IsDir():
		builder.Add("store", "store", doctorpkg.StatusPass, "Hideout store is present")
	case os.IsNotExist(statErr):
		builder.Add("store", "store", doctorpkg.StatusWarn, "Hideout store is not initialized",
			doctorpkg.WithRequired(false), doctorpkg.WithNextActions("hideout setup"))
	case statErr != nil:
		builder.Add("store", "store", doctorpkg.StatusError,
			supportreport.Sanitize(statErr.Error(), protected))
	default:
		builder.Add("store", "store", doctorpkg.StatusError, "Hideout store path is not a directory")
	}

	p, loadErr := store.Load(opts.profile)
	switch {
	case loadErr == nil:
		builder.Add("profile", "profile", doctorpkg.StatusPass, "selected profile is readable")
	case os.IsNotExist(loadErr):
		p = profile.Default(opts.profile)
		builder.Add("profile", "profile", doctorpkg.StatusWarn, "selected profile is not initialized",
			doctorpkg.WithRequired(false), doctorpkg.WithNextActions("hideout setup"))
	default:
		p = profile.Default(opts.profile)
		builder.Add("profile", "profile", doctorpkg.StatusError,
			supportreport.Sanitize(loadErr.Error(), protected))
	}

	platform, platformOK := releasecompat.FindEntry(releasecompat.BuiltinMatrix(), releasecompat.CurrentPlatformSubject())
	backendEntry, backendOK := releasecompat.FindEntry(releasecompat.BuiltinMatrix(), releasecompat.BackendSubject(backend))
	supportStatus := doctorpkg.StatusPass
	if !platformOK || !backendOK ||
		platform.Level == releasecompat.LevelUnsupported ||
		backendEntry.Level == releasecompat.LevelUnsupported {
		supportStatus = doctorpkg.StatusError
	} else if platform.Level == releasecompat.LevelDegraded ||
		backendEntry.Level == releasecompat.LevelDegraded {
		supportStatus = doctorpkg.StatusWarn
	}
	builder.Add("support-matrix", "support", supportStatus,
		fmt.Sprintf("platform=%s backend=%s", levelOrUnsupported(platform, platformOK), levelOrUnsupported(backendEntry, backendOK)),
		doctorpkg.WithRequired(supportStatus == doctorpkg.StatusError))

	if backend == "lima" {
		if _, lookupErr := exec.LookPath("limactl"); lookupErr != nil {
			builder.Add("backend", "backend", doctorpkg.StatusError, "Lima is not available",
				doctorpkg.WithNextActions("brew install lima"))
		} else {
			builder.Add("backend", "backend", doctorpkg.StatusPass, "Lima is available")
		}
	}

	switch p.Network.Mode {
	case "direct", "":
		builder.Add("network", "network", doctorpkg.StatusPass, "mode=direct; network origin visible")
	case "tun2socks":
		if strings.TrimSpace(p.Network.ProxySecretRef) == "" ||
			strings.TrimSpace(p.Network.MediatedResolver) == "" {
			builder.Add("network", "network", doctorpkg.StatusError,
				"privacy mode is missing a proxy reference or mediated resolver",
				doctorpkg.WithNextActions("hideout help privacy"))
		} else {
			builder.Add("network", "network", doctorpkg.StatusPass,
				"mode=tun2socks; proxy reference and mediated resolver are configured")
		}
	default:
		builder.Add("network", "network", doctorpkg.StatusError, "network mode is unsupported")
	}

	builder.Add(
		"activity-privacy",
		"activity",
		doctorpkg.StatusPass,
		"workload activity is local metadata owned by an exact VM/session incarnation; this shareable report excludes activity records and raw paths",
		doctorpkg.WithRequired(false),
		doctorpkg.WithDetails(map[string]any{
			"observedFacts": activityPrivacyFacts(
				activityRetentionForProfile(p),
			),
		}),
		doctorpkg.WithNextActions(
			"hideout doctor --feature activity",
			"hideout activity coverage",
		),
	)

	if opts.workspace == "" {
		builder.Add("workspace", "workspace", doctorpkg.StatusSkipped,
			"workspace inspection was not requested", doctorpkg.WithRequired(false))
	} else if info, statErr := os.Stat(opts.workspace); statErr != nil {
		builder.Add("workspace", "workspace", doctorpkg.StatusError,
			supportreport.Sanitize(statErr.Error(), protected))
	} else if !info.IsDir() {
		builder.Add("workspace", "workspace", doctorpkg.StatusError,
			"selected workspace is not a directory")
	} else {
		builder.Add("workspace", "workspace", doctorpkg.StatusPass,
			"selected workspace is a directory")
	}
	return builder.Report(), nil
}

func levelOrUnsupported(entry releasecompat.Entry, ok bool) string {
	if !ok {
		return releasecompat.LevelUnsupported
	}
	return entry.Level
}

func supportReportProtectedValues(workspace string) []string {
	values := []string{workspace}
	if home, err := os.UserHomeDir(); err == nil {
		values = append(values, home)
	}
	for _, item := range os.Environ() {
		name, value, found := strings.Cut(item, "=")
		if !found || len(strings.TrimSpace(value)) < 4 {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "PROXY") ||
			strings.Contains(upper, "PASSWORD") ||
			strings.Contains(upper, "CREDENTIAL") {
			values = append(values, value)
		}
	}
	return values
}
