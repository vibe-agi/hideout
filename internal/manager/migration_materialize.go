package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"sort"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/secrets"
)

// MigrationDestinationStager is the only provider authority needed while
// authenticated bundle records are being materialized. In particular, it does
// not receive the bundle path, file descriptor, passphrase, or record headers.
type MigrationDestinationStager interface {
	StageMigrationDestination(
		context.Context,
		backend.DestinationStageRequest,
	) (backend.DestinationStage, error)
}

type MigrationDestinationMaterializeRequest struct {
	BundlePath        string
	ExpectedFile      MigrationBundleFileBinding
	ExpectedBinding   migration.BundleBinding
	SecretInputHandle string
	ClientBinding     string
	Destination       backend.DestinationStageRequest
	OperationID       string
	SecretActions     []migration.SecretAction
}

type MigrationMaterializedProfile struct {
	ComponentID   migration.OpaqueID        `json:"componentId"`
	ContentDigest migration.Digest          `json:"contentDigest"`
	Snapshot      migration.PortableProfile `json:"snapshot"`
}

func (value MigrationMaterializedProfile) Validate() error {
	if !migrationValidOperationOpaqueID(value.ComponentID) ||
		value.ContentDigest.Validate() != nil || value.Snapshot.Validate() != nil {
		return ErrMigrationOperationInvalid
	}
	encoded, err := migration.EncodePortableProfile(value.Snapshot)
	if err != nil {
		return err
	}
	defer clear(encoded)
	digest := sha256.Sum256(encoded)
	if value.ContentDigest != migration.Digest("sha256:"+hex.EncodeToString(digest[:])) {
		return ErrMigrationOperationInvalid
	}
	return nil
}

type MigrationDestinationMaterialization struct {
	Stage           backend.DestinationStage       `json:"stage"`
	Profiles        []MigrationMaterializedProfile `json:"profiles"`
	PreparedSecrets []MigrationPreparedSecret      `json:"preparedSecrets,omitempty"`
}

func (value MigrationDestinationMaterialization) Validate() error {
	if value.Stage.Validate() != nil || len(value.Profiles) == 0 ||
		len(value.Profiles) > int(migration.HardMaxEnvironments) {
		return ErrMigrationOperationInvalid
	}
	var previous migration.OpaqueID
	for _, profile := range value.Profiles {
		if profile.Validate() != nil || (previous != "" && previous >= profile.ComponentID) {
			return ErrMigrationOperationInvalid
		}
		previous = profile.ComponentID
	}
	previous = ""
	for _, prepared := range value.PreparedSecrets {
		if prepared.Validate() != nil ||
			(previous != "" && previous >= prepared.SourceRef) {
			return ErrMigrationOperationInvalid
		}
		previous = prepared.SourceRef
	}
	return nil
}

// MigrationMaterializationService owns the narrow plaintext boundary between
// an immutable sealed bundle and an operation-owned destination stage. The
// one-shot import handle is consumed only after all public/file/plan facts have
// been revalidated and immediately before the reader derives bundle keys.
type MigrationMaterializationService struct {
	SecretInputs *MigrationSecretInputStore
	Cache        *MigrationInspectionCache
	Destination  MigrationDestinationStager
	Secrets      secrets.RuntimeStore
}

func (service MigrationMaterializationService) StageDestination(
	ctx context.Context,
	request MigrationDestinationMaterializeRequest,
) (backend.DestinationStage, error) {
	result, err := service.StageDestinationWithProfiles(ctx, request)
	return result.Stage, err
}

func (service MigrationMaterializationService) StageDestinationWithProfiles(
	ctx context.Context,
	request MigrationDestinationMaterializeRequest,
) (MigrationDestinationMaterialization, error) {
	if ctx == nil || service.SecretInputs == nil || service.Cache == nil ||
		service.Destination == nil || !validMigrationAbsolutePath(request.BundlePath) ||
		request.ExpectedFile.Validate() != nil ||
		validateMigrationBundleBinding(request.ExpectedBinding) != nil ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) || request.Destination.ReadComponent != nil ||
		validateMigrationSecretActions(request.SecretActions) != nil ||
		(migrationHasImportedSecretValues(request.SecretActions) &&
			(!operationIDPattern.MatchString(request.OperationID) || service.Secrets == nil)) {
		return MigrationDestinationMaterialization{}, ErrMigrationRequestInvalid
	}
	if err := ctx.Err(); err != nil {
		return MigrationDestinationMaterialization{}, err
	}

	file, observedFile, public, err := openAndBindMigrationBundleFile(request.BundlePath)
	if err != nil {
		return MigrationDestinationMaterialization{}, err
	}
	defer file.Close()
	if observedFile != request.ExpectedFile ||
		public.BundleID != request.ExpectedBinding.BundleID ||
		public.FormatVersion != request.ExpectedBinding.FormatVersion {
		return MigrationDestinationMaterialization{}, migration.ErrBundleChanged
	}
	inspection, err := service.Cache.Get(request.ExpectedBinding, observedFile)
	if err != nil {
		return MigrationDestinationMaterialization{}, err
	}
	if err := validateSealedBundleInspection(inspection); err != nil {
		return MigrationDestinationMaterialization{}, err
	}
	if err := migration.VerifySealedBundleFile(
		ctx, file, observedFile.Size, request.ExpectedBinding,
	); err != nil {
		return MigrationDestinationMaterialization{}, err
	}

	destination := request.Destination
	if err := validateMigrationDestinationAgainstManifest(
		destination, inspection.Manifest,
	); err != nil {
		return MigrationDestinationMaterialization{}, err
	}

	var staged backend.DestinationStage
	var profiles []MigrationMaterializedProfile
	var preparedSecrets []MigrationPreparedSecret
	err = service.SecretInputs.Consume(MigrationSecretInputUse{
		Handle: request.SecretInputHandle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: request.ClientBinding, BundleID: request.ExpectedBinding.BundleID,
		BundleFile: &observedFile,
	}, func(passphrase []byte) error {
		reader, readerErr := migration.NewReader(file, observedFile.Size, passphrase)
		if readerErr != nil {
			return readerErr
		}
		defer reader.Close()
		stream, streamErr := newMigrationBundleComponentStream(
			ctx, reader, inspection.Manifest, destination.Components, destination.Objects,
			request.ExpectedBinding, request.OperationID, request.SecretActions, service.Secrets,
		)
		if streamErr != nil {
			return streamErr
		}
		destination.ReadComponent = stream.ReadComponent
		if validateErr := destination.Validate(); validateErr != nil {
			return validateErr
		}
		result, stageErr := service.Destination.StageMigrationDestination(ctx, destination)
		if stageErr != nil {
			return stageErr
		}
		if finishErr := stream.Finish(); finishErr != nil {
			return finishErr
		}
		profiles, streamErr = stream.Profiles()
		if streamErr != nil {
			return streamErr
		}
		preparedSecrets, streamErr = stream.PreparedSecrets()
		if streamErr != nil {
			return streamErr
		}
		if validateErr := validateMigrationDestinationStageResult(destination, result); validateErr != nil {
			return validateErr
		}
		staged = result
		return nil
	})
	if err != nil {
		return MigrationDestinationMaterialization{}, err
	}

	after, afterPublic, err := bindOpenMigrationBundleFile(request.BundlePath, file)
	if err != nil {
		return MigrationDestinationMaterialization{}, err
	}
	if after != observedFile || afterPublic != public {
		return MigrationDestinationMaterialization{}, migration.ErrBundleChanged
	}
	if err := migration.VerifySealedBundleFile(
		ctx, file, after.Size, request.ExpectedBinding,
	); err != nil {
		return MigrationDestinationMaterialization{}, err
	}
	result := MigrationDestinationMaterialization{
		Stage: staged, Profiles: profiles, PreparedSecrets: preparedSecrets,
	}
	if err := result.Validate(); err != nil {
		return MigrationDestinationMaterialization{}, err
	}
	return result, nil
}

func validateMigrationDestinationAgainstManifest(
	request backend.DestinationStageRequest,
	manifest migration.Manifest,
) error {
	if request.ReadComponent != nil || manifest.Validate(migration.DefaultLimits()) != nil {
		return ErrMigrationPlanInvalid
	}
	validationCopy := request
	validationCopy.ReadComponent = func(
		context.Context,
		migration.OpaqueID,
		uint64,
		uint32,
		func(backend.MigrationExtent) error,
	) error {
		return errors.New("validation-only component reader must not execute")
	}
	if err := validationCopy.Validate(); err != nil {
		return err
	}

	environments := make(map[migration.OpaqueID]migration.EnvironmentSnapshot, len(manifest.Environments))
	for _, environment := range manifest.Environments {
		environments[environment.SourceEnvironmentRef] = environment
	}
	guestArchitecture, ok := migrationDestinationGuestArchitecture(manifest.SourceProduct.GuestArch)
	if !ok {
		return ErrMigrationPlanInvalid
	}
	selectedEnvironments := make(map[migration.OpaqueID]struct{}, len(request.Objects))
	for _, object := range request.Objects {
		environment, exists := environments[object.EnvironmentRef]
		if !exists || environment.Mode != migration.ExportModeFull ||
			object.Runtime != environment.Runtime ||
			object.GuestArchitecture != guestArchitecture ||
			object.GuestUser != environment.GuestUser ||
			object.ProfileComponent != environment.ProfileComponentID ||
			!migrationImageProvenanceEqual(object.ImageProvenance, environment.ImageProvenance) {
			return ErrMigrationPlanInvalid
		}
		selectedEnvironments[object.EnvironmentRef] = struct{}{}
	}

	disks := make(map[migration.OpaqueID]migration.DiskObject, len(manifest.DiskObjects))
	for _, disk := range manifest.DiskObjects {
		disks[disk.DiskID] = disk
	}
	selectedDisks := make(map[migration.OpaqueID]struct{}, len(request.Disks))
	for _, disk := range request.Disks {
		expected, exists := disks[disk.DiskID]
		if !exists || !migrationDiskObjectsEqual(disk, expected) {
			return ErrMigrationPlanInvalid
		}
		selectedDisks[disk.DiskID] = struct{}{}
	}

	edges := make(map[string]migration.DiskEdge, len(manifest.DiskEdges))
	for _, edge := range manifest.DiskEdges {
		edges[migrationDiskEdgeKey(edge)] = edge
	}
	for _, edge := range request.Edges {
		expected, exists := edges[migrationDiskEdgeKey(edge)]
		if !exists || edge != expected {
			return ErrMigrationPlanInvalid
		}
		if _, exists := selectedEnvironments[edge.EnvironmentRef]; !exists {
			return ErrMigrationPlanInvalid
		}
		if _, exists := selectedDisks[edge.DiskID]; !exists {
			return ErrMigrationPlanInvalid
		}
	}

	components := make(map[migration.OpaqueID]migration.ComponentIndexEntry, len(manifest.ComponentIndex))
	for _, component := range manifest.ComponentIndex {
		components[component.ComponentID] = component
	}
	var previousDiskLastRecord uint64
	previousDiskSeen := false
	for _, component := range request.Components {
		expected, exists := components[component.ComponentID]
		if !exists || expected.Kind != "disk" || component.Kind != expected.Kind ||
			component.DiskID != expected.DiskID ||
			component.LogicalBytes != expected.LogicalBytes ||
			component.ContentDigest != expected.ContentDigest ||
			(previousDiskSeen && expected.FirstRecord <= previousDiskLastRecord) {
			return ErrMigrationPlanInvalid
		}
		previousDiskLastRecord = expected.LastRecord
		previousDiskSeen = true
	}
	return nil
}

func migrationImageProvenanceEqual(left, right *migration.ImageProvenance) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func migrationDestinationGuestArchitecture(source string) (string, bool) {
	switch source {
	case "aarch64":
		return "linux/arm64", true
	case "x86_64":
		return "linux/amd64", true
	default:
		return "", false
	}
}

func migrationDiskObjectsEqual(left, right migration.DiskObject) bool {
	return left.DiskID == right.DiskID && left.Role == right.Role &&
		left.Format == right.Format && left.LogicalBytes == right.LogicalBytes &&
		left.AllocatedBytesHint == right.AllocatedBytesHint &&
		left.ContentDigest == right.ContentDigest &&
		left.Provider.Name == right.Provider.Name &&
		left.Provider.Kind == right.Provider.Kind &&
		slices.Equal(left.Provider.Features, right.Provider.Features)
}

func migrationDiskEdgeKey(edge migration.DiskEdge) string {
	return string(edge.EnvironmentRef) + "\x00" + string(edge.DiskID)
}

func validateMigrationDestinationStageResult(
	request backend.DestinationStageRequest,
	stage backend.DestinationStage,
) error {
	if err := stage.Validate(); err != nil || stage.Binding != request.Binding ||
		stage.StageHandle != request.StagingHandle ||
		len(stage.Checkpoints) != len(request.Components) {
		return backend.ErrMigrationProviderResponse
	}
	expectedHandles := make([]migration.OpaqueID, 0, len(request.Objects)+len(request.Components))
	for _, object := range request.Objects {
		expectedHandles = append(expectedHandles, object.BackendIdentity)
	}
	diskRoles := make(map[migration.OpaqueID]migration.DiskRole, len(request.Disks))
	for _, disk := range request.Disks {
		diskRoles[disk.DiskID] = disk.Role
	}
	for _, component := range request.Components {
		if diskRoles[component.DiskID] == migration.DiskRoleAttached {
			expectedHandles = append(expectedHandles, component.BackendIdentity)
		}
	}
	slices.Sort(expectedHandles)
	if !slices.Equal(stage.ObjectHandles, expectedHandles) {
		return backend.ErrMigrationProviderResponse
	}
	for index, component := range request.Components {
		checkpoint := stage.Checkpoints[index]
		if checkpoint.ComponentID != component.ComponentID ||
			checkpoint.NextOffset != component.LogicalBytes ||
			checkpoint.ContentDigest != component.ContentDigest {
			return backend.ErrMigrationProviderResponse
		}
	}
	return nil
}

type migrationBundleComponentStream struct {
	ctx              context.Context
	reader           *migration.Reader
	components       map[migration.OpaqueID]migration.ComponentIndexEntry
	selected         map[migration.OpaqueID]struct{}
	selectedProfiles map[migration.OpaqueID]struct{}
	profiles         map[migration.OpaqueID]MigrationMaterializedProfile
	secretPreparer   *migrationImportSecretPreparer
	expected         migration.BundleBinding

	nextSequence  uint64
	lastRequested migration.OpaqueID
	completion    migration.Digest
	finished      bool
}

func newMigrationBundleComponentStream(
	ctx context.Context,
	reader *migration.Reader,
	manifest migration.Manifest,
	selected []backend.MigrationDestinationComponent,
	objects []backend.MigrationDestinationObject,
	expected migration.BundleBinding,
	operationID string,
	secretActions []migration.SecretAction,
	secretStore secrets.RuntimeStore,
) (*migrationBundleComponentStream, error) {
	if ctx == nil || reader == nil || validateMigrationBundleBinding(expected) != nil ||
		len(selected) == 0 {
		return nil, ErrMigrationRequestInvalid
	}
	components := make(map[migration.OpaqueID]migration.ComponentIndexEntry, len(manifest.ComponentIndex))
	for _, component := range manifest.ComponentIndex {
		components[component.ComponentID] = component
	}
	selectedIDs := make(map[migration.OpaqueID]struct{}, len(selected))
	for _, component := range selected {
		entry, exists := components[component.ComponentID]
		if !exists || entry.Kind != "disk" {
			return nil, ErrMigrationPlanInvalid
		}
		selectedIDs[component.ComponentID] = struct{}{}
	}
	selectedProfiles := make(map[migration.OpaqueID]struct{}, len(objects))
	for _, object := range objects {
		entry, exists := components[object.ProfileComponent]
		if !exists || entry.Kind != "profile" || entry.RecordCount == 0 ||
			entry.LogicalBytes == 0 || entry.LogicalBytes > migration.MaxPortableProfileBytes {
			return nil, ErrMigrationPlanInvalid
		}
		selectedProfiles[object.ProfileComponent] = struct{}{}
	}
	if len(selectedProfiles) == 0 {
		return nil, ErrMigrationPlanInvalid
	}
	secretPreparer, err := newMigrationImportSecretPreparer(
		ctx, operationID, secretActions, manifest, secretStore,
	)
	if err != nil {
		return nil, err
	}
	return &migrationBundleComponentStream{
		ctx: ctx, reader: reader, components: components, selected: selectedIDs,
		selectedProfiles: selectedProfiles,
		profiles:         make(map[migration.OpaqueID]MigrationMaterializedProfile, len(selectedProfiles)),
		secretPreparer:   secretPreparer,
		expected:         expected,
	}, nil
}

func (stream *migrationBundleComponentStream) ReadComponent(
	ctx context.Context,
	componentID migration.OpaqueID,
	resumeOffset uint64,
	maxChunkBytes uint32,
	emit func(backend.MigrationExtent) error,
) error {
	if stream == nil || stream.finished || ctx == nil || emit == nil ||
		maxChunkBytes == 0 || maxChunkBytes > migration.HardMaxChunkBytes {
		return backend.ErrMigrationProviderRequest
	}
	if err := stream.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, selected := stream.components[componentID]
	_, permitted := stream.selected[componentID]
	if !selected || !permitted || entry.Kind != "disk" ||
		resumeOffset > entry.LogicalBytes ||
		(stream.lastRequested != "" && stream.lastRequested >= componentID) ||
		stream.nextSequence > entry.FirstRecord {
		return backend.ErrMigrationProviderRequest
	}
	stream.lastRequested = componentID

	for stream.nextSequence < entry.FirstRecord {
		record, err := stream.nextRecord()
		if err != nil {
			return err
		}
		if err := stream.consumeSkippedRecord(record); err != nil {
			return err
		}
	}

	logicalOffset := uint64(0)
	for stream.nextSequence <= entry.LastRecord {
		record, err := stream.nextRecord()
		if err != nil {
			return err
		}
		if err := func() error {
			defer clear(record.Plaintext)
			if record.Header.ComponentID != componentID {
				return migrationComponentStreamError(
					componentID, record.Sequence,
					errors.New("component record range was substituted"),
				)
			}
			if record.Header.Type == migration.RecordCheckpoint {
				return nil
			}
			if record.Header.LogicalOffset != logicalOffset ||
				record.Header.PlaintextLength > entry.LogicalBytes-logicalOffset {
				return migrationComponentStreamError(
					componentID, record.Sequence,
					errors.New("component logical range is invalid"),
				)
			}
			end := logicalOffset + record.Header.PlaintextLength
			if logicalOffset < resumeOffset && end > resumeOffset {
				return backend.ErrMigrationProviderRequest
			}
			if end > resumeOffset {
				if err := stream.emitRecord(
					ctx, record, maxChunkBytes, emit,
				); err != nil {
					return err
				}
			}
			logicalOffset = end
			return nil
		}(); err != nil {
			return err
		}
	}
	if logicalOffset != entry.LogicalBytes {
		return migrationComponentStreamError(
			componentID, entry.LastRecord,
			errors.New("component stream ended before its declared logical size"),
		)
	}
	return nil
}

func (stream *migrationBundleComponentStream) emitRecord(
	ctx context.Context,
	record migration.Record,
	maxChunkBytes uint32,
	emit func(backend.MigrationExtent) error,
) error {
	switch record.Header.Type {
	case migration.RecordDataChunk, migration.RecordRawChunk:
		if err := stream.ctx.Err(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Logical digests bind the canonical extent boundaries. Splitting an
		// authenticated data record would silently change that digest, so a
		// destination with a smaller chunk ceiling is incompatible and must be
		// rejected by preflight instead of being fed a different extent stream.
		if record.Header.PlaintextLength > uint64(maxChunkBytes) {
			return backend.ErrMigrationProviderRequest
		}
		extent := backend.MigrationExtent{
			Kind: migration.ExtentData, LogicalOffset: record.Header.LogicalOffset,
			Length: record.Header.PlaintextLength, Data: record.Plaintext,
		}
		if err := extent.Validate(maxChunkBytes); err != nil {
			return err
		}
		return emit(extent)
	case migration.RecordZeroExtent, migration.RecordHoleExtent:
		kind := migration.ExtentZero
		if record.Header.Type == migration.RecordHoleExtent {
			kind = migration.ExtentHole
		}
		extent := backend.MigrationExtent{
			Kind: kind, LogicalOffset: record.Header.LogicalOffset,
			Length: record.Header.PlaintextLength,
		}
		if err := extent.Validate(maxChunkBytes); err != nil {
			return err
		}
		return emit(extent)
	default:
		return migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("disk component contains a non-extent record"),
		)
	}
}

func (stream *migrationBundleComponentStream) Finish() error {
	if stream == nil || stream.finished {
		return ErrMigrationRequestInvalid
	}
	for {
		if err := stream.ctx.Err(); err != nil {
			return err
		}
		record, err := stream.nextRecord()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if record.Header.Type == migration.RecordCompletion {
			stream.completion = record.FrameDigest
		}
		if err := stream.consumeSkippedRecord(record); err != nil {
			return err
		}
	}
	summary, err := stream.reader.Summary()
	if err != nil {
		return err
	}
	if summary.BundleID != stream.expected.BundleID || !summary.Sealed ||
		summary.ManifestDigest != stream.expected.ManifestDigest ||
		stream.completion != stream.expected.CompletionDigest {
		return migrationComponentStreamError(
			"", stream.nextSequence,
			errors.New("materialized stream does not match the sealed binding"),
		)
	}
	stream.finished = true
	return nil
}

func (stream *migrationBundleComponentStream) PreparedSecrets() (
	[]MigrationPreparedSecret,
	error,
) {
	if stream == nil || !stream.finished {
		return nil, ErrMigrationOperationInvalid
	}
	return stream.secretPreparer.Prepared()
}

func (stream *migrationBundleComponentStream) Profiles() (
	[]MigrationMaterializedProfile,
	error,
) {
	if stream == nil || !stream.finished || len(stream.profiles) != len(stream.selectedProfiles) {
		return nil, ErrMigrationOperationInvalid
	}
	profiles := make([]MigrationMaterializedProfile, 0, len(stream.profiles))
	for _, value := range stream.profiles {
		value.Snapshot = value.Snapshot.Clone()
		profiles = append(profiles, value)
	}
	sort.Slice(profiles, func(left, right int) bool {
		return profiles[left].ComponentID < profiles[right].ComponentID
	})
	return profiles, nil
}

func (stream *migrationBundleComponentStream) consumeSkippedRecord(
	record migration.Record,
) error {
	defer clear(record.Plaintext)
	if stream == nil {
		return ErrMigrationOperationInvalid
	}
	consumed, err := stream.secretPreparer.Consume(record)
	if err != nil {
		return err
	}
	if consumed {
		return nil
	}
	if _, selected := stream.selectedProfiles[record.Header.ComponentID]; !selected {
		return nil
	}
	entry, exists := stream.components[record.Header.ComponentID]
	if !exists || entry.Kind != "profile" || entry.RecordCount == 0 ||
		record.Sequence < entry.FirstRecord || record.Sequence > entry.LastRecord ||
		record.Header.ComponentID != entry.ComponentID {
		return migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("portable profile component shape is invalid"),
		)
	}
	if record.Header.Type == migration.RecordCheckpoint {
		if _, exists := stream.profiles[record.Header.ComponentID]; !exists ||
			record.Sequence == entry.FirstRecord {
			return migrationComponentStreamError(
				record.Header.ComponentID, record.Sequence,
				errors.New("portable profile checkpoint precedes metadata"),
			)
		}
		return nil
	}
	if record.Header.Type != migration.RecordMetadata ||
		record.Sequence != entry.FirstRecord || record.Header.Ordinal != 0 ||
		record.Header.LogicalOffset != 0 || record.Header.PlaintextLength != entry.LogicalBytes {
		return migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("portable profile metadata shape is invalid"),
		)
	}
	if _, duplicate := stream.profiles[record.Header.ComponentID]; duplicate {
		return migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("portable profile component is duplicated"),
		)
	}
	digest := sha256.Sum256(record.Plaintext)
	contentDigest := migration.Digest("sha256:" + hex.EncodeToString(digest[:]))
	if contentDigest != entry.ContentDigest {
		return migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("portable profile component digest changed"),
		)
	}
	snapshot, err := migration.DecodePortableProfile(record.Plaintext)
	if err != nil {
		return migrationComponentStreamError(record.Header.ComponentID, record.Sequence, err)
	}
	stream.profiles[record.Header.ComponentID] = MigrationMaterializedProfile{
		ComponentID: record.Header.ComponentID, ContentDigest: contentDigest,
		Snapshot: snapshot,
	}
	return nil
}

func (stream *migrationBundleComponentStream) nextRecord() (migration.Record, error) {
	record, err := stream.reader.Next()
	if err != nil {
		return migration.Record{}, err
	}
	if record.Sequence != stream.nextSequence {
		clear(record.Plaintext)
		return migration.Record{}, migrationComponentStreamError(
			record.Header.ComponentID, record.Sequence,
			errors.New("materialization record sequence changed"),
		)
	}
	stream.nextSequence++
	return record, nil
}

func migrationComponentStreamError(
	componentID migration.OpaqueID,
	sequence uint64,
	cause error,
) error {
	return &migration.Error{
		Code: migration.CodeCorruptBundle, Sequence: sequence, SequenceKnown: true,
		ComponentID: string(componentID), RecoveryRequired: true, Cause: cause,
	}
}
