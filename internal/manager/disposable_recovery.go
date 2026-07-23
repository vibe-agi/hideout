package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
	"github.com/vibe-agi/hideout/internal/session"
)

const (
	DisposableRecoverySourceOrdinary = "ordinary-finalizer"
	DisposableRecoverySourceRestart  = "restart-recovery"

	DisposableRecoveryRemoved         = "removed"
	DisposableRecoveryCleanupRequired = "cleanup-required"
	DisposableRecoveryInterrupted     = "interrupted"
)

type DisposableRecoveryRequest struct {
	EnvironmentID string
	Source        string
	Provider      EnvironmentLifecycleBackend
}

type DisposableRecoveryOutcome struct {
	EnvironmentID         string
	Source                string
	Status                string
	LastPhase             string
	ReasonCode            string
	BackendCleanupInvoked bool
	AbsenceObservations   int
	RecordRemoved         bool
	JournalRemoved        bool
	RuntimeRemoved        bool
	CompletedAt           time.Time
}

// RecoverDisposableEnvironment executes the Manager-owned half of the durable
// disposal protocol. It holds the exact environment transition lock, reloads
// and binds the record, proves there are no live or unprovable owners, and
// removes the record only after backend absence and lifecycle metadata removal.
func (c Core) RecoverDisposableEnvironment(ctx context.Context, request DisposableRecoveryRequest) (DisposableRecoveryOutcome, error) {
	outcome := DisposableRecoveryOutcome{
		EnvironmentID: request.EnvironmentID, Source: request.Source,
		Status: DisposableRecoveryInterrupted,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if request.Source != DisposableRecoverySourceOrdinary && request.Source != DisposableRecoverySourceRestart {
		return outcome, errors.New("disposable recovery source is invalid")
	}
	if request.Provider == nil {
		return outcome, errors.New("disposable recovery backend is unavailable")
	}
	if c.LifecycleDisposals == nil {
		return outcome, errors.New("disposable lifecycle coordinator is unavailable")
	}

	store := environment.Store{Root: c.Store.Root}
	lock, err := store.LockContext(ctx, request.EnvironmentID)
	if err != nil {
		return outcome, err
	}
	defer lock.Unlock()
	record, err := store.Load(request.EnvironmentID)
	if err != nil {
		return outcome, err
	}
	return c.recoverDisposableEnvironmentLocked(ctx, store, record, request.Source, request.Provider)
}

func (c Core) recoverDisposableEnvironmentLocked(
	ctx context.Context,
	store environment.Store,
	record environment.Record,
	source string,
	provider EnvironmentLifecycleBackend,
) (DisposableRecoveryOutcome, error) {
	outcome := DisposableRecoveryOutcome{
		EnvironmentID: record.ID, Source: source, Status: DisposableRecoveryInterrupted,
	}
	identity, err := environment.NewDisposableIdentity(record)
	if err != nil {
		return outcome, err
	}
	if err := proveDisposableOwnersNotLive(store, record.ID); err != nil {
		return outcome, err
	}
	intent, err := c.LifecycleDisposals.BeginDisposal(ctx, lifecycle.DisposalRequest{
		EnvironmentID: identity.EnvironmentID, Backend: identity.Backend,
		InstanceName: identity.InstanceName, RecordDigest: identity.Digest,
	})
	if err != nil {
		return outcome, err
	}
	outcome.LastPhase = intent.State
	record.Status = environment.StatusError
	record.LastEndedAt = time.Now().UTC()
	if err := store.Save(record); err != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "record-retention-failed", err)
	}

	observation := provider.ObserveLifecycle(ctx, identity.InstanceName)
	if err := validateLifecycleObservationForInstance(observation, identity.InstanceName); err != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "backend-observation-unproved", err)
	}
	switch observation.State {
	case backend.LifecycleAbsent:
		// An already-absent exact instance proceeds directly to stable proof.
	case backend.LifecycleRunning, backend.LifecycleStopped:
		if intent.State != lifecycle.DisposalStatePlanned {
			return c.blockDisposableRecovery(
				ctx, outcome, identity, "backend-absence-unproved",
				fmt.Errorf("backend instance reappeared after durable %s proof", intent.State),
			)
		}
		outcome.BackendCleanupInvoked = true
		if err := provider.Cleanup(ctx, &backend.Session{
			EnvironmentID: identity.EnvironmentID,
			Backend:       identity.Backend,
			InstanceName:  identity.InstanceName,
		}); err != nil {
			return c.blockDisposableRecovery(ctx, outcome, identity, "backend-cleanup-failed", err)
		}
	default:
		return c.blockDisposableRecovery(
			ctx, outcome, identity, "backend-observation-unproved",
			fmt.Errorf("backend lifecycle state %q cannot prove disposable cleanup", observation.State),
		)
	}

	outcome.AbsenceObservations, err = observeDisposableAbsenceTwice(ctx, provider, identity.InstanceName)
	if err != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "backend-absence-unproved", err)
	}
	if intent.State == lifecycle.DisposalStatePlanned {
		if err := c.LifecycleDisposals.AdvanceDisposal(
			ctx, identity.EnvironmentID, identity.Digest, lifecycle.DisposalStateBackendAbsent,
		); err != nil {
			return c.blockDisposableRecovery(ctx, outcome, identity, "backend-absence-checkpoint-failed", err)
		}
		intent.State = lifecycle.DisposalStateBackendAbsent
	}
	outcome.LastPhase = lifecycle.DisposalStateBackendAbsent

	if _, err := session.RecoverStaleOwnersWithCleanup(store.OwnerRoot(record.ID), func(item session.OwnerObservation) error {
		return store.ClearSessionRuntime(record.ID, item.SessionID)
	}); err != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "owner-metadata-cleanup-failed", err)
	}
	metadataErr := errors.Join(
		(runtimeverify.Store{Root: c.Store.Root}).Remove(record.ID),
		backend.RemoveActivationReceipt(store.RuntimeDir(record.ID)),
		store.ClearRuntime(record.ID),
	)
	if metadataErr != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "runtime-cleanup-failed", metadataErr)
	}
	outcome.RuntimeRemoved = true
	if err := c.closeDisposableGateway(record.ID); err != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "gateway-cleanup-failed", err)
	}
	if intent.State == lifecycle.DisposalStateBackendAbsent {
		if err := c.LifecycleDisposals.AdvanceDisposal(
			ctx, identity.EnvironmentID, identity.Digest, lifecycle.DisposalStateMetadataCleaning,
		); err != nil {
			return c.blockDisposableRecovery(ctx, outcome, identity, "metadata-checkpoint-failed", err)
		}
		intent.State = lifecycle.DisposalStateMetadataCleaning
	}
	outcome.LastPhase = lifecycle.DisposalStateMetadataCleaning
	if err := c.LifecycleDisposals.CompleteDisposalMetadata(ctx, identity.EnvironmentID, identity.Digest); err != nil {
		return c.blockDisposableRecovery(ctx, outcome, identity, "journal-removal-failed", err)
	}
	outcome.JournalRemoved = true
	if err := store.Remove(record.ID); err != nil {
		outcome.Status = DisposableRecoveryCleanupRequired
		outcome.ReasonCode = "record-removal-failed"
		return outcome, err
	}
	outcome.RecordRemoved = true
	outcome.Status = DisposableRecoveryRemoved
	outcome.CompletedAt = time.Now().UTC()
	c.emitEnvironmentAudit("env.dispose", "allow", map[string]any{
		"environmentId": record.ID, "environmentName": record.Name,
		"instance": record.InstanceName, "source": source,
		"disposition": DisposableRecoveryRemoved,
	})
	return outcome, nil
}

func proveDisposableOwnersNotLive(store environment.Store, environmentID string) error {
	owners, err := session.ListOwners(store.OwnerRoot(environmentID))
	if err != nil {
		return &EnvironmentOwnerError{
			Code: "session.owner-unprovable", EnvironmentID: environmentID, Err: err,
		}
	}
	live := 0
	for _, owner := range owners {
		switch owner.Status {
		case session.OwnerLive:
			live++
		case session.OwnerUnprovable:
			return &EnvironmentOwnerError{
				Code: "session.owner-unprovable", EnvironmentID: environmentID,
				Err: fmt.Errorf("session %s: %w", owner.SessionID, session.ErrOwnerUnprovable),
			}
		}
	}
	if live != 0 {
		return &EnvironmentOwnerError{
			Code: "environment.active-sessions", EnvironmentID: environmentID, ActiveOwners: live,
		}
	}
	return nil
}

func observeDisposableAbsenceTwice(ctx context.Context, provider EnvironmentLifecycleBackend, instanceName string) (int, error) {
	observed := 0
	for observed < 2 {
		if observed != 0 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return observed, ctx.Err()
			case <-timer.C:
			}
		}
		observation := provider.ObserveLifecycle(ctx, instanceName)
		if err := validateLifecycleObservationForInstance(observation, instanceName); err != nil {
			return observed, err
		}
		if observation.State != backend.LifecycleAbsent {
			return observed, fmt.Errorf("backend instance absence changed to %s", observation.State)
		}
		observed++
	}
	return observed, nil
}

func (c Core) blockDisposableRecovery(
	ctx context.Context,
	outcome DisposableRecoveryOutcome,
	identity environment.DisposableIdentity,
	reasonCode string,
	cause error,
) (DisposableRecoveryOutcome, error) {
	outcome.Status = DisposableRecoveryCleanupRequired
	outcome.ReasonCode = reasonCode
	outcome.LastPhase = lifecycle.DisposalStateBlocked
	blockErr := c.LifecycleDisposals.BlockDisposal(
		context.WithoutCancel(ctx), identity.EnvironmentID, identity.Digest, reasonCode,
	)
	c.emitEnvironmentAudit("env.dispose", "deny", map[string]any{
		"environmentId": identity.EnvironmentID, "instance": identity.InstanceName,
		"source": outcome.Source, "disposition": DisposableRecoveryCleanupRequired,
		"reasonCode": reasonCode,
	})
	return outcome, errors.Join(cause, blockErr)
}

func (c Core) closeDisposableGateway(environmentID string) error {
	if c.disposableGatewayCloser != nil {
		return c.disposableGatewayCloser(environmentID)
	}
	if c.NetworkGateways != nil {
		return c.NetworkGateways.CloseEnvironment(environmentID)
	}
	return nil
}
