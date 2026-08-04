package manager

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
	"golang.org/x/sys/unix"
)

const (
	migrationClaimSchema                  = "hideout.migration-claim/v1"
	migrationDestinationNamespaceSchema   = "hideout.migration-destination-namespace/v1"
	maxMigrationOperationBytes            = 1 << 20
	maxMigrationClaimBytes                = 16 << 10
	maxMigrationDestinationNamespaceBytes = 4 << 10
	maxMigrationTerminalEvidenceBytes     = 128 << 10
)

var (
	ErrMigrationClaimConflict = errors.New("migration resource is claimed by another operation")
	ErrMigrationStoreRevision = errors.New("migration operation revision changed")
	migrationStoreLocks       sync.Map
)

type MigrationStore struct {
	Root string
	Now  func() time.Time

	// afterClaimWrite is a deterministic crash cut used only by package tests.
	afterClaimWrite func(int, MigrationClaim) error
}

type migrationClaimRecord struct {
	Schema      string              `json:"schema"`
	Class       MigrationClaimClass `json:"class"`
	Key         string              `json:"key"`
	KeyDigest   migration.Digest    `json:"keyDigest"`
	OperationID string              `json:"operationId"`
	PlanDigest  migration.Digest    `json:"planDigest"`
	AcquiredAt  time.Time           `json:"acquiredAt"`
}

// migrationDestinationNamespaceRecord supplies destination-local entropy for
// operation and identity derivation. It belongs to the destination store, not
// to any export bundle, so importing one bundle into independent stores cannot
// reproduce Hideout-owned identities even with identical client inputs.
type migrationDestinationNamespaceRecord struct {
	Schema    string             `json:"schema"`
	Namespace migration.OpaqueID `json:"namespace"`
}

func (store MigrationStore) DestinationNamespace() (migration.OpaqueID, error) {
	var namespace migration.OpaqueID
	err := store.withLock(func() error {
		if err := store.ensureDirectories(); err != nil {
			return err
		}
		path := filepath.Join(store.Root, "migration", "destination-namespace.json")
		var record migrationDestinationNamespaceRecord
		if err := readPrivateJSON(path, maxMigrationDestinationNamespaceBytes, &record); err == nil {
			if err := validateMigrationDestinationNamespace(record); err != nil {
				return err
			}
			namespace = record.Namespace
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		stateExists, err := store.destinationStateExistsWithoutNamespace()
		if err != nil {
			return err
		}
		if stateExists {
			return errors.New(
				"migration destination namespace is missing while migration state exists; explicit recovery is required",
			)
		}

		var raw [18]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return err
		}
		record = migrationDestinationNamespaceRecord{
			Schema: migrationDestinationNamespaceSchema,
			Namespace: migration.OpaqueID(
				"migdst_" + hex.EncodeToString(raw[:]),
			),
		}
		if err := validateMigrationDestinationNamespace(record); err != nil {
			return err
		}
		if err := writePrivateJSONAtomic(
			path, ".destination-namespace-*.tmp",
			maxMigrationDestinationNamespaceBytes, record,
		); err != nil {
			return err
		}
		namespace = record.Namespace
		return nil
	})
	return namespace, err
}

func (store MigrationStore) destinationStateExistsWithoutNamespace() (bool, error) {
	migrationRoot := filepath.Join(store.Root, "migration")
	entries, err := os.ReadDir(migrationRoot)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "operations", "claims", "receipts":
			children, err := os.ReadDir(filepath.Join(migrationRoot, entry.Name()))
			if err != nil {
				return false, err
			}
			if len(children) != 0 {
				return true, nil
			}
		default:
			return true, nil
		}
	}
	return false, nil
}

func validateMigrationDestinationNamespace(
	record migrationDestinationNamespaceRecord,
) error {
	encoded := strings.TrimPrefix(string(record.Namespace), "migdst_")
	raw, decodeErr := hex.DecodeString(encoded)
	if record.Schema != migrationDestinationNamespaceSchema ||
		!strings.HasPrefix(string(record.Namespace), "migdst_") ||
		!migrationValidOperationOpaqueID(record.Namespace) || decodeErr != nil || len(raw) != 18 {
		return errors.New("migration destination namespace is invalid")
	}
	return nil
}

func (store MigrationStore) OperationPath(id string) string {
	return filepath.Join(store.Root, "migration", "operations", id+".json")
}

func (store MigrationStore) TerminalEvidencePath(id string) string {
	return filepath.Join(store.terminalEvidenceDirectory(), id+".json")
}

func (store MigrationStore) Reserve(
	requested MigrationOperation,
) (MigrationOperation, bool, error) {
	if err := requested.Validate(); err != nil {
		return MigrationOperation{}, false, err
	}
	var operation MigrationOperation
	created := false
	err := store.withLock(func() error {
		if err := store.ensureDirectories(); err != nil {
			return err
		}
		existing, err := store.loadOperationUnlocked(requested.ID)
		if err == nil {
			if !existing.MatchesImmutable(requested) {
				return ErrMigrationOperationMismatch
			}
			operation = existing
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := validateFreshMigrationOperation(requested); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(requested); err != nil {
			return err
		}
		operation = requested.Clone()
		created = true
		return nil
	})
	return operation, created, err
}

func (store MigrationStore) Load(id string) (MigrationOperation, error) {
	if !operationIDPattern.MatchString(id) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	err := store.withLock(func() error {
		var err error
		operation, err = store.loadOperationUnlocked(id)
		return err
	})
	return operation, err
}

// List returns at most limit validated operation snapshots, newest first. A
// malformed or unsafe ledger entry fails the complete read instead of being
// silently omitted from operator history.
func (store MigrationStore) List(limit int) ([]MigrationOperation, error) {
	if limit <= 0 || limit > 4096 {
		return nil, ErrMigrationRequestInvalid
	}
	var operations []MigrationOperation
	err := store.withLock(func() error {
		if err := store.ensureDirectories(); err != nil {
			return err
		}
		entries, err := os.ReadDir(filepath.Join(store.Root, "migration", "operations"))
		if err != nil {
			return err
		}
		operations = make([]MigrationOperation, 0, min(len(entries), limit))
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				return ErrMigrationOperationInvalid
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			operation, err := store.loadOperationUnlocked(id)
			if err != nil {
				return err
			}
			operations = append(operations, operation)
		}
		slices.SortFunc(operations, func(left, right MigrationOperation) int {
			if order := right.UpdatedAt.Compare(left.UpdatedAt); order != 0 {
				return order
			}
			return strings.Compare(right.ID, left.ID)
		})
		if len(operations) > limit {
			operations = operations[:limit]
		}
		return nil
	})
	return operations, err
}

// EnsureTerminalEvidence creates exactly one immutable receipt/audit record for
// a terminal operation. Replays validate byte-level semantics against the
// authoritative operation and never append a duplicate event after restart.
func (store MigrationStore) EnsureTerminalEvidence(
	id string,
) (MigrationTerminalEvidence, bool, error) {
	if !operationIDPattern.MatchString(id) {
		return MigrationTerminalEvidence{}, false, ErrMigrationOperationInvalid
	}
	var evidence MigrationTerminalEvidence
	created := false
	err := store.withLock(func() error {
		if err := store.ensureDirectories(); err != nil {
			return err
		}
		operation, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		if !operation.Terminal() || operation.Recovery.Action != MigrationRecoveryNone {
			return ErrMigrationOperationInvalid
		}
		expected, err := BuildMigrationTerminalEvidence(operation)
		if err != nil {
			return err
		}
		path := store.TerminalEvidencePath(id)
		var existing MigrationTerminalEvidence
		if err := readPrivateJSON(
			path, maxMigrationTerminalEvidenceBytes, &existing,
		); err == nil {
			if existing.Validate() != nil || !reflect.DeepEqual(existing, expected) {
				return ErrMigrationOperationMismatch
			}
			evidence = existing
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writePrivateJSONAtomic(
			path, ".migration-terminal-evidence-*.tmp",
			maxMigrationTerminalEvidenceBytes, expected,
		); err != nil {
			return err
		}
		evidence = expected
		created = true
		return nil
	})
	return evidence, created, err
}

func (store MigrationStore) LoadTerminalEvidence(
	id string,
) (MigrationTerminalEvidence, error) {
	if !operationIDPattern.MatchString(id) {
		return MigrationTerminalEvidence{}, ErrMigrationOperationInvalid
	}
	var evidence MigrationTerminalEvidence
	err := store.withLock(func() error {
		if err := readPrivateJSON(
			store.TerminalEvidencePath(id), maxMigrationTerminalEvidenceBytes, &evidence,
		); err != nil {
			return err
		}
		if evidence.Validate() != nil || evidence.Receipt.OperationID != id {
			return ErrMigrationOperationInvalid
		}
		return nil
	})
	return evidence, err
}

// AcquireClaims converges a deterministic claim prefix after interruption. All
// conflicts are checked before creating a new claim; a crash-written prefix is
// owned by this operation and is completed on replay, never stolen.
func (store MigrationStore) AcquireClaims(
	id string,
) (MigrationOperation, bool, error) {
	var operation MigrationOperation
	changed := false
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return fmt.Errorf("load claim owner operation: %w", err)
		}
		if current.Phase != MigrationPhaseClaiming || current.Decision != nil {
			return fmt.Errorf(
				"%w: claims require claiming phase without decision",
				ErrMigrationOperationInvalid,
			)
		}
		for _, claim := range current.Claims {
			if claim.State == MigrationClaimReleased {
				return ErrMigrationOperationInvalid
			}
			record, err := store.loadClaimUnlocked(claim)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read migration claim preflight: %w", err)
			}
			if record.OperationID == current.ID && record.PlanDigest == current.PlanDigest {
				continue
			}
			released, err := store.claimOwnerReleased(record)
			if err != nil {
				return fmt.Errorf("inspect migration claim owner: %w", err)
			}
			if released {
				if err := os.Remove(store.claimPath(claim)); err != nil &&
					!errors.Is(err, os.ErrNotExist) {
					return err
				}
				continue
			}
			return fmt.Errorf(
				"%w: class=%s digest=%s owner=%s",
				ErrMigrationClaimConflict, claim.Class, claim.KeyDigest, record.OperationID,
			)
		}

		now := store.nextTime(current.UpdatedAt)
		records := make(map[migration.Digest]migrationClaimRecord, len(current.Claims))
		for index, claim := range current.Claims {
			record, err := store.loadClaimUnlocked(claim)
			if errors.Is(err, os.ErrNotExist) {
				record = migrationClaimRecord{
					Schema: migrationClaimSchema, Class: claim.Class,
					Key: claim.Key, KeyDigest: claim.KeyDigest,
					OperationID: current.ID, PlanDigest: current.PlanDigest,
					AcquiredAt: now,
				}
				if err := store.writeClaimExclusive(record); err != nil {
					return err
				}
				changed = true
				if store.afterClaimWrite != nil {
					if err := store.afterClaimWrite(index, claim); err != nil {
						return err
					}
				}
			} else if err != nil {
				return fmt.Errorf("read migration claim during acquire: %w", err)
			}
			records[claim.KeyDigest] = record
		}
		for index := range current.Claims {
			if current.Claims[index].State != MigrationClaimHeld {
				current.Claims[index].State = MigrationClaimHeld
				current.Claims[index].AcquiredAt = records[current.Claims[index].KeyDigest].AcquiredAt
				current.Claims[index].ReleasedAt = time.Time{}
				changed = true
			}
		}
		if changed {
			current.Revision++
			current.UpdatedAt = now
			if err := store.writeOperationUnlocked(current); err != nil {
				return err
			}
		}
		operation = current
		return nil
	})
	return operation, changed, err
}

// ReleaseClaims first records the non-owner state, then removes claim files.
// A crash can therefore leave a conservative stale file, but never a second
// durable operation that also claims the same resource as held.
func (store MigrationStore) ReleaseClaims(
	id string,
) (MigrationOperation, bool, error) {
	var operation MigrationOperation
	changed := false
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		now := store.nextTime(current.UpdatedAt)
		for index := range current.Claims {
			if current.Claims[index].State == MigrationClaimReleased {
				continue
			}
			current.Claims[index].State = MigrationClaimReleased
			current.Claims[index].ReleasedAt = now
			changed = true
		}
		if changed {
			current.Revision++
			current.UpdatedAt = now
			if err := store.writeOperationUnlocked(current); err != nil {
				return err
			}
		}
		operation = current

		removed := false
		for _, claim := range current.Claims {
			record, err := store.loadClaimUnlocked(claim)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if record.OperationID != current.ID || record.PlanDigest != current.PlanDigest {
				return fmt.Errorf("%w: release owner mismatch", ErrMigrationClaimConflict)
			}
			if err := os.Remove(store.claimPath(claim)); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return err
			}
			removed = true
		}
		if removed {
			return syncOperationDirectory(store.claimDirectory())
		}
		return nil
	})
	return operation, changed, err
}

func (store MigrationStore) TransitionPhase(
	id string,
	next MigrationPhase,
	result *MigrationOperationResult,
) (MigrationOperation, error) {
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		if current.Phase == next {
			operation = current
			return nil
		}
		if !migrationPhaseTransitionAllowed(current.Kind, current.Phase, next) {
			return ErrMigrationOperationInvalid
		}
		at := store.nextTime(current.UpdatedAt)
		current.Phase = next
		current.Result = cloneMigrationResult(result)
		current.Recovery = migrationRecoveryForState(current)
		current.Progress = migrationProgressForPhaseTransition(current.Progress, at)
		current.Revision++
		current.UpdatedAt = at
		if err := current.Validate(); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

// RequestCancellation atomically binds the operator's explicit cleanup choice
// to the revision they inspected. It never performs provider or filesystem I/O;
// daemon workers stop at a safe boundary and finish the persisted decision.
func (store MigrationStore) RequestCancellation(
	id string,
	expectedRevision uint64,
	retainPartial *bool,
) (MigrationOperation, error) {
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		if expectedRevision == 0 || current.Revision != expectedRevision {
			return ErrMigrationStoreRevision
		}
		if current.Terminal() || current.Cancellation != nil ||
			!migrationPhaseTransitionAllowed(
				current.Kind, current.Phase, MigrationPhaseCancelling,
			) {
			return ErrMigrationOperationInvalid
		}
		retain := false
		if current.Kind == MigrationOperationExport {
			if retainPartial == nil {
				return ErrMigrationRequestInvalid
			}
			retain = *retainPartial
		} else {
			if retainPartial != nil ||
				(current.Decision != nil && current.Decision.Value == MigrationDecisionCommit) {
				return ErrMigrationRequestInvalid
			}
		}
		at := store.nextTime(current.UpdatedAt)
		current.Cancellation = &MigrationCancellationDecision{
			RetainPartial: retain, OperationRevision: current.Revision, RequestedAt: at,
		}
		current.Phase = MigrationPhaseCancelling
		current.Result = nil
		current.Recovery = migrationRecoveryForState(current)
		current.Progress = migrationProgressForPhaseTransition(current.Progress, at)
		current.Progress.CancelPending = true
		current.Revision++
		current.UpdatedAt = at
		if err := current.Validate(); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

// UpdateProgress records a monotonic checkpoint under the same process and
// filesystem lock as effects and phase transitions. Replaying an identical
// checkpoint is a no-op.
func (store MigrationStore) UpdateProgress(
	id string,
	progress MigrationProgress,
) (MigrationOperation, bool, error) {
	var operation MigrationOperation
	changed := false
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		next, didChange, err := current.WithProgress(
			progress, store.nextTime(current.UpdatedAt),
		)
		if err != nil {
			return err
		}
		if didChange {
			if err := store.writeOperationUnlocked(next); err != nil {
				return err
			}
		}
		operation = next
		changed = didChange
		return nil
	})
	return operation, changed, err
}

// CompleteRetainedExportPartialRemoval closes the terminal cleanup action after
// the caller has proved that the exact operation-owned partial is absent. This
// is intentionally separate from ordinary progress updates: terminal progress
// is immutable except for retiring this one durable retained-byte obligation.
func (store MigrationStore) CompleteRetainedExportPartialRemoval(
	id string,
	expectedRevision uint64,
) (MigrationOperation, error) {
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		if expectedRevision == 0 || current.Revision != expectedRevision {
			return ErrMigrationStoreRevision
		}
		if current.Kind != MigrationOperationExport ||
			current.Phase != MigrationPhaseCancelled || current.Cancellation == nil ||
			!current.Cancellation.RetainPartial || current.Progress.RetainedBytes == 0 ||
			current.Recovery.Action != MigrationRecoveryRemovePartial {
			return ErrMigrationOperationInvalid
		}
		at := store.nextTime(current.UpdatedAt)
		current.Progress.RetainedBytes = 0
		current.Recovery = migrationRecoveryForState(current)
		current.Revision++
		current.UpdatedAt = at
		if err := current.Validate(); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

func (store MigrationStore) BeginEffect(
	id string,
	effectID migration.OpaqueID,
	provider string,
) (MigrationOperation, bool, error) {
	var operation MigrationOperation
	execute := false
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Provider != provider ||
			migrationPhaseTerminal(current.Phase) {
			return ErrMigrationOperationInvalid
		}
		if current.Effects[index].Status != MigrationEffectPending {
			operation = current
			return nil
		}
		current.Effects[index].Status = MigrationEffectRunning
		current.Revision++
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		execute = true
		return nil
	})
	return operation, execute, err
}

func (store MigrationStore) FinishEffect(
	id string,
	effectID migration.OpaqueID,
	provider string,
	status MigrationEffectStatus,
	evidence []MigrationEffectEvidence,
) (MigrationOperation, error) {
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Provider != provider ||
			migrationPhaseTerminal(current.Phase) {
			return ErrMigrationOperationInvalid
		}
		if (current.Effects[index].Kind == MigrationEffectStage ||
			current.Effects[index].Kind == MigrationEffectAdopt) &&
			status == MigrationEffectSucceeded &&
			!(current.Effects[index].Kind == MigrationEffectAdopt &&
				migrationImportObjectsConfigOnly(current.ImportObjects)) {
			return ErrMigrationOperationInvalid
		}
		for _, item := range evidence {
			if err := item.Validate(); err != nil {
				return err
			}
		}
		if current.Effects[index].Status == status {
			if slices.Equal(current.Effects[index].Evidence, evidence) {
				operation = current
				return nil
			}
			return ErrMigrationOperationMismatch
		}
		if !migrationEffectTransitionAllowed(current.Effects[index].Status, status) {
			return ErrMigrationOperationInvalid
		}
		current.Effects[index].Status = status
		current.Effects[index].Evidence = append([]MigrationEffectEvidence(nil), evidence...)
		current.Revision++
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

// FinishExportSeal atomically binds the already-authenticated sealed artifact,
// the final seal effect, terminal progress, and the completion receipt. A crash
// before this write leaves a recoverable sealing operation; a replay may
// authenticate the exclusive output and converge on the same binding.
func (store MigrationStore) FinishExportSeal(
	id string,
	effectID migration.OpaqueID,
	binding MigrationOperationBundleBinding,
	evidence MigrationEffectEvidence,
	encodedBytes uint64,
) (MigrationOperation, error) {
	if binding.Validate(true) != nil || evidence.Validate() != nil ||
		evidence.Code != "migration.export.bundle_sealed" ||
		evidence.Digest != binding.ManifestDigest || encodedBytes == 0 {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if current.Kind != MigrationOperationExport || index < 0 ||
			current.Effects[index].Kind != MigrationEffectSealBundle ||
			current.Effects[index].Provider != "manager" ||
			current.Bundle.BundleID != binding.BundleID ||
			current.Bundle.FormatVersion != binding.FormatVersion {
			return ErrMigrationOperationInvalid
		}
		if current.Phase == MigrationPhaseComplete {
			if current.Bundle == binding &&
				current.Effects[index].Status == MigrationEffectSucceeded &&
				slices.Equal(current.Effects[index].Evidence, []MigrationEffectEvidence{evidence}) {
				operation = current
				return nil
			}
			return ErrMigrationOperationMismatch
		}
		if current.Phase != MigrationPhaseSealing ||
			current.Effects[index].Status != MigrationEffectRunning {
			return ErrMigrationOperationInvalid
		}
		for effectIndex, effect := range current.Effects {
			if effectIndex != index && effect.Status != MigrationEffectSucceeded {
				return ErrMigrationOperationInvalid
			}
		}
		at := store.nextTime(current.UpdatedAt)
		current.Bundle = binding
		current.Effects[index].Status = MigrationEffectSucceeded
		current.Effects[index].Evidence = []MigrationEffectEvidence{evidence}
		current.Phase = MigrationPhaseComplete
		current.Progress = migrationProgressForPhaseTransition(current.Progress, at)
		current.Progress.CompletedLogicalBytes = current.Progress.TotalLogicalBytes
		current.Progress.ComponentsComplete = current.Progress.ComponentsTotal
		current.Progress.CompletedEncodedBytes = encodedBytes
		current.Progress.TotalEncodedBytes = encodedBytes
		current.Progress.EncodedTotalKnown = true
		current.Progress.CheckpointAt = at
		current.Result = &MigrationOperationResult{
			Code:          "migration.export.complete",
			ReceiptDigest: binding.CompletionDigest,
		}
		current.Recovery = migrationRecoveryForState(current)
		current.Revision++
		current.UpdatedAt = at
		if err := current.Validate(); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

// FinishDestinationStage atomically persists the exact rollback handles and
// component checkpoints with the successful stage effect. There is no durable
// state in which the effect is complete but its owned objects are unknown.
func (store MigrationStore) FinishDestinationStage(
	id string,
	effectID migration.OpaqueID,
	provider string,
	stage MigrationDestinationStageState,
	evidence []MigrationEffectEvidence,
) (MigrationOperation, error) {
	if err := stage.Validate(); err != nil || len(evidence) != 1 ||
		evidence[0].Validate() != nil || evidence[0].OpaqueRef != stage.StageHandle ||
		evidence[0].Digest != stage.EvidenceDigest ||
		evidence[0].Count != uint64(len(stage.Checkpoints)+len(stage.Profiles)) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Kind != MigrationEffectStage ||
			current.Effects[index].Provider != provider || migrationPhaseTerminal(current.Phase) {
			return ErrMigrationOperationInvalid
		}
		if current.Effects[index].Status == MigrationEffectSucceeded {
			if migrationDestinationStageStatesEqual(current.DestinationStage, &stage) &&
				slices.Equal(current.Effects[index].Evidence, evidence) {
				operation = current
				return nil
			}
			return ErrMigrationOperationMismatch
		}
		if current.Effects[index].Status != MigrationEffectRunning ||
			current.DestinationStage != nil {
			return ErrMigrationOperationInvalid
		}
		current.Effects[index].Status = MigrationEffectSucceeded
		current.Effects[index].Evidence = append([]MigrationEffectEvidence(nil), evidence...)
		current.DestinationStage = cloneMigrationDestinationStage(&stage)
		current.Revision++
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := current.Validate(); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

func migrationDestinationStageStatesEqual(
	left, right *MigrationDestinationStageState,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.StageHandle == right.StageHandle &&
		left.EvidenceDigest == right.EvidenceDigest &&
		slices.Equal(left.ObjectHandles, right.ObjectHandles) &&
		slices.Equal(left.Checkpoints, right.Checkpoints) &&
		reflect.DeepEqual(left.Profiles, right.Profiles)
}

// FinishPreparedSecrets atomically binds destination-provider generations to
// the successful prepare-secret effect. Plaintext and plaintext-derived
// digests never enter the operation ledger.
func (store MigrationStore) FinishPreparedSecrets(
	id string,
	effectID migration.OpaqueID,
	prepared []MigrationPreparedSecret,
	evidence MigrationEffectEvidence,
) (MigrationOperation, error) {
	if len(prepared) == 0 || len(prepared) > 256 || evidence.Validate() != nil ||
		evidence.Code != "migration.import.secrets_prepared" ||
		evidence.Count != uint64(len(prepared)) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	for index, value := range prepared {
		if value.Validate() != nil ||
			(index > 0 && prepared[index-1].SourceRef >= value.SourceRef) {
			return MigrationOperation{}, ErrMigrationOperationInvalid
		}
	}
	digest, err := CanonicalDigest("migration-import-prepared-secrets", prepared)
	if err != nil || evidence.Digest != migration.Digest(digest) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	err = store.withLock(func() error {
		current, loadErr := store.loadOperationUnlocked(id)
		if loadErr != nil {
			return loadErr
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Kind != MigrationEffectPrepareSecret ||
			current.Effects[index].Provider != "manager" ||
			migrationPhaseTerminal(current.Phase) {
			return ErrMigrationOperationInvalid
		}
		if current.Effects[index].Status == MigrationEffectSucceeded {
			if slices.Equal(current.PreparedSecrets, prepared) &&
				slices.Equal(current.Effects[index].Evidence, []MigrationEffectEvidence{evidence}) {
				operation = current
				return nil
			}
			return ErrMigrationOperationMismatch
		}
		if current.Effects[index].Status != MigrationEffectRunning ||
			len(current.PreparedSecrets) != 0 {
			return ErrMigrationOperationInvalid
		}
		current.Effects[index].Status = MigrationEffectSucceeded
		current.Effects[index].Evidence = []MigrationEffectEvidence{evidence}
		current.PreparedSecrets = append([]MigrationPreparedSecret(nil), prepared...)
		current.Revision++
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if validateErr := current.Validate(); validateErr != nil {
			return validateErr
		}
		if writeErr := store.writeOperationUnlocked(current); writeErr != nil {
			return writeErr
		}
		operation = current
		return nil
	})
	return operation, err
}

func (store MigrationStore) FinishPreparedSecretCompensation(
	id string,
	effectID migration.OpaqueID,
	prepared []MigrationPreparedSecret,
	evidence MigrationEffectEvidence,
) (MigrationOperation, error) {
	if len(prepared) > 256 || evidence.Validate() != nil ||
		evidence.Code != "migration.import.secrets_rolled_back" ||
		evidence.Count != uint64(len(prepared)) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	for index, value := range prepared {
		if value.Validate() != nil ||
			(index > 0 && prepared[index-1].SourceRef >= value.SourceRef) {
			return MigrationOperation{}, ErrMigrationOperationInvalid
		}
	}
	digest, err := CanonicalDigest("migration-import-secret-rollback", prepared)
	if err != nil || evidence.Digest != migration.Digest(digest) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	err = store.withLock(func() error {
		current, loadErr := store.loadOperationUnlocked(id)
		if loadErr != nil {
			return loadErr
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Kind != MigrationEffectPrepareSecret ||
			current.Effects[index].Provider != "manager" ||
			current.Phase != MigrationPhaseRollingBack {
			return ErrMigrationOperationInvalid
		}
		if current.Effects[index].Status == MigrationEffectCompensated {
			if slices.Equal(current.PreparedSecrets, prepared) &&
				slices.Equal(current.Effects[index].Evidence, []MigrationEffectEvidence{evidence}) {
				operation = current
				return nil
			}
			return ErrMigrationOperationMismatch
		}
		if current.Effects[index].Status != MigrationEffectCompensating {
			return ErrMigrationOperationInvalid
		}
		current.Effects[index].Status = MigrationEffectCompensated
		current.Effects[index].Evidence = []MigrationEffectEvidence{evidence}
		current.PreparedSecrets = append([]MigrationPreparedSecret(nil), prepared...)
		current.Revision++
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if validateErr := current.Validate(); validateErr != nil {
			return validateErr
		}
		if writeErr := store.writeOperationUnlocked(current); writeErr != nil {
			return writeErr
		}
		operation = current
		return nil
	})
	return operation, err
}

// FinishDestinationAdoption atomically binds every provider-generated guest
// request and matching receipt to the successful adoption effect. A crash may
// therefore replay the provider, but can never leave a durable successful
// effect whose nonces, helper, policy outcome, or cleanup proof are unknown.
func (store MigrationStore) FinishDestinationAdoption(
	id string,
	effectID migration.OpaqueID,
	provider string,
	state MigrationDestinationAdoptionState,
	evidence []MigrationEffectEvidence,
) (MigrationOperation, error) {
	if err := state.Validate(); err != nil || len(evidence) != 1 ||
		evidence[0].Validate() != nil || evidence[0].OpaqueRef != state.StageHandle ||
		evidence[0].Digest != state.EvidenceDigest ||
		evidence[0].Count != uint64(len(state.Records)) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Kind != MigrationEffectAdopt ||
			current.Effects[index].Provider != provider || migrationPhaseTerminal(current.Phase) ||
			current.DestinationStage == nil ||
			current.DestinationStage.StageHandle != state.StageHandle {
			return ErrMigrationOperationInvalid
		}
		if current.Effects[index].Status == MigrationEffectSucceeded {
			if reflect.DeepEqual(current.DestinationAdoption, &state) &&
				slices.Equal(current.Effects[index].Evidence, evidence) {
				operation = current
				return nil
			}
			return ErrMigrationOperationMismatch
		}
		if current.Effects[index].Status != MigrationEffectRunning ||
			current.DestinationAdoption != nil {
			return ErrMigrationOperationInvalid
		}
		current.Effects[index].Status = MigrationEffectSucceeded
		current.Effects[index].Evidence = append([]MigrationEffectEvidence(nil), evidence...)
		current.DestinationAdoption = cloneMigrationDestinationAdoption(&state)
		current.Revision++
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := current.Validate(); err != nil {
			return err
		}
		if err := store.writeOperationUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

func (store MigrationStore) BeginEffectCompensation(
	id string,
	effectID migration.OpaqueID,
	provider string,
) (MigrationOperation, bool, error) {
	var operation MigrationOperation
	execute := false
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		index := migrationEffectIndex(current.Effects, effectID)
		if index < 0 || current.Effects[index].Provider != provider ||
			current.Phase != MigrationPhaseRollingBack {
			return ErrMigrationOperationInvalid
		}
		switch current.Effects[index].Status {
		case MigrationEffectRunning, MigrationEffectSucceeded,
			MigrationEffectFailed, MigrationEffectUnproved:
			current.Effects[index].Status = MigrationEffectCompensating
			current.Revision++
			current.UpdatedAt = store.nextTime(current.UpdatedAt)
			if err := store.writeOperationUnlocked(current); err != nil {
				return err
			}
			execute = true
		case MigrationEffectCompensating, MigrationEffectCompensated:
		default:
			return ErrMigrationOperationInvalid
		}
		operation = current
		return nil
	})
	return operation, execute, err
}

func (store MigrationStore) Decide(
	id string,
	decision MigrationDecision,
) (MigrationOperation, bool, error) {
	var operation MigrationOperation
	changed := false
	err := store.withLock(func() error {
		current, err := store.loadOperationUnlocked(id)
		if err != nil {
			return err
		}
		next, didChange, err := current.Decide(decision, store.nextTime(current.UpdatedAt))
		if err != nil {
			return err
		}
		if didChange {
			if err := store.writeOperationUnlocked(next); err != nil {
				return err
			}
		}
		operation = next
		changed = didChange
		return nil
	})
	return operation, changed, err
}

func (store MigrationStore) loadOperationUnlocked(id string) (MigrationOperation, error) {
	if !operationIDPattern.MatchString(id) {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	var operation MigrationOperation
	if err := readPrivateJSON(
		store.OperationPath(id), maxMigrationOperationBytes, &operation,
	); err != nil {
		return MigrationOperation{}, err
	}
	if operation.ID != id {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	if err := operation.Validate(); err != nil {
		return MigrationOperation{}, err
	}
	return operation, nil
}

func (store MigrationStore) writeOperationUnlocked(operation MigrationOperation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	return writePrivateJSONAtomic(
		store.OperationPath(operation.ID), ".migration-operation-*.tmp",
		maxMigrationOperationBytes, operation,
	)
}

func (store MigrationStore) loadClaimUnlocked(
	claim MigrationClaim,
) (migrationClaimRecord, error) {
	var record migrationClaimRecord
	if err := readPrivateJSON(
		store.claimPath(claim), maxMigrationClaimBytes, &record,
	); err != nil {
		return migrationClaimRecord{}, err
	}
	if err := record.Validate(); err != nil || record.Class != claim.Class ||
		record.Key != claim.Key || record.KeyDigest != claim.KeyDigest {
		return migrationClaimRecord{}, ErrMigrationOperationInvalid
	}
	return record, nil
}

func (record migrationClaimRecord) Validate() error {
	claim := MigrationClaim{
		Class: record.Class, Key: record.Key, KeyDigest: record.KeyDigest,
		State: MigrationClaimHeld, AcquiredAt: record.AcquiredAt,
	}
	if record.Schema != migrationClaimSchema || claim.Validate() != nil ||
		!operationIDPattern.MatchString(record.OperationID) || record.PlanDigest.Validate() != nil {
		return ErrMigrationOperationInvalid
	}
	return nil
}

func (store MigrationStore) writeClaimExclusive(record migrationClaimRecord) (err error) {
	if err := record.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxMigrationClaimBytes {
		return ErrMigrationOperationInvalid
	}
	claim := MigrationClaim{Class: record.Class, Key: record.Key, KeyDigest: record.KeyDigest}
	path := store.claimPath(claim)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, file.Close())
		}
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := writeAllManager(file, data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		return closeErr
	}
	remove = false
	return syncOperationDirectory(store.claimDirectory())
}

func (store MigrationStore) claimOwnerReleased(record migrationClaimRecord) (bool, error) {
	owner, err := store.loadOperationUnlocked(record.OperationID)
	if err != nil {
		return false, err
	}
	for _, claim := range owner.Claims {
		if claim.KeyDigest == record.KeyDigest && claim.Class == record.Class && claim.Key == record.Key {
			return claim.State == MigrationClaimReleased, nil
		}
	}
	return false, ErrMigrationOperationInvalid
}

func (store MigrationStore) claimPath(claim MigrationClaim) string {
	name := strings.TrimPrefix(string(claim.KeyDigest), "sha256:") + ".json"
	return filepath.Join(store.claimDirectory(), name)
}

func (store MigrationStore) claimDirectory() string {
	return filepath.Join(store.Root, "migration", "claims")
}

func (store MigrationStore) terminalEvidenceDirectory() string {
	return filepath.Join(store.Root, "migration", "receipts")
}

func (store MigrationStore) ensureDirectories() error {
	if strings.TrimSpace(store.Root) == "" || !filepath.IsAbs(store.Root) {
		return errors.New("migration store root must be absolute")
	}
	for _, directory := range []string{
		store.Root,
		filepath.Join(store.Root, "migration"),
		filepath.Join(store.Root, "migration", "operations"),
		store.claimDirectory(),
		store.terminalEvidenceDirectory(),
	} {
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(directory)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("migration store directory must not be a symlink")
		}
		if info.Mode().Perm() != 0o700 {
			if err := os.Chmod(directory, 0o700); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store MigrationStore) withLock(callback func() error) (err error) {
	key := filepath.Join(store.Root, ".locks", "migration", "store.lock")
	value, _ := migrationStoreLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	lock, err := openMigrationStoreLock(store.Root)
	if err != nil {
		return err
	}
	fd := int(lock.Fd())
	defer func() {
		err = errors.Join(err, unix.Flock(fd, unix.LOCK_UN), lock.Close())
	}()
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock migration store: %w", err)
	}
	return callback()
}

func openMigrationStoreLock(root string) (*os.File, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, errors.New("migration store root must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	rootFD, err := unix.Open(
		root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0,
	)
	if err != nil {
		return nil, fmt.Errorf("open migration store for lock: %w", err)
	}
	defer unix.Close(rootFD)
	locksFD, err := openOrCreateLockDirectory(rootFD, ".locks")
	if err != nil {
		return nil, err
	}
	defer unix.Close(locksFD)
	migrationFD, err := openOrCreateLockDirectory(locksFD, "migration")
	if err != nil {
		return nil, err
	}
	defer unix.Close(migrationFD)
	fd, err := unix.Openat(
		migrationFD, "store.lock",
		unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open migration store lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(root, ".locks", "migration", "store.lock"))
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open migration store lock: invalid file descriptor")
	}
	return file, nil
}

func readPrivateJSON(path string, maximum int64, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 || info.Size() > maximum {
		return errors.New("migration record must be a bounded private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("migration record contains trailing data")
	}
	return nil
}

func writePrivateJSONAtomic(
	path, pattern string,
	maximum int,
	value any,
) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maximum {
		return errors.New("migration record exceeds size bound")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return errors.New("migration record target must be a private regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keep := true
	defer func() {
		_ = temp.Close()
		if keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if err := writeAllManager(temp, data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keep = false
	return syncOperationDirectory(directory)
}

func writeAllManager(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func (store MigrationStore) now() time.Time {
	if store.Now != nil {
		return store.Now().UTC()
	}
	return time.Now().UTC()
}

func (store MigrationStore) nextTime(previous time.Time) time.Time {
	now := store.now()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}

func migrationEffectIndex(effects []MigrationEffect, id migration.OpaqueID) int {
	for index := range effects {
		if effects[index].ID == id {
			return index
		}
	}
	return -1
}

func migrationEffectTransitionAllowed(
	from, to MigrationEffectStatus,
) bool {
	if from == MigrationEffectRunning {
		return to == MigrationEffectSucceeded || to == MigrationEffectFailed ||
			to == MigrationEffectUnproved
	}
	if from == MigrationEffectCompensating {
		return to == MigrationEffectCompensated || to == MigrationEffectUnproved
	}
	return false
}

func cloneMigrationResult(result *MigrationOperationResult) *MigrationOperationResult {
	if result == nil {
		return nil
	}
	cloned := *result
	return &cloned
}

func validateFreshMigrationOperation(operation MigrationOperation) error {
	if operation.Revision != 1 || operation.Decision != nil || operation.Result != nil {
		return ErrMigrationOperationInvalid
	}
	switch operation.Phase {
	case MigrationPhaseDraft, MigrationPhaseValidating,
		MigrationPhaseAwaitingConfirmation, MigrationPhaseClaiming:
	default:
		return ErrMigrationOperationInvalid
	}
	for _, claim := range operation.Claims {
		if claim.State != MigrationClaimPending {
			return ErrMigrationOperationInvalid
		}
	}
	for _, effect := range operation.Effects {
		if effect.Status != MigrationEffectPending || len(effect.Evidence) != 0 {
			return ErrMigrationOperationInvalid
		}
	}
	return nil
}
