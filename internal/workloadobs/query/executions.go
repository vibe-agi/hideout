package query

import (
	"context"
	"sort"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func (service *Service) Executions(
	ctx context.Context,
	input ExecutionsQuery,
) (ExecutionsResult, error) {
	if err := checkContext(ctx); err != nil {
		return ExecutionsResult{}, err
	}
	query, err := normalizeExecutionsQuery(input)
	if err != nil {
		return ExecutionsResult{}, err
	}
	snapshot, err := service.loadSnapshot(ctx, query.Owner)
	if err != nil {
		return ExecutionsResult{}, err
	}
	executions := make(map[string]workloadtypes.Execution, len(snapshot.Executions))
	children := make(map[string][]string, len(snapshot.Executions))
	counts := make(map[string]map[string]uint64, len(snapshot.Executions))
	for _, execution := range snapshot.Executions {
		if query.SessionID != "" && execution.SessionID != query.SessionID {
			continue
		}
		executions[execution.ID] = execution
		counts[execution.ID] = make(map[string]uint64)
	}
	for _, execution := range snapshot.Executions {
		if _, exists := executions[execution.ID]; !exists {
			continue
		}
		if _, exists := executions[execution.ParentExecutionID]; exists {
			children[execution.ParentExecutionID] = append(
				children[execution.ParentExecutionID], execution.ID,
			)
		}
	}
	for _, record := range snapshot.Records {
		if query.SessionID != "" && record.SessionID != query.SessionID {
			continue
		}
		executionID := primaryExecutionID(record)
		if _, exists := executions[executionID]; !exists {
			continue
		}
		counts[executionID][record.Kind] = saturatingCount(
			counts[executionID][record.Kind], record.Count,
		)
	}
	for parent := range children {
		sort.Slice(children[parent], func(left, right int) bool {
			return lessExecution(
				executions[children[parent][left]],
				executions[children[parent][right]],
			)
		})
	}

	var build func(string, bool) ExecutionNode
	build = func(id string, parentUnavailable bool) ExecutionNode {
		node := ExecutionNode{
			Execution:         cloneExecution(executions[id]),
			ActivityCounts:    cloneCounts(counts[id]),
			ParentUnavailable: parentUnavailable,
			Children:          make([]ExecutionNode, 0, len(children[id])),
		}
		for _, childID := range children[id] {
			node.Children = append(node.Children, build(childID, false))
		}
		return node
	}

	result := ExecutionsResult{
		Roots: make([]ExecutionNode, 0),
		Coverage: coverageForSubsystem(
			snapshot.Coverage, query.SessionID,
			workloadtypes.SubsystemProcess,
		),
	}
	if query.ID != "" {
		execution, exists := executions[query.ID]
		if !exists {
			return ExecutionsResult{}, ErrExecutionNotFound
		}
		parentUnavailable := execution.ParentExecutionID != ""
		if _, exists := executions[execution.ParentExecutionID]; exists {
			parentUnavailable = false
		}
		result.Roots = []ExecutionNode{build(query.ID, parentUnavailable)}
		return result, nil
	}
	for _, execution := range snapshot.Executions {
		if _, exists := executions[execution.ID]; !exists {
			continue
		}
		_, parentPresent := executions[execution.ParentExecutionID]
		if execution.ParentExecutionID != "" && parentPresent {
			continue
		}
		result.Roots = append(result.Roots, build(
			execution.ID,
			execution.ParentExecutionID != "" && !parentPresent,
		))
	}
	return result, nil
}

func primaryExecutionID(record workloadtypes.ActivityRecord) string {
	if record.Actor != nil {
		return record.Actor.ExecutionID
	}
	if record.Mediator != nil {
		return record.Mediator.ExecutionID
	}
	if subject, ok := record.Subject.(workloadtypes.ProcessSubject); ok {
		return subject.ExecutionID
	}
	return ""
}

func cloneCounts(values map[string]uint64) map[string]uint64 {
	cloned := make(map[string]uint64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func coverageForSubsystem(
	intervals []workloadtypes.CoverageInterval,
	sessionID string,
	subsystem string,
) []workloadtypes.CoverageInterval {
	result := make([]workloadtypes.CoverageInterval, 0)
	for _, interval := range intervals {
		if interval.Subsystem == subsystem &&
			(sessionID == "" || interval.SessionID == sessionID) {
			result = append(result, cloneCoverage(interval))
		}
	}
	return result
}
