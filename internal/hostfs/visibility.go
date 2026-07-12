package hostfs

import (
	"path/filepath"
	"strings"
)

const MaxDiscoverDepth = 32

type VisibilityState string

const (
	VisibilityHidden         VisibilityState = "hidden"
	VisibilityExact          VisibilityState = "exact-visible"
	VisibilityEnumerable     VisibilityState = "enumerable"
	VisibilityContentGranted VisibilityState = "content-granted"
)

type VisibilityResult struct {
	State            VisibilityState
	ExplicitDomain   bool
	Enumerable       bool
	ReservedHidden   bool
	DiscoverDenied   bool
	DepthExceeded    bool
	RuleID           string
	Source           Source
	Root             string
	RelativeDepth    int
	ContentDecision  Decision
	DiscoverDecision Decision
}

func (p EffectivePolicy) Visibility(hostPath string) VisibilityResult {
	clean, err := cleanHostPath(hostPath)
	if err != nil {
		return VisibilityResult{State: VisibilityHidden}
	}
	policyPath := visibilityPolicyPath(clean)
	if pathInReservedRoots(clean, p.ReservedRoots) || pathInReservedRoots(policyPath, p.ReservedRoots) {
		return VisibilityResult{State: VisibilityHidden, ReservedHidden: true}
	}

	content := combinedDecision(p.Decide(OpStat, clean), p.Decide(OpStat, policyPath))
	discover := combinedDecision(p.Decide(OpDiscover, clean), p.Decide(OpDiscover, policyPath))
	explicit := discover.Effect == "allow" || discover.Effect == "deny"
	if content.Allowed {
		return VisibilityResult{
			State:            VisibilityContentGranted,
			ExplicitDomain:   explicit,
			DiscoverDenied:   discover.Effect == "deny",
			RuleID:           content.RuleID,
			Source:           content.Source,
			ContentDecision:  content,
			DiscoverDecision: discover,
		}
	}
	if discover.Effect == "deny" {
		return VisibilityResult{
			State:            VisibilityHidden,
			ExplicitDomain:   true,
			DiscoverDenied:   true,
			RuleID:           discover.RuleID,
			Source:           discover.Source,
			ContentDecision:  content,
			DiscoverDecision: discover,
		}
	}
	if !discover.Allowed {
		return VisibilityResult{State: VisibilityHidden, ContentDecision: content, DiscoverDecision: discover}
	}

	result := VisibilityResult{
		State:            VisibilityExact,
		ExplicitDomain:   true,
		RuleID:           discover.RuleID,
		Source:           discover.Source,
		ContentDecision:  content,
		DiscoverDecision: discover,
	}
	grant, ok := p.discoverGrant(discover)
	if !ok {
		return result
	}
	result.Root = grant.HostPath
	switch grant.Scope {
	case ScopeDir:
		if policyPath == grant.HostPath {
			result.State = VisibilityEnumerable
			result.Enumerable = true
		}
	case ScopeRecursiveDir:
		depth := relativeDepth(grant.HostPath, policyPath)
		result.RelativeDepth = depth
		if depth > MaxDiscoverDepth {
			result.State = VisibilityHidden
			result.DepthExceeded = true
			return result
		}
		result.State = VisibilityEnumerable
		result.Enumerable = true
	}
	return result
}

func combinedDecision(requested, canonical Decision) Decision {
	if requested.Effect == "deny" {
		return requested
	}
	if canonical.Effect == "deny" {
		return canonical
	}
	if requested.Allowed {
		return requested
	}
	if canonical.Allowed {
		return canonical
	}
	return requested
}

func visibilityPolicyPath(path string) string {
	parent := filepath.Dir(path)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return path
	}
	return filepath.Join(resolvedParent, filepath.Base(path))
}

func (p EffectivePolicy) discoverGrant(decision Decision) (Grant, bool) {
	if !decision.Allowed {
		return Grant{}, false
	}
	for _, grant := range p.Grants {
		if grant.ID == decision.RuleID && grant.Source == decision.Source && opAllowed(grant.Rule, OpDiscover) {
			return grant, true
		}
	}
	return Grant{}, false
}

func relativeDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return MaxDiscoverDepth + 1
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}
