package coverage

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var (
	ErrInvalidTransition = errors.New("coverage transition is invalid")
	ErrTimelineEmpty     = errors.New("coverage timeline is empty")
)

type Transition struct {
	State               string
	Reason              string
	CollectorGeneration uint64
	DroppedEventCount   uint64
	RetentionGap        bool
	Evidence            []workloadtypes.CoverageEvidence
	Sequence            uint64
	At                  time.Time
}

type Timeline struct {
	mu        sync.RWMutex
	owner     workloadtypes.ActivityOwner
	sessionID string
	subsystem string
	intervals []workloadtypes.CoverageInterval
	newID     func() (string, error)
	now       func() time.Time
}

type Summary struct {
	CurrentState        string
	CurrentReason       string
	Intervals           int
	DegradedIntervals   int
	DroppedEventCount   uint64
	RetentionGap        bool
	CollectorGeneration uint64
}

func NewTimeline(owner workloadtypes.ActivityOwner, sessionID, subsystem string) (*Timeline, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	probe := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_validation",
		Owner: owner, SessionID: sessionID, Subsystem: subsystem,
		State: workloadtypes.CoverageAvailable, Reason: ReasonObserverReady,
		CollectorGeneration: 1, StartedAt: time.Unix(1, 0).UTC(),
	}
	if err := probe.Validate(); err != nil {
		return nil, err
	}
	return &Timeline{
		owner: owner, sessionID: sessionID, subsystem: subsystem,
		newID: newCoverageID, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (timeline *Timeline) Transition(next Transition) (workloadtypes.CoverageInterval, error) {
	if timeline == nil {
		return workloadtypes.CoverageInterval{}, ErrInvalidTransition
	}
	timeline.mu.Lock()
	defer timeline.mu.Unlock()
	return timeline.transitionLocked(next)
}

func (timeline *Timeline) MarkLoss(reason string, dropped, sequence uint64, at time.Time) (workloadtypes.CoverageInterval, error) {
	if timeline == nil || dropped == 0 {
		return workloadtypes.CoverageInterval{}, ErrInvalidTransition
	}
	timeline.mu.Lock()
	defer timeline.mu.Unlock()
	return timeline.transitionLocked(Transition{
		State: workloadtypes.CoveragePartial, Reason: reason,
		CollectorGeneration: timeline.currentGenerationLocked(), DroppedEventCount: dropped,
		Sequence: sequence, At: at,
	})
}

func (timeline *Timeline) MarkRetentionGap(reason string, sequence uint64, at time.Time) (workloadtypes.CoverageInterval, error) {
	if timeline == nil {
		return workloadtypes.CoverageInterval{}, ErrInvalidTransition
	}
	timeline.mu.Lock()
	defer timeline.mu.Unlock()
	return timeline.transitionLocked(Transition{
		State: workloadtypes.CoveragePartial, Reason: reason,
		CollectorGeneration: timeline.currentGenerationLocked(), RetentionGap: true,
		Sequence: sequence, At: at,
	})
}

func (timeline *Timeline) Current() (workloadtypes.CoverageInterval, error) {
	if timeline == nil {
		return workloadtypes.CoverageInterval{}, ErrTimelineEmpty
	}
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	if len(timeline.intervals) == 0 {
		return workloadtypes.CoverageInterval{}, ErrTimelineEmpty
	}
	return cloneInterval(timeline.intervals[len(timeline.intervals)-1]), nil
}

func (timeline *Timeline) Intervals(from, to time.Time) []workloadtypes.CoverageInterval {
	if timeline == nil || (!from.IsZero() && !to.IsZero() && to.Before(from)) {
		return []workloadtypes.CoverageInterval{}
	}
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	output := make([]workloadtypes.CoverageInterval, 0, len(timeline.intervals))
	for _, interval := range timeline.intervals {
		if !from.IsZero() && interval.EndedAt != nil && interval.EndedAt.Before(from) {
			continue
		}
		if !to.IsZero() && interval.StartedAt.After(to) {
			continue
		}
		output = append(output, cloneInterval(interval))
	}
	return output
}

func (timeline *Timeline) StateAt(at time.Time) (workloadtypes.CoverageInterval, bool) {
	if timeline == nil || at.IsZero() {
		return workloadtypes.CoverageInterval{}, false
	}
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	for index := len(timeline.intervals) - 1; index >= 0; index-- {
		interval := timeline.intervals[index]
		if at.Before(interval.StartedAt) {
			continue
		}
		if interval.EndedAt == nil || !at.After(*interval.EndedAt) {
			return cloneInterval(interval), true
		}
	}
	return workloadtypes.CoverageInterval{}, false
}

func (timeline *Timeline) GapReasons(from, to time.Time) []string {
	intervals := timeline.Intervals(from, to)
	var reasons []string
	for _, interval := range intervals {
		if interval.State == workloadtypes.CoverageAvailable &&
			interval.DroppedEventCount == 0 && !interval.RetentionGap {
			continue
		}
		if !slices.Contains(reasons, interval.Reason) {
			reasons = append(reasons, interval.Reason)
		}
	}
	slices.Sort(reasons)
	return reasons
}

func (timeline *Timeline) Summary() Summary {
	if timeline == nil {
		return Summary{}
	}
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	summary := Summary{Intervals: len(timeline.intervals)}
	for _, interval := range timeline.intervals {
		if interval.State != workloadtypes.CoverageAvailable {
			summary.DegradedIntervals++
		}
		summary.DroppedEventCount += interval.DroppedEventCount
		summary.RetentionGap = summary.RetentionGap || interval.RetentionGap
	}
	if len(timeline.intervals) > 0 {
		current := timeline.intervals[len(timeline.intervals)-1]
		summary.CurrentState = current.State
		summary.CurrentReason = current.Reason
		summary.CollectorGeneration = current.CollectorGeneration
	}
	return summary
}

func (timeline *Timeline) transitionLocked(next Transition) (workloadtypes.CoverageInterval, error) {
	definition, known := Reason(next.Reason)
	if !known || !slices.Contains(definition.AllowedStates, next.State) ||
		next.CollectorGeneration == 0 {
		return workloadtypes.CoverageInterval{}, ErrInvalidTransition
	}
	if definition.RequiresLossEvidence &&
		next.DroppedEventCount == 0 && !next.RetentionGap {
		return workloadtypes.CoverageInterval{}, ErrInvalidTransition
	}
	if next.State == workloadtypes.CoverageAvailable &&
		(next.DroppedEventCount != 0 || next.RetentionGap) {
		return workloadtypes.CoverageInterval{}, workloadtypes.ErrFalseAvailableCoverage
	}
	if next.At.IsZero() {
		if timeline.now == nil {
			next.At = time.Now().UTC()
		} else {
			next.At = timeline.now().UTC()
		}
	} else {
		next.At = next.At.UTC()
	}
	var (
		closedCurrent *workloadtypes.CoverageInterval
		currentIndex  = len(timeline.intervals) - 1
	)
	if currentIndex >= 0 {
		current := timeline.intervals[currentIndex]
		if next.At.Before(current.StartedAt) {
			return workloadtypes.CoverageInterval{}, ErrInvalidTransition
		}
		if next.CollectorGeneration < current.CollectorGeneration {
			return workloadtypes.CoverageInterval{}, ErrInvalidTransition
		}
		if next.CollectorGeneration == current.CollectorGeneration &&
			next.Sequence < current.StartSequence {
			return workloadtypes.CoverageInterval{}, ErrInvalidTransition
		}
		endedAt := next.At
		endSequence := next.Sequence
		current.EndedAt = &endedAt
		current.EndSequence = &endSequence
		if err := current.Validate(); err != nil {
			return workloadtypes.CoverageInterval{}, err
		}
		closedCurrent = &current
	}
	idGenerator := timeline.newID
	if idGenerator == nil {
		idGenerator = newCoverageID
	}
	id, err := idGenerator()
	if err != nil {
		return workloadtypes.CoverageInterval{}, err
	}
	interval := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: id,
		Owner: timeline.owner, SessionID: timeline.sessionID,
		Subsystem: timeline.subsystem, State: next.State, Reason: next.Reason,
		CollectorGeneration: next.CollectorGeneration,
		DroppedEventCount:   next.DroppedEventCount, RetentionGap: next.RetentionGap,
		Evidence:      append([]workloadtypes.CoverageEvidence(nil), next.Evidence...),
		StartSequence: next.Sequence, StartedAt: next.At,
	}
	if err := interval.ValidateForOwner(timeline.owner); err != nil {
		return workloadtypes.CoverageInterval{}, err
	}
	if closedCurrent != nil {
		timeline.intervals[currentIndex] = *closedCurrent
	}
	timeline.intervals = append(timeline.intervals, interval)
	return cloneInterval(interval), nil
}

func (timeline *Timeline) currentGenerationLocked() uint64 {
	if len(timeline.intervals) == 0 {
		return 1
	}
	return timeline.intervals[len(timeline.intervals)-1].CollectorGeneration
}

func cloneInterval(interval workloadtypes.CoverageInterval) workloadtypes.CoverageInterval {
	cloned := interval
	cloned.Evidence = append([]workloadtypes.CoverageEvidence(nil), interval.Evidence...)
	if interval.EndSequence != nil {
		value := *interval.EndSequence
		cloned.EndSequence = &value
	}
	if interval.EndedAt != nil {
		value := *interval.EndedAt
		cloned.EndedAt = &value
	}
	return cloned
}

func newCoverageID() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate coverage identity: %w", err)
	}
	return "cov_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
