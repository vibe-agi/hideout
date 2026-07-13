package environment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreCreateLoadByNameAndResolve(t *testing.T) {
	store := Store{Root: t.TempDir()}
	spec := Spec{
		Name:                 "latest-test",
		ImageRef:             BuiltinBaseImage,
		Profile:              "default",
		Backend:              "lima",
		BackendConfigVersion: "lima-config/test-a",
		Workspace:            filepath.Join(t.TempDir(), "workspace"),
		GuestWorkspace:       "/workspace",
		ProfileID:            "prf_1111",
		IdentityID:           "id_2222",
		User:                 "developer",
		Hostname:             "devbox",
	}
	rec, err := store.Create(spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.InstanceName = "hideout-default-env-test"
	rec.LastStartedAt = time.Now().UTC()
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.LoadByName(spec.Name)
	if err != nil {
		t.Fatalf("LoadByName: %v", err)
	}
	if got.ID != rec.ID || got.InstanceName != rec.InstanceName {
		t.Fatalf("LoadByName=%+v want %+v", got, rec)
	}
	short := rec.ID[len("env_") : len("env_")+12]
	loaded, err := store.Load(short)
	if err != nil {
		t.Fatalf("Load short prefix: %v", err)
	}
	if loaded.ID != rec.ID {
		t.Fatalf("Load short prefix ID=%s want %s", loaded.ID, rec.ID)
	}
}

func TestStorePinsRuntimeProvenanceWithoutInferringIt(t *testing.T) {
	store := Store{Root: t.TempDir()}
	provenance := testRuntimeProvenance()
	rec, err := store.Create(Spec{
		Name:           "runtime-pinned",
		ImageRef:       provenance.ImageRef(),
		Runtime:        &provenance,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	loaded, err := store.Load(rec.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Runtime == nil || *loaded.Runtime != provenance {
		t.Fatalf("runtime provenance=%+v want %+v", loaded.Runtime, provenance)
	}

	custom, err := store.Create(Spec{
		Name:           "custom-unverified",
		ImageRef:       provenance.ImageRef(),
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
	})
	if err != nil {
		t.Fatalf("Create custom: %v", err)
	}
	if custom.Runtime != nil {
		t.Fatalf("matching image ref must not infer runtime provenance: %+v", custom.Runtime)
	}
}

func TestStoreRejectsRuntimeProvenanceThatDoesNotMatchImage(t *testing.T) {
	provenance := testRuntimeProvenance()
	_, err := (Store{Root: t.TempDir()}).Create(Spec{
		Name:           "runtime-mismatch",
		ImageRef:       BuiltinBaseImage,
		Runtime:        &provenance,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
	})
	if err == nil || !strings.Contains(err.Error(), "runtime provenance") {
		t.Fatalf("expected runtime provenance mismatch, got %v", err)
	}
}

func TestRuntimeProvenanceRequiresPackageInventoryDigest(t *testing.T) {
	for name, digest := range map[string]string{
		"missing":    "",
		"no prefix":  strings.Repeat("c", 64),
		"uppercase":  "sha256:" + strings.Repeat("C", 64),
		"wrong size": "sha256:" + strings.Repeat("c", 63),
	} {
		t.Run(name, func(t *testing.T) {
			provenance := testRuntimeProvenance()
			provenance.PackageInventoryDigest = digest
			if err := provenance.Validate(); err == nil || !strings.Contains(err.Error(), "packageInventoryDigest") {
				t.Fatalf("package inventory digest error=%v", err)
			}
		})
	}
}

func testRuntimeProvenance() RuntimeProvenance {
	return RuntimeProvenance{
		Family:                 "developer-standard",
		Revision:               "2026.07.0",
		CatalogRelease:         "2026.07.0",
		ContractID:             "developer-standard/v1",
		ContractDigest:         "sha256:" + strings.Repeat("b", 64),
		ArtifactLocation:       "https://github.com/vibe-agi/hideout/releases/download/runtime-2026.07.0/developer-standard.qcow2",
		ArtifactSHA256:         strings.Repeat("a", 64),
		PackageInventoryDigest: "sha256:" + strings.Repeat("c", 64),
		DownloadBytes:          512 << 20,
		VirtualBytes:           12 << 30,
		HostOS:                 "darwin",
		HostArch:               "arm64",
		GuestArch:              "aarch64",
		Maturity:               "preview",
	}
}

func TestClearRuntimePreservesMountRootsAndRemovesContents(t *testing.T) {
	store := Store{Root: t.TempDir()}
	rec, err := store.Create(Spec{
		Name:           "runtime-test",
		ImageRef:       BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, rel := range []string{
		"tmp/value",
		"shims/open",
		"network/proxy.url",
		"bootstrap/bootstrap.sh",
		"lima.yaml",
	} {
		path := filepath.Join(store.RuntimeDir(rec.ID), rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ClearRuntime(rec.ID); err != nil {
		t.Fatalf("ClearRuntime: %v", err)
	}
	for _, dir := range []string{"tmp", "shims", "network", "bootstrap"} {
		path := filepath.Join(store.RuntimeDir(rec.ID), dir)
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatalf("runtime mount root %s missing: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("runtime mount root %s should be empty, got %v", dir, entries)
		}
	}
	if _, err := os.Stat(filepath.Join(store.RuntimeDir(rec.ID), "lima.yaml")); !os.IsNotExist(err) {
		t.Fatalf("runtime root files should be removed, err=%v", err)
	}
}

func TestStoreLockIsExclusiveAndReleasable(t *testing.T) {
	store := Store{Root: t.TempDir()}
	rec, err := store.Create(Spec{
		Name:           "lock-test",
		ImageRef:       BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	lock, err := store.Lock(rec.ID)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := store.Lock(rec.ID); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("second Lock should fail while held, got %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	lock, err = store.Lock(rec.ID)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatalf("second Unlock: %v", err)
	}
}

func TestValidateNameCharsetAndReservation(t *testing.T) {
	valid := []string{"work", "web-dev", "proj_3", "a", "ws-abc123.dev", "My-Env"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Fatalf("name %q should be valid: %v", name, err)
		}
	}
	invalid := []string{"", " ", "has space", "path/like", `back\slash`, "-lead", ".lead", "_lead", "semi;colon", "star*", strings.Repeat("x", 65)}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Fatalf("name %q should be rejected", name)
		}
	}
	for _, name := range []string{"default", "Default", "DEFAULT"} {
		err := ValidateName(name)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("name %q should be rejected as reserved, got %v", name, err)
		}
	}
}

func TestCreateEnforcesNameUniquenessCaseInsensitive(t *testing.T) {
	store := Store{Root: t.TempDir()}
	spec := testSpecNamed("Work", "/tmp/ws-a")
	if _, err := store.Create(spec); err != nil {
		t.Fatal(err)
	}
	dup := testSpecNamed("work", "/tmp/ws-b")
	if _, err := store.Create(dup); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("expected case-insensitive name collision error, got %v", err)
	}
}

func TestCreateRequiresImageRefAndValidName(t *testing.T) {
	store := Store{Root: t.TempDir()}
	spec := testSpecNamed("work", "/tmp/ws")
	spec.ImageRef = ""
	if _, err := store.Create(spec); err == nil {
		t.Fatal("expected missing imageRef to be rejected")
	}
	spec = testSpecNamed("bad name", "/tmp/ws")
	if _, err := store.Create(spec); err == nil {
		t.Fatal("expected invalid name to be rejected")
	}
}

func TestLoadByNameAndRecordShape(t *testing.T) {
	store := Store{Root: t.TempDir()}
	spec := testSpecNamed("work", "/tmp/ws")
	spec.AutoNamed = true
	created, err := store.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.LoadByName("WORK")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != created.ID || rec.Name != "work" || !rec.AutoNamed || rec.ImageRef != BuiltinBaseImage {
		t.Fatalf("unexpected record: %+v", rec)
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "environments", rec.ID, "environment.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "toolsHash") {
		t.Fatalf("v2 record must not carry toolsHash: %s", data)
	}
	if _, err := store.LoadByName("absent"); err == nil {
		t.Fatal("expected unknown name error")
	}
}

func TestForeignVersionRecordsAreRejectedWithGuidance(t *testing.T) {
	store := Store{Root: t.TempDir()}
	plantRecordFixture(t, store, "env_20260701t000000zaabbccddee00000000ff", "v1-record.json")
	_, err := store.Load("env_20260701t000000zaabbccddee00000000ff")
	if err == nil || !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
	if !strings.Contains(err.Error(), "clean") {
		t.Fatalf("guidance should mention clean/recreate: %v", err)
	}
}

func TestListSurfacesForeignRecordsAsUnsupportedRows(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if _, err := store.Create(testSpecNamed("work", "/tmp/ws")); err != nil {
		t.Fatal(err)
	}
	plantRecordFixture(t, store, "env_20260701t000000zaabbccddee00000000ff", "v1-record.json")
	plantRecordFixture(t, store, "env_20260701t000000zffeeddccbbaa99887766", "corrupt.json")
	records, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 rows, got %d: %+v", len(records), records)
	}
	unsupported := 0
	for _, rec := range records {
		if rec.Status == StatusUnsupportedVersion {
			unsupported++
			if rec.Name != "" || rec.ImageRef != "" || rec.Workspace != "" || rec.Profile != "" {
				t.Fatalf("unsupported row must not trust old fields: %+v", rec)
			}
			if rec.ID == "" {
				t.Fatalf("unsupported row must keep id: %+v", rec)
			}
		}
	}
	if unsupported != 2 {
		t.Fatalf("expected 2 unsupported rows, got %d", unsupported)
	}
}

func TestParseImageDeclarationFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "imagedecl", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Valid []struct {
			Ref  string `json:"ref"`
			Form string `json:"form"`
		} `json:"valid"`
		Invalid []struct {
			Ref    string `json:"ref"`
			Reason string `json:"reason"`
		} `json:"invalid"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases.Valid {
		decl, err := ParseImageDeclaration(tc.Ref)
		if err != nil {
			t.Fatalf("ref %q should parse: %v", tc.Ref, err)
		}
		if string(decl.Form) != tc.Form {
			t.Fatalf("ref %q form = %s, want %s", tc.Ref, decl.Form, tc.Form)
		}
		if tc.Form == "url" && len(decl.Digest) != 64 {
			t.Fatalf("ref %q digest not captured: %+v", tc.Ref, decl)
		}
	}
	for _, tc := range cases.Invalid {
		if _, err := ParseImageDeclaration(tc.Ref); err == nil {
			t.Fatalf("ref %q (%s) should be rejected", tc.Ref, tc.Reason)
		}
	}
	if _, err := ParseImageDeclaration("https://example.com/dev.img"); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("digest-less URL guidance should mention sha256 checksums, got %v", err)
	}
}

func TestAutoNameDeterministicAndMoveSensitive(t *testing.T) {
	a1 := AutoName("default", "/home/op/projects/My App")
	a2 := AutoName("default", "/home/op/projects/My App")
	if a1 != a2 {
		t.Fatalf("auto-name not deterministic: %s vs %s", a1, a2)
	}
	if err := ValidateName(a1); err != nil {
		t.Fatalf("auto-name %q must pass name validation: %v", a1, err)
	}
	if !strings.Contains(a1, "my-app") {
		t.Fatalf("auto-name should carry sanitized basename: %s", a1)
	}
	moved := AutoName("default", "/home/op/archive/My App")
	if moved == a1 {
		t.Fatal("moved workspace must not alias the old auto-name")
	}
	otherProfile := AutoName("privacy", "/home/op/projects/My App")
	if otherProfile == a1 {
		t.Fatal("different profile must derive a different auto-name")
	}
}

func testSpecNamed(name, workspace string) Spec {
	return Spec{
		Name:                 name,
		ImageRef:             BuiltinBaseImage,
		Profile:              "default",
		Backend:              "lima",
		BackendConfigVersion: "lima-config/v3",
		Workspace:            workspace,
		GuestWorkspace:       "/workspace",
	}
}

func plantRecordFixture(t *testing.T, store Store, id, fixture string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "records", fixture))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.Root, "environments", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), src, 0o600); err != nil {
		t.Fatal(err)
	}
}
