package hostcap

import (
	"context"
	"sync"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

type BindingIdentityResolver func(OpenResourceBinding) (OpenResourceBinding, error)

// HandoffLifecycleBegin records a prospective host-app handoff before the
// launcher receives authority. The returned completion function is called
// exactly once; launched is false for refusal, suppression, or launch failure.
type HandoffLifecycleBegin func(command string) (complete func(launched bool) error, err error)

// ProjectionConfig bundles the per-run/session projection dependencies that are
// not resource-specific. The broker supplies the ResourceResolver at call time
// (it owns the host-path mapping); the manager supplies everything else.
// A nil ProjectionConfig means projection is disabled for the run.
type ProjectionConfig struct {
	Platform           Platform
	SafeUserDataDir    string
	Grants             GrantChecker
	Launcher           appopen.Launcher
	Deduper            Deduper
	Bindings           BindingCatalog
	RunID              string
	GrantScopeBase     GrantScope
	ResolveIdentity    BindingIdentityResolver
	RevalidateIdentity IdentityRevalidator
	ValidateLifecycle  func(OpenResourceBinding) error
	BeginHandoff       HandoffLifecycleBegin
	identityMu         sync.Mutex
	resolvedIdentities map[string]OpenResourceBinding
}

// OpenCommand is the 032 production entry point. Command and binding digest
// must resolve to the same immutable binding; the guest intent has no app or
// resource-kind selector.
func (c *ProjectionConfig) OpenCommand(ctx context.Context, command, bindingDigest string, request BoundOpenRequest, resolver ResourceResolver, sessionID, profile string) (OpenResult, OpenResourceBinding, error) {
	if c == nil {
		return OpenResult{}, OpenResourceBinding{}, &Error{Code: CodeProviderUnavailable, Reason: "projection is not configured for this run"}
	}
	binding, ok := c.Bindings.ResolveCommand(command)
	if !ok || binding.BindingDigest != bindingDigest {
		return OpenResult{}, OpenResourceBinding{}, &Error{Code: CodeCommandUnbound, Reason: "projected command binding is absent or stale"}
	}
	if c.ValidateLifecycle == nil {
		return OpenResult{}, binding, &Error{Code: CodeProviderUnavailable, Reason: "host-app binding lifecycle validation is unavailable"}
	}
	if err := c.ValidateLifecycle(binding); err != nil {
		if CodeOf(err) != "" {
			return OpenResult{}, binding, err
		}
		return OpenResult{}, binding, &Error{Code: CodeCommandUnbound, Reason: "projected command binding is no longer active"}
	}
	if binding.IdentityDeferred {
		resolved, resolveErr := c.resolveBindingIdentity(binding)
		if resolveErr != nil {
			if CodeOf(resolveErr) != "" {
				return OpenResult{}, binding, resolveErr
			}
			return OpenResult{}, binding, &Error{Code: CodeAppIdentityDrift, Reason: "host application identity could not be resolved for this command"}
		}
		binding = resolved
	}
	complete := func(bool) error { return nil }
	if c.BeginHandoff != nil {
		var beginErr error
		complete, beginErr = c.BeginHandoff(command)
		if beginErr != nil {
			return OpenResult{}, binding, &Error{Code: CodeProviderUnavailable, Reason: "host application lifecycle registration failed"}
		}
	}
	result, err := OpenBoundResource(ctx, binding, request, BoundOpenContext{
		SessionID: sessionID, Profile: profile, RunID: c.RunID, Command: command, SafeStateBase: c.SafeUserDataDir,
		Platform: c.Platform, Resources: resolver,
		GrantScopeBase: c.GrantScopeBase, Grants: c.Grants, Launcher: c.Launcher, Deduper: c.Deduper, RevalidateIdentity: c.RevalidateIdentity,
		ValidateLifecycle: c.ValidateLifecycle,
	})
	launched := err == nil && result.Outcome == outcomeLaunched && !result.Suppressed
	if lifecycleErr := complete(launched); lifecycleErr != nil {
		if err != nil {
			return result, binding, err
		}
		return OpenResult{}, binding, &Error{Code: CodeProviderUnavailable, Reason: "host application lifecycle completion failed"}
	}
	return result, binding, err
}

func (c *ProjectionConfig) resolveBindingIdentity(binding OpenResourceBinding) (OpenResourceBinding, error) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	if resolved, ok := c.resolvedIdentities[binding.BindingDigest]; ok {
		return cloneOpenResourceBinding(resolved), nil
	}
	if c.ResolveIdentity == nil {
		return binding, &Error{Code: CodeProviderUnavailable, Reason: "on-demand host application identity resolution is unavailable"}
	}
	resolved, err := c.ResolveIdentity(cloneOpenResourceBinding(binding))
	if err != nil {
		return binding, err
	}
	if !resolved.IdentityDeferred || resolved.BindingDigest != binding.BindingDigest || resolved.ObservedIdentityDigest == "" {
		return binding, &Error{Code: CodeAppIdentityDrift, Reason: "on-demand host application identity changed the immutable binding"}
	}
	if err := ValidateOpenResourceBinding(resolved); err != nil {
		return binding, &Error{Code: CodeAppIdentityDrift, Reason: "on-demand host application identity did not validate"}
	}
	if c.resolvedIdentities == nil {
		c.resolvedIdentities = make(map[string]OpenResourceBinding)
	}
	c.resolvedIdentities[binding.BindingDigest] = cloneOpenResourceBinding(resolved)
	return resolved, nil
}
