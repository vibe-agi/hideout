package types

import (
	"errors"

	workloadobs "github.com/vibe-agi/hideout/internal/workloadobs"
)

const (
	DefaultActivityRetentionMaxBytes            = workloadobs.DefaultActivityPerOwnerBytes
	DefaultActivityRetentionMaxAgeSeconds       = workloadobs.DefaultActivityRetentionMaxAgeSeconds
	MinimumActivityRetentionMaxBytes      int64 = 1024
	MaximumActivityRetentionMaxBytes      int64 = 10 << 30
	MaximumActivityRetentionMaxAgeSeconds int64 = 365 * 24 * 60 * 60
)

var ErrActivityRetentionPolicy = errors.New(
	"activity retention policy is invalid",
)

// ActivityRetentionPolicy is snapshotted onto one exact activity owner.
// MaxAgeSeconds == 0 means lifecycle-only retention: quota pruning and exact
// owner cleanup still apply, but no wall-clock expiry is requested.
type ActivityRetentionPolicy struct {
	MaxBytes      int64 `json:"maxBytes"`
	MaxAgeSeconds int64 `json:"maxAgeSeconds"`
}

func DefaultActivityRetentionPolicy() ActivityRetentionPolicy {
	return ActivityRetentionPolicy{
		MaxBytes:      DefaultActivityRetentionMaxBytes,
		MaxAgeSeconds: DefaultActivityRetentionMaxAgeSeconds,
	}
}

func (policy ActivityRetentionPolicy) Validate() error {
	if policy.MaxBytes < MinimumActivityRetentionMaxBytes ||
		policy.MaxBytes > MaximumActivityRetentionMaxBytes ||
		policy.MaxAgeSeconds < 0 ||
		policy.MaxAgeSeconds > MaximumActivityRetentionMaxAgeSeconds {
		return ErrActivityRetentionPolicy
	}
	return nil
}
