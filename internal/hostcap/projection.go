package hostcap

import (
	"context"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

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
	RevalidateIdentity IdentityRevalidator
	ValidateLifecycle  func(OpenResourceBinding) error
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
	result, err := OpenBoundResource(ctx, binding, request, BoundOpenContext{
		SessionID: sessionID, Profile: profile, RunID: c.RunID, Command: command, SafeStateBase: c.SafeUserDataDir,
		Platform: c.Platform, Resources: resolver,
		GrantScopeBase: c.GrantScopeBase, Grants: c.Grants, Launcher: c.Launcher, Deduper: c.Deduper, RevalidateIdentity: c.RevalidateIdentity,
		ValidateLifecycle: c.ValidateLifecycle,
	})
	return result, binding, err
}
