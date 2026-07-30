package bpf

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

func TestDecodeDNSPacketAcceptsBoundedUDPAndTCPMetadataAndClearsRecord(
	t *testing.T,
) {
	for _, testCase := range []struct {
		name      string
		direction uint32
		family    uint32
		protocol  uint32
		ip        string
		port      uint32
		payload   []byte
	}{
		{
			name:      "UDP query IPv4",
			direction: DNSPacketEgress,
			family:    NetworkFamilyIPv4, protocol: NetworkProtocolUDP,
			ip: "1.1.1.1", port: 53,
			payload: []byte{0x12, 0x34, 0x01, 0x00},
		},
		{
			name:      "UDP response IPv6",
			direction: DNSPacketIngress,
			family:    NetworkFamilyIPv6, protocol: NetworkProtocolUDP,
			ip: "2001:4860:4860::8888", port: 53,
			payload: []byte{0x12, 0x34, 0x81, 0x80},
		},
		{
			name:      "TCP framed response",
			direction: DNSPacketIngress,
			family:    NetworkFamilyIPv4, protocol: NetworkProtocolTCP,
			ip: "9.9.9.9", port: 53,
			payload: []byte{0x00, 0x04, 0x12, 0x34, 0x81, 0x80},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			record := dnsPacketRecordFixture(
				testCase.direction,
				testCase.family,
				testCase.protocol,
				testCase.ip,
				testCase.port,
				testCase.payload,
				0,
				len(testCase.payload),
			)
			packet, err := DecodeDNSPacket(record)
			if err != nil {
				t.Fatal(err)
			}
			if !allBytesZero(record) {
				t.Fatal("raw ring record was not cleared")
			}
			if packet.Direction != testCase.direction ||
				packet.PID != 42 ||
				packet.TID != 43 ||
				packet.ExecutionPID != 42 ||
				packet.UID != 1000 ||
				packet.GID != 1001 ||
				packet.Family != testCase.family ||
				packet.Protocol != testCase.protocol ||
				packet.ResolverPort != testCase.port ||
				packet.ResolverIP().String() != net.ParseIP(testCase.ip).String() ||
				packet.WireLength != uint32(len(testCase.payload)) ||
				packet.CapturedLength != uint32(len(testCase.payload)) {
				t.Fatalf("packet=%+v resolver=%s", packet, packet.ResolverIP())
			}
			payload := packet.TakePayload()
			if string(payload) != string(testCase.payload) {
				t.Fatalf("payload=%x want=%x", payload, testCase.payload)
			}
			if !allBytesZero(packet.Payload[:]) {
				t.Fatal("packet payload was not cleared after ownership transfer")
			}
			clear(payload)
		})
	}
}

func TestDecodeDNSPacketAcceptsEncryptedMetadataWithoutPayload(t *testing.T) {
	record := dnsPacketRecordFixture(
		DNSPacketEgress,
		NetworkFamilyIPv4,
		NetworkProtocolTCP,
		"1.1.1.1",
		853,
		nil,
		DNSPacketFlagEncrypted,
		0,
	)
	packet, err := DecodeDNSPacket(record)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Flags != DNSPacketFlagEncrypted ||
		packet.WireLength != 0 ||
		packet.CapturedLength != 0 ||
		len(packet.TakePayload()) != 0 {
		t.Fatalf("packet=%+v", packet)
	}
}

func TestDecodeDNSPacketRejectsMalformedEvidenceAndAlwaysClearsRecord(
	t *testing.T,
) {
	valid := dnsPacketRecordFixture(
		DNSPacketEgress,
		NetworkFamilyIPv4,
		NetworkProtocolUDP,
		"1.1.1.1",
		53,
		[]byte{0x12, 0x34, 0x01, 0x00},
		0,
		4,
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
		"protocol": func(value []byte) {
			binary.LittleEndian.PutUint32(value[32:36], 99)
		},
		"port": func(value []byte) {
			binary.LittleEndian.PutUint32(value[36:40], 0)
		},
		"flags": func(value []byte) {
			binary.LittleEndian.PutUint32(value[40:44], 1<<31)
		},
		"wire shorter than capture": func(value []byte) {
			binary.LittleEndian.PutUint32(value[44:48], 3)
		},
		"capture too large": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[48:52],
				DNSPacketPayloadBytes+1,
			)
		},
		"missing truncated flag": func(value []byte) {
			binary.LittleEndian.PutUint32(value[44:48], 5)
		},
		"contradictory truncated flag": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[40:44],
				DNSPacketFlagTruncated,
			)
		},
		"encrypted with payload": func(value []byte) {
			binary.LittleEndian.PutUint32(
				value[40:44],
				DNSPacketFlagEncrypted,
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
	} {
		t.Run(name, func(t *testing.T) {
			record := append([]byte(nil), valid...)
			mutate(record)
			if _, err := DecodeDNSPacket(record); !errors.Is(
				err,
				ErrDNSPacketRecord,
			) {
				t.Fatalf("error=%v want=%v", err, ErrDNSPacketRecord)
			}
			if !allBytesZero(record) {
				t.Fatal("rejected raw record was not cleared")
			}
		})
	}
	for _, size := range []int{
		0,
		DNSPacketRecordSize - 1,
		DNSPacketRecordSize + 1,
	} {
		record := make([]byte, size)
		for index := range record {
			record[index] = 0x55
		}
		if _, err := DecodeDNSPacket(record); !errors.Is(
			err,
			ErrDNSPacketRecord,
		) {
			t.Fatalf("size=%d error=%v", size, err)
		}
		if !allBytesZero(record) {
			t.Fatalf("size=%d rejected bytes were not cleared", size)
		}
	}
}

func TestDNSPacketAllowsExplicitExecutionAndCaptureLoss(t *testing.T) {
	record := dnsPacketRecordFixture(
		DNSPacketIngress,
		NetworkFamilyIPv4,
		NetworkProtocolUDP,
		"1.1.1.1",
		53,
		[]byte{0x12, 0x34, 0x81, 0x80},
		DNSPacketFlagTruncated,
		600,
	)
	packet, err := DecodeDNSPacket(record)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Flags&DNSPacketFlagTruncated == 0 ||
		packet.WireLength != 600 ||
		packet.CapturedLength != 4 {
		t.Fatalf("packet=%+v", packet)
	}

	record = dnsPacketRecordFixture(
		DNSPacketEgress,
		NetworkFamilyIPv4,
		NetworkProtocolUDP,
		"1.1.1.1",
		53,
		[]byte{0x12, 0x34, 0x01, 0x00},
		DNSPacketFlagExecutionUnavailable,
		4,
	)
	binary.LittleEndian.PutUint32(record[16:20], 0)
	binary.LittleEndian.PutUint64(record[72:80], 0)
	packet, err = DecodeDNSPacket(record)
	if err != nil {
		t.Fatal(err)
	}
	if packet.ExecutionPID != 0 ||
		packet.ExecSequence != 0 ||
		packet.Flags&DNSPacketFlagExecutionUnavailable == 0 {
		t.Fatalf("packet=%+v", packet)
	}
}

func dnsPacketRecordFixture(
	direction, family, protocol uint32,
	ipText string,
	port uint32,
	payload []byte,
	flags uint32,
	wireLength int,
) []byte {
	value := make([]byte, DNSPacketRecordSize)
	values32 := []uint32{
		direction, 2, 42, 43, 42, 1000, 1001,
		family, protocol, port, flags,
		uint32(wireLength), uint32(len(payload)), 0,
	}
	offset := 0
	for _, current := range values32 {
		binary.LittleEndian.PutUint32(
			value[offset:offset+4],
			current,
		)
		offset += 4
	}
	for _, current := range []uint64{3141, 7, 3, 9000, 99} {
		binary.LittleEndian.PutUint64(
			value[offset:offset+8],
			current,
		)
		offset += 8
	}
	ip := net.ParseIP(ipText)
	if family == NetworkFamilyIPv4 {
		copy(value[offset:offset+16], ip.To4())
	} else {
		copy(value[offset:offset+16], ip.To16())
	}
	offset += 16
	copy(value[offset:offset+DNSPacketPayloadBytes], payload)
	return value
}

func allBytesZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}
