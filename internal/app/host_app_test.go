package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestHostAppPlanReviewIsPlainLanguageAndSanitizesUntrustedText(t *testing.T) {
	plan := manager.HostAppPackPlan{
		Version: manager.HostAppPackPlanVersion, Operation: "add",
		SourceReview: manager.HostAppSourceReview{Kind: hostapppack.SourceGit, Location: "https://example.test/editor.git\x1b]8;;bad\x07", Commit: strings.Repeat("a", 40)},
		Profile:      "privacy", PackID: "community.editor", RevisionID: "rev_123", Access: hostapppack.AccessSafe,
		ExpectedSourceDigest: "sha256:" + strings.Repeat("a", 64), ExpectedIdentityDigest: "sha256:" + strings.Repeat("b", 64),
		SafetyProfileID: "editor-safe-v1", SafetyProfileVersion: "1",
		Review: manager.HostAppPackReview{
			PackID: "community.editor", Version: "1.0.0", Description: "Editor\x1b[31m forged\nnext-line",
			InstallHint: "run this\x1b]0;owned\x07", Commands: []string{"editor"}, Applications: []string{"editor"},
			ApplicationsDeclared: []manager.HostAppApplicationReview{{
				AppID: "editor", BundleNames: []string{"Editor.app"}, ExecutableRelativePath: "Contents/MacOS/Editor",
			}},
			ApplicationsObserved: []manager.HostAppIdentityReview{{
				AppID: "editor", Verification: "unverified", RootClass: "applications", OwnerClass: "operator",
				ContentDigest: "bundle-tree-v1:sha256:" + strings.Repeat("c", 64), IdentityDigest: "sha256:" + strings.Repeat("b", 64),
			}},
			ResourceKinds: []string{hostapppack.ResourceWorkspace}, ResultPolicies: []string{hostapppack.ResultNone}, UntrustedPackageFields: true,
		},
		CommandPlan: cmdproxy.HostAppCommandPlan{Replacements: []cmdproxy.HostAppOwnerReplacement{{Command: "editor", FromOwner: "old", ToOwner: "new"}}},
	}
	var out bytes.Buffer
	writeHostAppPlanReview(&out, plan)
	text := out.String()
	for _, required := range []string{"Host application recipe review", "Source:", "Snapshot:", "Package description [untrusted]", "Commands: editor", "Host applications: editor", "Package app declaration [untrusted]: editor", "bundles=Editor.app", "executable=Contents/MacOS/Editor", "Resource kinds: workspace", "Result policy: none", "Access: safe", "Core-observed app trust: editor=unverified", "bundle=", "team=", "code=", "Core-observed app content: bundle-tree-v1", "Core safety profile: editor-safe-v1@1", "Core-observed app identity:", "Command replacement:", "future runs only"} {
		if !strings.Contains(text, required) {
			t.Fatalf("plain-language review lacks %q:\n%s", required, text)
		}
	}
	if strings.ContainsAny(text, "\x1b\x07\x00") || strings.Contains(text, "\nnext-line") || strings.Contains(text, "]0;owned") || strings.Contains(text, "]8;;bad") {
		t.Fatalf("terminal control or injected layout escaped sanitization: %q", text)
	}
}

func TestHostAppInitUsesSharedScaffold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recipe")
	var stdout bytes.Buffer
	a := app{stdout: &stdout, stderr: &stdout, stdin: strings.NewReader("")}
	err := a.hostAppCommand([]string{"init", "--dir", dir, "--id", "community.editor", "--app-id", "editor", "--command", "editor", "--bundle", "Editor.app", "--executable", "Contents/MacOS/Editor"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(dir, hostapppack.ManifestFileName))
	if err != nil || manifest.ID != "community.editor" || manifest.Bindings[0].Commands[0] != "editor" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
}

func TestHostAppCLIReviewConfirmationAndLifecycleParity(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	core := manager.New(store)
	configureAppTestHostIdentity(t, &core, root)
	packDir := filepath.Join(root, "pack")
	if err := hostapppack.Scaffold(hostapppack.ScaffoldRequest{Directory: packDir, PackID: "community.editor", AppID: "editor", Command: "editor", BundleName: "Editor.app", ExecutableRelativePath: "Contents/MacOS/Editor"}); err != nil {
		t.Fatal(err)
	}

	var refused bytes.Buffer
	a := app{stdout: &refused, stderr: &refused, stdin: strings.NewReader("")}
	if err := a.hostAppCommandWithCore(core, []string{"add", "--path", packDir, "--install-only"}); err == nil || !strings.Contains(refused.String(), "Package description [untrusted]") {
		t.Fatalf("non-interactive add must review then fail closed: err=%v output=%s", err, refused.String())
	}
	if _, err := os.Stat(hostapppack.NewStore(store.Root).RegistryPath()); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed add mutated registry: %v", err)
	}

	var output bytes.Buffer
	a.stdout, a.stderr = &output, &output
	if err := a.hostAppCommandWithCore(core, []string{"add", "--path", packDir, "--install-only", "--yes"}); err != nil {
		t.Fatal(err)
	}
	registry, err := hostapppack.NewStore(store.Root).LoadRegistry()
	if err != nil || len(registry.Packs) != 1 {
		t.Fatalf("registry=%+v err=%v", registry, err)
	}
	revision := registry.Packs[0].ActiveRevisionID
	for _, args := range [][]string{{"list"}, {"inspect", "community.editor"}, {"validate", "--revision", revision, "community.editor"}, {"test", "--revision", revision, "community.editor"}} {
		output.Reset()
		if err := a.hostAppCommandWithCore(core, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(output.String(), "community.editor") {
			t.Fatalf("%v output=%s", args, output.String())
		}
	}
	output.Reset()
	if err := a.hostAppCommandWithCore(core, []string{"enable", "--profile", "privacy", "--pack", "community.editor", "--revision", revision, "--access", hostapppack.AccessAskEachRun, "--yes"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "enabled for future runs") {
		t.Fatalf("enable output did not explain future-run scope: %s", output.String())
	}
}

func TestHostAppCLIExactCommitGitAddAndTTYConfirmation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "recipe")
	if err := hostapppack.Scaffold(hostapppack.ScaffoldRequest{Directory: repo, PackID: "community.git-cli", AppID: "editor", Command: "git-editor", BundleName: "Editor.app", ExecutableRelativePath: "Contents/MacOS/Editor"}); err != nil {
		t.Fatal(err)
	}
	runAppHostGit(t, repo, "init")
	runAppHostGit(t, repo, "add", hostapppack.ManifestFileName)
	runAppHostGit(t, repo, "-c", "user.name=Hideout Test", "-c", "user.email=test@hideout.invalid", "commit", "-m", "recipe")
	commit := strings.TrimSpace(runAppHostGit(t, repo, "rev-parse", "HEAD"))
	core := manager.New(profile.Store{Root: filepath.Join(root, "store")})
	var output bytes.Buffer
	a := app{stdout: &output, stderr: &output, stdin: strings.NewReader("")}
	for _, operation := range []string{"validate", "test"} {
		output.Reset()
		if err := a.hostAppCommandWithCore(core, []string{operation, "--git", repo, "--commit", commit}); err != nil {
			t.Fatalf("%s exact-commit source: %v", operation, err)
		}
		if !strings.Contains(output.String(), `"applied":true`) || !strings.Contains(output.String(), commit) {
			t.Fatalf("%s exact-commit output=%s", operation, output.String())
		}
		registry, err := hostapppack.NewStore(core.Store.Root).LoadRegistry()
		if err != nil || len(registry.Packs) != 0 {
			t.Fatalf("%s source check acquired authority: registry=%+v err=%v", operation, registry, err)
		}
	}
	output.Reset()
	if err := a.hostAppCommandWithCore(core, []string{"add", "--git", repo, "--commit", commit, "--install-only", "--yes"}); err != nil {
		t.Fatal(err)
	}
	registry, err := hostapppack.NewStore(core.Store.Root).LoadRegistry()
	if err != nil || len(registry.Packs) != 1 || registry.Packs[0].Revisions[0].Source.Commit != commit {
		t.Fatalf("exact-commit CLI add registry=%+v err=%v", registry, err)
	}

	output.Reset()
	a.stdin = strings.NewReader("yes\n")
	ok, err := a.confirmHostAppApplyWithTerminal(false, "Apply reviewed plan?", func() bool { return true })
	if err != nil || !ok || !strings.Contains(output.String(), "[y/N]") {
		t.Fatalf("TTY confirmation ok=%v err=%v output=%q", ok, err, output.String())
	}
	output.Reset()
	a.stdin = strings.NewReader("yes\n")
	ok, err = a.confirmHostAppApplyWithTerminal(false, "Apply reviewed plan?", func() bool { return false })
	if err != nil || ok || output.Len() != 0 {
		t.Fatalf("non-interactive confirmation must fail closed: ok=%v err=%v output=%q", ok, err, output.String())
	}
}

func TestHostAppCLIUpdateDisableRevokeRemoveAndInspectionRenderers(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	core := manager.New(store)
	configureAppTestHostIdentity(t, &core, root)
	packDir := filepath.Join(root, "pack")
	if err := hostapppack.Scaffold(hostapppack.ScaffoldRequest{Directory: packDir, PackID: "community.cli-lifecycle", AppID: "editor", Command: "cli-editor", BundleName: "Editor.app", ExecutableRelativePath: "Contents/MacOS/Editor"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	a := app{stdout: &output, stderr: &output, stdin: strings.NewReader("")}
	if err := a.hostAppCommandWithCore(core, []string{"add", "--path", packDir, "--profile", "privacy", "--yes"}); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(packDir, hostapppack.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "2.0.0"
	manifest.Apps[0].Launch.NewWindowFlag = "--new-window"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, hostapppack.ManifestFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	if err := a.hostAppCommandWithCore(core, []string{"update", "--path", packDir, "--profile", "privacy", "--pack", manifest.ID, "--yes"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Permission changes:") || !strings.Contains(output.String(), "updated") {
		t.Fatalf("update review/result omitted permission lifecycle: %s", output.String())
	}
	output.Reset()
	if err := a.hostAppCommandWithCore(core, []string{"inspect", "--profile", "privacy", manifest.ID}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Host application recipes", "cli-editor", "unverified", "ask-each-run", "accepted",
		"source=local", "quality-tests=", "capability=host.app.open-resource", "result=none",
		"resources=hostfs-portal, workspace", "permission-fingerprint=", "active-current-run=false",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("human inspection lacks %q: %s", want, output.String())
		}
	}
	output.Reset()
	if err := a.hostAppCommandWithCore(core, []string{"inspect", "--json", "--profile", "privacy", manifest.ID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), hostapppack.InspectionVersion) || strings.Contains(output.String(), "Contents/MacOS/Editor") {
		t.Fatalf("JSON inspection mismatch or path leak: %s", output.String())
	}

	for _, args := range [][]string{
		{"disable", "--profile", "privacy", "--pack", manifest.ID, "--yes"},
		{"revoke", "--pack", manifest.ID, "--yes"},
		{"remove", "--pack", manifest.ID, "--yes"},
	} {
		output.Reset()
		if err := a.hostAppCommandWithCore(core, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(output.String(), "Host application recipe") ||
			!strings.Contains(output.String(), "Core-observed app trust:") ||
			!strings.Contains(output.String(), "Result policy: none") {
			t.Fatalf("%v output=%s", args, output.String())
		}
	}
}

func configureAppTestHostIdentity(t *testing.T, core *manager.Core, root string) {
	t.Helper()
	appsRoot := filepath.Join(root, "Applications")
	executable := filepath.Join(appsRoot, "Editor.app", "Contents", "MacOS", "Editor")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("editor"), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := hostcap.ApplicationIdentityOptions{
		Roots:       []hostcap.ApplicationRoot{{Class: hostcap.ApplicationRootOperator, Path: appsRoot}},
		OperatorUID: uint32(os.Getuid()), ObserveSigning: func(string) (hostcap.SigningObservation, error) {
			return hostcap.SigningObservation{}, nil
		},
	}
	core.HostAppPlatform = hostcap.PlatformDarwin
	core.HostAppIdentityResolver = func(expectation hostcap.ApplicationExpectation, _ []string) (hostcap.ObservedApplicationIdentity, error) {
		return hostcap.ResolveApplicationIdentity(expectation, opts)
	}
}

func runAppHostGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
