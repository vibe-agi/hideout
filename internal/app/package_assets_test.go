package app

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/vibe-agi/hideout/internal/packagekit"
)

func TestPackageEmbeddedAssetsReportsCurrentExecutableAndStaysOutOfUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Main(
		[]string{"package", "embedded-assets"},
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var manifest packagekit.EmbeddedAssetManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if err := packagekit.ValidateEmbeddedAssetManifest(manifest); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := packagekit.FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ContainerSHA256 != want {
		t.Fatalf(
			"containerSHA256=%q want %q",
			manifest.ContainerSHA256,
			want,
		)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Main([]string{"package", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d stderr=%s", code, stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("embedded-assets")) {
		t.Fatal("internal packaging command leaked into operator help")
	}
}
