package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
)

const disposableRecoveryTimeout = 30 * time.Second

// recoverDisposableRecord runs only after the ordinary lifecycle inventory has
// reached a terminal classification. The same four-slot startup pool bounds
// both reconciliation and disposal, while the daemon control surfaces are
// already serving.
func (d *Daemon) recoverDisposableRecord(ctx context.Context, record environment.Record) {
	if d == nil || d.lifecycleBackend == nil || !record.Disposable {
		return
	}
	provider, err := d.lifecycleBackend(record)
	if err != nil {
		d.audit.record("lifecycle.disposal", "deny", map[string]any{
			"environmentId": record.ID, "reasonCode": "backend-provider-unavailable",
		})
		return
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, disposableRecoveryTimeout)
	defer cancel()
	outcome, recoveryErr := d.api.Core.RecoverDisposableEnvironment(recoveryCtx, manager.DisposableRecoveryRequest{
		EnvironmentID: record.ID, Source: manager.DisposableRecoverySourceRestart, Provider: provider,
	})
	decision := "allow"
	if recoveryErr != nil {
		decision = "deny"
	}
	d.audit.record("lifecycle.disposal", decision, map[string]any{
		"environmentId":         record.ID,
		"source":                manager.DisposableRecoverySourceRestart,
		"status":                outcome.Status,
		"lastPhase":             outcome.LastPhase,
		"reasonCode":            outcome.ReasonCode,
		"backendCleanupInvoked": outcome.BackendCleanupInvoked,
		"absenceObservations":   outcome.AbsenceObservations,
		"recordRemoved":         outcome.RecordRemoved,
		"journalRemoved":        outcome.JournalRemoved,
	})
}

func (d *Daemon) startMissingDisposableRecovery(environmentIDs []string) {
	for _, environmentID := range environmentIDs {
		d.launchMissingDisposableRecovery(environmentID)
	}
}

func (d *Daemon) launchMissingDisposableRecovery(environmentID string) {
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
			return
		}
		d.recoverMissingDisposableIntent(d.lifecycleCtx, environmentID)
	}()
}

// recoverMissingDisposableIntent never invokes backend cleanup. A valid
// record-bound intent can authorize only metadata convergence after the exact
// instance is independently observed absent twice. Legacy journal-only residue
// remains blocked by the startup inventory path.
func (d *Daemon) recoverMissingDisposableIntent(ctx context.Context, environmentID string) {
	journal, err := (lifecycle.JournalStore{Root: d.store.Root}).Load(environmentID)
	if err != nil || journal.Disposal == nil {
		return
	}
	intent := *journal.Disposal
	provider, err := d.lifecycleBackend(environment.Record{
		ID: environmentID, Backend: intent.Backend, InstanceName: intent.InstanceName, Disposable: true,
	})
	if err != nil {
		d.blockMissingDisposableIntent(environmentID, intent.RecordDigest, "backend-provider-unavailable", err)
		return
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, disposableRecoveryTimeout)
	defer cancel()
	resumed, err := d.lifecycle.BeginDisposal(recoveryCtx, lifecycle.DisposalRequest{
		EnvironmentID: environmentID, Backend: intent.Backend, InstanceName: intent.InstanceName,
		RecordDigest: intent.RecordDigest, Generation: intent.Generation,
	})
	if err != nil {
		return
	}
	if err := observeMissingDisposableAbsenceTwice(recoveryCtx, provider, intent.InstanceName); err != nil {
		d.blockMissingDisposableIntent(environmentID, intent.RecordDigest, "missing-record-backend-not-absent", err)
		return
	}
	if resumed.State == lifecycle.DisposalStatePlanned {
		if err := d.lifecycle.AdvanceDisposal(recoveryCtx, environmentID, intent.RecordDigest, lifecycle.DisposalStateBackendAbsent); err != nil {
			d.blockMissingDisposableIntent(environmentID, intent.RecordDigest, "backend-absence-checkpoint-failed", err)
			return
		}
		resumed.State = lifecycle.DisposalStateBackendAbsent
	}
	if resumed.State == lifecycle.DisposalStateBackendAbsent {
		if d.networkGateways != nil {
			if err := d.networkGateways.CloseEnvironment(environmentID); err != nil {
				d.blockMissingDisposableIntent(environmentID, intent.RecordDigest, "gateway-cleanup-failed", err)
				return
			}
		}
		if err := d.lifecycle.AdvanceDisposal(recoveryCtx, environmentID, intent.RecordDigest, lifecycle.DisposalStateMetadataCleaning); err != nil {
			d.blockMissingDisposableIntent(environmentID, intent.RecordDigest, "metadata-checkpoint-failed", err)
			return
		}
		resumed.State = lifecycle.DisposalStateMetadataCleaning
	}
	if resumed.State != lifecycle.DisposalStateMetadataCleaning {
		d.blockMissingDisposableIntent(
			environmentID, intent.RecordDigest, "missing-record-intent-state-invalid",
			fmt.Errorf("unexpected intent state %q", resumed.State),
		)
		return
	}
	if err := d.lifecycle.CompleteDisposalMetadata(recoveryCtx, environmentID, intent.RecordDigest); err != nil {
		d.blockMissingDisposableIntent(environmentID, intent.RecordDigest, "journal-removal-failed", err)
		return
	}
	d.audit.record("lifecycle.disposal", "allow", map[string]any{
		"environmentId": environmentID, "source": manager.DisposableRecoverySourceRestart,
		"status": manager.DisposableRecoveryRemoved, "recordPresent": false,
		"backendCleanupInvoked": false, "absenceObservations": 2,
		"journalRemoved": true,
	})
}

func observeMissingDisposableAbsenceTwice(ctx context.Context, provider manager.EnvironmentLifecycleBackend, instanceName string) error {
	for sample := 0; sample < 2; sample++ {
		if sample != 0 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		observation := provider.ObserveLifecycle(ctx, instanceName)
		if err := observation.Validate(); err != nil {
			return err
		}
		if observation.InstanceName != instanceName {
			return errors.New("backend lifecycle observation belongs to another instance")
		}
		if observation.State != backend.LifecycleAbsent {
			return fmt.Errorf("backend instance state is %s", observation.State)
		}
	}
	return nil
}

func (d *Daemon) blockMissingDisposableIntent(environmentID, digest, reasonCode string, cause error) {
	blockErr := d.lifecycle.BlockDisposal(context.Background(), environmentID, digest, reasonCode)
	// Raw provider/filesystem errors are deliberately not copied into daemon
	// evidence; the closed reason code is the public classification.
	_ = errors.Join(cause, blockErr)
	d.audit.record("lifecycle.disposal", "deny", map[string]any{
		"environmentId": environmentID, "source": manager.DisposableRecoverySourceRestart,
		"status":     manager.DisposableRecoveryCleanupRequired,
		"reasonCode": reasonCode, "backendCleanupInvoked": false,
	})
}
