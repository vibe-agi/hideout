package releasechannel

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

const maxPackageUncompressedBytes int64 = 512 << 20

func ExtractPackageArchive(archivePath, destination string) (string, error) {
	f, err := os.Open(filepath.Clean(archivePath))
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open package gzip: %w", err)
	}
	defer gz.Close()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read package tar: %w", err)
		}
		name := path.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || (name != "hideout" && !strings.HasPrefix(name, "hideout/")) {
			return "", fmt.Errorf("unsafe package entry %q", hdr.Name)
		}
		if name == "hideout" && hdr.Typeflag != tar.TypeDir {
			return "", errors.New("top-level hideout package entry must be a directory")
		}
		if seen[name] {
			return "", fmt.Errorf("duplicate package entry %q", name)
		}
		seen[name] = true
		rel := filepath.FromSlash(name)
		target := filepath.Join(destination, rel)
		if !strings.HasPrefix(target, filepath.Clean(destination)+string(filepath.Separator)) {
			return "", fmt.Errorf("package entry %q escapes destination", name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 || total+hdr.Size > maxPackageUncompressedBytes {
				return "", errors.New("package archive exceeds uncompressed size limit")
			}
			total += hdr.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			mode := os.FileMode(0o644)
			if hdr.FileInfo().Mode()&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return "", err
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil {
				out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("package entry %q uses unsupported tar type %d", name, hdr.Typeflag)
		}
	}
	root := filepath.Join(destination, "hideout")
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", errors.New("package archive is missing top-level hideout directory")
	}
	return root, nil
}

func ArchiveEvidenceBundle(root, archivePath string) error {
	if _, err := ValidateEvidenceBundleRoot(root, productevidence.RequiredProofIDsForTarget(productevidence.RequiredForReleaseCandidate)); err != nil {
		return err
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence archive path %q is a symlink", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("evidence archive path %q is not regular", path)
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(archivePath)), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filepath.Clean(archivePath)), ".evidence-archive-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	gz := gzip.NewWriter(tmp)
	gz.Header.ModTime = time.Unix(0, 0).UTC()
	tw := tar.NewWriter(gz)
	for _, source := range paths {
		rel, err := filepath.Rel(root, source)
		if err != nil {
			return closeArchiveWriters(tw, gz, tmp, err)
		}
		info, err := os.Lstat(source)
		if err != nil {
			return closeArchiveWriters(tw, gz, tmp, err)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return closeArchiveWriters(tw, gz, tmp, err)
		}
		header.Name = filepath.ToSlash(rel)
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		header.ModTime, header.AccessTime, header.ChangeTime = time.Unix(0, 0).UTC(), time.Time{}, time.Time{}
		if info.IsDir() {
			header.Mode = 0o755
		} else {
			header.Mode = 0o644
		}
		if err := tw.WriteHeader(header); err != nil {
			return closeArchiveWriters(tw, gz, tmp, err)
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(source)
			if err != nil {
				return closeArchiveWriters(tw, gz, tmp, err)
			}
			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return closeArchiveWriters(tw, gz, tmp, copyErr)
			}
			if closeErr != nil {
				return closeArchiveWriters(tw, gz, tmp, closeErr)
			}
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		tmp.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Clean(archivePath))
}

func ExtractEvidenceArchive(archivePath, destination string) error {
	f, err := os.Open(filepath.Clean(archivePath))
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := path.Clean(header.Name)
		if name == "." || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || seen[name] {
			return fmt.Errorf("unsafe or duplicate evidence entry %q", header.Name)
		}
		seen[name] = true
		target := filepath.Join(destination, filepath.FromSlash(name))
		if !strings.HasPrefix(target, filepath.Clean(destination)+string(filepath.Separator)) {
			return fmt.Errorf("evidence entry %q escapes destination", name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size <= 0 || total+header.Size > MaxEvidenceBundleBytes {
				return errors.New("evidence archive exceeds size limit or contains an empty file")
			}
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, tr, header.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("evidence entry %q uses unsupported tar type %d", name, header.Typeflag)
		}
	}
	return nil
}

func closeArchiveWriters(tw *tar.Writer, gz *gzip.Writer, file *os.File, cause error) error {
	_ = tw.Close()
	_ = gz.Close()
	_ = file.Close()
	return cause
}
