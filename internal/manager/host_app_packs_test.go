package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/packsnapshot"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestHostAppGuidedAddAtomicallyTestsAndEnablesFutureRuns(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.guided", "guided-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	oldCatalog, _, err := core.CompileHostAppCatalog("privacy", "run_before_enablement", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := oldCatalog.ResolveCommand("guided-editor"); ok {
		t.Fatal("old run unexpectedly started with the future recipe binding")
	}

	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstallOnly || plan.Profile != "privacy" || plan.ExpectedIdentityDigest == "" || len(plan.CommandPlan.Owners) == 0 || !strings.Contains(plan.Message, "future runs only") {
		t.Fatalf("guided plan lacks exact authority review: %+v", plan)
	}
	pinnedPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
		ExpectedDigest: plan.ExpectedSourceDigest,
	})
	if err != nil || pinnedPlan.RevisionID != plan.RevisionID {
		t.Fatalf("exact source digest was not accepted: plan=%+v err=%v", pinnedPlan, err)
	}
	if _, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
		ExpectedDigest: "sha256:" + strings.Repeat("0", 64),
	}); err == nil {
		t.Fatal("incorrect exact source digest was accepted")
	}
	if registry, err := hostapppack.NewStore(store.Root).LoadRegistry(); err != nil || len(registry.Packs) != 0 {
		t.Fatalf("guided plan mutated registry: %+v err=%v", registry, err)
	}

	result, err := core.ApplyHostAppPack(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Test == nil || result.Test.Status != hostapppack.TestPassed || result.Enablement == nil {
		t.Fatalf("guided result is partial: %+v", result)
	}
	stored, err := hostapppack.NewStore(store.Root).LoadEnablement("privacy", "community.guided")
	if err != nil || stored.RevisionID != plan.RevisionID || stored.ObservedIdentityDigest != plan.ExpectedIdentityDigest {
		t.Fatalf("enablement=%+v err=%v", stored, err)
	}
	if _, ok := oldCatalog.ResolveCommand("guided-editor"); ok {
		t.Fatal("existing run catalog changed after profile enablement")
	}
	newCatalog, _, err := core.CompileHostAppCatalog("privacy", "run_after_enablement", nil)
	if err != nil {
		t.Fatal(err)
	}
	if binding, ok := newCatalog.ResolveCommand("guided-editor"); !ok || binding.PackID != "community.guided" {
		t.Fatalf("new run did not receive the accepted recipe binding: %+v ok=%v", binding, ok)
	}
}

func TestHostAppGuidedAddAllowsAdvisoryTestsNotRun(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.no-vectors", "no-vectors-editor")
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(packDir, hostapppack.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Tests = []hostapppack.TestVector{}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, hostapppack.ManifestFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	plan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy"})
	if err != nil || plan.QualityTestStatus != hostapppack.TestNotRun {
		t.Fatalf("quality-only not-run was treated as an authority failure: plan=%+v err=%v", plan, err)
	}
	result, err := core.ApplyHostAppPack(plan)
	if err != nil || result.Test == nil || result.Test.Status != hostapppack.TestNotRun || result.Enablement == nil {
		t.Fatalf("guided not-run result=%+v err=%v", result, err)
	}
}

func TestHostAppInstalledRevisionCanEnableWithQualityNotRunOrFailed(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.optional-quality", "optional-editor")
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(packDir, hostapppack.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Tests = []hostapppack.TestVector{}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, hostapppack.ManifestFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	installPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, InstallOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := core.ApplyHostAppPack(installPlan)
	if err != nil || installed.Revision == nil {
		t.Fatalf("install-only result=%+v err=%v", installed, err)
	}
	enableOpts := HostAppPackOptions{
		Operation: "enable", ProfileName: "privacy", PackID: manifest.ID,
		RevisionID: installed.Revision.RevisionID, Access: hostapppack.AccessAskEachRun,
	}
	enablePlan, err := core.PlanHostAppPack(enableOpts)
	if err != nil || enablePlan.QualityTestStatus != hostapppack.TestNotRun {
		t.Fatalf("not-run quality became an authority prerequisite: plan=%+v err=%v", enablePlan, err)
	}
	if _, err := core.ApplyHostAppPack(enablePlan); err != nil {
		t.Fatalf("enable not-run revision: %v", err)
	}
	qualityPath := hostapppack.NewStore(store.Root).TestResultPath(manifest.ID, installed.Revision.RevisionID)
	if err := os.WriteFile(qualityPath, []byte(`{"corrupt":"quality-only"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptEvidencePlan, err := core.PlanHostAppPack(enableOpts)
	if err != nil || corruptEvidencePlan.QualityTestStatus != hostapppack.TestNotRun {
		t.Fatalf("corrupt optional quality evidence became authority: plan=%+v err=%v", corruptEvidencePlan, err)
	}
	if _, err := core.ApplyHostAppPack(corruptEvidencePlan); err != nil {
		t.Fatalf("corrupt optional quality evidence blocked exact enablement: %v", err)
	}

	failed := hostapppack.TestResult{
		SchemaVersion: hostapppack.TestResultVersion, ID: "quality-failed", PackID: manifest.ID,
		RevisionID: installed.Revision.RevisionID, Status: hostapppack.TestFailed,
		Failed: 1, Failures: []string{"recipe assertion failed"},
		Results:    []hostapppack.TestOutcome{{ID: "recipe-case", Status: hostapppack.TestFailed, Reason: "recipe assertion failed"}},
		RecordedAt: time.Now().UTC(),
	}
	if err := hostapppack.NewStore(store.Root).SaveTestResult(failed); err != nil {
		t.Fatal(err)
	}
	failedPlan, err := core.PlanHostAppPack(enableOpts)
	if err != nil || failedPlan.QualityTestStatus != hostapppack.TestFailed {
		t.Fatalf("failed optional quality result became authority: plan=%+v err=%v", failedPlan, err)
	}
	if result, err := core.ApplyHostAppPack(failedPlan); err != nil || result.Enablement == nil {
		t.Fatalf("failed quality evidence blocked exact enablement: result=%+v err=%v", result, err)
	}
}

func TestHostAppGuidedAddPersistsFailedQualityAsAdvisoryEvidence(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.failed-quality", "failed-quality-editor")
	manifest := readManagerHostAppManifest(t, packDir)
	manifest.Tests[0].Expected.Resource = "/workspace/not-the-parser-result"
	writeManagerHostAppManifest(t, packDir, manifest)
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil || plan.QualityTestStatus != hostapppack.TestFailed {
		t.Fatalf("failed quality plan=%+v err=%v", plan, err)
	}
	plan.QualityTestStatus = hostapppack.TestPassed
	result, err := core.ApplyHostAppPack(plan)
	if err != nil || result.Test == nil || result.Test.Status != hostapppack.TestFailed || result.Enablement == nil {
		t.Fatalf("failed quality was treated as authority: result=%+v err=%v", result, err)
	}
}

func TestHostAppPublicPlansResultsAndErrorsRedactSourceLocators(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	packDir := writeManagerHostAppPack(t, root, "community.private-source", "private-editor")
	core := Core{Store: store}
	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, InstallOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(planJSON), root) || strings.Contains(string(planJSON), packDir) || !strings.Contains(string(planJSON), "local-directory") {
		t.Fatalf("public plan leaked or omitted sanitized source: %s", planJSON)
	}
	result, err := core.ApplyHostAppPack(plan)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resultJSON), root) || strings.Contains(string(resultJSON), packDir) {
		t.Fatalf("public result leaked local source path: %s", resultJSON)
	}
	for _, operation := range []string{"validate", "test"} {
		exactPlan, err := core.PlanHostAppPack(HostAppPackOptions{
			Operation: operation, PackID: plan.PackID, RevisionID: plan.RevisionID,
		})
		if err != nil {
			t.Fatalf("%s plan: %v", operation, err)
		}
		exactResult, err := core.ApplyHostAppPack(exactPlan)
		if err != nil {
			t.Fatalf("%s apply: %v", operation, err)
		}
		encoded, err := json.Marshal(exactResult)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), packDir) {
			t.Fatalf("public %s result leaked local source path: %s", operation, encoded)
		}
	}

	rawURL := "https://operator:secret@example.test/community/private.git?token=hidden"
	sourceErr := fmt.Errorf("git clone %s failed below %s for %s\x1b[31m", rawURL, filepath.Join(root, "store"), packDir)
	clean := sanitizeHostAppSourceError(sourceErr, hostapppack.SourceSpec{Kind: hostapppack.SourceGit, URL: rawURL}, store.Root).Error()
	for _, secret := range []string{"operator", "secret", "token=hidden", root, packDir, "\x1b"} {
		if strings.Contains(clean, secret) {
			t.Fatalf("sanitized source error leaked %q: %q", secret, clean)
		}
	}
	if !strings.Contains(clean, "https://example.test/community/private.git") {
		t.Fatalf("sanitized error lost useful source identity: %q", clean)
	}
	auditPlan := plan
	auditPlan.Source = hostapppack.SourceSpec{Kind: hostapppack.SourceGit, URL: rawURL}
	if err := core.recordHostAppPackAudit("add-failed", auditPlan, hostAppLifecycleError(recovery.CodeHostAppSourceInvalid, sourceErr)); err != nil {
		t.Fatal(err)
	}
	auditPaths, err := filepath.Glob(filepath.Join(store.Root, "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var auditData strings.Builder
	for _, path := range auditPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		auditData.Write(data)
	}
	for _, secret := range []string{"operator", "secret", "token=hidden", root, packDir, "\x1b"} {
		if strings.Contains(auditData.String(), secret) {
			t.Fatalf("lifecycle audit leaked %q: %s", secret, auditData.String())
		}
	}
}

func TestHostAppIdentityReviewReceivesCompleteDerivedForbiddenRoots(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	profileRecord, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	hostFSRoot := filepath.Join(root, "hostfs-root")
	profileRecord.HostFS.Grants = []hostfs.Rule{{
		ID: "host-app-overlap", HostPath: hostFSRoot, GuestPath: "/mnt/hostfs",
		Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeRecursiveDir, Reason: "overlap regression",
	}}
	if err := store.Save(profileRecord); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if _, err := (environment.Store{Root: store.Root}).Create(environment.Spec{
		Name: "host-app-overlap", ImageRef: environment.BuiltinBaseImage, Profile: "privacy",
		Backend: "native", Workspace: workspace, GuestWorkspace: "/workspace",
	}); err != nil {
		t.Fatal(err)
	}
	activeRunRoot := filepath.Join(root, "active-run-root")
	packDir := writeManagerHostAppPack(t, root, "community.overlap", "overlap-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	originalResolver := core.HostAppIdentityResolver
	var observed []string
	core.HostAppIdentityResolver = func(expectation hostcap.ApplicationExpectation, forbidden []string) (hostcap.ObservedApplicationIdentity, error) {
		observed = append([]string(nil), forbidden...)
		return originalResolver(expectation, forbidden)
	}
	core.HostAppForbiddenRoots = func(profileName string) ([]string, error) {
		if profileName != "privacy" {
			t.Fatalf("forbidden-root helper profile=%q", profileName)
		}
		return []string{activeRunRoot}, nil
	}
	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Review.ApplicationsObserved) != 1 || plan.Review.ApplicationsObserved[0].Verification != "unverified" || plan.Review.ApplicationsObserved[0].ContentDigest == "" || len(plan.Review.ResultPolicies) != 1 || plan.Review.ResultPolicies[0] != hostapppack.ResultNone {
		t.Fatalf("review omitted trust/content/result facts: %+v", plan.Review)
	}
	for _, want := range []string{store.Root, os.TempDir(), packDir, hostFSRoot, workspace, activeRunRoot} {
		if !containsString(observed, filepath.Clean(want)) {
			t.Fatalf("identity review omitted forbidden root %q from %v", want, observed)
		}
	}
}

func TestUnverifiedHostAppTrustIsExactAndRequiresRetrustAfterChange(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.unverified", "unverified-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)

	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Access != hostapppack.AccessAskEachRun || len(plan.UnverifiedAppTrust) != 1 {
		t.Fatalf("unsigned app plan omitted elevated exact trust: %+v", plan)
	}
	tampered := plan
	tampered.UnverifiedAppTrust = append([]HostAppUnverifiedTrustReview(nil), plan.UnverifiedAppTrust...)
	tampered.UnverifiedAppTrust[0].ContentDigest = hostcap.BundleTreeDigestPrefix + strings.Repeat("0", 64)
	if _, err := core.ApplyHostAppPack(tampered); err == nil {
		t.Fatal("apply accepted a caller-rewritten unsigned app trust digest")
	}
	result, err := core.ApplyHostAppPack(plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Enablement == nil || len(result.Enablement.UnverifiedAppTrust) != 1 {
		t.Fatalf("unsigned app acceptance was not persisted with enablement: %+v", result.Enablement)
	}
	accepted := result.Enablement.UnverifiedAppTrust[0]
	if accepted.ContentDigest != plan.UnverifiedAppTrust[0].ContentDigest || accepted.AcceptedAt.IsZero() {
		t.Fatalf("persisted trust does not bind the reviewed digest: %+v", accepted)
	}
	if _, _, err := core.CompileHostAppCatalog("privacy", "run-before-change", nil); err != nil {
		t.Fatalf("accepted exact unsigned app did not compile: %v", err)
	}

	executable := filepath.Join(root, "Applications", "Editor.app", "Contents", "MacOS", "Editor")
	if err := os.WriteFile(executable, []byte("changed unsigned executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.CompileHostAppCatalog("privacy", "run-after-change", nil); hostcap.CodeOf(err) != hostcap.CodeAppIdentityDrift {
		t.Fatalf("changed unsigned app inherited prior trust: %v", err)
	}

	retrust, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "enable", ProfileName: "privacy", PackID: plan.PackID, RevisionID: plan.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(retrust.UnverifiedAppTrust) != 1 || retrust.UnverifiedAppTrust[0].ContentDigest == plan.UnverifiedAppTrust[0].ContentDigest {
		t.Fatalf("retrust did not surface the changed exact digest: %+v", retrust.UnverifiedAppTrust)
	}
	if _, err := core.ApplyHostAppPack(retrust); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.CompileHostAppCatalog("privacy", "run-after-retrust", nil); err != nil {
		t.Fatalf("explicit retrust did not restore future-run compilation: %v", err)
	}
}

func TestHostAppActiveForbiddenRootContractFailsAuthorityClosed(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.overlap-error", "overlap-error-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	core.HostAppForbiddenRoots = func(string) ([]string, error) {
		return nil, errors.New("active run roots unavailable")
	}
	installOnly, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy", InstallOnly: true,
	})
	if err != nil || len(installOnly.Review.ApplicationsObserved) != 1 || installOnly.Review.ApplicationsObserved[0].Verification != "unsupported" {
		t.Fatalf("read-only review did not expose unavailable observation: plan=%+v err=%v", installOnly, err)
	}
	_, err = core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if HostAppRecoveryCode(err) != recovery.CodeHostAppIdentityInvalid {
		t.Fatalf("active-root discovery failure did not fail authority closed: %v", err)
	}
}

func TestHostAppPackPlanApplyTestAndEnableFutureRuns(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.editor", "editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)

	plan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, InstallOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExpectedSourceDigest == "" || plan.ExpectedPermissionFingerprint == "" || plan.Review.PackID != "community.editor" {
		t.Fatalf("plan lacks Core-derived review: %+v", plan)
	}
	if _, err := os.Stat(hostapppack.NewStore(store.Root).RegistryPath()); !os.IsNotExist(err) {
		t.Fatalf("read-only plan mutated registry: %v", err)
	}
	added, err := core.ApplyHostAppPack(plan)
	if err != nil || !added.Applied || added.Revision == nil {
		t.Fatalf("add result=%+v err=%v", added, err)
	}
	if got, err := hostapppack.NewStore(store.Root).ListEnablements("privacy"); err != nil || len(got) != 0 {
		t.Fatalf("install must remain inert: enablements=%v err=%v", got, err)
	}
	validatePlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "validate", PackID: "community.editor", RevisionID: added.Revision.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if validated, err := core.ApplyHostAppPack(validatePlan); err != nil || !validated.Applied {
		t.Fatalf("validate result=%+v err=%v", validated, err)
	}

	testPlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "test", PackID: "community.editor", RevisionID: added.Revision.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	tested, err := core.ApplyHostAppPack(testPlan)
	if err != nil || tested.Test == nil || tested.Test.Status != hostapppack.TestPassed {
		t.Fatalf("test result=%+v err=%v", tested, err)
	}
	enablePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "enable", ProfileName: "privacy", PackID: "community.editor", RevisionID: added.Revision.RevisionID,
		BindingIDs: []string{"open-resource"}, Access: hostapppack.AccessAskEachRun,
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := core.ApplyHostAppPack(enablePlan)
	if err != nil || !enabled.Applied || enabled.Enablement == nil {
		t.Fatalf("enable result=%+v err=%v", enabled, err)
	}
	catalog, registrations, err := core.CompileHostAppCatalog("privacy", "run_new", nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := catalog.ResolveCommand("editor")
	if !ok || binding.PackID != "community.editor" || binding.BindingDigest == "" || len(registrations) == 0 {
		t.Fatalf("future-run binding missing: %+v registrations=%+v", binding, registrations)
	}
	auditPaths, err := filepath.Glob(filepath.Join(store.Root, "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var auditText strings.Builder
	for _, path := range auditPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		auditText.Write(data)
	}
	for _, action := range []string{`"action":"host.app.install"`, `"action":"host.app.validate"`, `"action":"host.app.test"`, `"action":"host.app.enable"`} {
		if !strings.Contains(auditText.String(), action) {
			t.Fatalf("lifecycle audit lacks %s: %s", action, auditText.String())
		}
	}
	if strings.Contains(auditText.String(), packDir) || strings.Contains(auditText.String(), root) {
		t.Fatalf("lifecycle audit leaked a source locator: %s", auditText.String())
	}
}

func TestHostAppPackApplyRejectsSourceDrift(t *testing.T) {
	root := t.TempDir()
	core := Core{Store: profile.Store{Root: filepath.Join(root, "store")}}
	packDir := writeManagerHostAppPack(t, root, "community.editor", "editor")
	plan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, InstallOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(plan); err == nil {
		t.Fatal("source drift was accepted")
	}
}

func TestHostAppPackExactCommitGitAddUsesTheSameGuidedPlanApply(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	repo := writeManagerHostAppPack(t, root, "community.git-editor", "git-editor")
	runGitForHostAppTest(t, repo, "init")
	runGitForHostAppTest(t, repo, "add", hostapppack.ManifestFileName)
	runGitForHostAppTest(t, repo, "-c", "user.name=Hideout Test", "-c", "user.email=test@hideout.invalid", "commit", "-m", "pack")
	commit := strings.TrimSpace(runGitForHostAppTest(t, repo, "rev-parse", "HEAD"))
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	if _, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceGit, SourceURL: repo, SourceCommit: "main", ProfileName: "privacy"}); err == nil {
		t.Fatal("non-exact Git ref was accepted")
	}
	plan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceGit, SourceURL: repo, SourceCommit: commit, ProfileName: "privacy"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := core.ApplyHostAppPack(plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision == nil || result.Revision.Source.Kind != hostapppack.SourceGit || result.Revision.Source.Commit != commit || result.Enablement == nil {
		t.Fatalf("Git guided result lost exact source or enablement: %+v", result)
	}
}

func TestCompileHostAppCatalogRejectsObservedIdentityDrift(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.identity", "identity-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	plan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(plan); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "Applications", "Editor.app", "Contents", "MacOS", "Editor")
	if err := os.WriteFile(executable, []byte("changed host application identity"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.CompileHostAppCatalog("privacy", "run_after_drift", nil); hostcap.CodeOf(err) != hostcap.CodeAppIdentityDrift {
		t.Fatalf("identity drift compiled into a future run: %v", err)
	}
}

func TestCompileHostAppCatalogOmitsUnclassifiableOptionalBuiltinWithoutBreakingRun(t *testing.T) {
	for name, identityErr := range map[string]error{
		"typed drift":       &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "unverified bundle exceeds bounded digest"},
		"trust unavailable": errors.New("host trust assessment timed out"),
	} {
		t.Run(name, func(t *testing.T) {
			store := profile.Store{Root: filepath.Join(t.TempDir(), "store")}
			if _, err := store.LoadOrInit("privacy"); err != nil {
				t.Fatal(err)
			}
			core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
			core.HostAppIdentityResolver = func(hostcap.ApplicationExpectation, []string) (hostcap.ObservedApplicationIdentity, error) {
				return hostcap.ObservedApplicationIdentity{}, identityErr
			}
			catalog, registrations, err := core.CompileHostAppCatalog("privacy", "run_without_optional_projection", nil)
			if err != nil {
				t.Fatalf("optional built-in projection broke unrelated run: %v", err)
			}
			if len(catalog.Bindings()) != 0 || len(registrations) != 0 {
				t.Fatalf("unclassifiable built-in retained authority: bindings=%+v registrations=%+v", catalog.Bindings(), registrations)
			}
		})
	}
}

func TestHostAppEnablePlanRejectsReservedConflictAndStaleReplacement(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)

	reservedDir := writeManagerHostAppPack(t, root, "community.reserved", "open")
	if _, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: reservedDir, ProfileName: "privacy"}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved command passed guided add planning: %v", err)
	}

	firstDir := writeManagerHostAppPack(t, root, "community.first", "shared-editor")
	firstPlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: firstDir, ProfileName: "privacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(firstPlan); err != nil {
		t.Fatal(err)
	}

	secondDir := writeManagerHostAppPack(t, root, "community.second", "shared-editor")
	secondOpts := HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: secondDir, ProfileName: "privacy"}
	if _, err := core.PlanHostAppPack(secondOpts); err == nil || !strings.Contains(err.Error(), "explicit owner replacement") {
		t.Fatalf("command conflict passed without replacement: %v", err)
	}
	secondRevision := sourceRevisionIDForTest(t, secondDir)
	secondOwner := hostAppBindingOwner("community.second", secondRevision, "open-resource")
	_ = secondOwner
	firstOwner := hostAppBindingOwner("community.first", firstPlan.RevisionID, "open-resource")
	secondOpts.Replacements = map[string]string{"shared-editor": "stale-owner"}
	if _, err := core.PlanHostAppPack(secondOpts); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale replacement passed planning: %v", err)
	}
	secondOpts.Replacements["shared-editor"] = firstOwner
	replacementPlan, err := core.PlanHostAppPack(secondOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(replacementPlan.CommandPlan.Replacements) != 1 || replacementPlan.CommandPlan.Replacements[0].ToOwner != secondOwner {
		t.Fatalf("exact replacement is absent from review: %+v", replacementPlan.CommandPlan)
	}
	if _, err := core.ApplyHostAppPack(replacementPlan); err != nil {
		t.Fatal(err)
	}
	catalog, _, err := core.CompileHostAppCatalog("privacy", "run_after_replacement", nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := catalog.ResolveCommand("shared-editor")
	if !ok || binding.PackID != "community.second" {
		t.Fatalf("future-run catalog did not select the reviewed replacement: %+v ok=%v", binding, ok)
	}
}

func TestHostAppUpdateRequiresExactPermissionReviewAndRollsBackOnDrift(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.update", "update-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	addPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(addPlan); err != nil {
		t.Fatal(err)
	}

	manifest := readManagerHostAppManifest(t, packDir)
	manifest.Version = "1.0.1"
	manifest.Description = "documentation-only update"
	writeManagerHostAppManifest(t, packDir, manifest)
	unchanged, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "update", SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
		PackID: manifest.ID, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.PreviousRevisionID != addPlan.RevisionID || unchanged.RevisionID == addPlan.RevisionID || unchanged.PermissionChanged || unchanged.PermissionDiff.TotalChanges != 0 {
		t.Fatalf("documentation-only update invented permission broadening: %+v", unchanged)
	}
	updated, err := core.ApplyHostAppPack(unchanged)
	if err != nil || updated.Enablement == nil || updated.Enablement.RevisionID != unchanged.RevisionID {
		t.Fatalf("unchanged-permission exact revision was not selected: result=%+v err=%v", updated, err)
	}

	manifest = readManagerHostAppManifest(t, packDir)
	manifest.Version = "2.0.0"
	manifest.Apps[0].Launch.NewWindowFlag = "--new-window"
	writeManagerHostAppManifest(t, packDir, manifest)
	if _, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	}); err == nil || !strings.Contains(err.Error(), "use app update") {
		t.Fatalf("second add bypassed update permission review: %v", err)
	}
	changed, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "update", SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
		PackID: manifest.ID, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.PermissionChanged || changed.PermissionDiff.TotalChanges == 0 || changed.ExpectedPermissionFingerprint == unchanged.ExpectedPermissionFingerprint {
		t.Fatalf("authority change did not require a fresh exact review: %+v", changed)
	}
	before, err := hostapppack.NewStore(store.Root).LoadEnablement("privacy", manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Description = "source drift after accepted plan"
	writeManagerHostAppManifest(t, packDir, manifest)
	if _, err := core.ApplyHostAppPack(changed); err == nil {
		t.Fatal("update source drift was accepted")
	}
	after, err := hostapppack.NewStore(store.Root).LoadEnablement("privacy", manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.RevisionID != before.RevisionID || after.PermissionFingerprint != before.PermissionFingerprint || after.State != hostapppack.EnablementEnabled {
		t.Fatalf("failed update partially changed authority: before=%+v after=%+v", before, after)
	}
}

func TestHostAppUpdateDiffCoversMutableAuthorityFields(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*hostapppack.Manifest)
	}{
		{name: "app-id", mutate: func(m *hostapppack.Manifest) {
			m.Apps[0].ID = "editor-two"
			m.Bindings[0].AppID = "editor-two"
		}},
		{name: "bundle-name", mutate: func(m *hostapppack.Manifest) { m.Apps[0].BundleNames = []string{"Editor Beta.app"} }},
		{name: "executable-relative-path", mutate: func(m *hostapppack.Manifest) { m.Apps[0].ExecutableRelativePath = "Contents/MacOS/EditorBeta" }},
		{name: "expected-bundle-id", mutate: func(m *hostapppack.Manifest) { m.Apps[0].ExpectedBundleID = "com.example.Editor" }},
		{name: "expected-team-id", mutate: func(m *hostapppack.Manifest) { m.Apps[0].ExpectedTeamID = "TEAM123456" }},
		{name: "requested-safety-profile", mutate: func(m *hostapppack.Manifest) { m.Apps[0].RequestedSafetyProfile = "reviewed-editor-v1" }},
		{name: "launch-goto", mutate: func(m *hostapppack.Manifest) { m.Apps[0].Launch.GotoFlag = "--goto" }},
		{name: "launch-new-window", mutate: func(m *hostapppack.Manifest) { m.Apps[0].Launch.NewWindowFlag = "--new-window" }},
		{name: "launch-reuse-window", mutate: func(m *hostapppack.Manifest) { m.Apps[0].Launch.ReuseWindowFlag = "--reuse" }},
		{name: "launch-goto-separator", mutate: func(m *hostapppack.Manifest) { m.Apps[0].Launch.GotoSeparator = ":" }},
		{name: "binding-id", mutate: func(m *hostapppack.Manifest) {
			m.Bindings[0].ID = "open-resource-two"
			m.Tests[0].BindingID = "open-resource-two"
		}},
		{name: "command", mutate: func(m *hostapppack.Manifest) {
			m.Bindings[0].Commands = []string{"authority-editor-two"}
			m.Tests[0].Argv[0] = "authority-editor-two"
		}},
		{name: "binding-app", mutate: func(m *hostapppack.Manifest) {
			second := m.Apps[0]
			second.ID = "editor-two"
			m.Apps = append(m.Apps, second)
			m.Bindings[0].AppID = second.ID
		}},
		{name: "resource-kinds", mutate: func(m *hostapppack.Manifest) {
			m.Bindings[0].ResourceKinds = []string{hostapppack.ResourceHostFSPortal, hostapppack.ResourceWorkspace}
		}},
		{name: "grammar-goto-flags", mutate: func(m *hostapppack.Manifest) { m.Bindings[0].Grammar.GotoFlags = []string{"--goto"} }},
		{name: "grammar-new-window-flags", mutate: func(m *hostapppack.Manifest) { m.Bindings[0].Grammar.NewWindowFlags = []string{"--new-window"} }},
		{name: "grammar-reuse-window-flags", mutate: func(m *hostapppack.Manifest) { m.Bindings[0].Grammar.ReuseWindowFlags = []string{"--reuse"} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			root := t.TempDir()
			store := profile.Store{Root: filepath.Join(root, "store")}
			if _, err := store.LoadOrInit("privacy"); err != nil {
				t.Fatal(err)
			}
			packDir := writeManagerHostAppPack(t, root, "community.authority-"+strings.ReplaceAll(mutation.name, "-", "."), "authority-editor")
			core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
			configureManagerHostAppIdentity(t, &core, root)
			addPlan, err := core.PlanHostAppPack(HostAppPackOptions{
				Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := core.ApplyHostAppPack(addPlan); err != nil {
				t.Fatal(err)
			}
			originalResolver := core.HostAppIdentityResolver
			baseManifest := readManagerHostAppManifest(t, packDir)
			baseIdentity, err := originalResolver(applicationExpectation(baseManifest.ID, addPlan.RevisionID, baseManifest.Apps[0]), nil)
			if err != nil {
				t.Fatal(err)
			}
			core.HostAppIdentityResolver = func(expectation hostcap.ApplicationExpectation, _ []string) (hostcap.ObservedApplicationIdentity, error) {
				identity := baseIdentity
				identity.QualifiedAppRef = expectation.QualifiedAppRef
				return identity, nil
			}
			candidate := readManagerHostAppManifest(t, packDir)
			candidate.Version = "2.0.0"
			mutation.mutate(&candidate)
			writeManagerHostAppManifest(t, packDir, candidate)
			updateOpts := HostAppPackOptions{
				Operation: "update", SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
				ProfileName: "privacy", PackID: addPlan.PackID,
			}
			if mutation.name == "binding-id" {
				updateOpts.BindingIDs = []string{"open-resource-two"}
			}
			updatePlan, err := core.PlanHostAppPack(updateOpts)
			if err != nil {
				t.Fatal(err)
			}
			if !updatePlan.PermissionChanged || updatePlan.PermissionDiff.TotalChanges == 0 ||
				updatePlan.ExpectedPermissionFingerprint == addPlan.ExpectedPermissionFingerprint {
				t.Fatalf("%s mutation escaped update diff: %+v", mutation.name, updatePlan)
			}
			if result, err := core.ApplyHostAppPack(updatePlan); err != nil || result.Enablement == nil || result.Enablement.RevisionID != updatePlan.RevisionID {
				t.Fatalf("%s exact update result=%+v err=%v", mutation.name, result, err)
			}
		})
	}
}

func TestHostAppUpdateRequiresExplicitChangedAccessInsteadOfInheritance(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Join(root, "verified-access-pack")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := builtinHostAppManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest.ID = "community.changed-access"
	manifest.Version = "1.0.0"
	manifest.Description = "changed access fixture"
	manifest.Bindings[0].Commands = []string{"changed-access-editor"}
	manifest.Tests = nil
	writeManagerHostAppManifest(t, packDir, manifest)
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	addPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(addPlan); err != nil {
		t.Fatal(err)
	}
	manifest = readManagerHostAppManifest(t, packDir)
	manifest.Version = "2.0.0"
	manifest.Bindings[0].RequestedAccess = hostapppack.AccessAskEachRun
	writeManagerHostAppManifest(t, packDir, manifest)
	baseOpts := HostAppPackOptions{
		Operation: "update", SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
		ProfileName: "privacy", PackID: addPlan.PackID,
	}
	if _, err := core.PlanHostAppPack(baseOpts); err == nil {
		t.Fatal("changed package access silently inherited the prior safe choice")
	}
	baseOpts.Access = hostapppack.AccessAskEachRun
	updatePlan, err := core.PlanHostAppPack(baseOpts)
	if err != nil {
		t.Fatal(err)
	}
	if !updatePlan.PermissionChanged || updatePlan.Access != hostapppack.AccessAskEachRun || updatePlan.PermissionDiff.TotalChanges == 0 {
		t.Fatalf("explicit changed access did not require a fresh diff: %+v", updatePlan)
	}
}

func TestHostAppDisableRevokeRemoveRetainEvidenceAndUnrelatedState(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	packDir := writeManagerHostAppPack(t, root, "community.lifecycle", "lifecycle-editor")
	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(plan); err != nil {
		t.Fatal(err)
	}
	oldCatalog, _, err := core.CompileHostAppCatalog("privacy", "old-run", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldBinding, ok := oldCatalog.ResolveCommand("lifecycle-editor")
	if !ok {
		t.Fatal("enabled command missing from materialized session catalog")
	}
	lifecycle, err := core.hostAppBindingLifecycleValidator("privacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle(oldBinding); err != nil {
		t.Fatalf("enabled binding failed runtime lifecycle validation: %v", err)
	}
	unrelated := filepath.Join(hostapppack.NewStore(store.Root).Root, "operator-owned-note")
	if err := os.WriteFile(unrelated, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	disablePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "disable", ProfileName: "privacy", PackID: plan.PackID, RevisionID: plan.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(disablePlan); err != nil {
		t.Fatal(err)
	}
	if _, ok := oldCatalog.ResolveCommand("lifecycle-editor"); !ok {
		t.Fatal("disable mutated an already-materialized session catalog")
	}
	if err := lifecycle(oldBinding); hostcap.CodeOf(err) != hostcap.CodeCommandUnbound {
		t.Fatalf("disabled binding retained runtime authority in an existing session: %v", err)
	}
	newCatalog, _, err := core.CompileHostAppCatalog("privacy", "new-run", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newCatalog.ResolveCommand("lifecycle-editor"); ok {
		t.Fatal("disabled command compiled into a future run")
	}
	enablement, err := hostapppack.NewStore(store.Root).LoadEnablement("privacy", plan.PackID)
	if err != nil || enablement.State != hostapppack.EnablementDisabled {
		t.Fatalf("disable state=%+v err=%v", enablement, err)
	}

	revokePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "revoke", PackID: plan.PackID, RevisionID: plan.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(revokePlan); err != nil {
		t.Fatal(err)
	}
	registry, err := hostapppack.NewStore(store.Root).LoadRegistry()
	if err != nil || registry.Packs[0].State != hostapppack.PackRevoked || registry.Packs[0].Revisions[0].State != hostapppack.RevisionRevoked {
		t.Fatalf("revoke registry=%+v err=%v", registry, err)
	}
	revokedInspection, err := core.InspectHostAppPack(plan.PackID, "privacy")
	if err != nil || revokedInspection.Summary.State != hostapppack.PackRevoked || len(revokedInspection.Status.Entries) != 1 || revokedInspection.Status.Entries[0].Summary.Readiness != "disabled" {
		t.Fatalf("revoked inspection=%+v err=%v", revokedInspection, err)
	}

	removePlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "remove", PackID: plan.PackID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(removePlan); err != nil {
		t.Fatal(err)
	}
	registry, err = hostapppack.NewStore(store.Root).LoadRegistry()
	if err != nil || registry.Packs[0].State != hostapppack.PackRemoved || registry.Packs[0].ActiveRevisionID != "" {
		t.Fatalf("remove tombstone=%+v err=%v", registry, err)
	}
	if _, err := os.Stat(filepath.Join(hostapppack.NewStore(store.Root).PacksDir(), plan.PackID)); !os.IsNotExist(err) {
		t.Fatalf("remove retained package-owned snapshot bytes: %v", err)
	}
	removedInspection, err := core.InspectHostAppPack(plan.PackID, "privacy")
	if err != nil || removedInspection.Summary.State != hostapppack.PackRemoved || len(removedInspection.Status.Entries) != 0 || !containsString(removedInspection.Recovery, recovery.CodeHostAppBindingRevoked) {
		t.Fatalf("removed inspection=%+v err=%v", removedInspection, err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "preserve" {
		t.Fatalf("remove touched unrelated state: data=%q err=%v", data, err)
	}
	auditPaths, _ := filepath.Glob(filepath.Join(store.Root, "sessions", "*", "audit.jsonl"))
	var evidence strings.Builder
	for _, path := range auditPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		evidence.Write(data)
	}
	for _, action := range []string{`"action":"host.app.disable"`, `"action":"host.app.revoke"`, `"action":"host.app.remove"`} {
		if !strings.Contains(evidence.String(), action) {
			t.Fatalf("retained lifecycle evidence lacks %s: %s", action, evidence.String())
		}
	}
}

func TestHostAppDisableRevokeRaceEndsFailClosed(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	packDir := writeManagerHostAppPack(t, root, "community.race", "race-editor")
	addPlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(addPlan); err != nil {
		t.Fatal(err)
	}
	disablePlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "disable", ProfileName: "privacy", PackID: addPlan.PackID, RevisionID: addPlan.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	revokePlan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "revoke", PackID: addPlan.PackID, RevisionID: addPlan.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []HostAppPackPlan{disablePlan, revokePlan} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, applyErr := core.ApplyHostAppPack(candidate)
			results <- applyErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for applyErr := range results {
		if applyErr == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatal("concurrent disable/revoke produced no fail-closed transition")
	}
	catalog, _, err := core.CompileHostAppCatalog("privacy", "after-race", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.ResolveCommand("race-editor"); ok {
		t.Fatal("disable/revoke race left command routable")
	}
}

func readManagerHostAppManifest(t *testing.T, dir string) hostapppack.Manifest {
	t.Helper()
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(dir, hostapppack.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func writeManagerHostAppManifest(t *testing.T, dir string, manifest hostapppack.Manifest) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hostapppack.ManifestFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func sourceRevisionIDForTest(t *testing.T, dir string) string {
	t.Helper()
	root := t.TempDir()
	snapshot, err := packsnapshot.Acquire(packsnapshot.SourceSpec{Kind: packsnapshot.SourceLocal, Path: dir}, filepath.Join(root, "source"), packsnapshot.Options{Limits: packsnapshot.DefaultLimits(), DigestStyle: packsnapshot.DigestCanonicalV1, WorkRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	return packsnapshot.RevisionID(snapshot.Digest)
}

func runGitForHostAppTest(t *testing.T, dir string, args ...string) string {
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

func writeManagerHostAppPack(t *testing.T, root, id, command string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := hostapppack.Manifest{
		SchemaVersion: hostapppack.ManifestVersion, ID: id, Version: "1.0.0", Description: "test editor",
		Apps:     []hostapppack.AppSpec{{ID: "editor", Platforms: []string{hostapppack.PlatformDarwin}, BundleNames: []string{"Editor.app"}, ExecutableRelativePath: "Contents/MacOS/Editor", Launch: hostapppack.LaunchSpec{ReuseWindowFlag: "--reuse-window"}}},
		Bindings: []hostapppack.BindingSpec{{ID: "open-resource", Commands: []string{command}, AppID: "editor", CapabilityID: hostapppack.CapabilityOpenResource, ResourceKinds: []string{hostapppack.ResourceWorkspace}, ResultPolicy: hostapppack.ResultNone, RequestedAccess: hostapppack.AccessAskEachRun, Grammar: hostapppack.GrammarSpec{Kind: hostapppack.GrammarOpenResourceV1, ResourceCount: 1, ReuseWindowFlags: []string{"--reuse-window"}, UnknownFlags: hostapppack.UnknownFlagsDeny}}},
		Tests:    []hostapppack.TestVector{{ID: "dot", BindingID: "open-resource", Argv: []string{command, "."}, Expected: hostapppack.TestExpectation{Resource: "/workspace", WindowMode: "reuse"}}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hostapppack.ManifestFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func configureManagerHostAppIdentity(t *testing.T, core *Core, root string) {
	t.Helper()
	appsRoot := filepath.Join(root, "Applications")
	for _, executable := range []string{
		filepath.Join(appsRoot, "Editor.app", "Contents", "MacOS", "Editor"),
		filepath.Join(appsRoot, "Visual Studio Code.app", "Contents", "MacOS", "Code"),
	} {
		if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(executable, []byte(filepath.Base(executable)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	opts := hostcap.ApplicationIdentityOptions{
		Roots:       []hostcap.ApplicationRoot{{Class: hostcap.ApplicationRootOperator, Path: appsRoot}},
		OperatorUID: uint32(os.Getuid()),
		ObserveSigning: func(bundlePath string) (hostcap.SigningObservation, error) {
			if filepath.Base(bundlePath) == "Visual Studio Code.app" {
				return hostcap.SigningObservation{
					Signed: true, Trusted: true, TrustAnchor: "test-platform-trust",
					BundleID: "com.microsoft.VSCode", TeamID: "UBF8T346G9", CodeIdentity: "Developer ID Application: Microsoft Corporation",
				}, nil
			}
			return hostcap.SigningObservation{}, nil
		},
	}
	core.HostAppIdentityResolver = func(expectation hostcap.ApplicationExpectation, _ []string) (hostcap.ObservedApplicationIdentity, error) {
		return hostcap.ResolveApplicationIdentity(expectation, opts)
	}
	core.HostAppIdentityRevalidator = func(expectation hostcap.ApplicationExpectation, previous hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
		return hostcap.RevalidateApplicationIdentity(expectation, previous, opts)
	}
}
