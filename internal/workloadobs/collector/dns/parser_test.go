package dns

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestParserConsumesUDPQueryResponseAndClearsWireBytes(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	queryWire := dnsQueryWire(t, 0x1234, "Api.Example.Test.", 1)
	queryInput := append([]byte(nil), queryWire...)
	queryPacket := dnsPacket(
		boundary,
		actor,
		1,
		at,
		99,
		DirectionEgress,
		TransportUDP,
		queryInput,
	)
	event, err := parser.Consume(&queryPacket)
	if err != nil {
		t.Fatal(err)
	}
	if event != nil {
		t.Fatalf("query emitted response event: %+v", event)
	}
	if !allZero(queryInput) || queryPacket.Payload != nil {
		t.Fatalf("query wire bytes were retained: %x", queryInput)
	}

	responseWire := dnsResponseWire(
		t,
		0x1234,
		"Api.Example.Test.",
		1,
		0,
		[]dnsAnswerFixture{{
			namePointer: true,
			kind:        1,
			ttl:         30,
			value:       net.ParseIP("203.0.113.20").To4(),
		}},
	)
	responseInput := append([]byte(nil), responseWire...)
	responsePacket := dnsPacket(
		boundary,
		actor,
		2,
		at.Add(time.Second),
		99,
		DirectionIngress,
		TransportUDP,
		responseInput,
	)
	event, err = parser.Consume(&responsePacket)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil ||
		event.Query != "api.example.test" ||
		event.QueryType != "A" ||
		!slices.Equal(event.Answers, []string{"203.0.113.20"}) ||
		event.TTL != 30*time.Second ||
		event.ResponseCode != "NOERROR" ||
		event.Resolver != "1.1.1.1:53" ||
		len(event.Limitations) != 0 {
		t.Fatalf("event=%+v", event)
	}
	if !allZero(responseInput) || responsePacket.Payload != nil {
		t.Fatalf("response wire bytes were retained: %x", responseInput)
	}

	correlator, err := networkcollector.NewCorrelator(
		boundary,
		networkcollector.Options{MaxDNSLifetime: time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := correlator.ObserveDNS(*event); err != nil {
		t.Fatal(err)
	}
	connection, err := correlator.NormalizeConnection(
		networkcollector.ConnectionEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           3, At: at.Add(2 * time.Second),
			Actor: actor, Protocol: "tcp",
			DestinationIP: "203.0.113.20", DestinationPort: 443,
			SocketCookie: 100, Route: "direct", Direction: "egress",
			Outcome: workloadtypes.Outcome{
				Status: workloadtypes.OutcomeUnknown,
				Reason: "remote-result-unavailable",
			},
			CoverageID: "cov_network_fixture",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	subject := connection.Subject.(workloadtypes.NetworkSubject)
	if subject.Domain != "api.example.test" ||
		subject.DomainAttribution != workloadtypes.AttributionInferred ||
		subject.CorrelationReason != "unique-dns-answer-same-execution" {
		t.Fatalf("connection subject=%+v", subject)
	}
}

func TestParserSupportsAAAAAndSingleFramedTCPMessage(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	query := frameDNSOverTCP(dnsQueryWire(
		t,
		0x3456,
		"v6.example.test",
		28,
	))
	queryPacket := dnsPacket(
		boundary,
		actor,
		1,
		at,
		200,
		DirectionEgress,
		TransportTCP,
		query,
	)
	if event, err := parser.Consume(&queryPacket); err != nil || event != nil {
		t.Fatalf("query event=%+v error=%v", event, err)
	}
	response := frameDNSOverTCP(dnsResponseWire(
		t,
		0x3456,
		"v6.example.test",
		28,
		0,
		[]dnsAnswerFixture{{
			namePointer: true,
			kind:        28,
			ttl:         60,
			value:       net.ParseIP("2001:db8::20").To16(),
		}},
	))
	responsePacket := dnsPacket(
		boundary,
		actor,
		2,
		at.Add(time.Second),
		200,
		DirectionIngress,
		TransportTCP,
		response,
	)
	event, err := parser.Consume(&responsePacket)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil ||
		event.QueryType != "AAAA" ||
		!slices.Equal(event.Answers, []string{"2001:db8::20"}) ||
		event.TTL != time.Minute {
		t.Fatalf("event=%+v", event)
	}
	if !allZero(query) || !allZero(response) {
		t.Fatal("TCP DNS wire bytes were retained")
	}
}

func TestParserFollowsCNAMEAndDoesNotAssociateUnrelatedAnswer(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	query := dnsPacket(
		boundary,
		actor,
		1,
		at,
		250,
		DirectionEgress,
		TransportUDP,
		dnsQueryWire(t, 0x4000, "api.example.test", 1),
	)
	if _, err := parser.Consume(&query); err != nil {
		t.Fatal(err)
	}
	response := dnsPacket(
		boundary,
		actor,
		2,
		at.Add(time.Second),
		250,
		DirectionIngress,
		TransportUDP,
		dnsResponseWire(
			t,
			0x4000,
			"api.example.test",
			1,
			0,
			[]dnsAnswerFixture{
				{
					namePointer: true,
					kind:        5,
					ttl:         20,
					value: encodeDNSName(
						t,
						"edge.example.test",
					),
				},
				{
					name:  "edge.example.test",
					kind:  1,
					ttl:   30,
					value: net.ParseIP("203.0.113.25").To4(),
				},
				{
					name:  "unrelated.example.test",
					kind:  1,
					ttl:   60,
					value: net.ParseIP("198.51.100.25").To4(),
				},
			},
		),
	)
	event, err := parser.Consume(&response)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil ||
		!slices.Equal(event.Answers, []string{"203.0.113.25"}) ||
		event.TTL != 20*time.Second {
		t.Fatalf("event=%+v", event)
	}
}

func TestParserEmitsNegativeResponseWithoutCreatingCacheEvidence(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	queryPacket := dnsPacket(
		boundary,
		actor,
		1,
		at,
		300,
		DirectionEgress,
		TransportUDP,
		dnsQueryWire(t, 0x4567, "missing.example.test", 1),
	)
	if _, err := parser.Consume(&queryPacket); err != nil {
		t.Fatal(err)
	}
	responsePacket := dnsPacket(
		boundary,
		actor,
		2,
		at.Add(time.Second),
		300,
		DirectionIngress,
		TransportUDP,
		dnsResponseWire(
			t,
			0x4567,
			"missing.example.test",
			1,
			3,
			nil,
		),
	)
	event, err := parser.Consume(&responsePacket)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil ||
		event.ResponseCode != "NXDOMAIN" ||
		len(event.Answers) != 0 ||
		event.TTL != 0 {
		t.Fatalf("event=%+v", event)
	}

	correlator, err := networkcollector.NewCorrelator(
		boundary,
		networkcollector.Options{MaxDNSLifetime: time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := correlator.ObserveDNS(*event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Outcome.Status != workloadtypes.OutcomeFailed ||
		record.Outcome.Reason != "dns-response-error" {
		t.Fatalf("record outcome=%+v", record.Outcome)
	}
	connection, err := correlator.NormalizeConnection(
		networkcollector.ConnectionEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           3, At: at.Add(2 * time.Second),
			Actor: actor, Protocol: "tcp",
			DestinationIP: "203.0.113.99", DestinationPort: 443,
			SocketCookie: 301, Route: "direct", Direction: "egress",
			Outcome: workloadtypes.Outcome{
				Status: workloadtypes.OutcomeUnknown,
				Reason: "remote-result-unavailable",
			},
			CoverageID: "cov_network_fixture",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := connection.Subject.(workloadtypes.NetworkSubject); got.Domain != "" ||
		got.CorrelationReason != "literal-or-uncorrelated-ip" {
		t.Fatalf("negative response created cache evidence: %+v", got)
	}
}

func TestParserBoundsPendingAndLabelsUnobservedResponse(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   2,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for index := range 3 {
		packet := dnsPacket(
			boundary,
			actor,
			uint64(index+1),
			at.Add(time.Duration(index)*time.Millisecond),
			uint64(400+index),
			DirectionEgress,
			TransportUDP,
			dnsQueryWire(
				t,
				uint16(0x5000+index),
				"bounded.example.test",
				1,
			),
		)
		if _, err := parser.Consume(&packet); err != nil {
			t.Fatal(err)
		}
	}
	response := dnsPacket(
		boundary,
		actor,
		4,
		at.Add(time.Second),
		400,
		DirectionIngress,
		TransportUDP,
		dnsResponseWire(
			t,
			0x5000,
			"bounded.example.test",
			1,
			0,
			[]dnsAnswerFixture{{
				namePointer: true,
				kind:        1,
				ttl:         30,
				value:       net.ParseIP("203.0.113.40").To4(),
			}},
		),
	)
	event, err := parser.Consume(&response)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil ||
		!slices.Equal(event.Limitations, []string{"dns-query-unobserved"}) {
		t.Fatalf("event=%+v", event)
	}
	counters := parser.Counters()
	if counters.Queries != 3 ||
		counters.Responses != 1 ||
		counters.Emitted != 1 ||
		counters.PendingEvicted != 1 ||
		counters.UnmatchedResponses != 1 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestParserRejectsMalformedTruncatedOversizedAndMismatchedEvidence(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 512,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	loop := make([]byte, 18)
	binary.BigEndian.PutUint16(loop[0:2], 0x6000)
	binary.BigEndian.PutUint16(loop[2:4], 0x0100)
	binary.BigEndian.PutUint16(loop[4:6], 1)
	loop[12], loop[13] = 0xc0, 0x0c
	binary.BigEndian.PutUint16(loop[14:16], 1)
	binary.BigEndian.PutUint16(loop[16:18], 1)
	loopInput := append([]byte(nil), loop...)
	loopPacket := dnsPacket(
		boundary,
		actor,
		1,
		at,
		500,
		DirectionEgress,
		TransportUDP,
		loopInput,
	)
	if _, err := parser.Consume(&loopPacket); !errors.Is(
		err,
		ErrMalformedMessage,
	) {
		t.Fatalf("compression loop error=%v", err)
	}
	if !allZero(loopInput) {
		t.Fatal("malformed payload was not cleared")
	}

	truncatedInput := dnsQueryWire(t, 0x6001, "truncated.example.test", 1)
	truncatedPacket := dnsPacket(
		boundary,
		actor,
		2,
		at,
		501,
		DirectionEgress,
		TransportUDP,
		truncatedInput,
	)
	truncatedPacket.Truncated = true
	truncatedPacket.WireLength = len(truncatedInput) + 100
	if _, err := parser.Consume(&truncatedPacket); !errors.Is(
		err,
		ErrTruncatedMessage,
	) {
		t.Fatalf("truncated error=%v", err)
	}
	if !allZero(truncatedInput) {
		t.Fatal("truncated payload was not cleared")
	}

	oversizedInput := bytes.Repeat([]byte{0x55}, 513)
	oversizedPacket := dnsPacket(
		boundary,
		actor,
		3,
		at,
		502,
		DirectionEgress,
		TransportUDP,
		oversizedInput,
	)
	if _, err := parser.Consume(&oversizedPacket); !errors.Is(
		err,
		ErrMessageLimit,
	) {
		t.Fatalf("oversized error=%v", err)
	}
	if !allZero(oversizedInput) {
		t.Fatal("oversized payload was not cleared")
	}

	queryPacket := dnsPacket(
		boundary,
		actor,
		4,
		at,
		503,
		DirectionEgress,
		TransportUDP,
		dnsQueryWire(t, 0x6002, "one.example.test", 1),
	)
	if _, err := parser.Consume(&queryPacket); err != nil {
		t.Fatal(err)
	}
	mismatchInput := dnsResponseWire(
		t,
		0x6002,
		"two.example.test",
		1,
		0,
		nil,
	)
	mismatchPacket := dnsPacket(
		boundary,
		actor,
		5,
		at.Add(time.Second),
		503,
		DirectionIngress,
		TransportUDP,
		mismatchInput,
	)
	if _, err := parser.Consume(&mismatchPacket); !errors.Is(
		err,
		ErrQueryMismatch,
	) {
		t.Fatalf("mismatch error=%v", err)
	}
	if !allZero(mismatchInput) {
		t.Fatal("mismatched response payload was not cleared")
	}

	tcpFragment := frameDNSOverTCP(dnsQueryWire(
		t,
		0x6003,
		"tcp.example.test",
		1,
	))
	tcpFragment = tcpFragment[:len(tcpFragment)-1]
	tcpPacket := dnsPacket(
		boundary,
		actor,
		6,
		at,
		504,
		DirectionEgress,
		TransportTCP,
		tcpFragment,
	)
	if _, err := parser.Consume(&tcpPacket); !errors.Is(
		err,
		ErrTruncatedMessage,
	) {
		t.Fatalf("TCP fragment error=%v", err)
	}
	if !allZero(tcpFragment) {
		t.Fatal("TCP fragment was not cleared")
	}
}

func TestParserRejectsCrossBoundaryAndEncryptedMetadataWithoutPayload(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	packet := dnsPacket(
		boundary,
		actor,
		1,
		time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		600,
		DirectionEgress,
		TransportUDP,
		dnsQueryWire(t, 0x7000, "boundary.example.test", 1),
	)
	packet.CgroupID++
	payload := packet.Payload
	if _, err := parser.Consume(&packet); !errors.Is(
		err,
		ErrBoundaryMismatch,
	) {
		t.Fatalf("boundary error=%v", err)
	}
	if !allZero(payload) {
		t.Fatal("cross-boundary payload was not cleared")
	}

	encrypted := dnsPacket(
		boundary,
		actor,
		2,
		time.Date(2026, 7, 29, 12, 0, 1, 0, time.UTC),
		601,
		DirectionEgress,
		TransportTCP,
		nil,
	)
	encrypted.ResolverPort = 853
	encrypted.Encrypted = true
	if _, err := parser.Consume(&encrypted); !errors.Is(
		err,
		ErrEncryptedMetadataUnavailable,
	) {
		t.Fatalf("encrypted error=%v", err)
	}
	if counters := parser.Counters(); counters.Encrypted != 1 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestParserCloseDropsEveryPendingQuery(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewParser(boundary, Options{
		MaxWireBytes: 4096,
		MaxPending:   8,
		QueryTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := dnsQueryWire(t, 0x1234, "pending.example.test", 1)
	packet := dnsPacket(
		boundary,
		actor,
		1,
		time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		99,
		DirectionEgress,
		TransportUDP,
		payload,
	)
	if event, err := parser.Consume(&packet); err != nil || event != nil {
		t.Fatalf("event=%+v error=%v", event, err)
	}
	if len(parser.pending) != 1 {
		t.Fatalf("pending=%d want=1", len(parser.pending))
	}
	parser.Close()
	parser.Close()
	if len(parser.pending) != 0 {
		t.Fatalf("pending after close=%d", len(parser.pending))
	}
}

type dnsAnswerFixture struct {
	namePointer bool
	name        string
	kind        uint16
	ttl         uint32
	value       []byte
}

func dnsQueryWire(
	t *testing.T,
	id uint16,
	name string,
	kind uint16,
) []byte {
	t.Helper()
	result := make([]byte, 12)
	binary.BigEndian.PutUint16(result[0:2], id)
	binary.BigEndian.PutUint16(result[2:4], 0x0100)
	binary.BigEndian.PutUint16(result[4:6], 1)
	result = append(result, encodeDNSName(t, name)...)
	result = appendUint16(result, kind)
	result = appendUint16(result, 1)
	return result
}

func dnsResponseWire(
	t *testing.T,
	id uint16,
	name string,
	queryKind uint16,
	rcode byte,
	answers []dnsAnswerFixture,
) []byte {
	t.Helper()
	result := make([]byte, 12)
	binary.BigEndian.PutUint16(result[0:2], id)
	binary.BigEndian.PutUint16(
		result[2:4],
		0x8180|uint16(rcode&0x0f),
	)
	binary.BigEndian.PutUint16(result[4:6], 1)
	binary.BigEndian.PutUint16(result[6:8], uint16(len(answers)))
	result = append(result, encodeDNSName(t, name)...)
	result = appendUint16(result, queryKind)
	result = appendUint16(result, 1)
	for _, answer := range answers {
		if answer.namePointer {
			result = append(result, 0xc0, 0x0c)
		} else {
			result = append(result, encodeDNSName(t, answer.name)...)
		}
		result = appendUint16(result, answer.kind)
		result = appendUint16(result, 1)
		result = appendUint32(result, answer.ttl)
		result = appendUint16(result, uint16(len(answer.value)))
		result = append(result, answer.value...)
	}
	return result
}

func encodeDNSName(t *testing.T, value string) []byte {
	t.Helper()
	value = string(bytes.TrimSuffix([]byte(value), []byte{"."[0]}))
	if value == "" {
		return []byte{0}
	}
	result := make([]byte, 0, len(value)+2)
	start := 0
	for index := 0; index <= len(value); index++ {
		if index != len(value) && value[index] != '.' {
			continue
		}
		label := value[start:index]
		if len(label) == 0 || len(label) > 63 {
			t.Fatalf("invalid DNS fixture label %q", label)
		}
		result = append(result, byte(len(label)))
		result = append(result, label...)
		start = index + 1
	}
	return append(result, 0)
}

func appendUint16(target []byte, value uint16) []byte {
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], value)
	return append(target, encoded[:]...)
}

func appendUint32(target []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(target, encoded[:]...)
}

func frameDNSOverTCP(message []byte) []byte {
	result := make([]byte, 2, len(message)+2)
	binary.BigEndian.PutUint16(result, uint16(len(message)))
	return append(result, message...)
}

func dnsPacket(
	boundary networkcollector.Boundary,
	actor workloadtypes.Actor,
	sequence uint64,
	at time.Time,
	socketCookie uint64,
	direction Direction,
	transport Transport,
	payload []byte,
) Packet {
	return Packet{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		Sequence:           sequence, At: at,
		Actor: actor, SocketCookie: socketCookie,
		Direction: direction, Transport: transport,
		ResolverIP: "1.1.1.1", ResolverPort: 53,
		WireLength: len(payload), Payload: payload,
		CoverageID: "cov_dns_fixture",
	}
}

func dnsTestBoundary(
	t *testing.T,
) (networkcollector.Boundary, workloadtypes.Actor) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_20260729T120000Z_dns"
	executionID, err := workloadtypes.NewExecutionID(
		workloadtypes.ExecutionIdentityInput{
			Owner: owner, SessionID: sessionID,
			GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
			ObserverGeneration: 1,
			PID:                42,
			ExecSequence:       3,
			StartedAtMonoNS:    900,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return networkcollector.Boundary{
			Owner: owner, SessionID: sessionID,
			CgroupID: 3141, ObserverGeneration: 1,
		}, workloadtypes.Actor{
			ExecutionID: executionID,
			PID:         42,
			UID:         1000,
			GID:         1000,
			User:        "developer",
		}
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}
