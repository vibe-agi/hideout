package query

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	maxSnapshotRecords    = 1 << 20
	maxSnapshotExecutions = 1 << 16
	maxSnapshotCoverage   = 1 << 20
	maxSnapshotRisks      = 1 << 20
	maxRetentionReasons   = 256
)

type SourceFunc func(
	context.Context,
	workloadtypes.ActivityOwner,
) (Snapshot, error)

func (source SourceFunc) Snapshot(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (Snapshot, error) {
	if source == nil {
		return Snapshot{}, ErrOwnerNotFound
	}
	return source(ctx, owner)
}

type MemorySource struct {
	mu        sync.RWMutex
	snapshots map[string]Snapshot
}

func NewMemorySource() *MemorySource {
	return &MemorySource{snapshots: make(map[string]Snapshot)}
}

func (source *MemorySource) Replace(snapshot Snapshot) error {
	if source == nil {
		return ErrInvalidSnapshot
	}
	normalized, err := normalizeSnapshot(snapshot)
	if err != nil {
		return err
	}
	key := normalized.Owner.Key()
	if key == "" {
		return ErrInvalidSnapshot
	}
	source.mu.Lock()
	source.snapshots[key] = normalized
	source.mu.Unlock()
	return nil
}

func (source *MemorySource) Snapshot(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (Snapshot, error) {
	if source == nil || owner.Validate() != nil {
		return Snapshot{}, ErrOwnerNotFound
	}
	if ctx == nil {
		return Snapshot{}, ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	source.mu.RLock()
	snapshot, exists := source.snapshots[owner.Key()]
	source.mu.RUnlock()
	if !exists || !snapshot.Owner.Equal(owner) {
		return Snapshot{}, ErrOwnerNotFound
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(snapshot), nil
}

func normalizeSnapshot(snapshot Snapshot) (Snapshot, error) {
	normalized := cloneSnapshot(snapshot)
	normalized.Retention.EarliestAt = normalizeTime(normalized.Retention.EarliestAt)
	normalized.Retention.LatestAt = normalizeTime(normalized.Retention.LatestAt)
	sort.Strings(normalized.Retention.Reasons)
	sort.Slice(normalized.Records, func(left, right int) bool {
		return lessRecord(normalized.Records[left], normalized.Records[right])
	})
	sort.Slice(normalized.Executions, func(left, right int) bool {
		return lessExecution(normalized.Executions[left], normalized.Executions[right])
	})
	sort.Slice(normalized.Coverage, func(left, right int) bool {
		return lessCoverage(normalized.Coverage[left], normalized.Coverage[right])
	})
	sort.Slice(normalized.Risks, func(left, right int) bool {
		return lessRisk(normalized.Risks[left], normalized.Risks[right])
	})
	if err := normalized.validate(); err != nil {
		return Snapshot{}, err
	}
	return normalized, nil
}

func (snapshot Snapshot) validate() error {
	if !revisionPattern.MatchString(snapshot.Revision) ||
		snapshot.Owner.Validate() != nil ||
		len(snapshot.Records) > maxSnapshotRecords ||
		len(snapshot.Executions) > maxSnapshotExecutions ||
		len(snapshot.Coverage) > maxSnapshotCoverage ||
		len(snapshot.Risks) > maxSnapshotRisks ||
		len(snapshot.Retention.Reasons) > maxRetentionReasons {
		return ErrInvalidSnapshot
	}
	if err := validateRetention(snapshot.Retention); err != nil {
		return errors.Join(ErrInvalidSnapshot, err)
	}

	recordIDs := make(map[string]workloadtypes.ActivityRecord, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if err := record.ValidatePersistable(); err != nil ||
			!record.Owner.Equal(snapshot.Owner) ||
			!sessionMatchesOwner(snapshot.Owner, record.SessionID) {
			return ErrInvalidSnapshot
		}
		if _, exists := recordIDs[record.ID]; exists {
			return ErrInvalidSnapshot
		}
		recordIDs[record.ID] = record
	}

	executionIDs := make(map[string]workloadtypes.Execution, len(snapshot.Executions))
	for _, execution := range snapshot.Executions {
		if err := execution.Validate(); err != nil ||
			!execution.Owner.Equal(snapshot.Owner) ||
			!sessionMatchesOwner(snapshot.Owner, execution.SessionID) {
			return ErrInvalidSnapshot
		}
		if _, exists := executionIDs[execution.ID]; exists {
			return ErrInvalidSnapshot
		}
		executionIDs[execution.ID] = execution
	}
	if executionGraphCyclic(executionIDs) {
		return ErrInvalidSnapshot
	}
	for _, execution := range snapshot.Executions {
		if parent, exists := executionIDs[execution.ParentExecutionID]; exists &&
			parent.SessionID != execution.SessionID {
			return ErrInvalidSnapshot
		}
	}
	for _, record := range snapshot.Records {
		for _, executionID := range recordExecutionIDs(record) {
			if execution, exists := executionIDs[executionID]; exists &&
				execution.SessionID != record.SessionID {
				return ErrInvalidSnapshot
			}
		}
	}

	coverageIDs := make(
		map[string]workloadtypes.CoverageInterval,
		len(snapshot.Coverage),
	)
	for _, interval := range snapshot.Coverage {
		if err := interval.Validate(); err != nil ||
			!interval.Owner.Equal(snapshot.Owner) ||
			!sessionMatchesOwner(snapshot.Owner, interval.SessionID) {
			return ErrInvalidSnapshot
		}
		if _, exists := coverageIDs[interval.ID]; exists {
			return ErrInvalidSnapshot
		}
		coverageIDs[interval.ID] = interval
	}

	riskIDs := make(map[string]struct{}, len(snapshot.Risks))
	for _, finding := range snapshot.Risks {
		if err := finding.Validate(); err != nil ||
			!finding.Owner.Equal(snapshot.Owner) ||
			!sessionMatchesOwner(snapshot.Owner, finding.SessionID) {
			return ErrInvalidSnapshot
		}
		if _, exists := riskIDs[finding.ID]; exists {
			return ErrInvalidSnapshot
		}
		if interval, exists := coverageIDs[finding.CoverageID]; exists &&
			interval.SessionID != finding.SessionID {
			return ErrInvalidSnapshot
		}
		for _, reference := range finding.EvidenceRefs {
			if record, exists := recordIDs[reference]; exists &&
				record.SessionID != finding.SessionID {
				return ErrInvalidSnapshot
			}
		}
		riskIDs[finding.ID] = struct{}{}
	}
	return nil
}

func recordExecutionIDs(record workloadtypes.ActivityRecord) []string {
	result := make([]string, 0, 3)
	if record.Actor != nil {
		result = append(result, record.Actor.ExecutionID)
	}
	if record.Mediator != nil && record.Mediator.ExecutionID != "" {
		result = append(result, record.Mediator.ExecutionID)
	}
	if subject, ok := record.Subject.(workloadtypes.ProcessSubject); ok {
		result = append(result, subject.ExecutionID)
	}
	return result
}

func validateRetention(retention RetentionState) error {
	if retention.EarliestAt.IsZero() != retention.LatestAt.IsZero() ||
		(!retention.EarliestAt.IsZero() &&
			retention.LatestAt.Before(retention.EarliestAt)) ||
		retention.MaxAgeSeconds < 0 ||
		retention.MaxAgeSeconds >
			workloadtypes.MaximumActivityRetentionMaxAgeSeconds {
		return ErrInvalidSnapshot
	}
	previous := ""
	for _, reason := range retention.Reasons {
		if !operationPattern.MatchString(reason) || reason <= previous {
			return ErrInvalidSnapshot
		}
		previous = reason
	}
	if retention.Pruned && !containsString(retention.Reasons, "retention-pruned") {
		return ErrInvalidSnapshot
	}
	if retention.Corrupt && !containsString(retention.Reasons, "corrupt-segment") {
		return ErrInvalidSnapshot
	}
	return nil
}

func executionGraphCyclic(executions map[string]workloadtypes.Execution) bool {
	const (
		unseen   = uint8(0)
		visiting = uint8(1)
		visited  = uint8(2)
	)
	states := make(map[string]uint8, len(executions))
	var visit func(string, int) bool
	visit = func(id string, depth int) bool {
		if depth > 1024 {
			return true
		}
		switch states[id] {
		case visiting:
			return true
		case visited:
			return false
		}
		states[id] = visiting
		parent := executions[id].ParentExecutionID
		if _, exists := executions[parent]; exists && visit(parent, depth+1) {
			return true
		}
		states[id] = visited
		return false
	}
	for id := range executions {
		if visit(id, 1) {
			return true
		}
	}
	return false
}

func sessionMatchesOwner(owner workloadtypes.ActivityOwner, sessionID string) bool {
	return owner.Kind != workloadtypes.OwnerDisposableSession ||
		owner.SessionID == sessionID
}

func lessRecord(left, right workloadtypes.ActivityRecord) bool {
	switch {
	case left.FirstAt.Before(right.FirstAt):
		return true
	case right.FirstAt.Before(left.FirstAt):
		return false
	case left.FirstSequence != right.FirstSequence:
		return left.FirstSequence < right.FirstSequence
	default:
		return left.ID < right.ID
	}
}

func lessExecution(left, right workloadtypes.Execution) bool {
	switch {
	case left.StartedAt.Before(right.StartedAt):
		return true
	case right.StartedAt.Before(left.StartedAt):
		return false
	case left.StartedAtMonoNS != right.StartedAtMonoNS:
		return left.StartedAtMonoNS < right.StartedAtMonoNS
	default:
		return left.ID < right.ID
	}
}

func lessCoverage(left, right workloadtypes.CoverageInterval) bool {
	switch {
	case left.StartedAt.Before(right.StartedAt):
		return true
	case right.StartedAt.Before(left.StartedAt):
		return false
	case left.Subsystem != right.Subsystem:
		return left.Subsystem < right.Subsystem
	default:
		return left.ID < right.ID
	}
}

func lessRisk(left, right risk.Finding) bool {
	switch {
	case left.FirstAt.Before(right.FirstAt):
		return true
	case right.FirstAt.Before(left.FirstAt):
		return false
	case left.RuleID != right.RuleID:
		return left.RuleID < right.RuleID
	default:
		return left.ID < right.ID
	}
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Round(0).UTC()
}

func containsString(values []string, wanted string) bool {
	index := sort.SearchStrings(values, wanted)
	return index < len(values) && values[index] == wanted
}
