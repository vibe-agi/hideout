package fanotify

import (
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type LossKind string

const (
	LossQueueOverflow    LossKind = "queue-overflow"
	LossFilterUnresolved LossKind = "filter-unresolved"
	LossDecodeFailure    LossKind = "decode-failure"
	LossUnsupportedMask  LossKind = "unsupported-mask"
)

var (
	ErrCoverageConfig = errors.New("fanotify coverage configuration is invalid")
	ErrCoverageClosed = errors.New("fanotify coverage interval is already closed")
	ErrLossKind       = errors.New("fanotify loss kind is invalid")
)

type CoverageTracker struct {
	mu sync.Mutex

	boundary            filecollector.Boundary
	id                  string
	collectorGeneration uint64
	startSequence       uint64
	startedAt           time.Time
	losses              map[LossKind]uint64
	droppedLowerBound   uint64
	endSequence         *uint64
	endedAt             *time.Time
}

func NewCoverageTracker(
	boundary filecollector.Boundary,
	id string,
	collectorGeneration, startSequence uint64,
	startedAt time.Time,
) (*CoverageTracker, error) {
	if boundary.Validate() != nil ||
		!validCoverageID(id) ||
		collectorGeneration == 0 ||
		startSequence == 0 ||
		startedAt.IsZero() {
		return nil, ErrCoverageConfig
	}
	result := &CoverageTracker{
		boundary: boundary, id: id,
		collectorGeneration: collectorGeneration,
		startSequence:       startSequence,
		startedAt:           startedAt.UTC(),
		losses:              make(map[LossKind]uint64),
	}
	if err := result.snapshotLocked().Validate(); err != nil {
		return nil, errors.Join(ErrCoverageConfig, err)
	}
	return result, nil
}

func (tracker *CoverageTracker) RecordLoss(kind LossKind) error {
	if tracker == nil {
		return ErrCoverageConfig
	}
	switch kind {
	case LossQueueOverflow,
		LossFilterUnresolved,
		LossDecodeFailure,
		LossUnsupportedMask:
	default:
		return ErrLossKind
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.endedAt != nil {
		return ErrCoverageClosed
	}
	if tracker.losses[kind] == math.MaxUint64 ||
		(kind == LossUnsupportedMask &&
			tracker.droppedLowerBound == math.MaxUint64) {
		return ErrCoverageConfig
	}
	tracker.losses[kind]++
	if kind == LossUnsupportedMask {
		tracker.droppedLowerBound++
	}
	return nil
}

func (tracker *CoverageTracker) Close(endSequence uint64, endedAt time.Time) error {
	if tracker == nil || endedAt.IsZero() {
		return ErrCoverageConfig
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.endedAt != nil {
		return ErrCoverageClosed
	}
	if endSequence < tracker.startSequence ||
		endedAt.Before(tracker.startedAt) {
		return ErrCoverageConfig
	}
	sequence := endSequence
	at := endedAt.UTC()
	tracker.endSequence = &sequence
	tracker.endedAt = &at
	return nil
}

func (tracker *CoverageTracker) Snapshot() workloadtypes.CoverageInterval {
	if tracker == nil {
		return workloadtypes.CoverageInterval{}
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.snapshotLocked()
}

func (tracker *CoverageTracker) snapshotLocked() workloadtypes.CoverageInterval {
	evidence := []workloadtypes.CoverageEvidence{
		{Code: "fanotify.drop-count-semantics", Value: "known-target-lower-bound"},
		{Code: "fanotify.events-may-merge"},
		{Code: "fanotify.mark-scope", Value: "mount"},
		{
			Code:  "fanotify.membership-semantics",
			Value: "receipt-time-pid",
		},
		{Code: "fanotify.mmap-unavailable"},
		{Code: "fanotify.operation-scope", Value: "open,read,write"},
		{Code: "fanotify.queue-overflow-possible"},
		{Code: "fanotify.timestamp-semantics", Value: "receipt-time"},
	}
	if value := tracker.losses[LossQueueOverflow]; value != 0 {
		evidence = append(evidence, workloadtypes.CoverageEvidence{
			Code:  "fanotify.queue-overflow",
			Value: strconv.FormatUint(value, 10),
		})
	}
	for _, current := range []struct {
		kind LossKind
		code string
	}{
		{LossFilterUnresolved, "fanotify.filter-unresolved"},
		{LossDecodeFailure, "fanotify.decode-failure"},
		{LossUnsupportedMask, "fanotify.unsupported-mask"},
	} {
		if value := tracker.losses[current.kind]; value != 0 {
			evidence = append(evidence, workloadtypes.CoverageEvidence{
				Code:  current.code,
				Value: strconv.FormatUint(value, 10),
			})
		}
	}
	result := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema,
		ID:     tracker.id, Owner: tracker.boundary.Owner,
		SessionID:           tracker.boundary.SessionID,
		Subsystem:           workloadtypes.SubsystemFile,
		State:               workloadtypes.CoveragePartial,
		Reason:              "fanotify-fallback",
		CollectorGeneration: tracker.collectorGeneration,
		DroppedEventCount:   tracker.droppedLowerBound,
		Evidence:            evidence,
		StartSequence:       tracker.startSequence,
		StartedAt:           tracker.startedAt,
	}
	if tracker.endSequence != nil {
		value := *tracker.endSequence
		result.EndSequence = &value
	}
	if tracker.endedAt != nil {
		value := *tracker.endedAt
		result.EndedAt = &value
	}
	return result
}
