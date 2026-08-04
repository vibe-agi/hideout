package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"testing"
)

func TestBundleWriterReaderRoundTripAndSeal(t *testing.T) {
	var output bytes.Buffer
	writer, err := NewWriter(&output, WriterOptions{
		BundleID:   "migb_roundtrip1234",
		CreatedAt:  "2026-08-02T00:00:00Z",
		KDF:        unitKDFParameters(),
		Limits:     DefaultLimits(),
		Random:     bytes.NewReader(deterministicRandomFixture(1024)),
		Passphrase: []byte("round-trip passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"schema":"fixture.metadata/v1","value":"safe"}`)
	data := bytes.Repeat([]byte("compressible migration data "), 4096)
	raw := deterministicRandomFixture(4096)
	for _, input := range []RecordInput{
		{
			Type: RecordMetadata, ComponentID: "component_profile1234",
			Plaintext: metadata,
		},
		{
			Type: RecordDataChunk, ComponentID: "component_disk1234",
			Plaintext: data,
		},
		{
			Type: RecordRawChunk, ComponentID: "component_disk1234",
			Ordinal: 1, LogicalOffset: uint64(len(data)), Plaintext: raw,
		},
		{
			Type: RecordHoleExtent, ComponentID: "component_disk1234",
			Ordinal: 2, LogicalOffset: uint64(len(data) + len(raw)),
			ExtentLength: 4096,
		},
	} {
		if _, err := writer.Append(input); err != nil {
			t.Fatalf("append %v: %v", input.Type, err)
		}
	}
	manifest := []byte(`{
		"schema":"hideout.migration-manifest/v1",
		"bundleId":"migb_roundtrip1234",
		"formatVersion":1
	}`)
	writtenSummary, err := writer.Seal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !writtenSummary.Sealed || writtenSummary.RecordCount != 6 {
		t.Fatalf("writer summary=%+v", writtenSummary)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !allZero(writer.masterKey) {
		t.Fatal("writer retained the master key after close")
	}

	reader, err := NewReader(
		bytes.NewReader(output.Bytes()),
		int64(output.Len()),
		[]byte("round-trip passphrase"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var records []Record
	for {
		record, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		records = append(records, record)
	}
	if len(records) != 6 ||
		!bytes.Equal(records[0].Plaintext, metadata) ||
		!bytes.Equal(records[1].Plaintext, data) ||
		!bytes.Equal(records[2].Plaintext, raw) ||
		records[3].Header.Type != RecordHoleExtent ||
		len(records[3].Plaintext) != 0 ||
		records[4].Header.Type != RecordFinalManifest ||
		records[5].Header.Type != RecordCompletion {
		t.Fatalf("round-trip records=%+v", records)
	}
	readSummary, err := reader.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if !readSummary.Sealed ||
		readSummary.BundleID != writtenSummary.BundleID ||
		readSummary.RecordCount != writtenSummary.RecordCount ||
		readSummary.PrefixDigest != writtenSummary.PrefixDigest {
		t.Fatalf("summary mismatch: writer=%+v reader=%+v", writtenSummary, readSummary)
	}
	if !bytes.Equal(reader.Manifest(), canonicalJSONForTest(t, manifest)) {
		t.Fatalf("manifest=%s", reader.Manifest())
	}
}

func TestBundleReaderRejectsHostileLengthsTamperTruncationAndTrailingData(t *testing.T) {
	sealed := sealedBundleFixture(t)
	prologue, err := decodePrologue(sealed[:PrologueSize])
	if err != nil {
		t.Fatal(err)
	}
	firstFrameOffset := PrologueSize + int(prologue.HeaderLength)

	oversized := append([]byte(nil), sealed...)
	binary.BigEndian.PutUint64(
		oversized[firstFrameOffset+frameCiphertextLengthOffset:],
		math.MaxUint64,
	)
	reader, err := NewReader(
		bytes.NewReader(oversized), int64(len(oversized)),
		[]byte("fixture passphrase"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("hostile frame length error=%v", err)
	}
	_ = reader.Close()

	tampered := append([]byte(nil), sealed...)
	tampered[firstFrameOffset+FrameHeaderSize] ^= 0x80
	reader, err = NewReader(
		bytes.NewReader(tampered), int64(len(tampered)),
		[]byte("fixture passphrase"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered frame error=%v", err)
	}
	_ = reader.Close()

	if _, err := NewReader(
		bytes.NewReader(sealed[:len(sealed)-1]),
		int64(len(sealed)-1),
		[]byte("fixture passphrase"),
	); !errors.Is(err, ErrIncompleteBundle) {
		t.Fatalf("truncated bundle error=%v", err)
	}

	trailing := append(append([]byte(nil), sealed...), 0x00)
	if _, err := NewReader(
		bytes.NewReader(trailing), int64(len(trailing)),
		[]byte("fixture passphrase"),
	); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("trailing bundle error=%v", err)
	}

	if _, err := NewReader(
		bytes.NewReader(sealed), int64(len(sealed)),
		[]byte("wrong passphrase"),
	); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("wrong passphrase error=%v", err)
	}

	overflowedTrailer := append([]byte(nil), sealed...)
	binary.BigEndian.PutUint64(
		overflowedTrailer[len(overflowedTrailer)-TrailerSize+8:], math.MaxUint64,
	)
	if _, err := NewReader(
		bytes.NewReader(overflowedTrailer), int64(len(overflowedTrailer)),
		[]byte("fixture passphrase"),
	); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("overflowed trailer offset error=%v", err)
	}
}

func TestBundleReaderFailureIsSticky(t *testing.T) {
	sealed := sealedBundleFixture(t)
	prologueValue, err := decodePrologue(sealed[:PrologueSize])
	if err != nil {
		t.Fatal(err)
	}
	frameOffset := PrologueSize + int(prologueValue.HeaderLength)
	sealed[frameOffset+FrameHeaderSize] ^= 0x80
	reader, err := NewReader(
		bytes.NewReader(sealed), int64(len(sealed)), []byte("fixture passphrase"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	_, firstErr := reader.Next()
	_, secondErr := reader.Next()
	if !errors.Is(firstErr, ErrAuthenticationFailed) || secondErr != firstErr {
		t.Fatalf("reader errors changed after failure: first=%v second=%v", firstErr, secondErr)
	}
}

func TestSealFailurePoisonsAppendOnlyWriter(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxMetadataBytes = 1
	var output bytes.Buffer
	writer, err := NewWriter(&output, WriterOptions{
		BundleID: "migb_poisoned1234", CreatedAt: "2026-08-02T00:00:00Z",
		KDF: unitKDFParameters(), Limits: limits,
		Random:     bytes.NewReader(deterministicRandomFixture(512)),
		Passphrase: []byte("fixture passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	manifest := []byte(
		`{"bundleId":"migb_poisoned1234","formatVersion":1,"schema":"hideout.migration-manifest/v1"}`,
	)
	if _, err := writer.Seal(manifest); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("first seal error=%v", err)
	}
	written := output.Len()
	if _, err := writer.Seal(manifest); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("second seal error=%v", err)
	}
	if output.Len() != written {
		t.Fatalf("poisoned writer appended %d bytes", output.Len()-written)
	}
}

func TestRecordCompressionRequiresExactAuthenticatedPlaintextLength(t *testing.T) {
	plaintext := bytes.Repeat([]byte("zstd exact output"), 4096)
	encoded, err := encodeRecordPayload(RecordDataChunk, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	header := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordDataChunk,
		ComponentID:     "component_disk1234",
		PlaintextLength: uint64(len(plaintext)), EncodedLength: uint64(len(encoded)),
		PlaintextDigest: digestBytes(plaintext),
	}
	decoded, err := decodeRecordPayload(header, encoded, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatal("zstd payload changed")
	}
	clear(decoded)
	header.PlaintextLength--
	if _, err := decodeRecordPayload(
		header, encoded, DefaultLimits(),
	); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("inexact decompression error=%v", err)
	}
}

func TestRecordCompressionRejectsExpansionBeyondAuthenticatedBound(t *testing.T) {
	expanded := bytes.Repeat([]byte{0}, 1<<20)
	encoded, err := encodeRecordPayload(RecordDataChunk, expanded)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(encoded)
	limits := DefaultLimits()
	limits.MaxChunkBytes = 64 << 10
	header := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordDataChunk,
		ComponentID:     "component_expansion1",
		PlaintextLength: uint64(limits.MaxChunkBytes),
		EncodedLength:   uint64(len(encoded)),
		PlaintextDigest: digestBytes(make([]byte, limits.MaxChunkBytes)),
	}
	if _, err := decodeRecordPayload(header, encoded, limits); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("oversized zstd expansion error=%v", err)
	}
}

func TestLogicalDiskDigestCanonicalExtents(t *testing.T) {
	digester, err := NewLogicalDigester(8192)
	if err != nil {
		t.Fatal(err)
	}
	for _, extent := range []Extent{
		{Kind: ExtentData, LogicalOffset: 0, Length: 4, Data: []byte("data")},
		{Kind: ExtentHole, LogicalOffset: 4, Length: 4092},
		{Kind: ExtentZero, LogicalOffset: 4096, Length: 4096},
	} {
		if err := digester.WriteExtent(extent); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := digester.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(digest); got != "sha256:34275d31ccf3408d0eb9c3e1a8faf55991d6158234cae0bb98dd70694c26cabf" {
		t.Fatalf("logical digest=%s", got)
	}

	gap, err := NewLogicalDigester(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := gap.WriteExtent(Extent{
		Kind: ExtentData, LogicalOffset: 1, Length: 1, Data: []byte{1},
	}); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("logical gap error=%v", err)
	}
	adjacent, err := NewLogicalDigester(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := adjacent.WriteExtent(Extent{
		Kind: ExtentHole, LogicalOffset: 0, Length: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adjacent.WriteExtent(Extent{
		Kind: ExtentHole, LogicalOffset: 4, Length: 4,
	}); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("noncanonical adjacent hole error=%v", err)
	}
}

func sealedBundleFixture(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer, err := NewWriter(&output, WriterOptions{
		BundleID: "migb_fixture1234", CreatedAt: "2026-08-02T00:00:00Z",
		KDF: unitKDFParameters(), Limits: DefaultLimits(),
		Random:     bytes.NewReader(deterministicRandomFixture(512)),
		Passphrase: []byte("fixture passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(RecordInput{
		Type: RecordRawChunk, ComponentID: "component_data1234",
		Plaintext: []byte("fixture payload"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Seal([]byte(
		`{"bundleId":"migb_fixture1234","formatVersion":1,"schema":"hideout.migration-manifest/v1"}`,
	)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func deterministicRandomFixture(size int) []byte {
	value := make([]byte, size)
	for index := range value {
		value[index] = byte(index*73 + 19)
	}
	return value
}

func unitKDFParameters() KDFParameters {
	return KDFParameters{MemoryKiB: 8 << 10, Passes: 1, Lanes: 1}
}

func canonicalJSONForTest(t *testing.T, value []byte) []byte {
	t.Helper()
	canonical, err := canonicalizeJSON(value, HardMaxManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestFrameDigestKnownShape(t *testing.T) {
	digest := digestBytes([]byte("frame"))
	decoded, err := hex.DecodeString(string(digest)[len("sha256:"):])
	if err != nil || len(decoded) != 32 {
		t.Fatalf("digest shape=%q err=%v", digest, err)
	}
}

func TestCanonicalJSONRejectsDuplicateUnknownAndNoncanonicalFields(t *testing.T) {
	var target struct {
		A int `json:"a"`
	}
	for name, input := range map[string]string{
		"duplicate":    `{"a":1,"a":2}`,
		"unknown":      `{"a":1,"extra":2}`,
		"noncanonical": "{\n  \"a\": 1\n}",
		"fraction":     `{"a":1.5}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := decodeCanonicalJSON(
				[]byte(input), HardMaxMetadataBytes, &target,
			); !errors.Is(err, ErrCorruptBundle) {
				t.Fatalf("strict JSON error=%v", err)
			}
		})
	}
	if err := decodeCanonicalJSON(
		[]byte(`{"a":1}`), HardMaxMetadataBytes, &target,
	); err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
}

func TestCanonicalJSONRejectsAboveHardManifestLimitBeforeParsing(t *testing.T) {
	oversized := bytes.Repeat([]byte{' '}, int(HardMaxManifestBytes)+1)
	if _, err := canonicalizeJSON(
		oversized, HardMaxManifestBytes,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized manifest error=%v", err)
	}
}

func TestPhysicalHeadersRejectReservedBytesAndInvalidTime(t *testing.T) {
	prologueBytes, err := encodePrologue(prologue{
		Major: BundleFormatVersion, HeaderLength: 128, SuiteID: formatSuiteIDV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	prologueBytes[PrologueSize-1] = 1
	if _, err := decodePrologue(prologueBytes[:]); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("reserved prologue error=%v", err)
	}

	frameBytes, err := encodeFrameHeader(frameHeader{
		CiphertextLength: AEADTagBytes + 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	frameBytes[FrameHeaderSize-1] = 1
	if _, err := decodeFrameHeader(
		frameBytes[:], DefaultLimits(),
	); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("reserved frame error=%v", err)
	}

	trailerBytes := encodeTrailer(trailer{})
	trailerBytes[TrailerSize-1] = 1
	if _, err := decodeTrailer(trailerBytes[:]); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("reserved trailer error=%v", err)
	}

	header := PublicHeader{
		BundleID: "migb_fixture1234", CreatedAt: "2026-08-02Z",
		Suite: SuiteV1, KDF: unitKDFParameters(),
		Salt:      bytes.Repeat([]byte{1}, KDFSaltBytes),
		WrapNonce: bytes.Repeat([]byte{2}, XNonceBytes),
		WrappedMasterKey: bytes.Repeat(
			[]byte{3}, MasterKeyBytes+AEADTagBytes,
		),
		Limits: DefaultLimits(),
	}
	if err := validatePublicHeader(header); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("invalid creation time error=%v", err)
	}
}

func TestRecordOrderingAndAggregateLogicalLimitFailClosed(t *testing.T) {
	reader := &Reader{
		components: make(map[OpaqueID]componentPosition),
	}
	first := PrivateRecordHeader{
		Type: RecordRawChunk, ComponentID: "component_disk1234",
		Ordinal: 0, LogicalOffset: 0, PlaintextLength: 4,
	}
	if err := reader.validateRecordOrder(first); err != nil {
		t.Fatal(err)
	}
	reader.advanceComponent(first)
	for name, header := range map[string]PrivateRecordHeader{
		"ordinal": {
			Type: RecordRawChunk, ComponentID: "component_disk1234",
			Ordinal: 2, LogicalOffset: 4, PlaintextLength: 1,
		},
		"overlap": {
			Type: RecordRawChunk, ComponentID: "component_disk1234",
			Ordinal: 1, LogicalOffset: 3, PlaintextLength: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := reader.validateRecordOrder(header); !errors.Is(err, ErrCorruptBundle) {
				t.Fatalf("ordering error=%v", err)
			}
		})
	}

	reader.manifest = []byte(`{}`)
	if err := reader.validateRecordOrder(PrivateRecordHeader{
		Type: RecordMetadata,
	}); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("post-manifest record error=%v", err)
	}
	reader = &Reader{components: make(map[OpaqueID]componentPosition)}
	if err := reader.validateRecordOrder(PrivateRecordHeader{
		Type: RecordCompletion,
	}); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("early completion error=%v", err)
	}

	limits := DefaultLimits()
	limits.MaxLogicalBytes = 4
	if got, err := checkedLogicalBytes(0, first, limits); err != nil || got != 4 {
		t.Fatalf("logical total=%d error=%v", got, err)
	}
	second := first
	second.ComponentID = "component_disk5678"
	second.PlaintextLength = 1
	if _, err := checkedLogicalBytes(4, second, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("aggregate logical bound error=%v", err)
	}
	metadata := first
	metadata.Type = RecordMetadata
	metadata.PlaintextLength = uint64(limits.MaxMetadataBytes)
	if got, err := checkedLogicalBytes(4, metadata, limits); err != nil || got != 4 {
		t.Fatalf("metadata changed logical total=%d error=%v", got, err)
	}
}

func TestLogicalDigesterDomainSeparatesSparseRepresentations(t *testing.T) {
	zero, err := NewLogicalDigester(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := zero.WriteExtent(Extent{
		Kind: ExtentZero, LogicalOffset: 0, Length: 4,
	}); err != nil {
		t.Fatal(err)
	}
	zeroDigest, err := zero.Finish()
	if err != nil {
		t.Fatal(err)
	}
	hole, err := NewLogicalDigester(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := hole.WriteExtent(Extent{
		Kind: ExtentHole, LogicalOffset: 0, Length: 4,
	}); err != nil {
		t.Fatal(err)
	}
	holeDigest, err := hole.Finish()
	if err != nil {
		t.Fatal(err)
	}
	rawZero := sha256.Sum256(make([]byte, 4))
	if zeroDigest == holeDigest ||
		zeroDigest == rawDigestToDigest(rawZero) ||
		holeDigest == rawDigestToDigest(rawZero) {
		t.Fatalf("logical digest lost extent domain: zero=%s hole=%s", zeroDigest, holeDigest)
	}
}
