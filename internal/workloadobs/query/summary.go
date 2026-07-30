package query

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func (service *Service) Summary(
	ctx context.Context,
	input SummaryQuery,
) (SummaryResult, error) {
	if err := checkContext(ctx); err != nil {
		return SummaryResult{}, err
	}
	query, err := normalizeSummaryQuery(input)
	if err != nil {
		return SummaryResult{}, err
	}
	snapshot, err := service.loadSnapshot(ctx, query.Owner)
	if err != nil {
		return SummaryResult{}, err
	}
	result := SummaryResult{
		Owner: query.Owner, Counts: make(map[string]uint64),
		HighestRisks: make([]risk.Finding, 0),
		Quota: QuotaSummary{
			UsedBytes:     snapshot.Retention.UsedBytes,
			LimitBytes:    snapshot.Retention.LimitBytes,
			MaxAgeSeconds: snapshot.Retention.MaxAgeSeconds,
		},
		Pruned: snapshot.Retention.Pruned, Corrupt: snapshot.Retention.Corrupt,
		Reasons: append([]string(nil), snapshot.Retention.Reasons...),
	}
	matchingRecords := 0
	for index, record := range snapshot.Records {
		if index&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return SummaryResult{}, err
			}
		}
		if (query.SessionID != "" && record.SessionID != query.SessionID) ||
			!overlaps(record.FirstAt, record.LastAt, query.From, query.To) {
			continue
		}
		result.Counts[record.Kind] = saturatingCount(
			result.Counts[record.Kind], record.Count,
		)
		matchingRecords++
	}
	result.CurrentCoverage = currentCoverage(
		snapshot.Coverage, query.SessionID, query.From, query.To, nil,
	)
	for _, finding := range snapshot.Risks {
		if (query.SessionID == "" || finding.SessionID == query.SessionID) &&
			overlaps(finding.FirstAt, finding.LastAt, query.From, query.To) {
			result.HighestRisks = append(result.HighestRisks, cloneRisk(finding))
		}
	}
	sortRiskFindings(result.HighestRisks)
	if len(result.HighestRisks) > DefaultHighestRiskLimit {
		result.HighestRisks = result.HighestRisks[:DefaultHighestRiskLimit]
	}
	result.RetainedRange = retainedRange(snapshot, query.SessionID)
	if matchingRecords != 0 {
		filter := eventFilter{
			SessionID: query.SessionID,
			From:      query.From,
			To:        query.To,
		}
		result.LatestCursor, err = service.encodeCursor(eventCursor{
			OwnerKey: query.Owner.Key(), Revision: snapshot.Revision,
			Filter: filter, Offset: matchingRecords,
		})
		if err != nil {
			return SummaryResult{}, err
		}
	}
	return result, nil
}

func (service *Service) Coverage(
	ctx context.Context,
	input CoverageQuery,
) (CoverageResult, error) {
	if err := checkContext(ctx); err != nil {
		return CoverageResult{}, err
	}
	query, err := normalizeCoverageQuery(input)
	if err != nil {
		return CoverageResult{}, err
	}
	snapshot, err := service.loadSnapshot(ctx, query.Owner)
	if err != nil {
		return CoverageResult{}, err
	}
	subsystems := stringSet(query.Subsystems)
	result := CoverageResult{
		Intervals: make([]workloadtypes.CoverageInterval, 0),
	}
	for _, interval := range snapshot.Coverage {
		if query.SessionID != "" && interval.SessionID != query.SessionID {
			continue
		}
		if len(subsystems) != 0 {
			if _, exists := subsystems[interval.Subsystem]; !exists {
				continue
			}
		}
		if coverageOverlaps(interval, query.From, query.To) {
			result.Intervals = append(result.Intervals, cloneCoverage(interval))
		}
	}
	result.Current = currentCoverage(
		snapshot.Coverage, query.SessionID,
		query.From, query.To, subsystems,
	)
	return result, nil
}

func (service *Service) Risks(
	ctx context.Context,
	input RisksQuery,
) (RisksResult, error) {
	if err := checkContext(ctx); err != nil {
		return RisksResult{}, err
	}
	query, err := normalizeRisksQuery(input)
	if err != nil {
		return RisksResult{}, err
	}
	snapshot, err := service.loadSnapshot(ctx, query.Owner)
	if err != nil {
		return RisksResult{}, err
	}
	severities := stringSet(query.Severities)
	rules := stringSet(query.Rules)
	executions := stringSet(query.Executions)
	evidenceByExecution := make(map[string]struct{})
	if len(executions) != 0 {
		for _, record := range snapshot.Records {
			if query.SessionID != "" && record.SessionID != query.SessionID {
				continue
			}
			if recordExecutionSelected(record, query.Executions) {
				evidenceByExecution[record.ID] = struct{}{}
			}
		}
	}
	result := RisksResult{Findings: make([]risk.Finding, 0)}
	for _, finding := range snapshot.Risks {
		if (query.SessionID != "" && finding.SessionID != query.SessionID) ||
			!overlaps(finding.FirstAt, finding.LastAt, query.From, query.To) {
			continue
		}
		if len(severities) != 0 {
			if _, exists := severities[finding.Severity]; !exists {
				continue
			}
		}
		if len(rules) != 0 {
			if _, exists := rules[finding.RuleID]; !exists {
				continue
			}
		}
		if len(executions) != 0 &&
			!findingHasEvidence(finding, evidenceByExecution) {
			continue
		}
		result.Findings = append(result.Findings, cloneRisk(finding))
	}
	sortRiskFindings(result.Findings)
	return result, nil
}

func currentCoverage(
	intervals []workloadtypes.CoverageInterval,
	sessionID string,
	from, to time.Time,
	subsystems map[string]struct{},
) []workloadtypes.CoverageInterval {
	current := make(map[string]workloadtypes.CoverageInterval)
	for _, interval := range intervals {
		if sessionID != "" && interval.SessionID != sessionID {
			continue
		}
		if len(subsystems) != 0 {
			if _, exists := subsystems[interval.Subsystem]; !exists {
				continue
			}
		}
		if !coverageOverlaps(interval, from, to) {
			continue
		}
		existing, exists := current[interval.Subsystem]
		if !exists || existing.StartedAt.Before(interval.StartedAt) ||
			(existing.StartedAt.Equal(interval.StartedAt) && existing.ID < interval.ID) {
			current[interval.Subsystem] = interval
		}
	}
	result := make([]workloadtypes.CoverageInterval, 0, len(current))
	for _, interval := range current {
		result = append(result, cloneCoverage(interval))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Subsystem != result[right].Subsystem {
			return result[left].Subsystem < result[right].Subsystem
		}
		return result[left].ID < result[right].ID
	})
	return result
}

func coverageOverlaps(
	interval workloadtypes.CoverageInterval,
	from, to time.Time,
) bool {
	if !to.IsZero() && interval.StartedAt.After(to) {
		return false
	}
	if !from.IsZero() && interval.EndedAt != nil && interval.EndedAt.Before(from) {
		return false
	}
	return true
}

func retainedRange(snapshot Snapshot, sessionID string) TimeRange {
	if sessionID == "" && !snapshot.Retention.EarliestAt.IsZero() {
		return TimeRange{
			From: snapshot.Retention.EarliestAt,
			To:   snapshot.Retention.LatestAt,
		}
	}
	var result TimeRange
	consider := func(first, last time.Time) {
		if first.IsZero() || last.IsZero() {
			return
		}
		if result.From.IsZero() || first.Before(result.From) {
			result.From = first
		}
		if result.To.IsZero() || last.After(result.To) {
			result.To = last
		}
	}
	for _, record := range snapshot.Records {
		if sessionID != "" && record.SessionID != sessionID {
			continue
		}
		consider(record.FirstAt, record.LastAt)
	}
	for _, interval := range snapshot.Coverage {
		if sessionID != "" && interval.SessionID != sessionID {
			continue
		}
		last := interval.StartedAt
		if interval.EndedAt != nil {
			last = *interval.EndedAt
		}
		consider(interval.StartedAt, last)
	}
	for _, finding := range snapshot.Risks {
		if sessionID != "" && finding.SessionID != sessionID {
			continue
		}
		consider(finding.FirstAt, finding.LastAt)
	}
	return result
}

func findingHasEvidence(
	finding risk.Finding,
	evidence map[string]struct{},
) bool {
	for _, reference := range finding.EvidenceRefs {
		if _, exists := evidence[reference]; exists {
			return true
		}
	}
	return false
}

func sortRiskFindings(findings []risk.Finding) {
	sort.Slice(findings, func(left, right int) bool {
		leftRank := riskRank(findings[left].Severity)
		rightRank := riskRank(findings[right].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if !findings[left].LastAt.Equal(findings[right].LastAt) {
			return findings[left].LastAt.After(findings[right].LastAt)
		}
		return findings[left].ID < findings[right].ID
	})
}

func riskRank(severity string) int {
	switch severity {
	case risk.SeverityCritical:
		return 5
	case risk.SeverityHigh:
		return 4
	case risk.SeverityMedium:
		return 3
	case risk.SeverityLow:
		return 2
	default:
		return 1
	}
}

func saturatingCount(left, right uint64) uint64 {
	if right > math.MaxUint64-left {
		return math.MaxUint64
	}
	return left + right
}
