package packsnapshot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAcquireLocalPublishesPrivateImmutableSnapshot(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "nested", "tool.txt"), "before", 0o755)
	dest := filepath.Join(t.TempDir(), "published", "source")

	got, err := Acquire(SourceSpec{Kind: SourceLocal, Path: src}, dest, Options{})
	if err != nil {
		t.Fatalf("acquire local source: %v", err)
	}
	if got.Source.Kind != SourceLocal || got.Source.Path != src || got.FileCount != 1 || got.TotalBytes != 6 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if got.Digest == "" || got.DigestStyle != DigestCanonicalV2 {
		t.Fatalf("missing canonical digest: %+v", got)
	}
	assertPrivateDir(t, filepath.Dir(dest))
	assertPrivateDir(t, dest)

	writeFile(t, filepath.Join(src, "nested", "tool.txt"), "after", 0o600)
	data, err := os.ReadFile(filepath.Join(dest, "nested", "tool.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("published snapshot followed mutable source: %q", data)
	}
	redigest, _, err := DigestTree(dest, DigestCanonicalV2, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if redigest != got.Digest {
		t.Fatalf("snapshot digest drift: got %s want %s", redigest, got.Digest)
	}
}

func TestAcquireRejectsLinksSpecialFilesAndLimitsWithoutPartialPublication(t *testing.T) {
	tests := []struct {
		name   string
		limits Limits
		build  func(*testing.T, string)
	}{
		{
			name: "symlink",
			build: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "target"), "x", 0o600)
				if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name: "fifo",
			build: func(t *testing.T, root string) {
				if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
					t.Skipf("fifo unavailable: %v", err)
				}
			},
		},
		{
			name:   "file-count",
			limits: Limits{MaxFiles: 1, MaxTotalBytes: 16, MaxFileBytes: 16},
			build: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "a"), "a", 0o600)
				writeFile(t, filepath.Join(root, "b"), "b", 0o600)
			},
		},
		{
			name:   "per-file-bytes",
			limits: Limits{MaxFiles: 2, MaxTotalBytes: 16, MaxFileBytes: 2},
			build: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "large"), "abc", 0o600)
			},
		},
		{
			name:   "total-bytes",
			limits: Limits{MaxFiles: 2, MaxTotalBytes: 3, MaxFileBytes: 3},
			build: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "a"), "ab", 0o600)
				writeFile(t, filepath.Join(root, "b"), "cd", 0o600)
			},
		},
		{
			name:   "entry-count-includes-empty-directories",
			limits: Limits{MaxFiles: 10, MaxEntries: 2, MaxTotalBytes: 16, MaxFileBytes: 16},
			build: func(t *testing.T, root string) {
				for _, name := range []string{"empty-a", "empty-b", "empty-c"} {
					if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := t.TempDir()
			tt.build(t, src)
			parent := t.TempDir()
			dest := filepath.Join(parent, "source")
			if _, err := Acquire(SourceSpec{Kind: SourceLocal, Path: src}, dest, Options{Limits: tt.limits}); err == nil {
				t.Fatal("expected acquisition failure")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("failed acquisition published partial destination: %v", err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed acquisition left staging state: %v", entries)
			}
		})
	}
}

func TestDigestStylesKeepExplicitV1CompatibilityAndAuthenticateMode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "tool"), "payload", 0o600)

	legacy, _, err := DigestTree(root, DigestLegacyPathContentV1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("tool"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte("payload"))
	_, _ = h.Write([]byte{0})
	wantLegacy := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if legacy != wantLegacy {
		t.Fatalf("legacy digest changed: got %s want %s", legacy, wantLegacy)
	}
	canonicalV1Before, _, err := DigestTree(root, DigestLegacyCanonicalV1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h = sha256.New()
	_, _ = h.Write([]byte("tool"))
	_, _ = h.Write([]byte{0})
	var mode [4]byte
	binary.BigEndian.PutUint32(mode[:], uint32(0o644))
	_, _ = h.Write(mode[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte("payload"))
	_, _ = h.Write([]byte{0})
	wantCanonicalV1 := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if canonicalV1Before != wantCanonicalV1 {
		t.Fatalf("canonical V1 digest changed: got %s want %s", canonicalV1Before, wantCanonicalV1)
	}
	canonicalV2Before, _, err := DigestTree(root, DigestCanonicalV2, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if canonicalV2Before == canonicalV1Before {
		t.Fatal("versioned digest styles unexpectedly share an identity")
	}
	if err := os.Chmod(filepath.Join(root, "tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyAfter, _, _ := DigestTree(root, DigestLegacyPathContentV1, DefaultLimits())
	canonicalV1After, _, _ := DigestTree(root, DigestLegacyCanonicalV1, DefaultLimits())
	canonicalV2After, _, _ := DigestTree(root, DigestCanonicalV2, DefaultLimits())
	if legacyAfter != legacy {
		t.Fatalf("legacy digest unexpectedly authenticated mode: %s != %s", legacyAfter, legacy)
	}
	if canonicalV1After == canonicalV1Before || canonicalV2After == canonicalV2Before {
		t.Fatal("a canonical digest did not authenticate mode")
	}
}

func TestDigestCanonicalV2FramesContentAgainstFollowingEntry(t *testing.T) {
	oneFile := t.TempDir()
	twoFiles := t.TempDir()
	writeFile(t, filepath.Join(twoFiles, "a"), "left", 0o600)
	writeFile(t, filepath.Join(twoFiles, "b"), "right", 0o600)

	var mode [4]byte
	binary.BigEndian.PutUint32(mode[:], uint32(0o644))
	malicious := append([]byte("left\x00b\x00"), mode[:]...)
	malicious = append(malicious, 0)
	malicious = append(malicious, []byte("right")...)
	writeBytes(t, filepath.Join(oneFile, "a"), malicious, 0o600)

	v1One, _, err := DigestTree(oneFile, DigestLegacyCanonicalV1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	v1Two, _, err := DigestTree(twoFiles, DigestLegacyCanonicalV1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if v1One != v1Two {
		t.Fatalf("regression setup did not reproduce the V1 concatenation collision: %s != %s", v1One, v1Two)
	}
	v2One, _, err := DigestTree(oneFile, DigestCanonicalV2, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	v2Two, _, err := DigestTree(twoFiles, DigestCanonicalV2, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if v2One == v2Two {
		t.Fatal("length-framed V2 digest allowed file content to inject a following entry")
	}
}

func TestDigestCanonicalMigrationRevalidatesInstalledV1AndCreatesV2(t *testing.T) {
	root := t.TempDir()
	legacyCandidate := filepath.Join(root, "legacy-candidate")
	writeFile(t, filepath.Join(legacyCandidate, "tool"), "legacy", 0o600)
	legacyDigest, _, err := DigestTree(legacyCandidate, DigestLegacyCanonicalV1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	legacyInstalled := filepath.Join(root, RevisionID(legacyDigest), "source")
	if err := os.MkdirAll(filepath.Dir(legacyInstalled), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(legacyCandidate, legacyInstalled); err != nil {
		t.Fatal(err)
	}
	gotLegacy, _, err := DigestTree(legacyInstalled, DigestCanonicalV1, DefaultLimits())
	if err != nil {
		t.Fatalf("revalidate installed V1 revision: %v", err)
	}
	if gotLegacy != legacyDigest {
		t.Fatalf("installed V1 migration digest=%s want %s", gotLegacy, legacyDigest)
	}

	newCandidate := filepath.Join(root, "new-candidate")
	writeFile(t, filepath.Join(newCandidate, "tool"), "new", 0o600)
	gotNew, _, err := DigestTree(newCandidate, DigestCanonicalV1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	wantNew, _, err := DigestTree(newCandidate, DigestCanonicalV2, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if gotNew != wantNew {
		t.Fatalf("migration selector created a non-V2 digest: got %s want %s", gotNew, wantNew)
	}
	newInstalled := filepath.Join(root, RevisionID(wantNew), "source")
	if err := os.MkdirAll(filepath.Dir(newInstalled), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(newCandidate, newInstalled); err != nil {
		t.Fatal(err)
	}
	if redigest, _, err := DigestTree(newInstalled, DigestCanonicalV1, DefaultLimits()); err != nil || redigest != wantNew {
		t.Fatalf("revalidate installed V2 revision: digest=%s err=%v", redigest, err)
	}

	writeFile(t, filepath.Join(legacyInstalled, "tool"), "tampered", 0o600)
	if _, _, err := DigestTree(legacyInstalled, DigestCanonicalV1, DefaultLimits()); err == nil {
		t.Fatal("migration selector accepted an installed tree matching neither digest version")
	}
}

func TestDigestTreeDoesNotOverflowUnboundedLegacyReadLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "payload"), "not-empty", 0o600)
	limits := Limits{MaxFiles: math.MaxInt, MaxTotalBytes: math.MaxInt64, MaxFileBytes: math.MaxInt64}
	got, stats, err := DigestTree(root, DigestLegacyPathContentV1, limits)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalBytes != int64(len("not-empty")) {
		t.Fatalf("unbounded read copied zero bytes: %+v", stats)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("payload"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte("not-empty"))
	_, _ = h.Write([]byte{0})
	want := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != want {
		t.Fatalf("unbounded legacy digest changed: got %s want %s", got, want)
	}
}

func TestAcquireGitRequiresExactCommitAndIgnoresHooksFiltersAndSubmodules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	child := t.TempDir()
	git(t, child, "init", "-b", "main")
	git(t, child, "config", "user.email", "test@example.invalid")
	git(t, child, "config", "user.name", "Test")
	writeFile(t, filepath.Join(child, "secret"), "submodule-secret", 0o600)
	git(t, child, "add", ".")
	git(t, child, "commit", "-m", "child")

	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	writeFile(t, filepath.Join(repo, "payload.txt"), "clean", 0o755)
	writeFile(t, filepath.Join(repo, ".gitattributes"), "payload.txt filter=hostile\n", 0o600)
	git(t, repo, "add", ".")
	git(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", child, "vendor/child")
	git(t, repo, "commit", "-m", "pack")
	commit := gitOutput(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "--orphan", "oversized-unrelated")
	git(t, repo, "rm", "-rf", "--ignore-unmatch", ".")
	writeRandomFile(t, filepath.Join(repo, "unrelated.bin"), 512<<10, 0o600)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "unrelated large history")
	git(t, repo, "checkout", "main")

	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".gitconfig"), "[filter \"hostile\"]\n\tsmudge = sed s/clean/altered/g\n", 0o600)
	hookMarker := filepath.Join(t.TempDir(), "hook-ran")
	template := filepath.Join(t.TempDir(), "template")
	writeFile(t, filepath.Join(template, "hooks", "post-checkout"), "#!/bin/sh\nprintf ran > '"+hookMarker+"'\n", 0o700)
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_TEMPLATE_DIR", template)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "filter.hostile.smudge")
	t.Setenv("GIT_CONFIG_VALUE_0", "sed s/clean/injected/g")

	if _, err := Acquire(SourceSpec{Kind: SourceGit, URL: repo, Commit: "main"}, filepath.Join(t.TempDir(), "bad"), Options{}); err == nil {
		t.Fatal("expected floating ref rejection")
	}
	dest := filepath.Join(t.TempDir(), "git-snapshot")
	limits := DefaultLimits()
	limits.MaxGitPackBytes = 128 << 10
	got, err := Acquire(SourceSpec{Kind: SourceGit, URL: repo, Commit: commit}, dest, Options{Limits: limits})
	if err != nil {
		t.Fatalf("acquire exact git commit: %v", err)
	}
	if got.Source.Commit != strings.ToLower(commit) {
		t.Fatalf("wrong source lock: %+v", got.Source)
	}
	data, err := os.ReadFile(filepath.Join(dest, "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "clean" {
		t.Fatalf("filter changed archived bytes: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dest, "vendor", "child", "secret")); !os.IsNotExist(err) {
		t.Fatalf("submodule content was acquired: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Fatalf("git metadata was published: %v", err)
	}
	if _, err := os.Stat(hookMarker); !os.IsNotExist(err) {
		t.Fatalf("git checkout hook executed: %v", err)
	}
}

func TestAcquireGitEnforcesObjectPackArchiveAndDiskLimits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	writeRandomFile(t, filepath.Join(repo, "payload.bin"), 64<<10, 0o600)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "bounded source")
	commit := gitOutput(t, repo, "rev-parse", "HEAD")

	tests := []struct {
		name  string
		limit func(*Limits)
		want  error
	}{
		{
			name: "object-count",
			limit: func(limits *Limits) {
				limits.MaxGitObjects = 2
			},
			want: ErrGitObjectLimit,
		},
		{
			name: "pack-bytes",
			limit: func(limits *Limits) {
				limits.MaxGitPackBytes = 1024
			},
			want: ErrGitPackLimit,
		},
		{
			name: "archive-bytes",
			limit: func(limits *Limits) {
				limits.MaxGitArchiveBytes = 1024
			},
			want: ErrGitArchiveLimit,
		},
		{
			name: "scratch-disk",
			limit: func(limits *Limits) {
				limits.MaxGitDiskBytes = 8 << 10
			},
			want: ErrGitDiskLimit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := DefaultLimits()
			tt.limit(&limits)
			parent := t.TempDir()
			dest := filepath.Join(parent, "snapshot")
			_, err := Acquire(SourceSpec{Kind: SourceGit, URL: repo, Commit: commit}, dest, Options{Limits: limits})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Acquire error=%v, want %v", err, tt.want)
			}
			if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
				t.Fatalf("limit failure published a destination: %v", statErr)
			}
		})
	}
}

func TestAcquireGitPartialFetchRejectsBlobBeyondFileLimit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "uploadpack.allowFilter", "true")
	writeRandomFile(t, filepath.Join(repo, "oversized.bin"), 64<<10, 0o600)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "oversized blob")
	commit := gitOutput(t, repo, "rev-parse", "HEAD")

	limits := DefaultLimits()
	limits.MaxFileBytes = 1024
	limits.MaxGitPackBytes = 8 << 10
	_, err := Acquire(
		SourceSpec{Kind: SourceGit, URL: "file://" + filepath.ToSlash(repo), Commit: commit},
		filepath.Join(t.TempDir(), "snapshot"),
		Options{Limits: limits},
	)
	if err == nil || !strings.Contains(err.Error(), "object excluded by the 1024-byte file limit") {
		t.Fatalf("partial exact fetch did not reject the omitted oversized blob: %v", err)
	}
	if errors.Is(err, ErrGitPackLimit) {
		t.Fatalf("partial fetch downloaded the oversized blob into the pack: %v", err)
	}
}

func TestAcquireGitTimeoutCancelsTheGitProcess(t *testing.T) {
	realPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	writeFile(t, fakeGit, "#!/bin/sh\nexec sleep 5\n", 0o700)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+realPath)

	started := time.Now()
	_, err := Acquire(
		SourceSpec{Kind: SourceGit, URL: "https://example.invalid/pack.git", Commit: strings.Repeat("a", 40)},
		filepath.Join(t.TempDir(), "snapshot"),
		Options{GitTimeout: 50 * time.Millisecond},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire error=%v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timed-out git process was not canceled promptly: %s", elapsed)
	}
}

func TestAcquireRefusesToReplacePublishedDestination(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "new"), "new", 0o600)
	dest := filepath.Join(t.TempDir(), "source")
	writeFile(t, filepath.Join(dest, "existing"), "keep", 0o600)
	if _, err := Acquire(SourceSpec{Kind: SourceLocal, Path: src}, dest, Options{}); err == nil {
		t.Fatal("expected immutable destination rejection")
	}
	data, err := os.ReadFile(filepath.Join(dest, "existing"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing destination changed: %q err=%v", data, err)
	}
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	writeBytes(t, path, []byte(contents), mode)
}

func writeBytes(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func writeRandomFile(t *testing.T, path string, size int, mode os.FileMode) {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	writeBytes(t, path, data, mode)
}

func assertPrivateDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&0o077 != 0 {
		t.Fatalf("directory %s is not private: %04o", path, got)
	}
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
