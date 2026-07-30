package dns

import (
	"errors"
	"net"
	"testing"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	processcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/process"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestPacketFromKernelRecordPreservesExactActorAndTransfersPayload(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	processNormalizer, err := processcollector.NewNormalizer(
		processcollector.Boundary{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
		},
		processcollector.ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 11, 59, 59, 0, time.UTC),
			MonotonicNS: 800,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := processNormalizer.Apply(processcollector.Event{
		Kind:  processcollector.EventExec,
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		PID:                actor.PID, TID: actor.PID,
		ExecSequence: 3,
		MonotonicNS:  900,
		Sequence:     1,
		Executable:   "/usr/bin/claude",
		Argv:         []string{"claude"},
		Cwd:          "/workspace",
		Identity: workloadtypes.GuestIdentity{
			UID: actor.UID, GID: actor.GID,
		},
	}); err != nil {
		t.Fatal(err)
	}
	wire := dnsQueryWire(t, 0x1234, "api.example.test", 1)
	raw := kernelDNSPacket(
		boundary,
		actor,
		observerbpf.DNSPacketEgress,
		observerbpf.NetworkProtocolUDP,
		"1.1.1.1",
		53,
		wire,
	)
	packet, err := PacketFromKernelRecord(
		boundary,
		ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1000,
		},
		"cov_dns_fixture",
		&raw,
		processNormalizer.LookupActor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Actor.ExecutionID != actor.ExecutionID ||
		packet.Actor.PID != raw.PID ||
		packet.Direction != DirectionEgress ||
		packet.Transport != TransportUDP ||
		packet.ResolverIP != "1.1.1.1" ||
		packet.ResolverPort != 53 ||
		packet.WireLength != len(wire) ||
		string(packet.Payload) != string(wire) {
		t.Fatalf("packet=%+v", packet)
	}
	if !allZero(raw.Payload[:]) {
		t.Fatal("raw kernel payload was not cleared")
	}
	if event, err := newParserForKernelTest(
		t,
		boundary,
	).Consume(&packet); err != nil || event != nil {
		t.Fatalf("event=%+v error=%v", event, err)
	}
	if !allZero(packet.Payload) || packet.Payload != nil {
		t.Fatal("parser did not clear transferred payload")
	}
}

func TestPacketFromKernelRecordAttributesForkedChildToInheritedExecution(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	raw := kernelDNSPacket(
		boundary,
		actor,
		observerbpf.DNSPacketIngress,
		observerbpf.NetworkProtocolUDP,
		"1.1.1.1",
		53,
		dnsResponseWire(
			t,
			0x1234,
			"api.example.test",
			1,
			0,
			nil,
		),
	)
	raw.PID = 84
	raw.TID = 85
	packet, err := PacketFromKernelRecord(
		boundary,
		ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1000,
		},
		"cov_dns_fixture",
		&raw,
		func(pid uint32, sequence uint64) (workloadtypes.Actor, bool) {
			if pid != actor.PID || sequence != raw.ExecSequence {
				t.Fatalf("lookup pid=%d sequence=%d", pid, sequence)
			}
			return actor, true
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Actor.ExecutionID != actor.ExecutionID ||
		packet.Actor.PID != 84 ||
		packet.Direction != DirectionIngress {
		t.Fatalf("packet=%+v", packet)
	}
	clear(packet.Payload)
}

func TestPacketFromKernelRecordClearsPayloadOnEveryFailure(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	for name, testCase := range map[string]struct {
		mutate   func(*observerbpf.RawDNSPacket)
		lookup   networkActorLookup
		expected error
	}{
		"boundary": {
			mutate: func(value *observerbpf.RawDNSPacket) {
				value.CgroupID++
			},
			lookup: func(uint32, uint64) (workloadtypes.Actor, bool) {
				return actor, true
			},
			expected: ErrKernelPacket,
		},
		"actor": {
			mutate: func(*observerbpf.RawDNSPacket) {},
			lookup: func(uint32, uint64) (workloadtypes.Actor, bool) {
				return workloadtypes.Actor{}, false
			},
			expected: ErrExecutionUnknown,
		},
		"truncated": {
			mutate: func(value *observerbpf.RawDNSPacket) {
				value.Flags |= observerbpf.DNSPacketFlagTruncated
				value.WireLength++
			},
			lookup: func(uint32, uint64) (workloadtypes.Actor, bool) {
				return actor, true
			},
			expected: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := kernelDNSPacket(
				boundary,
				actor,
				observerbpf.DNSPacketEgress,
				observerbpf.NetworkProtocolUDP,
				"1.1.1.1",
				53,
				dnsQueryWire(t, 0x1234, "api.example.test", 1),
			)
			testCase.mutate(&raw)
			packet, err := PacketFromKernelRecord(
				boundary,
				ClockAnchor{
					WallTime:    time.Now(),
					MonotonicNS: 1000,
				},
				"cov_dns_fixture",
				&raw,
				testCase.lookup,
			)
			if testCase.expected == nil {
				if err != nil || !packet.Truncated {
					t.Fatalf("packet=%+v error=%v", packet, err)
				}
				clear(packet.Payload)
			} else if !errors.Is(err, testCase.expected) {
				t.Fatalf("error=%v want=%v", err, testCase.expected)
			}
			if !allZero(raw.Payload[:]) {
				t.Fatal("raw payload was retained")
			}
		})
	}
}

type networkActorLookup func(
	uint32,
	uint64,
) (workloadtypes.Actor, bool)

func kernelDNSPacket(
	boundary networkcollector.Boundary,
	actor workloadtypes.Actor,
	direction, protocol uint32,
	resolver string,
	port uint32,
	payload []byte,
) observerbpf.RawDNSPacket {
	result := observerbpf.RawDNSPacket{
		Direction:        direction,
		CPU:              2,
		PID:              actor.PID,
		TID:              actor.PID,
		ExecutionPID:     actor.PID,
		UID:              actor.UID,
		GID:              actor.GID,
		Family:           observerbpf.NetworkFamilyIPv4,
		Protocol:         protocol,
		ResolverPort:     port,
		WireLength:       uint32(len(payload)),
		CapturedLength:   uint32(len(payload)),
		CgroupID:         boundary.CgroupID,
		ObserverSequence: 7,
		ExecSequence:     3,
		MonotonicNS:      2000,
		SocketCookie:     99,
	}
	copy(result.Address[:], net.ParseIP(resolver).To4())
	copy(result.Payload[:], payload)
	return result
}

func newParserForKernelTest(
	t *testing.T,
	boundary networkcollector.Boundary,
) *Parser {
	t.Helper()
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return parser
}
