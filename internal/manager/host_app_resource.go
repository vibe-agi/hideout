package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostfs/readgrant"
)

type hostAppRunResourceAuthority struct {
	*hostFSReadProvider
	profile string
}

func newHostAppRunResourceAuthority(provider *hostFSReadProvider, profileName string) *hostAppRunResourceAuthority {
	return &hostAppRunResourceAuthority{hostFSReadProvider: provider, profile: strings.TrimSpace(profileName)}
}

func (a *hostAppRunResourceAuthority) ValidateHostAppResource(check hostfs.HostAppResourceCheck) error {
	if a == nil || a.hostFSReadProvider == nil || a.profile == "" || check.Owner.Profile != a.profile {
		return hostfs.ErrHostAppResourceOwner
	}
	return a.hostFSReadProvider.ValidateHostAppResource(check)
}

func (a *hostAppRunResourceAuthority) HostAppResourceAuthorityLost(owner hostfs.HostAppResourceOwner, reason string) {
	if a == nil || a.hostFSReadProvider == nil || owner.Profile != a.profile {
		return
	}
	a.hostFSReadProvider.HostAppResourceAuthorityLost(owner, reason)
}

// ValidateHostAppResource is the Manager-owned side of HostFS resource
// consumption. It proves the live session owner and rechecks mutable profile
// policy; it never creates or broadens a HostFS grant.
func (p *hostFSReadProvider) ValidateHostAppResource(check hostfs.HostAppResourceCheck) error {
	if p == nil || p.store == nil || check.Owner.SessionID != p.sessionID || strings.TrimSpace(check.Owner.Profile) == "" {
		return hostfs.ErrHostAppResourceOwner
	}
	if err := readgrant.ProbeOwner(p.ownerPath); err != nil {
		return hostfs.ErrHostAppResourceOwner
	}
	profileValue, err := p.core.Store.Load(check.Owner.Profile)
	if err != nil {
		return hostfs.ErrHostAppResourceOwner
	}
	now := time.Now().UTC()
	if p.now != nil {
		now = p.now().UTC()
	}
	runPolicy := p.policy
	runPolicy.Now = now

	// Profile grants are operator-mutable control-plane state. Rebuild that
	// source at the current time so a profile revoke or expiry takes effect for
	// an already-running app decision. Environment/run grants remain immutable
	// session snapshots and are bounded by the owner lock above.
	profilePolicy, err := hostfs.Build(hostfs.BuildInput{
		Profile: profileValue.HostFS, StoreRoot: p.core.Store.Root, Now: now,
	})
	if err != nil {
		return hostfs.ErrHostAppResourceUnauthorized
	}
	profilePolicy = hostfs.NewService(profilePolicy).Policy
	profilePolicy.Now = now
	currentPolicy := hostAppPolicyWithCurrentProfile(runPolicy, profilePolicy)
	if !check.DynamicRead && !hostAppResourceDecisionsCurrent(currentPolicy, check) {
		return hostfs.ErrHostAppResourceUnauthorized
	}
	if check.DynamicRead && !hostAppDynamicVisibilityCurrent(currentPolicy, check) {
		return hostfs.ErrHostAppResourceUnauthorized
	}
	if !hostAppProfileAuthorityCurrent(runPolicy, profilePolicy, check) {
		return hostfs.ErrHostAppResourceUnauthorized
	}
	if check.ResourceType == hostfs.HostAppResourceTree && !currentPolicy.AuthorizesHostAppTree(check.CanonicalPath) {
		return hostfs.ErrHostAppResourceUnauthorized
	}
	return nil
}

func hostAppPolicyWithCurrentProfile(runPolicy, profilePolicy hostfs.EffectivePolicy) hostfs.EffectivePolicy {
	current := runPolicy
	current.Grants = append([]hostfs.Grant(nil), profilePolicy.Grants...)
	for _, grant := range runPolicy.Grants {
		if grant.Source != hostfs.SourceProfile {
			current.Grants = append(current.Grants, grant)
		}
	}
	current.Deny = append([]hostfs.Grant(nil), profilePolicy.Deny...)
	for _, deny := range runPolicy.Deny {
		if deny.Source != hostfs.SourceProfile {
			current.Deny = append(current.Deny, deny)
		}
	}
	return current
}

func (p *hostFSReadProvider) AllowsHostAppResourceRead(check hostfs.ReadGrantCheck) (bool, error) {
	allowed, err := p.AllowsRead(check)
	if err == nil {
		return allowed, nil
	}
	if p == nil || readgrant.ProbeOwner(p.ownerPath) != nil {
		return false, hostfs.ErrHostAppResourceOwner
	}
	return false, hostfs.ErrHostAppResourceUnauthorized
}

func hostAppResourceDecisionsCurrent(policy hostfs.EffectivePolicy, check hostfs.HostAppResourceCheck) bool {
	requested := policy.Decide(check.Operation, check.RequestedPath)
	canonical := policy.Decide(check.Operation, check.CanonicalPath)
	if requested.Effect == "deny" || canonical.Effect == "deny" {
		return false
	}
	return hostAppDecisionEquivalent(check.RequestedDecision, requested) && hostAppDecisionEquivalent(check.CanonicalDecision, canonical)
}

func hostAppDecisionEquivalent(expected, current hostfs.Decision) bool {
	if expected.Allowed != current.Allowed || expected.Effect != current.Effect {
		return false
	}
	if !expected.Allowed {
		return expected.Effect != "deny"
	}
	return expected.RuleID == current.RuleID && expected.Source == current.Source
}

func hostAppDynamicVisibilityCurrent(policy hostfs.EffectivePolicy, check hostfs.HostAppResourceCheck) bool {
	requested := policy.Visibility(check.RequestedPath)
	canonical := policy.Visibility(check.CanonicalPath)
	return requested.ExplicitDomain && requested.State != hostfs.VisibilityHidden && !requested.DiscoverDenied &&
		canonical.ExplicitDomain && canonical.State != hostfs.VisibilityHidden && !canonical.DiscoverDenied &&
		policy.Decide(hostfs.OpRead, check.RequestedPath).Effect != "deny" && policy.Decide(hostfs.OpRead, check.CanonicalPath).Effect != "deny"
}

func hostAppProfileAuthorityCurrent(runPolicy, profilePolicy hostfs.EffectivePolicy, check hostfs.HostAppResourceCheck) bool {
	if check.DynamicRead {
		for _, pair := range []struct {
			path     string
			expected hostfs.VisibilityResult
		}{
			{check.RequestedPath, runPolicy.Visibility(check.RequestedPath)},
			{check.CanonicalPath, runPolicy.Visibility(check.CanonicalPath)},
		} {
			if pair.expected.Source != hostfs.SourceProfile {
				continue
			}
			current := profilePolicy.Visibility(pair.path)
			if !current.ExplicitDomain || current.State == hostfs.VisibilityHidden || current.DiscoverDenied || current.RuleID != pair.expected.RuleID || current.Source != hostfs.SourceProfile {
				return false
			}
		}
		return true
	}
	for _, pair := range []struct {
		path     string
		expected hostfs.Decision
	}{
		{check.RequestedPath, check.RequestedDecision},
		{check.CanonicalPath, check.CanonicalDecision},
	} {
		if !pair.expected.Allowed || pair.expected.Source != hostfs.SourceProfile {
			continue
		}
		current := profilePolicy.Decide(check.Operation, pair.path)
		if !current.Allowed || current.RuleID != pair.expected.RuleID || current.Source != hostfs.SourceProfile {
			return false
		}
	}
	return true
}

// HostAppResourceAuthorityLost makes an already-approved app decision
// unusable after final HostFS owner/policy/canonical revalidation fails.
func (p *hostFSReadProvider) HostAppResourceAuthorityLost(owner hostfs.HostAppResourceOwner, reason string) {
	if p == nil || owner.SessionID != p.sessionID || strings.TrimSpace(owner.Profile) == "" {
		return
	}
	if reason == "" {
		reason = "hostfs-resource-authority-lost"
	}
	_ = p.core.invalidateProjectionGrantsForSession(owner.Profile, owner.SessionID, reason)
}

// hostAppRunForbiddenRoots assembles the production launch-time overlap
// boundary from Manager-owned state. Mutable package state is consulted only
// while compiling the immutable run; final checks retain those source roots.
func (c Core) hostAppRunForbiddenRoots(runSession RunSession, policy hostfs.EffectivePolicy) ([]string, error) {
	roots, err := c.hostAppRunLiveForbiddenRoots(runSession, policy)
	if err != nil {
		return nil, err
	}
	sources, err := c.hostAppCatalogSources(runSession.Plan.ProfileName)
	if err != nil {
		return nil, fmt.Errorf("resolve host-app source overlap roots: %w", err)
	}
	for _, source := range sources {
		roots = append(roots, source.revision.Source.LocalPath)
		if filepath.IsAbs(source.revision.Source.URL) {
			roots = append(roots, source.revision.Source.URL)
		}
	}
	return canonicalHostAppRunForbiddenRoots(roots)
}

func (c Core) hostAppRunLiveForbiddenRoots(runSession RunSession, policy hostfs.EffectivePolicy) ([]string, error) {
	roots := []string{
		c.Store.Root,
		os.TempDir(),
		runSession.Plan.Workspace,
		runSession.Layout.Root,
		runSession.Layout.Dir,
		runSession.Layout.TmpDir,
		runSession.Layout.ShimDir,
		runSession.Layout.BrokerSock,
		runSession.Layout.BrokerEndpointPath,
		runSession.Layout.HostFSReadDir,
		runSession.ProfileDir,
		runSession.IdentityDir,
		runSession.RuntimeSessionDir,
		runSession.RuntimeShimDir,
		runSession.Environment.Record.Workspace,
	}
	if filepath.IsAbs(runSession.AuditPath) {
		roots = append(roots, runSession.AuditPath)
	}
	for _, key := range []string{"HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "GIT_CONFIG_GLOBAL"} {
		roots = append(roots, runSession.Env.Synthetic[key])
	}
	for _, grant := range policy.Grants {
		if !grant.Overlay {
			continue
		}
		root := grant.HostPath
		if grant.Scope == hostfs.ScopeGlob {
			root = hostFSGlobGraftBase(root)
		}
		roots = append(roots, root)
	}
	if c.HostAppForbiddenRoots != nil {
		active, err := c.HostAppForbiddenRoots(runSession.Plan.ProfileName)
		if err != nil {
			return nil, fmt.Errorf("resolve active host-app forbidden roots: %w", err)
		}
		roots = append(roots, active...)
	}
	return canonicalHostAppRunForbiddenRoots(roots)
}

func (c Core) hostAppRunIdentityRevalidator(runSession RunSession, policy hostfs.EffectivePolicy, initialRoots []string) hostcap.IdentityRevalidator {
	initialRoots = append([]string(nil), initialRoots...)
	return func(expectation hostcap.ApplicationExpectation, previous hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
		liveRoots, err := c.hostAppRunLiveForbiddenRoots(runSession, policy)
		if err != nil {
			return hostcap.ObservedApplicationIdentity{}, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "launch-time application overlap boundary is unavailable"}
		}
		roots, err := canonicalHostAppRunForbiddenRoots(append(append([]string(nil), initialRoots...), liveRoots...))
		if err != nil {
			return hostcap.ObservedApplicationIdentity{}, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "launch-time application overlap boundary is invalid"}
		}
		return c.hostAppRevalidator(roots)(expectation, previous)
	}
}

func canonicalHostAppRunForbiddenRoots(roots []string) ([]string, error) {
	seen := map[string]bool{}
	for _, raw := range roots {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !filepath.IsAbs(raw) {
			return nil, fmt.Errorf("host-app forbidden root must be absolute: %q", raw)
		}
		root := filepath.Clean(raw)
		if canonical, err := filepath.EvalSymlinks(root); err == nil {
			root = canonical
		}
		seen[root] = true
	}
	out := make([]string, 0, len(seen))
	for root := range seen {
		out = append(out, root)
	}
	sort.Strings(out)
	return out, nil
}

var _ hostfs.HostAppResourceValidator = (*hostFSReadProvider)(nil)
var _ hostfs.HostAppResourceReadAuthority = (*hostFSReadProvider)(nil)
var _ hostfs.HostAppResourceLossReporter = (*hostFSReadProvider)(nil)
var _ hostfs.HostAppResourceValidator = (*hostAppRunResourceAuthority)(nil)
var _ hostfs.HostAppResourceReadAuthority = (*hostAppRunResourceAuthority)(nil)
var _ hostfs.HostAppResourceLossReporter = (*hostAppRunResourceAuthority)(nil)
