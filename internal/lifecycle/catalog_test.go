package lifecycle

import (
	"slices"
	"testing"
)

func TestProductionCatalogIsClosedAndProbeBacked(t *testing.T) {
	seen := map[ResourceKind]bool{}
	for _, descriptor := range Catalog() {
		if seen[descriptor.Kind] {
			t.Fatalf("duplicate kind %s", descriptor.Kind)
		}
		seen[descriptor.Kind] = true
		if descriptor.Status == KindImplemented && descriptor.ProductionRegistrar && !validRecoveryProbe(descriptor.RecoveryProbe) {
			t.Fatalf("implemented kind %s has no closed recovery probe: %q", descriptor.Kind, descriptor.RecoveryProbe)
		}
	}
	probes := RecoveryProbes()
	wantProbes := []RecoveryProbe{RecoveryBackendObservation, RecoverySessionAbsence, RecoveryNetworkRuntime}
	if !slices.Equal(probes, wantProbes) {
		t.Fatalf("recovery probes=%v want=%v", probes, wantProbes)
	}
	for _, kind := range []ResourceKind{KindBackendIncarnation, KindRunSession, KindGuestSupervisor, KindGuestTarget, KindBrokerListener, KindHostFSReadProvider, KindHostFSLiveGrant, KindNetworkService, KindRunBridge} {
		if !seen[kind] {
			t.Fatalf("production kind %s is missing", kind)
		}
	}
	facts := map[ResourceKind]FactClass{}
	for _, descriptor := range FactCatalog() {
		facts[descriptor.Kind] = descriptor.Class
	}
	for kind, class := range map[ResourceKind]FactClass{
		KindHostAppHandoff: FactHandoff, KindHostFSStaged: FactRetained, KindDecisionRecord: FactRetained,
	} {
		if facts[kind] != class {
			t.Fatalf("fact %s class=%q want=%q", kind, facts[kind], class)
		}
		if descriptor, ok := Lookup(kind); ok && descriptor.ProductionRegistrar {
			t.Fatalf("non-live fact %s entered the resource graph", kind)
		}
	}
}

func TestDesignReadyKindCannotRegisterInProduction(t *testing.T) {
	for _, resource := range []Resource{
		testResource(KindMaterializationSnapshot, "snapshot", 1, StateActive, "manager", PersistenceRetained, CloseSurviveRoot),
		testResource(KindMaterializationLive, "live", 1, StateActive, "manager", PersistenceEphemeral, ClosePreStopDrain),
	} {
		if resource.Ref.Kind == KindMaterializationLive {
			resource.Dependencies = []DependencySpec{{
				Ref: ResourceRef{Kind: KindBackendIncarnation, ID: "root", Generation: 1}, StopMode: StopModePin,
			}}
		}
		if err := validateResource(resource, true); err == nil {
			t.Fatalf("design-ready kind %s entered production", resource.Ref.Kind)
		}
	}
}

func TestMaterializationShapesKeepGuestLiveProjectionPinned(t *testing.T) {
	root := testResource(KindBackendIncarnation, "root", 1, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	root.Incarnation = &EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 1, InstanceName: "hideout-test", BootID: testBootID}
	snapshot := testResource(KindMaterializationSnapshot, "snapshot", 1, StateActive, "manager", PersistenceRetained, CloseSurviveRoot)
	if err := ValidateGraph([]Resource{snapshot}, false); err != nil {
		t.Fatalf("host-only materialization snapshot was rejected: %v", err)
	}
	live := testResource(KindMaterializationLive, "live", 1, StateActive, "manager", PersistenceEphemeral, ClosePreStopDrain)
	if err := ValidateGraph([]Resource{root, live}, false); err == nil {
		t.Fatal("guest-live materialization without its backend pin was accepted")
	}
	live.Dependencies = []DependencySpec{{Ref: root.Ref, StopMode: StopModePin}}
	if err := ValidateGraph([]Resource{root, live}, false); err != nil {
		t.Fatalf("guest-live materialization with its backend pin was rejected: %v", err)
	}
}

func TestCatalogRejectsUnsupportedDependencyMode(t *testing.T) {
	root := testResource(KindBackendIncarnation, "root", 1, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	root.Incarnation = &EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 1, InstanceName: "hideout-test", BootID: testBootID}
	session := testResource(KindRunSession, "session", 1, StateActive, "daemon", PersistenceEphemeral, ClosePreStopDrain)
	session.Dependencies = []DependencySpec{{Ref: root.Ref, StopMode: StopModeDrain}}
	if err := ValidateGraph([]Resource{root, session}, true); err == nil {
		t.Fatal("session drain edge accepted in place of required pin")
	}
}

func TestCatalogRejectsMissingRequiredDependencyShape(t *testing.T) {
	root := testResource(KindBackendIncarnation, "root", 1, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	root.Incarnation = &EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 1, InstanceName: "hideout-test", BootID: testBootID}
	network := testResource(KindNetworkService, "network", 1, StateActive, "manager", PersistenceEphemeral, ClosePreStopDrain)
	if err := ValidateGraph([]Resource{root, network}, true); err == nil {
		t.Fatal("VM-dependent resource without its required root edge was accepted")
	}
	network.Dependencies = []DependencySpec{
		{Ref: root.Ref, StopMode: StopModeDrain},
		{Ref: root.Ref, StopMode: StopModeDrain},
	}
	if err := ValidateGraph([]Resource{root, network}, true); err == nil {
		t.Fatal("duplicate dependency kind was accepted")
	}
}

func TestCatalogBoundsExplicitRootCoTermination(t *testing.T) {
	for _, kind := range []ResourceKind{KindRunSession, KindGuestSupervisor, KindGuestTarget} {
		descriptor, ok := Lookup(kind)
		if !ok || !slices.Contains(descriptor.ClosePolicies, CloseCoTerminateWithRoot) {
			t.Fatalf("kind %s is missing explicit root co-termination policy", kind)
		}
	}
	for _, kind := range []ResourceKind{KindBrokerListener, KindHostFSReadProvider, KindHostFSLiveGrant, KindNetworkService, KindRunBridge} {
		descriptor, ok := Lookup(kind)
		if !ok {
			t.Fatalf("kind %s is missing", kind)
		}
		if slices.Contains(descriptor.ClosePolicies, CloseCoTerminateWithRoot) {
			t.Fatalf("host-side provider kind %s may be hidden by root co-termination", kind)
		}
	}
}
