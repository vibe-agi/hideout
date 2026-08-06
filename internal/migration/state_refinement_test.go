package migration

import (
	"errors"
	"testing"
)

func TestBundleTransitionTraceRefinesMigrationBundle(t *testing.T) {
	state, err := NewBundleState(BundleStateOptions{
		SourceDigest:  "source-digest",
		SourceStopped: false,
		MaxChunks:     3,
		MaxCrashes:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyBundleTransition(state, BundleTransition{
		Action: BundleAcquireClaim,
	}); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("unstopped source acquired a claim: %v", err)
	}

	state = applyBundleStep(t, state, BundleStopSource)
	state = applyBundleStep(t, state, BundleAcquireClaim)
	if !state.ClaimHeld || state.Phase != BundlePhaseClaimed {
		t.Fatalf("AcquireClaim trace drifted: %+v", state)
	}
	state = applyBundleStep(t, state, BundleCreateSnapshot)
	if state.ClaimHeld || !state.SnapshotIndependent ||
		state.SnapshotEffects != 1 {
		t.Fatalf("CreateSnapshot trace drifted: %+v", state)
	}
	state = applyBundleStep(t, state, BundleBeginWriting)
	state = applyBundleStep(t, state, BundleWriteNextChunk)
	state = applyBundleStep(t, state, BundleCheckpointPrefix)
	state = applyBundleStep(t, state, BundleWriteNextChunk)
	state = applyBundleStep(t, state, BundleCrash)
	if state.DaemonUp || state.TailAuthentic || state.Written != 2 ||
		state.Checkpoint != 1 {
		t.Fatalf("Crash trace did not preserve only authenticated prefix: %+v", state)
	}
	state = applyBundleStep(t, state, BundleRestart)
	state = applyBundleStep(t, state, BundleTruncateUnverifiedTail)
	if state.Written != 1 || !state.TailAuthentic {
		t.Fatalf("resume trace did not truncate torn tail: %+v", state)
	}
	for state.Written < state.MaxChunks {
		state = applyBundleStep(t, state, BundleWriteNextChunk)
		state = applyBundleStep(t, state, BundleCheckpointPrefix)
	}
	state = applyBundleStep(t, state, BundleSeal)
	if !state.Importable() || state.SourceDigest != state.InitialSourceDigest ||
		state.SealEffects != 1 {
		t.Fatalf("Seal trace violated import/source/effect invariant: %+v", state)
	}
	if _, err := ApplyBundleTransition(state, BundleTransition{
		Action: BundleSeal,
	}); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("duplicate seal was not rejected: %v", err)
	}
	state = applyBundleStep(t, state, BundleTamper)
	if state.Importable() {
		t.Fatal("tampered bundle remained importable")
	}
}

func TestBundleCancellationNeverPublishes(t *testing.T) {
	state, err := NewBundleState(BundleStateOptions{
		SourceDigest:  "source-digest",
		SourceStopped: true,
		MaxChunks:     2,
		MaxCrashes:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []BundleAction{
		BundleAcquireClaim,
		BundleCreateSnapshot,
		BundleBeginWriting,
		BundleWriteNextChunk,
	} {
		state = applyBundleStep(t, state, action)
	}
	state, err = ApplyBundleTransition(state, BundleTransition{
		Action: BundleRequestCancel, RetainPartial: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	state = applyBundleStep(t, state, BundleCancel)
	if state.Phase != BundlePhaseCancelled || state.Published || state.Footer ||
		state.ClaimHeld || state.SnapshotExists || state.Written != 0 ||
		state.Checkpoint != 0 || !state.PartialRetained {
		t.Fatalf("Cancel trace retained publishable state: %+v", state)
	}
	if state.Importable() {
		t.Fatal("retained cancellation partial became importable")
	}
	state = applyBundleStep(t, state, BundleRemoveRetainedPartial)
	if state.PartialRetained {
		t.Fatalf("explicit retained-partial removal did not close cleanup: %+v", state)
	}
}

func TestAdoptionTraceRefinesIndependentMultiDestinationImports(t *testing.T) {
	state, err := NewAdoptionState(AdoptionStateOptions{
		BundleDigest:    "sealed-bundle-digest",
		BundleValid:     true,
		SourceControlID: "source-control",
		SourceBackendID: "source-backend",
		SourceGuestID:   "source-guest",
		MaxCrashes:      2,
		Destinations: []DestinationDraft{
			{ID: "host-b", RequestedName: "dev-b", Policy: GuestIdentitySafeClone},
			{ID: "host-c", RequestedName: "dev-c", Policy: GuestIdentitySafeClone},
			{ID: "host-exact", RequestedName: "dev-exact", Policy: GuestIdentityExactRestore},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := state.BundleDigest

	plans := map[string][2]string{
		"host-b":     {"control-b", "backend-b"},
		"host-c":     {"control-c", "backend-c"},
		"host-exact": {"control-exact", "backend-exact"},
	}
	for _, destination := range []string{"host-b", "host-c", "host-exact"} {
		ids := plans[destination]
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action:      AdoptionPlanDestination,
			Destination: destination,
			ControlID:   ids[0],
			BackendID:   ids[1],
		})
	}
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionApproveAuthority, Destination: "host-b",
	})

	for _, destination := range []string{"host-b", "host-c", "host-exact"} {
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionAcquireNameClaim, Destination: destination,
		})
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionStageDestination, Destination: destination,
		})
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionBegin, Destination: destination,
		})
		guestID := ""
		if destination == "host-b" {
			guestID = "guest-b"
		}
		if destination == "host-c" {
			guestID = "guest-c"
		}
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionFinish, Destination: destination, GuestID: guestID,
		})
		if !state.Destinations[destination].DiskFidelityProved {
			t.Fatalf("adoption receipt did not prove disk fidelity for %s", destination)
		}
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionVerifyDestination, Destination: destination,
		})
	}

	state = applyAdoptionStep(t, state, AdoptionTransition{Action: AdoptionCrash})
	state = applyAdoptionStep(t, state, AdoptionTransition{Action: AdoptionRestart})
	for _, destination := range []string{"host-b", "host-c", "host-exact"} {
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionDecideCommit, Destination: destination,
		})
		state = applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionActivate, Destination: destination,
		})
	}

	if state.BundleDigest != originalDigest {
		t.Fatalf("imports mutated sealed bundle binding: %q", state.BundleDigest)
	}
	b := state.Destinations["host-b"]
	c := state.Destinations["host-c"]
	exact := state.Destinations["host-exact"]
	if b.GuestID == c.GuestID || b.GuestID == state.SourceGuestID ||
		c.GuestID == state.SourceGuestID {
		t.Fatalf("Safe Clone identities are not fresh and distinct: b=%+v c=%+v", b, c)
	}
	if exact.GuestID != state.SourceGuestID {
		t.Fatalf("Exact Guest Restore did not preserve identity: %+v", exact)
	}
	if !b.AuthorityEffective || c.AuthorityEffective || exact.AuthorityEffective {
		t.Fatalf("authority did not follow per-import approval: b=%+v c=%+v exact=%+v", b, c, exact)
	}
	for destination, record := range state.Destinations {
		if record.Phase != AdoptionPhaseActive || !record.Runnable ||
			!record.DiskFidelityProved ||
			record.StageEffects != 1 || record.AdoptionEffects != 1 ||
			record.CommitEffects != 1 {
			t.Fatalf("destination %s activation trace drifted: %+v", destination, record)
		}
	}
}

func TestAdoptionConflictRollbackAndInvalidBundleStayNonRunnable(t *testing.T) {
	state, err := NewAdoptionState(AdoptionStateOptions{
		BundleDigest:    "sealed-bundle-digest",
		BundleValid:     true,
		SourceControlID: "source-control",
		SourceBackendID: "source-backend",
		SourceGuestID:   "source-guest",
		MaxCrashes:      1,
		Destinations: []DestinationDraft{
			{ID: "first", RequestedName: "dev", Policy: GuestIdentitySafeClone},
			{ID: "second", RequestedName: "dev", Policy: GuestIdentitySafeClone},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionPlanDestination, Destination: "first",
		ControlID: "control-first", BackendID: "backend-first",
	})
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionPlanDestination, Destination: "second",
		ControlID: "control-second", BackendID: "backend-second",
	})
	beforeClaim := state
	next, err := ApplyAdoptionTransition(state, AdoptionTransition{
		Action: AdoptionAcquireNameClaim, Destination: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if beforeClaim.Destinations["first"].Phase != AdoptionPhasePlanned ||
		beforeClaim.NameOwners["dev"] != "" {
		t.Fatalf("pure transition mutated its input: %+v", beforeClaim)
	}
	state = next
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionBlockNameConflict, Destination: "second",
	})
	if second := state.Destinations["second"]; second.Phase != AdoptionPhaseBlocked ||
		second.ControlID != "" || second.BackendID != "" || second.Runnable {
		t.Fatalf("conflicting import retained destination identity: %+v", second)
	}

	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionStageDestination, Destination: "first",
	})
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionRequestRollback, Destination: "first",
	})
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionRollback, Destination: "first",
	})
	first := state.Destinations["first"]
	if first.Phase != AdoptionPhaseRolledBack || first.Staged || first.Runnable ||
		first.DiskFidelityProved ||
		first.ControlID != "" || first.BackendID != "" ||
		state.NameOwners["dev"] != "" {
		t.Fatalf("rollback retained visible or claimed state: state=%+v", state)
	}

	invalid, err := NewAdoptionState(AdoptionStateOptions{
		BundleDigest:    "invalid-bundle-digest",
		BundleValid:     false,
		SourceControlID: "source-control",
		SourceBackendID: "source-backend",
		SourceGuestID:   "source-guest",
		Destinations: []DestinationDraft{{
			ID: "invalid", RequestedName: "dev-invalid", Policy: GuestIdentitySafeClone,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid = applyAdoptionStep(t, invalid, AdoptionTransition{
		Action: AdoptionRejectInvalidBundle, Destination: "invalid",
	})
	if invalid.Destinations["invalid"].Phase != AdoptionPhaseBlocked {
		t.Fatalf("invalid bundle was not blocked: %+v", invalid)
	}
	if _, err := ApplyAdoptionTransition(invalid, AdoptionTransition{
		Action: AdoptionStageDestination, Destination: "invalid",
	}); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("invalid bundle reached staging: %v", err)
	}
}

func TestAdoptionReplacementRequiresIndependentConfirmationAndFreshReplan(t *testing.T) {
	state, err := NewAdoptionState(AdoptionStateOptions{
		BundleDigest: "sealed-bundle-digest", BundleValid: true,
		SourceControlID: "source-control", SourceBackendID: "source-backend",
		SourceGuestID: "source-guest", MaxCrashes: 1,
		Destinations: []DestinationDraft{{
			ID: "replacement", RequestedName: "dev", Policy: GuestIdentitySafeClone,
		}},
		ExistingNames: []string{"dev"},
	})
	if err != nil {
		t.Fatal(err)
	}
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionPlanDestination, Destination: "replacement",
		ControlID: "control-before-delete", BackendID: "backend-before-delete",
	})
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionPlanReplacement, Destination: "replacement",
	})
	if _, err := ApplyAdoptionTransition(state, AdoptionTransition{
		Action: AdoptionDeleteReplacement, Destination: "replacement",
	}); err == nil {
		t.Fatal("replacement delete succeeded without its independent confirmation")
	}
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionConfirmReplacement, Destination: "replacement",
	})
	state = applyAdoptionStep(t, state, AdoptionTransition{Action: AdoptionCrash})
	state = applyAdoptionStep(t, state, AdoptionTransition{Action: AdoptionRestart})
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionDeleteReplacement, Destination: "replacement",
	})
	afterDelete := state.Destinations["replacement"]
	if afterDelete.Phase != AdoptionPhaseDraft || !afterDelete.ReplacementConfirmed ||
		!afterDelete.ReplacementDeleted || afterDelete.ReplacementEffects != 1 ||
		afterDelete.ControlID != "" || afterDelete.BackendID != "" ||
		afterDelete.Runnable || state.NameOwners["dev"] != "" {
		t.Fatalf("replacement delete crossed its replan boundary: state=%+v", state)
	}
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionPlanDestination, Destination: "replacement",
		ControlID: "control-after-delete", BackendID: "backend-after-delete",
	})
	state = applyAdoptionStep(t, state, AdoptionTransition{
		Action: AdoptionAcquireNameClaim, Destination: "replacement",
	})
	if destination := state.Destinations["replacement"]; destination.ControlID != "control-after-delete" ||
		destination.BackendID != "backend-after-delete" ||
		state.NameOwners["dev"] != "replacement" {
		t.Fatalf("post-delete import did not use a fresh plan: %+v", state)
	}
}

func TestAdoptionRenameAndRefusalNeverDeleteExistingOwner(t *testing.T) {
	newState := func() AdoptionState {
		state, err := NewAdoptionState(AdoptionStateOptions{
			BundleDigest: "sealed-bundle-digest", BundleValid: true,
			SourceControlID: "source-control", SourceBackendID: "source-backend",
			SourceGuestID: "source-guest",
			Destinations: []DestinationDraft{{
				ID: "destination", RequestedName: "dev", Policy: GuestIdentitySafeClone,
			}},
			ExistingNames: []string{"dev"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return applyAdoptionStep(t, state, AdoptionTransition{
			Action: AdoptionPlanDestination, Destination: "destination",
			ControlID: "control-destination", BackendID: "backend-destination",
		})
	}
	refused := applyAdoptionStep(t, newState(), AdoptionTransition{
		Action: AdoptionBlockNameConflict, Destination: "destination",
	})
	if refused.NameOwners["dev"] != adoptionExistingNameOwner ||
		refused.Destinations["destination"].Phase != AdoptionPhaseBlocked {
		t.Fatalf("default refusal changed existing owner: %+v", refused)
	}

	renamed := applyAdoptionStep(t, newState(), AdoptionTransition{
		Action: AdoptionRenameDestination, Destination: "destination",
		RequestedName: "dev-copy",
	})
	if renamed.NameOwners["dev"] != adoptionExistingNameOwner ||
		renamed.Destinations["destination"].RequestedName != "dev-copy" ||
		renamed.Destinations["destination"].Phase != AdoptionPhaseDraft {
		t.Fatalf("rename changed existing owner or retained stale plan: %+v", renamed)
	}
}

func TestStateInvariantNegativeFixtures(t *testing.T) {
	bundle, err := NewBundleState(BundleStateOptions{
		SourceDigest: "source", SourceStopped: true, MaxChunks: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutatedBundle := bundle
	mutatedBundle.SourceDigest = "changed"
	if !errors.Is(mutatedBundle.Validate(), ErrStateInvariant) {
		t.Fatal("source-content mutation escaped the bundle invariant judge")
	}
	mutatedBundle = bundle
	mutatedBundle.Published = true
	if !errors.Is(mutatedBundle.Validate(), ErrStateInvariant) {
		t.Fatal("unsealed publication escaped the bundle invariant judge")
	}

	adoption, err := NewAdoptionState(AdoptionStateOptions{
		BundleDigest:    "bundle",
		BundleValid:     true,
		SourceControlID: "source-control",
		SourceBackendID: "source-backend",
		SourceGuestID:   "source-guest",
		Destinations: []DestinationDraft{{
			ID: "destination", RequestedName: "dev", Policy: GuestIdentitySafeClone,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	adoption = applyAdoptionStep(t, adoption, AdoptionTransition{
		Action: AdoptionPlanDestination, Destination: "destination",
		ControlID: "destination-control", BackendID: "destination-backend",
	})
	mutatedAdoption := adoption.Clone()
	mutatedAdoption.BundleDigest = "changed"
	if !errors.Is(mutatedAdoption.Validate(), ErrStateInvariant) {
		t.Fatal("sealed-bundle mutation escaped the adoption invariant judge")
	}
	mutatedAdoption = adoption.Clone()
	record := mutatedAdoption.Destinations["destination"]
	record.Policy = GuestIdentityExactRestore
	mutatedAdoption.Destinations["destination"] = record
	if !errors.Is(mutatedAdoption.Validate(), ErrStateInvariant) {
		t.Fatal("post-plan identity policy change escaped the adoption invariant judge")
	}
	mutatedAdoption = adoption.Clone()
	record = mutatedAdoption.Destinations["destination"]
	record.Runnable = true
	mutatedAdoption.Destinations["destination"] = record
	if !errors.Is(mutatedAdoption.Validate(), ErrStateInvariant) {
		t.Fatal("preactivation runnable state escaped the adoption invariant judge")
	}
	mutatedAdoption = adoption.Clone()
	record = mutatedAdoption.Destinations["destination"]
	record.AuthorityEffective = true
	mutatedAdoption.Destinations["destination"] = record
	if !errors.Is(mutatedAdoption.Validate(), ErrStateInvariant) {
		t.Fatal("unapproved authority escaped the adoption invariant judge")
	}

	proved := adoption
	for _, transition := range []AdoptionTransition{
		{Action: AdoptionAcquireNameClaim, Destination: "destination"},
		{Action: AdoptionStageDestination, Destination: "destination"},
		{Action: AdoptionBegin, Destination: "destination"},
		{Action: AdoptionFinish, Destination: "destination", GuestID: "destination-guest"},
		{Action: AdoptionVerifyDestination, Destination: "destination"},
		{Action: AdoptionDecideCommit, Destination: "destination"},
		{Action: AdoptionActivate, Destination: "destination"},
	} {
		proved = applyAdoptionStep(t, proved, transition)
	}
	mutatedAdoption = proved.Clone()
	record = mutatedAdoption.Destinations["destination"]
	record.DiskFidelityProved = false
	mutatedAdoption.Destinations["destination"] = record
	if !errors.Is(mutatedAdoption.Validate(), ErrStateInvariant) {
		t.Fatal("runnable destination without disk fidelity escaped the invariant judge")
	}
}

func applyBundleStep(t *testing.T, state BundleState, action BundleAction) BundleState {
	t.Helper()
	next, err := ApplyBundleTransition(state, BundleTransition{Action: action})
	if err != nil {
		t.Fatalf("bundle action %q: %v\nstate=%+v", action, err, state)
	}
	if err := next.Validate(); err != nil {
		t.Fatalf("bundle action %q violated invariant: %v\nstate=%+v", action, err, next)
	}
	return next
}

func applyAdoptionStep(
	t *testing.T,
	state AdoptionState,
	transition AdoptionTransition,
) AdoptionState {
	t.Helper()
	next, err := ApplyAdoptionTransition(state, transition)
	if err != nil {
		t.Fatalf("adoption action %q for %q: %v\nstate=%+v", transition.Action, transition.Destination, err, state)
	}
	if err := next.Validate(); err != nil {
		t.Fatalf("adoption action %q violated invariant: %v\nstate=%+v", transition.Action, err, next)
	}
	return next
}
