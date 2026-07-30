package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityCleanupRemovesExactReusableIncarnationsForDestructiveLifecycle(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{
		ActivityCleanupEnvironmentClean,
		ActivityCleanupEnvironmentDelete,
		ActivityCleanupEnvironmentRecreate,
	} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			t.Parallel()

			activity := openCleanupStore(t)
			targetOld := cleanupReusableOwner(t, "env_cleanup", "incarnation-old")
			targetCurrent := cleanupReusableOwner(t, "env_cleanup", "incarnation-current")
			neighbor := cleanupReusableOwner(t, "env_neighbor", "incarnation-neighbor")
			disposable := cleanupDisposableOwner(t, "ses_disposable", "incarnation-disposable")
			for sequence, owner := range []workloadtypes.ActivityOwner{
				targetOld, targetCurrent, neighbor, disposable,
			} {
				appendCleanupRecord(t, activity, owner, uint64(sequence+1))
			}

			service := NewActivityCleanupService(activity, cleanupNow)
			plan, err := service.PlanEnvironment(
				context.Background(), "env_cleanup", operation,
			)
			if err != nil {
				t.Fatalf("plan cleanup: %v", err)
			}
			if got := ownerKeys(plan.Owners); !slices.Equal(got, []string{
				targetCurrent.Key(), targetOld.Key(),
			}) {
				t.Fatalf("planned owners = %v", got)
			}
			result, err := service.Apply(context.Background(), plan)
			if err != nil {
				t.Fatalf("apply cleanup: %v", err)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("cleanup result invalid: %v", err)
			}
			if result.Status != ActivityCleanupAbsent ||
				len(result.Proofs) != 2 || len(result.RemainingOwnerKeys) != 0 {
				t.Fatalf("cleanup result = %#v", result)
			}
			for _, proof := range result.Proofs {
				if proof.Status != ActivityCleanupAbsent ||
					proof.OwnerKey == "" || proof.ObservedAt.IsZero() {
					t.Fatalf("invalid exact absence proof: %#v", proof)
				}
			}
			for _, removed := range []workloadtypes.ActivityOwner{
				targetOld, targetCurrent,
			} {
				if _, err := activity.Snapshot(
					context.Background(), removed,
				); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
					t.Fatalf("removed owner %s snapshot error = %v", removed.Key(), err)
				}
			}
			for _, retained := range []workloadtypes.ActivityOwner{
				neighbor, disposable,
			} {
				if _, err := activity.Snapshot(
					context.Background(), retained,
				); err != nil {
					t.Fatalf("neighbor %s was deleted: %v", retained.Key(), err)
				}
			}

			retried, err := service.Apply(context.Background(), plan)
			if err != nil {
				t.Fatalf("idempotent retry: %v", err)
			}
			if len(retried.Proofs) != 2 ||
				!retried.Proofs[0].AlreadyAbsent ||
				!retried.Proofs[1].AlreadyAbsent {
				t.Fatalf("idempotent proofs = %#v", retried.Proofs)
			}
		})
	}
}

func TestActivityCleanupPlanNeverDeletesAnIncarnationCreatedAfterPlanning(t *testing.T) {
	t.Parallel()

	activity := openCleanupStore(t)
	old := cleanupReusableOwner(t, "env_stale", "incarnation-old")
	appendCleanupRecord(t, activity, old, 1)
	service := NewActivityCleanupService(activity, cleanupNow)
	plan, err := service.PlanEnvironment(
		context.Background(),
		"env_stale",
		ActivityCleanupEnvironmentRecreate,
	)
	if err != nil {
		t.Fatal(err)
	}

	replacement := cleanupReusableOwner(t, "env_stale", "incarnation-new")
	appendCleanupRecord(t, activity, replacement, 2)
	result, err := service.Apply(context.Background(), plan)
	if !errors.Is(err, ErrActivityCleanupStale) {
		t.Fatalf("stale cleanup error = %v", err)
	}
	if !slices.Equal(result.RemainingOwnerKeys, []string{replacement.Key()}) {
		t.Fatalf("remaining owners = %v", result.RemainingOwnerKeys)
	}
	if _, err := activity.Snapshot(context.Background(), replacement); err != nil {
		t.Fatalf("replacement incarnation was deleted: %v", err)
	}
	if _, err := activity.Snapshot(
		context.Background(), old,
	); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
		t.Fatalf("planned old owner was not deleted: %v", err)
	}
}

func TestDisposableActivityCleanupDeletesOnlyTerminalOwnerAndPreservesAudit(t *testing.T) {
	t.Parallel()

	activity := openCleanupStore(t)
	target := cleanupDisposableOwner(t, "ses_terminal", "incarnation-terminal")
	neighbor := cleanupDisposableOwner(t, "ses_neighbor", "incarnation-neighbor")
	reusable := cleanupReusableOwner(t, "env_terminal", "incarnation-reusable")
	for sequence, owner := range []workloadtypes.ActivityOwner{
		target, neighbor, reusable,
	} {
		appendCleanupRecord(t, activity, owner, uint64(sequence+1))
	}
	customAudit := filepath.Join(t.TempDir(), "custom-audit.jsonl")
	if err := os.WriteFile(customAudit, []byte("{\"preserve\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewActivityCleanupService(activity, cleanupNow)
	plan, err := service.PlanSession(
		context.Background(),
		"ses_terminal",
		ActivityCleanupDisposableTerminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply disposable cleanup: %v", err)
	}
	if result.Status != ActivityCleanupAbsent ||
		len(result.Proofs) != 1 ||
		!result.Proofs[0].Owner.Equal(target) {
		t.Fatalf("disposable cleanup result = %#v", result)
	}
	if _, err := activity.Snapshot(
		context.Background(), target,
	); !errors.Is(err, workloadquery.ErrOwnerNotFound) {
		t.Fatalf("terminal owner still exists: %v", err)
	}
	for _, retained := range []workloadtypes.ActivityOwner{neighbor, reusable} {
		if _, err := activity.Snapshot(context.Background(), retained); err != nil {
			t.Fatalf("unrelated owner %s was deleted: %v", retained.Key(), err)
		}
	}
	data, err := os.ReadFile(customAudit)
	if err != nil || string(data) != "{\"preserve\":true}\n" {
		t.Fatalf("custom audit was changed: data=%q err=%v", data, err)
	}
}

func TestActivityCleanupRejectsCrossScopeAndTamperedPlans(t *testing.T) {
	t.Parallel()

	activity := openCleanupStore(t)
	owner := cleanupReusableOwner(t, "env_plan", "incarnation-plan")
	appendCleanupRecord(t, activity, owner, 1)
	service := NewActivityCleanupService(activity, cleanupNow)
	plan, err := service.PlanEnvironment(
		context.Background(), "env_plan", ActivityCleanupEnvironmentClean,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan.EnvironmentID = "env_other"
	if _, err := service.Apply(
		context.Background(), plan,
	); !errors.Is(err, ErrActivityCleanupPlanInvalid) {
		t.Fatalf("tampered plan error = %v", err)
	}
	if _, err := activity.Snapshot(context.Background(), owner); err != nil {
		t.Fatalf("tampered plan deleted evidence: %v", err)
	}
}

func openCleanupStore(t *testing.T) *workloadstore.Store {
	t.Helper()
	activity, err := workloadstore.Open(workloadstore.Options{
		Root:               filepath.Join(t.TempDir(), "activity"),
		ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes:      8 << 20,
		GlobalBytes:        32 << 20,
		Now:                cleanupNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activity.Close() })
	return activity
}

func cleanupNow() time.Time {
	return time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
}

func cleanupReusableOwner(
	t *testing.T,
	environmentID, incarnation string,
) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		environmentID, "lima", incarnation,
	)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func cleanupDisposableOwner(
	t *testing.T,
	sessionID, incarnation string,
) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewDisposableOwner(
		sessionID, "lima", incarnation,
	)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func appendCleanupRecord(
	t *testing.T,
	activity *workloadstore.Store,
	owner workloadtypes.ActivityOwner,
	sequence uint64,
) {
	t.Helper()
	sessionID := owner.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("ses_cleanup_%d", sequence)
	}
	at := cleanupNow().Add(time.Duration(sequence) * time.Second)
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     fmt.Sprintf("act_cleanup%08d", sequence),
		Owner:  owner, SessionID: sessionID,
		Kind: workloadtypes.ActivityFile, Operation: "read",
		Subject: workloadtypes.FileSubject{
			Kind:      workloadtypes.ActivityFile,
			Path:      fmt.Sprintf("/workspace/%d", sequence),
			PathState: "resolved", PathClass: "workspace",
			FileType: "regular",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      fmt.Sprintf("cov_cleanup%08d", sequence),
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	if err := activity.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

func ownerKeys(owners []workloadtypes.ActivityOwner) []string {
	result := make([]string, len(owners))
	for index := range owners {
		result[index] = owners[index].Key()
	}
	slices.Sort(result)
	return result
}
