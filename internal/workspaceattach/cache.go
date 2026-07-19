package workspaceattach

import (
	"errors"
	"time"
)

// CachePolicy is the measured Portal/FUSE cache contract selected by Phase R.
// Long positive caching is safe only while every host mutation is paired with
// an invalidation event; negative entries are never cached.
type CachePolicy struct {
	EntryTTL          time.Duration
	AttributeTTL      time.Duration
	NegativeTTL       time.Duration
	ConvergenceBound  time.Duration
	RequireNotifyLoss bool
}

func SelectedCachePolicy() CachePolicy {
	return CachePolicy{
		EntryTTL:          60 * time.Second,
		AttributeTTL:      60 * time.Second,
		NegativeTTL:       0,
		ConvergenceBound:  250 * time.Millisecond,
		RequireNotifyLoss: true,
	}
}

func (policy CachePolicy) Validate() error {
	if policy.EntryTTL <= 0 || policy.AttributeTTL <= 0 {
		return errors.New("workspace cache requires bounded positive entry and attribute TTLs")
	}
	if policy.NegativeTTL != 0 {
		return errors.New("workspace cache must not retain negative lookups")
	}
	if policy.ConvergenceBound <= 0 || policy.ConvergenceBound > time.Second {
		return errors.New("workspace cache convergence bound is invalid")
	}
	if !policy.RequireNotifyLoss {
		return errors.New("workspace cache must fail closed when notification coherence is lost")
	}
	return nil
}
