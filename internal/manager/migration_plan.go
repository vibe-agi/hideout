package manager

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	MigrationExportRequestSchema = "hideout.migration-export-request/v1"
	MigrationExportPlanSchema    = "hideout.migration-export-plan/v1"
	MigrationImportDraftSchema   = "hideout.migration-import-draft/v1"
	MigrationImportPlanSchema    = "hideout.migration-import-plan/v1"
	MigrationExportApplySchema   = "hideout.migration-export-apply/v1"
	MigrationImportApplySchema   = "hideout.migration-import-apply/v1"

	migrationExportPlanDomain = "migration-export-plan"
	migrationImportPlanDomain = "migration-import-plan"

	MigrationRiskExactGuestRestore  = "migration.identity.exact_guest_restore_collision"
	MigrationRiskOpaqueGuestContent = "migration.content.opaque_guest_disk_sensitive"
	MigrationRiskSelectedSecrets    = "migration.secret.selected_value_transfer"
)

var (
	ErrMigrationRequestInvalid        = errors.New("migration request is invalid")
	ErrMigrationPlanInvalid           = errors.New("migration plan is invalid")
	ErrMigrationPlanStale             = errors.New("migration plan is stale")
	ErrMigrationConfirmationRequired  = errors.New("migration plan confirmation is required")
	ErrMigrationCapabilityUnavailable = errors.New("migration capability is unavailable")
)

func normalizeMigrationExportRequest(
	request migration.ExportRequest,
) (migration.ExportRequest, error) {
	if request.Schema != MigrationExportRequestSchema ||
		(request.Mode != migration.ExportModeConfig && request.Mode != migration.ExportModeFull) ||
		len(request.EnvironmentNames) == 0 ||
		len(request.EnvironmentNames) > int(migration.HardMaxEnvironments) ||
		len(request.IncludeSecretRefs) > 256 || !validMigrationAbsolutePath(request.OutputPath) {
		return migration.ExportRequest{}, ErrMigrationRequestInvalid
	}
	normalized := request
	normalized.EnvironmentNames = append([]string(nil), request.EnvironmentNames...)
	normalized.IncludeSecretRefs = append([]string(nil), request.IncludeSecretRefs...)
	normalized.RiskAcknowledgements = append(
		[]string(nil), request.RiskAcknowledgements...,
	)
	seenNames := make(map[string]struct{}, len(normalized.EnvironmentNames))
	for _, name := range normalized.EnvironmentNames {
		if !migrationDestinationNamePattern.MatchString(name) {
			return migration.ExportRequest{}, ErrMigrationRequestInvalid
		}
		key := strings.ToLower(name)
		if _, exists := seenNames[key]; exists {
			return migration.ExportRequest{}, ErrMigrationRequestInvalid
		}
		seenNames[key] = struct{}{}
	}
	for _, ref := range normalized.IncludeSecretRefs {
		if !migrationDestinationNamePattern.MatchString(ref) {
			return migration.ExportRequest{}, ErrMigrationRequestInvalid
		}
	}
	slices.Sort(normalized.EnvironmentNames)
	slices.Sort(normalized.IncludeSecretRefs)
	slices.Sort(normalized.RiskAcknowledgements)
	for index := 1; index < len(normalized.IncludeSecretRefs); index++ {
		if normalized.IncludeSecretRefs[index-1] == normalized.IncludeSecretRefs[index] {
			return migration.ExportRequest{}, ErrMigrationRequestInvalid
		}
	}
	if validateMigrationRiskCodes(normalized.RiskAcknowledgements) != nil {
		return migration.ExportRequest{}, ErrMigrationRequestInvalid
	}
	hasOpaqueAcknowledgement := slices.Contains(
		normalized.RiskAcknowledgements, MigrationRiskOpaqueGuestContent,
	)
	hasSecretAcknowledgement := slices.Contains(
		normalized.RiskAcknowledgements, MigrationRiskSelectedSecrets,
	)
	if normalized.Mode == migration.ExportModeFull {
		if !hasOpaqueAcknowledgement ||
			(len(normalized.IncludeSecretRefs) != 0) != hasSecretAcknowledgement {
			return migration.ExportRequest{}, ErrMigrationRequestInvalid
		}
	} else if hasOpaqueAcknowledgement ||
		(len(normalized.IncludeSecretRefs) != 0) != hasSecretAcknowledgement ||
		slices.Contains(normalized.RiskAcknowledgements, MigrationRiskExactGuestRestore) {
		return migration.ExportRequest{}, ErrMigrationRequestInvalid
	}
	return normalized, nil
}

// SealMigrationExportPlan computes the immutable Manager review digest.
func SealMigrationExportPlan(plan *migration.ExportPlan) error {
	if plan == nil {
		return ErrMigrationPlanInvalid
	}
	plan.PlanDigest = ""
	if err := validateMigrationExportPlan(*plan, false); err != nil {
		return err
	}
	digest, err := CanonicalDigest(migrationExportPlanDomain, *plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = migration.Digest(digest)
	return ValidateMigrationExportPlan(*plan)
}

func ValidateMigrationExportPlan(plan migration.ExportPlan) error {
	return validateMigrationExportPlan(plan, true)
}

func VerifyMigrationExportPlan(plan migration.ExportPlan) error {
	if err := ValidateMigrationExportPlan(plan); err != nil {
		return err
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	expected, err := CanonicalDigest(migrationExportPlanDomain, plan)
	if err != nil {
		return err
	}
	if provided != migration.Digest(expected) {
		return errors.Join(ErrMigrationPlanInvalid, errors.New("export plan digest mismatch"))
	}
	return nil
}

func validateMigrationExportPlan(plan migration.ExportPlan, requireDigest bool) error {
	if plan.Schema != MigrationExportPlanSchema || !validMigrationPlanID(plan.PlanID) ||
		(plan.Mode != migration.ExportModeConfig && plan.Mode != migration.ExportModeFull) ||
		!validMigrationAbsolutePath(plan.OutputPath) ||
		plan.ProviderCapabilityRevision.Validate() != nil ||
		plan.SourceInventoryDigest.Validate() != nil ||
		len(plan.EnvironmentRefs) == 0 ||
		len(plan.EnvironmentRefs) > int(migration.HardMaxEnvironments) ||
		len(plan.DiskRefs) > 256 || len(plan.SelectedSecretRefs) > 256 ||
		len(plan.IncludedClasses) == 0 || len(plan.IncludedClasses) > 32 ||
		len(plan.ExcludedClasses) == 0 || len(plan.ExcludedClasses) > 32 ||
		len(plan.EnvironmentEstimates) != len(plan.EnvironmentRefs) ||
		len(plan.DiskEstimates) != len(plan.DiskRefs) ||
		plan.EstimatedPayloadLogicalBytes == 0 ||
		plan.EstimatedPayloadLogicalBytes > migration.HardMaxLogicalBytes ||
		len(plan.Warnings) > 256 || len(plan.Effects) == 0 || len(plan.Effects) > 256 ||
		!boundedMigrationDisplayText(plan.ConfirmationText, 4096) ||
		plan.ConfirmationText != redactMigrationText(plan.ConfirmationText) {
		return ErrMigrationPlanInvalid
	}
	if requireDigest {
		if plan.PlanDigest.Validate() != nil {
			return ErrMigrationPlanInvalid
		}
	} else if plan.PlanDigest != "" {
		return ErrMigrationPlanInvalid
	}
	if plan.Mode == migration.ExportModeFull && len(plan.DiskRefs) == 0 {
		return ErrMigrationPlanInvalid
	}
	if err := validateMigrationBaseRevisions(plan.BaseRevisions); err != nil ||
		validateSortedMigrationOpaqueIDs(plan.EnvironmentRefs, false) != nil ||
		validateSortedMigrationOpaqueIDs(plan.DiskRefs, true) != nil ||
		validateSortedMigrationNames(plan.SelectedSecretRefs, true) != nil ||
		validateSortedMigrationTokens(plan.IncludedClasses, false) != nil ||
		validateSortedMigrationTokens(plan.ExcludedClasses, false) != nil ||
		validateMigrationExportPlanInventory(plan) != nil ||
		validateMigrationPlannedEffects(plan.Effects) != nil ||
		validateMigrationPlanNotices(plan.Warnings) != nil ||
		validateMigrationRiskCodes(plan.RiskAcknowledgements) != nil {
		return ErrMigrationPlanInvalid
	}
	hasOpaqueAcknowledgement := slices.Contains(
		plan.RiskAcknowledgements, MigrationRiskOpaqueGuestContent,
	)
	hasSecretAcknowledgement := slices.Contains(
		plan.RiskAcknowledgements, MigrationRiskSelectedSecrets,
	)
	if hasOpaqueAcknowledgement != (plan.Mode == migration.ExportModeFull) ||
		hasSecretAcknowledgement != (len(plan.SelectedSecretRefs) != 0) ||
		slices.Contains(plan.RiskAcknowledgements, MigrationRiskExactGuestRestore) {
		return ErrMigrationPlanInvalid
	}
	return nil
}

func SealMigrationImportPlan(plan *migration.ImportPlan) error {
	if plan == nil {
		return ErrMigrationPlanInvalid
	}
	plan.PlanDigest = ""
	if err := validateMigrationImportPlan(*plan, false); err != nil {
		return err
	}
	digest, err := CanonicalDigest(migrationImportPlanDomain, *plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = migration.Digest(digest)
	return ValidateMigrationImportPlan(*plan)
}

func ValidateMigrationImportPlan(plan migration.ImportPlan) error {
	return validateMigrationImportPlan(plan, true)
}

func VerifyMigrationImportPlan(plan migration.ImportPlan) error {
	if err := ValidateMigrationImportPlan(plan); err != nil {
		return err
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	expected, err := CanonicalDigest(migrationImportPlanDomain, plan)
	if err != nil {
		return err
	}
	if provided != migration.Digest(expected) {
		return errors.Join(ErrMigrationPlanInvalid, errors.New("import plan digest mismatch"))
	}
	return nil
}

func validateMigrationImportPlan(plan migration.ImportPlan, requireDigest bool) error {
	if plan.Schema != MigrationImportPlanSchema || !validMigrationPlanID(plan.PlanID) ||
		!validMigrationAbsolutePath(plan.BundlePath) ||
		validateMigrationBundleBinding(plan.BundleBinding) != nil ||
		len(plan.Objects) == 0 || len(plan.Objects) > int(migration.HardMaxEnvironments) ||
		len(plan.ConflictActions) > int(migration.HardMaxEnvironments)*2 ||
		len(plan.EnvironmentActions) != len(plan.Objects) ||
		len(plan.IdentityActions) != len(plan.Objects) ||
		len(plan.WorkspaceActions) > 128 ||
		len(plan.SecretActions) > 256 ||
		len(plan.AuthorityActions) > 1024 || len(plan.DisabledProposals) > 1024 ||
		len(plan.Effects) == 0 || len(plan.Effects) > 256 || len(plan.Blockers) > 1024 {
		return ErrMigrationPlanInvalid
	}
	if requireDigest {
		if plan.PlanDigest.Validate() != nil {
			return ErrMigrationPlanInvalid
		}
	} else if plan.PlanDigest != "" {
		return ErrMigrationPlanInvalid
	}
	if err := validateMigrationBaseRevisions(plan.BaseRevisions); err != nil ||
		validateMigrationCompatibility(plan.Compatibility) != nil ||
		validateMigrationImportObjects(plan.Objects, plan.IdentityActions) != nil ||
		validateMigrationConflictActions(plan.ConflictActions) != nil ||
		validateMigrationConflictActionClosure(plan.Objects, plan.ConflictActions) != nil ||
		validateMigrationEnvironmentActions(plan.Objects, plan.EnvironmentActions) != nil ||
		validateMigrationWorkspaceActions(plan.WorkspaceActions) != nil ||
		validateMigrationSecretActions(plan.SecretActions) != nil ||
		validateMigrationAuthorityActions(plan.AuthorityActions) != nil ||
		validateSortedMigrationOpaqueIDs(plan.DisabledProposals, true) != nil ||
		validateMigrationPlanAuthorityClosure(plan) != nil ||
		validateMigrationRiskCodes(plan.RiskAcknowledgements) != nil ||
		validateMigrationPlannedEffects(plan.Effects) != nil ||
		validateMigrationPlanNotices(plan.Blockers) != nil {
		return ErrMigrationPlanInvalid
	}
	hasExactRestore := false
	for _, action := range plan.IdentityActions {
		hasExactRestore = hasExactRestore || action.GuestPolicy == migration.GuestIdentityExactRestore
	}
	if hasExactRestore != slices.Contains(plan.RiskAcknowledgements, MigrationRiskExactGuestRestore) {
		return ErrMigrationPlanInvalid
	}
	if migrationHasImportedSecretValues(plan.SecretActions) != slices.Contains(
		plan.RiskAcknowledgements, MigrationRiskSelectedSecrets,
	) {
		return ErrMigrationPlanInvalid
	}
	return nil
}

func validateMigrationEnvironmentActions(
	objects []migration.ImportObject,
	actions []migration.EnvironmentAction,
) error {
	if len(objects) != len(actions) {
		return ErrMigrationPlanInvalid
	}
	for index, action := range actions {
		if action.SourceRef != objects[index].SourceRef ||
			action.DestinationProfileName != objects[index].DestinationName ||
			profile.ValidateName(action.DestinationProfileName) != nil ||
			(action.Runtime != "linux" && action.Runtime != "native") ||
			(objects[index].Mode == migration.ExportModeFull && action.Runtime != "linux") ||
			!migrationGuestUserPattern.MatchString(action.GuestUser) || action.GuestUser == "root" ||
			!migrationProviderNamePattern.MatchString(action.Backend) ||
			!migrationValidOperationOpaqueID(action.ProfileComponentID) ||
			action.ProfileContentDigest.Validate() != nil ||
			action.ProfileLogicalBytes == 0 ||
			action.ProfileLogicalBytes > migration.MaxPortableProfileBytes {
			return ErrMigrationPlanInvalid
		}
		if objects[index].Mode == migration.ExportModeFull {
			if !migrationValidOperationOpaqueID(action.ProfileStateComponentID) ||
				action.ProfileStateContentDigest.Validate() != nil ||
				action.ProfileStateLogicalBytes == 0 ||
				action.ProfileStateLogicalBytes > migration.HardMaxLogicalBytes {
				return ErrMigrationPlanInvalid
			}
		} else if action.ProfileStateComponentID != "" ||
			action.ProfileStateContentDigest != "" || action.ProfileStateLogicalBytes != 0 {
			return ErrMigrationPlanInvalid
		}
	}
	return nil
}

func validateMigrationWorkspaceActions(actions []migration.WorkspaceAction) error {
	var previous migration.OpaqueID
	mappedEnvironments := make(map[migration.OpaqueID]struct{})
	for _, action := range actions {
		if !migrationValidOperationOpaqueID(action.ProposalID) ||
			!migrationValidOperationOpaqueID(action.EnvironmentRef) ||
			(previous != "" && previous >= action.ProposalID) ||
			!validMigrationAbsolutePath(action.GuestPath) {
			return ErrMigrationPlanInvalid
		}
		switch action.Decision {
		case migrationWorkspaceDecisionDisabled:
			if action.DestinationPath != "" || action.RootDevice != 0 || action.RootInode != 0 {
				return ErrMigrationPlanInvalid
			}
		case migrationWorkspaceDecisionMapped:
			if !validMigrationAbsolutePath(action.DestinationPath) ||
				action.RootDevice == 0 || action.RootInode == 0 {
				return ErrMigrationPlanInvalid
			}
			if _, duplicate := mappedEnvironments[action.EnvironmentRef]; duplicate {
				return ErrMigrationPlanInvalid
			}
			mappedEnvironments[action.EnvironmentRef] = struct{}{}
		default:
			return ErrMigrationPlanInvalid
		}
		previous = action.ProposalID
	}
	return nil
}

func validateMigrationImportObjects(
	objects []migration.ImportObject,
	actions []migration.IdentityAction,
) error {
	var previous migration.OpaqueID
	names := make(map[string]struct{}, len(objects))
	for index, object := range objects {
		if !migrationValidOperationOpaqueID(object.SourceRef) ||
			(previous != "" && previous >= object.SourceRef) ||
			!migrationDestinationNamePattern.MatchString(object.DestinationName) ||
			environment.ValidateName(object.DestinationName) != nil ||
			(object.Mode != migration.ExportModeConfig && object.Mode != migration.ExportModeFull) ||
			validateSortedMigrationOpaqueIDs(object.DiskRefs, object.Mode == migration.ExportModeConfig) != nil {
			return ErrMigrationPlanInvalid
		}
		if object.Mode == migration.ExportModeFull && len(object.DiskRefs) == 0 {
			return ErrMigrationPlanInvalid
		}
		nameKey := strings.ToLower(object.DestinationName)
		if _, exists := names[nameKey]; exists {
			return ErrMigrationPlanInvalid
		}
		names[nameKey] = struct{}{}
		action := actions[index]
		if action.SourceRef != object.SourceRef ||
			(action.GuestPolicy != migration.GuestIdentitySafeClone &&
				action.GuestPolicy != migration.GuestIdentityExactRestore) ||
			!action.FreshControlIdentity || !action.FreshBackendIdentity {
			return ErrMigrationPlanInvalid
		}
		previous = object.SourceRef
	}
	return nil
}

func validateMigrationCompatibility(value migration.Compatibility) error {
	if !migrationProviderNamePattern.MatchString(value.Backend) ||
		value.CapabilityRevision.Validate() != nil ||
		value.RequiredBytes > migration.HardMaxLogicalBytes ||
		(value.Available && value.ReasonCode != "") ||
		(!value.Available && !migrationOperationCodePattern.MatchString(value.ReasonCode)) {
		return ErrMigrationPlanInvalid
	}
	if value.RequiredBytes == 0 {
		if !value.Capacity.IsZero() || value.AvailableBytes != 0 {
			return ErrMigrationPlanInvalid
		}
	} else if value.Capacity.Validate() != nil ||
		value.Capacity.PeakAdditionalBytes != value.RequiredBytes {
		return ErrMigrationPlanInvalid
	}
	return nil
}

func validateMigrationAuthorityActions(actions []migration.AuthorityAction) error {
	var previous migration.OpaqueID
	for _, action := range actions {
		if !migrationValidOperationOpaqueID(action.ProposalID) ||
			!migrationValidOperationOpaqueID(action.EnvironmentRef) ||
			(previous != "" && previous >= action.ProposalID) ||
			!validMigrationAuthorityClass(action.Class) || !action.Approved ||
			!boundedMigrationString(action.DestinationValue, migrationAuthoritySummaryLimit) ||
			action.DestinationValue != redactMigrationInspectionText(action.DestinationValue) {
			return ErrMigrationPlanInvalid
		}
		previous = action.ProposalID
	}
	return nil
}

func validateMigrationBundleBinding(binding migration.BundleBinding) error {
	if _, err := migration.ParseBundleID(string(binding.BundleID)); err != nil ||
		binding.FormatVersion != migration.BundleFormatVersion ||
		binding.FileDigest.Validate() != nil || binding.ManifestDigest.Validate() != nil ||
		binding.CompletionDigest.Validate() != nil {
		return ErrMigrationPlanInvalid
	}
	return nil
}

func validateMigrationPlannedEffects(effects []migration.PlannedEffect) error {
	seen := make(map[migration.OpaqueID]struct{}, len(effects))
	for _, effect := range effects {
		if !migrationValidOperationOpaqueID(effect.ID) ||
			!validMigrationPlannedEffectKind(effect.Kind) ||
			!migrationProviderNamePattern.MatchString(effect.Provider) ||
			!validMigrationPlannedCompensation(effect.Compensation) {
			return ErrMigrationPlanInvalid
		}
		if _, exists := seen[effect.ID]; exists {
			return ErrMigrationPlanInvalid
		}
		seen[effect.ID] = struct{}{}
	}
	return nil
}

func validMigrationPlannedEffectKind(value string) bool {
	return slices.Contains([]string{
		"claim-source", "snapshot-source", "write-bundle", "seal-bundle",
		"claim-destination", "stage-destination", "prepare-secret",
		"adopt-destination", "verify-destination", "commit-visibility",
		"release-claim",
	}, value)
}

func validMigrationPlannedCompensation(value string) bool {
	return slices.Contains([]string{
		"none", "release-claim", "release-snapshot", "remove-partial",
		"rollback-stage", "delete-provisional-secret",
	}, value)
}

func validateMigrationPlanNotices(notices []migration.PlanNotice) error {
	previous := ""
	for _, notice := range notices {
		key := notice.Code + "\x00" + string(notice.SourceRef)
		if !migrationOperationCodePattern.MatchString(notice.Code) || key <= previous ||
			!boundedMigrationDisplayText(notice.Summary, 2048) ||
			notice.Summary != redactMigrationText(notice.Summary) ||
			(notice.Remediation != "" &&
				(!boundedMigrationDisplayText(notice.Remediation, 2048) ||
					notice.Remediation != redactMigrationText(notice.Remediation))) {
			return ErrMigrationPlanInvalid
		}
		if notice.SourceRef != "" && !migrationValidOperationOpaqueID(notice.SourceRef) {
			return ErrMigrationPlanInvalid
		}
		previous = key
	}
	return nil
}

func validateMigrationRiskCodes(codes []string) error {
	if len(codes) > 1024 || !slices.IsSorted(codes) {
		return ErrMigrationPlanInvalid
	}
	for index, code := range codes {
		if !migrationOperationCodePattern.MatchString(code) ||
			(index > 0 && codes[index-1] == code) ||
			(code != MigrationRiskExactGuestRestore &&
				code != MigrationRiskOpaqueGuestContent &&
				code != MigrationRiskSelectedSecrets) {
			return ErrMigrationPlanInvalid
		}
	}
	return nil
}

func validateSortedMigrationOpaqueIDs(values []migration.OpaqueID, allowEmpty bool) error {
	if !allowEmpty && len(values) == 0 {
		return ErrMigrationPlanInvalid
	}
	var previous migration.OpaqueID
	for _, value := range values {
		if !migrationValidOperationOpaqueID(value) || (previous != "" && previous >= value) {
			return ErrMigrationPlanInvalid
		}
		previous = value
	}
	return nil
}

func validateSortedMigrationNames(values []string, allowEmpty bool) error {
	if !allowEmpty && len(values) == 0 {
		return ErrMigrationPlanInvalid
	}
	for index, value := range values {
		if !migrationDestinationNamePattern.MatchString(value) ||
			(index > 0 && values[index-1] >= value) {
			return ErrMigrationPlanInvalid
		}
	}
	return nil
}

func validateSortedMigrationTokens(values []string, allowEmpty bool) error {
	if !allowEmpty && len(values) == 0 {
		return ErrMigrationPlanInvalid
	}
	for index, value := range values {
		if !migrationProviderNamePattern.MatchString(value) ||
			(index > 0 && values[index-1] >= value) {
			return ErrMigrationPlanInvalid
		}
	}
	return nil
}

func validMigrationPlanID(value migration.OpaqueID) bool {
	return strings.HasPrefix(string(value), "migplan_") && migrationValidOperationOpaqueID(value)
}

func migrationPlanOutputParent(path string) string {
	return filepath.Dir(path)
}
