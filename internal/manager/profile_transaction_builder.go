package manager

import (
	"context"
	"errors"
	"fmt"

	profilechanges "github.com/vibe-agi/hideout/internal/manager/profile_changes"
	"github.com/vibe-agi/hideout/internal/profile"
)

func (service *ProfileTransactionService) buildTypedProfileChanges(
	ctx context.Context,
	current profile.Profile,
	changes []TypedChange,
) (profileTransactionBuild, error) {
	if err := checkProfileTransactionContext(ctx); err != nil {
		return profileTransactionBuild{}, err
	}
	privateChanges := make([]profilechanges.Change, len(changes))
	for index, change := range changes {
		privateChanges[index] = profilechanges.Change{
			Kind:  change.Kind,
			Value: append([]byte(nil), change.Value...),
		}
	}
	result, err := profilechanges.Build(
		current,
		privateChanges,
		profilechanges.Options{
			ProfileDir: service.Core.Store.ProfileDir(current.Name),
			Now:        service.now,
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, profilechanges.ErrNoChange):
			return profileTransactionBuild{}, fmt.Errorf(
				"%w: %v",
				ErrConfigurationNoChange,
				err,
			)
		case errors.Is(err, profilechanges.ErrInvalidChange),
			errors.Is(err, profilechanges.ErrUnknownChange):
			return profileTransactionBuild{}, fmt.Errorf(
				"%w: %v",
				ErrInvalidConfigurationDraft,
				err,
			)
		default:
			return profileTransactionBuild{}, err
		}
	}
	if err := checkProfileTransactionContext(ctx); err != nil {
		return profileTransactionBuild{}, err
	}

	diff := make([]ReviewDiff, len(result.Diff))
	for index, entry := range result.Diff {
		diff[index] = ReviewDiff{
			Kind:   entry.Kind,
			Field:  entry.Field,
			Before: entry.Before,
			After:  entry.After,
			Scope:  entry.Scope,
		}
	}
	warnings := make([]Warning, len(result.Warnings))
	for index, entry := range result.Warnings {
		warnings[index] = Warning{
			Code:    entry.Code,
			Summary: entry.Summary,
		}
	}
	build := profileTransactionBuild{
		Desired: result.Desired,
		Diff:    diff,
		Effects: []PlannedEffect{{
			ID:       "persist-profile",
			Kind:     "persist",
			Scope:    "profile",
			Provider: "manager.profile",
			Live:     true,
			Summary:  "Persist the reviewed profile configuration.",
			ProofRequired: []string{
				"profile-committed",
			},
		}},
		Blockers: []Blocker{},
		Warnings: warnings,
		Rollback: RollbackPlan{
			Mode:    "restore-previous",
			Summary: "Restore the previous profile configuration.",
			Effects: []string{"persist-profile"},
		},
	}
	if service.NetworkTransitions != nil &&
		current.Network != result.Desired.Network &&
		onlyNetworkTypedChanges(changes) {
		review, err := service.NetworkTransitions.Plan(
			ctx,
			current,
			result.Desired,
		)
		if err != nil {
			return profileTransactionBuild{}, err
		}
		build.NetworkTransitions = append(
			[]NetworkTransitionPlan(nil),
			review.Plans...,
		)
		liveEffects := profileNetworkPlannedEffects(review.Plans)
		build.Effects = append(build.Effects, liveEffects...)
		build.Blockers = append(build.Blockers, review.Blockers...)
		build.Warnings = append(build.Warnings, review.Warnings...)
		for _, effect := range liveEffects {
			build.Rollback.Effects = append(
				build.Rollback.Effects,
				effect.ID,
			)
		}
		if len(liveEffects) != 0 {
			build.Rollback.Summary = "Restore the previous profile configuration and the prior effective route."
		}
	} else if service.NetworkTransitions != nil &&
		current.Network != result.Desired.Network {
		build.Warnings = append(build.Warnings, Warning{
			Code:    "network-live-mixed-transaction-deferred",
			Summary: "This draft combines network and non-network fields; the reviewed profile is persisted atomically, while live route reconciliation waits for a dedicated network transaction or the next eligible attach.",
		})
	}
	return build, nil
}

func onlyNetworkTypedChanges(changes []TypedChange) bool {
	if len(changes) == 0 {
		return false
	}
	for _, change := range changes {
		switch change.Kind {
		case ChangeNetworkPosture,
			ChangeNetworkProxyRef,
			ChangeNetworkDNS:
		default:
			return false
		}
	}
	return true
}
