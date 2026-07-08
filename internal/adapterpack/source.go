package adapterpack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	slashpath "path"
	"path/filepath"
	"sort"
	"strings"
)

func LockSource(root string, spec SourceSpec, dest string) (Manifest, Revision, error) {
	spec.Kind = strings.TrimSpace(spec.Kind)
	if spec.Kind == "" {
		spec.Kind = SourceLocal
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".adapter-pack-*")
	if err != nil {
		return Manifest{}, Revision{}, err
	}
	defer os.RemoveAll(tmp)
	var lock SourceLock
	switch spec.Kind {
	case SourceLocal:
		if strings.TrimSpace(spec.Path) == "" {
			return Manifest{}, Revision{}, errors.New("local adapter pack path is required")
		}
		src, err := filepath.Abs(spec.Path)
		if err != nil {
			return Manifest{}, Revision{}, err
		}
		if err := copyTree(src, tmp); err != nil {
			return Manifest{}, Revision{}, err
		}
		lock = SourceLock{Kind: SourceLocal, Path: filepath.Clean(src)}
	case SourceGit:
		if !isFullCommit(spec.Commit) {
			return Manifest{}, Revision{}, fmt.Errorf("git source commit %q must be a full 40-hex commit", spec.Commit)
		}
		if strings.TrimSpace(spec.URL) == "" {
			return Manifest{}, Revision{}, errors.New("git source url is required")
		}
		if err := cloneExactCommit(root, spec.URL, spec.Commit, tmp); err != nil {
			return Manifest{}, Revision{}, err
		}
		lock = SourceLock{Kind: SourceGit, URL: spec.URL, Commit: strings.ToLower(spec.Commit)}
	default:
		return Manifest{}, Revision{}, fmt.Errorf("unsupported adapter pack source kind %q", spec.Kind)
	}
	manifestPath := filepath.Join(tmp, ManifestFileName)
	manifest, manifestBytes, err := LoadManifest(manifestPath)
	if err != nil {
		return Manifest{}, Revision{}, fmt.Errorf("load adapter pack manifest: %w", err)
	}
	manifestDigest := DigestBytes(manifestBytes)
	sourceDigest, err := DigestTree(tmp)
	if err != nil {
		return Manifest{}, Revision{}, err
	}
	adapterDigests, err := adapterDigests(tmp, manifest)
	if err != nil {
		return Manifest{}, Revision{}, err
	}
	rev := Revision{
		RevisionID:       RevisionID(sourceDigest),
		Version:          manifest.Version,
		Source:           lock,
		ManifestDigest:   manifestDigest,
		SourceDigest:     sourceDigest,
		AdapterDigests:   adapterDigests,
		ValidationStatus: "passed",
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return Manifest{}, Revision{}, err
	}
	_ = os.RemoveAll(dest)
	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, Revision{}, err
	}
	return manifest, rev, nil
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

func TestResultID(packID, revisionID string) string {
	key := packID + ":" + revisionID
	sum := sha256.Sum256([]byte(key))
	return "test_" + hex.EncodeToString(sum[:8])
}

func DigestTree(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if name == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("adapter pack path %q must be a regular file or directory", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func adapterDigests(root string, manifest Manifest) (map[string]string, error) {
	out := make(map[string]string, len(manifest.Adapters))
	for _, adapter := range manifest.Adapters {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(adapter.Script)))
		if err != nil {
			return nil, err
		}
		out[adapter.ID] = DigestBytes(data)
	}
	return out, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if name == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(filepath.ToSlash(rel), "../") || slashpath.Clean(filepath.ToSlash(rel)) == ".." {
			return fmt.Errorf("adapter pack path %q escapes source", path)
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("adapter pack path %q must not be a symlink", path)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("adapter pack path %q must be a regular file", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

func cloneExactCommit(root, url, commit, dst string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("system git is required for git adapter pack sources")
	}
	tmp, err := os.MkdirTemp(root, ".adapter-pack-git-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	clone := gitCommand("clone", "--no-checkout", "--no-recurse-submodules", "--", url, tmp)
	if out, err := clone.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone adapter pack source: %w: %s", err, strings.TrimSpace(string(out)))
	}
	checkout := gitCommand("-C", tmp, "checkout", "--detach", strings.ToLower(commit))
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout adapter pack commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	head := gitCommand("-C", tmp, "rev-parse", "HEAD")
	out, err := head.Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.ToLower(string(out))) != strings.ToLower(commit) {
		return errors.New("git checkout did not resolve to requested commit")
	}
	return copyTree(tmp, dst)
}

func gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{
		"-c", "core.hooksPath=/dev/null",
	}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	return cmd
}

func isFullCommit(value string) bool {
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
