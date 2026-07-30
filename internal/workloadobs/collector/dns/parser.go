package dns

import (
	"errors"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	defaultMaxWireBytes = 4096
	defaultMaxPending   = 4096
	defaultQueryTimeout = 30 * time.Second
	defaultMaxTTL       = 24 * time.Hour

	absoluteMaxWireBytes = 65535
	absoluteMaxPending   = 65536
	absoluteMaxTimeout   = 5 * time.Minute
	absoluteMaxTTL       = 7 * 24 * time.Hour
)

var (
	ErrInvalidOptions               = errors.New("DNS parser options are invalid")
	ErrBoundaryMismatch             = errors.New("DNS packet does not match the workload boundary")
	ErrInvalidPacket                = errors.New("DNS packet envelope is invalid")
	ErrMessageLimit                 = errors.New("DNS message exceeds a parser bound")
	ErrTruncatedMessage             = errors.New("DNS message is truncated or fragmented")
	ErrMalformedMessage             = errors.New("DNS message is malformed")
	ErrUnsupportedMessage           = errors.New("DNS message shape is unsupported")
	ErrQueryMismatch                = errors.New("DNS response does not match the observed query")
	ErrEncryptedMetadataUnavailable = errors.New("encrypted DNS metadata is unavailable")
)

type Direction string

const (
	DirectionEgress  Direction = "egress"
	DirectionIngress Direction = "ingress"
)

type Transport string

const (
	TransportUDP Transport = "udp"
	TransportTCP Transport = "tcp"
)

type Options struct {
	MaxWireBytes int
	MaxPending   int
	QueryTimeout time.Duration
	MaxTTL       time.Duration
}

type Packet struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
	Sequence           uint64
	At                 time.Time

	Actor        workloadtypes.Actor
	SocketCookie uint64
	Direction    Direction
	Transport    Transport
	ResolverIP   string
	ResolverPort uint16
	Encrypted    bool

	WireLength int
	Truncated  bool
	Payload    []byte
	CoverageID string
}

type Counters struct {
	Consumed           uint64
	Queries            uint64
	Responses          uint64
	Emitted            uint64
	Malformed          uint64
	Truncated          uint64
	Unsupported        uint64
	Encrypted          uint64
	QueryMismatches    uint64
	UnmatchedResponses uint64
	PendingEvicted     uint64
	PendingExpired     uint64
}

type pendingKey struct {
	socketCookie uint64
	transaction  uint16
	transport    Transport
}

type pendingQuery struct {
	actorExecutionID string
	query            string
	queryType        string
	resolver         string
	observedAt       time.Time
	sequence         uint64
}

type Parser struct {
	mu sync.Mutex

	boundary     networkcollector.Boundary
	maxWireBytes int
	maxPending   int
	queryTimeout time.Duration
	maxTTL       time.Duration
	pending      map[pendingKey]pendingQuery
	counters     Counters
}

func NewParser(
	boundary networkcollector.Boundary,
	options Options,
) (*Parser, error) {
	if err := boundary.Validate(); err != nil {
		return nil, ErrBoundaryMismatch
	}
	if options.MaxWireBytes == 0 {
		options.MaxWireBytes = defaultMaxWireBytes
	}
	if options.MaxPending == 0 {
		options.MaxPending = defaultMaxPending
	}
	if options.QueryTimeout == 0 {
		options.QueryTimeout = defaultQueryTimeout
	}
	if options.MaxTTL == 0 {
		options.MaxTTL = defaultMaxTTL
	}
	if options.MaxWireBytes < 12 ||
		options.MaxWireBytes > absoluteMaxWireBytes ||
		options.MaxPending < 1 ||
		options.MaxPending > absoluteMaxPending ||
		options.QueryTimeout <= 0 ||
		options.QueryTimeout > absoluteMaxTimeout ||
		options.MaxTTL <= 0 ||
		options.MaxTTL > absoluteMaxTTL {
		return nil, ErrInvalidOptions
	}
	return &Parser{
		boundary:     boundary,
		maxWireBytes: options.MaxWireBytes,
		maxPending:   options.MaxPending,
		queryTimeout: options.QueryTimeout,
		maxTTL:       options.MaxTTL,
		pending:      make(map[pendingKey]pendingQuery),
	}, nil
}

// Consume takes ownership of packet.Payload. It clears every byte and releases
// the slice before returning, including on validation and parsing failures.
// Only bounded DNS metadata can leave this method.
func (parser *Parser) Consume(
	packet *Packet,
) (*networkcollector.DNSEvent, error) {
	if packet == nil {
		return nil, ErrInvalidPacket
	}
	payload := packet.Payload
	packet.Payload = nil
	defer clear(payload)
	if parser == nil {
		return nil, ErrInvalidPacket
	}

	parser.mu.Lock()
	defer parser.mu.Unlock()
	increment(&parser.counters.Consumed)

	if !packet.Owner.Equal(parser.boundary.Owner) ||
		packet.SessionID != parser.boundary.SessionID ||
		packet.CgroupID != parser.boundary.CgroupID ||
		packet.ObserverGeneration != parser.boundary.ObserverGeneration {
		return nil, ErrBoundaryMismatch
	}
	resolverIP, resolverOK := canonicalIP(packet.ResolverIP)
	if packet.Sequence == 0 ||
		packet.At.IsZero() ||
		packet.Actor.Validate() != nil ||
		packet.SocketCookie == 0 ||
		(packet.Direction != DirectionEgress &&
			packet.Direction != DirectionIngress) ||
		(packet.Transport != TransportUDP &&
			packet.Transport != TransportTCP) ||
		!resolverOK ||
		packet.ResolverPort == 0 ||
		!validCoverageID(packet.CoverageID) {
		return nil, ErrInvalidPacket
	}
	if packet.Encrypted {
		if len(payload) != 0 ||
			packet.WireLength != 0 ||
			packet.Truncated {
			return nil, ErrInvalidPacket
		}
		increment(&parser.counters.Encrypted)
		return nil, ErrEncryptedMetadataUnavailable
	}
	if packet.ResolverPort != 53 ||
		packet.WireLength <= 0 ||
		packet.WireLength < len(payload) {
		return nil, ErrInvalidPacket
	}
	if packet.Truncated || packet.WireLength != len(payload) {
		increment(&parser.counters.Truncated)
		return nil, ErrTruncatedMessage
	}
	wire, err := parser.messageWire(packet.Transport, payload)
	if err != nil {
		parser.noteParseError(err)
		return nil, err
	}
	message, err := parseWireMessage(wire, parser.maxTTL)
	if err != nil {
		parser.noteParseError(err)
		return nil, err
	}
	if message.response != (packet.Direction == DirectionIngress) {
		increment(&parser.counters.Malformed)
		return nil, ErrMalformedMessage
	}
	resolver := net.JoinHostPort(
		resolverIP,
		strconv.Itoa(int(packet.ResolverPort)),
	)
	key := pendingKey{
		socketCookie: packet.SocketCookie,
		transaction:  message.transaction,
		transport:    packet.Transport,
	}
	parser.pruneExpiredLocked(packet.At.UTC())
	if !message.response {
		increment(&parser.counters.Queries)
		query := pendingQuery{
			actorExecutionID: packet.Actor.ExecutionID,
			query:            message.query,
			queryType:        message.queryType,
			resolver:         resolver,
			observedAt:       packet.At.UTC(),
			sequence:         packet.Sequence,
		}
		if prior, exists := parser.pending[key]; exists {
			if prior.actorExecutionID != query.actorExecutionID ||
				prior.query != query.query ||
				prior.queryType != query.queryType ||
				prior.resolver != query.resolver ||
				packet.At.Before(prior.observedAt) {
				delete(parser.pending, key)
				increment(&parser.counters.QueryMismatches)
				return nil, ErrQueryMismatch
			}
		}
		if _, exists := parser.pending[key]; !exists &&
			len(parser.pending) >= parser.maxPending {
			parser.evictOldestLocked()
		}
		parser.pending[key] = query
		return nil, nil
	}

	increment(&parser.counters.Responses)
	limitations := append([]string(nil), message.limitations...)
	if query, exists := parser.pending[key]; exists {
		delete(parser.pending, key)
		if query.actorExecutionID != packet.Actor.ExecutionID ||
			query.query != message.query ||
			query.queryType != message.queryType ||
			query.resolver != resolver ||
			packet.At.Before(query.observedAt) ||
			packet.At.Sub(query.observedAt) > parser.queryTimeout {
			increment(&parser.counters.QueryMismatches)
			return nil, ErrQueryMismatch
		}
	} else {
		limitations = append(limitations, "dns-query-unobserved")
		increment(&parser.counters.UnmatchedResponses)
	}
	sort.Strings(limitations)
	limitations = compact(limitations)
	event := &networkcollector.DNSEvent{
		Owner: parser.boundary.Owner, SessionID: parser.boundary.SessionID,
		CgroupID:           parser.boundary.CgroupID,
		ObserverGeneration: parser.boundary.ObserverGeneration,
		Sequence:           packet.Sequence,
		At:                 packet.At.UTC(),
		Actor:              packet.Actor,
		Query:              message.query,
		QueryType:          message.queryType,
		Answers:            append([]string(nil), message.answers...),
		TTL:                message.ttl,
		ResponseCode:       message.responseCode,
		Resolver:           resolver,
		CoverageID:         packet.CoverageID,
		Limitations:        limitations,
	}
	increment(&parser.counters.Emitted)
	return event, nil
}

func (parser *Parser) messageWire(
	transport Transport,
	payload []byte,
) ([]byte, error) {
	switch transport {
	case TransportUDP:
		if len(payload) > parser.maxWireBytes {
			return nil, ErrMessageLimit
		}
		return payload, nil
	case TransportTCP:
		if len(payload) < 2 {
			return nil, ErrTruncatedMessage
		}
		declared := int(payload[0])<<8 | int(payload[1])
		if declared < 12 {
			return nil, ErrMalformedMessage
		}
		if declared > parser.maxWireBytes {
			return nil, ErrMessageLimit
		}
		switch {
		case declared+2 > len(payload):
			return nil, ErrTruncatedMessage
		case declared+2 < len(payload):
			return nil, ErrUnsupportedMessage
		default:
			return payload[2:], nil
		}
	default:
		return nil, ErrInvalidPacket
	}
}

func (parser *Parser) noteParseError(err error) {
	switch {
	case errors.Is(err, ErrTruncatedMessage):
		increment(&parser.counters.Truncated)
	case errors.Is(err, ErrUnsupportedMessage):
		increment(&parser.counters.Unsupported)
	default:
		increment(&parser.counters.Malformed)
	}
}

func (parser *Parser) pruneExpiredLocked(now time.Time) {
	for key, value := range parser.pending {
		if now.Before(value.observedAt) ||
			now.Sub(value.observedAt) <= parser.queryTimeout {
			continue
		}
		delete(parser.pending, key)
		increment(&parser.counters.PendingExpired)
	}
}

func (parser *Parser) evictOldestLocked() {
	var selectedKey pendingKey
	var selected pendingQuery
	found := false
	for key, value := range parser.pending {
		if !found ||
			value.observedAt.Before(selected.observedAt) ||
			(value.observedAt.Equal(selected.observedAt) &&
				(value.sequence < selected.sequence ||
					(value.sequence == selected.sequence &&
						lessPendingKey(key, selectedKey)))) {
			selectedKey = key
			selected = value
			found = true
		}
	}
	if found {
		delete(parser.pending, selectedKey)
		increment(&parser.counters.PendingEvicted)
	}
}

func lessPendingKey(left, right pendingKey) bool {
	if left.socketCookie != right.socketCookie {
		return left.socketCookie < right.socketCookie
	}
	if left.transaction != right.transaction {
		return left.transaction < right.transaction
	}
	return left.transport < right.transport
}

func (parser *Parser) Counters() Counters {
	if parser == nil {
		return Counters{}
	}
	parser.mu.Lock()
	defer parser.mu.Unlock()
	return parser.counters
}

func (parser *Parser) Close() {
	if parser == nil {
		return
	}
	parser.mu.Lock()
	clear(parser.pending)
	parser.pending = make(map[pendingKey]pendingQuery)
	parser.mu.Unlock()
}

func increment(target *uint64) {
	if *target != math.MaxUint64 {
		(*target)++
	}
}

func canonicalIP(value string) (string, bool) {
	if value == "" ||
		strings.TrimSpace(value) != value ||
		strings.Contains(value, "%") {
		return "", false
	}
	parsed := net.ParseIP(value)
	if parsed == nil {
		return "", false
	}
	return parsed.String(), true
}

func validCoverageID(value string) bool {
	if !strings.HasPrefix(value, "cov_") ||
		len(value) < len("cov_")+8 ||
		len(value) > 128 {
		return false
	}
	for _, current := range value[len("cov_"):] {
		if current >= 'a' && current <= 'z' ||
			current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' ||
			current == '_' ||
			current == '-' {
			continue
		}
		return false
	}
	return true
}

func compact(values []string) []string {
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
