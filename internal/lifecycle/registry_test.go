package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestRegistrarPersistsPlannedDependencyBeforeBootBinding(t *testing.T) {
	coordinator, clock := newTestCoordinator(t, false, nil)
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleStopped, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.Incarnation == nil || journal.Incarnation.BootID != "" || len(journal.Resources) != 2 {
		t.Fatalf("planned attach not durable before boot: %+v", journal)
	}
	if session := findResource(t, journal.Resources, registration.Session()); session.State != StatePlanned || session.Dependencies[0].Ref.Key() != registration.Root().Key() {
		t.Fatalf("planned session dependency mismatch: %+v", session)
	}
	clock.advance(time.Second)
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	journal = waitForJournal(t, coordinator.store, "env-test", func(value Journal) bool { return value.IdleDeadline != nil })
	if journal.IdleDeadline == nil || journal.IdleDeadline.Deadline.Sub(journal.IdleDeadline.ScheduledAt) != DefaultIdleGrace {
		t.Fatalf("idle deadline not scheduled after proved release: %+v", journal.IdleDeadline)
	}
}

func TestCommitPersistsCompletePlannedGraphBeforeAuthority(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindGuestSupervisor, ID: "ses-one", OwnerKind: "session", OwnerID: "ses-one",
		Dependencies: []DependencySpec{
			{Ref: registration.Root(), StopMode: StopModeDrain},
			{Ref: registration.Session(), StopMode: StopModeDrain},
		},
		Persistence: PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindGuestTarget, ID: "ses-one", OwnerKind: "session", OwnerID: "ses-one",
		Dependencies: []DependencySpec{{Ref: supervisor, StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.store.Load("env-test"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncommitted graph became durable: %v", err)
	}
	if err := registration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ResourceRef{registration.Root(), registration.Session(), supervisor, {Kind: KindGuestTarget, ID: "ses-one", Generation: registration.Incarnation().StartGeneration}} {
		if resource := findResource(t, journal.Resources, ref); resource.State != StatePlanned && resource.Ref.Kind != KindBackendIncarnation {
			t.Fatalf("committed graph contains non-planned provider: %+v", resource)
		}
	}
}

func TestRegistrarSharedResourcesReleaseOnlyAfterFinalUser(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := testAttachRequest(backend.LifecycleRunning, testBootID)
	secondRequest.SessionID = "ses-two"
	second, err := coordinator.BeginAttach(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	for _, registration := range []Registration{first, second} {
		if err := registration.BindBoot(context.Background(), testBootID); err != nil {
			t.Fatal(err)
		}
		if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.IdleDeadline != nil {
		t.Fatal("first sibling release started idle grace")
	}
	if err := second.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	journal = waitForJournal(t, coordinator.store, "env-test", func(value Journal) bool { return value.IdleDeadline != nil })
	if journal.IdleDeadline == nil {
		t.Fatal("final sibling release did not start idle grace")
	}
}

func TestActiveAttachObservationRequiresCurrentDaemonLiveSibling(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	if _, ok := coordinator.ActiveAttachObservation(context.Background(), "env-test", "hideout-test"); ok {
		t.Fatal("observation was exposed without a current live sibling")
	}
	registration := prepareIdleRegistration(t, coordinator)
	observation, ok := coordinator.ActiveAttachObservation(context.Background(), "env-test", "hideout-test")
	if !ok || observation.State != backend.LifecycleRunning || observation.BootID != testBootID {
		t.Fatalf("current active observation unavailable: ok=%t observation=%+v", ok, observation)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := coordinator.ActiveAttachObservation(context.Background(), "env-test", "hideout-test"); ok {
		t.Fatal("idle journal state was exposed as current liveness proof")
	}
}

func TestRegistrarPlannedConsumerCanJoinIdenticalActiveSharedProvider(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first := prepareIdleRegistration(t, coordinator)
	secondRequest := testAttachRequest(backend.LifecycleRunning, testBootID)
	secondRequest.SessionID = "ses-two"
	second, err := coordinator.BeginAttach(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := second.Transition(context.Background(), second.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	spec := RegistrationSpec{
		Kind: KindNetworkService, ID: "env-test", OwnerKind: "manager", OwnerID: "env-test",
		State: StateActive, Dependencies: []DependencySpec{{Ref: first.Root(), StopMode: StopModeDrain}},
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	}
	firstRef, err := first.Register(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.State = StatePlanned
	secondRef, err := second.Register(context.Background(), spec)
	if err != nil {
		t.Fatalf("planned consumer could not declare an existing shared provider: %v", err)
	}
	if secondRef.Key() != firstRef.Key() {
		t.Fatalf("shared provider identity changed: first=%+v second=%+v", firstRef, secondRef)
	}
	if err := second.Transition(context.Background(), secondRef, StateActive); err != nil {
		t.Fatalf("joining consumer could not confirm active provider: %v", err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if resource := findResource(t, journal.Resources, firstRef); resource.State != StateActive {
		t.Fatalf("planned join downgraded shared provider: %+v", resource)
	}
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(coordinator.Snapshot()[0].Drains) != 1 {
		t.Fatalf("first consumer released the shared provider: %+v", coordinator.Snapshot()[0])
	}
	if err := second.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrarExplicitSharedReleaseIsIdempotentAtFinish(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first := prepareIdleRegistration(t, coordinator)
	secondRequest := testAttachRequest(backend.LifecycleRunning, testBootID)
	secondRequest.SessionID = "ses-two"
	second, err := coordinator.BeginAttach(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := second.Transition(context.Background(), second.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	spec := RegistrationSpec{
		Kind: KindNetworkService, ID: "env-test", OwnerKind: "manager", OwnerID: "env-test",
		State: StateActive, Dependencies: []DependencySpec{{Ref: first.Root(), StopMode: StopModeDrain}},
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	}
	firstRef, err := first.Register(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.State = StatePlanned
	secondRef, err := second.Register(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Transition(context.Background(), secondRef, StateActive); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(context.Background(), firstRef, nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(context.Background(), firstRef, nil); err != nil {
		t.Fatalf("repeated shared release was not idempotent: %v", err)
	}
	if order := coordinator.releaseOrderForHandle("env-test", "ses-one"); containsRef(order, firstRef) {
		t.Fatalf("explicitly released provider remained in final release order: %v", order)
	}
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatalf("finish repeated an explicitly released shared resource: %v", err)
	}
	if got := coordinator.Snapshot()[0].Drains; len(got) != 1 {
		t.Fatalf("shared provider did not remain for its sibling: %+v", got)
	}
	if err := second.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrarFailedDynamicCommitRollsBackReleaseOrder(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	journalPath := filepath.Join(coordinator.store.Root, journalDirName, "env-test", journalFileName)
	previous, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(journalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = registration.Register(context.Background(), RegistrationSpec{
		Kind: KindBrokerListener, ID: "ses-one", OwnerKind: "session", OwnerID: "ses-one",
		Dependencies: []DependencySpec{{Ref: registration.Session(), StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err == nil {
		t.Fatal("dynamic registration unexpectedly survived journal write failure")
	}
	if err := os.Remove(journalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatalf("rolled-back dynamic registration poisoned final release: %v", err)
	}
}

func TestRegistrarFailedFactCommitRollsBackInMemoryFact(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	journalPath := filepath.Join(coordinator.store.Root, journalDirName, "env-test", journalFileName)
	previous, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(journalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	err = registration.RecordFact(context.Background(), FactSpec{
		Kind: KindDecisionRecord, ID: "dec-failed-write", Class: FactRetained,
	})
	if err == nil {
		t.Fatal("fact registration unexpectedly survived journal write failure")
	}
	if err := os.Remove(journalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range journal.Facts {
		if fact.ID == "dec-failed-write" {
			t.Fatalf("failed fact commit was revived by a later persistence: %+v", fact)
		}
	}
}

func TestRegistrarCleanupFailureBecomesOrphanAndBlocksIdle(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.BindBoot(context.Background(), testBootID); err != nil {
		t.Fatal(err)
	}
	if err := registration.Transition(context.Background(), registration.Session(), StateActive); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), os.ErrPermission); err != nil {
		t.Fatal(err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journal.IdleDeadline != nil || journal.Reconciliation.State != "blocked" || journal.Reconciliation.ReasonCode != "cleanup-unproved" {
		t.Fatalf("failed cleanup looked idle: %+v", journal)
	}
	if session := findResource(t, journal.Resources, registration.Session()); session.State != StateOrphaned {
		t.Fatalf("failed cleanup did not remain nonterminal: %+v", session)
	}
	status := coordinator.Snapshot()
	if len(status) != 1 || status[0].ReasonCode != "cleanup-unproved" {
		t.Fatalf("failed cleanup lost its operator recovery classification: %+v", status)
	}
}

func TestDynamicSessionResourcesUseReverseRegistrationOrderAndFinalCleanup(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	provider, err := registration.Register(context.Background(), RegistrationSpec{
		Kind: KindHostFSReadProvider, ID: "ses-one", OwnerKind: "session", OwnerID: "ses-one", State: StateActive,
		Dependencies: []DependencySpec{{Ref: registration.Session(), StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := coordinator.RegisterSessionResource(context.Background(), "ses-one", RegistrationSpec{
		Kind: KindHostFSLiveGrant, ID: "dec-hfr-one", OwnerKind: "manager", OwnerID: "ses-one", State: StateStarting,
		Dependencies: []DependencySpec{
			{Ref: ResourceRef{Kind: KindHostFSReadProvider, ID: "ses-one"}, StopMode: StopModeDrain},
			{Ref: ResourceRef{Kind: KindRunSession, ID: "ses-one"}, StopMode: StopModeDrain},
		},
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Generation != provider.Generation {
		t.Fatalf("dynamic generation=%d provider=%d", grant.Generation, provider.Generation)
	}
	if err := coordinator.TransitionSessionResource(context.Background(), "ses-one", grant, StateActive); err != nil {
		t.Fatal(err)
	}
	order := coordinator.releaseOrderForHandle("env-test", "ses-one")
	positions := map[ResourceKind]int{}
	for index, ref := range order {
		positions[ref.Kind] = index
	}
	if !(positions[KindHostFSLiveGrant] < positions[KindHostFSReadProvider] && positions[KindHostFSReadProvider] < positions[KindRunSession]) {
		t.Fatalf("release order is not reverse-topological: %v", order)
	}
	if err := coordinator.RecordSessionFact(context.Background(), "ses-one", FactSpec{
		Kind: KindDecisionRecord, ID: "dec-hfr-one", Class: FactRetained,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatalf("reverse dependency cleanup failed: %v", err)
	}
	journal := waitForJournal(t, coordinator.store, "env-test", func(value Journal) bool {
		for _, resource := range value.Resources {
			if resource.Ref.Kind == KindHostFSLiveGrant || resource.Ref.Kind == KindHostFSReadProvider || resource.Ref.Kind == KindRunSession {
				return false
			}
		}
		return true
	})
	for _, resource := range journal.Resources {
		if resource.Ref.Kind == KindHostFSLiveGrant || resource.Ref.Kind == KindHostFSReadProvider || resource.Ref.Kind == KindRunSession {
			t.Fatalf("session dependency survived final cleanup: %+v", resource)
		}
	}
	if len(coordinator.Snapshot()[0].Retained) != 1 {
		t.Fatalf("retained decision was not preserved: %+v", coordinator.Snapshot()[0])
	}
}

func TestFinishSealsDynamicRegistrationBeforeTakingReleaseSnapshot(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	reg := prepareIdleRegistration(t, coordinator)
	concrete, ok := reg.(*registration)
	if !ok {
		t.Fatalf("registration type=%T", reg)
	}
	refs, err := coordinator.beginFinishRegistration(concrete.environment, concrete.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.RegisterSessionResource(context.Background(), concrete.id, RegistrationSpec{
		Kind: KindHostFSReadProvider, ID: concrete.id,
		OwnerKind: "session", OwnerID: concrete.id, State: StateActive,
		Dependencies: []DependencySpec{{Ref: concrete.session, StopMode: StopModeDrain}},
		Persistence:  PersistenceEphemeral, ClosePolicy: ClosePreStopDrain, PossibleVMDependency: true,
	}); err == nil {
		t.Fatal("dynamic resource registered after final release snapshot was sealed")
	}
	for _, ref := range refs {
		if err := coordinator.release(concrete.environment, concrete.id, ref, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := coordinator.finishRegistration(concrete.environment, concrete.id, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAttachRejectsObservationForDifferentInstance(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	request := testAttachRequest(backend.LifecycleRunning, testBootID)
	request.Observation.InstanceName = "hideout-other"
	if _, err := coordinator.BeginAttach(context.Background(), request); !errors.Is(err, ErrAttachBlocked) {
		t.Fatalf("cross-instance observation was accepted: %v", err)
	}
}

func TestAttachDoesNotReplaceIncarnationUnderActiveSession(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	first := prepareIdleRegistration(t, coordinator)
	journalBefore, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	request := testAttachRequest(backend.LifecycleRunning, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	request.SessionID = "ses-two"
	if _, err := coordinator.BeginAttach(context.Background(), request); !errors.Is(err, ErrAttachBlocked) {
		t.Fatalf("new boot replaced an active incarnation: %v", err)
	}
	journalAfter, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if journalAfter.StartGeneration != journalBefore.StartGeneration || journalAfter.Incarnation == nil || journalAfter.Incarnation.BootID != testBootID {
		t.Fatalf("blocked attach mutated current incarnation: before=%+v after=%+v", journalBefore.Incarnation, journalAfter.Incarnation)
	}
	if resource := findResource(t, journalAfter.Resources, first.Session()); resource.State != StateActive && resource.State != StatePlanned {
		t.Fatalf("blocked attach erased the active session: %+v", resource)
	}
	if err := first.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	next, err := coordinator.BeginAttach(context.Background(), request)
	if err != nil {
		t.Fatalf("new incarnation remained blocked after old session release: %v", err)
	}
	if next.Incarnation().StartGeneration <= first.Incarnation().StartGeneration {
		t.Fatalf("new boot reused old generation: old=%d new=%d", first.Incarnation().StartGeneration, next.Incarnation().StartGeneration)
	}
}

func findResource(t *testing.T, resources []Resource, ref ResourceRef) Resource {
	t.Helper()
	for _, resource := range resources {
		if resource.Ref.Key() == ref.Key() {
			return resource
		}
	}
	t.Fatalf("resource %s not found", ref.Key())
	return Resource{}
}
