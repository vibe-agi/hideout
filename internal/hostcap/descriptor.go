package hostcap

import "runtime"

// Recovery codes emitted by the projection layer. They follow the
// internal/recovery domain.subject.detail convention and are mirrored there.
const (
	CodeCommandUnbound        = "projection.command.unbound"
	CodeProviderUnavailable   = "projection.provider.unavailable"
	CodePathNoHostMapping     = "projection.path.no-host-mapping"
	CodeAppAbsent             = "projection.app.absent"
	CodeAppIdentityDrift      = "projection.app.identity-drift"
	CodeModeTrustedDenied     = "projection.mode.trusted-denied"
	CodeFlagUnrecognized      = "projection.flag.unrecognized"
	CodeIntentInvalid         = "projection.intent.invalid"
	CodeCapabilityDesignReady = "projection.capability.design-ready"
)

// RiskClass drives the DecisionPolicy for a capability facet.
type RiskClass string

const (
	RiskLow      RiskClass = "low"
	RiskElevated RiskClass = "elevated"
	RiskHigh     RiskClass = "high"
)

// ResultPolicy declares whether and how a host->guest result channel exists.
// A result channel is as dangerous as an argument channel, so it is typed per
// capability. host.app.open-resource is fire-and-forget (none).
type ResultPolicy string

const (
	ResultNone         ResultPolicy = "none"
	ResultBoundedTyped ResultPolicy = "bounded-typed"
	ResultStream       ResultPolicy = "stream"
	ResultLease        ResultPolicy = "lease"
)

// DecisionPolicy: low-risk families are default-allowed and audited (like
// host.open registration); high-risk families require an explicit operator
// grant through the decision center.
type DecisionPolicy string

const (
	DecisionDefaultAllowAudited DecisionPolicy = "default-allow-audited"
	DecisionOperatorGrant       DecisionPolicy = "operator-grant"
)

// LifecyclePolicy binds decisions/grants/leases to a lifetime and revokes at
// its end.
type LifecyclePolicy string

const (
	LifecycleSession LifecyclePolicy = "session"
	LifecycleRun     LifecyclePolicy = "run"
)

// Status marks whether a descriptor is implemented in this version or present
// only to prove the registry accommodates the full vision. design-ready
// descriptors MUST fail closed if dispatched.
type Status string

const (
	StatusImplemented Status = "implemented"
	StatusDesignReady Status = "design-ready"
)

// ResourceKind categorizes what a capability's ResourceRefs may reference.
// host.app.open-resource accepts workspace and already-authorized HostFS
// portal resources.
type ResourceKind string

const (
	KindWorkspace ResourceKind = "workspace"
	KindHostFS    ResourceKind = "hostfs"
	KindGuestOnly ResourceKind = "guest-only"
	KindURL       ResourceKind = "url"
	KindEndpoint  ResourceKind = "endpoint"
	KindDevice    ResourceKind = "device"
)

// Platform is a host platform a descriptor is available on.
type Platform string

const (
	PlatformDarwin Platform = "darwin"
	PlatformLinux  Platform = "linux"
)

// CurrentPlatform reports the host platform used for descriptor availability.
func CurrentPlatform() Platform {
	switch runtime.GOOS {
	case "darwin":
		return PlatformDarwin
	case "linux":
		return PlatformLinux
	default:
		return Platform(runtime.GOOS)
	}
}

// CapabilityDescriptor is the Core-owned, static description of one host
// capability. The registry of descriptors is the authority surface; it is not
// runtime-extensible.
type CapabilityDescriptor struct {
	ID              string
	RiskClass       RiskClass
	IntentSchema    string
	ResourceKinds   []ResourceKind
	ResultPolicy    ResultPolicy
	ProviderRef     string
	DecisionPolicy  DecisionPolicy
	LifecyclePolicy LifecyclePolicy
	Platforms       []Platform
	Status          Status
}

func validRiskClass(r RiskClass) bool {
	switch r {
	case RiskLow, RiskElevated, RiskHigh:
		return true
	}
	return false
}

func validResultPolicy(r ResultPolicy) bool {
	switch r {
	case ResultNone, ResultBoundedTyped, ResultStream, ResultLease:
		return true
	}
	return false
}

func validDecisionPolicy(d DecisionPolicy) bool {
	switch d {
	case DecisionDefaultAllowAudited, DecisionOperatorGrant:
		return true
	}
	return false
}

func validLifecyclePolicy(l LifecyclePolicy) bool {
	switch l {
	case LifecycleSession, LifecycleRun:
		return true
	}
	return false
}

func validStatus(s Status) bool {
	switch s {
	case StatusImplemented, StatusDesignReady:
		return true
	}
	return false
}

func validResourceKind(k ResourceKind) bool {
	switch k {
	case KindWorkspace, KindHostFS, KindGuestOnly, KindURL, KindEndpoint, KindDevice:
		return true
	}
	return false
}

func validPlatform(p Platform) bool {
	switch p {
	case PlatformDarwin, PlatformLinux:
		return true
	}
	return false
}
