package manager

import (
	"context"
	"errors"
	"regexp"

	"github.com/vibe-agi/hideout/internal/environment"
)

var (
	ErrEnvironmentNetworkRuntimeUnavailable = errors.New(
		"environment network runtime is unavailable",
	)
	environmentNetworkBootPattern = regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
	)
)

// EnvironmentNetworkRuntimeRegistration is an in-memory, daemon-owned
// capability for reconfiguring one exact running guest incarnation. It is
// deliberately not serializable and exposes DNS-only operations rather than a
// general backend or shell handle.
type EnvironmentNetworkRuntimeRegistration struct {
	EnvironmentID  string
	SessionID      string
	BootID         string
	ReconfigureDNS func(
		context.Context,
		string,
		string,
	) error
	VerifyDNS func(context.Context, string) error
}

func (registration EnvironmentNetworkRuntimeRegistration) Validate() error {
	if !environment.ValidID(registration.EnvironmentID) ||
		!networkTransitionSessionPattern.MatchString(registration.SessionID) ||
		!environmentNetworkBootPattern.MatchString(registration.BootID) ||
		registration.ReconfigureDNS == nil ||
		registration.VerifyDNS == nil {
		return ErrEnvironmentNetworkRuntimeUnavailable
	}
	return nil
}

// EnvironmentNetworkRuntimeRegistrar binds and releases a restricted runtime
// capability with the lifetime of its daemon session worker.
type EnvironmentNetworkRuntimeRegistrar interface {
	RegisterEnvironmentNetworkRuntime(
		EnvironmentNetworkRuntimeRegistration,
	) (func(), error)
}

// EnvironmentNetworkRuntimeLease serializes a live network mutation against
// session teardown. Release must be idempotent.
type EnvironmentNetworkRuntimeLease interface {
	EnvironmentID() string
	SessionID() string
	BootID() string
	ReconfigureDNS(context.Context, string, string) error
	VerifyDNS(context.Context, string) error
	Release()
}

// EnvironmentNetworkRuntimeProvider is implemented by the daemon session
// registry. Availability is advisory for review; Acquire is authoritative at
// apply time and closes the plan/apply race.
type EnvironmentNetworkRuntimeProvider interface {
	EnvironmentNetworkRuntimeAvailable(
		context.Context,
		string,
	) error
	AcquireEnvironmentNetworkRuntime(
		context.Context,
		string,
	) (EnvironmentNetworkRuntimeLease, error)
}
