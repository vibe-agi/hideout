package lifecycle

import (
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const testBootID = "01234567-89ab-cdef-0123-456789abcdef"

func TestEvaluateAutoStopExhaustiveSafety(t *testing.T) {
	now := time.Now().UTC()
	incarnation := EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 1, InstanceName: "hideout-test", BootID: testBootID}
	root := testResource(KindBackendIncarnation, "root", 1, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	root.Incarnation = &incarnation
	session := testResource(KindRunSession, "session", 1, StateActive, "daemon", PersistenceEphemeral, ClosePreStopDrain)
	session.Dependencies = []DependencySpec{{Ref: root.Ref, StopMode: StopModePin}}
	drain := testResource(KindNetworkService, "network", 1, StateActive, "manager", PersistenceEphemeral, ClosePreStopDrain)
	drain.Dependencies = []DependencySpec{{Ref: root.Ref, StopMode: StopModeDrain}}
	for mask := 0; mask < 64; mask++ {
		input := EvaluationInput{
			Incarnation: incarnation, Resources: []Resource{root},
			Observation:        backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: incarnation.InstanceName, BootID: incarnation.BootID, ObservedAt: now},
			TransitionInFlight: mask&1 != 0, GraceExpired: mask&2 != 0,
			ReconciliationComplete: mask&4 != 0, CurrentDaemonOwnsAttempt: mask&8 != 0,
		}
		withPin := mask&16 != 0
		withDrain := mask&32 != 0
		if withPin {
			input.Resources = append(input.Resources, session)
		}
		if withDrain {
			input.Resources = append(input.Resources, drain)
		}
		evaluation, err := EvaluateAutoStop(input)
		if err != nil {
			t.Fatal(err)
		}
		want := !input.TransitionInFlight && input.GraceExpired && input.ReconciliationComplete && input.CurrentDaemonOwnsAttempt && !withPin && !withDrain
		if evaluation.Allowed != want {
			t.Fatalf("mask %06b: allowed=%v want=%v reasons=%v", mask, evaluation.Allowed, want, evaluation.Reasons)
		}
	}
}

func TestEvaluateAutoStopOrphanAndGenerationFailClosed(t *testing.T) {
	now := time.Now().UTC()
	incarnation := EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 2, InstanceName: "hideout-test", BootID: testBootID}
	root := testResource(KindBackendIncarnation, "root", 2, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	root.Incarnation = &incarnation
	orphan := testResource(KindRunSession, "session", 2, StateOrphaned, "daemon", PersistenceEphemeral, ClosePreStopDrain)
	orphan.PossibleVMDependency = true
	evaluation, err := EvaluateAutoStop(EvaluationInput{
		Incarnation: incarnation, Resources: []Resource{root, orphan},
		Observation:  backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: incarnation.InstanceName, BootID: incarnation.BootID, ObservedAt: now},
		GraceExpired: true, ReconciliationComplete: true, CurrentDaemonOwnsAttempt: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Allowed || len(evaluation.Pins) != 1 {
		t.Fatalf("orphan did not pin: %+v", evaluation)
	}
	input := EvaluationInput{
		Incarnation: incarnation, Resources: []Resource{root},
		Observation:  backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: incarnation.InstanceName, BootID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ObservedAt: now},
		GraceExpired: true, ReconciliationComplete: true, CurrentDaemonOwnsAttempt: true,
	}
	evaluation, err = EvaluateAutoStop(input)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Allowed {
		t.Fatal("changed boot identity allowed stale stop")
	}
}

func TestValidateGraphRejectsMissingAndDuplicateDependencies(t *testing.T) {
	root := testResource(KindBackendIncarnation, "root", 1, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	incarnation := EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 1, InstanceName: "hideout-test", BootID: testBootID}
	root.Incarnation = &incarnation
	session := testResource(KindRunSession, "session", 1, StateActive, "daemon", PersistenceEphemeral, ClosePreStopDrain)
	session.Dependencies = []DependencySpec{{Ref: root.Ref, StopMode: StopModePin}}
	if err := ValidateGraph([]Resource{session}, true); err == nil {
		t.Fatal("missing dependency accepted")
	}
	if err := ValidateGraph([]Resource{root, root}, true); err == nil {
		t.Fatal("duplicate resource accepted")
	}
}

func TestValidateTransitionKeepsTerminalGenerationsTerminal(t *testing.T) {
	if err := ValidateTransition(StateReleased, StateActive); err == nil {
		t.Fatal("released generation reopened")
	}
	if err := ValidateTransition(StateOrphaned, StateDraining); err != nil {
		t.Fatal(err)
	}
}

func testResource(kind ResourceKind, id string, generation uint64, state ResourceState, owner string, persistence PersistenceClass, closePolicy ClosePolicy) Resource {
	return Resource{
		Ref:   ResourceRef{Kind: kind, ID: id, Generation: generation},
		Owner: OwnerRef{Kind: owner, ID: owner + "-owner", Generation: 1},
		State: state, Persistence: persistence, ClosePolicy: closePolicy,
		UpdatedAt: time.Now().UTC(),
	}
}
