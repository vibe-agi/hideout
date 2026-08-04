package manager

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	migrationConflictDecisionReplace = "replace"
	migrationConflictDecisionRefuse  = "refuse"
	migrationConflictKindEnvironment = "environment-name"
	migrationConflictKindProfile     = "profile-name"
)

func (service MigrationImportService) planMigrationConflicts(
	draft migration.ImportDraft,
	objects []migration.ImportObject,
) ([]migration.ConflictAction, []migration.PlanNotice, error) {
	decisions := make(map[migration.OpaqueID]migration.ConflictDecision, len(draft.ConflictDecisions))
	for _, decision := range draft.ConflictDecisions {
		decisions[decision.SourceRef] = decision
	}
	actions := make([]migration.ConflictAction, 0)
	blockers := make([]migration.PlanNotice, 0)
	for _, object := range objects {
		decision, replacing := decisions[object.SourceRef]
		record, recordErr := service.Environments.LoadByName(object.DestinationName)
		switch {
		case recordErr == nil:
			if replacing {
				return nil, nil, ErrMigrationPlanStale
			}
			// A completed replacement decision proves an older exact record was
			// deleted. It never authorizes deleting whichever record owns the name
			// now; a new occupant is a fresh conflict.
			actions = append(actions, refusedEnvironmentConflictAction(object, record))
			blockers = append(blockers, migration.PlanNotice{
				Code:    "migration.destination.name_conflict",
				Summary: "The reviewed destination environment name is already in use.",
				Remediation: fmt.Sprintf(
					"Choose a different name with --name %s=<new-name>, or stop the exact existing environment and rerun with --replace %s. Replacement is a separate permanent delete with no automatic rollback.",
					object.SourceRef, object.SourceRef,
				),
				SourceRef: object.SourceRef,
			})
		case !errors.Is(recordErr, environment.ErrNameNotFound):
			return nil, nil, recordErr
		case replacing:
			action, err := service.proveMigrationReplacement(object, decision)
			if err != nil {
				return nil, nil, err
			}
			actions = append(actions, action)
		}

		occupied, err := migrationDestinationProfileOccupied(
			profile.Store{Root: service.Store.Root}, object.DestinationName,
		)
		if err != nil {
			return nil, nil, err
		}
		if occupied {
			actions = append(actions, refusedProfileConflictAction(object))
			blockers = append(blockers, migration.PlanNotice{
				Code:        "migration.destination.profile_name_conflict",
				Summary:     "The reviewed destination profile name is already in use.",
				Remediation: fmt.Sprintf("Choose a different destination name with --name %s=<new-name>; migration never overwrites an existing profile.", object.SourceRef),
				SourceRef:   object.SourceRef,
			})
		}
	}
	for sourceRef := range decisions {
		if !slices.ContainsFunc(objects, func(object migration.ImportObject) bool {
			return object.SourceRef == sourceRef
		}) {
			return nil, nil, ErrMigrationRequestInvalid
		}
	}
	sortMigrationConflictActions(actions)
	sortMigrationPlanNotices(blockers)
	return actions, blockers, nil
}

func refusedEnvironmentConflictAction(
	object migration.ImportObject,
	record environment.Record,
) migration.ConflictAction {
	return migration.ConflictAction{
		SourceRef: object.SourceRef, DestinationName: object.DestinationName,
		Kind: migrationConflictKindEnvironment, Decision: migrationConflictDecisionRefuse,
		ExistingEnvironmentID: record.ID, ExistingStatus: record.Status,
		Destructive: false,
		Effects:     []string{"No destination object changes; import remains blocked."},
		Recovery:    "No recovery is needed because the conflict plan is read-only.",
	}
}

func refusedProfileConflictAction(
	object migration.ImportObject,
) migration.ConflictAction {
	return migration.ConflictAction{
		SourceRef: object.SourceRef, DestinationName: object.DestinationName,
		Kind: migrationConflictKindProfile, Decision: migrationConflictDecisionRefuse,
		Destructive: false,
		Effects:     []string{"No profile changes; import remains blocked."},
		Recovery:    "No recovery is needed because the conflict plan is read-only.",
	}
}

func (service MigrationImportService) proveMigrationReplacement(
	object migration.ImportObject,
	decision migration.ConflictDecision,
) (migration.ConflictAction, error) {
	if decision.SourceRef != object.SourceRef ||
		decision.Decision != migrationConflictDecisionReplace ||
		!operationIDPattern.MatchString(decision.LifecycleOperationID) ||
		decision.LifecyclePlanDigest.Validate() != nil {
		return migration.ConflictAction{}, ErrMigrationRequestInvalid
	}
	actions := EnvironmentActionService{Core: New(profile.Store{Root: service.Store.Root})}
	record, err := actions.loadPlanRecord(decision.LifecycleOperationID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return migration.ConflictAction{}, ErrMigrationPlanStale
		}
		return migration.ConflictAction{}, err
	}
	operation, err := actions.operationStore().Load(decision.LifecycleOperationID)
	if err != nil {
		return migration.ConflictAction{}, err
	}
	result, err := terminalEnvironmentActionResult(record, operation)
	if err != nil || record.Plan.Action != EnvironmentActionDelete ||
		record.Plan.PlanDigest != string(decision.LifecyclePlanDigest) ||
		len(record.Plan.Targets) == 0 || len(record.Plan.Skipped) != 0 ||
		len(record.Records) != len(record.Plan.Targets) ||
		len(result.Applied) != len(record.Plan.Targets) {
		return migration.ConflictAction{}, ErrMigrationPlanStale
	}
	var prior environment.Record
	for _, snapshot := range record.Records {
		if !strings.EqualFold(snapshot.Record.Name, object.DestinationName) {
			continue
		}
		if prior.ID != "" || snapshot.EnvironmentID != snapshot.Record.ID ||
			!slices.ContainsFunc(record.Plan.Targets, func(target EnvironmentActionTarget) bool {
				return target.ID == snapshot.Record.ID
			}) || !slices.ContainsFunc(result.Applied, func(target EnvironmentActionTarget) bool {
			return target.ID == snapshot.Record.ID
		}) {
			return migration.ConflictAction{}, ErrMigrationPlanStale
		}
		prior = snapshot.Record
	}
	if prior.ID == "" {
		return migration.ConflictAction{}, ErrMigrationPlanStale
	}
	if _, err := service.Environments.LoadByName(object.DestinationName); err == nil {
		return migration.ConflictAction{}, ErrMigrationPlanStale
	} else if !errors.Is(err, environment.ErrNameNotFound) {
		return migration.ConflictAction{}, err
	}
	return migration.ConflictAction{
		SourceRef: object.SourceRef, DestinationName: object.DestinationName,
		Kind: migrationConflictKindEnvironment, Decision: migrationConflictDecisionReplace,
		ExistingEnvironmentID: prior.ID, ExistingStatus: prior.Status,
		LifecycleOperationID: decision.LifecycleOperationID,
		LifecyclePlanDigest:  decision.LifecyclePlanDigest,
		Destructive:          true,
		Effects: []string{
			fmt.Sprintf("The separately confirmed lifecycle operation permanently deleted environment %s.", prior.ID),
			fmt.Sprintf("This import may create a new environment named %s; it does not reuse the deleted VM identity.", object.DestinationName),
		},
		Recovery: "The prior delete cannot be rolled back automatically. If the new import fails, restore the prior environment from a separately retained migration bundle.",
	}, nil
}

func validateMigrationConflictDecisions(
	decisions []migration.ConflictDecision,
) error {
	var previous migration.OpaqueID
	for _, decision := range decisions {
		if !migrationValidOperationOpaqueID(decision.SourceRef) ||
			(previous != "" && previous >= decision.SourceRef) ||
			decision.Decision != migrationConflictDecisionReplace ||
			!operationIDPattern.MatchString(decision.LifecycleOperationID) ||
			decision.LifecyclePlanDigest.Validate() != nil {
			return ErrMigrationRequestInvalid
		}
		previous = decision.SourceRef
	}
	return nil
}

func validateMigrationConflictActions(actions []migration.ConflictAction) error {
	previous := ""
	for _, action := range actions {
		key := string(action.SourceRef) + "\x00" + action.Kind
		if !migrationValidOperationOpaqueID(action.SourceRef) || key <= previous ||
			!migrationDestinationNamePattern.MatchString(action.DestinationName) ||
			environment.ValidateName(action.DestinationName) != nil ||
			len(action.Effects) == 0 || len(action.Effects) > 4 ||
			!boundedMigrationDisplayText(action.Recovery, 2048) ||
			action.Recovery != redactMigrationText(action.Recovery) {
			return ErrMigrationPlanInvalid
		}
		for _, effect := range action.Effects {
			if !boundedMigrationDisplayText(effect, 2048) || effect != redactMigrationText(effect) {
				return ErrMigrationPlanInvalid
			}
		}
		switch action.Kind {
		case migrationConflictKindEnvironment:
			if !environment.ValidID(action.ExistingEnvironmentID) ||
				!slices.Contains([]string{
					environment.StatusNew, environment.StatusCreated, environment.StatusReady,
					environment.StatusRunning, environment.StatusStopped, environment.StatusError,
				}, action.ExistingStatus) {
				return ErrMigrationPlanInvalid
			}
		case migrationConflictKindProfile:
			if action.ExistingEnvironmentID != "" || action.ExistingStatus != "" {
				return ErrMigrationPlanInvalid
			}
		default:
			return ErrMigrationPlanInvalid
		}
		switch action.Decision {
		case migrationConflictDecisionRefuse:
			if action.Destructive || action.LifecycleOperationID != "" ||
				action.LifecyclePlanDigest != "" {
				return ErrMigrationPlanInvalid
			}
		case migrationConflictDecisionReplace:
			if action.Kind != migrationConflictKindEnvironment || !action.Destructive ||
				!operationIDPattern.MatchString(action.LifecycleOperationID) ||
				action.LifecyclePlanDigest.Validate() != nil {
				return ErrMigrationPlanInvalid
			}
		default:
			return ErrMigrationPlanInvalid
		}
		previous = key
	}
	return nil
}

func validateMigrationConflictActionClosure(
	objects []migration.ImportObject,
	actions []migration.ConflictAction,
) error {
	bySource := make(map[migration.OpaqueID]string, len(objects))
	for _, object := range objects {
		bySource[object.SourceRef] = object.DestinationName
	}
	for _, action := range actions {
		if bySource[action.SourceRef] != action.DestinationName {
			return ErrMigrationPlanInvalid
		}
	}
	return nil
}

func (service MigrationImportService) revalidateMigrationConflictActions(
	plan migration.ImportPlan,
) error {
	actual := make([]migration.ConflictAction, 0, len(plan.ConflictActions))
	for _, action := range plan.ConflictActions {
		if action.Decision != migrationConflictDecisionReplace {
			return ErrMigrationPlanStale
		}
		object, found := migrationImportObjectBySource(plan.Objects, action.SourceRef)
		if !found {
			return ErrMigrationPlanStale
		}
		proved, err := service.proveMigrationReplacement(object, migration.ConflictDecision{
			SourceRef: action.SourceRef, Decision: action.Decision,
			LifecycleOperationID: action.LifecycleOperationID,
			LifecyclePlanDigest:  action.LifecyclePlanDigest,
		})
		if err != nil {
			return err
		}
		actual = append(actual, proved)
	}
	sortMigrationConflictActions(actual)
	if !migrationConflictActionsEqual(actual, plan.ConflictActions) {
		return ErrMigrationPlanStale
	}
	return nil
}

func migrationImportObjectBySource(
	objects []migration.ImportObject,
	sourceRef migration.OpaqueID,
) (migration.ImportObject, bool) {
	for _, object := range objects {
		if object.SourceRef == sourceRef {
			return object, true
		}
	}
	return migration.ImportObject{}, false
}

func sortMigrationConflictActions(actions []migration.ConflictAction) {
	sort.Slice(actions, func(left, right int) bool {
		leftKey := string(actions[left].SourceRef) + "\x00" + actions[left].Kind
		rightKey := string(actions[right].SourceRef) + "\x00" + actions[right].Kind
		return leftKey < rightKey
	})
}

func cloneMigrationConflictActions(
	actions []migration.ConflictAction,
) []migration.ConflictAction {
	cloned := make([]migration.ConflictAction, len(actions))
	for index, action := range actions {
		cloned[index] = action
		cloned[index].Effects = append([]string(nil), action.Effects...)
	}
	return cloned
}

func migrationConflictActionsEqual(
	left, right []migration.ConflictAction,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !reflect.DeepEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}
