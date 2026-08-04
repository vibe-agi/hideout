package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"sync"
)

type Record struct {
	Sequence    uint64
	Offset      uint64
	Header      PrivateRecordHeader
	Plaintext   []byte
	FrameDigest Digest
}

// Reader authenticates one record at a time and retains only the bounded final
// manifest/completion needed to establish the sealed predicate.
type Reader struct {
	mu sync.Mutex

	input         io.ReaderAt
	size          int64
	header        PublicHeader
	limits        Limits
	trailer       trailer
	trailerOffset uint64

	masterKey      []byte
	prologueDigest [sha256.Size]byte
	headerDigest   [sha256.Size]byte
	prefix         hash.Hash
	previousDigest [sha256.Size]byte
	nonces         *NonceTracker
	components     map[OpaqueID]componentPosition

	offset         uint64
	nextSequence   uint64
	payloadRecords uint64
	logicalBytes   uint64
	encodedBytes   uint64

	manifest         []byte
	manifestSequence uint64
	manifestFrame    Digest
	completion       Completion
	completionSeen   bool
	scanComplete     bool
	closed           bool
	failure          error
	summary          BundleSummary
}

func NewReader(
	input io.ReaderAt,
	size int64,
	passphrase []byte,
) (*Reader, error) {
	if input == nil || size < PrologueSize+TrailerSize+1 {
		return nil, ErrIncompleteBundle
	}
	prologueBytes := make([]byte, PrologueSize)
	if err := readAtFull(input, prologueBytes, 0); err != nil {
		return nil, ErrIncompleteBundle
	}
	prologueValue, err := decodePrologue(prologueBytes)
	if err != nil {
		return nil, err
	}
	headerEnd := uint64(PrologueSize) + uint64(prologueValue.HeaderLength)
	if headerEnd > uint64(size)-TrailerSize {
		return nil, ErrIncompleteBundle
	}
	headerBytes := make([]byte, int(prologueValue.HeaderLength))
	if err := readAtFull(input, headerBytes, PrologueSize); err != nil {
		return nil, ErrIncompleteBundle
	}
	header, err := decodePublicHeader(headerBytes)
	if err != nil {
		return nil, err
	}
	trailerValue, trailerOffset, err := locateTrailer(input, size)
	if err != nil {
		return nil, err
	}
	if trailerValue.CompletionOffset < headerEnd ||
		trailerValue.CompletionOffset > trailerOffset ||
		trailerOffset-trailerValue.CompletionOffset < FrameHeaderSize ||
		trailerValue.CompletionSequence > header.Limits.MaxPayloadRecords+1 {
		return nil, fmt.Errorf("%w: trailer completion binding is out of range", ErrCorruptBundle)
	}
	associatedData, err := publicHeaderAAD(prologueBytes, header)
	if err != nil {
		return nil, err
	}
	masterBuffer, err := UnwrapMasterKey(
		passphrase, header.WrappedMasterKey, header.KDF, header.Salt,
		header.WrapNonce, associatedData,
	)
	if err != nil {
		return nil, err
	}
	var masterKey []byte
	if err := masterBuffer.Use(func(value []byte) error {
		masterKey = append([]byte(nil), value...)
		return nil
	}); err != nil {
		return nil, err
	}
	prefix := sha256.New()
	_, _ = prefix.Write(prologueBytes)
	_, _ = prefix.Write(headerBytes)
	return &Reader{
		input: input, size: size, header: header, limits: header.Limits,
		trailer: trailerValue, trailerOffset: trailerOffset,
		masterKey:      masterKey,
		prologueDigest: sha256.Sum256(prologueBytes),
		headerDigest:   sha256.Sum256(headerBytes), prefix: prefix,
		nonces: NewNonceTracker(), components: make(map[OpaqueID]componentPosition),
		offset: headerEnd,
	}, nil
}

func locateTrailer(input io.ReaderAt, size int64) (trailer, uint64, error) {
	exactOffset := size - TrailerSize
	exact := make([]byte, TrailerSize)
	if err := readAtFull(input, exact, exactOffset); err != nil {
		return trailer{}, 0, ErrIncompleteBundle
	}
	if string(exact[:8]) == BundleEndMagic {
		value, err := decodeTrailer(exact)
		return value, uint64(exactOffset), err
	}

	windowSize := int64(HardMaxMetadataBytes + TrailerSize)
	if size < windowSize {
		windowSize = size
	}
	window := make([]byte, int(windowSize))
	windowOffset := size - windowSize
	if err := readAtFull(input, window, windowOffset); err != nil {
		return trailer{}, 0, ErrIncompleteBundle
	}
	search := window
	for {
		index := bytes.LastIndex(search, []byte(BundleEndMagic))
		if index < 0 {
			break
		}
		if index+TrailerSize <= len(window) {
			candidate := window[index : index+TrailerSize]
			if _, err := decodeTrailer(candidate); err == nil &&
				windowOffset+int64(index)+TrailerSize < size {
				return trailer{}, 0, ErrTrailingData
			}
		}
		search = search[:index]
	}
	return trailer{}, 0, ErrIncompleteBundle
}

func (reader *Reader) Next() (record Record, err error) {
	if reader == nil {
		return Record{}, fmt.Errorf("%w: bundle reader is nil", ErrInvalidBundle)
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return Record{}, fmt.Errorf("%w: bundle reader is closed", ErrInvalidBundle)
	}
	if reader.failure != nil {
		return Record{}, reader.failure
	}
	defer func() {
		if err != nil && !errors.Is(err, io.EOF) {
			reader.failure = err
		}
	}()
	if reader.scanComplete {
		return Record{}, io.EOF
	}
	if reader.offset == reader.trailerOffset {
		if err := reader.finishScan(); err != nil {
			return Record{}, err
		}
		return Record{}, io.EOF
	}
	if reader.offset > reader.trailerOffset ||
		reader.trailerOffset-reader.offset < FrameHeaderSize {
		return Record{}, fmt.Errorf("%w: frame overlaps the trailer", ErrCorruptBundle)
	}
	publicBytes := make([]byte, FrameHeaderSize)
	if err := readAtFull(reader.input, publicBytes, int64(reader.offset)); err != nil {
		return Record{}, ErrIncompleteBundle
	}
	public, err := decodeFrameHeader(publicBytes, reader.limits)
	if err != nil {
		return Record{}, err
	}
	if public.Sequence != reader.nextSequence ||
		public.PreviousDigest != reader.previousDigest {
		return Record{}, fmt.Errorf("%w: frame sequence or chain digest is invalid", ErrCorruptBundle)
	}
	if err := reader.nonces.Add(public.Nonce[:]); err != nil {
		return Record{}, fmt.Errorf("%w: duplicate frame nonce", ErrCorruptBundle)
	}
	remaining := reader.trailerOffset - reader.offset - FrameHeaderSize
	if public.CiphertextLength > remaining {
		return Record{}, ErrIncompleteBundle
	}
	ciphertext := make([]byte, int(public.CiphertextLength))
	if err := readAtFull(
		reader.input,
		ciphertext,
		int64(reader.offset+FrameHeaderSize),
	); err != nil {
		return Record{}, ErrIncompleteBundle
	}
	defer clear(ciphertext)
	key, err := DeriveRecordKey(reader.masterKey, reader.header.BundleID, public.Sequence)
	if err != nil {
		return Record{}, err
	}
	defer clear(key)
	associatedData := frameAssociatedData(
		reader.prologueDigest, reader.headerDigest, publicBytes,
	)
	plaintextEnvelope, err := OpenRecord(
		key, public.Nonce[:], associatedData, ciphertext,
	)
	if err != nil {
		return Record{}, err
	}
	defer clear(plaintextEnvelope)
	if len(plaintextEnvelope) < 4 {
		return Record{}, fmt.Errorf("%w: private header length is absent", ErrCorruptBundle)
	}
	privateLength := uint64(binary.BigEndian.Uint32(plaintextEnvelope[:4]))
	if privateLength == 0 || privateLength > MaxPrivateHeaderBytes ||
		privateLength > uint64(len(plaintextEnvelope)-4) {
		return Record{}, fmt.Errorf("%w: private header length is invalid", ErrCorruptBundle)
	}
	privateBytes := plaintextEnvelope[4 : 4+privateLength]
	if sha256.Sum256(privateBytes) != public.PrivateHeaderDigest {
		return Record{}, fmt.Errorf("%w: private header digest is invalid", ErrCorruptBundle)
	}
	var private PrivateRecordHeader
	if err := decodeCanonicalJSON(privateBytes, MaxPrivateHeaderBytes, &private); err != nil {
		return Record{}, err
	}
	if err := private.Validate(reader.limits); err != nil {
		return Record{}, err
	}
	encoded := plaintextEnvelope[4+privateLength:]
	if uint64(len(encoded)) != private.EncodedLength {
		return Record{}, fmt.Errorf("%w: private payload length is invalid", ErrCorruptBundle)
	}
	if err := reader.validateRecordOrder(private); err != nil {
		return Record{}, err
	}
	nextLogicalBytes, err := checkedLogicalBytes(
		reader.logicalBytes, private, reader.limits,
	)
	if err != nil {
		return Record{}, err
	}
	decoded, err := decodeRecordPayload(private, encoded, reader.limits)
	if err != nil {
		return Record{}, err
	}
	keepDecoded := false
	defer func() {
		if !keepDecoded {
			clear(decoded)
		}
	}()
	frameHasher := sha256.New()
	_, _ = frameHasher.Write(publicBytes)
	_, _ = frameHasher.Write(ciphertext)
	var frameDigest [sha256.Size]byte
	copy(frameDigest[:], frameHasher.Sum(nil))
	prefixBefore := currentDigest(reader.prefix)
	if err := reader.validateSpecialRecord(
		private, decoded, public.Sequence, frameDigest, prefixBefore,
	); err != nil {
		return Record{}, err
	}

	frameBytes := uint64(FrameHeaderSize) + public.CiphertextLength
	_, _ = reader.prefix.Write(publicBytes)
	_, _ = reader.prefix.Write(ciphertext)
	reader.previousDigest = frameDigest
	reader.offset += frameBytes
	reader.nextSequence++
	if recordHasLogicalExtent(private.Type) {
		reader.payloadRecords++
	}
	reader.logicalBytes = nextLogicalBytes
	reader.encodedBytes += frameBytes
	reader.advanceComponent(private)
	if private.Type == RecordCompletion {
		if reader.offset != reader.trailerOffset ||
			reader.trailer.CompletionOffset != reader.offset-frameBytes ||
			reader.trailer.CompletionSequence != public.Sequence ||
			reader.trailer.CompletionFrameDigest != frameDigest ||
			reader.trailer.PrefixDigest != currentDigest(reader.prefix) {
			return Record{}, fmt.Errorf("%w: completion trailer binding is invalid", ErrCorruptBundle)
		}
	}
	keepDecoded = true
	return Record{
		Sequence: public.Sequence, Offset: reader.offset - frameBytes,
		Header: private, Plaintext: decoded,
		FrameDigest: rawDigestToDigest(frameDigest),
	}, nil
}

func (reader *Reader) validateRecordOrder(header PrivateRecordHeader) error {
	if reader.completionSeen {
		return fmt.Errorf("%w: record follows completion", ErrCorruptBundle)
	}
	if len(reader.manifest) != 0 && header.Type != RecordCompletion {
		return fmt.Errorf("%w: final manifest is not followed by completion", ErrCorruptBundle)
	}
	if header.Type == RecordCompletion && len(reader.manifest) == 0 {
		return fmt.Errorf("%w: completion precedes final manifest", ErrCorruptBundle)
	}
	if header.Type == RecordFinalManifest && len(reader.manifest) != 0 {
		return fmt.Errorf("%w: final manifest is duplicated", ErrCorruptBundle)
	}
	if header.Type != RecordFinalManifest && header.Type != RecordCompletion {
		position, exists := reader.components[header.ComponentID]
		if (!exists && header.Ordinal != 0) ||
			(exists && header.Ordinal != position.nextOrdinal) {
			return fmt.Errorf("%w: component ordinal is not sequential", ErrCorruptBundle)
		}
		if recordHasLogicalExtent(header.Type) &&
			header.LogicalOffset != position.nextOffset {
			return fmt.Errorf("%w: component logical extent is not sequential", ErrCorruptBundle)
		}
	}
	return nil
}

func (reader *Reader) advanceComponent(header PrivateRecordHeader) {
	if header.Type == RecordFinalManifest || header.Type == RecordCompletion {
		return
	}
	position := reader.components[header.ComponentID]
	position.nextOrdinal++
	if recordHasLogicalExtent(header.Type) {
		position.nextOffset += header.PlaintextLength
	}
	reader.components[header.ComponentID] = position
}

func (reader *Reader) validateSpecialRecord(
	header PrivateRecordHeader,
	plaintext []byte,
	sequence uint64,
	frameDigest [sha256.Size]byte,
	prefixBefore [sha256.Size]byte,
) error {
	switch header.Type {
	case RecordFinalManifest:
		canonical, err := canonicalizeJSON(plaintext, reader.limits.MaxManifestBytes)
		if err != nil || !bytes.Equal(canonical, plaintext) {
			clear(canonical)
			return fmt.Errorf("%w: final manifest is not canonical", ErrCorruptBundle)
		}
		clear(canonical)
		if err := validateManifestEnvelope(plaintext, reader.header.BundleID); err != nil {
			return err
		}
		reader.manifest = append([]byte(nil), plaintext...)
		reader.manifestSequence = sequence
		reader.manifestFrame = rawDigestToDigest(frameDigest)
	case RecordCompletion:
		var completion Completion
		if err := decodeCanonicalJSON(
			plaintext, reader.limits.MaxMetadataBytes, &completion,
		); err != nil {
			return err
		}
		if completion.Schema != "hideout.migration-completion/v1" ||
			completion.BundleID != reader.header.BundleID ||
			completion.ManifestSequence != reader.manifestSequence ||
			completion.ManifestFrameDigest != reader.manifestFrame ||
			completion.RecordCount != sequence+1 ||
			completion.PrefixDigest != rawDigestToDigest(prefixBefore) ||
			completion.LogicalBytes != reader.logicalBytes ||
			completion.EncodedBytes != reader.encodedBytes {
			return fmt.Errorf("%w: completion record does not bind the prefix", ErrCorruptBundle)
		}
		reader.completion = completion
		reader.completionSeen = true
	}
	return nil
}

func (reader *Reader) finishScan() error {
	if !reader.completionSeen || len(reader.manifest) == 0 ||
		reader.nextSequence != reader.completion.RecordCount ||
		reader.previousDigest != reader.trailer.CompletionFrameDigest ||
		currentDigest(reader.prefix) != reader.trailer.PrefixDigest {
		return fmt.Errorf("%w: sealed record sequence is incomplete", ErrCorruptBundle)
	}
	reader.scanComplete = true
	reader.summary = BundleSummary{
		BundleID: reader.header.BundleID, Sealed: true,
		RecordCount:    reader.completion.RecordCount,
		LogicalBytes:   reader.completion.LogicalBytes,
		EncodedBytes:   uint64(reader.size),
		PrefixDigest:   rawDigestToDigest(reader.trailer.PrefixDigest),
		ManifestDigest: digestBytes(reader.manifest),
	}
	return nil
}

func (reader *Reader) Summary() (BundleSummary, error) {
	if reader == nil {
		return BundleSummary{}, fmt.Errorf("%w: bundle reader is nil", ErrInvalidBundle)
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if !reader.scanComplete {
		return BundleSummary{}, ErrIncompleteBundle
	}
	return reader.summary, nil
}

func (reader *Reader) Manifest() []byte {
	if reader == nil {
		return nil
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return append([]byte(nil), reader.manifest...)
}

func (reader *Reader) Close() error {
	if reader == nil {
		return nil
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return nil
	}
	clear(reader.masterKey)
	reader.closed = true
	return nil
}

func readAtFull(reader io.ReaderAt, output []byte, offset int64) error {
	if offset < 0 {
		return ErrIncompleteBundle
	}
	section := io.NewSectionReader(reader, offset, int64(len(output)))
	_, err := io.ReadFull(section, output)
	return err
}
