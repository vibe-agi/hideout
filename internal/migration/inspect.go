package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
)

// SealedBundleInspection is the secret-free result of authenticating one
// immutable migration bundle. It deliberately omits KDF material, wrapped keys,
// record plaintext, and selected secret values.
type SealedBundleInspection struct {
	CreatedAt string
	Summary   BundleSummary
	Binding   BundleBinding
	Manifest  Manifest
}

// PublicBundleInspection contains only the unencrypted facts needed to bind a
// one-shot secret-input handle to a concrete file before requesting its
// passphrase. TrailerPresent proves only physical completeness; authenticity is
// established exclusively by InspectSealedBundle.
type PublicBundleInspection struct {
	BundleID       BundleID
	FormatVersion  uint16
	CreatedAt      string
	EncodedBytes   uint64
	HeaderDigest   Digest
	TrailerPresent bool
}

type inspectedComponentRecords struct {
	first        uint64
	last         uint64
	count        uint64
	logicalBytes uint64
	types        uint16
	contentHash  hash.Hash
}

// InspectPublicBundle performs bounded parsing without deriving a key or
// exposing KDF/wrapped-key bytes. Its result is safe to show before prompting.
func InspectPublicBundle(
	input io.ReaderAt,
	size int64,
) (PublicBundleInspection, error) {
	inspection, headerEnd, limits, err := inspectBundleHeader(input, size)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	if size < PrologueSize+TrailerSize+1 || headerEnd > uint64(size)-TrailerSize {
		return PublicBundleInspection{}, ErrIncompleteBundle
	}
	header := inspection
	trailerValue, trailerOffset, err := locateTrailer(input, size)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	if trailerValue.CompletionOffset < headerEnd ||
		trailerValue.CompletionOffset > trailerOffset ||
		trailerOffset-trailerValue.CompletionOffset < FrameHeaderSize ||
		trailerValue.CompletionSequence > limits.MaxPayloadRecords+1 {
		return PublicBundleInspection{}, corruptManifest("public trailer binding is out of range")
	}
	header.TrailerPresent = true
	return header, nil
}

// InspectBundleHeader binds an in-progress export to its authenticated-public
// header bytes without pretending the artifact is sealed or importable.
func InspectBundleHeader(
	input io.ReaderAt,
	size int64,
) (PublicBundleInspection, error) {
	inspection, _, _, err := inspectBundleHeader(input, size)
	return inspection, err
}

// AuthenticateBundleHeader proves that passphrase unwraps the master key bound
// to this exact public header. It deliberately does not require a trailer or
// inspect payload records, so it is suitable for issuing short-lived handles
// for both sealed input and resumable export partials. The unwrapped key is
// cleared before return and is never exposed to the caller.
func AuthenticateBundleHeader(
	input io.ReaderAt,
	size int64,
	passphrase []byte,
) (PublicBundleInspection, error) {
	inspection, headerEnd, _, err := inspectBundleHeader(input, size)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	prologueBytes := make([]byte, PrologueSize)
	if err := readAtFull(input, prologueBytes, 0); err != nil {
		return PublicBundleInspection{}, ErrIncompleteBundle
	}
	prologueValue, err := decodePrologue(prologueBytes)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	if headerEnd != uint64(PrologueSize)+uint64(prologueValue.HeaderLength) {
		return PublicBundleInspection{}, ErrCorruptBundle
	}
	headerBytes := make([]byte, int(prologueValue.HeaderLength))
	if err := readAtFull(input, headerBytes, PrologueSize); err != nil {
		return PublicBundleInspection{}, ErrIncompleteBundle
	}
	header, err := decodePublicHeader(headerBytes)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	aad, err := publicHeaderAAD(prologueBytes, header)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	master, err := UnwrapMasterKey(
		passphrase,
		header.WrappedMasterKey,
		header.KDF,
		header.Salt,
		header.WrapNonce,
		aad,
	)
	clear(aad)
	if err != nil {
		return PublicBundleInspection{}, err
	}
	master.Clear()
	return inspection, nil
}

func inspectBundleHeader(
	input io.ReaderAt,
	size int64,
) (PublicBundleInspection, uint64, Limits, error) {
	if input == nil || size < PrologueSize+1 {
		return PublicBundleInspection{}, 0, Limits{}, ErrIncompleteBundle
	}
	prologueBytes := make([]byte, PrologueSize)
	if err := readAtFull(input, prologueBytes, 0); err != nil {
		return PublicBundleInspection{}, 0, Limits{}, ErrIncompleteBundle
	}
	prologueValue, err := decodePrologue(prologueBytes)
	if err != nil {
		return PublicBundleInspection{}, 0, Limits{}, err
	}
	headerEnd := uint64(PrologueSize) + uint64(prologueValue.HeaderLength)
	if headerEnd > uint64(size) {
		return PublicBundleInspection{}, 0, Limits{}, ErrIncompleteBundle
	}
	headerBytes := make([]byte, int(prologueValue.HeaderLength))
	if err := readAtFull(input, headerBytes, PrologueSize); err != nil {
		return PublicBundleInspection{}, 0, Limits{}, ErrIncompleteBundle
	}
	header, err := decodePublicHeader(headerBytes)
	if err != nil {
		return PublicBundleInspection{}, 0, Limits{}, err
	}
	hasher := sha256.New()
	_, _ = hasher.Write(prologueBytes)
	_, _ = hasher.Write(headerBytes)
	return PublicBundleInspection{
		BundleID: header.BundleID, FormatVersion: prologueValue.Major,
		CreatedAt: header.CreatedAt, EncodedBytes: uint64(size),
		HeaderDigest: bytesToDigest(hasher.Sum(nil)), TrailerPresent: false,
	}, headerEnd, header.Limits, nil
}

// InspectSealedBundle authenticates the complete ordered frame stream, strictly
// validates the final manifest, and binds its component index to the observed
// records. The input is only read and record plaintext is discarded eagerly.
func InspectSealedBundle(
	ctx context.Context,
	input io.ReaderAt,
	size int64,
	passphrase []byte,
) (SealedBundleInspection, error) {
	if ctx == nil {
		return SealedBundleInspection{}, fmt.Errorf("%w: inspection context is nil", ErrInvalidBundle)
	}
	if err := ctx.Err(); err != nil {
		return SealedBundleInspection{}, err
	}
	reader, err := NewReader(input, size, passphrase)
	if err != nil {
		return SealedBundleInspection{}, err
	}
	defer reader.Close()

	components := make(map[OpaqueID]*inspectedComponentRecords)
	var completionDigest Digest
	for {
		if err := ctx.Err(); err != nil {
			return SealedBundleInspection{}, err
		}
		record, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return SealedBundleInspection{}, readErr
		}
		if record.Header.Type == RecordMetadata || record.Header.Type == RecordCheckpoint {
			canonical, canonicalErr := canonicalizeJSON(
				record.Plaintext, reader.limits.MaxMetadataBytes,
			)
			canonicalMatch := canonicalErr == nil && bytes.Equal(canonical, record.Plaintext)
			clear(canonical)
			if !canonicalMatch {
				clear(record.Plaintext)
				return SealedBundleInspection{}, corruptManifest("metadata record is not canonical JSON")
			}
		}
		if record.Header.Type == RecordCompletion {
			completionDigest = record.FrameDigest
		}
		if record.Header.Type != RecordFinalManifest &&
			record.Header.Type != RecordCompletion {
			facts := components[record.Header.ComponentID]
			if facts == nil {
				facts = &inspectedComponentRecords{}
			}
			if facts.count == 0 {
				facts.first = record.Sequence
			}
			facts.last = record.Sequence
			facts.count++
			if record.Header.Type != RecordCheckpoint {
				if record.Header.PlaintextLength > mathRemaining(facts.logicalBytes) {
					clear(record.Plaintext)
					return SealedBundleInspection{}, corruptManifest("component logical size overflowed")
				}
				facts.logicalBytes += record.Header.PlaintextLength
				if record.Header.Type == RecordRawChunk {
					if facts.contentHash == nil {
						facts.contentHash = sha256.New()
					}
					_, _ = facts.contentHash.Write(record.Plaintext)
				}
			}
			facts.types |= uint16(1) << uint16(record.Header.Type-1)
			components[record.Header.ComponentID] = facts
		}
		clear(record.Plaintext)
	}

	summary, err := reader.Summary()
	if err != nil {
		return SealedBundleInspection{}, err
	}
	manifestBytes := reader.Manifest()
	defer clear(manifestBytes)
	var manifest Manifest
	if err := decodeCanonicalJSON(
		manifestBytes, reader.limits.MaxManifestBytes, &manifest,
	); err != nil {
		return SealedBundleInspection{}, err
	}
	if manifest.BundleID != reader.header.BundleID {
		return SealedBundleInspection{}, corruptManifest("manifest bundle binding is invalid")
	}
	if err := manifest.Validate(reader.limits); err != nil {
		return SealedBundleInspection{}, err
	}
	if err := validateManifestRecordIndex(
		manifest, components, reader.manifestSequence, summary.LogicalBytes,
	); err != nil {
		return SealedBundleInspection{}, err
	}
	if completionDigest.Validate() != nil {
		return SealedBundleInspection{}, corruptManifest("completion digest is absent")
	}
	fileDigest, err := digestReaderAt(ctx, input, size)
	if err != nil {
		return SealedBundleInspection{}, err
	}
	return SealedBundleInspection{
		CreatedAt: reader.header.CreatedAt,
		Summary:   summary,
		Binding: BundleBinding{
			BundleID: reader.header.BundleID, FormatVersion: BundleFormatVersion,
			FileDigest: fileDigest, ManifestDigest: summary.ManifestDigest,
			CompletionDigest: completionDigest,
		},
		Manifest: manifest,
	}, nil
}

// VerifySealedBundleFile proves that an input is byte-for-byte identical to a
// previously authenticated sealed binding. It performs no key derivation and
// is therefore suitable for plan/apply revalidation while the one-shot import
// secret remains reserved for materialization.
func VerifySealedBundleFile(
	ctx context.Context,
	input io.ReaderAt,
	size int64,
	expected BundleBinding,
) error {
	if ctx == nil || input == nil || size <= 0 {
		return fmt.Errorf("%w: bundle verification input is invalid", ErrInvalidBundle)
	}
	if _, err := ParseBundleID(string(expected.BundleID)); err != nil ||
		expected.FormatVersion != BundleFormatVersion ||
		expected.FileDigest.Validate() != nil ||
		expected.ManifestDigest.Validate() != nil ||
		expected.CompletionDigest.Validate() != nil {
		return fmt.Errorf("%w: expected sealed binding is invalid", ErrInvalidBundle)
	}
	public, err := InspectPublicBundle(input, size)
	if err != nil {
		return err
	}
	if public.BundleID != expected.BundleID || public.FormatVersion != expected.FormatVersion {
		return ErrBundleChanged
	}
	digest, err := digestReaderAt(ctx, input, size)
	if err != nil {
		return err
	}
	if digest != expected.FileDigest {
		return ErrBundleChanged
	}
	return nil
}

func validateManifestRecordIndex(
	manifest Manifest,
	observed map[OpaqueID]*inspectedComponentRecords,
	manifestSequence uint64,
	observedLogicalBytes uint64,
) error {
	if len(observed) != len(manifest.ComponentIndex) {
		return corruptManifest("component record set does not match its index")
	}
	var indexedRecords uint64
	var indexedLogicalBytes uint64
	for _, entry := range manifest.ComponentIndex {
		facts, exists := observed[entry.ComponentID]
		if !exists || facts == nil || facts.first != entry.FirstRecord || facts.last != entry.LastRecord ||
			facts.count != entry.RecordCount || facts.logicalBytes != entry.LogicalBytes ||
			!validManifestComponentRecordTypes(entry.Kind, facts.types) {
			return corruptManifest("component record range does not match its index")
		}
		indexedRecords += entry.RecordCount
		if entry.Kind == "profile-state" {
			if facts.contentHash == nil || bytesToDigest(facts.contentHash.Sum(nil)) != entry.ContentDigest {
				return corruptManifest("profile state content digest does not match its index")
			}
		}
		if entry.Kind == "disk" || entry.Kind == "profile-state" {
			if entry.LogicalBytes > mathRemaining(indexedLogicalBytes) {
				return corruptManifest("indexed logical size overflowed")
			}
			indexedLogicalBytes += entry.LogicalBytes
		}
	}
	if indexedRecords != manifestSequence || indexedLogicalBytes != observedLogicalBytes {
		return corruptManifest("manifest aggregate record binding is invalid")
	}
	return nil
}

func validManifestComponentRecordTypes(kind string, mask uint16) bool {
	checkpoint := recordTypeMask(RecordCheckpoint)
	var permitted uint16
	var required uint16
	switch kind {
	case "disk":
		permitted = checkpoint |
			recordTypeMask(RecordDataChunk) | recordTypeMask(RecordRawChunk) |
			recordTypeMask(RecordZeroExtent) | recordTypeMask(RecordHoleExtent)
		required = permitted &^ checkpoint
	case "profile-state":
		permitted = checkpoint | recordTypeMask(RecordRawChunk)
		required = recordTypeMask(RecordRawChunk)
	case "secret-value":
		permitted = checkpoint | recordTypeMask(RecordSecretValue)
		required = recordTypeMask(RecordSecretValue)
	case "profile", "environment", "provider-metadata":
		permitted = checkpoint | recordTypeMask(RecordMetadata)
		required = recordTypeMask(RecordMetadata)
	default:
		return false
	}
	return mask&^permitted == 0 && mask&required != 0
}

func recordTypeMask(recordType RecordType) uint16 {
	return uint16(1) << uint16(recordType-1)
}

func digestReaderAt(
	ctx context.Context,
	input io.ReaderAt,
	size int64,
) (Digest, error) {
	if input == nil || size <= 0 {
		return "", ErrIncompleteBundle
	}
	hasher := sha256.New()
	buffer := make([]byte, 1<<20)
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		remaining := size - offset
		chunk := buffer
		if remaining < int64(len(chunk)) {
			chunk = chunk[:int(remaining)]
		}
		if err := readAtFull(input, chunk, offset); err != nil {
			return "", ErrIncompleteBundle
		}
		_, _ = hasher.Write(chunk)
		offset += int64(len(chunk))
	}
	return bytesToDigest(hasher.Sum(nil)), nil
}

func mathRemaining(value uint64) uint64 {
	return ^uint64(0) - value
}
