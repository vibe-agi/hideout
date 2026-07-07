package manager

import (
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
)

// T025: the Core event-observer seam is nil in embedded construction and emitting
// is a no-op, so embedded mode is unchanged (FR-006).
func TestCoreObserverNilInEmbeddedConstruction(t *testing.T) {
	c := New(profile.Store{Root: t.TempDir()})
	if c.Observer != nil {
		t.Fatalf("embedded Core.Observer must be nil, got %T", c.Observer)
	}
	// emitOperation must not panic when the observer is nil.
	c.emitOperation("environment", "start", map[string]any{"action": "clean"})
}

type recordingObserver struct {
	events []string
}

func (o *recordingObserver) OperationEvent(kind, phase string, _ map[string]any) {
	o.events = append(o.events, kind+":"+phase)
}

// T025: when an observer is set, Core emits through it.
func TestCoreObserverReceivesOperationEvents(t *testing.T) {
	obs := &recordingObserver{}
	c := New(profile.Store{Root: t.TempDir()})
	c.Observer = obs
	c.emitOperation("environment", "complete", nil)
	if len(obs.events) != 1 || obs.events[0] != "environment:complete" {
		t.Fatalf("observer did not receive the event: %v", obs.events)
	}
}
