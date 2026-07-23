package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/environment"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

func TestBuildProjectionReadinessManifestCanonicalAndStrict(t *testing.T) {
	shimDir := t.TempDir()
	files := map[string]string{
		"hideout-shim": "dispatcher",
		"code":         "code script",
		"editor":       "editor script",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := cmdproxy.NewRegistry([]cmdproxy.Registration{
		{Name: "editor", Aliases: []string{"code"}, Action: cmdproxy.ActionHostOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildProjectionReadinessManifest(
		"ses_ready", "env_ready", "sha256:"+strings.Repeat("c", 64), shimDir, registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest validation: %v", err)
	}
	if got := []string{
		manifest.Entries[0].Name,
		manifest.Entries[1].Name,
		manifest.Entries[2].Name,
	}; strings.Join(got, ",") != "code,editor,hideout-shim" {
		t.Fatalf("entry order=%v", got)
	}
	firstDigest := manifest.CatalogDigest
	again, err := BuildProjectionReadinessManifest(
		"ses_ready", "env_ready", "sha256:"+strings.Repeat("c", 64), shimDir, registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if again.CatalogDigest != firstDigest {
		t.Fatalf("catalog digest changed: %s != %s", again.CatalogDigest, firstDigest)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "code"), []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	changed, err := BuildProjectionReadinessManifest(
		"ses_ready", "env_ready", "sha256:"+strings.Repeat("c", 64), shimDir, registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed.CatalogDigest == firstDigest {
		t.Fatal("catalog digest ignored entry bytes")
	}
}

func TestProjectionReadinessManifestWriteIsStrictAndAtomic(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	if err := os.Mkdir(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "hideout-shim"), []byte("dispatcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildProjectionReadinessManifest(
		"ses_ready", "env_ready", "sha256:"+strings.Repeat("c", 64), shimDir, cmdproxy.Registry{},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, backend.ProjectionReadinessManifestFile)
	if err := WriteProjectionReadinessManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode=%s", info.Mode())
	}
	decoded, err := ReadProjectionReadinessManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CatalogDigest != manifest.CatalogDigest {
		t.Fatalf("catalog digest=%s", decoded.CatalogDigest)
	}
	if err := os.WriteFile(path, []byte(`{"schema":"wrong"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProjectionReadinessManifest(path); err == nil {
		t.Fatal("invalid manifest passed strict read")
	}
}

func TestMaterializeProjectionReadinessPublishesOnlyAfterCompleteCatalog(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	if err := os.Mkdir(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"hideout-shim": "dispatcher", "code": "code"} {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := cmdproxy.NewRegistry([]cmdproxy.Registration{{Name: "code", Action: cmdproxy.ActionHostOpen}})
	if err != nil {
		t.Fatal(err)
	}
	expectation, err := MaterializeProjectionReadiness(
		root, "ses_ready", "env_ready", "sha256:"+strings.Repeat("c", 64), []string{"code"}, registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if expectation == nil || !expectation.TargetProjected {
		t.Fatalf("expectation=%+v", expectation)
	}
	if _, err := os.Lstat(filepath.Join(root, backend.ProjectionReadinessManifestFile)); err != nil {
		t.Fatalf("manifest was not published last: %v", err)
	}

	incomplete := t.TempDir()
	incompleteShims := filepath.Join(incomplete, "shims")
	if err := os.Mkdir(incompleteShims, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incompleteShims, "hideout-shim"), []byte("dispatcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeProjectionReadiness(
		incomplete, "ses_ready", "env_ready", "sha256:"+strings.Repeat("c", 64), []string{"code"}, registry,
	); err == nil {
		t.Fatal("incomplete catalog published readiness")
	}
	if _, err := os.Lstat(filepath.Join(incomplete, backend.ProjectionReadinessManifestFile)); !os.IsNotExist(err) {
		t.Fatalf("incomplete catalog left a readiness marker: %v", err)
	}
}

func TestProjectionReadinessCatalogsRemainSessionLocal(t *testing.T) {
	type sessionFixture struct {
		sessionID string
		command   string
		contents  string
		root      string
	}
	fixtures := []sessionFixture{
		{sessionID: "ses_alpha", command: "code", contents: "alpha-code", root: t.TempDir()},
		{sessionID: "ses_beta", command: "editor", contents: "beta-editor", root: t.TempDir()},
	}
	for _, fixture := range fixtures {
		shimDir := filepath.Join(fixture.root, "shims")
		if err := os.Mkdir(shimDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, contents := range map[string]string{
			"hideout-shim":  "dispatcher-" + fixture.sessionID,
			fixture.command: fixture.contents,
		} {
			if err := os.WriteFile(filepath.Join(shimDir, name), []byte(contents), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	type materializeResult struct {
		index       int
		expectation *backend.ProjectionReadinessExpectation
		err         error
	}
	results := make(chan materializeResult, len(fixtures))
	for index, fixture := range fixtures {
		go func(index int, fixture sessionFixture) {
			registry, err := cmdproxy.NewRegistry([]cmdproxy.Registration{{
				Name: fixture.command, Action: cmdproxy.ActionHostOpen,
			}})
			if err != nil {
				results <- materializeResult{index: index, err: err}
				return
			}
			expectation, err := MaterializeProjectionReadiness(
				fixture.root, fixture.sessionID, "env_ready", "sha256:"+strings.Repeat("c", 64),
				[]string{fixture.command}, registry,
			)
			results <- materializeResult{index: index, expectation: expectation, err: err}
		}(index, fixture)
	}
	expectations := make([]*backend.ProjectionReadinessExpectation, len(fixtures))
	for range fixtures {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		expectations[result.index] = result.expectation
	}
	if expectations[0].Manifest.CatalogDigest == expectations[1].Manifest.CatalogDigest {
		t.Fatal("session-local catalogs with different identities and bytes shared a digest")
	}
	for index, expectation := range expectations {
		if expectation.Manifest.SessionID != fixtures[index].sessionID {
			t.Fatalf("manifest %d session=%q", index, expectation.Manifest.SessionID)
		}
		foreign := fixtures[1-index].command
		for _, entry := range expectation.Manifest.Entries {
			if entry.Name == foreign {
				t.Fatalf("session %s inherited foreign projection %q", fixtures[index].sessionID, foreign)
			}
		}
	}

	ambientDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ambientDir, "ambient-editor"), []byte("ambient"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ambientDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	missingRoot := t.TempDir()
	missingShimDir := filepath.Join(missingRoot, "shims")
	if err := os.Mkdir(missingShimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missingShimDir, "hideout-shim"), []byte("dispatcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := cmdproxy.NewRegistry([]cmdproxy.Registration{{
		Name: "ambient-editor", Action: cmdproxy.ActionHostOpen,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeProjectionReadiness(
		missingRoot, "ses_ambient", "env_ready", "sha256:"+strings.Repeat("c", 64),
		[]string{"ambient-editor"}, registry,
	); err == nil {
		t.Fatal("ambient PATH executable substituted for a missing session projection")
	}
	if _, err := os.Lstat(filepath.Join(missingRoot, backend.ProjectionReadinessManifestFile)); !os.IsNotExist(err) {
		t.Fatalf("ambient fallback attempt published readiness: %v", err)
	}
}

func TestRunSpecBindsProjectionReadinessAcrossLifecycleModes(t *testing.T) {
	expectation := &backend.ProjectionReadinessExpectation{
		Manifest: backend.ProjectionReadinessManifest{
			Schema: backend.ProjectionReadinessManifestSchema, SessionID: "ses_ready",
			EnvironmentID: "env_ready", SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
			CatalogDigest: "sha256:" + strings.Repeat("d", 64),
			Entries: []backend.ProjectionReadinessEntry{{
				Name: "hideout-shim", RelativePath: "hideout-shim",
				SHA256: "sha256:" + strings.Repeat("e", 64), Kind: backend.ProjectionEntryDispatcher,
			}},
		},
		ManifestRelativePath: backend.ProjectionReadinessManifestFile,
		Deadline:             backend.MaxProjectionReadinessDeadline,
	}
	digest, err := backend.ProjectionReadinessCatalogDigest(expectation.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	expectation.Manifest.CatalogDigest = digest

	for _, mode := range []environment.Mode{
		environment.ModeShared,
		environment.ModeDedicated,
		environment.ModeWorkspaceBound,
	} {
		t.Run(string(mode), func(t *testing.T) {
			runSession := RunSession{
				Layout: runsession.Layout{ID: "ses_ready"},
				Plan: RunPlan{
					Workspace: "/workspace", GuestWorkspace: "/work",
					Command: []string{"echo"}, Backend: "lima",
				},
				IdentityDir: "/identity", RuntimeShimDir: "/runtime/shims",
				RuntimeSessionDir: "/runtime/session",
			}
			runEnv := RunEnvironment{
				Active: true,
				Record: environment.Record{ID: "env_ready", Mode: mode},
			}
			spec := (Core{}).runSpec(
				runSession, runEnv,
				RunDataPlane{ProjectionReadiness: expectation},
				RunNetwork{},
			)
			if spec.ProjectionReadiness == nil ||
				spec.ProjectionReadiness.Manifest.CatalogDigest != expectation.Manifest.CatalogDigest {
				t.Fatalf("mode %s lost projection readiness: %+v", mode, spec.ProjectionReadiness)
			}
			if spec.ProjectionReadiness == expectation {
				t.Fatalf("mode %s reused mutable expectation pointer", mode)
			}
		})
	}
}

func TestProjectionReadinessFailureDetailsUseStableRedactedRecoveryHint(t *testing.T) {
	expectation := &backend.ProjectionReadinessExpectation{
		Manifest: backend.ProjectionReadinessManifest{
			CatalogDigest: "sha256:" + strings.Repeat("d", 64),
			Entries:       make([]backend.ProjectionReadinessEntry, 2),
		},
		TargetProjected: true,
	}
	privatePath := "/Users/private/.hideout/sessions/ses_ready/shims/code"
	readinessErr := &backend.ProjectionReadinessError{
		Status: backend.ProjectionReadinessRefused, ReasonCode: backend.ProjectionReadinessDigestMismatch,
		Hint: "inspect " + privatePath, Err: errors.New(privatePath),
	}
	details := projectionReadinessFailureDetails(expectation, readinessErr)
	data, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), privatePath) || strings.Contains(string(data), "/Users/") {
		t.Fatalf("failure details leaked a private path: %s", data)
	}
	if got := details["hint"]; got != "rebuild the session projection and retry" {
		t.Fatalf("hint=%v", got)
	}
	if got := details["reasonCode"]; got != backend.ProjectionReadinessDigestMismatch {
		t.Fatalf("reasonCode=%v", got)
	}
}
