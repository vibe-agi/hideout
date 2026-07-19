package workspaceattach

import (
	"testing"
	"time"
)

func TestSelectedCachePolicyMatchesMeasuredPortalContract(t *testing.T) {
	policy := SelectedCachePolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.EntryTTL != 60*time.Second || policy.AttributeTTL != 60*time.Second || policy.NegativeTTL != 0 {
		t.Fatalf("unexpected selected cache policy: %#v", policy)
	}
	if policy.ConvergenceBound != 250*time.Millisecond || !policy.RequireNotifyLoss {
		t.Fatalf("selected invalidation contract drifted: %#v", policy)
	}
}

func TestCachePolicyRejectsStaleNegativeOrBestEffortNotificationModes(t *testing.T) {
	valid := SelectedCachePolicy()
	for name, mutate := range map[string]func(*CachePolicy){
		"negative cache":  func(policy *CachePolicy) { policy.NegativeTTL = time.Second },
		"unbounded entry": func(policy *CachePolicy) { policy.EntryTTL = 0 },
		"slow convergence": func(policy *CachePolicy) {
			policy.ConvergenceBound = 2 * time.Second
		},
		"best effort notify": func(policy *CachePolicy) { policy.RequireNotifyLoss = false },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("expected invalid policy %#v to be rejected", candidate)
			}
		})
	}
}
