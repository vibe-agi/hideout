package collector_test

import (
	"errors"
	"net"
	"testing"
	"time"

	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestNetworkCorrelatorNormalizesConnect4Connect6UDPAndTCP(t *testing.T) {
	boundary, actor := networkTestBoundary(t, 42, 1)
	correlator, err := networkcollector.NewCorrelator(boundary, networkcollector.Options{
		MaxDNSLifetime: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		protocol string
		ip       string
		port     uint16
	}{
		{name: "connect4 tcp", protocol: "tcp", ip: "203.0.113.10", port: 443},
		{name: "connect6 tcp", protocol: "tcp", ip: "2001:db8::10", port: 443},
		{name: "sendmsg4 udp", protocol: "udp", ip: "198.51.100.53", port: 53},
		{name: "sendmsg6 udp", protocol: "udp", ip: "2001:db8::53", port: 53},
	}
	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			record, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
				Owner: boundary.Owner, SessionID: boundary.SessionID,
				CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
				Sequence: uint64(index + 1), At: at.Add(time.Duration(index) * time.Millisecond),
				Actor: actor, Protocol: testCase.protocol, DestinationIP: testCase.ip,
				DestinationPort: testCase.port, SocketCookie: uint64(100 + index),
				Route: "direct", Direction: "egress",
				Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
				CoverageID: "cov_network_fixture",
			})
			if err != nil {
				t.Fatal(err)
			}
			subject, ok := record.Subject.(workloadtypes.NetworkSubject)
			if !ok || subject.Protocol != testCase.protocol ||
				!net.ParseIP(subject.IP).Equal(net.ParseIP(testCase.ip)) ||
				subject.Port != testCase.port ||
				subject.DomainAttribution != workloadtypes.AttributionUnknown ||
				subject.CorrelationReason != "literal-or-uncorrelated-ip" {
				t.Fatalf("network subject=%+v", subject)
			}
			if record.Actor == nil || record.Actor.ExecutionID != actor.ExecutionID ||
				record.Operation != "connect" || record.Count != 1 {
				t.Fatalf("network record=%+v", record)
			}
			if err := record.Validate(); err != nil {
				t.Fatalf("invalid network record: %v", err)
			}
		})
	}
}

func TestNetworkCorrelatorUsesTTLBoundSameExecutionDNSInference(t *testing.T) {
	boundary, actor := networkTestBoundary(t, 42, 1)
	otherBoundary, otherActor := networkTestBoundary(t, 43, 2)
	otherBoundary.Owner = boundary.Owner
	otherBoundary.SessionID = boundary.SessionID
	otherBoundary.CgroupID = boundary.CgroupID
	otherBoundary.ObserverGeneration = boundary.ObserverGeneration
	correlator, err := networkcollector.NewCorrelator(boundary, networkcollector.Options{
		MaxDNSLifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	dnsRecord, err := correlator.ObserveDNS(networkcollector.DNSEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 1, At: at, Actor: actor,
		Query: "api.example.test", QueryType: "A",
		Answers: []string{"203.0.113.20"}, TTL: 30 * time.Second,
		ResponseCode: "NOERROR", Resolver: "1.1.1.1:53",
		CoverageID: "cov_dns_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	dnsSubject := dnsRecord.Subject.(workloadtypes.DNSSubject)
	if dnsSubject.Query != "api.example.test" || dnsSubject.TTLSeconds != 30 {
		t.Fatalf("DNS record=%+v", dnsRecord)
	}

	inferred, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 2, At: at.Add(time.Second), Actor: actor,
		Protocol: "tcp", DestinationIP: "203.0.113.20", DestinationPort: 443,
		SocketCookie: 200, Route: "direct", Direction: "egress",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	subject := inferred.Subject.(workloadtypes.NetworkSubject)
	if subject.Domain != "api.example.test" ||
		subject.DomainAttribution != workloadtypes.AttributionInferred ||
		subject.CorrelationReason != "unique-dns-answer-same-execution" {
		t.Fatalf("inferred subject=%+v", subject)
	}

	crossExecution, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 3, At: at.Add(2 * time.Second), Actor: otherActor,
		Protocol: "tcp", DestinationIP: "203.0.113.20", DestinationPort: 443,
		SocketCookie: 201, Route: "direct", Direction: "egress",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	crossSubject := crossExecution.Subject.(workloadtypes.NetworkSubject)
	if crossSubject.Domain != "" ||
		crossSubject.DomainAttribution != workloadtypes.AttributionUnknown ||
		crossSubject.CorrelationReason != "no-same-execution-dns-evidence" {
		t.Fatalf("cross-execution DNS leaked: %+v", crossSubject)
	}

	expired, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 4, At: at.Add(31 * time.Second), Actor: actor,
		Protocol: "tcp", DestinationIP: "203.0.113.20", DestinationPort: 443,
		SocketCookie: 202, Route: "direct", Direction: "egress",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := expired.Subject.(workloadtypes.NetworkSubject).CorrelationReason; got != "dns-evidence-expired" {
		t.Fatalf("expired correlation reason=%q", got)
	}
}

func TestNetworkCorrelatorDoesNotGuessSharedIPCacheLiteralOrEncryptedDNS(t *testing.T) {
	boundary, actor := networkTestBoundary(t, 42, 1)
	correlator, err := networkcollector.NewCorrelator(boundary, networkcollector.Options{
		MaxDNSLifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for index, domain := range []string{"one.example.test", "two.example.test"} {
		if _, err := correlator.ObserveDNS(networkcollector.DNSEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
			Sequence: uint64(index + 1), At: at, Actor: actor,
			Query: domain, QueryType: "A", Answers: []string{"203.0.113.30"},
			TTL: time.Minute, ResponseCode: "NOERROR", Resolver: "1.1.1.1:53",
			CoverageID: "cov_dns_fixture",
		}); err != nil {
			t.Fatal(err)
		}
	}
	shared, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 3, At: at.Add(time.Second), Actor: actor,
		Protocol: "tcp", DestinationIP: "203.0.113.30", DestinationPort: 443,
		SocketCookie: 300, Route: "direct", Direction: "egress",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	subject := shared.Subject.(workloadtypes.NetworkSubject)
	if subject.Domain != "" || subject.DomainAttribution != workloadtypes.AttributionUnknown ||
		subject.CorrelationReason != "shared-ip-ambiguous" {
		t.Fatalf("shared IP was guessed: %+v", subject)
	}

	if _, err := correlator.ObserveDNS(networkcollector.DNSEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 4, At: at, Actor: actor, Encrypted: true,
		Query: "", QueryType: "", CoverageID: "cov_dns_fixture",
	}); !errors.Is(err, networkcollector.ErrEncryptedDNSMetadataUnavailable) {
		t.Fatalf("encrypted DNS error=%v", err)
	}
}

func TestNetworkCorrelatorUsesValidatedProxyTargetAsExactAndRejectsCrossBoundary(t *testing.T) {
	boundary, actor := networkTestBoundary(t, 42, 1)
	correlator, err := networkcollector.NewCorrelator(boundary, networkcollector.Options{
		MaxDNSLifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	if err := correlator.ObserveProxyTarget(networkcollector.ProxyTargetEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 2, At: at.Add(time.Millisecond), Actor: actor, SocketCookie: 444,
		Protocol: "socks5", ParserVersion: "v1", Validated: true,
		Domain: "proxy.example.test", DestinationPort: 443,
		ProxyIP: "127.0.0.1", ProxyPort: 7890,
		MediatorID: "local-proxy",
	}); err != nil {
		t.Fatal(err)
	}
	record, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 1, At: at, Actor: actor,
		Protocol: "tcp", DestinationIP: "127.0.0.1", DestinationPort: 7890,
		SocketCookie: 444, Route: "proxy", Direction: "egress",
		MediatorID: "local-proxy",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	subject := record.Subject.(workloadtypes.NetworkSubject)
	if subject.Domain != "proxy.example.test" ||
		subject.DomainAttribution != workloadtypes.AttributionExact ||
		subject.CorrelationReason != "validated-proxy-target" ||
		record.Mediator == nil || record.Mediator.Kind != "proxy" {
		t.Fatalf("proxy correlation=%+v mediator=%+v", subject, record.Mediator)
	}

	reused, err := correlator.NormalizeConnection(networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 3, At: at.Add(2 * time.Millisecond), Actor: actor,
		Protocol: "tcp", DestinationIP: "127.0.0.1", DestinationPort: 7890,
		SocketCookie: 444, Route: "proxy", Direction: "egress",
		MediatorID: "local-proxy",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	reusedSubject := reused.Subject.(workloadtypes.NetworkSubject)
	if reusedSubject.Domain != "" ||
		reusedSubject.TargetPort != 0 ||
		reusedSubject.CorrelationReason != "proxy-target-unavailable" {
		t.Fatalf(
			"reused socket cookie inherited stale target: %+v",
			reusedSubject,
		)
	}

	cross := networkcollector.ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID + 1, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 3, At: at, Actor: actor, Protocol: "tcp",
		DestinationIP: "203.0.113.40", DestinationPort: 443,
		Route: "direct", Direction: "egress",
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_network_fixture",
	}
	if _, err := correlator.NormalizeConnection(cross); !errors.Is(err, networkcollector.ErrBoundaryMismatch) {
		t.Fatalf("cross-boundary error=%v want %v", err, networkcollector.ErrBoundaryMismatch)
	}
}

func TestNetworkCorrelatorReportsBoundedEvidenceEvictions(t *testing.T) {
	boundary, actor := networkTestBoundary(t, 42, 1)
	correlator, err := networkcollector.NewCorrelator(
		boundary,
		networkcollector.Options{
			MaxDNSLifetime:  time.Minute,
			MaxDNSEntries:   1,
			MaxProxyEntries: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for index, answer := range []string{"203.0.113.10", "203.0.113.11"} {
		if _, err := correlator.ObserveDNS(networkcollector.DNSEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           uint64(index + 1), At: at.Add(time.Duration(index) * time.Millisecond),
			Actor: actor, Query: "bounded.example.test", QueryType: "A",
			Answers: []string{answer}, TTL: time.Minute,
			ResponseCode: "NOERROR", Resolver: "1.1.1.1:53",
			CoverageID: "cov_dns_fixture",
		}); err != nil {
			t.Fatal(err)
		}
	}
	proxyTarget := func(
		cookie uint64,
		sequence uint64,
		observedAt time.Time,
		domain string,
	) networkcollector.ProxyTargetEvent {
		return networkcollector.ProxyTargetEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           sequence, At: observedAt, Actor: actor,
			SocketCookie: cookie, Protocol: "socks5",
			ParserVersion: "socks5-v1", Validated: true,
			Domain: domain, DestinationPort: 443,
			ProxyIP: "127.0.0.1", ProxyPort: 7890,
			MediatorID: "local-proxy",
		}
	}
	if err := correlator.ObserveProxyTarget(proxyTarget(
		10,
		10,
		at.Add(10*time.Millisecond),
		"one.proxy.test",
	)); err != nil {
		t.Fatal(err)
	}
	if err := correlator.ObserveProxyTarget(proxyTarget(
		11,
		11,
		at.Add(11*time.Millisecond),
		"two.proxy.test",
	)); err != nil {
		t.Fatal(err)
	}
	if err := correlator.ObserveProxyTarget(proxyTarget(
		11,
		9,
		at.Add(9*time.Millisecond),
		"stale.proxy.test",
	)); !errors.Is(err, networkcollector.ErrInvalidProxyTarget) {
		t.Fatalf("stale target error=%v", err)
	}
	for index, cookie := range []uint64{20, 21} {
		if _, err := correlator.NormalizeConnection(
			networkcollector.ConnectionEvent{
				Owner: boundary.Owner, SessionID: boundary.SessionID,
				CgroupID:           boundary.CgroupID,
				ObserverGeneration: boundary.ObserverGeneration,
				Sequence:           uint64(20 + index),
				At:                 at.Add(time.Duration(20+index) * time.Millisecond),
				Actor:              actor, Protocol: "tcp",
				DestinationIP: "127.0.0.1", DestinationPort: 7890,
				SocketCookie: cookie, Route: "proxy", Direction: "egress",
				MediatorID: "local-proxy",
				Outcome: workloadtypes.Outcome{
					Status: workloadtypes.OutcomeSucceeded,
				},
				CoverageID: "cov_network_fixture",
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	counters := correlator.Counters()
	if counters.DNSEvidenceEvicted != 1 ||
		counters.ProxyEvidenceEvicted != 1 ||
		counters.PendingProxyEvicted != 1 ||
		counters.StaleProxyTarget != 1 {
		t.Fatalf("counters=%+v", counters)
	}

	correlator.Close()
	record, err := correlator.NormalizeConnection(
		networkcollector.ConnectionEvent{
			Owner: boundary.Owner, SessionID: boundary.SessionID,
			CgroupID:           boundary.CgroupID,
			ObserverGeneration: boundary.ObserverGeneration,
			Sequence:           30, At: at.Add(30 * time.Millisecond),
			Actor: actor, Protocol: "tcp",
			DestinationIP: "203.0.113.11", DestinationPort: 443,
			SocketCookie: 30, Route: "direct", Direction: "egress",
			Outcome: workloadtypes.Outcome{
				Status: workloadtypes.OutcomeSucceeded,
			},
			CoverageID: "cov_network_fixture",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if subject := record.Subject.(workloadtypes.NetworkSubject); subject.Domain != "" {
		t.Fatalf("closed correlator retained DNS evidence: %+v", subject)
	}
}

func networkTestBoundary(
	t *testing.T,
	pid uint32,
	execSequence uint64,
) (networkcollector.Boundary, workloadtypes.Actor) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_20260729T120000Z_network"
	executionID, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: pid, ExecSequence: execSequence,
		StartedAtMonoNS: 1000 + execSequence,
	})
	if err != nil {
		t.Fatal(err)
	}
	return networkcollector.Boundary{
			Owner: owner, SessionID: sessionID, CgroupID: 3141, ObserverGeneration: 1,
		}, workloadtypes.Actor{
			ExecutionID: executionID, PID: pid, UID: 1000, GID: 1000, User: "developer",
		}
}
