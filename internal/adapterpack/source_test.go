package adapterpack

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdapterPackDigestRetains011PathContentEncoding(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "adapter.js"), []byte("payload"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := DigestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("adapter.js"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte("payload"))
	_, _ = h.Write([]byte{0})
	want := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != want {
		t.Fatalf("011 digest compatibility changed: got %s want %s", got, want)
	}
	if err := os.Chmod(filepath.Join(root, "adapter.js"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := DigestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if after != got {
		t.Fatalf("011 digest unexpectedly changed on mode-only mutation: %s != %s", after, got)
	}
}

func TestLocalSourceLockAndDigestStability(t *testing.T) {
	src := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: src})
	if err != nil {
		t.Fatalf("install local pack: %v", err)
	}
	if entry.PackID != "example.pack" || rev.SourceDigest == "" || rev.ManifestDigest == "" || rev.AdapterDigests["tool"] == "" {
		t.Fatalf("unexpected lock: entry=%+v rev=%+v", entry, rev)
	}
	got, err := DigestTree(store.SourceDir(entry.PackID, rev.RevisionID))
	if err != nil {
		t.Fatal(err)
	}
	if got != rev.SourceDigest {
		t.Fatalf("digest drift: got %s want %s", got, rev.SourceDigest)
	}
}

func TestGitSourceRejectsFloatingRefsAndAcceptsExactCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := initGitPackRepo(t)
	store := NewStore(t.TempDir())
	for _, ref := range []string{"main", "v1.0.0", "abc123"} {
		t.Run(ref, func(t *testing.T) {
			if _, _, err := store.Install(SourceSpec{Kind: SourceGit, URL: repo, Commit: ref}); err == nil {
				t.Fatal("expected non-exact ref rejection")
			}
		})
	}
	commit := gitOutput(t, repo, "rev-parse", "HEAD")
	entry, rev, err := store.Install(SourceSpec{Kind: SourceGit, URL: repo, Commit: commit})
	if err != nil {
		t.Fatalf("install exact commit: %v", err)
	}
	if entry.PackID != "example.pack" || rev.Source.Commit != commit {
		t.Fatalf("unexpected git lock: entry=%+v rev=%+v", entry, rev)
	}
}

func TestGitSourceIgnoresGlobalFilterConfiguration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("adapter.js filter=spoof\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "pack")
	commit := gitOutput(t, repo, "rev-parse", "HEAD")

	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[filter \"spoof\"]\n\tsmudge = sed s/blocked/altered/g\n\tclean = cat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceGit, URL: repo, Commit: commit})
	if err != nil {
		t.Fatalf("install exact commit with hostile global filter: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(store.SourceDir(entry.PackID, rev.RevisionID), "adapter.js"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "altered") || !strings.Contains(string(data), "blocked") {
		t.Fatalf("git global filter changed locked source: %s", data)
	}
}

func TestGitSourceDoesNotRecurseSubmodules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	child := t.TempDir()
	git(t, child, "init", "-b", "main")
	git(t, child, "config", "user.email", "test@example.invalid")
	git(t, child, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(child, "secret.txt"), []byte("submodule-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, child, "add", ".")
	git(t, child, "commit", "-m", "child")

	parent := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	git(t, parent, "init", "-b", "main")
	git(t, parent, "config", "user.email", "test@example.invalid")
	git(t, parent, "config", "user.name", "Test")
	git(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", child, "vendor/child")
	git(t, parent, "add", ".")
	git(t, parent, "commit", "-m", "parent")

	commit := gitOutput(t, parent, "rev-parse", "HEAD")
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceGit, URL: parent, Commit: commit})
	if err != nil {
		t.Fatalf("install exact commit with submodule: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.SourceDir(entry.PackID, rev.RevisionID), "vendor", "child", "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("submodule content should not be copied, err=%v", err)
	}
}

func TestLocalSourceRejectsSymlinksAndExcludesGitDirectory(t *testing.T) {
	src := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	if err := os.Mkdir(filepath.Join(src, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: src})
	if err != nil {
		t.Fatalf("install with .git directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.SourceDir(entry.PackID, rev.RevisionID), ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git directory should not be copied, err=%v", err)
	}

	bad := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	if err := os.Symlink("adapter.js", filepath.Join(bad, "link.js")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := store.Install(SourceSpec{Kind: SourceLocal, Path: bad}); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func testPackDir(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	writePackManifest(t, dir, validManifest())
	if err := os.WriteFile(filepath.Join(dir, "adapter.js"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func initGitPackRepo(t *testing.T) string {
	t.Helper()
	dir := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "pack")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
