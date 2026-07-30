package liveconsole

import (
	"encoding/json"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func cloneProfileProjections(values []manager.ProfileProjection) []manager.ProfileProjection {
	return cloneJSONSlice(values)
}

func cloneTransitions(values []TransitionProjection) []TransitionProjection {
	return cloneJSONSlice(values)
}

func cloneOperations(values []manager.Operation) []manager.Operation {
	return cloneJSONSlice(values)
}

func cloneActivityProjection(value ActivityProjection) ActivityProjection {
	cloned := value
	cloned.Counts = append([]ActivityCount(nil), value.Counts...)
	cloned.Recent = nil
	if len(value.Recent) > 0 {
		cloned.Recent = make([]workloadtypes.ActivityRecord, len(value.Recent))
		for index, record := range value.Recent {
			cloned.Recent[index] = cloneActivityRecord(record)
		}
	}
	return cloned
}

func cloneActivityDelta(value ActivityProjectionDelta) ActivityProjectionDelta {
	cloned := value
	cloned.Counts = append([]ActivityCount(nil), value.Counts...)
	return cloned
}

func cloneActivityRecord(record workloadtypes.ActivityRecord) workloadtypes.ActivityRecord {
	cloned := record
	if record.Actor != nil {
		actor := *record.Actor
		cloned.Actor = &actor
	}
	if record.Mediator != nil {
		mediator := *record.Mediator
		cloned.Mediator = &mediator
	}
	if record.Outcome.Code != nil {
		code := *record.Outcome.Code
		cloned.Outcome.Code = &code
	}
	cloned.Truncation = append([]string(nil), record.Truncation...)
	switch subject := record.Subject.(type) {
	case workloadtypes.ProcessSubject:
		copy := subject
		copy.Argv = append([]string(nil), subject.Argv...)
		cloned.Subject = copy
	case *workloadtypes.ProcessSubject:
		if subject != nil {
			copy := *subject
			copy.Argv = append([]string(nil), subject.Argv...)
			cloned.Subject = &copy
		}
	case workloadtypes.DNSSubject:
		copy := subject
		copy.Answers = append([]string(nil), subject.Answers...)
		cloned.Subject = copy
	case *workloadtypes.DNSSubject:
		if subject != nil {
			copy := *subject
			copy.Answers = append([]string(nil), subject.Answers...)
			cloned.Subject = &copy
		}
	case workloadtypes.FileSubject, workloadtypes.NetworkSubject, workloadtypes.GenericSubject:
		cloned.Subject = subject
	case *workloadtypes.FileSubject:
		if subject != nil {
			copy := *subject
			cloned.Subject = &copy
		}
	case *workloadtypes.NetworkSubject:
		if subject != nil {
			copy := *subject
			cloned.Subject = &copy
		}
	case *workloadtypes.GenericSubject:
		if subject != nil {
			copy := *subject
			cloned.Subject = &copy
		}
	default:
		data, err := json.Marshal(subject)
		if err == nil {
			var copy any
			if json.Unmarshal(data, &copy) == nil {
				cloned.Subject = copy
			}
		}
	}
	return cloned
}

func cloneCoverage(values []workloadtypes.CoverageInterval) []workloadtypes.CoverageInterval {
	cloned := make([]workloadtypes.CoverageInterval, len(values))
	for index, interval := range values {
		cloned[index] = interval
		cloned[index].Evidence = append([]workloadtypes.CoverageEvidence(nil), interval.Evidence...)
		if interval.EndSequence != nil {
			value := *interval.EndSequence
			cloned[index].EndSequence = &value
		}
		if interval.EndedAt != nil {
			value := *interval.EndedAt
			cloned[index].EndedAt = &value
		}
	}
	return cloned
}

func cloneActivityRetention(
	values []manager.OperatorActivityRetentionProjection,
) []manager.OperatorActivityRetentionProjection {
	cloned := make(
		[]manager.OperatorActivityRetentionProjection,
		len(values),
	)
	for index, value := range values {
		cloned[index] = value
		cloned[index].Reasons = append([]string(nil), value.Reasons...)
	}
	return cloned
}

func cloneActivityStoreRetention(
	value *manager.OperatorActivityStoreRetentionProjection,
) *manager.OperatorActivityStoreRetentionProjection {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneRisks(values []RiskFinding) []RiskFinding {
	cloned := make([]RiskFinding, len(values))
	for index, finding := range values {
		cloned[index] = finding
		cloned[index].EvidenceRefs = append([]string(nil), finding.EvidenceRefs...)
	}
	return cloned
}

func cloneCapabilities(values []CapabilityProjection) []CapabilityProjection {
	cloned := make([]CapabilityProjection, len(values))
	for index, capability := range values {
		cloned[index] = capability
		cloned[index].ActionRefs = append([]string(nil), capability.ActionRefs...)
	}
	return cloned
}

// These projections consist only of JSON domain types. A round trip gives the
// client projection ownership of nested maps and slices. Invalid values are
// omitted instead of sharing mutable authoritative state.
func cloneJSONSlice[T any](values []T) []T {
	if len(values) == 0 {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	var cloned []T
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	return cloned
}
