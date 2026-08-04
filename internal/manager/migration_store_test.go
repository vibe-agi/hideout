package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationStoreReservesImmutableOperationAndRecoversClaimPrefix(t *testing.T) {
	root := t.TempDir()
	store := MigrationStore{Root: root}
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	reserved, created, err := store.Reserve(operation)
	if err != nil || !created || reserved.ID != operation.ID {
		t.Fatalf("reserve created=%t operation=%+v err=%v", created, reserved, err)
	}
	replayed, created, err := store.Reserve(operation)
	if err != nil || created || replayed.Revision != operation.Revision {
		t.Fatalf("reserve replay created=%t operation=%+v err=%v", created, replayed, err)
	}
	mutated := operation.Clone()
	mutated.PlanDigest = migration.Digest(digestFixture("f"))
	if _, _, err := store.Reserve(mutated); !errors.Is(err, ErrMigrationOperationMismatch) {
		t.Fatalf("immutable reserve mismatch error=%v", err)
	}
	freshWithHeldClaim := operation.Clone()
	freshWithHeldClaim.ID = "op_migrationforged1"
	freshWithHeldClaim.Claims[0].State = MigrationClaimHeld
	freshWithHeldClaim.Claims[0].AcquiredAt = operation.CreatedAt
	if _, _, err := store.Reserve(freshWithHeldClaim); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("forged held claim reserve error=%v", err)
	}

	interrupted := MigrationStore{
		Root: root,
		afterClaimWrite: func(index int, _ MigrationClaim) error {
			if index == 0 {
				return errors.New("simulated process loss")
			}
			return nil
		},
	}
	if _, _, err := interrupted.AcquireClaims(operation.ID); err == nil {
		t.Fatal("claim interruption was ignored")
	}
	loaded, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range loaded.Claims {
		if claim.State != MigrationClaimPending {
			t.Fatalf("operation claimed an incomplete prefix: %+v", loaded.Claims)
		}
	}
	claimed, acquired, err := store.AcquireClaims(operation.ID)
	if err != nil || !acquired {
		t.Fatalf("claim resume acquired=%t operation=%+v err=%v", acquired, claimed, err)
	}
	for _, claim := range claimed.Claims {
		if claim.State != MigrationClaimHeld {
			t.Fatalf("claim was not durably held: %+v", claimed.Claims)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "migration", "claims"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "dev-clone") || strings.Contains(entry.Name(), "/") {
			t.Fatalf("claim filename exposed raw key: %q", entry.Name())
		}
	}
}

func TestMigrationStoreCancellationRequiresExactRevisionAndExplicitExportChoice(t *testing.T) {
	store := MigrationStore{Root: t.TempDir()}
	operation := migrationExportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestCancellation(operation.ID, operation.Revision, nil); !errors.Is(err, ErrMigrationRequestInvalid) {
		t.Fatalf("missing export retention choice error=%v", err)
	}
	if _, err := store.RequestCancellation(operation.ID, operation.Revision+1, boolPointer(false)); !errors.Is(err, ErrMigrationStoreRevision) {
		t.Fatalf("stale cancellation revision error=%v", err)
	}
	cancelling, err := store.RequestCancellation(
		operation.ID, operation.Revision, boolPointer(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.Phase != MigrationPhaseCancelling || cancelling.Cancellation == nil ||
		!cancelling.Cancellation.RetainPartial || !cancelling.Progress.CancelPending ||
		cancelling.Cancellation.OperationRevision != operation.Revision {
		t.Fatalf("cancelling operation=%+v", cancelling)
	}
	cloned := cancelling.Clone()
	cloned.Cancellation.RetainPartial = false
	if !cancelling.Cancellation.RetainPartial {
		t.Fatal("cancellation decision was not deep-cloned")
	}
	if _, err := store.RequestCancellation(
		operation.ID, operation.Revision, boolPointer(true),
	); !errors.Is(err, ErrMigrationStoreRevision) {
		t.Fatalf("replayed stale cancellation error=%v", err)
	}
	failed, err := store.TransitionPhase(
		operation.ID, MigrationPhaseRecoverableFailure, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Recovery.Action != MigrationRecoveryRemovePartial {
		t.Fatalf("cancellation recovery=%+v", failed.Recovery)
	}
}

func TestMigrationStoreRejectsImportCancellationAfterCommitDecision(t *testing.T) {
	store := MigrationStore{Root: t.TempDir()}
	operation := migrationVerifiedImportOperationFixture(t)
	writeMigrationOperationFixture(t, store, operation)
	committing, _, err := store.Decide(operation.ID, MigrationDecisionCommit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestCancellation(
		operation.ID, committing.Revision, nil,
	); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("post-commit cancellation error=%v", err)
	}
}

func boolPointer(value bool) *bool { return &value }

func writeMigrationOperationFixture(
	t *testing.T,
	store MigrationStore,
	operation MigrationOperation,
) {
	t.Helper()
	if err := store.withLock(func() error {
		if err := store.ensureDirectories(); err != nil {
			return err
		}
		return store.writeOperationUnlocked(operation)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationDestinationNamespaceIsStableLocalAndDistinctAcrossStores(t *testing.T) {
	firstStore := MigrationStore{Root: t.TempDir()}
	secondStore := MigrationStore{Root: t.TempDir()}

	first, err := firstStore.DestinationNamespace()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := firstStore.DestinationNamespace()
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondStore.DestinationNamespace()
	if err != nil {
		t.Fatal(err)
	}
	if first != replayed {
		t.Fatalf("destination namespace changed on replay: first=%q replay=%q", first, replayed)
	}
	if first == second {
		t.Fatalf("independent stores shared destination namespace %q", first)
	}
	firstOperation := migrationOperationID(first, "same-client", "same-request")
	secondOperation := migrationOperationID(second, "same-client", "same-request")
	if firstOperation == secondOperation {
		t.Fatalf("independent stores derived the same operation id %q", firstOperation)
	}

	path := filepath.Join(firstStore.Root, "migration", "destination-namespace.json")
	if err := os.WriteFile(path, []byte("{\"schema\":\"wrong\",\"namespace\":\"migdst_invalid1\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := firstStore.DestinationNamespace(); err == nil {
		t.Fatal("corrupt destination namespace was silently rotated")
	}

	stateStore := MigrationStore{Root: t.TempDir()}
	if _, err := stateStore.DestinationNamespace(); err != nil {
		t.Fatal(err)
	}
	operation := migrationImportOperationFixture()
	if _, _, err := stateStore.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateStore.Root, "migration", "destination-namespace.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.DestinationNamespace(); err == nil {
		t.Fatal("missing destination namespace was rotated while migration state existed")
	}
}

func TestMigrationStoreAllowsExactlyOneConcurrentClaimOwner(t *testing.T) {
	store := MigrationStore{Root: t.TempDir()}
	first := migrationImportOperationFixture()
	first.Phase = MigrationPhaseClaiming
	second := migrationImportOperationFixtureWithID("op_migrationimport2")
	second.Phase = MigrationPhaseClaiming
	second.PlanID = "plan_import5678"
	second.PlanDigest = migration.Digest(digestFixture("e"))
	second.Effects[0].ID = "effect_stage5678"
	second.Effects[1].ID = "effect_activate5678"
	for _, operation := range []MigrationOperation{first, second} {
		if _, _, err := store.Reserve(operation); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	type result struct {
		id       string
		acquired bool
		err      error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, id := range []string{first.ID, second.ID} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, acquired, err := store.AcquireClaims(id)
			results <- result{id: id, acquired: acquired, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	winner := ""
	conflicts := 0
	for result := range results {
		if result.err == nil && result.acquired {
			if winner != "" {
				t.Fatalf("two operations acquired the same claims: %q and %q", winner, result.id)
			}
			winner = result.id
			continue
		}
		if errors.Is(result.err, ErrMigrationClaimConflict) {
			conflicts++
			continue
		}
		t.Fatalf(
			"unexpected claim result: id=%s acquired=%t err=%v conflict=%t",
			result.id, result.acquired, result.err,
			errors.Is(result.err, ErrMigrationClaimConflict),
		)
	}
	if winner == "" || conflicts != 1 {
		t.Fatalf("winner=%q conflicts=%d", winner, conflicts)
	}
	if _, released, err := store.ReleaseClaims(winner); err != nil || !released {
		t.Fatalf("release winner=%q released=%t err=%v", winner, released, err)
	}
	loser := first.ID
	if loser == winner {
		loser = second.ID
	}
	if _, acquired, err := store.AcquireClaims(loser); err != nil || !acquired {
		t.Fatalf("loser could not acquire after release: acquired=%t err=%v", acquired, err)
	}
}

func TestMigrationStoreEffectsAndCommitDecisionAreAtMostOnce(t *testing.T) {
	store := MigrationStore{Root: t.TempDir()}
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AcquireClaims(operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(
		operation.ID, MigrationPhaseMaterializing, nil,
	); err != nil {
		t.Fatal(err)
	}
	started, execute, err := store.BeginEffect(
		operation.ID, "effect_stage1234", "backend.lima",
	)
	if err != nil || !execute || started.Effects[0].Status != MigrationEffectRunning {
		t.Fatalf("first effect execute=%t operation=%+v err=%v", execute, started, err)
	}
	replayed, execute, err := store.BeginEffect(
		operation.ID, "effect_stage1234", "backend.lima",
	)
	if err != nil || execute || replayed.Revision != started.Revision {
		t.Fatalf("effect replay execute=%t operation=%+v err=%v", execute, replayed, err)
	}
	stage := migrationDestinationStageStateFixture()
	evidence := []MigrationEffectEvidence{{
		Code: "migration.stage.verified", OpaqueRef: stage.StageHandle,
		Digest:     stage.EvidenceDigest,
		Count:      uint64(len(stage.Checkpoints) + len(stage.Profiles)),
		ObservedAt: time.Date(2026, 8, 2, 4, 1, 0, 0, time.UTC),
	}}
	completed, err := store.FinishDestinationStage(
		operation.ID, "effect_stage1234", "backend.lima", stage, evidence,
	)
	if err != nil || completed.Effects[0].Status != MigrationEffectSucceeded {
		t.Fatalf("finish effect operation=%+v err=%v", completed, err)
	}
	if _, err := store.FinishDestinationStage(
		operation.ID, "effect_stage1234", "backend.lima", stage, evidence,
	); err != nil {
		t.Fatalf("effect completion replay: %v", err)
	}

	for _, phase := range []MigrationPhase{MigrationPhasePreparingSecrets, MigrationPhaseAdopting} {
		if _, err := store.TransitionPhase(operation.ID, phase, nil); err != nil {
			t.Fatal(err)
		}
	}
	adoptionStarted, execute, err := store.BeginEffect(
		operation.ID, "effect_adopt1234", "backend.lima",
	)
	if err != nil || !execute {
		t.Fatalf("begin adoption execute=%t operation=%+v err=%v", execute, adoptionStarted, err)
	}
	adoption := migrationDestinationAdoptionStateFixture(t, adoptionStarted)
	adoptionEvidence := []MigrationEffectEvidence{{
		Code: "migration.adoption.verified", OpaqueRef: adoption.StageHandle,
		Digest: adoption.EvidenceDigest, Count: uint64(len(adoption.Records)),
		ObservedAt: time.Date(2026, 8, 2, 4, 2, 0, 0, time.UTC),
	}}
	if _, err := store.FinishDestinationAdoption(
		operation.ID, "effect_adopt1234", "backend.lima", adoption, adoptionEvidence,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionPhase(operation.ID, MigrationPhaseVerifying, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginEffect(
		operation.ID, "effect_verify1234", "backend.lima",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishEffect(
		operation.ID, "effect_verify1234", "backend.lima", MigrationEffectSucceeded,
		[]MigrationEffectEvidence{{
			Code:       migrationDestinationVerificationEvidenceCode,
			OpaqueRef:  stage.StageHandle,
			Digest:     migration.Digest("sha256:" + strings.Repeat("f", 64)),
			Count:      uint64(len(operation.ExpectedDisks)),
			ObservedAt: time.Date(2026, 8, 2, 4, 3, 0, 0, time.UTC),
		}},
	); err != nil {
		t.Fatal(err)
	}
	committing, changed, err := store.Decide(
		operation.ID, MigrationDecisionCommit,
	)
	if err != nil || !changed || committing.Decision == nil {
		t.Fatalf("persist commit changed=%t operation=%+v err=%v", changed, committing, err)
	}
	replayDecision, changed, err := store.Decide(operation.ID, MigrationDecisionCommit)
	if err != nil || changed || replayDecision.Revision != committing.Revision {
		t.Fatalf("commit replay changed=%t operation=%+v err=%v", changed, replayDecision, err)
	}
	if _, _, err := store.Decide(
		operation.ID, MigrationDecisionRollback,
	); !errors.Is(err, ErrMigrationDecisionConflict) {
		t.Fatalf("opposite persisted decision error=%v", err)
	}
}

func TestMigrationStorePersistsMonotonicProgress(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseClaiming
	now := operation.UpdatedAt
	store := MigrationStore{
		Root: t.TempDir(),
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	}
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	materializing, err := store.TransitionPhase(
		operation.ID, MigrationPhaseMaterializing, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	progress := materializing.Progress
	progress.LogicalTotalKnown = true
	progress.CompletedLogicalBytes = 25
	progress.TotalLogicalBytes = 100
	progress.ComponentsComplete = 1
	progress.ComponentsTotal = 2
	progress.CurrentItem = "copy /Users/alice/private"
	progress.ActiveSince = materializing.UpdatedAt
	progress.CheckpointAt = materializing.UpdatedAt
	updated, changed, err := store.UpdateProgress(operation.ID, progress)
	if err != nil || !changed || updated.Progress.CurrentItem != "copy [path]" {
		t.Fatalf("progress changed=%t operation=%+v err=%v", changed, updated, err)
	}
	replayed, changed, err := store.UpdateProgress(operation.ID, updated.Progress)
	if err != nil || changed || replayed.Revision != updated.Revision {
		t.Fatalf("progress replay changed=%t operation=%+v err=%v", changed, replayed, err)
	}
	regressed := updated.Progress
	regressed.CompletedLogicalBytes--
	if _, _, err := store.UpdateProgress(operation.ID, regressed); !errors.Is(err, ErrMigrationProgressInvalid) {
		t.Fatalf("progress regression error=%v", err)
	}
	loaded, err := store.Load(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != updated.Revision || loaded.Progress != updated.Progress {
		t.Fatalf("durable progress=%+v want=%+v", loaded.Progress, updated.Progress)
	}
}

func TestMigrationStoreRejectsCorruptOrPublicOperationRecord(t *testing.T) {
	store := MigrationStore{Root: t.TempDir()}
	operation := migrationImportOperationFixture()
	if _, _, err := store.Reserve(operation); err != nil {
		t.Fatal(err)
	}
	path := store.OperationPath(operation.ID)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(operation.ID); err == nil {
		t.Fatal("public operation record was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(operation.ID); err == nil {
		t.Fatal("corrupt operation record was accepted")
	}
}
