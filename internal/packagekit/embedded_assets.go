package packagekit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

var browserConsoleAssets = []EmbeddedAsset{
	{Path: "index.html", MediaType: "text/html; charset=utf-8"},
	{Path: "style.css", MediaType: "text/css; charset=utf-8"},
	{Path: "state.js", MediaType: "text/javascript; charset=utf-8"},
	{Path: "client.js", MediaType: "text/javascript; charset=utf-8"},
	{Path: "activity.js", MediaType: "text/javascript; charset=utf-8"},
	{Path: "config.js", MediaType: "text/javascript; charset=utf-8"},
	{Path: "presentation.js", MediaType: "text/javascript; charset=utf-8"},
	{Path: "app.js", MediaType: "text/javascript; charset=utf-8"},
}

func BrowserConsoleAssets() []EmbeddedAsset {
	return append([]EmbeddedAsset(nil), browserConsoleAssets...)
}

func BytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ValidateEmbeddedAssetManifest(manifest EmbeddedAssetManifest) error {
	if manifest.Schema != EmbeddedAssetManifestSchema {
		return fmt.Errorf("unsupported embedded asset manifest schema %q", manifest.Schema)
	}
	if manifest.ID != BrowserConsoleAssetID {
		return fmt.Errorf("unsupported embedded asset id %q", manifest.ID)
	}
	if manifest.Container != BrowserConsoleContainerPath {
		return fmt.Errorf("embedded asset container must be %q", BrowserConsoleContainerPath)
	}
	if !sha256RE.MatchString(manifest.ContainerSHA256) {
		return errors.New("embedded asset containerSHA256 is invalid")
	}
	if manifest.License != BrowserConsoleAssetLicense {
		return fmt.Errorf("embedded asset license must be %q", BrowserConsoleAssetLicense)
	}
	if len(manifest.Assets) != len(browserConsoleAssets) {
		return fmt.Errorf(
			"embedded browser console requires exactly %d assets",
			len(browserConsoleAssets),
		)
	}
	seen := make(map[string]struct{}, len(manifest.Assets))
	for index, asset := range manifest.Assets {
		expected := browserConsoleAssets[index]
		clean, err := CleanRelative(asset.Path)
		if err != nil || clean != asset.Path || path.Dir(clean) != "." {
			return fmt.Errorf("embedded asset path %q is unsafe", asset.Path)
		}
		if _, ok := seen[asset.Path]; ok {
			return fmt.Errorf("embedded asset path %q is duplicated", asset.Path)
		}
		seen[asset.Path] = struct{}{}
		if asset.Path != expected.Path || asset.MediaType != expected.MediaType {
			return fmt.Errorf(
				"embedded asset at index %d must be %q with media type %q",
				index,
				expected.Path,
				expected.MediaType,
			)
		}
		if !sha256RE.MatchString(asset.SHA256) {
			return fmt.Errorf("embedded asset %q has invalid sha256", asset.Path)
		}
	}
	return nil
}

func LoadEmbeddedAssetManifest(manifestPath string) (EmbeddedAssetManifest, error) {
	file, err := os.Open(manifestPath)
	if err != nil {
		return EmbeddedAssetManifest{}, fmt.Errorf("open embedded asset manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest EmbeddedAssetManifest
	if err := decoder.Decode(&manifest); err != nil {
		return EmbeddedAssetManifest{}, fmt.Errorf("parse embedded asset manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return EmbeddedAssetManifest{}, fmt.Errorf("parse embedded asset manifest: %w", err)
	}
	if err := ValidateEmbeddedAssetManifest(manifest); err != nil {
		return EmbeddedAssetManifest{}, err
	}
	return manifest, nil
}

func validateEmbeddedAssetBindings(bindings []EmbeddedAssetBinding) error {
	if len(bindings) != 1 {
		return errors.New("package manifest requires exactly one embedded browser console binding")
	}
	binding := bindings[0]
	if binding.ID != BrowserConsoleAssetID ||
		binding.Container != BrowserConsoleContainerPath ||
		binding.Manifest != BrowserConsoleManifestPath ||
		binding.License != BrowserConsoleAssetLicense {
		return errors.New("package manifest embedded browser console binding identity is invalid")
	}
	if !sha256RE.MatchString(binding.ManifestSHA256) {
		return errors.New("package manifest embedded browser console manifestSHA256 is invalid")
	}
	for label, value := range map[string]string{
		"container": binding.Container,
		"manifest":  binding.Manifest,
	} {
		clean, err := CleanRelative(value)
		if err != nil || clean != value || strings.TrimSpace(value) == "" {
			return fmt.Errorf("package manifest embedded asset %s path is invalid", label)
		}
	}
	return nil
}

func verifyEmbeddedBrowserConsole(
	root string,
	files []File,
	bindings []EmbeddedAssetBinding,
	installed bool,
) error {
	manifestRel := BrowserConsoleManifestPath
	if installed {
		var err error
		manifestRel, err = installPathForArtifact(manifestRel)
		if err != nil {
			return err
		}
	} else if err := validateEmbeddedAssetBindings(bindings); err != nil {
		return err
	}
	indexed := make(map[string]File, len(files))
	for _, file := range files {
		indexed[file.Path] = file
	}
	containerFile, containerOK := indexed[BrowserConsoleContainerPath]
	if !containerOK || containerFile.Kind != "binary" || !containerFile.Executable {
		return fmt.Errorf(
			"package embedded browser console requires executable container %q",
			BrowserConsoleContainerPath,
		)
	}
	assetFile, assetOK := indexed[manifestRel]
	if !assetOK || assetFile.Kind != "embedded-asset-manifest" || assetFile.Executable {
		return fmt.Errorf(
			"package embedded browser console requires manifest %q",
			manifestRel,
		)
	}
	if !installed && bindings[0].ManifestSHA256 != assetFile.SHA256 {
		return errors.New("package embedded browser console binding digest does not match file inventory")
	}
	manifestPath, err := JoinRelative(root, manifestRel)
	if err != nil {
		return err
	}
	manifest, err := LoadEmbeddedAssetManifest(manifestPath)
	if err != nil {
		return err
	}
	containerPath, err := JoinRelative(root, BrowserConsoleContainerPath)
	if err != nil {
		return err
	}
	containerSHA256, err := FileSHA256(containerPath)
	if err != nil {
		return fmt.Errorf("hash embedded asset container: %w", err)
	}
	if manifest.ContainerSHA256 != containerSHA256 ||
		containerFile.SHA256 != containerSHA256 {
		return errors.New("embedded browser console container digest mismatch")
	}
	return nil
}
