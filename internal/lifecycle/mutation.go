package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	MutationOwnerAttach        = "attach"
	MutationOwnerConfiguration = "configuration"
	MutationOwnerReconcile     = "reconcile"
	MutationOwnerStop          = "stop"
	MutationOwnerCleanup       = "cleanup"
	MutationOwnerDisposal      = "disposal"
	MutationOwnerSession       = "session"

	MutationPhaseEstablishing = "establishing"
	MutationPhaseApplying     = "applying"
	MutationPhaseReconciling  = "reconciling"
	MutationPhaseStopping     = "stopping"
	MutationPhaseActive       = "active"
)

// MutationOwner is the bounded, non-secret identity displayed when one
// operation prevents another operation from acquiring the same mutation key.
// Recovery describes the safe next action; it never grants takeover authority.
type MutationOwner struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Phase     string    `json:"phase"`
	Recovery  string    `json:"recovery"`
	StartedAt time.Time `json:"startedAt"`
}

// MutationRequest atomically claims every key for one owner. Callers must
// release the returned lease on every terminal path.
type MutationRequest struct {
	Keys  []string
	Owner MutationOwner
}

// MutationLease is daemon-local exclusion authority. It is deliberately not
// durable: a daemon restart discards every claim and re-establishes authority
// only through lifecycle reconciliation and operation-ledger recovery.
type MutationLease interface {
	Release()
}

// MutationCoordinator is the narrow configuration/attach coordination surface
// shared with Manager. Lifecycle remains the only implementation.
type MutationCoordinator interface {
	AcquireMutation(context.Context, MutationRequest) (MutationLease, error)
}

// MutationConflictError exposes the exact key and current blocker without
// weakening the legacy errors.Is classifications used by older callers.
type MutationConflictError struct {
	Key       string        `json:"key"`
	Owner     MutationOwner `json:"owner"`
	Requested MutationOwner `json:"requestedOwner"`
	Cause     error         `json:"-"`
}

func (err *MutationConflictError) Error() string {
	if err == nil {
		return ErrMutationBlockedByActivity.Error()
	}
	return fmt.Sprintf(
		"%s: key=%s owner=%s/%s phase=%s recovery=%s",
		conflictCause(err).Error(),
		err.Key,
		err.Owner.Kind,
		err.Owner.ID,
		err.Owner.Phase,
		err.Owner.Recovery,
	)
}

func (err *MutationConflictError) Unwrap() error {
	return conflictCause(err)
}

func conflictCause(err *MutationConflictError) error {
	if err != nil && err.Cause != nil {
		return err.Cause
	}
	return ErrMutationBlockedByActivity
}

type mutationClaim struct {
	id    uint64
	key   string
	owner MutationOwner
}

type mutationLease struct {
	coordinator *Coordinator
	claims      []mutationClaim
	once        sync.Once
}

func (lease *mutationLease) Release() {
	if lease == nil || lease.coordinator == nil {
		return
	}
	lease.once.Do(func() {
		lease.coordinator.mu.Lock()
		defer lease.coordinator.mu.Unlock()
		lease.releaseLocked()
	})
}

func (lease *mutationLease) releaseLocked() {
	if lease == nil || lease.coordinator == nil {
		return
	}
	for _, claim := range lease.claims {
		owners := lease.coordinator.mutationClaims[claim.key]
		if owners == nil {
			continue
		}
		delete(owners, claim.id)
		if len(owners) == 0 {
			delete(lease.coordinator.mutationClaims, claim.key)
		}
	}
	lease.claims = nil
}

func EnvironmentMutationKey(environmentID string) string {
	if !idPattern.MatchString(environmentID) {
		return ""
	}
	return "environment:" + environmentID
}

func ProfileMutationKey(profileName, mutationKey string) string {
	if !idPattern.MatchString(profileName) || !idPattern.MatchString(mutationKey) {
		return ""
	}
	if strings.Contains(profileName, ":") || strings.Contains(mutationKey, ":") {
		return ""
	}
	return "profile:" + profileName + ":" + mutationKey
}

func (c *Coordinator) AcquireMutation(
	ctx context.Context,
	request MutationRequest,
) (MutationLease, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	request, err := c.normalizeMutationRequest(request)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed {
		return nil, ErrCoordinatorClosed
	}
	return c.claimMutationKeysLocked(request)
}

func (c *Coordinator) normalizeMutationRequest(
	request MutationRequest,
) (MutationRequest, error) {
	keys, err := normalizeMutationKeys(request.Keys)
	if err != nil {
		return MutationRequest{}, err
	}
	owner := request.Owner
	if !idPattern.MatchString(owner.Kind) ||
		!idPattern.MatchString(owner.ID) ||
		!idPattern.MatchString(owner.Phase) ||
		!boundedMutationRecovery(owner.Recovery) {
		return MutationRequest{}, errors.New("lifecycle mutation owner is invalid")
	}
	if owner.StartedAt.IsZero() {
		owner.StartedAt = c.nowUTC()
	}
	owner.StartedAt = owner.StartedAt.Round(0).UTC()
	return MutationRequest{Keys: keys, Owner: owner}, nil
}

func normalizeMutationKeys(keys []string) ([]string, error) {
	if len(keys) == 0 || len(keys) > 32 {
		return nil, errors.New("lifecycle mutation keys are invalid")
	}
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !validMutationKey(key) {
			return nil, errors.New("lifecycle mutation key is invalid")
		}
		set[key] = struct{}{}
	}
	normalized := make([]string, 0, len(set))
	for key := range set {
		normalized = append(normalized, key)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func validMutationKey(key string) bool {
	scope, rest, ok := strings.Cut(key, ":")
	if !ok {
		return false
	}
	switch scope {
	case "environment":
		return idPattern.MatchString(rest)
	case "profile":
		profileName, mutationKey, found := strings.Cut(rest, ":")
		return found &&
			!strings.Contains(mutationKey, ":") &&
			idPattern.MatchString(profileName) &&
			idPattern.MatchString(mutationKey)
	}
	return false
}

func boundedMutationRecovery(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (c *Coordinator) claimMutationKeysLocked(
	request MutationRequest,
) (*mutationLease, error) {
	for _, key := range request.Keys {
		claims := c.mutationClaims[key]
		claimIDs := make([]uint64, 0, len(claims))
		for claimID := range claims {
			claimIDs = append(claimIDs, claimID)
		}
		sort.Slice(claimIDs, func(i, j int) bool {
			return claimIDs[i] < claimIDs[j]
		})
		for _, claimID := range claimIDs {
			blocker := claims[claimID]
			if compatibleMutationOwners(request.Owner, blocker) {
				continue
			}
			return nil, &MutationConflictError{
				Key:       key,
				Owner:     blocker,
				Requested: request.Owner,
				Cause:     mutationConflictCause(request.Owner, blocker),
			}
		}
	}
	lease := &mutationLease{coordinator: c}
	for _, key := range request.Keys {
		c.mutationSequence++
		if c.mutationSequence == 0 {
			c.mutationSequence++
		}
		claim := mutationClaim{
			id: c.mutationSequence, key: key, owner: request.Owner,
		}
		if c.mutationClaims[key] == nil {
			c.mutationClaims[key] = make(map[uint64]MutationOwner)
		}
		c.mutationClaims[key][claim.id] = claim.owner
		lease.claims = append(lease.claims, claim)
	}
	return lease, nil
}

func compatibleMutationOwners(requested, blocking MutationOwner) bool {
	return requested.Kind == MutationOwnerAttach &&
		blocking.Kind == MutationOwnerAttach
}

func mutationConflictCause(requested, blocking MutationOwner) error {
	switch blocking.Kind {
	case MutationOwnerReconcile:
		return ErrReconciliationInFlight
	case MutationOwnerStop:
		return ErrStopInFlight
	}
	if requested.Kind == MutationOwnerAttach {
		return ErrStopInFlight
	}
	return ErrMutationBlockedByActivity
}

func (c *Coordinator) environmentConflictLocked(
	environmentID string,
	state *registryEnvironment,
	requested MutationOwner,
	fallback error,
) error {
	key := EnvironmentMutationKey(environmentID)
	if claims := c.mutationClaims[key]; len(claims) != 0 {
		claimIDs := make([]uint64, 0, len(claims))
		for claimID := range claims {
			claimIDs = append(claimIDs, claimID)
		}
		sort.Slice(claimIDs, func(i, j int) bool {
			return claimIDs[i] < claimIDs[j]
		})
		for _, claimID := range claimIDs {
			blocker := claims[claimID]
			if compatibleMutationOwners(requested, blocker) {
				continue
			}
			cause := mutationConflictCause(requested, blocker)
			if fallback != nil {
				cause = fallback
			}
			return &MutationConflictError{
				Key: key, Owner: blocker, Requested: requested, Cause: cause,
			}
		}
	}
	blocker, ok := c.syntheticEnvironmentBlockerLocked(state)
	if !ok {
		blocker = MutationOwner{
			Kind: MutationOwnerCleanup, ID: c.daemonID,
			Phase:     MutationPhaseApplying,
			Recovery:  "inspect lifecycle status and retry after the current operation finishes",
			StartedAt: c.nowUTC(),
		}
	}
	if fallback == nil {
		fallback = mutationConflictCause(requested, blocker)
	}
	return &MutationConflictError{
		Key: key, Owner: blocker, Requested: requested, Cause: fallback,
	}
}

func (c *Coordinator) syntheticEnvironmentBlockerLocked(
	state *registryEnvironment,
) (MutationOwner, bool) {
	if state == nil {
		return MutationOwner{}, false
	}
	if state.reconciling {
		startedAt := state.journal.Reconciliation.ObservedAt
		if startedAt.IsZero() {
			startedAt = c.nowUTC()
		}
		return MutationOwner{
			Kind: MutationOwnerReconcile, ID: c.daemonID,
			Phase:     MutationPhaseReconciling,
			Recovery:  "wait for lifecycle reconciliation to finish, then retry",
			StartedAt: startedAt,
		}, true
	}
	if len(state.establishing) != 0 {
		sessions := make([]string, 0, len(state.establishing))
		for sessionID := range state.establishing {
			sessions = append(sessions, sessionID)
		}
		sort.Strings(sessions)
		return attachMutationOwner(sessions[0], c.nowUTC()), true
	}
	if len(state.handles) != 0 {
		sessions := make([]string, 0, len(state.handles))
		for sessionID := range state.handles {
			sessions = append(sessions, sessionID)
		}
		sort.Strings(sessions)
		return MutationOwner{
			Kind: MutationOwnerSession, ID: sessions[0],
			Phase:     MutationPhaseActive,
			Recovery:  "stop the owning session or use an explicitly confirmed forced cleanup",
			StartedAt: c.nowUTC(),
		}, true
	}
	if state.mutationOwner != nil {
		return *state.mutationOwner, true
	}
	if state.journal.StopAttempt != nil &&
		attemptBlocksAttach(state.journal.StopAttempt) {
		attempt := state.journal.StopAttempt
		return MutationOwner{
			Kind: MutationOwnerStop, ID: attempt.ID,
			Phase:     MutationPhaseStopping,
			Recovery:  "wait for stable stop evidence, inspect lifecycle status, then retry",
			StartedAt: attempt.StartedAt,
		}, true
	}
	return MutationOwner{}, false
}

func attachMutationOwner(sessionID string, startedAt time.Time) MutationOwner {
	return MutationOwner{
		Kind: MutationOwnerAttach, ID: sessionID,
		Phase:     MutationPhaseEstablishing,
		Recovery:  "wait for attach establishment to finish, then retry",
		StartedAt: startedAt,
	}
}
