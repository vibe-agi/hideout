package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

func lifecycleEnvironmentRecords(store profile.Store, enabled bool) ([]environment.Record, []string, error) {
	if !enabled {
		return nil, nil, nil
	}
	records, err := (environment.Store{Root: store.Root}).List()
	if err != nil {
		return nil, nil, fmt.Errorf("daemon lifecycle inventory: %w", err)
	}
	result := make([]environment.Record, 0, len(records))
	known := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.Backend == "lima" && record.InstanceName != "" && record.Status != environment.StatusUnsupportedVersion {
			result = append(result, record)
			known[record.ID] = struct{}{}
		}
	}
	journalIDs, err := (lifecycle.JournalStore{Root: store.Root}).ListEnvironmentIDs()
	if err != nil {
		return nil, nil, fmt.Errorf("daemon lifecycle journal inventory: %w", err)
	}
	missing := make([]string, 0)
	for _, environmentID := range journalIDs {
		if _, ok := known[environmentID]; !ok {
			missing = append(missing, environmentID)
		}
	}
	return result, missing, nil
}

func (d *Daemon) startLifecycleReconciliation(records []environment.Record) {
	for _, record := range records {
		d.launchLifecycleReconciliation(record)
	}
}

func (d *Daemon) launchLifecycleReconciliation(record environment.Record) {
	if d == nil || d.lifecycle == nil || d.lifecycleBackend == nil || d.lifecycleCtx == nil {
		return
	}
	d.lifecycleWG.Add(1)
	go func() {
		defer d.lifecycleWG.Done()
		select {
		case d.lifecycleSlots <- struct{}{}:
			defer func() { <-d.lifecycleSlots }()
		case <-d.lifecycleCtx.Done():
			_ = d.lifecycle.BlockReconciliation(record.ID, "daemon-shutdown")
			return
		}
		d.reconcileLifecycleRecord(d.lifecycleCtx, record)
		if d.lifecycleCtx.Err() == nil {
			d.recoverPendingEnvironmentRemoval(d.lifecycleCtx, record)
			d.reconcilePendingEnvironmentActions(d.lifecycleCtx)
		}
	}()
}

func (d *Daemon) reconcilePendingEnvironmentActions(ctx context.Context) {
	if d == nil || d.environmentActions == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	if err := d.environmentActions.ReconcilePending(ctx); err != nil {
		d.audit.record("operation.lifecycle-reconcile", "deny", map[string]any{
			"reasonCode": "operation-ledger-unavailable",
		})
	}
}

func (d *Daemon) reconcileLifecycleRecord(ctx context.Context, record environment.Record) {
	provider, err := d.lifecycleBackend(record)
	if err != nil {
		_ = d.lifecycle.BlockReconciliation(record.ID, "backend-provider-unavailable")
		d.audit.record("lifecycle.reconcile", "deny", map[string]any{"environmentId": record.ID, "reasonCode": "backend-provider-unavailable"})
		return
	}
	observeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	observation := observeLifecycleForReconciliation(
		observeCtx, provider, record.InstanceName,
	)
	cancel()
	owners, residualReasons := reconcileRestartResidue(ctx, environment.Store{Root: d.store.Root}, record, observation, provider)
	ownerIDs := make([]string, 0, len(owners))
	for _, owner := range owners {
		ownerIDs = append(ownerIDs, owner.SessionID)
	}
	// A stale failed/cleaning owner has an absent kernel owner and is represented
	// by a catalog-typed session orphan for explicit co-termination recovery.
	// Every other residual is unclassified provider state and must block both
	// automatic and explicit stop.
	additionalUnproved := lifecycleResidualRequiresProviderProof(residualReasons)
	if observation.State == backend.LifecycleStopped &&
		!additionalUnproved && len(ownerIDs) == 0 {
		if err := d.reconcileStoppedEnvironmentRecord(
			ctx,
			record,
		); err != nil {
			_ = d.lifecycle.BlockReconciliation(
				record.ID,
				"stopped-metadata-convergence-failed",
			)
			d.audit.record(
				"lifecycle.reconcile",
				"deny",
				map[string]any{
					"environmentId": record.ID,
					"reasonCode":    "stopped-metadata-convergence-failed",
				},
			)
			return
		}
	}
	// Publish reconciliation completion only after the durable environment
	// record and runtime metadata have converged. Otherwise a caller can observe
	// "complete", plan against the old record, and immediately lose the clean
	// operation to the stale-plan guard.
	if err := d.lifecycle.Reconcile(ctx, lifecycle.ReconcileInput{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, Observation: observation,
		OwnerSessionIDs: ownerIDs, AdditionalUnproved: additionalUnproved,
	}); err != nil {
		_ = d.lifecycle.BlockReconciliation(record.ID, "reconciliation-failed")
		d.audit.record("lifecycle.reconcile", "deny", map[string]any{"environmentId": record.ID, "reasonCode": "reconciliation-failed"})
		return
	}
	decision := "allow"
	if observation.State == backend.LifecycleUnknown || additionalUnproved || len(ownerIDs) != 0 {
		decision = "deny"
	}
	d.audit.record("lifecycle.reconcile", decision, map[string]any{
		"environmentId": record.ID, "backendState": observation.State,
		"ownerRecords": len(ownerIDs), "additionalUnproved": additionalUnproved,
		"residualReasons": residualReasons,
	})
}

func (d *Daemon) reconcileStoppedEnvironmentRecord(
	ctx context.Context,
	observed environment.Record,
) (resultErr error) {
	store := environment.Store{Root: d.store.Root}
	lock, err := store.LockContext(ctx, observed.ID)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lock.Unlock())
	}()
	current, err := store.Load(observed.ID)
	if err != nil {
		return err
	}
	identity, err := environment.NewRemovalIdentity(observed)
	if err != nil || !identity.MatchesRecord(current) ||
		current.InstanceName != observed.InstanceName {
		return errors.New(
			"stopped environment metadata identity changed",
		)
	}
	if current.Status != environment.StatusStopped {
		current.Status = environment.StatusStopped
		if err := store.Save(current); err != nil {
			return err
		}
	}
	if err := errors.Join(
		store.ClearRuntimeServices(current.ID),
		backend.RemoveActivationReceipt(store.RuntimeDir(current.ID)),
	); err != nil {
		return err
	}
	if d.networkGateways != nil {
		if err := d.networkGateways.CloseEnvironment(current.ID); err != nil {
			return err
		}
	}
	return nil
}

func observeLifecycleForReconciliation(
	ctx context.Context,
	provider manager.EnvironmentLifecycleBackend,
	instanceName string,
) backend.LifecycleObservation {
	initial := provider.ObserveLifecycle(ctx, instanceName)
	if initial.Validate() != nil || initial.InstanceName != instanceName {
		return lifecycleReconciliationUnknown(
			instanceName, "backend-observation-invalid",
		)
	}
	if initial.State != backend.LifecycleStopped &&
		initial.State != backend.LifecycleAbsent {
		return initial
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return lifecycleReconciliationUnknown(
			instanceName, "reconciliation-cancelled",
		)
	case <-timer.C:
	}
	confirmation := provider.ObserveLifecycle(ctx, instanceName)
	if confirmation.Validate() != nil ||
		confirmation.InstanceName != instanceName ||
		confirmation.State != initial.State {
		return lifecycleReconciliationUnknown(
			instanceName, "backend-terminal-observation-unstable",
		)
	}
	return confirmation
}

func lifecycleReconciliationUnknown(
	instanceName, reasonCode string,
) backend.LifecycleObservation {
	return backend.LifecycleObservation{
		State: backend.LifecycleUnknown, InstanceName: instanceName,
		ObservedAt: time.Now().UTC(), ReasonCode: reasonCode,
	}
}

func lifecycleResidualRequiresProviderProof(reasons []string) bool {
	return slices.ContainsFunc(reasons, func(reason string) bool {
		return reason != "owner-requires-explicit-recovery"
	})
}

func (d *Daemon) retryLifecycleReconciliation(ctx context.Context, environmentID string) (bool, lifecycle.Status, error) {
	if d == nil || d.lifecycle == nil || d.lifecycleBackend == nil {
		return false, lifecycle.Status{}, errors.New("daemon lifecycle reconciliation is unavailable")
	}
	record, err := (environment.Store{Root: d.store.Root}).Load(environmentID)
	if err != nil {
		return false, lifecycle.Status{}, err
	}
	if record.Backend != "lima" || record.InstanceName == "" || record.Status == environment.StatusUnsupportedVersion {
		return false, lifecycle.Status{}, errors.New("environment does not support lifecycle reconciliation")
	}
	started, err := d.lifecycle.BeginReconciliation(ctx, environmentID)
	if err != nil {
		return false, lifecycle.Status{}, err
	}
	if started {
		d.launchLifecycleReconciliation(record)
	}
	for _, status := range d.lifecycle.Snapshot() {
		if status.EnvironmentID == environmentID {
			return started, status, nil
		}
	}
	return started, lifecycle.Status{}, errors.New("lifecycle status is unavailable")
}

// applyEnvironmentStopPlan is the daemon's single stop entry for Manager API,
// background work, and CLI-triggered plans. Lima targets use the lifecycle
// coordinator; backends without an incarnation contract retain Manager's
// existing direct plan/apply path.
func (d *Daemon) applyEnvironmentStopPlan(ctx context.Context, plan manager.EnvironmentActionPlan) (manager.EnvironmentActionResult, error) {
	result := manager.EnvironmentActionResult{
		Plan: plan, Applied: []manager.EnvironmentActionTarget{},
		Skipped: append([]manager.EnvironmentActionTarget(nil), plan.Skipped...),
	}
	for _, target := range plan.Targets {
		if target.Backend == "lima" {
			status, err := d.lifecycle.StopExplicit(ctx, target.ID)
			if err != nil {
				return result, d.environmentStopError(target.ID, status, err)
			}
			if status.Activity != lifecycle.ActivityStopped {
				return result, errors.New("lifecycle stop did not prove the environment stopped")
			}
			target.Status = "stopped"
			result.Applied = append(result.Applied, target)
			continue
		}
		direct := plan
		direct.Targets = []manager.EnvironmentActionTarget{target}
		direct.Skipped = nil
		direct.Total = 1
		applied, err := d.api.Core.ApplyEnvironmentStop(ctx, direct, manager.EnvironmentApplyOptions{Operator: d.api.EnvOperator})
		if err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, applied.Applied...)
		result.Skipped = append(result.Skipped, applied.Skipped...)
	}
	// Release the per-environment host-loopback egress gateway for every stopped
	// environment. The environment network is retained across runs for same-boot
	// reuse, but once the environment stops its gateway (listener + accept
	// goroutine + credentials + live egress dialer) must be closed rather than
	// lingering — a functional authenticated proxy — until daemon shutdown.
	// CloseEnvironment is a no-op for environments without a staged gateway.
	if d.api.Core.NetworkGateways != nil {
		for _, target := range result.Applied {
			_ = d.api.Core.NetworkGateways.CloseEnvironment(target.ID)
		}
	}
	return result, nil
}

// environmentStopError preserves a safe, operator-facing ownership refusal
// across the lifecycle/Manager boundary. Without this mapping, the durable
// operation layer cannot distinguish "nothing was stopped because live
// sessions still own the environment" from a provider response loss after a
// stop may have taken effect.
func (d *Daemon) environmentStopError(
	environmentID string,
	status lifecycle.Status,
	stopErr error,
) error {
	if d == nil || !errors.Is(
		stopErr,
		lifecycle.ErrMutationBlockedByActivity,
	) {
		return stopErr
	}
	store := environment.Store{Root: d.store.Root}
	owners, err := runsession.ListOwners(store.OwnerRoot(environmentID))
	if err != nil {
		return &manager.EnvironmentOwnerError{
			Code:          recovery.CodeSessionOwnerUnprovable,
			EnvironmentID: environmentID,
			Err:           runsession.ErrOwnerUnprovable,
		}
	}
	active := 0
	for _, owner := range owners {
		switch owner.Status {
		case runsession.OwnerLive:
			active++
		case runsession.OwnerUnprovable:
			return &manager.EnvironmentOwnerError{
				Code:          recovery.CodeSessionOwnerUnprovable,
				EnvironmentID: environmentID,
				Err:           runsession.ErrOwnerUnprovable,
			}
		}
	}
	if status.EstablishingSessions > active {
		active = status.EstablishingSessions
	}
	if active == 0 {
		return stopErr
	}
	return &manager.EnvironmentOwnerError{
		Code:          recovery.CodeEnvironmentActiveSessions,
		EnvironmentID: environmentID,
		ActiveOwners:  active,
	}
}

// applyEnvironmentCleanPlan routes Lima removal through Manager's durable
// disposal protocol. Backends without an observable incarnation retain the
// direct path, but activity is still proved absent before their record can be
// removed.
func (d *Daemon) applyEnvironmentCleanPlan(ctx context.Context, plan manager.EnvironmentActionPlan) (manager.EnvironmentActionResult, error) {
	result := manager.EnvironmentActionResult{
		Plan: plan, Applied: []manager.EnvironmentActionTarget{},
		Skipped: append([]manager.EnvironmentActionTarget(nil), plan.Skipped...),
	}
	environmentStore := environment.Store{Root: d.store.Root}
	for _, target := range plan.Targets {
		if target.Backend == "lima" && strings.TrimSpace(target.InstanceName) != "" {
			if d.lifecycleBackend == nil {
				return result, errors.New("daemon lifecycle backend is unavailable")
			}
			record, err := environmentStore.Load(target.ID)
			if err != nil {
				return result, err
			}
			if !environmentCleanTargetMatchesRecord(target, record) {
				return result, errors.New("environment clean plan is stale")
			}
			identity, err := environment.NewRemovalIdentity(record)
			if err != nil {
				return result, err
			}
			provider, err := d.lifecycleBackend(record)
			if err != nil {
				return result, err
			}
			outcome, err := d.api.Core.RecoverEnvironmentClean(
				ctx,
				manager.EnvironmentCleanRecoveryRequest{
					Identity: identity,
					Source:   manager.EnvironmentCleanRecoverySourceExplicit,
					Provider: provider,
					ActivityCleanup: func(
						cleanupCtx context.Context,
						cleanupRecord environment.Record,
					) error {
						return d.cleanupEnvironmentRemovalActivity(
							cleanupCtx,
							cleanupRecord.ID,
							cleanupRecord.LastSessionID,
							manager.ActivityCleanupEnvironmentClean,
						)
					},
				},
			)
			if err != nil {
				return result, err
			}
			if outcome.Status != manager.DisposableRecoveryRemoved ||
				!outcome.RecordRemoved || !outcome.JournalRemoved ||
				!outcome.ActivityRemoved {
				return result, errors.New("environment clean terminal evidence is incomplete")
			}
			result.Applied = append(result.Applied, target)
			continue
		}

		direct := plan
		direct.Targets = []manager.EnvironmentActionTarget{target}
		direct.Skipped = nil
		direct.Total = 1
		record, err := environmentStore.Load(target.ID)
		if err != nil {
			return result, err
		}
		if !environmentCleanTargetMatchesRecord(target, record) {
			return result, errors.New("environment clean plan is stale")
		}
		if err := d.cleanupEnvironmentRemovalActivity(
			ctx, record.ID, record.LastSessionID,
			manager.ActivityCleanupEnvironmentClean,
		); err != nil {
			return result, err
		}
		applied, err := d.api.Core.ApplyEnvironmentClean(
			ctx, direct, manager.EnvironmentApplyOptions{},
		)
		result.Applied = append(result.Applied, applied.Applied...)
		result.Skipped = append(result.Skipped, applied.Skipped...)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func environmentCleanTargetMatchesRecord(
	target manager.EnvironmentActionTarget,
	record environment.Record,
) bool {
	return target.ID == record.ID &&
		target.Profile == record.Profile &&
		target.Backend == record.Backend &&
		target.Status == record.Status &&
		target.InstanceName == record.InstanceName &&
		target.LastSessionID == record.LastSessionID &&
		target.LastCommand == record.LastCommand &&
		target.CreatedAt.Equal(record.CreatedAt) &&
		target.LastStartedAt.Equal(record.LastStartedAt) &&
		target.LastEndedAt.Equal(record.LastEndedAt)
}

func (d *Daemon) cleanupEnvironmentRemovalActivity(
	ctx context.Context,
	environmentID, sessionID, operation string,
) error {
	if d == nil || d.activityCleanup == nil {
		return errors.New("daemon activity cleanup is unavailable")
	}
	if operation != manager.ActivityCleanupDisposableTerminal {
		plan, err := d.activityCleanup.PlanEnvironment(
			ctx, environmentID, operation,
		)
		if err != nil {
			return err
		}
		cleanupResult, cleanupErr := d.activityCleanup.Apply(ctx, plan)
		d.recordActivityCleanup(cleanupResult, cleanupErr)
		if cleanupErr != nil {
			return cleanupErr
		}
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	plan, err := d.activityCleanup.PlanSession(ctx, sessionID, operation)
	if err != nil {
		return err
	}
	cleanupResult, cleanupErr := d.activityCleanup.Apply(ctx, plan)
	d.recordActivityCleanup(cleanupResult, cleanupErr)
	return cleanupErr
}

func (d *Daemon) proveEnvironmentAction(
	ctx context.Context,
	action string,
	record environment.Record,
	target manager.EnvironmentActionTarget,
) ([]manager.EvidenceRef, error) {
	if d == nil || d.lifecycleBackend == nil ||
		!environmentCleanTargetMatchesRecord(target, record) {
		return nil, errors.New(
			"environment lifecycle proof identity is unavailable",
		)
	}
	provider, err := d.lifecycleBackend(record)
	if err != nil {
		return nil, errors.New(
			"environment lifecycle proof provider is unavailable",
		)
	}
	proofCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	observation := observeLifecycleForReconciliation(
		proofCtx, provider, record.InstanceName,
	)
	cancel()
	if observation.State == backend.LifecycleUnknown {
		return nil, errors.New(
			"environment lifecycle terminal observation is unproved",
		)
	}
	evidence := []manager.EvidenceRef{}
	switch action {
	case manager.EnvironmentActionStop:
		if observation.State != backend.LifecycleStopped {
			return nil, errors.New(
				"environment stop lacks stable stopped evidence",
			)
		}
		current, err := (environment.Store{Root: d.store.Root}).Load(record.ID)
		if err != nil || current.Status != environment.StatusStopped ||
			current.InstanceName != record.InstanceName {
			return nil, errors.New(
				"environment stop metadata is unproved",
			)
		}
		statusProved := false
		for _, status := range d.lifecycle.Snapshot() {
			if status.EnvironmentID == record.ID &&
				status.Activity == lifecycle.ActivityStopped {
				statusProved = true
				break
			}
		}
		if !statusProved {
			return nil, errors.New(
				"environment lifecycle stop commit is unproved",
			)
		}
		evidence = append(evidence,
			manager.EvidenceRef{
				Code: "backend-stopped-stable",
				Ref:  record.InstanceName, ObservedAt: observation.ObservedAt,
			},
			manager.EvidenceRef{
				Code: "environment-record-stopped",
				Ref:  record.ID, ObservedAt: time.Now().UTC(),
			},
			manager.EvidenceRef{
				Code: "lifecycle-stop-committed",
				Ref:  record.ID, ObservedAt: time.Now().UTC(),
			},
		)
	case manager.EnvironmentActionClean, manager.EnvironmentActionDelete:
		if observation.State != backend.LifecycleAbsent {
			return nil, errors.New(
				"environment removal lacks stable absence evidence",
			)
		}
		records, err := (environment.Store{Root: d.store.Root}).List()
		if err != nil || slices.ContainsFunc(
			records,
			func(candidate environment.Record) bool {
				return candidate.ID == record.ID
			},
		) {
			return nil, errors.New(
				"environment removal record absence is unproved",
			)
		}
		if _, err := (lifecycle.JournalStore{
			Root: d.store.Root,
		}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New(
				"environment removal journal absence is unproved",
			)
		}
		activityOperation := manager.ActivityCleanupEnvironmentClean
		if action == manager.EnvironmentActionDelete {
			activityOperation = manager.ActivityCleanupEnvironmentDelete
		}
		activityPlan, err := d.activityCleanup.PlanEnvironment(
			ctx, record.ID, activityOperation,
		)
		if err != nil || len(activityPlan.Owners) != 0 {
			return nil, errors.New(
				"environment activity absence is unproved",
			)
		}
		if record.LastSessionID != "" {
			sessionPlan, err := d.activityCleanup.PlanSession(
				ctx, record.LastSessionID, activityOperation,
			)
			if err != nil || len(sessionPlan.Owners) != 0 {
				return nil, errors.New(
					"environment session activity absence is unproved",
				)
			}
		}
		evidence = append(evidence,
			manager.EvidenceRef{
				Code: "backend-absent-stable",
				Ref:  record.InstanceName, ObservedAt: observation.ObservedAt,
			},
			manager.EvidenceRef{
				Code: "environment-record-absent",
				Ref:  record.ID, ObservedAt: time.Now().UTC(),
			},
			manager.EvidenceRef{
				Code: "lifecycle-journal-absent",
				Ref:  record.ID, ObservedAt: time.Now().UTC(),
			},
			manager.EvidenceRef{
				Code: "activity-owner-absent",
				Ref:  record.ID, ObservedAt: time.Now().UTC(),
			},
		)
	default:
		return nil, errors.New("environment lifecycle proof action is invalid")
	}
	return evidence, nil
}

func (d *Daemon) applyEnvironmentMutation(ctx context.Context, environmentID, operation string, force bool) (environment.Record, error) {
	if d == nil || d.lifecycle == nil || d.lifecycleBackend == nil {
		return environment.Record{}, errors.New("daemon lifecycle mutation authority is unavailable")
	}
	store := environment.Store{Root: d.store.Root}
	record, err := store.Load(environmentID)
	if err != nil {
		return environment.Record{}, err
	}
	if record.Backend != "lima" {
		return environment.Record{}, errors.New("daemon lifecycle mutation is only required for Lima environments")
	}
	if operation != "remove" && operation != "recreate" {
		return environment.Record{}, errors.New(
			"unsupported lifecycle environment mutation",
		)
	}
	if record.Status == "running" && !force {
		return environment.Record{}, fmt.Errorf("environment %q is running; stop it first or pass --force", record.Name)
	}
	if operation == "remove" {
		options := manager.EnvironmentActionOptions{
			IDs: []string{record.ID}, Force: force,
		}
		if d.environmentActions != nil {
			plan, err := d.environmentActions.Prepare(
				manager.EnvironmentActionDelete,
				options,
			)
			if err != nil {
				return environment.Record{}, err
			}
			result, err := d.environmentActions.Apply(
				ctx,
				manager.EnvironmentActionDelete,
				manager.EnvironmentActionAPIRequest{
					IDs: []string{record.ID}, Force: force,
					OperationID: plan.OperationID,
					PlanDigest:  plan.PlanDigest,
					Confirmed:   true,
				},
			)
			if err != nil {
				return environment.Record{}, err
			}
			if result.Operation == nil ||
				result.Operation.Phase != manager.OperationSucceeded {
				return environment.Record{}, errors.New(
					"environment delete operation terminal state is unproved",
				)
			}
			return record, nil
		}
		_, err := d.applyEnvironmentDeletePlan(
			ctx,
			manager.EnvironmentActionPlan{
				Action: manager.EnvironmentActionDelete,
				Force:  force,
				Targets: []manager.EnvironmentActionTarget{
					daemonEnvironmentActionTarget(record),
				},
				Skipped: []manager.EnvironmentActionTarget{},
				Total:   1,
			},
		)
		return record, err
	}
	provider, err := d.lifecycleBackend(record)
	if err != nil {
		return environment.Record{}, err
	}
	if force {
		// --force stops first: cancel this environment's live sessions so
		// their ordinary cleanup releases the lifecycle handles the mutation
		// gate requires to be free.
		_ = d.sessions.cancelEnvironment(environmentID)
	}
	activityOperation := manager.ActivityCleanupEnvironmentRecreate
	var activityPlan manager.ActivityCleanupPlan
	if d.activityCleanup != nil {
		activityPlan, err = d.activityCleanup.PlanEnvironment(
			ctx, environmentID, activityOperation,
		)
		if err != nil {
			return environment.Record{}, err
		}
	}
	var result environment.Record
	err = d.runDestructiveMutationModeWithOwner(
		ctx,
		environmentID,
		force,
		lifecycle.MutationOwner{
			Kind: lifecycle.MutationOwnerConfiguration,
			ID:   operation, Phase: lifecycle.MutationPhaseApplying,
			Recovery: "wait for the environment configuration operation to finish, inspect lifecycle status, then retry",
		},
		func(mutationCtx context.Context) error {
			if d.activityCleanup != nil {
				cleanupResult, cleanupErr := d.activityCleanup.Apply(
					mutationCtx, activityPlan,
				)
				d.recordActivityCleanup(cleanupResult, cleanupErr)
				if cleanupErr != nil {
					return cleanupErr
				}
			}
			result, mutationErr := d.api.Core.RecreateEnvironment(
				mutationCtx,
				record.Name,
				force,
				manager.EnvironmentApplyOptions{Operator: provider},
			)
			if mutationErr == nil && result.ID != environmentID {
				return errors.New("lifecycle environment mutation identity changed")
			}
			return mutationErr
		},
	)
	return result, err
}

func (d *Daemon) applyEnvironmentDeletePlan(
	ctx context.Context,
	plan manager.EnvironmentActionPlan,
) (manager.EnvironmentActionResult, error) {
	result := manager.EnvironmentActionResult{
		Plan: plan, Applied: []manager.EnvironmentActionTarget{},
		Skipped: append(
			[]manager.EnvironmentActionTarget(nil),
			plan.Skipped...,
		),
	}
	if plan.Action != manager.EnvironmentActionDelete {
		return result, errors.New(
			"environment delete executor received another action",
		)
	}
	store := environment.Store{Root: d.store.Root}
	for _, target := range plan.Targets {
		record, err := store.Load(target.ID)
		if err != nil {
			return result, err
		}
		if !environmentCleanTargetMatchesRecord(target, record) {
			return result, errors.New("environment delete plan is stale")
		}
		if record.Status == environment.StatusRunning && !plan.Force {
			return result, fmt.Errorf(
				"environment %q is running; stop it first or pass --force",
				record.Name,
			)
		}
		provider, err := d.lifecycleBackend(record)
		if err != nil {
			return result, err
		}
		if plan.Force {
			_ = d.sessions.cancelEnvironment(record.ID)
		}
		if plan.Force && record.Status == environment.StatusRunning {
			if err := d.stopEnvironmentForRemoval(ctx, record.ID); err != nil {
				return result, err
			}
		}
		identity, err := environment.NewRemovalIdentity(record)
		if err != nil {
			return result, err
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			outcome, recoveryErr := d.api.Core.RecoverEnvironmentClean(
				ctx,
				manager.EnvironmentCleanRecoveryRequest{
					Identity: identity,
					Authority: lifecycle.
						DisposalAuthorityEnvironmentDelete,
					Source: manager.
						EnvironmentDeleteRecoverySourceExplicit,
					Provider: provider,
					ActivityCleanup: func(
						cleanupCtx context.Context,
						cleanupRecord environment.Record,
					) error {
						return d.cleanupEnvironmentRemovalActivity(
							cleanupCtx,
							cleanupRecord.ID,
							cleanupRecord.LastSessionID,
							manager.ActivityCleanupEnvironmentDelete,
						)
					},
				},
			)
			switch {
			case recoveryErr == nil:
				if outcome.Status != manager.DisposableRecoveryRemoved ||
					!outcome.RecordRemoved ||
					!outcome.JournalRemoved ||
					!outcome.ActivityRemoved {
					return result, errors.New(
						"environment delete terminal evidence is incomplete",
					)
				}
				result.Applied = append(result.Applied, target)
				goto nextTarget
			case errors.Is(
				recoveryErr,
				lifecycle.ErrReconciliationInFlight,
			):
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
			case plan.Force &&
				errors.Is(
					recoveryErr,
					lifecycle.ErrMutationBlockedByActivity,
				) &&
				time.Now().Before(deadline):
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			default:
				return result, recoveryErr
			}
		}
	nextTarget:
	}
	return result, nil
}

func daemonEnvironmentActionTarget(
	record environment.Record,
) manager.EnvironmentActionTarget {
	return manager.EnvironmentActionTarget{
		ID: record.ID, Profile: record.Profile,
		Backend: record.Backend, Status: record.Status,
		InstanceName:  record.InstanceName,
		LastSessionID: record.LastSessionID,
		LastCommand:   record.LastCommand,
		CreatedAt:     record.CreatedAt,
		LastStartedAt: record.LastStartedAt,
		LastEndedAt:   record.LastEndedAt,
	}
}

func (d *Daemon) stopEnvironmentForRemoval(
	ctx context.Context,
	environmentID string,
) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := d.lifecycle.WaitReconciliation(ctx, environmentID); err != nil {
			return err
		}
		status, err := d.lifecycle.StopExplicit(ctx, environmentID)
		switch {
		case err == nil:
			if status.Activity != lifecycle.ActivityStopped {
				return errors.New(
					"forced environment delete lacks stable stop evidence",
				)
			}
			return nil
		case errors.Is(err, lifecycle.ErrReconciliationInFlight):
			continue
		case errors.Is(err, lifecycle.ErrMutationBlockedByActivity) &&
			time.Now().Before(deadline):
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			continue
		default:
			return err
		}
	}
}

func (d *Daemon) recordActivityCleanup(
	result manager.ActivityCleanupResult,
	cleanupErr error,
) {
	if d == nil || d.audit == nil || result.Plan.ID == "" {
		return
	}
	decision := "allow"
	if cleanupErr != nil {
		decision = "deny"
	}
	d.audit.record("activity.cleanup", decision, map[string]any{
		"operationId": result.Plan.ID,
		"operation":   result.Plan.Operation,
		"scope":       result.Plan.Scope,
		"ownerCount":  len(result.Plan.Owners),
		"proofCount":  len(result.Proofs),
		"remaining":   len(result.RemainingOwnerKeys),
		"status":      result.Status,
	})
}

func (d *Daemon) runDestructiveMutationModeWithOwner(
	ctx context.Context,
	environmentID string,
	forced bool,
	owner lifecycle.MutationOwner,
	mutate func(context.Context) error,
) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := d.lifecycle.WaitReconciliation(ctx, environmentID); err != nil {
			return err
		}
		err := d.lifecycle.RunDestructiveMutationWithOwner(
			ctx,
			environmentID,
			owner,
			mutate,
		)
		switch {
		case errors.Is(err, lifecycle.ErrReconciliationInFlight):
			continue
		case forced && errors.Is(err, lifecycle.ErrMutationBlockedByActivity) && time.Now().Before(deadline):
			// The forced mutation already cancelled this environment's live
			// sessions; their cleanup releases the lifecycle handles the
			// mutation gate requires. Wait bounded instead of failing closed
			// on first contact.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			continue
		default:
			return err
		}
	}
}

// reconcileRestartResidue proves and removes only host-side state whose exact
// guest authority is absent. Unknown facts are returned as bounded reason codes
// and keep automatic stop fail closed.
func reconcileRestartResidue(ctx context.Context, store environment.Store, record environment.Record, observation backend.LifecycleObservation, provider manager.EnvironmentLifecycleBackend) ([]runsession.OwnerObservation, []string) {
	if ctx == nil {
		ctx = context.Background()
	}
	proofs := make(map[string]error)
	proveAbsent := func(sessionID string) error {
		if err, ok := proofs[sessionID]; ok {
			return err
		}
		var err error
		switch observation.State {
		case backend.LifecycleStopped, backend.LifecycleAbsent:
			// A stopped or absent VM is independent proof that no session target
			// remains in that backend incarnation.
		case backend.LifecycleRunning:
			prover, ok := provider.(backend.SessionAbsenceProver)
			if !ok {
				err = errors.New("backend does not provide exact session absence proof")
				break
			}
			proofCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
			err = prover.ProveSessionAbsent(proofCtx, record.InstanceName, sessionID)
			cancel()
		default:
			err = errors.New("backend incarnation is not proved")
		}
		proofs[sessionID] = err
		return err
	}

	reasons := make([]string, 0, 4)
	owners := []runsession.OwnerObservation{}
	for _, probe := range lifecycle.RecoveryProbes() {
		switch probe {
		case lifecycle.RecoveryBackendObservation:
			if observation.Validate() != nil || observation.InstanceName != record.InstanceName {
				reasons = appendReason(reasons, "backend-observation-unproved")
			}
		case lifecycle.RecoverySessionAbsence:
			var err error
			owners, err = runsession.ListOwners(store.OwnerRoot(record.ID))
			if err != nil {
				reasons = appendReason(reasons, "owner-inventory-unavailable")
				continue
			}
			canReconcileOwners := true
			for _, owner := range owners {
				switch owner.Status {
				case runsession.OwnerLive:
					canReconcileOwners = false
					reasons = appendReason(reasons, "owner-live")
				case runsession.OwnerStale:
					// The owner lock is daemon-held authority. If it is stale, the
					// previous daemon disappeared without proving the complete
					// session cleanup sequence, regardless of the last checkpointed
					// owner state. Exact guest-process absence may classify narrower
					// residuals below, but it must not erase this crash boundary or
					// authorize a replacement attach. Explicit stop owns the recovery
					// path after independently observing the backend stopped.
					canReconcileOwners = false
					reasons = appendReason(reasons, "owner-requires-explicit-recovery")
				case runsession.OwnerUnprovable:
					canReconcileOwners = false
					reasons = appendReason(reasons, "owner-record-unprovable")
				}
			}
			if canReconcileOwners {
				if _, err := runsession.ReconcileStaleOwnersWithCleanup(store.OwnerRoot(record.ID), func(item runsession.OwnerObservation) error {
					return store.ClearSessionRuntime(record.ID, item.SessionID)
				}); err != nil {
					reasons = appendReason(reasons, "owner-reconciliation-failed")
				}
			}
			owners, err = runsession.ListOwners(store.OwnerRoot(record.ID))
			if err != nil {
				reasons = appendReason(reasons, "owner-inventory-unavailable")
			}
			ownerSet := make(map[string]bool, len(owners))
			for _, owner := range owners {
				ownerSet[owner.SessionID] = true
			}
			if err := reconcileOrphanSessionRuntime(store, record.ID, ownerSet, proveAbsent); err != nil {
				reasons = appendReason(reasons, "session-runtime-unproved")
			}
			if err := reconcileActivationReceipt(store, record, observation, owners, proveAbsent); err != nil {
				reasons = appendReason(reasons, "activation-receipt-unproved")
			}
		case lifecycle.RecoveryWorkspaceProvider:
			if err := proveWorkspaceHostProvidersAbsent(store.Root, record.ID); err != nil {
				reasons = appendReason(reasons, "workspace-provider-absence-unproved")
			}
		case lifecycle.RecoveryWorkspaceView:
			if err := proveWorkspaceGuestViewsAbsent(store.Root, record.ID, proveAbsent); err != nil {
				reasons = appendReason(reasons, "workspace-view-absence-unproved")
			}
		case lifecycle.RecoveryNetworkRuntime:
			if nonempty, err := directoryHasEntries(store.RuntimeNetworkServiceDir(record.ID)); err != nil {
				reasons = appendReason(reasons, "network-runtime-unprovable")
			} else if nonempty {
				switch observation.State {
				case backend.LifecycleStopped, backend.LifecycleAbsent:
					if err := store.ClearRuntimeServices(record.ID); err != nil {
						reasons = appendReason(reasons, "network-runtime-cleanup-failed")
					}
				case backend.LifecycleRunning:
					// The environment network service is environment-scoped: it is
					// set up on the first run of a boot, retained across idle-grace
					// for same-boot reuse, and scrubbed once the guest is observed
					// stopped/absent (above). Presence while the guest runs — with
					// or without live owners — is the expected steady state, not an
					// unproved residual that must block automatic stop.
				default:
					reasons = appendReason(reasons, "network-runtime-unproved")
				}
			}
		default:
			// A catalog extension without restart classification must disable
			// automatic stop rather than becoming a documentation-only row.
			reasons = appendReason(reasons, "recovery-probe-unimplemented")
		}
	}
	slices.Sort(reasons)
	return owners, reasons
}

func proveWorkspaceHostProvidersAbsent(storeRoot, environmentID string) error {
	journal, err := (lifecycle.JournalStore{Root: storeRoot}).Load(environmentID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, resource := range journal.Resources {
		if resource.Ref.Kind != lifecycle.KindWorkspaceHostProvider {
			continue
		}
		// The accepted Portal host provider is an in-process hideoutd resource.
		// Reconciliation runs only after the new daemon owns the singleton lock,
		// which independently proves the previous process and its provider gone.
		if resource.Owner.Kind != "manager" {
			return errors.New("workspace host provider owner is unproved")
		}
	}
	return nil
}

func proveWorkspaceGuestViewsAbsent(storeRoot, environmentID string, proveSessionAbsent func(string) error) error {
	journal, err := (lifecycle.JournalStore{Root: storeRoot}).Load(environmentID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var result error
	for _, resource := range journal.Resources {
		if resource.Ref.Kind != lifecycle.KindWorkspaceGuestView {
			continue
		}
		sessionID := ""
		for _, dependency := range resource.Dependencies {
			if dependency.Ref.Kind == lifecycle.KindRunSession && dependency.StopMode == lifecycle.StopModeDrain {
				sessionID = dependency.Ref.ID
				break
			}
		}
		if sessionID == "" {
			result = errors.Join(result, errors.New("workspace guest view lacks a session absence identity"))
			continue
		}
		result = errors.Join(result, proveSessionAbsent(sessionID))
	}
	return result
}

func reconcileOrphanSessionRuntime(store environment.Store, environmentID string, owners map[string]bool, proveAbsent func(string) error) error {
	entries, err := os.ReadDir(store.RuntimeSessionsDir(environmentID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var result error
	for _, entry := range entries {
		sessionID := entry.Name()
		if owners[sessionID] {
			continue
		}
		if !entry.IsDir() || !runsession.ValidID(sessionID) {
			result = errors.Join(result, fmt.Errorf("unrecognized session runtime %q", sessionID))
			continue
		}
		if err := proveAbsent(sessionID); err != nil {
			result = errors.Join(result, err)
			continue
		}
		result = errors.Join(result, store.ClearSessionRuntime(environmentID, sessionID))
	}
	return result
}

func reconcileActivationReceipt(store environment.Store, record environment.Record, observation backend.LifecycleObservation, owners []runsession.OwnerObservation, proveAbsent func(string) error) error {
	receipt, err := backend.LoadActivationReceipt(store.RuntimeDir(record.ID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if receipt.EnvironmentID != record.ID || receipt.InstanceName != record.InstanceName {
		return errors.New("activation receipt identity mismatch")
	}
	switch observation.State {
	case backend.LifecycleStopped, backend.LifecycleAbsent:
		return backend.RemoveActivationReceipt(store.RuntimeDir(record.ID))
	case backend.LifecycleRunning:
		if receipt.BootID != observation.BootID {
			return backend.RemoveActivationReceipt(store.RuntimeDir(record.ID))
		}
		for _, owner := range owners {
			if owner.SessionID == receipt.OwnerSessionID && owner.Status == runsession.OwnerLive {
				return nil
			}
		}
		if err := proveAbsent(receipt.OwnerSessionID); err != nil {
			return err
		}
		return backend.RemoveActivationReceipt(store.RuntimeDir(record.ID))
	default:
		return errors.New("activation receipt backend incarnation is unproved")
	}
}

func directoryHasEntries(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("runtime path %q is not a real directory", filepath.Base(path))
	}
	entries, err := os.ReadDir(path)
	return len(entries) != 0, err
}

func appendReason(reasons []string, reason string) []string {
	reason = strings.TrimSpace(reason)
	if reason == "" || slices.Contains(reasons, reason) {
		return reasons
	}
	return append(reasons, reason)
}
