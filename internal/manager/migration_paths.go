package manager

import (
	"errors"
	"fmt"
	"sort"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

type migrationWorkspaceProposalBinding struct {
	EnvironmentRef migration.OpaqueID
	Proposal       migration.WorkspaceProposal
}

// planMigrationWorkspaceActions converts authenticated source proposals and
// explicit destination decisions into immutable destination authority. Host
// path hints from the bundle are never consulted as destination paths.
func (service MigrationImportService) planMigrationWorkspaceActions(
	draft migration.ImportDraft,
	manifest migration.Manifest,
) ([]migration.WorkspaceAction, []migration.OpaqueID, error) {
	proposals, err := migrationWorkspaceProposals(
		draft.SelectedEnvironmentRefs, manifest,
	)
	if err != nil {
		return nil, nil, err
	}
	mappings := make(map[migration.OpaqueID]migration.WorkspaceMapping, len(draft.WorkspaceMappings))
	for _, mapping := range draft.WorkspaceMappings {
		if _, exists := mappings[mapping.ProposalID]; exists {
			return nil, nil, ErrMigrationPlanInvalid
		}
		mappings[mapping.ProposalID] = mapping
	}

	actions := make([]migration.WorkspaceAction, 0, len(mappings))
	unresolved := make([]migration.OpaqueID, 0, len(proposals))
	for _, binding := range proposals {
		mapping, exists := mappings[binding.Proposal.ProposalID]
		if !exists {
			unresolved = append(unresolved, binding.Proposal.ProposalID)
			continue
		}
		action := migration.WorkspaceAction{
			ProposalID: binding.Proposal.ProposalID, EnvironmentRef: binding.EnvironmentRef,
			GuestPath: binding.Proposal.GuestPath, Decision: mapping.Decision,
		}
		switch mapping.Decision {
		case migrationWorkspaceDecisionDisabled:
		case migrationWorkspaceDecisionMapped:
			canonical, identity, err := workspaceattach.CaptureRootIdentity(mapping.DestinationPath)
			if err != nil {
				return nil, nil, errors.Join(
					ErrMigrationRequestInvalid,
					fmt.Errorf("resolve workspace proposal %s: %w", mapping.ProposalID, err),
				)
			}
			if !validMigrationAbsolutePath(canonical) || identity.Validate() != nil || identity.Device == 0 {
				return nil, nil, ErrMigrationRequestInvalid
			}
			if err := ValidateWorkspaceMountSafety(canonical, service.Store.Root); err != nil {
				return nil, nil, errors.Join(ErrMigrationRequestInvalid, err)
			}
			action.DestinationPath = canonical
			action.RootDevice = identity.Device
			action.RootInode = identity.Inode
		default:
			return nil, nil, ErrMigrationRequestInvalid
		}
		actions = append(actions, action)
		delete(mappings, mapping.ProposalID)
	}
	if len(mappings) != 0 {
		return nil, nil, ErrMigrationPlanInvalid
	}
	return actions, unresolved, nil
}

// revalidateMigrationWorkspaceActions rejects path replacement, symlink
// retargeting, proposal substitution, and newly reserved paths between review
// and apply. Apply calls it before reserving an operation or invoking a provider.
func (service MigrationImportService) revalidateMigrationWorkspaceActions(
	plan migration.ImportPlan,
	manifest migration.Manifest,
) error {
	proposals, err := migrationWorkspaceProposals(migrationImportObjectRefs(plan.Objects), manifest)
	if err != nil || len(proposals) != len(plan.WorkspaceActions) {
		return ErrMigrationPlanStale
	}
	for index, binding := range proposals {
		action := plan.WorkspaceActions[index]
		if action.ProposalID != binding.Proposal.ProposalID ||
			action.EnvironmentRef != binding.EnvironmentRef ||
			action.GuestPath != binding.Proposal.GuestPath {
			return ErrMigrationPlanStale
		}
		if action.Decision == migrationWorkspaceDecisionDisabled {
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

func migrationWorkspaceProposals(
	environmentRefs []migration.OpaqueID,
	manifest migration.Manifest,
) ([]migrationWorkspaceProposalBinding, error) {
	selected := make(map[migration.OpaqueID]struct{}, len(environmentRefs))
	for _, ref := range environmentRefs {
		if _, duplicate := selected[ref]; duplicate || !migrationValidOperationOpaqueID(ref) {
			return nil, ErrMigrationPlanInvalid
		}
		selected[ref] = struct{}{}
	}
	bindings := make([]migrationWorkspaceProposalBinding, 0)
	seen := make(map[migration.OpaqueID]struct{})
	for _, environment := range manifest.Environments {
		if _, exists := selected[environment.SourceEnvironmentRef]; !exists {
			continue
		}
		delete(selected, environment.SourceEnvironmentRef)
		for _, proposal := range environment.WorkspaceProposals {
			if _, duplicate := seen[proposal.ProposalID]; duplicate {
				return nil, ErrMigrationPlanInvalid
			}
			seen[proposal.ProposalID] = struct{}{}
			bindings = append(bindings, migrationWorkspaceProposalBinding{
				EnvironmentRef: environment.SourceEnvironmentRef, Proposal: proposal,
			})
		}
	}
	if len(selected) != 0 {
		return nil, ErrMigrationPlanInvalid
	}
	sort.Slice(bindings, func(left, right int) bool {
		return bindings[left].Proposal.ProposalID < bindings[right].Proposal.ProposalID
	})
	return bindings, nil
}
