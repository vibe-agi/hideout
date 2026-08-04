package manager

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	MigrationOperationProjectionSchema = "hideout.migration-operation-projection/v1"
	MigrationReceiptSchema             = "hideout.migration-operation-receipt/v1"
	MigrationAuditEventSchema          = "hideout.migration-audit-event/v1"
	MigrationTerminalEvidenceSchema    = "hideout.migration-terminal-evidence/v1"
)

var (
	migrationURLUserInfoPattern = regexp.MustCompile(
		`(?i)([a-z][a-z0-9+.-]*://)([^/@\s]+)@`,
	)
	migrationSensitiveAssignmentPattern = regexp.MustCompile(
		`(?i)\b(passphrase|password|privatekey|secretinputhandle|authorization)\s*[:=]\s*[^\s&;]+`,
	)
	migrationAbsolutePathPattern = regexp.MustCompile(
		`(^|[\s("'=])/(?:[^ \t\r\n"'<>]+)`,
	)
	migrationWindowsPathPattern = regexp.MustCompile(
		`(^|[\s("'=])[A-Za-z]:\\(?:[^ \t\r\n"'<>]+)`,
	)
)

type MigrationProgressProjection struct {
	LogicalTotalKnown        bool      `json:"logicalTotalKnown"`
	CompletedLogicalBytes    uint64    `json:"completedLogicalBytes"`
	TotalLogicalBytes        uint64    `json:"totalLogicalBytes,omitempty"`
	EncodedTotalKnown        bool      `json:"encodedTotalKnown"`
	CompletedEncodedBytes    uint64    `json:"completedEncodedBytes"`
	TotalEncodedBytes        uint64    `json:"totalEncodedBytes,omitempty"`
	ComponentsComplete       uint32    `json:"componentsComplete"`
	ComponentsTotal          uint32    `json:"componentsTotal,omitempty"`
	CurrentItem              string    `json:"currentItem,omitempty"`
	PhaseStartedAt           time.Time `json:"phaseStartedAt,omitempty"`
	CheckpointAt             time.Time `json:"checkpointAt,omitempty"`
	ElapsedSeconds           uint64    `json:"elapsedSeconds"`
	ThroughputBytesPerSecond uint64    `json:"throughputBytesPerSecond,omitempty"`
	RemainingKnown           bool      `json:"remainingKnown"`
	RemainingSeconds         uint64    `json:"remainingSeconds,omitempty"`
	CancelPending            bool      `json:"cancelPending"`
	RetainedBytes            uint64    `json:"retainedBytes,omitempty"`
}

type MigrationRecoveryProjection struct {
	Required       bool                      `json:"required"`
	Code           string                    `json:"code,omitempty"`
	AllowedActions []MigrationRecoveryAction `json:"allowedActions"`
	NextAction     string                    `json:"nextAction,omitempty"`
}

type MigrationEffectProjection struct {
	Kind   MigrationEffectKind   `json:"kind"`
	Status MigrationEffectStatus `json:"status"`
}

type MigrationIdentityPolicyCounts struct {
	SafeClone         uint32 `json:"safeClone"`
	ExactGuestRestore uint32 `json:"exactGuestRestore"`
	FreshControl      uint32 `json:"freshControl"`
	FreshBackend      uint32 `json:"freshBackend"`
}

type MigrationOperationProjection struct {
	Schema           string                        `json:"schema"`
	OperationID      string                        `json:"operationId"`
	Revision         uint64                        `json:"revision"`
	BundleID         migration.BundleID            `json:"bundleId"`
	Kind             MigrationOperationKind        `json:"kind"`
	State            MigrationPhase                `json:"state"`
	PhaseLabel       string                        `json:"phaseLabel"`
	Progress         MigrationProgressProjection   `json:"progress"`
	Recovery         MigrationRecoveryProjection   `json:"recovery"`
	Warnings         []MigrationNotice             `json:"warnings"`
	Effects          []MigrationEffectProjection   `json:"effects"`
	IdentityPolicies MigrationIdentityPolicyCounts `json:"identityPolicies"`
	Decision         MigrationDecision             `json:"decision,omitempty"`
	LastErrorCode    string                        `json:"lastErrorCode,omitempty"`
	TerminalReceipt  *MigrationReceipt             `json:"terminalReceipt,omitempty"`
}

// ProjectMigrationOperation is the only presentation projection for CLI, TUI,
// WebUI, and API callers. It deliberately omits provider names, claim keys,
// paths, plan digests, effect IDs, and provider evidence.
func ProjectMigrationOperation(
	operation MigrationOperation,
	now time.Time,
) (MigrationOperationProjection, error) {
	if err := operation.Validate(); err != nil {
		return MigrationOperationProjection{}, err
	}
	if !validMigrationTime(now) || now.Before(operation.UpdatedAt) {
		return MigrationOperationProjection{}, ErrMigrationOperationInvalid
	}
	progress := projectMigrationProgress(operation, now)
	recovery := MigrationRecoveryProjection{
		Required:       operation.Recovery.Action != MigrationRecoveryNone,
		Code:           operation.Recovery.Code,
		AllowedActions: []MigrationRecoveryAction{},
	}
	if recovery.Required {
		recovery.AllowedActions = append(recovery.AllowedActions, operation.Recovery.Action)
		recovery.NextAction = migrationRecoveryNextAction(operation.Recovery.Action)
	}
	warnings := make([]MigrationNotice, len(operation.Warnings))
	for index, warning := range operation.Warnings {
		warnings[index] = MigrationNotice{
			Code: warning.Code, Summary: redactMigrationText(warning.Summary),
		}
	}
	effects := make([]MigrationEffectProjection, len(operation.Effects))
	for index, effect := range operation.Effects {
		effects[index] = MigrationEffectProjection{Kind: effect.Kind, Status: effect.Status}
	}
	projection := MigrationOperationProjection{
		Schema:           MigrationOperationProjectionSchema,
		OperationID:      operation.ID,
		Revision:         operation.Revision,
		BundleID:         operation.Bundle.BundleID,
		Kind:             operation.Kind,
		State:            operation.Phase,
		PhaseLabel:       migrationPhaseLabel(operation.Phase),
		Progress:         progress,
		Recovery:         recovery,
		Warnings:         warnings,
		Effects:          effects,
		IdentityPolicies: migrationIdentityPolicyCounts(operation.IdentityActions),
	}
	if operation.Decision != nil {
		projection.Decision = operation.Decision.Value
	}
	if operation.Phase == MigrationPhaseRecoverableFailure ||
		operation.Phase == MigrationPhaseFailed {
		projection.LastErrorCode = operation.Recovery.Code
		if operation.Result != nil {
			projection.LastErrorCode = operation.Result.Code
		}
	}
	return projection, projection.Validate()
}

// ProjectStoredMigrationOperation adds the standard durable terminal receipt
// to the shared presentation projection. Snapshot, CLI, TUI, WebUI, and the
// direct migration API all use this boundary so no surface invents a terminal
// result from phase text alone.
func ProjectStoredMigrationOperation(
	store MigrationStore,
	operation MigrationOperation,
	now time.Time,
) (MigrationOperationProjection, error) {
	projection, err := ProjectMigrationOperation(operation, now)
	if err != nil {
		return MigrationOperationProjection{}, err
	}
	if !operation.Terminal() || operation.Recovery.Action != MigrationRecoveryNone {
		return projection, nil
	}
	evidence, _, err := store.EnsureTerminalEvidence(operation.ID)
	if err != nil {
		return MigrationOperationProjection{}, err
	}
	receipt := evidence.Receipt
	projection.TerminalReceipt = &receipt
	return projection, projection.Validate()
}

// Validate checks the closed, redacted wire shape consumed by every operator
// surface. It intentionally does not accept provider names, paths, claim keys,
// effect IDs, or plan digests because those fields do not belong in this
// projection.
func (projection MigrationOperationProjection) Validate() error {
	if projection.Schema != MigrationOperationProjectionSchema ||
		!operationIDPattern.MatchString(projection.OperationID) ||
		projection.Revision == 0 ||
		projection.BundleID == "" ||
		!validMigrationOperationKind(projection.Kind) ||
		!validMigrationPhase(projection.Kind, projection.State) ||
		projection.PhaseLabel != migrationPhaseLabel(projection.State) ||
		len(projection.Warnings) > 256 || len(projection.Effects) > 256 ||
		(projection.Decision != "" &&
			projection.Decision != MigrationDecisionCommit &&
			projection.Decision != MigrationDecisionRollback) ||
		(projection.LastErrorCode != "" &&
			!migrationOperationCodePattern.MatchString(projection.LastErrorCode)) {
		return ErrMigrationOperationInvalid
	}
	if _, err := migration.ParseBundleID(string(projection.BundleID)); err != nil {
		return ErrMigrationOperationInvalid
	}
	progress := projection.Progress
	encodedMaximum := migration.HardMaxLogicalBytes +
		uint64(migration.HardMaxPayloadRecords)*uint64(migration.HardMaxRecordOverhead)
	if progress.CompletedLogicalBytes > migration.HardMaxLogicalBytes ||
		progress.TotalLogicalBytes > migration.HardMaxLogicalBytes ||
		progress.CompletedEncodedBytes > encodedMaximum ||
		progress.TotalEncodedBytes > encodedMaximum ||
		progress.RetainedBytes > encodedMaximum ||
		uint64(progress.ComponentsComplete) > migration.HardMaxPayloadRecords ||
		uint64(progress.ComponentsTotal) > migration.HardMaxPayloadRecords ||
		!progress.LogicalTotalKnown && progress.TotalLogicalBytes != 0 ||
		progress.LogicalTotalKnown &&
			progress.CompletedLogicalBytes > progress.TotalLogicalBytes ||
		!progress.EncodedTotalKnown && progress.TotalEncodedBytes != 0 ||
		progress.EncodedTotalKnown &&
			progress.CompletedEncodedBytes > progress.TotalEncodedBytes ||
		progress.ComponentsTotal != 0 &&
			progress.ComponentsComplete > progress.ComponentsTotal ||
		!progress.RemainingKnown && progress.RemainingSeconds != 0 ||
		(progress.CurrentItem != "" &&
			(!boundedMigrationDisplayText(progress.CurrentItem, 512) ||
				progress.CurrentItem != redactMigrationText(progress.CurrentItem))) ||
		(!progress.PhaseStartedAt.IsZero() &&
			!validMigrationTime(progress.PhaseStartedAt)) ||
		(!progress.CheckpointAt.IsZero() &&
			!validMigrationTime(progress.CheckpointAt)) {
		return ErrMigrationProgressInvalid
	}
	recovery := projection.Recovery
	if recovery.Required {
		if len(recovery.AllowedActions) != 1 ||
			!validMigrationRecoveryAction(recovery.AllowedActions[0]) ||
			recovery.AllowedActions[0] == MigrationRecoveryNone ||
			!migrationOperationCodePattern.MatchString(recovery.Code) ||
			recovery.NextAction != migrationRecoveryNextAction(recovery.AllowedActions[0]) {
			return ErrMigrationOperationInvalid
		}
	} else if !migrationOperationCodePattern.MatchString(recovery.Code) ||
		len(recovery.AllowedActions) != 0 || recovery.NextAction != "" {
		return ErrMigrationOperationInvalid
	}
	for _, warning := range projection.Warnings {
		if err := warning.Validate(); err != nil {
			return err
		}
	}
	for _, effect := range projection.Effects {
		if !validMigrationEffectKind(effect.Kind) ||
			!validMigrationEffectStatus(effect.Status) {
			return ErrMigrationOperationInvalid
		}
	}
	counts := projection.IdentityPolicies
	if uint64(counts.SafeClone)+uint64(counts.ExactGuestRestore) >
		uint64(migration.HardMaxEnvironments) ||
		uint64(counts.FreshControl) > uint64(migration.HardMaxEnvironments) ||
		uint64(counts.FreshBackend) > uint64(migration.HardMaxEnvironments) {
		return ErrMigrationOperationInvalid
	}
	if projection.TerminalReceipt != nil {
		if !migrationPhaseTerminal(projection.State) {
			return ErrMigrationOperationInvalid
		}
		if err := projection.TerminalReceipt.Validate(); err != nil ||
			projection.TerminalReceipt.OperationID != projection.OperationID ||
			projection.TerminalReceipt.BundleID != projection.BundleID ||
			projection.TerminalReceipt.Kind != projection.Kind ||
			projection.TerminalReceipt.TerminalState != projection.State {
			return ErrMigrationOperationInvalid
		}
	}
	return nil
}

func projectMigrationProgress(
	operation MigrationOperation,
	now time.Time,
) MigrationProgressProjection {
	source := operation.Progress
	phaseStartedAt := source.PhaseStartedAt
	if phaseStartedAt.IsZero() {
		phaseStartedAt = operation.UpdatedAt
	}
	active := time.Duration(source.ActiveWorkNanos)
	if !source.ActiveSince.IsZero() && !migrationPhaseTerminal(operation.Phase) &&
		now.After(source.ActiveSince) {
		active += now.Sub(source.ActiveSince)
	}
	if active < 0 {
		active = 0
	}
	elapsedSeconds := uint64(active / time.Second)
	throughput := uint64(0)
	if elapsedSeconds > 0 {
		throughput = source.CompletedLogicalBytes / elapsedSeconds
	}
	remainingKnown := source.LogicalTotalKnown &&
		(source.CompletedLogicalBytes == source.TotalLogicalBytes || throughput > 0)
	remainingSeconds := uint64(0)
	if remainingKnown && source.CompletedLogicalBytes < source.TotalLogicalBytes {
		remaining := source.TotalLogicalBytes - source.CompletedLogicalBytes
		remainingSeconds = remaining / throughput
		if remaining%throughput != 0 {
			remainingSeconds++
		}
	}
	return MigrationProgressProjection{
		LogicalTotalKnown:        source.LogicalTotalKnown,
		CompletedLogicalBytes:    source.CompletedLogicalBytes,
		TotalLogicalBytes:        source.TotalLogicalBytes,
		EncodedTotalKnown:        source.EncodedTotalKnown,
		CompletedEncodedBytes:    source.CompletedEncodedBytes,
		TotalEncodedBytes:        source.TotalEncodedBytes,
		ComponentsComplete:       source.ComponentsComplete,
		ComponentsTotal:          source.ComponentsTotal,
		CurrentItem:              redactMigrationText(source.CurrentItem),
		PhaseStartedAt:           phaseStartedAt,
		CheckpointAt:             source.CheckpointAt,
		ElapsedSeconds:           elapsedSeconds,
		ThroughputBytesPerSecond: throughput,
		RemainingKnown:           remainingKnown,
		RemainingSeconds:         remainingSeconds,
		CancelPending:            source.CancelPending,
		RetainedBytes:            source.RetainedBytes,
	}
}

type MigrationEffectCounts struct {
	Pending      uint32 `json:"pending"`
	Running      uint32 `json:"running"`
	Succeeded    uint32 `json:"succeeded"`
	Failed       uint32 `json:"failed"`
	Compensating uint32 `json:"compensating"`
	Compensated  uint32 `json:"compensated"`
	Unproved     uint32 `json:"unproved"`
}

type MigrationReceipt struct {
	Schema                       string                         `json:"schema"`
	OperationID                  string                         `json:"operationId"`
	BundleID                     migration.BundleID             `json:"bundleId"`
	Kind                         MigrationOperationKind         `json:"kind"`
	TerminalState                MigrationPhase                 `json:"terminalState"`
	ResultCode                   string                         `json:"resultCode"`
	Decision                     MigrationDecision              `json:"decision,omitempty"`
	CompletedComponents          uint32                         `json:"completedComponents"`
	TotalComponents              uint32                         `json:"totalComponents,omitempty"`
	CompletedLogical             uint64                         `json:"completedLogicalBytes"`
	Effects                      MigrationEffectCounts          `json:"effects"`
	AllEffectsSucceeded          bool                           `json:"allEffectsSucceeded"`
	IdentityPolicies             MigrationIdentityPolicyCounts  `json:"identityPolicies"`
	Replacements                 []MigrationReplacedEnvironment `json:"replacements"`
	ApprovedAuthority            []MigrationApprovedAuthority   `json:"approvedAuthority"`
	DisabledAuthorityProposalIDs []migration.OpaqueID           `json:"disabledAuthorityProposalIds"`
	ClaimsReleased               bool                           `json:"claimsReleased"`
	CompletedAt                  time.Time                      `json:"completedAt"`
}

type MigrationApprovedAuthority struct {
	ProposalID     migration.OpaqueID `json:"proposalId"`
	EnvironmentRef migration.OpaqueID `json:"environmentRef"`
	Class          string             `json:"class"`
}

type MigrationReplacedEnvironment struct {
	SourceRef            migration.OpaqueID `json:"sourceRef"`
	DestinationName      string             `json:"destinationName"`
	LifecycleOperationID string             `json:"lifecycleOperationId"`
	LifecyclePlanDigest  migration.Digest   `json:"lifecyclePlanDigest"`
}

func BuildMigrationReceipt(operation MigrationOperation) (MigrationReceipt, error) {
	if err := operation.Validate(); err != nil {
		return MigrationReceipt{}, err
	}
	if !migrationPhaseTerminal(operation.Phase) || operation.Result == nil {
		return MigrationReceipt{}, ErrMigrationOperationInvalid
	}
	effects := migrationEffectCounts(operation.Effects)
	allSucceeded := len(operation.Effects) > 0 &&
		effects.Succeeded == uint32(len(operation.Effects))
	claimsReleased := true
	for _, claim := range operation.Claims {
		if claim.State != MigrationClaimReleased {
			claimsReleased = false
			break
		}
	}
	receipt := MigrationReceipt{
		Schema:              MigrationReceiptSchema,
		OperationID:         operation.ID,
		BundleID:            operation.Bundle.BundleID,
		Kind:                operation.Kind,
		TerminalState:       operation.Phase,
		ResultCode:          operation.Result.Code,
		CompletedComponents: operation.Progress.ComponentsComplete,
		TotalComponents:     operation.Progress.ComponentsTotal,
		CompletedLogical:    operation.Progress.CompletedLogicalBytes,
		Effects:             effects,
		AllEffectsSucceeded: allSucceeded,
		IdentityPolicies:    migrationIdentityPolicyCounts(operation.IdentityActions),
		Replacements:        migrationReplacementsForReceipt(operation.ConflictActions),
		ApprovedAuthority:   migrationApprovedAuthorityForReceipt(operation.AuthorityActions),
		DisabledAuthorityProposalIDs: append(
			[]migration.OpaqueID(nil), operation.DisabledProposals...,
		),
		ClaimsReleased: claimsReleased,
		CompletedAt:    operation.UpdatedAt,
	}
	if operation.Decision != nil {
		receipt.Decision = operation.Decision.Value
	}
	if err := receipt.Validate(); err != nil {
		return MigrationReceipt{}, err
	}
	return receipt, nil
}

func (receipt MigrationReceipt) Validate() error {
	if receipt.Schema != MigrationReceiptSchema ||
		!operationIDPattern.MatchString(receipt.OperationID) ||
		!validMigrationOperationKind(receipt.Kind) ||
		!migrationPhaseTerminal(receipt.TerminalState) ||
		!migrationOperationCodePattern.MatchString(receipt.ResultCode) ||
		!validMigrationTime(receipt.CompletedAt) {
		return ErrMigrationOperationInvalid
	}
	if _, err := migration.ParseBundleID(string(receipt.BundleID)); err != nil {
		return ErrMigrationOperationInvalid
	}
	if receipt.Kind == MigrationOperationImport {
		if receipt.Decision != MigrationDecisionCommit &&
			receipt.Decision != MigrationDecisionRollback &&
			receipt.TerminalState != MigrationPhaseCancelled &&
			receipt.TerminalState != MigrationPhaseFailed {
			return ErrMigrationOperationInvalid
		}
	} else if receipt.Decision != "" {
		return ErrMigrationOperationInvalid
	}
	if err := validateMigrationReceiptAuthority(receipt); err != nil {
		return err
	}
	if err := validateMigrationReceiptReplacements(receipt); err != nil {
		return err
	}
	return nil
}

func migrationReplacementsForReceipt(
	actions []migration.ConflictAction,
) []MigrationReplacedEnvironment {
	result := make([]MigrationReplacedEnvironment, 0, len(actions))
	for _, action := range actions {
		if action.Decision != migrationConflictDecisionReplace {
			continue
		}
		result = append(result, MigrationReplacedEnvironment{
			SourceRef: action.SourceRef, DestinationName: action.DestinationName,
			LifecycleOperationID: action.LifecycleOperationID,
			LifecyclePlanDigest:  action.LifecyclePlanDigest,
		})
	}
	return result
}

func migrationApprovedAuthorityForReceipt(
	actions []migration.AuthorityAction,
) []MigrationApprovedAuthority {
	result := make([]MigrationApprovedAuthority, len(actions))
	for index, action := range actions {
		result[index] = MigrationApprovedAuthority{
			ProposalID: action.ProposalID, EnvironmentRef: action.EnvironmentRef,
			Class: action.Class,
		}
	}
	return result
}

func validateMigrationReceiptAuthority(receipt MigrationReceipt) error {
	if receipt.Kind == MigrationOperationExport &&
		(len(receipt.ApprovedAuthority) != 0 || len(receipt.DisabledAuthorityProposalIDs) != 0) {
		return ErrMigrationOperationInvalid
	}
	seen := make(map[migration.OpaqueID]struct{}, len(receipt.ApprovedAuthority)+len(receipt.DisabledAuthorityProposalIDs))
	var previous migration.OpaqueID
	for _, authority := range receipt.ApprovedAuthority {
		if !migrationValidOperationOpaqueID(authority.ProposalID) ||
			!migrationValidOperationOpaqueID(authority.EnvironmentRef) ||
			!validMigrationAuthorityClass(authority.Class) ||
			(previous != "" && previous >= authority.ProposalID) {
			return ErrMigrationOperationInvalid
		}
		seen[authority.ProposalID] = struct{}{}
		previous = authority.ProposalID
	}
	previous = ""
	for _, proposalID := range receipt.DisabledAuthorityProposalIDs {
		if !migrationValidOperationOpaqueID(proposalID) ||
			(previous != "" && previous >= proposalID) {
			return ErrMigrationOperationInvalid
		}
		if _, duplicate := seen[proposalID]; duplicate {
			return ErrMigrationOperationInvalid
		}
		seen[proposalID] = struct{}{}
		previous = proposalID
	}
	return nil
}

func validateMigrationReceiptReplacements(receipt MigrationReceipt) error {
	if receipt.Kind == MigrationOperationExport && len(receipt.Replacements) != 0 {
		return ErrMigrationOperationInvalid
	}
	var previous migration.OpaqueID
	for _, replacement := range receipt.Replacements {
		if !migrationValidOperationOpaqueID(replacement.SourceRef) ||
			(previous != "" && previous >= replacement.SourceRef) ||
			!migrationDestinationNamePattern.MatchString(replacement.DestinationName) ||
			!operationIDPattern.MatchString(replacement.LifecycleOperationID) ||
			replacement.LifecyclePlanDigest.Validate() != nil {
			return ErrMigrationOperationInvalid
		}
		previous = replacement.SourceRef
	}
	return nil
}

type MigrationAuditAction string

const (
	MigrationAuditExportCompleted  MigrationAuditAction = "migration.export.completed"
	MigrationAuditExportCancelled  MigrationAuditAction = "migration.export.cancelled"
	MigrationAuditExportFailed     MigrationAuditAction = "migration.export.failed"
	MigrationAuditImportCommitted  MigrationAuditAction = "migration.import.committed"
	MigrationAuditImportCancelled  MigrationAuditAction = "migration.import.cancelled"
	MigrationAuditImportRolledBack MigrationAuditAction = "migration.import.rolled_back"
	MigrationAuditImportFailed     MigrationAuditAction = "migration.import.failed"
)

type MigrationAuditEvent struct {
	Schema                       string                         `json:"schema"`
	Action                       MigrationAuditAction           `json:"action"`
	OperationID                  string                         `json:"operationId"`
	BundleID                     migration.BundleID             `json:"bundleId"`
	Kind                         MigrationOperationKind         `json:"kind"`
	TerminalState                MigrationPhase                 `json:"terminalState"`
	Decision                     MigrationDecision              `json:"decision,omitempty"`
	ResultCode                   string                         `json:"resultCode"`
	CompletedComponents          uint32                         `json:"completedComponents"`
	TotalComponents              uint32                         `json:"totalComponents,omitempty"`
	Effects                      MigrationEffectCounts          `json:"effects"`
	IdentityPolicies             MigrationIdentityPolicyCounts  `json:"identityPolicies"`
	Replacements                 []MigrationReplacedEnvironment `json:"replacements"`
	ApprovedAuthority            []MigrationApprovedAuthority   `json:"approvedAuthority"`
	DisabledAuthorityProposalIDs []migration.OpaqueID           `json:"disabledAuthorityProposalIds"`
	OccurredAt                   time.Time                      `json:"occurredAt"`
}

// MigrationTerminalEvidence is the single durable, secret-free terminal
// publication for one migration operation. Keeping the typed audit event and
// receipt in one private record makes restart reconciliation idempotent without
// relying on an append-log transaction across two files.
type MigrationTerminalEvidence struct {
	Schema     string              `json:"schema"`
	Receipt    MigrationReceipt    `json:"receipt"`
	AuditEvent MigrationAuditEvent `json:"auditEvent"`
}

func BuildMigrationTerminalEvidence(
	operation MigrationOperation,
) (MigrationTerminalEvidence, error) {
	receipt, err := BuildMigrationReceipt(operation)
	if err != nil {
		return MigrationTerminalEvidence{}, err
	}
	action, ok := migrationAuditActionForOperation(operation)
	if !ok {
		return MigrationTerminalEvidence{}, ErrMigrationOperationInvalid
	}
	event, err := BuildMigrationAuditEvent(operation, action, operation.UpdatedAt)
	if err != nil {
		return MigrationTerminalEvidence{}, err
	}
	evidence := MigrationTerminalEvidence{
		Schema: MigrationTerminalEvidenceSchema, Receipt: receipt, AuditEvent: event,
	}
	if err := evidence.Validate(); err != nil {
		return MigrationTerminalEvidence{}, err
	}
	return evidence, nil
}

func (evidence MigrationTerminalEvidence) Validate() error {
	receipt := evidence.Receipt
	event := evidence.AuditEvent
	if evidence.Schema != MigrationTerminalEvidenceSchema || receipt.Validate() != nil ||
		!validMigrationAuditAction(event.Action) || event.Schema != MigrationAuditEventSchema ||
		!validMigrationTime(event.OccurredAt) || event.OccurredAt != receipt.CompletedAt ||
		event.OperationID != receipt.OperationID || event.BundleID != receipt.BundleID ||
		event.Kind != receipt.Kind || event.TerminalState != receipt.TerminalState ||
		event.Decision != receipt.Decision || event.ResultCode != receipt.ResultCode ||
		event.CompletedComponents != receipt.CompletedComponents ||
		event.TotalComponents != receipt.TotalComponents || event.Effects != receipt.Effects ||
		event.IdentityPolicies != receipt.IdentityPolicies ||
		!slices.Equal(event.Replacements, receipt.Replacements) ||
		!slices.Equal(event.ApprovedAuthority, receipt.ApprovedAuthority) ||
		!slices.Equal(
			event.DisabledAuthorityProposalIDs,
			receipt.DisabledAuthorityProposalIDs,
		) ||
		!migrationAuditActionMatchesReceipt(event.Action, receipt) {
		return ErrMigrationOperationInvalid
	}
	return nil
}

func BuildMigrationAuditEvent(
	operation MigrationOperation,
	action MigrationAuditAction,
	at time.Time,
) (MigrationAuditEvent, error) {
	receipt, err := BuildMigrationReceipt(operation)
	if err != nil {
		return MigrationAuditEvent{}, err
	}
	if !validMigrationAuditAction(action) || !validMigrationTime(at) ||
		at.Before(operation.UpdatedAt) || !migrationAuditActionMatches(action, operation) {
		return MigrationAuditEvent{}, ErrMigrationOperationInvalid
	}
	event := MigrationAuditEvent{
		Schema:              MigrationAuditEventSchema,
		Action:              action,
		OperationID:         receipt.OperationID,
		BundleID:            receipt.BundleID,
		Kind:                receipt.Kind,
		TerminalState:       receipt.TerminalState,
		Decision:            receipt.Decision,
		ResultCode:          receipt.ResultCode,
		CompletedComponents: receipt.CompletedComponents,
		TotalComponents:     receipt.TotalComponents,
		Effects:             receipt.Effects,
		IdentityPolicies:    receipt.IdentityPolicies,
		Replacements: append(
			[]MigrationReplacedEnvironment(nil), receipt.Replacements...,
		),
		ApprovedAuthority: append(
			[]MigrationApprovedAuthority(nil), receipt.ApprovedAuthority...,
		),
		DisabledAuthorityProposalIDs: append(
			[]migration.OpaqueID(nil), receipt.DisabledAuthorityProposalIDs...,
		),
		OccurredAt: at,
	}
	return event, nil
}

type MigrationPublicError struct {
	Code             string `json:"code"`
	Retryable        bool   `json:"retryable"`
	RecoveryRequired bool   `json:"recoveryRequired"`
}

func ProjectMigrationError(err error) MigrationPublicError {
	if err == nil {
		return MigrationPublicError{}
	}
	var providerError *backend.MigrationProviderError
	if errors.As(err, &providerError) {
		code := providerError.Code
		if !strings.HasPrefix(code, "migration.provider.") ||
			!migrationOperationCodePattern.MatchString(code) {
			code = "migration.provider.failed"
		}
		return MigrationPublicError{
			Code: code, Retryable: providerError.Retryable,
			RecoveryRequired: providerError.RecoveryRequired,
		}
	}
	var bundleError *migration.Error
	if errors.As(err, &bundleError) {
		code := knownMigrationBundleCode(migration.CodeOf(err))
		if code == "" {
			code = migration.CodeInvalidBundle
		}
		return MigrationPublicError{
			Code: code, Retryable: bundleError.Retryable,
			RecoveryRequired: bundleError.RecoveryRequired,
		}
	}
	if code := knownMigrationBundleCode(migration.CodeOf(err)); code != "" {
		return MigrationPublicError{Code: code}
	}
	switch {
	case errors.Is(err, ErrMigrationInspectionRequired):
		return MigrationPublicError{
			Code: "migration.bundle.inspection_required", Retryable: true,
		}
	case errors.Is(err, ErrMigrationClaimConflict),
		errors.Is(err, ErrMigrationStoreRevision),
		errors.Is(err, ErrMigrationOperationMismatch),
		errors.Is(err, ErrMigrationPlanStale):
		return MigrationPublicError{Code: "migration.plan.stale", Retryable: true}
	case errors.Is(err, ErrMigrationDecisionConflict):
		return MigrationPublicError{Code: "migration.decision.conflict", RecoveryRequired: true}
	case errors.Is(err, ErrMigrationCapabilityUnavailable):
		return MigrationPublicError{Code: "migration.capability.unavailable", Retryable: true}
	case errors.Is(err, ErrMigrationConfirmationRequired):
		return MigrationPublicError{Code: "migration.confirmation.required", Retryable: true}
	case errors.Is(err, ErrMigrationSecretInputRequired):
		return MigrationPublicError{Code: "migration.secret_input.required", Retryable: true}
	case errors.Is(err, ErrMigrationSecretInputExpired):
		return MigrationPublicError{Code: "migration.secret_input.expired", Retryable: true}
	case errors.Is(err, ErrMigrationSecretInputMismatch):
		return MigrationPublicError{Code: "migration.secret_input.mismatch", Retryable: true}
	case errors.Is(err, ErrMigrationSecretInputCapacity):
		return MigrationPublicError{Code: "migration.secret_input.capacity", Retryable: true}
	case errors.Is(err, ErrMigrationOperationInvalid),
		errors.Is(err, ErrMigrationProgressInvalid),
		errors.Is(err, ErrMigrationSecretInputInvalid),
		errors.Is(err, ErrMigrationRequestInvalid),
		errors.Is(err, ErrMigrationPlanInvalid),
		errors.Is(err, backend.ErrMigrationProviderRequest),
		errors.Is(err, backend.ErrMigrationProviderResponse),
		errors.Is(err, backend.ErrMigrationProviderCapability):
		return MigrationPublicError{Code: "migration.request.invalid"}
	default:
		return MigrationPublicError{Code: "migration.operation.failed"}
	}
}

func migrationEffectCounts(effects []MigrationEffect) MigrationEffectCounts {
	var counts MigrationEffectCounts
	for _, effect := range effects {
		switch effect.Status {
		case MigrationEffectPending:
			counts.Pending++
		case MigrationEffectRunning:
			counts.Running++
		case MigrationEffectSucceeded:
			counts.Succeeded++
		case MigrationEffectFailed:
			counts.Failed++
		case MigrationEffectCompensating:
			counts.Compensating++
		case MigrationEffectCompensated:
			counts.Compensated++
		case MigrationEffectUnproved:
			counts.Unproved++
		}
	}
	return counts
}

func migrationIdentityPolicyCounts(
	actions []migration.IdentityAction,
) MigrationIdentityPolicyCounts {
	var counts MigrationIdentityPolicyCounts
	for _, action := range actions {
		switch action.GuestPolicy {
		case migration.GuestIdentitySafeClone:
			counts.SafeClone++
		case migration.GuestIdentityExactRestore:
			counts.ExactGuestRestore++
		}
		if action.FreshControlIdentity {
			counts.FreshControl++
		}
		if action.FreshBackendIdentity {
			counts.FreshBackend++
		}
	}
	return counts
}

func migrationPhaseLabel(phase MigrationPhase) string {
	switch phase {
	case MigrationPhaseDraft:
		return "Preparing migration"
	case MigrationPhaseValidating:
		return "Checking source and destination"
	case MigrationPhaseAwaitingConfirmation:
		return "Waiting for confirmation"
	case MigrationPhaseClaiming:
		return "Reserving migration resources"
	case MigrationPhaseSnapshotting:
		return "Taking a consistent snapshot"
	case MigrationPhaseWriting:
		return "Writing the portable bundle"
	case MigrationPhaseSealing:
		return "Verifying and sealing the bundle"
	case MigrationPhaseMaterializing:
		return "Copying persistent data"
	case MigrationPhasePreparingSecrets:
		return "Preparing destination secrets"
	case MigrationPhaseAdopting:
		return "Applying destination identity policy"
	case MigrationPhaseVerifying:
		return "Verifying the imported environment"
	case MigrationPhaseCommitting:
		return "Publishing the imported environment"
	case MigrationPhaseCancelling:
		return "Stopping at a safe boundary"
	case MigrationPhaseRollingBack:
		return "Removing staged import data"
	case MigrationPhaseRecoverableFailure:
		return "Waiting for recovery"
	case MigrationPhaseComplete:
		return "Migration complete"
	case MigrationPhaseCancelled:
		return "Migration cancelled"
	case MigrationPhaseRolledBack:
		return "Import rolled back"
	case MigrationPhaseFailed:
		return "Migration failed"
	default:
		return "Migration state unavailable"
	}
}

func migrationRecoveryNextAction(action MigrationRecoveryAction) string {
	switch action {
	case MigrationRecoveryResume:
		return "Resume the same migration operation."
	case MigrationRecoveryFinish:
		return "Finish publishing the verified import."
	case MigrationRecoveryRollback:
		return "Roll back the staged import."
	case MigrationRecoveryRemovePartial:
		return "Review and remove the retained partial output."
	case MigrationRecoveryManual:
		return "Run migration doctor and review the reported recovery requirement."
	default:
		return ""
	}
}

func validMigrationAuditAction(action MigrationAuditAction) bool {
	switch action {
	case MigrationAuditExportCompleted, MigrationAuditExportCancelled,
		MigrationAuditExportFailed, MigrationAuditImportCommitted,
		MigrationAuditImportCancelled, MigrationAuditImportRolledBack,
		MigrationAuditImportFailed:
		return true
	default:
		return false
	}
}

func migrationAuditActionMatches(
	action MigrationAuditAction,
	operation MigrationOperation,
) bool {
	switch action {
	case MigrationAuditExportCompleted:
		return operation.Kind == MigrationOperationExport &&
			operation.Phase == MigrationPhaseComplete
	case MigrationAuditExportCancelled:
		return operation.Kind == MigrationOperationExport &&
			operation.Phase == MigrationPhaseCancelled
	case MigrationAuditExportFailed:
		return operation.Kind == MigrationOperationExport &&
			operation.Phase == MigrationPhaseFailed
	case MigrationAuditImportCommitted:
		return operation.Kind == MigrationOperationImport &&
			operation.Phase == MigrationPhaseComplete &&
			operation.Decision != nil && operation.Decision.Value == MigrationDecisionCommit
	case MigrationAuditImportCancelled:
		return operation.Kind == MigrationOperationImport &&
			operation.Phase == MigrationPhaseCancelled
	case MigrationAuditImportRolledBack:
		return operation.Kind == MigrationOperationImport &&
			operation.Phase == MigrationPhaseRolledBack
	case MigrationAuditImportFailed:
		return operation.Kind == MigrationOperationImport &&
			operation.Phase == MigrationPhaseFailed
	default:
		return false
	}
}

func migrationAuditActionForOperation(
	operation MigrationOperation,
) (MigrationAuditAction, bool) {
	for _, action := range []MigrationAuditAction{
		MigrationAuditExportCompleted,
		MigrationAuditExportCancelled,
		MigrationAuditExportFailed,
		MigrationAuditImportCommitted,
		MigrationAuditImportCancelled,
		MigrationAuditImportRolledBack,
		MigrationAuditImportFailed,
	} {
		if migrationAuditActionMatches(action, operation) {
			return action, true
		}
	}
	return "", false
}

func migrationAuditActionMatchesReceipt(
	action MigrationAuditAction,
	receipt MigrationReceipt,
) bool {
	switch action {
	case MigrationAuditExportCompleted:
		return receipt.Kind == MigrationOperationExport &&
			receipt.TerminalState == MigrationPhaseComplete
	case MigrationAuditExportCancelled:
		return receipt.Kind == MigrationOperationExport &&
			receipt.TerminalState == MigrationPhaseCancelled
	case MigrationAuditExportFailed:
		return receipt.Kind == MigrationOperationExport &&
			receipt.TerminalState == MigrationPhaseFailed
	case MigrationAuditImportCommitted:
		return receipt.Kind == MigrationOperationImport &&
			receipt.TerminalState == MigrationPhaseComplete &&
			receipt.Decision == MigrationDecisionCommit
	case MigrationAuditImportCancelled:
		return receipt.Kind == MigrationOperationImport &&
			receipt.TerminalState == MigrationPhaseCancelled
	case MigrationAuditImportRolledBack:
		return receipt.Kind == MigrationOperationImport &&
			receipt.TerminalState == MigrationPhaseRolledBack
	case MigrationAuditImportFailed:
		return receipt.Kind == MigrationOperationImport &&
			receipt.TerminalState == MigrationPhaseFailed
	default:
		return false
	}
}

func knownMigrationBundleCode(code string) string {
	switch code {
	case migration.CodeInvalidBundle,
		migration.CodeLimitExceeded,
		migration.CodeAuthenticationFailed,
		migration.CodeUnsupportedVersion,
		migration.CodeUnsupportedRecord,
		migration.CodeIncompleteBundle,
		migration.CodeCorruptBundle,
		migration.CodeTrailingData,
		migration.CodeBundleChanged,
		migration.CodeOutputExists:
		return code
	default:
		return ""
	}
}

func redactMigrationText(value string) string {
	if value == "" {
		return ""
	}
	value = audit.RedactString(value)
	value = migrationURLUserInfoPattern.ReplaceAllString(value, `${1}REDACTED@`)
	value = migrationSensitiveAssignmentPattern.ReplaceAllStringFunc(
		value,
		func(match string) string {
			separator := strings.IndexAny(match, ":=")
			if separator < 0 {
				return "credential=REDACTED"
			}
			return "credential=REDACTED"
		},
	)
	value = migrationWindowsPathPattern.ReplaceAllString(value, `${1}[path]`)
	value = migrationAbsolutePathPattern.ReplaceAllString(value, `${1}[path]`)
	value = strings.TrimSpace(value)
	return truncateMigrationText(value, 512)
}

func truncateMigrationText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

func boundedMigrationDisplayText(value string, maximum int) bool {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if !unicode.IsPrint(character) {
			return false
		}
	}
	return true
}
