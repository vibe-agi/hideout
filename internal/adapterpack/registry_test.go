package adapterpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryAtomicWriteAndSchemaValidation(t *testing.T) {
	store := NewStore(t.TempDir())
	reg := Registry{SchemaVersion: RegistryVersion, Packs: map[string]RegistryEntry{}}
	if err := store.Save(reg); err != nil {
		t.Fatalf("save registry: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if loaded.SchemaVersion != RegistryVersion || loaded.Packs == nil {
		t.Fatalf("unexpected registry: %+v", loaded)
	}
	data, err := os.ReadFile(store.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("registry is not JSON: %v", err)
	}
}

func TestRegistryWriteFailureLeavesExistingRegistry(t *testing.T) {
	store := NewStore(t.TempDir())
	initial := Registry{SchemaVersion: RegistryVersion, Packs: map[string]RegistryEntry{
		"example.pack": {
			PackID:           "example.pack",
			State:            StateInstalled,
			ActiveRevisionID: "rev_initial",
			Revisions: map[string]Revision{"rev_initial": {
				RevisionID:       "rev_initial",
				Version:          "1.0.0",
				SourceDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				ManifestDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				ValidationStatus: "passed",
			}},
		},
	}}
	if err := store.Save(initial); err != nil {
		t.Fatalf("save initial registry: %v", err)
	}
	if err := os.Chmod(store.Root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store.Root, 0o700) })
	next := Registry{SchemaVersion: RegistryVersion, Packs: map[string]RegistryEntry{
		"other.pack": {
			PackID:           "other.pack",
			State:            StateInstalled,
			ActiveRevisionID: "rev_other",
			Revisions: map[string]Revision{"rev_other": {
				RevisionID:       "rev_other",
				Version:          "1.0.0",
				SourceDigest:     "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				ManifestDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
				ValidationStatus: "passed",
			}},
		},
	}}
	if err := store.Save(next); err == nil {
		t.Fatal("expected registry write failure")
	}
	data, err := os.ReadFile(store.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("existing registry became invalid JSON: %s", data)
	}
	var got Registry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Packs["example.pack"]; !ok {
		t.Fatalf("initial registry entry was lost: %+v", got.Packs)
	}
	if _, ok := got.Packs["other.pack"]; ok {
		t.Fatalf("failed write partially replaced registry: %+v", got.Packs)
	}
}

func TestResolveRuntimeFailsClosedOnDigestDriftAndRevoke(t *testing.T) {
	src := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }")
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveRuntime(entry.PackID, rev.RevisionID, "tool", rev.SourceDigest); err != nil {
		t.Fatalf("resolve clean runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SourceDir(entry.PackID, rev.RevisionID), "adapter.js"), []byte("function decideCommandAdapter(){return {outcome:'deny',reason:'changed'}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveRuntime(entry.PackID, rev.RevisionID, "tool", rev.SourceDigest); err == nil {
		t.Fatal("expected digest drift rejection")
	}
}

func TestBuiltInRegistryMutationIsRejectedByValidation(t *testing.T) {
	reg := Registry{SchemaVersion: RegistryVersion, Packs: map[string]RegistryEntry{
		"root-sensitive": {PackID: "not-root-sensitive", State: StateBuiltin, BuiltIn: true},
	}}
	if err := ValidateRegistry(reg); err == nil {
		t.Fatal("expected key/packId mismatch rejection")
	}
}
