package daemon

import "github.com/vibe-agi/hideout/internal/audit"

// auditLog is the persistent, session-unbound daemon-local audit log. It reuses
// the deterministic control-plane redaction of internal/audit (RedactDetails is
// applied at emit) and is append-only across restarts (audit.NewFile opens
// O_APPEND). It records lifecycle and authentication events and never stores
// client-supplied token material.
type auditLog struct {
	w *audit.Writer
	// publish, when set by the daemon, republishes each record onto the live event
	// stream as a redacted audit event.
	publish func(action, decision string, details map[string]any)
}

func openAuditLog(path string) (*auditLog, error) {
	w, err := audit.NewFile(path)
	if err != nil {
		return nil, err
	}
	return &auditLog{w: w}, nil
}

// record emits a daemon audit event. It is session-unbound (no session id) and
// tagged to the synthetic "daemon" profile. Details pass through RedactDetails at
// emit, so no control-plane secret can appear; callers must never place token
// material in details.
func (l *auditLog) record(action, decision string, details map[string]any) {
	if l == nil || l.w == nil {
		return
	}
	_ = l.w.Emit(audit.Event{
		Profile:  "daemon",
		Backend:  "native",
		Action:   action,
		Decision: decision,
		Details:  details,
	})
	if l.publish != nil {
		l.publish(action, decision, details)
	}
}

func (l *auditLog) close() error {
	if l == nil || l.w == nil {
		return nil
	}
	return l.w.Close()
}
