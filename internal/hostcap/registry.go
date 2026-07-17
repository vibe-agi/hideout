package hostcap

import (
	"fmt"
	"sort"
)

// Capability ids.
const (
	CapabilityAppOpenResource = "host.app.open-resource"
	// Design-ready only; present to prove the registry accommodates the full
	// vision. MUST fail closed if dispatched in v1.
	CapabilityServiceBridge    = "host.service.bridge"    // adb / device services
	CapabilityAutomationInvoke = "host.automation.invoke" // AppleScript templates
)

// Provider reference ids (map to Go providers selected by the dispatch layer).
const (
	ProviderAppOpenResource = "provider.host.app.open-resource"
	ProviderDesignReady     = "provider.design-ready"
)

// IntentSchema ids.
const (
	IntentSchemaOpenResourceV2 = "open-resource-intent/v2"
)

// registry is the static, package-owned set of capability descriptors. It is
// intentionally a package var initialized once; there is no exported function
// to add to it at runtime.
var registry = []CapabilityDescriptor{
	{
		ID:              CapabilityAppOpenResource,
		RiskClass:       RiskLow, // safe mode; trusted-host-ide elevates at dispatch
		IntentSchema:    IntentSchemaOpenResourceV2,
		ResourceKinds:   []ResourceKind{KindWorkspace, KindHostFS},
		ResultPolicy:    ResultNone,
		ResidualPolicy:  ResidualExternalUnmanaged,
		ProviderRef:     ProviderAppOpenResource,
		DecisionPolicy:  DecisionDefaultAllowAudited,
		LifecyclePolicy: LifecycleSession,
		Platforms:       []Platform{PlatformDarwin},
		Status:          StatusImplemented,
	},
	{
		ID:              CapabilityServiceBridge,
		RiskClass:       RiskHigh,
		IntentSchema:    "service-bridge-intent/v1",
		ResourceKinds:   []ResourceKind{KindDevice, KindEndpoint},
		ResultPolicy:    ResultLease,
		ResidualPolicy:  ResidualManaged,
		ProviderRef:     ProviderDesignReady,
		DecisionPolicy:  DecisionOperatorGrant,
		LifecyclePolicy: LifecycleSession,
		Platforms:       []Platform{PlatformDarwin, PlatformLinux},
		Status:          StatusDesignReady,
	},
	{
		ID:              CapabilityAutomationInvoke,
		RiskClass:       RiskHigh,
		IntentSchema:    "automation-invoke-intent/v1",
		ResourceKinds:   []ResourceKind{},
		ResultPolicy:    ResultBoundedTyped,
		ResidualPolicy:  ResidualNone,
		ProviderRef:     ProviderDesignReady,
		DecisionPolicy:  DecisionOperatorGrant,
		LifecyclePolicy: LifecycleSession,
		Platforms:       []Platform{PlatformDarwin},
		Status:          StatusDesignReady,
	},
}

// knownProviders is the set of provider refs the dispatch layer can resolve.
// A descriptor whose ProviderRef is not here fails registry validation.
var knownProviders = map[string]bool{
	ProviderAppOpenResource: true,
	ProviderDesignReady:     true,
}

// Registry returns a copy of the static descriptor set, sorted by id.
func Registry() []CapabilityDescriptor {
	out := append([]CapabilityDescriptor(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup returns the descriptor for a capability id.
func Lookup(id string) (CapabilityDescriptor, bool) {
	for _, d := range registry {
		if d.ID == id {
			return d, true
		}
	}
	return CapabilityDescriptor{}, false
}

// Validate checks registry integrity. Called by a package-level test and safe
// to call at startup. Fails closed on any malformed descriptor.
func Validate() error {
	seen := map[string]bool{}
	for i, d := range registry {
		if d.ID == "" {
			return fmt.Errorf("descriptor[%d]: id is required", i)
		}
		if seen[d.ID] {
			return fmt.Errorf("descriptor %q: duplicate id", d.ID)
		}
		seen[d.ID] = true
		if !validRiskClass(d.RiskClass) {
			return fmt.Errorf("descriptor %q: unknown riskClass %q", d.ID, d.RiskClass)
		}
		if !validResultPolicy(d.ResultPolicy) {
			return fmt.Errorf("descriptor %q: unknown resultPolicy %q", d.ID, d.ResultPolicy)
		}
		if !validResidualPolicy(d.ResidualPolicy) {
			return fmt.Errorf("descriptor %q: unknown residualPolicy %q", d.ID, d.ResidualPolicy)
		}
		if d.ResultPolicy == ResultNone && d.ResidualPolicy == ResidualManaged {
			return fmt.Errorf("descriptor %q: result-none cannot leave a managed residual", d.ID)
		}
		if d.ResidualPolicy == ResidualExternalUnmanaged && d.ResultPolicy != ResultNone {
			return fmt.Errorf("descriptor %q: external-unmanaged handoff must be result-none", d.ID)
		}
		if !validDecisionPolicy(d.DecisionPolicy) {
			return fmt.Errorf("descriptor %q: unknown decisionPolicy %q", d.ID, d.DecisionPolicy)
		}
		if !validLifecyclePolicy(d.LifecyclePolicy) {
			return fmt.Errorf("descriptor %q: unknown lifecyclePolicy %q", d.ID, d.LifecyclePolicy)
		}
		if !validStatus(d.Status) {
			return fmt.Errorf("descriptor %q: unknown status %q", d.ID, d.Status)
		}
		if d.IntentSchema == "" {
			return fmt.Errorf("descriptor %q: intentSchema is required", d.ID)
		}
		if !knownProviders[d.ProviderRef] {
			return fmt.Errorf("descriptor %q: unresolved providerRef %q", d.ID, d.ProviderRef)
		}
		for _, k := range d.ResourceKinds {
			if !validResourceKind(k) {
				return fmt.Errorf("descriptor %q: unknown resourceKind %q", d.ID, k)
			}
		}
		if len(d.Platforms) == 0 {
			return fmt.Errorf("descriptor %q: at least one platform is required", d.ID)
		}
		for _, p := range d.Platforms {
			if !validPlatform(p) {
				return fmt.Errorf("descriptor %q: unknown platform %q", d.ID, p)
			}
		}
	}
	return nil
}

// EnsureExternalUnmanagedHandoff proves that a fire-and-forget provider cannot
// hide a managed bridge, worker, endpoint, or guest process behind ResultNone.
func EnsureExternalUnmanagedHandoff(id string) error {
	d, err := EnsureImplemented(id)
	if err != nil {
		return err
	}
	if d.ResultPolicy != ResultNone || d.ResidualPolicy != ResidualExternalUnmanaged {
		return fmt.Errorf("capability %q is not an external-unmanaged result-none handoff", id)
	}
	return nil
}

// EnsureImplemented returns the descriptor for id only if it is implemented in
// this version. A design-ready capability reached at dispatch fails closed.
func EnsureImplemented(id string) (CapabilityDescriptor, error) {
	d, ok := Lookup(id)
	if !ok {
		return CapabilityDescriptor{}, &Error{Code: CodeCommandUnbound, Reason: fmt.Sprintf("capability %q is not registered", id)}
	}
	if d.Status != StatusImplemented {
		return CapabilityDescriptor{}, &Error{Code: CodeCapabilityDesignReady, Reason: fmt.Sprintf("capability %q is design-ready and cannot be dispatched", id)}
	}
	return d, nil
}
