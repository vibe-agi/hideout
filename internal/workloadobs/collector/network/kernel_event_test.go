package network

import (
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	processcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/process"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestNormalizeKernelConnectionPreservesExactActorEndpointAndRouteEvidence(
	t *testing.T,
) {
	boundary, actor := kernelNetworkBoundary(t)
	raw := kernelNetworkEvent(t, boundary, actor, "203.0.113.50", 443)
	evidence := observerbpf.NetworkSocketEvidence{
		ObserverSequence: raw.ObserverSequence,
		SocketCookie:     raw.SocketCookie,
		IfIndex:          7, EgressPackets: 2, EgressBytes: 256,
	}
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
		PID:                raw.PID, TID: raw.PID,
		ExecSequence: raw.ExecSequence, MonotonicNS: 900, Sequence: 1,
		Executable: "/usr/bin/claude", Argv: []string{"claude"},
		Cwd:      "/workspace",
		Identity: workloadtypes.GuestIdentity{UID: actor.UID, GID: actor.GID},
	}); err != nil {
		t.Fatal(err)
	}
	anchor := ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 1000,
	}
	event, err := NormalizeKernelConnection(
		boundary,
		anchor,
		"cov_network_fixture",
		raw,
		&evidence,
		processNormalizer.LookupActor,
		func(query RouteQuery) (RouteResolution, bool) {
			if query.Protocol != "tcp" ||
				query.IP != "203.0.113.50" ||
				query.Port != 443 ||
				query.IfIndex != 7 ||
				query.SocketCookie != raw.SocketCookie {
				t.Fatalf("route query=%+v", query)
			}
			return RouteResolution{Route: "direct"}, true
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Actor.ExecutionID != actor.ExecutionID ||
		event.Attribution != workloadtypes.AttributionExact ||
		event.DestinationIP != "203.0.113.50" ||
		event.DestinationPort != 443 ||
		event.Route != "direct" ||
		event.Bytes != 256 ||
		event.Outcome.Status != workloadtypes.OutcomeUnknown ||
		event.Outcome.Reason != "remote-result-unavailable" ||
		!slices.Equal(event.Limitations, []string{
			"bytes-snapshot",
			"remote-result-unavailable",
		}) {
		t.Fatalf("event=%+v", event)
	}
	correlator, err := NewCorrelator(boundary, Options{
		MaxDNSLifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := correlator.NormalizeConnection(event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Actor == nil ||
		record.Actor.ExecutionID != actor.ExecutionID ||
		record.Bytes != 256 ||
		record.Attribution != workloadtypes.AttributionExact {
		t.Fatalf("record=%+v", record)
	}
}

func TestNormalizeKernelConnectionKeepsMissingActorAndEgressUnknown(
	t *testing.T,
) {
	boundary, actor := kernelNetworkBoundary(t)
	raw := kernelNetworkEvent(t, boundary, actor, "2001:db8::50", 53)
	raw.Protocol = observerbpf.NetworkProtocolUDP
	raw.Kind = observerbpf.NetworkEventSendmsg
	raw.ExecSequence = 0
	raw.ExecutionPID = 0
	raw.SocketCookie = 0
	raw.Flags = observerbpf.NetworkFlagExecutionUnavailable |
		observerbpf.NetworkFlagCookieUnavailable
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	event, err := NormalizeKernelConnection(
		boundary,
		ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1000,
		},
		"cov_network_fixture",
		raw,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Actor != (workloadtypes.Actor{}) ||
		event.Attribution != workloadtypes.AttributionUnknown ||
		event.Route != "unknown" ||
		event.Bytes != 0 ||
		!slices.Equal(event.Limitations, []string{
			"actor-unresolved",
			"egress-unobserved",
			"remote-result-unavailable",
			"route-unresolved",
			"socket-cookie-unavailable",
		}) {
		t.Fatalf("event=%+v", event)
	}
	correlator, err := NewCorrelator(boundary, Options{
		MaxDNSLifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := correlator.NormalizeConnection(event)
	if err != nil {
		t.Fatal(err)
	}
	subject := record.Subject.(workloadtypes.NetworkSubject)
	if record.Actor != nil ||
		record.Attribution != workloadtypes.AttributionUnknown ||
		subject.DomainAttribution != workloadtypes.AttributionUnknown ||
		subject.CorrelationReason != "actor-unresolved" {
		t.Fatalf("record=%+v subject=%+v", record, subject)
	}
}

func TestNormalizeKernelConnectionUsesEventCredentialsForExactExecution(
	t *testing.T,
) {
	boundary, actor := kernelNetworkBoundary(t)
	actor.User = "developer"
	actor.Group = "staff"
	raw := kernelNetworkEvent(t, boundary, actor, "203.0.113.55", 443)
	raw.UID = 2000
	raw.GID = 3000
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	event, err := NormalizeKernelConnection(
		boundary,
		ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1000,
		},
		"cov_network_fixture",
		raw,
		nil,
		func(pid uint32, execSequence uint64) (workloadtypes.Actor, bool) {
			return actor, pid == actor.PID && execSequence == raw.ExecSequence
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Actor.ExecutionID != actor.ExecutionID ||
		event.Actor.PID != raw.PID ||
		event.Actor.UID != raw.UID ||
		event.Actor.GID != raw.GID ||
		event.Actor.User != "" ||
		event.Actor.Group != "" ||
		event.Attribution != workloadtypes.AttributionExact {
		t.Fatalf("actor=%+v raw=%+v", event.Actor, raw)
	}
}

func TestNormalizeKernelConnectionAttributesUnexecedChildToInheritedExecution(
	t *testing.T,
) {
	boundary, actor := kernelNetworkBoundary(t)
	raw := kernelNetworkEvent(t, boundary, actor, "203.0.113.56", 443)
	raw.PID = 84
	raw.TID = 85
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	event, err := NormalizeKernelConnection(
		boundary,
		ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1000,
		},
		"cov_network_fixture",
		raw,
		nil,
		func(pid uint32, execSequence uint64) (workloadtypes.Actor, bool) {
			if pid != actor.PID || execSequence != raw.ExecSequence {
				t.Fatalf(
					"lookup pid=%d sequence=%d want pid=%d sequence=%d",
					pid,
					execSequence,
					actor.PID,
					raw.ExecSequence,
				)
			}
			return actor, true
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Actor.ExecutionID != actor.ExecutionID ||
		event.Actor.PID != raw.PID ||
		event.Attribution != workloadtypes.AttributionExact {
		t.Fatalf("event=%+v", event)
	}
}

func TestNormalizeKernelConnectionRejectsMismatchedEvidence(t *testing.T) {
	boundary, actor := kernelNetworkBoundary(t)
	raw := kernelNetworkEvent(t, boundary, actor, "203.0.113.60", 443)
	evidence := observerbpf.NetworkSocketEvidence{
		ObserverSequence: raw.ObserverSequence + 1,
		SocketCookie:     raw.SocketCookie,
		IfIndex:          2, EgressPackets: 1, EgressBytes: 64,
	}
	if _, err := NormalizeKernelConnection(
		boundary,
		ClockAnchor{
			WallTime:    time.Now(),
			MonotonicNS: raw.MonotonicNS,
		},
		"cov_network_fixture",
		raw,
		&evidence,
		nil,
		nil,
	); !errors.Is(err, ErrKernelEvent) {
		t.Fatalf("error=%v want=%v", err, ErrKernelEvent)
	}
}

func kernelNetworkBoundary(
	t *testing.T,
) (Boundary, workloadtypes.Actor) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_20260729T120000Z_network_kernel"
	executionID, err := workloadtypes.NewExecutionID(
		workloadtypes.ExecutionIdentityInput{
			Owner: owner, SessionID: sessionID,
			GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
			ObserverGeneration: 1, PID: 42, ExecSequence: 3,
			StartedAtMonoNS: 900,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return Boundary{
			Owner: owner, SessionID: sessionID,
			CgroupID: 3141, ObserverGeneration: 1,
		}, workloadtypes.Actor{
			ExecutionID: executionID,
			PID:         42, UID: 1000, GID: 1000,
		}
}

func kernelNetworkEvent(
	t *testing.T,
	boundary Boundary,
	actor workloadtypes.Actor,
	ip string,
	port uint32,
) observerbpf.RawNetworkEvent {
	t.Helper()
	result := observerbpf.RawNetworkEvent{
		Kind: observerbpf.NetworkEventConnect,
		CPU:  2, PID: actor.PID, TID: actor.PID,
		UID: actor.UID, GID: actor.GID,
		Protocol:         observerbpf.NetworkProtocolTCP,
		DestinationPort:  port,
		CgroupID:         boundary.CgroupID,
		ObserverSequence: 7, ExecSequence: 3,
		ExecutionPID: actor.PID,
		MonotonicNS:  2000, SocketCookie: 99,
	}
	parsed := net.ParseIP(ip)
	if ipv4 := parsed.To4(); ipv4 != nil {
		result.Family = observerbpf.NetworkFamilyIPv4
		copy(result.Address[:], ipv4)
	} else {
		result.Family = observerbpf.NetworkFamilyIPv6
		copy(result.Address[:], parsed.To16())
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	return result
}
