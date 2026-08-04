package migration

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"sync"
)

type WriterOptions struct {
	BundleID   BundleID
	CreatedAt  string
	KDF        KDFParameters
	Limits     Limits
	Random     io.Reader
	Passphrase []byte
}

type RecordInput struct {
	Type          RecordType
	Flags         RecordFlags
	ComponentID   OpaqueID
	Ordinal       uint64
	LogicalOffset uint64
	Plaintext     []byte
	ExtentLength  uint64
}

type RecordReceipt struct {
	Sequence     uint64
	Offset       uint64
	FrameDigest  Digest
	PrefixDigest Digest
	Header       PrivateRecordHeader
}

type BundleSummary struct {
	BundleID       BundleID
	Sealed         bool
	RecordCount    uint64
	LogicalBytes   uint64
	EncodedBytes   uint64
	PrefixDigest   Digest
	ManifestDigest Digest
}

type componentPosition struct {
	nextOrdinal uint64
	nextOffset  uint64
}

// Writer appends independently authenticated records. Any short/failed write
// permanently poisons the instance; recovery opens the partial file separately.
type Writer struct {
	mu sync.Mutex

	output io.Writer
	random io.Reader
	limits Limits
	header PublicHeader

	masterKey      []byte
	prologueDigest [sha256.Size]byte
	headerDigest   [sha256.Size]byte
	prefix         hash.Hash
	previousDigest [sha256.Size]byte
	nonces         *NonceTracker
	components     map[OpaqueID]componentPosition

	nextSequence   uint64
	payloadRecords uint64
	logicalBytes   uint64
	encodedBytes   uint64
	offset         uint64
	sealed         bool
	closed         bool
	failed         bool
}

func NewWriter(output io.Writer, options WriterOptions) (*Writer, error) {
	if output == nil {
		return nil, fmt.Errorf("%w: bundle output is nil", ErrInvalidBundle)
	}
	if _, err := ParseBundleID(string(options.BundleID)); err != nil {
		return nil, err
	}
	if err := options.KDF.Validate(); err != nil {
		return nil, err
	}
	if err := options.Limits.Validate(); err != nil {
		return nil, err
	}
	masterKey, err := randomBytes(options.Random, MasterKeyBytes)
	if err != nil {
		return nil, err
	}
	keepMasterKey := false
	defer func() {
		if !keepMasterKey {
			clear(masterKey)
		}
	}()
	salt, err := GenerateSalt(options.Random)
	if err != nil {
		return nil, err
	}
	wrapNonce, err := GenerateNonce(options.Random)
	if err != nil {
		return nil, err
	}
	header := PublicHeader{
		BundleID: options.BundleID, CreatedAt: options.CreatedAt,
		Suite: SuiteV1, KDF: options.KDF, Salt: salt,
		WrapNonce:        wrapNonce,
		WrappedMasterKey: make([]byte, MasterKeyBytes+AEADTagBytes),
		Limits:           options.Limits,
	}
	placeholderHeader, err := canonicalMarshal(header)
	if err != nil {
		return nil, err
	}
	if len(placeholderHeader) > int(HardMaxHeaderBytes) {
		return nil, fmt.Errorf("%w: public header exceeds v1", ErrLimitExceeded)
	}
	prologueBytes, err := encodePrologue(prologue{
		Major: BundleFormatVersion, HeaderLength: uint32(len(placeholderHeader)),
		SuiteID: formatSuiteIDV1,
	})
	if err != nil {
		return nil, err
	}
	associatedData, err := publicHeaderAAD(prologueBytes[:], header)
	if err != nil {
		return nil, err
	}
	header.WrappedMasterKey, err = WrapMasterKey(
		options.Passphrase, masterKey, options.KDF, salt, wrapNonce,
		associatedData,
	)
	if err != nil {
		return nil, err
	}
	headerBytes, err := canonicalMarshal(header)
	if err != nil {
		return nil, err
	}
	if len(headerBytes) != len(placeholderHeader) {
		return nil, fmt.Errorf("%w: wrapped header length changed", ErrInvalidBundle)
	}
	if err := validatePublicHeader(header); err != nil {
		return nil, err
	}
	if err := writeAllBytes(output, prologueBytes[:]); err != nil {
		return nil, fmt.Errorf("write migration prologue: %w", err)
	}
	if err := writeAllBytes(output, headerBytes); err != nil {
		return nil, fmt.Errorf("write migration public header: %w", err)
	}
	prefix := sha256.New()
	_, _ = prefix.Write(prologueBytes[:])
	_, _ = prefix.Write(headerBytes)
	writer := &Writer{
		output: output, random: options.Random, limits: options.Limits,
		header: header, masterKey: masterKey,
		prologueDigest: sha256.Sum256(prologueBytes[:]),
		headerDigest:   sha256.Sum256(headerBytes), prefix: prefix,
		nonces: NewNonceTracker(), components: make(map[OpaqueID]componentPosition),
		offset: uint64(PrologueSize + len(headerBytes)),
	}
	keepMasterKey = true
	return writer, nil
}

func (writer *Writer) Append(input RecordInput) (RecordReceipt, error) {
	if writer == nil {
		return RecordReceipt{}, fmt.Errorf("%w: bundle writer is nil", ErrInvalidBundle)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed || writer.failed || writer.sealed {
		return RecordReceipt{}, fmt.Errorf("%w: bundle writer is not appendable", ErrInvalidBundle)
	}
	if input.Type == RecordFinalManifest || input.Type == RecordCompletion ||
		!validRecordType(input.Type) {
		return RecordReceipt{}, fmt.Errorf("%w: reserved or unknown record type", ErrInvalidBundle)
	}
	return writer.appendInput(input)
}

func (writer *Writer) appendInput(input RecordInput) (RecordReceipt, error) {
	if _, err := ParseOpaqueID(string(input.ComponentID)); err != nil {
		return RecordReceipt{}, err
	}
	position, exists := writer.components[input.ComponentID]
	if (!exists && input.Ordinal != 0) || (exists && input.Ordinal != position.nextOrdinal) {
		return RecordReceipt{}, fmt.Errorf("%w: component ordinal is not sequential", ErrCorruptBundle)
	}
	if recordHasLogicalExtent(input.Type) && input.LogicalOffset != position.nextOffset {
		return RecordReceipt{}, fmt.Errorf("%w: component logical extent is not sequential", ErrCorruptBundle)
	}

	var plaintextLength uint64
	var plaintextDigest Digest
	var encoded []byte
	var err error
	if input.Type == RecordZeroExtent || input.Type == RecordHoleExtent {
		if len(input.Plaintext) != 0 || input.ExtentLength == 0 {
			return RecordReceipt{}, fmt.Errorf("%w: sparse record input is invalid", ErrInvalidBundle)
		}
		plaintextLength = input.ExtentLength
		plaintextDigest = digestZeros(input.ExtentLength)
	} else {
		if input.ExtentLength != 0 || len(input.Plaintext) == 0 {
			return RecordReceipt{}, fmt.Errorf("%w: payload record input is invalid", ErrInvalidBundle)
		}
		plaintextLength = uint64(len(input.Plaintext))
		plaintextDigest = digestBytes(input.Plaintext)
		encoded, err = encodeRecordPayload(input.Type, input.Plaintext)
		if err != nil {
			return RecordReceipt{}, err
		}
		defer clear(encoded)
	}
	header := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: input.Type, Flags: input.Flags,
		ComponentID: input.ComponentID, Ordinal: input.Ordinal,
		LogicalOffset: input.LogicalOffset, PlaintextLength: plaintextLength,
		EncodedLength: uint64(len(encoded)), PlaintextDigest: plaintextDigest,
	}
	if err := header.Validate(writer.limits); err != nil {
		return RecordReceipt{}, err
	}
	receipt, err := writer.appendPrepared(header, encoded)
	if err != nil {
		return RecordReceipt{}, err
	}
	position.nextOrdinal++
	if recordHasLogicalExtent(input.Type) {
		position.nextOffset += plaintextLength
	}
	writer.components[input.ComponentID] = position
	return receipt, nil
}

func (writer *Writer) appendPrepared(
	header PrivateRecordHeader,
	encoded []byte,
) (RecordReceipt, error) {
	if writer.nextSequence >= writer.limits.MaxPayloadRecords+2 {
		return RecordReceipt{}, fmt.Errorf("%w: record count exceeds v1", ErrLimitExceeded)
	}
	if recordHasLogicalExtent(header.Type) &&
		writer.payloadRecords >= writer.limits.MaxPayloadRecords {
		return RecordReceipt{}, fmt.Errorf("%w: payload record count exceeds v1", ErrLimitExceeded)
	}
	nextLogicalBytes, err := checkedLogicalBytes(
		writer.logicalBytes, header, writer.limits,
	)
	if err != nil {
		return RecordReceipt{}, err
	}
	privateHeader, err := canonicalMarshal(header)
	if err != nil {
		return RecordReceipt{}, err
	}
	if len(privateHeader) == 0 || len(privateHeader) > MaxPrivateHeaderBytes {
		return RecordReceipt{}, fmt.Errorf("%w: private record header exceeds v1", ErrLimitExceeded)
	}
	privateDigest := sha256.Sum256(privateHeader)
	plaintext := make([]byte, 4+len(privateHeader)+len(encoded))
	binary.BigEndian.PutUint32(plaintext[0:4], uint32(len(privateHeader)))
	copy(plaintext[4:], privateHeader)
	copy(plaintext[4+len(privateHeader):], encoded)
	defer clear(plaintext)

	key, err := DeriveRecordKey(writer.masterKey, writer.header.BundleID, writer.nextSequence)
	if err != nil {
		return RecordReceipt{}, err
	}
	defer clear(key)
	nonce, err := GenerateNonce(writer.random)
	if err != nil {
		return RecordReceipt{}, err
	}
	defer clear(nonce)
	if err := writer.nonces.Add(nonce); err != nil {
		return RecordReceipt{}, err
	}
	public := frameHeader{
		Sequence:            writer.nextSequence,
		CiphertextLength:    uint64(len(plaintext) + AEADTagBytes),
		PreviousDigest:      writer.previousDigest,
		PrivateHeaderDigest: privateDigest,
	}
	copy(public.Nonce[:], nonce)
	publicBytes, err := encodeFrameHeader(public)
	if err != nil {
		return RecordReceipt{}, err
	}
	associatedData := frameAssociatedData(
		writer.prologueDigest, writer.headerDigest, publicBytes[:],
	)
	ciphertext, err := SealRecord(key, nonce, associatedData, plaintext)
	if err != nil {
		return RecordReceipt{}, err
	}
	defer clear(ciphertext)
	offset := writer.offset
	if err := writer.writeFrameBytes(publicBytes[:], ciphertext); err != nil {
		return RecordReceipt{}, err
	}
	frameHasher := sha256.New()
	_, _ = frameHasher.Write(publicBytes[:])
	_, _ = frameHasher.Write(ciphertext)
	var frameDigest [sha256.Size]byte
	copy(frameDigest[:], frameHasher.Sum(nil))
	_, _ = writer.prefix.Write(publicBytes[:])
	_, _ = writer.prefix.Write(ciphertext)
	writer.previousDigest = frameDigest
	writer.nextSequence++
	if recordHasLogicalExtent(header.Type) {
		writer.payloadRecords++
	}
	writer.logicalBytes = nextLogicalBytes
	writer.encodedBytes += uint64(len(publicBytes) + len(ciphertext))
	return RecordReceipt{
		Sequence: public.Sequence, Offset: offset,
		FrameDigest:  rawDigestToDigest(frameDigest),
		PrefixDigest: rawDigestToDigest(currentDigest(writer.prefix)),
		Header:       header,
	}, nil
}

func (writer *Writer) writeFrameBytes(header, ciphertext []byte) error {
	if err := writeAllBytes(writer.output, header); err != nil {
		writer.failed = true
		return fmt.Errorf("write migration frame header: %w", err)
	}
	writer.offset += uint64(len(header))
	if err := writeAllBytes(writer.output, ciphertext); err != nil {
		writer.failed = true
		return fmt.Errorf("write migration frame ciphertext: %w", err)
	}
	writer.offset += uint64(len(ciphertext))
	return nil
}

func (writer *Writer) Seal(manifest []byte) (BundleSummary, error) {
	if writer == nil {
		return BundleSummary{}, fmt.Errorf("%w: bundle writer is nil", ErrInvalidBundle)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed || writer.failed || writer.sealed {
		return BundleSummary{}, fmt.Errorf("%w: bundle writer cannot seal", ErrInvalidBundle)
	}
	canonicalManifest, err := canonicalizeJSON(manifest, writer.limits.MaxManifestBytes)
	if err != nil {
		return BundleSummary{}, err
	}
	defer clear(canonicalManifest)
	if err := validateManifestEnvelope(canonicalManifest, writer.header.BundleID); err != nil {
		return BundleSummary{}, err
	}
	manifestHeader := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordFinalManifest,
		ComponentID: "component_manifest", PlaintextLength: uint64(len(canonicalManifest)),
		EncodedLength:   uint64(len(canonicalManifest)),
		PlaintextDigest: digestBytes(canonicalManifest),
	}
	if err := manifestHeader.Validate(writer.limits); err != nil {
		return BundleSummary{}, err
	}
	manifestReceipt, err := writer.appendPrepared(manifestHeader, canonicalManifest)
	if err != nil {
		return BundleSummary{}, err
	}
	// Once FinalManifest is appended, retrying Seal would append a second final
	// sequence. Keep this writer poisoned unless the completion and trailer also
	// land; recovery must reopen and scan the partial artifact instead.
	writer.failed = true
	completion := Completion{
		Schema: "hideout.migration-completion/v1", BundleID: writer.header.BundleID,
		ManifestSequence:    manifestReceipt.Sequence,
		ManifestFrameDigest: manifestReceipt.FrameDigest,
		RecordCount:         writer.nextSequence + 1,
		PrefixDigest:        rawDigestToDigest(currentDigest(writer.prefix)),
		LogicalBytes:        writer.logicalBytes, EncodedBytes: writer.encodedBytes,
	}
	completionBytes, err := canonicalMarshal(completion)
	if err != nil {
		return BundleSummary{}, err
	}
	defer clear(completionBytes)
	completionHeader := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordCompletion,
		ComponentID:     "component_completion",
		PlaintextLength: uint64(len(completionBytes)),
		EncodedLength:   uint64(len(completionBytes)),
		PlaintextDigest: digestBytes(completionBytes),
	}
	if err := completionHeader.Validate(writer.limits); err != nil {
		return BundleSummary{}, err
	}
	completionReceipt, err := writer.appendPrepared(completionHeader, completionBytes)
	if err != nil {
		return BundleSummary{}, err
	}
	completionFrameDigest, err := digestToRaw(completionReceipt.FrameDigest)
	if err != nil {
		return BundleSummary{}, err
	}
	fullPrefix := currentDigest(writer.prefix)
	trailerBytes := encodeTrailer(trailer{
		CompletionOffset:      completionReceipt.Offset,
		CompletionSequence:    completionReceipt.Sequence,
		CompletionFrameDigest: completionFrameDigest,
		PrefixDigest:          fullPrefix,
	})
	if err := writeAllBytes(writer.output, trailerBytes[:]); err != nil {
		writer.failed = true
		return BundleSummary{}, fmt.Errorf("write migration trailer: %w", err)
	}
	writer.offset += TrailerSize
	writer.sealed = true
	writer.failed = false
	return BundleSummary{
		BundleID: writer.header.BundleID, Sealed: true,
		RecordCount:    completion.RecordCount,
		LogicalBytes:   completion.LogicalBytes,
		EncodedBytes:   writer.offset,
		PrefixDigest:   rawDigestToDigest(fullPrefix),
		ManifestDigest: manifestHeader.PlaintextDigest,
	}, nil
}

func validateManifestEnvelope(input []byte, bundleID BundleID) error {
	var envelope struct {
		Schema        string   `json:"schema"`
		BundleID      BundleID `json:"bundleId"`
		FormatVersion uint16   `json:"formatVersion"`
	}
	if err := json.Unmarshal(input, &envelope); err != nil {
		return fmt.Errorf("%w: manifest envelope is invalid", ErrCorruptBundle)
	}
	if envelope.Schema != "hideout.migration-manifest/v1" ||
		envelope.BundleID != bundleID || envelope.FormatVersion != BundleFormatVersion {
		return fmt.Errorf("%w: manifest binding does not match bundle", ErrCorruptBundle)
	}
	return nil
}

func (writer *Writer) Close() error {
	if writer == nil {
		return nil
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	clear(writer.masterKey)
	writer.closed = true
	return nil
}

func recordHasLogicalExtent(recordType RecordType) bool {
	return recordType == RecordDataChunk || recordType == RecordRawChunk ||
		recordType == RecordZeroExtent || recordType == RecordHoleExtent
}

func checkedLogicalBytes(
	current uint64,
	header PrivateRecordHeader,
	limits Limits,
) (uint64, error) {
	if !recordHasLogicalExtent(header.Type) {
		return current, nil
	}
	if current > limits.MaxLogicalBytes ||
		header.PlaintextLength > limits.MaxLogicalBytes-current {
		return 0, fmt.Errorf("%w: aggregate logical data exceeds v1", ErrLimitExceeded)
	}
	return current + header.PlaintextLength, nil
}

func writeAllBytes(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}
