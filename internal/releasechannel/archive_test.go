package releasechannel

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPackageArchiveAcceptsExplicitHideoutRootDirectory(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "package.tar.gz")
	writePackageArchive(t, archive, []tar.Header{
		{Name: "hideout/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "hideout/bin/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "hideout/bin/hideout", Typeflag: tar.TypeReg, Mode: 0o755, Size: 1},
	})
	root, err := ExtractPackageArchive(archive, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "hideout")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPackageArchiveRejectsNonDirectoryHideoutRoot(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "package.tar.gz")
	writePackageArchive(t, archive, []tar.Header{
		{Name: "hideout", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
	})
	if _, err := ExtractPackageArchive(archive, t.TempDir()); err == nil {
		t.Fatal("non-directory package root unexpectedly passed")
	}
}

func TestExtractPackageArchiveRejectsOtherTopLevelDirectory(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "package.tar.gz")
	writePackageArchive(t, archive, []tar.Header{
		{Name: "other/", Typeflag: tar.TypeDir, Mode: 0o755},
	})
	if _, err := ExtractPackageArchive(archive, t.TempDir()); err == nil {
		t.Fatal("unexpected package root passed")
	}
}

func writePackageArchive(t *testing.T, path string, headers []tar.Header) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for i := range headers {
		header := headers[i]
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size > 0 {
			if _, err := tw.Write(make([]byte, header.Size)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
