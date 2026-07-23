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
