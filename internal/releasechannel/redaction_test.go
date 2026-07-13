package releasechannel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceValidationRejectsLocalDeveloperPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proofs", "proof.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"path":"/Users/alice/private/project"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	err = bundleFixture(digest, size).Validate(root, []string{"033.release.package-identity"})
	if err == nil || !strings.Contains(err.Error(), "local absolute path") {
		t.Fatalf("local developer path validation error=%v", err)
	}
}

func TestEvidenceValidationRejectsPrivateCandidatePaths(t *testing.T) {
	for _, localPath := range []string{
		"/private/var/folders/aa/bb/T/hideout-candidate/gate.json",
		"/var/folders/aa/bb/T/hideout-candidate/gate.json",
		"/tmp/hideout-candidate/gate.json",
	} {
		t.Run(strings.ReplaceAll(localPath, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "proofs", "proof.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"path":"`+localPath+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			digest, size, err := FileSHA256(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := bundleFixture(digest, size).Validate(root, []string{"033.release.package-identity"}); err == nil || !strings.Contains(err.Error(), "local absolute path") {
				t.Fatalf("local path validation error=%v", err)
			}
		})
	}
}

func TestEvidenceValidationRejectsSelfDeclaredRedactionDecision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proofs", "proof.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"status":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundleFixture(digest, size)
	bundle.Files[0].RedactionStages = nil
	if err := bundle.Validate(root, []string{"033.release.package-identity"}); err == nil || !strings.Contains(err.Error(), "not authoritative") {
		t.Fatalf("self-declared redaction decision error=%v", err)
	}
}

func TestPublicationReceiptRejectsURLSuffixes(t *testing.T) {
	_, release := releaseFixture(t)
	receipt := receiptFixture(release)
	for _, suffix := range []string{
		"/Users/alice/private/project",
		"?token=cap_0123456789abcdef0123456789abcdef",
	} {
		mutated := receipt
		mutated.URL += suffix
		if err := mutated.Validate(release); err == nil {
			t.Fatalf("receipt URL suffix %q unexpectedly passed", suffix)
		}
	}
}
