package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profilestate"
	"github.com/vibe-agi/hideout/internal/secrets"
)

var ErrMigrationExportResumeRequired = errors.New("migration export requires protected resume")

type MigrationExportWorkerRequest struct {
	OperationID       string
	Snapshot          backend.SourceSnapshot
	SecretInputHandle string
	SecretPurpose     MigrationSecretPurpose
	BundleFile        *MigrationBundleFileBinding
	ClientBinding     string
}

type MigrationExportWorkerResult struct {
	Operation  MigrationOperation
	OutputPath string
	Binding    migration.BundleBinding
}

type migrationExportProfile struct {
	record             environment.Record
	componentID        migration.OpaqueID
	encoded            []byte
	digest             migration.Digest
	stateComponentID   migration.OpaqueID
	stateSnapshot      profilestate.Snapshot
	stateDigest        migration.Digest
	guestUser          string
	authorityProposals []migration.AuthorityProposal
	authorityRefs      []migration.OpaqueID
}

type migrationExportPrepared struct {
	operation  MigrationOperation
	source     migrationExportSource
	inventory  backend.SourceInventory
	capability backend.MigrationCapabilities
	profiles   []migrationExportProfile
	secrets    []migrationExportSecret
	configOnly bool
}

// WriteAndSealExportBundle consumes one memory-only passphrase handle and
// publishes one immutable encrypted file. The provider sees only its snapshot
// component reads; it never receives a path, passphrase, key, or ciphertext.
func (service MigrationService) WriteAndSealExportBundle(
	ctx context.Context,
	request MigrationExportWorkerRequest,
) (result MigrationExportWorkerResult, resultErr error) {
	if ctx == nil || request.OperationID == "" ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) ||
		(request.SecretPurpose != MigrationSecretPurposeExportCreate &&
			request.SecretPurpose != MigrationSecretPurposeExportResume) {
		return MigrationExportWorkerResult{}, ErrMigrationRequestInvalid
	}
	prepared, err := service.prepareMigrationExport(ctx, request.OperationID, request.Snapshot)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	if prepared.operation.Phase == MigrationPhaseComplete {
		output, err := migrationExportOutputPath(prepared.operation)
		if err != nil {
			return MigrationExportWorkerResult{}, err
		}
		return MigrationExportWorkerResult{
			Operation: prepared.operation, OutputPath: output,
			Binding: migration.BundleBinding{
				BundleID:         prepared.operation.Bundle.BundleID,
				FormatVersion:    prepared.operation.Bundle.FormatVersion,
				FileDigest:       prepared.operation.Bundle.FileDigest,
				ManifestDigest:   prepared.operation.Bundle.ManifestDigest,
				CompletionDigest: prepared.operation.Bundle.CompletionDigest,
			},
		}, nil
	}
	defer func() {
		for index := range prepared.profiles {
			clear(prepared.profiles[index].encoded)
		}
		if resultErr != nil {
			service.markMigrationExportRecoverable(request.OperationID)
		}
	}()

	outputPath, err := migrationExportOutputPath(prepared.operation)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	partialPath := migrationExportPartialPath(outputPath, prepared.operation.ID)
	if err := inspectMigrationExportArtifactPaths(
		outputPath, partialPath, prepared.operation.Phase,
	); err != nil {
		return MigrationExportWorkerResult{}, err
	}
	finalExists, err := migrationExportRegularPathExists(outputPath)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	partialExists, err := migrationExportRegularPathExists(partialPath)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	recoveryAttempt := finalExists || partialExists ||
		prepared.operation.Phase == MigrationPhaseSealing ||
		prepared.operation.Phase == MigrationPhaseRecoverableFailure
	if (recoveryAttempt && request.SecretPurpose != MigrationSecretPurposeExportResume) ||
		(!recoveryAttempt && request.SecretPurpose != MigrationSecretPurposeExportCreate) {
		return MigrationExportWorkerResult{}, ErrMigrationExportResumeRequired
	}
	var secretBundleFile *MigrationBundleFileBinding
	if recoveryAttempt {
		artifactPath := partialPath
		if finalExists {
			artifactPath = outputPath
		}
		binding, err := bindMigrationExportArtifact(artifactPath)
		if err != nil {
			return MigrationExportWorkerResult{}, err
		}
		if request.BundleFile == nil || request.BundleFile.Validate() != nil ||
			*request.BundleFile != binding {
			return MigrationExportWorkerResult{}, ErrMigrationSecretInputMismatch
		}
		secretBundleFile = &binding
	} else if request.BundleFile != nil {
		return MigrationExportWorkerResult{}, ErrMigrationRequestInvalid
	}

	writeEffect, err := migrationOperationEffect(
		prepared.operation, MigrationEffectWriteBundle,
	)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	if prepared.operation.Phase == MigrationPhaseWriting &&
		writeEffect.Status == MigrationEffectPending {
		prepared.operation, _, err = service.Store.BeginEffect(
			prepared.operation.ID, writeEffect.ID, writeEffect.Provider,
		)
		if err != nil {
			return MigrationExportWorkerResult{}, err
		}
	}
	if service.SecretInputs == nil {
		return MigrationExportWorkerResult{}, ErrMigrationSecretInputRequired
	}

	var inspection migration.SealedBundleInspection
	consumeErr := service.SecretInputs.Consume(MigrationSecretInputUse{
		Handle: request.SecretInputHandle, Purpose: request.SecretPurpose,
		ClientBinding: request.ClientBinding, BundleID: prepared.operation.Bundle.BundleID,
		BundleFile: secretBundleFile,
	}, func(passphrase []byte) error {
		if _, err := os.Lstat(outputPath); err == nil {
			if prepared.operation.Phase != MigrationPhaseSealing &&
				prepared.operation.Phase != MigrationPhaseRecoverableFailure {
				return ErrMigrationOutputConflict
			}
			observed, err := inspectMigrationSealedExport(
				ctx, outputPath, passphrase, prepared.operation.Bundle.BundleID,
			)
			if err != nil {
				return err
			}
			if err := removePublishedMigrationExportPartial(partialPath, outputPath); err != nil {
				return err
			}
			inspection = observed
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Lstat(partialPath); err == nil {
			observed, err := service.resumeMigrationExport(
				ctx, prepared, request.Snapshot, outputPath, partialPath, passphrase,
			)
			if err != nil {
				return err
			}
			inspection = observed
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if prepared.operation.Phase != MigrationPhaseWriting {
			return ErrMigrationExportResumeRequired
		}
		observed, err := service.writeFreshMigrationExport(
			ctx, prepared, request.Snapshot, outputPath, partialPath, passphrase,
		)
		if err != nil {
			return err
		}
		inspection = observed
		return nil
	})
	if consumeErr != nil {
		return MigrationExportWorkerResult{}, consumeErr
	}
	if _, err := service.ensureMigrationExportSealPhase(prepared.operation.ID); err != nil {
		return MigrationExportWorkerResult{}, err
	}

	if !prepared.configOnly {
		snapshotEffect, err := migrationOperationEffect(
			prepared.operation, MigrationEffectSnapshot,
		)
		if err != nil {
			return MigrationExportWorkerResult{}, err
		}
		if service.Export == nil {
			return MigrationExportWorkerResult{}, ErrMigrationCapabilityUnavailable
		}
		if err := service.Export.ReleaseMigrationSnapshot(
			ctx, backend.SnapshotReleaseRequest{
				Binding: backend.MigrationEffectBinding{
					OperationID:        migration.OpaqueID(prepared.operation.ID),
					EffectID:           snapshotEffect.ID,
					CapabilityRevision: prepared.operation.CapabilityRevision,
				},
				SnapshotHandle: request.Snapshot.SnapshotHandle,
			},
		); err != nil {
			return MigrationExportWorkerResult{}, err
		}
	}
	operationBeforeSeal, err := service.Store.Load(prepared.operation.ID)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	sealEffect, err := migrationOperationEffect(operationBeforeSeal, MigrationEffectSealBundle)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	binding := MigrationOperationBundleBinding{
		BundleID:         inspection.Binding.BundleID,
		FormatVersion:    inspection.Binding.FormatVersion,
		FileDigest:       inspection.Binding.FileDigest,
		ManifestDigest:   inspection.Binding.ManifestDigest,
		CompletionDigest: inspection.Binding.CompletionDigest,
	}
	operation, err := service.Store.FinishExportSeal(
		prepared.operation.ID, sealEffect.ID, binding,
		MigrationEffectEvidence{
			Code:       "migration.export.bundle_sealed",
			Digest:     binding.ManifestDigest,
			Count:      inspection.Summary.RecordCount,
			ObservedAt: service.nowUTC(),
		},
		inspection.Summary.EncodedBytes,
	)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	operation, _, err = service.Store.ReleaseClaims(operation.ID)
	if err != nil {
		return MigrationExportWorkerResult{}, err
	}
	return MigrationExportWorkerResult{
		Operation: operation, OutputPath: outputPath, Binding: inspection.Binding,
	}, nil
}

func (service MigrationService) prepareMigrationExport(
	ctx context.Context,
	operationID string,
	snapshot backend.SourceSnapshot,
) (migrationExportPrepared, error) {
	operation, err := service.Store.Load(operationID)
	if err != nil {
		return migrationExportPrepared{}, err
	}
	if operation.Kind != MigrationOperationExport ||
		(operation.Phase != MigrationPhaseWriting &&
			operation.Phase != MigrationPhaseSealing &&
			operation.Phase != MigrationPhaseRecoverableFailure &&
			operation.Phase != MigrationPhaseComplete) {
		return migrationExportPrepared{}, ErrMigrationOperationInvalid
	}
	if operation.Phase == MigrationPhaseComplete {
		return migrationExportPrepared{operation: operation}, nil
	}
	snapshotEffect, err := migrationOperationEffect(operation, MigrationEffectSnapshot)
	if err != nil || snapshotEffect.Status != MigrationEffectSucceeded ||
		len(snapshotEffect.Evidence) != 1 {
		return migrationExportPrepared{}, ErrMigrationOperationInvalid
	}
	if migrationOperationConfigExport(operation) {
		if requestSnapshotNotEmpty(snapshot) {
			return migrationExportPrepared{}, ErrMigrationOperationInvalid
		}
		capability, err := service.configMigrationCapability(ctx)
		if err != nil || capability.Revision != operation.CapabilityRevision {
			return migrationExportPrepared{}, ErrMigrationPlanStale
		}
		refs := migrationOperationClaimRefs(operation.Claims, MigrationClaimSourceEnvironment)
		source, digest, err := service.resolveConfigExportRefs(refs, capability.Revision)
		if err != nil {
			return migrationExportPrepared{}, ErrMigrationPlanStale
		}
		secretEntries, secretRevisions, err := service.inspectMigrationExportSecrets(
			ctx, operation.PlanID, source, operation.SelectedSecretRefs,
		)
		if err != nil {
			return migrationExportPrepared{}, err
		}
		source.revisions = append(source.revisions, secretRevisions...)
		if !reflect.DeepEqual(source.revisions, operation.BaseRevisions) ||
			digest != operation.SourceInventoryDigest ||
			snapshotEffect.Evidence[0].Digest != digest {
			return migrationExportPrepared{}, ErrMigrationPlanStale
		}
		profiles, err := service.prepareMigrationExportProfiles(operation, source)
		if err != nil {
			return migrationExportPrepared{}, err
		}
		return migrationExportPrepared{
			operation: operation, source: source, capability: capability,
			profiles: profiles, secrets: secretEntries, configOnly: true,
		}, nil
	}
	binding := backend.MigrationEffectBinding{
		OperationID: migration.OpaqueID(operation.ID), EffectID: snapshotEffect.ID,
		CapabilityRevision: operation.CapabilityRevision,
	}
	if snapshot.Validate() != nil || snapshot.Binding != binding || !snapshot.Independent ||
		snapshot.SnapshotHandle != snapshotEffect.Evidence[0].OpaqueRef {
		return migrationExportPrepared{}, ErrMigrationOperationInvalid
	}
	source, inventory, err := service.revalidateExportOperation(ctx, operation, binding)
	if err != nil {
		return migrationExportPrepared{}, err
	}
	if err := validateMigrationExportSnapshot(snapshot, source, inventory); err != nil {
		return migrationExportPrepared{}, err
	}
	capability, err := service.exportCapability(ctx)
	if err != nil || capability.Revision != operation.CapabilityRevision {
		return migrationExportPrepared{}, ErrMigrationPlanStale
	}
	profiles, err := service.prepareMigrationExportProfiles(operation, source)
	if err != nil {
		return migrationExportPrepared{}, err
	}
	secretEntries, secretRevisions, err := service.inspectMigrationExportSecrets(
		ctx, operation.PlanID, source, operation.SelectedSecretRefs,
	)
	nonSecretRevisionCount := len(source.records) + len(source.profileStates)
	if err != nil || len(source.revisions) != nonSecretRevisionCount+len(secretRevisions) ||
		!reflect.DeepEqual(secretRevisions, source.revisions[nonSecretRevisionCount:]) {
		if err == nil {
			err = ErrMigrationPlanStale
		}
		return migrationExportPrepared{}, err
	}
	return migrationExportPrepared{
		operation: operation, source: source, inventory: inventory,
		capability: capability, profiles: profiles, secrets: secretEntries,
	}, nil
}

func (service MigrationService) prepareMigrationExportProfiles(
	operation MigrationOperation,
	source migrationExportSource,
) ([]migrationExportProfile, error) {
	profiles := make([]migrationExportProfile, len(source.records))
	configOnly := migrationOperationConfigExport(operation)
	for index, record := range source.records {
		profileValue, err := service.Profiles.Load(record.Profile)
		if err != nil || profileValue.Identity.User != record.User {
			return nil, ErrMigrationPlanStale
		}
		normalized, err := migration.NormalizePortableProfile(profileValue)
		if err != nil {
			return nil, err
		}
		encoded, err := migration.EncodePortableProfile(normalized)
		if err != nil {
			return nil, err
		}
		authorityProposals, authorityRefs, err := migrationAuthorityProposalsForProfile(
			operation.ID, record.ID, profileValue,
		)
		if err != nil {
			return nil, err
		}
		profileState := profilestate.Snapshot{}
		stateComponentID := migration.OpaqueID("")
		stateDigest := migration.Digest("")
		if !configOnly {
			var exists bool
			profileState, exists = source.profileStates[record.ID]
			if !exists || profileState.LogicalBytes() == 0 ||
				profileState.Digest() == "" {
				return nil, ErrMigrationPlanStale
			}
			stateComponentID = migrationExportDerivedID(
				"profilestatecomponent", operation.ID, record.ID,
			)
			stateDigest = migration.Digest(profileState.Digest())
		}
		profiles[index] = migrationExportProfile{
			record: record,
			componentID: migrationExportDerivedID(
				"profilecomponent", operation.ID, record.ID,
			),
			encoded: encoded, digest: migrationExportBytesDigest(encoded),
			stateComponentID: stateComponentID, stateSnapshot: profileState,
			stateDigest:        stateDigest,
			guestUser:          normalized.Identity.User,
			authorityProposals: authorityProposals,
			authorityRefs:      authorityRefs,
		}
	}
	return profiles, nil
}

func requestSnapshotNotEmpty(snapshot backend.SourceSnapshot) bool {
	return snapshot.Binding != (backend.MigrationEffectBinding{}) || snapshot.SnapshotHandle != "" ||
		len(snapshot.Components) != 0 || len(snapshot.Identities) != 0 ||
		snapshot.Independent || snapshot.SourceClaimsRequired
}

func validateMigrationExportSnapshot(
	snapshot backend.SourceSnapshot,
	source migrationExportSource,
	inventory backend.SourceInventory,
) error {
	components := make(map[migration.OpaqueID]backend.MigrationComponent, len(snapshot.Components))
	for _, component := range snapshot.Components {
		if _, duplicate := components[component.DiskRef]; duplicate {
			return ErrMigrationOperationInvalid
		}
		components[component.DiskRef] = component
	}
	if len(components) != len(inventory.Disks) || len(snapshot.Identities) != len(source.records) {
		return ErrMigrationOperationInvalid
	}
	for _, disk := range inventory.Disks {
		component, exists := components[disk.DiskRef]
		if !exists || component.Kind != "disk" || component.LogicalBytes != disk.LogicalBytes {
			return ErrMigrationOperationInvalid
		}
	}
	for index, record := range source.records {
		identity := snapshot.Identities[index]
		instance := inventory.Instances[index]
		root := components[instance.RootDiskRef]
		if identity.EnvironmentRef != migration.OpaqueID(record.ID) ||
			identity.RootComponent != root.ComponentID || identity.Evidence.Validate() != nil {
			return ErrMigrationOperationInvalid
		}
	}
	return nil
}

func (service MigrationService) writeFreshMigrationExport(
	ctx context.Context,
	prepared migrationExportPrepared,
	snapshot backend.SourceSnapshot,
	outputPath, partialPath string,
	passphrase []byte,
) (inspection migration.SealedBundleInspection, resultErr error) {
	file, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return migration.SealedBundleInspection{}, ErrMigrationExportResumeRequired
		}
		return migration.SealedBundleInspection{}, err
	}
	fileClosed := false
	defer func() {
		if !fileClosed {
			resultErr = errors.Join(resultErr, file.Close())
		}
	}()
	writer, err := migration.NewWriter(file, migration.WriterOptions{
		BundleID:  prepared.operation.Bundle.BundleID,
		CreatedAt: prepared.operation.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		KDF:       migration.DefaultKDFParameters(), Limits: migration.DefaultLimits(),
		Random: rand.Reader, Passphrase: passphrase,
	})
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	writerClosed := false
	defer func() {
		if !writerClosed {
			resultErr = errors.Join(resultErr, writer.Close())
		}
	}()
	// The public prologue and wrapped-key header are the first independently
	// recoverable export cut. Persist both their contents and the partial's
	// directory entry before any component work so a crash never makes an
	// already-started provider read the operation's first durable boundary.
	if err := file.Sync(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if err := syncMigrationExportDirectory(filepath.Dir(partialPath)); err != nil {
		return migration.SealedBundleInspection{}, err
	}

	selectedSecrets := selectedMigrationExportSecrets(prepared.secrets)
	profileStateCount := 0
	if !prepared.configOnly {
		profileStateCount = len(prepared.profiles)
	}
	componentIndex := make([]migration.ComponentIndexEntry, 0,
		len(selectedSecrets)+len(prepared.profiles)+profileStateCount+len(snapshot.Components))
	completedComponents := uint32(0)
	completedLogical := uint64(0)
	completedComponentIDs := make([]migration.OpaqueID, 0,
		len(selectedSecrets)+len(prepared.profiles)+profileStateCount+len(snapshot.Components))
	var lastPrefix migration.Digest
	for _, secretValue := range selectedSecrets {
		entry, prefix, err := service.writeMigrationExportSecret(
			ctx, prepared.operation, secretValue, writer, file,
			completedComponentIDs, completedLogical, completedComponents,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		componentIndex = append(componentIndex, entry)
		lastPrefix = prefix
		completedComponents++
		completedComponentIDs = append(
			completedComponentIDs, secretValue.entry.ValueComponentID,
		)
	}
	for _, profileValue := range prepared.profiles {
		receipt, err := writer.Append(migration.RecordInput{
			Type: migration.RecordMetadata, ComponentID: profileValue.componentID,
			Plaintext: profileValue.encoded,
		})
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		completedComponentIDs = append(completedComponentIDs, profileValue.componentID)
		_, checkpointReceipt, err := writer.AppendCheckpoint(migration.CheckpointInput{
			OperationID:         migration.OpaqueID(prepared.operation.ID),
			CompletedComponents: completedComponentIDs,
			CurrentComponent:    profileValue.componentID,
		})
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		if err := file.Sync(); err != nil {
			return migration.SealedBundleInspection{}, err
		}
		lastPrefix = checkpointReceipt.PrefixDigest
		componentIndex = append(componentIndex, migration.ComponentIndexEntry{
			ComponentID: profileValue.componentID, Kind: "profile",
			LogicalBytes: uint64(len(profileValue.encoded)),
			FirstRecord:  receipt.Sequence, LastRecord: checkpointReceipt.Sequence,
			RecordCount: 2, ContentDigest: profileValue.digest,
		})
		completedComponents++
		if err := service.updateMigrationExportProgress(
			prepared.operation.ID, completedLogical, completedComponents,
			migrationExportFileOffset(file), string(profileValue.componentID),
			checkpointReceipt,
		); err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	if !prepared.configOnly {
		for _, profileValue := range prepared.profiles {
			entry, prefix, err := service.writeMigrationExportProfileState(
				ctx, prepared.operation, profileValue, writer, file,
				completedComponentIDs, completedLogical, completedComponents,
			)
			if err != nil {
				return migration.SealedBundleInspection{}, err
			}
			componentIndex = append(componentIndex, entry)
			lastPrefix = prefix
			completedLogical += entry.LogicalBytes
			completedComponents++
			completedComponentIDs = append(completedComponentIDs, entry.ComponentID)
		}
	}

	components := append([]backend.MigrationComponent(nil), snapshot.Components...)
	sort.Slice(components, func(left, right int) bool {
		return components[left].ComponentID < components[right].ComponentID
	})
	diskDigests := make(map[migration.OpaqueID]migration.Digest, len(components))
	for _, component := range components {
		entry, prefix, err := service.writeMigrationExportDisk(
			ctx, prepared.operation, snapshot, component, writer, file,
			completedComponentIDs, completedLogical, completedComponents,
			migration.ResumeComponentState{}, nil,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		componentIndex = append(componentIndex, entry)
		lastPrefix = prefix
		diskDigests[component.DiskRef] = entry.ContentDigest
		completedLogical += component.LogicalBytes
		completedComponents++
		completedComponentIDs = append(completedComponentIDs, component.ComponentID)
	}
	manifest, err := service.buildMigrationExportManifest(
		prepared, snapshot, componentIndex, diskDigests,
	)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	defer clear(manifestBytes)

	operation, err := service.Store.Load(prepared.operation.ID)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	writeEffect, err := migrationOperationEffect(operation, MigrationEffectWriteBundle)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if writeEffect.Status == MigrationEffectRunning {
		if _, err := service.Store.FinishEffect(
			prepared.operation.ID, writeEffect.ID, writeEffect.Provider,
			MigrationEffectSucceeded, []MigrationEffectEvidence{{
				Code: "migration.export.bundle_written", Digest: lastPrefix,
				Count: uint64(len(componentIndex)), ObservedAt: service.nowUTC(),
			}},
		); err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	operation, err = service.Store.TransitionPhase(
		prepared.operation.ID, MigrationPhaseSealing, nil,
	)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	sealEffect, err := migrationOperationEffect(operation, MigrationEffectSealBundle)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if sealEffect.Status == MigrationEffectPending {
		if _, _, err := service.Store.BeginEffect(
			operation.ID, sealEffect.ID, sealEffect.Provider,
		); err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	summary, err := writer.Seal(manifestBytes)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if err := file.Sync(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if err := writer.Close(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	writerClosed = true
	if err := file.Close(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	fileClosed = true
	inspection, err = inspectMigrationSealedExport(
		ctx, partialPath, passphrase, prepared.operation.Bundle.BundleID,
	)
	if err != nil || inspection.Summary != summary ||
		!reflectMigrationManifestEqual(inspection.Manifest, manifest) {
		if err == nil {
			err = errors.New("sealed migration export verification changed")
		}
		return migration.SealedBundleInspection{}, err
	}
	if err := publishMigrationExportFile(partialPath, outputPath); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	return inspection, nil
}

func (service MigrationService) resumeMigrationExport(
	ctx context.Context,
	prepared migrationExportPrepared,
	snapshot backend.SourceSnapshot,
	outputPath, partialPath string,
	passphrase []byte,
) (inspection migration.SealedBundleInspection, resultErr error) {
	sealedInspection, sealedErr := inspectMigrationSealedExport(
		ctx, partialPath, passphrase, prepared.operation.Bundle.BundleID,
	)
	if sealedErr == nil {
		if err := publishMigrationExportFile(partialPath, outputPath); err != nil {
			return migration.SealedBundleInspection{}, err
		}
		return sealedInspection, nil
	}

	operation, err := service.Store.Load(prepared.operation.ID)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if operation.Phase == MigrationPhaseSealing {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseRecoverableFailure, nil,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseWriting, nil,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	if operation.Phase != MigrationPhaseWriting {
		return migration.SealedBundleInspection{}, ErrMigrationOperationInvalid
	}
	writeEffect, err := migrationOperationEffect(operation, MigrationEffectWriteBundle)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if writeEffect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(
			operation.ID, writeEffect.ID, writeEffect.Provider,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		writeEffect, err = migrationOperationEffect(operation, MigrationEffectWriteBundle)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	if writeEffect.Status != MigrationEffectRunning &&
		writeEffect.Status != MigrationEffectSucceeded {
		return migration.SealedBundleInspection{}, ErrMigrationOperationInvalid
	}
	prepared.operation = operation

	components := append([]backend.MigrationComponent(nil), snapshot.Components...)
	sort.Slice(components, func(left, right int) bool {
		return components[left].ComponentID < components[right].ComponentID
	})
	selectedSecrets := selectedMigrationExportSecrets(prepared.secrets)
	secretFacts := make(map[migration.OpaqueID]migrationExportSecretContentFacts,
		len(selectedSecrets))
	resumeSpecs := make([]migration.ResumeComponentSpec, 0,
		len(selectedSecrets)+len(prepared.profiles)*2+len(components))
	for _, secretValue := range selectedSecrets {
		facts, err := service.migrationExportSecretContentFacts(ctx, secretValue)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		secretFacts[secretValue.entry.ValueComponentID] = facts
		resumeSpecs = append(resumeSpecs, migration.ResumeComponentSpec{
			ComponentID: secretValue.entry.ValueComponentID, Kind: "secret-value",
			LogicalBytes: facts.logicalBytes,
		})
	}
	for _, profileValue := range prepared.profiles {
		resumeSpecs = append(resumeSpecs, migration.ResumeComponentSpec{
			ComponentID: profileValue.componentID, Kind: "profile",
			LogicalBytes: uint64(len(profileValue.encoded)),
		})
	}
	if !prepared.configOnly {
		for _, profileValue := range prepared.profiles {
			resumeSpecs = append(resumeSpecs, migration.ResumeComponentSpec{
				ComponentID: profileValue.stateComponentID, Kind: "profile-state",
				LogicalBytes: profileValue.stateSnapshot.LogicalBytes(),
			})
		}
	}
	for _, component := range components {
		resumeSpecs = append(resumeSpecs, migration.ResumeComponentSpec{
			ComponentID: component.ComponentID, Kind: "disk",
			LogicalBytes: component.LogicalBytes,
		})
	}
	file, err := openMigrationBundleReadWriteNoFollow(partialPath)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	fileClosed := false
	defer func() {
		if !fileClosed {
			resultErr = errors.Join(resultErr, file.Close())
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return migration.SealedBundleInspection{}, err
	}
	resumed, err := migration.ResumeWriter(file, info.Size(), migration.ResumeOptions{
		BundleID:    prepared.operation.Bundle.BundleID,
		OperationID: migration.OpaqueID(prepared.operation.ID),
		CreatedAt: prepared.operation.CreatedAt.UTC().Format(
			"2006-01-02T15:04:05.999999999Z07:00",
		),
		Passphrase: passphrase, Random: rand.Reader, Limits: migration.DefaultLimits(),
		Components:               resumeSpecs,
		ExpectedCheckpointDigest: prepared.operation.Progress.CheckpointDigest,
	})
	if err != nil {
		if errors.Is(err, migration.ErrBundleAlreadySealed) {
			return migration.SealedBundleInspection{}, sealedErr
		}
		return migration.SealedBundleInspection{}, err
	}
	writer := resumed.Writer
	writerClosed := false
	defer func() {
		if !writerClosed {
			resultErr = errors.Join(resultErr, writer.Close())
		}
	}()
	stateByComponent := make(map[migration.OpaqueID]migration.ResumeComponentState,
		len(resumed.Components))
	for _, state := range resumed.Components {
		stateByComponent[state.ComponentID] = state
	}
	completedAtCheckpoint := make(map[migration.OpaqueID]struct{})
	if resumed.Checkpoint != nil {
		for _, componentID := range resumed.Checkpoint.CompletedComponents {
			completedAtCheckpoint[componentID] = struct{}{}
		}
	}

	componentIndex := make([]migration.ComponentIndexEntry, 0, len(resumeSpecs))
	completedComponentIDs := make([]migration.OpaqueID, 0, len(resumeSpecs))
	completedComponents := uint32(0)
	completedLogical := uint64(0)
	lastPrefix := resumed.PrefixDigest
	for _, secretValue := range selectedSecrets {
		componentID := secretValue.entry.ValueComponentID
		facts := secretFacts[componentID]
		state, exists := stateByComponent[componentID]
		if exists {
			_, complete := completedAtCheckpoint[componentID]
			if !complete || state.ContentBytes != facts.logicalBytes ||
				state.PayloadRecords != 1 || state.ContentDigest != facts.digest {
				return migration.SealedBundleInspection{}, migration.ErrCheckpointMismatch
			}
			componentIndex = append(componentIndex, migration.ComponentIndexEntry{
				ComponentID: componentID, Kind: "secret-value",
				LogicalBytes: facts.logicalBytes,
				FirstRecord:  state.FirstRecord, LastRecord: state.LastRecord,
				RecordCount: state.RecordCount, ContentDigest: facts.digest,
			})
			completedComponentIDs = append(completedComponentIDs, componentID)
			completedComponents++
			continue
		}
		entry, prefix, err := service.writeMigrationExportSecret(
			ctx, prepared.operation, secretValue, writer, file,
			completedComponentIDs, completedLogical, completedComponents,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		if entry.LogicalBytes != facts.logicalBytes || entry.ContentDigest != facts.digest {
			return migration.SealedBundleInspection{}, ErrMigrationPlanStale
		}
		componentIndex = append(componentIndex, entry)
		lastPrefix = prefix
		completedComponentIDs = append(completedComponentIDs, componentID)
		completedComponents++
	}
	for _, profileValue := range prepared.profiles {
		state, exists := stateByComponent[profileValue.componentID]
		if exists {
			_, complete := completedAtCheckpoint[profileValue.componentID]
			if !complete || state.ContentBytes != uint64(len(profileValue.encoded)) ||
				state.PayloadRecords != 1 || state.ContentDigest != profileValue.digest {
				return migration.SealedBundleInspection{}, migration.ErrCheckpointMismatch
			}
			componentIndex = append(componentIndex, migration.ComponentIndexEntry{
				ComponentID: profileValue.componentID, Kind: "profile",
				LogicalBytes: uint64(len(profileValue.encoded)),
				FirstRecord:  state.FirstRecord, LastRecord: state.LastRecord,
				RecordCount: state.RecordCount, ContentDigest: profileValue.digest,
			})
			completedComponentIDs = append(completedComponentIDs, profileValue.componentID)
			completedComponents++
			continue
		}
		receipt, err := writer.Append(migration.RecordInput{
			Type: migration.RecordMetadata, ComponentID: profileValue.componentID,
			Plaintext: profileValue.encoded,
		})
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		completedComponentIDs = append(completedComponentIDs, profileValue.componentID)
		_, checkpointReceipt, err := writer.AppendCheckpoint(migration.CheckpointInput{
			OperationID:         migration.OpaqueID(prepared.operation.ID),
			CompletedComponents: completedComponentIDs,
			CurrentComponent:    profileValue.componentID,
		})
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		if err := file.Sync(); err != nil {
			return migration.SealedBundleInspection{}, err
		}
		lastPrefix = checkpointReceipt.PrefixDigest
		componentIndex = append(componentIndex, migration.ComponentIndexEntry{
			ComponentID: profileValue.componentID, Kind: "profile",
			LogicalBytes: uint64(len(profileValue.encoded)),
			FirstRecord:  receipt.Sequence, LastRecord: checkpointReceipt.Sequence,
			RecordCount: 2, ContentDigest: profileValue.digest,
		})
		completedComponents++
		if err := service.updateMigrationExportProgress(
			prepared.operation.ID, completedLogical, completedComponents,
			migrationExportFileOffset(file), string(profileValue.componentID),
			checkpointReceipt,
		); err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	if !prepared.configOnly {
		for _, profileValue := range prepared.profiles {
			state, exists := stateByComponent[profileValue.stateComponentID]
			logicalBytes := profileValue.stateSnapshot.LogicalBytes()
			if exists {
				_, complete := completedAtCheckpoint[profileValue.stateComponentID]
				if !complete || state.ContentBytes != logicalBytes ||
					state.NextLogicalOffset != logicalBytes || state.PayloadRecords == 0 {
					return migration.SealedBundleInspection{}, migration.ErrCheckpointMismatch
				}
				componentIndex = append(componentIndex, migration.ComponentIndexEntry{
					ComponentID: profileValue.stateComponentID, Kind: "profile-state",
					LogicalBytes: logicalBytes, FirstRecord: state.FirstRecord,
					LastRecord: state.LastRecord, RecordCount: state.RecordCount,
					ContentDigest: profileValue.stateDigest,
				})
				completedLogical += logicalBytes
				completedComponentIDs = append(completedComponentIDs, profileValue.stateComponentID)
				completedComponents++
				continue
			}
			entry, prefix, err := service.writeMigrationExportProfileState(
				ctx, prepared.operation, profileValue, writer, file,
				completedComponentIDs, completedLogical, completedComponents,
			)
			if err != nil {
				return migration.SealedBundleInspection{}, err
			}
			componentIndex = append(componentIndex, entry)
			lastPrefix = prefix
			completedLogical += entry.LogicalBytes
			completedComponents++
			completedComponentIDs = append(completedComponentIDs, entry.ComponentID)
		}
	}

	diskDigests := make(map[migration.OpaqueID]migration.Digest, len(components))
	for _, component := range components {
		state, exists := stateByComponent[component.ComponentID]
		digester := resumed.DiskDigesters[component.ComponentID]
		if digester == nil || (exists &&
			(state.ContentBytes != state.NextLogicalOffset ||
				state.NextLogicalOffset > component.LogicalBytes)) {
			return migration.SealedBundleInspection{}, migration.ErrCheckpointMismatch
		}
		if exists && state.NextLogicalOffset == component.LogicalBytes {
			if _, complete := completedAtCheckpoint[component.ComponentID]; !complete {
				return migration.SealedBundleInspection{}, migration.ErrCheckpointMismatch
			}
			digest, err := digester.Finish()
			if err != nil || (component.ContentDigest != "" && component.ContentDigest != digest) {
				if err == nil {
					err = backend.ErrMigrationProviderResponse
				}
				return migration.SealedBundleInspection{}, err
			}
			entry := migration.ComponentIndexEntry{
				ComponentID: component.ComponentID, Kind: "disk", DiskID: component.DiskRef,
				LogicalBytes: component.LogicalBytes,
				FirstRecord:  state.FirstRecord, LastRecord: state.LastRecord,
				RecordCount: state.RecordCount, ContentDigest: digest,
			}
			componentIndex = append(componentIndex, entry)
			diskDigests[component.DiskRef] = digest
			completedLogical += component.LogicalBytes
			completedComponents++
			completedComponentIDs = append(completedComponentIDs, component.ComponentID)
			continue
		}
		entry, prefix, err := service.writeMigrationExportDisk(
			ctx, prepared.operation, snapshot, component, writer, file,
			completedComponentIDs, completedLogical, completedComponents, state, digester,
		)
		if err != nil {
			return migration.SealedBundleInspection{}, err
		}
		componentIndex = append(componentIndex, entry)
		lastPrefix = prefix
		diskDigests[component.DiskRef] = entry.ContentDigest
		completedLogical += component.LogicalBytes
		completedComponents++
		completedComponentIDs = append(completedComponentIDs, component.ComponentID)
	}
	manifest, err := service.buildMigrationExportManifest(
		prepared, snapshot, componentIndex, diskDigests,
	)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	defer clear(manifestBytes)
	operation, err = service.Store.Load(prepared.operation.ID)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	writeEffect, err = migrationOperationEffect(operation, MigrationEffectWriteBundle)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if writeEffect.Status == MigrationEffectRunning {
		if _, err := service.Store.FinishEffect(
			operation.ID, writeEffect.ID, writeEffect.Provider,
			MigrationEffectSucceeded, []MigrationEffectEvidence{{
				Code: "migration.export.bundle_written", Digest: lastPrefix,
				Count: uint64(len(componentIndex)), ObservedAt: service.nowUTC(),
			}},
		); err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	operation, err = service.Store.TransitionPhase(
		operation.ID, MigrationPhaseSealing, nil,
	)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	sealEffect, err := migrationOperationEffect(operation, MigrationEffectSealBundle)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if sealEffect.Status == MigrationEffectPending {
		if _, _, err := service.Store.BeginEffect(
			operation.ID, sealEffect.ID, sealEffect.Provider,
		); err != nil {
			return migration.SealedBundleInspection{}, err
		}
	}
	summary, err := writer.Seal(manifestBytes)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if err := file.Sync(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if err := writer.Close(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	writerClosed = true
	if err := file.Close(); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	fileClosed = true
	inspection, err = inspectMigrationSealedExport(
		ctx, partialPath, passphrase, prepared.operation.Bundle.BundleID,
	)
	if err != nil || inspection.Summary != summary ||
		!reflectMigrationManifestEqual(inspection.Manifest, manifest) {
		if err == nil {
			err = errors.New("resumed migration export verification changed")
		}
		return migration.SealedBundleInspection{}, err
	}
	if err := publishMigrationExportFile(partialPath, outputPath); err != nil {
		return migration.SealedBundleInspection{}, err
	}
	return inspection, nil
}

func (service MigrationService) writeMigrationExportProfileState(
	ctx context.Context,
	operation MigrationOperation,
	profileValue migrationExportProfile,
	writer *migration.Writer,
	file *os.File,
	completedComponentIDs []migration.OpaqueID,
	completedLogical uint64,
	completedComponents uint32,
) (migration.ComponentIndexEntry, migration.Digest, error) {
	if profileValue.stateComponentID == "" || profileValue.stateSnapshot.LogicalBytes() == 0 ||
		profileValue.stateDigest != migration.Digest(profileValue.stateSnapshot.Digest()) {
		return migration.ComponentIndexEntry{}, "", ErrMigrationOperationInvalid
	}
	var firstRecord uint64
	var lastPayload uint64
	payloadRecords := uint64(0)
	ordinal := uint64(0)
	logicalOffset := uint64(0)
	maxChunk := int(migration.DefaultLimits().MaxChunkBytes)
	err := profileValue.stateSnapshot.Write(ctx, maxChunk, func(chunk []byte) error {
		receipt, err := writer.Append(migration.RecordInput{
			Type: migration.RecordRawChunk, ComponentID: profileValue.stateComponentID,
			Ordinal: ordinal, LogicalOffset: logicalOffset, Plaintext: chunk,
		})
		if err != nil {
			return err
		}
		if payloadRecords == 0 {
			firstRecord = receipt.Sequence
		}
		lastPayload = receipt.Sequence
		payloadRecords++
		ordinal++
		logicalOffset += uint64(len(chunk))
		return nil
	})
	if err != nil || payloadRecords == 0 || logicalOffset != profileValue.stateSnapshot.LogicalBytes() {
		return migration.ComponentIndexEntry{}, "", errors.Join(ErrMigrationPlanStale, err)
	}
	completed := append(append([]migration.OpaqueID(nil), completedComponentIDs...),
		profileValue.stateComponentID)
	_, checkpointReceipt, err := writer.AppendCheckpoint(migration.CheckpointInput{
		OperationID: migration.OpaqueID(operation.ID), CompletedComponents: completed,
		CurrentComponent: profileValue.stateComponentID,
	})
	if err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	if checkpointReceipt.Sequence != lastPayload+1 {
		return migration.ComponentIndexEntry{}, "", migration.ErrCorruptBundle
	}
	if err := file.Sync(); err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	if err := service.updateMigrationExportProgress(
		operation.ID, completedLogical+logicalOffset, completedComponents+1,
		migrationExportFileOffset(file), string(profileValue.stateComponentID),
		checkpointReceipt,
	); err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	return migration.ComponentIndexEntry{
		ComponentID: profileValue.stateComponentID, Kind: "profile-state",
		LogicalBytes: logicalOffset, FirstRecord: firstRecord,
		LastRecord: checkpointReceipt.Sequence, RecordCount: payloadRecords + 1,
		ContentDigest: profileValue.stateDigest,
	}, checkpointReceipt.PrefixDigest, nil
}

func (service MigrationService) writeMigrationExportDisk(
	ctx context.Context,
	operation MigrationOperation,
	snapshot backend.SourceSnapshot,
	component backend.MigrationComponent,
	writer *migration.Writer,
	file *os.File,
	completedComponentIDs []migration.OpaqueID,
	completedLogical uint64,
	completedComponents uint32,
	start migration.ResumeComponentState,
	digester *migration.LogicalDigester,
) (migration.ComponentIndexEntry, migration.Digest, error) {
	var err error
	if digester == nil {
		digester, err = migration.NewLogicalDigester(component.LogicalBytes)
		if err != nil {
			return migration.ComponentIndexEntry{}, "", err
		}
	}
	ordinal := start.NextOrdinal
	nextOffset := start.NextLogicalOffset
	firstSequence := start.FirstRecord
	lastSequence := start.LastRecord
	recordCount := start.RecordCount
	var prefix migration.Digest
	err = service.Export.ReadMigrationComponent(
		ctx, backend.ComponentReadRequest{
			Binding: snapshot.Binding, SnapshotHandle: snapshot.SnapshotHandle,
			ComponentID: component.ComponentID, ResumeOffset: nextOffset,
			MaxChunkBytes: migration.DefaultLimits().MaxChunkBytes,
		},
		func(extent backend.MigrationExtent) error {
			if extent.Validate(migration.DefaultLimits().MaxChunkBytes) != nil ||
				extent.LogicalOffset != nextOffset {
				return backend.ErrMigrationProviderResponse
			}
			if err := digester.WriteExtent(migration.Extent{
				Kind: extent.Kind, LogicalOffset: extent.LogicalOffset,
				Length: extent.Length, Data: extent.Data,
			}); err != nil {
				return err
			}
			record := migration.RecordInput{
				ComponentID: component.ComponentID, Ordinal: ordinal,
				LogicalOffset: extent.LogicalOffset,
			}
			switch extent.Kind {
			case migration.ExtentData:
				record.Type = migration.RecordDataChunk
				record.Plaintext = extent.Data
			case migration.ExtentZero:
				record.Type = migration.RecordZeroExtent
				record.ExtentLength = extent.Length
			case migration.ExtentHole:
				record.Type = migration.RecordHoleExtent
				record.ExtentLength = extent.Length
			default:
				return backend.ErrMigrationProviderResponse
			}
			receipt, err := writer.Append(record)
			if err != nil {
				return err
			}
			if recordCount == 0 {
				firstSequence = receipt.Sequence
			}
			ordinal++
			nextOffset += extent.Length
			recordCount++
			checkpointComponents := append(
				[]migration.OpaqueID(nil), completedComponentIDs...,
			)
			componentComplete := nextOffset == component.LogicalBytes
			if componentComplete {
				checkpointComponents = append(checkpointComponents, component.ComponentID)
			}
			_, checkpointReceipt, err := writer.AppendCheckpoint(migration.CheckpointInput{
				OperationID:         migration.OpaqueID(operation.ID),
				CompletedComponents: checkpointComponents,
				CurrentComponent:    component.ComponentID,
			})
			if err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			lastSequence = checkpointReceipt.Sequence
			prefix = checkpointReceipt.PrefixDigest
			ordinal = checkpointReceipt.Header.Ordinal + 1
			recordCount++
			progressComponents := completedComponents
			if componentComplete {
				progressComponents++
			}
			if err := service.updateMigrationExportProgress(
				operation.ID, completedLogical+nextOffset, progressComponents,
				migrationExportFileOffset(file), string(component.ComponentID),
				checkpointReceipt,
			); err != nil {
				return err
			}
			return nil
		},
	)
	if err != nil || recordCount == 0 || nextOffset != component.LogicalBytes {
		if err == nil {
			err = backend.ErrMigrationProviderResponse
		}
		return migration.ComponentIndexEntry{}, "", err
	}
	digest, err := digester.Finish()
	if err != nil || (component.ContentDigest != "" && component.ContentDigest != digest) {
		if err == nil {
			err = backend.ErrMigrationProviderResponse
		}
		return migration.ComponentIndexEntry{}, "", err
	}
	return migration.ComponentIndexEntry{
		ComponentID: component.ComponentID, Kind: "disk", DiskID: component.DiskRef,
		LogicalBytes: component.LogicalBytes,
		FirstRecord:  firstSequence, LastRecord: lastSequence,
		RecordCount: recordCount, ContentDigest: digest,
	}, prefix, nil
}

type migrationExportSecretContentFacts struct {
	logicalBytes uint64
	digest       migration.Digest
}

func selectedMigrationExportSecrets(
	values []migrationExportSecret,
) []migrationExportSecret {
	selected := make([]migrationExportSecret, 0, len(values))
	for _, value := range values {
		if value.entry.Transfer == migration.SecretSelectedValue {
			selected = append(selected, value)
		}
	}
	return selected
}

func (service MigrationService) resolveMigrationExportSecret(
	ctx context.Context,
	value migrationExportSecret,
) (*secrets.Buffer, error) {
	if service.Secrets == nil || value.entry.Transfer != migration.SecretSelectedValue ||
		value.entry.ValueComponentID == "" {
		return nil, ErrMigrationCapabilityUnavailable
	}
	reference, err := service.Secrets.Reference(ctx, string(value.entry.SecretRef))
	if err != nil {
		return nil, err
	}
	if reference.Validate() != nil || reference.Ref != string(value.entry.SecretRef) ||
		reference.Provider != value.entry.Provider ||
		reference.Availability != secrets.AvailabilityAvailable ||
		reference.Generation != value.generation {
		return nil, ErrMigrationPlanStale
	}
	buffer, err := service.Secrets.Resolve(ctx, reference.Ref)
	if err != nil {
		return nil, err
	}
	return buffer, nil
}

func (service MigrationService) migrationExportSecretContentFacts(
	ctx context.Context,
	value migrationExportSecret,
) (migrationExportSecretContentFacts, error) {
	buffer, err := service.resolveMigrationExportSecret(ctx, value)
	if err != nil {
		return migrationExportSecretContentFacts{}, err
	}
	defer buffer.Clear()
	var facts migrationExportSecretContentFacts
	err = buffer.Use(func(plaintext []byte) error {
		facts.logicalBytes = uint64(len(plaintext))
		facts.digest = migrationExportBytesDigest(plaintext)
		return nil
	})
	if err != nil {
		return migrationExportSecretContentFacts{}, err
	}
	return facts, nil
}

func (service MigrationService) writeMigrationExportSecret(
	ctx context.Context,
	operation MigrationOperation,
	value migrationExportSecret,
	writer *migration.Writer,
	file *os.File,
	completedComponentIDs []migration.OpaqueID,
	completedLogical uint64,
	completedComponents uint32,
) (migration.ComponentIndexEntry, migration.Digest, error) {
	buffer, err := service.resolveMigrationExportSecret(ctx, value)
	if err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	defer buffer.Clear()
	var receipt migration.RecordReceipt
	var facts migrationExportSecretContentFacts
	err = buffer.Use(func(plaintext []byte) error {
		facts.logicalBytes = uint64(len(plaintext))
		facts.digest = migrationExportBytesDigest(plaintext)
		var appendErr error
		receipt, appendErr = writer.Append(migration.RecordInput{
			Type: migration.RecordSecretValue, ComponentID: value.entry.ValueComponentID,
			Plaintext: plaintext,
		})
		return appendErr
	})
	if err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	checkpointComponents := append(
		[]migration.OpaqueID(nil), completedComponentIDs...,
	)
	checkpointComponents = append(checkpointComponents, value.entry.ValueComponentID)
	_, checkpointReceipt, err := writer.AppendCheckpoint(migration.CheckpointInput{
		OperationID:         migration.OpaqueID(operation.ID),
		CompletedComponents: checkpointComponents,
		CurrentComponent:    value.entry.ValueComponentID,
	})
	if err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	if err := file.Sync(); err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	if err := service.updateMigrationExportProgress(
		operation.ID, completedLogical, completedComponents+1,
		migrationExportFileOffset(file), string(value.entry.ValueComponentID),
		checkpointReceipt,
	); err != nil {
		return migration.ComponentIndexEntry{}, "", err
	}
	return migration.ComponentIndexEntry{
		ComponentID: value.entry.ValueComponentID, Kind: "secret-value",
		LogicalBytes: facts.logicalBytes,
		FirstRecord:  receipt.Sequence, LastRecord: checkpointReceipt.Sequence,
		RecordCount: 2, ContentDigest: facts.digest,
	}, checkpointReceipt.PrefixDigest, nil
}

func (service MigrationService) buildMigrationExportManifest(
	prepared migrationExportPrepared,
	snapshot backend.SourceSnapshot,
	componentIndex []migration.ComponentIndexEntry,
	diskDigests map[migration.OpaqueID]migration.Digest,
) (migration.Manifest, error) {
	if prepared.configOnly {
		return service.buildMigrationConfigExportManifest(prepared, componentIndex)
	}
	productVersion := strings.TrimSpace(service.ProductVersion)
	hostOS := strings.TrimSpace(service.HostOS)
	hostArch := strings.TrimSpace(service.HostArch)
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	if hostArch == "" {
		hostArch = runtime.GOARCH
	}
	guestArch := ""
	for _, pair := range prepared.capability.ArchitecturePairs {
		if pair.Host == hostOS+"/"+hostArch {
			guestArch = strings.TrimPrefix(pair.Guest, "linux/")
			break
		}
	}
	switch guestArch {
	case "arm64":
		guestArch = "aarch64"
	case "amd64":
		guestArch = "x86_64"
	}
	identities := make(map[migration.OpaqueID]migration.GuestIdentityEvidence, len(snapshot.Identities))
	for _, identity := range snapshot.Identities {
		value := identity.Evidence
		value.SSHHostKeyDigests = append([]migration.Digest(nil), value.SSHHostKeyDigests...)
		slices.Sort(value.SSHHostKeyDigests)
		identities[identity.EnvironmentRef] = value
	}
	profileByEnvironment := make(map[migration.OpaqueID]migrationExportProfile, len(prepared.profiles))
	for _, profileValue := range prepared.profiles {
		profileByEnvironment[migration.OpaqueID(profileValue.record.ID)] = profileValue
	}

	environments := make([]migration.EnvironmentSnapshot, 0, len(prepared.source.records))
	for _, record := range prepared.source.records {
		ref := migration.OpaqueID(record.ID)
		profileValue := profileByEnvironment[ref]
		diskRefs := make([]migration.OpaqueID, 0)
		for _, edge := range prepared.inventory.Attachments {
			if edge.EnvironmentRef == ref {
				diskRefs = append(diskRefs, edge.DiskRef)
			}
		}
		slices.Sort(diskRefs)
		workspaceProposals := []migration.WorkspaceProposal{}
		if binding, ok := pinnedEnvironmentWorkspace(record); ok {
			workspaceProposals = append(workspaceProposals, migration.WorkspaceProposal{
				ProposalID: migrationExportDerivedID("workspace", prepared.operation.ID, record.ID),
				GuestPath:  binding.GuestRoot, HostPathHint: binding.HostRoot, State: "disabled",
			})
		}
		environments = append(environments, migration.EnvironmentSnapshot{
			SourceEnvironmentRef: ref, DisplayNameHint: record.Name,
			Runtime: "linux", GuestUser: profileValue.guestUser,
			Backend: prepared.capability.Provider, Mode: migration.ExportModeFull,
			ImageProvenance:         migrationEnvironmentImageProvenance(record),
			ProfileComponentID:      profileValue.componentID,
			ProfileStateComponentID: profileValue.stateComponentID,
			WorkspaceProposals:      workspaceProposals,
			AuthorityProposalRefs: append(
				[]migration.OpaqueID(nil), profileValue.authorityRefs...,
			),
			GuestIdentityEvidence: identities[ref], DiskRefs: diskRefs,
		})
	}

	disks := make([]migration.DiskObject, 0, len(prepared.inventory.Disks))
	for _, disk := range prepared.inventory.Disks {
		kind := "lima-additional"
		if disk.Role == migration.DiskRoleRoot {
			kind = "lima-root"
		}
		features := []string{}
		if prepared.capability.SparseExtents {
			features = []string{"sparse-extents"}
		}
		disks = append(disks, migration.DiskObject{
			DiskID: disk.DiskRef, Role: disk.Role, Format: disk.Format,
			LogicalBytes:       disk.LogicalBytes,
			AllocatedBytesHint: disk.AllocatedBytesHint,
			ContentDigest:      diskDigests[disk.DiskRef],
			Provider: migration.ProviderDiskFacts{
				Name: migrationExportProviderDiskName(disk.DiskRef),
				Kind: kind, Features: features,
			},
		})
	}
	edges := make([]migration.DiskEdge, len(prepared.inventory.Attachments))
	for index, edge := range prepared.inventory.Attachments {
		edges[index] = migration.DiskEdge{
			EnvironmentRef: edge.EnvironmentRef, DiskID: edge.DiskRef,
			Attachment: edge.Attachment, GuestPath: edge.GuestPath,
			FSType: edge.FSType, ReadOnly: edge.ReadOnly,
		}
	}
	manifest := migration.Manifest{
		Schema:        "hideout.migration-manifest/v1",
		BundleID:      prepared.operation.Bundle.BundleID,
		FormatVersion: migration.BundleFormatVersion,
		SourceProduct: migration.SourceProduct{
			Version: productVersion, HostOS: hostOS, HostArch: hostArch,
			Backend:        prepared.capability.Provider,
			BackendVersion: prepared.capability.ProviderVersion, GuestArch: guestArch,
		},
		Environments: environments, DiskObjects: disks, DiskEdges: edges,
		SecretEntries:      migrationExportManifestSecrets(prepared.secrets),
		AuthorityProposals: migrationExportAuthorityProposals(prepared.profiles),
		ComponentIndex:     componentIndex,
		ExcludedClasses:    append([]string(nil), prepared.inventory.ExcludedClasses...),
		RequiredCapabilities: []migration.RequiredCapability{{
			ID: "full-state", Provider: prepared.capability.Provider,
			MinimumVersion: prepared.capability.ProviderVersion,
		}},
	}
	if err := manifest.Validate(migration.DefaultLimits()); err != nil {
		return migration.Manifest{}, err
	}
	return manifest, nil
}

func migrationEnvironmentImageProvenance(record environment.Record) *migration.ImageProvenance {
	if record.Runtime == nil {
		return nil
	}
	return &migration.ImageProvenance{
		Reference: record.Runtime.ArtifactLocation,
		Digest:    migration.Digest("sha256:" + record.Runtime.ArtifactSHA256),
	}
}

func (service MigrationService) buildMigrationConfigExportManifest(
	prepared migrationExportPrepared,
	componentIndex []migration.ComponentIndexEntry,
) (migration.Manifest, error) {
	productVersion := strings.TrimSpace(service.ProductVersion)
	hostOS := strings.TrimSpace(service.HostOS)
	hostArch := strings.TrimSpace(service.HostArch)
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	if hostArch == "" {
		hostArch = runtime.GOARCH
	}
	guestArch := ""
	for _, pair := range prepared.capability.ArchitecturePairs {
		if pair.Host == hostOS+"/"+hostArch {
			guestArch = strings.TrimPrefix(pair.Guest, "linux/")
			break
		}
	}
	switch guestArch {
	case "arm64":
		guestArch = "aarch64"
	case "amd64":
		guestArch = "x86_64"
	}
	profileByEnvironment := make(
		map[migration.OpaqueID]migrationExportProfile, len(prepared.profiles),
	)
	for _, profileValue := range prepared.profiles {
		profileByEnvironment[migration.OpaqueID(profileValue.record.ID)] = profileValue
	}
	environments := make([]migration.EnvironmentSnapshot, 0, len(prepared.source.records))
	for _, record := range prepared.source.records {
		ref := migration.OpaqueID(record.ID)
		profileValue, exists := profileByEnvironment[ref]
		if !exists {
			return migration.Manifest{}, ErrMigrationOperationInvalid
		}
		workspaceProposals := []migration.WorkspaceProposal{}
		if binding, ok := pinnedEnvironmentWorkspace(record); ok {
			workspaceProposals = append(workspaceProposals, migration.WorkspaceProposal{
				ProposalID: migrationExportDerivedID("workspace", prepared.operation.ID, record.ID),
				GuestPath:  binding.GuestRoot, HostPathHint: binding.HostRoot, State: "disabled",
			})
		}
		runtimeName := "linux"
		if record.Backend == "native" {
			runtimeName = "native"
		}
		environments = append(environments, migration.EnvironmentSnapshot{
			SourceEnvironmentRef: ref, DisplayNameHint: record.Name,
			Runtime: runtimeName, GuestUser: profileValue.guestUser,
			Backend: record.Backend, Mode: migration.ExportModeConfig,
			ProfileComponentID: profileValue.componentID,
			WorkspaceProposals: workspaceProposals,
			AuthorityProposalRefs: append(
				[]migration.OpaqueID(nil), profileValue.authorityRefs...,
			),
			GuestIdentityEvidence: migration.ConfigIdentityUnavailableEvidence(),
			DiskRefs:              []migration.OpaqueID{},
		})
	}
	manifest := migration.Manifest{
		Schema:        "hideout.migration-manifest/v1",
		BundleID:      prepared.operation.Bundle.BundleID,
		FormatVersion: migration.BundleFormatVersion,
		SourceProduct: migration.SourceProduct{
			Version: productVersion, HostOS: hostOS, HostArch: hostArch,
			Backend:        prepared.capability.Provider,
			BackendVersion: prepared.capability.ProviderVersion,
			GuestArch:      guestArch,
		},
		Environments: environments,
		DiskObjects:  []migration.DiskObject{}, DiskEdges: []migration.DiskEdge{},
		SecretEntries:        migrationExportManifestSecrets(prepared.secrets),
		AuthorityProposals:   migrationExportAuthorityProposals(prepared.profiles),
		ComponentIndex:       componentIndex,
		ExcludedClasses:      append([]string(nil), migrationConfigExcludedClasses...),
		RequiredCapabilities: []migration.RequiredCapability{},
	}
	if err := manifest.Validate(migration.DefaultLimits()); err != nil {
		return migration.Manifest{}, err
	}
	return manifest, nil
}

func migrationExportManifestSecrets(values []migrationExportSecret) []migration.SecretEntry {
	entries := make([]migration.SecretEntry, len(values))
	for index, value := range values {
		entries[index] = value.entry
		entries[index].EnvironmentRefs = append(
			[]migration.OpaqueID(nil), value.entry.EnvironmentRefs...,
		)
	}
	return entries
}

func migrationExportAuthorityProposals(
	profiles []migrationExportProfile,
) []migration.AuthorityProposal {
	proposals := make([]migration.AuthorityProposal, 0)
	for _, profileValue := range profiles {
		proposals = append(proposals, profileValue.authorityProposals...)
	}
	sort.Slice(proposals, func(left, right int) bool {
		return proposals[left].ProposalID < proposals[right].ProposalID
	})
	return proposals
}

func (service MigrationService) updateMigrationExportProgress(
	operationID string,
	logical uint64,
	components uint32,
	encoded uint64,
	current string,
	checkpoint migration.RecordReceipt,
) error {
	operation, err := service.Store.Load(operationID)
	if err != nil {
		return err
	}
	progress := operation.Progress
	progress.CompletedLogicalBytes = logical
	progress.ComponentsComplete = components
	progress.CompletedEncodedBytes = encoded
	progress.CurrentItem = current
	progress.CheckpointAt = operation.UpdatedAt
	progress.CheckpointSequence = checkpoint.Sequence
	progress.CheckpointOffset = encoded
	progress.CheckpointDigest = checkpoint.FrameDigest
	_, _, err = service.Store.UpdateProgress(operationID, progress)
	return err
}

func inspectMigrationExportArtifactPaths(
	outputPath, partialPath string,
	phase MigrationPhase,
) error {
	if !validMigrationAbsolutePath(outputPath) ||
		!validMigrationAbsolutePath(partialPath) ||
		filepath.Dir(outputPath) != filepath.Dir(partialPath) {
		return ErrMigrationRequestInvalid
	}
	if _, err := os.Lstat(outputPath); err == nil {
		if phase != MigrationPhaseSealing && phase != MigrationPhaseRecoverableFailure {
			return ErrMigrationOutputConflict
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(outputPath)
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrMigrationRequestInvalid
	}
	return nil
}

func migrationExportRegularPathExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return false, ErrMigrationOutputConflict
	}
	return true, nil
}

func bindMigrationExportArtifact(path string) (MigrationBundleFileBinding, error) {
	if !validMigrationAbsolutePath(path) {
		return MigrationBundleFileBinding{}, ErrMigrationRequestInvalid
	}
	file, err := openMigrationBundleNoFollow(path)
	if err != nil {
		return MigrationBundleFileBinding{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 || info.ModTime().UnixNano() <= 0 {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationBundleFileBinding{}, err
	}
	device, inode, err := migrationBundleFileDeviceInode(file)
	if err != nil || device == 0 || inode == 0 {
		return MigrationBundleFileBinding{}, ErrMigrationOperationInvalid
	}
	public, err := migration.InspectBundleHeader(file, info.Size())
	if err != nil || public.BundleID == "" || public.HeaderDigest.Validate() != nil {
		if err == nil {
			err = ErrMigrationOperationInvalid
		}
		return MigrationBundleFileBinding{}, err
	}
	pathDigest := sha256.Sum256([]byte("hideout.migration.bundle-path/v1\x00" + path))
	binding := MigrationBundleFileBinding{
		PathDigest:   migration.Digest("sha256:" + hex.EncodeToString(pathDigest[:])),
		HeaderDigest: public.HeaderDigest, Device: device, Inode: inode,
		Size: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano(),
	}
	if err := binding.Validate(); err != nil {
		return MigrationBundleFileBinding{}, err
	}
	return binding, nil
}

func (service MigrationService) ensureMigrationExportSealPhase(
	operationID string,
) (MigrationOperation, error) {
	operation, err := service.Store.Load(operationID)
	if err != nil {
		return MigrationOperation{}, err
	}
	writeEffect, err := migrationOperationEffect(operation, MigrationEffectWriteBundle)
	if err != nil || writeEffect.Status != MigrationEffectSucceeded {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	if operation.Phase == MigrationPhaseRecoverableFailure {
		operation, err = service.Store.TransitionPhase(
			operation.ID, MigrationPhaseSealing, nil,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	if operation.Phase != MigrationPhaseSealing {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	sealEffect, err := migrationOperationEffect(operation, MigrationEffectSealBundle)
	if err != nil {
		return MigrationOperation{}, err
	}
	if sealEffect.Status == MigrationEffectPending {
		operation, _, err = service.Store.BeginEffect(
			operation.ID, sealEffect.ID, sealEffect.Provider,
		)
		if err != nil {
			return MigrationOperation{}, err
		}
		sealEffect, err = migrationOperationEffect(operation, MigrationEffectSealBundle)
		if err != nil {
			return MigrationOperation{}, err
		}
	}
	if sealEffect.Status != MigrationEffectRunning {
		return MigrationOperation{}, ErrMigrationOperationInvalid
	}
	return operation, nil
}

func inspectMigrationSealedExport(
	ctx context.Context,
	path string,
	passphrase []byte,
	bundleID migration.BundleID,
) (migration.SealedBundleInspection, error) {
	file, err := os.Open(path)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 {
		return migration.SealedBundleInspection{}, ErrMigrationOperationInvalid
	}
	inspection, err := migration.InspectSealedBundle(ctx, file, info.Size(), passphrase)
	if err != nil {
		return migration.SealedBundleInspection{}, err
	}
	if inspection.Binding.BundleID != bundleID || !inspection.Summary.Sealed {
		return migration.SealedBundleInspection{}, ErrMigrationOperationMismatch
	}
	return inspection, nil
}

func publishMigrationExportFile(partialPath, outputPath string) error {
	if err := os.Link(partialPath, outputPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrMigrationOutputConflict
		}
		return err
	}
	if err := syncMigrationExportDirectory(filepath.Dir(outputPath)); err != nil {
		return err
	}
	if err := os.Remove(partialPath); err != nil {
		return err
	}
	return syncMigrationExportDirectory(filepath.Dir(outputPath))
}

func removePublishedMigrationExportPartial(partialPath, outputPath string) error {
	partialInfo, err := os.Lstat(partialPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	outputInfo, err := os.Lstat(outputPath)
	if err != nil || !partialInfo.Mode().IsRegular() ||
		!outputInfo.Mode().IsRegular() || !os.SameFile(partialInfo, outputInfo) {
		return ErrMigrationOutputConflict
	}
	if err := os.Remove(partialPath); err != nil {
		return err
	}
	return syncMigrationExportDirectory(filepath.Dir(outputPath))
}

func syncMigrationExportDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func migrationExportPartialPath(outputPath, operationID string) string {
	digest := sha256.Sum256([]byte(
		"hideout-migration-export-partial/v1\x00" + operationID + "\x00" + outputPath,
	))
	return filepath.Join(
		filepath.Dir(outputPath),
		".hideout-migration-"+hex.EncodeToString(digest[:12])+".partial",
	)
}

func migrationExportDerivedID(prefix string, values ...string) migration.OpaqueID {
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, "hideout-migration-export-id/v1\x00"+prefix)
	for _, value := range values {
		_, _ = io.WriteString(hasher, "\x00"+value)
	}
	return migration.OpaqueID(prefix + "_" + hex.EncodeToString(hasher.Sum(nil)[:16]))
}

func migrationExportBytesDigest(value []byte) migration.Digest {
	digest := sha256.Sum256(value)
	return migration.Digest("sha256:" + hex.EncodeToString(digest[:]))
}

func migrationExportProviderDiskName(ref migration.OpaqueID) string {
	digest := sha256.Sum256([]byte(ref))
	return "source-" + hex.EncodeToString(digest[:8])
}

func migrationExportFileOffset(file *os.File) uint64 {
	if file == nil {
		return 0
	}
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil || offset < 0 {
		return 0
	}
	return uint64(offset)
}

func (service MigrationService) markMigrationExportRecoverable(operationID string) {
	operation, err := service.Store.Load(operationID)
	if err != nil || operation.Kind != MigrationOperationExport ||
		migrationPhaseTerminal(operation.Phase) ||
		operation.Phase == MigrationPhaseRecoverableFailure ||
		operation.Phase == MigrationPhaseCancelling {
		return
	}
	_, _ = service.Store.TransitionPhase(
		operationID, MigrationPhaseRecoverableFailure, nil,
	)
}

func reflectMigrationManifestEqual(left, right migration.Manifest) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	defer clear(leftBytes)
	defer clear(rightBytes)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}
