package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func (store *Store) loadStateLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
) (ownerPersistentState, error) {
	path := filepath.Join(ownerRoot, ownerStateFile)
	data, err := readPrivateFile(path, 8<<20)
	if errors.Is(err, os.ErrNotExist) {
		return ownerPersistentState{
			Schema: ownerStateSchema, OwnerKey: owner.Key(),
			Reasons: []string{}, Gaps: []workloadtypes.CoverageInterval{},
			PendingPruneIDs: []string{},
		}, nil
	}
	if err != nil {
		return ownerPersistentState{}, err
	}
	var state ownerPersistentState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return ownerPersistentState{}, errors.Join(ErrStoreCorrupt, err)
	}
	if err := validateOwnerState(state, owner); err != nil {
		return ownerPersistentState{}, err
	}
	return state, nil
}

func (store *Store) saveStateLocked(
	owner workloadtypes.ActivityOwner,
	ownerRoot string,
	state ownerPersistentState,
) error {
	normalizeOwnerState(&state)
	if err := validateOwnerState(state, owner); err != nil {
		return err
	}
	_, err := writeAtomicJSON(ownerRoot, ownerStateFile, state)
	return err
}

func validateOwnerState(
	state ownerPersistentState,
	owner workloadtypes.ActivityOwner,
) error {
	if state.Schema != ownerStateSchema ||
		state.OwnerKey != owner.Key() ||
		len(state.Gaps) > maxStateGaps ||
		len(state.Reasons) > 256 ||
		len(state.PendingPruneIDs) > maxStateGaps {
		return ErrStoreCorrupt
	}
	if state.Pruned && !slices.Contains(state.Reasons, RetentionReasonPruned) {
		return ErrStoreCorrupt
	}
	if state.Corrupt && !slices.Contains(state.Reasons, RetentionReasonCorrupt) {
		return ErrStoreCorrupt
	}
	if !slices.IsSorted(state.Reasons) ||
		!slices.IsSorted(state.PendingPruneIDs) {
		return ErrStoreCorrupt
	}
	for index, reason := range state.Reasons {
		if reason == "" || index > 0 && reason == state.Reasons[index-1] {
			return ErrStoreCorrupt
		}
	}
	for index, segmentID := range state.PendingPruneIDs {
		if !validSegmentID(segmentID) ||
			index > 0 && segmentID == state.PendingPruneIDs[index-1] {
			return ErrStoreCorrupt
		}
	}
	for _, gap := range state.Gaps {
		if !gap.Owner.Equal(owner) || gap.Validate() != nil ||
			gap.State == workloadtypes.CoverageAvailable {
			return ErrStoreCorrupt
		}
	}
	return nil
}

func normalizeOwnerState(state *ownerPersistentState) {
	if state.Reasons == nil {
		state.Reasons = []string{}
	}
	if state.Gaps == nil {
		state.Gaps = []workloadtypes.CoverageInterval{}
	}
	if state.PendingPruneIDs == nil {
		state.PendingPruneIDs = []string{}
	}
	slices.Sort(state.Reasons)
	state.Reasons = slices.Compact(state.Reasons)
	slices.Sort(state.PendingPruneIDs)
	state.PendingPruneIDs = slices.Compact(state.PendingPruneIDs)
	sort.Slice(state.Gaps, func(left, right int) bool {
		if !state.Gaps[left].StartedAt.Equal(state.Gaps[right].StartedAt) {
			return state.Gaps[left].StartedAt.Before(state.Gaps[right].StartedAt)
		}
		return state.Gaps[left].ID < state.Gaps[right].ID
	})
}

func addStateReason(state *ownerPersistentState, reason string) {
	if !slices.Contains(state.Reasons, reason) {
		state.Reasons = append(state.Reasons, reason)
	}
	switch reason {
	case RetentionReasonPruned:
		state.Pruned = true
	case RetentionReasonCorrupt:
		state.Corrupt = true
	}
}

func addCoverageGaps(
	state *ownerPersistentState,
	owner workloadtypes.ActivityOwner,
	segmentID, reason string,
	scopes []segmentScope,
) {
	existing := make(map[string]struct{}, len(state.Gaps))
	for _, gap := range state.Gaps {
		existing[gap.ID] = struct{}{}
	}
	for _, scope := range scopes {
		gap := coverageGap(owner, segmentID, reason, scope)
		if _, found := existing[gap.ID]; found {
			continue
		}
		if len(state.Gaps) >= maxStateGaps {
			mergeCoverageGap(state, gap)
			continue
		}
		state.Gaps = append(state.Gaps, gap)
		existing[gap.ID] = struct{}{}
	}
}

func coverageGap(
	owner workloadtypes.ActivityOwner,
	segmentID, reason string,
	scope segmentScope,
) workloadtypes.CoverageInterval {
	payload := owner.Key() + "\x00" + segmentID + "\x00" + reason + "\x00" +
		scope.SessionID + "\x00" + scope.Subsystem
	sum := sha256.Sum256([]byte(payload))
	id := "cov_" + base64.RawURLEncoding.EncodeToString(sum[:18])
	endedAt := scope.LastAt.UTC()
	endSequence := scope.LastSequence
	gap := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: id,
		Owner: owner, SessionID: scope.SessionID,
		Subsystem: scope.Subsystem, State: workloadtypes.CoveragePartial,
		Reason: reason, CollectorGeneration: scope.CollectorGeneration,
		StartSequence: scope.FirstSequence, EndSequence: &endSequence,
		StartedAt: scope.FirstAt.UTC(), EndedAt: &endedAt,
		Evidence: []workloadtypes.CoverageEvidence{{
			Code: "activity-segment", Value: segmentID,
		}},
	}
	if reason == CoverageReasonRetentionPruned {
		gap.RetentionGap = true
	}
	return gap
}

func mergeCoverageGap(
	state *ownerPersistentState,
	next workloadtypes.CoverageInterval,
) {
	for index := range state.Gaps {
		current := &state.Gaps[index]
		if current.SessionID != next.SessionID ||
			current.Subsystem != next.Subsystem ||
			current.Reason != next.Reason {
			continue
		}
		if next.StartedAt.Before(current.StartedAt) {
			current.StartedAt = next.StartedAt
			current.StartSequence = next.StartSequence
		}
		if next.EndedAt != nil &&
			(current.EndedAt == nil || next.EndedAt.After(*current.EndedAt)) {
			value := *next.EndedAt
			current.EndedAt = &value
			if next.EndSequence != nil {
				sequence := *next.EndSequence
				current.EndSequence = &sequence
			}
		}
		return
	}
}

func buildSegmentScopes(entries []segmentEntry) []segmentScope {
	type scopeKey struct {
		sessionID string
		subsystem string
	}
	coverageByID := make(map[string]workloadtypes.CoverageInterval)
	for _, entry := range entries {
		if entry.Kind == entryCoverage && entry.Coverage != nil {
			coverageByID[entry.Coverage.ID] = *entry.Coverage
		}
	}
	scopes := make(map[scopeKey]segmentScope)
	for _, entry := range entries {
		var (
			sessionID  string
			subsystem  string
			generation uint64 = 1
		)
		firstAt, lastAt := entry.timeRange()
		firstSequence, lastSequence := entry.sequences()
		switch entry.Kind {
		case entryActivity:
			sessionID = entry.Activity.SessionID
			subsystem = activitySubsystem(entry.Activity.Kind)
			if coverage, found := coverageByID[entry.Activity.CoverageID]; found {
				generation = coverage.CollectorGeneration
			}
		case entryExecution:
			sessionID = entry.Execution.SessionID
			subsystem = workloadtypes.SubsystemProcess
			generation = entry.Execution.ObserverGeneration
		case entryCoverage:
			sessionID = entry.Coverage.SessionID
			subsystem = entry.Coverage.Subsystem
			generation = entry.Coverage.CollectorGeneration
		case entryRisk:
			continue
		}
		if sessionID == "" || subsystem == "" ||
			firstAt.IsZero() || lastAt.IsZero() {
			continue
		}
		key := scopeKey{sessionID: sessionID, subsystem: subsystem}
		current, exists := scopes[key]
		if !exists {
			scopes[key] = segmentScope{
				SessionID: sessionID, Subsystem: subsystem,
				CollectorGeneration: max(generation, 1),
				FirstSequence:       firstSequence, LastSequence: lastSequence,
				FirstAt: firstAt.UTC(), LastAt: lastAt.UTC(),
			}
			continue
		}
		current.CollectorGeneration = max(current.CollectorGeneration, generation, 1)
		if firstAt.Before(current.FirstAt) {
			current.FirstAt = firstAt.UTC()
		}
		if lastAt.After(current.LastAt) {
			current.LastAt = lastAt.UTC()
		}
		// Kernel and transport sequences are independently monotonic per CPU.
		// A later event from another CPU may therefore carry a smaller sequence.
		// Segment retention scopes are conservative bounds, not a fabricated
		// single-CPU timeline: compute their sequence range independently of the
		// wall-clock range.
		if current.FirstSequence == 0 ||
			firstSequence != 0 && firstSequence < current.FirstSequence {
			current.FirstSequence = firstSequence
		}
		if lastSequence > current.LastSequence {
			current.LastSequence = lastSequence
		}
		scopes[key] = current
	}
	result := make([]segmentScope, 0, len(scopes))
	for _, scope := range scopes {
		result = append(result, scope)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].SessionID != result[right].SessionID {
			return result[left].SessionID < result[right].SessionID
		}
		return result[left].Subsystem < result[right].Subsystem
	})
	return result
}

func extendSegmentScopesThrough(scopes []segmentScope, through time.Time) {
	if through.IsZero() {
		return
	}
	through = through.UTC()
	for index := range scopes {
		if scopes[index].LastAt.Before(through) {
			scopes[index].LastAt = through
		}
	}
}

func validateSegmentScope(
	scope segmentScope,
	owner workloadtypes.ActivityOwner,
) error {
	if scope.SessionID == "" || scope.Subsystem == "" ||
		scope.CollectorGeneration == 0 ||
		scope.FirstAt.IsZero() || scope.LastAt.Before(scope.FirstAt) ||
		scope.LastSequence < scope.FirstSequence {
		return ErrStoreCorrupt
	}
	endedAt := scope.LastAt.UTC()
	endSequence := scope.LastSequence
	probe := workloadtypes.CoverageInterval{
		Schema:    workloadtypes.CoverageIntervalSchema,
		ID:        "cov_scope_validation",
		Owner:     owner,
		SessionID: scope.SessionID, Subsystem: scope.Subsystem,
		State:               workloadtypes.CoveragePartial,
		Reason:              CoverageReasonStoreCorrupt,
		CollectorGeneration: scope.CollectorGeneration,
		StartSequence:       scope.FirstSequence,
		EndSequence:         &endSequence,
		StartedAt:           scope.FirstAt.UTC(),
		EndedAt:             &endedAt,
	}
	if err := probe.ValidateForOwner(owner); err != nil {
		return ErrStoreCorrupt
	}
	return nil
}

func activitySubsystem(kind string) string {
	switch kind {
	case workloadtypes.ActivityProcess:
		return workloadtypes.SubsystemProcess
	case workloadtypes.ActivityFile:
		return workloadtypes.SubsystemFile
	case workloadtypes.ActivityConnection:
		return workloadtypes.SubsystemNetwork
	case workloadtypes.ActivityDNS:
		return workloadtypes.SubsystemDNS
	default:
		return ""
	}
}
