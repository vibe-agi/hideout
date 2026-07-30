package profilechanges

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type activityRetentionValue struct {
	MaxBytes      int64 `json:"maxBytes"`
	MaxAgeSeconds int64 `json:"maxAgeSeconds"`
}

func normalizeActivityRetention(
	raw json.RawMessage,
) (activityRetentionValue, error) {
	var value activityRetentionValue
	if err := decodeStrict(raw, &value); err != nil {
		return activityRetentionValue{}, err
	}
	if (workloadtypes.ActivityRetentionPolicy{
		MaxBytes:      value.MaxBytes,
		MaxAgeSeconds: value.MaxAgeSeconds,
	}).Validate() != nil {
		return activityRetentionValue{}, errors.New(
			"activity retention bounds are invalid",
		)
	}
	return value, nil
}

func applyActivityRetention(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, error) {
	value, err := normalizeActivityRetention(raw)
	if err != nil {
		return nil, err
	}
	before := formatActivityRetention(
		workloadtypes.DefaultActivityRetentionPolicy(),
	) + " (default)"
	if desired.Activity != nil {
		before = formatActivityRetention(desired.Activity.Retention)
	}
	desired.Activity = &profile.ActivityConfig{
		Retention: profile.ActivityRetention{
			MaxBytes:      value.MaxBytes,
			MaxAgeSeconds: value.MaxAgeSeconds,
		},
	}
	return []Diff{{
		Kind:   KindActivityRetention,
		Field:  "activity.retention",
		Before: before,
		After: formatActivityRetention(
			workloadtypes.ActivityRetentionPolicy{
				MaxBytes:      value.MaxBytes,
				MaxAgeSeconds: value.MaxAgeSeconds,
			},
		),
		Scope: "activity-owner",
	}}, nil
}

func formatActivityRetention(
	policy workloadtypes.ActivityRetentionPolicy,
) string {
	bytes := fmt.Sprintf("%d bytes", policy.MaxBytes)
	for _, unit := range []struct {
		bytes int64
		name  string
	}{
		{bytes: 1 << 30, name: "GiB"},
		{bytes: 1 << 20, name: "MiB"},
		{bytes: 1 << 10, name: "KiB"},
	} {
		if policy.MaxBytes >= unit.bytes &&
			policy.MaxBytes%unit.bytes == 0 {
			bytes = fmt.Sprintf(
				"%d %s",
				policy.MaxBytes/unit.bytes,
				unit.name,
			)
			break
		}
	}
	age := "VM lifecycle"
	if policy.MaxAgeSeconds > 0 {
		const day = int64(24 * time.Hour / time.Second)
		const hour = int64(time.Hour / time.Second)
		switch {
		case policy.MaxAgeSeconds%day == 0:
			days := policy.MaxAgeSeconds / day
			age = fmt.Sprintf("%d days", days)
			if days == 1 {
				age = "1 day"
			}
		case policy.MaxAgeSeconds%hour == 0:
			hours := policy.MaxAgeSeconds / hour
			age = fmt.Sprintf("%d hours", hours)
			if hours == 1 {
				age = "1 hour"
			}
		default:
			age = fmt.Sprintf(
				"%d seconds",
				policy.MaxAgeSeconds,
			)
		}
	}
	return bytes + " / " + age
}
