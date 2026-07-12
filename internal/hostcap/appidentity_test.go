package hostcap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const testExecutableRelativePath = "Contents/MacOS/TestApp"

func makeTestBundle(t *testing.T, root, name, body string) string {
	t.Helper()
	bundle := filepath.Join(root, name)
	executable := filepath.Join(bundle, filepath.FromSlash(testExecutableRelativePath))
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func testIdentityOptions(root string) ApplicationIdentityOptions {
	return ApplicationIdentityOptions{
		Roots:       []ApplicationRoot{{Class: ApplicationRootOperator, Path: root}},
		OperatorUID: uint32(os.Getuid()),
		ObserveSigning: func(string) (SigningObservation, error) {
			return SigningObservation{Signed: true, Trusted: true, TrustAnchor: "test-system-policy", BundleID: "com.example.TestApp", TeamID: "TEAM123456", CodeIdentity: "test-app"}, nil
		},
		DigestLimits: DefaultBundleTreeLimits,
	}
}

func testIdentityExpectation() ApplicationExpectation {
	return ApplicationExpectation{
		QualifiedAppRef:        "community.test/test-app",
		BundleName:             "Test App.app",
		ExecutableRelativePath: testExecutableRelativePath,
		ExpectedBundleID:       "com.example.TestApp",
		ExpectedTeamID:         "TEAM123456",
	}
}

func TestResolveApplicationIdentityUsesBasenameAndAllowedRoot(t *testing.T) {
	root := t.TempDir()
	bundle := makeTestBundle(t, root, "Test App.app", "v1")
	bundle, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveApplicationIdentity(testIdentityExpectation(), testIdentityOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if got.BundlePath != bundle || got.RootClass != ApplicationRootOperator || got.Verification != AppVerificationVerified {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.CanonicalPathDigest == "" || got.ExecutablePath == "" {
		t.Fatalf("identity omitted host-local launch facts: %+v", got)
	}

	bad := testIdentityExpectation()
	bad.BundleName = "/Applications/Test App.app"
	if _, err := ResolveApplicationIdentity(bad, testIdentityOptions(root)); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("absolute package path should fail closed, got %v", err)
	}
}

func TestResolveApplicationIdentitySkipsAbsentOptionalRoots(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "Applications")
	root := t.TempDir()
	makeTestBundle(t, root, "Test App.app", "v1")
	opts := testIdentityOptions(root)
	opts.Roots = append([]ApplicationRoot{{Class: ApplicationRootOperator, Path: missing}}, opts.Roots...)
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), opts); err != nil {
		t.Fatalf("missing optional application root should be skipped: %v", err)
	}

	opts.Roots = []ApplicationRoot{{Class: ApplicationRootOperator, Path: missing}}
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), opts); CodeOf(err) != CodeAppAbsent {
		t.Fatalf("all roots absent should report app-absent, got %v", err)
	}
}

func TestResolveApplicationIdentityRequiresOneUnambiguousCandidate(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	makeTestBundle(t, first, "Test App.app", "one")
	makeTestBundle(t, second, "Test App.app", "two")
	opts := testIdentityOptions(first)
	opts.Roots = []ApplicationRoot{
		{Class: ApplicationRootOperator, Path: first},
		{Class: ApplicationRootOperator, Path: second},
	}
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), opts); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("multiple matches must be ambiguous, got %v", err)
	}
}

func TestResolveApplicationIdentitySupportsBoundedBundleNameList(t *testing.T) {
	root := t.TempDir()
	makeTestBundle(t, root, "Alternate App.app", "alternate")
	expectation := testIdentityExpectation()
	expectation.BundleName = ""
	expectation.BundleNames = []string{"Test App.app", "Alternate App.app"}
	if _, err := ResolveApplicationIdentity(expectation, testIdentityOptions(root)); err != nil {
		t.Fatalf("one match from a bundle-name list should resolve: %v", err)
	}

	makeTestBundle(t, root, "Test App.app", "primary")
	if _, err := ResolveApplicationIdentity(expectation, testIdentityOptions(root)); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("multiple bundle-name matches must be ambiguous, got %v", err)
	}
}

func TestResolveApplicationIdentityRejectsEscapeWritableAndOverlap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	makeTestBundle(t, outside, "Outside.app", "outside")
	if err := os.Symlink(filepath.Join(outside, "Outside.app"), filepath.Join(root, "Test App.app")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), testIdentityOptions(root)); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("bundle symlink escape should fail closed, got %v", err)
	}

	if err := os.Remove(filepath.Join(root, "Test App.app")); err != nil {
		t.Fatal(err)
	}
	bundle := makeTestBundle(t, root, "Test App.app", "safe")
	if err := os.Chmod(filepath.Join(bundle, "Contents"), 0o775); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), testIdentityOptions(root)); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("writable app ancestor should fail closed, got %v", err)
	}

	if err := os.Chmod(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := testIdentityOptions(root)
	opts.ForbiddenRoots = []string{bundle}
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), opts); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("guest-writable/control overlap should fail closed, got %v", err)
	}
}

func TestResolveApplicationIdentityRejectsExecutableEscape(t *testing.T) {
	root := t.TempDir()
	bundle := makeTestBundle(t, root, "Test App.app", "safe")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(bundle, filepath.FromSlash(testExecutableRelativePath))
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, executable); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveApplicationIdentity(testIdentityExpectation(), testIdentityOptions(root)); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("executable escape should fail closed, got %v", err)
	}
}

func TestRevalidateApplicationIdentityDetectsLaunchTimeReplacement(t *testing.T) {
	root := t.TempDir()
	bundle := makeTestBundle(t, root, "Test App.app", "v1")
	expectation := testIdentityExpectation()
	opts := testIdentityOptions(root)
	observed, err := ResolveApplicationIdentity(expectation, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(bundle); err != nil {
		t.Fatal(err)
	}
	makeTestBundle(t, root, "Test App.app", "replacement")
	if _, err := RevalidateApplicationIdentity(expectation, observed, opts); CodeOf(err) != CodeAppIdentityDrift {
		t.Fatalf("replacement should invalidate observed identity, got %v", err)
	}
}

func TestBundleTreeDigestIsStableBoundedAndDescriptorSafe(t *testing.T) {
	bundle := makeTestBundle(t, t.TempDir(), "Unsigned.app", "unsigned-v1")
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("config.json", filepath.Join(bundle, "Contents", "config-link")); err != nil {
		t.Fatal(err)
	}
	digest1, err := digestBundleTree(bundle, DefaultBundleTreeLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest2, err := digestBundleTree(bundle, DefaultBundleTreeLimits, nil)
	if err != nil || digest1 != digest2 || !strings.HasPrefix(digest1, BundleTreeDigestPrefix) {
		t.Fatalf("unstable digest: %q %q err=%v", digest1, digest2, err)
	}

	limit := DefaultBundleTreeLimits
	limit.MaxBytes = 1
	limit.MaxFileBytes = 1
	if _, err := digestBundleTree(bundle, limit, nil); !errors.Is(err, ErrBundleTreeLimit) {
		t.Fatalf("byte limit should fail closed, got %v", err)
	}

	if err := os.Symlink(filepath.Join(t.TempDir(), "escape"), filepath.Join(bundle, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := digestBundleTree(bundle, DefaultBundleTreeLimits, nil); err == nil {
		t.Fatal("escaping symlink should fail closed")
	}
}

func TestBundleTreeDigestRejectsSpecialFileAndMutation(t *testing.T) {
	bundle := makeTestBundle(t, t.TempDir(), "Unsigned.app", "unsigned-v1")
	fifo := filepath.Join(bundle, "Contents", "pipe")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := digestBundleTree(bundle, DefaultBundleTreeLimits, nil); err == nil {
		t.Fatal("special file should fail closed")
	}
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}
	mutated := false
	_, err := digestBundleTree(bundle, DefaultBundleTreeLimits, func(relative string) {
		if !mutated && strings.HasSuffix(relative, "TestApp") {
			mutated = true
			_ = os.WriteFile(filepath.Join(bundle, filepath.FromSlash(relative)), []byte("changed"), 0o755)
		}
	})
	if !errors.Is(err, ErrBundleTreeChanged) {
		t.Fatalf("concurrent mutation should fail closed, got %v", err)
	}
}
