package adapterpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEvidenceRedactsControlPlaneFields(t *testing.T) {
	event := Evidence("install", "allow", RegistryEntry{PackID: "example.pack", State: StateInstalled}, Revision{
		RevisionID:     "rev_abc",
		Version:        "1.0.0",
		ManifestDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}, "cap_0123456789abcdef0123456789abcdef")
	got := RedactedDetails(event.Details)
	if got["reason"] == "cap_0123456789abcdef0123456789abcdef" {
		t.Fatalf("control-plane token leaked: %+v", got)
	}
	if got["packId"] != "example.pack" {
		t.Fatalf("pack identity should remain visible: %+v", got)
	}
}

func writePackManifest(t *testing.T, dir string, manifest Manifest) {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
