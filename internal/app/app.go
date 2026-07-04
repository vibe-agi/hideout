package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/backend/native"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/envpolicy"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostopen"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/portbridge"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

type app struct {
	stdout io.Writer
	stderr io.Writer
}

func Main(args []string, stdout, stderr io.Writer) int {
	a := app{stdout: stdout, stderr: stderr}
	if err := a.run(args); err != nil {
		fmt.Fprintln(stderr, "hideout:", err)
		return 1
	}
	return 0
}

func (a app) run(args []string) error {
	if len(args) == 0 {
		a.usage()
		return nil
	}
	switch args[0] {
	case "init":
		return a.initCommand(args[1:])
	case "run":
		return a.runCommand(args[1:], false)
	case "explain":
		return a.runCommand(args[1:], true)
	case "doctor":
		return a.doctor(args[1:])
	case "profile":
		return a.profile(args[1:])
	case "list":
		return a.listEnvironments(args[1:])
	case "stop":
		return a.stopEnvironments(args[1:])
	case "clean":
		return a.cleanEnvironments(args[1:])
	case "cleanup":
		return a.cleanup(args[1:])
	case "audit":
		return a.auditCommand(args[1:])
	case "ui":
		return a.ui(args[1:])
	case "tui":
		return a.tui(args[1:])
	case "lab":
		return a.lab(args[1:])
	case "shim":
		return a.shim(args[1:])
	case "hostfsd":
		return a.hostfsd(args[1:])
	case "package":
		return a.packageCommand(args[1:])
	case "help", "-h", "--help":
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a app) usage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout run [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout run --explain [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout explain [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout run --preview 127.0.0.1:<guest-port> -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --fs read:/path --fs dir:/path -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --no-fs read:/path --no-profile-fs -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --verbose -- <command>  # print Hideout control-plane progress and summary")
	fmt.Fprintln(a.stdout, "  hideout run --allow-unsafe-workspace -- <command>  # explicit high-risk workspace mount")
	fmt.Fprintln(a.stdout, "  hideout init [--no-input] [--backend lima|auto] [--network direct]")
	fmt.Fprintln(a.stdout, "  hideout init --npm-package <npm-spec> --npm-command <command>")
	fmt.Fprintln(a.stdout, "  hideout run --backend native --allow-weak-isolation -- <command>  # dev harness only")
	fmt.Fprintln(a.stdout, "  hideout doctor")
	fmt.Fprintln(a.stdout, "  hideout doctor --fix [--dry-run]")
	fmt.Fprintln(a.stdout, "  hideout profile init <name>")
	fmt.Fprintln(a.stdout, "  hideout profile clone <source> <name>")
	fmt.Fprintln(a.stdout, "  hideout profile rotate-identity <name>")
	fmt.Fprintln(a.stdout, "  hideout profile reset <name>")
	fmt.Fprintln(a.stdout, "  hideout profile path <name>")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> add --fs <kind:/path> [--reason <text>]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> deny --no-fs <kind:/path> [--reason <text>]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> remove <rule-id>")
	fmt.Fprintln(a.stdout, "  hideout list")
	fmt.Fprintln(a.stdout, "  hideout stop [--dry-run] [--idle <duration>] [environment-id...]")
	fmt.Fprintln(a.stdout, "  hideout clean [--dry-run] [--stopped] [--idle <duration>] [environment-id...]")
	fmt.Fprintln(a.stdout, "  hideout cleanup [--session <id>] [--dry-run]")
	fmt.Fprintln(a.stdout, "  hideout audit show [--session <id>] [--profile <name>] [--action <name>] [--decision <value>] [--limit N] [--json]")
	fmt.Fprintln(a.stdout, "  hideout ui [--listen 127.0.0.1:0] [--ttl 15m] [--no-open] [--print-url]")
	fmt.Fprintln(a.stdout, "  hideout tui [--watch] [--interval 2s]")
	fmt.Fprintln(a.stdout, "  hideout package verify <package-root>")
	fmt.Fprintln(a.stdout, "  hideout shim build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	fmt.Fprintln(a.stdout, "  hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge guest-to-host --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge host-to-guest --enable-lab --guest-target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab browser-control --enable-lab --profile <name>")
	fmt.Fprintln(a.stdout, "  hideout lab preview-open --enable-lab --guest-url http://127.0.0.1:<port>")
}

type packageManifest struct {
	Schema  string `json:"schema"`
	BuiltAt string `json:"builtAt"`
	Git     struct {
		Commit string `json:"commit"`
		Dirty  bool   `json:"dirty"`
	} `json:"git"`
	Target struct {
		HostOS         string `json:"hostOS"`
		HostArch       string `json:"hostArch"`
		LinuxGuestArch string `json:"linuxGuestArch"`
	} `json:"target"`
	Layout struct {
		Root        string   `json:"root"`
		Binaries    []string `json:"binaries"`
		Entrypoints []string `json:"entrypoints"`
		Directories []string `json:"directories"`
	} `json:"layout"`
	Files []packageManifestFile `json:"files"`
}

type packageManifestFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

func (a app) packageCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("package command is required")
	}
	switch args[0] {
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: hideout package verify <package-root>")
		}
		result, err := verifyPackageRoot(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "package: ok root=%s files=%d\n", result.Root, result.Files)
		return nil
	default:
		return fmt.Errorf("unknown package command %q", args[0])
	}
}

type packageVerifyResult struct {
	Root  string
	Files int
}

func verifyPackageRoot(root string) (packageVerifyResult, error) {
	if strings.TrimSpace(root) == "" {
		return packageVerifyResult{}, errors.New("package root is required")
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return packageVerifyResult{}, err
	}
	cleanRoot, err = filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return packageVerifyResult{}, fmt.Errorf("resolve package root: %w", err)
	}
	if st, err := os.Stat(cleanRoot); err != nil {
		return packageVerifyResult{}, fmt.Errorf("stat package root: %w", err)
	} else if !st.IsDir() {
		return packageVerifyResult{}, fmt.Errorf("package root is not a directory: %s", cleanRoot)
	}
	manifestPath := filepath.Join(cleanRoot, "package-manifest.json")
	f, err := os.Open(manifestPath)
	if err != nil {
		return packageVerifyResult{}, fmt.Errorf("open package-manifest.json: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var manifest packageManifest
	if err := dec.Decode(&manifest); err != nil {
		return packageVerifyResult{}, fmt.Errorf("parse package-manifest.json: %w", err)
	}
	if manifest.Schema != "hideout.package-manifest.v1" {
		return packageVerifyResult{}, fmt.Errorf("unsupported package manifest schema %q", manifest.Schema)
	}
	if strings.TrimSpace(manifest.BuiltAt) == "" {
		return packageVerifyResult{}, errors.New("package manifest builtAt is required")
	}
	if strings.TrimSpace(manifest.Git.Commit) == "" {
		return packageVerifyResult{}, errors.New("package manifest git.commit is required")
	}
	if strings.TrimSpace(manifest.Target.HostOS) == "" || strings.TrimSpace(manifest.Target.HostArch) == "" || strings.TrimSpace(manifest.Target.LinuxGuestArch) == "" {
		return packageVerifyResult{}, errors.New("package manifest target hostOS, hostArch, and linuxGuestArch are required")
	}
	if manifest.Layout.Root != "hideout" {
		return packageVerifyResult{}, fmt.Errorf("package manifest layout.root must be hideout")
	}
	if len(manifest.Files) == 0 {
		return packageVerifyResult{}, errors.New("package manifest has no files")
	}
	if err := verifyPackageLayout(cleanRoot, manifest); err != nil {
		return packageVerifyResult{}, err
	}
	seenFiles := map[string]struct{}{}
	for _, file := range manifest.Files {
		if _, ok := seenFiles[file.Path]; ok {
			return packageVerifyResult{}, fmt.Errorf("package manifest contains duplicate file path %q", file.Path)
		}
		seenFiles[file.Path] = struct{}{}
		if err := verifyPackageManifestFile(cleanRoot, file); err != nil {
			return packageVerifyResult{}, err
		}
	}
	for _, rel := range append(append([]string{}, manifest.Layout.Binaries...), manifest.Layout.Entrypoints...) {
		if _, ok := seenFiles[rel]; !ok {
			return packageVerifyResult{}, fmt.Errorf("package manifest layout path %q is not covered by files checksums", rel)
		}
	}
	return packageVerifyResult{Root: cleanRoot, Files: len(manifest.Files)}, nil
}

func verifyPackageLayout(root string, manifest packageManifest) error {
	if !containsString(manifest.Layout.Binaries, "bin/hideout") {
		return errors.New("package manifest layout.binaries must include bin/hideout")
	}
	if !containsString(manifest.Layout.Entrypoints, "install.sh") {
		return errors.New("package manifest layout.entrypoints must include install.sh")
	}
	if !containsString(manifest.Layout.Entrypoints, "README.md") {
		return errors.New("package manifest layout.entrypoints must include README.md")
	}
	if !containsString(manifest.Layout.Directories, "schemas") {
		return errors.New("package manifest layout.directories must include schemas")
	}
	for _, rel := range manifest.Layout.Binaries {
		joined, err := packageRelativePath(root, rel)
		if err != nil {
			return fmt.Errorf("package manifest binary path %q: %w", rel, err)
		}
		if err := requirePackageRegularFile(joined, true); err != nil {
			return fmt.Errorf("package manifest binary %q: %w", rel, err)
		}
	}
	for _, rel := range manifest.Layout.Entrypoints {
		joined, err := packageRelativePath(root, rel)
		if err != nil {
			return fmt.Errorf("package manifest entrypoint path %q: %w", rel, err)
		}
		if err := requirePackageRegularFile(joined, false); err != nil {
			return fmt.Errorf("package manifest entrypoint %q: %w", rel, err)
		}
	}
	for _, rel := range manifest.Layout.Directories {
		joined, err := packageRelativePath(root, rel)
		if err != nil {
			return fmt.Errorf("package manifest directory path %q: %w", rel, err)
		}
		st, err := os.Lstat(joined)
		if err != nil {
			return fmt.Errorf("package manifest directory %q: %w", rel, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("package manifest directory %q must not be a symlink", rel)
		}
		if !st.IsDir() {
			return fmt.Errorf("package manifest directory %q is not a directory", rel)
		}
	}
	return nil
}

func verifyPackageManifestFile(root string, file packageManifestFile) error {
	joined, err := packageRelativePath(root, file.Path)
	if err != nil {
		return fmt.Errorf("package manifest file path %q: %w", file.Path, err)
	}
	switch file.Kind {
	case "binary", "linux-helper", "helper-manifest", "installer", "entrypoint", "schema":
	default:
		return fmt.Errorf("package manifest file %q has unsupported kind %q", file.Path, file.Kind)
	}
	requireExecutable := file.Kind == "binary" || file.Kind == "linux-helper" || file.Kind == "installer"
	if err := requirePackageRegularFile(joined, requireExecutable); err != nil {
		return fmt.Errorf("package manifest file %q: %w", file.Path, err)
	}
	if len(file.SHA256) != 64 {
		return fmt.Errorf("package manifest file %q has invalid sha256", file.Path)
	}
	data, err := os.ReadFile(joined)
	if err != nil {
		return fmt.Errorf("read package manifest file %q: %w", file.Path, err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != file.SHA256 {
		return fmt.Errorf("package checksum mismatch for %s: want %s got %s", file.Path, file.SHA256, got)
	}
	return nil
}

func packageRelativePath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("path must be package-relative")
	}
	if strings.Contains(rel, `\`) {
		return "", errors.New("path must use slash separators")
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path must stay inside the package")
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

func requirePackageRegularFile(path string, executable bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("must not be a symlink")
	}
	if !st.Mode().IsRegular() {
		return errors.New("is not a regular file")
	}
	if executable && st.Mode()&0o111 == 0 {
		return errors.New("is not executable")
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type initCommandOptions struct {
	profileName string
	backendName string
	networkMode string
	proxySecret string
	noInput     bool
	dryRun      bool
	tools       toolSupplyOptions
}

type toolSupplyOptions struct {
	presets     stringListFlag
	npmPackage  string
	npmCommands stringListFlag
}

func (a app) initCommand(args []string) error {
	opts, err := parseInitCommandOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	plan, err := core.PlanInit(inittask.Options{
		ProfileName:    opts.profileName,
		Backend:        opts.backendName,
		Network:        opts.networkMode,
		ProxySecretRef: opts.proxySecret,
		NoInput:        opts.noInput,
		ToolPresets:    []string(opts.tools.presets),
		NPMGlobals:     opts.tools.npmGlobals(),
	})
	if err != nil {
		return err
	}
	if opts.dryRun {
		writeInitPlan(a.stdout, "Hideout init plan", plan)
		return nil
	}
	result, err := core.ApplyInit(plan, inittask.ApplyOptions{
		NoInput: opts.noInput,
	})
	if err != nil {
		return err
	}
	writeInitResult(a.stdout, "Hideout init", result)
	return nil
}

func parseInitCommandOptions(args []string) (initCommandOptions, error) {
	opts := initCommandOptions{profileName: "default", backendName: "auto", networkMode: "direct"}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.profileName, "profile", "default", "profile name")
	fs.StringVar(&opts.backendName, "backend", "auto", "backend: auto/lima for isolation; native is a dev-only weak harness")
	fs.StringVar(&opts.networkMode, "network", "direct", "network mode")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref for tun2socks network mode")
	registerToolSupplyFlags(fs, &opts.tools)
	fs.BoolVar(&opts.noInput, "no-input", false, "do not ask for confirmation")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print init plan without applying")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected init argument %q", fs.Arg(0))
	}
	if err := opts.tools.validate(); err != nil {
		return opts, err
	}
	return opts, nil
}

func writeInitPlan(w io.Writer, title string, plan inittask.Plan) {
	fmt.Fprintln(w, title)
	fmt.Fprintf(w, "storage: %s\n", plan.StoreRoot)
	fmt.Fprintf(w, "profile: %s\n", plan.Profile)
	fmt.Fprintf(w, "backend: %s\n", plan.Backend)
	fmt.Fprintf(w, "network: %s\n", plan.Network)
	for _, task := range plan.Tasks {
		fmt.Fprintf(w, "task %s: %s risk=%s %s\n", task.Kind, task.Status, task.Risk, task.Message)
	}
}

func writeInitResult(w io.Writer, title string, result inittask.Result) {
	fmt.Fprintln(w, title)
	fmt.Fprintf(w, "storage: %s\n", result.Plan.StoreRoot)
	fmt.Fprintf(w, "profile: %s\n", result.Plan.Profile)
	fmt.Fprintf(w, "backend: %s\n", result.Plan.Backend)
	fmt.Fprintf(w, "network: %s\n", result.Plan.Network)
	if result.AuditPath != "" {
		fmt.Fprintf(w, "audit=%s\n", result.AuditPath)
	}
	for _, task := range result.Applied {
		fmt.Fprintf(w, "task %s: applied risk=%s %s\n", task.Kind, task.Risk, task.Message)
	}
	for _, task := range result.Skipped {
		fmt.Fprintf(w, "task %s: %s risk=%s %s\n", task.Kind, task.Status, task.Risk, task.Message)
	}
	writeInitNextSteps(w, result.Plan)
}

func writeInitNextSteps(w io.Writer, plan inittask.Plan) {
	if len(plan.NextSteps) == 0 {
		return
	}
	fmt.Fprintln(w, "next:")
	for _, step := range plan.NextSteps {
		if step.Command == "" {
			continue
		}
		if step.ID == "resolve-blocked" {
			fmt.Fprintf(w, "  resolve: %s\n", step.Command)
			continue
		}
		if step.ID == "doctor-check" {
			fmt.Fprintf(w, "  check: %s\n", step.Command)
			continue
		}
		if step.ID == "smoke-run" {
			fmt.Fprintf(w, "  smoke: %s\n", step.Command)
			continue
		}
		if step.ID == "cli-smoke" {
			fmt.Fprintf(w, "  cli: %s\n", step.Command)
			continue
		}
		label := strings.TrimSpace(step.Label)
		if label == "" {
			label = step.ID
		}
		fmt.Fprintf(w, "  %s: %s\n", label, step.Command)
	}
}

type runOptions struct {
	profileName           string
	backendName           string
	networkMode           string
	proxySecret           string
	workspace             string
	guestWorkspace        string
	auditPath             string
	allowWeakIsolation    bool
	allowUnsafeWorkspace  bool
	explainOnly           bool
	verbose               bool
	ephemeral             bool
	newEnvironment        bool
	resumeEnvironment     string
	removeEnvironment     bool
	hostFSGrantFlags      []string
	hostFSDenyFlags       []string
	noProfileHostFSGrants bool
	hostFSRun             hostfs.Config
	envPublic             map[string]string
	previewTargets        []string
	command               []string
}

type runEnvironment = manager.RunEnvironment

func (a app) runCommand(args []string, explainOnly bool) (retErr error) {
	opts, err := parseRunOptions(args, explainOnly)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	runPlan, err := core.PlanRun(manager.RunPlanOptions{
		ProfileName:          opts.profileName,
		Backend:              opts.backendName,
		NetworkMode:          opts.networkMode,
		ProxySecretRef:       opts.proxySecret,
		Workspace:            opts.workspace,
		GuestWorkspace:       opts.guestWorkspace,
		AllowUnsafeWorkspace: opts.allowUnsafeWorkspace,
		Ephemeral:            opts.ephemeral,
		Command:              opts.command,
	})
	if err != nil {
		return err
	}
	runtimeProfile := runPlan.RuntimeProfile
	if len(opts.envPublic) > 0 {
		if runtimeProfile.Env.Public == nil {
			runtimeProfile.Env.Public = map[string]string{}
		}
		for name, value := range opts.envPublic {
			runtimeProfile.Env.Public[name] = value
		}
		if err := runtimeProfile.Validate(); err != nil {
			return err
		}
		runPlan.RuntimeProfile = runtimeProfile
	}
	openTargets, endpointCandidates, endpointExposures, err := buildPreviewOpenOptions(runtimeProfile, opts.previewTargets)
	if err != nil {
		return err
	}
	opts.workspace = runPlan.Workspace
	opts.guestWorkspace = runPlan.GuestWorkspace
	backendName := runPlan.Backend
	if opts.explainOnly {
		return core.ExplainRun(runPlan, manager.RunExplainOptions{
			Environment: manager.RunEnvironmentOptions{
				New:            opts.newEnvironment,
				ResumeID:       opts.resumeEnvironment,
				RemoveAfterRun: opts.removeEnvironment,
			},
		}, func(explanation manager.RunExplanation) error {
			runSession := explanation.Session
			explain := explainText(runtimeProfile, opts, runSession.Layout, runSession.Environment, runSession.Env, runSession.ProfileDir, runSession.IdentityDir)
			fmt.Fprint(a.stdout, explain)
			return nil
		})
	}
	be := a.backend(backendName, opts)
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	result, err := core.ApplyRun(runCtx, runPlan, manager.ApplyRunOptions{
		Backend:                    be,
		RequestedBackend:           opts.backendName,
		AllowWeakIsolation:         opts.allowWeakIsolation,
		Environment:                manager.RunEnvironmentOptions{New: opts.newEnvironment, ResumeID: opts.resumeEnvironment, RemoveAfterRun: opts.removeEnvironment, Create: true},
		AuditPath:                  opts.auditPath,
		HostFSRun:                  opts.hostFSRun,
		DisableProfileHostFSGrants: opts.noProfileHostFSGrants,
		OpenTargets:                openTargets,
		EndpointCandidates:         endpointCandidates,
		EndpointExposures:          endpointExposures,
		OpenerForSession: func(runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, a.stdout, a.stderr)
		},
	})
	if err != nil {
		return err
	}
	if opts.verbose {
		a.writeRunResultSummary(result)
	}
	return nil
}

func (a app) writeRunResultSummary(result manager.RunResult) {
	if result.BoundarySummary == nil && !result.PreserveInstance {
		return
	}
	if result.EnvironmentID != "" {
		fmt.Fprintf(a.stderr, "Hideout environment: %s\n", result.EnvironmentID)
		if result.PreserveInstance {
			fmt.Fprintf(a.stderr, "resume: hideout run --resume %s -- <command>\n", result.EnvironmentID)
		}
	}
	if result.BoundarySummary == nil {
		return
	}
	fmt.Fprintln(a.stderr, "Hideout boundary:")
	if result.BoundarySummary.Evidence == "disabled" {
		fmt.Fprintln(a.stderr, "  audit: disabled - no boundary evidence")
		return
	}
	if result.BoundarySummary.Evidence == "unavailable" {
		fmt.Fprintln(a.stderr, "  audit: unavailable - no boundary evidence")
		return
	}
	if result.BoundarySummary.AuditPath != "" {
		fmt.Fprintf(a.stderr, "  audit: %s\n", result.BoundarySummary.AuditPath)
	}
	for _, capability := range result.BoundarySummary.Capabilities {
		fmt.Fprintf(a.stderr, "  %s: allowed=%d denied=%d", capability.Capability, capability.Allowed, capability.Denied)
		if capability.Capability == "hostfs" || capability.Unsupported > 0 {
			fmt.Fprintf(a.stderr, " unsupported=%d", capability.Unsupported)
		}
		if capability.Error > 0 {
			fmt.Fprintf(a.stderr, " error=%d", capability.Error)
		}
		if capability.AuditOnly > 0 {
			fmt.Fprintf(a.stderr, " auditOnly=%d", capability.AuditOnly)
		}
		if capability.Owner != "" {
			fmt.Fprintf(a.stderr, " owner=%s", capability.Owner)
		}
		if capability.Source != "" {
			fmt.Fprintf(a.stderr, " source=%s", capability.Source)
		}
		if capability.Lifetime != "" {
			fmt.Fprintf(a.stderr, " lifetime=%s", capability.Lifetime)
		}
		if capability.CloseReason != "" {
			fmt.Fprintf(a.stderr, " close=%s", capability.CloseReason)
		}
		if capability.EndpointCategory != "" {
			fmt.Fprintf(a.stderr, " endpoint=%s", capability.EndpointCategory)
		}
		fmt.Fprintln(a.stderr)
	}
}

func cleanupAuditDetails(result session.CleanupResult) map[string]any {
	return manager.CleanupAuditDetails(result)
}

func cleanupAuditType(path string) string {
	return manager.CleanupAuditType(path)
}

func presence(value string) string {
	if value == "" {
		return "absent"
	}
	return "present"
}

func runtimeIdentityDir(layout session.Layout, profileDir string, opts runOptions) string {
	return manager.RunIdentityDir(layout, profileDir, opts.ephemeral)
}

func selectRunEnvironment(store environment.Store, p profile.Profile, backendName string, opts runOptions, create bool) (runEnvironment, error) {
	return manager.SelectRunEnvironment(store, p, backendName, opts.workspace, opts.guestWorkspace, opts.ephemeral, manager.RunEnvironmentOptions{
		New:            opts.newEnvironment,
		ResumeID:       opts.resumeEnvironment,
		RemoveAfterRun: opts.removeEnvironment,
		Create:         create,
	})
}

func runEnvironmentSpec(p profile.Profile, backendName string, opts runOptions) environment.Spec {
	return manager.RunEnvironmentSpec(p, backendName, opts.workspace, opts.guestWorkspace)
}

func validateEnvironmentRecord(rec environment.Record, spec environment.Spec) error {
	return manager.ValidateEnvironmentRecord(rec, spec)
}

func resolveBackendName(name string) string {
	return manager.ResolveBackendName(name)
}

func resolveWorkspaceMapping(hostWorkspace, guestWorkspace string, p profile.Profile) (string, string, error) {
	return manager.ResolveWorkspaceMapping(hostWorkspace, guestWorkspace, p)
}

func networkDecision(plan netpolicy.Plan, err error) string {
	return manager.NetworkDecision(plan, err)
}

func guestSessionDirForBackend(backendName string) string {
	return manager.GuestSessionDirForBackend(backendName)
}

func localBypassHostsForBackend(backendName string) []string {
	return manager.LocalBypassHostsForBackend(backendName)
}

func brokerEndpointForBackend(backendName string, layout session.Layout) broker.Endpoint {
	return manager.BrokerEndpointForBackend(backendName, layout)
}

func brokerEndpointForGuest(backendName string, listen broker.Endpoint) (broker.Endpoint, error) {
	return manager.BrokerEndpointForGuest(backendName, listen)
}

func brokerEndpointForDoctorClient(endpoint broker.Endpoint) broker.Endpoint {
	if endpoint.Network != broker.EndpointTCP {
		return endpoint
	}
	host, port, err := net.SplitHostPort(endpoint.Address)
	if err != nil {
		return endpoint
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return broker.TCPEndpoint(net.JoinHostPort("127.0.0.1", port))
	}
	return endpoint
}

func appendBrokerEnv(env []string, endpoint broker.Endpoint, sessionID, token, socket string) []string {
	return manager.AppendBrokerEnv(env, endpoint, sessionID, token, socket)
}

func (a app) backend(name string, opts runOptions) backend.Backend {
	switch name {
	case "lima":
		controlOut := io.Discard
		controlErr := io.Discard
		if opts.verbose {
			controlOut = a.stdout
			controlErr = a.stderr
		}
		return lima.Backend{
			Stdout:        a.stdout,
			Stderr:        a.stderr,
			ControlStdout: controlOut,
			ControlStderr: controlErr,
			Stdin:         os.Stdin,
		}
	default:
		return native.Backend{
			AllowWeakIsolation: opts.allowWeakIsolation,
			Stdout:             a.stdout,
			Stderr:             a.stderr,
			Stdin:              os.Stdin,
		}
	}
}

func hostFSGrafts(policy hostfs.EffectivePolicy) []string {
	return manager.HostFSGrafts(policy)
}

func hostFSProfileForRun(p profile.Profile, opts runOptions) hostfs.Config {
	return manager.HostFSProfileForRun(p, opts.noProfileHostFSGrants)
}

func hostOpener(profileDir string, stdout, stderr io.Writer) hostopen.Opener {
	return hostopen.Opener{
		BrowserProfileDir: hostopen.BrowserProfileDir(profileDir),
		BrowserPath:       os.Getenv("HIDEOUT_BROWSER_PATH"),
		BrowserApp:        os.Getenv("HIDEOUT_BROWSER_APP"),
		DryRun:            os.Getenv("HIDEOUT_OPEN_DRY_RUN") == "1",
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func writeBrokerEndpoint(path string, endpoint broker.Endpoint) error {
	return manager.WriteBrokerEndpoint(path, endpoint)
}

func parseRunOptions(args []string, explainOnly bool) (runOptions, error) {
	opts := runOptions{profileName: "default", backendName: "auto", explainOnly: explainOnly}
	split := slices.Index(args, "--")
	flagArgs := args
	if split >= 0 {
		flagArgs = args[:split]
		opts.command = args[split+1:]
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.profileName, "profile", "default", "profile name")
	fs.StringVar(&opts.backendName, "backend", "auto", "backend")
	fs.StringVar(&opts.networkMode, "network", "", "network mode")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref")
	fs.StringVar(&opts.workspace, "workspace", "", "host workspace")
	fs.StringVar(&opts.guestWorkspace, "guest-workspace", "", "guest workspace")
	fs.StringVar(&opts.auditPath, "audit", "", "audit path or off")
	fs.BoolVar(&opts.allowWeakIsolation, "allow-weak-isolation", false, "allow native weak isolation")
	fs.BoolVar(&opts.allowUnsafeWorkspace, "allow-unsafe-workspace", false, "explicitly allow mounting a sensitive workspace root")
	fs.BoolVar(&opts.explainOnly, "explain", opts.explainOnly, "print the run boundary without executing the command")
	fs.BoolVar(&opts.verbose, "verbose", false, "print Hideout control-plane progress and run summary")
	fs.BoolVar(&opts.ephemeral, "ephemeral", false, "use session-local identity state for this run")
	fs.BoolVar(&opts.newEnvironment, "new", false, "create a new reusable environment")
	fs.StringVar(&opts.resumeEnvironment, "resume", "", "resume an environment id")
	fs.BoolVar(&opts.removeEnvironment, "rm", false, "remove the runtime environment after the command")
	var fsFlags stringListFlag
	fs.Var(&fsFlags, "fs", "run-scoped HostFS allow rule such as read:/absolute/path")
	var noFSFlags stringListFlag
	fs.Var(&noFSFlags, "no-fs", "run-scoped HostFS deny rule such as read:/absolute/path")
	fs.BoolVar(&opts.noProfileHostFSGrants, "no-profile-fs", false, "ignore profile HostFS grants for this run")
	var envFlags stringListFlag
	fs.Var(&envFlags, "env", "run-scoped public environment variable KEY=VALUE")
	var previewFlags stringListFlag
	fs.Var(&previewFlags, "preview", "open a preview for a profile endpoint candidate id or guest loopback endpoint")
	if err := fs.Parse(flagArgs); err != nil {
		return opts, err
	}
	grantInputs := appendHostFSFlagInputs(nil, "--fs", fsFlags, "run-scoped CLI allow")
	denyInputs := appendHostFSFlagInputs(nil, "--no-fs", noFSFlags, "run-scoped CLI deny")
	opts.hostFSGrantFlags = hostFSFlagValues(grantInputs)
	opts.hostFSDenyFlags = hostFSFlagValues(denyInputs)
	hostFSRun, err := parseHostFSRunPolicyFlags(grantInputs, denyInputs)
	if err != nil {
		return opts, err
	}
	opts.hostFSRun = hostFSRun
	envPublic, err := parseRunEnvFlags(envFlags)
	if err != nil {
		return opts, err
	}
	opts.envPublic = envPublic
	opts.previewTargets = append([]string(nil), previewFlags...)
	if opts.newEnvironment && strings.TrimSpace(opts.resumeEnvironment) != "" {
		return opts, errors.New("--new and --resume cannot be used together")
	}
	if opts.ephemeral && (opts.newEnvironment || strings.TrimSpace(opts.resumeEnvironment) != "") {
		return opts, errors.New("--ephemeral cannot be used with --new or --resume")
	}
	if split < 0 && fs.NArg() > 0 {
		opts.command = fs.Args()
	}
	return opts, nil
}

func parseRunEnvFlags(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, raw := range values {
		name, value, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, errors.New("--env must use KEY=VALUE")
		}
		name = strings.TrimSpace(name)
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("--env contains duplicate key %q", name)
		}
		out[name] = value
	}
	return out, nil
}

func buildPreviewOpenOptions(p profile.Profile, targets []string) ([]manager.RunOpenTargetOwner, []manager.RunEndpointCandidate, []manager.RunEndpointExposureRequest, error) {
	if len(targets) == 0 {
		return nil, nil, nil, nil
	}
	owners := []manager.RunOpenTargetOwner{{
		ID:   manager.OpenTargetPreviewOpen,
		Kind: manager.OpenTargetPreviewOpen,
	}}
	profileCandidates := map[string]profile.EndpointCandidate{}
	for _, candidate := range p.EndpointExposure.HostToGuest {
		profileCandidates[strings.TrimSpace(candidate.ID)] = candidate
	}
	var runCandidates []manager.RunEndpointCandidate
	var exposures []manager.RunEndpointExposureRequest
	for i, raw := range targets {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, nil, nil, errors.New("--preview cannot be empty")
		}
		candidateID := value
		if candidate, ok := profileCandidates[value]; ok {
			owner := strings.TrimSpace(candidate.Owner)
			if owner != manager.OpenTargetPreviewOpen {
				return nil, nil, nil, fmt.Errorf("--preview candidate %q belongs to owner %q, not preview.open", value, owner)
			}
		} else {
			targetAddress, err := normalizePreviewEndpoint(value)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("--preview %q: %w", value, err)
			}
			candidateID = fmt.Sprintf("manual_preview_%d", i+1)
			runCandidates = append(runCandidates, manager.RunEndpointCandidate{
				ID:            candidateID,
				Source:        manager.EndpointSourceManual,
				Owner:         manager.OpenTargetPreviewOpen,
				Proto:         "tcp",
				TargetAddress: targetAddress,
			})
		}
		exposures = append(exposures, manager.RunEndpointExposureRequest{
			CandidateID: candidateID,
			Owner:       manager.OpenTargetPreviewOpen,
			Kind:        manager.OpenTargetPreviewOpen,
			ClosePolicy: "session-end",
		})
	}
	return owners, runCandidates, exposures, nil
}

func normalizePreviewEndpoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("endpoint is required")
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", fmt.Errorf("preview URL scheme %q is unsupported", u.Scheme)
		}
		value = u.Host
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("must be host:port or http(s) loopback URL: %w", err)
	}
	if host == "localhost" {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("preview endpoint must use localhost or a loopback IP")
	}
	return net.JoinHostPort(host, port), nil
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type hostFSFlagInput struct {
	flagName string
	value    string
	reason   string
}

func appendHostFSFlagInputs(dst []hostFSFlagInput, flagName string, values []string, reason string) []hostFSFlagInput {
	for _, value := range values {
		dst = append(dst, hostFSFlagInput{flagName: flagName, value: value, reason: reason})
	}
	return dst
}

func hostFSFlagValues(inputs []hostFSFlagInput) []string {
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		values = append(values, input.value)
	}
	return values
}

func parseHostFSRunPolicyFlags(grants, deny []hostFSFlagInput) (hostfs.Config, error) {
	var config hostfs.Config
	for _, input := range grants {
		rule, err := parseHostFSRuleFlag(input)
		if err != nil {
			return hostfs.Config{}, err
		}
		config.Grants = append(config.Grants, rule)
	}
	for _, input := range deny {
		rule, err := parseHostFSRuleFlag(input)
		if err != nil {
			return hostfs.Config{}, err
		}
		config.Deny = append(config.Deny, rule)
	}
	if len(config.Grants) == 0 && len(config.Deny) == 0 {
		return config, nil
	}
	if err := hostfs.ValidateConfig(config, hostfs.SourceRun); err != nil {
		return hostfs.Config{}, err
	}
	return config, nil
}

func parseHostFSRuleFlag(input hostFSFlagInput) (hostfs.Rule, error) {
	kind, path, ok := strings.Cut(input.value, ":")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(path) == "" {
		return hostfs.Rule{}, fmt.Errorf("%s must use kind:/absolute/path", input.flagName)
	}
	rule := hostfs.Rule{
		HostPath: path,
		Reason:   input.reason,
	}
	switch kind {
	case "stat":
		rule.Ops = []hostfs.Op{hostfs.OpStat}
		rule.Scope = hostfs.ScopeExactFile
		if hostFSPathHasGlobMeta(path) {
			rule.Scope = hostfs.ScopeGlob
		}
	case "read":
		rule.Ops = []hostfs.Op{hostfs.OpRead}
		rule.Scope = hostfs.ScopeExactFile
		if hostFSPathHasGlobMeta(path) {
			rule.Scope = hostfs.ScopeGlob
		}
	case "list":
		if hostFSPathHasGlobMeta(path) {
			return hostfs.Rule{}, fmt.Errorf("%s kind %q does not support glob path selectors; use read: or stat:", input.flagName, kind)
		}
		rule.Ops = []hostfs.Op{hostfs.OpList}
		rule.Scope = hostfs.ScopeDir
	case "dir":
		if hostFSPathHasGlobMeta(path) {
			return hostfs.Rule{}, fmt.Errorf("%s kind %q does not support glob path selectors; use read: or stat:", input.flagName, kind)
		}
		rule.Ops = []hostfs.Op{hostfs.OpRead, hostfs.OpList}
		rule.Scope = hostfs.ScopeDir
	case "tree":
		if hostFSPathHasGlobMeta(path) {
			return hostfs.Rule{}, fmt.Errorf("%s kind %q does not support glob path selectors; use read: or stat:", input.flagName, kind)
		}
		rule.Ops = []hostfs.Op{hostfs.OpRead, hostfs.OpList}
		rule.Scope = hostfs.ScopeRecursiveDir
	default:
		return hostfs.Rule{}, fmt.Errorf("unsupported %s kind %q", input.flagName, kind)
	}
	return rule, nil
}

func hostFSPathHasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func explainText(p profile.Profile, opts runOptions, layout session.Layout, runEnv runEnvironment, env envpolicy.Result, profileDir, identityDir string) string {
	var b strings.Builder
	backendName := resolveBackendName(opts.backendName)
	registry, registryErr := commandProxyRegistry(p)
	displayEnv := env
	if backendName == "lima" {
		displayEnv.Env = lima.GuestEnv(env.Env)
		displayEnv.Synthetic = guestSyntheticEnv(env.Synthetic)
	}
	netPlan, netErr := netpolicy.Prepare(netpolicy.Spec{
		Profile:          p,
		Backend:          backendName,
		SessionDir:       layout.Dir,
		GuestSessionDir:  guestSessionDirForBackend(backendName),
		TargetEnv:        env.Env,
		Resolver:         netpolicy.EnvSecretResolver{},
		LocalBypassHosts: localBypassHostsForBackend(backendName),
		RuntimeVerify:    backendName == "lima",
		DryRun:           true,
	})
	fmt.Fprintf(&b, "Profile: %s\n", p.Name)
	if p.Metadata["profileId"] != "" || p.Metadata["identityId"] != "" {
		fmt.Fprintf(&b, "Identity: profileId=%s identityId=%s lineage=%s", p.Metadata["profileId"], p.Metadata["identityId"], p.Metadata["lineageMode"])
		if p.Metadata["createdFrom"] != "" {
			fmt.Fprintf(&b, " createdFrom=%s", p.Metadata["createdFrom"])
		}
		if p.Metadata["sourceIdentityId"] != "" {
			fmt.Fprintf(&b, " sourceIdentityId=%s", p.Metadata["sourceIdentityId"])
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "Backend: %s", backendName)
	if backendName == "native" {
		fmt.Fprintf(&b, " (Phase 1A native backend is weak isolation unless --allow-weak-isolation is used)")
	} else if backendName == "lima" {
		fmt.Fprintf(&b, " (target command resolves inside the Lima guest)")
	}
	fmt.Fprintln(&b)
	if backendName == "lima" {
		scope := "session scoped"
		if runEnv.Active {
			scope = "environment scoped"
			if runEnv.Created {
				scope = "environment scoped, new on run"
			}
			if runEnv.RemoveAfterRun {
				scope = "environment scoped, removed after run"
			}
		}
		fmt.Fprintf(&b, "Lima instance: %s (%s)\n", limaInstanceName(p, layout, opts, runEnv), scope)
		if runEnv.Active {
			fmt.Fprintf(&b, "Environment: %s status=%s workspace=%s\n", runEnv.Record.ID, explainValue(runEnv.Record.Status, "ready"), runEnv.Record.Workspace)
		}
	}
	if opts.ephemeral {
		fmt.Fprintf(&b, "Identity storage: ephemeral session-local at %s\n", identityDir)
	} else {
		fmt.Fprintf(&b, "Identity storage: persistent profile at %s\n", profileDir)
	}
	if len(opts.command) > 0 {
		fmt.Fprintf(&b, "Target command: %s\n", strings.Join(opts.command, " "))
		if backendName == "lima" {
			fmt.Fprintln(&b, "Target resolution: inside Lima guest PATH; no host fallback")
		} else {
			fmt.Fprintln(&b, "Target resolution: native host PATH because weak native backend was explicitly selected")
		}
	}
	fmt.Fprintf(&b, "Workspace: host=%s guest=%s mode=%s pathMode=%s\n", opts.workspace, opts.guestWorkspace, p.Workspace.Mode, p.Workspace.PathMode)
	fmt.Fprintln(&b, "Workspace visibility: guest can read/write mapped workspace contents, including project-local secrets")
	if p.Workspace.PathMode == "alias" {
		fmt.Fprintln(&b, "Workspace path privacy: alias mode uses a neutral guest path for the workspace")
	} else {
		fmt.Fprintln(&b, "Workspace path privacy: preserve mode may expose host path shape")
	}
	hostFSProfile := hostFSProfileForRun(p, opts)
	hostFSPolicy, hostFSErr := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile, Run: opts.hostFSRun})
	if hostFSErr != nil {
		fmt.Fprintf(&b, "HostFS Portal: invalid policy (%s)\n", hostFSErr)
	} else {
		fmt.Fprintf(&b, "HostFS Portal: roots=/hideout/hostfs,/Users,/Volumes,/private default=hidden profileGrants=%d runGrants=%d totalGrants=%d denyRules=%d write=unsupported\n", len(hostFSProfile.Grants), len(opts.hostFSRun.Grants), len(hostFSPolicy.Grants), len(hostFSPolicy.Deny))
		if opts.noProfileHostFSGrants {
			fmt.Fprintln(&b, "HostFS profile grants: disabled for this run; profile deny rules still apply")
		}
		if len(opts.hostFSRun.Deny) > 0 {
			fmt.Fprintf(&b, "HostFS run denies: %d temporary deny rule(s) active\n", len(opts.hostFSRun.Deny))
		}
		if len(hostFSPolicy.Grants) == 0 {
			fmt.Fprintln(&b, "HostFS data plane: inactive because no HostFS grants are active")
		} else if backendName == "lima" {
			fmt.Fprintln(&b, "HostFS data plane: enabled for Lima through hideout-hostfsd FUSE; grants do not create backend mounts")
		} else {
			fmt.Fprintln(&b, "HostFS data plane: not mounted by the native weak backend")
		}
	}
	fmt.Fprintf(&b, "Guest home: %s\n", displayEnv.Synthetic["HOME"])
	fmt.Fprintf(&b, "Identity env: user=%s hostname=%s timezone=%s locale=%s\n",
		displayEnv.Synthetic["USER"],
		displayEnv.Synthetic["HOSTNAME"],
		displayEnv.Synthetic["TZ"],
		displayEnv.Synthetic["LANG"],
	)
	machineScope := "persistent profile"
	if opts.ephemeral {
		machineScope = "ephemeral session"
	}
	machineStatus := "missing"
	if p.Metadata["machineId"] != "" {
		machineStatus = "present"
	}
	fmt.Fprintf(&b, "Machine identity: generated machine-id %s in %s identity root (value hidden)\n", machineStatus, machineScope)
	fmt.Fprintf(&b, "Config/cache/data: config=%s cache=%s data=%s tmp=%s\n",
		displayEnv.Synthetic["XDG_CONFIG_HOME"],
		displayEnv.Synthetic["XDG_CACHE_HOME"],
		displayEnv.Synthetic["XDG_DATA_HOME"],
		displayEnv.Synthetic["TMPDIR"],
	)
	fmt.Fprintf(&b, "Git identity: name=%s email=%s\n", p.Git.UserName, p.Git.UserEmail)
	fmt.Fprintf(&b, "Synthetic env: %s\n", explainMapKeys(displayEnv.Synthetic))
	fmt.Fprintf(&b, "Inherited env: %s\n", explainList(displayEnv.Inherited))
	fmt.Fprintf(&b, "Denied env observed: %s\n", explainList(displayEnv.Denied))
	fmt.Fprintf(&b, "Denied env patterns: %s\n", explainList(p.Env.Deny))
	fmt.Fprintf(&b, "Proxy env in target: absent\n")
	fmt.Fprintf(&b, "Network: %s", netPlan.Mode)
	if netPlan.Mode == netpolicy.ModeDirect {
		fmt.Fprint(&b, " (host network identity may be visible)")
	} else if netPlan.Mode == netpolicy.ModeTun2Socks {
		if netPlan.RuntimeVerify {
			fmt.Fprint(&b, " (hidden proxy via guest-side tun2socks; route verified inside guest before target launch)")
		} else {
			fmt.Fprint(&b, " (hidden proxy via guest-side tun2socks; fail closed until routing is verified)")
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Network plan: engine=%s verified=%t runtimeVerify=%t failClosed=%t reason=%s\n", explainValue(netPlan.Engine, "none"), netPlan.Verified, netPlan.RuntimeVerify, netPlan.FailClosed, explainValue(netPlan.Reason, "none"))
	fmt.Fprintf(&b, "Network DNS policy: %s\n", explainValue(netPlan.DNSPolicy, "none"))
	if len(netPlan.LocalBypassHosts) > 0 {
		fmt.Fprintf(&b, "Network local bypass: %s\n", strings.Join(netPlan.LocalBypassHosts, ","))
	}
	if netErr != nil {
		fmt.Fprintf(&b, "Network plan error: %s\n", netErr)
	}
	if p.Network.ProxySecretRef != "" {
		fmt.Fprintf(&b, "Network proxy secret: %s (value hidden)\n", p.Network.ProxySecretRef)
	}
	if netPlan.GuestBootstrapPath != "" {
		fmt.Fprintf(&b, "Network bootstrap: %s\n", netPlan.GuestBootstrapPath)
	}
	fmt.Fprintf(&b, "Tool presets: %s\n", strings.Join(lima.EffectiveToolPresetNames(p.Tools.Presets), ","))
	if registryErr != nil {
		fmt.Fprintf(&b, "Command proxy: invalid (%s) via %s\n", registryErr, explainBrokerEndpoint(backendName, layout))
	} else {
		fmt.Fprintf(&b, "Command proxy: %s via %s\n", explainCommandProxy(registry), explainBrokerEndpoint(backendName, layout))
	}
	fmt.Fprintln(&b, "Command proxy scope: registered commands only; ordinary guest processes are not fully audited in Phase 1")
	fmt.Fprintln(&b, "Host broker capability: host.open allows external http/https URLs and mapped workspace files only")
	fmt.Fprintf(&b, "Browser profile: isolated at %s\n", hostopen.BrowserProfileDir(identityDir))
	fmt.Fprintln(&b, "Host browser profile: real default browser profile is not used by default")
	fmt.Fprintln(&b, "Host browser network: localhost, loopback, private, CGNAT, benchmarking, link-local, multicast, .local, and .localhost URL targets are denied before host open")
	fmt.Fprintln(&b, "Host browser control: no DevTools or remote-debugging port is exposed to the guest in Phase 1")
	fmt.Fprintf(&b, "Audit: %s\n", resolveAuditPath(p, opts, layout))
	fmt.Fprintf(&b, "Session: %s\n", layout.ID)
	if backendName == "native" {
		fmt.Fprintln(&b, "Known limitation: native backend does not provide VM/container filesystem isolation.")
		fmt.Fprintln(&b, "Known limitation: native backend may still expose host OS identity APIs such as kernel hostname, OS user database, and system machine-id.")
	} else if backendName == "lima" {
		fmt.Fprintln(&b, "Known limitation: target command must exist inside the Lima guest or be installed by a tool preset.")
	}
	fmt.Fprintln(&b, "Known limitation: Phase 1 does not audit every child process inside the guest.")
	fmt.Fprintln(&b, "Known limitation: workspace secrets remain visible when they are inside the mounted workspace.")
	return b.String()
}

func guestSyntheticEnv(synthetic map[string]string) map[string]string {
	out := make(map[string]string, len(synthetic))
	for k, v := range synthetic {
		out[k] = v
	}
	out["HOME"] = lima.GuestProfileDir + "/home"
	out["TMPDIR"] = lima.GuestSessionDir + "/tmp"
	out["XDG_CONFIG_HOME"] = lima.GuestProfileDir + "/config"
	out["XDG_CACHE_HOME"] = lima.GuestProfileDir + "/cache"
	out["XDG_DATA_HOME"] = lima.GuestProfileDir + "/data"
	out["PATH"] = lima.GuestSessionDir + "/shims:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	return out
}

func explainList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func explainMapKeys(values map[string]string) string {
	if len(values) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func explainValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func resolveAuditPath(p profile.Profile, opts runOptions, layout session.Layout) string {
	return manager.ResolveRunAuditPath(p, opts.auditPath, layout)
}

func explainBrokerEndpoint(backendName string, layout session.Layout) string {
	if backendName == "lima" {
		return "tcp://host.lima.internal:<allocated-port>"
	}
	return broker.UnixEndpoint(layout.BrokerSock).String()
}

func explainCommandProxy(registry cmdproxy.Registry) string {
	return strings.Join(registry.ShimNames(), ", ") + " -> " + cmdproxy.ActionHostOpen
}

func commandProxyRegistry(p profile.Profile) (cmdproxy.Registry, error) {
	return cmdproxy.RegistryFromProfile(p)
}

func materializeShims(dir, backendName string, registry cmdproxy.Registry, netPlan netpolicy.Plan) error {
	return manager.MaterializeCommandProxyShims(dir, backendName, registry, netPlan)
}

func materializeHostFSD(dir, backendName string, enabled bool) error {
	return manager.MaterializeHostFSD(dir, backendName, enabled)
}

func resolveShimPath() string {
	return manager.ResolveShimPath()
}

func resolveLinuxShimPath() string {
	return manager.ResolveLinuxShimPath()
}

func resolveLinuxTun2SocksPath() string {
	return manager.ResolveLinuxTun2SocksPath()
}

func resolveLinuxHostFSDPath() string {
	return manager.ResolveLinuxHostFSDPath()
}

type linuxShimBuildOptions struct {
	out    string
	goarch string
	source string
}

func (a app) shim(args []string) error {
	if len(args) > 0 && args[0] == "build-linux" {
		return a.buildLinuxShim(args[1:])
	}
	command, commandArgs, err := cmdproxy.DefaultRegistry().ResolveInvocation("hideout-shim", args)
	if err != nil {
		return err
	}
	normalized, err := cmdproxy.DefaultRegistry().Normalize(command, commandArgs, mustGetwd())
	if err != nil {
		return err
	}
	endpoint, err := brokerEndpointFromEnv()
	if err != nil {
		return err
	}
	sessionID := os.Getenv(broker.EnvSession)
	token := os.Getenv(broker.EnvToken)
	if sessionID == "" || token == "" {
		return errors.New("broker environment is missing")
	}
	requestID, err := broker.NewRequestID()
	if err != nil {
		return err
	}
	resp := broker.ClientOpenEndpoint(context.Background(), endpoint, broker.Request{
		ID:              requestID,
		SessionID:       sessionID,
		CapabilityToken: token,
		Subject:         normalized.Subject,
		Command:         normalized.Command,
		Argv:            normalized.Argv,
		Route:           normalized.Route,
		Action:          normalized.Action,
		Args:            normalized.Payload,
	})
	if resp.Stdout != "" {
		fmt.Fprint(a.stdout, resp.Stdout)
	}
	if resp.Stderr != "" {
		fmt.Fprintln(a.stderr, resp.Stderr)
	}
	if resp.ExitCode != 0 {
		return fmt.Errorf("shim %s failed with exit code %d", normalized.Command, resp.ExitCode)
	}
	return nil
}

func (a app) buildLinuxShim(args []string) error {
	opts := linuxShimBuildOptions{
		goarch: runtime.GOARCH,
		source: ".",
	}
	fs := flag.NewFlagSet("shim build-linux", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.out, "out", "", "output path for linux hideout-shim")
	fs.StringVar(&opts.goarch, "goarch", opts.goarch, "linux target GOARCH")
	fs.StringVar(&opts.source, "source", opts.source, "Hideout source repository")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout shim build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	}
	if strings.TrimSpace(opts.goarch) == "" {
		return errors.New("linux shim GOARCH is required")
	}
	if strings.TrimSpace(opts.out) == "" {
		var err error
		opts.out, err = defaultLinuxShimPath(opts.goarch)
		if err != nil {
			return err
		}
	}
	if err := buildLinuxShimBinary(opts); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, opts.out)
	return nil
}

func defaultLinuxShimPath(goarch string) (string, error) {
	return manager.DefaultLinuxShimPath(goarch)
}

func (a app) hostfsd(args []string) error {
	if len(args) > 0 && args[0] == "build-linux" {
		return a.buildLinuxHostFSD(args[1:])
	}
	return errors.New("usage: hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
}

func (a app) buildLinuxHostFSD(args []string) error {
	opts := linuxShimBuildOptions{
		goarch: runtime.GOARCH,
		source: ".",
	}
	fs := flag.NewFlagSet("hostfsd build-linux", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.out, "out", "", "output path for linux hideout-hostfsd")
	fs.StringVar(&opts.goarch, "goarch", opts.goarch, "linux target GOARCH")
	fs.StringVar(&opts.source, "source", opts.source, "Hideout source repository")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	}
	if strings.TrimSpace(opts.goarch) == "" {
		return errors.New("linux hostfsd GOARCH is required")
	}
	if strings.TrimSpace(opts.out) == "" {
		var err error
		opts.out, err = defaultLinuxHostFSDPath(opts.goarch)
		if err != nil {
			return err
		}
	}
	if err := buildLinuxCommandBinary(opts, "hideout-hostfsd"); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, opts.out)
	return nil
}

func defaultLinuxHostFSDPath(goarch string) (string, error) {
	return manager.DefaultLinuxHostFSDPath(goarch)
}

func buildLinuxShimBinary(opts linuxShimBuildOptions) error {
	return buildLinuxCommandBinary(opts, "hideout-shim")
}

func buildLinuxCommandBinary(opts linuxShimBuildOptions, command string) error {
	return helperbin.BuildLinuxCommand(helperbin.BuildOptions{
		Out:     opts.out,
		GOARCH:  opts.goarch,
		Source:  opts.source,
		Command: command,
	})
}

func brokerEndpointFromEnv() (broker.Endpoint, error) {
	if raw := os.Getenv(broker.EnvEndpoint); raw != "" {
		return broker.ParseEndpoint(raw)
	}
	if sock := os.Getenv(broker.EnvSock); sock != "" {
		return broker.UnixEndpoint(sock), nil
	}
	return broker.Endpoint{}, errors.New("broker environment is missing")
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func (a app) doctor(args []string) error {
	opts, err := parseDoctorOptions(args)
	if err != nil {
		return err
	}
	if !opts.fix && opts.tools.hasChanges() {
		return errors.New("--tool-preset, --npm-package, and --npm-command require doctor --fix")
	}
	if opts.fix {
		return a.doctorFix(opts)
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	failed := false
	report := func(name, status, message string) {
		if message == "" {
			fmt.Fprintf(a.stdout, "%s: %s\n", name, status)
		} else {
			fmt.Fprintf(a.stdout, "%s: %s %s\n", name, status, message)
		}
		if status == "error" {
			failed = true
		}
	}
	fmt.Fprintln(a.stdout, "Hideout doctor")
	fmt.Fprintf(a.stdout, "storage: %s\n", store.Root)
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		report("store", "error", err.Error())
		return errors.New("doctor found errors")
	}
	report("store", "ok", "writable")
	checkManager(store, report)

	p, profileLoaded := loadDoctorProfile(store, opts.profileName, report)
	if opts.networkMode != "" {
		p.Network.Mode = opts.networkMode
	}
	if opts.proxySecret != "" {
		p.Network.ProxySecretRef = opts.proxySecret
	}
	if err := p.Validate(); err != nil {
		report("profile", "error", err.Error())
	}
	runtimeProfile := p
	if opts.ephemeral {
		runtimeProfile, err = profile.EphemeralIdentityProfile(p)
		if err != nil {
			report("identity", "error", err.Error())
		}
	}
	workspace, guestWorkspace, err := resolveWorkspaceMapping(opts.workspace, opts.guestWorkspace, runtimeProfile)
	if err != nil {
		report("workspace", "error", err.Error())
	}
	if err == nil {
		if safetyErr := manager.ValidateWorkspaceMountSafety(workspace, store.Root); safetyErr != nil {
			report("workspace", "error", safetyErr.Error())
		}
	}
	checkWorkspace(workspace, guestWorkspace, runtimeProfile, report)

	layout, err := session.New(store.Root)
	if err != nil {
		report("session", "error", err.Error())
		if failed {
			return errors.New("doctor found errors")
		}
		return nil
	}
	defer os.RemoveAll(layout.Dir)

	backendName := resolveBackendName(opts.backendName)
	checkBackend(backendName, report)
	profileDir := store.ProfileDir(p.Name)
	identityDir := runtimeIdentityDir(layout, profileDir, runOptions{ephemeral: opts.ephemeral})
	if opts.ephemeral && runtimeProfile.Metadata["machineId"] != "" {
		if err := profile.MaterializeIdentityState(identityDir, runtimeProfile); err != nil {
			report("identity", "error", err.Error())
		}
	}
	checkIdentityState(runtimeProfile, identityDir, opts.ephemeral, profileLoaded, report)
	checkMountPlan(backendName, runtimeProfile, layout, workspace, guestWorkspace, identityDir, report)
	checkLimaGeneratedConfig(backendName, runtimeProfile, layout, workspace, guestWorkspace, identityDir, report)
	env := envpolicy.Build(envpolicy.Spec{
		Profile:    runtimeProfile,
		ProfileDir: identityDir,
		SessionDir: layout.Dir,
		ShimDir:    layout.ShimDir,
	})
	checkEnv(env, report)
	checkPolicy(runtimeProfile, profileDir, report)
	checkNetwork(runtimeProfile, backendName, layout, env, report)
	checkBroker(runtimeProfile, backendName, layout, workspace, guestWorkspace, profileDir, report)
	checkCommandProxyRuntime(backendName, report)
	checkHostFSRuntime(backendName, runtimeProfile, report)
	checkHostOpen(runtimeProfile, identityDir, report)
	if !profileLoaded {
		report("profile-init", "warn", "run or profile init will materialize profile state")
	}
	if failed {
		return errors.New("doctor found errors")
	}
	return nil
}

func checkManager(store profile.Store, report func(string, string, string)) {
	overview, err := manager.New(store).Overview(context.Background())
	if err != nil {
		report("manager", "error", err.Error())
		return
	}
	report("manager", "ok", fmt.Sprintf(
		"profiles=%d sessions=%d backends=%d availableBackends=%d commandProxies=%d secrets=%d",
		len(overview.Profiles),
		len(overview.Sessions),
		len(overview.Backends),
		availableBackends(overview.Backends),
		len(overview.Capabilities.CommandProxies),
		len(overview.Secrets),
	))
}

func availableBackends(backends []manager.BackendSummary) int {
	count := 0
	for _, backend := range backends {
		if backend.Available {
			count++
		}
	}
	return count
}

func limaInstanceName(p profile.Profile, layout session.Layout, opts runOptions, runEnv runEnvironment) string {
	if runEnv.Active && runEnv.InstanceName != "" {
		return runEnv.InstanceName
	}
	return lima.InstanceNameForSession(p.Name, layout.ID)
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type doctorOptions struct {
	profileName    string
	backendName    string
	networkMode    string
	proxySecret    string
	workspace      string
	guestWorkspace string
	ephemeral      bool
	fix            bool
	dryRun         bool
	tools          toolSupplyOptions
}

func parseDoctorOptions(args []string) (doctorOptions, error) {
	opts := doctorOptions{profileName: "default", backendName: "auto"}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.profileName, "profile", "default", "profile name")
	fs.StringVar(&opts.backendName, "backend", "auto", "backend")
	fs.StringVar(&opts.networkMode, "network", "", "network mode")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref")
	fs.StringVar(&opts.workspace, "workspace", "", "host workspace")
	fs.StringVar(&opts.guestWorkspace, "guest-workspace", "", "guest workspace")
	registerToolSupplyFlags(fs, &opts.tools)
	fs.BoolVar(&opts.ephemeral, "ephemeral", false, "diagnose session-local identity state")
	fs.BoolVar(&opts.fix, "fix", false, "apply safe initialization repairs")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print fix plan without applying")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected doctor argument %q", fs.Arg(0))
	}
	if err := opts.tools.validate(); err != nil {
		return opts, err
	}
	return opts, nil
}

func (a app) doctorFix(opts doctorOptions) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	networkMode := opts.networkMode
	if networkMode == "" {
		networkMode = "direct"
	}
	core := manager.New(store)
	plan, err := core.PlanDoctorFix(inittask.Options{
		ProfileName:    opts.profileName,
		Backend:        opts.backendName,
		Network:        networkMode,
		ProxySecretRef: opts.proxySecret,
		NoInput:        true,
		ToolPresets:    []string(opts.tools.presets),
		NPMGlobals:     opts.tools.npmGlobals(),
	})
	if err != nil {
		return err
	}
	if opts.dryRun {
		writeInitPlan(a.stdout, "Hideout doctor fix plan", plan)
		return nil
	}
	result, err := core.ApplyDoctorFix(plan, inittask.ApplyOptions{NoInput: true})
	if err != nil {
		return err
	}
	writeInitResult(a.stdout, "Hideout doctor fix", result)
	return nil
}

func registerToolSupplyFlags(fs *flag.FlagSet, opts *toolSupplyOptions) {
	fs.Var(&opts.presets, "tool-preset", "tool preset to add to the profile; may be repeated")
	fs.StringVar(&opts.npmPackage, "npm-package", "", "npm package spec for one global CLI tool")
	fs.Var(&opts.npmCommands, "npm-command", "command expected after npm global install; may be repeated")
}

func (opts toolSupplyOptions) validate() error {
	if strings.TrimSpace(opts.npmPackage) == "" && len(opts.npmCommands) > 0 {
		return errors.New("--npm-command requires --npm-package")
	}
	if strings.TrimSpace(opts.npmPackage) != "" && len(opts.npmCommands) == 0 {
		return errors.New("--npm-package requires at least one --npm-command")
	}
	p := profile.Default("tool-check")
	if len(opts.presets) > 0 {
		p.Tools.Presets = []string(opts.presets)
	}
	p.Tools.NPMGlobals = opts.npmGlobals()
	return p.Validate()
}

func (opts toolSupplyOptions) hasChanges() bool {
	return len(opts.presets) > 0 || strings.TrimSpace(opts.npmPackage) != "" || len(opts.npmCommands) > 0
}

func (opts toolSupplyOptions) npmGlobals() []profile.NPMGlobalPackage {
	packageSpec := strings.TrimSpace(opts.npmPackage)
	if packageSpec == "" {
		return nil
	}
	return []profile.NPMGlobalPackage{{
		Package:  packageSpec,
		Commands: append([]string(nil), opts.npmCommands...),
	}}
}

func loadDoctorProfile(store profile.Store, name string, report func(string, string, string)) (profile.Profile, bool) {
	p, err := store.Load(name)
	if err == nil {
		report("profile", "ok", name)
		return p, true
	}
	if errors.Is(err, os.ErrNotExist) {
		report("profile", "warn", fmt.Sprintf("%s missing; using defaults for diagnostics", name))
		return profile.Default(name), false
	}
	report("profile", "error", err.Error())
	return profile.Default(name), false
}

func checkWorkspace(host, guest string, p profile.Profile, report func(string, string, string)) {
	if host == "" {
		report("workspace", "error", "workspace path is empty")
		return
	}
	st, err := os.Stat(host)
	if err != nil {
		report("workspace", "error", err.Error())
		return
	}
	if !st.IsDir() {
		report("workspace", "error", "workspace is not a directory")
		return
	}
	report("workspace", "ok", fmt.Sprintf("host=%s guest=%s mode=%s pathMode=%s", host, guest, p.Workspace.Mode, p.Workspace.PathMode))
}

func checkIdentityState(p profile.Profile, identityDir string, ephemeral, profileLoaded bool, report func(string, string, string)) {
	mode := "persistent"
	if ephemeral {
		mode = "ephemeral"
	}
	if !profileLoaded && !ephemeral {
		report("identity", "warn", "profile identity state is not materialized yet")
		return
	}
	if identityDir == "" {
		report("identity", "error", "identity root is empty")
		return
	}
	machineID := p.Metadata["machineId"]
	if machineID == "" {
		report("identity", "error", "metadata.machineId is missing")
		return
	}
	data, err := os.ReadFile(filepath.Join(identityDir, "machine", "machine-id"))
	if err != nil {
		report("identity", "error", err.Error())
		return
	}
	if strings.TrimSpace(string(data)) != machineID {
		report("identity", "error", "machine-id file does not match runtime identity metadata")
		return
	}
	parts := []string{
		"mode=" + mode,
		"root=" + identityDir,
		"identityId=" + p.Metadata["identityId"],
		"lineage=" + p.Metadata["lineageMode"],
	}
	if p.Metadata["sourceIdentityId"] != "" {
		parts = append(parts, "sourceIdentityId="+p.Metadata["sourceIdentityId"])
	}
	report("identity", "ok", strings.Join(parts, " "))
}

func checkBackend(backendName string, report func(string, string, string)) {
	switch backendName {
	case "lima":
		if err := (lima.Backend{}).Available(context.Background()); err != nil {
			report("backend", "error", "lima unavailable: "+err.Error())
			return
		}
		report("backend", "ok", "lima available")
	case "native":
		report("backend", "warn", "native is weak isolation and requires --backend native --allow-weak-isolation for run")
	default:
		report("backend", "error", fmt.Sprintf("%s is not implemented", backendName))
	}
}

func checkMountPlan(backendName string, p profile.Profile, layout session.Layout, hostRoot, guestRoot, profileDir string, report func(string, string, string)) {
	switch backendName {
	case "native":
		report("mount", "ok", "native weak backend has no VM mount plan; run still requires explicit weak isolation")
	case "lima":
		if hostRoot == "" || guestRoot == "" {
			report("mount", "error", "workspace mapping is unavailable")
			return
		}
		cfg := lima.ConfigFromRunSpec(backend.RunSpec{
			Profile:    p,
			HostWork:   hostRoot,
			GuestWork:  guestRoot,
			ProfileDir: profileDir,
			SessionDir: layout.Dir,
		})
		workspaceMounts := 0
		for _, m := range cfg.Mounts {
			if m.Location == hostRoot && m.MountPoint == guestRoot && m.Writable {
				workspaceMounts++
			}
			if err := validateRuntimeMount("profile", profileDir, m.Location, []string{"home", "cache", "config", "data", "browser", "machine"}); err != nil {
				report("mount", "error", err.Error())
				return
			}
			if err := validateRuntimeMount("session", layout.Dir, m.Location, []string{"tmp", "shims", "network", "bootstrap"}); err != nil {
				report("mount", "error", err.Error())
				return
			}
			if strings.HasPrefix(filepath.Base(m.Location), ".") && m.Location != hostRoot {
				report("mount", "error", fmt.Sprintf("hidden host path %q must not be mounted by default", m.Location))
				return
			}
		}
		if workspaceMounts != 1 {
			report("mount", "error", fmt.Sprintf("expected one writable workspace mount, got %d", workspaceMounts))
			return
		}
		report("mount", "ok", fmt.Sprintf("lima mounts=%d workspace=%s profileRuntimeOnly=true sessionRuntimeOnly=true", len(cfg.Mounts), guestRoot))
	default:
		report("mount", "error", fmt.Sprintf("%s is not implemented", backendName))
	}
}

func validateRuntimeMount(domain, root, location string, allowedTopLevel []string) error {
	if root == "" || location == "" {
		return nil
	}
	rel, err := filepath.Rel(root, location)
	if err != nil || pathEscapesRoot(rel) {
		return nil
	}
	if rel == "." {
		return fmt.Errorf("%s root %q must not be mounted as a whole", domain, root)
	}
	top := rel
	if before, _, ok := strings.Cut(rel, string(filepath.Separator)); ok {
		top = before
	}
	if !slices.Contains(allowedTopLevel, top) {
		return fmt.Errorf("%s control-plane path %q must not be mounted", domain, location)
	}
	return nil
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkLimaGeneratedConfig(backendName string, p profile.Profile, layout session.Layout, hostRoot, guestRoot, identityRoot string, report func(string, string, string)) {
	if backendName != "lima" {
		return
	}
	if hostRoot == "" || guestRoot == "" {
		return
	}
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		return
	}
	configPath := filepath.Join(layout.Dir, "doctor-lima.yaml")
	if err := lima.WriteConfig(configPath, lima.ConfigFromRunSpec(backend.RunSpec{
		Profile:      p,
		HostWork:     hostRoot,
		GuestWork:    guestRoot,
		ProfileDir:   identityRoot,
		SessionDir:   layout.Dir,
		IdentityRoot: identityRoot,
	})); err != nil {
		report("lima-config", "error", "could not write generated YAML: "+doctorDiagnostic(nil, err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, limactl, "validate", configPath)
	cmd.Env = lima.HostCommandEnv(os.Environ())
	data, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		report("lima-config", "error", "limactl validate timed out")
		return
	}
	if err != nil {
		report("lima-config", "error", "generated YAML failed validation: "+doctorDiagnostic(data, err))
		return
	}
	report("lima-config", "ok", "generated YAML validates")
}

func doctorDiagnostic(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message == "" && err != nil {
		message = err.Error()
	}
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return "unknown error"
	}
	const maxDiagnosticLen = 240
	if len(message) > maxDiagnosticLen {
		message = message[:maxDiagnosticLen] + "..."
	}
	return message
}

func checkEnv(env envpolicy.Result, report func(string, string, string)) {
	if netpolicy.ContainsProxyEnv(env.Env) {
		report("env", "error", "target env contains proxy variables")
		return
	}
	if containsHideoutSecretEnv(env.Env) {
		report("env", "error", "target env contains hideout secret variables")
		return
	}
	report("env", "ok", fmt.Sprintf("synthetic=%d inherited=%d denied=%d proxyEnv=absent secretEnv=absent", len(env.Synthetic), len(env.Inherited), len(env.Denied)))
}

func containsHideoutSecretEnv(env []string) bool {
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(name, "HIDEOUT_SECRET_") {
			return true
		}
	}
	return false
}

func checkPolicy(p profile.Profile, profileDir string, report func(string, string, string)) {
	evaluator := policy.NewEvaluator(p)
	if _, err := evaluator.Validate(policy.Proposal{
		Decision:  policy.AuditOnly,
		Route:     policy.GuestDirect,
		Action:    "guest.exec",
		Resources: []string{"guest-command:doctor"},
		Reason:    "doctor top-level command policy check",
	}); err != nil {
		report("policy", "error", err.Error())
		return
	}
	if _, err := evaluator.Validate(networkConnectProposal(p.Network.Mode, "doctor network policy check")); err != nil {
		report("policy", "error", err.Error())
		return
	}
	if _, err := evaluator.EvaluateOpen("https://example.com"); err != nil {
		report("policy", "error", err.Error())
		return
	}
	if err := checkPolicyScripts(p, profileDir, evaluator); err != nil {
		report("policy", "error", err.Error())
		return
	}
	report("policy", "ok", fmt.Sprintf("engine=%s maxCapabilities=%d scripts=%d", p.Policy.Engine, len(p.Policy.MaxCapabilities), len(p.Policy.ScriptRefs)))
}

func networkConnectProposal(mode, reason string) policy.Proposal {
	if mode == "" {
		mode = "direct"
	}
	return policy.Proposal{
		Decision:  policy.AuditOnly,
		Route:     policy.GuestDirect,
		Action:    "network.connect",
		Resources: []string{"network:" + mode},
		Reason:    reason,
	}
}

func checkPolicyScripts(p profile.Profile, profileDir string, evaluator policy.Evaluator) error {
	for _, ref := range p.Policy.ScriptRefs {
		path := ref.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(profileDir, path)
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("script %s: %w", ref.ID, err)
		}
		for _, entrypoint := range ref.Entrypoints {
			switch entrypoint {
			case "decideCommand":
				req := doctorCommandScriptRequest()
				ctx := policy.CommandContext{
					Version: "policy-script/v1",
					Profile: map[string]string{
						"name": p.Name,
					},
					Session: map[string]any{
						"id":          "doctor",
						"interactive": false,
					},
					Subject: "command:open",
					Command: map[string]any{
						"name":   "open",
						"argv":   []string{"open", "https://example.com"},
						"cwd":    "/workspace",
						"target": "https://example.com",
					},
					Workspace: map[string]any{
						"guestRoot":       "/workspace",
						"hostRootVisible": false,
						"mode":            "read-write",
					},
					Env: map[string]any{
						"safe": map[string]string{"TERM": "xterm-256color"},
					},
					Network: map[string]any{
						"mode": p.Network.Mode,
					},
				}
				proposal, err := evaluator.RunCommandScript(string(source), entrypoint, ctx)
				if err != nil {
					return fmt.Errorf("script %s entrypoint %s: %w", ref.ID, entrypoint, err)
				}
				if err := broker.ValidateCommandScriptProposal(req, proposal); err != nil {
					return fmt.Errorf("script %s entrypoint %s: %w", ref.ID, entrypoint, err)
				}
			case "redactAudit":
				ctx := policy.AuditContext{
					Version:  "policy-audit/v1",
					Profile:  map[string]string{"name": p.Name},
					Session:  map[string]any{"id": "doctor"},
					Subject:  "command:open",
					Action:   "host.open",
					Decision: string(policy.Allow),
					Details: map[string]any{
						"target": "https://example.com",
						"argv":   []string{"open", "https://example.com"},
					},
					Extra: map[string]interface{}{
						"status":   "ok",
						"exitCode": 0,
					},
				}
				if _, err := evaluator.RunAuditRedactScript(string(source), entrypoint, ctx); err != nil {
					return fmt.Errorf("script %s entrypoint %s: %w", ref.ID, entrypoint, err)
				}
			default:
				continue
			}
		}
	}
	return nil
}

func doctorCommandScriptRequest() broker.Request {
	return broker.Request{
		ID:              "req_doctor",
		SessionID:       "doctor",
		CapabilityToken: "doctor",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	}
}

func checkNetwork(p profile.Profile, backendName string, layout session.Layout, env envpolicy.Result, report func(string, string, string)) {
	plan, err := netpolicy.Prepare(netpolicy.Spec{
		Profile:          p,
		Backend:          backendName,
		SessionDir:       layout.Dir,
		GuestSessionDir:  guestSessionDirForBackend(backendName),
		TargetEnv:        env.Env,
		Resolver:         netpolicy.EnvSecretResolver{},
		LocalBypassHosts: localBypassHostsForBackend(backendName),
		RuntimeVerify:    backendName == "lima",
		DryRun:           true,
	})
	if err != nil {
		report("network", "error", err.Error())
		return
	}
	status := "ok"
	if networkDecision(plan, nil) == "audit-only" {
		status = "warn"
	}
	report("network", status, fmt.Sprintf("mode=%s engine=%s runtimeVerify=%t localBypass=%s reason=%s", plan.Mode, plan.Engine, plan.RuntimeVerify, explainList(plan.LocalBypassHosts), plan.Reason))
}

func checkBroker(p profile.Profile, backendName string, layout session.Layout, hostRoot, guestRoot, profileDir string, report func(string, string, string)) {
	token, err := broker.NewToken()
	if err != nil {
		report("broker", "error", err.Error())
		return
	}
	registry, err := commandProxyRegistry(p)
	if err != nil {
		report("broker", "error", err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := brokerEndpointForBackend(backendName, layout)
	server := &broker.Server{
		SessionID:     layout.ID,
		Token:         token,
		Socket:        layout.BrokerSock,
		Endpoint:      endpoint,
		HostRoot:      hostRoot,
		GuestRoot:     guestRoot,
		Profile:       p.Name,
		ProfileDir:    profileDir,
		Backend:       backendName,
		WorkspaceMode: p.Workspace.Mode,
		NetworkMode:   p.Network.Mode,
		Commands:      registry.ShimNames(),
		ScriptRefs:    p.Policy.ScriptRefs,
		Evaluator:     policy.NewEvaluator(p),
		Audit:         audit.NewDiscard(),
		Opener:        broker.NoopOpener{},
	}
	if err := server.StartEndpoint(ctx, endpoint); err != nil {
		report("broker", "error", err.Error())
		return
	}
	defer server.Close()
	resp := checkBrokerOpen(ctx, brokerEndpointForDoctorClient(server.Endpoint), broker.Request{
		ID:              "req_doctor",
		SessionID:       layout.ID,
		CapabilityToken: token,
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status == "broker-unavailable" {
		report("broker", "error", resp.Stderr)
		return
	}
	if resp.Status != "ok" {
		report("broker", "warn", fmt.Sprintf("transport=%s endpoint=present host.open decision=%s status=%s", server.Endpoint.Network, resp.Decision, resp.Status))
	} else {
		report("broker", "ok", fmt.Sprintf("transport=%s endpoint=present host.open=%s", server.Endpoint.Network, resp.Decision))
	}
}

func checkBrokerOpen(ctx context.Context, endpoint broker.Endpoint, req broker.Request) broker.Response {
	deadline := time.Now().Add(2 * time.Second)
	var resp broker.Response
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, 250*time.Millisecond)
		resp = broker.ClientOpenEndpoint(reqCtx, endpoint, req)
		reqCancel()
		if resp.Status != "broker-unavailable" || time.Now().After(deadline) {
			return resp
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func checkCommandProxyRuntime(backendName string, report func(string, string, string)) {
	switch backendName {
	case "lima":
		if resolveLinuxShimPath() == "" {
			report("command-proxy", "error", "prebuilt linux hideout-shim is required for Lima command proxies")
			return
		}
		report("command-proxy", "ok", "linux shim=present")
	case "native":
		if resolveShimPath() == "" {
			report("command-proxy", "warn", "native hideout-shim not found; registered command proxies will fail")
			return
		}
		report("command-proxy", "ok", "native shim=present")
	}
}

func checkHostFSRuntime(backendName string, p profile.Profile, report func(string, string, string)) {
	hostFSProfile := hostFSProfileForRun(p, runOptions{})
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile})
	if err != nil {
		report("hostfs", "error", err.Error())
		return
	}
	grants := len(hostFSPolicy.Grants)
	if grants == 0 {
		report("hostfs", "ok", "inactive grants=0")
		return
	}
	switch backendName {
	case "lima":
		if resolveLinuxHostFSDPath() == "" {
			report("hostfs", "error", fmt.Sprintf("grants=%d prebuilt linux hideout-hostfsd is required for Lima HostFS", grants))
			return
		}
		report("hostfs", "ok", fmt.Sprintf("grants=%d linux hostfsd=present", grants))
	case "native":
		report("hostfs", "warn", fmt.Sprintf("grants=%d backend=native dataPlane=not-mounted", grants))
	default:
		report("hostfs", "error", fmt.Sprintf("grants=%d backend=%s is not supported for HostFS", grants, backendName))
	}
}

func checkHostOpen(p profile.Profile, identityDir string, report func(string, string, string)) {
	if !p.HostCapabilities.Open.AllowURLs {
		report("host-open", "ok", "url disabled by profile")
		return
	}
	opener := hostOpener(identityDir, io.Discard, io.Discard)
	launcher, args, err := opener.URLCommand("https://example.com")
	if err != nil {
		status := "error"
		if strings.Contains(err.Error(), "isolated browser launcher requires") {
			status = "warn"
		}
		report("host-open", status, err.Error())
		return
	}
	if runtime.GOOS == "darwin" && os.Getenv("HIDEOUT_BROWSER_PATH") == "" {
		appName := os.Getenv("HIDEOUT_BROWSER_APP")
		if appName == "" {
			appName = "Google Chrome"
		}
		if !darwinBrowserAppInstalled(appName) {
			report("host-open", "error", fmt.Sprintf("browser app %q is not installed in a standard Applications directory; install it or set HIDEOUT_BROWSER_PATH to a direct Chromium-compatible browser binary", appName))
			return
		}
	}
	if _, err := exec.LookPath(launcher); err != nil {
		report("host-open", "error", fmt.Sprintf("browser launcher %q is not executable: %v", launcher, err))
		return
	}
	browserProfile := opener.BrowserProfile()
	if browserProfile == "" {
		report("host-open", "error", "isolated browser profile path is missing")
		return
	}
	if !slices.Contains(args, "--user-data-dir="+browserProfile) {
		report("host-open", "error", "URL launcher does not include isolated browser profile")
		return
	}
	report("host-open", "ok", fmt.Sprintf("url=isolated browserProfile=present launcher=%s", filepath.Base(launcher)))
}

func darwinBrowserAppInstalled(appName string) bool {
	home, _ := os.UserHomeDir()
	return darwinBrowserAppInstalledInRoots(appName, home, []string{"/Applications", "/System/Applications"})
}

func darwinBrowserAppInstalledInRoots(appName, home string, roots []string) bool {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return false
	}
	appName = strings.TrimSuffix(appName, ".app") + ".app"
	candidates := make([]string, 0, len(roots)+1)
	if strings.TrimSpace(home) != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", appName))
	}
	for _, root := range roots {
		candidates = append(candidates, filepath.Join(root, appName))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func (a app) profile(args []string) error {
	if len(args) == 0 {
		return errors.New("profile command is required")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		if len(args) != 2 {
			return errors.New("usage: hideout profile init <name>")
		}
		p := profile.Default(args[1])
		if err := store.Create(p); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, store.ProfilePath(args[1]))
		return nil
	case "clone":
		if len(args) != 3 {
			return errors.New("usage: hideout profile clone <source> <name>")
		}
		p, err := store.ClonePolicy(args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, store.ProfilePath(p.Name))
		return nil
	case "rotate-identity":
		if len(args) != 2 {
			return errors.New("usage: hideout profile rotate-identity <name>")
		}
		p, err := store.RotateIdentity(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s identityId=%s previousIdentityId=%s\n", store.ProfilePath(p.Name), p.Metadata["identityId"], p.Metadata["previousIdentityId"])
		return nil
	case "reset":
		if len(args) != 2 {
			return errors.New("usage: hideout profile reset <name>")
		}
		p, err := store.ResetIdentity(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s identityId=%s previousIdentityId=%s\n", store.ProfilePath(p.Name), p.Metadata["identityId"], p.Metadata["previousIdentityId"])
		return nil
	case "path":
		if len(args) != 2 {
			return errors.New("usage: hideout profile path <name>")
		}
		if err := profile.ValidateName(args[1]); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, store.ProfilePath(args[1]))
		return nil
	case "fs":
		return a.profileFS(store, args[1:])
	case "env":
		return a.profileEnv(store, args[1:])
	case "home":
		return a.profileHome(store, args[1:])
	case "tools":
		return a.profileTools(store, args[1:])
	default:
		return fmt.Errorf("unknown profile command %q", args[0])
	}
}

func (a app) profileHome(store profile.Store, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: hideout profile home <name> import --from <path> --to <relative-path> [--force]")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "import":
		return a.profileHomeImport(store, name, args[2:])
	default:
		return fmt.Errorf("unknown profile home command %q", command)
	}
}

type profileHomeImportOptions struct {
	from  string
	to    string
	force bool
}

type profileHomeImportOutput struct {
	Profile string `json:"profile"`
	Kind    string `json:"kind"`
	Dest    string `json:"dest"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
}

func parseProfileHomeImportOptions(args []string) (profileHomeImportOptions, error) {
	var opts profileHomeImportOptions
	fs := flag.NewFlagSet("profile home import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.from, "from", "", "host file or directory to import")
	fs.StringVar(&opts.to, "to", "", "relative path inside profile home")
	fs.BoolVar(&opts.force, "force", false, "replace an existing profile home path")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected profile home import argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(opts.from) == "" {
		return opts, errors.New("--from is required")
	}
	if strings.TrimSpace(opts.to) == "" {
		return opts, errors.New("--to is required")
	}
	return opts, nil
}

func (a app) profileHomeImport(store profile.Store, name string, args []string) error {
	opts, err := parseProfileHomeImportOptions(args)
	if err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	src, err := filepath.Abs(opts.from)
	if err != nil {
		return err
	}
	destRel, err := cleanProfileHomeDest(opts.to)
	if err != nil {
		return err
	}
	dest := filepath.Join(store.ProfileDir(p.Name), "home", destRel)
	homeRoot := filepath.Join(store.ProfileDir(p.Name), "home")
	if !pathWithinRoot(homeRoot, dest) {
		return fmt.Errorf("profile home import destination %q escapes profile home", opts.to)
	}
	if err := ensureProfileHomeParent(homeRoot, dest); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		if !opts.force {
			return fmt.Errorf("profile home import destination %q already exists; use --force to replace it", destRel)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stats, err := copyProfileHomePath(homeRoot, src, dest)
	if err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	return writeJSONLine(a.stdout, profileHomeImportOutput{
		Profile: p.Name,
		Kind:    "profile.home.import",
		Dest:    destRel,
		Files:   stats.files,
		Bytes:   stats.bytes,
	})
}

func cleanProfileHomeDest(value string) (string, error) {
	value = filepath.Clean(strings.TrimSpace(value))
	if value == "." || value == string(filepath.Separator) || filepath.IsAbs(value) {
		return "", fmt.Errorf("profile home destination must be a relative path: %q", value)
	}
	if value == ".." || strings.HasPrefix(value, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("profile home destination must stay inside profile home: %q", value)
	}
	return value, nil
}

func pathWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type profileHomeCopyStats struct {
	files int
	bytes int64
}

func copyProfileHomePath(homeRoot, src, dst string) (profileHomeCopyStats, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return profileHomeCopyStats{}, errors.New("profile home import source is not accessible")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return profileHomeCopyStats{}, fmt.Errorf("profile home import source %q must not be a symlink", filepath.Base(src))
	}
	if info.IsDir() {
		return copyProfileHomeDir(homeRoot, src, dst)
	}
	if !info.Mode().IsRegular() {
		return profileHomeCopyStats{}, fmt.Errorf("profile home import source %q must be a regular file or directory", filepath.Base(src))
	}
	if err := copyProfileHomeFile(homeRoot, src, dst, info); err != nil {
		return profileHomeCopyStats{}, err
	}
	return profileHomeCopyStats{files: 1, bytes: info.Size()}, nil
}

func copyProfileHomeDir(homeRoot, src, dst string) (profileHomeCopyStats, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return profileHomeCopyStats{}, errors.New("profile home import source directory is not readable")
	}
	if err := ensureProfileHomeDir(homeRoot, dst); err != nil {
		return profileHomeCopyStats{}, err
	}
	var stats profileHomeCopyStats
	for _, entry := range entries {
		childStats, err := copyProfileHomePath(homeRoot, filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
		if err != nil {
			return profileHomeCopyStats{}, err
		}
		stats.files += childStats.files
		stats.bytes += childStats.bytes
	}
	return stats, nil
}

func copyProfileHomeFile(homeRoot, src, dst string, info os.FileInfo) error {
	if err := ensureProfileHomeFile(homeRoot, dst); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return errors.New("profile home import source file is not readable")
	}
	mode := info.Mode().Perm()
	if mode == 0 || mode&0o077 != 0 {
		mode = 0o600
	}
	return os.WriteFile(dst, data, mode)
}

func ensureProfileHomeFile(homeRoot, dst string) error {
	if err := ensureProfileHomeParent(homeRoot, dst); err != nil {
		return err
	}
	info, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile home import destination must not use a symlink")
	}
	return nil
}

func ensureProfileHomeDir(homeRoot, dst string) error {
	if err := ensureProfileHomeParent(homeRoot, dst); err != nil {
		return err
	}
	info, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return os.Mkdir(dst, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile home import destination must not use a symlink")
	}
	if !info.IsDir() {
		return errors.New("profile home import destination directory is not a directory")
	}
	return nil
}

func ensureProfileHomeParent(homeRoot, dst string) error {
	return ensureProfileHomeDirPath(homeRoot, filepath.Dir(dst))
}

func ensureProfileHomeDirPath(homeRoot, dir string) error {
	homeRoot = filepath.Clean(homeRoot)
	dir = filepath.Clean(dir)
	if !pathWithinRoot(homeRoot, dir) {
		return errors.New("profile home import destination escapes profile home")
	}
	if err := ensureProfileHomeRoot(homeRoot); err != nil {
		return err
	}
	rel, err := filepath.Rel(homeRoot, dir)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	current := homeRoot
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			managedTarget, ok, err := managedProfileHomeSymlinkTarget(homeRoot, current)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("profile home import destination must not use a symlink")
			}
			current = managedTarget
			continue
		}
		if !info.IsDir() {
			return errors.New("profile home import destination parent is not a directory")
		}
	}
	return nil
}

func managedProfileHomeSymlinkTarget(homeRoot, linkPath string) (string, bool, error) {
	homeRoot = filepath.Clean(homeRoot)
	linkPath = filepath.Clean(linkPath)
	profileDir := filepath.Dir(homeRoot)
	managed := map[string]string{
		filepath.Join(homeRoot, ".config"):         filepath.Join(profileDir, "config"),
		filepath.Join(homeRoot, ".cache"):          filepath.Join(profileDir, "cache"),
		filepath.Join(homeRoot, ".local", "share"): filepath.Join(profileDir, "data"),
	}
	want, ok := managed[linkPath]
	if !ok {
		return "", false, nil
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	target = filepath.Clean(target)
	if target != want {
		return "", false, nil
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return "", false, err
		}
		return target, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("profile home import managed XDG target must not be a symlink")
	}
	if !info.IsDir() {
		return "", false, errors.New("profile home import managed XDG target is not a directory")
	}
	return target, true, nil
}

func ensureProfileHomeRoot(homeRoot string) error {
	info, err := os.Lstat(homeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(homeRoot, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile home import root must not be a symlink")
	}
	if !info.IsDir() {
		return errors.New("profile home import root is not a directory")
	}
	return nil
}

func (a app) profileEnv(store profile.Store, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: hideout profile env <name> <list|set|unset|inherit|uninherit|deny|undeny>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile env <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileEnv(a.stdout, p)
	case "set":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> set KEY=VALUE")
		}
		key, value, ok := strings.Cut(args[2], "=")
		if !ok || strings.TrimSpace(key) == "" {
			return errors.New("profile env set requires KEY=VALUE")
		}
		return a.profileEnvSet(store, name, strings.TrimSpace(key), value)
	case "unset":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> unset KEY")
		}
		return a.profileEnvUnset(store, name, args[2])
	case "inherit":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> inherit KEY")
		}
		return a.profileEnvListAdd(store, name, "inherit", args[2])
	case "uninherit":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> uninherit KEY")
		}
		return a.profileEnvListRemove(store, name, "inherit", args[2])
	case "deny":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> deny PATTERN")
		}
		return a.profileEnvListAdd(store, name, "deny", args[2])
	case "undeny":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> undeny PATTERN")
		}
		return a.profileEnvListRemove(store, name, "deny", args[2])
	default:
		return fmt.Errorf("unknown profile env command %q", command)
	}
}

type profileEnvListOutput struct {
	Profile string   `json:"profile"`
	Public  []string `json:"public"`
	Inherit []string `json:"inherit"`
	Deny    []string `json:"deny"`
}

type profileEnvChangeOutput struct {
	Profile string `json:"profile"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Removed bool   `json:"removed,omitempty"`
}

func writeProfileEnv(w io.Writer, p profile.Profile) error {
	return writeJSONLine(w, profileEnvListOutput{
		Profile: p.Name,
		Public:  sortedMapKeys(p.Env.Public),
		Inherit: sortedStrings(p.Env.Inherit),
		Deny:    sortedStrings(p.Env.Deny),
	})
}

func (a app) profileEnvSet(store profile.Store, name, key, value string) error {
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	if p.Env.Public == nil {
		p.Env.Public = map[string]string{}
	}
	p.Env.Public[key] = value
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env.public", Name: key})
}

func (a app) profileEnvUnset(store profile.Store, name, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("env key is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	delete(p.Env.Public, key)
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env.public", Name: key, Removed: true})
}

func (a app) profileEnvListAdd(store profile.Store, name, kind, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("env key or pattern is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	switch kind {
	case "inherit":
		p.Env.Inherit = appendIfMissing(p.Env.Inherit, value)
	case "deny":
		p.Env.Deny = appendIfMissing(p.Env.Deny, value)
	default:
		return fmt.Errorf("unsupported env list kind %q", kind)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env." + kind, Name: value})
}

func (a app) profileEnvListRemove(store profile.Store, name, kind, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("env key or pattern is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	switch kind {
	case "inherit":
		p.Env.Inherit = removeString(p.Env.Inherit, value)
	case "deny":
		p.Env.Deny = removeString(p.Env.Deny, value)
	default:
		return fmt.Errorf("unsupported env list kind %q", kind)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env." + kind, Name: value, Removed: true})
}

func (a app) profileTools(store profile.Store, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: hideout profile tools <name> <list|preset|npm>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile tools <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileTools(a.stdout, p)
	case "preset":
		return a.profileToolPreset(store, name, args[2:])
	case "npm":
		return a.profileToolNPM(store, name, args[2:])
	default:
		return fmt.Errorf("unknown profile tools command %q", command)
	}
}

type profileToolsOutput struct {
	Profile    string                     `json:"profile"`
	Presets    []string                   `json:"presets"`
	NPMGlobals []profile.NPMGlobalPackage `json:"npmGlobals,omitempty"`
}

type profileToolChangeOutput struct {
	Profile  string   `json:"profile"`
	Kind     string   `json:"kind"`
	Package  string   `json:"package,omitempty"`
	Preset   string   `json:"preset,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Removed  bool     `json:"removed,omitempty"`
}

func writeProfileTools(w io.Writer, p profile.Profile) error {
	return writeJSONLine(w, profileToolsOutput{
		Profile:    p.Name,
		Presets:    sortedStrings(p.Tools.Presets),
		NPMGlobals: copyNPMGlobalsForOutput(p.Tools.NPMGlobals),
	})
}

func (a app) profileToolPreset(store profile.Store, name string, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: hideout profile tools <name> preset <add|remove> <preset>")
	}
	action := args[0]
	preset := strings.TrimSpace(args[1])
	if preset == "" {
		return errors.New("tool preset is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	switch action {
	case "add":
		p.Tools.Presets = appendIfMissing(p.Tools.Presets, preset)
	case "remove":
		p.Tools.Presets = removeString(p.Tools.Presets, preset)
	default:
		return fmt.Errorf("unknown profile tools preset command %q", action)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileToolChangeOutput{
		Profile: p.Name,
		Kind:    "tools.presets",
		Preset:  preset,
		Removed: action == "remove",
	})
}

func (a app) profileToolNPM(store profile.Store, name string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout profile tools <name> npm <add|remove>")
	}
	switch args[0] {
	case "add":
		return a.profileToolNPMAdd(store, name, args[1:])
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: hideout profile tools <name> npm remove <package>")
		}
		return a.profileToolNPMRemove(store, name, args[1])
	default:
		return fmt.Errorf("unknown profile tools npm command %q", args[0])
	}
}

func (a app) profileToolNPMAdd(store profile.Store, name string, args []string) error {
	fs := flag.NewFlagSet("profile tools npm add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var packageSpec string
	var commands stringListFlag
	fs.StringVar(&packageSpec, "package", "", "npm package spec")
	fs.Var(&commands, "command", "command expected after npm global install")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected profile tools npm argument %q", fs.Arg(0))
	}
	packageSpec = strings.TrimSpace(packageSpec)
	if packageSpec == "" {
		return errors.New("--package is required")
	}
	if len(commands) == 0 {
		return errors.New("--command is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	next := profile.NPMGlobalPackage{Package: packageSpec, Commands: append([]string(nil), commands...)}
	replaced := false
	for i, pkg := range p.Tools.NPMGlobals {
		if strings.TrimSpace(pkg.Package) == packageSpec {
			p.Tools.NPMGlobals[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		p.Tools.NPMGlobals = append(p.Tools.NPMGlobals, next)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileToolChangeOutput{
		Profile:  p.Name,
		Kind:     "tools.npmGlobals",
		Package:  packageSpec,
		Commands: append([]string(nil), commands...),
	})
}

func (a app) profileToolNPMRemove(store profile.Store, name, packageSpec string) error {
	packageSpec = strings.TrimSpace(packageSpec)
	if packageSpec == "" {
		return errors.New("npm package is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	removed := false
	out := p.Tools.NPMGlobals[:0]
	for _, pkg := range p.Tools.NPMGlobals {
		if strings.TrimSpace(pkg.Package) == packageSpec {
			removed = true
			continue
		}
		out = append(out, pkg)
	}
	p.Tools.NPMGlobals = out
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileToolChangeOutput{
		Profile: p.Name,
		Kind:    "tools.npmGlobals",
		Package: packageSpec,
		Removed: removed,
	})
}

func (a app) profileFS(store profile.Store, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: hideout profile fs <name> <list|add|deny|remove>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile fs <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileFSRules(a.stdout, p)
	case "add":
		return a.profileFSAdd(store, name, args[2:], false)
	case "deny":
		return a.profileFSAdd(store, name, args[2:], true)
	case "remove":
		if len(args) != 3 {
			return errors.New("usage: hideout profile fs <name> remove <rule-id>")
		}
		return a.profileFSRemove(store, name, args[2])
	default:
		return fmt.Errorf("unknown profile fs command %q", command)
	}
}

type profileFSAddOptions struct {
	ruleValue string
	reason    string
}

func parseProfileFSAddOptions(args []string, deny bool) (profileFSAddOptions, error) {
	var opts profileFSAddOptions
	fs := flag.NewFlagSet("profile fs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if deny {
		fs.StringVar(&opts.ruleValue, "no-fs", "", "profile HostFS deny rule")
	} else {
		fs.StringVar(&opts.ruleValue, "fs", "", "profile HostFS allow rule")
	}
	fs.StringVar(&opts.reason, "reason", "", "reason for this HostFS rule")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected profile fs argument %q", fs.Arg(0))
	}
	flagName := "--fs"
	if deny {
		flagName = "--no-fs"
	}
	if strings.TrimSpace(opts.ruleValue) == "" {
		return opts, fmt.Errorf("%s is required", flagName)
	}
	if strings.TrimSpace(opts.reason) == "" {
		return opts, errors.New("--reason is required")
	}
	return opts, nil
}

func (a app) profileFSAdd(store profile.Store, name string, args []string, deny bool) error {
	opts, err := parseProfileFSAddOptions(args, deny)
	if err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	flagName := "--fs"
	reasonPrefix := "profile HostFS allow"
	if deny {
		flagName = "--no-fs"
		reasonPrefix = "profile HostFS deny"
	}
	rule, err := parseHostFSRuleFlag(hostFSFlagInput{
		flagName: flagName,
		value:    opts.ruleValue,
		reason:   opts.reason,
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	rule.ID, err = newHostFSRuleID(p.HostFS)
	if err != nil {
		return err
	}
	rule.CreatedAt = &now
	rule.Reason = opts.reason
	if deny {
		p.HostFS.Deny = append(p.HostFS.Deny, rule)
	} else {
		p.HostFS.Grants = append(p.HostFS.Grants, rule)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	item := profileFSRuleOutputFromRule(rule, deny)
	item.Profile = p.Name
	item.Reason = strings.TrimSpace(item.Reason)
	item.Kind = reasonPrefix
	return writeJSONLine(a.stdout, item)
}

func (a app) profileFSRemove(store profile.Store, name, ruleID string) error {
	if strings.TrimSpace(ruleID) == "" {
		return errors.New("rule-id is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	var removed *profileFSRuleOutput
	p.HostFS.Grants, removed = removeHostFSRule(p.HostFS.Grants, ruleID, false)
	if removed == nil {
		p.HostFS.Deny, removed = removeHostFSRule(p.HostFS.Deny, ruleID, true)
	}
	if removed == nil {
		return fmt.Errorf("profile HostFS rule %q not found", ruleID)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	removed.Profile = p.Name
	removed.Removed = true
	return writeJSONLine(a.stdout, removed)
}

type profileFSRuleOutput struct {
	Profile   string       `json:"profile,omitempty"`
	ID        string       `json:"id"`
	Kind      string       `json:"kind"`
	Effect    string       `json:"effect"`
	HostPath  string       `json:"hostPath"`
	Ops       []hostfs.Op  `json:"ops,omitempty"`
	Scope     hostfs.Scope `json:"scope"`
	Reason    string       `json:"reason"`
	CreatedAt string       `json:"createdAt,omitempty"`
	Removed   bool         `json:"removed,omitempty"`
}

func writeProfileFSRules(w io.Writer, p profile.Profile) error {
	out := struct {
		Profile string                `json:"profile"`
		Grants  []profileFSRuleOutput `json:"grants"`
		Deny    []profileFSRuleOutput `json:"deny"`
	}{
		Profile: p.Name,
		Grants:  make([]profileFSRuleOutput, 0, len(p.HostFS.Grants)),
		Deny:    make([]profileFSRuleOutput, 0, len(p.HostFS.Deny)),
	}
	for _, rule := range p.HostFS.Grants {
		out.Grants = append(out.Grants, profileFSRuleOutputFromRule(rule, false))
	}
	for _, rule := range p.HostFS.Deny {
		out.Deny = append(out.Deny, profileFSRuleOutputFromRule(rule, true))
	}
	return writeJSONLine(w, out)
}

func profileFSRuleOutputFromRule(rule hostfs.Rule, deny bool) profileFSRuleOutput {
	effect := "allow"
	kind := "profile HostFS allow"
	if deny {
		effect = "deny"
		kind = "profile HostFS deny"
	}
	createdAt := ""
	if rule.CreatedAt != nil {
		createdAt = rule.CreatedAt.Format(time.RFC3339Nano)
	}
	return profileFSRuleOutput{
		ID:        rule.ID,
		Kind:      kind,
		Effect:    effect,
		HostPath:  rule.HostPath,
		Ops:       append([]hostfs.Op(nil), rule.Ops...),
		Scope:     rule.Scope,
		Reason:    rule.Reason,
		CreatedAt: createdAt,
	}
}

func removeHostFSRule(rules []hostfs.Rule, id string, deny bool) ([]hostfs.Rule, *profileFSRuleOutput) {
	for i, rule := range rules {
		if rule.ID != id {
			continue
		}
		removed := profileFSRuleOutputFromRule(rule, deny)
		out := append([]hostfs.Rule(nil), rules[:i]...)
		out = append(out, rules[i+1:]...)
		return out, &removed
	}
	return rules, nil
}

func newHostFSRuleID(config hostfs.Config) (string, error) {
	for range 16 {
		var raw [6]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		id := "hfs_" + hex.EncodeToString(raw[:])
		if !hostFSRuleIDExists(config, id) {
			return id, nil
		}
	}
	return "", errors.New("could not allocate unique HostFS rule id")
}

func hostFSRuleIDExists(config hostfs.Config, id string) bool {
	for _, rule := range config.Grants {
		if rule.ID == id {
			return true
		}
	}
	for _, rule := range config.Deny {
		if rule.ID == id {
			return true
		}
	}
	return false
}

func writeJSONLine(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	out := values[:0]
	for _, existing := range values {
		if existing == value {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func copyNPMGlobalsForOutput(values []profile.NPMGlobalPackage) []profile.NPMGlobalPackage {
	if len(values) == 0 {
		return nil
	}
	out := make([]profile.NPMGlobalPackage, len(values))
	for i, value := range values {
		out[i] = value
		out[i].Commands = append([]string(nil), value.Commands...)
	}
	return out
}

func (a app) cleanup(args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id")
	dryRun := fs.Bool("dry-run", false, "show files that would be removed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	result, err := session.CleanupEphemeral(store.Root, *sessionID, *dryRun)
	if err != nil {
		return err
	}
	mode := "removed"
	if *dryRun {
		mode = "would remove"
	}
	secretState := "removed"
	if *dryRun {
		secretState = "would-remove"
	}
	fmt.Fprintf(a.stdout, "cleanup: sessions=%d %s=%d audit=preserved secretState=%s\n", result.Sessions, mode, len(result.Removed), secretState)
	for _, path := range result.Removed {
		fmt.Fprintf(a.stdout, "%s: %s\n", mode, path)
	}
	return nil
}

type auditShowOptions struct {
	session  string
	profile  string
	action   string
	decision string
	limit    int
	json     bool
}

func parseAuditShowOptions(args []string) (auditShowOptions, error) {
	fs := flag.NewFlagSet("audit show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := auditShowOptions{limit: 50}
	fs.StringVar(&opts.session, "session", "", "session id")
	fs.StringVar(&opts.profile, "profile", "", "profile name")
	fs.StringVar(&opts.action, "action", "", "audit action")
	fs.StringVar(&opts.decision, "decision", "", "audit decision")
	fs.IntVar(&opts.limit, "limit", opts.limit, "maximum events")
	fs.BoolVar(&opts.json, "json", false, "print redacted JSON events")
	if err := fs.Parse(args); err != nil {
		return auditShowOptions{}, err
	}
	if fs.NArg() != 0 {
		return auditShowOptions{}, errors.New("usage: hideout audit show [--session <id>] [--profile <name>] [--action <name>] [--decision <value>] [--limit N] [--json]")
	}
	if opts.limit <= 0 {
		return auditShowOptions{}, errors.New("--limit must be greater than zero")
	}
	return opts, nil
}

func (a app) auditCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout audit show [--session <id>] [--profile <name>] [--action <name>] [--decision <value>] [--limit N] [--json]")
	}
	switch args[0] {
	case "show":
		return a.auditShow(args[1:])
	default:
		return fmt.Errorf("unknown audit command %q", args[0])
	}
}

func (a app) auditShow(args []string) error {
	opts, err := parseAuditShowOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	events, err := manager.New(store).AuditEvents(manager.AuditEventFilter{
		Session:  opts.session,
		Profile:  opts.profile,
		Action:   opts.action,
		Decision: opts.decision,
		Limit:    opts.limit,
	})
	if err != nil {
		return err
	}
	if opts.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(events)
	}
	writeAuditShowEvents(a.stdout, events)
	return nil
}

func writeAuditShowEvents(w io.Writer, events []audit.Event) {
	if len(events) == 0 {
		fmt.Fprintln(w, "audit: none")
		return
	}
	fmt.Fprintln(w, "TIME\tSESSION\tPROFILE\tBACKEND\tACTION\tDECISION\tDETAILS")
	for _, event := range events {
		ts := "-"
		if !event.Time.IsZero() {
			ts = event.Time.UTC().Format(time.RFC3339Nano)
		}
		details := "{}"
		if len(event.Details) > 0 {
			if data, err := json.Marshal(event.Details); err == nil {
				details = string(data)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ts,
			event.Session,
			event.Profile,
			event.Backend,
			event.Action,
			event.Decision,
			details,
		)
	}
}

func (a app) listEnvironments(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout list")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	records, err := (environment.Store{Root: store.Root}).List()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(a.stdout, "environments: none")
		return nil
	}
	fmt.Fprintln(a.stdout, "ID\tPROFILE\tBACKEND\tSTATUS\tCREATED\tLAST_STARTED\tLAST_ENDED\tWORKSPACE\tCOMMAND")
	for _, rec := range records {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			rec.ID,
			rec.Profile,
			rec.Backend,
			explainValue(rec.Status, "ready"),
			formatEnvironmentTime(rec.CreatedAt),
			formatEnvironmentTime(rec.LastStartedAt),
			formatEnvironmentTime(rec.LastEndedAt),
			rec.Workspace,
			rec.LastCommand,
		)
	}
	return nil
}

func (a app) stopEnvironments(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show environments that would be stopped")
	idleValue := fs.String("idle", "", "stop environments whose last run ended at least this long ago")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idle, idleSet, err := parseIdleDuration(*idleValue)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	envStore := environment.Store{Root: store.Root}
	records, err := cleanEnvironmentRecords(envStore, fs.Args())
	if err != nil {
		return err
	}
	records = filterEnvironmentRecords(records, environmentRecordFilter{
		Idle:    idle,
		IdleSet: idleSet,
		Now:     time.Now().UTC(),
	})
	stopped := 0
	skipped := 0
	mode := "stopped"
	if *dryRun {
		mode = "would stop"
	}
	for _, rec := range records {
		if rec.Status == "stopped" {
			skipped++
			fmt.Fprintf(a.stdout, "skipped: %s reason=already-stopped instance=%s workspace=%s\n", rec.ID, rec.InstanceName, rec.Workspace)
			continue
		}
		if rec.Backend != "lima" || rec.InstanceName == "" {
			skipped++
			fmt.Fprintf(a.stdout, "skipped: %s reason=no-lima-instance backend=%s workspace=%s\n", rec.ID, rec.Backend, rec.Workspace)
			continue
		}
		if *dryRun {
			stopped++
			fmt.Fprintf(a.stdout, "%s: %s instance=%s workspace=%s\n", mode, rec.ID, rec.InstanceName, rec.Workspace)
			continue
		}
		lock, err := envStore.Lock(rec.ID)
		if err != nil {
			return err
		}
		stopErr := (lima.Backend{Stdout: io.Discard, Stderr: a.stderr}).StopInstance(context.Background(), rec.InstanceName)
		if stopErr != nil {
			_ = lock.Unlock()
			return stopErr
		}
		rec.Status = "stopped"
		if err := envStore.Save(rec); err != nil {
			_ = lock.Unlock()
			return err
		}
		if err := lock.Unlock(); err != nil {
			return err
		}
		stopped++
		fmt.Fprintf(a.stdout, "%s: %s instance=%s workspace=%s\n", mode, rec.ID, rec.InstanceName, rec.Workspace)
	}
	if *dryRun {
		fmt.Fprintf(a.stdout, "stop: environments=%d would stop=%d skipped=%d\n", len(records), stopped, skipped)
		return nil
	}
	fmt.Fprintf(a.stdout, "stop: environments=%d stopped=%d skipped=%d\n", len(records), stopped, skipped)
	return nil
}

func (a app) cleanEnvironments(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show environments that would be removed")
	stoppedOnly := fs.Bool("stopped", false, "remove only stopped environments")
	idleValue := fs.String("idle", "", "remove environments whose last run ended at least this long ago")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idle, idleSet, err := parseIdleDuration(*idleValue)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	envStore := environment.Store{Root: store.Root}
	records, err := cleanEnvironmentRecords(envStore, fs.Args())
	if err != nil {
		return err
	}
	records = filterEnvironmentRecords(records, environmentRecordFilter{
		StoppedOnly: *stoppedOnly,
		Idle:        idle,
		IdleSet:     idleSet,
		Now:         time.Now().UTC(),
	})
	mode := "removed"
	if *dryRun {
		mode = "would remove"
	}
	removed := 0
	for _, rec := range records {
		if *dryRun {
			fmt.Fprintf(a.stdout, "%s: %s instance=%s workspace=%s\n", mode, rec.ID, rec.InstanceName, rec.Workspace)
			continue
		}
		if rec.Backend == "lima" && rec.InstanceName != "" {
			if err := (lima.Backend{Stdout: io.Discard, Stderr: a.stderr}).Cleanup(context.Background(), &backend.Session{InstanceName: rec.InstanceName}); err != nil {
				return err
			}
		}
		if err := envStore.Remove(rec.ID); err != nil {
			return err
		}
		removed++
		fmt.Fprintf(a.stdout, "%s: %s instance=%s workspace=%s\n", mode, rec.ID, rec.InstanceName, rec.Workspace)
	}
	if *dryRun {
		fmt.Fprintf(a.stdout, "clean: environments=%d would remove=%d\n", len(records), len(records))
		return nil
	}
	fmt.Fprintf(a.stdout, "clean: environments=%d removed=%d\n", len(records), removed)
	return nil
}

type environmentRecordFilter struct {
	StoppedOnly bool
	Idle        time.Duration
	IdleSet     bool
	Now         time.Time
}

func filterEnvironmentRecords(records []environment.Record, filter environmentRecordFilter) []environment.Record {
	if !filter.StoppedOnly && !filter.IdleSet {
		return records
	}
	out := records[:0]
	for _, rec := range records {
		if filter.StoppedOnly && rec.Status != "stopped" {
			continue
		}
		if filter.IdleSet && !environmentRecordIdle(rec, filter.Idle, filter.Now) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func environmentRecordIdle(rec environment.Record, idle time.Duration, now time.Time) bool {
	if rec.Status == "running" || rec.LastEndedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !rec.LastEndedAt.After(now.Add(-idle))
}

func parseIdleDuration(value string) (time.Duration, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, false, fmt.Errorf("invalid --idle duration %q: %w", value, err)
	}
	if duration < 0 {
		return 0, false, errors.New("--idle duration must be non-negative")
	}
	return duration, true, nil
}

func cleanEnvironmentRecords(store environment.Store, ids []string) ([]environment.Record, error) {
	if len(ids) == 0 {
		return store.List()
	}
	records := make([]environment.Record, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		rec, err := store.Load(id)
		if err != nil {
			return nil, err
		}
		if seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		records = append(records, rec)
	}
	return records, nil
}

func formatEnvironmentTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

type uiOptions struct {
	listen   string
	ttl      time.Duration
	noOpen   bool
	printURL bool
}

func parseUIOptions(args []string) (uiOptions, error) {
	opts := uiOptions{
		listen: manager.DefaultUIListenAddr,
		ttl:    15 * time.Minute,
	}
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.listen, "listen", opts.listen, "127.0.0.1 listen address")
	fs.DurationVar(&opts.ttl, "ttl", opts.ttl, "UI token lifetime")
	fs.BoolVar(&opts.noOpen, "no-open", false, "do not open a browser")
	fs.BoolVar(&opts.printURL, "print-url", false, "print URL and exit")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, errors.New("usage: hideout ui [--listen 127.0.0.1:0] [--ttl 15m] [--no-open] [--print-url]")
	}
	return opts, nil
}

func (a app) ui(args []string) error {
	opts, err := parseUIOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := manager.StartLocalServer(ctx, manager.LocalServerOptions{
		Core:       manager.New(store),
		Addr:       opts.listen,
		TTL:        opts.ttl,
		RunBackend: a.runAPIBackend,
		RunOpener: func(_ manager.RunAPIRequest, _ manager.RunPlan, runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, a.stdout, a.stderr)
		},
	})
	if err != nil {
		return err
	}
	defer server.Close()
	fmt.Fprintf(a.stdout, "Hideout UI: %s\n", server.UIURL)
	fmt.Fprintf(a.stdout, "Manager API: %s\n", server.APIURL)
	fmt.Fprintf(a.stdout, "Token expires: %s\n", server.ExpiresAt.Format(time.RFC3339))
	if opts.printURL {
		return nil
	}
	if !opts.noOpen {
		opener := hostopen.Opener{
			BrowserProfileDir: filepath.Join(store.Root, "ui-browser"),
			BrowserPath:       os.Getenv("HIDEOUT_BROWSER_PATH"),
			BrowserApp:        os.Getenv("HIDEOUT_BROWSER_APP"),
			DryRun:            os.Getenv("HIDEOUT_OPEN_DRY_RUN") == "1",
			Stdout:            a.stdout,
			Stderr:            a.stderr,
		}
		if err := opener.OpenURL(context.Background(), server.UIURL); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.stdout, "Press Ctrl-C to stop.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	select {
	case <-sig:
		return nil
	case err := <-serverError(server):
		return err
	}
}

func (a app) runAPIBackend(req manager.RunAPIRequest, plan manager.RunPlan) (backend.Backend, error) {
	opts := runOptions{
		backendName:        plan.Backend,
		allowWeakIsolation: req.AllowWeakIsolation,
	}
	return a.backend(plan.Backend, opts), nil
}

type tuiOptions struct {
	watch    bool
	interval time.Duration
}

func parseTUIOptions(args []string) (tuiOptions, error) {
	opts := tuiOptions{interval: 2 * time.Second}
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.watch, "watch", false, "refresh the terminal dashboard until interrupted")
	fs.DurationVar(&opts.interval, "interval", opts.interval, "watch refresh interval")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, errors.New("usage: hideout tui [--watch] [--interval 2s]")
	}
	if opts.interval <= 0 {
		return opts, errors.New("--interval must be positive")
	}
	return opts, nil
}

func (a app) tui(args []string) error {
	opts, err := parseTUIOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	render := func(clear bool) error {
		overview, overviewErr := core.Overview(ctx)
		events, auditErr := core.AuditEvents(manager.AuditEventFilter{Limit: 5})
		deniedEvents, deniedAuditErr := core.AuditEvents(manager.AuditEventFilter{Decision: "deny", Limit: 5})
		if clear {
			fmt.Fprint(a.stdout, "\033[H\033[2J")
		}
		writeTUIDashboard(a.stdout, overview, events, deniedEvents, errors.Join(overviewErr, auditErr, deniedAuditErr))
		return nil
	}
	if !opts.watch {
		return render(false)
	}
	for {
		if err := render(true); err != nil {
			return err
		}
		timer := time.NewTimer(opts.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func writeTUIDashboard(w io.Writer, overview manager.Overview, events []audit.Event, deniedEvents []audit.Event, err error) {
	fmt.Fprintln(w, "Hideout TUI")
	fmt.Fprintf(w, "Store: %s\n", dash(overview.StorageRoot))
	if err != nil {
		fmt.Fprintf(w, "Status: degraded (%s)\n", err)
	} else {
		fmt.Fprintln(w, "Status: ok")
	}
	fmt.Fprintf(w, "Profiles: %d  Sessions: %d  Audit files: %d\n", len(overview.Profiles), len(overview.Sessions), overview.Audit.SessionAuditFiles)
	fmt.Fprintf(w, "Init: initialized=%t pending=%d profile=%s\n", overview.Init.Initialized, overview.Init.PendingTasks, dash(overview.Init.Profile))
	if len(overview.Init.NextSteps) > 0 {
		fmt.Fprintln(w, "Init Next:")
		for _, step := range overview.Init.NextSteps {
			fmt.Fprintf(w, "  - %s: %s\n", dash(step.Label), dash(step.Command))
		}
	}
	fmt.Fprintf(w, "Capabilities: host.open urls=%t workspaceFiles=%t commandProxies=%s max=%s\n",
		overview.Capabilities.HostOpen.AllowURLs,
		overview.Capabilities.HostOpen.AllowWorkspaceFiles,
		listForTUI(overview.Broker.CommandProxies),
		listForTUI(overview.Capabilities.MaxCapabilities),
	)

	fmt.Fprintln(w, "\nProfiles")
	if len(overview.Profiles) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, p := range overview.Profiles {
		status := "ok"
		if p.ValidationError != "" {
			status = "error: " + p.ValidationError
		}
		fmt.Fprintf(w, "  - %s  network=%s  presets=%s  npm=%s  status=%s\n", dash(p.Name), dash(p.NetworkMode), listForTUI(p.ToolPresets), npmGlobalsForTUI(p.NPMGlobals), status)
	}

	fmt.Fprintln(w, "\nBackends")
	if len(overview.Backends) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, b := range overview.Backends {
		status := "available"
		if !b.Available {
			status = "unavailable: " + b.Error
		}
		fmt.Fprintf(w, "  - %s  isolation=%s  %s\n", dash(b.Name), dash(b.Isolation), status)
	}

	fmt.Fprintln(w, "\nNetwork")
	if len(overview.Network.ProfileDefaults) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, n := range overview.Network.ProfileDefaults {
		mode := networkModeForTUI(n.Mode)
		fmt.Fprintf(w, "  - %s  mode=%s  proxyEnv=%s%s\n", dash(n.Profile), mode, proxyEnvForTUI(n.ProxyEnvVisible), networkWarningForTUI(mode))
	}

	fmt.Fprintln(w, "\nSessions")
	if len(overview.Sessions) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, s := range overview.Sessions {
		fmt.Fprintf(w, "  - %s  audit=%t  network=%s  runtime=%t\n", dash(s.ID), s.HasAudit, dash(s.NetworkMode), s.HasEphemeralState)
	}

	fmt.Fprintln(w, "\nRecent Denied Audit")
	if len(deniedEvents) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, event := range deniedEvents {
		fmt.Fprintf(w, "  - %s  action=%s  session=%s\n", dash(event.Profile), dash(event.Action), dash(event.Session))
	}

	fmt.Fprintln(w, "\nRecent Audit")
	if len(events) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, event := range events {
		fmt.Fprintf(w, "  - %s  action=%s  decision=%s  session=%s\n", dash(event.Profile), dash(event.Action), dash(event.Decision), dash(event.Session))
	}
}

func dash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func proxyEnvForTUI(visible bool) string {
	if visible {
		return "visible"
	}
	return "hidden"
}

func networkModeForTUI(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return "direct"
	}
	return mode
}

func networkWarningForTUI(mode string) string {
	switch strings.TrimSpace(mode) {
	case "direct":
		return "  warning=direct exposes network identity"
	case "tun2socks":
		return "  warning=proxy hides origin path, not data egress"
	default:
		return ""
	}
}

func listForTUI(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func npmGlobalsForTUI(values []profile.NPMGlobalPackage) string {
	if len(values) == 0 {
		return "none"
	}
	out := make([]string, 0, len(values))
	for _, pkg := range values {
		label := pkg.Package
		if len(pkg.Commands) > 0 {
			label += " (" + strings.Join(pkg.Commands, ",") + ")"
		}
		out = append(out, label)
	}
	return strings.Join(out, ",")
}

type labPortbridgeLoopbackOptions struct {
	enableLab bool
	listen    string
	target    string
	send      string
	expect    string
	timeout   time.Duration
}

type labPortbridgeDirectionOptions struct {
	enableLab bool
	listen    string
	target    string
	send      string
	expect    string
	timeout   time.Duration
}

type labBrowserControlOptions struct {
	enableLab   bool
	profileName string
	browserPath string
	timeout     time.Duration
}

type labPreviewOpenOptions struct {
	enableLab bool
	guestURL  string
	timeout   time.Duration
}

type labOutputField struct {
	key   string
	value string
}

type labProbeNotImplementedError struct {
	command  string
	guidance string
}

func (e labProbeNotImplementedError) Error() string {
	if e.guidance != "" {
		return fmt.Sprintf("hideout lab %s is not implemented; %s", e.command, e.guidance)
	}
	return fmt.Sprintf("hideout lab %s is not implemented", e.command)
}

func isLabProbeNotImplemented(err error) bool {
	var target labProbeNotImplementedError
	return errors.As(err, &target)
}

func (a app) lab(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>")
	}
	switch args[0] {
	case "portbridge":
		return a.labPortbridge(args[1:])
	case "browser-control":
		return a.labBrowserControl(args[1:])
	case "preview-open":
		return a.labPreviewOpen(args[1:])
	default:
		return fmt.Errorf("unknown lab command %q", args[0])
	}
}

func (a app) labPortbridge(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>")
	}
	switch args[0] {
	case "loopback":
		return a.labPortbridgeLoopback(args[1:])
	case "guest-to-host", "host-to-guest":
		return a.labPortbridgeDirection(args[0], args[1:])
	default:
		return fmt.Errorf("unknown lab portbridge command %q", args[0])
	}
}

func (a app) labPortbridgeDirection(mode string, args []string) error {
	opts := labPortbridgeDirectionOptions{
		listen:  "127.0.0.1:0",
		timeout: 2 * time.Second,
	}
	fs := flag.NewFlagSet("lab portbridge "+mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.listen, "listen", opts.listen, "loopback listen address")
	switch mode {
	case "guest-to-host":
		fs.StringVar(&opts.target, "target", "", "explicit host target address")
	case "host-to-guest":
		fs.StringVar(&opts.target, "guest-target", "", "explicit guest target address")
	default:
		return fmt.Errorf("unknown lab portbridge command %q", mode)
	}
	fs.StringVar(&opts.send, "send", "", "bytes to send through the bridge")
	fs.StringVar(&opts.expect, "expect", "", "expected bytes to read through the bridge")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: hideout lab portbridge %s --enable-lab --%s 127.0.0.1:<port> [--listen 127.0.0.1:0] [--send bytes --expect bytes]", mode, labPortbridgeTargetFlag(mode))
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.target) == "" {
		return fmt.Errorf("lab portbridge %s requires --%s", mode, labPortbridgeTargetFlag(mode))
	}
	if opts.expect != "" && opts.send == "" {
		return fmt.Errorf("lab portbridge %s requires --send when --expect is set", mode)
	}
	proposal := labPortbridgeDirectionProposal(mode, opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer cleanupLabLayout(layout)
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, "", "", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	bridge, err := portbridge.Start(ctx, portbridge.Spec{
		ID:            "lab_portbridge_" + strings.ReplaceAll(mode, "-", "_"),
		Direction:     labPortbridgeDirectionValue(mode),
		ListenScope:   portbridge.ListenScopeLoopback,
		ListenAddress: opts.listen,
		TargetAddress: opts.target,
	})
	if err != nil {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, "", "", err)
	}
	defer bridge.Close()
	a.printLabProbeEvidence(layout, proposal,
		labOutputField{"mode", mode},
		labOutputField{"listen", bridge.ListenAddress()},
		labOutputField{labPortbridgeTargetFlag(mode), opts.target},
	)
	if opts.send == "" {
		fmt.Fprintln(a.stdout, "probe=tcp-forward skipped")
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), "", nil)
	}
	got, err := probeTCPBridge(ctx, bridge.ListenAddress(), opts.send, opts.expect, opts.timeout)
	if err != nil {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), got, err)
	}
	if opts.expect != "" && got != opts.expect {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), got, fmt.Errorf("lab portbridge %s expected %q, got %q", mode, opts.expect, got))
	}
	fmt.Fprintln(a.stdout, "probe=tcp-forward ok")
	if opts.expect != "" {
		fmt.Fprintf(a.stdout, "received=%q\n", got)
	}
	return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), got, nil)
}

func labPortbridgeTargetFlag(mode string) string {
	if mode == "host-to-guest" {
		return "guest-target"
	}
	return "target"
}

func labPortbridgeDirectionValue(mode string) portbridge.Direction {
	if mode == "host-to-guest" {
		return portbridge.DirectionHostToGuest
	}
	return portbridge.DirectionGuestToHost
}

func (a app) labPortbridgeLoopback(args []string) error {
	opts := labPortbridgeLoopbackOptions{
		listen:  "127.0.0.1:0",
		timeout: 2 * time.Second,
	}
	fs := flag.NewFlagSet("lab portbridge loopback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.listen, "listen", opts.listen, "loopback listen address")
	fs.StringVar(&opts.target, "target", "", "explicit target address")
	fs.StringVar(&opts.send, "send", "", "bytes to send through the bridge")
	fs.StringVar(&opts.expect, "expect", "", "expected bytes to read through the bridge")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port> [--listen 127.0.0.1:0] [--send bytes --expect bytes]")
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.target) == "" {
		return errors.New("lab portbridge loopback requires --target")
	}
	if opts.expect != "" && opts.send == "" {
		return errors.New("lab portbridge loopback requires --send when --expect is set")
	}
	proposal := labPortbridgeLoopbackProposal(opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer func() {
		_ = os.RemoveAll(layout.TmpDir)
		_ = os.RemoveAll(layout.ShimDir)
	}()
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, "", "", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	bridge, err := portbridge.Start(ctx, portbridge.Spec{
		ID:            "lab_portbridge_loopback",
		Direction:     portbridge.DirectionGuestToHost,
		ListenScope:   portbridge.ListenScopeLoopback,
		ListenAddress: opts.listen,
		TargetAddress: opts.target,
	})
	if err != nil {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, "", "", err)
	}
	defer bridge.Close()
	fmt.Fprintln(a.stdout, "Hideout lab: experimental evidence only")
	fmt.Fprintf(a.stdout, "capability=%s\n", proposal.Action)
	fmt.Fprintf(a.stdout, "route=%s\n", proposal.Route)
	fmt.Fprintln(a.stdout, "mode=loopback")
	fmt.Fprintf(a.stdout, "session=%s\n", layout.ID)
	fmt.Fprintf(a.stdout, "audit=%s\n", layout.AuditPath)
	fmt.Fprintf(a.stdout, "listen=%s\n", bridge.ListenAddress())
	fmt.Fprintf(a.stdout, "target=%s\n", opts.target)
	if opts.send == "" {
		fmt.Fprintln(a.stdout, "probe=tcp-forward skipped")
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), "", nil)
	}
	got, err := probeTCPBridge(ctx, bridge.ListenAddress(), opts.send, opts.expect, opts.timeout)
	if err != nil {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), got, err)
	}
	if opts.expect != "" && got != opts.expect {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), got, fmt.Errorf("lab portbridge loopback expected %q, got %q", opts.expect, got))
	}
	fmt.Fprintln(a.stdout, "probe=tcp-forward ok")
	if opts.expect != "" {
		fmt.Fprintf(a.stdout, "received=%q\n", got)
	}
	return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), got, nil)
}

func labPortbridgeLoopbackProposal(opts labPortbridgeLoopbackOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:portbridge",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionPortbridgeProbe,
		Resources: []string{
			"portbridge:loopback",
			"listen:" + opts.listen,
			"target:" + opts.target,
		},
		Reason: "loopback port bridge capability probe",
	}
}

func labPortbridgeDirectionProposal(mode string, opts labPortbridgeDirectionOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:portbridge",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionPortbridgeProbe,
		Resources: []string{
			"portbridge:" + mode,
			"listen:" + opts.listen,
			labPortbridgeTargetFlag(mode) + ":" + opts.target,
		},
		Reason: mode + " port bridge capability probe",
	}
}

func (a app) labBrowserControl(args []string) error {
	opts := labBrowserControlOptions{timeout: 2 * time.Second}
	fs := flag.NewFlagSet("lab browser-control", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.profileName, "profile", "", "explicit Hideout profile name")
	fs.StringVar(&opts.browserPath, "browser-path", os.Getenv("HIDEOUT_BROWSER_PATH"), "direct Chromium-compatible browser binary")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout lab browser-control --enable-lab --profile <name> [--browser-path <path>]")
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.profileName) == "" {
		return errors.New("lab browser-control requires --profile")
	}
	if err := profile.ValidateName(opts.profileName); err != nil {
		return err
	}
	proposal := labBrowserControlProposal(opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer cleanupLabLayout(layout)
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabBrowserControlProbe(aw, layout, proposal, opts, "", "", false, err)
	}
	if strings.TrimSpace(opts.browserPath) == "" {
		return emitLabBrowserControlProbe(aw, layout, proposal, opts, "", "", false, errors.New("lab browser-control requires --browser-path or HIDEOUT_BROWSER_PATH"))
	}
	browserProfileDir := filepath.Join(layout.TmpDir, "browser-control-profile")
	controlURL, browserName, wsPresent, err := probeLabBrowserControl(context.Background(), opts, browserProfileDir)
	if err != nil {
		return emitLabBrowserControlProbe(aw, layout, proposal, opts, controlURL, browserName, wsPresent, err)
	}
	a.printLabProbeEvidence(layout, proposal,
		labOutputField{"mode", "browser-control"},
		labOutputField{"profile", opts.profileName},
		labOutputField{"browser-profile", "present"},
		labOutputField{"control-url", controlURL},
		labOutputField{"browser", browserName},
		labOutputField{"probe", "devtools-version ok"},
	)
	return emitLabBrowserControlProbe(aw, layout, proposal, opts, controlURL, browserName, wsPresent, nil)
}

func labBrowserControlProposal(opts labBrowserControlOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:browser",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionBrowserControlProbe,
		Resources: []string{
			"browser-control:loopback",
			"profile:" + opts.profileName,
		},
		Reason: "browser control capability probe",
	}
}

func probeLabBrowserControl(ctx context.Context, opts labBrowserControlOptions, browserProfileDir string) (string, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	if err := os.MkdirAll(browserProfileDir, 0o700); err != nil {
		return "", "", false, err
	}
	launcher, args, err := labBrowserControlCommand(opts.browserPath, browserProfileDir)
	if err != nil {
		return "", "", false, err
	}
	cmd := exec.CommandContext(ctx, launcher, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", "", false, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	}()
	port, err := waitForDevToolsActivePort(ctx, browserProfileDir, done)
	if err != nil {
		return "", "", false, err
	}
	controlURL := "http://" + net.JoinHostPort("127.0.0.1", port) + "/json/version"
	browserName, wsPresent, err := probeDevToolsVersion(ctx, controlURL, opts.timeout)
	return controlURL, browserName, wsPresent, err
}

func labBrowserControlCommand(browserPath, browserProfileDir string) (string, []string, error) {
	opener := hostopen.Opener{
		BrowserPath:       browserPath,
		BrowserProfileDir: browserProfileDir,
	}
	launcher, args, err := opener.URLCommand("about:blank")
	if err != nil {
		return "", nil, err
	}
	args = append([]string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
	}, args...)
	return launcher, args, nil
}

func waitForDevToolsActivePort(ctx context.Context, browserProfileDir string, browserDone <-chan error) (string, error) {
	path := filepath.Join(browserProfileDir, "DevToolsActivePort")
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
				port := strings.TrimSpace(lines[0])
				value, err := strconv.Atoi(port)
				if err == nil && value > 0 && value <= 65535 {
					return port, nil
				}
				return "", fmt.Errorf("browser DevToolsActivePort contains invalid port %q", port)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		select {
		case err, ok := <-browserDone:
			if ok && err != nil {
				return "", fmt.Errorf("browser exited before control endpoint became ready: %w", err)
			}
			return "", errors.New("browser exited before control endpoint became ready")
		case <-ctx.Done():
			return "", fmt.Errorf("browser control endpoint did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func probeDevToolsVersion(ctx context.Context, controlURL string, timeout time.Duration) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, controlURL, nil)
	if err != nil {
		return "", false, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false, fmt.Errorf("browser control version endpoint returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 16*1024))
	if err := dec.Decode(&payload); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(payload.Browser) == "" {
		return "", false, errors.New("browser control version endpoint did not report browser name")
	}
	return payload.Browser, strings.TrimSpace(payload.WebSocketDebuggerURL) != "", nil
}

func (a app) labPreviewOpen(args []string) error {
	opts := labPreviewOpenOptions{timeout: 2 * time.Second}
	fs := flag.NewFlagSet("lab preview-open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.guestURL, "guest-url", "", "explicit guest HTTP URL")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout lab preview-open --enable-lab --guest-url http://127.0.0.1:<port>")
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.guestURL) == "" {
		return errors.New("lab preview-open requires --guest-url")
	}
	if err := validateLabGuestURL(opts.guestURL); err != nil {
		return err
	}
	proposal := labPreviewOpenProposal(opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer cleanupLabLayout(layout)
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, "", 0, err)
	}
	guestURL, err := url.Parse(opts.guestURL)
	if err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, "", 0, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	bridge, err := portbridge.Start(ctx, portbridge.Spec{
		ID:            "lab_preview_open",
		Direction:     portbridge.DirectionHostToGuest,
		ListenScope:   portbridge.ListenScopeLoopback,
		ListenAddress: "127.0.0.1:0",
		TargetAddress: net.JoinHostPort(guestURL.Hostname(), guestURL.Port()),
	})
	if err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, "", 0, err)
	}
	defer bridge.Close()
	hostURL := labPreviewHostURL(*guestURL, bridge.ListenAddress())
	statusCode, err := probeLabHTTP(ctx, hostURL, opts.timeout)
	if err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, hostURL, statusCode, err)
	}
	a.printLabProbeEvidence(layout, proposal,
		labOutputField{"mode", "preview-open"},
		labOutputField{"guest-url", opts.guestURL},
		labOutputField{"host-url", hostURL},
		labOutputField{"status-code", fmt.Sprint(statusCode)},
		labOutputField{"probe", "http-get ok"},
	)
	return emitLabPreviewOpenProbe(aw, layout, proposal, opts, hostURL, statusCode, nil)
}

func validateLabGuestURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("lab preview-open guest URL is invalid: %w", err)
	}
	if u.Scheme != "http" {
		return errors.New("lab preview-open guest URL must use http")
	}
	if u.User != nil {
		return errors.New("lab preview-open guest URL must not contain user info")
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" {
		return errors.New("lab preview-open guest URL must target 127.0.0.1 or localhost")
	}
	if u.Port() == "" {
		return errors.New("lab preview-open guest URL must include an explicit port")
	}
	return nil
}

func labPreviewHostURL(guestURL url.URL, listenAddress string) string {
	guestURL.Scheme = "http"
	guestURL.Host = listenAddress
	return guestURL.String()
}

func probeLabHTTP(ctx context.Context, target string, timeout time.Duration) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, nil
}

func labPreviewOpenProposal(opts labPreviewOpenOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:preview",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionPreviewOpenProbe,
		Resources: []string{
			"preview-open:guest-http",
			"guest-url:" + opts.guestURL,
		},
		Reason: "preview open capability probe",
	}
}

func newLabAudit() (session.Layout, *audit.Writer, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return session.Layout{}, nil, err
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return session.Layout{}, nil, err
	}
	layout, err := session.New(store.Root)
	if err != nil {
		return session.Layout{}, nil, err
	}
	aw, err := audit.NewFile(layout.AuditPath)
	if err != nil {
		return session.Layout{}, nil, err
	}
	return layout, aw, nil
}

func cleanupLabLayout(layout session.Layout) {
	_ = os.RemoveAll(layout.TmpDir)
	_ = os.RemoveAll(layout.ShimDir)
}

func (a app) printLabProbeEvidence(layout session.Layout, proposal policy.LabProposal, fields ...labOutputField) {
	fmt.Fprintln(a.stdout, "Hideout lab: experimental evidence only")
	fmt.Fprintf(a.stdout, "capability=%s\n", proposal.Action)
	fmt.Fprintf(a.stdout, "route=%s\n", proposal.Route)
	fmt.Fprintf(a.stdout, "session=%s\n", layout.ID)
	fmt.Fprintf(a.stdout, "audit=%s\n", layout.AuditPath)
	for _, field := range fields {
		fmt.Fprintf(a.stdout, "%s=%s\n", field.key, field.value)
	}
}

func emitLabPortbridgeDirectionProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, mode string, opts labPortbridgeDirectionOptions, listen, received string, probeErr error) error {
	targetField := labPortbridgeTargetFlag(mode)
	decision := "allow"
	status := "error"
	if opts.send == "" && probeErr == nil {
		status = "skipped"
	} else if probeErr == nil {
		status = "ok"
	} else {
		decision = "error"
	}
	details := map[string]any{
		"probe":         "portbridge." + mode,
		"subject":       proposal.Subject,
		"route":         string(proposal.Route),
		"mode":          mode,
		"listen":        listen,
		targetField:     audit.RedactString(opts.target),
		"sendBytes":     len(opts.send),
		"expectBytes":   len(opts.expect),
		"receivedBytes": len(received),
		"status":        status,
		"timeoutMs":     opts.timeout.Milliseconds(),
		"targetField":   targetField,
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func emitLabBrowserControlProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, opts labBrowserControlOptions, controlURL, browserName string, wsPresent bool, probeErr error) error {
	decision := "allow"
	status := "ok"
	if probeErr != nil {
		decision = "error"
		status = "error"
	}
	if isLabProbeNotImplemented(probeErr) {
		status = "not-implemented"
	}
	details := map[string]any{
		"probe":                       "browser-control",
		"subject":                     proposal.Subject,
		"route":                       string(proposal.Route),
		"mode":                        "browser-control",
		"profile":                     audit.RedactString(opts.profileName),
		"browserPath":                 filepath.Base(opts.browserPath),
		"browserProfile":              "present",
		"controlURL":                  audit.RedactString(controlURL),
		"browser":                     browserName,
		"webSocketDebuggerURLPresent": wsPresent,
		"status":                      status,
		"timeoutMs":                   opts.timeout.Milliseconds(),
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
		if isLabProbeNotImplemented(probeErr) {
			details["errorType"] = "lab-probe-not-implemented"
		}
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func emitLabPreviewOpenProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, opts labPreviewOpenOptions, hostURL string, statusCode int, probeErr error) error {
	decision := "allow"
	status := "ok"
	if probeErr != nil {
		decision = "error"
		status = "error"
		if isLabProbeNotImplemented(probeErr) {
			status = "not-implemented"
		}
	}
	details := map[string]any{
		"probe":          "preview-open",
		"subject":        proposal.Subject,
		"route":          string(proposal.Route),
		"mode":           "preview-open",
		"guestURL":       audit.RedactString(opts.guestURL),
		"hostURL":        audit.RedactString(hostURL),
		"httpStatusCode": statusCode,
		"status":         status,
		"timeoutMs":      opts.timeout.Milliseconds(),
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
		if isLabProbeNotImplemented(probeErr) {
			details["errorType"] = "lab-probe-not-implemented"
		}
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func emitLabPortbridgeProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, opts labPortbridgeLoopbackOptions, listen, received string, probeErr error) error {
	decision := "allow"
	status := "ok"
	if opts.send == "" && probeErr == nil {
		status = "skipped"
	}
	if probeErr != nil {
		decision = "error"
		status = "error"
	}
	details := map[string]any{
		"probe":         "portbridge.loopback",
		"subject":       proposal.Subject,
		"route":         string(proposal.Route),
		"mode":          "loopback",
		"listen":        listen,
		"target":        audit.RedactString(opts.target),
		"sendBytes":     len(opts.send),
		"expectBytes":   len(opts.expect),
		"receivedBytes": len(received),
		"status":        status,
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func probeTCPBridge(ctx context.Context, address, send, expect string, timeout time.Duration) (string, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	if _, err := io.WriteString(conn, send); err != nil {
		return "", err
	}
	if expect == "" {
		return "", nil
	}
	buf := make([]byte, len(expect))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func serverError(server *manager.LocalServer) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- server.Wait()
	}()
	return ch
}
