package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func validOwnerRecord() OwnerRecord {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	return OwnerRecord{
		Schema:            ActiveSessionSchema,
		SessionID:         "ses_20260716T120000Z_0123456789abcdef",
		EnvironmentID:     "env_20260716t120000z0123456789abcdef",
		Profile:           "default",
		Backend:           "lima",
		WorkspaceID:       "wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SessionSnapshotID: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		State:             OwnerStatePreparing,
		TerminalMode:      TerminalNone,
		StartedAt:         now,
		UpdatedAt:         now,
		CommandClass:      "bash",
	}
}

func TestOwnerRecordRequiresOpaqueWorkspaceIdentity(t *testing.T) {
	record := validOwnerRecord()
	record.WorkspaceID = ""
	if _, err := AcquireOwner(t.TempDir(), record); err == nil {
		t.Fatal("owner record accepted empty workspace authority")
	}
	record.WorkspaceID = strings.Repeat("a", 64)
	if _, err := AcquireOwner(t.TempDir(), record); err == nil {
		t.Fatal("owner record accepted legacy raw path digest")
	}
}

func TestOwnerRecordRequiresCanonicalSessionSnapshotIdentity(t *testing.T) {
	record := validOwnerRecord()
	for _, invalid := range []string{"", strings.Repeat("c", 64), "sha256:" + strings.Repeat("C", 64)} {
		record.SessionSnapshotID = invalid
		if _, err := AcquireOwner(t.TempDir(), record); err == nil {
			t.Fatalf("owner record accepted invalid session snapshot %q", invalid)
		}
	}
}

func TestOwnerLeaseProvesLiveThenStale(t *testing.T) {
	root := t.TempDir()
	record := validOwnerRecord()
	owner, err := AcquireOwner(root, record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwner(root, record); !errors.Is(err, ErrOwnerAlreadyLive) {
		t.Fatalf("second acquire error=%v", err)
	}
	observed, err := ProbeOwner(root, record.SessionID)
	if err != nil || observed.Status != OwnerLive {
		t.Fatalf("live observation=%+v err=%v", observed, err)
	}

	// Simulate an uncatchable host-process exit: the kernel closes the file but
	// leaves metadata behind for reconciliation.
	if err := owner.file.Close(); err != nil {
		t.Fatal(err)
	}
	owner.file = nil
	observed, err = ProbeOwner(root, record.SessionID)
	if err != nil || observed.Status != OwnerStale {
		t.Fatalf("stale observation=%+v err=%v", observed, err)
	}
}

func TestOwnerProbeTreatsCorruptOrMissingLeaseAsUnprovable(t *testing.T) {
	root := t.TempDir()
	record := validOwnerRecord()
	dir := filepath.Join(root, record.SessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ownerRecordFile), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	observed, err := ProbeOwner(root, record.SessionID)
	if !errors.Is(err, ErrOwnerUnprovable) || observed.Status != OwnerUnprovable {
		t.Fatalf("corrupt observation=%+v err=%v", observed, err)
	}

	if err := writeOwnerRecord(dir, record); err != nil {
		t.Fatal(err)
	}
	observed, err = ProbeOwner(root, record.SessionID)
	if !errors.Is(err, ErrOwnerUnprovable) || observed.Status != OwnerUnprovable {
		t.Fatalf("missing lock observation=%+v err=%v", observed, err)
	}
}

func TestOwnerUpdateAndConcurrentProbe(t *testing.T) {
	root := t.TempDir()
	record := validOwnerRecord()
	owner, err := AcquireOwner(root, record)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.Update(OwnerStateRunning, ""); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			observed, err := ProbeOwner(root, record.SessionID)
			if err != nil {
				errs <- err
				return
			}
			if observed.Status != OwnerLive || observed.Record.State != OwnerStateRunning {
				errs <- errors.New("probe did not observe one live running owner")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestOwnerCloseRemovesOnlyItsDirectory(t *testing.T) {
	root := t.TempDir()
	recordA := validOwnerRecord()
	recordB := validOwnerRecord()
	recordB.SessionID = "ses_20260716T120001Z_fedcba9876543210"
	a, err := AcquireOwner(root, recordA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AcquireOwner(root, recordB)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, recordA.SessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner A directory remains: %v", err)
	}
	if observed, err := ProbeOwner(root, recordB.SessionID); err != nil || observed.Status != OwnerLive {
		t.Fatalf("owner B changed: %+v err=%v", observed, err)
	}
}

func TestFailedOwnerReleasesLivenessButBlocksAutomaticReconcile(t *testing.T) {
	root := t.TempDir()
	record := validOwnerRecord()
	owner, err := AcquireOwner(root, record)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Update(OwnerStateFailed, "cleanup failed without cap_0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
	observed, err := ProbeOwner(root, record.SessionID)
	if err != nil || observed.Status != OwnerStale || observed.Record.State != OwnerStateFailed {
		t.Fatalf("failed owner observation=%+v err=%v", observed, err)
	}
	if observed.Record.CleanupError == "" || observed.Record.CleanupError == "cleanup failed without cap_0123456789abcdef" {
		t.Fatalf("cleanup error was absent or not sanitized: %q", observed.Record.CleanupError)
	}
	if _, err := ReconcileStaleOwners(root); !errors.Is(err, ErrOwnerCleanupFailed) {
		t.Fatalf("failed owner was automatically reconciled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, record.SessionID)); err != nil {
		t.Fatalf("failed owner evidence disappeared: %v", err)
	}
}

func TestOwnerDirectoryIdentityMismatchIsUnprovableAndCannotDeleteSibling(t *testing.T) {
	root := t.TempDir()
	recordA := validOwnerRecord()
	recordB := recordA
	recordB.SessionID = "ses_20260716T120001Z_fedcba9876543210"
	ownerB, err := AcquireOwner(root, recordB)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerB.Close()

	dirA := filepath.Join(root, recordA.SessionID)
	if err := os.Mkdir(dirA, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(recordB)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, ownerRecordFile), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, ownerLockFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	observed, err := ProbeOwner(root, recordA.SessionID)
	if !errors.Is(err, ErrOwnerUnprovable) || observed.SessionID != recordA.SessionID || observed.Status != OwnerUnprovable {
		t.Fatalf("mismatched owner observation=%+v err=%v", observed, err)
	}
	if _, err := ReconcileStaleOwners(root); !errors.Is(err, ErrOwnerUnprovable) {
		t.Fatalf("mismatched owner reconciled: %v", err)
	}
	if observedB, err := ProbeOwner(root, recordB.SessionID); err != nil || observedB.Status != OwnerLive {
		t.Fatalf("sibling owner changed: %+v err=%v", observedB, err)
	}
}

func TestValidOwnerNameWithNonDirectoryEntryIsUnprovable(t *testing.T) {
	root := t.TempDir()
	id := validOwnerRecord().SessionID
	if err := os.WriteFile(filepath.Join(root, id), []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owners, err := ListOwners(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 1 || owners[0].SessionID != id || owners[0].Status != OwnerUnprovable {
		t.Fatalf("owners=%+v", owners)
	}
	if _, err := ReconcileStaleOwners(root); !errors.Is(err, ErrOwnerUnprovable) {
		t.Fatalf("non-directory owner was ignored: %v", err)
	}
}

func TestReconcileCleansExactRuntimeBeforeRemovingOwnershipProof(t *testing.T) {
	root := t.TempDir()
	record := validOwnerRecord()
	owner, err := AcquireOwner(root, record)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Update(OwnerStateRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := owner.file.Close(); err != nil {
		t.Fatal(err)
	}
	owner.file = nil

	cleanupCalled := false
	removed, err := ReconcileStaleOwnersWithCleanup(root, func(item OwnerObservation) error {
		cleanupCalled = true
		if item.SessionID != record.SessionID {
			t.Fatalf("cleanup id=%q", item.SessionID)
		}
		if _, err := os.Stat(filepath.Join(root, item.SessionID)); err != nil {
			t.Fatalf("owner proof removed before cleanup: %v", err)
		}
		return nil
	})
	if err != nil || !cleanupCalled || !slices.Equal(removed, []string{record.SessionID}) {
		t.Fatalf("removed=%v cleanup=%t err=%v", removed, cleanupCalled, err)
	}
	if _, err := os.Stat(filepath.Join(root, record.SessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner proof remains: %v", err)
	}
}

func TestReconcileCleanupFailureRetainsFailedEvidence(t *testing.T) {
	root := t.TempDir()
	record := validOwnerRecord()
	owner, err := AcquireOwner(root, record)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.file.Close(); err != nil {
		t.Fatal(err)
	}
	owner.file = nil

	_, err = ReconcileStaleOwnersWithCleanup(root, func(OwnerObservation) error {
		return errors.New("cleanup cap_0123456789abcdef failed")
	})
	if !errors.Is(err, ErrOwnerCleanupFailed) {
		t.Fatalf("cleanup error=%v", err)
	}
	observed, probeErr := ProbeOwner(root, record.SessionID)
	if probeErr != nil || observed.Status != OwnerStale || observed.Record.State != OwnerStateFailed {
		t.Fatalf("retained observation=%+v err=%v", observed, probeErr)
	}
	if strings.Contains(observed.Record.CleanupError, "cap_0123456789abcdef") {
		t.Fatalf("cleanup evidence leaked credential: %q", observed.Record.CleanupError)
	}
}
