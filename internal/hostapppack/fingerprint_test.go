package hostapppack

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestPermissionFingerprintChangesForEveryAuthorityBearingMutation(t *testing.T) {
	base := validHostAppManifest()
	baseFingerprint, err := PermissionFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"pack-id", func(m *Manifest) { m.ID = "community.cursor-alt" }},
		{"app-id", func(m *Manifest) { m.Apps[0].ID = "cursor-alt"; m.Bindings[0].AppID = "cursor-alt" }},
		{"platform", func(m *Manifest) { m.Apps[0].Platforms = append(m.Apps[0].Platforms, "darwin-preview") }},
		{"bundle-name", func(m *Manifest) { m.Apps[0].BundleNames = []string{"Cursor Beta.app"} }},
		{"executable-path", func(m *Manifest) { m.Apps[0].ExecutableRelativePath = "Contents/MacOS/CursorBeta" }},
		{"bundle-id", func(m *Manifest) { m.Apps[0].ExpectedBundleID = "com.example.cursor" }},
		{"team-id", func(m *Manifest) { m.Apps[0].ExpectedTeamID = "AAAAAAAAAA" }},
		{"safety-profile", func(m *Manifest) { m.Apps[0].RequestedSafetyProfile = "vscode-family-v2" }},
		{"goto-launch", func(m *Manifest) { m.Apps[0].Launch.GotoFlag = "--open-at" }},
		{"new-launch", func(m *Manifest) { m.Apps[0].Launch.NewWindowFlag = "--fresh-window" }},
		{"reuse-launch", func(m *Manifest) { m.Apps[0].Launch.ReuseWindowFlag = "--same-window" }},
		{"binding-id", func(m *Manifest) { m.Bindings[0].ID = "cursor-alt"; m.Tests[0].BindingID = "cursor-alt" }},
		{"command", func(m *Manifest) { m.Bindings[0].Commands = []string{"cursor-beta"} }},
		{"binding-app", func(m *Manifest) {
			app := m.Apps[0]
			app.ID = "cursor-two"
			m.Apps = append(m.Apps, app)
			m.Bindings[0].AppID = "cursor-two"
		}},
		{"resource-kinds", func(m *Manifest) { m.Bindings[0].ResourceKinds = []string{ResourceWorkspace} }},
		{"access", func(m *Manifest) { m.Bindings[0].RequestedAccess = AccessSafe }},
		{"grammar-goto", func(m *Manifest) { m.Bindings[0].Grammar.GotoFlags = []string{"--open-at"} }},
		{"grammar-new", func(m *Manifest) { m.Bindings[0].Grammar.NewWindowFlags = []string{"--fresh-window"} }},
		{"grammar-reuse", func(m *Manifest) { m.Bindings[0].Grammar.ReuseWindowFlags = []string{"--same-window"} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := cloneManifest(t, base)
			mutation.mutate(&candidate)
			got, err := PermissionFingerprint(candidate)
			if err != nil {
				// Platform alternatives are intentionally closed in v1. The raw
				// item still must exist so future allowlist expansion cannot omit it.
				if mutation.name == "platform" {
					items := PermissionItems(candidate)
					if !hasPermissionValue(items, "apps/cursor/platforms", "darwin-preview") {
						t.Fatalf("platform authority missing from items: %v", items)
					}
					return
				}
				t.Fatalf("fingerprint mutated manifest: %v", err)
			}
			if got == baseFingerprint {
				t.Fatalf("authority mutation did not change fingerprint: %s", got)
			}
		})
	}
}

func TestPermissionFingerprintIncludesClosedAndReturnFields(t *testing.T) {
	items := PermissionItems(validHostAppManifest())
	for key, value := range map[string]string{
		"bindings/cursor-command/capability":       CapabilityOpenResource,
		"bindings/cursor-command/result-policy":    ResultNone,
		"bindings/cursor-command/host-data-return": "false",
		"bindings/cursor-command/grammar/kind":     GrammarOpenResourceV1,
		"bindings/cursor-command/grammar/count":    "1",
		"bindings/cursor-command/grammar/unknown":  UnknownFlagsDeny,
		"apps/cursor/launch/goto-separator":        ":",
	} {
		if !hasPermissionValue(items, key, value) {
			t.Fatalf("permission item %s=%s missing from %v", key, value, items)
		}
	}
}

func TestPermissionFingerprintIgnoresDocsVersionAndTestsOnlyChanges(t *testing.T) {
	base := validHostAppManifest()
	want, err := PermissionFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*Manifest){
		func(m *Manifest) { m.Version = "9.9.9" },
		func(m *Manifest) { m.Description = "Different untrusted prose" },
		func(m *Manifest) {
			m.InstallHint = &InstallHint{Text: "Different hint", URL: "https://example.invalid"}
		},
		func(m *Manifest) { m.Tests[0].Argv = []string{"cursor", "README.md"} },
		func(m *Manifest) { m.Tests = nil },
	}
	for i, mutate := range mutations {
		candidate := cloneManifest(t, base)
		mutate(&candidate)
		got, err := PermissionFingerprint(candidate)
		if err != nil {
			t.Fatalf("mutation %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("docs/tests mutation %d changed permission fingerprint: %s != %s", i, got, want)
		}
	}
}

func TestEffectivePermissionFingerprintBindsCoreSafetyProfileVersion(t *testing.T) {
	manifest := validHostAppManifest()
	base, err := BasePermissionFingerprint(manifest)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := EffectivePermissionFingerprint(manifest, EffectivePermissionContext{
		Access: AccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := EffectivePermissionFingerprint(manifest, EffectivePermissionContext{
		Access: AccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if base == v1 || v1 == v2 {
		t.Fatalf("effective fingerprint omitted Core safety version: base=%s v1=%s v2=%s", base, v1, v2)
	}
	for label, digest := range map[string]string{"base": base, "effective-v1": v1, "effective-v2": v2} {
		if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
			t.Fatalf("%s fingerprint has non-canonical format %q", label, digest)
		}
	}
	diff, err := DiffEffectivePermissions(
		manifest,
		EffectivePermissionContext{Access: AccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1"},
		manifest,
		EffectivePermissionContext{Access: AccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasChangeKey(diff.Changed, "core/safety-profile/version") {
		t.Fatalf("Core safety-profile version absent from permission diff: %+v", diff)
	}
	if _, err := EffectivePermissionFingerprint(manifest, EffectivePermissionContext{Access: AccessSafe, SafetyProfileID: "vscode-family-v1"}); err == nil {
		t.Fatal("safe effective fingerprint accepted a missing Core safety version")
	}
	if _, err := EffectivePermissionFingerprint(manifest, EffectivePermissionContext{
		Access: AccessAskEachRun, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
	}); err == nil {
		t.Fatal("ask-each-run fingerprint claimed a selected Core safety profile")
	}
}

func TestEffectivePermissionFingerprintBindsEnabledBindingsAndCommandReplacement(t *testing.T) {
	manifest := validHostAppManifest()
	second := manifest.Bindings[0]
	second.ID = "cursor-preview-command"
	second.Commands = []string{"cursor-preview"}
	manifest.Bindings = append(manifest.Bindings, second)

	base := EffectivePermissionContext{
		Access: AccessAskEachRun, BindingIDs: []string{"cursor-command"}, ConflictReplacements: map[string]string{},
	}
	firstOnly, err := EffectivePermissionFingerprint(manifest, base)
	if err != nil {
		t.Fatal(err)
	}
	allBindings := base
	allBindings.BindingIDs = []string{"cursor-command", "cursor-preview-command"}
	all, err := EffectivePermissionFingerprint(manifest, allBindings)
	if err != nil {
		t.Fatal(err)
	}
	replaced := base
	replaced.ConflictReplacements = map[string]string{
		"cursor": "community.previous/rev_previous/cursor-command",
	}
	withReplacement, err := EffectivePermissionFingerprint(manifest, replaced)
	if err != nil {
		t.Fatal(err)
	}
	if firstOnly == all || firstOnly == withReplacement || all == withReplacement {
		t.Fatalf("effective authority collapsed to one fingerprint: first=%s all=%s replacement=%s", firstOnly, all, withReplacement)
	}
	diff, err := DiffEffectivePermissions(manifest, base, manifest, allBindings)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPermissionValue(diff.Added, "core/enabled-binding", "cursor-preview-command") {
		t.Fatalf("enabled binding absent from permission diff: %+v", diff)
	}
	diff, err = DiffEffectivePermissions(manifest, base, manifest, replaced)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPermissionValue(diff.Added, "core/command-replacement/cursor", "community.previous/rev_previous/cursor-command") {
		t.Fatalf("command replacement absent from permission diff: %+v", diff)
	}

	invalid := base
	invalid.ConflictReplacements = map[string]string{"cursor-preview": "prior-owner"}
	if _, err := EffectivePermissionFingerprint(manifest, invalid); err == nil {
		t.Fatal("replacement outside the enabled binding set was accepted")
	}
}

func TestPermissionFingerprintCanonicalizesSetOrdering(t *testing.T) {
	base := validHostAppManifest()
	base.Apps = append(base.Apps, AppSpec{
		ID:                     "zed",
		Platforms:              []string{PlatformDarwin},
		BundleNames:            []string{"Zed Preview.app", "Zed.app"},
		ExecutableRelativePath: "Contents/MacOS/zed",
		Launch:                 LaunchSpec{GotoFlag: "--goto", GotoSeparator: ":"},
	})
	base.Bindings = append(base.Bindings, BindingSpec{
		ID: "zed-command", Commands: []string{"zeditor", "zed"}, AppID: "zed",
		CapabilityID: CapabilityOpenResource, ResourceKinds: []string{ResourceWorkspace},
		ResultPolicy: ResultNone, RequestedAccess: AccessAskEachRun,
		Grammar: GrammarSpec{Kind: GrammarOpenResourceV1, ResourceCount: 1, UnknownFlags: UnknownFlagsDeny},
	})
	want, err := PermissionFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	reordered := cloneManifest(t, base)
	slices.Reverse(reordered.Apps)
	slices.Reverse(reordered.Bindings)
	slices.Reverse(reordered.Apps[1].BundleNames)
	slices.Reverse(reordered.Bindings[1].Commands)
	slices.Reverse(reordered.Bindings[1].ResourceKinds)
	got, err := PermissionFingerprint(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("set ordering changed fingerprint: %s != %s", got, want)
	}
}

func TestPermissionDiffReportsAddedRemovedAndChangedItems(t *testing.T) {
	before := validHostAppManifest()
	after := cloneManifest(t, before)
	after.Apps[0].BundleNames = append(after.Apps[0].BundleNames, "Cursor Beta.app")
	after.Bindings[0].Commands = []string{"cursor-next"}
	after.Bindings[0].RequestedAccess = AccessSafe
	diff := DiffPermissions(before, after)
	if len(diff.Added) == 0 || len(diff.Removed) == 0 || len(diff.Changed) == 0 {
		t.Fatalf("incomplete permission diff: %+v", diff)
	}
	if !strings.Contains(diff.Changed[0].Key, "access") && !hasChangeKey(diff.Changed, "bindings/cursor-command/access") {
		t.Fatalf("access change missing: %+v", diff)
	}
}

func TestPermissionDiffIsBoundedAndReportsTruncation(t *testing.T) {
	before := make([]PermissionItem, 200)
	after := make([]PermissionItem, 200)
	for i := range before {
		before[i] = PermissionItem{Key: "key", Value: "before-" + twoDigits(i%100)}
		after[i] = PermissionItem{Key: "key", Value: "after-" + twoDigits(i%100)}
	}
	diff := diffPermissionItems(before, after)
	visible := len(diff.Added) + len(diff.Removed) + len(diff.Changed)
	if visible > MaxPermissionDiffEntries || !diff.Truncated || diff.TotalChanges <= visible {
		t.Fatalf("permission diff was not bounded honestly: %+v", diff)
	}
}

func cloneManifest(t *testing.T, manifest Manifest) Manifest {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var clone Manifest
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func hasPermissionValue(items []PermissionItem, key, value string) bool {
	return slices.ContainsFunc(items, func(item PermissionItem) bool { return item.Key == key && item.Value == value })
}

func hasChangeKey(changes []PermissionChange, key string) bool {
	return slices.ContainsFunc(changes, func(change PermissionChange) bool { return change.Key == key })
}
