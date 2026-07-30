package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestHostAppContributorCLIEndToEnd(t *testing.T) {
	fixture := newHostAppContributorFixture(t)
	packDir := filepath.Join(fixture.root, "recipe\x1b]8;;evil.invalid\x07FORGED\x1b]8;;\x07\nnext-line")

	initResult := runHostAppContributorCLI(
		t,
		"init",
		"--dir", packDir,
		"--id", "community.contributor-cli",
		"--app-id", "editor",
		"--command", "contributor-editor",
		"--bundle", "ContributorEditor.app",
		"--executable", "Contents/MacOS/ContributorEditor",
	)
	requireHostAppContributorSuccess(t, initResult)
	var created struct {
		Created   bool   `json:"created"`
		Directory string `json:"directory"`
		PackID    string `json:"packId"`
	}
	if err := json.Unmarshal([]byte(initResult.stdout), &created); err != nil {
		t.Fatalf("decode app init output: %v\n%s", err, initResult.stdout)
	}
	if !created.Created || created.Directory != packDir || created.PackID != "community.contributor-cli" {
		t.Fatalf("unexpected app init result: %+v", created)
	}
	assertHostAppContributorTerminalSafe(t, initResult.stdout+initResult.stderr)

	manifestPath := filepath.Join(packDir, hostapppack.ManifestFileName)
	manifest, _, err := hostapppack.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Description = "Contributor package prose"
	writeHostAppContributorManifest(t, manifestPath, manifest)

	sourceValidate := runHostAppContributorCLI(t, "validate", "--path", packDir)
	requireHostAppContributorSuccess(t, sourceValidate)
	sourceValidated := decodeHostAppContributorResult(t, sourceValidate.stdout)
	if !sourceValidated.Applied || sourceValidated.Plan.Operation != "validate" || sourceValidated.Plan.PackID != manifest.ID ||
		sourceValidated.Plan.ExpectedSourceDigest == "" || sourceValidated.Revision != nil {
		t.Fatalf("unexpected source validation result: %+v", sourceValidated)
	}
	sourceTest := runHostAppContributorCLI(t, "test", "--path", packDir)
	requireHostAppContributorSuccess(t, sourceTest)
	sourceTested := decodeHostAppContributorResult(t, sourceTest.stdout)
	if !sourceTested.Applied || sourceTested.Plan.Operation != "test" || sourceTested.Test == nil ||
		sourceTested.Test.Status != hostapppack.TestPassed || sourceTested.Test.Passed != 1 || sourceTested.Revision != nil {
		t.Fatalf("unexpected source test result: %+v", sourceTested)
	}
	requireHostAppContributorPackCount(t, fixture.storeRoot, 0)
	assertHostAppContributorTerminalSafe(t, sourceValidate.stdout+sourceValidate.stderr+sourceTest.stdout+sourceTest.stderr)

	addResult := runHostAppContributorCLI(t, "add", "--path", packDir, "--install-only", "--yes")
	requireHostAppContributorSuccess(t, addResult)
	for _, want := range []string{
		"Host application recipe review",
		"Source: local <local-directory>/recipeFORGEDnext-line",
		"Package description [untrusted]: Contributor package prose",
		"Effect: store immutable bytes only; no command is enabled",
		"installed without enabling commands",
	} {
		if !strings.Contains(addResult.stdout, want) {
			t.Fatalf("app add output lacks %q:\n%s", want, addResult.stdout)
		}
	}
	assertHostAppContributorTerminalSafe(t, addResult.stdout+addResult.stderr)
	if strings.Contains(addResult.stdout, fixture.root) || strings.Contains(addResult.stdout, "evil.invalid") || strings.Contains(addResult.stdout, "\nnext-line") {
		t.Fatalf("app add exposed a source path or terminal-layout injection: %q", addResult.stdout)
	}

	registry := loadHostAppContributorRegistry(t, fixture.storeRoot)
	if len(registry.Packs) != 1 || registry.Packs[0].ID != manifest.ID || len(registry.Packs[0].Revisions) != 1 {
		t.Fatalf("unexpected contributor registry: %+v", registry)
	}
	revisionID := registry.Packs[0].ActiveRevisionID
	installedManifest := filepath.Join(hostapppack.NewStore(fixture.storeRoot).SourceDir(manifest.ID, revisionID), hostapppack.ManifestFileName)
	if _, err := os.Stat(installedManifest); err != nil {
		t.Fatalf("installed immutable manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("{\"mutatedSource\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	validateResult := runHostAppContributorCLI(t, "validate", "--revision", revisionID, manifest.ID)
	requireHostAppContributorSuccess(t, validateResult)
	validated := decodeHostAppContributorResult(t, validateResult.stdout)
	if !validated.Applied || validated.Plan.Operation != "validate" || validated.Plan.PackID != manifest.ID || validated.Revision == nil || validated.Revision.RevisionID != revisionID {
		t.Fatalf("unexpected app validate result: %+v", validated)
	}

	testResult := runHostAppContributorCLI(t, "test", "--revision", revisionID, manifest.ID)
	requireHostAppContributorSuccess(t, testResult)
	tested := decodeHostAppContributorResult(t, testResult.stdout)
	if !tested.Applied || tested.Plan.Operation != "test" || tested.Test == nil || tested.Test.Status != hostapppack.TestPassed || tested.Test.Passed != 1 {
		t.Fatalf("unexpected app test result: %+v", tested)
	}
	persisted, err := hostapppack.NewStore(fixture.storeRoot).LoadTestResult(manifest.ID, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != hostapppack.TestPassed || persisted.Passed != 1 || persisted.RevisionID != revisionID {
		t.Fatalf("unexpected persisted quality result: %+v", persisted)
	}
	assertHostAppContributorTerminalSafe(t, validateResult.stdout+validateResult.stderr+testResult.stdout+testResult.stderr)
}

func TestHostAppContributorInitResolvesDocumentedRelativeDirectory(t *testing.T) {
	fixture := newHostAppContributorFixture(t)
	t.Chdir(fixture.root)
	result := runHostAppContributorCLI(
		t,
		"init",
		"--dir", "./relative-recipe",
		"--id", "community.relative-recipe",
		"--app-id", "editor",
		"--command", "relative-editor",
		"--bundle", "RelativeEditor.app",
		"--executable", "Contents/MacOS/RelativeEditor",
	)
	requireHostAppContributorSuccess(t, result)
	var created struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &created); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(fixture.root, "relative-recipe")
	if created.Directory != want {
		t.Fatalf("scaffold directory=%q, want %q", created.Directory, want)
	}
	if _, err := os.Stat(filepath.Join(want, hostapppack.ManifestFileName)); err != nil {
		t.Fatalf("relative scaffold manifest: %v", err)
	}
}

func TestHostAppContributorSourceValidationFailsClosedOnPlanDrift(t *testing.T) {
	fixture := newHostAppContributorFixture(t)
	packDir := filepath.Join(fixture.root, "source-drift")
	initHostAppContributorPack(t, packDir, "community.source-drift", "source-drift-editor", "SourceDriftEditor.app")
	core := manager.Core{Store: profile.Store{Root: fixture.storeRoot}}
	plan, err := core.PlanHostAppPack(manager.HostAppPackOptions{
		Operation: "validate", SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(packDir, hostapppack.ManifestFileName)
	manifest, _, err := hostapppack.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Description = "changed after the read-only plan"
	writeHostAppContributorManifest(t, manifestPath, manifest)
	if _, err := core.ApplyHostAppPack(plan); err == nil || !strings.Contains(err.Error(), recovery.CodeHostAppDigestMismatch) || !strings.Contains(err.Error(), "plan is stale") {
		t.Fatalf("source drift apply error=%v, want stale rejection", err)
	}
	requireHostAppContributorPackCount(t, fixture.storeRoot, 0)
	auditPaths, err := filepath.Glob(filepath.Join(fixture.storeRoot, "sessions", "*", "audit.jsonl"))
	if err != nil || len(auditPaths) == 0 {
		t.Fatalf("source drift audit paths=%v err=%v", auditPaths, err)
	}
	var auditText strings.Builder
	for _, path := range auditPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		auditText.Write(data)
	}
	for _, want := range []string{`"action":"host.app.validate"`, `"decision":"deny"`, `"recoveryCode":"` + recovery.CodeHostAppDigestMismatch + `"`} {
		if !strings.Contains(auditText.String(), want) {
			t.Fatalf("source drift audit lacks %s: %s", want, auditText.String())
		}
	}
}

func TestHostAppContributorCLIActionableErrors(t *testing.T) {
	fixture := newHostAppContributorFixture(t)

	t.Run("source-digest", func(t *testing.T) {
		packDir := filepath.Join(fixture.root, "digest-mismatch")
		initHostAppContributorPack(t, packDir, "community.digest-mismatch", "digest-editor", "DigestEditor.app")
		result := runHostAppContributorCLI(
			t, "validate", "--path", packDir,
			"--expected-digest", "sha256:"+strings.Repeat("0", 64),
		)
		requireHostAppContributorFailure(t, result, recovery.CodeHostAppDigestMismatch, "source digest mismatch")
		requireHostAppContributorPackCount(t, fixture.storeRoot, 0)
	})

	t.Run("schema", func(t *testing.T) {
		packDir := filepath.Join(fixture.root, "invalid-schema")
		initHostAppContributorPack(t, packDir, "community.invalid-schema", "schema-editor", "MissingSchemaEditor.app")
		manifestPath := filepath.Join(packDir, hostapppack.ManifestFileName)
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		document["unexpectedAuthority"] = map[string]any{"hostExec": true}
		raw, err = json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}

		result := runHostAppContributorCLI(t, "add", "--path", packDir, "--install-only", "--yes")
		requireHostAppContributorFailure(t, result, recovery.CodeHostAppSourceInvalid, `unknown field "unexpectedAuthority"`)
		requireHostAppContributorPackCount(t, fixture.storeRoot, 0)
	})

	t.Run("identity", func(t *testing.T) {
		packDir := filepath.Join(fixture.root, "missing-identity")
		initHostAppContributorPack(t, packDir, "community.missing-identity", "missing-editor", "DefinitelyMissingT072.app")

		result := runHostAppContributorCLI(t, "add", "--path", packDir, "--profile", "default", "--yes")
		requireHostAppContributorFailure(t, result, recovery.CodeHostAppAbsent)
		if !strings.Contains(result.stderr, "application bundle was not found") && !strings.Contains(result.stderr, "available on macOS hosts") {
			t.Fatalf("identity error is not actionable: %s", result.stderr)
		}
		requireHostAppContributorPackCount(t, fixture.storeRoot, 0)
	})

	t.Run("reserved-command-conflict", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("production host-app identity resolution is macOS-only in v1")
		}
		writeUnsignedHostAppContributorBundle(t, fixture.home, "ContributorConflict.app", "ContributorConflict")
		packDir := filepath.Join(fixture.root, "reserved-command")
		initHostAppContributorPack(t, packDir, "community.reserved-command", "hideout", "ContributorConflict.app")

		result := runHostAppContributorCLI(t, "add", "--path", packDir, "--profile", "default", "--yes")
		requireHostAppContributorFailure(
			t,
			result,
			recovery.CodeHostAppCommandConflict,
			`host-app command "hideout" is reserved by Core`,
			"Hideout operator control command",
		)
		requireHostAppContributorPackCount(t, fixture.storeRoot, 0)
	})
}

type hostAppContributorFixture struct {
	root      string
	home      string
	storeRoot string
}

func newHostAppContributorFixture(t *testing.T) hostAppContributorFixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	temporary := filepath.Join(root, "temporary")
	storeRoot := filepath.Join(root, "store")
	for _, dir := range []string{home, temporary} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", temporary)
	t.Setenv("HIDEOUT_STORE_ROOT", storeRoot)
	return hostAppContributorFixture{root: root, home: home, storeRoot: storeRoot}
}

type hostAppContributorCLIResult struct {
	code   int
	stdout string
	stderr string
}

func runHostAppContributorCLI(t *testing.T, args ...string) hostAppContributorCLIResult {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Main(append([]string{"app"}, args...), &stdout, &stderr)
	return hostAppContributorCLIResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func requireHostAppContributorSuccess(t *testing.T, result hostAppContributorCLIResult) {
	t.Helper()
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("CLI failed with code %d\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
}

func requireHostAppContributorFailure(t *testing.T, result hostAppContributorCLIResult, recoveryCode string, details ...string) {
	t.Helper()
	if result.code != 1 {
		t.Fatalf("CLI code=%d, want 1\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
	for _, want := range append([]string{"hideout: " + recoveryCode + ":"}, details...) {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("CLI error lacks %q:\n%s", want, result.stderr)
		}
	}
	assertHostAppContributorTerminalSafe(t, result.stdout+result.stderr)
}

func assertHostAppContributorTerminalSafe(t *testing.T, output string) {
	t.Helper()
	if strings.ContainsAny(output, "\x00\x07\x1b") ||
		strings.IndexByte(output, '\x9b') >= 0 {
		t.Fatalf("CLI output contains terminal control bytes: %q", output)
	}
}

func initHostAppContributorPack(t *testing.T, dir, packID, command, bundle string) {
	t.Helper()
	result := runHostAppContributorCLI(
		t,
		"init",
		"--dir", dir,
		"--id", packID,
		"--app-id", "editor",
		"--command", command,
		"--bundle", bundle,
		"--executable", "Contents/MacOS/"+strings.TrimSuffix(bundle, ".app"),
	)
	requireHostAppContributorSuccess(t, result)
}

func writeHostAppContributorManifest(t *testing.T, path string, manifest hostapppack.Manifest) {
	t.Helper()
	raw, err := json.MarshalIndent(hostapppack.NormalizeManifest(manifest), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeHostAppContributorResult(t *testing.T, output string) manager.HostAppPackResult {
	t.Helper()
	var result manager.HostAppPackResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode host-app CLI result: %v\n%s", err, output)
	}
	return result
}

func loadHostAppContributorRegistry(t *testing.T, storeRoot string) hostapppack.Registry {
	t.Helper()
	registry, err := hostapppack.NewStore(storeRoot).LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func requireHostAppContributorPackCount(t *testing.T, storeRoot string, want int) {
	t.Helper()
	registry := loadHostAppContributorRegistry(t, storeRoot)
	if len(registry.Packs) != want {
		t.Fatalf("registry contains %d packs, want %d: %+v", len(registry.Packs), want, registry)
	}
}

func writeUnsignedHostAppContributorBundle(t *testing.T, home, bundleName, executableName string) {
	t.Helper()
	bundle := filepath.Join(home, "Applications", bundleName)
	executable := filepath.Join(bundle, "Contents", "MacOS", executableName)
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>` + executableName + `</string>
<key>CFBundleIdentifier</key><string>invalid.hideout.contributor-test</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
}
