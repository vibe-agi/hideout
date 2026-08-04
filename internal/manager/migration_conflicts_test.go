package manager

import (
	"context"
	"errors"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationConflictDefaultsToRefusalAndAcceptsOnlyProvedSeparateDelete(
	t *testing.T,
) {
	actions, existing := newEnvironmentActionTestService(t)
	existing.Status = environment.StatusStopped
	store := environment.Store{Root: actions.Core.Store.Root}
	if err := store.Save(existing); err != nil {
		t.Fatal(err)
	}
	service := MigrationImportService{MigrationService: MigrationService{
		Store: MigrationStore{Root: actions.Core.Store.Root}, Environments: store,
	}}
	object := migration.ImportObject{
		SourceRef: "environment_source1", DestinationName: existing.Name,
		Mode: migration.ExportModeConfig, DiskRefs: []migration.OpaqueID{},
	}

	planned, blockers, err := service.planMigrationConflicts(
		migration.ImportDraft{}, []migration.ImportObject{object},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 1 || len(blockers) != 1 ||
		planned[0].Decision != migrationConflictDecisionRefuse ||
		planned[0].Destructive ||
		blockers[0].Code != "migration.destination.name_conflict" {
		t.Fatalf("default actions=%+v blockers=%+v", planned, blockers)
	}

	actions.ApplyDelete = func(
		_ context.Context,
		plan EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		for _, target := range plan.Targets {
			if err := store.Remove(target.ID); err != nil {
				return EnvironmentActionResult{Plan: plan}, err
			}
		}
		return EnvironmentActionResult{
			Plan: plan, Applied: cloneEnvironmentActionTargets(plan.Targets),
			Skipped: []EnvironmentActionTarget{},
		}, nil
	}
	actions.Prove = func(
		_ context.Context,
		action string,
		snapshot environment.Record,
		target EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		if action != EnvironmentActionDelete || snapshot.ID != existing.ID ||
			target.ID != existing.ID {
			return nil, errors.New("unexpected delete proof authority")
		}
		return []EvidenceRef{{Code: "backend-absent-stable"}}, nil
	}
	deletePlan, err := actions.Prepare(
		EnvironmentActionDelete,
		EnvironmentActionOptions{IDs: []string{existing.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	deleteResult, err := actions.Apply(
		context.Background(), EnvironmentActionDelete,
		EnvironmentActionAPIRequest{
			IDs: []string{existing.ID}, OperationID: deletePlan.OperationID,
			PlanDigest: deletePlan.PlanDigest, Confirmed: true,
		},
	)
	if err != nil || deleteResult.Operation == nil ||
		deleteResult.Operation.Phase != OperationSucceeded {
		t.Fatalf("delete result=%+v error=%v", deleteResult, err)
	}
	decision := migration.ConflictDecision{
		SourceRef: object.SourceRef, Decision: migrationConflictDecisionReplace,
		LifecycleOperationID: deletePlan.OperationID,
		LifecyclePlanDigest:  migration.Digest(deletePlan.PlanDigest),
	}
	planned, blockers, err = service.planMigrationConflicts(
		migration.ImportDraft{ConflictDecisions: []migration.ConflictDecision{decision}},
		[]migration.ImportObject{object},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 || len(planned) != 1 ||
		planned[0].Decision != migrationConflictDecisionReplace ||
		!planned[0].Destructive ||
		planned[0].LifecycleOperationID != deletePlan.OperationID ||
		planned[0].Recovery == "" {
		t.Fatalf("replacement actions=%+v blockers=%+v", planned, blockers)
	}

	decision.LifecyclePlanDigest = migration.Digest(
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	)
	if _, _, err := service.planMigrationConflicts(
		migration.ImportDraft{ConflictDecisions: []migration.ConflictDecision{decision}},
		[]migration.ImportObject{object},
	); !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("mismatched delete evidence error=%v", err)
	}
}

func TestMigrationConflictReplacementNeverDeletesAReplacementOccupant(t *testing.T) {
	actions, existing := newEnvironmentActionTestService(t)
	existing.Status = environment.StatusStopped
	store := environment.Store{Root: actions.Core.Store.Root}
	if err := store.Save(existing); err != nil {
		t.Fatal(err)
	}
	service := MigrationImportService{MigrationService: MigrationService{
		Store: MigrationStore{Root: actions.Core.Store.Root}, Environments: store,
	}}
	object := migration.ImportObject{
		SourceRef: "environment_source1", DestinationName: existing.Name,
		Mode: migration.ExportModeConfig, DiskRefs: []migration.OpaqueID{},
	}
	decision := migration.ConflictDecision{
		SourceRef: object.SourceRef, Decision: migrationConflictDecisionReplace,
		LifecycleOperationID: "op_missingreplacement1",
		LifecyclePlanDigest: migration.Digest(
			"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		),
	}
	if _, _, err := service.planMigrationConflicts(
		migration.ImportDraft{ConflictDecisions: []migration.ConflictDecision{decision}},
		[]migration.ImportObject{object},
	); !errors.Is(err, ErrMigrationPlanStale) {
		t.Fatalf("current owner accepted stale replacement evidence: %v", err)
	}
	if current, err := store.Load(existing.ID); err != nil || current.ID != existing.ID {
		t.Fatalf("conflict planning changed current owner: current=%+v err=%v", current, err)
	}
}
