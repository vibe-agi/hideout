package store

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
)

const (
	frameHeaderBytes   = 4
	frameChecksumBytes = 4
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

type segmentScan struct {
	Data           []byte
	Entries        []segmentEntry
	ValidBytes     int64
	FailureKind    string
	DiscardedBytes int64
}

func canonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func encodeFrame(entry segmentEntry, maximum int64) ([]byte, error) {
	payload, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	total := frameHeaderBytes + len(payload) + frameChecksumBytes
	if len(payload) == 0 || int64(total) > maximum {
		return nil, ErrFrameTooLarge
	}
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[:frameHeaderBytes], uint32(len(payload)))
	copy(frame[frameHeaderBytes:], payload)
	binary.BigEndian.PutUint32(
		frame[frameHeaderBytes+len(payload):],
		crc32.Checksum(payload, crc32cTable),
	)
	return frame, nil
}

func scanSegment(path string, ownerKeyMaximum int64, ownerValidator func(segmentEntry) error) (segmentScan, error) {
	data, err := readPrivateFile(path, ownerKeyMaximum)
	if err != nil {
		return segmentScan{}, err
	}
	result := segmentScan{Data: data}
	offset := 0
	for offset < len(data) {
		remaining := len(data) - offset
		if remaining < frameHeaderBytes {
			result.ValidBytes = int64(offset)
			result.FailureKind = RepairTornWrite
			result.DiscardedBytes = int64(remaining)
			return result, nil
		}
		payloadBytes := int(binary.BigEndian.Uint32(data[offset : offset+frameHeaderBytes]))
		if payloadBytes <= 0 ||
			int64(payloadBytes+frameHeaderBytes+frameChecksumBytes) > ownerKeyMaximum {
			result.ValidBytes = int64(offset)
			result.FailureKind = RepairInvalidFrame
			result.DiscardedBytes = int64(remaining)
			return result, nil
		}
		frameBytes := frameHeaderBytes + payloadBytes + frameChecksumBytes
		if remaining < frameBytes {
			result.ValidBytes = int64(offset)
			result.FailureKind = RepairTornWrite
			result.DiscardedBytes = int64(remaining)
			return result, nil
		}
		payloadStart := offset + frameHeaderBytes
		payloadEnd := payloadStart + payloadBytes
		payload := data[payloadStart:payloadEnd]
		expected := binary.BigEndian.Uint32(data[payloadEnd : payloadEnd+frameChecksumBytes])
		if crc32.Checksum(payload, crc32cTable) != expected {
			result.ValidBytes = int64(offset)
			result.FailureKind = RepairCRCFailure
			result.DiscardedBytes = int64(remaining)
			return result, nil
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		var entry segmentEntry
		if err := decoder.Decode(&entry); err != nil ||
			ownerValidator(entry) != nil {
			result.ValidBytes = int64(offset)
			result.FailureKind = RepairInvalidFrame
			result.DiscardedBytes = int64(remaining)
			return result, nil
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			result.ValidBytes = int64(offset)
			result.FailureKind = RepairInvalidFrame
			result.DiscardedBytes = int64(remaining)
			return result, nil
		}
		result.Entries = append(result.Entries, entry)
		offset += frameBytes
	}
	result.ValidBytes = int64(offset)
	return result, nil
}
