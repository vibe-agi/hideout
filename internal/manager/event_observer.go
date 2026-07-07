package manager

// EventObserver receives control-plane operation-lifecycle notifications from Core
// operations. It is nil in embedded construction (New), so embedded mode emits
// nothing and behaves exactly as before; the daemon (internal/daemon) sets it to
// its live event publisher. Observers must be non-blocking and must never mutate
// operation state (FR-006, FR-007).
type EventObserver interface {
	OperationEvent(kind, phase string, details map[string]any)
}

// emitOperation notifies the observer if one is set. It is a no-op in embedded
// mode.
func (c Core) emitOperation(kind, phase string, details map[string]any) {
	if c.Observer != nil {
		c.Observer.OperationEvent(kind, phase, details)
	}
}
