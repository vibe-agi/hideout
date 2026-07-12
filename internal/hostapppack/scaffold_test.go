package hostapppack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldIsDeterministicAndStrictlyValid(t *testing.T) {
	request := ScaffoldRequest{
		PackID: "community.zed", AppID: "zed", Command: "zed",
		BundleName: "Zed.app", ExecutableRelativePath: "Contents/MacOS/zed",
	}
	var first []byte
	for i := 0; i < 2; i++ {
		dir := filepath.Join(t.TempDir(), "pack")
		request.Directory = dir
		if err := Scaffold(request); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeManifest(raw); err != nil {
			t.Fatalf("scaffold manifest is invalid: %v", err)
		}
		if i == 0 {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatalf("scaffold output is not deterministic:\n%s\n%s", first, raw)
		}
	}
}

func TestScaffoldRefusesNonEmptyOrSymlinkedDestination(t *testing.T) {
	request := ScaffoldRequest{
		PackID: "community.zed", AppID: "zed", Command: "zed",
		BundleName: "Zed.app", ExecutableRelativePath: "Contents/MacOS/zed",
	}
	nonEmpty := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.Directory = nonEmpty
	if err := Scaffold(request); err == nil {
		t.Fatal("scaffold overwrote a non-empty directory")
	}
	if data, _ := os.ReadFile(filepath.Join(nonEmpty, "keep")); string(data) != "keep" {
		t.Fatal("scaffold changed an existing file")
	}

	root := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(root, "pack")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	request.Directory = link
	if err := Scaffold(request); err == nil {
		t.Fatal("scaffold followed a destination symlink")
	}
}
