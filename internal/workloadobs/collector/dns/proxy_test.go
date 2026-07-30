package dns

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"

	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestSOCKS5ParserEmitsValidatedDomainAndStopsInspectingTunnelPayload(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewSOCKS5Parser(
		boundary,
		[]ProxyEndpoint{{
			IP: "127.0.0.1", Port: 7890, MediatorID: "local-proxy",
		}},
		SOCKS5Options{
			MaxFlows:         8,
			MaxBufferedBytes: 1024,
			HandshakeTimeout: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sequence := uint64(0)
	clientTCPSequence := uint32(1000)
	serverTCPSequence := uint32(2000)
	consume := func(direction Direction, value []byte) *networkcollector.ProxyTargetEvent {
		t.Helper()
		sequence++
		input := append([]byte(nil), value...)
		tcpSequence := &clientTCPSequence
		if direction == DirectionIngress {
			tcpSequence = &serverTCPSequence
		}
		chunk := proxyChunk(
			boundary,
			actor,
			sequence,
			at.Add(time.Duration(sequence)*time.Millisecond),
			700,
			direction,
			*tcpSequence,
			input,
		)
		*tcpSequence += uint32(len(value))
		event, err := parser.Consume(&chunk)
		if err != nil {
			t.Fatal(err)
		}
		if !allZero(input) || chunk.Payload != nil {
			t.Fatalf("SOCKS bytes were retained: %x", input)
		}
		return event
	}
	if event := consume(DirectionEgress, []byte{0x05, 0x01}); event != nil {
		t.Fatalf("partial greeting emitted event: %+v", event)
	}
	if event := consume(DirectionEgress, []byte{0x00}); event != nil {
		t.Fatalf("greeting emitted event: %+v", event)
	}
	if event := consume(DirectionIngress, []byte{0x05, 0x00}); event != nil {
		t.Fatalf("method selection emitted event: %+v", event)
	}
	request := socksDomainRequest(t, "Proxy.Example.Test", 443)
	if event := consume(DirectionEgress, request[:7]); event != nil {
		t.Fatalf("partial request emitted event: %+v", event)
	}
	event := consume(DirectionEgress, request[7:])
	if event == nil ||
		event.Actor.ExecutionID != actor.ExecutionID ||
		event.SocketCookie != 700 ||
		event.Protocol != "socks5" ||
		event.ParserVersion != "socks5-v1" ||
		!event.Validated ||
		event.Domain != "proxy.example.test" ||
		event.TargetIP != "" ||
		event.DestinationPort != 443 ||
		event.ProxyIP != "127.0.0.1" ||
		event.ProxyPort != 7890 ||
		event.MediatorID != "local-proxy" {
		t.Fatalf("event=%+v", event)
	}

	tunnelInput := []byte(
		"GET / HTTP/1.1\r\nAuthorization: Bearer do-not-inspect\r\n\r\n",
	)
	tunnelChunk := proxyChunk(
		boundary,
		actor,
		sequence+1,
		at.Add(time.Second),
		700,
		DirectionEgress,
		clientTCPSequence,
		tunnelInput,
	)
	if event, err := parser.Consume(&tunnelChunk); err != nil || event != nil {
		t.Fatalf("tunnel event=%+v error=%v", event, err)
	}
	if !allZero(tunnelInput) || tunnelChunk.Payload != nil {
		t.Fatal("tunnel payload was retained")
	}
	counters := parser.Counters()
	if counters.ValidatedTargets != 1 ||
		counters.TunnelChunksIgnored != 1 {
		t.Fatalf("counters=%+v", counters)
	}

	correlator, err := networkcollector.NewCorrelator(
		boundary,
		networkcollector.Options{MaxDNSLifetime: time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := correlator.NormalizeConnection(
		networkcollector.ConnectionEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           sequence + 2,
			At:                 at,
			Actor:              actor,
			Protocol:           "tcp",
			DestinationIP:      "127.0.0.1",
			DestinationPort:    7890,
			SocketCookie:       700,
			Route:              "proxy",
			Direction:          "egress",
			MediatorID:         "local-proxy",
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
	initialSubject := initial.Subject.(workloadtypes.NetworkSubject)
	if initialSubject.Domain != "" ||
		initialSubject.DomainAttribution != workloadtypes.AttributionUnknown ||
		initialSubject.CorrelationReason != "proxy-target-unavailable" {
		t.Fatalf("initial subject=%+v", initialSubject)
	}
	reconciled, ok, err := correlator.ObserveProxyTargetAndReconcile(*event)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || reconciled.ID != initial.ID {
		t.Fatalf(
			"reconciled ok=%v id=%q initial=%q",
			ok,
			reconciled.ID,
			initial.ID,
		)
	}
	record := reconciled
	subject := record.Subject.(workloadtypes.NetworkSubject)
	if subject.Domain != "proxy.example.test" ||
		subject.DomainAttribution != workloadtypes.AttributionExact ||
		subject.CorrelationReason != "validated-proxy-target" ||
		subject.TargetIP != "" ||
		subject.TargetPort != 443 ||
		record.Mediator == nil ||
		record.Mediator.Attribution != workloadtypes.AttributionExact {
		t.Fatalf("subject=%+v mediator=%+v", subject, record.Mediator)
	}
}

func TestSOCKS5ParserClearsUsernamePasswordAndSupportsFragmentedAuth(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewSOCKS5Parser(
		boundary,
		[]ProxyEndpoint{{
			IP: "127.0.0.1", Port: 7890, MediatorID: "local-proxy",
		}},
		SOCKS5Options{
			MaxFlows:         8,
			MaxBufferedBytes: 1024,
			HandshakeTimeout: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sequence := uint64(0)
	clientTCPSequence := uint32(3000)
	serverTCPSequence := uint32(4000)
	consume := func(direction Direction, value []byte) *networkcollector.ProxyTargetEvent {
		t.Helper()
		sequence++
		input := append([]byte(nil), value...)
		tcpSequence := &clientTCPSequence
		if direction == DirectionIngress {
			tcpSequence = &serverTCPSequence
		}
		chunk := proxyChunk(
			boundary,
			actor,
			sequence,
			at.Add(time.Duration(sequence)*time.Millisecond),
			701,
			direction,
			*tcpSequence,
			input,
		)
		*tcpSequence += uint32(len(value))
		event, err := parser.Consume(&chunk)
		if err != nil {
			t.Fatal(err)
		}
		if !allZero(input) {
			t.Fatalf("input not cleared: %x", input)
		}
		return event
	}
	consume(DirectionEgress, []byte{0x05, 0x02, 0x00, 0x02})
	consume(DirectionIngress, []byte{0x05, 0x02})

	username := []byte("fixture-user")
	password := []byte("hideout-fixture-password-canary")
	auth := []byte{0x01, byte(len(username))}
	auth = append(auth, username...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, password...)
	consume(DirectionEgress, auth[:5])
	consume(DirectionEgress, auth[5:])
	if parser.containsFlowBytes(701, username) ||
		parser.containsFlowBytes(701, password) {
		t.Fatal("SOCKS credentials remained in parser flow state")
	}
	consume(DirectionIngress, []byte{0x01, 0x00})
	event := consume(
		DirectionEgress,
		socksDomainRequest(t, "auth.example.test", 8443),
	)
	if event == nil ||
		event.Domain != "auth.example.test" ||
		event.DestinationPort != 8443 {
		t.Fatalf("event=%+v", event)
	}
	if parser.containsFlowBytes(701, username) ||
		parser.containsFlowBytes(701, password) {
		t.Fatal("SOCKS credentials remained after target parsing")
	}
}

func TestSOCKS5ParserEmitsValidatedIPTargetWithoutInventingDomain(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser := newTestSOCKS5Parser(t, boundary)
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	var targetEvent *networkcollector.ProxyTargetEvent
	for index, fixture := range []struct {
		direction   Direction
		tcpSequence uint32
		payload     []byte
	}{
		{DirectionEgress, 5000, []byte{0x05, 0x01, 0x00}},
		{DirectionIngress, 6000, []byte{0x05, 0x00}},
		{DirectionEgress, 5003, socksIPRequest(t, "203.0.113.70", 9443)},
	} {
		chunk := proxyChunk(
			boundary,
			actor,
			uint64(index+1),
			at.Add(time.Duration(index)*time.Millisecond),
			702,
			fixture.direction,
			fixture.tcpSequence,
			fixture.payload,
		)
		event, err := parser.Consume(&chunk)
		if err != nil {
			t.Fatal(err)
		}
		if index < 2 && event != nil {
			t.Fatalf("early event=%+v", event)
		}
		if index == 2 {
			if event == nil ||
				event.Domain != "" ||
				event.TargetIP != "203.0.113.70" ||
				event.DestinationPort != 9443 {
				t.Fatalf("event=%+v", event)
			}
			targetEvent = event
		}
	}
	correlator, err := networkcollector.NewCorrelator(
		boundary,
		networkcollector.Options{MaxDNSLifetime: time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := correlator.ObserveProxyTarget(*targetEvent); err != nil {
		t.Fatal(err)
	}
	record, err := correlator.NormalizeConnection(
		networkcollector.ConnectionEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           4, At: at,
			Actor: actor, Protocol: "tcp",
			DestinationIP: "127.0.0.1", DestinationPort: 7890,
			SocketCookie: 702, Route: "proxy", Direction: "egress",
			MediatorID: "local-proxy",
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
	subject := record.Subject.(workloadtypes.NetworkSubject)
	if subject.Domain != "" ||
		subject.DomainAttribution != workloadtypes.AttributionUnknown ||
		subject.CorrelationReason != "validated-proxy-ip-target" ||
		subject.TargetIP != "203.0.113.70" ||
		subject.TargetPort != 9443 ||
		record.Mediator == nil ||
		record.Mediator.Attribution != workloadtypes.AttributionExact {
		t.Fatalf("subject=%+v mediator=%+v", subject, record.Mediator)
	}
}

func TestSOCKS5ParserRejectsUntrustedMalformedOversizedAndCrossBoundary(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser := newTestSOCKS5Parser(t, boundary)
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	untrustedInput := []byte{0x05, 0x01, 0x00}
	untrusted := proxyChunk(
		boundary,
		actor,
		1,
		at,
		800,
		DirectionEgress,
		100,
		untrustedInput,
	)
	untrusted.RemotePort = 7891
	if _, err := parser.Consume(&untrusted); !errors.Is(
		err,
		ErrUntrustedProxyEndpoint,
	) {
		t.Fatalf("untrusted error=%v", err)
	}
	if !allZero(untrustedInput) {
		t.Fatal("untrusted payload was not cleared")
	}

	malformedInput := []byte{0x04, 0x01, 0x00}
	malformed := proxyChunk(
		boundary,
		actor,
		2,
		at,
		801,
		DirectionEgress,
		100,
		malformedInput,
	)
	if _, err := parser.Consume(&malformed); !errors.Is(
		err,
		ErrMalformedSOCKS5,
	) {
		t.Fatalf("malformed error=%v", err)
	}
	if !allZero(malformedInput) {
		t.Fatal("malformed payload was not cleared")
	}

	oversizedInput := bytes.Repeat([]byte{0x05}, 1025)
	oversized := proxyChunk(
		boundary,
		actor,
		3,
		at,
		802,
		DirectionEgress,
		100,
		oversizedInput,
	)
	if _, err := parser.Consume(&oversized); !errors.Is(
		err,
		ErrSOCKS5Limit,
	) {
		t.Fatalf("oversized error=%v", err)
	}
	if !allZero(oversizedInput) {
		t.Fatal("oversized payload was not cleared")
	}

	crossInput := []byte{0x05, 0x01, 0x00}
	cross := proxyChunk(
		boundary,
		actor,
		4,
		at,
		803,
		DirectionEgress,
		100,
		crossInput,
	)
	cross.ObserverGeneration++
	if _, err := parser.Consume(&cross); !errors.Is(
		err,
		ErrBoundaryMismatch,
	) {
		t.Fatalf("cross-boundary error=%v", err)
	}
	if !allZero(crossInput) {
		t.Fatal("cross-boundary payload was not cleared")
	}

	counters := parser.Counters()
	if counters.UntrustedEndpoints != 1 ||
		counters.Malformed != 1 ||
		counters.BufferLimit != 1 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestSOCKS5ParserRejectsTruncatedOrDiscontinuousTCPStream(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser := newTestSOCKS5Parser(t, boundary)
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	first := proxyChunk(
		boundary,
		actor,
		1,
		at,
		850,
		DirectionEgress,
		100,
		[]byte{0x05},
	)
	if _, err := parser.Consume(&first); err != nil {
		t.Fatal(err)
	}
	gappedPayload := []byte{0x01, 0x00}
	gapped := proxyChunk(
		boundary,
		actor,
		2,
		at.Add(time.Millisecond),
		850,
		DirectionEgress,
		102,
		gappedPayload,
	)
	if _, err := parser.Consume(&gapped); !errors.Is(
		err,
		ErrSOCKS5StreamDiscontinuity,
	) {
		t.Fatalf("gap error=%v", err)
	}
	if !allZero(gappedPayload) {
		t.Fatal("discontinuous payload was not cleared")
	}

	truncatedPayload := []byte{0x05, 0x01, 0x00}
	truncated := proxyChunk(
		boundary,
		actor,
		3,
		at.Add(2*time.Millisecond),
		851,
		DirectionEgress,
		200,
		truncatedPayload,
	)
	truncated.Truncated = true
	truncated.WireLength++
	if _, err := parser.Consume(&truncated); !errors.Is(
		err,
		ErrSOCKS5CaptureTruncated,
	) {
		t.Fatalf("truncated error=%v", err)
	}
	if !allZero(truncatedPayload) {
		t.Fatal("truncated payload was not cleared")
	}

	wrappedFirst := proxyChunk(
		boundary,
		actor,
		4,
		at.Add(3*time.Millisecond),
		852,
		DirectionEgress,
		^uint32(0),
		[]byte{0x05},
	)
	if _, err := parser.Consume(&wrappedFirst); err != nil {
		t.Fatal(err)
	}
	wrappedSecond := proxyChunk(
		boundary,
		actor,
		5,
		at.Add(4*time.Millisecond),
		852,
		DirectionEgress,
		0,
		[]byte{0x01, 0x00},
	)
	if _, err := parser.Consume(&wrappedSecond); err != nil {
		t.Fatalf("wrapped TCP sequence rejected: %v", err)
	}

	counters := parser.Counters()
	if counters.StreamDiscontinuities != 1 ||
		counters.TruncatedChunks != 1 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestSOCKS5ParserBoundsFlowsAndNeverCrossesExecutionIdentity(
	t *testing.T,
) {
	boundary, actor := dnsTestBoundary(t)
	parser, err := NewSOCKS5Parser(
		boundary,
		[]ProxyEndpoint{{
			IP: "127.0.0.1", Port: 7890, MediatorID: "local-proxy",
		}},
		SOCKS5Options{
			MaxFlows:         1,
			MaxBufferedBytes: 1024,
			HandshakeTimeout: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	first := proxyChunk(
		boundary,
		actor,
		1,
		at,
		900,
		DirectionEgress,
		100,
		[]byte{0x05},
	)
	if _, err := parser.Consume(&first); err != nil {
		t.Fatal(err)
	}
	second := proxyChunk(
		boundary,
		actor,
		2,
		at.Add(time.Millisecond),
		901,
		DirectionEgress,
		100,
		[]byte{0x05},
	)
	if _, err := parser.Consume(&second); err != nil {
		t.Fatal(err)
	}
	if counters := parser.Counters(); counters.FlowsEvicted != 1 {
		t.Fatalf("counters=%+v", counters)
	}

	other := actor
	other.ExecutionID = dnsOtherExecutionID(t, boundary, 43, 4)
	crossExecution := proxyChunk(
		boundary,
		other,
		3,
		at.Add(2*time.Millisecond),
		901,
		DirectionEgress,
		101,
		[]byte{0x01, 0x00},
	)
	if _, err := parser.Consume(&crossExecution); !errors.Is(
		err,
		ErrSOCKS5FlowMismatch,
	) {
		t.Fatalf("cross-execution error=%v", err)
	}
}

func TestSOCKS5ParserCloseClearsEveryActiveFlowBuffer(t *testing.T) {
	boundary, actor := dnsTestBoundary(t)
	parser := newTestSOCKS5Parser(t, boundary)
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	canaries := [][]byte{
		[]byte("partial-secret-one"),
		[]byte("partial-secret-two"),
	}
	retained := make([][]byte, 0, len(canaries))
	sequence := uint64(0)
	nextTCPSequence := make(map[[2]uint64]uint32)
	consume := func(socketCookie uint64, direction Direction, payload []byte) {
		t.Helper()
		sequence++
		directionKey := uint64(socks5StreamIndex(direction))
		key := [2]uint64{socketCookie, directionKey}
		tcpSequence, exists := nextTCPSequence[key]
		if !exists {
			tcpSequence = 7000 + uint32(directionKey)*1000
		}
		chunk := proxyChunk(
			boundary,
			actor,
			sequence,
			at.Add(time.Duration(sequence)*time.Millisecond),
			socketCookie,
			direction,
			tcpSequence,
			append([]byte(nil), payload...),
		)
		nextTCPSequence[key] = tcpSequence + uint32(len(payload))
		if _, err := parser.Consume(&chunk); err != nil {
			t.Fatal(err)
		}
	}
	for index, canary := range canaries {
		socketCookie := uint64(1000 + index)
		consume(socketCookie, DirectionEgress, []byte{0x05, 0x01, 0x02})
		consume(socketCookie, DirectionIngress, []byte{0x05, 0x02})
		partialAuth := []byte{0x01, byte(len(canary) + 2)}
		partialAuth = append(partialAuth, canary...)
		consume(
			socketCookie,
			DirectionEgress,
			partialAuth,
		)
		flow := parser.flows[socketCookie]
		if flow == nil ||
			!bytes.Contains(flow.clientBuffer, canary) {
			t.Fatalf("flow=%+v", flow)
		}
		retained = append(retained, flow.clientBuffer)
	}

	parser.Close()
	parser.Close()
	if len(parser.flows) != 0 {
		t.Fatalf("active flows after close=%d", len(parser.flows))
	}
	for index, buffer := range retained {
		if !allZero(buffer) {
			t.Fatalf("flow %d backing buffer was not cleared: %x", index, buffer)
		}
	}
}

func newTestSOCKS5Parser(
	t *testing.T,
	boundary networkcollector.Boundary,
) *SOCKS5Parser {
	t.Helper()
	parser, err := NewSOCKS5Parser(
		boundary,
		[]ProxyEndpoint{{
			IP: "127.0.0.1", Port: 7890, MediatorID: "local-proxy",
		}},
		SOCKS5Options{
			MaxFlows:         8,
			MaxBufferedBytes: 1024,
			HandshakeTimeout: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return parser
}

func proxyChunk(
	boundary networkcollector.Boundary,
	actor workloadtypes.Actor,
	sequence uint64,
	at time.Time,
	socketCookie uint64,
	direction Direction,
	tcpSequence uint32,
	payload []byte,
) ProxyChunk {
	return ProxyChunk{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		Sequence:           sequence, At: at,
		Actor: actor, SocketCookie: socketCookie,
		RemoteIP: "127.0.0.1", RemotePort: 7890,
		Direction: direction, TCPSequence: tcpSequence,
		WireLength: len(payload), Payload: payload,
	}
}

func socksDomainRequest(
	t *testing.T,
	domain string,
	port uint16,
) []byte {
	t.Helper()
	if len(domain) == 0 || len(domain) > 255 || port == 0 {
		t.Fatal("invalid SOCKS domain fixture")
	}
	result := []byte{0x05, 0x01, 0x00, 0x03, byte(len(domain))}
	result = append(result, domain...)
	return append(result, byte(port>>8), byte(port))
}

func socksIPRequest(
	t *testing.T,
	ipText string,
	port uint16,
) []byte {
	t.Helper()
	ip := net.ParseIP(ipText)
	if ip == nil || port == 0 {
		t.Fatal("invalid SOCKS IP fixture")
	}
	result := []byte{0x05, 0x01, 0x00}
	if ipv4 := ip.To4(); ipv4 != nil {
		result = append(result, 0x01)
		result = append(result, ipv4...)
	} else {
		result = append(result, 0x04)
		result = append(result, ip.To16()...)
	}
	return append(result, byte(port>>8), byte(port))
}

func dnsOtherExecutionID(
	t *testing.T,
	boundary networkcollector.Boundary,
	pid uint32,
	sequence uint64,
) string {
	t.Helper()
	result, err := workloadtypes.NewExecutionID(
		workloadtypes.ExecutionIdentityInput{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
			ObserverGeneration: boundary.ObserverGeneration,
			PID:                pid,
			ExecSequence:       sequence,
			StartedAtMonoNS:    1000 + sequence,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
