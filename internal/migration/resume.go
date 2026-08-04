package migration

import (
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

var (
	ErrCheckpointMismatch  = errors.New("migration checkpoint does not match durable state")
	ErrBundleAlreadySealed = errors.New("migration bundle is already sealed")
)

// CheckpointInput contains only Manager-owned facts. Cryptographic and stream
// counters are filled by Writer so a caller cannot accidentally assert a
// checkpoint that does not describe the bytes immediately before it.
type CheckpointInput struct {
	OperationID         OpaqueID
	CompletedComponents []OpaqueID
	CurrentComponent    OpaqueID
}

// AppendCheckpoint appends an encrypted, chain-bound resume checkpoint to the
// current component. The returned receipt identifies the checkpoint frame that
// a durable operation ledger may corroborate after the file is synced.
func (writer *Writer) AppendCheckpoint(input CheckpointInput) (
	Checkpoint,
	RecordReceipt,
	error,
) {
	if writer == nil {
		return Checkpoint{}, RecordReceipt{}, fmt.Errorf("%w: bundle writer is nil", ErrInvalidBundle)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed || writer.failed || writer.sealed || writer.nextSequence == 0 ||
		writer.output == nil || writer.prefix == nil || writer.nonces == nil {
		return Checkpoint{}, RecordReceipt{}, fmt.Errorf("%w: bundle writer is not checkpointable", ErrInvalidBundle)
	}
	if _, err := ParseOpaqueID(string(input.OperationID)); err != nil {
		return Checkpoint{}, RecordReceipt{}, err
	}
	if _, err := ParseOpaqueID(string(input.CurrentComponent)); err != nil {
		return Checkpoint{}, RecordReceipt{}, err
	}
	position, exists := writer.components[input.CurrentComponent]
	if !exists || position.nextOrdinal == 0 {
		return Checkpoint{}, RecordReceipt{}, fmt.Errorf("%w: checkpoint component has no payload", ErrInvalidBundle)
	}
	if err := validateCheckpointComponents(input.CompletedComponents); err != nil {
		return Checkpoint{}, RecordReceipt{}, err
	}
	checkpoint := Checkpoint{
		Schema: "hideout.migration-checkpoint/v1", BundleID: writer.header.BundleID,
		OperationID: input.OperationID, LastSequence: writer.nextSequence - 1,
		CompletedComponents:   append([]OpaqueID(nil), input.CompletedComponents...),
		CurrentComponent:      input.CurrentComponent,
		NextOrdinal:           position.nextOrdinal + 1,
		CompletedLogicalBytes: writer.logicalBytes,
		CompletedEncodedBytes: writer.offset,
		PrefixDigest:          rawDigestToDigest(currentDigest(writer.prefix)),
	}
	encoded, err := canonicalMarshal(checkpoint)
	if err != nil {
		return Checkpoint{}, RecordReceipt{}, err
	}
	defer clear(encoded)
	receipt, err := writer.appendInput(RecordInput{
		Type: RecordCheckpoint, ComponentID: input.CurrentComponent,
		Ordinal: position.nextOrdinal, Plaintext: encoded,
	})
	if err != nil {
		return Checkpoint{}, RecordReceipt{}, err
	}
	return checkpoint, receipt, nil
}

type ResumableFile interface {
	io.ReaderAt
	io.Writer
	io.Seeker
	Truncate(int64) error
	Sync() error
}

type ResumeComponentSpec struct {
	ComponentID  OpaqueID
	Kind         string
	LogicalBytes uint64
}

type ResumeOptions struct {
	BundleID                 BundleID
	OperationID              OpaqueID
	CreatedAt                string
	Passphrase               []byte
	Random                   io.Reader
	Limits                   Limits
	Components               []ResumeComponentSpec
	ExpectedCheckpointDigest Digest
}

type ResumeComponentState struct {
	ComponentID       OpaqueID
	FirstRecord       uint64
	LastRecord        uint64
	RecordCount       uint64
	NextOrdinal       uint64
	NextLogicalOffset uint64
	ContentBytes      uint64
	PayloadRecords    uint64
	ContentDigest     Digest
	RecordTypes       uint16
}

type ResumeResult struct {
	Writer                *Writer
	Checkpoint            *Checkpoint
	CheckpointFrameDigest Digest
	CheckpointOffset      uint64
	PrefixDigest          Digest
	TruncatedBytes        uint64
	Components            []ResumeComponentState
	DiskDigesters         map[OpaqueID]*LogicalDigester
}

type resumeScanState struct {
	prefix         hash.Hash
	previousDigest [sha256.Size]byte
	nonces         *NonceTracker
	components     map[OpaqueID]componentPosition
	facts          map[OpaqueID]ResumeComponentState
	digesters      map[OpaqueID]*LogicalDigester
	nextSequence   uint64
	payloadRecords uint64
	logicalBytes   uint64
	encodedBytes   uint64
	offset         uint64
	componentIndex int
}

type resumeSnapshot struct {
	state       resumeScanState
	checkpoint  Checkpoint
	frameDigest Digest
}

// ResumeWriter authenticates the original header and every complete frame,
// proves any durable ledger checkpoint, truncates only to the newest valid
// encrypted checkpoint, and returns a Writer whose nonce/chain/component state
// continues that exact bundle. No sealed artifact is accepted by this API.
func ResumeWriter(file ResumableFile, size int64, options ResumeOptions) (
	ResumeResult,
	error,
) {
	if file == nil || size < PrologueSize || options.Random == nil {
		return ResumeResult{}, fmt.Errorf("%w: resume input is invalid", ErrInvalidBundle)
	}
	if _, err := ParseBundleID(string(options.BundleID)); err != nil {
		return ResumeResult{}, err
	}
	if _, err := ParseOpaqueID(string(options.OperationID)); err != nil {
		return ResumeResult{}, err
	}
	limits := options.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	if err := limits.Validate(); err != nil {
		return ResumeResult{}, err
	}
	specs, specIndex, err := validateResumeComponentSpecs(options.Components, limits)
	if err != nil {
		return ResumeResult{}, err
	}
	if options.ExpectedCheckpointDigest != "" &&
		options.ExpectedCheckpointDigest.Validate() != nil {
		return ResumeResult{}, ErrCheckpointMismatch
	}

	prologueBytes := make([]byte, PrologueSize)
	if err := readAtFull(file, prologueBytes, 0); err != nil {
		return ResumeResult{}, ErrIncompleteBundle
	}
	prologueValue, err := decodePrologue(prologueBytes)
	if err != nil {
		return ResumeResult{}, err
	}
	headerEnd := uint64(PrologueSize) + uint64(prologueValue.HeaderLength)
	if headerEnd > uint64(size) {
		return ResumeResult{}, ErrIncompleteBundle
	}
	headerBytes := make([]byte, int(prologueValue.HeaderLength))
	if err := readAtFull(file, headerBytes, PrologueSize); err != nil {
		return ResumeResult{}, ErrIncompleteBundle
	}
	header, err := decodePublicHeader(headerBytes)
	if err != nil {
		return ResumeResult{}, err
	}
	if header.BundleID != options.BundleID || header.CreatedAt != options.CreatedAt ||
		header.Limits != limits {
		return ResumeResult{}, ErrCheckpointMismatch
	}
	aad, err := publicHeaderAAD(prologueBytes, header)
	if err != nil {
		return ResumeResult{}, err
	}
	masterBuffer, err := UnwrapMasterKey(
		options.Passphrase, header.WrappedMasterKey, header.KDF, header.Salt,
		header.WrapNonce, aad,
	)
	if err != nil {
		return ResumeResult{}, err
	}
	defer masterBuffer.Clear()
	var masterKey []byte
	if err := masterBuffer.Use(func(value []byte) error {
		masterKey = append([]byte(nil), value...)
		return nil
	}); err != nil {
		return ResumeResult{}, err
	}
	if size >= TrailerSize {
		trailerBytes := make([]byte, TrailerSize)
		if readAtFull(file, trailerBytes, size-TrailerSize) == nil {
			if _, trailerErr := decodeTrailer(trailerBytes); trailerErr == nil {
				clear(masterKey)
				return ResumeResult{}, ErrBundleAlreadySealed
			}
		}
		clear(trailerBytes)
	}
	keepMasterKey := false
	defer func() {
		if !keepMasterKey {
			clear(masterKey)
		}
	}()

	prefix := sha256.New()
	_, _ = prefix.Write(prologueBytes)
	_, _ = prefix.Write(headerBytes)
	prologueDigest := sha256.Sum256(prologueBytes)
	headerDigest := sha256.Sum256(headerBytes)
	state := resumeScanState{
		prefix: prefix, nonces: NewNonceTracker(),
		components: make(map[OpaqueID]componentPosition),
		facts:      make(map[OpaqueID]ResumeComponentState),
		digesters:  make(map[OpaqueID]*LogicalDigester),
		offset:     headerEnd, componentIndex: -1,
	}
	for _, spec := range specs {
		if spec.Kind != "disk" {
			continue
		}
		digester, err := NewLogicalDigester(spec.LogicalBytes)
		if err != nil {
			return ResumeResult{}, err
		}
		state.digesters[spec.ComponentID] = digester
	}
	baseState, err := cloneResumeScanState(state)
	if err != nil {
		return ResumeResult{}, err
	}
	var latest *resumeSnapshot
	expectedSeen := options.ExpectedCheckpointDigest == ""
	for state.offset < uint64(size) {
		before := state
		private, decoded, publicBytes, ciphertext, frameDigest, scanErr :=
			scanResumeFrame(
				file, uint64(size), header, prologueDigest, headerDigest,
				masterKey, specs, specIndex, &state,
			)
		if scanErr != nil {
			break
		}
		if private.Type == RecordFinalManifest || private.Type == RecordCompletion {
			clear(decoded)
			clear(ciphertext)
			clear(publicBytes)
			break
		}
		var checkpointValue *Checkpoint
		if private.Type == RecordCheckpoint {
			checkpoint, checkpointErr := decodeAndValidateResumeCheckpoint(
				decoded, header, options.OperationID, private,
				before.nextSequence, before, specs,
			)
			if checkpointErr != nil {
				clear(decoded)
				clear(ciphertext)
				clear(publicBytes)
				break
			}
			checkpointValue = &checkpoint
		}
		_, _ = state.prefix.Write(publicBytes)
		_, _ = state.prefix.Write(ciphertext)
		state.previousDigest = frameDigest
		frameBytes := uint64(len(publicBytes) + len(ciphertext))
		sequence := state.nextSequence
		state.offset += frameBytes
		state.nextSequence++
		state.encodedBytes += frameBytes
		if recordHasLogicalExtent(private.Type) {
			state.payloadRecords++
		}
		state.componentIndex = specIndex[private.ComponentID]
		if err := advanceResumeDigester(state.digesters, private, decoded); err != nil {
			clear(decoded)
			clear(ciphertext)
			clear(publicBytes)
			break
		}
		advanceResumeComponent(&state, private, sequence)
		if checkpointValue != nil {
			cloned, cloneErr := cloneResumeScanState(state)
			if cloneErr != nil {
				clear(decoded)
				clear(ciphertext)
				clear(publicBytes)
				return ResumeResult{}, cloneErr
			}
			frameDigestValue := rawDigestToDigest(frameDigest)
			latest = &resumeSnapshot{
				state: cloned, checkpoint: *checkpointValue, frameDigest: frameDigestValue,
			}
			if frameDigestValue == options.ExpectedCheckpointDigest {
				expectedSeen = true
			}
		}
		clear(decoded)
		clear(ciphertext)
		clear(publicBytes)
	}
	if !expectedSeen {
		return ResumeResult{}, ErrCheckpointMismatch
	}
	selected := baseState
	var checkpoint *Checkpoint
	var checkpointDigest Digest
	if latest != nil {
		selected = latest.state
		copyValue := latest.checkpoint
		copyValue.CompletedComponents = append([]OpaqueID(nil), copyValue.CompletedComponents...)
		checkpoint = &copyValue
		checkpointDigest = latest.frameDigest
	}
	if selected.offset > uint64(size) {
		return ResumeResult{}, ErrIncompleteBundle
	}
	if err := file.Truncate(int64(selected.offset)); err != nil {
		return ResumeResult{}, err
	}
	if _, err := file.Seek(int64(selected.offset), io.SeekStart); err != nil {
		return ResumeResult{}, err
	}
	if err := file.Sync(); err != nil {
		return ResumeResult{}, err
	}
	writer := &Writer{
		output: file, random: options.Random, limits: limits, header: header,
		masterKey:      masterKey,
		prologueDigest: prologueDigest,
		headerDigest:   headerDigest, prefix: selected.prefix,
		previousDigest: selected.previousDigest, nonces: selected.nonces,
		components: selected.components, nextSequence: selected.nextSequence,
		payloadRecords: selected.payloadRecords, logicalBytes: selected.logicalBytes,
		encodedBytes: selected.encodedBytes, offset: selected.offset,
	}
	keepMasterKey = true
	return ResumeResult{
		Writer: writer, Checkpoint: checkpoint,
		CheckpointFrameDigest: checkpointDigest, CheckpointOffset: selected.offset,
		PrefixDigest:   rawDigestToDigest(currentDigest(selected.prefix)),
		TruncatedBytes: uint64(size) - selected.offset,
		Components:     orderedResumeComponentStates(specs, selected.facts),
		DiskDigesters:  selected.digesters,
	}, nil
}

func scanResumeFrame(
	file io.ReaderAt,
	size uint64,
	header PublicHeader,
	prologueDigest [sha256.Size]byte,
	headerDigest [sha256.Size]byte,
	masterKey []byte,
	specs []ResumeComponentSpec,
	specIndex map[OpaqueID]int,
	state *resumeScanState,
) (PrivateRecordHeader, []byte, []byte, []byte, [sha256.Size]byte, error) {
	var frameDigest [sha256.Size]byte
	if state == nil || state.offset > size || size-state.offset < FrameHeaderSize {
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrIncompleteBundle
	}
	publicBytes := make([]byte, FrameHeaderSize)
	if err := readAtFull(file, publicBytes, int64(state.offset)); err != nil {
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrIncompleteBundle
	}
	public, err := decodeFrameHeader(publicBytes, header.Limits)
	if err != nil || public.Sequence != state.nextSequence ||
		public.PreviousDigest != state.previousDigest || state.nonces.Add(public.Nonce[:]) != nil {
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrCorruptBundle
	}
	remaining := size - state.offset - FrameHeaderSize
	if public.CiphertextLength > remaining {
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrIncompleteBundle
	}
	ciphertext := make([]byte, int(public.CiphertextLength))
	if err := readAtFull(file, ciphertext, int64(state.offset+FrameHeaderSize)); err != nil {
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrIncompleteBundle
	}
	key, err := DeriveRecordKey(masterKey, header.BundleID, public.Sequence)
	if err != nil {
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, err
	}
	aad := frameAssociatedData(prologueDigest, headerDigest, publicBytes)
	plaintext, openErr := OpenRecord(key, public.Nonce[:], aad, ciphertext)
	clear(aad)
	clear(key)
	if openErr != nil {
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, openErr
	}
	if len(plaintext) < 4 {
		clear(plaintext)
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrCorruptBundle
	}
	privateLength := uint64(binary.BigEndian.Uint32(plaintext[:4]))
	if privateLength == 0 || privateLength > MaxPrivateHeaderBytes ||
		privateLength > uint64(len(plaintext)-4) {
		clear(plaintext)
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrCorruptBundle
	}
	privateBytes := plaintext[4 : 4+privateLength]
	if sha256.Sum256(privateBytes) != public.PrivateHeaderDigest {
		clear(plaintext)
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrCorruptBundle
	}
	var private PrivateRecordHeader
	if err := decodeCanonicalJSON(privateBytes, MaxPrivateHeaderBytes, &private); err != nil ||
		private.Validate(header.Limits) != nil {
		clear(plaintext)
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrCorruptBundle
	}
	encoded := plaintext[4+privateLength:]
	if uint64(len(encoded)) != private.EncodedLength ||
		validateResumeRecordOrder(private, specs, specIndex, *state) != nil {
		clear(plaintext)
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, ErrCorruptBundle
	}
	nextLogical, err := checkedLogicalBytes(state.logicalBytes, private, header.Limits)
	if err != nil {
		clear(plaintext)
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, err
	}
	decoded, err := decodeRecordPayload(private, encoded, header.Limits)
	clear(plaintext)
	if err != nil {
		clear(ciphertext)
		return PrivateRecordHeader{}, nil, nil, nil, frameDigest, err
	}
	state.logicalBytes = nextLogical
	frameHasher := sha256.New()
	_, _ = frameHasher.Write(publicBytes)
	_, _ = frameHasher.Write(ciphertext)
	copy(frameDigest[:], frameHasher.Sum(nil))
	return private, decoded, publicBytes, ciphertext, frameDigest, nil
}

func decodeAndValidateResumeCheckpoint(
	decoded []byte,
	header PublicHeader,
	operationID OpaqueID,
	private PrivateRecordHeader,
	sequence uint64,
	before resumeScanState,
	specs []ResumeComponentSpec,
) (Checkpoint, error) {
	var checkpoint Checkpoint
	if err := decodeCanonicalJSON(decoded, header.Limits.MaxMetadataBytes, &checkpoint); err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Schema != "hideout.migration-checkpoint/v1" ||
		checkpoint.BundleID != header.BundleID || checkpoint.OperationID != operationID ||
		sequence == 0 || checkpoint.LastSequence != sequence-1 ||
		checkpoint.CurrentComponent != private.ComponentID ||
		checkpoint.NextOrdinal != private.Ordinal+1 ||
		checkpoint.CompletedLogicalBytes != before.logicalBytes ||
		checkpoint.CompletedEncodedBytes != before.offset ||
		checkpoint.PrefixDigest != rawDigestToDigest(currentDigest(before.prefix)) ||
		validateCheckpointComponents(checkpoint.CompletedComponents) != nil ||
		!checkpointCompletionMatchesSpecs(checkpoint, specs, before.facts) {
		return Checkpoint{}, ErrCheckpointMismatch
	}
	return checkpoint, nil
}

func validateResumeRecordOrder(
	header PrivateRecordHeader,
	specs []ResumeComponentSpec,
	specIndex map[OpaqueID]int,
	state resumeScanState,
) error {
	if header.Type == RecordFinalManifest || header.Type == RecordCompletion {
		return nil
	}
	index, exists := specIndex[header.ComponentID]
	if !exists || index < state.componentIndex || index > state.componentIndex+1 ||
		(state.componentIndex < 0 && index != 0) {
		return ErrCorruptBundle
	}
	position, seen := state.components[header.ComponentID]
	if (!seen && header.Ordinal != 0) || (seen && header.Ordinal != position.nextOrdinal) ||
		(recordHasLogicalExtent(header.Type) && header.LogicalOffset != position.nextOffset) {
		return ErrCorruptBundle
	}
	kind := specs[index].Kind
	if !resumeRecordTypeAllowed(kind, header.Type) ||
		(header.Type == RecordCheckpoint && !seen) {
		return ErrCorruptBundle
	}
	return nil
}

func resumeRecordTypeAllowed(kind string, recordType RecordType) bool {
	if recordType == RecordCheckpoint {
		return true
	}
	switch kind {
	case "disk":
		return recordType == RecordDataChunk || recordType == RecordRawChunk ||
			recordType == RecordZeroExtent || recordType == RecordHoleExtent
	case "secret-value":
		return recordType == RecordSecretValue
	case "profile", "environment", "provider-metadata":
		return recordType == RecordMetadata
	default:
		return false
	}
}

func advanceResumeComponent(
	state *resumeScanState,
	header PrivateRecordHeader,
	sequence uint64,
) {
	if header.Type == RecordFinalManifest || header.Type == RecordCompletion {
		return
	}
	position := state.components[header.ComponentID]
	position.nextOrdinal++
	if recordHasLogicalExtent(header.Type) {
		position.nextOffset += header.PlaintextLength
	}
	state.components[header.ComponentID] = position
	facts := state.facts[header.ComponentID]
	if facts.RecordCount == 0 {
		facts.ComponentID = header.ComponentID
		facts.FirstRecord = sequence
	}
	facts.LastRecord = sequence
	facts.RecordCount++
	facts.NextOrdinal = position.nextOrdinal
	facts.NextLogicalOffset = position.nextOffset
	if header.Type != RecordCheckpoint {
		facts.ContentBytes += header.PlaintextLength
		facts.PayloadRecords++
		if header.Type == RecordMetadata || header.Type == RecordSecretValue {
			if facts.PayloadRecords == 1 {
				facts.ContentDigest = header.PlaintextDigest
			} else {
				facts.ContentDigest = ""
			}
		}
	}
	facts.RecordTypes |= recordTypeMask(header.Type)
	state.facts[header.ComponentID] = facts
}

func advanceResumeDigester(
	digesters map[OpaqueID]*LogicalDigester,
	header PrivateRecordHeader,
	decoded []byte,
) error {
	digester := digesters[header.ComponentID]
	if digester == nil || !recordHasLogicalExtent(header.Type) {
		return nil
	}
	extent := Extent{
		LogicalOffset: header.LogicalOffset, Length: header.PlaintextLength,
	}
	switch header.Type {
	case RecordDataChunk, RecordRawChunk:
		extent.Kind = ExtentData
		extent.Data = decoded
	case RecordZeroExtent:
		extent.Kind = ExtentZero
	case RecordHoleExtent:
		extent.Kind = ExtentHole
	default:
		return ErrCorruptBundle
	}
	return digester.WriteExtent(extent)
}

func validateResumeComponentSpecs(
	input []ResumeComponentSpec,
	limits Limits,
) ([]ResumeComponentSpec, map[OpaqueID]int, error) {
	if len(input) == 0 || len(input) > maxManifestComponents {
		return nil, nil, ErrCheckpointMismatch
	}
	output := append([]ResumeComponentSpec(nil), input...)
	index := make(map[OpaqueID]int, len(output))
	for current, spec := range output {
		if _, err := ParseOpaqueID(string(spec.ComponentID)); err != nil ||
			!validManifestComponentKind(spec.Kind) || spec.LogicalBytes == 0 ||
			spec.LogicalBytes > limits.MaxLogicalBytes {
			return nil, nil, ErrCheckpointMismatch
		}
		if _, duplicate := index[spec.ComponentID]; duplicate {
			return nil, nil, ErrCheckpointMismatch
		}
		index[spec.ComponentID] = current
	}
	return output, index, nil
}

func validateCheckpointComponents(values []OpaqueID) error {
	if len(values) > maxManifestComponents {
		return ErrCheckpointMismatch
	}
	seen := make(map[OpaqueID]struct{}, len(values))
	for _, value := range values {
		if _, err := ParseOpaqueID(string(value)); err != nil {
			return ErrCheckpointMismatch
		}
		if _, duplicate := seen[value]; duplicate {
			return ErrCheckpointMismatch
		}
		seen[value] = struct{}{}
	}
	return nil
}

func checkpointCompletionMatchesSpecs(
	checkpoint Checkpoint,
	specs []ResumeComponentSpec,
	facts map[OpaqueID]ResumeComponentState,
) bool {
	if len(checkpoint.CompletedComponents) > len(specs) {
		return false
	}
	for index, componentID := range checkpoint.CompletedComponents {
		if specs[index].ComponentID != componentID {
			return false
		}
		fact, exists := facts[componentID]
		if !exists || fact.ContentBytes != specs[index].LogicalBytes {
			return false
		}
	}
	currentIndex := len(checkpoint.CompletedComponents)
	if currentIndex > 0 && checkpoint.CurrentComponent == checkpoint.CompletedComponents[currentIndex-1] {
		return true
	}
	return currentIndex < len(specs) && checkpoint.CurrentComponent == specs[currentIndex].ComponentID
}

func cloneResumeScanState(input resumeScanState) (resumeScanState, error) {
	prefix, err := cloneSHA256Hash(input.prefix)
	if err != nil {
		return resumeScanState{}, err
	}
	output := input
	output.prefix = prefix
	output.components = make(map[OpaqueID]componentPosition, len(input.components))
	for key, value := range input.components {
		output.components[key] = value
	}
	output.facts = make(map[OpaqueID]ResumeComponentState, len(input.facts))
	for key, value := range input.facts {
		output.facts[key] = value
	}
	output.digesters = make(map[OpaqueID]*LogicalDigester, len(input.digesters))
	for key, value := range input.digesters {
		cloned, err := cloneLogicalDigester(value)
		if err != nil {
			return resumeScanState{}, err
		}
		output.digesters[key] = cloned
	}
	output.nonces = NewNonceTracker()
	for value := range input.nonces.seen {
		output.nonces.seen[value] = struct{}{}
	}
	return output, nil
}

func cloneLogicalDigester(input *LogicalDigester) (*LogicalDigester, error) {
	if input == nil || input.hasher == nil {
		return nil, ErrCheckpointMismatch
	}
	hasher, err := cloneSHA256Hash(input.hasher)
	if err != nil {
		return nil, err
	}
	return &LogicalDigester{
		logicalSize: input.logicalSize, nextOffset: input.nextOffset,
		lastKind: input.lastKind, hasher: hasher, finished: input.finished,
	}, nil
}

func cloneSHA256Hash(input hash.Hash) (hash.Hash, error) {
	marshaler, ok := input.(encoding.BinaryMarshaler)
	if !ok {
		return nil, ErrCheckpointMismatch
	}
	state, err := marshaler.MarshalBinary()
	if err != nil {
		return nil, err
	}
	output := sha256.New()
	unmarshaler, ok := output.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, ErrCheckpointMismatch
	}
	if err := unmarshaler.UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return output, nil
}

func orderedResumeComponentStates(
	specs []ResumeComponentSpec,
	facts map[OpaqueID]ResumeComponentState,
) []ResumeComponentState {
	output := make([]ResumeComponentState, 0, len(facts))
	for _, spec := range specs {
		if value, exists := facts[spec.ComponentID]; exists {
			output = append(output, value)
		}
	}
	return output
}
