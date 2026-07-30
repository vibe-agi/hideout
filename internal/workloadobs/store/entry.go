package store

import (
	"errors"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	entryActivity  = "activity"
	entryExecution = "execution"
	entryCoverage  = "coverage"
	entryRisk      = "risk"
)

type segmentEntry struct {
	Schema    string                          `json:"schema"`
	Kind      string                          `json:"kind"`
	Activity  *workloadtypes.ActivityRecord   `json:"activity,omitempty"`
	Execution *workloadtypes.Execution        `json:"execution,omitempty"`
	Coverage  *workloadtypes.CoverageInterval `json:"coverage,omitempty"`
	Risk      *risk.Finding                   `json:"risk,omitempty"`
}

func activityEntry(record workloadtypes.ActivityRecord) segmentEntry {
	value := record
	return segmentEntry{
		Schema: segmentEntrySchema, Kind: entryActivity, Activity: &value,
	}
}

func executionEntry(execution workloadtypes.Execution) segmentEntry {
	value := execution
	return segmentEntry{
		Schema: segmentEntrySchema, Kind: entryExecution, Execution: &value,
	}
}

func coverageEntry(interval workloadtypes.CoverageInterval) segmentEntry {
	value := interval
	return segmentEntry{
		Schema: segmentEntrySchema, Kind: entryCoverage, Coverage: &value,
	}
}

func riskEntry(finding risk.Finding) segmentEntry {
	value := finding
	return segmentEntry{
		Schema: segmentEntrySchema, Kind: entryRisk, Risk: &value,
	}
}

func (entry segmentEntry) validate(owner workloadtypes.ActivityOwner) error {
	if entry.Schema != segmentEntrySchema {
		return ErrStoreCorrupt
	}
	switch entry.Kind {
	case entryActivity:
		if entry.Activity == nil || entry.Execution != nil ||
			entry.Coverage != nil || entry.Risk != nil {
			return ErrStoreCorrupt
		}
		if !entry.Activity.Owner.Equal(owner) {
			return workloadtypes.ErrOwnerMismatch
		}
		if err := entry.Activity.ValidatePersistable(); err != nil {
			return errors.Join(ErrRecordNotPersistable, err)
		}
	case entryExecution:
		if entry.Execution == nil || entry.Activity != nil ||
			entry.Coverage != nil || entry.Risk != nil {
			return ErrStoreCorrupt
		}
		if !entry.Execution.Owner.Equal(owner) {
			return workloadtypes.ErrOwnerMismatch
		}
		if err := entry.Execution.Validate(); err != nil {
			return errors.Join(ErrStoreCorrupt, err)
		}
	case entryCoverage:
		if entry.Coverage == nil || entry.Activity != nil ||
			entry.Execution != nil || entry.Risk != nil {
			return ErrStoreCorrupt
		}
		if !entry.Coverage.Owner.Equal(owner) {
			return workloadtypes.ErrOwnerMismatch
		}
		if err := entry.Coverage.Validate(); err != nil {
			return errors.Join(ErrStoreCorrupt, err)
		}
	case entryRisk:
		if entry.Risk == nil || entry.Activity != nil ||
			entry.Execution != nil || entry.Coverage != nil {
			return ErrStoreCorrupt
		}
		if !entry.Risk.Owner.Equal(owner) {
			return workloadtypes.ErrOwnerMismatch
		}
		if err := entry.Risk.Validate(); err != nil {
			return errors.Join(ErrStoreCorrupt, err)
		}
	default:
		return ErrStoreCorrupt
	}
	return nil
}

func (entry segmentEntry) owner() workloadtypes.ActivityOwner {
	switch entry.Kind {
	case entryActivity:
		return entry.Activity.Owner
	case entryExecution:
		return entry.Execution.Owner
	case entryCoverage:
		return entry.Coverage.Owner
	case entryRisk:
		return entry.Risk.Owner
	default:
		return workloadtypes.ActivityOwner{}
	}
}

func (entry segmentEntry) timeRange() (time.Time, time.Time) {
	switch entry.Kind {
	case entryActivity:
		return entry.Activity.FirstAt, entry.Activity.LastAt
	case entryExecution:
		last := entry.Execution.StartedAt
		if entry.Execution.Exit != nil && entry.Execution.Exit.At.After(last) {
			last = entry.Execution.Exit.At
		}
		return entry.Execution.StartedAt, last
	case entryCoverage:
		last := entry.Coverage.StartedAt
		if entry.Coverage.EndedAt != nil {
			last = *entry.Coverage.EndedAt
		}
		return entry.Coverage.StartedAt, last
	case entryRisk:
		return entry.Risk.FirstAt, entry.Risk.LastAt
	default:
		return time.Time{}, time.Time{}
	}
}

func (entry segmentEntry) sequences() (uint64, uint64) {
	switch entry.Kind {
	case entryActivity:
		return entry.Activity.FirstSequence, entry.Activity.LastSequence
	case entryExecution:
		return entry.Execution.ExecSequence, entry.Execution.ExecSequence
	case entryCoverage:
		last := entry.Coverage.StartSequence
		if entry.Coverage.EndSequence != nil {
			last = *entry.Coverage.EndSequence
		}
		return entry.Coverage.StartSequence, last
	default:
		return 0, 0
	}
}
