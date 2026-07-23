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
		if d.lifecycleCtx.Err() == nil && record.Disposable {
			d.recoverDisposableRecord(d.lifecycleCtx, record)
		}
	}()
}

func (d *Daemon) reconcileLifecycleRecord(ctx context.Context, record environment.Record) {
	provider, err := d.lifecycleBackend(record)
	if err != nil {
		_ = d.lifecycle.BlockReconciliation(record.ID, "backend-provider-unavailable")
		d.audit.record("lifecycle.reconcile", "deny", map[string]any{"environmentId": record.ID, "reasonCode": "backend-provider-unavailable"})
		return
	}
	observeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	observation := provider.ObserveLifecycle(observeCtx, record.InstanceName)
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
				return result, err
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

// applyEnvironmentCleanPlan keeps destructive cleanup in Manager Core while
// ensuring the daemon's lifecycle discovery state is removed only after the
// environment and backend have been successfully removed.
func (d *Daemon) applyEnvironmentCleanPlan(ctx context.Context, plan manager.EnvironmentActionPlan) (manager.EnvironmentActionResult, error) {
	result := manager.EnvironmentActionResult{
		Plan: plan, Applied: []manager.EnvironmentActionTarget{},
		Skipped: append([]manager.EnvironmentActionTarget(nil), plan.Skipped...),
	}
	environmentStore := environment.Store{Root: d.store.Root}
	for _, target := range plan.Targets {
		direct := plan
		direct.Targets = []manager.EnvironmentActionTarget{target}
		direct.Skipped = nil
		direct.Total = 1
		var operator manager.EnvironmentOperator
		if target.Backend == "lima" {
			if d.lifecycleBackend == nil {
				return result, errors.New("daemon lifecycle backend is unavailable")
			}
			record, err := environmentStore.Load(target.ID)
			if err != nil {
				return result, err
			}
			provider, err := d.lifecycleBackend(record)
			if err != nil {
				return result, err
			}
			operator = provider
		}
		var applied manager.EnvironmentActionResult
		apply := func(applyCtx context.Context) error {
			var applyErr error
			applied, applyErr = d.api.Core.ApplyEnvironmentClean(applyCtx, direct, manager.EnvironmentApplyOptions{Operator: operator})
			return applyErr
		}
		var err error
		if target.Backend == "lima" {
			err = d.runDestructiveMutation(ctx, target.ID, apply)
		} else {
			err = apply(ctx)
		}
		result.Applied = append(result.Applied, applied.Applied...)
		result.Skipped = append(result.Skipped, applied.Skipped...)
		if err != nil {
			return result, err
		}
	}
	return result, nil
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
	if record.Status == "running" && !force {
		return environment.Record{}, fmt.Errorf("environment %q is running; stop it first or pass --force", record.Name)
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
	var result environment.Record
	err = d.runDestructiveMutationMode(ctx, environmentID, force, func(mutationCtx context.Context) error {
		var mutationErr error
		switch operation {
		case "remove":
			result, mutationErr = d.api.Core.RemoveEnvironment(mutationCtx, record.Name, force, manager.EnvironmentApplyOptions{Operator: provider})
		case "recreate":
			result, mutationErr = d.api.Core.RecreateEnvironment(mutationCtx, record.Name, force, manager.EnvironmentApplyOptions{Operator: provider})
		default:
			return errors.New("unsupported lifecycle environment mutation")
		}
		if mutationErr == nil && result.ID != environmentID {
			return errors.New("lifecycle environment mutation identity changed")
		}
		return mutationErr
	})
	return result, err
}

func (d *Daemon) runDestructiveMutation(ctx context.Context, environmentID string, mutate func(context.Context) error) error {
	return d.runDestructiveMutationMode(ctx, environmentID, false, mutate)
}

func (d *Daemon) runDestructiveMutationMode(ctx context.Context, environmentID string, forced bool, mutate func(context.Context) error) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := d.lifecycle.WaitReconciliation(ctx, environmentID); err != nil {
			return err
		}
		err := d.lifecycle.RunDestructiveMutation(ctx, environmentID, mutate)
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
