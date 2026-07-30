package bpf

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

func TestDecodeProxyChunkTransfersOnlyBoundedHandshakeBytesAndClearsRecord(
	t *testing.T,
) {
	for _, testCase := range []struct {
		name      string
		direction uint32
		family    uint32
		ip        string
		payload   []byte
	}{
		{
			name:      "IPv4 egress",
			direction: ProxyChunkEgress,
			family:    NetworkFamilyIPv4,
			ip:        "127.0.0.1",
			payload:   []byte{0x05, 0x01, 0x00},
		},
		{
			name:      "IPv6 ingress",
			direction: ProxyChunkIngress,
			family:    NetworkFamilyIPv6,
			ip:        "2001:db8::70",
			payload:   []byte{0x05, 0x00},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			record := proxyChunkRecordFixture(
				testCase.direction,
				testCase.family,
				testCase.ip,
				7890,
				100,
				testCase.payload,
				0,
				len(testCase.payload),
			)
			chunk, err := DecodeProxyChunk(record)
			if err != nil {
				t.Fatal(err)
			}
			if !allBytesZero(record) {
				t.Fatal("raw ring record was not cleared")
			}
			if chunk.Direction != testCase.direction ||
				chunk.PID != 42 ||
				chunk.TID != 43 ||
				chunk.ExecutionPID != 42 ||
				chunk.UID != 1000 ||
				chunk.GID != 1001 ||
				chunk.Family != testCase.family ||
				chunk.ProxyPort != 7890 ||
				chunk.ProxyIP().String() != net.ParseIP(testCase.ip).String() ||
				chunk.TCPSequence != 100 ||
				chunk.WireLength != uint32(len(testCase.payload)) ||
				chunk.CapturedLength != uint32(len(testCase.payload)) {
				t.Fatalf("chunk=%+v proxy=%s", chunk, chunk.ProxyIP())
			}
			payload := chunk.TakePayload()
			if string(payload) != string(testCase.payload) {
				t.Fatalf("payload=%x want=%x", payload, testCase.payload)
			}
			if !allBytesZero(chunk.Payload[:]) {
				t.Fatal("chunk payload was not cleared after transfer")
			}
			clear(payload)
		})
	}
}

func TestDecodeProxyChunkRejectsMalformedEvidenceAndAlwaysClearsRecord(
	t *testing.T,
) {
	valid := proxyChunkRecordFixture(
		ProxyChunkEgress,
		NetworkFamilyIPv4,
		"127.0.0.1",
		7890,
		100,
		[]byte{0x05, 0x01, 0x00},
		0,
		3,
	)
	for name, mutate := range map[string]func([]byte){
		"direction": func(value []byte) {
			binary.LittleEndian.PutUint32(value[0:4], 99)
		},
		"pid": func(value []byte) {
			binary.LittleEndian.PutUint32(value[8:12], 0)
		},
		"execution pid": func(value []byte) {
			binary.LittleEndian.PutUint32(value[16:20], 0)
		},
		"family": func(value []byte) {
			binary.LittleEndian.PutUint32(value[28:32], 99)
		},
		"port": func(value []byte) {
			binary.LittleEndian.PutUint32(value[32:36], 0)
		},
		"flags": func(value []byte) {
			binary.LittleEndian.PutUint32(value[36:40], 1<<31)
		},
		"wire shorter than capture": func(value []byte) {
			binary.LittleEndian.PutUint32(value[40:44], 2)
		},
		"capture too large": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[44:48],
				ProxyChunkPayloadBytes+1,
			)
		},
		"missing truncated flag": func(value []byte) {
			binary.LittleEndian.PutUint32(value[40:44], 4)
		},
		"contradictory truncated flag": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[36:40],
				ProxyChunkFlagTruncated,
			)
		},
		"missing execution flag": func(value []byte) {
			binary.LittleEndian.PutUint32(value[16:20], 0)
			binary.LittleEndian.PutUint64(value[72:80], 0)
		},
		"reserved": func(value []byte) {
			binary.LittleEndian.PutUint32(value[52:56], 1)
		},
		"address": func(value []byte) {
			clear(value[96:112])
		},
		"payload tail": func(value []byte) {
			value[ProxyChunkRecordSize-1] = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := append([]byte(nil), valid...)
			mutate(record)
			if _, err := DecodeProxyChunk(record); !errors.Is(
				err,
				ErrProxyChunkRecord,
			) {
				t.Fatalf("error=%v want=%v", err, ErrProxyChunkRecord)
			}
			if !allBytesZero(record) {
				t.Fatal("rejected raw record was not cleared")
			}
		})
	}
	for _, size := range []int{
		0,
		ProxyChunkRecordSize - 1,
		ProxyChunkRecordSize + 1,
	} {
		record := make([]byte, size)
		for index := range record {
			record[index] = 0x55
		}
		if _, err := DecodeProxyChunk(record); !errors.Is(
			err,
			ErrProxyChunkRecord,
		) {
			t.Fatalf("size=%d error=%v", size, err)
		}
		if !allBytesZero(record) {
			t.Fatalf("size=%d rejected bytes were not cleared", size)
		}
	}
}

func TestProxyChunkAllowsExplicitExecutionAndCaptureLoss(t *testing.T) {
	record := proxyChunkRecordFixture(
		ProxyChunkIngress,
		NetworkFamilyIPv4,
		"127.0.0.1",
		7890,
		200,
		[]byte{0x05, 0x00},
		ProxyChunkFlagTruncated,
		600,
	)
	chunk, err := DecodeProxyChunk(record)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Flags&ProxyChunkFlagTruncated == 0 ||
		chunk.WireLength != 600 ||
		chunk.CapturedLength != 2 {
		t.Fatalf("chunk=%+v", chunk)
	}
	chunk.ClearPayload()

	record = proxyChunkRecordFixture(
		ProxyChunkEgress,
		NetworkFamilyIPv4,
		"127.0.0.1",
		7890,
		300,
		[]byte{0x05},
		ProxyChunkFlagExecutionUnavailable,
		1,
	)
	binary.LittleEndian.PutUint32(record[16:20], 0)
	binary.LittleEndian.PutUint64(record[72:80], 0)
	chunk, err = DecodeProxyChunk(record)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.ExecutionPID != 0 || chunk.ExecSequence != 0 {
		t.Fatalf("chunk=%+v", chunk)
	}
	chunk.ClearPayload()
}

func proxyChunkRecordFixture(
	direction, family uint32,
	ipText string,
	port uint32,
	tcpSequence uint32,
	payload []byte,
	flags uint32,
	wireLength int,
) []byte {
	record := make([]byte, ProxyChunkRecordSize)
	values32 := []uint32{
		direction,
		2,
		42,
		43,
		42,
		1000,
		1001,
		family,
		port,
		flags,
		uint32(wireLength),
		uint32(len(payload)),
		tcpSequence,
		0,
	}
	offset := 0
	for _, value := range values32 {
		binary.LittleEndian.PutUint32(record[offset:offset+4], value)
		offset += 4
	}
	for _, value := range []uint64{
		4242,
		9,
		3,
		123456,
		99,
	} {
		binary.LittleEndian.PutUint64(record[offset:offset+8], value)
		offset += 8
	}
	ip := net.ParseIP(ipText)
	if family == NetworkFamilyIPv4 {
		copy(record[offset:offset+NetworkAddressBytes], ip.To4())
	} else {
		copy(record[offset:offset+NetworkAddressBytes], ip.To16())
	}
	offset += NetworkAddressBytes
	copy(record[offset:], payload)
	return record
}
