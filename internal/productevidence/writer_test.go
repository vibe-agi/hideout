package productevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileRejectsUnknownFields(t *testing.T) {
	data, err := json.Marshal(validManifest().Sanitized())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	proofs := document["proofs"].([]any)
	proofs[0].(map[string]any)["unexpectedAuthority"] = true
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown product evidence field err=%v", err)
	}
}

func TestReadFileRejectsSelfAttestedRedactionWithRealSecrets(t *testing.T) {
	secrets := []string{
		"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080",
		"cap_0123456789abcdef0123456789abcdef",
		"setupCredential=private-key-material",
		"machineId=0123456789abcdef0123456789abcdef",
	}
	for _, secret := range secrets {
		m := validManifest()
		m.Proofs[0].CommandSummary = "passed scan " + secret
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "secret.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "control-plane") {
			t.Fatalf("self-attested redaction accepted %q: %v", secret, err)
		}
	}
}

func TestWriteFileWritesRedactedManifestAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence", "product-hardening-evidence.json")
	m := validManifest()
	m.Proofs[0].CommandSummary = "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1 keep-me"
	if err := WriteFile(path, m); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "socks5://127.0.0.1:1") || strings.Contains(text, "HIDEOUT_SECRET_DEFAULT_PROXY") {
		t.Fatalf("written manifest leaked control-plane material: %s", text)
	}
	if !strings.Contains(text, "keep-me") {
		t.Fatalf("written manifest removed user data: %s", text)
	}
	loaded, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(loaded.Proofs) != 1 || loaded.Proofs[0].ProofID != Proof021EvidenceSchema {
		t.Fatalf("unexpected loaded manifest: %+v", loaded)
	}
}

func TestWriterAddsProofs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "product-hardening-evidence.json")
	w, err := NewWriter(path, NewManifest("abc123", true))
	if err != nil {
		t.Fatal(err)
	}
	w.AddProof(validManifest().Proofs[0])
	if err := w.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	loaded, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Dirty || len(loaded.Proofs) != 1 {
		t.Fatalf("unexpected manifest: %+v", loaded)
	}
}
