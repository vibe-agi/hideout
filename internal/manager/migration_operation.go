package manager

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	MigrationOperationSchema                     = "hideout.migration-operation/v1"
	migrationDestinationVerificationEvidenceCode = "migration.import.destination_verified"
	migrationDestinationActivationEvidenceCode   = "migration.import.activation_committed"
	migrationRecoveryCodeNone                    = "migration.operation.none"
	migrationRecoveryCodeResume                  = "migration.operation.resume"
	migrationRecoveryCodeFinish                  = "migration.import.finish_required"
	migrationRecoveryCodeRollback                = "migration.import.rollback_required"
)

var (
	ErrMigrationOperationInvalid  = errors.New("migration operation is invalid")
	ErrMigrationOperationMismatch = errors.New("migration operation immutable binding changed")
	ErrMigrationDecisionConflict  = errors.New("migration operation already has the opposite decision")
	ErrMigrationProgressInvalid   = errors.New("migration operation progress is invalid")

	migrationOperationCodePattern   = regexp.MustCompile(`^migration\.[a-z][a-z0-9._-]{1,127}$`)
	migrationProviderNamePattern    = regexp.MustCompile(`^[a-z][a-z0-9.-]{1,127}$`)
	migrationDestinationNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	migrationGuestUserPattern       = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

type MigrationOperationKind string

const (
	MigrationOperationExport MigrationOperationKind = "export"
	MigrationOperationImport MigrationOperationKind = "import"
)

type MigrationPhase string

const (
	MigrationPhaseDraft                MigrationPhase = "draft"
	MigrationPhaseValidating           MigrationPhase = "validating"
	MigrationPhaseAwaitingConfirmation MigrationPhase = "awaiting-confirmation"
	MigrationPhaseClaiming             MigrationPhase = "claiming"
	MigrationPhaseSnapshotting         MigrationPhase = "snapshotting"
	MigrationPhaseWriting              MigrationPhase = "writing"
	MigrationPhaseSealing              MigrationPhase = "sealing"
	MigrationPhaseMaterializing        MigrationPhase = "materializing"
	MigrationPhasePreparingSecrets     MigrationPhase = "preparing-secrets"
	MigrationPhaseAdopting             MigrationPhase = "adopting"
	MigrationPhaseVerifying            MigrationPhase = "verifying"
	MigrationPhaseCommitting           MigrationPhase = "committing"
	MigrationPhaseCancelling           MigrationPhase = "cancelling"
	MigrationPhaseRollingBack          MigrationPhase = "rolling-back"
	MigrationPhaseRecoverableFailure   MigrationPhase = "recoverable-failure"
	MigrationPhaseComplete             MigrationPhase = "complete"
	MigrationPhaseCancelled            MigrationPhase = "cancelled"
	MigrationPhaseRolledBack           MigrationPhase = "rolled-back"
	MigrationPhaseFailed               MigrationPhase = "failed"
)

type MigrationClaimClass string

const (
	MigrationClaimSourceEnvironment    MigrationClaimClass = "source-environment"
	MigrationClaimSourceDisk           MigrationClaimClass = "source-disk"
	MigrationClaimOutputPath           MigrationClaimClass = "output-path"
	MigrationClaimDestinationName      MigrationClaimClass = "destination-name"
	MigrationClaimDestinationProfile   MigrationClaimClass = "destination-profile"
	MigrationClaimDestinationControl   MigrationClaimClass = "destination-control"
	MigrationClaimDestinationWorkspace MigrationClaimClass = "destination-workspace"
	MigrationClaimBackendObject        MigrationClaimClass = "backend-object"
	MigrationClaimSecretDestination    MigrationClaimClass = "secret-destination"
	MigrationClaimStagingRoot          MigrationClaimClass = "staging-root"
)

type MigrationClaimState string

const (
	MigrationClaimPending  MigrationClaimState = "pending"
	MigrationClaimHeld     MigrationClaimState = "held"
	MigrationClaimReleased MigrationClaimState = "released"
)

type MigrationClaim struct {
	Class      MigrationClaimClass `json:"class"`
	Key        string              `json:"key"`
	KeyDigest  migration.Digest    `json:"keyDigest"`
	State      MigrationClaimState `json:"state"`
	AcquiredAt time.Time           `json:"acquiredAt,omitempty"`
	ReleasedAt time.Time           `json:"releasedAt,omitempty"`
}

func NewMigrationClaim(class MigrationClaimClass, key string) (MigrationClaim, error) {
	key = canonicalMigrationClaimKey(class, key)
	claim := MigrationClaim{Class: class, Key: key, State: MigrationClaimPending}
	claim.KeyDigest = migrationClaimDigest(class, key)
	if err := claim.Validate(); err != nil {
		return MigrationClaim{}, err
	}
	return claim, nil
}

func (claim MigrationClaim) Validate() error {
	if !validMigrationClaimClass(claim.Class) ||
		!validMigrationClaimKey(claim.Class, claim.Key) ||
		claim.KeyDigest != migrationClaimDigest(claim.Class, claim.Key) {
		return fmt.Errorf("%w: claim binding", ErrMigrationOperationInvalid)
	}
	switch claim.State {
	case MigrationClaimPending:
		if !claim.AcquiredAt.IsZero() || !claim.ReleasedAt.IsZero() {
			return fmt.Errorf("%w: pending claim has timestamps", ErrMigrationOperationInvalid)
		}
	case MigrationClaimHeld:
		if !validMigrationTime(claim.AcquiredAt) || !claim.ReleasedAt.IsZero() {
			return fmt.Errorf("%w: held claim timestamps", ErrMigrationOperationInvalid)
		}
	case MigrationClaimReleased:
		if !validMigrationTime(claim.ReleasedAt) ||
			(!claim.AcquiredAt.IsZero() &&
				(!validMigrationTime(claim.AcquiredAt) || claim.ReleasedAt.Before(claim.AcquiredAt))) {
			return fmt.Errorf("%w: released claim timestamps", ErrMigrationOperationInvalid)
		}
	default:
		return fmt.Errorf("%w: claim state", ErrMigrationOperationInvalid)
	}
	return nil
}

func SortMigrationClaims(claims []MigrationClaim) {
	sort.Slice(claims, func(left, right int) bool {
		if claims[left].Class == claims[right].Class {
			return claims[left].Key < claims[right].Key
		}
		return claims[left].Class < claims[right].Class
	})
}

type MigrationEffectKind string

const (
	MigrationEffectSnapshot      MigrationEffectKind = "snapshot"
	MigrationEffectWriteBundle   MigrationEffectKind = "write-bundle"
	MigrationEffectSealBundle    MigrationEffectKind = "seal-bundle"
	MigrationEffectStage         MigrationEffectKind = "stage"
	MigrationEffectPrepareSecret MigrationEffectKind = "prepare-secret"
	MigrationEffectAdopt         MigrationEffectKind = "adopt"
	MigrationEffectVerify        MigrationEffectKind = "verify"
	MigrationEffectActivate      MigrationEffectKind = "activate"
	MigrationEffectCleanup       MigrationEffectKind = "cleanup"
)

type MigrationEffectStatus string

const (
	MigrationEffectPending      MigrationEffectStatus = "pending"
	MigrationEffectRunning      MigrationEffectStatus = "running"
	MigrationEffectSucceeded    MigrationEffectStatus = "succeeded"
	MigrationEffectFailed       MigrationEffectStatus = "failed"
	MigrationEffectCompensating MigrationEffectStatus = "compensating"
	MigrationEffectCompensated  MigrationEffectStatus = "compensated"
	MigrationEffectUnproved     MigrationEffectStatus = "unproved"
)

type MigrationCompensation string

const (
	MigrationCompensationNone             MigrationCompensation = "none"
	MigrationCompensationReleaseSnapshot  MigrationCompensation = "release-snapshot"
	MigrationCompensationRemovePartial    MigrationCompensation = "remove-partial"
	MigrationCompensationRollbackStage    MigrationCompensation = "rollback-stage"
	MigrationCompensationDeleteSecret     MigrationCompensation = "delete-secret"
	MigrationCompensationRollbackAdoption MigrationCompensation = "rollback-adoption"
	MigrationCompensationDeactivate       MigrationCompensation = "deactivate"
)

type MigrationEffectEvidence struct {
	Code       string             `json:"code"`
	OpaqueRef  migration.OpaqueID `json:"opaqueRef,omitempty"`
	Digest     migration.Digest   `json:"digest,omitempty"`
	Count      uint64             `json:"count,omitempty"`
	ObservedAt time.Time          `json:"observedAt"`
}

type MigrationEffect struct {
	ID           migration.OpaqueID        `json:"id"`
	Kind         MigrationEffectKind       `json:"kind"`
	Provider     string                    `json:"provider"`
	Status       MigrationEffectStatus     `json:"status"`
	Compensation MigrationCompensation     `json:"compensation"`
	Evidence     []MigrationEffectEvidence `json:"evidence,omitempty"`
}

func (effect MigrationEffect) Validate() error {
	if _, err := migration.ParseOpaqueID(string(effect.ID)); err != nil {
		return fmt.Errorf("%w: effect identity", ErrMigrationOperationInvalid)
	}
	if !validMigrationEffectKind(effect.Kind) ||
		!migrationProviderNamePattern.MatchString(effect.Provider) ||
		!validMigrationEffectStatus(effect.Status) ||
		!validMigrationCompensation(effect.Compensation) || len(effect.Evidence) > 256 {
		return fmt.Errorf("%w: effect binding", ErrMigrationOperationInvalid)
	}
	for _, evidence := range effect.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (evidence MigrationEffectEvidence) Validate() error {
	if !migrationOperationCodePattern.MatchString(evidence.Code) ||
		!validMigrationTime(evidence.ObservedAt) {
		return fmt.Errorf("%w: effect evidence", ErrMigrationOperationInvalid)
	}
	if evidence.OpaqueRef != "" {
		if _, err := migration.ParseOpaqueID(string(evidence.OpaqueRef)); err != nil {
			return fmt.Errorf("%w: effect evidence reference", ErrMigrationOperationInvalid)
		}
	}
	if evidence.Digest != "" && evidence.Digest.Validate() != nil {
		return fmt.Errorf("%w: effect evidence digest", ErrMigrationOperationInvalid)
	}
	return nil
}

type MigrationDecision string

const (
	MigrationDecisionCommit   MigrationDecision = "commit"
	MigrationDecisionRollback MigrationDecision = "rollback"
)

type MigrationCommitDecision struct {
	Value             MigrationDecision `json:"value"`
	PlanDigest        migration.Digest  `json:"planDigest"`
	OperationRevision uint64            `json:"operationRevision"`
	DecidedAt         time.Time         `json:"decidedAt"`
}

type MigrationRecoveryAction string

const (
	MigrationRecoveryNone          MigrationRecoveryAction = "none"
	MigrationRecoveryResume        MigrationRecoveryAction = "resume"
	MigrationRecoveryFinish        MigrationRecoveryAction = "finish"
	MigrationRecoveryRollback      MigrationRecoveryAction = "rollback"
	MigrationRecoveryRemovePartial MigrationRecoveryAction = "remove-partial"
	MigrationRecoveryManual        MigrationRecoveryAction = "manual"
)

type MigrationRecovery struct {
	Code   string                  `json:"code"`
	Action MigrationRecoveryAction `json:"action"`
}

type MigrationOperationResult struct {
	Code          string           `json:"code"`
	ReceiptDigest migration.Digest `json:"receiptDigest,omitempty"`
}

// MigrationCancellationDecision is the durable, revision-bound operator
// choice used to finish cancellation after a daemon restart. RetainPartial is
// meaningful only for exports; imports always roll back operation-owned stage
// state and therefore persist false.
type MigrationCancellationDecision struct {
	RetainPartial     bool      `json:"retainPartial"`
	OperationRevision uint64    `json:"operationRevision"`
	RequestedAt       time.Time `json:"requestedAt"`
}

// Terminal reports whether the durable operation can no longer execute effects.
// It is exported for daemon worker ownership; callers still cannot transition
// state without going through MigrationStore.
func (operation MigrationOperation) Terminal() bool {
	return migrationPhaseTerminal(operation.Phase)
}

type MigrationOperationBundleBinding struct {
	BundleID         migration.BundleID `json:"bundleId"`
	FormatVersion    uint16             `json:"formatVersion"`
	FileDigest       migration.Digest   `json:"fileDigest,omitempty"`
	ManifestDigest   migration.Digest   `json:"manifestDigest,omitempty"`
	CompletionDigest migration.Digest   `json:"completionDigest,omitempty"`
}

// MigrationDestinationIdentity contains destination-local identities generated
// once per import operation. They are durable for crash recovery but omitted
// from operator projections; the sealed bundle never contains them.
type MigrationDestinationIdentity struct {
	SourceRef       migration.OpaqueID `json:"sourceRef"`
	ControlIdentity migration.OpaqueID `json:"controlIdentity"`
	BackendIdentity migration.OpaqueID `json:"backendIdentity"`
}

type MigrationDestinationDiskIdentity struct {
	DiskID          migration.OpaqueID `json:"diskId"`
	Role            migration.DiskRole `json:"role"`
	BackendIdentity migration.OpaqueID `json:"backendIdentity"`
}

type MigrationSourceGuestIdentity struct {
	SourceRef migration.OpaqueID              `json:"sourceRef"`
	Evidence  migration.GuestIdentityEvidence `json:"evidence"`
}

type MigrationDestinationStageCheckpoint struct {
	ComponentID   migration.OpaqueID `json:"componentId"`
	NextOffset    uint64             `json:"nextOffset"`
	ContentDigest migration.Digest   `json:"contentDigest"`
}

type MigrationDestinationStageState struct {
	StageHandle    migration.OpaqueID                    `json:"stageHandle"`
	ObjectHandles  []migration.OpaqueID                  `json:"objectHandles"`
	Checkpoints    []MigrationDestinationStageCheckpoint `json:"checkpoints"`
	Profiles       []MigrationMaterializedProfile        `json:"profiles"`
	ProfileStates  []MigrationMaterializedProfileState   `json:"profileStates,omitempty"`
	EvidenceDigest migration.Digest                      `json:"evidenceDigest"`
}

func (stage MigrationDestinationStageState) Validate() error {
	if !migrationValidOperationOpaqueID(stage.StageHandle) ||
		stage.EvidenceDigest.Validate() != nil || len(stage.ObjectHandles) == 0 ||
		len(stage.ObjectHandles) > 512 || len(stage.Checkpoints) == 0 ||
		len(stage.Checkpoints) > 256 || len(stage.Profiles) == 0 ||
		len(stage.Profiles) > int(migration.HardMaxEnvironments) {
		return fmt.Errorf("%w: destination stage state", ErrMigrationOperationInvalid)
	}
	var previousHandle migration.OpaqueID
	for _, handle := range stage.ObjectHandles {
		if !migrationValidOperationOpaqueID(handle) ||
			(previousHandle != "" && previousHandle >= handle) {
			return fmt.Errorf("%w: destination stage object", ErrMigrationOperationInvalid)
		}
		previousHandle = handle
	}
	var previousComponent migration.OpaqueID
	for _, checkpoint := range stage.Checkpoints {
		if !migrationValidOperationOpaqueID(checkpoint.ComponentID) ||
			checkpoint.NextOffset == 0 || checkpoint.NextOffset > migration.HardMaxLogicalBytes ||
			checkpoint.ContentDigest.Validate() != nil ||
			(previousComponent != "" && previousComponent >= checkpoint.ComponentID) {
			return fmt.Errorf("%w: destination stage checkpoint", ErrMigrationOperationInvalid)
		}
		previousComponent = checkpoint.ComponentID
	}
	previousComponent = ""
	for _, materialized := range stage.Profiles {
		if materialized.Validate() != nil ||
			(previousComponent != "" && previousComponent >= materialized.ComponentID) {
			return fmt.Errorf("%w: destination stage profile", ErrMigrationOperationInvalid)
		}
		previousComponent = materialized.ComponentID
	}
	var previousSource migration.OpaqueID
	for _, state := range stage.ProfileStates {
		if state.validateShape() != nil ||
			(previousSource != "" && previousSource >= state.SourceRef) {
			return fmt.Errorf("%w: destination stage profile state", ErrMigrationOperationInvalid)
		}
		previousSource = state.SourceRef
	}
	return nil
}

type MigrationDestinationAdoptionRecord struct {
	EnvironmentRef            migration.OpaqueID        `json:"environmentRef"`
	Request                   migration.AdoptionRequest `json:"request"`
	Receipt                   migration.AdoptionReceipt `json:"receipt"`
	Stopped                   bool                      `json:"stopped"`
	TemporaryAuthorityRemoved bool                      `json:"temporaryAuthorityRemoved"`
}

type MigrationPreparedSecret struct {
	SourceRef      migration.OpaqueID `json:"sourceRef"`
	DestinationRef string             `json:"destinationRef"`
	Provider       string             `json:"provider"`
	BaseGeneration uint64             `json:"baseGeneration"`
	Generation     uint64             `json:"generation"`
	OperationID    string             `json:"operationId"`
}

func (prepared MigrationPreparedSecret) Validate() error {
	if !migrationValidOperationOpaqueID(prepared.SourceRef) ||
		!migrationDestinationNamePattern.MatchString(prepared.DestinationRef) ||
		!migrationProviderNamePattern.MatchString(prepared.Provider) ||
		prepared.Generation == 0 || prepared.Generation != prepared.BaseGeneration+1 ||
		!operationIDPattern.MatchString(prepared.OperationID) {
		return fmt.Errorf("%w: prepared secret", ErrMigrationOperationInvalid)
	}
	return nil
}

type MigrationDestinationAdoptionState struct {
	StageHandle    migration.OpaqueID                   `json:"stageHandle"`
	Records        []MigrationDestinationAdoptionRecord `json:"records"`
	EvidenceDigest migration.Digest                     `json:"evidenceDigest"`
}

func (state MigrationDestinationAdoptionState) Validate() error {
	if !migrationValidOperationOpaqueID(state.StageHandle) ||
		state.EvidenceDigest.Validate() != nil || len(state.Records) == 0 ||
		len(state.Records) > int(migration.HardMaxEnvironments) {
		return fmt.Errorf("%w: destination adoption state", ErrMigrationOperationInvalid)
	}
	nonces := make(map[migration.OpaqueID]struct{}, len(state.Records)*2)
	var previous migration.OpaqueID
	for _, record := range state.Records {
		if !migrationValidOperationOpaqueID(record.EnvironmentRef) ||
			record.Request.EnvironmentRef != record.EnvironmentRef ||
			record.Request.Validate() != nil || record.Receipt.MatchesRequest(record.Request) != nil ||
			record.Receipt.Status != migration.AdoptionReceiptStatusCompleted ||
			!record.Stopped || !record.TemporaryAuthorityRemoved ||
			(previous != "" && previous >= record.EnvironmentRef) {
			return fmt.Errorf("%w: destination adoption record", ErrMigrationOperationInvalid)
		}
		for _, nonce := range []migration.OpaqueID{
			record.Request.RequestNonce, record.Request.ReceiptNonce,
		} {
			if _, duplicate := nonces[nonce]; duplicate {
				return fmt.Errorf("%w: reused destination adoption nonce", ErrMigrationOperationInvalid)
			}
			nonces[nonce] = struct{}{}
		}
		previous = record.EnvironmentRef
	}
	return nil
}

// MigrationProgress is the durable, presentation-neutral progress checkpoint
// shared by every operator surface. Totals have explicit known bits so zero is
// never confused with unavailable information. Active work excludes time spent
// waiting for confirmation, secrets, or an operator recovery choice.
type MigrationProgress struct {
	LogicalTotalKnown     bool             `json:"logicalTotalKnown"`
	CompletedLogicalBytes uint64           `json:"completedLogicalBytes"`
	TotalLogicalBytes     uint64           `json:"totalLogicalBytes,omitempty"`
	EncodedTotalKnown     bool             `json:"encodedTotalKnown"`
	CompletedEncodedBytes uint64           `json:"completedEncodedBytes"`
	TotalEncodedBytes     uint64           `json:"totalEncodedBytes,omitempty"`
	ComponentsComplete    uint32           `json:"componentsComplete"`
	ComponentsTotal       uint32           `json:"componentsTotal,omitempty"`
	CurrentItem           string           `json:"currentItem,omitempty"`
	PhaseStartedAt        time.Time        `json:"phaseStartedAt,omitempty"`
	CheckpointAt          time.Time        `json:"checkpointAt,omitempty"`
	CheckpointSequence    uint64           `json:"checkpointSequence,omitempty"`
	CheckpointOffset      uint64           `json:"checkpointOffset,omitempty"`
	CheckpointDigest      migration.Digest `json:"checkpointDigest,omitempty"`
	ActiveSince           time.Time        `json:"activeSince,omitempty"`
	ActiveWorkNanos       int64            `json:"activeWorkNanos"`
	CancelPending         bool             `json:"cancelPending"`
	RetainedBytes         uint64           `json:"retainedBytes,omitempty"`
}

// MigrationNotice is plan-bound operator evidence. Summary must already be
// redacted before it can enter durable operation state; NewMigrationNotice is
// the normal construction path.
type MigrationNotice struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

func NewMigrationNotice(code, summary string) (MigrationNotice, error) {
	notice := MigrationNotice{Code: code, Summary: redactMigrationText(summary)}
	if err := notice.Validate(); err != nil {
		return MigrationNotice{}, err
	}
	return notice, nil
}

func (notice MigrationNotice) Validate() error {
	if !migrationOperationCodePattern.MatchString(notice.Code) ||
		!boundedMigrationDisplayText(notice.Summary, 512) ||
		notice.Summary != redactMigrationText(notice.Summary) {
		return ErrMigrationOperationInvalid
	}
	return nil
}

func (progress MigrationProgress) Validate() error {
	encodedMaximum := migration.HardMaxLogicalBytes +
		uint64(migration.HardMaxPayloadRecords)*uint64(migration.HardMaxRecordOverhead)
	if progress.CompletedLogicalBytes > migration.HardMaxLogicalBytes ||
		progress.TotalLogicalBytes > migration.HardMaxLogicalBytes ||
		progress.CompletedEncodedBytes > encodedMaximum ||
		progress.TotalEncodedBytes > encodedMaximum ||
		uint64(progress.ComponentsComplete) > migration.HardMaxPayloadRecords ||
		uint64(progress.ComponentsTotal) > migration.HardMaxPayloadRecords ||
		progress.ActiveWorkNanos < 0 ||
		progress.RetainedBytes > encodedMaximum ||
		progress.CheckpointOffset > encodedMaximum ||
		progress.CheckpointSequence > migration.HardMaxPayloadRecords+1 {
		return ErrMigrationProgressInvalid
	}
	checkpointFields := 0
	if progress.CheckpointSequence != 0 {
		checkpointFields++
	}
	if progress.CheckpointOffset != 0 {
		checkpointFields++
	}
	if progress.CheckpointDigest != "" {
		checkpointFields++
	}
	if checkpointFields != 0 && (checkpointFields != 3 ||
		progress.CheckpointDigest.Validate() != nil ||
		progress.CheckpointAt.IsZero() ||
		progress.CheckpointOffset > progress.CompletedEncodedBytes) {
		return ErrMigrationProgressInvalid
	}
	if progress.LogicalTotalKnown {
		if progress.CompletedLogicalBytes > progress.TotalLogicalBytes {
			return ErrMigrationProgressInvalid
		}
	} else if progress.TotalLogicalBytes != 0 {
		return ErrMigrationProgressInvalid
	}
	if progress.EncodedTotalKnown {
		if progress.CompletedEncodedBytes > progress.TotalEncodedBytes {
			return ErrMigrationProgressInvalid
		}
	} else if progress.TotalEncodedBytes != 0 {
		return ErrMigrationProgressInvalid
	}
	if progress.ComponentsTotal != 0 &&
		progress.ComponentsComplete > progress.ComponentsTotal {
		return ErrMigrationProgressInvalid
	}
	for _, value := range []time.Time{
		progress.PhaseStartedAt, progress.CheckpointAt, progress.ActiveSince,
	} {
		if !value.IsZero() && value.Location() != time.UTC {
			return ErrMigrationProgressInvalid
		}
	}
	if progress.CurrentItem != "" &&
		(!boundedMigrationDisplayText(progress.CurrentItem, 512) ||
			progress.CurrentItem != redactMigrationText(progress.CurrentItem)) {
		return ErrMigrationProgressInvalid
	}
	return nil
}

type MigrationOperation struct {
	Schema                    string                             `json:"schema"`
	ID                        string                             `json:"id"`
	Kind                      MigrationOperationKind             `json:"kind"`
	PlanID                    migration.OpaqueID                 `json:"planId"`
	PlanDigest                migration.Digest                   `json:"planDigest"`
	Bundle                    MigrationOperationBundleBinding    `json:"bundle"`
	BundlePath                string                             `json:"bundlePath,omitempty"`
	BundleFile                *MigrationBundleFileBinding        `json:"bundleFile,omitempty"`
	SourceInventoryDigest     migration.Digest                   `json:"sourceInventoryDigest,omitempty"`
	SelectedSecretRefs        []string                           `json:"selectedSecretRefs,omitempty"`
	BaseRevisions             []migration.BaseRevision           `json:"baseRevisions"`
	CapabilityRevision        migration.Digest                   `json:"capabilityRevision"`
	Phase                     MigrationPhase                     `json:"phase"`
	Revision                  uint64                             `json:"revision"`
	Claims                    []MigrationClaim                   `json:"claims"`
	Effects                   []MigrationEffect                  `json:"effects"`
	ImportObjects             []migration.ImportObject           `json:"importObjects,omitempty"`
	ConflictActions           []migration.ConflictAction         `json:"conflictActions,omitempty"`
	EnvironmentActions        []migration.EnvironmentAction      `json:"environmentActions,omitempty"`
	ExpectedDisks             []migration.DiskObject             `json:"expectedDisks,omitempty"`
	IdentityActions           []migration.IdentityAction         `json:"identityActions,omitempty"`
	WorkspaceActions          []migration.WorkspaceAction        `json:"workspaceActions,omitempty"`
	SecretActions             []migration.SecretAction           `json:"secretActions,omitempty"`
	AuthorityActions          []migration.AuthorityAction        `json:"authorityActions,omitempty"`
	DisabledProposals         []migration.OpaqueID               `json:"disabledProposals,omitempty"`
	PreparedSecrets           []MigrationPreparedSecret          `json:"preparedSecrets,omitempty"`
	SourceGuestIdentities     []MigrationSourceGuestIdentity     `json:"sourceGuestIdentities,omitempty"`
	AdoptionHelper            *migration.HelperBinding           `json:"adoptionHelper,omitempty"`
	DestinationIdentities     []MigrationDestinationIdentity     `json:"destinationIdentities,omitempty"`
	DestinationDiskIdentities []MigrationDestinationDiskIdentity `json:"destinationDiskIdentities,omitempty"`
	DestinationStage          *MigrationDestinationStageState    `json:"destinationStage,omitempty"`
	DestinationAdoption       *MigrationDestinationAdoptionState `json:"destinationAdoption,omitempty"`
	Warnings                  []MigrationNotice                  `json:"warnings,omitempty"`
	Progress                  MigrationProgress                  `json:"progress"`
	Decision                  *MigrationCommitDecision           `json:"decision,omitempty"`
	Cancellation              *MigrationCancellationDecision     `json:"cancellation,omitempty"`
	Recovery                  MigrationRecovery                  `json:"recovery"`
	Result                    *MigrationOperationResult          `json:"result,omitempty"`
	CreatedAt                 time.Time                          `json:"createdAt"`
	UpdatedAt                 time.Time                          `json:"updatedAt"`
}

func (operation MigrationOperation) Validate() error {
	if operation.Schema != MigrationOperationSchema ||
		!operationIDPattern.MatchString(operation.ID) ||
		operation.Revision == 0 || !validMigrationOperationKind(operation.Kind) ||
		!validMigrationPhase(operation.Kind, operation.Phase) {
		return ErrMigrationOperationInvalid
	}
	if _, err := migration.ParseOpaqueID(string(operation.PlanID)); err != nil {
		return ErrMigrationOperationInvalid
	}
	if operation.PlanDigest.Validate() != nil || operation.CapabilityRevision.Validate() != nil ||
		!validMigrationTime(operation.CreatedAt) || !validMigrationTime(operation.UpdatedAt) ||
		operation.UpdatedAt.Before(operation.CreatedAt) {
		return ErrMigrationOperationInvalid
	}
	requireSealedBundle := operation.Kind == MigrationOperationImport ||
		operation.Phase == MigrationPhaseComplete
	if err := operation.Bundle.Validate(requireSealedBundle); err != nil {
		return err
	}
	if operation.Kind == MigrationOperationImport {
		if !validMigrationAbsolutePath(operation.BundlePath) || operation.BundleFile == nil ||
			operation.BundleFile.Validate() != nil {
			return fmt.Errorf("%w: import bundle path", ErrMigrationOperationInvalid)
		}
		if operation.SourceInventoryDigest != "" {
			return fmt.Errorf("%w: import has a source snapshot digest", ErrMigrationOperationInvalid)
		}
	} else if operation.BundlePath != "" || operation.BundleFile != nil ||
		operation.SourceInventoryDigest.Validate() != nil {
		return fmt.Errorf("%w: export source binding", ErrMigrationOperationInvalid)
	}
	if err := validateMigrationBaseRevisions(operation.BaseRevisions); err != nil {
		return err
	}
	if operation.Kind == MigrationOperationExport {
		if validateSortedMigrationNames(operation.SelectedSecretRefs, true) != nil {
			return fmt.Errorf("%w: selected export secrets", ErrMigrationOperationInvalid)
		}
	} else if len(operation.SelectedSecretRefs) != 0 {
		return fmt.Errorf("%w: import has selected export secrets", ErrMigrationOperationInvalid)
	}
	if len(operation.Claims) == 0 || len(operation.Claims) > 256 ||
		len(operation.Effects) == 0 || len(operation.Effects) > 256 {
		return ErrMigrationOperationInvalid
	}
	for index, claim := range operation.Claims {
		if err := claim.Validate(); err != nil {
			return err
		}
		if index > 0 && !migrationClaimLess(operation.Claims[index-1], claim) {
			return fmt.Errorf("%w: claims are duplicated or unsorted", ErrMigrationOperationInvalid)
		}
	}
	seenEffects := make(map[migration.OpaqueID]struct{}, len(operation.Effects))
	for _, effect := range operation.Effects {
		if err := effect.Validate(); err != nil {
			return err
		}
		if _, exists := seenEffects[effect.ID]; exists {
			return fmt.Errorf("%w: duplicate effect", ErrMigrationOperationInvalid)
		}
		seenEffects[effect.ID] = struct{}{}
	}
	if err := operation.validateIdentityActions(); err != nil {
		return err
	}
	if err := operation.validateWorkspaceActions(); err != nil {
		return err
	}
	if err := operation.validateSecretActions(); err != nil {
		return err
	}
	if err := operation.validateAuthorityActions(); err != nil {
		return err
	}
	if err := operation.validatePreparedSecrets(); err != nil {
		return err
	}
	if err := operation.validateDestinationProfileClaims(); err != nil {
		return err
	}
	if err := operation.validateSourceGuestIdentities(); err != nil {
		return err
	}
	if operation.Kind == MigrationOperationExport {
		if len(operation.ImportObjects) != 0 || len(operation.ConflictActions) != 0 ||
			len(operation.EnvironmentActions) != 0 {
			return fmt.Errorf("%w: export has import objects", ErrMigrationOperationInvalid)
		}
	} else if validateMigrationImportObjects(
		operation.ImportObjects, operation.IdentityActions,
	) != nil || validateMigrationConflictActions(operation.ConflictActions) != nil ||
		validateMigrationConflictActionClosure(operation.ImportObjects, operation.ConflictActions) != nil ||
		validateMigrationEnvironmentActions(
			operation.ImportObjects, operation.EnvironmentActions,
		) != nil {
		return fmt.Errorf("%w: durable import objects", ErrMigrationOperationInvalid)
	}
	if err := operation.validateExpectedDisks(); err != nil {
		return err
	}
	if err := operation.validateDestinationIdentities(); err != nil {
		return err
	}
	if err := operation.validateDestinationDiskIdentities(); err != nil {
		return err
	}
	if err := operation.validateImportClaimClosure(); err != nil {
		return err
	}
	if err := operation.validateDestinationStage(); err != nil {
		return err
	}
	if err := operation.validateDestinationAdoption(); err != nil {
		return err
	}
	if err := operation.validateDestinationVerification(); err != nil {
		return err
	}
	if err := operation.validateDestinationActivation(); err != nil {
		return err
	}
	if err := operation.validateImportEffectProgression(); err != nil {
		return err
	}
	if len(operation.Warnings) > 256 {
		return ErrMigrationOperationInvalid
	}
	for _, warning := range operation.Warnings {
		if err := warning.Validate(); err != nil {
			return err
		}
	}
	if err := operation.Progress.Validate(); err != nil {
		return err
	}
	for _, value := range []time.Time{
		operation.Progress.PhaseStartedAt,
		operation.Progress.CheckpointAt,
		operation.Progress.ActiveSince,
	} {
		if !value.IsZero() &&
			(value.Before(operation.CreatedAt) || value.After(operation.UpdatedAt)) {
			return ErrMigrationProgressInvalid
		}
	}
	if migrationPhaseTerminal(operation.Phase) && !operation.Progress.ActiveSince.IsZero() {
		return ErrMigrationProgressInvalid
	}
	if err := operation.validateDecision(); err != nil {
		return err
	}
	if err := operation.validateCancellation(); err != nil {
		return err
	}
	if err := operation.validateRecovery(); err != nil {
		return err
	}
	terminal := migrationPhaseTerminal(operation.Phase)
	if terminal {
		if operation.Result == nil ||
			!migrationOperationCodePattern.MatchString(operation.Result.Code) ||
			(operation.Result.ReceiptDigest != "" && operation.Result.ReceiptDigest.Validate() != nil) {
			return ErrMigrationOperationInvalid
		}
	} else if operation.Result != nil {
		return ErrMigrationOperationInvalid
	}
	if operation.Phase == MigrationPhaseComplete {
		for _, effect := range operation.Effects {
			if effect.Status != MigrationEffectSucceeded {
				return fmt.Errorf("%w: complete operation has unfinished effects", ErrMigrationOperationInvalid)
			}
		}
		if operation.Kind == MigrationOperationImport {
			activation, err := migrationOperationEffect(operation, MigrationEffectActivate)
			if err != nil || len(activation.Evidence) != 1 || operation.Result == nil ||
				operation.Result.ReceiptDigest != activation.Evidence[0].Digest {
				return fmt.Errorf("%w: import receipt is not bound to activation evidence", ErrMigrationOperationInvalid)
			}
		}
	}
	return nil
}

func (operation MigrationOperation) validateRecovery() error {
	if !migrationOperationCodePattern.MatchString(operation.Recovery.Code) ||
		!validMigrationRecoveryAction(operation.Recovery.Action) {
		return ErrMigrationOperationInvalid
	}
	expected := migrationRecoveryForState(operation)
	if operation.Recovery != expected {
		return fmt.Errorf("%w: recovery action does not match durable state", ErrMigrationOperationInvalid)
	}
	return nil
}

func migrationRecoveryForState(operation MigrationOperation) MigrationRecovery {
	if migrationPhaseTerminal(operation.Phase) {
		if operation.Phase == MigrationPhaseCancelled &&
			operation.Kind == MigrationOperationExport &&
			operation.Cancellation != nil && operation.Cancellation.RetainPartial &&
			operation.Progress.RetainedBytes > 0 {
			return MigrationRecovery{
				Code: "migration.export.retained_partial", Action: MigrationRecoveryRemovePartial,
			}
		}
		code := migrationRecoveryCodeNone
		if operation.Result != nil && migrationOperationCodePattern.MatchString(operation.Result.Code) {
			code = operation.Result.Code
		}
		return MigrationRecovery{Code: code, Action: MigrationRecoveryNone}
	}
	if operation.Phase != MigrationPhaseRecoverableFailure {
		return MigrationRecovery{Code: migrationRecoveryCodeNone, Action: MigrationRecoveryNone}
	}
	if operation.Kind == MigrationOperationExport {
		if operation.Cancellation != nil {
			return MigrationRecovery{
				Code: "migration.export.cleanup_required", Action: MigrationRecoveryRemovePartial,
			}
		}
		return MigrationRecovery{Code: migrationRecoveryCodeResume, Action: MigrationRecoveryResume}
	}
	if operation.Decision != nil && operation.Decision.Value == MigrationDecisionCommit {
		return MigrationRecovery{Code: migrationRecoveryCodeFinish, Action: MigrationRecoveryFinish}
	}
	if operation.Cancellation != nil ||
		(operation.Decision != nil && operation.Decision.Value == MigrationDecisionRollback) {
		return MigrationRecovery{Code: migrationRecoveryCodeRollback, Action: MigrationRecoveryRollback}
	}
	return MigrationRecovery{Code: migrationRecoveryCodeResume, Action: MigrationRecoveryResume}
}

func (operation MigrationOperation) validateImportClaimClosure() error {
	if operation.Kind == MigrationOperationExport {
		return nil
	}
	expected := make(map[MigrationClaimClass]map[string]struct{})
	add := func(class MigrationClaimClass, key string) {
		if expected[class] == nil {
			expected[class] = make(map[string]struct{})
		}
		expected[class][canonicalMigrationClaimKey(class, key)] = struct{}{}
	}
	for _, object := range operation.ImportObjects {
		add(MigrationClaimDestinationName, object.DestinationName)
	}
	for _, action := range operation.EnvironmentActions {
		add(MigrationClaimDestinationProfile, action.DestinationProfileName)
	}
	for _, identity := range operation.DestinationIdentities {
		add(MigrationClaimDestinationControl, string(identity.ControlIdentity))
		add(MigrationClaimBackendObject, string(identity.BackendIdentity))
	}
	for _, identity := range operation.DestinationDiskIdentities {
		add(MigrationClaimBackendObject, string(identity.BackendIdentity))
	}
	for _, action := range operation.WorkspaceActions {
		if action.Decision == migrationWorkspaceDecisionMapped {
			add(MigrationClaimDestinationWorkspace, action.DestinationPath)
		}
	}
	for _, action := range operation.SecretActions {
		add(MigrationClaimSecretDestination, action.DestinationRef)
	}

	actual := make(map[MigrationClaimClass]map[string]struct{})
	stagingClaims := 0
	for _, claim := range operation.Claims {
		switch claim.Class {
		case MigrationClaimDestinationName, MigrationClaimDestinationProfile,
			MigrationClaimDestinationControl, MigrationClaimDestinationWorkspace,
			MigrationClaimBackendObject, MigrationClaimSecretDestination:
			if actual[claim.Class] == nil {
				actual[claim.Class] = make(map[string]struct{})
			}
			actual[claim.Class][claim.Key] = struct{}{}
		case MigrationClaimStagingRoot:
			stagingClaims++
			clean := filepath.Clean(claim.Key)
			if filepath.Base(clean) != operation.ID ||
				filepath.Base(filepath.Dir(clean)) != "staging" ||
				filepath.Base(filepath.Dir(filepath.Dir(clean))) != "migration" {
				return fmt.Errorf("%w: staging claim binding", ErrMigrationOperationInvalid)
			}
		default:
			return fmt.Errorf("%w: unexpected import claim class", ErrMigrationOperationInvalid)
		}
	}
	if stagingClaims != 1 || len(actual) != len(expected) {
		return fmt.Errorf("%w: import claim class closure", ErrMigrationOperationInvalid)
	}
	for class, keys := range expected {
		if len(actual[class]) != len(keys) {
			return fmt.Errorf("%w: import claim cardinality", ErrMigrationOperationInvalid)
		}
		for key := range keys {
			if _, exists := actual[class][key]; !exists {
				return fmt.Errorf("%w: import claim binding", ErrMigrationOperationInvalid)
			}
		}
	}
	return nil
}

func (binding MigrationOperationBundleBinding) Validate(requireSealed bool) error {
	if _, err := migration.ParseBundleID(string(binding.BundleID)); err != nil ||
		binding.FormatVersion != migration.BundleFormatVersion {
		return fmt.Errorf("%w: bundle binding", ErrMigrationOperationInvalid)
	}
	for _, digest := range []migration.Digest{
		binding.FileDigest, binding.ManifestDigest, binding.CompletionDigest,
	} {
		if digest == "" {
			if requireSealed {
				return fmt.Errorf("%w: sealed bundle digest is absent", ErrMigrationOperationInvalid)
			}
			continue
		}
		if digest.Validate() != nil {
			return fmt.Errorf("%w: bundle digest", ErrMigrationOperationInvalid)
		}
	}
	return nil
}

func (operation MigrationOperation) Clone() MigrationOperation {
	cloned := operation
	cloned.BaseRevisions = append([]migration.BaseRevision(nil), operation.BaseRevisions...)
	cloned.SelectedSecretRefs = append([]string(nil), operation.SelectedSecretRefs...)
	cloned.Claims = append([]MigrationClaim(nil), operation.Claims...)
	cloned.Effects = append([]MigrationEffect(nil), operation.Effects...)
	for index := range cloned.Effects {
		cloned.Effects[index].Evidence = append(
			[]MigrationEffectEvidence(nil), operation.Effects[index].Evidence...,
		)
	}
	cloned.IdentityActions = append([]migration.IdentityAction(nil), operation.IdentityActions...)
	cloned.ConflictActions = cloneMigrationConflictActions(operation.ConflictActions)
	cloned.EnvironmentActions = append(
		[]migration.EnvironmentAction(nil), operation.EnvironmentActions...,
	)
	cloned.WorkspaceActions = append([]migration.WorkspaceAction(nil), operation.WorkspaceActions...)
	cloned.SecretActions = cloneMigrationSecretActions(operation.SecretActions)
	cloned.AuthorityActions = append(
		[]migration.AuthorityAction(nil), operation.AuthorityActions...,
	)
	cloned.DisabledProposals = append(
		[]migration.OpaqueID(nil), operation.DisabledProposals...,
	)
	cloned.PreparedSecrets = append(
		[]MigrationPreparedSecret(nil), operation.PreparedSecrets...,
	)
	cloned.ImportObjects = cloneMigrationImportObjects(operation.ImportObjects)
	cloned.ExpectedDisks = cloneMigrationDiskObjects(operation.ExpectedDisks)
	cloned.SourceGuestIdentities = cloneMigrationSourceGuestIdentities(
		operation.SourceGuestIdentities,
	)
	if operation.AdoptionHelper != nil {
		helper := *operation.AdoptionHelper
		cloned.AdoptionHelper = &helper
	}
	cloned.DestinationIdentities = append(
		[]MigrationDestinationIdentity(nil), operation.DestinationIdentities...,
	)
	cloned.DestinationDiskIdentities = append(
		[]MigrationDestinationDiskIdentity(nil), operation.DestinationDiskIdentities...,
	)
	cloned.DestinationStage = cloneMigrationDestinationStage(operation.DestinationStage)
	cloned.DestinationAdoption = cloneMigrationDestinationAdoption(
		operation.DestinationAdoption,
	)
	cloned.Warnings = append([]MigrationNotice(nil), operation.Warnings...)
	if operation.Decision != nil {
		decision := *operation.Decision
		cloned.Decision = &decision
	}
	if operation.Cancellation != nil {
		cancellation := *operation.Cancellation
		cloned.Cancellation = &cancellation
	}
	if operation.Result != nil {
		result := *operation.Result
		cloned.Result = &result
	}
	cloned.BundleFile = cloneBundleFileBinding(operation.BundleFile)
	return cloned
}

func (operation MigrationOperation) MatchesImmutable(other MigrationOperation) bool {
	if operation.ID != other.ID || operation.Kind != other.Kind ||
		operation.PlanID != other.PlanID || operation.PlanDigest != other.PlanDigest ||
		operation.Bundle != other.Bundle ||
		operation.BundlePath != other.BundlePath ||
		!bundleFileBindingsEqual(operation.BundleFile, other.BundleFile) ||
		operation.SourceInventoryDigest != other.SourceInventoryDigest ||
		operation.CapabilityRevision != other.CapabilityRevision ||
		!slices.Equal(operation.SelectedSecretRefs, other.SelectedSecretRefs) ||
		!slices.Equal(operation.BaseRevisions, other.BaseRevisions) ||
		!migrationImportObjectsEqual(operation.ImportObjects, other.ImportObjects) ||
		!migrationConflictActionsEqual(operation.ConflictActions, other.ConflictActions) ||
		!slices.Equal(operation.EnvironmentActions, other.EnvironmentActions) ||
		!migrationDiskObjectSlicesEqual(operation.ExpectedDisks, other.ExpectedDisks) ||
		!slices.Equal(operation.IdentityActions, other.IdentityActions) ||
		!slices.Equal(operation.WorkspaceActions, other.WorkspaceActions) ||
		!migrationSecretActionsEqual(operation.SecretActions, other.SecretActions) ||
		!slices.Equal(operation.AuthorityActions, other.AuthorityActions) ||
		!slices.Equal(operation.DisabledProposals, other.DisabledProposals) ||
		!migrationSourceGuestIdentitiesEqual(
			operation.SourceGuestIdentities, other.SourceGuestIdentities,
		) || !migrationHelperBindingsEqual(operation.AdoptionHelper, other.AdoptionHelper) ||
		!slices.Equal(operation.DestinationIdentities, other.DestinationIdentities) ||
		!slices.Equal(operation.DestinationDiskIdentities, other.DestinationDiskIdentities) ||
		!slices.Equal(operation.Warnings, other.Warnings) ||
		len(operation.Claims) != len(other.Claims) || len(operation.Effects) != len(other.Effects) {
		return false
	}
	for index := range operation.Claims {
		left, right := operation.Claims[index], other.Claims[index]
		if left.Class != right.Class || left.Key != right.Key || left.KeyDigest != right.KeyDigest {
			return false
		}
	}
	for index := range operation.Effects {
		left, right := operation.Effects[index], other.Effects[index]
		if left.ID != right.ID || left.Kind != right.Kind ||
			left.Provider != right.Provider || left.Compensation != right.Compensation {
			return false
		}
	}
	return true
}

// WithProgress persists a monotonic checkpoint. It sanitizes the only free-text
// field before validation so provider diagnostics never enter Manager state.
func (operation MigrationOperation) WithProgress(
	progress MigrationProgress,
	at time.Time,
) (MigrationOperation, bool, error) {
	if err := operation.Validate(); err != nil {
		return operation, false, err
	}
	if !validMigrationTime(at) || at.Before(operation.UpdatedAt) ||
		migrationPhaseTerminal(operation.Phase) {
		return operation, false, ErrMigrationProgressInvalid
	}
	progress.CurrentItem = redactMigrationText(progress.CurrentItem)
	if err := progress.Validate(); err != nil {
		return operation, false, err
	}
	previous := operation.Progress
	if progress.CompletedLogicalBytes < previous.CompletedLogicalBytes ||
		progress.CompletedEncodedBytes < previous.CompletedEncodedBytes ||
		progress.ComponentsComplete < previous.ComponentsComplete ||
		progress.CheckpointSequence < previous.CheckpointSequence ||
		progress.CheckpointOffset < previous.CheckpointOffset ||
		(previous.CheckpointDigest != "" && progress.CheckpointDigest == "") ||
		(previous.CheckpointSequence != 0 &&
			progress.CheckpointSequence == previous.CheckpointSequence &&
			(progress.CheckpointOffset != previous.CheckpointOffset ||
				progress.CheckpointDigest != previous.CheckpointDigest)) ||
		progress.ActiveWorkNanos < previous.ActiveWorkNanos ||
		(previous.LogicalTotalKnown && !progress.LogicalTotalKnown) ||
		(previous.EncodedTotalKnown && !progress.EncodedTotalKnown) ||
		(progress.LogicalTotalKnown && previous.LogicalTotalKnown &&
			progress.TotalLogicalBytes < previous.TotalLogicalBytes) ||
		(progress.EncodedTotalKnown && previous.EncodedTotalKnown &&
			progress.TotalEncodedBytes < previous.TotalEncodedBytes) ||
		(progress.ComponentsTotal != 0 && previous.ComponentsTotal != 0 &&
			progress.ComponentsTotal < previous.ComponentsTotal) ||
		(!previous.CheckpointAt.IsZero() &&
			(progress.CheckpointAt.IsZero() || progress.CheckpointAt.Before(previous.CheckpointAt))) ||
		(!previous.PhaseStartedAt.IsZero() &&
			progress.PhaseStartedAt != previous.PhaseStartedAt) {
		return operation, false, ErrMigrationProgressInvalid
	}
	for _, value := range []time.Time{
		progress.PhaseStartedAt, progress.CheckpointAt, progress.ActiveSince,
	} {
		if !value.IsZero() && (value.Before(operation.CreatedAt) || value.After(at)) {
			return operation, false, ErrMigrationProgressInvalid
		}
	}
	if reflect.DeepEqual(previous, progress) {
		return operation.Clone(), false, nil
	}
	next := operation.Clone()
	next.Progress = progress
	next.Revision++
	next.UpdatedAt = at
	if err := next.Validate(); err != nil {
		return operation, false, err
	}
	return next, true, nil
}

// Decide records the only allowed import commit/rollback decision. Replaying the
// same value is a no-op; an opposite value can never replace it.
func (operation MigrationOperation) Decide(
	decision MigrationDecision,
	at time.Time,
) (MigrationOperation, bool, error) {
	if err := operation.Validate(); err != nil {
		return operation, false, err
	}
	if operation.Kind != MigrationOperationImport || !validMigrationTime(at) ||
		at.Before(operation.UpdatedAt) {
		return operation, false, ErrMigrationOperationInvalid
	}
	if operation.Decision != nil {
		if operation.Decision.Value == decision {
			return operation.Clone(), false, nil
		}
		return operation, false, ErrMigrationDecisionConflict
	}
	next := operation.Clone()
	switch decision {
	case MigrationDecisionCommit:
		if operation.Phase != MigrationPhaseVerifying &&
			operation.Phase != MigrationPhaseRecoverableFailure {
			return operation, false, ErrMigrationOperationInvalid
		}
		verifyEffect, err := migrationOperationEffect(operation, MigrationEffectVerify)
		if err != nil || verifyEffect.Status != MigrationEffectSucceeded {
			return operation, false, ErrMigrationOperationInvalid
		}
		next.Phase = MigrationPhaseCommitting
	case MigrationDecisionRollback:
		if !migrationRollbackDecisionPhase(operation.Phase) {
			return operation, false, ErrMigrationOperationInvalid
		}
		next.Phase = MigrationPhaseRollingBack
	default:
		return operation, false, ErrMigrationOperationInvalid
	}
	next.Decision = &MigrationCommitDecision{
		Value: decision, PlanDigest: operation.PlanDigest,
		OperationRevision: operation.Revision, DecidedAt: at,
	}
	next.Recovery = MigrationRecovery{
		Code: migrationRecoveryCodeNone, Action: MigrationRecoveryNone,
	}
	next.Progress = migrationProgressForPhaseTransition(next.Progress, at)
	next.Revision++
	next.UpdatedAt = at
	if err := next.Validate(); err != nil {
		return operation, false, err
	}
	return next, true, nil
}

func migrationProgressForPhaseTransition(
	progress MigrationProgress,
	at time.Time,
) MigrationProgress {
	if !progress.ActiveSince.IsZero() && at.After(progress.ActiveSince) {
		active := at.Sub(progress.ActiveSince)
		if progress.ActiveWorkNanos <= int64(^uint64(0)>>1)-int64(active) {
			progress.ActiveWorkNanos += int64(active)
		} else {
			progress.ActiveWorkNanos = int64(^uint64(0) >> 1)
		}
	}
	progress.ActiveSince = time.Time{}
	progress.PhaseStartedAt = at
	progress.CurrentItem = ""
	return progress
}

func (operation MigrationOperation) validateIdentityActions() error {
	if operation.Kind == MigrationOperationExport {
		if len(operation.IdentityActions) != 0 || len(operation.DestinationIdentities) != 0 {
			return fmt.Errorf("%w: export has destination identities", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if len(operation.IdentityActions) == 0 ||
		len(operation.IdentityActions) > int(migration.HardMaxEnvironments) {
		return fmt.Errorf("%w: import identity actions", ErrMigrationOperationInvalid)
	}
	var previous migration.OpaqueID
	for _, action := range operation.IdentityActions {
		if _, err := migration.ParseOpaqueID(string(action.SourceRef)); err != nil ||
			(action.GuestPolicy != migration.GuestIdentitySafeClone &&
				action.GuestPolicy != migration.GuestIdentityExactRestore) ||
			!action.FreshControlIdentity || !action.FreshBackendIdentity ||
			(previous != "" && previous >= action.SourceRef) {
			return fmt.Errorf("%w: import identity action", ErrMigrationOperationInvalid)
		}
		previous = action.SourceRef
	}
	return nil
}

func (operation MigrationOperation) validateWorkspaceActions() error {
	workspaceClaims := make(map[string]struct{})
	for _, claim := range operation.Claims {
		if claim.Class != MigrationClaimDestinationWorkspace {
			continue
		}
		if _, duplicate := workspaceClaims[claim.Key]; duplicate {
			return fmt.Errorf("%w: duplicate workspace claim", ErrMigrationOperationInvalid)
		}
		workspaceClaims[claim.Key] = struct{}{}
	}
	if operation.Kind == MigrationOperationExport {
		if len(operation.WorkspaceActions) != 0 || len(workspaceClaims) != 0 {
			return fmt.Errorf("%w: export has workspace authority", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if validateMigrationWorkspaceActions(operation.WorkspaceActions) != nil {
		return fmt.Errorf("%w: import workspace actions", ErrMigrationOperationInvalid)
	}
	environments := make(map[migration.OpaqueID]struct{}, len(operation.ImportObjects))
	for _, object := range operation.ImportObjects {
		environments[object.SourceRef] = struct{}{}
	}
	expectedClaims := make(map[string]struct{})
	for _, action := range operation.WorkspaceActions {
		if _, selected := environments[action.EnvironmentRef]; !selected {
			return fmt.Errorf("%w: workspace environment binding", ErrMigrationOperationInvalid)
		}
		if action.Decision == migrationWorkspaceDecisionMapped {
			expectedClaims[action.DestinationPath] = struct{}{}
		}
	}
	if len(expectedClaims) != len(workspaceClaims) {
		return fmt.Errorf("%w: workspace claim cardinality", ErrMigrationOperationInvalid)
	}
	for path := range expectedClaims {
		if _, exists := workspaceClaims[path]; !exists {
			return fmt.Errorf("%w: workspace claim binding", ErrMigrationOperationInvalid)
		}
	}
	return nil
}

func (operation MigrationOperation) validateSecretActions() error {
	secretClaims := make(map[string]struct{})
	for _, claim := range operation.Claims {
		if claim.Class != MigrationClaimSecretDestination {
			continue
		}
		if _, duplicate := secretClaims[claim.Key]; duplicate {
			return fmt.Errorf("%w: duplicate secret destination claim", ErrMigrationOperationInvalid)
		}
		secretClaims[claim.Key] = struct{}{}
	}
	if operation.Kind == MigrationOperationExport {
		if len(operation.SecretActions) != 0 || len(secretClaims) != 0 {
			return fmt.Errorf("%w: export has destination secret actions", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if validateMigrationSecretActions(operation.SecretActions) != nil {
		return fmt.Errorf("%w: import secret actions", ErrMigrationOperationInvalid)
	}
	selected := make(map[migration.OpaqueID]struct{}, len(operation.ImportObjects))
	for _, object := range operation.ImportObjects {
		selected[object.SourceRef] = struct{}{}
	}
	expectedClaims := make(map[string]struct{}, len(operation.SecretActions))
	for _, action := range operation.SecretActions {
		for _, environmentRef := range action.EnvironmentRefs {
			if _, exists := selected[environmentRef]; !exists {
				return fmt.Errorf("%w: secret environment binding", ErrMigrationOperationInvalid)
			}
		}
		expectedClaims[action.DestinationRef] = struct{}{}
	}
	if len(expectedClaims) != len(secretClaims) {
		return fmt.Errorf("%w: secret destination claim cardinality", ErrMigrationOperationInvalid)
	}
	for ref := range expectedClaims {
		if _, exists := secretClaims[ref]; !exists {
			return fmt.Errorf("%w: secret destination claim binding", ErrMigrationOperationInvalid)
		}
	}
	return nil
}

func (operation MigrationOperation) validateAuthorityActions() error {
	if operation.Kind == MigrationOperationExport {
		if len(operation.AuthorityActions) != 0 || len(operation.DisabledProposals) != 0 {
			return fmt.Errorf("%w: export has destination authority decisions", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if validateMigrationAuthorityActions(operation.AuthorityActions) != nil ||
		validateSortedMigrationOpaqueIDs(operation.DisabledProposals, true) != nil {
		return fmt.Errorf("%w: import authority actions", ErrMigrationOperationInvalid)
	}
	selected := make(map[migration.OpaqueID]struct{}, len(operation.ImportObjects))
	for _, object := range operation.ImportObjects {
		selected[object.SourceRef] = struct{}{}
	}
	seen := make(map[migration.OpaqueID]struct{}, len(operation.AuthorityActions)+len(operation.DisabledProposals))
	for _, action := range operation.AuthorityActions {
		if _, exists := selected[action.EnvironmentRef]; !exists {
			return fmt.Errorf("%w: authority environment binding", ErrMigrationOperationInvalid)
		}
		if _, duplicate := seen[action.ProposalID]; duplicate {
			return fmt.Errorf("%w: duplicate authority decision", ErrMigrationOperationInvalid)
		}
		seen[action.ProposalID] = struct{}{}
	}
	for _, proposalID := range operation.DisabledProposals {
		if _, duplicate := seen[proposalID]; duplicate {
			return fmt.Errorf("%w: duplicate authority decision", ErrMigrationOperationInvalid)
		}
		seen[proposalID] = struct{}{}
	}
	return nil
}

func (operation MigrationOperation) validatePreparedSecrets() error {
	var prepareEffect *MigrationEffect
	for index := range operation.Effects {
		if operation.Effects[index].Kind != MigrationEffectPrepareSecret {
			continue
		}
		if prepareEffect != nil {
			return fmt.Errorf("%w: duplicate prepare-secret effect", ErrMigrationOperationInvalid)
		}
		prepareEffect = &operation.Effects[index]
	}
	expected := make(map[migration.OpaqueID]migration.SecretAction)
	for _, action := range operation.SecretActions {
		if action.Decision == migrationSecretDecisionImportValue {
			expected[action.SourceRef] = action
		}
	}
	if operation.Kind == MigrationOperationExport || len(expected) == 0 {
		if prepareEffect != nil || len(operation.PreparedSecrets) != 0 {
			return fmt.Errorf("%w: unexpected prepared secrets", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if prepareEffect == nil || prepareEffect.Provider != "manager" ||
		prepareEffect.Compensation != MigrationCompensationDeleteSecret {
		return fmt.Errorf("%w: prepare-secret effect is absent", ErrMigrationOperationInvalid)
	}
	if prepareEffect.Status != MigrationEffectSucceeded &&
		prepareEffect.Status != MigrationEffectCompensating &&
		prepareEffect.Status != MigrationEffectCompensated {
		if len(operation.PreparedSecrets) != 0 {
			return fmt.Errorf("%w: unproved prepared secrets", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if prepareEffect.Status != MigrationEffectCompensated &&
		len(operation.PreparedSecrets) != len(expected) {
		return fmt.Errorf("%w: prepared secret closure", ErrMigrationOperationInvalid)
	}
	if prepareEffect.Status == MigrationEffectCompensated &&
		len(operation.PreparedSecrets) > len(expected) {
		return fmt.Errorf("%w: compensated secret closure", ErrMigrationOperationInvalid)
	}
	var previous migration.OpaqueID
	for _, prepared := range operation.PreparedSecrets {
		action, exists := expected[prepared.SourceRef]
		if !exists || prepared.Validate() != nil ||
			(previous != "" && previous >= prepared.SourceRef) ||
			prepared.DestinationRef != action.DestinationRef ||
			prepared.Provider != action.DestinationProvider ||
			prepared.BaseGeneration != action.BaseGeneration ||
			prepared.OperationID != migrationImportSecretOperationID(
				operation.ID, action.DestinationRef,
			) {
			return fmt.Errorf("%w: prepared secret binding", ErrMigrationOperationInvalid)
		}
		previous = prepared.SourceRef
	}
	if len(prepareEffect.Evidence) != 1 {
		return fmt.Errorf("%w: prepared secret evidence is absent", ErrMigrationOperationInvalid)
	}
	evidence := prepareEffect.Evidence[0]
	domain := "migration-import-prepared-secrets"
	code := "migration.import.secrets_prepared"
	if prepareEffect.Status == MigrationEffectCompensated {
		domain = "migration-import-secret-rollback"
		code = "migration.import.secrets_rolled_back"
	}
	digest, err := CanonicalDigest(domain, operation.PreparedSecrets)
	if err != nil || evidence.Code != code || evidence.Digest != migration.Digest(digest) ||
		evidence.Count != uint64(len(operation.PreparedSecrets)) ||
		evidence.ObservedAt.Before(operation.CreatedAt) ||
		evidence.ObservedAt.After(operation.UpdatedAt) {
		return fmt.Errorf("%w: prepared secret evidence", ErrMigrationOperationInvalid)
	}
	return nil
}

func (operation MigrationOperation) validateDestinationProfileClaims() error {
	actual := make(map[string]struct{})
	for _, claim := range operation.Claims {
		if claim.Class != MigrationClaimDestinationProfile {
			continue
		}
		if _, duplicate := actual[claim.Key]; duplicate {
			return fmt.Errorf("%w: duplicate destination profile claim", ErrMigrationOperationInvalid)
		}
		actual[claim.Key] = struct{}{}
	}
	if operation.Kind == MigrationOperationExport {
		if len(actual) != 0 {
			return fmt.Errorf("%w: export has destination profile claims", ErrMigrationOperationInvalid)
		}
		return nil
	}
	expected := make(map[string]struct{}, len(operation.EnvironmentActions))
	for _, action := range operation.EnvironmentActions {
		expected[canonicalMigrationClaimKey(
			MigrationClaimDestinationProfile,
			action.DestinationProfileName,
		)] = struct{}{}
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("%w: destination profile claim cardinality", ErrMigrationOperationInvalid)
	}
	for name := range expected {
		if _, exists := actual[name]; !exists {
			return fmt.Errorf("%w: destination profile claim binding", ErrMigrationOperationInvalid)
		}
	}
	return nil
}

func (operation MigrationOperation) validateSourceGuestIdentities() error {
	if operation.Kind == MigrationOperationExport {
		if len(operation.SourceGuestIdentities) != 0 || operation.AdoptionHelper != nil ||
			operation.DestinationAdoption != nil {
			return fmt.Errorf("%w: export has adoption state", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		if len(operation.SourceGuestIdentities) != 0 || operation.AdoptionHelper != nil ||
			operation.DestinationAdoption != nil {
			return fmt.Errorf("%w: config import has guest adoption state", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if len(operation.SourceGuestIdentities) != len(operation.IdentityActions) ||
		operation.AdoptionHelper == nil || operation.AdoptionHelper.Validate() != nil {
		return fmt.Errorf("%w: import adoption source facts", ErrMigrationOperationInvalid)
	}
	for index, identity := range operation.SourceGuestIdentities {
		if identity.SourceRef != operation.IdentityActions[index].SourceRef ||
			identity.Evidence.Validate() != nil {
			return fmt.Errorf("%w: import source guest identity", ErrMigrationOperationInvalid)
		}
	}
	return nil
}

func (operation MigrationOperation) validateExpectedDisks() error {
	if operation.Kind == MigrationOperationExport {
		if len(operation.ExpectedDisks) != 0 {
			return fmt.Errorf("%w: export has destination disk expectations", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		if len(operation.ExpectedDisks) != 0 || len(operation.DestinationDiskIdentities) != 0 {
			return fmt.Errorf("%w: config import has destination disk state", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if migration.ValidateDiskObjects(operation.ExpectedDisks, migration.DefaultLimits()) != nil ||
		len(operation.ExpectedDisks) != len(operation.DestinationDiskIdentities) {
		return fmt.Errorf("%w: destination disk expectations", ErrMigrationOperationInvalid)
	}
	selected := make(map[migration.OpaqueID]struct{})
	for _, object := range operation.ImportObjects {
		for _, diskID := range object.DiskRefs {
			selected[diskID] = struct{}{}
		}
	}
	for index, disk := range operation.ExpectedDisks {
		if _, exists := selected[disk.DiskID]; !exists ||
			operation.DestinationDiskIdentities[index].DiskID != disk.DiskID ||
			operation.DestinationDiskIdentities[index].Role != disk.Role {
			return fmt.Errorf("%w: destination disk expectation binding", ErrMigrationOperationInvalid)
		}
		delete(selected, disk.DiskID)
	}
	if len(selected) != 0 {
		return fmt.Errorf("%w: destination disk expectation closure", ErrMigrationOperationInvalid)
	}
	return nil
}

func (operation MigrationOperation) validateDestinationIdentities() error {
	if operation.Kind == MigrationOperationExport {
		if len(operation.DestinationDiskIdentities) != 0 {
			return fmt.Errorf("%w: export has destination disk identities", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if len(operation.DestinationIdentities) != len(operation.IdentityActions) {
		return fmt.Errorf("%w: destination identity cardinality", ErrMigrationOperationInvalid)
	}
	controlIDs := make(map[migration.OpaqueID]struct{}, len(operation.DestinationIdentities))
	backendIDs := make(map[migration.OpaqueID]struct{}, len(operation.DestinationIdentities))
	for index, identity := range operation.DestinationIdentities {
		if identity.SourceRef != operation.IdentityActions[index].SourceRef ||
			!environment.ValidID(string(identity.ControlIdentity)) ||
			!migrationValidOperationOpaqueID(identity.BackendIdentity) {
			return fmt.Errorf("%w: destination identity binding", ErrMigrationOperationInvalid)
		}
		if _, exists := controlIDs[identity.ControlIdentity]; exists {
			return fmt.Errorf("%w: duplicate control identity", ErrMigrationOperationInvalid)
		}
		if _, exists := backendIDs[identity.BackendIdentity]; exists {
			return fmt.Errorf("%w: duplicate backend identity", ErrMigrationOperationInvalid)
		}
		controlIDs[identity.ControlIdentity] = struct{}{}
		backendIDs[identity.BackendIdentity] = struct{}{}
	}
	return nil
}

func (operation MigrationOperation) validateDestinationDiskIdentities() error {
	if operation.Kind == MigrationOperationExport {
		return nil
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		if len(operation.DestinationDiskIdentities) != 0 {
			return fmt.Errorf("%w: config import has destination disk identities", ErrMigrationOperationInvalid)
		}
		return nil
	}
	diskRefs := make(map[migration.OpaqueID]struct{})
	for _, object := range operation.ImportObjects {
		for _, diskID := range object.DiskRefs {
			diskRefs[diskID] = struct{}{}
		}
	}
	if len(operation.DestinationDiskIdentities) != len(diskRefs) ||
		len(operation.DestinationDiskIdentities) == 0 {
		return fmt.Errorf("%w: destination disk identity cardinality", ErrMigrationOperationInvalid)
	}
	environmentIdentities := make(map[migration.OpaqueID]struct{}, len(operation.DestinationIdentities))
	for _, identity := range operation.DestinationIdentities {
		environmentIdentities[identity.BackendIdentity] = struct{}{}
	}
	backendIdentities := make(map[migration.OpaqueID]struct{}, len(operation.DestinationDiskIdentities))
	var previous migration.OpaqueID
	for _, identity := range operation.DestinationDiskIdentities {
		_, selected := diskRefs[identity.DiskID]
		_, environmentIdentity := environmentIdentities[identity.BackendIdentity]
		if !selected || !migrationValidOperationOpaqueID(identity.BackendIdentity) ||
			(identity.Role != migration.DiskRoleRoot && identity.Role != migration.DiskRoleAttached) ||
			(identity.Role == migration.DiskRoleRoot) != environmentIdentity ||
			(previous != "" && previous >= identity.DiskID) {
			return fmt.Errorf("%w: destination disk identity binding", ErrMigrationOperationInvalid)
		}
		if _, exists := backendIdentities[identity.BackendIdentity]; exists {
			return fmt.Errorf("%w: duplicate destination disk identity", ErrMigrationOperationInvalid)
		}
		backendIdentities[identity.BackendIdentity] = struct{}{}
		previous = identity.DiskID
	}
	return nil
}

func (operation MigrationOperation) validateDestinationStage() error {
	if operation.Kind == MigrationOperationExport {
		if operation.DestinationStage != nil {
			return fmt.Errorf("%w: export has a destination stage", ErrMigrationOperationInvalid)
		}
		return nil
	}
	var stageEffect *MigrationEffect
	for index := range operation.Effects {
		if operation.Effects[index].Kind == MigrationEffectStage {
			if stageEffect != nil {
				return fmt.Errorf("%w: duplicate destination stage effect", ErrMigrationOperationInvalid)
			}
			stageEffect = &operation.Effects[index]
		}
	}
	if stageEffect == nil {
		return fmt.Errorf("%w: destination stage effect is absent", ErrMigrationOperationInvalid)
	}
	requiresState := stageEffect.Status == MigrationEffectSucceeded
	if operation.DestinationStage == nil {
		if requiresState {
			return fmt.Errorf("%w: completed destination stage state is absent", ErrMigrationOperationInvalid)
		}
		return nil
	}
	statePermitted := requiresState || stageEffect.Status == MigrationEffectCompensating ||
		stageEffect.Status == MigrationEffectCompensated
	if !statePermitted || operation.DestinationStage.Validate() != nil {
		return fmt.Errorf("%w: destination stage state/status mismatch", ErrMigrationOperationInvalid)
	}
	if validateMigrationMaterializedProfiles(
		operation.EnvironmentActions, operation.DestinationStage.Profiles,
	) != nil {
		return fmt.Errorf("%w: destination stage profiles", ErrMigrationOperationInvalid)
	}
	if validateMigrationMaterializedProfileStates(
		operation.ID, operation.ImportObjects, operation.EnvironmentActions,
		operation.DestinationStage.ProfileStates,
	) != nil {
		return fmt.Errorf("%w: destination stage profile states", ErrMigrationOperationInvalid)
	}
	return nil
}

func cloneMigrationDestinationStage(
	stage *MigrationDestinationStageState,
) *MigrationDestinationStageState {
	if stage == nil {
		return nil
	}
	cloned := *stage
	cloned.ObjectHandles = append([]migration.OpaqueID(nil), stage.ObjectHandles...)
	cloned.Checkpoints = append(
		[]MigrationDestinationStageCheckpoint(nil), stage.Checkpoints...,
	)
	cloned.Profiles = cloneMigrationMaterializedProfiles(stage.Profiles)
	cloned.ProfileStates = append(
		[]MigrationMaterializedProfileState(nil), stage.ProfileStates...,
	)
	return &cloned
}

func cloneMigrationMaterializedProfiles(
	profiles []MigrationMaterializedProfile,
) []MigrationMaterializedProfile {
	cloned := make([]MigrationMaterializedProfile, len(profiles))
	for index, materialized := range profiles {
		cloned[index] = materialized
		cloned[index].Snapshot = materialized.Snapshot.Clone()
	}
	return cloned
}

func (operation MigrationOperation) validateDestinationAdoption() error {
	if operation.Kind == MigrationOperationExport {
		return nil
	}
	var adoptionEffect *MigrationEffect
	for index := range operation.Effects {
		if operation.Effects[index].Kind == MigrationEffectAdopt {
			if adoptionEffect != nil {
				return fmt.Errorf("%w: duplicate destination adoption effect", ErrMigrationOperationInvalid)
			}
			adoptionEffect = &operation.Effects[index]
		}
	}
	if adoptionEffect == nil {
		return fmt.Errorf("%w: destination adoption effect is absent", ErrMigrationOperationInvalid)
	}
	if migrationImportObjectsConfigOnly(operation.ImportObjects) {
		if operation.DestinationAdoption != nil {
			return fmt.Errorf("%w: config import has destination adoption", ErrMigrationOperationInvalid)
		}
		if adoptionEffect.Status == MigrationEffectSucceeded {
			if len(adoptionEffect.Evidence) != 1 ||
				adoptionEffect.Evidence[0].Code != "migration.import.config_identity_fresh" ||
				operation.DestinationStage == nil ||
				adoptionEffect.Evidence[0].OpaqueRef != operation.DestinationStage.StageHandle ||
				adoptionEffect.Evidence[0].Count != uint64(len(operation.ImportObjects)) {
				return fmt.Errorf("%w: config identity evidence is absent", ErrMigrationOperationInvalid)
			}
		}
		return nil
	}
	requiresState := adoptionEffect.Status == MigrationEffectSucceeded
	if operation.DestinationAdoption == nil {
		if requiresState {
			return fmt.Errorf("%w: completed destination adoption state is absent", ErrMigrationOperationInvalid)
		}
		return nil
	}
	statePermitted := requiresState || adoptionEffect.Status == MigrationEffectCompensating ||
		adoptionEffect.Status == MigrationEffectCompensated
	state := operation.DestinationAdoption
	if !statePermitted || state.Validate() != nil || operation.DestinationStage == nil ||
		state.StageHandle != operation.DestinationStage.StageHandle ||
		len(state.Records) != len(operation.IdentityActions) {
		return fmt.Errorf("%w: destination adoption state/status mismatch", ErrMigrationOperationInvalid)
	}
	for index, record := range state.Records {
		action := operation.IdentityActions[index]
		source := operation.SourceGuestIdentities[index]
		if record.EnvironmentRef != action.SourceRef || source.SourceRef != action.SourceRef ||
			record.Request.OperationID != migration.OpaqueID(operation.ID) ||
			record.Request.Policy != action.GuestPolicy ||
			!record.Request.SourceIdentity.Equal(source.Evidence) ||
			operation.AdoptionHelper == nil || record.Request.Helper != *operation.AdoptionHelper {
			return fmt.Errorf("%w: destination adoption immutable binding", ErrMigrationOperationInvalid)
		}
	}
	return nil
}

// validateDestinationVerification makes a successful read-only verification
// self-describing in the durable effect ledger. The proof's success bits are
// fixed by backend.DestinationProof.Validate; its provider digest and exact
// stage binding are retained here so replay does not need bundle secrets or an
// inspection-cache entry.
func (operation MigrationOperation) validateDestinationVerification() error {
	if operation.Kind == MigrationOperationExport {
		return nil
	}
	var verifyEffect *MigrationEffect
	for index := range operation.Effects {
		if operation.Effects[index].Kind == MigrationEffectVerify {
			if verifyEffect != nil {
				return fmt.Errorf("%w: duplicate destination verification effect", ErrMigrationOperationInvalid)
			}
			verifyEffect = &operation.Effects[index]
		}
	}
	if verifyEffect == nil {
		return fmt.Errorf("%w: destination verification effect is absent", ErrMigrationOperationInvalid)
	}
	if verifyEffect.Status != MigrationEffectSucceeded {
		return nil
	}
	if operation.DestinationStage == nil || len(verifyEffect.Evidence) != 1 {
		return fmt.Errorf("%w: destination verification proof is absent", ErrMigrationOperationInvalid)
	}
	evidence := verifyEffect.Evidence[0]
	if evidence.Code != migrationDestinationVerificationEvidenceCode ||
		evidence.OpaqueRef != operation.DestinationStage.StageHandle ||
		evidence.Digest.Validate() != nil ||
		evidence.Count != uint64(len(operation.ExpectedDisks)) ||
		evidence.ObservedAt.Before(operation.CreatedAt) ||
		evidence.ObservedAt.After(operation.UpdatedAt) {
		return fmt.Errorf("%w: destination verification proof binding", ErrMigrationOperationInvalid)
	}
	return nil
}

func (operation MigrationOperation) validateDestinationActivation() error {
	if operation.Kind == MigrationOperationExport {
		return nil
	}
	var activation *MigrationEffect
	for index := range operation.Effects {
		if operation.Effects[index].Kind != MigrationEffectActivate {
			continue
		}
		if activation != nil {
			return fmt.Errorf("%w: duplicate destination activation effect", ErrMigrationOperationInvalid)
		}
		activation = &operation.Effects[index]
	}
	if activation == nil {
		return fmt.Errorf("%w: destination activation effect is absent", ErrMigrationOperationInvalid)
	}
	if activation.Status != MigrationEffectSucceeded {
		if len(activation.Evidence) != 0 {
			return fmt.Errorf("%w: unfinished destination activation has evidence", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if operation.DestinationStage == nil || len(activation.Evidence) != 1 {
		return fmt.Errorf("%w: destination activation proof is absent", ErrMigrationOperationInvalid)
	}
	evidence := activation.Evidence[0]
	if evidence.Code != migrationDestinationActivationEvidenceCode ||
		evidence.OpaqueRef != operation.DestinationStage.StageHandle ||
		evidence.Digest.Validate() != nil ||
		evidence.Count != uint64(len(operation.ImportObjects)) ||
		evidence.ObservedAt.Before(operation.CreatedAt) ||
		evidence.ObservedAt.After(operation.UpdatedAt) {
		return fmt.Errorf("%w: destination activation proof binding", ErrMigrationOperationInvalid)
	}
	return nil
}

func (operation MigrationOperation) validateImportEffectProgression() error {
	if operation.Kind == MigrationOperationExport {
		return nil
	}
	effects := make(map[MigrationEffectKind]MigrationEffect, 5)
	for _, effect := range operation.Effects {
		switch effect.Kind {
		case MigrationEffectStage, MigrationEffectPrepareSecret, MigrationEffectAdopt,
			MigrationEffectVerify, MigrationEffectActivate:
			if _, duplicate := effects[effect.Kind]; duplicate {
				return fmt.Errorf("%w: duplicate import effect kind", ErrMigrationOperationInvalid)
			}
			effects[effect.Kind] = effect
		}
	}
	for _, kind := range []MigrationEffectKind{
		MigrationEffectStage, MigrationEffectAdopt,
		MigrationEffectVerify, MigrationEffectActivate,
	} {
		if _, exists := effects[kind]; !exists {
			return fmt.Errorf("%w: required import effect is absent", ErrMigrationOperationInvalid)
		}
	}
	hasImportedSecretValues := migrationHasImportedSecretValues(operation.SecretActions)
	_, hasPrepareSecret := effects[MigrationEffectPrepareSecret]
	if hasImportedSecretValues != hasPrepareSecret {
		return fmt.Errorf("%w: prepare-secret effect closure", ErrMigrationOperationInvalid)
	}
	requireSucceeded := func(kind MigrationEffectKind) error {
		if effects[kind].Status != MigrationEffectSucceeded {
			return fmt.Errorf("%w: import phase lacks prior effect proof", ErrMigrationOperationInvalid)
		}
		return nil
	}
	switch operation.Phase {
	case MigrationPhasePreparingSecrets:
		if err := requireSucceeded(MigrationEffectStage); err != nil {
			return err
		}
	case MigrationPhaseAdopting:
		if err := requireSucceeded(MigrationEffectStage); err != nil {
			return err
		}
		if hasPrepareSecret {
			if err := requireSucceeded(MigrationEffectPrepareSecret); err != nil {
				return err
			}
		}
	case MigrationPhaseVerifying:
		if err := requireSucceeded(MigrationEffectStage); err != nil {
			return err
		}
		if err := requireSucceeded(MigrationEffectAdopt); err != nil {
			return err
		}
		if hasPrepareSecret {
			if err := requireSucceeded(MigrationEffectPrepareSecret); err != nil {
				return err
			}
		}
	case MigrationPhaseCommitting, MigrationPhaseComplete:
		for _, kind := range []MigrationEffectKind{
			MigrationEffectStage, MigrationEffectAdopt, MigrationEffectVerify,
		} {
			if err := requireSucceeded(kind); err != nil {
				return err
			}
		}
		if hasPrepareSecret {
			if err := requireSucceeded(MigrationEffectPrepareSecret); err != nil {
				return err
			}
		}
	case MigrationPhaseRolledBack:
		kinds := []MigrationEffectKind{
			MigrationEffectStage, MigrationEffectAdopt,
			MigrationEffectVerify, MigrationEffectActivate,
		}
		if hasPrepareSecret {
			kinds = append(kinds, MigrationEffectPrepareSecret)
		}
		for _, kind := range kinds {
			status := effects[kind].Status
			if status != MigrationEffectPending && status != MigrationEffectCompensated {
				return fmt.Errorf("%w: rolled-back effect is not compensated", ErrMigrationOperationInvalid)
			}
		}
	}
	return nil
}

func cloneMigrationSourceGuestIdentities(
	identities []MigrationSourceGuestIdentity,
) []MigrationSourceGuestIdentity {
	cloned := make([]MigrationSourceGuestIdentity, len(identities))
	for index, identity := range identities {
		cloned[index] = identity
		cloned[index].Evidence.SSHHostKeyDigests = append(
			[]migration.Digest(nil), identity.Evidence.SSHHostKeyDigests...,
		)
	}
	return cloned
}

func cloneMigrationDiskObjects(disks []migration.DiskObject) []migration.DiskObject {
	cloned := make([]migration.DiskObject, len(disks))
	for index, disk := range disks {
		cloned[index] = disk
		if disk.Provider.Features != nil {
			cloned[index].Provider.Features = make([]string, len(disk.Provider.Features))
			copy(cloned[index].Provider.Features, disk.Provider.Features)
		}
	}
	return cloned
}

func migrationDiskObjectSlicesEqual(left, right []migration.DiskObject) bool {
	return reflect.DeepEqual(left, right)
}

func migrationSourceGuestIdentitiesEqual(
	left, right []MigrationSourceGuestIdentity,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].SourceRef != right[index].SourceRef ||
			!left[index].Evidence.Equal(right[index].Evidence) {
			return false
		}
	}
	return true
}

func migrationHelperBindingsEqual(left, right *migration.HelperBinding) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneMigrationDestinationAdoption(
	state *MigrationDestinationAdoptionState,
) *MigrationDestinationAdoptionState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Records = make([]MigrationDestinationAdoptionRecord, len(state.Records))
	for index, record := range state.Records {
		cloned.Records[index] = record
		cloned.Records[index].Request.SourceIdentity.SSHHostKeyDigests = append(
			[]migration.Digest(nil), record.Request.SourceIdentity.SSHHostKeyDigests...,
		)
		cloned.Records[index].Request.DestinationSSHKeys = append(
			[]string(nil), record.Request.DestinationSSHKeys...,
		)
		cloned.Records[index].Request.PermittedActions = append(
			[]string(nil), record.Request.PermittedActions...,
		)
		cloned.Records[index].Receipt.ActionResults = append(
			[]migration.AdoptionActionResult(nil), record.Receipt.ActionResults...,
		)
		if record.Receipt.PostIdentity != nil {
			post := *record.Receipt.PostIdentity
			post.SSHHostKeyDigests = append(
				[]migration.Digest(nil), record.Receipt.PostIdentity.SSHHostKeyDigests...,
			)
			cloned.Records[index].Receipt.PostIdentity = &post
		}
	}
	return &cloned
}

func migrationValidOperationOpaqueID(value migration.OpaqueID) bool {
	_, err := migration.ParseOpaqueID(string(value))
	return err == nil
}

func validMigrationAbsolutePath(value string) bool {
	return boundedMigrationString(value, 4096) && filepath.IsAbs(value) &&
		filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (operation MigrationOperation) validateDecision() error {
	if operation.Kind == MigrationOperationExport {
		if operation.Decision != nil {
			return fmt.Errorf("%w: export has import decision", ErrMigrationOperationInvalid)
		}
		return nil
	}
	if operation.Decision == nil {
		if operation.Phase == MigrationPhaseCommitting ||
			operation.Phase == MigrationPhaseRollingBack ||
			operation.Phase == MigrationPhaseComplete ||
			operation.Phase == MigrationPhaseRolledBack {
			return fmt.Errorf("%w: decision-required phase", ErrMigrationOperationInvalid)
		}
		return nil
	}
	decision := operation.Decision
	if decision.PlanDigest != operation.PlanDigest || decision.OperationRevision == 0 ||
		decision.OperationRevision >= operation.Revision || !validMigrationTime(decision.DecidedAt) ||
		decision.DecidedAt.Before(operation.CreatedAt) {
		return fmt.Errorf("%w: commit decision binding", ErrMigrationOperationInvalid)
	}
	switch decision.Value {
	case MigrationDecisionCommit:
		if operation.Phase != MigrationPhaseCommitting &&
			operation.Phase != MigrationPhaseRecoverableFailure &&
			operation.Phase != MigrationPhaseComplete {
			return fmt.Errorf("%w: commit decision phase", ErrMigrationOperationInvalid)
		}
	case MigrationDecisionRollback:
		if operation.Phase != MigrationPhaseRollingBack &&
			operation.Phase != MigrationPhaseRecoverableFailure &&
			operation.Phase != MigrationPhaseRolledBack &&
			operation.Phase != MigrationPhaseFailed {
			return fmt.Errorf("%w: rollback decision phase", ErrMigrationOperationInvalid)
		}
	default:
		return fmt.Errorf("%w: decision value", ErrMigrationOperationInvalid)
	}
	return nil
}

func (operation MigrationOperation) validateCancellation() error {
	if operation.Cancellation == nil {
		if operation.Phase == MigrationPhaseCancelling ||
			operation.Phase == MigrationPhaseCancelled || operation.Progress.CancelPending {
			return fmt.Errorf("%w: cancellation decision is absent", ErrMigrationOperationInvalid)
		}
		return nil
	}
	cancellation := operation.Cancellation
	if cancellation.OperationRevision == 0 ||
		cancellation.OperationRevision >= operation.Revision ||
		!validMigrationTime(cancellation.RequestedAt) ||
		cancellation.RequestedAt.Before(operation.CreatedAt) ||
		cancellation.RequestedAt.After(operation.UpdatedAt) ||
		(operation.Kind == MigrationOperationImport && cancellation.RetainPartial) {
		return fmt.Errorf("%w: cancellation decision binding", ErrMigrationOperationInvalid)
	}
	allowed := false
	if operation.Kind == MigrationOperationExport {
		allowed = operation.Phase == MigrationPhaseCancelling ||
			operation.Phase == MigrationPhaseRecoverableFailure ||
			operation.Phase == MigrationPhaseCancelled || operation.Phase == MigrationPhaseFailed
	} else {
		allowed = operation.Phase == MigrationPhaseCancelling ||
			operation.Phase == MigrationPhaseRollingBack ||
			operation.Phase == MigrationPhaseRecoverableFailure ||
			operation.Phase == MigrationPhaseRolledBack || operation.Phase == MigrationPhaseFailed
	}
	if !allowed || (migrationPhaseTerminal(operation.Phase) && operation.Progress.CancelPending) {
		return fmt.Errorf("%w: cancellation phase", ErrMigrationOperationInvalid)
	}
	return nil
}

func validateMigrationBaseRevisions(revisions []migration.BaseRevision) error {
	if len(revisions) == 0 || len(revisions) > 256 {
		return fmt.Errorf("%w: base revisions", ErrMigrationOperationInvalid)
	}
	previous := ""
	for _, revision := range revisions {
		if !boundedMigrationString(revision.Resource, 256) || revision.Revision == 0 ||
			revision.Digest.Validate() != nil ||
			(previous != "" && previous >= revision.Resource) {
			return fmt.Errorf("%w: base revision", ErrMigrationOperationInvalid)
		}
		previous = revision.Resource
	}
	return nil
}

func migrationClaimDigest(class MigrationClaimClass, key string) migration.Digest {
	digest := sha256.Sum256([]byte("hideout-migration-claim/v1\x00" + string(class) + "\x00" + key))
	return migration.Digest(fmt.Sprintf("sha256:%x", digest[:]))
}

func migrationClaimLess(left, right MigrationClaim) bool {
	return left.Class < right.Class || left.Class == right.Class && left.Key < right.Key
}

func validMigrationClaimClass(class MigrationClaimClass) bool {
	switch class {
	case MigrationClaimSourceEnvironment, MigrationClaimSourceDisk,
		MigrationClaimOutputPath, MigrationClaimDestinationName,
		MigrationClaimDestinationProfile,
		MigrationClaimDestinationControl, MigrationClaimDestinationWorkspace,
		MigrationClaimBackendObject, MigrationClaimSecretDestination,
		MigrationClaimStagingRoot:
		return true
	default:
		return false
	}
}

func validMigrationClaimKey(class MigrationClaimClass, key string) bool {
	if !boundedMigrationString(key, 4096) {
		return false
	}
	switch class {
	case MigrationClaimOutputPath, MigrationClaimStagingRoot,
		MigrationClaimDestinationWorkspace:
		return filepath.IsAbs(key) && filepath.Clean(key) == key
	case MigrationClaimDestinationName:
		return key == canonicalMigrationClaimKey(class, key) &&
			migrationDestinationNamePattern.MatchString(key)
	case MigrationClaimDestinationProfile:
		return key == canonicalMigrationClaimKey(class, key) &&
			profile.ValidateName(key) == nil
	case MigrationClaimSourceEnvironment, MigrationClaimSourceDisk,
		MigrationClaimDestinationControl, MigrationClaimBackendObject:
		_, err := migration.ParseOpaqueID(key)
		return err == nil
	case MigrationClaimSecretDestination:
		return evidenceCodePattern.MatchString(key)
	default:
		return false
	}
}

func canonicalMigrationClaimKey(class MigrationClaimClass, key string) string {
	switch class {
	case MigrationClaimDestinationName, MigrationClaimDestinationProfile:
		return strings.ToLower(key)
	default:
		return key
	}
}

func validMigrationOperationKind(kind MigrationOperationKind) bool {
	return kind == MigrationOperationExport || kind == MigrationOperationImport
}

func validMigrationPhase(kind MigrationOperationKind, phase MigrationPhase) bool {
	common := phase == MigrationPhaseDraft || phase == MigrationPhaseValidating ||
		phase == MigrationPhaseAwaitingConfirmation || phase == MigrationPhaseClaiming ||
		phase == MigrationPhaseCancelling || phase == MigrationPhaseRecoverableFailure ||
		phase == MigrationPhaseComplete || phase == MigrationPhaseCancelled ||
		phase == MigrationPhaseFailed
	if common {
		return true
	}
	if kind == MigrationOperationExport {
		return phase == MigrationPhaseSnapshotting || phase == MigrationPhaseWriting ||
			phase == MigrationPhaseSealing
	}
	return phase == MigrationPhaseMaterializing || phase == MigrationPhasePreparingSecrets ||
		phase == MigrationPhaseAdopting || phase == MigrationPhaseVerifying ||
		phase == MigrationPhaseCommitting || phase == MigrationPhaseRollingBack ||
		phase == MigrationPhaseRolledBack
}

func validMigrationEffectKind(kind MigrationEffectKind) bool {
	switch kind {
	case MigrationEffectSnapshot, MigrationEffectWriteBundle,
		MigrationEffectSealBundle, MigrationEffectStage,
		MigrationEffectPrepareSecret, MigrationEffectAdopt,
		MigrationEffectVerify, MigrationEffectActivate,
		MigrationEffectCleanup:
		return true
	default:
		return false
	}
}

func validMigrationEffectStatus(status MigrationEffectStatus) bool {
	switch status {
	case MigrationEffectPending, MigrationEffectRunning,
		MigrationEffectSucceeded, MigrationEffectFailed,
		MigrationEffectCompensating, MigrationEffectCompensated,
		MigrationEffectUnproved:
		return true
	default:
		return false
	}
}

func validMigrationCompensation(compensation MigrationCompensation) bool {
	switch compensation {
	case MigrationCompensationNone, MigrationCompensationReleaseSnapshot,
		MigrationCompensationRemovePartial, MigrationCompensationRollbackStage,
		MigrationCompensationDeleteSecret, MigrationCompensationRollbackAdoption,
		MigrationCompensationDeactivate:
		return true
	default:
		return false
	}
}

func validMigrationRecoveryAction(action MigrationRecoveryAction) bool {
	switch action {
	case MigrationRecoveryNone, MigrationRecoveryResume,
		MigrationRecoveryFinish, MigrationRecoveryRollback,
		MigrationRecoveryRemovePartial, MigrationRecoveryManual:
		return true
	default:
		return false
	}
}

func migrationRollbackDecisionPhase(phase MigrationPhase) bool {
	switch phase {
	case MigrationPhaseClaiming, MigrationPhaseMaterializing,
		MigrationPhasePreparingSecrets, MigrationPhaseAdopting,
		MigrationPhaseVerifying, MigrationPhaseCancelling,
		MigrationPhaseRecoverableFailure:
		return true
	default:
		return false
	}
}

func migrationPhaseTerminal(phase MigrationPhase) bool {
	return phase == MigrationPhaseComplete || phase == MigrationPhaseCancelled ||
		phase == MigrationPhaseRolledBack || phase == MigrationPhaseFailed
}

func migrationPhaseTransitionAllowed(
	kind MigrationOperationKind,
	from, to MigrationPhase,
) bool {
	if from == to {
		return true
	}
	switch from {
	case MigrationPhaseDraft:
		return to == MigrationPhaseValidating || to == MigrationPhaseCancelled
	case MigrationPhaseValidating:
		return to == MigrationPhaseAwaitingConfirmation ||
			to == MigrationPhaseFailed
	case MigrationPhaseAwaitingConfirmation:
		return to == MigrationPhaseClaiming || to == MigrationPhaseCancelled
	case MigrationPhaseClaiming:
		if to == MigrationPhaseCancelling || to == MigrationPhaseRecoverableFailure ||
			to == MigrationPhaseFailed {
			return true
		}
		return kind == MigrationOperationExport && to == MigrationPhaseSnapshotting ||
			kind == MigrationOperationImport && to == MigrationPhaseMaterializing
	case MigrationPhaseSnapshotting:
		return kind == MigrationOperationExport &&
			(to == MigrationPhaseWriting || migrationExecutionExit(to))
	case MigrationPhaseWriting:
		return kind == MigrationOperationExport &&
			(to == MigrationPhaseSealing || migrationExecutionExit(to))
	case MigrationPhaseSealing:
		return kind == MigrationOperationExport &&
			(to == MigrationPhaseComplete || to == MigrationPhaseRecoverableFailure ||
				to == MigrationPhaseFailed)
	case MigrationPhaseMaterializing:
		return kind == MigrationOperationImport &&
			(to == MigrationPhasePreparingSecrets || migrationExecutionExit(to))
	case MigrationPhasePreparingSecrets:
		return kind == MigrationOperationImport &&
			(to == MigrationPhaseAdopting || migrationExecutionExit(to))
	case MigrationPhaseAdopting:
		return kind == MigrationOperationImport &&
			(to == MigrationPhaseVerifying || migrationExecutionExit(to))
	case MigrationPhaseVerifying:
		return kind == MigrationOperationImport &&
			(to == MigrationPhaseCommitting || to == MigrationPhaseRollingBack ||
				migrationExecutionExit(to))
	case MigrationPhaseCommitting:
		return kind == MigrationOperationImport &&
			(to == MigrationPhaseComplete || to == MigrationPhaseRecoverableFailure ||
				to == MigrationPhaseFailed)
	case MigrationPhaseCancelling:
		return kind == MigrationOperationExport && to == MigrationPhaseCancelled ||
			kind == MigrationOperationImport && to == MigrationPhaseRollingBack ||
			to == MigrationPhaseRecoverableFailure
	case MigrationPhaseRollingBack:
		return kind == MigrationOperationImport &&
			(to == MigrationPhaseRolledBack || to == MigrationPhaseRecoverableFailure ||
				to == MigrationPhaseFailed)
	case MigrationPhaseRecoverableFailure:
		if to == MigrationPhaseFailed || to == MigrationPhaseCancelling {
			return true
		}
		if kind == MigrationOperationExport {
			return to == MigrationPhaseSnapshotting || to == MigrationPhaseWriting ||
				to == MigrationPhaseSealing
		}
		return to == MigrationPhaseMaterializing || to == MigrationPhasePreparingSecrets ||
			to == MigrationPhaseAdopting || to == MigrationPhaseVerifying ||
			to == MigrationPhaseCommitting || to == MigrationPhaseRollingBack
	default:
		return false
	}
}

func migrationExecutionExit(to MigrationPhase) bool {
	return to == MigrationPhaseCancelling || to == MigrationPhaseRecoverableFailure ||
		to == MigrationPhaseFailed
}

func validMigrationTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func boundedMigrationString(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum &&
		!strings.ContainsAny(value, "\x00\r\n")
}
