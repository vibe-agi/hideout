package packsnapshot

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	slashpath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SourceLocal = "local"
	SourceGit   = "git"

	// DigestCanonicalV1 is a deprecated source-level compatibility selector.
	// It creates V2 digests, but can revalidate an installed V1 revision whose
	// rev_<digest-prefix> directory authenticates the legacy choice.
	DigestCanonicalV1         = "migrate-path-mode-content-v1-to-length-framed-v2"
	DigestCanonicalV2         = "length-framed-path-mode-content-v2"
	DigestLegacyCanonicalV1   = "path-mode-content-v1"
	DigestLegacyPathContentV1 = "path-content-v1"

	DefaultGitTimeout = 30 * time.Second
)

var (
	ErrGitObjectLimit  = errors.New("git pack exceeds object limit")
	ErrGitPackLimit    = errors.New("git pack exceeds byte limit")
	ErrGitDiskLimit    = errors.New("git intake exceeds disk limit")
	ErrGitArchiveLimit = errors.New("git archive exceeds byte limit")
)

type Limits struct {
	MaxFiles           int
	MaxEntries         int
	MaxTotalBytes      int64
	MaxFileBytes       int64
	MaxGitObjects      int
	MaxGitPackBytes    int64
	MaxGitArchiveBytes int64
	MaxGitDiskBytes    int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxFiles: 256, MaxEntries: 256, MaxTotalBytes: 4 << 20, MaxFileBytes: 256 << 10,
		MaxGitObjects: 1024, MaxGitPackBytes: 16 << 20, MaxGitArchiveBytes: 8 << 20, MaxGitDiskBytes: 32 << 20,
	}
}

type SourceSpec struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type SourceLock struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type Options struct {
	Limits      Limits
	DigestStyle string
	WorkRoot    string
	Context     context.Context
	GitTimeout  time.Duration
}

type Snapshot struct {
	Source      SourceLock `json:"source"`
	Digest      string     `json:"digest"`
	DigestStyle string     `json:"digestStyle"`
	FileCount   int        `json:"fileCount"`
	TotalBytes  int64      `json:"totalBytes"`
}

type TreeStats struct {
	FileCount  int
	EntryCount int
	TotalBytes int64
}

func Acquire(spec SourceSpec, dest string, opts Options) (Snapshot, error) {
	if strings.TrimSpace(dest) == "" {
		return Snapshot{}, errors.New("snapshot destination is required")
	}
	if _, err := os.Lstat(dest); err == nil {
		return Snapshot{}, fmt.Errorf("snapshot destination %q already exists", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, err
	}
	limits := normalizeLimits(opts.Limits)
	digestStyle := opts.DigestStyle
	if digestStyle == "" {
		digestStyle = DigestCanonicalV2
	}
	if err := validateDigestStyle(digestStyle); err != nil {
		return Snapshot{}, err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Snapshot{}, err
	}
	staging, err := os.MkdirTemp(parent, ".pack-snapshot-*")
	if err != nil {
		return Snapshot{}, err
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return Snapshot{}, err
	}

	spec.Kind = strings.TrimSpace(spec.Kind)
	if spec.Kind == "" {
		spec.Kind = SourceLocal
	}
	var lock SourceLock
	switch spec.Kind {
	case SourceLocal:
		lock, err = acquireLocal(spec, staging, limits)
	case SourceGit:
		lock, err = acquireGit(spec, staging, limits, opts)
	default:
		err = fmt.Errorf("unsupported pack source kind %q", spec.Kind)
	}
	if err != nil {
		return Snapshot{}, err
	}
	digest, stats, err := DigestTree(staging, digestStyle, limits)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := os.Lstat(dest); err == nil {
		return Snapshot{}, fmt.Errorf("snapshot destination %q appeared during acquisition", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, err
	}
	if err := os.Rename(staging, dest); err != nil {
		return Snapshot{}, err
	}
	if digestStyle == DigestCanonicalV1 {
		digestStyle = DigestCanonicalV2
	}
	return Snapshot{
		Source:      lock,
		Digest:      digest,
		DigestStyle: digestStyle,
		FileCount:   stats.FileCount,
		TotalBytes:  stats.TotalBytes,
	}, nil
}

func DigestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func RevisionID(digest string) string {
	hexPart := strings.TrimPrefix(digest, "sha256:")
	if len(hexPart) > 16 {
		hexPart = hexPart[:16]
	}
	return "rev_" + hexPart
}

func DigestTree(root, style string, limits Limits) (string, TreeStats, error) {
	limits = normalizeLimits(limits)
	if err := validateDigestStyle(style); err != nil {
		return "", TreeStats{}, err
	}
	if style == DigestCanonicalV1 {
		return digestTreeWithV1Migration(root, limits)
	}
	files, stats, err := inspectTree(root, limits)
	if err != nil {
		return "", TreeStats{}, err
	}
	h := sha256.New()
	if style == DigestCanonicalV2 {
		_, _ = io.WriteString(h, "hideout.pack-tree")
		var versionAndCount [12]byte
		binary.BigEndian.PutUint32(versionAndCount[:4], 2)
		binary.BigEndian.PutUint64(versionAndCount[4:], uint64(len(files)))
		_, _ = h.Write(versionAndCount[:])
	}
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Rel))
		in, err := os.Open(path)
		if err != nil {
			return "", TreeStats{}, err
		}
		before, err := in.Stat()
		if err != nil {
			_ = in.Close()
			return "", TreeStats{}, err
		}
		if !before.Mode().IsRegular() || !os.SameFile(file.Info, before) || before.Size() != file.Info.Size() {
			_ = in.Close()
			return "", TreeStats{}, fmt.Errorf("pack source file %q changed during digest", file.Rel)
		}
		switch style {
		case DigestCanonicalV2:
			writeDigestFrame(h, 1, []byte(file.Rel))
			var mode [4]byte
			binary.BigEndian.PutUint32(mode[:], uint32(canonicalFileMode(before.Mode())))
			writeDigestFrame(h, 2, mode[:])
			writeDigestFrameHeader(h, 3, uint64(before.Size()))
		case DigestLegacyCanonicalV1:
			_, _ = h.Write([]byte(file.Rel))
			_, _ = h.Write([]byte{0})
			var mode [4]byte
			binary.BigEndian.PutUint32(mode[:], uint32(canonicalFileMode(before.Mode())))
			_, _ = h.Write(mode[:])
			_, _ = h.Write([]byte{0})
		case DigestLegacyPathContentV1:
			_, _ = h.Write([]byte(file.Rel))
			_, _ = h.Write([]byte{0})
		}
		written, copyErr := io.Copy(h, io.LimitReader(in, overflowSafeReadLimit(limits.MaxFileBytes)))
		if copyErr != nil {
			_ = in.Close()
			return "", TreeStats{}, copyErr
		}
		if written != before.Size() {
			_ = in.Close()
			return "", TreeStats{}, fmt.Errorf("pack source file %q changed during digest", file.Rel)
		}
		after, statErr := in.Stat()
		closeErr := in.Close()
		if statErr != nil {
			return "", TreeStats{}, statErr
		}
		if closeErr != nil {
			return "", TreeStats{}, closeErr
		}
		if !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() {
			return "", TreeStats{}, fmt.Errorf("pack source file %q changed during digest", file.Rel)
		}
		if style != DigestCanonicalV2 {
			_, _ = h.Write([]byte{0})
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), stats, nil
}

func digestTreeWithV1Migration(root string, limits Limits) (string, TreeStats, error) {
	v2, stats, err := DigestTree(root, DigestCanonicalV2, limits)
	if err != nil {
		return "", TreeStats{}, err
	}
	revisionID, installed := installedRevisionID(root)
	if !installed || RevisionID(v2) == revisionID {
		return v2, stats, nil
	}
	v1, legacyStats, err := DigestTree(root, DigestLegacyCanonicalV1, limits)
	if err != nil {
		return "", TreeStats{}, err
	}
	if RevisionID(v1) == revisionID {
		return v1, legacyStats, nil
	}
	return "", TreeStats{}, fmt.Errorf("installed revision %q matches neither canonical digest version", revisionID)
}

func installedRevisionID(root string) (string, bool) {
	if filepath.Base(filepath.Clean(root)) != "source" {
		return "", false
	}
	revisionID := filepath.Base(filepath.Dir(filepath.Clean(root)))
	if len(revisionID) != len("rev_")+16 || !strings.HasPrefix(revisionID, "rev_") {
		return "", false
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(revisionID, "rev_")); err != nil {
		return "", false
	}
	return revisionID, true
}

func writeDigestFrame(w io.Writer, field byte, value []byte) {
	writeDigestFrameHeader(w, field, uint64(len(value)))
	_, _ = w.Write(value)
}

func writeDigestFrameHeader(w io.Writer, field byte, length uint64) {
	var header [9]byte
	header[0] = field
	binary.BigEndian.PutUint64(header[1:], length)
	_, _ = w.Write(header[:])
}

type treeFile struct {
	Rel  string
	Info os.FileInfo
}

func inspectTree(root string, limits Limits) ([]treeFile, TreeStats, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, TreeStats{}, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, TreeStats{}, fmt.Errorf("pack source root %q must be a directory, not a link", root)
	}
	var files []treeFile
	var stats TreeStats
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if slashRel == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		if !validRelativePath(slashRel) {
			return fmt.Errorf("pack source path %q escapes source", path)
		}
		stats.EntryCount++
		if stats.EntryCount > limits.MaxEntries {
			return fmt.Errorf("pack source exceeds %d entries", limits.MaxEntries)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pack source path %q must not be a symlink", path)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("pack source path %q must be a regular file", path)
		}
		stats.FileCount++
		stats.TotalBytes += info.Size()
		if stats.FileCount > limits.MaxFiles {
			return fmt.Errorf("pack source exceeds %d files", limits.MaxFiles)
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("pack source file %q exceeds %d bytes", slashRel, limits.MaxFileBytes)
		}
		if stats.TotalBytes > limits.MaxTotalBytes {
			return fmt.Errorf("pack source exceeds %d total bytes", limits.MaxTotalBytes)
		}
		files = append(files, treeFile{Rel: slashRel, Info: info})
		return nil
	})
	if err != nil {
		return nil, TreeStats{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, stats, nil
}

func acquireLocal(spec SourceSpec, staging string, limits Limits) (SourceLock, error) {
	if strings.TrimSpace(spec.Path) == "" {
		return SourceLock{}, errors.New("local pack source path is required")
	}
	abs, err := filepath.Abs(spec.Path)
	if err != nil {
		return SourceLock{}, err
	}
	abs = filepath.Clean(abs)
	files, _, err := inspectTree(abs, limits)
	if err != nil {
		return SourceLock{}, err
	}
	for _, file := range files {
		if err := copyRegularFile(abs, staging, file, limits.MaxFileBytes); err != nil {
			return SourceLock{}, err
		}
	}
	return SourceLock{Kind: SourceLocal, Path: abs}, nil
}

func copyRegularFile(root, dest string, file treeFile, maxBytes int64) error {
	sourcePath := filepath.Join(root, filepath.FromSlash(file.Rel))
	targetPath := filepath.Join(dest, filepath.FromSlash(file.Rel))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return err
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(file.Info, opened) || file.Info.Size() != opened.Size() {
		return fmt.Errorf("pack source file %q changed during acquisition", file.Rel)
	}
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFileMode(opened.Mode()))
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(in, overflowSafeReadLimit(maxBytes)))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != opened.Size() || written > maxBytes {
		return fmt.Errorf("pack source file %q changed or exceeded limits during acquisition", file.Rel)
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.Mode() != after.Mode() {
		return fmt.Errorf("pack source file %q changed during acquisition", file.Rel)
	}
	return nil
}

func acquireGit(spec SourceSpec, staging string, limits Limits, opts Options) (SourceLock, error) {
	commit := strings.ToLower(strings.TrimSpace(spec.Commit))
	if !IsFullCommit(commit) {
		return SourceLock{}, fmt.Errorf("git source commit %q must be a full 40-hex commit", spec.Commit)
	}
	url := strings.TrimSpace(spec.URL)
	if url == "" {
		return SourceLock{}, errors.New("git pack source url is required")
	}
	if strings.ContainsAny(url, "\x00\r\n") {
		return SourceLock{}, errors.New("git pack source url contains unsupported control characters")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return SourceLock{}, errors.New("system git is required for git pack sources")
	}
	gitPath, err = filepath.Abs(gitPath)
	if err != nil {
		return SourceLock{}, err
	}
	baseContext := opts.Context
	if baseContext == nil {
		baseContext = context.Background()
	}
	timeout := opts.GitTimeout
	if timeout <= 0 {
		timeout = DefaultGitTimeout
	}
	ctx, cancel := context.WithTimeout(baseContext, timeout)
	defer cancel()

	workRoot := opts.WorkRoot
	if workRoot == "" {
		workRoot = filepath.Dir(staging)
	}
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return SourceLock{}, err
	}
	work, err := os.MkdirTemp(workRoot, ".pack-git-*")
	if err != nil {
		return SourceLock{}, err
	}
	defer os.RemoveAll(work)
	repo := filepath.Join(work, "repository.git")

	execPathCmd := isolatedGit(ctx, gitPath, work, "--exec-path")
	execPathOut, err := runGitCommand(ctx, execPathCmd, work, repo, limits)
	if err != nil {
		return SourceLock{}, fmt.Errorf("locate git execution helpers: %w", err)
	}
	boundedExecPath, err := prepareBoundedGitExec(work, strings.TrimSpace(string(execPathOut)))
	if err != nil {
		return SourceLock{}, err
	}
	command := func(args ...string) *exec.Cmd {
		cmd := isolatedGit(ctx, gitPath, work, args...)
		cmd.Env = append(cmd.Env,
			"GIT_EXEC_PATH="+boundedExecPath,
			"GIT_NO_LAZY_FETCH=1",
			"HIDEOUT_REAL_GIT="+gitPath,
			"HIDEOUT_GIT_OBJECTS="+strconv.Itoa(limits.MaxGitObjects),
			"HIDEOUT_GIT_PACK_BYTES="+strconv.FormatInt(limits.MaxGitPackBytes, 10),
		)
		return cmd
	}

	if out, err := runGitCommand(ctx, command("init", "--bare", "--", repo), work, repo, limits); err != nil {
		return SourceLock{}, fmt.Errorf("initialize bounded git intake: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fetchArgs := []string{
		"-C", repo, "fetch", "--force", "--no-tags", "--no-recurse-submodules", "--depth=1",
		"--no-write-fetch-head", "--no-auto-maintenance",
	}
	if limits.MaxFileBytes != math.MaxInt64 {
		fetchArgs = append(fetchArgs, "--filter=blob:limit="+strconv.FormatInt(overflowSafeReadLimit(limits.MaxFileBytes), 10))
	}
	fetchArgs = append(fetchArgs, "--", url, commit+":refs/hideout/intake")
	if out, err := runGitCommand(ctx, command(fetchArgs...), work, repo, limits); err != nil {
		return SourceLock{}, fmt.Errorf("fetch exact git pack commit: %w: %s", err, strings.TrimSpace(string(out)))
	}

	out, err := runGitCommand(ctx, command("-C", repo, "rev-parse", "--verify", "refs/hideout/intake^{commit}"), work, repo, limits)
	if err != nil {
		return SourceLock{}, fmt.Errorf("resolve exact git pack commit: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(string(out))) != commit {
		return SourceLock{}, errors.New("git source did not resolve to requested commit")
	}
	objects, err := runGitCommand(ctx, command("-C", repo, "rev-list", "--objects", "--missing=print", "--no-object-names", commit), work, repo, limits)
	if err != nil {
		return SourceLock{}, fmt.Errorf("inspect exact git pack objects: %w", err)
	}
	objectNames := strings.Fields(string(objects))
	if len(objectNames) > limits.MaxGitObjects {
		return SourceLock{}, fmt.Errorf("%w: exact commit reaches %d objects, limit %d", ErrGitObjectLimit, len(objectNames), limits.MaxGitObjects)
	}
	for _, line := range objectNames {
		if strings.HasPrefix(line, "?") {
			return SourceLock{}, fmt.Errorf("git pack source contains an object excluded by the %d-byte file limit", limits.MaxFileBytes)
		}
	}

	archivePath := filepath.Join(work, "source.tar")
	if out, err := runGitCommand(ctx, command("-C", repo, "archive", "--format=tar", "--output="+archivePath, commit), work, repo, limits); err != nil {
		return SourceLock{}, fmt.Errorf("archive exact git pack commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return SourceLock{}, err
	}
	extractErr := extractGitArchive(archive, staging, limits)
	closeErr := archive.Close()
	if extractErr != nil {
		return SourceLock{}, extractErr
	}
	if closeErr != nil {
		return SourceLock{}, closeErr
	}
	return SourceLock{Kind: SourceGit, URL: url, Commit: commit}, nil
}

func extractGitArchive(reader io.Reader, dest string, limits Limits) error {
	tr := tar.NewReader(bufio.NewReader(reader))
	files := 0
	entries := 0
	var total int64
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" {
			continue
		}
		if !validRelativePath(name) {
			return fmt.Errorf("git archive path %q escapes source", header.Name)
		}
		entries++
		if entries > limits.MaxEntries {
			return fmt.Errorf("git pack source exceeds %d entries", limits.MaxEntries)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			files++
			total += header.Size
			if files > limits.MaxFiles || header.Size > limits.MaxFileBytes || total > limits.MaxTotalBytes {
				return fmt.Errorf("git pack source exceeds configured limits at %q", name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFileMode(os.FileMode(header.Mode)))
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(out, io.LimitReader(tr, overflowSafeReadLimit(limits.MaxFileBytes)))
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != header.Size {
				return fmt.Errorf("git archive file %q changed length", name)
			}
		default:
			return fmt.Errorf("git archive path %q must be a regular file or directory", name)
		}
	}
}

func isolatedGit(ctx context.Context, executable, home string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.attributesFile=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "credential.interactive=never",
		"-c", "fetch.fsckObjects=true",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "fetch.unpackLimit=1",
		"-c", "fetch.writeCommitGraph=false",
		"-c", "init.templateDir=",
		"-c", "maintenance.auto=false",
		"-c", "protocol.ext.allow=never",
		"-c", "submodule.recurse=false",
	}, args...)...)
	cmd.Env = isolatedGitEnv(home)
	cmd.WaitDelay = time.Second
	return cmd
}

func isolatedGitEnv(home string) []string {
	allowed := []string{"PATH", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "SSH_AUTH_SOCK"}
	env := make([]string, 0, len(allowed)+8)
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env,
		"HOME="+home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=0",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
	return env
}

const boundedGitWrapper = `#!/bin/sh
has_shallow=0
shallow=
if [ "$1" = "--shallow-file" ]; then
	has_shallow=1
	shallow=$2
	shift 2
fi
if [ "$1" != "index-pack" ]; then
	if [ "$has_shallow" -eq 1 ]; then
		exec "$HIDEOUT_REAL_GIT" --shallow-file "$shallow" "$@"
	fi
	exec "$HIDEOUT_REAL_GIT" "$@"
fi
shift
count=
for arg do
	case "$arg" in
		--pack_header=*,*) count=${arg##*,} ;;
	esac
done
case "$count" in
	''|*[!0-9]*)
		echo "hideout: git pack object count unavailable" >&2
		exit 64
		;;
esac
if [ "$count" -gt "$HIDEOUT_GIT_OBJECTS" ]; then
	echo "hideout: git pack object limit exceeded: $count > $HIDEOUT_GIT_OBJECTS" >&2
	exit 65
fi
if [ "$has_shallow" -eq 1 ]; then
	exec "$HIDEOUT_REAL_GIT" --shallow-file "$shallow" index-pack "--max-input-size=$HIDEOUT_GIT_PACK_BYTES" "$@"
fi
exec "$HIDEOUT_REAL_GIT" index-pack "--max-input-size=$HIDEOUT_GIT_PACK_BYTES" "$@"
`

func prepareBoundedGitExec(work, systemExecPath string) (string, error) {
	if systemExecPath == "" || !filepath.IsAbs(systemExecPath) {
		return "", errors.New("git execution helper path is unavailable")
	}
	entries, err := os.ReadDir(systemExecPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(work, "bounded-git-exec")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Name() == "git" {
			continue
		}
		if err := os.Symlink(filepath.Join(systemExecPath, entry.Name()), filepath.Join(dir, entry.Name())); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(boundedGitWrapper), 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

type limitedCommandOutput struct {
	mu        sync.Mutex
	data      []byte
	max       int
	truncated bool
}

func (w *limitedCommandOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.max - len(w.data)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		w.data = append(w.data, data[:remaining]...)
	}
	if remaining < len(data) {
		w.truncated = true
	}
	return len(data), nil
}

func (w *limitedCommandOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]byte(nil), w.data...)
	if w.truncated {
		out = append(out, []byte("\n[git output truncated]")...)
	}
	return out
}

func runGitCommand(ctx context.Context, cmd *exec.Cmd, work, repo string, limits Limits) ([]byte, error) {
	output := &limitedCommandOutput{max: 128 << 10}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return output.Bytes(), ctx.Err()
		}
		return output.Bytes(), err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-wait:
			if limitErr := gitStorageLimitError(work, limits); limitErr != nil {
				return output.Bytes(), limitErr
			}
			if ctx.Err() != nil {
				return output.Bytes(), ctx.Err()
			}
			if err != nil {
				return output.Bytes(), classifyGitCommandError(err, output.Bytes())
			}
			return output.Bytes(), nil
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-wait
			return output.Bytes(), ctx.Err()
		case <-ticker.C:
			if limitErr := gitStorageLimitError(work, limits); limitErr != nil {
				_ = cmd.Process.Kill()
				<-wait
				return output.Bytes(), limitErr
			}
		}
	}
}

func classifyGitCommandError(commandErr error, output []byte) error {
	message := string(output)
	switch {
	case strings.Contains(message, "git pack object limit exceeded"), strings.Contains(message, "git pack object count unavailable"):
		return fmt.Errorf("%w: %v", ErrGitObjectLimit, commandErr)
	case strings.Contains(message, "pack exceeds maximum allowed size"):
		return fmt.Errorf("%w: %v", ErrGitPackLimit, commandErr)
	default:
		return commandErr
	}
}

func gitStorageLimitError(work string, limits Limits) error {
	archivePath := filepath.Join(work, "source.tar")
	if info, err := os.Stat(archivePath); err == nil {
		if info.Size() > limits.MaxGitArchiveBytes {
			return fmt.Errorf("%w: %d > %d", ErrGitArchiveLimit, info.Size(), limits.MaxGitArchiveBytes)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if limits.MaxGitDiskBytes == math.MaxInt64 {
		return nil
	}
	used, err := regularFileBytes(work)
	if err != nil {
		return err
	}
	if used > limits.MaxGitDiskBytes {
		return fmt.Errorf("%w: %d > %d", ErrGitDiskLimit, used, limits.MaxGitDiskBytes)
	}
	return nil
}

func regularFileBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode().IsRegular() {
			if info.Size() > math.MaxInt64-total {
				return ErrGitDiskLimit
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func IsFullCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range strings.ToLower(value) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func normalizeLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	unboundedSource := limits.MaxFiles == math.MaxInt && limits.MaxTotalBytes == math.MaxInt64 && limits.MaxFileBytes == math.MaxInt64
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = limits.MaxFiles
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = defaults.MaxTotalBytes
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxGitObjects <= 0 {
		if unboundedSource {
			limits.MaxGitObjects = math.MaxInt
		} else {
			limits.MaxGitObjects = defaults.MaxGitObjects
		}
	}
	if limits.MaxGitPackBytes <= 0 {
		if unboundedSource {
			limits.MaxGitPackBytes = math.MaxInt64
		} else {
			limits.MaxGitPackBytes = defaults.MaxGitPackBytes
		}
	}
	if limits.MaxGitArchiveBytes <= 0 {
		if unboundedSource {
			limits.MaxGitArchiveBytes = math.MaxInt64
		} else {
			limits.MaxGitArchiveBytes = defaults.MaxGitArchiveBytes
		}
	}
	if limits.MaxGitDiskBytes <= 0 {
		if unboundedSource {
			limits.MaxGitDiskBytes = math.MaxInt64
		} else {
			limits.MaxGitDiskBytes = defaults.MaxGitDiskBytes
		}
	}
	return limits
}

func validateDigestStyle(style string) error {
	switch style {
	case DigestCanonicalV1, DigestCanonicalV2, DigestLegacyCanonicalV1, DigestLegacyPathContentV1:
		return nil
	default:
		return fmt.Errorf("unsupported pack digest style %q", style)
	}
}

func validRelativePath(value string) bool {
	if value == "" || strings.Contains(value, "\x00") || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") {
		return false
	}
	clean := slashpath.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && clean == value
}

func canonicalFileMode(mode os.FileMode) os.FileMode {
	if mode.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func privateFileMode(mode os.FileMode) os.FileMode {
	if mode.Perm()&0o111 != 0 {
		return 0o700
	}
	return 0o600
}

func overflowSafeReadLimit(max int64) int64 {
	if max == math.MaxInt64 {
		return max
	}
	return max + 1
}
