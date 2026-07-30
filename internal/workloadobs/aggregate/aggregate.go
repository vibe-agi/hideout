// Package aggregate combines repetitive, already-normalized activity without
// crossing an owner, session, execution, semantic subject, outcome, or
// coverage interval.
//
// A window is inclusive and anchored at the first observation in a group:
// records merge only while the complete first-to-last span is no greater than
// the configured window. Inputs are sorted before grouping, so late arrival
// cannot turn a rolling window into an unbounded chain or change the result.
// Process lifecycle records and destructive file records are never collapsed.
package aggregate

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
	"time"

	workloadobs "github.com/vibe-agi/hideout/internal/workloadobs"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	DefaultWindow    = workloadobs.DefaultActivityAggregationWindow
	MinimumWindow    = time.Millisecond
	MaximumWindow    = 10 * time.Minute
	DefaultMaxInputs = workloadobs.DefaultActivityAggregationInputs
	MaximumInputs    = 1 << 20
)

var (
	ErrInvalidOptions = errors.New("activity aggregation options are invalid")
	ErrInvalidRecord  = errors.New("activity aggregation record is invalid")
	ErrDuplicateID    = errors.New("activity aggregation id is bound to different evidence")
	ErrCapacity       = errors.New("activity aggregation input capacity reached")
)

type Options struct {
	Window    time.Duration
	MaxInputs int
}

type Aggregator struct {
	mu sync.RWMutex

	window    time.Duration
	maxInputs int
	records   map[string]storedRecord
}

type storedRecord struct {
	record   workloadtypes.ActivityRecord
	digest   string
	identity string
}

func New(options Options) (*Aggregator, error) {
	if options.Window == 0 {
		options.Window = DefaultWindow
	}
	if options.MaxInputs == 0 {
		options.MaxInputs = DefaultMaxInputs
	}
	if options.Window < MinimumWindow || options.Window > MaximumWindow ||
		options.MaxInputs < 1 || options.MaxInputs > MaximumInputs {
		return nil, ErrInvalidOptions
	}
	return &Aggregator{
		window: options.Window, maxInputs: options.MaxInputs,
		records: make(map[string]storedRecord),
	}, nil
}

// Add takes an ownership-safe copy of record. Repeating the exact same ID and
// evidence is idempotent; rebinding an ID to different evidence fails closed.
func (aggregator *Aggregator) Add(record workloadtypes.ActivityRecord) error {
	if aggregator == nil {
		return ErrInvalidOptions
	}
	normalized := cloneActivityRecord(record)
	if err := normalized.Validate(); err != nil {
		return errors.Join(ErrInvalidRecord, err)
	}
	if err := validateAggregationBinding(normalized); err != nil {
		return errors.Join(ErrInvalidRecord, err)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return errors.Join(ErrInvalidRecord, err)
	}
	identity, err := aggregationIdentity(normalized)
	if err != nil {
		return errors.Join(ErrInvalidRecord, err)
	}
	stored := storedRecord{
		record: normalized, digest: string(encoded), identity: identity,
	}

	aggregator.mu.Lock()
	defer aggregator.mu.Unlock()
	if existing, exists := aggregator.records[normalized.ID]; exists {
		if existing.digest == stored.digest {
			return nil
		}
		return ErrDuplicateID
	}
	if len(aggregator.records) >= aggregator.maxInputs {
		return ErrCapacity
	}
	aggregator.records[normalized.ID] = stored
	return nil
}

// Records returns a deterministic snapshot and never aliases aggregator state.
func (aggregator *Aggregator) Records() []workloadtypes.ActivityRecord {
	if aggregator == nil {
		return []workloadtypes.ActivityRecord{}
	}
	aggregator.mu.RLock()
	window := aggregator.window
	values := make([]storedRecord, 0, len(aggregator.records))
	for _, value := range aggregator.records {
		values = append(values, storedRecord{
			record: cloneActivityRecord(value.record), identity: value.identity,
		})
	}
	aggregator.mu.RUnlock()

	groups := make(map[string][]workloadtypes.ActivityRecord, len(values))
	for _, value := range values {
		groups[value.identity] = append(groups[value.identity], value.record)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]workloadtypes.ActivityRecord, 0, len(values))
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(left, right int) bool {
			return lessActivityRecord(group[left], group[right])
		})
		if len(group) == 0 {
			continue
		}
		current := cloneActivityRecord(group[0])
		for _, candidate := range group[1:] {
			if canMerge(current, candidate, window) {
				current = merge(current, candidate)
				continue
			}
			result = append(result, current)
			current = cloneActivityRecord(candidate)
		}
		result = append(result, current)
	}
	sort.Slice(result, func(left, right int) bool {
		return lessActivityRecord(result[left], result[right])
	})
	for index := range result {
		result[index] = cloneActivityRecord(result[index])
	}
	return result
}

func aggregationIdentity(record workloadtypes.ActivityRecord) (string, error) {
	identity := cloneActivityRecord(record)
	identity.ID = ""
	identity.Count = 0
	identity.Bytes = 0
	identity.FirstAt = time.Time{}
	identity.LastAt = time.Time{}
	identity.FirstSequence = 0
	identity.LastSequence = 0
	if subject, ok := identity.Subject.(workloadtypes.NetworkSubject); ok {
		subject.SocketCookie = 0
		identity.Subject = subject
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	if !aggregatable(record) {
		encoded = append(encoded, 0)
		encoded = append(encoded, record.ID...)
	}
	return string(encoded), nil
}

func aggregatable(record workloadtypes.ActivityRecord) bool {
	switch record.Kind {
	case workloadtypes.ActivityProcess,
		workloadtypes.ActivityRisk,
		workloadtypes.ActivityCoverage:
		return false
	case workloadtypes.ActivityFile:
		subject, ok := record.Subject.(workloadtypes.FileSubject)
		if !ok || subject.Destructive {
			return false
		}
		switch record.Operation {
		case "rename", "unlink", "truncate", "delete", "remove", "rmdir":
			return false
		}
		return true
	case workloadtypes.ActivityConnection, workloadtypes.ActivityDNS:
		return true
	default:
		return false
	}
}

func validateAggregationBinding(record workloadtypes.ActivityRecord) error {
	if record.Owner.Kind == workloadtypes.OwnerDisposableSession &&
		record.Owner.SessionID != record.SessionID {
		return workloadtypes.ErrOwnerMismatch
	}
	if record.Kind != workloadtypes.ActivityProcess {
		return nil
	}
	subject, ok := record.Subject.(workloadtypes.ProcessSubject)
	if !ok || record.Actor == nil ||
		subject.ExecutionID != record.Actor.ExecutionID {
		return workloadtypes.ErrInvalidActivity
	}
	return nil
}

func canMerge(
	left, right workloadtypes.ActivityRecord,
	window time.Duration,
) bool {
	earliest := left.FirstAt
	if right.FirstAt.Before(earliest) {
		earliest = right.FirstAt
	}
	latest := left.LastAt
	if right.LastAt.After(latest) {
		latest = right.LastAt
	}
	if latest.Sub(earliest) > window {
		return false
	}
	disclosures := append([]string(nil), left.Truncation...)
	if sumWouldOverflow(left.Count, right.Count) {
		disclosures = appendUnique(disclosures, "count-overflow")
	}
	if sumWouldOverflow(left.Bytes, right.Bytes) {
		disclosures = appendUnique(disclosures, "bytes-overflow")
	}
	leftNetwork, leftOK := left.Subject.(workloadtypes.NetworkSubject)
	rightNetwork, rightOK := right.Subject.(workloadtypes.NetworkSubject)
	if leftOK && rightOK && leftNetwork.SocketCookie != rightNetwork.SocketCookie {
		disclosures = appendUnique(disclosures, "socket-cookie-aggregated")
	}
	return len(disclosures) <= 32
}

func merge(
	left, right workloadtypes.ActivityRecord,
) workloadtypes.ActivityRecord {
	result := cloneActivityRecord(left)
	if right.ID < result.ID {
		result.ID = right.ID
	}
	result.Count, result.Truncation = addWithDisclosure(
		result.Count,
		right.Count,
		"count-overflow",
		result.Truncation,
	)
	result.Bytes, result.Truncation = addWithDisclosure(
		result.Bytes,
		right.Bytes,
		"bytes-overflow",
		result.Truncation,
	)
	if right.FirstAt.Before(result.FirstAt) {
		result.FirstAt = right.FirstAt
	}
	if right.LastAt.After(result.LastAt) {
		result.LastAt = right.LastAt
	}
	if right.FirstSequence < result.FirstSequence {
		result.FirstSequence = right.FirstSequence
	}
	if right.LastSequence > result.LastSequence {
		result.LastSequence = right.LastSequence
	}
	leftNetwork, leftOK := result.Subject.(workloadtypes.NetworkSubject)
	rightNetwork, rightOK := right.Subject.(workloadtypes.NetworkSubject)
	if leftOK && rightOK && leftNetwork.SocketCookie != rightNetwork.SocketCookie {
		leftNetwork.SocketCookie = 0
		result.Subject = leftNetwork
		result.Truncation = appendUnique(
			result.Truncation,
			"socket-cookie-aggregated",
		)
	}
	sort.Strings(result.Truncation)
	return result
}

func addWithDisclosure(
	left, right uint64,
	code string,
	disclosures []string,
) (uint64, []string) {
	if sumWouldOverflow(left, right) {
		return math.MaxUint64, appendUnique(disclosures, code)
	}
	return left + right, disclosures
}

func sumWouldOverflow(left, right uint64) bool {
	return right > math.MaxUint64-left
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func lessActivityRecord(left, right workloadtypes.ActivityRecord) bool {
	switch {
	case left.FirstAt.Before(right.FirstAt):
		return true
	case right.FirstAt.Before(left.FirstAt):
		return false
	case left.LastAt.Before(right.LastAt):
		return true
	case right.LastAt.Before(left.LastAt):
		return false
	case left.FirstSequence != right.FirstSequence:
		return left.FirstSequence < right.FirstSequence
	case left.LastSequence != right.LastSequence:
		return left.LastSequence < right.LastSequence
	case left.Owner.String() != right.Owner.String():
		return left.Owner.String() < right.Owner.String()
	case left.SessionID != right.SessionID:
		return left.SessionID < right.SessionID
	case left.Kind != right.Kind:
		return left.Kind < right.Kind
	case left.Operation != right.Operation:
		return left.Operation < right.Operation
	case left.CoverageID != right.CoverageID:
		return left.CoverageID < right.CoverageID
	default:
		return left.ID < right.ID
	}
}

func cloneActivityRecord(record workloadtypes.ActivityRecord) workloadtypes.ActivityRecord {
	cloned := record
	cloned.FirstAt = stripMonotonic(record.FirstAt)
	cloned.LastAt = stripMonotonic(record.LastAt)
	if record.Actor != nil {
		value := *record.Actor
		cloned.Actor = &value
	}
	if record.Mediator != nil {
		value := *record.Mediator
		cloned.Mediator = &value
	}
	if record.Outcome.Code != nil {
		value := *record.Outcome.Code
		cloned.Outcome.Code = &value
	}
	cloned.Truncation = append([]string(nil), record.Truncation...)
	sort.Strings(cloned.Truncation)
	cloned.Subject = cloneSubject(record.Subject)
	return cloned
}

func cloneSubject(subject any) any {
	switch value := subject.(type) {
	case workloadtypes.ProcessSubject:
		value.Argv = append([]string(nil), value.Argv...)
		return value
	case *workloadtypes.ProcessSubject:
		if value == nil {
			return (*workloadtypes.ProcessSubject)(nil)
		}
		cloned := *value
		cloned.Argv = append([]string(nil), value.Argv...)
		return cloned
	case workloadtypes.FileSubject:
		return value
	case *workloadtypes.FileSubject:
		if value == nil {
			return (*workloadtypes.FileSubject)(nil)
		}
		return *value
	case workloadtypes.NetworkSubject:
		return value
	case *workloadtypes.NetworkSubject:
		if value == nil {
			return (*workloadtypes.NetworkSubject)(nil)
		}
		return *value
	case workloadtypes.DNSSubject:
		value.Answers = append([]string(nil), value.Answers...)
		return value
	case *workloadtypes.DNSSubject:
		if value == nil {
			return (*workloadtypes.DNSSubject)(nil)
		}
		cloned := *value
		cloned.Answers = append([]string(nil), value.Answers...)
		return cloned
	case workloadtypes.GenericSubject:
		return value
	case *workloadtypes.GenericSubject:
		if value == nil {
			return (*workloadtypes.GenericSubject)(nil)
		}
		return *value
	default:
		return value
	}
}

func stripMonotonic(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	// Round removes the monotonic component; UTC makes equal instants produce
	// identical keys even if a caller supplied another location.
	return value.Round(0).UTC()
}
