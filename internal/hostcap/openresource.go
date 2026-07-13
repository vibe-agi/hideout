package hostcap

import (
	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

// GrantScope is the complete immutable authority identity for one elevated
// host-app launch. A grant for one recipe, alias, run, environment, workspace,
// session, or profile cannot authorize another.
type GrantScope struct {
	SessionID, Profile, RunID, WorkspaceID, EnvironmentID string
	PackID, RevisionID, BindingID, QualifiedAppRef        string
	BindingDigest, Command                                string
}

// GrantChecker reports whether an active trusted-host-app grant exists for the
// exact immutable launch scope. Implemented by Manager over Decision Center.
type GrantChecker interface {
	TrustedGrantActive(scope GrantScope) bool
}

// ResourceGrantChecker is the resource-aware extension used by community
// host-app bindings. It keeps the legacy checker API available while allowing
// an approval to be restricted to the Core-derived workspace or HostFS class.
type ResourceGrantChecker interface {
	TrustedGrantActiveForResource(scope GrantScope, resource ResourceRef) bool
}

// TrustedGrantActiveForResource fails closed when a checker has not adopted
// the resource-aware contract. Callers on the immutable binding path must use
// this helper after resolving the resource class.
func TrustedGrantActiveForResource(checker GrantChecker, scope GrantScope, resource ResourceRef) bool {
	resourceChecker, ok := checker.(ResourceGrantChecker)
	return ok && resourceChecker.TrustedGrantActiveForResource(scope, resource)
}

// PublicResourceClass is the only resource classification permitted in
// decisions, inspection, audit, and export-safe evidence.
func PublicResourceClass(kind ResourceKind) string {
	switch kind {
	case KindWorkspace:
		return "workspace"
	case KindHostFS:
		return "hostfs-portal"
	default:
		return ""
	}
}

// GrantScopeForBinding completes a run-owned scope from one immutable binding.
// Broker and provider use the same construction so request metadata cannot
// select a different app, package revision, binding, or command.
func GrantScopeForBinding(base GrantScope, binding OpenResourceBinding, command, sessionID, profile, runID string) GrantScope {
	base.SessionID, base.Profile, base.RunID = sessionID, profile, runID
	base.PackID, base.RevisionID, base.BindingID = binding.PackID, binding.RevisionID, binding.BindingID
	base.QualifiedAppRef, base.BindingDigest, base.Command = binding.QualifiedAppRef, binding.BindingDigest, command
	return base
}

// Deduper suppresses rapid identical opens so an agent cannot flood host
// windows. Reserve is transactional: failed launches release their key, while
// successful launches commit it for the remainder of the dedup window.
type Deduper interface {
	Reserve(key string) bool
	Commit(key string)
	Release(key string)
}

// OpenResult is the Core-internal outcome. Argv/HostTarget are for audit and
// tests inside Core; they contain the host path and MUST NOT be serialized back
// to the guest. Only Outcome and (on failure) the recovery Code cross to guest.
type OpenResult struct {
	Outcome    string // "launched" | "refused"
	Mode       appopen.Mode
	Argv       []string
	HostTarget string
	Suppressed bool // true when a dedup window suppressed a duplicate launch
}

const outcomeLaunched = "launched"
