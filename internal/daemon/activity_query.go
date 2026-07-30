package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type daemonActivityQuerySource struct {
	activity   *activityService
	persistent *workloadstore.Store
}

func newDaemonActivityProvider(
	activity *activityService,
	instanceID, operatorToken string,
) (manager.ActivityProvider, error) {
	return newDaemonActivityProviderWithStore(
		activity, nil, instanceID, operatorToken,
	)
}

func newDaemonActivityProviderWithStore(
	activity *activityService,
	persistent *workloadstore.Store,
	instanceID, operatorToken string,
) (manager.ActivityProvider, error) {
	source := &daemonActivityQuerySource{
		activity: activity, persistent: persistent,
	}
	key := sha256.Sum256([]byte(
		"hideout.activity-query-cursor/v1\x00" +
			instanceID + "\x00" + operatorToken,
	))
	service, err := workloadquery.NewService(workloadquery.Options{
		Source: source, CursorKey: key[:],
	})
	clear(key[:])
	if err != nil {
		return nil, err
	}
	return manager.ActivityService{
		OwnerResolver: source,
		Query:         service,
	}, nil
}

// daemonOperatorObservation projects the bounded seed view used to discover an
// exact owner before the TUI opens the richer query routes. Detailed records,
// execution trees, and findings stay on the activity query surface; until the
// collector-to-store data path is connected they remain honestly empty.
func daemonOperatorObservation(
	ctx context.Context,
	activity *activityService,
	query manager.OperatorSnapshotQuery,
) (manager.OperatorObservation, error) {
	return daemonOperatorObservationWithProvider(
		ctx,
		activity,
		nil,
		nil,
		query,
	)
}

func daemonOperatorObservationWithProvider(
	ctx context.Context,
	activity *activityService,
	provider manager.ActivityProvider,
	persistent *workloadstore.Store,
	query manager.OperatorSnapshotQuery,
) (manager.OperatorObservation, error) {
	if ctx == nil || activity == nil {
		return manager.OperatorObservation{}, workloadquery.ErrOwnerNotFound
	}
	if err := query.Validate(); err != nil {
		return manager.OperatorObservation{}, err
	}
	snapshots, err := activity.snapshots(ctx)
	if err != nil {
		return manager.OperatorObservation{}, err
	}
	observation := manager.OperatorObservation{
		Activity:          []manager.ActivityProjection{},
		Coverage:          []manager.CoverageProjection{},
		ActivityRetention: []manager.OperatorActivityRetentionProjection{},
		Risks:             []manager.RiskFinding{},
	}
	storeRetentionAvailable := false
	if persistent != nil {
		stats, statsErr := persistent.Stats()
		if statsErr != nil {
			observation.Capabilities = append(
				observation.Capabilities,
				manager.OperatorCapabilityProjection{
					ID: "activity.retention", State: manager.OperatorCapabilityPartial,
					Provider: "host-store",
					Reason:   "activity store quota could not be projected",
					ActionRefs: []string{
						"activity.inspect", "doctor.activity",
					},
				},
			)
		} else {
			observation.ActivityStoreRetention =
				&manager.OperatorActivityStoreRetentionProjection{
					UsedBytes:              stats.UsedBytes,
					LimitBytes:             stats.LimitBytes,
					DefaultOwnerLimitBytes: stats.DefaultOwnerLimitBytes,
					ActiveSegmentBytes:     stats.ActiveSegmentBytes,
					Owners:                 stats.Owners,
					Segments:               stats.Segments,
				}
			storeRetentionAvailable = true
		}
	}
	type capabilityState struct {
		state  string
		reason string
	}
	capabilities := make(map[string]capabilityState)
	owners := make(map[string]workloadtypes.ActivityOwner)
	for _, snapshot := range snapshots {
		if query.Session != "" && snapshot.SessionID != query.Session {
			continue
		}
		owners[snapshot.Owner.Key()] = snapshot.Owner
		for _, subsystem := range activitySubsystems {
			observation.Coverage = append(
				observation.Coverage,
				snapshot.Intervals[subsystem]...,
			)
			summary, exists := snapshot.Coverage[subsystem]
			if !exists || summary.CurrentState == "" {
				continue
			}
			state := managerActivityCapabilityState(summary.CurrentState)
			current, found := capabilities[subsystem]
			if !found ||
				managerActivityCapabilityRank(state) <
					managerActivityCapabilityRank(current.state) {
				capabilities[subsystem] = capabilityState{
					state: state, reason: summary.CurrentReason,
				}
			}
		}
	}
	if provider != nil {
		ownerKeys := make([]string, 0, len(owners))
		for key := range owners {
			ownerKeys = append(ownerKeys, key)
		}
		sort.Strings(ownerKeys)
		for _, key := range ownerKeys {
			owner := owners[key]
			summary, summaryErr := provider.ActivitySummary(
				ctx,
				manager.ActivitySummaryQuery{Owner: owner},
			)
			coverageResult, coverageErr := provider.ActivityCoverage(
				ctx,
				manager.ActivityCoverageQuery{Owner: owner},
			)
			if summaryErr != nil || coverageErr != nil {
				observation.Capabilities = append(
					observation.Capabilities,
					manager.OperatorCapabilityProjection{
						ID: "activity.retention", State: manager.OperatorCapabilityPartial,
						Provider: "host-store",
						Reason:   "retained activity evidence could not be projected",
						ActionRefs: []string{
							"activity.inspect", "doctor.activity",
						},
					},
				)
				continue
			}
			observation.Coverage = append(
				observation.Coverage,
				coverageResult.Intervals...,
			)
			observation.ActivityRetention = append(
				observation.ActivityRetention,
				manager.OperatorActivityRetentionProjection{
					Owner:         owner,
					EarliestAt:    summary.RetainedRange.From,
					LatestAt:      summary.RetainedRange.To,
					UsedBytes:     summary.Quota.UsedBytes,
					LimitBytes:    summary.Quota.LimitBytes,
					MaxAgeSeconds: summary.Quota.MaxAgeSeconds,
					Pruned:        summary.Pruned,
					Corrupt:       summary.Corrupt,
					Reasons:       append([]string(nil), summary.Reasons...),
				},
			)
		}
		if len(observation.ActivityRetention) > 0 ||
			storeRetentionAvailable {
			observation.Capabilities = append(
				observation.Capabilities,
				manager.OperatorCapabilityProjection{
					ID: "activity.retention", State: manager.OperatorCapabilityAvailable,
					Provider: "host-store",
					ActionRefs: []string{
						"activity.inspect", "doctor.activity",
					},
				},
			)
		}
	}
	for _, subsystem := range activitySubsystems {
		state, exists := capabilities[subsystem]
		if !exists {
			continue
		}
		observation.Capabilities = append(
			observation.Capabilities,
			manager.OperatorCapabilityProjection{
				ID: "activity." + subsystem, State: state.state,
				Provider: "guest-observer", Reason: state.reason,
				ActionRefs: []string{"activity.inspect", "doctor.activity"},
			},
		)
	}
	coverageByID := make(
		map[string]manager.CoverageProjection,
		len(observation.Coverage),
	)
	for _, interval := range observation.Coverage {
		coverageByID[interval.ID] = interval
	}
	observation.Coverage = make(
		[]manager.CoverageProjection,
		0,
		len(coverageByID),
	)
	for _, interval := range coverageByID {
		observation.Coverage = append(observation.Coverage, interval)
	}
	sort.Slice(observation.Coverage, func(left, right int) bool {
		if observation.Coverage[left].StartedAt.Equal(
			observation.Coverage[right].StartedAt,
		) {
			return observation.Coverage[left].ID <
				observation.Coverage[right].ID
		}
		return observation.Coverage[left].StartedAt.After(
			observation.Coverage[right].StartedAt,
		)
	})
	if len(observation.Coverage) > 1024 {
		observation.Coverage = observation.Coverage[:1024]
	}
	return observation, nil
}

func managerActivityCapabilityState(coverageState string) string {
	switch coverageState {
	case workloadtypes.CoverageAvailable:
		return manager.OperatorCapabilityAvailable
	case workloadtypes.CoveragePartial:
		return manager.OperatorCapabilityPartial
	default:
		return manager.OperatorCapabilityUnavailable
	}
}

func managerActivityCapabilityRank(state string) int {
	switch state {
	case manager.OperatorCapabilityUnavailable:
		return 0
	case manager.OperatorCapabilityPartial:
		return 1
	default:
		return 2
	}
}

func (source *daemonActivityQuerySource) ResolveActivityOwner(
	ctx context.Context,
	selector manager.ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	if source == nil || source.activity == nil || selector.Validate() != nil {
		return workloadtypes.ActivityOwner{}, manager.ErrActivityOwnerNotFound
	}
	snapshots, err := source.activity.snapshots(ctx)
	if err != nil {
		return workloadtypes.ActivityOwner{}, err
	}
	for _, snapshot := range snapshots {
		if selector.SessionID != "" {
			if snapshot.SessionID == selector.SessionID &&
				snapshot.Owner.Kind == workloadtypes.OwnerDisposableSession {
				return snapshot.Owner, nil
			}
			continue
		}
		if snapshot.Owner.Kind == workloadtypes.OwnerReusableEnvironment &&
			snapshot.Owner.EnvironmentID == selector.EnvironmentID &&
			snapshot.Owner.BackendIncarnationID == selector.BackendIncarnationID {
			return snapshot.Owner, nil
		}
	}
	if source.persistent != nil {
		owners, err := source.persistent.Owners(ctx)
		if err != nil {
			return workloadtypes.ActivityOwner{}, err
		}
		for _, owner := range owners {
			if selector.SessionID != "" {
				if owner.Kind == workloadtypes.OwnerDisposableSession &&
					owner.SessionID == selector.SessionID {
					return owner, nil
				}
				continue
			}
			if owner.Kind == workloadtypes.OwnerReusableEnvironment &&
				owner.EnvironmentID == selector.EnvironmentID &&
				owner.BackendIncarnationID ==
					selector.BackendIncarnationID {
				return owner, nil
			}
		}
	}
	return workloadtypes.ActivityOwner{}, manager.ErrActivityOwnerNotFound
}

func (source *daemonActivityQuerySource) Snapshot(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (workloadquery.Snapshot, error) {
	if source == nil || source.activity == nil || owner.Validate() != nil {
		return workloadquery.Snapshot{}, workloadquery.ErrOwnerNotFound
	}
	snapshots, err := source.activity.snapshots(ctx)
	if err != nil {
		return workloadquery.Snapshot{}, err
	}
	matched := make([]activitySessionSnapshot, 0)
	for _, snapshot := range snapshots {
		if snapshot.Owner.Equal(owner) {
			matched = append(matched, snapshot)
		}
	}
	var persisted workloadquery.Snapshot
	persistedFound := false
	if source.persistent != nil {
		persisted, err = source.persistent.Snapshot(ctx, owner)
		switch {
		case err == nil:
			persistedFound = true
		case errors.Is(err, workloadquery.ErrOwnerNotFound):
		default:
			return workloadquery.Snapshot{}, err
		}
	}
	if len(matched) == 0 {
		if persistedFound {
			return persisted, nil
		}
		return workloadquery.Snapshot{}, workloadquery.ErrOwnerNotFound
	}
	sort.Slice(matched, func(left, right int) bool {
		return matched[left].SessionID < matched[right].SessionID
	})
	encoded, err := json.Marshal(matched)
	if err != nil {
		return workloadquery.Snapshot{}, errors.Join(
			workloadquery.ErrInvalidSnapshot, err,
		)
	}
	digest := sha256.Sum256(append(
		[]byte("hideout.activity-query-snapshot/v1\x00"), encoded...,
	))
	revision := "rev_" + base64.RawURLEncoding.EncodeToString(digest[:18])

	result := workloadquery.Snapshot{
		Revision: revision, Owner: owner,
		Records:    []workloadtypes.ActivityRecord{},
		Executions: []workloadtypes.Execution{},
		Coverage:   make([]workloadtypes.CoverageInterval, 0),
		Risks:      []workloadrisk.Finding{},
		Retention:  workloadquery.RetentionState{Reasons: []string{}},
	}
	reasons := make(map[string]struct{})
	for _, snapshot := range matched {
		for _, subsystem := range activitySubsystems {
			for _, interval := range snapshot.Intervals[subsystem] {
				result.Coverage = append(
					result.Coverage, cloneDaemonActivityCoverage(interval),
				)
				observeDaemonRetention(&result.Retention, interval)
				if interval.RetentionGap {
					result.Retention.Pruned = true
					reasons["retention-pruned"] = struct{}{}
				}
			}
		}
	}
	for reason := range reasons {
		result.Retention.Reasons = append(result.Retention.Reasons, reason)
	}
	sort.Strings(result.Retention.Reasons)
	if persistedFound {
		result = mergeDaemonActivitySnapshots(persisted, result)
	}
	return result, nil
}

func mergeDaemonActivitySnapshots(
	persisted, live workloadquery.Snapshot,
) workloadquery.Snapshot {
	result := persisted
	result.Owner = live.Owner
	coverageByID := make(
		map[string]workloadtypes.CoverageInterval,
		len(persisted.Coverage)+len(live.Coverage),
	)
	for _, interval := range persisted.Coverage {
		coverageByID[interval.ID] = cloneDaemonActivityCoverage(interval)
	}
	for _, interval := range live.Coverage {
		coverageByID[interval.ID] = cloneDaemonActivityCoverage(interval)
	}
	result.Coverage = make(
		[]workloadtypes.CoverageInterval, 0, len(coverageByID),
	)
	for _, interval := range coverageByID {
		result.Coverage = append(result.Coverage, interval)
		observeDaemonRetention(&result.Retention, interval)
	}
	sort.Slice(result.Coverage, func(left, right int) bool {
		if !result.Coverage[left].StartedAt.Equal(
			result.Coverage[right].StartedAt,
		) {
			return result.Coverage[left].StartedAt.Before(
				result.Coverage[right].StartedAt,
			)
		}
		return result.Coverage[left].ID < result.Coverage[right].ID
	})
	reasons := make(map[string]struct{})
	for _, reason := range persisted.Retention.Reasons {
		reasons[reason] = struct{}{}
	}
	for _, reason := range live.Retention.Reasons {
		reasons[reason] = struct{}{}
	}
	result.Retention.Pruned = persisted.Retention.Pruned ||
		live.Retention.Pruned
	result.Retention.Corrupt = persisted.Retention.Corrupt ||
		live.Retention.Corrupt
	result.Retention.Reasons = make([]string, 0, len(reasons))
	for reason := range reasons {
		result.Retention.Reasons = append(result.Retention.Reasons, reason)
	}
	sort.Strings(result.Retention.Reasons)
	revisionSeed := result
	revisionSeed.Revision = ""
	encoded, _ := json.Marshal(revisionSeed)
	digest := sha256.Sum256(append(
		[]byte("hideout.activity-query-merged/v1\x00"), encoded...,
	))
	result.Revision = "rev_" +
		base64.RawURLEncoding.EncodeToString(digest[:18])
	return result
}

func (service *activityService) snapshots(
	ctx context.Context,
) ([]activitySessionSnapshot, error) {
	if service == nil {
		return nil, workloadquery.ErrOwnerNotFound
	}
	if ctx == nil {
		return nil, workloadquery.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mu.RLock()
	sessions := make([]*activitySession, 0, len(service.sessions))
	for _, session := range service.sessions {
		sessions = append(sessions, session)
	}
	service.mu.RUnlock()
	result := make([]activitySessionSnapshot, 0, len(sessions))
	for index, session := range sessions {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		result = append(result, session.snapshot())
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].SessionID < result[right].SessionID
	})
	return result, nil
}

func observeDaemonRetention(
	retention *workloadquery.RetentionState,
	interval workloadtypes.CoverageInterval,
) {
	if retention == nil {
		return
	}
	first := interval.StartedAt
	last := interval.StartedAt
	if interval.EndedAt != nil {
		last = *interval.EndedAt
	}
	if retention.EarliestAt.IsZero() || first.Before(retention.EarliestAt) {
		retention.EarliestAt = first.Round(0).UTC()
	}
	if retention.LatestAt.IsZero() || last.After(retention.LatestAt) {
		retention.LatestAt = last.Round(0).UTC()
	}
}

func cloneDaemonActivityCoverage(
	interval workloadtypes.CoverageInterval,
) workloadtypes.CoverageInterval {
	cloned := interval
	cloned.StartedAt = interval.StartedAt.Round(0).UTC()
	cloned.Evidence = append(
		[]workloadtypes.CoverageEvidence(nil), interval.Evidence...,
	)
	if interval.EndSequence != nil {
		value := *interval.EndSequence
		cloned.EndSequence = &value
	}
	if interval.EndedAt != nil {
		value := interval.EndedAt.Round(0).UTC()
		cloned.EndedAt = &value
	}
	return cloned
}
