package appopen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

const (
	IsolatedStateQualifiedAppRun    = "qualified-app-run-directory"
	VerificationCombinedEffectV1    = "combined-effect-v1"
	ExecutableIdentityExactObserved = "exact-observed-binding"
)

var safetyID = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)

const (
	maxSafetyCatalogBytes  = 1 << 20
	maxSafetyProfiles      = 32
	maxSafetyMatchers      = 16
	maxSafetyListItems     = 64
	maxSafetyTextBytes     = 256
	maxSafetySettingsBytes = 64 << 10
)

type SafetyIdentity struct {
	Signed                 bool
	Platform               string
	BundleID               string
	TeamID                 string
	CodeIdentity           string
	ExecutableRelativePath string
	ExecutableCodeIdentity string
}

type SafetyIdentityMatcher struct {
	Platform                 string `json:"platform"`
	BundleID                 string `json:"bundleId"`
	TeamID                   string `json:"teamId"`
	CodeIdentity             string `json:"codeIdentity,omitempty"`
	ExecutableRelativePath   string `json:"executableRelativePath"`
	ExecutableIdentityPolicy string `json:"executableIdentityPolicy"`
}

type IsolatedStateSpec struct {
	Kind                 string `json:"kind"`
	ArgumentFlag         string `json:"argumentFlag"`
	SettingsRelativePath string `json:"settingsRelativePath"`
}

// LaunchSyntaxProfile is the exact package-declared syntax a reviewed app
// family may use while retaining the safe label. It is Core-owned data.
type LaunchSyntaxProfile struct {
	AllowedGotoFlags        []string `json:"allowedGotoFlags"`
	AllowedNewWindowFlags   []string `json:"allowedNewWindowFlags"`
	AllowedReuseWindowFlags []string `json:"allowedReuseWindowFlags"`
	AllowedGotoSeparators   []string `json:"allowedGotoSeparators"`
	AllowPositionalTarget   bool     `json:"allowPositionalTarget"`
}

// SafetyProfile is reviewed Core data. A community package may request ID but
// cannot supply or mutate any field in this structure.
type SafetyProfile struct {
	ID                string                  `json:"id"`
	Version           string                  `json:"version"`
	IdentityMatchers  []SafetyIdentityMatcher `json:"identityMatchers"`
	RequiredArgv      []string                `json:"requiredArgv"`
	ForbiddenArgv     []string                `json:"forbiddenArgv"`
	LaunchSyntax      LaunchSyntaxProfile     `json:"launchSyntax"`
	IsolatedState     IsolatedStateSpec       `json:"isolatedState"`
	AllowedSettings   []string                `json:"allowedSettings,omitempty"`
	RequiredSettings  map[string]any          `json:"requiredSettings"`
	ForbiddenSettings map[string]any          `json:"forbiddenSettings"`
	Verification      []string                `json:"verification"`
}

type SafeEffect struct {
	ProfileID              string
	ProfileVersion         string
	Argv                   []string
	Settings               map[string]any
	StateBase              string
	StateRoot              string
	SettingsRelativePath   string
	LaunchSpec             LaunchSpec
	LaunchRequest          OpenRequest
	ExecutableCodeIdentity string
}

type safetyProfileFile struct {
	SchemaVersion string          `json:"schemaVersion"`
	Profiles      []SafetyProfile `json:"profiles"`
}

// DecodeSafetyProfileCatalog strictly loads reviewed Core package data. It is
// intentionally not a registration API and accepts no community manifest.
func DecodeSafetyProfileCatalog(raw []byte) ([]SafetyProfile, error) {
	if len(raw) == 0 || len(raw) > maxSafetyCatalogBytes {
		return nil, errors.New("appopen: Core safety profile catalog exceeds its bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file safetyProfileFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("appopen: decode Core safety profiles: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("appopen: Core safety profile catalog must be one JSON document")
	}
	if file.SchemaVersion != "hideout.host-app-safety-profiles/v1" || len(file.Profiles) == 0 || len(file.Profiles) > maxSafetyProfiles {
		return nil, errors.New("appopen: unsupported or empty Core safety profile catalog")
	}
	seen := map[string]bool{}
	for _, profile := range file.Profiles {
		if seen[profile.ID] {
			return nil, errors.New("appopen: duplicate Core safety profile")
		}
		seen[profile.ID] = true
		if err := ValidateSafetyProfile(profile); err != nil {
			return nil, err
		}
	}
	return file.Profiles, nil
}

func SelectSafetyProfile(profiles []SafetyProfile, requestedID string, identity SafetyIdentity) (SafetyProfile, error) {
	for _, profile := range profiles {
		if profile.ID != requestedID {
			continue
		}
		if err := ValidateSafetyProfile(profile); err != nil {
			return SafetyProfile{}, err
		}
		if !safetyIdentityMatches(profile, identity) {
			return SafetyProfile{}, errors.New("appopen: requested safety profile is incompatible with the Core-observed identity")
		}
		return profile, nil
	}
	return SafetyProfile{}, errors.New("appopen: requested safety profile is not Core-owned")
}

// BuildSafeEffect renders launch arguments and settings from one compatible
// Core profile. Package-authored safe flags/settings are rejected rather than
// merged, so they cannot reproduce a forbidden effect through another channel.
func BuildSafeEffect(spec LaunchSpec, req OpenRequest, profile SafetyProfile, identity SafetyIdentity) (SafeEffect, error) {
	if req.Mode != ModeSafe {
		return SafeEffect{}, errors.New("appopen: a Core safety profile applies only to safe mode")
	}
	if err := ValidateSafetyProfile(profile); err != nil {
		return SafeEffect{}, err
	}
	if !safetyIdentityMatches(profile, identity) {
		return SafeEffect{}, errors.New("appopen: observed identity is not compatible with the safety profile")
	}
	if len(spec.SafeIsolationFlags) != 0 || spec.IsolatedDataDirFlag != "" || spec.SafeConfiguration != nil {
		return SafeEffect{}, errors.New("appopen: package-authored safe effects are not accepted")
	}
	if err := validateSafeLaunchSyntax(profile, spec, req); err != nil {
		return SafeEffect{}, err
	}
	stateRoot, err := QualifiedRunStateRoot(req.SafeUserDataDir, req.QualifiedAppRef, req.RunID)
	if err != nil {
		return SafeEffect{}, err
	}
	coreSpec := spec
	coreSpec.SafeIsolationFlags = append([]string(nil), profile.RequiredArgv...)
	coreSpec.IsolatedDataDirFlag = profile.IsolatedState.ArgumentFlag
	coreSpec.SafeConfiguration = nil
	coreRequest := req
	coreRequest.SafeUserDataDir = stateRoot
	argv, err := RenderArgv(coreSpec, coreRequest)
	if err != nil {
		return SafeEffect{}, err
	}
	settings, err := cloneSettings(profile.RequiredSettings)
	if err != nil {
		return SafeEffect{}, err
	}
	effect := SafeEffect{
		ProfileID:              profile.ID,
		ProfileVersion:         profile.Version,
		Argv:                   argv,
		Settings:               settings,
		StateBase:              filepath.Clean(req.SafeUserDataDir),
		StateRoot:              stateRoot,
		SettingsRelativePath:   filepath.FromSlash(profile.IsolatedState.SettingsRelativePath),
		LaunchSpec:             spec,
		LaunchRequest:          coreRequest,
		ExecutableCodeIdentity: identity.ExecutableCodeIdentity,
	}
	if err := ValidateSafetyEffect(profile, identity, effect); err != nil {
		return SafeEffect{}, err
	}
	return effect, nil
}

func ValidateSafetyEffect(profile SafetyProfile, identity SafetyIdentity, effect SafeEffect) error {
	if err := ValidateSafetyProfile(profile); err != nil {
		return err
	}
	if !safetyIdentityMatches(profile, identity) {
		return errors.New("appopen: safety identity changed or is incompatible")
	}
	if effect.ProfileID != profile.ID || effect.ProfileVersion != profile.Version {
		return errors.New("appopen: safety profile identity mismatch")
	}
	if effect.ExecutableCodeIdentity == "" || effect.ExecutableCodeIdentity != identity.ExecutableCodeIdentity {
		return errors.New("appopen: safe effect is not bound to the exact observed executable identity")
	}
	if err := validateSafeLaunchSyntax(profile, effect.LaunchSpec, effect.LaunchRequest); err != nil {
		return err
	}
	expectedStateRoot, err := QualifiedRunStateRoot(effect.StateBase, effect.LaunchRequest.QualifiedAppRef, effect.LaunchRequest.RunID)
	if err != nil || expectedStateRoot != effect.StateRoot {
		return errors.New("appopen: safe state does not match qualified app/run identity")
	}
	if effect.LaunchRequest.SafeUserDataDir != effect.StateRoot {
		return errors.New("appopen: safe launch request points at different state")
	}
	expectedSpec := effect.LaunchSpec
	expectedSpec.SafeIsolationFlags = append([]string(nil), profile.RequiredArgv...)
	expectedSpec.IsolatedDataDirFlag = profile.IsolatedState.ArgumentFlag
	expectedSpec.SafeConfiguration = nil
	expectedArgv, err := RenderArgv(expectedSpec, effect.LaunchRequest)
	if err != nil || !slices.Equal(expectedArgv, effect.Argv) {
		return errors.New("appopen: safe launch argv differs from the Core-built effect")
	}
	if !pathWithin(effect.StateBase, effect.StateRoot) || filepath.Clean(effect.StateBase) == filepath.Clean(effect.StateRoot) {
		return errors.New("appopen: safe state is not isolated beneath its Core root")
	}
	for _, required := range profile.RequiredArgv {
		if !argvHasFlag(effect.Argv, required) {
			return fmt.Errorf("appopen: required safety argument %q is missing", required)
		}
	}
	for _, forbidden := range profile.ForbiddenArgv {
		if argvHasFlag(effect.Argv, forbidden) {
			return fmt.Errorf("appopen: forbidden safety argument %q is present", forbidden)
		}
	}
	for _, forbidden := range hardForbiddenFlags {
		if argvHasFlag(effect.Argv, forbidden) {
			return fmt.Errorf("appopen: framework-forbidden argument %q is present", forbidden)
		}
	}
	if profile.IsolatedState.ArgumentFlag != "" && !argvHasPair(effect.Argv, profile.IsolatedState.ArgumentFlag, effect.StateRoot) {
		return errors.New("appopen: isolated state argument is missing or points elsewhere")
	}
	allowed := map[string]bool{}
	for _, key := range profile.AllowedSettings {
		allowed[key] = true
	}
	for key := range profile.RequiredSettings {
		allowed[key] = true
	}
	for key, actual := range effect.Settings {
		if !allowed[key] {
			return fmt.Errorf("appopen: setting %q is outside the Core safety profile", key)
		}
		if forbidden, ok := profile.ForbiddenSettings[key]; ok && reflect.DeepEqual(actual, forbidden) {
			return fmt.Errorf("appopen: setting %q has a forbidden safety effect", key)
		}
	}
	for key, required := range profile.RequiredSettings {
		actual, ok := effect.Settings[key]
		if !ok || !reflect.DeepEqual(actual, required) {
			return fmt.Errorf("appopen: required setting %q is missing or changed", key)
		}
	}
	return nil
}

func QualifiedRunStateRoot(base, qualifiedAppRef, runID string) (string, error) {
	base = filepath.Clean(base)
	if !filepath.IsAbs(base) {
		return "", errors.New("appopen: safe state base must be absolute")
	}
	if strings.TrimSpace(qualifiedAppRef) == "" || strings.TrimSpace(runID) == "" || strings.ContainsRune(qualifiedAppRef, '\x00') || strings.ContainsRune(runID, '\x00') {
		return "", errors.New("appopen: qualified app and run identity are required")
	}
	return filepath.Join(base, "apps", shortIdentity(qualifiedAppRef), "runs", shortIdentity(runID)), nil
}

func ValidateSafetyProfile(profile SafetyProfile) error {
	if !safetyID.MatchString(profile.ID) || len(profile.ID) > maxSafetyTextBytes || strings.TrimSpace(profile.Version) == "" || len(profile.Version) > maxSafetyTextBytes || hasControl(profile.Version) {
		return errors.New("appopen: invalid Core safety profile identity")
	}
	if len(profile.IdentityMatchers) == 0 || len(profile.IdentityMatchers) > maxSafetyMatchers {
		return errors.New("appopen: safety profile requires an observed identity matcher")
	}
	for _, matcher := range profile.IdentityMatchers {
		if matcher.Platform == "" || matcher.BundleID == "" || matcher.TeamID == "" || matcher.ExecutableRelativePath == "" || matcher.ExecutableIdentityPolicy != ExecutableIdentityExactObserved || boundedSafetyText(matcher.Platform) != nil || boundedSafetyText(matcher.BundleID) != nil || boundedSafetyText(matcher.TeamID) != nil || boundedSafetyText(matcher.CodeIdentity) != nil || boundedSafetyText(matcher.ExecutableRelativePath) != nil {
			return errors.New("appopen: safety identity matcher is incomplete")
		}
	}
	if len(profile.RequiredArgv) > maxSafetyListItems || len(profile.ForbiddenArgv) > maxSafetyListItems || len(profile.AllowedSettings) > maxSafetyListItems || len(profile.RequiredSettings) > maxSafetyListItems || len(profile.ForbiddenSettings) > maxSafetyListItems || len(profile.Verification) > maxSafetyListItems {
		return errors.New("appopen: safety profile exceeds a list bound")
	}
	if encoded, err := json.Marshal(struct {
		Required  map[string]any
		Forbidden map[string]any
	}{profile.RequiredSettings, profile.ForbiddenSettings}); err != nil || len(encoded) > maxSafetySettingsBytes {
		return errors.New("appopen: safety settings exceed their data bound")
	}
	if profile.IsolatedState.Kind != IsolatedStateQualifiedAppRun || strings.TrimSpace(profile.IsolatedState.ArgumentFlag) == "" {
		return errors.New("appopen: safety profile lacks qualified app/run isolation")
	}
	if boundedSafetyText(profile.IsolatedState.Kind) != nil || boundedSafetyText(profile.IsolatedState.SettingsRelativePath) != nil {
		return errors.New("appopen: isolated state declaration exceeds its bound")
	}
	if !validSafetyFlag(profile.IsolatedState.ArgumentFlag) || slices.Contains(profile.ForbiddenArgv, profile.IsolatedState.ArgumentFlag) {
		return errors.New("appopen: isolated state argument conflicts with the safety floor")
	}
	cleanSettingsPath := filepath.Clean(filepath.FromSlash(profile.IsolatedState.SettingsRelativePath))
	if cleanSettingsPath == "." || cleanSettingsPath == ".." || filepath.IsAbs(cleanSettingsPath) || strings.HasPrefix(cleanSettingsPath, ".."+string(filepath.Separator)) || filepath.ToSlash(cleanSettingsPath) != profile.IsolatedState.SettingsRelativePath {
		return errors.New("appopen: safety settings path must remain under the isolated state root")
	}
	if hasDuplicate(profile.RequiredArgv) || hasDuplicate(profile.ForbiddenArgv) || hasDuplicate(profile.AllowedSettings) {
		return errors.New("appopen: duplicate safety profile item")
	}
	if err := validateLaunchSyntaxProfile(profile.LaunchSyntax); err != nil {
		return err
	}
	for _, required := range profile.RequiredArgv {
		if !validSafetyFlag(required) || slices.Contains(profile.ForbiddenArgv, required) {
			return errors.New("appopen: safety argv floor is contradictory")
		}
	}
	for _, forbidden := range profile.ForbiddenArgv {
		if !validSafetyFlag(forbidden) {
			return errors.New("appopen: invalid forbidden safety argument")
		}
	}
	for key, required := range profile.RequiredSettings {
		if key == "" || boundedSafetyText(key) != nil {
			return errors.New("appopen: empty required setting key")
		}
		if forbidden, ok := profile.ForbiddenSettings[key]; ok && reflect.DeepEqual(required, forbidden) {
			return errors.New("appopen: safety settings floor is contradictory")
		}
	}
	for _, key := range profile.AllowedSettings {
		if key == "" || boundedSafetyText(key) != nil {
			return errors.New("appopen: invalid allowed setting key")
		}
	}
	for key := range profile.ForbiddenSettings {
		if key == "" || boundedSafetyText(key) != nil {
			return errors.New("appopen: invalid forbidden setting key")
		}
	}
	if !slices.Equal(profile.Verification, []string{VerificationCombinedEffectV1}) {
		return errors.New("appopen: unsupported safety verification contract")
	}
	return nil
}

func validateLaunchSyntaxProfile(syntax LaunchSyntaxProfile) error {
	groups := [][]string{syntax.AllowedGotoFlags, syntax.AllowedNewWindowFlags, syntax.AllowedReuseWindowFlags}
	for _, flags := range groups {
		if len(flags) > maxSafetyListItems || hasDuplicate(flags) {
			return errors.New("appopen: duplicate allowed launch flag")
		}
		for _, flag := range flags {
			if !validSafetyFlag(flag) {
				return errors.New("appopen: invalid allowed launch flag")
			}
		}
	}
	if len(syntax.AllowedGotoSeparators) > maxSafetyListItems || hasDuplicate(syntax.AllowedGotoSeparators) {
		return errors.New("appopen: duplicate allowed goto separator")
	}
	for _, separator := range syntax.AllowedGotoSeparators {
		if separator == "" || len(separator) > 4 || hasControl(separator) {
			return errors.New("appopen: invalid allowed goto separator")
		}
	}
	return nil
}

func validateSafeLaunchSyntax(profile SafetyProfile, spec LaunchSpec, req OpenRequest) error {
	if len(spec.SafeIsolationFlags) != 0 || spec.IsolatedDataDirFlag != "" || spec.SafeConfiguration != nil {
		return errors.New("appopen: package-authored safe effects are not accepted")
	}
	if spec.GotoFlag != "" && !slices.Contains(profile.LaunchSyntax.AllowedGotoFlags, spec.GotoFlag) {
		return errors.New("appopen: package goto flag is not allowed by the Core safety profile")
	}
	if spec.NewWindowFlag != "" && !slices.Contains(profile.LaunchSyntax.AllowedNewWindowFlags, spec.NewWindowFlag) {
		return errors.New("appopen: package new-window flag is not allowed by the Core safety profile")
	}
	if spec.ReuseWindowFlag != "" && !slices.Contains(profile.LaunchSyntax.AllowedReuseWindowFlags, spec.ReuseWindowFlag) {
		return errors.New("appopen: package reuse-window flag is not allowed by the Core safety profile")
	}
	if spec.GotoFlag != "" {
		separator := spec.GotoSeparator
		if separator == "" {
			separator = ":"
		}
		if !slices.Contains(profile.LaunchSyntax.AllowedGotoSeparators, separator) {
			return errors.New("appopen: package goto separator is not allowed by the Core safety profile")
		}
	}
	if req.Line > 0 && spec.GotoFlag == "" {
		return errors.New("appopen: requested location has no Core-reviewed launch syntax")
	}
	if req.NewWindow && spec.NewWindowFlag == "" {
		return errors.New("appopen: requested new-window effect has no Core-reviewed launch syntax")
	}
	if !req.NewWindow && req.BinaryPath != "" && spec.ReuseWindowFlag == "" {
		return errors.New("appopen: requested reuse-window effect has no Core-reviewed launch syntax")
	}
	if req.Line == 0 && req.BinaryPath != "" && !profile.LaunchSyntax.AllowPositionalTarget {
		return errors.New("appopen: positional resource launch is not allowed by the Core safety profile")
	}
	return nil
}

func safetyIdentityMatches(profile SafetyProfile, identity SafetyIdentity) bool {
	if !identity.Signed || identity.Platform == "" || identity.BundleID == "" || identity.TeamID == "" || identity.CodeIdentity == "" || identity.ExecutableRelativePath == "" || identity.ExecutableCodeIdentity == "" {
		return false
	}
	for _, matcher := range profile.IdentityMatchers {
		if matcher.Platform == identity.Platform && matcher.BundleID == identity.BundleID && matcher.TeamID == identity.TeamID && matcher.ExecutableRelativePath == identity.ExecutableRelativePath && matcher.ExecutableIdentityPolicy == ExecutableIdentityExactObserved && (matcher.CodeIdentity == "" || matcher.CodeIdentity == identity.CodeIdentity) {
			return true
		}
	}
	return false
}

func argvHasFlag(argv []string, flag string) bool {
	for _, arg := range argv {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func argvHasPair(argv []string, flag, value string) bool {
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] == flag && argv[index+1] == value {
			return true
		}
	}
	return false
}

func hasDuplicate(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func validSafetyFlag(value string) bool {
	return len(value) <= maxSafetyTextBytes && strings.HasPrefix(value, "-") && !strings.ContainsAny(value, "\x00 \t\r\n=")
}

func boundedSafetyText(value string) error {
	if len(value) > maxSafetyTextBytes || hasControl(value) {
		return errors.New("text exceeds its Core safety bound")
	}
	return nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func cloneSettings(settings map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, errors.New("appopen: Core safety settings are not JSON data")
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, errors.New("appopen: Core safety settings could not be normalized")
	}
	return clone, nil
}

func shortIdentity(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
