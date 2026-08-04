// Package migration contains backend-neutral migration formats and state
// transitions. State transitions in this file are pure: callers persist and
// execute the corresponding effects only after the returned state validates.
package migration

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidStateTransition identifies an action that is not enabled for the
	// supplied state. The input state is never mutated when this error is returned.
	ErrInvalidStateTransition = errors.New("migration state transition is not enabled")
	// ErrStateInvariant identifies state that cannot be produced by a valid
	// migration trace.
	ErrStateInvariant = errors.New("migration state invariant is violated")
)

// BundlePhase is the durable phase of one export bundle operation.
type BundlePhase string

const (
	BundlePhaseDraft       BundlePhase = "draft"
	BundlePhaseClaimed     BundlePhase = "claimed"
	BundlePhaseSnapshotted BundlePhase = "snapshotted"
	BundlePhaseWriting     BundlePhase = "writing"
	BundlePhaseSealed      BundlePhase = "sealed"
	BundlePhaseCancelled   BundlePhase = "cancelled"
)

// BundleAction is one atomic transition in the export state machine.
type BundleAction string

const (
	BundleStopSource             BundleAction = "stop-source"
	BundleAcquireClaim           BundleAction = "acquire-claim"
	BundleCreateSnapshot         BundleAction = "create-snapshot"
	BundleBeginWriting           BundleAction = "begin-writing"
	BundleWriteNextChunk         BundleAction = "write-next-chunk"
	BundleCheckpointPrefix       BundleAction = "checkpoint-prefix"
	BundleCrash                  BundleAction = "crash"
	BundleRestart                BundleAction = "restart"
	BundleTruncateUnverifiedTail BundleAction = "truncate-unverified-tail"
	BundleSeal                   BundleAction = "seal"
	BundleRequestCancel          BundleAction = "request-cancel"
	BundleCancel                 BundleAction = "cancel"
	BundleRemoveRetainedPartial  BundleAction = "remove-retained-partial"
	BundleTamper                 BundleAction = "tamper"
)

// BundleStateOptions are immutable inputs to a fresh export state machine.
type BundleStateOptions struct {
	SourceDigest  string
	SourceStopped bool
	MaxChunks     uint64
	MaxCrashes    uint64
}

// BundleState is the production-shaped refinement of formal/MigrationBundle.tla.
// Effect counters represent durable effect bindings, not retry attempts.
type BundleState struct {
	Phase               BundlePhase
	InitialSourceDigest string
	SourceDigest        string
	SourceStopped       bool
	ClaimHeld           bool
	SnapshotExists      bool
	SnapshotIndependent bool
	Written             uint64
	Checkpoint          uint64
	TailAuthentic       bool
	Footer              bool
	Published           bool
	Tampered            bool
	CancelRequested     bool
	RetainPartial       bool
	PartialRetained     bool
	DaemonUp            bool
	CrashCount          uint64
	SnapshotEffects     uint8
	SealEffects         uint8
	MaxChunks           uint64
	MaxCrashes          uint64
}

// BundleTransition requests one pure export-state transition.
type BundleTransition struct {
	Action        BundleAction
	RetainPartial bool
}

// NewBundleState constructs and validates a fresh export state.
func NewBundleState(options BundleStateOptions) (BundleState, error) {
	state := BundleState{
		Phase:               BundlePhaseDraft,
		InitialSourceDigest: options.SourceDigest,
		SourceDigest:        options.SourceDigest,
		SourceStopped:       options.SourceStopped,
		TailAuthentic:       true,
		DaemonUp:            true,
		MaxChunks:           options.MaxChunks,
		MaxCrashes:          options.MaxCrashes,
	}
	if err := state.Validate(); err != nil {
		return BundleState{}, err
	}
	return state, nil
}

// Importable reports the exact sealed-bundle predicate from the formal model.
func (state BundleState) Importable() bool {
	return state.Phase == BundlePhaseSealed &&
		state.Footer &&
		state.Published &&
		!state.Tampered &&
		state.Written == state.MaxChunks &&
		state.Checkpoint == state.MaxChunks &&
		state.TailAuthentic
}

// Validate checks the safety invariants shared with MigrationBundle.tla.
func (state BundleState) Validate() error {
	if state.InitialSourceDigest == "" ||
		state.SourceDigest != state.InitialSourceDigest {
		return invariantError("source content digest changed")
	}
	if state.MaxChunks == 0 {
		return invariantError("maximum chunk count is zero")
	}
	if !validBundlePhase(state.Phase) {
		return invariantError("unknown bundle phase %q", state.Phase)
	}
	if state.Written > state.MaxChunks || state.Checkpoint > state.Written {
		return invariantError(
			"checkpoint range is invalid: checkpoint=%d written=%d max=%d",
			state.Checkpoint,
			state.Written,
			state.MaxChunks,
		)
	}
	if state.CrashCount > state.MaxCrashes {
		return invariantError("crash count exceeds its bound")
	}
	if state.SnapshotEffects > 1 || state.SealEffects > 1 {
		return invariantError("a critical effect executed more than once")
	}
	if state.SnapshotIndependent && !state.SnapshotExists {
		return invariantError("independent snapshot proof has no snapshot")
	}
	if state.ClaimHeld &&
		(state.Phase != BundlePhaseClaimed ||
			!state.SourceStopped || state.SnapshotIndependent) {
		return invariantError("source claim is held outside the stopped pre-snapshot phase")
	}
	if state.Written > 0 || state.Checkpoint > 0 || state.Footer || state.Published {
		if !state.SnapshotExists || !state.SnapshotIndependent {
			return invariantError("payload exists without an independent snapshot")
		}
	}
	if !state.TailAuthentic &&
		(state.Phase != BundlePhaseWriting || state.Written <= state.Checkpoint) {
		return invariantError("unauthenticated tail is not beyond the durable checkpoint")
	}
	if state.Published && (state.Phase != BundlePhaseSealed || !state.Footer) {
		return invariantError("unsealed bundle is published")
	}
	if state.RetainPartial && !state.CancelRequested {
		return invariantError("partial retention choice exists without cancellation")
	}
	if state.PartialRetained &&
		(state.Phase != BundlePhaseCancelled || !state.RetainPartial) {
		return invariantError("retained partial is not bound to a cancelled export")
	}

	switch state.Phase {
	case BundlePhaseDraft:
		if state.ClaimHeld || state.SnapshotExists || state.Written != 0 ||
			state.Checkpoint != 0 || state.Footer || state.Published ||
			state.PartialRetained {
			return invariantError("draft contains execution effects")
		}
	case BundlePhaseClaimed:
		if !state.ClaimHeld || !state.SourceStopped || state.SnapshotExists {
			return invariantError("claimed phase lacks an exclusive stopped-source claim")
		}
	case BundlePhaseSnapshotted, BundlePhaseWriting:
		if state.ClaimHeld || !state.SnapshotExists ||
			!state.SnapshotIndependent || state.SnapshotEffects != 1 ||
			state.Footer || state.Published {
			return invariantError("snapshot/writing phase has inconsistent durable effects")
		}
	case BundlePhaseSealed:
		if state.ClaimHeld || !state.SnapshotExists ||
			!state.SnapshotIndependent || state.Written != state.MaxChunks ||
			state.Checkpoint != state.MaxChunks || !state.TailAuthentic ||
			!state.Footer || !state.Published || state.SealEffects != 1 {
			return invariantError("sealed bundle is incomplete")
		}
	case BundlePhaseCancelled:
		if state.ClaimHeld || state.SnapshotExists || state.SnapshotIndependent ||
			state.Written != 0 || state.Checkpoint != 0 || !state.TailAuthentic ||
			state.Footer || state.Published {
			return invariantError("cancelled export retained publishable state")
		}
	}
	return nil
}

// ApplyBundleTransition returns the next state without mutating the input.
func ApplyBundleTransition(
	state BundleState,
	transition BundleTransition,
) (BundleState, error) {
	if err := state.Validate(); err != nil {
		return state, err
	}
	next := state
	requireDaemon := func() error {
		if !state.DaemonUp {
			return transitionError(transition.Action, "daemon is down")
		}
		return nil
	}

	switch transition.Action {
	case BundleStopSource:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseDraft || state.SourceStopped {
			return state, transitionError(transition.Action, "source is not a running draft")
		}
		next.SourceStopped = true
	case BundleAcquireClaim:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseDraft || !state.SourceStopped {
			return state, transitionError(transition.Action, "source is not proved stopped")
		}
		next.Phase = BundlePhaseClaimed
		next.ClaimHeld = true
	case BundleCreateSnapshot:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseClaimed || !state.ClaimHeld ||
			state.SnapshotEffects != 0 {
			return state, transitionError(transition.Action, "snapshot effect is not enabled")
		}
		next.Phase = BundlePhaseSnapshotted
		next.ClaimHeld = false
		next.SnapshotExists = true
		next.SnapshotIndependent = true
		next.SnapshotEffects++
	case BundleBeginWriting:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseSnapshotted ||
			!state.SnapshotExists || !state.SnapshotIndependent {
			return state, transitionError(transition.Action, "independent snapshot is absent")
		}
		next.Phase = BundlePhaseWriting
	case BundleWriteNextChunk:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseWriting || !state.TailAuthentic ||
			state.Written >= state.MaxChunks {
			return state, transitionError(transition.Action, "next chunk is not writable")
		}
		next.Written++
	case BundleCheckpointPrefix:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseWriting || !state.TailAuthentic ||
			state.Checkpoint >= state.Written {
			return state, transitionError(transition.Action, "no new authentic prefix exists")
		}
		next.Checkpoint = state.Written
	case BundleCrash:
		if !state.DaemonUp || bundleTerminal(state.Phase) ||
			state.CrashCount >= state.MaxCrashes {
			return state, transitionError(transition.Action, "crash is not enabled")
		}
		next.DaemonUp = false
		next.CrashCount++
		if state.Phase == BundlePhaseWriting && state.Written > state.Checkpoint {
			next.TailAuthentic = false
		}
	case BundleRestart:
		if state.DaemonUp {
			return state, transitionError(transition.Action, "daemon is already running")
		}
		next.DaemonUp = true
	case BundleTruncateUnverifiedTail:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseWriting || state.TailAuthentic {
			return state, transitionError(transition.Action, "tail is already authentic")
		}
		next.Written = state.Checkpoint
		next.TailAuthentic = true
	case BundleSeal:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseWriting || !state.TailAuthentic ||
			state.Written != state.MaxChunks || state.Checkpoint != state.MaxChunks ||
			state.SealEffects != 0 {
			return state, transitionError(transition.Action, "complete authenticated prefix is absent")
		}
		next.Phase = BundlePhaseSealed
		next.Footer = true
		next.Published = true
		next.SealEffects++
	case BundleRequestCancel:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if !bundleCancellable(state.Phase) || state.CancelRequested {
			return state, transitionError(transition.Action, "cancellation is not requestable")
		}
		next.CancelRequested = true
		next.RetainPartial = transition.RetainPartial
	case BundleCancel:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if !bundleCancellable(state.Phase) || !state.CancelRequested {
			return state, transitionError(transition.Action, "cancellation is not confirmed")
		}
		next.Phase = BundlePhaseCancelled
		next.ClaimHeld = false
		next.SnapshotExists = false
		next.SnapshotIndependent = false
		next.PartialRetained = state.RetainPartial && state.Written > 0
		next.Written = 0
		next.Checkpoint = 0
		next.TailAuthentic = true
		next.Footer = false
		next.Published = false
	case BundleRemoveRetainedPartial:
		if err := requireDaemon(); err != nil {
			return state, err
		}
		if state.Phase != BundlePhaseCancelled || !state.PartialRetained {
			return state, transitionError(transition.Action, "no retained partial is removable")
		}
		next.PartialRetained = false
	case BundleTamper:
		if state.Phase != BundlePhaseSealed || state.Tampered {
			return state, transitionError(transition.Action, "sealed bundle is not tamperable")
		}
		next.Tampered = true
	default:
		return state, transitionError(transition.Action, "unknown action")
	}
	if err := next.Validate(); err != nil {
		return state, err
	}
	return next, nil
}

func validBundlePhase(phase BundlePhase) bool {
	switch phase {
	case BundlePhaseDraft, BundlePhaseClaimed, BundlePhaseSnapshotted,
		BundlePhaseWriting, BundlePhaseSealed, BundlePhaseCancelled:
		return true
	default:
		return false
	}
}

func bundleTerminal(phase BundlePhase) bool {
	return phase == BundlePhaseSealed || phase == BundlePhaseCancelled
}

func bundleCancellable(phase BundlePhase) bool {
	return phase == BundlePhaseClaimed ||
		phase == BundlePhaseSnapshotted ||
		phase == BundlePhaseWriting
}

// GuestIdentityPolicy is chosen independently for every destination import.
type GuestIdentityPolicy string

const (
	GuestIdentitySafeClone    GuestIdentityPolicy = "safe-clone"
	GuestIdentityExactRestore GuestIdentityPolicy = "exact-guest-restore"
)

// AdoptionPhase is the durable phase of one destination import.
type AdoptionPhase string

const (
	AdoptionPhaseDraft                AdoptionPhase = "draft"
	AdoptionPhasePlanned              AdoptionPhase = "planned"
	AdoptionPhaseClaimed              AdoptionPhase = "claimed"
	AdoptionPhaseStaged               AdoptionPhase = "staged"
	AdoptionPhaseAdopting             AdoptionPhase = "adopting"
	AdoptionPhaseAdopted              AdoptionPhase = "adopted"
	AdoptionPhaseVerified             AdoptionPhase = "verified"
	AdoptionPhaseCommitted            AdoptionPhase = "committed"
	AdoptionPhaseActive               AdoptionPhase = "active"
	AdoptionPhaseRollingBack          AdoptionPhase = "rolling-back"
	AdoptionPhaseRolledBack           AdoptionPhase = "rolled-back"
	AdoptionPhaseBlocked              AdoptionPhase = "blocked"
	AdoptionPhaseReplacementPlanned   AdoptionPhase = "replacement-planned"
	AdoptionPhaseReplacementConfirmed AdoptionPhase = "replacement-confirmed"
)

// AdoptionDecision records the one-way activation or rollback choice.
type AdoptionDecision string

const (
	AdoptionDecisionNone     AdoptionDecision = "none"
	AdoptionDecisionCommit   AdoptionDecision = "commit"
	AdoptionDecisionRollback AdoptionDecision = "rollback"
)

// AdoptionAction is one atomic transition in the import state machine.
type AdoptionAction string

const (
	AdoptionRejectInvalidBundle AdoptionAction = "reject-invalid-bundle"
	AdoptionPlanDestination     AdoptionAction = "plan-destination"
	AdoptionApproveAuthority    AdoptionAction = "approve-authority"
	AdoptionAcquireNameClaim    AdoptionAction = "acquire-name-claim"
	AdoptionBlockNameConflict   AdoptionAction = "block-name-conflict"
	AdoptionRenameDestination   AdoptionAction = "rename-destination"
	AdoptionPlanReplacement     AdoptionAction = "plan-replacement"
	AdoptionConfirmReplacement  AdoptionAction = "confirm-replacement"
	AdoptionDeleteReplacement   AdoptionAction = "delete-replacement"
	AdoptionStageDestination    AdoptionAction = "stage-destination"
	AdoptionBegin               AdoptionAction = "begin-adoption"
	AdoptionFinish              AdoptionAction = "finish-adoption"
	AdoptionVerifyDestination   AdoptionAction = "verify-destination"
	AdoptionDecideCommit        AdoptionAction = "decide-commit"
	AdoptionActivate            AdoptionAction = "activate"
	AdoptionRequestRollback     AdoptionAction = "request-rollback"
	AdoptionRollback            AdoptionAction = "rollback"
	AdoptionCrash               AdoptionAction = "crash"
	AdoptionRestart             AdoptionAction = "restart"
)

// DestinationDraft contains destination-specific choices made at import time.
type DestinationDraft struct {
	ID            string
	RequestedName string
	Policy        GuestIdentityPolicy
}

// AdoptionStateOptions are immutable inputs for imports from one sealed bundle.
type AdoptionStateOptions struct {
	BundleDigest    string
	BundleValid     bool
	SourceControlID string
	SourceBackendID string
	SourceGuestID   string
	MaxCrashes      uint64
	Destinations    []DestinationDraft
	ExistingNames   []string
}

// DestinationState is isolated per import, even when several destinations read
// the same immutable bundle.
type DestinationState struct {
	ID                   string
	RequestedName        string
	Policy               GuestIdentityPolicy
	PlannedPolicy        GuestIdentityPolicy
	Phase                AdoptionPhase
	ControlID            string
	BackendID            string
	GuestID              string
	AuthorityApproved    bool
	AuthorityEffective   bool
	Staged               bool
	ReceiptValid         bool
	Runnable             bool
	Decision             AdoptionDecision
	StageEffects         uint8
	AdoptionEffects      uint8
	CommitEffects        uint8
	ReplacementConfirmed bool
	ReplacementDeleted   bool
	ReplacementEffects   uint8
}

// AdoptionState refines formal/MigrationAdoption.tla for any bounded set of
// destination imports sharing one immutable bundle digest.
type AdoptionState struct {
	InitialBundleDigest string
	BundleDigest        string
	BundleValid         bool
	SourceControlID     string
	SourceBackendID     string
	SourceGuestID       string
	DaemonUp            bool
	CrashCount          uint64
	MaxCrashes          uint64
	Destinations        map[string]DestinationState
	NameOwners          map[string]string
}

// AdoptionTransition requests one pure destination or daemon transition.
type AdoptionTransition struct {
	Action        AdoptionAction
	Destination   string
	ControlID     string
	BackendID     string
	GuestID       string
	RequestedName string
}

const adoptionExistingNameOwner = "__existing_destination__"

// NewAdoptionState constructs a group of independent imports from one bundle.
func NewAdoptionState(options AdoptionStateOptions) (AdoptionState, error) {
	state := AdoptionState{
		InitialBundleDigest: options.BundleDigest,
		BundleDigest:        options.BundleDigest,
		BundleValid:         options.BundleValid,
		SourceControlID:     options.SourceControlID,
		SourceBackendID:     options.SourceBackendID,
		SourceGuestID:       options.SourceGuestID,
		DaemonUp:            true,
		MaxCrashes:          options.MaxCrashes,
		Destinations:        make(map[string]DestinationState, len(options.Destinations)),
		NameOwners:          make(map[string]string),
	}
	for _, draft := range options.Destinations {
		if draft.ID == "" || draft.RequestedName == "" ||
			!validGuestIdentityPolicy(draft.Policy) {
			return AdoptionState{}, invariantError("destination draft is invalid")
		}
		if _, exists := state.Destinations[draft.ID]; exists {
			return AdoptionState{}, invariantError("duplicate destination %q", draft.ID)
		}
		state.Destinations[draft.ID] = DestinationState{
			ID: draft.ID, RequestedName: draft.RequestedName,
			Policy: draft.Policy, Phase: AdoptionPhaseDraft,
			Decision: AdoptionDecisionNone,
		}
	}
	for _, name := range options.ExistingNames {
		if name == "" || state.NameOwners[name] != "" {
			return AdoptionState{}, invariantError(
				"existing destination name is invalid or duplicated",
			)
		}
		state.NameOwners[name] = adoptionExistingNameOwner
	}
	if err := state.Validate(); err != nil {
		return AdoptionState{}, err
	}
	return state, nil
}

// Clone returns a deep copy suitable for a pure transition or negative fixture.
func (state AdoptionState) Clone() AdoptionState {
	clone := state
	clone.Destinations = make(map[string]DestinationState, len(state.Destinations))
	for id, destination := range state.Destinations {
		clone.Destinations[id] = destination
	}
	clone.NameOwners = make(map[string]string, len(state.NameOwners))
	for name, owner := range state.NameOwners {
		clone.NameOwners[name] = owner
	}
	return clone
}

// Validate checks the safety invariants shared with MigrationAdoption.tla.
func (state AdoptionState) Validate() error {
	if state.InitialBundleDigest == "" ||
		state.BundleDigest != state.InitialBundleDigest {
		return invariantError("sealed bundle digest changed")
	}
	if state.SourceControlID == "" || state.SourceBackendID == "" ||
		state.SourceGuestID == "" {
		return invariantError("source identity evidence is incomplete")
	}
	if state.CrashCount > state.MaxCrashes {
		return invariantError("adoption crash count exceeds its bound")
	}
	if len(state.Destinations) == 0 || state.NameOwners == nil {
		return invariantError("destination state is empty")
	}

	controlOwners := make(map[string]string)
	backendOwners := make(map[string]string)
	safeGuestOwners := make(map[string]string)
	for id, destination := range state.Destinations {
		if destination.ID != id || destination.ID == "" ||
			destination.RequestedName == "" ||
			!validGuestIdentityPolicy(destination.Policy) ||
			!validAdoptionPhase(destination.Phase) ||
			!validAdoptionDecision(destination.Decision) {
			return invariantError("destination %q has invalid typed state", id)
		}
		if destination.PlannedPolicy != "" &&
			destination.PlannedPolicy != destination.Policy {
			return invariantError("destination %q changed policy after planning", id)
		}
		if adoptionRequiresPlan(destination.Phase) &&
			destination.PlannedPolicy != destination.Policy {
			return invariantError("destination %q has no frozen plan policy", id)
		}
		if destination.StageEffects > 1 || destination.AdoptionEffects > 1 ||
			destination.CommitEffects > 1 || destination.ReplacementEffects > 1 {
			return invariantError("destination %q repeated a durable effect", id)
		}
		if destination.ReplacementDeleted &&
			(!destination.ReplacementConfirmed || destination.ReplacementEffects != 1) {
			return invariantError(
				"destination %q replacement delete lacks separate confirmation", id,
			)
		}
		if destination.Phase == AdoptionPhaseReplacementConfirmed &&
			!destination.ReplacementConfirmed {
			return invariantError(
				"destination %q replacement phase lacks confirmation", id,
			)
		}
		if destination.ReplacementConfirmed && !destination.ReplacementDeleted &&
			destination.Phase != AdoptionPhaseReplacementConfirmed {
			return invariantError(
				"destination %q confirmed replacement left its delete boundary", id,
			)
		}
		if destination.Runnable != (destination.Phase == AdoptionPhaseActive) {
			return invariantError("destination %q is runnable outside active", id)
		}
		if destination.AuthorityEffective &&
			(!destination.AuthorityApproved || !destination.Runnable) {
			return invariantError("destination %q has unapproved authority", id)
		}
		if destination.Phase == AdoptionPhaseActive &&
			destination.AuthorityEffective != destination.AuthorityApproved {
			return invariantError("destination %q active authority does not match its plan", id)
		}

		claimExpected := adoptionClaimPhase(destination.Phase)
		claimActual := state.NameOwners[destination.RequestedName] == id
		if claimActual != claimExpected {
			return invariantError("destination %q name claim does not match its phase", id)
		}
		if adoptionHasDestinationIdentity(destination.Phase) {
			if destination.ControlID == "" || destination.BackendID == "" {
				return invariantError("destination %q lacks fresh destination identity", id)
			}
		} else if destination.Phase == AdoptionPhaseDraft ||
			destination.Phase == AdoptionPhaseRolledBack ||
			destination.Phase == AdoptionPhaseBlocked {
			if destination.ControlID != "" || destination.BackendID != "" {
				return invariantError("destination %q retained identity outside a plan", id)
			}
		}
		if destination.ControlID != "" {
			if destination.ControlID == state.SourceControlID {
				return invariantError("destination %q reused source control identity", id)
			}
			if owner := controlOwners[destination.ControlID]; owner != "" && owner != id {
				return invariantError("destinations %q and %q share control identity", owner, id)
			}
			controlOwners[destination.ControlID] = id
		}
		if destination.BackendID != "" {
			if destination.BackendID == state.SourceBackendID {
				return invariantError("destination %q reused source backend identity", id)
			}
			if owner := backendOwners[destination.BackendID]; owner != "" && owner != id {
				return invariantError("destinations %q and %q share backend identity", owner, id)
			}
			backendOwners[destination.BackendID] = id
		}

		if destination.GuestID != "" {
			switch destination.PlannedPolicy {
			case GuestIdentitySafeClone:
				if destination.GuestID == state.SourceGuestID {
					return invariantError("destination %q reused source guest identity", id)
				}
				if owner := safeGuestOwners[destination.GuestID]; owner != "" && owner != id {
					return invariantError("Safe Clone destinations %q and %q share guest identity", owner, id)
				}
				safeGuestOwners[destination.GuestID] = id
			case GuestIdentityExactRestore:
				if destination.GuestID != state.SourceGuestID {
					return invariantError("destination %q did not preserve exact guest identity", id)
				}
			default:
				return invariantError("destination %q received guest identity before planning", id)
			}
		}
		if adoptionIdentityPhase(destination.Phase) &&
			(destination.GuestID == "" || !destination.ReceiptValid ||
				destination.AdoptionEffects != 1) {
			return invariantError("destination %q lacks proved adoption identity", id)
		}
		if destination.Staged && destination.StageEffects != 1 {
			return invariantError("destination %q is staged without one stage effect", id)
		}
		if adoptionNeedsStage(destination.Phase) && !destination.Staged {
			return invariantError("destination %q lost its staged object", id)
		}
		if (destination.Phase == AdoptionPhaseCommitted ||
			destination.Phase == AdoptionPhaseActive) &&
			(destination.Decision != AdoptionDecisionCommit ||
				destination.CommitEffects != 1) {
			return invariantError("destination %q lacks one-way commit evidence", id)
		}
		if (destination.Phase == AdoptionPhaseRollingBack ||
			destination.Phase == AdoptionPhaseRolledBack) &&
			destination.Decision != AdoptionDecisionRollback {
			return invariantError("destination %q lacks rollback decision", id)
		}
		if destination.Phase == AdoptionPhaseRolledBack &&
			(destination.Staged || destination.ReceiptValid || destination.Runnable ||
				destination.GuestID != "" || destination.AuthorityEffective) {
			return invariantError("destination %q rollback retained active state", id)
		}
		if !state.BundleValid &&
			(destination.Staged || destination.Runnable ||
				(destination.Phase != AdoptionPhaseDraft &&
					destination.Phase != AdoptionPhaseBlocked)) {
			return invariantError("invalid bundle produced destination effects")
		}
	}

	for name, owner := range state.NameOwners {
		if owner == "" {
			continue
		}
		if owner == adoptionExistingNameOwner {
			continue
		}
		destination, exists := state.Destinations[owner]
		if !exists || destination.RequestedName != name ||
			!adoptionClaimPhase(destination.Phase) {
			return invariantError("name %q has an invalid owner %q", name, owner)
		}
	}
	return nil
}

// ApplyAdoptionTransition returns a deep-copied next state and never mutates its
// input. Destination-specific resets therefore occur independently per import.
func ApplyAdoptionTransition(
	state AdoptionState,
	transition AdoptionTransition,
) (AdoptionState, error) {
	if err := state.Validate(); err != nil {
		return state, err
	}
	next := state.Clone()
	if transition.Action == AdoptionCrash {
		if !state.DaemonUp || state.CrashCount >= state.MaxCrashes {
			return state, adoptionTransitionError(transition, "crash is not enabled")
		}
		next.DaemonUp = false
		next.CrashCount++
		return validatedAdoptionNext(state, next)
	}
	if transition.Action == AdoptionRestart {
		if state.DaemonUp {
			return state, adoptionTransitionError(transition, "daemon is already running")
		}
		next.DaemonUp = true
		return validatedAdoptionNext(state, next)
	}
	if !state.DaemonUp {
		return state, adoptionTransitionError(transition, "daemon is down")
	}
	destination, exists := next.Destinations[transition.Destination]
	if !exists {
		return state, adoptionTransitionError(transition, "destination is unknown")
	}

	switch transition.Action {
	case AdoptionRejectInvalidBundle:
		if state.BundleValid || destination.Phase != AdoptionPhaseDraft {
			return state, adoptionTransitionError(transition, "bundle is not a rejected draft")
		}
		destination.Phase = AdoptionPhaseBlocked
	case AdoptionPlanDestination:
		if !state.BundleValid || destination.Phase != AdoptionPhaseDraft ||
			transition.ControlID == "" || transition.BackendID == "" ||
			transition.ControlID == state.SourceControlID ||
			transition.BackendID == state.SourceBackendID ||
			adoptionControlIDUsed(state, transition.ControlID) ||
			adoptionBackendIDUsed(state, transition.BackendID) {
			return state, adoptionTransitionError(transition, "fresh destination identities are unavailable")
		}
		destination.Phase = AdoptionPhasePlanned
		destination.PlannedPolicy = destination.Policy
		destination.ControlID = transition.ControlID
		destination.BackendID = transition.BackendID
	case AdoptionApproveAuthority:
		if destination.Phase != AdoptionPhasePlanned || destination.AuthorityApproved {
			return state, adoptionTransitionError(transition, "authority approval is not enabled")
		}
		destination.AuthorityApproved = true
	case AdoptionAcquireNameClaim:
		if destination.Phase != AdoptionPhasePlanned ||
			state.NameOwners[destination.RequestedName] != "" {
			return state, adoptionTransitionError(transition, "destination name is unavailable")
		}
		destination.Phase = AdoptionPhaseClaimed
		next.NameOwners[destination.RequestedName] = destination.ID
	case AdoptionBlockNameConflict:
		owner := state.NameOwners[destination.RequestedName]
		if destination.Phase != AdoptionPhasePlanned || owner == "" ||
			owner == destination.ID {
			return state, adoptionTransitionError(transition, "name conflict is not proved")
		}
		destination.Phase = AdoptionPhaseBlocked
		destination.ControlID = ""
		destination.BackendID = ""
	case AdoptionRenameDestination:
		owner := state.NameOwners[destination.RequestedName]
		if destination.Phase != AdoptionPhasePlanned || owner == "" ||
			owner == destination.ID || transition.RequestedName == "" ||
			transition.RequestedName == destination.RequestedName ||
			state.NameOwners[transition.RequestedName] != "" {
			return state, adoptionTransitionError(
				transition, "conflict-free rename is not enabled",
			)
		}
		destination.Phase = AdoptionPhaseDraft
		destination.RequestedName = transition.RequestedName
		destination.PlannedPolicy = ""
		destination.ControlID = ""
		destination.BackendID = ""
	case AdoptionPlanReplacement:
		if destination.Phase != AdoptionPhasePlanned ||
			state.NameOwners[destination.RequestedName] != adoptionExistingNameOwner {
			return state, adoptionTransitionError(
				transition, "replace planning is not enabled",
			)
		}
		destination.Phase = AdoptionPhaseReplacementPlanned
	case AdoptionConfirmReplacement:
		if destination.Phase != AdoptionPhaseReplacementPlanned ||
			destination.ReplacementConfirmed {
			return state, adoptionTransitionError(
				transition, "replacement confirmation is not enabled",
			)
		}
		destination.Phase = AdoptionPhaseReplacementConfirmed
		destination.ReplacementConfirmed = true
	case AdoptionDeleteReplacement:
		if destination.Phase != AdoptionPhaseReplacementConfirmed ||
			!destination.ReplacementConfirmed || destination.ReplacementEffects != 0 ||
			state.NameOwners[destination.RequestedName] != adoptionExistingNameOwner {
			return state, adoptionTransitionError(
				transition, "confirmed replacement delete is not enabled",
			)
		}
		next.NameOwners[destination.RequestedName] = ""
		destination.Phase = AdoptionPhaseDraft
		destination.PlannedPolicy = ""
		destination.ControlID = ""
		destination.BackendID = ""
		destination.ReplacementDeleted = true
		destination.ReplacementEffects++
	case AdoptionStageDestination:
		if !state.BundleValid || destination.Phase != AdoptionPhaseClaimed ||
			state.NameOwners[destination.RequestedName] != destination.ID ||
			destination.StageEffects != 0 {
			return state, adoptionTransitionError(transition, "staging is not enabled")
		}
		destination.Phase = AdoptionPhaseStaged
		destination.Staged = true
		destination.StageEffects++
	case AdoptionBegin:
		if !state.BundleValid || destination.Phase != AdoptionPhaseStaged ||
			!destination.Staged {
			return state, adoptionTransitionError(transition, "adoption is not enabled")
		}
		destination.Phase = AdoptionPhaseAdopting
	case AdoptionFinish:
		if !state.BundleValid || destination.Phase != AdoptionPhaseAdopting ||
			destination.AdoptionEffects != 0 {
			return state, adoptionTransitionError(transition, "adoption completion is not enabled")
		}
		switch destination.PlannedPolicy {
		case GuestIdentitySafeClone:
			if transition.GuestID == "" || transition.GuestID == state.SourceGuestID ||
				adoptionSafeGuestIDUsed(state, transition.GuestID) {
				return state, adoptionTransitionError(transition, "Safe Clone identity is not fresh")
			}
			destination.GuestID = transition.GuestID
		case GuestIdentityExactRestore:
			if transition.GuestID != "" && transition.GuestID != state.SourceGuestID {
				return state, adoptionTransitionError(transition, "exact identity does not match source")
			}
			destination.GuestID = state.SourceGuestID
		default:
			return state, adoptionTransitionError(transition, "identity policy is not planned")
		}
		destination.Phase = AdoptionPhaseAdopted
		destination.ReceiptValid = true
		destination.AdoptionEffects++
	case AdoptionVerifyDestination:
		if !state.BundleValid || destination.Phase != AdoptionPhaseAdopted ||
			!destination.Staged || !destination.ReceiptValid {
			return state, adoptionTransitionError(transition, "destination proof is incomplete")
		}
		destination.Phase = AdoptionPhaseVerified
	case AdoptionDecideCommit:
		if !state.BundleValid || destination.Phase != AdoptionPhaseVerified ||
			destination.Decision != AdoptionDecisionNone ||
			destination.CommitEffects != 0 {
			return state, adoptionTransitionError(transition, "commit decision is not enabled")
		}
		destination.Phase = AdoptionPhaseCommitted
		destination.Decision = AdoptionDecisionCommit
		destination.CommitEffects++
	case AdoptionActivate:
		if !state.BundleValid || destination.Phase != AdoptionPhaseCommitted ||
			destination.Decision != AdoptionDecisionCommit {
			return state, adoptionTransitionError(transition, "activation is not enabled")
		}
		destination.Phase = AdoptionPhaseActive
		destination.Runnable = true
		destination.AuthorityEffective = destination.AuthorityApproved
	case AdoptionRequestRollback:
		if !adoptionRollbackSourcePhase(destination.Phase) ||
			destination.Decision != AdoptionDecisionNone {
			return state, adoptionTransitionError(transition, "rollback decision is not enabled")
		}
		destination.Phase = AdoptionPhaseRollingBack
		destination.Decision = AdoptionDecisionRollback
	case AdoptionRollback:
		if destination.Phase != AdoptionPhaseRollingBack ||
			destination.Decision != AdoptionDecisionRollback ||
			state.NameOwners[destination.RequestedName] != destination.ID {
			return state, adoptionTransitionError(transition, "owned rollback is not enabled")
		}
		destination.Phase = AdoptionPhaseRolledBack
		next.NameOwners[destination.RequestedName] = ""
		destination.ControlID = ""
		destination.BackendID = ""
		destination.GuestID = ""
		destination.AuthorityEffective = false
		destination.Staged = false
		destination.ReceiptValid = false
		destination.Runnable = false
	default:
		return state, adoptionTransitionError(transition, "unknown action")
	}
	next.Destinations[destination.ID] = destination
	return validatedAdoptionNext(state, next)
}

func validatedAdoptionNext(previous, next AdoptionState) (AdoptionState, error) {
	if err := next.Validate(); err != nil {
		return previous, err
	}
	return next, nil
}

func validGuestIdentityPolicy(policy GuestIdentityPolicy) bool {
	return policy == GuestIdentitySafeClone || policy == GuestIdentityExactRestore
}

func validAdoptionPhase(phase AdoptionPhase) bool {
	switch phase {
	case AdoptionPhaseDraft, AdoptionPhasePlanned, AdoptionPhaseClaimed,
		AdoptionPhaseStaged, AdoptionPhaseAdopting, AdoptionPhaseAdopted,
		AdoptionPhaseVerified, AdoptionPhaseCommitted, AdoptionPhaseActive,
		AdoptionPhaseRollingBack, AdoptionPhaseRolledBack, AdoptionPhaseBlocked,
		AdoptionPhaseReplacementPlanned, AdoptionPhaseReplacementConfirmed:
		return true
	default:
		return false
	}
}

func validAdoptionDecision(decision AdoptionDecision) bool {
	return decision == AdoptionDecisionNone ||
		decision == AdoptionDecisionCommit ||
		decision == AdoptionDecisionRollback
}

func adoptionRequiresPlan(phase AdoptionPhase) bool {
	switch phase {
	case AdoptionPhasePlanned, AdoptionPhaseClaimed, AdoptionPhaseStaged,
		AdoptionPhaseAdopting, AdoptionPhaseAdopted, AdoptionPhaseVerified,
		AdoptionPhaseCommitted, AdoptionPhaseActive, AdoptionPhaseRollingBack,
		AdoptionPhaseRolledBack, AdoptionPhaseReplacementPlanned,
		AdoptionPhaseReplacementConfirmed:
		return true
	default:
		return false
	}
}

func adoptionHasDestinationIdentity(phase AdoptionPhase) bool {
	switch phase {
	case AdoptionPhasePlanned, AdoptionPhaseClaimed, AdoptionPhaseStaged,
		AdoptionPhaseAdopting, AdoptionPhaseAdopted, AdoptionPhaseVerified,
		AdoptionPhaseCommitted, AdoptionPhaseActive, AdoptionPhaseRollingBack,
		AdoptionPhaseReplacementPlanned, AdoptionPhaseReplacementConfirmed:
		return true
	default:
		return false
	}
}

func adoptionClaimPhase(phase AdoptionPhase) bool {
	switch phase {
	case AdoptionPhaseClaimed, AdoptionPhaseStaged, AdoptionPhaseAdopting,
		AdoptionPhaseAdopted, AdoptionPhaseVerified, AdoptionPhaseCommitted,
		AdoptionPhaseActive, AdoptionPhaseRollingBack:
		return true
	default:
		return false
	}
}

func adoptionIdentityPhase(phase AdoptionPhase) bool {
	return phase == AdoptionPhaseAdopted || phase == AdoptionPhaseVerified ||
		phase == AdoptionPhaseCommitted || phase == AdoptionPhaseActive
}

func adoptionNeedsStage(phase AdoptionPhase) bool {
	switch phase {
	case AdoptionPhaseStaged, AdoptionPhaseAdopting, AdoptionPhaseAdopted,
		AdoptionPhaseVerified, AdoptionPhaseCommitted, AdoptionPhaseActive:
		return true
	default:
		return false
	}
}

func adoptionRollbackSourcePhase(phase AdoptionPhase) bool {
	switch phase {
	case AdoptionPhaseClaimed, AdoptionPhaseStaged, AdoptionPhaseAdopting,
		AdoptionPhaseAdopted, AdoptionPhaseVerified:
		return true
	default:
		return false
	}
}

func adoptionControlIDUsed(state AdoptionState, value string) bool {
	for _, destination := range state.Destinations {
		if destination.ControlID == value {
			return true
		}
	}
	return false
}

func adoptionBackendIDUsed(state AdoptionState, value string) bool {
	for _, destination := range state.Destinations {
		if destination.BackendID == value {
			return true
		}
	}
	return false
}

func adoptionSafeGuestIDUsed(state AdoptionState, value string) bool {
	for _, destination := range state.Destinations {
		if destination.PlannedPolicy == GuestIdentitySafeClone &&
			destination.GuestID == value {
			return true
		}
	}
	return false
}

func transitionError(action BundleAction, message string) error {
	return fmt.Errorf("%w: bundle action %q: %s", ErrInvalidStateTransition, action, message)
}

func adoptionTransitionError(transition AdoptionTransition, message string) error {
	return fmt.Errorf(
		"%w: adoption action %q for %q: %s",
		ErrInvalidStateTransition,
		transition.Action,
		transition.Destination,
		message,
	)
}

func invariantError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrStateInvariant, fmt.Sprintf(format, arguments...))
}
