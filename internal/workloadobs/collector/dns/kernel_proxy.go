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
	ErrKernelProxyChunk = errors.New(
		"kernel proxy handshake chunk metadata is invalid",
	)
	ErrProxyExecutionUnknown = errors.New(
		"proxy handshake execution identity is unknown",
	)
)

// ProxyChunkFromKernelRecord transfers ownership of a transient kernel payload
// into a SOCKS5 parser chunk. The raw fixed buffer is cleared on every path.
func ProxyChunkFromKernelRecord(
	boundary networkcollector.Boundary,
	anchor ClockAnchor,
	raw *observerbpf.RawProxyChunk,
	lookupActor func(
		pid uint32,
		execSequence uint64,
	) (workloadtypes.Actor, bool),
) (ProxyChunk, error) {
	if raw == nil {
		return ProxyChunk{}, ErrKernelProxyChunk
	}
	defer raw.ClearPayload()
	if boundary.Validate() != nil ||
		anchor.WallTime.IsZero() ||
		anchor.MonotonicNS == 0 ||
		raw.Validate() != nil ||
		raw.CgroupID != boundary.CgroupID ||
		raw.MonotonicNS < anchor.MonotonicNS {
		return ProxyChunk{}, ErrKernelProxyChunk
	}
	delta := raw.MonotonicNS - anchor.MonotonicNS
	if delta > math.MaxInt64 {
		return ProxyChunk{}, ErrKernelProxyChunk
	}
	if raw.Flags&observerbpf.ProxyChunkFlagExecutionUnavailable != 0 ||
		raw.ExecutionPID == 0 ||
		raw.ExecSequence == 0 ||
		lookupActor == nil {
		return ProxyChunk{}, ErrProxyExecutionUnknown
	}
	resolved, ok := lookupActor(raw.ExecutionPID, raw.ExecSequence)
	if !ok ||
		resolved.Validate() != nil ||
		resolved.PID != raw.ExecutionPID {
		return ProxyChunk{}, ErrProxyExecutionUnknown
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
		return ProxyChunk{}, ErrKernelProxyChunk
	}
	direction := DirectionEgress
	if raw.Direction == observerbpf.ProxyChunkIngress {
		direction = DirectionIngress
	}
	payload := raw.TakePayload()
	if int(raw.CapturedLength) != len(payload) {
		clear(payload)
		return ProxyChunk{}, ErrKernelProxyChunk
	}
	return ProxyChunk{
		Owner:              boundary.Owner,
		SessionID:          boundary.SessionID,
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		Sequence:           raw.ObserverSequence,
		At: anchor.WallTime.UTC().Add(
			time.Duration(delta),
		),
		Actor:        actor,
		SocketCookie: raw.SocketCookie,
		RemoteIP:     raw.ProxyIP().String(),
		RemotePort:   uint16(raw.ProxyPort),
		Direction:    direction,
		TCPSequence:  raw.TCPSequence,
		WireLength:   int(raw.WireLength),
		Truncated: raw.Flags&
			observerbpf.ProxyChunkFlagTruncated != 0,
		Payload: payload,
	}, nil
}
