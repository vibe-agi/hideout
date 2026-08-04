package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const migrationEnvironmentBaseDomain = "migration-environment-base"

var (
	ErrMigrationOutputConflict = errors.New("migration output path is occupied")

	migrationIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
)

type MigrationIDSource func(prefix string) (migration.OpaqueID, error)

// MigrationService is the Manager-owned migration plan/apply boundary. Provider
// interfaces carry storage mechanics only; Manager retains names, revisions,
// crypto handles, claims, identities, and visibility authority.
type MigrationService struct {
	Store          MigrationStore
	Environments   environment.Store
	Profiles       profile.Store
	Export         backend.MigrationExportProvider
	Import         backend.MigrationImportProvider
	Config         backend.MigrationCapabilityProvider
	SecretInputs   *MigrationSecretInputStore
	Secrets        secrets.RuntimeStore
	Now            func() time.Time
	NewID          MigrationIDSource
	ProductVersion string
	HostOS         string
	HostArch       string
}

type MigrationPlanConfirmation struct {
	PlanDigest                   migration.Digest     `json:"planDigest"`
	AcceptedRiskAcknowledgements []string             `json:"acceptedRiskAcknowledgements"`
	ApprovedAuthorityProposalIDs []migration.OpaqueID `json:"approvedAuthorityProposalIds,omitempty"`
}

type MigrationExportApplyRequest struct {
	Schema            string                    `json:"schema"`
	Plan              migration.ExportPlan      `json:"plan"`
	Confirmation      MigrationPlanConfirmation `json:"confirmation"`
	SecretInputHandle string                    `json:"secretInputHandle"`
	ClientBinding     string                    `json:"-"`
	IdempotencyKey    string                    `json:"idempotencyKey"`
}

type MigrationApplyResult struct {
	OperationID string         `json:"operationId"`
	State       MigrationPhase `json:"state"`
	Created     bool           `json:"created"`
	Next        string         `json:"next"`
}

type migrationExportSource struct {
	records    []environment.Record
	selections []backend.MigrationSourceSelection
	revisions  []migration.BaseRevision
}

func (service MigrationService) PlanExport(
	ctx context.Context,
	request migration.ExportRequest,
) (migration.ExportPlan, error) {
	if ctx == nil {
		return migration.ExportPlan{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return migration.ExportPlan{}, err
	}
	normalized, err := normalizeMigrationExportRequest(request)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	if err := inspectMigrationOutputPath(normalized.OutputPath); err != nil {
		return migration.ExportPlan{}, err
	}
	if normalized.Mode == migration.ExportModeConfig {
		return service.planConfigExport(ctx, normalized)
	}
	if service.Export == nil {
		return migration.ExportPlan{}, ErrMigrationCapabilityUnavailable
	}
	capability, err := service.exportCapability(ctx)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	source, err := service.resolveExportNames(normalized.EnvironmentNames, capability.Provider)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	planID, err := service.newMigrationID("migplan")
	if err != nil {
		return migration.ExportPlan{}, err
	}
	effects, err := service.newExportPlannedEffects(capability.Provider)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	snapshotEffect := effects[0]
	binding := backend.MigrationEffectBinding{
		OperationID: planID, EffectID: snapshotEffect.ID,
		CapabilityRevision: capability.Revision,
	}
	inventory, err := service.Export.InspectMigrationSource(
		ctx, backend.SourceInspectionRequest{
			Binding: binding, Mode: migration.ExportModeFull,
			Selections: source.selections,
		},
	)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	if err := validateExportInventory(inventory, binding, capability.Provider, source); err != nil {
		return migration.ExportPlan{}, err
	}
	_, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, planID, source, normalized.IncludeSecretRefs,
	)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	source.revisions = append(source.revisions, secretRevisions...)
	review, err := service.buildMigrationExportPlanInventory(
		source, inventory, migration.ExportModeFull, normalized.IncludeSecretRefs,
	)
	if err != nil {
		return migration.ExportPlan{}, err
	}
	plan := migration.ExportPlan{
		Schema: MigrationExportPlanSchema, PlanID: planID,
		BaseRevisions:              append([]migration.BaseRevision(nil), source.revisions...),
		Mode:                       migration.ExportModeFull,
		EnvironmentRefs:            migrationEnvironmentRefs(source.records),
		DiskRefs:                   migrationInventoryDiskRefs(inventory),
		SelectedSecretRefs:         append([]string(nil), normalized.IncludeSecretRefs...),
		ExcludedClasses:            append([]string(nil), inventory.ExcludedClasses...),
		OutputPath:                 normalized.OutputPath,
		ProviderCapabilityRevision: capability.Revision,
		SourceInventoryDigest:      inventory.InventoryDigest,
		Warnings:                   []migration.PlanNotice{},
		Effects:                    effects,
		ConfirmationText: fmt.Sprintf(
			"Export %d stopped environment(s) and their complete persistent disk graph into one encrypted bundle.",
			len(source.records),
		),
		RiskAcknowledgements: append(
			[]string(nil), normalized.RiskAcknowledgements...,
		),
	}
	review.apply(&plan)
	if err := SealMigrationExportPlan(&plan); err != nil {
		return migration.ExportPlan{}, err
	}
	return plan, nil
}

func (service MigrationService) ApplyExport(
	ctx context.Context,
	request MigrationExportApplyRequest,
) (MigrationApplyResult, error) {
	if ctx == nil || request.Schema != MigrationExportApplySchema ||
		!validClientBinding(request.ClientBinding) ||
		!migrationIdempotencyKeyPattern.MatchString(request.IdempotencyKey) ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) {
		return MigrationApplyResult{}, ErrMigrationRequestInvalid
	}
	if err := VerifyMigrationExportPlan(request.Plan); err != nil {
		return MigrationApplyResult{}, err
	}
	if err := validateMigrationConfirmation(
		request.Plan.PlanDigest, request.Plan.RiskAcknowledgements, nil,
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
		return migrationApplyReplay(existing, MigrationOperationExport, request.Plan.PlanID, request.Plan.PlanDigest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return MigrationApplyResult{}, err
	}
	if service.SecretInputs == nil {
		return MigrationApplyResult{}, ErrMigrationSecretInputRequired
	}
	secret, err := service.SecretInputs.Lookup(MigrationSecretInputLookup{
		Handle: request.SecretInputHandle, Purpose: MigrationSecretPurposeExportCreate,
		ClientBinding: request.ClientBinding,
	})
	if err != nil {
		if existing, loadErr := service.Store.Load(operationID); loadErr == nil {
			return migrationApplyReplay(existing, MigrationOperationExport, request.Plan.PlanID, request.Plan.PlanDigest)
		}
		return MigrationApplyResult{}, err
	}
	snapshotEffect, err := plannedMigrationEffect(request.Plan.Effects, "snapshot-source")
	if err != nil {
		return MigrationApplyResult{}, err
	}
	binding := backend.MigrationEffectBinding{
		OperationID: migration.OpaqueID(operationID), EffectID: snapshotEffect.ID,
		CapabilityRevision: request.Plan.ProviderCapabilityRevision,
	}
	var source migrationExportSource
	var inventory backend.SourceInventory
	if request.Plan.Mode == migration.ExportModeConfig {
		source, err = service.revalidateConfigExportPlan(ctx, request.Plan)
	} else {
		source, inventory, err = service.revalidateExportPlan(ctx, request.Plan, binding)
	}
	if err != nil {
		return MigrationApplyResult{}, err
	}
	operation, err := service.buildExportOperation(
		request.Plan, secret.BundleID, operationID, source, inventory,
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

// SnapshotExportSource performs the first long-running export effect. It can be
// replayed after response loss: the Manager effect ledger and provider binding
// are stable, and the provider returns the same operation-owned snapshot.
func (service MigrationService) SnapshotExportSource(
	ctx context.Context,
	operationID string,
) (MigrationOperation, backend.SourceSnapshot, error) {
	if ctx == nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationRequestInvalid
	}
	operation, err := service.Store.Load(operationID)
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	if operation.Kind != MigrationOperationExport {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	if migrationOperationConfigExport(operation) {
		return service.snapshotConfigExportSource(ctx, operation)
	}
	if service.Export == nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationCapabilityUnavailable
	}
	if operation.Phase == MigrationPhaseClaiming {
		if _, _, err := service.Store.AcquireClaims(operation.ID); err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseSnapshotting, nil,
		)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
	} else if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseSnapshotting, nil,
		)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
	} else if operation.Phase != MigrationPhaseSnapshotting &&
		operation.Phase != MigrationPhaseWriting {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	effect, err := migrationOperationEffect(operation, MigrationEffectSnapshot)
	if err != nil {
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	binding := backend.MigrationEffectBinding{
		OperationID: migration.OpaqueID(operation.ID), EffectID: effect.ID,
		CapabilityRevision: operation.CapabilityRevision,
	}
	source, inventory, err := service.revalidateExportOperation(ctx, operation, binding)
	if err != nil {
		_, _ = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseRecoverableFailure, nil,
		)
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	if effect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(operation.ID, effect.ID, effect.Provider)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
		effect, err = migrationOperationEffect(operation, MigrationEffectSnapshot)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
	}
	if effect.Status != MigrationEffectRunning && effect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	snapshot, err := service.Export.SnapshotMigrationSource(
		ctx, backend.SourceSnapshotRequest{
			Binding: binding, InventoryDigest: inventory.InventoryDigest,
			Selections: source.selections, DiskRefs: migrationInventoryDiskRefs(inventory),
		},
	)
	if err != nil {
		_, _ = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseRecoverableFailure, nil,
		)
		return MigrationOperation{}, backend.SourceSnapshot{}, err
	}
	if err := snapshot.Validate(); err != nil || snapshot.Binding != binding || !snapshot.Independent {
		return MigrationOperation{}, backend.SourceSnapshot{}, ErrMigrationOperationInvalid
	}
	if effect.Status == MigrationEffectRunning {
		operation, err = service.Store.FinishEffect(
			operation.ID, effect.ID, effect.Provider, MigrationEffectSucceeded,
			[]MigrationEffectEvidence{{
				Code: "migration.export.snapshot_ready", OpaqueRef: snapshot.SnapshotHandle,
				Digest: inventory.InventoryDigest, Count: uint64(len(snapshot.Components)),
				ObservedAt: service.nowUTC(),
			}},
		)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
	}
	if operation.Phase == MigrationPhaseSnapshotting {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseWriting, nil,
		)
		if err != nil {
			return MigrationOperation{}, backend.SourceSnapshot{}, err
		}
	}
	return operation, snapshot, nil
}

func (service MigrationService) revalidateExportPlan(
	ctx context.Context,
	plan migration.ExportPlan,
	binding backend.MigrationEffectBinding,
) (migrationExportSource, backend.SourceInventory, error) {
	capability, err := service.exportCapability(ctx)
	if err != nil || capability.Revision != plan.ProviderCapabilityRevision {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	if err := inspectMigrationOutputPath(plan.OutputPath); err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, err
	}
	source, err := service.resolveExportRefs(plan.EnvironmentRefs, capability.Provider)
	if err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	inventory, err := service.Export.InspectMigrationSource(
		ctx, backend.SourceInspectionRequest{
			Binding: binding, Mode: migration.ExportModeFull, Selections: source.selections,
		},
	)
	if err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, err
	}
	_, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, plan.PlanID, source, plan.SelectedSecretRefs,
	)
	if err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, err
	}
	source.revisions = append(source.revisions, secretRevisions...)
	if err := validateExportInventory(inventory, binding, capability.Provider, source); err != nil ||
		!reflect.DeepEqual(source.revisions, plan.BaseRevisions) ||
		inventory.InventoryDigest != plan.SourceInventoryDigest ||
		!slices.Equal(migrationInventoryDiskRefs(inventory), plan.DiskRefs) ||
		!slices.Equal(inventory.ExcludedClasses, plan.ExcludedClasses) {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	review, err := service.buildMigrationExportPlanInventory(
		source, inventory, migration.ExportModeFull, plan.SelectedSecretRefs,
	)
	if err != nil || !review.matches(plan) {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	return source, inventory, nil
}

func (service MigrationService) revalidateExportOperation(
	ctx context.Context,
	operation MigrationOperation,
	binding backend.MigrationEffectBinding,
) (migrationExportSource, backend.SourceInventory, error) {
	capability, err := service.exportCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	refs := migrationOperationClaimRefs(operation.Claims, MigrationClaimSourceEnvironment)
	source, err := service.resolveExportRefs(refs, capability.Provider)
	if err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	inventory, err := service.Export.InspectMigrationSource(
		ctx, backend.SourceInspectionRequest{
			Binding: binding, Mode: migration.ExportModeFull, Selections: source.selections,
		},
	)
	if err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, err
	}
	_, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, operation.PlanID, source, operation.SelectedSecretRefs,
	)
	if err != nil {
		return migrationExportSource{}, backend.SourceInventory{}, err
	}
	source.revisions = append(source.revisions, secretRevisions...)
	if err := validateExportInventory(inventory, binding, capability.Provider, source); err != nil ||
		!reflect.DeepEqual(source.revisions, operation.BaseRevisions) ||
		inventory.InventoryDigest != operation.SourceInventoryDigest ||
		!slices.Equal(
			migrationInventoryDiskRefs(inventory),
			migrationOperationClaimRefs(operation.Claims, MigrationClaimSourceDisk),
		) {
		return migrationExportSource{}, backend.SourceInventory{}, ErrMigrationPlanStale
	}
	return source, inventory, nil
}

func (service MigrationService) buildExportOperation(
	plan migration.ExportPlan,
	bundleID migration.BundleID,
	operationID string,
	source migrationExportSource,
	inventory backend.SourceInventory,
) (MigrationOperation, error) {
	if _, err := migration.ParseBundleID(string(bundleID)); err != nil {
		return MigrationOperation{}, err
	}
	claims := make([]MigrationClaim, 0, len(source.records)+len(inventory.Disks)+1)
	for _, record := range source.records {
		claim, err := NewMigrationClaim(MigrationClaimSourceEnvironment, record.ID)
		if err != nil {
			return MigrationOperation{}, err
		}
		claims = append(claims, claim)
	}
	for _, disk := range inventory.Disks {
		claim, err := NewMigrationClaim(MigrationClaimSourceDisk, string(disk.DiskRef))
		if err != nil {
			return MigrationOperation{}, err
		}
		claims = append(claims, claim)
	}
	outputClaim, err := NewMigrationClaim(MigrationClaimOutputPath, plan.OutputPath)
	if err != nil {
		return MigrationOperation{}, err
	}
	claims = append(claims, outputClaim)
	SortMigrationClaims(claims)
	effects, err := migrationExportOperationEffects(plan.Effects)
	if err != nil {
		return MigrationOperation{}, err
	}
	warnings := make([]MigrationNotice, len(plan.Warnings))
	for index, warning := range plan.Warnings {
		warnings[index], err = NewMigrationNotice(warning.Code, warning.Summary)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	logicalTotal := uint64(0)
	for _, disk := range inventory.Disks {
		if disk.LogicalBytes > migration.HardMaxLogicalBytes-logicalTotal {
			return MigrationOperation{}, ErrMigrationOperationInvalid
		}
		logicalTotal += disk.LogicalBytes
	}
	now := service.nowUTC()
	operation := MigrationOperation{
		Schema: MigrationOperationSchema, ID: operationID,
		Kind: MigrationOperationExport, PlanID: plan.PlanID, PlanDigest: plan.PlanDigest,
		Bundle: MigrationOperationBundleBinding{
			BundleID: bundleID, FormatVersion: migration.BundleFormatVersion,
		},
		SourceInventoryDigest: plan.SourceInventoryDigest,
		SelectedSecretRefs:    append([]string(nil), plan.SelectedSecretRefs...),
		BaseRevisions:         append([]migration.BaseRevision(nil), plan.BaseRevisions...),
		CapabilityRevision:    plan.ProviderCapabilityRevision,
		Phase:                 MigrationPhaseClaiming, Revision: 1,
		Claims: claims, Effects: effects, Warnings: warnings,
		Progress: MigrationProgress{
			LogicalTotalKnown: true, TotalLogicalBytes: logicalTotal,
			ComponentsTotal: uint32(
				len(inventory.Disks) + len(source.records) + len(plan.SelectedSecretRefs),
			),
			PhaseStartedAt: now,
		},
		Recovery: MigrationRecovery{
			Code: migrationRecoveryCodeNone, Action: MigrationRecoveryNone,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := operation.Validate(); err != nil {
		return MigrationOperation{}, err
	}
	return operation, nil
}

func (service MigrationService) exportCapability(
	ctx context.Context,
) (backend.MigrationCapabilities, error) {
	if service.Export == nil {
		return backend.MigrationCapabilities{}, ErrMigrationCapabilityUnavailable
	}
	capability, err := service.Export.MigrationCapabilities(ctx)
	if err != nil {
		return backend.MigrationCapabilities{}, err
	}
	if capability.Validate() != nil || !capability.FullExport {
		return backend.MigrationCapabilities{}, ErrMigrationCapabilityUnavailable
	}
	return capability, nil
}

func (service MigrationService) resolveExportNames(
	names []string,
	provider string,
) (migrationExportSource, error) {
	records := make([]environment.Record, 0, len(names))
	for _, name := range names {
		record, err := service.Environments.LoadByName(name)
		if err != nil {
			return migrationExportSource{}, err
		}
		records = append(records, record)
	}
	return service.exportSource(records, provider)
}

func (service MigrationService) resolveExportRefs(
	refs []migration.OpaqueID,
	provider string,
) (migrationExportSource, error) {
	records := make([]environment.Record, 0, len(refs))
	for _, ref := range refs {
		record, err := service.Environments.Load(string(ref))
		if err != nil {
			return migrationExportSource{}, err
		}
		records = append(records, record)
	}
	return service.exportSource(records, provider)
}

func (service MigrationService) exportSource(
	records []environment.Record,
	provider string,
) (migrationExportSource, error) {
	if len(records) == 0 || len(records) > int(migration.HardMaxEnvironments) {
		return migrationExportSource{}, ErrMigrationRequestInvalid
	}
	sort.Slice(records, func(left, right int) bool { return records[left].ID < records[right].ID })
	source := migrationExportSource{
		records:    append([]environment.Record(nil), records...),
		selections: make([]backend.MigrationSourceSelection, 0, len(records)),
		revisions:  make([]migration.BaseRevision, 0, len(records)),
	}
	previous := ""
	for _, record := range records {
		if record.Validate() != nil || record.Status != environment.StatusStopped ||
			record.Backend != provider || strings.TrimSpace(record.InstanceName) == "" ||
			record.ID <= previous {
			return migrationExportSource{}, ErrMigrationPlanStale
		}
		ref, err := migration.ParseOpaqueID(record.ID)
		if err != nil {
			return migrationExportSource{}, err
		}
		digest, err := CanonicalDigest(migrationEnvironmentBaseDomain, record)
		if err != nil {
			return migrationExportSource{}, err
		}
		revision := uint64(record.CreatedAt.UnixNano())
		if record.CreatedAt.UnixNano() <= 0 {
			revision = 1
		}
		source.selections = append(source.selections, backend.MigrationSourceSelection{
			EnvironmentRef: ref, ProviderInstance: record.InstanceName,
		})
		source.revisions = append(source.revisions, migration.BaseRevision{
			Resource: "environment:" + record.ID, Revision: revision,
			Digest: migration.Digest(digest),
		})
		previous = record.ID
	}
	return source, nil
}

func validateExportInventory(
	inventory backend.SourceInventory,
	binding backend.MigrationEffectBinding,
	provider string,
	source migrationExportSource,
) error {
	if err := inventory.Validate(); err != nil || inventory.Binding != binding ||
		inventory.Provider != provider || !inventory.Capturable || !inventory.SelectionClosed ||
		len(inventory.Instances) != len(source.records) {
		return ErrMigrationPlanStale
	}
	for index, instance := range inventory.Instances {
		if instance.EnvironmentRef != migration.OpaqueID(source.records[index].ID) ||
			instance.Lifecycle != backend.MigrationLifecycleStopped {
			return ErrMigrationPlanStale
		}
	}
	return nil
}

func inspectMigrationOutputPath(path string) error {
	if !validMigrationAbsolutePath(path) {
		return ErrMigrationRequestInvalid
	}
	if _, err := os.Lstat(path); err == nil {
		return ErrMigrationOutputConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := migrationPlanOutputParent(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrMigrationRequestInvalid
	}
	return nil
}

func (service MigrationService) newExportPlannedEffects(
	provider string,
) ([]migration.PlannedEffect, error) {
	snapshotID, err := service.newMigrationID("migeffect")
	if err != nil {
		return nil, err
	}
	writeID, err := service.newMigrationID("migeffect")
	if err != nil {
		return nil, err
	}
	sealID, err := service.newMigrationID("migeffect")
	if err != nil {
		return nil, err
	}
	return []migration.PlannedEffect{
		{ID: snapshotID, Kind: "snapshot-source", Provider: provider, Compensation: "release-snapshot"},
		{ID: writeID, Kind: "write-bundle", Provider: "manager", Compensation: "remove-partial"},
		{ID: sealID, Kind: "seal-bundle", Provider: "manager", Compensation: "none"},
	}, nil
}

func (service MigrationService) newMigrationID(prefix string) (migration.OpaqueID, error) {
	if service.NewID != nil {
		value, err := service.NewID(prefix)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(string(value), prefix+"_") || !migrationValidOperationOpaqueID(value) {
			return "", ErrMigrationRequestInvalid
		}
		return value, nil
	}
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return migration.OpaqueID(prefix + "_" + hex.EncodeToString(raw[:])), nil
}

func migrationOperationID(
	destinationNamespace migration.OpaqueID,
	clientBinding, idempotencyKey string,
) string {
	digest := sha256.Sum256([]byte(
		"hideout-migration-operation/v2\x00" + string(destinationNamespace) + "\x00" +
			clientBinding + "\x00" + idempotencyKey,
	))
	return "op_migration_" + hex.EncodeToString(digest[:24])
}

func validateMigrationConfirmation(
	planDigest migration.Digest,
	risks []string,
	approvedAuthorityProposalIDs []migration.OpaqueID,
	confirmation MigrationPlanConfirmation,
) error {
	if confirmation.PlanDigest != planDigest ||
		!slices.Equal(confirmation.AcceptedRiskAcknowledgements, risks) ||
		!slices.Equal(
			confirmation.ApprovedAuthorityProposalIDs,
			approvedAuthorityProposalIDs,
		) {
		return ErrMigrationConfirmationRequired
	}
	return nil
}

func migrationApplyReplay(
	operation MigrationOperation,
	kind MigrationOperationKind,
	planID migration.OpaqueID,
	planDigest migration.Digest,
) (MigrationApplyResult, error) {
	if operation.Kind != kind || operation.PlanID != planID || operation.PlanDigest != planDigest {
		return MigrationApplyResult{}, ErrMigrationOperationMismatch
	}
	return migrationApplyResult(operation, false), nil
}

func migrationApplyResult(operation MigrationOperation, created bool) MigrationApplyResult {
	return MigrationApplyResult{
		OperationID: operation.ID, State: operation.Phase, Created: created,
		Next: "hideout migrate status " + operation.ID,
	}
}

func migrationEnvironmentRefs(records []environment.Record) []migration.OpaqueID {
	refs := make([]migration.OpaqueID, len(records))
	for index, record := range records {
		refs[index] = migration.OpaqueID(record.ID)
	}
	return refs
}

func migrationInventoryDiskRefs(inventory backend.SourceInventory) []migration.OpaqueID {
	refs := make([]migration.OpaqueID, len(inventory.Disks))
	for index, disk := range inventory.Disks {
		refs[index] = disk.DiskRef
	}
	return refs
}

func plannedMigrationEffect(
	effects []migration.PlannedEffect,
	kind string,
) (migration.PlannedEffect, error) {
	for _, effect := range effects {
		if effect.Kind == kind {
			return effect, nil
		}
	}
	return migration.PlannedEffect{}, ErrMigrationPlanInvalid
}

func migrationExportOperationEffects(
	planned []migration.PlannedEffect,
) ([]MigrationEffect, error) {
	if len(planned) != 3 || planned[0].Kind != "snapshot-source" ||
		planned[1].Kind != "write-bundle" || planned[2].Kind != "seal-bundle" {
		return nil, ErrMigrationPlanInvalid
	}
	return []MigrationEffect{
		{ID: planned[0].ID, Kind: MigrationEffectSnapshot, Provider: planned[0].Provider, Status: MigrationEffectPending, Compensation: MigrationCompensationReleaseSnapshot},
		{ID: planned[1].ID, Kind: MigrationEffectWriteBundle, Provider: planned[1].Provider, Status: MigrationEffectPending, Compensation: MigrationCompensationRemovePartial},
		{ID: planned[2].ID, Kind: MigrationEffectSealBundle, Provider: planned[2].Provider, Status: MigrationEffectPending, Compensation: MigrationCompensationNone},
	}, nil
}

func migrationOperationEffect(
	operation MigrationOperation,
	kind MigrationEffectKind,
) (MigrationEffect, error) {
	for _, effect := range operation.Effects {
		if effect.Kind == kind {
			return effect, nil
		}
	}
	return MigrationEffect{}, ErrMigrationOperationInvalid
}

func migrationOperationClaimRefs(
	claims []MigrationClaim,
	class MigrationClaimClass,
) []migration.OpaqueID {
	refs := make([]migration.OpaqueID, 0)
	for _, claim := range claims {
		if claim.Class == class {
			refs = append(refs, migration.OpaqueID(claim.Key))
		}
	}
	slices.Sort(refs)
	return refs
}

func (service MigrationService) nowUTC() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func migrationExportOutputPath(operation MigrationOperation) (string, error) {
	for _, claim := range operation.Claims {
		if claim.Class == MigrationClaimOutputPath {
			return filepath.Clean(claim.Key), nil
		}
	}
	return "", ErrMigrationOperationInvalid
}
