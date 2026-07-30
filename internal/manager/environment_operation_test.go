package manager

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestEnvironmentActionOperationPrepareApplyAndTerminalRetry(t *testing.T) {
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	var proveCalls atomic.Int32
	service.ApplyStop = func(
		_ context.Context,
		plan EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		return EnvironmentActionResult{
			Plan: plan, Applied: append(
				[]EnvironmentActionTarget(nil), plan.Targets...,
			), Skipped: []EnvironmentActionTarget{},
		}, nil
	}
	service.Prove = func(
		_ context.Context,
		action string,
		snapshot environment.Record,
		target EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		proveCalls.Add(1)
		if action != EnvironmentActionStop ||
			snapshot.ID != record.ID || target.ID != record.ID {
			t.Fatal("prover received a different durable authority")
		}
		return []EvidenceRef{{
			Code: "backend-stopped-stable",
			Ref:  target.InstanceName,
		}}, nil
	}

	plan, err := service.Prepare(
		EnvironmentActionStop,
		EnvironmentActionOptions{IDs: []string{record.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.OperationID == "" || plan.PlanDigest == "" {
		t.Fatalf("prepared plan lacks durable identity: %+v", plan)
	}
	request := EnvironmentActionAPIRequest{
		IDs: []string{record.ID}, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Confirmed: true,
	}
	result, err := service.Apply(
		context.Background(), EnvironmentActionStop, request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation == nil ||
		result.Operation.Phase != OperationSucceeded ||
		executeCalls.Load() != 1 || proveCalls.Load() != 1 {
		t.Fatalf(
			"result=%+v execute=%d prove=%d",
			result, executeCalls.Load(), proveCalls.Load(),
		)
	}

	retry, err := service.Apply(
		context.Background(), EnvironmentActionStop, request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Operation == nil ||
		retry.Operation.ID != plan.OperationID ||
		executeCalls.Load() != 1 || proveCalls.Load() != 1 {
		t.Fatalf(
			"retry=%+v execute=%d prove=%d",
			retry, executeCalls.Load(), proveCalls.Load(),
		)
	}
}

func TestEnvironmentActionOperationPersistsSingleTargetOwnershipRefusal(
	t *testing.T,
) {
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	var proveCalls atomic.Int32
	service.ApplyStop = func(
		_ context.Context,
		plan EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		return EnvironmentActionResult{Plan: plan},
			&EnvironmentOwnerError{
				Code:          recovery.CodeEnvironmentActiveSessions,
				EnvironmentID: record.ID,
				ActiveOwners:  2,
			}
	}
	service.Prove = func(
		context.Context,
		string,
		environment.Record,
		EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		proveCalls.Add(1)
		return nil, errors.New("refused action must not enter provider proof")
	}
	plan, err := service.Prepare(
		EnvironmentActionStop,
		EnvironmentActionOptions{IDs: []string{record.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := EnvironmentActionAPIRequest{
		IDs:         []string{record.ID},
		OperationID: plan.OperationID,
		PlanDigest:  plan.PlanDigest,
		Confirmed:   true,
	}
	result, err := service.Apply(
		context.Background(),
		EnvironmentActionStop,
		request,
	)
	if EnvironmentRecoveryCode(err) !=
		recovery.CodeEnvironmentActiveSessions ||
		!strings.Contains(err.Error(), "2 active session") ||
		result.Operation == nil ||
		result.Operation.Phase != OperationFailed ||
		result.Operation.Result == nil ||
		result.Operation.Result.Code !=
			recovery.CodeEnvironmentActiveSessions ||
		len(result.Operation.Effects) != 1 ||
		result.Operation.Effects[0].Status != EffectFailed ||
		len(result.Operation.Effects[0].Evidence) != 1 ||
		result.Operation.Effects[0].Evidence[0].Code !=
			recovery.CodeEnvironmentActiveSessions ||
		executeCalls.Load() != 1 ||
		proveCalls.Load() != 0 {
		t.Fatalf(
			"result=%+v err=%v execute=%d prove=%d",
			result,
			err,
			executeCalls.Load(),
			proveCalls.Load(),
		)
	}
	replayed, replayErr := service.Apply(
		context.Background(),
		EnvironmentActionStop,
		request,
	)
	if EnvironmentRecoveryCode(replayErr) !=
		recovery.CodeEnvironmentActiveSessions ||
		replayed.Operation == nil ||
		replayed.Operation.Phase != OperationFailed ||
		executeCalls.Load() != 1 ||
		proveCalls.Load() != 0 {
		t.Fatalf(
			"replayed=%+v err=%v execute=%d prove=%d",
			replayed,
			replayErr,
			executeCalls.Load(),
			proveCalls.Load(),
		)
	}
}

func TestEnvironmentActionOperationLegacyApplyPreparesExactRequest(t *testing.T) {
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	service.ApplyStop = func(
		_ context.Context,
		plan EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		return EnvironmentActionResult{
			Plan: plan,
			Applied: append(
				[]EnvironmentActionTarget(nil), plan.Targets...,
			),
			Skipped: []EnvironmentActionTarget{},
		}, nil
	}
	service.Prove = func(
		context.Context,
		string,
		environment.Record,
		EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		return []EvidenceRef{{Code: "backend-stopped-stable"}}, nil
	}

	result, err := service.Apply(
		context.Background(),
		EnvironmentActionStop,
		EnvironmentActionAPIRequest{IDs: []string{record.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation == nil ||
		result.Operation.Phase != OperationSucceeded ||
		executeCalls.Load() != 1 {
		t.Fatalf("result=%+v execute=%d", result, executeCalls.Load())
	}
}

func TestEnvironmentActionOperationResponseLossUsesProofWithoutReplay(
	t *testing.T,
) {
	const providerCanary = "socks5://user:password@private.invalid:7890"
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	service.ApplyStop = func(
		context.Context,
		EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		return EnvironmentActionResult{}, errors.New(providerCanary)
	}
	service.Prove = func(
		context.Context,
		string,
		environment.Record,
		EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		return []EvidenceRef{{
			Code: "backend-stopped-stable",
		}}, nil
	}
	plan, err := service.Prepare(
		EnvironmentActionStop,
		EnvironmentActionOptions{IDs: []string{record.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := EnvironmentActionAPIRequest{
		IDs: []string{record.ID}, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Confirmed: true,
	}
	result, err := service.Apply(
		context.Background(), EnvironmentActionStop, request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation == nil ||
		result.Operation.Phase != OperationSucceeded ||
		executeCalls.Load() != 1 {
		t.Fatalf("result=%+v execute=%d", result, executeCalls.Load())
	}
	if _, err := service.Apply(
		context.Background(), EnvironmentActionStop, request,
	); err != nil {
		t.Fatal(err)
	}
	if executeCalls.Load() != 1 {
		t.Fatalf("provider replayed after response loss: %d", executeCalls.Load())
	}
	for _, path := range []string{
		service.operationStore().OperationPath(plan.OperationID),
		service.planPath(plan.OperationID),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), providerCanary) {
			t.Fatalf("durable operation leaked provider text in %s", path)
		}
	}
}

func TestEnvironmentActionOperationRecoveryProvesRunningEffectOnly(
	t *testing.T,
) {
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	var proofReady atomic.Bool
	service.ApplyStop = func(
		context.Context,
		EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		return EnvironmentActionResult{}, errors.New(
			"provider response unavailable",
		)
	}
	service.Prove = func(
		context.Context,
		string,
		environment.Record,
		EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		if !proofReady.Load() {
			return nil, errors.New("inventory unavailable")
		}
		return []EvidenceRef{{
			Code: "backend-stopped-stable",
		}}, nil
	}
	plan, err := service.Prepare(
		EnvironmentActionStop,
		EnvironmentActionOptions{IDs: []string{record.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		EnvironmentActionStop,
		EnvironmentActionAPIRequest{
			IDs: []string{record.ID}, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Confirmed: true,
		},
	)
	if !errors.Is(err, ErrOperationTerminalUnproved) ||
		result.Operation == nil ||
		result.Operation.Phase != OperationRecoveryRequired ||
		result.Operation.Recovery.Code != "lifecycle-terminal-unproved" ||
		executeCalls.Load() != 1 {
		t.Fatalf(
			"result=%+v err=%v execute=%d",
			result, err, executeCalls.Load(),
		)
	}
	proofReady.Store(true)
	operation, err := service.ReconcileOperation(
		context.Background(), plan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != OperationSucceeded ||
		executeCalls.Load() != 1 {
		t.Fatalf(
			"operation=%+v execute=%d",
			operation, executeCalls.Load(),
		)
	}
}

func TestEnvironmentDeleteOperationBindsForceAndExactPlan(t *testing.T) {
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	service.ApplyDelete = func(
		_ context.Context,
		plan EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		if !plan.Force {
			t.Fatal("delete executor lost reviewed force authority")
		}
		return EnvironmentActionResult{
			Plan: plan,
			Applied: append(
				[]EnvironmentActionTarget(nil), plan.Targets...,
			),
			Skipped: []EnvironmentActionTarget{},
		}, nil
	}
	service.Prove = func(
		context.Context,
		string,
		environment.Record,
		EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		return []EvidenceRef{{
			Code: "backend-absent-stable",
		}}, nil
	}
	plan, err := service.Prepare(
		EnvironmentActionDelete,
		EnvironmentActionOptions{
			IDs: []string{record.ID}, Force: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != EnvironmentActionDelete || !plan.Force {
		t.Fatalf("delete plan lost authority: %+v", plan)
	}
	_, err = service.Apply(
		context.Background(),
		EnvironmentActionDelete,
		EnvironmentActionAPIRequest{
			IDs: []string{record.ID}, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Confirmed: true,
		},
	)
	if !errors.Is(err, ErrOperationMismatch) ||
		executeCalls.Load() != 0 {
		t.Fatalf("force mismatch err=%v execute=%d", err, executeCalls.Load())
	}
	result, err := service.Apply(
		context.Background(),
		EnvironmentActionDelete,
		EnvironmentActionAPIRequest{
			IDs: []string{record.ID}, Force: true,
			OperationID: plan.OperationID,
			PlanDigest:  plan.PlanDigest,
			Confirmed:   true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation == nil ||
		result.Operation.Phase != OperationSucceeded ||
		executeCalls.Load() != 1 {
		t.Fatalf("result=%+v execute=%d", result, executeCalls.Load())
	}
}

func TestEnvironmentActionReconcileResumesAcceptedPendingEffect(
	t *testing.T,
) {
	service, record := newEnvironmentActionTestService(t)
	var executeCalls atomic.Int32
	service.ApplyStop = func(
		_ context.Context,
		plan EnvironmentActionPlan,
	) (EnvironmentActionResult, error) {
		executeCalls.Add(1)
		return EnvironmentActionResult{
			Plan: plan, Applied: cloneEnvironmentActionTargets(
				plan.Targets,
			), Skipped: []EnvironmentActionTarget{},
		}, nil
	}
	service.Prove = func(
		context.Context,
		string,
		environment.Record,
		EnvironmentActionTarget,
	) ([]EvidenceRef, error) {
		return []EvidenceRef{{
			Code: "backend-stopped-stable",
		}}, nil
	}
	plan, err := service.Prepare(
		EnvironmentActionStop,
		EnvironmentActionOptions{IDs: []string{record.ID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.operations().Transition(
		plan.OperationID,
		OperationClaimed,
	); err != nil {
		t.Fatal(err)
	}
	operation, err := service.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != OperationSucceeded ||
		executeCalls.Load() != 1 {
		t.Fatalf(
			"operation=%+v execute=%d",
			operation, executeCalls.Load(),
		)
	}
}

func newEnvironmentActionTestService(
	t *testing.T,
) (*EnvironmentActionService, environment.Record) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	environmentStore := environment.Store{Root: store.Root}
	record, err := environmentStore.Create(environment.Spec{
		Name: "operation-test", ImageRef: environment.BuiltinBaseImage,
		Profile: "default", Backend: "lima",
		Mode:                environment.ModeWorkspaceBound,
		MachineIdentityID:   testEnvironmentMachineIdentityID(),
		BootConfigurationID: testEnvironmentBootConfigurationID(),
		BoundWorkspace:      t.TempDir(), BoundGuestRoot: "/workspace",
		InstanceName: "hideout-operation-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = environment.StatusRunning
	record.LastStartedAt = time.Date(
		2026, 7, 29, 0, 0, 0, 0, time.UTC,
	)
	if err := environmentStore.Save(record); err != nil {
		t.Fatal(err)
	}
	return &EnvironmentActionService{Core: New(store)}, record
}
