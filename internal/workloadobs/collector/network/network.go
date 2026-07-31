package network

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	defaultMaxDNSEntries   = 4096
	defaultMaxProxyEntries = 1024
	maxEvidenceEntries     = 65536
	maxDNSLifetime         = 24 * time.Hour
)

var (
	ErrBoundaryMismatch                = errors.New("network event does not match the workload boundary")
	ErrInvalidOptions                  = errors.New("network correlator options are invalid")
	ErrInvalidConnection               = errors.New("network connection event is invalid")
	ErrInvalidDNS                      = errors.New("DNS event is invalid")
	ErrInvalidProxyTarget              = errors.New("proxy target event is invalid")
	ErrEncryptedDNSMetadataUnavailable = errors.New("encrypted DNS metadata is unavailable")
)

type Boundary struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
}

func (boundary Boundary) Validate() error {
	if boundary.Owner.Validate() != nil ||
		!validSessionID(boundary.SessionID) ||
		boundary.CgroupID == 0 ||
		boundary.ObserverGeneration == 0 {
		return ErrBoundaryMismatch
	}
	return nil
}

type Options struct {
	MaxDNSLifetime  time.Duration
	MaxDNSEntries   int
	MaxProxyEntries int
}

type ConnectionEvent struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
	Sequence           uint64
	At                 time.Time

	Actor           workloadtypes.Actor
	Attribution     string
	Protocol        string
	DestinationIP   string
	DestinationPort uint16
	SocketCookie    uint64
	Route           string
	Direction       string
	MediatorID      string
	Bytes           uint64
	Outcome         workloadtypes.Outcome
	CoverageID      string
	Limitations     []string
}

type DNSEvent struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
	Sequence           uint64
	At                 time.Time

	Actor        workloadtypes.Actor
	Query        string
	QueryType    string
	Answers      []string
	TTL          time.Duration
	ResponseCode string
	Resolver     string
	Encrypted    bool
	CoverageID   string
	Limitations  []string
}

type ProxyTargetEvent struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
	Sequence           uint64
	At                 time.Time

	Actor           workloadtypes.Actor
	SocketCookie    uint64
	Protocol        string
	ParserVersion   string
	Validated       bool
	Domain          string
	TargetIP        string
	DestinationPort uint16
	ProxyIP         string
	ProxyPort       uint16
	MediatorID      string
}

type dnsEvidence struct {
	executionID string
	ip          string
	domain      string
	observedAt  time.Time
	expiresAt   time.Time
	sequence    uint64
}

type proxyEvidence struct {
	actor           workloadtypes.Actor
	domain          string
	targetIP        string
	destinationPort uint16
	proxyIP         string
	proxyPort       uint16
	mediatorID      string
	protocol        string
	parserVersion   string
	observedAt      time.Time
	sequence        uint64
}

type CorrelatorCounters struct {
	DNSEvidenceEvicted   uint64
	ProxyEvidenceEvicted uint64
	PendingProxyEvicted  uint64
	StaleProxyTarget     uint64
}

type Correlator struct {
	mu sync.Mutex

	boundary        Boundary
	maxDNSLifetime  time.Duration
	maxDNSEntries   int
	maxProxyEntries int
	dnsEvidence     []dnsEvidence
	proxyEvidence   map[uint64]proxyEvidence
	pendingProxy    map[uint64]ConnectionEvent
	counters        CorrelatorCounters
}

func NewCorrelator(boundary Boundary, options Options) (*Correlator, error) {
	if err := boundary.Validate(); err != nil {
		return nil, err
	}
	if options.MaxDNSLifetime <= 0 ||
		options.MaxDNSLifetime > maxDNSLifetime ||
		options.MaxDNSEntries < 0 ||
		options.MaxDNSEntries > maxEvidenceEntries ||
		options.MaxProxyEntries < 0 ||
		options.MaxProxyEntries > maxEvidenceEntries {
		return nil, ErrInvalidOptions
	}
	if options.MaxDNSEntries == 0 {
		options.MaxDNSEntries = defaultMaxDNSEntries
	}
	if options.MaxProxyEntries == 0 {
		options.MaxProxyEntries = defaultMaxProxyEntries
	}
	return &Correlator{
		boundary:        boundary,
		maxDNSLifetime:  options.MaxDNSLifetime,
		maxDNSEntries:   options.MaxDNSEntries,
		maxProxyEntries: options.MaxProxyEntries,
		proxyEvidence:   make(map[uint64]proxyEvidence),
		pendingProxy:    make(map[uint64]ConnectionEvent),
	}, nil
}

func (correlator *Correlator) NormalizeConnection(
	event ConnectionEvent,
) (workloadtypes.ActivityRecord, error) {
	if correlator == nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	if err := correlator.validateBoundary(
		event.Owner,
		event.SessionID,
		event.CgroupID,
		event.ObserverGeneration,
	); err != nil {
		return workloadtypes.ActivityRecord{}, err
	}
	ip, ok := canonicalIP(event.DestinationIP)
	if !ok ||
		event.Sequence == 0 ||
		event.At.IsZero() ||
		(event.Protocol != "tcp" && event.Protocol != "udp") ||
		event.DestinationPort == 0 ||
		event.Outcome.Validate() != nil ||
		!validCoverageID(event.CoverageID) ||
		!validLimitations(event.Limitations) {
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	attribution := event.Attribution
	if attribution == "" {
		attribution = workloadtypes.AttributionExact
	}
	var actor *workloadtypes.Actor
	switch attribution {
	case workloadtypes.AttributionExact:
		if event.Actor.Validate() != nil {
			return workloadtypes.ActivityRecord{}, ErrInvalidConnection
		}
		value := event.Actor
		actor = &value
	case workloadtypes.AttributionInferred:
		if event.Actor.Validate() != nil ||
			!hasLimitation(event.Limitations, "actor-inferred") {
			return workloadtypes.ActivityRecord{}, ErrInvalidConnection
		}
		value := event.Actor
		actor = &value
	case workloadtypes.AttributionUnknown:
		if event.Actor != (workloadtypes.Actor{}) ||
			!hasLimitation(event.Limitations, "actor-unresolved") {
			return workloadtypes.ActivityRecord{}, ErrInvalidConnection
		}
	default:
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	switch event.Route {
	case "direct", "proxy", "unknown":
	default:
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	switch event.Direction {
	case "egress", "ingress":
	default:
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	if event.Route != "proxy" && event.MediatorID != "" {
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	if event.Route == "proxy" &&
		!boundedPrintable(event.MediatorID, 1, 128) {
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}

	domain := ""
	targetIP := ""
	targetPort := uint16(0)
	domainAttribution := workloadtypes.AttributionUnknown
	var correlationReason string
	var mediator *workloadtypes.Mediator

	correlator.mu.Lock()
	if event.Route == "proxy" {
		evidence, found := correlator.proxyEvidence[event.SocketCookie]
		mediatorAttribution := workloadtypes.AttributionUnknown
		targetResolved := false
		if found &&
			actor != nil &&
			evidence.actor.ExecutionID == actor.ExecutionID &&
			evidence.proxyIP == ip &&
			evidence.proxyPort == event.DestinationPort &&
			evidence.mediatorID == event.MediatorID &&
			proxyEvidenceInWindow(event.At, evidence.observedAt) {
			targetPort = evidence.destinationPort
			targetIP = evidence.targetIP
			mediatorAttribution = workloadtypes.AttributionExact
			targetResolved = true
			if evidence.domain != "" {
				domain = evidence.domain
				domainAttribution = workloadtypes.AttributionExact
				correlationReason = "validated-proxy-target"
			} else {
				correlationReason = "validated-proxy-ip-target"
			}
		} else {
			correlationReason = "proxy-target-unavailable"
		}
		if targetResolved {
			delete(correlator.pendingProxy, event.SocketCookie)
		} else if event.SocketCookie != 0 && actor != nil {
			correlator.rememberPendingProxyLocked(event)
		}
		mediatorValue := workloadtypes.Mediator{
			Kind: "proxy", ID: event.MediatorID,
			Attribution: mediatorAttribution,
			Reason:      correlationReason,
		}
		mediator = &mediatorValue
	} else if actor != nil {
		domain, domainAttribution, correlationReason =
			correlator.correlateDNSLocked(actor.ExecutionID, ip, event.At)
	} else {
		correlationReason = "actor-unresolved"
	}
	correlator.mu.Unlock()

	subject := workloadtypes.NetworkSubject{
		Kind:     workloadtypes.ActivityConnection,
		Protocol: event.Protocol, IP: ip, Port: event.DestinationPort,
		TargetIP: targetIP, TargetPort: targetPort,
		Domain: domain, DomainAttribution: domainAttribution,
		CorrelationReason: correlationReason,
		Route:             event.Route, Direction: event.Direction,
		SocketCookie: event.SocketCookie,
	}
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		Owner:  event.Owner, SessionID: event.SessionID,
		Actor: actor, Mediator: mediator,
		Kind: workloadtypes.ActivityConnection, Operation: "connect",
		Subject: subject, Outcome: event.Outcome,
		Count: 1, Bytes: event.Bytes,
		FirstAt: event.At.UTC(), LastAt: event.At.UTC(),
		FirstSequence: event.Sequence, LastSequence: event.Sequence,
		Attribution:     attribution,
		Truncation:      append([]string(nil), event.Limitations...),
		CoverageID:      event.CoverageID,
		RedactionStatus: workloadtypes.RedactionPending,
	}
	id, err := networkActivityID("connection", event)
	if err != nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidConnection
	}
	record.ID = id
	if err := record.Validate(); err != nil {
		return workloadtypes.ActivityRecord{}, errors.Join(ErrInvalidConnection, err)
	}
	return record, nil
}

func (correlator *Correlator) ObserveDNS(
	event DNSEvent,
) (workloadtypes.ActivityRecord, error) {
	if correlator == nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidDNS
	}
	if err := correlator.validateBoundary(
		event.Owner,
		event.SessionID,
		event.CgroupID,
		event.ObserverGeneration,
	); err != nil {
		return workloadtypes.ActivityRecord{}, err
	}
	if event.Encrypted {
		return workloadtypes.ActivityRecord{}, ErrEncryptedDNSMetadataUnavailable
	}
	query, ok := canonicalDomain(event.Query)
	if !ok ||
		event.Sequence == 0 ||
		event.At.IsZero() ||
		event.Actor.Validate() != nil ||
		!validDNSQueryType(event.QueryType) ||
		event.TTL < 0 ||
		event.TTL%time.Second != 0 ||
		event.TTL > correlator.maxDNSLifetime ||
		!validResponseCode(event.ResponseCode) ||
		!validResolver(event.Resolver) ||
		!validCoverageID(event.CoverageID) ||
		!validLimitations(event.Limitations) ||
		len(event.Answers) > 64 {
		return workloadtypes.ActivityRecord{}, ErrInvalidDNS
	}
	queryType := strings.ToUpper(event.QueryType)
	responseCode := strings.ToUpper(event.ResponseCode)
	if (len(event.Answers) == 0 && event.TTL != 0) ||
		(responseCode != "NOERROR" &&
			(len(event.Answers) != 0 || event.TTL != 0)) {
		return workloadtypes.ActivityRecord{}, ErrInvalidDNS
	}
	answers := make([]string, 0, len(event.Answers))
	seenAnswers := make(map[string]struct{}, len(event.Answers))
	for _, answer := range event.Answers {
		canonical, valid := canonicalIP(answer)
		parsed := net.ParseIP(canonical)
		if !valid ||
			(queryType == "A" && parsed.To4() == nil) ||
			(queryType == "AAAA" &&
				(parsed.To16() == nil || parsed.To4() != nil)) {
			return workloadtypes.ActivityRecord{}, ErrInvalidDNS
		}
		if _, exists := seenAnswers[canonical]; exists {
			continue
		}
		seenAnswers[canonical] = struct{}{}
		answers = append(answers, canonical)
	}
	sort.Strings(answers)
	ttlSeconds := uint32(event.TTL / time.Second)
	subject := workloadtypes.DNSSubject{
		Kind:  workloadtypes.ActivityDNS,
		Query: query, QueryType: queryType,
		Answers: answers, TTLSeconds: ttlSeconds,
		ResponseCode: responseCode,
		Resolver:     event.Resolver,
	}
	outcome := workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded}
	if subject.ResponseCode != "NOERROR" {
		outcome = workloadtypes.Outcome{
			Status: workloadtypes.OutcomeFailed,
			Reason: "dns-response-error",
		}
	}
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		Owner:  event.Owner, SessionID: event.SessionID,
		Actor: &event.Actor,
		Kind:  workloadtypes.ActivityDNS, Operation: "resolve",
		Subject: subject, Outcome: outcome,
		Count:   1,
		FirstAt: event.At.UTC(), LastAt: event.At.UTC(),
		FirstSequence: event.Sequence, LastSequence: event.Sequence,
		Attribution:     workloadtypes.AttributionExact,
		Truncation:      append([]string(nil), event.Limitations...),
		CoverageID:      event.CoverageID,
		RedactionStatus: workloadtypes.RedactionPending,
	}
	id, err := networkActivityID("dns", event)
	if err != nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidDNS
	}
	record.ID = id
	if err := record.Validate(); err != nil {
		return workloadtypes.ActivityRecord{}, errors.Join(ErrInvalidDNS, err)
	}

	if responseCode == "NOERROR" && event.TTL > 0 && len(answers) > 0 {
		expiresAt := event.At.UTC().Add(event.TTL)
		correlator.mu.Lock()
		for _, answer := range answers {
			correlator.dnsEvidence = append(correlator.dnsEvidence, dnsEvidence{
				executionID: event.Actor.ExecutionID,
				ip:          answer, domain: query,
				observedAt: event.At.UTC(), expiresAt: expiresAt,
				sequence: event.Sequence,
			})
		}
		if excess := len(correlator.dnsEvidence) - correlator.maxDNSEntries; excess > 0 {
			copy(correlator.dnsEvidence, correlator.dnsEvidence[excess:])
			clear(correlator.dnsEvidence[len(correlator.dnsEvidence)-excess:])
			correlator.dnsEvidence = correlator.dnsEvidence[:len(correlator.dnsEvidence)-excess]
			addCorrelatorCounter(
				&correlator.counters.DNSEvidenceEvicted,
				uint64(excess),
			)
		}
		correlator.mu.Unlock()
	}
	return record, nil
}

func (correlator *Correlator) ObserveProxyTarget(event ProxyTargetEvent) error {
	return correlator.storeProxyTarget(event)
}

func (correlator *Correlator) ObserveProxyTargetAndReconcile(
	event ProxyTargetEvent,
) (workloadtypes.ActivityRecord, bool, error) {
	if err := correlator.storeProxyTarget(event); err != nil {
		return workloadtypes.ActivityRecord{}, false, err
	}
	correlator.mu.Lock()
	pending, exists := correlator.pendingProxy[event.SocketCookie]
	if exists {
		pending.Limitations = append(
			[]string(nil),
			pending.Limitations...,
		)
	}
	correlator.mu.Unlock()
	if !exists {
		return workloadtypes.ActivityRecord{}, false, nil
	}
	record, err := correlator.NormalizeConnection(pending)
	if err != nil {
		return workloadtypes.ActivityRecord{}, false, err
	}
	subject, ok := record.Subject.(workloadtypes.NetworkSubject)
	if !ok ||
		(subject.DomainAttribution != workloadtypes.AttributionExact &&
			subject.TargetIP == "") {
		return workloadtypes.ActivityRecord{}, false, nil
	}
	return record, true, nil
}

func (correlator *Correlator) storeProxyTarget(event ProxyTargetEvent) error {
	if correlator == nil {
		return ErrInvalidProxyTarget
	}
	if err := correlator.validateBoundary(
		event.Owner,
		event.SessionID,
		event.CgroupID,
		event.ObserverGeneration,
	); err != nil {
		return err
	}
	domain := ""
	targetIP := ""
	if event.Domain != "" {
		var ok bool
		domain, ok = canonicalDomain(event.Domain)
		if !ok {
			return ErrInvalidProxyTarget
		}
	}
	if event.TargetIP != "" {
		var ok bool
		targetIP, ok = canonicalIP(event.TargetIP)
		if !ok {
			return ErrInvalidProxyTarget
		}
	}
	proxyIP, proxyOK := canonicalIP(event.ProxyIP)
	if (domain == "") == (targetIP == "") ||
		!proxyOK ||
		event.Sequence == 0 ||
		event.At.IsZero() ||
		event.Actor.Validate() != nil ||
		event.SocketCookie == 0 ||
		event.Protocol != "socks5" ||
		!validCode(event.ParserVersion) ||
		!event.Validated ||
		event.DestinationPort == 0 ||
		event.ProxyPort == 0 ||
		!boundedPrintable(event.MediatorID, 1, 128) {
		return ErrInvalidProxyTarget
	}
	candidate := proxyEvidence{
		actor: event.Actor, domain: domain, targetIP: targetIP,
		destinationPort: event.DestinationPort,
		proxyIP:         proxyIP,
		proxyPort:       event.ProxyPort,
		mediatorID:      event.MediatorID,
		protocol:        event.Protocol,
		parserVersion:   event.ParserVersion,
		observedAt:      event.At.UTC(),
		sequence:        event.Sequence,
	}
	correlator.mu.Lock()
	if prior, exists := correlator.proxyEvidence[event.SocketCookie]; exists {
		if prior == candidate {
			correlator.mu.Unlock()
			return nil
		}
		if event.At.Before(prior.observedAt) ||
			(event.At.Equal(prior.observedAt) &&
				event.Sequence <= prior.sequence) {
			incrementCorrelatorCounter(
				&correlator.counters.StaleProxyTarget,
			)
			correlator.mu.Unlock()
			return ErrInvalidProxyTarget
		}
	}
	if _, exists := correlator.proxyEvidence[event.SocketCookie]; !exists &&
		len(correlator.proxyEvidence) >= correlator.maxProxyEntries {
		var oldestCookie uint64
		var oldest proxyEvidence
		for cookie, evidence := range correlator.proxyEvidence {
			if oldestCookie == 0 ||
				evidence.observedAt.Before(oldest.observedAt) ||
				(evidence.observedAt.Equal(oldest.observedAt) &&
					evidence.sequence < oldest.sequence) {
				oldestCookie = cookie
				oldest = evidence
			}
		}
		delete(correlator.proxyEvidence, oldestCookie)
		incrementCorrelatorCounter(
			&correlator.counters.ProxyEvidenceEvicted,
		)
	}
	correlator.proxyEvidence[event.SocketCookie] = candidate
	correlator.mu.Unlock()
	return nil
}

func proxyEvidenceInWindow(connectionAt, evidenceAt time.Time) bool {
	connectionAt = connectionAt.UTC()
	evidenceAt = evidenceAt.UTC()
	return !connectionAt.After(evidenceAt) &&
		evidenceAt.Sub(connectionAt) <= 30*time.Second
}

func (correlator *Correlator) rememberPendingProxyLocked(
	event ConnectionEvent,
) {
	if prior, exists := correlator.pendingProxy[event.SocketCookie]; exists {
		if event.At.Before(prior.At) ||
			(event.At.Equal(prior.At) &&
				event.Sequence <= prior.Sequence) {
			return
		}
	} else if len(correlator.pendingProxy) >= correlator.maxProxyEntries {
		var oldestCookie uint64
		var oldest ConnectionEvent
		for cookie, candidate := range correlator.pendingProxy {
			if oldestCookie == 0 ||
				candidate.At.Before(oldest.At) ||
				(candidate.At.Equal(oldest.At) &&
					candidate.Sequence < oldest.Sequence) {
				oldestCookie = cookie
				oldest = candidate
			}
		}
		delete(correlator.pendingProxy, oldestCookie)
		incrementCorrelatorCounter(
			&correlator.counters.PendingProxyEvicted,
		)
	}
	event.Limitations = append([]string(nil), event.Limitations...)
	correlator.pendingProxy[event.SocketCookie] = event
}

func (correlator *Correlator) Counters() CorrelatorCounters {
	if correlator == nil {
		return CorrelatorCounters{}
	}
	correlator.mu.Lock()
	defer correlator.mu.Unlock()
	return correlator.counters
}

func (correlator *Correlator) Close() {
	if correlator == nil {
		return
	}
	correlator.mu.Lock()
	clear(correlator.dnsEvidence)
	correlator.dnsEvidence = nil
	clear(correlator.proxyEvidence)
	correlator.proxyEvidence = make(map[uint64]proxyEvidence)
	clear(correlator.pendingProxy)
	correlator.pendingProxy = make(map[uint64]ConnectionEvent)
	correlator.mu.Unlock()
}

func incrementCorrelatorCounter(target *uint64) {
	addCorrelatorCounter(target, 1)
}

func addCorrelatorCounter(target *uint64, value uint64) {
	if target == nil || value == 0 {
		return
	}
	maximum := ^uint64(0)
	if maximum-*target < value {
		*target = maximum
		return
	}
	*target += value
}

func (correlator *Correlator) correlateDNSLocked(
	executionID, ip string,
	at time.Time,
) (string, string, string) {
	activeDomains := map[string]struct{}{}
	sameExecution := false
	otherExecution := false
	for _, evidence := range correlator.dnsEvidence {
		if evidence.ip != ip || evidence.observedAt.After(at) {
			continue
		}
		if evidence.executionID != executionID {
			otherExecution = true
			continue
		}
		sameExecution = true
		if at.Before(evidence.expiresAt) {
			activeDomains[evidence.domain] = struct{}{}
		}
	}
	switch len(activeDomains) {
	case 1:
		for domain := range activeDomains {
			return domain,
				workloadtypes.AttributionInferred,
				"unique-dns-answer-same-execution"
		}
	case 0:
	default:
		return "", workloadtypes.AttributionUnknown, "shared-ip-ambiguous"
	}
	switch {
	case sameExecution:
		return "", workloadtypes.AttributionUnknown, "dns-evidence-expired"
	case otherExecution:
		return "", workloadtypes.AttributionUnknown, "no-same-execution-dns-evidence"
	default:
		return "", workloadtypes.AttributionUnknown, "literal-or-uncorrelated-ip"
	}
}

func (correlator *Correlator) validateBoundary(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	cgroupID, observerGeneration uint64,
) error {
	if !owner.Equal(correlator.boundary.Owner) ||
		sessionID != correlator.boundary.SessionID ||
		cgroupID != correlator.boundary.CgroupID ||
		observerGeneration != correlator.boundary.ObserverGeneration {
		return ErrBoundaryMismatch
	}
	return nil
}

func networkActivityID(
	kind string,
	event any,
) (string, error) {
	encoded, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Event any    `json:"event"`
	}{
		Kind: kind, Event: event,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(
		[]byte("hideout.network-activity/v1\x00"),
		encoded...,
	))
	return "act_" + base64.RawURLEncoding.EncodeToString(sum[:18]), nil
}

func canonicalIP(value string) (string, bool) {
	if value == "" ||
		strings.Contains(value, "%") ||
		strings.TrimSpace(value) != value {
		return "", false
	}
	parsed := net.ParseIP(value)
	if parsed == nil {
		return "", false
	}
	return parsed.String(), true
}

func canonicalDomain(value string) (string, bool) {
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
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 ||
			label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, current := range label {
			if (current >= 'a' && current <= 'z') ||
				(current >= '0' && current <= '9') ||
				current == '-' {
				continue
			}
			return "", false
		}
	}
	return value, true
}

func validDNSQueryType(value string) bool {
	switch strings.ToUpper(value) {
	case "A", "AAAA":
		return true
	default:
		return false
	}
}

func validResponseCode(value string) bool {
	value = strings.ToUpper(value)
	if value == "" || len(value) > 32 {
		return false
	}
	for _, current := range value {
		if (current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			current == '-' {
			continue
		}
		return false
	}
	return true
}

func validResolver(value string) bool {
	if value == "" {
		return true
	}
	host, portValue, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return false
	}
	if _, ok := canonicalIP(host); !ok {
		if _, ok := canonicalDomain(host); !ok {
			return false
		}
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	return err == nil && port != 0
}

func validLimitations(values []string) bool {
	if len(values) > 32 {
		return false
	}
	previous := ""
	for _, value := range values {
		if !validCode(value) || value <= previous {
			return false
		}
		previous = value
	}
	return true
}

func hasLimitation(values []string, expected string) bool {
	index := sort.SearchStrings(values, expected)
	return index < len(values) && values[index] == expected
}

func validCode(value string) bool {
	if value == "" || len(value) > 128 ||
		value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, current := range value {
		if (current >= 'a' && current <= 'z') ||
			(current >= '0' && current <= '9') ||
			current == '.' || current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}

func validSessionID(value string) bool {
	if !strings.HasPrefix(value, "ses_") ||
		len(value) < 5 || len(value) > 128 {
		return false
	}
	for _, current := range value[4:] {
		if (current >= 'a' && current <= 'z') ||
			(current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}

func validCoverageID(value string) bool {
	if !strings.HasPrefix(value, "cov_") ||
		len(value) < len("cov_")+8 || len(value) > 128 {
		return false
	}
	for _, current := range value[len("cov_"):] {
		if (current >= 'a' && current <= 'z') ||
			(current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}

func boundedPrintable(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum ||
		!utf8.ValidString(value) ||
		strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}
