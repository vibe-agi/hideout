package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	doctorpkg "github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func (a app) addDoctorActivityDiagnostic(
	req doctorpkg.Request,
	store profile.Store,
	p profile.Profile,
	builder *doctorpkg.Builder,
) {
	retention := activityRetentionForProfile(p)
	facts := activityPrivacyFacts(retention)
	query := manager.OperatorSnapshotQuery{
		Profile:       req.Profile,
		ActivityLimit: 1,
	}
	statusCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	snapshot, err := a.fetchActivitySnapshot(statusCtx, store.Root, query)
	cancel()
	if err != nil {
		facts = append(facts, "authoritativeSnapshot=unavailable")
		addDoctorFeatureFinding(
			builder,
			"feature-activity",
			"activity",
			doctorpkg.StatusWarn,
			"workload observation cannot be proved because the authenticated daemon snapshot is unavailable",
			facts,
			[]string{
				"the daemon may be stopped, unreachable, or using an incompatible observation schema",
			},
			[]string{
				"real Lima loss, quota, cleanup, and redaction evidence is required before a full observation claim",
			},
			[]string{
				"hideout daemon start",
				"hideout activity coverage",
				"scripts/gates/workload-privacy-lima.sh",
			},
		)
		return
	}

	facts = append(facts, "authoritativeSnapshot=available")
	current := currentActivityCoverage(snapshot.Coverage)
	available, partial, unavailable, dropped, retentionGaps :=
		summarizeCurrentActivityCoverage(current)
	missing := missingActivityCoverageSubsystems(current)
	facts = append(facts,
		fmt.Sprintf("currentCoverageIntervals=%d", len(current)),
		fmt.Sprintf("currentCoverageAvailable=%d", available),
		fmt.Sprintf("currentCoveragePartial=%d", partial),
		fmt.Sprintf("currentCoverageUnavailable=%d", unavailable),
		fmt.Sprintf("currentCoverageMissingSubsystems=%d", missing),
		fmt.Sprintf("currentCoverageDroppedEvents=%d", dropped),
		fmt.Sprintf("currentCoverageRetentionGaps=%d", retentionGaps),
	)

	status := doctorpkg.StatusPass
	summary := fmt.Sprintf(
		"workload observation is visible with %d current coverage intervals",
		len(current),
	)
	candidates := []string(nil)
	if len(current) == 0 {
		status = doctorpkg.StatusSkipped
		summary = "no scoped workload coverage interval is available; no absence-of-activity claim is made"
		candidates = append(
			candidates,
			"no retained workload exists in this profile or its observer has not published coverage",
		)
	} else if partial > 0 || unavailable > 0 || missing > 0 ||
		dropped > 0 || retentionGaps > 0 {
		status = doctorpkg.StatusWarn
		summary = "workload observation has incomplete coverage; empty event results are not proof of no behavior"
		candidates = append(
			candidates,
			"one or more workload subsystems are Partial, Unavailable, missing, or report known loss",
		)
	}

	retentionFacts, retentionWarn := activityRetentionSnapshotFacts(snapshot)
	facts = append(facts, retentionFacts...)
	if retentionWarn {
		if status != doctorpkg.StatusWarn {
			status = doctorpkg.StatusWarn
			summary = "workload observation is available but retained evidence needs attention"
		}
		candidates = append(
			candidates,
			"activity evidence is pruned, corrupt, at a quota boundary, or its store limit is unavailable",
		)
	}

	addDoctorFeatureFinding(
		builder,
		"feature-activity",
		"activity",
		status,
		summary,
		facts,
		candidates,
		[]string{
			"real Lima loss, quota, cleanup, and redaction evidence is required before a full observation claim",
		},
		[]string{
			"hideout activity coverage",
			"hideout activity summary",
			"scripts/gates/workload-privacy-lima.sh",
		},
	)
}

func activityRetentionForProfile(
	p profile.Profile,
) workloadtypes.ActivityRetentionPolicy {
	retention := workloadtypes.DefaultActivityRetentionPolicy()
	if p.Activity != nil {
		retention = p.Activity.Retention
	}
	return retention
}

func activityPrivacyFacts(
	retention workloadtypes.ActivityRetentionPolicy,
) []string {
	maxAge := fmt.Sprintf("%ds", retention.MaxAgeSeconds)
	if retention.MaxAgeSeconds == 0 {
		maxAge = "owner-lifecycle"
	}
	return []string{
		"observationScope=" + workloadtypes.ActivityObservationScope,
		"retainedData=command-exec,file-open-read-write-metadata,process-network-ip-port,dns-query",
		"notCaptured=" + strings.Join(
			workloadtypes.ActivityExcludedData(),
			",",
		),
		"localPathVisibility=" + workloadtypes.ActivityLocalPathVisibility,
		"shareableSupport=activity-records-and-raw-paths-excluded",
		"shareableExport=explicit-review-and-deterministic-redaction-required",
		"coverageNonClaim=no-events-does-not-prove-no-behavior;require-Available-for-subsystem-and-window",
		"retentionOwner=" + workloadtypes.ActivityRetentionOwner,
		"retentionLifecycle=" + workloadtypes.ActivityRetentionLifecycle,
		fmt.Sprintf("desiredOwnerLimitBytes=%d", retention.MaxBytes),
		"desiredMaxAge=" + maxAge,
		fmt.Sprintf(
			"defaultGlobalSafetyLimitBytes=%d",
			workloadstore.DefaultGlobalBytes,
		),
		fmt.Sprintf(
			"defaultActiveSegmentAllowanceBytes=%d",
			workloadstore.DefaultActiveSegmentBytes,
		),
		"ttlPruning=sealed-segment-granularity;active-segment-is-a-bounded-safety-allowance",
	}
}

func currentActivityCoverage(
	intervals []workloadtypes.CoverageInterval,
) []workloadtypes.CoverageInterval {
	current := make(map[string]workloadtypes.CoverageInterval)
	for _, interval := range intervals {
		key := interval.Owner.Key() + "\x00" +
			interval.SessionID + "\x00" + interval.Subsystem
		existing, found := current[key]
		if !found || interval.StartedAt.After(existing.StartedAt) {
			current[key] = interval
		}
	}
	out := make([]workloadtypes.CoverageInterval, 0, len(current))
	for _, interval := range current {
		out = append(out, interval)
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].Owner.Key() + out[i].SessionID + out[i].Subsystem
		right := out[j].Owner.Key() + out[j].SessionID + out[j].Subsystem
		return left < right
	})
	return out
}

func summarizeCurrentActivityCoverage(
	intervals []workloadtypes.CoverageInterval,
) (
	available int,
	partial int,
	unavailable int,
	dropped uint64,
	retentionGaps int,
) {
	for _, interval := range intervals {
		switch interval.State {
		case workloadtypes.CoverageAvailable:
			available++
		case workloadtypes.CoveragePartial:
			partial++
		case workloadtypes.CoverageUnavailable:
			unavailable++
		default:
			unavailable++
		}
		dropped += interval.DroppedEventCount
		if interval.RetentionGap {
			retentionGaps++
		}
	}
	return
}

func missingActivityCoverageSubsystems(
	intervals []workloadtypes.CoverageInterval,
) int {
	workloads := make(map[string]map[string]bool)
	for _, interval := range intervals {
		key := interval.Owner.Key() + "\x00" + interval.SessionID
		if workloads[key] == nil {
			workloads[key] = make(map[string]bool)
		}
		workloads[key][interval.Subsystem] = true
	}
	required := []string{
		workloadtypes.SubsystemProcess,
		workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork,
		workloadtypes.SubsystemDNS,
	}
	missing := 0
	for _, subsystems := range workloads {
		for _, subsystem := range required {
			if !subsystems[subsystem] {
				missing++
			}
		}
	}
	return missing
}

func activityRetentionSnapshotFacts(
	snapshot manager.OperatorSnapshot,
) ([]string, bool) {
	facts := []string{
		fmt.Sprintf(
			"effectiveRetentionOwners=%d",
			len(snapshot.ActivityRetention),
		),
	}
	warn := false
	var (
		totalUsed          uint64
		totalLimit         uint64
		minimumHeadroom    uint64
		haveHeadroom       bool
		minimumTTLHeadroom int64
		haveTTLHeadroom    bool
		lifecycleOnly      int
		ttlOwners          int
		pruned             int
		corrupt            int
	)
	now := snapshot.GeneratedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, owner := range snapshot.ActivityRetention {
		totalUsed += owner.UsedBytes
		totalLimit += owner.LimitBytes
		headroom := uint64(0)
		if owner.LimitBytes > owner.UsedBytes {
			headroom = owner.LimitBytes - owner.UsedBytes
		}
		if !haveHeadroom || headroom < minimumHeadroom {
			minimumHeadroom = headroom
			haveHeadroom = true
		}
		if owner.LimitBytes == 0 || owner.UsedBytes >= owner.LimitBytes {
			warn = true
		}
		if owner.MaxAgeSeconds == 0 {
			lifecycleOnly++
		} else {
			ttlOwners++
			if !owner.EarliestAt.IsZero() {
				age := int64(now.Sub(owner.EarliestAt).Seconds())
				if age < 0 {
					age = 0
				}
				headroom := owner.MaxAgeSeconds - age
				if headroom < 0 {
					headroom = 0
				}
				if !haveTTLHeadroom || headroom < minimumTTLHeadroom {
					minimumTTLHeadroom = headroom
					haveTTLHeadroom = true
				}
			}
		}
		if owner.Pruned {
			pruned++
			warn = true
		}
		if owner.Corrupt {
			corrupt++
			warn = true
		}
	}
	facts = append(facts,
		fmt.Sprintf("effectiveOwnerUsedBytes=%d", totalUsed),
		fmt.Sprintf("effectiveOwnerLimitBytes=%d", totalLimit),
		fmt.Sprintf("lifecycleOnlyOwners=%d", lifecycleOnly),
		fmt.Sprintf("ttlOwners=%d", ttlOwners),
		fmt.Sprintf("prunedOwners=%d", pruned),
		fmt.Sprintf("corruptOwners=%d", corrupt),
	)
	if haveHeadroom {
		facts = append(
			facts,
			fmt.Sprintf("minimumOwnerHeadroomBytes=%d", minimumHeadroom),
		)
	} else {
		facts = append(facts, "minimumOwnerHeadroomBytes=unavailable")
	}
	if haveTTLHeadroom {
		facts = append(
			facts,
			fmt.Sprintf(
				"minimumApproxTTLHeadroomSeconds=%d",
				minimumTTLHeadroom,
			),
		)
	} else {
		facts = append(
			facts,
			"minimumApproxTTLHeadroomSeconds=owner-lifecycle-or-unavailable",
		)
	}

	store := snapshot.ActivityStoreRetention
	if store == nil {
		facts = append(facts, "globalStoreRetention=unavailable")
		return facts, true
	}
	globalHeadroom := uint64(0)
	if store.LimitBytes > store.UsedBytes {
		globalHeadroom = store.LimitBytes - store.UsedBytes
	}
	if store.UsedBytes >= store.LimitBytes {
		warn = true
	}
	facts = append(facts,
		fmt.Sprintf("globalUsedBytes=%d", store.UsedBytes),
		fmt.Sprintf("globalLimitBytes=%d", store.LimitBytes),
		fmt.Sprintf("globalHeadroomBytes=%d", globalHeadroom),
		fmt.Sprintf(
			"activeSegmentAllowanceBytes=%d",
			store.ActiveSegmentBytes,
		),
		fmt.Sprintf("globalOwners=%d", store.Owners),
		fmt.Sprintf("globalSegments=%d", store.Segments),
	)
	return facts, warn
}
