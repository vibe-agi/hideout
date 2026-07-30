package bpf

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

func TestDecodeNetworkEventAcceptsCanonicalConnectAndSendmsgRecords(t *testing.T) {
	tests := []struct {
		name     string
		kind     uint32
		family   uint32
		protocol uint32
		ip       string
		port     uint32
		wantIP   string
	}{
		{
			name: "connect4 tcp", kind: NetworkEventConnect,
			family: NetworkFamilyIPv4, protocol: NetworkProtocolTCP,
			ip: "203.0.113.10", port: 443, wantIP: "203.0.113.10",
		},
		{
			name: "connect6 tcp", kind: NetworkEventConnect,
			family: NetworkFamilyIPv6, protocol: NetworkProtocolTCP,
			ip: "2001:db8::10", port: 8443, wantIP: "2001:db8::10",
		},
		{
			name: "sendmsg4 udp", kind: NetworkEventSendmsg,
			family: NetworkFamilyIPv4, protocol: NetworkProtocolUDP,
			ip: "198.51.100.53", port: 53, wantIP: "198.51.100.53",
		},
		{
			name: "sendmsg6 udp", kind: NetworkEventSendmsg,
			family: NetworkFamilyIPv6, protocol: NetworkProtocolUDP,
			ip: "2001:db8::53", port: 53, wantIP: "2001:db8::53",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := networkRecordFixture(
				testCase.kind,
				testCase.family,
				testCase.protocol,
				testCase.ip,
				testCase.port,
			)
			event, err := DecodeNetworkEvent(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind != testCase.kind ||
				event.CPU != 2 ||
				event.PID != 42 ||
				event.TID != 43 ||
				event.UID != 1000 ||
				event.GID != 1001 ||
				event.ExecutionPID != 42 ||
				event.CgroupID != 3141 ||
				event.ObserverSequence != 7 ||
				event.ExecSequence != 3 ||
				event.MonotonicNS != 9000 ||
				event.SocketCookie != 99 ||
				event.DestinationPort != testCase.port ||
				event.Protocol != testCase.protocol ||
				event.Family != testCase.family ||
				event.DestinationIP().String() != testCase.wantIP {
				t.Fatalf("event=%+v ip=%q", event, event.DestinationIP())
			}
		})
	}
}

func TestDecodeNetworkEventRejectsMalformedOrAmbiguousEvidence(t *testing.T) {
	valid := networkRecordFixture(
		NetworkEventConnect,
		NetworkFamilyIPv4,
		NetworkProtocolTCP,
		"203.0.113.10",
		443,
	)
	for name, mutate := range map[string]func([]byte){
		"kind": func(value []byte) {
			binary.LittleEndian.PutUint32(value[0:4], 99)
		},
		"pid": func(value []byte) {
			binary.LittleEndian.PutUint32(value[8:12], 0)
		},
		"family": func(value []byte) {
			binary.LittleEndian.PutUint32(value[24:28], 99)
		},
		"protocol": func(value []byte) {
			binary.LittleEndian.PutUint32(value[28:32], 99)
		},
		"sendmsg tcp": func(value []byte) {
			binary.LittleEndian.PutUint32(value[0:4], NetworkEventSendmsg)
		},
		"port": func(value []byte) {
			binary.LittleEndian.PutUint32(value[32:36], 0)
		},
		"reserved": func(value []byte) {
			binary.LittleEndian.PutUint32(value[44:48], 1)
		},
		"flags": func(value []byte) {
			binary.LittleEndian.PutUint32(value[40:44], 1<<31)
		},
		"missing cookie flag": func(value []byte) {
			binary.LittleEndian.PutUint64(value[80:88], 0)
		},
		"contradictory cookie flag": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[40:44],
				NetworkFlagCookieUnavailable,
			)
		},
		"missing execution flag": func(value []byte) {
			binary.LittleEndian.PutUint32(value[36:40], 0)
			binary.LittleEndian.PutUint64(value[64:72], 0)
		},
		"execution pid": func(value []byte) {
			binary.LittleEndian.PutUint32(value[36:40], 4194305)
		},
		"contradictory execution flag": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[40:44],
				NetworkFlagExecutionUnavailable,
			)
		},
		"ipv4 nonzero tail": func(value []byte) {
			value[111] = 1
		},
		"unspecified address": func(value []byte) {
			clear(value[96:112])
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := append([]byte(nil), valid...)
			mutate(fixture)
			if _, err := DecodeNetworkEvent(fixture); !errors.Is(
				err,
				ErrNetworkRecord,
			) {
				t.Fatalf("error=%v want=%v", err, ErrNetworkRecord)
			}
		})
	}
	for _, size := range []int{0, NetworkRecordSize - 1, NetworkRecordSize + 1} {
		if _, err := DecodeNetworkEvent(make([]byte, size)); !errors.Is(
			err,
			ErrNetworkRecord,
		) {
			t.Fatalf("size=%d error=%v want=%v", size, err, ErrNetworkRecord)
		}
	}
}

func TestDecodeNetworkEventPreservesInheritedExecutionOwner(t *testing.T) {
	fixture := networkRecordFixture(
		NetworkEventConnect,
		NetworkFamilyIPv4,
		NetworkProtocolTCP,
		"203.0.113.10",
		443,
	)
	binary.LittleEndian.PutUint32(fixture[8:12], 43)
	binary.LittleEndian.PutUint32(fixture[12:16], 44)
	event, err := DecodeNetworkEvent(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if event.PID != 43 ||
		event.TID != 44 ||
		event.ExecutionPID != 42 ||
		event.ExecSequence != 3 {
		t.Fatalf("event=%+v", event)
	}
}

func TestNetworkSocketEvidenceMustMatchExactEventIdentity(t *testing.T) {
	event, err := DecodeNetworkEvent(networkRecordFixture(
		NetworkEventConnect,
		NetworkFamilyIPv6,
		NetworkProtocolTCP,
		"2001:db8::20",
		443,
	))
	if err != nil {
		t.Fatal(err)
	}
	evidence := NetworkSocketEvidence{
		ObserverSequence: event.ObserverSequence,
		SocketCookie:     event.SocketCookie,
		IfIndex:          2,
		EgressPackets:    1,
		EgressBytes:      128,
	}
	if err := evidence.ValidateFor(event); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*NetworkSocketEvidence){
		"sequence": func(value *NetworkSocketEvidence) {
			value.ObserverSequence++
		},
		"cookie": func(value *NetworkSocketEvidence) {
			value.SocketCookie++
		},
		"packets without bytes": func(value *NetworkSocketEvidence) {
			value.EgressBytes = 0
		},
		"bytes without packets": func(value *NetworkSocketEvidence) {
			value.EgressPackets = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := evidence
			mutate(&candidate)
			if err := candidate.ValidateFor(event); !errors.Is(
				err,
				ErrNetworkCorrelation,
			) {
				t.Fatalf("error=%v want=%v", err, ErrNetworkCorrelation)
			}
		})
	}
}

func networkRecordFixture(
	kind, family, protocol uint32,
	ip string,
	port uint32,
) []byte {
	value := make([]byte, NetworkRecordSize)
	values32 := []uint32{
		kind, 2, 42, 43, 1000, 1001,
		family, protocol, port, 42, 0, 0,
	}
	offset := 0
	for _, current := range values32 {
		binary.LittleEndian.PutUint32(value[offset:offset+4], current)
		offset += 4
	}
	for _, current := range []uint64{3141, 7, 3, 9000, 99, 0} {
		binary.LittleEndian.PutUint64(value[offset:offset+8], current)
		offset += 8
	}
	parsed := net.ParseIP(ip)
	if family == NetworkFamilyIPv4 {
		copy(value[offset:offset+16], parsed.To4())
	} else {
		copy(value[offset:offset+16], parsed.To16())
	}
	return value
}
