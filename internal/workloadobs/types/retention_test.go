package types

import (
	"errors"
	"testing"
)

func TestActivityRetentionPolicyDistinguishesLifecycleFromInvalidBounds(
	t *testing.T,
) {
	if policy := DefaultActivityRetentionPolicy(); policy.Validate() != nil ||
		policy.MaxAgeSeconds != 0 {
		t.Fatalf("default lifecycle retention=%+v", policy)
	}
	for _, policy := range []ActivityRetentionPolicy{
		{
			MaxBytes:      MinimumActivityRetentionMaxBytes - 1,
			MaxAgeSeconds: 0,
		},
		{
			MaxBytes:      DefaultActivityRetentionMaxBytes,
			MaxAgeSeconds: -1,
		},
		{
			MaxBytes: DefaultActivityRetentionMaxBytes,
			MaxAgeSeconds: MaximumActivityRetentionMaxAgeSeconds +
				1,
		},
	} {
		if err := policy.Validate(); !errors.Is(
			err,
			ErrActivityRetentionPolicy,
		) {
			t.Fatalf("invalid policy=%+v err=%v", policy, err)
		}
	}
}
