package render

import (
	"fmt"
	"math"
	"strings"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

// activityCoverageHUDLines turns coverage and retention evidence into operator
// language. It intentionally distinguishes a known drop count from other
// reduced intervals whose missing event count cannot be measured.
func activityCoverageHUDLines(input ActivityInput) []string {
	lines := []string{activityCoverageSummary(input)}
	intervals := selectedActivityCoverage(input)
	if len(intervals) > 0 {
		var degraded int
		var dropped uint64
		for _, interval := range intervals {
			if interval.State != workloadtypes.CoverageAvailable ||
				interval.RetentionGap ||
				interval.DroppedEventCount > 0 {
				degraded++
			}
			if math.MaxUint64-dropped < interval.DroppedEventCount {
				dropped = math.MaxUint64
			} else {
				dropped += interval.DroppedEventCount
			}
		}
		history := fmt.Sprintf(
			"history %d intervals · %d reduced",
			len(intervals),
			degraded,
		)
		if dropped > 0 {
			history += fmt.Sprintf(" · %d events known lost", dropped)
		}
		lines = append(lines, history)
	}

	summary := input.Data.Summary
	retained := summary.RetainedRange
	quota := summary.Quota
	if !retained.From.IsZero() || !retained.To.IsZero() ||
		quota.UsedBytes > 0 || quota.LimitBytes > 0 {
		storage := "stored evidence"
		if !retained.From.IsZero() && !retained.To.IsZero() {
			storage += " " + coverageTime(retained.From) +
				" → " + coverageTime(retained.To)
		} else {
			storage += " range unavailable"
		}
		if quota.LimitBytes > 0 {
			percent := float64(quota.UsedBytes) * 100 /
				float64(quota.LimitBytes)
			storage += fmt.Sprintf(
				" · %s of %s (%.0f%%)",
				activityByteSize(quota.UsedBytes),
				activityByteSize(quota.LimitBytes),
				percent,
			)
		} else if quota.UsedBytes > 0 {
			storage += " · " + activityByteSize(quota.UsedBytes) +
				" used · limit unavailable"
		}
		lines = append(lines, storage)
	}

	var limits []string
	if summary.Pruned {
		limits = append(limits, "pruned")
	}
	if summary.Corrupt {
		limits = append(limits, "corrupt")
	}
	if len(summary.Reasons) > 0 {
		reasons := make([]string, 0, len(summary.Reasons))
		for _, reason := range summary.Reasons {
			if value := sanitizeInline(reason); value != "" {
				reasons = append(reasons, value)
			}
		}
		if len(reasons) > 0 {
			limits = append(limits, "reasons "+strings.Join(reasons, ", "))
		}
	}
	if len(limits) > 0 {
		lines = append(lines, "evidence limits "+strings.Join(limits, " · "))
	}
	return lines
}

func activityCoverageHUDExtraLines(input ActivityInput) int {
	lines := activityCoverageHUDLines(input)
	if len(lines) <= 1 {
		return 0
	}
	return len(lines) - 1
}

func coverageTime(value time.Time) string {
	return value.UTC().Format("Jan 02 15:04:05")
}

func activityByteSize(value uint64) string {
	const unit = uint64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor := float64(unit)
	suffix := "KiB"
	for _, candidate := range []string{"MiB", "GiB", "TiB"} {
		if float64(value) < divisor*float64(unit) {
			break
		}
		divisor *= float64(unit)
		suffix = candidate
	}
	return fmt.Sprintf("%.1f %s", float64(value)/divisor, suffix)
}
