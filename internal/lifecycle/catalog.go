package lifecycle

import (
	"errors"
	"slices"
	"sort"
)

const (
	KindBackendIncarnation          ResourceKind = "backend.incarnation"
	KindRunSession                  ResourceKind = "run.session"
	KindGuestSupervisor             ResourceKind = "guest.supervisor"
	KindGuestTarget                 ResourceKind = "guest.target"
	KindBrokerListener              ResourceKind = "broker.listener"
	KindHostFSReadProvider          ResourceKind = "hostfs.read-provider"
	KindHostFSLiveGrant             ResourceKind = "hostfs.live-read-grant"
	KindWorkspaceHostProvider       ResourceKind = "workspace.host-provider"
	KindWorkspaceGuestView          ResourceKind = "workspace.guest-view"
	KindWorkspaceEnvironmentService ResourceKind = "workspace.environment-service"
	KindNetworkService              ResourceKind = "network.environment-service"
	KindRunBridge                   ResourceKind = "endpoint.run-bridge"
	KindHostAppHandoff              ResourceKind = "hostapp.handoff"
	KindHostFSStaged                ResourceKind = "hostfs.staged-object"
	KindDecisionRecord              ResourceKind = "decision.record"
	KindAuditEvent                  ResourceKind = "audit.event"
	KindMaterializationSnapshot     ResourceKind = "host.materialization.snapshot"
	KindMaterializationLive         ResourceKind = "host.materialization.live-projection"
)

type DependencyRule struct {
	Kind  ResourceKind
	Modes []StopMode
}

type Descriptor struct {
	Kind                ResourceKind
	Status              KindStatus
	OwnerKinds          []string
	Dependencies        []DependencyRule
	Persistence         []PersistenceClass
	ClosePolicies       []ClosePolicy
	RecoveryProbe       RecoveryProbe
	PublicLabel         string
	ProductionRegistrar bool
}

// RecoveryProbe names a closed restart-classification contract. A probe does
// not grant authority or re-adopt a resource; it only supplies enough current
// evidence to classify old lifecycle metadata conservatively.
type RecoveryProbe string

const (
	RecoveryBackendObservation RecoveryProbe = "backend-observation"
	RecoverySessionAbsence     RecoveryProbe = "session-absence"
	RecoveryWorkspaceProvider  RecoveryProbe = "workspace-provider-absence"
	RecoveryWorkspaceView      RecoveryProbe = "workspace-view-absence"
	RecoveryNetworkRuntime     RecoveryProbe = "network-runtime"
)

type FactDescriptor struct {
	Kind  ResourceKind
	Class FactClass
	Label string
}

var productionCatalog = []Descriptor{
	{KindBackendIncarnation, KindImplemented, []string{"daemon"}, nil, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{CloseCoTerminateWithRoot}, RecoveryBackendObservation, "backend incarnation", true},
	{KindRunSession, KindImplemented, []string{"daemon"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModePin}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain, CloseCoTerminateWithRoot}, RecoverySessionAbsence, "run session", true},
	{KindGuestSupervisor, KindImplemented, []string{"session"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModeDrain}}, {KindRunSession, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain, CloseCoTerminateWithRoot}, RecoverySessionAbsence, "guest supervisor", true},
	{KindGuestTarget, KindImplemented, []string{"session"}, []DependencyRule{{KindGuestSupervisor, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain, CloseCoTerminateWithRoot}, RecoverySessionAbsence, "guest target", true},
	{KindBrokerListener, KindImplemented, []string{"session"}, []DependencyRule{{KindRunSession, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoverySessionAbsence, "broker listener", true},
	{KindHostFSReadProvider, KindImplemented, []string{"session"}, []DependencyRule{{KindRunSession, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoverySessionAbsence, "HostFS read provider", true},
	{KindHostFSLiveGrant, KindImplemented, []string{"manager"}, []DependencyRule{{KindHostFSReadProvider, []StopMode{StopModeDrain}}, {KindRunSession, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoverySessionAbsence, "HostFS live read grant", true},
	{KindWorkspaceHostProvider, KindImplemented, []string{"manager"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoveryWorkspaceProvider, "workspace host provider", true},
	{KindWorkspaceGuestView, KindImplemented, []string{"session"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModeDrain}}, {KindRunSession, []StopMode{StopModeDrain}}, {KindWorkspaceHostProvider, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoveryWorkspaceView, "workspace guest view", true},
	{KindNetworkService, KindImplemented, []string{"manager"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoveryNetworkRuntime, "environment network service", true},
	{KindRunBridge, KindImplemented, []string{"session"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModePin}}, {KindRunSession, []StopMode{StopModeDrain}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, RecoverySessionAbsence, "run endpoint bridge", true},
	{KindMaterializationSnapshot, KindDesignReady, []string{"manager"}, nil, []PersistenceClass{PersistenceRetained}, []ClosePolicy{CloseSurviveRoot}, "", "host materialization snapshot", false},
	{KindMaterializationLive, KindDesignReady, []string{"manager"}, []DependencyRule{{KindBackendIncarnation, []StopMode{StopModePin}}}, []PersistenceClass{PersistenceEphemeral}, []ClosePolicy{ClosePreStopDrain}, "", "live host materialization projection", false},
}

// RecoveryProbes returns the unique probe contracts in dependency order. The
// daemon iterates this list, so adding a new probe without a handler blocks
// reconciliation instead of silently creating a false-green catalog row.
func RecoveryProbes() []RecoveryProbe {
	seen := map[RecoveryProbe]bool{}
	out := make([]RecoveryProbe, 0, 3)
	for _, descriptor := range productionCatalog {
		if descriptor.Status != KindImplemented || !descriptor.ProductionRegistrar || descriptor.RecoveryProbe == "" || seen[descriptor.RecoveryProbe] {
			continue
		}
		seen[descriptor.RecoveryProbe] = true
		out = append(out, descriptor.RecoveryProbe)
	}
	return out
}

func validRecoveryProbe(probe RecoveryProbe) bool {
	switch probe {
	case RecoveryBackendObservation, RecoverySessionAbsence, RecoveryWorkspaceProvider, RecoveryWorkspaceView, RecoveryNetworkRuntime:
		return true
	default:
		return false
	}
}

var productionFactCatalog = []FactDescriptor{
	{Kind: KindHostAppHandoff, Class: FactHandoff, Label: "host application handoff"},
	{Kind: KindHostFSStaged, Class: FactRetained, Label: "HostFS staged object"},
	{Kind: KindDecisionRecord, Class: FactRetained, Label: "decision record"},
}

func Catalog() []Descriptor {
	out := append([]Descriptor(nil), productionCatalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func FactCatalog() []FactDescriptor {
	out := append([]FactDescriptor(nil), productionFactCatalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func Lookup(kind ResourceKind) (Descriptor, bool) {
	for _, descriptor := range productionCatalog {
		if descriptor.Kind == kind {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

func LookupFact(kind ResourceKind) (FactDescriptor, bool) {
	for _, descriptor := range productionFactCatalog {
		if descriptor.Kind == kind {
			return descriptor, true
		}
	}
	return FactDescriptor{}, false
}

func validateFact(fact Fact) error {
	if !idPattern.MatchString(string(fact.Kind)) || !idPattern.MatchString(fact.ID) || fact.Generation == 0 || fact.RecordedAt.IsZero() {
		return errors.New("lifecycle fact identity is invalid")
	}
	descriptor, ok := LookupFact(fact.Kind)
	if !ok || descriptor.Class != fact.Class {
		return errors.New("lifecycle fact does not match the closed fact catalog")
	}
	return nil
}

func validateResource(resource Resource, production bool) error {
	if err := resource.Ref.Validate(); err != nil {
		return err
	}
	if err := resource.Owner.Validate(); err != nil {
		return err
	}
	descriptor, ok := Lookup(resource.Ref.Kind)
	if !ok {
		return errors.New("unknown lifecycle resource kind")
	}
	if production && (descriptor.Status != KindImplemented || !descriptor.ProductionRegistrar) {
		return errors.New("resource kind is not implemented in production")
	}
	if !slices.Contains(descriptor.OwnerKinds, resource.Owner.Kind) || !slices.Contains(descriptor.Persistence, resource.Persistence) || !slices.Contains(descriptor.ClosePolicies, resource.ClosePolicy) {
		return errors.New("resource does not match its catalog descriptor")
	}
	if !validState(resource.State) {
		return errors.New("resource state is invalid")
	}
	dependencyKinds := map[ResourceKind]bool{}
	for _, dependency := range resource.Dependencies {
		if err := dependency.Ref.Validate(); err != nil {
			return err
		}
		if dependencyKinds[dependency.Ref.Kind] {
			return errors.New("resource repeats a catalog dependency kind")
		}
		dependencyKinds[dependency.Ref.Kind] = true
		allowed := false
		for _, rule := range descriptor.Dependencies {
			if rule.Kind == dependency.Ref.Kind && slices.Contains(rule.Modes, dependency.StopMode) {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("resource dependency is not allowed by catalog")
		}
	}
	// Implemented live kinds have one closed dependency shape. An omitted edge
	// must not make a VM-dependent effect disappear from the stop closure. A
	// post-crash orphan may lack its old root edge after that root is independently
	// proved gone; PossibleVMDependency keeps that discovery row fail closed.
	if resource.State != StateOrphaned {
		for _, rule := range descriptor.Dependencies {
			if !dependencyKinds[rule.Kind] {
				return errors.New("resource is missing a required catalog dependency")
			}
		}
	}
	if resource.Ref.Kind == KindBackendIncarnation {
		if resource.Incarnation == nil {
			return errors.New("backend incarnation identity is required")
		}
		if err := resource.Incarnation.Validate(resource.State != StatePlanned); err != nil {
			return err
		}
	}
	return nil
}
