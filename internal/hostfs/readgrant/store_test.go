package readgrant

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestManifestRoundTripIsStrictPrivateAndSchemaValid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "ses_test", "hostfs-read")
	store, err := NewStore(dir, "ses_test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	manifest := Manifest{
		Version:    GrantVersion,
		SessionID:  "ses_test",
		Generation: 1,
		UpdatedAt:  now,
		Grants: []Grant{{
			DecisionID:       "dec_hfr_0123",
			Revision:         1,
			Operation:        OperationRead,
			RequestedPath:    "/Users/alice/Documents/report.txt",
			CanonicalPath:    "/Users/alice/Documents/report.txt",
			VisibilityRuleID: "hfs_see",
			VisibilitySource: "profile",
			IssuedAt:         now,
			ExpiresAt:        now.Add(24 * time.Hour),
		}},
	}
	if err := store.WithExclusive(func() error { return store.SaveManifest(manifest) }); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	var loaded Manifest
	if err := store.WithShared(func() error {
		var loadErr error
		loaded, loadErr = store.LoadManifest()
		return loadErr
	}); err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if loaded.Generation != 1 || len(loaded.Grants) != 1 || loaded.Grants[0].CanonicalPath != manifest.Grants[0].CanonicalPath {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	info, err := os.Stat(store.GrantsPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("grant mode=%#o want 0600", info.Mode().Perm())
	}
	validateGrantSchema(t, store.GrantsPath())

	data, err := os.ReadFile(store.GrantsPath())
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"generation": 1`), []byte(`"generation": 1, "unknown": true`), 1)
	if err := os.WriteFile(store.GrantsPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadManifest(); err == nil {
		t.Fatal("unknown manifest field should fail closed")
	}
}

func TestOpenStoreNeverCreatesMissingSessionAuthority(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "ses_test", "hostfs-read")
	created, err := NewStore(dir, "ses_test")
	if err != nil {
		t.Fatal(err)
	}
	if err := created.WithExclusive(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenStore(dir, "ses_test")
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.WithShared(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir, "ses_test"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenStore missing dir err=%v want not-exist", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenStore recreated missing authority: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir, "ses_test"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenStore missing provider lock err=%v want not-exist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "provider.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenStore recreated provider lock: %v", err)
	}
}

func TestManifestValidationRejectsAuthorityDrift(t *testing.T) {
	now := time.Now().UTC()
	base := Manifest{
		Version:    GrantVersion,
		SessionID:  "ses_test",
		Generation: 1,
		UpdatedAt:  now,
		Grants: []Grant{{
			DecisionID:       "dec_hfr_0123",
			Revision:         1,
			Operation:        OperationRead,
			RequestedPath:    "/Users/alice/report.txt",
			CanonicalPath:    "/Users/alice/report.txt",
			VisibilityRuleID: "hfs_see",
			VisibilitySource: "profile",
			IssuedAt:         now,
			ExpiresAt:        now.Add(time.Hour),
		}},
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"wrong session", func(m *Manifest) { m.SessionID = "ses_other" }},
		{"zero generation", func(m *Manifest) { m.Generation = 0 }},
		{"relative path", func(m *Manifest) { m.Grants[0].CanonicalPath = "relative" }},
		{"wrong operation", func(m *Manifest) { m.Grants[0].Operation = "list" }},
		{"too durable", func(m *Manifest) { m.Grants[0].ExpiresAt = m.Grants[0].IssuedAt.Add(24*time.Hour + time.Second) }},
		{"expiry before issue", func(m *Manifest) { m.Grants[0].ExpiresAt = m.Grants[0].IssuedAt.Add(-time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := base
			candidate.Grants = append([]Grant(nil), base.Grants...)
			tt.mutate(&candidate)
			if err := candidate.Validate("ses_test"); err == nil {
				t.Fatalf("expected invalid manifest: %+v", candidate)
			}
		})
	}
}

func TestFindActiveGrantRequiresExactCanonicalPathAndFreshExpiry(t *testing.T) {
	now := time.Now().UTC()
	manifest := Manifest{Version: GrantVersion, SessionID: "ses_test", Generation: 1, UpdatedAt: now, Grants: []Grant{{
		DecisionID: "dec_hfr_0123", Revision: 1, Operation: OperationRead,
		RequestedPath: "/Users/alice/link", CanonicalPath: "/Users/alice/a.txt",
		VisibilityRuleID: "hfs_see", VisibilitySource: "profile", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}}}
	grant, ok := manifest.FindActive(OperationRead, "/Users/alice/link", "/Users/alice/a.txt", now)
	if !ok || grant.DecisionID != "dec_hfr_0123" {
		t.Fatalf("active grant not found: %+v ok=%t", grant, ok)
	}
	for _, canonical := range []string{"/Users/alice/b.txt", "/Users/alice/a.txt/child"} {
		if _, ok := manifest.FindActive(OperationRead, "/Users/alice/link", canonical, now); ok {
			t.Fatalf("retargeted canonical path %q inherited grant", canonical)
		}
	}
	if _, ok := manifest.FindActive(OperationRead, "/Users/alice/link", "/Users/alice/a.txt", now.Add(2*time.Hour)); ok {
		t.Fatal("expired grant remained active")
	}
}

func TestProviderStateRoundTripRequiresCompleteTrustedProposalSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "ses_test", "hostfs-read")
	store, err := NewStore(dir, "ses_test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	state := ProviderState{
		Version:   ProviderVersion,
		SessionID: "ses_test",
		Requests: map[string]RequestRecord{
			"abc": {
				KeyDigest:        "abc",
				DecisionID:       "dec_hfr_abc",
				RequestedPath:    "/Users/alice/Documents/report.txt",
				CanonicalPath:    "/Users/alice/Documents/report.txt",
				Operation:        OperationRead,
				Profile:          "default",
				Backend:          "lima",
				VisibilityRule:   "hfs_see_documents",
				VisibilitySource: "profile",
				VisibilityRoot:   "/Users/alice/Documents",
				VisibilityScope:  "recursive-dir",
				UntrustedReason:  "inspect referenced report",
				State:            "pending",
				Revision:         1,
				CreatedAt:        now,
				TimeoutAt:        now.Add(5 * time.Minute),
			},
		},
		CreationTimes: []time.Time{now},
		UpdatedAt:     now,
	}
	if err := store.WithExclusive(func() error { return store.SaveState(state) }); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	var loaded ProviderState
	if err := store.WithShared(func() error {
		var loadErr error
		loaded, loadErr = store.LoadState()
		return loadErr
	}); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := loaded.Requests["abc"]; got.Profile != "default" || got.VisibilityRoot != "/Users/alice/Documents" {
		t.Fatalf("proposal snapshot drifted: %+v", got)
	}

	bad := state
	bad.Requests = map[string]RequestRecord{"abc": state.Requests["abc"]}
	record := bad.Requests["abc"]
	record.VisibilityRoot = "relative"
	bad.Requests["abc"] = record
	if err := bad.Validate("ses_test"); err == nil {
		t.Fatal("relative visibility root should fail closed")
	}
}

func validateGrantSchema(t *testing.T, path string) {
	t.Helper()
	schemaData, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "hostfs-read-grants.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("hostfs-read-grants.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("hostfs-read-grants.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("grant manifest schema: %v", err)
	}
}
