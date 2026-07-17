package lifecycle

import "testing"

func TestCoordinatorTwoClientTwoIncarnationSequenceExplorer(t *testing.T) {
	report, err := RunBoundedModelCheck()
	if err != nil {
		t.Fatal(err)
	}
	if report.ExhaustiveSequences != modelExhaustiveSequenceCount || report.ExploredTransitions == 0 ||
		len(report.CoveredEvents) != len(requiredModelEvents) {
		t.Fatalf("incomplete model report: %+v", report)
	}
}
