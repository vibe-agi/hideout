package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperationStoreBindsIDAndReplaysTerminalResult(t *testing.T) {
	now := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	store := OperationStore{Root: t.TempDir(), Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}
	binding := operationBindingFixture("op_fixture0001", digestFixture("a"))
	effects := []EffectResult{{ID: "persist-profile", Kind: "persist", Status: EffectPending}}

	planned, created, err := store.Reserve(binding, effects)
	if err != nil {
		t.Fatal(err)
	}
	if !created || planned.Phase != OperationPlanned {
		t.Fatalf("operation was not reserved: created=%t operation=%+v", created, planned)
	}
	replay, created, err := store.Reserve(binding, effects)
	if err != nil {
		t.Fatal(err)
	}
	if created || replay.ID != planned.ID {
		t.Fatalf("same binding did not replay: created=%t operation=%+v", created, replay)
	}

	mismatch := binding
	mismatch.PlanDigest = digestFixture("b")
	if _, _, err := store.Reserve(mismatch, effects); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("mismatched operation error=%v want %v", err, ErrOperationMismatch)
	}

	if _, err := store.Transition(binding.ID, OperationClaimed, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(binding.ID, OperationStaging, nil); err != nil {
		t.Fatal(err)
	}
	if _, execute, err := store.BeginEffect(binding.ID, "persist-profile", "manager"); err != nil || !execute {
		t.Fatalf("first effect claim: execute=%t err=%v", execute, err)
	}
	if _, execute, err := store.BeginEffect(binding.ID, "persist-profile", "manager"); err != nil || execute {
		t.Fatalf("duplicate effect claim was executable: execute=%t err=%v", execute, err)
	}
	if _, err := store.FinishEffect(binding.ID, "persist-profile", "manager", EffectSucceeded, []EvidenceRef{{
		Code: "profile-committed", Ref: "profile:default@2",
	}}); err != nil {
		t.Fatal(err)
	}
	result := &OperationResult{Status: OperationSucceeded, Summary: "profile updated"}
	if _, err := store.Transition(binding.ID, OperationSucceeded, result); err != nil {
		t.Fatal(err)
	}

	terminalReplay, created, err := store.Reserve(binding, effects)
	if err != nil {
		t.Fatal(err)
	}
	if created || terminalReplay.Phase != OperationSucceeded || terminalReplay.Result == nil {
		t.Fatalf("response-loss retry did not replay terminal result: %+v", terminalReplay)
	}
}

func TestOperationStoreRepairsEmptyCompletionEvidenceExactlyOnce(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	binding := operationBindingFixture(
		"op_evidencerepair1",
		digestFixture("9"),
	)
	if _, _, err := store.Reserve(binding, []EffectResult{{
		ID: "persist-profile", Kind: "persist",
		Provider: "manager.profile", Status: EffectPending,
	}}); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{OperationClaimed, OperationStaging} {
		if _, err := store.Transition(binding.ID, phase, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, execute, err := store.BeginEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
	); err != nil || !execute {
		t.Fatalf("begin effect execute=%t err=%v", execute, err)
	}
	if _, err := store.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	service := OperationService{Store: store}
	if _, err := service.Terminal(
		binding.ID,
		OperationSucceeded,
		"profile-committed",
		"profile committed",
	); !errors.Is(err, ErrOperationTerminalUnproved) {
		t.Fatalf("empty evidence terminal error=%v", err)
	}

	evidence := []EvidenceRef{{
		Code: "profile-persisted",
		Ref:  "profile:default",
	}}
	repaired, err := store.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		evidence,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired.Effects[0].Evidence) != 1 {
		t.Fatalf("completion evidence was not repaired: %+v", repaired)
	}
	if _, err := store.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		evidence,
	); err != nil {
		t.Fatalf("exact evidence replay failed: %v", err)
	}
	if _, err := store.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		[]EvidenceRef{{Code: "different-proof"}},
	); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("conflicting evidence error=%v", err)
	}
	if _, err := service.Terminal(
		binding.ID,
		OperationSucceeded,
		"profile-committed",
		"profile committed",
	); err != nil {
		t.Fatal(err)
	}
}

func TestOperationStoreIgnoresTornTemporaryRecordAndRejectsCorruptPrimary(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	binding := operationBindingFixture("op_fixture0002", digestFixture("c"))
	if _, _, err := store.Reserve(binding, nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(store.OperationPath(binding.ID))
	if err := os.WriteFile(filepath.Join(dir, ".operation-torn.tmp"), []byte(`{"schema":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(binding.ID); err != nil {
		t.Fatalf("orphan temporary record hid committed operation: %v", err)
	}
	if err := os.WriteFile(store.OperationPath(binding.ID), []byte(`{"schema":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(binding.ID); err == nil {
		t.Fatal("corrupt primary operation was treated as success")
	}
}

func TestOperationStoreRejectsInvalidPhaseAndPrunesOldestTerminalRecord(t *testing.T) {
	now := time.Date(2026, 7, 29, 2, 30, 0, 0, time.UTC)
	store := OperationStore{Root: t.TempDir(), MaxRecords: 2, Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}
	first := operationBindingFixture("op_fixture1001", digestFixture("d"))
	second := operationBindingFixture("op_fixture1002", digestFixture("e"))
	third := operationBindingFixture("op_fixture1003", digestFixture("f"))
	for _, binding := range []OperationBinding{first, second} {
		if _, _, err := store.Reserve(binding, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Transition(binding.ID, OperationSucceeded, &OperationResult{
			Status: OperationSucceeded,
		}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("planned-to-succeeded error=%v want %v", err, ErrInvalidTransition)
		}
		if _, err := store.Transition(binding.ID, OperationClaimed, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Transition(binding.ID, OperationStaging, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Transition(binding.ID, OperationSucceeded, &OperationResult{
			Status: OperationSucceeded,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.Reserve(third, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(first.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest terminal record was not pruned: %v", err)
	}
	if _, err := store.Load(second.ID); err != nil {
		t.Fatalf("newer terminal record was pruned: %v", err)
	}
	if _, err := store.Load(third.ID); err != nil {
		t.Fatalf("new planned record is missing: %v", err)
	}
}

func TestOperationStorePersistsRetryableAndRequiredRecovery(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	binding := operationBindingFixture(
		"op_recoveryfixture1",
		digestFixture("f"),
	)
	if _, _, err := store.Reserve(binding, []EffectResult{{
		ID: "persist-profile", Kind: "persist",
		Provider: "manager.profile", Status: EffectPending,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RequireRecovery(
		binding.ID,
		Recovery{
			Code:    "recovery-required",
			Summary: "recovery is required",
		},
	); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unaccepted recovery error=%v", err)
	}
	for _, phase := range []string{OperationClaimed, OperationStaging} {
		if _, err := store.Transition(binding.ID, phase, nil); err != nil {
			t.Fatal(err)
		}
	}

	retryable := Recovery{
		Code:       "retry-with-proof",
		Summary:    "retry after checking the provider",
		NextAction: "retry the same operation",
	}
	operation, changed, err := store.SetRecovery(binding.ID, retryable)
	if err != nil || !changed ||
		operation.Phase != OperationStaging ||
		operation.Recovery != retryable {
		t.Fatalf(
			"retryable recovery changed=%t operation=%+v err=%v",
			changed,
			operation,
			err,
		)
	}
	if _, changed, err := store.SetRecovery(
		binding.ID,
		retryable,
	); err != nil || changed {
		t.Fatalf("idempotent recovery changed=%t err=%v", changed, err)
	}

	required := Recovery{
		Code:       "provider-state-unproved",
		Summary:    "provider completion could not be proved",
		NextAction: "inspect provider state",
	}
	operation, changed, err = store.RequireRecovery(
		binding.ID,
		required,
	)
	if err != nil || !changed ||
		operation.Phase != OperationRecoveryRequired ||
		operation.Recovery != required {
		t.Fatalf(
			"required recovery changed=%t operation=%+v err=%v",
			changed,
			operation,
			err,
		)
	}
	replayed, err := store.Load(binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Phase != OperationRecoveryRequired ||
		replayed.Recovery != required {
		t.Fatalf("replayed recovery=%+v", replayed)
	}
}

func operationBindingFixture(id, digest string) OperationBinding {
	return OperationBinding{
		ID:           id,
		Kind:         "profile.transaction",
		Owner:        OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest:   digest,
		BaseRevision: 1,
	}
}

func digestFixture(value string) string {
	return "sha256:" + strings.Repeat(value, 64)
}
