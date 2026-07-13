package hostapppack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/packsnapshot"
)

func TestStoreGuidedInstallRollsBackSnapshotWhenEnablementCannotCommit(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := validHostAppManifest()
	source := writeHostAppPack(t, t.TempDir(), manifest)
	digest, _, err := packsnapshot.DigestTree(source, packsnapshot.DigestCanonicalV1, packsnapshot.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	base, err := BasePermissionFingerprint(manifest)
	if err != nil {
		t.Fatal(err)
	}
	context := EffectivePermissionContext{Access: AccessAskEachRun}
	effective, err := EffectivePermissionFingerprint(manifest, context)
	if err != nil {
		t.Fatal(err)
	}
	revisionID := packsnapshot.RevisionID(digest)
	enablement := Enablement{
		Schema: EnablementVersion, Profile: "privacy", PackID: manifest.ID, RevisionID: revisionID,
		BindingIDs: []string{manifest.Bindings[0].ID}, SourceDigest: digest,
		BasePermissionFingerprint: base, PermissionFingerprint: effective,
		Access: AccessAskEachRun, ObservedIdentityDigest: "sha256:" + repeatHex('c'),
		ConflictReplacements: map[string]string{}, EnabledAt: time.Now().UTC(), State: EnablementEnabled, Reason: "guided add",
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root, "enablements"), []byte("blocks enablement directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.InstallTestEnable(InstallRequest{
		Source: SourceSpec{Kind: SourceLocal, Path: source}, ExpectedSourceDigest: digest,
		ExpectedBasePermissionFingerprint: base,
	}, enablement, context, time.Now().UTC()); err == nil {
		t.Fatal("guided install unexpectedly committed through a blocked enablement path")
	}
	registry, err := store.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Packs) != 0 {
		t.Fatalf("failed guided install left registry authority: %+v", registry)
	}
	if _, err := os.Stat(filepath.Dir(store.SourceDir(manifest.ID, revisionID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed guided install left snapshot bytes: %v", err)
	}
}

func TestStoreInstallPublishesPrivateImmutableRevision(t *testing.T) {
	storeRoot := t.TempDir()
	store := NewStore(storeRoot)
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatalf("install host-app pack: %v", err)
	}
	if entry.ID != "community.cursor" || entry.ActiveRevisionID != revision.RevisionID || revision.State != RevisionInstalled {
		t.Fatalf("unexpected install result: entry=%+v revision=%+v", entry, revision)
	}
	assertPrivateMode(t, store.Root, 0o700)
	assertPrivateMode(t, store.RegistryPath(), 0o600)
	assertPrivateMode(t, store.LockPath(), 0o600)
	assertPrivateMode(t, store.SourceDir(entry.ID, revision.RevisionID), 0o700)

	writeHostAppPack(t, source, func() Manifest {
		changed := validHostAppManifest()
		changed.Description = "mutable source changed"
		return changed
	}())
	installed, err := store.ResolveRevision(entry.ID, revision.RevisionID)
	if err != nil {
		t.Fatalf("resolve immutable revision: %v", err)
	}
	if installed.SourceDigest != revision.SourceDigest {
		t.Fatalf("source mutation changed installed revision: %+v", installed)
	}
}

func TestStoreInstallEnforcesExpectedSourceAndPermissionDigests(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	for _, request := range []InstallRequest{
		{Source: SourceSpec{Kind: SourceLocal, Path: source}, ExpectedSourceDigest: "sha256:" + repeatHex('a')},
		{Source: SourceSpec{Kind: SourceLocal, Path: source}, ExpectedBasePermissionFingerprint: "sha256:" + repeatHex('b')},
	} {
		if _, _, err := store.Install(request); err == nil {
			t.Fatal("expected digest mismatch rejection")
		}
		registry, err := store.LoadRegistry()
		if err != nil {
			t.Fatal(err)
		}
		if len(registry.Packs) != 0 {
			t.Fatalf("failed install changed registry: %+v", registry)
		}
		if entries, err := os.ReadDir(store.PacksDir()); err == nil && len(entries) != 0 {
			t.Fatalf("failed install published pack bytes: %v", entries)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
}

func TestStoreSerializesConcurrentRegistryUpdatesAcrossInstances(t *testing.T) {
	root := t.TempDir()
	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		manifest := validHostAppManifest()
		manifest.ID = fmt.Sprintf("community.pack-%02d", i)
		manifest.Apps[0].ID = fmt.Sprintf("app-%02d", i)
		manifest.Bindings[0].AppID = manifest.Apps[0].ID
		manifest.Bindings[0].ID = fmt.Sprintf("binding-%02d", i)
		manifest.Bindings[0].Commands = []string{fmt.Sprintf("command-%02d", i)}
		manifest.Tests = []TestVector{}
		source := writeHostAppPack(t, t.TempDir(), manifest)
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			_, _, err := NewStore(root).Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
			errs <- err
		}(source)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent install: %v", err)
		}
	}
	registry, err := NewStore(root).LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Packs) != count {
		t.Fatalf("lost concurrent registry updates: got %d want %d", len(registry.Packs), count)
	}
}

func TestStoreDetectsInstalledSnapshotMutation(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(store.SourceDir(entry.ID, revision.RevisionID), ManifestFileName)
	if err := os.WriteFile(manifestPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveRevision(entry.ID, revision.RevisionID); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected source re-digest failure, got %v", err)
	}
}

func TestStoreKeepsImmutableAtomicRevisions(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, first, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	updated := validHostAppManifest()
	updated.Version = "2.0.0"
	updated.Description = "Updated package documentation"
	writeHostAppPack(t, source, updated)
	entry, second, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionID == second.RevisionID || entry.ActiveRevisionID != second.RevisionID || len(entry.Revisions) != 2 {
		t.Fatalf("immutable revisions were not retained: entry=%+v first=%+v second=%+v", entry, first, second)
	}
	if _, err := store.ResolveRevision(entry.ID, first.RevisionID); err != nil {
		t.Fatalf("prior immutable revision no longer resolves: %v", err)
	}
	if _, err := store.ResolveRevision(entry.ID, second.RevisionID); err != nil {
		t.Fatalf("new immutable revision does not resolve: %v", err)
	}
}

func TestStoreGuidedInstallPersistsQualityOnlyStatuses(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{name: "passed", mutate: func(*Manifest) {}, want: TestPassed},
		{name: "failed", mutate: func(manifest *Manifest) {
			manifest.Tests[0].Expected.Resource = "/workspace/not-the-requested-file"
		}, want: TestFailed},
		{name: "not-run", mutate: func(manifest *Manifest) {
			manifest.Tests = []TestVector{}
		}, want: TestNotRun},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := NewStore(t.TempDir())
			manifest := validHostAppManifest()
			testCase.mutate(&manifest)
			source := writeHostAppPack(t, t.TempDir(), manifest)
			digest, _, err := packsnapshot.DigestTree(source, packsnapshot.DigestCanonicalV1, packsnapshot.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			base, err := BasePermissionFingerprint(manifest)
			if err != nil {
				t.Fatal(err)
			}
			context := EffectivePermissionContext{Access: AccessAskEachRun}
			candidate := Revision{
				RevisionID: packsnapshot.RevisionID(digest), PackID: manifest.ID,
				SourceDigest: digest, BasePermissionFingerprint: base,
			}
			enablement := qualityTestEnablement(t, manifest, candidate, context)

			entry, revision, result, err := store.InstallTestEnable(InstallRequest{
				Source: SourceSpec{Kind: SourceLocal, Path: source}, ExpectedSourceDigest: digest,
				ExpectedBasePermissionFingerprint: base,
			}, enablement, context, time.Now().UTC())
			if err != nil {
				t.Fatalf("guided install with %s quality result: %v", testCase.want, err)
			}
			if result.Status != testCase.want || revision.TestStatus != testCase.want {
				t.Fatalf("quality status was not retained: result=%+v revision=%+v", result, revision)
			}
			loaded, err := store.LoadTestResult(entry.ID, revision.RevisionID)
			if err != nil || loaded.Status != testCase.want {
				t.Fatalf("persisted quality result=%+v err=%v", loaded, err)
			}
		})
	}
}

func TestStorePersistsBoundedQualityResultStatuses(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Now().UTC()
	for _, result := range []TestResult{
		{
			SchemaVersion: TestResultVersion, ID: "quality-not-run", PackID: entry.ID,
			RevisionID: revision.RevisionID, Status: TestNotRun, RecordedAt: recordedAt,
		},
		{
			SchemaVersion: TestResultVersion, ID: "quality-passed", PackID: entry.ID,
			RevisionID: revision.RevisionID, Status: TestPassed, Passed: 1,
			Results: []TestOutcome{{ID: "quality-case", Status: TestPassed}}, RecordedAt: recordedAt,
		},
		{
			SchemaVersion: TestResultVersion, ID: "quality-failed", PackID: entry.ID,
			RevisionID: revision.RevisionID, Status: TestFailed, Passed: 1, Failed: 1,
			Results: []TestOutcome{
				{ID: "passing-case", Status: TestPassed},
				{ID: "failing-case", Status: TestFailed, Reason: "resource mismatch"},
			},
			Failures: []string{"failing-case: resource mismatch"}, RecordedAt: recordedAt,
		},
	} {
		t.Run(result.Status, func(t *testing.T) {
			if err := store.SaveTestResult(result); err != nil {
				t.Fatalf("save %s result: %v", result.Status, err)
			}
			loaded, err := store.LoadTestResult(entry.ID, revision.RevisionID)
			if err != nil || loaded.Status != result.Status {
				t.Fatalf("load %s result: %+v err=%v", result.Status, loaded, err)
			}
			registry, err := store.LoadRegistry()
			if err != nil {
				t.Fatal(err)
			}
			got := registry.Packs[registryPackIndex(registry, entry.ID)].Revisions[0].TestStatus
			if got != result.Status {
				t.Fatalf("registry testStatus=%q want %q", got, result.Status)
			}
		})
	}
	assertPrivateMode(t, store.TestResultPath(entry.ID, revision.RevisionID), 0o600)
}

func TestStoreRejectsContradictoryOrUnboundedQualityResults(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	base := TestResult{
		SchemaVersion: TestResultVersion, ID: "quality-result", PackID: entry.ID,
		RevisionID: revision.RevisionID, Status: TestNotRun, RecordedAt: time.Now().UTC(),
	}
	tooManyOutcomes := make([]TestOutcome, MaxTests+1)
	for i := range tooManyOutcomes {
		tooManyOutcomes[i] = TestOutcome{ID: fmt.Sprintf("case-%02d", i), Status: TestPassed}
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*TestResult)
	}{
		{name: "not-run-claims-passed", mutate: func(result *TestResult) { result.Passed = 1 }},
		{name: "not-run-has-passed-outcome", mutate: func(result *TestResult) {
			result.Results = []TestOutcome{{ID: "quality-case", Status: TestPassed}}
		}},
		{name: "too-many-counts", mutate: func(result *TestResult) {
			result.Status, result.Passed = TestPassed, MaxTests+1
		}},
		{name: "too-many-outcomes", mutate: func(result *TestResult) {
			result.Status, result.Passed, result.Results = TestPassed, MaxTests, tooManyOutcomes
		}},
		{name: "outcomes-contradict-counts", mutate: func(result *TestResult) {
			result.Status, result.Passed = TestPassed, 1
			result.Results = []TestOutcome{{ID: "quality-case", Status: TestFailed, Reason: "failed"}}
		}},
		{name: "failed-with-zero-failures", mutate: func(result *TestResult) { result.Status = TestFailed }},
		{name: "unbounded-failure", mutate: func(result *TestResult) {
			result.Status, result.Failed = TestFailed, 1
			result.Failures = []string{strings.Repeat("x", MaxDescriptionBytes+1)}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := base
			testCase.mutate(&result)
			if err := store.SaveTestResult(result); err == nil {
				t.Fatalf("store accepted contradictory result: %+v", result)
			}
		})
	}
	if _, err := os.Stat(store.TestResultPath(entry.ID, revision.RevisionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected quality result was persisted: %v", err)
	}
}

func TestStorePersistsTestResultsAndEnablementStates(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	result := TestResult{
		SchemaVersion: TestResultVersion,
		ID:            "test_result", PackID: entry.ID, RevisionID: revision.RevisionID,
		Status: TestPassed, Passed: 1, RecordedAt: time.Now().UTC(),
	}
	if err := store.SaveTestResult(result); err != nil {
		t.Fatalf("save test result: %v", err)
	}
	loadedResult, err := store.LoadTestResult(entry.ID, revision.RevisionID)
	if err != nil || loadedResult.Status != TestPassed {
		t.Fatalf("load test result: %+v err=%v", loadedResult, err)
	}
	assertPrivateMode(t, store.TestResultPath(entry.ID, revision.RevisionID), 0o600)
	context := EffectivePermissionContext{Access: AccessAskEachRun}
	effective, err := EffectivePermissionFingerprint(validHostAppManifest(), context)
	if err != nil {
		t.Fatal(err)
	}

	enablement := Enablement{
		Schema:  EnablementVersion,
		Profile: "privacy", PackID: entry.ID, RevisionID: revision.RevisionID,
		BindingIDs: []string{"cursor-command"}, SourceDigest: revision.SourceDigest,
		BasePermissionFingerprint: revision.BasePermissionFingerprint,
		PermissionFingerprint:     effective,
		Access:                    AccessAskEachRun, ObservedIdentityDigest: "sha256:" + repeatHex('c'),
		ConflictReplacements: map[string]string{},
		EnabledAt:            time.Now().UTC(), State: EnablementEnabled, Reason: "enabled",
	}
	if err := store.SaveEnablement(enablement, context); err != nil {
		t.Fatalf("save enablement: %v", err)
	}
	if _, err := store.ResolveEnablement(enablement.Profile, enablement.PackID, context); err != nil {
		t.Fatalf("resolve exact enablement: %v", err)
	}
	loaded, err := store.LoadEnablement(enablement.Profile, enablement.PackID)
	if err != nil || loaded.State != EnablementEnabled {
		t.Fatalf("load enablement: %+v err=%v", loaded, err)
	}
	assertPrivateMode(t, store.EnablementPath(enablement.Profile, enablement.PackID), 0o600)

	enablement.State = EnablementSuspended
	enablement.Reason = "permission-fingerprint-changed"
	if err := store.SaveEnablement(enablement, context); err != nil {
		t.Fatalf("suspend enablement: %v", err)
	}
	list, err := store.ListEnablements("privacy")
	if err != nil || len(list) != 1 || list[0].State != EnablementSuspended {
		t.Fatalf("list enablements: %+v err=%v", list, err)
	}
}

func TestStoreEnablementResolutionRejectsReplacedRevisionFiles(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		store := NewStore(t.TempDir())
		manifest := validHostAppManifest()
		source := writeHostAppPack(t, t.TempDir(), manifest)
		_, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
		if err != nil {
			t.Fatal(err)
		}
		context := EffectivePermissionContext{Access: AccessAskEachRun}
		enablement := qualityTestEnablement(t, manifest, revision, context)
		replacement := manifest
		replacement.Description = "replacement manifest outside the installed revision digest"
		manifestPath := filepath.Join(store.SourceDir(manifest.ID, revision.RevisionID), ManifestFileName)
		if err := replaceHostAppRevisionFile(manifestPath, marshalManifest(t, replacement)); err != nil {
			t.Fatal(err)
		}

		if err := store.SaveEnablement(enablement, context); err == nil || !strings.Contains(err.Error(), "source digest mismatch") {
			t.Fatalf("save did not fail closed on a replaced revision manifest: %v", err)
		}
		if _, err := os.Stat(store.EnablementPath(enablement.Profile, enablement.PackID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed save persisted an enablement: %v", err)
		}
	})

	t.Run("resolve", func(t *testing.T) {
		store := NewStore(t.TempDir())
		manifest := validHostAppManifest()
		source := writeHostAppPack(t, t.TempDir(), manifest)
		_, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
		if err != nil {
			t.Fatal(err)
		}
		context := EffectivePermissionContext{Access: AccessAskEachRun}
		enablement := qualityTestEnablement(t, manifest, revision, context)
		if err := store.SaveEnablement(enablement, context); err != nil {
			t.Fatal(err)
		}
		readmePath := filepath.Join(store.SourceDir(manifest.ID, revision.RevisionID), "README.md")
		if err := replaceHostAppRevisionFile(readmePath, []byte("replacement non-manifest revision content")); err != nil {
			t.Fatal(err)
		}

		if _, err := store.ResolveEnablement(enablement.Profile, enablement.PackID, context); err == nil || !strings.Contains(err.Error(), "source digest mismatch") {
			t.Fatalf("resolve did not fail closed on replaced revision content: %v", err)
		}
	})
}

func TestResolveRevisionSnapshotIsCoherentDuringConcurrentReplacement(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := validHostAppManifest()
	source := writeHostAppPack(t, t.TempDir(), manifest)
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(store.SourceDir(entry.ID, revision.RevisionID), ManifestFileName)
	original, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	replacementManifest := manifest
	replacementManifest.Description = "concurrent replacement outside the verified snapshot"
	replacement := marshalManifest(t, replacementManifest)
	if err := replaceHostAppRevisionFile(manifestPath, replacement); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		next := original
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := replaceHostAppRevisionFile(manifestPath, next); err != nil {
				done <- err
				return
			}
			if string(next) == string(original) {
				next = replacement
			} else {
				next = original
			}
		}
	}()

	var coherenceErr error
	for range 64 {
		resolvedRevision, resolvedManifest, err := store.ResolveRevisionManifest(entry.ID, revision.RevisionID)
		if err != nil {
			continue
		}
		if resolvedRevision.SourceDigest != revision.SourceDigest || resolvedManifest.Description != manifest.Description {
			coherenceErr = fmt.Errorf("mixed revision state resolved: %+v %+v", resolvedRevision, resolvedManifest)
			break
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("replace revision manifest: %v", err)
	}
	if coherenceErr != nil {
		t.Fatal(coherenceErr)
	}
	if err := replaceHostAppRevisionFile(manifestPath, original); err != nil {
		t.Fatal(err)
	}
	resolvedRevision, resolvedManifest, err := store.ResolveRevisionManifest(entry.ID, revision.RevisionID)
	if err != nil || resolvedRevision.SourceDigest != revision.SourceDigest || resolvedManifest.Description != manifest.Description {
		t.Fatalf("stable verified snapshot did not resolve: %+v %+v err=%v", resolvedRevision, resolvedManifest, err)
	}
	if scratch, err := filepath.Glob(filepath.Join(store.Root, ".resolve-*")); err != nil || len(scratch) != 0 {
		t.Fatalf("revision resolution left scratch snapshots: %v err=%v", scratch, err)
	}
}

func TestStoreRevokeAndRemoveRetainTerminalTombstone(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	context := EffectivePermissionContext{Access: AccessAskEachRun}
	effective, err := EffectivePermissionFingerprint(validHostAppManifest(), context)
	if err != nil {
		t.Fatal(err)
	}
	enablement := Enablement{
		Schema: EnablementVersion, Profile: "privacy", PackID: entry.ID, RevisionID: revision.RevisionID,
		BindingIDs: []string{"cursor-command"}, SourceDigest: revision.SourceDigest,
		BasePermissionFingerprint: revision.BasePermissionFingerprint, PermissionFingerprint: effective,
		Access: AccessAskEachRun, ObservedIdentityDigest: "sha256:" + repeatHex('e'),
		ConflictReplacements: map[string]string{}, EnabledAt: time.Now().UTC(), State: EnablementEnabled, Reason: "enabled",
	}
	if err := store.SaveEnablement(enablement, context); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeRevision(entry.ID, revision.RevisionID, "operator-revoked"); err != nil {
		t.Fatalf("revoke revision: %v", err)
	}
	if _, err := store.ResolveRevision(entry.ID, revision.RevisionID); err == nil {
		t.Fatal("revoked revision resolved")
	}
	registry, err := store.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	packIndex := registryPackIndex(registry, entry.ID)
	if packIndex < 0 || registry.Packs[packIndex].Revisions[revisionIndex(registry.Packs[packIndex], revision.RevisionID)].State != RevisionRevoked {
		t.Fatalf("revision was not persisted revoked: %+v", registry)
	}
	revokedEnablement, err := store.LoadEnablement(enablement.Profile, enablement.PackID)
	if err != nil || revokedEnablement.State != EnablementRevoked || revokedEnablement.Reason != "operator-revoked" {
		t.Fatalf("enablement was not revoked with revision: %+v err=%v", revokedEnablement, err)
	}
	if err := store.RemovePack(entry.ID, "operator-removed"); err != nil {
		t.Fatalf("remove pack: %v", err)
	}
	registry, err = store.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	packIndex = registryPackIndex(registry, entry.ID)
	if packIndex < 0 {
		t.Fatal("removed pack tombstone disappeared")
	}
	tombstone := registry.Packs[packIndex]
	if tombstone.State != PackRemoved || tombstone.ActiveRevisionID != "" {
		t.Fatalf("missing retained tombstone: %+v", tombstone)
	}
	if _, err := os.Stat(filepath.Join(store.PacksDir(), entry.ID)); !os.IsNotExist(err) {
		t.Fatalf("removed pack bytes remain: %v", err)
	}
}

func TestStoreFailedInstallDoesNotReplaceExistingRegistryOrRevision(t *testing.T) {
	store := NewStore(t.TempDir())
	source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
	entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	bad := validHostAppManifest()
	bad.Bindings[0].CapabilityID = "host.exec"
	writeHostAppPack(t, source, bad)
	if _, _, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}}); err == nil {
		t.Fatal("expected invalid candidate rejection")
	}
	after, err := os.ReadFile(store.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed install changed registry")
	}
	if _, err := store.ResolveRevision(entry.ID, revision.RevisionID); err != nil {
		t.Fatalf("failed install damaged active revision: %v", err)
	}
}

func TestEnablementBindsRevisionBaseAndEffectiveCoreSafetyVersion(t *testing.T) {
	manifest := validHostAppManifest()
	base, err := BasePermissionFingerprint(manifest)
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision{
		RevisionID: "rev_example", PackID: manifest.ID,
		SourceDigest: "sha256:" + repeatHex('a'), BasePermissionFingerprint: base,
	}
	v1 := EffectivePermissionContext{
		Access: AccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
		BindingIDs: []string{"cursor-command"}, ConflictReplacements: map[string]string{},
	}
	effective, err := EffectivePermissionFingerprint(manifest, v1)
	if err != nil {
		t.Fatal(err)
	}
	enablement := Enablement{
		Schema: EnablementVersion, Profile: "privacy", PackID: manifest.ID, RevisionID: revision.RevisionID,
		BindingIDs: []string{"cursor-command"}, SourceDigest: revision.SourceDigest,
		BasePermissionFingerprint: base, PermissionFingerprint: effective,
		Access: AccessSafe, ObservedIdentityDigest: "sha256:" + repeatHex('b'),
		ConflictReplacements: map[string]string{}, EnabledAt: time.Now().UTC(), State: EnablementEnabled, Reason: "enabled",
	}
	if err := ValidateEnablement(enablement, revision, manifest, v1); err != nil {
		t.Fatalf("exact effective permission context rejected: %v", err)
	}
	v2 := EffectivePermissionContext{Access: AccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "2"}
	if err := ValidateEnablement(enablement, revision, manifest, v2); err == nil {
		t.Fatal("Core safety-profile version drift did not suspend effective trust")
	}
	replacementMismatch := v1
	replacementMismatch.ConflictReplacements = map[string]string{"cursor": "prior-owner"}
	if err := ValidateEnablement(enablement, revision, manifest, replacementMismatch); err == nil {
		t.Fatal("enablement accepted a different command-owner replacement context")
	}
	enablement.BasePermissionFingerprint = "sha256:" + repeatHex('c')
	if err := ValidateEnablement(enablement, revision, manifest, v1); err == nil {
		t.Fatal("enablement accepted a mismatched revision base fingerprint")
	}
}

func TestStoreStrictReadersRejectMalformedTrailingData(t *testing.T) {
	newInstalledStore := func(t *testing.T) (Store, RegistryEntry, Revision) {
		t.Helper()
		store := NewStore(t.TempDir())
		source := writeHostAppPack(t, t.TempDir(), validHostAppManifest())
		entry, revision, err := store.Install(InstallRequest{Source: SourceSpec{Kind: SourceLocal, Path: source}})
		if err != nil {
			t.Fatal(err)
		}
		return store, entry, revision
	}
	t.Run("registry", func(t *testing.T) {
		store, _, _ := newInstalledStore(t)
		appendMalformedTrailing(t, store.RegistryPath())
		if _, err := store.LoadRegistry(); err == nil {
			t.Fatal("registry accepted malformed trailing bytes")
		}
	})
	t.Run("test-result", func(t *testing.T) {
		store, entry, revision := newInstalledStore(t)
		result := TestResult{SchemaVersion: TestResultVersion, ID: "result", PackID: entry.ID, RevisionID: revision.RevisionID, Status: TestPassed, Passed: 1, RecordedAt: time.Now().UTC()}
		if err := store.SaveTestResult(result); err != nil {
			t.Fatal(err)
		}
		appendMalformedTrailing(t, store.TestResultPath(entry.ID, revision.RevisionID))
		if _, err := store.LoadTestResult(entry.ID, revision.RevisionID); err == nil {
			t.Fatal("test result accepted malformed trailing bytes")
		}
	})
	t.Run("enablement", func(t *testing.T) {
		store, entry, revision := newInstalledStore(t)
		context := EffectivePermissionContext{Access: AccessAskEachRun}
		effective, err := EffectivePermissionFingerprint(validHostAppManifest(), context)
		if err != nil {
			t.Fatal(err)
		}
		enablement := Enablement{
			Schema: EnablementVersion, Profile: "privacy", PackID: entry.ID, RevisionID: revision.RevisionID,
			BindingIDs: []string{"cursor-command"}, SourceDigest: revision.SourceDigest,
			BasePermissionFingerprint: revision.BasePermissionFingerprint, PermissionFingerprint: effective,
			Access: AccessAskEachRun, ObservedIdentityDigest: "sha256:" + repeatHex('d'),
			ConflictReplacements: map[string]string{}, EnabledAt: time.Now().UTC(), State: EnablementEnabled, Reason: "enabled",
		}
		if err := store.SaveEnablement(enablement, context); err != nil {
			t.Fatal(err)
		}
		appendMalformedTrailing(t, store.EnablementPath(enablement.Profile, enablement.PackID))
		if _, err := store.LoadEnablement(enablement.Profile, enablement.PackID); err == nil {
			t.Fatal("enablement accepted malformed trailing bytes")
		}
	})
}

func writeHostAppPack(t *testing.T, root string, manifest Manifest) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := marshalManifest(t, manifest)
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(manifest.Description), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func qualityTestEnablement(t *testing.T, manifest Manifest, revision Revision, context EffectivePermissionContext) Enablement {
	t.Helper()
	effective, err := EffectivePermissionFingerprint(manifest, context)
	if err != nil {
		t.Fatal(err)
	}
	return Enablement{
		Schema: EnablementVersion, Profile: "privacy", PackID: manifest.ID, RevisionID: revision.RevisionID,
		BindingIDs: []string{manifest.Bindings[0].ID}, SourceDigest: revision.SourceDigest,
		BasePermissionFingerprint: revision.BasePermissionFingerprint, PermissionFingerprint: effective,
		Access: context.Access, ObservedIdentityDigest: "sha256:" + repeatHex('f'),
		ConflictReplacements: map[string]string{}, EnabledAt: time.Now().UTC(),
		State: EnablementEnabled, Reason: "quality test fixture",
	}
}

func replaceHostAppRevisionFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".revision-replacement-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%04o want=%04o", path, got, want)
	}
}

func appendMalformedTrailing(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(" garbage"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func repeatHex(value byte) string { return strings.Repeat(string(value), 64) }
