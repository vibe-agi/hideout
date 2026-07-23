package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestObserveProjectionReadinessValidatesExactSessionCatalog(t *testing.T) {
	root := t.TempDir()
	shims := filepath.Join(root, "shims")
	if err := os.Mkdir(shims, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := make([]backend.ProjectionReadinessEntry, 0, 2)
	for _, item := range []struct {
		name string
		kind backend.ProjectionReadinessEntryKind
	}{
		{name: "code", kind: backend.ProjectionEntryCommand},
		{name: "hideout-shim", kind: backend.ProjectionEntryDispatcher},
	} {
		data := []byte("#!/bin/sh\nexit 0\n" + item.name)
		if err := os.WriteFile(filepath.Join(shims, item.name), data, 0o700); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		entries = append(entries, backend.ProjectionReadinessEntry{
			Name: item.name, RelativePath: item.name,
			SHA256: "sha256:" + hex.EncodeToString(sum[:]), Kind: item.kind,
		})
	}
	manifest := backend.ProjectionReadinessManifest{
		Schema: backend.ProjectionReadinessManifestSchema, SessionID: "ses_ready",
		EnvironmentID: "env_ready", SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
		Entries: entries,
	}
	var err error
	manifest.CatalogDigest, err = backend.ProjectionReadinessCatalogDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, backend.ProjectionReadinessManifestFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	expectation := projectionReadinessSpec{
		EnvironmentID: manifest.EnvironmentID, SessionSnapshotID: manifest.SessionSnapshotID,
		CatalogDigest: manifest.CatalogDigest, ExpectedEntries: len(entries), TargetProjected: true,
	}
	observation, err := observeProjectionReadiness(root, "ses_ready", expectation)
	if err != nil {
		t.Fatal(err)
	}
	if observation.CatalogDigest != manifest.CatalogDigest ||
		observation.ExpectedEntries != len(entries) || observation.ObservedEntries != len(entries) ||
		!observation.TargetProjected {
		t.Fatalf("observation=%+v", observation)
	}

	if err := os.Remove(filepath.Join(shims, "code")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("hideout-shim", filepath.Join(shims, "code")); err != nil {
		t.Fatal(err)
	}
	if _, err := observeProjectionReadiness(root, "ses_ready", expectation); readinessReason(err) != string(backend.ProjectionReadinessEntryInvalid) {
		t.Fatalf("symlink reason=%q err=%v", readinessReason(err), err)
	}
}

func TestObserveProjectionReadinessRejectsDigestAndIdentityDrift(t *testing.T) {
	root, expectation := projectionReadinessFixture(t)
	expectation.CatalogDigest = "sha256:" + strings.Repeat("e", 64)
	if _, err := observeProjectionReadiness(root, "ses_ready", expectation); readinessReason(err) != string(backend.ProjectionReadinessCatalogDrift) {
		t.Fatalf("catalog reason=%q err=%v", readinessReason(err), err)
	}
	root, expectation = projectionReadinessFixture(t)
	if err := os.WriteFile(filepath.Join(root, "shims", "hideout-shim"), []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := observeProjectionReadiness(root, "ses_ready", expectation); readinessReason(err) != string(backend.ProjectionReadinessDigestMismatch) {
		t.Fatalf("digest reason=%q err=%v", readinessReason(err), err)
	}
}

func TestObserveProjectionReadinessClassifiesMissingInvalidAndForeignState(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, root string, expectation *projectionReadinessSpec)
		reason backend.ProjectionReadinessReason
	}{
		{
			name: "missing manifest",
			mutate: func(t *testing.T, root string, _ *projectionReadinessSpec) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, backend.ProjectionReadinessManifestFile)); err != nil {
					t.Fatal(err)
				}
			},
			reason: backend.ProjectionReadinessManifestMissing,
		},
		{
			name: "missing entry",
			mutate: func(t *testing.T, root string, _ *projectionReadinessSpec) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "shims", "hideout-shim")); err != nil {
					t.Fatal(err)
				}
			},
			reason: backend.ProjectionReadinessEntryMissing,
		},
		{
			name: "non executable entry",
			mutate: func(t *testing.T, root string, _ *projectionReadinessSpec) {
				t.Helper()
				if err := os.Chmod(filepath.Join(root, "shims", "hideout-shim"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			reason: backend.ProjectionReadinessEntryInvalid,
		},
		{
			name: "foreign environment",
			mutate: func(_ *testing.T, _ string, expectation *projectionReadinessSpec) {
				expectation.EnvironmentID = "env_foreign"
			},
			reason: backend.ProjectionReadinessIdentityDrift,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, expectation := projectionReadinessFixture(t)
			test.mutate(t, root, &expectation)
			_, err := observeProjectionReadiness(root, "ses_ready", expectation)
			if got := readinessReason(err); got != string(test.reason) {
				t.Fatalf("reason=%q want=%q err=%v", got, test.reason, err)
			}
		})
	}

	root, expectation := projectionReadinessFixture(t)
	if _, err := observeProjectionReadiness(root, "ses_foreign", expectation); readinessReason(err) != string(backend.ProjectionReadinessIdentityDrift) {
		t.Fatalf("foreign session reason=%q err=%v", readinessReason(err), err)
	}

	foreignRoot, foreignExpectation := projectionReadinessFixture(t)
	foreignExpectation.TargetProjected = true
	if expectation.CatalogDigest != foreignExpectation.CatalogDigest {
		t.Fatal("identical fixture unexpectedly changed catalog identity")
	}
	if err := os.WriteFile(
		filepath.Join(foreignRoot, "shims", "hideout-shim"),
		[]byte("foreign-dispatcher"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := observeProjectionReadiness(foreignRoot, "ses_ready", foreignExpectation); readinessReason(err) != string(backend.ProjectionReadinessDigestMismatch) {
		t.Fatalf("foreign bytes reason=%q err=%v", readinessReason(err), err)
	}
}

func projectionReadinessFixture(t *testing.T) (string, projectionReadinessSpec) {
	t.Helper()
	root := t.TempDir()
	shims := filepath.Join(root, "shims")
	if err := os.Mkdir(shims, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte("dispatcher")
	if err := os.WriteFile(filepath.Join(shims, "hideout-shim"), data, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	manifest := backend.ProjectionReadinessManifest{
		Schema: backend.ProjectionReadinessManifestSchema, SessionID: "ses_ready",
		EnvironmentID: "env_ready", SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
		Entries: []backend.ProjectionReadinessEntry{{
			Name: "hideout-shim", RelativePath: "hideout-shim",
			SHA256: "sha256:" + hex.EncodeToString(sum[:]), Kind: backend.ProjectionEntryDispatcher,
		}},
	}
	var err error
	manifest.CatalogDigest, err = backend.ProjectionReadinessCatalogDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, backend.ProjectionReadinessManifestFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, projectionReadinessSpec{
		EnvironmentID: manifest.EnvironmentID, SessionSnapshotID: manifest.SessionSnapshotID,
		CatalogDigest: manifest.CatalogDigest, ExpectedEntries: 1,
	}
}
