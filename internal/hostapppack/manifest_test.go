package hostapppack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeManifestStrictAndCanonical(t *testing.T) {
	manifest := validHostAppManifest()
	manifest.Apps[0].BundleNames = []string{"Zed.app", "Cursor.app"}
	manifest.Bindings[0].Commands = []string{"zed", "cursor"}
	manifest.Bindings[0].ResourceKinds = []string{ResourceWorkspace, ResourceHostFSPortal}
	raw := marshalManifest(t, manifest)

	got, err := DecodeManifest(raw)
	if err != nil {
		t.Fatalf("decode valid manifest: %v", err)
	}
	if got.Apps[0].BundleNames[0] != "Cursor.app" || got.Bindings[0].Commands[0] != "cursor" || got.Bindings[0].ResourceKinds[0] != ResourceHostFSPortal {
		t.Fatalf("manifest was not canonically normalized: %+v", got)
	}
	canonical, err := CanonicalManifestBytes(got)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := DecodeManifest(canonical)
	if err != nil {
		t.Fatalf("decode canonical manifest: %v", err)
	}
	if string(canonical) != string(mustCanonical(t, roundTrip)) {
		t.Fatal("canonical manifest encoding is unstable")
	}
}

func TestDecodeManifestRejectsUnknownFieldsAndMultipleValues(t *testing.T) {
	valid := string(marshalManifest(t, validHostAppManifest()))
	for _, mutation := range []struct {
		name string
		raw  string
	}{
		{"top-level", strings.Replace(valid, `"version":"1.0.0"`, `"version":"1.0.0","script":"evil"`, 1)},
		{"nested-app", strings.Replace(valid, `"id":"cursor"`, `"id":"cursor","hook":"evil"`, 1)},
		{"nested-binding", strings.Replace(valid, `"appId":"cursor"`, `"appId":"cursor","provider":"host.exec"`, 1)},
		{"nested-grammar", strings.Replace(valid, `"resourceCount":1`, `"resourceCount":1,"javascript":"evil"`, 1)},
		{"multiple-json-values", valid + `{}`},
		{"malformed-trailing", valid + ` garbage`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := DecodeManifest([]byte(mutation.raw)); err == nil {
				t.Fatal("expected strict decoding rejection")
			}
		})
	}
}

func TestValidateManifestRejectsIdentifiersBundleAndExecutableEscapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"builtin-namespace", func(m *Manifest) { m.ID = "builtin.cursor" }},
		{"uppercase-pack", func(m *Manifest) { m.ID = "Community.Cursor" }},
		{"invalid-app-id", func(m *Manifest) { m.Apps[0].ID = "../cursor" }},
		{"duplicate-app", func(m *Manifest) { m.Apps = append(m.Apps, m.Apps[0]) }},
		{"bundle-path", func(m *Manifest) { m.Apps[0].BundleNames = []string{"/Applications/Cursor.app"} }},
		{"bundle-variable", func(m *Manifest) { m.Apps[0].BundleNames = []string{"$HOME/Cursor.app"} }},
		{"bundle-not-app", func(m *Manifest) { m.Apps[0].BundleNames = []string{"cursor"} }},
		{"executable-absolute", func(m *Manifest) { m.Apps[0].ExecutableRelativePath = "/bin/sh" }},
		{"executable-traversal", func(m *Manifest) { m.Apps[0].ExecutableRelativePath = "../evil" }},
		{"executable-unclean", func(m *Manifest) { m.Apps[0].ExecutableRelativePath = "Contents/../evil" }},
		{"unsupported-platform", func(m *Manifest) { m.Apps[0].Platforms = []string{"linux"} }},
		{"missing-app-reference", func(m *Manifest) { m.Bindings[0].AppID = "missing" }},
		{"duplicate-command", func(m *Manifest) { m.Bindings[0].Commands = []string{"cursor", "cursor"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validHostAppManifest()
			tt.mutate(&manifest)
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("expected manifest rejection")
			}
		})
	}
}

func TestValidateManifestEnforcesClosedAuthorityAllowlists(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"capability", func(m *Manifest) { m.Bindings[0].CapabilityID = "host.exec" }},
		{"result", func(m *Manifest) { m.Bindings[0].ResultPolicy = "stdout" }},
		{"resource", func(m *Manifest) { m.Bindings[0].ResourceKinds = []string{"host-path"} }},
		{"access", func(m *Manifest) { m.Bindings[0].RequestedAccess = "always" }},
		{"grammar", func(m *Manifest) { m.Bindings[0].Grammar.Kind = "javascript" }},
		{"resource-count", func(m *Manifest) { m.Bindings[0].Grammar.ResourceCount = 2 }},
		{"unknown-flags", func(m *Manifest) { m.Bindings[0].Grammar.UnknownFlags = "pass" }},
		{"raw-flag", func(m *Manifest) { m.Bindings[0].Grammar.GotoFlags = []string{"--goto;sh"} }},
		{"duplicate-flag-across-groups", func(m *Manifest) { m.Bindings[0].Grammar.NewWindowFlags = []string{"--goto"} }},
		{"raw-launch-flag", func(m *Manifest) { m.Apps[0].Launch.GotoFlag = "--goto --shell" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validHostAppManifest()
			tt.mutate(&manifest)
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("expected authority allowlist rejection")
			}
		})
	}
}

func TestValidateManifestBoundsCountsAndUntrustedText(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"too-many-apps", func(m *Manifest) {
			base := m.Apps[0]
			m.Apps = nil
			for i := 0; i < MaxApps+1; i++ {
				app := base
				app.ID = "app-" + twoDigits(i)
				m.Apps = append(m.Apps, app)
			}
		}},
		{"too-many-bindings", func(m *Manifest) {
			base := m.Bindings[0]
			m.Bindings = nil
			for i := 0; i < MaxBindings+1; i++ {
				binding := base
				binding.ID = "binding-" + twoDigits(i)
				binding.Commands = []string{"command-" + twoDigits(i)}
				m.Bindings = append(m.Bindings, binding)
			}
		}},
		{"too-many-aliases", func(m *Manifest) {
			m.Bindings[0].Commands = nil
			for i := 0; i < MaxCommandsPerBinding+1; i++ {
				m.Bindings[0].Commands = append(m.Bindings[0].Commands, "command-"+twoDigits(i))
			}
		}},
		{"description-too-long", func(m *Manifest) { m.Description = strings.Repeat("x", MaxDescriptionBytes+1) }},
		{"install-hint-too-long", func(m *Manifest) { m.InstallHint.Text = strings.Repeat("x", MaxHintBytes+1) }},
		{"ansi", func(m *Manifest) { m.Description = "safe\x1b[31mred" }},
		{"osc", func(m *Manifest) { m.InstallHint.Text = "click\x1b]8;;https://evil.invalid\x07here" }},
		{"newline", func(m *Manifest) { m.Description = "first\nsecond" }},
		{"c1-control", func(m *Manifest) { m.Description = "bad\u009b31m" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validHostAppManifest()
			tt.mutate(&manifest)
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("expected bounded/control-free rejection")
			}
		})
	}
}

func TestLoadManifestDoesNotExecuteOrResolvePackageFiles(t *testing.T) {
	dir := t.TempDir()
	manifest := validHostAppManifest()
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), marshalManifest(t, manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("exit 99"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadManifest(filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatalf("ordinary inert package file should not execute or change manifest validation: %v", err)
	}
	if got.ID != manifest.ID {
		t.Fatalf("unexpected manifest: %+v", got)
	}
}

func validHostAppManifest() Manifest {
	return Manifest{
		SchemaVersion: ManifestVersion,
		ID:            "community.cursor",
		Version:       "1.0.0",
		Description:   "Cursor host application projection",
		InstallHint:   &InstallHint{Text: "Install Cursor from its official site", URL: "https://cursor.com"},
		Apps: []AppSpec{{
			ID:                     "cursor",
			Platforms:              []string{PlatformDarwin},
			BundleNames:            []string{"Cursor.app"},
			ExecutableRelativePath: "Contents/MacOS/Cursor",
			ExpectedBundleID:       "com.todesktop.230313mzl4w4u92",
			ExpectedTeamID:         "VDXQ22DGB9",
			RequestedSafetyProfile: "vscode-family-v1",
			Launch: LaunchSpec{
				GotoFlag:        "--goto",
				NewWindowFlag:   "--new-window",
				ReuseWindowFlag: "--reuse-window",
				GotoSeparator:   ":",
			},
		}},
		Bindings: []BindingSpec{{
			ID:              "cursor-command",
			Commands:        []string{"cursor"},
			AppID:           "cursor",
			CapabilityID:    CapabilityOpenResource,
			ResourceKinds:   []string{ResourceWorkspace, ResourceHostFSPortal},
			ResultPolicy:    ResultNone,
			RequestedAccess: AccessAskEachRun,
			Grammar: GrammarSpec{
				Kind:             GrammarOpenResourceV1,
				ResourceCount:    1,
				GotoFlags:        []string{"-g", "--goto"},
				NewWindowFlags:   []string{"-n", "--new-window"},
				ReuseWindowFlags: []string{"-r", "--reuse-window"},
				UnknownFlags:     UnknownFlagsDeny,
			},
		}},
		Tests: []TestVector{{
			ID:        "opens-file",
			BindingID: "cursor-command",
			Argv:      []string{"cursor", "src/main.go"},
			Expected: TestExpectation{
				Resource:   "/workspace/src/main.go",
				WindowMode: "reuse",
			},
		}},
	}
}

func marshalManifest(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustCanonical(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	raw, err := CanonicalManifestBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func twoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
