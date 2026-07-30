package dns

import (
	"errors"
	"net"
	"testing"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestProxyChunkFromKernelRecordFeedsValidatedSOCKS5Parser(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser := newTestSOCKS5Parser(t, boundary)
	anchor := ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 1000,
	}
	var target *networkcollector.ProxyTargetEvent
	for index, fixture := range []struct {
		direction   uint32
		tcpSequence uint32
		payload     []byte
	}{
		{
			direction:   observerbpf.ProxyChunkEgress,
			tcpSequence: 100,
			payload:     []byte{0x05, 0x01, 0x00},
		},
		{
			direction:   observerbpf.ProxyChunkIngress,
			tcpSequence: 200,
			payload:     []byte{0x05, 0x00},
		},
		{
			direction:   observerbpf.ProxyChunkEgress,
			tcpSequence: 103,
			payload: socksDomainRequest(
				t,
				"kernel.proxy.example.test",
				443,
			),
		},
	} {
		raw := kernelProxyChunk(
			boundary,
			actor,
			fixture.direction,
			fixture.tcpSequence,
			fixture.payload,
		)
		raw.ObserverSequence = uint64(index + 1)
		raw.MonotonicNS += uint64(index)
		chunk, err := ProxyChunkFromKernelRecord(
			boundary,
			anchor,
			&raw,
			func(pid uint32, sequence uint64) (workloadtypes.Actor, bool) {
				if pid != actor.PID || sequence != raw.ExecSequence {
					return workloadtypes.Actor{}, false
				}
				return actor, true
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !allZero(raw.Payload[:]) {
			t.Fatal("raw proxy payload was not cleared")
		}
		event, err := parser.Consume(&chunk)
		if err != nil {
			t.Fatal(err)
		}
		if !allZero(chunk.Payload) || chunk.Payload != nil {
			t.Fatal("parser did not clear transferred proxy payload")
		}
		if event != nil {
			target = event
		}
	}
	if target == nil ||
		target.Actor.ExecutionID != actor.ExecutionID ||
		target.Domain != "kernel.proxy.example.test" ||
		target.DestinationPort != 443 ||
		target.ProxyIP != "127.0.0.1" ||
		target.ProxyPort != 7890 {
		t.Fatalf("target=%+v", target)
	}
}

func TestProxyChunkFromKernelRecordPreservesForkedChildActor(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	raw := kernelProxyChunk(
		boundary,
		actor,
		observerbpf.ProxyChunkEgress,
		100,
		[]byte{0x05},
	)
	raw.PID = 84
	raw.TID = 85
	chunk, err := ProxyChunkFromKernelRecord(
		boundary,
		ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1000,
		},
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
	if chunk.Actor.ExecutionID != actor.ExecutionID ||
		chunk.Actor.PID != 84 ||
		chunk.TCPSequence != 100 ||
		chunk.RemoteIP != "127.0.0.1" ||
		chunk.RemotePort != 7890 {
		t.Fatalf("chunk=%+v", chunk)
	}
	clear(chunk.Payload)
}

func TestProxyChunkFromKernelRecordClearsPayloadOnEveryFailure(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	for name, testCase := range map[string]struct {
		mutate   func(*observerbpf.RawProxyChunk)
		lookup   networkActorLookup
		expected error
	}{
		"boundary": {
			mutate: func(value *observerbpf.RawProxyChunk) {
				value.CgroupID++
			},
			lookup: func(uint32, uint64) (workloadtypes.Actor, bool) {
				return actor, true
			},
			expected: ErrKernelProxyChunk,
		},
		"actor": {
			mutate: func(*observerbpf.RawProxyChunk) {},
			lookup: func(uint32, uint64) (workloadtypes.Actor, bool) {
				return workloadtypes.Actor{}, false
			},
			expected: ErrProxyExecutionUnknown,
		},
		"truncated": {
			mutate: func(value *observerbpf.RawProxyChunk) {
				value.Flags |= observerbpf.ProxyChunkFlagTruncated
				value.WireLength++
			},
			lookup: func(uint32, uint64) (workloadtypes.Actor, bool) {
				return actor, true
			},
			expected: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := kernelProxyChunk(
				boundary,
				actor,
				observerbpf.ProxyChunkEgress,
				100,
				[]byte{0x05, 0x01, 0x00},
			)
			testCase.mutate(&raw)
			chunk, err := ProxyChunkFromKernelRecord(
				boundary,
				ClockAnchor{
					WallTime:    time.Now(),
					MonotonicNS: 1000,
				},
				&raw,
				testCase.lookup,
			)
			if testCase.expected == nil {
				if err != nil || !chunk.Truncated {
					t.Fatalf("chunk=%+v error=%v", chunk, err)
				}
				clear(chunk.Payload)
			} else if !errors.Is(err, testCase.expected) {
				t.Fatalf("error=%v want=%v", err, testCase.expected)
			}
			if !allZero(raw.Payload[:]) {
				t.Fatal("raw payload was retained")
			}
		})
	}
}

func kernelProxyChunk(
	boundary networkcollector.Boundary,
	actor workloadtypes.Actor,
	direction uint32,
	tcpSequence uint32,
	payload []byte,
) observerbpf.RawProxyChunk {
	result := observerbpf.RawProxyChunk{
		Direction:        direction,
		CPU:              2,
		PID:              actor.PID,
		TID:              actor.PID,
		ExecutionPID:     actor.PID,
		UID:              actor.UID,
		GID:              actor.GID,
		Family:           observerbpf.NetworkFamilyIPv4,
		ProxyPort:        7890,
		WireLength:       uint32(len(payload)),
		CapturedLength:   uint32(len(payload)),
		TCPSequence:      tcpSequence,
		CgroupID:         boundary.CgroupID,
		ObserverSequence: 7,
		ExecSequence:     3,
		MonotonicNS:      2000,
		SocketCookie:     99,
	}
	copy(result.Address[:], net.ParseIP("127.0.0.1").To4())
	copy(result.Payload[:], payload)
	return result
}
