package manager

import (
	"context"
	"errors"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

type MigrationImportCommitRequest struct {
	OperationID string
}

// CommitImportDestination is the one-way import activation boundary. Provider
// objects are first promoted while stopped, then profiles and environment
// records become visible through one atomic batch marker. Every input is
// deterministic from the durable operation, so a crash at any point replays
// the same provider request and the same visibility batch.
func (service MigrationImportService) CommitImportDestination(
	ctx context.Context,
	request MigrationImportCommitRequest,
) (MigrationOperation, environment.BatchPublication, error) {
	if ctx == nil || !operationIDPattern.MatchString(request.OperationID) ||
		strings.TrimSpace(service.Store.Root) == "" ||
		service.Environments.Root != service.Store.Root {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationOperation{}, environment.BatchPublication{}, err
	}
	operation, err := service.Store.Load(request.OperationID)
	if err != nil {
		return MigrationOperation{}, environment.BatchPublication{}, err
	}
	configOnly := migrationImportObjectsConfigOnly(operation.ImportObjects)
	if !configOnly && service.Import == nil {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationCapabilityUnavailable
	}
	if operation.Kind != MigrationOperationImport || operation.DestinationStage == nil {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationOperationInvalid
	}
	verifyEffect, err := migrationOperationEffect(operation, MigrationEffectVerify)
	if err != nil || verifyEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationOperationInvalid
	}
	profiles, records, err := service.migrationImportPublicationInputs(operation)
	if err != nil {
		return MigrationOperation{}, environment.BatchPublication{}, err
	}
	activationEffect, activationRequest, expectedActivation, err :=
		migrationImportActivationRequest(operation)
	if err != nil {
		return MigrationOperation{}, environment.BatchPublication{}, err
	}

	if operation.Phase == MigrationPhaseComplete {
		publication, publishErr := service.publishMigrationImportBatch(
			operation, profiles, records,
		)
		if publishErr != nil {
			return operation, environment.BatchPublication{}, publishErr
		}
		evidenceDigest, digestErr := migrationImportActivationDigest(
			expectedActivation, publication,
		)
		if digestErr != nil || activationEffect.Status != MigrationEffectSucceeded ||
			len(activationEffect.Evidence) != 1 ||
			activationEffect.Evidence[0].Digest != evidenceDigest ||
			operation.Result == nil || operation.Result.ReceiptDigest != evidenceDigest {
			return operation, publication, errors.Join(
				ErrMigrationOperationMismatch, digestErr,
			)
		}
		operation, _, err = service.Store.ReleaseClaims(operation.ID)
		return operation, publication, err
	}
	if operation.Decision == nil {
		if operation.Phase != MigrationPhaseVerifying {
			return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationOperationInvalid
		}
		if _, err := service.preflightMigrationImportBatch(
			operation, profiles, records,
		); err != nil {
			return operation, environment.BatchPublication{}, err
		}
		operation, _, err = service.Store.Decide(
			operation.ID, MigrationDecisionCommit,
		)
	} else if operation.Decision.Value != MigrationDecisionCommit {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationDecisionConflict
	} else if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseCommitting, nil,
		)
	} else if operation.Phase != MigrationPhaseCommitting {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationOperationInvalid
	}
	if err != nil {
		return MigrationOperation{}, environment.BatchPublication{}, err
	}

	activation := expectedActivation
	if activationEffect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(
			operation.ID, activationEffect.ID, activationEffect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, environment.BatchPublication{}, err
		}
		activationEffect, err = migrationOperationEffect(operation, MigrationEffectActivate)
		if err != nil {
			return MigrationOperation{}, environment.BatchPublication{}, err
		}
	}
	if activationEffect.Status == MigrationEffectRunning {
		if !configOnly {
			activation, err = service.Import.ActivateMigrationDestination(ctx, activationRequest)
			if err != nil {
				return service.importActivationFailure(operation.ID, err)
			}
			if err := activation.MatchesRequest(activationRequest); err != nil {
				return service.importActivationFailure(operation.ID, err)
			}
		}
	} else if activationEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, environment.BatchPublication{}, ErrMigrationOperationInvalid
	}

	// Recheck destination path identity at the last safe boundary. A failed
	// check after the one-way decision leaves a finish-required operation; it
	// never substitutes another path or publishes partial authority.
	if err := service.revalidateMigrationWorkspaceActionTargets(operation); err != nil {
		return service.importActivationFailure(operation.ID, err)
	}
	publication, err := service.publishMigrationImportBatch(operation, profiles, records)
	if err != nil {
		return service.importActivationFailure(operation.ID, err)
	}
	evidenceDigest, err := migrationImportActivationDigest(activation, publication)
	if err != nil {
		return service.importActivationFailure(operation.ID, err)
	}
	if activationEffect.Status != MigrationEffectSucceeded {
		operation, err = service.Store.FinishEffect(
			operation.ID, activationEffect.ID, activationEffect.Provider,
			MigrationEffectSucceeded, []MigrationEffectEvidence{{
				Code:      migrationDestinationActivationEvidenceCode,
				OpaqueRef: operation.DestinationStage.StageHandle,
				Digest:    evidenceDigest, Count: uint64(len(records)),
				ObservedAt: service.nowUTC(),
			}},
		)
		if err != nil {
			failed, _, failureErr := service.importActivationFailure(operation.ID, err)
			return failed, publication, failureErr
		}
	} else if len(activationEffect.Evidence) != 1 ||
		activationEffect.Evidence[0].Digest != evidenceDigest {
		return operation, publication, ErrMigrationOperationMismatch
	}
	operation, err = service.Store.TransitionPhase(
		operation.ID, MigrationPhaseComplete,
		&MigrationOperationResult{
			Code:          "migration.import.complete",
			ReceiptDigest: evidenceDigest,
		},
	)
	if err != nil {
		failed, _, failureErr := service.importActivationFailure(operation.ID, err)
		return failed, publication, failureErr
	}
	operation, _, err = service.Store.ReleaseClaims(operation.ID)
	return operation, publication, err
}

func migrationImportActivationRequest(
	operation MigrationOperation,
) (MigrationEffect, backend.DestinationActivationRequest, backend.DestinationActivation, error) {
	activationEffect, err := migrationOperationEffect(operation, MigrationEffectActivate)
	if err != nil {
		return MigrationEffect{}, backend.DestinationActivationRequest{}, backend.DestinationActivation{}, err
	}
	proof, err := migrationDestinationVerificationProof(operation)
	if err != nil {
		return MigrationEffect{}, backend.DestinationActivationRequest{}, backend.DestinationActivation{}, err
	}
	request := backend.DestinationActivationRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID:        migration.OpaqueID(operation.ID),
			EffectID:           activationEffect.ID,
			CapabilityRevision: operation.CapabilityRevision,
		},
		Proof: proof,
		ObjectHandles: append(
			[]migration.OpaqueID(nil), operation.DestinationStage.ObjectHandles...,
		),
	}
	if err := request.Validate(); err != nil {
		return MigrationEffect{}, backend.DestinationActivationRequest{}, backend.DestinationActivation{}, err
	}
	expected := backend.DestinationActivation{
		Binding: request.Binding, StageHandle: proof.StageHandle,
		ProofDigest:   proof.ProofDigest,
		ObjectHandles: append([]migration.OpaqueID(nil), request.ObjectHandles...),
		Stopped:       true, Promoted: true,
	}
	return activationEffect, request, expected, nil
}

func migrationImportActivationDigest(
	activation backend.DestinationActivation,
	publication environment.BatchPublication,
) (migration.Digest, error) {
	digest, err := CanonicalDigest(
		"migration-destination-activation",
		struct {
			Activation  backend.DestinationActivation `json:"activation"`
			Publication environment.BatchPublication  `json:"publication"`
		}{Activation: activation, Publication: publication},
	)
	return migration.Digest(digest), err
}

func (service MigrationImportService) migrationImportPublicationInputs(
	operation MigrationOperation,
) ([]profile.Profile, []environment.Record, error) {
	if err := service.revalidateMigrationWorkspaceActionTargets(operation); err != nil {
		return nil, nil, err
	}
	profiles, err := migrationDestinationProfiles(operation)
	if err != nil {
		return nil, nil, err
	}
	records, err := migrationDestinationEnvironmentRecords(operation, profiles)
	if err != nil {
		return nil, nil, err
	}
	return profiles, records, nil
}

func (service MigrationImportService) publishMigrationImportBatch(
	operation MigrationOperation,
	profiles []profile.Profile,
	records []environment.Record,
) (environment.BatchPublication, error) {
	states, err := migrationImportedProfileStates(operation)
	if err != nil {
		return environment.BatchPublication{}, err
	}
	return service.Environments.PublishBatchWithParticipant(
		operation.ID, records,
		profile.EnvironmentBatchParticipant{
			Store: profile.Store{Root: service.Store.Root}, Profiles: profiles,
			ImportedStates: states,
		},
	)
}

func (service MigrationImportService) preflightMigrationImportBatch(
	operation MigrationOperation,
	profiles []profile.Profile,
	records []environment.Record,
) (environment.BatchPublication, error) {
	states, err := migrationImportedProfileStates(operation)
	if err != nil {
		return environment.BatchPublication{}, err
	}
	return service.Environments.PreflightBatchWithParticipant(
		operation.ID, records,
		profile.EnvironmentBatchParticipant{
			Store: profile.Store{Root: service.Store.Root}, Profiles: profiles,
			ImportedStates: states,
		},
	)
}

func migrationImportedProfileStates(
	operation MigrationOperation,
) ([]profile.ImportedState, error) {
	if operation.DestinationStage == nil ||
		validateMigrationMaterializedProfileStates(
			operation.ID, operation.ImportObjects, operation.EnvironmentActions,
			operation.DestinationStage.ProfileStates,
		) != nil {
		return nil, ErrMigrationOperationInvalid
	}
	states := make([]profile.ImportedState, len(operation.DestinationStage.ProfileStates))
	for index, value := range operation.DestinationStage.ProfileStates {
		states[index] = profile.ImportedState{
			ProfileName: value.ProfileName, StagePath: value.StagePath,
			Owner: value.Owner(operation.ID),
		}
	}
	return states, nil
}

func (service MigrationImportService) revalidateMigrationWorkspaceActionTargets(
	operation MigrationOperation,
) error {
	for _, action := range operation.WorkspaceActions {
		if action.Decision != migrationWorkspaceDecisionMapped {
			continue
		}
		canonical, identity, err := workspaceattach.CaptureRootIdentity(action.DestinationPath)
		if err != nil || canonical != action.DestinationPath ||
			identity.Device != action.RootDevice || identity.Inode != action.RootInode {
			return ErrMigrationPlanStale
		}
		if err := ValidateWorkspaceMountSafety(canonical, service.Store.Root); err != nil {
			return ErrMigrationPlanStale
		}
	}
	return nil
}

func (service MigrationImportService) importActivationFailure(
	operationID string,
	cause error,
) (MigrationOperation, environment.BatchPublication, error) {
	operation, transitionErr := service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
	if transitionErr != nil {
		return MigrationOperation{}, environment.BatchPublication{}, errors.Join(cause, transitionErr)
	}
	return operation, environment.BatchPublication{}, cause
}
