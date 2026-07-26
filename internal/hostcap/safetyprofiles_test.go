package hostcap

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

func TestCoreSafetyProfileCatalogLoadsStrictReviewedData(t *testing.T) {
	profiles := CoreSafetyProfiles()
	if len(profiles) == 0 {
		t.Fatal("Core safety profile catalog is empty")
	}
	identity := appopen.SafetyIdentity{
		Signed: true, Platform: "darwin", BundleID: "com.microsoft.VSCode", TeamID: "UBF8T346G9", CodeIdentity: "observed-cdhash",
		ExecutableRelativePath: "Contents/MacOS/Code", ExecutableCodeIdentity: "sha256:observed-executable",
	}
	profile, err := SelectCoreSafetyProfile("vscode-family-v1", identity)
	if err != nil || profile.ID != "vscode-family-v1" {
		t.Fatalf("reviewed profile unavailable: %+v err=%v", profile, err)
	}
	if profile.IsolatedState.LocalIPCSuffix != "1.12-main.sock" || profile.IsolatedState.MaxLocalIPCPathBytes != 103 {
		t.Fatalf("reviewed VS Code local IPC contract is missing: %+v", profile.IsolatedState)
	}

	profiles[0].RequiredArgv[0] = "--forged"
	again := CoreSafetyProfiles()
	if again[0].RequiredArgv[0] == "--forged" {
		t.Fatal("callers mutated the Core-owned safety profile catalog")
	}
}

func TestCoreSafetyProfileCatalogRejectsUnknownAndInvalidFields(t *testing.T) {
	valid := string(safetyProfilesJSON)
	unknown := strings.Replace(valid, `"version": "1"`, `"version": "1", "packageOverride": true`, 1)
	if unknown == valid {
		t.Fatal("unknown-field fixture did not mutate the catalog")
	}
	if _, err := loadCoreSafetyProfiles([]byte(unknown)); err == nil {
		t.Fatal("unknown profile field should fail strict loading")
	}
	invalid := strings.Replace(valid, `"combined-effect-v1"`, `"trust-package-claim"`, 1)
	if invalid == valid {
		t.Fatal("invalid-verification fixture did not mutate the catalog")
	}
	if _, err := loadCoreSafetyProfiles([]byte(invalid)); err == nil {
		t.Fatal("invalid Core safety contract should fail loading")
	}
}
