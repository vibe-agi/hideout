package dns

import (
	"bytes"
	"errors"
	"math"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	defaultMaxSOCKS5Flows    = 1024
	defaultSOCKS5BufferBytes = 1024
	defaultSOCKS5Timeout     = 30 * time.Second

	absoluteMaxSOCKS5Flows    = 65536
	absoluteSOCKS5BufferBytes = 4096
	absoluteSOCKS5Timeout     = 5 * time.Minute
	maxProxyEndpoints         = 256
	maxSOCKS5Methods          = 16
)

var (
	ErrInvalidSOCKS5Options      = errors.New("SOCKS5 parser options are invalid")
	ErrInvalidProxyChunk         = errors.New("SOCKS5 chunk envelope is invalid")
	ErrUntrustedProxyEndpoint    = errors.New("SOCKS5 endpoint is not a configured proxy")
	ErrMalformedSOCKS5           = errors.New("SOCKS5 handshake is malformed")
	ErrUnsupportedSOCKS5         = errors.New("SOCKS5 handshake feature is unsupported")
	ErrSOCKS5Limit               = errors.New("SOCKS5 parser bound was exceeded")
	ErrSOCKS5FlowMismatch        = errors.New("SOCKS5 flow identity changed")
	ErrSOCKS5HandshakeExpired    = errors.New("SOCKS5 handshake expired")
	ErrSOCKS5CaptureTruncated    = errors.New("SOCKS5 handshake capture is truncated")
	ErrSOCKS5StreamDiscontinuity = errors.New(
		"SOCKS5 TCP stream is discontinuous",
	)
)

type ProxyEndpoint struct {
	IP         string
	Port       uint16
	MediatorID string
}

type SOCKS5Options struct {
	MaxFlows         int
	MaxBufferedBytes int
	HandshakeTimeout time.Duration
}

type ProxyChunk struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
	Sequence           uint64
	At                 time.Time

	Actor        workloadtypes.Actor
	SocketCookie uint64
	RemoteIP     string
	RemotePort   uint16
	Direction    Direction
	TCPSequence  uint32
	WireLength   int
	Truncated    bool
	Payload      []byte
}

type SOCKS5Counters struct {
	ConsumedChunks        uint64
	FlowsStarted          uint64
	FlowsEvicted          uint64
	FlowsExpired          uint64
	ValidatedTargets      uint64
	TunnelChunksIgnored   uint64
	UntrustedEndpoints    uint64
	Malformed             uint64
	Unsupported           uint64
	BufferLimit           uint64
	FlowMismatches        uint64
	TruncatedChunks       uint64
	StreamDiscontinuities uint64
}

type proxyEndpointKey struct {
	ip   string
	port uint16
}

type socks5Phase uint8

const (
	socks5Greeting socks5Phase = iota
	socks5Method
	socks5Authentication
	socks5AuthenticationResult
	socks5Request
	socks5Done
	socks5Failed
)

type socks5Flow struct {
	endpoint         ProxyEndpoint
	actorExecutionID string
	createdAt        time.Time
	lastAt           time.Time
	lastSequence     uint64
	phase            socks5Phase
	offeredMethods   [256]bool
	clientBuffer     []byte
	serverBuffer     []byte
	tcpInitialized   [2]bool
	nextTCPSequence  [2]uint32
}

type SOCKS5Parser struct {
	mu sync.Mutex

	boundary         networkcollector.Boundary
	endpoints        map[proxyEndpointKey]ProxyEndpoint
	maxFlows         int
	maxBufferedBytes int
	handshakeTimeout time.Duration
	flows            map[uint64]*socks5Flow
	counters         SOCKS5Counters
}

func NewSOCKS5Parser(
	boundary networkcollector.Boundary,
	endpoints []ProxyEndpoint,
	options SOCKS5Options,
) (*SOCKS5Parser, error) {
	if boundary.Validate() != nil {
		return nil, ErrBoundaryMismatch
	}
	if options.MaxFlows == 0 {
		options.MaxFlows = defaultMaxSOCKS5Flows
	}
	if options.MaxBufferedBytes == 0 {
		options.MaxBufferedBytes = defaultSOCKS5BufferBytes
	}
	if options.HandshakeTimeout == 0 {
		options.HandshakeTimeout = defaultSOCKS5Timeout
	}
	if len(endpoints) == 0 ||
		len(endpoints) > maxProxyEndpoints ||
		options.MaxFlows < 1 ||
		options.MaxFlows > absoluteMaxSOCKS5Flows ||
		options.MaxBufferedBytes < 64 ||
		options.MaxBufferedBytes > absoluteSOCKS5BufferBytes ||
		options.HandshakeTimeout <= 0 ||
		options.HandshakeTimeout > absoluteSOCKS5Timeout {
		return nil, ErrInvalidSOCKS5Options
	}
	normalized := make(map[proxyEndpointKey]ProxyEndpoint, len(endpoints))
	for _, endpoint := range endpoints {
		ip, ok := canonicalIP(endpoint.IP)
		if !ok ||
			endpoint.Port == 0 ||
			!validMediatorID(endpoint.MediatorID) {
			return nil, ErrInvalidSOCKS5Options
		}
		endpoint.IP = ip
		key := proxyEndpointKey{ip: ip, port: endpoint.Port}
		if prior, exists := normalized[key]; exists &&
			prior.MediatorID != endpoint.MediatorID {
			return nil, ErrInvalidSOCKS5Options
		}
		normalized[key] = endpoint
	}
	return &SOCKS5Parser{
		boundary:         boundary,
		endpoints:        normalized,
		maxFlows:         options.MaxFlows,
		maxBufferedBytes: options.MaxBufferedBytes,
		handshakeTimeout: options.HandshakeTimeout,
		flows:            make(map[uint64]*socks5Flow),
	}, nil
}

// Consume takes ownership of chunk.Payload and clears it on every return path.
// Once a CONNECT request is validated the flow becomes an opaque tunnel: later
// bytes are cleared without being buffered or inspected.
func (parser *SOCKS5Parser) Consume(
	chunk *ProxyChunk,
) (*networkcollector.ProxyTargetEvent, error) {
	if chunk == nil {
		return nil, ErrInvalidProxyChunk
	}
	payload := chunk.Payload
	chunk.Payload = nil
	defer clear(payload)
	if parser == nil {
		return nil, ErrInvalidProxyChunk
	}

	parser.mu.Lock()
	defer parser.mu.Unlock()
	incrementSOCKS5(&parser.counters.ConsumedChunks)
	if !chunk.Owner.Equal(parser.boundary.Owner) ||
		chunk.SessionID != parser.boundary.SessionID ||
		chunk.CgroupID != parser.boundary.CgroupID ||
		chunk.ObserverGeneration != parser.boundary.ObserverGeneration {
		return nil, ErrBoundaryMismatch
	}
	remoteIP, ok := canonicalIP(chunk.RemoteIP)
	if chunk.Sequence == 0 ||
		chunk.At.IsZero() ||
		chunk.Actor.Validate() != nil ||
		chunk.SocketCookie == 0 ||
		!ok ||
		chunk.RemotePort == 0 ||
		(chunk.Direction != DirectionEgress &&
			chunk.Direction != DirectionIngress) ||
		len(payload) == 0 ||
		chunk.WireLength <= 0 ||
		chunk.WireLength > 65535 ||
		chunk.WireLength < len(payload) ||
		(!chunk.Truncated && chunk.WireLength != len(payload)) ||
		(chunk.Truncated && chunk.WireLength == len(payload)) {
		return nil, ErrInvalidProxyChunk
	}
	endpoint, trusted := parser.endpoints[proxyEndpointKey{
		ip: remoteIP, port: chunk.RemotePort,
	}]
	if !trusted {
		incrementSOCKS5(&parser.counters.UntrustedEndpoints)
		return nil, ErrUntrustedProxyEndpoint
	}

	flow := parser.flows[chunk.SocketCookie]
	if chunk.Truncated {
		if flow != nil {
			parser.failFlow(flow)
		}
		incrementSOCKS5(&parser.counters.TruncatedChunks)
		return nil, ErrSOCKS5CaptureTruncated
	}
	if flow != nil {
		if chunk.At.Before(flow.createdAt) ||
			chunk.Actor.ExecutionID != flow.actorExecutionID ||
			endpoint != flow.endpoint {
			parser.failFlow(flow)
			incrementSOCKS5(&parser.counters.FlowMismatches)
			return nil, ErrSOCKS5FlowMismatch
		}
		if chunk.At.Sub(flow.createdAt) > parser.handshakeTimeout &&
			flow.phase != socks5Done &&
			flow.phase != socks5Failed {
			parser.removeFlow(chunk.SocketCookie)
			incrementSOCKS5(&parser.counters.FlowsExpired)
			return nil, ErrSOCKS5HandshakeExpired
		}
	} else {
		parser.pruneFlowsLocked(chunk.At.UTC(), chunk.SocketCookie)
		if len(parser.flows) >= parser.maxFlows {
			parser.evictOldestFlowLocked()
		}
		flow = &socks5Flow{
			endpoint:         endpoint,
			actorExecutionID: chunk.Actor.ExecutionID,
			createdAt:        chunk.At.UTC(),
			lastAt:           chunk.At.UTC(),
			lastSequence:     chunk.Sequence,
			phase:            socks5Greeting,
		}
		parser.flows[chunk.SocketCookie] = flow
		incrementSOCKS5(&parser.counters.FlowsStarted)
	}
	flow.lastAt = chunk.At.UTC()
	flow.lastSequence = chunk.Sequence
	switch flow.phase {
	case socks5Done:
		incrementSOCKS5(&parser.counters.TunnelChunksIgnored)
		return nil, nil
	case socks5Failed:
		return nil, ErrMalformedSOCKS5
	}

	expected := expectedSOCKS5Direction(flow.phase)
	if chunk.Direction != expected {
		parser.failFlow(flow)
		incrementSOCKS5(&parser.counters.Malformed)
		return nil, ErrMalformedSOCKS5
	}
	streamIndex := socks5StreamIndex(chunk.Direction)
	if flow.tcpInitialized[streamIndex] &&
		chunk.TCPSequence != flow.nextTCPSequence[streamIndex] {
		parser.failFlow(flow)
		incrementSOCKS5(&parser.counters.StreamDiscontinuities)
		return nil, ErrSOCKS5StreamDiscontinuity
	}
	flow.tcpInitialized[streamIndex] = true
	flow.nextTCPSequence[streamIndex] =
		chunk.TCPSequence + uint32(len(payload))
	buffer := &flow.clientBuffer
	if chunk.Direction == DirectionIngress {
		buffer = &flow.serverBuffer
	}
	if len(payload) > parser.maxBufferedBytes-len(*buffer) {
		parser.failFlow(flow)
		incrementSOCKS5(&parser.counters.BufferLimit)
		return nil, ErrSOCKS5Limit
	}
	*buffer = append(*buffer, payload...)

	event, needMore, err := parser.advanceSOCKS5(
		flow,
		chunk,
	)
	if err != nil {
		parser.failFlow(flow)
		switch {
		case errors.Is(err, ErrSOCKS5Limit):
			incrementSOCKS5(&parser.counters.BufferLimit)
		case errors.Is(err, ErrUnsupportedSOCKS5):
			incrementSOCKS5(&parser.counters.Unsupported)
		default:
			incrementSOCKS5(&parser.counters.Malformed)
		}
		return nil, err
	}
	if needMore {
		return nil, nil
	}
	if event != nil {
		incrementSOCKS5(&parser.counters.ValidatedTargets)
	}
	return event, nil
}

func (parser *SOCKS5Parser) advanceSOCKS5(
	flow *socks5Flow,
	chunk *ProxyChunk,
) (*networkcollector.ProxyTargetEvent, bool, error) {
	switch flow.phase {
	case socks5Greeting:
		buffer := flow.clientBuffer
		if len(buffer) < 2 {
			return nil, true, nil
		}
		methodCount := int(buffer[1])
		if buffer[0] != 0x05 || methodCount == 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		if methodCount > maxSOCKS5Methods {
			return nil, false, ErrSOCKS5Limit
		}
		if len(buffer) < methodCount+2 {
			return nil, true, nil
		}
		for _, method := range buffer[2 : methodCount+2] {
			flow.offeredMethods[method] = true
		}
		consumeSOCKS5Prefix(&flow.clientBuffer, methodCount+2)
		if len(flow.clientBuffer) != 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		flow.phase = socks5Method
		return nil, false, nil
	case socks5Method:
		buffer := flow.serverBuffer
		if len(buffer) < 2 {
			return nil, true, nil
		}
		method := buffer[1]
		if buffer[0] != 0x05 ||
			method == 0xff ||
			!flow.offeredMethods[method] {
			return nil, false, ErrMalformedSOCKS5
		}
		consumeSOCKS5Prefix(&flow.serverBuffer, 2)
		if len(flow.serverBuffer) != 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		switch method {
		case 0x00:
			flow.phase = socks5Request
		case 0x02:
			flow.phase = socks5Authentication
		default:
			return nil, false, ErrUnsupportedSOCKS5
		}
		return nil, false, nil
	case socks5Authentication:
		buffer := flow.clientBuffer
		if len(buffer) < 2 {
			return nil, true, nil
		}
		if buffer[0] != 0x01 || buffer[1] == 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		usernameLength := int(buffer[1])
		if len(buffer) < usernameLength+3 {
			return nil, true, nil
		}
		passwordLength := int(buffer[usernameLength+2])
		if passwordLength == 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		total := usernameLength + passwordLength + 3
		if len(buffer) < total {
			return nil, true, nil
		}
		consumeSOCKS5Prefix(&flow.clientBuffer, total)
		if len(flow.clientBuffer) != 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		flow.phase = socks5AuthenticationResult
		return nil, false, nil
	case socks5AuthenticationResult:
		buffer := flow.serverBuffer
		if len(buffer) < 2 {
			return nil, true, nil
		}
		if buffer[0] != 0x01 || buffer[1] != 0x00 {
			return nil, false, ErrMalformedSOCKS5
		}
		consumeSOCKS5Prefix(&flow.serverBuffer, 2)
		if len(flow.serverBuffer) != 0 {
			return nil, false, ErrMalformedSOCKS5
		}
		flow.phase = socks5Request
		return nil, false, nil
	case socks5Request:
		target, consumed, needMore, err := parseSOCKS5Target(
			flow.clientBuffer,
		)
		if err != nil || needMore {
			return nil, needMore, err
		}
		consumeSOCKS5Prefix(&flow.clientBuffer, consumed)
		clear(flow.clientBuffer)
		flow.clientBuffer = nil
		clear(flow.serverBuffer)
		flow.serverBuffer = nil
		flow.phase = socks5Done
		return &networkcollector.ProxyTargetEvent{
			Owner:              parser.boundary.Owner,
			SessionID:          parser.boundary.SessionID,
			CgroupID:           parser.boundary.CgroupID,
			ObserverGeneration: parser.boundary.ObserverGeneration,
			Sequence:           chunk.Sequence, At: chunk.At.UTC(),
			Actor: chunk.Actor, SocketCookie: chunk.SocketCookie,
			Protocol: "socks5", ParserVersion: "socks5-v1",
			Validated: true, Domain: target.domain,
			TargetIP: target.ip, DestinationPort: target.port,
			ProxyIP: flow.endpoint.IP, ProxyPort: flow.endpoint.Port,
			MediatorID: flow.endpoint.MediatorID,
		}, false, nil
	default:
		return nil, false, ErrMalformedSOCKS5
	}
}

type socks5Target struct {
	domain string
	ip     string
	port   uint16
}

func parseSOCKS5Target(
	buffer []byte,
) (socks5Target, int, bool, error) {
	if len(buffer) < 4 {
		return socks5Target{}, 0, true, nil
	}
	if buffer[0] != 0x05 ||
		buffer[1] != 0x01 ||
		buffer[2] != 0x00 {
		return socks5Target{}, 0, false, ErrMalformedSOCKS5
	}
	offset := 4
	target := socks5Target{}
	switch buffer[3] {
	case 0x01:
		if len(buffer) < offset+net.IPv4len+2 {
			return socks5Target{}, 0, true, nil
		}
		target.ip = net.IP(buffer[offset : offset+net.IPv4len]).String()
		offset += net.IPv4len
	case 0x04:
		if len(buffer) < offset+net.IPv6len+2 {
			return socks5Target{}, 0, true, nil
		}
		target.ip = net.IP(buffer[offset : offset+net.IPv6len]).String()
		offset += net.IPv6len
	case 0x03:
		if len(buffer) < offset+1 {
			return socks5Target{}, 0, true, nil
		}
		length := int(buffer[offset])
		offset++
		if length == 0 {
			return socks5Target{}, 0, false, ErrMalformedSOCKS5
		}
		if len(buffer) < offset+length+2 {
			return socks5Target{}, 0, true, nil
		}
		value := string(buffer[offset : offset+length])
		offset += length
		if ip, ok := canonicalIP(value); ok {
			target.ip = ip
		} else {
			var ok bool
			target.domain, ok = canonicalProxyDomain(value)
			if !ok {
				return socks5Target{}, 0, false, ErrMalformedSOCKS5
			}
		}
	default:
		return socks5Target{}, 0, false, ErrUnsupportedSOCKS5
	}
	target.port = uint16(buffer[offset])<<8 |
		uint16(buffer[offset+1])
	offset += 2
	if target.port == 0 ||
		target.ip != "" && net.ParseIP(target.ip).IsUnspecified() {
		return socks5Target{}, 0, false, ErrMalformedSOCKS5
	}
	return target, offset, false, nil
}

func expectedSOCKS5Direction(phase socks5Phase) Direction {
	switch phase {
	case socks5Method, socks5AuthenticationResult:
		return DirectionIngress
	default:
		return DirectionEgress
	}
}

func socks5StreamIndex(direction Direction) int {
	if direction == DirectionIngress {
		return 1
	}
	return 0
}

func consumeSOCKS5Prefix(buffer *[]byte, count int) {
	if count <= 0 || count > len(*buffer) {
		return
	}
	copy(*buffer, (*buffer)[count:])
	clear((*buffer)[len(*buffer)-count:])
	*buffer = (*buffer)[:len(*buffer)-count]
}

func (parser *SOCKS5Parser) failFlow(flow *socks5Flow) {
	if flow == nil {
		return
	}
	clear(flow.clientBuffer)
	clear(flow.serverBuffer)
	flow.clientBuffer = nil
	flow.serverBuffer = nil
	flow.phase = socks5Failed
}

func (parser *SOCKS5Parser) removeFlow(socketCookie uint64) {
	flow := parser.flows[socketCookie]
	parser.failFlow(flow)
	delete(parser.flows, socketCookie)
}

func (parser *SOCKS5Parser) pruneFlowsLocked(
	now time.Time,
	exclude uint64,
) {
	for cookie, flow := range parser.flows {
		if cookie == exclude ||
			now.Before(flow.lastAt) ||
			now.Sub(flow.lastAt) <= parser.handshakeTimeout {
			continue
		}
		parser.removeFlow(cookie)
		incrementSOCKS5(&parser.counters.FlowsExpired)
	}
}

func (parser *SOCKS5Parser) evictOldestFlowLocked() {
	var selectedCookie uint64
	var selected *socks5Flow
	for cookie, flow := range parser.flows {
		if selected == nil ||
			flow.lastAt.Before(selected.lastAt) ||
			(flow.lastAt.Equal(selected.lastAt) &&
				(flow.lastSequence < selected.lastSequence ||
					(flow.lastSequence == selected.lastSequence &&
						cookie < selectedCookie))) {
			selectedCookie = cookie
			selected = flow
		}
	}
	if selected != nil {
		parser.removeFlow(selectedCookie)
		incrementSOCKS5(&parser.counters.FlowsEvicted)
	}
}

func (parser *SOCKS5Parser) CloseFlow(socketCookie uint64) {
	if parser == nil || socketCookie == 0 {
		return
	}
	parser.mu.Lock()
	parser.removeFlow(socketCookie)
	parser.mu.Unlock()
}

// Close clears every buffered handshake byte and removes all flow state. It is
// safe to call more than once.
func (parser *SOCKS5Parser) Close() {
	if parser == nil {
		return
	}
	parser.mu.Lock()
	for socketCookie := range parser.flows {
		parser.removeFlow(socketCookie)
	}
	parser.flows = make(map[uint64]*socks5Flow)
	parser.mu.Unlock()
}

func (parser *SOCKS5Parser) Counters() SOCKS5Counters {
	if parser == nil {
		return SOCKS5Counters{}
	}
	parser.mu.Lock()
	defer parser.mu.Unlock()
	return parser.counters
}

func (parser *SOCKS5Parser) containsFlowBytes(
	socketCookie uint64,
	value []byte,
) bool {
	if parser == nil || len(value) == 0 {
		return false
	}
	parser.mu.Lock()
	defer parser.mu.Unlock()
	flow := parser.flows[socketCookie]
	return flow != nil &&
		(bytes.Contains(flow.clientBuffer, value) ||
			bytes.Contains(flow.serverBuffer, value))
}

func canonicalProxyDomain(value string) (string, bool) {
	if value == "" ||
		len(value) > 253 ||
		!utf8.ValidString(value) ||
		strings.TrimSpace(value) != value ||
		strings.IndexByte(value, 0) >= 0 {
		return "", false
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if value == "" || len(value) > 253 {
		return "", false
	}
	for _, label := range strings.Split(value, ".") {
		if _, ok := canonicalLabel([]byte(label)); !ok {
			return "", false
		}
	}
	return value, true
}

func validMediatorID(value string) bool {
	if len(value) == 0 ||
		len(value) > 128 ||
		!utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func incrementSOCKS5(target *uint64) {
	if *target != math.MaxUint64 {
		(*target)++
	}
}
