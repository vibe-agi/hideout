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

	EnvironmentCleanRecoverySourceExplicit  = "explicit-clean"
	EnvironmentDeleteRecoverySourceExplicit = "explicit-delete"
	EnvironmentCleanRecoverySourceRestart   = "restart-recovery"

	DisposableRecoveryRemoved         = "removed"
	DisposableRecoveryCleanupRequired = "cleanup-required"
	DisposableRecoveryInterrupted     = "interrupted"
)

type EnvironmentRemovalActivityCleanupFunc func(
	context.Context,
	environment.Record,
) error

type DisposableRecoveryRequest struct {
	EnvironmentID   string
	Source          string
	Provider        EnvironmentLifecycleBackend
	ActivityCleanup EnvironmentRemovalActivityCleanupFunc
}

type EnvironmentCleanRecoveryRequest struct {
	Identity        environment.RemovalIdentity
	Authority       string
	Source          string
	Provider        EnvironmentLifecycleBackend
	ActivityCleanup EnvironmentRemovalActivityCleanupFunc
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
	ActivityRemoved       bool
	CompletedAt           time.Time
}

type EnvironmentCleanRecoveryOutcome = DisposableRecoveryOutcome

type environmentRemovalIdentity struct {
	EnvironmentID     string
	Authority         string
	Backend           string
	InstanceName      string
	RecordDigest      string
	ActivitySessionID string
}

// RecoverDisposableEnvironment executes the Manager-owned half of the durable
// disposal protocol. It holds the exact environment transition lock, reloads
// and binds the record, proves there are no live or unprovable owners, and
// removes the record only after backend absence and lifecycle metadata removal.
func (c Core) RecoverDisposableEnvironment(
	ctx context.Context,
	request DisposableRecoveryRequest,
) (outcome DisposableRecoveryOutcome, resultErr error) {
	outcome = DisposableRecoveryOutcome{
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
	defer func() {
		resultErr = errors.Join(resultErr, lock.Unlock())
	}()
	record, err := store.Load(request.EnvironmentID)
	if err != nil {
		return outcome, err
	}
	return c.recoverDisposableEnvironmentLocked(
		ctx, store, record, request.Source, request.Provider,
		request.ActivityCleanup,
	)
}

// RecoverEnvironmentClean applies an explicitly confirmed clean through the
// same crash-safe removal protocol as --rm. The caller must bind the provider
// to Identity and supply an activity cleaner that proves retained observations
// are absent before the environment record can be removed.
func (c Core) RecoverEnvironmentClean(
	ctx context.Context,
	request EnvironmentCleanRecoveryRequest,
) (
	outcome EnvironmentCleanRecoveryOutcome,
	resultErr error,
) {
	outcome = EnvironmentCleanRecoveryOutcome{
		EnvironmentID: request.Identity.EnvironmentID,
		Source:        request.Source,
		Status:        DisposableRecoveryInterrupted,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if request.Source != EnvironmentCleanRecoverySourceExplicit &&
		request.Source != EnvironmentDeleteRecoverySourceExplicit &&
		request.Source != EnvironmentCleanRecoverySourceRestart {
		return outcome, errors.New("environment clean recovery source is invalid")
	}
	if request.Provider == nil {
		return outcome, errors.New("environment clean recovery backend is unavailable")
	}
	if request.ActivityCleanup == nil {
		return outcome, errors.New("environment clean activity proof is unavailable")
	}
	if c.LifecycleDisposals == nil {
		return outcome, errors.New("environment clean lifecycle coordinator is unavailable")
	}
	if request.Identity.Schema != environment.RemovalIdentitySchema ||
		request.Identity.EnvironmentID == "" {
		return outcome, errors.New("environment clean recovery identity is invalid")
	}
	authority := request.Authority
	if authority == "" {
		authority = lifecycle.DisposalAuthorityEnvironmentClean
	}
	if authority != lifecycle.DisposalAuthorityEnvironmentClean &&
		authority != lifecycle.DisposalAuthorityEnvironmentDelete {
		return outcome, errors.New("environment clean recovery authority is invalid")
	}
	if (request.Source == EnvironmentCleanRecoverySourceExplicit &&
		authority != lifecycle.DisposalAuthorityEnvironmentClean) ||
		(request.Source == EnvironmentDeleteRecoverySourceExplicit &&
			authority != lifecycle.DisposalAuthorityEnvironmentDelete) {
		return outcome, errors.New("environment clean recovery source does not match authority")
	}

	store := environment.Store{Root: c.Store.Root}
	lock, err := store.LockContext(ctx, request.Identity.EnvironmentID)
	if err != nil {
		return outcome, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lock.Unlock())
	}()
	record, err := store.Load(request.Identity.EnvironmentID)
	if err != nil {
		return outcome, err
	}
	if !request.Identity.MatchesRecord(record) {
		return outcome, errors.New("environment clean recovery identity mismatch")
	}
	identity := environmentRemovalIdentity{
		EnvironmentID:     request.Identity.EnvironmentID,
		Authority:         authority,
		Backend:           request.Identity.Backend,
		InstanceName:      request.Identity.InstanceName,
		RecordDigest:      request.Identity.Digest,
		ActivitySessionID: record.LastSessionID,
	}
	return c.recoverEnvironmentRemovalLocked(
		ctx, store, record, identity, request.Source, request.Provider,
		request.ActivityCleanup,
	)
}

func (c Core) recoverDisposableEnvironmentLocked(
	ctx context.Context,
	store environment.Store,
	record environment.Record,
	source string,
	provider EnvironmentLifecycleBackend,
	activityCleanup EnvironmentRemovalActivityCleanupFunc,
) (DisposableRecoveryOutcome, error) {
	identity, err := environment.NewDisposableIdentity(record)
	if err != nil {
		return DisposableRecoveryOutcome{
			EnvironmentID: record.ID, Source: source,
			Status: DisposableRecoveryInterrupted,
		}, err
	}
	return c.recoverEnvironmentRemovalLocked(
		ctx,
		store,
		record,
		environmentRemovalIdentity{
			EnvironmentID:     identity.EnvironmentID,
			Authority:         lifecycle.DisposalAuthorityRunRM,
			Backend:           identity.Backend,
			InstanceName:      identity.InstanceName,
			RecordDigest:      identity.Digest,
			ActivitySessionID: record.LastSessionID,
		},
		source,
		provider,
		activityCleanup,
	)
}

func (c Core) recoverEnvironmentRemovalLocked(
	ctx context.Context,
	store environment.Store,
	record environment.Record,
	identity environmentRemovalIdentity,
	source string,
	provider EnvironmentLifecycleBackend,
	activityCleanup EnvironmentRemovalActivityCleanupFunc,
) (DisposableRecoveryOutcome, error) {
	outcome := DisposableRecoveryOutcome{
		EnvironmentID: record.ID, Source: source,
		Status: DisposableRecoveryInterrupted,
	}
	if err := proveRemovalOwnersNotLive(store, record.ID); err != nil {
		return outcome, err
	}
	intent, err := c.LifecycleDisposals.BeginDisposal(ctx, lifecycle.DisposalRequest{
		EnvironmentID: identity.EnvironmentID, Authority: identity.Authority,
		Backend: identity.Backend, InstanceName: identity.InstanceName,
		RecordDigest:      identity.RecordDigest,
		ActivitySessionID: identity.ActivitySessionID,
	})
	if err != nil {
		return outcome, err
	}
	outcome.LastPhase = intent.State
	record.Status = environment.StatusError
	record.LastEndedAt = time.Now().UTC()
	if err := store.Save(record); err != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonRecordRetentionFailed, err,
		)
	}

	observation := provider.ObserveLifecycle(ctx, identity.InstanceName)
	if err := validateLifecycleObservationForInstance(observation, identity.InstanceName); err != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonBackendObservationUnproved, err,
		)
	}
	switch observation.State {
	case backend.LifecycleAbsent:
		// An already-absent exact instance proceeds directly to stable proof.
	case backend.LifecycleRunning, backend.LifecycleStopped:
		if intent.State != lifecycle.DisposalStatePlanned {
			return c.blockEnvironmentRemovalRecovery(
				ctx, outcome, identity, lifecycle.DisposalReasonBackendAbsenceUnproved,
				fmt.Errorf("backend instance reappeared after durable %s proof", intent.State),
			)
		}
		outcome.BackendCleanupInvoked = true
		if err := provider.Cleanup(ctx, &backend.Session{
			EnvironmentID: identity.EnvironmentID,
			Backend:       identity.Backend,
			InstanceName:  identity.InstanceName,
		}); err != nil {
			return c.blockEnvironmentRemovalRecovery(
				ctx, outcome, identity, lifecycle.DisposalReasonBackendCleanupFailed, err,
			)
		}
	default:
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonBackendObservationUnproved,
			fmt.Errorf("backend lifecycle state %q cannot prove disposable cleanup", observation.State),
		)
	}

	outcome.AbsenceObservations, err = observeDisposableAbsenceTwice(ctx, provider, identity.InstanceName)
	if err != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonBackendAbsenceUnproved, err,
		)
	}
	if intent.State == lifecycle.DisposalStatePlanned {
		if err := c.LifecycleDisposals.AdvanceDisposal(
			ctx, identity.EnvironmentID, identity.RecordDigest, lifecycle.DisposalStateBackendAbsent,
		); err != nil {
			return c.blockEnvironmentRemovalRecovery(
				ctx, outcome, identity, lifecycle.DisposalReasonBackendCheckpointFailed, err,
			)
		}
		intent.State = lifecycle.DisposalStateBackendAbsent
	}
	outcome.LastPhase = lifecycle.DisposalStateBackendAbsent

	if _, err := session.RecoverStaleOwnersWithCleanup(store.OwnerRoot(record.ID), func(item session.OwnerObservation) error {
		return store.ClearSessionRuntime(record.ID, item.SessionID)
	}); err != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonOwnerMetadataCleanupFailed, err,
		)
	}
	metadataErr := errors.Join(
		(runtimeverify.Store{Root: c.Store.Root}).Remove(record.ID),
		backend.RemoveActivationReceipt(store.RuntimeDir(record.ID)),
		store.ClearRuntime(record.ID),
	)
	if metadataErr != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonRuntimeCleanupFailed, metadataErr,
		)
	}
	outcome.RuntimeRemoved = true
	if err := c.closeDisposableGateway(record.ID); err != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonGatewayCleanupFailed, err,
		)
	}
	if activityCleanup != nil {
		if err := activityCleanup(ctx, record); err != nil {
			return c.blockEnvironmentRemovalRecovery(
				ctx, outcome, identity, lifecycle.DisposalReasonActivityCleanupFailed, err,
			)
		}
		outcome.ActivityRemoved = true
	}
	if intent.State == lifecycle.DisposalStateBackendAbsent {
		if err := c.LifecycleDisposals.AdvanceDisposal(
			ctx, identity.EnvironmentID, identity.RecordDigest, lifecycle.DisposalStateMetadataCleaning,
		); err != nil {
			return c.blockEnvironmentRemovalRecovery(
				ctx, outcome, identity, lifecycle.DisposalReasonMetadataCheckpointFailed, err,
			)
		}
		intent.State = lifecycle.DisposalStateMetadataCleaning
	}
	outcome.LastPhase = lifecycle.DisposalStateMetadataCleaning
	// Remove the Manager record before the lifecycle intent. A crash at this
	// boundary leaves the exact, non-destructive metadata-convergence authority
	// behind; removing the journal first would lose automatic recovery for
	// explicitly cleaned named environments.
	if err := store.Remove(record.ID); err != nil {
		return c.blockEnvironmentRemovalRecovery(
			ctx, outcome, identity, lifecycle.DisposalReasonRecordRemovalFailed, err,
		)
	}
	outcome.RecordRemoved = true
	if err := c.LifecycleDisposals.CompleteDisposalMetadata(
		ctx, identity.EnvironmentID, identity.RecordDigest,
	); err != nil {
		outcome.Status = DisposableRecoveryCleanupRequired
		outcome.ReasonCode = lifecycle.DisposalReasonJournalRemovalFailed
		c.emitRemovalAudit(identity, outcome, "deny")
		return outcome, err
	}
	outcome.JournalRemoved = true
	outcome.Status = DisposableRecoveryRemoved
	outcome.CompletedAt = time.Now().UTC()
	c.emitRemovalAudit(identity, outcome, "allow")
	return outcome, nil
}

func proveRemovalOwnersNotLive(store environment.Store, environmentID string) error {
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

func (c Core) blockEnvironmentRemovalRecovery(
	ctx context.Context,
	outcome DisposableRecoveryOutcome,
	identity environmentRemovalIdentity,
	reasonCode string,
	cause error,
) (DisposableRecoveryOutcome, error) {
	outcome.Status = DisposableRecoveryCleanupRequired
	outcome.ReasonCode = reasonCode
	outcome.LastPhase = lifecycle.DisposalStateBlocked
	blockErr := c.LifecycleDisposals.BlockDisposal(
		context.WithoutCancel(ctx), identity.EnvironmentID, identity.RecordDigest, reasonCode,
	)
	c.emitRemovalAudit(identity, outcome, "deny")
	return outcome, errors.Join(cause, blockErr)
}

func (c Core) emitRemovalAudit(
	identity environmentRemovalIdentity,
	outcome DisposableRecoveryOutcome,
	decision string,
) {
	action := "env.dispose"
	if identity.Authority == lifecycle.DisposalAuthorityEnvironmentClean {
		action = "env.clean.recover"
	} else if identity.Authority == lifecycle.DisposalAuthorityEnvironmentDelete {
		action = "env.delete.recover"
	}
	c.emitEnvironmentAudit(action, decision, map[string]any{
		"environmentId":         identity.EnvironmentID,
		"instance":              identity.InstanceName,
		"authority":             identity.Authority,
		"source":                outcome.Source,
		"disposition":           outcome.Status,
		"reasonCode":            outcome.ReasonCode,
		"backendCleanupInvoked": outcome.BackendCleanupInvoked,
		"absenceObservations":   outcome.AbsenceObservations,
		"activityRemoved":       outcome.ActivityRemoved,
		"recordRemoved":         outcome.RecordRemoved,
		"journalRemoved":        outcome.JournalRemoved,
	})
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
