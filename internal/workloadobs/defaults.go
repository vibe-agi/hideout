// Package workloadobs owns the release-frozen defaults shared by workload
// observation collectors, aggregation, retention, and explainable risk rules.
package workloadobs

import "time"

const (
	// DefaultsVersion changes only when a measured release candidate adopts a
	// different observation-volume or risk-classification default.
	DefaultsVersion = "v1"

	// DefaultFileEventAggregationWindow bounds the synchronous observer-side
	// coalescing of repetitive, non-destructive file I/O before normalization.
	DefaultFileEventAggregationWindow = 500 * time.Millisecond

	// DefaultActivityAggregationWindow is inclusive and anchored at the first
	// normalized observation in a semantic group. Destructive file operations,
	// process lifecycle, risk, and coverage records are never merged.
	DefaultActivityAggregationWindow = 5 * time.Second
	DefaultActivityAggregationInputs = 65536

	// Activity storage is private and exact-owner scoped. Quota enforcement may
	// temporarily exceed a sealed-data limit by at most one active segment.
	DefaultActivityActiveSegmentBytes int64 = 8 << 20
	DefaultActivityPerOwnerBytes      int64 = 256 << 20
	DefaultActivityGlobalBytes        int64 = 1 << 30

	// Zero means lifecycle-only retention; it is not an infinite-data promise
	// because owner and global byte quotas still apply.
	DefaultActivityRetentionMaxAgeSeconds int64 = 0

	// Risk rules deliberately trigger on the first matching observation. The
	// engine reports observed behavior separately from policy disposition.
	DefaultRiskRuleSetVersion           = "v1"
	DefaultRiskRuleVersion              = "v1"
	DefaultRiskMinimumEvidenceCount     = uint64(1)
	DefaultRiskPrivilegedUID            = uint32(0)
	DefaultRiskOutsideWorkspaceSeverity = "high"
	DefaultRiskDestructiveFileSeverity  = "high"
	DefaultRiskRootExecutionSeverity    = "critical"
)
