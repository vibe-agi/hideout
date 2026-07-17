package daemon

import (
	"fmt"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/liveconsole"
)

// Event is one item in the live stream (schemas/daemon-event.schema.json).
// It is non-durable: the bus keeps no history and a restart replays nothing.
type Event = liveconsole.Event

type subscriber struct {
	ch   chan Event
	done chan struct{}
	once sync.Once
	seq  int
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
	details = audit.RedactDetails(details)
	switch kind {
	case liveconsole.KindEnvironment:
		payload := environmentPayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindEnvironment,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindEnvironment, ID: payload.ID, Profile: payload.Profile},
			Payload: payload,
		})
	case liveconsole.KindBackground:
		payload := backgroundPayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindBackground,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindBackground, ID: payload.ID},
			Payload: payload,
		})
	case liveconsole.KindExport:
		payload := exportPayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindExport,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindExport, ID: payload.ID, Profile: payload.Profile},
			Payload: payload,
		})
	case liveconsole.KindCleanup:
		payload := cleanupPayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindCleanup,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindCleanup, ID: payload.ID},
			Payload: payload,
		})
	case liveconsole.KindHostFSWrite:
		payload := hostFSWritePayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindHostFSWrite,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindHostFSWrite, ID: payload.DecisionID, Profile: payload.Profile, Session: payload.Session},
			Payload: payload,
		})
	case liveconsole.KindDecision:
		payload := decisionPayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindDecision,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindDecision, ID: payload.DecisionID, Profile: payload.Profile, Session: payload.Session},
			Payload: payload,
		})
	case liveconsole.KindNotice:
		payload := noticePayload(details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindNotice,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindNotice, ID: payload.NoticeID, Profile: payload.Profile, Session: payload.Session},
			Payload: payload,
		})
	case "host-app":
		b.publishAuditEvent(audit.Event{
			Time:     time.Now().UTC(),
			Profile:  stringValue(details, "profile"),
			Backend:  "native",
			Action:   stringValue(details, "action"),
			Decision: stringValue(details, "decision"),
			Details:  details,
		})
	case liveconsole.KindSession, "run", "operation":
		payload := sessionPayload(kind, details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindSession,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindSession, ID: payload.ID, Profile: payload.Profile, Session: payload.Session},
			Payload: payload,
		})
	default:
		payload := sessionPayload(kind, details, phase)
		b.publish(Event{
			Kind:    liveconsole.KindSession,
			Phase:   phase,
			Entity:  liveconsole.EntityRef{Kind: liveconsole.KindSession, ID: payload.ID, Profile: payload.Profile, Session: payload.Session},
			Payload: payload,
		})
	}
}

// publishAudit republishes a daemon audit record as a redacted audit event.
func (b *eventBus) publishAudit(action, decision string, details map[string]any) {
	b.publishAuditEvent(audit.Event{
		Time:     time.Now().UTC(),
		Profile:  "daemon",
		Backend:  "native",
		Action:   action,
		Decision: decision,
		Details:  details,
	})
}

func (b *eventBus) publishAuditEvent(ev audit.Event) {
	payload := liveconsole.EventPayload{
		Time:     ev.Time,
		Session:  ev.Session,
		Profile:  ev.Profile,
		Backend:  ev.Backend,
		Action:   ev.Action,
		Decision: ev.Decision,
		Details:  audit.RedactDetails(ev.Details),
	}
	if payload.Profile == "" {
		payload.Profile = "daemon"
	}
	if payload.Backend == "" {
		payload.Backend = "native"
	}
	if payload.Time.IsZero() {
		payload.Time = time.Now().UTC()
	}
	b.publish(Event{
		Kind:    liveconsole.KindAudit,
		Entity:  liveconsole.EntityRef{Kind: liveconsole.KindAudit, Profile: payload.Profile, Session: payload.Session},
		Payload: payload,
	})
}

func (b *eventBus) publishBackground(id, op, status string) {
	b.publish(Event{
		Kind:   liveconsole.KindBackground,
		Entity: liveconsole.EntityRef{Kind: liveconsole.KindBackground, ID: id},
		Payload: liveconsole.EventPayload{
			ID:     id,
			Op:     op,
			Status: status,
		},
	})
}

func (b *eventBus) publishLifecycle(status lifecycle.Status, phase string) {
	copy := status
	b.publish(Event{
		Kind:   liveconsole.KindLifecycle,
		Phase:  phase,
		Entity: liveconsole.EntityRef{Kind: liveconsole.KindLifecycle, ID: status.EnvironmentID},
		Payload: liveconsole.EventPayload{
			ID:        status.EnvironmentID,
			Status:    string(status.Activity),
			Lifecycle: &copy,
		},
	})
}

// publish fans an event out to current subscribers. A subscriber whose bounded
// buffer is full is dropped and terminated (backpressure), never blocked on.
func (b *eventBus) publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.seq++
	if ev.Version == "" {
		ev.Version = liveconsole.EventVersion
	}
	ev.Seq = b.seq
	if ev.Entity.Kind == "" && ev.Kind != liveconsole.KindTerminal {
		ev.Entity.Kind = ev.Kind
	}
	for s := range b.subs {
		select {
		case s.ch <- ev:
			s.seq = ev.Seq
		default:
			delete(b.subs, s)
			s.terminate()
		}
	}
}

func (b *eventBus) terminalEvent(s *subscriber, reason string) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	seq := 1
	if s != nil && s.seq > 0 {
		seq = s.seq + 1
	}
	return Event{
		Version: liveconsole.EventVersion,
		Kind:    liveconsole.KindTerminal,
		Seq:     seq,
		Entity:  liveconsole.EntityRef{Kind: "stream"},
		Payload: liveconsole.EventPayload{Reason: reason},
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

func environmentPayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:             firstString(details, "id", "name", "action"),
		Name:           stringValue(details, "name"),
		Profile:        stringValue(details, "profile"),
		Backend:        stringValue(details, "backend"),
		Status:         firstString(details, "status", "reason"),
		Workspace:      stringValue(details, "workspace"),
		GuestWorkspace: stringValue(details, "guestWorkspace"),
		ImageRef:       stringValue(details, "imageRef"),
		InstanceName:   stringValue(details, "instanceName"),
		LastSessionID:  stringValue(details, "lastSessionId"),
		LastCommand:    stringValue(details, "lastCommand"),
		CreatedAt:      timeValue(details, "createdAt"),
		LastStartedAt:  timeValue(details, "lastStartedAt"),
		LastEndedAt:    timeValue(details, "lastEndedAt"),
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.ID == "" {
		payload.ID = "environment"
	}
	return payload
}

func sessionPayload(kind string, details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:                firstString(details, "id", "session", "path", "action"),
		Profile:           stringValue(details, "profile"),
		Backend:           stringValue(details, "backend"),
		Status:            stringValue(details, "status"),
		NetworkMode:       stringValue(details, "networkMode"),
		HasAudit:          boolValue(details, "hasAudit"),
		HasEphemeralState: boolValue(details, "hasEphemeralState"),
		Session:           stringValue(details, "session"),
		Details:           details,
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.ID == "" {
		payload.ID = kind
	}
	return payload
}

func backgroundPayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:     firstString(details, "id", "op"),
		Op:     stringValue(details, "op"),
		Status: stringValue(details, "status"),
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.ID == "" {
		payload.ID = "background"
	}
	if payload.Op == "" {
		payload.Op = payload.ID
	}
	return payload
}

func exportPayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:           firstString(details, "id", "out", "artifactPath", "source"),
		Status:       stringValue(details, "status"),
		Profile:      stringValue(details, "profile"),
		Source:       stringValue(details, "source"),
		ArtifactPath: firstString(details, "artifactPath", "out"),
		Decision:     stringValue(details, "decision"),
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	return payload
}

func cleanupPayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:          firstString(details, "id", "session", "source"),
		Status:      stringValue(details, "status"),
		Sessions:    intValue(details, "sessions"),
		Removed:     stringSliceValue(details, "removed", "removedTypes"),
		SecretState: stringValue(details, "secretState"),
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.ID == "" {
		payload.ID = "cleanup"
	}
	return payload
}

func hostFSWritePayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:              firstString(details, "decisionId", "operationId", "id"),
		OperationID:     stringValue(details, "operationId"),
		DecisionID:      stringValue(details, "decisionId"),
		Profile:         stringValue(details, "profile"),
		Session:         stringValue(details, "session"),
		Backend:         stringValue(details, "backend"),
		Status:          firstString(details, "status", "state"),
		Operation:       firstString(details, "operation", "op"),
		Path:            stringValue(details, "path"),
		DestinationPath: stringValue(details, "destinationPath"),
		PrivilegeStatus: stringValue(details, "privilegeStatus"),
		Reason:          stringValue(details, "reason"),
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.DecisionID == "" {
		payload.DecisionID = payload.ID
	}
	if payload.OperationID == "" {
		payload.OperationID = payload.ID
	}
	return payload
}

func decisionPayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:             firstString(details, "decisionId", "id"),
		DecisionID:     firstString(details, "decisionId", "id"),
		RecordKind:     stringValue(details, "kind"),
		Status:         firstString(details, "status", "state"),
		Profile:        stringValue(details, "profile"),
		Session:        stringValue(details, "session"),
		Backend:        stringValue(details, "backend"),
		DefaultOutcome: stringValue(details, "defaultOutcome"),
		Reason:         stringValue(details, "reason"),
		Preview:        details["preview"],
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.DecisionID == "" {
		payload.DecisionID = "decision"
		payload.ID = payload.DecisionID
	}
	return payload
}

func noticePayload(details map[string]any, phase string) liveconsole.EventPayload {
	payload := liveconsole.EventPayload{
		ID:           firstString(details, "noticeId", "id"),
		NoticeID:     firstString(details, "noticeId", "id"),
		RecordKind:   stringValue(details, "kind"),
		Status:       stringValue(details, "status"),
		Severity:     stringValue(details, "severity"),
		Profile:      stringValue(details, "profile"),
		Session:      stringValue(details, "session"),
		Backend:      stringValue(details, "backend"),
		Acknowledged: boolValue(details, "acknowledged"),
		Preview:      details["preview"],
	}
	if payload.Status == "" {
		payload.Status = statusFromPhase(phase)
	}
	if payload.NoticeID == "" {
		payload.NoticeID = "notice"
		payload.ID = payload.NoticeID
	}
	return payload
}

func statusFromPhase(phase string) string {
	switch phase {
	case "start", "progress":
		return "running"
	case "complete":
		return "completed"
	case "failed":
		return "failed"
	default:
		return phase
	}
}

func firstString(details map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringValue(details, key); v != "" {
			return v
		}
	}
	return ""
}

func stringValue(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	switch v := details[key].(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func boolValue(details map[string]any, key string) bool {
	if details == nil {
		return false
	}
	v, _ := details[key].(bool)
	return v
}

func intValue(details map[string]any, key string) int {
	if details == nil {
		return 0
	}
	switch v := details[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func stringSliceValue(details map[string]any, keys ...string) []string {
	if details == nil {
		return nil
	}
	for _, key := range keys {
		switch v := details[key].(type) {
		case []string:
			return append([]string(nil), v...)
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					out = nil
					break
				}
				out = append(out, s)
			}
			if out != nil {
				return out
			}
		}
	}
	return nil
}

func timeValue(details map[string]any, key string) time.Time {
	if details == nil {
		return time.Time{}
	}
	v, _ := details[key].(time.Time)
	return v
}
