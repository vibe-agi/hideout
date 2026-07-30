//go:build linux

package main

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	workloadobs "github.com/vibe-agi/hideout/internal/workloadobs"
	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	observerFileAggregationWindow = workloadobs.DefaultFileEventAggregationWindow
	observerFileAggregationLimit  = 4096
)

var errObserverFileAggregationOverflow = errors.New(
	"observer file aggregation counter overflow",
)

// fileObservationAggregator coalesces repetitive, non-destructive file
// events before ActivityRecord ID hashing, validation, JSON encoding, and the
// guest-to-host transport. It preserves raw operation and byte counts,
// first/last timestamps, and first/last collector sequences. Destructive
// operations are deliberately emitted individually.
type fileObservationAggregator struct {
	pending map[fileObservationKey]aggregatedFileObservation
}

type aggregatedFileObservation struct {
	event filecollector.Event

	count         uint64
	bytes         uint64
	firstAt       time.Time
	lastAt        time.Time
	firstSequence uint64
	lastSequence  uint64

	cpu         uint64
	monotonicNS uint64
}

type fileObservationKey struct {
	kind               filecollector.EventKind
	owner              workloadtypes.ActivityOwner
	sessionID          string
	cgroupID           uint64
	observerGeneration uint64
	actor              workloadtypes.Actor
	attribution        string
	path               string
	targetPath         string
	pathState          string
	pathClass          string
	fileType           string
	device             uint64
	inode              uint64
	mountID            uint64
	coverageID         string
	limitations        string

	outcomeStatus  string
	outcomeCodeSet bool
	outcomeCode    int
	outcomeSignal  uint32
	outcomeReason  string
}

func newFileObservationAggregator() *fileObservationAggregator {
	return &fileObservationAggregator{
		pending: make(map[fileObservationKey]aggregatedFileObservation),
	}
}

func (aggregator *fileObservationAggregator) Len() int {
	if aggregator == nil {
		return 0
	}
	return len(aggregator.pending)
}

// Add returns false when the caller must flush the current window before
// adding event. This keeps capacity and arithmetic failure explicit instead
// of silently dropping or saturating evidence.
func (aggregator *fileObservationAggregator) Add(
	event filecollector.Event,
) (bool, error) {
	if aggregator == nil || aggregator.pending == nil {
		return false, errors.New("observer file aggregator is unavailable")
	}
	key, aggregatable := fileObservationAggregationKey(event)
	if !aggregatable {
		return false, nil
	}
	current, exists := aggregator.pending[key]
	if !exists {
		if len(aggregator.pending) >= observerFileAggregationLimit {
			return false, nil
		}
		aggregator.pending[key] = aggregatedFileObservation{
			event: event,
			count: 1, bytes: event.Bytes,
			firstAt: event.At, lastAt: event.At,
			firstSequence: event.Sequence, lastSequence: event.Sequence,
			cpu: event.CPU, monotonicNS: event.MonotonicNS,
		}
		return true, nil
	}
	if current.count == math.MaxUint64 ||
		math.MaxUint64-current.bytes < event.Bytes {
		return false, errObserverFileAggregationOverflow
	}
	current.count++
	current.bytes += event.Bytes
	if event.At.Before(current.firstAt) {
		current.firstAt = event.At
		current.firstSequence = minSequence(
			current.firstSequence,
			event.Sequence,
		)
	}
	if event.At.After(current.lastAt) {
		current.lastAt = event.At
		current.cpu = event.CPU
		current.monotonicNS = event.MonotonicNS
	}
	current.firstSequence = minSequence(
		current.firstSequence,
		event.Sequence,
	)
	if event.Sequence > current.lastSequence {
		current.lastSequence = event.Sequence
	}
	aggregator.pending[key] = current
	return true, nil
}

func (aggregator *fileObservationAggregator) Flush(
	normalizer *filecollector.Normalizer,
	sink observerRecordSink,
) error {
	if aggregator == nil || aggregator.pending == nil ||
		normalizer == nil || sink == nil {
		return errors.New("observer file aggregation sink is unavailable")
	}
	if len(aggregator.pending) == 0 {
		return nil
	}
	observations := make([]aggregatedFileObservation, 0, len(aggregator.pending))
	for _, observation := range aggregator.pending {
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(left, right int) bool {
		leftObservation := observations[left]
		rightObservation := observations[right]
		switch {
		case leftObservation.firstAt.Before(rightObservation.firstAt):
			return true
		case rightObservation.firstAt.Before(leftObservation.firstAt):
			return false
		case leftObservation.firstSequence != rightObservation.firstSequence:
			return leftObservation.firstSequence <
				rightObservation.firstSequence
		case leftObservation.lastSequence != rightObservation.lastSequence:
			return leftObservation.lastSequence <
				rightObservation.lastSequence
		default:
			return leftObservation.event.Path <
				rightObservation.event.Path
		}
	})
	for _, observation := range observations {
		if err := emitAggregatedFileObservation(
			normalizer,
			sink,
			observation,
		); err != nil {
			return err
		}
	}
	clear(aggregator.pending)
	return nil
}

func emitFileObservation(
	normalizer *filecollector.Normalizer,
	sink observerRecordSink,
	event filecollector.Event,
) error {
	return emitAggregatedFileObservation(
		normalizer,
		sink,
		aggregatedFileObservation{
			event: event,
			count: 1, bytes: event.Bytes,
			firstAt: event.At, lastAt: event.At,
			firstSequence: event.Sequence, lastSequence: event.Sequence,
			cpu: event.CPU, monotonicNS: event.MonotonicNS,
		},
	)
}

func emitAggregatedFileObservation(
	normalizer *filecollector.Normalizer,
	sink observerRecordSink,
	observation aggregatedFileObservation,
) error {
	if normalizer == nil || sink == nil ||
		observation.count == 0 ||
		observation.firstAt.IsZero() ||
		observation.lastAt.Before(observation.firstAt) ||
		observation.lastSequence < observation.firstSequence {
		return errors.New("observer aggregated file event is invalid")
	}
	event := observation.event
	event.At = observation.firstAt
	event.Sequence = observation.firstSequence
	event.Bytes = observation.bytes
	record, err := normalizer.Normalize(event)
	if err != nil {
		return err
	}
	record.Count = observation.count
	record.Bytes = observation.bytes
	record.FirstAt = observation.firstAt
	record.LastAt = observation.lastAt
	record.FirstSequence = observation.firstSequence
	record.LastSequence = observation.lastSequence
	if err := record.Validate(); err != nil {
		return err
	}
	return sink(observerRecord{
		Record: record, CPU: observation.cpu,
		MonotonicNS: observation.monotonicNS,
	})
}

func fileObservationAggregationKey(
	event filecollector.Event,
) (fileObservationKey, bool) {
	switch event.Kind {
	case filecollector.EventRename,
		filecollector.EventUnlink,
		filecollector.EventTruncate,
		filecollector.EventRmdir:
		return fileObservationKey{}, false
	}
	key := fileObservationKey{
		kind:  event.Kind,
		owner: event.Owner, sessionID: event.SessionID,
		cgroupID:           event.CgroupID,
		observerGeneration: event.ObserverGeneration,
		actor:              event.Actor, attribution: event.Attribution,
		path: event.Path, targetPath: event.TargetPath,
		pathState: event.PathState, pathClass: event.PathClass,
		fileType: event.FileType,
		device:   event.Device, inode: event.Inode, mountID: event.MountID,
		coverageID:    event.CoverageID,
		limitations:   strings.Join(event.Limitations, "\x00"),
		outcomeStatus: event.Outcome.Status,
		outcomeSignal: event.Outcome.Signal,
		outcomeReason: event.Outcome.Reason,
	}
	if event.Outcome.Code != nil {
		key.outcomeCodeSet = true
		key.outcomeCode = *event.Outcome.Code
	}
	return key, true
}

func minSequence(left, right uint64) uint64 {
	if left == 0 || right < left {
		return right
	}
	return left
}
