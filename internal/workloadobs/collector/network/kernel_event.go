package network

import (
	"errors"
	"math"
	"sort"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var ErrKernelEvent = errors.New("kernel network event is invalid")

type ClockAnchor struct {
	WallTime    time.Time
	MonotonicNS uint64
}

type ActorLookup func(
	pid uint32,
	execSequence uint64,
) (workloadtypes.Actor, bool)

type RouteQuery struct {
	Protocol     string
	IP           string
	Port         uint16
	IfIndex      uint32
	SocketCookie uint64
}

type RouteResolution struct {
	Route      string
	MediatorID string
}

type RouteResolver func(RouteQuery) (RouteResolution, bool)

func NormalizeKernelConnection(
	boundary Boundary,
	anchor ClockAnchor,
	coverageID string,
	raw observerbpf.RawNetworkEvent,
	evidence *observerbpf.NetworkSocketEvidence,
	lookupActor ActorLookup,
	resolveRoute RouteResolver,
) (ConnectionEvent, error) {
	if boundary.Validate() != nil ||
		anchor.WallTime.IsZero() ||
		anchor.MonotonicNS == 0 ||
		!validCoverageID(coverageID) ||
		raw.Validate() != nil ||
		raw.CgroupID != boundary.CgroupID ||
		raw.MonotonicNS < anchor.MonotonicNS {
		return ConnectionEvent{}, ErrKernelEvent
	}
	delta := raw.MonotonicNS - anchor.MonotonicNS
	if delta > math.MaxInt64 {
		return ConnectionEvent{}, ErrKernelEvent
	}
	at := anchor.WallTime.UTC().Add(time.Duration(delta))
	if at.IsZero() {
		return ConnectionEvent{}, ErrKernelEvent
	}
	protocol := ""
	switch raw.Protocol {
	case observerbpf.NetworkProtocolTCP:
		protocol = "tcp"
	case observerbpf.NetworkProtocolUDP:
		protocol = "udp"
	default:
		return ConnectionEvent{}, ErrKernelEvent
	}
	ip := raw.DestinationIP().String()
	if _, ok := canonicalIP(ip); !ok {
		return ConnectionEvent{}, ErrKernelEvent
	}

	limitations := []string{"remote-result-unavailable"}
	actor := workloadtypes.Actor{}
	attribution := workloadtypes.AttributionUnknown
	if raw.ExecSequence != 0 && lookupActor != nil {
		resolved, ok := lookupActor(raw.ExecutionPID, raw.ExecSequence)
		if ok {
			if resolved.Validate() != nil ||
				resolved.PID != raw.ExecutionPID {
				return ConnectionEvent{}, ErrKernelEvent
			}
			actor = workloadtypes.Actor{
				ExecutionID: resolved.ExecutionID,
				PID:         raw.PID,
				UID:         raw.UID,
				GID:         raw.GID,
			}
			if resolved.UID == raw.UID {
				actor.User = resolved.User
			}
			if resolved.GID == raw.GID {
				actor.Group = resolved.Group
			}
			if actor.Validate() != nil {
				return ConnectionEvent{}, ErrKernelEvent
			}
			attribution = workloadtypes.AttributionExact
		}
	}
	if attribution == workloadtypes.AttributionUnknown {
		limitations = append(limitations, "actor-unresolved")
	}
	if raw.Flags&observerbpf.NetworkFlagStateUnavailable != 0 {
		limitations = append(limitations, "kernel-state-unavailable")
	}

	bytesObserved := uint64(0)
	route := "unknown"
	mediatorID := ""
	if raw.SocketCookie == 0 {
		if evidence != nil &&
			*evidence != (observerbpf.NetworkSocketEvidence{}) {
			return ConnectionEvent{}, ErrKernelEvent
		}
		limitations = append(
			limitations,
			"egress-unobserved",
			"route-unresolved",
			"socket-cookie-unavailable",
		)
	} else if evidence == nil {
		limitations = append(
			limitations,
			"egress-unobserved",
			"route-unresolved",
			"socket-correlation-unavailable",
		)
	} else {
		if err := evidence.ValidateFor(raw); err != nil {
			return ConnectionEvent{}, errors.Join(ErrKernelEvent, err)
		}
		if evidence.EgressPackets == 0 {
			limitations = append(
				limitations,
				"egress-unobserved",
				"route-unresolved",
			)
		} else {
			bytesObserved = evidence.EgressBytes
			limitations = append(limitations, "bytes-snapshot")
			if resolveRoute == nil {
				limitations = append(limitations, "route-unresolved")
			} else {
				resolution, ok := resolveRoute(RouteQuery{
					Protocol: protocol,
					IP:       ip, Port: uint16(raw.DestinationPort),
					IfIndex:      evidence.IfIndex,
					SocketCookie: raw.SocketCookie,
				})
				if !ok || !validRouteResolution(resolution) {
					limitations = append(limitations, "route-unresolved")
				} else {
					route = resolution.Route
					mediatorID = resolution.MediatorID
				}
			}
		}
	}
	sort.Strings(limitations)
	limitations = compactStrings(limitations)
	return ConnectionEvent{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		Sequence:           raw.ObserverSequence, At: at,
		Actor: actor, Attribution: attribution,
		Protocol: protocol, DestinationIP: ip,
		DestinationPort: uint16(raw.DestinationPort),
		SocketCookie:    raw.SocketCookie,
		Route:           route, Direction: "egress", MediatorID: mediatorID,
		Bytes: bytesObserved,
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeUnknown,
			Reason: "remote-result-unavailable",
		},
		CoverageID: coverageID, Limitations: limitations,
	}, nil
}

func validRouteResolution(value RouteResolution) bool {
	switch value.Route {
	case "direct":
		return value.MediatorID == ""
	case "proxy":
		return boundedPrintable(value.MediatorID, 1, 128)
	default:
		return false
	}
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] == values[write-1] {
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write]
}
