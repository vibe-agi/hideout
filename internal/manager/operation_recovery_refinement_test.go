package manager

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestOperationRecoveryTraceRefinesOperatorConfigurationModel(
	t *testing.T,
) {
	trace := newOperationRecoveryTrace(t)
	trace.runToCrash(t, recoveryTraceCrashPoint{
		boundary: "activate",
		after:    true,
	})
	recovered, _ := trace.restart(t, "")
	trace.assertSuccessfulRecovery(t, recovered)
	trace.assertExactReplay(t, recovered, true)

	mismatch := trace.binding
	mismatch.PlanDigest = digestFixture("8")
	if _, _, err := trace.store.Reserve(
		mismatch,
		[]EffectResult{trace.effect},
	); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("mismatched retry error=%v", err)
	}
	current, err := trace.store.Load(trace.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Matches(trace.binding) ||
		current.Phase != OperationSucceeded ||
		trace.provider.calls != 1 {
		t.Fatalf(
			"mismatched retry changed authoritative trace: operation=%+v calls=%d",
			current,
			trace.provider.calls,
		)
	}
}

func TestOperationRollbackTraceRefinesOperatorConfigurationModel(
	t *testing.T,
) {
	trace := newOperationRecoveryTrace(t)
	trace.runToCrash(t, recoveryTraceCrashPoint{
		boundary: "activate",
		after:    true,
	})
	restartedStore := OperationStore{Root: trace.store.Root}
	restarted := OperationService{
		Store: restartedStore, Observer: trace.observer,
	}
	if _, err := restarted.Transition(
		trace.binding.ID,
		OperationRollingBack,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.FinishEffect(
		trace.binding.ID,
		trace.effect.ID,
		trace.effect.Provider,
		EffectRolledBack,
		[]EvidenceRef{{
			Code: "profile-rollback-proved",
			Ref:  "profile:default",
		}},
	); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := restarted.Terminal(
		trace.binding.ID,
		OperationRolledBack,
		"profile-rolled-back",
		"profile rollback proved",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Phase != OperationRolledBack ||
		rolledBack.Effects[0].Status != EffectRolledBack ||
		len(rolledBack.Effects[0].Evidence) != 1 ||
		trace.provider.calls != 1 {
		t.Fatalf(
			"rollback trace violated model: operation=%+v calls=%d",
			rolledBack,
			trace.provider.calls,
		)
	}
	replayed, created, err := restartedStore.Reserve(
		trace.binding,
		[]EffectResult{trace.effect},
	)
	if err != nil || created ||
		replayed.Phase != OperationRolledBack ||
		trace.provider.calls != 1 {
		t.Fatalf(
			"rollback replay changed effect: created=%t operation=%+v calls=%d err=%v",
			created,
			replayed,
			trace.provider.calls,
			err,
		)
	}
}

// scripts/gates/recovery.sh runs this judge once normally and once for each
// named trace mutant. A mutant changes recovery behavior, not the assertion;
// the unchanged invariant checks below must kill it.
func TestOperationRecoveryRefinementMutationJudge(t *testing.T) {
	mutation := strings.TrimSpace(os.Getenv(recoveryTraceMutationEnv))
	switch mutation {
	case "":
	case "replay-running-effect",
		"success-without-proof",
		"duplicate-terminal-event":
		t.Logf("recovery-mutation-fixture=%s", mutation)
	default:
		t.Fatalf("unknown recovery mutation fixture %q", mutation)
	}

	point := recoveryTraceCrashPoint{
		boundary: "activate",
		after:    true,
	}
	if mutation == "duplicate-terminal-event" {
		point = recoveryTraceCrashPoint{boundary: "commit"}
	}
	trace := newOperationRecoveryTrace(t)
	trace.runToCrash(t, point)
	recovered, _ := trace.restart(t, mutation)
	trace.assertSuccessfulRecovery(t, recovered)
}
