package manager

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestCanonicalProfileDigestIsStableAcrossMapInsertionOrder(t *testing.T) {
	left := map[string]any{
		"schema": "fixture/v1",
		"nested": map[string]any{"beta": 2, "alpha": 1},
	}
	right := map[string]any{}
	right["nested"] = map[string]any{"alpha": 1, "beta": 2}
	right["schema"] = "fixture/v1"

	leftDigest, err := CanonicalDigest(CanonicalDomainProfileProjection, left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := CanonicalDigest(CanonicalDomainProfileProjection, right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest || !strings.HasPrefix(leftDigest, "sha256:") {
		t.Fatalf("canonical digest mismatch: left=%q right=%q", leftDigest, rightDigest)
	}
	otherDomain, err := CanonicalDigest(CanonicalDomainConfigurationPlan, left)
	if err != nil {
		t.Fatal(err)
	}
	if otherDomain == leftDigest {
		t.Fatal("canonical digest did not bind its domain")
	}
}

func TestProfileProjectionMigratesRevisionAndDetectsOutOfBandDrift(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC)
	service := ProfileProjectionService{Store: store, Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}

	first, err := service.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.Profile != "default" {
		t.Fatalf("legacy projection was not migrated at revision 1: %+v", first)
	}
	if !strings.HasPrefix(first.ContentDigest, "sha256:") {
		t.Fatalf("projection digest is not canonical: %q", first.ContentDigest)
	}
	info, err := os.Stat(store.ProfileRevisionPath("default"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("revision sidecar mode=%#o want 0600", info.Mode().Perm())
	}

	unchanged, err := service.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != first.Revision || unchanged.ContentDigest != first.ContentDigest {
		t.Fatalf("unchanged projection advanced: first=%+v next=%+v", first, unchanged)
	}

	edited, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	edited.Identity.Hostname = "out-of-band-edit"
	if err := store.Save(edited); err != nil {
		t.Fatal(err)
	}
	drifted, err := service.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Revision != first.Revision+1 || drifted.ContentDigest == first.ContentDigest {
		t.Fatalf("out-of-band drift was hidden: first=%+v drifted=%+v", first, drifted)
	}
	if err := service.CheckCAS("default", first.Revision, first.ContentDigest); !errors.Is(err, ErrStaleProfileRevision) {
		t.Fatalf("stale CAS error=%v want %v", err, ErrStaleProfileRevision)
	}
	if err := service.CheckCAS("default", drifted.Revision, drifted.ContentDigest); err != nil {
		t.Fatalf("current CAS rejected: %v", err)
	}
}

func TestProfileProjectionRejectsTamperedRevisionSidecar(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	service := ProfileProjectionService{Store: store}
	if _, err := service.Load("default"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ProfileRevisionPath("default"), []byte(`{"schema":"tampered"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Load("default"); err == nil {
		t.Fatal("tampered revision sidecar was accepted")
	}
}

func TestProfileProjectionMigrationIsSerialized(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	service := ProfileProjectionService{Store: store}
	const readers = 16
	results := make(chan ProfileProjection, readers)
	errs := make(chan error, readers)
	var wait sync.WaitGroup
	for index := 0; index < readers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			projection, err := service.Load("default")
			results <- projection
			errs <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for projection := range results {
		if projection.Revision != 1 {
			t.Fatalf("concurrent migration revision=%d want 1", projection.Revision)
		}
	}
	if _, err := loadProfileRevisionRecord(store.ProfileRevisionPath("default")); err != nil {
		t.Fatalf("concurrent migration left invalid sidecar: %v", err)
	}
}
