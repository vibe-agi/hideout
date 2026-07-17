package appopen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testSafetyProfile() SafetyProfile {
	return SafetyProfile{
		ID:      "editor-family-v1",
		Version: "1",
		IdentityMatchers: []SafetyIdentityMatcher{{
			Platform: "darwin", BundleID: "com.example.Editor", TeamID: "TEAM123456",
			ExecutableRelativePath: "Contents/MacOS/Editor", ExecutableIdentityPolicy: ExecutableIdentityExactObserved,
		}},
		RequiredArgv:  []string{"--disable-extensions"},
		ForbiddenArgv: []string{"--disable-workspace-trust"},
		LaunchSyntax: LaunchSyntaxProfile{
			AllowedGotoFlags:        []string{"--goto"},
			AllowedNewWindowFlags:   []string{"--new-window"},
			AllowedReuseWindowFlags: []string{"--reuse-window"},
			AllowedGotoSeparators:   []string{":"},
			AllowPositionalTarget:   true,
		},
		IsolatedState: IsolatedStateSpec{Kind: IsolatedStateQualifiedAppRun, ArgumentFlag: "--user-data-dir", SettingsRelativePath: "User/settings.json"},
		RequiredSettings: map[string]any{
			"security.workspace.trust.enabled": true,
			"task.allowAutomaticTasks":         "off",
		},
		ForbiddenSettings: map[string]any{
			"security.workspace.trust.enabled": false,
			"task.allowAutomaticTasks":         "on",
		},
		Verification: []string{VerificationCombinedEffectV1},
	}
}

func testSafetyIdentity() SafetyIdentity {
	return SafetyIdentity{
		Signed: true, Platform: "darwin", BundleID: "com.example.Editor", TeamID: "TEAM123456", CodeIdentity: "code-id",
		ExecutableRelativePath: "Contents/MacOS/Editor", ExecutableCodeIdentity: "sha256:exact-editor-binary",
	}
}

func testSafeLaunchSpec() LaunchSpec {
	return LaunchSpec{GotoFlag: "--goto", GotoSeparator: ":", NewWindowFlag: "--new-window", ReuseWindowFlag: "--reuse-window"}
}

func TestSelectSafetyProfileRequiresCoreIdentityCompatibility(t *testing.T) {
	profile := testSafetyProfile()
	got, err := SelectSafetyProfile([]SafetyProfile{profile}, profile.ID, testSafetyIdentity())
	if err != nil || got.ID != profile.ID {
		t.Fatalf("compatible profile not selected: %+v err=%v", got, err)
	}
	unknown := testSafetyIdentity()
	unknown.Signed = false
	if _, err := SelectSafetyProfile([]SafetyProfile{profile}, profile.ID, unknown); err == nil {
		t.Fatal("unsigned/unknown app must not obtain safe status")
	}
	mismatch := testSafetyIdentity()
	mismatch.TeamID = "OTHERTEAM0"
	if _, err := SelectSafetyProfile([]SafetyProfile{profile}, profile.ID, mismatch); err == nil {
		t.Fatal("identity mismatch must not obtain safe status")
	}
	helper := testSafetyIdentity()
	helper.ExecutableRelativePath = "Contents/Helpers/GuestWritableHelper"
	if _, err := SelectSafetyProfile([]SafetyProfile{profile}, profile.ID, helper); err == nil {
		t.Fatal("another executable inside the same trusted bundle inherited safe status")
	}
	missingExecutableIdentity := testSafetyIdentity()
	missingExecutableIdentity.ExecutableCodeIdentity = ""
	if _, err := SelectSafetyProfile([]SafetyProfile{profile}, profile.ID, missingExecutableIdentity); err == nil {
		t.Fatal("safe status was granted without an exact executable identity")
	}
}

func TestBuildSafeEffectCombinesCoreArgvSettingsAndRunState(t *testing.T) {
	profile := testSafetyProfile()
	req := OpenRequest{
		BinaryPath:      "/Applications/Editor.app/Contents/MacOS/Editor",
		Mode:            ModeSafe,
		HostTarget:      "/Users/operator/workspace/a.go",
		Line:            12,
		Column:          3,
		SafeUserDataDir: t.TempDir(),
		QualifiedAppRef: "community.editor/editor",
		RunID:           "run-123",
	}
	effect, err := BuildSafeEffect(testSafeLaunchSpec(), req, profile, testSafetyIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(effect.Argv, "--disable-extensions") || !slices.Contains(effect.Argv, "--user-data-dir") {
		t.Fatalf("Core safety argv missing: %v", effect.Argv)
	}
	if effect.Settings["task.allowAutomaticTasks"] != "off" || effect.Settings["security.workspace.trust.enabled"] != true {
		t.Fatalf("Core settings missing: %#v", effect.Settings)
	}
	if filepath.Clean(effect.StateRoot) == filepath.Clean(req.SafeUserDataDir) || !pathWithin(req.SafeUserDataDir, effect.StateRoot) {
		t.Fatalf("state is not isolated by app/run: %q", effect.StateRoot)
	}
	if err := ValidateSafetyEffect(profile, testSafetyIdentity(), effect); err != nil {
		t.Fatalf("valid combined effect rejected: %v", err)
	}
	changedExecutable := testSafetyIdentity()
	changedExecutable.ExecutableCodeIdentity = "sha256:changed-editor-binary"
	if err := ValidateSafetyEffect(profile, changedExecutable, effect); err == nil {
		t.Fatal("safe effect survived executable identity drift")
	}
}

func TestSafetyFloorRejectsArgvAndEquivalentSettingBypass(t *testing.T) {
	profile := testSafetyProfile()
	req := OpenRequest{BinaryPath: "/bin/editor", Mode: ModeSafe, HostTarget: "/workspace", SafeUserDataDir: t.TempDir(), QualifiedAppRef: "p/a", RunID: "r"}

	spec := LaunchSpec{SafeConfiguration: &SafeConfigurationSpec{RelativePath: "User/settings.json", Values: map[string]any{"security.workspace.trust.enabled": false}}}
	if _, err := BuildSafeEffect(spec, req, profile, testSafetyIdentity()); err == nil {
		t.Fatal("package-authored safe settings must not participate in Core safe mode")
	}

	effect, err := BuildSafeEffect(testSafeLaunchSpec(), req, profile, testSafetyIdentity())
	if err != nil {
		t.Fatal(err)
	}
	effect.Argv = append(effect.Argv, "--disable-workspace-trust")
	if err := ValidateSafetyEffect(profile, testSafetyIdentity(), effect); err == nil {
		t.Fatal("forbidden argv must fail the combined effect")
	}
	effect, err = BuildSafeEffect(testSafeLaunchSpec(), req, profile, testSafetyIdentity())
	if err != nil {
		t.Fatal(err)
	}
	effect.Argv = append(effect.Argv, "--inspect-port=9229")
	if err := ValidateSafetyEffect(profile, testSafetyIdentity(), effect); err == nil {
		t.Fatal("an unreviewed extra argv effect must fail exact Core reconstruction")
	}
	effect, err = BuildSafeEffect(testSafeLaunchSpec(), req, profile, testSafetyIdentity())
	if err != nil {
		t.Fatal(err)
	}
	effect.Settings["security.workspace.trust.enabled"] = false
	if err := ValidateSafetyEffect(profile, testSafetyIdentity(), effect); err == nil {
		t.Fatal("forbidden equivalent setting must fail the combined effect")
	}
}

func TestSafetyFloorRejectsUnreviewedPackageLaunchSemantics(t *testing.T) {
	profile := testSafetyProfile()
	req := OpenRequest{
		BinaryPath: "/bin/editor", Mode: ModeSafe, HostTarget: "/workspace/a.go",
		Line: 12, Column: 3, SafeUserDataDir: t.TempDir(), QualifiedAppRef: "p/a", RunID: "r",
	}
	for _, spec := range []LaunchSpec{
		{GotoFlag: "--inspect-extensions", GotoSeparator: ":", ReuseWindowFlag: "--reuse-window"},
		{GotoFlag: "--goto", GotoSeparator: "=", NewWindowFlag: "--new-window", ReuseWindowFlag: "--reuse-window"},
		{GotoFlag: "--goto", GotoSeparator: ":", NewWindowFlag: "--eval-workspace", ReuseWindowFlag: "--reuse-window"},
	} {
		if _, err := BuildSafeEffect(spec, req, profile, testSafetyIdentity()); err == nil {
			t.Fatalf("unreviewed package launch semantics obtained safe status: %+v", spec)
		}
	}
}

func TestSafeStateIsSeparatedByQualifiedAppAndRun(t *testing.T) {
	base := t.TempDir()
	a, err := QualifiedRunStateRoot(base, "pack.one/editor", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := QualifiedRunStateRoot(base, "pack.two/editor", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	c, err := QualifiedRunStateRoot(base, "pack.one/editor", "run-b")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == c || b == c {
		t.Fatalf("state identities collided: %q %q %q", a, b, c)
	}
}

func TestQualifiedRunStateRootLeavesDarwinLocalIPCSocketBudget(t *testing.T) {
	base := "/Users/operator/.hideout/profiles/default/host-app/state"
	root, err := QualifiedRunStateRoot(base, "builtin.vscode/rev_0123456789abcdef/vscode", "ses_20260716T113520Z_a4aedc3b131e8ca5d51b")
	if err != nil {
		t.Fatal(err)
	}
	const maxDarwinUnixSocketPath = 103
	if socket := filepath.Join(root, "1.12-main.sock"); len(socket) > maxDarwinUnixSocketPath {
		t.Fatalf("qualified state leaves no Darwin IPC budget: len(%q)=%d", socket, len(socket))
	}
}

func TestPrepareSafetyProfileStateWritesOnlyValidatedCoreSettings(t *testing.T) {
	profile := testSafetyProfile()
	identity := testSafetyIdentity()
	req := OpenRequest{BinaryPath: "/bin/editor", Mode: ModeSafe, HostTarget: "/workspace", SafeUserDataDir: t.TempDir(), QualifiedAppRef: "pack/editor", RunID: "run"}
	effect, err := BuildSafeEffect(testSafeLaunchSpec(), req, profile, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareSafetyProfileState(profile, identity, effect); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(effect.StateRoot, "User", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"task.allowAutomaticTasks": "off"`) {
		t.Fatalf("reviewed Core settings were not materialized: %s", data)
	}

	forged := effect
	forged.Settings = map[string]any{"task.allowAutomaticTasks": "on"}
	if err := PrepareSafetyProfileState(profile, identity, forged); err == nil {
		t.Fatal("state writer must independently reject a forged unsafe effect")
	}
}
