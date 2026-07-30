package query

import (
	"sort"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := snapshot
	cloned.Records = make([]workloadtypes.ActivityRecord, len(snapshot.Records))
	for index := range snapshot.Records {
		cloned.Records[index] = cloneRecord(snapshot.Records[index])
	}
	cloned.Executions = make([]workloadtypes.Execution, len(snapshot.Executions))
	for index := range snapshot.Executions {
		cloned.Executions[index] = cloneExecution(snapshot.Executions[index])
	}
	cloned.Coverage = make([]workloadtypes.CoverageInterval, len(snapshot.Coverage))
	for index := range snapshot.Coverage {
		cloned.Coverage[index] = cloneCoverage(snapshot.Coverage[index])
	}
	cloned.Risks = make([]risk.Finding, len(snapshot.Risks))
	for index := range snapshot.Risks {
		cloned.Risks[index] = cloneRisk(snapshot.Risks[index])
	}
	cloned.Retention.Reasons = append([]string(nil), snapshot.Retention.Reasons...)
	return cloned
}

func cloneRecord(record workloadtypes.ActivityRecord) workloadtypes.ActivityRecord {
	cloned := record
	cloned.FirstAt = normalizeTime(record.FirstAt)
	cloned.LastAt = normalizeTime(record.LastAt)
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
	switch subject := record.Subject.(type) {
	case workloadtypes.ProcessSubject:
		subject.Argv = append([]string(nil), subject.Argv...)
		cloned.Subject = subject
	case *workloadtypes.ProcessSubject:
		if subject != nil {
			value := *subject
			value.Argv = append([]string(nil), subject.Argv...)
			cloned.Subject = value
		}
	case workloadtypes.FileSubject:
		cloned.Subject = subject
	case *workloadtypes.FileSubject:
		if subject != nil {
			cloned.Subject = *subject
		}
	case workloadtypes.NetworkSubject:
		cloned.Subject = subject
	case *workloadtypes.NetworkSubject:
		if subject != nil {
			cloned.Subject = *subject
		}
	case workloadtypes.DNSSubject:
		subject.Answers = append([]string(nil), subject.Answers...)
		cloned.Subject = subject
	case *workloadtypes.DNSSubject:
		if subject != nil {
			value := *subject
			value.Answers = append([]string(nil), subject.Answers...)
			cloned.Subject = value
		}
	case workloadtypes.GenericSubject:
		cloned.Subject = subject
	case *workloadtypes.GenericSubject:
		if subject != nil {
			cloned.Subject = *subject
		}
	}
	return cloned
}

func cloneExecution(execution workloadtypes.Execution) workloadtypes.Execution {
	cloned := execution
	cloned.StartedAt = normalizeTime(execution.StartedAt)
	cloned.Argv = append([]string(nil), execution.Argv...)
	cloned.Limitations = append([]string(nil), execution.Limitations...)
	if execution.Exit != nil {
		value := *execution.Exit
		value.At = normalizeTime(value.At)
		if execution.Exit.Code != nil {
			code := *execution.Exit.Code
			value.Code = &code
		}
		cloned.Exit = &value
	}
	return cloned
}

func cloneCoverage(interval workloadtypes.CoverageInterval) workloadtypes.CoverageInterval {
	cloned := interval
	cloned.StartedAt = normalizeTime(interval.StartedAt)
	cloned.Evidence = append([]workloadtypes.CoverageEvidence(nil), interval.Evidence...)
	if interval.EndSequence != nil {
		value := *interval.EndSequence
		cloned.EndSequence = &value
	}
	if interval.EndedAt != nil {
		value := normalizeTime(*interval.EndedAt)
		cloned.EndedAt = &value
	}
	return cloned
}

func cloneRisk(finding risk.Finding) risk.Finding {
	cloned := finding
	cloned.FirstAt = normalizeTime(finding.FirstAt)
	cloned.LastAt = normalizeTime(finding.LastAt)
	cloned.EvidenceRefs = append([]string(nil), finding.EvidenceRefs...)
	return cloned
}
