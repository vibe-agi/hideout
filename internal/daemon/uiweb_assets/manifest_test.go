package uiweb_assets

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/packagekit"
)

func TestEmbeddedManifestBindsEveryCompiledAsset(t *testing.T) {
	containerSHA256 := strings.Repeat("a", 64)
	manifest, err := EmbeddedManifest(containerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := packagekit.ValidateEmbeddedAssetManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ContainerSHA256 != containerSHA256 {
		t.Fatalf(
			"containerSHA256=%q want %q",
			manifest.ContainerSHA256,
			containerSHA256,
		)
	}
	for _, asset := range manifest.Assets {
		data, err := files.ReadFile(asset.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got := packagekit.BytesSHA256(data); got != asset.SHA256 {
			t.Fatalf(
				"asset %s digest=%q want %q",
				asset.Path,
				asset.SHA256,
				got,
			)
		}
	}
}
