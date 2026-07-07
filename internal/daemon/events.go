package daemon

import (
	"sync"

	"github.com/vibe-agi/hideout/internal/audit"
)

// Event is one item in the live stream (schemas/daemon-event.schema.json). It is
// non-durable: the bus keeps no history and a restart replays nothing.
type Event struct {
	Version string         `json:"version"`
	Kind    string         `json:"kind"`
	Phase   string         `json:"phase,omitempty"`
	Seq     int            `json:"seq"`
	Payload map[string]any `json:"payload,omitempty"`
}

type subscriber struct {
	ch   chan Event
	done chan struct{}
	once sync.Once
}

func (s *subscriber) terminate() { s.once.Do(func() { close(s.done) }) }

// eventBus is the daemon-owned live fan-out. It derives events from operation
// lifecycle (via manager.EventObserver) and the daemon audit tail, applies the
// deterministic control-plane redaction to every payload, keeps no durable log,
// and bounds each subscriber so a slow consumer is dropped with a terminal signal
// rather than stalling operations.
type eventBus struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	seq    int
	closed bool
}

func newEventBus() *eventBus {
	return &eventBus{subs: map[*subscriber]struct{}{}}
}

// OperationEvent implements manager.EventObserver.
func (b *eventBus) OperationEvent(kind, phase string, details map[string]any) {
	b.publish(kind, phase, audit.RedactDetails(details))
}

// publishAudit republishes a daemon audit record as a redacted audit event.
func (b *eventBus) publishAudit(action, decision string, details map[string]any) {
	payload := map[string]any{"action": action, "decision": decision}
	for k, v := range audit.RedactDetails(details) {
		payload[k] = v
	}
	b.publish("audit", "", payload)
}

// publish fans an event out to current subscribers. A subscriber whose bounded
// buffer is full is dropped and terminated (backpressure), never blocked on.
func (b *eventBus) publish(kind, phase string, payload map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.seq++
	ev := Event{Version: eventVersion, Kind: kind, Phase: phase, Seq: b.seq, Payload: payload}
	for s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			delete(b.subs, s)
			s.terminate()
		}
	}
}

func (b *eventBus) subscribe(buffer int) *subscriber {
	s := &subscriber{ch: make(chan Event, buffer), done: make(chan struct{})}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		s.terminate()
		return s
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (b *eventBus) unsubscribe(s *subscriber) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
	s.terminate()
}

// closeAll terminates every subscriber and stops the bus (daemon shutdown).
func (b *eventBus) closeAll() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
		delete(b.subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.terminate()
	}
}
