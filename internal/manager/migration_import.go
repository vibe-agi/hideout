package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

type MigrationBundleInspectRequest struct {
	BundlePath        string
	ExpectedBinding   migration.BundleBinding
	SecretInputHandle string
	ClientBinding     string
}

type MigrationBundleInspection struct {
	Binding    migration.BundleBinding
	BundleFile MigrationBundleFileBinding
	Manifest   migration.Manifest
}

// MigrationBundleSource authenticates and validates sealed input before
// returning typed facts. Its production implementation owns passphrase use and
// bounded hostile-input parsing; providers never receive a path or key.
type MigrationBundleSource interface {
	InspectMigrationBundle(context.Context, MigrationBundleInspectRequest) (MigrationBundleInspection, error)
}

type MigrationImportPlanRequest struct {
	Draft             migration.ImportDraft `json:"draft"`
	SecretInputHandle string                `json:"secretInputHandle"`
	ClientBinding     string                `json:"-"`
}

type MigrationImportApplyRequest struct {
	Schema            string                    `json:"schema"`
	Plan              migration.ImportPlan      `json:"plan"`
	Confirmation      MigrationPlanConfirmation `json:"confirmation"`
	SecretInputHandle string                    `json:"secretInputHandle"`
	ClientBinding     string                    `json:"-"`
	IdempotencyKey    string                    `json:"idempotencyKey"`
}

const (
	migrationWorkspaceDecisionMapped   = "mapped"
	migrationWorkspaceDecisionDisabled = "disabled"
	migrationSecretDecisionExistingRef = "existing-ref"
	migrationSecretDecisionImportValue = "import-value"
	migrationSecretDecisionUnresolved  = "unresolved"
	migrationAuthorityDecisionDisabled = "disabled"
	migrationAuthorityDecisionApproved = "approved"
	migrationAuthorityDecisionRejected = "rejected"
)

// BundleSource is separate from the backend provider because it owns bundle
// crypto and hostile-input parsing rather than VM/storage authority.
type MigrationImportService struct {
	MigrationService
	BundleSource    MigrationBundleSource
	InspectionCache *MigrationInspectionCache
}

func (service MigrationImportService) PlanImport(
	ctx context.Context,
	request MigrationImportPlanRequest,
) (migration.ImportPlan, error) {
	if ctx == nil || service.BundleSource == nil ||
		!validClientBinding(request.ClientBinding) ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) {
		return migration.ImportPlan{}, ErrMigrationRequestInvalid
	}
	draft, err := normalizeMigrationImportDraft(request.Draft)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	inspection, err := service.BundleSource.InspectMigrationBundle(
		ctx, MigrationBundleInspectRequest{
			BundlePath: draft.BundlePath, ExpectedBinding: draft.BundleBinding,
			SecretInputHandle: request.SecretInputHandle, ClientBinding: request.ClientBinding,
		},
	)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	if err := validateMigrationBundleInspection(inspection, draft.BundlePath, draft.BundleBinding); err != nil {
		return migration.ImportPlan{}, err
	}
	if err := validateMigrationImportDraftReferences(draft, inspection.Manifest); err != nil {
		return migration.ImportPlan{}, err
	}
	objects, actions, selectedDisks, selectedEdges, err :=
		migrationImportSelectionFromDraft(draft, inspection.Manifest)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	configOnly := migrationImportObjectsConfigOnly(objects)
	var capability backend.MigrationCapabilities
	if configOnly {
		capability, err = service.configMigrationCapability(ctx)
	} else {
		capability, err = service.importCapability(ctx)
	}
	if err != nil {
		return migration.ImportPlan{}, err
	}
	environmentActions, err := migrationImportEnvironmentActions(objects, inspection.Manifest)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	workspaceActions, unresolvedWorkspaces, err :=
		service.planMigrationWorkspaceActions(draft, inspection.Manifest)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	secretActions, unresolvedSecrets, err := service.planMigrationSecretActions(
		ctx, draft, inspection.Manifest,
	)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	authorityActions, disabled, authorityBlockers, err := planMigrationAuthorityActions(
		draft, inspection.Manifest, secretActions,
	)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	planID, err := service.newMigrationID("migplan")
	if err != nil {
		return migration.ImportPlan{}, err
	}
	effectProvider := capability.Provider
	if configOnly {
		effectProvider = "manager"
	}
	effects, err := service.newImportPlannedEffects(
		effectProvider, migrationHasImportedSecretValues(secretActions),
	)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	stageEffect := effects[0]
	binding := backend.MigrationEffectBinding{
		OperationID: planID, EffectID: stageEffect.ID,
		CapabilityRevision: capability.Revision,
	}
	logicalBytes, err := migrationDiskLogicalBytes(selectedDisks)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	requiredBytes := logicalBytes
	capacity := migration.CapacityRequirement{}
	var profileStateBytes uint64
	if !configOnly {
		var stateErr error
		profileStateBytes, stateErr = migrationProfileStateLogicalBytes(environmentActions)
		if stateErr != nil {
			return migration.ImportPlan{}, stateErr
		}
		capacity, err = migrationImportCapacityRequirement(
			inspection.BundleFile.Size, selectedDisks, profileStateBytes,
		)
		if err != nil {
			return migration.ImportPlan{}, err
		}
		requiredBytes = capacity.PeakAdditionalBytes
	}
	destination := backend.DestinationInventory{
		Binding: binding, CapabilityRevision: capability.Revision,
		Compatible: true, AvailableBytes: requiredBytes,
		Conflicts: []migration.OpaqueID{},
		Blockers:  []backend.MigrationProviderBlocker{},
	}
	blockers := append([]migration.PlanNotice(nil), authorityBlockers...)
	if !configOnly {
		destinationRequest := backend.DestinationInspectionRequest{
			Binding: binding, ManifestDigest: draft.BundleBinding.ManifestDigest,
			SourceProduct:   inspection.Manifest.SourceProduct,
			EnvironmentRefs: migrationImportObjectRefs(objects),
			Disks:           selectedDisks, ProfileStateBytes: profileStateBytes,
			Edges: selectedEdges,
			RequiredCapabilities: append(
				[]migration.RequiredCapability(nil), inspection.Manifest.RequiredCapabilities...,
			),
			RequiredBytes: requiredBytes, Capacity: capacity,
		}
		if err := destinationRequest.Validate(); err != nil {
			return migration.ImportPlan{}, err
		}
		destination, err = service.Import.InspectMigrationDestination(ctx, destinationRequest)
		if err != nil {
			return migration.ImportPlan{}, err
		}
		if err := destination.Validate(); err != nil {
			return migration.ImportPlan{}, err
		}
		blockers = migrationDestinationBlockers(destination, binding, capability, requiredBytes)
	}
	baseRevision, err := service.destinationEnvironmentBaseRevision()
	if err != nil {
		return migration.ImportPlan{}, err
	}
	conflictActions, conflictBlockers, err := service.planMigrationConflicts(
		draft, objects,
	)
	if err != nil {
		return migration.ImportPlan{}, err
	}
	blockers = append(blockers, conflictBlockers...)
	for _, proposalID := range unresolvedWorkspaces {
		blockers = append(blockers, migration.PlanNotice{
			Code:        "migration.workspace.mapping_required",
			Summary:     "A destination workspace mapping must be resolved before import.",
			Remediation: "Map the workspace to a reviewed destination path or keep it disabled.",
			SourceRef:   proposalID,
		})
	}
	for _, secretRef := range unresolvedSecrets {
		blockers = append(blockers, migration.PlanNotice{
			Code:        "migration.secret.mapping_required",
			Summary:     "A destination secret mapping is missing, unavailable, occupied, or incompatible.",
			Remediation: "Map the secret to an available existing reference, or import an explicitly bundled value into a new reference.",
			SourceRef:   secretRef,
		})
	}
	sortMigrationPlanNotices(blockers)
	compatibility := migration.Compatibility{
		Backend: capability.Provider, Available: destination.Compatible,
		CapabilityRevision: capability.Revision,
		RequiredBytes:      requiredBytes, AvailableBytes: destination.AvailableBytes,
		Capacity: capacity,
	}
	if !compatibility.Available {
		compatibility.ReasonCode = "migration.provider.compatibility_unproved"
		if len(blockers) != 0 {
			compatibility.ReasonCode = blockers[0].Code
		}
	}
	risks := append([]string(nil), draft.RiskAcknowledgements...)
	slices.Sort(risks)
	plan := migration.ImportPlan{
		Schema: MigrationImportPlanSchema, PlanID: planID,
		BundlePath: draft.BundlePath, BundleBinding: draft.BundleBinding,
		BaseRevisions: []migration.BaseRevision{baseRevision},
		Compatibility: compatibility, Objects: objects,
		ConflictActions:    conflictActions,
		EnvironmentActions: environmentActions, IdentityActions: actions,
		WorkspaceActions: workspaceActions, SecretActions: secretActions,
		AuthorityActions: authorityActions, DisabledProposals: disabled,
		RiskAcknowledgements: risks, Effects: effects, Blockers: blockers,
	}
	if err := SealMigrationImportPlan(&plan); err != nil {
		return migration.ImportPlan{}, err
	}
	return plan, nil
}

func (service MigrationImportService) ApplyImport(
	ctx context.Context,
	request MigrationImportApplyRequest,
) (MigrationApplyResult, error) {
	if ctx == nil || service.BundleSource == nil || request.Schema != MigrationImportApplySchema ||
		!validClientBinding(request.ClientBinding) ||
		!migrationIdempotencyKeyPattern.MatchString(request.IdempotencyKey) ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) {
		return MigrationApplyResult{}, ErrMigrationRequestInvalid
	}
	if err := VerifyMigrationImportPlan(request.Plan); err != nil {
		return MigrationApplyResult{}, err
	}
	if len(request.Plan.Blockers) != 0 || !request.Plan.Compatibility.Available {
		return MigrationApplyResult{}, ErrMigrationCapabilityUnavailable
	}
	if err := validateMigrationConfirmation(
		request.Plan.PlanDigest, request.Plan.RiskAcknowledgements,
		migrationApprovedAuthorityProposalIDs(request.Plan.AuthorityActions),
		request.Confirmation,
	); err != nil {
		return MigrationApplyResult{}, err
	}
	destinationNamespace, err := service.Store.DestinationNamespace()
	if err != nil {
		return MigrationApplyResult{}, err
	}
	operationID := migrationOperationID(
		destinationNamespace, request.ClientBinding, request.IdempotencyKey,
	)
	if existing, err := service.Store.Load(operationID); err == nil {
		return migrationApplyReplay(existing, MigrationOperationImport, request.Plan.PlanID, request.Plan.PlanDigest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return MigrationApplyResult{}, err
	}
	inspection, err := service.BundleSource.InspectMigrationBundle(
		ctx, MigrationBundleInspectRequest{
			BundlePath: request.Plan.BundlePath, ExpectedBinding: request.Plan.BundleBinding,
			SecretInputHandle: request.SecretInputHandle, ClientBinding: request.ClientBinding,
		},
	)
	if err != nil {
		if existing, loadErr := service.Store.Load(operationID); loadErr == nil {
			return migrationApplyReplay(existing, MigrationOperationImport, request.Plan.PlanID, request.Plan.PlanDigest)
		}
		return MigrationApplyResult{}, err
	}
	if err := validateMigrationBundleInspection(
		inspection, request.Plan.BundlePath, request.Plan.BundleBinding,
	); err != nil {
		return MigrationApplyResult{}, err
	}
	stageEffect, err := plannedMigrationEffect(request.Plan.Effects, "stage-destination")
	if err != nil {
		return MigrationApplyResult{}, err
	}
	binding := backend.MigrationEffectBinding{
		OperationID: migration.OpaqueID(operationID), EffectID: stageEffect.ID,
		CapabilityRevision: request.Plan.Compatibility.CapabilityRevision,
	}
	selectedDisks, selectedEdges, err := migrationImportSelectionFromPlan(
		request.Plan, inspection.Manifest,
	)
	if err != nil {
		return MigrationApplyResult{}, err
	}
	if err := service.revalidateImportDestination(
		ctx, request.Plan, inspection.Manifest, selectedDisks, selectedEdges, binding,
	); err != nil {
		return MigrationApplyResult{}, err
	}
	configOnly := migrationImportObjectsConfigOnly(request.Plan.Objects)
	var capability backend.MigrationCapabilities
	if configOnly {
		capability, err = service.configMigrationCapability(ctx)
	} else {
		capability, err = service.importCapability(ctx)
	}
	if err != nil || capability.Revision != request.Plan.Compatibility.CapabilityRevision ||
		(!configOnly && capability.AdoptionHelper == nil) {
		return MigrationApplyResult{}, ErrMigrationPlanStale
	}
	operation, err := service.buildImportOperation(
		request.Plan, inspection.BundleFile, inspection.Manifest, capability,
		operationID, selectedDisks, selectedEdges,
	)
	if err != nil {
		return MigrationApplyResult{}, err
	}
	reserved, created, err := service.Store.Reserve(operation)
	if err != nil {
		return MigrationApplyResult{}, err
	}
	return migrationApplyResult(reserved, created), nil
}

func (service MigrationImportService) revalidateImportDestination(
	ctx context.Context,
	plan migration.ImportPlan,
	manifest migration.Manifest,
	disks []migration.DiskObject,
	edges []migration.DiskEdge,
	binding backend.MigrationEffectBinding,
) error {
	configOnly := migrationImportObjectsConfigOnly(plan.Objects)
	var capability backend.MigrationCapabilities
	var err error
	if configOnly {
		capability, err = service.configMigrationCapability(ctx)
	} else {
		capability, err = service.importCapability(ctx)
	}
	if err != nil || capability.Revision != plan.Compatibility.CapabilityRevision {
		return ErrMigrationPlanStale
	}
	base, err := service.destinationEnvironmentBaseRevision()
	if err != nil || len(plan.BaseRevisions) != 1 || plan.BaseRevisions[0] != base {
		return ErrMigrationPlanStale
	}
	for _, object := range plan.Objects {
		if _, err := service.Environments.LoadByName(object.DestinationName); err == nil {
			return ErrMigrationPlanStale
		} else if !errors.Is(err, environment.ErrNameNotFound) {
			return err
		}
		occupied, err := migrationDestinationProfileOccupied(
			profile.Store{Root: service.Store.Root}, object.DestinationName,
		)
		if err != nil {
			return err
		}
		if occupied {
			return ErrMigrationPlanStale
		}
	}
	if err := service.revalidateMigrationConflictActions(plan); err != nil {
		return err
	}
	if err := revalidateMigrationEnvironmentActions(plan, manifest); err != nil {
		return err
	}
	if err := service.revalidateMigrationWorkspaceActions(plan, manifest); err != nil {
		return err
	}
	if err := service.revalidateMigrationSecretActions(ctx, plan, manifest); err != nil {
		return err
	}
	if err := revalidateMigrationAuthorityActions(plan, manifest); err != nil {
		return err
	}
	if configOnly {
		if len(disks) != 0 || len(edges) != 0 || plan.Compatibility.RequiredBytes != 0 ||
			plan.Compatibility.Backend != capability.Provider ||
			plan.Compatibility.AvailableBytes != 0 || !plan.Compatibility.Capacity.IsZero() ||
			!plan.Compatibility.Available {
			return ErrMigrationPlanStale
		}
		return nil
	}
	profileStateBytes, err := migrationProfileStateLogicalBytes(plan.EnvironmentActions)
	if err != nil {
		return ErrMigrationPlanStale
	}
	capacity, err := migrationImportCapacityRequirement(
		int64(plan.Compatibility.Capacity.BundleBytes), disks, profileStateBytes,
	)
	if err != nil || capacity != plan.Compatibility.Capacity ||
		capacity.PeakAdditionalBytes != plan.Compatibility.RequiredBytes {
		return ErrMigrationPlanStale
	}
	destinationRequest := backend.DestinationInspectionRequest{
		Binding: binding, ManifestDigest: plan.BundleBinding.ManifestDigest,
		SourceProduct:   manifest.SourceProduct,
		EnvironmentRefs: migrationImportObjectRefs(plan.Objects),
		Disks:           disks, ProfileStateBytes: profileStateBytes,
		Edges:                edges,
		RequiredCapabilities: append([]migration.RequiredCapability(nil), manifest.RequiredCapabilities...),
		RequiredBytes:        capacity.PeakAdditionalBytes,
		Capacity:             capacity,
	}
	if err := destinationRequest.Validate(); err != nil {
		return err
	}
	destination, err := service.Import.InspectMigrationDestination(ctx, destinationRequest)
	if err != nil {
		return err
	}
	if err := destination.Validate(); err != nil {
		return err
	}
	if destination.Binding != binding || !destination.Compatible ||
		destination.CapabilityRevision != capability.Revision || len(destination.Blockers) != 0 ||
		destination.AvailableBytes < plan.Compatibility.RequiredBytes {
		return ErrMigrationPlanStale
	}
	return nil
}

func migrationDestinationProfileOccupied(store profile.Store, name string) (bool, error) {
	if profile.ValidateName(name) != nil || strings.TrimSpace(store.Root) == "" {
		return false, ErrMigrationPlanInvalid
	}
	entries, err := os.ReadDir(filepath.Join(store.Root, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), name) {
			// Any node occupies the case-insensitive profile namespace. A malformed
			// or pending node is not treated as absence and cannot be overwritten by
			// an import plan.
			return true, nil
		}
	}
	return false, nil
}

func (service MigrationImportService) buildImportOperation(
	plan migration.ImportPlan,
	bundleFile MigrationBundleFileBinding,
	manifest migration.Manifest,
	capability backend.MigrationCapabilities,
	operationID string,
	disks []migration.DiskObject,
	edges []migration.DiskEdge,
) (MigrationOperation, error) {
	configOnly := migrationImportObjectsConfigOnly(plan.Objects)
	if err := manifest.Validate(migration.DefaultLimits()); err != nil ||
		capability.Validate() != nil || capability.Revision != plan.Compatibility.CapabilityRevision ||
		(configOnly && (capability.Provider != "native" || capability.FullImport)) ||
		(!configOnly && (!capability.FullImport || capability.AdoptionHelper == nil)) {
		return MigrationOperation{}, ErrMigrationPlanInvalid
	}
	identities := make([]MigrationDestinationIdentity, len(plan.IdentityActions))
	sourceByRef := make(
		map[migration.OpaqueID]migration.GuestIdentityEvidence, len(manifest.Environments),
	)
	for _, environment := range manifest.Environments {
		sourceByRef[environment.SourceEnvironmentRef] = environment.GuestIdentityEvidence
	}
	sourceIdentities := make([]MigrationSourceGuestIdentity, 0, len(plan.IdentityActions))
	claims := make(
		[]MigrationClaim, 0,
		len(plan.Objects)*3+len(plan.WorkspaceActions)+len(plan.SecretActions)+1,
	)
	for index, action := range plan.IdentityActions {
		sourceIdentity, exists := sourceByRef[action.SourceRef]
		if !exists || (configOnly && !migration.IsConfigIdentityUnavailableEvidence(sourceIdentity)) ||
			(!configOnly && sourceIdentity.Validate() != nil) {
			return MigrationOperation{}, ErrMigrationPlanInvalid
		}
		if !configOnly {
			sourceIdentities = append(sourceIdentities, MigrationSourceGuestIdentity{
				SourceRef: action.SourceRef, Evidence: sourceIdentity,
			})
		}
		identities[index] = migrationDestinationIdentity(operationID, action.SourceRef)
		for _, spec := range []struct {
			class MigrationClaimClass
			key   string
		}{
			{MigrationClaimDestinationName, plan.Objects[index].DestinationName},
			{MigrationClaimDestinationProfile, plan.EnvironmentActions[index].DestinationProfileName},
			{MigrationClaimDestinationControl, string(identities[index].ControlIdentity)},
			{MigrationClaimBackendObject, string(identities[index].BackendIdentity)},
		} {
			claim, err := NewMigrationClaim(spec.class, spec.key)
			if err != nil {
				return MigrationOperation{}, err
			}
			claims = append(claims, claim)
		}
	}
	diskIdentities := []MigrationDestinationDiskIdentity{}
	if !configOnly {
		var diskErr error
		diskIdentities, diskErr = migrationDestinationDiskIdentities(
			operationID, plan.Objects, identities, disks, edges,
		)
		if diskErr != nil {
			return MigrationOperation{}, diskErr
		}
	}
	for _, identity := range diskIdentities {
		if identity.Role != migration.DiskRoleAttached {
			continue
		}
		claim, err := NewMigrationClaim(
			MigrationClaimBackendObject, string(identity.BackendIdentity),
		)
		if err != nil {
			return MigrationOperation{}, err
		}
		claims = append(claims, claim)
	}
	claimedWorkspaces := make(map[string]struct{}, len(plan.WorkspaceActions))
	for _, action := range plan.WorkspaceActions {
		if action.Decision != migrationWorkspaceDecisionMapped {
			continue
		}
		if _, exists := claimedWorkspaces[action.DestinationPath]; exists {
			continue
		}
		claim, err := NewMigrationClaim(
			MigrationClaimDestinationWorkspace, action.DestinationPath,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
		claims = append(claims, claim)
		claimedWorkspaces[action.DestinationPath] = struct{}{}
	}
	claimedSecrets := make(map[string]struct{}, len(plan.SecretActions))
	for _, action := range plan.SecretActions {
		if _, exists := claimedSecrets[action.DestinationRef]; exists {
			continue
		}
		claim, err := NewMigrationClaim(
			MigrationClaimSecretDestination, action.DestinationRef,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
		claims = append(claims, claim)
		claimedSecrets[action.DestinationRef] = struct{}{}
	}
	stagingRoot := filepath.Join(service.Store.Root, "migration", "staging", operationID)
	stagingClaim, err := NewMigrationClaim(MigrationClaimStagingRoot, stagingRoot)
	if err != nil {
		return MigrationOperation{}, err
	}
	claims = append(claims, stagingClaim)
	SortMigrationClaims(claims)
	effects, err := migrationImportOperationEffects(plan.Effects)
	if err != nil {
		return MigrationOperation{}, err
	}
	logical, err := migrationDiskLogicalBytes(disks)
	if err != nil {
		return MigrationOperation{}, err
	}
	now := service.nowUTC()
	var helper *migration.HelperBinding
	if !configOnly {
		value := migration.HelperBinding{
			PackageID: capability.AdoptionHelper.PackageID,
			Version:   capability.AdoptionHelper.Version, SHA256: capability.AdoptionHelper.Digest,
		}
		if err := value.Validate(); err != nil {
			return MigrationOperation{}, ErrMigrationPlanInvalid
		}
		helper = &value
	}
	operation := MigrationOperation{
		Schema: MigrationOperationSchema, ID: operationID,
		Kind: MigrationOperationImport, PlanID: plan.PlanID, PlanDigest: plan.PlanDigest,
		Bundle: MigrationOperationBundleBinding{
			BundleID:         plan.BundleBinding.BundleID,
			FormatVersion:    plan.BundleBinding.FormatVersion,
			FileDigest:       plan.BundleBinding.FileDigest,
			ManifestDigest:   plan.BundleBinding.ManifestDigest,
			CompletionDigest: plan.BundleBinding.CompletionDigest,
		},
		BundlePath: plan.BundlePath, BundleFile: cloneBundleFileBinding(&bundleFile),
		BaseRevisions:      append([]migration.BaseRevision(nil), plan.BaseRevisions...),
		CapabilityRevision: plan.Compatibility.CapabilityRevision,
		Phase:              MigrationPhaseClaiming, Revision: 1, Claims: claims, Effects: effects,
		ImportObjects:             cloneMigrationImportObjects(plan.Objects),
		ConflictActions:           cloneMigrationConflictActions(plan.ConflictActions),
		EnvironmentActions:        append([]migration.EnvironmentAction(nil), plan.EnvironmentActions...),
		ExpectedDisks:             cloneMigrationDiskObjects(disks),
		ExpectedDiskEdges:         append([]migration.DiskEdge(nil), edges...),
		IdentityActions:           append([]migration.IdentityAction(nil), plan.IdentityActions...),
		WorkspaceActions:          append([]migration.WorkspaceAction(nil), plan.WorkspaceActions...),
		SecretActions:             cloneMigrationSecretActions(plan.SecretActions),
		AuthorityActions:          append([]migration.AuthorityAction(nil), plan.AuthorityActions...),
		DisabledProposals:         append([]migration.OpaqueID(nil), plan.DisabledProposals...),
		SourceGuestIdentities:     cloneMigrationSourceGuestIdentities(sourceIdentities),
		AdoptionHelper:            helper,
		DestinationIdentities:     identities,
		DestinationDiskIdentities: diskIdentities,
		Warnings:                  []MigrationNotice{},
		Progress: MigrationProgress{
			LogicalTotalKnown: true, TotalLogicalBytes: logical,
			ComponentsTotal: uint32(len(disks)), PhaseStartedAt: now,
		},
		Recovery: MigrationRecovery{
			Code: migrationRecoveryCodeNone, Action: MigrationRecoveryNone,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if configOnly {
		for _, action := range plan.EnvironmentActions {
			if action.ProfileLogicalBytes > migration.HardMaxLogicalBytes-operation.Progress.TotalLogicalBytes {
				return MigrationOperation{}, ErrMigrationPlanInvalid
			}
			operation.Progress.TotalLogicalBytes += action.ProfileLogicalBytes
		}
		operation.Progress.ComponentsTotal = uint32(len(plan.EnvironmentActions))
	} else {
		profileStateBytes, stateErr := migrationProfileStateLogicalBytes(plan.EnvironmentActions)
		if stateErr != nil || profileStateBytes > migration.HardMaxLogicalBytes-operation.Progress.TotalLogicalBytes {
			return MigrationOperation{}, ErrMigrationPlanInvalid
		}
		operation.Progress.TotalLogicalBytes += profileStateBytes
		operation.Progress.ComponentsTotal += uint32(len(plan.EnvironmentActions))
	}
	if err := operation.Validate(); err != nil {
		return MigrationOperation{}, err
	}
	return operation, nil
}

func migrationDestinationDiskIdentities(
	operationID string,
	objects []migration.ImportObject,
	destinations []MigrationDestinationIdentity,
	disks []migration.DiskObject,
	edges []migration.DiskEdge,
) ([]MigrationDestinationDiskIdentity, error) {
	if len(objects) != len(destinations) || len(disks) == 0 {
		return nil, ErrMigrationPlanInvalid
	}
	backendByEnvironment := make(map[migration.OpaqueID]migration.OpaqueID, len(objects))
	for index, object := range objects {
		if destinations[index].SourceRef != object.SourceRef {
			return nil, ErrMigrationPlanInvalid
		}
		backendByEnvironment[object.SourceRef] = destinations[index].BackendIdentity
	}
	rootBackendByDisk := make(map[migration.OpaqueID]migration.OpaqueID)
	for _, edge := range edges {
		if edge.Attachment != migration.DiskRoleRoot {
			continue
		}
		backendIdentity, exists := backendByEnvironment[edge.EnvironmentRef]
		if !exists {
			return nil, ErrMigrationPlanInvalid
		}
		rootBackendByDisk[edge.DiskID] = backendIdentity
	}
	identities := make([]MigrationDestinationDiskIdentity, len(disks))
	for index, disk := range disks {
		backendIdentity := rootBackendByDisk[disk.DiskID]
		if disk.Role == migration.DiskRoleAttached {
			digest := sha256.Sum256([]byte(
				"hideout-migration-disk-identity/v1\x00" + operationID + "\x00" + string(disk.DiskID),
			))
			backendIdentity = migration.OpaqueID(
				"disk_" + hex.EncodeToString(digest[:20]),
			)
		}
		if !migrationValidOperationOpaqueID(backendIdentity) {
			return nil, ErrMigrationPlanInvalid
		}
		identities[index] = MigrationDestinationDiskIdentity{
			DiskID: disk.DiskID, Role: disk.Role, BackendIdentity: backendIdentity,
		}
	}
	return identities, nil
}

func cloneMigrationImportObjects(
	objects []migration.ImportObject,
) []migration.ImportObject {
	cloned := make([]migration.ImportObject, len(objects))
	for index, object := range objects {
		cloned[index] = object
		cloned[index].DiskRefs = append([]migration.OpaqueID(nil), object.DiskRefs...)
	}
	return cloned
}

func migrationImportObjectsEqual(
	left, right []migration.ImportObject,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].SourceRef != right[index].SourceRef ||
			left[index].DestinationName != right[index].DestinationName ||
			left[index].Mode != right[index].Mode ||
			!slices.Equal(left[index].DiskRefs, right[index].DiskRefs) {
			return false
		}
	}
	return true
}

func (service MigrationService) importCapability(
	ctx context.Context,
) (backend.MigrationCapabilities, error) {
	if service.Import == nil {
		return backend.MigrationCapabilities{}, ErrMigrationCapabilityUnavailable
	}
	capability, err := service.Import.MigrationCapabilities(ctx)
	if err != nil {
		return backend.MigrationCapabilities{}, err
	}
	if capability.Validate() != nil || !capability.FullImport {
		return backend.MigrationCapabilities{}, ErrMigrationCapabilityUnavailable
	}
	return capability, nil
}

func normalizeMigrationImportDraft(draft migration.ImportDraft) (migration.ImportDraft, error) {
	if draft.Schema != MigrationImportDraftSchema || !validMigrationAbsolutePath(draft.BundlePath) ||
		validateMigrationBundleBinding(draft.BundleBinding) != nil ||
		len(draft.SelectedEnvironmentRefs) == 0 ||
		len(draft.SelectedEnvironmentRefs) > int(migration.HardMaxEnvironments) ||
		len(draft.NameMappings) != len(draft.SelectedEnvironmentRefs) ||
		len(draft.IdentityPolicies) != len(draft.SelectedEnvironmentRefs) ||
		len(draft.ConflictDecisions) > int(migration.HardMaxEnvironments) ||
		len(draft.WorkspaceMappings) > 128 || len(draft.SecretMappings) > 256 ||
		len(draft.AuthorityDecisions) > 1024 {
		return migration.ImportDraft{}, ErrMigrationRequestInvalid
	}
	normalized := draft
	normalized.SelectedEnvironmentRefs = append([]migration.OpaqueID(nil), draft.SelectedEnvironmentRefs...)
	normalized.NameMappings = append([]migration.NameMapping(nil), draft.NameMappings...)
	normalized.ConflictDecisions = append([]migration.ConflictDecision(nil), draft.ConflictDecisions...)
	normalized.IdentityPolicies = append([]migration.IdentitySelection(nil), draft.IdentityPolicies...)
	normalized.WorkspaceMappings = append([]migration.WorkspaceMapping(nil), draft.WorkspaceMappings...)
	normalized.SecretMappings = append([]migration.SecretMapping(nil), draft.SecretMappings...)
	normalized.AuthorityDecisions = append([]migration.AuthorityDecision(nil), draft.AuthorityDecisions...)
	normalized.RiskAcknowledgements = append([]string(nil), draft.RiskAcknowledgements...)
	slices.Sort(normalized.SelectedEnvironmentRefs)
	sort.Slice(normalized.NameMappings, func(left, right int) bool {
		return normalized.NameMappings[left].SourceRef < normalized.NameMappings[right].SourceRef
	})
	sort.Slice(normalized.ConflictDecisions, func(left, right int) bool {
		return normalized.ConflictDecisions[left].SourceRef < normalized.ConflictDecisions[right].SourceRef
	})
	sort.Slice(normalized.IdentityPolicies, func(left, right int) bool {
		return normalized.IdentityPolicies[left].SourceRef < normalized.IdentityPolicies[right].SourceRef
	})
	sort.Slice(normalized.WorkspaceMappings, func(left, right int) bool {
		return normalized.WorkspaceMappings[left].ProposalID < normalized.WorkspaceMappings[right].ProposalID
	})
	sort.Slice(normalized.SecretMappings, func(left, right int) bool {
		return normalized.SecretMappings[left].SourceRef < normalized.SecretMappings[right].SourceRef
	})
	sort.Slice(normalized.AuthorityDecisions, func(left, right int) bool {
		return normalized.AuthorityDecisions[left].ProposalID < normalized.AuthorityDecisions[right].ProposalID
	})
	if validateSortedMigrationOpaqueIDs(normalized.SelectedEnvironmentRefs, false) != nil {
		return migration.ImportDraft{}, ErrMigrationRequestInvalid
	}
	names := make(map[string]struct{}, len(normalized.NameMappings))
	hasExact := false
	for index, sourceRef := range normalized.SelectedEnvironmentRefs {
		mapping := normalized.NameMappings[index]
		identity := normalized.IdentityPolicies[index]
		if mapping.SourceRef != sourceRef || identity.SourceRef != sourceRef ||
			!migrationDestinationNamePattern.MatchString(mapping.DestinationName) ||
			environment.ValidateName(mapping.DestinationName) != nil ||
			(identity.Policy != migration.GuestIdentitySafeClone &&
				identity.Policy != migration.GuestIdentityExactRestore) {
			return migration.ImportDraft{}, ErrMigrationRequestInvalid
		}
		nameKey := strings.ToLower(mapping.DestinationName)
		if _, exists := names[nameKey]; exists {
			return migration.ImportDraft{}, ErrMigrationRequestInvalid
		}
		names[nameKey] = struct{}{}
		hasExact = hasExact || identity.Policy == migration.GuestIdentityExactRestore
	}
	if err := validateMigrationWorkspaceMappings(normalized.WorkspaceMappings); err != nil {
		return migration.ImportDraft{}, err
	}
	if err := validateMigrationConflictDecisions(normalized.ConflictDecisions); err != nil {
		return migration.ImportDraft{}, err
	}
	if err := validateMigrationSecretMappings(normalized.SecretMappings); err != nil {
		return migration.ImportDraft{}, err
	}
	if err := validateMigrationAuthorityDecisions(normalized.AuthorityDecisions); err != nil {
		return migration.ImportDraft{}, err
	}
	hasSelectedSecret := false
	for _, mapping := range normalized.SecretMappings {
		hasSelectedSecret = hasSelectedSecret ||
			mapping.Decision == migrationSecretDecisionImportValue
	}
	slices.Sort(normalized.RiskAcknowledgements)
	if validateMigrationRiskCodes(normalized.RiskAcknowledgements) != nil ||
		hasExact != slices.Contains(normalized.RiskAcknowledgements, MigrationRiskExactGuestRestore) ||
		hasSelectedSecret != slices.Contains(
			normalized.RiskAcknowledgements, MigrationRiskSelectedSecrets,
		) {
		return migration.ImportDraft{}, ErrMigrationRequestInvalid
	}
	return normalized, nil
}

func validateMigrationWorkspaceMappings(mappings []migration.WorkspaceMapping) error {
	var previous migration.OpaqueID
	for _, mapping := range mappings {
		if !migrationValidOperationOpaqueID(mapping.ProposalID) ||
			(previous != "" && previous >= mapping.ProposalID) {
			return ErrMigrationRequestInvalid
		}
		switch mapping.Decision {
		case migrationWorkspaceDecisionDisabled:
			if mapping.DestinationPath != "" {
				return ErrMigrationRequestInvalid
			}
		case migrationWorkspaceDecisionMapped:
			if !validMigrationAbsolutePath(mapping.DestinationPath) {
				return ErrMigrationRequestInvalid
			}
		default:
			return ErrMigrationRequestInvalid
		}
		previous = mapping.ProposalID
	}
	return nil
}

func validateMigrationSecretMappings(mappings []migration.SecretMapping) error {
	var previous migration.OpaqueID
	for _, mapping := range mappings {
		if !migrationValidOperationOpaqueID(mapping.SourceRef) ||
			(previous != "" && previous >= mapping.SourceRef) {
			return ErrMigrationRequestInvalid
		}
		switch mapping.Decision {
		case migrationSecretDecisionUnresolved:
			if mapping.DestinationRef != "" {
				return ErrMigrationRequestInvalid
			}
		case migrationSecretDecisionExistingRef, migrationSecretDecisionImportValue:
			if !migrationDestinationNamePattern.MatchString(mapping.DestinationRef) {
				return ErrMigrationRequestInvalid
			}
		default:
			return ErrMigrationRequestInvalid
		}
		previous = mapping.SourceRef
	}
	return nil
}

func validateMigrationAuthorityDecisions(decisions []migration.AuthorityDecision) error {
	var previous migration.OpaqueID
	for _, decision := range decisions {
		if !migrationValidOperationOpaqueID(decision.ProposalID) ||
			(previous != "" && previous >= decision.ProposalID) {
			return ErrMigrationRequestInvalid
		}
		switch decision.Decision {
		case migrationAuthorityDecisionDisabled, migrationAuthorityDecisionRejected:
			if decision.DestinationValue != "" {
				return ErrMigrationRequestInvalid
			}
		case migrationAuthorityDecisionApproved:
			if !boundedMigrationString(decision.DestinationValue, migrationAuthoritySummaryLimit) ||
				decision.DestinationValue != redactMigrationInspectionText(decision.DestinationValue) {
				return ErrMigrationRequestInvalid
			}
		default:
			return ErrMigrationRequestInvalid
		}
		previous = decision.ProposalID
	}
	return nil
}

func validateMigrationImportDraftReferences(
	draft migration.ImportDraft,
	manifest migration.Manifest,
) error {
	selected := make(map[migration.OpaqueID]struct{}, len(draft.SelectedEnvironmentRefs))
	for _, ref := range draft.SelectedEnvironmentRefs {
		selected[ref] = struct{}{}
	}
	workspaces := make(map[migration.OpaqueID]struct{})
	selectedAuthorities := make(map[migration.OpaqueID]struct{})
	for _, environmentSnapshot := range manifest.Environments {
		if _, exists := selected[environmentSnapshot.SourceEnvironmentRef]; !exists {
			continue
		}
		for _, proposal := range environmentSnapshot.WorkspaceProposals {
			if _, duplicate := workspaces[proposal.ProposalID]; duplicate ||
				!migrationValidOperationOpaqueID(proposal.ProposalID) {
				return ErrMigrationPlanInvalid
			}
			workspaces[proposal.ProposalID] = struct{}{}
		}
		for _, proposalID := range environmentSnapshot.AuthorityProposalRefs {
			if _, duplicate := selectedAuthorities[proposalID]; duplicate ||
				!migrationValidOperationOpaqueID(proposalID) {
				return ErrMigrationPlanInvalid
			}
			selectedAuthorities[proposalID] = struct{}{}
		}
	}
	secrets := make(map[migration.OpaqueID]struct{}, len(manifest.SecretEntries))
	for _, secret := range manifest.SecretEntries {
		if _, duplicate := secrets[secret.SecretRef]; duplicate ||
			!migrationValidOperationOpaqueID(secret.SecretRef) {
			return ErrMigrationPlanInvalid
		}
		secrets[secret.SecretRef] = struct{}{}
	}
	authorities := make(map[migration.OpaqueID]struct{}, len(manifest.AuthorityProposals))
	for _, proposal := range manifest.AuthorityProposals {
		if _, duplicate := authorities[proposal.ProposalID]; duplicate ||
			!migrationValidOperationOpaqueID(proposal.ProposalID) {
			return ErrMigrationPlanInvalid
		}
		authorities[proposal.ProposalID] = struct{}{}
	}
	for proposalID := range selectedAuthorities {
		if _, exists := authorities[proposalID]; !exists {
			return ErrMigrationPlanInvalid
		}
	}
	for _, mapping := range draft.WorkspaceMappings {
		if _, exists := workspaces[mapping.ProposalID]; !exists {
			return ErrMigrationPlanInvalid
		}
	}
	for _, mapping := range draft.SecretMappings {
		if _, exists := secrets[mapping.SourceRef]; !exists {
			return ErrMigrationPlanInvalid
		}
	}
	for _, decision := range draft.ConflictDecisions {
		if _, exists := selected[decision.SourceRef]; !exists {
			return ErrMigrationPlanInvalid
		}
	}
	for _, decision := range draft.AuthorityDecisions {
		if _, exists := selectedAuthorities[decision.ProposalID]; !exists {
			return ErrMigrationPlanInvalid
		}
	}
	return nil
}

func migrationSelectedWorkspaceProposalIDs(
	draft migration.ImportDraft,
	manifest migration.Manifest,
) []migration.OpaqueID {
	selected := make(map[migration.OpaqueID]struct{}, len(draft.SelectedEnvironmentRefs))
	for _, ref := range draft.SelectedEnvironmentRefs {
		selected[ref] = struct{}{}
	}
	result := make([]migration.OpaqueID, 0)
	for _, environmentSnapshot := range manifest.Environments {
		if _, exists := selected[environmentSnapshot.SourceEnvironmentRef]; !exists {
			continue
		}
		for _, proposal := range environmentSnapshot.WorkspaceProposals {
			result = append(result, proposal.ProposalID)
		}
	}
	slices.Sort(result)
	return result
}

func migrationSelectedAuthorityProposalIDs(
	draft migration.ImportDraft,
	manifest migration.Manifest,
) []migration.OpaqueID {
	selected := make(map[migration.OpaqueID]struct{}, len(draft.SelectedEnvironmentRefs))
	for _, ref := range draft.SelectedEnvironmentRefs {
		selected[ref] = struct{}{}
	}
	result := make([]migration.OpaqueID, 0)
	for _, environmentSnapshot := range manifest.Environments {
		if _, exists := selected[environmentSnapshot.SourceEnvironmentRef]; !exists {
			continue
		}
		result = append(result, environmentSnapshot.AuthorityProposalRefs...)
	}
	slices.Sort(result)
	return result
}

func validateMigrationBundleInspection(
	inspection MigrationBundleInspection,
	path string,
	binding migration.BundleBinding,
) error {
	manifest := inspection.Manifest
	if inspection.Binding != binding || inspection.BundleFile.Validate() != nil ||
		manifest.BundleID != binding.BundleID || manifest.FormatVersion != binding.FormatVersion ||
		manifest.Validate(migration.DefaultLimits()) != nil ||
		!validMigrationAbsolutePath(path) {
		return ErrMigrationPlanInvalid
	}
	return nil
}

func migrationImportSelectionFromDraft(
	draft migration.ImportDraft,
	manifest migration.Manifest,
) ([]migration.ImportObject, []migration.IdentityAction, []migration.DiskObject, []migration.DiskEdge, error) {
	environments := make(map[migration.OpaqueID]migration.EnvironmentSnapshot, len(manifest.Environments))
	for _, item := range manifest.Environments {
		environments[item.SourceEnvironmentRef] = item
	}
	diskByID := make(map[migration.OpaqueID]migration.DiskObject, len(manifest.DiskObjects))
	for _, disk := range manifest.DiskObjects {
		diskByID[disk.DiskID] = disk
	}
	selected := make(map[migration.OpaqueID]struct{}, len(draft.SelectedEnvironmentRefs))
	selectedDisks := make(map[migration.OpaqueID]migration.DiskObject)
	objects := make([]migration.ImportObject, len(draft.SelectedEnvironmentRefs))
	actions := make([]migration.IdentityAction, len(draft.SelectedEnvironmentRefs))
	var selectedMode migration.ExportMode
	for index, ref := range draft.SelectedEnvironmentRefs {
		environmentSnapshot, exists := environments[ref]
		if !exists || (environmentSnapshot.Mode != migration.ExportModeFull &&
			environmentSnapshot.Mode != migration.ExportModeConfig) ||
			(environmentSnapshot.Mode == migration.ExportModeFull &&
				environmentSnapshot.Backend != manifest.SourceProduct.Backend) ||
			(selectedMode != "" && selectedMode != environmentSnapshot.Mode) {
			return nil, nil, nil, nil, ErrMigrationPlanInvalid
		}
		selectedMode = environmentSnapshot.Mode
		diskRefs := append([]migration.OpaqueID(nil), environmentSnapshot.DiskRefs...)
		slices.Sort(diskRefs)
		if validateSortedMigrationOpaqueIDs(
			diskRefs, environmentSnapshot.Mode == migration.ExportModeConfig,
		) != nil || (environmentSnapshot.Mode == migration.ExportModeConfig &&
			draft.IdentityPolicies[index].Policy != migration.GuestIdentitySafeClone) {
			return nil, nil, nil, nil, ErrMigrationPlanInvalid
		}
		for _, diskRef := range diskRefs {
			disk, exists := diskByID[diskRef]
			if !exists {
				return nil, nil, nil, nil, ErrMigrationPlanInvalid
			}
			selectedDisks[diskRef] = disk
		}
		objects[index] = migration.ImportObject{
			SourceRef: ref, DestinationName: draft.NameMappings[index].DestinationName,
			Mode: environmentSnapshot.Mode, DiskRefs: diskRefs,
		}
		actions[index] = migration.IdentityAction{
			SourceRef: ref, GuestPolicy: draft.IdentityPolicies[index].Policy,
			FreshControlIdentity: true, FreshBackendIdentity: true,
		}
		selected[ref] = struct{}{}
	}
	disks := make([]migration.DiskObject, 0, len(selectedDisks))
	for _, disk := range selectedDisks {
		disks = append(disks, disk)
	}
	sort.Slice(disks, func(left, right int) bool { return disks[left].DiskID < disks[right].DiskID })
	edges := make([]migration.DiskEdge, 0)
	for _, edge := range manifest.DiskEdges {
		_, selectedEnvironment := selected[edge.EnvironmentRef]
		_, selectedDisk := selectedDisks[edge.DiskID]
		if selectedDisk && !selectedEnvironment {
			return nil, nil, nil, nil, ErrMigrationPlanInvalid
		}
		if selectedEnvironment && selectedDisk {
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(left, right int) bool {
		leftKey := string(edges[left].EnvironmentRef) + "\x00" + string(edges[left].DiskID)
		rightKey := string(edges[right].EnvironmentRef) + "\x00" + string(edges[right].DiskID)
		return leftKey < rightKey
	})
	return objects, actions, disks, edges, nil
}

func migrationImportObjectsConfigOnly(objects []migration.ImportObject) bool {
	if len(objects) == 0 {
		return false
	}
	for _, object := range objects {
		if object.Mode != migration.ExportModeConfig || len(object.DiskRefs) != 0 {
			return false
		}
	}
	return true
}

func migrationImportSelectionFromPlan(
	plan migration.ImportPlan,
	manifest migration.Manifest,
) ([]migration.DiskObject, []migration.DiskEdge, error) {
	draft := migration.ImportDraft{
		Schema: MigrationImportDraftSchema, BundlePath: plan.BundlePath,
		BundleBinding:           plan.BundleBinding,
		SelectedEnvironmentRefs: migrationImportObjectRefs(plan.Objects),
		NameMappings:            make([]migration.NameMapping, len(plan.Objects)),
		ConflictDecisions:       []migration.ConflictDecision{},
		IdentityPolicies:        make([]migration.IdentitySelection, len(plan.IdentityActions)),
		WorkspaceMappings:       []migration.WorkspaceMapping{}, SecretMappings: []migration.SecretMapping{},
		AuthorityDecisions:   []migration.AuthorityDecision{},
		RiskAcknowledgements: append([]string(nil), plan.RiskAcknowledgements...),
	}
	for index, object := range plan.Objects {
		draft.NameMappings[index] = migration.NameMapping{
			SourceRef: object.SourceRef, DestinationName: object.DestinationName,
		}
		draft.IdentityPolicies[index] = migration.IdentitySelection{
			SourceRef: object.SourceRef, Policy: plan.IdentityActions[index].GuestPolicy,
		}
	}
	for _, action := range plan.ConflictActions {
		if action.Decision != migrationConflictDecisionReplace {
			continue
		}
		draft.ConflictDecisions = append(draft.ConflictDecisions, migration.ConflictDecision{
			SourceRef: action.SourceRef, Decision: action.Decision,
			LifecycleOperationID: action.LifecycleOperationID,
			LifecyclePlanDigest:  action.LifecyclePlanDigest,
		})
	}
	sort.Slice(draft.ConflictDecisions, func(left, right int) bool {
		return draft.ConflictDecisions[left].SourceRef < draft.ConflictDecisions[right].SourceRef
	})
	_, _, disks, edges, err := migrationImportSelectionFromDraft(draft, manifest)
	if err != nil {
		return nil, nil, err
	}
	for index := range plan.Objects {
		if !slices.Equal(plan.Objects[index].DiskRefs, draftDiskRefs(manifest, plan.Objects[index].SourceRef)) {
			return nil, nil, ErrMigrationPlanStale
		}
	}
	return disks, edges, nil
}

func draftDiskRefs(manifest migration.Manifest, ref migration.OpaqueID) []migration.OpaqueID {
	for _, item := range manifest.Environments {
		if item.SourceEnvironmentRef == ref {
			refs := append([]migration.OpaqueID(nil), item.DiskRefs...)
			slices.Sort(refs)
			return refs
		}
	}
	return nil
}

func (service MigrationService) destinationEnvironmentBaseRevision() (migration.BaseRevision, error) {
	records, err := service.Environments.List()
	if err != nil {
		return migration.BaseRevision{}, err
	}
	sort.Slice(records, func(left, right int) bool { return records[left].ID < records[right].ID })
	digest, err := CanonicalDigest("migration-destination-environments", records)
	if err != nil {
		return migration.BaseRevision{}, err
	}
	return migration.BaseRevision{
		Resource: "environment-names", Revision: uint64(len(records)) + 1,
		Digest: migration.Digest(digest),
	}, nil
}

func migrationDestinationBlockers(
	destination backend.DestinationInventory,
	binding backend.MigrationEffectBinding,
	capability backend.MigrationCapabilities,
	requiredBytes uint64,
) []migration.PlanNotice {
	blockers := make([]migration.PlanNotice, 0, len(destination.Blockers)+1)
	valid := destination.Binding == binding &&
		destination.CapabilityRevision == capability.Revision &&
		destination.AvailableBytes >= requiredBytes && len(destination.Conflicts) == 0 &&
		destination.Compatible && len(destination.Blockers) == 0
	if !valid && len(destination.Blockers) == 0 {
		blockers = append(blockers, migration.PlanNotice{
			Code:        "migration.provider.compatibility_unproved",
			Summary:     "The destination provider could not prove this full-state import compatible.",
			Remediation: "Resolve the provider, architecture, layout, or free-space blocker and plan again.",
		})
	}
	for _, blocker := range destination.Blockers {
		blockers = append(blockers, migration.PlanNotice{
			Code: blocker.Code, Summary: blocker.Summary, Remediation: blocker.Remediation,
		})
	}
	return blockers
}

func sortMigrationPlanNotices(notices []migration.PlanNotice) {
	sort.Slice(notices, func(left, right int) bool {
		leftKey := notices[left].Code + "\x00" + string(notices[left].SourceRef)
		rightKey := notices[right].Code + "\x00" + string(notices[right].SourceRef)
		return leftKey < rightKey
	})
}

func (service MigrationService) newImportPlannedEffects(
	provider string,
	withSecretValues bool,
) ([]migration.PlannedEffect, error) {
	kinds := []struct {
		kind, effectProvider, compensation string
	}{
		{"stage-destination", provider, "rollback-stage"},
	}
	if withSecretValues {
		kinds = append(kinds, struct {
			kind, effectProvider, compensation string
		}{"prepare-secret", "manager", "delete-provisional-secret"})
	}
	kinds = append(kinds,
		struct{ kind, effectProvider, compensation string }{
			"adopt-destination", provider, "rollback-stage",
		},
		struct{ kind, effectProvider, compensation string }{
			"verify-destination", provider, "rollback-stage",
		},
		struct{ kind, effectProvider, compensation string }{
			"commit-visibility", "manager", "none",
		},
	)
	effects := make([]migration.PlannedEffect, len(kinds))
	for index, item := range kinds {
		id, err := service.newMigrationID("migeffect")
		if err != nil {
			return nil, err
		}
		effects[index] = migration.PlannedEffect{
			ID: id, Kind: item.kind, Provider: item.effectProvider,
			Compensation: item.compensation,
		}
	}
	return effects, nil
}

func migrationImportOperationEffects(planned []migration.PlannedEffect) ([]MigrationEffect, error) {
	withSecrets := len(planned) == 5
	if (len(planned) != 4 && !withSecrets) || planned[0].Kind != "stage-destination" {
		return nil, ErrMigrationPlanInvalid
	}
	index := 1
	effects := []MigrationEffect{{
		ID: planned[0].ID, Kind: MigrationEffectStage, Provider: planned[0].Provider,
		Status: MigrationEffectPending, Compensation: MigrationCompensationRollbackStage,
	}}
	if withSecrets {
		if planned[index].Kind != "prepare-secret" {
			return nil, ErrMigrationPlanInvalid
		}
		effects = append(effects, MigrationEffect{
			ID: planned[index].ID, Kind: MigrationEffectPrepareSecret,
			Provider: planned[index].Provider, Status: MigrationEffectPending,
			Compensation: MigrationCompensationDeleteSecret,
		})
		index++
	}
	if planned[index].Kind != "adopt-destination" ||
		planned[index+1].Kind != "verify-destination" ||
		planned[index+2].Kind != "commit-visibility" {
		return nil, ErrMigrationPlanInvalid
	}
	effects = append(effects,
		MigrationEffect{ID: planned[index].ID, Kind: MigrationEffectAdopt, Provider: planned[index].Provider, Status: MigrationEffectPending, Compensation: MigrationCompensationRollbackAdoption},
		MigrationEffect{ID: planned[index+1].ID, Kind: MigrationEffectVerify, Provider: planned[index+1].Provider, Status: MigrationEffectPending, Compensation: MigrationCompensationRollbackStage},
		MigrationEffect{ID: planned[index+2].ID, Kind: MigrationEffectActivate, Provider: planned[index+2].Provider, Status: MigrationEffectPending, Compensation: MigrationCompensationDeactivate},
	)
	return effects, nil
}

func migrationHasImportedSecretValues(actions []migration.SecretAction) bool {
	for _, action := range actions {
		if action.Decision == migrationSecretDecisionImportValue {
			return true
		}
	}
	return false
}

func migrationDestinationIdentity(
	operationID string,
	sourceRef migration.OpaqueID,
) MigrationDestinationIdentity {
	digest := func(domain string) [sha256.Size]byte {
		return sha256.Sum256([]byte(domain + "\x00" + operationID + "\x00" + string(sourceRef)))
	}
	control := digest("hideout-migration-control-identity/v1")
	backendID := digest("hideout-migration-backend-identity/v1")
	return MigrationDestinationIdentity{
		SourceRef:       sourceRef,
		ControlIdentity: migration.OpaqueID("env_" + hex.EncodeToString(control[:20])),
		BackendIdentity: migration.OpaqueID("backend_" + hex.EncodeToString(backendID[:20])),
	}
}

func migrationImportObjectRefs(objects []migration.ImportObject) []migration.OpaqueID {
	refs := make([]migration.OpaqueID, len(objects))
	for index, object := range objects {
		refs[index] = object.SourceRef
	}
	return refs
}

func migrationDiskLogicalBytes(disks []migration.DiskObject) (uint64, error) {
	total := uint64(0)
	for _, disk := range disks {
		if disk.LogicalBytes == 0 || disk.LogicalBytes > migration.HardMaxLogicalBytes-total {
			return 0, ErrMigrationPlanInvalid
		}
		total += disk.LogicalBytes
	}
	return total, nil
}

func migrationProfileStateLogicalBytes(actions []migration.EnvironmentAction) (uint64, error) {
	var total uint64
	for _, action := range actions {
		if action.ProfileStateLogicalBytes == 0 ||
			action.ProfileStateLogicalBytes > migration.HardMaxLogicalBytes-total {
			return 0, ErrMigrationPlanInvalid
		}
		total += action.ProfileStateLogicalBytes
	}
	return total, nil
}
