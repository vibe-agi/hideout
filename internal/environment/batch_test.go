package environment

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPublishBatchHasOneVisibilityPointAndConvergesAfterEveryCut(t *testing.T) {
	root := t.TempDir()
	records := batchRecordFixtures(t)
	interrupted := errors.New("injected batch interruption")
	preActivation := Store{
		Root: root,
		batchCut: func(phase string, index int) error {
			if phase == "record-prepared" && index == len(records)-1 {
				return interrupted
			}
			return nil
		},
	}
	if _, err := preActivation.PublishBatch("op_migration_batch0001", records); !errors.Is(err, interrupted) {
		t.Fatalf("pre-activation cut error=%v", err)
	}
	assertBatchRecordCount(t, Store{Root: root}, 0)
	if _, err := (Store{Root: root}).LoadByName(records[0].Name); !errors.Is(err, ErrNameNotFound) {
		t.Fatalf("pending name became visible: %v", err)
	}

	postActivation := Store{
		Root: root,
		batchCut: func(phase string, _ int) error {
			if phase == "activation-published" {
				return interrupted
			}
			return nil
		},
	}
	if _, err := postActivation.PublishBatch("op_migration_batch0001", records); !errors.Is(err, interrupted) {
		t.Fatalf("post-activation cut error=%v", err)
	}
	store := Store{Root: root}
	assertBatchRecordCount(t, store, len(records))
	mutated := records[0]
	mutated.Status = StatusReady
	if err := store.Save(mutated); !errors.Is(err, ErrBatchFinalizationRequired) {
		t.Fatalf("mutation during batch finalization error=%v", err)
	}
	if err := store.Remove(records[0].ID); !errors.Is(err, ErrBatchFinalizationRequired) {
		t.Fatalf("removal during batch finalization error=%v", err)
	}

	publication, err := store.PublishBatch("op_migration_batch0001", records)
	if err != nil {
		t.Fatal(err)
	}
	if publication.BatchID != "op_migration_batch0001" ||
		!validEnvironmentDigest(publication.Digest) ||
		!reflect.DeepEqual(publication.RecordIDs, []string{records[0].ID, records[1].ID}) {
		t.Fatalf("publication=%+v", publication)
	}
	for _, record := range records {
		if _, err := os.Lstat(filepath.Join(root, "environments", record.ID, batchPendingFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pending marker retained for %s: %v", record.ID, err)
		}
		if _, err := os.Lstat(filepath.Join(root, "environments", record.ID, batchOriginFile)); err != nil {
			t.Fatalf("origin marker absent for %s: %v", record.ID, err)
		}
	}
	if _, err := os.Lstat(store.activationPath(publication.BatchID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("activation marker retained after convergence: %v", err)
	}

	mutated.Status = StatusReady
	mutated.LastStartedAt = records[0].CreatedAt.Add(time.Minute)
	if err := store.Save(mutated); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.PublishBatch("op_migration_batch0001", records)
	if err != nil || !reflect.DeepEqual(replayed, publication) {
		t.Fatalf("publication replay=%+v err=%v", replayed, err)
	}
	loaded, err := store.Load(records[0].ID)
	if err != nil || loaded.Status != StatusReady {
		t.Fatalf("publication replay overwrote lifecycle state: %+v err=%v", loaded, err)
	}
}

func TestPublishBatchRejectsPendingSubstitutionWithoutPartialVisibility(t *testing.T) {
	root := t.TempDir()
	records := batchRecordFixtures(t)
	interrupted := errors.New("injected batch interruption")
	store := Store{
		Root: root,
		batchCut: func(phase string, index int) error {
			if phase == "record-prepared" && index == 0 {
				return interrupted
			}
			return nil
		},
	}
	if _, err := store.PublishBatch("op_migration_batch0002", records); !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	path := filepath.Join(root, "environments", records[0].ID, recordFile)
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Store{Root: root}).PublishBatch(
		"op_migration_batch0002", records,
	); !errors.Is(err, ErrBatchConflict) {
		t.Fatalf("pending substitution error=%v", err)
	}
	assertBatchRecordCount(t, Store{Root: root}, 0)
}

func TestActiveBatchClosureFailureNeverReturnsAVisiblePrefix(t *testing.T) {
	root := t.TempDir()
	records := batchRecordFixtures(t)
	interrupted := errors.New("injected batch interruption")
	store := Store{
		Root: root,
		batchCut: func(phase string, _ int) error {
			if phase == "activation-published" {
				return interrupted
			}
			return nil
		},
	}
	if _, err := store.PublishBatch("op_migration_batch0003", records); !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "environments", records[1].ID, recordFile)); err != nil {
		t.Fatal(err)
	}
	listed, err := (Store{Root: root}).List()
	if !errors.Is(err, ErrBatchVisibilityUnproved) || len(listed) != 0 {
		t.Fatalf("broken active closure returned prefix=%+v err=%v", listed, err)
	}
}

func TestPublishBatchRefusesExistingNameAndDoesNotMutateInputOrder(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	existingSpec := testSpecNamed("batch-one", filepath.Join(root, "existing-work"))
	if _, err := store.Create(existingSpec); err != nil {
		t.Fatal(err)
	}
	records := batchRecordFixtures(t)
	records[0].Name = "BATCH-ONE"
	reversed := []Record{records[1], records[0]}
	if _, err := store.PublishBatch(
		"op_migration_batch0004", reversed,
	); !errors.Is(err, ErrBatchConflict) {
		t.Fatalf("name collision error=%v", err)
	}
	if reversed[0].ID != records[1].ID || reversed[1].ID != records[0].ID {
		t.Fatalf("PublishBatch reordered caller input: %+v", reversed)
	}
}

func TestEmptyCatalogReadsDoNotCreateStoreState(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	if records, err := store.List(); err != nil || len(records) != 0 {
		t.Fatalf("empty list records=%+v err=%v", records, err)
	}
	if _, err := store.LoadByName("absent"); !errors.Is(err, ErrNameNotFound) {
		t.Fatalf("empty name lookup error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "environments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only catalog access created state: %v", err)
	}
}

func assertBatchRecordCount(t *testing.T, store Store, want int) {
	t.Helper()
	records, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != want {
		t.Fatalf("visible records=%d want=%d records=%+v", len(records), want, records)
	}
}

func batchRecordFixtures(t *testing.T) []Record {
	t.Helper()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	makeRecord := func(id, name, machineDigit, bootDigit string) Record {
		return Record{
			Version: RecordVersion, ID: id, Name: name,
			ImageRef: BuiltinBaseImage, Profile: "default", Backend: "lima",
			Mode:                ModeDedicated,
			MachineIdentityID:   "sha256:" + strings.Repeat(machineDigit, 64),
			BootConfigurationID: "sha256:" + strings.Repeat(bootDigit, 64),
			DedicatedWorkspace:  filepath.Join(t.TempDir(), "workspace"),
			DedicatedGuestRoot:  "/work", User: "developer",
			InstanceName: "hideout-" + strings.TrimPrefix(id, "env_"),
			Status:       StatusStopped, CreatedAt: now,
		}
	}
	return []Record{
		makeRecord("env_aaaaaaaaaaaaaaaaaaaa", "batch-one", "a", "b"),
		makeRecord("env_bbbbbbbbbbbbbbbbbbbb", "batch-two", "c", "d"),
	}
}
