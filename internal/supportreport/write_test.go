package supportreport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicCreatesMode0600AndDoesNotOverwriteByDefault(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	data := []byte("{\"ok\":true}\n")
	if err := WriteAtomic(out, data, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o want=600", info.Mode().Perm())
	}
	if err := WriteAtomic(out, data, false); err == nil {
		t.Fatal("existing destination overwritten without --overwrite")
	}
	if err := WriteAtomic(out, []byte("{\"updated\":true}\n"), true); err != nil {
		t.Fatalf("explicit regular-file overwrite failed: %v", err)
	}
}

func TestWriteAtomicRejectsSymlinkAndUnsafeParent(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "report.json")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := WriteAtomic(link, []byte("{}\n"), true); err == nil {
			t.Fatal("symlink destination accepted")
		}
		data, _ := os.ReadFile(target)
		if string(data) != "keep" {
			t.Fatalf("symlink target changed: %q", data)
		}
	})

	t.Run("unsafe-parent", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0o700)
		if err := WriteAtomic(filepath.Join(dir, "report.json"), []byte("{}\n"), false); err == nil {
			t.Fatal("group/world-writable parent accepted")
		}
	})
}

func TestWriteAtomicFailureLeavesNoFinalOrTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	if err := WriteAtomic(out, make([]byte, MaxBytes+1), false); err == nil {
		t.Fatal("oversized write accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("oversized write left final file: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".report.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("failed write left temporary files: %v", matches)
	}
}
