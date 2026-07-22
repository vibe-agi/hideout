package lifecycle

import (
	"context"
	"errors"
)

// BeginReconciliation persists an environment-scoped pending fence before a
// backend or provider probe starts. A concurrent caller coalesces onto the
// existing probe instead of creating a second source of lifecycle truth.
func (c *Coordinator) BeginReconciliation(ctx context.Context, environmentID string) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	if !idPattern.MatchString(environmentID) {
		return false, errors.New("lifecycle reconciliation identity is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return false, ErrCoordinatorClosed
	}
	state, err := c.loadEnvironmentLocked(environmentID)
	if err != nil {
		return false, err
	}
	if state.reconciling {
		return false, nil
	}
	// An authenticated retry is recovery for a blocked result, not a way to
	// renew an already proved idle grace period. Startup still reconciles a
	// prior daemon's complete row because its daemon instance differs.
	if state.journal.Reconciliation.DaemonInstanceID == c.daemonID &&
		state.journal.Reconciliation.State == "complete" {
		return false, nil
	}
	if len(state.handles) != 0 || len(state.establishing) != 0 || state.mutation ||
		attemptBlocksReconciliation(state.journal.StopAttempt, c.daemonID) || state.stopCancel != nil {
		return false, errors.New("lifecycle reconciliation is blocked by environment activity")
	}
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	state.deadlineSeq++
	state.journal.IdleDeadline = nil
	if state.journal.StartGeneration == 0 {
		state.journal.StartGeneration = 1
	}
	state.blocked = true
	state.reconciling = true
	state.reconcileDone = make(chan struct{})
	state.journal.Reconciliation = Reconciliation{
		DaemonInstanceID: c.daemonID,
		State:            "pending",
		ReasonCode:       "reconciliation-pending",
		ObservedAt:       c.nowUTC(),
	}
	if err := c.persistLocked(state); err != nil {
		c.finishReconciliationLocked(state)
		return false, err
	}
	c.emitLocked(Event{
		EnvironmentID: environmentID,
		Generation:    state.journal.StartGeneration,
		Kind:          "reconciliation-started",
		ReasonCode:    "reconciliation-pending",
		At:            c.nowUTC(),
	})
	return true, nil
}

func attemptBlocksReconciliation(attempt *StopAttempt, daemonID string) bool {
	return attempt != nil && attempt.DaemonInstanceID == daemonID && attemptBlocksAttach(attempt)
}

// WaitReconciliation waits only for the named environment. It does not make a
// slow provider for one environment a global daemon availability dependency.
func (c *Coordinator) WaitReconciliation(ctx context.Context, environmentID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !idPattern.MatchString(environmentID) {
		return errors.New("lifecycle reconciliation identity is invalid")
	}
	for {
		c.mu.Lock()
		if c.closing || c.closed {
			c.mu.Unlock()
			return ErrCoordinatorClosed
		}
		state, err := c.loadEnvironmentLocked(environmentID)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		if !state.reconciling {
			c.mu.Unlock()
			return nil
		}
		done := state.reconcileDone
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
}

// BlockReconciliation closes an in-flight fence with a durable, typed reason.
// It is used when a provider cannot produce even a schema-valid observation.
func (c *Coordinator) BlockReconciliation(environmentID, reasonCode string) error {
	if !idPattern.MatchString(environmentID) {
		return errors.New("lifecycle reconciliation identity is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return ErrCoordinatorClosed
	}
	state, err := c.loadEnvironmentLocked(environmentID)
	if err != nil {
		return err
	}
	state.blocked = true
	state.journal.IdleDeadline = nil
	state.journal.StopAttempt = nil
	state.journal.Reconciliation = blockedReconciliation(c.daemonID, reasonCode, c.nowUTC())
	if err := c.persistLocked(state); err != nil {
		c.finishReconciliationLocked(state)
		return err
	}
	c.finishReconciliationLocked(state)
	c.emitLocked(Event{
		EnvironmentID: environmentID,
		Generation:    state.journal.StartGeneration,
		Kind:          "reconciliation-blocked",
		ReasonCode:    state.journal.Reconciliation.ReasonCode,
		At:            c.nowUTC(),
	})
	return nil
}

func (c *Coordinator) finishReconciliationLocked(state *registryEnvironment) {
	if !state.reconciling {
		return
	}
	state.reconciling = false
	if state.reconcileDone != nil {
		close(state.reconcileDone)
		state.reconcileDone = nil
	}
}
