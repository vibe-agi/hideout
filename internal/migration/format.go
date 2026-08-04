package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	PrologueSize          = 32
	FrameHeaderSize       = 128
	TrailerSize           = 96
	MaxPrivateHeaderBytes = 16 << 10

	frameCiphertextLengthOffset        = 24
	formatSuiteIDV1             uint16 = 1
)

type prologue struct {
	Major        uint16
	Minor        uint16
	HeaderLength uint32
	SuiteID      uint16
}

func encodePrologue(value prologue) ([PrologueSize]byte, error) {
	var output [PrologueSize]byte
	if value.Major != BundleFormatVersion || value.Minor != 0 ||
		value.HeaderLength == 0 || value.HeaderLength > HardMaxHeaderBytes ||
		value.SuiteID != formatSuiteIDV1 {
		return output, fmt.Errorf("%w: prologue fields are invalid", ErrInvalidBundle)
	}
	copy(output[0:8], BundleMagic)
	binary.BigEndian.PutUint16(output[8:10], value.Major)
	binary.BigEndian.PutUint16(output[10:12], value.Minor)
	binary.BigEndian.PutUint32(output[12:16], value.HeaderLength)
	binary.BigEndian.PutUint16(output[16:18], value.SuiteID)
	return output, nil
}

func decodePrologue(input []byte) (prologue, error) {
	if len(input) != PrologueSize || string(input[0:8]) != BundleMagic {
		return prologue{}, fmt.Errorf("%w: prologue magic or length is invalid", ErrUnsupportedVersion)
	}
	for _, value := range input[18:] {
		if value != 0 {
			return prologue{}, fmt.Errorf("%w: prologue reserved bytes are nonzero", ErrUnsupportedVersion)
		}
	}
	value := prologue{
		Major:        binary.BigEndian.Uint16(input[8:10]),
		Minor:        binary.BigEndian.Uint16(input[10:12]),
		HeaderLength: binary.BigEndian.Uint32(input[12:16]),
		SuiteID:      binary.BigEndian.Uint16(input[16:18]),
	}
	if value.Major != BundleFormatVersion || value.Minor != 0 ||
		value.SuiteID != formatSuiteIDV1 {
		return prologue{}, fmt.Errorf("%w: prologue version or suite is unsupported", ErrUnsupportedVersion)
	}
	if value.HeaderLength == 0 || value.HeaderLength > HardMaxHeaderBytes {
		return prologue{}, fmt.Errorf("%w: public header length exceeds v1", ErrLimitExceeded)
	}
	return value, nil
}

type publicHeaderAssociatedData struct {
	BundleID  BundleID      `json:"bundleId"`
	CreatedAt string        `json:"createdAt"`
	Suite     string        `json:"suite"`
	KDF       KDFParameters `json:"kdf"`
	Salt      []byte        `json:"salt"`
	Limits    Limits        `json:"limits"`
}

func publicHeaderAAD(prologueBytes []byte, header PublicHeader) ([]byte, error) {
	fields, err := canonicalMarshal(publicHeaderAssociatedData{
		BundleID: header.BundleID, CreatedAt: header.CreatedAt,
		Suite: header.Suite, KDF: header.KDF, Salt: header.Salt,
		Limits: header.Limits,
	})
	if err != nil {
		return nil, err
	}
	output := make([]byte, 0, len(prologueBytes)+len(fields))
	output = append(output, prologueBytes...)
	output = append(output, fields...)
	return output, nil
}

func validatePublicHeader(header PublicHeader) error {
	if _, err := ParseBundleID(string(header.BundleID)); err != nil {
		return err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, header.CreatedAt)
	if err != nil || createdAt.Location() != time.UTC ||
		!strings.HasSuffix(header.CreatedAt, "Z") || len(header.CreatedAt) > 64 ||
		header.Suite != SuiteV1 {
		return fmt.Errorf("%w: public header identity or suite is invalid", ErrUnsupportedVersion)
	}
	if err := header.KDF.Validate(); err != nil {
		return err
	}
	if len(header.Salt) != KDFSaltBytes || len(header.WrapNonce) != XNonceBytes ||
		len(header.WrappedMasterKey) != MasterKeyBytes+AEADTagBytes {
		return fmt.Errorf("%w: public header cryptographic fields are invalid", ErrInvalidBundle)
	}
	return header.Limits.Validate()
}

func decodePublicHeader(input []byte) (PublicHeader, error) {
	var header PublicHeader
	if err := decodeCanonicalJSON(input, HardMaxHeaderBytes, &header); err != nil {
		return PublicHeader{}, err
	}
	if err := validatePublicHeader(header); err != nil {
		return PublicHeader{}, err
	}
	return header, nil
}

type frameHeader struct {
	Sequence            uint64
	CiphertextLength    uint64
	Nonce               [XNonceBytes]byte
	PreviousDigest      [sha256.Size]byte
	PrivateHeaderDigest [sha256.Size]byte
}

func encodeFrameHeader(value frameHeader) ([FrameHeaderSize]byte, error) {
	var output [FrameHeaderSize]byte
	if value.CiphertextLength < AEADTagBytes+4 ||
		value.CiphertextLength > maxFrameCiphertextBytes(DefaultLimits()) {
		return output, fmt.Errorf("%w: frame ciphertext length is invalid", ErrLimitExceeded)
	}
	copy(output[0:8], RecordMagic)
	output[8] = RecordFrameVersion
	binary.BigEndian.PutUint64(output[16:24], value.Sequence)
	binary.BigEndian.PutUint64(output[24:32], value.CiphertextLength)
	copy(output[32:56], value.Nonce[:])
	copy(output[56:88], value.PreviousDigest[:])
	copy(output[88:120], value.PrivateHeaderDigest[:])
	return output, nil
}

func decodeFrameHeader(input []byte, limits Limits) (frameHeader, error) {
	if len(input) != FrameHeaderSize || string(input[0:8]) != RecordMagic ||
		input[8] != RecordFrameVersion {
		return frameHeader{}, fmt.Errorf("%w: frame header magic or version is invalid", ErrCorruptBundle)
	}
	for _, index := range []struct{ start, end int }{{9, 16}, {120, 128}} {
		for _, value := range input[index.start:index.end] {
			if value != 0 {
				return frameHeader{}, fmt.Errorf("%w: frame reserved bytes are nonzero", ErrCorruptBundle)
			}
		}
	}
	value := frameHeader{
		Sequence:         binary.BigEndian.Uint64(input[16:24]),
		CiphertextLength: binary.BigEndian.Uint64(input[24:32]),
	}
	copy(value.Nonce[:], input[32:56])
	copy(value.PreviousDigest[:], input[56:88])
	copy(value.PrivateHeaderDigest[:], input[88:120])
	if value.CiphertextLength < AEADTagBytes+4 ||
		value.CiphertextLength > maxFrameCiphertextBytes(limits) {
		return frameHeader{}, fmt.Errorf("%w: frame ciphertext length exceeds v1", ErrLimitExceeded)
	}
	return value, nil
}

func frameAssociatedData(
	prologueDigest [sha256.Size]byte,
	headerDigest [sha256.Size]byte,
	frame []byte,
) []byte {
	output := make([]byte, 0, sha256.Size*2+len(frame))
	output = append(output, prologueDigest[:]...)
	output = append(output, headerDigest[:]...)
	output = append(output, frame...)
	return output
}

type trailer struct {
	CompletionOffset      uint64
	CompletionSequence    uint64
	CompletionFrameDigest [sha256.Size]byte
	PrefixDigest          [sha256.Size]byte
}

func encodeTrailer(value trailer) [TrailerSize]byte {
	var output [TrailerSize]byte
	copy(output[0:8], BundleEndMagic)
	binary.BigEndian.PutUint64(output[8:16], value.CompletionOffset)
	binary.BigEndian.PutUint64(output[16:24], value.CompletionSequence)
	copy(output[24:56], value.CompletionFrameDigest[:])
	copy(output[56:88], value.PrefixDigest[:])
	return output
}

func decodeTrailer(input []byte) (trailer, error) {
	if len(input) != TrailerSize || string(input[0:8]) != BundleEndMagic {
		return trailer{}, ErrIncompleteBundle
	}
	for _, value := range input[88:] {
		if value != 0 {
			return trailer{}, fmt.Errorf("%w: trailer reserved bytes are nonzero", ErrCorruptBundle)
		}
	}
	value := trailer{
		CompletionOffset:   binary.BigEndian.Uint64(input[8:16]),
		CompletionSequence: binary.BigEndian.Uint64(input[16:24]),
	}
	copy(value.CompletionFrameDigest[:], input[24:56])
	copy(value.PrefixDigest[:], input[56:88])
	return value, nil
}

func maxFrameCiphertextBytes(limits Limits) uint64 {
	maximum := uint64(limits.MaxManifestBytes)
	if candidate := uint64(limits.MaxChunkBytes) + uint64(HardMaxRecordOverhead); candidate > maximum {
		maximum = candidate
	}
	if candidate := uint64(limits.MaxMetadataBytes); candidate > maximum {
		maximum = candidate
	}
	return 4 + MaxPrivateHeaderBytes + maximum + AEADTagBytes
}

func canonicalMarshal(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical JSON", ErrInvalidBundle)
	}
	canonical, err := canonicalizeJSON(encoded, HardMaxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical JSON", ErrInvalidBundle)
	}
	return canonical, nil
}

func canonicalizeJSON(input []byte, maximum uint32) ([]byte, error) {
	if len(input) == 0 || uint64(len(input)) > uint64(maximum) {
		return nil, fmt.Errorf("%w: JSON document length is invalid", ErrLimitExceeded)
	}
	duplicateDecoder := json.NewDecoder(bytes.NewReader(input))
	duplicateDecoder.UseNumber()
	if err := consumeJSONValue(duplicateDecoder); err != nil {
		return nil, fmt.Errorf("%w: JSON document is invalid", ErrCorruptBundle)
	}
	if token, err := duplicateDecoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return nil, fmt.Errorf("%w: JSON document has trailing data", ErrCorruptBundle)
	}

	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: JSON document is invalid", ErrCorruptBundle)
	}
	if err := normalizeJSONNumbers(&value); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize JSON", ErrCorruptBundle)
	}
	if uint64(len(encoded)) > uint64(maximum) {
		return nil, fmt.Errorf("%w: canonical JSON exceeds its bound", ErrLimitExceeded)
	}
	return encoded, nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func normalizeJSONNumbers(value *any) error {
	switch typed := (*value).(type) {
	case json.Number:
		text := typed.String()
		if strings.ContainsAny(text, ".eE") {
			return fmt.Errorf("%w: migration JSON numbers must be integers", ErrCorruptBundle)
		}
		integer := new(big.Int)
		if _, ok := integer.SetString(text, 10); !ok {
			return fmt.Errorf("%w: JSON integer is invalid", ErrCorruptBundle)
		}
		*value = json.Number(integer.String())
	case []any:
		for index := range typed {
			if err := normalizeJSONNumbers(&typed[index]); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range typed {
			if err := normalizeJSONNumbers(&item); err != nil {
				return err
			}
			typed[key] = item
		}
	}
	return nil
}

func decodeCanonicalJSON(input []byte, maximum uint32, target any) error {
	canonical, err := canonicalizeJSON(input, maximum)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, input) {
		return fmt.Errorf("%w: JSON document is not canonical", ErrCorruptBundle)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: strict JSON decode failed", ErrCorruptBundle)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: strict JSON has trailing data", ErrCorruptBundle)
	}
	return nil
}

func encodeRecordPayload(recordType RecordType, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 || len(plaintext) > int(HardMaxManifestBytes) {
		return nil, fmt.Errorf("%w: record plaintext length is invalid", ErrLimitExceeded)
	}
	if recordType != RecordDataChunk {
		return append([]byte(nil), plaintext...), nil
	}
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(int(HardMaxChunkBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize zstd encoder", ErrInvalidBundle)
	}
	defer encoder.Close()
	encoded := encoder.EncodeAll(plaintext, nil)
	if len(encoded) > len(plaintext)+int(HardMaxRecordOverhead) {
		clear(encoded)
		return nil, fmt.Errorf("%w: zstd output exceeds bounded overhead", ErrLimitExceeded)
	}
	return encoded, nil
}

func decodeRecordPayload(
	header PrivateRecordHeader,
	encoded []byte,
	limits Limits,
) ([]byte, error) {
	if err := header.Validate(limits); err != nil {
		return nil, err
	}
	if uint64(len(encoded)) != header.EncodedLength {
		return nil, fmt.Errorf("%w: encoded record length does not match header", ErrCorruptBundle)
	}
	if header.Type == RecordZeroExtent || header.Type == RecordHoleExtent {
		if len(encoded) != 0 || digestZeros(header.PlaintextLength) != header.PlaintextDigest {
			return nil, fmt.Errorf("%w: sparse record digest is invalid", ErrCorruptBundle)
		}
		return nil, nil
	}

	var plaintext []byte
	if header.Type == RecordDataChunk {
		decoder, err := zstd.NewReader(
			nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(uint64(limits.MaxChunkBytes)),
			zstd.WithDecoderMaxWindow(uint64(limits.MaxChunkBytes)),
		)
		if err != nil {
			return nil, fmt.Errorf("%w: initialize zstd decoder", ErrCorruptBundle)
		}
		defer decoder.Close()
		plaintext, err = decoder.DecodeAll(encoded, make([]byte, 0, int(header.PlaintextLength)))
		if err != nil {
			return nil, fmt.Errorf("%w: zstd record is invalid", ErrCorruptBundle)
		}
	} else {
		plaintext = append([]byte(nil), encoded...)
	}
	if uint64(len(plaintext)) != header.PlaintextLength ||
		digestBytes(plaintext) != header.PlaintextDigest {
		clear(plaintext)
		return nil, fmt.Errorf("%w: decoded record length or digest is invalid", ErrCorruptBundle)
	}
	return plaintext, nil
}

func digestBytes(input []byte) Digest {
	digest := sha256.Sum256(input)
	return rawDigestToDigest(digest)
}

func digestZeros(length uint64) Digest {
	hasher := sha256.New()
	var zeros [32 << 10]byte
	for length > 0 {
		chunk := uint64(len(zeros))
		if length < chunk {
			chunk = length
		}
		_, _ = hasher.Write(zeros[:chunk])
		length -= chunk
	}
	return bytesToDigest(hasher.Sum(nil))
}

func rawDigestToDigest(input [sha256.Size]byte) Digest {
	return Digest("sha256:" + hex.EncodeToString(input[:]))
}

func bytesToDigest(input []byte) Digest {
	return Digest("sha256:" + hex.EncodeToString(input))
}

func digestToRaw(input Digest) ([sha256.Size]byte, error) {
	var output [sha256.Size]byte
	if err := input.Validate(); err != nil {
		return output, err
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(string(input), "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return output, fmt.Errorf("%w: digest bytes are invalid", ErrInvalidBundle)
	}
	copy(output[:], decoded)
	return output, nil
}

func currentDigest(hasher hash.Hash) [sha256.Size]byte {
	var output [sha256.Size]byte
	copy(output[:], hasher.Sum(nil))
	return output
}

// Extent is a canonical logical-disk range.
type Extent struct {
	Kind          ExtentKind
	LogicalOffset uint64
	Length        uint64
	Data          []byte
}

type LogicalDigester struct {
	logicalSize uint64
	nextOffset  uint64
	lastKind    ExtentKind
	hasher      hash.Hash
	finished    bool
}

func NewLogicalDigester(logicalSize uint64) (*LogicalDigester, error) {
	if logicalSize == 0 || logicalSize > HardMaxLogicalBytes {
		return nil, fmt.Errorf("%w: logical disk size is invalid", ErrLimitExceeded)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("HIDMIG-DISK-V1\x00"))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], logicalSize)
	_, _ = hasher.Write(size[:])
	return &LogicalDigester{logicalSize: logicalSize, hasher: hasher}, nil
}

func (digester *LogicalDigester) WriteExtent(extent Extent) error {
	if digester == nil || digester.finished || digester.hasher == nil {
		return fmt.Errorf("%w: logical digester is not writable", ErrInvalidBundle)
	}
	if extent.Kind != ExtentData && extent.Kind != ExtentZero && extent.Kind != ExtentHole {
		return fmt.Errorf("%w: logical extent kind is invalid", ErrCorruptBundle)
	}
	if extent.Length == 0 || extent.LogicalOffset != digester.nextOffset ||
		extent.LogicalOffset > digester.logicalSize ||
		extent.Length > digester.logicalSize-extent.LogicalOffset {
		return fmt.Errorf("%w: logical extents have a gap, overlap, or overflow", ErrCorruptBundle)
	}
	if extent.Kind == ExtentData {
		if uint64(len(extent.Data)) != extent.Length {
			return fmt.Errorf("%w: data extent length does not match bytes", ErrCorruptBundle)
		}
	} else {
		if len(extent.Data) != 0 || digester.lastKind == extent.Kind {
			return fmt.Errorf("%w: sparse extent is noncanonical", ErrCorruptBundle)
		}
	}
	var descriptor [17]byte
	switch extent.Kind {
	case ExtentData:
		descriptor[0] = 1
	case ExtentZero:
		descriptor[0] = 2
	case ExtentHole:
		descriptor[0] = 3
	}
	binary.BigEndian.PutUint64(descriptor[1:9], extent.LogicalOffset)
	binary.BigEndian.PutUint64(descriptor[9:17], extent.Length)
	_, _ = digester.hasher.Write(descriptor[:])
	if extent.Kind == ExtentData {
		_, _ = digester.hasher.Write(extent.Data)
	}
	digester.nextOffset += extent.Length
	digester.lastKind = extent.Kind
	return nil
}

func (digester *LogicalDigester) Finish() (Digest, error) {
	if digester == nil || digester.finished || digester.hasher == nil ||
		digester.nextOffset != digester.logicalSize {
		return "", fmt.Errorf("%w: logical disk extents are incomplete", ErrCorruptBundle)
	}
	digester.finished = true
	return bytesToDigest(digester.hasher.Sum(nil)), nil
}
