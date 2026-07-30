package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

const (
	DNSPacketEgress  uint32 = 1
	DNSPacketIngress uint32 = 2

	DNSPacketFlagTruncated            uint32 = 1 << 0
	DNSPacketFlagEncrypted            uint32 = 1 << 1
	DNSPacketFlagExecutionUnavailable uint32 = 1 << 2

	DNSPacketPayloadBytes = 512
	DNSPacketRecordSize   = 624
)

var ErrDNSPacketRecord = errors.New(
	"workload observer DNS packet record is invalid",
)

type RawDNSPacket struct {
	Direction      uint32
	CPU            uint32
	PID            uint32
	TID            uint32
	ExecutionPID   uint32
	UID            uint32
	GID            uint32
	Family         uint32
	Protocol       uint32
	ResolverPort   uint32
	Flags          uint32
	WireLength     uint32
	CapturedLength uint32
	Reserved       uint32

	CgroupID         uint64
	ObserverSequence uint64
	ExecSequence     uint64
	MonotonicNS      uint64
	SocketCookie     uint64

	Address [NetworkAddressBytes]byte
	Payload [DNSPacketPayloadBytes]byte

	payloadTaken bool
}

// DecodeDNSPacket takes ownership of record and clears all input bytes before
// returning. A decoded payload must subsequently be transferred with
// TakePayload, which clears the fixed record buffer.
func DecodeDNSPacket(record []byte) (RawDNSPacket, error) {
	defer clear(record)
	if len(record) != DNSPacketRecordSize {
		return RawDNSPacket{}, fmt.Errorf(
			"%w: size=%d want=%d",
			ErrDNSPacketRecord,
			len(record),
			DNSPacketRecordSize,
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
	packet := RawDNSPacket{
		Direction: next32(), CPU: next32(),
		PID: next32(), TID: next32(),
		ExecutionPID: next32(), UID: next32(), GID: next32(),
		Family: next32(), Protocol: next32(),
		ResolverPort: next32(), Flags: next32(),
		WireLength: next32(), CapturedLength: next32(),
		Reserved: next32(),
		CgroupID: next64(), ObserverSequence: next64(),
		ExecSequence: next64(), MonotonicNS: next64(),
		SocketCookie: next64(),
	}
	copy(
		packet.Address[:],
		record[offset:offset+NetworkAddressBytes],
	)
	offset += NetworkAddressBytes
	copy(
		packet.Payload[:],
		record[offset:offset+DNSPacketPayloadBytes],
	)
	offset += DNSPacketPayloadBytes
	if offset != len(record) || !packet.valid() {
		packet.ClearPayload()
		return RawDNSPacket{}, ErrDNSPacketRecord
	}
	return packet, nil
}

func (packet RawDNSPacket) Validate() error {
	if !packet.valid() {
		return ErrDNSPacketRecord
	}
	return nil
}

func (packet RawDNSPacket) ResolverIP() net.IP {
	switch packet.Family {
	case NetworkFamilyIPv4:
		return append(net.IP(nil), packet.Address[:net.IPv4len]...)
	case NetworkFamilyIPv6:
		return append(net.IP(nil), packet.Address[:]...)
	default:
		return nil
	}
}

func (packet *RawDNSPacket) TakePayload() []byte {
	if packet == nil || packet.payloadTaken {
		return nil
	}
	packet.payloadTaken = true
	length := int(packet.CapturedLength)
	if length < 0 || length > len(packet.Payload) {
		packet.ClearPayload()
		return nil
	}
	result := make([]byte, length)
	copy(result, packet.Payload[:length])
	clear(packet.Payload[:])
	return result
}

func (packet *RawDNSPacket) ClearPayload() {
	if packet == nil {
		return
	}
	clear(packet.Payload[:])
	packet.payloadTaken = true
}

func (packet RawDNSPacket) valid() bool {
	if packet.payloadTaken ||
		(packet.Direction != DNSPacketEgress &&
			packet.Direction != DNSPacketIngress) ||
		packet.PID == 0 ||
		packet.PID > 4194304 ||
		packet.TID == 0 ||
		packet.TID > 4194304 ||
		packet.CgroupID == 0 ||
		packet.ObserverSequence == 0 ||
		packet.MonotonicNS == 0 ||
		packet.SocketCookie == 0 ||
		packet.ResolverPort == 0 ||
		packet.ResolverPort > 65535 ||
		packet.Reserved != 0 ||
		packet.Flags&^knownDNSPacketFlags() != 0 ||
		packet.WireLength > 65535 ||
		packet.CapturedLength > DNSPacketPayloadBytes {
		return false
	}
	switch packet.Protocol {
	case NetworkProtocolTCP, NetworkProtocolUDP:
	default:
		return false
	}
	switch packet.Family {
	case NetworkFamilyIPv4:
		for _, current := range packet.Address[net.IPv4len:] {
			if current != 0 {
				return false
			}
		}
	case NetworkFamilyIPv6:
	default:
		return false
	}
	ip := packet.ResolverIP()
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	executionUnavailable :=
		packet.Flags&DNSPacketFlagExecutionUnavailable != 0
	if executionUnavailable {
		if packet.ExecutionPID != 0 || packet.ExecSequence != 0 {
			return false
		}
	} else if packet.ExecutionPID == 0 ||
		packet.ExecutionPID > 4194304 ||
		packet.ExecSequence == 0 {
		return false
	}
	encrypted := packet.Flags&DNSPacketFlagEncrypted != 0
	truncated := packet.Flags&DNSPacketFlagTruncated != 0
	if encrypted {
		if packet.Protocol != NetworkProtocolTCP ||
			packet.WireLength != 0 ||
			packet.CapturedLength != 0 ||
			truncated {
			return false
		}
	} else {
		if packet.WireLength == 0 ||
			packet.CapturedLength == 0 ||
			packet.CapturedLength > packet.WireLength ||
			truncated != (packet.CapturedLength < packet.WireLength) {
			return false
		}
	}
	for _, current := range packet.Payload[packet.CapturedLength:] {
		if current != 0 {
			return false
		}
	}
	return true
}

func knownDNSPacketFlags() uint32 {
	return DNSPacketFlagTruncated |
		DNSPacketFlagEncrypted |
		DNSPacketFlagExecutionUnavailable
}
