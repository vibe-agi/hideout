package query

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strings"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func (service *Service) Events(
	ctx context.Context,
	input EventsQuery,
) (EventsPage, error) {
	if err := checkContext(ctx); err != nil {
		return EventsPage{}, err
	}
	query, filter, explicitFilter, err := normalizeEventsQuery(input)
	if err != nil {
		return EventsPage{}, err
	}
	offset := 0
	var cursor eventCursor
	if query.Cursor != "" {
		cursor, err = service.decodeCursor(query.Cursor)
		if err != nil {
			return EventsPage{}, err
		}
		if cursor.OwnerKey != query.Owner.Key() {
			return EventsPage{}, ErrCursorOwnerMismatch
		}
		if explicitFilter && !equalEventFilter(filter, cursor.Filter) {
			return EventsPage{}, ErrCursorFilterMismatch
		}
		filter = cursor.Filter
		applyEventFilter(&query, filter)
		offset = cursor.Offset
	}

	snapshot, err := service.loadSnapshot(ctx, query.Owner)
	if err != nil {
		return EventsPage{}, err
	}
	if query.Cursor != "" && cursor.Revision != snapshot.Revision {
		return EventsPage{}, ErrCursorStale
	}
	riskEvidence := selectedRiskEvidence(
		snapshot, query.SessionID, query.Risks,
	)
	records := make([]workloadtypes.ActivityRecord, 0, len(snapshot.Records))
	for index, record := range snapshot.Records {
		if index&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return EventsPage{}, err
			}
		}
		if eventMatches(record, query, riskEvidence) {
			records = append(records, cloneRecord(record))
		}
	}
	if offset > len(records) {
		return EventsPage{}, errors.Join(ErrCursorInvalid, ErrCursorStale)
	}
	end := offset + query.Limit
	if end > len(records) {
		end = len(records)
	}
	page := EventsPage{
		Records:  make([]workloadtypes.ActivityRecord, end-offset),
		Coverage: coverageForEvents(snapshot.Coverage, query),
	}
	copy(page.Records, records[offset:end])
	if end < len(records) {
		page.QueryTruncated = true
		page.NextCursor, err = service.encodeCursor(eventCursor{
			OwnerKey: query.Owner.Key(), Revision: snapshot.Revision,
			Filter: filter, Offset: end,
		})
		if err != nil {
			return EventsPage{}, err
		}
	}
	return page, nil
}

func eventMatches(
	record workloadtypes.ActivityRecord,
	query EventsQuery,
	riskEvidence map[string]struct{},
) bool {
	if (query.SessionID != "" && record.SessionID != query.SessionID) ||
		!overlaps(record.FirstAt, record.LastAt, query.From, query.To) ||
		!selected(query.Kinds, record.Kind) ||
		!selected(query.Operations, record.Operation) {
		return false
	}
	if len(query.Executions) != 0 && !recordExecutionSelected(record, query.Executions) {
		return false
	}
	if len(query.Risks) != 0 {
		if _, exists := riskEvidence[record.ID]; !exists {
			return false
		}
	}
	if query.Path != "" && !recordPathContains(record, query.Path) {
		return false
	}
	if query.Domain != "" && !recordDomainContains(record, query.Domain) {
		return false
	}
	if query.IP != "" && !recordHasIP(record, query.IP) {
		return false
	}
	return true
}

func selectedRiskEvidence(
	snapshot Snapshot,
	sessionID string,
	selectors []string,
) map[string]struct{} {
	if len(selectors) == 0 {
		return nil
	}
	wanted := stringSet(selectors)
	result := make(map[string]struct{})
	for _, finding := range snapshot.Risks {
		if sessionID != "" && finding.SessionID != sessionID {
			continue
		}
		if _, exists := wanted[finding.ID]; !exists {
			if _, exists = wanted[finding.RuleID]; !exists {
				continue
			}
		}
		for _, reference := range finding.EvidenceRefs {
			result[reference] = struct{}{}
		}
	}
	return result
}

func recordExecutionSelected(
	record workloadtypes.ActivityRecord,
	executions []string,
) bool {
	wanted := stringSet(executions)
	if record.Actor != nil {
		if _, exists := wanted[record.Actor.ExecutionID]; exists {
			return true
		}
	}
	if record.Mediator != nil {
		if _, exists := wanted[record.Mediator.ExecutionID]; exists {
			return true
		}
	}
	if subject, ok := record.Subject.(workloadtypes.ProcessSubject); ok {
		if _, exists := wanted[subject.ExecutionID]; exists {
			return true
		}
	}
	return false
}

func recordPathContains(record workloadtypes.ActivityRecord, wanted string) bool {
	subject, ok := record.Subject.(workloadtypes.FileSubject)
	return ok && (strings.Contains(subject.Path, wanted) ||
		strings.Contains(subject.TargetPath, wanted))
}

func recordDomainContains(record workloadtypes.ActivityRecord, wanted string) bool {
	wanted = strings.ToLower(wanted)
	switch subject := record.Subject.(type) {
	case workloadtypes.NetworkSubject:
		return strings.Contains(strings.ToLower(strings.TrimSuffix(subject.Domain, ".")), wanted)
	case workloadtypes.DNSSubject:
		return strings.Contains(strings.ToLower(strings.TrimSuffix(subject.Query, ".")), wanted)
	default:
		return false
	}
}

func recordHasIP(record workloadtypes.ActivityRecord, wanted string) bool {
	switch subject := record.Subject.(type) {
	case workloadtypes.NetworkSubject:
		return canonicalIP(subject.IP) == wanted || canonicalIP(subject.TargetIP) == wanted
	case workloadtypes.DNSSubject:
		for _, answer := range subject.Answers {
			if canonicalIP(answer) == wanted {
				return true
			}
		}
	}
	return false
}

func canonicalIP(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	return address.Unmap().String()
}

func coverageForEvents(
	intervals []workloadtypes.CoverageInterval,
	query EventsQuery,
) []workloadtypes.CoverageInterval {
	subsystems := subsystemsForKinds(query.Kinds)
	result := make([]workloadtypes.CoverageInterval, 0, len(intervals))
	for _, interval := range intervals {
		if query.SessionID != "" && interval.SessionID != query.SessionID {
			continue
		}
		if len(subsystems) != 0 {
			if _, exists := subsystems[interval.Subsystem]; !exists {
				continue
			}
		}
		if coverageOverlaps(interval, query.From, query.To) {
			result = append(result, cloneCoverage(interval))
		}
	}
	return result
}

func subsystemsForKinds(kinds []string) map[string]struct{} {
	if len(kinds) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		switch kind {
		case workloadtypes.ActivityProcess:
			result[workloadtypes.SubsystemProcess] = struct{}{}
		case workloadtypes.ActivityFile:
			result[workloadtypes.SubsystemFile] = struct{}{}
		case workloadtypes.ActivityConnection:
			result[workloadtypes.SubsystemNetwork] = struct{}{}
		case workloadtypes.ActivityDNS:
			result[workloadtypes.SubsystemDNS] = struct{}{}
		}
	}
	return result
}

func overlaps(
	firstAt, lastAt, from, to time.Time,
) bool {
	return (from.IsZero() || !lastAt.Before(from)) &&
		(to.IsZero() || !firstAt.After(to))
}

func selected(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
