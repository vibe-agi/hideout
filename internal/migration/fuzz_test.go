package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

func FuzzMigrationPublicHeader(f *testing.F) {
	header := PublicHeader{
		BundleID: "migb_fuzzseed1234", CreatedAt: "2026-08-02T00:00:00Z",
		Suite: SuiteV1, KDF: unitKDFParameters(),
		Salt:             bytes.Repeat([]byte{0x11}, KDFSaltBytes),
		WrapNonce:        bytes.Repeat([]byte{0x22}, XNonceBytes),
		WrappedMasterKey: bytes.Repeat([]byte{0x33}, MasterKeyBytes+AEADTagBytes),
		Limits:           DefaultLimits(),
	}
	seed, err := canonicalMarshal(header)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(seed))
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > int(HardMaxHeaderBytes)+1 {
			t.Skip()
		}
		_, _ = decodePublicHeader([]byte(input))
	})
}

func FuzzMigrationPrivateHeader(f *testing.F) {
	header := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordRawChunk,
		ComponentID: "component_fuzz1234", PlaintextLength: 4,
		EncodedLength: 4, PlaintextDigest: digestBytes([]byte("seed")),
	}
	seed, err := canonicalMarshal(header)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(seed))
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > MaxPrivateHeaderBytes+1 {
			t.Skip()
		}
		var candidate PrivateRecordHeader
		if err := decodeCanonicalJSON(
			[]byte(input), MaxPrivateHeaderBytes, &candidate,
		); err == nil {
			_ = candidate.Validate(DefaultLimits())
		}
	})
}

func FuzzMigrationFrame(f *testing.F) {
	seed, err := encodeFrameHeader(frameHeader{
		CiphertextLength: AEADTagBytes + 4,
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(seed[:]))
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > FrameHeaderSize*2 {
			t.Skip()
		}
		_, _ = decodeFrameHeader([]byte(input), DefaultLimits())
	})
}

func FuzzMigrationManifest(f *testing.F) {
	f.Add(`{"bundleId":"migb_fuzzseed1234","formatVersion":1,"schema":"hideout.migration-manifest/v1"}`)
	f.Fuzz(func(t *testing.T, input string) {
		// Structural fuzzing must keep each invocation well below the coordinator's
		// time-based smoke window. The independent deterministic boundary test
		// guards the full 16 MiB protocol limit.
		if len(input) > int(HardMaxMetadataBytes) {
			t.Skip()
		}
		canonical, err := canonicalizeJSON(
			[]byte(input), HardMaxManifestBytes,
		)
		if err != nil {
			return
		}
		defer clear(canonical)
		if validateManifestEnvelope(canonical, "migb_fuzzseed1234") != nil {
			return
		}
		var manifest Manifest
		if decodeCanonicalJSON(canonical, HardMaxManifestBytes, &manifest) == nil {
			_ = manifest.Validate(DefaultLimits())
		}
	})
}

func FuzzMigrationTrailer(f *testing.F) {
	seed := encodeTrailer(trailer{})
	f.Add(string(seed[:]))
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > TrailerSize*2 {
			t.Skip()
		}
		_, _ = decodeTrailer([]byte(input))
	})
}

func FuzzMigrationZstdRecord(f *testing.F) {
	plaintext := []byte("bounded zstd migration seed")
	encoded, err := encodeRecordPayload(RecordDataChunk, plaintext)
	if err != nil {
		f.Fatal(err)
	}
	seed := make([]byte, 4+sha256.Size+len(encoded))
	binary.BigEndian.PutUint32(seed[:4], uint32(len(plaintext)))
	digest := sha256.Sum256(plaintext)
	copy(seed[4:4+sha256.Size], digest[:])
	copy(seed[4+sha256.Size:], encoded)
	clear(encoded)
	f.Add(string(seed))
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) < 4+sha256.Size ||
			len(input) > 4+sha256.Size+int(HardMaxChunkBytes+HardMaxRecordOverhead) {
			t.Skip()
		}
		bytes := []byte(input)
		declared := binary.BigEndian.Uint32(bytes[:4])
		encoded := bytes[4+sha256.Size:]
		if declared == 0 || declared > HardMaxChunkBytes || len(encoded) == 0 ||
			uint64(len(encoded)) > uint64(declared)+uint64(HardMaxRecordOverhead) {
			return
		}
		header := PrivateRecordHeader{
			Version: RecordPrivateVersion, Type: RecordDataChunk,
			ComponentID:     "component_fuzz1234",
			PlaintextLength: uint64(declared), EncodedLength: uint64(len(encoded)),
			PlaintextDigest: bytesToDigest(bytes[4 : 4+sha256.Size]),
		}
		plaintext, err := decodeRecordPayload(header, encoded, DefaultLimits())
		if err != nil {
			return
		}
		defer clear(plaintext)
		if uint64(len(plaintext)) != uint64(declared) {
			t.Fatalf("decoder returned %d bytes for declared %d", len(plaintext), declared)
		}
	})
}
