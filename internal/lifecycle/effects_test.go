package lifecycle

import (
	"context"
	"fmt"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestIndependentEffectsAndRetainedRecordsNeverPinBackend(t *testing.T) {
	var clock *testClock
	coordinator, createdClock := newTestCoordinator(t, true, func(_ context.Context, request StopRequest) (StopResult, error) {
		return StopResult{Observation: backend.LifecycleObservation{
			State: backend.LifecycleStopped, InstanceName: request.Incarnation.InstanceName, ObservedAt: clockTime(),
		}}, nil
	})
	clock = createdClock
	registration := prepareIdleRegistration(t, coordinator)
	for _, spec := range []FactSpec{
		{Kind: KindHostAppHandoff, ID: "handoff-one", Class: FactHandoff},
		{Kind: KindHostFSStaged, ID: "overlay-one", Class: FactRetained},
		{Kind: KindDecisionRecord, ID: "decision-one", Class: FactRetained},
	} {
		if err := registration.RecordFact(context.Background(), spec); err != nil {
			t.Fatalf("record %s: %v", spec.Kind, err)
		}
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	status := coordinator.Snapshot()[0]
	if len(status.Pins) != 0 || len(status.Drains) != 0 || len(status.Handoffs) != 1 || len(status.Retained) != 2 {
		t.Fatalf("independent effects were misclassified: %+v", status)
	}
	clock.advance(DefaultIdleGrace)
	status = coordinator.Snapshot()[0]
	if status.Activity != ActivityStopped || len(status.Handoffs) != 1 || len(status.Retained) != 2 {
		t.Fatalf("root stop destroyed independent lifecycle metadata: %+v", status)
	}
}

func TestIndependentRecordsSurviveNewBootAndReconciliation(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	if err := registration.RecordFact(context.Background(), FactSpec{
		Kind: KindHostFSStaged, ID: "overlay-one", Class: FactRetained,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	request := testAttachRequest(backend.LifecycleRunning, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	request.SessionID = "ses-next"
	next, err := coordinator.BeginAttach(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if next.Incarnation().StartGeneration != 2 {
		t.Fatalf("new boot generation=%d", next.Incarnation().StartGeneration)
	}
	if err := next.Commit(context.Background()); err != nil {
		t.Fatalf("persist new generation with historical fact: %v", err)
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatalf("load new generation journal: %v", err)
	}
	if len(journal.Facts) != 1 || journal.Facts[0].Generation != 1 || journal.StartGeneration != 2 {
		t.Fatalf("historical fact was not retained with its original generation: %+v", journal)
	}
	status := coordinator.Snapshot()[0]
	if len(status.Retained) != 1 || status.Retained[0].ID != "overlay-one" {
		t.Fatalf("retained record disappeared across boot: %+v", status)
	}
}

func TestLifecycleFactsAreBoundedAndNeverConsumeResourceCapacity(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration := prepareIdleRegistration(t, coordinator)
	for index := 0; index < maxJournalResources+maxJournalFacts+10; index++ {
		if err := registration.RecordFact(context.Background(), FactSpec{
			Kind: KindDecisionRecord, ID: fmt.Sprintf("decision-%d", index), Class: FactRetained,
		}); err != nil {
			t.Fatalf("fact %d: %v", index, err)
		}
	}
	journal, err := coordinator.store.Load("env-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Facts) != maxJournalFacts || len(journal.Resources) != 2 {
		t.Fatalf("facts=%d resources=%d", len(journal.Facts), len(journal.Resources))
	}
}
