package hostapppack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	slashpath "path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxManifestBytes = 1 << 20

func LoadManifest(path string) (Manifest, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := DecodeManifest(data)
	if err != nil {
		return Manifest{}, nil, err
	}
	return manifest, data, nil
}

func DecodeManifest(data []byte) (Manifest, error) {
	if len(data) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("host-app manifest exceeds %d bytes", maxManifestBytes)
	}
	if err := validateManifestRequiredShape(data); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("host-app manifest must contain one JSON value")
		}
		return Manifest{}, fmt.Errorf("host-app manifest has malformed trailing data: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return NormalizeManifest(manifest), nil
}

func CanonicalManifestBytes(manifest Manifest) ([]byte, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return json.Marshal(NormalizeManifest(manifest))
}

func ValidateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestVersion {
		return fmt.Errorf("unsupported host-app pack schema %q", manifest.SchemaVersion)
	}
	if err := validatePackID(manifest.ID); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if strings.HasPrefix(manifest.ID, "builtin.") {
		return errors.New("id: builtin namespace is Core-owned")
	}
	if err := validateText("version", manifest.Version, 1, MaxVersionBytes); err != nil {
		return err
	}
	if err := validateText("description", manifest.Description, 1, MaxDescriptionBytes); err != nil {
		return err
	}
	if err := validateInstallHint(manifest.InstallHint); err != nil {
		return err
	}
	if len(manifest.Apps) == 0 || len(manifest.Apps) > MaxApps {
		return fmt.Errorf("apps must contain 1-%d entries", MaxApps)
	}
	if len(manifest.Bindings) == 0 || len(manifest.Bindings) > MaxBindings {
		return fmt.Errorf("bindings must contain 1-%d entries", MaxBindings)
	}
	if len(manifest.Tests) > MaxTests {
		return fmt.Errorf("tests must contain at most %d entries", MaxTests)
	}
	appIDs := map[string]struct{}{}
	for i, app := range manifest.Apps {
		if err := validateApp(fmt.Sprintf("apps[%d]", i), app); err != nil {
			return err
		}
		if _, exists := appIDs[app.ID]; exists {
			return fmt.Errorf("apps[%d].id duplicates %q", i, app.ID)
		}
		appIDs[app.ID] = struct{}{}
	}
	bindingIDs := map[string]struct{}{}
	commands := map[string]struct{}{}
	for i, binding := range manifest.Bindings {
		label := fmt.Sprintf("bindings[%d]", i)
		if err := validateBinding(label, binding, appIDs); err != nil {
			return err
		}
		if _, exists := bindingIDs[binding.ID]; exists {
			return fmt.Errorf("%s.id duplicates %q", label, binding.ID)
		}
		bindingIDs[binding.ID] = struct{}{}
		for _, command := range binding.Commands {
			if _, exists := commands[command]; exists {
				return fmt.Errorf("%s.commands contains pack-wide duplicate %q", label, command)
			}
			commands[command] = struct{}{}
		}
	}
	if len(commands) > MaxCommandsPerProfile {
		return fmt.Errorf("pack declares %d commands, exceeding profile limit %d", len(commands), MaxCommandsPerProfile)
	}
	testIDs := map[string]struct{}{}
	for i, vector := range manifest.Tests {
		label := fmt.Sprintf("tests[%d]", i)
		if err := validateTestVector(label, vector, bindingIDs); err != nil {
			return err
		}
		if _, exists := testIDs[vector.ID]; exists {
			return fmt.Errorf("%s.id duplicates %q", label, vector.ID)
		}
		testIDs[vector.ID] = struct{}{}
	}
	return nil
}

func NormalizeManifest(manifest Manifest) Manifest {
	normalized := manifest
	normalized.Apps = append([]AppSpec(nil), manifest.Apps...)
	for i := range normalized.Apps {
		normalized.Apps[i].Platforms = sortedCopy(normalized.Apps[i].Platforms)
		normalized.Apps[i].BundleNames = sortedCopy(normalized.Apps[i].BundleNames)
	}
	sort.Slice(normalized.Apps, func(i, j int) bool { return normalized.Apps[i].ID < normalized.Apps[j].ID })
	normalized.Bindings = append([]BindingSpec(nil), manifest.Bindings...)
	for i := range normalized.Bindings {
		binding := &normalized.Bindings[i]
		binding.Commands = sortedCopy(binding.Commands)
		binding.ResourceKinds = sortedCopy(binding.ResourceKinds)
		binding.Grammar.GotoFlags = sortedCopy(binding.Grammar.GotoFlags)
		binding.Grammar.NewWindowFlags = sortedCopy(binding.Grammar.NewWindowFlags)
		binding.Grammar.ReuseWindowFlags = sortedCopy(binding.Grammar.ReuseWindowFlags)
	}
	sort.Slice(normalized.Bindings, func(i, j int) bool { return normalized.Bindings[i].ID < normalized.Bindings[j].ID })
	normalized.Tests = append([]TestVector(nil), manifest.Tests...)
	if normalized.Tests == nil {
		normalized.Tests = []TestVector{}
	}
	for i := range normalized.Tests {
		normalized.Tests[i].Argv = append([]string(nil), normalized.Tests[i].Argv...)
	}
	sort.Slice(normalized.Tests, func(i, j int) bool { return normalized.Tests[i].ID < normalized.Tests[j].ID })
	return normalized
}

func validateApp(label string, app AppSpec) error {
	if err := validateLocalID(app.ID); err != nil {
		return fmt.Errorf("%s.id: %w", label, err)
	}
	if len(app.Platforms) != 1 || app.Platforms[0] != PlatformDarwin {
		return fmt.Errorf("%s.platforms must contain only %q in v1", label, PlatformDarwin)
	}
	if len(app.BundleNames) == 0 || len(app.BundleNames) > MaxBundleNames {
		return fmt.Errorf("%s.bundleNames must contain 1-%d entries", label, MaxBundleNames)
	}
	if err := validateUniqueStrings(label+".bundleNames", app.BundleNames, validateBundleName); err != nil {
		return err
	}
	if err := validateRelativePath(label+".executableRelativePath", app.ExecutableRelativePath, MaxExecutableBytes); err != nil {
		return err
	}
	if err := validateOptionalText(label+".expectedBundleId", app.ExpectedBundleID, MaxVersionBytes); err != nil {
		return err
	}
	if err := validateOptionalText(label+".expectedTeamId", app.ExpectedTeamID, MaxVersionBytes); err != nil {
		return err
	}
	if app.RequestedSafetyProfile != "" {
		if err := validateLocalID(app.RequestedSafetyProfile); err != nil {
			return fmt.Errorf("%s.requestedSafetyProfile: %w", label, err)
		}
	}
	return validateLaunch(label+".launch", app.Launch)
}

func validateBinding(label string, binding BindingSpec, apps map[string]struct{}) error {
	if err := validateLocalID(binding.ID); err != nil {
		return fmt.Errorf("%s.id: %w", label, err)
	}
	if _, exists := apps[binding.AppID]; !exists {
		return fmt.Errorf("%s.appId %q does not name an app", label, binding.AppID)
	}
	if len(binding.Commands) == 0 || len(binding.Commands) > MaxCommandsPerBinding {
		return fmt.Errorf("%s.commands must contain 1-%d entries", label, MaxCommandsPerBinding)
	}
	if err := validateUniqueStrings(label+".commands", binding.Commands, validateCommand); err != nil {
		return err
	}
	if binding.CapabilityID != CapabilityOpenResource {
		return fmt.Errorf("%s.capabilityId %q is unsupported", label, binding.CapabilityID)
	}
	if len(binding.ResourceKinds) == 0 || len(binding.ResourceKinds) > 2 {
		return fmt.Errorf("%s.resourceKinds must contain 1-2 entries", label)
	}
	if err := validateUniqueStrings(label+".resourceKinds", binding.ResourceKinds, func(value string) error {
		switch value {
		case ResourceWorkspace, ResourceHostFSPortal:
			return nil
		default:
			return fmt.Errorf("resource kind %q is unsupported", value)
		}
	}); err != nil {
		return err
	}
	if binding.ResultPolicy != ResultNone {
		return fmt.Errorf("%s.resultPolicy %q is unsupported", label, binding.ResultPolicy)
	}
	switch binding.RequestedAccess {
	case AccessSafe, AccessAskEachRun:
	default:
		return fmt.Errorf("%s.requestedAccess %q is unsupported", label, binding.RequestedAccess)
	}
	return validateGrammar(label+".grammar", binding.Grammar)
}

func validateGrammar(label string, grammar GrammarSpec) error {
	if grammar.Kind != GrammarOpenResourceV1 {
		return fmt.Errorf("%s.kind %q is unsupported", label, grammar.Kind)
	}
	if grammar.ResourceCount != 1 {
		return fmt.Errorf("%s.resourceCount must be 1", label)
	}
	if grammar.UnknownFlags != UnknownFlagsDeny {
		return fmt.Errorf("%s.unknownFlags must be %q", label, UnknownFlagsDeny)
	}
	seen := map[string]string{}
	for group, flags := range map[string][]string{
		"gotoFlags": grammar.GotoFlags, "newWindowFlags": grammar.NewWindowFlags, "reuseWindowFlags": grammar.ReuseWindowFlags,
	} {
		if len(flags) > MaxGrammarFlags {
			return fmt.Errorf("%s.%s exceeds %d entries", label, group, MaxGrammarFlags)
		}
		for i, flag := range flags {
			if err := validateFlag(flag); err != nil {
				return fmt.Errorf("%s.%s[%d]: %w", label, group, i, err)
			}
			if prior, exists := seen[flag]; exists {
				return fmt.Errorf("%s.%s[%d] duplicates flag %q from %s", label, group, i, flag, prior)
			}
			seen[flag] = group
		}
	}
	return nil
}

func validateLaunch(label string, launch LaunchSpec) error {
	for field, value := range map[string]string{
		"gotoFlag": launch.GotoFlag, "newWindowFlag": launch.NewWindowFlag, "reuseWindowFlag": launch.ReuseWindowFlag,
	} {
		if value != "" {
			if err := validateFlag(value); err != nil {
				return fmt.Errorf("%s.%s: %w", label, field, err)
			}
		}
	}
	if launch.GotoSeparator != "" {
		if launch.GotoSeparator != ":" {
			return fmt.Errorf("%s.gotoSeparator must be colon", label)
		}
	}
	return nil
}

func validateTestVector(label string, vector TestVector, bindings map[string]struct{}) error {
	if err := validateLocalID(vector.ID); err != nil {
		return fmt.Errorf("%s.id: %w", label, err)
	}
	if _, exists := bindings[vector.BindingID]; !exists {
		return fmt.Errorf("%s.bindingId %q does not name a binding", label, vector.BindingID)
	}
	if len(vector.Argv) == 0 || len(vector.Argv) > MaxArgv {
		return fmt.Errorf("%s.argv must contain 1-%d entries", label, MaxArgv)
	}
	for i, arg := range vector.Argv {
		if !utf8.ValidString(arg) || len(arg) > MaxArgBytes || !safePrintable(arg) {
			return fmt.Errorf("%s.argv[%d] must be at most %d printable bytes", label, i, MaxArgBytes)
		}
	}
	if len(vector.Expected.Resource) == 0 || len(vector.Expected.Resource) > MaxPathBytes || !safePrintable(vector.Expected.Resource) {
		return fmt.Errorf("%s.expected.resource must be a bounded resource", label)
	}
	if vector.Expected.Line < 0 || vector.Expected.Column < 0 {
		return fmt.Errorf("%s.expected line and column must be positive when set", label)
	}
	switch vector.Expected.WindowMode {
	case "reuse", "new":
	default:
		return fmt.Errorf("%s.expected.windowMode %q is unsupported", label, vector.Expected.WindowMode)
	}
	return nil
}

func validateInstallHint(hint *InstallHint) error {
	if hint == nil {
		return nil
	}
	if err := validateText("installHint.text", hint.Text, 1, MaxHintBytes); err != nil {
		return err
	}
	if hint.URL == "" {
		return nil
	}
	if err := validateOptionalText("installHint.url", hint.URL, MaxURLBytes); err != nil {
		return err
	}
	parsed, err := url.Parse(hint.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("installHint.url must be an https URL without credentials")
	}
	return nil
}

func validatePackID(value string) error {
	if err := validateSlug(value, MaxPackIDBytes); err != nil {
		return err
	}
	if !strings.ContainsAny(value, ".-") {
		return errors.New("pack id must contain a namespace separator")
	}
	return nil
}

func validateLocalID(value string) error {
	return validateSlug(value, MaxSlugBytes)
}

func validateSlug(value string, max int) error {
	if len(value) == 0 || len(value) > max || strings.TrimSpace(value) != value || value[0] < 'a' || value[0] > 'z' {
		return fmt.Errorf("identifier must contain 1-%d normalized bytes and start with a letter", max)
	}
	previousSeparator := false
	for _, r := range value {
		separator := r == '.' || r == '-'
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || separator {
			if separator && previousSeparator {
				return fmt.Errorf("identifier %q contains adjacent separators", value)
			}
			previousSeparator = separator
			continue
		}
		return fmt.Errorf("identifier %q contains unsupported characters", value)
	}
	if previousSeparator {
		return fmt.Errorf("identifier %q ends with a separator", value)
	}
	return nil
}

func validateBundleName(value string) error {
	if err := validateText("bundle name", value, 5, MaxBundleNameBytes); err != nil {
		return err
	}
	if value != filepath.Base(value) || strings.ContainsAny(value, `/\$`) || !strings.HasSuffix(value, ".app") {
		return fmt.Errorf("bundle name %q must be an .app basename", value)
	}
	return nil
}

func validateRelativePath(label, value string, max int) error {
	if err := validateText(label, value, 1, max); err != nil {
		return err
	}
	if strings.Contains(value, `\`) || filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return fmt.Errorf("%s must be a relative slash-separated path", label)
	}
	clean := slashpath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return fmt.Errorf("%s must be normalized and stay inside the bundle", label)
	}
	return nil
}

func validateCommand(value string) error {
	if len(value) == 0 || len(value) > MaxSlugBytes || strings.TrimSpace(value) != value || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("command %q must be a bounded simple name", value)
	}
	for i, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && strings.ContainsRune("._+-", r)) {
			continue
		}
		return fmt.Errorf("command %q contains unsupported characters", value)
	}
	return nil
}

func validateFlag(value string) error {
	if len(value) < 2 || len(value) > MaxFlagBytes || !strings.HasPrefix(value, "-") || !safePrintable(value) {
		return fmt.Errorf("flag %q must be one bounded flag token", value)
	}
	body := strings.TrimPrefix(value[1:], "-")
	if body == "" {
		return fmt.Errorf("flag %q must name a flag", value)
	}
	for i, r := range body {
		alphanumeric := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if alphanumeric || (i > 0 && r == '-') {
			continue
		}
		return fmt.Errorf("flag %q contains unsupported characters", value)
	}
	return nil
}

func validateOptionalText(label, value string, max int) error {
	if value == "" {
		return nil
	}
	return validateText(label, value, 1, max)
}

func validateText(label, value string, min, max int) error {
	if !utf8.ValidString(value) || len(value) < min || len(value) > max || strings.TrimSpace(value) != value || !safePrintable(value) {
		return fmt.Errorf("%s must contain %d-%d normalized printable bytes", label, min, max)
	}
	return nil
}

func safePrintable(value string) bool {
	for _, r := range value {
		if !unicode.IsPrint(r) || unicode.IsControl(r) || r == '\u001b' || r == '\u009b' {
			return false
		}
	}
	return true
}

func validateUniqueStrings(label string, values []string, validate func(string) error) error {
	seen := map[string]struct{}{}
	for i, value := range values {
		if err := validate(value); err != nil {
			return fmt.Errorf("%s[%d]: %w", label, i, err)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s[%d] duplicates %q", label, i, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	if out == nil {
		out = []string{}
	}
	sort.Strings(out)
	return out
}

func validateManifestRequiredShape(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	if err := requireJSONKeys("manifest", top, "schemaVersion", "id", "version", "description", "apps", "bindings", "tests"); err != nil {
		return err
	}
	var apps []map[string]json.RawMessage
	if err := json.Unmarshal(top["apps"], &apps); err != nil {
		return err
	}
	for i, app := range apps {
		if err := requireJSONKeys(fmt.Sprintf("apps[%d]", i), app, "id", "platforms", "bundleNames", "executableRelativePath", "launch"); err != nil {
			return err
		}
	}
	var bindings []map[string]json.RawMessage
	if err := json.Unmarshal(top["bindings"], &bindings); err != nil {
		return err
	}
	for i, binding := range bindings {
		if err := requireJSONKeys(fmt.Sprintf("bindings[%d]", i), binding, "id", "commands", "appId", "capabilityId", "resourceKinds", "resultPolicy", "requestedAccess", "grammar"); err != nil {
			return err
		}
		var grammar map[string]json.RawMessage
		if err := json.Unmarshal(binding["grammar"], &grammar); err != nil {
			return err
		}
		if err := requireJSONKeys(fmt.Sprintf("bindings[%d].grammar", i), grammar, "kind", "resourceCount", "gotoFlags", "newWindowFlags", "reuseWindowFlags", "unknownFlags"); err != nil {
			return err
		}
	}
	var tests []map[string]json.RawMessage
	if err := json.Unmarshal(top["tests"], &tests); err != nil {
		return err
	}
	for i, vector := range tests {
		if err := requireJSONKeys(fmt.Sprintf("tests[%d]", i), vector, "id", "bindingId", "argv", "expected"); err != nil {
			return err
		}
		var expected map[string]json.RawMessage
		if err := json.Unmarshal(vector["expected"], &expected); err != nil {
			return err
		}
		if err := requireJSONKeys(fmt.Sprintf("tests[%d].expected", i), expected, "resource", "windowMode"); err != nil {
			return err
		}
	}
	if raw, exists := top["installHint"]; exists {
		var hint map[string]json.RawMessage
		if err := json.Unmarshal(raw, &hint); err != nil {
			return err
		}
		if err := requireJSONKeys("installHint", hint, "text"); err != nil {
			return err
		}
	}
	return nil
}

func requireJSONKeys(label string, object map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return fmt.Errorf("%s.%s is required", label, key)
		}
	}
	return nil
}
