package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

const (
	ProxyChunkEgress  uint32 = 1
	ProxyChunkIngress uint32 = 2

	ProxyChunkFlagTruncated            uint32 = 1 << 0
	ProxyChunkFlagExecutionUnavailable uint32 = 1 << 1

	ProxyChunkPayloadBytes = 512
	ProxyChunkRecordSize   = 624
)

var ErrProxyChunkRecord = errors.New(
	"workload observer proxy handshake chunk record is invalid",
)

// RawProxyChunk owns a transient, bounded prefix of one TCP payload segment.
// It must never be serialized or persisted.
type RawProxyChunk struct {
	Direction      uint32
	CPU            uint32
	PID            uint32
	TID            uint32
	ExecutionPID   uint32
	UID            uint32
	GID            uint32
	Family         uint32
	ProxyPort      uint32
	Flags          uint32
	WireLength     uint32
	CapturedLength uint32
	TCPSequence    uint32
	Reserved       uint32

	CgroupID         uint64
	ObserverSequence uint64
	ExecSequence     uint64
	MonotonicNS      uint64
	SocketCookie     uint64

	Address [NetworkAddressBytes]byte
	Payload [ProxyChunkPayloadBytes]byte

	payloadTaken bool
}

// DecodeProxyChunk takes ownership of record and clears all input bytes before
// returning. The decoded payload must then be moved with TakePayload, which
// clears the fixed record buffer.
func DecodeProxyChunk(record []byte) (RawProxyChunk, error) {
	defer clear(record)
	if len(record) != ProxyChunkRecordSize {
		return RawProxyChunk{}, fmt.Errorf(
			"%w: size=%d want=%d",
			ErrProxyChunkRecord,
			len(record),
			ProxyChunkRecordSize,
		)
	}
	offset := 0
	next32 := func() uint32 {
		value := binary.LittleEndian.Uint32(
			record[offset : offset+4],
		)
		offset += 4
		return value
	}
	next64 := func() uint64 {
		value := binary.LittleEndian.Uint64(
			record[offset : offset+8],
		)
		offset += 8
		return value
	}
	chunk := RawProxyChunk{
		Direction: next32(), CPU: next32(),
		PID: next32(), TID: next32(),
		ExecutionPID: next32(), UID: next32(), GID: next32(),
		Family: next32(), ProxyPort: next32(),
		Flags: next32(), WireLength: next32(),
		CapturedLength: next32(), TCPSequence: next32(),
		Reserved: next32(),
		CgroupID: next64(), ObserverSequence: next64(),
		ExecSequence: next64(), MonotonicNS: next64(),
		SocketCookie: next64(),
	}
	copy(
		chunk.Address[:],
		record[offset:offset+NetworkAddressBytes],
	)
	offset += NetworkAddressBytes
	copy(
		chunk.Payload[:],
		record[offset:offset+ProxyChunkPayloadBytes],
	)
	offset += ProxyChunkPayloadBytes
	if offset != len(record) || !chunk.valid() {
		chunk.ClearPayload()
		return RawProxyChunk{}, ErrProxyChunkRecord
	}
	return chunk, nil
}

func (chunk RawProxyChunk) Validate() error {
	if !chunk.valid() {
		return ErrProxyChunkRecord
	}
	return nil
}

func (chunk RawProxyChunk) ProxyIP() net.IP {
	switch chunk.Family {
	case NetworkFamilyIPv4:
		return append(net.IP(nil), chunk.Address[:net.IPv4len]...)
	case NetworkFamilyIPv6:
		return append(net.IP(nil), chunk.Address[:]...)
	default:
		return nil
	}
}

func (chunk *RawProxyChunk) TakePayload() []byte {
	if chunk == nil || chunk.payloadTaken {
		return nil
	}
	chunk.payloadTaken = true
	length := int(chunk.CapturedLength)
	if length < 0 || length > len(chunk.Payload) {
		chunk.ClearPayload()
		return nil
	}
	result := make([]byte, length)
	copy(result, chunk.Payload[:length])
	clear(chunk.Payload[:])
	return result
}

func (chunk *RawProxyChunk) ClearPayload() {
	if chunk == nil {
		return
	}
	clear(chunk.Payload[:])
	chunk.payloadTaken = true
}

func (chunk RawProxyChunk) valid() bool {
	if chunk.payloadTaken ||
		(chunk.Direction != ProxyChunkEgress &&
			chunk.Direction != ProxyChunkIngress) ||
		chunk.PID == 0 ||
		chunk.PID > 4194304 ||
		chunk.TID == 0 ||
		chunk.TID > 4194304 ||
		chunk.CgroupID == 0 ||
		chunk.ObserverSequence == 0 ||
		chunk.MonotonicNS == 0 ||
		chunk.SocketCookie == 0 ||
		chunk.ProxyPort == 0 ||
		chunk.ProxyPort > 65535 ||
		chunk.Reserved != 0 ||
		chunk.Flags&^knownProxyChunkFlags() != 0 ||
		chunk.WireLength == 0 ||
		chunk.WireLength > 65535 ||
		chunk.CapturedLength == 0 ||
		chunk.CapturedLength > ProxyChunkPayloadBytes ||
		chunk.CapturedLength > chunk.WireLength {
		return false
	}
	switch chunk.Family {
	case NetworkFamilyIPv4:
		for _, current := range chunk.Address[net.IPv4len:] {
			if current != 0 {
				return false
			}
		}
	case NetworkFamilyIPv6:
	default:
		return false
	}
	ip := chunk.ProxyIP()
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	executionUnavailable :=
		chunk.Flags&ProxyChunkFlagExecutionUnavailable != 0
	if executionUnavailable {
		if chunk.ExecutionPID != 0 || chunk.ExecSequence != 0 {
			return false
		}
	} else if chunk.ExecutionPID == 0 ||
		chunk.ExecutionPID > 4194304 ||
		chunk.ExecSequence == 0 {
		return false
	}
	truncated := chunk.Flags&ProxyChunkFlagTruncated != 0
	if truncated != (chunk.CapturedLength < chunk.WireLength) {
		return false
	}
	for _, current := range chunk.Payload[chunk.CapturedLength:] {
		if current != 0 {
			return false
		}
	}
	return true
}

func knownProxyChunkFlags() uint32 {
	return ProxyChunkFlagTruncated |
		ProxyChunkFlagExecutionUnavailable
}
