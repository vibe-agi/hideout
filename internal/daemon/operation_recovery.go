package daemon

import (
	"context"
	"fmt"
	"sort"

	"github.com/vibe-agi/hideout/internal/manager"
)

const maxStartupRecoveryOperations = 1024

type startupOperationReconciler func(
	context.Context,
	string,
) (manager.Operation, error)

// startupOperationRecovery reconciles durable, already accepted Manager
// operations before the daemon begins serving clients. Provider errors are
// deliberately converted to stable recovery metadata; their raw text is never
// written to the operation ledger or audit log.
type startupOperationRecovery struct {
	Store                manager.OperationStore
	Operations           manager.OperationService
	ReconcileEnvironment startupOperationReconciler
	ReconcileProfile     startupOperationReconciler
	ReconcileSecret      startupOperationReconciler
	ReconcileNetwork     startupOperationReconciler
	Record               func(string, string, map[string]any)
}

func (recovery startupOperationRecovery) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	operations, err := recovery.Store.List(maxStartupRecoveryOperations)
	if err != nil {
		return fmt.Errorf("list durable operations for startup recovery: %w", err)
	}
	sort.Slice(operations, func(left, right int) bool {
		if operations[left].CreatedAt.Equal(operations[right].CreatedAt) {
			return operations[left].ID < operations[right].ID
		}
		return operations[left].CreatedAt.Before(operations[right].CreatedAt)
	})
	for _, operation := range operations {
		if operation.Terminal() || operation.Phase == manager.OperationPlanned {
			continue
		}
		if err := recovery.reconcileOne(ctx, operation); err != nil {
			return err
		}
	}
	return nil
}

func (recovery startupOperationRecovery) reconcileOne(
	ctx context.Context,
	before manager.Operation,
) error {
	reconcile := recovery.reconciler(before.Kind)
	if reconcile == nil {
		operation, err := recovery.Operations.RequireRecovery(
			before.ID,
			"operation-provider-unavailable",
			"The daemon cannot reconcile this accepted operation because its provider is unavailable.",
			"Upgrade or repair the provider, then retry the same operation ID.",
		)
		if err != nil {
			return fmt.Errorf(
				"mark operation %s provider unavailable: %w",
				before.ID,
				err,
			)
		}
		recovery.record(operation, "deny")
		return nil
	}

	_, _ = reconcile(ctx, before.ID)
	current, err := recovery.Store.Load(before.ID)
	if err != nil {
		return fmt.Errorf(
			"reload operation %s after startup reconciliation: %w",
			before.ID,
			err,
		)
	}
	if current.Terminal() {
		recovery.record(current, "allow")
		return nil
	}
	if current.Phase == manager.OperationRecoveryRequired ||
		hasProviderRecovery(current) {
		recovery.record(current, "deny")
		return nil
	}

	if operationHasRunningEffect(current) {
		current, err = recovery.Operations.RequireRecovery(
			current.ID,
			"provider-completion-unproved",
			"The provider may have committed this effect, but startup reconciliation could not prove completion.",
			"Repair or unlock the provider, then retry the same operation ID.",
		)
	} else {
		current, err = recovery.Operations.SetRecovery(
			current.ID,
			"operation-retry-required",
			"Startup reconciliation could not finish this accepted operation.",
			"Retry the same operation ID after its provider is available.",
		)
	}
	if err != nil {
		return fmt.Errorf(
			"persist startup recovery for operation %s: %w",
			before.ID,
			err,
		)
	}
	recovery.record(current, "deny")
	return nil
}

func (recovery startupOperationRecovery) reconciler(
	kind string,
) startupOperationReconciler {
	if manager.IsEnvironmentActionOperationKind(kind) {
		return recovery.ReconcileEnvironment
	}
	if manager.IsNetworkTransitionOperationKind(kind) {
		return recovery.ReconcileNetwork
	}
	switch kind {
	case "profile.transaction":
		return recovery.ReconcileProfile
	case "secret.set", "secret.rotate", "secret.delete":
		return recovery.ReconcileSecret
	default:
		return nil
	}
}

func (recovery startupOperationRecovery) record(
	operation manager.Operation,
	decision string,
) {
	if recovery.Record == nil {
		return
	}
	recovery.Record(
		"operation.startup-reconcile",
		decision,
		map[string]any{
			"operationId":  operation.ID,
			"kind":         operation.Kind,
			"phase":        operation.Phase,
			"recoveryCode": operation.Recovery.Code,
		},
	)
}

func operationHasRunningEffect(operation manager.Operation) bool {
	for _, effect := range operation.Effects {
		if effect.Status == manager.EffectRunning {
			return true
		}
	}
	return false
}

func hasProviderRecovery(operation manager.Operation) bool {
	return operation.Recovery.Code != "" &&
		operation.Recovery.Code != "retry-operation"
}
