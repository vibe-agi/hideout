package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

const (
	NetworkEventConnect uint32 = 1
	NetworkEventSendmsg uint32 = 2

	NetworkFamilyIPv4 uint32 = 4
	NetworkFamilyIPv6 uint32 = 6

	NetworkProtocolTCP uint32 = 6
	NetworkProtocolUDP uint32 = 17

	NetworkFlagExecutionUnavailable uint32 = 1 << 0
	NetworkFlagCookieUnavailable    uint32 = 1 << 1
	NetworkFlagStateUnavailable     uint32 = 1 << 2

	NetworkAddressBytes = 16
	NetworkRecordSize   = 112
)

var (
	ErrNetworkRecord      = errors.New("workload observer network record is invalid")
	ErrNetworkCorrelation = errors.New("workload observer network socket correlation is invalid")
)

type RawNetworkEvent struct {
	Kind            uint32
	CPU             uint32
	PID             uint32
	TID             uint32
	UID             uint32
	GID             uint32
	Family          uint32
	Protocol        uint32
	DestinationPort uint32
	ExecutionPID    uint32
	Flags           uint32
	Reserved        uint32

	CgroupID         uint64
	ObserverSequence uint64
	ExecSequence     uint64
	MonotonicNS      uint64
	SocketCookie     uint64
	Bytes            uint64

	Address [NetworkAddressBytes]byte
}

type NetworkSocketEvidence struct {
	ObserverSequence uint64
	SocketCookie     uint64
	IfIndex          uint32
	EgressPackets    uint64
	EgressBytes      uint64
}

func DecodeNetworkEvent(record []byte) (RawNetworkEvent, error) {
	if len(record) != NetworkRecordSize {
		return RawNetworkEvent{}, fmt.Errorf(
			"%w: size=%d want=%d",
			ErrNetworkRecord,
			len(record),
			NetworkRecordSize,
		)
	}
	offset := 0
	next32 := func() uint32 {
		value := binary.LittleEndian.Uint32(record[offset : offset+4])
		offset += 4
		return value
	}
	next64 := func() uint64 {
		value := binary.LittleEndian.Uint64(record[offset : offset+8])
		offset += 8
		return value
	}
	event := RawNetworkEvent{
		Kind: next32(), CPU: next32(), PID: next32(), TID: next32(),
		UID: next32(), GID: next32(),
		Family: next32(), Protocol: next32(),
		DestinationPort: next32(), ExecutionPID: next32(),
		Flags: next32(), Reserved: next32(),
		CgroupID: next64(), ObserverSequence: next64(),
		ExecSequence: next64(), MonotonicNS: next64(),
		SocketCookie: next64(), Bytes: next64(),
	}
	copy(event.Address[:], record[offset:offset+NetworkAddressBytes])
	offset += NetworkAddressBytes
	if offset != len(record) || !event.valid() {
		return RawNetworkEvent{}, ErrNetworkRecord
	}
	return event, nil
}

func (event RawNetworkEvent) DestinationIP() net.IP {
	switch event.Family {
	case NetworkFamilyIPv4:
		return append(net.IP(nil), event.Address[:net.IPv4len]...)
	case NetworkFamilyIPv6:
		return append(net.IP(nil), event.Address[:]...)
	default:
		return nil
	}
}

func (event RawNetworkEvent) Validate() error {
	if !event.valid() {
		return ErrNetworkRecord
	}
	return nil
}

func (event RawNetworkEvent) valid() bool {
	if event.Kind != NetworkEventConnect &&
		event.Kind != NetworkEventSendmsg {
		return false
	}
	if event.PID == 0 || event.PID > 4194304 ||
		event.TID == 0 || event.TID > 4194304 ||
		event.CgroupID == 0 ||
		event.ObserverSequence == 0 ||
		event.MonotonicNS == 0 ||
		event.DestinationPort == 0 ||
		event.DestinationPort > 65535 ||
		event.Bytes != 0 ||
		event.Reserved != 0 ||
		event.Flags&^knownNetworkFlags() != 0 {
		return false
	}
	switch event.Protocol {
	case NetworkProtocolTCP, NetworkProtocolUDP:
	default:
		return false
	}
	if event.Kind == NetworkEventSendmsg &&
		event.Protocol != NetworkProtocolUDP {
		return false
	}
	switch event.Family {
	case NetworkFamilyIPv4:
		for _, current := range event.Address[net.IPv4len:] {
			if current != 0 {
				return false
			}
		}
	case NetworkFamilyIPv6:
	default:
		return false
	}
	ip := event.DestinationIP()
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	executionUnavailable :=
		event.Flags&NetworkFlagExecutionUnavailable != 0
	if executionUnavailable {
		if event.ExecutionPID != 0 || event.ExecSequence != 0 {
			return false
		}
	} else if event.ExecutionPID == 0 ||
		event.ExecutionPID > 4194304 ||
		event.ExecSequence == 0 {
		return false
	}
	cookieUnavailable :=
		event.Flags&NetworkFlagCookieUnavailable != 0
	return (event.SocketCookie == 0) == cookieUnavailable
}

func (evidence NetworkSocketEvidence) ValidateFor(
	event RawNetworkEvent,
) error {
	if !event.valid() ||
		evidence.ObserverSequence != event.ObserverSequence ||
		evidence.SocketCookie == 0 ||
		evidence.SocketCookie != event.SocketCookie {
		return ErrNetworkCorrelation
	}
	if evidence.EgressPackets == 0 {
		if evidence.IfIndex != 0 || evidence.EgressBytes != 0 {
			return ErrNetworkCorrelation
		}
		return nil
	}
	if evidence.EgressBytes == 0 {
		return ErrNetworkCorrelation
	}
	return nil
}

func knownNetworkFlags() uint32 {
	return NetworkFlagExecutionUnavailable |
		NetworkFlagCookieUnavailable |
		NetworkFlagStateUnavailable
}
