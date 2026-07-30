package dns

import (
	"errors"
	"math"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var (
	ErrKernelPacket     = errors.New("kernel DNS packet metadata is invalid")
	ErrExecutionUnknown = errors.New("DNS packet execution identity is unknown")
)

type ClockAnchor struct {
	WallTime    time.Time
	MonotonicNS uint64
}

func PacketFromKernelRecord(
	boundary networkcollector.Boundary,
	anchor ClockAnchor,
	coverageID string,
	raw *observerbpf.RawDNSPacket,
	lookupActor func(
		pid uint32,
		execSequence uint64,
	) (workloadtypes.Actor, bool),
) (Packet, error) {
	if raw == nil {
		return Packet{}, ErrKernelPacket
	}
	defer raw.ClearPayload()
	if boundary.Validate() != nil ||
		anchor.WallTime.IsZero() ||
		anchor.MonotonicNS == 0 ||
		!validCoverageID(coverageID) ||
		raw.Validate() != nil ||
		raw.CgroupID != boundary.CgroupID ||
		raw.MonotonicNS < anchor.MonotonicNS {
		return Packet{}, ErrKernelPacket
	}
	delta := raw.MonotonicNS - anchor.MonotonicNS
	if delta > math.MaxInt64 {
		return Packet{}, ErrKernelPacket
	}
	if raw.Flags&observerbpf.DNSPacketFlagExecutionUnavailable != 0 ||
		raw.ExecutionPID == 0 ||
		raw.ExecSequence == 0 ||
		lookupActor == nil {
		return Packet{}, ErrExecutionUnknown
	}
	resolved, ok := lookupActor(raw.ExecutionPID, raw.ExecSequence)
	if !ok ||
		resolved.Validate() != nil ||
		resolved.PID != raw.ExecutionPID {
		return Packet{}, ErrExecutionUnknown
	}
	actor := workloadtypes.Actor{
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
		return Packet{}, ErrKernelPacket
	}
	direction := DirectionEgress
	if raw.Direction == observerbpf.DNSPacketIngress {
		direction = DirectionIngress
	}
	transport := TransportUDP
	if raw.Protocol == observerbpf.NetworkProtocolTCP {
		transport = TransportTCP
	}
	payload := raw.TakePayload()
	if int(raw.CapturedLength) != len(payload) {
		clear(payload)
		return Packet{}, ErrKernelPacket
	}
	if raw.Flags&observerbpf.DNSPacketFlagEncrypted != 0 {
		clear(payload)
		payload = nil
	}
	return Packet{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		Sequence:           raw.ObserverSequence,
		At: anchor.WallTime.UTC().Add(
			time.Duration(delta),
		),
		Actor:        actor,
		SocketCookie: raw.SocketCookie,
		Direction:    direction,
		Transport:    transport,
		ResolverIP:   raw.ResolverIP().String(),
		ResolverPort: uint16(raw.ResolverPort),
		Encrypted: raw.Flags&
			observerbpf.DNSPacketFlagEncrypted != 0,
		WireLength: int(raw.WireLength),
		Truncated: raw.Flags&
			observerbpf.DNSPacketFlagTruncated != 0,
		Payload:    payload,
		CoverageID: coverageID,
	}, nil
}
